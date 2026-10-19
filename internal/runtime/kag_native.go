package runtime

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/eslzzyl/krkrgo/internal/assets"
	"github.com/eslzzyl/krkrgo/internal/kag"
	"github.com/eslzzyl/krkrgo/internal/tjs"
)

const kagConductorQueuesMember = "__krkrgoConductorQueues"

var kagConductorQueueNames = [...]string{"pendings", "fasttags", "nexttags"}

func cloneKAGConductorQueue(vm *tjs.VM, value tjs.Value, host tjs.Host, budget *uint64) (tjs.Value, error) {
	if value.Kind() != tjs.KindObject || value.IsNull() {
		return tjs.VoidValue, fmt.Errorf("KAG conductor queue is not an Array")
	}
	if _, err := vm.ArrayItems(value); err != nil {
		return tjs.VoidValue, fmt.Errorf("KAG conductor queue is not an Array: %w", err)
	}
	copy, err := vm.NewArray(nil)
	if err != nil {
		return tjs.VoidValue, err
	}
	arrayClass, ok := vm.Globals["Array"]
	if !ok {
		return tjs.VoidValue, fmt.Errorf("Array class is unavailable")
	}
	assign, err := vm.GetMember(arrayClass, "assignStruct", host, budget)
	if err != nil {
		return tjs.VoidValue, err
	}
	if _, err := vm.CallFunction(assign, copy, []tjs.Value{value}, host, budget); err != nil {
		return tjs.VoidValue, err
	}
	return copy, nil
}

func storeKAGConductorQueues(vm *tjs.VM, context, state tjs.Value, host tjs.Host, budget *uint64) error {
	var queueState tjs.Value
	present := false
	for _, name := range kagConductorQueueNames {
		value, exists, err := vm.GetMemberIfPresent(context, name, host, budget)
		if err != nil {
			return fmt.Errorf("read KAG conductor %s: %w", name, err)
		}
		if !exists {
			continue
		}
		copy, err := cloneKAGConductorQueue(vm, value, host, budget)
		if err != nil {
			return fmt.Errorf("copy KAG conductor %s: %w", name, err)
		}
		if !present {
			queueState, err = vm.NewDictionary()
			if err != nil {
				return err
			}
		}
		if err := vm.SetMemberWithRuntime(queueState, name, copy, 0, host, budget); err != nil {
			return fmt.Errorf("store KAG conductor %s: %w", name, err)
		}
		present = true
	}
	if !present {
		return nil
	}
	if err := vm.SetMemberWithRuntime(state, kagConductorQueuesMember, queueState, 0, host, budget); err != nil {
		return fmt.Errorf("store KAG conductor queues: %w", err)
	}
	return nil
}

func restoreKAGConductorQueues(vm *tjs.VM, context, state tjs.Value, host tjs.Host, budget *uint64) error {
	queueState, exists, err := vm.GetMemberIfPresent(state, kagConductorQueuesMember, host, budget)
	if err != nil {
		return err
	}
	if exists {
		for _, name := range kagConductorQueueNames {
			value, found, err := vm.GetMemberIfPresent(queueState, name, host, budget)
			if err != nil {
				return fmt.Errorf("read saved KAG conductor %s: %w", name, err)
			}
			if !found {
				continue
			}
			if _, exists, err := vm.GetMemberIfPresent(context, name, host, budget); err != nil {
				return fmt.Errorf("read KAG conductor %s before restore: %w", name, err)
			} else if !exists {
				continue
			}
			copy, err := cloneKAGConductorQueue(vm, value, host, budget)
			if err != nil {
				return fmt.Errorf("restore KAG conductor %s: %w", name, err)
			}
			if err := vm.SetMemberWithRuntime(context, name, copy, 0, host, budget); err != nil {
				return fmt.Errorf("restore KAG conductor %s: %w", name, err)
			}
		}
	}
	return resumeSavedKAGScenePlayer(vm, context, host, budget)
}

func conductorHasTag(vm *tjs.VM, context tjs.Value, tagName string, host tjs.Host, budget *uint64) (bool, error) {
	for _, queueName := range kagConductorQueueNames {
		queue, exists, err := vm.GetMemberIfPresent(context, queueName, host, budget)
		if err != nil {
			return false, fmt.Errorf("read KAG conductor %s: %w", queueName, err)
		}
		if !exists {
			continue
		}
		items, err := vm.ArrayItems(queue)
		if err != nil {
			return false, fmt.Errorf("read KAG conductor %s: %w", queueName, err)
		}
		for _, item := range items {
			if item.Kind() != tjs.KindObject || item.IsNull() {
				continue
			}
			name, found, err := vm.GetMemberIfPresent(item, "tagname", host, budget)
			if err != nil {
				return false, err
			}
			if found && name.Kind() == tjs.KindString && name.Text() == tagName {
				return true, nil
			}
		}
	}
	return false, nil
}

func resumeSavedKAGScenePlayer(vm *tjs.VM, context tjs.Value, host tjs.Host, budget *uint64) error {
	owner, exists, err := vm.GetMemberIfPresent(context, "owner", host, budget)
	if err != nil || !exists || owner.Kind() != tjs.KindObject || owner.IsNull() {
		return err
	}
	mainConductor, exists, err := vm.GetMemberIfPresent(owner, "mainConductor", host, budget)
	if err != nil || !exists || mainConductor.Kind() != tjs.KindObject || mainConductor.IsNull() {
		return err
	}
	if mainConductor.ObjectRef().Object != context.ObjectRef().Object {
		return nil
	}
	flags, exists, err := vm.GetMemberIfPresent(owner, "pcflags", host, budget)
	if err != nil || !exists || flags.Kind() != tjs.KindObject || flags.IsNull() {
		return err
	}
	scenePlayer, exists, err := vm.GetMemberIfPresent(flags, "scenePlayer", host, budget)
	if err != nil || !exists || scenePlayer.Kind() != tjs.KindObject || scenePlayer.IsNull() {
		return err
	}
	scene, exists, err := vm.GetMemberIfPresent(scenePlayer, "scene", host, budget)
	if err != nil || !exists || scene.Kind() != tjs.KindString || scene.Text() == "" {
		return err
	}
	queued, err := conductorHasTag(vm, context, "sceneplay", host, budget)
	if err != nil || queued {
		return err
	}
	insertTag, exists, err := vm.GetMemberIfPresent(context, "insertTag", host, budget)
	if err != nil {
		return err
	}
	if !exists || insertTag.IsVoid() {
		return fmt.Errorf("KAG conductor cannot resume a saved scene player")
	}
	if _, err := vm.CallFunction(insertTag, context, []tjs.Value{tjs.StringValue("sceneplay")}, host, budget); err != nil {
		return fmt.Errorf("queue sceneplay for saved KAG scene player: %w", err)
	}
	return nil
}

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
		delete(s.KAGMacroObjects, id)
		delete(s.KAGParamMacroObjects, id)
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
	if op == "getNextTag" || op == "step" || op == "run" || op == "loadScenario" || op == "return" || op == "restore" {
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
		previousOnReturn := callbacks.OnReturn
		callbacks.OnReturn = func(parser *KAGParser, tag *kag.Tag) (bool, error) {
			process := true
			event := KAGEvent{Kind: KAGEventTag, Tag: tag}
			event.Location.Storage = parser.StorageName()
			value, err := kagEventValue(vm, event)
			if err != nil {
				return false, fmt.Errorf("KAGParser.onReturn event: %w", err)
			}
			handler, err := vm.GetMember(context, "onReturn", s, budget)
			if err != nil {
				return false, fmt.Errorf("KAGParser.onReturn lookup: %w", err)
			}
			if !handler.IsVoid() {
				if parser.StorageName() == "backlog.ks" {
					handlerID := handler.ObjectRef().Object
					eventID := value.ObjectRef().Object
					_, handlerAlive := vm.Objects.Lookup(handlerID)
					_, eventAlive := vm.Objects.Lookup(eventID)
					fmt.Printf("[kag-return-hook] parser=%p context=%d handler=%d/%t event=%d/%t depth=%d\n", parser, id, handlerID, handlerAlive, eventID, eventAlive, parser.kagCallDepth())
				}
				result, err := vm.CallFunction(handler, context, []tjs.Value{value}, s, budget)
				if err != nil {
					fmt.Printf("[kag-return-hook] callback error=%v\n", err)
					marker := "invalid TJS object handle: "
					if index := strings.LastIndex(err.Error(), marker); index >= 0 {
						if raw := strings.TrimSpace(err.Error()[index+len(marker):]); raw != "" {
							if parsed, parseErr := strconv.ParseUint(raw, 10, 64); parseErr == nil {
								fault := tjs.ObjectID(parsed)
								_, isMenuItem := s.menuData.items[fault]
								fmt.Printf("[kag-return-hook] invalid=%d menu_item=%t roots=%d items=%d\n", fault, isMenuItem, len(s.menuData.roots), len(s.menuData.items))
								for windowID, root := range s.menuData.roots {
									if root.Kind() == tjs.KindObject && !root.IsNull() {
										fmt.Printf("[kag-return-hook] menu_root window=%d object=%d valid=%t\n", windowID, root.ObjectRef().Object, vm.IsValid(root).IntegerValue() != 0)
									}
								}
								for itemID, item := range s.menuData.items {
									arrayID := tjs.ObjectID(0)
									if item.array.Kind() == tjs.KindObject && !item.array.IsNull() {
										arrayID = item.array.ObjectRef().Object
									}
									matched := itemID == fault || arrayID == fault
									for _, childID := range item.children {
										matched = matched || childID == fault
									}
									if matched {
										fmt.Printf("[kag-return-hook] menu_match item=%d array=%d children=%v\n", itemID, arrayID, item.children)
									}
								}
							}
						}
					}
					return false, fmt.Errorf("KAGParser.onReturn call: %w", err)
				}
				if !parser.Alive() {
					return false, ErrKAGParserInvalid
				}
				process, err = result.Truth()
				if err != nil {
					return false, fmt.Errorf("KAGParser.onReturn result: %w", err)
				}
			}
			if previousOnReturn != nil {
				previousProcess, err := previousOnReturn(parser, tag)
				if err != nil {
					return false, err
				}
				process = process && previousProcess
			}
			return process, nil
		}
		previousOnAfterReturn := callbacks.OnAfterReturn
		callbacks.OnAfterReturn = func(parser *KAGParser) error {
			handler, err := vm.GetMember(context, "onAfterReturn", s, budget)
			if err != nil {
				return fmt.Errorf("KAGParser.onAfterReturn lookup: %w", err)
			}
			if !handler.IsVoid() {
				if _, err := vm.CallFunction(handler, context, nil, s, budget); err != nil {
					return fmt.Errorf("KAGParser.onAfterReturn call: %w", err)
				}
				if !parser.Alive() {
					return ErrKAGParserInvalid
				}
			}
			if previousOnAfterReturn != nil {
				return previousOnAfterReturn(parser)
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
			value, err := withKAGMacroExpressionScope(vm, p, budget, func() (tjs.Value, error) {
				return vm.Execute(&program, s, budget)
			})
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
			return withKAGMacroExpressionScope(vm, p, budget, func() (tjs.Value, error) {
				return vm.Execute(&program, s, budget)
			})
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
		value, err := kagStateValue(vm, p.Store())
		if err != nil {
			return tjs.VoidValue, err
		}
		if err := storeKAGConductorQueues(vm, context, value, s, budget); err != nil {
			return tjs.VoidValue, err
		}
		return value, nil
	case "restore":
		value, err := arg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		object, ok := vm.Objects.Lookup(value.ObjectRef().Object)
		if !ok {
			return tjs.VoidValue, fmt.Errorf("invalid KAG restore object")
		}
		if err := p.RestoreObject(object); err != nil {
			return tjs.VoidValue, err
		}
		if err := restoreKAGConductorQueues(vm, context, value, s, budget); err != nil {
			return tjs.VoidValue, err
		}
		return tjs.VoidValue, nil
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
		value, object, err := kagProjectionObject(vm, s.KAGMacroObjects, id)
		if err != nil {
			return tjs.VoidValue, err
		}
		if err := syncKAGMacroProjection(object, p.macroDefs, budget); err != nil {
			return tjs.VoidValue, err
		}
		return value, nil
	case "set:macros":
		// The reference KAGParser marks the macros property read-only
		// (TJS_DENY_NATIVE_PROP_SETTER in KAGParser.cpp); definitions change
		// only through @macro, @erasemacro, assign, restore and loadScenario.
		return tjs.VoidValue, fmt.Errorf("KAGParser.macros is read-only: the reference KAGParser denies the property setter, so define macros with @macro or remove them with @erasemacro")
	case "get:paramMacros":
		id := context.ObjectRef().Object
		value, object, err := kagProjectionObject(vm, s.KAGParamMacroObjects, id)
		if err != nil {
			return tjs.VoidValue, err
		}
		if err := syncKAGParamMacroProjection(vm, object, p.paramMacroDefs, budget); err != nil {
			return tjs.VoidValue, err
		}
		return value, nil
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

// kagProjectionObject returns the stable dictionary object exposed by a
// read-only projection property, creating it on first access. Keeping one
// object per parser instance preserves the reference identity contract
// (p.macros === p.macros).
func kagProjectionObject(vm *tjs.VM, cache map[tjs.ObjectID]tjs.Value, id tjs.ObjectID) (tjs.Value, *tjs.Object, error) {
	if value, ok := cache[id]; ok && value.Kind() == tjs.KindObject && !value.IsNull() {
		if object, ok := vm.Objects.Lookup(value.ObjectRef().Object); ok {
			return value, object, nil
		}
	}
	value, err := vm.NewDictionary()
	if err != nil {
		return tjs.VoidValue, nil, err
	}
	object, ok := vm.Objects.Lookup(value.ObjectRef().Object)
	if !ok {
		return tjs.VoidValue, nil, fmt.Errorf("KAG projection dictionary allocation lost")
	}
	cache[id] = value
	return value, object, nil
}

// syncKAGMacroProjection refreshes the macros projection from the live macro
// definitions. The reference dictionary maps a macro name to its recorded body
// text; this projection renders the tokenized body canonically and drops the
// reference [macropop] sentinel because frames pop structurally here.
func syncKAGMacroProjection(object *tjs.Object, definitions map[string][]kag.Token, budget *uint64) error {
	values := make(map[string]tjs.Value, len(definitions))
	for name, body := range definitions {
		if err := chargeKAG(budget); err != nil {
			return err
		}
		values[name] = tjs.StringValue(kagMacroBodyText(body))
	}
	return syncKAGProjectionValues(object, values)
}

// syncKAGParamMacroProjection refreshes the paramMacros projection. The
// reference KAGParserEx dictionary stores each parameter macro as a flat array
// of alternating attribute names and values.
func syncKAGParamMacroProjection(vm *tjs.VM, object *tjs.Object, definitions map[string][]kag.Attribute, budget *uint64) error {
	values := make(map[string]tjs.Value, len(definitions))
	for name, attributes := range definitions {
		items := make([]tjs.Value, 0, len(attributes)*2)
		for _, attribute := range attributes {
			if err := chargeKAG(budget); err != nil {
				return err
			}
			value := attribute.Value
			if attribute.Entity {
				value = "&" + value
			}
			items = append(items, tjs.StringValue(attribute.Name), tjs.StringValue(value))
		}
		array, err := vm.NewArray(items)
		if err != nil {
			return err
		}
		values[name] = array
	}
	return syncKAGProjectionValues(object, values)
}

func syncKAGProjectionValues(object *tjs.Object, values map[string]tjs.Value) error {
	for name, value := range values {
		if err := object.Set(name, value); err != nil {
			return fmt.Errorf("KAG macro projection %s: %w", name, err)
		}
	}
	for _, name := range object.Keys() {
		if _, ok := values[name]; ok {
			continue
		}
		object.Delete(name)
	}
	return nil
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
func withKAGMacroExpressionScope(vm *tjs.VM, parser *KAGParser, budget *uint64, fn func() (tjs.Value, error)) (tjs.Value, error) {
	if vm == nil || parser == nil || len(parser.macroFrames) == 0 {
		return fn()
	}
	parameters, err := kagMacroParametersValue(vm, parser.macroFrames[len(parser.macroFrames)-1].arguments, budget)
	if err != nil {
		return tjs.VoidValue, err
	}
	previousMP, hadMP := vm.Globals["mp"]
	previousMacroParams, hadMacroParams := vm.Globals["macroParams"]
	vm.Globals["mp"] = parameters
	vm.Globals["macroParams"] = parameters
	defer func() {
		if hadMP {
			vm.Globals["mp"] = previousMP
		} else {
			delete(vm.Globals, "mp")
		}
		if hadMacroParams {
			vm.Globals["macroParams"] = previousMacroParams
		} else {
			delete(vm.Globals, "macroParams")
		}
	}()
	return fn()
}

func kagStateValue(vm *tjs.VM, state KAGState) (tjs.Value, error) {
	object, err := tjsDictionary(vm)
	if err != nil {
		return tjs.VoidValue, err
	}
	_ = object.Set("storage", tjs.StringValue(state.Storage))
	_ = object.Set("storageName", tjs.StringValue(state.Storage))
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
