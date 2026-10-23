package runtime

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"strings"

	"github.com/eslzzyl/krkrgo/internal/simd"
	"github.com/eslzzyl/krkrgo/internal/tjs"
)

const layerCompositionDepthLimit = 128

func chargeLayerPixels(budget *uint64, pixels uint64) error {
	if budget == nil {
		return nil
	}
	if pixels > *budget {
		return ErrBudgetExceeded
	}
	*budget -= pixels
	return nil
}

func (s *Services) layerPiledCopy(dst *Layer, dstID tjs.ObjectID, args []tjs.Value, budget *uint64, operation string) error {
	if len(args) < 3 {
		return fmt.Errorf("Layer.%s: requires destination position and source Layer", operation)
	}
	if args[2].Kind() != tjs.KindObject || args[2].IsNull() {
		return fmt.Errorf("Layer.%s requires a source Layer", operation)
	}
	srcID := args[2].ObjectRef().Object
	dx, err := layerImageInt(args, 0, operation)
	if err != nil {
		return err
	}
	dy, err := layerImageInt(args, 1, operation)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Layers[dstID] != dst {
		return fmt.Errorf("Layer.%s: destination is not a Layer", operation)
	}
	src := s.Layers[srcID]
	if src == nil {
		return fmt.Errorf("Layer.%s: source is not a Layer", operation)
	}
	readRectArg := func(index int, defaultValue int32) (int32, error) {
		if index >= len(args) || args[index].IsVoid() {
			return defaultValue, nil
		}
		return layerImageInt(args, index, operation)
	}
	var sx, sy, width, height int32
	if operation == "pileRect" {
		sx, err = readRectArg(3, 0)
		if err != nil {
			return err
		}
		sy, err = readRectArg(4, 0)
		if err != nil {
			return err
		}
		width, err = readRectArg(5, dst.Bounds.Width)
		if err != nil {
			return err
		}
		height, err = readRectArg(6, dst.Bounds.Height)
		if err != nil {
			return err
		}
	} else {
		if len(args) < 7 {
			return fmt.Errorf("Layer.piledCopy: missing argument %d", len(args))
		}
		sx, err = layerImageInt(args, 3, operation)
		if err != nil {
			return err
		}
		sy, err = layerImageInt(args, 4, operation)
		if err != nil {
			return err
		}
		width, err = layerImageInt(args, 5, operation)
		if err != nil {
			return err
		}
		height, err = layerImageInt(args, 6, operation)
		if err != nil {
			return err
		}
	}
	if width < 0 || height < 0 {
		return fmt.Errorf("Layer.%s: negative source rectangle", operation)
	}
	if err := validLayerPixels(dst); err != nil {
		return err
	}
	if err := validLayerPixels(src); err != nil {
		return fmt.Errorf("Layer.%s: source layer has no image: %w", operation, err)
	}

	left, top := int64(0), int64(0)
	left = max64(max64(left, -int64(sx)), -int64(dx))
	top = max64(max64(top, -int64(sy)), -int64(dy))
	right := min64(min64(int64(width), int64(src.Bounds.Width)-int64(sx)), int64(dst.deviceWidth())-int64(dx))
	bottom := min64(min64(int64(height), int64(src.Bounds.Height)-int64(sy)), int64(dst.deviceHeight())-int64(dy))
	if dst.clipSet {
		clipRight := int64(dst.clip.X) + int64(dst.clip.Width)
		clipBottom := int64(dst.clip.Y) + int64(dst.clip.Height)
		left = max64(left, int64(dst.clip.X)-int64(dx))
		top = max64(top, int64(dst.clip.Y)-int64(dy))
		right = min64(right, clipRight-int64(dx))
		bottom = min64(bottom, clipBottom-int64(dy))
	}
	if right <= left || bottom <= top {
		return nil
	}
	copyWidth, copyHeight := right-left, bottom-top
	pixels := uint64(copyWidth) * uint64(copyHeight)

	// A simple unoffset leaf can be copied directly. The composed path below
	// snapshots pixels first, which is required when source and destination alias.
	visibleChild := false
	for _, child := range src.children {
		if child != nil && child.Visible && child.Opacity != 0 {
			visibleChild = true
			break
		}
	}
	if src != dst && !visibleChild && src.ImageLeft == 0 && src.ImageTop == 0 &&
		src.deviceWidth() == src.Bounds.Width && src.deviceHeight() == src.Bounds.Height {
		if err := chargeLayerPixels(budget, pixels); err != nil {
			return err
		}
		sourceX, sourceY := int64(sx)+left, int64(sy)+top
		destX, destY := int64(dx)+left, int64(dy)+top
		rowBytes := int(copyWidth) * 4
		for row := int64(0); row < copyHeight; row++ {
			sourceStart := (int(sourceY+row)*int(src.deviceWidth()) + int(sourceX)) * 4
			destStart := (int(destY+row)*int(dst.deviceWidth()) + int(destX)) * 4
			copy(dst.RGBA[destStart:destStart+rowBytes], src.RGBA[sourceStart:sourceStart+rowBytes])
		}
		return nil
	}

	region := Rect{
		X:      int32(int64(sx) + left),
		Y:      int32(int64(sy) + top),
		Width:  int32(copyWidth),
		Height: int32(copyHeight),
	}
	pixelsRGBA, err := layerCompleteRegion(src, region, 0, 0, budget)
	if err != nil {
		return err
	}
	destX, destY := int64(dx)+left, int64(dy)+top
	rowBytes := int(copyWidth) * 4
	for row := int64(0); row < copyHeight; row++ {
		sourceStart := int(row) * rowBytes
		destStart := (int(destY+row)*int(dst.deviceWidth()) + int(destX)) * 4
		copy(dst.RGBA[destStart:destStart+rowBytes], pixelsRGBA[sourceStart:sourceStart+rowBytes])
	}
	return nil
}

// layerCompleteRegion returns the source layer's completed local image for a
// clipped rectangle. The layer's own image is copied as-is; its type, opacity,
// visibility and draw face affect neither the base pixels nor the copy result.
func layerCompleteRegion(layer *Layer, rect Rect, depth int, livePixels uint64, budget *uint64) ([]byte, error) {
	if depth >= layerCompositionDepthLimit {
		return nil, fmt.Errorf("Layer.piledCopy: layer composition nesting limit exceeded")
	}
	if layer == nil || !supportedLayerType(layer.Type) || layer.Type == LayerBinder {
		if layer != nil {
			return nil, unsupportedLayerType(layer)
		}
		return nil, ErrInvalidLayer
	}
	if err := validLayerPixels(layer); err != nil {
		return nil, err
	}
	if rect.Width <= 0 || rect.Height <= 0 {
		return nil, ErrInvalidLayer
	}
	pixels := uint64(rect.Width) * uint64(rect.Height)
	if pixels > maxLayerPixels || livePixels > maxLayerPixels-pixels {
		return nil, fmt.Errorf("Layer.piledCopy: layer composition memory limit exceeded")
	}
	if err := chargeLayerPixels(budget, pixels); err != nil {
		return nil, err
	}
	fullWidth, fullHeight := layer.Bounds.Width, layer.Bounds.Height
	cacheable := rect.X == 0 && rect.Y == 0 && rect.Width == fullWidth && rect.Height == fullHeight
	if cacheable && layer.completionRevision == layer.subtreeRevision &&
		layer.completionWidth == rect.Width && layer.completionHeight == rect.Height &&
		len(layer.completionRGBA) == int(pixels)*4 {
		return layer.completionRGBA, nil
	}
	out := make([]byte, int(pixels)*4)

	// The main image is copied without applying this layer's visibility,
	// opacity, face, or clip. ImageLeft/ImageTop position it in layer space.
	left := max64(int64(rect.X), int64(layer.ImageLeft))
	top := max64(int64(rect.Y), int64(layer.ImageTop))
	right := min64(int64(rect.X)+int64(rect.Width), int64(layer.ImageLeft)+int64(layer.deviceWidth()))
	bottom := min64(int64(rect.Y)+int64(rect.Height), int64(layer.ImageTop)+int64(layer.deviceHeight()))
	if right > left && bottom > top {
		rowBytes := int(right-left) * 4
		for y := top; y < bottom; y++ {
			sourceStart := (int(y-int64(layer.ImageTop))*int(layer.deviceWidth()) + int(left-int64(layer.ImageLeft))) * 4
			destStart := (int(y-int64(rect.Y))*int(rect.Width) + int(left-int64(rect.X))) * 4
			copy(out[destStart:destStart+rowBytes], layer.RGBA[sourceStart:sourceStart+rowBytes])
		}
	}

	for _, child := range layer.children {
		if child == nil || !child.Visible || child.Opacity == 0 {
			continue
		}
		left := max64(int64(rect.X), int64(child.Bounds.X))
		top := max64(int64(rect.Y), int64(child.Bounds.Y))
		right := min64(int64(rect.X)+int64(rect.Width), int64(child.Bounds.X)+int64(child.Bounds.Width))
		bottom := min64(int64(rect.Y)+int64(rect.Height), int64(child.Bounds.Y)+int64(child.Bounds.Height))
		if right <= left || bottom <= top {
			continue
		}
		if child.Type == LayerBinder {
			clipped := Rect{X: int32(left), Y: int32(top), Width: int32(right - left), Height: int32(bottom - top)}
			if err := layerCompositeBinderChildren(out, rect, layer.Type, child, int64(child.Bounds.X), int64(child.Bounds.Y), clipped, depth+1, livePixels+pixels, budget); err != nil {
				return nil, err
			}
			continue
		}
		childRect := Rect{X: int32(left - int64(child.Bounds.X)), Y: int32(top - int64(child.Bounds.Y)), Width: int32(right - left), Height: int32(bottom - top)}
		piece, err := layerCompleteRegion(child, childRect, depth+1, livePixels+pixels, budget)
		if err != nil {
			return nil, err
		}
		blendLayerPiece(out, rect, left, top, piece, childRect.Width, childRect.Height, clampLayerOpacity(child.Opacity), child.Type, layer.Type)
	}
	if cacheable {
		layer.completionRGBA = out
		layer.completionRevision = layer.subtreeRevision
		layer.completionWidth = rect.Width
		layer.completionHeight = rect.Height
	}
	return out, nil
}

func layerCompositeBinderChildren(target []byte, targetRect Rect, targetType LayerType, binder *Layer, binderX, binderY int64, clip Rect, depth int, livePixels uint64, budget *uint64) error {
	if depth >= layerCompositionDepthLimit {
		return fmt.Errorf("Layer.piledCopy: layer composition nesting limit exceeded")
	}
	for _, child := range binder.children {
		if child == nil || !child.Visible || child.Opacity == 0 {
			continue
		}
		childX := binderX + int64(child.Bounds.X)
		childY := binderY + int64(child.Bounds.Y)
		left := max64(int64(clip.X), childX)
		top := max64(int64(clip.Y), childY)
		right := min64(int64(clip.X)+int64(clip.Width), childX+int64(child.Bounds.Width))
		bottom := min64(int64(clip.Y)+int64(clip.Height), childY+int64(child.Bounds.Height))
		if right <= left || bottom <= top {
			continue
		}
		clipped := Rect{X: int32(left), Y: int32(top), Width: int32(right - left), Height: int32(bottom - top)}
		if child.Type == LayerBinder {
			if err := layerCompositeBinderChildren(target, targetRect, targetType, child, childX, childY, clipped, depth+1, livePixels, budget); err != nil {
				return err
			}
			continue
		}
		childRect := Rect{X: int32(left - childX), Y: int32(top - childY), Width: int32(right - left), Height: int32(bottom - top)}
		piece, err := layerCompleteRegion(child, childRect, depth+1, livePixels, budget)
		if err != nil {
			return err
		}
		blendLayerPiece(target, targetRect, left, top, piece, childRect.Width, childRect.Height, clampLayerOpacity(child.Opacity), child.Type, targetType)
	}
	return nil
}

func blendLayerPiece(target []byte, targetRect Rect, x, y int64, piece []byte, width, height int32, opacity uint8, typ, targetType LayerType) {
	rowBytes := int(width) * 4
	for row := int32(0); row < height; row++ {
		targetStart := (int(y-int64(targetRect.Y)+int64(row))*int(targetRect.Width) + int(x-int64(targetRect.X))) * 4
		sourceStart := int(row) * int(width) * 4
		destinationRow := target[targetStart : targetStart+rowBytes]
		sourceRow := piece[sourceStart : sourceStart+rowBytes]
		if opacity == 255 && typ == LayerAlpha {
			if rowAllOpaque(sourceRow) {
				copy(destinationRow, sourceRow)
				continue
			}
			if targetType != LayerOpaque && rowAllOpaque(destinationRow) {
				simd.AlphaOverOpaqueRow(destinationRow, sourceRow)
				continue
			}
		}
		if opacity == 255 && typ == LayerOpaque {
			copy(destinationRow, sourceRow)
			for alpha := 3; alpha < len(destinationRow); alpha += 4 {
				destinationRow[alpha] = 255
			}
			continue
		}
		for column := int32(0); column < width; column++ {
			dst := targetStart + int(column)*4
			src := sourceStart + int(column)*4
			blendPixelForTarget(target[dst:dst+4], piece[src:src+4], opacity, typ, targetType)
		}
	}
}

func (s *Services) layerSaveImage(layer *Layer, layerID tjs.ObjectID, args []tjs.Value, budget *uint64) error {
	if len(args) < 1 {
		return fmt.Errorf("Layer.saveLayerImage: missing filename")
	}
	if args[0].Kind() != tjs.KindString {
		return fmt.Errorf("Layer.saveLayerImage requires a storage name")
	}
	name := args[0].Text()
	format := "bmp"
	if len(args) > 1 && !args[1].IsVoid() {
		format = args[1].Text()
	}
	bmpFormat, bpp := bitmapBMPType(format)
	pngFormat := format == "png" || strings.HasPrefix(format, "png") || format == ".png"
	jpegFormat := bitmapJPEGType(format)
	if !bmpFormat && !pngFormat && !jpegFormat {
		return UnsupportedDevice("Layer image encoder " + format)
	}

	s.mu.RLock()
	if s.Layers[layerID] != layer {
		s.mu.RUnlock()
		return fmt.Errorf("Layer.saveLayerImage: context has no Layer native instance")
	}
	if err := validLayerPixels(layer); err != nil {
		s.mu.RUnlock()
		return fmt.Errorf("Layer.saveLayerImage: %w", err)
	}
	width, height := uint32(layer.ImageWidth), uint32(layer.ImageHeight)
	if err := chargeLayerPixels(budget, uint64(width)*uint64(height)); err != nil {
		s.mu.RUnlock()
		return err
	}
	pixels := append([]byte(nil), layer.RGBA...)
	s.mu.RUnlock()

	var (
		output []byte
		err    error
	)
	if bmpFormat {
		output, err = encodeBitmapBMP(width, height, pixels, bpp)
	} else if pngFormat {
		var encoded bytes.Buffer
		imageNRGBA := &image.NRGBA{Pix: pixels, Stride: int(width) * 4, Rect: image.Rect(0, 0, int(width), int(height))}
		err = png.Encode(&encoded, imageNRGBA)
		output = encoded.Bytes()
	} else {
		for alpha := 3; alpha < len(pixels); alpha += 4 {
			pixels[alpha] = 255
		}
		imageRGBA := &image.RGBA{Pix: pixels, Stride: int(width) * 4, Rect: image.Rect(0, 0, int(width), int(height))}
		var encoded bytes.Buffer
		err = jpeg.Encode(&encoded, imageRGBA, &jpeg.Options{Quality: bitmapJPEGQuality(format)})
		output = encoded.Bytes()
	}
	if err != nil {
		return err
	}
	return s.writeSave(name, output, 0)
}
