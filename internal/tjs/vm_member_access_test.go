package tjs

import "testing"

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
