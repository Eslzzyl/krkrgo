package tjs

import "testing"

func TestInstanceOfOperator(t *testing.T) {
	vm := NewVM()
	array, err := vm.NewArray(nil)
	if err != nil {
		t.Fatal(err)
	}
	dictionary, err := vm.NewDictionary()
	if err != nil {
		t.Fatal(err)
	}
	function := ObjectValue(newFunctionObject(vm.Objects, Function{Name: "test"}, nil, nil, ObjectRef{}, nil).Ref())
	native, _, err := vm.alloc(CategoryNative)
	if err != nil {
		t.Fatal(err)
	}
	method, _, err := vm.alloc(CategoryMethod)
	if err != nil {
		t.Fatal(err)
	}
	property, _, err := vm.alloc(CategoryProperty)
	if err != nil {
		t.Fatal(err)
	}
	regexp, err := NewRegExp(vm.Objects, "pattern", "")
	if err != nil {
		t.Fatal(err)
	}
	class, err := vm.RegisterNativeClass("ConductorException")
	if err != nil {
		t.Fatal(err)
	}
	budget := uint64(1000)
	instance, err := vm.construct(class, nil, vmTestHost{}, &budget)
	if err != nil {
		t.Fatal(err)
	}
	base := NewClass(vm.Objects, Class{Name: "BaseException"})
	derived := NewClassWithBases(vm.Objects, Class{Name: "DerivedException"}, base)
	derivedInstance, err := NewInstance(vm.Objects, derived)
	if err != nil {
		t.Fatal(err)
	}
	vm.Globals["fnValue"] = function
	vm.Globals["nativeValue"] = native
	vm.Globals["methodValue"] = method
	vm.Globals["propValue"] = property
	vm.Globals["regexpValue"] = ObjectValue(regexp.Ref())
	vm.Globals["classValue"] = class
	vm.Globals["instanceValue"] = instance
	vm.Globals["derivedValue"] = ObjectValue(derivedInstance.Ref())
	vm.Globals["array"] = array
	vm.Globals["dictionary"] = dictionary

	for _, test := range []struct {
		source string
		want   int64
	}{
		{source: `void instanceof "Object"`, want: 0},
		{source: `null instanceof "Object"`, want: 1},
		{source: `1 instanceof "Object"`, want: 1},
		{source: `1 instanceof "Number"`, want: 1},
		{source: `1.5 instanceof "Number"`, want: 1},
		{source: `"value" instanceof "String"`, want: 1},
		{source: `<% 00 ff %> instanceof "Octet"`, want: 1},
		{source: `array instanceof "Array"`, want: 1},
		{source: `dictionary instanceof "Dictionary"`, want: 1},
		{source: `fnValue instanceof "Function"`, want: 1},
		{source: `(&propValue) instanceof "Property"`, want: 1},
		{source: `regexpValue instanceof "RegExp"`, want: 1},
		{source: `classValue instanceof "Class"`, want: 1},
		{source: `instanceValue instanceof "Object"`, want: 1},
		{source: `nativeValue instanceof "Function"`, want: 1},
		{source: `methodValue instanceof "Function"`, want: 1},
		{source: `instanceValue instanceof "ConductorException"`, want: 1},
		{source: `derivedValue instanceof "DerivedException"`, want: 1},
		{source: `derivedValue instanceof "BaseException"`, want: 1},
		{source: `array instanceof "Dictionary"`, want: 0},
		{source: `void instanceof "ConductorException"`, want: 0},
		{source: `1 instanceof "String"`, want: 0},
		{source: `1 instanceof 2`, want: 0},
		{source: `1 instanceof array`, want: 0},
		{source: `1 + 2 instanceof "String"`, want: 0},
	} {
		program, err := compile_expression("instanceof.tjs", test.source)
		if err != nil {
			t.Errorf("compile %q: %v", test.source, err)
			continue
		}
		got, err := vm.Execute(&program, vmTestHost{}, &budget)
		if err != nil {
			t.Errorf("execute %q: %v", test.source, err)
			continue
		}
		if got.Kind() != KindInteger || got.IntegerValue() != test.want {
			t.Errorf("%q = %#v, want integer %d", test.source, got, test.want)
		}
	}
}

func TestInstanceOfInvalidObjectReturnsError(t *testing.T) {
	vm := NewVM()
	value, err := vm.NewDictionary()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := vm.Invalidate(value, vmTestHost{}, nil); err != nil {
		t.Fatal(err)
	}
	vm.Globals["stale"] = value
	program, err := compile_expression("instanceof-invalid.tjs", `stale instanceof "Dictionary"`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := vm.Execute(&program, vmTestHost{}, nil); err == nil {
		t.Fatal("instanceof accepted an invalid object")
	}
}
