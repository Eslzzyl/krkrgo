package runtime

import (
	"testing"

	"github.com/eslzzyl/krkrgo/internal/tjs"
)

func TestMenuPluginInstallsHeadlessMenuSurface(t *testing.T) {
	services := DefaultServices()
	session := NewSession(services, 100_000)
	defer session.Close()

	if _, err := session.Evaluate("MenuItem"); err == nil {
		t.Fatal("MenuItem was available before menu.dll was linked")
	}
	if _, err := session.Evaluate(`Plugins.link("Plugin/Menu.DLL")`); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Evaluate(`Plugins.link("plugin/menu.dll")`); err != nil {
		t.Fatal(err)
	}
	if len(services.Plugins) != 1 {
		t.Fatalf("case-insensitive menu link list = %v", services.Plugins)
	}
	if _, err := session.executeSource("<menu-test>", `Plugins.link("plugin/KAGParser.dll"); parser = new KAGParser()`, false); err != nil {
		t.Fatalf("KAGParser.dll registration path: %v", err)
	}

	for _, source := range []string{
		`window = new Window()`,
		`clicks = 0; clickType = ""; window.action = function(event) { clicks = clicks + 1; clickType = event.type; clickedTarget = event.target; }`,
		`root = window.menu`,
		`first = new MenuItem(window, "first")`,
		`second = new MenuItem(window, "second")`,
		`root.add(first)`,
		`root.insert(second, 0)`,
		`first.group = 7; first.radio = true; first.checked = true`,
		`second.group = 7; second.radio = true; second.checked = true`,
		`first.caption = "renamed"; first.enabled = true; first.visible = true; first.shortcut = "Ctrl+Shift+F4"`,
		`overrideCalled = false`,
		`class CallbackMenuItem extends MenuItem {
			function CallbackMenuItem(owner, caption) { super.MenuItem(owner, caption); }
			function onClick() { overrideCalled = true; super.onClick(...); }
		}`,
		`callbackItem = new CallbackMenuItem(window, "callback"); root.add(callbackItem)`,
	} {
		if _, err := session.executeSource("<menu-test>", source, false); err != nil {
			t.Fatalf("%s: %v", source, err)
		}
	}

	evalInt := func(source string) int64 {
		t.Helper()
		value, err := session.Evaluate(source)
		if err != nil {
			t.Fatalf("%s: %v", source, err)
		}
		result, err := value.Int()
		if err != nil {
			t.Fatalf("%s: %v", source, err)
		}
		return result
	}
	evalTruth := func(source string) bool {
		t.Helper()
		value, err := session.Evaluate(source)
		if err != nil {
			t.Fatalf("%s: %v", source, err)
		}
		result, err := value.Truth()
		if err != nil {
			t.Fatalf("%s: %v", source, err)
		}
		return result
	}
	if !evalTruth(`root === window.menu && root.window === window && first.root === root && first.window === null && first.parent === root && second.parent === root && first.index == 1 && second.index == 0`) {
		t.Fatal("menu root or child tree relationships are incorrect")
	}
	if !evalTruth(`root.children.count == 3 && root.children === root.children`) {
		t.Fatal("menu children array was not stable or complete")
	}
	if !evalTruth(`first.caption == "renamed" && first.enabled && first.visible && first.shortcut == "Shift+Ctrl+F4" && first.group == 7 && first.radio && !first.checked && second.checked`) {
		t.Fatal("MenuItem writable properties or radio grouping are incorrect")
	}
	if !evalTruth(`MenuItem.textToKeycode.f4 == 115 && MenuItem.keycodeToText[115] == "F4"`) {
		t.Fatal("MenuItem shortcut lookup tables are not available")
	}
	if _, err := session.Evaluate(`first.popup(10, 20)`); err != nil {
		t.Fatal(err)
	}
	popup, ok := services.LastMenuPopup()
	if !ok || popup.Item != evalObjectID(t, session, "first") || len(popup.Arguments) != 2 || popup.Arguments[0].IntegerValue() != 10 || popup.Arguments[1].IntegerValue() != 20 {
		t.Fatalf("headless popup request = %#v, %v", popup, ok)
	}
	if _, err := session.Evaluate(`first.fireClick()`); err != nil {
		t.Fatal(err)
	}
	if !evalTruth(`clicks == 1 && clickType == "onClick" && clickedTarget === first`) {
		t.Fatal("MenuItem fireClick did not execute the owner action callback")
	}
	if _, err := session.Evaluate(`callbackItem.fireClick()`); err != nil {
		t.Fatal(err)
	}
	if !evalTruth(`overrideCalled && clicks == 2 && clickType == "onClick" && clickedTarget === callbackItem`) {
		t.Fatal("fireClick did not dispatch the TJS MenuItem.onClick override")
	}
	if _, err := session.executeSource("<menu-test>", `root.enabled = false; first.fireClick(); root.enabled = true`, false); err != nil {
		t.Fatal(err)
	}
	if evalInt("clicks") != 2 {
		t.Fatal("fireClick ignored a disabled ancestor")
	}
	if _, err := session.Evaluate(`root.remove(second)`); err != nil {
		t.Fatal(err)
	}
	if !evalTruth(`second.parent === null && second.index == -1 && root.children.count == 2`) {
		t.Fatal("MenuItem.remove did not detach the child")
	}
	if err := session.GC(); err != nil {
		t.Fatal(err)
	}
	if !evalTruth(`window.menu === root && first.caption == "renamed" && root.children.count == 2`) {
		t.Fatal("GC did not preserve the window menu tree")
	}
}

func evalObjectID(t *testing.T, session *Session, source string) tjs.ObjectID {
	t.Helper()
	value, err := session.Evaluate(source)
	if err != nil {
		t.Fatalf("%s: %v", source, err)
	}
	if value.Kind() != tjs.KindObject || value.IsNull() {
		t.Fatalf("%s returned non-object %v", source, value)
	}
	return value.ObjectRef().Object
}
