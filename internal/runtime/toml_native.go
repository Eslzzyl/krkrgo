package runtime

import (
	"fmt"
	"sort"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/eslzzyl/krkrgo/internal/assets"
	"github.com/eslzzyl/krkrgo/internal/tjs"
)

func installTOMLNative(vm *tjs.VM) error {
	if vm == nil {
		return fmt.Errorf("nil TJS VM")
	}
	scripts, ok := vm.Globals["Scripts"]
	if !ok {
		return fmt.Errorf("Scripts class is unavailable")
	}
	for _, method := range []string{"tomlDecodeFromStorage", "tomlDecodeFromString", "tomlEncode"} {
		if _, exists, err := vm.GetMemberRawIfPresent(scripts, method, nil, nil); err != nil {
			return err
		} else if exists {
			continue
		}
		if err := vm.RegisterNativeStaticMethod(scripts, method, "Scripts."+method); err != nil {
			return err
		}
	}
	return nil
}

func (s *Services) tomlDecodeFromStorage(vm *tjs.VM, storageName string, budget *uint64) (tjs.Value, error) {
	data, err := s.readStorage(storageName)
	if err != nil {
		return tjs.VoidValue, fmt.Errorf("Scripts.tomlDecodeFromStorage %q: %w", storageName, err)
	}
	text, err := assets.Decode(data)
	if err != nil {
		return tjs.VoidValue, fmt.Errorf("Scripts.tomlDecodeFromStorage %q: %w", storageName, err)
	}
	return tomlDecodeText(vm, s, budget, text, fmt.Sprintf("Scripts.tomlDecodeFromStorage %q", storageName))
}

func tomlDecodeFromString(vm *tjs.VM, host tjs.Host, text string, budget *uint64) (tjs.Value, error) {
	return tomlDecodeText(vm, host, budget, text, "Scripts.tomlDecodeFromString")
}

func tomlDecodeText(vm *tjs.VM, host tjs.Host, budget *uint64, text, context string) (tjs.Value, error) {
	var document map[string]any
	if _, err := toml.Decode(text, &document); err != nil {
		return tjs.VoidValue, fmt.Errorf("%s: %w", context, err)
	}
	value, err := tomlValueToTJS(vm, host, budget, document)
	if err != nil {
		return tjs.VoidValue, fmt.Errorf("%s: %w", context, err)
	}
	return value, nil
}

func tomlEncodeTJS(vm *tjs.VM, host tjs.Host, budget *uint64, value tjs.Value) (tjs.Value, error) {
	converted, err := tomlValueFromTJS(vm, host, budget, value, make(map[tjs.ObjectID]struct{}))
	if err != nil {
		return tjs.VoidValue, fmt.Errorf("Scripts.tomlEncode: %w", err)
	}
	document, ok := converted.(map[string]any)
	if !ok {
		return tjs.VoidValue, fmt.Errorf("Scripts.tomlEncode requires a Dictionary")
	}
	data, err := toml.Marshal(document)
	if err != nil {
		return tjs.VoidValue, fmt.Errorf("Scripts.tomlEncode: %w", err)
	}
	return tjs.StringValue(string(data)), nil
}

func tomlValueToTJS(vm *tjs.VM, host tjs.Host, budget *uint64, value any) (tjs.Value, error) {
	switch value := value.(type) {
	case map[string]any:
		result, err := vm.NewDictionary()
		if err != nil {
			return tjs.VoidValue, err
		}
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			member, err := tomlValueToTJS(vm, host, budget, value[key])
			if err != nil {
				return tjs.VoidValue, fmt.Errorf("TOML table member %q: %w", key, err)
			}
			if err := vm.SetMemberWithRuntime(result, key, member, tjs.MemberEnsure|tjs.IgnoreProperty, host, budget); err != nil {
				return tjs.VoidValue, fmt.Errorf("TOML table member %q: %w", key, err)
			}
		}
		return result, nil
	case []any:
		items := make([]tjs.Value, len(value))
		for index, item := range value {
			converted, err := tomlValueToTJS(vm, host, budget, item)
			if err != nil {
				return tjs.VoidValue, fmt.Errorf("TOML array element %d: %w", index, err)
			}
			items[index] = converted
		}
		return vm.NewArray(items)
	case []map[string]any:
		items := make([]tjs.Value, len(value))
		for index, item := range value {
			converted, err := tomlValueToTJS(vm, host, budget, item)
			if err != nil {
				return tjs.VoidValue, fmt.Errorf("TOML table array element %d: %w", index, err)
			}
			items[index] = converted
		}
		return vm.NewArray(items)
	case string:
		return tjs.StringValue(value), nil
	case int64:
		return tjs.IntegerValue(value), nil
	case float64:
		return tjs.RealValue(value), nil
	case bool:
		return tjs.IntegerValue(boolInt(value)), nil
	case time.Time:
		return tjs.StringValue(value.Format(time.RFC3339Nano)), nil
	default:
		return tjs.VoidValue, fmt.Errorf("unsupported TOML value type %T", value)
	}
}

func tomlValueFromTJS(vm *tjs.VM, host tjs.Host, budget *uint64, value tjs.Value, active map[tjs.ObjectID]struct{}) (any, error) {
	switch value.Kind() {
	case tjs.KindInteger:
		return value.Int()
	case tjs.KindReal:
		return value.RealValue(), nil
	case tjs.KindString:
		return value.Text(), nil
	case tjs.KindObject:
		if value.IsNull() {
			return nil, fmt.Errorf("TOML does not support null")
		}
		id := value.ObjectRef().Object
		if _, exists := active[id]; exists {
			return nil, fmt.Errorf("TOML does not support cyclic objects")
		}
		object, ok := vm.Objects.Lookup(id)
		if !ok {
			return nil, fmt.Errorf("invalid TJS object %d", id)
		}
		active[id] = struct{}{}
		defer delete(active, id)
		switch object.Category {
		case tjs.CategoryArray:
			items, err := vm.ArrayItems(value)
			if err != nil {
				return nil, err
			}
			converted := make([]any, len(items))
			for index, item := range items {
				converted[index], err = tomlValueFromTJS(vm, host, budget, item, active)
				if err != nil {
					return nil, fmt.Errorf("TOML array element %d: %w", index, err)
				}
			}
			return converted, nil
		case tjs.CategoryDictionary:
			entries := object.Entries()
			converted := make(map[string]any, len(entries))
			for _, entry := range entries {
				member, err := vm.GetMember(value, entry.Name, host, budget)
				if err != nil {
					return nil, fmt.Errorf("TOML table member %q: %w", entry.Name, err)
				}
				converted[entry.Name], err = tomlValueFromTJS(vm, host, budget, member, active)
				if err != nil {
					return nil, fmt.Errorf("TOML table member %q: %w", entry.Name, err)
				}
			}
			return converted, nil
		default:
			return nil, fmt.Errorf("TOML value requires a primitive, Array, or Dictionary; got %v", object.Category)
		}
	default:
		return nil, fmt.Errorf("TOML does not support TJS value kind %v", value.Kind())
	}
}
