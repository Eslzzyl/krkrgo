package runtime

import (
	"fmt"

	"github.com/eslzzyl/krkrgo/internal/tjs"
)

const defaultAsyncTriggerActionName = "action"

type asyncTriggerState struct {
	owner  tjs.ObjectRef
	name   string
	cached bool
	mode   Priority
}

func asyncTriggerObjectContext(context tjs.Value, operation string) (tjs.ObjectID, error) {
	if context.Kind() != tjs.KindObject || context.IsNull() || context.ObjectRef().Object == 0 {
		return 0, fmt.Errorf("AsyncTrigger.%s requires non-null object context", operation)
	}
	return context.ObjectRef().Object, nil
}

func (s *Services) asyncTriggerNative(vm *tjs.VM, context tjs.Value, operation string, args []tjs.Value, budget *uint64) (tjs.Value, error) {
	s.ensureInit()
	id, err := asyncTriggerObjectContext(context, operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	if operation == "@initialize" || operation == "AsyncTrigger" {
		if vm == nil || vm.Objects == nil {
			return tjs.VoidValue, fmt.Errorf("AsyncTrigger.%s requires a VM", operation)
		}
		if object, ok := vm.Objects.Lookup(id); !ok || !object.IsValid() {
			return tjs.VoidValue, fmt.Errorf("AsyncTrigger.%s: invalid object context", operation)
		}
		if len(args) < 1 || len(args) > 2 || args[0].Kind() != tjs.KindObject || args[0].IsNull() || args[0].ObjectRef().Object == 0 {
			return tjs.VoidValue, fmt.Errorf("AsyncTrigger.%s requires an ActionOwner object and optional ActionName", operation)
		}
		owner := args[0].ObjectRef()
		if object, ok := vm.Objects.Lookup(owner.Object); !ok || !object.IsValid() {
			return tjs.VoidValue, fmt.Errorf("AsyncTrigger.%s: invalid ActionOwner object", operation)
		}
		name := defaultAsyncTriggerActionName
		if len(args) == 2 && !args[1].IsVoid() {
			name = args[1].Text()
		}
		state := &asyncTriggerState{owner: owner, name: name, cached: true, mode: PriorityNormal}
		s.mu.Lock()
		s.AsyncTriggers[id] = state
		s.AsyncTriggerActions[id] = timerAction{owner: owner, name: name}
		s.mu.Unlock()
		if class, ok := vm.Globals["AsyncTrigger"]; ok {
			if classObject, ok := vm.Objects.Lookup(class.ObjectRef().Object); ok {
				if method, ok := classObject.Get("onFire"); ok {
					if instance, ok := vm.Objects.Lookup(id); ok {
						if err := instance.Set("onFire", method); err != nil {
							return tjs.VoidValue, err
						}
					}
				}
			}
		}
		return tjs.VoidValue, nil
	}
	if operation == "@invalidate" || operation == "finalize" {
		s.Events.Cancel(uint64(id), EventTrigger)
		s.mu.Lock()
		delete(s.AsyncTriggers, id)
		delete(s.AsyncTriggerActions, id)
		s.mu.Unlock()
		return tjs.VoidValue, nil
	}

	s.mu.RLock()
	state := s.AsyncTriggers[id]
	s.mu.RUnlock()
	if state == nil {
		return tjs.VoidValue, fmt.Errorf("AsyncTrigger.%s: context has no AsyncTrigger native instance", operation)
	}
	if len(operation) >= 4 && operation[:4] == "get:" {
		switch operation[4:] {
		case "cached":
			return tjs.IntegerValue(boolInt(state.cached)), nil
		case "mode":
			return tjs.IntegerValue(int64(state.mode)), nil
		default:
			return tjs.VoidValue, fmt.Errorf("unknown AsyncTrigger property: %s", operation[4:])
		}
	}
	if len(operation) >= 4 && operation[:4] == "set:" {
		if len(args) != 1 {
			return tjs.VoidValue, fmt.Errorf("AsyncTrigger.%s requires exactly one argument", operation)
		}
		switch operation[4:] {
		case "cached":
			cached, err := args[0].Truth()
			if err != nil {
				return tjs.VoidValue, err
			}
			if state.cached != cached {
				state.cached = cached
				s.Events.Cancel(uint64(id), EventTrigger)
			}
			return tjs.VoidValue, nil
		case "mode":
			mode, err := args[0].Int()
			if err != nil {
				return tjs.VoidValue, err
			}
			state.mode = normalizePriority(int(mode))
			s.Events.Cancel(uint64(id), EventTrigger)
			return tjs.VoidValue, nil
		default:
			return tjs.VoidValue, fmt.Errorf("unknown AsyncTrigger property: %s", operation[4:])
		}
	}

	switch operation {
	case "trigger":
		if len(args) != 0 {
			return tjs.VoidValue, fmt.Errorf("AsyncTrigger.trigger requires exactly 0 arguments")
		}
		if state.cached {
			s.Events.Cancel(uint64(id), EventTrigger)
		}
		if err := s.Events.Post(uint64(id), EventTrigger, state.mode); err != nil {
			return tjs.VoidValue, err
		}
		return tjs.VoidValue, nil
	case "cancel":
		if len(args) != 0 {
			return tjs.VoidValue, fmt.Errorf("AsyncTrigger.cancel requires exactly 0 arguments")
		}
		s.Events.Cancel(uint64(id), EventTrigger)
		return tjs.VoidValue, nil
	case "onFire":
		if len(args) != 0 {
			return tjs.VoidValue, fmt.Errorf("AsyncTrigger.onFire requires exactly 0 arguments")
		}
		return tjs.VoidValue, s.asyncTriggerAction(vm, id, budget)
	default:
		return tjs.VoidValue, fmt.Errorf("unknown AsyncTrigger operation: %s", operation)
	}
}

func (s *Services) asyncTriggerAction(vm *tjs.VM, id tjs.ObjectID, budget *uint64) error {
	if vm == nil || vm.Objects == nil {
		return fmt.Errorf("AsyncTrigger.onFire requires a VM")
	}
	s.mu.RLock()
	action, ok := s.AsyncTriggerActions[id]
	s.mu.RUnlock()
	if !ok {
		return nil
	}
	ownerObject, exists := vm.Objects.Lookup(action.owner.Object)
	if !exists || !ownerObject.IsValid() {
		return nil
	}
	var function tjs.Value
	if action.name == "" {
		function = tjs.ObjectValue(action.owner)
	} else {
		var found bool
		function, found = ownerObject.Get(action.name)
		if !found {
			return nil
		}
	}
	if function.Kind() != tjs.KindObject || function.IsNull() {
		return nil
	}
	event, err := vm.NewDictionary()
	if err != nil {
		return err
	}
	eventObject, ok := vm.Objects.Lookup(event.ObjectRef().Object)
	if !ok {
		return fmt.Errorf("AsyncTrigger.onFire event was not registered")
	}
	if err := eventObject.Set("type", tjs.StringValue("onFire")); err != nil {
		return err
	}
	if err := eventObject.Set("target", tjs.ObjectValue(tjs.NewObjectRef(id))); err != nil {
		return err
	}
	_, err = vm.CallFunction(function, tjs.ObjectValue(action.owner), []tjs.Value{event}, s, budget)
	return err
}
