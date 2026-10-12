package runtime

import (
	"bytes"
	"log"
	"strconv"
	"strings"
	"testing"

	"github.com/eslzzyl/krkrgo/internal/tjs"
)

func TestTimerNativeConstructorPropertiesAndEvent(t *testing.T) {
	services := DefaultServices()
	session := NewSession(services, 10_000)
	owner := session.VM.Objects.New(tjs.CategoryDictionary)
	ownerValue := tjs.ObjectValue(owner.Ref())
	calls := 0
	if err := session.RegisterNative("timerAction", func(_ tjs.Value, _ []tjs.Value) (tjs.Value, error) {
		calls++
		return tjs.VoidValue, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := owner.Set("action", session.VM.Globals["timerAction"]); err != nil {
		t.Fatal(err)
	}
	session.VM.Globals["timerOwner"] = ownerValue

	if _, err := session.Evaluate("timer = new Timer(timerOwner)"); err != nil {
		t.Fatalf("construct Timer: %v", err)
	}
	defaults, err := session.Evaluate("[timer.interval, timer.enabled, timer.capacity, timer.mode]")
	if err != nil {
		t.Fatal(err)
	}
	items, err := session.VM.ArrayItems(defaults)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 4 || items[0].RealValue() != float64(1000)/float64(TimerScale) || items[1].IntegerValue() != 0 || items[2].IntegerValue() != 6 || items[3].IntegerValue() != 0 {
		t.Fatalf("Timer defaults = %#v", items)
	}

	for _, source := range []string{"timer.interval = 10.5", "timer.enabled = true", "timer.capacity = 2", "timer.mode = 1"} {
		if _, err := session.Evaluate(source); err != nil {
			t.Fatalf("%s: %v", source, err)
		}
	}
	timerValue, err := session.Evaluate("timer")
	if err != nil {
		t.Fatal(err)
	}
	timer := services.Timers.Timer(uint64(timerValue.ObjectRef().Object))
	if timer == nil || timer.IntervalTicks() != 10*TimerScale+TimerScale/2 || !timer.Enabled() || timer.Capacity() != 2 || timer.Mode() != PriorityExclusive {
		t.Fatalf("Timer state = %#v", timer)
	}

	if err := session.Tick(11); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("timer action calls = %d, want 1", calls)
	}
	if _, err := session.Evaluate("timer.enabled = false"); err != nil {
		t.Fatal(err)
	}
	if err := session.Tick(100); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("disabled timer action calls = %d", calls)
	}

	budget := uint64(1000)
	if _, err := session.VM.Invalidate(timerValue, services, &budget); err != nil {
		t.Fatal(err)
	}
	if services.Timers.Timer(uint64(timerValue.ObjectRef().Object)) != nil {
		t.Fatal("invalidated Timer remained registered")
	}
	if _, err := session.VM.Invalidate(timerValue, services, &budget); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Evaluate("timer.enabled"); err == nil {
		t.Fatal("invalidated Timer property unexpectedly succeeded")
	}
}

func TestTimerNativeRequiresActionOwner(t *testing.T) {
	session := NewSession(DefaultServices(), 1000)
	if _, err := session.Evaluate("new Timer()"); err == nil {
		t.Fatal("Timer without ActionOwner unexpectedly succeeded")
	}
	if _, err := session.Evaluate("new Timer(1)"); err == nil {
		t.Fatal("Timer with non-object ActionOwner unexpectedly succeeded")
	}
	if _, err := session.Evaluate("new Timer(null)"); err == nil {
		t.Fatal("Timer with null ActionOwner unexpectedly succeeded")
	}
}

func TestTimerNativeBaseInitializationDefersActionOwnerToConstructor(t *testing.T) {
	session := NewSession(DefaultServices(), 10_000)
	owner := session.VM.Objects.New(tjs.CategoryDictionary)
	session.VM.Globals["timerOwner"] = tjs.ObjectValue(owner.Ref())
	source := `
class ScriptTimer extends Timer {
	function ScriptTimer(owner) { super.Timer(owner); }
}
timer = new ScriptTimer(timerOwner);
`
	if _, err := session.Evaluate(source); err != nil {
		t.Fatalf("construct Timer subclass: %v", err)
	}
	timerValue, err := session.Evaluate("timer")
	if err != nil {
		t.Fatal(err)
	}
	if servicesTimer := session.Services.Timers.Timer(uint64(timerValue.ObjectRef().Object)); servicesTimer == nil {
		t.Fatal("Timer subclass was not registered")
	}
	if _, err := session.Evaluate("new Timer()"); err == nil {
		t.Fatal("Timer without ActionOwner unexpectedly succeeded")
	}
}

func TestTimerNativeForwardsScriptAction(t *testing.T) {
	session := NewSession(DefaultServices(), 10_000)
	owner := session.VM.Objects.New(tjs.CategoryDictionary)
	session.VM.Globals["timerOwner"] = tjs.ObjectValue(owner.Ref())
	for _, source := range []string{
		"timerOwner.count = 0",
		"timerOwner.action = function() { timerOwner.count = timerOwner.count + 1; }",
		"timer = new Timer(timerOwner)",
		"timer.interval = 10",
		"timer.enabled = true",
	} {
		if _, err := session.Evaluate(source); err != nil {
			t.Fatalf("%s: %v", source, err)
		}
	}
	if err := session.Tick(11); err != nil {
		t.Fatal(err)
	}
	count, err := session.Evaluate("timerOwner.count")
	if err != nil {
		t.Fatal(err)
	}
	if count.IntegerValue() != 1 {
		t.Fatalf("script Timer action count = %d, want 1", count.IntegerValue())
	}
}

func TestTimerActionDiagnostics(t *testing.T) {
	t.Setenv("KRKRGO_RENDER_STATS", "1")
	var output bytes.Buffer
	previousOutput, previousFlags, previousPrefix := log.Writer(), log.Flags(), log.Prefix()
	log.SetOutput(&output)
	log.SetFlags(0)
	log.SetPrefix("")
	t.Cleanup(func() {
		log.SetOutput(previousOutput)
		log.SetFlags(previousFlags)
		log.SetPrefix(previousPrefix)
	})

	session := NewSession(DefaultServices(), 10_000)
	owner := session.VM.Objects.New(tjs.CategoryDictionary)
	session.VM.Globals["timerOwner"] = tjs.ObjectValue(owner.Ref())
	for _, source := range []string{
		"timerOwner.action = function() {}",
		"timer = new Timer(timerOwner)",
		"timer.interval = 1",
		"timer.enabled = true",
	} {
		if _, err := session.Evaluate(source); err != nil {
			t.Fatalf("%s: %v", source, err)
		}
	}
	timerValue, err := session.Evaluate("timer")
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Tick(2); err != nil {
		t.Fatal(err)
	}

	want := []string{
		"krkrgo timer action:",
		"timer=" + strconv.FormatInt(int64(timerValue.ObjectRef().Object), 10),
		"owner=" + strconv.FormatInt(int64(owner.ID), 10),
		`action="action"`,
		"elapsed=",
	}
	for _, part := range want {
		if !strings.Contains(output.String(), part) {
			t.Fatalf("Timer action diagnostics missing %q in %q", part, output.String())
		}
	}
}
