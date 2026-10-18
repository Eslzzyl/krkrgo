package runtime

import (
	"strings"
	"testing"

	"github.com/eslzzyl/krkrgo/internal/kag"
)

func TestKAGExecutionNestedMacroDefinition(t *testing.T) {
	source := `[macro name=outer]
[macro name=inner]
Inner %value
[endmacro]
After %value
[endmacro]
[outer value=one]
[outer value=two]
[inner value=three]
@end
`
	parser, err := NewKAGParser("nested.ks", source)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := parser.macroDefs["outer"]; !ok {
		t.Fatal("outer macro was not indexed")
	}
	if _, ok := parser.macroDefs["inner"]; ok {
		t.Fatal("nested macro was registered before its definition executed")
	}
	var text []string
	parser.SetCallbacks(&KAGCallbacks{OnText: func(_ *KAGParser, value string) error {
		if value = strings.TrimSpace(value); value != "" {
			text = append(text, value)
		}
		return nil
	}})
	if _, err := parser.Run(nil); err != nil {
		t.Fatal(err)
	}
	want := []string{"After one", "After two", "Inner three"}
	if strings.Join(text, "|") != strings.Join(want, "|") {
		t.Fatalf("nested macro text = %v, want %v", text, want)
	}
	if body, ok := parser.macroDefs["inner"]; !ok || len(body) == 0 {
		t.Fatalf("nested macro definition = %v, %v; want a registered body", body, ok)
	}
}

func TestKAGExecutionNestedMacroMissingEndmacro(t *testing.T) {
	parser, err := NewKAGParser("nested-broken.ks", "[macro name=outer]\n[macro name=inner]\ninside\n@endmacro\n")
	if err == nil {
		t.Fatalf("nested macro without a closing endmacro parsed successfully: %+v", parser)
	}
	if !strings.Contains(err.Error(), "missing endmacro") {
		t.Fatalf("nested macro error = %v, want a missing endmacro error", err)
	}
	if strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("nested macro error still reports unsupported: %v", err)
	}
}

func TestKAGExecutionNestedParameterMacroDefinition(t *testing.T) {
	source := `[macro name=define]
[pmacro name=opt a=1]
[endmacro]
[define]
[use opt b=2]
@end
`
	parser, err := NewKAGParser("nested-pmacro.ks", source)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := parser.paramMacroDefs["opt"]; ok {
		t.Fatal("nested parameter macro was registered before its definition executed")
	}
	var used kag.Tag
	parser.SetCallbacks(&KAGCallbacks{OnTag: func(_ *KAGParser, tag *kag.Tag) error {
		if tag.Name == "use" {
			used = *tag
		}
		return nil
	}})
	if _, err := parser.Run(nil); err != nil {
		t.Fatal(err)
	}
	if value, ok := used.Get("a"); !ok || value != "1" {
		t.Fatalf("nested parameter macro a = %q, %v; want 1", value, ok)
	}
	if value, ok := used.Get("b"); !ok || value != "2" {
		t.Fatalf("nested parameter macro b = %q, %v; want 2", value, ok)
	}
}

func TestKAGExecutionNestedMacroConditionalDefinition(t *testing.T) {
	source := `[macro name=outer]
[if exp="false"]
[macro name=hidden]
hidden
[endmacro]
[endif]
visible
[endmacro]
[outer]
[hidden]
@end
`
	parser, err := NewKAGParser("nested-cond.ks", source)
	if err != nil {
		t.Fatal(err)
	}
	var text []string
	var tags []string
	parser.SetCallbacks(&KAGCallbacks{
		OnText: func(_ *KAGParser, value string) error {
			if value = strings.TrimSpace(value); value != "" {
				text = append(text, value)
			}
			return nil
		},
		OnTag: func(_ *KAGParser, tag *kag.Tag) error {
			tags = append(tags, tag.Name)
			return nil
		},
	})
	if _, err := parser.Run(nil); err != nil {
		t.Fatal(err)
	}
	if strings.Join(text, "|") != "visible" {
		t.Fatalf("nested conditional text = %v, want visible only", text)
	}
	if strings.Join(tags, "|") != "hidden" {
		t.Fatalf("nested conditional tags = %v, want the inactive definition to stay an ordinary tag", tags)
	}
}

func TestKAGMacroProjectionsReflectLiveDefinitions(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()

	source := `Plugins.link("plugin/KAGParser.dll");
p = new KAGParser();
p.loadScenario("projection.ks", "[macro name=one]\nHello %who\n[endmacro]\n[pmacro name=style x=1 y=2]\n[macro name=gone]\nbye\n[endmacro]\n[erasepmacro name=style]\n[erasemacro name=gone]\n@end\n[pmacro name=link storage=&expr href=\"a b\"]\n");
macrosStable = p.macros === p.macros;
paramsStable = p.paramMacros === p.paramMacros;
macroOne = p.macros.one;
macroGoneBefore = p.macros.gone;
styleX = p.paramMacros.style[0];
styleY = p.paramMacros.style[3];
linkStorage = p.paramMacros.link[1];
while (p.getNextTag() !== void) {}
macroGoneAfter = p.macros.gone;
paramGoneAfter = p.paramMacros.style;`
	if _, err := session.executeSource("<kag-macro-projection>", source, false); err != nil {
		t.Fatal(err)
	}
	value, err := session.Evaluate(`macrosStable && paramsStable && macroOne === "\nHello %who\n" && macroGoneBefore === "\nbye\n" && styleX === "x" && styleY === "2" && linkStorage === "&expr" && macroGoneAfter === void && paramGoneAfter === void`)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := value.Truth(); !ok {
		detail, _ := session.Evaluate(`[macrosStable,paramsStable,macroOne,macroGoneBefore,styleX,styleY,linkStorage,macroGoneAfter,paramGoneAfter].join("|")`)
		t.Fatalf("macro projections did not match live definitions: %v (%v)", value, detail)
	}
}

func TestKAGMacrosPropertyIsReadOnly(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()

	source := `Plugins.link("plugin/KAGParser.dll");
p = new KAGParser();
p.loadScenario("readonly.ks", "[macro name=one][endmacro]\n@end\n");`
	if _, err := session.executeSource("<kag-macro-readonly>", source, false); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Evaluate(`p.macros = null`); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("KAGParser.macros assignment error = %v, want a read-only error", err)
	}
	value, err := session.Evaluate(`p.macros.one !== void`)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := value.Truth(); !ok {
		t.Fatalf("KAGParser.macros projection was cleared by a rejected assignment: %v", value)
	}
}

func TestKAGMacroBodyTextCanonicalForm(t *testing.T) {
	parser, err := NewKAGParser("body.ks", "[macro name=body]\nplain [tag a=1 b=&x c=\"needs quote\"] [[literal]\n[endmacro]\n")
	if err != nil {
		t.Fatal(err)
	}
	body, ok := parser.macroDefs["body"]
	if !ok {
		t.Fatal("body macro was not indexed")
	}
	want := "\nplain [tag a=1 b=&x c=\"needs quote\"] [[literal]\n"
	if got := kagMacroBodyText(body); got != want {
		t.Fatalf("macro body text = %q, want %q", got, want)
	}
}

func TestKAGMacroProjectionIncludesNestedDefinitions(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()

	source := `Plugins.link("plugin/KAGParser.dll");
p = new KAGParser();
p.loadScenario("nested-projection.ks", "[macro name=outer]\n[macro name=inner]\nX %value\n[endmacro]\n[endmacro]\n[outer]\n@end\n");
before = p.macros.inner;
while (p.getNextTag() !== void) {}
after = p.macros.inner;`
	if _, err := session.executeSource("<kag-nested-projection>", source, false); err != nil {
		t.Fatal(err)
	}
	value, err := session.Evaluate(`before === void && after === "\nX %value\n"`)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := value.Truth(); !ok {
		detail, _ := session.Evaluate(`[before,after].join("|")`)
		t.Fatalf("nested macro projection = %v (%v)", value, detail)
	}
}
