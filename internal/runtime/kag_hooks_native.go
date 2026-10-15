package runtime

import (
	"fmt"

	"github.com/eslzzyl/krkrgo/internal/tjs"
)

type kagHookRegistration struct {
	vm      *tjs.VM
	handle  tjs.Value
	fn      tjs.Value
	context tjs.Value
}

func (s *Services) installLegacyKAGHookNative(vm *tjs.VM, budget *uint64) error {
	if s == nil || vm == nil {
		return nil
	}
	s.kagHookMu.Lock()
	if s.kagHookNatives == nil {
		s.kagHookNatives = make(map[*tjs.VM]tjs.Value)
	}
	native, ok := s.kagHookNatives[vm]
	if !ok {
		if err := vm.RegisterNative("addKagHookCallback"); err != nil {
			s.kagHookMu.Unlock()
			return err
		}
		native = vm.Globals["addKagHookCallback"]
		s.kagHookNatives[vm] = native
	}
	vm.Globals["addKagHookCallback"] = native
	s.kagHookMu.Unlock()

	global, ok := vm.Objects.Lookup(0)
	if !ok {
		return fmt.Errorf("install addKagHookCallback: TJS global object is unavailable")
	}
	if err := global.Set("addKagHookCallback", native); err != nil {
		return fmt.Errorf("install addKagHookCallback: %w", err)
	}
	return s.flushLegacyKAGHooks(vm, budget)
}

func (s *Services) registerLegacyKAGHook(vm *tjs.VM, args []tjs.Value, budget *uint64) error {
	registration := kagHookRegistration{vm: vm}
	if len(args) > 0 {
		registration.handle = args[0]
	}
	if len(args) > 1 {
		registration.fn = args[1]
	}
	if len(args) > 2 {
		registration.context = args[2]
	}

	global := tjs.ObjectValue(tjs.NewObjectRef(0, 1))
	entries, found, err := vm.GetMemberIfPresent(global, "kagHookEntries", s, budget)
	if err != nil {
		return err
	}
	if found && !entries.IsVoid() {
		entry, err := vm.NewDictionary()
		if err != nil {
			return err
		}
		object, ok := vm.Objects.Lookup(entry.ObjectRef().Object)
		if !ok {
			return fmt.Errorf("KAG hook entry object is unavailable")
		}
		if err := object.Set("handle", registration.handle); err != nil {
			return err
		}
		if err := object.Set("func", registration.fn); err != nil {
			return err
		}
		if err := object.Set("kagcontext", registration.context); err != nil {
			return err
		}
		return vm.AppendArray(entries, entry)
	}
	kag, found, err := vm.GetMemberIfPresent(global, "kag", s, budget)
	if err != nil {
		return err
	}
	if found && kag.Kind() == tjs.KindObject && !kag.IsNull() {
		if err := s.flushLegacyKAGHooks(vm, budget); err != nil {
			return err
		}
		return s.installLegacyKAGHookOnWindow(vm, kag, registration, budget)
	}

	s.kagHookMu.Lock()
	if s.pendingKAGHooks == nil {
		s.pendingKAGHooks = make(map[*tjs.VM][]kagHookRegistration)
	}
	s.pendingKAGHooks[vm] = append(s.pendingKAGHooks[vm], registration)
	s.kagHookMu.Unlock()
	return nil
}

func (s *Services) flushLegacyKAGHooks(vm *tjs.VM, budget *uint64) error {
	global := tjs.ObjectValue(tjs.NewObjectRef(0, 1))
	kag, found, err := vm.GetMemberIfPresent(global, "kag", s, budget)
	if err != nil {
		return err
	}
	if !found || kag.Kind() != tjs.KindObject || kag.IsNull() {
		return nil
	}

	s.kagHookMu.Lock()
	pending := append([]kagHookRegistration(nil), s.pendingKAGHooks[vm]...)
	delete(s.pendingKAGHooks, vm)
	s.kagHookMu.Unlock()
	for i, registration := range pending {
		if err := s.installLegacyKAGHookOnWindow(vm, kag, registration, budget); err != nil {
			s.kagHookMu.Lock()
			s.pendingKAGHooks[vm] = append(pending[i:], s.pendingKAGHooks[vm]...)
			s.kagHookMu.Unlock()
			return err
		}
	}
	return nil
}

func (s *Services) installLegacyKAGHookOnWindow(vm *tjs.VM, kag tjs.Value, registration kagHookRegistration, budget *uint64) error {
	global := tjs.ObjectValue(tjs.NewObjectRef(0, 1))
	factory, found, err := vm.GetMemberIfPresent(global, "getInitCallbackFunction", s, budget)
	if err != nil {
		return err
	}
	if !found || factory.IsVoid() {
		return fmt.Errorf("register KAG hook %q: getInitCallbackFunction is unavailable", registration.handle.Text())
	}
	callback, err := vm.CallFunction(factory, global, []tjs.Value{registration.fn, registration.context, kag}, s, budget)
	if err != nil {
		return fmt.Errorf("bind KAG hook %q: %w", registration.handle.Text(), err)
	}
	addHook, err := vm.GetMember(kag, "addHook", s, budget)
	if err != nil {
		return fmt.Errorf("register KAG hook %q: %w", registration.handle.Text(), err)
	}
	if _, err := vm.CallFunction(addHook, kag, []tjs.Value{registration.handle, callback}, s, budget); err != nil {
		return fmt.Errorf("register KAG hook %q: %w", registration.handle.Text(), err)
	}
	return nil
}
