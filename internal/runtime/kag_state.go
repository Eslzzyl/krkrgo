package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/eslzzyl/krkrgo/internal/kag"
	"github.com/eslzzyl/krkrgo/internal/tjs"
)

type KAGMacroFrameState struct {
	Name      string            `json:"name"`
	Position  int               `json:"position"`
	Tokens    []kag.Token       `json:"tokens"`
	Arguments map[string]string `json:"arguments,omitempty"`
}

type KAGScenarioCallState struct {
	Scenario kag.Scenario `json:"scenario"`
	State    KAGState     `json:"state"`
}

// KAGState is a portable parser checkpoint. Calls is copied on every boundary;
// callers may safely retain or serialize a returned state.
type KAGState struct {
	Storage               string                     `json:"storage"`
	Position              int                        `json:"position"`
	CurrentLine           int                        `json:"current_line,omitempty"`
	Calls                 []int                      `json:"calls,omitempty"`
	Fingerprint           string                     `json:"fingerprint,omitempty"`
	Conditions            []KAGConditionState        `json:"conditions,omitempty"`
	ConditionCalls        [][]KAGConditionState      `json:"condition_calls,omitempty"`
	IgnoreDepth           int                        `json:"ignore_depth,omitempty"`
	Ended                 bool                       `json:"ended,omitempty"`
	IgnoreCR              bool                       `json:"ignore_cr,omitempty"`
	MacroFrames           []KAGMacroFrameState       `json:"macro_frames,omitempty"`
	MacroDefinitions      map[string][]kag.Token     `json:"macro_definitions,omitempty"`
	ParamMacroDefinitions map[string][]kag.Attribute `json:"param_macro_definitions,omitempty"`
	CrossScenarioCalls    []KAGScenarioCallState     `json:"cross_scenario_calls,omitempty"`
	LegacyText            []KAGEvent                 `json:"legacy_text,omitempty"`
}

// KAGParserState is the descriptive spelling used by host save systems.
type KAGParserState = KAGState

func newKAGState(scenario kag.Scenario) KAGState {
	return KAGState{Storage: scenario.Storage, Fingerprint: scenarioFingerprint(scenario)}
}

func scenarioFingerprint(scenario kag.Scenario) string {
	// Physical lines affect curLineStr and therefore checkpoint identity.
	data, err := json.Marshal(struct {
		Storage     string      `json:"storage"`
		Tokens      []kag.Token `json:"tokens"`
		SourceLines []string    `json:"source_lines,omitempty"`
	}{scenario.Storage, scenario.Tokens, scenario.SourceLines})
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func legacyScenarioFingerprint(scenario kag.Scenario) string {
	// Accept checkpoints written before physical source lines were fingerprinted.
	data, err := json.Marshal(struct {
		Storage string      `json:"storage"`
		Tokens  []kag.Token `json:"tokens"`
	}{scenario.Storage, scenario.Tokens})
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
func cloneKAGTokens(tokens []kag.Token) []kag.Token {
	cloned := append([]kag.Token(nil), tokens...)
	for i := range cloned {
		if tokens[i].Tag != nil {
			tag := *tokens[i].Tag
			tag.Attributes = append([]kag.Attribute(nil), tag.Attributes...)
			cloned[i].Tag = &tag
		}
		if tokens[i].Label != nil {
			label := *tokens[i].Label
			if label.Caption != nil {
				caption := *label.Caption
				label.Caption = &caption
			}
			cloned[i].Label = &label
		}
		if tokens[i].InlineScript != nil {
			script := *tokens[i].InlineScript
			cloned[i].InlineScript = &script
		}
	}
	return cloned
}

func cloneKAGScenario(scenario kag.Scenario) kag.Scenario {
	scenario.Tokens = cloneKAGTokens(scenario.Tokens)
	scenario.SourceLines = append([]string(nil), scenario.SourceLines...)
	if scenario.Labels != nil {
		labels := make(map[string]int, len(scenario.Labels))
		for name, position := range scenario.Labels {
			labels[name] = position
		}
		scenario.Labels = labels
	}
	return scenario
}

func cloneKAGCallState(state KAGState) KAGState {
	cloned := state
	cloned.Calls = append([]int(nil), state.Calls...)
	cloned.LegacyText = append([]KAGEvent(nil), state.LegacyText...)
	cloned.Conditions = append([]KAGConditionState(nil), state.Conditions...)
	cloned.ConditionCalls = make([][]KAGConditionState, len(state.ConditionCalls))
	for i := range state.ConditionCalls {
		cloned.ConditionCalls[i] = append([]KAGConditionState(nil), state.ConditionCalls[i]...)
	}
	cloned.MacroFrames = make([]KAGMacroFrameState, len(state.MacroFrames))
	for i, frame := range state.MacroFrames {
		arguments := make(map[string]string, len(frame.Arguments))
		for name, value := range frame.Arguments {
			arguments[name] = value
		}
		cloned.MacroFrames[i] = KAGMacroFrameState{Name: frame.Name, Position: frame.Position, Tokens: cloneKAGTokens(frame.Tokens), Arguments: arguments}
	}
	cloned.MacroDefinitions = nil
	cloned.ParamMacroDefinitions = nil
	cloned.CrossScenarioCalls = nil
	return cloned
}

func (p *KAGParser) Store() KAGState { return p.storeState(true, true) }

func (p *KAGParser) storeCallFrameState() KAGState { return p.storeState(false, false) }

func (p *KAGParser) storeState(includeDefinitions, includeCrossCalls bool) KAGState {
	if p == nil || p.parser == nil {
		return KAGState{}
	}
	cursor := p.parser.State()
	state := p.state
	state.Storage = p.parser.Scenario().Storage
	state.Position = cursor.Position
	state.CurrentLine = cursor.CurrentLine
	state.Calls = append([]int(nil), cursor.Calls...)
	state.Fingerprint = scenarioFingerprint(p.parser.Scenario())
	state.Conditions = make([]KAGConditionState, len(p.conditions))
	for i, condition := range p.conditions {
		state.Conditions[i] = KAGConditionState{ParentActive: condition.parentActive, BranchTaken: condition.branchTaken, Active: condition.active}
	}
	state.ConditionCalls = make([][]KAGConditionState, len(p.conditionCalls))
	for i, frame := range p.conditionCalls {
		state.ConditionCalls[i] = make([]KAGConditionState, len(frame))
		for j, condition := range frame {
			state.ConditionCalls[i][j] = KAGConditionState{ParentActive: condition.parentActive, BranchTaken: condition.branchTaken, Active: condition.active}
		}
	}
	state.MacroFrames = make([]KAGMacroFrameState, len(p.macroFrames))
	for i, frame := range p.macroFrames {
		arguments := make(map[string]string, len(frame.arguments))
		for name, value := range frame.arguments {
			arguments[name] = value
		}
		state.MacroFrames[i] = KAGMacroFrameState{Name: frame.name, Position: frame.position, Tokens: append([]kag.Token(nil), frame.tokens...), Arguments: arguments}
	}
	state.MacroDefinitions = nil
	state.ParamMacroDefinitions = nil
	if includeDefinitions {
		state.MacroDefinitions = make(map[string][]kag.Token, len(p.macroDefs))
		for name, tokens := range p.macroDefs {
			state.MacroDefinitions[name] = append([]kag.Token(nil), tokens...)
		}
		state.ParamMacroDefinitions = make(map[string][]kag.Attribute, len(p.paramMacroDefs))
		for name, attributes := range p.paramMacroDefs {
			state.ParamMacroDefinitions[name] = append([]kag.Attribute(nil), attributes...)
		}
	}
	state.CrossScenarioCalls = nil
	if includeCrossCalls {
		state.CrossScenarioCalls = make([]KAGScenarioCallState, len(p.crossCalls))
		for i, frame := range p.crossCalls {
			state.CrossScenarioCalls[i] = KAGScenarioCallState{Scenario: cloneKAGScenario(frame.parser.Scenario()), State: cloneKAGCallState(frame.state)}
		}
	}
	state.IgnoreDepth = p.ignoreDepth
	state.LegacyText = append([]KAGEvent(nil), p.legacyText...)
	state.Ended = p.ended
	state.IgnoreCR = p.ignoreCR
	return state
}

func (p *KAGParser) StoreState() KAGState { return p.Store() }

// StoreObject is the native-compatible state representation. Values are
// ordinary TJS dictionary members so a VM can persist or inspect a checkpoint.
func (p *KAGParser) StoreObject() *tjs.Object {
	state := p.Store()
	object := tjs.NewObject(tjs.CategoryDictionary)
	_ = object.Set("storage", tjs.StringValue(state.Storage))
	_ = object.Set("storageName", tjs.StringValue(state.Storage))
	_ = object.Set("position", tjs.IntegerValue(int64(state.Position)))
	_ = object.Set("current_line", tjs.IntegerValue(int64(state.CurrentLine)))
	_ = object.Set("calls", tjs.StringValue(joinCallPositions(state.Calls)))
	_ = object.Set("fingerprint", tjs.StringValue(state.Fingerprint))
	conditions, _ := json.Marshal(state.Conditions)
	_ = object.Set("conditions", tjs.StringValue(string(conditions)))
	conditionCalls, _ := json.Marshal(state.ConditionCalls)
	_ = object.Set("condition_calls", tjs.StringValue(string(conditionCalls)))
	_ = object.Set("ignore_depth", tjs.IntegerValue(int64(state.IgnoreDepth)))
	_ = object.Set("ended", tjs.IntegerValue(boolInt(state.Ended)))
	_ = object.Set("ignore_cr", tjs.IntegerValue(boolInt(state.IgnoreCR)))
	macroFrames, _ := json.Marshal(state.MacroFrames)
	_ = object.Set("macro_frames", tjs.StringValue(string(macroFrames)))
	macroDefinitions, _ := json.Marshal(state.MacroDefinitions)
	_ = object.Set("macro_definitions", tjs.StringValue(string(macroDefinitions)))
	paramMacroDefinitions, _ := json.Marshal(state.ParamMacroDefinitions)
	_ = object.Set("param_macro_definitions", tjs.StringValue(string(paramMacroDefinitions)))
	crossScenarioCalls, _ := json.Marshal(state.CrossScenarioCalls)
	legacyText, _ := json.Marshal(state.LegacyText)
	_ = object.Set("legacy_text", tjs.StringValue(string(legacyText)))
	_ = object.Set("cross_scenario_calls", tjs.StringValue(string(crossScenarioCalls)))
	return object
}
func (p *KAGParser) StoreTJS() *tjs.Object { return p.StoreObject() }

func joinCallPositions(calls []int) string {
	if len(calls) == 0 {
		return ""
	}
	parts := make([]string, len(calls))
	for i, call := range calls {
		parts[i] = strconv.Itoa(call)
	}
	return strings.Join(parts, ",")
}
func parseCallPositions(value string) ([]int, error) {
	if value == "" {
		return nil, nil
	}
	parts := strings.Split(value, ",")
	calls := make([]int, len(parts))
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return nil, fmt.Errorf("invalid KAG call position")
		}
		calls[i] = n
	}
	return calls, nil
}

// RestoreObject accepts the dictionary returned by StoreObject and validates
// its fields before resolving the scenario and applying the checkpoint.
func (p *KAGParser) RestoreObject(value any) error {
	object, ok := value.(*tjs.Object)
	if !ok || object == nil {
		return fmt.Errorf("unsupported KAG restore state %T", value)
	}
	get := func(name string) (tjs.Value, error) {
		v, exists := object.Get(name)
		if !exists {
			return tjs.Value{}, fmt.Errorf("KAG state missing %q", name)
		}
		return v, nil
	}
	storage, err := get("storage")
	if err != nil {
		return err
	}
	position, err := get("position")
	if err != nil {
		return err
	}
	callsValue, err := get("calls")
	if err != nil {
		return err
	}
	fingerprintValue, err := get("fingerprint")
	if err != nil {
		return err
	}
	if storage.Kind() != tjs.KindString || position.Kind() != tjs.KindInteger || callsValue.Kind() != tjs.KindString || fingerprintValue.Kind() != tjs.KindString {
		return fmt.Errorf("invalid KAG state member types")
	}
	state := KAGState{Storage: storage.Text(), Position: int(position.IntegerValue()), Calls: nil, Fingerprint: fingerprintValue.Text()}
	state.Calls, err = parseCallPositions(callsValue.Text())
	if err != nil {
		return err
	}
	if currentLineValue, ok := object.Get("current_line"); ok {
		if currentLineValue.Kind() != tjs.KindInteger || currentLineValue.IntegerValue() < 0 {
			return fmt.Errorf("invalid KAG current line type")
		}
		state.CurrentLine = int(currentLineValue.IntegerValue())
	}
	if conditionsValue, ok := object.Get("conditions"); ok {
		if conditionsValue.Kind() != tjs.KindString {
			return fmt.Errorf("invalid KAG conditions type")
		}
		if conditionsValue.Text() != "" {
			if err := json.Unmarshal([]byte(conditionsValue.Text()), &state.Conditions); err != nil {
				return fmt.Errorf("invalid KAG conditions: %w", err)
			}
		}
	}
	if conditionCallsValue, ok := object.Get("condition_calls"); ok {
		if conditionCallsValue.Kind() != tjs.KindString {
			return fmt.Errorf("invalid KAG condition calls type")
		}
		if conditionCallsValue.Text() != "" {
			if err := json.Unmarshal([]byte(conditionCallsValue.Text()), &state.ConditionCalls); err != nil {
				return fmt.Errorf("invalid KAG condition calls: %w", err)
			}
		}
	}
	if ignoreCRValue, ok := object.Get("ignore_cr"); ok {
		if ignoreCRValue.Kind() != tjs.KindInteger {
			return fmt.Errorf("invalid KAG ignoreCR type")
		}
		state.IgnoreCR = ignoreCRValue.IntegerValue() != 0
	}
	if ignoreDepthValue, ok := object.Get("ignore_depth"); ok {
		if ignoreDepthValue.Kind() != tjs.KindInteger || ignoreDepthValue.IntegerValue() < 0 || ignoreDepthValue.IntegerValue() > 128 {
			return fmt.Errorf("invalid KAG ignore depth")
		}
		state.IgnoreDepth = int(ignoreDepthValue.IntegerValue())
	}
	if endedValue, ok := object.Get("ended"); ok {
		if endedValue.Kind() != tjs.KindInteger {
			return fmt.Errorf("invalid KAG ended type")
		}
		state.Ended = endedValue.IntegerValue() != 0
	}
	if macroFramesValue, ok := object.Get("macro_frames"); ok {
		if macroFramesValue.Kind() != tjs.KindString {
			return fmt.Errorf("invalid KAG macro frames type")
		}
		if macroFramesValue.Text() != "" && macroFramesValue.Text() != "null" {
			if err := json.Unmarshal([]byte(macroFramesValue.Text()), &state.MacroFrames); err != nil {
				return fmt.Errorf("invalid KAG macro frames: %w", err)
			}
		}
	}
	if legacyTextValue, ok := object.Get("legacy_text"); ok {
		if legacyTextValue.Kind() != tjs.KindString {
			return fmt.Errorf("invalid KAG legacy text type")
		}
		if legacyTextValue.Text() != "" && legacyTextValue.Text() != "null" {
			if err := json.Unmarshal([]byte(legacyTextValue.Text()), &state.LegacyText); err != nil {
				return fmt.Errorf("invalid KAG legacy text: %w", err)
			}
		}
	}
	if macroDefinitionsValue, ok := object.Get("macro_definitions"); ok {
		if macroDefinitionsValue.Kind() != tjs.KindString {
			return fmt.Errorf("invalid KAG macro definitions type")
		}
		if macroDefinitionsValue.Text() != "" && macroDefinitionsValue.Text() != "null" {
			if err := json.Unmarshal([]byte(macroDefinitionsValue.Text()), &state.MacroDefinitions); err != nil {
				return fmt.Errorf("invalid KAG macro definitions: %w", err)
			}
		}
	}
	if paramMacroDefinitionsValue, ok := object.Get("param_macro_definitions"); ok {
		if paramMacroDefinitionsValue.Kind() != tjs.KindString {
			return fmt.Errorf("invalid KAG parameter macro definitions type")
		}
		if paramMacroDefinitionsValue.Text() != "" && paramMacroDefinitionsValue.Text() != "null" {
			if err := json.Unmarshal([]byte(paramMacroDefinitionsValue.Text()), &state.ParamMacroDefinitions); err != nil {
				return fmt.Errorf("invalid KAG parameter macro definitions: %w", err)
			}
		}
	}
	if crossScenarioCallsValue, ok := object.Get("cross_scenario_calls"); ok {
		if crossScenarioCallsValue.Kind() != tjs.KindString {
			return fmt.Errorf("invalid KAG cross-scenario calls type")
		}
		if crossScenarioCallsValue.Text() != "" && crossScenarioCallsValue.Text() != "null" {
			if err := json.Unmarshal([]byte(crossScenarioCallsValue.Text()), &state.CrossScenarioCalls); err != nil {
				return fmt.Errorf("invalid KAG cross-scenario calls: %w", err)
			}
		}
	}
	// The native KAGParser restore operation reloads the saved scenario before
	// restoring its cursor. In the runtime, this callback resolves storage from
	// the project; it also makes restore work after KAGParser.clear().
	if state.Storage == "" {
		if p.StorageName() != "" {
			if err := p.clear(); err != nil {
				return err
			}
		}
	} else if p.callbacks != nil && p.callbacks.OnScenarioLoad != nil {
		// Reload only when the current scenario no longer matches the saved
		// one. An inline scenario with the same storage name and fingerprint
		// is already loaded, so restore works without a storage device.
		current := p.parser.Scenario()
		if p.StorageName() != state.Storage || len(current.Tokens) == 0 || scenarioFingerprint(current) != state.Fingerprint {
			if err := p.LoadScenario(state.Storage, ""); err != nil {
				return fmt.Errorf("load KAG restore scenario %q: %w", state.Storage, err)
			}
		}
	}
	return p.RestoreState(state)
}

// RestoreState validates both cursor bounds and, when present, scenario
// identity. An empty fingerprint is accepted for hand-written legacy states.
func (p *KAGParser) RestoreState(state KAGState) error {
	if p == nil || p.parser == nil || !p.alive {
		return fmt.Errorf("nil KAG parser")
	}
	frames, err := restoreKAGScenarioCallFrames(state.CrossScenarioCalls)
	if err != nil {
		return err
	}
	depth := len(state.Calls) + len(frames)
	for _, frame := range frames {
		depth += len(frame.parser.State().Calls)
	}
	if depth > 128 {
		return fmt.Errorf("invalid KAG call depth")
	}
	if err := p.restoreLocalState(state); err != nil {
		return err
	}
	p.crossCalls = frames
	return nil
}

func restoreKAGScenarioCallFrames(states []KAGScenarioCallState) ([]kagScenarioCallFrame, error) {
	if len(states) > 128 {
		return nil, fmt.Errorf("invalid KAG scenario call depth")
	}
	frames := make([]kagScenarioCallFrame, len(states))
	for i, saved := range states {
		if len(saved.State.CrossScenarioCalls) != 0 {
			return nil, fmt.Errorf("invalid nested KAG scenario call state")
		}
		scenario := cloneKAGScenario(saved.Scenario)
		parser := NewKAGParserFromScenario(scenario)
		if err := parser.restoreLocalState(saved.State); err != nil {
			return nil, fmt.Errorf("invalid KAG caller state: %w", err)
		}
		frames[i] = kagScenarioCallFrame{
			parser:        parser.parser,
			state:         parser.storeCallFrameState(),
			macroRanges:   parser.macroRanges,
			macroIndexErr: parser.macroIndexErr,
		}
	}
	return frames, nil
}

func (p *KAGParser) restoreLocalState(state KAGState) error {
	if p == nil || p.parser == nil {
		return fmt.Errorf("nil KAG parser")
	}
	scenario := p.parser.Scenario()
	if state.Storage != "" && state.Storage != scenario.Storage {
		return fmt.Errorf("KAG state belongs to storage %q, parser uses %q", state.Storage, scenario.Storage)
	}
	if state.Fingerprint != "" && state.Fingerprint != scenarioFingerprint(scenario) && state.Fingerprint != legacyScenarioFingerprint(scenario) {
		return fmt.Errorf("KAG state belongs to a different scenario")
	}
	if len(state.Conditions) > 128 || len(state.ConditionCalls) > 128 {
		return fmt.Errorf("invalid KAG conditional state")
	}
	if len(state.LegacyText) > 1_000_000 {
		return fmt.Errorf("invalid KAG legacy text queue")
	}
	for _, frame := range state.ConditionCalls {
		if len(frame) > 128 {
			return fmt.Errorf("invalid KAG conditional state")
		}
	}
	if len(state.MacroFrames) > 128 {
		return fmt.Errorf("invalid KAG macro frame depth")
	}
	for _, frame := range state.MacroFrames {
		if frame.Position < 0 || frame.Position > len(frame.Tokens) || len(frame.Tokens) > 1_000_000 {
			return fmt.Errorf("invalid KAG macro frame")
		}
	}
	if len(state.MacroDefinitions) > 1_000_000 || len(state.ParamMacroDefinitions) > 1_000_000 {
		return fmt.Errorf("invalid KAG macro definitions")
	}
	for _, tokens := range state.MacroDefinitions {
		if len(tokens) > 1_000_000 {
			return fmt.Errorf("invalid KAG macro definitions")
		}
	}
	if err := p.parser.Restore(kag.Cursor{
		Position:    state.Position,
		Calls:       append([]int(nil), state.Calls...),
		CurrentLine: state.CurrentLine,
	}); err != nil {
		return err
	}
	p.state = newKAGState(scenario)
	p.ended = state.Ended
	p.conditions = make([]kagCondition, len(state.Conditions))
	for i, condition := range state.Conditions {
		p.conditions[i] = kagCondition{parentActive: condition.ParentActive, branchTaken: condition.BranchTaken, active: condition.Active}
	}
	p.conditionCalls = make([][]kagCondition, len(state.ConditionCalls))
	for i, frame := range state.ConditionCalls {
		p.conditionCalls[i] = make([]kagCondition, len(frame))
		for j, condition := range frame {
			p.conditionCalls[i][j] = kagCondition{parentActive: condition.ParentActive, branchTaken: condition.BranchTaken, active: condition.Active}
		}
	}
	p.macroFrames = make([]kagMacroFrame, len(state.MacroFrames))
	for i, frame := range state.MacroFrames {
		p.macroFrames[i] = kagMacroFrame{name: frame.Name, position: frame.Position, tokens: cloneKAGTokens(frame.Tokens), arguments: make(map[string]string, len(frame.Arguments))}
		for name, value := range frame.Arguments {
			p.macroFrames[i].arguments[name] = value
		}
	}
	p.ignoreDepth = state.IgnoreDepth
	p.ignoreCR = state.IgnoreCR
	p.legacyText = append([]KAGEvent(nil), state.LegacyText...)
	if state.MacroDefinitions != nil {
		p.macroDefs = make(map[string][]kag.Token, len(state.MacroDefinitions))
		for name, tokens := range state.MacroDefinitions {
			p.macroDefs[name] = cloneKAGTokens(tokens)
		}
	}
	if state.ParamMacroDefinitions != nil {
		p.paramMacroDefs = make(map[string][]kag.Attribute, len(state.ParamMacroDefinitions))
		for name, attributes := range state.ParamMacroDefinitions {
			p.paramMacroDefs[name] = append([]kag.Attribute(nil), attributes...)
		}
	}
	return nil
}

func (p *KAGParser) RestoreKAGState(state KAGState) error { return p.RestoreState(state) }

// RestoreStoredState is an alias useful to save systems that distinguish
// checkpoint restoration from low-level cursor restoration.
func (p *KAGParser) RestoreStoredState(state KAGState) error { return p.RestoreState(state) }
