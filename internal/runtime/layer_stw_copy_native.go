package runtime

import (
	"fmt"

	"github.com/eslzzyl/krkrgo/internal/tjs"
)

func installLayerStwCopyNative(vm *tjs.VM) error {
	layer, ok := vm.Globals["Layer"]
	if !ok {
		return fmt.Errorf("Layer class is unavailable for layerStwCopy.dll")
	}
	return vm.RegisterNativeMethod(layer, "stitchWrappedCopy", "Layer.stitchWrappedCopy")
}

// layerStitchWrappedCopy tiles a source rectangle into the receiver rectangle.
// The lllj script passes empty history arrays; the reference DLL's wrapped copy
// then repeats the source rectangle while clipping against each Layer's clip.
func (s *Services) layerStitchWrappedCopy(vm *tjs.VM, dstID tjs.ObjectID, dst *Layer, args []tjs.Value) error {
	const operation = "stitchWrappedCopy"
	if len(args) < 9 {
		return fmt.Errorf("Layer.%s requires destination rectangle, source Layer, and source rectangle", operation)
	}
	dx, err := layerImageInt(args, 0, operation)
	if err != nil {
		return err
	}
	dy, err := layerImageInt(args, 1, operation)
	if err != nil {
		return err
	}
	destWidth, err := layerImageInt(args, 2, operation)
	if err != nil {
		return err
	}
	destHeight, err := layerImageInt(args, 3, operation)
	if err != nil {
		return err
	}
	source, sourceID, err := s.layerFromValue(args[4])
	if err != nil {
		return fmt.Errorf("Layer.%s source: %w", operation, err)
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
	if len(args) > 9 && !args[9].IsVoid() {
		if vm == nil {
			return fmt.Errorf("Layer.%s requires a VM to inspect its history array", operation)
		}
		count, err := vm.GetArrayLength(args[9])
		if err != nil {
			return fmt.Errorf("Layer.%s history: %w", operation, err)
		}
		if count != 0 {
			return fmt.Errorf("%w: Layer.%s with non-empty history array", ErrUnsupportedNative, operation)
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Layers[dstID] != dst || s.Layers[sourceID] != source {
		return fmt.Errorf("Layer.%s source or destination was invalidated", operation)
	}
	if err := validLayerPixels(dst); err != nil {
		return fmt.Errorf("Layer.%s destination: %w", operation, err)
	}
	dst.ensureImageWritable()
	if err := validLayerPixels(source); err != nil {
		return fmt.Errorf("Layer.%s source: %w", operation, err)
	}
	dstLeft, dstTop, dstRight, dstBottom := layerImageClip(dst)
	srcLeft, srcTop, srcRight, srcBottom := layerImageClip(source)
	if destWidth < 0 {
		destWidth = dstRight - dstLeft
	}
	if destHeight < 0 {
		destHeight = dstBottom - dstTop
	}
	if sourceWidth < 0 {
		sourceWidth = srcRight - srcLeft
	}
	if sourceHeight < 0 {
		sourceHeight = srcBottom - srcTop
	}
	if destWidth == 0 || destHeight == 0 || sourceWidth <= 0 || sourceHeight <= 0 {
		return nil
	}
	dstClipWidth, dstClipHeight := int64(dstRight-dstLeft), int64(dstBottom-dstTop)
	if dstClipWidth <= 0 || dstClipHeight <= 0 {
		return nil
	}
	sourceRectLeft := max64(int64(sx), int64(srcLeft))
	sourceRectTop := max64(int64(sy), int64(srcTop))
	sourceRectRight := min(int64(sx)+int64(sourceWidth), int64(srcRight))
	sourceRectBottom := min(int64(sy)+int64(sourceHeight), int64(srcBottom))
	sourceRectWidth := sourceRectRight - sourceRectLeft
	sourceRectHeight := sourceRectBottom - sourceRectTop
	if sourceRectWidth <= 0 || sourceRectHeight <= 0 {
		return nil
	}
	left := max64(int64(dx), int64(dstLeft))
	top := max64(int64(dy), int64(dstTop))
	right := min(int64(dx)+int64(destWidth), int64(dstRight))
	bottom := min(int64(dy)+int64(destHeight), int64(dstBottom))
	if right <= left || bottom <= top {
		return nil
	}

	sourcePixels := source.RGBA
	if byteSlicesOverlap(sourcePixels, dst.RGBA) {
		sourcePixels = append([]byte(nil), sourcePixels...)
	}
	for y := top; y < bottom; y++ {
		sourceY := sourceRectTop + (y-int64(dy))%sourceRectHeight
		for x := left; x < right; x++ {
			sourceX := sourceRectLeft + (x-int64(dx))%sourceRectWidth
			sourceOffset := (sourceY*int64(source.ImageWidth) + sourceX) * 4
			destOffset := (y*int64(dst.ImageWidth) + x) * 4
			copy(dst.RGBA[destOffset:destOffset+4], sourcePixels[sourceOffset:sourceOffset+4])
		}
	}
	dst.imageModified = true
	dst.markImageDirtyRect(Rect{X: int32(left), Y: int32(top), Width: int32(right - left), Height: int32(bottom - top)})
	return nil
}
