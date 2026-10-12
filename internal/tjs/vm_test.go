package tjs

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

type vmTestHost struct{}

func (vmTestHost) Call(_ *VM, _ string, _ []Value, _ *uint64) (Value, error) { return VoidValue, nil }

type vmSuperclassInitHost struct{ called bool }

func (vmSuperclassInitHost) Call(_ *VM, _ string, _ []Value, _ *uint64) (Value, error) {
	return VoidValue, nil
}

func (h *vmSuperclassInitHost) CallWithContext(_ *VM, name string, _ Value, _ []Value, _ *uint64) (Value, error) {
	if name == "SuperclassGetterBaseInitialize" {
		h.called = true
	}
	return VoidValue, nil
}

type vmRecordingTraceHost struct{ instructions []Instruction }
type vmGlobalConstructionHost struct {
	observed bool
}

func (h *vmGlobalConstructionHost) Call(_ *VM, _ string, _ []Value, _ *uint64) (Value, error) {
	return VoidValue, nil
}

func (h *vmGlobalConstructionHost) CallWithContext(vm *VM, name string, context Value, _ []Value, _ *uint64) (Value, error) {
	if name == "C.@initialize" {
		global, err := vm.obj(0)
		if err != nil {
			return VoidValue, err
		}
		value, ok := global.Get("x")
		h.observed = ok && value.StrictEqual(context)
	}
	return VoidValue, nil
}

func (h *vmRecordingTraceHost) Trace(instruction *Instruction) error {
	h.instructions = append(h.instructions, *instruction)
	return nil
}
func (h *vmRecordingTraceHost) Call(_ *VM, _ string, _ []Value, _ *uint64) (Value, error) {
	return VoidValue, nil
}

func TestLegacyClassCallDispatchesBodyWithCurrentContext(t *testing.T) {
	vm := NewVM()
	class, err := vm.RegisterLegacyClass("LegacyClassCallTest")
	if err != nil {
		t.Fatal(err)
	}
	receiver, receiverID, err := vm.alloc(CategoryInstance)
	if err != nil {
		t.Fatal(err)
	}
	arg := StringValue("base argument")
	called := false
	if err := vm.SetLegacyClassBody(class, func(context Value, args []Value, _ Host, _ *uint64, needed bool) (Value, error) {
		called = true
		if !context.StrictEqual(receiver) {
			t.Fatalf("class body context = %v, want current receiver %v", context, receiver)
		}
		if len(args) != 1 || !args[0].StrictEqual(arg) {
			t.Fatalf("class body args = %v, want [%v]", args, arg)
		}
		if !needed {
			t.Fatal("class body did not receive result-needed flag")
		}
		return IntegerValue(int64(receiverID)), nil
	}); err != nil {
		t.Fatal(err)
	}
	got, err := vm.InvokeLegacy(class, receiver, []Value{arg}, vmTestHost{}, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if !called || got.Kind() != KindInteger || got.IntegerValue() != int64(receiverID) {
		t.Fatalf("class call = %v, called=%t; want receiver id %d", got, called, receiverID)
	}
}

func TestLegacyClassCallKeepsNonCallableObjectsNonCallable(t *testing.T) {
	vm := NewVM()
	object, _, err := vm.alloc(CategoryDictionary)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := vm.InvokeLegacy(object, VoidValue, nil, vmTestHost{}, nil, true); err == nil || !strings.Contains(err.Error(), "TJS object is not callable") {
		t.Fatalf("dictionary call error = %v, want not-callable error", err)
	}
}
func TestLegacyClassCallLinksClassBaseOnce(t *testing.T) {
	vm := NewVM()
	base, baseID, err := vm.alloc(CategoryClass)
	if err != nil {
		t.Fatal(err)
	}
	vm.meta[baseID].nativeClass = true
	derived, derivedID, err := vm.alloc(CategoryClass)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := vm.InvokeLegacy(base, derived, nil, vmTestHost{}, nil, false); err != nil {
		t.Fatal(err)
	}
	if _, err := vm.InvokeLegacy(base, derived, nil, vmTestHost{}, nil, false); err != nil {
		t.Fatal(err)
	}
	if got := vm.meta[derivedID].classBases; len(got) != 1 || got[0] != baseID {
		t.Fatalf("derived class bases = %v, want exactly [%d]", got, baseID)
	}
}

func TestVMTraceAndBudgetObserveEveryExecutedInstruction(t *testing.T) {
	program, err := compile("trace-budget.tjs", "var x=1; return x;")
	if err != nil {
		t.Fatal(err)
	}
	host := &vmRecordingTraceHost{}
	vm := NewVM()
	budget := uint64(2)
	_, err = vm.Execute(&program, host, &budget)
	if err == nil || !strings.Contains(err.Error(), "execution budget exhausted") {
		t.Fatalf("execution error = %v, want budget exhaustion", err)
	}
	if vm.Executed != 2 || budget != 0 {
		t.Fatalf("executed/budget = %d/%d, want 2/0", vm.Executed, budget)
	}
	if len(host.instructions) != 2 {
		t.Fatalf("trace count = %d, want 2", len(host.instructions))
	}
	for i, got := range host.instructions {
		if !reflect.DeepEqual(got, program.Code[i]) {
			t.Fatalf("trace[%d] = %+v, want instruction %+v", i, got, program.Code[i])
		}
	}
	if !strings.Contains(err.Error(), program.Code[2].Location.Storage) {
		t.Fatalf("budget error %q omits next instruction storage %q", err, program.Code[2].Location.Storage)
	}
}

func TestVMReexecutesMutatedProgramCode(t *testing.T) {
	program := Program{
		Storage:   "mutable-program.tjs",
		Registers: 1,
		Code: []Instruction{
			{Op: Op{Code: OpConstant, Out: 0, Value: IntegerValue(7)}},
			{Op: Op{Code: OpReturn, Input: 0}},
		},
	}
	vm := NewVM()
	for _, want := range []int64{7, 19} {
		program.Code[0].Op.Value = IntegerValue(want)
		budget := uint64(10)
		got, err := vm.Execute(&program, vmTestHost{}, &budget)
		if err != nil {
			t.Fatal(err)
		}
		value, err := got.Int()
		if err != nil || value != want {
			t.Fatalf("execution after Program.Code mutation = %v, %v; want %d", got, err, want)
		}
	}
}

func TestVMVoidFunctionGuardDoesNotInvoke(t *testing.T) {
	vm := NewVM()
	program, err := compile("void-function-guard.tjs", `
		var called = 0;
		var EndFunction;
		if (EndFunction !== void) EndFunction();
		EndFunction = function() { called++; };
		if (EndFunction !== void) EndFunction();
		return called;
	`)
	if err != nil {
		t.Fatal(err)
	}
	budget := uint64(1000)
	got, err := vm.Execute(&program, vmTestHost{}, &budget)
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := got.Int(); n != 1 {
		t.Fatalf("call count = %v, want 1", got)
	}
}

func TestVMArithmeticProgram(t *testing.T) {
	vm := NewVM()
	p := Program{Storage: "test", Registers: 3, Code: []Instruction{
		{Op: Op{Code: OpConstant, Out: 0, Value: IntegerValue(2)}},
		{Op: Op{Code: OpConstant, Out: 1, Value: IntegerValue(3)}},
		{Op: Op{Code: OpBinary, Out: 2, Operator: "+", Left: 0, Right: 1}},
		{Op: Op{Code: OpReturn, Input: 2}},
	}}
	budget := uint64(100)
	got, e := vm.Execute(&p, vmTestHost{}, &budget)
	if e != nil {
		t.Fatal(e)
	}
	if n, _ := got.Int(); n != 5 {
		t.Fatalf("got %v", got)
	}
}

func TestVMTopLevelDeclaredNameLookup(t *testing.T) {
	vm := NewVM()
	program, err := compile("top-level-name.tjs", "var x = 7; return x;")
	if err != nil {
		t.Fatal(err)
	}
	budget := uint64(100)
	got, err := vm.Execute(&program, vmTestHost{}, &budget)
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := got.Int(); n != 7 {
		t.Fatalf("value = %v, want 7", got)
	}
}

func TestVMTopLevelDeclaredNameUpdate(t *testing.T) {
	vm := NewVM()
	program, err := compile("top-level-name-update.tjs", "var x = 1; x++; return x;")
	if err != nil {
		t.Fatal(err)
	}
	budget := uint64(100)
	got, err := vm.Execute(&program, vmTestHost{}, &budget)
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := got.Int(); n != 2 {
		t.Fatalf("value = %v, want 2", got)
	}
}
func TestVMInvalidationAndStableIDs(t *testing.T) {
	vm := NewVM()
	x, e := vm.NewDictionary()
	if e != nil {
		t.Fatal(e)
	}
	id := x.ObjectRef().Object
	if _, e = vm.Invalidate(x, vmTestHost{}, nil); e != nil {
		t.Fatal(e)
	}
	if vm.IsValid(x).IntegerValue() != 0 {
		t.Fatal("object remained valid")
	}
	y, e := vm.NewDictionary()
	if e != nil {
		t.Fatal(e)
	}
	if y.ObjectRef().Object <= id {
		t.Fatal("object ID was reused")
	}
}

type vmNativeClassHost struct {
	initialized ObjectID
	invalidated ObjectID
	calls       []ObjectID
	value       map[ObjectID]int64
}

func (h *vmNativeClassHost) Call(_ *VM, _ string, _ []Value, _ *uint64) (Value, error) {
	return VoidValue, nil
}

func (h *vmNativeClassHost) CallWithContext(_ *VM, name string, context Value, args []Value, _ *uint64) (Value, error) {
	id := context.ObjectRef().Object
	switch name {
	case "Tiny.@initialize":
		h.initialized = id
	case "Tiny.@invalidate":
		h.invalidated = id
	case "Tiny.bump":
		h.calls = append(h.calls, id)
		increment := int64(1)
		if len(args) > 0 {
			var err error
			increment, err = args[0].Int()
			if err != nil {
				return VoidValue, err
			}
		}
		h.value[id] += increment
	case "Tiny.get:value":
		return IntegerValue(h.value[id]), nil
	case "Tiny.set:value":
		n, err := args[0].Int()
		if err != nil {
			return VoidValue, err
		}
		h.value[id] = n
	}
	return VoidValue, nil
}

func TestVMNativeClassGlobalBinding(t *testing.T) {
	vm := NewVM()
	class, err := vm.RegisterNativeClass("NativeGlobalTest")
	if err != nil {
		t.Fatal(err)
	}
	program, err := compile("native-class-global.tjs", "var viaGlobal = new global.NativeGlobalTest(); var viaName = new NativeGlobalTest(); return viaGlobal;")
	if err != nil {
		t.Fatal(err)
	}
	budget := uint64(1000)
	got, err := vm.Execute(&program, vmTestHost{}, &budget)
	if err != nil {
		t.Fatal(err)
	}
	instanceID := got.ObjectRef().Object
	if got.Kind() != KindObject || vm.meta[instanceID] == nil || vm.meta[instanceID].classObject != class.ObjectRef().Object {
		t.Fatalf("global.NativeGlobalTest instance = %v, want instance of class %v", got, class.ObjectRef().Object)
	}
}

func TestVMGlobalMemberAssignmentExposesConstructedObjectDuringConstruction(t *testing.T) {
	program, err := compile("global-member-construction.tjs", `
global.x = new C();
return true;
`)
	if err != nil {
		t.Fatal(err)
	}
	vm := NewVM()
	if _, err := vm.NewNativeClass("C", "C.@initialize"); err != nil {
		t.Fatal(err)
	}
	host := &vmGlobalConstructionHost{}
	budget := uint64(1000)
	got, err := vm.Execute(&program, host, &budget)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := got.Truth()
	if err != nil || !ok || !host.observed {
		t.Fatalf("global member assignment result = %v, %v; constructor observed in-progress instance = %v", got, err, host.observed)
	}
}

func TestVMNativeClassGlobalBindingPreservesExistingMember(t *testing.T) {
	vm := NewVM()
	const name = "NativeGlobalConflict"
	setup, err := compile("native-class-global-setup.tjs", "var NativeGlobalConflict = 42;")
	if err != nil {
		t.Fatal(err)
	}
	budget := uint64(1000)
	if _, err := vm.Execute(&setup, vmTestHost{}, &budget); err != nil {
		t.Fatal(err)
	}
	scriptValue := IntegerValue(42)
	if _, err := vm.RegisterNativeClass(name); err != nil {
		t.Fatal(err)
	}
	program, err := compile("native-class-global-conflict.tjs", "return global.NativeGlobalConflict;")
	if err != nil {
		t.Fatal(err)
	}
	budget = 1000
	got, err := vm.Execute(&program, vmTestHost{}, &budget)
	if err != nil {
		t.Fatal(err)
	}
	if !got.StrictEqual(scriptValue) {
		t.Fatalf("global.%s = %v, want existing script value %v", name, got, scriptValue)
	}
}

func TestVMNativeClassInstanceDispatch(t *testing.T) {
	vm := NewVM()
	class, err := vm.RegisterNativeClass("Tiny")
	if err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"@initialize", "@invalidate", "bump"} {
		if err := vm.RegisterNativeMethod(class, method, "Tiny."+method); err != nil {
			t.Fatal(err)
		}
	}
	if err := vm.RegisterNativeProperty(class, "value", "Tiny.get:value", "Tiny.set:value"); err != nil {
		t.Fatal(err)
	}
	program, err := compile("native-class.tjs", "var x = new Tiny(); x.value = 4; var f = x.bump; f(); function invoke(o, amount) { with (o) { .bump(amount); } } invoke(x, 6); return x.value;")
	if err != nil {
		t.Fatal(err)
	}
	host := &vmNativeClassHost{value: make(map[ObjectID]int64)}
	budget := uint64(1000)
	got, err := vm.Execute(&program, host, &budget)
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := got.Int(); n != 11 {
		t.Fatalf("value = %v, want 11 after passing amount through with-dot native call", got)
	}
	if host.initialized == 0 || len(host.calls) != 2 || host.calls[0] != host.initialized || host.calls[1] != host.initialized {
		t.Fatalf("native context calls = %#v, initialized = %d", host.calls, host.initialized)
	}
	instance := ObjectValue(NewObjectRef(host.initialized))
	if _, err := vm.Invalidate(instance, host, &budget); err != nil {
		t.Fatal(err)
	}
	if host.invalidated != host.initialized {
		t.Fatalf("invalidated = %d, want %d", host.invalidated, host.initialized)
	}
}

func executeVMSource(t *testing.T, source string) Value {
	t.Helper()
	program, err := compile("vm-focused.tjs", source)
	if err != nil {
		t.Fatal(err)
	}
	budget := uint64(10000)
	value, err := NewVM().Execute(&program, vmTestHost{}, &budget)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestVMRegExpCompileInternalEncoding(t *testing.T) {
	got := executeVMSource(t, `
var r = new RegExp();
var result = r._compile("//liig/foo");
return result === void && r.test("FOO");
`)
	truth, err := got.Truth()
	if err != nil || !truth {
		t.Fatalf("RegExp._compile result = %v, %v; want true", got, err)
	}
}

func TestVMRegExpCompileInternalEncodingErrors(t *testing.T) {
	for _, source := range []string{
		`var r=new RegExp();r._compile("bad");`,
		`var r=new RegExp();r._compile("//i");`,
		`var r=new RegExp();r._compile("//i/(");`,
		`var r=new RegExp();r._compile();`,
		`var r=new RegExp();r._compile("//i/x","extra");`,
	} {
		program, err := compile("regexp-compile-error.tjs", source)
		if err != nil {
			t.Fatalf("compile %q: %v", source, err)
		}
		budget := uint64(10000)
		if _, err := NewVM().Execute(&program, vmTestHost{}, &budget); err == nil {
			t.Errorf("RegExp._compile source %q unexpectedly succeeded", source)
		}
	}
}
func TestVMFunctionDeclarationReplacesGlobalProperty(t *testing.T) {
	vm := NewVM()
	program, err := compile("function-property.tjs", `
property lazyFunction { getter { return void; } }
function lazyFunction() { return 7; }
return lazyFunction();
`)
	if err != nil {
		t.Fatal(err)
	}
	budget := uint64(10000)
	got, err := vm.Execute(&program, vmTestHost{}, &budget)
	if err != nil {
		t.Fatal(err)
	}
	result, err := got.Int()
	if err != nil || result != 7 {
		t.Fatalf("function declaration result = %v, %v; want 7", got, err)
	}
}

func TestVMGlobalFunctionDeclarationIsAvailableBeforeItsStatement(t *testing.T) {
	got := executeVMSource(t, `
SystemActionBase = %[];
with (SystemActionBase) {
	.min = MinimizeOnPanicMode;
}
function MinimizeOnPanicMode() { return 7; }
return SystemActionBase.min();
`)
	if result, err := got.Int(); err != nil || result != 7 {
		t.Fatalf("forward global function result = %v, %v; want 7", got, err)
	}
}

func TestVMOrdinaryDictionaryMissingMemberIsVoid(t *testing.T) {
	got := executeVMSource(t, `
var direct = %[];
var nested = %[];
nested.child = %[];
return direct.missing === void &&
	nested.child.missing === void &&
	typeof direct.missing == "undefined" &&
	typeof nested.child.missing == "undefined";
`)
	truth, err := got.Truth()
	if err != nil || !truth {
		t.Fatalf("missing dictionary member result = %v, %v; want true", got, err)
	}
}

func TestVMNonDictionaryMissingMemberStillErrors(t *testing.T) {
	for _, test := range []struct {
		name   string
		source string
	}{
		{name: "native", source: `return Dictionary.missing;`},
		{name: "class", source: `class MissingMemberClass {}
return MissingMemberClass.missing;`},
		{name: "instance", source: `class MissingMemberClass {}
var instance = new MissingMemberClass();
return instance.missing;`},
	} {
		t.Run(test.name, func(t *testing.T) {
			program, err := compile("missing-member-"+test.name+".tjs", test.source)
			if err != nil {
				t.Fatal(err)
			}
			_, err = NewVM().Execute(&program, vmTestHost{}, nil)
			if err == nil || !strings.Contains(err.Error(), "member not found: missing") {
				t.Fatalf("missing %s member error = %v; want member-not-found error", test.name, err)
			}
		})
	}
}

func TestVMOptionalMissingMemberStillReturnsVoid(t *testing.T) {
	vm := NewVM()
	target := vm.Globals["Dictionary"]
	program := Program{Storage: "optional-missing-member.tjs", Registers: 3, Code: []Instruction{
		{Op: Op{Code: OpConstant, Out: 0, Value: target}},
		{Op: Op{Code: OpConstant, Out: 1, Value: StringValue("missing")}},
		{Op: Op{Code: OpGet, Out: 2, Object: 0, Key: 1, Optional: true}},
		{Op: Op{Code: OpReturn, Input: 2}},
	}}
	got, err := vm.Execute(&program, vmTestHost{}, nil)
	if err != nil || !got.IsVoid() {
		t.Fatalf("optional missing native member = %v, %v; want void", got, err)
	}
}

func TestVMCompiledClassesInheritanceSuperAndProperties(t *testing.T) {
	source := `
class Empty {}
class Left {
	var amount = 2;
	function Left(value) { amount = value; }
	function label() { return "left"; }
	property amountProperty {
		getter { return amount; }
		setter(value) { amount = value; }
	}
}
class Right {
	var bonus = 20;
	function bonusValue() { return bonus; }
}
class Child extends (1 ? Left : Left), Right {
	var total = amount + bonusValue();
	function Child(value) { super.Left(value); }
	function label() { return super.label() + " child"; }
	function parentLabel() { var parent = super; return parent.label(); }
	property amountProperty {
		getter { return super.amountProperty + 1; }
		setter(value) { super.amountProperty = value - 1; }
	}
}
var empty = new Empty();
var item = new Child(9);
item.amountProperty = 14;
return item.amount * 100 + item.total +
	(item.label() == "left child" ? 1 : 0) +
	(item.amountProperty == 14 ? 10 : 0) +
	(item.parentLabel() == "left" ? 100 : 0) +
	(item instanceof "Left" ? 1000 : 0);
`
	program, err := compile("script-classes.tjs", source)
	if err != nil {
		t.Fatal(err)
	}
	vm := NewVM()
	budget := uint64(10000)
	got, err := vm.Execute(&program, vmTestHost{}, &budget)
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := got.Int(); n != 2433 {
		t.Fatalf("class result = %v, want 2433", got)
	}
	for _, test := range []struct {
		name string
		want []string
	}{
		{name: "empty", want: []string{"Empty"}},
		{name: "item", want: []string{"Child", "Left", "Right"}},
	} {
		value, ok, err := vm.getMember(0, StringValue(test.name))
		if err != nil || !ok {
			t.Fatalf("global %s lookup = (%v, %v)", test.name, ok, err)
		}
		names, err := vm.ClassNames(value)
		if err != nil {
			t.Fatalf("class names for %s: %v", test.name, err)
		}
		if !reflect.DeepEqual(names, test.want) {
			t.Errorf("class names for %s = %v, want %v", test.name, names, test.want)
		}
	}
}

func TestVMClassAndFunctionDefaultParameters(t *testing.T) {
	got := executeVMSource(t, `
function standalone(value = 4) { return value; }
class Defaulted {
	var result = 0;
	function Defaulted(base = 10, next = base + 1) { result = base + next; }
	function add(amount = 2) { return result + amount; }
}
var instance = new Defaulted();
return standalone() + standalone(8) + instance.add() +
	instance.add(void) + instance.add(3);
`)
	if n, _ := got.Int(); n != 82 {
		t.Fatalf("default-parameter result = %v, want 82", got)
	}
}

type vmScriptClassNativeHost struct {
	calls  int
	values []int64
}

func (h *vmScriptClassNativeHost) Call(_ *VM, _ string, _ []Value, _ *uint64) (Value, error) {
	return VoidValue, nil
}

func (h *vmScriptClassNativeHost) CallWithContext(vm *VM, name string, context Value, args []Value, _ *uint64) (Value, error) {
	if name != "NativeBase.@initialize" {
		return VoidValue, nil
	}
	value := IntegerValue(0)
	if len(args) != 0 {
		value = args[0]
	}
	number, err := value.Int()
	if err != nil {
		return VoidValue, err
	}
	h.calls++
	h.values = append(h.values, number)
	return VoidValue, vm.setMember(context.ObjectRef().Object, StringValue("nativeValue"), value)
}

func TestVMCompiledClassExtendingNativeBase(t *testing.T) {
	vm := NewVM()
	_, err := vm.NewNativeClass("NativeBase", "NativeBase.@initialize")
	if err != nil {
		t.Fatal(err)
	}
	program, err := compile("native-class-base.tjs", `
class ScriptChild extends NativeBase {
	var extra = 3;
	function ScriptChild(value) { super.NativeBase(value); }
	function read() { return nativeValue + extra; }
}
var instance = new ScriptChild(17);
return instance.read();
`)
	if err != nil {
		t.Fatal(err)
	}
	host := &vmScriptClassNativeHost{}
	budget := uint64(10000)
	got, err := vm.Execute(&program, host, &budget)
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := got.Int(); n != 20 {
		t.Fatalf("native-base class result = %v, want 20", got)
	}
	if host.calls != 2 || !reflect.DeepEqual(host.values, []int64{0, 17}) {
		t.Fatalf("native base initializer calls = %d with %v, want 2 calls [0 17]", host.calls, host.values)
	}
}

func TestVMThisIdentityAndScriptPropertyOverridesNativeBase(t *testing.T) {
	vm := NewVM()
	class, err := vm.RegisterNativeClass("Tiny")
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.RegisterNativeProperty(class, "value", "Tiny.get:value", ""); err != nil {
		t.Fatal(err)
	}
	program, err := compile("native-property-override.tjs", `
class ScriptChild extends Tiny {
	property value {
		getter() { return (this === target ? 100 : 0) + super.value; }
	}
	function matchesTarget() { return this === target; }
}
var target = new ScriptChild();
var boundMethod = target.matchesTarget;
var nativeInstance = new Tiny();
return (target.value === 100 ? 1 : 0) * 100 +
	(boundMethod() ? 1 : 0) * 10 +
	(nativeInstance.value === 0 ? 1 : 0);
`)
	if err != nil {
		t.Fatal(err)
	}
	host := &vmNativeClassHost{value: make(map[ObjectID]int64)}
	budget := uint64(10000)
	got, err := vm.Execute(&program, host, &budget)
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := got.Int(); n != 111 {
		t.Fatalf("script property and this identity result = %v, want 111", got)
	}
}
func TestVMObjectStrictEqualityUsesIdentityNotBoundContext(t *testing.T) {
	instance := ObjectValue(NewObjectRef(17, 17))
	bound := ObjectValue(NewObjectRef(17, 23))
	other := ObjectValue(NewObjectRef(23, 17))
	if !instance.StrictEqual(bound) {
		t.Fatal("same object with a different bound context was not strictly equal")
	}
	if instance.StrictEqual(other) {
		t.Fatal("different objects with the same context were strictly equal")
	}
}

func TestVMCompiledClassExtendingBuiltinException(t *testing.T) {
	got := executeVMSource(t, `
class ScriptException extends Exception {
	function ScriptException(message) { super.Exception(message); }
}
var problem = new ScriptException("native base");
return problem.message;
`)
	if got.Text() != "native base" {
		t.Fatalf("native base constructor message = %q, want %q", got.Text(), "native base")
	}
}

func TestVMGlobalScriptPropertyGetterAndSetter(t *testing.T) {
	got := executeVMSource(t, `var backing=1; property value { getter { return backing; } setter (v) { backing=v; } } value=9; return value;`)
	if n, _ := got.Int(); n != 9 {
		t.Fatalf("property value = %v, want 9", got)
	}
}

func TestVMReadOnlyScriptProperty(t *testing.T) {
	got := executeVMSource(t, `property answer { getter() { return 42; } } answer=7; return answer;`)
	if n, _ := got.Int(); n != 42 {
		t.Fatalf("read-only property value = %v, want 42", got)
	}
}

func TestVMPropertyReferenceUsesBoundContext(t *testing.T) {
	got := executeVMSource(t, `
class Hooked {
	property value {
		getter { return __propertyHook.value; }
		setter(v) { __propertyHook.value = v; }
	}
}
var first = new Hooked();
var second = new Hooked();
first.__propertyHook = %[value:4];
second.__propertyHook = %[value:9];
var reference = (&first.value) incontextof second;
var original = *reference;
*reference = 17;
return original * 1000 + second.value * 10 + first.value;
`)
	if n, _ := got.Int(); n != 9174 {
		t.Fatalf("bound property reference result = %v, want 9174", got)
	}
}
func TestVMQuickMenuProxyPropertyReferenceSurvivesNestedGet(t *testing.T) {
	got := executeVMSource(t, `
class Proxy {
	var target;
	function Proxy() { target = %[enabled:true]; }
	property proxyEnabled {
		getter { return target.enabled; }
		setter(v) { target.enabled = v; }
	}
	function getMap() { return %[skip:%[enabled:&proxyEnabled]]; }
}
var proxy = new Proxy(), map = proxy.getMap();
var enabled = map.skip.enabled;
map.skip.enabled = false;
return enabled && !proxy.target.enabled;
`)
	truth, err := got.Truth()
	if err != nil || !truth {
		t.Fatalf("nested quickmenu proxy property reference = %v, %v; want true", got, err)
	}
}
func TestVMWithRawPropertyAssignment(t *testing.T) {
	got := executeVMSource(t, `var backing=0; var object=%[field:0]; property original { getter() { return -1; } setter(v) { backing=v; } } property source { getter() { return backing; } setter(v) { backing=v; } } object.field = &original; with(object) { &.field = &source; } *(&object.field)=29; return *(&object.field);`)
	if n, _ := got.Int(); n != 29 {
		t.Fatalf("raw property reference value = %v, want 29", got)
	}
}

func TestVMStartupRawPropertyAssignmentFragment(t *testing.T) {
	vm := NewVM()
	window, err := vm.NewDictionary()
	if err != nil {
		t.Fatal(err)
	}
	if err = vm.setMember(window.ObjectRef().Object, StringValue("value"), IntegerValue(11)); err != nil {
		t.Fatal(err)
	}
	if err = vm.setMember(0, StringValue("Window"), window); err != nil {
		t.Fatal(err)
	}
	program, err := compile("startup-fragment.tjs", `property _dummyProp { getter() { return this.value; } } with(Window) { &.innerSunken = &_dummyProp; } return Window.innerSunken;`)
	if err != nil {
		t.Fatal(err)
	}
	budget := uint64(10000)
	got, err := vm.Execute(&program, vmTestHost{}, &budget)
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := got.Int(); n != 11 {
		t.Fatalf("startup property value = %v, want 11", got)
	}
}

func TestVMRawPropertyReferencePreservesOrdinaryAmpersand(t *testing.T) {
	got := executeVMSource(t, `return 6 & 3;`)
	if n, _ := got.Int(); n != 2 {
		t.Fatalf("bitwise result = %v, want 2", got)
	}
	program, err := compile("unary-ampersand.tjs", `return &5;`)
	if err != nil {
		t.Fatal(err)
	}
	budget := uint64(10000)
	_, err = NewVM().Execute(&program, vmTestHost{}, &budget)
	if err == nil || !strings.Contains(err.Error(), "unsupported TJS unary operator &") {
		t.Fatalf("ordinary unary ampersand error = %v", err)
	}
}

func TestVMArrayPropertiesAndSparseAssignmentLimit(t *testing.T) {
	got := executeVMSource(t, `var a=[7]; return a.count == 1 && a.length == 1 && a[0] == 7 && typeof a.count == "Integer" && typeof a.length == "Integer" && typeof a[0] == "Integer";`)
	truth, err := got.Truth()
	if err != nil {
		t.Fatal(err)
	}
	if !truth {
		t.Fatalf("array property typeof result = %v", got)
	}
	program, err := compile("array-limit.tjs", "var a=[]; a[100000]=1;")
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewVM().Execute(&program, vmTestHost{}, nil)
	if err == nil || !strings.Contains(err.Error(), "TJS array length limit exceeded") {
		t.Fatalf("sparse assignment error = %v", err)
	}
}

func TestVMArrayStrictIndexAndMemberDispatch(t *testing.T) {
	const setup = `var a=[7]; a["foo"]=9; a["01"]=10; a["1.0"]=11; a[-1]=12; a[1.5]=13; a["2"]=14; `
	assertions := []struct {
		name string
		expr string
	}{
		{"arbitrary key is member", `a["foo"] == 9`},
		{"arbitrary key has value type", `typeof a["foo"] == "Integer"`},
		{"leading-zero key is member", `a["01"] == 10`},
		{"decimal-looking key is member", `a["1.0"] == 11`},
		{"negative key is member", `a[-1] == 12`},
		{"fractional key is member", `a[1.5] == 13`},
		{"canonical sparse index is zero", `a[1] == 0`},
		{"canonical index stores value", `a[2] == 14`},
		{"length reflects array storage", `a.length == 3`},
		{"count reflects array storage", `a.count == 3`},
		{"out-of-range index is undefined", `typeof a[99] == "undefined"`},
	}
	for _, assertion := range assertions {
		t.Run(assertion.name, func(t *testing.T) {
			got := executeVMSource(t, setup+"return "+assertion.expr+";")
			truth, err := got.Truth()
			if err != nil {
				t.Fatal(err)
			}
			if !truth {
				t.Fatalf("strict array assertion %q = %v", assertion.expr, got)
			}
		})
	}
}

func TestVMArrayMethodsAndLimit(t *testing.T) {
	got := executeVMSource(t, `var a=[1,2,3]; var joined=a.join("-"); var pushed=a.push(4); var popped=a.pop(); var shifted=a.shift(); a.reverse(); var idx=a.indexOf(2); a.clear(); return joined == "1-2-3" && pushed == 4 && popped == 4 && shifted == 1 && idx == 1 && a.length == 0 && a.count == 0;`)
	truth, err := got.Truth()
	if err != nil {
		t.Fatal(err)
	}
	if !truth {
		t.Fatalf("array methods result = %v", got)
	}
	program, err := compile("array-method-limit.tjs", "var a=[]; a[99999]=1; a.push(1);")
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewVM().Execute(&program, vmTestHost{}, nil)
	if err == nil || !strings.Contains(err.Error(), "TJS array length limit exceeded") {
		t.Fatalf("array method limit error = %v", err)
	}
}

func TestVMArrayExtendedMethodsAndLengthSetter(t *testing.T) {
	got := executeVMSource(t, `var a=[1,2,1]; var strict=a.remove("1"); var add=a.add(3); var front=a.unshift(0,1); var once=a.remove(1,0); var all=a.remove(1); a.insert(2,9); a.erase(1); return strict==0 && add==3 && front==6 && once==1 && all==2 && a.join(",")=="0,9,3";`)
	truth, err := got.Truth()
	if err != nil {
		t.Fatal(err)
	}
	if !truth {
		t.Fatalf("extended array methods result = %v", got)
	}
	got = executeVMSource(t, `var a=[]; a[2]=7; a["ordinary"]=8; var n=a.push(9); a.unshift(1); return n==4 && a.length==5 && typeof a[1]=="undefined" && a["ordinary"]==8 && a[4]==9;`)
	truth, err = got.Truth()
	if err != nil {
		t.Fatal(err)
	}
	if !truth {
		t.Fatalf("sparse array methods result = %v", got)
	}
	got = executeVMSource(t, `var a=[1,2]; a.length=4; var grown=typeof a[3]=="undefined" && a.count==4; a.count=1; return grown && a.length==1 && typeof a[1]=="undefined";`)
	truth, err = got.Truth()
	if err != nil {
		t.Fatal(err)
	}
	if !truth {
		t.Fatalf("array length setter result = %v", got)
	}
}
func TestVMArrayLogicalLengthHolesAndComposition(t *testing.T) {
	got := executeVMSource(t, `var a=[]; a[9]=90; a["ordinary"]=8; a.length=10; a[9]=a[99]; var hole=typeof a[9]=="undefined"; var high=a.length==10 && a.count==10; a.length=3; var shrink=a.length==3 && typeof a[9]=="undefined" && a["ordinary"]==8; a.push(3); a.unshift(1); a.pop(); a.shift(); return hole && high && shrink && a.length==3 && a.count==3;`)
	truth, err := got.Truth()
	if err != nil {
		t.Fatal(err)
	}
	if !truth {
		t.Fatalf("sparse length delete/composition result = %v", got)
	}
}

func TestVMArrayExtendedMethodErrors(t *testing.T) {
	for _, source := range []string{
		"var a=[1]; a.erase(1);",
		"var a=[1]; a.insert(2,4);",
		"var a=[]; a.insert(0,1); a.insert(0,2); a.count=100001;",
	} {
		program, err := compile("array-extended-error.tjs", source)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = NewVM().Execute(&program, vmTestHost{}, nil); err == nil {
			t.Fatalf("array operation unexpectedly succeeded: %s", source)
		}
	}
}

func TestVMFunctionsRecursionAndArgumentForms(t *testing.T) {
	got := executeVMSource(t, "function fact(n){ if(n<=1)return 1; return n*fact(n-1); } function pick(a,b,r*){ return a+b+r[0]; } var xs=[3,4,5]; return fact(5)+pick(xs*);")
	if n, _ := got.Int(); n != 132 {
		t.Fatalf("value = %v, want 132", got)
	}
	got = executeVMSource(t, "function take(a*){return a[1];} function forward(a,b){return take(*);} return forward(6,7);")
	if n, _ := got.Int(); n != 7 {
		t.Fatalf("forward value = %v, want 7", got)
	}
}
func TestVMOwnedCallArgumentsSurviveForwardingSpreadAndClosureCapture(t *testing.T) {
	got := executeVMSource(t, `
function leaf(a,b,tail*) {
	var captured=function(){ return a+b+tail[0]; };
	return captured;
}
function forward(x,y,z) { return leaf(*); }
var values=[3,4,5];
var captured=forward(values*);
return captured();
`)
	if value, err := got.Int(); err != nil || value != 12 {
		t.Fatalf("forwarded spread/rest closure result = %v, %v; want 12", got, err)
	}
}

func TestVMOmittedCallArguments(t *testing.T) {
	got := executeVMSource(t, `
function defaults(a=2, b=3, c=4) { return a*100 + b*10 + c; }
function gather(values*) { return values; }
function forward(first, second) { return gather(*); }
class ArgumentCalls {
	var constructed = 0;
	function ArgumentCalls(a=5, b=6) { constructed = a*10 + b; }
	function member(a, b) { return (a===void ? 100 : a) + b; }
}
var instance = new ArgumentCalls(, 7);
var trailing = gather(1,);
var repeated = gather(1,,,2);
var spreadSource = [4,5];
var spread = gather(1,,spreadSource*);
var forwarded = forward(8,);
return defaults(,,9) + instance.member(,10) + instance.constructed
	+ (trailing.length===2 && trailing[0]===1 && trailing[1]===void)
	+ (repeated.length===4 && repeated[1]===void && repeated[2]===void && repeated[3]===2)
	+ (spread.length===4 && spread[1]===void && spread[2]===4 && spread[3]===5)
	+ (forwarded.length===2 && forwarded[0]===8 && forwarded[1]===void);
`)
	if n, _ := got.Int(); n != 410 {
		t.Fatalf("omitted argument result = %v, want 410", got)
	}
}

func TestVMThrownValuesAndFinally(t *testing.T) {
	got := executeVMSource(t, "var o=%[x:1]; var n=0; try { throw o; } catch(e) { n=e===o; } finally { n=n+2; } return n;")
	if n, _ := got.Int(); n != 3 {
		t.Fatalf("value = %v, want 3", got)
	}
	got = executeVMSource(t, "var n=0; try { throw 4; } catch(e) { n=e; } try { throw 'x'; } catch(e) { return n==4 && e==='x'; }")
	if n, _ := got.Int(); n != 1 {
		t.Fatalf("primitive catch value = %v, want 1", got)
	}
}

func TestVMTryLoopContinueRunsFinally(t *testing.T) {
	got := executeVMSource(t, "var i=0; var s=0; while(i<3){ i++; try { if(i==2) continue; s=s+i; } finally { s=s+10; } } return s;")
	if n, _ := got.Int(); n != 34 {
		t.Fatalf("value = %v, want 34", got)
	}
}

func TestVMRethrowAndBudgetRemainFatal(t *testing.T) {
	program, err := compile("rethrow.tjs", "try { throw 9; } catch(e) { throw e; }")
	if err != nil {
		t.Fatal(err)
	}
	budget := uint64(100)
	if _, err = NewVM().Execute(&program, vmTestHost{}, &budget); err == nil {
		t.Fatal("rethrow was swallowed")
	}
	program, err = compile("budget.tjs", "try { while(1) { } } catch(e) { return 'caught'; }")
	if err != nil {
		t.Fatal(err)
	}
	budget = 8
	_, err = NewVM().Execute(&program, vmTestHost{}, &budget)
	if err == nil || !strings.Contains(err.Error(), "execution budget exhausted") {
		t.Fatalf("budget exhaustion error = %v", err)
	}
}

func TestVMClosuresCaptureAndMutateLexicalBindings(t *testing.T) {
	got := executeVMSource(t, "function outer(){var x=1; return function(){x++; return x;};} var f=outer(); f(); return f();")
	if n, _ := got.Int(); n != 3 {
		t.Fatalf("closure value = %v, want 3", got)
	}
}
func TestVMClosureCapturesLexicalMethodContextForNameLookup(t *testing.T) {
	got := executeVMSource(t, `
class C {
	var x = 17;
	function callback() {
		var f = function() { return x; };
		return f incontextof null;
	}
}
var c = new C();
return c.callback()();
`)
	if n, _ := got.Int(); n != 17 {
		t.Fatalf("closure lookup = %v, want 17", got)
	}
}
func TestVMContextAssignmentUpdatesDeclaredGlobal(t *testing.T) {
	got := executeVMSource(t, `
var errorLog;
var context = %[];
var callback = (function() {
	errorLog = [];
	errorLog.add(42);
	return errorLog.count;
} incontextof context);
return callback();
`)
	if n, _ := got.Int(); n != 1 {
		t.Fatalf("global assigned under context = %v, want 1", got)
	}
}
func TestVMGlobalBaseMethodCallForwardsDerivedInstanceReceiver(t *testing.T) {
	got := executeVMSource(t, `
class Base {
	var value = 42;
	function read() { return this.value; }
}
global.Base = Base;
class Derived extends Base {
	function readViaGlobalBase() { return global.Base.read(); }
}
return (new Derived()).readViaGlobalBase();
`)
	if value, err := got.Int(); err != nil || value != 42 {
		t.Fatalf("global base method result = %v, %v; want 42", got, err)
	}
}

func TestVMNamedFunctionExpressionRecursion(t *testing.T) {
	got := executeVMSource(t, "return (function sum(n){if(n<=0)return 0; return n+sum(n-1);})(4);")
	if n, _ := got.Int(); n != 10 {
		t.Fatalf("recursive expression value = %v, want 10", got)
	}
}

func TestVMAnonymousFunctionsWithOptionalParameterList(t *testing.T) {
	for _, test := range []struct {
		name   string
		source string
		want   int64
	}{
		{name: "without parentheses", source: `return (function { return 42; })();`, want: 42},
		{name: "with empty parentheses", source: `return (function() { return 42; })();`, want: 42},
		{name: "incontextof", source: `var object=%[answer:42]; return (function { return this.answer; } incontextof object)();`, want: 42},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := executeVMSource(t, test.source)
			if value, err := got.Int(); err != nil || value != test.want {
				t.Fatalf("result = %v, want %d", got, test.want)
			}
		})
	}
}

func TestVMWithNameReadAndWrite(t *testing.T) {
	got := executeVMSource(t, "var o=%[value:1]; with(o){value=3; return value;}")
	if n, _ := got.Int(); n != 3 {
		t.Fatalf("with value = %v, want 3", got)
	}
}

func TestVMWithDotMemberReadAndWrite(t *testing.T) {
	got := executeVMSource(t, "var o=%[x:1]; with(o){.x=4; return .x;}")
	if n, _ := got.Int(); n != 4 {
		t.Fatalf("with-dot member value = %v, want 4", got)
	}
}

func TestVMWithDotMemberCompoundAssignmentAndCall(t *testing.T) {
	got := executeVMSource(t, "var o=%[x:1]; o.bump=function(amount){this.x+=amount;}; with(o){.x+=2; .bump(3); return .x;}")
	if n, _ := got.Int(); n != 6 {
		t.Fatalf("with-dot method call value = %v, want 6", got)
	}
}

func TestVMRejectsMalformedArgumentDescriptors(t *testing.T) {
	cases := []Program{
		{Storage: "bad-spread.tjs", Registers: 1, Code: []Instruction{{Op: Op{Code: OpCall, Out: 0, Callee: 0, Args: []Argument{{Kind: ArgumentSpread, Register: 1}}}}}},
		{Storage: "bad-forward.tjs", Registers: 1, Code: []Instruction{{Op: Op{Code: OpCall, Out: 0, Callee: 0, Args: []Argument{{Kind: ArgumentForwardFrom, From: -1}}}}}},
	}
	for _, program := range cases {
		if _, err := NewVM().Execute(&program, vmTestHost{}, nil); err == nil {
			t.Fatalf("malformed program %s was accepted", program.Storage)
		}
	}
}

func TestLegacySuperclassGetterResolvesBasesInReverseOrder(t *testing.T) {
	vm := NewVM()
	baseA, err := vm.RegisterLegacyClass("SuperclassGetterBaseA")
	if err != nil {
		t.Fatal(err)
	}
	baseB, err := vm.RegisterLegacyClass("SuperclassGetterBaseB")
	if err != nil {
		t.Fatal(err)
	}
	for _, base := range []Value{baseA, baseB} {
		id := base.ObjectRef().Object
		if err := vm.setMember(id, StringValue("inherited"), base); err != nil {
			t.Fatal(err)
		}
	}
	derived, err := vm.RegisterLegacyClass("SuperclassGetterDerived")
	if err != nil {
		t.Fatal(err)
	}
	derivedID := derived.ObjectRef().Object
	host := &vmTestHost{}
	budget := uint64(9)
	var offsets []int
	if err := vm.SetLegacyClassSuperclassGetter(derived, []int{11, 22}, func(offset int, gotHost Host, gotBudget *uint64) (Value, error) {
		if gotHost != host || gotBudget != &budget {
			t.Fatal("superclass getter did not receive the active host and budget")
		}
		offsets = append(offsets, offset)
		if offset == 22 {
			return baseB, nil
		}
		return baseA, nil
	}); err != nil {
		t.Fatal(err)
	}
	got, ok, err := vm.GetMemberIfPresent(derived, "inherited", host, &budget)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || !got.StrictEqual(baseB) {
		t.Fatalf("inherited member = %v, present=%t; want reverse-precedence base %v", got, ok, baseB)
	}
	if !reflect.DeepEqual(offsets, []int{22}) {
		t.Fatalf("superclass getter offsets = %v, want lazy reverse-first [22]", offsets)
	}
	if _, _, err := vm.GetMemberIfPresent(derived, "inherited", host, &budget); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(offsets, []int{22}) {
		t.Fatalf("cached superclass getter ran again: %v", offsets)
	}
	if gotBases := vm.meta[derivedID].superclassBases; !reflect.DeepEqual(gotBases, []ObjectID{0, baseB.ObjectRef().Object}) {
		t.Fatalf("cached superclass bases = %v, want [0 %d]", gotBases, baseB.ObjectRef().Object)
	}
}

func TestLegacySuperclassGetterWithoutGetterIsNoop(t *testing.T) {
	vm := NewVM()
	derived, err := vm.RegisterLegacyClass("SuperclassGetterWithoutGetter")
	if err != nil {
		t.Fatal(err)
	}
	derivedID := derived.ObjectRef().Object
	if _, ok, err := vm.GetMemberIfPresent(derived, "absent", vmTestHost{}, nil); err != nil || ok {
		t.Fatalf("missing superclass member = present %t, error %v; want absent without error", ok, err)
	}
	if len(vm.meta[derivedID].superclassBases) != 0 {
		t.Fatalf("missing getter linked superclass bases: %v", vm.meta[derivedID].superclassBases)
	}
}
func TestLegacySuperclassGetterRecursionAndErrorContext(t *testing.T) {
	t.Run("recursive lookup", func(t *testing.T) {
		vm := NewVM()
		derived, err := vm.RegisterLegacyClass("SuperclassGetterRecursive")
		if err != nil {
			t.Fatal(err)
		}
		if err := vm.SetLegacyClassSuperclassGetter(derived, []int{31}, func(_ int, host Host, budget *uint64) (Value, error) {
			_, _, err := vm.GetMemberIfPresent(derived, "absent", host, budget)
			return VoidValue, err
		}); err != nil {
			t.Fatal(err)
		}
		_, _, err = vm.GetMemberIfPresent(derived, "absent", vmTestHost{}, nil)
		if err == nil || !strings.Contains(err.Error(), "recursive TJS superclass getter") || !strings.Contains(err.Error(), "offset 31") {
			t.Fatalf("recursive getter error = %v, want recursion and getter-offset context", err)
		}
	})
	t.Run("getter failure context", func(t *testing.T) {
		vm := NewVM()
		derived, err := vm.RegisterLegacyClass("SuperclassGetterFailure")
		if err != nil {
			t.Fatal(err)
		}
		if err := vm.SetLegacyClassSuperclassGetter(derived, []int{47}, func(_ int, _ Host, _ *uint64) (Value, error) {
			return VoidValue, errors.New("getter failure")
		}); err != nil {
			t.Fatal(err)
		}
		_, _, err = vm.GetMemberIfPresent(derived, "absent", vmTestHost{}, nil)
		if err == nil || !strings.Contains(err.Error(), "TJS superclass getter at offset 47") || !strings.Contains(err.Error(), "getter failure") {
			t.Fatalf("getter failure = %v, want offset and underlying error context", err)
		}
	})
}

func TestLegacySuperclassGetterInitializesBaseDuringConstruction(t *testing.T) {
	vm := NewVM()
	base, err := vm.RegisterLegacyClass("SuperclassGetterConstructBase")
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.RegisterNativeMethod(base, "@initialize", "SuperclassGetterBaseInitialize"); err != nil {
		t.Fatal(err)
	}
	derived, err := vm.RegisterLegacyClass("SuperclassGetterConstructDerived")
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.SetLegacyClassSuperclassGetter(derived, []int{7}, func(_ int, _ Host, _ *uint64) (Value, error) {
		return base, nil
	}); err != nil {
		t.Fatal(err)
	}
	host := &vmSuperclassInitHost{}
	budget := uint64(20)
	if _, err := vm.Construct(derived, nil, host, &budget); err != nil {
		t.Fatal(err)
	}
	if !host.called {
		t.Fatal("base initializer was not called during construction")
	}
}
