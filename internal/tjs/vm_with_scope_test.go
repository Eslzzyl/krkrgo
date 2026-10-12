package tjs

import "testing"

func TestVMWithObjectDoesNotShadowLexicalLocals(t *testing.T) {
	got := executeVMSource(t, `
function update() {
	var name = "local";
	var target = %[name:"member"];
	var observed;
	with (target) {
		observed = name;
		name = "updated";
	}
	return [observed, name, target.name].join("|");
}
return update();
`)
	if want := "local|updated|member"; got.Text() != want {
		t.Fatalf("with/local scope resolution = %q, want %q", got.Text(), want)
	}
}
