package runtime

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/eslzzyl/krkrgo/internal/assets"
	"github.com/eslzzyl/krkrgo/internal/core"
	"github.com/eslzzyl/krkrgo/internal/tjs"
)

func newScriptsTestSession(t *testing.T, resources map[string][]byte) *Session {
	t.Helper()
	project := t.TempDir()
	for name, content := range resources {
		path := filepath.Join(project, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	storage, err := assets.OpenStorage(project, core.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	session := NewSession(NewServices(storage, "", core.DefaultLimits()), 1_000_000)
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Errorf("close session: %v", err)
		}
		for _, archive := range storage.Archives {
			if err := archive.Close(); err != nil {
				t.Errorf("close storage archive: %v", err)
			}
		}
	})
	return session
}

func scriptsTestUTF16LE(source string) []byte {
	units := utf16.Encode([]rune(source))
	data := make([]byte, 2+2*len(units))
	data[0], data[1] = 0xff, 0xfe
	for i, unit := range units {
		binary.LittleEndian.PutUint16(data[2+2*i:], unit)
	}
	return data
}

func TestScriptsInlineExecutionAndContext(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()

	encoding, err := session.Evaluate("Scripts.textEncoding")
	if err != nil || encoding.Text() != "UTF-8" {
		t.Fatalf("default Scripts.textEncoding = %q, err=%v", encoding.Text(), err)
	}
	value, err := session.Evaluate(`Scripts.eval("1 + 2", "inline.tjs")`)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := value.Int(); err != nil || n != 3 {
		t.Fatalf("Scripts.eval result = %v, err=%v", value, err)
	}
	_, err = session.executeSource("<scripts-test>", `var context = %[value:2]; Scripts.exec("value += 3; this.extra = value;", "context.tjs", 0, context); global.context_result = Scripts.eval("value + this.extra", "context.tjs", 0, context);`, false)
	if err != nil {
		t.Fatal(err)
	}
	contextResult, err := session.Evaluate("global.context_result")
	if err != nil {
		t.Fatal(err)
	}
	if n, err := contextResult.Int(); err != nil || n != 10 {
		t.Fatalf("context eval result = %v, err=%v", contextResult, err)
	}
	if _, err := session.Evaluate("new Scripts()"); !errors.Is(err, ErrUnsupportedNative) {
		t.Fatalf("Scripts instance construction error = %v", err)
	}
}

func TestScriptsCloneRecursivelyCopiesArraysAndDictionaries(t *testing.T) {
	session := newScriptsTestSession(t, nil)
	_, err := session.executeSource("<scripts-clone-test>", `
var source = [1, [2], %["nested" => [3]]];
var copied = Scripts.clone(source);
source[0] = 99;
source[1][0] = 98;
source[2].nested[0] = 97;
global.clone_array_values = copied[0] + copied[1][0] + copied[2].nested[0];

var dictionary = %["number" => 4, "nested" => %["items" => [5]]];
var copied_dictionary = Scripts.clone(dictionary);
dictionary.number = 40;
dictionary.nested.items[0] = 50;
global.clone_dictionary_values = copied_dictionary.number + copied_dictionary.nested.items[0];
copied_dictionary.nested.items[0] = 6;
global.clone_dictionary_source_value = dictionary.nested.items[0];
`, false)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]int64{
		"clone_array_values":            6,
		"clone_dictionary_values":       9,
		"clone_dictionary_source_value": 50,
	} {
		value, err := session.Evaluate("global." + name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got, err := value.Int()
		if err != nil || got != want {
			t.Errorf("%s = %v, %v; want %d", name, value, err, want)
		}
	}
}

func TestScriptsStorageExecutionAndEncoding(t *testing.T) {
	shiftJIS := append([]byte("global.encoding_value='"), []byte{0x93, 0xfa, 0x96, 0x7b, 0x8c, 0xea}...)
	shiftJIS = append(shiftJIS, '\'', ';')
	session := newScriptsTestSession(t, map[string][]byte{
		"run.tjs":                  []byte("global.storage_value=19;"),
		"expression.tjs":           []byte("6 * 7"),
		"malformed-expression.tjs": []byte("6 * 7 trailing"),
		"savedata/datasc.ksd":      scriptsTestUTF16LE(`(const) %["score" => 42,"title" => "日本語","nested" => (const) %["empty" => (const) []]]`),
		"offset.tjs":               []byte("xxglobal.offset_value=23;"),
		"offset-utf16.tjs":         append([]byte("xx"), scriptsTestUTF16LE("6 * 7")...),
		"context-exec.tjs":         []byte("value += 2;"),
		"context-eval.tjs":         []byte("value + 3"),
		"shift-jis.tjs":            shiftJIS,
		"compiled.tjs":             []byte("TJS2compiled bytecode"),
		"compiled-offset.tjs":      []byte("xxTJS2compiled bytecode"),
		"plain.tjs":                []byte("global.plain_value=1;"),
	})
	if _, err := session.Evaluate(`Scripts.execStorage("run.tjs")`); err != nil {
		t.Fatal(err)
	}
	storageValue, err := session.Evaluate("global.storage_value")
	if err != nil {
		t.Fatal(err)
	}
	if n, err := storageValue.Int(); err != nil || n != 19 {
		t.Fatalf("execStorage side effect = %v, err=%v", storageValue, err)
	}
	evalValue, err := session.Evaluate(`Scripts.evalStorage("expression.tjs")`)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := evalValue.Int(); err != nil || n != 42 {
		t.Fatalf("evalStorage result = %v, err=%v", evalValue, err)
	}
	savedScore, err := session.Evaluate(`Scripts.evalStorage("savedata/datasc.ksd").score`)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := savedScore.Int(); err != nil || n != 42 {
		t.Fatalf("UTF-16 const save score = %v, err=%v", savedScore, err)
	}
	savedTitle, err := session.Evaluate(`Scripts.evalStorage("savedata/datasc.ksd").title`)
	if err != nil || savedTitle.Text() != "日本語" {
		t.Fatalf("UTF-16 const save title = %q, err=%v", savedTitle.Text(), err)
	}
	protected, err := session.Evaluate(`Scripts.evalStorage("savedata/datasc.ksd").nested.empty.count`)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := protected.Int(); err != nil || n != 0 {
		t.Fatalf("nested UTF-16 const save array = %v, err=%v", protected, err)
	}
	if _, err := session.Evaluate(`Scripts.evalStorage("malformed-expression.tjs")`); err == nil || !strings.Contains(err.Error(), "unexpected tokens after TJS expression") {
		t.Fatalf("ordinary evalStorage expression error = %v", err)
	}
	if _, err := session.Evaluate(`Scripts.execStorage("offset.tjs", "o2")`); err != nil {
		t.Fatal(err)
	}
	offsetValue, err := session.Evaluate("global.offset_value")
	if err != nil {
		t.Fatal(err)
	}
	if n, err := offsetValue.Int(); err != nil || n != 23 {
		t.Fatalf("offset storage result = %v, err=%v", offsetValue, err)
	}
	offsetEvalValue, err := session.Evaluate(`Scripts.evalStorage("offset-utf16.tjs", "o2")`)
	if err != nil {
		t.Fatalf("evalStorage TextStream data after binary offset: %v", err)
	}
	if n, err := offsetEvalValue.Int(); err != nil || n != 42 {
		t.Fatalf("offset evalStorage result = %v, err=%v", offsetEvalValue, err)
	}
	_, err = session.executeSource("<scripts-storage-test>", `var context = %[value:4]; Scripts.execStorage("context-exec.tjs", "", context); global.storage_context_result = Scripts.evalStorage("context-eval.tjs", "", context);`, false)
	if err != nil {
		t.Fatal(err)
	}
	contextValue, err := session.Evaluate("global.storage_context_result")
	if err != nil {
		t.Fatal(err)
	}
	if n, err := contextValue.Int(); err != nil || n != 9 {
		t.Fatalf("storage context result = %v, err=%v", contextValue, err)
	}
	if _, err := session.Evaluate(`Scripts.execStorage("compiled.tjs")`); !errors.Is(err, ErrUnsupportedNative) {
		t.Fatalf("compiled storage error = %v", err)
	}
	if _, err := session.Evaluate(`Scripts.execStorage("compiled-offset.tjs", "o2")`); !errors.Is(err, ErrUnsupportedNative) {
		t.Fatalf("offset bytecode storage error = %v", err)
	}
	if _, err := session.Evaluate(`Scripts.textEncoding = "UTF-8"`); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Evaluate(`Scripts.execStorage("shift-jis.tjs")`); err == nil || !strings.Contains(err.Error(), "invalid UTF-8") {
		t.Fatalf("explicit UTF-8 decoding of Shift-JIS source error = %v", err)
	}
	if _, err := session.Evaluate(`Scripts.textEncoding = "Shift_JIS"`); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Evaluate(`Scripts.execStorage("shift-jis.tjs")`); err != nil {
		t.Fatal(err)
	}
	encodedValue, err := session.Evaluate("global.encoding_value")
	if err != nil || encodedValue.Text() != "日本語" {
		t.Fatalf("Shift-JIS script value = %q, err=%v", encodedValue.Text(), err)
	}
	if _, err := session.Evaluate(`Scripts.textEncoding = "UTF-8"`); err != nil {
		t.Fatal(err)
	}
	if _, err := session.ExecuteStorage("shift-jis.tjs"); err != nil {
		t.Fatalf("Session.ExecuteStorage compatibility after encoding update: %v", err)
	}
	if _, err := session.Evaluate(`Scripts.textEncoding = "unsupported"`); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Evaluate(`Scripts.execStorage("plain.tjs")`); err == nil || !strings.Contains(err.Error(), "unsupported script text encoding") {
		t.Fatalf("unsupported text encoding error = %v", err)
	}
}
func TestScriptsStorageExecutesGBKFixture(t *testing.T) {
	source := append([]byte("global.gbk_value='"), 0xd6, 0xd0, 0xce, 0xc4)
	source = append(source, '\'', ';')
	session := newScriptsTestSession(t, map[string][]byte{"gbk.tjs": source})
	if _, err := session.ExecuteStorage("gbk.tjs"); err != nil {
		t.Fatalf("ExecuteStorage GBK fixture: %v", err)
	}
	value, err := session.Evaluate("global.gbk_value")
	if err != nil || value.Text() != "中文" {
		t.Fatalf("GBK script value = %q, err=%v", value.Text(), err)
	}
	if got, err := decodeScriptText(source, "GBK"); err != nil || !strings.Contains(got, "中文") {
		t.Fatalf("explicit GBK decoding = %q, err=%v", got, err)
	}
}
func TestScriptsNestedStorageUsesDefaultDecoderUntilExplicitEncoding(t *testing.T) {
	shiftJIS := append([]byte("global.nested_encoding_value='"), []byte{0x93, 0xfa, 0x96, 0x7b, 0x8c, 0xea}...)
	shiftJIS = append(shiftJIS, '\'', ';')
	session := newScriptsTestSession(t, map[string][]byte{
		"startup.tjs":         []byte(`Scripts.execStorage("sys_source/Init.tjs");`),
		"sys_source/Init.tjs": shiftJIS,
	})
	if _, err := session.Startup(); err != nil {
		t.Fatalf("startup with nested Shift-JIS storage: %v", err)
	}
	value, err := session.Evaluate("global.nested_encoding_value")
	if err != nil || value.Text() != "日本語" {
		t.Fatalf("nested Shift-JIS value = %q, err=%v", value.Text(), err)
	}
	if _, err := session.Evaluate(`Scripts.textEncoding = "UTF-8"`); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Startup(); err == nil || !strings.Contains(err.Error(), "invalid UTF-8") {
		t.Fatalf("explicit UTF-8 nested storage error = %v", err)
	}
	if _, err := session.Evaluate(`Scripts.textEncoding = "Shift_JIS"`); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Startup(); err != nil {
		t.Fatalf("explicit Shift_JIS nested storage: %v", err)
	}
}

func TestScriptsPreprocessorPersistsAcrossExecutionPaths(t *testing.T) {
	session := newScriptsTestSession(t, map[string][]byte{
		"define.tjs": []byte("#define CUSTOM_DEFINE 23\n"),
		"check-storage.tjs": []byte("#if kirikiriz == 1 && CUSTOM_DEFINE == 23 && EXEC_DEFINE == 10\n" +
			"global.storage_pp_value = 23;\n#else\nglobal.storage_pp_value = -1;\n#endif\n"),
	})
	if value, ok := session.Services.preprocessor.Get("kirikiriz"); !ok || value != 1 {
		t.Fatalf("Services kirikiriz = %d, %v; want 1, true", value, ok)
	}
	if _, err := session.ExecuteStorage("define.tjs"); err != nil {
		t.Fatal(err)
	}
	defineSource := "#define EXEC_DEFINE 10\n"
	if _, err := session.Evaluate("Scripts.exec(" + strconv.Quote(defineSource) + `, "define-inline.tjs")`); err != nil {
		t.Fatal(err)
	}
	if _, err := session.ExecuteStorage("check-storage.tjs"); err != nil {
		t.Fatal(err)
	}
	storageValue, err := session.Evaluate("global.storage_pp_value")
	if err != nil || storageValue.IntegerValue() != 23 {
		t.Fatalf("storage preprocessor result = %v, err=%v; want 23", storageValue, err)
	}

	source := "#if kirikiriz == 1 && CUSTOM_DEFINE == 23 && EXEC_DEFINE == 10\n" +
		"global.source_pp_value = 23 + 1;\n#else\nglobal.source_pp_value = -1;\n#endif\n"
	if _, err := session.executeSource("check-source.tjs", source, false); err != nil {
		t.Fatal(err)
	}
	sourceValue, err := session.Evaluate("global.source_pp_value")
	if err != nil || sourceValue.IntegerValue() != 24 {
		t.Fatalf("source preprocessor result = %v, err=%v; want 24", sourceValue, err)
	}

	evalSource := "#if kirikiriz == 1 && CUSTOM_DEFINE == 23 && EXEC_DEFINE == 10\n" +
		"23 + 10 + 1\n#else\n-1\n#endif"
	evalValue, err := session.Evaluate("Scripts.eval(" + strconv.Quote(evalSource) + `, "persistent-eval.tjs")`)
	if err != nil || evalValue.IntegerValue() != 34 {
		t.Fatalf("eval preprocessor result = %v, err=%v; want 34", evalValue, err)
	}
}

func TestSessionsHaveIndependentPreprocessorState(t *testing.T) {
	first := newScriptsTestSession(t, map[string][]byte{
		"define.tjs": []byte("#define SESSION_ONLY 1\n"),
		"check.tjs": []byte("#if kirikiriz == 1 && SESSION_ONLY == 1\n" +
			"global.session_pp_value = 1;\n#else\nglobal.session_pp_value = 0;\n#endif\n"),
	})
	second := NewSession(first.Services, 1_000_000)
	t.Cleanup(func() {
		if err := second.Close(); err != nil {
			t.Errorf("close second session: %v", err)
		}
	})

	if _, err := first.ExecuteStorage("define.tjs"); err != nil {
		t.Fatal(err)
	}
	if _, err := first.ExecuteStorage("check.tjs"); err != nil {
		t.Fatal(err)
	}
	if _, err := second.ExecuteStorage("check.tjs"); err != nil {
		t.Fatal(err)
	}
	firstValue, err := first.Evaluate("global.session_pp_value")
	if err != nil || firstValue.IntegerValue() != 1 {
		t.Fatalf("first session preprocessor result = %v, err=%v; want 1", firstValue, err)
	}
	secondValue, err := second.Evaluate("global.session_pp_value")
	if err != nil || secondValue.IntegerValue() != 0 {
		t.Fatalf("second session preprocessor result = %v, err=%v; want 0", secondValue, err)
	}
}

func TestScriptsClassNamesAndUnsupportedOperations(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()
	_, err := session.executeSource("<class-names-test>", `var rect = new Rect(); global.native_class_names = Scripts.getClassNames(rect).join(","); global.plain_class_names = Scripts.getClassNames(%[x:1]).join(",");`, false)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"native_class_names": "Rect", "plain_class_names": ""} {
		value, err := session.Evaluate("global." + name)
		if err != nil || value.Text() != want {
			t.Errorf("Scripts.getClassNames %s = %q, err=%v, want %q", name, value.Text(), err, want)
		}
	}
	for _, source := range []string{
		`Scripts.compileStorage("source.tjs", "out.tjs")`,
		`Scripts.dump()`,
		`Scripts.setCallMissing(%[x:1])`,
	} {
		if _, err := session.Evaluate(source); !errors.Is(err, ErrUnsupportedNative) {
			t.Errorf("%s error = %v, want ErrUnsupportedNative", source, err)
		}
	}
	traceValue, err := session.Evaluate(`Scripts.eval("(function inner(){ return Scripts.getTraceString(1); })()", "trace.tjs")`)
	if err != nil || !strings.Contains(traceValue.Text(), "trace.tjs(1)[(function) inner]") {
		t.Fatalf("getTraceString limit result = %q, err=%v", traceValue.Text(), err)
	}
	if _, err := session.Evaluate(`Scripts.compileStorage("source.tjs")`); err == nil || !strings.Contains(err.Error(), "missing argument 1") {
		t.Fatalf("compileStorage argument error = %v", err)
	}
	if _, err := session.Evaluate("Scripts.getClassNames(1)"); err == nil {
		t.Fatal("getClassNames accepted a non-object")
	}
}

func TestScriptsExecLineOffsetIncludesNestedFunctions(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()
	source := "function nested(){\n  return missing_member;\n}\nnested();"
	_, err := session.Evaluate("Scripts.exec(" + strconv.Quote(source) + `, "offset.tjs", 10)`)
	if err == nil || !strings.Contains(err.Error(), "offset.tjs:12:") {
		t.Fatalf("nested execution error = %v, want offset.tjs line 12", err)
	}
}
func TestScriptsExObjectHelpers(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()
	_, err := session.executeSource("<scripts-ex-test>", `
		var object = %[alpha: 1, beta: [2, 3]];
		global.script_ex_keys = Scripts.getObjectKeys(object).join(",");
		global.script_ex_count = Scripts.getObjectCount(object);
		global.script_ex_equal = Scripts.equalStruct(%[x:[1,2]], %[x:[1,2]]);
		global.script_ex_loose = Scripts.equalStructNumericLoose(%[x:1], %[x:1.0]);
		global.script_ex_md5 = Scripts.getMD5HashString(<% 61 62 63 %>);
	`, false)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"script_ex_keys":  "alpha,beta",
		"script_ex_count": "2",
		"script_ex_equal": "1",
		"script_ex_loose": "1",
		"script_ex_md5":   "900150983cd24fb0d6963f7d28e17f72",
	} {
		got, err := session.Evaluate("global." + name)
		if err != nil || got.Text() != want {
			t.Errorf("%s = %q, err=%v, want %q", name, got.Text(), err, want)
		}
	}
}
func TestScriptsExContextsAndPropertyFlags(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()
	_, err := session.Evaluate(`Plugins.link("ScriptsEx.dll");`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = session.executeSource("<scripts-ex-context-test>", `
		var array = [], unbound = function() {};
		global.script_ex_array_context = Scripts.getObjectContext(array) === array;
		global.script_ex_array_null_context = Scripts.isNullContext(array);
		global.script_ex_unbound_null_context = Scripts.isNullContext(unbound);
		global.script_ex_flags = Scripts.pfMemberEnsure + "," + Scripts.pfMemberMustExist + "," + Scripts.pfIgnoreProp + "," + Scripts.pfHiddenMember + "," + Scripts.pfStaticMember;
	`, false)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"script_ex_array_context":        "1",
		"script_ex_array_null_context":   "0",
		"script_ex_unbound_null_context": "1",
		"script_ex_flags":                "512,1024,2048,4096,65536",
	} {
		got, err := session.Evaluate("global." + name)
		if err != nil || got.Text() != want {
			t.Errorf("%s = %q, err=%v; want %q", name, got.Text(), err, want)
		}
	}
}

func TestScriptsForeachSnapshotsEntriesAndForwardsArguments(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()
	_, err := session.executeSource("<scripts-foreach-test>", `
		var object = %[alpha: 1, beta: 2];
		var visited = [];
		var callbackTarget;
		global.script_foreach_result = Scripts.foreach(object, function(key, value, label, target) {
			visited.add(label + ":" + key + "=" + value);
			callbackTarget = target;
			target[key] = value + 10;
			if (key == "alpha") {
				target.gamma = 3;
			}
			if (key == "beta") {
				return value * 10;
			}
		}, "arg", object);
		global.script_foreach_visited = visited.join(",");
		global.script_foreach_target = callbackTarget === object;
	`, false)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"script_foreach_result":  "20",
		"script_foreach_visited": "arg:alpha=1,arg:beta=2",
		"script_foreach_target":  "1",
	} {
		got, err := session.Evaluate("global." + name)
		if err != nil || got.Text() != want {
			t.Errorf("%s = %q, err=%v, want %q", name, got.Text(), err, want)
		}
	}
}
func TestScriptsForeachForwardsAccumulatorArguments(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()
	_, err := session.executeSource("<scripts-foreach-accumulator-test>", `
		var objects = %[layer: %[]];
		var output = [];
		Scripts.foreach(objects, function(name, object, destination, storeSound) {
			destination.add(name + ":" + storeSound);
		}, output, true);
		global.script_foreach_accumulator = output.join(",");
	`, false)
	if err != nil {
		t.Fatal(err)
	}
	got, err := session.Evaluate("global.script_foreach_accumulator")
	if err != nil || got.Text() != "layer:1" {
		t.Fatalf("Scripts.foreach accumulator = %q, err=%v, want %q", got.Text(), err, "layer:1")
	}
}
func TestLegacyObjectConstantBindsCallableScriptFunction(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()
	decoded := &tjs.DecodedBytecode{
		Strings: []string{"", "echo"},
		Objects: []tjs.BytecodeObject{
			{
				Constants: []tjs.BytecodeConstant{{Type: 2, Index: 1, Value: tjs.VoidValue}},
			},
			{
				Name:          1,
				ContextType:   1,
				ArgumentCount: 1,
				Code:          []int16{118, -3, 119},
			},
		},
	}
	global := tjs.ObjectValue(tjs.NewObjectRef(0, 1))
	if err := bindLegacyScriptFunctions(session.VM, decoded, global, session.Services, nil); err != nil {
		t.Fatal(err)
	}
	function := decoded.Objects[0].Constants[0].Value
	globalObject, ok := session.VM.Objects.Lookup(0)
	if !ok {
		t.Fatal("global object missing")
	}
	bound, ok := globalObject.Get("echo")
	if !ok || !bound.StrictEqual(function) {
		t.Fatalf("global echo binding = %v, %v; want callable %v", bound, ok, function)
	}
	got, err := session.VM.InvokeLegacy(function, global, []tjs.Value{tjs.StringValue("argument")}, nil, nil, true)
	if err != nil || got.Text() != "argument" {
		t.Fatalf("legacy function call = %q, %v; want argument", got.Text(), err)
	}
}

func TestLegacyClassConstantBindsClassAndMemberMethod(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()
	decoded := &tjs.DecodedBytecode{
		Strings: []string{"", "LegacyClass", "echo"},
		Objects: []tjs.BytecodeObject{
			{Constants: []tjs.BytecodeConstant{{Type: 2, Index: 1, Value: tjs.VoidValue}}},
			{
				Name:        1,
				ContextType: 6,
				Constants: []tjs.BytecodeConstant{
					{Value: tjs.StringValue("ready")},
					{Value: tjs.IntegerValue(7)},
				},
				Code:         []int16{1, 0, 1, 104, -1, 0, 0, 119},
				MaxVariables: 1,
				Properties:   []tjs.BytecodeProperty{{Name: 2, Object: 2}},
			},
			{
				Name:          2,
				Parent:        1,
				ContextType:   1,
				ArgumentCount: 1,
				Code:          []int16{118, -3, 119},
			},
		},
	}
	global := tjs.ObjectValue(tjs.NewObjectRef(0, 1))
	if err := bindLegacyScriptFunctions(session.VM, decoded, global, session.Services, nil); err != nil {
		t.Fatal(err)
	}
	class := decoded.Objects[0].Constants[0].Value
	if class.Kind() != tjs.KindObject || class.IsNull() {
		t.Fatalf("class constant = %v; want materialized class object", class)
	}
	instance, err := session.VM.Construct(class, nil, session.Services, nil)
	if err != nil {
		t.Fatal(err)
	}
	member, err := session.VM.GetMember(instance, "echo", session.Services, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := session.VM.InvokeLegacy(member, instance, []tjs.Value{tjs.StringValue("class member")}, session.Services, nil, true)
	if err != nil || got.Text() != "class member" {
		t.Fatalf("compiled class member call = %q, %v; want %q", got.Text(), err, "class member")
	}
}
func TestLegacyTopLevelClassWithoutObjectConstantIsGlobalAcrossStorages(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()
	global := tjs.ObjectValue(tjs.NewObjectRef(0, 1))
	newDecoded := func() *tjs.DecodedBytecode {
		return &tjs.DecodedBytecode{
			Strings: []string{"", "SharedLegacyClass"},
			Objects: []tjs.BytecodeObject{{
				Name:        1,
				ContextType: 6,
				Parent:      0,
			}},
		}
	}
	first := newDecoded()
	if err := bindLegacyScriptFunctions(session.VM, first, global, session.Services, nil); err != nil {
		t.Fatal(err)
	}
	firstClass, err := session.VM.GetMember(global, "SharedLegacyClass", session.Services, nil)
	if err != nil || firstClass.Kind() != tjs.KindObject || firstClass.IsNull() {
		t.Fatalf("first global class = %v, %v; want class object", firstClass, err)
	}

	second := newDecoded()
	if err := bindLegacyScriptFunctions(session.VM, second, global, session.Services, nil); err != nil {
		t.Fatal(err)
	}
	secondClass, err := session.VM.GetMember(global, "SharedLegacyClass", session.Services, nil)
	if err != nil || secondClass.Kind() != tjs.KindObject || secondClass.IsNull() {
		t.Fatalf("second global class = %v, %v; want class object", secondClass, err)
	}
	if firstClass.StrictEqual(secondClass) {
		t.Fatal("separate storage class definition reused the previous class object")
	}
	if got, err := session.VM.GetMember(global, "SharedLegacyClass", session.Services, nil); err != nil || !got.StrictEqual(secondClass) {
		t.Fatalf("current global class = %v, %v; want latest class %v", got, err, secondClass)
	}
}

func TestLegacyObjectConstantCallsStringMethod(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()
	decoded := &tjs.DecodedBytecode{
		Strings: []string{"", "lowercase"},
		Objects: []tjs.BytecodeObject{
			{Constants: []tjs.BytecodeConstant{{Type: 2, Index: 1, Value: tjs.VoidValue}}},
			{
				Name:        1,
				ContextType: 1,
				Code: []int16{
					1, 0, 0, // CONST r0, "MiXeD"
					100, 1, 0, 1, 0, // CALLD r1, r0.toLowerCase()
					118, 1, // SRV r1
					119, // RET
				},
				Constants: []tjs.BytecodeConstant{
					{Value: tjs.StringValue("MiXeD")},
					{Value: tjs.StringValue("toLowerCase")},
				},
			},
		},
	}
	global := tjs.ObjectValue(tjs.NewObjectRef(0, 1))
	if err := bindLegacyScriptFunctions(session.VM, decoded, global, session.Services, nil); err != nil {
		t.Fatal(err)
	}
	function := decoded.Objects[0].Constants[0].Value
	got, err := session.VM.InvokeLegacy(function, global, nil, nil, nil, true)
	if err != nil || got.Text() != "mixed" {
		t.Fatalf("legacy String.toLowerCase call = %q, %v; want mixed", got.Text(), err)
	}
}
func TestLegacyClassBindsDeferredSuperclassGetter(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()
	decoded := &tjs.DecodedBytecode{
		Strings: []string{"", "Derived", "Base"},
		Objects: []tjs.BytecodeObject{
			{},
			{
				Name:             1,
				ContextType:      6,
				SuperclassGetter: 2,
			},
			{
				Parent:            1,
				ContextType:       7,
				MaxVariables:      1,
				SuperclassGetters: []int32{0},
				Constants:         []tjs.BytecodeConstant{{Type: 2, Index: 3}},
				Code:              []int16{1, 0, 0, 118, 0, 119},
			},
			{Name: 2, ContextType: 6},
		},
	}
	global := tjs.ObjectValue(tjs.NewObjectRef(0, 1))
	if err := bindLegacyScriptFunctions(session.VM, decoded, global, session.Services, nil); err != nil {
		t.Fatal(err)
	}
	derived := session.VM.Globals["Derived"]
	base := session.VM.Globals["Base"]
	if err := session.VM.RegisterNativeStaticValue(base, "inheritedValue", tjs.IntegerValue(73)); err != nil {
		t.Fatal(err)
	}
	var budget uint64 = 100_000
	got, found, err := session.VM.GetMemberIfPresent(derived, "inheritedValue", session.Services, &budget)
	if err != nil || !found || !got.StrictEqual(tjs.IntegerValue(73)) {
		t.Fatalf("derived inherited value = %v, found=%t, err=%v; want 73", got, found, err)
	}
}
func TestScriptsStorageExecutesModeOneTextStream(t *testing.T) {
	source := "global.mode_one_script_value=31;"
	data, err := assets.EncodeUnits(utf16.Encode([]rune(source)), "c1")
	if err != nil {
		t.Fatal(err)
	}
	session := newScriptsTestSession(t, map[string][]byte{"mode-one.tjs": data})
	if _, err := session.Evaluate(`Scripts.execStorage("mode-one.tjs")`); err != nil {
		t.Fatal(err)
	}
	value, err := session.Evaluate("global.mode_one_script_value")
	if err != nil {
		t.Fatal(err)
	}
	if n, err := value.Int(); err != nil || n != 31 {
		t.Fatalf("mode-1 TextStream script result = %v, err=%v", value, err)
	}
}
