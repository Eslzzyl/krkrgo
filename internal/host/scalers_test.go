package host

import (
	"testing"

	"github.com/hajimehoshi/ebiten/v2"
)

func TestScalersOutputSizeAndReset(t *testing.T) {
	factories := map[Upscaler]func() (PostProcessor, error){
		UpscalerBilinear: NewBilinearPostProcessor,
		UpscalerBicubic:  NewBicubicPostProcessor,
	}
	for mode, factory := range factories {
		t.Run(string(mode), func(t *testing.T) {
			processor, err := factory()
			if err != nil {
				t.Fatal(err)
			}
			defer processor.Deallocate()

			src := ebiten.NewImage(4, 3)
			defer src.Deallocate()
			pixels := make([]byte, 4*3*4)
			for i := range pixels {
				pixels[i] = byte(i)
			}
			src.WritePixels(pixels)
			for range 2 {
				out, err := processor.Process(src)
				if err != nil {
					t.Fatal(err)
				}
				if bounds := out.Bounds(); bounds.Dx() != 8 || bounds.Dy() != 6 {
					t.Fatalf("output size = %v, want 8x6", bounds)
				}
			}
			processor.Reset()
			out, err := processor.Process(src)
			if err != nil {
				t.Fatal(err)
			}
			if bounds := out.Bounds(); bounds.Dx() != 8 || bounds.Dy() != 6 {
				t.Fatalf("output after reset = %v, want 8x6", bounds)
			}
		})
	}
}

func TestNewPostProcessorModes(t *testing.T) {
	for _, mode := range []Upscaler{UpscalerBilinear, UpscalerBicubic, UpscalerAnime4K} {
		processor, err := newPostProcessor(mode)
		if err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
		processor.Deallocate()
	}
	if _, err := newPostProcessor(Upscaler("unknown")); err == nil {
		t.Fatal("unknown upscaler succeeded")
	}
}
