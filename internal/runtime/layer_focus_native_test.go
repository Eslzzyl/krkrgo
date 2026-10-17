package runtime

import (
	"testing"
)

func TestNativeLayerFocusPropertiesFollowModalState(t *testing.T) {
	session := NewSession(DefaultServices(), 20_000)
	if _, err := session.executeSource("<layer-focus-test>", `window = new Window(); root = new Layer(window); a = new Layer(window, root); b = new Layer(window, root); root.visible = true; a.visible = true; b.visible = true; a.focusable = true; b.focusable = true; a.focus(); a.setMode()`, false); err != nil {
		t.Fatal(err)
	}
	value, err := session.Evaluate(`[a.focusable,a.nodeEnabled,a.nodeFocusable,a.focused,b.nodeEnabled,b.nodeFocusable,b.focused].join('|')`)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := value.Text(), "1|1|1|1|0|0|0"; got != want {
		t.Fatalf("focus properties while modal = %q, want %q", got, want)
	}
	if _, err := session.Evaluate(`a.focusable = false`); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Evaluate(`a.removeMode()`); err != nil {
		t.Fatal(err)
	}
	value, err = session.Evaluate(`[a.focused,b.nodeEnabled,b.nodeFocusable,b.focused].join('|')`)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := value.Text(), "0|1|1|1"; got != want {
		t.Fatalf("focus properties after modal removal = %q, want %q", got, want)
	}
}

func TestNativeLayerSetModeIsIdempotentForCurrentModal(t *testing.T) {
	session := NewSession(DefaultServices(), 20_000)
	if _, err := session.executeSource("<layer-focus-test>", `window = new Window(); root = new Layer(window); modal = new Layer(window, root); root.visible = true; modal.visible = true; modal.focusable = true; modal.setMode(); modal.setMode()`, false); err != nil {
		t.Fatalf("setting the current modal Layer mode twice: %v", err)
	}
	if _, err := session.Evaluate(`root.setMode()`); err == nil {
		t.Fatal("setting an ancestor of the current modal Layer succeeded")
	}
}
