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
