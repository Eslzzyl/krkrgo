package tjs

import (
	"errors"
	"strings"
	"testing"
)

func executeDeleteSource(t *testing.T, source string) Value {
	t.Helper()
	program, err := compile("delete.tjs", source)
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

func TestVMDeleteNamesAndMembers(t *testing.T) {
	got := executeDeleteSource(t, `var name=1; var first=delete name; var second=delete name; var o=%[x:1]; var dynamic=delete o["x"]; var dotted=delete o.x; return first && !second && dynamic && !dotted;`)
	truth, err := got.Truth()
	if err != nil || !truth {
		t.Fatalf("delete names and members = %v, %v", got, err)
	}
}

func TestVMDeleteEvaluatesMemberReceiverAndKeyOnce(t *testing.T) {
	got := executeDeleteSource(t, `var receiver=0; var keyCalls=0; function makeObject(){ receiver=receiver+1; return %[x:1]; } function key(){ keyCalls=keyCalls+1; return "x"; } var removed=delete makeObject()[key()]; return removed && receiver==1 && keyCalls==1;`)
	truth, err := got.Truth()
	if err != nil || !truth {
		t.Fatalf("delete evaluation order = %v, %v", got, err)
	}
}

func TestVMDeleteArrayLeavesHoleAndLength(t *testing.T) {
	got := executeDeleteSource(t, `var a=[]; a[2]=9; var removed=delete a[2]; var repeated=delete a[2]; return removed && !repeated && a.length==3 && a.count==3 && typeof a[2]=="undefined";`)
	truth, err := got.Truth()
	if err != nil || !truth {
		t.Fatalf("delete array hole = %v, %v", got, err)
	}
}

func TestVMDeleteRespectsMemberFlags(t *testing.T) {
	vm := NewVM()
	global, ok := vm.Objects.Lookup(0)
	if !ok {
		t.Fatal("global object missing")
	}
	if err := DefineReadOnly(global, "fixed", IntegerValue(1)); err != nil {
		t.Fatal(err)
	}
	program, err := compile("delete-readonly.tjs", "return delete fixed;")
	if err != nil {
		t.Fatal(err)
	}
	budget := uint64(1000)
	got, err := vm.Execute(&program, vmTestHost{}, &budget)
	if err != nil {
		t.Fatal(err)
	}
	if truth, err := got.Truth(); err != nil || truth {
		t.Fatalf("delete read-only = %v, %v", got, err)
	}
	if !global.IsMemberValid("fixed") {
		t.Fatal("read-only member was deleted")
	}
}

func TestVMDeleteRejectsNonObjectMemberReceiver(t *testing.T) {
	program, err := compile("delete-invalid-receiver.tjs", `return delete 1["x"];`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewVM().Execute(&program, vmTestHost{}, nil)
	if err == nil || !strings.HasSuffix(err.Error(), "TJS value is not a non-null object") || !errors.Is(err, errTJSNonNullObject) {
		t.Fatalf("invalid delete receiver error = %v, want source-wrapped handle error", err)
	}
}

func TestCompileDeleteRejectsNonReference(t *testing.T) {
	if _, err := compile_expression("delete-invalid-target.tjs", "delete (1+2)"); err == nil || !strings.Contains(err.Error(), "delete requires a member or variable") {
		t.Fatalf("invalid delete target error = %v", err)
	}
}
