package tjs

import (
	"errors"
	"strings"
	"testing"
)

type vmNestedEvalStorageHost struct {
	program Program
	result  Value
}

func (h *vmNestedEvalStorageHost) Call(vm *VM, name string, _ []Value, budget *uint64) (Value, error) {
	if name != "evalStorage" {
		return VoidValue, nil
	}
	result, err := vm.Execute(&h.program, h, budget)
	h.result = result
	return result, err
}

func TestVMAutomaticGCTracesNestedFramesAndExecutionContext(t *testing.T) {
	nested, err := compile("nested-eval-storage.tjs", `
var n=0;
while(n<100100){var discarded=%[value:n]; n=n+1;}
return n;
`)
	if err != nil {
		t.Fatal(err)
	}
	vm := NewVM()
	if err := vm.RegisterNative("evalStorage"); err != nil {
		t.Fatal(err)
	}
	host := &vmNestedEvalStorageHost{program: nested}
	context, err := vm.NewDictionary()
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.setMember(context.ObjectRef().Object, StringValue("answer"), IntegerValue(73)); err != nil {
		t.Fatal(err)
	}
	outer, err := compile("outer-eval-storage.tjs", `
function outer(){
	var local=%[dictionary:%[value:73]];
	evalStorage();
	return local.dictionary.value+this.answer;
}
return outer();
`)
	if err != nil {
		t.Fatal(err)
	}
	budget := uint64(1 << 60)
	got, err := vm.ExecuteInContext(&outer, context, host, &budget)
	if err != nil {
		t.Fatal(err)
	}
	if value, err := got.Int(); err != nil || value != 146 {
		t.Fatalf("nested evalStorage result = %v, %v; want 146", got, err)
	}
	if value, err := host.result.Int(); err != nil || value != 100100 {
		t.Fatalf("evalStorage result = %v, %v; want 100100", host.result, err)
	}
	if _, ok := vm.Objects.Lookup(context.ObjectRef().Object); !ok {
		t.Fatal("ExecuteInContext context was collected while its frame was active")
	}
	if vm.allocationsSinceGC >= vmGCAllocationThreshold {
		t.Fatalf("automatic collection did not run at the allocation threshold: %d", vm.allocationsSinceGC)
	}
}

func TestVMIdleGCReclaimsTemporariesAndMarksBoundContexts(t *testing.T) {
	vm := NewVM()
	object, err := vm.NewDictionary()
	if err != nil {
		t.Fatal(err)
	}
	context, err := vm.NewDictionary()
	if err != nil {
		t.Fatal(err)
	}
	dead, err := vm.NewDictionary()
	if err != nil {
		t.Fatal(err)
	}
	bound := ObjectValue(NewObjectRef(object.ObjectRef().Object, context.ObjectRef().Object))
	if err := vm.Root(bound); err != nil {
		t.Fatal(err)
	}
	removed, err := vm.CollectGarbage(vmTestHost{})
	if err != nil {
		t.Fatal(err)
	}
	if removed == 0 {
		t.Fatal("idle collection did not reclaim an unreachable temporary")
	}
	if _, ok := vm.Objects.Lookup(dead.ObjectRef().Object); ok {
		t.Fatal("unrooted temporary survived idle collection")
	}
	for _, id := range []ObjectID{object.ObjectRef().Object, context.ObjectRef().Object} {
		if _, ok := vm.Objects.Lookup(id); !ok {
			t.Fatalf("bound ObjectRef member %d was collected", id)
		}
	}
	vm.Unroot(bound)
	if _, err := vm.CollectGarbage(vmTestHost{}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []ObjectID{object.ObjectRef().Object, context.ObjectRef().Object} {
		if _, ok := vm.Objects.Lookup(id); ok {
			t.Fatalf("unrooted bound ObjectRef member %d survived collection", id)
		}
	}
}

func TestVMGarbageCollectionTracesTransientCallRoots(t *testing.T) {
	vm := NewVM()
	root, err := vm.NewDictionary()
	if err != nil {
		t.Fatal(err)
	}
	child, err := vm.NewDictionary()
	if err != nil {
		t.Fatal(err)
	}
	dead, err := vm.NewDictionary()
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.setMember(root.ObjectRef().Object, StringValue("child"), child); err != nil {
		t.Fatal(err)
	}
	vm.transientRoots = []Value{root}
	if _, err := vm.CollectGarbage(vmTestHost{}); err != nil {
		t.Fatal(err)
	}
	for _, value := range []Value{root, child} {
		if _, ok := vm.Objects.Lookup(value.ObjectRef().Object); !ok {
			t.Fatalf("transient call root object %d was collected", value.ObjectRef().Object)
		}
	}
	if _, ok := vm.Objects.Lookup(dead.ObjectRef().Object); ok {
		t.Fatal("unreachable object survived collection")
	}
}

type vmNativeAllocationRootHost struct{ root Value }

func (h *vmNativeAllocationRootHost) Call(vm *VM, _ string, _ []Value, _ *uint64) (Value, error) {
	returned, err := vm.NewDictionary()
	if err != nil {
		return VoidValue, err
	}
	h.root = returned
	vm.allocationsSinceGC = vmGCAllocationThreshold
	if _, err := vm.NewDictionary(); err != nil {
		return VoidValue, err
	}
	if _, ok := vm.Objects.Lookup(returned.ObjectRef().Object); !ok {
		return VoidValue, errors.New("native-call allocation was collected before return")
	}
	return returned, nil
}

func TestVMAutomaticGCTracesAllocationsDuringNativeInstructions(t *testing.T) {
	vm := NewVM()
	if err := vm.RegisterNative("allocateDuringCall"); err != nil {
		t.Fatal(err)
	}
	program, err := compile("native-instruction-gc-roots.tjs", "return allocateDuringCall();")
	if err != nil {
		t.Fatal(err)
	}
	host := &vmNativeAllocationRootHost{}
	budget := uint64(100)
	got, err := vm.Execute(&program, host, &budget)
	if err != nil {
		t.Fatal(err)
	}
	if !got.StrictEqual(host.root) {
		t.Fatalf("native call returned %v, want %v", got, host.root)
	}
	if _, ok := vm.Objects.Lookup(got.ObjectRef().Object); !ok {
		t.Fatal("native call result was collected before the next instruction")
	}
}

type vmCollectDuringExecutionHost struct{ err error }

func (h *vmCollectDuringExecutionHost) Call(vm *VM, _ string, _ []Value, _ *uint64) (Value, error) {
	_, h.err = vm.CollectGarbage(h)
	return VoidValue, nil
}

func TestVMExplicitCollectionRequiresIdleInterpreter(t *testing.T) {
	vm := NewVM()
	if err := vm.RegisterNative("collectDuringCall"); err != nil {
		t.Fatal(err)
	}
	program, err := compile("active-gc.tjs", "collectDuringCall(); return 1;")
	if err != nil {
		t.Fatal(err)
	}
	host := &vmCollectDuringExecutionHost{}
	budget := uint64(100)
	if _, err := vm.Execute(&program, host, &budget); err != nil {
		t.Fatal(err)
	}
	if host.err == nil || !strings.Contains(host.err.Error(), "idle interpreter") {
		t.Fatalf("collection during active execution error = %v, want idle-interpreter error", host.err)
	}
}

func TestVMAutomaticGCTracesAllocationsDuringObjectInstructions(t *testing.T) {
	for _, source := range []string{
		`class C { var answer = 73; } return (new C()).answer;`,
		`var x = %[outer:%[inner:73]]; return x.outer.inner;`,
	} {
		vm := NewVM()
		program, err := compile("instruction-gc-roots.tjs", source)
		if err != nil {
			t.Fatal(err)
		}
		vm.allocationsSinceGC = vmGCAllocationThreshold - 1
		budget := uint64(10000)
		got, err := vm.Execute(&program, vmTestHost{}, &budget)
		if err != nil {
			t.Fatalf("execute with collection during object instruction: %v", err)
		}
		if value, err := got.Int(); err != nil || value != 73 {
			t.Fatalf("object instruction result = %v, %v; want 73", got, err)
		}
	}
}

type vmFrameRootTraceHost struct {
	roots         []Value
	outgoing      map[ObjectID][]Value
	nestedProgram *Program
	nestedHost    Host
}

func (h *vmFrameRootTraceHost) Call(vm *VM, name string, _ []Value, budget *uint64) (Value, error) {
	if name != "collectNested" || h.nestedProgram == nil {
		return VoidValue, nil
	}
	vm.allocationsSinceGC = vmGCAllocationThreshold
	return vm.Execute(h.nestedProgram, h.nestedHost, budget)
}

func (h *vmFrameRootTraceHost) GCRoots() []Value { return h.roots }

func (h *vmFrameRootTraceHost) GCTrace(id ObjectID) []Value {
	return h.outgoing[id]
}

func TestVMAutomaticGCTracesRootsFromEveryActiveFrameHost(t *testing.T) {
	nested, err := compile("nested-host-gc.tjs", "return 1;")
	if err != nil {
		t.Fatal(err)
	}
	outer, err := compile("outer-host-gc.tjs", "collectNested(); return 1;")
	if err != nil {
		t.Fatal(err)
	}
	vm := NewVM()
	if err := vm.RegisterNative("collectNested"); err != nil {
		t.Fatal(err)
	}
	outerRoot, err := vm.NewDictionary()
	if err != nil {
		t.Fatal(err)
	}
	outerChild, err := vm.NewDictionary()
	if err != nil {
		t.Fatal(err)
	}
	innerRoot, err := vm.NewDictionary()
	if err != nil {
		t.Fatal(err)
	}
	innerHost := &vmFrameRootTraceHost{roots: []Value{innerRoot}}
	outerHost := &vmFrameRootTraceHost{
		roots:         []Value{outerRoot},
		outgoing:      map[ObjectID][]Value{outerRoot.ObjectRef().Object: []Value{outerChild}},
		nestedProgram: &nested,
		nestedHost:    innerHost,
	}
	budget := uint64(10000)
	if _, err := vm.Execute(&outer, outerHost, &budget); err != nil {
		t.Fatal(err)
	}
	for label, value := range map[string]Value{
		"outer host root":          outerRoot,
		"inner host root":          innerRoot,
		"outer host traced object": outerChild,
	} {
		if _, ok := vm.Objects.Lookup(value.ObjectRef().Object); !ok {
			t.Fatalf("collection lost %s", label)
		}
	}
}

func TestVMDispatchRegistryEntriesFollowCollection(t *testing.T) {
	vm := NewVM()
	if got := registry.scopeCount(vm.Objects); got != 0 {
		t.Fatalf("fresh VM registry entries = %d, want 0", got)
	}
	_ = NewNativeFunctionForVM(vm, "registryGCTest", func(ObjectRef, []Value) (Value, error) {
		return VoidValue, nil
	})
	if got := registry.scopeCount(vm.Objects); got != 1 {
		t.Fatalf("registry entries after registration = %d, want 1", got)
	}
	if _, err := vm.CollectGarbage(nil); err != nil {
		t.Fatal(err)
	}
	if got := registry.scopeCount(vm.Objects); got != 0 {
		t.Fatalf("registry entries after collection = %d, want 0", got)
	}
}

func TestVMReleaseDropsDispatchRegistry(t *testing.T) {
	vm := NewVM()
	_ = NewNativeFunctionForVM(vm, "releaseTest", func(ObjectRef, []Value) (Value, error) {
		return VoidValue, nil
	})
	if err := vm.Release(); err != nil {
		t.Fatal(err)
	}
	if vm.Objects != nil {
		t.Fatal("VM release retained the object table")
	}
}
