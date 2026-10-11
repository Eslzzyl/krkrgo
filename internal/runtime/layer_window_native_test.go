package runtime

import (
	"testing"

	"github.com/eslzzyl/krkrgo/internal/tjs"
)

func TestNativeLayerWindowAndPrimaryProperties(t *testing.T) {
	services := DefaultServices()
	services.SetRoot(NewRootLayer(0, 32, 32))
	session := NewSession(services, 10_000)
	defer session.Close()
	if _, err := session.executeSource("<layer-window-test>", `window = new Window(); primary = new Layer(window, null); child = new Layer(window, primary); standalone = new Layer(); window.add(primary)`, false); err != nil {
		t.Fatal(err)
	}
	windowValue, err := session.Evaluate("window")
	if err != nil {
		t.Fatal(err)
	}
	windowID := windowValue.ObjectRef().Object
	for _, source := range []string{
		`primary.window === window && primary.isPrimary`,
		`child.window === window && !child.isPrimary`,
		`standalone.window === null && !standalone.isPrimary`,
	} {
		value, evalErr := session.Evaluate(source)
		if evalErr != nil {
			t.Fatalf("%s: %v", source, evalErr)
		}
		ok, truthErr := value.Truth()
		if truthErr != nil || !ok {
			t.Fatalf("%s = %t, %v", source, ok, truthErr)
		}
	}
	for _, source := range []string{`primary.window = standalone`, `primary.isPrimary = false`} {
		if _, err := session.Evaluate(source); err != nil {
			t.Fatalf("read-only property assignment errored: %s: %v", source, err)
		}
	}
	if value, err := session.Evaluate(`primary.window === window && primary.isPrimary`); err != nil {
		t.Fatal(err)
	} else if ok, truthErr := value.Truth(); truthErr != nil || !ok {
		t.Fatalf("read-only property assignment changed state: %v, %v", value, truthErr)
	}

	ownerRooted := false
	for _, root := range services.GCRoots() {
		if root.Kind() == tjs.KindObject && root.ObjectRef().Object == windowID {
			ownerRooted = true
			break
		}
	}
	if !ownerRooted {
		t.Fatal("Window owning the Layer is not a GC root")
	}
	delete(session.VM.Globals, "window")
	if err := session.GC(); err != nil {
		t.Fatal(err)
	}
	if _, ok := session.VM.Objects.Lookup(windowID); !ok {
		t.Fatal("Layer owner Window was collected while registered")
	}
	gotWindow, err := session.Evaluate("primary.window")
	if err != nil || !gotWindow.StrictEqual(windowValue) {
		t.Fatalf("primary.window after GC = %v, %v", gotWindow, err)
	}

	budget := uint64(10_000)
	if _, err := session.VM.Invalidate(windowValue, services, &budget); err != nil {
		t.Fatal(err)
	}
	gotWindow, err = session.Evaluate("child.window")
	if err != nil || !gotWindow.IsNull() {
		t.Fatalf("child.window after owner invalidation = %v, %v", gotWindow, err)
	}
	isPrimary, err := session.Evaluate("child.isPrimary")
	if err != nil || isPrimary.IntegerValue() != 0 {
		t.Fatalf("child.isPrimary after owner invalidation = %v, %v", isPrimary, err)
	}
}

func TestDerivedWindowOwnsDerivedLayer(t *testing.T) {
	services := DefaultServices()
	session := NewSession(services, 10_000)
	defer session.Close()
	source := `
class DerivedWindow extends Window {
	function DerivedWindow() { super.Window(); }
}
class DerivedLayer extends Layer {
	function DerivedLayer(win, par) { super.Layer(win, par); }
}
window = new DerivedWindow();
primary = new Layer(window, null);
window.add(primary);
child = new DerivedLayer(window, primary);
`
	if _, err := session.executeSource("<derived-layer-window-test>", source, false); err != nil {
		t.Fatal(err)
	}
	value, err := session.Evaluate(`primary.window === window && child.window === window && child.parent === primary && !child.isPrimary`)
	if err != nil {
		t.Fatal(err)
	}
	if ok, truthErr := value.Truth(); truthErr != nil || !ok {
		t.Fatalf("derived Window/Layer ownership = %v, %v", value, truthErr)
	}
}
