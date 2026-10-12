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
	return d.executeLegacyWithRuntime(object, -1, global, global, nil, evaluate, invoke, nil, nil, nil, tables...)
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
func (d *DecodedBytecode) ExecuteLegacyWithContextAndConstructionAndPropertyResolver(object int, global, thisContext Value, evaluate func(string, Value, bool) (Value, error), invoke func(Value, Value, []Value, bool) (Value, error), construct func(Value, []Value) (Value, error), resolve func(Value, string) (Value, bool, error), tables ...*ObjectTable) (Value, error) {
	return d.executeLegacyWithRuntime(object, -1, global, thisContext, nil, evaluate, invoke, construct, resolve, nil, tables...)
}

// ExecuteLegacyWithContextAndConstructionAndArguments executes a script function
// with the supplied positional arguments in its legacy argument registers.
func (d *DecodedBytecode) ExecuteLegacyWithContextAndConstructionAndArguments(object int, global, thisContext Value, args []Value, evaluate func(string, Value, bool) (Value, error), invoke func(Value, Value, []Value, bool) (Value, error), construct func(Value, []Value) (Value, error), tables ...*ObjectTable) (Value, error) {
	return d.ExecuteLegacyWithContextAndConstructionAndArgumentsAndPropertyResolver(object, global, thisContext, args, evaluate, invoke, construct, nil, tables...)
}

// ExecuteLegacyWithContextAndConstructionAndArgumentsAndPropertyResolver executes
// a script function with VM-aware property reads.
func (d *DecodedBytecode) ExecuteLegacyWithContextAndConstructionAndArgumentsAndPropertyResolver(object int, global, thisContext Value, args []Value, evaluate func(string, Value, bool) (Value, error), invoke func(Value, Value, []Value, bool) (Value, error), construct func(Value, []Value) (Value, error), resolve func(Value, string) (Value, bool, error), tables ...*ObjectTable) (Value, error) {
	return d.executeLegacyWithRuntime(object, -1, global, thisContext, args, evaluate, invoke, construct, resolve, nil, tables...)
}

// ExecuteLegacyAtOffset executes one deferred expression at a decoded code-unit
// boundary while preserving its parent object's constants and register layout.
func (d *DecodedBytecode) ExecuteLegacyAtOffset(object, startOffset int, global, thisContext Value, evaluate func(string, Value, bool) (Value, error), invoke func(Value, Value, []Value, bool) (Value, error), construct func(Value, []Value) (Value, error), resolve func(Value, string) (Value, bool, error), tables ...*ObjectTable) (Value, error) {
	return d.executeLegacyWithRuntime(object, startOffset, global, thisContext, nil, evaluate, invoke, construct, resolve, nil, tables...)
}

// ExecuteLegacyWithContextAndConstructionAndMemberResolvers routes member
// reads and writes through the owning VM while retaining the legacy dispatcher
// for operations the VM does not implement.
func (d *DecodedBytecode) ExecuteLegacyWithContextAndConstructionAndMemberResolvers(object int, global, thisContext Value, evaluate func(string, Value, bool) (Value, error), invoke func(Value, Value, []Value, bool) (Value, error), construct func(Value, []Value) (Value, error), resolve func(Value, string) (Value, bool, error), set func(Value, string, Value, uint32) error, tables ...*ObjectTable) (Value, error) {
	return d.executeLegacyWithRuntime(object, -1, global, thisContext, nil, evaluate, invoke, construct, resolve, set, tables...)
}

// ExecuteLegacyWithContextAndConstructionAndArgumentsAndMemberResolvers is
// the argument-preserving counterpart of ExecuteLegacyWithContextAndConstructionAndMemberResolvers.
func (d *DecodedBytecode) ExecuteLegacyWithContextAndConstructionAndArgumentsAndMemberResolvers(object int, global, thisContext Value, args []Value, evaluate func(string, Value, bool) (Value, error), invoke func(Value, Value, []Value, bool) (Value, error), construct func(Value, []Value) (Value, error), resolve func(Value, string) (Value, bool, error), set func(Value, string, Value, uint32) error, tables ...*ObjectTable) (Value, error) {
	return d.executeLegacyWithRuntime(object, -1, global, thisContext, args, evaluate, invoke, construct, resolve, set, tables...)
}

// ExecuteLegacyAtOffsetAndMemberResolvers executes a deferred expression at
// an instruction boundary with VM-aware member reads and writes.
func (d *DecodedBytecode) ExecuteLegacyAtOffsetAndMemberResolvers(object, startOffset int, global, thisContext Value, evaluate func(string, Value, bool) (Value, error), invoke func(Value, Value, []Value, bool) (Value, error), construct func(Value, []Value) (Value, error), resolve func(Value, string) (Value, bool, error), set func(Value, string, Value, uint32) error, tables ...*ObjectTable) (Value, error) {
	return d.executeLegacyWithRuntime(object, startOffset, global, thisContext, nil, evaluate, invoke, construct, resolve, set, tables...)
}
func (d *DecodedBytecode) executeLegacyWithRuntime(object int, startOffset int, global, thisContext Value, args []Value, evaluate func(string, Value, bool) (Value, error), invoke func(Value, Value, []Value, bool) (Value, error), construct func(Value, []Value) (Value, error), resolve func(Value, string) (Value, bool, error), set func(Value, string, Value, uint32) error, tables ...*ObjectTable) (Value, error) {
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
		if resolve != nil {
			if value, found, err := resolve(target, name); err != nil || found {
				return value, err
			}
		}
		return dispatcher.Get(target, name, flags)
	}
	readProperty := func(dst, target int, name string, flags uint32, pc int) error {
		if err := propertyTarget(regs[target], pc); err != nil {
			return err
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
			return fmt.Errorf("legacy intercode object %d code offset %d: get property %q: %w", object, pc, name, err)
		}
		regs[dst] = value
		return nil
	}
	writeProperty := func(target int, name string, value Value, flags uint32, pc int) error {
		if err := propertyTarget(regs[target], pc); err != nil {
			return err
		}
		var err error
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
		case 9: // CLT updates the VM comparison flag from ra.GreaterThan(rb).
			left, e := reg(in.Operands[0], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			right, e := reg(in.Operands[1], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			result, e := regs[left].Binary(">", regs[right])
			if e != nil {
				return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: CLT: %w", object, in.Offset, e)
			}
			flag, e = result.Truth()
			if e != nil {
				return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: CLT: %w", object, in.Offset, e)
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
			value := regs[index]
			if value.Kind() == KindString {
				value, e = value.Number()
				if e != nil {
					return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: %s: %w", object, in.Offset, in.Name, e)
				}
			}
			switch value.Kind() {
			case KindVoid:
				if in.Opcode == 18 {
					value = IntegerValue(1)
				} else {
					value = IntegerValue(-1)
				}
			case KindInteger:
				if in.Opcode == 18 {
					value = IntegerValue(value.IntegerValue() + 1)
				} else {
					value = IntegerValue(value.IntegerValue() - 1)
				}
			case KindReal:
				delta := 1.0
				if in.Opcode == 22 {
					delta = -1
				}
				value = RealValue(value.RealValue() + delta)
			default:
				return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: %s: cannot convert TJS %s to Integer or Real", object, in.Offset, in.Name, value.TypeName())
			}
			regs[index] = value
		case 58: // ADD replaces the left register with the TJS addition of both operands.
			left, e := reg(in.Operands[0], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			right, e := reg(in.Operands[1], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			regs[left], e = regs[left].Binary("+", regs[right])
			if e != nil {
				return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: ADD: %w", object, in.Offset, e)
			}
		case 62: // SUB replaces the left register with the TJS subtraction of both operands.
			left, e := reg(in.Operands[0], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			right, e := reg(in.Operands[1], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			regs[left], e = regs[left].Binary("-", regs[right])
			if e != nil {
				return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: SUB: %w", object, in.Offset, e)
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
			if err := table.AddClassInstanceMember(regs[target].ObjectRef().Object, name); err != nil {
				return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: ADDCI: %w", object, in.Offset, err)
			}
		case 126: // REGMEMBER enables the registered class members for instances.
			if e := propertyTarget(regs[registerBase-1], in.Offset); e != nil {
				return VoidValue, e
			}
			if err := table.RegisterClassInstanceMembers(regs[registerBase-1].ObjectRef().Object); err != nil {
				return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: REGMEMBER: %w", object, in.Offset, err)
			}
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
		case 102: // NEW ra, ra, count, args -- construct a class/native object.
			result, e := reg(in.Operands[0], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			callee, e := reg(in.Operands[1], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			count := int(in.Operands[2])
			var args []Value
			switch count {
			case -1: // Reuse caller arguments; top-level execution has none.
			case -2: // Explicit arguments with spread descriptors.
				if len(in.Operands) < 4 {
					return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: truncated NEW argument descriptors", object, in.Offset)
				}
				written := int(in.Operands[3])
				if written < 0 || len(in.Operands) != 4+written*2 {
					return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: invalid NEW argument descriptor count %d", object, in.Offset, written)
				}
				for i := range written {
					register, e := reg(in.Operands[4+i*2], in.Offset)
					if e != nil {
						return VoidValue, e
					}
					switch in.Operands[5+i*2] {
					case 0:
						args = append(args, regs[register])
					case 1:
						array := regs[register]
						if array.Kind() != KindObject || array.IsNull() {
							return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: NEW spread argument is %s, not an array", object, in.Offset, array.TypeName())
						}
						length, e := dispatcher.Get(array, "count")
						if e != nil {
							return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: NEW spread argument count: %w", object, in.Offset, e)
						}
						n, e := length.Int()
						if e != nil || n < 0 || n > 1024-int64(len(args)) {
							return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: invalid NEW spread argument count %s", object, in.Offset, length.Text())
						}
						for j := range n {
							value, e := dispatcher.Get(array, fmt.Sprint(j))
							if e != nil {
								return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: NEW spread argument %d: %w", object, in.Offset, j, e)
							}
							args = append(args, value)
						}
					case 2: // fatUnnamedExpand; top-level caller has no arguments.
					default:
						return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: invalid NEW argument type %d", object, in.Offset, in.Operands[5+i*2])
					}
					if len(args) > 1024 {
						return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: too many NEW arguments", object, in.Offset)
					}
				}
			default:
				if count < 0 || len(in.Operands) != 3+count {
					return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: invalid NEW argument count %d", object, in.Offset, count)
				}
				if count > 1024 {
					return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: too many NEW arguments", object, in.Offset)
				}
				args = make([]Value, 0, count)
				for _, operand := range in.Operands[3:] {
					register, e := reg(operand, in.Offset)
					if e != nil {
						return VoidValue, e
					}
					args = append(args, regs[register])
				}
			}
			if len(args) > 1024 {
				return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: too many NEW arguments", object, in.Offset)
			}
			if construct == nil {
				return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: NEW requires VM construction support", object, in.Offset)
			}
			value, e := construct(regs[callee], args)
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
			count := int(in.Operands[2])
			var args []Value
			switch count {
			case -1: // Reuse caller arguments; top-level execution has none.
			case -2: // Explicit arguments with spread descriptors.
				if len(in.Operands) < 4 {
					return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: truncated CALL argument descriptors", object, in.Offset)
				}
				written := int(in.Operands[3])
				if written < 0 || len(in.Operands) != 4+written*2 {
					return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: invalid CALL argument descriptor count %d", object, in.Offset, written)
				}
				for i := 0; i < written; i++ {
					register, e := reg(in.Operands[4+i*2], in.Offset)
					if e != nil {
						return VoidValue, e
					}
					switch in.Operands[5+i*2] {
					case 0: // fatNormal
						args = append(args, regs[register])
					case 1: // fatExpand
						array := regs[register]
						if array.Kind() != KindObject || array.IsNull() {
							return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: CALL spread argument is %s, not an array", object, in.Offset, array.TypeName())
						}
						length, e := dispatcher.Get(array, "count")
						if e != nil {
							return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: CALL spread argument count: %w", object, in.Offset, e)
						}
						n, e := length.Int()
						if e != nil || n < 0 || n > 1024-int64(len(args)) {
							return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: invalid CALL spread argument count %s", object, in.Offset, length.Text())
						}
						for j := int64(0); j < n; j++ {
							value, e := dispatcher.Get(array, fmt.Sprint(j))
							if e != nil {
								return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: CALL spread argument %d: %w", object, in.Offset, j, e)
							}
							args = append(args, value)
						}
					case 2: // fatUnnamedExpand; top-level execution has no caller arguments.
					default:
						return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: invalid CALL argument type %d", object, in.Offset, in.Operands[5+i*2])
					}
					if len(args) > 1024 {
						return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: too many CALL arguments", object, in.Offset)
					}
				}
			default:
				if count < 0 || len(in.Operands) != 3+count {
					return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: invalid CALL argument count %d", object, in.Offset, count)
				}
				if count > 1024 {
					return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: too many CALL arguments", object, in.Offset)
				}
				args = make([]Value, 0, count)
				for _, operand := range in.Operands[3:] {
					register, e := reg(operand, in.Offset)
					if e != nil {
						return VoidValue, e
					}
					args = append(args, regs[register])
				}
			}
			if len(args) > 1024 {
				return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: too many CALL arguments", object, in.Offset)
			}
			receiver := regs[registerBase-1]
			var value Value
			if invoke != nil {
				value, e = invoke(regs[callee], receiver, args, result != 0)
			} else {
				context := ObjectRef{}
				if receiver.Kind() == KindObject && !receiver.IsNull() {
					context = receiver.ObjectRef()
				}
				value, e = dispatcher.CallWithContext(regs[callee], context, args...)
			}
			if e != nil {
				return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: CALL %s: %w", object, in.Offset, regs[callee].TypeName(), e)
			}
			if result != 0 {
				regs[result] = value
			}
		case 100: // CALLD -- invoke a callable value with explicit direct-call arguments.
			result, e := reg(in.Operands[0], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			callee, e := reg(in.Operands[1], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			name, e := directName(in.Operands[2], in.Offset)
			if e != nil {
				return VoidValue, e
			}
			count := int(in.Operands[3])
			var args []Value
			switch count {
			case -1: // Reuse caller arguments; top-level execution has none.
			case -2: // Explicit arguments with spread descriptors.
				if len(in.Operands) < 5 {
					return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: truncated CALLD argument descriptors", object, in.Offset)
				}
				written := int(in.Operands[4])
				if written < 0 || len(in.Operands) != 5+written*2 {
					return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: invalid CALLD argument descriptor count %d", object, in.Offset, written)
				}
				for i := 0; i < written; i++ {
					register, e := reg(in.Operands[5+i*2], in.Offset)
					if e != nil {
						return VoidValue, e
					}
					switch in.Operands[6+i*2] {
					case 0: // fatNormal
						args = append(args, regs[register])
					case 1: // fatExpand
						array := regs[register]
						if array.Kind() != KindObject || array.IsNull() {
							return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: CALLD spread argument is %s, not an array", object, in.Offset, array.TypeName())
						}
						length, e := dispatcher.Get(array, "count")
						if e != nil {
							return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: CALLD spread argument count: %w", object, in.Offset, e)
						}
						n, e := length.Int()
						if e != nil || n < 0 || n > 1024-int64(len(args)) {
							return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: invalid CALLD spread argument count %s", object, in.Offset, length.Text())
						}
						for j := int64(0); j < n; j++ {
							value, e := dispatcher.Get(array, fmt.Sprint(j))
							if e != nil {
								return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: CALLD spread argument %d: %w", object, in.Offset, j, e)
							}
							args = append(args, value)
						}
					case 2: // fatUnnamedExpand; no caller arguments at top level.
					default:
						return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: invalid CALLD argument type %d", object, in.Offset, in.Operands[6+i*2])
					}
					if len(args) > 1024 {
						return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: too many CALLD arguments", object, in.Offset)
					}
				}
			default:
				if count < 0 || len(in.Operands) != 4+count {
					return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: invalid CALLD argument count %d", object, in.Offset, count)
				}
				args = make([]Value, 0, count)
				for _, operand := range in.Operands[4:] {
					register, e := reg(operand, in.Offset)
					if e != nil {
						return VoidValue, e
					}
					args = append(args, regs[register])
				}
			}
			if len(args) > 1024 {
				return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: too many CALLD arguments", object, in.Offset)
			}
			receiver := regs[callee]
			var value Value
			if name == "eval" && evaluate != nil {
				if len(args) != 0 {
					value, e = evaluate(args[0].Text(), thisContext, result != 0)
				}
				if e != nil {
					return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: CALLD %q: %w", object, in.Offset, name, e)
				}
				if result != 0 {
					regs[result] = value
				}
			} else {
				function, e := read(receiver, name, MemberMustExist)
				if e != nil {
					return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: CALLD %q lookup: %w", object, in.Offset, name, e)
				}
				if invoke != nil {
					value, e = invoke(function, receiver, args, result != 0)
				} else {
					context := ObjectRef{}
					if receiver.Kind() == KindObject && !receiver.IsNull() {
						context = receiver.ObjectRef()
					}
					value, e = dispatcher.CallWithContext(function, context, args...)
				}
				if e != nil {
					return VoidValue, fmt.Errorf("legacy intercode object %d code offset %d: CALLD %q: %w", object, in.Offset, name, e)
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
