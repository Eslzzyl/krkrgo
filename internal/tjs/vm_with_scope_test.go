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

func TestVMBlockLocalShadowsParameter(t *testing.T) {
	got := executeVMSource(t, `
function collect(name, list) {
	var out = [];
	for (var i=list.count \ 2 * 2 - 2;i>=0;i-=2) {
		var name  = list[i];
		var value = list[i+1];
		out.add(name + "=" + value);
	}
	return out.join(",");
}
return collect("outer", ["level", 0, "file", "title_bg"]);
`)
	if want := "file=title_bg,level=0"; got.Text() != want {
		t.Fatalf("block local/parameter shadowing = %q, want %q", got.Text(), want)
	}
}

func TestVMBlockLocalIsScopedToItsBlock(t *testing.T) {
	got := executeVMSource(t, `
function probe(name) {
	var observed;
	{
		var name = "inner";
		observed = name;
	}
	return observed + "|" + name;
}
return probe("outer");
`)
	if want := "inner|outer"; got.Text() != want {
		t.Fatalf("block local scope = %q, want %q", got.Text(), want)
	}
}

func TestVMWithScopeReadsMember(t *testing.T) {
	got := executeVMSource(t, `
var value = %[answer:42];
with (value) {
	return answer;
}
`)
	if n, err := got.Int(); err != nil || n != 42 {
		t.Fatalf("with-scope member = %v, %v; want 42", got, err)
	}
}

func TestVMWithScopeAssignsMemberAndBindsFunction(t *testing.T) {
	got := executeVMSource(t, `
var value = %[];
with (value) {
	.x = 42;
	.read = function() { return this.x; } incontextof value;
}
return value.read();
`)
	if n, err := got.Int(); err != nil || n != 42 {
		t.Fatalf("with-scope assignment = %v, %v; want 42", got, err)
	}
}
