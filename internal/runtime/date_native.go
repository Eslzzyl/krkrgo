package runtime

import (
	"fmt"

	"github.com/eslzzyl/krkrgo/internal/tjs"
)

func dateObjectContext(context tjs.Value, operation string) (tjs.ObjectID, error) {
	if context.Kind() != tjs.KindObject || context.IsNull() || context.ObjectRef().Object == 0 {
		return 0, fmt.Errorf("Date.%s requires non-null object context", operation)
	}
	return context.ObjectRef().Object, nil
}

func dateFromArgs(args []tjs.Value) *tjs.Date {
	switch len(args) {
	case 0:
		return tjs.NewDate()
	case 1:
		return tjs.NewDate(args[0])
	case 2:
		return tjs.NewDate(args[0], args[1])
	case 3:
		return tjs.NewDate(args[0], args[1], args[2])
	case 4:
		return tjs.NewDate(args[0], args[1], args[2], args[3])
	case 5:
		return tjs.NewDate(args[0], args[1], args[2], args[3], args[4])
	default:
		return tjs.NewDate(args[0], args[1], args[2], args[3], args[4], args[5])
	}
}

func dateIntegerArgument(args []tjs.Value, operation string) (int64, error) {
	if len(args) == 0 {
		return 0, fmt.Errorf("Date.%s requires an argument", operation)
	}
	value, err := args[0].Int()
	if err != nil {
		return 0, fmt.Errorf("Date.%s argument must be an integer: %w", operation, err)
	}
	return value, nil
}

func dateComponentArgument(args []tjs.Value, operation string) (int, error) {
	value, err := dateIntegerArgument(args, operation)
	if err != nil {
		return 0, err
	}
	component := int32(value)
	if int64(component) != value {
		return 0, fmt.Errorf("Date.%s argument exceeds TJS integer range", operation)
	}
	return int(component), nil
}

func (s *Services) dateNative(vm *tjs.VM, context tjs.Value, operation string, args []tjs.Value) (tjs.Value, error) {
	s.ensureInit()
	if operation == "getTimezoneOffset" {
		return tjs.IntegerValue(int64(tjs.DateTimezoneOffset())), nil
	}
	id, err := dateObjectContext(context, operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	switch operation {
	case "@initialize":
		if vm == nil || vm.Objects == nil {
			return tjs.VoidValue, fmt.Errorf("Date.@initialize requires a VM")
		}
		if object, ok := vm.Objects.Lookup(id); !ok || !object.IsValid() {
			return tjs.VoidValue, fmt.Errorf("Date.@initialize: invalid object context")
		}
		s.mu.Lock()
		s.Dates[id] = dateFromArgs(args)
		s.mu.Unlock()
		return tjs.VoidValue, nil
	case "@invalidate", "finalize":
		s.mu.Lock()
		delete(s.Dates, id)
		s.mu.Unlock()
		return tjs.VoidValue, nil
	}

	s.mu.RLock()
	date := s.Dates[id]
	s.mu.RUnlock()
	if date == nil {
		return tjs.VoidValue, fmt.Errorf("Date.%s: context has no Date native instance", operation)
	}
	switch operation {
	case "getDate":
		return tjs.IntegerValue(int64(date.GetDate())), nil
	case "getDay":
		return tjs.IntegerValue(int64(date.GetDay())), nil
	case "getHours":
		return tjs.IntegerValue(int64(date.GetHours())), nil
	case "getMinutes":
		return tjs.IntegerValue(int64(date.GetMinutes())), nil
	case "getMonth":
		return tjs.IntegerValue(int64(date.GetMonth())), nil
	case "getSeconds":
		return tjs.IntegerValue(int64(date.GetSeconds())), nil
	case "getTime":
		return tjs.IntegerValue(date.GetTime()), nil
	case "getYear":
		return tjs.IntegerValue(int64(date.GetYear())), nil
	case "setYear", "setMonth", "setDate", "setHours", "setMinutes", "setSeconds":
		value, err := dateComponentArgument(args, operation)
		if err != nil {
			return tjs.VoidValue, err
		}
		switch operation {
		case "setYear":
			err = date.SetYear(value)
		case "setMonth":
			err = date.SetMonth(value)
		case "setDate":
			err = date.SetDate(value)
		case "setHours":
			err = date.SetHours(value)
		case "setMinutes":
			err = date.SetMinutes(value)
		case "setSeconds":
			err = date.SetSeconds(value)
		}
		return tjs.VoidValue, err
	case "setTime":
		value, err := dateIntegerArgument(args, operation)
		if err != nil {
			return tjs.VoidValue, err
		}
		return tjs.VoidValue, date.SetTime(value)
	case "parse":
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("Date.parse requires an argument")
		}
		return tjs.VoidValue, date.Parse(args[0].Text())
	default:
		return tjs.VoidValue, fmt.Errorf("unknown Date operation: %s", operation)
	}
}
