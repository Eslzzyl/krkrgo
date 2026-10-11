package runtime

import (
	"testing"

	"github.com/eslzzyl/krkrgo/internal/tjs"
)

func TestRectNativeFourEdgeSemantics(t *testing.T) {
	session := NewSession(DefaultServices(), 10000)
	for _, source := range []string{"r = new Rect(1, 2, 11, 22)", "c = new Rect(r)", "c.setOffset(4, 5)", "c.addOffset(1, -1)", "c.setSize(5, 6)"} {
		if _, err := session.Evaluate(source); err != nil {
			t.Fatalf("%s: %v", source, err)
		}
	}
	value, err := session.Evaluate("[c.left, c.top, c.right, c.bottom, c.width, c.height, c.includedPos(5, 5), c.includedPos(9, 10)]")
	if err != nil {
		t.Fatal(err)
	}
	items, err := session.VM.ArrayItems(value)
	if err != nil {
		t.Fatal(err)
	}
	want := []int64{5, 4, 10, 10, 5, 6, 1, 0}
	if len(items) != len(want) {
		t.Fatalf("Rect result length=%d, want %d", len(items), len(want))
	}
	for i, expected := range want {
		got, err := items[i].Int()
		if err != nil || got != expected {
			t.Fatalf("Rect result[%d]=%v/%v, want %d", i, items[i], err, expected)
		}
	}
	arrayValue, err := session.Evaluate("r.nativeArray")
	if err != nil {
		t.Fatal(err)
	}
	arrayItems, err := session.VM.ArrayItems(arrayValue)
	if err != nil || len(arrayItems) != 4 {
		t.Fatalf("Rect.nativeArray=%v/%v", arrayItems, err)
	}
	for i, expected := range []int64{1, 2, 11, 22} {
		got, _ := arrayItems[i].Int()
		if got != expected {
			t.Fatalf("nativeArray[%d]=%d, want %d", i, got, expected)
		}
	}
	if _, err := session.Evaluate("new Rect(1)"); err == nil {
		t.Fatal("Rect accepted a non-Rect copy argument")
	}
	if _, err := session.Evaluate("new Rect(null)"); err == nil {
		t.Fatal("Rect accepted a null copy argument")
	}
}

func TestRectNativeClipUnionAndEmptyEdges(t *testing.T) {
	session := NewSession(DefaultServices(), 10000)
	for _, source := range []string{"a = new Rect(0, 0, 10, 10)", "b = new Rect(5, 5, 20, 20)", "e = new Rect()"} {
		if _, err := session.Evaluate(source); err != nil {
			t.Fatalf("%s: %v", source, err)
		}
	}
	value, err := session.Evaluate("[a.intersects(b), a.included(b), a.clip(b), a.left, a.top, a.right, a.bottom, e.union(a), e.left, e.top, e.right, e.bottom, e.isEmpty()]")
	if err != nil {
		t.Fatal(err)
	}
	items, err := session.VM.ArrayItems(value)
	if err != nil {
		t.Fatal(err)
	}
	want := []int64{1, 0, 1, 5, 5, 10, 10, 1, 0, 0, 10, 10, 0}
	if len(items) != len(want) {
		t.Fatalf("Rect edge result length=%d, want %d", len(items), len(want))
	}
	for i, expected := range want {
		got, err := items[i].Int()
		if err != nil || got != expected {
			t.Fatalf("Rect edge result[%d]=%v/%v, want %d", i, items[i], err, expected)
		}
	}
}

func TestAsyncTriggerNativeActionCachedCancelModeAndInvalidate(t *testing.T) {
	services := DefaultServices()
	session := NewSession(services, 10000)
	owner := session.VM.Objects.New(tjs.CategoryDictionary)
	calls := 0
	if err := session.RegisterNative("asyncAction", func(_ tjs.Value, _ []tjs.Value) (tjs.Value, error) {
		calls++
		return tjs.VoidValue, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := owner.Set("action", session.VM.Globals["asyncAction"]); err != nil {
		t.Fatal(err)
	}
	session.VM.Globals["owner"] = tjs.ObjectValue(owner.Ref())
	for _, source := range []string{"a = new AsyncTrigger(owner)", "a.trigger()", "a.trigger()"} {
		if _, err := session.Evaluate(source); err != nil {
			t.Fatalf("%s: %v", source, err)
		}
	}
	value, err := session.Evaluate("[a.cached, a.mode]")
	if err != nil {
		t.Fatal(err)
	}
	items, err := session.VM.ArrayItems(value)
	if err != nil || len(items) != 2 || items[0].IntegerValue() != 1 || items[1].IntegerValue() != 0 {
		t.Fatalf("AsyncTrigger defaults=%v/%v", items, err)
	}
	triggerValue, err := session.Evaluate("a")
	if err != nil {
		t.Fatal(err)
	}
	id := triggerValue.ObjectRef().Object
	if services.Events.Count(uint64(id), EventTrigger) != 1 {
		t.Fatalf("cached trigger events=%d, want 1", services.Events.Count(uint64(id), EventTrigger))
	}
	if err := session.DispatchEvents(); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("cached action calls=%d, want 1", calls)
	}
	for _, source := range []string{"a.cached = false", "a.mode = 1", "a.trigger()", "a.trigger()", "a.cancel()"} {
		if _, err := session.Evaluate(source); err != nil {
			t.Fatalf("%s: %v", source, err)
		}
	}
	if services.Events.Count(uint64(id), EventTrigger) != 0 {
		t.Fatal("cancel left AsyncTrigger events queued")
	}
	for _, source := range []string{"a.mode = 2", "a.trigger()"} {
		if _, err := session.Evaluate(source); err != nil {
			t.Fatalf("%s: %v", source, err)
		}
	}
	pending := services.Events.Pending()
	if len(pending) != 1 || pending[0].Priority != PriorityIdle {
		t.Fatalf("AsyncTrigger mode queue=%v", pending)
	}
	roots := services.GCRoots()
	ownerRooted := false
	for _, root := range roots {
		if root.Kind() == tjs.KindObject && root.ObjectRef().Object == owner.ID {
			ownerRooted = true
			break
		}
	}
	if !ownerRooted {
		t.Fatal("AsyncTrigger ActionOwner is not a GC root")
	}
	budget := uint64(1000)
	if _, err := session.VM.Invalidate(triggerValue, services, &budget); err != nil {
		t.Fatal(err)
	}
	if services.Events.Count(uint64(id), EventTrigger) != 0 || services.AsyncTriggers[id] != nil || services.AsyncTriggerActions[id].owner.Object != 0 {
		t.Fatal("invalidated AsyncTrigger state remained")
	}
}

func TestAsyncTriggerRunFalseCallbackRunsDuringTick(t *testing.T) {
	services := DefaultServices()
	session := NewSession(services, 10_000)
	for _, source := range []string{
		`BaseConductor = new Dictionary()`,
		`BaseConductor.timerCallbackCount = 0`,
		`BaseConductor.timerCallback = function() { BaseConductor.timerCallbackCount = BaseConductor.timerCallbackCount + 1; }`,
		`BaseConductor.oneshot = new AsyncTrigger(BaseConductor, "timerCallback")`,
		`BaseConductor.run = function(processing) { if (!processing) BaseConductor.oneshot.trigger(); }`,
		`BaseConductor.run(false)`,
	} {
		if _, err := session.Evaluate(source); err != nil {
			t.Fatalf("%s: %v", source, err)
		}
	}
	oneshot, err := session.Evaluate("BaseConductor.oneshot")
	if err != nil {
		t.Fatal(err)
	}
	if queued := services.Events.Count(uint64(oneshot.ObjectRef().Object), EventTrigger); queued != 1 {
		t.Fatalf("run(false) queued %d AsyncTrigger events, want 1", queued)
	}
	if err := session.Tick(1); err != nil {
		t.Fatal(err)
	}
	count, err := session.Evaluate("BaseConductor.timerCallbackCount")
	if err != nil {
		t.Fatal(err)
	}
	if count.IntegerValue() != 1 {
		t.Fatalf("timerCallback calls = %d, want 1", count.IntegerValue())
	}
	if services.Events.Count(uint64(oneshot.ObjectRef().Object), EventTrigger) != 0 {
		t.Fatal("Tick left the one-shot AsyncTrigger event queued")
	}
}

func TestAsyncTriggerEmptyActionNameCallsOwnerFunctionWithEvent(t *testing.T) {
	services := DefaultServices()
	session := NewSession(services, 10_000)
	calls := 0
	var callbackArgs []tjs.Value
	if err := session.RegisterNative("asyncTriggerCallback", func(_ tjs.Value, args []tjs.Value) (tjs.Value, error) {
		calls++
		callbackArgs = append([]tjs.Value(nil), args...)
		return tjs.VoidValue, nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := session.executeSource("<async-trigger-empty-name>", `trigger = new AsyncTrigger(asyncTriggerCallback, ""); trigger.trigger()`, false); err != nil {
		t.Fatal(err)
	}
	if err := session.DispatchEvents(); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("empty-name callback calls = %d, want 1", calls)
	}
	if len(callbackArgs) != 1 {
		t.Fatalf("empty-name callback arguments = %d, want exactly 1", len(callbackArgs))
	}
	event := callbackArgs[0]
	if event.Kind() != tjs.KindObject || event.IsNull() {
		t.Fatalf("empty-name callback event = %v, want a dictionary", event)
	}
	eventObject, ok := session.VM.Objects.Lookup(event.ObjectRef().Object)
	if !ok || !eventObject.IsValid() {
		t.Fatal("empty-name callback event object is invalid")
	}
	eventType, ok := eventObject.Get("type")
	if !ok || eventType.Kind() != tjs.KindString || eventType.Text() != "onFire" {
		t.Fatalf("empty-name callback event type = %v, want onFire", eventType)
	}
	trigger, err := session.Evaluate("trigger")
	if err != nil {
		t.Fatal(err)
	}
	target, ok := eventObject.Get("target")
	if !ok || target.Kind() != tjs.KindObject || target.IsNull() || target.ObjectRef().Object != trigger.ObjectRef().Object {
		t.Fatalf("empty-name callback event target = %v, want current trigger %v", target, trigger.ObjectRef().Object)
	}
}

func TestAsyncTriggerNativeRequiresActionOwner(t *testing.T) {
	session := NewSession(DefaultServices(), 1000)
	if _, err := session.Evaluate("new AsyncTrigger()"); err == nil {
		t.Fatal("AsyncTrigger without ActionOwner unexpectedly succeeded")
	}
	if _, err := session.Evaluate("new AsyncTrigger(1)"); err == nil {
		t.Fatal("AsyncTrigger with non-object ActionOwner unexpectedly succeeded")
	}
}
