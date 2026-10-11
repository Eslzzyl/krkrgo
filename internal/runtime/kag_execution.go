package runtime

import (
	"errors"
	"fmt"
	"strings"

	"github.com/eslzzyl/krkrgo/internal/core"
	"github.com/eslzzyl/krkrgo/internal/kag"
	"github.com/eslzzyl/krkrgo/internal/tjs"
)

var (
	ErrKAGParserInvalid   = errors.New("KAG parser is invalid")
	ErrKAGExecutionBudget = fmt.Errorf("%w: KAG parser execution budget exhausted", ErrBudgetExceeded)
)

// KAGEventKind identifies one observable execution result.
type KAGEventKind uint8

const (
	KAGEventText KAGEventKind = iota
	KAGEventTag
	KAGEventLabel
	KAGEventNewline
	KAGEventEnd
	KAGEventScript
)

// Descriptive aliases keep callers from depending on the KAG prefix.
const (
	KAGTextEvent    = KAGEventText
	KAGTagEvent     = KAGEventTag
	KAGLabelEvent   = KAGEventLabel
	KAGNewlineEvent = KAGEventNewline
	KAGEndEvent     = KAGEventEnd
	KAGScriptEvent  = KAGEventScript
)

// KAGEvent is one visible unit delivered by Step and Run, including active
// inline scripts. Control directives are consumed and are not emitted.
type KAGEvent struct {
	Kind     KAGEventKind
	Token    kag.Token
	Text     string
	Script   string
	Tag      *kag.Tag
	Label    *kag.Label
	Location struct {
		Storage string
		Line    uint64
		Column  uint64
	}
}

// KAGExpressionEvaluator can replace the default TJS expression evaluator.
// It is useful when a host has a live VM/global scope to expose to conditions.
type KAGExpressionEvaluator func(expression string) (bool, error)

// KAGExpressionValueEvaluator evaluates deterministic embedded expressions.
// Hosts with a live VM may install one to expose their globals to @emb.
type KAGExpressionValueEvaluator func(expression string) (tjs.Value, error)

type kagMacroFrame struct {
	name      string
	position  int
	tokens    []kag.Token
	arguments map[string]string
}
type kagScenarioCallFrame struct {
	parser        *kag.Parser
	state         KAGState
	macroRanges   map[int]int
	macroIndexErr error
}

// KAGCallbacks receives visible execution events and scenario-load requests.
// OnScript receives the source file and 1-based first body line for an active
// inline script. A callback may invalidate the parser during event delivery.
type KAGCallbacks struct {
	OnScenarioLoad   func(parser *KAGParser, storage, source string) (string, error)
	OnScenarioLoaded func(parser *KAGParser, storage string) error
	OnText           func(parser *KAGParser, text string) error
	OnTag            func(parser *KAGParser, tag *kag.Tag) error
	OnLabel          func(parser *KAGParser, label *kag.Label) error
	OnScript         func(parser *KAGParser, script, storage string, startLine uint64) error
	OnEvent          func(parser *KAGParser, event KAGEvent) error
}

type KAGConditionState struct {
	ParentActive bool `json:"parent_active"`
	BranchTaken  bool `json:"branch_taken"`
	Active       bool `json:"active"`
}

type kagCondition struct {
	parentActive bool
	branchTaken  bool
	active       bool
}

// SetCallbacks installs execution callbacks. A nil value disables callbacks.
func (p *KAGParser) SetCallbacks(callbacks *KAGCallbacks) {
	if p != nil {
		p.callbacks = callbacks
	}
}
func (p *KAGParser) Callbacks() *KAGCallbacks {
	if p == nil {
		return nil
	}
	return p.callbacks
}
func (p *KAGParser) SetExpressionEvaluator(evaluator KAGExpressionEvaluator) {
	if p != nil {
		p.evaluator = evaluator
	}
}
func (p *KAGParser) SetExpressionValueEvaluator(evaluator KAGExpressionValueEvaluator) {
	if p != nil {
		p.valueEvaluator = evaluator
	}
}
func (p *KAGParser) Invalidate() {
	if p != nil {
		p.alive = false
	}
}
func (p *KAGParser) Alive() bool { return p != nil && p.alive && p.parser != nil }
func (p *KAGParser) SetTextCallback(callback func(*KAGParser, string) error) {
	if p == nil {
		return
	}
	if p.callbacks == nil {
		p.callbacks = &KAGCallbacks{}
	}
	p.callbacks.OnText = callback
}
func (p *KAGParser) SetTagCallback(callback func(*KAGParser, *kag.Tag) error) {
	if p == nil {
		return
	}
	if p.callbacks == nil {
		p.callbacks = &KAGCallbacks{}
	}
	p.callbacks.OnTag = callback
}
func (p *KAGParser) SetScenarioLoadCallback(callback func(*KAGParser, string, string) (string, error)) {
	if p == nil {
		return
	}
	if p.callbacks == nil {
		p.callbacks = &KAGCallbacks{}
	}
	p.callbacks.OnScenarioLoad = callback
}
func (p *KAGParser) SetScenarioLoadedCallback(callback func(*KAGParser, string) error) {
	if p == nil {
		return
	}
	if p.callbacks == nil {
		p.callbacks = &KAGCallbacks{}
	}
	p.callbacks.OnScenarioLoaded = callback
}

func (p *KAGParser) ensureExecutable() error {
	if !p.Alive() {
		return ErrKAGParserInvalid
	}
	return nil
}

func kagLocationError(location core.SourceLocation, err error) error {
	if err == nil {
		return nil
	}
	if location.Storage == "" {
		return fmt.Errorf("KAG line %d column %d: %w", location.Line, location.Column, err)
	}
	return fmt.Errorf("%s:%d:%d: %w", location.Storage, location.Line, location.Column, err)
}

func (p *KAGParser) kagEOFError(err error) error {
	if p == nil || p.parser == nil {
		return err
	}
	scenario := p.parser.Scenario()
	if len(scenario.Tokens) == 0 {
		return kagLocationError(core.SourceLocation{Storage: scenario.Storage}, err)
	}
	position := p.parser.State().Position
	if position < 0 {
		position = 0
	}
	if position >= len(scenario.Tokens) {
		position = len(scenario.Tokens) - 1
	}
	return kagLocationError(scenario.Tokens[position].Location, err)
}

func chargeKAG(budget *uint64) error {
	if budget == nil {
		return nil
	}
	if *budget == 0 {
		return ErrKAGExecutionBudget
	}
	*budget--
	return nil
}

func (p *KAGParser) evaluateKAG(expression string, budget *uint64) (bool, error) {
	if p.evaluator != nil {
		if err := chargeKAG(budget); err != nil {
			return false, err
		}
		return p.evaluator(expression)
	}
	program, err := tjs.CompileExpression(p.StorageName(), expression)
	if err != nil {
		return false, err
	}
	localBudget := budget
	if localBudget == nil {
		unlimited := ^uint64(0)
		localBudget = &unlimited
	}
	value, err := tjs.NewVM().Execute(&program, nil, localBudget)
	if err != nil {
		return false, err
	}
	return value.Truth()
}
func (p *KAGParser) evaluateKAGValue(expression string, budget *uint64) (tjs.Value, error) {
	if p.valueEvaluator != nil {
		if err := chargeKAG(budget); err != nil {
			return tjs.VoidValue, err
		}
		return p.valueEvaluator(expression)
	}
	program, err := tjs.CompileExpression(p.StorageName(), expression)
	if err != nil {
		return tjs.VoidValue, err
	}
	localBudget := budget
	if localBudget == nil {
		unlimited := ^uint64(0)
		localBudget = &unlimited
	}
	return tjs.NewVM().Execute(&program, nil, localBudget)
}

func (p *KAGParser) invokeEvent(event KAGEvent) error {
	if p.callbacks == nil {
		return nil
	}
	if event.Kind == KAGEventText && p.callbacks.OnText != nil {
		if err := p.callbacks.OnText(p, event.Text); err != nil {
			return err
		}
		if !p.Alive() {
			return ErrKAGParserInvalid
		}
	}
	if event.Kind == KAGEventTag && p.callbacks.OnTag != nil {
		if err := p.callbacks.OnTag(p, event.Tag); err != nil {
			return err
		}
		if !p.Alive() {
			return ErrKAGParserInvalid
		}
	}
	if event.Kind == KAGEventLabel && p.callbacks.OnLabel != nil {
		if err := p.callbacks.OnLabel(p, event.Label); err != nil {
			return err
		}
		if !p.Alive() {
			return ErrKAGParserInvalid
		}
	}
	if event.Kind == KAGEventScript && p.callbacks.OnScript != nil {
		if err := p.callbacks.OnScript(p, event.Script, event.Location.Storage, event.Location.Line); err != nil {
			return err
		}
		if !p.Alive() {
			return ErrKAGParserInvalid
		}
	}
	if p.callbacks.OnEvent != nil {
		if err := p.callbacks.OnEvent(p, event); err != nil {
			return err
		}
	}
	if !p.Alive() {
		return ErrKAGParserInvalid
	}
	return nil
}

func (p *KAGParser) conditionActive() bool {
	if len(p.conditions) == 0 {
		return true
	}
	return p.conditions[len(p.conditions)-1].active
}

func (p *KAGParser) conditionExpression(tag *kag.Tag) (string, error) {
	if value, ok := tag.Get("exp"); ok && value != "" {
		return value, nil
	}
	if value, ok := tag.Get("cond"); ok && value != "" {
		return value, nil
	}
	return "", fmt.Errorf("KAG conditional %s missing exp", tag.Name)
}

func (p *KAGParser) handleConditional(tag *kag.Tag, budget *uint64) (bool, error) {
	switch tag.Name {
	case "if":
		parent := p.conditionActive()
		active := false
		if parent {
			expression, err := p.conditionExpression(tag)
			if err != nil {
				return true, err
			}
			active, err = p.evaluateKAG(expression, budget)
			if err != nil {
				return true, err
			}
		}
		if len(p.conditions) >= 128 {
			return true, fmt.Errorf("KAG conditional depth exceeded")
		}
		p.conditions = append(p.conditions, kagCondition{parentActive: parent, branchTaken: parent && active, active: parent && active})
		return true, nil
	case "elsif":
		if len(p.conditions) == 0 {
			return true, fmt.Errorf("KAG elsif without if")
		}
		last := &p.conditions[len(p.conditions)-1]
		if !last.parentActive || last.branchTaken {
			last.active = false
			return true, nil
		}
		expression, err := p.conditionExpression(tag)
		if err != nil {
			return true, err
		}
		active, err := p.evaluateKAG(expression, budget)
		if err != nil {
			return true, err
		}
		last.active = active
		last.branchTaken = active
		return true, nil
	case "else":
		if len(p.conditions) == 0 {
			return true, fmt.Errorf("KAG else without if")
		}
		last := &p.conditions[len(p.conditions)-1]
		last.active = last.parentActive && !last.branchTaken
		last.branchTaken = last.branchTaken || last.active
		return true, nil
	case "endif":
		if len(p.conditions) == 0 {
			return true, fmt.Errorf("KAG endif without if")
		}
		p.conditions = p.conditions[:len(p.conditions)-1]
		return true, nil
	}
	return false, nil
}
func (p *KAGParser) resolveKAGTag(tag *kag.Tag, budget *uint64) (*kag.Tag, error) {
	resolved := *tag
	resolved.Attributes = append([]kag.Attribute(nil), tag.Attributes...)
	for i := range resolved.Attributes {
		attribute := &resolved.Attributes[i]
		if !attribute.Entity {
			continue
		}
		if attribute.Value == "" {
			return nil, fmt.Errorf("KAG tag [%s] attribute %s has an empty entity expression", tag.Name, attribute.Name)
		}
		value, err := p.evaluateKAGValue(attribute.Value, budget)
		if err != nil {
			return nil, fmt.Errorf("KAG tag [%s] attribute %s expression %q: %w", tag.Name, attribute.Name, attribute.Value, err)
		}
		attribute.Value = value.Text()
	}
	return &resolved, nil
}

func (p *KAGParser) eventFor(token *kag.Token) KAGEvent {
	event := KAGEvent{Token: *token}
	event.Location.Storage = token.Location.Storage
	event.Location.Line = token.Location.Line
	event.Location.Column = token.Location.Column
	switch token.Kind {
	case kag.KindText:
		event.Kind, event.Text = KAGEventText, token.Text
	case kag.KindTag:
		event.Kind, event.Tag = KAGEventTag, token.Tag
	case kag.KindLabel:
		event.Kind, event.Label = KAGEventLabel, token.Label
	default:
		event.Kind = KAGEventNewline
	}
	return event
}

func (p *KAGParser) kagCallDepth() int {
	depth := len(p.crossCalls)
	if p.parser != nil {
		depth += len(p.parser.State().Calls)
	}
	for _, frame := range p.crossCalls {
		if frame.parser != nil {
			depth += len(frame.parser.State().Calls)
		}
	}
	return depth
}

func (p *KAGParser) jumpScenario(storage, label, tagName string) error {
	if storage != "" {
		if p.callbacks == nil || p.callbacks.OnScenarioLoad == nil {
			return fmt.Errorf("KAG %s storage %q requires a scenario load callback", tagName, storage)
		}
		if err := p.LoadScenario(storage, ""); err != nil {
			return err
		}
	}
	if err := p.parser.Jump(label); err != nil {
		return err
	}
	p.conditions = nil
	p.conditionCalls = nil
	return nil
}

func (p *KAGParser) restoreScenarioCallFrame(frame kagScenarioCallFrame) error {
	if frame.parser == nil {
		return fmt.Errorf("invalid KAG scenario call frame")
	}
	p.parser = frame.parser
	p.state = frame.state
	p.macroRanges = frame.macroRanges
	p.macroIndexErr = frame.macroIndexErr
	return p.restoreLocalState(frame.state)
}

func (p *KAGParser) consumeExplicitReturnFrame(parser *kag.Parser, localCalls int) error {
	if localCalls != 0 {
		cursor := parser.State()
		if len(cursor.Calls) == 0 {
			return nil
		}
		cursor.Calls = cursor.Calls[:len(cursor.Calls)-1]
		return parser.Restore(cursor)
	}
	if n := len(p.crossCalls); n != 0 {
		p.crossCalls = p.crossCalls[:n-1]
	}
	return nil
}

func (p *KAGParser) callScenario(storage, label string) error {
	if p.kagCallDepth() >= 128 {
		return fmt.Errorf("KAG call depth exceeded")
	}
	if p.callbacks == nil || p.callbacks.OnScenarioLoad == nil {
		return fmt.Errorf("KAG call storage %q requires a scenario load callback", storage)
	}
	frame := kagScenarioCallFrame{
		parser:        p.parser,
		state:         p.storeCallFrameState(),
		macroRanges:   p.macroRanges,
		macroIndexErr: p.macroIndexErr,
	}
	previousCrossCalls := p.crossCalls
	p.crossCalls = append(p.crossCalls, frame)
	restore := func(err error) error {
		p.crossCalls = previousCrossCalls
		if restoreErr := p.restoreScenarioCallFrame(frame); restoreErr != nil {
			return errors.Join(err, restoreErr)
		}
		return err
	}
	p.macroFrames = nil
	if err := p.LoadScenario(storage, ""); err != nil {
		return restore(err)
	}
	if label != "" {
		if err := p.parser.Jump(label); err != nil {
			return restore(err)
		}
	}
	return nil
}

// Step executes until one visible text, label, tag, newline, inline-script, or
// end event is available. Consumed tokens, script body lines, and conditional
// expressions all charge the supplied budget.
func (p *KAGParser) Step(budget *uint64) (KAGEvent, error) {
	if err := p.ensureExecutable(); err != nil {
		return KAGEvent{}, err
	}
	if p.ended {
		return KAGEvent{Kind: KAGEventEnd}, nil
	}
	if p.macroIndexErr != nil {
		return KAGEvent{}, kagLocationError(core.SourceLocation{Storage: p.StorageName()}, p.macroIndexErr)
	}
	for {
		if err := chargeKAG(budget); err != nil {
			return KAGEvent{}, err
		}
		if p.interrupted && p.hasReadableKAGToken() {
			p.interrupted = false
			tag := &kag.Tag{Name: "interrupt"}
			token := kag.Token{Kind: kag.KindTag, Tag: tag, Location: core.SourceLocation{Storage: p.StorageName()}}
			event := p.eventFor(&token)
			if err := p.invokeEvent(event); err != nil {
				return KAGEvent{}, err
			}
			return event, nil
		}
		token := p.nextKAGToken()
		if token == nil {
			if p.ignoreDepth != 0 {
				return KAGEvent{}, p.kagEOFError(fmt.Errorf("KAG ignore block missing endignore"))
			}
			if len(p.conditions) != 0 {
				return KAGEvent{}, p.kagEOFError(fmt.Errorf("KAG conditional block missing endif"))
			}
			if len(p.conditionCalls) != 0 {
				return KAGEvent{}, p.kagEOFError(fmt.Errorf("KAG conditional call block missing return"))
			}
			p.ended = true
			return KAGEvent{Kind: KAGEventEnd}, nil
		}
		if token.Kind == kag.KindTag && token.Tag != nil {
			name := token.Tag.Name
			if token.InlineScript != nil {
				for remaining := token.InlineScript.LineCount; remaining > 0; remaining-- {
					if err := chargeKAG(budget); err != nil {
						return KAGEvent{}, kagLocationError(token.Location, err)
					}
				}
			}
			if name == "ignore" {
				if p.ignoreDepth == 0 && !p.conditionActive() {
					continue
				}
				if p.ignoreDepth >= 128 {
					return KAGEvent{}, kagLocationError(token.Location, fmt.Errorf("KAG ignore depth exceeded"))
				}
				p.ignoreDepth++
				continue
			}
			if name == "endignore" {
				if p.ignoreDepth != 0 {
					p.ignoreDepth--
					continue
				}
				if !p.conditionActive() {
					continue
				}
				return KAGEvent{}, kagLocationError(token.Location, fmt.Errorf("KAG endignore without ignore"))
			}
			if p.ignoreDepth != 0 {
				continue
			}
			if p.conditionActive() || name == "if" || name == "elsif" || name == "else" || name == "endif" {
				resolved, err := p.resolveKAGTag(token.Tag, budget)
				if err != nil {
					return KAGEvent{}, kagLocationError(token.Location, err)
				}
				token.Tag = resolved
			}
			control, err := p.handleConditional(token.Tag, budget)
			if err != nil {
				return KAGEvent{}, kagLocationError(token.Location, err)
			}
			if control {
				continue
			}
			switch name {
			case "jump", "goto":
				if !p.conditionActive() {
					continue
				}
				label, ok := token.Tag.Get("target")
				if !ok {
					label, ok = token.Tag.Get("label")
				}
				if !ok || label == "" {
					return KAGEvent{}, kagLocationError(token.Location, fmt.Errorf("KAG %s missing target", name))
				}
				storage, _ := token.Tag.Get("storage")
				if err := p.jumpScenario(storage, label, name); err != nil {
					return KAGEvent{}, kagLocationError(token.Location, err)
				}
				continue
			case "call":
				if !p.conditionActive() {
					continue
				}
				label, ok := token.Tag.Get("target")
				if !ok {
					label, ok = token.Tag.Get("label")
				}
				storage, _ := token.Tag.Get("storage")
				if (!ok || label == "") && storage == "" {
					return KAGEvent{}, kagLocationError(token.Location, fmt.Errorf("KAG call missing target"))
				}
				if storage != "" {
					if err := p.callScenario(storage, label); err != nil {
						return KAGEvent{}, kagLocationError(token.Location, err)
					}
				} else {
					if p.kagCallDepth() >= 128 {
						return KAGEvent{}, kagLocationError(token.Location, fmt.Errorf("KAG call depth exceeded"))
					}
					if err := p.parser.Call(label); err != nil {
						return KAGEvent{}, kagLocationError(token.Location, err)
					}
					p.conditionCalls = append(p.conditionCalls, append([]kagCondition(nil), p.conditions...))
					p.conditions = nil
				}
				continue
			case "return":
				if !p.conditionActive() {
					continue
				}
				storage, _ := token.Tag.Get("storage")
				label, hasTarget := token.Tag.Get("target")
				if !hasTarget {
					label, hasTarget = token.Tag.Get("label")
				}
				if storage != "" || hasTarget {
					if !hasTarget || label == "" {
						return KAGEvent{}, kagLocationError(token.Location, fmt.Errorf("KAG return missing target"))
					}
					returnParser := p.parser
					localCalls := len(returnParser.State().Calls)
					if err := p.jumpScenario(storage, label, "return"); err != nil {
						return KAGEvent{}, kagLocationError(token.Location, err)
					}
					if err := p.consumeExplicitReturnFrame(returnParser, localCalls); err != nil {
						return KAGEvent{}, kagLocationError(token.Location, err)
					}
					continue
				}
				if len(p.parser.State().Calls) != 0 {
					if err := p.parser.ReturnFromCall(); err != nil {
						return KAGEvent{}, kagLocationError(token.Location, err)
					}
					if n := len(p.conditionCalls); n != 0 {
						p.conditions = p.conditionCalls[n-1]
						p.conditionCalls = p.conditionCalls[:n-1]
					} else {
						p.conditions = nil
					}
					continue
				}
				if n := len(p.crossCalls); n != 0 {
					frame := p.crossCalls[n-1]
					if err := p.restoreScenarioCallFrame(frame); err != nil {
						return KAGEvent{}, kagLocationError(token.Location, err)
					}
					p.crossCalls = p.crossCalls[:n-1]
					continue
				}
				if err := p.parser.ReturnFromCall(); err != nil {
					return KAGEvent{}, kagLocationError(token.Location, err)
				}
				continue
			case "end":
				if !p.conditionActive() {
					continue
				}
				if len(p.conditions) != 0 {
					return KAGEvent{}, kagLocationError(token.Location, fmt.Errorf("KAG end encountered before endif"))
				}
				p.ended = true
				event := KAGEvent{Kind: KAGEventEnd, Token: *token}
				event.Location.Storage = token.Location.Storage
				event.Location.Line = token.Location.Line
				event.Location.Column = token.Location.Column
				return event, nil
			case "macro":
				if !p.conditionActive() {
					continue
				}
				if len(p.macroFrames) != 0 {
					return KAGEvent{}, kagLocationError(token.Location, fmt.Errorf("KAG nested macro definition is unsupported"))
				}
				start := p.parser.State().Position - 1
				end, ok := p.macroRanges[start]
				if !ok {
					return KAGEvent{}, kagLocationError(token.Location, fmt.Errorf("KAG macro definition range is unavailable"))
				}
				for range end - start - 1 {
					if err := chargeKAG(budget); err != nil {
						return KAGEvent{}, err
					}
				}
				cursor := p.parser.State()
				cursor.Position = end
				if err := p.parser.Restore(cursor); err != nil {
					return KAGEvent{}, kagLocationError(token.Location, err)
				}
				continue
			case "pmacro":
				if !p.conditionActive() {
					continue
				}
				continue
			case "erasemacro", "erasepmacro":
				if !p.conditionActive() {
					continue
				}
				macroName, ok := token.Tag.Get("name")
				if !ok || macroName == "" {
					return KAGEvent{}, kagLocationError(token.Location, fmt.Errorf("KAG %s missing name", name))
				}
				macroName = strings.ToLower(macroName)
				if name == "erasemacro" {
					if _, exists := p.macroDefs[macroName]; !exists {
						return KAGEvent{}, kagLocationError(token.Location, fmt.Errorf("unknown KAG macro name: %s", macroName))
					}
					delete(p.macroDefs, macroName)
				} else {
					if _, exists := p.paramMacroDefs[macroName]; !exists {
						return KAGEvent{}, kagLocationError(token.Location, fmt.Errorf("unknown KAG parameter macro name: %s", macroName))
					}
					delete(p.paramMacroDefs, macroName)
				}
				continue
			case "endmacro":
				if !p.conditionActive() {
					continue
				}
				return KAGEvent{}, kagLocationError(token.Location, fmt.Errorf("KAG endmacro without macro"))
			case "emb":
				if !p.conditionActive() {
					continue
				}
				expression, ok := token.Tag.Get("exp")
				if !ok || expression == "" {
					return KAGEvent{}, kagLocationError(token.Location, fmt.Errorf("KAG emb missing exp"))
				}
				value, err := p.evaluateKAGValue(expression, budget)
				if err != nil {
					return KAGEvent{}, kagLocationError(token.Location, err)
				}
				text := value.Text()
				if escape, ok := token.Tag.Get("escape"); !ok || escape != "false" {
					text = strings.ReplaceAll(text, "[", "[[")
				}
				event := KAGEvent{Kind: KAGEventText, Token: *token, Text: text}
				event.Location.Storage = token.Location.Storage
				event.Location.Line = token.Location.Line
				event.Location.Column = token.Location.Column
				if err := p.invokeEvent(event); err != nil {
					return KAGEvent{}, err
				}
				return event, nil
			case "iscript":
				if token.InlineScript != nil && p.conditionActive() {
					script := token.InlineScript
					event := KAGEvent{Kind: KAGEventScript, Token: *token, Script: script.Source}
					event.Location.Storage = token.Location.Storage
					event.Location.Line = script.StartLine
					event.Location.Column = 1
					if err := p.invokeEvent(event); err != nil {
						return KAGEvent{}, err
					}
					return event, nil
				}
			}
			if p.conditionActive() {
				if _, ok := p.macroDefs[name]; ok {
					if err := p.pushKAGMacro(token.Tag); err != nil {
						return KAGEvent{}, kagLocationError(token.Location, err)
					}
					continue
				}
			}
		}
		if p.ignoreDepth != 0 {
			continue
		}
		if !p.conditionActive() {
			continue
		}
		event := p.eventFor(token)
		if err := p.invokeEvent(event); err != nil {
			return KAGEvent{}, err
		}
		return event, nil
	}
}

// Run executes until @end or end of scenario, returning the visible event
// sequence. The same budget is shared by all steps.
func (p *KAGParser) Run(budget *uint64) ([]KAGEvent, error) {
	if err := p.ensureExecutable(); err != nil {
		return nil, err
	}
	events := make([]KAGEvent, 0)
	for {
		event, err := p.Step(budget)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
		if event.Kind == KAGEventEnd {
			return events, nil
		}
	}
}

// StepWithBudget is a value-budget adapter for hosts that do not keep a
// mutable budget variable.
func (p *KAGParser) StepWithBudget(budget uint64) (KAGEvent, uint64, error) {
	event, err := p.Step(&budget)
	return event, budget, err
}
func (p *KAGParser) NextEvent(budget *uint64) (KAGEvent, error) { return p.Step(budget) }
func (p *KAGParser) RunWithBudget(budget uint64) ([]KAGEvent, uint64, error) {
	events, err := p.Run(&budget)
	return events, budget, err
}

// LoadScenario replaces the current scenario and delivers OnScenarioLoad. If
// the callback returns non-empty source it becomes the parsed scenario text.
func (p *KAGParser) LoadScenario(storage, source string) error {
	if err := p.ensureExecutable(); err != nil {
		return err
	}
	if p.callbacks != nil && p.callbacks.OnScenarioLoad != nil {
		loaded, err := p.callbacks.OnScenarioLoad(p, storage, source)
		if err != nil {
			return err
		}
		if loaded != "" {
			source = loaded
		}
		if !p.Alive() {
			return ErrKAGParserInvalid
		}
	}
	scenario, err := kag.Parse(storage, source)
	if err != nil {
		return err
	}
	macros, ranges, pmacros, err := indexKAGMacros(scenario)
	if err != nil {
		return err
	}
	// KAG macro definitions belong to the parser, not a single scenario.
	// Loading another storage replaces only the ranges used to skip that
	// storage's definitions; existing macro bodies remain callable until
	// erasemacro/erasepmacro removes them.
	if p.macroDefs == nil && len(macros) != 0 {
		p.macroDefs = make(map[string][]kag.Token, len(macros))
	}
	for name, body := range macros {
		p.macroDefs[name] = body
	}
	if p.paramMacroDefs == nil && len(pmacros) != 0 {
		p.paramMacroDefs = make(map[string][]kag.Attribute, len(pmacros))
	}
	for name, attributes := range pmacros {
		p.paramMacroDefs[name] = attributes
	}
	p.parser = kag.NewParser(scenario)
	p.state = newKAGState(scenario)
	p.macroRanges = ranges
	p.legacyText = nil
	p.macroIndexErr = nil
	p.conditions = nil
	p.conditionCalls = nil
	p.ignoreDepth = 0
	p.ended = false
	if p.callbacks != nil && p.callbacks.OnScenarioLoaded != nil {
		if err := p.callbacks.OnScenarioLoaded(p, storage); err != nil {
			return err
		}
		if !p.Alive() {
			return ErrKAGParserInvalid
		}
	}
	return nil
}

// Load is an alias matching the native KAGParser vocabulary.
func (p *KAGParser) Load(storage, source string) error { return p.LoadScenario(storage, source) }
