package runtime

import (
	"fmt"
	"math"

	"github.com/eslzzyl/krkrgo/internal/tjs"
)

// scriptRect is the script-facing four-edge rectangle. It deliberately remains
// separate from Rect, which is the X/Y/Width/Height geometry used by layers.
type scriptRect struct {
	left, top, right, bottom int32
}

func rectObjectContext(context tjs.Value, operation string) (tjs.ObjectID, error) {
	if context.Kind() != tjs.KindObject || context.IsNull() || context.ObjectRef().Object == 0 {
		return 0, fmt.Errorf("Rect.%s requires non-null object context", operation)
	}
	return context.ObjectRef().Object, nil
}

func nativeInt32(value tjs.Value, what string) (int32, error) {
	n, err := value.Int()
	if err != nil {
		return 0, fmt.Errorf("%s requires an integer: %w", what, err)
	}
	if n < math.MinInt32 || n > math.MaxInt32 {
		return 0, fmt.Errorf("%s exceeds int32", what)
	}
	return int32(n), nil
}

func (s *Services) rectNative(vm *tjs.VM, context tjs.Value, operation string, args []tjs.Value) (tjs.Value, error) {
	s.ensureInit()
	id, err := rectObjectContext(context, operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	if operation == "@initialize" || operation == "Rect" {
		if vm == nil || vm.Objects == nil {
			return tjs.VoidValue, fmt.Errorf("Rect.%s requires a VM", operation)
		}
		if object, ok := vm.Objects.Lookup(id); !ok || !object.IsValid() {
			return tjs.VoidValue, fmt.Errorf("Rect.%s: invalid object context", operation)
		}
		state := &scriptRect{}
		switch len(args) {
		case 0:
		case 1:
			if args[0].Kind() != tjs.KindObject || args[0].IsNull() || args[0].ObjectRef().Object == 0 {
				return tjs.VoidValue, fmt.Errorf("Rect.%s requires a Rect object", operation)
			}
			other := s.Rects[args[0].ObjectRef().Object]
			if other == nil {
				return tjs.VoidValue, fmt.Errorf("Rect.%s: invalid Rect object", operation)
			}
			*state = *other
		case 4:
			values := [4]int32{}
			for i := range values {
				values[i], err = nativeInt32(args[i], fmt.Sprintf("Rect.%s argument %d", operation, i))
				if err != nil {
					return tjs.VoidValue, err
				}
			}
			state.left, state.top, state.right, state.bottom = values[0], values[1], values[2], values[3]
		default:
			return tjs.VoidValue, fmt.Errorf("Rect.%s requires 0, 1, or 4 arguments", operation)
		}
		s.mu.Lock()
		s.Rects[id] = state
		s.mu.Unlock()
		return tjs.VoidValue, nil
	}
	if operation == "@invalidate" || operation == "finalize" {
		s.mu.Lock()
		delete(s.Rects, id)
		s.mu.Unlock()
		return tjs.VoidValue, nil
	}

	s.mu.RLock()
	state := s.Rects[id]
	s.mu.RUnlock()
	if state == nil {
		return tjs.VoidValue, fmt.Errorf("Rect.%s: context has no Rect native instance", operation)
	}
	if len(operation) >= 4 && operation[:4] == "get:" {
		switch operation[4:] {
		case "width":
			return tjs.IntegerValue(int64(state.right - state.left)), nil
		case "height":
			return tjs.IntegerValue(int64(state.bottom - state.top)), nil
		case "left":
			return tjs.IntegerValue(int64(state.left)), nil
		case "top":
			return tjs.IntegerValue(int64(state.top)), nil
		case "right":
			return tjs.IntegerValue(int64(state.right)), nil
		case "bottom":
			return tjs.IntegerValue(int64(state.bottom)), nil
		case "nativeArray":
			if vm == nil {
				return tjs.VoidValue, fmt.Errorf("Rect.nativeArray requires a VM")
			}
			return vm.NewArray([]tjs.Value{
				tjs.IntegerValue(int64(state.left)), tjs.IntegerValue(int64(state.top)),
				tjs.IntegerValue(int64(state.right)), tjs.IntegerValue(int64(state.bottom)),
			})
		default:
			return tjs.VoidValue, fmt.Errorf("unknown Rect property: %s", operation[4:])
		}
	}
	if len(operation) >= 4 && operation[:4] == "set:" {
		if len(args) != 1 {
			return tjs.VoidValue, fmt.Errorf("Rect.%s requires exactly one argument", operation)
		}
		value, err := nativeInt32(args[0], "Rect."+operation)
		if err != nil {
			return tjs.VoidValue, err
		}
		switch operation[4:] {
		case "width":
			state.right = state.left + value
		case "height":
			state.bottom = state.top + value
		case "left":
			state.left = value
		case "top":
			state.top = value
		case "right":
			state.right = value
		case "bottom":
			state.bottom = value
		default:
			return tjs.VoidValue, fmt.Errorf("unknown Rect property: %s", operation[4:])
		}
		return tjs.VoidValue, nil
	}

	argCount := func(want int) error {
		if len(args) != want {
			return fmt.Errorf("Rect.%s requires exactly %d arguments", operation, want)
		}
		return nil
	}
	switch operation {
	case "isEmpty":
		if err := argCount(0); err != nil {
			return tjs.VoidValue, err
		}
		return tjs.IntegerValue(boolInt(state.left >= state.right || state.top >= state.bottom)), nil
	case "setSize":
		if err := argCount(2); err != nil {
			return tjs.VoidValue, err
		}
		width, err := nativeInt32(args[0], "Rect.setSize width")
		if err != nil {
			return tjs.VoidValue, err
		}
		height, err := nativeInt32(args[1], "Rect.setSize height")
		if err != nil {
			return tjs.VoidValue, err
		}
		state.right, state.bottom = state.left+width, state.top+height
		return tjs.VoidValue, nil
	case "setOffset":
		if err := argCount(2); err != nil {
			return tjs.VoidValue, err
		}
		x, err := nativeInt32(args[0], "Rect.setOffset x")
		if err != nil {
			return tjs.VoidValue, err
		}
		y, err := nativeInt32(args[1], "Rect.setOffset y")
		if err != nil {
			return tjs.VoidValue, err
		}
		width, height := state.right-state.left, state.bottom-state.top
		state.left, state.top, state.right, state.bottom = x, y, x+width, y+height
		return tjs.VoidValue, nil
	case "addOffset":
		if err := argCount(2); err != nil {
			return tjs.VoidValue, err
		}
		x, err := nativeInt32(args[0], "Rect.addOffset x")
		if err != nil {
			return tjs.VoidValue, err
		}
		y, err := nativeInt32(args[1], "Rect.addOffset y")
		if err != nil {
			return tjs.VoidValue, err
		}
		state.left, state.right = state.left+x, state.right+x
		state.top, state.bottom = state.top+y, state.bottom+y
		return tjs.VoidValue, nil
	case "clear":
		if err := argCount(0); err != nil {
			return tjs.VoidValue, err
		}
		*state = scriptRect{}
		return tjs.VoidValue, nil
	case "set":
		if err := argCount(4); err != nil {
			return tjs.VoidValue, err
		}
		values := [4]int32{}
		for i := range values {
			values[i], err = nativeInt32(args[i], fmt.Sprintf("Rect.set argument %d", i))
			if err != nil {
				return tjs.VoidValue, err
			}
		}
		state.left, state.top, state.right, state.bottom = values[0], values[1], values[2], values[3]
		return tjs.VoidValue, nil
	case "clip", "union", "intersects", "included", "equal":
		if err := argCount(1); err != nil {
			return tjs.VoidValue, err
		}
		other, err := s.rectArgument(vm, args[0], operation)
		if err != nil {
			return tjs.VoidValue, err
		}
		switch operation {
		case "clip":
			left, top := state.left, state.top
			if other.left > left {
				left = other.left
			}
			if other.top > top {
				top = other.top
			}
			right, bottom := state.right, state.bottom
			if other.right < right {
				right = other.right
			}
			if other.bottom < bottom {
				bottom = other.bottom
			}
			if right > left && bottom > top {
				state.left, state.top, state.right, state.bottom = left, top, right, bottom
				return tjs.IntegerValue(1), nil
			}
			return tjs.IntegerValue(0), nil
		case "union":
			left, top := state.left, state.top
			if other.left < left {
				left = other.left
			}
			if other.top < top {
				top = other.top
			}
			right, bottom := state.right, state.bottom
			if other.right > right {
				right = other.right
			}
			if other.bottom > bottom {
				bottom = other.bottom
			}
			if right > left && bottom > top {
				state.left, state.top, state.right, state.bottom = left, top, right, bottom
				return tjs.IntegerValue(1), nil
			}
			return tjs.IntegerValue(0), nil
		case "intersects":
			return tjs.IntegerValue(boolInt(rectIntersects(*state, *other))), nil
		case "included":
			return tjs.IntegerValue(boolInt(rectIncluded(*state, *other))), nil
		default:
			return tjs.IntegerValue(boolInt(*state == *other)), nil
		}
	case "includedPos":
		if err := argCount(2); err != nil {
			return tjs.VoidValue, err
		}
		x, err := nativeInt32(args[0], "Rect.includedPos x")
		if err != nil {
			return tjs.VoidValue, err
		}
		y, err := nativeInt32(args[1], "Rect.includedPos y")
		if err != nil {
			return tjs.VoidValue, err
		}
		return tjs.IntegerValue(boolInt(state.left <= x && x < state.right && state.top <= y && y < state.bottom)), nil
	default:
		return tjs.VoidValue, fmt.Errorf("unknown Rect operation: %s", operation)
	}
}

func rectIntersects(a, b scriptRect) bool {
	if a.left >= a.right || a.top >= a.bottom || b.left >= b.right || b.top >= b.bottom {
		return false
	}
	return !(a.left >= b.right || a.top >= b.bottom || a.right <= b.left || a.bottom <= b.top)
}

func rectIncluded(a, b scriptRect) bool {
	if a.left >= a.right || a.top >= a.bottom || b.left >= b.right || b.top >= b.bottom {
		return false
	}
	return b.left <= a.left && b.top <= a.top && b.right >= a.right && b.bottom >= a.bottom
}

func (s *Services) rectArgument(vm *tjs.VM, value tjs.Value, operation string) (*scriptRect, error) {
	if value.Kind() != tjs.KindObject || value.IsNull() || value.ObjectRef().Object == 0 {
		return nil, fmt.Errorf("Rect.%s requires a Rect object", operation)
	}
	if vm == nil || vm.Objects == nil {
		return nil, fmt.Errorf("Rect.%s requires a VM", operation)
	}
	if object, ok := vm.Objects.Lookup(value.ObjectRef().Object); !ok || !object.IsValid() {
		return nil, fmt.Errorf("Rect.%s: invalid Rect object", operation)
	}
	other := s.Rects[value.ObjectRef().Object]
	if other == nil {
		return nil, fmt.Errorf("Rect.%s: invalid Rect object", operation)
	}
	return other, nil
}
