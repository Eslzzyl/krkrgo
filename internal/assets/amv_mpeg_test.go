package assets

import (
	"encoding/binary"
	"errors"
	"testing"
)

func testAMV(alpha byte, entropy []byte) []byte {
	tables := 2
	if alpha&1 != 0 {
		tables = 3
	}
	h := 40 + tables*64
	b := make([]byte, h)
	copy(b, []byte("AJPM"))
	binary.LittleEndian.PutUint32(b[8:], 0)
	binary.LittleEndian.PutUint32(b[12:], uint32(h))
	binary.LittleEndian.PutUint32(b[20:], 1)
	binary.LittleEndian.PutUint32(b[24:], 1)
	binary.LittleEndian.PutUint32(b[28:], 30)
	binary.LittleEndian.PutUint16(b[32:], 16)
	binary.LittleEndian.PutUint16(b[34:], 16)
	b[36] = alpha
	for i := 40; i < h; i++ {
		b[i] = 1
	}
	b = append(b, []byte("FRAM")...)
	length := 12 + len(entropy)
	b = append(b, make([]byte, 4)...)
	binary.LittleEndian.PutUint32(b[len(b)-4:], uint32(length))
	b = append(b, make([]byte, 12)...)
	base := len(b) - 12
	binary.LittleEndian.PutUint16(b[base+8:], 16)
	binary.LittleEndian.PutUint16(b[base+10:], 16)
	b = append(b, entropy...)
	binary.LittleEndian.PutUint32(b[4:], uint32(len(b)))
	return b
}
func zeroEntropy16() []byte {
	var out []byte
	var current byte
	used := 0
	put := func(value byte, width int) {
		for bit := width - 1; bit >= 0; bit-- {
			if value&(1<<uint(bit)) != 0 {
				current |= 1 << uint(7-used)
			}
			used++
			if used == 8 {
				out = append(out, current)
				current, used = 0, 0
			}
		}
	}
	put(0, 2) // Cb DC category zero
	put(0, 2) // Cb EOB
	put(0, 2) // Cr DC category zero
	put(0, 2) // Cr EOB
	for range 8 {
		put(0, 2)   // luma/alpha DC category zero
		put(0xA, 4) // luma/alpha EOB
	}
	if used != 0 {
		out = append(out, current)
	}
	return out
}

func TestAMVPacketIndexAndTypedBounds(t *testing.T) {
	m, err := ParseAMV(testAMV(1, []byte{0, 0, 0, 0}))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Packets) != 1 || m.Packets[0].Width != 16 {
		t.Fatalf("unexpected packet index: %+v", m.Packets)
	}
	if got, err := m.Entropy(0); err != nil || len(got) != 4 {
		t.Fatalf("entropy: %v, %d bytes", err, len(got))
	}
	if _, err := m.Entropy(1); !errors.Is(err, ErrAMVRange) {
		t.Fatalf("want range error, got %v", err)
	}
}

func TestAMVRejectsTruncationBeforeDecode(t *testing.T) {
	data := testAMV(1, []byte{0, 0, 0, 0})
	data = data[:len(data)-1]
	binary.LittleEndian.PutUint32(data[4:], uint32(len(data)))
	_, err := ParseAMV(data)
	if !errors.Is(err, ErrAMVTruncated) {
		t.Fatalf("want typed truncation, got %v", err)
	}
}

func TestAMVZeroDCDecodesToNeutralRGBA(t *testing.T) {
	m, err := ParseAMV(testAMV(1, zeroEntropy16()))
	if err != nil {
		t.Fatal(err)
	}
	img, err := m.DecodePacket(0)
	if err != nil {
		t.Fatal(err)
	}
	if img == nil || len(img.RGBA) != 16*16*4 {
		t.Fatalf("unexpected image: %#v", img)
	}
	for i := range img.RGBA {
		if img.RGBA[i] != 128 {
			t.Fatalf("pixel byte %d: got %d, want 128", i, img.RGBA[i])
		}
	}
}
