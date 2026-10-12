package runtime

import (
	"errors"
	"strings"
	"testing"

	"github.com/eslzzyl/krkrgo/internal/tjs"
)

func TestNativeWindowResizeBaseCallback(t *testing.T) {
	session := NewSession(DefaultServices(), 10_000)
	if _, err := session.Evaluate(`
		class DerivedWindow extends Window {
			function DerivedWindow() { super.Window(); }
			function onResize() { resizeCalled = true; super.onResize(...); }
		}
		window = new DerivedWindow();
		window.onResize();
	`); err != nil {
		t.Fatalf("derived Window.onResize(): %v", err)
	}
	if value, err := session.Evaluate("resizeCalled"); err != nil {
		t.Fatal(err)
	} else if called, err := value.Truth(); err != nil || !called {
		t.Fatalf("derived onResize callback = %v, %v", called, err)
	}
	if _, err := session.Evaluate(`class PlainWindowLike {}; plain = new PlainWindowLike(); plain.onResize()`); err == nil || !strings.Contains(err.Error(), "member not found: onResize") {
		t.Fatalf("non-Window onResize error = %v, want missing member", err)
	}
}
func TestNativeWindowSessionPropertiesAndTransitions(t *testing.T) {
	services := DefaultServices()
	session := NewSession(services, 10_000)
	if _, err := session.Evaluate(`window = new Window()`); err != nil {
		t.Fatal(err)
	}
	windowValue, err := session.Evaluate("window")
	if err != nil {
		t.Fatal(err)
	}
	if windowValue.Kind() != tjs.KindObject {
		t.Fatalf("window value = %v", windowValue)
	}
	id := windowValue.ObjectRef().Object
	window := services.Window(id)
	if window == nil || services.MainWindow != uint64(id) || services.Display.MainWindow != uint64(id) {
		t.Fatalf("window registration id=%d main=%d displayMain=%d", id, services.MainWindow, services.Display.MainWindow)
	}
	if value, err := session.Evaluate(`window.drawDevice = %[]; var drawDevice = window.drawDevice; window.drawDevice === drawDevice`); err != nil {
		t.Fatal(err)
	} else if same, err := value.Truth(); err != nil || !same {
		t.Fatalf("Window.drawDevice setter/getter = %v, %v", value, err)
	}
	object, ok := session.VM.Objects.Lookup(id)
	if !ok {
		t.Fatal("window object missing from VM")
	}
	if err := object.Set("onMove", tjs.IntegerValue(1)); err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{
		`window.caption = "runtime"`,
		`window.setPos(30,40)`,
		`window.setSize(320,240)`,
		`window.disableResize = true`,
		`window.disableMove = true`,
		`window.enableNCMouseEvent = true`,
		`window.registerExEvent()`,
		`window.bringToFront()`,
	} {
		if _, err := session.Evaluate(source); err != nil {
			t.Fatalf("%s: %v", source, err)
		}
	}
	if window.Caption != "runtime" || window.Left != 30 || window.Top != 40 || window.Width() != 320 || window.Height() != 240 {
		t.Fatalf("window state = %#v", window)
	}
	if !window.DisableResize || !window.DisableMove || !window.EnableNCMouseEvent || !window.ExEvents[2] {
		t.Fatalf("WindowEx state = %#v", window)
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
	if evalInt("window.getWindowRect().x") != 30 || evalInt("window.getWindowRect().y") != 40 || evalInt("window.getWindowRect().w") != 320 || evalInt("window.getWindowRect().h") != 240 {
		t.Fatalf("window rect = %#v", window.WindowRect())
	}
	if evalInt("window.getClientRect().w") != int64(window.InnerWidth) || evalInt("window.getClientRect().h") != int64(window.InnerHeight) {
		t.Fatalf("client rect = %#v", window.ClientRect())
	}
	if evalInt("window.getNormalRect().x") != 30 || evalInt("window.getNormalRect().y") != 40 || evalInt("window.getNormalRect().w") != 320 || evalInt("window.getNormalRect().h") != 240 {
		t.Fatalf("normal rect = %#v", window.NormalRect())
	}
	if !evalTruth("Window.mainWindow === window") {
		t.Fatal("Window.mainWindow does not reference instance")
	}
	if _, err := session.Evaluate(`window.maximize()`); err != nil {
		t.Fatal(err)
	}
	if !window.Maximized || window.Minimized || window.WindowRect() != services.Display.DesktopBounds() {
		t.Fatalf("maximized window = %#v", window)
	}
	if _, err := session.Evaluate(`window.minimize()`); err != nil {
		t.Fatal(err)
	}
	if !window.Minimized || window.Maximized {
		t.Fatalf("minimized window = %#v", window)
	}
	if _, err := session.Evaluate(`window.showRestore()`); err != nil {
		t.Fatal(err)
	}
	if !window.Maximized || window.Minimized {
		t.Fatalf("restored maximized window = %#v", window)
	}
	if _, err := session.Evaluate(`window.showRestore()`); err != nil {
		t.Fatal(err)
	}
	if window.Maximized || window.Minimized || window.WindowRect() != [4]int32{30, 40, 320, 240} {
		t.Fatalf("restored normal window = %#v", window)
	}
	if _, err := session.Evaluate(`window.fullScreen = true`); err != nil {
		t.Fatal(err)
	}
	if !window.IsFullScreen() || window.WindowRect() != services.Display.PrimaryDisplay().Bounds {
		t.Fatalf("fullscreen window = %#v", window)
	}
	if _, err := session.Evaluate(`window.fullScreen = false`); err != nil {
		t.Fatal(err)
	}
	if window.IsFullScreen() || window.WindowRect() != [4]int32{30, 40, 320, 240} {
		t.Fatalf("window after fullscreen = %#v", window)
	}
	if _, err := session.Evaluate(`window.width = -1`); err == nil {
		t.Fatal("negative width unexpectedly succeeded")
	}
	services.UnregisterWindow(id)
	if _, err := session.Evaluate(`window.caption`); !errors.Is(err, ErrInvalidWindow) {
		t.Fatalf("invalidated window error = %v", err)
	}
}

func TestWindowSetMessageHook(t *testing.T) {
	session := NewSession(DefaultServices(), 10_000)
	t.Cleanup(func() { _ = session.Close() })
	if _, err := session.Evaluate(`window = new Window()`); err != nil {
		t.Fatal(err)
	}
	eval := func(source string) int64 {
		t.Helper()
		value, err := session.Evaluate(source)
		if err != nil {
			t.Fatalf("%s: %v", source, err)
		}
		got, err := value.Int()
		if err != nil {
			t.Fatalf("%s: %v", source, err)
		}
		return got
	}
	window := session.Services.Window(tjs.ObjectID(session.Services.MainWindow))
	if window.ExEventRegistered {
		t.Fatal("new Window unexpectedly registered extended events")
	}
	if got := eval(`window.setMessageHook(1, "MOVE")`); got != 0 {
		t.Fatalf("individual hook return while global hook disabled = %d", got)
	}
	if window.EnableWinMsgHook {
		t.Fatal("individual hook changed global hook state")
	}
	if got := eval(`window.setMessageHook(1)`); got != 1 {
		t.Fatalf("enable all hooks = %d", got)
	}
	if !window.EnableWinMsgHook {
		t.Fatal("setMessageHook(1) did not enable global message hook")
	}
	for _, source := range []string{
		`window.setMessageHook(0, "MOVE")`,
		`window.setMessageHook(0, 5)`,
		`window.setMessageHook(1, "CTLCOLOR")`,
		`window.setMessageHook(1, "DEADCHAR")`,
		`window.setMessageHook(1, "MOUSELAST")`,
		`window.setMessageHook(1, "PENWINLAST")`,
	} {
		if got := eval(source); got != 1 {
			t.Fatalf("%s return while global hook enabled = %d", source, got)
		}
	}
	if !window.EnableWinMsgHook {
		t.Fatal("individual hook changed global hook state")
	}
	if got := eval(`window.setMessageHook(0)`); got != 0 {
		t.Fatalf("disable all hooks = %d", got)
	}
	if window.EnableWinMsgHook {
		t.Fatal("setMessageHook(0) did not disable global message hook")
	}
	for _, source := range []string{
		`window.setMessageHook(1, "NOT_A_MESSAGE")`,
		`window.setMessageHook(1, 1024)`,
		`window.setMessageHook(1, -1)`,
	} {
		if _, err := session.Evaluate(source); err == nil {
			t.Fatalf("%s succeeded", source)
		}
	}
}

func TestNativeWindowChildObjectsAreRetainedAndRemovable(t *testing.T) {
	services := DefaultServices()
	session := NewSession(services, 10_000)
	defer session.Close()
	if _, err := session.Evaluate(`window = new Window()`); err != nil {
		t.Fatal(err)
	}
	windowValue, err := session.Evaluate("window")
	if err != nil {
		t.Fatal(err)
	}
	child, err := session.VM.NewDictionary()
	if err != nil {
		t.Fatal(err)
	}
	session.VM.Globals["windowChild"] = child
	if _, err := session.executeSource("<window-test>", `window.add(windowChild); window.add(windowChild)`, false); err != nil {
		t.Fatal(err)
	}
	window := services.Window(windowValue.ObjectRef().Object)
	if len(window.registeredObjects) != 1 || !window.registeredObjects[0].StrictEqual(child) {
		t.Fatalf("registered Window objects = %#v", window.registeredObjects)
	}
	delete(session.VM.Globals, "windowChild")
	if err := session.GC(); err != nil {
		t.Fatal(err)
	}
	if _, ok := session.VM.Objects.Lookup(child.ObjectRef().Object); !ok {
		t.Fatal("Window.add did not retain its object through garbage collection")
	}

	session.VM.Globals["windowChild"] = child
	if _, err := session.executeSource("<window-test>", `window.remove(windowChild); window.remove(windowChild)`, false); err != nil {
		t.Fatal(err)
	}
	delete(session.VM.Globals, "windowChild")
	if len(window.registeredObjects) != 0 {
		t.Fatalf("registered Window objects after remove = %#v", window.registeredObjects)
	}
	if err := session.GC(); err != nil {
		t.Fatal(err)
	}
	if _, ok := session.VM.Objects.Lookup(child.ObjectRef().Object); ok {
		t.Fatal("Window.remove did not release its object reference")
	}
}

func TestNativeWindowCloseAndShowModalHeadlessState(t *testing.T) {
	services := DefaultServices()
	session := NewSession(services, 10_000)
	defer session.Close()
	if _, err := session.Evaluate(`window = new Window()`); err != nil {
		t.Fatal(err)
	}
	windowValue, err := session.Evaluate("window")
	if err != nil {
		t.Fatal(err)
	}
	window := services.Window(windowValue.ObjectRef().Object)
	if _, err := session.Evaluate(`window.finalize()`); err != nil {
		t.Fatal(err)
	}
	if services.Window(windowValue.ObjectRef().Object) != window {
		t.Fatal("Window.finalize unexpectedly invalidated the Window")
	}
	result, err := session.Evaluate(`window.close()`)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsVoid() || !window.Closed || window.Visible || window.Modal {
		t.Fatalf("close result/state = %v / %#v", result, window)
	}
	result, err = session.Evaluate(`window.showModal()`)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsVoid() || window.Closed || !window.Visible || !window.Modal {
		t.Fatalf("showModal result/state = %v / %#v", result, window)
	}
}

func TestNativeWindowInvalidationInvalidatesRegisteredObjects(t *testing.T) {
	services := DefaultServices()
	session := NewSession(services, 10_000)
	defer session.Close()
	if _, err := session.executeSource("<window-test>", `window = new Window(); owner = new Dictionary(); timer = new Timer(owner); layer = new Layer(); other = new Dictionary(); window.add(timer); window.add(layer); window.add(other); window.add(timer)`, false); err != nil {
		t.Fatal(err)
	}
	windowValue, err := session.Evaluate("window")
	if err != nil {
		t.Fatal(err)
	}
	timerValue, err := session.Evaluate("timer")
	if err != nil {
		t.Fatal(err)
	}
	layerValue, err := session.Evaluate("layer")
	if err != nil {
		t.Fatal(err)
	}
	otherValue, err := session.Evaluate("other")
	if err != nil {
		t.Fatal(err)
	}
	window := services.Window(windowValue.ObjectRef().Object)
	if len(window.registeredObjects) != 3 {
		t.Fatalf("registered Window object count = %d, want 3", len(window.registeredObjects))
	}
	budget := uint64(10_000)
	if _, err := session.VM.Invalidate(windowValue, services, &budget); err != nil {
		t.Fatal(err)
	}
	if services.Window(windowValue.ObjectRef().Object) != nil {
		t.Fatal("invalidated Window remains registered")
	}
	if services.Timers.Timer(uint64(timerValue.ObjectRef().Object)) != nil {
		t.Fatal("Window invalidation did not invalidate its Timer child")
	}
	if services.Layers[layerValue.ObjectRef().Object] != nil {
		t.Fatal("Window invalidation did not invalidate its Layer child")
	}
	for name, value := range map[string]tjs.Value{"Timer": timerValue, "Layer": layerValue, "other object": otherValue} {
		if session.VM.IsValid(value).IntegerValue() != 0 {
			t.Fatalf("Window invalidation left its %s child valid", name)
		}
	}
}
func TestNativeWindowFocusedLayerAndMetaKeyDown(t *testing.T) {
	session := NewSession(DefaultServices(), 10_000)
	defer session.Close()
	if _, err := session.executeSource("<window-focus-test>", `window = new Window(); root = new Layer(window); focused = new Layer(window, root); root.visible = true; focused.visible = true; focused.focusable = true`, false); err != nil {
		t.Fatal(err)
	}
	value, err := session.Evaluate(`window.focusedLayer`)
	if err != nil {
		t.Fatal(err)
	}
	if !value.StrictEqual(tjs.NullValue) {
		t.Fatalf("unfocused Window.focusedLayer = %v, want null", value)
	}
	if _, err := session.Evaluate(`window.focusedLayer = focused`); err != nil {
		t.Fatal(err)
	}
	value, err = session.Evaluate(`window.focusedLayer`)
	if err != nil {
		t.Fatal(err)
	}
	if !value.StrictEqual(mustEvalValue(t, session, "focused")) {
		t.Fatalf("focused Window.focusedLayer = %v, want the original Layer object", value)
	}
	value, err = session.Evaluate(`focused.focused`)
	if err != nil || value.IntegerValue() != 1 {
		t.Fatalf("Layer.focused after Window.focusedLayer assignment = %v, err=%v", value, err)
	}
	if _, err := session.Evaluate(`window.focusedLayer = null`); err != nil {
		t.Fatal(err)
	}
	value, err = session.Evaluate(`window.focusedLayer`)
	if err != nil || !value.StrictEqual(tjs.NullValue) {
		t.Fatalf("cleared Window.focusedLayer = %v, err=%v", value, err)
	}
	value, err = session.Evaluate(`focused.focused`)
	if err != nil || value.IntegerValue() != 0 {
		t.Fatalf("Layer.focused after Window.focusedLayer was cleared = %v, err=%v", value, err)
	}

	if _, err := session.executeSource("<window-focus-test>", `window.keyDowns = ""; window.onKeyDown = function(key, shift) { if (window.focusedLayer === null) window.keyDowns = window.keyDowns + key + ","; }`, false); err != nil {
		t.Fatal(err)
	}
	windowValue, err := session.Evaluate("window")
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Input(windowValue, KeyDown(0x5b, 0)); err != nil {
		t.Fatalf("VK_LWIN/Meta onKeyDown: %v", err)
	}
	if err := session.Input(windowValue, KeyDown(65, 0)); err != nil {
		t.Fatalf("subsequent key onKeyDown: %v", err)
	}
	value, err = session.Evaluate(`window.keyDowns`)
	if err != nil || value.Text() != "91,65," {
		t.Fatalf("handled Meta and subsequent key-downs = %q, err=%v", value.Text(), err)
	}
}

func mustEvalValue(t *testing.T, session *Session, source string) tjs.Value {
	t.Helper()
	value, err := session.Evaluate(source)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestNativeSystemDisplayQueries(t *testing.T) {
	services := DefaultServices()
	if err := services.SetDisplays([]Monitor{{Name: "main", Primary: true, Bounds: [4]int32{0, 0, 1280, 720}, Work: [4]int32{0, 0, 1280, 700}}}); err != nil {
		t.Fatal(err)
	}
	session := NewSession(services, 10_000)
	if _, err := session.Evaluate(`monitors = System.getDisplayMonitors()`); err != nil {
		t.Fatal(err)
	}
	value, err := session.Evaluate("monitors.count")
	if err != nil {
		t.Fatal(err)
	}
	count, err := value.Int()
	if err != nil || count != 1 {
		t.Fatalf("monitor count=%d err=%v", count, err)
	}
	if _, err := session.Evaluate(`info = System.getMonitorInfo(1,10,20)`); err != nil {
		t.Fatal(err)
	}
	value, err = session.Evaluate("info.name")
	if err != nil || value.Text() != "main" {
		t.Fatalf("monitor info=%q err=%v", value.Text(), err)
	}
	if _, err := session.Evaluate(`System.getCursorPos()`); !errors.Is(err, ErrUnsupportedDevice) {
		t.Fatalf("cursor error = %v", err)
	}
}
func TestWindowNotificationLookupAndHitTestConstants(t *testing.T) {
	session := NewSession(DefaultServices(), 10_000)
	t.Cleanup(func() { _ = session.Close() })
	for source, want := range map[string]int64{
		"Window.getNotificationNum('MOVE')":      3,
		"Window.getNotificationNum('MOUSEMOVE')": 0x200,
		"Window.getNotificationNum('missing')":   -1,
		"Window.nchtNoWhere":                     0,
		"Window.nchtBorder":                      18,
		"Window.nchtTransparent":                 65535,
	} {
		value, err := session.Evaluate(source)
		if err != nil {
			t.Fatalf("%s: %v", source, err)
		}
		if got, err := value.Int(); err != nil || got != want {
			t.Fatalf("%s = %v (err %v), want %d", source, got, err, want)
		}
	}
	for source, want := range map[string]string{
		"Window.getNotificationName(0x200)": "MOUSEMOVE",
		"Window.getNotificationName(0x01a)": "SETTINGCHANGE",
	} {
		value, err := session.Evaluate(source)
		if err != nil || value.Text() != want {
			t.Fatalf("%s = %q, err=%v; want %q", source, value.Text(), err, want)
		}
	}
	value, err := session.Evaluate(`Window.getNotificationName(0x7fff)`)
	if err != nil || value.Text() != "" {
		t.Fatalf("unknown notification name = %q, err=%v", value.Text(), err)
	}
	if _, err := session.Evaluate(`Window.getNotificationNum()`); err == nil {
		t.Fatal("missing notification name unexpectedly succeeded")
	}
}
func TestWindowExUnsupportedHostOperationsAreExplicit(t *testing.T) {
	session := NewSession(DefaultServices(), 10_000)
	t.Cleanup(func() { _ = session.Close() })
	if _, err := session.Evaluate(`window = new Window()`); err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{
		`window.resetWindowIcon()`,
		`window.setWindowIcon()`,
		`window.setOverlayBitmap()`,
		`window.bringTo()`,
		`window.sendToBack()`,
		`window.ncHitTest(1, 2)`,
	} {
		if _, err := session.Evaluate(source); !errors.Is(err, ErrUnsupportedDevice) {
			t.Errorf("%s error = %v, want ErrUnsupportedDevice", source, err)
		}
	}
	if _, err := session.Evaluate(`window.ncHitTest(1)`); err == nil || !strings.Contains(err.Error(), "missing argument 1") {
		t.Fatalf("ncHitTest missing argument error = %v", err)
	}
	if _, err := session.executeSource("<window-ex-system-menu-test>", `menu = new Array(); window.exSystemMenu = menu`, false); err != nil {
		t.Fatal(err)
	}
	if value, err := session.Evaluate(`window.exSystemMenu === menu`); err != nil {
		t.Fatal(err)
	} else if equal, err := value.Truth(); err != nil || !equal {
		t.Fatalf("exSystemMenu did not preserve assigned object (value=%v, err=%v)", value, err)
	}
	if _, err := session.Evaluate(`window.exSystemMenu = 1`); err == nil || !strings.Contains(err.Error(), "requires an Object") {
		t.Fatalf("invalid exSystemMenu assignment error = %v", err)
	}
}
