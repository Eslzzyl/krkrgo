package tjs

import (
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
					116, 0, -3, 0, // DELD r0, argument object, constant "x"
					118, 0, 119, // Return the delete result.
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
	resolve := func(receiver Value, name string) (Value, bool, error) {
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
	global := ObjectValue(NewObjectRef(0, 1))
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
	resolve := func(receiver Value, name string) (Value, bool, error) {
		return vm.GetMemberIfPresent(receiver, name, vmTestHost{}, nil)
	}
	if _, err := bytecode.ExecuteLegacyWithContextAndConstructionAndPropertyResolver(
		0, ObjectValue(NewObjectRef(0, 1)), ObjectValue(NewObjectRef(0, 1)), nil, nil, nil, resolve, vm.Objects,
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
func TestExecuteLegacyCLT(t *testing.T) {
	for _, test := range []struct {
		name  string
		left  Value
		right Value
		want  int64
	}{
		{name: "numeric greater", left: IntegerValue(8), right: IntegerValue(3), want: 1},
		{name: "numeric reversed operands", left: IntegerValue(3), right: IntegerValue(8), want: 0},
		{name: "string greater", left: StringValue("beta"), right: StringValue("alpha"), want: 1},
		{name: "string reversed operands", left: StringValue("alpha"), right: StringValue("beta"), want: 0},
		{name: "mixed numeric coercion", left: IntegerValue(8), right: StringValue("3"), want: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
				ArgumentCount: 2,
				Code: []int16{
					9, -3, -4, // CLT ra, rb
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
				t.Fatalf("CLT result = %v (kind %v), want integer %d", got, got.Kind(), test.want)
			}
		})
	}
}
func TestExecuteLegacyEXTRYDoesNotSwallowProtectedBodyError(t *testing.T) {
	bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
		ArgumentCount: 1,
		Code: []int16{
			120, 0, 0, // ENTRY catch target, exception register
			91, -3, // NUM argument register (an object cannot be converted).
			121, // EXTRY is not reached when the protected body fails.
		},
	}}}

	_, err := bytecode.ExecuteLegacyWithContextAndConstructionAndArguments(
		0, VoidValue, VoidValue, []Value{ObjectValue(NewObjectTable().New(CategoryDictionary).Ref())}, nil, nil, nil,
	)
	if err == nil || !strings.Contains(err.Error(), "legacy intercode object 0 code offset 3: NUM: cannot convert TJS Object to number") {
		t.Fatalf("protected-body error = %v, want the original NUM conversion error", err)
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
