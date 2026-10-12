package runtime

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/eslzzyl/krkrgo/internal/assets"
	"github.com/eslzzyl/krkrgo/internal/tjs"
)

const (
	layerLoadImagesDefaultKey uint32 = 0x1fffffff
	layerLoadImagesAutoKey    uint32 = 0x01ffffff
	layerLoadImagesPaletteKey uint32 = 0x03000000
	layerLoadImagesMatteKey   uint32 = 0x04000000
)

// This follows the native graphic handler order. JXR is recognized as a
// graphic extension but has no decoder in the Go assets package.
var layerGraphicExtensions = []string{
	".jxr", ".tlg6", ".tlg5", ".tlg", ".png", ".jif", ".jpg", ".jpeg", ".dib", ".bmp", ".gif",
}

func supportedLayerImageExtension(ext string) bool {
	switch ext {
	case ".tlg", ".tlg5", ".tlg6", ".png", ".jif", ".jpg", ".jpeg", ".dib", ".bmp", ".gif":
		return true
	default:
		return false
	}
}

func (s *Services) readLayerImage(name string) ([]byte, error) {
	if name == "" {
		return nil, fmt.Errorf("empty storage name")
	}
	if ext := strings.ToLower(extractStorageExt(name)); ext != "" {
		data, err := s.readStorage(name)
		if err != nil {
			return nil, err
		}
		if !supportedLayerImageExtension(ext) {
			return nil, fmt.Errorf("%w: Layer.loadImages extension %q", assets.ErrUnsupported, ext)
		}
		return data, nil
	}

	var lastErr error
	for _, ext := range layerGraphicExtensions {
		data, err := s.readStorage(name + ext)
		if errors.Is(err, assets.ErrNotFound) {
			lastErr = err
			continue
		}
		if err != nil {
			return nil, err
		}
		if !supportedLayerImageExtension(ext) {
			return nil, fmt.Errorf("%w: Layer.loadImages extension %q", assets.ErrUnsupported, ext)
		}
		return data, nil
	}
	if lastErr != nil {
		return nil, fmt.Errorf("Layer.loadImages graphic %q: %w", name, lastErr)
	}
	return nil, fmt.Errorf("Layer.loadImages graphic %q not found", name)
}

func layerImageIndexedFormat(data []byte) bool {
	if bytes.HasPrefix(data, []byte("BM")) {
		return len(data) >= 30 && binary.LittleEndian.Uint16(data[28:30]) <= 8
	}
	const pngSignature = "\x89PNG\r\n\x1a\n"
	return len(data) > 25 && bytes.HasPrefix(data, []byte(pngSignature)) && data[25] == 3
}

func applyLayerImageKey(im *assets.Image, data []byte, key uint32) error {
	if key == layerLoadImagesDefaultKey {
		return nil
	}
	pixels := im.RGBA
	switch {
	case key == layerLoadImagesAutoKey:
		rowBytes := int(im.Width) * 4
		counts := make(map[uint32]int, im.Width)
		var selected uint32
		maximum := 0
		for offset := 0; offset < rowBytes; offset += 4 {
			p := pixels[offset : offset+4]
			color := uint32(p[0])<<16 | uint32(p[1])<<8 | uint32(p[2])
			counts[color]++
			if counts[color] > maximum {
				maximum, selected = counts[color], color
			}
		}
		setLayerImageKey(pixels, selected)
	case key&0xff000000 == 0:
		setLayerImageKey(pixels, key&0x00ffffff)
	case key&0xff000000 == layerLoadImagesPaletteKey:
		if !layerImageIndexedFormat(data) {
			return nil
		}
		indexed, err := assets.DecodeIndexedImage(data)
		if err != nil {
			return err
		}
		if indexed.Width != im.Width || indexed.Height != im.Height || len(indexed.Pixels) != int(im.Width*im.Height) {
			return fmt.Errorf("%w: Layer.loadImages indexed image dimensions mismatch", assets.ErrMalformed)
		}
		for i, index := range indexed.Pixels {
			if index == uint8(key) {
				pixels[i*4+3] = 0
			} else {
				pixels[i*4+3] = 255
			}
		}
	case key&0xff000000 == layerLoadImagesMatteKey:
		matte := [3]int32{int32(key >> 16 & 0xff), int32(key >> 8 & 0xff), int32(key & 0xff)}
		for i := 0; i < len(pixels); i += 4 {
			alpha := int32(pixels[i+3])
			for channel := range 3 {
				value := int32(pixels[i+channel])
				pixels[i+channel] = uint8(matte[channel] + ((value - matte[channel]) * alpha >> 8))
			}
			pixels[i+3] = 255
		}
	}
	return nil
}

func setLayerImageKey(pixels []byte, color uint32) {
	for i := 0; i < len(pixels); i += 4 {
		p := pixels[i : i+4]
		matches := (uint32(p[0])<<16 | uint32(p[1])<<8 | uint32(p[2])) == color
		if matches {
			p[3] = 0
		} else {
			p[3] = 255
		}
	}
}

func layerImageMetadata(vm *tjs.VM, tags map[string]string) (tjs.Value, error) {
	if len(tags) == 0 {
		return tjs.NullValue, nil
	}
	result, err := vm.NewDictionary()
	if err != nil {
		return tjs.VoidValue, err
	}
	object, ok := vm.Objects.Lookup(result.ObjectRef().Object)
	if !ok {
		return tjs.VoidValue, fmt.Errorf("Layer.loadImages metadata allocation failed")
	}
	for name, value := range tags {
		if err := object.Set(name, tjs.StringValue(value)); err != nil {
			return tjs.VoidValue, err
		}
	}
	return result, nil
}

func (s *Services) layerLoadImages(vm *tjs.VM, layer *Layer, id tjs.ObjectID, args []tjs.Value) (tjs.Value, error) {
	if len(args) == 0 {
		return tjs.VoidValue, fmt.Errorf("Layer.loadImages requires a storage")
	}
	name := ""
	key := layerLoadImagesDefaultKey
	if args[0].Kind() == tjs.KindObject && !args[0].IsNull() {
		if vm == nil || vm.Objects == nil {
			return tjs.VoidValue, fmt.Errorf("Layer.loadImages requires a VM")
		}
		options, ok := vm.Objects.Lookup(args[0].ObjectRef().Object)
		if !ok || !options.IsValid() {
			return tjs.VoidValue, fmt.Errorf("Layer.loadImages requires a valid options object")
		}
		storage, ok := options.Get("storage")
		if !ok || storage.IsVoid() {
			return tjs.VoidValue, fmt.Errorf("Layer.loadImages requires a storage")
		}
		name = storage.Text()
		if value, ok := options.Get("key"); ok && !value.IsVoid() {
			n, err := value.Int()
			if err != nil {
				return tjs.VoidValue, err
			}
			key = uint32(n)
		}
	} else {
		name = args[0].Text()
	}
	if len(args) > 1 && !args[1].IsVoid() {
		n, err := args[1].Int()
		if err != nil {
			return tjs.VoidValue, err
		}
		key = uint32(n)
	}

	s.mu.RLock()
	validType := layer.Type != LayerBinder && layer.Type != LayerEffect && layer.Type != LayerFilter
	s.mu.RUnlock()
	if !validType {
		return tjs.VoidValue, fmt.Errorf("%w: layer type cannot have an image", ErrInvalidLayer)
	}

	profile := os.Getenv("KRKRGO_RENDER_STATS") != ""
	readStarted := time.Now()
	data, err := s.readLayerImage(name)
	readTime := time.Since(readStarted)
	if err != nil {
		return tjs.VoidValue, fmt.Errorf("Layer.loadImages %q: %w", name, err)
	}
	decodeStarted := time.Now()
	decoded, err := s.Storage.DecodeImage(data)
	decodeTime := time.Since(decodeStarted)
	if err != nil {
		return tjs.VoidValue, fmt.Errorf("Layer.loadImages %q: %w", name, err)
	}
	width, height := int32(decoded.Width), int32(decoded.Height)
	if !validLayerImageSize(width, height, len(decoded.RGBA), 4) {
		return tjs.VoidValue, fmt.Errorf("%w: Layer.loadImages image dimensions %dx%d", ErrInvalidLayer, width, height)
	}
	if profile && readTime+decodeTime >= 2*time.Millisecond {
		log.Printf("krkrgo startup-image storage=%q bytes=%d decoded=%dx%d read=%s decode=%s", name, len(data), width, height, readTime, decodeTime)
	}
	if err := applyLayerImageKey(&decoded, data, key); err != nil {
		return tjs.VoidValue, fmt.Errorf("Layer.loadImages %q color key: %w", name, err)
	}
	tags, err := assets.ImageMetadata(data)
	if err != nil {
		return tjs.VoidValue, fmt.Errorf("Layer.loadImages %q metadata: %w", name, err)
	}
	result, err := layerImageMetadata(vm, tags)
	if err != nil {
		return tjs.VoidValue, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Layers[id] != layer {
		return tjs.VoidValue, fmt.Errorf("Layer.loadImages: context has no Layer native instance")
	}
	if layer.Type == LayerBinder || layer.Type == LayerEffect || layer.Type == LayerFilter {
		return tjs.VoidValue, fmt.Errorf("%w: layer type cannot have an image", ErrInvalidLayer)
	}
	if width < layer.Bounds.Width {
		layer.Bounds.Width = width
		layer.ImageLeft = 0
	}
	if height < layer.Bounds.Height {
		layer.Bounds.Height = height
		layer.ImageTop = 0
	}
	layer.ImageLeft = maxInt32(layer.ImageLeft, layer.Bounds.Width-width)
	layer.ImageTop = maxInt32(layer.ImageTop, layer.Bounds.Height-height)
	layer.RGBA = decoded.RGBA
	layer.ImageWidth, layer.ImageHeight = width, height
	layer.ClearProvince()
	resetNativeLayerClip(layer)
	layer.imageModified = true
	return result, nil
}

// layerLoadProvinceImage loads the palette indices as the hit-test/composition
// province plane, not as visible RGB pixels.
func (s *Services) layerLoadProvinceImage(layer *Layer, id tjs.ObjectID, args []tjs.Value) error {
	if len(args) == 0 {
		return fmt.Errorf("Layer.loadProvinceImage requires a storage")
	}
	name := args[0].Text()

	s.mu.Lock()
	if s.Layers[id] != layer {
		s.mu.Unlock()
		return fmt.Errorf("Layer.loadProvinceImage: context has no Layer native instance")
	}
	if !validLayerImageSize(layer.ImageWidth, layer.ImageHeight, len(layer.RGBA), 4) {
		s.mu.Unlock()
		return fmt.Errorf("%w: Layer.loadProvinceImage requires a main image", ErrInvalidLayer)
	}
	width, height := layer.ImageWidth, layer.ImageHeight
	layer.Province = nil
	layer.ProvinceWidth, layer.ProvinceHeight = 0, 0
	s.mu.Unlock()

	clearProvinceOnError := func(err error) error {
		if err != nil {
			s.mu.Lock()
			if s.Layers[id] == layer {
				layer.Province = nil
				layer.ProvinceWidth, layer.ProvinceHeight = 0, 0
			}
			s.mu.Unlock()
		}
		return err
	}

	data, err := s.readLayerImage(name)
	if err != nil {
		return clearProvinceOnError(fmt.Errorf("Layer.loadProvinceImage %q: %w", name, err))
	}
	indexed, err := assets.DecodeIndexedImage(data)
	if err != nil {
		return clearProvinceOnError(fmt.Errorf("Layer.loadProvinceImage %q: %w", name, err))
	}
	if indexed.Width == 0 || indexed.Height == 0 ||
		indexed.Width > uint32(width) || indexed.Height > uint32(height) ||
		!validLayerImageSize(int32(indexed.Width), int32(indexed.Height), len(indexed.Pixels), 1) ||
		!validLayerImageSize(width, height, int(uint64(width)*uint64(height)), 1) {
		return clearProvinceOnError(fmt.Errorf("%w: Layer.loadProvinceImage %q is %dx%d, cannot tile to main image %dx%d",
			ErrProvinceSizeMismatch, name, indexed.Width, indexed.Height, width, height))
	}
	tiled := make([]byte, int(width)*int(height))
	sourceWidth, sourceHeight := int(indexed.Width), int(indexed.Height)
	for y := 0; y < int(height); y++ {
		sourceRow := (y % sourceHeight) * sourceWidth
		targetRow := y * int(width)
		for x := 0; x < int(width); x++ {
			tiled[targetRow+x] = indexed.Pixels[sourceRow+x%sourceWidth]
		}
	}
	indexed.Pixels = tiled

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Layers[id] != layer || layer.ImageWidth != width || layer.ImageHeight != height ||
		!validLayerImageSize(width, height, len(layer.RGBA), 4) {
		layer.Province = nil
		layer.ProvinceWidth, layer.ProvinceHeight = 0, 0
		return fmt.Errorf("%w: Layer.loadProvinceImage main image changed during load", ErrInvalidLayer)
	}
	layer.Province = append(layer.Province[:0], indexed.Pixels...)
	layer.ProvinceWidth, layer.ProvinceHeight = width, height
	layer.imageModified = true
	return nil
}
