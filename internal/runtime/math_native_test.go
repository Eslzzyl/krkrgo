package runtime

import (
	"errors"
	"testing"

	"github.com/eslzzyl/krkrgo/internal/tjs"
)

func TestMathNamespacePowConstantsAndEdges(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()

	if _, err := session.executeSource("<math-native-test>", `
		global.mathPow = Math.pow(2, 3);
		global.mathPi = Math.PI;
		global.mathNaN = Math.sqrt(-1);
	`, false); err != nil {
		t.Fatal(err)
	}
	got, err := session.Evaluate(`mathPow == 8 && mathPi > 3.14 && mathPi < 3.15 && mathNaN !== mathNaN`)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := got.Truth(); !ok {
		t.Fatalf("Math results did not match expected values: %v", got)
	}
	if _, err := session.Services.Call(session.VM, "Math.pow", []tjs.Value{tjs.IntegerValue(2)}, &session.Budget); !errors.Is(err, tjs.ErrBadParamCount) {
		t.Fatalf("Math.pow missing-argument error = %v, want bad parameter count", err)
	}
}
