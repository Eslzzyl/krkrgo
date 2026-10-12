// Frame-header parsing translated from libavcodec/vc1.c
// ff_vc1_parse_frame_header and its helpers (read_bfraction,
// vop_dquant_decoding, rotate_luts, INIT_LUT), LGPL-2.1-or-later; see
// doc.go for the full license notice.
//
// Only progressive Simple/Main headers are parsed. The Simple/Main
// header never carries FCM/field syntax (vc1.c:671-672 sets
// field_mode=0 and fcm=PROGRESSIVE unconditionally), so interlaced
// state cannot occur here; Advanced profile headers
// (ff_vc1_parse_frame_header_adv) are out of scope.
package wmv3

import "fmt"

// PictureType is the coded picture type of one frame, after the BI
// conversion at the end of the header (FFmpeg pict_type + bi_type).
type PictureType uint8

const (
	PictureI PictureType = iota + 1
	PictureP
	PictureB
)

func (p PictureType) String() string {
	switch p {
	case PictureI:
		return "I"
	case PictureP:
		return "P"
	case PictureB:
		return "B"
	default:
		return fmt.Sprintf("unknown(%d)", uint8(p))
	}
}

// DQProfile is the 2-bit DQPROFILE syntax element selecting where
// DQUANT alternate quantizers apply, matching enum DQProfile in
// libavcodec/vc1.h.
type DQProfile uint8

const (
	DQProfileFourEdges   DQProfile = 0
	DQProfileDoubleEdges DQProfile = 1
	DQProfileSingleEdge  DQProfile = 2
	DQProfileAllMBs      DQProfile = 3
)

func (p DQProfile) String() string {
	switch p {
	case DQProfileFourEdges:
		return "four edges"
	case DQProfileDoubleEdges:
		return "double edges"
	case DQProfileSingleEdge:
		return "single edge"
	case DQProfileAllMBs:
		return "all MBs"
	default:
		return fmt.Sprintf("unknown(%d)", uint8(p))
	}
}

// FrameHeader is one fully parsed progressive Simple/Main frame header
// (VC-1 section 7.1.1.4, ff_vc1_parse_frame_header). Every syntax
// element the macroblock stage needs is preserved, including the
// macroblock-grid bitplanes owned by this header. The zero
// FrameHeader is not valid; use Decoder.ParseFrameHeader.
//
// Fields marked for P/B pictures keep their zero value on I pictures.
// Unlike FFmpeg's mutable VC1Context, this struct does not retain
// stale values from the previous frame; the decoder-owned state that
// genuinely persists across frames (rounding control, intensity
// compensation tables) lives in Decoder.
type FrameHeader struct {
	// Picture is I, P, or B. BI frames report PictureB with BiType
	// set, matching FFmpeg's conversion of pict_type BI to
	// B + bi_type=1 at the end of the header.
	Picture PictureType
	BiType  bool

	InterpFrm   bool // INTERPFRM bit, present when Config.FInterpFlag
	RangeRedFrm bool // RANGEREDFRM bit, present when Config.Rangered

	// Quantizer selection.
	PQIndex    int  // 5-bit PQINDEX, never 0
	PQ         int  // frame quantizer from PQuantTable
	HalfPQ     bool // half-step quantizer, PQIndex < 9
	PQuantizer bool // uniform (true) vs non-uniform (false) quantizer

	// Motion-vector range, from ExtendedMV streams only.
	MVRange int // get_unary(0, 3): 0..3
	KX      int // MVRange + 9 + (MVRange >> 1): 9, 10, 12, or 13
	KY      int // MVRange + 8: 8..11
	RangeX  int // 1 << (KX - 1)
	RangeY  int // 1 << (KY - 1)

	Respic int  // RESPIC 0..3, present when Config.MultiRes and not a B picture
	X8Type bool // X8 hybrid transform; only I/BI frames when Config.ResX8
	Rnd    bool // rounding control in effect for this frame

	// P/B coding tools; zero values on I pictures.
	MVMode        uint8 // MVModes; meaningful for P/B pictures
	MVMode2       uint8 // second-level mode when MVMode == MVModeIntensityComp
	QuarterSample bool  // motion vectors are quarter-pel
	MSPel         bool  // half-pel interpolation may be used
	LumScale      uint8 // 6-bit intensity-compensation scale
	LumShift      uint8 // 6-bit intensity-compensation shift

	// Macroblock grids. Plane slices are mbWidth*mbHeight bits in
	// row-major order, owned by this header. The raw flag means the
	// macroblock layer reads one bit per macroblock from the
	// bitstream instead (IMODE_RAW), and the plane is nil.
	MVTypeRaw   bool
	MVTypePlane []uint8 // mixed-MV P pictures only
	SkipRaw     bool
	SkipPlane   []uint8
	DirectRaw   bool
	DirectPlane []uint8 // B pictures only

	MVTableIndex int // 2-bit MV VLC table select
	CBPTab       int // 2-bit CBPCY VLC table select

	// DQUANT, present when Config.DQuant != 0.
	DQuantFrame bool
	DQProfile   DQProfile
	DQSbEdge    int // 0..3, edge profiles only
	DQBilevel   bool
	AltPQ       int // alternate quantizer for the selected DQPROFILE

	// Transform size signaling.
	TTMBF   bool  // per-macroblock transform size is coded; false on I pictures
	TTFrame uint8 // TransformTypes value used when TTMBF is false

	// Coefficient VLC table selection.
	CACTable int // 0..2 chroma AC tables
	YACTable int // 0..2 luma AC tables, I/BI pictures only
	DCTable  int // 0..1 DC tables

	// B pictures.
	BFractionLutIndex int   // 0..22, never 21
	BFraction         int16 // scale from BFractionLUT; 0 marks BI

	// TTIndex is (PQ > 4) + (PQ > 12); selects the TTMB VLC in the
	// macroblock layer.
	TTIndex int
}

// readBFraction decodes the B fraction (VC-1 section 7.1.1.6,
// read_bfraction in vc1.c): a 3-bit LUT index, escaped to 7..22 by a
// following 4-bit field when the index is 7. Index 21 is invalid;
// index 22 (scale 0) selects BI frames.
func readBFraction(r *BitReader) (int, error) {
	idx, err := r.Bits(3)
	if err != nil {
		return 0, err
	}
	if idx == 7 {
		hi, err := r.Bits(4)
		if err != nil {
			return 0, err
		}
		idx = 7 + hi
	}
	if idx == 21 {
		return 0, fmt.Errorf("bfraction index %d: %w", idx, ErrInvalidValue)
	}
	return int(idx), nil
}

// parseDQuant decodes the DQUANT frame-level fields
// (vop_dquant_decoding in vc1.c). The caller invokes it only when
// Config.DQuant != 0; DQuantFrame must already be false.
func parseDQuant(r *BitReader, cfg Config, h *FrameHeader) error {
	// DQuant == 2 skips the DQPROFILE selection entirely.
	if cfg.DQuant != 2 {
		frame, err := r.Bit()
		if err != nil {
			return err
		}
		h.DQuantFrame = frame
		if !h.DQuantFrame {
			return nil
		}
		prof, err := r.Bits(2)
		if err != nil {
			return err
		}
		h.DQProfile = DQProfile(prof)
		switch h.DQProfile {
		case DQProfileSingleEdge, DQProfileDoubleEdges:
			edge, err := r.Bits(2)
			if err != nil {
				return err
			}
			h.DQSbEdge = int(edge)
		case DQProfileAllMBs:
			bilevel, err := r.Bit()
			if err != nil {
				return err
			}
			h.DQBilevel = bilevel
			if !h.DQBilevel {
				// FFmpeg also clears HalfPQ here.
				h.HalfPQ = false
				return nil
			}
		default:
			// DQProfileFourEdges: no extra field, falls through.
		}
	}
	pqdiff, err := r.Bits(3)
	if err != nil {
		return err
	}
	if pqdiff == 7 {
		altpq, err := r.Bits(5)
		if err != nil {
			return err
		}
		h.AltPQ = int(altpq)
	} else {
		h.AltPQ = h.PQ + int(pqdiff) + 1
	}
	return nil
}

// parseFrameHeader decodes one progressive Simple/Main frame header
// from r and advances the decoder's cross-frame state (rounding
// control and the intensity-compensation LUT rotation) exactly once,
// matching ff_vc1_parse_frame_header with first_pic_header_flag set
// (WMV3 frames have no slices, so the flag is always set).
func (d *Decoder) parseFrameHeader(r *BitReader) (FrameHeader, error) {
	var h FrameHeader
	cfg := d.cfg

	// INTERPFRM
	if cfg.FInterpFlag {
		interp, err := r.Bit()
		if err != nil {
			return h, err
		}
		h.InterpFrm = interp
	}
	// frame counter field, unused for WMV3 (vc1.c:679-680 skips 2 bits
	// for every codec but MSS2).
	if err := r.Skip(2); err != nil {
		return h, err
	}
	if cfg.Rangered {
		red, err := r.Bit()
		if err != nil {
			return h, err
		}
		h.RangeRedFrm = red
	}

	// Picture type selection (vc1.c:684-692).
	pictBit, err := r.Bit()
	if err != nil {
		return h, err
	}
	// biCode tracks the pre-conversion picture type so BI frames skip
	// the P and B switch branches, exactly as FFmpeg's distinct
	// AV_PICTURE_TYPE_BI does.
	biCode := PictureI
	bi := false
	if pictBit {
		h.Picture = PictureP
		biCode = PictureP
	} else if cfg.MaxBFrames > 0 {
		iBit, err := r.Bit()
		if err != nil {
			return h, err
		}
		if iBit {
			h.Picture = PictureI
			biCode = PictureI
		} else {
			h.Picture = PictureB
			biCode = PictureB
		}
	} else {
		h.Picture = PictureI
		biCode = PictureI
	}

	if h.Picture == PictureB {
		idx, err := readBFraction(r)
		if err != nil {
			return h, err
		}
		h.BFractionLutIndex = idx
		h.BFraction = BFractionLUT[idx]
		if h.BFraction == 0 {
			bi = true
		}
	}
	// I and BI frames carry a 7-bit buffer-fullness field.
	isI := h.Picture == PictureI || bi
	isB := h.Picture == PictureB && !bi
	if bi {
		biCode = PictureI // BI takes no P/B switch branch, like I
	}
	if isI {
		if err := r.Skip(7); err != nil {
			return h, err
		}
	}

	// Rounding control (vc1.c:707-711): I/BI reset it to 1, P toggles.
	if isI {
		d.rnd = true
	}
	if h.Picture == PictureP {
		d.rnd = !d.rnd
	}
	h.Rnd = d.rnd

	// Quantizer selection (vc1.c:713-741).
	if r.Remaining() < 5 {
		return h, fmt.Errorf("frame header quantizer: %w", ErrTruncated)
	}
	pqindex, err := r.Bits(5)
	if err != nil {
		return h, err
	}
	if pqindex == 0 {
		return h, fmt.Errorf("pqindex 0: %w", ErrInvalidValue)
	}
	h.PQIndex = int(pqindex)
	if cfg.QuantizerMode == QuantFrameImplicit {
		h.PQ = int(PQuantTable[0][pqindex])
	} else {
		h.PQ = int(PQuantTable[1][pqindex])
	}
	if pqindex < 9 {
		half, err := r.Bit()
		if err != nil {
			return h, err
		}
		h.HalfPQ = half
	}
	switch cfg.QuantizerMode {
	case QuantFrameImplicit:
		h.PQuantizer = pqindex < 9
	case QuantNonUniform:
		h.PQuantizer = false
	case QuantFrameExplicit:
		pquantizer, err := r.Bit()
		if err != nil {
			return h, err
		}
		h.PQuantizer = pquantizer
	default: // QuantUniform
		h.PQuantizer = true
	}

	// Extended motion-vector range (vc1.c:743-748).
	if cfg.ExtendedMV {
		mvrange, err := Unary(r, false, 3)
		if err != nil {
			return h, err
		}
		h.MVRange = mvrange
	}
	h.KX = h.MVRange + 9 + (h.MVRange >> 1)
	h.KY = h.MVRange + 8
	h.RangeX = 1 << (h.KX - 1)
	h.RangeY = 1 << (h.KY - 1)

	// Multi-resolution RESPIC; BI frames read it too (the check is
	// against B, which has not been converted yet at this point).
	if cfg.MultiRes && !isB {
		respic, err := r.Bits(2)
		if err != nil {
			return h, err
		}
		h.Respic = int(respic)
	}

	// X8 hybrid transform flag, I/BI only (vc1.c:752-755).
	if cfg.ResX8 && isI {
		x8, err := r.Bit()
		if err != nil {
			return h, err
		}
		h.X8Type = x8
	}

	// Intensity-compensation LUT rotation; first_pic_header_flag is
	// always set for WMV3 frames (vc1.c:760-761).
	d.ic.rotate(h.Picture)

	switch biCode {
	case PictureP:
		h.TTIndex = boolToInt(h.PQ > 4) + boolToInt(h.PQ > 12)
		lowquant := 1
		if h.PQ > 12 {
			lowquant = 0
		}
		unaryMode, err := Unary(r, true, 4)
		if err != nil {
			return h, err
		}
		h.MVMode = MVPModeTable[lowquant][unaryMode]
		if h.MVMode == MVModeIntensityComp {
			unaryMode2, err := Unary(r, true, 3)
			if err != nil {
				return h, err
			}
			h.MVMode2 = MVPModeTable2[lowquant][unaryMode2]
			lumscale, err := r.Bits(6)
			if err != nil {
				return h, err
			}
			lumshift, err := r.Bits(6)
			if err != nil {
				return h, err
			}
			h.LumScale = uint8(lumscale)
			h.LumShift = uint8(lumshift)
			d.ic.setIC(h.LumScale, h.LumShift)
		}
		if h.MVMode == MVModeIntensityComp {
			h.QuarterSample = h.MVMode2 != MVMode1MVHPel && h.MVMode2 != MVMode1MVHPelBilin
			h.MSPel = h.MVMode2 != MVMode1MVHPelBilin
		} else {
			h.QuarterSample = h.MVMode != MVMode1MVHPel && h.MVMode != MVMode1MVHPelBilin
			h.MSPel = h.MVMode != MVMode1MVHPelBilin
		}
		if (h.MVMode == MVModeIntensityComp && h.MVMode2 == MVModeMixed) ||
			h.MVMode == MVModeMixed {
			plane, raw, _, err := DecodeBitplane(r, d.mbWidth, d.mbHeight)
			if err != nil {
				return h, fmt.Errorf("mb mv type plane: %w", err)
			}
			h.MVTypePlane, h.MVTypeRaw = plane, raw
		}
		plane, raw, _, err := DecodeBitplane(r, d.mbWidth, d.mbHeight)
		if err != nil {
			return h, fmt.Errorf("mb skip plane: %w", err)
		}
		h.SkipPlane, h.SkipRaw = plane, raw
		// FFmpeg checks remaining bits before the table fields in the
		// P branch only (vc1.c:817-818).
		if r.Remaining() < 4 {
			return h, fmt.Errorf("frame header tables: %w", ErrTruncated)
		}
		if err := d.parsePBTail(r, &h); err != nil {
			return h, err
		}
	case PictureB:
		h.TTIndex = boolToInt(h.PQ > 4) + boolToInt(h.PQ > 12)
		mvBit, err := r.Bit()
		if err != nil {
			return h, err
		}
		if mvBit {
			h.MVMode = MVMode1MV
		} else {
			h.MVMode = MVMode1MVHPelBilin
		}
		h.QuarterSample = h.MVMode == MVMode1MV
		h.MSPel = h.QuarterSample
		plane, raw, _, err := DecodeBitplane(r, d.mbWidth, d.mbHeight)
		if err != nil {
			return h, fmt.Errorf("mb direct plane: %w", err)
		}
		h.DirectPlane, h.DirectRaw = plane, raw
		plane, raw, _, err = DecodeBitplane(r, d.mbWidth, d.mbHeight)
		if err != nil {
			return h, fmt.Errorf("mb skip plane: %w", err)
		}
		h.SkipPlane, h.SkipRaw = plane, raw
		if err := d.parsePBTail(r, &h); err != nil {
			return h, err
		}
	}
	// I and BI pictures read neither P nor B branch syntax.

	// AC/DC VLC table selection (vc1.c:854-865), skipped for X8.
	if !h.X8Type {
		cac, err := Decode012(r)
		if err != nil {
			return h, err
		}
		h.CACTable = cac
		if isI {
			yac, err := Decode012(r)
			if err != nil {
				return h, err
			}
			h.YACTable = yac
		}
		dc, err := r.Bit()
		if err != nil {
			return h, err
		}
		h.DCTable = boolToInt(dc)
	}

	if bi {
		h.Picture = PictureB
		h.BiType = true
	}
	return h, nil
}

// parsePBTail decodes the shared tail of the P and B branches:
// MV/CBP VLC table selection, DQUANT fields, and transform-size
// signaling (vc1.c:822-849 and 852-853).
func (d *Decoder) parsePBTail(r *BitReader, h *FrameHeader) error {
	mvTable, err := r.Bits(2)
	if err != nil {
		return err
	}
	h.MVTableIndex = int(mvTable)
	cbptab, err := r.Bits(2)
	if err != nil {
		return err
	}
	h.CBPTab = int(cbptab)

	if d.cfg.DQuant != 0 {
		if err := parseDQuant(r, d.cfg, h); err != nil {
			return err
		}
	}

	if d.cfg.VSTransform {
		ttmbf, err := r.Bit()
		if err != nil {
			return err
		}
		h.TTMBF = ttmbf
		if h.TTMBF {
			ttfrm, err := r.Bits(2)
			if err != nil {
				return err
			}
			h.TTFrame = ttfrmToTT[ttfrm]
		} else {
			h.TTFrame = TT8x8
		}
	} else {
		h.TTMBF = true
		h.TTFrame = TT8x8
	}
	return nil
}
