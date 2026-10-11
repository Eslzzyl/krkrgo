package runtime

import (
	"errors"
	"image"
	"image/color"
	"strings"
	"testing"

	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

func TestFontBookSelectionAndUnsupportedFont(t *testing.T) {
	book := NewFontBook()
	if err := book.Register("Deterministic", basicfont.Face7x13); err != nil {
		t.Fatal(err)
	}
	face, err := book.Select("missing, DETERMINISTIC", 13)
	if err != nil || face == nil {
		t.Fatalf("select = %v, %v", face, err)
	}
	if _, err := book.Select("missing", 13); !errors.Is(err, ErrUnsupportedFont) {
		t.Fatalf("missing font error = %v", err)
	}
	if _, err := book.Select("@missing", 13); !errors.Is(err, ErrUnsupportedFont) {
		t.Fatalf("at-prefixed missing font error = %v", err)
	}
}

type testFontFace struct {
	FontFace
}

type sparseFallbackFace struct{ FontFace }

func (f *sparseFallbackFace) GlyphAdvance(r rune) (fixed.Int26_6, bool) {
	if r != ' ' && r != 'A' {
		return 0, false
	}
	return f.FontFace.GlyphAdvance('A')
}

func (f *sparseFallbackFace) Glyph(dot fixed.Point26_6, r rune) (image.Rectangle, image.Image, image.Point, fixed.Int26_6, bool) {
	if r != ' ' && r != 'A' {
		return image.Rectangle{}, nil, image.Point{}, 0, false
	}
	return f.FontFace.Glyph(dot, 'A')
}

func (f *sparseFallbackFace) Kern(left, right rune) fixed.Int26_6 {
	if left == ' ' && right == ' ' {
		return -fixed.I(1)
	}
	return 0
}

type firstGlyphFallbackFace struct{ FontFace }

func (f *firstGlyphFallbackFace) GlyphAdvance(r rune) (fixed.Int26_6, bool) {
	if r != '!' {
		return 0, false
	}
	return f.FontFace.GlyphAdvance('A')
}

func (f *firstGlyphFallbackFace) Glyph(dot fixed.Point26_6, r rune) (image.Rectangle, image.Image, image.Point, fixed.Int26_6, bool) {
	if r != '!' {
		return image.Rectangle{}, nil, image.Point{}, 0, false
	}
	return f.FontFace.Glyph(dot, 'A')
}

func (f *firstGlyphFallbackFace) Kern(rune, rune) fixed.Int26_6 { return 0 }

func TestMissingGlyphUsesFirstAvailableWhenDefaultIsMissing(t *testing.T) {
	face := withGlyphFallback(&firstGlyphFallbackFace{FontFace: basicfont.Face7x13})
	advance, ok := face.GlyphAdvance('蚍')
	wantAdvance, wantOK := face.GlyphAdvance('!')
	if !ok || !wantOK || advance != wantAdvance {
		t.Fatalf("missing advance = %v,%t; first glyph advance = %v,%t", advance, ok, wantAdvance, wantOK)
	}
	_, mask, _, _, ok := face.Glyph(fixed.Point26_6{}, '蚍')
	_, wantMask, _, _, wantOK := face.Glyph(fixed.Point26_6{}, '!')
	if !ok || !wantOK || mask.Bounds() != wantMask.Bounds() {
		t.Fatalf("missing glyph mask = %v,%t; first glyph mask = %v,%t", mask, ok, wantMask, wantOK)
	}
}

func TestMissingGlyphFallbackIsConsistentForDrawingAndMeasurement(t *testing.T) {
	face := withGlyphFallback(&sparseFallbackFace{FontFace: basicfont.Face7x13})
	const missing = '蚍'

	advance, ok := face.GlyphAdvance(missing)
	spaceAdvance, spaceOK := face.GlyphAdvance(' ')
	if !ok || !spaceOK || advance != spaceAdvance {
		t.Fatalf("missing glyph advance = %v,%t; space = %v,%t", advance, ok, spaceAdvance, spaceOK)
	}
	glyph, mask, _, glyphAdvance, ok := face.Glyph(fixed.Point26_6{}, missing)
	wantGlyph, wantMask, _, wantAdvance, wantOK := face.Glyph(fixed.Point26_6{}, ' ')
	if !ok || !wantOK || glyph != wantGlyph || glyphAdvance != wantAdvance || mask.Bounds() != wantMask.Bounds() {
		t.Fatalf("missing glyph drawing = %v,%v,%v; space = %v,%v,%v", glyph, mask, glyphAdvance, wantGlyph, wantMask, wantAdvance)
	}

	missingImage, err := RasterizeText(face, string(missing), color.White)
	if err != nil {
		t.Fatal(err)
	}
	spaceImage, err := RasterizeText(face, " ", color.White)
	if err != nil {
		t.Fatal(err)
	}
	if missingImage.Bounds() != spaceImage.Bounds() || !imagesEqual(missingImage, spaceImage) {
		t.Fatal("missing-character raster does not match fallback-space raster")
	}

	missingMetrics, err := MeasureText(face, string([]rune{missing, missing}))
	if err != nil {
		t.Fatal(err)
	}
	spaceMetrics, err := MeasureText(face, "  ")
	if err != nil {
		t.Fatal(err)
	}
	if missingMetrics.Width != spaceMetrics.Width || missingMetrics.Width >= fixedToPixels(2*spaceAdvance) {
		t.Fatalf("fallback measurement width=%d; space width=%d, expected fallback kerning", missingMetrics.Width, spaceMetrics.Width)
	}
}

func imagesEqual(a, b *image.RGBA) bool {
	if a.Bounds() != b.Bounds() {
		return false
	}
	for y := a.Bounds().Min.Y; y < a.Bounds().Max.Y; y++ {
		for x := a.Bounds().Min.X; x < a.Bounds().Max.X; x++ {
			if a.RGBAAt(x, y) != b.RGBAAt(x, y) {
				return false
			}
		}
	}
	return true
}

func TestFontSystemCandidateSelectionAndDefaultFallback(t *testing.T) {
	book := NewFontBook()
	second := &testFontFace{FontFace: basicfont.Face7x13}
	if err := book.Register("First", basicfont.Face7x13); err != nil {
		t.Fatal(err)
	}
	if err := book.Register("Second", second); err != nil {
		t.Fatal(err)
	}
	system := &FontSystem{Book: book, DefaultFont: "Second", DefaultSize: 12}

	tests := []struct {
		name string
		want FontFace
	}{
		{name: "First", want: basicfont.Face7x13},
		{name: "Second,First", want: second},
		{name: "missing, Second, First", want: second},
		{name: "@First", want: basicfont.Face7x13},
		{name: "missing", want: second},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := system.Select(test.name, 12)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("Select(%q) returned %v, want %v", test.name, got, test.want)
			}
		})
	}
}

func TestFontSystemFallsBackWhenDefaultMissing(t *testing.T) {
	book := NewFontBook()
	if err := book.Register("Chosen", basicfont.Face7x13); err != nil {
		t.Fatal(err)
	}
	system := &FontSystem{Book: book, DefaultFont: "unavailable", DefaultSize: 12}
	for _, name := range []string{"also unavailable", "", "@"} {
		face, err := system.Select(name, 12)
		if err != nil {
			t.Fatalf("Select(%q) error = %v", name, err)
		}
		if face != basicfont.Face7x13 {
			t.Fatalf("Select(%q) = %v, want first registered face", name, face)
		}
	}
}

func TestFontSystemMissingFontsRemainDiagnosable(t *testing.T) {
	system := &FontSystem{Book: NewFontBook(), DefaultFont: "unavailable", DefaultSize: 12}
	_, err := system.Select("requested", 12)
	if !errors.Is(err, ErrUnsupportedFont) {
		t.Fatalf("missing fonts error = %v", err)
	}
	for _, name := range []string{"requested", "unavailable", "no explicitly registered fonts", "no discovered system fonts"} {
		if !strings.Contains(err.Error(), name) {
			t.Fatalf("missing fonts error %q does not mention %q", err, name)
		}
	}
}

func TestLayoutUsesUTF16OffsetsAndRealGlyphs(t *testing.T) {
	layout, err := LayoutTextWithOptions(basicfont.Face7x13, "AB\nCD", TextLayoutOptions{Mode: WrapCharacter, MaxWidth: 8, Kinsoku: true})
	if err != nil {
		t.Fatal(err)
	}
	if layout.UTF16Length != 5 || len(layout.Lines) != 4 {
		t.Fatalf("layout = %+v", layout)
	}
	if layout.Lines[0].Text != "A" || layout.Lines[1].Text != "B" || layout.Lines[2].Text != "C" || layout.Lines[3].Text != "D" {
		t.Fatalf("wrapped lines = %+v", layout.Lines)
	}
	if layout.Lines[2].StartUTF16 != 3 || layout.Lines[3].StartUTF16 != 4 {
		t.Fatalf("UTF-16 offsets = %+v", layout.Lines)
	}
}

func TestRasterizeTextProducesGlyphPixels(t *testing.T) {
	img, err := RasterizeText(basicfont.Face7x13, "A", color.RGBA{R: 255, A: 255})
	if err != nil {
		t.Fatal(err)
	}
	nonzero, minX, minY, maxX, maxY := 0, img.Rect.Dx(), img.Rect.Dy(), -1, -1
	alphaCounts := make(map[uint8]int)
	for y := 0; y < img.Rect.Dy(); y++ {
		for x := 0; x < img.Rect.Dx(); x++ {
			alpha := img.RGBAAt(x, y).A
			if alpha == 0 {
				continue
			}
			nonzero++
			alphaCounts[alpha]++
			minX, minY = min(minX, x), min(minY, y)
			maxX, maxY = max(maxX, x), max(maxY, y)
		}
	}
	if nonzero == 0 {
		t.Fatal("real face rasterization produced no pixels")
	}
	if minX < 0 || minY < 0 || maxX < minX || maxY < minY {
		t.Fatalf("invalid glyph bounds: (%d,%d)-(%d,%d)", minX, minY, maxX, maxY)
	}
	if len(alphaCounts) != 1 || alphaCounts[255] != nonzero {
		t.Fatalf("bitmap face coverage distribution = %v, want binary source coverage", alphaCounts)
	}
	if maxX-minX+1 >= img.Rect.Dx() || maxY-minY+1 >= img.Rect.Dy() {
		t.Fatalf("glyph bounds (%d,%d)-(%d,%d) unexpectedly fill raster bounds %v", minX, minY, maxX, maxY, img.Rect)
	}
	t.Logf("glyph coverage pixels=%d alpha=%v bounds=(%d,%d)-(%d,%d) canvas=%dx%d", nonzero, alphaCounts, minX, minY, maxX, maxY, img.Rect.Dx(), img.Rect.Dy())
}
func TestFontFaceStylesEmboldenAndObliqueGlyphs(t *testing.T) {
	base := basicfont.Face7x13
	services := DefaultServices()
	if err := services.RegisterFontFace("Styled", base); err != nil {
		t.Fatal(err)
	}
	boldFace, err := services.fontFace(&FontState{Face: "Styled", Height: 13, Bold: true})
	if err != nil {
		t.Fatal(err)
	}
	bold, ok := boldFace.(*styledFontFace)
	if !ok || !bold.bold {
		t.Fatalf("bold state resolved to %T, want emboldened face", boldFace)
	}
	italic := &styledFontFace{FontFace: base, italic: true}
	_, normalMask, _, _, normalOK := base.Glyph(fixed.Point26_6{}, 'A')
	_, boldMask, _, _, boldOK := bold.Glyph(fixed.Point26_6{}, 'A')
	_, italicMask, _, _, italicOK := italic.Glyph(fixed.Point26_6{}, 'A')
	if !normalOK || !boldOK || !italicOK {
		t.Fatal("expected A in every styled face")
	}
	coverage := func(mask image.Image) int {
		total := 0
		for y := mask.Bounds().Min.Y; y < mask.Bounds().Max.Y; y++ {
			for x := mask.Bounds().Min.X; x < mask.Bounds().Max.X; x++ {
				a, _, _, _ := mask.At(x, y).RGBA()
				total += int(a)
			}
		}
		return total
	}
	if coverage(boldMask) <= coverage(normalMask) {
		t.Fatalf("bold coverage = %d, normal coverage = %d", coverage(boldMask), coverage(normalMask))
	}
	if boldMask.Bounds().Dx() <= normalMask.Bounds().Dx() || italicMask.Bounds().Dx() <= normalMask.Bounds().Dx() {
		t.Fatalf("styled glyph widths bold=%d italic=%d normal=%d", boldMask.Bounds().Dx(), italicMask.Bounds().Dx(), normalMask.Bounds().Dx())
	}
}

func TestRasterizeTextRejectsMissingGlyph(t *testing.T) {
	if _, err := RasterizeText(basicfont.Face7x13, "Ω", color.White); !errors.Is(err, ErrUnsupportedFont) {
		t.Fatalf("missing glyph error = %v", err)
	}
}
