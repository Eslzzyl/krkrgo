package runtime

import "github.com/eslzzyl/krkrgo/internal/tjs"

// CursorDevice moves the host's desktop cursor. Reading is sourced from the
// deterministic runtime state updated by input events, which also covers
// headless and replay sessions.
type CursorDevice interface {
	SetPosition(x, y int32) error
}

func (s *Services) SetCursorDevice(device CursorDevice) {
	if s == nil {
		return
	}
	s.ensureInit()
	s.mu.Lock()
	s.cursorDevice = device
	s.mu.Unlock()
}

func (s *Services) cursorPosition() (int32, int32) {
	if s == nil {
		return 0, 0
	}
	s.mu.RLock()
	x, y, known := s.cursorX, s.cursorY, s.cursorKnown
	s.mu.RUnlock()
	if !known {
		return 0, 0
	}
	return x, y
}

func (s *Services) setCursorPosition(x, y int32) error {
	if s == nil {
		return ErrUnsupportedDevice
	}
	s.ensureInit()
	s.mu.RLock()
	device := s.cursorDevice
	s.mu.RUnlock()
	if device != nil {
		if err := device.SetPosition(x, y); err != nil {
			return err
		}
	}
	s.mu.Lock()
	s.cursorX, s.cursorY = x, y
	s.cursorKnown = true
	s.mu.Unlock()
	return nil
}

func (s *Services) updateCursorPosition(windowID uint64, event InputEvent) {
	if s == nil {
		return
	}
	switch event.Kind {
	case InputPointerMove, InputPointerDown, InputPointerUp, InputClick, InputDoubleClick, InputWheel:
	default:
		return
	}
	window := s.Window(tjs.ObjectID(windowID))
	if window == nil {
		return
	}
	client := window.ClientRect()
	x := satAdd(client[0], event.X)
	y := satAdd(client[1], event.Y)
	s.mu.Lock()
	s.cursorX, s.cursorY = x, y
	s.cursorKnown = true
	s.mu.Unlock()
}
func (s *Services) setLayerCursorPosition(layer *Layer, x, y int32) error {
	if layer == nil {
		return ErrInvalidWindow
	}
	window := layerWindowState(s, layer)
	if window == nil {
		layer.CursorX, layer.CursorY = x, y
		return nil
	}
	for parent := layer; parent.Parent != nil; parent = parent.Parent {
		x = satAdd(x, parent.Bounds.X)
		y = satAdd(y, parent.Bounds.Y)
	}
	client := window.ClientRect()
	if err := s.setCursorPosition(satAdd(client[0], x), satAdd(client[1], y)); err != nil {
		return err
	}
	layer.CursorX, layer.CursorY = x, y
	return nil
}

func (s *Services) layerCursorPosition(layer *Layer) (int32, int32, bool) {
	if layer == nil {
		return 0, 0, false
	}
	window := layerWindowState(s, layer)
	if window == nil {
		return layer.CursorX, layer.CursorY, true
	}
	x, y := s.cursorPosition()
	client := window.ClientRect()
	x -= client[0]
	y -= client[1]
	for parent := layer; parent.Parent != nil; parent = parent.Parent {
		x = satInt64(int64(x) - int64(parent.Bounds.X))
		y = satInt64(int64(y) - int64(parent.Bounds.Y))
	}
	return x, y, true
}

func cursorPointValue(vm *tjs.VM, x, y int32) (tjs.Value, error) {
	value, err := vm.NewDictionary()
	if err != nil {
		return tjs.VoidValue, err
	}
	object, ok := vm.Objects.Lookup(value.ObjectRef().Object)
	if !ok {
		return tjs.VoidValue, ErrInvalidWindow
	}
	if err := object.Set("x", tjs.IntegerValue(int64(x))); err != nil {
		return tjs.VoidValue, err
	}
	if err := object.Set("y", tjs.IntegerValue(int64(y))); err != nil {
		return tjs.VoidValue, err
	}
	return value, nil
}
