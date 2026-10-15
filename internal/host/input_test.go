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
	zero := InputPoller{}
	if p.keys != zero.keys || p.x != zero.x || p.y != zero.y ||
		p.buttons != zero.buttons || p.havePointer != zero.havePointer ||
		p.focused != zero.focused || p.haveFocus != zero.haveFocus ||
		p.touchIDs != nil || p.touchPoints != nil || p.touch != zero.touch {
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

func TestTouchMapsPrimaryContactToMouseEvents(t *testing.T) {
	const firstID, secondID = ebiten.TouchID(1), ebiten.TouchID(2)
	p := &InputPoller{}
	events := p.appendTouchEvents(nil, []touchPoint{{id: firstID, x: 20, y: 30}})
	events = p.appendTouchEvents(events, []touchPoint{
		{id: secondID, x: 90, y: 90},
		{id: firstID, x: 24, y: 30},
	})
	events = p.appendTouchEvents(events, []touchPoint{{id: secondID, x: 90, y: 90}})
	events = p.appendTouchEvents(events, []touchPoint{{id: secondID, x: 90, y: 90}})

	wantKinds := []runtime.InputKind{
		runtime.InputPointerDown,
		runtime.InputPointerMove,
		runtime.InputPointerUp,
		runtime.InputClick,
		runtime.InputPointerDown,
	}
	if len(events) != len(wantKinds) {
		t.Fatalf("got %d events, want %d: %#v", len(events), len(wantKinds), events)
	}
	for i, want := range wantKinds {
		if events[i].Kind != want {
			t.Errorf("event %d kind = %v, want %v", i, events[i].Kind, want)
		}
	}
	if events[0].X != 20 || events[0].Y != 30 || events[0].Button != 0 || events[0].Shift&ShiftStateLeft == 0 {
		t.Fatalf("touch start did not become left-button down: %#v", events[0])
	}
	if events[1].X != 24 || events[1].Y != 30 || events[1].Shift&ShiftStateLeft == 0 {
		t.Fatalf("primary contact movement was not preserved: %#v", events[1])
	}
	if events[3].X != 24 || events[3].Y != 30 {
		t.Fatalf("tap click position = (%d,%d), want (24,30)", events[3].X, events[3].Y)
	}
}

func TestTouchDragThresholdControlsTapClick(t *testing.T) {
	for _, test := range []struct {
		delta     int
		wantClick bool
	}{{10, true}, {11, false}} {
		p := &InputPoller{}
		point := touchPoint{id: ebiten.TouchID(1), x: 10, y: 20}
		p.appendTouchEvents(nil, []touchPoint{point})
		point.x += test.delta
		p.appendTouchEvents(nil, []touchPoint{point})
		events := p.appendTouchEvents(nil, nil)
		gotClick := len(events) == 2 && events[1].Kind == runtime.InputClick
		if gotClick != test.wantClick {
			t.Errorf("delta %d click = %v, want %v; events %#v", test.delta, gotClick, test.wantClick, events)
		}
	}
}
