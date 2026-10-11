package tjs

import (
	"strings"
	"testing"
)

func TestPreprocessorDefinesAndEagerExpressions(t *testing.T) {
	p := NewPreprocessor()
	p.Define("A", 7)
	if got, ok := p.Get("A"); !ok || got != 7 {
		t.Fatalf("Get(A) = %d, %v", got, ok)
	}
	p.Set("B", -2)
	got, err := p.Evaluate("A * 3 + B")
	if err != nil || got != 19 {
		t.Fatalf("Evaluate = %d, %v", got, err)
	}
	got, err = p.Evaluate("0 && (1 / 0)")
	if err == nil {
		t.Fatalf("eager expression unexpectedly hid division by zero (value %d)", got)
	}
	got, err = p.Evaluate("0xffffffff + 2")
	if err != nil || got != 1 {
		t.Fatalf("32-bit wrap = %d, %v", got, err)
	}
}

func TestPreprocessorSkipsBranchesAndKeepsNewlines(t *testing.T) {
	p := NewPreprocessor()
	src := Source{Storage: "main.tjs", Text: "#define FLAG 0\n#if FLAG\nbad();\n#elif 1\nok(); // comment\n#else\nno();\n#endif\n"}
	out, err := p.Process(src)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Repeat(" ", len("#define FLAG 0")) + "\n" +
		strings.Repeat(" ", len("#if FLAG")) + "\n" +
		strings.Repeat(" ", len("bad();")) + "\n" +
		strings.Repeat(" ", len("#elif 1")) + "\n" +
		"ok(); // comment\n" +
		strings.Repeat(" ", len("#else")) + "\n" +
		strings.Repeat(" ", len("no();")) + "\n" +
		strings.Repeat(" ", len("#endif")) + "\n"
	if out.Text != want {
		t.Fatalf("processed text = %q, want %q", out.Text, want)
	}
	if len(out.Locations) != out.Len() {
		t.Fatalf("location count = %d, units = %d", len(out.Locations), out.Len())
	}
	if out.Locations[0].Storage != "main.tjs" || out.Locations[0].Line != 1 || out.Locations[0].Column != 1 {
		t.Fatalf("first location = %#v", out.Locations[0])
	}
	if got := out.Locations[len(out.Locations)-1]; got.Line != 8 {
		t.Fatalf("last location = %#v", got)
	}
}

func TestCompileAtSetInitDirective(t *testing.T) {
	source := "@set(_DEBUG = 0)\n@if(_DEBUG == 0)\nvar startup = 1;\n@endif\n"
	state := NewPreprocessor()
	program, err := CompileWithPreprocessor("Init.tjs", source, false, state)
	if err != nil {
		t.Fatalf("CompileWithPreprocessor() error = %v", err)
	}
	if got, ok := state.Get("_DEBUG"); !ok || got != 0 {
		t.Fatalf("_DEBUG = %d, %v; want defined as 0", got, ok)
	}
	if len(program.Code) == 0 {
		t.Fatal("compiled Init.tjs fixture has no code")
	}
}

func TestPreprocessorAtConditionalDirectives(t *testing.T) {
	p := NewPreprocessor()
	p.Define("ENABLE", 1)
	source := "@if (kirikiriz)\nunreachable();\n@elif ENABLE && 2\nselected();\n@else\nother();\n@endif\n" +
		"@if 0\nouter();\n@if 1\ninner();\n@endif\n@else\n@if kirikiriz\nnested();\n@else\nfallback();\n@endif\n@endif\n"
	out, err := p.Process(Source{Storage: "at.tjs", Text: source})
	if err != nil {
		t.Fatal(err)
	}
	blank := func(line string) string { return strings.Repeat(" ", len(line)) + "\n" }
	want := blank("@if (kirikiriz)") + blank("unreachable();") +
		blank("@elif ENABLE && 2") + "selected();\n" +
		blank("@else") + blank("other();") + blank("@endif") +
		blank("@if 0") + blank("outer();") + blank("@if 1") +
		blank("inner();") + blank("@endif") + blank("@else") +
		blank("@if kirikiriz") + blank("nested();") + blank("@else") +
		"fallback();\n" + blank("@endif") + blank("@endif")
	if out.Text != want {
		t.Fatalf("processed text = %q, want %q", out.Text, want)
	}
	if out.Len() != len(source) {
		t.Fatalf("processed length = %d, source length = %d", out.Len(), len(source))
	}
	for _, line := range []struct {
		text string
		line int
	}{{"selected();", 4}, {"fallback();", 17}} {
		index := strings.Index(out.Text, line.text)
		if index < 0 || out.Locations[index].Line != uint64(line.line) || out.Locations[index].Column != 1 {
			t.Errorf("location for %q = %#v", line.text, out.Locations[index])
		}
	}

	p.Define("kirikiriz", 1)
	runtime, err := p.Process("@if (kirikiriz)\nruntime();\n@else\nfallback();\n@endif\n")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(runtime.Text, "runtime();") || strings.Contains(runtime.Text, "fallback();") {
		t.Fatalf("injected kirikiriz did not select its branch: %q", runtime.Text)
	}
}

func TestPreprocessorAtInlineConditionals(t *testing.T) {
	p := NewPreprocessor()
	p.Define("SLICE", 7)
	p.Define("kirikiriz", 1)
	source := "before(); @if(0) ignored @endif after();\n" +
		"@if(SLICE==7 && kirikiriz) selected(); @endif\n" +
		"@if(0) no(); @else yes(); @endif\n" +
		"var text = \"@if(0)\";\n"
	out, err := p.Process(source)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"before();", "after();", "selected();", "yes();", "\"@if(0)\""} {
		if !strings.Contains(out.Text, want) {
			t.Errorf("processed text %q does not contain %q", out.Text, want)
		}
	}
	for _, absent := range []string{"ignored", "no();"} {
		if strings.Contains(out.Text, absent) {
			t.Errorf("processed text still contains disabled %q: %q", absent, out.Text)
		}
	}
	selectedIndex := strings.Index(out.Text, "selected();")
	lineStart := strings.Index(source, "\n") + 1
	selectedColumn := strings.Index(source[lineStart:], "selected();") + 1
	if got := out.Locations[selectedIndex]; got.Line != 2 || got.Column != uint64(selectedColumn) {
		t.Fatalf("inline selected location = %#v, want line 2 column %d", got, selectedColumn)
	}
	if out.Len() != len(source) {
		t.Fatalf("processed length = %d, source length = %d", out.Len(), len(source))
	}
}

func TestPreprocessorAtDirectiveErrorsAndOrdinaryAtText(t *testing.T) {
	for _, tc := range []struct {
		source  string
		message string
	}{
		{"@elif 1\n", "@elif without @if"},
		{"@else\n", "@else without @if"},
		{"@endif\n", "@endif without @if"},
		{"@if 1\n", "unterminated conditional"},
		{"@if 1\n@else\n@elif 1\n@endif\n", "@elif after @else"},
		{"@if 1\n@else\n@else\n@endif\n", "duplicate @else"},
	} {
		_, err := NewPreprocessor().Process(tc.source)
		if err == nil || !strings.Contains(err.Error(), tc.message) {
			t.Errorf("Process(%q) error = %v, want containing %q", tc.source, err, tc.message)
		}
	}

	source := "var value = @;\n@ordinary;\n"
	out, err := NewPreprocessor().Process(source)
	if err != nil {
		t.Fatal(err)
	}
	if out.Text != source {
		t.Fatalf("ordinary @ text changed: %q", out.Text)
	}
}

func TestPreprocessorNestedAndUnicodeLocations(t *testing.T) {
	p := NewPreprocessor()
	src := Source{Storage: "x", Text: "😀\n#if 0\n😀\n#endif\n"}
	out, err := p.Process(src)
	if err != nil {
		t.Fatal(err)
	}
	if got := out.Units.Len(); got != 19 {
		t.Fatalf("unit count = %d", got)
	}
	if out.Locations[0].Column != 1 || out.Locations[1].Column != 2 {
		t.Fatalf("astral locations = %#v %#v", out.Locations[0], out.Locations[1])
	}
	// The disabled astral character is replaced by two spaces, not one rune.
	if out.Units.Units()[9] != ' ' || out.Units.Units()[10] != ' ' {
		t.Fatalf("disabled UTF-16 units = %#v", out.Units.Units()[9:11])
	}
}

func TestPreprocessorConditionalOperators(t *testing.T) {
	p := NewPreprocessor()
	p.Define("X", 4)
	for expression, want := range map[string]int32{
		"defined(X) && X == 4":       1,
		"defined (MISSING) || 2 < 1": 0,
		"~0":                         -1,
		"-1 >> 1":                    -1,
		"1 << 31":                    -2147483648,
	} {
		got, err := p.Expression(expression)
		if err != nil || got != want {
			t.Errorf("%q = %d, %v; want %d", expression, got, err, want)
		}
	}
}
