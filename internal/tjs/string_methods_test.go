package tjs

import "testing"

func TestVMStringPrimitiveMethods(t *testing.T) {
	got := executeVMSource(t, `
var s = "A😀Baba";
var split = "a,,b,".split(",");
var replaced = "abbb".replace(new RegExp("b", "g"), "x");
return s.length == 7 &&
    s.indexOf("B") == 3 && s.indexOf("B", 4) == -1 &&
    s.lastIndexOf("a") == 6 &&
    s.charAt(3) == "B" && s.substr(1, 2) == "😀" &&
    s.substring(3, 4) == "B" && s.substring(4, 7) == "aba" &&
    "hello".toUpperCase() == "HELLO" && "HELLO".toLowerCase() == "hello" &&
    " \t alpha \n".trim() == "alpha" && split.join("|") == "a||b|" &&
    "abc".replace("b", "x") == "axc" && replaced == "axxx";
`)
	truth, err := got.Truth()
	if err != nil {
		t.Fatal(err)
	}
	if !truth {
		t.Fatalf("String methods returned %v", got)
	}
}

func TestVMStringIndexOfCoercesVoidArgument(t *testing.T) {
	got := executeVMSource(t, `return "abc".indexOf(void) == -1 && "void".indexOf(void) == 0;`)
	truth, err := got.Truth()
	if err != nil {
		t.Fatal(err)
	}
	if !truth {
		t.Fatalf("String.indexOf(void) coercion returned %v", got)
	}
}

func TestVMStringIndexedPropertiesUseUTF16Units(t *testing.T) {
	got := executeVMSource(t, `
var s = "A😀B", empty = "";
return [
    s[0] == "A",
    s[1] === s.charAt(1),
    s[2] === s.charAt(2),
    s[3] == "B",
    s[4] === void,
    typeof s[4] == "void",
    typeof empty[0] == "void"
].join("|");
`)
	if got.Text() != "1|1|1|1|1|1|1" {
		t.Fatalf("indexed String properties returned %q", got.Text())
	}
	truth, err := got.Truth()
	if err != nil {
		t.Fatal(err)
	}
	if !truth {
		t.Fatalf("indexed String properties returned %v", got)
	}

	indexed, err := NewVM().GetMember(StringValue("abc"), "0", vmTestHost{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if indexed.Text() != "a" {
		t.Fatalf("public String indexed member = %q, want %q", indexed.Text(), "a")
	}
}

func TestVMStringMethodParenthesesAndIncontextof(t *testing.T) {
	got := executeVMSource(t, `
var find = "unused".indexOf incontextof "abcabc";
return ("a:b").indexOf(":") == 1 && find("bc") == 1;
`)
	truth, err := got.Truth()
	if err != nil {
		t.Fatal(err)
	}
	if !truth {
		t.Fatalf("parenthesized or context-bound String call returned %v", got)
	}
}

func TestVMStringMethodCacheAndPublicMemberAccess(t *testing.T) {
	vm := NewVM()
	first, err := vm.GetMember(StringValue("one"), "indexOf", vmTestHost{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := vm.GetMember(StringValue("two"), "indexOf", vmTestHost{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.ObjectRef().Object != second.ObjectRef().Object {
		t.Fatal("String.indexOf method was not cached")
	}
	length, err := vm.GetMember(StringValue("😀"), "length", vmTestHost{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if length.IntegerValue() != 2 {
		t.Fatalf("UTF-16 string length = %v, want 2", length)
	}
	got, err := vm.CallFunction(first, StringValue("a:b"), []Value{StringValue(":")}, vmTestHost{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.IntegerValue() != 1 {
		t.Fatalf("String.indexOf call = %v, want 1", got)
	}
}

func TestDispatchStringSearchUsesUTF16Indices(t *testing.T) {
	d := NewDispatcher(NewObjectTable())
	for _, test := range []struct {
		name   string
		needle string
		want   int64
	}{
		{name: "indexOf", needle: "B", want: 3},
		{name: "lastIndexOf", needle: "😀", want: 4},
	} {
		got, err := d.CallMember(StringValue("A😀B😀"), test.name, StringValue(test.needle))
		if err != nil {
			t.Fatal(err)
		}
		if got.IntegerValue() != test.want {
			t.Errorf("%s(%q) = %v, want %d", test.name, test.needle, got, test.want)
		}
	}
}
