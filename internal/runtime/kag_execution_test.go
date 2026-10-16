package runtime

import (
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/eslzzyl/krkrgo/internal/kag"
	"github.com/eslzzyl/krkrgo/internal/tjs"
)

func TestKAGExecutionTextTagOrder(t *testing.T) {
	parser, err := NewKAGParser("order.ks", "hello[tag x=1]\n@foo y=2\n@end\n")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	parser.SetCallbacks(&KAGCallbacks{
		OnText: func(_ *KAGParser, text string) error { got = append(got, "text:"+text); return nil },
		OnTag:  func(_ *KAGParser, tag *kag.Tag) error { got = append(got, "tag:"+tag.Name); return nil },
	})
	budget := uint64(100)
	if _, err := parser.Run(&budget); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"text:hello", "tag:tag", "tag:foo"}) {
		t.Fatalf("events=%v", got)
	}
}

func TestKAGExecutionRecognizesIndentedAnimationWaits(t *testing.T) {
	parser, err := NewKAGParser("animation.asd", "*skip\n\t@wait time=200\n@jump target=*skip\n")
	if err != nil {
		t.Fatal(err)
	}
	budget := uint64(100)
	if event, err := parser.Step(&budget); err != nil {
		t.Fatal(err)
	} else if event.Kind != KAGEventLabel {
		t.Fatalf("initial step = %#v, want label", event)
	}
	for i := range 3 {
		event, err := parser.Step(&budget)
		if err != nil {
			t.Fatal(err)
		}
		if i == 1 {
			if event.Kind != KAGEventLabel {
				t.Fatalf("loop step = %#v, want jump target label", event)
			}
			continue
		}
		if event.Kind != KAGEventTag || event.Tag == nil || event.Tag.Name != "wait" {
			t.Fatalf("step %d = %#v, want wait tag", i, event)
		}
	}
}

func TestKAGExecutionConditionalBranches(t *testing.T) {
	parser, err := NewKAGParser("branch.ks", "@if exp=\"false\"\nno\n@elsif exp=\"1\"\nyes\n@else\nno2\n@endif\n@end\n")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	parser.SetTextCallback(func(_ *KAGParser, text string) error { got = append(got, text); return nil })
	budget := uint64(100)
	if _, err := parser.Run(&budget); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"yes"}) {
		t.Fatalf("text=%v", got)
	}
}

func TestKAGExecutionCallReturn(t *testing.T) {
	parser, err := NewKAGParser("call.ks", "@call target=\"sub\"\nmain\n@end\n*sub\nsub\n@return\n")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	parser.SetTextCallback(func(_ *KAGParser, text string) error { got = append(got, text); return nil })
	budget := uint64(100)
	if _, err := parser.Run(&budget); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"sub", "main"}) {
		t.Fatalf("text=%v", got)
	}
}

func TestKAGExecutionCrossScenarioCallReturn(t *testing.T) {
	parser, err := NewKAGParser("caller.ks", `
[caller-before]
caller before
[call storage=callee.ks target=*entry]
[caller-after]
caller after
[end]
`)
	if err != nil {
		t.Fatal(err)
	}
	type observedEvent struct {
		storage       string
		kind          string
		value         string
		callFrames    int
		parserStorage string
	}
	var got []observedEvent
	var loaded []string
	var loadedFrameVisible bool
	parser.SetCallbacks(&KAGCallbacks{
		OnScenarioLoad: func(p *KAGParser, storage, source string) (string, error) {
			if p.StorageName() != "caller.ks" || source != "" {
				t.Errorf("scenario load from %q with source %q; want caller.ks and empty source", p.StorageName(), source)
			}
			loaded = append(loaded, storage)
			return `
*entry
[callee-entry]
callee body
[return]
`, nil
		},
		OnScenarioLoaded: func(p *KAGParser, storage string) error {
			state := p.Store()
			if storage != "callee.ks" || p.StorageName() != "callee.ks" {
				t.Errorf("scenario loaded as %q with active storage %q; want callee.ks", storage, p.StorageName())
			}
			if len(state.CrossScenarioCalls) != 1 || state.CrossScenarioCalls[0].Scenario.Storage != "caller.ks" {
				t.Errorf("cross-scenario frames in OnScenarioLoaded = %#v; want caller.ks frame", state.CrossScenarioCalls)
			} else {
				lines := state.CrossScenarioCalls[0].Scenario.SourceLines
				if len(lines) < 3 || lines[2] != "caller before" {
					t.Errorf("cloned caller source lines = %#v; want physical line 3 preserved", lines)
				}
				loadedFrameVisible = true
			}
			return nil
		},
		OnEvent: func(p *KAGParser, event KAGEvent) error {
			observed := observedEvent{
				storage:       event.Location.Storage,
				parserStorage: p.StorageName(),
				callFrames:    len(p.Store().CrossScenarioCalls),
			}
			switch event.Kind {
			case KAGEventTag:
				observed.kind = "tag"
				observed.value = event.Tag.Name
			case KAGEventText:
				observed.kind = "text"
				observed.value = event.Text
			default:
				return nil
			}
			got = append(got, observed)
			return nil
		},
	})
	budget := uint64(1_000)
	if _, err := parser.Run(&budget); err != nil {
		t.Fatal(err)
	}
	want := []observedEvent{
		{storage: "caller.ks", kind: "tag", value: "caller-before", parserStorage: "caller.ks"},
		{storage: "caller.ks", kind: "text", value: "caller before", parserStorage: "caller.ks"},
		{storage: "callee.ks", kind: "tag", value: "callee-entry", callFrames: 1, parserStorage: "callee.ks"},
		{storage: "callee.ks", kind: "text", value: "callee body", callFrames: 1, parserStorage: "callee.ks"},
		{storage: "caller.ks", kind: "tag", value: "caller-after", parserStorage: "caller.ks"},
		{storage: "caller.ks", kind: "text", value: "caller after", parserStorage: "caller.ks"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("visible events = %#v, want %#v", got, want)
	}
	if !reflect.DeepEqual(loaded, []string{"callee.ks"}) {
		t.Fatalf("loaded storages = %v, want [callee.ks]", loaded)
	}
	if !loadedFrameVisible {
		t.Fatal("OnScenarioLoaded could not observe the pending caller frame")
	}
	state := parser.Store()
	if parser.StorageName() != "caller.ks" || state.Storage != "caller.ks" || len(state.CrossScenarioCalls) != 0 {
		t.Fatalf("parser after cross-scenario return = storage %q, state %#v; want caller.ks with no pending calls", parser.StorageName(), state)
	}
}

func TestKAGExecutionCrossScenarioCallWithoutTargetStartsAtFirstToken(t *testing.T) {
	parser, err := NewKAGParser("caller.ks", "[call storage=callee.ks]\ncaller after\n[end]\n")
	if err != nil {
		t.Fatal(err)
	}
	parser.SetScenarioLoadCallback(func(_ *KAGParser, storage, source string) (string, error) {
		if storage != "callee.ks" || source != "" {
			t.Errorf("scenario load = %q, %q; want callee.ks and empty source", storage, source)
		}
		return "callee first token\n[return]\n", nil
	})
	var text []string
	parser.SetTextCallback(func(_ *KAGParser, value string) error {
		text = append(text, value)
		return nil
	})
	if _, err := parser.Run(nil); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(text, []string{"callee first token", "caller after"}) {
		t.Fatalf("visible text = %v, want [callee first token caller after]", text)
	}
	if parser.StorageName() != "caller.ks" || len(parser.Store().CrossScenarioCalls) != 0 {
		t.Fatalf("state after cross-scenario return = storage %q, frames %#v; want caller.ks and no pending calls", parser.StorageName(), parser.Store().CrossScenarioCalls)
	}
}

func TestKAGReturnFromCallRestoresCrossScenarioFrame(t *testing.T) {
	parser, err := NewKAGParser("caller.ks", "@call storage=callee.ks target=*entry\ncaller after\n")
	if err != nil {
		t.Fatal(err)
	}
	parser.SetScenarioLoadCallback(func(_ *KAGParser, storage, source string) (string, error) {
		if storage != "callee.ks" || source != "" {
			t.Fatalf("scenario load = %q, %q; want callee.ks and empty source", storage, source)
		}
		return "*entry\ncallee body\n", nil
	})

	if _, err := parser.Step(nil); err != nil {
		t.Fatal(err)
	}
	if parser.StorageName() != "callee.ks" || parser.kagCallDepth() != 1 {
		t.Fatalf("state before native return = storage %q, depth %d; want callee.ks and 1", parser.StorageName(), parser.kagCallDepth())
	}
	if err := parser.ReturnFromCall(); err != nil {
		t.Fatal(err)
	}
	if parser.StorageName() != "caller.ks" || parser.kagCallDepth() != 0 {
		t.Fatalf("state after native return = storage %q, depth %d; want caller.ks and 0", parser.StorageName(), parser.kagCallDepth())
	}
	event, err := parser.Step(nil)
	if err != nil || event.Kind != KAGEventText || event.Text != "caller after" {
		t.Fatalf("caller event = %#v, %v; want caller after text", event, err)
	}
}

func TestKAGCallLabelFrameSurvivesScenarioLoad(t *testing.T) {
	parser, err := NewKAGParser("caller.ks", "*start\ncaller after\n*sub\ncallee body\n")
	if err != nil {
		t.Fatal(err)
	}
	if err := parser.CallLabel("*sub"); err != nil {
		t.Fatal(err)
	}
	if err := parser.LoadScenario("callee.ks", "*entry\ncallee body\n"); err != nil {
		t.Fatal(err)
	}
	if parser.StorageName() != "callee.ks" || parser.kagCallDepth() != 1 {
		t.Fatalf("state before return = storage %q, depth %d; want callee.ks and 1", parser.StorageName(), parser.kagCallDepth())
	}
	if err := parser.ReturnFromCall(); err != nil {
		t.Fatal(err)
	}
	if parser.StorageName() != "caller.ks" || parser.kagCallDepth() != 0 {
		t.Fatalf("state after return = storage %q, depth %d; want caller.ks and 0", parser.StorageName(), parser.kagCallDepth())
	}
}

func TestKAGCallLabelLogoCautionReturnStack(t *testing.T) {
	parser, err := NewKAGParser("conductor.ks", "caller after\n")
	if err != nil {
		t.Fatal(err)
	}
	if err := parser.CallLabel(""); err != nil {
		t.Fatal(err)
	}
	const custom = `*logo
@call target=*caution
logo resumed
@return
*caution
caution body
@return
`
	if err := parser.LoadScenario("custom.ks", custom); err != nil {
		t.Fatal(err)
	}
	if err := parser.Jump("*logo"); err != nil {
		t.Fatal(err)
	}
	var text []string
	for range 20 {
		event, err := parser.Step(nil)
		if err != nil {
			t.Fatal(err)
		}
		if event.Kind == KAGEventLabel {
			depth := parser.kagCallDepth()
			if event.Label.Name == "*caution" && (parser.StorageName() != "custom.ks" || parser.CurrentLabel() != "*caution" || depth != 2) {
				t.Fatalf("caution stack = storage %q, label %q, depth %d; want custom.ks, *caution, 2", parser.StorageName(), parser.CurrentLabel(), depth)
			}
			if event.Label.Name == "*logo" && (parser.StorageName() != "custom.ks" || parser.CurrentLabel() != "*logo" || depth != 1) {
				t.Fatalf("logo stack = storage %q, label %q, depth %d; want custom.ks, *logo, 1", parser.StorageName(), parser.CurrentLabel(), depth)
			}
		}
		if event.Kind == KAGEventText {
			text = append(text, event.Text)
			if event.Text == "caller after" && (parser.StorageName() != "conductor.ks" || parser.kagCallDepth() != 0) {
				t.Fatalf("state after logo return = storage %q, depth %d; want conductor.ks and 0", parser.StorageName(), parser.kagCallDepth())
			}
		}
		if event.Kind == KAGEventEnd {
			break
		}
	}
	if !reflect.DeepEqual(text, []string{"caution body", "logo resumed", "caller after"}) {
		t.Fatalf("text execution = %v; want caution body, logo resumed, caller after", text)
	}
}

func TestKAGExecutionCallWithoutStorageAndTargetFails(t *testing.T) {
	parser, err := NewKAGParser("missing-call-target.ks", "[call]\n[end]\n")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parser.Step(nil); err == nil || !strings.Contains(err.Error(), "KAG call missing target") {
		t.Fatalf("Step error = %v, want missing-target error", err)
	}
}

func TestKAGExecutionExplicitReturnConsumesLocalCallFrame(t *testing.T) {
	parser, err := NewKAGParser("explicit-local-return.ks", `@call target="sub"
caller
@end
*sub
sub
@return target="*alternate"
*alternate
alternate
@end
`)
	if err != nil {
		t.Fatal(err)
	}
	var text []string
	parser.SetTextCallback(func(p *KAGParser, value string) error {
		text = append(text, value)
		if value == "alternate" && p.kagCallDepth() != 0 {
			t.Errorf("call depth at explicit return target = %d, want 0", p.kagCallDepth())
		}
		return nil
	})
	if _, err := parser.Run(nil); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(text, []string{"sub", "alternate"}) {
		t.Fatalf("visible text = %v, want [sub alternate]", text)
	}
}

func TestKAGExecutionExplicitStorageReturnConsumesCrossScenarioFrame(t *testing.T) {
	parser, err := NewKAGParser("explicit-cross-return-caller.ks", `@call storage="callee.ks" target="*entry"
caller
@end
`)
	if err != nil {
		t.Fatal(err)
	}
	parser.SetScenarioLoadCallback(func(_ *KAGParser, storage, source string) (string, error) {
		if source != "" {
			t.Errorf("scenario load source = %q, want empty", source)
		}
		switch storage {
		case "callee.ks":
			return "*entry\n@return storage=\"return-target.ks\" target=\"*target\"\n", nil
		case "return-target.ks":
			return "*target\nreturned\n@end\n", nil
		default:
			return "", errors.New("unexpected scenario " + storage)
		}
	})
	var text []string
	parser.SetTextCallback(func(p *KAGParser, value string) error {
		text = append(text, value)
		if value == "returned" {
			state := p.Store()
			if p.StorageName() != "return-target.ks" || len(state.CrossScenarioCalls) != 0 {
				t.Errorf("state at explicit return target = storage %q, cross calls %#v; want return-target.ks with no pending frame", p.StorageName(), state.CrossScenarioCalls)
			}
		}
		return nil
	})
	if _, err := parser.Run(nil); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(text, []string{"returned"}) {
		t.Fatalf("visible text = %v, want [returned]", text)
	}
	if parser.StorageName() != "return-target.ks" || len(parser.Store().CrossScenarioCalls) != 0 {
		t.Fatalf("final parser state = storage %q, cross calls %#v; want return-target.ks with no pending frame", parser.StorageName(), parser.Store().CrossScenarioCalls)
	}
}

func TestKAGExecutionCrossScenarioCallLoadedFailureRestoresCaller(t *testing.T) {
	parser, err := NewKAGParser("failed-call-caller.ks", `@call storage="callee.ks" target="*entry"
recovered
@end
`)
	if err != nil {
		t.Fatal(err)
	}
	loadFailure := errors.New("loaded callback failed")
	var loadedFrameVisible bool
	parser.SetCallbacks(&KAGCallbacks{
		OnScenarioLoad: func(_ *KAGParser, storage, source string) (string, error) {
			if storage != "callee.ks" || source != "" {
				t.Errorf("scenario load = %q, %q; want callee.ks and empty source", storage, source)
			}
			return "*entry\n@end\n", nil
		},
		OnScenarioLoaded: func(p *KAGParser, storage string) error {
			state := p.Store()
			if storage != "callee.ks" || len(state.CrossScenarioCalls) != 1 || state.CrossScenarioCalls[0].Scenario.Storage != "failed-call-caller.ks" {
				t.Errorf("OnScenarioLoaded state = storage %q, cross calls %#v; want pending caller frame", p.StorageName(), state.CrossScenarioCalls)
			} else {
				loadedFrameVisible = true
			}
			return loadFailure
		},
	})
	if _, err := parser.Step(nil); !errors.Is(err, loadFailure) {
		t.Fatalf("Step error = %v, want loaded callback error", err)
	}
	if !loadedFrameVisible {
		t.Fatal("OnScenarioLoaded could not observe the pending caller frame")
	}
	state := parser.Store()
	if parser.StorageName() != "failed-call-caller.ks" || state.Storage != "failed-call-caller.ks" || len(state.CrossScenarioCalls) != 0 {
		t.Fatalf("state after failed load = storage %q, %#v; want restored caller and no pending frame", parser.StorageName(), state)
	}
	event, err := parser.Step(nil)
	if err != nil {
		t.Fatal(err)
	}
	if event.Kind != KAGEventText || event.Text != "recovered" {
		t.Fatalf("event after failed load = %#v, want recovered text", event)
	}
}

func TestKAGParserCallLabelEnforcesTotalCallDepth(t *testing.T) {
	parser, err := NewKAGParser("current.ks", "*target\n")
	if err != nil {
		t.Fatal(err)
	}
	callerScenario, err := kag.Parse("caller.ks", "*return\n")
	if err != nil {
		t.Fatal(err)
	}
	state := parser.Store()
	state.CrossScenarioCalls = make([]KAGScenarioCallState, 127)
	for i := range state.CrossScenarioCalls {
		state.CrossScenarioCalls[i] = KAGScenarioCallState{
			Scenario: callerScenario,
			State:    KAGState{Storage: "caller.ks"},
		}
	}
	if err := parser.RestoreState(state); err != nil {
		t.Fatal(err)
	}
	if got := parser.kagCallDepth(); got != 127 {
		t.Fatalf("initial call depth = %d, want 127", got)
	}
	if err := parser.CallLabel("*target"); err != nil {
		t.Fatal(err)
	}
	if got := parser.kagCallDepth(); got != 128 {
		t.Fatalf("call depth after CallLabel = %d, want 128", got)
	}
	if err := parser.Call("*target"); err == nil || !strings.Contains(err.Error(), "call depth exceeded") {
		t.Fatalf("Call at total depth 128 error = %v, want depth-exceeded error", err)
	}
}

func TestKAGExecutionStorageJumpLoadsScenario(t *testing.T) {
	parser, err := NewKAGParser("first.ks", "@jump storage=\"0.ks\" target=\"*1\"\n")
	if err != nil {
		t.Fatal(err)
	}
	parser.SetScenarioLoadCallback(func(_ *KAGParser, storage, source string) (string, error) {
		if storage != "0.ks" || source != "" {
			t.Errorf("scenario load = %q, %q", storage, source)
		}
		return "*1|\n*|\n@title\n@end\n", nil
	})
	budget := uint64(100)
	seenTarget := false
	var event KAGEvent
	for step := 0; step < 4; step++ {
		event, err = parser.Step(&budget)
		if err != nil {
			t.Fatal(err)
		}
		if event.Kind == KAGEventLabel && event.Label != nil && event.Label.Name == "*1" {
			seenTarget = true
		}
		if event.Kind == KAGEventTag {
			break
		}
	}
	if !seenTarget {
		t.Fatal("storage jump did not reach target label")
	}
	if event.Kind != KAGEventTag || event.Tag == nil || event.Tag.Name != "title" {
		t.Fatalf("event = %#v, want title tag", event)
	}
	if parser.StorageName() != "0.ks" {
		t.Fatalf("storage = %q, want 0.ks", parser.StorageName())
	}
}

func TestKAGExecutionBudgetAndInvalidation(t *testing.T) {
	parser, err := NewKAGParser("budget.ks", "one\ntwo\n")
	if err != nil {
		t.Fatal(err)
	}
	budget := uint64(1)
	if _, err := parser.Step(&budget); err != nil {
		t.Fatal(err)
	}
	if _, err := parser.Step(&budget); !errors.Is(err, ErrKAGExecutionBudget) {
		t.Fatalf("budget error=%v", err)
	}
	parser, err = NewKAGParser("invalidate.ks", "one\n")
	if err != nil {
		t.Fatal(err)
	}
	parser.SetTextCallback(func(p *KAGParser, _ string) error { p.Invalidate(); return nil })
	budget = 10
	if _, err := parser.Step(&budget); !errors.Is(err, ErrKAGParserInvalid) {
		t.Fatalf("invalidation error=%v", err)
	}
}

func TestKAGExecutionStateRestoresCondition(t *testing.T) {
	parser, err := NewKAGParser("state.ks", "@if exp=\"true\"\nvisible\n@endif\n@end\n")
	if err != nil {
		t.Fatal(err)
	}
	budget := uint64(100)
	state := parser.Store()
	if _, err := parser.Step(&budget); err != nil {
		t.Fatal(err)
	}
	if err := parser.RestoreState(state); err != nil {
		t.Fatal(err)
	}
	event, err := parser.Step(&budget)
	if err != nil {
		t.Fatal(err)
	}
	if event.Kind != KAGEventText || event.Text != "visible" {
		t.Fatalf("event=%#v", event)
	}
}

func TestKAGExecutionIgnoreSuppressesAndRestores(t *testing.T) {
	parser, err := NewKAGParser("ignore.ks", "before\n@ignore\nhidden [tag x=1]\n@ignore\nnested\n@endignore\n@endignore\nafter\n@end\n")
	if err != nil {
		t.Fatal(err)
	}
	var text []string
	parser.SetTextCallback(func(_ *KAGParser, value string) error {
		text = append(text, value)
		return nil
	})
	budget := uint64(100)
	if _, err := parser.Run(&budget); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(text, []string{"before", "after"}) {
		t.Fatalf("visible text = %v", text)
	}

	parser, err = NewKAGParser("ignore-state.ks", "@ignore\nhidden\n@endignore\nvisible\n@end\n")
	if err != nil {
		t.Fatal(err)
	}
	budget = 2
	if _, err := parser.Step(&budget); !errors.Is(err, ErrKAGExecutionBudget) {
		t.Fatalf("ignore budget error = %v", err)
	}
	saved := parser.Store()
	if saved.IgnoreDepth != 1 {
		t.Fatalf("saved ignore depth = %d", saved.IgnoreDepth)
	}
	if err := parser.RestoreState(saved); err != nil {
		t.Fatal(err)
	}
	budget = 100
	event, err := parser.Step(&budget)
	if err != nil {
		t.Fatal(err)
	}
	if event.Kind != KAGEventText || event.Text != "visible" {
		t.Fatalf("restored ignore event = %#v", event)
	}
}

func TestKAGNativeEventRetainsOrderedAttributes(t *testing.T) {
	parser, err := NewKAGParser("native.ks", "[foo a=1 a=2 b=\"x y\" storage=bg0]\n")
	if err != nil {
		t.Fatal(err)
	}
	budget := uint64(10)
	event, err := parser.Step(&budget)
	if err != nil {
		t.Fatal(err)
	}
	if event.Kind != KAGEventTag || event.Tag == nil {
		t.Fatalf("event = %#v", event)
	}
	vm := tjs.NewVM()
	value, err := kagEventValue(vm, event)
	if err != nil {
		t.Fatal(err)
	}
	object, ok := vm.Objects.Lookup(value.ObjectRef().Object)
	if !ok {
		t.Fatal("event dictionary was not allocated")
	}
	attrs, ok := object.Get("attributes")
	if !ok {
		t.Fatal("event omitted attributes")
	}
	items, err := vm.ArrayItems(attrs)
	if err != nil || len(items) != 4 {
		t.Fatalf("attributes = %#v, %v", items, err)
	}
	for i, want := range [][2]string{{"a", "1"}, {"a", "2"}, {"b", "x y"}, {"storage", "bg0"}} {
		pair, err := vm.ArrayItems(items[i])
		if err != nil || len(pair) != 2 || pair[0].Text() != want[0] || pair[1].Text() != want[1] {
			t.Fatalf("attribute %d = %#v, %v", i, pair, err)
		}
	}
	if got, ok := object.Get("a"); !ok || got.Text() != "2" {
		t.Fatalf("attribute a = %v, %v; want last value 2", got, ok)
	}
	if got, ok := object.Get("storage"); !ok || got.Text() != "bg0" {
		t.Fatalf("tag storage = %v, %v; want bg0", got, ok)
	}
	if got, ok := object.Get("sourceStorage"); !ok || got.Text() != "native.ks" {
		t.Fatalf("source storage = %v, %v; want native.ks", got, ok)
	}
}

func TestKAGNativeEventKeepsSourceStorageSeparateFromMissingAttribute(t *testing.T) {
	parser, err := NewKAGParser("native.ks", "@loadcell\n")
	if err != nil {
		t.Fatal(err)
	}
	budget := uint64(10)
	event, err := parser.Step(&budget)
	if err != nil {
		t.Fatal(err)
	}
	vm := tjs.NewVM()
	value, err := kagEventValue(vm, event)
	if err != nil {
		t.Fatal(err)
	}
	object, ok := vm.Objects.Lookup(value.ObjectRef().Object)
	if !ok {
		t.Fatal("event dictionary was not allocated")
	}
	if _, ok := object.Get("storage"); ok {
		t.Fatal("source storage shadowed a missing tag storage attribute")
	}
	if got, ok := object.Get("sourceStorage"); !ok || got.Text() != "native.ks" {
		t.Fatalf("source storage = %v, %v; want native.ks", got, ok)
	}
}

func TestKAGCursorLocationUsesTokenAtCursor(t *testing.T) {
	parser, err := NewKAGParser("cursor.ks", "*start\none\n*target\ntext")
	if err != nil {
		t.Fatal(err)
	}
	if err := parser.Jump("target"); err != nil {
		t.Fatal(err)
	}
	if got := parser.GetCurLine(); got != 3 {
		t.Fatalf("jump line = %d", got)
	}
	if got := parser.GetCurPos(); got != 1 {
		t.Fatalf("jump position = %d", got)
	}
	if err := parser.Call("target"); err != nil {
		t.Fatal(err)
	}
	if got := parser.GetCurLine(); got != 3 {
		t.Fatalf("call line = %d", got)
	}
	if _, err := parser.Run(nil); err != nil {
		t.Fatal(err)
	}
	if got := parser.GetCurLine(); got != 4 {
		t.Fatalf("EOF line = %d", got)
	}
}

func TestKAGExecutionReportsUnclosedControlBlocksWithLocation(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{name: "ignore", source: "@ignore\nhidden\n", want: "ignore block missing endignore"},
		{name: "condition", source: "@if exp=\"true\"\nvisible\n", want: "conditional block missing endif"},
		{name: "call condition", source: "@call target=\"sub\"\n@end\n*sub\ntext\n", want: "conditional call block missing return"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parser, err := NewKAGParser("unclosed.ks", test.source)
			if err != nil {
				t.Fatal(err)
			}
			_, err = parser.Run(nil)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
			if !strings.Contains(err.Error(), "unclosed.ks:") {
				t.Fatalf("error lacks source location: %v", err)
			}
		})
	}
}

func TestKAGExecutionRestoreRestoresEndedState(t *testing.T) {
	parser, err := NewKAGParser("restore-end.ks", "@end\n")
	if err != nil {
		t.Fatal(err)
	}
	saved := parser.Store()
	if event, err := parser.Step(nil); err != nil || event.Kind != KAGEventEnd {
		t.Fatalf("first end = %#v, %v", event, err)
	}
	if err := parser.RestoreState(saved); err != nil {
		t.Fatal(err)
	}
	event, err := parser.Step(nil)
	if err != nil || event.Kind != KAGEventEnd || event.Token.Tag == nil || event.Token.Tag.Name != "end" {
		t.Fatalf("restored end = %#v, %v", event, err)
	}
}

func TestKAGExecutionMacroExpansionAndArguments(t *testing.T) {
	parser, err := NewKAGParser("macro.ks", "@macro name=welcome\nHello %who|friend\n[result value=%who missing=%pos]\n@endmacro\n@welcome who=world\n@end\n")
	if err != nil {
		t.Fatal(err)
	}
	var text []string
	var result kag.Tag
	parser.SetCallbacks(&KAGCallbacks{
		OnTag: func(_ *KAGParser, tag *kag.Tag) error {
			if tag.Name == "result" {
				result = *tag
			}
			return nil
		},
		OnText: func(_ *KAGParser, value string) error {
			text = append(text, value)
			return nil
		},
	})
	budget := uint64(100)
	if _, err := parser.Run(&budget); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(text, []string{"Hello world"}) {
		t.Fatalf("expanded text = %v", text)
	}
	if value, ok := result.Get("value"); !ok || value != "world" {
		t.Fatalf("provided macro argument = %q, %v; want world", value, ok)
	}
	if value, ok := result.Get("missing"); !ok || value != "" {
		t.Fatalf("missing macro argument = %q, %v; want empty", value, ok)
	}
}

func TestKAGMacroDefinitionsSurviveScenarioLoad(t *testing.T) {
	parser, err := NewKAGParser("first.ks", `
[macro name=clear]
[cm]
[call storage=uiscript.ks target=*uisetup]
[endmacro]
[pmacro name=common value=from-first]
[end]
`)
	if err != nil {
		t.Fatal(err)
	}
	budget := uint64(1_000)
	if _, err := parser.Run(&budget); err != nil {
		t.Fatalf("run macro-definition scenario: %v", err)
	}
	if err := parser.LoadScenario("1.ks", `
[macro name=local]
[localtag]
[endmacro]
[clear]
[local]
[foo common=true]
`); err != nil {
		t.Fatalf("load scenario using earlier macro: %v", err)
	}
	if err := parser.pushKAGMacro(&kag.Tag{Name: "clear"}); err != nil {
		t.Fatalf("expand clear macro: %v", err)
	}
	var clearTags []kag.Tag
	for len(clearTags) < 2 {
		token := parser.nextKAGToken()
		if token == nil {
			t.Fatal("clear macro ended before cm and call")
		}
		if token.Kind != kag.KindTag {
			continue
		}
		if token.Tag == nil {
			t.Fatalf("clear macro token = %#v", token)
		}
		clearTags = append(clearTags, *token.Tag)
	}
	if clearTags[0].Name != "cm" || clearTags[1].Name != "call" {
		t.Fatalf("clear macro expanded tags = %#v, want cm, call", clearTags)
	}
	if storage, ok := clearTags[1].Get("storage"); !ok || storage != "uiscript.ks" {
		t.Fatalf("clear macro call storage = %q, %v; want uiscript.ks", storage, ok)
	}
	if target, ok := clearTags[1].Get("target"); !ok || target != "*uisetup" {
		t.Fatalf("clear macro call target = %q, %v; want *uisetup", target, ok)
	}
	if err := parser.pushKAGMacro(&kag.Tag{Name: "local"}); err != nil {
		t.Fatalf("expand local macro: %v", err)
	}
	var localTag *kag.Tag
	for localTag == nil {
		token := parser.nextKAGToken()
		if token == nil {
			t.Fatal("local macro ended without localtag")
		}
		if token.Kind == kag.KindTag {
			localTag = token.Tag
		}
	}
	if localTag == nil || localTag.Name != "localtag" {
		t.Fatalf("local macro tag = %#v, want localtag", localTag)
	}
	foo := kag.Tag{Name: "foo", Attributes: []kag.Attribute{{Name: "common", Value: "true"}}}
	expandedFoo := expandKAGTag(foo, parser.paramMacroDefs, nil)
	if value, ok := expandedFoo.Get("value"); !ok || value != "from-first" {
		t.Fatalf("persistent parameter macro value = %q, %v; want from-first", value, ok)
	}
	if _, ok := parser.Store().MacroDefinitions["clear"]; !ok {
		t.Fatal("clear macro was not retained after scenario load")
	}
}

func TestKAGParameterMacroExpansionPreservesEntityAttributes(t *testing.T) {
	tag := kag.Tag{Name: "link", Attributes: []kag.Attribute{{Name: "style"}}}
	parameters := map[string][]kag.Attribute{
		"style": {{Name: "storage", Value: "sf.chapter + f.suffixks", Entity: true}},
	}
	expanded := expandKAGTag(tag, parameters, nil)
	if len(expanded.Attributes) != 1 {
		t.Fatalf("expanded attributes = %#v", expanded.Attributes)
	}
	got := expanded.Attributes[0]
	if got.Name != "storage" || got.Value != "sf.chapter + f.suffixks" || !got.Entity {
		t.Fatalf("expanded entity attribute = %#v", got)
	}
}

func TestKAGExecutionParameterMacroAndEmbeddedExpression(t *testing.T) {
	parser, err := NewKAGParser("pmacro.ks", "@pmacro name=common x=1 y=2\n[foo common=true]\n[emb exp=\"'a[b]'\" escape=false]\n@end\n")
	if err != nil {
		t.Fatal(err)
	}
	var gotTag kag.Tag
	var text []string
	parser.SetCallbacks(&KAGCallbacks{
		OnTag: func(_ *KAGParser, tag *kag.Tag) error {
			gotTag = *tag
			return nil
		},
		OnText: func(_ *KAGParser, value string) error {
			text = append(text, value)
			return nil
		},
	})
	budget := uint64(100)
	if _, err := parser.Run(&budget); err != nil {
		t.Fatal(err)
	}
	if gotTag.Name != "foo" {
		t.Fatalf("tag = %#v", gotTag)
	}
	if value, ok := gotTag.Get("x"); !ok || value != "1" {
		t.Fatalf("parameter macro x = %q, %v", value, ok)
	}
	if !reflect.DeepEqual(text, []string{"a[b]"}) {
		t.Fatalf("embedded text = %v", text)
	}
}

func TestKAGExecutionInlineScriptsRespectConditionsAndOrder(t *testing.T) {
	source := `@if exp="false"
@iscript
global.skipped = 99;
@endscript
@endif
[iscript]\
global.first = 40;
global.second = global.first + 2;
[endscript]\
after
@end
`
	parser, err := NewKAGParser("script.ks", source)
	if err != nil {
		t.Fatal(err)
	}
	var calls []string
	state := make(map[string]int)
	parser.SetCallbacks(&KAGCallbacks{
		OnScript: func(_ *KAGParser, script, storage string, startLine uint64) error {
			calls = append(calls, script)
			if storage != "script.ks" || startLine != 7 {
				t.Fatalf("script source position = %q:%d, want script.ks:7", storage, startLine)
			}
			state["first"] = 40
			state["second"] = state["first"] + 2
			return nil
		},
	})
	budget := uint64(100)
	events, err := parser.Run(&budget)
	if err != nil {
		t.Fatal(err)
	}
	expectedScript := "global.first = 40;\r\nglobal.second = global.first + 2;\r\n"
	if !reflect.DeepEqual(calls, []string{expectedScript}) {
		t.Fatalf("executed scripts = %q, want only active script %q", calls, expectedScript)
	}
	if state["first"] != 40 || state["second"] != 42 {
		t.Fatalf("script callback state = %v", state)
	}
	if len(events) < 2 || events[0].Kind != KAGEventScript || events[0].Location.Line != 7 || events[1].Kind != KAGEventText || events[1].Text != "after" {
		t.Fatalf("script/text event order = %#v", events)
	}
}

func TestKAGExecutionInlineScriptBudgetAndUnterminatedPosition(t *testing.T) {
	parser, err := NewKAGParser("budget-inline.ks", "@iscript\none\ntwo\n@endscript\n@end\n")
	if err != nil {
		t.Fatal(err)
	}
	budget := uint64(2)
	if _, err := parser.Step(&budget); !errors.Is(err, ErrKAGExecutionBudget) {
		t.Fatalf("inline script budget error = %v", err)
	}
	if _, err := NewKAGParser("broken-inline.ks", "@iscript\nvar value = 1;\n"); err == nil ||
		!strings.Contains(err.Error(), "broken-inline.ks:1:1") ||
		!strings.Contains(err.Error(), "unterminated KAG inline script") {
		t.Fatalf("unterminated inline script error = %v", err)
	}
	if _, err := NewKAGParser("macro-inline.ks", "@macro name=m\n@iscript\nvar value = 1;\n@endscript\n@endmacro\n"); err == nil ||
		!strings.Contains(err.Error(), "macro-inline.ks:2:1") ||
		!strings.Contains(err.Error(), "inline script inside KAG macro") {
		t.Fatalf("inline script in macro error = %v", err)
	}
}

func TestSessionKAGInlineScriptUsesCurrentVM(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()
	scenario := `@if exp="false"
[iscript]
global.inlineSkipped = 99;
[endscript]
@endif
[iscript]
global.inlineFirst = 40;
global.inlineSecond = global.inlineFirst + 2;
[endscript]
@end
`
	source := `Plugins.link("plugin/KAGParser.dll");
parser = new KAGParser();
parser.onScript = function(script, scriptname, lineofs) {
	global.inlineSourceName = scriptname;
	global.inlineStartOffset = lineofs;
	Scripts.exec(script, scriptname, lineofs);
};
parser.loadScenario("scenario/inline.ks", ` + strconv.Quote(scenario) + `);
parser.getNextTag();
`
	if _, err := session.executeSource("<inline-script-session-test>", source, false); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]int64{
		"global.inlineFirst":       40,
		"global.inlineSecond":      42,
		"global.inlineStartOffset": 6,
	} {
		value, err := session.Evaluate(name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got, err := value.Int()
		if err != nil || got != want {
			t.Fatalf("%s = %v, %v; want %d", name, value, err, want)
		}
	}
	sourceName, err := session.Evaluate("global.inlineSourceName")
	if err != nil || sourceName.Text() != "inline.ks" {
		t.Fatalf("inline source name = %v, %v", sourceName, err)
	}
	skippedType, err := session.Evaluate("typeof global.inlineSkipped")
	if err != nil || skippedType.Text() != "undefined" {
		t.Fatalf("inactive inline script typeof = %v, %v; want undefined", skippedType, err)
	}
}

func TestSessionKAGLabelAndScenarioCallbacks(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()
	callbackScenario := "*define|callback caption\n[visible]\n@end\n"
	source := `Plugins.link("plugin/KAGParser.dll");
parser = new KAGParser();
parser.onScenarioLoad = function(storage) {
	global.scenarioLoadStorage = storage;
	return ` + strconv.Quote(callbackScenario) + `;
};
parser.onScenarioLoaded = function(storage) {
	global.scenarioLoadedStorage = storage;
};
parser.onLabel = function(label, caption) {
	global.labelName = label;
	global.labelCaption = caption;
};
parser.loadScenario("scenario/callback.ks");
global.callbackTag = parser.getNextTag();
`
	if _, err := session.executeSource("<kag-label-callback-test>", source, false); err != nil {
		t.Fatal(err)
	}
	for expression, want := range map[string]string{
		"global.scenarioLoadStorage":   "scenario/callback.ks",
		"global.scenarioLoadedStorage": "scenario/callback.ks",
		"global.labelName":             "*define",
		"global.labelCaption":          "callback caption",
		"global.callbackTag.tagname":   "visible",
	} {
		value, err := session.Evaluate(expression)
		if err != nil || value.Text() != want {
			t.Errorf("%s = %q, err=%v; want %q", expression, value.Text(), err, want)
		}
	}
}

func TestSessionKAGRepeatedLabelAliases(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()
	scenario := "*a\nA\n*|second\nB\n[foo]\n*a\nC\n[bar]"
	source := `Plugins.link("plugin/KAGParser.dll");
p = new KAGParser();
p.onScenarioLoad = function() { return ` + strconv.Quote(scenario) + `; };
p.loadScenario("labels.ks");
function nextNonCharacterTag() {
	var tag;
	do { tag = p.getNextTag(); } while (tag.tagname == "ch" || tag.tagname == "r");
	return tag;
}
p.goToLabel("*a:2");
var second = p.curLabel + ":" + nextNonCharacterTag().tagname;
p.goToLabel("*a:3");
global.aliasResult = second + "|" + p.curLabel + ":" + nextNonCharacterTag().tagname;
`
	if _, err := session.executeSource("<kag-label-alias-test>", source, false); err != nil {
		t.Fatal(err)
	}
	value, err := session.Evaluate("global.aliasResult")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := value.Text(), "*a:2:foo|*a:3:bar"; got != want {
		t.Fatalf("repeated label result = %q, want %q", got, want)
	}
}

func TestKAGExecutionMacroExpansionRestoresState(t *testing.T) {
	parser, err := NewKAGParser("macro-state.ks", "@macro name=m\nbody\n@endmacro\n@m\n@end\n")
	if err != nil {
		t.Fatal(err)
	}
	if event, err := parser.Step(nil); err != nil || event.Text != "body" {
		t.Fatalf("macro event = %#v, %v", event, err)
	}
	saved := parser.Store()
	restored, err := NewKAGParser("macro-state.ks", "@macro name=m\nbody\n@endmacro\n@m\n@end\n")
	if err != nil {
		t.Fatal(err)
	}
	if err := restored.RestoreState(saved); err != nil {
		t.Fatal(err)
	}
	event, err := restored.Step(nil)
	if err != nil || event.Kind != KAGEventNewline {
		t.Fatalf("restored macro event = %#v, %v", event, err)
	}
}

func TestKAGMacroMalformedDefinitionsReportLocation(t *testing.T) {
	if _, err := NewKAGParser("bad-macro.ks", "@macro name=x\nbody\n"); err == nil || !strings.Contains(err.Error(), "bad-macro.ks:1:1") || !strings.Contains(err.Error(), "missing endmacro") {
		t.Fatalf("missing macro terminator error = %v", err)
	}
	if _, err := NewKAGParser("bad-pmacro.ks", "@pmacro x=1\n"); err == nil || !strings.Contains(err.Error(), "parameter macro missing name") {
		t.Fatalf("missing parameter macro name error = %v", err)
	}
}
func TestKAGInterruptWaitsForReadableScenarioAndIsOneShot(t *testing.T) {
	parser := NewKAGParserFromScenario(kag.Scenario{})
	parser.Interrupt()

	budget := uint64(100)
	event, err := parser.Step(&budget)
	if err != nil || event.Kind != KAGEventEnd {
		t.Fatalf("step without scenario = %#v, %v; want end", event, err)
	}
	if !parser.interrupted {
		t.Fatal("interrupt was consumed without a readable scenario")
	}

	if err := parser.LoadScenario("interrupt.ks", "XY"); err != nil {
		t.Fatal(err)
	}
	budget = 0
	if _, err := parser.Step(&budget); !errors.Is(err, ErrKAGExecutionBudget) {
		t.Fatalf("step with exhausted budget = %v; want budget error", err)
	}
	if !parser.interrupted {
		t.Fatal("budget error consumed the pending interrupt")
	}

	budget = 100
	event, err = parser.Step(&budget)
	if err != nil || event.Kind != KAGEventTag || event.Tag == nil || event.Tag.Name != "interrupt" {
		t.Fatalf("interrupt event = %#v, %v; want interrupt tag", event, err)
	}
	if parser.interrupted {
		t.Fatal("interrupt remained pending after emitting its tag")
	}
	event, err = parser.Step(&budget)
	if err != nil || event.Kind != KAGEventText || event.Text != "XY" {
		t.Fatalf("event after interrupt = %#v, %v; want text XY", event, err)
	}

	parser.Interrupt()
	parser.ResetInterrupt()
	if err := parser.LoadScenario("reset.ks", "Z"); err != nil {
		t.Fatal(err)
	}
	event, err = parser.Step(&budget)
	if err != nil || event.Kind != KAGEventText || event.Text != "Z" {
		t.Fatalf("event after resetInterrupt = %#v, %v; want text Z", event, err)
	}
}

func TestKAGInterruptNativeMethodsMatchFixture(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()

	if _, err := session.executeSource("<kag-interrupt-native-test>", `Plugins.link("Plugin/KAGParser.dll"); p = new KAGParser(); p.interrupt(); before = p.getNextTag(); p.loadScenario("interrupt.ks", "XY"); interrupted = p.getNextTag(); text = p.step(); p.interrupt(); p.resetInterrupt(); p.loadScenario("reset.ks", "Z"); resetText = p.step()`, false); err != nil {
		t.Fatal(err)
	}
	value, err := session.Evaluate(`[before === void,interrupted.tagname,text.text,resetText.text].join('|')`)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := value.Text(), "1|interrupt|XY|Z"; got != want {
		t.Fatalf("native interrupt result = %q, want %q", got, want)
	}
}
func TestKAGClearCallStackNativeMethod(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()

	source := `Plugins.link("Plugin/KAGParser.dll"); p = new KAGParser(); p.loadScenario("clear-call-stack.ks", "*entry\nbody\n*sub\n"); p.callLabel("*sub"); depthBefore = p.callStackDepth; cursorBefore = p.curPos; p.clearCallStack()`
	if _, err := session.executeSource("<kag-clear-call-stack-native-test>", source, false); err != nil {
		t.Fatal(err)
	}
	value, err := session.Evaluate(`[depthBefore,p.callStackDepth,p.curStorage,p.curLabel,p.curPos===cursorBefore].join('|')`)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := value.Text(), "1|0|clear-call-stack.ks|*sub|1"; got != want {
		t.Fatalf("clearCallStack result = %q, want %q", got, want)
	}
}

func TestKAGInterruptRemainsPendingAtEOF(t *testing.T) {
	parser, err := NewKAGParser("interrupt-eof.ks", "before")
	if err != nil {
		t.Fatal(err)
	}
	if event, err := parser.Step(nil); err != nil || event.Kind != KAGEventText || event.Text != "before" {
		t.Fatalf("before-EOF event = %#v, %v; want text", event, err)
	}
	if event, err := parser.Step(nil); err != nil || event.Kind != KAGEventNewline {
		t.Fatalf("newline before EOF event = %#v, %v; want newline", event, err)
	}
	parser.Interrupt()
	if event, err := parser.Step(nil); err != nil || event.Kind != KAGEventEnd {
		t.Fatalf("EOF event = %#v, %v; want end", event, err)
	}
	if !parser.interrupted {
		t.Fatal("interrupt was consumed at EOF")
	}
	if err := parser.LoadScenario("after-eof.ks", "after"); err != nil {
		t.Fatal(err)
	}
	if event, err := parser.Step(nil); err != nil || event.Kind != KAGEventTag || event.Tag == nil || event.Tag.Name != "interrupt" {
		t.Fatalf("new scenario event = %#v, %v; want interrupt tag", event, err)
	}
	if event, err := parser.Step(nil); err != nil || event.Kind != KAGEventText || event.Text != "after" {
		t.Fatalf("new scenario next event = %#v, %v; want text", event, err)
	}
}

func TestKAGInterruptRecognizesReadableMacroFrame(t *testing.T) {
	parser, err := NewKAGParser("interrupt-macro.ks", "@macro name=m\n[first][second]\n@endmacro\n@m")
	if err != nil {
		t.Fatal(err)
	}
	if event, err := parser.Step(nil); err != nil || event.Kind != KAGEventTag || event.Tag == nil || event.Tag.Name != "first" {
		t.Fatalf("first macro event = %#v, %v; want first tag", event, err)
	}
	parser.Interrupt()
	if event, err := parser.Step(nil); err != nil || event.Kind != KAGEventTag || event.Tag == nil || event.Tag.Name != "interrupt" {
		t.Fatalf("macro interrupt event = %#v, %v; want interrupt tag", event, err)
	}
	if event, err := parser.Step(nil); err != nil || event.Kind != KAGEventTag || event.Tag == nil || event.Tag.Name != "second" {
		t.Fatalf("next macro event = %#v, %v; want second tag", event, err)
	}
}

func TestKAGInterruptIsNotSerializedInParserState(t *testing.T) {
	parser, err := NewKAGParser("interrupt-state.ks", "text")
	if err != nil {
		t.Fatal(err)
	}
	parser.Interrupt()
	state := parser.Store()
	parser.ResetInterrupt()
	if err := parser.RestoreState(state); err != nil {
		t.Fatal(err)
	}
	if event, err := parser.Step(nil); err != nil || event.Kind != KAGEventText || event.Text != "text" {
		t.Fatalf("restored event = %#v, %v; want text without interrupt", event, err)
	}
}

func TestKAGMacroStarMergeCopiesActiveArguments(t *testing.T) {
	parser, err := NewKAGParser("macro-star.ks", `
[macro name=outer]
[inner *]
[endmacro]
[macro name=inner]
[emit value=%flag]
[endmacro]
[outer flag=ok]
[end]
`)
	if err != nil {
		t.Fatal(err)
	}
	var emitted kag.Tag
	parser.SetTagCallback(func(_ *KAGParser, tag *kag.Tag) error {
		if tag.Name == "emit" {
			emitted = *tag
		}
		return nil
	})
	if _, err := parser.Run(nil); err != nil {
		t.Fatal(err)
	}
	if value, ok := emitted.Get("value"); !ok || value != "ok" {
		t.Fatalf("merged macro argument = %q, %v; want ok", value, ok)
	}
}

func TestSessionKAGMacroExpressionsExposeMP(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()
	scenario := `
[macro name=outer]
[inner *]
[endmacro]
[macro name=inner]
[emit value=conditional cond="mp.flag == 'ok' && macroParams.flag == 'ok'"]
[endmacro]
[outer flag=ok]
[end]
`
	source := `Plugins.link("plugin/KAGParser.dll");
parser = new KAGParser();
global.emitted = "";
parser.loadScenario("macro-mp.ks", ` + strconv.Quote(scenario) + `);
var tag;
while ((tag = parser.getNextTag()) !== void) {
	if (tag.tagname == "emit") {
		global.emitted = tag.value;
		break;
	}
}
`
	if _, err := session.executeSource("<kag-macro-mp-test>", source, false); err != nil {
		t.Fatal(err)
	}
	value, err := session.Evaluate("global.emitted")
	if err != nil {
		t.Fatal(err)
	}
	if got := value.Text(); got != "conditional" {
		t.Fatalf("macro mp expression result = %q; want conditional", got)
	}
}
