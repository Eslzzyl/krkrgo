package runtime

import (
	"fmt"
	"github.com/eslzzyl/krkrgo/internal/tjs"
	"math"
	"time"
	"unsafe"
)

type nativeLayerImage struct {
	layer                         *Layer
	bitmap                        *Bitmap
	id                            tjs.ObjectID
	width, height                 int32
	pixels                        []byte
	province                      []byte
	provinceWidth, provinceHeight int32
	mode                          int32
}

func validLayerPixels(l *Layer) error {
	if l == nil || !validLayerImageSize(l.ImageWidth, l.ImageHeight, len(l.RGBA), 4) {
		return fmt.Errorf("%w: layer has no valid image", ErrInvalidLayer)
	}
	return nil
}

func layerImageClip(l *Layer) (left, top, right, bottom int32) {
	left, top, right, bottom = 0, 0, l.ImageWidth, l.ImageHeight
	clip, set := l.Clip()
	if !set {
		return
	}
	clipLeft, clipTop := int64(clip.X), int64(clip.Y)
	clipRight, clipBottom := clipLeft+int64(clip.Width), clipTop+int64(clip.Height)
	if clipLeft > int64(left) {
		left = int32(min(clipLeft, int64(l.ImageWidth)))
	}
	if clipTop > int64(top) {
		top = int32(min(clipTop, int64(l.ImageHeight)))
	}
	if clipRight < int64(right) {
		right = int32(max64(clipRight, 0))
	}
	if clipBottom < int64(bottom) {
		bottom = int32(max64(clipBottom, 0))
	}
	if left < 0 {
		left = 0
	}
	if top < 0 {
		top = 0
	}
	if right < left {
		right = left
	}
	if bottom < top {
		bottom = top
	}
	return
}

func (l *Layer) AssignImages(source *Layer) error {
	if l == nil || source == nil {
		return fmt.Errorf("%w: AssignImages requires two layers", ErrInvalidLayer)
	}
	if source == l {
		if len(l.RGBA) == 0 {
			l.ResetClip()
		} else {
			_ = l.SetClip(Rect{Width: l.ImageWidth, Height: l.ImageHeight})
		}
		return nil
	}
	if len(source.Province) != 0 && !validLayerImageSize(source.ProvinceWidth, source.ProvinceHeight, len(source.Province), 1) {
		return fmt.Errorf("%w: invalid source province dimensions", ErrInvalidLayer)
	}
	if len(source.RGBA) == 0 {
		l.ClearImage()
		if len(source.Province) == 0 {
			l.ClearProvince()
		} else {
			l.Province = append(l.Province[:0], source.Province...)
			l.ProvinceWidth, l.ProvinceHeight = source.ProvinceWidth, source.ProvinceHeight
		}
		l.ResetClip()
		return nil
	}
	if err := validLayerPixels(source); err != nil {
		return err
	}

	width, height := source.ImageWidth, source.ImageHeight
	if width < l.Bounds.Width {
		l.ImageLeft = 0
		l.Bounds.Width = width
	}
	if int64(width)+int64(l.ImageLeft) < int64(l.Bounds.Width) {
		l.ImageLeft = l.Bounds.Width - width
	}
	if height < l.Bounds.Height {
		l.ImageTop = 0
		l.Bounds.Height = height
	}
	if int64(height)+int64(l.ImageTop) < int64(l.Bounds.Height) {
		l.ImageTop = l.Bounds.Height - height
	}
	l.RGBA = append(l.RGBA[:0], source.RGBA...)
	l.ImageWidth, l.ImageHeight = width, height
	if len(source.Province) == 0 {
		l.ClearProvince()
	} else {
		l.Province = append(l.Province[:0], source.Province...)
		l.ProvinceWidth, l.ProvinceHeight = source.ProvinceWidth, source.ProvinceHeight
	}
	_ = l.SetClip(Rect{Width: l.ImageWidth, Height: l.ImageHeight})
	return nil
}

func operationModeForLayer(source *Layer) int32 {
	if source == nil {
		return int32(LayerAlpha)
	}
	switch source.Type {
	case LayerOpaque, LayerAlpha, LayerAdditive, LayerSubtractive, LayerMultiplicative,
		LayerDodge, LayerDarken, LayerLighten, LayerScreen, LayerAddAlpha,
		LayerPSNormal, LayerPSAdditive, LayerPSSubtractive, LayerPSMultiplicative,
		LayerPSScreen, LayerPSOverlay, LayerPSHardLight, LayerPSSoftLight,
		LayerPSColorDodge, LayerPSColorDodge5, LayerPSColorBurn, LayerPSLighten,
		LayerPSDarken, LayerPSDifference, LayerPSDifference5, LayerPSExclusion:
		return int32(source.Type)
	default:
		return int32(LayerOpaque)
	}
}

func validLayerOperationMode(mode int32) bool {
	return mode >= int32(LayerOpaque) && mode <= int32(LayerPSExclusion) && mode != 6 && mode != 7
}

func layerFaceTarget(face int32) (LayerType, bool) {
	switch face {
	case layerFaceAlpha:
		return LayerAlpha, true
	case layerFaceOpaque:
		return LayerOpaque, true
	case layerFaceAddAlpha:
		return LayerAddAlpha, true
	default:
		return 0, false
	}
}

func layerOperationPixel(dst, src []byte, face, mode, opacity int32, holdAlpha bool) error {
	target, ok := layerFaceTarget(face)
	if !ok {
		return fmt.Errorf("%w: invalid operateRect draw face %d", ErrInvalidLayer, face)
	}
	if !validLayerOperationMode(mode) {
		return fmt.Errorf("%w: Layer.operateRect blend mode %d", ErrUnsupportedNative, mode)
	}
	if opacity < 0 {
		opacity = 0
	} else if opacity > 255 {
		opacity = 255
	}
	if opacity == 0 {
		return nil
	}
	if mode == int32(LayerPSMultiplicative) {
		alpha := int(src[3])
		if opacity != 255 {
			alpha = alpha * int(opacity) >> 8
		}
		for c := 0; c < 3; c++ {
			multiplied := int(dst[c]) * int(src[c]) >> 8
			dst[c] = byte(int(dst[c]) + ((multiplied - int(dst[c])) * alpha >> 8))
		}
		if !holdAlpha {
			dst[3] = 0
		}
		return nil
	}

	source := [4]byte{src[0], src[1], src[2], src[3]}
	alpha := int(source[3])
	if mode == int32(LayerOpaque) {
		alpha = int(opacity)
	} else if opacity != 255 {
		alpha = alpha * int(opacity) >> 8
	}
	if mode == int32(LayerAddAlpha) && face == layerFaceAlpha {
		return nil
	}
	if mode == int32(LayerOpaque) && opacity == 255 {
		dst[0], dst[1], dst[2] = source[0], source[1], source[2]
		if face != layerFaceOpaque {
			dst[3] = 255
		} else if !holdAlpha {
			dst[3] = source[3]
		}
		return nil
	}
	if face == layerFaceAlpha {
		weight := straightAlphaWeight(dst[3], uint8(alpha))
		for c := 0; c < 3; c++ {
			dst[c] = byte(int(dst[c]) + ((int(source[c]) - int(dst[c])) * weight >> 8))
		}
		dst[3] = byte(255 - (255-int(dst[3]))*(255-alpha)/255)
		return nil
	}
	if face == layerFaceAddAlpha || mode == int32(LayerAddAlpha) {
		if mode == int32(LayerAddAlpha) && face == layerFaceOpaque && !holdAlpha && opacity != 255 {
			opacity += opacity >> 7
			alpha = int(source[3]) * int(opacity) >> 8
		}
		if mode == int32(LayerAddAlpha) && opacity != 255 {
			for c := 0; c < 4; c++ {
				source[c] = byte(int(source[c]) * int(opacity) >> 8)
			}
		}
		if mode == int32(LayerAlpha) {
			for c := 0; c < 3; c++ {
				source[c] = byte(int(source[c]) * alpha >> 8)
			}
		}
		for c := 0; c < 3; c++ {
			remaining := int(dst[c]) * (255 - alpha) >> 8
			if mode == int32(LayerAddAlpha) && (opacity == 255 || (face == layerFaceOpaque && !holdAlpha)) {
				remaining = int(dst[c]) - (int(dst[c]) * alpha >> 8)
			}
			dst[c] = byte(min(255, remaining+int(source[c])))
		}
		if face == layerFaceAddAlpha || !holdAlpha {
			dst[3] = byte(min(255, int(dst[3])+alpha-(int(dst[3])*alpha>>8)))
		}
		return nil
	}
	if mode == int32(LayerAlpha) {
		limit := 4
		if holdAlpha {
			limit = 3
		}
		for c := 0; c < limit; c++ {
			if alpha == 255 && !holdAlpha {
				dst[c] = source[c]
			} else {
				dst[c] = byte(int(dst[c]) + ((int(source[c]) - int(dst[c])) * alpha >> 8))
			}
		}
		return nil
	}

	oldAlpha := dst[3]
	blendPixelForTarget(dst, source[:], uint8(opacity), LayerType(mode), target)
	if holdAlpha {
		dst[3] = oldAlpha
	}
	return nil
}

func (l *Layer) OperateRect(source *Layer, dx, dy, sx, sy, width, height, mode, opacity int32) error {
	if source == nil {
		return fmt.Errorf("%w: operateRect source is nil", ErrInvalidLayer)
	}
	if err := validLayerPixels(source); err != nil {
		return err
	}
	if mode == layerFaceAuto {
		mode = operationModeForLayer(source)
	}
	return l.operateRectPixels(source.RGBA, source.ImageWidth, source.ImageHeight, dx, dy, sx, sy, width, height, mode, opacity)
}

// CopyRect copies a source image rectangle to this layer without blending.
// Face and HoldAlpha control whether it copies RGB, alpha, or the province
// plane, matching Layer.copyRect's bitmap-plane semantics.
func (l *Layer) CopyRect(source *Layer, dx, dy, sx, sy, width, height int32) error {
	if source == nil {
		return fmt.Errorf("%w: copyRect source is nil", ErrInvalidLayer)
	}
	sourceWidth, sourceHeight := source.ImageWidth, source.ImageHeight
	if sourceWidth <= 0 || sourceHeight <= 0 {
		sourceWidth, sourceHeight = source.ProvinceWidth, source.ProvinceHeight
	}
	return l.copyRectPixels(source.RGBA, sourceWidth, sourceHeight, source.Province,
		source.ProvinceWidth, source.ProvinceHeight, dx, dy, sx, sy, width, height)
}

func (l *Layer) copyRectPixels(source []byte, sourceWidth, sourceHeight int32, sourceProvince []byte,
	sourceProvinceWidth, sourceProvinceHeight, dx, dy, sx, sy, width, height int32) error {
	if width <= 0 || height <= 0 {
		return nil
	}
	if err := validLayerPixels(l); err != nil {
		return err
	}

	left, top, right, bottom := layerImageClip(l)
	blitLeft := max64(0, max64(-int64(sx), max64(-int64(dx), int64(left)-int64(dx))))
	blitTop := max64(0, max64(-int64(sy), max64(-int64(dy), int64(top)-int64(dy))))
	blitRight := min(int64(width), min(int64(sourceWidth)-int64(sx),
		min(int64(l.ImageWidth)-int64(dx), int64(right)-int64(dx))))
	blitBottom := min(int64(height), min(int64(sourceHeight)-int64(sy),
		min(int64(l.ImageHeight)-int64(dy), int64(bottom)-int64(dy))))
	if blitRight <= blitLeft || blitBottom <= blitTop {
		return nil
	}

	face := l.drawFace()
	switch face {
	case layerFaceAlpha, layerFaceOpaque, layerFaceMask, layerFaceAddAlpha:
		if sourceWidth <= 0 || sourceHeight <= 0 ||
			uint64(sourceWidth)*uint64(sourceHeight)*4 != uint64(len(source)) {
			return fmt.Errorf("%w: invalid copyRect source image", ErrInvalidLayer)
		}
	case layerFaceProvince:
		if len(sourceProvince) != 0 {
			if !validLayerImageSize(sourceProvinceWidth, sourceProvinceHeight, len(sourceProvince), 1) {
				return ErrProvinceSizeMismatch
			}
			if sourceProvinceWidth != sourceWidth || sourceProvinceHeight != sourceHeight {
				return ErrProvinceSizeMismatch
			}
		}
	default:
		return fmt.Errorf("%w: invalid copyRect draw face %d", ErrInvalidLayer, face)
	}

	destX, destY := int64(dx)+blitLeft, int64(dy)+blitTop
	sourceX, sourceY := int64(sx)+blitLeft, int64(sy)+blitTop
	copyWidth, copyHeight := blitRight-blitLeft, blitBottom-blitTop
	if face == layerFaceProvince {
		if len(l.Province) != 0 && !validLayerImageSize(l.ProvinceWidth, l.ProvinceHeight, len(l.Province), 1) {
			return ErrProvinceSizeMismatch
		}
		if len(l.Province) != 0 && (l.ProvinceWidth != l.ImageWidth || l.ProvinceHeight != l.ImageHeight) {
			return ErrProvinceSizeMismatch
		}
		if len(sourceProvince) == 0 && len(l.Province) == 0 {
			return nil
		}
		if len(l.Province) == 0 {
			l.Province = make([]byte, int(uint64(l.ImageWidth)*uint64(l.ImageHeight)))
			l.ProvinceWidth, l.ProvinceHeight = l.ImageWidth, l.ImageHeight
		}

		samePlane := len(sourceProvince) != 0 && &sourceProvince[0] == &l.Province[0]
		backwards := samePlane && destY > sourceY
		for row := int64(0); row < copyHeight; row++ {
			sourceRow, destRow := row, row
			if backwards {
				sourceRow, destRow = copyHeight-1-row, copyHeight-1-row
			}
			sourceOffset := (sourceY+sourceRow)*int64(sourceProvinceWidth) + sourceX
			destOffset := (destY+destRow)*int64(l.ProvinceWidth) + destX
			if len(sourceProvince) == 0 {
				clear(l.Province[destOffset : destOffset+copyWidth])
			} else {
				copy(l.Province[destOffset:destOffset+copyWidth], sourceProvince[sourceOffset:sourceOffset+copyWidth])
			}
		}
		return nil
	}

	samePixels := len(source) != 0 && &source[0] == &l.RGBA[0]
	backwards := samePixels && destY > sourceY
	copyAll := face == layerFaceAlpha || face == layerFaceAddAlpha || (face == layerFaceOpaque && !l.HoldAlpha)
	for row := int64(0); row < copyHeight; row++ {
		sourceRow, destRow := row, row
		if backwards {
			sourceRow, destRow = copyHeight-1-row, copyHeight-1-row
		}
		sourceOffset := ((sourceY+sourceRow)*int64(sourceWidth) + sourceX) * 4
		destOffset := ((destY+destRow)*int64(l.ImageWidth) + destX) * 4
		if copyAll {
			rowBytes := copyWidth * 4
			copy(l.RGBA[destOffset:destOffset+rowBytes], source[sourceOffset:sourceOffset+rowBytes])
			continue
		}

		start, end, step := int64(0), copyWidth, int64(1)
		if samePixels && destX > sourceX {
			start, end, step = copyWidth-1, -1, -1
		}
		for column := start; column != end; column += step {
			sourcePixel := sourceOffset + column*4
			destPixel := destOffset + column*4
			if face == layerFaceMask {
				l.RGBA[destPixel+3] = source[sourcePixel+3]
			} else {
				l.RGBA[destPixel] = source[sourcePixel]
				l.RGBA[destPixel+1] = source[sourcePixel+1]
				l.RGBA[destPixel+2] = source[sourcePixel+2]
			}
		}
	}
	return nil
}

func (l *Layer) operateRectPixels(source []byte, sourceWidth, sourceHeight, dx, dy, sx, sy, width, height, mode, opacity int32) error {
	if err := validLayerPixels(l); err != nil {
		return err
	}
	if sourceWidth <= 0 || sourceHeight <= 0 || uint64(sourceWidth)*uint64(sourceHeight)*4 != uint64(len(source)) {
		return fmt.Errorf("%w: invalid operateRect source image", ErrInvalidLayer)
	}
	if width <= 0 || height <= 0 {
		return nil
	}
	left, top, right, bottom := layerImageClip(l)
	clipLeft := int64(left)
	clipTop := int64(top)
	clipRight := int64(right)
	clipBottom := int64(bottom)
	blitLeft := max64(int64(0), max64(-int64(sx), max64(-int64(dx), clipLeft-int64(dx))))
	blitTop := max64(int64(0), max64(-int64(sy), max64(-int64(dy), clipTop-int64(dy))))
	blitRight := min(int64(width), min(int64(sourceWidth)-int64(sx), min(int64(l.ImageWidth)-int64(dx), clipRight-int64(dx))))
	blitBottom := min(int64(height), min(int64(sourceHeight)-int64(sy), min(int64(l.ImageHeight)-int64(dy), clipBottom-int64(dy))))
	if blitRight <= blitLeft || blitBottom <= blitTop {
		return nil
	}
	face := l.drawFace()
	if _, ok := layerFaceTarget(face); !ok {
		return fmt.Errorf("%w: invalid operateRect draw face %d", ErrInvalidLayer, face)
	}
	if !validLayerOperationMode(mode) {
		return fmt.Errorf("%w: Layer.operateRect blend mode %d", ErrUnsupportedNative, mode)
	}
	if opacity <= 0 {
		return nil
	}

	copyWidth, copyHeight := blitRight-blitLeft, blitBottom-blitTop
	var snapshot []byte
	if len(source) != 0 && len(source) == len(l.RGBA) && &source[0] == &l.RGBA[0] {
		pixels := uint64(copyWidth) * uint64(copyHeight)
		if pixels > maxLayerPixels || pixels > uint64(maxIntValue()/4) {
			return fmt.Errorf("%w: operateRect temporary image exceeds limit", ErrInvalidLayer)
		}
		snapshot = make([]byte, int(pixels)*4)
		for row := int64(0); row < copyHeight; row++ {
			sourceOffset := ((int64(sy)+blitTop+row)*int64(sourceWidth) + int64(sx) + blitLeft) * 4
			copy(snapshot[row*copyWidth*4:(row+1)*copyWidth*4], source[sourceOffset:sourceOffset+copyWidth*4])
		}
		source = snapshot
		sourceWidth = int32(copyWidth)
		sx, sy = 0, 0
	}
	for y := int64(0); y < copyHeight; y++ {
		for x := int64(0); x < copyWidth; x++ {
			di := ((int64(dy)+blitTop+y)*int64(l.ImageWidth) + int64(dx) + blitLeft + x) * 4
			var si int64
			if snapshot != nil {
				si = (y*copyWidth + x) * 4
			} else {
				si = ((int64(sy)+blitTop+y)*int64(sourceWidth) + int64(sx) + blitLeft + x) * 4
			}
			if err := layerOperationPixel(l.RGBA[di:di+4], source[si:si+4], face, mode, opacity, l.HoldAlpha); err != nil {
				return err
			}
		}
	}
	return nil
}

func (l *Layer) StretchCopy(source *Layer, dx, dy, width, height, sx, sy, sourceWidth, sourceHeight, stretchType int32) error {
	if source == nil {
		return fmt.Errorf("%w: stretchCopy source is nil", ErrInvalidLayer)
	}
	if err := validLayerPixels(source); err != nil {
		return err
	}
	return l.stretchCopyPixels(source.RGBA, source.ImageWidth, source.ImageHeight, dx, dy, width, height, sx, sy, sourceWidth, sourceHeight, stretchType)
}

func (l *Layer) stretchCopyPixels(source []byte, sourceWidth, sourceHeight, dx, dy, width, height, sx, sy, cropWidth, cropHeight, stretchType int32) error {
	if !stretchCopyStatsEnabled {
		return l.stretchCopyPixelsImpl(source, sourceWidth, sourceHeight, dx, dy, width, height, sx, sy, cropWidth, cropHeight, stretchType)
	}
	started := time.Now()
	err := l.stretchCopyPixelsImpl(source, sourceWidth, sourceHeight, dx, dy, width, height, sx, sy, cropWidth, cropHeight, stretchType)
	recordStretchCopyStats(time.Since(started), len(source), width, height)
	return err
}

func (l *Layer) stretchCopyPixelsImpl(source []byte, sourceWidth, sourceHeight, dx, dy, width, height, sx, sy, cropWidth, cropHeight, stretchType int32) error {
	if err := validLayerPixels(l); err != nil {
		return err
	}
	if sourceWidth <= 0 || sourceHeight <= 0 || uint64(sourceWidth)*uint64(sourceHeight)*4 != uint64(len(source)) {
		return fmt.Errorf("%w: invalid stretchCopy source image", ErrInvalidLayer)
	}
	if width == 0 || height == 0 || cropWidth == 0 || cropHeight == 0 {
		return nil
	}
	if width < 0 || height < 0 || cropWidth < 0 || cropHeight < 0 {
		return fmt.Errorf("%w: negative stretchCopy rectangle", ErrInvalidLayer)
	}
	if sx < 0 || sy < 0 || int64(sx)+int64(cropWidth) > int64(sourceWidth) || int64(sy)+int64(cropHeight) > int64(sourceHeight) {
		return fmt.Errorf("%w: stretchCopy source rectangle outside image", ErrInvalidLayer)
	}
	filter := stretchType & 0xffff
	if filter != 0 && filter != 1 && filter != 2 {
		return fmt.Errorf("%w: Layer.stretchCopy filter %d", ErrUnsupportedNative, stretchType)
	}
	left, top, right, bottom := layerImageClip(l)
	outLeft := max64(int64(dx), int64(left))
	outTop := max64(int64(dy), int64(top))
	outRight := min(int64(dx)+int64(width), int64(right))
	outBottom := min(int64(dy)+int64(height), int64(bottom))
	if outRight <= outLeft || outBottom <= outTop {
		return nil
	}
	face := l.drawFace()
	if !layerCopyFaceSupported(face) {
		return fmt.Errorf("%w: invalid stretchCopy draw face %d", ErrInvalidLayer, face)
	}
	outWidth, outHeight := outRight-outLeft, outBottom-outTop
	pixels := uint64(outWidth) * uint64(outHeight)
	if pixels > maxLayerPixels || pixels > uint64(maxIntValue()/4) {
		return fmt.Errorf("%w: stretchCopy temporary image exceeds limit", ErrInvalidLayer)
	}
	outWidthInt := int(outWidth)
	sourceStride := int(sourceWidth)
	directOutput := !byteSlicesOverlap(source, l.RGBA) && outLeft == 0 && outWidth == int64(l.ImageWidth) &&
		(face != layerFaceOpaque || !l.HoldAlpha)
	var output []byte
	if directOutput {
		start := int(outTop) * outWidthInt * 4
		output = l.RGBA[start : start+int(pixels)*4]
	} else {
		output = make([]byte, int(pixels)*4)
	}
	if filter == 0 {
		xSamples := make([]int, outWidthInt)
		for x := range xSamples {
			u := (float64(outLeft-int64(dx)+int64(x)) + 0.5) / float64(width)
			px := float64(sx) + u*float64(cropWidth) - 0.5
			ix := int(math.Floor(px + 0.5))
			xSamples[x] = max(int(sx), min(ix, int(sx+cropWidth-1))) * 4
		}
		for y := int64(0); y < outHeight; y++ {
			v := (float64(outTop-int64(dy)+y) + 0.5) / float64(height)
			py := float64(sy) + v*float64(cropHeight) - 0.5
			iy := max(int(sy), min(int(math.Floor(py+0.5)), int(sy+cropHeight-1))) * sourceStride * 4
			row := int(y) * outWidthInt * 4
			for x, sourceX := range xSamples {
				copy(output[row+x*4:row+x*4+4], source[iy+sourceX:iy+sourceX+4])
			}
		}
	} else {
		type axisSample struct {
			first, second int
			weight        float64
		}
		axisSamples := make([]axisSample, outWidthInt+int(outHeight))
		xSamples := axisSamples[:outWidthInt]
		for x := range xSamples {
			u := (float64(outLeft-int64(dx)+int64(x)) + 0.5) / float64(width)
			px := float64(sx) + u*float64(cropWidth) - 0.5
			minX := int(math.Floor(px))
			maxX := min(minX+1, int(sx+cropWidth-1))
			minX = max(minX, int(sx))
			fx := px - float64(minX)
			if fx < 0 {
				fx = 0
			}
			if fx > 1 {
				fx = 1
			}
			xSamples[x] = axisSample{first: minX * 4, second: maxX * 4, weight: fx}
		}
		ySamples := axisSamples[outWidthInt:]
		for y := range ySamples {
			v := (float64(outTop-int64(dy)+int64(y)) + 0.5) / float64(height)
			py := float64(sy) + v*float64(cropHeight) - 0.5
			minY := int(math.Floor(py))
			maxY := min(minY+1, int(sy+cropHeight-1))
			minY = max(minY, int(sy))
			fy := py - float64(minY)
			if fy < 0 {
				fy = 0
			}
			if fy > 1 {
				fy = 1
			}
			ySamples[y] = axisSample{first: minY * sourceStride * 4, second: maxY * sourceStride * 4, weight: fy}
		}
		for y, ys := range ySamples {
			row := y * outWidthInt * 4
			topRow, bottomRow := ys.first, ys.second
			fy, inverseY := ys.weight, 1-ys.weight
			for x, xs := range xSamples {
				topLeft, topRight := topRow+xs.first, topRow+xs.second
				bottomLeft, bottomRight := bottomRow+xs.first, bottomRow+xs.second
				fx, inverseX := xs.weight, 1-xs.weight
				destinationOffset := row + x*4
				for channel := 0; channel < 4; channel++ {
					topValue := float64(source[topLeft+channel])*inverseX + float64(source[topRight+channel])*fx
					bottomValue := float64(source[bottomLeft+channel])*inverseX + float64(source[bottomRight+channel])*fx
					value := topValue*inverseY + bottomValue*fy
					// The bilinear result is a convex combination of byte values,
					// so value is in [0,255]; truncating value+0.5 matches
					// math.Round for this nonnegative range.
					output[destinationOffset+channel] = uint8(value + 0.5)
				}
			}
		}
	}
	if !directOutput && (face != layerFaceOpaque || !l.HoldAlpha) {
		rowBytes := outWidthInt * 4
		for y := int64(0); y < outHeight; y++ {
			di := ((outTop+y)*int64(l.ImageWidth) + outLeft) * 4
			si := y * int64(rowBytes)
			copy(l.RGBA[di:di+int64(rowBytes)], output[si:si+int64(rowBytes)])
		}
	} else if !directOutput {
		for y := int64(0); y < outHeight; y++ {
			for x := int64(0); x < outWidth; x++ {
				di := ((outTop+y)*int64(l.ImageWidth) + outLeft + x) * 4
				si := (y*outWidth + x) * 4
				copy(l.RGBA[di:di+3], output[si:si+3])
			}
		}
	}
	return nil
}

// byteSlicesOverlap checks whether two non-empty slices refer to any common
// byte. The slices keep their backing storage alive while their addresses are
// compared; uintptr differences avoid end-address overflow.
func byteSlicesOverlap(a, b []byte) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	aStart := uintptr(unsafe.Pointer(unsafe.SliceData(a)))
	bStart := uintptr(unsafe.Pointer(unsafe.SliceData(b)))
	if aStart <= bStart {
		return bStart-aStart < uintptr(len(a))
	}
	return aStart-bStart < uintptr(len(b))
}

func layerCopyFaceSupported(face int32) bool {
	return face == layerFaceAlpha || face == layerFaceOpaque || face == layerFaceAddAlpha
}

// copyPixelForFace writes one sampled pixel with the channel selection
// Layer.affineCopy's bmCopy path uses: an opaque face that holds alpha copies
// RGB, every other supported face copies all four channels.
func copyPixelForFace(dst, src []byte, face int32, holdAlpha bool) {
	if face == layerFaceOpaque && holdAlpha {
		dst[0], dst[1], dst[2] = src[0], src[1], src[2]
		return
	}
	dst[0], dst[1], dst[2], dst[3] = src[0], src[1], src[2], src[3]
}

// affineCopyPixels maps a source rectangle onto this layer's image plane and
// writes the sampled pixels over the transformed area, mirroring
// tTJSNI_BaseLayer::AffineCopy: the draw face selects the copied channels,
// destination pixels the transform leaves uncovered keep their value, and clear
// fills the clipping rectangle with the layer's neutral color first. The matrix
// maps source-rectangle center coordinates to destination coordinates, which is
// the form the caller derives from either the matrix or the points arguments.
func (l *Layer) affineCopyPixels(source []byte, sourceWidth, sourceHeight, sleft, stop, swidth, sheight int32, matrix [6]float64, filter int32, clear bool) error {
	if err := validLayerPixels(l); err != nil {
		return err
	}
	if sourceWidth <= 0 || sourceHeight <= 0 || uint64(sourceWidth)*uint64(sourceHeight)*4 != uint64(len(source)) {
		return fmt.Errorf("%w: invalid affineCopy source image", ErrInvalidLayer)
	}
	if swidth < 0 || sheight < 0 {
		return fmt.Errorf("%w: negative affineCopy source rectangle", ErrInvalidLayer)
	}
	if sleft < 0 || stop < 0 || int64(sleft)+int64(swidth) > int64(sourceWidth) || int64(stop)+int64(sheight) > int64(sourceHeight) {
		return fmt.Errorf("%w: affineCopy source rectangle outside image", ErrInvalidLayer)
	}
	if filter != 0 && filter != 1 && filter != 2 {
		return fmt.Errorf("%w: Layer.affineCopy filter %d", ErrUnsupportedNative, filter)
	}
	face := l.drawFace()
	if !layerCopyFaceSupported(face) {
		return fmt.Errorf("%w: affineCopy draw face %d", ErrUnsupportedNative, face)
	}
	left, top, right, bottom := layerImageClip(l)
	if swidth == 0 || sheight == 0 || left >= right || top >= bottom {
		return nil
	}
	if clear {
		neutral := layerNeutralColor(l.Type)
		for y := int64(top); y < int64(bottom); y++ {
			offset := (y*int64(l.ImageWidth) + int64(left)) * 4
			for x := range int64(right - left) {
				copy(l.RGBA[offset+x*4:offset+4+x*4], neutral[:])
			}
		}
	}

	a, b, c, d, tx, ty := matrix[0], matrix[1], matrix[2], matrix[3], matrix[4], matrix[5]
	determinant := a*d - b*c
	if math.IsNaN(determinant) || math.IsInf(determinant, 0) {
		return fmt.Errorf("%w: affineCopy transform outside safe numeric range", ErrUnsupportedNative)
	}
	if determinant == 0 {
		return nil
	}
	sourceWidthF, sourceHeightF := float64(swidth), float64(sheight)
	minX, maxX := math.Inf(1), math.Inf(-1)
	minY, maxY := math.Inf(1), math.Inf(-1)
	for _, corner := range [4][2]float64{
		{-0.5, -0.5},
		{sourceWidthF - 0.5, -0.5},
		{sourceWidthF - 0.5, sourceHeightF - 0.5},
		{-0.5, sourceHeightF - 0.5},
	} {
		x := a*corner[0] + c*corner[1] + tx
		y := b*corner[0] + d*corner[1] + ty
		if math.IsNaN(x) || math.IsInf(x, 0) || math.IsNaN(y) || math.IsInf(y, 0) {
			return fmt.Errorf("%w: affineCopy transform outside safe numeric range", ErrUnsupportedNative)
		}
		minX, maxX = math.Min(minX, x), math.Max(maxX, x)
		minY, maxY = math.Min(minY, y), math.Max(maxY, y)
	}
	outLeft, outTop := int64(left), int64(top)
	outRight, outBottom := int64(right), int64(bottom)
	if minX > float64(outLeft) {
		outLeft = int64(math.Ceil(minX))
	}
	if maxX < float64(outRight) {
		outRight = int64(math.Ceil(maxX))
	}
	if minY > float64(outTop) {
		outTop = int64(math.Ceil(minY))
	}
	if maxY < float64(outBottom) {
		outBottom = int64(math.Ceil(maxY))
	}
	if outRight <= outLeft || outBottom <= outTop {
		return nil
	}
	// The plane bounds the destination scan even when the clip rectangle or the
	// transform reaches past the allocated image.
	if outLeft < 0 {
		outLeft = 0
	}
	if outTop < 0 {
		outTop = 0
	}
	if outRight > int64(l.ImageWidth) {
		outRight = int64(l.ImageWidth)
	}
	if outBottom > int64(l.ImageHeight) {
		outBottom = int64(l.ImageHeight)
	}
	if outRight <= outLeft || outBottom <= outTop {
		return nil
	}

	if len(source) == len(l.RGBA) && &source[0] == &l.RGBA[0] {
		// Reading from the plane being written would let the scan sample pixels
		// this call already replaced, so the source rectangle is snapshotted.
		rows := int64(sheight)
		snapshot := make([]byte, int64(swidth)*rows*4)
		for row := int64(0); row < rows; row++ {
			offset := ((int64(stop)+row)*int64(sourceWidth) + int64(sleft)) * 4
			copy(snapshot[row*int64(swidth)*4:(row+1)*int64(swidth)*4], source[offset:offset+int64(swidth)*4])
		}
		source, sourceWidth, sleft, stop = snapshot, swidth, 0, 0
	}

	sourceStride := int64(sourceWidth)
	nearest := filter == 0
	lastColumn, lastRow := int64(sleft+swidth-1), int64(stop+sheight-1)
	for y := outTop; y < outBottom; y++ {
		dy := float64(y) - ty
		rowOffset := (y*int64(l.ImageWidth) + outLeft) * 4
		for x := outLeft; x < outRight; x++ {
			dx := float64(x) - tx
			u := (dx*d - dy*c) / determinant
			v := (dy*a - dx*b) / determinant
			if !(u >= -0.5 && u < sourceWidthF-0.5 && v >= -0.5 && v < sourceHeightF-0.5) {
				continue
			}
			di := rowOffset + (x-outLeft)*4
			if nearest {
				column := int64(math.Floor(u + 0.5))
				row := int64(math.Floor(v + 0.5))
				if column < 0 {
					column = 0
				} else if column > int64(swidth)-1 {
					column = int64(swidth) - 1
				}
				if row < 0 {
					row = 0
				} else if row > int64(sheight)-1 {
					row = int64(sheight) - 1
				}
				si := ((int64(stop)+row)*sourceStride + int64(sleft) + column) * 4
				copyPixelForFace(l.RGBA[di:di+4], source[si:si+4], face, l.HoldAlpha)
				continue
			}
			sx := float64(sleft) + u
			sy := float64(stop) + v
			minColumn := int64(math.Floor(sx))
			minRow := int64(math.Floor(sy))
			fx, fy := sx-float64(minColumn), sy-float64(minRow)
			if minColumn < int64(sleft) {
				minColumn = int64(sleft)
			} else if minColumn > lastColumn {
				minColumn = lastColumn
			}
			if minRow < int64(stop) {
				minRow = int64(stop)
			} else if minRow > lastRow {
				minRow = lastRow
			}
			if minColumn+1 > lastColumn {
				fx = 0
			}
			if minRow+1 > lastRow {
				fy = 0
			}
			if fx < 0 {
				fx = 0
			} else if fx > 1 {
				fx = 1
			}
			if fy < 0 {
				fy = 0
			} else if fy > 1 {
				fy = 1
			}
			// The neighbouring taps stay inside the source image so sampling
			// the last row or column cannot read past the plane.
			nextColumn, nextRow := minColumn+1, minRow+1
			if nextColumn > int64(sourceWidth)-1 {
				nextColumn = int64(sourceWidth) - 1
			}
			if nextRow > int64(sourceHeight)-1 {
				nextRow = int64(sourceHeight) - 1
			}
			topLeft := (minRow*sourceStride + minColumn) * 4
			topRight := (minRow*sourceStride + nextColumn) * 4
			bottomLeft := (nextRow*sourceStride + minColumn) * 4
			bottomRight := (nextRow*sourceStride + nextColumn) * 4
			var pixel [4]byte
			for channel := range pixel {
				top := float64(source[topLeft+int64(channel)])*(1-fx) + float64(source[topRight+int64(channel)])*fx
				bottomValue := float64(source[bottomLeft+int64(channel)])*(1-fx) + float64(source[bottomRight+int64(channel)])*fx
				pixel[channel] = uint8(top*(1-fy) + bottomValue*fy + 0.5)
			}
			copyPixelForFace(l.RGBA[di:di+4], pixel[:], face, l.HoldAlpha)
		}
	}
	return nil
}

func (l *Layer) DoGrayScale() error {
	if err := validLayerPixels(l); err != nil {
		return err
	}
	left, top, right, bottom := layerImageClip(l)
	for y := top; y < bottom; y++ {
		for x := left; x < right; x++ {
			i := (int(y)*int(l.ImageWidth) + int(x)) * 4
			gray := byte((uint32(l.RGBA[i])*54 + uint32(l.RGBA[i+1])*183 + uint32(l.RGBA[i+2])*19) >> 8)
			l.RGBA[i], l.RGBA[i+1], l.RGBA[i+2] = gray, gray, gray
		}
	}
	return nil
}

type LayerGammaChannel struct {
	Gamma          float64
	Floor, Ceiling int32
}

func layerGammaTable(adjust LayerGammaChannel) [256]byte {
	var table [256]byte
	inverse := 1 / adjust.Gamma
	amplitude := float64(int64(adjust.Ceiling) - int64(adjust.Floor))
	for value := range table {
		result := math.Pow(float64(value)/255, inverse)*amplitude + 0.5 + float64(adjust.Floor)
		if !math.IsNaN(result) && !math.IsInf(result, 0) && result >= math.MinInt32 && result < 2147483648 {
			integer := int64(result)
			if integer < 0 {
				integer = 0
			} else if integer > 255 {
				integer = 255
			}
			table[value] = byte(integer)
		}
	}
	return table
}

func (l *Layer) AdjustGamma(channels [3]LayerGammaChannel) error {
	if err := validLayerPixels(l); err != nil {
		return err
	}
	for i, channel := range channels {
		if math.IsNaN(channel.Gamma) || math.IsInf(channel.Gamma, 0) {
			return fmt.Errorf("%w: Layer.adjustGamma gamma must be finite", ErrInvalidLayer)
		}
		channels[i].Gamma = float64(float32(channel.Gamma))
		if math.IsInf(channels[i].Gamma, 0) {
			return fmt.Errorf("%w: Layer.adjustGamma gamma must be finite", ErrInvalidLayer)
		}
	}
	var tables [3][256]byte
	for i := range channels {
		tables[i] = layerGammaTable(channels[i])
	}
	return l.applyRGBTables(tables)
}

// Light applies brightness in [-255, 255] and contrast in [-100, 100]
// around the channel midpoint, clamping RGB while preserving alpha.
func (l *Layer) Light(light, contrast int32) error {
	if err := validLayerPixels(l); err != nil {
		return err
	}
	scale := 100 + int64(contrast)
	offset := int64(light) + 128
	var tables [3][256]byte
	for value := range tables[0] {
		adjusted := float64(int64(value)-128)*float64(scale)/100 + float64(offset)
		if adjusted <= 0 {
			tables[0][value] = 0
		} else if adjusted >= 255 {
			tables[0][value] = 255
		} else {
			tables[0][value] = byte(adjusted + 0.5)
		}
	}
	for channel := 1; channel < len(tables); channel++ {
		tables[channel] = tables[0]
	}
	return l.applyRGBTables(tables)
}

func (l *Layer) applyRGBTables(tables [3][256]byte) error {
	left, top, right, bottom := layerImageClip(l)
	additive := l.drawFace() == layerFaceAddAlpha
	for y := top; y < bottom; y++ {
		for x := left; x < right; x++ {
			i := (int(y)*int(l.ImageWidth) + int(x)) * 4
			alpha := uint32(l.RGBA[i+3])
			if additive && alpha < 255 {
				adjusted := alpha + alpha>>7
				reciprocal := uint32(32767)
				if alpha != 0 {
					reciprocal = min(uint32(32767), 65536/alpha)
				}
				for channel := 0; channel < 3; channel++ {
					color := uint32(l.RGBA[i+channel])
					if color > alpha {
						l.RGBA[i+channel] = byte((uint32(tables[channel][255]) * adjusted >> 8) + color - alpha)
					} else {
						index := reciprocal * color >> 8
						if index >= uint32(len(tables[channel])) {
							// Fixed-point normalization can produce 256 at an exact endpoint.
							index = uint32(len(tables[channel]) - 1)
						}
						l.RGBA[i+channel] = byte(uint32(tables[channel][index]) * adjusted >> 8)
					}
				}
			} else if alpha != 0 {
				for channel := 0; channel < 3; channel++ {
					l.RGBA[i+channel] = tables[channel][l.RGBA[i+channel]]
				}
			}
		}
	}
	return nil
}

func (l *Layer) DoBoxBlur(xRadius, yRadius int32) error {
	if err := validLayerPixels(l); err != nil {
		return err
	}
	rx, ry := int64(xRadius), int64(yRadius)
	if rx < 0 {
		rx = -rx
	}
	if ry < 0 {
		ry = -ry
	}
	if rx > 1024 || ry > 1024 {
		return fmt.Errorf("%w: Layer.doBoxBlur radius exceeds limit", ErrInvalidLayer)
	}
	if (rx*2+1)*(ry*2+1) > 1<<24 {
		return fmt.Errorf("%w: Layer.doBoxBlur kernel area exceeds limit", ErrInvalidLayer)
	}
	left, top, right, bottom := layerImageClip(l)
	if right <= left || bottom <= top || rx == 0 && ry == 0 {
		return nil
	}
	columnLeft := max64(int64(0), int64(left)-rx)
	columnRight := min(int64(l.ImageWidth), int64(right)+rx)
	columns := columnRight - columnLeft
	pixels := uint64(right-left) * uint64(bottom-top)
	if pixels > maxLayerPixels || pixels > uint64(maxIntValue()/4) || uint64(columns) > uint64(maxIntValue()/32) {
		return fmt.Errorf("%w: Layer.doBoxBlur temporary image exceeds limit", ErrInvalidLayer)
	}
	vertical := make([]int64, int(columns)*4)
	output := make([]byte, int(pixels)*4)
	alphaFace := l.drawFace() == layerFaceAlpha
	currentTop := max64(int64(0), int64(top)-ry)
	currentBottom := currentTop
	outputRow := int64(0)
	for y := int64(top); y < int64(bottom); y++ {
		wantTop := max64(int64(0), y-ry)
		wantBottom := min(int64(l.ImageHeight), y+ry+1)
		for currentBottom < wantBottom {
			row := currentBottom
			for x := columnLeft; x < columnRight; x++ {
				sourceOffset := (row*int64(l.ImageWidth) + x) * 4
				columnOffset := int((x - columnLeft) * 4)
				alpha := uint32(l.RGBA[sourceOffset+3])
				for channel := 0; channel < 4; channel++ {
					value := uint32(l.RGBA[sourceOffset+int64(channel)])
					if alphaFace && channel != 3 {
						value = value * (alpha + (alpha >> 7)) >> 8
					}
					vertical[columnOffset+channel] += int64(value)
				}
			}
			currentBottom++
		}
		for currentTop < wantTop {
			row := currentTop
			for x := columnLeft; x < columnRight; x++ {
				sourceOffset := (row*int64(l.ImageWidth) + x) * 4
				columnOffset := int((x - columnLeft) * 4)
				alpha := uint32(l.RGBA[sourceOffset+3])
				for channel := 0; channel < 4; channel++ {
					value := uint32(l.RGBA[sourceOffset+int64(channel)])
					if alphaFace && channel != 3 {
						value = value * (alpha + (alpha >> 7)) >> 8
					}
					vertical[columnOffset+channel] -= int64(value)
				}
			}
			currentTop++
		}
		windowLeft := max64(int64(0), int64(left)-rx)
		windowRight := min(int64(l.ImageWidth), int64(left)+rx+1)
		var sums [4]int64
		for x := windowLeft; x < windowRight; x++ {
			columnOffset := int((x - columnLeft) * 4)
			for channel := 0; channel < 4; channel++ {
				sums[channel] += vertical[columnOffset+channel]
			}
		}
		for x := int64(left); x < int64(right); x++ {
			windowRight = min(int64(l.ImageWidth), x+rx+1)
			count := (windowRight - windowLeft) * (wantBottom - wantTop)
			outOffset := (outputRow*int64(right-left) + x - int64(left)) * 4
			for channel := 0; channel < 4; channel++ {
				output[outOffset+int64(channel)] = byte((sums[channel] + count/2) / count)
			}
			if x+1 < int64(right) {
				leave := x - rx
				enter := x + rx + 1
				if leave >= 0 {
					columnOffset := int((leave - columnLeft) * 4)
					for channel := 0; channel < 4; channel++ {
						sums[channel] -= vertical[columnOffset+channel]
					}
					windowLeft++
				}
				if enter < int64(l.ImageWidth) {
					columnOffset := int((enter - columnLeft) * 4)
					for channel := 0; channel < 4; channel++ {
						sums[channel] += vertical[columnOffset+channel]
					}
				}
			}
		}
		outputRow++
	}
	if alphaFace {
		for i := 0; i+3 < len(output); i += 4 {
			alpha := uint32(output[i+3])
			for channel := 0; channel < 3; channel++ {
				if alpha == 0 {
					output[i+channel] = 0
				} else {
					output[i+channel] = byte(min(uint32(output[i+channel])*255/alpha, 255))
				}
			}
		}
	}
	rowBytes := int(right-left) * 4
	for row := int32(0); row < bottom-top; row++ {
		dst := (int(top+row)*int(l.ImageWidth) + int(left)) * 4
		src := int(row) * rowBytes
		copy(l.RGBA[dst:dst+rowBytes], output[src:src+rowBytes])
	}
	return nil
}

func maxIntValue() int {
	return int(^uint(0) >> 1)
}

// layerAffineCopy implements
// Layer.affineCopy(src, sleft, stop, swidth, sheight, affine, A, B, C, D, E, F,
// type=stNearest, clear=false). Points mode passes the destination coordinates
// of the source rectangle's top-left, top-right, and bottom-left edges; matrix
// mode passes an affine matrix whose coefficients address source pixels with a
// 1x1 extent, as tTVPBaseBitmap::AffineBlt does.
func (s *Services) layerAffineCopy(l *Layer, args []tjs.Value) error {
	const operation = "affineCopy"
	if len(args) < 12 {
		return fmt.Errorf("Layer.%s requires at least 12 arguments", operation)
	}
	source, err := s.nativeLayerImageSource(args[0], operation)
	if err != nil {
		return err
	}
	sleft, err := layerImageInt(args, 1, operation)
	if err != nil {
		return err
	}
	stop, err := layerImageInt(args, 2, operation)
	if err != nil {
		return err
	}
	swidth, err := layerImageInt(args, 3, operation)
	if err != nil {
		return err
	}
	sheight, err := layerImageInt(args, 4, operation)
	if err != nil {
		return err
	}
	affine, err := layerOptionalBool(args, 5, false, operation)
	if err != nil {
		return err
	}
	values := [6]float64{}
	for i := range values {
		values[i], err = layerImageOptionalFloat(args, 6+i, 0, operation)
		if err != nil {
			return err
		}
	}
	stretchType, err := layerImageOptionalInt(args, 12, 0, operation)
	if err != nil {
		return err
	}
	clear, err := layerOptionalBool(args, 13, false, operation)
	if err != nil {
		return err
	}
	if swidth == 0 || sheight == 0 {
		return nil
	}
	matrix := values
	if !affine {
		matrix[0] = (values[2] - values[0]) / float64(swidth)
		matrix[1] = (values[3] - values[1]) / float64(swidth)
		matrix[2] = (values[4] - values[0]) / float64(sheight)
		matrix[3] = (values[5] - values[1]) / float64(sheight)
		// Points mode supplies the destination position of the source
		// rectangle's top-left pixel edge. Convert that edge position into
		// the translation for the source-center coordinate system used by
		// affineCopyPixels.
		matrix[4] = values[0] + 0.5*(matrix[0]+matrix[2])
		matrix[5] = values[1] + 0.5*(matrix[1]+matrix[3])
	} else {
		matrix[4] = values[4] - 0.5*values[0] - 0.5*values[2]
		matrix[5] = values[5] - 0.5*values[1] - 0.5*values[3]
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.nativeLayerImageStillValid(source) {
		return fmt.Errorf("Layer.%s source was invalidated", operation)
	}
	if err := l.affineCopyPixels(source.pixels, source.width, source.height, sleft, stop, swidth, sheight, matrix, stretchType&0xffff, clear); err != nil {
		return err
	}
	l.imageModified = true
	return nil
}

func layerOptionalBool(args []tjs.Value, index int, defaultValue bool, operation string) (bool, error) {
	if index >= len(args) || args[index].IsVoid() {
		return defaultValue, nil
	}
	value, err := args[index].Truth()
	if err != nil {
		return false, fmt.Errorf("Layer.%s argument %d: %w", operation, index, err)
	}
	return value, nil
}

func (s *Services) nativeLayerImageSource(value tjs.Value, operation string) (nativeLayerImage, error) {
	if value.Kind() != tjs.KindObject || value.IsNull() {
		return nativeLayerImage{}, fmt.Errorf("Layer.%s requires a Layer or Bitmap source", operation)
	}
	id := value.ObjectRef().Object
	s.mu.RLock()
	layer, bitmap := s.Layers[id], s.Bitmaps[id]
	if layer != nil {
		if err := validLayerPixels(layer); err != nil {
			s.mu.RUnlock()
			return nativeLayerImage{}, err
		}
		source := nativeLayerImage{
			layer: layer, id: id, width: layer.ImageWidth, height: layer.ImageHeight,
			pixels: layer.RGBA, province: layer.Province, provinceWidth: layer.ProvinceWidth,
			provinceHeight: layer.ProvinceHeight, mode: operationModeForLayer(layer),
		}
		s.mu.RUnlock()
		return source, nil
	}
	if bitmap != nil {
		if bitmap.Width == 0 || bitmap.Height == 0 || uint64(bitmap.Width)*uint64(bitmap.Height)*4 != uint64(len(bitmap.RGBA)) || bitmap.Width > math.MaxInt32 || bitmap.Height > math.MaxInt32 {
			s.mu.RUnlock()
			return nativeLayerImage{}, fmt.Errorf("%w: invalid Bitmap source image", ErrInvalidLayer)
		}
		source := nativeLayerImage{bitmap: bitmap, id: id, width: int32(bitmap.Width), height: int32(bitmap.Height), pixels: bitmap.RGBA, mode: int32(LayerAlpha)}
		s.mu.RUnlock()
		return source, nil
	}
	s.mu.RUnlock()
	return nativeLayerImage{}, fmt.Errorf("Layer.%s source is not a Layer or Bitmap", operation)
}

func (s *Services) nativeLayerImageStillValid(source nativeLayerImage) bool {
	if source.layer != nil {
		layer := source.layer
		provinceCurrent := len(layer.Province) == len(source.province) &&
			layer.ProvinceWidth == source.provinceWidth && layer.ProvinceHeight == source.provinceHeight
		if provinceCurrent && len(source.province) != 0 {
			provinceCurrent = &layer.Province[0] == &source.province[0]
		}
		return s.Layers[source.id] == layer && layer.ImageWidth == source.width && layer.ImageHeight == source.height &&
			len(layer.RGBA) == len(source.pixels) && len(layer.RGBA) != 0 && &layer.RGBA[0] == &source.pixels[0] && provinceCurrent
	}
	if source.bitmap != nil {
		bitmap := source.bitmap
		return s.Bitmaps[source.id] == bitmap && int32(bitmap.Width) == source.width && int32(bitmap.Height) == source.height &&
			len(bitmap.RGBA) == len(source.pixels) && len(bitmap.RGBA) != 0 && &bitmap.RGBA[0] == &source.pixels[0]
	}
	return false
}
func layerImageInt(args []tjs.Value, index int, operation string) (int32, error) {
	if index < 0 || index >= len(args) {
		return 0, fmt.Errorf("Layer.%s: missing argument %d", operation, index)
	}
	value, err := args[index].Int()
	if err != nil {
		return 0, err
	}
	if value < math.MinInt32 || value > math.MaxInt32 {
		return 0, fmt.Errorf("Layer.%s: integer argument exceeds int32", operation)
	}
	return int32(value), nil
}

func layerImageOptionalInt(args []tjs.Value, index int, defaultValue int32, operation string) (int32, error) {
	if index >= len(args) || args[index].IsVoid() {
		return defaultValue, nil
	}
	return layerImageInt(args, index, operation)
}

func layerImageOptionalFloat(args []tjs.Value, index int, defaultValue float64, operation string) (float64, error) {
	if index >= len(args) || args[index].IsVoid() {
		return defaultValue, nil
	}
	value, err := args[index].Float()
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		if err == nil {
			err = fmt.Errorf("must be finite")
		}
		return 0, fmt.Errorf("Layer.%s argument %d value %s: %w", operation, index, args[index].Text(), err)
	}
	return value, nil
}

func (s *Services) layerImageNative(l *Layer, operation string, args []tjs.Value) error {
	switch operation {
	case "assignImages":
		if len(args) < 1 {
			return fmt.Errorf("Layer.assignImages: missing argument 0")
		}
		source, sourceID, err := s.layerFromValue(args[0])
		if err != nil {
			return err
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.Layers[sourceID] != source {
			return fmt.Errorf("Layer.assignImages source was invalidated")
		}
		return l.AssignImages(source)
	case "copyRect":
		if len(args) < 7 {
			return fmt.Errorf("Layer.copyRect requires at least 7 arguments")
		}
		dx, err := layerImageInt(args, 0, operation)
		if err != nil {
			return err
		}
		dy, err := layerImageInt(args, 1, operation)
		if err != nil {
			return err
		}
		source, err := s.nativeLayerImageSource(args[2], operation)
		if err != nil {
			return err
		}
		sx, err := layerImageInt(args, 3, operation)
		if err != nil {
			return err
		}
		sy, err := layerImageInt(args, 4, operation)
		if err != nil {
			return err
		}
		width, err := layerImageInt(args, 5, operation)
		if err != nil {
			return err
		}
		height, err := layerImageInt(args, 6, operation)
		if err != nil {
			return err
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if !s.nativeLayerImageStillValid(source) {
			return fmt.Errorf("Layer.%s source was invalidated", operation)
		}
		return l.copyRectPixels(source.pixels, source.width, source.height, source.province,
			source.provinceWidth, source.provinceHeight, dx, dy, sx, sy, width, height)
	case "operateRect":
		if len(args) < 7 {
			return fmt.Errorf("Layer.operateRect requires at least 7 arguments")
		}
		dx, err := layerImageInt(args, 0, operation)
		if err != nil {
			return err
		}
		dy, err := layerImageInt(args, 1, operation)
		if err != nil {
			return err
		}
		source, err := s.nativeLayerImageSource(args[2], operation)
		if err != nil {
			return err
		}
		sx, err := layerImageInt(args, 3, operation)
		if err != nil {
			return err
		}
		sy, err := layerImageInt(args, 4, operation)
		if err != nil {
			return err
		}
		width, err := layerImageInt(args, 5, operation)
		if err != nil {
			return err
		}
		height, err := layerImageInt(args, 6, operation)
		if err != nil {
			return err
		}
		mode, err := layerImageOptionalInt(args, 7, layerFaceAuto, operation)
		if err != nil {
			return err
		}
		if mode == layerFaceAuto {
			mode = source.mode
		}
		opacity, err := layerImageOptionalInt(args, 8, 255, operation)
		if err != nil {
			return err
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if !s.nativeLayerImageStillValid(source) {
			return fmt.Errorf("Layer.%s source was invalidated", operation)
		}
		return l.operateRectPixels(source.pixels, source.width, source.height, dx, dy, sx, sy, width, height, mode, opacity)
	case "stretchCopy":
		if len(args) < 9 {
			return fmt.Errorf("Layer.stretchCopy requires at least 9 arguments")
		}
		dx, err := layerImageInt(args, 0, operation)
		if err != nil {
			return err
		}
		dy, err := layerImageInt(args, 1, operation)
		if err != nil {
			return err
		}
		width, err := layerImageInt(args, 2, operation)
		if err != nil {
			return err
		}
		height, err := layerImageInt(args, 3, operation)
		if err != nil {
			return err
		}
		source, err := s.nativeLayerImageSource(args[4], operation)
		if err != nil {
			return err
		}
		sx, err := layerImageInt(args, 5, operation)
		if err != nil {
			return err
		}
		sy, err := layerImageInt(args, 6, operation)
		if err != nil {
			return err
		}
		sourceWidth, err := layerImageInt(args, 7, operation)
		if err != nil {
			return err
		}
		sourceHeight, err := layerImageInt(args, 8, operation)
		if err != nil {
			return err
		}
		stretchType, err := layerImageOptionalInt(args, 9, 0, operation)
		if err != nil {
			return err
		}
		if _, err := layerImageOptionalFloat(args, 10, 0, operation); err != nil {
			return err
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if !s.nativeLayerImageStillValid(source) {
			return fmt.Errorf("Layer.%s source was invalidated", operation)
		}
		return l.stretchCopyPixels(source.pixels, source.width, source.height, dx, dy, width, height, sx, sy, sourceWidth, sourceHeight, stretchType)
	case "doGrayScale":
		s.mu.Lock()
		defer s.mu.Unlock()
		return l.DoGrayScale()
	case "light":
		if len(args) < 2 {
			return fmt.Errorf("Layer.light requires at least 2 arguments")
		}
		light, err := layerImageInt(args, 0, operation)
		if err != nil {
			return err
		}
		contrast, err := layerImageInt(args, 1, operation)
		if err != nil {
			return err
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		return l.Light(light, contrast)
	case "adjustGamma":
		if len(args) == 0 {
			return nil
		}
		channels := [3]LayerGammaChannel{{Gamma: 1, Floor: 0, Ceiling: 255}, {Gamma: 1, Floor: 0, Ceiling: 255}, {Gamma: 1, Floor: 0, Ceiling: 255}}
		for channel := 0; channel < len(channels); channel++ {
			gamma, err := layerImageOptionalFloat(args, channel*3, 1, operation)
			if err != nil {
				return err
			}
			floor, err := layerImageOptionalInt(args, channel*3+1, 0, operation)
			if err != nil {
				return err
			}
			ceiling, err := layerImageOptionalInt(args, channel*3+2, 255, operation)
			if err != nil {
				return err
			}
			channels[channel] = LayerGammaChannel{Gamma: gamma, Floor: floor, Ceiling: ceiling}
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		return l.AdjustGamma(channels)
	case "doBoxBlur":
		rx, err := layerImageOptionalInt(args, 0, 1, operation)
		if err != nil {
			return err
		}
		ry, err := layerImageOptionalInt(args, 1, 1, operation)
		if err != nil {
			return err
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		return l.DoBoxBlur(rx, ry)
	default:
		return fmt.Errorf("%w: Layer.%s", ErrUnsupportedNative, operation)
	}
}
