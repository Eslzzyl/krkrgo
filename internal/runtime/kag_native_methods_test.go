package runtime

import (
	"errors"
	"strings"
	"testing"

	"github.com/eslzzyl/krkrgo/internal/tjs"
)

func TestKAGNativeBaseConstructorUsesCurrentDerivedReceiverWhenCached(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()

	source := `
class BaseConductor extends KAGParser {
	function BaseConductor() {
		var ctor;
		if (typeof global.BaseConductor.SuperConstructor != "Object") {
			ctor = super.KAGParser;
			global.BaseConductor.SuperConstructor = ctor;
		} else {
			ctor = global.BaseConductor.SuperConstructor;
		}
		ctor(...);
	}
}
class AnimationConductor extends BaseConductor {
	function AnimationConductor() { super.BaseConductor(...); }
}
first = new AnimationConductor();
invalidate first;
second = new AnimationConductor();
`
	if _, err := session.executeSource("<kag-native-cached-base-constructor>", source, false); err != nil {
		t.Fatal(err)
	}
	value, err := session.Evaluate(`second.curStorage == ""`)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := value.Truth(); !ok {
		t.Fatalf("cached KAGParser base constructor did not initialize the new derived receiver: %v", value)
	}
	if len(session.Services.KAGParsers) != 1 {
		t.Fatalf("live KAGParser contexts = %d, want only the second instance", len(session.Services.KAGParsers))
	}
}

func TestKAGNativeClearResetsParserFramesAndKeepsMacroDefinitions(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()

	source := `Plugins.link("plugin/KAGParser.dll");
	p = new KAGParser();
	p.loadScenario("clear.ks", "[macro name=hello][first][second][endmacro]\n*entry\n[hello value=old]\n*sub\n[sub]\n");
p.ignoreCR = true;
	first = p.getNextTag();
	p.callLabel("*sub");
	depthBeforeClear = p.callStackDepth;
	p.clear();
	cleared = p.curStorage == "" && p.curLabel == "" && p.callStackDepth == 0 && p.mp === null && p.getNextTag() === void;
	p.loadScenario("again.ks", "[hello value=again]");
	again = p.getNextTag();
	macroDefinitionsKept = again.tagname == "first" && p.mp.value == "again";`
	if _, err := session.executeSource("<kag-native-clear-test>", source, false); err != nil {
		t.Fatal(err)
	}
	value, err := session.Evaluate(`first.tagname == "first" && depthBeforeClear == 1 && cleared && macroDefinitionsKept`)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := value.Truth(); !ok {
		detail, _ := session.Evaluate(`[first.tagname,depthBeforeClear,cleared,again.tagname,macroDefinitionsKept].join("|")`)
		t.Fatalf("KAGParser.clear did not reset parser state or preserve macro definitions: %v (%v)", value, detail)
	}
}

func TestKAGNativeGetNextTagSkipsStructuralNewlinesBeforeTags(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()

	source := `Plugins.link("plugin/KAGParser.dll");
p = new KAGParser();
p.loadScenario("lines.ks", "first\n[hitret]\n[after]\n");
chars = "";
sawText = false;
trailingBreaks = 0;
for (;;) {
	e = p.getNextTag();
	if (e === void) break;
	if (e.tagname == "ch") {
		chars += e.text;
		sawText = true;
		continue;
	}
	if (e.tagname == "r" && !sawText) continue;
	if (e.tagname == "r") trailingBreaks++;
	hitret = e;
	break;
}
after = p.getNextTag();`
	if _, err := session.executeSource("<kag-native-newline-test>", source, false); err != nil {
		t.Fatal(err)
	}
	value, err := session.Evaluate(`chars == "first" && trailingBreaks == 0 && hitret.tagname == "hitret" && after.tagname == "after"`)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := value.Truth(); !ok {
		detail, _ := session.Evaluate(`[chars, hitret.tagname, after.tagname].join("|")`)
		t.Fatalf("legacy KAG parser exposed line-ending newlines before tags: %v (%v)", value, detail)
	}
}

func TestKAGNativeGetNextTagExpandsEmbeddedKAGText(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()

	source := `Plugins.link("plugin/KAGParser.dll");
p = new KAGParser();
p.loadScenario("embedded.ks", "[emb exp=\"'[foo a=1]'\" escape=false]\n[after]");
first = p.getNextTag();
second = p.getNextTag();`
	if _, err := session.executeSource("<kag-native-embedded-tag-test>", source, false); err != nil {
		t.Fatal(err)
	}
	value, err := session.Evaluate(`first.tagname == "foo" && first.a == "1" && second.tagname == "after"`)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := value.Truth(); !ok {
		detail, _ := session.Evaluate(`[first.tagname,first.a,second.tagname].join("|")`)
		t.Fatalf("embedded KAG text was not parsed as a tag: %v (%v)", value, detail)
	}
}

func TestKAGNativeGoToLabelVoidIsNoOp(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()

	source := `Plugins.link("plugin/KAGParser.dll");
p = new KAGParser();
p.loadScenario("title.ks", "*start\n[first]\n*next\n[second]\n");
p.goToLabel("*next");
before = p.curLabel;
p.goToLabel(void);
after = p.curLabel;
next = p.getNextTag();`
	if _, err := session.executeSource("<kag-native-empty-label-test>", source, false); err != nil {
		t.Fatal(err)
	}
	value, err := session.Evaluate(`before == "*next" && after == before && next.tagname == "second"`)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := value.Truth(); !ok {
		detail, _ := session.Evaluate(`[before, after, next.tagname].join("|")`)
		t.Fatalf("goToLabel(void) changed KAG parser position: %v (%v)", value, detail)
	}
}

func TestKAGNativeEmptyOptionalPositionIsVoid(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()

	source := `Plugins.link("plugin/KAGParser.dll");
p = new KAGParser();
p.loadScenario("position.ks", "[macro name=draw]\n[ImageDraw file=%file pos=%pos]\n[endmacro]\n[draw file=EV_A]\n[ImageDraw file=EV_B pos= layer=]\n[ImageDraw file=EV_C pos=mouth]\n");
p.ignoreCR = true;
macroImage = p.getNextTag();
directImage = p.getNextTag();
explicitImage = p.getNextTag();`
	if _, err := session.executeSource("<kag-native-position-test>", source, false); err != nil {
		t.Fatal(err)
	}
	value, err := session.Evaluate(`macroImage.tagname == "imagedraw" && macroImage.file == "EV_A" && macroImage.pos === void && directImage.tagname == "imagedraw" && directImage.file == "EV_B" && directImage.pos === void && directImage.layer == "" && explicitImage.tagname == "imagedraw" && explicitImage.file == "EV_C" && explicitImage.pos == "mouth"`)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := value.Truth(); !ok {
		detail, _ := session.Evaluate(`[macroImage.tagname, macroImage.file, typeof macroImage.pos, directImage.tagname, directImage.file, typeof directImage.pos, directImage.layer, explicitImage.tagname, explicitImage.file, explicitImage.pos].join("|")`)
		t.Fatalf("optional KAG position values = %v (%v)", value, detail)
	}
}

func TestKAGNativePopMacroArgsAndUnderflow(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()

	source := `Plugins.link("plugin/KAGParser.dll");
	p = new KAGParser();
	p.loadScenario("pop.ks", "[macro name=hello][first][second][endmacro][hello value=ok][outside]");
	first = p.getNextTag();
	p.popMacroArgs();
	macroParamsCleared = p.mp === null && p.macroParams === null;
	afterPop = p.getNextTag();`
	if _, err := session.executeSource("<kag-native-pop-macro-args-test>", source, false); err != nil {
		t.Fatal(err)
	}
	value, err := session.Evaluate(`first.tagname == "first" && macroParamsCleared && afterPop.tagname == "outside"`)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := value.Truth(); !ok {
		t.Fatal("KAGParser.popMacroArgs did not remove the active macro argument frame")
	}
	if _, err := session.executeSource("<kag-native-pop-macro-args-underflow>", `empty = new KAGParser(); empty.popMacroArgs()`, false); err == nil || !strings.Contains(err.Error(), "KAG macro argument stack underflow") {
		t.Fatalf("popMacroArgs underflow error = %v, want KAG macro argument stack underflow", err)
	}
}

func TestKAGNativeAssignCopiesParserStateWithoutReplacingTarget(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()

	source := `Plugins.link("plugin/KAGParser.dll");
	p = new KAGParser();
	q = new KAGParser();
	p.loadScenario("target.ks", "target");
	pRef = p;
	p.marker = new Dictionary();
	marker = p.marker;
	p.ignoreCR = false;
	p.processSpecialTags = false;
	p.multiLineTagEnabled = true;
	p.debugLevel = 3;
	p.interrupt();
	q.loadScenario("source.ks", "[macro name=hello][first][second][endmacro][hello value=source][outside]");
	sourceFirst = q.getNextTag();
	q.ignoreCR = true;
	q.processSpecialTags = true;
	q.multiLineTagEnabled = false;
	q.debugLevel = 7;`
	if _, err := session.executeSource("<kag-native-assign-setup>", source, false); err != nil {
		t.Fatal(err)
	}

	target := session.VM.Globals["p"]
	from := session.VM.Globals["q"]
	budget := uint64(0)
	if _, err := session.Services.kagNativeCall(session.VM, "KAGParser.assign", target, []tjs.Value{from}, &budget); !errors.Is(err, ErrKAGExecutionBudget) {
		t.Fatalf("assign with exhausted budget error = %v, want %v", err, ErrKAGExecutionBudget)
	}
	value, err := session.Evaluate(`p.curStorage == "target.ks" && pRef === p`)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := value.Truth(); !ok {
		t.Fatal("budget-exhausted assign changed the target parser")
	}

	assign := `p.assign(q);
	qSecond = q.getNextTag();
	pInterrupt = p.getNextTag();
	p.resetInterrupt();
	pSecond = p.getNextTag();`
	if _, err := session.executeSource("<kag-native-assign-test>", assign, false); err != nil {
		t.Fatal(err)
	}
	value, err = session.Evaluate(`pRef === p && p.marker === marker && p.curStorage == "source.ks" && p.ignoreCR == 1 && p.processSpecialTags == 0 && p.multiLineTagEnabled == 1 && p.debugLevel == 7 && p.mp.value == "source" && qSecond.tagname == "second" && pInterrupt.tagname == "interrupt" && pSecond.tagname == "second"`)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := value.Truth(); !ok {
		t.Fatal("KAGParser.assign failed to copy source state independently or preserve target identity/options")
	}
}

func TestKAGNativeAssignValidatesSource(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()

	if _, err := session.executeSource("<kag-native-assign-source-validation>", `Plugins.link("plugin/KAGParser.dll"); p = new KAGParser(); p.assign(null)`, false); err == nil || !strings.Contains(err.Error(), "KAGParser.assign requires non-null source") {
		t.Fatalf("assign(null) error = %v, want non-null source error", err)
	}
	if _, err := session.executeSource("<kag-native-assign-wrong-source>", `p.assign(new Dictionary())`, false); err == nil || !strings.Contains(err.Error(), "KAGParser.assign requires a KAGParser") {
		t.Fatalf("assign(non-parser) error = %v, want KAGParser source error", err)
	}
}

func TestKAGNativeAssignSelfClearsMacrosOnly(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()

	source := `Plugins.link("plugin/KAGParser.dll");
p = new KAGParser();
p.loadScenario("self.ks", "[macro name=hello][first][endmacro]\n*entry\nbody\n*sub\n");
p.callLabel("*sub");
depthBeforeSelfAssign = p.callStackDepth;
p.assign(p);
selfStateKept = p.curStorage == "self.ks" && p.callStackDepth == 1;
p.loadScenario("after-self.ks", "[hello value=not-a-macro]");
afterSelfAssign = p.getNextTag();`
	if _, err := session.executeSource("<kag-native-assign-self-test>", source, false); err != nil {
		t.Fatal(err)
	}
	value, err := session.Evaluate(`depthBeforeSelfAssign == 1 && selfStateKept && afterSelfAssign.tagname == "hello" && p.mp === null`)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := value.Truth(); !ok {
		t.Fatal("self-assign changed parser state or retained macro definitions")
	}
}
func TestKAGConductorSnapshotPreservesOnlyPresentQueues(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()
	vm, host := session.VM, session.Services
	budget := uint64(10_000)

	context, err := vm.NewDictionary()
	if err != nil {
		t.Fatal(err)
	}
	state, err := vm.NewDictionary()
	if err != nil {
		t.Fatal(err)
	}
	pending, err := vm.NewArray([]tjs.Value{tjs.StringValue("queued")})
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.SetMemberWithRuntime(context, "pendings", pending, 0, host, &budget); err != nil {
		t.Fatal(err)
	}
	if err := storeKAGConductorQueues(vm, context, state, host, &budget); err != nil {
		t.Fatalf("store sparse conductor queues: %v", err)
	}

	savedQueues, exists, err := vm.GetMemberIfPresent(state, kagConductorQueuesMember, host, &budget)
	if err != nil || !exists {
		t.Fatalf("saved queue state = %v, %t, %v", savedQueues, exists, err)
	}
	if _, exists, err := vm.GetMemberIfPresent(savedQueues, "fasttags", host, &budget); err != nil || exists {
		t.Fatalf("saved fasttags = %t, %v; want omitted", exists, err)
	}
	extraQueue, err := vm.NewArray([]tjs.Value{tjs.StringValue("unrecognized by this conductor")})
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.SetMemberWithRuntime(savedQueues, "fasttags", extraQueue, 0, host, &budget); err != nil {
		t.Fatal(err)
	}

	restoredContext, err := vm.NewDictionary()
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := vm.NewArray([]tjs.Value{tjs.StringValue("replacement")})
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.SetMemberWithRuntime(restoredContext, "pendings", replacement, 0, host, &budget); err != nil {
		t.Fatal(err)
	}
	if err := restoreKAGConductorQueues(vm, restoredContext, state, host, &budget); err != nil {
		t.Fatalf("restore sparse conductor queues: %v", err)
	}
	got, exists, err := vm.GetMemberIfPresent(restoredContext, "pendings", host, &budget)
	if err != nil || !exists {
		t.Fatalf("restored pendings = %v, %t, %v", got, exists, err)
	}
	items, err := vm.ArrayItems(got)
	if err != nil || len(items) != 1 || items[0].Text() != "queued" {
		t.Fatalf("restored pendings items = %v, %v; want [queued]", items, err)
	}
	if _, exists, err := vm.GetMemberIfPresent(restoredContext, "fasttags", host, &budget); err != nil || exists {
		t.Fatalf("restored fasttags = %t, %v; want omitted", exists, err)
	}
}
