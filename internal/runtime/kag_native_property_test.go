package runtime

import (
	"strconv"
	"testing"
)

func TestKAGNativeMacroParameterProperties(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()

	if _, err := session.executeSource("<kag-native-macro-properties>", `
Plugins.link("plugin/KAGParser.dll");
p = new KAGParser();
global.emptyMacroParameters = p.mp === null && p.macroParams === null;
p.loadScenario("macro.ks", "[macro name=hello][foo][endmacro][hello value=ok other=17]");
tag = p.getNextTag();
global.activeMacroParameters = tag.tagname == "foo" && p.mp.value == "ok" && p.macroParams.other == "17";
p.getNextTag();
end = p.getNextTag();
global.clearedMacroParameters = end === void && p.mp === null && p.macroParams === null;
`, false); err != nil {
		t.Fatal(err)
	}

	value, err := session.Evaluate(`emptyMacroParameters && activeMacroParameters && clearedMacroParameters`)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := value.Truth(); !ok {
		t.Fatal("KAGParser.mp and macroParams did not reflect the active macro argument frame")
	}
}

func TestKAGNativeCurLineStrTracksPhysicalSourceAndLifecycle(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()

	source := "*a|Title\r\n@line value=1 \\\r\n;continued=2\r\n*next|Caption\r\nbody"
	script := `Plugins.link("plugin/KAGParser.dll");
p = new KAGParser();
global.emptyLine = p.curLineStr == "";
p.loadScenario("lines.ks", ` + strconv.Quote(source) + `);
global.loadedLine = p.curLineStr;
p.getNextTag();
global.multilineLine = p.curLineStr;
p.goToLabel("*next");
global.lineAfterJump = p.curLineStr;
p.getNextTag();
global.lineAfterLabel = p.curLineStr;
p.clear();
global.clearedLine = p.curLineStr == "";
p.loadScenario("again.ks", "again");
global.reloadedLine = p.curLineStr;`
	if _, err := session.executeSource("<kag-cur-line-str-test>", script, false); err != nil {
		t.Fatal(err)
	}

	for name, want := range map[string]string{
		"loadedLine":     "*a|Title",
		"multilineLine":  "@line value=1 \\",
		"lineAfterJump":  "@line value=1 \\",
		"lineAfterLabel": "body",
		"reloadedLine":   "again",
	} {
		value, err := session.Evaluate(name)
		if err != nil {
			t.Fatal(err)
		}
		if got := value.Text(); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	value, err := session.Evaluate("emptyLine && clearedLine")
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := value.Truth(); !ok {
		t.Fatal("curLineStr was not empty before load and after clear")
	}
}
