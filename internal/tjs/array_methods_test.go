package tjs

import (
	"strconv"
	"testing"
)

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

func TestVMInContextOfNullClearsFunctionContext(t *testing.T) {
	got := executeVMSource(t, `
var receiver = %[answer:42];
var bound = function { return this.answer; } incontextof receiver;
var unbound = bound incontextof null;
return [bound(), typeof unbound].join("|");
`)
	if want := "42|Object"; got.Text() != want {
		t.Fatalf("function incontextof null = %q, want %q", got.Text(), want)
	}
}
func TestVMArrayMethodsResolveFromFunctionContext(t *testing.T) {
	got := executeVMSource(t, `
var tmp=[];
var append=function(arr){for(var i=0;i<arr.count;i++) add(arr[i]);} incontextof tmp;
append(["ctor","params","wrap"]);
return tmp.join("|");`)
	if got.Text() != "ctor|params|wrap" {
		t.Fatalf("array method from function context = %q, want %q", got.Text(), "ctor|params|wrap")
	}
}
func TestVMArrayFindUsesArrayContext(t *testing.T) {
	got := executeVMSource(t, `
var names=["DerivedConductor","BaseConductor","BaseConductor"];
var position=(Array.find incontextof names)("BaseConductor");
var typed=[1,"1",1];
var other=%[name:"BaseConductor"];
return position==1 && names.find("BaseConductor",2)==2 && names.find("BaseConductor",3)==-1 &&
	typed.find(1,1)==2 && typed.find("1")==1 && names.find("missing")==-1 &&
	typeof other.find=="undefined";
`)
	truth, err := got.Truth()
	if err != nil || !truth {
		t.Fatalf("Array.find receiver/result = %v, %v; want true", got, err)
	}
}
func TestVMSortArrayModesAndComparator(t *testing.T) {
	got := executeVMSource(t, `
var out=[];
var modes=['+','-','0','9','a','z','?'];
for(var i=0;i<modes.count;i++){
	var a=['10','2','1'];var r=a.sort(modes[i]);
	out.add(a.join(',')+':'+typeof r);
}
var objects=[%[key:2,id:'a'],%[key:1,id:'b'],%[key:2,id:'c'],%[key:1,id:'d']];
objects.sort(function(a,b){return a.key<b.key;},true);
var ids=[];for(var i=0;i<objects.count;i++)ids.add(objects[i].id);
out.add(ids.join(''));
return out.join('|');`)
	const want = "1,10,2:void|2,10,1:void|1,2,10:void|2,10,1:void|1,10,2:void|2,10,1:void|1,10,2:void|bdac"
	if got.Text() != want {
		t.Fatalf("Array.sort modes/callback = %q, want %q", got.Text(), want)
	}
	got = executeVMSource(t, `var a=[10,2,1];(Array.sort incontextof a)("0");return a.join(',');`)
	if got.Text() != "1,2,10" {
		t.Fatalf("bound Array.sort = %q, want %q", got.Text(), "1,2,10")
	}
}

func TestVMSortArrayComparatorErrorAndBoundContext(t *testing.T) {
	got := executeVMSource(t, `
class Sorter { var direction=-1; function less(a,b){return a*direction<b*direction;} }
var s=new Sorter(),a=[1,3,2];a.sort(s.less);
var caught="";
try{[2,1].sort(function(a,b){throw 'compare';});}catch(e){caught=e;}
return a.join(',')+'|'+caught;`)
	if got.Text() != "3,2,1|compare" {
		t.Fatalf("Array.sort bound comparator/error = %q, want %q", got.Text(), "3,2,1|compare")
	}
}
func TestDispatchArraySort(t *testing.T) {
	table := NewObjectTable()
	d := NewDispatcher(table)
	array := table.New(CategoryArray)
	target := ObjectValue(array.Ref())
	for i, value := range []Value{IntegerValue(3), IntegerValue(1), IntegerValue(2)} {
		if err := array.Set(strconv.Itoa(i), value); err != nil {
			t.Fatal(err)
		}
	}
	result, err := d.CallMember(target, "sort", StringValue("0"))
	if err != nil || !result.IsVoid() {
		t.Fatalf("Array.sort result = %v, %v; want void", result, err)
	}
	joined, err := d.CallMember(target, "join", StringValue(","))
	if err != nil || joined.Text() != "1,2,3" {
		t.Fatalf("Array.sort values = %q, %v; want 1,2,3", joined.Text(), err)
	}
}
