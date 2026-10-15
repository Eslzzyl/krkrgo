package runtime

import (
	"encoding/json"
	"fmt"

	"github.com/eslzzyl/krkrgo/internal/assets"
	"github.com/eslzzyl/krkrgo/internal/kag"
	"github.com/eslzzyl/krkrgo/internal/tjs"
)

func (s *Services) kagNativeCall(vm *tjs.VM, name string, context tjs.Value, args []tjs.Value, budget *uint64) (tjs.Value, error) {
	s.ensureInit()
	if context.Kind() != tjs.KindObject || context.IsNull() {
		return tjs.VoidValue, fmt.Errorf("KAGParser requires object context")
	}
	id := context.ObjectRef().Object
	op := name[len("KAGParser."):]
	if op == "@initialize" {
		s.KAGParsers[id] = NewKAGParserFromScenario(kag.Scenario{})
		return tjs.VoidValue, nil
	}
	if op == "@invalidate" {
		if p := s.KAGParsers[id]; p != nil {
			p.Invalidate()
		}
		delete(s.KAGParsers, id)
		return tjs.VoidValue, nil
	}
	p := s.KAGParsers[id]
	if p == nil {
		return tjs.VoidValue, fmt.Errorf("KAGParser context is not initialized")
	}
	arg := func(index int) (tjs.Value, error) {
		if index >= len(args) {
			return tjs.VoidValue, fmt.Errorf("KAGParser.%s: missing argument %d", op, index)
		}
		return args[index], nil
	}
	if op == "getNextTag" || op == "step" || op == "run" || op == "loadScenario" {
		previousCallbacks := p.callbacks
		callbacks := KAGCallbacks{}
		if previousCallbacks != nil {
			callbacks = *previousCallbacks
		}
		previousOnScenarioLoad := callbacks.OnScenarioLoad
		callbacks.OnScenarioLoad = func(parser *KAGParser, storage, source string) (string, error) {
			handler, err := vm.GetMember(context, "onScenarioLoad", s, budget)
			if err != nil {
				return "", err
			}
			if !handler.IsVoid() {
				loaded, err := vm.CallFunction(handler, context, []tjs.Value{tjs.StringValue(storage)}, s, budget)
				if err != nil {
					return "", err
				}
				if !parser.Alive() {
					return "", ErrKAGParserInvalid
				}
				if loaded.Kind() == tjs.KindString {
					source = loaded.Text()
				}
			}
			if source == "" {
				data, err := s.readStorage(storage)
				if err != nil {
					return "", err
				}
				source, err = assets.Decode(data)
				if err != nil {
					return "", err
				}
			}
			if previousOnScenarioLoad != nil {
				loaded, err := previousOnScenarioLoad(parser, storage, source)
				if err != nil {
					return "", err
				}
				if loaded != "" {
					source = loaded
				}
			}
			return source, nil
		}
		previousOnScenarioLoaded := callbacks.OnScenarioLoaded
		callbacks.OnScenarioLoaded = func(parser *KAGParser, storage string) error {
			handler, err := vm.GetMember(context, "onScenarioLoaded", s, budget)
			if err != nil {
				return err
			}
			if !handler.IsVoid() {
				if _, err := vm.CallFunction(handler, context, []tjs.Value{tjs.StringValue(storage)}, s, budget); err != nil {
					return err
				}
				if !parser.Alive() {
					return ErrKAGParserInvalid
				}
			}
			if previousOnScenarioLoaded != nil {
				return previousOnScenarioLoaded(parser, storage)
			}
			return nil
		}
		previousOnLabel := callbacks.OnLabel
		callbacks.OnLabel = func(parser *KAGParser, label *kag.Label) error {
			handler, err := vm.GetMember(context, "onLabel", s, budget)
			if err != nil {
				return err
			}
			if !handler.IsVoid() {
				caption := tjs.VoidValue
				if label.Caption != nil {
					caption = tjs.StringValue(*label.Caption)
				}
				if _, err := vm.CallFunction(handler, context, []tjs.Value{
					tjs.StringValue(label.Name),
					caption,
				}, s, budget); err != nil {
					return err
				}
				if !parser.Alive() {
					return ErrKAGParserInvalid
				}
			}
			if previousOnLabel != nil {
				return previousOnLabel(parser, label)
			}
			return nil
		}
		previousOnScript := callbacks.OnScript
		callbacks.OnScript = func(parser *KAGParser, script, storage string, startLine uint64) error {
			handler, err := vm.GetMember(context, "onScript", s, budget)
			if err != nil {
				return err
			}
			if !handler.IsVoid() {
				lineOffset := int64(startLine)
				if lineOffset > 0 {
					lineOffset--
				}
				if _, err := vm.CallFunction(handler, context, []tjs.Value{
					tjs.StringValue(script),
					tjs.StringValue(scriptStorageName(storage)),
					tjs.IntegerValue(lineOffset),
				}, s, budget); err != nil {
					return err
				}
				if !parser.Alive() {
					return ErrKAGParserInvalid
				}
			}
			if previousOnScript != nil {
				return previousOnScript(parser, script, storage, startLine)
			}
			return nil
		}
		previousEvaluator := p.evaluator
		previousValueEvaluator := p.valueEvaluator
		p.SetCallbacks(&callbacks)
		p.SetExpressionEvaluator(func(expression string) (bool, error) {
			program, err := tjs.CompileExpression(p.StorageName(), expression)
			if err != nil {
				return false, err
			}
			value, err := vm.Execute(&program, s, budget)
			if err != nil {
				return false, err
			}
			return value.Truth()
		})
		p.SetExpressionValueEvaluator(func(expression string) (tjs.Value, error) {
			program, err := tjs.CompileExpression(p.StorageName(), expression)
			if err != nil {
				return tjs.VoidValue, err
			}
			return vm.Execute(&program, s, budget)
		})
		defer func() {
			p.SetCallbacks(previousCallbacks)
			p.SetExpressionEvaluator(previousEvaluator)
			p.SetExpressionValueEvaluator(previousValueEvaluator)
		}()
	}
	switch op {
	case "KAGParser", "finalize":
		return tjs.VoidValue, nil
	case "interrupt":
		p.Interrupt()
		return tjs.VoidValue, nil
	case "resetInterrupt":
		p.ResetInterrupt()
		return tjs.VoidValue, nil
	case "loadScenario":
		storage, err := arg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		source := ""
		if len(args) > 1 {
			source = args[1].Text()
		}
		return tjs.VoidValue, p.LoadScenario(storage.Text(), source)
	case "goToLabel":
		v, err := arg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		return tjs.VoidValue, p.Jump(v.Text())
	case "callLabel":
		v, err := arg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		return tjs.VoidValue, p.Call(v.Text())
	case "return":
		return tjs.VoidValue, p.ReturnFromCall()
	case "clearCallStack":
		return tjs.VoidValue, p.ClearCallStack()
	case "clear":
		return tjs.VoidValue, p.clear()
	case "assign":
		value, err := arg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		if value.Kind() != tjs.KindObject {
			return tjs.VoidValue, fmt.Errorf("expected an Object")
		}
		sourceID := value.ObjectRef().Object
		if value.IsNull() || sourceID == 0 {
			return tjs.VoidValue, fmt.Errorf("KAGParser.assign requires non-null source")
		}
		source := s.KAGParsers[sourceID]
		if source == nil {
			return tjs.VoidValue, fmt.Errorf("KAGParser.assign requires a KAGParser")
		}
		return tjs.VoidValue, p.assignFrom(source, budget)
	case "popMacroArgs":
		return tjs.VoidValue, p.popMacroArgs()
	case "getNextTag":
		for {
			if len(p.legacyText) != 0 {
				event := p.legacyText[0]
				p.legacyText = p.legacyText[1:]
				return legacyKAGEventValue(vm, event, "ch")
			}
			event, err := p.Step(budget)
			if err != nil {
				return tjs.VoidValue, err
			}
			switch event.Kind {
			case KAGEventTag:
				return kagEventValue(vm, event)
			case KAGEventText:
				p.legacyText = legacyKAGTextEvents(event)
			case KAGEventNewline:
				nextKind, hasNext := p.nextKAGTokenKind()
				if !hasNext || nextKind != kag.KindText {
					continue
				}
				if !p.ignoreCR {
					tag := kag.Tag{Name: "r"}
					event.Kind, event.Tag = KAGEventTag, &tag
					return kagEventValue(vm, event)
				}
			case KAGEventEnd:
				return tjs.VoidValue, nil
			}
		}
	case "step":
		event, err := p.Step(budget)
		if err != nil {
			return tjs.VoidValue, err
		}
		return kagEventValue(vm, event)
	case "run":
		events, err := p.Run(budget)
		if err != nil {
			return tjs.VoidValue, err
		}
		values := make([]tjs.Value, len(events))
		for i, event := range events {
			values[i], err = kagEventValue(vm, event)
			if err != nil {
				return tjs.VoidValue, err
			}
		}
		return vm.NewArray(values)
	case "store":
		return kagStateValue(vm, p.Store())
	case "restore":
		value, err := arg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		object, ok := vm.Objects.Lookup(value.ObjectRef().Object)
		if !ok {
			return tjs.VoidValue, fmt.Errorf("invalid KAG restore object")
		}
		return tjs.VoidValue, p.RestoreObject(object)
	case "get:ignoreCR":
		return tjs.IntegerValue(boolInt(p.ignoreCR)), nil
	case "set:ignoreCR":
		value, err := arg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		ignoreCR, err := value.Truth()
		if err != nil {
			return tjs.VoidValue, err
		}
		p.ignoreCR = ignoreCR
		return tjs.VoidValue, nil
	case "get:processSpecialTags":
		return tjs.IntegerValue(boolInt(p.processSpecialTags)), nil
	case "set:processSpecialTags":
		value, err := arg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		processSpecialTags, err := value.Truth()
		if err != nil {
			return tjs.VoidValue, err
		}
		p.processSpecialTags = processSpecialTags
		return tjs.VoidValue, nil
	case "get:multiLineTagEnabled":
		return tjs.IntegerValue(boolInt(p.multiLineTagEnabled)), nil
	case "set:multiLineTagEnabled":
		value, err := arg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		multiLineTagEnabled, err := value.Truth()
		if err != nil {
			return tjs.VoidValue, err
		}
		p.multiLineTagEnabled = multiLineTagEnabled
		return tjs.VoidValue, nil
	case "get:debugLevel":
		return tjs.IntegerValue(int64(p.debugLevel)), nil
	case "set:debugLevel":
		value, err := arg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		debugLevel, err := value.Int()
		if err != nil {
			return tjs.VoidValue, err
		}
		p.debugLevel = int32(debugLevel)
		return tjs.VoidValue, nil
	case "get:curLabel":
		return tjs.StringValue(p.CurrentLabel()), nil
	case "get:curLine":
		return tjs.IntegerValue(int64(p.GetCurLine())), nil
	case "get:curLineStr":
		return tjs.StringValue(p.GetCurLineStr()), nil
	case "get:curPos":
		return tjs.IntegerValue(int64(p.GetCurPos())), nil
	case "get:curStorage":
		return tjs.StringValue(p.StorageName()), nil
	case "get:callStackDepth":
		return tjs.IntegerValue(int64(p.kagCallDepth())), nil
	case "get:mp", "get:macroParams":
		if len(p.macroFrames) == 0 {
			return tjs.NullValue, nil
		}
		return kagMacroParametersValue(vm, p.macroFrames[len(p.macroFrames)-1].arguments, budget)
	case "get:macros":
		id := context.ObjectRef().Object
		if value, ok := s.KAGMacroObjects[id]; ok {
			return value, nil
		}
		value, err := vm.NewDictionary()
		if err != nil {
			return tjs.VoidValue, err
		}
		s.KAGMacroObjects[id] = value
		return value, nil
	case "set:macros":
		if len(args) != 1 {
			return tjs.VoidValue, fmt.Errorf("KAGParser.macros requires one value")
		}
		s.KAGMacroObjects[context.ObjectRef().Object] = args[0]
		return tjs.VoidValue, nil
	default:
		return tjs.VoidValue, fmt.Errorf("unsupported KAGParser operation: %s", op)
	}
}

func tjsDictionary(vm *tjs.VM) (*tjs.Object, error) {
	value, err := vm.NewDictionary()
	if err != nil {
		return nil, err
	}
	object, ok := vm.Objects.Lookup(value.ObjectRef().Object)
	if !ok {
		return nil, fmt.Errorf("dictionary allocation lost")
	}
	return object, nil
}

func kagMacroParametersValue(vm *tjs.VM, parameters map[string]string, budget *uint64) (tjs.Value, error) {
	object, err := tjsDictionary(vm)
	if err != nil {
		return tjs.VoidValue, err
	}
	for name, value := range parameters {
		if err := chargeKAG(budget); err != nil {
			return tjs.VoidValue, err
		}
		if err := object.Set(name, tjs.StringValue(value)); err != nil {
			return tjs.VoidValue, err
		}
	}
	return tjs.ObjectValue(object.Ref()), nil
}

func kagStateValue(vm *tjs.VM, state KAGState) (tjs.Value, error) {
	object, err := tjsDictionary(vm)
	if err != nil {
		return tjs.VoidValue, err
	}
	_ = object.Set("storage", tjs.StringValue(state.Storage))
	_ = object.Set("position", tjs.IntegerValue(int64(state.Position)))
	_ = object.Set("calls", tjs.StringValue(joinCallPositions(state.Calls)))
	_ = object.Set("fingerprint", tjs.StringValue(state.Fingerprint))
	conditions, _ := json.Marshal(state.Conditions)
	_ = object.Set("conditions", tjs.StringValue(string(conditions)))
	conditionCalls, _ := json.Marshal(state.ConditionCalls)
	_ = object.Set("condition_calls", tjs.StringValue(string(conditionCalls)))
	_ = object.Set("ended", tjs.IntegerValue(boolInt(state.Ended)))
	_ = object.Set("ignore_depth", tjs.IntegerValue(int64(state.IgnoreDepth)))
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
	return tjs.ObjectValue(object.Ref()), nil

}
func kagEventValue(vm *tjs.VM, event KAGEvent) (tjs.Value, error) {
	object, err := tjsDictionary(vm)
	if err != nil {
		return tjs.VoidValue, err
	}
	_ = object.Set("kind", tjs.IntegerValue(int64(event.Kind)))
	_ = object.Set("sourceStorage", tjs.StringValue(event.Location.Storage))
	_ = object.Set("line", tjs.IntegerValue(int64(event.Location.Line)))
	_ = object.Set("column", tjs.IntegerValue(int64(event.Location.Column)))
	_ = object.Set("text", tjs.StringValue(event.Text))
	switch {
	case event.Kind == KAGEventScript:
		_ = object.Set("script", tjs.StringValue(event.Script))
		_ = object.Set("payload", tjs.StringValue(event.Script))
	case event.Tag != nil:
		attributes := make([]tjs.Value, len(event.Tag.Attributes))
		// KAG command dispatch walks elm.taglist as the element's parameter
		// names, so it must be the ordered member names rather than the
		// attribute/value pairs. KAG scripts such as the KAGEnvironment
		// framework size the list with elm.taglist.count to decide whether a
		// defined environment object name supplies parameters at all.
		taglist := make([]tjs.Value, 0, len(event.Tag.Attributes)+1)
		taglist = append(taglist, tjs.StringValue("tagname"))
		seen := make(map[string]struct{}, len(event.Tag.Attributes))
		for i, attribute := range event.Tag.Attributes {
			pair, err := vm.NewArray([]tjs.Value{tjs.StringValue(attribute.Name), tjs.StringValue(attribute.Value)})
			if err != nil {
				return tjs.VoidValue, err
			}
			attributes[i] = pair
			value := tjs.StringValue(attribute.Value)
			// KAG scripts use a blank optional position as no position.
			if attribute.Name == "pos" && attribute.Value == "" {
				value = tjs.VoidValue
			}
			if err := object.Set(attribute.Name, value); err != nil {
				return tjs.VoidValue, err
			}
			if _, ok := seen[attribute.Name]; ok {
				continue
			}
			seen[attribute.Name] = struct{}{}
			taglist = append(taglist, tjs.StringValue(attribute.Name))
		}
		ordered, err := vm.NewArray(attributes)
		if err != nil {
			return tjs.VoidValue, err
		}
		_ = object.Set("attributes", ordered)
		_ = object.Set("tagname", tjs.StringValue(event.Tag.Name))
		_ = object.Set("payload", tjs.StringValue(event.Tag.Name))
		names, err := vm.NewArray(taglist)
		if err != nil {
			return tjs.VoidValue, err
		}
		_ = object.Set("taglist", names)
	case event.Label != nil:
		_ = object.Set("label", tjs.StringValue(event.Label.Name))
		if event.Label.Caption != nil {
			_ = object.Set("caption", tjs.StringValue(*event.Label.Caption))
		}
		_ = object.Set("payload", tjs.StringValue(event.Label.Name))
	default:
		_ = object.Set("payload", tjs.StringValue(event.Text))
	}
	return tjs.ObjectValue(object.Ref()), nil
}

func legacyKAGTextEvents(event KAGEvent) []KAGEvent {
	runes := []rune(event.Text)
	events := make([]KAGEvent, len(runes))
	column := event.Location.Column
	for i, ch := range runes {
		textEvent := KAGEvent{Kind: KAGEventText, Text: string(ch)}
		textEvent.Location = event.Location
		textEvent.Location.Column = column
		events[i] = textEvent
		column++
		if ch > 0xffff {
			// Go/TJS strings store Unicode scalar values as UTF-8, so an
			// astral character remains one drawable glyph while its source
			// location advances by both UTF-16 code units.
			column++
		}
	}
	return events
}

func legacyKAGEventValue(vm *tjs.VM, event KAGEvent, tagName string) (tjs.Value, error) {
	value, err := kagEventValue(vm, event)
	if err != nil {
		return tjs.VoidValue, err
	}
	object, ok := vm.Objects.Lookup(value.ObjectRef().Object)
	if !ok {
		return tjs.VoidValue, fmt.Errorf("KAGParser.getNextTag: lost text event")
	}
	_ = object.Set("tagname", tjs.StringValue(tagName))
	_ = object.Set("payload", tjs.StringValue(tagName))
	return value, nil
}
