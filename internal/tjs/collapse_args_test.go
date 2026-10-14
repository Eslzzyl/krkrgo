package tjs

import (
	"strconv"
	"testing"
)

// TestExecuteLegacyCollapseBaseMaterializesArgumentArray verifies that a legacy
// frame whose record declares a collapse base receives the arguments from that
// index on as a single array in the collapse register, matching the reference
// VM's ra-3-CollapseBase behavior.
func TestExecuteLegacyCollapseBaseMaterializesArgumentArray(t *testing.T) {
	table := NewObjectTable()
	global := table.New(CategoryDictionary)
	base := int32(0)
	bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
		CollapseBase: &base,
		Code:         []int16{118, -3, 119}, // Return the collapse register.
	}}}
	args := []Value{StringValue("simpleanim.tjs"), StringValue("system/Initialize.tjs")}
	got, err := bytecode.ExecuteLegacyWithContextAndConstructionAndArguments(
		0, ObjectValue(global.Ref()), ObjectValue(global.Ref()), args, nil, nil, nil, table,
	)
	if err != nil {
		t.Fatal(err)
	}
	array := collapseArrayForTest(t, table, got)
	if n := arrayLength(array); n != int64(len(args)) {
		t.Fatalf("collapse array count = %d, want %d", n, len(args))
	}
	for i, want := range args {
		element, present := array.Get(strconv.Itoa(i))
		if !present || !element.StrictEqual(want) {
			t.Fatalf("collapse array[%d] = %v (present %v), want %v", i, element, present, want)
		}
	}
}

// TestExecuteLegacyCollapseBasePreservesNamedArguments verifies that named
// arguments keep their registers while the collapsed array still receives every
// argument from the collapse base on, including arguments beyond the declared
// argument count.
func TestExecuteLegacyCollapseBasePreservesNamedArguments(t *testing.T) {
	table := NewObjectTable()
	global := table.New(CategoryDictionary)
	base := int32(2)
	run := func(operand int16) Value {
		bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
			ArgumentCount: 2,
			MaxVariables:  2,
			CollapseBase:  &base,
			Code:          []int16{118, operand, 119},
		}}}
		args := []Value{IntegerValue(10), IntegerValue(20), IntegerValue(30), IntegerValue(40)}
		got, err := bytecode.ExecuteLegacyWithContextAndConstructionAndArguments(
			0, ObjectValue(global.Ref()), ObjectValue(global.Ref()), args, nil, nil, nil, table,
		)
		if err != nil {
			t.Fatalf("operand %d: %v", operand, err)
		}
		return got
	}
	if got := run(-3); !got.StrictEqual(IntegerValue(10)) {
		t.Fatalf("first named argument = %v, want 10", got)
	}
	if got := run(-4); !got.StrictEqual(IntegerValue(20)) {
		t.Fatalf("second named argument = %v, want 20", got)
	}
	array := collapseArrayForTest(t, table, run(-5))
	if n := arrayLength(array); n != 2 {
		t.Fatalf("collapse array count = %d, want 2", n)
	}
	for i, want := range []Value{IntegerValue(30), IntegerValue(40)} {
		element, present := array.Get(strconv.Itoa(i))
		if !present || !element.StrictEqual(want) {
			t.Fatalf("collapse array[%d] = %v (present %v), want %v", i, element, present, want)
		}
	}
}

// TestExecuteLegacyCollapseBaseRequiresObjectTable verifies that a declared
// collapse base without an active object table fails loudly instead of
// forwarding a non-array to the callee.
func TestExecuteLegacyCollapseBaseRequiresObjectTable(t *testing.T) {
	base := int32(0)
	bytecode := &DecodedBytecode{Objects: []BytecodeObject{{
		CollapseBase: &base,
		Code:         []int16{118, -3, 119},
	}}}
	if _, err := bytecode.ExecuteLegacyWithContextAndConstructionAndArguments(
		0, VoidValue, VoidValue, []Value{StringValue("simpleanim.tjs")}, nil, nil, nil,
	); err == nil {
		t.Fatal("collapse base without an object table: error = nil, want failure")
	}
}

func collapseArrayForTest(t *testing.T, table *ObjectTable, value Value) *Object {
	t.Helper()
	if value.Kind() != KindObject || value.IsNull() {
		t.Fatalf("collapse register = %v, want an array object", value)
	}
	array, ok := table.Lookup(value.ObjectRef().Object)
	if !ok {
		t.Fatalf("collapse array %v is not registered in the object table", value)
	}
	if array.Category != CategoryArray {
		t.Fatalf("collapse register category = %v, want array", array.Category)
	}
	return array
}
