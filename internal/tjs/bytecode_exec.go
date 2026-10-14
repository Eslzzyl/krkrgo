package tjs

import (
	"errors"
	"fmt"
)

// ExecuteLegacy runs the real TJS2 intercode subset currently needed by
// startup. Register operands are relative to the VM register base: -1/-2
// address the this context/proxy, and argument registers start at -3.
func (d *DecodedBytecode) ExecuteLegacy(object int, global Value, tables ...*ObjectTable) (Value, error) {
	return d.ExecuteLegacyWithEval(object, global, nil, tables...)
}

// ExecuteLegacyWithEval runs legacy intercode with an optional evaluator for
// the global eval function. The evaluator receives source, this-context, and
// whether the call's result is used.
func (d *DecodedBytecode) ExecuteLegacyWithEval(object int, global Value, evaluate func(string, Value, bool) (Value, error), tables ...*ObjectTable) (Value, error) {
	return d.ExecuteLegacyWithRuntime(object, global, evaluate, nil, tables...)
}

// ExecuteLegacyWithRuntime also routes callable values through the owning VM,
// which is required for native and script callables held in VM metadata.
func (d *DecodedBytecode) ExecuteLegacyWithRuntime(object int, global Value, evaluate func(string, Value, bool) (Value, error), invoke func(Value, Value, []Value, bool) (Value, error), tables ...*ObjectTable) (Value, error) {
	return d.executeLegacyWithRuntime(object, -1, global, global, nil, evaluate, invoke, nil, nil, nil, nil, nil, nil, nil, tables...)
}

// ExecuteLegacyWithConstruction routes NEW through the VM's class-construction lifecycle.
func (d *DecodedBytecode) ExecuteLegacyWithConstruction(object int, global Value, evaluate func(string, Value, bool) (Value, error), invoke func(Value, Value, []Value, bool) (Value, error), construct func(Value, []Value) (Value, error), tables ...*ObjectTable) (Value, error) {
	return d.ExecuteLegacyWithContextAndConstruction(object, global, global, evaluate, invoke, construct, tables...)
}

// ExecuteLegacyWithContextAndConstruction executes a compiled script with
// separate global and this-context objects, matching the TJS top-level frame.
func (d *DecodedBytecode) ExecuteLegacyWithContextAndConstruction(object int, global, thisContext Value, evaluate func(string, Value, bool) (Value, error), invoke func(Value, Value, []Value, bool) (Value, error), construct func(Value, []Value) (Value, error), tables ...*ObjectTable) (Value, error) {
	return d.ExecuteLegacyWithContextAndConstructionAndPropertyResolver(object, global, thisContext, evaluate, invoke, construct, nil, tables...)
}

// ExecuteLegacyWithContextAndConstructionAndPropertyResolver delegates member
// reads to the owning VM before using legacy dispatch for non-VM properties.
func (d *DecodedBytecode) ExecuteLegacyWithContextAndConstructionAndPropertyResolver(object int, global, thisContext Value, evaluate func(string, Value, bool) (Value, error), invoke func(Value, Value, []Value, bool) (Value, error), construct func(Value, []Value) (Value, error), resolve func(Value, string, uint32) (Value, bool, error), tables ...*ObjectTable) (Value, error) {
	return d.executeLegacyWithRuntime(object, -1, global, thisContext, nil, evaluate, invoke, construct, resolve, nil, nil, nil, nil, nil, tables...)
}

// ExecuteLegacyWithContextAndConstructionAndArguments executes a script function
// with the supplied positional arguments in its legacy argument registers.
func (d *DecodedBytecode) ExecuteLegacyWithContextAndConstructionAndArguments(object int, global, thisContext Value, args []Value, evaluate func(string, Value, bool) (Value, error), invoke func(Value, Value, []Value, bool) (Value, error), construct func(Value, []Value) (Value, error), tables ...*ObjectTable) (Value, error) {
	return d.ExecuteLegacyWithContextAndConstructionAndArgumentsAndPropertyResolver(object, global, thisContext, args, evaluate, invoke, construct, nil, tables...)
}

// ExecuteLegacyWithContextAndConstructionAndArgumentsAndPropertyResolver executes
// a script function with VM-aware property reads.
func (d *DecodedBytecode) ExecuteLegacyWithContextAndConstructionAndArgumentsAndPropertyResolver(object int, global, thisContext Value, args []Value, evaluate func(string, Value, bool) (Value, error), invoke func(Value, Value, []Value, bool) (Value, error), construct func(Value, []Value) (Value, error), resolve func(Value, string, uint32) (Value, bool, error), tables ...*ObjectTable) (Value, error) {
	return d.executeLegacyWithRuntime(object, -1, global, thisContext, args, evaluate, invoke, construct, resolve, nil, nil, nil, nil, nil, tables...)
}

// ExecuteLegacyAtOffset executes one deferred expression at a decoded code-unit
// boundary while preserving its parent object's constants and register layout.
func (d *DecodedBytecode) ExecuteLegacyAtOffset(object, startOffset int, global, thisContext Value, evaluate func(string, Value, bool) (Value, error), invoke func(Value, Value, []Value, bool) (Value, error), construct func(Value, []Value) (Value, error), resolve func(Value, string, uint32) (Value, bool, error), tables ...*ObjectTable) (Value, error) {
	return d.executeLegacyWithRuntime(object, startOffset, global, thisContext, nil, evaluate, invoke, construct, resolve, nil, nil, nil, nil, nil, tables...)
}

// ExecuteLegacyWithContextAndConstructionAndMemberResolvers routes member
// reads and writes through the owning VM while retaining the legacy dispatcher
// for operations the VM does not implement.
func (d *DecodedBytecode) ExecuteLegacyWithContextAndConstructionAndMemberResolvers(object int, global, thisContext Value, evaluate func(string, Value, bool) (Value, error), invoke func(Value, Value, []Value, bool) (Value, error), construct func(Value, []Value) (Value, error), resolve func(Value, string, uint32) (Value, bool, error), set func(Value, string, Value, uint32) error, tables ...*ObjectTable) (Value, error) {
	return d.executeLegacyWithRuntime(object, -1, global, thisContext, nil, evaluate, invoke, construct, resolve, set, nil, nil, nil, nil, tables...)
}

// ExecuteLegacyWithContextAndConstructionAndArgumentsAndMemberResolvers is
// the argument-preserving counterpart of ExecuteLegacyWithContextAndConstructionAndMemberResolvers.
func (d *DecodedBytecode) ExecuteLegacyWithContextAndConstructionAndArgumentsAndMemberResolvers(object int, global, thisContext Value, args []Value, evaluate func(string, Value, bool) (Value, error), invoke func(Value, Value, []Value, bool) (Value, error), construct func(Value, []Value) (Value, error), resolve func(Value, string, uint32) (Value, bool, error), set func(Value, string, Value, uint32) error, tables ...*ObjectTable) (Value, error) {
	return d.executeLegacyWithRuntime(object, -1, global, thisContext, args, evaluate, invoke, construct, resolve, set, nil, nil, nil, nil, tables...)
}

// ExecuteLegacyAtOffsetAndMemberResolvers executes a deferred expression at
// an instruction boundary with VM-aware member reads and writes.
func (d *DecodedBytecode) ExecuteLegacyAtOffsetAndMemberResolvers(object, startOffset int, global, thisContext Value, evaluate func(string, Value, bool) (Value, error), invoke func(Value, Value, []Value, bool) (Value, error), construct func(Value, []Value) (Value, error), resolve func(Value, string, uint32) (Value, bool, error), set func(Value, string, Value, uint32) error, tables ...*ObjectTable) (Value, error) {
	return d.executeLegacyWithRuntime(object, startOffset, global, thisContext, nil, evaluate, invoke, construct, resolve, set, nil, nil, nil, nil, tables...)
}

// ExecuteLegacyAtOffsetAndVMOps is the VM-hook-aware counterpart of
// ExecuteLegacyAtOffsetAndMemberResolvers: deferred superclass getter
// expressions reach CHKINS, INV and CHKINV through the owning VM as well.
func (d *DecodedBytecode) ExecuteLegacyAtOffsetAndVMOps(object, startOffset int, global, thisContext Value, evaluate func(string, Value, bool) (Value, error), invoke func(Value, Value, []Value, bool) (Value, error), construct func(Value, []Value) (Value, error), resolve func(Value, string, uint32) (Value, bool, error), set func(Value, string, Value, uint32) error, instanceOf func(Value, Value) (Value, error), invalidate func(Value) (Value, error), isValid func(Value) Value, tables ...*ObjectTable) (Value, error) {
	return d.executeLegacyWithRuntime(object, startOffset, global, thisContext, nil, evaluate, invoke, construct, resolve, set, instanceOf, invalidate, isValid, nil, tables...)
}

// ExecuteLegacyWithContextAndConstructionAndMemberResolversAndVMOps is the
// VM-hook-aware counterpart of
// ExecuteLegacyWithContextAndConstructionAndMemberResolvers: instanceOf backs
// CHKINS, invalidate backs INV and isValid backs CHKINV, so the executor can
// reach the class-membership and finalization state the shared object table
// alone cannot express.
func (d *DecodedBytecode) ExecuteLegacyWithContextAndConstructionAndMemberResolversAndVMOps(object int, global, thisContext Value, evaluate func(string, Value, bool) (Value, error), invoke func(Value, Value, []Value, bool) (Value, error), construct func(Value, []Value) (Value, error), resolve func(Value, string, uint32) (Value, bool, error), set func(Value, string, Value, uint32) error, instanceOf func(Value, Value) (Value, error), invalidate func(Value) (Value, error), isValid func(Value) Value, tables ...*ObjectTable) (Value, error) {
	return d.executeLegacyWithRuntime(object, -1, global, thisContext, nil, evaluate, invoke, construct, resolve, set, instanceOf, invalidate, isValid, nil, tables...)
}

// ExecuteLegacyWithContextAndConstructionAndArgumentsAndMemberResolversAndVMOps
// is the argument-preserving counterpart of
// ExecuteLegacyWithContextAndConstructionAndMemberResolversAndVMOps.
func (d *DecodedBytecode) ExecuteLegacyWithContextAndConstructionAndArgumentsAndMemberResolversAndVMOps(object int, global, thisContext Value, args []Value, evaluate func(string, Value, bool) (Value, error), invoke func(Value, Value, []Value, bool) (Value, error), construct func(Value, []Value) (Value, error), resolve func(Value, string, uint32) (Value, bool, error), set func(Value, string, Value, uint32) error, instanceOf func(Value, Value) (Value, error), invalidate func(Value) (Value, error), isValid func(Value) Value, tables ...*ObjectTable) (Value, error) {
	return d.executeLegacyWithRuntime(object, -1, global, thisContext, args, evaluate, invoke, construct, resolve, set, instanceOf, invalidate, isValid, nil, tables...)
}

// ExecuteLegacyAtOffsetAndVMOpsWithRoots is the root-aware deferred-expression
// entry point used by a VM that owns the legacy frame.
func (d *DecodedBytecode) ExecuteLegacyAtOffsetAndVMOpsWithRoots(object, startOffset int, global, thisContext Value, evaluate func(string, Value, bool) (Value, error), invoke func(Value, Value, []Value, bool) (Value, error), construct func(Value, []Value) (Value, error), resolve func(Value, string, uint32) (Value, bool, error), set func(Value, string, Value, uint32) error, instanceOf func(Value, Value) (Value, error), invalidate func(Value) (Value, error), isValid func(Value) Value, roots LegacyFrameRoots, tables ...*ObjectTable) (Value, error) {
	return d.executeLegacyWithRuntime(object, startOffset, global, thisContext, nil, evaluate, invoke, construct, resolve, set, instanceOf, invalidate, isValid, roots, tables...)
}

// ExecuteLegacyWithContextAndConstructionAndMemberResolversAndVMOpsWithRoots
// publishes the legacy register file for nested garbage collection.
func (d *DecodedBytecode) ExecuteLegacyWithContextAndConstructionAndMemberResolversAndVMOpsWithRoots(object int, global, thisContext Value, evaluate func(string, Value, bool) (Value, error), invoke func(Value, Value, []Value, bool) (Value, error), construct func(Value, []Value) (Value, error), resolve func(Value, string, uint32) (Value, bool, error), set func(Value, string, Value, uint32) error, instanceOf func(Value, Value) (Value, error), invalidate func(Value) (Value, error), isValid func(Value) Value, roots LegacyFrameRoots, tables ...*ObjectTable) (Value, error) {
	return d.executeLegacyWithRuntime(object, -1, global, thisContext, nil, evaluate, invoke, construct, resolve, set, instanceOf, invalidate, isValid, roots, tables...)
}

// ExecuteLegacyWithContextAndConstructionAndArgumentsAndMemberResolversAndVMOpsWithRoots
// is the argument-preserving root-aware legacy entry point.
func (d *DecodedBytecode) ExecuteLegacyWithContextAndConstructionAndArgumentsAndMemberResolversAndVMOpsWithRoots(object int, global, thisContext Value, args []Value, evaluate func(string, Value, bool) (Value, error), invoke func(Value, Value, []Value, bool) (Value, error), construct func(Value, []Value) (Value, error), resolve func(Value, string, uint32) (Value, bool, error), set func(Value, string, Value, uint32) error, instanceOf func(Value, Value) (Value, error), invalidate func(Value) (Value, error), isValid func(Value) Value, roots LegacyFrameRoots, tables ...*ObjectTable) (Value, error) {
	return d.executeLegacyWithRuntime(object, -1, global, thisContext, args, evaluate, invoke, construct, resolve, set, instanceOf, invalidate, isValid, roots, tables...)
}

func (d *DecodedBytecode) executeLegacyWithRuntime(object int, startOffset int, global, thisContext Value, args []Value, evaluate func(string, Value, bool) (Value, error), invoke func(Value, Value, []Value, bool) (Value, error), construct func(Value, []Value) (Value, error), resolve func(Value, string, uint32) (Value, bool, error), set func(Value, string, Value, uint32) error, instanceOf func(Value, Value) (Value, error), invalidate func(Value) (Value, error), isValid func(Value) Value, roots LegacyFrameRoots, tables ...*ObjectTable) (Value, error) {
	var table *ObjectTable
	if len(tables) != 0 {
		table = tables[0]
	}
	dispatcher := NewDispatcher(table)
	if d == nil || object < 0 || object >= len(d.Objects) {
		return VoidValue, fmt.Errorf("legacy intercode object %d: index out of range", object)
	}
	o := &d.Objects[object]
	instructions, err := d.DecodeLegacyInstructions(object)
	if err != nil {
		return VoidValue, err
	}
	offsetToIP := make(map[int]int, len(instructions)+1)
	for i, instruction := range instructions {
		offsetToIP[instruction.Offset] = i
	}
	offsetToIP[len(o.Code)] = len(instructions)
	startIP := 0
	if startOffset >= 0 {
		var ok bool
		startIP, ok = offsetToIP[startOffset]
		if !ok || startIP >= len(instructions) {
			return VoidValue, fmt.Errorf("legacy intercode object %d: code offset %d is not an instruction", object, startOffset)
		}
	}
	// -1 is the actual objthis receiver. -2 is the object proxy used by the
	// legacy VM: member access checks objthis first and falls back to global.
	argumentSlots := int(o.ArgumentCount)
	if argumentSlots < 1 {
		argumentSlots = 1
	}
	registerBase := argumentSlots + 2
	maxArgumentSlot := argumentSlots + int(o.MaxVariables) + int(o.VariableReserve) + 2
	for _, instruction := range instructions {
		for _, operand := range instruction.Operands {
			slot := -int(operand)
			if slot >= 3 && slot <= maxArgumentSlot && slot > registerBase {
				registerBase = slot
			}
		}
	}
	regs := make([]Value, registerBase+int(o.MaxVariables)+int(o.VariableReserve)+int(o.MaxFrames)+16)
	for i := range registerBase - 2 {
		regs[i] = VoidValue
	}
	for i, arg := range args {
		if i >= argumentSlots {
			break
		}
		regs[registerBase-3-i] = arg
	}
	if thisContext.Kind() != KindObject || thisContext.IsNull() {
		thisContext = global
	}
	regs[registerBase-1] = thisContext
	regs[registerBase-2] = global
	if roots != nil {
		if pop := roots(regs, thisContext); pop != nil {
			defer pop()
		}
	}
	flag := false
	returnValue := VoidValue
	reg := func(addr int16, pc int) (int, error) {
		index := registerBase + int(addr)
		if index < 0 || index >= len(regs) {
			return 0, fmt.Errorf("legacy intercode object %d code offset %d: register index %d out of range", object, pc, int(addr))
		}
		return index, nil
	}
	propertyTarget := func(value Value, pc int) error {
		if value.Kind() == KindString {
			return nil
		}
		if value.Kind() != KindObject || value.IsNull() {
			return fmt.Errorf("legacy intercode object %d code offset %d: property target is %s", object, pc, value.TypeName())
		}
		if table == nil {
			return fmt.Errorf("legacy intercode object %d code offset %d: property access requires an object table", object, pc)
		}
		if _, ok := table.Lookup(value.ObjectRef().Object); !ok {
			return fmt.Errorf("legacy intercode object %d code offset %d: invalid object handle %d", object, pc, value.ObjectRef().Object)
		}
		return nil
	}
	memberName := func(value Value, pc int) (string, error) {
		if value.Kind() == KindVoid || value.Kind() == KindOctet {
			return "", fmt.Errorf("legacy intercode object %d code offset %d: invalid property name type %s", object, pc, value.TypeName())
		}
		return value.Text(), nil
	}
	directName := func(index int16, pc int) (string, error) {
		ci := int(index)
		if ci < 0 || ci >= len(o.Constants) {
			return "", fmt.Errorf("legacy intercode object %d code offset %d: property constant index %d out of range", object, pc, ci)
		}
		if o.Constants[ci].Value.Kind() != KindString {
			return "", fmt.Errorf("legacy intercode object %d code offset %d: property constant %d is %s, not a string", object, pc, ci, o.Constants[ci].Value.TypeName())
		}
		return o.Constants[ci].Value.Text(), nil
	}
	read := func(target Value, name string, flags uint32) (Value, error) {
		if resolve != nil && target.Kind() == KindObject && !target.IsNull() {
			if value, found, err := resolve(target, name, flags); err != nil || found {
				return value, err
			}
		}
		return dispatcher.Get(target, name, flags)
	}
	readValue := func(target int, name string, flags uint32, pc int) (Value, error) {
		if err := propertyTarget(regs[target], pc); err != nil {
			return VoidValue, err
		}
		value := VoidValue
		var err error
		if target == registerBase-2 {
			value, err = read(thisContext, name, flags|MemberMustExist)
			if errors.Is(err, ErrMemberNotFound) {
				value, err = read(global, name, flags)
			}
		} else {
			value, err = read(regs[target], name, flags)
		}
		if err != nil {
			return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: get property %q: %w", object, pc, name, err)
		}
		return value, nil
	}
	readProperty := func(dst, target int, name string, flags uint32, pc int) error {
		value, err := readValue(target, name, flags, pc)
		if err != nil {
			return err
		}
		regs[dst] = value
		return nil
	}
	writeProperty := func(target int, name string, value Value, flags uint32, pc int) error {
		if err := propertyTarget(regs[target], pc); err != nil {
			return err
		}
		if set != nil {
			err = set(regs[target], name, value, flags)
		} else {
			err = dispatcher.Set(regs[target], name, value, flags)
		}
		if err != nil {
			return fmt.Errorf("legacy intercode object %d code offset %d: set property %q: %w", object, pc, name, err)
		}
		return nil
	}
	// registeredProperty reports whether value is an accessor descriptor owned
	// by the shared dispatcher registry, as opposed to a VM-owned property
	// value. Both kinds appear as raw members in registers, but only the
	// registry form runs its getter and setter programs through the dispatcher.
	registeredProperty := func(value Value) bool {
		if value.Kind() != KindObject || value.IsNull() {
			return false
		}
		registry.mu.RLock()
		_, ok := registry.props[value.ObjectRef().Object]
		registry.mu.RUnlock()
		return ok
	}
	// propertyClosure validates that a register holds a property closure -- the
	// accessor value the unary '*' operator yields. VM_GETP/VM_SETP read and
	// write such a value with a null member name; the reference VM fails every
	// other dispatch type with TJS_E_INVALIDTYPE, so plain data members are
	// rejected here as well.
	propertyClosure := func(value Value, pc int, op string) error {
		if registeredProperty(value) {
			return nil
		}
		if value.Kind() == KindObject && !value.IsNull() && table != nil {
			if o, ok := table.Lookup(value.ObjectRef().Object); ok && o.Category == CategoryProperty {
				return nil
			}
		}
		return fmt.Errorf("legacy intercode object %d code offset %d: %s property is %s, not a property closure", object, pc, op, value.TypeName())
	}
	// propertyReceiver is the object a property closure dispatches against: the
	// closure's own objthis when it carries one, and the current frame's this
	// context otherwise, exactly like the reference VM's
	// `clo.ObjThis ? clo.ObjThis : ra[-1]`.
	propertyReceiver := func(property Value) Value {
		if property.Kind() == KindObject && !property.IsNull() && property.ObjectRef().Context != 0 {
			context := property.ObjectRef().Context
			return ObjectValue(NewObjectRef(context, context))
		}
		return regs[registerBase-1]
	}
	// dereferenceProperty implements VM_GETP, the reference VM's
	// PropGet(0, nullptr, ...): read the value a property closure stands for.
	// Dispatcher-registered descriptors run their getter program directly; a
	// VM-owned property value dispatches through the owning VM, which is how a
	// compiled property context runs its getter with the receiver as its
	// this-context.
	dereferenceProperty := func(property, receiver Value, pc int) (Value, error) {
		if err := propertyClosure(property, pc, "GETP"); err != nil {
			return VoidValue, err
		}
		if registeredProperty(property) {
			context := ObjectRef{}
			if receiver.Kind() == KindObject && !receiver.IsNull() {
				context = receiver.ObjectRef()
			}
			value, handled, err := dispatcher.getProperty(property, context, property)
			if err != nil {
				return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: GETP: %w", object, pc, err)
			}
			if handled {
				return value, nil
			}
		}
		if invoke == nil {
			return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: GETP requires VM property support", object, pc)
		}
		value, err := invoke(property, receiver, nil, true)
		if err != nil {
			return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: GETP: %w", object, pc, err)
		}
		return value, nil
	}
	// assignProperty implements VM_SETP, the reference VM's
	// PropSet(0, nullptr, ...): write through a property closure. A
	// dispatcher-registered descriptor runs its setter program directly. A
	// VM-owned property value carries the member name its accessor was declared
	// under, so the write is routed through the VM-aware member setter of the
	// object the closure dispatches against; the raw member is verified to still
	// be this closure first, so the write can never create or overwrite an
	// unrelated member when the closure no longer belongs to its receiver.
	assignProperty := func(property, receiver, input Value, pc int) error {
		if err := propertyClosure(property, pc, "SETP"); err != nil {
			return err
		}
		if registeredProperty(property) {
			context := ObjectRef{}
			if receiver.Kind() == KindObject && !receiver.IsNull() {
				context = receiver.ObjectRef()
			}
			handled, err := dispatcher.setProperty(property, context, property, input)
			if err != nil {
				return fmt.Errorf("legacy intercode object %d code offset %d: SETP: %w", object, pc, err)
			}
			if handled {
				return nil
			}
		}
		if set == nil {
			return fmt.Errorf("legacy intercode object %d code offset %d: SETP requires VM property support", object, pc)
		}
		if receiver.Kind() != KindObject || receiver.IsNull() {
			return fmt.Errorf("legacy intercode object %d code offset %d: SETP receiver is %s", object, pc, receiver.TypeName())
		}
		declared, err := read(property, "name", 0)
		if err != nil {
			return fmt.Errorf("legacy intercode object %d code offset %d: SETP property name: %w", object, pc, err)
		}
		if declared.Kind() != KindString || declared.Text() == "" {
			return fmt.Errorf("legacy intercode object %d code offset %d: SETP property object has no declared name", object, pc)
		}
		name := declared.Text()
		// The member must still be this very closure: the VM-aware setter only
		// dispatches to a property when the receiver still stores the property
		// value, and reading it raw keeps the check independent of the VM's
		// property dereferencing.
		member, err := dispatcher.Get(receiver, name, IgnoreProperty)
		if err != nil || member.Kind() != KindObject || member.IsNull() || member.ObjectRef().Object != property.ObjectRef().Object {
			return fmt.Errorf("legacy intercode object %d code offset %d: SETP property %q does not belong to its receiver", object, pc, name)
		}
		if err = set(receiver, name, input, 0); err != nil {
			return fmt.Errorf("legacy intercode object %d code offset %d: SETP %q: %w", object, pc, name, err)
		}
		return nil
	}
	// invalidateValue implements VM_INV: a non-object register becomes false,
	// while an object register is invalidated through the owning VM so its
	// finalizer runs. Without a VM hook the object table's validity bit is
	// cleared instead, which preserves validity bookkeeping but cannot run the
	// VM-owned finalizer.
	invalidateValue := func(value Value, pc int) (Value, error) {
		if value.Kind() != KindObject || value.IsNull() {
			return IntegerValue(0), nil
		}
		if invalidate != nil {
			result, err := invalidate(value)
			if err != nil {
				return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: INV: %w", object, pc, err)
			}
			return result, nil
		}
		if table == nil {
			return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: INV requires an object table or invalidation support", object, pc)
		}
		target, ok := table.Lookup(value.ObjectRef().Object)
		if !ok {
			return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: invalid object handle %d", object, pc, value.ObjectRef().Object)
		}
		if !target.IsValid() {
			return IntegerValue(0), nil
		}
		target.Invalidate()
		return IntegerValue(1), nil
	}
	// isValidValue implements VM_CHKINV, mirroring (*VM).IsValid: a non-object
	// register is valid by definition and an object register reports the VM's
	// or the object table's validity state.
	isValidValue := func(value Value, pc int) (Value, error) {
		if value.Kind() != KindObject || value.IsNull() {
			return IntegerValue(1), nil
		}
		if isValid != nil {
			return isValid(value), nil
		}
		if table == nil {
			return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: CHKINV requires an object table or validity support", object, pc)
		}
		target, ok := table.Lookup(value.ObjectRef().Object)
		if !ok {
			return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: invalid object handle %d", object, pc, value.ObjectRef().Object)
		}
		return boolValue(target.IsValid()), nil
	}
	// stepValue applies the shared TJS ++/-- conversion used by the register
	// forms (VM_INC/VM_DEC) and by every property form
	// (VM_INCPD/VM_INCPI/VM_INCP and the decrement variants): strings convert
	// to a number first, void is treated as 0, and every other type that is
	// neither Integer nor Real raises the conversion error.
	stepValue := func(value Value, delta int64, pc int, op string) (Value, error) {
		if value.Kind() == KindString {
			converted, err := value.Number()
			if err != nil {
				return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: %s: %w", object, pc, op, err)
			}
			value = converted
		}
		switch value.Kind() {
		case KindVoid:
			return IntegerValue(delta), nil
		case KindInteger:
			return IntegerValue(value.IntegerValue() + delta), nil
		case KindReal:
			return RealValue(value.RealValue() + float64(delta)), nil
		default:
			return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: %s: cannot convert TJS %s to Integer or Real", object, pc, op, value.TypeName())
		}
	}
	// stepDelta maps an increment/decrement opcode to its signed step. The four
	// decrement opcodes are DEC, DECPD, DECPI and DECP.
	stepDelta := func(op int16) int64 {
		switch op {
		case 22, 23, 24, 25:
			return -1
		default:
			return 1
		}
	}
	// stepProperty performs the read-modify-write property operation behind
	// INCPD/INCPI and DECPD/DECPI. The legacy VM always computes the new value
	// and stores it into the optional result register; the postfix/prefixed
	// distinction is resolved by the code generator, which saves the previous
	// value before issuing the operation with a zero result register. The
	// member-less INCP/DECP forms step the operand register in place instead.
	stepProperty := func(dst int16, target int, name string, delta int64, op string, pc int) error {
		value, err := readValue(target, name, 0, pc)
		if err != nil {
			return err
		}
		value, err = stepValue(value, delta, pc, op)
		if err != nil {
			return err
		}
		if err = writeProperty(target, name, value, 0, pc); err != nil {
			return err
		}
		if dst != 0 {
			index, err := reg(dst, pc)
			if err != nil {
				return err
			}
			regs[index] = value
		}
		return nil
	}
	// shiftOperator maps the legacy shift-family opcodes onto the operator
	// names understood by Value.Binary. Each of the four suffixes shares the
	// operator of its base opcode: SAR (46..49) is the arithmetic right shift,
	// SAL (50..53) the left shift and SR (54..57) the logical right shift.
	shiftOperator := func(op int16) string {
		switch {
		case op >= 46 && op <= 49:
			return ">>"
		case op >= 50 && op <= 53:
			return "<<"
		default:
			return ">>>"
		}
	}
	// bitwiseOperator maps the legacy bitwise/logical-family opcodes onto the
	// operator names understood by Value.Binary. Each of the four suffixes
	// shares the operator of its base opcode: LOR (26..29) is the logical OR,
	// LAND (30..33) the logical AND, BOR (34..37) the bitwise OR, BXOR
	// (38..41) the bitwise XOR and BAND (42..45) the bitwise AND. The logical
	// forms yield the TJS 0/1 integer result, exactly like the reference VM's
	// logicalorequal/logicalandequal, and never return one of the operands.
	bitwiseOperator := func(op int16) string {
		switch {
		case op >= 26 && op <= 29:
			return "||"
		case op >= 30 && op <= 33:
			return "&&"
		case op >= 34 && op <= 37:
			return "|"
		case op >= 38 && op <= 41:
			return "^"
		default:
			return "&"
		}
	}
	// arithmeticOperator maps the legacy arithmetic-family opcodes onto the
	// operator names understood by Value.Binary. Each of the four suffixes
	// shares the operator of its base opcode: ADD (58..61) is addition, SUB
	// (62..65) subtraction, MOD (66..69) the integer remainder, DIV (70..73)
	// the floating-point division, IDIV (74..77) the integer division the
	// reference VM calls idivequal (rendered as "\" by TJS) and MUL (78..81)
	// multiplication.
	arithmeticOperator := func(op int16) string {
		switch {
		case op >= 58 && op <= 61:
			return "+"
		case op >= 62 && op <= 65:
			return "-"
		case op >= 66 && op <= 69:
			return "%"
		case op >= 70 && op <= 73:
			return "/"
		case op >= 74 && op <= 77:
			return "\\"
		default:
			return "*"
		}
	}
	// unaryInPlace applies one of the in-place conversion opcodes -- BNOT
	// (82), ASC (89), CHR (90), CHS (92), INT (95), REAL (96) and OCTET (98)
	// -- to the register named by its single operand, storing the converted
	// value back into that same register exactly like the reference VM's
	// tTJSVariant conversions (bitnot, changesign, ToInteger, ToReal,
	// ToOctet, CharacterCodeOf and CharacterCodeFrom).
	unaryInPlace := func(operand int16, unary, op string, pc int) error {
		index, err := reg(operand, pc)
		if err != nil {
			return err
		}
		value, err := regs[index].Unary(unary)
		if err != nil {
			return fmt.Errorf("legacy intercode object %d code offset %d: %s: %w", object, pc, op, err)
		}
		regs[index] = value
		return nil
	}
	// binaryProperty performs the read-modify-write behind the compound
	// property forms VM_SARPD/SARPI/SARP and the SAL/SR counterparts. The
	// legacy VM dispatches the operator through the member's closure and stores
	// the new value into the optional result register; this implementation
	// reads the member, applies the shared TJS operator semantics and writes it
	// back, mirroring stepProperty for ++/--.
	binaryProperty := func(dst int16, target int, name string, param Value, operator, op string, pc int) error {
		value, err := readValue(target, name, 0, pc)
		if err != nil {
			return err
		}
		value, err = value.Binary(operator, param)
		if err != nil {
			return fmt.Errorf("legacy intercode object %d code offset %d: %s: %w", object, pc, op, err)
		}
		if err = writeProperty(target, name, value, 0, pc); err != nil {
			return err
		}
		if dst != 0 {
			index, err := reg(dst, pc)
			if err != nil {
				return err
			}
			regs[index] = value
		}
		return nil
	}
	// callArguments mirrors the reference VM's TJS_BEGIN_FUNC_CALL_ARGS macro
	// and is shared by CALL, CALLD, CALLI and NEW. countOffset is the operand
	// index holding the argument count; the argument operands, or the written
	// descriptor count of an expanded list, follow it. A count of -1 forwards
	// the current frame's arguments to the callee unmodified, and the
	// fatUnnamedExpand descriptor forwards the current frame's unnamed
	// arguments, exactly like the macro.
	callArguments := func(in LegacyInstruction, countOffset int, label string) ([]Value, error) {
		count := int(in.Operands[countOffset])
		switch {
		case count == -1:
			return args, nil
		case count == -2:
			descriptors := countOffset + 1
			if len(in.Operands) <= descriptors {
				return nil, fmt.Errorf("legacy intercode object %d code offset %d: truncated %s argument descriptors", object, in.Offset, label)
			}
			written := int(in.Operands[descriptors])
			if written < 0 || len(in.Operands) != descriptors+1+written*2 {
				return nil, fmt.Errorf("legacy intercode object %d code offset %d: invalid %s argument descriptor count %d", object, in.Offset, label, written)
			}
			loaded := make([]Value, 0, written)
			for i := range written {
				argType := in.Operands[descriptors+1+i*2]
				register, e := reg(in.Operands[descriptors+2+i*2], in.Offset)
				if e != nil {
					return nil, e
				}
				switch argType {
				case 0: // fatNormal
					loaded = append(loaded, regs[register])
				case 1: // fatExpand
					array := regs[register]
					if array.Kind() != KindObject || array.IsNull() {
						return nil, fmt.Errorf("legacy intercode object %d code offset %d: %s spread argument is %s, not an array", object, in.Offset, label, array.TypeName())
					}
					length, e := dispatcher.Get(array, "count")
					if e != nil {
						return nil, fmt.Errorf("legacy intercode object %d code offset %d: %s spread argument count: %w", object, in.Offset, label, e)
					}
					n, e := length.Int()
					if e != nil || n < 0 || n > 1024-int64(len(loaded)) {
						return nil, fmt.Errorf("legacy intercode object %d code offset %d: invalid %s spread argument count %s", object, in.Offset, label, length.Text())
					}
					for j := int64(0); j < n; j++ {
						value, e := dispatcher.Get(array, fmt.Sprint(j))
						if e != nil {
							return nil, fmt.Errorf("legacy intercode object %d code offset %d: %s spread argument %d: %w", object, in.Offset, label, j, e)
						}
						loaded = append(loaded, value)
					}
				case 2: // fatUnnamedExpand
					if base := int(o.UnnamedArgumentBase); base < len(args) {
						loaded = append(loaded, args[base:]...)
					}
				default:
					return nil, fmt.Errorf("legacy intercode object %d code offset %d: invalid %s argument type %d", object, in.Offset, label, argType)
				}
				if len(loaded) > 1024 {
					return nil, fmt.Errorf("legacy intercode object %d code offset %d: too many %s arguments", object, in.Offset, label)
				}
			}
			return loaded, nil
		default:
			if count < 0 || len(in.Operands) != countOffset+1+count {
				return nil, fmt.Errorf("legacy intercode object %d code offset %d: invalid %s argument count %d", object, in.Offset, label, count)
			}
			if count > 1024 {
				return nil, fmt.Errorf("legacy intercode object %d code offset %d: too many %s arguments", object, in.Offset, label)
			}
			loaded := make([]Value, 0, count)
			for _, operand := range in.Operands[countOffset+1:] {
				register, e := reg(operand, in.Offset)
				if e != nil {
					return nil, e
				}
				loaded = append(loaded, regs[register])
			}
			return loaded, nil
		}
	}
	type legacyExceptionHandler struct {
		catchIP      int
		exceptionReg int
	}
	var exceptionHandlers []legacyExceptionHandler
	// ENTRY/THROW/EXTRY model the legacy VM's explicit script-exception path.
	// Operation errors still propagate normally; only THROW transfers to a
	// registered catch target.
	for ip := startIP; ip < len(instructions); {
		in := instructions[ip]
		nextIP := ip + 1
		switch in.Opcode {
		case 15, 16, 17: // Jump operands are relative offsets in 16-bit code units.
			take := in.Opcode == 17 || (in.Opcode == 15 && flag) || (in.Opcode == 16 && !flag)
			if take {
				targetOffset := in.Offset + int(in.Operands[0])
				targetIP, ok := offsetToIP[targetOffset]
				if !ok {
					return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: %s target offset %d is not an instruction boundary", object, in.Offset, in.Name, targetOffset)
				}
				nextIP = targetIP
			}
		case 5, 6: // TT/TF set the VM comparison flag from a register's truthiness.
			value, e := reg(in.Operands[0], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			flag, e = regs[value].Truth()
			if e != nil {
				return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: %s: %w", object, in.Offset, legacyOpcodeNames[in.Opcode], e)
			}
			if in.Opcode == 6 {
				flag = !flag
			}
		case 14: // NF negates the VM comparison flag without touching registers.
			flag = !flag
		case 13: // LNOT replaces a register with its logical negation.
			value, e := reg(in.Operands[0], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			regs[value], e = regs[value].Unary("!")
			if e != nil {
				return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: LNOT: %w", object, in.Offset, e)
			}
		case 2: // CP copies a register value, preserving its complete TJS variant.
			dst, e := reg(in.Operands[0], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			src, e := reg(in.Operands[1], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			regs[dst] = regs[src]
		case 0: // NOP
		case 3: // CL clears one register to the TJS void value.
			index, e := reg(in.Operands[0], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			regs[index] = VoidValue
		case 7, 8: // CEQ/CDEQ update the VM comparison flag.
			left, e := reg(in.Operands[0], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			right, e := reg(in.Operands[1], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			if in.Opcode == 7 {
				flag = regs[left].Equal(regs[right])
			} else {
				flag = regs[left].StrictEqual(regs[right])
			}
		case 9, 10: // CLT/CGT update the VM comparison flag: CLT computes
			// ra < rb, CGT computes ra > rb (the reference VM realizes CLT with
			// tTJSVariant::GreaterThan and CGT with LittlerThan, both of which
			// are named against their actual result).
			left, e := reg(in.Operands[0], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			right, e := reg(in.Operands[1], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			operator := "<"
			if in.Opcode == 10 {
				operator = ">"
			}
			result, e := regs[left].Binary(operator, regs[right])
			if e != nil {
				return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: %s: %w", object, in.Offset, in.Name, e)
			}
			flag, e = result.Truth()
			if e != nil {
				return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: %s: %w", object, in.Offset, in.Name, e)
			}
		case 11, 12: // SETF/SETNF copy the comparison flag to a register.
			dst, e := reg(in.Operands[0], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			value := flag
			if in.Opcode == 12 {
				value = !value
			}
			if value {
				regs[dst] = IntegerValue(1)
			} else {
				regs[dst] = IntegerValue(0)
			}
		case 1: // CONST ra, da -- value is copied from this object's DATA-backed constants.
			dst, e := reg(in.Operands[0], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			ci := int(in.Operands[1])
			if ci < 0 || ci >= len(o.Constants) {
				return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: constant index %d out of range", object, in.Offset, ci)
			}
			regs[dst] = o.Constants[ci].Value
		case 18, 22: // INC/DEC update one register using TJS numeric conversion.
			index, e := reg(in.Operands[0], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			delta := int64(1)
			if in.Opcode == 22 {
				delta = -1
			}
			value, e := stepValue(regs[index], delta, in.Offset, in.Name)
			if e != nil {
				return VoidValue, e
			}
			regs[index] = value
		case 19, 23: // INCPD/DECPD -- result = ++ra[target][constant-name].
			target, e := reg(in.Operands[1], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			name, e := directName(in.Operands[2], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			if e = stepProperty(in.Operands[0], target, name, stepDelta(in.Opcode), in.Name, in.Offset); e != nil {
				return VoidValue, e
			}
		case 20, 24: // INCPI/DECPI -- result = ++ra[target][ra[key]].
			target, e := reg(in.Operands[1], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			key, e := reg(in.Operands[2], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			name, e := memberName(regs[key], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			if e = stepProperty(in.Operands[0], target, name, stepDelta(in.Opcode), in.Name, in.Offset); e != nil {
				return VoidValue, e
			}
		case 21, 25: // INCP/DECP -- result = ++ra[target] (member-less property form).
			target, e := reg(in.Operands[1], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			value, e := stepValue(regs[target], stepDelta(in.Opcode), in.Offset, in.Name)
			if e != nil {
				return VoidValue, e
			}
			regs[target] = value
			if in.Operands[0] != 0 {
				index, e := reg(in.Operands[0], in.Offset)
				if e != nil {
					return VoidValue, e
				}
				regs[index] = value
			}
		case 26, 30, 34, 38, 42: // LOR/LAND/BOR/BXOR/BAND -- ra[left] op= ra[right].
			left, e := reg(in.Operands[0], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			right, e := reg(in.Operands[1], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			regs[left], e = regs[left].Binary(bitwiseOperator(in.Opcode), regs[right])
			if e != nil {
				return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: %s: %w", object, in.Offset, in.Name, e)
			}
		case 27, 31, 35, 39, 43: // LORPD/LANDPD/BORPD/BXORPD/BANDPD -- result = ra[target][constant-name] op= ra[param].
			target, e := reg(in.Operands[1], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			name, e := directName(in.Operands[2], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			param, e := reg(in.Operands[3], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			if e = binaryProperty(in.Operands[0], target, name, regs[param], bitwiseOperator(in.Opcode), in.Name, in.Offset); e != nil {
				return VoidValue, e
			}
		case 28, 32, 36, 40, 44: // LORPI/LANDPI/BORPI/BXORPI/BANDPI -- result = ra[target][ra[key]] op= ra[param].
			target, e := reg(in.Operands[1], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			key, e := reg(in.Operands[2], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			name, e := memberName(regs[key], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			param, e := reg(in.Operands[3], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			if e = binaryProperty(in.Operands[0], target, name, regs[param], bitwiseOperator(in.Opcode), in.Name, in.Offset); e != nil {
				return VoidValue, e
			}
		case 29, 33, 37, 41, 45: // LORP/LANDP/BORP/BXORP/BANDP -- result = ra[target] op= ra[param] (member-less form).
			target, e := reg(in.Operands[1], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			param, e := reg(in.Operands[2], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			value, e := regs[target].Binary(bitwiseOperator(in.Opcode), regs[param])
			if e != nil {
				return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: %s: %w", object, in.Offset, in.Name, e)
			}
			regs[target] = value
			if in.Operands[0] != 0 {
				index, e := reg(in.Operands[0], in.Offset)
				if e != nil {
					return VoidValue, e
				}
				regs[index] = value
			}
		case 46, 50, 54: // SAR/SAL/SR -- ra[left] op= ra[right].
			left, e := reg(in.Operands[0], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			right, e := reg(in.Operands[1], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			regs[left], e = regs[left].Binary(shiftOperator(in.Opcode), regs[right])
			if e != nil {
				return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: %s: %w", object, in.Offset, in.Name, e)
			}
		case 47, 51, 55: // SARPD/SALPD/SRPD -- result = ra[target][constant-name] op= ra[param].
			target, e := reg(in.Operands[1], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			name, e := directName(in.Operands[2], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			param, e := reg(in.Operands[3], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			if e = binaryProperty(in.Operands[0], target, name, regs[param], shiftOperator(in.Opcode), in.Name, in.Offset); e != nil {
				return VoidValue, e
			}
		case 48, 52, 56: // SARPI/SALPI/SRPI -- result = ra[target][ra[key]] op= ra[param].
			target, e := reg(in.Operands[1], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			key, e := reg(in.Operands[2], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			name, e := memberName(regs[key], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			param, e := reg(in.Operands[3], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			if e = binaryProperty(in.Operands[0], target, name, regs[param], shiftOperator(in.Opcode), in.Name, in.Offset); e != nil {
				return VoidValue, e
			}
		case 49, 53, 57: // SARP/SALP/SRP -- result = ra[target] op= ra[param] (member-less form).
			target, e := reg(in.Operands[1], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			param, e := reg(in.Operands[2], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			value, e := regs[target].Binary(shiftOperator(in.Opcode), regs[param])
			if e != nil {
				return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: %s: %w", object, in.Offset, in.Name, e)
			}
			regs[target] = value
			if in.Operands[0] != 0 {
				index, e := reg(in.Operands[0], in.Offset)
				if e != nil {
					return VoidValue, e
				}
				regs[index] = value
			}
		// The arithmetic family mirrors the bitwise and shift groups. The
		// register form applies the operator in place, the direct/indirect
		// property forms pipe it through binaryProperty and the member-less
		// property form reads the object itself; each suffix shares its base
		// opcode's operator through arithmeticOperator.
		case 58, 62, 66, 70, 74, 78: // ADD/SUB/MOD/DIV/IDIV/MUL -- ra[left] op= ra[right].
			left, e := reg(in.Operands[0], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			right, e := reg(in.Operands[1], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			regs[left], e = regs[left].Binary(arithmeticOperator(in.Opcode), regs[right])
			if e != nil {
				return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: %s: %w", object, in.Offset, in.Name, e)
			}
		case 59, 63, 67, 71, 75, 79: // ADDPD/SUBPD/MODPD/DIVPD/IDIVPD/MULPD -- result = ra[target][constant-name] op= ra[param].
			target, e := reg(in.Operands[1], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			name, e := directName(in.Operands[2], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			param, e := reg(in.Operands[3], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			if e = binaryProperty(in.Operands[0], target, name, regs[param], arithmeticOperator(in.Opcode), in.Name, in.Offset); e != nil {
				return VoidValue, e
			}
		case 60, 64, 68, 72, 76, 80: // ADDPI/SUBPI/MODPI/DIVPI/IDIVPI/MULPI -- result = ra[target][ra[key]] op= ra[param].
			target, e := reg(in.Operands[1], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			key, e := reg(in.Operands[2], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			name, e := memberName(regs[key], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			param, e := reg(in.Operands[3], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			if e = binaryProperty(in.Operands[0], target, name, regs[param], arithmeticOperator(in.Opcode), in.Name, in.Offset); e != nil {
				return VoidValue, e
			}
		case 61, 65, 69, 73, 77, 81: // ADDP/SUBP/MODP/DIVP/IDIVP/MULP -- result = ra[target] op= ra[param] (member-less form).
			target, e := reg(in.Operands[1], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			param, e := reg(in.Operands[2], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			value, e := regs[target].Binary(arithmeticOperator(in.Opcode), regs[param])
			if e != nil {
				return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: %s: %w", object, in.Offset, in.Name, e)
			}
			regs[target] = value
			if in.Operands[0] != 0 {
				index, e := reg(in.Operands[0], in.Offset)
				if e != nil {
					return VoidValue, e
				}
				regs[index] = value
			}
		// The in-place conversion family rewrites one register from its own
		// value. BNOT, ASC, CHR, CHS, INT, REAL and OCTET share unaryInPlace;
		// NUM (91) and STR (97) below keep their dedicated conversions.
		case 82: // BNOT -- bitwise complement through TJS integer conversion.
			if e := unaryInPlace(in.Operands[0], "~", in.Name, in.Offset); e != nil {
				return VoidValue, e
			}
		case 89: // ASC -- replace the register with its first character code.
			if e := unaryInPlace(in.Operands[0], "#", in.Name, in.Offset); e != nil {
				return VoidValue, e
			}
		case 90: // CHR -- replace the register with the character for its code.
			if e := unaryInPlace(in.Operands[0], "$", in.Name, in.Offset); e != nil {
				return VoidValue, e
			}
		case 92: // CHS -- negate the register through TJS numeric conversion.
			if e := unaryInPlace(in.Operands[0], "-", in.Name, in.Offset); e != nil {
				return VoidValue, e
			}
		case 95: // INT -- convert the register in place to a TJS integer.
			if e := unaryInPlace(in.Operands[0], "int", in.Name, in.Offset); e != nil {
				return VoidValue, e
			}
		case 96: // REAL -- convert the register in place to a TJS real.
			if e := unaryInPlace(in.Operands[0], "real", in.Name, in.Offset); e != nil {
				return VoidValue, e
			}
		case 98: // OCTET -- convert the register in place to a TJS octet.
			if e := unaryInPlace(in.Operands[0], "octet", in.Name, in.Offset); e != nil {
				return VoidValue, e
			}
		case 91: // NUM -- convert the register in place using TJS tonumber semantics.
			index, e := reg(in.Operands[0], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			value, e := regs[index].Number()
			if e != nil {
				return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: NUM: %w", object, in.Offset, e)
			}
			regs[index] = value
		case 97: // STR -- convert the register in place to a TJS string.
			index, e := reg(in.Operands[0], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			value, e := regs[index].Unary("string")
			if e != nil {
				return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: STR: %w", object, in.Offset, e)
			}
			regs[index] = value
		case 118: // SRV copies a register into the current function's return slot.
			index, e := reg(in.Operands[0], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			returnValue = regs[index]
		case 119: // RET returns the value most recently set by SRV.
			return returnValue, nil
		case 120: // ENTRY registers a relative catch target and exception register.
			targetOffset := in.Offset + int(in.Operands[0])
			catchIP, ok := offsetToIP[targetOffset]
			if !ok {
				return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: ENTRY target offset %d is not an instruction boundary", object, in.Offset, targetOffset)
			}
			exceptionReg, e := reg(in.Operands[1], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			exceptionHandlers = append(exceptionHandlers, legacyExceptionHandler{
				catchIP: catchIP, exceptionReg: exceptionReg,
			})
		case 121: // EXTRY leaves the innermost protected region.
			if len(exceptionHandlers) != 0 {
				exceptionHandlers = exceptionHandlers[:len(exceptionHandlers)-1]
			}
		case 122: // THROW transfers the script value to the innermost catch.
			thrown, e := reg(in.Operands[0], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			if len(exceptionHandlers) == 0 {
				message := regs[thrown].Text()
				if regs[thrown].Kind() == KindObject && !regs[thrown].IsNull() {
					if property, propertyErr := dispatcher.Get(regs[thrown], "message"); propertyErr == nil {
						switch property.Kind() {
						case KindString, KindInteger, KindReal:
							message = property.Text()
						}
					}
				}
				return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: uncaught TJS exception: %s", object, in.Offset, message)
			}
			handler := exceptionHandlers[len(exceptionHandlers)-1]
			exceptionHandlers = exceptionHandlers[:len(exceptionHandlers)-1]
			regs[handler.exceptionReg] = regs[thrown]
			nextIP = handler.catchIP
		case 123: // CHGTHIS
			dst, e := reg(in.Operands[0], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			ctx, e := reg(in.Operands[1], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			if regs[dst].Kind() == KindObject {
				ref := regs[dst].ObjectRef()
				context := regs[ctx].ObjectRef()
				regs[dst] = ObjectValue(NewObjectRef(ref.Object, context.Object))
			}
		case 83: // TYPEOF replaces the register with the TJS type-name string.
			index, e := reg(in.Operands[0], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			regs[index], e = regs[index].Unary("typeof")
			if e != nil {
				return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: TYPEOF: %w", object, in.Offset, e)
			}
		case 84: // TYPEOFD ra[dst] = typeof ra[target][constant-name].
			dst, e := reg(in.Operands[0], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			target, e := reg(in.Operands[1], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			name, e := directName(in.Operands[2], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			value, e := read(regs[target], name, MemberMustExist)
			if errors.Is(e, ErrMemberNotFound) {
				regs[dst] = StringValue("undefined")
				break
			}
			if e != nil {
				return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: TYPEOFD %q lookup: %w", object, in.Offset, name, e)
			}
			regs[dst], e = value.Unary("typeof")
			if e != nil {
				return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: TYPEOFD %q: %w", object, in.Offset, name, e)
			}
		case 85: // TYPEOFI ra[dst] = typeof ra[target][ra[key]].
			dst, e := reg(in.Operands[0], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			target, e := reg(in.Operands[1], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			key, e := reg(in.Operands[2], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			name, e := memberName(regs[key], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			value, e := read(regs[target], name, MemberMustExist)
			if errors.Is(e, ErrMemberNotFound) {
				regs[dst] = StringValue("undefined")
				break
			}
			if e != nil {
				return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: TYPEOFI %q lookup: %w", object, in.Offset, name, e)
			}
			regs[dst], e = value.Unary("typeof")
			if e != nil {
				return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: TYPEOFI %q: %w", object, in.Offset, name, e)
			}
		case 86, 87: // EVAL/EEXP -- evaluate the register's source text in this context.
			index, e := reg(in.Operands[0], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			// The reference VM evaluates the register's string form and only
			// leaves the result in that same register when it is used: EVAL
			// stores it back, EEXP discards it. An empty source is a no-op.
			if source := regs[index].Text(); source != "" {
				if evaluate == nil {
					return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: %s requires evaluation support", object, in.Offset, in.Name)
				}
				value, e := evaluate(source, thisContext, in.Opcode == 86)
				if e != nil {
					return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: %s: %w", object, in.Offset, in.Name, e)
				}
				if in.Opcode == 86 {
					regs[index] = value
				}
			}
		case 88: // CHKINS ra[value] = ra[value] instanceof ra[class].
			value, e := reg(in.Operands[0], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			class, e := reg(in.Operands[1], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			if instanceOf == nil {
				return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: CHKINS requires instanceof support", object, in.Offset)
			}
			result, e := instanceOf(regs[value], regs[class])
			if e != nil {
				return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: CHKINS: %w", object, in.Offset, e)
			}
			regs[value] = result
		case 93: // INV -- invalidate the object register; non-objects yield false.
			index, e := reg(in.Operands[0], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			regs[index], e = invalidateValue(regs[index], in.Offset)
			if e != nil {
				return VoidValue, e
			}
		case 94: // CHKINV -- report whether the object register is still valid.
			index, e := reg(in.Operands[0], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			regs[index], e = isValidValue(regs[index], in.Offset)
			if e != nil {
				return VoidValue, e
			}
		case 124: // GLOBAL
			dst, e := reg(in.Operands[0], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			regs[dst] = global
		case 125: // ADDCI registers an instance member on the current class.
			target, e := reg(in.Operands[0], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			nameRegister, e := reg(in.Operands[1], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			if e = propertyTarget(regs[target], in.Offset); e != nil {
				return VoidValue, e
			}
			name, e := memberName(regs[nameRegister], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			classID := regs[target].ObjectRef().Object
			if targetObject, ok := table.Lookup(classID); ok && targetObject.Category == CategoryInstance {
				if mapped, mappedOK := table.ClassForInstance(classID); mappedOK {
					classID = mapped
				}
			}
			if err := table.AddClassInstanceMember(classID, name); err != nil {
				return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: ADDCI: %w", object, in.Offset, err)
			}
		case 126: // REGMEMBER enables the registered class members for instances.
			target := regs[registerBase-1]
			if e := propertyTarget(target, in.Offset); e != nil {
				return VoidValue, e
			}
			classID := target.ObjectRef().Object
			if targetObject, ok := table.Lookup(classID); ok && targetObject.Category == CategoryInstance {
				if mapped, mappedOK := table.ClassForInstance(classID); mappedOK {
					classID = mapped
				}
			}
			if err := table.RegisterClassInstanceMembers(classID); err != nil {
				return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: REGMEMBER: %w", object, in.Offset, err)
			}
		case 127: // DEBUGGER -- the reference VM calls TJSNativeDebuggerBreak, which
			// raises a native breakpoint only inside an MSVC-hosted debugger and is
			// a no-op everywhere else; with no native debugger attached this explicit
			// no-op is the faithful behaviour.
		case 103, 110: // GPD, GPDS -- ra[dst] = ra[target][constant-name]
			dst, e := reg(in.Operands[0], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			target, e := reg(in.Operands[1], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			name, e := directName(in.Operands[2], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			var flags uint32
			if in.Opcode == 110 {
				flags = IgnoreProperty
			}
			if e = readProperty(dst, target, name, flags, in.Offset); e != nil {
				return VoidValue, e
			}
		case 104, 105, 106, 111: // SPD, SPDE, SPDEH, SPDS
			target, e := reg(in.Operands[0], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			name, e := directName(in.Operands[1], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			value, e := reg(in.Operands[2], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			var flags uint32
			switch in.Opcode {
			case 105:
				flags = MemberEnsure
			case 106:
				flags = MemberEnsure | HiddenMember
			case 111:
				flags = MemberEnsure | IgnoreProperty
			}
			if e = writeProperty(target, name, regs[value], flags, in.Offset); e != nil {
				return VoidValue, e
			}
		case 107, 112: // GPI, GPIS -- ra[dst] = ra[target][ra[key]]
			dst, e := reg(in.Operands[0], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			target, e := reg(in.Operands[1], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			key, e := reg(in.Operands[2], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			name, e := memberName(regs[key], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			var flags uint32
			if in.Opcode == 112 {
				flags = IgnoreProperty
			}
			if e = readProperty(dst, target, name, flags, in.Offset); e != nil {
				return VoidValue, e
			}
		case 108, 109, 113: // SPI, SPIE, SPIS
			target, e := reg(in.Operands[0], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			key, e := reg(in.Operands[1], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			name, e := memberName(regs[key], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			value, e := reg(in.Operands[2], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			var flags uint32
			switch in.Opcode {
			case 109:
				flags = MemberEnsure
			case 113:
				flags = MemberEnsure | IgnoreProperty
			}
			if e = writeProperty(target, name, regs[value], flags, in.Offset); e != nil {
				return VoidValue, e
			}
		case 116: // DELD ra[result] = delete ra[target][constant-name].
			result, e := reg(in.Operands[0], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			target, e := reg(in.Operands[1], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			name, e := directName(in.Operands[2], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			if e = propertyTarget(regs[target], in.Offset); e != nil {
				return VoidValue, e
			}
			existed := dispatcher.Has(regs[target], name)
			e = dispatcher.Delete(regs[target], name)
			deleted := e == nil && existed
			if e != nil && !errors.Is(e, ErrMemberNotFound) && !errors.Is(e, ErrCannotDelete) {
				return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: delete property %q: %w", object, in.Offset, name, e)
			}
			if result != 0 {
				regs[result] = boolValue(deleted)
			}
		case 117: // DELI ra[result] = delete ra[target][ra[key]].
			result, e := reg(in.Operands[0], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			target, e := reg(in.Operands[1], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			key, e := reg(in.Operands[2], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			name, e := memberName(regs[key], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			if e = propertyTarget(regs[target], in.Offset); e != nil {
				return VoidValue, e
			}
			existed := dispatcher.Has(regs[target], name)
			e = dispatcher.Delete(regs[target], name)
			deleted := e == nil && existed
			if e != nil && !errors.Is(e, ErrMemberNotFound) && !errors.Is(e, ErrCannotDelete) {
				return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: delete property %q: %w", object, in.Offset, name, e)
			}
			if result != 0 {
				regs[result] = boolValue(deleted)
			}
		case 115: // GETP ra[dst] = *ra[property] -- read the value a property closure stands for.
			dst, e := reg(in.Operands[0], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			property, e := reg(in.Operands[1], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			value, e := dereferenceProperty(regs[property], propertyReceiver(regs[property]), in.Offset)
			if e != nil {
				return VoidValue, e
			}
			regs[dst] = value
		case 114: // SETP *ra[property] = ra[src] -- write through a property closure.
			property, e := reg(in.Operands[0], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			src, e := reg(in.Operands[1], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			if e = assignProperty(regs[property], propertyReceiver(regs[property]), regs[src], in.Offset); e != nil {
				return VoidValue, e
			}
		case 102: // NEW ra, ra, count, args -- construct a class/native object.
			result, e := reg(in.Operands[0], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			callee, e := reg(in.Operands[1], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			constructed, e := callArguments(in, 2, "NEW")
			if e != nil {
				return VoidValue, e
			}
			if construct == nil {
				return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: NEW requires VM construction support", object, in.Offset)
			}
			value, e := construct(regs[callee], constructed)
			if e != nil {
				return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: NEW: %w", object, in.Offset, e)
			}
			if result != 0 {
				regs[result] = value
			}
		case 99: // CALL ra, callable, count, args -- invoke with the current this context.
			result, e := reg(in.Operands[0], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			callee, e := reg(in.Operands[1], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			callArgs, e := callArguments(in, 2, "CALL")
			if e != nil {
				return VoidValue, e
			}
			receiver := regs[registerBase-1]
			var value Value
			if invoke != nil {
				value, e = invoke(regs[callee], receiver, callArgs, result != 0)
			} else {
				context := ObjectRef{}
				if receiver.Kind() == KindObject && !receiver.IsNull() {
					context = receiver.ObjectRef()
				}
				value, e = dispatcher.CallWithContext(regs[callee], context, callArgs...)
			}
			if e != nil {
				return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: CALL %s: %w", object, in.Offset, regs[callee].TypeName(), e)
			}
			if result != 0 {
				regs[result] = value
			}
		case 100, 101: // CALLD/CALLI -- invoke the named member of an object. CALLD
			// reads the member name from the constant area, CALLI from a register;
			// the argument layout is identical, so both share the loader.
			result, e := reg(in.Operands[0], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			callee, e := reg(in.Operands[1], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			var name string
			if in.Opcode == 100 {
				name, e = directName(in.Operands[2], in.Offset)
			} else {
				key, keyErr := reg(in.Operands[2], in.Offset)
				if keyErr != nil {
					return VoidValue, keyErr
				}
				name, e = memberName(regs[key], in.Offset)
			}
			if e != nil {
				return VoidValue, e
			}
			callArgs, e := callArguments(in, 3, in.Name)
			if e != nil {
				return VoidValue, e
			}
			receiver := regs[callee]
			if callee == registerBase-2 {
				receiver = thisContext
			}
			var value Value
			// The global eval function is a VM hook rather than a real member,
			// so only the direct-call form binds it here.
			if in.Opcode == 100 && name == "eval" && evaluate != nil {
				if len(callArgs) != 0 {
					value, e = evaluate(callArgs[0].Text(), thisContext, result != 0)
				}
				if e != nil {
					return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: CALLD %q: %w", object, in.Offset, name, e)
				}
				if result != 0 {
					regs[result] = value
				}
			} else {
				function, e := read(receiver, name, MemberMustExist)
				if e != nil && callee == registerBase-2 && errors.Is(e, ErrMemberNotFound) {
					function, e = read(global, name, MemberMustExist)
				}
				if e != nil {
					return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: %s %q lookup: %w", object, in.Offset, in.Name, name, e)
				}
				if invoke != nil {
					value, e = invoke(function, receiver, callArgs, result != 0)
				} else {
					context := ObjectRef{}
					if receiver.Kind() == KindObject && !receiver.IsNull() {
						context = receiver.ObjectRef()
					}
					value, e = dispatcher.CallWithContext(function, context, callArgs...)
				}
				if e != nil {
					return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: %s %q: %w", object, in.Offset, in.Name, name, e)
				}
				if result != 0 {
					regs[result] = value
				}
			}
		default:
			return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: unsupported %s opcode %d", object, in.Offset, in.Name, in.Opcode)
		}
		ip = nextIP
	}
	return VoidValue, nil
}
