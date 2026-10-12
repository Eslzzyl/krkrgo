package runtime

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/eslzzyl/krkrgo/internal/assets"
	"github.com/eslzzyl/krkrgo/internal/tjs"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

func (s *Services) decodeScriptStorage(data []byte) (string, error) {
	encoding, explicit := s.scriptTextEncodingConfig()
	if explicit {
		return decodeScriptText(data, encoding)
	}
	return decodeScriptStorageDefault(data)
}

func decodeScriptStorageDefault(data []byte) (string, error) {
	return assets.Decode(data)
}
func executeTJS2Bytecode(vm *tjs.VM, data []byte, context tjs.Value, host tjs.Host, budget *uint64) (tjs.Value, error) {
	decoded, err := tjs.DecodeBytecode(data)
	if err != nil {
		return tjs.VoidValue, fmt.Errorf("%w: %v", ErrUnsupportedNative, err)
	}
	// Legacy intercode resolves global names through the shared object table,
	// while the source VM keeps registered globals in VM.Globals. Mirror the
	// registered values onto the global object before executing the bytecode.
	globalObject, ok := vm.Objects.Lookup(0)
	if !ok {
		return tjs.VoidValue, fmt.Errorf("%w: TJS global object is unavailable", ErrUnsupportedNative)
	}
	for name, value := range vm.Globals {
		if err := globalObject.Set(name, value); err != nil {
			return tjs.VoidValue, fmt.Errorf("install TJS global %q for legacy intercode: %w", name, err)
		}
	}
	top := int(decoded.Info.TopLevel)
	if top < 0 || top >= len(decoded.Objects) {
		return tjs.VoidValue, fmt.Errorf("%w: compiled TJS2100 bytecode has invalid top-level object %d", ErrUnsupportedNative, top)
	}
	evaluate := func(source string, context tjs.Value, resultNeeded bool) (tjs.Value, error) {
		if source == "" {
			return tjs.VoidValue, nil
		}
		program, err := tjs.CompileEval("<eval>", source)
		if err != nil {
			return tjs.VoidValue, err
		}
		value, err := vm.ExecuteInContext(&program, context, host, budget)
		if err != nil || resultNeeded {
			return value, err
		}
		return tjs.VoidValue, nil
	}
	invoke := func(function, receiver tjs.Value, args []tjs.Value, resultNeeded bool) (tjs.Value, error) {
		return vm.InvokeLegacy(function, receiver, args, host, budget, resultNeeded)
	}
	construct := func(class tjs.Value, args []tjs.Value) (tjs.Value, error) {
		return vm.Construct(class, args, host, budget)
	}
	legacyGlobal := tjs.ObjectValue(tjs.NewObjectRef(0, 1))
	if err := bindLegacyScriptFunctions(vm, decoded, legacyGlobal, host, budget); err != nil {
		return tjs.VoidValue, err
	}
	resolve := func(receiver tjs.Value, name string) (tjs.Value, bool, error) {
		return vm.GetMemberIfPresent(receiver, name, host, budget)
	}
	set := func(receiver tjs.Value, name string, value tjs.Value, flags uint32) error {
		return vm.SetMemberWithRuntime(receiver, name, value, flags, host, budget)
	}
	value, err := decoded.ExecuteLegacyWithContextAndConstructionAndMemberResolvers(top, legacyGlobal, context, evaluate, invoke, construct, resolve, set, vm.Objects)
	if err != nil {
		if strings.Contains(err.Error(), "unsupported ") {
			return tjs.VoidValue, fmt.Errorf("%w: compiled TJS2100 %v", ErrUnsupportedNative, err)
		}
		return tjs.VoidValue, err
	}
	return value, nil
}
func bindLegacyScriptFunctions(vm *tjs.VM, decoded *tjs.DecodedBytecode, global tjs.Value, host tjs.Host, budget *uint64) error {
	functions := make(map[int]tjs.Value)
	bindClassBody := func(index int, class tjs.Value) error {
		classIndex := index
		return vm.SetLegacyClassBody(class, func(receiver tjs.Value, args []tjs.Value, callHost tjs.Host, callBudget *uint64, _ bool) (tjs.Value, error) {
			callEvaluate := func(source string, evalContext tjs.Value, resultNeeded bool) (tjs.Value, error) {
				if source == "" {
					return tjs.VoidValue, nil
				}
				program, compileErr := tjs.CompileExpression("<eval>", source)
				if compileErr != nil {
					return tjs.VoidValue, compileErr
				}
				value, evalErr := vm.ExecuteInContext(&program, evalContext, callHost, callBudget)
				if evalErr != nil || resultNeeded {
					return value, evalErr
				}
				return tjs.VoidValue, nil
			}
			callInvoke := func(callee, thisValue tjs.Value, callArgs []tjs.Value, resultNeeded bool) (tjs.Value, error) {
				return vm.InvokeLegacy(callee, thisValue, callArgs, callHost, callBudget, resultNeeded)
			}
			callConstruct := func(value tjs.Value, callArgs []tjs.Value) (tjs.Value, error) {
				return vm.Construct(value, callArgs, callHost, callBudget)
			}
			resolve := func(value tjs.Value, member string) (tjs.Value, bool, error) {
				return vm.GetMemberIfPresent(value, member, callHost, callBudget)
			}
			set := func(value tjs.Value, member string, assigned tjs.Value, flags uint32) error {
				return vm.SetMemberWithRuntime(value, member, assigned, flags, callHost, callBudget)
			}
			return decoded.ExecuteLegacyWithContextAndConstructionAndArgumentsAndMemberResolvers(classIndex, global, receiver, args, callEvaluate, callInvoke, callConstruct, resolve, set, vm.Objects)
		})
	}
	bindSuperclassGetter := func(classIndex int, class tjs.Value) error {
		classObject := &decoded.Objects[classIndex]
		getterIndex := int32(classObject.SuperclassGetter)
		if getterIndex < 0 {
			return nil
		}
		if getterIndex == int32(classIndex) {
			return nil
		}
		if int(getterIndex) >= len(decoded.Objects) {
			return fmt.Errorf("compiled TJS2100 superclass getter object index %d is out of range", getterIndex)
		}
		getterObject := &decoded.Objects[getterIndex]
		if getterObject.ContextType != 7 {
			if getterObject.ContextType == 0 && len(getterObject.SuperclassGetters) == 0 {
				return nil
			}
			return fmt.Errorf("compiled TJS2100 superclass getter object %d has context type %d, want 7", getterIndex, getterObject.ContextType)
		}
		offsets := make([]int, len(getterObject.SuperclassGetters))
		for i, offset := range getterObject.SuperclassGetters {
			if offset < 0 || int(offset) >= len(getterObject.Code) {
				return fmt.Errorf("compiled TJS2100 superclass getter code offset %d is out of range", offset)
			}
			offsets[i] = int(offset)
		}
		return vm.SetLegacyClassSuperclassGetter(class, offsets, func(offset int, callHost tjs.Host, callBudget *uint64) (tjs.Value, error) {
			callEvaluate := func(source string, evalContext tjs.Value, resultNeeded bool) (tjs.Value, error) {
				if source == "" {
					return tjs.VoidValue, nil
				}
				program, compileErr := tjs.CompileExpression("<eval>", source)
				if compileErr != nil {
					return tjs.VoidValue, compileErr
				}
				value, evalErr := vm.ExecuteInContext(&program, evalContext, callHost, callBudget)
				if evalErr != nil || resultNeeded {
					return value, evalErr
				}
				return tjs.VoidValue, nil
			}
			callInvoke := func(callee, thisValue tjs.Value, args []tjs.Value, resultNeeded bool) (tjs.Value, error) {
				return vm.InvokeLegacy(callee, thisValue, args, callHost, callBudget, resultNeeded)
			}
			callConstruct := func(value tjs.Value, args []tjs.Value) (tjs.Value, error) {
				return vm.Construct(value, args, callHost, callBudget)
			}
			resolve := func(value tjs.Value, member string) (tjs.Value, bool, error) {
				return vm.GetMemberIfPresent(value, member, callHost, callBudget)
			}
			set := func(value tjs.Value, member string, assigned tjs.Value, flags uint32) error {
				return vm.SetMemberWithRuntime(value, member, assigned, flags, callHost, callBudget)
			}
			return decoded.ExecuteLegacyAtOffsetAndMemberResolvers(int(getterIndex), offset, global, global, callEvaluate, callInvoke, callConstruct, resolve, set, vm.Objects)
		})
	}
	// Class member tables reference method objects independently of the class
	// body's bytecode constants; include those references in the shared binder pass.
	for objectIndex := range decoded.Objects {
		classObject := &decoded.Objects[objectIndex]
		if classObject.ContextType != 6 {
			continue
		}
		for _, property := range classObject.Properties {
			if property.Object < 0 || int(property.Object) >= len(decoded.Objects) {
				return fmt.Errorf("compiled TJS2100 class property object index %d is out of range", property.Object)
			}
			contextType := decoded.Objects[property.Object].ContextType
			if contextType == 1 || contextType == 2 {
				classObject.Constants = append(classObject.Constants, tjs.BytecodeConstant{Type: 2, Index: int16(property.Object), Value: tjs.VoidValue})
			}
		}
	}
	// Top-level class objects are globals even when no object constant refers
	// to them; superclass-getter bytecode may resolve them by global name.
	for objectIndex := range decoded.Objects {
		classObject := &decoded.Objects[objectIndex]
		if classObject.ContextType != 6 || classObject.Parent != 0 {
			continue
		}
		name := ""
		if classObject.Name >= 0 && int(classObject.Name) < len(decoded.Strings) {
			name = decoded.Strings[classObject.Name]
		}
		class, err := vm.RegisterLegacyClass(name)
		if err != nil {
			return fmt.Errorf("register compiled TJS class %q: %w", name, err)
		}
		if err := bindClassBody(objectIndex, class); err != nil {
			return fmt.Errorf("bind compiled TJS class body %d: %w", objectIndex, err)
		}
		if err := bindSuperclassGetter(objectIndex, class); err != nil {
			return fmt.Errorf("bind compiled TJS superclass getter %d: %w", objectIndex, err)
		}
		functions[objectIndex] = class
	}
	for objectIndex := range decoded.Objects {
		object := &decoded.Objects[objectIndex]
		for constantIndex := range object.Constants {
			constant := &object.Constants[constantIndex]
			if constant.Type != 2 {
				continue
			}
			index := int(constant.Index)
			if index < 0 || index >= len(decoded.Objects) {
				return fmt.Errorf("compiled TJS2100 object constant index %d is out of range", index)
			}
			target := decoded.Objects[index]
			if target.ContextType == 6 {
				class, exists := functions[index]
				if !exists {
					name := ""
					if target.Name >= 0 && int(target.Name) < len(decoded.Strings) {
						name = decoded.Strings[target.Name]
					}
					var err error
					class, err = vm.RegisterLegacyClass(name)
					if err != nil {
						return fmt.Errorf("register compiled TJS class %q: %w", name, err)
					}
					functions[index] = class
				}
				if err := bindClassBody(index, class); err != nil {
					return fmt.Errorf("bind compiled TJS class body %d: %w", index, err)
				}
				if err := bindSuperclassGetter(index, class); err != nil {
					return fmt.Errorf("bind compiled TJS superclass getter %d: %w", index, err)
				}
				constant.Value = class
				continue
			}
			if target.ContextType != 1 && target.ContextType != 2 {
				continue
			}
			function, exists := functions[index]
			if !exists {
				name := ""
				if target.Name >= 0 && int(target.Name) < len(decoded.Strings) {
					name = decoded.Strings[target.Name]
				}
				registered, err := vm.RegisterLegacyFunction(name, func(receiver tjs.Value, args []tjs.Value, callHost tjs.Host, callBudget *uint64, resultNeeded bool) (tjs.Value, error) {
					callEvaluate := func(source string, evalContext tjs.Value, needed bool) (tjs.Value, error) {
						if source == "" {
							return tjs.VoidValue, nil
						}
						program, compileErr := tjs.CompileExpression("<eval>", source)
						if compileErr != nil {
							return tjs.VoidValue, compileErr
						}
						value, evalErr := vm.ExecuteInContext(&program, evalContext, callHost, callBudget)
						if evalErr != nil || needed {
							return value, evalErr
						}
						return tjs.VoidValue, nil
					}
					callInvoke := func(callee, thisValue tjs.Value, callArgs []tjs.Value, needed bool) (tjs.Value, error) {
						return vm.InvokeLegacy(callee, thisValue, callArgs, callHost, callBudget, needed)
					}
					callConstruct := func(class tjs.Value, callArgs []tjs.Value) (tjs.Value, error) {
						return vm.Construct(class, callArgs, callHost, callBudget)
					}
					resolve := func(value tjs.Value, member string) (tjs.Value, bool, error) {
						return vm.GetMemberIfPresent(value, member, callHost, callBudget)
					}
					set := func(value tjs.Value, member string, assigned tjs.Value, flags uint32) error {
						return vm.SetMemberWithRuntime(value, member, assigned, flags, callHost, callBudget)
					}
					return decoded.ExecuteLegacyWithContextAndConstructionAndArgumentsAndMemberResolvers(index, global, receiver, args, callEvaluate, callInvoke, callConstruct, resolve, set, vm.Objects)
				})
				if err != nil {
					return err
				}
				function = registered
				if target.Parent == 0 && name != "" {
					globalObject, ok := vm.Objects.Lookup(global.ObjectRef().Object)
					if !ok {
						return fmt.Errorf("TJS global object is unavailable while binding %q", name)
					}
					if err := globalObject.Set(name, function); err != nil {
						return fmt.Errorf("bind compiled TJS function %q: %w", name, err)
					}
				}
				functions[index] = function
			}
			constant.Value = function
		}
	}
	for objectIndex := range decoded.Objects {
		classObject := &decoded.Objects[objectIndex]
		if classObject.ContextType != 6 {
			continue
		}
		class, ok := functions[objectIndex]
		if !ok {
			continue
		}
		for _, property := range classObject.Properties {
			if property.Object < 0 || int(property.Object) >= len(decoded.Objects) {
				return fmt.Errorf("compiled TJS2100 class property object index %d is out of range", property.Object)
			}
			memberName := ""
			if property.Name >= 0 && int(property.Name) < len(decoded.Strings) {
				memberName = decoded.Strings[property.Name]
			}
			if memberName == "" {
				continue
			}
			member, exists := functions[int(property.Object)]
			if !exists {
				continue
			}
			if err := vm.RegisterNativeStaticValue(class, memberName, member); err != nil {
				return fmt.Errorf("bind compiled TJS class member %q: %w", memberName, err)
			}
		}
		callEvaluate := func(source string, evalContext tjs.Value, needed bool) (tjs.Value, error) {
			if source == "" {
				return tjs.VoidValue, nil
			}
			program, err := tjs.CompileExpression("<eval>", source)
			if err != nil {
				return tjs.VoidValue, err
			}
			value, err := vm.ExecuteInContext(&program, evalContext, host, budget)
			if err != nil || needed {
				return value, err
			}
			return tjs.VoidValue, nil
		}
		callInvoke := func(callee, receiver tjs.Value, args []tjs.Value, needed bool) (tjs.Value, error) {
			return vm.InvokeLegacy(callee, receiver, args, host, budget, needed)
		}
		callConstruct := func(value tjs.Value, args []tjs.Value) (tjs.Value, error) {
			return vm.Construct(value, args, host, budget)
		}
		resolve := func(receiver tjs.Value, name string) (tjs.Value, bool, error) {
			return vm.GetMemberIfPresent(receiver, name, host, budget)
		}
		set := func(receiver tjs.Value, name string, value tjs.Value, flags uint32) error {
			return vm.SetMemberWithRuntime(receiver, name, value, flags, host, budget)
		}
		if _, err := decoded.ExecuteLegacyWithContextAndConstructionAndMemberResolvers(
			objectIndex, global, class, callEvaluate, callInvoke, callConstruct, resolve, set, vm.Objects,
		); err != nil {
			return fmt.Errorf("initialize compiled TJS2100 class %d: %w", objectIndex, err)
		}
	}
	return nil
}

// registerScriptsNative installs the process-free subset of C++'s static
// Scripts class. The VM constructor hook intentionally rejects instances.
func (s *Session) registerScriptsNative() error {
	class, err := s.VM.RegisterNativeClass("Scripts")
	if err != nil {
		return err
	}
	if err := s.VM.RegisterNativeMethod(class, "@initialize", "Scripts.@initialize"); err != nil {
		return err
	}
	for _, method := range []string{"execStorage", "evalStorage", "compileStorage", "exec", "eval", "dump", "getTraceString", "setCallMissing", "getClassNames", "getObjectKeys", "getObjectCount", "getMD5HashString", "equalStruct", "equalStructNumericLoose", "foreach", "clone", "getObjectContext", "isNullContext"} {
		if err := s.VM.RegisterNativeStaticMethod(class, method, "Scripts."+method); err != nil {
			return err
		}
	}
	for name, flag := range map[string]uint32{
		"pfMemberEnsure":    tjs.MemberEnsure,
		"pfMemberMustExist": tjs.MemberMustExist,
		"pfIgnoreProp":      tjs.IgnoreProperty,
		"pfHiddenMember":    tjs.HiddenMember,
		"pfStaticMember":    tjs.StaticMember,
	} {
		if err := s.VM.RegisterNativeStaticValue(class, name, tjs.IntegerValue(int64(flag))); err != nil {
			return err
		}
	}
	return s.VM.RegisterNativeStaticProperty(class, "textEncoding", "Scripts.get:textEncoding", "Scripts.set:textEncoding")
}

func (s *Services) scriptTextEncoding() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ScriptTextEncoding
}

func (s *Services) scriptTextEncodingConfig() (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ScriptTextEncoding, s.scriptTextEncodingExplicit
}

func (s *Services) setScriptTextEncoding(encoding string) {
	s.mu.Lock()
	s.ScriptTextEncoding = encoding
	s.scriptTextEncodingExplicit = true
	s.mu.Unlock()
}

func (s *Services) scriptsNative(vm *tjs.VM, operation string, args []tjs.Value, budget *uint64, resultNeeded bool) (tjs.Value, error) {
	arg := func(index int) (tjs.Value, error) {
		if index >= len(args) {
			return tjs.VoidValue, fmt.Errorf("Scripts.%s: missing argument %d", operation, index)
		}
		return args[index], nil
	}
	unsupported := func(detail string) (tjs.Value, error) {
		return tjs.VoidValue, fmt.Errorf("%w: Scripts.%s: %s", ErrUnsupportedNative, operation, detail)
	}
	finish := func(value tjs.Value, err error) (tjs.Value, error) {
		if err != nil {
			return tjs.VoidValue, err
		}
		if !resultNeeded {
			return tjs.VoidValue, nil
		}
		return value, nil
	}
	preprocessor := s.preprocessorFor(vm)
	switch operation {
	case "@initialize":
		return tjs.VoidValue, fmt.Errorf("%w: Scripts is a static class", ErrUnsupportedNative)
	case "get:textEncoding":
		return tjs.StringValue(s.scriptTextEncoding()), nil
	case "set:textEncoding":
		value, err := arg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		s.setScriptTextEncoding(value.Text())
		return tjs.VoidValue, nil
	case "execStorage", "evalStorage":
		nameValue, err := arg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		storageName := nameValue.Text()
		mode := ""
		if len(args) >= 2 && !args[1].IsVoid() {
			mode = args[1].Text()
		}
		context := tjs.VoidValue
		if len(args) >= 3 && !args[2].IsVoid() {
			context = args[2]
		}
		profile := os.Getenv("KRKRGO_RENDER_STATS") != ""
		readStarted := time.Now()
		data, err := s.readStorage(storageName)
		readTime := time.Since(readStarted)
		if err != nil {
			return tjs.VoidValue, err
		}
		offset, err := scriptReadOffset(mode)
		if err != nil {
			return tjs.VoidValue, fmt.Errorf("Scripts.%s: %w", operation, err)
		}
		if offset > uint64(len(data)) {
			return tjs.VoidValue, fmt.Errorf("Scripts.%s: binary offset %d exceeds storage size %d", operation, offset, len(data))
		}
		data = data[offset:]
		if len(data) >= 4 && string(data[:4]) == "TJS2" {
			value, err := executeTJS2Bytecode(vm, data, context, s, budget)
			if err != nil {
				return tjs.VoidValue, fmt.Errorf("Scripts.%s storage %q: %w", operation, storageName, err)
			}
			return finish(value, nil)
		}
		decodeStarted := time.Now()
		source, err := s.decodeScriptStorage(data)
		decodeTime := time.Since(decodeStarted)
		if err != nil {
			return tjs.VoidValue, err
		}
		if operation == "evalStorage" {
			source = stripEvalStorageConstDictionary(source)
		}
		executeStarted := time.Now()
		value, err := executeScriptSource(vm, s, budget, preprocessor, scriptStorageName(storageName), source, operation == "evalStorage", 0, context)
		executeTime := time.Since(executeStarted)
		if profile {
			log.Printf("krkrgo startup-script op=%s storage=%q bytes=%d read=%s decode=%s compile_execute=%s error=%t", operation, storageName, len(data), readTime, decodeTime, executeTime, err != nil)
		}
		return finish(value, err)
	case "exec", "eval":
		sourceValue, err := arg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		name := ""
		if len(args) >= 2 && !args[1].IsVoid() {
			name = args[1].Text()
		}
		var lineOffset int64
		if len(args) >= 3 && !args[2].IsVoid() {
			line, err := args[2].Int()
			if err != nil {
				return tjs.VoidValue, err
			}
			lineOffset = int64(int32(line))
		}
		context := tjs.VoidValue
		if len(args) >= 4 && !args[3].IsVoid() {
			context = args[3]
		}
		value, err := executeScriptSource(vm, s, budget, preprocessor, name, sourceValue.Text(), operation == "eval", lineOffset, context)
		return finish(value, err)
	case "getObjectContext", "isNullContext":
		value, err := arg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		if value.Kind() != tjs.KindObject || value.IsNull() {
			return tjs.VoidValue, fmt.Errorf("Scripts.%s: expected object closure", operation)
		}
		context := value.ObjectRef().Context
		if operation == "isNullContext" {
			return finish(tjs.IntegerValue(boolInt(context == 0)), nil)
		}
		if context == 0 {
			return tjs.NullValue, nil
		}
		if _, ok := vm.Objects.Lookup(context); !ok {
			return tjs.VoidValue, fmt.Errorf("Scripts.getObjectContext: invalid context")
		}
		return finish(tjs.ObjectValue(tjs.NewObjectRef(context, context)), nil)
	case "getClassNames":
		value, err := arg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		classNames, err := vm.ClassNames(value)
		if err != nil {
			return tjs.VoidValue, err
		}
		if !resultNeeded {
			return tjs.VoidValue, nil
		}
		items := make([]tjs.Value, len(classNames))
		for i, name := range classNames {
			items[i] = tjs.StringValue(name)
		}
		result, err := vm.NewArray(items)
		return finish(result, err)
	case "foreach":
		target, err := arg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		callback, err := arg(1)
		if err != nil {
			return tjs.VoidValue, err
		}
		if target.Kind() != tjs.KindObject || target.IsNull() {
			return tjs.VoidValue, fmt.Errorf("Scripts.foreach: expected object")
		}
		object, ok := vm.Objects.Lookup(target.ObjectRef().Object)
		if !ok {
			return tjs.VoidValue, fmt.Errorf("Scripts.foreach: invalid object")
		}
		entries := object.Entries()
		callbackArgs := make([]tjs.Value, 2, len(args))
		callbackArgs = append(callbackArgs, args[2:]...)
		for _, entry := range entries {
			callbackArgs[0] = tjs.StringValue(entry.Name)
			callbackArgs[1] = entry.Value
			value, err := vm.InvokeLegacy(callback, tjs.VoidValue, callbackArgs, s, budget, true)
			if err != nil {
				return tjs.VoidValue, err
			}
			if !value.IsVoid() {
				return finish(value, nil)
			}
		}
		return tjs.VoidValue, nil
	case "clone":
		value, err := arg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		cloned, err := scriptsClone(vm, value, s, budget)
		return finish(cloned, err)
	case "getObjectKeys", "getObjectCount", "getMD5HashString", "equalStruct", "equalStructNumericLoose":
		value, err := scriptsExNative(vm, operation, args, resultNeeded)
		return finish(value, err)
	case "compileStorage":
		if _, err := arg(0); err != nil {
			return tjs.VoidValue, err
		}
		if _, err := arg(1); err != nil {
			return tjs.VoidValue, err
		}
		if len(args) > 2 {
			end := len(args)
			if end > 5 {
				end = 5
			}
			for _, value := range args[2:end] {
				if !value.IsVoid() {
					if _, err := value.Int(); err != nil {
						return tjs.VoidValue, err
					}
				}
			}
		}
		return unsupported("compiled bytecode output is unavailable")
	case "dump":
		return unsupported("TJS heap dumping is unavailable")
	case "getTraceString":
		var limit int64
		if len(args) != 0 && !args[0].IsVoid() {
			var err error
			limit, err = args[0].Int()
			if err != nil {
				return tjs.VoidValue, err
			}
		}
		if !resultNeeded {
			return tjs.VoidValue, nil
		}
		return tjs.StringValue(vm.TraceString(limit)), nil
	case "setCallMissing":
		value, err := arg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		if value.Kind() != tjs.KindObject || value.IsNull() {
			return tjs.VoidValue, nil
		}
		return unsupported("TJS missing-member dispatch is unavailable")
	default:
		return tjs.VoidValue, fmt.Errorf("%w: Scripts.%s", ErrUnsupportedNative, operation)
	}
}

func executeScriptSource(vm *tjs.VM, host tjs.Host, budget *uint64, preprocessor *tjs.Preprocessor, name, source string, expression bool, lineOffset int64, context tjs.Value) (tjs.Value, error) {
	program, err := tjs.CompileWithPreprocessor(name, source, expression, preprocessor)
	if err != nil {
		return tjs.VoidValue, err
	}
	if lineOffset != 0 {
		shiftProgramLines(&program, lineOffset)
	}
	if context.Kind() == tjs.KindObject && !context.IsNull() {
		return vm.ExecuteInContext(&program, context, host, budget)
	}
	return vm.Execute(&program, host, budget)
}

// stripEvalStorageConstDictionary removes the (const) annotations emitted by
// the C++ core serializer for nested constant dictionaries and arrays. The
// annotation is storage-format metadata, so it stays scoped to evalStorage
// rather than becoming general TJS expression syntax.
func stripEvalStorageConstDictionary(source string) string {
	const annotation = "(const)"
	var out strings.Builder
	out.Grow(len(source))
	var quote byte
	escaped := false
	lineComment := false
	blockComment := false
	for i := 0; i < len(source); {
		if quote != 0 {
			c := source[i]
			out.WriteByte(c)
			i++
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == quote {
				quote = 0
			}
			continue
		}
		if lineComment {
			c := source[i]
			out.WriteByte(c)
			i++
			if c == '\n' || c == '\r' {
				lineComment = false
			}
			continue
		}
		if blockComment {
			if strings.HasPrefix(source[i:], "*/") {
				out.WriteString("*/")
				i += 2
				blockComment = false
			} else {
				out.WriteByte(source[i])
				i++
			}
			continue
		}
		if strings.HasPrefix(source[i:], "//") {
			out.WriteString("//")
			i += 2
			lineComment = true
			continue
		}
		if strings.HasPrefix(source[i:], "/*") {
			out.WriteString("/*")
			i += 2
			blockComment = true
			continue
		}
		if strings.HasPrefix(source[i:], annotation) {
			i += len(annotation)
			continue
		}
		c := source[i]
		out.WriteByte(c)
		i++
		if c == '\'' || c == '"' {
			quote = c
		}
	}
	return out.String()
}

func shiftProgramLines(program *tjs.Program, offset int64) {
	shiftProgramLinesVisited(program, offset, map[*tjs.Function]bool{}, map[*tjs.Class]bool{}, map[*tjs.Property]bool{})
}

func shiftProgramLinesVisited(program *tjs.Program, offset int64, functions map[*tjs.Function]bool, classes map[*tjs.Class]bool, properties map[*tjs.Property]bool) {
	if program == nil || offset == 0 {
		return
	}
	for i := range program.Code {
		location := &program.Code[i].Location
		if offset > 0 {
			location.Line += uint64(offset)
		} else {
			delta := uint64(-offset)
			if location.Line < delta {
				location.Line = 0
			} else {
				location.Line -= delta
			}
		}
		op := &program.Code[i].Op
		shiftFunctionLinesVisited(op.Function, offset, functions, classes, properties)
		shiftClassLinesVisited(op.Class, offset, functions, classes, properties)
		shiftPropertyLinesVisited(op.Property, offset, functions, classes, properties)
	}
}

func shiftFunctionLinesVisited(function *tjs.Function, offset int64, functions map[*tjs.Function]bool, classes map[*tjs.Class]bool, properties map[*tjs.Property]bool) {
	if function != nil && !functions[function] {
		functions[function] = true
		shiftProgramLinesVisited(&function.Program, offset, functions, classes, properties)
	}
}

func shiftPropertyLinesVisited(property *tjs.Property, offset int64, functions map[*tjs.Function]bool, classes map[*tjs.Class]bool, properties map[*tjs.Property]bool) {
	if property != nil && !properties[property] {
		properties[property] = true
		shiftFunctionLinesVisited(property.Getter, offset, functions, classes, properties)
		shiftFunctionLinesVisited(property.Setter, offset, functions, classes, properties)
	}
}

func shiftClassLinesVisited(class *tjs.Class, offset int64, functions map[*tjs.Function]bool, classes map[*tjs.Class]bool, properties map[*tjs.Property]bool) {
	if class == nil || classes[class] {
		return
	}
	classes[class] = true
	shiftProgramLinesVisited(&class.Initializer, offset, functions, classes, properties)
	for i := range class.Bases {
		shiftProgramLinesVisited(&class.Bases[i], offset, functions, classes, properties)
	}
	for i := range class.Methods {
		shiftFunctionLinesVisited(&class.Methods[i], offset, functions, classes, properties)
	}
	for i := range class.Properties {
		shiftPropertyLinesVisited(&class.Properties[i], offset, functions, classes, properties)
	}
}

func scriptStorageName(name string) string {
	if index := strings.LastIndexAny(name, "/\\>"); index >= 0 {
		return name[index+1:]
	}
	return name
}

func scriptReadOffset(mode string) (uint64, error) {
	index := strings.IndexByte(mode, 'o')
	if index < 0 {
		return 0, nil
	}
	start := index + 1
	end := start
	for end < len(mode) && end-start < 255 && mode[end] >= '0' && mode[end] <= '9' {
		end++
	}
	if start == end {
		return 0, nil
	}
	offset, err := strconv.ParseUint(mode[start:end], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid binary offset in mode %q: %w", mode, err)
	}
	return offset, nil
}

func decodeScriptText(data []byte, encoding string) (string, error) {
	hasTextBOM := len(data) >= 3 && data[0] == 0xef && data[1] == 0xbb && data[2] == 0xbf
	if len(data) >= 2 && ((data[0] == 0xfe && data[1] == 0xfe) || (data[0] == 0xff && data[1] == 0xfe) || (data[0] == 0xfe && data[1] == 0xff)) || hasTextBOM {
		return assets.Decode(data)
	}
	switch encoding {
	case "UTF-8":
		if !utf8.Valid(data) {
			return "", fmt.Errorf("invalid UTF-8 script text")
		}
		return string(data), nil
	case "Shift_JIS":
		return decodeScriptEncoding(data, japanese.ShiftJIS.NewDecoder(), "Shift-JIS")
	case "GBK", "gbk":
		return decodeScriptEncoding(data, simplifiedchinese.GBK.NewDecoder(), "GBK")
	default:
		return "", fmt.Errorf("unsupported script text encoding %q", encoding)
	}
}

func decodeScriptEncoding(data []byte, decoder transform.Transformer, name string) (string, error) {
	decoded, _, err := transform.Bytes(decoder, data)
	if err != nil {
		return "", fmt.Errorf("invalid %s script text: %w", name, err)
	}
	text := string(decoded)
	for _, r := range text {
		if r == utf8.RuneError {
			return "", fmt.Errorf("invalid %s script text: unmapped character", name)
		}
	}
	return text, nil
}
