package assets

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

func TestDecodeTLG5SyntheticRGBA(t *testing.T) {
	rgba := []byte{
		10, 20, 30, 40, 50, 60, 70, 80,
		90, 100, 110, 120, 130, 140, 150, 160,
		170, 180, 190, 200, 210, 220, 230, 240,
		5, 15, 25, 35, 45, 55, 65, 75,
	}
	encoded := syntheticTLG5(rgba, 4, 2, 2, false)
	got, err := DecodeImage(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if got.Width != 4 || got.Height != 2 || !bytes.Equal(got.RGBA, rgba) {
		t.Fatalf("decoded = %dx%d %#v", got.Width, got.Height, got.RGBA)
	}
}

func TestDecodeTLG5CompressedAndTruncated(t *testing.T) {
	rgba := []byte{25, 77, 130, 255, 0, 128, 200, 12}
	encoded := syntheticTLG5(rgba, 2, 1, 1, true)
	got, err := DecodeTLG(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if got.Width != 2 || got.Height != 1 || !bytes.Equal(got.RGBA, rgba) {
		t.Fatalf("decoded = %dx%d %#v", got.Width, got.Height, got.RGBA)
	}
	for n := 3; n < len(encoded); n++ {
		if _, err := DecodeTLG(encoded[:n]); err == nil {
			t.Fatalf("truncation at %d unexpectedly decoded", n)
		} else if !errors.Is(err, ErrTruncated) {
			t.Fatalf("truncation at %d error = %v", n, err)
		}
	}
}

func TestDecodeTLG6SyntheticChannels(t *testing.T) {
	tests := []struct {
		name    string
		encoded []byte
		want    []byte
	}{
		{"gray", syntheticTLG6(1, 1, 1, []byte{42}), []byte{42, 42, 42, 255}},
		{"rgb", syntheticTLG6(3, 1, 1, []byte{10, 20, 30, 255}), []byte{10, 20, 30, 255}},
		{"rgba", syntheticTLG6(4, 1, 1, []byte{1, 2, 3, 4}), []byte{1, 2, 3, 4}},
		{"high residuals", syntheticTLG6(4, 1, 1, []byte{200, 240, 250, 255}), []byte{200, 240, 250, 255}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DecodeTLG(tc.encoded)
			if err != nil {
				t.Fatal(err)
			}
			if got.Width != 1 || got.Height != 1 || !bytes.Equal(got.RGBA, tc.want) {
				t.Fatalf("decoded = %dx%d %#v, want %#v", got.Width, got.Height, got.RGBA, tc.want)
			}
		})
	}
}

func TestDecodeTLG6TypedErrors(t *testing.T) {
	base := syntheticTLG6(3, 1, 1, []byte{10, 20, 30, 255})
	badChannels := append([]byte(nil), base...)
	badChannels[len(tlg6Header)] = 2
	if _, err := DecodeTLG(badChannels); !errors.Is(err, ErrMalformed) {
		t.Fatalf("channel error = %v", err)
	}
	for i := 1; i < 4; i++ {
		bad := append([]byte(nil), base...)
		bad[len(tlg6Header)+i] = 1
		if _, err := DecodeTLG(bad); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("flag %d error = %v", i, err)
		}
	}
	badMethod := append([]byte(nil), base...)
	// Header (11), flags (4), dimensions (8), max bits (4), filter size
	// (4), filter stream (2), then the first entropy bit-length word.
	badMethod[33] = 0
	badMethod[34] = 0
	badMethod[35] = 0
	badMethod[36] = 0x40
	if _, err := DecodeTLG(badMethod); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("entropy method error = %v", err)
	}
	for n := 3; n < len(base); n++ {
		if _, err := DecodeTLG(base[:n]); err == nil {
			t.Fatalf("truncation at %d unexpectedly decoded", n)
		} else if !errors.Is(err, ErrTruncated) {
			t.Fatalf("truncation at %d error = %v", n, err)
		}
	}
}
func TestTLG6GolombQuotientSupportsExtendedRuns(t *testing.T) {
	reader := tlg6BitReader{data: []byte{0, 0, 0, 0, 5, 0x05}, bits: 43}
	quotient, err := reader.golombQuotient()
	if err != nil {
		t.Fatal(err)
	}
	if quotient != 5 || reader.pos != 40 {
		t.Fatalf("extended quotient=%d at bit %d, want 5 at 40", quotient, reader.pos)
	}
	remainder, err := reader.read(3)
	if err != nil {
		t.Fatal(err)
	}
	if remainder != 5 || reader.pos != 43 {
		t.Fatalf("extended remainder=%d at bit %d, want 5 at 43", remainder, reader.pos)
	}
}

type tlg6TestBits struct {
	data []byte
	bit  uint
}

func (w *tlg6TestBits) put(v uint32, n uint) {
	for i := range n {
		if w.bit == 0 {
			w.data = append(w.data, 0)
		}
		if v&(1<<i) != 0 {
			w.data[len(w.data)-1] |= 1 << w.bit
		}
		w.bit = (w.bit + 1) & 7
	}
}

func (w *tlg6TestBits) gamma(v int) {
	n := 0
	for (1 << n) <= v {
		n++
	}
	for i := 1; i < n; i++ {
		w.put(0, 1)
	}
	w.put(1, 1)
	w.put(uint32(v-(1<<(n-1))), uint(n-1))
}

func syntheticTLG6(channels byte, width, height uint32, rgba []byte) []byte {
	if width != 1 || height != 1 || (channels != 1 && channels != 3 && channels != 4) {
		panic("invalid synthetic TLG6 image")
	}
	var streams [4][]byte
	var bitLengths [4]uint32
	maxBits := uint32(0)
	for c := byte(0); c < channels; c++ {
		value := int(rgba[0])
		if channels != 1 {
			switch c {
			case 0:
				value = int(rgba[2])
			case 1:
				value = int(rgba[1])
			case 2:
				value = int(rgba[0])
			case 3:
				value = int(rgba[3])
			}
		}
		var bits tlg6TestBits
		bits.put(1, 1)
		bits.gamma(1)
		value = int(int8(byte(value)))
		m := uint32(2 * value)
		if value < 0 {
			m = uint32(-2*value - 1)
		}
		m--
		k := uint32(tlg6GolombBitLengthTable[0][3])
		q := m >> k
		if q >= 32 {
			for range 30 {
				bits.put(0, 1)
			}
			bits.put(q, 8)
		} else {
			for q != 0 {
				bits.put(0, 1)
				q--
			}
			bits.put(1, 1)
			bits.put(m, uint(k))
		}
		streams[c] = bits.data
		bitLengths[c] = uint32(len(bits.data) * 8)
		if bits.bit != 0 {
			bitLengths[c] -= uint32(8 - bits.bit)
		}
		if bitLengths[c] > maxBits {
			maxBits = bitLengths[c]
		}
	}
	out := append([]byte{}, []byte(tlg6Header)...)
	out = append(out, channels, 0, 0, 0)
	var buf [4]byte
	binary.LittleEndian.PutUint32(buf[:], width)
	out = append(out, buf[:]...)
	binary.LittleEndian.PutUint32(buf[:], height)
	out = append(out, buf[:]...)
	binary.LittleEndian.PutUint32(buf[:], maxBits)
	out = append(out, buf[:]...)
	filter := literalSlide([]byte{0})
	binary.LittleEndian.PutUint32(buf[:], uint32(len(filter)))
	out = append(out, buf[:]...)
	out = append(out, filter...)
	for c := byte(0); c < channels; c++ {
		bits := streams[c]
		binary.LittleEndian.PutUint32(buf[:], bitLengths[c])
		out = append(out, buf[:]...)
		out = append(out, bits...)
	}
	return out
}

// syntheticTLG5 writes a tiny valid TLG5 stream using raw planes or literal
// LZSS planes. It mirrors SaveTLG5's per-row residual and B/G/R decorrelation.
func syntheticTLG5(rgba []byte, width, height, blockHeight uint32, compressed bool) []byte {
	if uint32(len(rgba)) != width*height*4 {
		panic("invalid synthetic image")
	}
	channels := uint8(4)
	body := make([]byte, 0, len(rgba)+64)
	blockCount := (height + blockHeight - 1) / blockHeight
	blockSizes := make([]uint32, blockCount)
	for block := uint32(0); block < blockCount; block++ {
		top := block * blockHeight
		rows := blockHeight
		if rows > height-top {
			rows = height - top
		}
		planes := [4][]byte{}
		for y := uint32(0); y < rows; y++ {
			var previous [4]byte
			for x := uint32(0); x < width; x++ {
				base := ((top+y)*width + x) * 4
				pixel := [4]byte{rgba[base+2], rgba[base+1], rgba[base], rgba[base+3]}
				var upper [4]byte
				if top+y != 0 {
					upperBase := ((top+y-1)*width + x) * 4
					upper = [4]byte{rgba[upperBase+2], rgba[upperBase+1], rgba[upperBase], rgba[upperBase+3]}
				}
				var value [4]byte
				for c := range value {
					delta := pixel[c] - upper[c]
					value[c] = delta - previous[c]
					previous[c] = delta
				}
				planes[0] = append(planes[0], value[0]-value[1])
				planes[1] = append(planes[1], value[1])
				planes[2] = append(planes[2], value[2]-value[1])
				planes[3] = append(planes[3], value[3])
			}
		}
		start := len(body)
		for c := range planes {
			payload := planes[c]
			mode := byte(1)
			if compressed {
				mode = 0
				payload = literalSlide(payload)
			}
			body = append(body, mode, 0, 0, 0, 0)
			binary.LittleEndian.PutUint32(body[len(body)-4:], uint32(len(payload)))
			body = append(body, payload...)
		}
		blockSizes[block] = uint32(len(body) - start)
	}

	out := make([]byte, len(tlg5Header)+1+12+len(blockSizes)*4+len(body))
	copy(out, tlg5Header)
	out[11] = channels
	binary.LittleEndian.PutUint32(out[12:16], width)
	binary.LittleEndian.PutUint32(out[16:20], height)
	binary.LittleEndian.PutUint32(out[20:24], blockHeight)
	pos := 24
	for _, size := range blockSizes {
		binary.LittleEndian.PutUint32(out[pos:pos+4], size)
		pos += 4
	}
	copy(out[pos:], body)
	return out
}

func literalSlide(src []byte) []byte {
	out := make([]byte, 0, len(src)+len(src)/8+1)
	for len(src) != 0 {
		n := 8
		if n > len(src) {
			n = len(src)
		}
		out = append(out, 0)
		out = append(out, src[:n]...)
		src = src[n:]
	}
	return out
}
