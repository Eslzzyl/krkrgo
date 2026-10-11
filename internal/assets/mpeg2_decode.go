package assets

import (
	"math"
)

// mpeg2RealDecoder is the bounded MPEG-1/2 picture state machine used by the
// public MPEG2Decoder wrapper. It follows the MPEG-12 syntax: headers are
// byte-delimited by the caller while slices are decoded MSB-first.
type mpeg2RealDecoder struct {
	sequence         *MPEGSequence
	width, height    int
	chroma           int
	progressive      bool
	mpeg2            bool
	intraMatrix      [64]int
	interMatrix      [64]int
	chromaIntra      [64]int
	chromaInter      [64]int
	altScan          bool
	intraVLC         bool
	dcPrecision      int
	qscaleType       bool
	pictureStructure int
	framePredDCT     bool
	cur              *mpeg2Picture
	lastDC           [3]int
	qscale           int
}

type mpeg2Picture struct {
	frame MPEGFrame
	typ   byte
}

type mpeg2Bits struct {
	b []byte
	n int
}

func (r *mpeg2Bits) left() int { return len(r.b)*8 - r.n }
func (r *mpeg2Bits) show(n int) (uint32, error) {
	if n < 0 || n > 24 || r.left() < n {
		return 0, mpeg2Err(MPEG2Truncated, -1, "truncated video bitstream")
	}
	var v uint32
	for i := 0; i < n; i++ {
		v = (v << 1) | uint32((r.b[(r.n+i)>>3]>>uint(7-((r.n+i)&7)))&1)
	}
	return v, nil
}
func (r *mpeg2Bits) get(n int) (uint32, error) {
	v, e := r.show(n)
	if e == nil {
		r.n += n
	}
	return v, e
}
func (r *mpeg2Bits) bit() (uint32, error) { return r.get(1) }
func (r *mpeg2Bits) skip(n int) error     { _, e := r.get(n); return e }
func (r *mpeg2Bits) signed(n int) (int, error) {
	v, e := r.get(n)
	if e != nil {
		return 0, e
	}
	if n == 0 {
		return 0, nil
	}
	if v&(1<<uint(n-1)) != 0 {
		return int(v), nil
	}
	return int(v) - (1 << uint(n)) + 1, nil
}
func (r *mpeg2Bits) xbits(n int) (int, error) { return r.signed(n) }
func mpeg2StopBits(r *mpeg2Bits) error {
	for {
		b, e := r.bit()
		if e != nil {
			return e
		}
		if b == 0 {
			return nil
		}
		if e = r.skip(8); e != nil {
			return e
		}
	}
}

var mpeg2DefaultIntra = [...]int{8, 16, 19, 22, 26, 27, 29, 34, 16, 16, 22, 24, 27, 29, 34, 37, 19, 22, 26, 27, 29, 34, 34, 38, 22, 22, 26, 27, 29, 34, 37, 40, 22, 26, 27, 29, 32, 35, 40, 48, 26, 27, 29, 32, 35, 40, 48, 58, 26, 27, 29, 34, 38, 46, 56, 69, 27, 29, 35, 38, 46, 56, 69, 83}
var mpeg2DefaultInter = [...]int{16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16}
var mpeg2Zigzag = [...]int{0, 1, 8, 16, 9, 2, 3, 10, 17, 24, 32, 25, 18, 11, 4, 5, 12, 19, 26, 33, 40, 48, 41, 34, 27, 20, 13, 6, 7, 14, 21, 28, 35, 42, 49, 56, 57, 50, 43, 36, 29, 22, 15, 23, 30, 37, 44, 51, 58, 59, 52, 45, 38, 31, 39, 46, 53, 60, 61, 54, 47, 55, 62, 63}
var mpeg2AltScan = [...]int{0, 8, 16, 24, 1, 9, 2, 10, 17, 25, 32, 40, 48, 56, 57, 49, 41, 33, 26, 18, 3, 11, 4, 12, 19, 27, 34, 42, 50, 58, 35, 43, 51, 59, 20, 28, 5, 13, 6, 14, 21, 29, 36, 44, 52, 60, 37, 45, 53, 61, 22, 30, 7, 15, 23, 31, 38, 46, 54, 62, 39, 47, 55, 63}

var mpeg2DCLumCode = [...]uint16{4, 0, 1, 5, 6, 14, 30, 62, 126, 254, 510, 511}
var mpeg2DCLumLen = [...]uint8{3, 2, 2, 3, 3, 4, 5, 6, 7, 8, 9, 9}
var mpeg2DCChrCode = [...]uint16{0, 1, 2, 6, 14, 30, 62, 126, 254, 510, 1022, 1023}
var mpeg2DCChrLen = [...]uint8{2, 2, 2, 3, 4, 5, 6, 7, 8, 9, 10, 10}
var mpeg2MBAddrCode = [...]uint16{1, 3, 2, 3, 2, 3, 2, 7, 6, 11, 10, 9, 8, 7, 6, 23, 22, 21, 20, 19, 18, 35, 34, 33, 32, 31, 30, 29, 28, 27, 26, 25, 24, 8, 15, 0}
var mpeg2MBAddrLen = [...]uint8{1, 3, 3, 4, 4, 5, 5, 7, 7, 8, 8, 8, 8, 8, 8, 10, 10, 10, 10, 10, 10, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 8}

// MPEG-2 DCT coefficient VLC. Entries 0..110 are (run,level); 111 is EOB
// and 112 is ESC. The table is searched incrementally, avoiding a large
// mutable lookup allocation while keeping all reads bounded.
var mpeg2RLCode = [...]uint16{2, 6, 7, 28, 29, 5, 4, 123, 124, 35, 34, 250, 251, 254, 255, 31, 30, 29, 28, 27, 26, 25, 24, 23, 22, 21, 20, 19, 18, 17, 16, 24, 23, 22, 21, 20, 19, 17, 16, 2, 6, 121, 39, 32, 22, 21, 31, 30, 29, 28, 27, 26, 25, 19, 18, 17, 16, 5, 7, 252, 12, 20, 7, 38, 28, 19, 6, 253, 18, 7, 4, 18, 6, 30, 20, 4, 21, 5, 17, 120, 17, 122, 16, 33, 26, 37, 25, 36, 24, 5, 23, 7, 22, 13, 21, 31, 26, 25, 23, 22, 31, 30, 29, 28, 27, 31, 30, 29, 28, 27, 1, 6}
var mpeg2RLLen = [...]uint8{2, 3, 4, 5, 5, 6, 6, 7, 7, 8, 8, 8, 8, 8, 8, 14, 14, 14, 14, 14, 14, 14, 14, 14, 14, 14, 14, 14, 14, 14, 14, 15, 15, 15, 15, 15, 15, 16, 16, 3, 5, 7, 8, 8, 13, 13, 15, 15, 15, 15, 15, 15, 15, 16, 16, 16, 16, 5, 7, 8, 10, 13, 5, 8, 12, 13, 6, 8, 6, 9, 12, 7, 9, 12, 16, 7, 12, 7, 12, 13, 8, 16, 8, 16, 9, 16, 9, 16, 10, 16, 12, 12, 12, 13, 13, 13, 13, 16, 16, 16, 16, 16, 16, 16, 6}
var mpeg2RLLevel = [...]int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32, 33, 34, 35, 36, 37, 38, 39, 40, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 1, 2, 3, 4, 5, 1, 2, 3, 4, 1, 2, 3, 1, 2, 3, 1, 2, 3, 1, 2, 1, 2, 1, 2, 1, 2, 1, 2, 1, 2, 1, 2, 1, 2, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1}
var mpeg2RLRun = [...]int{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 2, 2, 2, 2, 2, 3, 3, 3, 3, 4, 4, 4, 5, 5, 5, 6, 6, 6, 7, 7, 8, 8, 9, 9, 10, 10, 11, 11, 12, 12, 13, 13, 14, 14, 15, 15, 16, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31}

func newMPEG2Real() *mpeg2RealDecoder {
	d := &mpeg2RealDecoder{progressive: true, chroma: 1, pictureStructure: 3, framePredDCT: true}
	d.loadMatrices()
	return d
}
func (d *mpeg2RealDecoder) loadMatrices() {
	d.intraMatrix = mpeg2DefaultIntra
	d.interMatrix = mpeg2DefaultInter
	d.chromaIntra = mpeg2DefaultIntra
	d.chromaInter = mpeg2DefaultInter
}
func (d *mpeg2RealDecoder) fail(k MPEG2ErrorKind, msg string) error { return mpeg2Err(k, -1, msg) }

func (d *mpeg2RealDecoder) process(code byte, payload []byte, pts uint64, hasPTS bool, dts uint64, hasDTS bool) (*MPEGFrame, error) {
	if code == 0 || code == 0xb3 || code == 0xb7 {
		f, e := d.finish()
		if e != nil {
			return nil, e
		}
		if code == 0xb7 {
			return f, nil
		}
		if code == 0xb3 {
			if e = d.sequenceHeader(payload); e != nil {
				return nil, e
			}
			return f, nil
		}
	}
	switch {
	case code == 0xb5:
		return nil, d.extension(payload)
	case code == 0:
		return nil, d.pictureHeader(payload, pts, hasPTS, dts, hasDTS)
	case code >= 1 && code <= 0xaf:
		if d.cur == nil {
			return nil, nil
		}
		return nil, d.slice(int(code)-1, payload)
	}
	return nil, nil
}
func (d *mpeg2RealDecoder) finish() (*MPEGFrame, error) {
	if d.cur == nil {
		return nil, nil
	}
	f := d.cur.frame
	d.cur = nil
	return &f, nil
}
func (d *mpeg2RealDecoder) sequenceHeader(p []byte) error {
	if len(p) < 8 {
		return d.fail(MPEG2Truncated, "short sequence header")
	}
	q := mpeg2Bits{b: p}
	w, e := q.get(12)
	if e != nil {
		return e
	}
	h, e := q.get(12)
	if e != nil {
		return e
	}
	if w == 0 || h == 0 {
		return d.fail(MPEG2Malformed, "zero sequence dimensions")
	}
	aspect, e := q.get(4)
	if e != nil {
		return e
	}
	rate, e := q.get(4)
	if e != nil || rate == 0 || rate >= 9 {
		return d.fail(MPEG2Malformed, "invalid frame-rate code")
	}
	bitrate, e := q.get(18)
	if e != nil {
		return e
	}
	mark, e := q.bit()
	if e != nil || mark == 0 {
		return d.fail(MPEG2Malformed, "missing sequence marker")
	}
	vbv, e := q.get(10)
	if e != nil {
		return e
	}
	constrained, e := q.bit()
	if e != nil {
		return e
	}
	d.width, d.height = int(w), int(h)
	d.mpeg2 = false
	d.progressive = true
	d.chroma = 1
	d.loadMatrices()
	load, e := q.bit()
	if e != nil {
		return e
	}
	if load != 0 {
		if e = d.readMatrix(&q, &d.intraMatrix); e != nil {
			return e
		}
	}
	load, e = q.bit()
	if e != nil {
		return e
	}
	if load != 0 {
		if e = d.readMatrix(&q, &d.interMatrix); e != nil {
			return e
		}
	}
	d.chromaIntra = d.intraMatrix
	d.chromaInter = d.interMatrix
	d.sequence = &MPEGSequence{Width: w, Height: h, AspectRatioCode: byte(aspect), FrameRateCode: byte(rate), FrameRateNumerator: mpegFrameRates[rate].n, FrameRateDenominator: mpegFrameRates[rate].d, BitRate: uint32(bitrate), VBVBufferSize: uint16(vbv), Constrained: constrained != 0}
	return nil
}
func (d *mpeg2RealDecoder) readMatrix(r *mpeg2Bits, m *[64]int) error {
	for i := 0; i < 64; i++ {
		v, e := r.get(8)
		if e != nil {
			return e
		}
		m[mpeg2Zigzag[i]] = int(v)
	}
	return nil
}
func (d *mpeg2RealDecoder) extension(p []byte) error {
	r := mpeg2Bits{b: p}
	id, e := r.get(4)
	if e != nil {
		return e
	}
	switch id {
	case 1:
		return d.sequenceExtension(&r)
	case 3:
		return d.quantExtension(&r)
	case 8:
		return d.pictureExtension(&r)
	}
	return nil
}
func (d *mpeg2RealDecoder) sequenceExtension(r *mpeg2Bits) error {
	if e := r.skip(1); e != nil {
		return e
	}
	_, e := r.get(3)
	if e != nil {
		return e
	}
	_, e = r.get(4)
	if e != nil {
		return e
	}
	ps, e := r.bit()
	if e != nil {
		return e
	}
	cf, e := r.get(2)
	if e != nil {
		return e
	}
	he, e := r.get(2)
	if e != nil {
		return e
	}
	ve, e := r.get(2)
	if e != nil {
		return e
	}
	br, e := r.get(12)
	if e != nil {
		return e
	}
	mark, e := r.bit()
	if e != nil || mark == 0 {
		return d.fail(MPEG2Malformed, "missing sequence extension marker")
	}
	vbv, e := r.get(8)
	if e != nil {
		return e
	}
	_, e = r.bit()
	if e != nil {
		return e
	}
	_, e = r.get(2)
	if e != nil {
		return e
	}
	_, e = r.get(5)
	if e != nil {
		return e
	}
	d.mpeg2 = true
	d.progressive = ps != 0
	d.chroma = int(cf)
	if !d.progressive || d.chroma != 1 {
		return d.fail(MPEG2Unsupported, "only progressive 4:2:0 MPEG-2 is supported")
	}
	d.width |= int(he) << 12
	d.height |= int(ve) << 12
	if d.sequence != nil {
		d.sequence.Width = uint32(d.width)
		d.sequence.Height = uint32(d.height)
		d.sequence.MPEG2 = true
		d.sequence.BitRate = (d.sequence.BitRate << 12) | uint32(br)
		d.sequence.VBVBufferSize = uint16(vbv)
		d.sequence.MPEG2 = true
	}
	return nil
}
func (d *mpeg2RealDecoder) quantExtension(r *mpeg2Bits) error {
	var flags [4]bool
	for i := range flags {
		v, e := r.bit()
		if e != nil {
			return e
		}
		flags[i] = v != 0
	}
	for i := range flags {
		if flags[i] {
			m := &d.intraMatrix
			if i == 1 {
				m = &d.interMatrix
			}
			if i == 2 {
				m = &d.chromaIntra
			}
			if i == 3 {
				m = &d.chromaInter
			}
			if e := d.readMatrix(r, m); e != nil {
				return e
			}
		}
	}
	return nil
}
func (d *mpeg2RealDecoder) pictureExtension(r *mpeg2Bits) error {
	for range 4 {
		v, e := r.get(4)
		if e != nil {
			return e
		}
		if v == 0 {
			return d.fail(MPEG2Malformed, "zero picture f_code")
		}
	}
	v, e := r.get(2)
	if e != nil {
		return e
	}
	d.dcPrecision = int(v)
	v, e = r.get(2)
	if e != nil {
		return e
	}
	d.pictureStructure = int(v)
	if d.pictureStructure != 3 {
		return d.fail(MPEG2Unsupported, "only progressive frame pictures are supported")
	}
	var progressiveFrame bool
	for i := range 9 {
		v, e = r.bit()
		if e != nil {
			return e
		}
		switch i {
		case 3:
			d.qscaleType = v != 0
		case 4:
			d.intraVLC = v != 0
		case 5:
			d.altScan = v != 0
		case 8:
			progressiveFrame = v != 0
		}
	}
	if !progressiveFrame {
		return d.fail(MPEG2Unsupported, "interlaced picture is unsupported")
	}
	return nil
}
func (d *mpeg2RealDecoder) pictureHeader(p []byte, pts uint64, hasPTS bool, dts uint64, hasDTS bool) error {
	r := mpeg2Bits{b: p}
	tr, e := r.get(10)
	if e != nil {
		return e
	}
	typ, e := r.get(3)
	if e != nil {
		return e
	}
	if typ < 1 || typ > 3 {
		return d.fail(MPEG2Malformed, "invalid picture coding type")
	}
	if _, e = r.get(16); e != nil {
		return e
	}
	if !d.mpeg2 {
		if typ == 2 {
			if e = r.skip(1); e != nil {
				return e
			}
			if _, e = r.get(3); e != nil {
				return e
			}
		} else if typ == 3 {
			if e = r.skip(1); e != nil {
				return e
			}
			if _, e = r.get(3); e != nil {
				return e
			}
			if e = r.skip(1); e != nil {
				return e
			}
			if _, e = r.get(3); e != nil {
				return e
			}
		}
	}
	if e = mpeg2StopBits(&r); e != nil {
		return e
	}
	if typ != 1 {
		return d.fail(MPEG2Unsupported, "inter-picture reconstruction is not available")
	}
	d.pictureStructure = 3
	d.framePredDCT = true
	f, e := newMPEGFrame(d.width, d.height, d.chroma)
	if e != nil {
		return e
	}
	f.PictureType = byte(typ)
	f.Progressive = d.progressive
	f.PTS = pts
	f.HasPTS = hasPTS
	f.DTS = dts
	f.HasDTS = hasDTS
	d.cur = &mpeg2Picture{frame: f, typ: byte(typ)}
	d.lastDC = [3]int{}
	d.qscale = 8
	_ = tr
	return nil
}

func newMPEGFrame(w, h, chroma int) (MPEGFrame, error) {
	if w <= 0 || h <= 0 {
		return MPEGFrame{}, mpeg2Err(MPEG2Range, -1, "invalid frame dimensions")
	}
	fmt := MPEGYUV420
	cw, ch := 1, 1
	switch chroma {
	case 2:
		fmt = MPEGYUV422
		ch = 0
	case 3:
		fmt = MPEGYUV444
		cw = 0
		ch = 0
	case 1:
	default:
		return MPEGFrame{}, mpeg2Err(MPEG2Unsupported, -1, "unsupported chroma format")
	}
	uw, uh := (w+((1<<cw)-1))>>cw, (h+((1<<ch)-1))>>ch
	f := MPEGFrame{Width: uint32(w), Height: uint32(h), Format: fmt}
	f.Y = MPEGPixelPlane{Data: make([]byte, w*h), Width: w, Height: h, Stride: w}
	f.U = MPEGPixelPlane{Data: make([]byte, uw*uh), Width: uw, Height: uh, Stride: uw}
	f.V = MPEGPixelPlane{Data: make([]byte, uw*uh), Width: uw, Height: uh, Stride: uw}
	for i := range f.U.Data {
		f.U.Data[i] = 128
		f.V.Data[i] = 128
	}
	f.Planes = [3]MPEGPixelPlane{f.Y, f.U, f.V}
	return f, nil
}

func (d *mpeg2RealDecoder) slice(row int, p []byte) error {
	if d.cur == nil {
		return nil
	}
	if row*16 >= d.height {
		return d.fail(MPEG2Range, "slice row exceeds frame")
	}
	r := mpeg2Bits{b: p}
	q, e := r.get(5)
	if e != nil {
		return e
	}
	if q == 0 {
		return d.fail(MPEG2Malformed, "zero slice quantizer")
	}
	d.qscale = int(q)
	if d.qscaleType {
		v := mpeg2NonlinearQScale[d.qscale]
		d.qscale = int(v)
	}
	if e = mpeg2StopBits(&r); e != nil {
		return e
	}
	inc, e := d.readMBAddr(&r)
	if e != nil {
		return e
	}
	x := inc
	mbW := (d.width + 15) / 16
	for x < mbW {
		if inc != 0 {
			return d.fail(MPEG2Unsupported, "skipped intra macroblock")
		}
		if e = d.decodeIntraMB(&r, x, row); e != nil {
			return e
		}
		x++
		inc, e = d.readMBAddr(&r)
		if e != nil {
			if errorsIsMPEGTruncated(e) {
				return nil
			}
			return e
		}
	}
	return nil
}
func errorsIsMPEGTruncated(e error) bool { return e != nil && mpeg2ErrorKind(e) == MPEG2Truncated }
func mpeg2ErrorKind(e error) MPEG2ErrorKind {
	if x, ok := e.(*MPEG2Error); ok {
		return x.Kind
	}
	return 0
}
func (d *mpeg2RealDecoder) readMBAddr(r *mpeg2Bits) (int, error) {
	code := uint32(0)
	for n := 1; n <= 11; n++ {
		b, e := r.bit()
		if e != nil {
			return 0, e
		}
		code = (code << 1) | b
		for i := 0; i < len(mpeg2MBAddrCode); i++ {
			if int(mpeg2MBAddrLen[i]) == n && uint32(mpeg2MBAddrCode[i]) == code {
				if i == 33 {
					return 33, nil
				}
				if i == 34 {
					return 34, nil
				}
				if i == 35 {
					return 35, nil
				}
				return i, nil
			}
		}
	}
	return 0, d.fail(MPEG2Malformed, "invalid macroblock address increment")
}
func (d *mpeg2RealDecoder) decodeIntraMB(r *mpeg2Bits, mbx, mby int) error {
	v, e := r.bit()
	if e != nil {
		return e
	}
	if v == 0 {
		v, e = r.bit()
		if e != nil {
			return e
		}
		if v == 0 {
			return d.fail(MPEG2Malformed, "invalid intra macroblock type")
		}
		v, e = r.get(5)
		if e != nil {
			return e
		}
		if v == 0 {
			return d.fail(MPEG2Malformed, "zero macroblock quantizer")
		}
		d.qscale = int(v)
	}
	var blocks [6][64]int
	for i := 0; i < 6; i++ {
		comp := 0
		if i >= 4 {
			comp = i - 3
		}
		if e = d.decodeBlock(r, &blocks[i], comp); e != nil {
			return e
		}
	}
	d.putBlocks(mbx, mby, &blocks)
	return nil
}
func (d *mpeg2RealDecoder) decodeBlock(r *mpeg2Bits, b *[64]int, comp int) error {
	tabC, tabL := mpeg2DCLumCode, mpeg2DCLumLen
	if comp != 0 {
		tabC, tabL = mpeg2DCChrCode, mpeg2DCChrLen
	}
	size, e := readCode(r, tabC[:], tabL[:])
	if e != nil {
		return e
	}
	diff := 0
	if size > 0 {
		diff, e = r.xbits(size)
		if e != nil {
			return e
		}
	}
	dc := d.lastDC[comp] + diff
	d.lastDC[comp] = dc
	mat := d.intraMatrix[:]
	if comp != 0 {
		mat = d.chromaIntra[:]
	}
	b[0] = clipMPEG12(dc * (1 << uint(3-d.dcPrecision)))
	pos := 0
	for pos < 63 {
		v, e := r.show(2)
		if e != nil {
			return e
		}
		if v == 2 {
			r.n += 2
			break
		}
		code, run, lev, e := d.readRL(r)
		if e != nil {
			return e
		}
		if code == 111 {
			break
		}
		pos += run
		if pos >= 64 {
			return d.fail(MPEG2Malformed, "DCT run exceeds block")
		}
		level := lev * d.qscale * mat[mpeg2Zigzag[pos]] >> 4
		if level == 0 {
			level = 1
		}
		sg, e := r.bit()
		if e != nil {
			return e
		}
		if sg != 0 {
			level = -level
		}
		b[mpeg2Zigzag[pos]] = clipMPEG12(level)
		pos++
	}
	return nil
}
func readCode(r *mpeg2Bits, codes []uint16, lens []uint8) (int, error) {
	var code uint32
	for n := 1; n <= 10; n++ {
		b, e := r.bit()
		if e != nil {
			return 0, e
		}
		code = (code << 1) | b
		for i := range codes {
			if int(lens[i]) == n && uint32(codes[i]) == code {
				return i, nil
			}
		}
	}
	return 0, mpeg2Err(MPEG2Malformed, -1, "invalid DC VLC")
}
func (d *mpeg2RealDecoder) readRL(r *mpeg2Bits) (int, int, int, error) {
	var code uint32
	for n := 1; n <= 16; n++ {
		b, e := r.bit()
		if e != nil {
			return 0, 0, 0, e
		}
		code = (code << 1) | b
		for i := range mpeg2RLCode {
			if int(mpeg2RLLen[i]) == n && uint32(mpeg2RLCode[i]) == code {
				if i == 111 {
					return i, 0, 0, nil
				}
				if i == 112 {
					run, e := r.get(6)
					if e != nil {
						return 0, 0, 0, e
					}
					lv, e := r.signed(12)
					if e != nil {
						return 0, 0, 0, e
					}
					return i, int(run) + 1, lv, nil
				}
				return i, mpeg2RLRun[i], mpeg2RLLevel[i], nil
			}
		}
	}
	return 0, 0, 0, d.fail(MPEG2Malformed, "invalid DCT VLC")
}
func clipMPEG12(v int) int {
	if v < -2048 {
		return -2048
	}
	if v > 2047 {
		return 2047
	}
	return v
}

var mpeg2NonlinearQScale = [32]int{0, 1, 2, 3, 4, 5, 6, 7, 8, 10, 12, 14, 16, 18, 20, 22, 24, 28, 32, 36, 40, 44, 48, 52, 56, 64, 72, 80, 88, 96, 104, 112}

func (d *mpeg2RealDecoder) putBlocks(mbx, mby int, b *[6][64]int) {
	for i := 0; i < 4; i++ {
		x := mbx*16 + (i&1)*8
		y := mby*16 + (i>>1)*8
		mpeg2IDCTPut(d.cur.frame.Y.Data, d.cur.frame.Y.Stride, x, y, &b[i])
	}
	x, y := mbx*d.cur.frame.U.Width/((d.width+15)/16), mby*d.cur.frame.U.Height/((d.height+15)/16)
	mpeg2IDCTPut(d.cur.frame.U.Data, d.cur.frame.U.Stride, x, y, &b[4])
	mpeg2IDCTPut(d.cur.frame.V.Data, d.cur.frame.V.Stride, x, y, &b[5])
}
func mpeg2IDCTPut(dst []byte, stride, x, y int, c *[64]int) {
	for yy := 0; yy < 8; yy++ {
		py := y + yy
		if py >= stride*0+len(dst)/stride {
			return
		}
		for xx := 0; xx < 8; xx++ {
			px := x + xx
			if px >= stride {
				break
			}
			var sum float64
			for v := 0; v < 8; v++ {
				for u := 0; u < 8; u++ {
					cu, cv := 1.0, 1.0
					if u == 0 {
						cu = 1 / math.Sqrt2
					}
					if v == 0 {
						cv = 1 / math.Sqrt2
					}
					sum += cu * cv * float64(c[v*8+u]) * math.Cos(math.Pi*float64((2*xx+1)*u)/16) * math.Cos(math.Pi*float64((2*yy+1)*v)/16)
				}
			}
			z := int(math.Round(sum/4 + 128))
			if z < 0 {
				z = 0
			}
			if z > 255 {
				z = 255
			}
			dst[py*stride+px] = byte(z)
		}
	}
}
