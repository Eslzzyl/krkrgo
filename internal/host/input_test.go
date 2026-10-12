package host

import (
	"math"
	"testing"

	runtime "github.com/eslzzyl/krkrgo/internal/runtime"
	"github.com/hajimehoshi/ebiten/v2"
)

func TestInputWireContract(t *testing.T) {
	if len(trackedKeys) != len(trackedKeyNames) {
		t.Fatalf("tracked key/name tables have inconsistent lengths: %d/%d", len(trackedKeys), len(trackedKeyNames))
	}
	if len(trackedKeys) != len(trackedKeyCodes) {
		t.Fatalf("tracked key/code tables have inconsistent lengths: %d/%d", len(trackedKeys), len(trackedKeyCodes))
	}
	if ShiftStateShift != 0x01 || ShiftStateAlt != 0x02 || ShiftStateCtrl != 0x04 || ShiftStateLeft != 0x08 || ShiftStateRight != 0x10 || ShiftStateMiddle != 0x20 {
		t.Fatalf("shift-state contract changed")
	}
	if trackedKeyCodes[0] != 0x30 || trackedKeyCodes[10] != 0x41 || trackedKeyCodes[67] != 0x27 || trackedKeyCodes[len(trackedKeyCodes)-1] != 0x6d {
		t.Fatalf("unexpected key code table boundaries")
	}
}

func TestWheelDeltaIsStableAndSaturating(t *testing.T) {
	cases := []struct {
		input float64
		want  int32
	}{
		{0.5, 60},
		{-0.5, -60},
		{1.0 / 3.0, 40},
		{math.Inf(1), int32(1<<31 - 1)},
		{math.Inf(-1), int32(-1 << 31)},
	}
	for _, test := range cases {
		if got := wheelDelta(test.input); got != test.want {
			t.Errorf("wheelDelta(%v) = %d, want %d", test.input, got, test.want)
		}
	}
}

func TestInputStateResetClearsEveryDeviceState(t *testing.T) {
	p := &InputPoller{x: 4, y: 5, havePointer: true, focused: true, haveFocus: true}
	p.keys[3], p.buttons[1] = true, true
	p.Reset()
	if *p != (InputPoller{}) {
		t.Fatalf("Reset left state behind: %#v", p)
	}
}

func TestInputEventSequenceUsesRuntimeKinds(t *testing.T) {
	p := &InputPoller{}
	p.keys[51] = true
	if got := p.shiftStateFor(true, 51); got&ShiftStateShift == 0 {
		t.Fatal("modifier press was not included in key event state")
	}
	if got := p.mouseStateWith(0, false); got&ShiftStateLeft != 0 {
		t.Fatal("released pointer button remained in shift state")
	}
	if runtime.InputKeyDown == runtime.InputPointerDown {
		t.Fatal("runtime input kinds must remain distinct")
	}
	if ebiten.KeyShift == ebiten.KeyControl {
		t.Fatal("Ebitengine modifier keys must remain distinct")
	}
}
