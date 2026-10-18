package host

import (
	"testing"

	"github.com/hajimehoshi/ebiten/v2"
)

func TestAnime4KShadersCompile(t *testing.T) {
	processor, err := NewAnime4KPostProcessor()
	if err != nil {
		t.Fatal(err)
	}
	processor.Deallocate()
}

func TestAnime4KProcessorOutputSizeAndReset(t *testing.T) {
	processor, err := NewAnime4KPostProcessor()
	if err != nil {
		t.Fatal(err)
	}
	defer processor.Deallocate()

	src := ebiten.NewImage(4, 5)
	defer src.Deallocate()
	pixels := make([]byte, 4*5*4)
	for i := range pixels {
		pixels[i] = byte(i)
	}
	src.WritePixels(pixels)
	for range 2 {
		out, err := processor.Process(src)
		if err != nil {
			t.Fatal(err)
		}
		if bounds := out.Bounds(); bounds.Dx() != 8 || bounds.Dy() != 10 {
			t.Fatalf("output size = %v, want 8x10", bounds)
		}
	}
	processor.Reset()
	out, err := processor.Process(src)
	if err != nil {
		t.Fatal(err)
	}
	if bounds := out.Bounds(); bounds.Dx() != 8 || bounds.Dy() != 10 {
		t.Fatalf("output after reset = %v, want 8x10", bounds)
	}
}

func TestAnime4KProcessorRejectsNilSource(t *testing.T) {
	processor, err := NewAnime4KPostProcessor()
	if err != nil {
		t.Fatal(err)
	}
	defer processor.Deallocate()
	if _, err := processor.Process(nil); err == nil {
		t.Fatal("Process(nil) succeeded")
	}
}
