package runtime

import (
	"errors"
	"math"
	"strings"
	"sync"
	"testing"

	"github.com/eslzzyl/krkrgo/internal/tjs"
	"golang.org/x/image/font/basicfont"
)

func TestNativeLayerConstructionTreeAndInvalidation(t *testing.T) {
	services := DefaultServices()
	session := NewSession(services, 10_000)
	if _, err := session.Evaluate(`root = new Layer()`); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Evaluate(`child = new Layer(root)`); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Evaluate(`arr = root.children`); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Evaluate(`child.parent = null`); err != nil {
		t.Fatal(err)
	}
	evalInt := func(source string) int64 {
		value, err := session.Evaluate(source)
		if err != nil {
			t.Fatalf("%s: %v", source, err)
		}
		n, err := value.Int()
		if err != nil {
			t.Fatalf("%s: %v", source, err)
		}
		return n
	}
	evalTruth := func(source string) bool {
		value, err := session.Evaluate(source)
		if err != nil {
			t.Fatalf("%s: %v", source, err)
		}
		truth, err := value.Truth()
		if err != nil {
			t.Fatalf("%s: %v", source, err)
		}
		return truth
	}
	width := evalInt(`root.width`)
	height := evalInt(`root.height`)
	imageWidth := evalInt(`root.imageWidth`)
	imageHeight := evalInt(`root.imageHeight`)
	sameChildren := evalTruth(`arr===root.children`)
	childCount := evalInt(`arr.count`)
	detached := evalTruth(`child.parent===null`)
	if width != 32 || height != 32 || imageWidth != 32 || imageHeight != 32 || !sameChildren || childCount != 0 || !detached {
		t.Fatalf("layer defaults=%d,%d,%d,%d,%t,%d,%t", width, height, imageWidth, imageHeight, sameChildren, childCount, detached)
	}
	if len(services.Layers) != 2 {
		t.Fatalf("layers=%d", len(services.Layers))
	}
	for id := range services.Layers {
		if _, err := session.VM.Invalidate(tjs.ObjectIDValue(id), services, &session.Budget); err != nil {
			t.Fatal(err)
		}
		break
	}
}
func TestNativeLayerNeutralColorPropertyAndInheritance(t *testing.T) {
	session := NewSession(DefaultServices(), 10_000)
	if _, err := session.executeSource("<layer-neutral-color-test>", `
class AffineLayer extends Layer {
	function AffineLayer() { super.Layer(); }
	function clearColor() { return neutralColor; }
}
layer = new AffineLayer();
`, false); err != nil {
		t.Fatal(err)
	}
	read := func(expression string) int64 {
		t.Helper()
		value, err := session.Evaluate(expression)
		if err != nil {
			t.Fatalf("%s: %v", expression, err)
		}
		n, err := value.Int()
		if err != nil {
			t.Fatalf("%s = %v: %v", expression, value, err)
		}
		return n
	}
	if got := read("layer.neutralColor"); got != 0 {
		t.Fatalf("default neutralColor = %#x, want 0", got)
	}
	if got := read("layer.clearColor()"); got != 0 {
		t.Fatalf("inherited method neutralColor = %#x, want 0", got)
	}
	if _, err := session.Evaluate("layer.type = ltSubtractive"); err != nil {
		t.Fatal(err)
	}
	if got := read("layer.neutralColor"); got != 0xffffff {
		t.Fatalf("subtractive neutralColor = %#x, want 0xffffff", got)
	}
	if _, err := session.Evaluate("layer.neutralColor = 1"); err == nil {
		t.Fatal("assigning read-only Layer.neutralColor succeeded")
	}
	if _, err := session.Evaluate(`class OrdinaryLayerLike {}; ordinary = new OrdinaryLayerLike(); ordinary.neutralColor`); err == nil || !strings.Contains(err.Error(), "neutralColor") {
		t.Fatalf("ordinary object neutralColor error = %v, want missing member", err)
	}
}

func TestNativeLayerImagePropertyContextPropagatesThroughDerivedClasses(t *testing.T) {
	services := DefaultServices()
	session := NewSession(services, 10_000)
	t.Cleanup(func() { _ = session.Close() })
	if _, err := session.executeSource("<derived-layer-image-context-test>", `
class KAGLayer extends Layer {
	function KAGLayer(win, par) { super.Layer(win, par); }
}
class AffineLayer extends KAGLayer {
	function AffineLayer(win, par) { super.KAGLayer(win, par); }
	function readNativeImageWidth() { return imageWidth; }
	function readThroughBase() { return this.readNativeImageWidth(); }
}
class BMPBaseAffineSourceLayer extends AffineLayer {
	function BMPBaseAffineSourceLayer(win, par) { super.AffineLayer(win, par); }
	function readThroughSuper() { return super.readThroughBase(); }
}
layer = new BMPBaseAffineSourceLayer();
`, false); err != nil {
		t.Fatalf("construct derived native Layer: %v", err)
	}
	derived, err := session.Evaluate("layer")
	if err != nil {
		t.Fatal(err)
	}
	id := derived.ObjectRef().Object
	object, ok := session.VM.Objects.Lookup(id)
	if services.Layer(id) == nil {
		category := tjs.ObjectCategory(255)
		if ok {
			category = object.Category
		}
		t.Fatalf("derived receiver object %d (TJS category %d) has no Layer native instance; registered layer IDs=%v", id, category, services.Layers)
	}
	for _, expression := range []string{"layer.imageWidth", "layer.readNativeImageWidth()", "layer.readThroughBase()", "layer.readThroughSuper()", "*(&global.Layer.imageWidth incontextof layer)"} {
		value, err := session.Evaluate(expression)
		if err != nil {
			t.Fatalf("%s: %v", expression, err)
		}
		if got, err := value.Int(); err != nil || got != int64(nativeLayerDefaultSize) {
			t.Errorf("%s = %v, %v; want %d", expression, value, err, nativeLayerDefaultSize)
		}
	}
}

func TestNativeLayerRejectsCycles(t *testing.T) {
	services := DefaultServices()
	session := NewSession(services, 10_000)
	if _, err := session.Evaluate(`root = new Layer()`); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Evaluate(`child = new Layer(root)`); err != nil {
		t.Fatal(err)
	}
	_, err := session.Evaluate(`child.addChild(root)`)
	if !errors.Is(err, ErrLayerTree) {
		t.Fatalf("cycle error=%v", err)
	}
}

func TestNativeLayerPixelsAndClip(t *testing.T) {
	services := DefaultServices()
	session := NewSession(services, 10_000)
	if _, err := session.Evaluate(`l = new Layer()`); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Evaluate(`l.setImageSize(4,4)`); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Evaluate(`l.setClip(1,1,2,2)`); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Evaluate(`l.fillRect(0,0,4,4,-2143281136)`); err != nil {
		t.Fatal(err)
	}
	valueAt := func(source string) int64 {
		value, err := session.Evaluate(source)
		if err != nil {
			t.Fatalf("%s: %v", source, err)
		}
		n, err := value.Int()
		if err != nil {
			t.Fatalf("%s: %v", source, err)
		}
		return n
	}
	outsideRGB := valueAt(`l.getMainPixel(0,0)`)
	outsideAlpha := valueAt(`l.getMaskPixel(0,0)`)
	insideRGB := valueAt(`l.getMainPixel(1,1)`)
	insideAlpha := valueAt(`l.getMaskPixel(1,1)`)
	if outsideRGB != 0 || outsideAlpha != 0 || insideRGB != 4202512 || insideAlpha != 128 {
		t.Fatalf("pixels=%d,%d,%d,%d", outsideRGB, outsideAlpha, insideRGB, insideAlpha)
	}

}

func TestNativeLayerSizingImageSizeFaceAndHoldAlpha(t *testing.T) {
	services := DefaultServices()
	session := NewSession(services, 100_000)
	eval := func(source string) {
		t.Helper()
		if _, err := session.executeSource("<layer-test>", source, false); err != nil {
			t.Fatalf("%s: %v", source, err)
		}
	}
	valueAt := func(source string) int64 {
		t.Helper()
		value, err := session.Evaluate(source)
		if err != nil {
			t.Fatalf("%s: %v", source, err)
		}
		valueInt, err := value.Int()
		if err != nil {
			t.Fatalf("%s: %v", source, err)
		}
		return valueInt
	}
	eval(`l = new Layer(); l.fillRect(0,0,1,1,0x7f123456); l.setSize(40,20)`)
	if got := [4]int64{valueAt(`l.width`), valueAt(`l.height`), valueAt(`l.imageWidth`), valueAt(`l.imageHeight`)}; got != [4]int64{40, 20, 40, 32} {
		t.Fatalf("setSize dimensions = %v", got)
	}
	eval(`l.setSizeToImageSize()`)
	eval(`l.setImageSize(2,2)`)
	if got := [4]int64{valueAt(`l.width`), valueAt(`l.height`), valueAt(`l.imageWidth`), valueAt(`l.imageHeight`)}; got != [4]int64{2, 2, 2, 2} {
		t.Fatalf("setImageSize dimensions = %v", got)
	}
	eval(`l.face=dfOpaque; l.holdAlpha=true; l.fillRect(0,0,1,1,0x80ff0000)`)
	if rgb, alpha := valueAt(`l.getMainPixel(0,0)`), valueAt(`l.getMaskPixel(0,0)`); rgb != 0xff0000 || alpha != 127 {
		t.Fatalf("holdAlpha pixel = %#x,%d", rgb, alpha)
	}
	eval(`l.holdAlpha=false; l.fillRect(0,0,1,1,0x8000ff00); l.face=dfMask; l.fillRect(0,0,1,1,0x12345678)`)
	if rgb, alpha := valueAt(`l.getMainPixel(0,0)`), valueAt(`l.getMaskPixel(0,0)`); rgb != 0x00ff00 || alpha != 0x78 {
		t.Fatalf("face mask pixel = %#x,%d", rgb, alpha)
	}
	eval(`l.imageWidth=4; l.imageHeight=3; l.setImagePos(-2,-1)`)
	if got := [4]int64{valueAt(`l.imageWidth`), valueAt(`l.imageHeight`), valueAt(`l.imageLeft`), valueAt(`l.imageTop`)}; got != [4]int64{4, 3, -2, -1} {
		t.Fatalf("image properties = %v", got)
	}
	if got := valueAt(`l.face`); got != 2 {
		t.Fatalf("face getter = %d, want 2", got)
	}
	if got := valueAt(`l.holdAlpha`); got != 0 {
		t.Fatalf("holdAlpha getter = %d, want 0", got)
	}
	eval(`l.face=dfAlpha; l.colorRect(0,0,1,1,0xffff0000,255)`)
	if rgb, alpha := valueAt(`l.getMainPixel(0,0)`), valueAt(`l.getMaskPixel(0,0)`); rgb != 0xff0000 || alpha != 255 {
		t.Fatalf("colorRect pixel = %#x,%d", rgb, alpha)
	}
	eval(`l.colorRect(0,0,1,1,0x00000000,-255)`)
	if rgb, alpha := valueAt(`l.getMainPixel(0,0)`), valueAt(`l.getMaskPixel(0,0)`); rgb != 0xff0000 || alpha != 0 {
		t.Fatalf("colorRect alpha removal = %#x,%d", rgb, alpha)
	}
	if _, err := session.Evaluate(`l.imageLeft=1`); !errors.Is(err, ErrInvalidLayer) {
		t.Fatalf("invalid imageLeft error = %v", err)
	}
	eval(`l.setSize(1,1); l.setSizeToImageSize()`)
	if got := [4]int64{valueAt(`l.width`), valueAt(`l.height`), valueAt(`l.imageLeft`), valueAt(`l.imageTop`)}; got != [4]int64{4, 3, 0, 0} {
		t.Fatalf("setSizeToImageSize state = %v", got)
	}
	eval(`l.setImageSize(1,1)`)
	if _, err := session.Evaluate(`l.setSize(-1,1)`); !errors.Is(err, ErrInvalidLayer) {
		t.Fatalf("negative setSize error = %v", err)
	}
	if _, err := session.Evaluate(`l.setSize(32769,1)`); !errors.Is(err, ErrInvalidLayer) {
		t.Fatalf("oversized setSize error = %v", err)
	}
	if _, err := session.Evaluate(`l.setSize()`); err == nil {
		t.Fatal("setSize accepted missing dimensions")
	}
	if got := [4]int64{valueAt(`l.width`), valueAt(`l.height`), valueAt(`l.imageWidth`), valueAt(`l.imageHeight`)}; got != [4]int64{1, 1, 1, 1} {
		t.Fatalf("failed size change modified dimensions = %v", got)
	}
	eval(`l.type=ltBinder`)
	if got := valueAt(`l.hasImage`); got != 0 {
		t.Fatalf("binder retained image = %d", got)
	}
	eval(`l.type=ltAlpha; l.hasImage=true`)
	eval(`l.hasImage=false`)
	if got := valueAt(`l.hasImage`); got != 0 {
		t.Fatalf("hasImage after clear = %d", got)
	}
	if _, err := session.Evaluate(`l.imageWidth`); !errors.Is(err, ErrInvalidLayer) {
		t.Fatalf("imageWidth without image error = %v", err)
	}
	if _, err := session.Evaluate(`l.setSizeToImageSize()`); !errors.Is(err, ErrInvalidLayer) {
		t.Fatalf("setSizeToImageSize without image error = %v", err)
	}
	eval(`l.type=ltBinder`)
	if _, err := session.Evaluate(`l.hasImage=true`); !errors.Is(err, ErrInvalidLayer) {
		t.Fatalf("binder image allocation error = %v", err)
	}
	eval(`l.type=ltAlpha; l.hasImage=true`)
	if got := valueAt(`l.hasImage`); got != 1 {
		t.Fatalf("hasImage after allocation = %d", got)
	}
}

func TestLayerDrawTextPixelPreservesDestinationDuringAntialiasing(t *testing.T) {
	// RasterizeText supplies premultiplied source pixels: half-covered red is
	// (128,0,0,128). dfAlpha/bmAlphaOnAlpha must retain the uncovered half of
	// the opaque blue destination rather than multiplying that contribution
	// by alpha a second time.
	destination := []byte{0, 0, 255, 255}
	source := []byte{128, 0, 0, 128}
	layerDrawTextPixel(destination, source, layerFaceAlpha, false)

	if got, want := destination, []byte{128, 0, 127, 255}; string(got) != string(want) {
		t.Fatalf("antialiased text pixel = %v, want %v", got, want)
	}
}
func TestNativeObjectsRemainGCReachableUntilInvalidated(t *testing.T) {
	services := DefaultServices()
	session := NewSession(services, 10_000)
	if _, err := session.Evaluate(`f = new Font()`); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Evaluate(`l = new Layer()`); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Evaluate(`l.font=f`); err != nil {
		t.Fatal(err)
	}
	roots := services.GCRoots()
	for id := range services.Layers {
		found := false
		for _, root := range roots {
			if root.Kind() == tjs.KindObject && root.ObjectRef().Object == id {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("layer %d missing from GC roots", id)
		}
	}
	for id := range services.Fonts {
		found := false
		for _, root := range roots {
			if root.Kind() == tjs.KindObject && root.ObjectRef().Object == id {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("font %d missing from GC roots", id)
		}
	}
}

func TestNativeLayerDefaultFontLifecycle(t *testing.T) {
	services := DefaultServices()
	if err := services.RegisterFontFace(defaultFontFaceName, basicfont.Face7x13); err != nil {
		t.Fatal(err)
	}
	session := NewSession(services, 10_000)
	if _, err := session.executeSource("<layer-font-test>", `l = new Layer(); face = l.font.face; height = l.font.height; width = l.font.getTextWidth('A'); textHeight = l.font.getTextHeight('A'); sameFont = l.font === l.font`, false); err != nil {
		t.Fatal(err)
	}
	valueAt := func(source string) tjs.Value {
		t.Helper()
		value, err := session.Evaluate(source)
		if err != nil {
			t.Fatalf("%s: %v", source, err)
		}
		return value
	}
	face := valueAt(`face`).Text()
	height, err := valueAt(`height`).Int()
	if err != nil {
		t.Fatal(err)
	}
	width, err := valueAt(`width`).Int()
	if err != nil {
		t.Fatal(err)
	}
	textHeight, err := valueAt(`textHeight`).Int()
	if err != nil {
		t.Fatal(err)
	}
	sameFont, err := valueAt(`sameFont`).Truth()
	if err != nil {
		t.Fatal(err)
	}
	if face != defaultFontFaceName || height != -12 || width <= 0 || textHeight <= 0 || !sameFont {
		t.Fatalf("default font face=%q height=%d metrics=%d,%d stable=%t", face, height, width, textHeight, sameFont)
	}
	fontValue := valueAt(`l.font`)
	fontID := fontValue.ObjectRef().Object
	if services.Font(fontID) == nil {
		t.Fatal("default Layer.font has no native Font state")
	}
	roots := services.GCRoots()
	rooted := false
	for _, root := range roots {
		if root.Kind() == tjs.KindObject && root.ObjectRef().Object == fontID {
			rooted = true
			break
		}
	}
	if !rooted {
		t.Fatalf("default font %d missing from GC roots", fontID)
	}
	if _, err := session.CollectGarbage(nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := session.VM.Objects.Lookup(fontID); !ok {
		t.Fatal("default Font object was collected while its Layer remained alive")
	}

	layer := session.VM.Globals["l"]
	if _, err := session.VM.Invalidate(layer, services, &session.Budget); err != nil {
		t.Fatal(err)
	}
	if services.Font(fontID) != nil {
		t.Fatal("invalidating Layer retained its default Font state")
	}
	if _, err := session.CollectGarbage(nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := session.VM.Objects.Lookup(fontID); ok {
		t.Fatal("invalidated Layer's default Font object remained reachable")
	}

	if _, err := session.executeSource("<layer-font-test>", `explicitFont = new Font('Explicit Face', 18); explicitLayer = new Layer(); explicitLayer.font = explicitFont; explicitSame = explicitLayer.font === explicitFont`, false); err != nil {
		t.Fatal(err)
	}
	explicitSame, err := valueAt(`explicitSame`).Truth()
	if err != nil || !explicitSame {
		t.Fatalf("explicit Layer.font binding = %t, %v", explicitSame, err)
	}
	explicitFont := session.VM.Globals["explicitFont"]
	explicitID := explicitFont.ObjectRef().Object
	explicitLayer := session.VM.Globals["explicitLayer"]
	if _, err := session.VM.Invalidate(explicitLayer, services, &session.Budget); err != nil {
		t.Fatal(err)
	}
	if services.Font(explicitID) == nil {
		t.Fatal("invalidating Layer released an explicitly bound Font")
	}
}

func TestLayerFontGetterUsesInContextOfReceiver(t *testing.T) {
	services := DefaultServices()
	session := NewSession(services, 10_000)
	if _, err := session.executeSource("<layer-font-context-test>", `
		class DerivedLayer extends Layer {
			function DerivedLayer() { super.Layer(); }
		}
		layer = new DerivedLayer();
		boundFont = (global.Layer incontextof layer).font;
		rejected = false;
		try { (global.Layer incontextof %[]).font; } catch (e) { rejected = true; }
	`, false); err != nil {
		t.Fatal(err)
	}
	layer := session.VM.Globals["layer"]
	font := session.VM.Globals["boundFont"]
	if font.Kind() != tjs.KindObject || services.Font(font.ObjectRef().Object) == nil {
		t.Fatalf("bound Layer.font = %v, want native Font object", font)
	}
	if layerFont := services.LayerFonts[layer.ObjectRef().Object]; layerFont != font.ObjectRef().Object {
		t.Fatalf("bound Layer.font id = %d, layer font id = %d", font.ObjectRef().Object, layerFont)
	}
	rejected, err := session.Evaluate("rejected")
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := rejected.Truth(); !ok {
		t.Fatal("Layer.font getter accepted a non-Layer incontextof receiver")
	}
}

func TestNativeFontEscapementMetrics(t *testing.T) {
	services := DefaultServices()
	if err := services.RegisterFontFace("Deterministic", basicfont.Face7x13); err != nil {
		t.Fatal(err)
	}
	session := NewSession(services, 10_000)
	if _, err := session.executeSource("<font-escapement-test>", `
		f = new Font('Deterministic', -17);
		width = f.getTextWidth('A');
		height = f.getTextHeight('A');
		widthX = f.getEscWidthX('A');
		widthY = f.getEscWidthY('A');
		heightX = f.getEscHeightX('A');
		heightY = f.getEscHeightY('A');
		f.angle = 900;
		rotatedWidthX = f.getEscWidthX('A');
		rotatedWidthY = f.getEscWidthY('A');
		rotatedHeightX = f.getEscHeightX('A');
		rotatedHeightY = f.getEscHeightY('A');
		l = new Layer();
		l.font.face = 'Deterministic';
		layerWidthX = l.font.getEscWidthX('A');
	`, false); err != nil {
		t.Fatal(err)
	}
	valueAt := func(name string) tjs.Value {
		t.Helper()
		value, err := session.Evaluate(name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return value
	}
	width, err := valueAt("width").Int()
	if err != nil {
		t.Fatal(err)
	}
	height, err := valueAt("height").Int()
	if err != nil {
		t.Fatal(err)
	}
	if width <= 0 || height != 17 {
		t.Fatalf("text metrics = %d x %d, want positive width and absolute configured height 17", width, height)
	}
	for name, want := range map[string]float64{
		"widthX": float64(width), "widthY": 0,
		"heightX": 0, "heightY": 17,
		"rotatedWidthX": 0, "rotatedWidthY": -float64(width),
		"rotatedHeightX": 17, "rotatedHeightY": 0,
		"layerWidthX": float64(width),
	} {
		got := valueAt(name)
		if got.Kind() != tjs.KindReal || math.Abs(got.RealValue()-want) > 1e-6 {
			t.Errorf("%s = %v (kind %v), want %v", name, got, got.Kind(), want)
		}
	}
}

func TestNativeFontMeasurementAndDrawText(t *testing.T) {
	services := DefaultServices()
	if err := services.RegisterFontFace("Deterministic", basicfont.Face7x13); err != nil {
		t.Fatal(err)
	}
	session := NewSession(services, 10_000)
	if _, err := session.Evaluate(`f = new Font()`); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Evaluate(`f.face='Deterministic'`); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Evaluate(`l = new Layer()`); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Evaluate(`l.font=f`); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Evaluate(`w = f.getTextWidth('A')`); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Evaluate(`l.drawText(0,0,'A',-65536)`); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Evaluate(`function forwardText(x,y,text,color,*) { l.drawText(...); } forwardText(0,0,'A',-65536,128)`); err != nil {
		t.Fatalf("drawText with explicit forwarding: %v", err)
	}
	widthValue, err := session.Evaluate(`w`)
	if err != nil {
		t.Fatal(err)
	}
	width, err := widthValue.Int()
	if err != nil {
		t.Fatal(err)
	}
	maskValue, err := session.Evaluate(`l.getMaskPixel(1,1)`)
	if err != nil {
		t.Fatal(err)
	}
	mask, err := maskValue.Int()
	if err != nil {
		t.Fatal(err)
	}
	if width == 0 && mask == 0 {
		t.Fatal("drawText did not return metrics")
	}
	changed := false
	for _, layer := range services.Layers {
		for _, pixel := range layer.RGBA {
			if pixel != 0 {
				changed = true
				break
			}
		}
	}
	if !changed {
		t.Fatal("drawText did not modify frame pixels")
	}
}

func TestNativeLayerDrawTextRGBColorIsOpaque(t *testing.T) {
	services := DefaultServices()
	if err := services.RegisterFontFace("RGBColor", basicfont.Face7x13); err != nil {
		t.Fatal(err)
	}
	session := NewSession(services, 10_000)
	if _, err := session.executeSource("<layer-draw-text-rgb-test>", `
		l = new Layer();
		l.font.face = "RGBColor";
		l.drawText(0, 0, "A", 0xffffff)
	`, false); err != nil {
		t.Fatal(err)
	}
	value, err := session.Evaluate("l")
	if err != nil {
		t.Fatal(err)
	}
	layer := services.Layer(value.ObjectRef().Object)
	if layer == nil {
		t.Fatal("drawText layer was not registered")
	}
	for i := 0; i < len(layer.RGBA); i += 4 {
		if layer.RGBA[i+3] != 0 {
			if layer.RGBA[i] != 255 || layer.RGBA[i+1] != 255 || layer.RGBA[i+2] != 255 {
				t.Fatalf("RGB drawText pixel = %v, want opaque white", layer.RGBA[i:i+4])
			}
			return
		}
	}
	t.Fatal("drawText with 0xRRGGBB produced no visible pixels")
}

func TestNativeLayerDrawTextUsesSelectedFontHeight(t *testing.T) {
	services := DefaultServices()
	var requestedSize float64
	if err := services.RegisterFontFactory("FactoryFace", func(size float64) (FontFace, error) {
		requestedSize = size
		return basicfont.Face7x13, nil
	}); err != nil {
		t.Fatal(err)
	}
	session := NewSession(services, 10_000)
	if _, err := session.Evaluate(`f = new Font('FactoryFace',24)`); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Evaluate(`l = new Layer()`); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Evaluate(`l.drawText(0,0,'A',-65536,f)`); err != nil {
		t.Fatal(err)
	}
	if requestedSize != 24 {
		t.Fatalf("drawText selected font size = %v, want 24", requestedSize)
	}
}

func TestNativeFontHeightUsesConfiguredHeightAndSelectedFace(t *testing.T) {
	services := DefaultServices()
	services.SetFontBook(NewFontBook())
	if err := services.RegisterFontFace("Deterministic", basicfont.Face7x13); err != nil {
		t.Fatal(err)
	}
	session := NewSession(services, 10_000)
	if _, err := session.Evaluate(`f = new Font()`); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Evaluate(`f.face='Deterministic'`); err != nil {
		t.Fatal(err)
	}
	value, err := session.Evaluate(`f.getTextHeight('A\nB')`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := value.Int()
	if err != nil {
		t.Fatal(err)
	}
	expectedWidth, err := session.Evaluate(`f.getTextWidth('A') + f.getTextWidth('B')`)
	if err != nil {
		t.Fatal(err)
	}
	wantWidth, err := expectedWidth.Int()
	if err != nil {
		t.Fatal(err)
	}
	if got != 12 {
		t.Fatalf("height = %d, want absolute configured font height 12", got)
	}
	selectedFace, err := session.Evaluate(`f.face`)
	if err != nil {
		t.Fatal(err)
	}
	if got := selectedFace.Text(); got != "Deterministic" {
		t.Fatalf("selected face = %q, want Deterministic", got)
	}

	if _, err := session.Evaluate(`f.face='Missing'`); err != nil {
		t.Fatal(err)
	}
	fallbackHeight, err := session.Evaluate(`f.getTextHeight('A\nB')`)
	if err != nil {
		t.Fatalf("height for missing face with registered fallback: %v", err)
	}
	fallbackHeightValue, err := fallbackHeight.Int()
	if err != nil {
		t.Fatal(err)
	}
	fallbackWidth, err := session.Evaluate(`f.getTextWidth('A\nB')`)
	if err != nil {
		t.Fatalf("width for missing face with registered fallback: %v", err)
	}
	fallbackWidthValue, err := fallbackWidth.Int()
	if err != nil {
		t.Fatal(err)
	}
	if fallbackHeightValue != 12 || fallbackWidthValue != wantWidth {
		t.Fatalf(
			"missing face fallback metrics = %d,%d, want width %d and configured height 12",
			fallbackWidthValue, fallbackHeightValue, wantWidth,
		)
	}
}

func TestNativeFontUnsupportedFace(t *testing.T) {
	services := DefaultServices()
	services.SetFontSystem(&FontSystem{
		Book:        NewFontBook(),
		DefaultFont: "UnavailableDefault",
		DefaultSize: 12,
	})
	session := NewSession(services, 10_000)
	if _, err := session.Evaluate(`f = new Font('RequestedMissing',12)`); err != nil {
		t.Fatal(err)
	}
	_, err := session.Evaluate(`f.getTextWidth('A')`)
	if !errors.Is(err, ErrUnsupportedFont) {
		t.Fatalf("unsupported face error=%v", err)
	}
	for _, detail := range []string{
		"RequestedMissing",
		"UnavailableDefault",
		"no explicitly registered fonts",
		"no discovered system fonts",
	} {
		if !strings.Contains(err.Error(), detail) {
			t.Fatalf("unsupported face error=%q, want diagnostic detail %q", err, detail)
		}
	}
}

func TestNativeLayerChildrenAliasesStaySynchronized(t *testing.T) {
	services := DefaultServices()
	session := NewSession(services, 20_000)
	evalInt := func(source string) int64 {
		value, err := session.Evaluate(source)
		if err != nil {
			t.Fatalf("%s: %v", source, err)
		}
		n, err := value.Int()
		if err != nil {
			t.Fatalf("%s: %v", source, err)
		}
		return n
	}
	evalTruth := func(source string) bool {
		value, err := session.Evaluate(source)
		if err != nil {
			t.Fatalf("%s: %v", source, err)
		}
		truth, err := value.Truth()
		if err != nil {
			t.Fatalf("%s: %v", source, err)
		}
		return truth
	}
	if _, err := session.Evaluate(`root = new Layer()`); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Evaluate(`rootChildren = root.children`); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Evaluate(`child = new Layer(root)`); err != nil {
		t.Fatal(err)
	}
	if evalInt(`rootChildren.count`) != 1 || !evalTruth(`rootChildren[0]===child`) {
		t.Fatal("constructor parent insertion left children alias stale")
	}
	if _, err := session.Evaluate(`childChildren = child.children`); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Evaluate(`grand = new Layer(child)`); err != nil {
		t.Fatal(err)
	}
	if evalInt(`childChildren.count`) != 1 || !evalTruth(`childChildren[0]===grand`) {
		t.Fatal("nested constructor insertion left children alias stale")
	}
	if _, err := session.Evaluate(`second = new Layer(root)`); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Evaluate(`second.move(0)`); err != nil {
		t.Fatal(err)
	}
	if !evalTruth(`rootChildren[0]===second`) {
		t.Fatal("move left children alias stale")
	}
	if _, err := session.Evaluate(`root.removeChild(second)`); err != nil {
		t.Fatal(err)
	}
	if evalInt(`rootChildren.count`) != 1 || !evalTruth(`rootChildren[0]===child`) {
		t.Fatal("removeChild left children alias stale")
	}
	if _, err := session.Evaluate(`third = new Layer()`); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Evaluate(`root.addChild(third)`); err != nil {
		t.Fatal(err)
	}
	if evalInt(`rootChildren.count`) != 2 || !evalTruth(`rootChildren[1]===third`) {
		t.Fatal("addChild left children alias stale")
	}
	if _, err := session.Evaluate(`root.removeChild(third)`); err != nil {
		t.Fatal(err)
	}
	if evalInt(`rootChildren.count`) != 1 {
		t.Fatal("removeChild after addChild left children alias stale")
	}
	if _, err := session.Evaluate(`other = new Layer()`); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Evaluate(`otherChildren = other.children`); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Evaluate(`child.parent = other`); err != nil {
		t.Fatal(err)
	}
	if evalInt(`rootChildren.count`) != 0 || evalInt(`otherChildren.count`) != 1 || !evalTruth(`otherChildren[0]===child`) {
		t.Fatal("reparent left children aliases stale")
	}
	childValue, err := session.Evaluate(`child`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.VM.Invalidate(childValue, services, &session.Budget); err != nil {
		t.Fatal(err)
	}
	if evalInt(`rootChildren.count`) != 0 || evalInt(`otherChildren.count`) != 0 || evalInt(`childChildren.count`) != 0 {
		t.Fatal("invalidation left children aliases stale")
	}
}

func TestNativeLayerSettersPreserveValuesOnConversionError(t *testing.T) {
	services := DefaultServices()
	session := NewSession(services, 10_000)
	for _, source := range []string{`l = new Layer()`, `l.left=7`, `l.top=9`, `l.setSize(20,20)`, `l.imageLeft=-3`, `l.imageTop=-4`, `l.visible=1`, `l.enabled=1`, `l.hitThreshold=256`} {
		if _, err := session.Evaluate(source); err != nil {
			t.Fatal(err)
		}
	}
	expectError := func(source string) {
		if _, err := session.Evaluate(source); err == nil {
			t.Fatalf("%s succeeded", source)
		}
	}
	expectError(`l.left=l`)
	expectError(`l.top=l`)
	expectError(`l.imageLeft=l`)
	expectError(`l.imageTop=l`)
	expectError(`l.setPos(99,100,l,10)`)
	expectError(`l.hitThreshold=l`)
	expectError(`l.hitThreshold=-1`)
	expectError(`l.hitThreshold=257`)
	values, err := session.Evaluate(`[l.left,l.top,l.imageLeft,l.imageTop,l.visible,l.enabled,l.hitThreshold]`)
	if err != nil {
		t.Fatal(err)
	}
	items, err := session.VM.ArrayItems(values)
	if err != nil {
		t.Fatal(err)
	}
	expected := []int64{7, 9, -3, -4, 1, 1, 256}
	if len(items) != len(expected) {
		t.Fatalf("setter values=%d", len(items))
	}
	for i, item := range items {
		value, err := item.Int()
		if err != nil || value != expected[i] {
			t.Fatalf("setter value[%d]=%d,%v", i, value, err)
		}
	}
}

func TestNativeLayerDrawTextUsesConfiguredDefaultFace(t *testing.T) {
	services := DefaultServices()
	if err := services.RegisterFontFace(defaultFontFaceName, basicfont.Face7x13); err != nil {
		t.Fatal(err)
	}
	session := NewSession(services, 10_000)
	if _, err := session.Evaluate(`l = new Layer()`); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Evaluate(`l.drawText(0,0,'A',0xffffff)`); err != nil {
		t.Fatal(err)
	}
	changed := false
	for _, layer := range services.Layers {
		for _, pixel := range layer.RGBA {
			if pixel != 0 {
				changed = true
				break
			}
		}
	}
	if !changed {
		t.Fatal("default-face drawText did not modify frame pixels")
	}
}
func TestNativeLayerDrawTextRequiresColor(t *testing.T) {
	session := NewSession(DefaultServices(), 10_000)
	if _, err := session.Evaluate(`l = new Layer()`); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Evaluate(`l.drawText(0,0,'A')`); err == nil || !strings.Contains(err.Error(), "Layer.drawText: missing argument 3") {
		t.Fatalf("drawText without color error = %v, want missing argument 3", err)
	}
}

func TestNativeFontMappingConcurrentAccessIsSerialized(t *testing.T) {
	services := DefaultServices()
	context := tjs.ObjectIDValue(9_001)
	if _, err := services.fontNative(nil, context, "Font", nil, nil); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for worker := range 8 {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := range 100 {
				_, _ = services.fontNative(nil, context, "Font.set:height", []tjs.Value{tjs.IntegerValue(int64(worker + i))}, nil)
				_, _ = services.fontNative(nil, context, "Font.get:height", nil, nil)
				_ = services.GCRoots()
				if i%17 == 0 {
					_, _ = services.fontNative(nil, context, "@invalidate", nil, nil)
					_, _ = services.fontNative(nil, context, "Font", nil, nil)
				}
			}
		}(worker)
	}
	wg.Wait()
}
func TestNativeFontGetListReturnsAvailableFamilies(t *testing.T) {
	book := NewFontBook()
	if err := book.Register("First Face", basicfont.Face7x13); err != nil {
		t.Fatal(err)
	}
	if err := book.Register("Second Face", basicfont.Face7x13); err != nil {
		t.Fatal(err)
	}
	services := DefaultServices()
	services.SetFontBook(book)
	session := NewSession(services, 10_000)
	defer session.Close()

	value, err := session.Evaluate(`font = new Font(); font.getList(fsfSameCharSet | fsfNoVertical | fsfTrueTypeOnly).join("|")`)
	if err != nil {
		t.Fatal(err)
	}
	if got := value.Text(); got != "First Face|Second Face" {
		t.Fatalf("Font.getList() = %q, want registered available faces in order", got)
	}
}
