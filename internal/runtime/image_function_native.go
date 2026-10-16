package runtime

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"strconv"

	"github.com/eslzzyl/krkrgo/internal/tjs"
)

// ImageFunction exposes the bounded, pure-Go subset of krkrz's static image
// helpers. Bitmap pixels remain non-premultiplied RGBA bytes; script colors are
// interpreted as 0xAARRGGBB.
func imageFunctionUnsupported(detail string) error {
	return fmt.Errorf("%w: ImageFunction %s", ErrUnsupportedNative, detail)
}

func imageOptionalBool(args []tjs.Value, index int, def bool, operation string) (bool, error) {
	if index >= len(args) || args[index].IsVoid() {
		return def, nil
	}
	value, err := args[index].Truth()
	if err != nil {
		return false, fmt.Errorf("ImageFunction.%s argument %d: %w", operation, index, err)
	}
	return value, nil
}

func imageOptionalInt(args []tjs.Value, index int, def int64, operation string) (int64, error) {
	if index >= len(args) || args[index].IsVoid() {
		return def, nil
	}
	value, err := args[index].Int()
	if err != nil {
		return 0, fmt.Errorf("ImageFunction.%s argument %d: %w", operation, index, err)
	}
	return value, nil
}

func imageOptionalFloat(args []tjs.Value, index int, def float64, operation string) (float64, error) {
	if index >= len(args) || args[index].IsVoid() {
		return def, nil
	}
	value, err := args[index].Float()
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		if err == nil {
			err = fmt.Errorf("must be finite")
		}
		return 0, fmt.Errorf("ImageFunction.%s argument %d: %w", operation, index, err)
	}
	return value, nil
}

func imageOptionalRect(s *Services, vm *tjs.VM, value tjs.Value, def scriptRect, operation string) (scriptRect, error) {
	if value.IsVoid() || value.IsNull() {
		return def, nil
	}
	rect, err := s.rectArgument(vm, value, operation)
	if err != nil {
		return scriptRect{}, err
	}
	return *rect, nil
}

func imageRect(left, top, right, bottom int32) scriptRect {
	return scriptRect{left: left, top: top, right: right, bottom: bottom}
}

func imageIntersect(a, b scriptRect) (scriptRect, bool) {
	out := a
	if b.left > out.left {
		out.left = b.left
	}
	if b.top > out.top {
		out.top = b.top
	}
	if b.right < out.right {
		out.right = b.right
	}
	if b.bottom < out.bottom {
		out.bottom = b.bottom
	}
	return out, out.right > out.left && out.bottom > out.top
}

func imageBounds(b *Bitmap) scriptRect {
	return imageRect(0, 0, int32(b.Width), int32(b.Height))
}

func imageClip(s *Services, vm *tjs.VM, value tjs.Value, b *Bitmap, operation string) (scriptRect, bool, error) {
	clip, err := imageOptionalRect(s, vm, value, imageBounds(b), operation)
	if err != nil {
		return scriptRect{}, false, err
	}
	clip, ok := imageIntersect(clip, imageBounds(b))
	return clip, ok, nil
}

func imageBitmapArg(s *Services, args []tjs.Value, index int, operation string) (*Bitmap, tjs.ObjectID, error) {
	if index >= len(args) {
		return nil, 0, fmt.Errorf("ImageFunction.%s requires argument %d", operation, index)
	}
	return s.bitmapFromValue(args[index])
}

func imageClampByte(value int64) uint8 {
	if value <= 0 {
		return 0
	}
	if value >= 255 {
		return 255
	}
	return uint8(value)
}

func imageColor(value int64) (r, g, b, a uint8) {
	bits := uint64(value)
	return uint8(bits >> 16), uint8(bits >> 8), uint8(bits), uint8(bits >> 24)
}

func imageBlendMode(mode, face int64) (LayerType, LayerType, error) {
	if mode == 128 { // omAuto
		mode = int64(LayerAlpha)
	}
	if face != 0 && face != 1 && face != 4 { // dfAlpha, dfOpaque, dfAddAlpha
		return 0, 0, imageFunctionUnsupported(fmt.Sprintf("draw face %d", face))
	}
	if mode < int64(LayerAlpha) || mode > int64(LayerPSExclusion) {
		return 0, 0, imageFunctionUnsupported(fmt.Sprintf("blend mode %d", mode))
	}
	if mode == int64(LayerAddAlpha) && face == 0 {
		return 0, 0, imageFunctionUnsupported("add-alpha mode on alpha destination")
	}
	return LayerType(mode), LayerType(faceToTarget(face)), nil
}

func faceToTarget(face int64) LayerType {
	switch face {
	case 1:
		return LayerOpaque
	case 4:
		return LayerAddAlpha
	default:
		return LayerAlpha
	}
}

func imageBlend(dst, src []byte, mode, face, opa int64) error {
	typ, target, err := imageBlendMode(mode, face)
	if err != nil {
		return err
	}
	if opa < 0 || opa > 255 {
		return fmt.Errorf("ImageFunction opacity %d outside 0..255", opa)
	}
	blendPixelForTarget(dst, src, uint8(opa), typ, target)
	return nil
}

func imageCopySource(src *Bitmap, same bool) []byte {
	if same {
		return append([]byte(nil), src.RGBA...)
	}
	return src.RGBA
}

// imageRectBlt clips a source rectangle and destination point to both images
// and the caller's clip rectangle. It returns matching source/destination
// rectangles with equal dimensions.
func imageRectBlt(dst, src *Bitmap, dx, dy int64, sr, clip scriptRect) (scriptRect, scriptRect, bool) {
	if sr.left < 0 {
		dx -= int64(sr.left)
		sr.left = 0
	}
	if sr.top < 0 {
		dy -= int64(sr.top)
		sr.top = 0
	}
	if sr.right > int32(src.Width) {
		sr.right = int32(src.Width)
	}
	if sr.bottom > int32(src.Height) {
		sr.bottom = int32(src.Height)
	}
	if sr.right <= sr.left || sr.bottom <= sr.top {
		return scriptRect{}, scriptRect{}, false
	}
	dr := imageRect(int32(dx), int32(dy), int32(dx)+sr.right-sr.left, int32(dy)+sr.bottom-sr.top)
	clip, ok := imageIntersect(clip, imageBounds(dst))
	if !ok {
		return scriptRect{}, scriptRect{}, false
	}
	if dr.left < clip.left {
		delta := clip.left - dr.left
		dr.left = clip.left
		sr.left += delta
	}
	if dr.top < clip.top {
		delta := clip.top - dr.top
		dr.top = clip.top
		sr.top += delta
	}
	if dr.right > clip.right {
		delta := dr.right - clip.right
		dr.right = clip.right
		sr.right -= delta
	}
	if dr.bottom > clip.bottom {
		delta := dr.bottom - clip.bottom
		dr.bottom = clip.bottom
		sr.bottom -= delta
	}
	if dr.right <= dr.left || dr.bottom <= dr.top || sr.right <= sr.left || sr.bottom <= sr.top {
		return scriptRect{}, scriptRect{}, false
	}
	return dr, sr, true
}

func (s *Services) imageOperateRect(vm *tjs.VM, args []tjs.Value) (tjs.Value, error) {
	const operation = "operateRect"
	if len(args) < 4 {
		return tjs.VoidValue, fmt.Errorf("ImageFunction.%s requires at least 4 arguments", operation)
	}
	dst, dstID, err := imageBitmapArg(s, args, 0, operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	dx, err := args[1].Int()
	if err != nil {
		return tjs.VoidValue, err
	}
	dy, err := args[2].Int()
	if err != nil {
		return tjs.VoidValue, err
	}
	src, srcID, err := imageBitmapArg(s, args, 3, operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	sr, err := imageOptionalRect(s, vm, valueAt(args, 4), imageBounds(src), operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	clip, err := imageOptionalRect(s, vm, valueAt(args, 5), imageBounds(dst), operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	mode, err := imageOptionalInt(args, 6, int64(LayerAlpha), operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	face, err := imageOptionalInt(args, 7, 0, operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	opa, err := imageOptionalInt(args, 8, 255, operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	hda, err := imageOptionalBool(args, 9, false, operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	if hda {
		return tjs.VoidValue, imageFunctionUnsupported("hda=true")
	}
	if _, _, err := imageBlendMode(mode, face); err != nil {
		return tjs.VoidValue, err
	}
	dr, sr, ok := imageRectBlt(dst, src, dx, dy, sr, clip)
	if !ok {
		return tjs.VoidValue, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	source := imageCopySource(src, dstID == srcID)
	for y := dr.top; y < dr.bottom; y++ {
		for x := dr.left; x < dr.right; x++ {
			sx := sr.left + (x - dr.left)
			sy := sr.top + (y - dr.top)
			di := (int(y)*int(dst.Width) + int(x)) * 4
			si := (int(sy)*int(src.Width) + int(sx)) * 4
			if err := imageBlend(dst.RGBA[di:di+4], source[si:si+4], mode, face, opa); err != nil {
				return tjs.VoidValue, err
			}
		}
	}
	dst.markDirty()
	return tjs.VoidValue, nil
}

func valueAt(args []tjs.Value, index int) tjs.Value {
	if index < 0 || index >= len(args) {
		return tjs.VoidValue
	}
	return args[index]
}

func (s *Services) imageStretch(dst, src *Bitmap, dstID, srcID tjs.ObjectID, dr, sr, clip scriptRect, mode, face, opa int64) error {
	if dr.left > dr.right {
		dr.left, dr.right = dr.right, dr.left
	}
	if dr.top > dr.bottom {
		dr.top, dr.bottom = dr.bottom, dr.top
	}
	if sr.left > sr.right {
		sr.left, sr.right = sr.right, sr.left
	}
	if sr.top > sr.bottom {
		sr.top, sr.bottom = sr.bottom, sr.top
	}
	if dr.right <= dr.left || dr.bottom <= dr.top || sr.right <= sr.left || sr.bottom <= sr.top {
		return nil
	}
	if sr.left < 0 || sr.top < 0 || sr.right > int32(src.Width) || sr.bottom > int32(src.Height) {
		return imageFunctionUnsupported("stretch source rectangle outside source bitmap")
	}
	clip, ok := imageIntersect(clip, imageBounds(dst))
	if !ok {
		return nil
	}
	orig := dr
	dr, ok = imageIntersect(dr, clip)
	if !ok {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	source := imageCopySource(src, dstID == srcID)
	mapDW, mapDH := int64(orig.right-orig.left), int64(orig.bottom-orig.top)
	for y := dr.top; y < dr.bottom; y++ {
		for x := dr.left; x < dr.right; x++ {
			sx := sr.left + int32(int64(x-orig.left)*int64(sr.right-sr.left)/mapDW)
			sy := sr.top + int32(int64(y-orig.top)*int64(sr.bottom-sr.top)/mapDH)
			if sx >= sr.right {
				sx = sr.right - 1
			}
			if sy >= sr.bottom {
				sy = sr.bottom - 1
			}
			di := (int(y)*int(dst.Width) + int(x)) * 4
			si := (int(sy)*int(src.Width) + int(sx)) * 4
			if err := imageBlend(dst.RGBA[di:di+4], source[si:si+4], mode, face, opa); err != nil {
				return err
			}
		}
	}
	dst.markDirty()
	return nil
}

// imageAffine applies an affine matrix to source pixel centers. The scan is
// limited to the transformed source bounds intersected with the destination
// clip, so its work and any overlap snapshot are bounded by the bitmaps.
func (s *Services) imageAffine(dst, src *Bitmap, dstID, srcID tjs.ObjectID, matrix [6]float64, sr, clip scriptRect, mode, face, opa int64) error {
	if sr.left >= sr.right || sr.top >= sr.bottom {
		return nil
	}
	if sr.left < 0 || sr.top < 0 || sr.right > int32(src.Width) || sr.bottom > int32(src.Height) {
		return imageFunctionUnsupported("affine source rectangle outside source bitmap")
	}

	a, b, c, d, tx, ty := matrix[0], matrix[1], matrix[2], matrix[3], matrix[4], matrix[5]
	determinant := a*d - b*c
	if math.IsNaN(determinant) || math.IsInf(determinant, 0) {
		return imageFunctionUnsupported("affine transform outside safe numeric range")
	}
	if determinant == 0 {
		return nil
	}

	sourceWidth := float64(sr.right - sr.left)
	sourceHeight := float64(sr.bottom - sr.top)
	corners := [4][2]float64{
		{-0.5, -0.5},
		{sourceWidth - 0.5, -0.5},
		{sourceWidth - 0.5, sourceHeight - 0.5},
		{-0.5, sourceHeight - 0.5},
	}
	minX, maxX := math.Inf(1), math.Inf(-1)
	minY, maxY := math.Inf(1), math.Inf(-1)
	for _, corner := range corners {
		x := a*corner[0] + c*corner[1] + tx
		y := b*corner[0] + d*corner[1] + ty
		if math.IsNaN(x) || math.IsInf(x, 0) || math.IsNaN(y) || math.IsInf(y, 0) {
			return imageFunctionUnsupported("affine transform outside safe numeric range")
		}
		if x < minX {
			minX = x
		}
		if x > maxX {
			maxX = x
		}
		if y < minY {
			minY = y
		}
		if y > maxY {
			maxY = y
		}
	}

	clip, ok := imageIntersect(clip, imageBounds(dst))
	if !ok || maxX <= float64(clip.left) || minX >= float64(clip.right) ||
		maxY <= float64(clip.top) || minY >= float64(clip.bottom) {
		return nil
	}
	left, right, top, bottom := clip.left, clip.right, clip.top, clip.bottom
	if minX > float64(left) {
		left = int32(math.Ceil(minX))
	}
	if maxX < float64(right) {
		right = int32(math.Ceil(maxX))
	}
	if minY > float64(top) {
		top = int32(math.Ceil(minY))
	}
	if maxY < float64(bottom) {
		bottom = int32(math.Ceil(maxY))
	}
	if left >= right || top >= bottom {
		return nil
	}

	blendType, target, err := imageBlendMode(mode, face)
	if err != nil {
		return err
	}
	if opa < 0 || opa > 255 {
		return fmt.Errorf("ImageFunction opacity %d outside 0..255", opa)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	source := imageCopySource(src, dstID == srcID)
	for y := top; y < bottom; y++ {
		for x := left; x < right; x++ {
			dx, dy := float64(x)-tx, float64(y)-ty
			sx := (dx*d - dy*c) / determinant
			sy := (dy*a - dx*b) / determinant
			if math.IsNaN(sx) || math.IsInf(sx, 0) || math.IsNaN(sy) || math.IsInf(sy, 0) ||
				sx < -0.5 || sx >= sourceWidth-0.5 || sy < -0.5 || sy >= sourceHeight-0.5 {
				continue
			}
			sourceX := sr.left + int32(math.Floor(sx+0.5))
			sourceY := sr.top + int32(math.Floor(sy+0.5))
			di := (int(y)*int(dst.Width) + int(x)) * 4
			si := (int(sourceY)*int(src.Width) + int(sourceX)) * 4
			blendPixelForTarget(dst.RGBA[di:di+4], source[si:si+4], uint8(opa), blendType, target)
		}
	}
	dst.markDirty()
	return nil
}

func (s *Services) imageOperateStretch(vm *tjs.VM, args []tjs.Value) (tjs.Value, error) {
	const operation = "operateStretch"
	if len(args) < 2 {
		return tjs.VoidValue, fmt.Errorf("ImageFunction.%s requires at least 2 arguments", operation)
	}
	dst, dstID, err := imageBitmapArg(s, args, 0, operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	src, srcID, err := imageBitmapArg(s, args, 1, operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	dr, err := imageOptionalRect(s, vm, valueAt(args, 2), imageBounds(dst), operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	sr, err := imageOptionalRect(s, vm, valueAt(args, 3), imageBounds(src), operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	clip, err := imageOptionalRect(s, vm, valueAt(args, 4), imageBounds(dst), operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	mode, err := imageOptionalInt(args, 5, int64(LayerAlpha), operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	face, err := imageOptionalInt(args, 6, 0, operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	opa, err := imageOptionalInt(args, 7, 255, operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	typ, err := imageOptionalInt(args, 8, 0, operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	hda, err := imageOptionalBool(args, 9, false, operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	if typ != 0 {
		return tjs.VoidValue, imageFunctionUnsupported(fmt.Sprintf("stretch type %d", typ))
	}
	if hda {
		return tjs.VoidValue, imageFunctionUnsupported("hda=true")
	}
	if _, _, err := imageBlendMode(mode, face); err != nil {
		return tjs.VoidValue, err
	}
	if err := s.imageStretch(dst, src, dstID, srcID, dr, sr, clip, mode, face, opa); err != nil {
		return tjs.VoidValue, err
	}
	return tjs.VoidValue, nil
}

func (s *Services) imageOperateAffine(vm *tjs.VM, args []tjs.Value) (tjs.Value, error) {
	const operation = "operateAffine"
	if len(args) < 11 {
		return tjs.VoidValue, fmt.Errorf("ImageFunction.%s requires at least 11 arguments", operation)
	}
	dst, dstID, err := imageBitmapArg(s, args, 0, operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	src, srcID, err := imageBitmapArg(s, args, 1, operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	values := [6]float64{}
	for i := range values {
		values[i], err = args[i+2].Float()
		if err != nil || math.IsNaN(values[i]) || math.IsInf(values[i], 0) {
			return tjs.VoidValue, fmt.Errorf("ImageFunction.%s coordinate %d must be finite", operation, i)
		}
	}
	sr, err := imageOptionalRect(s, vm, valueAt(args, 8), imageBounds(src), operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	clip, err := imageOptionalRect(s, vm, valueAt(args, 9), imageBounds(dst), operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	affine, err := imageOptionalBool(args, 10, true, operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	mode, err := imageOptionalInt(args, 11, int64(LayerAlpha), operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	face, err := imageOptionalInt(args, 12, 0, operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	opa, err := imageOptionalInt(args, 13, 255, operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	typ, err := imageOptionalInt(args, 14, 0, operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	hda, err := imageOptionalBool(args, 15, false, operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	if typ != 0 {
		return tjs.VoidValue, imageFunctionUnsupported(fmt.Sprintf("affine type %d", typ))
	}
	if hda {
		return tjs.VoidValue, imageFunctionUnsupported("hda=true")
	}
	if _, _, err := imageBlendMode(mode, face); err != nil {
		return tjs.VoidValue, err
	}
	if affine {
		if err := s.imageAffine(dst, src, dstID, srcID, values, sr, clip, mode, face, opa); err != nil {
			return tjs.VoidValue, err
		}
		return tjs.VoidValue, nil
	}
	// Point mode is supported only for an axis-aligned rectangle.
	if values[1] != values[3] || values[0] != values[4] {
		return tjs.VoidValue, imageFunctionUnsupported("rotated point transform")
	}
	dr := imageRect(int32(values[0]), int32(values[1]), int32(values[2]), int32(values[5]))
	if err := s.imageStretch(dst, src, dstID, srcID, dr, sr, clip, mode, face, opa); err != nil {
		return tjs.VoidValue, err
	}
	return tjs.VoidValue, nil
}

func (s *Services) imageFlip(vm *tjs.VM, args []tjs.Value, horizontal bool) (tjs.Value, error) {
	operation := "flipUD"
	if horizontal {
		operation = "flipLR"
	}
	if len(args) < 1 {
		return tjs.VoidValue, fmt.Errorf("ImageFunction.%s requires a Bitmap", operation)
	}
	b, _, err := imageBitmapArg(s, args, 0, operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	rect, ok, err := imageClip(s, vm, valueAt(args, 1), b, operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	if !ok {
		return tjs.VoidValue, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if horizontal {
		for y := rect.top; y < rect.bottom; y++ {
			for left, right := rect.left, rect.right-1; left < right; left, right = left+1, right-1 {
				li := (int(y)*int(b.Width) + int(left)) * 4
				ri := (int(y)*int(b.Width) + int(right)) * 4
				for c := 0; c < 4; c++ {
					b.RGBA[li+c], b.RGBA[ri+c] = b.RGBA[ri+c], b.RGBA[li+c]
				}
			}
		}
	} else {
		for top, bottom := rect.top, rect.bottom-1; top < bottom; top, bottom = top+1, bottom-1 {
			for x := rect.left; x < rect.right; x++ {
				ti := (int(top)*int(b.Width) + int(x)) * 4
				bi := (int(bottom)*int(b.Width) + int(x)) * 4
				for c := 0; c < 4; c++ {
					b.RGBA[ti+c], b.RGBA[bi+c] = b.RGBA[bi+c], b.RGBA[ti+c]
				}
			}
		}
	}
	b.markDirty()
	return tjs.VoidValue, nil
}

func (s *Services) imageAdjustGamma(vm *tjs.VM, args []tjs.Value) (tjs.Value, error) {
	const operation = "adjustGamma"
	if len(args) < 1 {
		return tjs.VoidValue, fmt.Errorf("ImageFunction.%s requires a Bitmap", operation)
	}
	b, _, err := imageBitmapArg(s, args, 0, operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	rg, err := imageOptionalFloat(args, 1, 1, operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	rf, err := imageOptionalFloat(args, 2, 0, operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	rc, err := imageOptionalFloat(args, 3, 255, operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	gg, err := imageOptionalFloat(args, 4, 1, operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	gf, err := imageOptionalFloat(args, 5, 0, operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	gc, err := imageOptionalFloat(args, 6, 255, operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	bg, err := imageOptionalFloat(args, 7, 1, operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	bf, err := imageOptionalFloat(args, 8, 0, operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	bc, err := imageOptionalFloat(args, 9, 255, operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	clip, ok, err := imageClip(s, vm, valueAt(args, 10), b, operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	addAlpha, err := imageOptionalBool(args, 11, false, operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	if addAlpha {
		return tjs.VoidValue, imageFunctionUnsupported("additive-alpha gamma adjustment")
	}
	if !ok {
		return tjs.VoidValue, nil
	}
	transform := func(value uint8, gamma, floor, ceil float64) uint8 {
		result := floor + math.Pow(float64(value)/255, gamma)*(ceil-floor)
		if result < 0 {
			result = 0
		} else if result > 255 {
			result = 255
		}
		return uint8(math.Round(result))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for y := clip.top; y < clip.bottom; y++ {
		for x := clip.left; x < clip.right; x++ {
			i := (int(y)*int(b.Width) + int(x)) * 4
			b.RGBA[i], b.RGBA[i+1], b.RGBA[i+2] = transform(b.RGBA[i], rg, rf, rc), transform(b.RGBA[i+1], gg, gf, gc), transform(b.RGBA[i+2], bg, bf, bc)
		}
	}
	b.markDirty()
	return tjs.VoidValue, nil
}

func (s *Services) imageGrayScale(vm *tjs.VM, args []tjs.Value) (tjs.Value, error) {
	const operation = "doGrayScale"
	if len(args) < 1 {
		return tjs.VoidValue, fmt.Errorf("ImageFunction.%s requires a Bitmap", operation)
	}
	b, _, err := imageBitmapArg(s, args, 0, operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	clip, ok, err := imageClip(s, vm, valueAt(args, 1), b, operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	if !ok {
		return tjs.VoidValue, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for y := clip.top; y < clip.bottom; y++ {
		for x := clip.left; x < clip.right; x++ {
			i := (int(y)*int(b.Width) + int(x)) * 4
			gray := uint8((77*int(b.RGBA[i]) + 150*int(b.RGBA[i+1]) + 29*int(b.RGBA[i+2]) + 128) >> 8)
			b.RGBA[i], b.RGBA[i+1], b.RGBA[i+2] = gray, gray, gray
		}
	}
	b.markDirty()
	return tjs.VoidValue, nil
}

func (s *Services) imageFillRect(vm *tjs.VM, args []tjs.Value) (tjs.Value, error) {
	const operation = "fillRect"
	if len(args) < 2 {
		return tjs.VoidValue, fmt.Errorf("ImageFunction.%s requires Bitmap and value", operation)
	}
	b, _, err := imageBitmapArg(s, args, 0, operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	value, err := args[1].Int()
	if err != nil {
		return tjs.VoidValue, err
	}
	rect, err := imageOptionalRect(s, vm, valueAt(args, 2), imageBounds(b), operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	isAlpha, err := imageOptionalBool(args, 3, true, operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	clip, err := imageOptionalRect(s, vm, valueAt(args, 4), imageBounds(b), operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	rect, ok := imageIntersect(rect, clip)
	if ok {
		rect, ok = imageIntersect(rect, imageBounds(b))
	}
	if !ok {
		return tjs.VoidValue, nil
	}
	r, g, bl, a := imageColor(value)
	s.mu.Lock()
	defer s.mu.Unlock()
	for y := rect.top; y < rect.bottom; y++ {
		for x := rect.left; x < rect.right; x++ {
			i := (int(y)*int(b.Width) + int(x)) * 4
			b.RGBA[i], b.RGBA[i+1], b.RGBA[i+2] = r, g, bl
			if isAlpha {
				b.RGBA[i+3] = a
			}
		}
	}
	b.markDirty()
	return tjs.VoidValue, nil
}

func (s *Services) imageColorRect(vm *tjs.VM, args []tjs.Value) (tjs.Value, error) {
	const operation = "colorRect"
	if len(args) < 2 {
		return tjs.VoidValue, fmt.Errorf("ImageFunction.%s requires Bitmap and value", operation)
	}
	b, _, err := imageBitmapArg(s, args, 0, operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	value, err := args[1].Int()
	if err != nil {
		return tjs.VoidValue, err
	}
	opa, err := imageOptionalInt(args, 2, 255, operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	if opa < -255 {
		opa = -255
	} else if opa > 255 {
		opa = 255
	}
	face, err := imageOptionalInt(args, 4, 0, operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	if face != 0 && face != 1 && face != 4 {
		return tjs.VoidValue, imageFunctionUnsupported(fmt.Sprintf("draw face %d", face))
	}
	rect, err := imageOptionalRect(s, vm, valueAt(args, 3), imageBounds(b), operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	clip, err := imageOptionalRect(s, vm, valueAt(args, 5), imageBounds(b), operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	rect, ok := imageIntersect(rect, clip)
	if ok {
		rect, ok = imageIntersect(rect, imageBounds(b))
	}
	if !ok {
		return tjs.VoidValue, nil
	}
	r, g, bl, a := imageColor(value)
	src := [4]byte{r, g, bl, 0}
	s.mu.Lock()
	defer s.mu.Unlock()
	for y := rect.top; y < rect.bottom; y++ {
		for x := rect.left; x < rect.right; x++ {
			i := (int(y)*int(b.Width) + int(x)) * 4
			switch face {
			case 1:
				if opa <= 0 {
					continue
				}
				for c := 0; c < 3; c++ {
					b.RGBA[i+c] = byte((int(b.RGBA[i+c])*(255-int(opa)) + int(src[c])*int(opa) + 127) / 255)
				}
			case 0:
				if opa < 0 {
					level := -opa
					b.RGBA[i+3] = byte(int(b.RGBA[i+3]) * (255 - int(level)) / 255)
					continue
				}
				src[3] = uint8(int(a) * int(opa) / 255)
				if err := imageBlend(b.RGBA[i:i+4], src[:], int64(LayerAlpha), face, 255); err != nil {
					return tjs.VoidValue, err
				}
			case 4:
				if opa < 0 {
					continue
				}
				src[3] = uint8(int(a) * int(opa) / 255)
				if err := imageBlend(b.RGBA[i:i+4], src[:], int64(LayerAddAlpha), face, 255); err != nil {
					return tjs.VoidValue, err
				}
			}
		}
	}
	b.markDirty()
	return tjs.VoidValue, nil
}

func (s *Services) imageBoxBlur(vm *tjs.VM, args []tjs.Value) (tjs.Value, error) {
	const operation = "doBoxBlur"
	if len(args) < 1 {
		return tjs.VoidValue, fmt.Errorf("ImageFunction.%s requires a Bitmap", operation)
	}
	b, _, err := imageBitmapArg(s, args, 0, operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	xblur, err := imageOptionalInt(args, 1, 1, operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	yblur, err := imageOptionalInt(args, 2, 1, operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	if xblur < 0 || yblur < 0 || xblur > 1024 || yblur > 1024 {
		return tjs.VoidValue, imageFunctionUnsupported("box blur radius")
	}
	clip, ok, err := imageClip(s, vm, valueAt(args, 3), b, operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	isAlpha, err := imageOptionalBool(args, 4, true, operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	if !ok {
		return tjs.VoidValue, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	source := append([]byte(nil), b.RGBA...)
	for y := clip.top; y < clip.bottom; y++ {
		for x := clip.left; x < clip.right; x++ {
			minX, maxX := int64(x)-xblur, int64(x)+xblur
			minY, maxY := int64(y)-yblur, int64(y)+yblur
			if minX < 0 {
				minX = 0
			}
			if minY < 0 {
				minY = 0
			}
			if maxX >= int64(b.Width) {
				maxX = int64(b.Width) - 1
			}
			if maxY >= int64(b.Height) {
				maxY = int64(b.Height) - 1
			}
			count := int64(0)
			sums := [4]int64{}
			for sy := minY; sy <= maxY; sy++ {
				for sx := minX; sx <= maxX; sx++ {
					i := (sy*int64(b.Width) + sx) * 4
					for c := 0; c < 4; c++ {
						sums[c] += int64(source[i+int64(c)])
					}
					count++
				}
			}
			i := (int64(y)*int64(b.Width) + int64(x)) * 4
			for c := 0; c < 4; c++ {
				if isAlpha && c != 3 {
					continue
				}
				b.RGBA[i+int64(c)] = uint8((sums[c] + count/2) / count)
			}
		}
	}
	b.markDirty()
	return tjs.VoidValue, nil
}

type imageDrawConfig struct {
	x, y    int64
	color   [3]uint8
	opacity uint8
	mode    LayerType
	target  LayerType
	clip    scriptRect
}

func imageDrawOptions(args []tjs.Value, operation string, xIndex, yIndex, colorIndex int, allowNoAntialias bool) (imageDrawConfig, error) {
	var config imageDrawConfig
	x, err := imageOptionalInt(args, xIndex, 0, operation)
	if err != nil {
		return config, err
	}
	y, err := imageOptionalInt(args, yIndex, 0, operation)
	if err != nil {
		return config, err
	}
	if x < math.MinInt32 || x > math.MaxInt32 || y < math.MinInt32 || y > math.MaxInt32 {
		return config, fmt.Errorf("ImageFunction.%s: position exceeds int32", operation)
	}
	colorValue, err := imageOptionalInt(args, colorIndex, 0, operation)
	if err != nil {
		return config, err
	}
	r, g, b, _ := imageColor(colorValue)
	config.x, config.y, config.color = x, y, [3]uint8{r, g, b}

	opacity, err := imageOptionalInt(args, colorIndex+1, 255, operation)
	if err != nil {
		return config, err
	}
	antialias, err := imageOptionalBool(args, colorIndex+2, true, operation)
	if err != nil {
		return config, err
	}
	face, err := imageOptionalInt(args, colorIndex+3, 0, operation)
	if err != nil {
		return config, err
	}
	mode := int64(LayerAlpha)
	if face == 4 {
		mode = int64(LayerAddAlpha)
	}
	config.mode, config.target, err = imageBlendMode(mode, face)
	if err != nil {
		return config, err
	}
	shadowLevel, err := imageOptionalInt(args, colorIndex+4, 0, operation)
	if err != nil {
		return config, err
	}
	if shadowLevel != 0 {
		return config, imageFunctionUnsupported("text and glyph shadows")
	}
	hda, err := imageOptionalBool(args, colorIndex+9, false, operation)
	if err != nil {
		return config, err
	}
	if hda {
		return config, imageFunctionUnsupported("hda text and glyph drawing")
	}
	if !antialias && !allowNoAntialias {
		return config, imageFunctionUnsupported("non-antialiased text rasterization")
	}
	if face == 4 && opacity < 0 {
		return config, fmt.Errorf("ImageFunction.%s: negative opacity on add-alpha draw face", operation)
	}
	if face == 0 && opacity < 0 {
		return config, imageFunctionUnsupported("negative text and glyph opacity")
	}
	if opacity < 0 {
		opacity = 0
	} else if opacity > 255 {
		opacity = 255
	}
	config.opacity = uint8(opacity)
	return config, nil
}

func (s *Services) imageDrawClip(vm *tjs.VM, args []tjs.Value, b *Bitmap, clipIndex int, operation string) (scriptRect, error) {
	s.mu.RLock()
	bounds := imageBounds(b)
	hasImage := len(b.RGBA) != 0
	s.mu.RUnlock()
	if !hasImage {
		return scriptRect{}, fmt.Errorf("ImageFunction.%s: Bitmap has no image", operation)
	}
	clip, err := imageOptionalRect(s, vm, valueAt(args, clipIndex), bounds, operation)
	if err != nil {
		return scriptRect{}, err
	}
	clip, _ = imageIntersect(clip, bounds)
	return clip, nil
}

func imageDrawMaskLocked(b *Bitmap, mask []byte, maskWidth, maskHeight, maskStride, pixelStride, alphaOffset int, x, y int64, clip scriptRect, config imageDrawConfig) {
	clip, ok := imageIntersect(clip, imageBounds(b))
	if !ok || config.opacity == 0 || maskWidth <= 0 || maskHeight <= 0 {
		return
	}
	left := max64(int64(clip.left), x)
	top := max64(int64(clip.top), y)
	right := min64(int64(clip.right), x+int64(maskWidth))
	bottom := min64(int64(clip.bottom), y+int64(maskHeight))
	if right <= left || bottom <= top {
		return
	}
	for dy := top; dy < bottom; dy++ {
		sy := int(dy - y)
		for dx := left; dx < right; dx++ {
			sx := int(dx - x)
			coverage := mask[sy*maskStride+sx*pixelStride+alphaOffset]
			opacity := uint8(int(coverage) * int(config.opacity) / 255)
			if opacity == 0 {
				continue
			}
			source := [4]byte{config.color[0], config.color[1], config.color[2], 255}
			destinationIndex := (int(dy)*int(b.Width) + int(dx)) * 4
			blendPixelForTarget(b.RGBA[destinationIndex:destinationIndex+4], source[:], opacity, config.mode, config.target)
		}
	}
}

func (s *Services) imageDrawText(vm *tjs.VM, args []tjs.Value) (tjs.Value, error) {
	const operation = "drawText"
	if len(args) < 6 {
		return tjs.VoidValue, fmt.Errorf("ImageFunction.%s requires a Bitmap, Font, position, text, and color", operation)
	}
	b, _, err := imageBitmapArg(s, args, 0, operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	fontValue := args[1]
	if fontValue.Kind() != tjs.KindObject || fontValue.IsNull() {
		return tjs.VoidValue, fmt.Errorf("ImageFunction.%s requires a Font", operation)
	}
	s.mu.RLock()
	selected := s.Fonts[fontValue.ObjectRef().Object]
	var state FontState
	if selected != nil {
		state = *selected
	}
	s.mu.RUnlock()
	if selected == nil {
		return tjs.VoidValue, fmt.Errorf("ImageFunction.%s: font argument is not a Font", operation)
	}
	config, err := imageDrawOptions(args, operation, 2, 3, 5, false)
	if err != nil {
		return tjs.VoidValue, err
	}
	config.clip, err = s.imageDrawClip(vm, args, b, 15, operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	s.mu.Lock()
	b.font = state
	s.mu.Unlock()
	var rasterized *image.RGBA
	if config.opacity != 0 {
		if state.Angle != 0 {
			return tjs.VoidValue, imageFunctionUnsupported("rotated text")
		}
		face, faceErr := s.fontFace(&state)
		if faceErr != nil {
			return tjs.VoidValue, faceErr
		}
		rasterized, err = RasterizeText(face, args[4].Text(), color.NRGBA{R: config.color[0], G: config.color[1], B: config.color[2], A: 255})
		if err != nil {
			return tjs.VoidValue, err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if rasterized != nil {
		imageDrawMaskLocked(b, rasterized.Pix, rasterized.Rect.Dx(), rasterized.Rect.Dy(), rasterized.Stride, 4, 3, config.x, config.y, config.clip, config)
		b.markDirty()
	}
	return tjs.VoidValue, nil
}

type imageGlyphData struct {
	width, height int
	originX       int64
	originY       int64
	mask          []byte
}

func imageGlyphDataFromValue(vm *tjs.VM, value tjs.Value) (imageGlyphData, error) {
	var glyph imageGlyphData
	if vm == nil || value.Kind() != tjs.KindObject || value.IsNull() {
		return glyph, imageFunctionUnsupported("invalid drawGlyph glyph data")
	}
	object, ok := vm.Objects.Lookup(value.ObjectRef().Object)
	if !ok || !object.IsValid() {
		return glyph, imageFunctionUnsupported("invalid drawGlyph glyph data")
	}
	var count int64
	if object.Category == tjs.CategoryArray {
		var err error
		count, err = vm.GetArrayLength(value)
		if err != nil {
			return glyph, imageFunctionUnsupported("invalid drawGlyph glyph data")
		}
	} else {
		countValue, exists := object.Get("count")
		if !exists {
			return glyph, imageFunctionUnsupported("invalid drawGlyph glyph data")
		}
		var err error
		count, err = countValue.Int()
		if err != nil {
			return glyph, imageFunctionUnsupported("invalid drawGlyph glyph data")
		}
	}
	if count < 8 {
		return glyph, imageFunctionUnsupported("invalid drawGlyph glyph data")
	}
	field := func(index int) (tjs.Value, bool) {
		if object.Category == tjs.CategoryArray {
			value, exists, err := vm.GetArrayElement(value, int64(index))
			return value, exists && err == nil
		}
		value, exists := object.Get(strconv.Itoa(index))
		return value, exists
	}
	var values [7]int64
	for i := range values {
		value, exists := field(i)
		if !exists {
			return glyph, imageFunctionUnsupported("invalid drawGlyph glyph data")
		}
		number, err := value.Int()
		if err != nil || number < math.MinInt32 || number > math.MaxInt32 {
			return glyph, imageFunctionUnsupported("invalid drawGlyph glyph data")
		}
		values[i] = number
	}
	width, height := values[0], values[1]
	if width < 0 || height < 0 || width > maxTextDimension || height > maxTextDimension ||
		uint64(width)*uint64(height) > maxTextPixels {
		return glyph, imageFunctionUnsupported("drawGlyph bitmap dimensions")
	}
	if count >= 9 {
		colors, exists := field(8)
		if !exists {
			return glyph, imageFunctionUnsupported("invalid drawGlyph glyph data")
		}
		number, err := colors.Int()
		if err != nil || number != 256 {
			return glyph, imageFunctionUnsupported("drawGlyph bitmap format")
		}
	}
	bitmapValue, exists := field(7)
	if !exists || bitmapValue.Kind() != tjs.KindOctet {
		return glyph, imageFunctionUnsupported("invalid drawGlyph bitmap")
	}
	mask := bitmapValue.Octets()
	pixels := uint64(width) * uint64(height)
	if uint64(len(mask)) < pixels {
		return glyph, imageFunctionUnsupported("truncated drawGlyph bitmap")
	}
	glyph.width, glyph.height = int(width), int(height)
	glyph.originX, glyph.originY = values[2], values[3]
	glyph.mask = mask
	return glyph, nil
}

func (s *Services) imageDrawGlyph(vm *tjs.VM, args []tjs.Value) (tjs.Value, error) {
	const operation = "drawGlyph"
	if len(args) < 5 {
		return tjs.VoidValue, fmt.Errorf("ImageFunction.%s requires a Bitmap, position, glyph, and color", operation)
	}
	b, _, err := imageBitmapArg(s, args, 0, operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	config, err := imageDrawOptions(args, operation, 1, 2, 4, true)
	if err != nil {
		return tjs.VoidValue, err
	}
	config.clip, err = s.imageDrawClip(vm, args, b, 14, operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	if config.opacity == 0 {
		return tjs.VoidValue, nil
	}
	glyph, err := imageGlyphDataFromValue(vm, args[3])
	if err != nil {
		return tjs.VoidValue, err
	}
	s.mu.RLock()
	state := b.font
	s.mu.RUnlock()
	face, err := s.fontFace(&state)
	if err != nil {
		return tjs.VoidValue, err
	}
	if state.Angle != 0 {
		return tjs.VoidValue, imageFunctionUnsupported("rotated glyph drawing")
	}
	metrics, err := MetricsForFace(face)
	if err != nil {
		return tjs.VoidValue, err
	}
	offsetY := int64(metrics.Ascent)
	x := config.x + glyph.originX
	y := config.y - glyph.originY + offsetY
	s.mu.Lock()
	defer s.mu.Unlock()
	imageDrawMaskLocked(b, glyph.mask, glyph.width, glyph.height, glyph.width, 1, 0, x, y, config.clip, config)
	b.markDirty()
	return tjs.VoidValue, nil
}

func (s *Services) imageFunctionNative(vm *tjs.VM, operation string, args []tjs.Value) (tjs.Value, error) {
	s.ensureInit()
	switch operation {
	case "@initialize", "@invalidate", "ImageFunction", "finalize":
		return tjs.VoidValue, nil
	case "operateRect":
		return s.imageOperateRect(vm, args)
	case "operateStretch":
		return s.imageOperateStretch(vm, args)
	case "operateAffine":
		return s.imageOperateAffine(vm, args)
	case "flipLR":
		return s.imageFlip(vm, args, true)
	case "flipUD":
		return s.imageFlip(vm, args, false)
	case "adjustGamma":
		return s.imageAdjustGamma(vm, args)
	case "doBoxBlur":
		return s.imageBoxBlur(vm, args)
	case "doGrayScale":
		return s.imageGrayScale(vm, args)
	case "fillRect":
		return s.imageFillRect(vm, args)
	case "colorRect":
		return s.imageColorRect(vm, args)
	case "drawText":
		return s.imageDrawText(vm, args)
	case "drawGlyph":
		return s.imageDrawGlyph(vm, args)
	default:
		return tjs.VoidValue, fmt.Errorf("%w: ImageFunction.%s", ErrUnsupportedNative, operation)
	}
}
