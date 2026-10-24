package runtime

import (
	"bytes"
	"testing"
)

func TestFillLayerRectRGBAContents(t *testing.T) {
	layer := NewLayer(1)
	layer.ImageWidth, layer.ImageHeight = 37, 29
	layer.RGBA = make([]byte, int(layer.ImageWidth*layer.ImageHeight*4))
	rect := Rect{X: 5, Y: 7, Width: 19, Height: 13}
	color := [4]uint8{17, 34, 51, 68}
	fillLayerRectRGBA(layer, rect, color)
	for y := 0; y < int(layer.ImageHeight); y++ {
		for x := 0; x < int(layer.ImageWidth); x++ {
			pixel := layer.RGBA[(y*int(layer.ImageWidth)+x)*4:]
			inside := x >= int(rect.X) && x < int(rect.Right()) && y >= int(rect.Y) && y < int(rect.Bottom())
			if inside && !bytes.Equal(pixel[:4], color[:]) {
				t.Fatalf("pixel (%d,%d) = %v, want %v", x, y, pixel[:4], color)
			}
			if !inside && !bytes.Equal(pixel[:4], []byte{0, 0, 0, 0}) {
				t.Fatalf("outside pixel (%d,%d) = %v", x, y, pixel[:4])
			}
		}
	}
}

func BenchmarkFillLayerRectRGBA720p(b *testing.B) {
	const width, height = 1280, 720
	layer := NewLayer(1)
	layer.ImageWidth, layer.ImageHeight = width, height
	layer.RGBA = make([]byte, width*height*4)
	rect := Rect{Width: width, Height: height}
	color := [4]uint8{17, 34, 51, 68}
	b.SetBytes(int64(len(layer.RGBA)))
	b.ResetTimer()
	for range b.N {
		fillLayerRectRGBA(layer, rect, color)
	}
}

func BenchmarkFillLayerRectRGBAScalar720p(b *testing.B) {
	const width, height = 1280, 720
	pixels := make([]byte, width*height*4)
	rect := Rect{Width: width, Height: height}
	color := [4]uint8{17, 34, 51, 68}
	b.SetBytes(int64(len(pixels)))
	b.ResetTimer()
	for range b.N {
		for y := rect.Y; y < rect.Bottom(); y++ {
			for x := rect.X; x < rect.Right(); x++ {
				offset := (int(y)*width + int(x)) * 4
				pixels[offset], pixels[offset+1] = color[0], color[1]
				pixels[offset+2], pixels[offset+3] = color[2], color[3]
			}
		}
	}
}

func TestFillLayerProvinceContents(t *testing.T) {
	layer := NewLayer(1)
	layer.ProvinceWidth, layer.ProvinceHeight = 37, 29
	layer.Province = make([]byte, 37*29)
	rect := Rect{X: 5, Y: 7, Width: 19, Height: 13}
	fillLayerProvince(layer, rect, 68)
	for y := 0; y < int(layer.ProvinceHeight); y++ {
		for x := 0; x < int(layer.ProvinceWidth); x++ {
			inside := x >= int(rect.X) && x < int(rect.Right()) && y >= int(rect.Y) && y < int(rect.Bottom())
			want := byte(0)
			if inside {
				want = 68
			}
			if got := layer.Province[y*int(layer.ProvinceWidth)+x]; got != want {
				t.Fatalf("province pixel (%d,%d) = %d, want %d", x, y, got, want)
			}
		}
	}
}

func TestFillLayerMaskContents(t *testing.T) {
	layer := NewLayer(1)
	layer.ImageWidth, layer.ImageHeight = 37, 29
	layer.RGBA = make([]byte, int(layer.ImageWidth*layer.ImageHeight*4))
	for index := range layer.RGBA {
		layer.RGBA[index] = byte(index)
	}
	rect := Rect{X: 5, Y: 7, Width: 19, Height: 13}
	fillLayerMaskRGBA(layer, rect, 68)
	for y := 0; y < int(layer.ImageHeight); y++ {
		for x := 0; x < int(layer.ImageWidth); x++ {
			inside := x >= int(rect.X) && x < int(rect.Right()) && y >= int(rect.Y) && y < int(rect.Bottom())
			want := byte((y*int(layer.ImageWidth)+x)*4 + 3)
			if inside {
				want = 68
			}
			if got := layer.RGBA[(y*int(layer.ImageWidth)+x)*4+3]; got != want {
				t.Fatalf("mask alpha (%d,%d) = %d, want %d", x, y, got, want)
			}
		}
	}
}

func BenchmarkFillLayerProvince720p(b *testing.B) {
	const width, height = 1280, 720
	layer := NewLayer(1)
	layer.ProvinceWidth, layer.ProvinceHeight = width, height
	layer.Province = make([]byte, width*height)
	rect := Rect{Width: width, Height: height}
	b.SetBytes(int64(len(layer.Province)))
	b.ResetTimer()
	for range b.N {
		fillLayerProvince(layer, rect, 68)
	}
}

func BenchmarkFillLayerProvinceScalar720p(b *testing.B) {
	const width, height = 1280, 720
	province := make([]byte, width*height)
	rect := Rect{Width: width, Height: height}
	b.SetBytes(int64(len(province)))
	b.ResetTimer()
	for range b.N {
		for y := int(rect.Y); y < int(rect.Bottom()); y++ {
			for x := int(rect.X); x < int(rect.Right()); x++ {
				province[y*width+x] = 68
			}
		}
	}
}

func BenchmarkFillLayerMaskRGBA720p(b *testing.B) {
	const width, height = 1280, 720
	layer := NewLayer(1)
	layer.ImageWidth, layer.ImageHeight = width, height
	layer.RGBA = make([]byte, width*height*4)
	rect := Rect{Width: width, Height: height}
	b.SetBytes(int64(len(layer.RGBA)))
	b.ResetTimer()
	for range b.N {
		fillLayerMaskRGBA(layer, rect, 68)
	}
}

func BenchmarkFillLayerMaskRGBAScalar720p(b *testing.B) {
	const width, height = 1280, 720
	pixels := make([]byte, width*height*4)
	rect := Rect{Width: width, Height: height}
	b.SetBytes(int64(len(pixels)))
	b.ResetTimer()
	for range b.N {
		for y := int(rect.Y); y < int(rect.Bottom()); y++ {
			for x := int(rect.X); x < int(rect.Right()); x++ {
				pixels[(y*width+x)*4+3] = 68
			}
		}
	}
}
