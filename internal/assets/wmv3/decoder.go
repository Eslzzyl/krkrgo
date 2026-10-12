// Decoder and frame API. Frame payload handling (end-of-sequence
// trimming) translated from libavcodec/vc1dec.c vc1_decode_frame,
// LGPL-2.1-or-later; see doc.go for the full license notice.
package wmv3

import (
	"errors"
	"fmt"
)

// ErrPictureUnavailable reports that a frame header parsed
// successfully but the picture (macroblock) layer of this package is
// not implemented yet: coefficients, transforms, motion compensation,
// and overlap filtering still live in FFmpeg's vc1_block.c /
// vc1_pred.c / vc1_mc.c / vc1dsp.c. The returned error is a permanent
// capability statement for this package version, not a corrupt-stream
// condition; callers should surface it as "WMV3 video not decodable"
// rather than as damaged input.
var ErrPictureUnavailable = errors.New("WMV3 picture decode unavailable")

// vc1CodeEndOfSequence is the VC-1 advanced-profile end-of-sequence
// start code (VC1_CODE_ENDOFSEQ). vc1_decode_frame strips it from the
// end of any packet before parsing, so this package does the same;
// WMV3 payloads in ASF normally never carry it.
var vc1CodeEndOfSequence = [4]byte{0x00, 0x00, 0x01, 0x0A}

// Frame is one decoded access unit. Header is always populated by a
// successful Decode; the YUV picture planes will be added to this
// struct by the macroblock stage, which is why Decode currently
// reports ErrPictureUnavailable for supported pictures instead of
// fabricating output.
type Frame struct {
	Header FrameHeader
}

// Decoder holds the WMV3 sequence configuration plus the state that
// persists across frames: the rounding-control bit and the
// intensity-compensation lookup-table generations. It is not safe for
// concurrent use. One Decoder decodes one stream; create a new
// Decoder for each open, seek, or restart so state cannot leak across
// discontinuities.
type Decoder struct {
	cfg      Config
	width    int
	height   int
	mbWidth  int
	mbHeight int
	rnd      bool
	ic       icState
}

// NewDecoder builds a decoder from WMV3 codec extradata (the
// Simple/Main sequence header, as carried in ASF video stream
// properties) and the coded frame size from the container. The same
// width/height must be used for every frame of the stream.
//
// Sprite (WMV3IMAGE) extradata is rejected with an error wrapping
// ErrUnsupportedFeature; Complex and Advanced profile extradata is
// rejected by ParseExtradata with ErrUnsupportedProfile. Dimensions
// must be positive and within [1, maxFrameDimension]; the macroblock
// grid is also capped by the bitplane cell limit.
func NewDecoder(extradata []byte, width, height int) (*Decoder, error) {
	cfg, err := ParseExtradata(extradata)
	if err != nil {
		return nil, err
	}
	if cfg.Sprite {
		return nil, fmt.Errorf("sprite extradata: %w", ErrUnsupportedFeature)
	}
	if width < 1 || height < 1 || width > maxFrameDimension || height > maxFrameDimension {
		return nil, fmt.Errorf("frame size %dx%d: %w", width, height, ErrInvalidValue)
	}
	mbWidth := (width + 15) / 16
	mbHeight := (height + 15) / 16
	if int64(mbWidth)*int64(mbHeight) > maxBitplaneCells {
		return nil, fmt.Errorf("macroblock grid %dx%d: %w", mbWidth, mbHeight, ErrInvalidValue)
	}
	return &Decoder{
		cfg:      cfg,
		width:    width,
		height:   height,
		mbWidth:  mbWidth,
		mbHeight: mbHeight,
	}, nil
}

// maxFrameDimension caps one frame side: 8192 pixels, i.e. the same
// 512-macroblock grid limit the bitplane decoder enforces.
const maxFrameDimension = 8192

// Config returns the parsed sequence header.
func (d *Decoder) Config() Config { return d.cfg }

// Size returns the coded frame width and height in pixels.
func (d *Decoder) Size() (width, height int) { return d.width, d.height }

// MacroblockGrid returns the macroblock counts per row and column.
func (d *Decoder) MacroblockGrid() (mbWidth, mbHeight int) {
	return d.mbWidth, d.mbHeight
}

// ParseFrameHeader parses one frame header from data and advances the
// decoder's cross-frame state (rounding bit, intensity-compensation
// tables) exactly once. Callers pass each access unit to either
// ParseFrameHeader or Decode, never both.
//
// data is one frame payload as reassembled by the container: for WMV3
// in ASF there are no start codes and no emulation-prevention bytes,
// so the bits are consumed verbatim (vc1dec.c initializes the bit
// reader directly for WMV3). A trailing end-of-sequence marker is
// trimmed first, matching vc1_decode_frame.
//
// Errors wrap ErrTruncated, ErrInvalidValue, or
// ErrUnsupportedFeature. On error the decoder's cross-frame state may
// already have advanced, matching FFmpeg; treat header errors as
// fatal for the stream.
func (d *Decoder) ParseFrameHeader(data []byte) (FrameHeader, error) {
	return d.parseFrameHeader(NewBitReader(trimEndOfSequence(data)))
}

// Decode parses one frame header and then decodes the picture.
//
// Current behavior: B and BI pictures are rejected with an error
// wrapping ErrUnsupportedFeature, and I and P pictures are rejected
// with an error wrapping ErrPictureUnavailable once their headers
// parse cleanly. Bitstream errors wrap ErrTruncated or
// ErrInvalidValue. The macroblock stage will replace the
// ErrPictureUnavailable path with YUV picture output without changing
// the Frame or FrameHeader shapes.
func (d *Decoder) Decode(data []byte) (Frame, error) {
	h, err := d.ParseFrameHeader(data)
	if err != nil {
		return Frame{}, err
	}
	if h.Picture == PictureB {
		if h.BiType {
			return Frame{Header: h}, fmt.Errorf("BI picture: %w", ErrUnsupportedFeature)
		}
		return Frame{Header: h}, fmt.Errorf("B picture: %w", ErrUnsupportedFeature)
	}
	if h.X8Type {
		return Frame{Header: h}, fmt.Errorf("X8 transform frame: %w", ErrUnsupportedFeature)
	}
	return Frame{Header: h}, fmt.Errorf("%s picture header parsed: %w", h.Picture, ErrPictureUnavailable)
}

// trimEndOfSequence drops one trailing VC1_CODE_ENDOFSEQ marker, the
// first step of vc1_decode_frame.
func trimEndOfSequence(data []byte) []byte {
	if n := len(data); n >= 4 &&
		data[n-4] == vc1CodeEndOfSequence[0] &&
		data[n-3] == vc1CodeEndOfSequence[1] &&
		data[n-2] == vc1CodeEndOfSequence[2] &&
		data[n-1] == vc1CodeEndOfSequence[3] {
		return data[:n-4]
	}
	return data
}

// icState mirrors the intensity-compensation LUT generations in
// VC1Context: three rotating sets (last, next, aux) of luma and
// chroma tables plus their use flags, with curr aliasing one of them.
// rotate_luts and the P-frame INIT_LUT calls in ff_vc1_parse_frame_header
// maintain the aliases.
type icState struct {
	lastY, nextY, auxY       [2][256]uint8
	lastUV, nextUV, auxUV    [2][256]uint8
	lastUse, nextUse, auxUse bool

	currY   *[2][256]uint8
	currUV  *[2][256]uint8
	currUse *bool
}

// rotate performs one rotate_luts pass for the given picture type.
// B/BI pictures select the aux generation; I/P pictures swap
// last and next and alias curr to next. The aliased generation is
// reset to the identity LUT and its use flag cleared.
func (s *icState) rotate(pict PictureType) {
	if pict == PictureB {
		s.currY = &s.auxY
		s.currUV = &s.auxUV
		s.currUse = &s.auxUse
	} else {
		s.lastY, s.nextY = s.nextY, s.lastY
		s.lastUV, s.nextUV = s.nextUV, s.lastUV
		s.lastUse, s.nextUse = s.nextUse, s.lastUse
		s.currY = &s.nextY
		s.currUV = &s.nextUV
		s.currUse = &s.nextUse
	}
	// INIT_LUT(32, 0, ...) with chain unset writes the identity map.
	for row := range 2 {
		s.currY[row], s.currUV[row] = IntensityLUT(32, 0, &s.currY[row], &s.currUV[row], false)
	}
	*s.currUse = false
}

// setIC fills the last-generation tables for intensity compensation,
// exactly as the P-frame branch of ff_vc1_parse_frame_header does
// (INIT_LUT chained over the current last tables) and marks them used.
func (s *icState) setIC(lumscale, lumshift uint8) {
	s.lastUse = true
	for row := range 2 {
		s.lastY[row], s.lastUV[row] = IntensityLUT(lumscale, lumshift, &s.lastY[row], &s.lastUV[row], true)
	}
}
