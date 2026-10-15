package host

import (
	"math"

	"github.com/hajimehoshi/ebiten/v2"

	"github.com/eslzzyl/krkrgo/internal/core"
	runtime "github.com/eslzzyl/krkrgo/internal/runtime"
)

// A 10-pixel slop approximates krkr2's DPI/10 touch-move threshold at 96 DPI.
const touchMouseMoveThresholdSq = 10 * 10

// Kirikiri's shift-state bits are part of the script-visible input contract.
const (
	ShiftStateShift  int32 = 0x01
	ShiftStateAlt    int32 = 0x02
	ShiftStateCtrl   int32 = 0x04
	ShiftStateLeft   int32 = 0x08
	ShiftStateRight  int32 = 0x10
	ShiftStateMiddle int32 = 0x20
)

// InputPoller converts Ebitengine's level-triggered device state into stable
// edge events. Ebitengine is sampled only from Update, so the same update
// schedule produces the same event order on every supported backend.
type InputPoller struct {
	keys        [len(trackedKeys)]bool
	x, y        int
	buttons     [3]bool
	havePointer bool
	focused     bool
	haveFocus   bool

	touchIDs    []ebiten.TouchID
	touchPoints []touchPoint
	touch       touchMouseState
}

type touchPoint struct {
	id ebiten.TouchID
	x  int
	y  int
}

type touchMouseState struct {
	id             ebiten.TouchID
	x, y           int
	startX, startY int
	active, moved  bool
}

// trackedKeys is a fixed physical-key set. Ebitengine intentionally exposes
// no portable key enumeration; this list covers script-visible keyboard keys
// and keeps their order stable for focus-loss releases and replay.
var trackedKeys = [...]ebiten.Key{
	ebiten.Key0, ebiten.Key1, ebiten.Key2, ebiten.Key3, ebiten.Key4,
	ebiten.Key5, ebiten.Key6, ebiten.Key7, ebiten.Key8, ebiten.Key9,
	ebiten.KeyA, ebiten.KeyB, ebiten.KeyC, ebiten.KeyD, ebiten.KeyE,
	ebiten.KeyF, ebiten.KeyG, ebiten.KeyH, ebiten.KeyI, ebiten.KeyJ,
	ebiten.KeyK, ebiten.KeyL, ebiten.KeyM, ebiten.KeyN, ebiten.KeyO,
	ebiten.KeyP, ebiten.KeyQ, ebiten.KeyR, ebiten.KeyS, ebiten.KeyT,
	ebiten.KeyU, ebiten.KeyV, ebiten.KeyW, ebiten.KeyX, ebiten.KeyY,
	ebiten.KeyZ, ebiten.KeyF1, ebiten.KeyF2, ebiten.KeyF3, ebiten.KeyF4,
	ebiten.KeyF5, ebiten.KeyF6, ebiten.KeyF7, ebiten.KeyF8, ebiten.KeyF9,
	ebiten.KeyF10, ebiten.KeyF11, ebiten.KeyF12, ebiten.KeyEscape,
	ebiten.KeyTab, ebiten.KeyCapsLock, ebiten.KeyShift, ebiten.KeyControl,
	ebiten.KeyAlt, ebiten.KeyMeta, ebiten.KeyEnter, ebiten.KeyBackspace,
	ebiten.KeySpace, ebiten.KeyInsert, ebiten.KeyDelete, ebiten.KeyHome,
	ebiten.KeyEnd, ebiten.KeyPageUp, ebiten.KeyPageDown, ebiten.KeyArrowUp,
	ebiten.KeyArrowDown, ebiten.KeyArrowLeft, ebiten.KeyArrowRight,
	ebiten.KeyBackquote, ebiten.KeyBackslash, ebiten.KeyBracketLeft,
	ebiten.KeyBracketRight, ebiten.KeyComma, ebiten.KeyEqual, ebiten.KeyMinus,
	ebiten.KeyPeriod, ebiten.KeyQuote, ebiten.KeySemicolon, ebiten.KeySlash,
	ebiten.KeyPrintScreen, ebiten.KeyScrollLock, ebiten.KeyPause,
	ebiten.KeyContextMenu, ebiten.KeyF13, ebiten.KeyF14, ebiten.KeyF15,
	ebiten.KeyF16, ebiten.KeyF17, ebiten.KeyF18, ebiten.KeyF19, ebiten.KeyF20,
	ebiten.KeyF21, ebiten.KeyF22, ebiten.KeyF23, ebiten.KeyF24, ebiten.KeyNumLock,
	ebiten.KeyNumpad0, ebiten.KeyNumpad1, ebiten.KeyNumpad2, ebiten.KeyNumpad3,
	ebiten.KeyNumpad4, ebiten.KeyNumpad5, ebiten.KeyNumpad6, ebiten.KeyNumpad7,
	ebiten.KeyNumpad8, ebiten.KeyNumpad9, ebiten.KeyNumpadAdd,
	ebiten.KeyNumpadDecimal, ebiten.KeyNumpadDivide, ebiten.KeyNumpadEnter,
	ebiten.KeyNumpadEqual, ebiten.KeyNumpadMultiply, ebiten.KeyNumpadSubtract,
}

var trackedKeyNames = [...]string{
	"0", "1", "2", "3", "4", "5", "6", "7", "8", "9",
	"A", "B", "C", "D", "E", "F", "G", "H", "I", "J",
	"K", "L", "M", "N", "O", "P", "Q", "R", "S", "T",
	"U", "V", "W", "X", "Y", "Z", "F1", "F2", "F3", "F4",
	"F5", "F6", "F7", "F8", "F9", "F10", "F11", "F12", "Escape",
	"Tab", "CapsLock", "Shift", "Control", "Alt", "Meta", "Enter",
	"Backspace", "Space", "Insert", "Delete", "Home", "End", "PageUp",
	"PageDown", "ArrowUp", "ArrowDown", "ArrowLeft", "ArrowRight",
	"Backquote", "Backslash", "BracketLeft", "BracketRight", "Comma",
	"Equal", "Minus", "Period", "Quote", "Semicolon", "Slash",
	"PrintScreen", "ScrollLock", "Pause", "ContextMenu",
	"F13", "F14", "F15", "F16", "F17", "F18", "F19", "F20",
	"F21", "F22", "F23", "F24", "NumLock",
	"Numpad0", "Numpad1", "Numpad2", "Numpad3", "Numpad4", "Numpad5",
	"Numpad6", "Numpad7", "Numpad8", "Numpad9", "NumpadAdd",
	"NumpadDecimal", "NumpadDivide", "NumpadEnter", "NumpadEqual",
	"NumpadMultiply", "NumpadSubtract",
}

// These are the stable Windows virtual-key values used by Kirikiri's C++ host.
// They are portable wire values, not Ebitengine implementation values.
var trackedKeyCodes = [...]int32{
	0x30, 0x31, 0x32, 0x33, 0x34, 0x35, 0x36, 0x37, 0x38, 0x39,
	0x41, 0x42, 0x43, 0x44, 0x45, 0x46, 0x47, 0x48, 0x49, 0x4a,
	0x4b, 0x4c, 0x4d, 0x4e, 0x4f, 0x50, 0x51, 0x52, 0x53, 0x54,
	0x55, 0x56, 0x57, 0x58, 0x59, 0x5a, 0x70, 0x71, 0x72, 0x73,
	0x74, 0x75, 0x76, 0x77, 0x78, 0x79, 0x7a, 0x7b, 0x1b, 0x09,
	0x14, 0x10, 0x11, 0x12, 0x5b, 0x0d, 0x08, 0x20, 0x2d, 0x2e,
	0x24, 0x23, 0x21, 0x22, 0x26, 0x28, 0x25, 0x27,
	0xc0, 0xdc, 0xdb, 0xdd, 0xbc, 0xbb, 0xbd, 0xbe,
	0xde, 0xba, 0xbf, 0x2c, 0x91, 0x13, 0x5d,
	0x7c, 0x7d, 0x7e, 0x7f, 0x80, 0x81, 0x82, 0x83,
	0x84, 0x85, 0x86, 0x87, 0x90,
	0x60, 0x61, 0x62, 0x63, 0x64, 0x65, 0x66, 0x67,
	0x68, 0x69, 0x6b, 0x6e, 0x6f, 0x0d, 0xbb, 0x6a, 0x6d,
}

var trackedButtons = [...]ebiten.MouseButton{ebiten.MouseButtonLeft, ebiten.MouseButtonRight, ebiten.MouseButtonMiddle}

// Poll retains the original core action API for embedders and replay clients.
// Rich host events (wheel, text, modifiers, focus) are available through
// PollRuntime and are used by Game; this method preserves the core JSON shape.
func (p *InputPoller) Poll(dst []core.InputEvent, timeMS uint64) []core.InputEvent {
	if p == nil {
		return append(dst, core.InputEvent{TimeMS: timeMS, Action: core.NewAdvanceAction()})
	}
	for i, key := range trackedKeys {
		pressed := ebiten.IsKeyPressed(key)
		if pressed != p.keys[i] {
			dst = append(dst, core.InputEvent{TimeMS: timeMS, Action: core.NewKeyAction(trackedKeyNames[i], pressed)})
			p.keys[i] = pressed
		}
	}
	x, y := ebiten.CursorPosition()
	pointerChanged := !p.havePointer || p.x != x || p.y != y
	var pointerPressed bool
	for i, button := range trackedButtons {
		pressed := ebiten.IsMouseButtonPressed(button)
		if pressed != p.buttons[i] {
			pointerChanged = true
		}
		p.buttons[i] = pressed
		pointerPressed = pointerPressed || pressed
	}
	if pointerChanged {
		dst = append(dst, core.InputEvent{TimeMS: timeMS, Action: core.NewPointerAction(int32(x), int32(y), pointerPressed)})
		p.x, p.y, p.havePointer = x, y, true
	}
	return append(dst, core.InputEvent{TimeMS: timeMS, Action: core.NewAdvanceAction()})
}

// PollRuntime returns the complete portable event set consumed by Session.Input.
// Ordering is fixed: focus transition, key edges, pointer motion, button edges,
// touch contacts, wheel, then text. Losing focus emits releases for sampled
// state before the poller's state is cleared, preventing stuck input.
func (p *InputPoller) PollRuntime(dst []runtime.InputEvent) []runtime.InputEvent {
	if p == nil {
		return dst
	}
	focused := ebiten.IsFocused()
	x, y := ebiten.CursorPosition()
	if !p.haveFocus || focused != p.focused {
		dst = append(dst, runtime.InputEvent{Kind: runtime.InputFocus, Focused: focused})
		p.haveFocus, p.focused = true, focused
		if !focused {
			shift := p.shiftState()
			for i, pressed := range p.keys {
				if pressed {
					dst = append(dst, runtime.InputEvent{Kind: runtime.InputKeyUp, Key: trackedKeyCodes[i], Shift: shift})
				}
			}
			for i, pressed := range p.buttons {
				if pressed {
					dst = append(dst, runtime.InputEvent{Kind: runtime.InputPointerUp, X: int32(x), Y: int32(y), Button: int32(i), Shift: shift | buttonShift(i)})
				}
			}
			if p.touch.active {
				dst = append(dst, runtime.InputEvent{
					Kind: runtime.InputPointerUp, X: int32(p.touch.x), Y: int32(p.touch.y),
					Button: 0, Shift: shift | buttonShift(0),
				})
			}
			p.touch = touchMouseState{}
			p.touchIDs = p.touchIDs[:0]
			p.touchPoints = p.touchPoints[:0]
			p.keys = [len(trackedKeys)]bool{}
			p.buttons = [3]bool{}
			p.havePointer = false
			p.x, p.y = x, y
			return dst
		}
	}
	previousKeys := p.keys
	for i, key := range trackedKeys {
		p.keys[i] = ebiten.IsKeyPressed(key)
	}
	for i, pressed := range p.keys {
		if pressed == previousKeys[i] {
			continue
		}
		kind := runtime.InputKeyUp
		if pressed {
			kind = runtime.InputKeyDown
		}
		dst = append(dst, runtime.InputEvent{Kind: kind, Key: trackedKeyCodes[i], Shift: p.shiftStateFor(pressed, i)})
	}
	pointerMoved := !p.havePointer || p.x != x || p.y != y
	if pointerMoved {
		dst = append(dst, runtime.InputEvent{Kind: runtime.InputPointerMove, X: int32(x), Y: int32(y), Shift: p.shiftState() | p.mouseState()})
	}
	for i, button := range trackedButtons {
		pressed := ebiten.IsMouseButtonPressed(button)
		if pressed != p.buttons[i] {
			kind := runtime.InputPointerUp
			if pressed {
				kind = runtime.InputPointerDown
			}
			dst = append(dst, runtime.InputEvent{Kind: kind, X: int32(x), Y: int32(y), Button: int32(i), Shift: p.shiftState() | p.mouseStateWith(i, pressed)})
		}
		p.buttons[i] = pressed
	}
	p.x, p.y, p.havePointer = x, y, true
	if focused {
		dst = p.pollTouch(dst)
	}

	// Runtime.InputEvent carries one scalar delta. Preserve vertical wheel
	// motion when both axes are present; use horizontal motion as a fallback.
	xoff, yoff := ebiten.Wheel()
	if yoff != 0 {
		dst = append(dst, runtime.InputEvent{Kind: runtime.InputWheel, X: int32(x), Y: int32(y), Delta: wheelDelta(yoff), Shift: p.shiftState() | p.mouseState()})
	} else if xoff != 0 {
		dst = append(dst, runtime.InputEvent{Kind: runtime.InputWheel, X: int32(x), Y: int32(y), Delta: wheelDelta(xoff), Shift: p.shiftState() | p.mouseState()})
	}
	chars := ebiten.AppendInputChars(nil)
	if len(chars) != 0 {
		dst = append(dst, runtime.InputEvent{Kind: runtime.InputText, Text: string(chars)})
	}
	return dst
}

func (p *InputPoller) pollTouch(dst []runtime.InputEvent) []runtime.InputEvent {
	p.touchIDs = ebiten.AppendTouchIDs(p.touchIDs[:0])
	p.touchPoints = p.touchPoints[:0]
	for _, id := range p.touchIDs {
		x, y := ebiten.TouchPosition(id)
		p.touchPoints = append(p.touchPoints, touchPoint{id: id, x: x, y: y})
	}
	return p.appendTouchEvents(dst, p.touchPoints)
}

func (p *InputPoller) appendTouchEvents(dst []runtime.InputEvent, points []touchPoint) []runtime.InputEvent {
	if !p.touch.active {
		if len(points) == 0 {
			return dst
		}
		point := points[0]
		p.touch = touchMouseState{
			id: point.id, x: point.x, y: point.y,
			startX: point.x, startY: point.y, active: true,
		}
		return append(dst, runtime.InputEvent{
			Kind: runtime.InputPointerDown, X: int32(point.x), Y: int32(point.y),
			Button: 0, Shift: p.shiftState() | p.mouseState() | ShiftStateLeft,
		})
	}

	for _, point := range points {
		if point.id != p.touch.id {
			continue
		}
		if point.x != p.touch.x || point.y != p.touch.y {
			dx, dy := point.x-p.touch.startX, point.y-p.touch.startY
			if dx*dx+dy*dy > touchMouseMoveThresholdSq {
				p.touch.moved = true
			}
			p.touch.x, p.touch.y = point.x, point.y
			dst = append(dst, runtime.InputEvent{
				Kind: runtime.InputPointerMove, X: int32(point.x), Y: int32(point.y),
				Shift: p.shiftState() | p.mouseState() | ShiftStateLeft,
			})
		}
		return dst
	}

	dst = append(dst, runtime.InputEvent{
		Kind: runtime.InputPointerUp, X: int32(p.touch.x), Y: int32(p.touch.y),
		Button: 0, Shift: p.shiftState() | p.mouseState(),
	})
	if !p.touch.moved {
		dst = append(dst, runtime.InputEvent{
			Kind: runtime.InputClick, X: int32(p.touch.x), Y: int32(p.touch.y),
		})
	}
	p.touch = touchMouseState{}
	return dst
}

func (p *InputPoller) shiftState() int32 {
	state := int32(0)
	for i, key := range trackedKeys {
		if !p.keys[i] {
			continue
		}
		switch key {
		case ebiten.KeyShift:
			state |= ShiftStateShift
		case ebiten.KeyAlt:
			state |= ShiftStateAlt
		case ebiten.KeyControl:
			state |= ShiftStateCtrl
		}
	}
	return state
}

func (p *InputPoller) shiftStateFor(pressed bool, changed int) int32 {
	state := p.shiftState()
	switch trackedKeys[changed] {
	case ebiten.KeyShift:
		if pressed {
			state |= ShiftStateShift
		} else {
			state &^= ShiftStateShift
		}
	case ebiten.KeyAlt:
		if pressed {
			state |= ShiftStateAlt
		} else {
			state &^= ShiftStateAlt
		}
	case ebiten.KeyControl:
		if pressed {
			state |= ShiftStateCtrl
		} else {
			state &^= ShiftStateCtrl
		}
	}
	return state
}

func (p *InputPoller) mouseState() int32 {
	state := int32(0)
	for i, pressed := range p.buttons {
		if pressed {
			state |= buttonShift(i)
		}
	}
	return state
}

func (p *InputPoller) mouseStateWith(changed int, pressed bool) int32 {
	state := p.mouseState()
	bit := buttonShift(changed)
	if pressed {
		state |= bit
	} else {
		state &^= bit
	}
	return state
}

func buttonShift(button int) int32 {
	switch button {
	case 0:
		return ShiftStateLeft
	case 1:
		return ShiftStateRight
	case 2:
		return ShiftStateMiddle
	default:
		return 0
	}
}

func wheelDelta(value float64) int32 {
	const maxInt32Float = float64(1<<31 - 1)
	const minInt32Float = float64(-1 << 31)
	if value > maxInt32Float/120 {
		return int32(1<<31 - 1)
	}
	if value < minInt32Float/120 {
		return int32(-1 << 31)
	}
	return int32(math.Round(value * 120))
}

// Reset forgets sampled device state. The next PollRuntime emits a focus
// snapshot and current edges, which is useful after a focus loss or restart.
func (p *InputPoller) Reset() {
	if p == nil {
		return
	}
	*p = InputPoller{}
}
