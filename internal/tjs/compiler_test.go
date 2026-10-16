package tjs

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestCompileExpressionEmitsReturnAndLocations(t *testing.T) {
	p, err := compile_expression("expr.tjs", "1 + 2 * 3")
	if err != nil {
		t.Fatal(err)
	}
	if p.Storage != "expr.tjs" || len(p.Code) == 0 {
		t.Fatalf("invalid program: %#v", p)
	}
	last := p.Code[len(p.Code)-1]
	if last.Op.Code != OpReturn {
		t.Fatalf("last op = %q, want return", last.Op.Code)
	}
	if last.Location.Storage != "expr.tjs" || last.Location.Line != 1 {
		t.Fatalf("bad source location: %#v", last.Location)
	}
}

func TestCompileEvalAnimationLayerInlineWithProperties(t *testing.T) {
	source := "with (this) .clipleft=0,.cliptop=0,.clipwidth=50,.clipheight=50;"
	program, err := CompileEval("<eval operator>", source)
	if err != nil {
		t.Fatal(err)
	}

	vm := NewVM()
	context, err := vm.NewDictionary()
	if err != nil {
		t.Fatal(err)
	}
	budget := uint64(1000)
	if _, err := vm.ExecuteInContext(&program, context, vmTestHost{}, &budget); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]int64{"clipleft": 0, "cliptop": 0, "clipwidth": 50, "clipheight": 50} {
		value, err := vm.GetMember(context, name, vmTestHost{}, &budget)
		if got, intErr := value.Int(); err != nil || intErr != nil || got != want {
			t.Errorf("%s = %v, %v; want %d", name, value, errors.Join(err, intErr), want)
		}
	}
}

func TestCompileInterpolatedString(t *testing.T) {
	got := executeVMSource(t, `var name = "World"; return @"Hello &name; ${1 + 2}";`)
	if got.Text() != "Hello World 3" {
		t.Fatalf("interpolated string = %q, want %q", got.Text(), "Hello World 3")
	}
}

func TestCompileCatchWithoutBinding(t *testing.T) {
	for _, source := range []string{
		`try { throw "caught"; } catch {} return 7;`,
		`try { throw "caught"; } catch() {} return 7;`,
	} {
		got := executeVMSource(t, source)
		if value, err := got.Int(); err != nil || value != 7 {
			t.Errorf("catch without binding in %q = %v, %v; want 7", source, got, err)
		}
	}
}

func TestCompileAndRunNumberLiterals(t *testing.T) {
	tests := []struct {
		source  string
		kind    ValueKind
		integer int64
		real    float64
	}{
		{source: "0x000000", kind: KindInteger, integer: 0},
		{source: "0X2A", kind: KindInteger, integer: 42},
		{source: "0b101010", kind: KindInteger, integer: 42},
		{source: "0b10_10", kind: KindInteger, integer: 10},
		{source: "077", kind: KindInteger, integer: 63},
		{source: "08", kind: KindInteger, integer: 8},
		{source: "1_000", kind: KindInteger, integer: 1000},
		{source: "0x2a + 2", kind: KindInteger, integer: 44},
		{source: "0b101 + 0x1", kind: KindInteger, integer: 6},
		{source: "42", kind: KindInteger, integer: 42},
		{source: "12.5e1", kind: KindReal, real: 125},
		{source: "0x1.8p1", kind: KindReal, real: 3},
		{source: "0x1p3", kind: KindReal, real: 8},
		{source: "0x1_8p0", kind: KindReal, real: 24},
		{source: "0X1.8P+1", kind: KindReal, real: 3},
	}
	for _, test := range tests {
		program, err := compile_expression("number.tjs", test.source)
		if err != nil {
			t.Fatalf("compile %q: %v", test.source, err)
		}
		budget := uint64(1000)
		got, err := NewVM().Execute(&program, vmTestHost{}, &budget)
		if err != nil {
			t.Fatalf("execute %q: %v", test.source, err)
		}
		if got.Kind() != test.kind {
			t.Errorf("execute %q kind = %v, want %v", test.source, got.Kind(), test.kind)
			continue
		}
		if test.kind == KindInteger && got.IntegerValue() != test.integer {
			t.Errorf("execute %q = %d, want %d", test.source, got.IntegerValue(), test.integer)
		}
		if test.kind == KindReal && got.RealValue() != test.real {
			t.Errorf("execute %q = %v, want %v", test.source, got.RealValue(), test.real)
		}
	}

	program, err := compile_expression("number-location.tjs", "1 +\n  0x2a")
	if err != nil {
		t.Fatal(err)
	}
	for _, instruction := range program.Code {
		if instruction.Op.Code == OpConstant && instruction.Op.Value.Kind() == KindInteger && instruction.Op.Value.IntegerValue() == 42 {
			if instruction.Location.Line != 2 || instruction.Location.Column != 3 {
				t.Fatalf("hex literal location = %#v, want line 2 column 3", instruction.Location)
			}
			return
		}
	}
	t.Fatal("hex literal constant was not emitted")
}

func TestCompileAndRunPostfixIsValid(t *testing.T) {
	for _, test := range []struct {
		name   string
		source string
		want   int64
	}{
		{name: "object", source: `var object = %[]; return object isvalid;`, want: 1},
		{name: "void", source: `return void isvalid;`, want: 1},
		{name: "null", source: `return null isvalid;`, want: 1},
		{name: "condition", source: `var object = %[]; if (object isvalid) return 1; return 0;`, want: 1},
		{name: "prefix compatibility", source: `var object = %[]; var valid = isvalid object; var invalidated = invalidate object; return valid * 10 + invalidated;`, want: 11},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := executeVMSource(t, test.source)
			if value, err := got.Int(); err != nil || value != test.want {
				t.Fatalf("result = %v, want %d", got, test.want)
			}
		})
	}
}

func TestCompileAndRunChainedAssignments(t *testing.T) {
	for _, test := range []struct {
		name   string
		source string
		want   int64
	}{
		{
			name:   "variables",
			source: "var a = 0; var b = 0; var result = a = b = 7; return a * 100 + b * 10 + result;",
			want:   777,
		},
		{
			name:   "members",
			source: "var object = %[first:0, second:0]; var result = object.first = object.second = 9; return object.first * 100 + object.second * 10 + result;",
			want:   999,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := executeVMSource(t, test.source)
			if value, err := got.Int(); err != nil || value != test.want {
				t.Fatalf("result = %v, want %d", got, test.want)
			}
		})
	}
}

func TestCompileStaticMemberKeys(t *testing.T) {
	program, err := compile("static-member-keys.tjs", `
var object = %[value:1];
var key = "value";
object.value;
object[key];
object.value = 2;
object.value += 1;
object.value++;
typeof object.value;
delete object.value;
`)
	if err != nil {
		t.Fatal(err)
	}
	staticOps := 0
	dynamicGets := 0
	for _, instruction := range program.Code {
		switch instruction.Op.Code {
		case OpGet, OpSet, OpTypeOfMember, OpDeleteMember:
			if instruction.Op.StaticKey {
				staticOps++
				if instruction.Op.Value.Kind() != KindString || instruction.Op.Value.Text() != "value" {
					t.Fatalf("static member key = %#v, want string value", instruction.Op.Value)
				}
			} else if instruction.Op.Code == OpGet {
				dynamicGets++
			}
		}
	}
	if staticOps != 8 {
		t.Fatalf("static member operations = %d, want 8", staticOps)
	}
	if dynamicGets != 1 {
		t.Fatalf("dynamic member reads = %d, want 1", dynamicGets)
	}
}

func TestCompileAndRunSwapAssignment(t *testing.T) {
	got := executeVMSource(t, `
var first = "left", second = "right";
first <-> second;
var object = %[x:1, y:2], index = 0;
function nextKey() { index++; return index == 1 ? "x" : "y"; }
object[nextKey()] <-> object[nextKey()];
return [first, second, object.x, object.y, index].join("|");
`)
	if want := "right|left|2|1|2"; got.Text() != want {
		t.Fatalf("swap assignment = %q, want %q", got.Text(), want)
	}
}

func TestCompileAndRunBackslashAssignment(t *testing.T) {
	for _, test := range []struct {
		name   string
		source string
		want   int64
	}{
		{
			name:   "variable and returned value",
			source: `var n=17; var result=n \= 5; return n * 10 + result;`,
			want:   33,
		},
		{
			name:   "member and index",
			source: `var object=%[value:17]; var array=[17]; var member=object.value \= 5; var index=array[0] \= 4; return member * 1000 + object.value * 100 + index * 10 + array[0];`,
			want:   3344,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := executeVMSource(t, test.source)
			if value, err := got.Int(); err != nil || value != test.want {
				t.Fatalf("result = %v, want %d", got, test.want)
			}
		})
	}
}

func TestBackslashAssignmentDivisionErrorHasSourceLocation(t *testing.T) {
	const source = "var n=1;\nn \\= 0;"
	program, err := compile("backslash-assignment.tjs", source)
	if err != nil {
		t.Fatal(err)
	}
	var operator *Instruction
	for i := range program.Code {
		if program.Code[i].Op.Code == OpBinary && program.Code[i].Op.Operator == "\\" {
			operator = &program.Code[i]
			break
		}
	}
	if operator == nil {
		t.Fatal("compiled backslash assignment did not emit integer division")
	}
	if operator.Location.Storage != "backslash-assignment.tjs" || operator.Location.Line != 2 || operator.Location.Column != 1 {
		t.Fatalf("integer division location = %#v, want backslash-assignment.tjs:2:1", operator.Location)
	}
	budget := uint64(1000)
	_, err = NewVM().Execute(&program, vmTestHost{}, &budget)
	if err == nil || !strings.Contains(err.Error(), "integer division by zero") {
		t.Fatalf("execution error = %v, want integer division by zero", err)
	}
}

func TestCompileAndRunDictionarySeparators(t *testing.T) {
	got := executeVMSource(t, `var key = "dynamic"; var object = %[plain:1, key=>%[child:2, ("nested" + key)=>[3,4,],],]; return object.plain + object.dynamic.child + object.dynamic.nesteddynamic[1];`)
	if value, err := got.Int(); err != nil || value != 7 {
		t.Fatalf("dictionary result = %v, want 7", got)
	}
}
func TestCompileAndRunArrayAssociativeEntries(t *testing.T) {
	got := executeVMSource(t, `var key = "dynamic"; var values = ["first", key => 7, "last", "alias": 9]; var sparse = ["first",, key => 7, "last", "alias": 9]; var pairs = ["left" => 3, "right" => 4]; return values.count == 6 && values[0] == "first" && values[1] == key && values[2] == 7 && values[3] == "last" && values[4] == "alias" && values[5] == 9 && values.dynamic == 7 && values.alias == 9 && sparse.count == 7 && sparse[0] == "first" && sparse[1] === void && sparse[2] == key && sparse[3] == 7 && sparse[4] == "last" && sparse[5] == "alias" && sparse[6] == 9 && sparse.dynamic == 7 && sparse.alias == 9 && pairs.count == 4 && pairs[0] == "left" && pairs[1] == 3 && pairs[2] == "right" && pairs[3] == 4 && pairs.left == 3 && pairs.right == 4;`)
	truth, err := got.Truth()
	if err != nil || !truth {
		t.Fatalf("associative array result = %v, %v; want true", got, err)
	}
}

func TestCompileRejectsMalformedNumberLiterals(t *testing.T) {
	for _, test := range []struct {
		source string
		want   string
	}{
		{source: "0x", want: "missing digits in numeric literal"},
		{source: "0X", want: "missing digits in numeric literal"},
		{source: "0xg", want: "missing digits in numeric literal"},
		{source: "0b", want: "missing digits in numeric literal"},
		{source: "0b2", want: "missing digits in numeric literal"},
		{source: "078", want: "invalid octal literal"},
		{source: "1e+", want: "missing exponent digits"},
		{source: "0x1.8p", want: "missing exponent digits"},
		{source: "0b1.1", want: "invalid real literal"},
		{source: "0x8000000000000000", want: "integer literal out of range"},
	} {
		_, err := compile_expression("number-error.tjs", test.source)
		if err == nil {
			t.Errorf("compile_expression(%q) succeeded", test.source)
		} else if !strings.Contains(err.Error(), "number-error.tjs:1:1: "+test.want) {
			t.Errorf("compile_expression(%q) error = %q, want location and %q", test.source, err, test.want)
		}
	}
}

func TestCompileAndRunOctetLiteral(t *testing.T) {
	program, err := compile_expression("octet.tjs", "<%\n 00 ff, 10; 2a %>")
	if err != nil {
		t.Fatal(err)
	}
	vm := NewVM()
	budget := uint64(100)
	got, err := vm.Execute(&program, vmTestHost{}, &budget)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind() != KindOctet || !bytes.Equal(got.Octets(), []byte{0x00, 0xff, 0x10, 0x2a}) {
		t.Fatalf("octet value = %v, want 00 ff 10 2a", got)
	}
	if program.Code[0].Location.Storage != "octet.tjs" || program.Code[0].Location.Line != 1 || program.Code[0].Location.Column != 1 {
		t.Fatalf("literal location = %#v", program.Code[0].Location)
	}
}

func TestCompileOctetLiteralRejectsMalformedInput(t *testing.T) {
	for _, test := range []struct {
		source string
		want   string
	}{
		{source: "<% 0 %>", want: "odd number of octet digits"},
		{source: "<% gg %>", want: "invalid octet digit"},
		{source: "<% 00", want: "unterminated octet literal"},
	} {
		if _, err := compile_expression("octet.tjs", test.source); err == nil {
			t.Errorf("compile_expression(%q) succeeded", test.source)
		} else if !strings.Contains(err.Error(), test.want) {
			t.Errorf("compile_expression(%q) error = %q, want %q", test.source, err, test.want)
		}
	}
}

func TestCompileStatementsAndShortCircuit(t *testing.T) {
	p, err := compile("unit.tjs", "var x = 1; if (x && x < 3) x = x + 1; while (x < 3) x = x + 1;")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Code) < 8 {
		t.Fatalf("too little code: %d", len(p.Code))
	}
}

func TestCompilePreprocessor(t *testing.T) {
	pp := NewPreprocessor()
	pp.Define("enabled", 1)
	p, err := compile_with_preprocessor("pp.tjs", "#if enabled\nvar x = 4;\n#else\nvar y = 9;\n#endif\n", false, pp)
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range p.Code {
		if in.Op.Code == OpDeclare && in.Op.Name == "y" {
			t.Fatal("disabled branch emitted")
		}
	}
}

func TestCompileSwitch(t *testing.T) {
	p, err := compile("switch.tjs", "switch (x) { case 1: x = 2; default: x = 3; }")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Code) < 6 {
		t.Fatalf("switch emitted too little code: %d", len(p.Code))
	}
}

func TestCompileAdjacentStringLiterals(t *testing.T) {
	tests := []struct {
		name               string
		source             string
		want               []string
		wantUnits          []uint16
		wantPlus           bool
		wantLocationLine   uint64
		wantLocationColumn uint64
	}{
		{name: "same-line whitespace", source: `"left\n" "right"`, want: []string{"left\nright"}},
		{name: "cross-line whitespace", source: `"left"
"right"`, want: []string{"leftright"}},
		{name: "first fragment source location", source: `  "left"
 "right"`, want: []string{"leftright"}, wantLocationLine: 1, wantLocationColumn: 3},
		{name: "without whitespace", source: `"left""right"`, want: []string{"leftright"}},
		{name: "array comma", source: `["a" "b", "c"]`, want: []string{"ab", "c"}},
		{name: "ordinary plus operator", source: `"a" + "b"`, want: []string{"a", "b"}, wantPlus: true},
		{name: "function arguments", source: `consume("a" "b", "c" "d")`, want: []string{"ab", "cd"}},
		{
			name: "UTF-16 surrogate pair",
			source: `"\uD83D"
"\uDE00"`,
			want:      []string{"😀"},
			wantUnits: []uint16{0xd83d, 0xde00},
		},
	}
	for _, test := range tests {
		program, err := compile_expression("adjacent.tjs", test.source)
		if err != nil {
			t.Errorf("%s: compile failed: %v", test.name, err)
			continue
		}
		var got []string
		var units []uint16
		var firstLocationLine, firstLocationColumn uint64
		for _, instruction := range program.Code {
			if instruction.Op.Code != OpConstant || instruction.Op.Value.Kind() != KindString {
				continue
			}
			if len(got) == 0 {
				firstLocationLine = instruction.Location.Line
				firstLocationColumn = instruction.Location.Column
			}
			got = append(got, instruction.Op.Value.Text())
			units = instruction.Op.Value.UTF16()
		}
		if !reflect.DeepEqual(got, test.want) {
			t.Errorf("%s: string constants = %q, want %q", test.name, got, test.want)
		}
		if test.wantUnits != nil && !reflect.DeepEqual(units, test.wantUnits) {
			t.Errorf("%s: UTF-16 units = %#v, want %#v", test.name, units, test.wantUnits)
		}
		if test.wantLocationLine != 0 &&
			(firstLocationLine != test.wantLocationLine || firstLocationColumn != test.wantLocationColumn) {
			t.Errorf("%s: first string location = %d:%d, want %d:%d", test.name, firstLocationLine, firstLocationColumn, test.wantLocationLine, test.wantLocationColumn)
		}
		if test.wantPlus {
			foundPlus := false
			for _, instruction := range program.Code {
				if instruction.Op.Code == OpBinary && instruction.Op.Operator == "+" {
					foundPlus = true
					break
				}
			}
			if !foundPlus {
				t.Errorf("%s: ordinary + operator was not emitted", test.name)
			}
		}
	}
	for _, source := range []string{`"a" /* comment */ "b"`, `"a" 'b'`} {
		if _, err := compile_expression("adjacent.tjs", source); err == nil {
			t.Errorf("compile_expression(%q) succeeded; comments or a different delimiter must separate literals", source)
		}
	}
}

func TestCompileAndRunRegexpLiteralReplacement(t *testing.T) {
	program, err := compile_expression("regexp.tjs", `"foo/bar foo/bar".replace(/[a-z]+\/[a-z]+/g, "x")`)
	if err != nil {
		t.Fatal(err)
	}
	foundConstruct := false
	for _, instruction := range program.Code {
		if instruction.Op.Code == OpConstruct {
			foundConstruct = true
			break
		}
	}
	if !foundConstruct {
		t.Fatal("regexp literal did not compile through RegExp construction")
	}
	budget := uint64(1000)
	got, err := NewVM().Execute(&program, vmTestHost{}, &budget)
	if err != nil {
		t.Fatal(err)
	}
	if got.Text() != "x x" {
		t.Fatalf("replace = %q, want %q", got.Text(), "x x")
	}
}

func TestCompileRegexpLiteralAndDivision(t *testing.T) {
	for _, test := range []struct {
		source string
		want   string
	}{
		{source: `"ababa".replace(/a/, "x")`, want: "xbaba"},
		{source: `"AbA".replace(/a/i, "x")`, want: "xbA"},
		{source: `/foo/.test("foobar")`, want: "1"},
		{source: `10 / 2 / 5`, want: "1"},
		{source: `/^([a-z]+)(\d+)/.match("foo42").join("|") + "#" + /x/.match("foo42").count`, want: "foo42|foo|42#0"},
	} {
		program, err := compile_expression("regexp-division.tjs", test.source)
		if err != nil {
			t.Fatalf("compile %q: %v", test.source, err)
		}
		budget := uint64(1000)
		got, err := NewVM().Execute(&program, vmTestHost{}, &budget)
		if err != nil {
			t.Fatalf("execute %q: %v", test.source, err)
		}
		if got.Text() != test.want {
			t.Errorf("execute %q = %q, want %q", test.source, got.Text(), test.want)
		}
	}
}

func TestCompileRegexpLiteralRejectsMalformedPatternsAndFlags(t *testing.T) {
	for _, source := range []string{`"x".replace(/(/, "y")`, `"x".replace(/a/gg, "y")`} {
		program, err := compile_expression("bad-regexp.tjs", source)
		if err != nil {
			t.Fatalf("compile %q: %v", source, err)
		}
		budget := uint64(1000)
		_, err = NewVM().Execute(&program, vmTestHost{}, &budget)
		if err == nil || !errors.Is(err, ErrRegexpMalformed) {
			t.Errorf("execute %q error = %v, want malformed regexp", source, err)
		}
	}
}
func TestCompileAndRunCStyleCasts(t *testing.T) {
	for _, test := range []struct {
		name   string
		source string
		want   Value
	}{
		{name: "int cast", source: `return (int)(7.9);`, want: IntegerValue(7)},
		{name: "real cast", source: `return (real)(1 + 2);`, want: RealValue(3)},
		{name: "string cast", source: `return (string)(20 + 22);`, want: StringValue("42")},
		{name: "nested cast and grouping", source: `return ((int)(7.9 + 0.4));`, want: IntegerValue(8)},
		{name: "ordinary grouping", source: `return (1 + 2) * 3;`, want: IntegerValue(9)},
		{name: "prefix int", source: `return int 7.9;`, want: IntegerValue(7)},
		{name: "prefix real", source: `return real (1 + 2);`, want: RealValue(3)},
		{name: "prefix string", source: `return string (20 + 22);`, want: StringValue("42")},
		{name: "const object expression", source: `var value = (const)%[a:1]; return value.a;`, want: IntegerValue(1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := executeVMSource(t, test.source)
			if !got.StrictEqual(test.want) {
				t.Fatalf("result = %v (%s), want %v (%s)", got, got.Kind(), test.want, test.want.Kind())
			}
		})
	}
}
