package runtime

import (
	"bytes"
	"image/color"
	"testing"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
)

func TestOpenTypeFactoriesKeepAntialiasingWithFullHinting(t *testing.T) {
	parsed, err := opentype.Parse(goregular.TTF)
	if err != nil {
		t.Fatal(err)
	}
	hinted, err := fontFactoryFor(parsed)(12)
	if err != nil {
		t.Fatal(err)
	}
	defer hinted.Close()
	explicitFactory, err := ParseOpenType(goregular.TTF)
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := explicitFactory(12)
	if err != nil {
		t.Fatal(err)
	}
	defer explicit.Close()
	unhinted, err := opentype.NewFace(parsed, &opentype.FaceOptions{Size: 12, DPI: 72, Hinting: font.HintingNone})
	if err != nil {
		t.Fatal(err)
	}
	defer unhinted.Close()
	const sample = "Hamburgefontsiv"
	hintedImage, err := RasterizeText(hinted, sample, color.White)
	if err != nil {
		t.Fatal(err)
	}
	explicitImage, err := RasterizeText(explicit, sample, color.White)
	if err != nil {
		t.Fatal(err)
	}
	if explicitImage.Bounds() != hintedImage.Bounds() || !bytes.Equal(explicitImage.Pix, hintedImage.Pix) {
		t.Fatal("explicit OpenType factory does not use the discovered-font rasterization settings")
	}
	unhintedImage, err := RasterizeText(unhinted, sample, color.White)
	if err != nil {
		t.Fatal(err)
	}
	overlap := hintedImage.Bounds().Intersect(unhintedImage.Bounds())
	intermediate, changed := 0, 0
	for y := overlap.Min.Y; y < overlap.Max.Y; y++ {
		for x := overlap.Min.X; x < overlap.Max.X; x++ {
			hintedAlpha := hintedImage.RGBAAt(x, y).A
			unhintedAlpha := unhintedImage.RGBAAt(x, y).A
			if hintedAlpha > 0 && hintedAlpha < 255 {
				intermediate++
			}
			if hintedAlpha != unhintedAlpha {
				changed++
			}
		}
	}
	if intermediate == 0 {
		t.Fatal("hinted glyphs lost antialiased edge coverage")
	}
	if changed == 0 {
		t.Fatal("full hinting produced the same softened pixels as no hinting")
	}
}
