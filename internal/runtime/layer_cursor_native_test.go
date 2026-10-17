package runtime

import (
	"testing"

	"github.com/eslzzyl/krkrgo/internal/tjs"
)

func TestNativeLayerCursorProperties(t *testing.T) {
	session := NewSession(DefaultServices(), 10_000)
	if _, err := session.Evaluate(`l = new Layer()`); err != nil {
		t.Fatal(err)
	}

	for _, property := range []string{"cursor", "cursorX", "cursorY"} {
		value, err := session.Evaluate("l." + property)
		if err != nil {
			t.Fatalf("read default Layer.%s: %v", property, err)
		}
		if value.Kind() != tjs.KindInteger || value.IntegerValue() != 0 {
			t.Errorf("default Layer.%s = %#v, want integer 0", property, value)
		}
	}

	if _, err := session.executeSource("<layer-cursor-test>", `l.cursor = 7; l.cursorX = -17; l.cursorY = 23`, false); err != nil {
		t.Fatal(err)
	}
	for property, want := range map[string]int64{"cursor": 7, "cursorX": -17, "cursorY": 23} {
		value, err := session.Evaluate("l." + property)
		if err != nil {
			t.Fatalf("read Layer.%s: %v", property, err)
		}
		if value.Kind() != tjs.KindInteger || value.IntegerValue() != want {
			t.Errorf("Layer.%s = %#v, want integer %d", property, value, want)
		}
	}

	if _, err := session.Evaluate(`l.cursor = "cursor.cur"`); err != nil {
		t.Fatal(err)
	}
	cursor, err := session.Evaluate(`l.cursor`)
	if err != nil {
		t.Fatal(err)
	}
	if cursor.Kind() != tjs.KindString || cursor.Text() != "cursor.cur" {
		t.Fatalf("Layer.cursor = %#v, want string cursor.cur", cursor)
	}
}

func TestNativeLayerCursorCoordinateValidation(t *testing.T) {
	session := NewSession(DefaultServices(), 10_000)
	if _, err := session.executeSource("<layer-cursor-test>", `l = new Layer(); l.cursorX = -2147483648; l.cursorY = 2147483647`, false); err != nil {
		t.Fatal(err)
	}

	for _, source := range []string{
		`l.cursorX = l`,
		`l.cursorY = l`,
		`l.cursorX = 2147483648`,
		`l.cursorY = -2147483649`,
		`l.cursor = l`,
	} {
		if _, err := session.Evaluate(source); err == nil {
			t.Errorf("accepted invalid cursor assignment %q", source)
		}
	}

	for property, want := range map[string]int64{"cursorX": -2147483648, "cursorY": 2147483647} {
		value, err := session.Evaluate("l." + property)
		if err != nil {
			t.Fatalf("read Layer.%s: %v", property, err)
		}
		if value.Kind() != tjs.KindInteger || value.IntegerValue() != want {
			t.Errorf("Layer.%s after rejected assignments = %#v, want integer %d", property, value, want)
		}
	}
}

func TestNativeLayerSetCursorPos(t *testing.T) {
	session := NewSession(DefaultServices(), 10_000)
	if _, err := session.Evaluate(`l = new Layer(); l.setCursorPos(-17, 23)`); err != nil {
		t.Fatal(err)
	}
	for property, want := range map[string]int64{"cursorX": -17, "cursorY": 23} {
		value, err := session.Evaluate("l." + property)
		if err != nil {
			t.Fatalf("read Layer.%s after setCursorPos: %v", property, err)
		}
		if value.Kind() != tjs.KindInteger || value.IntegerValue() != want {
			t.Errorf("Layer.%s after setCursorPos = %#v, want integer %d", property, value, want)
		}
	}
	for _, source := range []string{`l.setCursorPos()`, `l.setCursorPos(1)`, `l.setCursorPos(2147483648, 0)`} {
		if _, err := session.Evaluate(source); err == nil {
			t.Errorf("accepted invalid Layer.setCursorPos call %q", source)
		}
	}
}

func TestNativeLayerHintProperties(t *testing.T) {
	session := NewSession(DefaultServices(), 10_000)
	if _, err := session.Evaluate(`l = new Layer()`); err != nil {
		t.Fatal(err)
	}
	value, err := session.Evaluate(`l.hint`)
	if err != nil {
		t.Fatalf("read default Layer.hint: %v", err)
	}
	if value.Kind() != tjs.KindString || value.Text() != "" {
		t.Fatalf("default Layer.hint = %#v, want empty string", value)
	}
	if _, err := session.executeSource("<layer-hint-test>", `l.hint = "button tip"; l.showParentHint = 1`, false); err != nil {
		t.Fatal(err)
	}
	value, err = session.Evaluate(`l.hint`)
	if err != nil || value.Kind() != tjs.KindString || value.Text() != "button tip" {
		t.Fatalf("Layer.hint = %#v, %v", value, err)
	}
	value, err = session.Evaluate(`l.showParentHint`)
	if err != nil || value.Kind() != tjs.KindInteger || value.IntegerValue() != 1 {
		t.Fatalf("Layer.showParentHint = %#v, %v", value, err)
	}
	if _, err := session.executeSource("<layer-hint-test>", `l.hint = "next tip"`, false); err != nil {
		t.Fatal(err)
	}
	value, err = session.Evaluate(`l.showParentHint`)
	if err != nil || value.Kind() != tjs.KindInteger || value.IntegerValue() != 0 {
		t.Fatalf("Layer.showParentHint after hint assignment = %#v, %v", value, err)
	}
}
