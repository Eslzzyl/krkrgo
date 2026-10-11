package host

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"

	"github.com/eslzzyl/krkrgo/internal/runtime"
	"github.com/eslzzyl/krkrgo/internal/tjs"
)

const MainWindowExpression = "Window.mainWindow"

// Replay is an ordered, deterministic list of host actions.
type Replay struct {
	Entries []ReplayEntry `json:"entries"`
}

func (r Replay) MarshalJSON() ([]byte, error) {
	return json.Marshal(r.Entries)
}

func (r *Replay) UnmarshalJSON(data []byte) error {
	parsed, err := decodeReplay(data)
	if err != nil {
		return err
	}
	*r = *parsed
	return nil
}

// ReplayEntry is one timestamped action. Window is a TJS expression yielding a
// Window object; omitted windows use Window.mainWindow.
type ReplayEntry struct {
	AtMS   uint64       `json:"at_ms"`
	Window string       `json:"window"`
	Action ReplayAction `json:"-"`
}

// ReplayAction is the wire-compatible action union used by the Rust replay CLI.
type ReplayAction struct {
	Action     string   `json:"action"`
	X, Y       int32    `json:"-"`
	Button     int32    `json:"-"`
	Shift      int32    `json:"-"`
	Delta      int32    `json:"-"`
	Key        int32    `json:"-"`
	Text       string   `json:"-"`
	Focused    bool     `json:"-"`
	Name       string   `json:"-"`
	Inspect    []string `json:"-"`
	Frame      string   `json:"-"`
	HasFrame   bool     `json:"-"`
	Expression string   `json:"-"`
}

const (
	ActionPointerMove  = "pointer_move"
	ActionPointerDown  = "pointer_down"
	ActionPointerUp    = "pointer_up"
	ActionClick        = "click"
	ActionDoubleClick  = "double_click"
	ActionPointerLeave = "pointer_leave"
	ActionWheel        = "wheel"
	ActionKeyDown      = "key_down"
	ActionKeyUp        = "key_up"
	ActionText         = "text"
	ActionFocus        = "focus"
	ActionCheckpoint   = "checkpoint"
	ActionAssert       = "assert"
)

// ReplayFrameSummary is a stable, content-addressed description of a frame.
type ReplayFrameSummary struct {
	AtMS      uint64 `json:"at_ms"`
	Width     int32  `json:"width"`
	Height    int32  `json:"height"`
	RGBABytes int    `json:"rgba_bytes"`
	SHA256    string `json:"sha256"`
}

// ReplayAudioSummary is a stable description of samples drained at one tick.
type ReplayAudioSummary struct {
	AtMS    uint64 `json:"at_ms"`
	Samples int    `json:"samples"`
	SHA256  string `json:"sha256"`
}

// ReplayInspect is one evaluated checkpoint expression.
type ReplayInspect struct {
	Expression string `json:"expression"`
	Value      any    `json:"value"`
}

// ReplayCheckpoint records the deterministic observations made at a checkpoint.
type ReplayCheckpoint struct {
	Name    string              `json:"name"`
	AtMS    uint64              `json:"at_ms"`
	Inspect []ReplayInspect     `json:"inspect"`
	Frame   *ReplayFrameSummary `json:"frame,omitempty"`
}

// ReplayResult is the complete stable output of Replay.Run.
type ReplayResult struct {
	FinalTimeMS uint64               `json:"final_time_ms"`
	Frames      []ReplayFrameSummary `json:"frames"`
	Audio       []ReplayAudioSummary `json:"audio"`
	Checkpoints []ReplayCheckpoint   `json:"checkpoints"`
}

const replayDiagnosticMessageLimit = 8

// ReplayError adds the TJS messages emitted by the failing replay operation
// while preserving the underlying error for errors.Is and errors.As.
type ReplayError struct {
	Context  string
	Cause    error
	Messages []string
}

func (e *ReplayError) Error() string {
	if e == nil {
		return "<nil>"
	}
	var message strings.Builder
	message.WriteString(e.Context)
	if e.Context != "" && e.Cause != nil {
		message.WriteString(": ")
	}
	if e.Cause != nil {
		message.WriteString(e.Cause.Error())
	}
	if len(e.Messages) != 0 {
		message.WriteString("\nServices.Messages:")
		for _, entry := range e.Messages {
			message.WriteString("\n  ")
			message.WriteString(entry)
		}
	}
	return message.String()
}

func (e *ReplayError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func replayMessageCount(session *runtime.Session) int {
	if session == nil || session.Services == nil {
		return 0
	}
	return len(session.Services.Messages)
}

func newReplayError(session *runtime.Session, context string, cause error, messageStart int) error {
	var messages []string
	if session != nil && session.Services != nil {
		all := session.Services.Messages
		if messageStart < 0 {
			messageStart = 0
		}
		if messageStart > len(all) {
			messageStart = len(all)
		}
		messages = all[messageStart:]
		if len(messages) > replayDiagnosticMessageLimit {
			messages = messages[len(messages)-replayDiagnosticMessageLimit:]
		}
		messages = append([]string(nil), messages...)
	}
	return &ReplayError{Context: context, Cause: cause, Messages: messages}
}

// WrapReplayError preserves a replay or startup failure together with newly
// emitted session diagnostics.
func WrapReplayError(session *runtime.Session, context string, cause error, messageStart int) error {
	return newReplayError(session, context, cause, messageStart)
}

func (a ReplayAction) MarshalJSON() ([]byte, error) {
	if err := validateAction(a, 0); err != nil {
		return nil, err
	}
	var v any
	switch a.Action {
	case ActionPointerMove:
		v = struct {
			Action string `json:"action"`
			X      int32  `json:"x"`
			Y      int32  `json:"y"`
			Shift  int32  `json:"shift,omitempty"`
		}{a.Action, a.X, a.Y, a.Shift}
	case ActionPointerDown, ActionPointerUp:
		v = struct {
			Action string `json:"action"`
			X      int32  `json:"x"`
			Y      int32  `json:"y"`
			Button int32  `json:"button,omitempty"`
			Shift  int32  `json:"shift,omitempty"`
		}{a.Action, a.X, a.Y, a.Button, a.Shift}
	case ActionClick, ActionDoubleClick:
		v = struct {
			Action string `json:"action"`
			X      int32  `json:"x"`
			Y      int32  `json:"y"`
		}{a.Action, a.X, a.Y}
	case ActionPointerLeave:
		v = struct {
			Action string `json:"action"`
		}{a.Action}
	case ActionWheel:
		v = struct {
			Action string `json:"action"`
			X      int32  `json:"x"`
			Y      int32  `json:"y"`
			Delta  int32  `json:"delta"`
			Shift  int32  `json:"shift,omitempty"`
		}{a.Action, a.X, a.Y, a.Delta, a.Shift}
	case ActionKeyDown, ActionKeyUp:
		v = struct {
			Action string `json:"action"`
			Key    int32  `json:"key"`
			Shift  int32  `json:"shift,omitempty"`
		}{a.Action, a.Key, a.Shift}
	case ActionText:
		v = struct {
			Action string `json:"action"`
			Text   string `json:"text"`
		}{a.Action, a.Text}
	case ActionFocus:
		v = struct {
			Action  string `json:"action"`
			Focused bool   `json:"focused"`
		}{a.Action, a.Focused}
	case ActionCheckpoint:
		v = struct {
			Action  string   `json:"action"`
			Name    string   `json:"name"`
			Inspect []string `json:"inspect,omitempty"`
			Frame   *string  `json:"frame,omitempty"`
		}{a.Action, a.Name, a.Inspect, optionalString(a.Frame, a.HasFrame)}
	case ActionAssert:
		v = struct {
			Action     string `json:"action"`
			Expression string `json:"expression"`
		}{a.Action, a.Expression}
	}
	return json.Marshal(v)
}

func optionalString(s string, ok bool) *string {
	if !ok {
		return nil
	}
	return &s
}

func (e ReplayEntry) MarshalJSON() ([]byte, error) {
	if e.Window == "" {
		e.Window = MainWindowExpression
	}
	action, err := json.Marshal(e.Action)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(action, &fields); err != nil {
		return nil, err
	}
	fields["at_ms"], _ = json.Marshal(e.AtMS)
	if e.Window != MainWindowExpression {
		fields["window"], _ = json.Marshal(e.Window)
	}
	return json.Marshal(fields)
}

func (a *ReplayAction) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&raw); err != nil {
		return err
	}
	if len(raw) == 0 {
		return errors.New("replay action must be an object")
	}
	var kind string
	if b, ok := raw["action"]; !ok || json.Unmarshal(b, &kind) != nil || kind == "" {
		return errors.New("replay action requires action")
	}
	allowed := map[string]bool{"action": true, "x": true, "y": true, "button": true, "shift": true, "delta": true, "key": true, "text": true, "focused": true, "name": true, "inspect": true, "frame": true, "expression": true}
	for k := range raw {
		if !allowed[k] {
			return fmt.Errorf("replay action: unknown field %q", k)
		}
	}
	*a = ReplayAction{Action: kind}
	getI := func(name string, dst *int32) error {
		if b, ok := raw[name]; ok {
			return json.Unmarshal(b, dst)
		}
		return nil
	}
	requiredI := func(name string, dst *int32) error {
		b, ok := raw[name]
		if !ok {
			return fmt.Errorf("replay %s requires %s", kind, name)
		}
		return json.Unmarshal(b, dst)
	}
	switch kind {
	case ActionPointerMove, ActionPointerDown, ActionPointerUp, ActionClick, ActionDoubleClick, ActionWheel:
		if err := requiredI("x", &a.X); err != nil {
			return err
		}
		if err := requiredI("y", &a.Y); err != nil {
			return err
		}
		if kind == ActionPointerDown || kind == ActionPointerUp {
			if err := getI("button", &a.Button); err != nil {
				return err
			}
		}
		if kind == ActionWheel {
			if err := requiredI("delta", &a.Delta); err != nil {
				return err
			}
		}
		if err := getI("shift", &a.Shift); err != nil {
			return err
		}
	case ActionPointerLeave:
	case ActionKeyDown, ActionKeyUp:
		if err := requiredI("key", &a.Key); err != nil {
			return err
		}
		if err := getI("shift", &a.Shift); err != nil {
			return err
		}
	case ActionText:
		b, ok := raw["text"]
		if !ok || json.Unmarshal(b, &a.Text) != nil {
			return fmt.Errorf("replay %s requires text", kind)
		}
	case ActionFocus:
		b, ok := raw["focused"]
		if !ok || json.Unmarshal(b, &a.Focused) != nil {
			return fmt.Errorf("replay %s requires focused", kind)
		}
	case ActionCheckpoint:
		b, ok := raw["name"]
		if !ok || json.Unmarshal(b, &a.Name) != nil {
			return fmt.Errorf("replay checkpoint requires name")
		}
		if b, ok := raw["inspect"]; ok && json.Unmarshal(b, &a.Inspect) != nil {
			return fmt.Errorf("replay checkpoint inspect must be an array")
		}
		if b, ok := raw["frame"]; ok && string(b) != "null" {
			a.HasFrame = true
			if json.Unmarshal(b, &a.Frame) != nil {
				return fmt.Errorf("replay checkpoint frame must be a string")
			}
		}
	case ActionAssert:
		b, ok := raw["expression"]
		if !ok || json.Unmarshal(b, &a.Expression) != nil {
			return fmt.Errorf("replay assert requires expression")
		}
	default:
		return fmt.Errorf("replay action: unknown action %q", kind)
	}
	return validateAction(*a, 0)
}

func (e *ReplayEntry) UnmarshalJSON(data []byte) error {
	var raw struct {
		AtMS   *uint64         `json:"at_ms"`
		Window string          `json:"window"`
		Action json.RawMessage `json:"-"`
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for k := range fields {
		if k != "at_ms" && k != "window" && k != "action" && k != "x" && k != "y" && k != "button" && k != "shift" && k != "delta" && k != "key" && k != "text" && k != "focused" && k != "name" && k != "inspect" && k != "frame" && k != "expression" {
			return fmt.Errorf("replay entry: unknown field %q", k)
		}
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw.AtMS == nil {
		return errors.New("replay entry requires at_ms")
	}
	actionFields := make(map[string]json.RawMessage, len(fields))
	for key, value := range fields {
		if key != "at_ms" && key != "window" {
			actionFields[key] = value
		}
	}
	actionData, err := json.Marshal(actionFields)
	if err != nil {
		return err
	}
	var action ReplayAction
	if err := json.Unmarshal(actionData, &action); err != nil {
		return err
	}
	window := raw.Window
	if window == "" {
		window = MainWindowExpression
	}
	*e = ReplayEntry{AtMS: *raw.AtMS, Window: window, Action: action}
	return nil
}

func decodeReplay(data []byte) (*Replay, error) {
	var entries []ReplayEntry
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&entries); err != nil {
		return nil, fmt.Errorf("parse replay: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, errors.New("parse replay: trailing JSON")
	}
	return &Replay{Entries: entries}, nil
}

func ReadReplay(r io.Reader) (*Replay, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read replay: %w", err)
	}
	return decodeReplay(b)
}
func ParseReplay(data []byte) (*Replay, error) { return decodeReplay(data) }

func validateAction(a ReplayAction, index int) error {
	switch a.Action {
	case ActionPointerDown, ActionPointerUp:
		if a.Button < 0 || a.Button > 2 {
			return fmt.Errorf("replay action %d: button must be 0, 1, or 2", index)
		}
	case ActionCheckpoint:
		if strings.TrimSpace(a.Name) == "" {
			return fmt.Errorf("replay action %d: empty checkpoint name", index)
		}
		for _, expression := range a.Inspect {
			if strings.TrimSpace(expression) == "" {
				return fmt.Errorf("replay action %d: empty inspect expression", index)
			}
		}
	case ActionAssert:
		if strings.TrimSpace(a.Expression) == "" {
			return fmt.Errorf("replay action %d: empty assertion", index)
		}
	case ActionText:
	case ActionPointerMove, ActionClick, ActionDoubleClick, ActionPointerLeave, ActionWheel, ActionKeyDown, ActionKeyUp, ActionFocus:
	default:
		return fmt.Errorf("replay action %d: unknown action %q", index, a.Action)
	}
	return nil
}

// Validate checks the complete replay without mutating or executing session state.
func (r *Replay) Validate(session *runtime.Session, end ...uint64) error {
	if r == nil {
		return errors.New("nil replay")
	}
	if session == nil || session.Services == nil {
		return errors.New("replay validation requires a session")
	}
	previous := session.Services.TimeMS
	for i, entry := range r.Entries {
		if entry.AtMS < previous {
			return fmt.Errorf("replay action %d: timestamps must be nondecreasing", i)
		}
		previous = entry.AtMS
		if strings.TrimSpace(entry.Window) == "" {
			return fmt.Errorf("replay action %d: empty window expression", i)
		}
		if err := validateAction(entry.Action, i); err != nil {
			return err
		}
	}
	if len(end) > 1 {
		return errors.New("replay validation accepts at most one end time")
	}
	if len(end) == 1 && end[0] < previous {
		return fmt.Errorf("advance time %d precedes final replay timestamp %d", end[0], previous)
	}
	return nil
}

func (r *Replay) Run(session *runtime.Session, stepMS uint64, roots ...[]tjs.Value) (ReplayResult, error) {
	var result ReplayResult
	if err := r.Validate(session); err != nil {
		return result, err
	}
	if stepMS == 0 {
		return result, errors.New("replay step must be positive")
	}
	result.FinalTimeMS = session.Services.TimeMS
	var gcRoots []tjs.Value
	if len(roots) != 0 {
		gcRoots = roots[0]
	}
	record := func(at uint64) error {
		frame, err := session.Frame()
		if err != nil {
			return fmt.Errorf("capture frame at %d ms: %w", at, err)
		}
		result.Frames = append(result.Frames, summarizeFrame(at, frame))
		audio := session.TakeAudio()
		if len(audio) != 0 {
			result.Audio = append(result.Audio, summarizeAudio(at, audio))
		}
		return nil
	}
	current := session.Services.TimeMS
	requested := current
	messageStart := replayMessageCount(session)
	if err := session.Tick(requested); err != nil {
		context := fmt.Sprintf("replay initial tick from %d ms to %d ms", current, requested)
		return result, newReplayError(session, context, err, messageStart)
	}
	result.FinalTimeMS = session.Services.TimeMS
	if err := record(session.Services.TimeMS); err != nil {
		return result, err
	}
	for i, entry := range r.Entries {
		for session.Services.TimeMS < entry.AtMS {
			next := session.Services.TimeMS + stepMS
			if next < session.Services.TimeMS || next > entry.AtMS {
				next = entry.AtMS
			}
			messageStart := replayMessageCount(session)
			current := session.Services.TimeMS
			context := fmt.Sprintf("replay action %d at %d ms: tick from %d ms to %d ms", i, entry.AtMS, current, next)
			if err := session.Tick(next); err != nil {
				return result, newReplayError(session, context, err, messageStart)
			}
			result.FinalTimeMS = session.Services.TimeMS
			if err := record(next); err != nil {
				return result, newReplayError(session, context, err, messageStart)
			}
		}
		messageStart := replayMessageCount(session)
		if err := applyReplayAction(session, entry, &result); err != nil {
			context := fmt.Sprintf("replay action %d at %d ms", i, entry.AtMS)
			return result, newReplayError(session, context, err, messageStart)
		}
		_ = gcRoots // retained for API parity; runtime GC is host-owned between ticks.
	}
	result.FinalTimeMS = session.Services.TimeMS
	return result, nil
}

func applyReplayAction(session *runtime.Session, entry ReplayEntry, result *ReplayResult) error {
	a := entry.Action
	switch a.Action {
	case ActionAssert:
		value, err := session.Evaluate(a.Expression)
		if err != nil {
			return fmt.Errorf("evaluate assertion %q: %w", a.Expression, err)
		}
		ok, err := value.Truth()
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("assertion failed: %s", a.Expression)
		}
		return nil
	case ActionCheckpoint:
		cp := ReplayCheckpoint{Name: a.Name, AtMS: session.Services.TimeMS, Inspect: make([]ReplayInspect, 0, len(a.Inspect))}
		for _, expression := range a.Inspect {
			value, err := session.Evaluate(expression)
			if err != nil {
				return fmt.Errorf("evaluate %q: %w", expression, err)
			}
			cp.Inspect = append(cp.Inspect, ReplayInspect{Expression: expression, Value: valueJSON(session, value)})
		}
		if a.HasFrame {
			frame, err := session.Frame()
			if err != nil {
				return fmt.Errorf("capture checkpoint frame: %w", err)
			}
			summary := summarizeFrame(session.Services.TimeMS, frame)
			cp.Frame = &summary
			result.Frames = append(result.Frames, summary)
		}
		result.Checkpoints = append(result.Checkpoints, cp)
		return nil
	default:
		window, err := session.Evaluate(entry.Window)
		if err != nil {
			return fmt.Errorf("evaluate window %q: %w", entry.Window, err)
		}
		var event runtime.InputEvent
		switch a.Action {
		case ActionPointerMove:
			event = runtime.InputEvent{Kind: runtime.InputPointerMove, X: a.X, Y: a.Y, Shift: a.Shift}
		case ActionPointerDown:
			event = runtime.InputEvent{Kind: runtime.InputPointerDown, X: a.X, Y: a.Y, Button: a.Button, Shift: a.Shift}
		case ActionPointerUp:
			event = runtime.InputEvent{Kind: runtime.InputPointerUp, X: a.X, Y: a.Y, Button: a.Button, Shift: a.Shift}
		case ActionClick:
			event = runtime.InputEvent{Kind: runtime.InputClick, X: a.X, Y: a.Y}
		case ActionDoubleClick:
			event = runtime.InputEvent{Kind: runtime.InputDoubleClick, X: a.X, Y: a.Y}
		case ActionPointerLeave:
			event = runtime.InputEvent{Kind: runtime.InputPointerLeave}
		case ActionWheel:
			event = runtime.InputEvent{Kind: runtime.InputWheel, X: a.X, Y: a.Y, Delta: a.Delta, Shift: a.Shift}
		case ActionKeyDown:
			event = runtime.InputEvent{Kind: runtime.InputKeyDown, Key: a.Key, Shift: a.Shift}
		case ActionKeyUp:
			event = runtime.InputEvent{Kind: runtime.InputKeyUp, Key: a.Key, Shift: a.Shift}
		case ActionText:
			event = runtime.InputEvent{Kind: runtime.InputText, Text: a.Text}
		case ActionFocus:
			event = runtime.InputEvent{Kind: runtime.InputFocus, Focused: a.Focused}
		default:
			return fmt.Errorf("unsupported action %q", a.Action)
		}
		return session.Input(window, event)
	}
}

func summarizeFrame(at uint64, frame runtime.FrameSnapshot) ReplayFrameSummary {
	sum := sha256.Sum256(frame.RGBA)
	return ReplayFrameSummary{AtMS: at, Width: frame.Width, Height: frame.Height, RGBABytes: len(frame.RGBA), SHA256: hex.EncodeToString(sum[:])}
}
func summarizeAudio(at uint64, samples []float32) ReplayAudioSummary {
	h := sha256.New()
	var b [4]byte
	for _, sample := range samples {
		binary.LittleEndian.PutUint32(b[:], math.Float32bits(sample))
		_, _ = h.Write(b[:])
	}
	return ReplayAudioSummary{AtMS: at, Samples: len(samples), SHA256: hex.EncodeToString(h.Sum(nil))}
}

func valueJSON(session *runtime.Session, value tjs.Value) any {
	switch value.Kind() {
	case tjs.KindVoid:
		return nil
	case tjs.KindInteger:
		return value.IntegerValue()
	case tjs.KindReal:
		return value.RealValue()
	case tjs.KindString:
		return value.Text()
	case tjs.KindOctet:
		return value.Octets()
	default:
		serialized, err := session.VM.Serialize(value)
		if err != nil {
			return value.Text()
		}
		return serialized
	}
}

// MarshalResult writes stable JSON in struct field order followed by a newline.
func MarshalResult(w io.Writer, result ReplayResult) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(result)
}

// MarshalJSON is explicit to keep nil collections as stable empty arrays.
func (r ReplayResult) MarshalJSON() ([]byte, error) {
	type alias ReplayResult
	x := alias(r)
	if x.Frames == nil {
		x.Frames = []ReplayFrameSummary{}
	}
	if x.Audio == nil {
		x.Audio = []ReplayAudioSummary{}
	}
	if x.Checkpoints == nil {
		x.Checkpoints = []ReplayCheckpoint{}
	}
	return json.Marshal(x)
}
