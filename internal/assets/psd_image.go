package assets

// Channel decoding follows psdimage.cpp: decodePackBits, the zlib decoders
// (with and without prediction) and mergeChannels{Rgb,Gray,Cmyk,Index,Bitmap}.
// Only 8-bit channels and 1-bit bitmap data are decoded; 16/32-bit channel
// layouts are reported as unsupported instead of being truncated to a byte.

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"io"
)

// PSDImageMode selects the layer plane requested from a layer record.
type PSDImageMode int

const (
	// PSDImageRaw returns the layer pixels without its layer mask.
	PSDImageRaw PSDImageMode = iota
	// PSDImageMask returns only the layer mask as a grayscale image.
	PSDImageMask
	// PSDImageMasked multiplies the layer mask into the layer alpha.
	PSDImageMasked
)

const (
	psdCompressionRaw    = 0
	psdCompressionRLE    = 1
	psdCompressionZIP    = 2
	psdCompressionZIPRLE = 3
)

func psdPlaneSize(width, height int32, depth int) (int, error) {
	if width <= 0 || height <= 0 {
		return 0, ErrPSDEmptyLayer
	}
	if int64(width)*int64(height) > psdMaxPixels {
		return 0, psdMalformed("plane %dx%d exceeds pixel limit", width, height)
	}
	switch depth {
	case 1:
		return int((int64(width) + 7) / 8 * int64(height)), nil
	case 8:
		return int(int64(width) * int64(height)), nil
	default:
		return 0, psdUnsupported("depth %d channels", depth)
	}
}

func psdRowBytes(width int32, depth int) int {
	if depth == 1 {
		return int((int64(width) + 7) / 8)
	}
	return int(width)
}

// decodePackBitsRow expands one RLE row (Photoshop's PackBits variant).
func decodePackBitsRow(dst, src []byte) (int, error) {
	out, in := 0, 0
	for out < len(dst) {
		if in >= len(src) {
			return in, psdMalformed("truncated RLE row: decoded %d of %d bytes", out, len(dst))
		}
		n := int(src[in])
		in++
		switch {
		case n < 128:
			count := n + 1
			if in+count > len(src) {
				return in, psdMalformed("truncated RLE literal run")
			}
			if out+count > len(dst) {
				return in, psdMalformed("RLE literal run overflows row")
			}
			copy(dst[out:], src[in:in+count])
			out += count
			in += count
		case n > 128:
			count := 257 - n
			if in >= len(src) {
				return in, psdMalformed("truncated RLE repeat run")
			}
			if out+count > len(dst) {
				return in, psdMalformed("RLE repeat run overflows row")
			}
			value := src[in]
			in++
			for i := range count {
				dst[out+i] = value
			}
			out += count
		default:
			// 128 is a no-op.
		}
	}
	return in, nil
}

func decodePackBitsPlane(data []byte, rowBytes, height int) ([]byte, error) {
	if height <= 0 || rowBytes <= 0 {
		return nil, ErrPSDEmptyLayer
	}
	headerSize := 2 * height
	if len(data) < headerSize {
		return nil, psdMalformed("RLE channel needs %d count bytes, got %d", headerSize, len(data))
	}
	out := make([]byte, rowBytes*height)
	offset := headerSize
	for y := range height {
		count := int(data[y*2])<<8 | int(data[y*2+1])
		if count > len(data)-offset {
			return nil, psdMalformed("RLE row %d length %d exceeds channel data", y, count)
		}
		consumed, err := decodePackBitsRow(out[y*rowBytes:(y+1)*rowBytes], data[offset:offset+count])
		if err != nil {
			return nil, err
		}
		if consumed != count {
			return nil, psdMalformed("RLE row %d used %d of %d bytes", y, consumed, count)
		}
		offset += count
	}
	return out, nil
}

func psdInflate(data []byte, expected int) ([]byte, error) {
	if expected < 0 {
		return nil, psdMalformed("negative inflate size")
	}
	reader, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, psdMalformed("zlib channel header: %v", err)
	}
	out, readErr := io.ReadAll(io.LimitReader(reader, int64(expected)+1))
	closeErr := reader.Close()
	if readErr != nil {
		return nil, psdMalformed("zlib channel data: %v", readErr)
	}
	if closeErr != nil {
		return nil, psdMalformed("zlib channel trailer: %v", closeErr)
	}
	if len(out) != expected {
		return nil, psdMalformed("zlib channel decoded %d bytes, want %d", len(out), expected)
	}
	return out, nil
}

// psdInflateStream inflates one zlib stream and reports how many input bytes
// it consumed, so consecutive composite channel streams can be decoded.
func psdInflateStream(data []byte, expected int) ([]byte, int, error) {
	source := bytes.NewReader(data)
	reader, err := zlib.NewReader(source)
	if err != nil {
		return nil, 0, psdMalformed("zlib channel header: %v", err)
	}
	out, readErr := io.ReadAll(io.LimitReader(reader, int64(expected)+1))
	closeErr := reader.Close()
	consumed := len(data) - source.Len()
	if readErr != nil {
		return nil, consumed, psdMalformed("zlib channel data: %v", readErr)
	}
	if closeErr != nil {
		return nil, consumed, psdMalformed("zlib channel trailer: %v", closeErr)
	}
	if len(out) != expected {
		return nil, consumed, psdMalformed("zlib channel decoded %d bytes, want %d", len(out), expected)
	}
	return out, consumed, nil
}

func undoPSDPrediction(plane []byte, width, height, depth int) error {
	if depth != 8 {
		return psdUnsupported("ZIP prediction for depth %d", depth)
	}
	if width <= 0 || height <= 0 || len(plane) != int(width)*int(height) {
		return psdMalformed("prediction plane size mismatch")
	}
	for y := range height {
		row := plane[y*width : (y+1)*width]
		for x := 1; x < len(row); x++ {
			row[x] += row[x-1]
		}
	}
	return nil
}

func (p *PSD) decodeChannel(payload []byte, width, height int32) ([]byte, error) {
	size, err := psdPlaneSize(width, height, p.Header.Depth)
	if err != nil {
		return nil, err
	}
	r := &psdReader{data: payload}
	compression, err := r.u16()
	if err != nil {
		return nil, err
	}
	switch compression {
	case psdCompressionRaw:
		data, err := r.bytes(size)
		if err != nil {
			return nil, err
		}
		return data, nil
	case psdCompressionRLE:
		return decodePackBitsPlane(r.data[r.pos:], psdRowBytes(width, p.Header.Depth), int(height))
	case psdCompressionZIP:
		return psdInflate(r.data[r.pos:], size)
	case psdCompressionZIPRLE:
		plane, err := psdInflate(r.data[r.pos:], size)
		if err != nil {
			return nil, err
		}
		if err := undoPSDPrediction(plane, int(width), int(height), p.Header.Depth); err != nil {
			return nil, err
		}
		return plane, nil
	default:
		return nil, psdUnsupported("channel compression %d", compression)
	}
}

func (p *PSD) decodeCompositeChannels() ([][]byte, error) {
	size, err := psdPlaneSize(p.Header.Width, p.Header.Height, p.Header.Depth)
	if err != nil {
		return nil, err
	}
	channels := p.Header.Channels
	planes := make([][]byte, channels)
	r := &psdReader{data: p.composite}
	compression, err := r.u16()
	if err != nil {
		return nil, err
	}
	switch compression {
	case psdCompressionRaw:
		for i := range planes {
			data, err := r.bytes(size)
			if err != nil {
				return nil, err
			}
			planes[i] = data
		}
	case psdCompressionRLE:
		rowBytes := psdRowBytes(p.Header.Width, p.Header.Depth)
		counts := make([][]int, channels)
		for c := range planes {
			counts[c] = make([]int, p.Header.Height)
			for y := range counts[c] {
				count, err := r.u16()
				if err != nil {
					return nil, err
				}
				if int(count) > r.rest() {
					return nil, psdMalformed("composite RLE row %d/%d length %d exceeds data", c, y, count)
				}
				counts[c][y] = int(count)
			}
		}
		for c := range planes {
			planes[c] = make([]byte, rowBytes*int(p.Header.Height))
			for y := range int(p.Header.Height) {
				row := planes[c][y*rowBytes : (y+1)*rowBytes]
				data, err := r.bytes(counts[c][y])
				if err != nil {
					return nil, err
				}
				consumed, err := decodePackBitsRow(row, data)
				if err != nil {
					return nil, err
				}
				if consumed != len(data) {
					return nil, psdMalformed("composite RLE row %d/%d used %d of %d bytes", c, y, consumed, len(data))
				}
			}
		}
	case psdCompressionZIP, psdCompressionZIPRLE:
		for i := range planes {
			plane, consumed, err := psdInflateStream(r.data[r.pos:], size)
			if err != nil {
				return nil, err
			}
			if consumed <= 0 {
				return nil, psdMalformed("composite zlib channel %d consumed no data", i)
			}
			r.pos += consumed
			if compression == psdCompressionZIPRLE {
				if err := undoPSDPrediction(plane, int(p.Header.Width), int(p.Header.Height), p.Header.Depth); err != nil {
					return nil, err
				}
			}
			planes[i] = plane
		}
	default:
		return nil, psdUnsupported("composite compression %d", compression)
	}
	return planes, nil
}

// Composite returns the merged image stored in the PSD as straight-alpha RGBA.
func (p *PSD) Composite() (Image, error) {
	if !p.HasComposite {
		return Image{}, ErrPSDNoComposite
	}
	if p.Header.Depth != 1 && p.Header.Depth != 8 {
		return Image{}, psdUnsupported("composite depth %d", p.Header.Depth)
	}
	if p.Header.Depth == 1 && p.Header.Mode != PSDColorModeBitmap {
		return Image{}, psdUnsupported("1-bit color mode %d", p.Header.Mode)
	}
	planes, err := p.decodeCompositeChannels()
	if err != nil {
		return Image{}, err
	}
	return p.mergeComposite(planes)
}

func (p *PSD) mergeComposite(planes [][]byte) (Image, error) {
	width, height := p.Header.Width, p.Header.Height
	out := make([]byte, int(width)*int(height)*4)
	switch p.Header.Mode {
	case PSDColorModeBitmap:
		if len(planes) < 1 {
			return Image{}, psdUnsupported("bitmap composite without channel")
		}
		unpackBitmapPlane(out, planes[0], int(width), int(height))
	case PSDColorModeGrayscale:
		if len(planes) < 1 {
			return Image{}, psdUnsupported("grayscale composite without channel")
		}
		var alpha []byte
		if len(planes) > 1 {
			alpha = planes[1]
		}
		mergeGrayPlane(out, planes[0], alpha, int(width), int(height))
	case PSDColorModeIndexed:
		if len(planes) < 1 {
			return Image{}, psdUnsupported("indexed composite without channel")
		}
		if !p.colorTableSet {
			return Image{}, psdUnsupported("indexed composite without color table")
		}
		mergeIndexedPlane(out, planes[0], &p.ColorTable, int(width), int(height))
	case PSDColorModeRGB:
		if len(planes) < 3 {
			return Image{}, psdUnsupported("RGB composite has %d channels", len(planes))
		}
		var alpha []byte
		if len(planes) > 3 {
			alpha = planes[3]
		}
		mergeRGBPlanes(out, planes[0], planes[1], planes[2], alpha, int(width), int(height))
	case PSDColorModeCMYK:
		if len(planes) < 4 {
			return Image{}, psdUnsupported("CMYK composite has %d channels", len(planes))
		}
		var alpha []byte
		if len(planes) > 4 {
			alpha = planes[4]
		}
		mergeCMYKPlanes(out, planes[0], planes[1], planes[2], planes[3], alpha, int(width), int(height))
	case PSDColorModeMultichannel, PSDColorModeDuotone, PSDColorModeLAB:
		return Image{}, psdUnsupported("color mode %d", p.Header.Mode)
	default:
		return Image{}, psdUnsupported("color mode %d", p.Header.Mode)
	}
	return Image{Width: uint32(width), Height: uint32(height), RGBA: out}, nil
}

// LayerMask returns the parsed layer mask rectangle and default color.
func (p *PSD) LayerMask(index int) (PSDLayerMask, error) {
	if index < 0 || index >= len(p.Layers) {
		return PSDLayerMask{}, psdMalformed("layer index %d outside 0..%d", index, len(p.Layers))
	}
	return p.Layers[index].Mask, nil
}

// LayerImage decodes one layer in the requested image mode.
func (p *PSD) LayerImage(index int, mode PSDImageMode) (Image, error) {
	if index < 0 || index >= len(p.Layers) {
		return Image{}, psdMalformed("layer index %d outside 0..%d", index, len(p.Layers))
	}
	if p.Header.Depth != 1 && p.Header.Depth != 8 {
		return Image{}, psdUnsupported("layer depth %d", p.Header.Depth)
	}
	if p.Header.Depth == 1 && p.Header.Mode != PSDColorModeBitmap {
		return Image{}, psdUnsupported("1-bit color mode %d", p.Header.Mode)
	}
	layer := &p.Layers[index]
	if p.Header.Depth == 1 && mode != PSDImageRaw &&
		layer.Mask.Width > 0 && layer.Mask.Height > 0 {
		return Image{}, psdUnsupported("1-bit layer mask")
	}
	if mode == PSDImageMask {
		return p.layerMaskImage(layer)
	}
	if layer.Width <= 0 || layer.Height <= 0 {
		return Image{}, ErrPSDEmptyLayer
	}

	var planes []psdLayerPlane
	var maskPlane []byte
	for i := range layer.Channels {
		channel := &layer.Channels[i]
		if channel.IsMask() {
			if mode == PSDImageRaw || layer.Mask.Width <= 0 || layer.Mask.Height <= 0 {
				continue
			}
			data, err := p.decodeChannel(channel.Payload, layer.Mask.Width, layer.Mask.Height)
			if err != nil {
				return Image{}, fmt.Errorf("layer %d channel %d: %w", index, channel.ID, err)
			}
			// The real user mask (-3) wins when both masks exist.
			if maskPlane == nil || channel.ID == -3 {
				maskPlane = data
			}
			continue
		}
		data, err := p.decodeChannel(channel.Payload, layer.Width, layer.Height)
		if err != nil {
			return Image{}, fmt.Errorf("layer %d channel %d: %w", index, channel.ID, err)
		}
		planes = append(planes, psdLayerPlane{id: channel.ID, data: data})
	}
	image, err := p.mergeLayerPlanes(layer, planes, mode)
	if err != nil {
		return Image{}, err
	}
	if mode == PSDImageMasked && maskPlane != nil && layerHasAlphaChannel(layer) {
		mergeMaskIntoAlpha(image.RGBA, layer, layer.Mask, maskPlane)
	}
	return image, nil
}

func layerHasAlphaChannel(layer *PSDLayer) bool {
	for i := range layer.Channels {
		if layer.Channels[i].ID == -1 {
			return true
		}
	}
	return false
}

type psdLayerPlane struct {
	id   int16
	data []byte
}

func (p *PSD) mergeLayerPlanes(layer *PSDLayer, planes []psdLayerPlane, mode PSDImageMode) (Image, error) {
	width, height := int(layer.Width), int(layer.Height)
	out := make([]byte, width*height*4)
	find := func(id int16) []byte {
		for i := len(planes) - 1; i >= 0; i-- {
			if planes[i].id == id {
				return planes[i].data
			}
		}
		return nil
	}
	alpha := find(-1)
	switch p.Header.Mode {
	case PSDColorModeBitmap:
		var bits []byte
		if len(planes) > 0 {
			bits = planes[0].data
		}
		if bits == nil {
			return Image{}, psdUnsupported("bitmap layer without channel")
		}
		unpackBitmapPlane(out, bits, width, height)
	case PSDColorModeGrayscale:
		gray := find(0)
		if gray == nil {
			return Image{}, psdUnsupported("grayscale layer without gray channel")
		}
		mergeGrayPlane(out, gray, alpha, width, height)
	case PSDColorModeIndexed:
		index := find(0)
		if index == nil {
			return Image{}, psdUnsupported("indexed layer without channel")
		}
		if !p.colorTableSet {
			return Image{}, psdUnsupported("indexed layer without color table")
		}
		mergeIndexedPlane(out, index, &p.ColorTable, width, height)
	case PSDColorModeRGB:
		red, green, blue := find(0), find(1), find(2)
		if red == nil || green == nil || blue == nil {
			return Image{}, psdUnsupported("RGB layer channel layout %v", planeIDs(planes))
		}
		mergeRGBPlanes(out, red, green, blue, alpha, width, height)
	case PSDColorModeCMYK:
		c, m, y, k := find(0), find(1), find(2), find(3)
		if c == nil || m == nil || y == nil || k == nil {
			return Image{}, psdUnsupported("CMYK layer channel layout %v", planeIDs(planes))
		}
		mergeCMYKPlanes(out, c, m, y, k, alpha, width, height)
	case PSDColorModeMultichannel, PSDColorModeDuotone, PSDColorModeLAB:
		return Image{}, psdUnsupported("color mode %d", p.Header.Mode)
	default:
		return Image{}, psdUnsupported("color mode %d", p.Header.Mode)
	}
	return Image{Width: uint32(width), Height: uint32(height), RGBA: out}, nil
}

func planeIDs(planes []psdLayerPlane) []int16 {
	ids := make([]int16, len(planes))
	for i := range planes {
		ids[i] = planes[i].id
	}
	return ids
}

func (p *PSD) layerMaskImage(layer *PSDLayer) (Image, error) {
	mask := layer.Mask
	if mask.Width <= 0 || mask.Height <= 0 {
		// psdclass.cpp synthesizes a 1x1 gray pixel from defaultColor when a
		// layer has no mask.
		gray := mask.DefaultColor
		return Image{Width: 1, Height: 1, RGBA: []byte{gray, gray, gray, 0xff}}, nil
	}
	var data []byte
	for i := range layer.Channels {
		channel := &layer.Channels[i]
		if !channel.IsMask() {
			continue
		}
		decoded, err := p.decodeChannel(channel.Payload, mask.Width, mask.Height)
		if err != nil {
			return Image{}, fmt.Errorf("layer mask channel %d: %w", channel.ID, err)
		}
		if data == nil || channel.ID == -3 {
			data = decoded
		}
	}
	if data == nil {
		return Image{}, ErrPSDNoMask
	}
	width, height := int(mask.Width), int(mask.Height)
	out := make([]byte, width*height*4)
	for i := range width * height {
		gray := data[i]
		out[i*4+0], out[i*4+1], out[i*4+2], out[i*4+3] = gray, gray, gray, 0xff
	}
	return Image{Width: uint32(width), Height: uint32(height), RGBA: out}, nil
}

func mergeRGBPlanes(out, red, green, blue, alpha []byte, width, height int) {
	for i := range width * height {
		out[i*4+0] = red[i]
		out[i*4+1] = green[i]
		out[i*4+2] = blue[i]
		if alpha != nil {
			out[i*4+3] = alpha[i]
		} else {
			out[i*4+3] = 0xff
		}
	}
}

func mergeGrayPlane(out, gray, alpha []byte, width, height int) {
	for i := range width * height {
		g := gray[i]
		out[i*4+0], out[i*4+1], out[i*4+2] = g, g, g
		if alpha != nil {
			out[i*4+3] = alpha[i]
		} else {
			out[i*4+3] = 0xff
		}
	}
}

func mergeCMYKPlanes(out, c, m, y, k, alpha []byte, width, height int) {
	for i := range width * height {
		// PSD stores 0 as no ink and 255 as full ink.
		inkK := 255 - int(k[i])
		out[i*4+0] = uint8((255 - int(c[i])) * inkK / 255)
		out[i*4+1] = uint8((255 - int(m[i])) * inkK / 255)
		out[i*4+2] = uint8((255 - int(y[i])) * inkK / 255)
		if alpha != nil {
			out[i*4+3] = alpha[i]
		} else {
			out[i*4+3] = 0xff
		}
	}
}

func mergeIndexedPlane(out, index []byte, table *[256][4]byte, width, height int) {
	for i := range width * height {
		color := table[index[i]]
		out[i*4+0], out[i*4+1], out[i*4+2], out[i*4+3] = color[0], color[1], color[2], color[3]
	}
}

func unpackBitmapPlane(out, bits []byte, width, height int) {
	stride := (width + 7) / 8
	for y := range height {
		row := bits[y*stride : (y+1)*stride]
		for x := 0; x < width; x++ {
			bit := row[x/8] >> (7 - uint(x%8)) & 1
			// Photoshop bitmap data stores 0 as white and 1 as black.
			value := uint8(0xff)
			if bit != 0 {
				value = 0
			}
			at := (y*width + x) * 4
			out[at+0], out[at+1], out[at+2], out[at+3] = value, value, value, 0xff
		}
	}
}

// mergeMaskIntoAlpha applies the layer mask to the alpha plane. It mirrors
// mergeMaskToAlpha in psdimage.cpp: overlapping pixels multiply, and alpha
// outside the mask becomes transparent when the mask default color is 0.
func mergeMaskIntoAlpha(out []byte, layer *PSDLayer, mask PSDLayerMask, maskPlane []byte) {
	if mask.Width <= 0 || mask.Height <= 0 {
		return
	}
	width, height := int(layer.Width), int(layer.Height)
	for y := range height {
		globalY := int(layer.Top) + y
		for x := 0; x < width; x++ {
			globalX := int(layer.Left) + x
			at := (y*width + x) * 4
			mx := globalX - int(mask.Left)
			my := globalY - int(mask.Top)
			if mx < 0 || my < 0 || mx >= int(mask.Width) || my >= int(mask.Height) {
				if mask.DefaultColor == 0 {
					out[at+3] = 0
				}
				continue
			}
			maskValue := maskPlane[my*int(mask.Width)+mx]
			out[at+3] = uint8(int(out[at+3]) * int(maskValue) / 255)
		}
	}
}
