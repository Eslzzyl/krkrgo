package host

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/eslzzyl/krkrgo/internal/runtime"
	"github.com/eslzzyl/krkrgo/internal/tjs"
)

func testReplaySession() *runtime.Session {
	services := runtime.DefaultServices()
	services.SetRoot(runtime.NewRootLayer(1, 2, 1))
	_ = services.SetFrameSize(2, 1)
	return runtime.NewSession(services, 1024)
}

func TestReplayRejectsMalformedOrderingAndActions(t *testing.T) {
	session := testReplaySession()
	defer session.Close()
	for _, source := range []string{
		`[{"at_ms":2,"action":"click","x":0,"y":0},{"at_ms":1,"action":"click","x":0,"y":0}]`,
		`[{"at_ms":1,"action":"pointer_down","x":0,"y":0,"button":4}]`,
		`[{"at_ms":1,"action":"assert","expression":""}]`,
		`[{"at_ms":1,"action":"click","x":0,"unknown":1}]`,
	} {
		replay, err := ParseReplay([]byte(source))
		if err != nil {
			continue
		}
		if err := replay.Validate(session); err == nil {
			t.Errorf("accepted malformed replay %s", source)
		}
	}
}

func TestReplayHeadlessSequenceProducesStableResult(t *testing.T) {
	session := testReplaySession()
	defer session.Close()
	replay, err := ParseReplay([]byte(`[
		{"at_ms":0,"action":"assert","expression":"1 + 1 == 2"},
		{"at_ms":4,"action":"checkpoint","name":"done","inspect":["1 + 2"]}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	result, err := replay.Run(session, 2)
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalTimeMS != 4 || len(result.Frames) != 3 || len(result.Checkpoints) != 1 {
		t.Fatalf("result=%#v", result)
	}
	var first, second bytes.Buffer
	if err := MarshalResult(&first, result); err != nil {
		t.Fatal(err)
	}
	if err := MarshalResult(&second, result); err != nil {
		t.Fatal(err)
	}
	if first.String() != second.String() {
		t.Fatalf("unstable result JSON: %q != %q", first.String(), second.String())
	}
	var wire map[string]any
	if err := json.Unmarshal(first.Bytes(), &wire); err != nil {
		t.Fatal(err)
	}
	if wire["final_time_ms"] != float64(4) {
		t.Fatalf("wire=%v", wire)
	}
}

func TestReplayTickErrorIncludesRecentMessagesAndUnwraps(t *testing.T) {
	session := testReplaySession()
	defer session.Close()
	session.Services.Messages = append(session.Services.Messages, "old diagnostic")

	target := session.VM.Objects.New(tjs.CategoryDictionary)
	callbackErr := errors.New("timer callback failed")
	if err := session.RegisterNative("failReplayTimer", func(_ tjs.Value, _ []tjs.Value) (tjs.Value, error) {
		for i := range 10 {
			session.Services.Messages = append(session.Services.Messages, fmt.Sprintf("tick-%d", i))
		}
		return tjs.VoidValue, callbackErr
	}); err != nil {
		t.Fatal(err)
	}
	timerCallback, ok := session.VM.Globals["failReplayTimer"]
	if !ok {
		t.Fatal("timer callback registration missing")
	}
	if err := target.Set("onTimer", timerCallback); err != nil {
		t.Fatal(err)
	}
	if err := session.RegisterNative("postReplayTimerEvent", func(_ tjs.Value, _ []tjs.Value) (tjs.Value, error) {
		return tjs.VoidValue, session.Services.Events.Post(uint64(target.ID), runtime.EventTimer, runtime.PriorityNormal)
	}); err != nil {
		t.Fatal(err)
	}

	replay, err := ParseReplay([]byte(`[
		{"at_ms":0,"action":"checkpoint","name":"post timer","inspect":["postReplayTimerEvent()"]},
		{"at_ms":13,"action":"checkpoint","name":"tick failure","inspect":[]}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	_, err = replay.Run(session, 5)
	if !errors.Is(err, callbackErr) {
		t.Fatalf("replay error = %v, want wrapped callback error", err)
	}
	var replayErr *ReplayError
	if !errors.As(err, &replayErr) {
		t.Fatalf("replay error type = %T, want *ReplayError", err)
	}
	if replayErr.Context != "replay action 1 at 13 ms: tick from 0 ms to 5 ms" {
		t.Fatalf("replay error context = %q", replayErr.Context)
	}
	if len(replayErr.Messages) != replayDiagnosticMessageLimit {
		t.Fatalf("diagnostic message count = %d, want %d", len(replayErr.Messages), replayDiagnosticMessageLimit)
	}
	if replayErr.Messages[0] != "tick-2" || replayErr.Messages[len(replayErr.Messages)-1] != "tick-9" {
		t.Fatalf("diagnostic messages = %v, want tick-2 through tick-9", replayErr.Messages)
	}
	if strings.Contains(err.Error(), "old diagnostic") || strings.Contains(err.Error(), "tick-0") {
		t.Fatalf("replay error included historical/omitted messages: %v", err)
	}
	if !strings.Contains(err.Error(), "timer callback failed") || !strings.Contains(err.Error(), "tick-9") {
		t.Fatalf("replay error = %v, missing cause or recent diagnostics", err)
	}
}

func TestReplayActionErrorIncludesNewMessages(t *testing.T) {
	session := testReplaySession()
	defer session.Close()
	session.Services.Messages = append(session.Services.Messages, "old diagnostic")

	replay, err := ParseReplay([]byte(`[
		{"at_ms":0,"action":"assert","expression":"(function(){Debug.message(\"fresh-0\");Debug.message(\"fresh-1\");Debug.message(\"fresh-2\");Debug.message(\"fresh-3\");Debug.message(\"fresh-4\");Debug.message(\"fresh-5\");Debug.message(\"fresh-6\");Debug.message(\"fresh-7\");Debug.message(\"fresh-8\");Debug.message(\"fresh-9\");throw new Exception(\"action failure\");})()"}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	_, err = replay.Run(session, 5)
	var replayErr *ReplayError
	if !errors.As(err, &replayErr) {
		t.Fatalf("replay error = %v, want *ReplayError", err)
	}
	if replayErr.Context != "replay action 0 at 0 ms" {
		t.Fatalf("replay error context = %q", replayErr.Context)
	}
	if len(replayErr.Messages) != replayDiagnosticMessageLimit || replayErr.Messages[0] != "fresh-2" || replayErr.Messages[len(replayErr.Messages)-1] != "fresh-9" {
		t.Fatalf("diagnostic messages = %v, want fresh-2 through fresh-9", replayErr.Messages)
	}
	cause := errors.Unwrap(err)
	if cause == nil || !strings.Contains(cause.Error(), "TJS throw: action failure") {
		t.Fatalf("unwrapped replay cause = %v, want original TJS exception", cause)
	}
	if strings.Contains(err.Error(), "old diagnostic") {
		t.Fatalf("replay error included historical/omitted messages: %v", err)
	}
}
