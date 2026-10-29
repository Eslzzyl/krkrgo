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
    "abc".replace("b", "x") == "axc" && replaced == "axxx" &&
    " clipleft=0 cliptop=0 ".split(" ",,true).join(",") == "clipleft=0,cliptop=0";
`)
	truth, err := got.Truth()
	if err != nil {
		t.Fatal(err)
	}
	if !truth {
		t.Fatalf("String methods returned %v", got)
	}
}

func TestVMStringSplitPurgesEmptyFields(t *testing.T) {
	got := executeVMSource(t, `return " clipleft=0 cliptop=0 ".split(" ",,true).join(",") + "#" + "a;b,c".split(",;").join("|");`)
	if got.Text() != "clipleft=0,cliptop=0#a|b|c" {
		t.Fatalf("string split = %q", got.Text())
	}
}

func TestVMStringSplitRegexp(t *testing.T) {
	got := executeVMSource(t, `
var fields = "dress\t仕事服\tdiff\t1\tウエイトレス".split(/\t/);
return fields.count + "|" + fields[0] + "|" + fields[1] + "|" + fields[2] + "|" + fields[3] + "|" + fields[4];`)
	if got.Text() != "5|dress|仕事服|diff|1|ウエイトレス" {
		t.Fatalf("regexp string split = %q", got.Text())
	}
}

func TestDispatchStringSplitRegexp(t *testing.T) {
	table := NewObjectTable()
	d := NewDispatcher(table)
	pattern, err := NewRegExp(table, `\t`, "")
	if err != nil {
		t.Fatal(err)
	}
	result, err := d.CallMember(StringValue("a\tb\t"), "split", ObjectValue(pattern.Ref()))
	if err != nil {
		t.Fatal(err)
	}
	array, ok := table.Lookup(result.ObjectRef().Object)
	if !ok || array.Category != CategoryArray {
		t.Fatalf("regexp string split result = %v, want array", result)
	}
	joined, err := d.CallMember(result, "join", StringValue("|"))
	if err != nil || joined.Text() != "a|b|" {
		t.Fatalf("dispatcher regexp string split = %q, %v", joined.Text(), err)
	}
}

func TestVMStringEscapeUsesTJSStringLiteralEscapes(t *testing.T) {
	got := executeVMSource(t, `
var escaped = "quote: \" ' slash: \\ controls:\x07\b\f\n\r\t\v \x01 \x1b9".escape();
return escaped;
`)
	if got.Text() != `quote: \" \' slash: \\ controls:\a\b\f\n\r\t\v \x01 \x1b\x39` {
		t.Fatalf("String.escape = %q", got.Text())
	}
	astral := executeVMSource(t, `return "😀".escape();`)
	if astral.Text() != "😀" {
		t.Fatalf("String.escape astral = %q", astral.Text())
	}
	surrogate := executeVMSource(t, `return "\uD800".escape();`)
	if surrogate.Text() != "\\uD800" {
		t.Fatalf("String.escape isolated surrogate = %q", surrogate.Text())
	}
	dispatcher := NewDispatcher(NewObjectTable())
	raw := StringUnits([]uint16{'a', '\n', 0xd800})
	escaped, err := dispatcher.CallMember(raw, "escape")
	if err != nil {
		t.Fatal(err)
	}
	if escaped.Text() != `a\n\uD800` {
		t.Fatalf("dispatcher String.escape = %q", escaped.Text())
	}
}

func TestVMStringRepeatUsesUTF16AndCount(t *testing.T) {
	got := executeVMSource(t, `
return "=".repeat(4) == "====" &&
    "😀".repeat(2).length == 4 &&
    "".repeat(10) == "" &&
    "x".repeat(0) == "";
`)
	truth, err := got.Truth()
	if err != nil {
		t.Fatal(err)
	}
	if !truth {
		t.Fatalf("String.repeat returned %v", got)
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

func TestVMStringCountMatchesLength(t *testing.T) {
	vm := NewVM()
	for _, test := range []struct {
		text string
		want int64
	}{
		{text: "abc", want: 3},
		{text: "", want: 0},
		{text: "😀", want: 2},
		{text: "A😀B", want: 4},
	} {
		length, err := vm.GetMember(StringValue(test.text), "length", vmTestHost{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		count, err := vm.GetMember(StringValue(test.text), "count", vmTestHost{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if length.IntegerValue() != test.want {
			t.Errorf("%q length = %v, want %d", test.text, length, test.want)
		}
		if count.IntegerValue() != test.want {
			t.Errorf("%q count = %v, want %d", test.text, count, test.want)
		}
	}
}

func TestVMStringCountSource(t *testing.T) {
	got := executeVMSource(t, `
return "abc".count == 3 && "😀".count == 2 && "A😀B".count == "A😀B".length;
`)
	truth, err := got.Truth()
	if err != nil {
		t.Fatal(err)
	}
	if !truth {
		t.Fatalf("String.count in source returned %v", got)
	}
}

func TestDispatchStringCountMatchesLength(t *testing.T) {
	d := NewDispatcher(NewObjectTable())
	for _, test := range []struct {
		text string
		want int64
	}{
		{text: "abc", want: 3},
		{text: "", want: 0},
		{text: "😀", want: 2},
		{text: "A😀B", want: 4},
	} {
		length, err := d.Get(StringValue(test.text), "length")
		if err != nil {
			t.Fatal(err)
		}
		count, err := d.Get(StringValue(test.text), "count")
		if err != nil {
			t.Fatal(err)
		}
		if length.IntegerValue() != test.want {
			t.Errorf("%q length = %v, want %d", test.text, length, test.want)
		}
		if count.IntegerValue() != test.want {
			t.Errorf("%q count = %v, want %d", test.text, count, test.want)
		}
		if test.want > 0 {
			indexed, indexErr := d.Get(StringValue(test.text), "0")
			if indexErr != nil || indexed.Kind() != KindString {
				t.Fatalf("%q index = %v, %v", test.text, indexed, indexErr)
			}
		}
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
