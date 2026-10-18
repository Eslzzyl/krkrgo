package simd

import (
	"bytes"
	"math/rand"
	"testing"
)

func TestAllAlphaOpaque(t *testing.T) {
	for width := 0; width <= 64; width++ {
		row := make([]byte, width*4)
		for i := 3; i < len(row); i += 4 {
			row[i] = 255
		}
		if !AllAlphaOpaque(row) {
			t.Fatalf("opaque width %d rejected", width)
		}
		for i := 3; i < len(row); i += 4 {
			row[i] = 254
			if AllAlphaOpaque(row) {
				t.Fatalf("translucent width %d pixel %d accepted", width, i/4)
			}
			row[i] = 255
		}
	}
}

func TestScale2xRow(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for width := 0; width <= 64; width++ {
		src := make([]byte, width*4)
		for i := range src {
			src[i] = byte(rng.Intn(256))
		}
		dst := make([]byte, len(src)*2)
		Scale2xRow(dst, src)
		var want []byte
		for i := 0; i+3 < len(src); i += 4 {
			want = append(want, src[i:i+4]...)
			want = append(want, src[i:i+4]...)
		}
		if !bytes.Equal(dst, want) {
			t.Fatalf("width %d: got %v want %v", width, dst, want)
		}
	}
}

func BenchmarkAllAlphaOpaque(b *testing.B) {
	row := make([]byte, 1280*4)
	for i := 3; i < len(row); i += 4 {
		row[i] = 255
	}
	b.SetBytes(int64(len(row)))
	b.ResetTimer()
	for b.Loop() {
		if !AllAlphaOpaque(row) {
			b.Fatal("row not opaque")
		}
	}
}

func BenchmarkScale2xRow(b *testing.B) {
	src := make([]byte, 1280*4)
	for i := range src {
		src[i] = byte(i)
	}
	dst := make([]byte, len(src)*2)
	b.SetBytes(int64(len(src)))
	b.ResetTimer()
	for b.Loop() {
		Scale2xRow(dst, src)
	}
}

func scalarAllAlphaOpaque(row []byte) bool {
	for i := 3; i < len(row); i += 4 {
		if row[i] != 255 {
			return false
		}
	}
	return true
}

func BenchmarkAllAlphaOpaqueScalar(b *testing.B) {
	row := make([]byte, 1280*4)
	for i := 3; i < len(row); i += 4 {
		row[i] = 255
	}
	b.SetBytes(int64(len(row)))
	b.ResetTimer()
	for b.Loop() {
		if !scalarAllAlphaOpaque(row) {
			b.Fatal("row not opaque")
		}
	}
}

func scalarScale2xRow(dst, src []byte) {
	for i := 0; i+3 < len(src); i += 4 {
		copy(dst[i*2:i*2+4], src[i:i+4])
		copy(dst[i*2+4:i*2+8], src[i:i+4])
	}
}

func BenchmarkScale2xRowScalar(b *testing.B) {
	src := make([]byte, 1280*4)
	for i := range src {
		src[i] = byte(i)
	}
	dst := make([]byte, len(src)*2)
	b.SetBytes(int64(len(src)))
	b.ResetTimer()
	for b.Loop() {
		scalarScale2xRow(dst, src)
	}
}

func TestAlphaOverOpaqueRowMatchesScalar(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	for width := 0; width <= 64; width++ {
		src := make([]byte, width*4)
		dst := make([]byte, width*4)
		for i := range src {
			src[i] = byte(rng.Intn(256))
		}
		for i := range dst {
			dst[i] = byte(rng.Intn(256))
		}
		for i := range width {
			dst[i*4+3] = 255
		}
		want := append([]byte(nil), dst...)
		for i := 0; i < width; i++ {
			alphaOverOpaquePixel(want[i*4:i*4+4], src[i*4:i*4+4])
		}
		AlphaOverOpaqueRow(dst, src)
		if !bytes.Equal(dst, want) {
			t.Fatalf("width %d: got %v want %v", width, dst, want)
		}
	}
}

func BenchmarkAlphaOverOpaqueRow(b *testing.B) {
	src := make([]byte, 1280*4)
	dst := make([]byte, len(src))
	for i := range src {
		src[i] = byte(i*7 + 13)
		dst[i] = byte(i*3 + 29)
	}
	for i := range 1280 {
		src[i*4+3] = byte(i % 256)
		dst[i*4+3] = 255
	}
	b.SetBytes(int64(len(src)))
	b.ResetTimer()
	for b.Loop() {
		copy(dst, src)
		for i := range 1280 {
			dst[i*4+3] = 255
		}
		AlphaOverOpaqueRow(dst, src)
	}
}

func BenchmarkAlphaOverOpaqueRowScalar(b *testing.B) {
	src := make([]byte, 1280*4)
	dst := make([]byte, len(src))
	for i := range src {
		src[i] = byte(i*7 + 13)
		dst[i] = byte(i*3 + 29)
	}
	for i := range 1280 {
		src[i*4+3] = byte(i % 256)
		dst[i*4+3] = 255
	}
	b.SetBytes(int64(len(src)))
	b.ResetTimer()
	for b.Loop() {
		copy(dst, src)
		for i := range 1280 {
			dst[i*4+3] = 255
		}
		for i := 0; i < 1280; i++ {
			alphaOverOpaquePixel(dst[i*4:i*4+4], src[i*4:i*4+4])
		}
	}
}
