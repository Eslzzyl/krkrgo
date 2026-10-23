package tjs

import (
	"strconv"
	"strings"
	"testing"
)

func TestVMDictionaryAssignStructDeepCopiesAndBreaksCycles(t *testing.T) {
	got := executeVMSource(t, `
var shared=%[n:2],s=%[a:[shared,shared],x:3],d=%[old:4];
s.self=s;
(Dictionary.assignStruct incontextof d)(s);
d.a[0].n=9;
return [s.a[0].n,d.a[1].n,typeof d.old,d.x,d.a[0]===d.a[1],d.a[0]===shared,d.self===null].join('|');`)
	if want := "2|2|undefined|3|0|0|1"; got.Text() != want {
		t.Fatalf("Dictionary.assignStruct = %q, want %q", got.Text(), want)
	}
}

func TestVMDictionaryAssignStructImportsSharedTableSource(t *testing.T) {
	vm := NewVM()
	source := vm.Objects.New(CategoryDictionary)
	if err := source.Set("value", IntegerValue(7)); err != nil {
		t.Fatal(err)
	}
	target, err := vm.NewDictionary()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := vm.assignStructure(target, []Value{ObjectValue(source.Ref())}, false, nil); err != nil {
		t.Fatal(err)
	}
	got, ok, err := vm.getMember(target.ObjectRef().Object, StringValue("value"))
	if err != nil {
		t.Fatal(err)
	}
	value, intErr := got.Int()
	if !ok || intErr != nil || value != 7 {
		t.Fatalf("imported dictionary value = %v, present=%t; want 7", got, ok)
	}
}

func TestVMDictionaryAssignImportsSharedTableArraySource(t *testing.T) {
	vm := NewVM()
	source := vm.Objects.New(CategoryArray)
	for index, value := range []Value{StringValue("width"), IntegerValue(1920), StringValue("height"), IntegerValue(1080)} {
		if err := source.SetString(strconv.Itoa(index), value); err != nil {
			t.Fatal(err)
		}
	}
	target, err := vm.NewDictionary()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := vm.dictionaryAssign(target, []Value{ObjectValue(source.Ref())}, nil); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]int64{"width": 1920, "height": 1080} {
		got, ok, err := vm.getMemberString(target.ObjectRef().Object, name)
		if err != nil || !ok || got.IntegerValue() != want {
			t.Fatalf("imported array pair %s = (%v, %v, %v), want %d", name, got, ok, err, want)
		}
	}
}

func TestVMDictionaryAssignStructKeepsSourceObjectWithGlobalContext(t *testing.T) {
	vm := NewVM()
	source := vm.Objects.New(CategoryDictionary)
	if err := source.Set("value", IntegerValue(9)); err != nil {
		t.Fatal(err)
	}
	target, err := vm.NewDictionary()
	if err != nil {
		t.Fatal(err)
	}
	sourceValue := ObjectValue(NewObjectRef(source.ID, 0))
	if _, err := vm.assignStructure(target, []Value{sourceValue}, false, nil); err != nil {
		t.Fatal(err)
	}
	got, ok, err := vm.getMember(target.ObjectRef().Object, StringValue("value"))
	if err != nil {
		t.Fatal(err)
	}
	value, intErr := got.Int()
	if !ok || intErr != nil || value != 9 {
		t.Fatalf("global-context source value = %v, present=%t; want 9", got, ok)
	}
}

func TestVMArrayAssignStructDeepCopiesAndSelfClears(t *testing.T) {
	got := executeVMSource(t, `
var node=%[n:1],source=[node,node],target=[9];
target.assignStruct(source);
target[0].n=7;
var self=[1,2];self.extra=9;self.assignStruct(self);
return [source[0].n,target[1].n,target[0]===target[1],target[0]===node,self.count,self.extra].join('|');`)
	if want := "1|1|0|0|0|9"; got.Text() != want {
		t.Fatalf("Array.assignStruct = %q, want %q", got.Text(), want)
	}
}
func TestVMArrayAssignStructRetainsOtherObjectReferences(t *testing.T) {
	got := executeVMSource(t, `
class C {var n=2;}
var c=new C(),f=function(){return this.n;},source=[c,f incontextof c],target=[];
target.assignStruct(source);
return [target[0]===c,target[1](),target[1]===source[1]].join('|');`)
	if want := "1|2|1"; got.Text() != want {
		t.Fatalf("Array.assignStruct other objects = %q, want %q", got.Text(), want)
	}
}

func TestVMAssignStructTypeErrorsPreserveOfficialClearOrder(t *testing.T) {
	got := executeVMSource(t, `
var a=[7],d=%[x:8];
try{a.assignStruct(d);}catch(e){}
try{(Dictionary.assignStruct incontextof d)(a);}catch(e){}
return [a.count,typeof a[0],d.x].join('|');`)
	if want := "0|undefined|8"; got.Text() != want {
		t.Fatalf("assignStruct mismatch effects = %q, want %q", got.Text(), want)
	}
}

func TestVMDictionaryAssignIsShallowAndSupportsPairs(t *testing.T) {
	got := executeVMSource(t, `
var child=%[n:1],source=%[child:child],target=%[old:4];
(Dictionary.assign incontextof target)(source);
target.child.n=3;
var keep=%[old:5];(Dictionary.assign incontextof keep)(["new",8,"odd"],false);
return [source.child.n,typeof target.old,keep.old,keep.new,typeof keep.odd,target.child===source.child].join('|');`)
	if want := "3|undefined|5|8|undefined|1"; got.Text() != want {
		t.Fatalf("Dictionary.assign = %q, want %q", got.Text(), want)
	}
}

func TestVMDictionaryAssignKeepsBoundSourceObject(t *testing.T) {
	vm := NewVM()
	source := vm.Objects.New(CategoryDictionary)
	if err := source.Set("value", IntegerValue(11)); err != nil {
		t.Fatal(err)
	}
	context := vm.Objects.New(CategoryNative)
	target, err := vm.NewDictionary()
	if err != nil {
		t.Fatal(err)
	}
	sourceValue := ObjectValue(NewObjectRef(source.ID, context.ID))
	if _, err := vm.dictionaryAssign(target, []Value{sourceValue}, nil); err != nil {
		t.Fatal(err)
	}
	got, ok, err := vm.getMember(target.ObjectRef().Object, StringValue("value"))
	if err != nil {
		t.Fatal(err)
	}
	value, intErr := got.Int()
	if !ok || intErr != nil || value != 11 {
		t.Fatalf("bound source value = %v, present=%t; want 11", got, ok)
	}
}

func TestVMDictionaryClearSupportsStaticCalls(t *testing.T) {
	got := executeVMSource(t, `
var d=%[one:1];(Dictionary.clear incontextof d)();
var first=d.one===void;d.two=2;(Dictionary.clear incontextof d)();
return [first,d.two===void].join('|');`)
	if want := "1|1"; got.Text() != want {
		t.Fatalf("Dictionary.clear = %q, want %q", got.Text(), want)
	}
}

func TestVMDictionaryMembersAreStaticOnly(t *testing.T) {
	// Kirikiri registers Dictionary.assign/assignStruct/clear as static
	// members, so instances must not expose them: KAG tag elements are
	// dictionaries and scripts probe attributes such as elm.clear.
	got := executeVMSource(t, `
var d=%[one:1];
var instanceClear=d.clear===void, instanceAssign=d.assign===void, instanceAssignStruct=d.assignStruct===void;
(Dictionary.assign incontextof d)(%[two:2]);
(Dictionary.clear incontextof d)();
var a=[1]; a.clear();
return [instanceClear,instanceAssign,instanceAssignStruct,typeof d.two,d.one===void,a.count].join('|');`)
	if want := "1|1|1|undefined|1|0"; got.Text() != want {
		t.Fatalf("Dictionary static members = %q, want %q", got.Text(), want)
	}
}

func TestVMAssignStructBudgetAndDepthLimits(t *testing.T) {
	vm := NewVM()
	source, err := vm.NewDictionary()
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.setMember(source.ObjectRef().Object, StringValue("value"), IntegerValue(1)); err != nil {
		t.Fatal(err)
	}
	target, err := vm.NewDictionary()
	if err != nil {
		t.Fatal(err)
	}
	budget := uint64(1)
	if _, err := vm.assignStructure(target, []Value{source}, false, &budget); err == nil || !strings.Contains(err.Error(), "execution budget exceeded") {
		t.Fatalf("assignStruct budget error = %v", err)
	}

	vm = NewVM()
	source, err = vm.NewDictionary()
	if err != nil {
		t.Fatal(err)
	}
	current := source
	for range 128 {
		next, err := vm.NewDictionary()
		if err != nil {
			t.Fatal(err)
		}
		if err := vm.setMember(current.ObjectRef().Object, StringValue("next"), next); err != nil {
			t.Fatal(err)
		}
		current = next
	}
	target, err = vm.NewDictionary()
	if err != nil {
		t.Fatal(err)
	}
	budget = 10000
	if _, err := vm.assignStructure(target, []Value{source}, false, &budget); err == nil || !strings.Contains(err.Error(), "nesting limit exceeded") {
		t.Fatalf("assignStruct depth error = %v", err)
	}
}
