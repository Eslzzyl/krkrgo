package runtime

import (
	"fmt"
	"sort"
	"strings"

	"github.com/eslzzyl/krkrgo/internal/tjs"
)

func isVarStorage(name string) bool { return strings.HasPrefix(strings.ToLower(name), "var://") }

func (s *Services) varStorageObject(vm *tjs.VM, name string) (tjs.Value, string, error) {
	if vm == nil {
		return tjs.VoidValue, "", fmt.Errorf("varfile storage requires a live TJS VM")
	}
	path := name[len("var://"):]
	if !strings.HasPrefix(path, "./") {
		return tjs.VoidValue, "", fmt.Errorf("varfile storage path %q must use the . domain", name)
	}
	path = strings.TrimPrefix(path, "./")
	parts := strings.Split(path, "/")
	if path == "" {
		parts = nil
	}
	dispatch := tjs.NewDispatcher(vm.Objects)
	base := tjs.ObjectValue(tjs.NewObjectRef(0, 0))
	for index, part := range parts[:max(0, len(parts)-1)] {
		if part == "" {
			return tjs.VoidValue, "", fmt.Errorf("varfile storage path %q contains an empty directory", name)
		}
		var value tjs.Value
		var err error
		if index == 0 {
			value, _ = vm.Globals[part]
			if value.IsVoid() {
				value, err = dispatch.Get(base, part)
			}
		} else {
			value, err = dispatch.Get(base, part)
		}
		if err != nil || value.Kind() != tjs.KindObject || value.IsNull() {
			return tjs.VoidValue, "", fmt.Errorf("varfile storage path %q has no directory %q", name, part)
		}
		base = value
	}
	leaf := ""
	if len(parts) != 0 {
		leaf = parts[len(parts)-1]
	}
	return base, leaf, nil
}

func (s *Services) readVarStorage(vm *tjs.VM, name string) ([]byte, error) {
	parent, leaf, err := s.varStorageObject(vm, name)
	if err != nil {
		return nil, err
	}
	if leaf == "" {
		return nil, fmt.Errorf("varfile storage path %q names a directory", name)
	}
	var value tjs.Value
	if parent.ObjectRef().Object == 0 {
		value, _ = vm.Globals[leaf]
		if value.IsVoid() {
			err = tjs.ErrMemberNotFound
		}
	} else {
		value, err = tjs.NewDispatcher(vm.Objects).Get(parent, leaf)
	}
	if err != nil {
		return nil, fmt.Errorf("varfile storage %q: %w", name, err)
	}
	if value.Kind() != tjs.KindOctet {
		return nil, fmt.Errorf("varfile storage %q is not an Octet", name)
	}
	return value.Octets(), nil
}

func (s *Services) writeVarStorage(vm *tjs.VM, name string, data []byte) error {
	parent, leaf, err := s.varStorageObject(vm, name)
	if err != nil {
		return err
	}
	if leaf == "" {
		return fmt.Errorf("varfile storage path %q names a directory", name)
	}
	if parent.ObjectRef().Object == 0 {
		vm.Globals[leaf] = tjs.OctetValue(data)
		return nil
	}
	if err := tjs.NewDispatcher(vm.Objects).Set(parent, leaf, tjs.OctetValue(data)); err != nil {
		return fmt.Errorf("varfile storage %q write: %w", name, err)
	}
	return nil
}

func (s *Services) listVarStorage(vm *tjs.VM, name string) (tjs.Value, error) {
	parent, leaf, err := s.varStorageObject(vm, name)
	if err != nil {
		return tjs.VoidValue, err
	}
	if leaf != "" {
		if parent.ObjectRef().Object == 0 {
			parent, _ = vm.Globals[leaf]
		} else {
			parent, err = tjs.NewDispatcher(vm.Objects).Get(parent, leaf)
		}
		if err != nil || parent.Kind() != tjs.KindObject || parent.IsNull() {
			return tjs.VoidValue, fmt.Errorf("varfile storage path %q is not a directory", name)
		}
	}
	names := make([]string, 0)
	if parent.ObjectRef().Object == 0 {
		for child, value := range vm.Globals {
			if value.Kind() == tjs.KindOctet {
				names = append(names, child)
			}
		}
	} else {
		o, ok := vm.Objects.Lookup(parent.ObjectRef().Object)
		if !ok {
			return tjs.VoidValue, fmt.Errorf("varfile storage path %q has an invalid directory", name)
		}
		for _, entry := range o.Entries() {
			if entry.Value.Kind() == tjs.KindOctet {
				names = append(names, entry.Name)
			}
		}
	}
	sort.Strings(names)
	values := make([]tjs.Value, len(names))
	for i, child := range names {
		values[i] = tjs.StringValue(child)
	}
	return vm.NewArray(values)
}
