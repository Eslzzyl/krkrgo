package runtime

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/eslzzyl/krkrgo/internal/core"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

type fallbackFingerprintFace struct{ FontFace }

func (f fallbackFingerprintFace) Glyph(dot fixed.Point26_6, r rune) (image.Rectangle, image.Image, image.Point, fixed.Int26_6, bool) {
	bounds, mask, maskp, _, ok := f.FontFace.Glyph(dot, r)
	return bounds, mask, maskp, fixed.I(17), ok
}

func (f fallbackFingerprintFace) GlyphAdvance(r rune) (fixed.Int26_6, bool) {
	if _, ok := f.FontFace.GlyphAdvance(r); !ok {
		return 0, false
	}
	return fixed.I(17), true
}

func TestScanSystemFontsOrderFamilyAndInvalidFile(t *testing.T) {
	root := t.TempDir()
	zDir := filepath.Join(root, "z")
	aDir := filepath.Join(root, "a")
	for _, dir := range []string{zDir, aDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	zPath := filepath.Join(zDir, "not-the-family-name.ttf")
	aPath := filepath.Join(aDir, "also-not-the-family-name.otf")
	for _, path := range []string{zPath, aPath} {
		if err := os.WriteFile(path, goregular.TTF, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	badPath := filepath.Join(root, "broken.ttf")
	if err := os.WriteFile(badPath, []byte("not a font"), 0o600); err != nil {
		t.Fatal(err)
	}

	fonts, errs := ScanSystemFonts(zDir, aDir, root)
	if len(errs) == 0 || !errors.Is(errs[0], ErrInvalidFont) {
		t.Fatalf("invalid-font diagnostics = %v", errs)
	}
	if len(fonts) != 1 {
		t.Fatalf("discovered fonts = %+v", fonts)
	}
	if fonts[0].Family != "Go" {
		t.Fatalf("family = %q, want name-table Family Go", fonts[0].Family)
	}
	if fonts[0].Path != aPath {
		t.Fatalf("duplicate-family winner = %q, want lexical first %q", fonts[0].Path, aPath)
	}
	face, err := fonts[0].Factory(12)
	if err != nil || face == nil {
		t.Fatalf("factory face = %v, %v", face, err)
	}
	face.Close()
}
func TestScanSystemFontsTTC(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "collection.ttc")
	data := makeFontCollection(goregular.TTF, goregular.TTF)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	fonts, errs := ScanSystemFonts(root)
	if len(errs) != 0 || len(fonts) != 1 {
		t.Fatalf("TTC scan: fonts=%v errs=%v", fonts, errs)
	}
	if fonts[0].Family != "Go" || fonts[0].Path != path {
		t.Fatalf("TTC face = %+v", fonts[0])
	}
	face, err := fonts[0].Factory(12)
	if err != nil || face == nil {
		t.Fatalf("TTC factory = %v, %v", face, err)
	}
	face.Close()
}

func makeFontCollection(fonts ...[]byte) []byte {
	headerSize := 12 + 4*len(fonts)
	result := make([]byte, headerSize)
	copy(result, "ttcf")
	binary.BigEndian.PutUint32(result[4:], 0x00010000)
	binary.BigEndian.PutUint32(result[8:], uint32(len(fonts)))
	for i, fontData := range fonts {
		offset := len(result)
		binary.BigEndian.PutUint32(result[12+i*4:], uint32(offset))
		face := append([]byte(nil), fontData...)
		numTables := int(binary.BigEndian.Uint16(face[4:6]))
		for table := 0; table < numTables; table++ {
			tableOffset := 12 + table*16 + 8
			binary.BigEndian.PutUint32(face[tableOffset:], binary.BigEndian.Uint32(face[tableOffset:])+uint32(offset))
		}
		result = append(result, face...)
	}
	return result
}

func TestFontFamilyNameNormalizationAndPlatformFallbackPriority(t *testing.T) {
	book := NewFontBook()
	requestedFace := &testFontFace{FontFace: basicfont.Face7x13}
	fallbackFace := &testFontFace{FontFace: basicfont.Face7x13}
	if err := book.Register("MS ゴシック", requestedFace); err != nil {
		t.Fatal(err)
	}
	if face, err := book.Select("ＭＳ　ゴシック", 12); err != nil || face != requestedFace {
		t.Fatalf("normalized family alias = %v, %v", face, err)
	}

	book = NewFontBook()
	var candidate string
	switch runtime.GOOS {
	case "darwin":
		candidate = darwinSystemFontFallbackFamilies[0]
	case "linux":
		candidate = linuxSystemFontFallbackFamilies[0]
	case "windows":
		candidate = windowsSystemFontFallbackFamilies[0]
	default:
		t.Skip("no platform font fallback list")
	}
	if err := book.Register(candidate, fallbackFace); err != nil {
		t.Fatal(err)
	}
	system := &FontSystem{Book: book, DefaultFont: "ＭＳ ゴシック", DefaultSize: 12}
	face, err := system.Select("メイリオ,ＭＳ ゴシック", 12)
	if err != nil || face != fallbackFace {
		t.Fatalf("platform fallback for missing Japanese aliases = %v, %v", face, err)
	}
}
func TestSerifFallbackPreservesMinchoIntentAndRasterizesSelectedGlyph(t *testing.T) {
	serifCandidates := systemFontFallbackCandidates("ＭＳ Ｐ明朝")
	if len(serifCandidates) == 0 {
		t.Skip("no platform font fallback list")
	}
	var sansCandidate string
	switch runtime.GOOS {
	case "darwin":
		if serifCandidates[0] != "Hiragino Mincho ProN" {
			t.Fatalf("macOS Mincho fallback = %q, want Hiragino Mincho ProN", serifCandidates[0])
		}
		sansCandidate = darwinSystemFontFallbackFamilies[0]
	case "linux":
		if !strings.Contains(serifCandidates[0], "Serif") {
			t.Fatalf("Linux Mincho fallback = %q, want a CJK serif family", serifCandidates[0])
		}
		sansCandidate = linuxSystemFontFallbackFamilies[0]
	case "windows":
		if !strings.Contains(strings.ToLower(serifCandidates[0]), "mincho") {
			t.Fatalf("Windows Mincho fallback = %q, want a Mincho family", serifCandidates[0])
		}
		sansCandidate = windowsSystemFontFallbackFamilies[0]
	}

	book := NewFontBook()
	sansFace := &testFontFace{FontFace: basicfont.Face7x13}
	serifFace := fallbackFingerprintFace{FontFace: basicfont.Face7x13}
	for _, entry := range []fontEntry{
		{name: sansCandidate, factory: func(float64) (FontFace, error) { return sansFace, nil }, discovered: true},
		{name: serifCandidates[0], factory: func(float64) (FontFace, error) { return serifFace, nil }, discovered: true},
	} {
		if err := book.register(entry); err != nil {
			t.Fatal(err)
		}
	}
	system := &FontSystem{Book: book, DefaultFont: "ＭＳ ゴシック", DefaultSize: 12}
	face, err := system.Select("ＭＳ Ｐ明朝", 12)
	if err != nil {
		t.Fatalf("Mincho fallback selection failed: %v", err)
	}
	advance, ok := face.GlyphAdvance('A')
	if !ok || advance != fixed.I(17) {
		t.Fatalf("selected fallback advance = %v,%t, want 17px", advance, ok)
	}
	bounds, mask, _, glyphAdvance, ok := face.Glyph(fixed.Point26_6{}, 'A')
	if !ok || bounds.Empty() || mask == nil || glyphAdvance != fixed.I(17) {
		t.Fatalf("selected fallback glyph = bounds %v, mask %v, advance %v, ok %t", bounds, mask, glyphAdvance, ok)
	}
}

func TestNewServicesRetainsDiscoveredSystemFonts(t *testing.T) {
	expected := NewFontBook()
	expected.DiscoverSystemFonts()
	expectedNames := expected.Names()

	services := NewServices(nil, "", core.Limits{})
	if services.FontBook == nil || services.FontSystem == nil {
		t.Fatalf("NewServices font state = book %v, system %v", services.FontBook, services.FontSystem)
	}
	if services.FontSystem.Book != services.FontBook {
		t.Fatal("NewServices FontSystem is not bound to its discovered FontBook")
	}
	names := services.FontBook.Names()
	if len(names) != len(expectedNames) {
		t.Fatalf("NewServices registered %d font families, DiscoverSystemFonts registered %d", len(names), len(expectedNames))
	}
	for i := range names {
		if names[i] != expectedNames[i] {
			t.Fatalf("NewServices family %d = %q, want %q", i, names[i], expectedNames[i])
		}
	}
	face, err := services.FontSystem.Select("unavailable Japanese family", 12)
	if len(expectedNames) == 0 {
		if face != nil || !errors.Is(err, ErrUnsupportedFont) {
			t.Fatalf("fontless NewServices fallback = %v, %v; want typed unsupported font", face, err)
		}
	} else if err != nil || face == nil {
		t.Fatalf("NewServices did not select its discovered fallback: face=%v err=%v", face, err)
	}
	if face != nil {
		face.Close()
	}
}

func TestRegisterSystemFontsPreservesExplicitFontAndMissingError(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "go.ttf")
	if err := os.WriteFile(path, goregular.TTF, 0o600); err != nil {
		t.Fatal(err)
	}
	book := NewFontBook()
	if err := book.Register("Go", basicfont.Face7x13); err != nil {
		t.Fatal(err)
	}
	if errs := book.RegisterSystemFonts(root); len(errs) != 0 {
		t.Fatalf("register diagnostics = %v", errs)
	}
	face, err := book.Select("go", 12)
	if err != nil || face != basicfont.Face7x13 {
		t.Fatalf("explicit font not preserved: face=%v err=%v", face, err)
	}
	system := &FontSystem{Book: book, DefaultFont: "missing", DefaultSize: 12}
	fallback, err := system.Select("also missing", 12)
	if err != nil || fallback != basicfont.Face7x13 {
		t.Fatalf("fallback did not preserve explicit font priority: face=%v err=%v", fallback, err)
	}
	if _, err := book.Select("not-installed", 12); !errors.Is(err, ErrUnsupportedFont) {
		t.Fatalf("missing font error = %v", err)
	}
}

func TestRegisterSystemFontsInitializesZeroValueFontBook(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "go.ttf")
	if err := os.WriteFile(path, goregular.TTF, 0o600); err != nil {
		t.Fatal(err)
	}

	book := &FontBook{}
	if errs := book.RegisterSystemFonts(root); len(errs) != 0 {
		t.Fatalf("register diagnostics = %v", errs)
	}
	if names := book.Names(); len(names) != 1 || names[0] != "Go" {
		t.Fatalf("zero-value FontBook discovered names = %v, want [Go]", names)
	}
}

func TestFontFactorySelectionPreservesErrorCause(t *testing.T) {
	cause := errors.New("font factory failed")
	book := NewFontBook()
	if err := book.RegisterFactory("Broken", func(float64) (FontFace, error) {
		return nil, cause
	}); err != nil {
		t.Fatal(err)
	}

	for name, selectFont := range map[string]func() (FontFace, error){
		"requested": func() (FontFace, error) {
			return book.Select("Broken", 12)
		},
		"fallback": func() (FontFace, error) {
			return (&FontSystem{Book: book}).Select("missing", 12)
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := selectFont(); !errors.Is(err, ErrInvalidFont) || !errors.Is(err, cause) {
				t.Fatalf("factory error = %v, want ErrInvalidFont and original cause", err)
			}
		})
	}
}

func TestFontSystemFallsBackToDiscoveredSystemFont(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "go.ttf")
	if err := os.WriteFile(path, goregular.TTF, 0o600); err != nil {
		t.Fatal(err)
	}
	book := NewFontBook()
	if errs := book.RegisterSystemFonts(root); len(errs) != 0 {
		t.Fatalf("register diagnostics = %v", errs)
	}
	system := &FontSystem{Book: book, DefaultFont: "unavailable", DefaultSize: 12}
	for _, requested := range []string{"not-installed", ""} {
		face, err := system.Select(requested, 12)
		if err != nil || face == nil {
			t.Fatalf("Select(%q) with discovered font = %v, %v", requested, face, err)
		}
		if _, err := MetricsForFace(face); err != nil {
			t.Fatalf("discovered face metrics = %v", err)
		}
		face.Close()
	}
}
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
