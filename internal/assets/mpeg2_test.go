package assets

import (
	"encoding/binary"
	"errors"
	"testing"
)

func mpegPTS(v uint64, prefix byte) [5]byte {
	return [5]byte{
		(prefix << 4) | byte((v>>30)&7)<<1 | 1,
		byte(v >> 22),
		byte((v>>15)&0x7f)<<1 | 1,
		byte(v >> 7),
		byte(v&0x7f)<<1 | 1,
	}
}

func mpegSequenceUnit() []byte {
	// 16x16, aspect 1, 25fps, bitrate/vbv fields zero, constrained off.
	// The MPEG sequence marker is bit 5 of byte 6.
	return []byte{0, 0, 1, 0xb3, 0x01, 0x00, 0x10, 0x32, 0, 0, 0x20, 0}
}

func mpegFlatSliceUnit() []byte {
	// qscale=1, one intra macroblock, all six blocks DC-only at the neutral level.
	return []byte{0, 0, 1, 0x01, 0x0b, 0x94, 0xa5, 0x22, 0x20}
}

func mpegPES(payload []byte, pts uint64) []byte {
	t := mpegPTS(pts, 2)
	body := []byte{0x80, 0x80, 5, t[0], t[1], t[2], t[3], t[4]}
	body = append(body, payload...)
	out := []byte{0, 0, 1, 0xe0, 0, 0}
	binary.BigEndian.PutUint16(out[4:], uint16(len(body)))
	return append(out, body...)
}

func TestMPEGESSequenceAndBounds(t *testing.T) {
	s, err := ParseMPEGES(append(mpegSequenceUnit(), []byte{0, 0, 1, 0xb7}...))
	if err != nil {
		t.Fatal(err)
	}
	if s.Sequence == nil || s.Sequence.Width != 16 || s.Sequence.Height != 16 || s.Sequence.FrameRateCode != 2 {
		t.Fatalf("unexpected sequence: %#v", s.Sequence)
	}
	if len(s.Packets) != 2 || len(s.Packets[0].Payload) != 8 {
		t.Fatalf("unexpected ES packets: %#v", s.Packets)
	}
	if _, err := ParseMPEGES([]byte{0, 0, 1}); !errors.Is(err, ErrMPEG2Truncated) {
		t.Fatalf("want typed truncation, got %v", err)
	}
}

func TestMPEGPESPTS(t *testing.T) {
	want := uint64(0x12345678)
	p, err := ParseMPEGPES(mpegPES([]byte{0xaa, 0xbb}, want))
	if err != nil {
		t.Fatal(err)
	}
	if !p.HasPTS || p.PTS != want || len(p.Payload) != 2 || p.Payload[0] != 0xaa {
		t.Fatalf("unexpected PES: %#v", p)
	}
	got, ok, err := ExtractMPEGPTS(mpegPES(nil, want))
	if err != nil || !ok || got != want {
		t.Fatalf("extracted PTS: %d %v %v", got, ok, err)
	}
}

func TestMPEGTransportDemuxAndDecoderFrame(t *testing.T) {
	pes := mpegPES(mpegSequenceUnit(), 90_000)
	var ts []byte
	for len(pes) != 0 {
		n := len(pes)
		if n > 184 {
			n = 184
		}
		p := make([]byte, 188)
		p[0] = 0x47
		p[1] = 0x40 // PUSI, PID zero.
		p[2] = 1
		p[3] = 0x10
		copy(p[4:], pes[:n])
		for i := 4 + n; i < len(p); i++ {
			p[i] = 0xff
		}
		ts = append(ts, p...)
		pes = pes[n:]
	}
	s, err := ParseMPEGTS(ts)
	if err != nil || len(s.Packets) != 1 || !s.Packets[0].HasPTS {
		t.Fatalf("TS demux: %v %#v", err, s)
	}
	if s.Packets[0].PTS != 90_000 {
		t.Fatalf("PTS = %d", s.Packets[0].PTS)
	}

	d := NewMPEG2Decoder()
	if _, err := d.Feed(mpegSequenceUnit()[:6]); err != nil {
		t.Fatal(err)
	}
	picture := []byte{0, 0, 1, 0, 0, 0x08, 0, 0}
	if _, err := d.Feed(append(mpegSequenceUnit()[6:], picture...)); err != nil {
		t.Fatalf("decode picture header: %v", err)
	}

	frames, err := d.Flush()
	if err != nil || len(frames) != 1 || frames[0].Width != 16 || frames[0].Height != 16 {
		t.Fatalf("want one decoded frame, got %d: %v", len(frames), err)
	}
}
func TestMPEG2FlatIFramePixels(t *testing.T) {
	picture := []byte{0, 0, 1, 0, 0, 0x08, 0, 0}
	data := append(append(append([]byte{}, mpegSequenceUnit()...), picture...), mpegFlatSliceUnit()...)
	frames, err := DecodeMPEG2(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 1 {
		t.Fatalf("frames = %d", len(frames))
	}
	f := frames[0]
	if f.Width != 16 || f.Height != 16 || f.Format != MPEGYUV420 {
		t.Fatalf("unexpected frame metadata: %#v", f)
	}
	for _, p := range []MPEGPixelPlane{f.Y, f.U, f.V} {
		want := byte(128)
		for _, got := range p.Data {
			if got != want {
				t.Fatalf("non-neutral pixel %d in plane %#v", got, p)
			}
		}
	}
}
