package runtime

import (
	"fmt"
	"github.com/eslzzyl/krkrgo/internal/tjs"
	"log"
	"math"
	"os"
	"time"
)

const defaultTimerActionName = "action"

type timerAction struct {
	owner tjs.ObjectRef
	name  string
}

func timerObjectContext(context tjs.Value, operation string) (tjs.ObjectID, error) {
	if context.Kind() != tjs.KindObject || context.IsNull() || context.ObjectRef().Object == 0 {
		return 0, fmt.Errorf("Timer.%s requires non-null object context", operation)
	}
	return context.ObjectRef().Object, nil
}

func (s *Services) timerNative(vm *tjs.VM, context tjs.Value, operation string, args []tjs.Value, budget *uint64) (tjs.Value, error) {
	s.ensureInit()
	id, err := timerObjectContext(context, operation)
	if err != nil {
		return tjs.VoidValue, err
	}

	if operation == "@initialize" || operation == "Timer" {
		if len(args) < 1 || args[0].Kind() != tjs.KindObject || args[0].IsNull() || args[0].ObjectRef().Object == 0 {
			return tjs.VoidValue, fmt.Errorf("Timer.%s requires an ActionOwner object", operation)
		}
		if vm == nil || vm.Objects == nil {
			return tjs.VoidValue, fmt.Errorf("Timer.%s requires a VM", operation)
		}
		owner := args[0].ObjectRef()
		if ownerObject, ok := vm.Objects.Lookup(owner.Object); !ok || !ownerObject.IsValid() {
			return tjs.VoidValue, fmt.Errorf("Timer.%s: invalid ActionOwner object", operation)
		}
		actionName := defaultTimerActionName
		if len(args) >= 2 && args[1].Kind() != tjs.KindVoid {
			actionName = args[1].Text()
		}
		if existing := s.Timers.Timer(uint64(id)); existing != nil {
			s.mu.Lock()
			s.TimerActions[id] = timerAction{owner: owner, name: actionName}
			s.mu.Unlock()
			return tjs.VoidValue, nil
		}
		if _, err := s.Timers.Register(uint64(id)); err != nil {
			return tjs.VoidValue, fmt.Errorf("Timer.%s: %w", operation, err)
		}
		s.mu.Lock()
		s.TimerActions[id] = timerAction{owner: owner, name: actionName}
		s.mu.Unlock()
		// DispatchEvents intentionally uses direct object members for event
		// handlers. Shadow the class method on this instance so the timer's
		// native onTimer operation is reached by the existing dispatcher.
		if class, ok := vm.Globals["Timer"]; ok {
			if classObject, ok := vm.Objects.Lookup(class.ObjectRef().Object); ok {
				if method, ok := classObject.Get("onTimer"); ok {
					if instance, ok := vm.Objects.Lookup(id); ok {
						if err := instance.Set("onTimer", method); err != nil {
							return tjs.VoidValue, err
						}
					}
				}
			}
		}
		return tjs.VoidValue, nil
	}

	if operation == "@invalidate" || operation == "finalize" {
		s.mu.Lock()
		delete(s.TimerActions, id)
		s.mu.Unlock()
		s.Timers.Unregister(uint64(id))
		return tjs.VoidValue, nil
	}

	timer := s.Timers.Timer(uint64(id))
	if timer == nil {
		return tjs.VoidValue, fmt.Errorf("Timer.%s: context has no Timer native instance", operation)
	}
	if operation == "onTimer" {
		return tjs.VoidValue, s.timerAction(vm, id, budget)
	}
	if len(operation) >= 4 && operation[:4] == "get:" {
		switch operation[4:] {
		case "interval":
			return tjs.RealValue(timer.Interval()), nil
		case "enabled":
			return tjs.IntegerValue(boolInt(timer.Enabled())), nil
		case "capacity":
			return tjs.IntegerValue(int64(timer.Capacity())), nil
		case "mode":
			return tjs.IntegerValue(int64(timer.Mode())), nil
		default:
			return tjs.VoidValue, fmt.Errorf("unknown Timer property: %s", operation[4:])
		}
	}
	if len(operation) >= 4 && operation[:4] == "set:" {
		if len(args) < 1 {
			return tjs.VoidValue, fmt.Errorf("Timer.%s: missing argument 0", operation)
		}
		switch operation[4:] {
		case "interval":
			value, err := args[0].Float()
			if err != nil {
				return tjs.VoidValue, err
			}
			return tjs.VoidValue, timer.SetInterval(value)
		case "enabled":
			value, err := args[0].Truth()
			if err != nil {
				return tjs.VoidValue, err
			}
			return tjs.VoidValue, timer.SetEnabled(value)
		case "capacity":
			value, err := args[0].Int()
			if err != nil {
				return tjs.VoidValue, err
			}
			if value < math.MinInt32 || value > math.MaxInt32 {
				return tjs.VoidValue, fmt.Errorf("Timer.capacity exceeds int32")
			}
			return tjs.VoidValue, timer.SetCapacity(int32(value))
		case "mode":
			value, err := args[0].Int()
			if err != nil {
				return tjs.VoidValue, err
			}
			if value < math.MinInt32 || value > math.MaxInt32 {
				return tjs.VoidValue, fmt.Errorf("Timer.mode exceeds int32")
			}
			return tjs.VoidValue, timer.SetMode(int(value))
		default:
			return tjs.VoidValue, fmt.Errorf("unknown Timer property: %s", operation[4:])
		}
	}
	return tjs.VoidValue, fmt.Errorf("unknown Timer operation: %s", operation)
}

func (s *Services) timerAction(vm *tjs.VM, id tjs.ObjectID, budget *uint64) error {
	if vm == nil || vm.Objects == nil {
		return fmt.Errorf("Timer.onTimer requires a VM")
	}
	s.mu.RLock()
	action, ok := s.TimerActions[id]
	s.mu.RUnlock()
	if !ok {
		return nil
	}
	owner := tjs.ObjectValue(action.owner)
	var function tjs.Value
	if action.name == "" {
		function = owner
	} else {
		object, exists := vm.Objects.Lookup(action.owner.Object)
		if !exists || !object.IsValid() {
			return nil
		}
		var found bool
		function, found = object.Get(action.name)
		if !found {
			return nil
		}
	}
	if function.Kind() != tjs.KindObject || function.IsNull() {
		return nil
	}
	profile := os.Getenv("KRKRGO_RENDER_STATS") != ""
	callStarted := time.Time{}
	if profile {
		callStarted = time.Now()
	}
	_, err := vm.CallFunction(function, owner, nil, s, budget)
	if profile {
		log.Printf("krkrgo timer action: timer=%d owner=%d action=%q elapsed=%s", id, action.owner.Object, action.name, time.Since(callStarted))
	}
	return err
}
