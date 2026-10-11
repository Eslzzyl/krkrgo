package assets

import (
	"errors"
	"fmt"
)

const (
	maxMPEG2Packets = 1_000_000
	maxMPEG2Input   = 64 * 1024 * 1024
	maxMPEG2Packet  = 16 * 1024 * 1024
)

// MPEG2ErrorKind classifies errors found while parsing an MPEG stream.
type MPEG2ErrorKind uint8

const (
	MPEG2Malformed MPEG2ErrorKind = iota + 1
	MPEG2Truncated
	MPEG2Range
	MPEG2Unsupported
)

// MPEG2Error preserves the byte offset and the reason for an MPEG error.
type MPEG2Error struct {
	Kind    MPEG2ErrorKind
	Offset  int
	Message string
	Err     error
}

func (e *MPEG2Error) Error() string {
	kind := "malformed"
	switch e.Kind {
	case MPEG2Truncated:
		kind = "truncated"
	case MPEG2Range:
		kind = "range error"
	case MPEG2Unsupported:
		kind = "unsupported"
	}
	if e.Offset >= 0 {
		return fmt.Sprintf("MPEG %s at %#x: %s", kind, e.Offset, e.Message)
	}
	return "MPEG " + kind + ": " + e.Message
}
func (e *MPEG2Error) Unwrap() error { return e.Err }

var (
	ErrMPEG2Malformed   = errors.New("malformed MPEG stream")
	ErrMPEG2Truncated   = errors.New("truncated MPEG stream")
	ErrMPEG2Range       = errors.New("MPEG stream range error")
	ErrMPEG2Unsupported = errors.New("unsupported MPEG stream feature")
	// Short aliases are useful to callers that do not distinguish MPEG-1/2.
	ErrMPEGMalformed   = ErrMPEG2Malformed
	ErrMPEGTruncated   = ErrMPEG2Truncated
	ErrMPEGRange       = ErrMPEG2Range
	ErrMPEGUnsupported = ErrMPEG2Unsupported
)

func mpeg2Err(kind MPEG2ErrorKind, off int, msg string) error {
	base := ErrMPEG2Malformed
	switch kind {
	case MPEG2Truncated:
		base = ErrMPEG2Truncated
	case MPEG2Range:
		base = ErrMPEG2Range
	case MPEG2Unsupported:
		base = ErrMPEG2Unsupported
	}
	return &MPEG2Error{Kind: kind, Offset: off, Message: msg, Err: base}
}

// MPEGContainer identifies the elementary/container syntax used by a stream.
type MPEGContainer uint8

const (
	MPEGElementary MPEGContainer = iota + 1
	MPEGTransport
	MPEGProgram
)

// MPEGFormat is an alias retained for code that calls the container a format.
type MPEGFormat = MPEGContainer

const (
	MPEGFormatES = MPEGElementary
	MPEGFormatTS = MPEGTransport
	MPEGFormatPS = MPEGProgram
)

// MPEGPacket is one PES packet or one raw elementary-stream start-code unit.
// Payload and Data refer to the same zero-copy byte range in the source stream.
type MPEGPacket struct {
	Offset       int
	StreamID     byte
	PID          uint16
	Continuity   byte
	Payload      []byte
	Data         []byte
	Raw          []byte
	PTS          uint64
	DTS          uint64
	HasPTS       bool
	HasDTS       bool
	RandomAccess bool
}

type MPEG2Packet = MPEGPacket

// MPEGStream is a bounded demux result. Packet payloads point into Source when
// possible; TS assembly necessarily owns a bounded copy for a PES payload.
type MPEGStream struct {
	Format   MPEGContainer
	Packets  []MPEGPacket
	Sequence *MPEGSequence
	Source   []byte
}

type MPEG2Stream = MPEGStream

// MPEGSequence describes the decoded sequence header and its frame timing.
type MPEGSequence struct {
	Width                uint32
	Height               uint32
	AspectRatioCode      byte
	FrameRateCode        byte
	FrameRateNumerator   uint32
	FrameRateDenominator uint32
	BitRate              uint32
	VBVBufferSize        uint16
	Constrained          bool
	MPEG2                bool
}

// MPEGErrorKind and MPEGError are generic spellings of the MPEG2 types.
type MPEGErrorKind = MPEG2ErrorKind
type MPEGError = MPEG2Error

const (
	MPEGMalformed   = MPEG2Malformed
	MPEGTruncated   = MPEG2Truncated
	MPEGRange       = MPEG2Range
	MPEGUnsupported = MPEG2Unsupported
)

// MPEGFrame contains decoded frame metadata and bounded planar YUV storage.
type MPEGFrame struct {
	Width, Height  uint32
	PTS, DTS       uint64
	HasPTS, HasDTS bool
	PictureType    byte
	Progressive    bool
	Format         MPEGPixelFormat
	Y, U, V        MPEGPixelPlane
	Planes         [3]MPEGPixelPlane
}

type MPEG2Frame = MPEGFrame

// MPEGPixelFormat is the chroma layout of an MPEG frame.
type MPEGPixelFormat uint8

const (
	MPEGYUV420 MPEGPixelFormat = iota + 1
	MPEGYUV422
	MPEGYUV444
)
const (
	MPEGPixelYUV420 = MPEGYUV420
	MPEGPixelYUV422 = MPEGYUV422
	MPEGPixelYUV444 = MPEGYUV444
)

// MPEGPixelPlane is a bounded, row-major plane. Stride is in bytes.
type MPEGPixelPlane struct {
	Data          []byte
	Width, Height int
	Stride        int
}

type MPEG2Plane = MPEGPixelPlane

var mpegFrameRates = [...]struct{ n, d uint32 }{
	{}, {24000, 1001}, {24, 1}, {25, 1}, {30000, 1001}, {30, 1},
	{50, 1}, {60000, 1001}, {60, 1},
}

func parseMPEGSequence(payload []byte, off int) (*MPEGSequence, error) {
	if len(payload) < 8 {
		return nil, mpeg2Err(MPEG2Truncated, off, "short sequence header")
	}
	width := uint32(payload[0])<<4 | uint32(payload[1]>>4)
	height := uint32(payload[1]&15)<<8 | uint32(payload[2])
	aspect, rate := payload[3]>>4, payload[3]&15
	if width == 0 || height == 0 {
		return nil, mpeg2Err(MPEG2Malformed, off, "zero sequence dimensions")
	}
	if uint64(width)*uint64(height) > maxPixels || width > maxImageWidth || height > maxImageHeight {
		return nil, mpeg2Err(MPEG2Range, off, "sequence dimensions exceed limits")
	}
	if rate == 0 || int(rate) >= len(mpegFrameRates) {
		return nil, mpeg2Err(MPEG2Malformed, off, "invalid frame-rate code")
	}
	bitrate := uint32(payload[4])<<10 | uint32(payload[5])<<2 | uint32(payload[6]>>6)
	vbv := uint16(payload[6]&31)<<5 | uint16(payload[7]>>3)
	return &MPEGSequence{Width: width, Height: height, AspectRatioCode: aspect,
		FrameRateCode: rate, FrameRateNumerator: mpegFrameRates[rate].n,
		FrameRateDenominator: mpegFrameRates[rate].d, BitRate: bitrate,
		VBVBufferSize: vbv, Constrained: payload[7]&4 != 0}, nil
}

func findMPEGStart(data []byte, from int) int {
	for i := from; i+3 < len(data); i++ {
		if data[i] == 0 && data[i+1] == 0 && data[i+2] == 1 {
			return i
		}
	}
	return -1
}

func appendMPEGPacket(out []MPEGPacket, p MPEGPacket) ([]MPEGPacket, error) {
	if len(out) >= maxMPEG2Packets {
		return nil, mpeg2Err(MPEG2Range, p.Offset, "too many MPEG packets")
	}
	return append(out, p), nil
}

func sequenceFromPackets(s *MPEGStream) error {
	for i := range s.Packets {
		p := &s.Packets[i]
		if p.StreamID == 0xb3 {
			seq, err := parseMPEGSequence(p.Payload, p.Offset)
			if err != nil {
				return err
			}
			s.Sequence = seq
		}
		if p.StreamID == 0xb5 && len(p.Payload) != 0 && p.Payload[0]>>4 == 1 && s.Sequence != nil {
			s.Sequence.MPEG2 = true
		}
	}
	return nil
}

// ParseMPEGES parses raw MPEG-1/2 elementary stream start-code units.
func ParseMPEGES(data []byte) (*MPEGStream, error) {
	if len(data) > maxMPEG2Input {
		return nil, mpeg2Err(MPEG2Range, 0, "elementary stream exceeds input limit")
	}
	first := findMPEGStart(data, 0)
	if first < 0 {
		if len(data) < 4 {
			return nil, mpeg2Err(MPEG2Truncated, 0, "missing start code")
		}
		return nil, mpeg2Err(MPEG2Malformed, 0, "missing start code")
	}
	if first > 0 {
		// MPEG ES permits zero-byte padding before the first code only.
		for _, b := range data[:first] {
			if b != 0 {
				return nil, mpeg2Err(MPEG2Malformed, 0, "data before first start code")
			}
		}
	}
	s := &MPEGStream{Format: MPEGElementary, Source: data}
	for at := first; at < len(data); {
		if at+4 > len(data) {
			return nil, mpeg2Err(MPEG2Truncated, at, "truncated start code")
		}
		next := findMPEGStart(data, at+4)
		end := len(data)
		if next >= 0 {
			end = next
		}
		p := MPEGPacket{Offset: at, StreamID: data[at+3], Raw: data[at:end],
			Payload: data[at+4 : end], Data: data[at+4 : end]}
		var err error
		s.Packets, err = appendMPEGPacket(s.Packets, p)
		if err != nil {
			return nil, err
		}
		at = end
	}
	if err := sequenceFromPackets(s); err != nil {
		return nil, err
	}
	return s, nil
}

func parseMPEGPTS5(p []byte, off int) (uint64, error) {
	if len(p) < 5 {
		return 0, mpeg2Err(MPEG2Truncated, off, "short PTS")
	}
	prefix := p[0] >> 4
	if prefix != 2 && prefix != 3 && prefix != 1 {
		return 0, mpeg2Err(MPEG2Malformed, off, "invalid PTS prefix")
	}
	if p[0]&1 == 0 || p[2]&1 == 0 || p[4]&1 == 0 {
		return 0, mpeg2Err(MPEG2Malformed, off, "invalid PTS marker")
	}
	v := (uint64((p[0]>>1)&7) << 30) | (uint64(p[1]) << 22) |
		(uint64((p[2]>>1)&0x7f) << 15) | (uint64(p[3]) << 7) |
		uint64((p[4]>>1)&0x7f)
	return v, nil
}

// ParseMPEGPTS parses the five-byte MPEG-1/2 90-kHz timestamp representation.
func ParseMPEGPTS(data []byte) (uint64, error) { return parseMPEGPTS5(data, 0) }

func parsePES(data []byte, off int) (MPEGPacket, int, error) {
	var p MPEGPacket
	if len(data) < 6 {
		return p, 0, mpeg2Err(MPEG2Truncated, off, "short PES header")
	}
	if data[0] != 0 || data[1] != 0 || data[2] != 1 {
		return p, 0, mpeg2Err(MPEG2Malformed, off, "invalid PES start code")
	}
	streamID := data[3]
	declared := int(data[4])<<8 | int(data[5])
	end := len(data)
	if declared != 0 {
		if declared > len(data)-6 {
			return p, 0, mpeg2Err(MPEG2Truncated, off+6, "PES packet exceeds input")
		}
		end = 6 + declared
	}
	p.Offset, p.StreamID, p.Raw = off, streamID, data[:end]
	body := data[6:end]
	headerEnd := 0
	// Audio/video PES packets use this optional MPEG-2 header. Private and
	// padding packets are still exposed, but have no timestamp extraction.
	if len(body) >= 3 && body[0]&0xc0 == 0x80 {
		hlen := int(body[2])
		if hlen > len(body)-3 {
			return p, 0, mpeg2Err(MPEG2Truncated, off+8, "PES optional header exceeds packet")
		}
		flags := body[1] >> 6
		if flags == 2 || flags == 3 {
			if hlen < 5 {
				return p, 0, mpeg2Err(MPEG2Malformed, off+9, "short PTS field")
			}
			v, err := parseMPEGPTS5(body[3:8], off+9)
			if err != nil {
				return p, 0, err
			}
			p.PTS, p.HasPTS = v, true
		}
		if flags == 3 {
			if hlen < 10 {
				return p, 0, mpeg2Err(MPEG2Malformed, off+9, "short DTS field")
			}
			v, err := parseMPEGPTS5(body[8:13], off+14)
			if err != nil {
				return p, 0, err
			}
			p.DTS, p.HasDTS = v, true
		}
		headerEnd = 3 + hlen
	} else {
		// MPEG-1 PES header: stuffing, optional STD buffer, then timestamps.
		for headerEnd < len(body) && body[headerEnd] == 0xff {
			headerEnd++
		}
		if headerEnd < len(body) && body[headerEnd]&0xc0 == 0x40 {
			if len(body)-headerEnd < 2 {
				return p, 0, mpeg2Err(MPEG2Truncated, off+6+headerEnd, "short MPEG-1 PES STD field")
			}
			headerEnd += 2
		}
		if headerEnd < len(body) {
			prefix := body[headerEnd] >> 4
			if prefix == 2 || prefix == 3 {
				if len(body)-headerEnd < 5 {
					return p, 0, mpeg2Err(MPEG2Truncated, off+6+headerEnd, "short MPEG-1 PTS")
				}
				v, err := parseMPEGPTS5(body[headerEnd:headerEnd+5], off+6+headerEnd)
				if err != nil {
					return p, 0, err
				}
				p.PTS, p.HasPTS = v, true
				headerEnd += 5
				if prefix == 3 {
					if len(body)-headerEnd < 5 {
						return p, 0, mpeg2Err(MPEG2Truncated, off+6+headerEnd, "short MPEG-1 DTS")
					}
					v, err := parseMPEGPTS5(body[headerEnd:headerEnd+5], off+6+headerEnd)
					if err != nil {
						return p, 0, err
					}
					p.DTS, p.HasDTS = v, true
					headerEnd += 5
				}
			} else if body[headerEnd] != 0x0f {
				return p, 0, mpeg2Err(MPEG2Malformed, off+6+headerEnd, "invalid MPEG-1 PES header")
			} else {
				headerEnd++
			}
		}
	}
	if headerEnd > len(body) {
		return p, 0, mpeg2Err(MPEG2Truncated, off+6, "PES header exceeds packet")
	}
	p.Payload, p.Data = body[headerEnd:], body[headerEnd:]
	return p, end, nil
}

// ParseMPEGPES parses one complete PES packet and extracts PTS/DTS.
func ParseMPEGPES(data []byte) (MPEGPacket, error) {
	p, n, err := parsePES(data, 0)
	if err != nil {
		return MPEGPacket{}, err
	}
	if n != len(data) {
		return MPEGPacket{}, mpeg2Err(MPEG2Malformed, n, "trailing bytes after PES packet")
	}
	return p, nil
}

// ExtractMPEGPTS extracts an optional timestamp from a complete PES packet.
func ExtractMPEGPTS(data []byte) (uint64, bool, error) {
	p, err := ParseMPEGPES(data)
	if err != nil {
		return 0, false, err
	}
	return p.PTS, p.HasPTS, nil
}

func demuxTS(data []byte) (*MPEGStream, error) {
	if len(data)%188 != 0 {
		return nil, mpeg2Err(MPEG2Truncated, len(data)-(len(data)%188), "incomplete transport packet")
	}
	type assembly struct {
		data       []byte
		start      int
		pid        uint16
		continuity byte
		random     bool
	}
	active := make(map[uint16]*assembly)
	s := &MPEGStream{Format: MPEGTransport, Source: data}
	flush := func(a *assembly) error {
		if a == nil || len(a.data) == 0 {
			return nil
		}
		p, _, err := parsePES(a.data, a.start)
		if err != nil {
			return err
		}
		p.PID, p.Continuity, p.RandomAccess = a.pid, a.continuity, a.random
		var e error
		s.Packets, e = appendMPEGPacket(s.Packets, p)
		return e
	}
	for pos := 0; pos < len(data); pos += 188 {
		t := data[pos : pos+188]
		if t[0] != 0x47 {
			return nil, mpeg2Err(MPEG2Malformed, pos, "invalid transport sync byte")
		}
		pid := uint16(t[1]&0x1f)<<8 | uint16(t[2])
		pusi := t[1]&0x40 != 0
		adapt := (t[3] >> 4) & 3
		if adapt == 0 {
			return nil, mpeg2Err(MPEG2Malformed, pos+3, "invalid adaptation control")
		}
		i := 4
		random := false
		if adapt == 2 || adapt == 3 {
			if i >= len(t) {
				return nil, mpeg2Err(MPEG2Truncated, pos+i, "missing adaptation length")
			}
			n := int(t[i])
			if n > len(t)-i-1 {
				return nil, mpeg2Err(MPEG2Truncated, pos+i, "adaptation field exceeds packet")
			}
			if n > 0 {
				random = t[i+1]&0x40 != 0
			}
			i += n + 1
		}
		if adapt == 2 || i == len(t) {
			continue
		}
		a := active[pid]
		if pusi {
			if a != nil {
				if err := flush(a); err != nil {
					return nil, err
				}
			}
			a = &assembly{start: pos + i, pid: pid, random: random}
			active[pid] = a
		} else if a == nil {
			continue // PSI or a continuation before the first PES is ignored.
		}
		if len(a.data)+len(t[i:]) > maxMPEG2Packet {
			return nil, mpeg2Err(MPEG2Range, pos, "PES packet exceeds limit")
		}
		a.data = append(a.data, t[i:]...)
		a.continuity = t[3] & 15
	}
	for _, a := range active {
		if err := flush(a); err != nil {
			return nil, err
		}
	}
	if len(s.Packets) == 0 {
		return nil, mpeg2Err(MPEG2Malformed, 0, "transport stream has no PES packets")
	}
	return s, sequenceFromPES(s)
}

func sequenceFromPES(s *MPEGStream) error {
	for i := range s.Packets {
		es, err := ParseMPEGES(s.Packets[i].Payload)
		if err != nil {
			continue
		}
		if es.Sequence != nil {
			s.Sequence = es.Sequence
			return nil
		}
	}
	return nil
}

// ParseMPEGTS demuxes 188-byte MPEG transport packets into PES packets.
func ParseMPEGTS(data []byte) (*MPEGStream, error) {
	if len(data) > maxMPEG2Input {
		return nil, mpeg2Err(MPEG2Range, 0, "transport stream exceeds input limit")
	}
	return demuxTS(data)
}

func demuxPS(data []byte) (*MPEGStream, error) {
	if len(data) > maxMPEG2Input {
		return nil, mpeg2Err(MPEG2Range, 0, "program stream exceeds input limit")
	}
	first := findMPEGStart(data, 0)
	if first < 0 {
		return nil, mpeg2Err(MPEG2Truncated, 0, "missing program stream start code")
	}
	s := &MPEGStream{Format: MPEGProgram, Source: data}
	for at := first; at < len(data); {
		if at+4 > len(data) {
			return nil, mpeg2Err(MPEG2Truncated, at, "truncated program start code")
		}
		code := data[at+3]
		switch code {
		case 0xba:
			if at+12 > len(data) {
				return nil, mpeg2Err(MPEG2Truncated, at, "short pack header")
			}
			n := 12
			if data[at+4]&0xc0 == 0x40 {
				n = 14
			}
			if at+n > len(data) {
				return nil, mpeg2Err(MPEG2Truncated, at, "short MPEG-2 pack header")
			}
			at += n
		case 0xbb:
			if at+6 > len(data) {
				return nil, mpeg2Err(MPEG2Truncated, at, "short system header")
			}
			n := int(data[at+4])<<8 | int(data[at+5])
			if n > len(data)-at-6 {
				return nil, mpeg2Err(MPEG2Truncated, at+4, "system header exceeds input")
			}
			at += 6 + n
		case 0xb9:
			at += 4
		default:
			p, n, err := parsePES(data[at:], at)
			if err != nil {
				return nil, err
			}
			p.Raw = data[at:n]
			var e error
			s.Packets, e = appendMPEGPacket(s.Packets, p)
			if e != nil {
				return nil, e
			}
			at = n
		}
	}
	if len(s.Packets) == 0 {
		return nil, mpeg2Err(MPEG2Malformed, 0, "program stream has no PES packets")
	}
	return s, sequenceFromPES(s)
}

// ParseMPEGPS demuxes an MPEG program stream into PES packets.
func ParseMPEGPS(data []byte) (*MPEGStream, error) { return demuxPS(data) }

// ParseMPEGStream detects raw ES, transport stream, or program stream syntax.
func ParseMPEGStream(data []byte) (*MPEGStream, error) {
	if len(data) >= 1 && data[0] == 0x47 && len(data)%188 == 0 {
		return ParseMPEGTS(data)
	}
	if len(data) >= 4 && data[0] == 0 && data[1] == 0 && data[2] == 1 && data[3] == 0xba {
		return ParseMPEGPS(data)
	}
	return ParseMPEGES(data)
}

// DemuxMPEG is an alias for ParseMPEGStream.
func DemuxMPEG(data []byte) (*MPEGStream, error)   { return ParseMPEGStream(data) }
func DemuxMPEGTS(data []byte) (*MPEGStream, error) { return ParseMPEGTS(data) }
func DemuxMPEGPS(data []byte) (*MPEGStream, error) { return ParseMPEGPS(data) }

// ParseMPEG2 is the conventional codec-name alias for ParseMPEGStream.
func ParseMPEG2(data []byte) (*MPEGStream, error) { return ParseMPEGStream(data) }

// MPEG2Decoder incrementally parses MPEG-1/2 elementary streams and reconstructs
// progressive pictures into bounded planar YUV frames.
type MPEG2Decoder struct {
	pending  []byte
	sequence *MPEGSequence
	pts      uint64
	hasPTS   bool
	dts      uint64
	hasDTS   bool
	closed   bool
	real     *mpeg2RealDecoder
}

type MPEGDecoder = MPEG2Decoder

func NewMPEG2Decoder() *MPEG2Decoder { return &MPEG2Decoder{} }
func NewMPEGDecoder() *MPEG2Decoder  { return NewMPEG2Decoder() }

func (d *MPEG2Decoder) processUnit(unit []byte, off int, pts uint64, hasPTS bool, dts uint64, hasDTS bool) (*MPEGFrame, error) {
	if len(unit) < 4 {
		return nil, mpeg2Err(MPEG2Truncated, off, "short elementary-stream unit")
	}
	if d.real == nil {
		d.real = newMPEG2Real()
	}
	d.pts, d.hasPTS, d.dts, d.hasDTS = pts, hasPTS, dts, hasDTS
	f, err := d.real.process(unit[3], unit[4:], pts, hasPTS, dts, hasDTS)
	if d.real.sequence != nil {
		s := *d.real.sequence
		d.sequence = &s
	}
	if err != nil {
		if x, ok := err.(*MPEG2Error); ok && off != 0 && x.Offset < 0 {
			x.Offset = off
		}
		return nil, err
	}
	return f, nil
}

func (d *MPEG2Decoder) consume(final bool) ([]MPEGFrame, error) {
	var frames []MPEGFrame
	for {
		first := findMPEGStart(d.pending, 0)
		if first < 0 {
			if final && len(d.pending) != 0 {
				return nil, mpeg2Err(MPEG2Truncated, 0, "trailing bytes without start code")
			}
			return frames, nil
		}
		if first > 0 {
			for _, b := range d.pending[:first] {
				if b != 0 {
					return nil, mpeg2Err(MPEG2Malformed, first, "data before start code")
				}
			}
			d.pending = d.pending[first:]
		}
		next := findMPEGStart(d.pending, 4)
		if next < 0 && !final {
			if len(d.pending) > maxMPEG2Packet {
				return nil, mpeg2Err(MPEG2Range, 0, "elementary unit exceeds limit")
			}
			return frames, nil
		}
		end := len(d.pending)
		if next >= 0 {
			end = next
		}
		f, err := d.processUnit(d.pending[:end], 0, d.pts, d.hasPTS, d.dts, d.hasDTS)
		if err != nil {
			return nil, err
		}
		if f != nil {
			frames = append(frames, *f)
		}
		d.pending = d.pending[end:]
		if next < 0 {
			return frames, nil
		}
	}
}

// Feed appends ES bytes and processes all complete start-code units. It keeps
// an incomplete final unit for the next call.
func (d *MPEG2Decoder) Feed(data []byte) ([]MPEGFrame, error) {
	if d == nil {
		return nil, mpeg2Err(MPEG2Range, -1, "nil decoder")
	}
	if d.closed {
		return nil, mpeg2Err(MPEG2Range, -1, "decoder is closed")
	}
	if len(data) > maxMPEG2Input || len(d.pending)+len(data) > maxMPEG2Input {
		return nil, mpeg2Err(MPEG2Range, -1, "decoder input exceeds limit")
	}
	d.pending = append(d.pending, data...)
	return d.consume(false)
}

func (d *MPEG2Decoder) Push(data []byte) ([]MPEGFrame, error) { return d.Feed(data) }

// Write implements io.Writer-shaped incremental feeding and returns the number
// of source bytes admitted to the bounded decoder.
func (d *MPEG2Decoder) Write(data []byte) (int, error) {
	_, err := d.Feed(data)
	if err != nil {
		return 0, err
	}
	return len(data), nil
}

// Flush processes the final elementary unit and closes the decoder.
func (d *MPEG2Decoder) Flush() ([]MPEGFrame, error) {
	if d == nil {
		return nil, mpeg2Err(MPEG2Range, -1, "nil decoder")
	}
	if d.closed {
		return nil, mpeg2Err(MPEG2Range, -1, "decoder is closed")
	}
	d.closed = true
	frames, err := d.consume(true)
	if err != nil {
		return nil, err
	}
	if d.real != nil {
		f, e := d.real.finish()
		if e != nil {
			return nil, e
		}
		if f != nil {
			frames = append(frames, *f)
		}
	}
	return frames, nil
}

// DecodeMPEG2 validates/demuxes an MPEG stream and reconstructs its pictures.
func DecodeMPEG2(data []byte) ([]MPEGFrame, error) {
	s, err := ParseMPEGStream(data)
	if err != nil {
		return nil, err
	}
	d := NewMPEG2Decoder()
	var frames []MPEGFrame
	for _, p := range s.Packets {
		if s.Format == MPEGElementary {
			f, e := d.processUnit(p.Raw, p.Offset, 0, false, 0, false)
			if e != nil {
				return nil, e
			}
			if f != nil {
				frames = append(frames, *f)
			}
			continue
		}
		es, e := ParseMPEGES(p.Payload)
		if e != nil {
			return nil, e
		}
		for _, ep := range es.Packets {
			f, pe := d.processUnit(ep.Raw, ep.Offset, p.PTS, p.HasPTS, p.DTS, p.HasDTS)
			if pe != nil {
				return nil, pe
			}
			if f != nil {
				frames = append(frames, *f)
			}
		}
	}
	if f, e := d.real.finish(); e != nil {
		return nil, e
	} else if f != nil {
		frames = append(frames, *f)
	}
	return frames, nil
}
