package runtime

import (
	"testing"

	"github.com/eslzzyl/krkrgo/internal/tjs"
	"golang.org/x/image/font/basicfont"
)

func TestSessionInputDispatchesButtonLayerMouseEvents(t *testing.T) {
	session := NewSession(DefaultServices(), 50_000)
	defer session.Close()
	if _, err := session.executeSource("<layer-input-test>", `
class TestWindow extends Window {
	function TestWindow() { super.Window(); }
	function onMouseDown(x, y, button, shift) {
		window.windowDown++;
		super.onMouseDown(...);
	}
}
class TestButton extends Layer {
	var pressed = false;
	function TestButton(win, parent) {
		super.Layer(win, parent);
		setPos(10, 10);
		setSize(10, 10);
		visible = true;
		hitThreshold = 0;
	}
	function onMouseEnter() {
		update();
		super.onMouseEnter(...);
	}
	function onMouseDown(x, y, button, shift) {
		pressed = true;
		window.downX = x;
		window.downY = y;
		super.onMouseDown(...);
	}
	function onMouseUp(x, y, button, shift) {
		if(pressed && button == 0) window.process("1.ks", "*1", true);
		pressed = false;
		super.onMouseUp(...);
	}
}
window = new TestWindow();
window.launched = "";
window.downX = -1;
window.downY = -1;
window.windowDown = 0;
window.process = function(storage, target, count) { window.launched = storage + "|" + target; };
root = new Layer(window);
root.setSize(40, 40);
root.visible = true;
button = new TestButton(window, root);
button.update();
button.update(0, 0, 0, 0);
var invalidUpdate = false;
try { button.update(0, 0, 0); } catch(e) { invalidUpdate = true; }
`, false); err != nil {
		t.Fatal(err)
	}
	windowValue, err := session.Evaluate("window")
	if err != nil {
		t.Fatal(err)
	}
	windowID := windowValue.ObjectRef().Object
	windowState := session.Services.Window(tjs.ObjectID(windowID))
	if windowState == nil {
		t.Fatal("window state missing")
	}
	hit := session.Services.Layer(tjs.ObjectID(windowState.primaryLayer)).HitTest(15, 16)
	if hit == nil {
		t.Fatal("primary layer hit test returned nil")
	}
	if err := session.Input(windowID, PointerMove(15, 16)); err != nil {
		t.Fatal(err)
	}
	if err := session.Input(windowID, PointerDown(15, 16, 0, 0)); err != nil {
		t.Fatal(err)
	}
	if err := session.Input(windowID, PointerUp(15, 16, 0, 0)); err != nil {
		t.Fatal(err)
	}
	if primary := session.Services.Layer(tjs.ObjectID(windowState.primaryLayer)); primary.CursorX != 15 || primary.CursorY != 16 {
		t.Fatalf("primary cursor = (%d, %d), want (15, 16)", primary.CursorX, primary.CursorY)
	}
	result, err := session.Evaluate(`[window.launched,window.downX,window.downY,window.windowDown,button.pressed,invalidUpdate].join('|')`)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := result.Text(), "1.ks|*1|5|6|1|0|1"; got != want {
		t.Fatalf("button input result = %q, want %q", got, want)
	}
}

func TestLayerUpdateDispatchesOnPaintAtFrameCapture(t *testing.T) {
	session := NewSession(DefaultServices(), 10_000)
	defer session.Close()
	if _, err := session.executeSource("<layer-on-paint>", `
class PaintProbe extends Layer {
	var paints = 0;
	function PaintProbe(win) { super.Layer(win, null); setSize(4, 4); }
	function onPaint() { paints++; super.onPaint(...); }
}
window = new Window();
paintLayer = new PaintProbe(window);
paintLayer.update();
`, false); err != nil {
		t.Fatal(err)
	}
	result, err := session.Evaluate("paintLayer.paints")
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Text(); got != "0" {
		t.Fatalf("onPaint before capture = %s, want 0", got)
	}
	if _, err := session.Frame(); err != nil {
		t.Fatal(err)
	}
	result, err = session.Evaluate("paintLayer.paints")
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Text(); got != "1" {
		t.Fatalf("onPaint after one update = %s, want 1", got)
	}
	if _, err := session.Frame(); err != nil {
		t.Fatal(err)
	}
	result, err = session.Evaluate("paintLayer.paints")
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Text(); got != "1" {
		t.Fatalf("onPaint repeated without update = %s, want 1", got)
	}
	if _, err := session.Evaluate("paintLayer.update()"); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Frame(); err != nil {
		t.Fatal(err)
	}
	result, err = session.Evaluate("paintLayer.paints")
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Text(); got != "2" {
		t.Fatalf("onPaint after second update = %s, want 2", got)
	}
}

func TestSessionInputRunsOnHitTestAndPreservesNestedHitState(t *testing.T) {
	session := NewSession(DefaultServices(), 20_000)
	defer session.Close()
	if _, err := session.executeSource("<layer-on-hit-test>", `
class HitTestRoot extends Layer {
	function HitTestRoot(win) {
		super.Layer(win, null);
		setSize(20, 20);
		visible = true;
		hitThreshold = 0;
	}
	function onMouseDown(x, y, button, shift) {
		window.rootDown++;
		super.onMouseDown(...);
	}
}
class HitTestProbe extends Layer {
	function HitTestProbe(win) {
		super.Layer(win, null);
		setSize(20, 20);
		visible = true;
		hitThreshold = 0;
	}
	function onHitTest(x, y, hit) {
		return super.onHitTest(x, y, false);
	}
}
class HitTestOverlay extends Layer {
	function HitTestOverlay(win, parent) {
		super.Layer(win, parent);
		setSize(20, 20);
		visible = true;
		hitThreshold = 0;
	}
	function onHitTest(x, y, hit) {
		window.overlayHitTests++;
		window.nestedProbeRejected = probe.getLayerAt(x, y) === null;
		if(window.passThrough) return super.onHitTest(x, y, false);
	}
	function onMouseDown(x, y, button, shift) {
		window.overlayDown++;
		super.onMouseDown(...);
	}
}
window = new Window();
window.rootDown = 0;
window.overlayDown = 0;
window.overlayHitTests = 0;
window.passThrough = false;
root = new HitTestRoot(window);
probe = new HitTestProbe(window);
overlay = new HitTestOverlay(window, root);
`, false); err != nil {
		t.Fatal(err)
	}
	windowValue, err := session.Evaluate("window")
	if err != nil {
		t.Fatal(err)
	}
	windowID := uint64(windowValue.ObjectRef().Object)
	result, err := session.Evaluate("root.getLayerAt(2,2) === overlay && window.nestedProbeRejected")
	if err != nil {
		t.Fatal(err)
	}
	truth, truthErr := result.Truth()
	if truthErr != nil {
		t.Fatal(truthErr)
	}
	if !truth {
		t.Fatal("getLayerAt ignored onHitTest or nested hit-test state")
	}
	if _, err := session.Evaluate("window.passThrough = true"); err != nil {
		t.Fatal(err)
	}
	if err := session.Input(windowID, PointerDown(2, 2, 0, 0)); err != nil {
		t.Fatal(err)
	}
	result, err = session.Evaluate(`[window.rootDown,window.overlayDown,window.overlayHitTests].join('|')`)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := result.Text(), "1|0|2"; got != want {
		t.Fatalf("script hit-test input result = %q, want %q", got, want)
	}
}

func TestKAGTextTagBuildsClickableProvinceGeometry(t *testing.T) {
	services := DefaultServices()
	if err := services.RegisterFontFace("LinkTest", basicfont.Face7x13); err != nil {
		t.Fatal(err)
	}
	session := NewSession(services, 50_000)
	defer session.Close()
	if _, err := session.executeSource("<kag-link-province-test>", `
Plugins.link("plugin/KAGParser.dll");
class KAGLinkLayer extends Layer {
	var clicks = 0;
	var provinceAtClick = 0;
	function KAGLinkLayer(win) {
		super.Layer(win, null);
		setSize(80, 24);
		visible = true;
		hitType = htProvince;
		font.face = "LinkTest";
		font.height = 13;
	}
	function onTag(elm) {
		if(elm.tagname == "link") linkOpen = true;
		else if(elm.tagname == "ch") {
			var cw = font.getTextWidth(elm.text);
			drawText(10 + linkWidth, 2, elm.text, 0xffffff, 255, true);
			if(linkOpen) {
				face = dfProvince;
				fillRect(10 + linkWidth, 0, cw, 14, 1);
				face = dfAuto;
			}
			linkWidth += cw;
			linkText += elm.text;
		} else if(elm.tagname == "endlink") linkOpen = false;
	}
	function onMouseDown(x, y, button, shift) {
		provinceAtClick = getProvincePixel(x, y);
		clicks++;
		super.onMouseDown(...);
	}
}
window = new Window();
linkWidth = 0;
linkText = "";
linkLayer = new KAGLinkLayer(window);
parser = new KAGParser();
parser.ignoreCR = true;
parser.loadScenario("link.ks", "[link]go[endlink]");
linkLayer.onTag(parser.getNextTag());
linkLayer.onTag(parser.getNextTag());
linkLayer.onTag(parser.getNextTag());
linkLayer.onTag(parser.getNextTag());
ignoredCR = parser.getNextTag() === void;
`, false); err != nil {
		t.Fatal(err)
	}
	widthValue, err := session.Evaluate("linkWidth")
	if err != nil {
		t.Fatal(err)
	}
	width, err := widthValue.Int()
	if err != nil || width <= 0 {
		t.Fatalf("real glyph layout width = %d, %v", width, err)
	}
	textValue, err := session.Evaluate("linkText")
	if err != nil || textValue.Text() != "go" {
		t.Fatalf("legacy ch sequence = %q, %v; want one event per character yielding go", textValue.Text(), err)
	}
	ignoredCR, err := session.Evaluate("ignoredCR")
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := ignoredCR.Truth(); !ok {
		t.Fatal("KAGParser.getNextTag did not honor ignoreCR=true")
	}
	layerValue, err := session.Evaluate("linkLayer")
	if err != nil {
		t.Fatal(err)
	}
	layer := services.Layer(tjs.ObjectID(layerValue.ObjectRef().Object))
	insideX, insideY := int32(10+width/2), int32(5)
	outsideX := int32(10 + width)
	inside := layer != nil && insideX < layer.ProvinceWidth && insideY < layer.ProvinceHeight &&
		layer.Province[int(insideY*layer.ProvinceWidth+insideX)] == 1
	outside := layer != nil && outsideX < layer.ProvinceWidth && insideY < layer.ProvinceHeight &&
		layer.Province[int(insideY*layer.ProvinceWidth+outsideX)] != 0
	if !inside || outside {
		t.Fatalf("text-driven Province geometry inside=%t outside=%t width=%d", inside, outside, width)
	}
	windowValue, err := session.Evaluate("window")
	if err != nil {
		t.Fatal(err)
	}
	windowID := windowValue.ObjectRef().Object
	if err := session.Input(windowID, PointerDown(int32(10+width/2), 5, 0, 0)); err != nil {
		t.Fatal(err)
	}
	clicked, err := session.Evaluate("linkLayer.clicks == 1 && linkLayer.provinceAtClick == 1")
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := clicked.Truth(); !ok {
		t.Fatal("pointer-down inside the KAG text Province did not dispatch to the link layer")
	}
	if err := session.Input(windowID, PointerUp(int32(10+width/2), 5, 0, 0)); err != nil {
		t.Fatal(err)
	}
}

func TestKAGGetNextTagEmitsLegacyTextAndRSequence(t *testing.T) {
	session := NewSession(DefaultServices(), 50_000)
	defer session.Close()
	if _, err := session.executeSource("<kag-legacy-text-test>", `
Plugins.link("plugin/KAGParser.dll");
parser = new KAGParser();
parser.ignoreCR = false;
parser.loadScenario("legacy.ks", "xy\nz");
a = parser.getNextTag();
saved = parser.store();
parser.restore(saved);
b = parser.getNextTag();
c = parser.getNextTag();
d = parser.getNextTag();
e = parser.getNextTag();
function tagName(value) { return value === void ? "void" : value.tagname; }
function textValue(value) { return value === void ? "" : value.text; }
legacyNames = [tagName(a),tagName(b),tagName(c),tagName(d),tagName(e)].join("|");
legacyText = textValue(a) + textValue(b) + textValue(c) + textValue(d) + textValue(e);
parser.ignoreCR = true;
parser.loadScenario("ignore.ks", "x\ny");
ignoredFirst = parser.getNextTag();
ignoredSecond = parser.getNextTag();
ignoredEnd = parser.getNextTag() === void;
parser.loadScenario("native-state.ks", "[locate x=1000 y=400]\n[button]");
parser.ignoreCR = true;
nativeState = parser.store();
parser.ignoreCR = false;
parser.restore(nativeState);
restoredIgnoreCR = parser.ignoreCR;
firstNativeTag = parser.getNextTag();
secondNativeTag = parser.getNextTag();
nativeTagEnd = parser.getNextTag() === void;
defaultNativeProperties = parser.processSpecialTags && !parser.multiLineTagEnabled && parser.debugLevel == 1;
parser.processSpecialTags = false;
parser.multiLineTagEnabled = true;
parser.debugLevel = 2;
setNativeProperties = !parser.processSpecialTags && parser.multiLineTagEnabled && parser.debugLevel == 2;
`, false); err != nil {
		t.Fatal(err)
	}
	for expression, want := range map[string]string{
		"legacyNames": "ch|ch|r|ch|void",
		"legacyText":  "xyz",
	} {
		value, err := session.Evaluate(expression)
		if err != nil {
			t.Fatal(err)
		}
		if value.Text() != want {
			t.Fatalf("%s = %q, want %q", expression, value.Text(), want)
		}
	}
	ignored, err := session.Evaluate(`ignoredFirst.tagname == "ch" && ignoredSecond.tagname == "ch" && ignoredEnd`)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := ignored.Truth(); !ok {
		t.Fatal("ignoreCR=true did not omit the line-ending r event")
	}
	nativeProperties, err := session.Evaluate(`restoredIgnoreCR && firstNativeTag.tagname == "locate" && secondNativeTag.tagname == "button" && nativeTagEnd`)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := nativeProperties.Truth(); !ok {
		t.Fatal("ignoreCR did not survive restore while consecutive locate/button tags skipped the physical newline")
	}
	boundProperties, err := session.Evaluate(`defaultNativeProperties && setNativeProperties`)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := boundProperties.Truth(); !ok {
		t.Fatal("KAGParser property defaults or writable native bindings differ")
	}
}
