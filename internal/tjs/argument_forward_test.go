package tjs

import "testing"

func TestVMUnnamedArgumentsForwardOnlyExtraArguments(t *testing.T) {
	got := executeVMSource(t, `
var target = %[];
function callback(tag, value, map) { map[tag] = value; }
function foreach(cb, *) { cb("answer", 42, *); }
foreach(callback, target);
var config = %[];
foreach(function(tag, value, map) { map[tag] = value; } incontextof global, config.answerMap = %[]);
return target.answer + config.answerMap.answer;
`)
	if value, err := got.Int(); err != nil || value != 84 {
		t.Fatalf("callback forwarded values = %v, %v; want 84", got, err)
	}
}
func TestVMExplicitForwardIncludesNamedArguments(t *testing.T) {
	got := executeVMSource(t, `
function collect(values*) { return values; }
function forward(first, second, *) { return collect(...); }
var args = forward("named", "also named", "unnamed");
return args.length === 3 && args[0] === "named" &&
	args[1] === "also named" && args[2] === "unnamed";
`)
	if value, err := got.Truth(); err != nil || !value {
		t.Fatalf("explicit argument forwarding = %v, %v; want all named and unnamed arguments", got, err)
	}
}

func TestVMSuperMethodForwardsArgumentsWithReceiverContext(t *testing.T) {
	got := executeVMSource(t, `
class Base {
	function addSlider(elm) { return elm.x + this.offset; }
}
class Custom extends Base {
	function addSlider(elm) { return super.addSlider(...); }
	var offset = 7;
}
var descriptor = %[];
descriptor.x = 35;
var custom = new Custom();
return custom.addSlider(descriptor);
`)
	if value, err := got.Int(); err != nil || value != 42 {
		t.Fatalf("super method forwarded descriptor = %v, %v; want 42", got, err)
	}
}
func TestVMNestedSuperclassForwardingPreservesDictionaryArgument(t *testing.T) {
	got := executeVMSource(t, `
class Layer {
	function addSlider(elm) { return elm.x; }
}
class Dialog extends Layer {
	function addSlider(elm) { return super.addSlider(...); }
}
class QuickMenu extends Dialog {
	function addSlider(elm) { return super.addSlider(...); }
}
class CustomQuickMenu extends QuickMenu {
	function addSlider(elm) {
		super.addSlider(...);
		return elm.x;
	}
}
var descriptor = %[];
descriptor.x = 1198;
var menu = new CustomQuickMenu();
return menu.addSlider(descriptor);
`)
	if value, err := got.Int(); err != nil || value != 1198 {
		t.Fatalf("nested super forwarding descriptor = %v, %v; want 1198", got, err)
	}
}
func TestVMWithScopeDoesNotShadowLocalArgument(t *testing.T) {
	got := executeVMSource(t, `
function forward(value) {
	with (value) {
		return value;
	}
}
var descriptor = %[value:"member"];
return forward(descriptor) === descriptor;
`)
	truth, err := got.Truth()
	if err != nil || !truth {
		t.Fatalf("with-scope local argument result = %v, %v; want true", got, err)
	}
}
