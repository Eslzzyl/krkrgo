package runtime

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"

	"github.com/eslzzyl/krkrgo/internal/assets"
	"github.com/eslzzyl/krkrgo/internal/core"
)

func TestNativeFontBoundPropertyReferenceKeepsDynamicMembers(t *testing.T) {
	session := NewSession(DefaultServices(), 10_000)
	defer session.Close()

	got, err := session.Evaluate(`
first = new Font();
second = new Font();
first.__propertyHook = %[value:4];
second.__propertyHook = %[value:9];
first.face = "first";
second.face = "second";
var reference = (&first.face) incontextof second;
var original = *reference;
*reference = "bound";
result = original == "second" && second.face == "bound" && first.face == "first" && first.__propertyHook.value == 4 && second.__propertyHook.value == 9;
`)
	if err != nil {
		t.Fatal(err)
	}
	got, err = session.Evaluate("result")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := got.Truth(); err != nil || !ok {
		t.Fatalf("bound native Font property reference = %v, %v; want true", got, err)
	}
}
func TestFontMapPrerenderedFontSharesMappingByFontState(t *testing.T) {
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "mapped.tft"), testPrerenderedFontData('A', 64, 9), 0o600); err != nil {
		t.Fatal(err)
	}
	storage, err := assets.OpenStorage(project, core.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	services := NewServices(storage, "", core.Limits{})
	if err := services.RegisterFontFace("Synthetic", basicfont.Face7x13); err != nil {
		t.Fatal(err)
	}
	session := NewSession(services, 10_000)
	defer session.Close()

	if _, err := session.Evaluate(`
first = new Font("Synthetic", 13);
second = new Font("Synthetic", 13);
caseVariant = new Font("synthetic", 13);
styled = new Font("Synthetic", 13);
styled.bold = true;
first.mapPrerenderedFont("mapped.tft");
`); err != nil {
		t.Fatal(err)
	}
	secondValue, err := session.Evaluate("second")
	if err != nil {
		t.Fatal(err)
	}
	secondFont, err := services.fontFace(services.Font(secondValue.ObjectRef().Object))
	if err != nil {
		t.Fatal(err)
	}
	mappedFace, ok := secondFont.(*prerenderedFontFace)
	if !ok {
		t.Fatalf("matching Font instance face = %T, want shared prerendered mapping", secondFont)
	}
	if advance, ok := mappedFace.GlyphAdvance('A'); !ok || advance != fixed.I(9) {
		t.Fatalf("shared mapped advance = %v, %t; want 9 px", advance, ok)
	}
	for _, name := range []string{"caseVariant", "styled"} {
		value, err := session.Evaluate(name)
		if err != nil {
			t.Fatal(err)
		}
		face, err := services.fontFace(services.Font(value.ObjectRef().Object))
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := face.(*prerenderedFontFace); ok {
			t.Errorf("%s received the mapped font face for a different font state", name)
		}
	}
	if _, err := session.Evaluate(`first.mapPrerenderedFont("missing.tft")`); err == nil {
		t.Fatal("mapping a missing storage unexpectedly succeeded")
	}
	if _, err := session.Evaluate("second.unmapPrerenderedFont()"); err != nil {
		t.Fatal(err)
	}
	unmappedFace, err := services.fontFace(services.Font(secondValue.ObjectRef().Object))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := unmappedFace.(*prerenderedFontFace); ok {
		t.Fatalf("font face after unmap = %T, want the selected face", unmappedFace)
	}
}
