package runtime

import (
	"errors"
	"testing"

	"github.com/eslzzyl/krkrgo/internal/tjs"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

func newTextRenderTestContext(t *testing.T) (*Services, *tjs.VM, tjs.Value) {
	t.Helper()
	services := DefaultServices()
	if err := services.RegisterFontFace("normal", basicfont.Face7x13); err != nil {
		t.Fatal(err)
	}
	vm := tjs.NewVM()
	object := vm.Objects.New(tjs.CategoryDictionary)
	context := tjs.ObjectValue(object.Ref())
	if _, err := services.textRenderNative(vm, context, "@initialize", nil, nil); err != nil {
		t.Fatal(err)
	}
	return services, vm, context
}

func TestTextRenderBaseRegistrationAndDefaults(t *testing.T) {
	session := NewSession(DefaultServices(), 256)
	if _, ok := session.VM.Globals["TextRenderBase"]; !ok {
		t.Fatal("TextRenderBase class was not registered")
	}
	services, vm, context := newTextRenderTestContext(t)
	value, err := services.textRenderNative(vm, context, "get:defaultFontSize", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := value.Float(); err != nil || n != 24 {
		t.Fatalf("defaultFontSize = %v, %v", n, err)
	}
}

func TestTextRenderFaceAppliesBoldAndItalic(t *testing.T) {
	services, _, _ := newTextRenderTestContext(t)
	state := newTextRenderState()
	state.current["Face"] = tjs.StringValue("normal")
	state.current["FontSize"] = tjs.RealValue(13)

	normal, err := textRenderFace(services, state)
	if err != nil {
		t.Fatal(err)
	}
	state.current["Bold"] = tjs.IntegerValue(1)
	bold, err := textRenderFace(services, state)
	if err != nil {
		t.Fatal(err)
	}
	boldFace, ok := bold.(*styledFontFace)
	if !ok || !boldFace.bold || boldFace.italic {
		t.Fatalf("bold TextRender face = %#v, want bold styled face", bold)
	}
	normalBounds, _, _, _, normalOK := normal.Glyph(fixed.Point26_6{}, 'A')
	boldBounds, _, _, _, boldOK := bold.Glyph(fixed.Point26_6{}, 'A')
	if !normalOK || !boldOK || boldBounds.Dx() <= normalBounds.Dx() {
		t.Fatalf("bold glyph bounds = %v, normal = %v", boldBounds, normalBounds)
	}

	state.current["Bold"] = tjs.IntegerValue(0)
	state.current["Italic"] = tjs.IntegerValue(1)
	italic, err := textRenderFace(services, state)
	if err != nil {
		t.Fatal(err)
	}
	italicFace, ok := italic.(*styledFontFace)
	if !ok || italicFace.bold || !italicFace.italic {
		t.Fatalf("italic TextRender face = %#v, want italic styled face", italic)
	}
}

func TestTextRenderBaseLayoutAndCharacterContract(t *testing.T) {
	services, vm, context := newTextRenderTestContext(t)
	if _, err := services.textRenderNative(vm, context, "setRenderSize", []tjs.Value{tjs.IntegerValue(8), tjs.IntegerValue(200)}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := services.textRenderNative(vm, context, "render", []tjs.Value{tjs.StringValue("AB\nCD"), tjs.IntegerValue(0), tjs.RealValue(10), tjs.RealValue(0), tjs.IntegerValue(0)}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := services.textRenderNative(vm, context, "done", nil, nil); err != nil {
		t.Fatal(err)
	}
	value, err := services.textRenderNative(vm, context, "getCharacters", []tjs.Value{tjs.IntegerValue(0), tjs.IntegerValue(0)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	items, err := vm.ArrayItems(value)
	if err != nil || len(items) != 4 {
		t.Fatalf("characters = %v, %v", len(items), err)
	}
	first, ok := vm.Objects.Lookup(items[0].ObjectRef().Object)
	if !ok {
		t.Fatal("character dictionary missing")
	}
	text, ok := first.Get("text")
	if !ok || text.Text() != "A" {
		t.Fatalf("first character text = %q", text.Text())
	}
	renderText, err := services.textRenderNative(vm, context, "get:renderText", nil, nil)
	if err != nil || renderText.Text() != "A\nB\nC\nD\n" {
		t.Fatalf("renderText = %q, %v", renderText.Text(), err)
	}
}

func TestTextRenderBaseBudgetAndUnknownOperation(t *testing.T) {
	services, vm, context := newTextRenderTestContext(t)
	if _, err := services.textRenderNative(vm, context, "setRenderSize", []tjs.Value{tjs.IntegerValue(100), tjs.IntegerValue(100)}, nil); err != nil {
		t.Fatal(err)
	}
	budget := uint64(0)
	if _, err := services.textRenderNative(vm, context, "render", []tjs.Value{tjs.StringValue("A"), tjs.IntegerValue(0), tjs.RealValue(1), tjs.RealValue(0), tjs.IntegerValue(0)}, &budget); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("budget error = %v", err)
	}
	if _, err := services.textRenderNative(vm, context, "notAnOperation", nil, nil); !errors.Is(err, ErrUnsupportedNative) {
		t.Fatalf("unknown operation error = %v", err)
	}
}
