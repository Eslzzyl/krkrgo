package runtime

import (
	"errors"
	"testing"
)

func TestNativeLayerOrderingProperties(t *testing.T) {
	session := NewSession(DefaultServices(), 20_000)
	defer session.Close()
	eval := func(source string) {
		t.Helper()
		if _, err := session.executeSource("<layer-order-test>", source, false); err != nil {
			t.Fatalf("%s: %v", source, err)
		}
	}
	intAt := func(source string) int64 {
		t.Helper()
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
	truthAt := func(source string) bool {
		t.Helper()
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

	eval(`root = new Layer(); a = new Layer(root); b = new Layer(root); c = new Layer(root); children = root.children`)
	if intAt(`root.absoluteOrderMode`) != 0 || intAt(`a.order`) != 0 || intAt(`b.order`) != 1 || intAt(`c.order`) != 2 || intAt(`a.absolute`) != 0 || intAt(`b.absolute`) != 1 {
		t.Fatal("Layer ordering defaults do not reflect construction order")
	}

	eval(`root.absoluteOrderMode = true; a.absolute = 2`)
	if intAt(`root.absoluteOrderMode`) != 1 || !truthAt(`children === root.children && children[0] === b && children[1] === a && children[2] === c`) {
		t.Fatal("absolute order did not reorder siblings or preserve the children array")
	}
	if intAt(`a.absolute`) != 2 || intAt(`a.order`) != 1 || intAt(`b.absolute`) != 1 {
		t.Fatal("absolute/order getters do not report native sibling state")
	}

	eval(`c.absolute = -1`)
	if !truthAt(`children[0] === c && children[1] === b && children[2] === a`) || intAt(`c.absolute`) != -1 {
		t.Fatal("negative absolute order did not deterministically move the child to the front")
	}

	eval(`a.order = 0`)
	if intAt(`root.absoluteOrderMode`) != 0 || !truthAt(`children[0] === a && children[1] === c && children[2] === b`) {
		t.Fatal("relative order assignment did not disable absolute mode and update children")
	}
	eval(`b.order = -1`)
	if intAt(`b.order`) != 0 || !truthAt(`children[0] === b && children[1] === a && children[2] === c`) {
		t.Fatal("relative order assignment did not clamp and reorder siblings")
	}
	eval(`root.absoluteOrderMode = true`)
	if intAt(`a.absolute`) != 1 || intAt(`b.absolute`) != 0 || intAt(`c.absolute`) != 2 {
		t.Fatal("re-entering absolute mode did not snapshot the current sibling order")
	}

	if _, err := session.Evaluate(`root.absolute = 3`); !errors.Is(err, ErrLayerTree) {
		t.Fatalf("setting absolute order on a root returned %v, want ErrLayerTree", err)
	}
}

func TestNativeLayerBringToFront(t *testing.T) {
	session := NewSession(DefaultServices(), 20_000)
	defer session.Close()
	eval := func(source string) {
		t.Helper()
		if _, err := session.executeSource("<layer-bring-to-front-test>", source, false); err != nil {
			t.Fatalf("%s: %v", source, err)
		}
	}
	intAt := func(source string) int64 {
		t.Helper()
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
	truthAt := func(source string) bool {
		t.Helper()
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

	eval(`parent = new Layer(); a = new Layer(parent); b = new Layer(parent); children = parent.children; b.order = 0`)
	eval(`b.bringToFront()`)
	if intAt(`b.order`) != 1 || !truthAt(`children === parent.children && children[0] === a && children[1] === b`) {
		t.Fatal("bringToFront did not move the layer to the end of its parent's children")
	}

	eval(`detached = new Layer()`)
	if _, err := session.Evaluate(`detached.bringToFront()`); !errors.Is(err, ErrLayerTree) {
		t.Fatalf("bringing a parentless layer to front returned %v, want ErrLayerTree", err)
	}
}
