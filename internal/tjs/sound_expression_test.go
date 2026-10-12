package tjs

import (
	"strings"
	"testing"
)

func TestSoundExpressionStorageRemainsStringThroughMenuDictionary(t *testing.T) {
	const source = `
function getSysSE() {
	return %[storage: "  menu-click.ogg  ".trim(), buf: 9];
}
function createSoundExpression(exp, storage, buf) {
	if (typeof exp === "String") {
		return "(kag.se[" + buf + "].play(%[storage:\"" + storage.escape() + "\"]))";
	}
	return "other";
}
function createMenuFromList() {
	var item = %[se: "menu.save"];
	with (item) {
		var se = getSysSE(.se);
		var exp = %[exp: "click", clickse: se.storage, clicksebuf: se.buf];
		return createSoundExpression(exp.exp, exp.clickse, exp.clicksebuf);
	}
}
return createMenuFromList();
`
	got := executeVMSource(t, source)
	if got.Kind() != KindString || got.Text() != `(kag.se[9].play(%[storage:"menu-click.ogg"]))` {
		t.Fatalf("menu sound expression = %#v, want escaped click storage expression", got)
	}
}

func TestUnknownStringMemberIsNotFabricatedAsMethod(t *testing.T) {
	program, err := compile("string-unknown-member.tjs", `return "sound-alias".storage;`)
	if err != nil {
		t.Fatal(err)
	}
	budget := uint64(1000)
	_, err = NewVM().Execute(&program, vmTestHost{}, &budget)
	if err == nil || !strings.Contains(err.Error(), "member not found: storage") {
		t.Fatalf("unknown String.storage error = %v, want missing member", err)
	}
}
func TestUninitializedVarInWithShadowsMemberWithoutOverwritingIt(t *testing.T) {
	// TJS2 declares a local for "var se;" and clears it, so the unqualified name
	// resolves to that local while the with-object member stays reachable through
	// the explicit ".se" spelling.
	got := executeVMSource(t, `
function read(item) {
	with (item) {
		var se;
		return .se + ":" + typeof se;
	}
}
return read(%[se: "menu.save"]);
`)
	if got.Kind() != KindString || got.Text() != "menu.save:void" {
		t.Fatalf("uninitialized var result = %#v, want local shadow over the with member", got)
	}
}
