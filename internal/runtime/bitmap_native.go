package runtime

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"math"
	"strings"

	"github.com/eslzzyl/krkrgo/internal/assets"
	"github.com/eslzzyl/krkrgo/internal/tjs"
)

const (
	bitmapDefaultDimension = uint32(32)
	bitmapMaxPixels        = uint64(64 * 1024 * 1024)
	bitmapMaxDimension     = uint32(16384)
)

// Bitmap is the script-facing, tightly packed 32bpp image. Pixels are stored
// as non-premultiplied RGBA bytes while the script color convention remains
// 0xAARRGGBB (getPixel/setPixel expose RGB and getMaskPixel/setMaskPixel
// expose alpha, matching Kirikiri's Bitmap API).
type Bitmap struct {
	Width, Height uint32
	RGBA          []byte
	font          FontState
	loading       bool
}

type bitmapAsyncResult struct {
	generation uint64
	data       []byte
	image      assets.Image
	err        error
}

func newBitmap(width, height uint32) (*Bitmap, error) {
	if width == 0 || height == 0 || width > bitmapMaxDimension || height > bitmapMaxDimension || uint64(width)*uint64(height) > bitmapMaxPixels {
		return nil, fmt.Errorf("Bitmap: invalid dimensions %dx%d", width, height)
	}
	return &Bitmap{Width: width, Height: height, RGBA: make([]byte, uint64(width)*uint64(height)*4), font: *newFontState()}, nil
}

func (b *Bitmap) pixel(x, y int64) ([]byte, error) {
	if b == nil || len(b.RGBA) == 0 {
		return nil, fmt.Errorf("Bitmap: object has no image")
	}
	if x < 0 || y < 0 || x >= int64(b.Width) || y >= int64(b.Height) {
		return nil, fmt.Errorf("Bitmap: pixel outside image")
	}
	i := (uint64(y)*uint64(b.Width) + uint64(x)) * 4
	return b.RGBA[i : i+4], nil
}

func (b *Bitmap) resize(width, height uint32, keep bool) error {
	if width == b.Width && height == b.Height {
		return nil
	}
	next, err := newBitmap(width, height)
	if err != nil {
		return err
	}
	if keep && b != nil && len(b.RGBA) != 0 {
		copyWidth, copyHeight := b.Width, b.Height
		if width < copyWidth {
			copyWidth = width
		}
		if height < copyHeight {
			copyHeight = height
		}
		for y := uint32(0); y < copyHeight; y++ {
			src := uint64(y) * uint64(b.Width) * 4
			dst := uint64(y) * uint64(width) * 4
			copy(next.RGBA[dst:dst+uint64(copyWidth)*4], b.RGBA[src:src+uint64(copyWidth)*4])
		}
	}
	font := b.font
	*b = *next
	b.font = font
	return nil
}

func bitmapContext(context tjs.Value, operation string) (tjs.ObjectID, error) {
	if context.Kind() != tjs.KindObject || context.IsNull() || context.ObjectRef().Object == 0 {
		return 0, fmt.Errorf("Bitmap.%s requires non-null object context", operation)
	}
	return context.ObjectRef().Object, nil
}

func (s *Services) bitmapForContext(context tjs.Value, operation string) (*Bitmap, tjs.ObjectID, error) {
	id, err := bitmapContext(context, operation)
	if err != nil {
		return nil, 0, err
	}
	s.ensureInit()
	s.mu.RLock()
	b := s.Bitmaps[id]
	s.mu.RUnlock()
	if b == nil {
		return nil, id, fmt.Errorf("Bitmap.%s: context has no Bitmap native instance", operation)
	}
	return b, id, nil
}

func (s *Services) bitmapFromValue(value tjs.Value) (*Bitmap, tjs.ObjectID, error) {
	if value.Kind() != tjs.KindObject || value.IsNull() || value.ObjectRef().Object == 0 {
		return nil, 0, fmt.Errorf("Bitmap.copyFrom requires a non-null Bitmap object")
	}
	id := value.ObjectRef().Object
	s.ensureInit()
	s.mu.RLock()
	b := s.Bitmaps[id]
	s.mu.RUnlock()
	if b == nil {
		return nil, id, fmt.Errorf("object %d is not a Bitmap", id)
	}
	return b, id, nil
}

func (s *Services) Bitmap(id tjs.ObjectID) *Bitmap {
	if s == nil {
		return nil
	}
	s.ensureInit()
	s.mu.RLock()
	b := s.Bitmaps[id]
	s.mu.RUnlock()
	return b
}

func bitmapArg(args []tjs.Value, index int, operation string) (tjs.Value, error) {
	if index < 0 || index >= len(args) {
		return tjs.VoidValue, fmt.Errorf("Bitmap.%s: missing argument %d", operation, index)
	}
	return args[index], nil
}

func bitmapIntArg(args []tjs.Value, index int, operation string) (int64, error) {
	v, err := bitmapArg(args, index, operation)
	if err != nil {
		return 0, err
	}
	return v.Int()
}

func bitmapBytes(s *Services, name string) ([]byte, error) {
	if s == nil {
		return nil, fmt.Errorf("%w: bitmap storage", ErrUnsupportedDevice)
	}
	return s.readStorage(name)
}
func (s *Services) bitmapLoadAsync(id tjs.ObjectID, b *Bitmap, name string) error {
	s.mu.Lock()
	if b.loading {
		s.mu.Unlock()
		return fmt.Errorf("Bitmap.loadAsync: bitmap is already loading")
	}
	generation := s.bitmapLoadGeneration[id] + 1
	if generation == 0 {
		s.mu.Unlock()
		return fmt.Errorf("Bitmap.loadAsync: request generation overflow")
	}
	s.bitmapLoadGeneration[id] = generation
	delete(s.bitmapLoadResults, id)
	b.loading = true
	s.mu.Unlock()

	go func() {
		data, err := bitmapBytes(s, name)
		var image assets.Image
		if err == nil {
			image, err = assets.DecodeImage(data)
		}
		if err == nil && (image.Width > bitmapMaxDimension || image.Height > bitmapMaxDimension) {
			err = fmt.Errorf("Bitmap: image dimensions exceed limits")
		}

		s.mu.Lock()
		if s.Bitmaps[id] != b || s.bitmapLoadGeneration[id] != generation {
			s.mu.Unlock()
			return
		}
		s.bitmapLoadResults[id] = bitmapAsyncResult{
			generation: generation,
			data:       data,
			image:      image,
			err:        err,
		}
		if postErr := s.Events.Post(uint64(id), EventBitmapLoad, PriorityNormal); postErr != nil {
			delete(s.bitmapLoadResults, id)
			b.loading = false
		}
		s.mu.Unlock()
	}()
	return nil
}

func (s *Services) takeBitmapAsyncResult(id tjs.ObjectID) (bitmapAsyncResult, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result, ok := s.bitmapLoadResults[id]
	if !ok {
		return bitmapAsyncResult{}, false
	}
	delete(s.bitmapLoadResults, id)
	if b := s.Bitmaps[id]; b != nil && s.bitmapLoadGeneration[id] == result.generation {
		b.loading = false
		if result.err == nil {
			b.Width, b.Height = result.image.Width, result.image.Height
			b.RGBA = append(b.RGBA[:0], result.image.RGBA...)
		}
	}
	return result, true
}

func bitmapMetadata(vm *tjs.VM, data []byte, width, height uint32) (tjs.Value, error) {
	out, err := vm.NewDictionary()
	if err != nil {
		return tjs.VoidValue, err
	}
	o, ok := vm.Objects.Lookup(out.ObjectRef().Object)
	if !ok {
		return tjs.VoidValue, fmt.Errorf("Bitmap metadata allocation failed")
	}
	values := map[string]tjs.Value{
		"width": tjs.IntegerValue(int64(width)), "height": tjs.IntegerValue(int64(height)), "bpp": tjs.IntegerValue(32),
	}
	if tags, metadataErr := assets.ImageMetadata(data); metadataErr == nil {
		for key, value := range tags {
			values[key] = tjs.StringValue(value)
		}
	}
	for key, value := range values {
		if err := o.Set(key, value); err != nil {
			return tjs.VoidValue, err
		}
	}
	return out, nil
}

func (s *Services) bitmapLoad(vm *tjs.VM, b *Bitmap, name string) (tjs.Value, error) {
	data, err := bitmapBytes(s, name)
	if err != nil {
		return tjs.VoidValue, err
	}
	im, err := assets.DecodeImage(data)
	if err != nil {
		return tjs.VoidValue, err
	}
	if im.Width > bitmapMaxDimension || im.Height > bitmapMaxDimension {
		return tjs.VoidValue, fmt.Errorf("Bitmap: image dimensions exceed limits")
	}
	b.Width, b.Height = im.Width, im.Height
	b.RGBA = append(b.RGBA[:0], im.RGBA...)
	b.loading = false
	return bitmapMetadata(vm, data, im.Width, im.Height)
}

func bitmapBMPType(typeName string) (bool, int) {
	switch typeName {
	case "bmp24":
		return true, 24
	case "bmp8":
		return true, 8
	case "bmp32":
		return true, 32
	}
	if strings.HasPrefix(typeName, "bmp") || typeName == ".bmp" || typeName == ".dib" {
		return true, 32
	}
	return false, 0
}

func bitmapJPEGType(typeName string) bool {
	return strings.HasPrefix(typeName, "jpg") || typeName == ".jpg" || typeName == ".jpeg" || typeName == ".jif"
}

func bitmapJPEGQuality(typeName string) int {
	quality := 90
	if strings.HasPrefix(typeName, "jpg") && len(typeName) > 3 {
		quality = 0
		for i := 3; i < len(typeName); i++ {
			digit := typeName[i]
			if digit < '0' || digit > '9' {
				continue
			}
			if quality > 100 {
				continue
			}
			quality = quality*10 + int(digit-'0')
		}
		if quality <= 0 {
			quality = 10
		}
		if quality > 100 {
			quality = 100
		}
	}
	return quality
}

func bitmapSaveOptionBPP(vm *tjs.VM, metadata tjs.Value, bpp int) int {
	if metadata.Kind() != tjs.KindObject || metadata.IsNull() {
		return bpp
	}
	object, ok := vm.Objects.Lookup(metadata.ObjectRef().Object)
	if !ok {
		return bpp
	}
	value, ok := object.Get("bpp")
	if !ok {
		return bpp
	}
	index, err := value.Int()
	if err != nil {
		return bpp
	}
	switch index {
	case 0:
		return 32
	case 1:
		return 24
	case 2:
		return 8
	default:
		return bpp
	}
}

func bitmapSaveOptionQuality(vm *tjs.VM, metadata tjs.Value, quality int) int {
	if metadata.Kind() != tjs.KindObject || metadata.IsNull() {
		return quality
	}
	object, ok := vm.Objects.Lookup(metadata.ObjectRef().Object)
	if !ok {
		return quality
	}
	value, ok := object.Get("quality")
	if !ok {
		return quality
	}
	selected, err := value.Int()
	if err != nil {
		return quality
	}
	if selected < 1 {
		return 1
	}
	if selected > 100 {
		return 100
	}
	return int(selected)
}

func bitmapUnsupportedJPEGOption(vm *tjs.VM, metadata tjs.Value) string {
	if metadata.Kind() != tjs.KindObject || metadata.IsNull() {
		return ""
	}
	object, ok := vm.Objects.Lookup(metadata.ObjectRef().Object)
	if !ok {
		return ""
	}
	for _, key := range [...]string{"subsampling", "dct", "progressive"} {
		if _, exists := object.Get(key); exists {
			return key
		}
	}
	return ""
}

func bitmapSaveOption(vm *tjs.VM) (tjs.Value, error) {
	options, err := vm.NewDictionary()
	if err != nil {
		return tjs.VoidValue, err
	}
	optionObject, ok := vm.Objects.Lookup(options.ObjectRef().Object)
	if !ok {
		return tjs.VoidValue, fmt.Errorf("Bitmap save-option allocation failed")
	}
	bppOptions, err := vm.NewDictionary()
	if err != nil {
		return tjs.VoidValue, err
	}
	bppObject, ok := vm.Objects.Lookup(bppOptions.ObjectRef().Object)
	if !ok {
		return tjs.VoidValue, fmt.Errorf("Bitmap bpp save-option allocation failed")
	}
	items, err := vm.NewArray([]tjs.Value{
		tjs.StringValue("32"),
		tjs.StringValue("24"),
		tjs.StringValue("8"),
	})
	if err != nil {
		return tjs.VoidValue, err
	}
	if err := bppObject.Set("type", tjs.StringValue("select")); err != nil {
		return tjs.VoidValue, err
	}
	if err := bppObject.Set("items", items); err != nil {
		return tjs.VoidValue, err
	}
	if err := bppObject.Set("desc", tjs.StringValue("bpp")); err != nil {
		return tjs.VoidValue, err
	}
	if err := bppObject.Set("default", tjs.IntegerValue(0)); err != nil {
		return tjs.VoidValue, err
	}
	if err := optionObject.Set("bpp", bppOptions); err != nil {
		return tjs.VoidValue, err
	}
	return options, nil
}

func bitmapJPEGSaveOption(vm *tjs.VM) (tjs.Value, error) {
	options, err := vm.NewDictionary()
	if err != nil {
		return tjs.VoidValue, err
	}
	optionObject, ok := vm.Objects.Lookup(options.ObjectRef().Object)
	if !ok {
		return tjs.VoidValue, fmt.Errorf("Bitmap JPEG save-option allocation failed")
	}
	quality, err := vm.NewDictionary()
	if err != nil {
		return tjs.VoidValue, err
	}
	qualityObject, ok := vm.Objects.Lookup(quality.ObjectRef().Object)
	if !ok {
		return tjs.VoidValue, fmt.Errorf("Bitmap JPEG quality save-option allocation failed")
	}
	if err := qualityObject.Set("type", tjs.StringValue("range")); err != nil {
		return tjs.VoidValue, err
	}
	if err := qualityObject.Set("min", tjs.IntegerValue(1)); err != nil {
		return tjs.VoidValue, err
	}
	if err := qualityObject.Set("max", tjs.IntegerValue(100)); err != nil {
		return tjs.VoidValue, err
	}
	if err := qualityObject.Set("desc", tjs.StringValue("100 is high quality, 1 is low quality")); err != nil {
		return tjs.VoidValue, err
	}
	if err := qualityObject.Set("default", tjs.IntegerValue(90)); err != nil {
		return tjs.VoidValue, err
	}
	if err := optionObject.Set("quality", quality); err != nil {
		return tjs.VoidValue, err
	}
	return options, nil
}

func encodeBitmapBMP(width, height uint32, pixels []byte, bpp int) ([]byte, error) {
	if width == 0 || height == 0 || width > bitmapMaxDimension || height > bitmapMaxDimension ||
		uint64(width)*uint64(height) > bitmapMaxPixels || (bpp != 8 && bpp != 24 && bpp != 32) {
		return nil, fmt.Errorf("Bitmap BMP encoder: invalid dimensions or bit depth")
	}
	if uint64(len(pixels)) != uint64(width)*uint64(height)*4 {
		return nil, fmt.Errorf("Bitmap BMP encoder: invalid pixel buffer")
	}
	rowBytes := (uint64(width)*uint64(bpp) + 31) / 32 * 4
	paletteBytes := uint64(0)
	if bpp == 8 {
		paletteBytes = 256 * 4
	}
	fileSize := uint64(54) + paletteBytes + rowBytes*uint64(height)
	if fileSize > math.MaxUint32 || fileSize > uint64(int(^uint(0)>>1)) {
		return nil, fmt.Errorf("Bitmap BMP encoder: image size exceeds limits")
	}
	out := make([]byte, int(fileSize))
	copy(out, "BM")
	binary.LittleEndian.PutUint32(out[2:6], uint32(fileSize))
	pixelOffset := uint32(54 + paletteBytes)
	binary.LittleEndian.PutUint32(out[10:14], pixelOffset)
	binary.LittleEndian.PutUint32(out[14:18], 40)
	binary.LittleEndian.PutUint32(out[18:22], width)
	binary.LittleEndian.PutUint32(out[22:26], height)
	binary.LittleEndian.PutUint16(out[26:28], 1)
	binary.LittleEndian.PutUint16(out[28:30], uint16(bpp))
	if bpp == 8 {
		writeBitmapBMPPalette(out[54:pixelOffset])
	}
	for row := range height {
		sourceY := height - 1 - row
		source := uint64(sourceY) * uint64(width) * 4
		destination := uint64(pixelOffset) + uint64(row)*rowBytes
		switch bpp {
		case 32:
			for x := range width {
				src := source + uint64(x)*4
				dst := destination + uint64(x)*4
				out[dst], out[dst+1], out[dst+2], out[dst+3] = pixels[src+2], pixels[src+1], pixels[src], pixels[src+3]
			}
		case 24:
			for x := range width {
				src := source + uint64(x)*4
				dst := destination + uint64(x)*3
				out[dst], out[dst+1], out[dst+2] = pixels[src+2], pixels[src+1], pixels[src]
			}
		case 8:
			for x := range width {
				src := source + uint64(x)*4
				out[destination+uint64(x)] = bitmapBMPPaletteIndex(pixels[src], pixels[src+1], pixels[src+2], x, sourceY)
			}
		}
	}
	return out, nil
}

func writeBitmapBMPPalette(palette []byte) {
	for r := range 6 {
		for g := range 7 {
			for b := range 6 {
				index := (r*7+g)*6 + b
				palette[index*4] = byte(r * 255 / 5)
				palette[index*4+1] = byte(g * 255 / 6)
				palette[index*4+2] = byte(b * 255 / 5)
			}
		}
	}
}

// C++ BMP saving uses this matrix to dither into its 6x7x6 8-bit palette.
var bitmapBMPDither4x4 = [4][4]byte{
	{0, 12, 2, 14},
	{8, 4, 10, 6},
	{3, 15, 1, 13},
	{11, 7, 9, 5},
}

func bitmapBMPQuantize(value byte, levels int, threshold byte) int {
	scaled := int(value) * (levels - 1)
	level := scaled / 255
	if int(threshold)*255 < (scaled%255)*16 {
		level++
	}
	if level >= levels {
		level = levels - 1
	}
	return level
}

func bitmapBMPPaletteIndex(r, g, b byte, x, y uint32) byte {
	dx, dy := int(x&3), int(y&3)
	redThreshold := bitmapBMPDither4x4[(dy+1)%2][(dx+1)%2]
	greenThreshold := bitmapBMPDither4x4[dy][(dx+1)%2]
	red := bitmapBMPQuantize(r, 6, redThreshold)
	green := bitmapBMPQuantize(g, 7, greenThreshold)
	blue := bitmapBMPQuantize(b, 6, bitmapBMPDither4x4[dy][dx])
	return byte(red + green*6 + blue*42)
}

func (s *Services) bitmapNative(vm *tjs.VM, context tjs.Value, operation string, args []tjs.Value, _ *uint64) (tjs.Value, error) {
	s.ensureInit()
	if operation == "loadHeader" || operation == "getSaveOption" {
		return s.bitmapStaticNative(vm, operation, args)
	}
	id, err := bitmapContext(context, operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	if operation == "@initialize" || operation == "Bitmap" {
		s.mu.Lock()
		if s.Bitmaps[id] != nil {
			s.mu.Unlock()
			return tjs.VoidValue, nil
		}
		var b *Bitmap
		if len(args) == 0 {
			b, err = newBitmap(bitmapDefaultDimension, bitmapDefaultDimension)
		} else if args[0].Kind() == tjs.KindString {
			b, err = newBitmap(bitmapDefaultDimension, bitmapDefaultDimension)
		} else if len(args) < 2 {
			err = fmt.Errorf("Bitmap.Bitmap requires width and height")
		} else {
			width, e := args[0].Int()
			if e != nil {
				err = e
			} else {
				height, e := args[1].Int()
				if e != nil {
					err = e
				} else if width < 0 || height < 0 || width > math.MaxUint32 || height > math.MaxUint32 {
					err = fmt.Errorf("Bitmap.Bitmap: dimensions exceed uint32")
				} else if len(args) > 2 {
					bpp, e := args[2].Int()
					if e != nil {
						err = e
					} else if bpp != 32 {
						err = fmt.Errorf("%w: Bitmap bpp %d", ErrUnsupportedDevice, bpp)
					} else {
						b, err = newBitmap(uint32(width), uint32(height))
					}
				} else {
					b, err = newBitmap(uint32(width), uint32(height))
				}
			}
		}
		if err != nil {
			s.mu.Unlock()
			return tjs.VoidValue, err
		}
		if b != nil && s.FontSystem != nil && strings.TrimSpace(s.FontSystem.DefaultFont) != "" {
			b.font.Face = s.FontSystem.DefaultFont
		}
		s.Bitmaps[id] = b
		s.mu.Unlock()
		if len(args) > 0 && args[0].Kind() == tjs.KindString {
			if _, err := s.bitmapLoad(vm, b, args[0].Text()); err != nil {
				s.mu.Lock()
				delete(s.Bitmaps, id)
				s.mu.Unlock()
				return tjs.VoidValue, err
			}
		}
		return tjs.VoidValue, nil
	}
	if operation == "@invalidate" || operation == "finalize" {
		s.mu.Lock()
		delete(s.Bitmaps, id)
		s.bitmapLoadGeneration[id]++
		delete(s.bitmapLoadResults, id)
		s.mu.Unlock()
		s.Events.Cancel(uint64(id), EventBitmapLoad)
		return tjs.VoidValue, nil
	}
	b, _, err := s.bitmapForContext(context, operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	if strings.HasPrefix(operation, "get:") {
		key := strings.TrimPrefix(operation, "get:")
		s.mu.RLock()
		defer s.mu.RUnlock()
		switch key {
		case "width":
			return tjs.IntegerValue(int64(b.Width)), nil
		case "height":
			return tjs.IntegerValue(int64(b.Height)), nil
		case "loading":
			if b.loading {
				return tjs.IntegerValue(1), nil
			}
			return tjs.IntegerValue(0), nil
		case "buffer", "bufferForWrite":
			return tjs.VoidValue, UnsupportedDevice("Bitmap pixel buffer pointer")
		case "bufferPitch":
			return tjs.IntegerValue(int64(b.Width) * 4), nil
		default:
			return tjs.VoidValue, fmt.Errorf("%w: unsupported Bitmap property %s", ErrUnsupportedNative, key)
		}
	}
	if strings.HasPrefix(operation, "set:") {
		key := strings.TrimPrefix(operation, "set:")
		n, err := bitmapIntArg(args, 0, operation)
		if err != nil {
			return tjs.VoidValue, err
		}
		if n < 0 || n > math.MaxUint32 {
			return tjs.VoidValue, fmt.Errorf("Bitmap.%s: value exceeds uint32", operation)
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		switch key {
		case "width":
			err = b.resize(uint32(n), b.Height, true)
		case "height":
			err = b.resize(b.Width, uint32(n), true)
		default:
			return tjs.VoidValue, fmt.Errorf("%w: unsupported Bitmap property %s", ErrUnsupportedNative, key)
		}
		return tjs.VoidValue, err
	}
	switch operation {
	case "getPixel", "getMaskPixel":
		x, err := bitmapIntArg(args, 0, operation)
		if err != nil {
			return tjs.VoidValue, err
		}
		y, err := bitmapIntArg(args, 1, operation)
		if err != nil {
			return tjs.VoidValue, err
		}
		s.mu.RLock()
		p, err := b.pixel(x, y)
		if err != nil {
			s.mu.RUnlock()
			return tjs.VoidValue, err
		}
		if operation == "getMaskPixel" {
			value := tjs.IntegerValue(int64(p[3]))
			s.mu.RUnlock()
			return value, nil
		}
		value := tjs.IntegerValue(int64(p[0])<<16 | int64(p[1])<<8 | int64(p[2]))
		s.mu.RUnlock()
		return value, nil
	case "setPixel", "setMaskPixel":
		x, err := bitmapIntArg(args, 0, operation)
		if err != nil {
			return tjs.VoidValue, err
		}
		y, err := bitmapIntArg(args, 1, operation)
		if err != nil {
			return tjs.VoidValue, err
		}
		value, err := bitmapIntArg(args, 2, operation)
		if err != nil {
			return tjs.VoidValue, err
		}
		s.mu.Lock()
		p, err := b.pixel(x, y)
		if err == nil {
			if operation == "setMaskPixel" {
				p[3] = uint8(value)
			} else {
				p[0], p[1], p[2] = uint8(uint64(value)>>16), uint8(uint64(value)>>8), uint8(value)
			}
		}
		s.mu.Unlock()
		return tjs.VoidValue, err
	case "independ":
		copyImage := true
		if len(args) > 0 && !args[0].IsVoid() {
			copyImage, err = args[0].Truth()
			if err != nil {
				return tjs.VoidValue, err
			}
		}
		if copyImage {
			s.mu.Lock()
			b.RGBA = append([]byte(nil), b.RGBA...)
			s.mu.Unlock()
		}
		return tjs.VoidValue, nil
	case "setSize":
		if len(args) < 2 {
			return tjs.VoidValue, fmt.Errorf("Bitmap.setSize: missing argument %d", len(args))
		}
		width, err := bitmapIntArg(args, 0, operation)
		if err != nil {
			return tjs.VoidValue, err
		}
		height, err := bitmapIntArg(args, 1, operation)
		if err != nil {
			return tjs.VoidValue, err
		}
		keep := true
		if len(args) > 2 && !args[2].IsVoid() {
			keep, err = args[2].Truth()
			if err != nil {
				return tjs.VoidValue, err
			}
		}
		if width < 0 || height < 0 || width > math.MaxUint32 || height > math.MaxUint32 {
			return tjs.VoidValue, fmt.Errorf("Bitmap.setSize: dimensions exceed uint32")
		}
		s.mu.Lock()
		err = b.resize(uint32(width), uint32(height), keep)
		s.mu.Unlock()
		return tjs.VoidValue, err
	case "copyFrom":
		v, err := bitmapArg(args, 0, operation)
		if err != nil {
			return tjs.VoidValue, err
		}
		src, _, err := s.bitmapFromValue(v)
		if err != nil {
			return tjs.VoidValue, err
		}
		s.mu.Lock()
		b.Width, b.Height = src.Width, src.Height
		b.RGBA = append(b.RGBA[:0], src.RGBA...)
		b.font = src.font
		s.mu.Unlock()
		return tjs.VoidValue, nil
	case "load":
		name, err := bitmapArg(args, 0, operation)
		if err != nil {
			return tjs.VoidValue, err
		}
		if name.Kind() != tjs.KindString {
			return tjs.VoidValue, fmt.Errorf("Bitmap.load requires a storage name")
		}
		s.mu.Lock()
		if b.loading {
			s.mu.Unlock()
			return tjs.VoidValue, fmt.Errorf("Bitmap.load: bitmap is already loading")
		}
		b.loading = true
		s.mu.Unlock()
		result, err := s.bitmapLoad(vm, b, name.Text())
		if err != nil {
			s.mu.Lock()
			b.loading = false
			s.mu.Unlock()
		}
		return result, err
	case "save":
		name, err := bitmapArg(args, 0, operation)
		if err != nil {
			return tjs.VoidValue, err
		}
		if name.Kind() != tjs.KindString {
			return tjs.VoidValue, fmt.Errorf("Bitmap.save requires a storage name")
		}
		typeName := "bmp"
		if len(args) > 1 && !args[1].IsVoid() {
			typeName = args[1].Text()
		}
		isPNG := strings.EqualFold(typeName, "png")
		isJPEG := bitmapJPEGType(typeName)
		bpp := 0
		quality := 90
		if isJPEG {
			quality = bitmapJPEGQuality(typeName)
			if len(args) > 2 && !args[2].IsVoid() {
				if unsupported := bitmapUnsupportedJPEGOption(vm, args[2]); unsupported != "" {
					return tjs.VoidValue, UnsupportedDevice("Bitmap JPEG option " + unsupported)
				}
				quality = bitmapSaveOptionQuality(vm, args[2], quality)
			}
		} else if !isPNG {
			var supported bool
			supported, bpp = bitmapBMPType(typeName)
			if !supported {
				return tjs.VoidValue, UnsupportedDevice("Bitmap encoder " + typeName)
			}
			if len(args) > 2 && !args[2].IsVoid() {
				bpp = bitmapSaveOptionBPP(vm, args[2], bpp)
			}
		}
		s.mu.RLock()
		width, height := b.Width, b.Height
		pixels := append([]byte(nil), b.RGBA...)
		s.mu.RUnlock()
		if isPNG {
			encoded := &image.RGBA{Pix: pixels, Stride: int(width) * 4, Rect: image.Rect(0, 0, int(width), int(height))}
			var out bytes.Buffer
			if err := png.Encode(&out, encoded); err != nil {
				return tjs.VoidValue, err
			}
			return tjs.VoidValue, s.writeSave(name.Text(), out.Bytes(), 0)
		}
		if isJPEG {
			for alpha := 3; alpha < len(pixels); alpha += 4 {
				pixels[alpha] = 255
			}
			encoded := &image.RGBA{Pix: pixels, Stride: int(width) * 4, Rect: image.Rect(0, 0, int(width), int(height))}
			var out bytes.Buffer
			if err := jpeg.Encode(&out, encoded, &jpeg.Options{Quality: quality}); err != nil {
				return tjs.VoidValue, err
			}
			return tjs.VoidValue, s.writeSave(name.Text(), out.Bytes(), 0)
		}
		out, err := encodeBitmapBMP(width, height, pixels, bpp)
		if err != nil {
			return tjs.VoidValue, err
		}
		return tjs.VoidValue, s.writeSave(name.Text(), out, 0)
	case "loadAsync":
		name, err := bitmapArg(args, 0, operation)
		if err != nil {
			return tjs.VoidValue, err
		}
		if name.Kind() != tjs.KindString {
			return tjs.VoidValue, fmt.Errorf("Bitmap.loadAsync requires a storage name")
		}
		return tjs.VoidValue, s.bitmapLoadAsync(id, b, name.Text())
	case "onLoaded":
		return tjs.VoidValue, nil
	default:
		return tjs.VoidValue, fmt.Errorf("%w: Bitmap.%s", ErrUnsupportedNative, operation)
	}
}

func (s *Services) bitmapStaticNative(vm *tjs.VM, operation string, args []tjs.Value) (tjs.Value, error) {
	if len(args) < 1 {
		return tjs.VoidValue, fmt.Errorf("Bitmap.%s: missing argument 0", operation)
	}
	name := args[0].Text()
	if operation == "getSaveOption" {
		if supported, _ := bitmapBMPType(name); supported {
			return bitmapSaveOption(vm)
		}
		if bitmapJPEGType(name) {
			return bitmapJPEGSaveOption(vm)
		}
		if !strings.EqualFold(name, "png") {
			return tjs.VoidValue, nil
		}
		value, err := bitmapMetadata(vm, nil, 0, 0)
		if err != nil {
			return tjs.VoidValue, err
		}
		if object, ok := vm.Objects.Lookup(value.ObjectRef().Object); ok {
			_ = object.Set("type", tjs.StringValue("png"))
			_ = object.Set("extension", tjs.StringValue("png"))
		}
		return value, nil
	}
	if operation != "loadHeader" {
		return tjs.VoidValue, fmt.Errorf("%w: Bitmap.%s", ErrUnsupportedNative, operation)
	}
	data, err := bitmapBytes(s, name)
	if err != nil {
		return tjs.VoidValue, err
	}
	im, err := assets.DecodeImage(data)
	if err != nil {
		return tjs.VoidValue, err
	}
	return bitmapMetadata(vm, data, im.Width, im.Height)
}
