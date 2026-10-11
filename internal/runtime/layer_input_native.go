package runtime

import "github.com/eslzzyl/krkrgo/internal/tjs"

func (s *Session) dispatchLayerMouseInput(windowID uint64, window *WindowState, ev InputEvent) error {
	switch ev.Kind {
	case InputPointerMove, InputPointerDown, InputPointerUp, InputClick, InputDoubleClick, InputPointerLeave, InputWheel:
	default:
		return nil
	}
	if s == nil || s.Services == nil || window == nil {
		return nil
	}

	root := s.Services.Layer(tjs.ObjectID(window.primaryLayer))
	if root == nil {
		root = s.Services.Root
		if root != nil && root.windowID != 0 && root.windowID != windowID {
			root = nil
		}
	}
	var target *Layer
	if root != nil && ev.Kind != InputPointerLeave {
		var err error
		target, err = s.Services.hitTestLayer(s.VM, root, ev.X, ev.Y, &s.Budget)
		if err != nil {
			return err
		}
	}
	if err := s.updateLayerMouseTarget(window, target); err != nil {
		return err
	}
	if ev.Kind == InputPointerLeave {
		return nil
	}

	dispatchTarget := target
	if ev.Kind == InputPointerDown {
		if index, ok := pointerCaptureIndex(ev.Button); ok && target != nil {
			window.mouseCapture[index] = target.ID
		}
	} else if ev.Kind == InputPointerUp {
		if index, ok := pointerCaptureIndex(ev.Button); ok {
			captured := window.mouseCapture[index]
			window.mouseCapture[index] = 0
			if captured != 0 {
				dispatchTarget = s.Services.Layer(tjs.ObjectID(captured))
				if dispatchTarget == nil {
					dispatchTarget = target
				}
			}
		}
	}
	if dispatchTarget == nil {
		return nil
	}
	name, args := layerMouseEventArgs(dispatchTarget, ev)
	if name == "" {
		return nil
	}
	return s.Services.layerEvent(s.VM, dispatchTarget, name, args, &s.Budget)
}

// hitTestLayer adds scriptable onHitTest filtering while preserving the current
// hit result across nested getLayerAt calls.
func (s *Services) hitTestLayer(vm *tjs.VM, root *Layer, x, y int32, budget *uint64) (*Layer, error) {
	return s.hitLayerForInput(vm, root, x, y, 0, 0, budget)
}

func (s *Services) hitLayerForInput(vm *tjs.VM, l *Layer, x, y, parentX, parentY int32, budget *uint64) (*Layer, error) {
	if l == nil || !l.Visible || !l.Enabled || l.Opacity == 0 {
		return nil, nil
	}
	absoluteX, absoluteY := parentX+l.Bounds.X, parentY+l.Bounds.Y
	for i := len(l.children) - 1; i >= 0; i-- {
		if hit, err := s.hitLayerForInput(vm, l.children[i], x, y, absoluteX, absoluteY, budget); err != nil || hit != nil {
			return hit, err
		}
	}
	if !layerHitAt(l, x, y, absoluteX, absoluteY) {
		return nil, nil
	}

	window := layerWindowState(s, l)
	previousHit := l.hitTestWork
	if window != nil {
		previousHit = window.hitTestWork
		window.hitTestWork = true
	} else {
		l.hitTestWork = true
	}
	err := s.layerEvent(vm, l, "onHitTest", []tjs.Value{
		tjs.IntegerValue(int64(x - absoluteX)),
		tjs.IntegerValue(int64(y - absoluteY)),
		tjs.IntegerValue(1),
	}, budget)
	accepted := l.hitTestWork
	if window != nil {
		accepted = window.hitTestWork
		window.hitTestWork = previousHit
	} else {
		l.hitTestWork = previousHit
	}
	if err != nil {
		return nil, err
	}
	if !accepted || (l.ID != 0 && s.Layer(tjs.ObjectID(l.ID)) != l) {
		return nil, nil
	}
	return l, nil
}

func (s *Session) updateLayerMouseTarget(window *WindowState, target *Layer) error {
	if window == nil {
		return nil
	}
	var targetID LayerID
	if target != nil {
		targetID = target.ID
	}
	if window.mouseLayer == targetID {
		return nil
	}
	previous := s.Services.Layer(tjs.ObjectID(window.mouseLayer))
	window.mouseLayer = 0
	if previous != nil {
		if err := s.Services.layerEvent(s.VM, previous, "onMouseLeave", nil, &s.Budget); err != nil {
			return err
		}
	}
	if target == nil || s.Services.Layer(tjs.ObjectID(target.ID)) != target {
		return nil
	}
	window.mouseLayer = target.ID
	return s.Services.layerEvent(s.VM, target, "onMouseEnter", nil, &s.Budget)
}

func pointerCaptureIndex(button int32) (int, bool) {
	if button < 0 || button >= 5 {
		return 0, false
	}
	return int(button), true
}

func layerMouseEventArgs(layer *Layer, ev InputEvent) (string, []tjs.Value) {
	name, args := eventArgs(ev)
	if name == "" || len(args) == 0 {
		return name, args
	}
	x, y := ev.X, ev.Y
	for current := layer; current != nil; current = current.Parent {
		x -= current.Bounds.X
		y -= current.Bounds.Y
	}
	if ev.Kind == InputWheel {
		args[2] = tjs.IntegerValue(int64(x))
		args[3] = tjs.IntegerValue(int64(y))
	} else {
		args[0] = tjs.IntegerValue(int64(x))
		args[1] = tjs.IntegerValue(int64(y))
	}
	return name, args
}
