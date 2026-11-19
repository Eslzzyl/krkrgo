package runtime

import (
	"errors"
	"testing"

	"github.com/eslzzyl/krkrgo/internal/tjs"
)

type recordingCursorDevice struct {
	x, y  int32
	calls int
	err   error
}

func (d *recordingCursorDevice) SetPosition(x, y int32) error {
	d.calls++
	if d.err != nil {
		return d.err
	}
	d.x, d.y = x, y
	return nil
}

func TestSystemCursorPositionTracksInputAndNativeSet(t *testing.T) {
	services := DefaultServices()
	device := &recordingCursorDevice{}
	services.SetCursorDevice(device)
	session := NewSession(services, 10_000)
	defer session.Close()

	if _, err := session.Evaluate(`window = new Window(); window.setPos(100, 200); window.setSize(320, 240)`); err != nil {
		t.Fatal(err)
	}
	window := services.Window(tjs.ObjectID(services.MainWindow))
	if window == nil {
		t.Fatal("main window was not registered")
	}
	if value, err := session.Evaluate(`cursor = System.getCursorPos(); [cursor.x, cursor.y].join(',')`); err != nil || value.Text() != "0,0" {
		t.Fatalf("initial cursor = %q, err=%v; want 0,0", value.Text(), err)
	}
	if err := session.Input(services.MainWindow, PointerMove(13, 17)); err != nil {
		t.Fatal(err)
	}
	if value, err := session.Evaluate(`cursor = System.getCursorPos(); [cursor.x, cursor.y].join(',')`); err != nil || value.Text() != "113,217" {
		t.Fatalf("input cursor = %q, err=%v; want 113,217", value.Text(), err)
	}
	if _, err := session.Evaluate(`System.setCursorPos(-7, 9)`); err != nil {
		t.Fatal(err)
	}
	if device.calls != 1 || device.x != -7 || device.y != 9 {
		t.Fatalf("native cursor device = calls %d position (%d,%d), want 1 (-7,9)", device.calls, device.x, device.y)
	}
	if value, err := session.Evaluate(`cursor = System.getCursorPos(); [cursor.x, cursor.y].join(',')`); err != nil || value.Text() != "-7,9" {
		t.Fatalf("set cursor = %q, err=%v; want -7,9", value.Text(), err)
	}
}

func TestSystemSetCursorPosValidatesArgumentsAndPreservesStateOnDeviceError(t *testing.T) {
	services := DefaultServices()
	device := &recordingCursorDevice{err: UnsupportedDevice("cursor positioning")}
	services.SetCursorDevice(device)
	session := NewSession(services, 10_000)
	defer session.Close()

	if _, err := session.Evaluate(`System.setCursorPos(1)`); err == nil {
		t.Fatal("setCursorPos accepted a missing y argument")
	}
	if _, err := session.Evaluate(`System.setCursorPos(2147483648, 0)`); err == nil {
		t.Fatal("setCursorPos accepted an out-of-range x argument")
	}
	if _, err := session.Evaluate(`System.setCursorPos(1, 2)`); !errors.Is(err, ErrUnsupportedDevice) {
		t.Fatalf("setCursorPos device error = %v, want ErrUnsupportedDevice", err)
	}
	if value, err := session.Evaluate(`cursor = System.getCursorPos(); [cursor.x, cursor.y].join(',')`); err != nil || value.Text() != "0,0" {
		t.Fatalf("cursor after failed set = %q, err=%v; want 0,0", value.Text(), err)
	}
}

func TestLayerSetCursorPosUsesWindowCursorDevice(t *testing.T) {
	services := DefaultServices()
	device := &recordingCursorDevice{}
	services.SetCursorDevice(device)
	session := NewSession(services, 10_000)
	defer session.Close()

	if _, err := session.Evaluate(`
window = new Window();
window.setPos(100, 200);
window.setSize(320, 240);
root = new Layer(window, null);
layer = new Layer(root, null);
layer.setPos(10, 20);
layer.setCursorPos(3, 4);
`); err != nil {
		t.Fatal(err)
	}
	if device.calls != 1 || device.x != 113 || device.y != 224 {
		t.Fatalf("layer cursor device = calls %d position (%d,%d), want 1 (113,224)", device.calls, device.x, device.y)
	}
	if value, err := session.Evaluate(`cursor = System.getCursorPos(); [cursor.x, cursor.y].join(',')`); err != nil || value.Text() != "113,224" {
		t.Fatalf("layer cursor position = %q, err=%v; want 113,224", value.Text(), err)
	}
	if value, err := session.Evaluate(`[layer.cursorX, layer.cursorY].join(',')`); err != nil || value.Text() != "3,4" {
		t.Fatalf("layer local cursor position = %q, err=%v; want 3,4", value.Text(), err)
	}
}
