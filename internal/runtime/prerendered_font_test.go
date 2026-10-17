package runtime

import (
	"encoding/binary"
	"image"
	"image/color"
	"testing"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

type testGlyphData struct {
	advance  fixed.Int26_6
	coverage uint8
}

type testGlyphFace struct {
	glyphs map[rune]testGlyphData
}

func (f *testGlyphFace) Glyph(dot fixed.Point26_6, character rune) (image.Rectangle, image.Image, image.Point, fixed.Int26_6, bool) {
	glyph, ok := f.glyphs[character]
	if !ok {
		return image.Rectangle{}, nil, image.Point{}, 0, false
	}
	mask := image.NewAlpha(image.Rect(0, 0, 1, 1))
	mask.Pix[0] = glyph.coverage
	x, y := dot.X.Round(), dot.Y.Round()
	return image.Rect(x, y-1, x+1, y), mask, image.Point{}, glyph.advance, true
}

func (f *testGlyphFace) GlyphAdvance(character rune) (fixed.Int26_6, bool) {
	glyph, ok := f.glyphs[character]
	return glyph.advance, ok
}

func (f *testGlyphFace) GlyphBounds(character rune) (fixed.Rectangle26_6, fixed.Int26_6, bool) {
	glyph, ok := f.glyphs[character]
	if !ok {
		return fixed.Rectangle26_6{}, 0, false
	}
	return fixed.Rectangle26_6{Min: fixed.P(0, -1), Max: fixed.P(1, 0)}, glyph.advance, true
}

func (*testGlyphFace) Kern(rune, rune) fixed.Int26_6 { return 0 }
func (*testGlyphFace) Metrics() font.Metrics {
	return font.Metrics{Height: fixed.I(2), Ascent: fixed.I(1), Descent: fixed.I(1)}
}
func (*testGlyphFace) Close() error { return nil }

func testPrerenderedFontData(character rune, coverage byte, advance int16) []byte {
	const indexOffset = prerenderedFontHeaderSize
	const itemOffset = indexOffset + 2
	const bitmapOffset = itemOffset + prerenderedFontItemSize
	data := make([]byte, bitmapOffset+1)
	copy(data, prerenderedFontMagic)
	data[22] = 1
	data[23] = 2
	binary.LittleEndian.PutUint32(data[24:28], 1)
	binary.LittleEndian.PutUint32(data[28:32], indexOffset)
	binary.LittleEndian.PutUint32(data[32:36], itemOffset)
	binary.LittleEndian.PutUint16(data[indexOffset:indexOffset+2], uint16(character))
	binary.LittleEndian.PutUint32(data[itemOffset:itemOffset+4], bitmapOffset)
	binary.LittleEndian.PutUint16(data[itemOffset+4:itemOffset+6], 1)
	binary.LittleEndian.PutUint16(data[itemOffset+6:itemOffset+8], 1)
	binary.LittleEndian.PutUint16(data[itemOffset+8:itemOffset+10], 0xffff)
	binary.LittleEndian.PutUint16(data[itemOffset+10:itemOffset+12], 2)
	binary.LittleEndian.PutUint16(data[itemOffset+12:itemOffset+14], uint16(advance))
	binary.LittleEndian.PutUint16(data[itemOffset+16:itemOffset+18], uint16(advance))
	data[bitmapOffset] = coverage
	return data
}

func TestPrerenderedFontParsesAndRendersMappedGlyph(t *testing.T) {
	parsed, err := parsePrerenderedFont(testPrerenderedFontData('A', 64, 9))
	if err != nil {
		t.Fatal(err)
	}
	glyph, ok := parsed.glyph('A')
	if !ok || glyph.alpha == nil || glyph.alpha.Pix[0] != 64 {
		t.Fatalf("parsed glyph = %#v, %t", glyph, ok)
	}
	face := &prerenderedFontFace{FontFace: basicfont.Face7x13, font: parsed}
	advance, ok := face.GlyphAdvance('A')
	if !ok || advance != fixed.I(9) {
		t.Fatalf("mapped advance = %v, %t; want 9 px", advance, ok)
	}
	bounds, mask, maskp, glyphAdvance, ok := face.Glyph(fixed.P(20, 30), 'A')
	if !ok || bounds != image.Rect(19, 28, 20, 29) || maskp != (image.Point{}) || glyphAdvance != fixed.I(9) {
		t.Fatalf("mapped glyph = %v %v %v %v %t", bounds, mask, maskp, glyphAdvance, ok)
	}
	if got := color.AlphaModel.Convert(mask.At(0, 0)).(color.Alpha).A; got != 64 {
		t.Fatalf("mapped glyph coverage = %d, want 64", got)
	}
}

func TestPrerenderedFontRejectsInvalidIndex(t *testing.T) {
	data := testPrerenderedFontData('A', 64, 9)
	binary.LittleEndian.PutUint32(data[28:32], uint32(len(data)+1))
	if _, err := parsePrerenderedFont(data); err == nil {
		t.Fatal("invalid character index offset was accepted")
	}
}

func TestGlyphFallbackChainPrefersRequestedFaceAndFillsMissingGlyphs(t *testing.T) {
	primary := &testGlyphFace{glyphs: map[rune]testGlyphData{
		'A': {advance: fixed.I(7), coverage: 64},
	}}
	fallback := &testGlyphFace{glyphs: map[rune]testGlyphData{
		'A': {advance: fixed.I(12), coverage: 255},
		'海': {advance: fixed.I(14), coverage: 192},
	}}
	book := NewFontBook()
	if err := book.Register("Original Face", primary); err != nil {
		t.Fatal(err)
	}
	if err := book.Register("CJK Fallback", fallback); err != nil {
		t.Fatal(err)
	}
	face := withGlyphFallbackChain(primary, book, "Original Face", 12)

	advance, ok := face.GlyphAdvance('A')
	if !ok || advance != fixed.I(7) {
		t.Fatalf("original face advance = %v, %t; want 7 px", advance, ok)
	}
	_, mask, _, _, ok := face.Glyph(fixed.P(0, 1), 'A')
	if !ok || color.AlphaModel.Convert(mask.At(0, 0)).(color.Alpha).A != 64 {
		t.Fatalf("original face glyph did not retain its coverage: %v", mask)
	}
	advance, ok = face.GlyphAdvance('海')
	if !ok || advance != fixed.I(14) {
		t.Fatalf("fallback glyph advance = %v, %t; want 14 px", advance, ok)
	}
	_, mask, _, _, ok = face.Glyph(fixed.P(0, 1), '海')
	if !ok || color.AlphaModel.Convert(mask.At(0, 0)).(color.Alpha).A != 192 {
		t.Fatalf("missing glyph was not rendered from the fallback face: %v", mask)
	}
}
