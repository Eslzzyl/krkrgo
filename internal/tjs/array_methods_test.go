package tjs

import "testing"

func TestDispatchArraySplitTJSArgumentsAndDelimiters(t *testing.T) {
	table := NewObjectTable()
	d := NewDispatcher(table)
	array := table.New(CategoryArray)
	target := ObjectValue(array.Ref())
	if err := array.Set("0", IntegerValue(99)); err != nil {
		t.Fatal(err)
	}
	if err := array.Set("extra", IntegerValue(8)); err != nil {
		t.Fatal(err)
	}

	result, err := d.CallMember(target, "split", StringValue(",;"), StringValue(";a,,b;c;"), VoidValue, IntegerValue(1))
	if err != nil {
		t.Fatal(err)
	}
	if result.Kind() != KindObject || result.ObjectRef().Object != array.ID {
		t.Fatalf("split result = %v, want receiver array %d", result, array.ID)
	}
	if got := arrayLength(array); got != 3 {
		t.Fatalf("purged split length = %d, want 3", got)
	}
	joined, err := d.CallMember(target, "join", StringValue("|"))
	if err != nil || joined.Text() != "a|b|c" {
		t.Fatalf("purged split join = %q, %v", joined.Text(), err)
	}
	if extra, ok := array.Get("extra"); !ok || extra.IntegerValue() != 8 {
		t.Fatalf("split lost ordinary member: %v, %v", extra, ok)
	}

	if _, err := d.CallMember(target, "split", StringValue(",")); err != ErrBadParamCount {
		t.Fatalf("split with missing subject = %v, want ErrBadParamCount", err)
	}
	if got := arrayLength(array); got != 3 {
		t.Fatalf("bad split changed receiver length = %d, want 3", got)
	}
	if _, err := d.CallMember(target, "split", StringValue(""), StringValue("ab")); err != nil {
		t.Fatal(err)
	}
	joined, err = d.CallMember(target, "join", StringValue("|"))
	if err != nil || joined.Text() != "ab" {
		t.Fatalf("empty delimiter-set split = %q, %v", joined.Text(), err)
	}
}

func TestDispatchArraySplitRegexpAndPurge(t *testing.T) {
	table := NewObjectTable()
	d := NewDispatcher(table)
	array := table.New(CategoryArray)
	target := ObjectValue(array.Ref())
	pattern, err := NewRegExp(table, ",+", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.CallMember(target, "split", ObjectValue(pattern.Ref()), StringValue(",a,,b,"), VoidValue, IntegerValue(1)); err != nil {
		t.Fatal(err)
	}
	joined, err := d.CallMember(target, "join", StringValue("|"))
	if err != nil || joined.Text() != "a|b" {
		t.Fatalf("regexp purge split = %q, %v", joined.Text(), err)
	}

	nonmatching, err := NewRegExp(table, "x", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.CallMember(target, "split", ObjectValue(nonmatching.Ref()), StringValue(""), VoidValue, IntegerValue(1)); err != nil {
		t.Fatal(err)
	}
	if got := arrayLength(array); got != 1 {
		t.Fatalf("nonmatching regexp split length = %d, want 1", got)
	}
}
func TestDispatchArraySplitUsesUTF16UnitsAndNULTermination(t *testing.T) {
	table := NewObjectTable()
	d := NewDispatcher(table)
	array := table.New(CategoryArray)
	target := ObjectValue(array.Ref())
	subject := StringUnits([]uint16{'A', 0xd83d, 0xde00, 'B'})
	pattern := StringUnits([]uint16{0xd83d})
	if _, err := d.CallMember(target, "split", pattern, subject); err != nil {
		t.Fatal(err)
	}
	first, firstOK := array.Get("0")
	second, secondOK := array.Get("1")
	if !firstOK || !secondOK {
		t.Fatalf("UTF-16 split items present = %v, %v", firstOK, secondOK)
	}
	if units := first.UTF16(); len(units) != 1 || units[0] != 'A' {
		t.Fatalf("first UTF-16 split item = %v", units)
	}
	if units := second.UTF16(); len(units) != 2 || units[0] != 0xde00 || units[1] != 'B' {
		t.Fatalf("second UTF-16 split item = %v", units)
	}

	if _, err := d.CallMember(target, "split", StringUnits([]uint16{0, ','}), StringValue("a,b")); err != nil {
		t.Fatal(err)
	}
	if got, ok := array.Get("0"); !ok || arrayLength(array) != 1 || got.Text() != "a,b" {
		t.Fatalf("NUL-terminated delimiter split = %v, length %d, present %v", got, arrayLength(array), ok)
	}
	if _, err := d.CallMember(target, "split", StringValue(","), StringUnits([]uint16{'a', 0, 'b', ',', 'c'})); err != nil {
		t.Fatal(err)
	}
	if got, ok := array.Get("0"); !ok || arrayLength(array) != 1 || got.Text() != "a" {
		t.Fatalf("NUL-terminated subject split = %v, length %d, present %v", got, arrayLength(array), ok)
	}
}

func TestDispatchArrayAssignArrayAndEnumeratedObject(t *testing.T) {
	table := NewObjectTable()
	d := NewDispatcher(table)
	targetObject := table.New(CategoryArray)
	target := ObjectValue(targetObject.Ref())
	if err := targetObject.Set("0", IntegerValue(9)); err != nil {
		t.Fatal(err)
	}
	if err := targetObject.Set("extra", IntegerValue(7)); err != nil {
		t.Fatal(err)
	}

	sourceArray := table.New(CategoryArray)
	if err := sourceArray.Set("0", StringValue("first")); err != nil {
		t.Fatal(err)
	}
	if err := sourceArray.Set("2", IntegerValue(3)); err != nil {
		t.Fatal(err)
	}
	if _, err := d.CallMember(target, "assign", ObjectValue(sourceArray.Ref())); err != nil {
		t.Fatal(err)
	}
	if got := arrayLength(targetObject); got != 3 {
		t.Fatalf("copied array length = %d, want 3", got)
	}
	if value, ok := targetObject.Get("1"); !ok || !value.IsVoid() {
		t.Fatalf("copied sparse slot = %v, present=%v; want an explicit void item", value, ok)
	}
	if extra, ok := targetObject.Get("extra"); !ok || extra.IntegerValue() != 7 {
		t.Fatalf("assign lost ordinary member: %v, %v", extra, ok)
	}

	sourceObject := table.New(CategoryDictionary)
	for name, value := range map[string]Value{"beta": IntegerValue(2), "alpha": IntegerValue(1), "hidden": IntegerValue(9)} {
		if err := sourceObject.Set(name, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := sourceObject.SetFlags("hidden", MemberHidden); err != nil {
		t.Fatal(err)
	}
	if _, err := d.CallMember(target, "assign", ObjectValue(sourceObject.Ref())); err != nil {
		t.Fatal(err)
	}
	joined, err := d.CallMember(target, "join", StringValue("|"))
	if err != nil || joined.Text() != "alpha|1|beta|2" {
		t.Fatalf("enumerated object assignment = %q, %v", joined.Text(), err)
	}
	if extra, ok := targetObject.Get("extra"); !ok || extra.IntegerValue() != 7 {
		t.Fatalf("object assign lost ordinary member: %v, %v", extra, ok)
	}

	if _, err := d.CallMember(target, "assign", target); err != nil {
		t.Fatal(err)
	}
	if got := arrayLength(targetObject); got != 0 {
		t.Fatalf("self-assign length = %d, want 0", got)
	}
	if extra, ok := targetObject.Get("extra"); !ok || extra.IntegerValue() != 7 {
		t.Fatalf("self-assign lost ordinary member: %v, %v", extra, ok)
	}
}

func TestVMArraySplitAndAssign(t *testing.T) {
	got := executeVMSource(t, `
var a=[99],b=[],r=a.split(',;',';a,,b;c;',void,false);
b.add(r===a);b.add(a.count);b.add(a.join('|'));
a.split(',;',';a,,b;c;',123,true);b.add(a.join('|'));
a.split('','ab');b.add(a.join('|'));
a.split(',', '', void, true);b.add(a.count);
var rx=new RegExp(',+');a.split(rx,',a,,b,',void,true);b.add(a.join('|'));
a.split(new RegExp(''),'ab');b.add(a.join('|'));
var empty=[];empty.split(new RegExp('x'),'',void,true);b.add(empty.count);
return b.join('#');`)
	if want := "1#6#|a||b|c|#a|b|c#ab#0#a|b#ab#1"; got.Text() != want {
		t.Fatalf("Array.split = %q, want %q", got.Text(), want)
	}
}

func TestVMArrayAssignArrayObject(t *testing.T) {
	got := executeVMSource(t, `
var nested=[3],source=[nested,void,7],target=[1,2];target.extra=9;
var result=target.assign(source);nested[0]=4;
var fromObject=[];fromObject.assign(%[one:1,two:2]);
return [typeof result,target.count,target[0][0],target[1],target[2],target.extra,fromObject.join('|')].join('|');`)
	if want := "void|3|4||7|9|one|1|two|2"; got.Text() != want {
		t.Fatalf("Array.assign = %q, want %q", got.Text(), want)
	}

}

func TestVMArrayMethodsSupportBoundClassCalls(t *testing.T) {
	got := executeVMSource(t, `var a=[9];(Array.split incontextof a)(',', 'x,y');return a.join('|');`)
	if got.Text() != "x|y" {
		t.Fatalf("bound Array.split = %q, want %q", got.Text(), "x|y")
	}
	got = executeVMSource(t, `var src=[3,4],a=[];a.assign((function(){}) incontextof src);return a.join('|');`)
	if got.Text() != "3|4" {
		t.Fatalf("bound Array.assign = %q, want %q", got.Text(), "3|4")
	}
}
