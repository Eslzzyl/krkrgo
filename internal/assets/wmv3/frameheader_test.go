package wmv3

import (
	"errors"
	"reflect"
	"testing"
)

// frameBits writes one frame header MSB-first, in the progressive
// Simple/Main order of ff_vc1_parse_frame_header.
type frameBits struct {
	buf []byte
	n   int
}

func (b *frameBits) add(v uint32, n int) {
	for i := n - 1; i >= 0; i-- {
		if b.n%8 == 0 {
			b.buf = append(b.buf, 0)
		}
		if v>>uint(i)&1 == 1 {
			b.buf[b.n/8] |= 1 << (7 - b.n%8)
		}
		b.n++
	}
}

func (b *frameBits) bit(v bool) {
	var u uint32
	if v {
		u = 1
	}
	b.add(u, 1)
}

// imodeBit writes one IMODE code: invert bit plus the IMODE VLC from
// tables.go (index 0..6 with imodeCodes/imodeBits).
func (b *frameBits) imode(index int, invert bool) {
	b.bit(invert)
	b.add(uint32(imodeCodes[index]), int(imodeBits[index]))
}

// decode012 writes the truncated unary code for 0..2: "0", "10", "11".
func (b *frameBits) decode012(v int) {
	switch v {
	case 0:
		b.bit(false)
	case 1:
		b.bit(true)
		b.bit(false)
	case 2:
		b.bit(true)
		b.bit(true)
	}
}

// iFrame writes a minimal I frame for a Simple/Main config: framecnt,
// picture bit, buffer fullness, PQINDEX, halfpq, and the AC/DC table
// codes. finterp/rangered/extendedMV/multiRes/resX8 must match the
// config passed to the decoder.
type iFrame struct {
	finterp       bool
	rangered      bool
	extendedMV    bool
	multiRes      bool
	resX8         bool
	quantExplicit bool // config quantizer is QUANT_FRAME_EXPLICIT
	pqindex       uint32
	halfpq        bool
	x8type        bool
	cac           int // decode012 value 0..2
	yac           int
	dc            int
}

func (f iFrame) bytes() []byte {
	var b frameBits
	if f.finterp {
		b.bit(false) // INTERPFRM
	}
	b.add(0, 2) // framecnt, unused
	if f.rangered {
		b.bit(false) // RANGEREDFRM
	}
	b.bit(false) // not P
	// maxBFrames == 0 in the test configs, so no second picture bit.
	b.add(0x2a, 7) // buffer fullness, unused
	b.add(f.pqindex, 5)
	if f.pqindex < 9 {
		b.bit(f.halfpq)
	}
	if f.quantExplicit {
		b.bit(true) // PQuantizer
	}
	if f.extendedMV {
		b.bit(false) // mvrange unary(0,3) = 0
	}
	if f.multiRes {
		b.add(0, 2) // RESPIC
	}
	if f.resX8 {
		b.bit(f.x8type)
	}
	// No P/B switch branch for I pictures.
	if !f.x8type {
		b.decode012(f.cac)
		b.decode012(f.yac)
		b.bit(f.dc != 0)
	}
	return b.buf
}

func newTestDecoder(t *testing.T, hf headerFields, width, height int) *Decoder {
	t.Helper()
	d, err := NewDecoder(hf.bytes(), width, height)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	return d
}

func TestNewDecoderAccessors(t *testing.T) {
	d := newTestDecoder(t, validHeader(), 1280, 720)
	if got, want := d.Config().Profile, ProfileMain; got != want {
		t.Errorf("Config().Profile = %v, want %v", got, want)
	}
	w, h := d.Size()
	if w != 1280 || h != 720 {
		t.Errorf("Size() = %dx%d, want 1280x720", w, h)
	}
	mbw, mbh := d.MacroblockGrid()
	if mbw != 80 || mbh != 45 {
		t.Errorf("MacroblockGrid() = %dx%d, want 80x45", mbw, mbh)
	}
}

func TestNewDecoderRejectsSprite(t *testing.T) {
	sh := validHeader()
	sh.sprite = true
	sh.spriteW, sh.spriteH = 320, 240
	_, err := NewDecoder(sh.bytes(), 320, 240)
	if !errors.Is(err, ErrUnsupportedFeature) {
		t.Fatalf("sprite extradata: err = %v, want ErrUnsupportedFeature", err)
	}
}

func TestNewDecoderRejectsBadDimensions(t *testing.T) {
	d := newTestDecoder(t, validHeader(), 32, 32)
	_ = d
	for _, tc := range []struct{ w, h int }{
		{0, 32}, {32, 0}, {-1, 32}, {32, -1}, {maxFrameDimension + 1, 32}, {32, maxFrameDimension + 1},
	} {
		_, err := NewDecoder(validHeader().bytes(), tc.w, tc.h)
		if !errors.Is(err, ErrInvalidValue) {
			t.Errorf("NewDecoder(%d, %d): err = %v, want ErrInvalidValue", tc.w, tc.h, err)
		}
	}
}

func TestNewDecoderRejectsOversizeGrid(t *testing.T) {
	// 8192x8192 is exactly the 512x512 macroblock cap; one step more
	// on either side must fail.
	if _, err := NewDecoder(validHeader().bytes(), maxFrameDimension, maxFrameDimension); err != nil {
		t.Errorf("8192x8192: %v", err)
	}
}

func TestParseFrameHeaderI(t *testing.T) {
	cfg := validHeader() // main, implicit quantizer
	d := newTestDecoder(t, cfg, 1280, 720)
	f := iFrame{pqindex: 12, halfpq: false, cac: 0, yac: 1, dc: 1}
	h, err := d.ParseFrameHeader(f.bytes())
	if err != nil {
		t.Fatalf("ParseFrameHeader: %v", err)
	}
	if h.Picture != PictureI || h.BiType {
		t.Errorf("Picture = %v BiType = %v, want I/false", h.Picture, h.BiType)
	}
	if h.PQIndex != 12 || h.PQ != int(PQuantTable[0][12]) {
		t.Errorf("PQIndex/PQ = %d/%d, want 12/%d", h.PQIndex, h.PQ, PQuantTable[0][12])
	}
	if h.PQ != int(PQuantTable[0][12]) {
		t.Errorf("implicit PQ mismatch: %d vs table %d", h.PQ, PQuantTable[0][12])
	}
	if h.PQuantizer {
		t.Errorf("PQuantizer = true for pqindex 12 implicit, want false")
	}
	if !h.Rnd {
		t.Errorf("Rnd = false for I picture, want true")
	}
	if h.CACTable != 0 || h.YACTable != 1 || h.DCTable != 1 {
		t.Errorf("AC/DC tables = %d/%d/%d, want 0/1/1", h.CACTable, h.YACTable, h.DCTable)
	}
	if h.KX != 9 || h.KY != 8 || h.RangeX != 256 || h.RangeY != 128 {
		t.Errorf("MV range k_x/k_y/range_x/range_y = %d/%d/%d/%d, want 9/8/256/128", h.KX, h.KY, h.RangeX, h.RangeY)
	}
}

func TestParseFrameHeaderIHalfPQ(t *testing.T) {
	d := newTestDecoder(t, validHeader(), 64, 64)
	f := iFrame{pqindex: 5, halfpq: true, cac: 0, yac: 0, dc: 0}
	h, err := d.ParseFrameHeader(f.bytes())
	if err != nil {
		t.Fatalf("ParseFrameHeader: %v", err)
	}
	if h.PQ != int(PQuantTable[0][5]) || !h.HalfPQ {
		t.Errorf("PQ/HalfPQ = %d/%v, want %d/true", h.PQ, h.HalfPQ, PQuantTable[0][5])
	}
	if !h.PQuantizer {
		t.Errorf("PQuantizer = false for implicit pqindex<9, want true")
	}
}

func TestParseFrameHeaderIExplicitQuant(t *testing.T) {
	cfg := validHeader()
	cfg.quantizer = uint32(QuantFrameExplicit)
	d := newTestDecoder(t, cfg, 64, 64)
	f := iFrame{quantExplicit: true, pqindex: 16, cac: 2, yac: 2, dc: 0}
	h, err := d.ParseFrameHeader(f.bytes())
	if err != nil {
		t.Fatalf("ParseFrameHeader: %v", err)
	}
	if h.PQ != int(PQuantTable[1][16]) {
		t.Errorf("explicit PQ = %d, want %d", h.PQ, PQuantTable[1][16])
	}
	if !h.PQuantizer {
		t.Errorf("PQuantizer = false, want explicit bit true")
	}
	if h.CACTable != 2 || h.YACTable != 2 {
		t.Errorf("AC tables = %d/%d, want 2/2", h.CACTable, h.YACTable)
	}
}

func TestParseFrameHeaderP(t *testing.T) {
	cfg := validHeader()
	cfg.quantizer = uint32(QuantFrameExplicit)
	cfg.extendedMV = true
	d := newTestDecoder(t, cfg, 640, 480)

	var b frameBits
	b.add(0, 2) // framecnt
	b.bit(true) // P picture
	b.add(16, 5)
	b.bit(true)  // pquantizer (explicit)
	b.bit(false) // mvrange unary(0,3) = 0
	// MV mode: unary(1,4) = 0 -> table[1][0] = 1MV_HPEL_BILIN.
	b.bit(true)
	// Skip bitplane: IMODE_RAW.
	b.imode(ImodeRaw, false)
	b.add(1, 2) // mv_table_index
	b.add(2, 2) // cbptab
	// DQuant off, VSTransform off in cfg.
	b.decode012(1) // c_ac
	b.bit(false)   // dc

	h, err := d.ParseFrameHeader(b.buf)
	if err != nil {
		t.Fatalf("ParseFrameHeader: %v", err)
	}
	if h.Picture != PictureP {
		t.Errorf("Picture = %v, want P", h.Picture)
	}
	if !h.Rnd {
		t.Errorf("Rnd = false for first P after init, want true (0 xor 1)")
	}
	if h.PQ != int(PQuantTable[1][16]) {
		t.Errorf("PQ = %d, want %d", h.PQ, PQuantTable[1][16])
	}
	if h.TTIndex != 2 {
		t.Errorf("TTIndex = %d, want 2 (pq 16 > 4 and > 12)", h.TTIndex)
	}
	if h.MVMode != MVMode1MVHPelBilin || h.QuarterSample || h.MSPel {
		t.Errorf("MVMode = %d QS=%v MSPel=%v, want 1MVHPelBilin/false/false", h.MVMode, h.QuarterSample, h.MSPel)
	}
	if !h.SkipRaw || h.SkipPlane != nil {
		t.Errorf("SkipRaw = %v plane = %v, want true/nil (IMODE_RAW)", h.SkipRaw, h.SkipPlane)
	}
	if h.MVTableIndex != 1 || h.CBPTab != 2 {
		t.Errorf("MVTableIndex/CBPTab = %d/%d, want 1/2", h.MVTableIndex, h.CBPTab)
	}
	if !h.TTMBF || h.TTFrame != TT8x8 {
		t.Errorf("TTMBF/TTFrame = %v/%d, want true/TT8x8 when VSTransform off", h.TTMBF, h.TTFrame)
	}
	if h.CACTable != 1 || h.DCTable != 0 || h.YACTable != 0 {
		t.Errorf("AC/DC tables = %d/%d/%d, want 1/0/0", h.CACTable, h.DCTable, h.YACTable)
	}
	if h.KX != 9 || h.RangeX != 256 {
		t.Errorf("mvrange fields wrong: KX=%d RangeX=%d", h.KX, h.RangeX)
	}
}

func TestParseFrameHeaderPRndToggles(t *testing.T) {
	cfg := validHeader()
	cfg.quantizer = uint32(QuantFrameExplicit)
	d := newTestDecoder(t, cfg, 32, 32)

	pFrame := func() []byte {
		var b frameBits
		b.add(0, 2)
		b.bit(true)
		b.add(16, 5)
		b.bit(true)
		b.bit(true) // 1MV
		b.imode(ImodeRaw, false)
		b.add(0, 2)
		b.add(0, 2)
		b.bit(false)
		b.bit(false)
		return b.buf
	}
	for i, want := range []bool{true, false, true} {
		h, err := d.ParseFrameHeader(pFrame())
		if err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
		if h.Rnd != want {
			t.Errorf("frame %d: Rnd = %v, want %v", i, h.Rnd, want)
		}
	}
}

func TestParseFrameHeaderPMixedMVPlane(t *testing.T) {
	cfg := validHeader()
	cfg.quantizer = uint32(QuantFrameExplicit)
	d := newTestDecoder(t, cfg, 32, 32) // 2x2 macroblocks

	var b frameBits
	b.add(0, 2)
	b.bit(true)
	b.add(12, 5) // explicit PQ = 12, so lowquant row 1
	b.bit(true)
	// mv_mode unary(1,4) = 1 -> table[1][1] = MIXED_MV.
	b.bit(false)
	b.bit(true)
	// mv_type bitplane: IMODE_NORM2, invert off, two NORM2 symbols.
	b.imode(ImodeNorm2, false)
	b.add(uint32(norm2Codes[1]), int(norm2Bits[1])) // (1,0)
	b.add(uint32(norm2Codes[3]), int(norm2Bits[3])) // (1,1)
	// skip plane: RAW.
	b.imode(ImodeRaw, false)
	b.add(0, 2)
	b.add(0, 2)
	b.bit(false)
	b.bit(false)

	h, err := d.ParseFrameHeader(b.buf)
	if err != nil {
		t.Fatalf("ParseFrameHeader: %v", err)
	}
	if h.MVMode != MVModeMixed {
		t.Fatalf("MVMode = %d, want MIXED", h.MVMode)
	}
	if h.MVTypeRaw {
		t.Errorf("MVTypeRaw = true, want false for NORM2")
	}
	want := []uint8{1, 0, 1, 1}
	if len(h.MVTypePlane) != 4 {
		t.Fatalf("MVTypePlane len = %d, want 4", len(h.MVTypePlane))
	}
	for i := range want {
		if h.MVTypePlane[i] != want[i] {
			t.Errorf("MVTypePlane[%d] = %d, want %d", i, h.MVTypePlane[i], want[i])
		}
	}
	if !h.SkipRaw {
		t.Errorf("SkipRaw = false, want true")
	}
	if !h.QuarterSample || !h.MSPel {
		t.Errorf("QS/MSPel = %v/%v, want true/true for MIXED", h.QuarterSample, h.MSPel)
	}
}

func TestParseFrameHeaderPIntensityComp(t *testing.T) {
	cfg := validHeader()
	cfg.quantizer = uint32(QuantFrameExplicit)
	d := newTestDecoder(t, cfg, 32, 32)

	var b frameBits
	b.add(0, 2)
	b.bit(true)
	b.add(16, 5)
	b.bit(true)
	// mv_mode unary = 3 -> table[1][3] = INTENSITY_COMP.
	b.bit(false)
	b.bit(false)
	b.bit(false)
	b.bit(true)
	// mv_mode2 unary(1,3) = 1 -> table2[0][1] = 1MV.
	b.bit(false)
	b.bit(true)
	b.add(40, 6)             // lumscale
	b.add(7, 6)              // lumshift
	b.imode(ImodeRaw, false) // skip plane
	b.add(0, 2)
	b.add(0, 2)
	b.bit(false)
	b.bit(false)

	h, err := d.ParseFrameHeader(b.buf)
	if err != nil {
		t.Fatalf("ParseFrameHeader: %v", err)
	}
	if h.MVMode != MVModeIntensityComp || h.MVMode2 != MVMode1MV {
		t.Fatalf("MVMode/Mode2 = %d/%d, want INTENSITY_COMP/1MV", h.MVMode, h.MVMode2)
	}
	if h.LumScale != 40 || h.LumShift != 7 {
		t.Errorf("LumScale/LumShift = %d/%d, want 40/7", h.LumScale, h.LumShift)
	}
	if !h.QuarterSample || !h.MSPel {
		t.Errorf("QS/MSPel = %v/%v, want true/true for 1MV mode2", h.QuarterSample, h.MSPel)
	}
	if !d.ic.lastUse {
		t.Errorf("ic.lastUse = false after intensity comp frame, want true")
	}
	// The chained LUT must differ from the identity baseline.
	if d.ic.lastY[0][0] == 0 && d.ic.lastY[0][255] == 255 {
		t.Errorf("ic LUT looks like identity after setIC")
	}
}

func TestParseFrameHeaderX8Frame(t *testing.T) {
	cfg := validHeader()
	cfg.resX8 = true
	d := newTestDecoder(t, cfg, 64, 64)

	var b frameBits
	b.add(0, 2)
	b.bit(false) // I
	b.add(0x2a, 7)
	b.add(10, 5)
	// res_x8 on: x8_type bit, no halfpq (pqindex 10 >= 9).
	b.bit(true) // x8_type
	// x8_type skips AC/DC table codes entirely.
	h, err := d.ParseFrameHeader(b.buf)
	if err != nil {
		t.Fatalf("ParseFrameHeader: %v", err)
	}
	if !h.X8Type {
		t.Errorf("X8Type = false, want true")
	}
	if h.CACTable != 0 || h.YACTable != 0 || h.DCTable != 0 {
		t.Errorf("AC/DC tables must stay zero for X8 frames")
	}
	if _, err := d.Decode(b.buf); !errors.Is(err, ErrUnsupportedFeature) {
		t.Errorf("Decode x8 frame: err = %v, want ErrUnsupportedFeature", err)
	}
}

func TestParseFrameHeaderBFractionAndB(t *testing.T) {
	cfg := validHeader()
	cfg.maxBFrames = 2
	d := newTestDecoder(t, cfg, 64, 64)

	var b frameBits
	b.add(0, 2)
	b.bit(false) // not P
	b.bit(false) // B (maxBFrames > 0)
	b.add(1, 3)  // bfraction index 1 -> 85
	b.add(12, 5)
	// B branch: mv_mode bit 1 -> 1MV; direct + skip planes RAW.
	b.bit(true)
	b.imode(ImodeRaw, false) // direct
	b.imode(ImodeRaw, false) // skip
	b.add(0, 2)
	b.add(0, 2)
	b.bit(false)
	b.bit(false)

	h, err := d.ParseFrameHeader(b.buf)
	if err != nil {
		t.Fatalf("ParseFrameHeader: %v", err)
	}
	if h.Picture != PictureB || h.BiType {
		t.Fatalf("Picture/BiType = %v/%v, want B/false", h.Picture, h.BiType)
	}
	if h.BFractionLutIndex != 1 || h.BFraction != 85 {
		t.Errorf("BFraction index/value = %d/%d, want 1/85", h.BFractionLutIndex, h.BFraction)
	}
	if h.MVMode != MVMode1MV || !h.QuarterSample || !h.MSPel {
		t.Errorf("MVMode/QS/MSPel = %d/%v/%v, want 1MV/true/true", h.MVMode, h.QuarterSample, h.MSPel)
	}
	if !h.DirectRaw || !h.SkipRaw {
		t.Errorf("DirectRaw/SkipRaw = %v/%v, want true/true", h.DirectRaw, h.SkipRaw)
	}
	if _, err := d.Decode(b.buf); !errors.Is(err, ErrUnsupportedFeature) {
		t.Errorf("Decode B frame: err = %v, want ErrUnsupportedFeature", err)
	}
}

func TestParseFrameHeaderBIDistinguished(t *testing.T) {
	// bfraction index 22 (3-bit escape 7 + 4-bit 15) selects BI. BI
	// carries buffer fullness and, unlike B, reads the luma AC table.
	cfg := validHeader()
	cfg.maxBFrames = 2
	d := newTestDecoder(t, cfg, 64, 64)

	var b frameBits
	b.add(0, 2)
	b.bit(false)
	b.bit(false) // B path
	b.add(7, 3)
	b.add(15, 4)
	b.add(0x2a, 7) // buffer fullness
	b.add(10, 5)
	b.bit(false) // c_ac
	b.bit(false) // y_ac (BI reads it)
	b.bit(false) // dc

	h, err := d.ParseFrameHeader(b.buf)
	if err != nil {
		t.Fatalf("ParseFrameHeader: %v", err)
	}
	if h.Picture != PictureB || !h.BiType {
		t.Fatalf("Picture/BiType = %v/%v, want B/true", h.Picture, h.BiType)
	}
	if h.BFraction != 0 {
		t.Errorf("BFraction = %d, want 0 for BI", h.BFraction)
	}
	if !h.Rnd {
		t.Errorf("Rnd = false for BI, want true")
	}
	if h.YACTable != 0 {
		t.Errorf("YACTable = %d, want 0", h.YACTable)
	}
	if _, err := d.Decode(b.buf); !errors.Is(err, ErrUnsupportedFeature) {
		t.Errorf("Decode BI: err = %v, want ErrUnsupportedFeature", err)
	}
}

func TestParseFrameHeaderRejectsInvalidBFraction(t *testing.T) {
	cfg := validHeader()
	cfg.maxBFrames = 2
	d := newTestDecoder(t, cfg, 32, 32)

	// Escape: 3-bit value 7 followed by 4-bit 14 selects the invalid
	// index 21 (vc1.c read_bfraction rejects it).
	var b frameBits
	b.add(0, 2)
	b.bit(false)
	b.bit(false) // B
	b.add(7, 3)
	b.add(14, 4)
	if _, err := d.ParseFrameHeader(b.buf); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("bfraction 21: err = %v, want ErrInvalidValue", err)
	}
}

func TestParseFrameHeaderRejectsPQIndexZero(t *testing.T) {
	d := newTestDecoder(t, validHeader(), 32, 32)
	var b frameBits
	b.add(0, 2)
	b.bit(false)
	b.add(0x2a, 7)
	b.add(0, 5) // pqindex 0 is forbidden
	if _, err := d.ParseFrameHeader(b.buf); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("pqindex 0: err = %v, want ErrInvalidValue", err)
	}
}

func TestParseFrameHeaderTruncated(t *testing.T) {
	d := newTestDecoder(t, validHeader(), 32, 32)
	// Frame that stops right after the picture bit.
	var b frameBits
	b.add(0, 2)
	b.bit(false)
	if _, err := d.ParseFrameHeader(b.buf); !errors.Is(err, ErrTruncated) {
		t.Fatalf("truncated frame: err = %v, want ErrTruncated", err)
	}
	// I frame whose PQ index field is cut short of the 5-bit check.
	var b2 frameBits
	b2.add(0, 2)
	b2.bit(false)
	b2.add(0x2a, 7)
	b2.add(0x1f, 4) // only 4 of 5 pqindex bits
	if _, err := d.ParseFrameHeader(b2.buf); !errors.Is(err, ErrTruncated) {
		t.Fatalf("short pqindex: err = %v, want ErrTruncated", err)
	}
}

func TestDecodeIReportsPictureUnavailable(t *testing.T) {
	d := newTestDecoder(t, validHeader(), 64, 64)
	f := iFrame{pqindex: 10, cac: 0, yac: 0, dc: 0}
	frame, err := d.Decode(f.bytes())
	if !errors.Is(err, ErrPictureUnavailable) {
		t.Fatalf("Decode I: err = %v, want ErrPictureUnavailable", err)
	}
	if frame.Header.Picture != PictureI {
		t.Errorf("returned Frame.Header.Picture = %v, want I", frame.Header.Picture)
	}
}

func TestDecodeTrimsEndOfSequence(t *testing.T) {
	d := newTestDecoder(t, validHeader(), 64, 64)
	f := iFrame{pqindex: 10, cac: 0, yac: 0, dc: 0}
	data := append(append([]byte{}, f.bytes()...), 0x00, 0x00, 0x01, 0x0A)
	h1, err := d.ParseFrameHeader(f.bytes())
	if err != nil {
		t.Fatalf("plain: %v", err)
	}
	h2, err := d.ParseFrameHeader(data)
	if err != nil {
		t.Fatalf("with end-of-sequence: %v", err)
	}
	if !reflect.DeepEqual(h1, h2) {
		t.Errorf("end-of-sequence marker changed the parse")
	}
}

func TestParseFrameHeaderFinterpAndRangered(t *testing.T) {
	cfg := validHeader()
	cfg.finterp = true
	cfg.rangered = true
	d := newTestDecoder(t, cfg, 32, 32)

	var b frameBits
	b.bit(true)  // INTERPFRM
	b.add(0, 2)  // framecnt
	b.bit(true)  // RANGEREDFRM
	b.bit(false) // I
	b.add(0x2a, 7)
	b.add(8, 5)  // pqindex 8 < 9 reads halfpq
	b.bit(false) // halfpq
	b.bit(false) // c_ac
	b.bit(false) // y_ac
	b.bit(false) // dc

	h, err := d.ParseFrameHeader(b.buf)
	if err != nil {
		t.Fatalf("ParseFrameHeader: %v", err)
	}
	if !h.InterpFrm || !h.RangeRedFrm {
		t.Errorf("InterpFrm/RangeRedFrm = %v/%v, want true/true", h.InterpFrm, h.RangeRedFrm)
	}
	if h.PQ != int(PQuantTable[0][8]) {
		t.Errorf("PQ = %d, want %d", h.PQ, PQuantTable[0][8])
	}
}
