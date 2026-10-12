package runtime

import (
	"fmt"

	"github.com/eslzzyl/krkrgo/internal/assets"
	"github.com/eslzzyl/krkrgo/internal/tjs"
)

func installPSBFileNative(vm *tjs.VM) error {
	if vm == nil {
		return fmt.Errorf("psbfile.dll requires a VM")
	}
	if _, installed := vm.Globals["PSBFile"]; installed {
		return nil
	}
	class, err := vm.RegisterNativeClass("PSBFile")
	if err != nil {
		return err
	}
	for _, method := range []string{"@initialize", "@invalidate", "PSBFile", "finalize", "load"} {
		if err := vm.RegisterNativeMethod(class, method, "PSBFile."+method); err != nil {
			return err
		}
	}
	return vm.RegisterNativeProperty(class, "root", "PSBFile.get:root", "")
}

func (s *Services) psbFileNative(vm *tjs.VM, context tjs.Value, operation string, args []tjs.Value) (tjs.Value, error) {
	switch operation {
	case "@initialize", "@invalidate", "finalize":
		return tjs.VoidValue, nil
	case "PSBFile", "load":
		if len(args) == 0 {
			if operation == "PSBFile" {
				return tjs.VoidValue, nil
			}
			return tjs.VoidValue, fmt.Errorf("PSBFile.load: missing argument 0")
		}
		root, err := s.loadPSBRoot(vm, args[0].Text())
		if err != nil {
			return tjs.IntegerValue(0), nil
		}
		if err := tjs.SetMember(vm.Objects, context, "_psbRoot", root); err != nil {
			return tjs.VoidValue, err
		}
		return tjs.IntegerValue(1), nil
	case "get:root":
		if !tjs.HasMember(vm.Objects, context, "_psbRoot") {
			return vm.NewDictionary()
		}
		return tjs.GetMember(vm.Objects, context, "_psbRoot")
	default:
		return tjs.VoidValue, fmt.Errorf("unsupported PSBFile operation %q", operation)
	}
}

func (s *Services) loadPSBRoot(vm *tjs.VM, storage string) (tjs.Value, error) {
	data, err := s.readStorage(storage)
	if err != nil {
		return tjs.VoidValue, err
	}
	doc, err := assets.ParsePSB(data)
	if err != nil {
		return tjs.VoidValue, err
	}
	return psbValueToTJS(vm, doc.Root, data, 0)
}

func psbValueToTJS(vm *tjs.VM, value assets.Value, data []byte, depth int) (tjs.Value, error) {
	if depth > 128 {
		return tjs.VoidValue, fmt.Errorf("PSB value nesting exceeds limit")
	}
	switch v := value.(type) {
	case nil:
		return tjs.VoidValue, nil
	case bool:
		if v {
			return tjs.IntegerValue(1), nil
		}
		return tjs.IntegerValue(0), nil
	case int64:
		return tjs.IntegerValue(v), nil
	case float64:
		return tjs.RealValue(v), nil
	case string:
		return tjs.StringValue(v), nil
	case []assets.Value:
		items := make([]tjs.Value, len(v))
		for i, item := range v {
			converted, err := psbValueToTJS(vm, item, data, depth+1)
			if err != nil {
				return tjs.VoidValue, err
			}
			items[i] = converted
		}
		return vm.NewArray(items)
	case map[string]assets.Value:
		object, err := vm.NewDictionary()
		if err != nil {
			return tjs.VoidValue, err
		}
		for name, item := range v {
			converted, err := psbValueToTJS(vm, item, data, depth+1)
			if err != nil {
				return tjs.VoidValue, err
			}
			if err := tjs.SetMember(vm.Objects, object, name, converted); err != nil {
				return tjs.VoidValue, err
			}
		}
		return object, nil
	case assets.Resource:
		if v.Offset < 0 || v.Length < 0 || v.Offset > len(data) || v.Length > len(data)-v.Offset {
			return tjs.VoidValue, fmt.Errorf("PSB resource range outside file")
		}
		return tjs.OctetValue(data[v.Offset : v.Offset+v.Length]), nil
	default:
		return tjs.VoidValue, fmt.Errorf("unsupported PSB value type %T", value)
	}
}
