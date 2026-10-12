// Sequence header logic translated from FFmpeg libavcodec/vc1.c
// ff_vc1_decode_sequence_header, Copyright (c) 2011 Mashiat Sarker
// Shakkhar, (c) 2006-2007 Konstantin Shishkov, LGPL-2.1-or-later; see
// doc.go for the full license notice.
package wmv3

import (
	"errors"
	"fmt"
)

// Profile is a VC-1 coding profile, the 2-bit field that opens the
// sequence header.
type Profile uint8

const (
	ProfileSimple   Profile = 0
	ProfileMain     Profile = 1
	ProfileComplex  Profile = 2 // WMV9-specific; rejected by this package
	ProfileAdvanced Profile = 3 // rejected by this package
)

func (p Profile) String() string {
	switch p {
	case ProfileSimple:
		return "simple"
	case ProfileMain:
		return "main"
	case ProfileComplex:
		return "complex"
	case ProfileAdvanced:
		return "advanced"
	default:
		return fmt.Sprintf("unknown(%d)", uint8(p))
	}
}

// QuantizerMode is the 2-bit QUANTZONEMODE syntax element: how each frame
// specifies its quantizer.
type QuantizerMode uint8

const (
	QuantFrameImplicit QuantizerMode = 0 // implied at frame level
	QuantFrameExplicit QuantizerMode = 1 // explicit at frame level
	QuantNonUniform    QuantizerMode = 2 // non-uniform quantization
	QuantUniform       QuantizerMode = 3 // uniform quantization
)

// Parse errors wrap exactly one of these sentinels; BitReader failures
// additionally surface ErrTruncated through the same wrapping.
var (
	// ErrTruncated reports that the bitstream ended before a required
	// field could be read.
	ErrTruncated = errors.New("truncated WMV3 bitstream")
	// ErrInvalidValue reports a reserved or forbidden bitstream value,
	// including Simple-profile restrictions on FASTUVMC and EXTENDMV.
	ErrInvalidValue = errors.New("invalid WMV3 bitstream value")
	// ErrUnsupportedProfile reports Complex or Advanced profile
	// extradata; this package implements Simple and Main only.
	ErrUnsupportedProfile = errors.New("unsupported WMV3 profile")
	// ErrUnsupportedFeature reports a syntactically valid feature this
	// package cannot honor, such as the optional sprite DC VLC.
	ErrUnsupportedFeature = errors.New("unsupported WMV3 feature")
)

// Config is a decoded WMV3 Simple/Main sequence header: every syntax
// element of the header plus the sprite dimensions it may carry. Frame
// decoding consumes these fields directly; the reserved elements are
// kept so a parsed header can be reproduced or validated byte-for-byte.
// The zero Config is not a valid header; use ParseExtradata.
type Config struct {
	Profile Profile

	// Post-processing and frame tools.
	FrmRTQPostProc uint8 // 3-bit frame-rate-based postproc strength, (fps-2)/4
	BitRTQPostProc uint8 // 5-bit bitrate-based postproc strength, (bitrate-32kbps)/64kbps
	LoopFilter     bool
	MultiRes       bool  // per-frame RESPIC syntax element present
	FastUVMC       bool  // qpel motion vectors round to hpel
	ExtendedMV     bool  // extended motion vectors in P/B frames
	DQuant         uint8 // 2-bit; nonzero enables per-MB quantizer variation
	VSTransform    bool  // variable-size transform enabled
	Overlap        bool  // overlapped transforms enabled
	ResyncMarker   bool  // stream may contain resync markers
	Rangered       bool  // frame-level range reduction present
	MaxBFrames     uint8 // 3-bit maximum consecutive B frames
	QuantizerMode  QuantizerMode
	FInterpFlag    bool // INTERPFRM may be present at frame level

	// Reserved elements, retained for round-trip validation.
	ResX8      bool
	ResFastTX  bool // normally 1; 0 is followed by a reserved 16-bit field
	ResRTMFlag bool // reserved, expected 1

	// Sprite mode (WMV3IMAGE); width and height are nonzero only when
	// Sprite is set.
	Sprite       bool
	SpriteWidth  uint16
	SpriteHeight uint16
}

// ParseExtradata decodes the WMV3 sequence header stored in codec
// extradata, the equivalent of FFmpeg's vc1_decode_init WMV3 path. Extra
// bits or bytes after the header are tolerated: real extradata carries a
// trailing version byte that the reference decoder does not validate, and
// sequence dimensions for non-sprite streams come from the container, not
// the header. data must be the complete extradata; empty or short input
// fails with an error wrapping ErrTruncated.
//
// Complex and Advanced profiles are rejected explicitly with an error
// wrapping ErrUnsupportedProfile. A Simple profile header that enables
// the loop filter is accepted with LoopFilter set, matching FFmpeg,
// which logs the profile violation but continues parsing.
func ParseExtradata(data []byte) (Config, error) {
	return parseSequenceHeader(NewBitReader(data))
}

// parseSequenceHeader decodes one Simple/Main sequence header from r in
// VC-1 bit order (Figure 7-8 of the specification).
func parseSequenceHeader(r *BitReader) (Config, error) {
	var c Config
	var err error

	raw, err := r.Bits(2)
	if err != nil {
		return Config{}, err
	}
	c.Profile = Profile(raw)
	switch c.Profile {
	case ProfileSimple, ProfileMain:
	default:
		return Config{}, fmt.Errorf("%s profile: %w", c.Profile, ErrUnsupportedProfile)
	}

	resY411, err := r.Bit() // reserved, old interlaced mode
	if err != nil {
		return Config{}, err
	}
	if resY411 {
		return Config{}, fmt.Errorf("old interlaced mode: %w", ErrInvalidValue)
	}

	if c.Sprite, err = r.Bit(); err != nil {
		return Config{}, err
	}

	if c.FrmRTQPostProc, err = readBits8(r, 3); err != nil {
		return Config{}, err
	}
	if c.BitRTQPostProc, err = readBits8(r, 5); err != nil {
		return Config{}, err
	}

	if c.LoopFilter, err = r.Bit(); err != nil {
		return Config{}, err
	}
	// FFmpeg logs an error when a Simple profile header enables the loop
	// filter but keeps the parsed value; preserve that behavior.
	if c.ResX8, err = r.Bit(); err != nil {
		return Config{}, err
	}
	if c.MultiRes, err = r.Bit(); err != nil {
		return Config{}, err
	}
	if c.ResFastTX, err = r.Bit(); err != nil {
		return Config{}, err
	}

	if c.FastUVMC, err = r.Bit(); err != nil {
		return Config{}, err
	}
	if !c.FastUVMC && c.Profile == ProfileSimple {
		return Config{}, fmt.Errorf("fastuvmc disabled in simple profile: %w", ErrInvalidValue)
	}

	if c.ExtendedMV, err = r.Bit(); err != nil {
		return Config{}, err
	}
	if c.ExtendedMV && c.Profile == ProfileSimple {
		return Config{}, fmt.Errorf("extended mv in simple profile: %w", ErrInvalidValue)
	}

	if c.DQuant, err = readBits8(r, 2); err != nil {
		return Config{}, err
	}
	if c.VSTransform, err = r.Bit(); err != nil {
		return Config{}, err
	}

	resTranstab, err := r.Bit() // reserved, always 0
	if err != nil {
		return Config{}, err
	}
	if resTranstab {
		return Config{}, fmt.Errorf("reserved res_transtab: %w", ErrInvalidValue)
	}

	if c.Overlap, err = r.Bit(); err != nil {
		return Config{}, err
	}
	if c.ResyncMarker, err = r.Bit(); err != nil {
		return Config{}, err
	}
	if c.Rangered, err = r.Bit(); err != nil {
		return Config{}, err
	}
	if c.MaxBFrames, err = readBits8(r, 3); err != nil {
		return Config{}, err
	}

	quantizer, err := r.Bits(2)
	if err != nil {
		return Config{}, err
	}
	c.QuantizerMode = QuantizerMode(quantizer)

	if c.FInterpFlag, err = r.Bit(); err != nil {
		return Config{}, err
	}

	if c.Sprite {
		width, err := r.Bits(11)
		if err != nil {
			return Config{}, err
		}
		height, err := r.Bits(11)
		if err != nil {
			return Config{}, err
		}
		if width == 0 || height == 0 {
			return Config{}, fmt.Errorf("sprite size %dx%d: %w", width, height, ErrInvalidValue)
		}
		c.SpriteWidth = uint16(width)
		c.SpriteHeight = uint16(height)
		if err := r.Skip(5); err != nil { // reserved frame rate
			return Config{}, err
		}
		if c.ResX8, err = r.Bit(); err != nil {
			return Config{}, err
		}
		extraSprite, err := r.Bit() // optional sprite DC VLC selection
		if err != nil {
			return Config{}, err
		}
		if extraSprite {
			return Config{}, fmt.Errorf("sprite dc vlc selection: %w", ErrUnsupportedFeature)
		}
		if err := r.Skip(3); err != nil { // reserved slice code
			return Config{}, err
		}
		c.ResRTMFlag = false
	} else if c.ResRTMFlag, err = r.Bit(); err != nil {
		return Config{}, err
	}

	if !c.ResFastTX {
		// Reserved 16-bit field, "always 0x402F" in FFmpeg samples.
		if err := r.Skip(16); err != nil {
			return Config{}, err
		}
	}
	return c, nil
}

func readBits8(r *BitReader, n int) (uint8, error) {
	v, err := r.Bits(n)
	return uint8(v), err
}
