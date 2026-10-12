package runtime

import (
	"fmt"

	"github.com/eslzzyl/krkrgo/internal/kag"
)

// KAGParser is the runtime-facing incremental KAG parser.  The parser does not
// execute tags: callers consume tokens and dispatch them to the runtime.  This
// keeps parsing deterministic and makes a parser safe to use for save/restore.
type KAGParser struct {
	parser              *kag.Parser
	state               KAGState
	ignoreCR            bool
	processSpecialTags  bool
	multiLineTagEnabled bool
	debugLevel          int32
	alive               bool
	interrupted         bool
	callbacks           *KAGCallbacks
	conditions          []kagCondition
	conditionCalls      [][]kagCondition
	ignoreDepth         int
	ended               bool
	evaluator           KAGExpressionEvaluator
	valueEvaluator      KAGExpressionValueEvaluator
	macroDefs           map[string][]kag.Token
	macroRanges         map[int]int
	paramMacroDefs      map[string][]kag.Attribute
	macroFrames         []kagMacroFrame
	macroIndexErr       error
	crossCalls          []kagScenarioCallFrame
	legacyText          []KAGEvent
}

// KAGParserEx is the name used by the native extension and is an alias so both
// host integrations share exactly the same parser implementation.
type KAGParserEx = KAGParser

// NewKAGParser parses source once and returns a cursor at its beginning.
func NewKAGParser(storage, source string) (*KAGParser, error) {
	scenario, err := kag.Parse(storage, source)
	if err != nil {
		return nil, err
	}
	macros, ranges, pmacros, err := indexKAGMacros(scenario)
	if err != nil {
		return nil, err
	}
	p := NewKAGParserFromScenario(scenario)
	p.macroDefs, p.macroRanges, p.paramMacroDefs, p.macroIndexErr = macros, ranges, pmacros, nil
	return p, nil
}

// NewKAGParserEx constructs the native-compatible parser spelling.
func NewKAGParserEx(storage, source string) (*KAGParserEx, error) {
	return NewKAGParser(storage, source)
}

// NewKAGParserWithCallbacks parses a scenario and installs execution callbacks.
func NewKAGParserWithCallbacks(storage, source string, callbacks *KAGCallbacks) (*KAGParser, error) {
	p, err := NewKAGParser(storage, source)
	if err != nil {
		return nil, err
	}
	p.SetCallbacks(callbacks)
	return p, nil
}

// NewKAGParserFromScenario constructs a parser without reparsing a scenario.
func NewKAGParserFromScenario(scenario kag.Scenario) *KAGParser {
	macros, ranges, pmacros, err := indexKAGMacros(scenario)
	p := &KAGParser{
		parser:             kag.NewParser(scenario),
		alive:              true,
		processSpecialTags: true,
		debugLevel:         1,
		macroDefs:          macros,
		macroRanges:        ranges,
		paramMacroDefs:     pmacros,
		macroIndexErr:      err,
	}
	p.state = newKAGState(scenario)
	return p
}

// Scenario returns the immutable-by-convention parsed scenario.
func (p *KAGParser) Scenario() kag.Scenario {
	if p == nil || p.parser == nil {
		return kag.Scenario{}
	}
	return p.parser.Scenario()
}

// Interrupt schedules one synthetic interrupt tag before the next readable
// scenario token.
func (p *KAGParser) Interrupt() {
	if p != nil {
		p.interrupted = true
	}
}

// ResetInterrupt discards a pending synthetic interrupt tag.

func (p *KAGParser) ResetInterrupt() {
	if p != nil {
		p.interrupted = false
	}
}

func (p *KAGParser) hasReadableKAGToken() bool {
	if p == nil || p.parser == nil {
		return false
	}
	for i := range p.macroFrames {
		if p.macroFrames[i].position < len(p.macroFrames[i].tokens) {
			return true
		}
	}
	scenario := p.parser.Scenario()
	position := p.parser.State().Position
	return position >= 0 && position < len(scenario.Tokens)
}

// NextToken consumes one token, including text, labels, and newline tokens.
func (p *KAGParser) NextToken() *kag.Token {
	if p == nil || p.parser == nil {
		return nil
	}
	return p.parser.NextToken()
}

// Next is a concise alias for NextToken.
func (p *KAGParser) Next() *kag.Token { return p.NextToken() }

// NextTag consumes non-tag tokens until the next tag. Labels are retained in
// parser state and are not returned as tags, matching KAGParserEx::GetNextTag.
func (p *KAGParser) NextTag() (*kag.Tag, error) {
	for {
		token := p.NextToken()
		if token == nil {
			return nil, nil
		}
		if token.Kind != kag.KindTag {
			continue
		}
		if token.Tag == nil {
			return nil, fmt.Errorf("KAG tag token has no payload")
		}
		return token.Tag, nil
	}
}

// GetNextTag is the C++-style convenience API. Parse/dispatch errors are
// represented by a nil result; callers needing errors should use NextTag.
func (p *KAGParser) GetNextTag() *kag.Tag {
	tag, _ := p.NextTag()
	return tag
}

// Jump, Call, and ReturnFromCall expose deterministic label control flow.
func (p *KAGParser) Jump(label string) error {
	if err := p.ensureExecutable(); err != nil {
		return err
	}
	return p.parser.Jump(label)
}
func (p *KAGParser) GoToLabel(label string) error { return p.Jump(label) }
func (p *KAGParser) Call(label string) error {
	if err := p.ensureExecutable(); err != nil {
		return err
	}
	if p.kagCallDepth() >= 128 {
		return fmt.Errorf("KAG call depth exceeded")
	}
	// Keep this frame outside the scenario cursor: native callLabel callers
	// commonly load another storage after calling and must still be able to return.
	frame := kagScenarioCallFrame{
		parser:        p.parser,
		state:         p.storeCallFrameState(),
		macroRanges:   p.macroRanges,
		macroIndexErr: p.macroIndexErr,
	}
	if err := p.parser.Jump(label); err != nil {
		return err
	}
	p.crossCalls = append(p.crossCalls, frame)
	p.conditions = nil
	p.conditionCalls = nil
	return nil
}
func (p *KAGParser) CallLabel(label string) error { return p.Call(label) }

// clear drops the current scenario and every active parser frame while keeping
// parser options and macro definitions intact.
func (p *KAGParser) clear() error {
	if err := p.ensureExecutable(); err != nil {
		return err
	}
	scenario := kag.Scenario{}
	p.parser = kag.NewParser(scenario)
	p.state = newKAGState(scenario)
	p.conditions = nil
	p.conditionCalls = nil
	p.ignoreDepth = 0
	p.ended = false
	p.macroRanges = nil
	p.macroFrames = nil
	p.macroIndexErr = nil
	p.crossCalls = nil
	p.legacyText = nil
	return nil
}

func (p *KAGParser) popMacroArgs() error {
	if err := p.ensureExecutable(); err != nil {
		return err
	}
	last := len(p.macroFrames) - 1
	if last < 0 {
		return fmt.Errorf("KAG macro argument stack underflow")
	}
	p.macroFrames[last] = kagMacroFrame{}
	p.macroFrames = p.macroFrames[:last]
	return nil
}

func chargeKAGDictionaryCopies(budget *uint64, parser *KAGParser) error {
	for range parser.macroDefs {
		if err := chargeKAG(budget); err != nil {
			return err
		}
	}
	for range parser.paramMacroDefs {
		if err := chargeKAG(budget); err != nil {
			return err
		}
	}
	for _, frame := range parser.macroFrames {
		for range frame.arguments {
			if err := chargeKAG(budget); err != nil {
				return err
			}
		}
	}
	for _, call := range parser.crossCalls {
		for _, frame := range call.state.MacroFrames {
			for range frame.Arguments {
				if err := chargeKAG(budget); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func cloneKAGMacroFrames(frames []kagMacroFrame) []kagMacroFrame {
	if frames == nil {
		return nil
	}
	cloned := append([]kagMacroFrame(nil), frames...)
	for i, frame := range frames {
		if frame.arguments == nil {
			continue
		}
		cloned[i].arguments = make(map[string]string, len(frame.arguments))
		for name, value := range frame.arguments {
			cloned[i].arguments[name] = value
		}
	}
	return cloned
}

func cloneKAGConditionCalls(calls [][]kagCondition) [][]kagCondition {
	if calls == nil {
		return nil
	}
	cloned := make([][]kagCondition, len(calls))
	for i, call := range calls {
		cloned[i] = append([]kagCondition(nil), call...)
	}
	return cloned
}

func cloneKAGScenarioCallFrames(frames []kagScenarioCallFrame) ([]kagScenarioCallFrame, error) {
	if frames == nil {
		return nil, nil
	}
	cloned := make([]kagScenarioCallFrame, len(frames))
	for i, frame := range frames {
		if frame.parser == nil {
			return nil, fmt.Errorf("invalid KAG scenario call frame")
		}
		parser := kag.NewParser(frame.parser.Scenario())
		if err := parser.Restore(frame.parser.State()); err != nil {
			return nil, fmt.Errorf("invalid KAG scenario call frame: %w", err)
		}
		cloned[i] = kagScenarioCallFrame{
			parser:        parser,
			state:         cloneKAGCallState(frame.state),
			macroRanges:   frame.macroRanges,
			macroIndexErr: frame.macroIndexErr,
		}
	}
	return cloned, nil
}

func cloneKAGMacroDefinitions(source map[string][]kag.Token) map[string][]kag.Token {
	if source == nil {
		return nil
	}
	cloned := make(map[string][]kag.Token, len(source))
	for name, tokens := range source {
		cloned[name] = tokens
	}
	return cloned
}

func cloneKAGParamMacroDefinitions(source map[string][]kag.Attribute) map[string][]kag.Attribute {
	if source == nil {
		return nil
	}
	cloned := make(map[string][]kag.Attribute, len(source))
	for name, attributes := range source {
		cloned[name] = attributes
	}
	return cloned
}

func (p *KAGParser) assignFrom(source *KAGParser, budget *uint64) error {
	if err := p.ensureExecutable(); err != nil {
		return err
	}
	if err := source.ensureExecutable(); err != nil {
		return err
	}
	if source == p {
		clear(p.macroDefs)
		clear(p.paramMacroDefs)
		return nil
	}
	if err := chargeKAGDictionaryCopies(budget, source); err != nil {
		return err
	}

	copied := *source
	copied.parser = kag.NewParser(source.parser.Scenario())
	if err := copied.parser.Restore(source.parser.State()); err != nil {
		return err
	}
	copied.conditions = append([]kagCondition(nil), source.conditions...)
	copied.conditionCalls = cloneKAGConditionCalls(source.conditionCalls)
	copied.macroFrames = cloneKAGMacroFrames(source.macroFrames)
	crossCalls, err := cloneKAGScenarioCallFrames(source.crossCalls)
	if err != nil {
		return err
	}
	copied.crossCalls = crossCalls
	copied.macroDefs = cloneKAGMacroDefinitions(source.macroDefs)
	copied.paramMacroDefs = cloneKAGParamMacroDefinitions(source.paramMacroDefs)
	copied.legacyText = append([]KAGEvent(nil), source.legacyText...)

	// Keep the target's native-instance options and callbacks bound to its own
	// VM object. The parser state itself is copied from the source.
	copied.processSpecialTags = p.processSpecialTags
	copied.multiLineTagEnabled = p.multiLineTagEnabled
	copied.interrupted = p.interrupted
	copied.callbacks = p.callbacks
	copied.evaluator = p.evaluator
	copied.valueEvaluator = p.valueEvaluator
	copied.alive = p.alive
	*p = copied
	return nil
}

// ClearCallStack discards same- and cross-scenario return frames without moving the cursor.
func (p *KAGParser) ClearCallStack() error {
	if err := p.ensureExecutable(); err != nil {
		return err
	}
	cursor := p.parser.State()
	cursor.Calls = nil
	if err := p.parser.Restore(cursor); err != nil {
		return err
	}
	p.conditionCalls = nil
	p.macroFrames = nil
	p.crossCalls = nil
	return nil
}
func (p *KAGParser) ReturnFromCall() error {
	if err := p.ensureExecutable(); err != nil {
		return err
	}
	localCalls := len(p.parser.State().Calls)
	if n := len(p.crossCalls); n != 0 && len(p.crossCalls[n-1].state.Calls) >= localCalls {
		frame := p.crossCalls[n-1]
		if err := p.restoreScenarioCallFrame(frame); err != nil {
			return err
		}
		p.crossCalls = p.crossCalls[:n-1]
		return nil
	}
	if localCalls != 0 {
		if err := p.parser.ReturnFromCall(); err != nil {
			return err
		}
		if n := len(p.conditionCalls); n != 0 {
			p.conditions = p.conditionCalls[n-1]
			p.conditionCalls = p.conditionCalls[:n-1]
		} else {
			p.conditions = nil
		}
		return nil
	}
	if n := len(p.crossCalls); n != 0 {
		frame := p.crossCalls[n-1]
		if err := p.restoreScenarioCallFrame(frame); err != nil {
			return err
		}
		p.crossCalls = p.crossCalls[:n-1]
		return nil
	}
	return p.parser.ReturnFromCall()
}

// State returns the low-level cursor used by internal/kag.
func (p *KAGParser) State() kag.Cursor {
	if p == nil || p.parser == nil {
		return kag.Cursor{}
	}
	return p.parser.State()
}
func (p *KAGParser) Cursor() kag.Cursor { return p.State() }

// Restore accepts a low-level kag.Cursor, a KAGState, or a TJS dictionary
// produced by StoreObject. The broad input preserves both Go and native
// KAGParserEx call sites without weakening validation.
func (p *KAGParser) Restore(value any) error {
	switch state := value.(type) {
	case kag.Cursor:
		if err := p.ensureExecutable(); err != nil {
			return err
		}
		if err := p.parser.Restore(state); err != nil {
			return err
		}
		p.macroFrames = nil
		return nil
	case *KAGState:
		if state == nil {
			return fmt.Errorf("nil KAG state")
		}
		return p.RestoreState(*state)
	default:
		return p.RestoreObject(value)
	}
}

// Current label and source position mirror the native KAGParserEx accessors.
func (p *KAGParser) GetCurLabel() string { return p.CurrentLabel() }
func (p *KAGParser) CurrentLabel() string {
	if p == nil || p.parser == nil {
		return ""
	}
	s := p.parser.Scenario()
	position := p.parser.State().Position
	if position > len(s.Tokens) {
		position = len(s.Tokens)
	}
	for i := position; i >= 0; i-- {
		if i >= len(s.Tokens) {
			continue
		}
		if s.Tokens[i].Kind == kag.KindLabel && s.Tokens[i].Label != nil {
			return s.Tokens[i].Label.Name
		}
	}
	return ""
}
func (p *KAGParser) GetCurLine() int {
	if p == nil || p.parser == nil {
		return 0
	}
	return int(p.currentLocation().Line)
}
func (p *KAGParser) GetCurLineStr() string {
	if p == nil || p.parser == nil {
		return ""
	}
	return p.parser.CurrentLineStr()
}
func (p *KAGParser) CurrentLineStr() string { return p.GetCurLineStr() }
func (p *KAGParser) GetCurPos() int {
	if p == nil || p.parser == nil {
		return 0
	}
	return int(p.currentLocation().Column)
}
func (p *KAGParser) CurrentLine() int     { return p.GetCurLine() }
func (p *KAGParser) CurrentPosition() int { return p.GetCurPos() }
func (p *KAGParser) StorageName() string {
	if p == nil || p.parser == nil {
		return ""
	}
	return p.parser.Scenario().Storage
}
func (p *KAGParser) GetStorageName() string { return p.StorageName() }

func (p *KAGParser) currentLocation() (location struct{ Line, Column uint64 }) {
	s := p.parser.Scenario()
	pos := p.parser.State().Position
	if pos < 0 {
		pos = 0
	}
	if pos < len(s.Tokens) {
		x := s.Tokens[pos].Location
		return struct{ Line, Column uint64 }{x.Line, x.Column}
	}
	if len(s.Tokens) != 0 {
		x := s.Tokens[len(s.Tokens)-1].Location
		return struct{ Line, Column uint64 }{x.Line, x.Column}
	}
	return
}

// ScenarioFingerprint identifies the parsed scenario for state validation.
func (p *KAGParser) ScenarioFingerprint() string {
	if p == nil || p.parser == nil {
		return ""
	}
	return scenarioFingerprint(p.parser.Scenario())
}
