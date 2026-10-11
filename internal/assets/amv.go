package assets

import (
	"encoding/binary"
	"errors"
	"fmt"
	"image"
)

const (
	amvMaxPixels  = 64 * 1024 * 1024
	amvMaxPackets = 1_000_000
)

// AlphaEncoding identifies the alpha plane representation in an AlphaMovie.
type AlphaEncoding uint8

const (
	AlphaDeflate AlphaEncoding = iota
	AlphaDCT
	// Spelling aliases retained for callers that prefer encoding-qualified names.
	AlphaEncodingDeflate  = AlphaDeflate
	AlphaEncodingDCT      = AlphaDCT
	AlphaEncodingDeflated = AlphaDeflate
)

func (e AlphaEncoding) String() string {
	if e == AlphaDCT {
		return "DCT"
	}
	return "Deflate"
}

// AMVErrorKind classifies malformed, truncated, and range errors.
type AMVErrorKind uint8

const (
	AMVMalformed AMVErrorKind = iota + 1
	AMVTruncated
	AMVRange
)

// AMVError is returned for invalid AlphaMovie data. Offset is the byte offset
// at which a container error was found, when known.
type AMVError struct {
	Kind    AMVErrorKind
	Offset  int
	Message string
	Err     error
}

func (e *AMVError) Error() string {
	if e.Offset >= 0 {
		return fmt.Sprintf("AlphaMovie %s at %#x: %s", e.kindString(), e.Offset, e.Message)
	}
	return "AlphaMovie " + e.kindString() + ": " + e.Message
}
func (e *AMVError) Unwrap() error { return e.Err }
func (e *AMVError) kindString() string {
	switch e.Kind {
	case AMVTruncated:
		return "truncated"
	case AMVRange:
		return "range error"
	default:
		return "malformed"
	}
}

var ErrAMVMalformed = errors.New("malformed AlphaMovie")
var ErrAMVTruncated = errors.New("truncated AlphaMovie")
var ErrAMVRange = errors.New("AlphaMovie range error")

func amvErr(kind AMVErrorKind, at int, msg string) error {
	var base error
	switch kind {
	case AMVTruncated:
		base = ErrAMVTruncated
	case AMVRange:
		base = ErrAMVRange
	default:
		base = ErrAMVMalformed
	}
	return &AMVError{Kind: kind, Offset: at, Message: msg, Err: base}
}

// Header is the fixed AlphaMovie header.
type Header struct {
	Revision      uint32
	HeaderSize    uint32
	FrameCount    uint32
	FPSScale      uint32
	FPSRate       uint32
	Width, Height uint16
	Attributes    uint8
	AlphaEncoding AlphaEncoding
}

// Packet indexes one rectangle payload. Byte ranges are half-open and refer
// directly to Movie.Data; accessors are preferred by callers.
type Packet struct {
	Offset                   int
	Sequence                 uint32
	Left, Top                int16
	Width, Height            uint16
	AlphaStart, AlphaEnd     int
	EntropyStart, EntropyEnd int
}

// Image is a decoded rectangle of tightly packed, non-premultiplied RGBA bytes.
type Image struct {
	Width, Height uint32
	RGBA          []byte
}

// RGBAImage returns a zero-copy NRGBA view of the Image's straight-alpha bytes.
func (i *Image) RGBAImage() *image.NRGBA {
	if i == nil {
		return nil
	}
	return &image.NRGBA{Pix: i.RGBA, Stride: int(i.Width) * 4, Rect: image.Rect(0, 0, int(i.Width), int(i.Height))}
}

// Movie is a checked AlphaMovie container. Data is retained so packet payload
// accessors return zero-copy slices.
type Movie struct {
	Header       Header
	Quantization [][]byte
	Packets      []Packet
	Data         []byte
}

// ParseAMV parses a complete AJPM AlphaMovie resource without allocating from
// any length field until that length has been checked against the input.
func ParseAMV(data []byte) (*Movie, error) {
	r := amvReader{b: data}
	if x, err := r.take(4); err != nil {
		return nil, err
	} else if string(x) != "AJPM" {
		return nil, amvErr(AMVMalformed, 0, "invalid signature")
	}
	fileSize, err := r.u32()
	if err != nil {
		return nil, err
	}
	if uint64(fileSize) != uint64(len(data)) {
		return nil, amvErr(AMVMalformed, 4, "file size mismatch")
	}
	revision, err := r.u32()
	if err != nil {
		return nil, err
	}
	if revision != 0 {
		return nil, amvErr(AMVMalformed, 8, "unsupported revision")
	}
	headerSize, err := r.u32()
	if err != nil {
		return nil, err
	}
	if _, err = r.take(4); err != nil {
		return nil, err
	}
	frameCount, err := r.u32()
	if err != nil {
		return nil, err
	}
	fpsScale, err := r.u32()
	if err != nil {
		return nil, err
	}
	fpsRate, err := r.u32()
	if err != nil {
		return nil, err
	}
	width, err := r.u16()
	if err != nil {
		return nil, err
	}
	height, err := r.u16()
	if err != nil {
		return nil, err
	}
	attrs, err := r.byte()
	if err != nil {
		return nil, err
	}
	if _, err = r.take(3); err != nil {
		return nil, err
	}
	if frameCount == 0 {
		return nil, amvErr(AMVMalformed, 24, "movie has no frames")
	}
	if fpsScale == 0 || fpsRate == 0 {
		return nil, amvErr(AMVMalformed, 28, "invalid frame rate")
	}
	if width == 0 || height == 0 || uint64(width)*uint64(height) > amvMaxPixels {
		return nil, amvErr(AMVMalformed, 32, "invalid canvas dimensions")
	}
	var enc AlphaEncoding
	switch {
	case attrs&1 != 0:
		enc = AlphaDCT
	case attrs&2 != 0:
		enc = AlphaDeflate
	default:
		return nil, amvErr(AMVMalformed, 36, "invalid attributes")
	}
	tableCount := 2
	if enc == AlphaDCT {
		tableCount = 3
	}
	if uint64(headerSize) != uint64(40+64*tableCount) {
		return nil, amvErr(AMVMalformed, 12, "invalid quantization table size")
	}
	q := make([][]byte, tableCount)
	for n := range q {
		x, e := r.take(64)
		if e != nil {
			return nil, e
		}
		q[n] = x
	}
	packets := make([]Packet, 0, 16)
	for r.pos < len(data) {
		if len(packets) >= amvMaxPackets {
			return nil, amvErr(AMVMalformed, r.pos, "packet limit exceeded")
		}
		off := r.pos
		tag, e := r.take(4)
		if e != nil {
			return nil, e
		}
		if string(tag) != "FRAM" {
			return nil, amvErr(AMVMalformed, off, "invalid packet tag")
		}
		length, e := r.u32()
		if e != nil {
			return nil, e
		}
		if uint64(length) > uint64(len(data)-r.pos) {
			return nil, amvErr(AMVTruncated, off, "packet exceeds file")
		}
		base := r.pos
		end := base + int(length)
		p := amvReader{b: data[base:end]}
		seq, e := p.u32()
		if e != nil {
			return nil, e
		}
		l, e := p.u16()
		if e != nil {
			return nil, e
		}
		t, e := p.u16()
		if e != nil {
			return nil, e
		}
		w, e := p.u16()
		if e != nil {
			return nil, e
		}
		h, e := p.u16()
		if e != nil {
			return nil, e
		}
		if w%16 != 0 || h%16 != 0 {
			return nil, amvErr(AMVMalformed, off, "rectangle is not 16-pixel aligned")
		}
		if (w == 0) != (h == 0) {
			return nil, amvErr(AMVMalformed, off, "invalid empty rectangle")
		}
		if uint64(w)*uint64(h) > amvMaxPixels {
			return nil, amvErr(AMVMalformed, off, "rectangle exceeds pixel limit")
		}
		pkt := Packet{Offset: off, Sequence: seq, Left: int16(l), Top: int16(t), Width: w, Height: h, AlphaStart: -1, AlphaEnd: -1, EntropyStart: base + p.pos, EntropyEnd: end}
		if enc == AlphaDeflate {
			n, e := p.u32()
			if e != nil {
				return nil, e
			}
			if uint64(n) > uint64(len(p.b)-p.pos) {
				return nil, amvErr(AMVTruncated, base+p.pos, "alpha payload exceeds packet")
			}
			pkt.AlphaStart = base + p.pos
			p.pos += int(n)
			pkt.AlphaEnd = base + p.pos
			pkt.EntropyStart = base + p.pos
		}
		if p.pos > end {
			return nil, amvErr(AMVTruncated, base+p.pos, "packet payload truncated")
		}
		packets = append(packets, pkt)
		r.pos = end
	}
	if len(packets) == 0 {
		return nil, amvErr(AMVMalformed, r.pos, "movie has no packets")
	}
	return &Movie{Header: Header{Revision: revision, HeaderSize: headerSize, FrameCount: frameCount, FPSScale: fpsScale, FPSRate: fpsRate, Width: width, Height: height, Attributes: attrs, AlphaEncoding: enc}, Quantization: q, Packets: packets, Data: data}, nil
}
func Parse(data []byte) (*Movie, error) { return ParseAMV(data) }
func (m *Movie) Entropy(packet int) ([]byte, error) {
	if m == nil || packet < 0 || packet >= len(m.Packets) {
		return nil, amvErr(AMVRange, -1, "packet index out of range")
	}
	p := m.Packets[packet]
	if p.EntropyStart < 0 || p.EntropyEnd > len(m.Data) || p.EntropyStart > p.EntropyEnd {
		return nil, amvErr(AMVMalformed, p.Offset, "invalid entropy range")
	}
	return m.Data[p.EntropyStart:p.EntropyEnd], nil
}
func (m *Movie) CompressedAlpha(packet int) ([]byte, error) {
	if m == nil || packet < 0 || packet >= len(m.Packets) {
		return nil, amvErr(AMVRange, -1, "packet index out of range")
	}
	p := m.Packets[packet]
	if p.AlphaStart < 0 {
		return nil, nil
	}
	if p.AlphaEnd > len(m.Data) || p.AlphaStart > p.AlphaEnd {
		return nil, amvErr(AMVMalformed, p.Offset, "invalid alpha range")
	}
	return m.Data[p.AlphaStart:p.AlphaEnd], nil
}

// Reader methods keep all cursor arithmetic bounded.
type amvReader struct {
	b   []byte
	pos int
}

func (r *amvReader) take(n int) ([]byte, error) {
	if n < 0 || n > len(r.b)-r.pos {
		return nil, amvErr(AMVTruncated, r.pos, "unexpected end of input")
	}
	x := r.b[r.pos : r.pos+n]
	r.pos += n
	return x, nil
}
func (r *amvReader) byte() (byte, error) {
	x, e := r.take(1)
	if e != nil {
		return 0, e
	}
	return x[0], nil
}
func (r *amvReader) u16() (uint16, error) {
	x, e := r.take(2)
	if e != nil {
		return 0, e
	}
	return binary.LittleEndian.Uint16(x), nil
}
func (r *amvReader) u32() (uint32, error) {
	x, e := r.take(4)
	if e != nil {
		return 0, e
	}
	return binary.LittleEndian.Uint32(x), nil
}
