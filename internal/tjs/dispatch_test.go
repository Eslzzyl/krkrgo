package tjs

import (
	"bytes"
	"strings"
	"testing"
)

func TestDispatchMembersAndReadOnlyData(t *testing.T) {
	table := NewObjectTable()
	o := table.New(CategoryDictionary)
	d := NewDispatcher(table)
	if err := d.Set(ObjectValue(o.Ref()), "answer", IntegerValue(42), MemberEnsure); err != nil {
		t.Fatal(err)
	}
	got, err := d.Get(ObjectValue(o.Ref()), "answer", MemberMustExist)
	if err != nil || got.IntegerValue() != 42 {
		t.Fatalf("get = %v, %v", got, err)
	}
	if !d.Has(ObjectValue(o.Ref()), "answer") {
		t.Fatal("member was not found")
	}
	if err := DefineReadOnly(o, "fixed", IntegerValue(1)); err != nil {
		t.Fatal(err)
	}
	if err := d.Set(ObjectValue(o.Ref()), "fixed", IntegerValue(2)); err != ErrReadOnly {
		t.Fatalf("read-only set = %v", err)
	}
	if err := d.Delete(ObjectValue(o.Ref()), "fixed"); err != ErrCannotDelete {
		t.Fatalf("read-only delete = %v", err)
	}
}

func TestDispatchPrimitiveMethodsAndBoundContext(t *testing.T) {
	d := NewDispatcher(NewObjectTable())
	upper, err := d.Get(StringValue("hello"), "toUpperCase")
	if err != nil {
		t.Fatal(err)
	}
	got, err := d.Call(upper)
	if err != nil || got.Text() != "HELLO" {
		t.Fatalf("upper = %v, %v", got, err)
	}
	arr := d.Table.New(CategoryArray)
	if err := arr.Set("0", StringValue("a")); err != nil {
		t.Fatal(err)
	}
	if err := arr.Set("1", StringValue("b")); err != nil {
		t.Fatal(err)
	}
	joined, err := d.CallMember(ObjectValue(arr.Ref()), "join", StringValue("-"))
	if err != nil || joined.Text() != "a-b" {
		t.Fatalf("join = %v, %v", joined, err)
	}
}

func TestDispatchMemberLookupPreservesExplicitFunctionContext(t *testing.T) {
	table := NewObjectTable()
	d := NewDispatcher(table)
	boundContext := table.New(CategoryDictionary)
	receiver := table.New(CategoryClass)
	function := NewNativeFunction(table, "bound", func(context ObjectRef, _ []Value) (Value, error) {
		return IntegerValue(int64(context.Object)), nil
	})
	bound, err := Bind(function, boundContext.Ref())
	if err != nil {
		t.Fatal(err)
	}
	if err := receiver.Set("bound", bound); err != nil {
		t.Fatal(err)
	}
	got, err := d.Get(ObjectValue(receiver.Ref()), "bound")
	if err != nil {
		t.Fatal(err)
	}
	result, err := d.Call(got)
	if err != nil || result.IntegerValue() != int64(boundContext.ID) {
		t.Fatalf("bound member context = %v, %v; want object %d", result, err, boundContext.ID)
	}
	memberResult, err := d.CallMember(ObjectValue(receiver.Ref()), "bound")
	if err != nil || memberResult.IntegerValue() != int64(boundContext.ID) {
		t.Fatalf("bound member call context = %v, %v; want object %d", memberResult, err, boundContext.ID)
	}
}
func TestDispatchRegexpMatchReturnsCaptureArrayAndEmptyMiss(t *testing.T) {
	table := NewObjectTable()
	dispatcher := NewDispatcher(table)
	pattern, err := NewRegExp(table, `([a-z]+)(\d+)`, "")
	if err != nil {
		t.Fatal(err)
	}
	matched, err := dispatcher.CallMember(ObjectValue(pattern.Ref()), "match", StringValue("foo42"))
	if err != nil {
		t.Fatal(err)
	}
	array, ok := table.Lookup(matched.ObjectRef().Object)
	if !ok {
		t.Fatal("match did not return a registered array")
	}
	for key, want := range map[string]string{"0": "foo42", "1": "foo", "2": "42"} {
		got, present := array.Get(key)
		if !present || got.Text() != want {
			t.Errorf("capture %s = %v, present %t; want %q", key, got, present, want)
		}
	}
	missed, err := dispatcher.CallMember(ObjectValue(pattern.Ref()), "match", StringValue("no digits"))
	if err != nil {
		t.Fatal(err)
	}
	missArray, ok := table.Lookup(missed.ObjectRef().Object)
	if !ok || missArray.Category != CategoryArray || arrayLength(missArray) != 0 {
		t.Fatalf("unmatched RegExp.match = %v; want empty array", missed)
	}
}

func TestDispatchRegexpExecReturnsEmptyArrayOnMiss(t *testing.T) {
	table := NewObjectTable()
	dispatcher := NewDispatcher(table)
	pattern, err := NewRegExp(table, "foo", "")
	if err != nil {
		t.Fatal(err)
	}
	missed, err := dispatcher.CallMember(ObjectValue(pattern.Ref()), "exec", StringValue("bar"))
	if err != nil {
		t.Fatal(err)
	}
	missArray, ok := table.Lookup(missed.ObjectRef().Object)
	if !ok || missArray.Category != CategoryArray || arrayLength(missArray) != 0 {
		t.Fatalf("unmatched RegExp.exec = %v; want empty array", missed)
	}
}

func TestDispatchArrayCanonicalIndicesAndBounds(t *testing.T) {
	table := NewObjectTable()
	d := NewDispatcher(table)
	arr := table.New(CategoryArray)
	target := ObjectValue(arr.Ref())
	for _, item := range []struct {
		name  string
		value Value
	}{
		{"0", IntegerValue(1)},
		{"2", IntegerValue(3)},
		{"01", IntegerValue(10)},
		{"1.0", IntegerValue(11)},
		{"-1", IntegerValue(12)},
	} {
		if err := d.Set(target, item.name, item.value); err != nil {
			t.Fatal(err)
		}
	}
	length, err := d.Get(target, "length")
	if err != nil || length.IntegerValue() != 3 {
		t.Fatalf("sparse length = %v, %v", length, err)
	}
	pushed, err := d.CallMember(target, "push", IntegerValue(4))
	if err != nil || pushed.IntegerValue() != 4 {
		t.Fatalf("push result = %v, %v", pushed, err)
	}
	zeroPush, err := d.CallMember(target, "push")
	if err != nil || zeroPush.IntegerValue() != 4 {
		t.Fatalf("zero-argument push = %v, %v", zeroPush, err)
	}
	length, err = d.Get(target, "count")
	if err != nil || length.IntegerValue() != 4 {
		t.Fatalf("count after push = %v, %v; want 4", length, err)
	}
	manyPush, err := d.CallMember(target, "push", IntegerValue(5), IntegerValue(6))
	if err != nil || manyPush.IntegerValue() != 6 {
		t.Fatalf("multi-argument push result = %v, %v; want 6", manyPush, err)
	}
	length, err = d.Get(target, "count")
	if err != nil || length.IntegerValue() != 6 {
		t.Fatalf("count after multi-argument push = %v, %v; want 6", length, err)
	}
	for index, name := range []string{"4", "5"} {
		got, getErr := d.Get(target, name)
		if getErr != nil || got.IntegerValue() != int64(index+5) {
			t.Fatalf("multi-argument push item %s = %v, %v; want %d", name, got, getErr, index+5)
		}
	}
	canonical, err := d.Get(target, "3")
	if err != nil || canonical.IntegerValue() != 4 {
		t.Fatalf("push start = %v, %v", canonical, err)
	}
	for _, name := range []string{"01", "1.0", "-1"} {
		if !d.Has(target, name) {
			t.Fatalf("ordinary member %q missing", name)
		}
	}
	if err := d.Set(target, "100000", IntegerValue(1)); err == nil {
		t.Fatal("out-of-range canonical index was accepted")
	}
	if err := d.Set(target, "9223372036854775808", IntegerValue(2)); err != nil {
		t.Fatalf("overflowing ordinary key rejected: %v", err)
	}
}

func TestDispatchArrayExtendedMethodsAndSparseHoles(t *testing.T) {
	table := NewObjectTable()
	d := NewDispatcher(table)
	arr := table.New(CategoryArray)
	target := ObjectValue(arr.Ref())
	for _, item := range []struct {
		name  string
		value Value
	}{{"0", IntegerValue(1)}, {"2", IntegerValue(3)}, {"ordinary", IntegerValue(8)}} {
		if err := d.Set(target, item.name, item.value); err != nil {
			t.Fatal(err)
		}
	}
	added, err := d.CallMember(target, "add", IntegerValue(4))
	if err != nil || added.IntegerValue() != 3 {
		t.Fatalf("add = %v, %v", added, err)
	}
	shifted, err := d.CallMember(target, "unshift", IntegerValue(9))
	if err != nil || shifted.IntegerValue() != 5 {
		t.Fatalf("unshift = %v, %v", shifted, err)
	}
	if removed, e := d.CallMember(target, "remove", IntegerValue(1)); e != nil || removed.IntegerValue() != 1 {
		t.Fatalf("remove = %v, %v", removed, e)
	}
	erased, err := d.CallMember(target, "erase", IntegerValue(1))
	if err != nil || !erased.IsVoid() {
		t.Fatalf("erase = %v, %v", erased, err)
	}
	inserted, err := d.CallMember(target, "insert", IntegerValue(1), IntegerValue(7))
	if err != nil || !inserted.IsVoid() {
		t.Fatalf("insert = %v, %v", inserted, err)
	}
	joined, err := d.CallMember(target, "join", StringValue(","))
	if err != nil || joined.Text() != "9,7,,3,4" {
		t.Fatalf("sparse join = %v, %v", joined, err)
	}
	if !d.Has(target, "ordinary") {
		t.Fatal("ordinary member was lost")
	}
	if err := d.Set(target, "length", IntegerValue(2)); err != nil {
		t.Fatal(err)
	}
	length, err := d.Get(target, "count")
	if err != nil || length.IntegerValue() != 2 {
		t.Fatalf("shrunk length = %v, %v", length, err)
	}
	if err := d.Set(target, "length", IntegerValue(3)); err != nil {
		t.Fatalf("sparse length growth = %v", err)
	}
	if got, err := d.Get(target, "length"); err != nil || got.IntegerValue() != 3 {
		t.Fatalf("grown length = %v, %v", got, err)
	}
}
func TestDispatchArrayLogicalLengthAndMethodComposition(t *testing.T) {
	table := NewObjectTable()
	d := NewDispatcher(table)
	arr := table.New(CategoryArray)
	target := ObjectValue(arr.Ref())
	if err := d.Set(target, "9", IntegerValue(90)); err != nil {
		t.Fatal(err)
	}
	if err := d.Set(target, "ordinary", IntegerValue(8)); err != nil {
		t.Fatal(err)
	}
	if got, err := d.Get(target, "count"); err != nil || got.IntegerValue() != 10 {
		t.Fatalf("high index count = %v, %v", got, err)
	}
	if err := d.Delete(target, "9"); err != nil {
		t.Fatal(err)
	}
	hole, err := d.Get(target, "9")
	if err != nil {
		t.Fatal(err)
	}
	if !hole.IsVoid() {
		t.Fatalf("deleted tail = %v, want void", hole)
	}
	if got, err := d.Get(target, "length"); err != nil || got.IntegerValue() != 10 {
		t.Fatalf("delete changed logical length = %v, %v", got, err)
	}
	if err := d.Set(target, "length", IntegerValue(3)); err != nil {
		t.Fatal(err)
	}
	if d.Has(target, "9") || !d.Has(target, "ordinary") {
		t.Fatal("length shrink did not remove indexed member or preserve ordinary member")
	}
	if _, err := d.CallMember(target, "push", IntegerValue(3)); err != nil {
		t.Fatal(err)
	}
	if _, err := d.CallMember(target, "unshift", IntegerValue(1)); err != nil {
		t.Fatal(err)
	}
	if _, err := d.CallMember(target, "pop"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.CallMember(target, "shift"); err != nil {
		t.Fatal(err)
	}
	if got, err := d.Get(target, "count"); err != nil || got.IntegerValue() != 3 {
		t.Fatalf("method composition length = %v, %v", got, err)
	}
	if got, err := d.Get(target, "ordinary"); err != nil || got.IntegerValue() != 8 {
		t.Fatalf("ordinary member = %v, %v", got, err)
	}
}

func TestDispatchArrayEmptyAndBounds(t *testing.T) {
	table := NewObjectTable()
	d := NewDispatcher(table)
	target := ObjectValue(table.New(CategoryArray).Ref())
	for _, name := range []string{"pop", "shift"} {
		value, err := d.CallMember(target, name)
		if err != nil || !value.IsVoid() {
			t.Fatalf("empty %s = %v, %v", name, value, err)
		}
	}
	for _, call := range []struct {
		name string
		args []Value
	}{{"erase", []Value{IntegerValue(0)}}, {"insert", []Value{IntegerValue(1), IntegerValue(2)}}} {
		if _, err := d.CallMember(target, call.name, call.args...); err == nil {
			t.Fatalf("out-of-range %s succeeded", call.name)
		}
	}
	if err := d.Set(target, "100000", IntegerValue(1)); err == nil {
		t.Fatal("dispatcher accepted out-of-range array index")
	}
	if err := d.Set(target, "length", IntegerValue(maxVMArrayLength+1)); err == nil {
		t.Fatal("dispatcher accepted out-of-range array length")
	}
}

func TestDispatchClassInheritanceAndAccessor(t *testing.T) {
	table := NewObjectTable()
	base := NewClass(table, Class{Name: "Base"})
	derived := NewClassWithBases(table, Class{Name: "Derived"}, base)
	instance, err := NewInstance(table, derived)
	if err != nil {
		t.Fatal(err)
	}
	if !InstanceOf(ObjectValue(instance.Ref()), base) || !instance.IsInstanceOf(derived) {
		t.Fatal("instanceof failed")
	}
}
func TestDispatchArrayFindUsesStrictValueAndStartIndex(t *testing.T) {
	table := NewObjectTable()
	d := NewDispatcher(table)
	array := table.New(CategoryArray)
	target := ObjectValue(array.Ref())
	for i, value := range []Value{IntegerValue(1), StringValue("1"), IntegerValue(1)} {
		if err := d.Set(target, []string{"0", "1", "2"}[i], value); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range []struct {
		value Value
		start Value
		want  int64
	}{
		{IntegerValue(1), IntegerValue(1), 2},
		{StringValue("1"), IntegerValue(0), 1},
		{IntegerValue(1), IntegerValue(3), -1},
	} {
		got, err := d.CallMember(target, "find", test.value, test.start)
		if err != nil || got.IntegerValue() != test.want {
			t.Fatalf("Array.find(%v, %v) = %v, %v; want %d", test.value, test.start, got, err, test.want)
		}
	}
}

func TestDispatchOctetIndexAndXorMutateNativeBuffer(t *testing.T) {
	d := NewDispatcher(NewObjectTable())
	buf := []byte{0x01, 0x02, 0x03, 0x04}
	octet := OctetBufferValue(buf)

	if got, err := d.Get(octet, "length"); err != nil || got.IntegerValue() != 4 {
		t.Fatalf("octet length = %v, %v; want 4", got, err)
	}
	if got, err := d.Get(octet, "1"); err != nil || got.IntegerValue() != 0x02 {
		t.Fatalf("octet[1] = %v, %v; want 2", got, err)
	}
	if _, err := d.Get(octet, "4"); err == nil || !strings.Contains(err.Error(), "out of range") {
		t.Fatalf("out-of-range octet read = %v, want range error", err)
	}
	if err := d.Set(octet, "0", IntegerValue(0x109)); err != nil {
		t.Fatal(err)
	}
	if buf[0] != 0x09 {
		t.Fatalf("octet[0] write = %#x, want low byte 0x09", buf[0])
	}
	if _, err := d.CallMember(octet, "xor", IntegerValue(1), IntegerValue(2), IntegerValue(0x1ff)); err != nil {
		t.Fatal(err)
	}
	if want := []byte{0x09, 0xfd, 0xfc, 0x04}; !bytes.Equal(buf, want) {
		t.Fatalf("octet after xor = %#x, want %#x", buf, want)
	}
	if _, err := d.CallMember(octet, "xor", IntegerValue(3), IntegerValue(2), IntegerValue(0)); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("out-of-range xor = %v, want range error", err)
	}

	immutable := OctetValue(buf)
	if err := d.Set(immutable, "0", IntegerValue(0)); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("immutable octet write = %v, want immutable error", err)
	}
	if _, err := d.CallMember(immutable, "xor", IntegerValue(0), IntegerValue(1), IntegerValue(1)); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("immutable octet xor = %v, want immutable error", err)
	}
}
