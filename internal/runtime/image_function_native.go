package runtime

import (
	"fmt"
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
func imageRectValue(s *Services, vm *tjs.VM, rect scriptRect, updated bool) (tjs.Value, error) {
	if !updated {
		return tjs.VoidValue, nil
	}
	if vm == nil {
		return tjs.VoidValue, fmt.Errorf("ImageFunction result requires a VM")
	}
	global := tjs.ObjectValue(tjs.NewObjectRef(0, 0))
	class, err := vm.GetMember(global, "Rect", s, nil)
	if err != nil {
		return tjs.VoidValue, err
	}
	return vm.Construct(class, []tjs.Value{
		tjs.IntegerValue(int64(rect.left)),
		tjs.IntegerValue(int64(rect.top)),
		tjs.IntegerValue(int64(rect.right)),
		tjs.IntegerValue(int64(rect.bottom)),
	}, s, nil)
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
	if face != 0 && face != 1 && face != 4 {
		face = 0
	}
	if mode < int64(LayerAlpha) || mode > int64(LayerPSExclusion) {
		return 0, 0, imageFunctionUnsupported(fmt.Sprintf("blend mode %d", mode))
	}
	target := faceToTarget(face)
	if mode == int64(LayerAddAlpha) && face == 0 {
		target = LayerAddAlpha
	}
	return LayerType(mode), target, nil
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
	return imageBlendWithHDA(dst, src, mode, face, opa, false)
}

func imageBlendWithHDA(dst, src []byte, mode, face, opa int64, hda bool) error {
	typ, target, err := imageBlendMode(mode, face)
	if err != nil {
		return err
	}
	if opa < 0 || opa > 255 {
		return fmt.Errorf("ImageFunction opacity %d outside 0..255", opa)
	}
	destinationAlpha := dst[3]
	blendPixelForTarget(dst, src, uint8(opa), typ, target)
	if hda {
		dst[3] = destinationAlpha
	}
	return nil
}
func imageBlendTargetWithHDA(dst, src []byte, opacity int64, mode, target LayerType, hda bool) {
	destinationAlpha := dst[3]
	blendPixelForTarget(dst, src, uint8(opacity), mode, target)
	if hda {
		dst[3] = destinationAlpha
	}
}

func imageSampleRGBA(source []byte, stride int, rect scriptRect, x, y float64, filter int64) [4]byte {
	width, height := int(rect.right-rect.left), int(rect.bottom-rect.top)
	clampX := func(value int) int {
		if value < int(rect.left) {
			return int(rect.left)
		}
		if value >= int(rect.right) {
			return int(rect.right) - 1
		}
		return value
	}
	clampY := func(value int) int {
		if value < int(rect.top) {
			return int(rect.top)
		}
		if value >= int(rect.bottom) {
			return int(rect.bottom) - 1
		}
		return value
	}
	if filter == 0 || width == 1 || height == 1 {
		ix := clampX(int(math.Floor(x + 0.5)))
		iy := clampY(int(math.Floor(y + 0.5)))
		index := (iy*stride + ix) * 4
		return [4]byte{source[index], source[index+1], source[index+2], source[index+3]}
	}
	x0 := int(math.Floor(x))
	y0 := int(math.Floor(y))
	fx, fy := x-float64(x0), y-float64(y0)
	x1, y1 := x0+1, y0+1
	x0, x1 = clampX(x0), clampX(x1)
	y0, y1 = clampY(y0), clampY(y1)
	topLeft := (y0*stride + x0) * 4
	topRight := (y0*stride + x1) * 4
	bottomLeft := (y1*stride + x0) * 4
	bottomRight := (y1*stride + x1) * 4
	return bilinearRGBA(source, int64(topLeft), int64(topRight), int64(bottomLeft), int64(bottomRight), fx, fy, false)
}

type imageAxisWeight struct {
	index  int
	weight float64
}

func imageFilterRadius(filter int64) float64 {
	switch filter & 0xffff {
	case 3, 5:
		return 2
	case 6, 7:
		return 2
	case 8, 9:
		return 3
	case 10, 11:
		return 2
	case 12, 13:
		return 3
	case 16, 17:
		return 2
	case 18, 19:
		return 4
	default:
		return 1
	}
}

func imageFilterWeight(filter int64, distance, typeOpt float64) float64 {
	x := math.Abs(distance)
	switch filter & 0xffff {
	case 1, 2, 4:
		if x < 1 {
			return 1 - x
		}
	case 3, 5:
		coeff := typeOpt
		p0, p1 := coeff+3, coeff+2
		p2, p3, p4 := -coeff*4, coeff*8, coeff*5
		if x <= 1 {
			return 1 - p0*x*x + p1*x*x*x
		}
		if x <= 2 {
			return p2 + p3*x - p4*x*x + coeff*x*x*x
		}
	case 6, 7:
		if x < 1e-12 {
			return 1
		}
		if x < 2 {
			return math.Sin(math.Pi*distance) * math.Sin(math.Pi*distance/2) / (math.Pi * math.Pi * distance * distance / 2)
		}
	case 8, 9:
		if x < 1e-12 {
			return 1
		}
		if x < 3 {
			return math.Sin(math.Pi*distance) * math.Sin(math.Pi*distance/3) / (math.Pi * math.Pi * distance * distance / 3)
		}
	case 10, 11:
		if x <= 1 {
			return x*x*x - x*x*9/5 - x/5 + 1
		}
		if x <= 2 {
			return -x*x*x/3 + x*x*9/5 - x*46/15 + 8/5
		}
	case 12, 13:
		if x <= 1 {
			return x*x*x*13/11 - x*x*453/209 - x*3/209 + 1
		}
		if x <= 2 {
			return -x*x*x*6/11 + x*x*612/209 - x*1038/209 + 540.0/209
		}
		if x <= 3 {
			return x*x*x/11 - x*x*159/209 + x*434/209 - 384.0/209
		}
	case 16, 17:
		if x <= 2 {
			return math.Exp(-2*x*x) * math.Sqrt(2/math.Pi)
		}
	case 18, 19:
		if x < 1e-12 {
			return 1
		}
		if x < 4 {
			return (0.42 + 0.5*math.Cos(math.Pi*x/4) + 0.08*math.Cos(2*math.Pi*x/4)) *
				(math.Sin(math.Pi*x) / (math.Pi * x))
		}
	}
	return 0
}

func imageAxisWeights(center, sourceStart, sourceEnd, sourceLength, destinationLength float64, filter int64, typeOpt float64) []imageAxisWeight {
	radius := imageFilterRadius(filter)
	distanceScale := 1.0
	if sourceLength > destinationLength {
		radius *= sourceLength / destinationLength
		distanceScale = destinationLength / sourceLength
	}
	left := int(math.Floor(center - radius))
	right := int(math.Floor(center + radius))
	weights := make([]imageAxisWeight, 0, max(1, right-left))
	for index := left; index < right; index++ {
		distance := (float64(index) + 0.5 - center) * distanceScale
		weight := imageFilterWeight(filter, distance, typeOpt)
		if weight == 0 {
			continue
		}
		clamped := index
		if clamped < int(sourceStart) {
			clamped = int(sourceStart)
		}
		if clamped >= int(sourceEnd) {
			clamped = int(sourceEnd) - 1
		}
		found := false
		for position := range weights {
			if weights[position].index == clamped {
				weights[position].weight += weight
				found = true
				break
			}
		}
		if !found {
			weights = append(weights, imageAxisWeight{index: clamped, weight: weight})
		}
	}
	sum := 0.0
	for _, item := range weights {
		sum += item.weight
	}
	if sum == 0 {
		return []imageAxisWeight{{index: max(int(sourceStart), min(int(sourceEnd)-1, int(math.Floor(center)))), weight: 1}}
	}
	for position := range weights {
		weights[position].weight /= sum
	}
	return weights
}

func imageAreaAxisWeights(center, sourceStart, sourceEnd, sourceLength, destinationLength float64) []imageAxisWeight {
	if sourceLength <= destinationLength {
		return imageAxisWeights(center, sourceStart, sourceEnd, sourceLength, destinationLength, 2, 0)
	}
	scale := sourceLength / destinationLength
	left, right := center-scale/2, center+scale/2
	start := int(math.Floor(left - 0.5))
	end := int(math.Ceil(right - 0.5))
	weights := make([]imageAxisWeight, 0, max(1, end-start+1))
	for index := start; index <= end; index++ {
		pixelLeft, pixelRight := float64(index), float64(index+1)
		overlap := math.Min(pixelRight, right) - math.Max(pixelLeft, left)
		if overlap <= 0 {
			continue
		}
		clamped := index
		if clamped < int(sourceStart) {
			clamped = int(sourceStart)
		}
		if clamped >= int(sourceEnd) {
			clamped = int(sourceEnd) - 1
		}
		found := false
		for position := range weights {
			if weights[position].index == clamped {
				weights[position].weight += overlap / scale
				found = true
				break
			}
		}
		if !found {
			weights = append(weights, imageAxisWeight{index: clamped, weight: overlap / scale})
		}
	}
	return weights
}

func imageSampleStretchRGBA(source []byte, stride int, rect scriptRect, x, y, destinationWidth, destinationHeight float64, filter int64, typeOpt float64) [4]byte {
	if filter == 0 {
		return imageSampleRGBA(source, stride, rect, x, y, 0)
	}
	centerX, centerY := x+0.5, y+0.5
	sourceWidth, sourceHeight := float64(rect.right-rect.left), float64(rect.bottom-rect.top)
	filterID := filter & 0xffff
	xWeights := imageAxisWeights(centerX, float64(rect.left), float64(rect.right), sourceWidth, destinationWidth, filterID, typeOpt)
	yWeights := imageAxisWeights(centerY, float64(rect.top), float64(rect.bottom), sourceHeight, destinationHeight, filterID, typeOpt)
	if filterID == 14 || filterID == 15 {
		xWeights = imageAreaAxisWeights(centerX, float64(rect.left), float64(rect.right), sourceWidth, destinationWidth)
		yWeights = imageAreaAxisWeights(centerY, float64(rect.top), float64(rect.bottom), sourceHeight, destinationHeight)
	}
	var values [4]float64
	for _, yw := range yWeights {
		for _, xw := range xWeights {
			index := (yw.index*stride + xw.index) * 4
			weight := yw.weight * xw.weight
			for channel := range values {
				values[channel] += float64(source[index+channel]) * weight
			}
		}
	}
	var pixel [4]byte
	for channel, value := range values {
		pixel[channel] = imageClampByte(int64(math.Round(value)))
	}
	return pixel
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
	if _, _, err := imageBlendMode(mode, face); err != nil {
		return tjs.VoidValue, err
	}
	dr, sr, ok := imageRectBlt(dst, src, dx, dy, sr, clip)
	if !ok {
		return tjs.VoidValue, nil
	}
	s.mu.Lock()
	source := imageCopySource(src, dstID == srcID)
	for y := dr.top; y < dr.bottom; y++ {
		for x := dr.left; x < dr.right; x++ {
			sx := sr.left + (x - dr.left)
			sy := sr.top + (y - dr.top)

			si := (int(sy)*int(src.deviceWidth()) + int(sx)) * 4
			dstPixel := append([]byte(nil), dst.blockAt(int64(x), int64(y))...)
			if err := imageBlendWithHDA(dstPixel, source[si:si+4], mode, face, opa, hda); err != nil {
				s.mu.Unlock()
				return tjs.VoidValue, err
			}
			dst.setBlock(int64(x), int64(y), dstPixel)
		}
	}
	s.mu.Unlock()
	return imageRectValue(s, vm, dr, true)
}

func valueAt(args []tjs.Value, index int) tjs.Value {
	if index < 0 || index >= len(args) {
		return tjs.VoidValue
	}
	return args[index]
}

func imageStretchResult(dst *Bitmap, dr, sr, clip scriptRect, filter int64) (scriptRect, bool) {
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
		return scriptRect{}, false
	}
	clip, ok := imageIntersect(clip, imageBounds(dst))
	if !ok {
		return scriptRect{}, false
	}
	result, ok := imageIntersect(dr, clip)
	if filter&0xffff >= 14 && filter&0xffff <= 15 &&
		(dr.right-dr.left > sr.right-sr.left || dr.bottom-dr.top > sr.bottom-sr.top) {
		return scriptRect{}, false
	}
	return result, ok
}

func (s *Services) imageStretch(dst, src *Bitmap, dstID, srcID tjs.ObjectID, dr, sr, clip scriptRect, mode, face, opa, filter int64, typeOpt float64, hda bool) error {
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
	filter &= 0xffff
	if filter < 0 || filter > 19 {
		return imageFunctionUnsupported(fmt.Sprintf("stretch type %d", filter))
	}
	if sr.left < 0 || sr.top < 0 || sr.right > int32(src.Width) || sr.bottom > int32(src.Height) {
		return imageFunctionUnsupported("stretch source rectangle outside source bitmap")
	}
	if (filter == 14 || filter == 15) &&
		(dr.right-dr.left > sr.right-sr.left || dr.bottom-dr.top > sr.bottom-sr.top) {
		return nil
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
	mapDW, mapDH := float64(orig.right-orig.left), float64(orig.bottom-orig.top)
	for y := dr.top; y < dr.bottom; y++ {
		for x := dr.left; x < dr.right; x++ {
			sourceX := float64(sr.left) + (float64(x-orig.left)+0.5)*float64(sr.right-sr.left)/mapDW - 0.5
			sourceY := float64(sr.top) + (float64(y-orig.top)+0.5)*float64(sr.bottom-sr.top)/mapDH - 0.5
			sample := imageSampleStretchRGBA(source, int(src.deviceWidth()), sr, sourceX, sourceY, mapDW, mapDH, filter, typeOpt)
			dstPixel := append([]byte(nil), dst.blockAt(int64(x), int64(y))...)
			if err := imageBlendWithHDA(dstPixel, sample[:], mode, face, opa, hda); err != nil {
				return err
			}
			dst.setBlock(int64(x), int64(y), dstPixel)
		}
	}
	dst.markDirty()
	return nil
}

func imageAffineResult(dst *Bitmap, clip scriptRect, corners [4][2]float64) (scriptRect, bool) {
	minX, maxX := corners[0][0], corners[0][0]
	minY, maxY := corners[0][1], corners[0][1]
	for _, corner := range corners[1:] {
		minX, maxX = math.Min(minX, corner[0]), math.Max(maxX, corner[0])
		minY, maxY = math.Min(minY, corner[1]), math.Max(maxY, corner[1])
	}
	clip, ok := imageIntersect(clip, imageBounds(dst))
	if !ok || maxX <= float64(clip.left) || minX >= float64(clip.right) ||
		maxY <= float64(clip.top) || minY >= float64(clip.bottom) {
		return scriptRect{}, false
	}
	result := clip
	if minX > float64(result.left) {
		result.left = int32(math.Ceil(minX))
	}
	if maxX < float64(result.right) {
		result.right = int32(math.Ceil(maxX))
	}
	if minY > float64(result.top) {
		result.top = int32(math.Ceil(minY))
	}
	if maxY < float64(result.bottom) {
		result.bottom = int32(math.Ceil(maxY))
	}
	return result, result.left < result.right && result.top < result.bottom
}

// imageAffine applies an affine matrix to source pixel centers. The scan is
// limited to the transformed source bounds intersected with the destination
// clip, so its work and any overlap snapshot are bounded by the bitmaps.
func (s *Services) imageAffine(dst, src *Bitmap, dstID, srcID tjs.ObjectID, matrix [6]float64, sr, clip scriptRect, mode, face, opa, filter int64, hda bool) error {
	if sr.left >= sr.right || sr.top >= sr.bottom {
		return nil
	}
	if sr.left < 0 || sr.top < 0 || sr.right > int32(src.Width) || sr.bottom > int32(src.Height) {
		return imageFunctionUnsupported("affine source rectangle outside source bitmap")
	}
	filter &= 0xffff
	if filter < 0 || filter > 19 {
		return imageFunctionUnsupported(fmt.Sprintf("affine type %d", filter))
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

	if _, _, err := imageBlendMode(mode, face); err != nil {
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
			sourceX := float64(sr.left) + sx
			sourceY := float64(sr.top) + sy
			sample := imageSampleRGBA(source, int(src.deviceWidth()), sr, sourceX, sourceY, filter)
			dstPixel := append([]byte(nil), dst.blockAt(int64(x), int64(y))...)
			if err := imageBlendWithHDA(dstPixel, sample[:], mode, face, opa, hda); err != nil {
				return err
			}
			dst.setBlock(int64(x), int64(y), dstPixel)
		}
	}
	dst.markDirty()
	return nil
}

func (s *Services) imagePointAffine(dst, src *Bitmap, dstID, srcID tjs.ObjectID, points [6]float64, sr, clip scriptRect, mode, face, opa, filter int64, hda bool) error {
	if sr.left >= sr.right || sr.top >= sr.bottom {
		return nil
	}
	if sr.left < 0 || sr.top < 0 || sr.right > int32(src.Width) || sr.bottom > int32(src.Height) {
		return imageFunctionUnsupported("point affine source rectangle outside source bitmap")
	}
	filter &= 0xffff
	if filter < 0 || filter > 19 {
		return imageFunctionUnsupported(fmt.Sprintf("point affine type %d", filter))
	}
	sourceWidth := float64(sr.right - sr.left)
	sourceHeight := float64(sr.bottom - sr.top)
	exX := (points[2] - points[0]) / sourceWidth
	exY := (points[3] - points[1]) / sourceWidth
	eyX := (points[4] - points[0]) / sourceHeight
	eyY := (points[5] - points[1]) / sourceHeight
	determinant := exX*eyY - exY*eyX
	if determinant == 0 || math.IsNaN(determinant) || math.IsInf(determinant, 0) {
		return nil
	}
	corners := [4][2]float64{
		{points[0], points[1]},
		{points[2], points[3]},
		{points[2] + points[4] - points[0], points[3] + points[5] - points[1]},
		{points[4], points[5]},
	}
	minX, maxX := corners[0][0], corners[0][0]
	minY, maxY := corners[0][1], corners[0][1]
	for _, corner := range corners[1:] {
		if math.IsNaN(corner[0]) || math.IsInf(corner[0], 0) || math.IsNaN(corner[1]) || math.IsInf(corner[1], 0) {
			return imageFunctionUnsupported("point affine transform outside safe numeric range")
		}
		minX, maxX = math.Min(minX, corner[0]), math.Max(maxX, corner[0])
		minY, maxY = math.Min(minY, corner[1]), math.Max(maxY, corner[1])
	}
	clip, ok := imageIntersect(clip, imageBounds(dst))
	if !ok || maxX <= float64(clip.left) || minX >= float64(clip.right) ||
		maxY <= float64(clip.top) || minY >= float64(clip.bottom) {
		return nil
	}
	left, top := int64(clip.left), int64(clip.top)
	right, bottom := int64(clip.right), int64(clip.bottom)
	if minX > float64(clip.left) {
		left = int64(math.Ceil(minX))
	}
	if minY > float64(clip.top) {
		top = int64(math.Ceil(minY))
	}
	if maxX < float64(clip.right) {
		right = int64(math.Ceil(maxX))
	}
	if maxY < float64(clip.bottom) {
		bottom = int64(math.Ceil(maxY))
	}
	if left >= right || top >= bottom {
		return nil
	}
	if _, _, err := imageBlendMode(mode, face); err != nil {
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
			dx := float64(x) + 0.5 - points[0]
			dy := float64(y) + 0.5 - points[1]
			u := (dx*eyY - dy*eyX) / determinant
			v := (dy*exX - dx*exY) / determinant
			if u < 0 || u >= 1 || v < 0 || v >= 1 {
				continue
			}
			sourceX := float64(sr.left) + u*sourceWidth - 0.5
			sourceY := float64(sr.top) + v*sourceHeight - 0.5
			sample := imageSampleRGBA(source, int(src.deviceWidth()), sr, sourceX, sourceY, filter)
			dstPixel := append([]byte(nil), dst.blockAt(x, y)...)
			if err := imageBlendWithHDA(dstPixel, sample[:], mode, face, opa, hda); err != nil {
				return err
			}
			dst.setBlock(x, y, dstPixel)
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
	typeOpt := 0.0
	if typ == 3 || typ == 5 {
		typeOpt = -1
	}
	typeOpt, err = imageOptionalFloat(args, 10, typeOpt, operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	if _, _, err := imageBlendMode(mode, face); err != nil {
		return tjs.VoidValue, err
	}
	if err := s.imageStretch(dst, src, dstID, srcID, dr, sr, clip, mode, face, opa, typ, typeOpt, hda); err != nil {
		return tjs.VoidValue, err
	}
	result, updated := imageStretchResult(dst, dr, sr, clip, typ)
	return imageRectValue(s, vm, result, updated)
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
	if _, _, err := imageBlendMode(mode, face); err != nil {
		return tjs.VoidValue, err
	}
	if sr.left >= sr.right || sr.top >= sr.bottom {
		return tjs.VoidValue, nil
	}
	if affine {
		if values[0]*values[3]-values[1]*values[2] == 0 {
			return tjs.VoidValue, nil
		}
		if err := s.imageAffine(dst, src, dstID, srcID, values, sr, clip, mode, face, opa, typ, hda); err != nil {
			return tjs.VoidValue, err
		}
		sourceWidth := float64(sr.right - sr.left)
		sourceHeight := float64(sr.bottom - sr.top)
		a, b, c, d, tx, ty := values[0], values[1], values[2], values[3], values[4], values[5]
		sourceCorners := [4][2]float64{
			{-0.5, -0.5},
			{sourceWidth - 0.5, -0.5},
			{sourceWidth - 0.5, sourceHeight - 0.5},
			{-0.5, sourceHeight - 0.5},
		}
		var corners [4][2]float64
		for i, corner := range sourceCorners {
			corners[i] = [2]float64{
				a*corner[0] + c*corner[1] + tx,
				b*corner[0] + d*corner[1] + ty,
			}
		}
		result, updated := imageAffineResult(dst, clip, corners)
		return imageRectValue(s, vm, result, updated)
	}
	sourceWidth := float64(sr.right - sr.left)
	sourceHeight := float64(sr.bottom - sr.top)
	exX := (values[2] - values[0]) / sourceWidth
	exY := (values[3] - values[1]) / sourceWidth
	eyX := (values[4] - values[0]) / sourceHeight
	eyY := (values[5] - values[1]) / sourceHeight
	if exX*eyY-exY*eyX == 0 {
		return tjs.VoidValue, nil
	}
	if err := s.imagePointAffine(dst, src, dstID, srcID, values, sr, clip, mode, face, opa, typ, hda); err != nil {
		return tjs.VoidValue, err
	}
	corners := [4][2]float64{
		{values[0], values[1]},
		{values[2], values[3]},
		{values[2] + values[4] - values[0], values[3] + values[5] - values[1]},
		{values[4], values[5]},
	}
	result, updated := imageAffineResult(dst, clip, corners)
	return imageRectValue(s, vm, result, updated)
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
	if horizontal {
		for y := rect.top; y < rect.bottom; y++ {
			for left, right := rect.left, rect.right-1; left < right; left, right = left+1, right-1 {
				li := (int(y)*int(b.deviceWidth()) + int(left)) * 4
				ri := (int(y)*int(b.deviceWidth()) + int(right)) * 4
				for c := 0; c < 4; c++ {
					b.RGBA[li+c], b.RGBA[ri+c] = b.RGBA[ri+c], b.RGBA[li+c]
				}
			}
		}
	} else {
		for top, bottom := rect.top, rect.bottom-1; top < bottom; top, bottom = top+1, bottom-1 {
			for x := rect.left; x < rect.right; x++ {
				ti := (int(top)*int(b.deviceWidth()) + int(x)) * 4
				bi := (int(bottom)*int(b.deviceWidth()) + int(x)) * 4
				for c := 0; c < 4; c++ {
					b.RGBA[ti+c], b.RGBA[bi+c] = b.RGBA[bi+c], b.RGBA[ti+c]
				}
			}
		}
	}
	b.markDirty()
	s.mu.Unlock()
	return imageRectValue(s, vm, rect, true)
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
	if !ok {
		return tjs.VoidValue, nil
	}
	gammaTable := func(gamma, floor, ceil float64) [256]uint8 {
		var table [256]uint8
		inverse := 1 / gamma
		for value := range table {
			rate := math.Log(float64(value) / 255)
			result := math.Exp(rate*inverse)*(ceil-floor) + floor
			if value == 0 && gamma == 0 {
				result = floor
			}
			if math.IsNaN(result) || result < 0 {
				result = 0
			} else if math.IsInf(result, 0) || result > 255 {
				result = 255
			}
			table[value] = uint8(math.Round(result))
		}
		return table
	}
	tables := [3][256]uint8{gammaTable(rg, rf, rc), gammaTable(gg, gf, gc), gammaTable(bg, bf, bc)}
	s.mu.Lock()
	for y := clip.top; y < clip.bottom; y++ {
		for x := clip.left; x < clip.right; x++ {
			i := (int(y)*int(b.deviceWidth()) + int(x)) * 4
			alpha := b.RGBA[i+3]
			if alpha == 0 {
				continue
			}
			if addAlpha && alpha < 255 {
				adjusted := int(alpha) + int(alpha>>7)
				reciprocal := 65536 / int(alpha)
				if reciprocal > 32767 {
					reciprocal = 32767
				}
				for channel := 0; channel < 3; channel++ {
					value := int(b.RGBA[i+channel])
					if value > int(alpha) {
						value = int(tables[channel][255])*adjusted>>8 + value - int(alpha)
					} else {
						index := reciprocal * value >> 8
						if index > 255 {
							index = 255
						}
						value = int(tables[channel][index]) * adjusted >> 8
					}
					b.RGBA[i+channel] = imageClampByte(int64(value))
				}
				continue
			}
			b.RGBA[i], b.RGBA[i+1], b.RGBA[i+2] = tables[0][b.RGBA[i]], tables[1][b.RGBA[i+1]], tables[2][b.RGBA[i+2]]
		}
	}
	b.markDirty()
	s.mu.Unlock()
	return imageRectValue(s, vm, clip, true)
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
	for y := clip.top; y < clip.bottom; y++ {
		for x := clip.left; x < clip.right; x++ {
			i := (int(y)*int(b.deviceWidth()) + int(x)) * 4
			gray := uint8((77*int(b.RGBA[i]) + 150*int(b.RGBA[i+1]) + 29*int(b.RGBA[i+2]) + 128) >> 8)
			b.RGBA[i], b.RGBA[i+1], b.RGBA[i+2] = gray, gray, gray
		}
	}
	b.markDirty()
	s.mu.Unlock()
	return imageRectValue(s, vm, clip, true)
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
	for y := rect.top; y < rect.bottom; y++ {
		for x := rect.left; x < rect.right; x++ {
			pixel := [4]byte{r, g, bl, 0}
			if isAlpha {
				pixel[3] = a
			}
			b.setBlock(int64(x), int64(y), pixel[:])
		}
	}
	b.markDirty()
	s.mu.Unlock()
	return imageRectValue(s, vm, rect, true)
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
	face, err := imageOptionalInt(args, 4, 0, operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	if opa < -255 {
		opa = -255
	} else if opa > 255 {
		opa = 255
	}
	if face != 0 && face != 1 && face != 2 && face != 4 {
		face = 0
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
	for y := rect.top; y < rect.bottom; y++ {
		for x := rect.left; x < rect.right; x++ {
			dstPixel := append([]byte(nil), b.blockAt(int64(x), int64(y))...)
			switch face {
			case 1:
				if opa <= 0 {
					continue
				}
				for c := 0; c < 3; c++ {
					dstPixel[c] = byte((int(dstPixel[c])*(255-int(opa)) + int(src[c])*int(opa) + 127) / 255)
				}
			case 2:
				dstPixel[3] = uint8(value)
			case 0:
				if opa < 0 {
					level := -opa
					dstPixel[3] = byte(int(dstPixel[3]) * (255 - int(level)) / 255)
					break
				}
				src[3] = uint8(int(a) * int(opa) / 255)
				if err := imageBlend(dstPixel, src[:], int64(LayerAlpha), face, 255); err != nil {
					s.mu.Unlock()
					return tjs.VoidValue, err
				}
			case 4:
				if opa < 0 {
					continue
				}
				src[3] = uint8(int(a) * int(opa) / 255)
				if err := imageBlend(dstPixel, src[:], int64(LayerAddAlpha), face, 255); err != nil {
					s.mu.Unlock()
					return tjs.VoidValue, err
				}
			}
			b.setBlock(int64(x), int64(y), dstPixel)
		}
	}
	b.markDirty()
	s.mu.Unlock()
	return imageRectValue(s, vm, rect, true)
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
			if maxX >= int64(b.deviceWidth()) {
				maxX = int64(b.deviceWidth()) - 1
			}
			if maxY >= int64(b.deviceHeight()) {
				maxY = int64(b.deviceHeight()) - 1
			}
			count := int64(0)
			sums := [4]int64{}
			for sy := minY; sy <= maxY; sy++ {
				for sx := minX; sx <= maxX; sx++ {
					i := (sy*int64(b.deviceWidth()) + sx) * 4
					for c := 0; c < 4; c++ {
						sums[c] += int64(source[i+int64(c)])
					}
					count++
				}
			}
			i := (int64(y)*int64(b.deviceWidth()) + int64(x)) * 4
			for c := 0; c < 4; c++ {
				if isAlpha && c != 3 {
					continue
				}
				b.RGBA[i+int64(c)] = uint8((sums[c] + count/2) / count)
			}
		}
	}
	b.markDirty()
	s.mu.Unlock()
	return imageRectValue(s, vm, clip, true)
}

type imageDrawConfig struct {
	x, y                         int64
	color                        [3]uint8
	opacity                      int64
	mode                         LayerType
	target                       LayerType
	antialias                    bool
	hda                          bool
	shadowLevel                  int64
	shadowColor                  [3]uint8
	shadowWidth                  int64
	shadowOffsetX, shadowOffsetY int64
	clip                         scriptRect
}

func imageDrawOptions(args []tjs.Value, operation string, xIndex, yIndex, colorIndex int, _ bool) (imageDrawConfig, error) {
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
	config.antialias, err = imageOptionalBool(args, colorIndex+2, true, operation)
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
	config.shadowLevel, err = imageOptionalInt(args, colorIndex+4, 0, operation)
	if err != nil {
		return config, err
	}
	if config.shadowLevel < 0 {
		config.shadowLevel = 0
	} else if config.shadowLevel > 255 {
		config.shadowLevel = 255
	}
	shadowColor, err := imageOptionalInt(args, colorIndex+5, 0, operation)
	if err != nil {
		return config, err
	}
	sr, sg, sb, _ := imageColor(shadowColor)
	config.shadowColor = [3]uint8{sr, sg, sb}
	config.shadowWidth, err = imageOptionalInt(args, colorIndex+6, 0, operation)
	if err != nil {
		return config, err
	}
	if config.shadowWidth < 0 {
		config.shadowWidth = 0
	}
	config.shadowOffsetX, err = imageOptionalInt(args, colorIndex+7, 0, operation)
	if err != nil {
		return config, err
	}
	config.shadowOffsetY, err = imageOptionalInt(args, colorIndex+8, 0, operation)
	if err != nil {
		return config, err
	}
	config.hda, err = imageOptionalBool(args, colorIndex+9, false, operation)
	if err != nil {
		return config, err
	}
	if face != 0 && opacity < 0 {
		opacity = 0
	}
	if opacity < -255 {
		opacity = -255
	} else if opacity > 255 {
		opacity = 255
	}
	config.opacity = opacity
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

func imageMaskValue(mask []byte, width, height, stride, pixelStride, alphaOffset, x, y int) uint8 {
	if x < 0 || y < 0 || x >= width || y >= height {
		return 0
	}
	return mask[y*stride+x*pixelStride+alphaOffset]
}

func imageShadowMask(mask []byte, width, height, stride, pixelStride, alphaOffset int, radius, level int64) []byte {
	if width <= 0 || height <= 0 {
		return nil
	}
	output := make([]byte, width*height)
	if radius <= 0 {
		for y := 0; y < height; y++ {
			for x := 0; x < width; x++ {
				value := int64(imageMaskValue(mask, width, height, stride, pixelStride, alphaOffset, x, y)) * level / 255
				output[y*width+x] = imageClampByte(value)
			}
		}
		return output
	}
	if radius > int64(max(width, height)) {
		radius = int64(max(width, height))
	}
	integral := make([]int64, (width+1)*(height+1))
	for y := 0; y < height; y++ {
		row := int64(0)
		for x := 0; x < width; x++ {
			row += int64(imageMaskValue(mask, width, height, stride, pixelStride, alphaOffset, x, y))
			integral[(y+1)*(width+1)+x+1] = integral[y*(width+1)+x+1] + row
		}
	}
	for y := 0; y < height; y++ {
		top := max(0, y-int(radius))
		bottom := min(height-1, y+int(radius))
		for x := 0; x < width; x++ {
			left := max(0, x-int(radius))
			right := min(width-1, x+int(radius))
			sum := integral[(bottom+1)*(width+1)+right+1] -
				integral[top*(width+1)+right+1] -
				integral[(bottom+1)*(width+1)+left] +
				integral[top*(width+1)+left]
			count := int64((right - left + 1) * (bottom - top + 1))
			output[y*width+x] = imageClampByte(sum * level / (count * 255))
		}
	}
	return output
}

func imageDrawMaskLocked(b *Bitmap, mask []byte, maskWidth, maskHeight, maskStride, pixelStride, alphaOffset int, maskScale int32, x, y int64, clip scriptRect, config imageDrawConfig) {
	if maskScale < 1 {
		maskScale = 1
	}
	clip, ok := imageIntersect(clip, imageBounds(b))
	if !ok || config.opacity == 0 || maskWidth <= 0 || maskHeight <= 0 {
		return
	}
	originX, originY := x, y
	left := max64(int64(clip.left), originX)
	top := max64(int64(clip.top), originY)
	right := min64(int64(clip.right), originX+int64(maskWidth)*int64(maskScale))
	bottom := min64(int64(clip.bottom), originY+int64(maskHeight)*int64(maskScale))
	if right <= left || bottom <= top {
		return
	}
	for dy := top; dy < bottom; dy++ {
		sy := int((dy - originY) / int64(maskScale))
		for dx := left; dx < right; dx++ {
			sx := int((dx - originX) / int64(maskScale))
			coverage := imageMaskValue(mask, maskWidth, maskHeight, maskStride, pixelStride, alphaOffset, sx, sy)
			if !config.antialias && coverage < 128 {
				coverage = 0
			} else if !config.antialias {
				coverage = 255
			}
			if coverage == 0 {
				continue
			}
			destinationIndex := (int(dy)*int(b.deviceWidth()) + int(dx)) * 4
			destination := b.RGBA[destinationIndex : destinationIndex+4]
			if config.opacity < 0 {
				destination[3] = uint8(int(destination[3]) * (255 - int(coverage)) / 255)
				continue
			}
			opacity := uint8(int(coverage) * int(config.opacity) / 255)
			if opacity == 0 {
				continue
			}
			source := [4]byte{config.color[0], config.color[1], config.color[2], 255}
			imageBlendTargetWithHDA(destination, source[:], int64(opacity), config.mode, config.target, config.hda)
		}
	}
}

func imageAlphaMaskFromRGBA(rgba []byte, width, height, stride int) []byte {
	mask := make([]byte, width*height)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			mask[y*width+x] = rgba[y*stride+x*4+3]
		}
	}
	return mask
}

func imageRotateMask(mask []byte, width, height int, angle float64) ([]byte, int, int, int64, int64) {
	if angle == 0 {
		return mask, width, height, 0, 0
	}
	cosine, sine := math.Cos(angle), math.Sin(angle)
	corners := [4][2]float64{{0, 0}, {float64(width), 0}, {float64(width), float64(height)}, {0, float64(height)}}
	minX, maxX := math.Inf(1), math.Inf(-1)
	minY, maxY := math.Inf(1), math.Inf(-1)
	for _, corner := range corners {
		x := cosine*corner[0] - sine*corner[1]
		y := sine*corner[0] + cosine*corner[1]
		minX, maxX = math.Min(minX, x), math.Max(maxX, x)
		minY, maxY = math.Min(minY, y), math.Max(maxY, y)
	}
	left, top := math.Floor(minX), math.Floor(minY)
	rotatedWidth := int(math.Ceil(maxX) - left)
	rotatedHeight := int(math.Ceil(maxY) - top)
	output := make([]byte, rotatedWidth*rotatedHeight)
	for y := 0; y < rotatedHeight; y++ {
		for x := 0; x < rotatedWidth; x++ {
			worldX := left + float64(x) + 0.5
			worldY := top + float64(y) + 0.5
			sourceX := cosine*worldX + sine*worldY
			sourceY := -sine*worldX + cosine*worldY
			if sourceX < 0 || sourceY < 0 || sourceX >= float64(width) || sourceY >= float64(height) {
				continue
			}
			output[y*rotatedWidth+x] = mask[int(sourceY)*width+int(sourceX)]
		}
	}
	return output, rotatedWidth, rotatedHeight, int64(left), int64(top)
}

func imageDrawResultRect(b *Bitmap, clip scriptRect, x, y int64, width, height int, config imageDrawConfig) (scriptRect, bool) {
	if config.opacity == 0 || width <= 0 || height <= 0 {
		return scriptRect{}, false
	}
	clip, ok := imageIntersect(clip, imageBounds(b))
	if !ok {
		return scriptRect{}, false
	}
	result := scriptRect{}
	hasResult := false
	include := func(left, top, right, bottom int64) {
		if right <= left || bottom <= top {
			return
		}
		if left < int64(clip.left) {
			left = int64(clip.left)
		}
		if top < int64(clip.top) {
			top = int64(clip.top)
		}
		if right > int64(clip.right) {
			right = int64(clip.right)
		}
		if bottom > int64(clip.bottom) {
			bottom = int64(clip.bottom)
		}
		if right <= left || bottom <= top {
			return
		}
		rect := imageRect(int32(left), int32(top), int32(right), int32(bottom))
		if !hasResult {
			result, hasResult = rect, true
			return
		}
		if rect.left < result.left {
			result.left = rect.left
		}
		if rect.top < result.top {
			result.top = rect.top
		}
		if rect.right > result.right {
			result.right = rect.right
		}
		if rect.bottom > result.bottom {
			result.bottom = rect.bottom
		}
	}
	include(x, y, x+int64(width), y+int64(height))
	if config.shadowLevel > 0 {
		include(x+config.shadowOffsetX, y+config.shadowOffsetY,
			x+config.shadowOffsetX+int64(width), y+config.shadowOffsetY+int64(height))
	}
	return result, hasResult
}

func imageDrawMaskWithShadowLocked(b *Bitmap, mask []byte, width, height int, x, y int64, clip scriptRect, config imageDrawConfig) {
	if config.shadowLevel > 0 {
		shadow := imageShadowMask(mask, width, height, width, 1, 0, config.shadowWidth, 255)
		shadowConfig := config
		shadowConfig.color = config.shadowColor
		shadowConfig.opacity = config.opacity * config.shadowLevel / 255
		imageDrawMaskLocked(b, shadow, width, height, width, 1, 0, 1,
			x+config.shadowOffsetX, y+config.shadowOffsetY, clip, shadowConfig)
	}
	imageDrawMaskLocked(b, mask, width, height, width, 1, 0, 1, x, y, clip, config)
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
	var mask []byte
	maskWidth, maskHeight := 0, 0
	originX, originY := int64(0), int64(0)
	if config.opacity != 0 {
		face, faceErr := s.fontFace(&state)
		if faceErr != nil {
			return tjs.VoidValue, faceErr
		}
		rasterized, rasterErr := RasterizeText(face, args[4].Text(), color.White)
		if rasterErr != nil {
			return tjs.VoidValue, rasterErr
		}
		mask = imageAlphaMaskFromRGBA(rasterized.Pix, rasterized.Rect.Dx(), rasterized.Rect.Dy(), rasterized.Stride)
		mask, maskWidth, maskHeight, originX, originY = imageRotateMask(mask, rasterized.Rect.Dx(), rasterized.Rect.Dy(), float64(state.Angle)*math.Pi/1800)
	}
	s.mu.Lock()
	if mask != nil && maskWidth > 0 && maskHeight > 0 {
		imageDrawMaskWithShadowLocked(b, mask, maskWidth, maskHeight,
			config.x+originX, config.y+originY, config.clip, config)
		b.markDirty()
	}
	result, updated := imageDrawResultRect(b, config.clip, config.x+originX, config.y+originY, maskWidth, maskHeight, config)
	s.mu.Unlock()
	return imageRectValue(s, vm, result, updated)
}

func imageRotateRGBA(pixels []byte, width, height int, angle float64) ([]byte, int, int, int64, int64) {
	if angle == 0 {
		return pixels, width, height, 0, 0
	}
	cosine, sine := math.Cos(angle), math.Sin(angle)
	corners := [4][2]float64{{0, 0}, {float64(width), 0}, {float64(width), float64(height)}, {0, float64(height)}}
	minX, maxX := math.Inf(1), math.Inf(-1)
	minY, maxY := math.Inf(1), math.Inf(-1)
	for _, corner := range corners {
		x := cosine*corner[0] - sine*corner[1]
		y := sine*corner[0] + cosine*corner[1]
		minX, maxX = math.Min(minX, x), math.Max(maxX, x)
		minY, maxY = math.Min(minY, y), math.Max(maxY, y)
	}
	left, top := math.Floor(minX), math.Floor(minY)
	rotatedWidth := int(math.Ceil(maxX) - left)
	rotatedHeight := int(math.Ceil(maxY) - top)
	output := make([]byte, rotatedWidth*rotatedHeight*4)
	for y := 0; y < rotatedHeight; y++ {
		for x := 0; x < rotatedWidth; x++ {
			worldX := left + float64(x) + 0.5
			worldY := top + float64(y) + 0.5
			sourceX := cosine*worldX + sine*worldY
			sourceY := -sine*worldX + cosine*worldY
			if sourceX < 0 || sourceY < 0 || sourceX >= float64(width) || sourceY >= float64(height) {
				continue
			}
			sourceIndex := (int(sourceY)*width + int(sourceX)) * 4
			destinationIndex := (y*rotatedWidth + x) * 4
			copy(output[destinationIndex:destinationIndex+4], pixels[sourceIndex:sourceIndex+4])
		}
	}
	return output, rotatedWidth, rotatedHeight, int64(left), int64(top)
}

func imageDrawColorMaskLocked(b *Bitmap, pixels []byte, width, height, x, y int64, clip scriptRect, config imageDrawConfig) {
	clip, ok := imageIntersect(clip, imageBounds(b))
	if !ok || config.opacity == 0 || width <= 0 || height <= 0 {
		return
	}
	left := max64(int64(clip.left), x)
	top := max64(int64(clip.top), y)
	right := min64(int64(clip.right), x+width)
	bottom := min64(int64(clip.bottom), y+height)
	for dy := top; dy < bottom; dy++ {
		for dx := left; dx < right; dx++ {
			sourceIndex := ((dy-y)*width + (dx - x)) * 4
			source := pixels[sourceIndex : sourceIndex+4]
			if !config.antialias && source[3] < 128 {
				continue
			}
			if config.opacity < 0 {
				destinationIndex := (int(dy)*int(b.deviceWidth()) + int(dx)) * 4
				b.RGBA[destinationIndex+3] = uint8(int(b.RGBA[destinationIndex+3]) * (255 - int(source[3])) / 255)
				continue
			}
			opacity := int64(source[3]) * config.opacity / 255
			if !config.antialias {
				opacity = config.opacity
				sourceCopy := [4]byte{source[0], source[1], source[2], 255}
				source = sourceCopy[:]
			}
			destinationIndex := (int(dy)*int(b.deviceWidth()) + int(dx)) * 4
			imageBlendTargetWithHDA(b.RGBA[destinationIndex:destinationIndex+4], source, int64(opacity), config.mode, config.target, config.hda)
		}
	}
}

type imageGlyphData struct {
	width, height int
	originX       int64
	originY       int64
	mask          []byte
	colors        int64
	colored       bool
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
	colors := int64(256)
	if count >= 9 {
		colorValue, exists := field(8)
		if !exists {
			return glyph, imageFunctionUnsupported("invalid drawGlyph glyph data")
		}
		var colorErr error
		colors, colorErr = colorValue.Int()
		if colorErr != nil || colors <= 0 {
			return glyph, imageFunctionUnsupported("drawGlyph bitmap format")
		}
	}
	bitmapValue, exists := field(7)
	if !exists || bitmapValue.Kind() != tjs.KindOctet {
		return glyph, imageFunctionUnsupported("invalid drawGlyph bitmap")
	}
	mask := bitmapValue.Octets()
	pixels := uint64(width) * uint64(height)
	colored := colors > 256
	bytesPerPixel := uint64(1)
	if colored {
		bytesPerPixel = 4
	}
	if uint64(len(mask)) < pixels*bytesPerPixel {
		return glyph, imageFunctionUnsupported("truncated drawGlyph bitmap")
	}
	mask = mask[:pixels*bytesPerPixel]
	if !colored && colors != 256 {
		normalized := make([]byte, int(pixels))
		for i, value := range mask {
			if colors == 1 {
				if value != 0 {
					normalized[i] = 255
				}
			} else {
				normalized[i] = uint8((int64(value) * 255) / (colors - 1))
			}
		}
		mask = normalized
	}
	glyph.width, glyph.height = int(width), int(height)
	glyph.originX, glyph.originY = values[2], values[3]
	glyph.mask, glyph.colors, glyph.colored = mask, colors, colored
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
	metrics, err := MetricsForFace(face)
	if err != nil {
		return tjs.VoidValue, err
	}
	offsetY := int64(metrics.Ascent)
	x := config.x + glyph.originX
	y := config.y - glyph.originY + offsetY
	s.mu.Lock()
	angle := float64(state.Angle) * math.Pi / 1800
	drawWidth, drawHeight := glyph.width, glyph.height
	if glyph.colored {
		pixels, width, height, originX, originY := imageRotateRGBA(glyph.mask, glyph.width, glyph.height, angle)
		x += originX
		y += originY
		drawWidth, drawHeight = width, height
		if config.shadowLevel > 0 {
			shadowMask := imageAlphaMaskFromRGBA(pixels, width, height, width*4)
			imageDrawMaskWithShadowLocked(b, shadowMask, width, height, x, y, config.clip, config)
		}
		imageDrawColorMaskLocked(b, pixels, int64(width), int64(height), x, y, config.clip, config)
	} else {
		mask, width, height, originX, originY := imageRotateMask(glyph.mask, glyph.width, glyph.height, angle)
		x += originX
		y += originY
		drawWidth, drawHeight = width, height
		imageDrawMaskWithShadowLocked(b, mask, width, height, x, y, config.clip, config)
	}
	b.markDirty()
	result, updated := imageDrawResultRect(b, config.clip, x, y, drawWidth, drawHeight, config)
	s.mu.Unlock()
	return imageRectValue(s, vm, result, updated)
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
