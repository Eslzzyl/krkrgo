package runtime

import (
	"image"
	"image/color"

	"golang.org/x/image/math/fixed"
)

// styledFontFace supplies the embolden and oblique effects used by the
// FreeType renderer when the selected face has no explicit style variant.
type styledFontFace struct {
	FontFace
	bold   bool
	italic bool
}

func (f *styledFontFace) Glyph(dot fixed.Point26_6, r rune) (dr image.Rectangle, mask image.Image, maskp image.Point, advance fixed.Int26_6, ok bool) {
	dr, mask, maskp, advance, ok = f.FontFace.Glyph(dot, r)
	if !ok || mask == nil || (!f.bold && !f.italic) {
		return
	}
	bounds := mask.Bounds()
	slant := 0
	if f.italic && bounds.Dy() > 0 {
		slant = (bounds.Dy() + 3) / 4
	}
	extra := slant
	if f.bold {
		extra++
	}
	styled := image.NewAlpha(image.Rect(bounds.Min.X, bounds.Min.Y, bounds.Max.X+extra, bounds.Max.Y))
	for y := range bounds.Dy() {
		shift := 0
		if slant > 0 {
			shift = (bounds.Dy() - 1 - y) / 4
		}
		for x := 0; x < bounds.Dx(); x++ {
			a := color.AlphaModel.Convert(mask.At(bounds.Min.X+x, bounds.Min.Y+y)).(color.Alpha).A
			if a == 0 {
				continue
			}
			dx := x + shift
			row := y * styled.Stride
			styled.Pix[row+dx] = a
			if f.bold && dx+1 < styled.Rect.Dx() && a > styled.Pix[row+dx+1] {
				styled.Pix[row+dx+1] = a
			}
		}
	}
	mask = styled
	if slant > 0 || f.bold {
		dr.Max.X += slant + boolToInt(f.bold)
	}
	return
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
