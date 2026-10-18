package assets

// PSD parsing follows reference/krkr2-main/cpp/plugins/psdfile/psdparse:
// psdparse.cpp parses the header, image resources, layer/mask section and
// layer records; psdlayer.cpp resolves the section divider, unicode name,
// layer id and fill opacity additional blocks; psdimage.cpp decodes channel
// planes. The parser keeps the input bytes so channel payloads can be decoded
// lazily, as the reference keeps iterator ranges into the mapped file.

import (
	"errors"
	"fmt"
	"unicode/utf16"
)

var (
	// ErrPSD identifies malformed or truncated PSD input.
	ErrPSD = errors.New("invalid PSD")
	// ErrPSDUnsupported identifies recognized PSD data this port does not
	// decode yet (multichannel, duotone and Lab modes, 16/32-bit channels,
	// slices, guides and layer comps).
	ErrPSDUnsupported = errors.New("unsupported PSD feature")
	// ErrPSDNoComposite reports a document whose merged image data is absent.
	ErrPSDNoComposite = errors.New("PSD has no composite image")
	// ErrPSDEmptyLayer reports a zero-sized layer, which cannot carry pixels.
	ErrPSDEmptyLayer = errors.New("PSD layer has no pixels")
	// ErrPSDNoMask reports a layer without a user layer mask.
	ErrPSDNoMask = errors.New("PSD layer has no mask")
)

// PSDColorMode is the header color mode (psd::ColorMode).
type PSDColorMode int

const (
	PSDColorModeBitmap       PSDColorMode = 0
	PSDColorModeGrayscale    PSDColorMode = 1
	PSDColorModeIndexed      PSDColorMode = 2
	PSDColorModeRGB          PSDColorMode = 3
	PSDColorModeCMYK         PSDColorMode = 4
	PSDColorModeMultichannel PSDColorMode = 7
	PSDColorModeDuotone      PSDColorMode = 8
	PSDColorModeLAB          PSDColorMode = 9
)

// PSDLayerType mirrors psd::LayerType. The values are part of the TJS PSD
// class contract.
type PSDLayerType int

const (
	PSDLayerNormal PSDLayerType = iota
	PSDLayerHidden
	PSDLayerFolder
	PSDLayerAdjust
	PSDLayerFill
	PSDLayerText
)

// PSDBlendMode mirrors psd::BlendMode, including the non-libpsd tail values.
type PSDBlendMode int

const (
	PSDBlendInvalid      PSDBlendMode = -1
	PSDBlendNormal       PSDBlendMode = 0
	PSDBlendDissolve     PSDBlendMode = 1
	PSDBlendDarken       PSDBlendMode = 2
	PSDBlendMultiply     PSDBlendMode = 3
	PSDBlendColorBurn    PSDBlendMode = 4
	PSDBlendLinearBurn   PSDBlendMode = 5
	PSDBlendLighten      PSDBlendMode = 6
	PSDBlendScreen       PSDBlendMode = 7
	PSDBlendColorDodge   PSDBlendMode = 8
	PSDBlendLinearDodge  PSDBlendMode = 9
	PSDBlendOverlay      PSDBlendMode = 10
	PSDBlendSoftLight    PSDBlendMode = 11
	PSDBlendHardLight    PSDBlendMode = 12
	PSDBlendVividLight   PSDBlendMode = 13
	PSDBlendLinearLight  PSDBlendMode = 14
	PSDBlendPinLight     PSDBlendMode = 15
	PSDBlendHardMix      PSDBlendMode = 16
	PSDBlendDifference   PSDBlendMode = 17
	PSDBlendExclusion    PSDBlendMode = 18
	PSDBlendHue          PSDBlendMode = 19
	PSDBlendSaturation   PSDBlendMode = 20
	PSDBlendColor        PSDBlendMode = 21
	PSDBlendLuminosity   PSDBlendMode = 22
	PSDBlendPassThrough  PSDBlendMode = 23
	PSDBlendDarkerColor  PSDBlendMode = 24
	PSDBlendLighterColor PSDBlendMode = 25
	PSDBlendSubtract     PSDBlendMode = 26
	PSDBlendDivide       PSDBlendMode = 27
)

// Layer type constants in reference are the enum above plus pass-through in
// the C++ source; the TJS class exposes the same integer values.

var psdBlendModes = map[string]PSDBlendMode{
	"norm": PSDBlendNormal,
	"diss": PSDBlendDissolve,
	"dark": PSDBlendDarken,
	"mul ": PSDBlendMultiply,
	"idiv": PSDBlendColorBurn,
	"lbrn": PSDBlendLinearBurn,
	"dkCl": PSDBlendDarkerColor,
	"lite": PSDBlendLighten,
	"scrn": PSDBlendScreen,
	"div ": PSDBlendColorDodge,
	"lddg": PSDBlendLinearDodge,
	"ltCl": PSDBlendLighterColor,
	"over": PSDBlendOverlay,
	"sLit": PSDBlendSoftLight,
	"hLit": PSDBlendHardLight,
	"vLit": PSDBlendVividLight,
	"lLit": PSDBlendLinearLight,
	"pLit": PSDBlendPinLight,
	"hMix": PSDBlendHardMix,
	"diff": PSDBlendDifference,
	"smud": PSDBlendExclusion,
	"fsub": PSDBlendSubtract,
	"fdiv": PSDBlendDivide,
	"hue ": PSDBlendHue,
	"sat ": PSDBlendSaturation,
	"colr": PSDBlendColor,
	"lum ": PSDBlendLuminosity,
	"pass": PSDBlendPassThrough,
}

// PSDHeader is the fixed 26-byte header.
type PSDHeader struct {
	Version  uint16
	Channels int
	Height   int32
	Width    int32
	Depth    int
	Mode     PSDColorMode
}

// PSDChannel is one layer channel record. Payload holds the channel bytes
// starting at the two-byte compression id; Length is the declared size.
type PSDChannel struct {
	ID      int16
	Length  uint32
	Payload []byte
}

// IsMask reports the user and real user mask channel ids.
func (c PSDChannel) IsMask() bool { return c.ID == -2 || c.ID == -3 }

// PSDLayerMask is the parsed layer mask rectangle.
type PSDLayerMask struct {
	Top, Left, Bottom, Right int32
	Width, Height            int32
	DefaultColor             uint8
	Flags                    uint8
	RealFlags                uint8
}

// PSDLayer is one decoded layer record. ParentIndex is -1 for a top-level
// layer; otherwise it indexes the enclosing folder layer.
type PSDLayer struct {
	Top, Left, Bottom, Right int32
	Width, Height            int32
	Channels                 []PSDChannel
	BlendModeKey             string
	BlendMode                PSDBlendMode
	Opacity                  uint8
	FillOpacity              uint8
	Clipping                 uint8
	Flags                    uint8
	Name                     string
	LayerID                  int32
	Type                     PSDLayerType
	ParentIndex              int
	Mask                     PSDLayerMask
}

// Visible reports whether the per-layer visibility flag is clear.
func (l *PSDLayer) Visible() bool               { return l.Flags&0x02 == 0 }
func (l *PSDLayer) TransparencyProtected() bool { return l.Flags&0x01 != 0 }
func (l *PSDLayer) Obsolete() bool              { return l.Flags&0x04 != 0 }
func (l *PSDLayer) PixelDataIrrelevant() bool   { return l.Flags&0x10 != 0 }

// PSD is a parsed Photoshop document. Input bytes are retained for lazy
// channel decoding.
type PSD struct {
	Header        PSDHeader
	Layers        []PSDLayer
	HasComposite  bool
	MergedAlpha   bool
	HasSlices     bool
	HasGuides     bool
	HasLayerComps bool
	ColorTable    [256][4]byte

	data          []byte
	composite     []byte
	colorTableSet bool
}

const (
	psdMaxPixels            = int64(64 * 1024 * 1024)
	psdMaxDimension         = int64(30000)
	psdResourceSlices       = 1050
	psdResourceGuides       = 1032
	psdResourceTransparency = 1047
	psdResourceLayerComps   = 1065
)

type psdReader struct {
	data []byte
	pos  int
}

func (r *psdReader) rest() int { return len(r.data) - r.pos }

func (r *psdReader) need(n int) error {
	if n < 0 || r.pos < 0 || r.pos > len(r.data) || n > len(r.data)-r.pos {
		return psdMalformed("truncated input at %#x: need %d bytes", r.pos, n)
	}
	return nil
}

func (r *psdReader) bytes(n int) ([]byte, error) {
	if err := r.need(n); err != nil {
		return nil, err
	}
	b := r.data[r.pos : r.pos+n]
	r.pos += n
	return b, nil
}

func (r *psdReader) skip(n int) error {
	if err := r.need(n); err != nil {
		return err
	}
	r.pos += n
	return nil
}

func (r *psdReader) u8() (uint8, error) {
	b, err := r.bytes(1)
	if err != nil {
		return 0, err
	}
	return b[0], nil
}

func (r *psdReader) u16() (uint16, error) {
	b, err := r.bytes(2)
	if err != nil {
		return 0, err
	}
	return uint16(b[0])<<8 | uint16(b[1]), nil
}

func (r *psdReader) i16() (int16, error) {
	v, err := r.u16()
	return int16(v), err
}

func (r *psdReader) u32() (uint32, error) {
	b, err := r.bytes(4)
	if err != nil {
		return 0, err
	}
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3]), nil
}

func (r *psdReader) i32() (int32, error) {
	v, err := r.u32()
	return int32(v), err
}

func psdMalformed(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrPSD, fmt.Sprintf(format, args...))
}

func psdUnsupported(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrPSDUnsupported, fmt.Sprintf(format, args...))
}

// ParsePSD decodes the PSD header, color mode table, image-resource flags,
// layer records and channel locations. Pixel decoding is deferred to
// Composite and LayerImage.
func ParsePSD(data []byte) (*PSD, error) {
	r := &psdReader{data: data}
	magic, err := r.bytes(4)
	if err != nil || string(magic) != "8BPS" {
		return nil, psdMalformed("missing 8BPS signature")
	}
	version, err := r.u16()
	if err != nil {
		return nil, err
	}
	if version != 1 {
		return nil, psdMalformed("unsupported version %d", version)
	}
	if err := r.skip(6); err != nil {
		return nil, err
	}
	channels, err := r.u16()
	if err != nil {
		return nil, err
	}
	if channels == 0 || channels > 56 {
		return nil, psdMalformed("channel count %d outside 1..56", channels)
	}
	height, err := r.i32()
	if err != nil {
		return nil, err
	}
	width, err := r.i32()
	if err != nil {
		return nil, err
	}
	if width <= 0 || height <= 0 || int64(width) > psdMaxDimension || int64(height) > psdMaxDimension {
		return nil, psdMalformed("invalid dimensions %dx%d", width, height)
	}
	if int64(width)*int64(height) > psdMaxPixels {
		return nil, psdMalformed("dimensions %dx%d exceed pixel limit", width, height)
	}
	depth, err := r.u16()
	if err != nil {
		return nil, err
	}
	switch depth {
	case 1, 8, 16, 32:
	default:
		return nil, psdMalformed("unsupported depth %d", depth)
	}
	mode, err := r.u16()
	if err != nil {
		return nil, err
	}
	switch PSDColorMode(mode) {
	case PSDColorModeBitmap, PSDColorModeGrayscale, PSDColorModeIndexed, PSDColorModeRGB,
		PSDColorModeCMYK, PSDColorModeMultichannel, PSDColorModeDuotone, PSDColorModeLAB:
	default:
		return nil, psdMalformed("unknown color mode %d", mode)
	}
	if PSDColorMode(mode) == PSDColorModeBitmap && depth != 1 {
		return nil, psdMalformed("bitmap mode requires depth 1, got %d", depth)
	}
	if (PSDColorMode(mode) == PSDColorModeIndexed || PSDColorMode(mode) == PSDColorModeGrayscale) && depth == 32 {
		return nil, psdMalformed("color mode %d does not support depth 32", mode)
	}

	doc := &PSD{
		Header: PSDHeader{
			Version:  version,
			Channels: int(channels),
			Height:   height,
			Width:    width,
			Depth:    int(depth),
			Mode:     PSDColorMode(mode),
		},
		data: data,
	}

	colorModeLength, err := r.u32()
	if err != nil {
		return nil, err
	}
	colorModeData, err := r.bytes(int(colorModeLength))
	if err != nil {
		return nil, err
	}
	if doc.Header.Mode == PSDColorModeIndexed {
		if len(colorModeData) != 768 {
			return nil, psdMalformed("indexed color table is %d bytes, want 768", len(colorModeData))
		}
		for i := range 256 {
			doc.ColorTable[i] = [4]byte{colorModeData[i], colorModeData[256+i], colorModeData[512+i], 0xff}
		}
		doc.colorTableSet = true
	}

	resourceLength, err := r.u32()
	if err != nil {
		return nil, err
	}
	resourceData, err := r.bytes(int(resourceLength))
	if err != nil {
		return nil, err
	}
	if err := doc.parseImageResources(resourceData); err != nil {
		return nil, err
	}

	layerMaskLength, err := r.u32()
	if err != nil {
		return nil, err
	}
	layerMaskData, err := r.bytes(int(layerMaskLength))
	if err != nil {
		return nil, err
	}
	if len(layerMaskData) > 0 {
		if err := doc.parseLayerAndMask(layerMaskData); err != nil {
			return nil, err
		}
	}

	composite, err := r.bytes(r.rest())
	if err != nil {
		return nil, err
	}
	if len(composite) > 0 {
		doc.HasComposite = true
		doc.composite = composite
	}
	return doc, nil
}

func (p *PSD) parseImageResources(data []byte) error {
	r := &psdReader{data: data}
	for r.rest() > 0 {
		signature, err := r.bytes(4)
		if err != nil {
			return err
		}
		if string(signature) != "8BIM" && string(signature) != "8B64" {
			return psdMalformed("image resource signature %q", signature)
		}
		id, err := r.u16()
		if err != nil {
			return err
		}
		nameLength, err := r.u8()
		if err != nil {
			return err
		}
		if _, err := r.bytes(int(nameLength)); err != nil {
			return err
		}
		if (int(nameLength)+1)%2 != 0 {
			if err := r.skip(1); err != nil {
				return err
			}
		}
		size, err := r.u32()
		if err != nil {
			return err
		}
		if uint64(size) > uint64(r.rest()) {
			return psdMalformed("image resource %d size %d exceeds section", id, size)
		}
		payload, err := r.bytes(int(size))
		if err != nil {
			return err
		}
		if size%2 != 0 {
			if err := r.skip(1); err != nil {
				return err
			}
		}
		switch int(id) {
		case psdResourceSlices:
			p.HasSlices = true
		case psdResourceGuides:
			p.HasGuides = true
		case psdResourceLayerComps:
			p.HasLayerComps = true
		case psdResourceTransparency:
			if len(payload) >= 2 && p.colorTableSet {
				index := int(payload[0])<<8 | int(payload[1])
				if index >= 0 && index < 256 {
					p.ColorTable[index][3] = 0
				}
			}
		}
	}
	return nil
}

func (p *PSD) parseLayerAndMask(data []byte) error {
	r := &psdReader{data: data}
	layerInfoLength, err := r.u32()
	if err != nil {
		return err
	}
	if uint64(layerInfoLength) > uint64(r.rest()) {
		return psdMalformed("layer info size %d exceeds section", layerInfoLength)
	}
	layerInfo, err := r.bytes(int(layerInfoLength))
	if err != nil {
		return err
	}
	if len(layerInfo) > 0 {
		if err := p.parseLayerInfo(layerInfo); err != nil {
			return err
		}
	}
	// The remaining section holds the global layer mask and optional
	// large-document layer info. Neither changes the layer records.
	return nil
}

func (p *PSD) parseLayerInfo(info []byte) error {
	r := &psdReader{data: info}
	countValue, err := r.i16()
	if err != nil {
		return err
	}
	count := int(countValue)
	p.MergedAlpha = count < 0
	if count < 0 {
		count = -count
	}
	if count > 10000 {
		return psdMalformed("layer count %d exceeds limit", count)
	}
	layers := make([]PSDLayer, count)
	for i := range count {
		if err := parseLayerRecord(&layers[i], r); err != nil {
			return fmt.Errorf("layer %d: %w", i, err)
		}
	}
	// Layer channel data follows the records without an alignment gap; the
	// reference parser records the rest of the section as channelImageData.
	for i := range layers {
		for j := range layers[i].Channels {
			channel := &layers[i].Channels[j]
			if uint64(channel.Length) > uint64(r.rest()) {
				return psdMalformed("layer %d channel %d length %d exceeds available %d", i, j, channel.Length, r.rest())
			}
			payload, err := r.bytes(int(channel.Length))
			if err != nil {
				return err
			}
			channel.Payload = payload
		}
	}
	p.linkLayerParents(layers)
	p.Layers = layers
	return nil
}

func (p *PSD) linkLayerParents(layers []PSDLayer) {
	stack := []int{-1}
	for i := len(layers) - 1; i >= 0; i-- {
		layers[i].ParentIndex = stack[len(stack)-1]
		switch layers[i].Type {
		case PSDLayerFolder:
			stack = append(stack, i)
		case PSDLayerHidden:
			if len(stack) > 1 {
				stack = stack[:len(stack)-1]
			}
		}
	}
}

func parseLayerRecord(layer *PSDLayer, r *psdReader) error {
	top, err := r.i32()
	if err != nil {
		return err
	}
	left, err := r.i32()
	if err != nil {
		return err
	}
	bottom, err := r.i32()
	if err != nil {
		return err
	}
	right, err := r.i32()
	if err != nil {
		return err
	}
	if int64(right)-int64(left) < -psdMaxDimension || int64(right)-int64(left) > psdMaxDimension ||
		int64(bottom)-int64(top) < -psdMaxDimension || int64(bottom)-int64(top) > psdMaxDimension {
		return psdMalformed("layer rectangle exceeds limit")
	}
	width := right - left
	height := bottom - top
	if width > 0 && height > 0 && int64(width)*int64(height) > psdMaxPixels {
		return psdMalformed("layer rectangle %dx%d exceeds pixel limit", width, height)
	}
	channelCount, err := r.u16()
	if err != nil {
		return err
	}
	if channelCount > 56 {
		return psdMalformed("channel count %d exceeds limit", channelCount)
	}
	channels := make([]PSDChannel, channelCount)
	for i := range channels {
		id, err := r.i16()
		if err != nil {
			return err
		}
		length, err := r.u32()
		if err != nil {
			return err
		}
		channels[i] = PSDChannel{ID: id, Length: length}
	}
	signature, err := r.bytes(4)
	if err != nil {
		return err
	}
	if string(signature) != "8BIM" {
		return psdMalformed("layer blend signature %q", signature)
	}
	blendKey, err := r.bytes(4)
	if err != nil {
		return err
	}
	opacity, err := r.u8()
	if err != nil {
		return err
	}
	clipping, err := r.u8()
	if err != nil {
		return err
	}
	flags, err := r.u8()
	if err != nil {
		return err
	}
	if err := r.skip(1); err != nil {
		return err
	}
	extraLength, err := r.u32()
	if err != nil {
		return err
	}
	extra, err := r.bytes(int(extraLength))
	if err != nil {
		return err
	}

	*layer = PSDLayer{
		Top:          top,
		Left:         left,
		Bottom:       bottom,
		Right:        right,
		Width:        width,
		Height:       height,
		Channels:     channels,
		BlendModeKey: string(blendKey),
		BlendMode:    psdBlendModes[string(blendKey)],
		Opacity:      opacity,
		FillOpacity:  255,
		Clipping:     clipping,
		Flags:        flags,
		LayerID:      -1,
		Type:         PSDLayerNormal,
		ParentIndex:  -1,
	}
	return parseLayerExtra(layer, extra)
}

func parseLayerExtra(layer *PSDLayer, data []byte) error {
	r := &psdReader{data: data}
	maskLength, err := r.u32()
	if err != nil {
		return err
	}
	if uint64(maskLength) > uint64(r.rest()) {
		return psdMalformed("layer mask size %d exceeds extra data", maskLength)
	}
	maskData, err := r.bytes(int(maskLength))
	if err != nil {
		return err
	}
	if len(maskData) >= 18 {
		m := &psdReader{data: maskData}
		top, _ := m.i32()
		left, _ := m.i32()
		bottom, _ := m.i32()
		right, _ := m.i32()
		defaultColor, _ := m.u8()
		flags, _ := m.u8()
		layer.Mask.Top, layer.Mask.Left, layer.Mask.Bottom, layer.Mask.Right = top, left, bottom, right
		layer.Mask.Width, layer.Mask.Height = right-left, bottom-top
		if layer.Mask.Width < 0 {
			layer.Mask.Width = 0
		}
		if layer.Mask.Height < 0 {
			layer.Mask.Height = 0
		}
		layer.Mask.DefaultColor, layer.Mask.Flags = defaultColor, flags
		if len(maskData) >= 20 {
			layer.Mask.RealFlags = maskData[18]
		}
	}

	blendLength, err := r.u32()
	if err != nil {
		return err
	}
	if err := r.skip(int(blendLength)); err != nil {
		return err
	}

	nameLength, err := r.u8()
	if err != nil {
		return err
	}
	name, err := r.bytes(int(nameLength))
	if err != nil {
		return err
	}
	layer.Name = string(name)
	padding := (4 - (int(nameLength)+1)&3) & 3
	if err := r.skip(padding); err != nil {
		return err
	}

	for r.rest() > 0 {
		signature, err := r.bytes(4)
		if err != nil {
			return err
		}
		if string(signature) != "8BIM" && string(signature) != "8B64" {
			return psdMalformed("additional layer signature %q", signature)
		}
		keyBytes, err := r.bytes(4)
		if err != nil {
			return err
		}
		size, err := r.u32()
		if err != nil {
			return err
		}
		if uint64(size) > uint64(r.rest()) {
			return psdMalformed("additional layer %q size %d exceeds extra data", keyBytes, size)
		}
		block, err := r.bytes(int(size))
		if err != nil {
			return err
		}
		if err := applyLayerAdditional(layer, string(keyBytes), block); err != nil {
			return err
		}
	}
	return nil
}

var (
	psdAdjustKeys = map[string]bool{
		"grdm": true, "levl": true, "curv": true, "hue ": true, "hue2": true,
		"blnc": true, "nvrt": true, "post": true, "thrs": true, "selc": true,
		"brit": true, "mixr": true, "clrL": true, "phfl": true, "blwh": true,
		"vibA": true, "expA": true,
	}
	psdFillKeys = map[string]bool{"SoCo": true, "GdFl": true, "PtFl": true}
)

func applyLayerAdditional(layer *PSDLayer, key string, block []byte) error {
	switch key {
	case "lsct", "lsdk":
		if len(block) < 4 {
			return psdMalformed("section divider block is %d bytes", len(block))
		}
		kind := int32(uint32(block[0])<<24 | uint32(block[1])<<16 | uint32(block[2])<<8 | uint32(block[3]))
		switch kind {
		case 1, 2:
			layer.Type = PSDLayerFolder
		case 3:
			layer.Type = PSDLayerHidden
		}
	case "luni":
		if len(block) < 4 {
			return psdMalformed("unicode layer name block is %d bytes", len(block))
		}
		count := int(uint32(block[0])<<24 | uint32(block[1])<<16 | uint32(block[2])<<8 | uint32(block[3]))
		if count < 0 || count > (len(block)-4)/2 {
			return psdMalformed("unicode layer name count %d exceeds block", count)
		}
		units := make([]uint16, count)
		for i := range count {
			off := 4 + i*2
			units[i] = uint16(block[off])<<8 | uint16(block[off+1])
		}
		layer.Name = string(utf16.Decode(units))
	case "lyid":
		if len(block) < 4 {
			return psdMalformed("layer id block is %d bytes", len(block))
		}
		layer.LayerID = int32(uint32(block[0])<<24 | uint32(block[1])<<16 | uint32(block[2])<<8 | uint32(block[3]))
	case "iOpa":
		if len(block) >= 1 {
			layer.FillOpacity = block[0]
		}
	default:
		if psdAdjustKeys[key] {
			layer.Type = PSDLayerAdjust
		} else if psdFillKeys[key] {
			layer.Type = PSDLayerFill
		}
	}
	return nil
}
