package tjs

import "errors"

// IsValid reports handle validity without rejecting an already-invalidated
// object. This is intentionally separate from ordinary dispatch validation.
func (v *VM) IsValid(value Value) Value {
	if value.Kind() != KindObject || value.IsNull() {
		return IntegerValue(1)
	}
	id, e := v.handle(value)
	if e != nil {
		return IntegerValue(0)
	}
	o, e := v.obj(id)
	if e != nil || !o.IsValid() {
		return IntegerValue(0)
	}
	return IntegerValue(1)
}

// RegisterFinalizer attaches a host operation invoked once by Invalidate.
func (v *VM) RegisterFinalizer(value Value, operation string) error {
	id, e := v.handle(value)
	if e != nil {
		return e
	}
	if operation == "" {
		return errors.New("empty TJS finalizer")
	}
	m := v.meta[id]
	if m == nil {
		return errors.New("invalid TJS object metadata")
	}
	m.finalizer = operation
	return nil
}

// Invalidate follows tTJSCustomObject::_Finalize: finalization happens before
// the valid bit is cleared, re-entrant invalidation is harmless, and the heap
// entry remains addressable so stale external handles cannot be reused.
func (v *VM) Invalidate(value Value, host Host, budget *uint64) (Value, error) {
	if value.Kind() != KindObject || value.IsNull() {
		return IntegerValue(0), nil
	}
	id, e := v.handle(value)
	if e != nil {
		return VoidValue, e
	}
	o, e := v.obj(id)
	if e != nil {
		return VoidValue, e
	}
	if !o.IsValid() {
		return IntegerValue(0), nil
	}
	m := v.meta[id]
	if m != nil && m.finalizing {
		return IntegerValue(1), nil
	}
	if id == 0 {
		return IntegerValue(0), nil
	}
	if m != nil && (m.scriptFinalizer != 0 || m.finalizer != "") {
		m.finalizing = true
		if host == nil {
			host = nopHost{}
		}
		if budget == nil {
			x := uint64(1 << 62)
			budget = &x
		}
		if m.scriptFinalizer != 0 {
			finalizer := ObjectValue(NewObjectRef(m.scriptFinalizer, id))
			_, e = v.invoke(finalizer, ObjectValue(NewObjectRef(id, id)), nil, host, budget, true)
		}
		if e == nil && m.finalizer != "" {
			_, e = v.nativeCall(m.finalizer, value, nil, host, budget, true)
		}
		m.finalizing = false
		if e != nil {
			return VoidValue, e
		}
	}
	o.Invalidate()
	if m != nil {
		m.array = nil
		m.arrayLength = 0
	}
	return IntegerValue(1), nil
}

func (v *VM) Finalize(value Value, host Host, budget *uint64) (Value, error) {
	return v.Invalidate(value, host, budget)
}
