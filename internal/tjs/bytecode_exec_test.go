package tjs

import (
	"errors"
	"math"
	"strings"
	"testing"
)

func TestExecuteLegacyNFNegatesFlag(t *testing.T) {
	bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
		ArgumentCount: 1,
		Code: []int16{
			7, -3, -3, // CEQ sets the flag true.
			14,    // NF negates only the flag.
			11, 0, // SETF stores the inverted flag in r0.
			118, 0, 119, // Return r0.
		},
	}}}
	instructions, err := bytecode.DecodeLegacyInstructions(0)
	if err != nil {
		t.Fatal(err)
	}
	if instructions[1].Opcode != 14 || instructions[1].Offset != 3 || len(instructions[1].Operands) != 0 ||
		instructions[2].Opcode != 11 || instructions[2].Offset != 4 {
		t.Fatalf("decoded NF sequence = %#v; want one-word NF then SETF", instructions[1:3])
	}

	got, err := bytecode.ExecuteLegacyWithContextAndConstructionAndArguments(0, VoidValue, VoidValue, []Value{IntegerValue(9)}, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !got.StrictEqual(IntegerValue(0)) {
		t.Fatalf("NF result = %v (kind %v), want integer 0", got, got.Kind())
	}
}

func TestExecuteLegacyDELDResultAndDeletion(t *testing.T) {
	for _, test := range []struct {
		name       string
		hasMember  bool
		wantResult Value
	}{
		{name: "existing member", hasMember: true, wantResult: IntegerValue(1)},
		{name: "missing member", wantResult: IntegerValue(0)},
	} {
		t.Run(test.name, func(t *testing.T) {
			table := NewObjectTable()
			object := table.New(CategoryDictionary)
			if test.hasMember {
				if err := object.Set("x", IntegerValue(7)); err != nil {
					t.Fatal(err)
				}
			}
			bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
				ArgumentCount: 1,
				Constants:     []BytecodeConstant{{Value: StringValue("x")}},
				Code: []int16{
					116, 1, -3, 0, // DELD r1, argument object, constant "x"
					118, 1, 119, // Return the delete result.
				},
			}}}
			got, err := bytecode.ExecuteLegacyWithContextAndConstructionAndArguments(
				0, VoidValue, VoidValue, []Value{ObjectValue(object.Ref())}, nil, nil, nil, table,
			)
			if err != nil {
				t.Fatal(err)
			}
			if !got.StrictEqual(test.wantResult) {
				t.Fatalf("DELD result = %v, want %v", got, test.wantResult)
			}
			_, stillPresent := object.Get("x")
			if stillPresent {
				t.Fatalf("member is still present after DELD")
			}
		})
	}
}

func TestExecuteLegacyNUMConversion(t *testing.T) {
	for _, test := range []struct {
		name     string
		input    Value
		kind     ValueKind
		wantInt  int64
		wantReal float64
		isNaN    bool
	}{
		{name: "integer string", input: StringValue("42tail"), kind: KindInteger, wantInt: 42},
		{name: "real string", input: StringValue("2.5tail"), kind: KindReal, wantReal: 2.5},
		{name: "nan string", input: StringValue("NaN"), kind: KindReal, isNaN: true},
		{name: "void", input: VoidValue, kind: KindInteger, wantInt: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			table := NewObjectTable()
			global := table.New(CategoryDictionary)
			bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
				MaxVariables: 2,
				Code:         []int16{1, 1, 1, 91, 1, 104, -1, 0, 1, 119},
				Constants:    []BytecodeConstant{{Value: StringValue("result")}, {Value: test.input}},
			}}}
			if _, err := bytecode.ExecuteLegacy(0, ObjectValue(global.Ref()), table); err != nil {
				t.Fatal(err)
			}
			got, ok := global.Get("result")
			if !ok || got.Kind() != test.kind {
				t.Fatalf("NUM result = %v (kind %v), present %v; want kind %v", got, got.Kind(), ok, test.kind)
			}
			if test.kind == KindInteger && got.IntegerValue() != test.wantInt {
				t.Fatalf("NUM integer = %d, want %d", got.IntegerValue(), test.wantInt)
			}
			if test.isNaN {
				n, err := got.Float()
				if err != nil || !math.IsNaN(n) {
					t.Fatalf("NUM NaN = %v, %v", n, err)
				}
			} else if test.kind == KindReal {
				n, err := got.Float()
				if err != nil || n != test.wantReal {
					t.Fatalf("NUM real = %v, %v; want %v", n, err, test.wantReal)
				}
			}
		})
	}
}
func TestExecuteLegacyINCDEC(t *testing.T) {
	for _, test := range []struct {
		name  string
		op    int16
		input Value
		want  Value
	}{
		{name: "INC integer", op: 18, input: IntegerValue(4), want: IntegerValue(5)},
		{name: "DEC integer", op: 22, input: IntegerValue(4), want: IntegerValue(3)},
		{name: "INC real", op: 18, input: RealValue(1.5), want: RealValue(2.5)},
		{name: "DEC real", op: 22, input: RealValue(1.5), want: RealValue(0.5)},
		{name: "INC numeric string", op: 18, input: StringValue("4.5tail"), want: RealValue(5.5)},
		{name: "DEC numeric string", op: 22, input: StringValue("4tail"), want: IntegerValue(3)},
		{name: "INC void", op: 18, input: VoidValue, want: IntegerValue(1)},
		{name: "DEC void", op: 22, input: VoidValue, want: IntegerValue(-1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
				ArgumentCount: 1,
				Code: []int16{
					test.op, -3, // INC/DEC the first argument register in place
					118, -3, // SRV the updated argument
					119,
				},
			}}}
			got, err := bytecode.ExecuteLegacyWithContextAndConstructionAndArguments(0, VoidValue, VoidValue, []Value{test.input}, nil, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !got.StrictEqual(test.want) {
				t.Fatalf("%s result = %v (kind %v), want %v (kind %v)", test.name, got, got.Kind(), test.want, test.want.Kind())
			}
		})
	}
}

func TestExecuteLegacySUB(t *testing.T) {
	for _, test := range []struct {
		name  string
		left  Value
		right Value
		want  Value
	}{
		{name: "integer", left: IntegerValue(9), right: IntegerValue(4), want: IntegerValue(5)},
		{name: "real", left: RealValue(4.5), right: RealValue(1.25), want: RealValue(3.25)},
		{name: "numeric strings", left: StringValue("9"), right: StringValue("4"), want: IntegerValue(5)},
	} {
		t.Run(test.name, func(t *testing.T) {
			bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
				MaxVariables: 2,
				Code:         []int16{1, 0, 0, 1, 1, 1, 62, 0, 1, 118, 0, 119},
				Constants: []BytecodeConstant{
					{Value: test.left},
					{Value: test.right},
				},
			}}}
			got, err := bytecode.ExecuteLegacy(0, VoidValue)
			if err != nil {
				t.Fatal(err)
			}
			if !got.StrictEqual(test.want) {
				t.Fatalf("SUB result = %v (kind %v), want %v (kind %v)", got, got.Kind(), test.want, test.want.Kind())
			}
		})
	}
}

func TestExecuteLegacySUBConversionErrorContext(t *testing.T) {
	bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
		MaxVariables: 2,
		Code:         []int16{1, 0, 0, 1, 1, 1, 62, 0, 1, 119},
		Constants: []BytecodeConstant{
			{Value: ObjectValue(NewObjectRef(1, 0))},
			{Value: IntegerValue(1)},
		},
	}}}
	_, err := bytecode.ExecuteLegacy(0, VoidValue)
	if err == nil || !strings.Contains(err.Error(), "legacy intercode object 0 code offset 6: SUB: cannot convert TJS Object to number") {
		t.Fatalf("SUB error = %v, want conversion error with legacy bytecode location", err)
	}
}

func TestExecuteLegacyINCDECConversionErrorContext(t *testing.T) {
	for _, op := range []int16{18, 22} {
		bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
			MaxVariables: 1,
			Code:         []int16{1, 0, 0, op, 0, 118, 0, 119},
			Constants:    []BytecodeConstant{{Value: ObjectValue(NewObjectRef(1, 0))}},
		}}}
		_, err := bytecode.ExecuteLegacy(0, VoidValue)
		name := legacyOpcodeNames[op]
		if err == nil || !strings.Contains(err.Error(), "legacy intercode object 0 code offset 3: "+name+": cannot convert TJS Object to Integer or Real") {
			t.Errorf("%s error = %v", name, err)
		}
	}
}
func TestExecuteLegacySRVReturnsSelectedRegister(t *testing.T) {
	table := NewObjectTable()
	global := table.New(CategoryDictionary)
	bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
		MaxVariables: 2,
		Code:         []int16{1, 1, 0, 118, 1, 119},
		Constants:    []BytecodeConstant{{Value: StringValue("returned")}},
	}}}

	got, err := bytecode.ExecuteLegacy(0, ObjectValue(global.Ref()), table)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind() != KindString || got.Text() != "returned" {
		t.Fatalf("SRV return = %v (kind %v), want string %q", got, got.Kind(), "returned")
	}
}
func TestExecuteLegacyADD(t *testing.T) {
	for _, test := range []struct {
		name        string
		left, right Value
		want        Value
	}{
		{name: "integers", left: IntegerValue(7), right: IntegerValue(9), want: IntegerValue(16)},
		{name: "reals", left: RealValue(1.25), right: RealValue(2.5), want: RealValue(3.75)},
		{name: "string concatenation", left: StringValue("island"), right: StringValue(" diary"), want: StringValue("island diary")},
		{name: "mixed string and integer", left: IntegerValue(7), right: StringValue(" days"), want: StringValue("7 days")},
	} {
		t.Run(test.name, func(t *testing.T) {
			table := NewObjectTable()
			global := table.New(CategoryDictionary)
			bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
				MaxVariables: 2,
				Code:         []int16{1, 0, 0, 1, 1, 1, 58, 0, 1, 104, -1, 2, 0, 119},
				Constants: []BytecodeConstant{
					{Value: test.left},
					{Value: test.right},
					{Value: StringValue("result")},
				},
			}}}
			if _, err := bytecode.ExecuteLegacy(0, ObjectValue(global.Ref()), table); err != nil {
				t.Fatal(err)
			}
			got, ok := global.Get("result")
			if !ok || !got.StrictEqual(test.want) {
				t.Fatalf("ADD result = %v (kind %v), present %v; want %v (kind %v)", got, got.Kind(), ok, test.want, test.want.Kind())
			}
		})
	}
}
func TestExecuteLegacyCLClearsRegister(t *testing.T) {
	table := NewObjectTable()
	global := table.New(CategoryDictionary)
	bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
		MaxVariables: 2,
		Code: []int16{
			1, 0, 1, // CONST r0, initial value
			3, 0, // CL r0
			5, 0, // TT r0
			11, 1, // SETF r1
			104, -1, 0, 1, // SPD global, "result", r1
			119, // RET
		},
		Constants: []BytecodeConstant{
			{Value: StringValue("result")},
			{Value: IntegerValue(7)},
		},
	}}}
	if _, err := bytecode.ExecuteLegacy(0, ObjectValue(global.Ref()), table); err != nil {
		t.Fatal(err)
	}
	got, ok := global.Get("result")
	if !ok || !got.StrictEqual(IntegerValue(0)) {
		t.Fatalf("CL result = %v (kind %v), present %v; want integer 0", got, got.Kind(), ok)
	}
}

func TestExecuteLegacyCPRegisterCopy(t *testing.T) {
	table := NewObjectTable()
	global := table.New(CategoryDictionary)
	child := table.New(CategoryDictionary)
	context := table.New(CategoryDictionary)
	tests := []struct {
		name  string
		value Value
	}{
		{name: "void", value: VoidValue},
		{name: "integer", value: IntegerValue(42)},
		{name: "real", value: RealValue(2.5)},
		{name: "string", value: StringValue("copied")},
		{name: "octet", value: OctetValue([]byte{1, 2, 255})},
		{name: "object reference", value: ObjectValue(NewObjectRef(child.ID, context.ID))},
	}
	object := BytecodeObject{MaxVariables: 2}
	for _, test := range tests {
		nameIndex := int16(len(object.Constants))
		object.Constants = append(object.Constants, BytecodeConstant{Value: StringValue(test.name)})
		valueIndex := int16(len(object.Constants))
		object.Constants = append(object.Constants, BytecodeConstant{Value: test.value})
		object.Code = append(object.Code,
			1, 0, valueIndex, // CONST r0, value
			2, 1, 0, // CP r1, r0
			104, -1, nameIndex, 1, // SPD global, name, r1
		)
	}
	object.Code = append(object.Code, 119)
	bytecode := &DecodedBytecode{Objects: []BytecodeObject{object}}
	if _, err := bytecode.ExecuteLegacy(0, ObjectValue(global.Ref()), table); err != nil {
		t.Fatal(err)
	}
	for _, test := range tests {
		got, ok := global.Get(test.name)
		if !ok || !got.StrictEqual(test.value) {
			t.Errorf("CP %s result = %v (kind %v), present %v; want %v (kind %v)", test.name, got, got.Kind(), ok, test.value, test.value.Kind())
		}
	}
}

func TestExecuteLegacyContextProxyLookupAndCallReceiver(t *testing.T) {
	table := NewObjectTable()
	global := table.New(CategoryDictionary)
	context := table.New(CategoryDictionary)
	if err := global.Set("globalOnly", IntegerValue(73)); err != nil {
		t.Fatal(err)
	}
	var called bool
	method := NewNativeFunction(table, "contextMethod", func(receiver ObjectRef, _ []Value) (Value, error) {
		if receiver.Object != context.ID {
			t.Errorf("native method receiver = %d, want context %d", receiver.Object, context.ID)
		}
		called = true
		return VoidValue, nil
	})
	if err := context.Set("method", method); err != nil {
		t.Fatal(err)
	}
	bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
		MaxVariables: 2,
		Code: []int16{
			103, 2, -2, 0, // GPD r2, this-proxy, globalOnly
			124, 1, // GLOBAL r1
			104, 1, 1, 2, // SPD global, result, r2
			103, 1, -2, 2, // GPD r1, this-proxy, method
			99, 0, 1, 0, // CALL (discarded result), r1, 0 args
			119,
		},
		Constants: []BytecodeConstant{
			{Value: StringValue("globalOnly")},
			{Value: StringValue("result")},
			{Value: StringValue("method")},
		},
	}}}
	invoke := func(function, receiver Value, _ []Value, _ bool) (Value, error) {
		if function.Kind() != KindObject || function.ObjectRef().Context != context.ID {
			t.Errorf("callee = %v, want method bound to context %d", function, context.ID)
		}
		if receiver.Kind() != KindObject || receiver.ObjectRef().Object != context.ID {
			t.Errorf("receiver = %v, want context %d", receiver, context.ID)
		}
		called = true
		return VoidValue, nil
	}
	_, err := bytecode.ExecuteLegacyWithContextAndConstruction(
		0, ObjectValue(global.Ref()), ObjectValue(context.Ref()), nil, invoke, nil, table,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("context method was not invoked")
	}
	if got, ok := global.Get("result"); !ok || !got.StrictEqual(IntegerValue(73)) {
		t.Fatalf("global fallback result = %v, present %t; want 73", got, ok)
	}
}

func TestExecuteLegacyPropertyResolverHandlesVMClassProperties(t *testing.T) {
	table := NewObjectTable()
	global := table.New(CategoryDictionary)
	target := table.New(CategoryClass)
	bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
		MaxVariables: 2,
		Code:         []int16{1, 0, 0, 103, 1, 0, 1, 104, -1, 2, 1, 119},
		Constants: []BytecodeConstant{
			{Value: ObjectValue(target.Ref())},
			{Value: StringValue("biMinimize")},
			{Value: StringValue("result")},
		},
	}}}
	resolved := false
	resolve := func(receiver Value, name string, flags uint32) (Value, bool, error) {
		if receiver.StrictEqual(ObjectValue(target.Ref())) && name == "biMinimize" {
			resolved = true
			return IntegerValue(3), true, nil
		}
		return VoidValue, false, nil
	}
	if _, err := bytecode.ExecuteLegacyWithContextAndConstructionAndPropertyResolver(
		0, ObjectValue(global.Ref()), ObjectValue(global.Ref()), nil, nil, nil, resolve, table,
	); err != nil {
		t.Fatal(err)
	}
	if !resolved {
		t.Fatal("legacy property resolver was not called")
	}
	if got, ok := global.Get("result"); !ok || !got.StrictEqual(IntegerValue(3)) {
		t.Fatalf("compiled property result = %v, present %t; want 3", got, ok)
	}
}

func TestLegacyClassInstanceMembersRegisterAndMaterialize(t *testing.T) {
	vm := NewVM()
	class, err := vm.RegisterLegacyClass("LegacyClassInfoTest")
	if err != nil {
		t.Fatal(err)
	}
	bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
		Constants: []BytecodeConstant{
			{Value: StringValue("value")},
			{Value: IntegerValue(17)},
		},
		Code: []int16{
			1, 1, 0, // CONST r1, "value"
			125, -1, 1, // ADDCI this, r1
			126,     // REGMEMBER
			1, 2, 1, // CONST r2, 17
			111, -1, 0, 2, // SPDS this, "value", r2
			119, // RET
		},
	}}}
	global := ObjectValue(NewObjectRef(0, 0))
	if _, err := bytecode.ExecuteLegacyWithContextAndConstructionAndPropertyResolver(
		0, global, class, nil, nil, nil, nil, vm.Objects,
	); err != nil {
		t.Fatal(err)
	}
	if names, registered := vm.Objects.ClassInstanceMembers(class.ObjectRef().Object); !registered || len(names) != 1 || names[0] != "value" {
		t.Fatalf("registered class instance members = %v, %t; want [value], true", names, registered)
	}
	instance, err := vm.construct(class, nil, nopHost{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	value, ok := vm.Objects.Lookup(instance.ObjectRef().Object)
	if !ok {
		t.Fatal("constructed instance missing")
	}
	if got, present := value.Get("value"); !present || !got.StrictEqual(IntegerValue(17)) {
		t.Fatalf("instance value = %v, %t; want integer 17", got, present)
	}
}

func TestExecuteLegacyResolvesNativeStaticPropertyOnConstructorMethod(t *testing.T) {
	vm := NewVM()
	class, err := vm.RegisterNativeClass("LegacyStatic")
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.RegisterNativeMethod(class, "LegacyStatic", "LegacyStatic.LegacyStatic"); err != nil {
		t.Fatal(err)
	}
	if err := vm.RegisterNativeStaticProperty(class, "bitmap", "", ""); err != nil {
		t.Fatal(err)
	}
	constructor, err := vm.GetMember(class, "LegacyStatic", vmTestHost{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	globalObject, ok := vm.Objects.Lookup(0)
	if !ok {
		t.Fatal("VM global object is missing")
	}
	target := constructor
	bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
		MaxVariables: 2,
		Code:         []int16{1, 0, 0, 103, 1, 0, 1, 104, -1, 2, 1, 119},
		Constants: []BytecodeConstant{
			{Value: target},
			{Value: StringValue("bitmap")},
			{Value: StringValue("result")},
		},
	}}}
	resolve := func(receiver Value, name string, flags uint32) (Value, bool, error) {
		if flags&IgnoreProperty != 0 {
			return vm.GetMemberRawIfPresent(receiver, name, vmTestHost{}, nil)
		}
		return vm.GetMemberIfPresent(receiver, name, vmTestHost{}, nil)
	}
	if _, err := bytecode.ExecuteLegacyWithContextAndConstructionAndPropertyResolver(
		0, ObjectValue(NewObjectRef(0, 0)), ObjectValue(NewObjectRef(0, 0)), nil, nil, nil, resolve, vm.Objects,
	); err != nil {
		t.Fatal(err)
	}
	if got, present := globalObject.Get("result"); !present || got.Kind() != KindVoid {
		t.Fatalf("legacy native static property = %v, present %t; want present void", got, present)
	}
}

func TestExecuteLegacyTYPEOFDDirectMember(t *testing.T) {
	for _, test := range []struct {
		name    string
		value   Value
		present bool
		want    string
	}{
		{name: "existing member", value: IntegerValue(42), present: true, want: "Integer"},
		{name: "missing member", want: "undefined"},
	} {
		t.Run(test.name, func(t *testing.T) {
			table := NewObjectTable()
			global := table.New(CategoryDictionary)
			target := table.New(CategoryDictionary)
			if test.present {
				if err := target.Set("value", test.value); err != nil {
					t.Fatal(err)
				}
			}
			bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
				MaxVariables: 2,
				Code:         []int16{1, 0, 0, 84, 1, 0, 1, 104, -1, 2, 1, 119},
				Constants: []BytecodeConstant{
					{Value: ObjectValue(target.Ref())},
					{Value: StringValue("value")},
					{Value: StringValue("result")},
				},
			}}}
			if _, err := bytecode.ExecuteLegacy(0, ObjectValue(global.Ref()), table); err != nil {
				t.Fatal(err)
			}
			got, ok := global.Get("result")
			if !ok || got.Kind() != KindString || got.Text() != test.want {
				t.Fatalf("TYPEOFD result = %v (kind %v), present %v; want %q", got, got.Kind(), ok, test.want)
			}
		})
	}
}
func TestExecuteLegacyTYPEOFInPlace(t *testing.T) {
	table := NewObjectTable()
	object := table.New(CategoryDictionary)
	class := table.New(CategoryClass)
	function := table.New(CategoryFunction)
	tests := []struct {
		name  string
		value Value
		want  string
	}{
		{name: "void", value: VoidValue, want: "void"},
		{name: "null object", value: NullValue, want: "Object"},
		{name: "dictionary", value: ObjectValue(object.Ref()), want: "Object"},
		{name: "class", value: ObjectValue(class.Ref()), want: "Object"},
		{name: "function", value: ObjectValue(function.Ref()), want: "Object"},
		{name: "string", value: StringValue("text"), want: "String"},
		{name: "integer", value: IntegerValue(42), want: "Integer"},
		{name: "real", value: RealValue(4.2), want: "Real"},
		{name: "octet", value: OctetValue([]byte{1, 2}), want: "Octet"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
				Code:      []int16{1, 0, 0, 83, 0, 118, 0, 119},
				Constants: []BytecodeConstant{{Value: test.value}},
			}}}
			got, err := bytecode.ExecuteLegacy(0, VoidValue, table)
			if err != nil {
				t.Fatal(err)
			}
			if got.Kind() != KindString || got.Text() != test.want {
				t.Fatalf("TYPEOF result = %v (kind %v); want string %q", got, got.Kind(), test.want)
			}
		})
	}
}

func TestExecuteLegacySTRConversion(t *testing.T) {
	table := NewObjectTable()
	global := table.New(CategoryDictionary)
	bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
		MaxVariables: 2,
		Code:         []int16{1, 1, 1, 97, 1, 104, -1, 0, 1, 119},
		Constants: []BytecodeConstant{
			{Value: StringValue("result")},
			{Value: IntegerValue(42)},
		},
	}}}
	if _, err := bytecode.ExecuteLegacy(0, ObjectValue(global.Ref()), table); err != nil {
		t.Fatal(err)
	}
	got, ok := global.Get("result")
	if !ok || got.Kind() != KindString || got.Text() != "42" {
		t.Fatalf("STR result = %v (kind %v), present %v; want string %q", got, got.Kind(), ok, "42")
	}
}

func TestExecuteLegacySTROctetConversionErrorContext(t *testing.T) {
	bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
		MaxVariables: 1,
		Code:         []int16{1, 0, 0, 97, 0, 119},
		Constants:    []BytecodeConstant{{Value: OctetValue([]byte{1})}},
	}}}
	_, err := bytecode.ExecuteLegacy(0, VoidValue)
	if err == nil || !strings.Contains(err.Error(), "legacy intercode object 0 code offset 3: STR: cannot convert TJS Octet to String") {
		t.Fatalf("STR error = %v", err)
	}
}

func TestExecuteLegacyCEQAndCDEQFlags(t *testing.T) {
	for _, test := range []struct {
		name        string
		left, right Value
		op          int16
		want        int64
	}{
		{name: "loose string-number equality", left: StringValue("2"), right: IntegerValue(2), op: 7, want: 1},
		{name: "strict string-number inequality", left: StringValue("2"), right: IntegerValue(2), op: 8, want: 0},
		{name: "loose object identity ignores bound context", left: ObjectValue(NewObjectRef(7, 11)), right: ObjectValue(NewObjectRef(7, 12)), op: 7, want: 1},
		{name: "different object identity", left: ObjectValue(NewObjectRef(7, 11)), right: ObjectValue(NewObjectRef(8, 11)), op: 7, want: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			table := NewObjectTable()
			global := table.New(CategoryDictionary)
			bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
				MaxVariables: 4,
				Code:         []int16{1, 1, 0, 1, 2, 1, test.op, 1, 2, 11, 3, 124, 0, 104, 0, 2, 3, 119},
				Constants:    []BytecodeConstant{{Value: test.left}, {Value: test.right}, {Value: StringValue("result")}},
			}}}
			if _, err := bytecode.ExecuteLegacy(0, ObjectValue(global.Ref()), table); err != nil {
				t.Fatal(err)
			}
			got, ok := global.Get("result")
			if !ok || got.Kind() != KindInteger || got.IntegerValue() != test.want {
				t.Fatalf("comparison result = %v (present %v), want integer %d", got, ok, test.want)
			}
		})
	}
}

func TestExecuteLegacyTruthinessOpcodes(t *testing.T) {
	for _, test := range []struct {
		name      string
		code      []int16
		constants []BytecodeConstant
		want      int64
	}{
		{
			name:      "TT reads truthy negative context register",
			code:      []int16{5, -1, 11, 0, 104, -1, 0, 0, 119},
			constants: []BytecodeConstant{{Value: StringValue("result")}},
			want:      1,
		},
		{
			name:      "TF negates false truthiness",
			code:      []int16{1, 0, 0, 6, 0, 11, 1, 104, -1, 1, 1, 119},
			constants: []BytecodeConstant{{Value: IntegerValue(0)}, {Value: StringValue("result")}},
			want:      1,
		},
		{
			name:      "LNOT replaces source value",
			code:      []int16{1, 0, 0, 13, 0, 104, -1, 1, 0, 119},
			constants: []BytecodeConstant{{Value: IntegerValue(0)}, {Value: StringValue("result")}},
			want:      1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			table := NewObjectTable()
			global := table.New(CategoryDictionary)
			bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
				MaxVariables: 2,
				Code:         test.code,
				Constants:    test.constants,
			}}}
			if _, err := bytecode.ExecuteLegacy(0, ObjectValue(global.Ref()), table); err != nil {
				t.Fatal(err)
			}
			got, ok := global.Get("result")
			if !ok || got.Kind() != KindInteger || got.IntegerValue() != test.want {
				t.Fatalf("truthiness result = %v (present %v), want integer %d", got, ok, test.want)
			}
		})
	}
}

func TestExecuteLegacyNegativeArgumentRegister(t *testing.T) {
	table := NewObjectTable()
	global := table.New(CategoryDictionary)
	bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
		MaxVariables: 4,
		Code:         []int16{1, 3, 0, 2, -3, 3, 104, -1, 1, -3, 119},
		Constants: []BytecodeConstant{
			{Value: StringValue("value")},
			{Value: StringValue("result")},
		},
	}}}

	if _, err := bytecode.ExecuteLegacy(0, ObjectValue(global.Ref()), table); err != nil {
		t.Fatal(err)
	}
	got, ok := global.Get("result")
	if !ok || got.Text() != "value" {
		t.Fatalf("negative argument register = %v (present %v), want value", got, ok)
	}
}

func TestExecuteLegacyNUMConversionErrorContext(t *testing.T) {
	bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
		MaxVariables: 2,
		Code:         []int16{1, 1, 1, 91, 1, 119},
		Constants:    []BytecodeConstant{{Value: StringValue("property")}, {Value: ObjectValue(ObjectRef{Object: 1})}},
	}}}
	_, err := bytecode.ExecuteLegacy(0, VoidValue)
	if err == nil || !strings.Contains(err.Error(), "legacy intercode object 0 code offset 3: NUM: cannot convert TJS Object to number") {
		t.Fatalf("NUM error = %v", err)
	}
}
func TestExecuteLegacyConditionalJumps(t *testing.T) {
	for _, test := range []struct {
		name  string
		op    int16
		truth bool
		want  int64
	}{
		{name: "JNF takes false branch", op: 16, truth: false, want: 22},
		{name: "JNF skips true branch", op: 16, truth: true, want: 11},
		{name: "JF takes true branch", op: 15, truth: true, want: 22},
		{name: "JF skips false branch", op: 15, truth: false, want: 11},
	} {
		t.Run(test.name, func(t *testing.T) {
			table := NewObjectTable()
			global := table.New(CategoryDictionary)
			source := IntegerValue(0)
			if test.truth {
				source = IntegerValue(1)
			}
			bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
				MaxVariables: 2,
				Code:         []int16{1, 0, 0, 5, 0, test.op, 7, 1, 1, 1, 17, 5, 1, 1, 2, 104, -1, 3, 1, 119},
				Constants: []BytecodeConstant{
					{Value: source},
					{Value: IntegerValue(11)},
					{Value: IntegerValue(22)},
					{Value: StringValue("result")},
				},
			}}}
			if _, err := bytecode.ExecuteLegacy(0, ObjectValue(global.Ref()), table); err != nil {
				t.Fatal(err)
			}
			got, ok := global.Get("result")
			if !ok || got.Kind() != KindInteger || got.IntegerValue() != test.want {
				t.Fatalf("branch result = %v (present %v), want %d", got, ok, test.want)
			}
		})
	}
}
func TestDecodeLegacyINCInstructionBoundaries(t *testing.T) {
	bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
		Code: []int16{
			18, 2, // INC r2
			113, 1, 2, 0, // SPIS r1, r2, r0
			18, 2, // INC r2
			2, -4, 1, // CP r-4, r1
			1, 1, 1, // CONST r1, constant 1
			119, // RET
		},
	}}}
	instructions, err := bytecode.DecodeLegacyInstructions(0)
	if err != nil {
		t.Fatal(err)
	}
	wantOffsets := []int{0, 2, 6, 8, 11, 14}
	if len(instructions) != len(wantOffsets) {
		t.Fatalf("decoded %d instructions, want %d: %#v", len(instructions), len(wantOffsets), instructions)
	}
	for i, want := range wantOffsets {
		if instructions[i].Offset != want {
			t.Fatalf("instruction %d offset = %d, want %d (%s)", i, instructions[i].Offset, want, instructions[i].Name)
		}
	}
	if instructions[0].Name != "INC" || instructions[2].Name != "INC" ||
		instructions[3].Name != "CP" || instructions[3].Operands[0] != -4 {
		t.Fatalf("decoded INC/CP operands = %#v, want unary INCs and CP r-4, r1", instructions[:4])
	}
}
func TestDecodeAndExecuteLegacyNEW(t *testing.T) {
	table := NewObjectTable()
	global := table.New(CategoryDictionary)
	bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
		MaxVariables: 3,
		Code: []int16{
			1, 0, 0, // CONST r0, class
			1, 1, 1, // CONST r1, argument
			102, 2, 0, 1, 1, // NEW r2, r0, 1, r1
			104, -1, 2, 2, // SPD global, "result", r2
			119,
		},
		Constants: []BytecodeConstant{
			{Value: IntegerValue(17)},
			{Value: IntegerValue(42)},
			{Value: StringValue("result")},
		},
	}}}
	instructions, err := bytecode.DecodeLegacyInstructions(0)
	if err != nil {
		t.Fatal(err)
	}
	var newInstruction *LegacyInstruction
	for i := range instructions {
		if instructions[i].Opcode == 102 {
			newInstruction = &instructions[i]
			break
		}
	}
	if newInstruction == nil || newInstruction.Offset != 6 || len(newInstruction.Operands) != 4 ||
		newInstruction.Operands[0] != 2 || newInstruction.Operands[1] != 0 ||
		newInstruction.Operands[2] != 1 || newInstruction.Operands[3] != 1 {
		t.Fatalf("decoded NEW = %#v; want offset 6, result r2, callee r0, one arg r1", newInstruction)
	}
	var gotClass Value
	var gotArgs []Value
	construct := func(class Value, args []Value) (Value, error) {
		gotClass = class
		gotArgs = append([]Value(nil), args...)
		return StringValue("constructed"), nil
	}
	if _, err := bytecode.ExecuteLegacyWithConstruction(0, ObjectValue(global.Ref()), nil, nil, construct, table); err != nil {
		t.Fatal(err)
	}
	if !gotClass.StrictEqual(IntegerValue(17)) || len(gotArgs) != 1 || !gotArgs[0].StrictEqual(IntegerValue(42)) {
		t.Fatalf("NEW callback class/args = %v/%v; want 17/[42]", gotClass, gotArgs)
	}
	if got, ok := global.Get("result"); !ok || !got.StrictEqual(StringValue("constructed")) {
		t.Fatalf("NEW result = %v (present %v), want constructed string", got, ok)
	}
}

func TestExecuteLegacyNEWWithoutResultStillConstructs(t *testing.T) {
	bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
		MaxVariables: 1,
		Code:         []int16{102, 0, 0, 0, 119}, // NEW with discarded result and no arguments
	}}}
	calls := 0
	construct := func(Value, []Value) (Value, error) {
		calls++
		return StringValue("instance"), nil
	}
	if _, err := bytecode.ExecuteLegacyWithConstruction(0, VoidValue, nil, nil, construct); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("NEW constructor calls = %d, want 1", calls)
	}
}
func TestExecuteLegacyCALLInvokesCallableWithCurrentContext(t *testing.T) {
	table := NewObjectTable()
	global := table.New(CategoryDictionary)
	bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
		MaxVariables: 3,
		Code: []int16{
			1, 0, 0, // CONST r0, callable
			1, 1, 1, // CONST r1, argument
			99, 2, 0, 1, 1, // CALL r2, r0, 1, r1
			104, -1, 2, 2, // SPD global, "result", r2
			119,
		},
		Constants: []BytecodeConstant{
			{Value: StringValue("callable")},
			{Value: IntegerValue(42)},
			{Value: StringValue("result")},
		},
	}}}
	var gotCallee, gotContext Value
	var gotArgs []Value
	invoke := func(callee, context Value, args []Value, resultNeeded bool) (Value, error) {
		gotCallee, gotContext = callee, context
		gotArgs = append([]Value(nil), args...)
		if !resultNeeded {
			t.Fatal("CALL discarded-result flag = false, want true")
		}
		return StringValue("called"), nil
	}
	if _, err := bytecode.ExecuteLegacyWithRuntime(0, ObjectValue(global.Ref()), nil, invoke, table); err != nil {
		t.Fatal(err)
	}
	if !gotCallee.StrictEqual(StringValue("callable")) ||
		!gotContext.StrictEqual(ObjectValue(global.Ref())) ||
		len(gotArgs) != 1 || !gotArgs[0].StrictEqual(IntegerValue(42)) {
		t.Fatalf("CALL arguments = callee %v, context %v, args %v; want callable, global, [42]", gotCallee, gotContext, gotArgs)
	}
	if got, ok := global.Get("result"); !ok || !got.StrictEqual(StringValue("called")) {
		t.Fatalf("CALL result = %v (present %v), want called", got, ok)
	}
}
func TestExecuteLegacyCHGTHISBindsGlobalContext(t *testing.T) {
	vm := NewVM()
	receiver, err := vm.NewDictionary()
	if err != nil {
		t.Fatal(err)
	}
	var gotContext Value
	function, err := vm.RegisterLegacyFunction("global-bound", func(context Value, _ []Value, _ Host, _ *uint64, _ bool) (Value, error) {
		gotContext = context
		return IntegerValue(1), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
		MaxVariables: 3,
		Code: []int16{
			1, 0, 0, // CONST r0, function
			124, 1, // GLOBAL r1
			123, 0, 1, // CHGTHIS r0, r1
			99, 2, 0, 0, // CALL r2, r0, 0
			119,
		},
		Constants: []BytecodeConstant{{Value: function}},
	}}}
	var budget uint64 = 4096
	invoke := func(callee, context Value, args []Value, resultNeeded bool) (Value, error) {
		return vm.invoke(callee, context, args, nopHost{}, &budget, resultNeeded)
	}
	_, err = bytecode.ExecuteLegacyWithContextAndConstructionAndArguments(
		0, vm.globalValue(), receiver, nil, nil, invoke, nil, vm.Objects,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !gotContext.ObjectRef().HasBoundContext() ||
		gotContext.ObjectRef().Object != 0 || gotContext.ObjectRef().Context != 0 {
		t.Fatalf("CHGTHIS callback context = %#v; want explicitly bound global object", gotContext.ObjectRef())
	}
}
func TestExecuteLegacyCHGTHISNullContextUsesCurrentReceiver(t *testing.T) {
	vm := NewVM()
	receiver, err := vm.NewDictionary()
	if err != nil {
		t.Fatal(err)
	}
	var gotContext Value
	function, err := vm.RegisterLegacyFunction("unbound-after-null", func(context Value, _ []Value, _ Host, _ *uint64, _ bool) (Value, error) {
		gotContext = context
		return IntegerValue(1), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
		MaxVariables: 3,
		Code: []int16{
			1, 0, 0, // CONST r0, function
			1, 1, 1, // CONST r1, null context
			123, 0, 1, // CHGTHIS r0, r1
			99, 2, 0, 0, // CALL r2, r0, 0
			119,
		},
		Constants: []BytecodeConstant{{Value: function}, {Value: NullValue}},
	}}}
	var budget uint64 = 4096
	invoke := func(callee, context Value, args []Value, resultNeeded bool) (Value, error) {
		return vm.invoke(callee, context, args, nopHost{}, &budget, resultNeeded)
	}
	_, err = bytecode.ExecuteLegacyWithContextAndConstructionAndArguments(
		0, vm.globalValue(), receiver, nil, nil, invoke, nil, vm.Objects,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !gotContext.StrictEqual(receiver) {
		t.Fatalf("CHGTHIS with null callback context = %v; want current receiver %v", gotContext, receiver)
	}
}
func TestExecuteLegacyCALLDZeroResultRegisterDiscardsReturn(t *testing.T) {
	table := NewObjectTable()
	global := table.New(CategoryDictionary)
	receiver := table.New(CategoryDictionary)
	if err := receiver.Set("run", StringValue("callable")); err != nil {
		t.Fatal(err)
	}
	bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
		MaxVariables: 2,
		Code: []int16{
			1, 0, 0, // CONST r0, value to preserve
			1, 1, 1, // CONST r1, receiver
			100, 0, 1, 2, 0, // CALLD with a discarded result
			118, 0, 119, // Return r0.
		},
		Constants: []BytecodeConstant{
			{Value: StringValue("preserved")},
			{Value: ObjectValue(receiver.Ref())},
			{Value: StringValue("run")},
		},
	}}}
	invoke := func(_ Value, _ Value, _ []Value, resultNeeded bool) (Value, error) {
		if resultNeeded {
			t.Fatal("CALLD resultNeeded = true, want false")
		}
		return IntegerValue(0), nil
	}
	got, err := bytecode.ExecuteLegacyWithRuntime(0, ObjectValue(global.Ref()), nil, invoke, table)
	if err != nil {
		t.Fatal(err)
	}
	if !got.StrictEqual(StringValue("preserved")) {
		t.Fatalf("discarded CALLD overwrote r0: got %v, want preserved string", got)
	}
}
func TestDecodeLegacyCALLArgumentWidths(t *testing.T) {
	bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
		Code: []int16{
			99, 0, 1, 1, 2, // CALL r0, r1, 1, r2
			99, 0, 1, -1, // CALL reuses current arguments
			99, 0, 1, -2, 1, 2, 0, // CALL one fatNormal descriptor
			119,
		},
	}}}
	instructions, err := bytecode.DecodeLegacyInstructions(0)
	if err != nil {
		t.Fatal(err)
	}
	wantOffsets := []int{0, 5, 9, 16}
	if len(instructions) != len(wantOffsets) {
		t.Fatalf("decoded %d instructions, want %d: %#v", len(instructions), len(wantOffsets), instructions)
	}
	for i, want := range wantOffsets {
		if instructions[i].Offset != want {
			t.Fatalf("instruction %d offset = %d, want %d (%s)", i, instructions[i].Offset, want, instructions[i].Name)
		}
	}
	if instructions[0].Opcode != 99 || len(instructions[0].Operands) != 4 ||
		instructions[1].Opcode != 99 || len(instructions[1].Operands) != 3 ||
		instructions[2].Opcode != 99 || len(instructions[2].Operands) != 6 {
		t.Fatalf("decoded CALL instructions = %#v; want operand widths 4, 3, and 6", instructions[:3])
	}
}
func TestExecuteLegacyCLTCGTSemantics(t *testing.T) {
	for _, test := range []struct {
		name  string
		op    int16
		left  Value
		right Value
		want  int64
	}{
		// CLT implements `<`: flag = ra < rb.
		{name: "CLT integer less", op: 9, left: IntegerValue(3), right: IntegerValue(8), want: 1},
		{name: "CLT integer not less", op: 9, left: IntegerValue(8), right: IntegerValue(3), want: 0},
		{name: "CLT integer equal", op: 9, left: IntegerValue(8), right: IntegerValue(8), want: 0},
		{name: "CLT string less", op: 9, left: StringValue("alpha"), right: StringValue("beta"), want: 1},
		{name: "CLT string not less", op: 9, left: StringValue("beta"), right: StringValue("alpha"), want: 0},
		{name: "CLT mixed numeric coercion", op: 9, left: IntegerValue(3), right: StringValue("8"), want: 1},
		// CGT implements `>`: flag = ra > rb.
		{name: "CGT integer greater", op: 10, left: IntegerValue(8), right: IntegerValue(3), want: 1},
		{name: "CGT integer not greater", op: 10, left: IntegerValue(3), right: IntegerValue(8), want: 0},
		{name: "CGT integer equal", op: 10, left: IntegerValue(8), right: IntegerValue(8), want: 0},
		{name: "CGT string greater", op: 10, left: StringValue("beta"), right: StringValue("alpha"), want: 1},
		{name: "CGT string not greater", op: 10, left: StringValue("alpha"), right: StringValue("beta"), want: 0},
		{name: "CGT mixed numeric coercion", op: 10, left: StringValue("8"), right: IntegerValue(3), want: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
				ArgumentCount: 2,
				Code: []int16{
					test.op, -3, -4, // CLT/CGT ra, rb
					11, 0, // SETF r0
					118, 0, // SRV r0
					119,
				},
			}}}
			got, err := bytecode.ExecuteLegacyWithContextAndConstructionAndArguments(
				0, VoidValue, VoidValue, []Value{test.left, test.right}, nil, nil, nil,
			)
			if err != nil {
				t.Fatal(err)
			}
			if got.Kind() != KindInteger || got.IntegerValue() != test.want {
				t.Fatalf("opcode %d result = %v (kind %v), want integer %d", test.op, got, got.Kind(), test.want)
			}
		})
	}
}
func TestExecuteLegacyCALLDOperationErrorTransfersToEntryHandler(t *testing.T) {
	table := NewObjectTable()
	global := table.New(CategoryDictionary)
	receiver := table.New(CategoryDictionary)
	method := NewNativeFunction(table, "callHook", func(ObjectRef, []Value) (Value, error) {
		return VoidValue, errors.New("operation failed")
	})
	if err := receiver.Set("callHook", method); err != nil {
		t.Fatal(err)
	}
	bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
		MaxVariables: 2,
		Constants: []BytecodeConstant{
			{Value: ObjectValue(receiver.Ref())},
			{Value: StringValue("callHook")},
		},
		Code: []int16{
			1, 0, 0, // CONST r0, call receiver
			120, 9, 0, // ENTRY catches at offset 12 into r0.
			100, 1, 0, 1, 0, // CALLD r1, r0.callHook()
			121,    // EXTRY is skipped when the protected call fails.
			118, 0, // Return the caught exception value.
			119,
		},
	}}}
	invoke := func(Value, Value, []Value, bool) (Value, error) {
		return VoidValue, errors.New("operation failed")
	}

	got, err := bytecode.ExecuteLegacyWithContextAndConstruction(
		0, ObjectValue(global.Ref()), ObjectValue(global.Ref()), nil, invoke, nil, table,
	)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind() != KindString || !strings.Contains(got.Text(), "operation failed") {
		t.Fatalf("caught operation error = %v (kind %v), want containing %q", got, got.Kind(), "operation failed")
	}
}

func TestExecuteLegacyEXTRYContinuesAfterProtectedBody(t *testing.T) {
	bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
		MaxVariables: 1,
		Code: []int16{
			1, 0, 0, // CONST r0, 7
			118, 0, // SRV r0
			120, 0, 0, // ENTRY catch target, exception register
			121,     // EXTRY leaves the protected region and continues.
			1, 0, 1, // CONST r0, 9
			118, 0,
			119,
		},
		Constants: []BytecodeConstant{
			{Value: IntegerValue(7)},
			{Value: IntegerValue(9)},
		},
	}}}

	got, err := bytecode.ExecuteLegacy(0, VoidValue)
	if err != nil {
		t.Fatal(err)
	}
	if !got.StrictEqual(IntegerValue(9)) {
		t.Fatalf("EXTRY result = %v, want 9 from code after the protected region", got)
	}
}

func TestExecuteLegacyTHROWTransfersValueToEntryHandler(t *testing.T) {
	bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
		MaxVariables: 2,
		Code: []int16{
			120, 8, 0, // ENTRY catches at offset 8 into r0.
			1, 1, 0, // CONST r1, "raised"
			122, 1, // THROW r1.
			118, 0, // Catch returns the exception value.
			119,
		},
		Constants: []BytecodeConstant{{Value: StringValue("raised")}},
	}}}
	got, err := bytecode.ExecuteLegacy(0, VoidValue)
	if err != nil {
		t.Fatal(err)
	}
	if !got.StrictEqual(StringValue("raised")) {
		t.Fatalf("caught value = %v, want original thrown value", got)
	}
}

func TestExecuteLegacyTHROWWithoutHandlerPropagates(t *testing.T) {
	bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
		MaxVariables: 1,
		Code:         []int16{1, 0, 0, 122, 0, 119},
		Constants:    []BytecodeConstant{{Value: StringValue("uncaught")}},
	}}}
	_, err := bytecode.ExecuteLegacy(0, VoidValue)
	if err == nil || !strings.Contains(err.Error(), "legacy intercode object 0 code offset 3: uncaught TJS exception: uncaught") {
		t.Fatalf("uncaught THROW error = %v, want surfaced script exception", err)
	}
}

type legacyArrayLoadHost struct {
	receiver Value
	name     string
	args     []Value
}

func (h *legacyArrayLoadHost) Call(_ *VM, name string, args []Value, _ *uint64) (Value, error) {
	h.name, h.args = name, args
	return VoidValue, nil
}

func (h *legacyArrayLoadHost) CallWithContext(_ *VM, name string, receiver Value, args []Value, _ *uint64) (Value, error) {
	h.name, h.receiver, h.args = name, receiver, args
	return receiver, nil
}

func (h *legacyArrayLoadHost) CallWithResult(_ *VM, name string, receiver Value, args []Value, _ *uint64, _ bool) (Value, error) {
	h.name, h.receiver, h.args = name, receiver, args
	return receiver, nil
}

func TestExecuteLegacyArrayLoadUsesVMHost(t *testing.T) {
	vm := NewVM()
	array, err := vm.NewArray(nil)
	if err != nil {
		t.Fatal(err)
	}
	host := &legacyArrayLoadHost{}
	bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
		Constants: []BytecodeConstant{
			{Value: array},
			{Value: StringValue("EngineRev")},
			{Value: StringValue("load")},
		},
		Code: []int16{
			1, 0, 0, // CONST r0, array receiver
			1, 1, 1, // CONST r1, storage name
			100, 2, 0, 2, 1, 1, // CALLD r2, r0.load(r1)
			118, 2, 119, // Return r2.
		},
	}}}
	invoke := func(function, receiver Value, args []Value, needed bool) (Value, error) {
		return vm.InvokeLegacy(function, receiver, args, host, nil, needed)
	}
	got, err := bytecode.ExecuteLegacyWithContextAndConstruction(
		0, VoidValue, VoidValue, nil, invoke, nil, vm.Objects,
	)
	if err != nil {
		t.Fatal(err)
	}
	if host.name != "Array.load" || !host.receiver.StrictEqual(array) ||
		len(host.args) != 1 || !host.args[0].StrictEqual(StringValue("EngineRev")) {
		t.Fatalf("legacy Array.load host call = %q, receiver %v, args %v", host.name, host.receiver, host.args)
	}
	if !got.StrictEqual(array) {
		t.Fatalf("legacy Array.load result = %v, want receiver %v", got, array)
	}
}

func TestDecodeLegacyIncDecInstructionLengths(t *testing.T) {
	bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
		Code: []int16{
			18, 0, // INC r0
			19, 0, 0, 0, // INCPD r0, r0, name
			20, 0, 0, 0, // INCPI r0, r0, key
			21, 0, 0, // INCP r0, r0
			22, 0, // DEC r0
			23, 0, 0, 0, // DECPD r0, r0, name
			24, 0, 0, 0, // DECPI r0, r0, key
			25, 0, 0, // DECP r0, r0
			119,            // RET
			59, 0, 0, 0, 0, // ADDPD r0, r0, name, param
			61, 0, 0, 0, // ADDP r0, r0, param
		},
	}}}
	instructions, err := bytecode.DecodeLegacyInstructions(0)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		offset  int
		name    string
		opcount int
	}{
		{0, "INC", 1},
		{2, "INCPD", 3},
		{6, "INCPI", 3},
		{10, "INCP", 2},
		{13, "DEC", 1},
		{15, "DECPD", 3},
		{19, "DECPI", 3},
		{23, "DECP", 2},
		{26, "RET", 0},
		{27, "ADDPD", 4},
		{32, "ADDP", 3},
	}
	if len(instructions) != len(want) {
		t.Fatalf("decoded %d instructions, want %d: %#v", len(instructions), len(want), instructions)
	}
	for i, expect := range want {
		got := instructions[i]
		if got.Offset != expect.offset || got.Name != expect.name || len(got.Operands) != expect.opcount {
			t.Fatalf("instruction %d = offset %d %s with %d operands, want offset %d %s with %d operands",
				i, got.Offset, got.Name, len(got.Operands), expect.offset, expect.name, expect.opcount)
		}
	}
}

func TestExecuteLegacyRegisterPrePostIncDec(t *testing.T) {
	for _, test := range []struct {
		name string
		code []int16
		want Value
	}{
		{
			name: "pre increment returns new value",
			code: []int16{1, 0, 0, 18, 0, 118, 0, 119},
			want: IntegerValue(5),
		},
		{
			name: "pre decrement returns new value",
			code: []int16{1, 0, 0, 22, 0, 118, 0, 119},
			want: IntegerValue(3),
		},
		{
			name: "post increment saves old value before stepping",
			code: []int16{1, 0, 0, 2, 1, 0, 18, 0, 118, 1, 119},
			want: IntegerValue(4),
		},
		{
			name: "post decrement saves old value before stepping",
			code: []int16{1, 0, 0, 2, 1, 0, 22, 0, 118, 1, 119},
			want: IntegerValue(4),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
				MaxVariables: 2,
				Code:         test.code,
				Constants:    []BytecodeConstant{{Value: IntegerValue(4)}},
			}}}
			got, err := bytecode.ExecuteLegacy(0, VoidValue)
			if err != nil {
				t.Fatal(err)
			}
			if !got.StrictEqual(test.want) {
				t.Fatalf("result = %v (kind %v), want %v", got, got.Kind(), test.want)
			}
		})
	}
}

func TestExecuteLegacyRegisterIncDecPersistsStep(t *testing.T) {
	// The step must be stored back into the operand register: return the
	// incremented register itself rather than a separate result register.
	bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
		MaxVariables: 1,
		Code:         []int16{1, 0, 0, 18, 0, 118, 0, 119},
		Constants:    []BytecodeConstant{{Value: IntegerValue(4)}},
	}}}
	got, err := bytecode.ExecuteLegacy(0, VoidValue)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind() != KindInteger || got.IntegerValue() != 5 {
		t.Fatalf("incremented register = %v (kind %v), want integer 5", got, got.Kind())
	}
}

func TestExecuteLegacyPropertyIncDec(t *testing.T) {
	type memberKind int
	const (
		dictionaryMember memberKind = iota
		arrayElement
	)
	for _, test := range []struct {
		name    string
		kind    memberKind
		op      int16
		initial Value
		stored  Value
		direct  bool
	}{
		{name: "INCPD dictionary member prefix", kind: dictionaryMember, op: 19, initial: IntegerValue(4), stored: IntegerValue(5), direct: true},
		{name: "DECPD dictionary member prefix", kind: dictionaryMember, op: 23, initial: IntegerValue(4), stored: IntegerValue(3), direct: true},
		{name: "INCPI array element prefix", kind: arrayElement, op: 20, initial: IntegerValue(4), stored: IntegerValue(5)},
		{name: "DECPI array element prefix", kind: arrayElement, op: 24, initial: IntegerValue(4), stored: IntegerValue(3)},
	} {
		t.Run(test.name, func(t *testing.T) {
			table := NewObjectTable()
			var target *Object
			key := "0"
			if test.kind == dictionaryMember {
				target = table.New(CategoryDictionary)
				key = "count"
				if err := target.Set(key, test.initial); err != nil {
					t.Fatal(err)
				}
			} else {
				target = table.New(CategoryArray)
				if err := target.Set(key, test.initial); err != nil {
					t.Fatal(err)
				}
			}
			var code []int16
			if test.direct {
				// CONST r0, target; INCPD/DECPD r1, r0, name; SRV r1; RET
				code = []int16{1, 0, 0, test.op, 1, 0, 1, 118, 1, 119}
			} else {
				// CONST r0, target; CONST r2, key; INCPI/DECPI r1, r0, r2; SRV r1; RET
				code = []int16{1, 0, 0, 1, 2, 2, test.op, 1, 0, 2, 118, 1, 119}
			}
			bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
				MaxVariables: 3,
				Code:         code,
				Constants: []BytecodeConstant{
					{Value: ObjectValue(target.Ref())},
					{Value: StringValue(key)},
					{Value: IntegerValue(0)},
				},
			}}}
			got, err := bytecode.ExecuteLegacy(0, VoidValue, table)
			if err != nil {
				t.Fatal(err)
			}
			if !got.StrictEqual(test.stored) {
				t.Fatalf("result = %v (kind %v), want %v", got, got.Kind(), test.stored)
			}
			stored, ok := target.Get(key)
			if !ok || !stored.StrictEqual(test.stored) {
				t.Fatalf("member %q = %v (present %v), want %v", key, stored, ok, test.stored)
			}
		})
	}
}

func TestExecuteLegacyPropertyIncDecPostfixDiscardsResult(t *testing.T) {
	// Postfix codegen saves the previous value, issues the property step with a
	// zero result register, and returns the saved value while the member is
	// still updated.
	table := NewObjectTable()
	target := table.New(CategoryDictionary)
	if err := target.Set("count", IntegerValue(4)); err != nil {
		t.Fatal(err)
	}
	bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
		MaxVariables: 3,
		Code: []int16{
			1, 0, 0, // CONST r0, target
			103, 2, 0, 1, // GPD r2, r0, name -- save old value
			19, 0, 0, 1, // INCPD r0(zero=discard), r0, name -- increment in place
			118, 2, 119, // Return the saved value.
		},
		Constants: []BytecodeConstant{
			{Value: ObjectValue(target.Ref())},
			{Value: StringValue("count")},
		},
	}}}
	got, err := bytecode.ExecuteLegacy(0, VoidValue, table)
	if err != nil {
		t.Fatal(err)
	}
	if !got.StrictEqual(IntegerValue(4)) {
		t.Fatalf("postfix result = %v (kind %v), want integer 4", got, got.Kind())
	}
	stored, ok := target.Get("count")
	if !ok || !stored.StrictEqual(IntegerValue(5)) {
		t.Fatalf("member count = %v (present %v), want integer 5", stored, ok)
	}
}

func TestExecuteLegacyINCPDECPRegisterForm(t *testing.T) {
	for _, test := range []struct {
		name string
		op   int16
		want Value
	}{
		// Return the stepped operand register to prove it was written back.
		{name: "INCP writes back operand register", op: 21, want: IntegerValue(6)},
		{name: "DECP writes back operand register", op: 25, want: IntegerValue(4)},
	} {
		t.Run(test.name, func(t *testing.T) {
			bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
				MaxVariables: 2,
				Code:         []int16{1, 0, 0, test.op, 1, 0, 118, 0, 119},
				Constants:    []BytecodeConstant{{Value: IntegerValue(5)}},
			}}}
			got, err := bytecode.ExecuteLegacy(0, VoidValue)
			if err != nil {
				t.Fatal(err)
			}
			if !got.StrictEqual(test.want) {
				t.Fatalf("result = %v (kind %v), want %v", got, got.Kind(), test.want)
			}
		})
	}
}

func TestExecuteLegacyPropertyIncDecConversionError(t *testing.T) {
	table := NewObjectTable()
	target := table.New(CategoryDictionary)
	if err := target.Set("blob", ObjectValue(NewObjectRef(1, 0))); err != nil {
		t.Fatal(err)
	}
	bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
		MaxVariables: 2,
		Code:         []int16{1, 0, 0, 19, 1, 0, 1, 118, 1, 119},
		Constants: []BytecodeConstant{
			{Value: ObjectValue(target.Ref())},
			{Value: StringValue("blob")},
		},
	}}}
	_, err := bytecode.ExecuteLegacy(0, VoidValue, table)
	if err == nil || !strings.Contains(err.Error(), "legacy intercode object 0 code offset 3: INCPD: cannot convert TJS Object to Integer or Real") {
		t.Fatalf("INCPD error = %v, want conversion error with legacy bytecode location", err)
	}
}

func TestDecodeLegacyShiftInstructionLengths(t *testing.T) {
	bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
		Code: []int16{
			46, 0, 0, // SAR
			47, 0, 0, 0, 0, // SARPD
			48, 0, 0, 0, 0, // SARPI
			49, 0, 0, 0, // SARP
			50, 0, 0, // SAL
			51, 0, 0, 0, 0, // SALPD
			52, 0, 0, 0, 0, // SALPI
			53, 0, 0, 0, // SALP
			54, 0, 0, // SR
			55, 0, 0, 0, 0, // SRPD
			56, 0, 0, 0, 0, // SRPI
			57, 0, 0, 0, // SRP
			119, // RET
		},
	}}}
	instructions, err := bytecode.DecodeLegacyInstructions(0)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		offset  int
		name    string
		opcount int
	}{
		{0, "SAR", 2},
		{3, "SARPD", 4},
		{8, "SARPI", 4},
		{13, "SARP", 3},
		{17, "SAL", 2},
		{20, "SALPD", 4},
		{25, "SALPI", 4},
		{30, "SALP", 3},
		{34, "SR", 2},
		{37, "SRPD", 4},
		{42, "SRPI", 4},
		{47, "SRP", 3},
		{51, "RET", 0},
	}
	if len(instructions) != len(want) {
		t.Fatalf("decoded %d instructions, want %d: %#v", len(instructions), len(want), instructions)
	}
	for i, expect := range want {
		got := instructions[i]
		if got.Offset != expect.offset || got.Name != expect.name || len(got.Operands) != expect.opcount {
			t.Fatalf("instruction %d = offset %d %s with %d operands, want offset %d %s with %d operands",
				i, got.Offset, got.Name, len(got.Operands), expect.offset, expect.name, expect.opcount)
		}
	}
}

func TestExecuteLegacyShiftRegister(t *testing.T) {
	// The register form replaces the left register in place, so the shifted
	// value is observed by returning the left register itself.
	logicalRightOfNeg16 := int64(uint64(0xFFFFFFFFFFFFFFF0) >> 2)
	for _, test := range []struct {
		name        string
		op          int16
		left, right Value
		want        Value
	}{
		{name: "SAR arithmetic right keeps the sign", op: 46, left: IntegerValue(-16), right: IntegerValue(2), want: IntegerValue(-4)},
		{name: "SAR positive rounds toward negative infinity", op: 46, left: IntegerValue(16), right: IntegerValue(2), want: IntegerValue(4)},
		{name: "SAL left shift", op: 50, left: IntegerValue(3), right: IntegerValue(2), want: IntegerValue(12)},
		{name: "SR logical right fills with zeroes", op: 54, left: IntegerValue(-16), right: IntegerValue(2), want: IntegerValue(logicalRightOfNeg16)},
		{name: "SR positive matches SAR", op: 54, left: IntegerValue(16), right: IntegerValue(2), want: IntegerValue(4)},
		{name: "SAL converts a numeric string", op: 50, left: StringValue("8"), right: IntegerValue(1), want: IntegerValue(16)},
		{name: "SAL truncates a real to an integer", op: 50, left: RealValue(2.5), right: IntegerValue(1), want: IntegerValue(4)},
	} {
		t.Run(test.name, func(t *testing.T) {
			bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
				MaxVariables: 2,
				Code:         []int16{1, 0, 0, 1, 1, 1, test.op, 0, 1, 118, 0, 119},
				Constants:    []BytecodeConstant{{Value: test.left}, {Value: test.right}},
			}}}
			got, err := bytecode.ExecuteLegacy(0, VoidValue)
			if err != nil {
				t.Fatal(err)
			}
			if !got.StrictEqual(test.want) {
				t.Fatalf("shift result = %v (kind %v), want %v (kind %v)", got, got.Kind(), test.want, test.want.Kind())
			}
		})
	}
}

func TestExecuteLegacyShiftPropertyDirect(t *testing.T) {
	// SARPD r1, r0, "bits", r2 -- shift a dictionary member by the value in a
	// register, storing the new value in the result register and the member.
	table := NewObjectTable()
	target := table.New(CategoryDictionary)
	if err := target.Set("bits", IntegerValue(64)); err != nil {
		t.Fatal(err)
	}
	bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
		MaxVariables: 3,
		Code:         []int16{1, 0, 0, 1, 2, 2, 47, 1, 0, 1, 2, 118, 1, 119},
		Constants: []BytecodeConstant{
			{Value: ObjectValue(target.Ref())},
			{Value: StringValue("bits")},
			{Value: IntegerValue(3)},
		},
	}}}
	got, err := bytecode.ExecuteLegacy(0, VoidValue, table)
	if err != nil {
		t.Fatal(err)
	}
	if !got.StrictEqual(IntegerValue(8)) {
		t.Fatalf("SARPD result = %v (kind %v), want integer 8", got, got.Kind())
	}
	stored, ok := target.Get("bits")
	if !ok || !stored.StrictEqual(IntegerValue(8)) {
		t.Fatalf("member bits = %v (present %v), want integer 8", stored, ok)
	}
}

func TestExecuteLegacyShiftPropertyIndirect(t *testing.T) {
	// SARPI r1, r0, r2, r3 -- the member key comes from a register.
	table := NewObjectTable()
	target := table.New(CategoryArray)
	if err := target.Set("1", IntegerValue(64)); err != nil {
		t.Fatal(err)
	}
	bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
		MaxVariables: 4,
		Code:         []int16{1, 0, 0, 1, 2, 1, 1, 3, 2, 48, 1, 0, 2, 3, 118, 1, 119},
		Constants: []BytecodeConstant{
			{Value: ObjectValue(target.Ref())},
			{Value: IntegerValue(1)},
			{Value: IntegerValue(2)},
		},
	}}}
	got, err := bytecode.ExecuteLegacy(0, VoidValue, table)
	if err != nil {
		t.Fatal(err)
	}
	if !got.StrictEqual(IntegerValue(16)) {
		t.Fatalf("SARPI result = %v (kind %v), want integer 16", got, got.Kind())
	}
	stored, ok := target.Get("1")
	if !ok || !stored.StrictEqual(IntegerValue(16)) {
		t.Fatalf("member 1 = %v (present %v), want integer 16", stored, ok)
	}
}

func TestExecuteLegacyShiftPropertyMemberless(t *testing.T) {
	// The P form replaces the target register in place and optionally copies
	// the shifted value into the result register.
	logicalRightOfNeg16 := int64(uint64(0xFFFFFFFFFFFFFFF0) >> 2)
	for _, test := range []struct {
		name  string
		op    int16
		left  Value
		param Value
		want  Value
	}{
		{name: "SARP", op: 49, left: IntegerValue(-16), param: IntegerValue(2), want: IntegerValue(-4)},
		{name: "SALP", op: 53, left: IntegerValue(3), param: IntegerValue(2), want: IntegerValue(12)},
		{name: "SRP", op: 57, left: IntegerValue(-16), param: IntegerValue(2), want: IntegerValue(logicalRightOfNeg16)},
	} {
		t.Run(test.name, func(t *testing.T) {
			// CONST r0, left; CONST r2, param; op r1, r0, r2; SRV r0; RET
			bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
				MaxVariables: 3,
				Code:         []int16{1, 0, 0, 1, 2, 1, test.op, 1, 0, 2, 118, 0, 119},
				Constants:    []BytecodeConstant{{Value: test.left}, {Value: test.param}},
			}}}
			got, err := bytecode.ExecuteLegacy(0, VoidValue)
			if err != nil {
				t.Fatal(err)
			}
			if !got.StrictEqual(test.want) {
				t.Fatalf("%s result = %v (kind %v), want %v", test.name, got, got.Kind(), test.want)
			}
		})
	}
}

func TestExecuteLegacyShiftConversionErrorContext(t *testing.T) {
	table := NewObjectTable()
	target := table.New(CategoryDictionary)
	if err := target.Set("blob", ObjectValue(NewObjectRef(1, 0))); err != nil {
		t.Fatal(err)
	}
	t.Run("register form", func(t *testing.T) {
		bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
			MaxVariables: 2,
			Code:         []int16{1, 0, 0, 1, 1, 1, 46, 0, 1, 118, 0, 119},
			Constants:    []BytecodeConstant{{Value: ObjectValue(NewObjectRef(1, 0))}, {Value: IntegerValue(2)}},
		}}}
		_, err := bytecode.ExecuteLegacy(0, VoidValue)
		if err == nil || !strings.Contains(err.Error(), "legacy intercode object 0 code offset 6: SAR: cannot convert TJS Object to number") {
			t.Fatalf("SAR error = %v, want conversion error with legacy bytecode location", err)
		}
	})
	t.Run("property form", func(t *testing.T) {
		bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
			MaxVariables: 3,
			Code:         []int16{1, 0, 0, 1, 2, 2, 47, 1, 0, 1, 2, 118, 1, 119},
			Constants: []BytecodeConstant{
				{Value: ObjectValue(target.Ref())},
				{Value: StringValue("blob")},
				{Value: IntegerValue(1)},
			},
		}}}
		_, err := bytecode.ExecuteLegacy(0, VoidValue, table)
		if err == nil || !strings.Contains(err.Error(), "legacy intercode object 0 code offset 6: SARPD: cannot convert TJS Object to number") {
			t.Fatalf("SARPD error = %v, want conversion error with legacy bytecode location", err)
		}
	})
}

func TestDecodeLegacyBitwiseInstructionLengths(t *testing.T) {
	bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
		Code: []int16{
			26, 0, 0, // LOR
			27, 0, 0, 0, 0, // LORPD
			28, 0, 0, 0, 0, // LORPI
			29, 0, 0, 0, // LORP
			30, 0, 0, // LAND
			31, 0, 0, 0, 0, // LANDPD
			32, 0, 0, 0, 0, // LANDPI
			33, 0, 0, 0, // LANDP
			34, 0, 0, // BOR
			35, 0, 0, 0, 0, // BORPD
			36, 0, 0, 0, 0, // BORPI
			37, 0, 0, 0, // BORP
			38, 0, 0, // BXOR
			39, 0, 0, 0, 0, // BXORPD
			40, 0, 0, 0, 0, // BXORPI
			41, 0, 0, 0, // BXORP
			42, 0, 0, // BAND
			43, 0, 0, 0, 0, // BANDPD
			44, 0, 0, 0, 0, // BANDPI
			45, 0, 0, 0, // BANDP
			119, // RET
		},
	}}}
	instructions, err := bytecode.DecodeLegacyInstructions(0)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		offset  int
		name    string
		opcount int
	}{
		{0, "LOR", 2},
		{3, "LORPD", 4},
		{8, "LORPI", 4},
		{13, "LORP", 3},
		{17, "LAND", 2},
		{20, "LANDPD", 4},
		{25, "LANDPI", 4},
		{30, "LANDP", 3},
		{34, "BOR", 2},
		{37, "BORPD", 4},
		{42, "BORPI", 4},
		{47, "BORP", 3},
		{51, "BXOR", 2},
		{54, "BXORPD", 4},
		{59, "BXORPI", 4},
		{64, "BXORP", 3},
		{68, "BAND", 2},
		{71, "BANDPD", 4},
		{76, "BANDPI", 4},
		{81, "BANDP", 3},
		{85, "RET", 0},
	}
	if len(instructions) != len(want) {
		t.Fatalf("decoded %d instructions, want %d: %#v", len(instructions), len(want), instructions)
	}
	for i, expect := range want {
		got := instructions[i]
		if got.Offset != expect.offset || got.Name != expect.name || len(got.Operands) != expect.opcount {
			t.Fatalf("instruction %d = offset %d %s with %d operands, want offset %d %s with %d operands",
				i, got.Offset, got.Name, len(got.Operands), expect.offset, expect.name, expect.opcount)
		}
	}
}

func TestDecodeLegacyBitwiseTruncated(t *testing.T) {
	// Every opcode in 26..45 must report a truncated instruction when its
	// operands run past the end of the code array.
	for _, test := range []struct {
		name   string
		code   []int16
		opcode string
	}{
		{name: "LOR register form", code: []int16{26, 0}, opcode: "LOR"},
		{name: "BORP memberless form", code: []int16{37, 0, 0}, opcode: "BORP"},
		{name: "BANDPD direct form", code: []int16{43, 0, 0, 0}, opcode: "BANDPD"},
		{name: "LANDPI indirect form", code: []int16{32, 0, 0, 0}, opcode: "LANDPI"},
	} {
		t.Run(test.name, func(t *testing.T) {
			bytecode := &DecodedBytecode{Objects: []BytecodeObject{{Code: test.code}}}
			_, err := bytecode.DecodeLegacyInstructions(0)
			if err == nil || !strings.Contains(err.Error(), "truncated "+test.opcode+" instruction") {
				t.Fatalf("decode error = %v, want truncated %s instruction", err, test.opcode)
			}
		})
	}
}

func TestExecuteLegacyBitwiseRegister(t *testing.T) {
	// The register form replaces the left register in place. LOR/LAND follow
	// the reference VM's logicalorequal/logicalandequal and always produce the
	// 0/1 integer result of the TJS truthiness test, never an operand.
	for _, test := range []struct {
		name        string
		op          int16
		left, right Value
		want        Value
	}{
		{name: "LOR true when either side is truthy", op: 26, left: IntegerValue(4), right: IntegerValue(0), want: IntegerValue(1)},
		{name: "LOR false when both sides are falsy", op: 26, left: IntegerValue(0), right: IntegerValue(0), want: IntegerValue(0)},
		{name: "LOR uses truthiness not bitwise or", op: 26, left: IntegerValue(2), right: IntegerValue(0), want: IntegerValue(1)},
		{name: "LAND true only when both sides are truthy", op: 30, left: IntegerValue(4), right: IntegerValue(1), want: IntegerValue(1)},
		{name: "LAND false when either side is falsy", op: 30, left: IntegerValue(4), right: IntegerValue(0), want: IntegerValue(0)},
		{name: "BOR combines bits", op: 34, left: IntegerValue(12), right: IntegerValue(10), want: IntegerValue(14)},
		{name: "BOR with all bits set", op: 34, left: IntegerValue(-1), right: IntegerValue(0), want: IntegerValue(-1)},
		{name: "BXOR combines bits", op: 38, left: IntegerValue(12), right: IntegerValue(10), want: IntegerValue(6)},
		{name: "BAND keeps common bits", op: 42, left: IntegerValue(12), right: IntegerValue(10), want: IntegerValue(8)},
		{name: "BAND with all bits set", op: 42, left: IntegerValue(-1), right: IntegerValue(6), want: IntegerValue(6)},
		{name: "BOR converts a numeric string", op: 34, left: StringValue("12"), right: IntegerValue(10), want: IntegerValue(14)},
		{name: "BAND truncates a real toward zero", op: 42, left: RealValue(2.9), right: IntegerValue(3), want: IntegerValue(2)},
		{name: "BXOR of equal values is zero", op: 38, left: StringValue("7"), right: IntegerValue(7), want: IntegerValue(0)},
	} {
		t.Run(test.name, func(t *testing.T) {
			bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
				MaxVariables: 2,
				Code:         []int16{1, 0, 0, 1, 1, 1, test.op, 0, 1, 118, 0, 119},
				Constants:    []BytecodeConstant{{Value: test.left}, {Value: test.right}},
			}}}
			got, err := bytecode.ExecuteLegacy(0, VoidValue)
			if err != nil {
				t.Fatal(err)
			}
			if !got.StrictEqual(test.want) {
				t.Fatalf("%s result = %v (kind %v), want %v (kind %v)", test.name, got, got.Kind(), test.want, test.want.Kind())
			}
		})
	}
}

func TestExecuteLegacyBitwisePropertyDirect(t *testing.T) {
	// The PD form reads a dictionary member by constant name, applies the
	// operator and stores the new value into both the member and the optional
	// result register.
	for _, test := range []struct {
		name  string
		op    int16
		start Value
		param Value
		want  Value
	}{
		{name: "LORPD", op: 27, start: IntegerValue(0), param: IntegerValue(5), want: IntegerValue(1)},
		{name: "LANDPD", op: 31, start: IntegerValue(1), param: IntegerValue(0), want: IntegerValue(0)},
		{name: "BORPD", op: 35, start: IntegerValue(8), param: IntegerValue(4), want: IntegerValue(12)},
		{name: "BXORPD", op: 39, start: IntegerValue(12), param: IntegerValue(10), want: IntegerValue(6)},
		{name: "BANDPD", op: 43, start: IntegerValue(12), param: IntegerValue(10), want: IntegerValue(8)},
	} {
		t.Run(test.name, func(t *testing.T) {
			table := NewObjectTable()
			target := table.New(CategoryDictionary)
			if err := target.Set("acc", test.start); err != nil {
				t.Fatal(err)
			}
			// CONST r0, target; CONST r2, param; op r1, r0, "acc", r2; SRV r1; RET
			bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
				MaxVariables: 3,
				Code:         []int16{1, 0, 0, 1, 2, 2, test.op, 1, 0, 1, 2, 118, 1, 119},
				Constants: []BytecodeConstant{
					{Value: ObjectValue(target.Ref())},
					{Value: StringValue("acc")},
					{Value: test.param},
				},
			}}}
			got, err := bytecode.ExecuteLegacy(0, VoidValue, table)
			if err != nil {
				t.Fatal(err)
			}
			if !got.StrictEqual(test.want) {
				t.Fatalf("%s result = %v (kind %v), want %v", test.name, got, got.Kind(), test.want)
			}
			stored, ok := target.Get("acc")
			if !ok || !stored.StrictEqual(test.want) {
				t.Fatalf("%s member acc = %v (present %v), want %v", test.name, stored, ok, test.want)
			}
		})
	}
}

func TestExecuteLegacyBitwisePropertyIndirect(t *testing.T) {
	// BANDPI r1, r0, r2, r3 -- the member key comes from a register. Array
	// indices are canonicalized to strings, as in the reference VM.
	table := NewObjectTable()
	target := table.New(CategoryArray)
	if err := target.Set("1", IntegerValue(12)); err != nil {
		t.Fatal(err)
	}
	bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
		MaxVariables: 4,
		Code:         []int16{1, 0, 0, 1, 2, 1, 1, 3, 2, 44, 1, 0, 2, 3, 118, 1, 119},
		Constants: []BytecodeConstant{
			{Value: ObjectValue(target.Ref())},
			{Value: IntegerValue(1)},
			{Value: IntegerValue(10)},
		},
	}}}
	got, err := bytecode.ExecuteLegacy(0, VoidValue, table)
	if err != nil {
		t.Fatal(err)
	}
	if !got.StrictEqual(IntegerValue(8)) {
		t.Fatalf("BANDPI result = %v (kind %v), want integer 8", got, got.Kind())
	}
	stored, ok := target.Get("1")
	if !ok || !stored.StrictEqual(IntegerValue(8)) {
		t.Fatalf("member 1 = %v (present %v), want integer 8", stored, ok)
	}
}

func TestExecuteLegacyBitwisePropertyMemberless(t *testing.T) {
	// The P form replaces the target register in place and optionally copies
	// the new value into the result register.
	for _, test := range []struct {
		name  string
		op    int16
		left  Value
		param Value
		want  Value
	}{
		{name: "LORP", op: 29, left: IntegerValue(0), param: IntegerValue(5), want: IntegerValue(1)},
		{name: "LANDP", op: 33, left: IntegerValue(1), param: IntegerValue(0), want: IntegerValue(0)},
		{name: "BORP", op: 37, left: IntegerValue(8), param: IntegerValue(4), want: IntegerValue(12)},
		{name: "BXORP", op: 41, left: IntegerValue(12), param: IntegerValue(10), want: IntegerValue(6)},
		{name: "BANDP", op: 45, left: IntegerValue(12), param: IntegerValue(10), want: IntegerValue(8)},
	} {
		t.Run(test.name, func(t *testing.T) {
			// CONST r0, left; CONST r2, param; op r1, r0, r2; SRV r0; RET
			bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
				MaxVariables: 3,
				Code:         []int16{1, 0, 0, 1, 2, 1, test.op, 1, 0, 2, 118, 0, 119},
				Constants:    []BytecodeConstant{{Value: test.left}, {Value: test.param}},
			}}}
			got, err := bytecode.ExecuteLegacy(0, VoidValue)
			if err != nil {
				t.Fatal(err)
			}
			if !got.StrictEqual(test.want) {
				t.Fatalf("%s result = %v (kind %v), want %v", test.name, got, got.Kind(), test.want)
			}
		})
	}
}

func TestExecuteLegacyBitwiseMemberlessZeroResultRegister(t *testing.T) {
	// The P form with a zero result register must only update the target.
	// CONST r0, 12; CONST r1, 10; BANDP r0, r0, r1; SRV r0; RET
	bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
		MaxVariables: 2,
		Code:         []int16{1, 0, 0, 1, 1, 1, 45, 0, 0, 1, 118, 0, 119},
		Constants:    []BytecodeConstant{{Value: IntegerValue(12)}, {Value: IntegerValue(10)}},
	}}}
	got, err := bytecode.ExecuteLegacy(0, VoidValue)
	if err != nil {
		t.Fatal(err)
	}
	if !got.StrictEqual(IntegerValue(8)) {
		t.Fatalf("BANDP result = %v (kind %v), want integer 8", got, got.Kind())
	}
}

func TestExecuteLegacyBitwiseConversionErrorContext(t *testing.T) {
	table := NewObjectTable()
	target := table.New(CategoryDictionary)
	if err := target.Set("blob", ObjectValue(NewObjectRef(1, 0))); err != nil {
		t.Fatal(err)
	}
	t.Run("register form", func(t *testing.T) {
		bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
			MaxVariables: 2,
			Code:         []int16{1, 0, 0, 1, 1, 1, 42, 0, 1, 118, 0, 119},
			Constants:    []BytecodeConstant{{Value: ObjectValue(NewObjectRef(1, 0))}, {Value: IntegerValue(2)}},
		}}}
		_, err := bytecode.ExecuteLegacy(0, VoidValue)
		if err == nil || !strings.Contains(err.Error(), "legacy intercode object 0 code offset 6: BAND: cannot convert TJS Object to number") {
			t.Fatalf("BAND error = %v, want conversion error with legacy bytecode location", err)
		}
	})
	t.Run("property form", func(t *testing.T) {
		bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
			MaxVariables: 3,
			Code:         []int16{1, 0, 0, 1, 2, 2, 35, 1, 0, 1, 2, 118, 1, 119},
			Constants: []BytecodeConstant{
				{Value: ObjectValue(target.Ref())},
				{Value: StringValue("blob")},
				{Value: IntegerValue(1)},
			},
		}}}
		_, err := bytecode.ExecuteLegacy(0, VoidValue, table)
		if err == nil || !strings.Contains(err.Error(), "legacy intercode object 0 code offset 6: BORPD: cannot convert TJS Object to number") {
			t.Fatalf("BORPD error = %v, want conversion error with legacy bytecode location", err)
		}
	})
}

func TestDecodeLegacyArithmeticInstructionLengths(t *testing.T) {
	// All six arithmetic base opcodes and their PD/PI/P suffixes must decode
	// with the operand widths the reference VM's TJS_DEF_VM_P macro consumes.
	bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
		Code: []int16{
			58, 0, 0, // ADD
			59, 0, 0, 0, 0, // ADDPD
			60, 0, 0, 0, 0, // ADDPI
			61, 0, 0, 0, // ADDP
			62, 0, 0, // SUB
			63, 0, 0, 0, 0, // SUBPD
			64, 0, 0, 0, 0, // SUBPI
			65, 0, 0, 0, // SUBP
			66, 0, 0, // MOD
			67, 0, 0, 0, 0, // MODPD
			68, 0, 0, 0, 0, // MODPI
			69, 0, 0, 0, // MODP
			70, 0, 0, // DIV
			71, 0, 0, 0, 0, // DIVPD
			72, 0, 0, 0, 0, // DIVPI
			73, 0, 0, 0, // DIVP
			74, 0, 0, // IDIV
			75, 0, 0, 0, 0, // IDIVPD
			76, 0, 0, 0, 0, // IDIVPI
			77, 0, 0, 0, // IDIVP
			78, 0, 0, // MUL
			79, 0, 0, 0, 0, // MULPD
			80, 0, 0, 0, 0, // MULPI
			81, 0, 0, 0, // MULP
			119, // RET
		},
	}}}
	instructions, err := bytecode.DecodeLegacyInstructions(0)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		offset  int
		name    string
		opcount int
	}{
		{0, "ADD", 2}, {3, "ADDPD", 4}, {8, "ADDPI", 4}, {13, "ADDP", 3},
		{17, "SUB", 2}, {20, "SUBPD", 4}, {25, "SUBPI", 4}, {30, "SUBP", 3},
		{34, "MOD", 2}, {37, "MODPD", 4}, {42, "MODPI", 4}, {47, "MODP", 3},
		{51, "DIV", 2}, {54, "DIVPD", 4}, {59, "DIVPI", 4}, {64, "DIVP", 3},
		{68, "IDIV", 2}, {71, "IDIVPD", 4}, {76, "IDIVPI", 4}, {81, "IDIVP", 3},
		{85, "MUL", 2}, {88, "MULPD", 4}, {93, "MULPI", 4}, {98, "MULP", 3},
		{102, "RET", 0},
	}
	if len(instructions) != len(want) {
		t.Fatalf("decoded %d instructions, want %d: %#v", len(instructions), len(want), instructions)
	}
	for i, expect := range want {
		got := instructions[i]
		if got.Offset != expect.offset || got.Name != expect.name || len(got.Operands) != expect.opcount {
			t.Fatalf("instruction %d = offset %d %s with %d operands, want offset %d %s with %d operands",
				i, got.Offset, got.Name, len(got.Operands), expect.offset, expect.name, expect.opcount)
		}
	}
}

func TestDecodeLegacyArithmeticTruncated(t *testing.T) {
	for _, test := range []struct {
		name   string
		code   []int16
		opcode string
	}{
		{name: "ADD register form", code: []int16{58, 0}, opcode: "ADD"},
		{name: "MULP memberless form", code: []int16{81, 0, 0}, opcode: "MULP"},
		{name: "SUBPD direct form", code: []int16{63, 0, 0, 0}, opcode: "SUBPD"},
		{name: "IDIVPI indirect form", code: []int16{76, 0, 0, 0}, opcode: "IDIVPI"},
	} {
		t.Run(test.name, func(t *testing.T) {
			bytecode := &DecodedBytecode{Objects: []BytecodeObject{{Code: test.code}}}
			_, err := bytecode.DecodeLegacyInstructions(0)
			if err == nil || !strings.Contains(err.Error(), "truncated "+test.opcode+" instruction") {
				t.Fatalf("decode error = %v, want truncated %s instruction", err, test.opcode)
			}
		})
	}
}

func TestExecuteLegacyArithmeticRegister(t *testing.T) {
	// The register form replaces the left register in place, so the result is
	// observed by returning the left register itself. DIV follows TJS's
	// floating-point division, MOD the integer remainder and IDIV the
	// reference VM's idivequal integer division.
	for _, test := range []struct {
		name        string
		op          int16
		left, right Value
		want        Value
	}{
		{name: "ADD adds integers", op: 58, left: IntegerValue(3), right: IntegerValue(4), want: IntegerValue(7)},
		{name: "ADD concatenates when either side is a string", op: 58, left: StringValue("a"), right: IntegerValue(2), want: StringValue("a2")},
		{name: "SUB subtracts integers", op: 62, left: IntegerValue(10), right: IntegerValue(3), want: IntegerValue(7)},
		{name: "SUB converts a numeric string", op: 62, left: StringValue("10"), right: IntegerValue(3), want: IntegerValue(7)},
		{name: "MOD keeps the integer remainder", op: 66, left: IntegerValue(17), right: IntegerValue(5), want: IntegerValue(2)},
		{name: "DIV performs floating-point division", op: 70, left: IntegerValue(7), right: IntegerValue(2), want: RealValue(3.5)},
		{name: "IDIV truncates toward zero", op: 74, left: IntegerValue(17), right: IntegerValue(5), want: IntegerValue(3)},
		{name: "MUL multiplies integers", op: 78, left: IntegerValue(6), right: IntegerValue(7), want: IntegerValue(42)},
		{name: "MUL promotes to real", op: 78, left: RealValue(2.5), right: IntegerValue(2), want: RealValue(5)},
	} {
		t.Run(test.name, func(t *testing.T) {
			bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
				MaxVariables: 2,
				Code:         []int16{1, 0, 0, 1, 1, 1, test.op, 0, 1, 118, 0, 119},
				Constants:    []BytecodeConstant{{Value: test.left}, {Value: test.right}},
			}}}
			got, err := bytecode.ExecuteLegacy(0, VoidValue)
			if err != nil {
				t.Fatal(err)
			}
			if !got.StrictEqual(test.want) {
				t.Fatalf("%s result = %v (kind %v), want %v (kind %v)", test.name, got, got.Kind(), test.want, test.want.Kind())
			}
		})
	}
}

func TestExecuteLegacyArithmeticPropertyDirect(t *testing.T) {
	// The PD form reads a dictionary member by constant name, applies the
	// operator and stores the new value into both the member and the optional
	// result register.
	for _, test := range []struct {
		name  string
		op    int16
		start Value
		param Value
		want  Value
	}{
		{name: "ADDPD", op: 59, start: IntegerValue(3), param: IntegerValue(4), want: IntegerValue(7)},
		{name: "SUBPD", op: 63, start: IntegerValue(10), param: IntegerValue(3), want: IntegerValue(7)},
		{name: "MODPD", op: 67, start: IntegerValue(17), param: IntegerValue(5), want: IntegerValue(2)},
		{name: "DIVPD", op: 71, start: IntegerValue(7), param: IntegerValue(2), want: RealValue(3.5)},
		{name: "IDIVPD", op: 75, start: IntegerValue(17), param: IntegerValue(5), want: IntegerValue(3)},
		{name: "MULPD", op: 79, start: IntegerValue(6), param: IntegerValue(7), want: IntegerValue(42)},
	} {
		t.Run(test.name, func(t *testing.T) {
			table := NewObjectTable()
			target := table.New(CategoryDictionary)
			if err := target.Set("acc", test.start); err != nil {
				t.Fatal(err)
			}
			// CONST r0, target; CONST r2, param; op r1, r0, "acc", r2; SRV r1; RET
			bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
				MaxVariables: 3,
				Code:         []int16{1, 0, 0, 1, 2, 2, test.op, 1, 0, 1, 2, 118, 1, 119},
				Constants: []BytecodeConstant{
					{Value: ObjectValue(target.Ref())},
					{Value: StringValue("acc")},
					{Value: test.param},
				},
			}}}
			got, err := bytecode.ExecuteLegacy(0, VoidValue, table)
			if err != nil {
				t.Fatal(err)
			}
			if !got.StrictEqual(test.want) {
				t.Fatalf("%s result = %v (kind %v), want %v", test.name, got, got.Kind(), test.want)
			}
			stored, ok := target.Get("acc")
			if !ok || !stored.StrictEqual(test.want) {
				t.Fatalf("%s member acc = %v (present %v), want %v", test.name, stored, ok, test.want)
			}
		})
	}
}

func TestExecuteLegacyArithmeticPropertyIndirect(t *testing.T) {
	// MULPI r1, r0, r2, r3 -- the member key comes from a register. Array
	// indices are canonicalized to strings, as in the reference VM.
	table := NewObjectTable()
	target := table.New(CategoryArray)
	if err := target.Set("1", IntegerValue(12)); err != nil {
		t.Fatal(err)
	}
	bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
		MaxVariables: 4,
		Code:         []int16{1, 0, 0, 1, 2, 1, 1, 3, 2, 80, 1, 0, 2, 3, 118, 1, 119},
		Constants: []BytecodeConstant{
			{Value: ObjectValue(target.Ref())},
			{Value: IntegerValue(1)},
			{Value: IntegerValue(10)},
		},
	}}}
	got, err := bytecode.ExecuteLegacy(0, VoidValue, table)
	if err != nil {
		t.Fatal(err)
	}
	if !got.StrictEqual(IntegerValue(120)) {
		t.Fatalf("MULPI result = %v (kind %v), want integer 120", got, got.Kind())
	}
	stored, ok := target.Get("1")
	if !ok || !stored.StrictEqual(IntegerValue(120)) {
		t.Fatalf("member 1 = %v (present %v), want integer 120", stored, ok)
	}
}

func TestExecuteLegacyArithmeticPropertyMemberless(t *testing.T) {
	// The P form replaces the target register in place and optionally copies
	// the new value into the result register.
	for _, test := range []struct {
		name  string
		op    int16
		left  Value
		param Value
		want  Value
	}{
		{name: "ADDP", op: 61, left: IntegerValue(3), param: IntegerValue(4), want: IntegerValue(7)},
		{name: "SUBP", op: 65, left: IntegerValue(10), param: IntegerValue(3), want: IntegerValue(7)},
		{name: "MODP", op: 69, left: IntegerValue(17), param: IntegerValue(5), want: IntegerValue(2)},
		{name: "DIVP", op: 73, left: IntegerValue(7), param: IntegerValue(2), want: RealValue(3.5)},
		{name: "IDIVP", op: 77, left: IntegerValue(17), param: IntegerValue(5), want: IntegerValue(3)},
		{name: "MULP", op: 81, left: IntegerValue(6), param: IntegerValue(7), want: IntegerValue(42)},
	} {
		t.Run(test.name, func(t *testing.T) {
			// CONST r0, left; CONST r2, param; op r1, r0, r2; SRV r0; RET
			bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
				MaxVariables: 3,
				Code:         []int16{1, 0, 0, 1, 2, 1, test.op, 1, 0, 2, 118, 0, 119},
				Constants:    []BytecodeConstant{{Value: test.left}, {Value: test.param}},
			}}}
			got, err := bytecode.ExecuteLegacy(0, VoidValue)
			if err != nil {
				t.Fatal(err)
			}
			if !got.StrictEqual(test.want) {
				t.Fatalf("%s result = %v (kind %v), want %v", test.name, got, got.Kind(), test.want)
			}
		})
	}
}

func TestExecuteLegacyArithmeticMemberlessZeroResultRegister(t *testing.T) {
	// The P form with a zero result register must only update the target.
	// CONST r0, 6; CONST r1, 7; MULP r0, r0, r1; SRV r0; RET
	bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
		MaxVariables: 2,
		Code:         []int16{1, 0, 0, 1, 1, 1, 81, 0, 0, 1, 118, 0, 119},
		Constants:    []BytecodeConstant{{Value: IntegerValue(6)}, {Value: IntegerValue(7)}},
	}}}
	got, err := bytecode.ExecuteLegacy(0, VoidValue)
	if err != nil {
		t.Fatal(err)
	}
	if !got.StrictEqual(IntegerValue(42)) {
		t.Fatalf("MULP result = %v (kind %v), want integer 42", got, got.Kind())
	}
}

func TestExecuteLegacyArithmeticIntegerDivideByZero(t *testing.T) {
	// MOD and IDIV share TJS's integer operators, which raise the
	// "TJS integer division by zero" error on a zero divisor; DIV is the
	// floating-point operator and yields +Inf instead of raising.
	for _, test := range []struct {
		name string
		op   int16
	}{
		{name: "MOD", op: 66},
		{name: "IDIV", op: 74},
	} {
		t.Run(test.name, func(t *testing.T) {
			bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
				MaxVariables: 2,
				Code:         []int16{1, 0, 0, 1, 1, 1, test.op, 0, 1, 118, 0, 119},
				Constants:    []BytecodeConstant{{Value: IntegerValue(7)}, {Value: IntegerValue(0)}},
			}}}
			_, err := bytecode.ExecuteLegacy(0, VoidValue)
			if err == nil || !strings.Contains(err.Error(), "TJS integer division by zero") {
				t.Fatalf("%s error = %v, want integer division by zero", test.name, err)
			}
		})
	}
	t.Run("DIV returns real infinity", func(t *testing.T) {
		bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
			MaxVariables: 2,
			Code:         []int16{1, 0, 0, 1, 1, 1, 70, 0, 1, 118, 0, 119},
			Constants:    []BytecodeConstant{{Value: IntegerValue(7)}, {Value: IntegerValue(0)}},
		}}}
		got, err := bytecode.ExecuteLegacy(0, VoidValue)
		if err != nil {
			t.Fatal(err)
		}
		if got.Kind() != KindReal {
			t.Fatalf("DIV result kind = %v, want real", got.Kind())
		}
		if f, err := got.Float(); err != nil || !math.IsInf(f, 1) {
			t.Fatalf("DIV result = %v, want +Inf", got)
		}
	})
}

func TestExecuteLegacyArithmeticConversionErrorContext(t *testing.T) {
	table := NewObjectTable()
	target := table.New(CategoryDictionary)
	if err := target.Set("blob", ObjectValue(NewObjectRef(1, 0))); err != nil {
		t.Fatal(err)
	}
	t.Run("register form", func(t *testing.T) {
		bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
			MaxVariables: 2,
			Code:         []int16{1, 0, 0, 1, 1, 1, 66, 0, 1, 118, 0, 119},
			Constants:    []BytecodeConstant{{Value: ObjectValue(NewObjectRef(1, 0))}, {Value: IntegerValue(2)}},
		}}}
		_, err := bytecode.ExecuteLegacy(0, VoidValue)
		if err == nil || !strings.Contains(err.Error(), "legacy intercode object 0 code offset 6: MOD: cannot convert TJS Object to number") {
			t.Fatalf("MOD error = %v, want conversion error with legacy bytecode location", err)
		}
	})
	t.Run("property form", func(t *testing.T) {
		bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
			MaxVariables: 3,
			Code:         []int16{1, 0, 0, 1, 2, 2, 79, 1, 0, 1, 2, 118, 1, 119},
			Constants: []BytecodeConstant{
				{Value: ObjectValue(target.Ref())},
				{Value: StringValue("blob")},
				{Value: IntegerValue(1)},
			},
		}}}
		_, err := bytecode.ExecuteLegacy(0, VoidValue, table)
		if err == nil || !strings.Contains(err.Error(), "legacy intercode object 0 code offset 6: MULPD: cannot convert TJS Object to number") {
			t.Fatalf("MULPD error = %v, want conversion error with legacy bytecode location", err)
		}
	})
}

func TestDecodeLegacyConversionInstructionLengths(t *testing.T) {
	// BNOT, TYPEOFI, EVAL, EEXP, CHKINS, ASC, CHR, CHS, INV, CHKINV, INT,
	// REAL and OCTET must decode with the operand widths the reference VM's
	// intercode interpreter consumes.
	bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
		Code: []int16{
			82, 0, // BNOT
			85, 0, 0, 0, // TYPEOFI
			86, 0, // EVAL
			87, 0, // EEXP
			88, 0, 0, // CHKINS
			89, 0, // ASC
			90, 0, // CHR
			92, 0, // CHS
			93, 0, // INV
			94, 0, // CHKINV
			95, 0, // INT
			96, 0, // REAL
			98, 0, // OCTET
			119, // RET
		},
	}}}
	instructions, err := bytecode.DecodeLegacyInstructions(0)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		offset  int
		name    string
		opcount int
	}{
		{0, "BNOT", 1}, {2, "TYPEOFI", 3}, {6, "EVAL", 1}, {8, "EEXP", 1},
		{10, "CHKINS", 2}, {13, "ASC", 1}, {15, "CHR", 1}, {17, "CHS", 1},
		{19, "INV", 1}, {21, "CHKINV", 1}, {23, "INT", 1}, {25, "REAL", 1},
		{27, "OCTET", 1}, {29, "RET", 0},
	}
	if len(instructions) != len(want) {
		t.Fatalf("decoded %d instructions, want %d: %#v", len(instructions), len(want), instructions)
	}
	for i, expect := range want {
		got := instructions[i]
		if got.Offset != expect.offset || got.Name != expect.name || len(got.Operands) != expect.opcount {
			t.Fatalf("instruction %d = offset %d %s with %d operands, want offset %d %s with %d operands",
				i, got.Offset, got.Name, len(got.Operands), expect.offset, expect.name, expect.opcount)
		}
	}
}

func TestDecodeLegacyConversionTruncated(t *testing.T) {
	for _, test := range []struct {
		name   string
		code   []int16
		opcode string
	}{
		{name: "BNOT", code: []int16{82}, opcode: "BNOT"},
		{name: "TYPEOFI", code: []int16{85, 0, 0}, opcode: "TYPEOFI"},
		{name: "CHKINS", code: []int16{88, 0}, opcode: "CHKINS"},
		{name: "OCTET", code: []int16{98}, opcode: "OCTET"},
	} {
		t.Run(test.name, func(t *testing.T) {
			bytecode := &DecodedBytecode{Objects: []BytecodeObject{{Code: test.code}}}
			_, err := bytecode.DecodeLegacyInstructions(0)
			if err == nil || !strings.Contains(err.Error(), "truncated "+test.opcode+" instruction") {
				t.Fatalf("decode error = %v, want truncated %s instruction", err, test.opcode)
			}
		})
	}
}

func TestExecuteLegacyUnaryConversions(t *testing.T) {
	// Every one of these opcodes rewrites its single register in place; the
	// value is observed by returning that same register through SRV.
	tests := []struct {
		name  string
		op    int16
		input Value
		want  Value
	}{
		{name: "BNOT integer", op: 82, input: IntegerValue(5), want: IntegerValue(-6)},
		{name: "BNOT numeric string", op: 82, input: StringValue("5"), want: IntegerValue(-6)},
		{name: "BNOT real truncates", op: 82, input: RealValue(2.9), want: IntegerValue(-3)},
		{name: "ASC string", op: 89, input: StringValue("A"), want: IntegerValue(65)},
		{name: "ASC numeric text", op: 89, input: IntegerValue(66), want: IntegerValue(54)},
		{name: "ASC void", op: 89, input: VoidValue, want: IntegerValue(0)},
		{name: "CHR integer", op: 90, input: IntegerValue(65), want: StringValue("A")},
		{name: "CHR numeric string", op: 90, input: StringValue("66"), want: StringValue("B")},
		{name: "CHR void", op: 90, input: VoidValue, want: StringValue("\x00")},
		{name: "CHS integer", op: 92, input: IntegerValue(5), want: IntegerValue(-5)},
		{name: "CHS real", op: 92, input: RealValue(1.5), want: RealValue(-1.5)},
		{name: "CHS numeric string", op: 92, input: StringValue("-7"), want: IntegerValue(7)},
		{name: "CHS void", op: 92, input: VoidValue, want: IntegerValue(0)},
		{name: "INT real truncates", op: 95, input: RealValue(4.9), want: IntegerValue(4)},
		{name: "INT numeric string", op: 95, input: StringValue("12"), want: IntegerValue(12)},
		{name: "INT void", op: 95, input: VoidValue, want: IntegerValue(0)},
		{name: "REAL integer", op: 96, input: IntegerValue(3), want: RealValue(3)},
		{name: "REAL numeric string", op: 96, input: StringValue("2.5"), want: RealValue(2.5)},
		{name: "REAL void", op: 96, input: VoidValue, want: RealValue(0)},
		{name: "OCTET void", op: 98, input: VoidValue, want: OctetValue(nil)},
		{name: "OCTET octet copies", op: 98, input: OctetValue([]byte{1, 2}), want: OctetValue([]byte{1, 2})},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
				MaxVariables: 1,
				Code:         []int16{1, 0, 0, test.op, 0, 118, 0, 119},
				Constants:    []BytecodeConstant{{Value: test.input}},
			}}}
			got, err := bytecode.ExecuteLegacy(0, VoidValue)
			if err != nil {
				t.Fatal(err)
			}
			if !got.StrictEqual(test.want) {
				t.Fatalf("%s result = %#v (kind %v); want %#v (kind %v)", legacyOpcodeNames[test.op], got, got.Kind(), test.want, test.want.Kind())
			}
		})
	}
}

func TestExecuteLegacyUnaryConversionErrorContext(t *testing.T) {
	for _, test := range []struct {
		name   string
		op     int16
		input  Value
		reason string
	}{
		{name: "BNOT object", op: 82, input: ObjectValue(NewObjectRef(1, 0)), reason: "cannot convert TJS Object to number"},
		{name: "ASC octet", op: 89, input: OctetValue([]byte{1}), reason: "cannot convert TJS Octet to String"},
		{name: "CHR object", op: 90, input: ObjectValue(NewObjectRef(1, 0)), reason: "cannot convert TJS Object to number"},
		{name: "CHS octet", op: 92, input: OctetValue([]byte{1}), reason: "cannot convert TJS Octet to number"},
		{name: "INT object", op: 95, input: ObjectValue(NewObjectRef(1, 0)), reason: "cannot convert TJS Object to number"},
		{name: "REAL octet", op: 96, input: OctetValue([]byte{1}), reason: "cannot convert TJS Octet to number"},
		{name: "OCTET string", op: 98, input: StringValue("x"), reason: "cannot convert TJS String to Octet"},
		{name: "OCTET object", op: 98, input: ObjectValue(NewObjectRef(1, 0)), reason: "cannot convert TJS Object to Octet"},
	} {
		t.Run(test.name, func(t *testing.T) {
			bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
				MaxVariables: 1,
				Code:         []int16{1, 0, 0, test.op, 0, 118, 0, 119},
				Constants:    []BytecodeConstant{{Value: test.input}},
			}}}
			_, err := bytecode.ExecuteLegacy(0, VoidValue)
			want := "legacy intercode object 0 code offset 3: " + legacyOpcodeNames[test.op] + ": " + test.reason
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("%s error = %v, want %q", legacyOpcodeNames[test.op], err, want)
			}
		})
	}
}

func TestExecuteLegacyTYPEOFIIndirectMember(t *testing.T) {
	for _, test := range []struct {
		name    string
		value   Value
		present bool
		want    string
	}{
		{name: "existing member", value: IntegerValue(42), present: true, want: "Integer"},
		{name: "missing member", want: "undefined"},
	} {
		t.Run(test.name, func(t *testing.T) {
			table := NewObjectTable()
			global := table.New(CategoryDictionary)
			target := table.New(CategoryDictionary)
			if test.present {
				if err := target.Set("value", test.value); err != nil {
					t.Fatal(err)
				}
			}
			bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
				MaxVariables: 3,
				Code: []int16{
					1, 0, 0, // CONST ra0 = target object
					1, 1, 1, // CONST ra1 = "value"
					85, 2, 0, 1, // TYPEOFI ra2 = typeof ra0[ra1]
					104, -1, 2, 2, // SPD this["result"] = ra2
					119,
				},
				Constants: []BytecodeConstant{
					{Value: ObjectValue(target.Ref())},
					{Value: StringValue("value")},
					{Value: StringValue("result")},
				},
			}}}
			if _, err := bytecode.ExecuteLegacy(0, ObjectValue(global.Ref()), table); err != nil {
				t.Fatal(err)
			}
			got, ok := global.Get("result")
			if !ok || got.Kind() != KindString || got.Text() != test.want {
				t.Fatalf("TYPEOFI result = %v (kind %v), present %v; want %q", got, got.Kind(), ok, test.want)
			}
		})
	}
}

func TestExecuteLegacyEVALAndEEXP(t *testing.T) {
	table := NewObjectTable()
	global := table.New(CategoryDictionary)
	globalValue := ObjectValue(global.Ref())
	for _, test := range []struct {
		name      string
		op        int16
		source    Value
		result    Value
		wantValue Value
		wantNeed  bool
		wantCalls int
	}{
		{name: "EVAL stores the result", op: 86, source: StringValue("1+2"), result: IntegerValue(3), wantValue: IntegerValue(3), wantNeed: true, wantCalls: 1},
		{name: "EEXP discards the result", op: 87, source: StringValue("1+2"), result: IntegerValue(3), wantValue: StringValue("1+2"), wantNeed: false, wantCalls: 1},
		{name: "empty source is a no-op", op: 86, source: StringValue(""), result: IntegerValue(3), wantValue: StringValue(""), wantCalls: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			type call struct {
				source  string
				context Value
				need    bool
			}
			var calls []call
			evaluate := func(source string, context Value, need bool) (Value, error) {
				calls = append(calls, call{source: source, context: context, need: need})
				return test.result, nil
			}
			bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
				MaxVariables: 1,
				Code:         []int16{1, 0, 0, test.op, 0, 118, 0, 119},
				Constants:    []BytecodeConstant{{Value: test.source}},
			}}}
			got, err := bytecode.ExecuteLegacyWithEval(0, globalValue, evaluate)
			if err != nil {
				t.Fatal(err)
			}
			if len(calls) != test.wantCalls {
				t.Fatalf("%s invoked the evaluator %d times, want %d", legacyOpcodeNames[test.op], len(calls), test.wantCalls)
			}
			if test.wantCalls != 0 {
				first := calls[0]
				if first.source != test.source.Text() || first.need != test.wantNeed {
					t.Fatalf("%s call = (%q, need %v), want (%q, need %v)", legacyOpcodeNames[test.op], first.source, first.need, test.source.Text(), test.wantNeed)
				}
				if !first.context.StrictEqual(globalValue) {
					t.Fatalf("%s context = %v, want the this-context global", legacyOpcodeNames[test.op], first.context)
				}
			}
			if !got.StrictEqual(test.wantValue) {
				t.Fatalf("%s result = %v (kind %v); want %v (kind %v)", legacyOpcodeNames[test.op], got, got.Kind(), test.wantValue, test.wantValue.Kind())
			}
		})
	}
	t.Run("evaluator error is wrapped", func(t *testing.T) {
		evaluate := func(string, Value, bool) (Value, error) { return VoidValue, errors.New("boom") }
		bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
			MaxVariables: 1,
			Code:         []int16{1, 0, 0, 86, 0, 118, 0, 119},
			Constants:    []BytecodeConstant{{Value: StringValue("1+2")}},
		}}}
		_, err := bytecode.ExecuteLegacyWithEval(0, VoidValue, evaluate)
		if err == nil || !strings.Contains(err.Error(), "legacy intercode object 0 code offset 3: EVAL: boom") {
			t.Fatalf("EVAL error = %v", err)
		}
	})
	t.Run("missing evaluator support", func(t *testing.T) {
		bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
			MaxVariables: 1,
			Code:         []int16{1, 0, 0, 87, 0, 118, 0, 119},
			Constants:    []BytecodeConstant{{Value: StringValue("1+2")}},
		}}}
		_, err := bytecode.ExecuteLegacy(0, VoidValue)
		if err == nil || !strings.Contains(err.Error(), "EEXP requires evaluation support") {
			t.Fatalf("EEXP error = %v", err)
		}
	})
}

func TestExecuteLegacyCHKINS(t *testing.T) {
	vm := NewVM()
	for _, test := range []struct {
		name  string
		value Value
		class Value
		want  int64
	}{
		{name: "integer is Number", value: IntegerValue(1), class: StringValue("Number"), want: 1},
		{name: "string is String", value: StringValue("x"), class: StringValue("String"), want: 1},
		{name: "integer is not String", value: IntegerValue(1), class: StringValue("String"), want: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
				MaxVariables: 2,
				Code:         []int16{1, 0, 0, 1, 1, 1, 88, 0, 1, 118, 0, 119},
				Constants:    []BytecodeConstant{{Value: test.value}, {Value: test.class}},
			}}}
			got, err := bytecode.ExecuteLegacyWithContextAndConstructionAndMemberResolversAndVMOps(
				0, VoidValue, VoidValue, nil, nil, nil, nil, nil, vm.InstanceOfValue, nil, nil, vm.Objects,
			)
			if err != nil {
				t.Fatal(err)
			}
			if got.Kind() != KindInteger || got.IntegerValue() != test.want {
				t.Fatalf("CHKINS result = %v (kind %v); want %d", got, got.Kind(), test.want)
			}
		})
	}
	t.Run("missing instanceof support", func(t *testing.T) {
		bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
			MaxVariables: 2,
			Code:         []int16{1, 0, 0, 1, 1, 1, 88, 0, 1, 119},
			Constants:    []BytecodeConstant{{Value: IntegerValue(1)}, {Value: StringValue("Number")}},
		}}}
		_, err := bytecode.ExecuteLegacy(0, VoidValue)
		if err == nil || !strings.Contains(err.Error(), "CHKINS requires instanceof support") {
			t.Fatalf("CHKINS error = %v", err)
		}
	})
}

// lifecycleBytecode builds "CONST ra0 = constant; <op> ra0; SRV ra0; RET".
func lifecycleBytecode(value Value, op int16) *DecodedBytecode {
	return &DecodedBytecode{Objects: []BytecodeObject{{
		MaxVariables: 1,
		Code:         []int16{1, 0, 0, op, 0, 118, 0, 119},
		Constants:    []BytecodeConstant{{Value: value}},
	}}}
}

func TestExecuteLegacyINVAndCHKINVThroughVM(t *testing.T) {
	vm := NewVM()
	object := vm.Objects.New(CategoryDictionary)
	value := ObjectValue(object.Ref())
	invalidate := func(value Value) (Value, error) { return vm.Invalidate(value, nil, nil) }
	isValid := func(value Value) Value { return vm.IsValid(value) }
	check := func(t *testing.T, op int16, want int64) {
		t.Helper()
		got, err := lifecycleBytecode(value, op).ExecuteLegacyWithContextAndConstructionAndMemberResolversAndVMOps(
			0, VoidValue, VoidValue, nil, nil, nil, nil, nil, nil, invalidate, isValid, vm.Objects,
		)
		if err != nil {
			t.Fatal(err)
		}
		if got.Kind() != KindInteger || got.IntegerValue() != want {
			t.Fatalf("opcode %s result = %v (kind %v); want %d", legacyOpcodeNames[op], got, got.Kind(), want)
		}
	}
	check(t, 94, 1) // CHKINV reports the fresh object valid
	check(t, 93, 1) // INV invalidates it
	check(t, 94, 0) // CHKINV now reports it invalid
	check(t, 93, 0) // INV on an already-invalid object yields false
}

func TestExecuteLegacyINVAndCHKINVUseHooks(t *testing.T) {
	table := NewObjectTable()
	object := table.New(CategoryDictionary)
	var invalidated, checked int
	invalidate := func(Value) (Value, error) { invalidated++; return IntegerValue(9), nil }
	isValid := func(Value) Value { checked++; return IntegerValue(8) }
	run := func(t *testing.T, op int16) Value {
		t.Helper()
		got, err := lifecycleBytecode(ObjectValue(object.Ref()), op).ExecuteLegacyWithContextAndConstructionAndMemberResolversAndVMOps(
			0, VoidValue, VoidValue, nil, nil, nil, nil, nil, nil, invalidate, isValid, table,
		)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := run(t, 94); got.IntegerValue() != 8 {
		t.Fatalf("CHKINV result = %v; want the isValid hook sentinel 8", got)
	}
	if got := run(t, 93); got.IntegerValue() != 9 {
		t.Fatalf("INV result = %v; want the invalidate hook sentinel 9", got)
	}
	if checked != 1 || invalidated != 1 {
		t.Fatalf("hooks called %d/%d times, want 1/1", checked, invalidated)
	}
	if !object.IsValid() {
		t.Fatal("the hooked INV must not touch the object table validity bit")
	}
}

func TestExecuteLegacyINVAndCHKINVObjectTableFallback(t *testing.T) {
	table := NewObjectTable()
	object := table.New(CategoryDictionary)
	run := func(t *testing.T, op int16) Value {
		t.Helper()
		got, err := lifecycleBytecode(ObjectValue(object.Ref()), op).ExecuteLegacyWithContextAndConstructionAndMemberResolvers(
			0, VoidValue, VoidValue, nil, nil, nil, nil, nil, table,
		)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := run(t, 94); got.IntegerValue() != 1 {
		t.Fatalf("CHKINV before invalidation = %v; want 1", got)
	}
	if got := run(t, 93); got.IntegerValue() != 1 {
		t.Fatalf("INV = %v; want 1", got)
	}
	if got := run(t, 94); got.IntegerValue() != 0 {
		t.Fatalf("CHKINV after invalidation = %v; want 0", got)
	}
	if got := run(t, 93); got.IntegerValue() != 0 {
		t.Fatalf("INV on an already-invalid object = %v; want 0", got)
	}
}

func TestExecuteLegacyINVAndCHKINVNonObjects(t *testing.T) {
	for _, test := range []struct {
		name  string
		value Value
	}{
		{name: "void", value: VoidValue},
		{name: "null object", value: NullValue},
		{name: "integer", value: IntegerValue(1)},
		{name: "string", value: StringValue("x")},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, op := range []int16{93, 94} {
				got, err := lifecycleBytecode(test.value, op).ExecuteLegacy(0, VoidValue)
				if err != nil {
					t.Fatal(err)
				}
				want := int64(0)
				if op == 94 {
					want = 1
				}
				if got.Kind() != KindInteger || got.IntegerValue() != want {
					t.Fatalf("%s on %s = %v (kind %v); want %d", legacyOpcodeNames[op], test.name, got, got.Kind(), want)
				}
			}
		})
	}
}

func TestDecodeLegacyIndirectMemberInstructionLengths(t *testing.T) {
	bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
		Code: []int16{
			101, 0, 1, 2, 1, 3, // CALLI r0, r1, r2, 1, r3
			101, 0, 1, 2, -2, 1, 3, 0, // CALLI with one fatNormal descriptor
			114, 1, 0, // SETP *r1 = r0
			115, 0, 1, // GETP r0, r1
			117, 0, 1, 2, // DELI r0, r1, r2
			127, // DEBUGGER
			119, // RET
		},
	}}}
	instructions, err := bytecode.DecodeLegacyInstructions(0)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		opcode   int16
		offset   int
		operands int
	}{
		{opcode: 101, offset: 0, operands: 5},
		{opcode: 101, offset: 6, operands: 7},
		{opcode: 114, offset: 14, operands: 2},
		{opcode: 115, offset: 17, operands: 2},
		{opcode: 117, offset: 20, operands: 3},
		{opcode: 127, offset: 24, operands: 0},
		{opcode: 119, offset: 25, operands: 0},
	}
	if len(instructions) != len(want) {
		t.Fatalf("decoded %d instructions, want %d: %#v", len(instructions), len(want), instructions)
	}
	for i, expected := range want {
		got := instructions[i]
		if got.Opcode != expected.opcode || got.Offset != expected.offset || len(got.Operands) != expected.operands {
			t.Fatalf("instruction %d = %s at %d with %v; want opcode %d at %d with %d operands",
				i, got.Name, got.Offset, got.Operands, expected.opcode, expected.offset, expected.operands)
		}
	}
}

func TestExecuteLegacyCALLIUsesRegisterMemberName(t *testing.T) {
	table := NewObjectTable()
	global := table.New(CategoryDictionary)
	object := table.New(CategoryDictionary)
	method := NewNativeFunction(table, "method", func(ObjectRef, []Value) (Value, error) {
		return VoidValue, nil
	})
	if err := object.Set("method", method); err != nil {
		t.Fatal(err)
	}
	bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
		MaxVariables: 4,
		Code: []int16{
			1, 0, 0, // CONST r0, object
			1, 1, 1, // CONST r1, member name
			1, 2, 2, // CONST r2, argument
			101, 3, 0, 1, 1, 2, // CALLI r3, r0, r1, 1, r2
			104, -1, 3, 3, // SPD global, "result", r3
			119,
		},
		Constants: []BytecodeConstant{
			{Value: ObjectValue(object.Ref())},
			{Value: StringValue("method")},
			{Value: IntegerValue(42)},
			{Value: StringValue("result")},
		},
	}}}
	instructions, err := bytecode.DecodeLegacyInstructions(0)
	if err != nil {
		t.Fatal(err)
	}
	var indirect *LegacyInstruction
	for i := range instructions {
		if instructions[i].Opcode == 101 {
			indirect = &instructions[i]
			break
		}
	}
	if indirect == nil || len(indirect.Operands) != 5 || indirect.Operands[2] != 1 || indirect.Operands[3] != 1 || indirect.Operands[4] != 2 {
		t.Fatalf("decoded CALLI = %#v; want result r3, object r0, name r1, one argument r2", indirect)
	}
	var gotCallee, gotReceiver Value
	var gotArgs []Value
	invoke := func(callee, receiver Value, args []Value, resultNeeded bool) (Value, error) {
		gotCallee, gotReceiver = callee, receiver
		gotArgs = append([]Value(nil), args...)
		if !resultNeeded {
			t.Fatal("CALLI discarded-result flag = false, want true")
		}
		return StringValue("called"), nil
	}
	if _, err := bytecode.ExecuteLegacyWithRuntime(0, ObjectValue(global.Ref()), nil, invoke, table); err != nil {
		t.Fatal(err)
	}
	callee, ok := table.Lookup(gotCallee.ObjectRef().Object)
	if !ok {
		t.Fatalf("CALLI callee %v is not in the object table", gotCallee)
	}
	if name, present := callee.Get("name"); !present || !name.StrictEqual(StringValue("method")) {
		t.Fatalf("CALLI callee name = %v (present %v), want the member named by the key register", name, present)
	}
	if !gotReceiver.StrictEqual(ObjectValue(object.Ref())) {
		t.Fatalf("CALLI receiver = %v, want the object register %v", gotReceiver, ObjectValue(object.Ref()))
	}
	if len(gotArgs) != 1 || !gotArgs[0].StrictEqual(IntegerValue(42)) {
		t.Fatalf("CALLI arguments = %v, want [42]", gotArgs)
	}
	if got, present := global.Get("result"); !present || !got.StrictEqual(StringValue("called")) {
		t.Fatalf("CALLI result = %v (present %v), want called", got, present)
	}
}

// callForwardingBytecode builds a frame that loads the supplied object and a
// member name into r0/r1 and then issues the supplied call instruction.
func callForwardingBytecode(object Value, call []int16) *DecodedBytecode {
	code := []int16{
		1, 0, 0, // CONST r0, callee/object
		1, 1, 1, // CONST r1, member name
	}
	code = append(code, call...)
	code = append(code, 119)
	return &DecodedBytecode{Objects: []BytecodeObject{{
		MaxVariables:        3,
		ArgumentCount:       2,
		UnnamedArgumentBase: 2,
		Constants: []BytecodeConstant{
			{Value: object},
			{Value: StringValue("method")},
		},
		Code: code,
	}}}
}

func TestExecuteLegacyCallFormsForwardCallerArguments(t *testing.T) {
	// count == -1 forwards the caller's arguments unmodified, exactly like the
	// reference VM's TJS_BEGIN_FUNC_CALL_ARGS macro.
	for _, test := range []struct {
		name string
		call []int16
	}{
		{name: "CALL", call: []int16{99, 2, 0, -1}},
		{name: "CALLD", call: []int16{100, 2, 0, 1, -1}},
		{name: "CALLI", call: []int16{101, 2, 0, 1, -1}},
		{name: "NEW", call: []int16{102, 2, 0, -1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			table := NewObjectTable()
			global := table.New(CategoryDictionary)
			object := table.New(CategoryDictionary)
			method := NewNativeFunction(table, "method", func(ObjectRef, []Value) (Value, error) {
				return VoidValue, nil
			})
			if err := object.Set("method", method); err != nil {
				t.Fatal(err)
			}
			bytecode := callForwardingBytecode(ObjectValue(object.Ref()), test.call)
			var got []Value
			invoke := func(callee, receiver Value, callArgs []Value, resultNeeded bool) (Value, error) {
				got = append([]Value(nil), callArgs...)
				return VoidValue, nil
			}
			construct := func(class Value, callArgs []Value) (Value, error) {
				got = append([]Value(nil), callArgs...)
				return VoidValue, nil
			}
			if _, err := bytecode.ExecuteLegacyWithContextAndConstructionAndArguments(
				0, ObjectValue(global.Ref()), ObjectValue(global.Ref()),
				[]Value{IntegerValue(7), IntegerValue(8)}, nil, invoke, construct, table,
			); err != nil {
				t.Fatal(err)
			}
			if len(got) != 2 || !got[0].StrictEqual(IntegerValue(7)) || !got[1].StrictEqual(IntegerValue(8)) {
				t.Fatalf("%s forwarded caller arguments = %v; want [7 8]", test.name, got)
			}
		})
	}
}

func TestExecuteLegacyCallFormsForwardUnnamedArguments(t *testing.T) {
	// The fatUnnamedExpand descriptor forwards the current frame's unnamed
	// arguments, which are the caller's arguments from UnnamedArgumentBase on.
	for _, test := range []struct {
		name string
		call []int16
	}{
		{name: "CALL", call: []int16{99, 2, 0, -2, 1, 2, 0}},
		{name: "CALLD", call: []int16{100, 2, 0, 1, -2, 1, 2, 0}},
		{name: "CALLI", call: []int16{101, 2, 0, 1, -2, 1, 2, 0}},
		{name: "NEW", call: []int16{102, 2, 0, -2, 1, 2, 0}},
	} {
		for _, extra := range []bool{false, true} {
			name := test.name
			if extra {
				name += " with extra caller arguments"
			} else {
				name += " without extra caller arguments"
			}
			t.Run(name, func(t *testing.T) {
				table := NewObjectTable()
				global := table.New(CategoryDictionary)
				object := table.New(CategoryDictionary)
				method := NewNativeFunction(table, "method", func(ObjectRef, []Value) (Value, error) {
					return VoidValue, nil
				})
				if err := object.Set("method", method); err != nil {
					t.Fatal(err)
				}
				bytecode := callForwardingBytecode(ObjectValue(object.Ref()), test.call)
				args := []Value{IntegerValue(1), IntegerValue(2)}
				if extra {
					args = append(args, IntegerValue(3), IntegerValue(4))
				}
				var got []Value
				invoke := func(callee, receiver Value, callArgs []Value, resultNeeded bool) (Value, error) {
					got = append([]Value(nil), callArgs...)
					return VoidValue, nil
				}
				construct := func(class Value, callArgs []Value) (Value, error) {
					got = append([]Value(nil), callArgs...)
					return VoidValue, nil
				}
				if _, err := bytecode.ExecuteLegacyWithContextAndConstructionAndArguments(
					0, ObjectValue(global.Ref()), ObjectValue(global.Ref()), args, nil, invoke, construct, table,
				); err != nil {
					t.Fatal(err)
				}
				if !extra {
					if len(got) != 0 {
						t.Fatalf("%s forwarded unnamed arguments = %v; want none", test.name, got)
					}
					return
				}
				if len(got) != 2 || !got[0].StrictEqual(IntegerValue(3)) || !got[1].StrictEqual(IntegerValue(4)) {
					t.Fatalf("%s forwarded unnamed arguments = %v; want [3 4]", test.name, got)
				}
			})
		}
	}
}

func TestExecuteLegacyDELIResultAndDeletion(t *testing.T) {
	for _, test := range []struct {
		name       string
		hasMember  bool
		wantResult Value
	}{
		{name: "existing member", hasMember: true, wantResult: IntegerValue(1)},
		{name: "missing member", wantResult: IntegerValue(0)},
	} {
		t.Run(test.name, func(t *testing.T) {
			table := NewObjectTable()
			object := table.New(CategoryDictionary)
			if test.hasMember {
				if err := object.Set("x", IntegerValue(7)); err != nil {
					t.Fatal(err)
				}
			}
			bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
				ArgumentCount: 1,
				Constants:     []BytecodeConstant{{Value: StringValue("x")}},
				Code: []int16{
					1, 0, 0, // CONST r0, key name
					117, 1, -3, 0, // DELI r1, argument object, r0
					118, 1, 119, // Return the delete result.
				},
			}}}
			got, err := bytecode.ExecuteLegacyWithContextAndConstructionAndArguments(
				0, VoidValue, VoidValue, []Value{ObjectValue(object.Ref())}, nil, nil, nil, table,
			)
			if err != nil {
				t.Fatal(err)
			}
			if !got.StrictEqual(test.wantResult) {
				t.Fatalf("DELI result = %v, want %v", got, test.wantResult)
			}
			if _, stillPresent := object.Get("x"); test.hasMember && stillPresent {
				t.Fatal("member is still present after DELI")
			}
		})
	}
}

func TestExecuteLegacyGETPReadsPropertyClosure(t *testing.T) {
	vm := NewVM()
	table := vm.Objects
	global := table.New(CategoryDictionary)
	receiver := table.New(CategoryDictionary)
	var gotReceiver Value
	getter, err := vm.RegisterLegacyFunction("getter", func(context Value, _ []Value, _ Host, _ *uint64, _ bool) (Value, error) {
		gotReceiver = context
		return StringValue("property value"), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	property, err := vm.RegisterLegacyProperty("value", getter, VoidValue)
	if err != nil {
		t.Fatal(err)
	}
	if err := receiver.Set("value", property); err != nil {
		t.Fatal(err)
	}
	bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
		MaxVariables: 2,
		Constants:    []BytecodeConstant{{Value: property}},
		Code: []int16{
			1, 0, 0, // CONST r0, property closure
			115, 1, 0, // GETP r1, r0
			118, 1, 119, // Return r1.
		},
	}}}
	instructions, err := bytecode.DecodeLegacyInstructions(0)
	if err != nil {
		t.Fatal(err)
	}
	if instructions[1].Opcode != 115 || len(instructions[1].Operands) != 2 ||
		instructions[1].Operands[0] != 1 || instructions[1].Operands[1] != 0 {
		t.Fatalf("decoded GETP = %#v; want result r1 and property r0", instructions[1])
	}
	invoke := func(function, context Value, args []Value, resultNeeded bool) (Value, error) {
		return vm.InvokeLegacy(function, context, args, vmTestHost{}, nil, resultNeeded)
	}
	got, err := bytecode.ExecuteLegacyWithContextAndConstruction(
		0, ObjectValue(global.Ref()), ObjectValue(receiver.Ref()), nil, invoke, nil, table,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !got.StrictEqual(StringValue("property value")) {
		t.Fatalf("GETP result = %v, want the property getter value", got)
	}
	if !gotReceiver.StrictEqual(ObjectValue(receiver.Ref())) {
		t.Fatalf("GETP getter receiver = %v, want the frame this-context %v", gotReceiver, ObjectValue(receiver.Ref()))
	}
}

func TestExecuteLegacySETPWritesPropertyClosure(t *testing.T) {
	vm := NewVM()
	table := vm.Objects
	global := table.New(CategoryDictionary)
	receiver := table.New(CategoryDictionary)
	var gotReceiver Value
	var gotArgs []Value
	setter, err := vm.RegisterLegacyFunction("setter", func(context Value, args []Value, _ Host, _ *uint64, _ bool) (Value, error) {
		gotReceiver = context
		gotArgs = append([]Value(nil), args...)
		return VoidValue, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	property, err := vm.RegisterLegacyProperty("value", VoidValue, setter)
	if err != nil {
		t.Fatal(err)
	}
	if err := receiver.Set("value", property); err != nil {
		t.Fatal(err)
	}
	bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
		MaxVariables: 2,
		Constants:    []BytecodeConstant{{Value: property}, {Value: IntegerValue(9)}},
		Code: []int16{
			1, 0, 0, // CONST r0, property closure
			1, 1, 1, // CONST r1, assigned value
			114, 0, 1, // SETP *r0 = r1
			119,
		},
	}}}
	instructions, err := bytecode.DecodeLegacyInstructions(0)
	if err != nil {
		t.Fatal(err)
	}
	if instructions[2].Opcode != 114 || len(instructions[2].Operands) != 2 ||
		instructions[2].Operands[0] != 0 || instructions[2].Operands[1] != 1 {
		t.Fatalf("decoded SETP = %#v; want property r0 and source r1", instructions[2])
	}
	set := func(target Value, name string, value Value, flags uint32) error {
		if flags&LegacyPropertySetFlag != 0 {
			return vm.SetPropertyWithRuntime(target, value, vmTestHost{}, nil)
		}
		return vm.SetMemberWithRuntime(target, name, value, flags, vmTestHost{}, nil)
	}
	if _, err := bytecode.ExecuteLegacyWithContextAndConstructionAndMemberResolvers(
		0, ObjectValue(global.Ref()), ObjectValue(receiver.Ref()), nil, nil, nil, nil, set, table,
	); err != nil {
		t.Fatal(err)
	}
	if !gotReceiver.StrictEqual(ObjectValue(receiver.Ref())) {
		t.Fatalf("SETP setter receiver = %v, want the frame this-context %v", gotReceiver, ObjectValue(receiver.Ref()))
	}
	if len(gotArgs) != 1 || !gotArgs[0].StrictEqual(IntegerValue(9)) {
		t.Fatalf("SETP setter arguments = %v, want [9]", gotArgs)
	}
}

func TestExecuteLegacyGETPAndSETPRejectNonPropertyValues(t *testing.T) {
	table := NewObjectTable()
	global := table.New(CategoryDictionary)
	bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
		MaxVariables: 2,
		Constants:    []BytecodeConstant{{Value: IntegerValue(3)}},
		Code:         []int16{1, 0, 0, 115, 1, 0, 119}, // GETP r1, r0 on a plain integer
	}}}
	if _, err := bytecode.ExecuteLegacy(0, ObjectValue(global.Ref()), table); err == nil || !strings.Contains(err.Error(), "not a property closure") {
		t.Fatalf("GETP on a plain value error = %v, want a property-closure error", err)
	}
	bytecode.Objects[0].Code = []int16{1, 0, 0, 114, 0, 0, 119} // SETP *r0 = r0 on a plain integer
	if _, err := bytecode.ExecuteLegacy(0, ObjectValue(global.Ref()), table); err == nil || !strings.Contains(err.Error(), "not a property closure") {
		t.Fatalf("SETP on a plain value error = %v, want a property-closure error", err)
	}
}

func TestExecuteLegacyDEBUGGERIsNoop(t *testing.T) {
	bytecode := &DecodedBytecode{Objects: []BytecodeObject{{Code: []int16{127, 119}}}}
	instructions, err := bytecode.DecodeLegacyInstructions(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(instructions) != 2 || instructions[0].Opcode != 127 || instructions[0].Offset != 0 || instructions[1].Offset != 1 {
		t.Fatalf("decoded DEBUGGER sequence = %#v; want a one-word DEBUGGER then RET", instructions)
	}
	got, err := bytecode.ExecuteLegacy(0, VoidValue)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind() != KindVoid {
		t.Fatalf("DEBUGGER result = %v (kind %v), want void", got, got.Kind())
	}
}

func TestExecuteLegacyCCLClearsContiguousRegisters(t *testing.T) {
	table := NewObjectTable()
	global := table.New(CategoryDictionary)
	bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
		ArgumentCount: 1,
		MaxVariables:  3,
		Code: []int16{
			1, 0, 0, // CONST r0, "alpha"
			1, 1, 1, // CONST r1, "beta"
			1, 2, 2, // CONST r2, "gamma"
			4, 0, 2, // CCL r0, 2
			104, -2, 3, 0, // SPD global, "r0", r0
			104, -2, 4, 1, // SPD global, "r1", r1
			104, -2, 5, 2, // SPD global, "r2", r2
			119, // RET
		},
		Constants: []BytecodeConstant{
			{Value: StringValue("alpha")},
			{Value: StringValue("beta")},
			{Value: StringValue("gamma")},
			{Value: StringValue("r0")},
			{Value: StringValue("r1")},
			{Value: StringValue("r2")},
		},
	}}}
	instructions, err := bytecode.DecodeLegacyInstructions(0)
	if err != nil {
		t.Fatal(err)
	}
	if instructions[3].Name != "CCL" || instructions[3].Offset != 9 ||
		len(instructions[3].Operands) != 2 || instructions[3].Operands[0] != 0 || instructions[3].Operands[1] != 2 {
		t.Fatalf("decoded CCL = %#v; want register 0 and count 2", instructions[3])
	}
	if _, err := bytecode.ExecuteLegacy(0, ObjectValue(global.Ref()), table); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"r0", "r1"} {
		got, ok := global.Get(name)
		if !ok || !got.IsVoid() {
			t.Fatalf("CCL cleared %s = %v (kind %v, present %v); want void", name, got, got.Kind(), ok)
		}
	}
	if got, ok := global.Get("r2"); !ok || !got.StrictEqual(StringValue("gamma")) {
		t.Fatalf("CCL over-cleared r2 = %v (present %v); want gamma", got, ok)
	}
}

func TestExecuteLegacyCCLClearsArgumentRegisterRange(t *testing.T) {
	table := NewObjectTable()
	global := table.New(CategoryDictionary)
	bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
		ArgumentCount: 3,
		MaxVariables:  1,
		Code: []int16{
			4, -4, 1, // CCL from the second argument, one register
			104, -2, 0, -3, // SPD global, "a0", arg0
			104, -2, 1, -4, // SPD global, "a1", arg1
			104, -2, 2, -5, // SPD global, "a2", arg2
			119, // RET
		},
		Constants: []BytecodeConstant{
			{Value: StringValue("a0")},
			{Value: StringValue("a1")},
			{Value: StringValue("a2")},
		},
	}}}
	if _, err := bytecode.ExecuteLegacyWithContextAndConstructionAndArguments(
		0, ObjectValue(global.Ref()), ObjectValue(global.Ref()),
		[]Value{IntegerValue(10), IntegerValue(20), IntegerValue(30)}, nil, nil, nil, table,
	); err != nil {
		t.Fatal(err)
	}
	if got, _ := global.Get("a0"); !got.StrictEqual(IntegerValue(10)) {
		t.Fatalf("CCL changed the first argument = %v, want 10", got)
	}
	if got, ok := global.Get("a1"); !ok || !got.IsVoid() {
		t.Fatalf("CCL second argument = %v (present %v); want void", got, ok)
	}
	if got, _ := global.Get("a2"); !got.StrictEqual(IntegerValue(30)) {
		t.Fatalf("CCL changed the third argument = %v, want 30", got)
	}
}

func TestExecuteLegacyCCLLeavesFlagAndControlFlowIntact(t *testing.T) {
	table := NewObjectTable()
	global := table.New(CategoryDictionary)
	bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
		ArgumentCount: 1,
		MaxVariables:  2,
		Code: []int16{
			7, -3, -3, // CEQ arg0, arg0 -- sets the flag true
			4, 0, 2, // CCL r0, 2 -- must not touch the flag
			11, 0, // SETF r0
			17, 5, // JMP +5 to the instruction after the skipped CONST
			1, 1, 1, // CONST r1, 99 (skipped)
			118, 0, // SRV r0
			119, // RET
		},
		Constants: []BytecodeConstant{
			{Value: StringValue("unused")},
			{Value: IntegerValue(99)},
		},
	}}}
	got, err := bytecode.ExecuteLegacy(0, ObjectValue(global.Ref()), table)
	if err != nil {
		t.Fatal(err)
	}
	if !got.StrictEqual(IntegerValue(1)) {
		t.Fatalf("CCL result = %v (kind %v), want integer 1", got, got.Kind())
	}
}

func TestExecuteLegacyCCLKeepsPropertyMembersReachable(t *testing.T) {
	table := NewObjectTable()
	global := table.New(CategoryDictionary)
	subject := table.New(CategoryDictionary)
	if err := subject.Set("x", IntegerValue(7)); err != nil {
		t.Fatal(err)
	}
	bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
		ArgumentCount: 1,
		MaxVariables:  3,
		Code: []int16{
			104, -2, 0, -3, // SPD global, "subject", arg0
			4, 0, 2, // CCL r0, 2
			103, 0, -2, 0, // GPD r0, global, "subject"
			103, 1, 0, 1, // GPD r1, r0, "x"
			118, 1, // SRV r1
			119, // RET
		},
		Constants: []BytecodeConstant{
			{Value: StringValue("subject")},
			{Value: StringValue("x")},
		},
	}}}
	got, err := bytecode.ExecuteLegacyWithContextAndConstructionAndArguments(
		0, ObjectValue(global.Ref()), ObjectValue(global.Ref()),
		[]Value{ObjectValue(subject.Ref())}, nil, nil, nil, table,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !got.StrictEqual(IntegerValue(7)) {
		t.Fatalf("CCL property read = %v (kind %v), want integer 7", got, got.Kind())
	}
	if stored, ok := subject.Get("x"); !ok || !stored.StrictEqual(IntegerValue(7)) {
		t.Fatalf("CCL changed the subject member to %v (present %v)", stored, ok)
	}
}

func TestExecuteLegacyCCLRejectsMalformedCounts(t *testing.T) {
	for _, test := range []struct {
		name string
		code []int16
		want string
	}{
		{name: "negative count", code: []int16{4, 0, -1, 119}, want: "CCL count -1 is negative"},
		{name: "count past frame", code: []int16{4, 0, 20000, 119}, want: "CCL clears 20000 registers"},
	} {
		t.Run(test.name, func(t *testing.T) {
			bytecode := &DecodedBytecode{Objects: []BytecodeObject{{MaxVariables: 2, Code: test.code}}}
			_, err := bytecode.ExecuteLegacy(0, VoidValue)
			if err == nil || !strings.Contains(err.Error(), test.want) || !strings.Contains(err.Error(), "ContinuousClear") {
				t.Fatalf("CCL error = %v, want %q citing ContinuousClear", err, test.want)
			}
		})
	}
}

func TestExecuteLegacyCCLZeroCountIsNoop(t *testing.T) {
	bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
		ArgumentCount: 1,
		MaxVariables:  2,
		Code: []int16{
			1, 0, 0, // CONST r0, "kept"
			4, 0, 0, // CCL r0, 0 -- the reference loop clears nothing
			118, 0, // SRV r0
			119, // RET
		},
		Constants: []BytecodeConstant{{Value: StringValue("kept")}},
	}}}
	got, err := bytecode.ExecuteLegacy(0, VoidValue)
	if err != nil {
		t.Fatal(err)
	}
	if !got.StrictEqual(StringValue("kept")) {
		t.Fatalf("CCL with zero count = %v, want kept", got)
	}
}
