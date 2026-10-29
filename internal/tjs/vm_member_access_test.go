package tjs

import "testing"

func TestVMMissingMemberDispatch(t *testing.T) {
	vm := NewVM()
	program, err := compile("missing-member-dispatch.tjs", `
class MissingMemberDispatchProbe {
	var stored = %[];
	function missing(setting, name, prop) {
		if (setting) {
			stored[name] = *prop;
			return true;
		}
		if (name == "virtual") {
			*prop = 37;
			return true;
		}
		return false;
	}
	function readVirtual() { return virtual; }
}
return new MissingMemberDispatchProbe();
`)
	if err != nil {
		t.Fatal(err)
	}
	budget := uint64(10000)
	proxy, err := vm.Execute(&program, vmTestHost{}, &budget)
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.SetCallMissing(proxy); err != nil {
		t.Fatal(err)
	}
	vm.Globals["proxy"] = proxy

	budget = 10000
	got, found, err := vm.GetMemberIfPresent(proxy, "virtual", vmTestHost{}, &budget)
	if err != nil || !found || got.IntegerValue() != 37 {
		t.Fatalf("missing member API read = %v, %v, %v; want 37", got, found, err)
	}
	budget = 10000
	if err := vm.SetMemberWithRuntime(proxy, "capturedByAPI", IntegerValue(23), 0, vmTestHost{}, &budget); err != nil {
		t.Fatal(err)
	}
	stored, found, err := vm.GetMemberIfPresent(proxy, "stored", vmTestHost{}, &budget)
	if err != nil || !found {
		t.Fatalf("stored member = %v, %v, %v", stored, found, err)
	}
	got, found, err = vm.GetMemberIfPresent(stored, "capturedByAPI", vmTestHost{}, &budget)
	if err != nil || !found || got.IntegerValue() != 23 {
		t.Fatalf("missing member API write = %v, %v, %v; want 23", got, found, err)
	}

	readProgram, err := compile("missing-member-read.tjs", `return proxy.virtual + "|" + proxy.readVirtual();`)
	if err != nil {
		t.Fatal(err)
	}
	budget = 10000
	budget = 10000
	got, err = vm.Execute(&readProgram, vmTestHost{}, &budget)
	if err != nil || got.Text() != "37|37" {
		t.Fatalf("missing member read = %v, %v; want 37|37", got, err)
	}

	writeProgram, err := compile("missing-member-write.tjs", `
proxy.captured = 19;
return proxy.stored.captured;
`)
	if err != nil {
		t.Fatal(err)
	}
	budget = 10000
	got, err = vm.Execute(&writeProgram, vmTestHost{}, &budget)
	if err != nil || got.IntegerValue() != 19 {
		t.Fatalf("missing member write = %v, %v; want 19", got, err)
	}
}

func TestVMMemberAccessPreservesUTF16Keys(t *testing.T) {
	vm := NewVM()
	object, err := vm.NewDictionary()
	if err != nil {
		t.Fatal(err)
	}
	id := object.ObjectRef().Object
	cases := []struct {
		name string
		key  Value
	}{
		{name: "non-ASCII", key: StringValue("café")},
		{name: "surrogate", key: StringUnits([]uint16{0xD800})},
	}
	for i, test := range cases {
		want := IntegerValue(int64(i + 1))
		if err := vm.setMember(id, test.key, want); err != nil {
			t.Fatalf("set %s: %v", test.name, err)
		}
		got, ok, err := vm.getMember(id, test.key)
		if err != nil || !ok || got.IntegerValue() != want.IntegerValue() {
			t.Fatalf("get %s = (%v, %v, %v), want %v", test.name, got, ok, err, want)
		}
	}
	if _, ok, err := vm.getMember(id, StringValue("absent")); err != nil || ok {
		t.Fatalf("ordinary miss = (%v, %v), want absent", ok, err)
	}
}

func TestVMStringNameMemberAccessMatchesUTF16(t *testing.T) {
	vm := NewVM()
	object, err := vm.NewDictionary()
	if err != nil {
		t.Fatal(err)
	}
	id := object.ObjectRef().Object
	stored, err := vm.obj(id)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"plain", "日本語", "emoji-🙂", string([]byte{0xff, 'x', 0xc0, 0xaf})} {
		units := UTF16FromString(name)
		if got, want := memberKeyString(name), memberKey(units); got != want {
			t.Fatalf("member key for %q = %x, want %x", name, got, want)
		}
		if err := vm.setMemberString(id, name, IntegerValue(7)); err != nil {
			t.Fatalf("setMemberString(%q): %v", name, err)
		}
		if got, ok := stored.GetUnits(units); !ok || got.IntegerValue() != 7 {
			t.Fatalf("GetUnits(%q) = (%v, %v), want 7", name, got, ok)
		}
		if got, ok, err := vm.getMemberString(id, name); err != nil || !ok || got.IntegerValue() != 7 {
			t.Fatalf("getMemberString(%q) = (%v, %v, %v), want 7", name, got, ok, err)
		}
	}
	if _, ok, err := vm.getMemberString(id, "absent"); err != nil || ok {
		t.Fatalf("ordinary name miss = (%v, %v), want absent", ok, err)
	}
}

func TestVMMemberAccessGlobalClassPropertyAndMutation(t *testing.T) {
	t.Run("class-method-uses-instance-fields", func(t *testing.T) {
		got := executeVMSource(t, `
class Matrix {
		var m11 = 1;
		var m14 = 0;
		function scale(v) { m11 = v; m14 = 7; }
		function transform(x) { return m11 * x + m14; }
}
var value = new Matrix();
value.scale(0.5);
return value.transform(10);
`)
		if n, err := got.Real(); err != nil || n != 12 {
			t.Fatalf("class method instance fields = %v, %v; want 12", got, err)
		}
	})
	t.Run("affine-multiply-keeps-homogeneous-denominator", func(t *testing.T) {
		got := executeVMSource(t, `
class Matrix {
		var m11; var m12; var m13; var m14;
		var m21; var m22; var m23; var m24;
		var m31; var m32; var m33; var m34;
		var m41; var m42; var m43; var m44;
		function Matrix() {
			m11=1; m12=0; m13=0; m14=0;
			m21=0; m22=1; m23=0; m24=0;
			m31=0; m32=0; m33=1; m34=0;
			m41=0; m42=0; m43=0; m44=1;
		}
		function translate(x,y) { m14 += m11*x + m12*y; m24 += m21*x + m22*y; }
		function scale(sx,sy) { m11*=sx; m21*=sx; m12*=sy; m22*=sy; }
		function multiply(m) {
			var x11=m11*m.m11+m12*m.m21+m14*m.m41;
			var x21=m21*m.m11+m22*m.m21+m24*m.m41;
			var x31=m31*m.m11+m32*m.m21+m33*m.m31+m34*m.m41;
			var x41=m41*m.m11+m42*m.m21+m44*m.m41;
			var x12=m11*m.m12+m12*m.m22+m14*m.m42;
			var x22=m21*m.m12+m22*m.m22+m24*m.m42;
			var x32=m31*m.m12+m32*m.m22+m33*m.m32+m34*m.m42;
			var x42=m41*m.m12+m42*m.m22+m44*m.m42;
			var x13=m11*m.m13+m12*m.m23+m13*m.m33+m14*m.m43;
			var x23=m21*m.m13+m22*m.m23+m23*m.m33+m24*m.m43;
			var x33=m31*m.m13+m32*m.m23+m33*m.m33+m34*m.m43;
			var x43=m41*m.m13+m42*m.m23+m43*m.m33+m44*m.m43;
			var x14=m11*m.m14+m12*m.m24+m14*m.m44;
			var x24=m21*m.m14+m22*m.m24+m24*m.m44;
			var x34=m31*m.m14+m32*m.m24+m34*m.m44;
			var x44=m41*m.m14+m42*m.m24+m44*m.m44;
			m11=x11, m21=x21; m31=x31; m41=x41;
			m12=x12, m22=x22; m32=x32; m42=x42;
			m13=x13, m23=x23; m33=x33; m43=x43;
			m14=x14, m24=x24; m34=x34; m44=x44;
		}
}
var outer=new Matrix(); outer.translate(135,-41.5);
var inner=new Matrix(); inner.scale(0.5,0.5);
outer.multiply(inner);
return outer.m11 == 0.5 && outer.m22 == 0.5 && outer.m14 == 135 && outer.m24 == -41.5 && outer.m44 == 1;
`)
		truth, err := got.Truth()
		if err != nil || !truth {
			t.Fatalf("affine multiply result = %v; want true", got)
		}
	})

	t.Run("global-fallback", func(t *testing.T) {
		vm := NewVM()
		vm.Globals["fromGlobals"] = IntegerValue(17)
		got, ok, err := vm.getMember(0, StringValue("fromGlobals"))
		if err != nil || !ok || got.IntegerValue() != 17 {
			t.Fatalf("global fallback = (%v, %v, %v), want 17", got, ok, err)
		}
	})

	t.Run("class-base-fallback", func(t *testing.T) {
		got := executeVMSource(t, `
class Base { function inherited() { return 23; } }
class Child extends Base {}
var value = new Child();
return value.inherited();
`)
		if got.IntegerValue() != 23 {
			t.Fatalf("inherited member result = %v, want 23", got)
		}
	})

	t.Run("property-side-effects", func(t *testing.T) {
		got := executeVMSource(t, `
var backing = 4;
property value { getter() { return backing; } setter(v) { backing = v; } }
value = value + 5;
return value * 10 + backing;
`)
		if got.IntegerValue() != 99 {
			t.Fatalf("getter/setter result = %v, want 99", got)
		}
	})

	t.Run("dynamic-add-delete", func(t *testing.T) {
		vm := NewVM()
		object, err := vm.NewDictionary()
		if err != nil {
			t.Fatal(err)
		}
		id := object.ObjectRef().Object
		key := StringValue("dynamic")
		if err := vm.setMember(id, key, IntegerValue(31)); err != nil {
			t.Fatal(err)
		}
		if got, ok, err := vm.getMember(id, key); err != nil || !ok || got.IntegerValue() != 31 {
			t.Fatalf("added member = (%v, %v, %v), want 31", got, ok, err)
		}
		deleted, err := vm.deleteMember(id, key)
		if err != nil || !deleted {
			t.Fatalf("delete member = (%v, %v), want deleted", deleted, err)
		}
		if _, ok, err := vm.getMember(id, key); err != nil || ok {
			t.Fatalf("deleted member lookup = (%v, %v), want absent", ok, err)
		}
	})
}
func TestVMClassStaticMembersAccessibleThroughBoundFunctionThis(t *testing.T) {
	got := executeVMSource(t, `
class C { function C() {} }
with (C) {
	.cursorMoveTimeByFocus = .defaultCursorMove = 300;
	.setCursorMoveTime = function () { return this.defaultCursorMove; } incontextof C;
	.instance = new C();
}
return C.setCursorMoveTime();
`)
	if got.IntegerValue() != 300 {
		t.Fatalf("class-bound function this.defaultCursorMove = %v, want 300", got)
	}
}

func TestVMLegacyPropertyDispatchesCompiledAccessors(t *testing.T) {
	vm := NewVM()
	receiver, err := vm.NewDictionary()
	if err != nil {
		t.Fatal(err)
	}
	other, err := vm.NewDictionary()
	if err != nil {
		t.Fatal(err)
	}
	host := nopHost{}
	var budget uint64 = 4096
	var (
		getterContext, setterContext Value
		getterHost                   Host
		getterBudget                 *uint64
		getterCalls, setterCalls     int
		setterArgs                   []Value
	)
	getter, err := vm.RegisterLegacyFunction("value.get", func(context Value, args []Value, h Host, b *uint64, resultNeeded bool) (Value, error) {
		getterCalls++
		if len(args) != 0 || !resultNeeded {
			t.Errorf("legacy getter args = %v, resultNeeded = %t; want no arguments and a needed result", args, resultNeeded)
		}
		getterContext, getterHost, getterBudget = context, h, b
		return IntegerValue(11), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	setter, err := vm.RegisterLegacyFunction("value.set", func(context Value, args []Value, _ Host, _ *uint64, _ bool) (Value, error) {
		setterCalls++
		setterContext, setterArgs = context, args
		return VoidValue, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	property, err := vm.RegisterLegacyProperty("value", getter, setter)
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.SetMemberWithRuntime(receiver, "value", property, MemberEnsure, host, &budget); err != nil {
		t.Fatal(err)
	}
	if getterCalls != 0 {
		t.Fatalf("legacy getter ran %d times while binding the member; want lazy dispatch", getterCalls)
	}
	got, err := vm.GetMember(receiver, "value", host, &budget)
	if err != nil || got.IntegerValue() != 11 {
		t.Fatalf("legacy property read = %v, %v; want 11", got, err)
	}
	if getterContext.ObjectRef() != receiver.ObjectRef() {
		t.Fatalf("legacy getter this-context = %v; want the reading receiver %v", getterContext, receiver)
	}
	if getterHost != host || getterBudget != &budget {
		t.Fatalf("legacy getter host/budget = %v, %v; want the caller's host and budget", getterHost, getterBudget)
	}
	if err := vm.SetMemberWithRuntime(receiver, "value", IntegerValue(5), 0, host, &budget); err != nil {
		t.Fatal(err)
	}
	if setterCalls != 1 || len(setterArgs) != 1 || setterArgs[0].IntegerValue() != 5 {
		t.Fatalf("legacy setter calls/args = %d, %v; want one call carrying the assigned value", setterCalls, setterArgs)
	}
	if setterContext.ObjectRef() != receiver.ObjectRef() {
		t.Fatalf("legacy setter this-context = %v; want the written receiver %v", setterContext, receiver)
	}
	if got, err := vm.GetMember(receiver, "value", host, &budget); err != nil || got.IntegerValue() != 11 {
		t.Fatalf("legacy property read after write = %v, %v; want the getter result 11", got, err)
	}
	// A property member keeps the owner it was bound to as its this-context,
	// which is what compiled top-level declarations express with CHGTHIS.
	bound := ObjectValue(NewObjectRef(property.ObjectRef().Object, receiver.ObjectRef().Object))
	if err := vm.SetMemberWithRuntime(other, "value", bound, MemberEnsure, host, &budget); err != nil {
		t.Fatal(err)
	}
	if _, err := vm.GetMember(other, "value", host, &budget); err != nil {
		t.Fatal(err)
	}
	if getterContext.ObjectRef() != receiver.ObjectRef() {
		t.Fatalf("bound legacy getter this-context = %v; want the bound owner %v", getterContext, receiver)
	}
}
