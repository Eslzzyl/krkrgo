package tjs

import (
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Host is the native-call boundary. Implementations may optionally implement
// HostContext, HostResult, HostTrace, HostRoots, or HostTraceObjects below.
type Host interface {
	Call(*VM, string, []Value, *uint64) (Value, error)
}
type HostContext interface {
	CallWithContext(*VM, string, Value, []Value, *uint64) (Value, error)
}
type HostResult interface {
	CallWithResult(*VM, string, Value, []Value, *uint64, bool) (Value, error)
}
type HostTrace interface{ Trace(*Instruction) error }
type HostRoots interface{ GCRoots() []Value }
type HostTraceObjects interface{ GCTrace(ObjectID) []Value }

type nopHost struct{}

func (nopHost) Call(_ *VM, name string, _ []Value, _ *uint64) (Value, error) {
	return VoidValue, vmAbortError(fmt.Errorf("unsupported native call: %s", name))
}

// Vm is kept as an alias because the Rust API and older Go callers spell it Vm.
type Vm = VM

type LegacyFunction func(context Value, args []Value, host Host, budget *uint64, resultNeeded bool) (Value, error)

// legacyProperty are the compiled TJS2100 accessor objects behind one property
// context (ContextType 3). Reading the property runs the getter with zero
// parameters; writing it runs the setter with the assigned value as its single
// parameter. Both receive the accessing receiver as their this-context, which
// is how `this` inside a compiled getter/setter body is resolved.
type legacyProperty struct {
	getter Value
	setter Value
}

type vmObject struct {
	category                ObjectCategory
	native                  string
	function                *Function
	legacyFunction          LegacyFunction
	legacyClassBody         LegacyFunction
	legacyConstructor       ObjectID
	legacyClassInitializer  func(Host, *uint64) error
	callMissing             bool
	processingMissing       bool
	legacyClassInitializing bool
	legacyClassInitialized  bool
	legacySuperclassGetter  func(int, Host, *uint64) (Value, error)
	superclassGetterOffsets []int
	superclassBases         []ObjectID
	superclassBasesResolved []bool
	superclassBasesLoading  []bool
	owner                   ObjectID
	legacyProperty          *legacyProperty
	class                   *Class
	classObject             ObjectID
	instanceClassNames      []string
	classBases              []ObjectID
	nativeClass             bool
	nativeConstructor       ObjectID
	property                *Property
	array                   []Value
	arrayLength             int64
	arrayMethods            map[string]Value
	stringMethods           map[string]Value
	randomGenerator         *RandomGenerator
	methodContext           Value
	methodContextSet        bool
	superContext            ObjectID
	superOwner              ObjectID
	capturedScopes          []map[string]Value
	capturedWiths           []Value
	lexicalContext          ObjectID
	nativeStatic            bool
	finalizer               string
	scriptFinalizer         ObjectID
	finalizing              bool
	variantProperty         *Value
}

// RegisterLegacyFunction creates a VM-owned callable backed by TJS2 intercode.
func (v *VM) RegisterLegacyFunction(name string, call LegacyFunction) (Value, error) {
	value, id, err := v.alloc(CategoryFunction)
	if err != nil {
		return VoidValue, err
	}
	if call == nil {
		return VoidValue, errors.New("nil legacy function")
	}
	v.meta[id].legacyFunction = call
	if object, ok := v.Objects.Lookup(id); ok {
		if err := object.Set("name", StringValue(name)); err != nil {
			return VoidValue, err
		}
		_ = object.SetFlags("name", MemberReadOnly)
	}
	return value, nil
}

const maxVMArrayLength = 100000

// RegisterLegacyProperty creates a VM-owned property value whose accessors are
// compiled legacy callables (the property's getter and setter objects). The
// accessors keep the receiver they are read or written through as their
// this-context and run on the caller's host and execution budget, matching the
// C++ dispatch for a ContextType 3 property context.
func (v *VM) RegisterLegacyProperty(name string, getter, setter Value) (Value, error) {
	value, id, err := v.alloc(CategoryProperty)
	if err != nil {
		return VoidValue, err
	}
	accessors := &legacyProperty{}
	if getter.Kind() == KindObject && !getter.IsNull() {
		accessors.getter = getter
	}
	if setter.Kind() == KindObject && !setter.IsNull() {
		accessors.setter = setter
	}
	meta := v.meta[id]
	meta.property = &Property{Name: name}
	meta.legacyProperty = accessors
	if object, ok := v.Objects.Lookup(id); ok {
		if err := object.Set("name", StringValue(name)); err != nil {
			return VoidValue, err
		}
		_ = object.SetFlags("name", MemberReadOnly)
	}
	return value, nil
}

var errTJSNonNullObject = errors.New("TJS value is not a non-null object")

type vmHandler struct{ target, exception, scopes, withs, finally int }
type tjsThrown struct {
	value      Value
	message    string
	hasMessage bool
}

func (v *VM) thrown(value Value) *tjsThrown {
	thrown := &tjsThrown{value: value}
	if v == nil || v.Objects == nil || value.Kind() != KindObject || value.IsNull() {
		return thrown
	}
	message, ok, err := v.getMember(value.ObjectRef().Object, StringValue("message"))
	if err != nil || !ok {
		return thrown
	}
	switch message.Kind() {
	case KindString, KindInteger, KindReal:
		thrown.message = message.Text()
		thrown.hasMessage = true
	}
	return thrown
}

func (e *tjsThrown) Error() string {
	if e.hasMessage {
		return "TJS throw: " + e.message
	}
	return "TJS throw: " + e.value.Text()
}

func exceptionMessage(err error) string {
	if err == nil {
		return ""
	}
	// Keep the full wrapped chain. The innermost sentinel alone (for example
	// "unsupported media codec") discards the storage name and operation that
	// identify which asset failed.
	return err.Error()
}

func (v *VM) exceptionValue(message, trace string, h Host, b *uint64) (Value, error) {
	class, ok := v.Globals["Exception"]
	if !ok {
		return VoidValue, errors.New("TJS Exception class is unavailable")
	}
	value, err := v.construct(class, []Value{StringValue(message)}, h, b)
	if err != nil {
		return VoidValue, err
	}
	id, err := v.handle(value)
	if err != nil {
		return VoidValue, err
	}
	if err := v.setMember(id, StringValue("trace"), StringValue(trace)); err != nil {
		return VoidValue, err
	}
	return value, nil
}

type vmCompletion struct {
	kind                    string
	value                   Value
	thrown                  *tjsThrown
	target                  int
	scopes, handlers, withs int
}

type vmFrame struct {
	scopes         []map[string]Value
	parameters     map[string]Value
	scopeBase      int
	handlers       []vmHandler
	withs          []Value
	context        ObjectID
	lexicalContext ObjectID
	owner          ObjectID
	args           []Value
	argScratch     []Value
	pending        *vmCompletion
	functionName   string
	function       bool
	functionObject ObjectID
}

type vmTraceFrame struct {
	storage      string
	line         uint64
	functionName string
	function     bool
	context      ObjectID
	owner        ObjectID
}

type vmLegacyFrameRoots struct {
	registers []Value
	context   Value
}

// LegacyFrameRoots publishes the register file of a legacy intercode frame
// while nested VM execution may trigger garbage collection.
type LegacyFrameRoots func(registers []Value, context Value) func()

type vmFrameRoots struct {
	registers        []Value
	frame            *vmFrame
	host             Host
	instructionRoots []Value
	trackAllocations bool
}

// VM owns the script heap and the global namespace. Objects are retained in an
// explicit table; collection removes unreachable entries but IDs are allocated
// from the process-wide monotonic counter and are never reused.
type VM struct {
	mu                                 sync.RWMutex
	Objects                            *ObjectTable
	Globals                            map[string]Value
	Executed                           uint64
	allocationsSinceGC                 uint64
	collecting                         bool
	gcSafeDepth                        int
	depth                              int
	roots                              map[ObjectRef]struct{}
	frameRoots                         []vmFrameRoots
	legacyRoots                        []vmLegacyFrameRoots
	transientRoots                     []Value
	legacyClassInitializationDeferrals int
	meta                               map[ObjectID]*vmObject
	literals                           map[ObjectID]Value
	nativeSave                         bool
	nativeSavePlugin                   bool
	traceStack                         []vmTraceFrame
	constructing                       []ObjectID
	constructingArgs                   [][]Value
	legacyClassBodyDepth               int
	legacyConstructorDepth             int
	legacyAutoConstructorClasses       []ObjectID
	idOrder                            []ObjectID
	registerScratch                    []Value
	gcSeen                             map[ObjectID]struct{}
	gcPending                          []ObjectID
	gcHosts                            []Host
	gcIDs                              []ObjectID
	gcRemoved                          []ObjectID
	executionProfile                   *ExecutionProfile
}

func NewVM() *VM {
	table := NewObjectTable()
	global := &Object{ID: 0, Category: CategoryGlobal, members: make(map[string]member), valid: true}
	_ = table.Add(global)
	vm := &VM{Objects: table, Globals: make(map[string]Value), roots: make(map[ObjectRef]struct{}), meta: make(map[ObjectID]*vmObject), literals: make(map[ObjectID]Value), idOrder: []ObjectID{0}}
	vm.meta[0] = &vmObject{category: CategoryGlobal}
	for _, name := range []string{"Array", "Dictionary", "Object", "RegExp"} {
		_ = vm.RegisterNative(name)
	}
	array := vm.Globals["Array"]
	for _, method := range []string{"assign", "assignStruct", "split", "load", "find", "sort", "add"} {
		_ = vm.RegisterNativeMethod(array, method, "Array."+method)
	}
	dictionary := vm.Globals["Dictionary"]
	for _, method := range []string{"assign", "assignStruct", "clear"} {
		_ = vm.RegisterNativeStaticMethod(dictionary, method, "Dictionary."+method)
	}
	mathClass, _ := vm.RegisterNativeClass("Math")
	for _, method := range []string{"abs", "acos", "asin", "atan", "atan2", "ceil", "cos", "exp", "floor", "log", "max", "min", "pow", "random", "round", "sin", "sqrt", "tan"} {
		_ = vm.RegisterNativeStaticMethod(mathClass, method, "Math."+method)
	}
	for _, property := range []string{"E", "LOG2E", "LOG10E", "LN10", "LN2", "PI", "SQRT1_2", "SQRT2"} {
		_ = vm.RegisterNativeStaticProperty(mathClass, property, "Math.get:"+property, "")
	}
	randomGenerator, _ := vm.RegisterNativeClass("RandomGenerator")
	delete(vm.Globals, "RandomGenerator")
	_ = vm.RegisterNativeStaticValue(mathClass, "RandomGenerator", randomGenerator)
	for _, method := range []string{"RandomGenerator", "@invalidate", "finalize", "randomize", "random", "random32", "random63", "random64", "serialize"} {
		_ = vm.RegisterNativeMethod(randomGenerator, method, "RandomGenerator."+method)
	}
	exception, _ := vm.RegisterNativeClass("Exception")
	_ = vm.RegisterNativeMethod(exception, "Exception", "Exception.construct")
	_ = vm.RegisterSerialization(false)
	return vm
}

// SetExecutionProfile installs an optional diagnostic profile for VM execution.
// Profiling is disabled when profile is nil.
func (v *VM) SetExecutionProfile(profile *ExecutionProfile) {
	if v != nil {
		v.executionProfile = profile
	}
}
func NewVm() *VM                 { return NewVM() }
func (v *VM) globalValue() Value { return ObjectValue(NewObjectRef(0, 0)) }
func (v *VM) boundContextValue(ref ObjectRef) Value {
	return ObjectValue(boundContextRef(ref.Context))
}
func (v *VM) lookup(id ObjectID) (*Object, *vmObject, error) {
	if v == nil || v.Objects == nil {
		return nil, nil, errors.New("nil TJS VM")
	}
	o, ok := v.Objects.Lookup(id)
	if !ok {
		return nil, nil, fmt.Errorf("invalid TJS object handle: %d", id)
	}
	meta := v.meta[id]
	if (o.Category == CategoryDictionary || o.Category == CategoryArray) && (meta == nil || meta.category != o.Category) {
		newMeta := meta == nil
		// Native dispatchers can create objects directly in the VM's shared
		// object table. Import their category before VM operations inspect the
		// per-VM metadata map.
		if meta == nil {
			meta = &vmObject{category: o.Category}
		} else {
			meta.category = o.Category
		}
		if o.Category == CategoryArray {
			length := arrayLength(o)
			if length > int64(maxVMArrayLength) {
				return nil, nil, errors.New("TJS array length limit exceeded")
			}
			meta.arrayLength = length
			meta.array = make([]Value, int(length))
			for _, entry := range o.Entries() {
				index, indexed := vmArrayIndex(StringValue(entry.Name))
				if indexed && index < length {
					meta.array[index] = entry.Value
				}
			}
		}
		v.meta[id] = meta
		if newMeta {
			v.idOrder = append(v.idOrder, id)
		}
	}
	return o, meta, nil
}
func (v *VM) handle(value Value) (ObjectID, error) {
	if value.Kind() != KindObject || value.IsNull() {
		return 0, errTJSNonNullObject
	}
	id := value.ObjectRef().Object
	if _, _, e := v.lookup(id); e != nil {
		return 0, e
	}
	return id, nil
}
func (v *VM) validID(id ObjectID) error {
	o, _, e := v.lookup(id)
	if e != nil {
		return e
	}
	if !o.IsValid() {
		return errors.New("TJS object is invalid")
	}
	return nil
}
func (v *VM) alloc(category ObjectCategory) (Value, ObjectID, error) {
	if v.Objects == nil {
		return VoidValue, 0, vmAbortError(errors.New("nil TJS VM"))
	}
	if v.depth > 0 && !v.collecting && v.allocationsSinceGC >= vmGCAllocationThreshold {
		if len(v.frameRoots) != v.depth {
			return VoidValue, 0, vmAbortError(errors.New("TJS allocation reached an incomplete collection safe point"))
		}
		if _, err := v.collectAtSafePoint(v.frameRoots[len(v.frameRoots)-1].host); err != nil {
			return VoidValue, 0, vmAbortError(err)
		}
	}
	o := v.Objects.New(category)
	id := o.ID
	v.meta[id] = &vmObject{category: category}
	v.idOrder = append(v.idOrder, id)
	v.allocationsSinceGC++
	value := ObjectValue(o.Ref())
	// A constructed instance is a bound self-reference, matching TJS `new` values.
	if category == CategoryInstance {
		value = ObjectValue(o.Ref(id))
	}
	if v.depth > 0 {
		last := len(v.frameRoots) - 1
		if v.frameRoots[last].trackAllocations {
			v.frameRoots[last].instructionRoots = append(v.frameRoots[last].instructionRoots, value)
		}
	}
	return value, id, nil
}

func (v *VM) newFunctionValue(function Function, scopes []map[string]Value, withs []Value, lexicalContext ObjectID) (Value, ObjectID, error) {
	value, id, err := v.alloc(CategoryFunction)
	if err != nil {
		return VoidValue, 0, err
	}
	definition := function.Clone()
	meta := v.meta[id]
	meta.function = &definition
	meta.capturedScopes = append([]map[string]Value(nil), scopes...)
	meta.capturedWiths = append([]Value(nil), withs...)
	meta.lexicalContext = lexicalContext
	for i := range definition.Members {
		member := definition.Members[i]
		memberValue, _, memberErr := v.newFunctionValue(member, scopes, withs, lexicalContext)
		if memberErr != nil {
			return VoidValue, 0, memberErr
		}
		if memberErr = v.setMemberString(id, member.Name, memberValue); memberErr != nil {
			return VoidValue, 0, memberErr
		}
	}
	for i := range definition.Properties {
		property := definition.Properties[i].Clone()
		propertyValue, propertyID, propertyErr := v.alloc(CategoryProperty)
		if propertyErr != nil {
			return VoidValue, 0, propertyErr
		}
		propertyMeta := v.meta[propertyID]
		propertyMeta.property = &property
		propertyMeta.owner = id
		propertyMeta.capturedScopes = append([]map[string]Value(nil), scopes...)
		propertyMeta.capturedWiths = append([]Value(nil), withs...)
		propertyMeta.lexicalContext = lexicalContext
		if propertyErr = v.setMemberString(id, property.Name, propertyValue); propertyErr != nil {
			return VoidValue, 0, propertyErr
		}
	}
	return value, id, nil
}

func (v *VM) hasNestedFunction(id ObjectID, name string) bool {
	meta := v.meta[id]
	if meta == nil || meta.function == nil {
		return false
	}
	for i := range meta.function.Members {
		if meta.function.Members[i].Name == name {
			return true
		}
	}
	return false
}
func (v *VM) ObjectsSize() int {
	if v == nil || v.Objects == nil {
		return 0
	}
	return len(v.meta)
}
func (v *VM) allIDsInto(out []ObjectID) []ObjectID {
	if v == nil || v.Objects == nil {
		return out[:0]
	} // ObjectTable intentionally has no iterator; idOrder tracks VM allocations.
	return append(out[:0], v.idOrder...)
}
func (v *VM) obj(id ObjectID) (*Object, error) {
	o, _, e := v.lookup(id)
	if e != nil {
		return nil, e
	}
	return o, nil
}
func memberKeyUnits(key Value) UTF16 {
	if key.Kind() == KindString {
		return key.string
	}
	return UTF16FromString(key.Text())
}

func (v *VM) setMember(id ObjectID, key Value, val Value) error {
	o, e := v.obj(id)
	if e != nil {
		if strings.Contains(e.Error(), "invalid TJS object handle") {
			fmt.Printf("[tjs-invalid-set] receiver=%d key=%q trace=%+v\n", id, key.Text(), v.traceStack)
		}
		return e
	}
	if e := o.ChangeFlagsUnits(memberKeyUnits(key), 0, HiddenMember|StaticMember); e != nil {
		return e
	}
	return o.SetUnits(memberKeyUnits(key), val)
}

func (v *VM) setMemberString(id ObjectID, name string, val Value) error {
	o, e := v.obj(id)
	if e != nil {
		return e
	}
	if e := o.ChangeFlags(name, 0, HiddenMember|StaticMember); e != nil {
		return e
	}
	return o.SetString(name, val)
}

func (v *VM) lookupClassMemberWithRuntime(classID ObjectID, name string, depth int, skipStatic bool, host Host, budget *uint64) (Value, bool, error) {
	return v.lookupClassMemberWithRuntimeMode(classID, name, depth, skipStatic, true, host, budget)
}

func (v *VM) lookupClassMemberWithRuntimeMode(classID ObjectID, name string, depth int, skipStatic, classCall bool, host Host, budget *uint64) (Value, bool, error) {
	if depth >= 128 {
		return VoidValue, false, errors.New("class inheritance depth exceeded")
	}
	if err := v.ensureLegacyClassInitialized(classID, host, budget); err != nil {
		return VoidValue, false, err
	}
	classObject, err := v.obj(classID)
	if err != nil {
		return VoidValue, false, err
	}
	if value, ok := classObject.Get(name); ok {
		meta := v.meta[classID]
		if classCall && meta != nil && meta.legacyClassInitializing && meta.class != nil && meta.class.Name == name {
			if memberID, memberErr := v.handle(value); memberErr == nil && v.meta[memberID] != nil && v.meta[memberID].category == CategoryFunction {
				return ObjectValue(NewObjectRef(classID)), true, nil
			}
		}
		if !skipStatic {
			return value, true, nil
		}
		if _, memberFlags, direct := hasDirect(classObject, UTF16FromString(name)); direct && memberFlags&StaticMember != 0 {
			// A member marked static by Scripts.propSet is not inherited by
			// instances; fall through to the superclass chain.
		} else if memberID, memberErr := v.handle(value); memberErr != nil || v.meta[memberID] == nil || !v.meta[memberID].nativeStatic {
			return value, true, nil
		}
	}
	meta := v.meta[classID]
	if meta == nil {
		return VoidValue, false, nil
	}
	skipSuperclassConstructor := false
	if meta.legacyClassInitializing {
		if globalBase, found := v.Globals[name]; found && globalBase.Kind() == KindObject && !globalBase.IsNull() {
			if baseID, handleErr := v.handle(globalBase); handleErr == nil {
				baseMeta := v.meta[baseID]
				skipSuperclassConstructor = baseMeta != nil && baseMeta.category == CategoryClass
			}
		}
	}
	if !skipSuperclassConstructor && meta.legacyClassInitializing {
		for index := range meta.superclassGetterOffsets {
			baseID, resolveErr := v.resolveLegacySuperclassBase(classID, index, host, budget)
			if resolveErr != nil {
				return VoidValue, false, resolveErr
			}
			baseMeta := v.meta[baseID]
			if baseMeta != nil && baseMeta.class != nil && baseMeta.class.Name == name {
				skipSuperclassConstructor = true
				break
			}
		}
	}
	if !skipSuperclassConstructor {
		if value, ok, err := v.lookupLegacySuperclassMemberMode(classID, name, depth, skipStatic, classCall, host, budget); err != nil || ok {
			return value, ok, err
		}
	}
	if !skipSuperclassConstructor {
		for i := len(meta.classBases) - 1; i >= 0; i-- {
			value, ok, err := v.lookupClassMemberWithRuntimeMode(meta.classBases[i], name, depth+1, skipStatic, classCall, host, budget)
			if err != nil || ok {
				return value, ok, err
			}
		}
	}
	return VoidValue, false, nil
}

func (v *VM) lookupLegacySuperclassMember(classID ObjectID, name string, depth int, skipStatic bool, host Host, budget *uint64) (Value, bool, error) {
	return v.lookupLegacySuperclassMemberMode(classID, name, depth, skipStatic, true, host, budget)
}

func (v *VM) lookupLegacySuperclassMemberMode(classID ObjectID, name string, depth int, skipStatic, classCall bool, host Host, budget *uint64) (Value, bool, error) {
	meta := v.meta[classID]
	if meta == nil || meta.legacySuperclassGetter == nil {
		return VoidValue, false, nil
	}
	for i := len(meta.superclassGetterOffsets) - 1; i >= 0; i-- {
		baseID, err := v.resolveLegacySuperclassBase(classID, i, host, budget)
		if err != nil {
			return VoidValue, false, err
		}
		if baseID == 0 {
			continue
		}
		value, ok, err := v.lookupClassMemberWithRuntimeMode(baseID, name, depth+1, skipStatic, classCall, host, budget)
		if err != nil || ok {
			return value, ok, err
		}
	}
	return VoidValue, false, nil
}

func (v *VM) resolveLegacySuperclassBase(classID ObjectID, index int, host Host, budget *uint64) (ObjectID, error) {
	meta := v.meta[classID]
	if meta == nil || meta.legacySuperclassGetter == nil || index < 0 || index >= len(meta.superclassGetterOffsets) {
		return 0, nil
	}
	if meta.superclassBasesResolved[index] {
		return meta.superclassBases[index], nil
	}
	if meta.superclassBasesLoading[index] {
		// A class-body extends expression can read the name of the base
		// currently being resolved through the class/global proxy. Treat that
		// nested lookup as unresolved so the proxy can continue with global
		// lookup. The resolved base cycle check below remains authoritative.
		return 0, nil
	}
	meta.superclassBasesLoading[index] = true
	defer func() { meta.superclassBasesLoading[index] = false }()
	offset := meta.superclassGetterOffsets[index]
	base, err := meta.legacySuperclassGetter(offset, host, budget)
	if err != nil {
		return 0, fmt.Errorf("TJS superclass getter at offset %d for class object %d: %w", offset, classID, err)
	}
	if base.IsVoid() {
		meta.superclassBasesResolved[index] = true
		return 0, nil
	}
	if base.Kind() != KindObject || base.IsNull() {
		return 0, fmt.Errorf("TJS superclass getter at offset %d for class object %d returned %s, not a class object", offset, classID, base.TypeName())
	}
	baseID, err := v.handle(base)
	if err != nil {
		return 0, fmt.Errorf("TJS superclass getter at offset %d for class object %d: %w", offset, classID, err)
	}
	baseMeta := v.meta[baseID]
	if baseMeta == nil || (baseMeta.category != CategoryClass &&
		baseMeta.category != CategoryFunction &&
		baseMeta.category != CategoryNative &&
		baseMeta.category != CategoryMethod) {
		return 0, fmt.Errorf("TJS superclass getter at offset %d for class object %d returned a non-callable object", offset, classID)
	}
	if baseID == classID || v.classHasBase(baseID, classID, 0) {
		return 0, fmt.Errorf("recursive TJS superclass relation for class object %d", classID)
	}
	meta.superclassBases[index] = baseID
	meta.superclassBasesResolved[index] = true
	return baseID, nil
}

func (v *VM) resolveLegacyClassBases(classID ObjectID, host Host, budget *uint64) error {
	meta := v.meta[classID]
	if meta == nil || meta.legacySuperclassGetter == nil {
		return nil
	}
	for i := range meta.superclassGetterOffsets {
		if _, err := v.resolveLegacySuperclassBase(classID, i, host, budget); err != nil {
			return err
		}
	}
	return nil
}

func (v *VM) bindClassMember(value Value, context ObjectID) Value {
	if value.Kind() != KindObject || value.IsNull() {
		return value
	}
	if contextMeta := v.meta[context]; contextMeta != nil &&
		contextMeta.category == CategoryClass && len(v.constructing) != 0 {
		context = v.constructing[len(v.constructing)-1]
	}
	if contextMeta := v.meta[context]; contextMeta != nil &&
		contextMeta.category == CategoryInstance {
		// REGMEMBER binds class-body closures to the object used while the
		// class body runs. A later lookup through an instance must replace
		// that class-object binding with the actual instance.
		return ObjectValue(NewObjectRef(value.ObjectRef().Object, context))
	}
	if value.ObjectRef().HasBoundContext() {
		return value
	}
	return ObjectValue(NewObjectRef(value.ObjectRef().Object, context))
}
func (v *VM) linkClassBase(receiver Value, baseID ObjectID) {
	if receiver.Kind() != KindObject || receiver.IsNull() {
		return
	}
	if receiver.ObjectRef().Object == baseID {
		return
	}
	meta := v.meta[receiver.ObjectRef().Object]
	if meta != nil && meta.category == CategoryInstance {
		if meta.classObject == baseID {
			return
		}
		meta = v.meta[meta.classObject]
	}
	if meta == nil || meta.category != CategoryClass {
		return
	}
	for _, base := range meta.classBases {
		if base == baseID {
			return
		}
	}
	meta.classBases = append(meta.classBases, baseID)
}

func (v *VM) getMember(id ObjectID, key Value) (Value, bool, error) {
	return v.getMemberWithRuntime(id, key, nil, nil)
}

func (v *VM) getMemberWithRuntime(id ObjectID, key Value, host Host, budget *uint64) (Value, bool, error) {
	o, e := v.obj(id)
	if e != nil {
		if strings.Contains(e.Error(), "invalid TJS object handle") {
			fmt.Printf("[tjs-invalid-get] receiver=%d key=%q trace=%+v\n", id, key.Text(), v.traceStack)
		}
		return VoidValue, false, e
	}
	if meta := v.meta[id]; meta != nil && meta.category == CategoryClass {
		if err := v.ensureLegacyClassInitialized(id, host, budget); err != nil {
			return VoidValue, false, err
		}
	}
	if m := v.meta[id]; m != nil && m.category == CategoryArray {
		if index, indexed := vmArrayIndex(key); indexed {
			if index >= m.arrayLength {
				return VoidValue, false, nil
			}
			if index >= int64(len(m.array)) || m.array[index].IsVoid() {
				return VoidValue, true, nil
			}
			return m.array[index], true, nil
		}
	}
	if x, ok := o.GetUnits(memberKeyUnits(key)); ok {
		meta := v.meta[id]
		if meta != nil && meta.category == CategoryClass && meta.legacyClassInitializing && meta.class != nil && meta.class.Name == key.Text() {
			if memberID, memberErr := v.handle(x); memberErr == nil && v.meta[memberID] != nil && v.meta[memberID].category == CategoryFunction {
				return ObjectValue(NewObjectRef(id)), true, nil
			}
		}
		return x, true, nil
	}
	return v.getMemberFallbackWithRuntime(id, key.Text(), host, budget)
}

func (v *VM) getMemberStringWithRuntime(id ObjectID, name string, host Host, budget *uint64) (Value, bool, error) {
	o, e := v.obj(id)
	if e != nil {
		return VoidValue, false, e
	}
	if m := v.meta[id]; m != nil && m.category == CategoryArray {
		if index, indexed := vmArrayIndex(StringValue(name)); indexed {
			if index >= m.arrayLength {
				return VoidValue, false, nil
			}
			if index >= int64(len(m.array)) || m.array[index].IsVoid() {
				return VoidValue, true, nil
			}
			return m.array[index], true, nil
		}
	}
	if x, ok := o.GetString(name); ok {
		return x, true, nil
	}
	return v.getMemberFallbackWithRuntime(id, name, host, budget)
}

func (v *VM) getMemberString(id ObjectID, name string) (Value, bool, error) {
	return v.getMemberStringWithRuntime(id, name, nil, nil)
}

func (v *VM) getMemberFallbackWithRuntime(id ObjectID, name string, host Host, budget *uint64) (Value, bool, error) {
	if id == 0 {
		if x, ok := v.Globals[name]; ok {
			return x, true, nil
		}
	}
	meta := v.meta[id]
	if meta == nil {
		return VoidValue, false, nil
	}
	boundContext := meta.superContext
	if meta.category == CategoryInstance {
		boundContext = id
	}
	if meta.superOwner != 0 {
		if _, err := v.obj(meta.superOwner); err != nil {
			return VoidValue, false, err
		}
		ownerMeta := v.meta[meta.superOwner]
		if ownerMeta != nil {
			for i := len(ownerMeta.classBases) - 1; i >= 0; i-- {
				if value, ok, lookupErr := v.lookupClassMemberWithRuntimeMode(ownerMeta.classBases[i], name, 0, false, false, host, budget); lookupErr != nil {
					return VoidValue, false, lookupErr
				} else if ok {
					return v.bindClassMember(value, boundContext), true, nil
				}
			}
		}
		if ownerMeta != nil {
			if value, ok, err := v.lookupLegacySuperclassMemberMode(meta.superOwner, name, 0, false, false, host, budget); err != nil {
				return VoidValue, false, err
			} else if ok {
				return v.bindClassMember(value, boundContext), true, nil
			}
		}
		if ownerMeta != nil && ownerMeta.class != nil && ownerMeta.class.Name != "" {
			for i := len(ownerMeta.classBases) - 1; i >= 0; i-- {
				baseID := ownerMeta.classBases[i]
				base, baseErr := v.obj(baseID)
				if baseErr != nil {
					return VoidValue, false, baseErr
				}
				baseMeta := v.meta[baseID]
				if baseMeta == nil || baseMeta.class == nil || baseMeta.class.Name != name {
					continue
				}
				if initializer, found := base.Get("@initialize"); found {
					return v.bindClassMember(initializer, boundContext), true, nil
				}
				if baseMeta.nativeConstructor != 0 {
					return ObjectValue(NewObjectRef(baseMeta.nativeConstructor, boundContext)), true, nil
				}
			}
			for i := len(ownerMeta.superclassBases) - 1; i >= 0; i-- {
				baseID := ownerMeta.superclassBases[i]
				if baseID == 0 {
					continue
				}
				base, baseErr := v.obj(baseID)
				if baseErr != nil {
					return VoidValue, false, baseErr
				}
				baseMeta := v.meta[baseID]
				if baseMeta == nil || baseMeta.class == nil || baseMeta.class.Name != name {
					continue
				}
				if initializer, found := base.Get("@initialize"); found {
					return v.bindClassMember(initializer, boundContext), true, nil
				}
				if baseMeta.nativeConstructor != 0 {
					return ObjectValue(NewObjectRef(baseMeta.nativeConstructor, boundContext)), true, nil
				}
			}
		}
	}
	if meta.classObject != 0 {
		value, ok, err := v.lookupClassMemberWithRuntime(meta.classObject, name, 0, meta.category == CategoryInstance, host, budget)
		if err != nil || !ok {
			return value, ok, err
		}
		if meta.category == CategoryInstance {
			value = v.bindClassMember(value, id)
		}
		return value, true, nil
	}
	if meta.category == CategoryClass {
		return v.lookupClassMemberWithRuntime(id, name, 0, false, host, budget)
	}
	return VoidValue, false, nil
}
func (v *VM) deleteMember(id ObjectID, key Value) (bool, error) {
	o, e := v.obj(id)
	if e != nil {
		return false, e
	}
	units := memberKeyUnits(key)
	if m := v.meta[id]; m != nil && m.category == CategoryArray {
		if index, indexed := vmArrayIndex(key); indexed {
			if _, memberFlags, ok := hasDirect(o, units); ok && (memberDontDelete(memberFlags) || memberReadOnly(memberFlags)) {
				return false, ErrCannotDelete
			}
			if index < 0 || index >= m.arrayLength || index >= int64(len(m.array)) || m.array[index].IsVoid() {
				return false, nil
			}
			m.array[index] = VoidValue
			return true, nil
		}
	}
	return o.DeleteUnits(units), nil
}

// GetMember reads a script-visible member from a value, evaluating native or
// script property getters with the supplied host and execution budget.
func (v *VM) GetMember(receiver Value, name string, host Host, budget *uint64) (Value, error) {
	value, _, err := v.GetMemberIfPresent(receiver, name, host, budget)
	return value, err
}

func (v *VM) collectionClassMember(id ObjectID, key Value, host Host, budget *uint64) (Value, bool, error) {
	meta := v.meta[id]
	if meta == nil || (meta.category != CategoryArray && meta.category != CategoryDictionary) {
		return VoidValue, false, nil
	}
	className := "Dictionary"
	if meta.category == CategoryArray {
		className = "Array"
	}
	if !v.nativeSavePlugin && !(className == "Array" && key.Text() == "add") {
		return VoidValue, false, nil
	}
	classValue, exists := v.Globals[className]
	if !exists {
		return VoidValue, false, nil
	}
	member, found, err := v.getMemberWithRuntime(classValue.ObjectRef().Object, key, host, budget)
	if err != nil || !found {
		return VoidValue, found, err
	}
	return member, true, nil
}

// GetMemberIfPresent reads a script-visible member and reports whether it exists.
func (v *VM) GetMemberIfPresent(receiver Value, name string, host Host, budget *uint64) (Value, bool, error) {
	if v == nil || v.Objects == nil {
		return VoidValue, false, errors.New("nil TJS VM")
	}
	key := StringValue(name)
	if receiver.Kind() == KindString {
		if property, ok := vmStringProperty(receiver, key); ok {
			return property, true, nil
		}
		if method, ok, err := v.vmStringMethod(name); err != nil || ok {
			return method, ok, err
		}
	}
	id, err := v.handle(receiver)
	if err != nil {
		return VoidValue, false, err
	}
	member, ok, err := v.getMemberWithRuntime(id, key, host, budget)
	if err != nil {
		return VoidValue, false, err
	}
	if !ok && receiver.Kind() == KindObject && !receiver.IsNull() {
		className := ""
		if meta := v.meta[id]; meta != nil {
			switch meta.category {
			case CategoryArray:
				className = "Array"
			case CategoryDictionary:
				className = "Dictionary"
			}
		}
		if className != "" && (className == "Dictionary" || name == "add" || v.nativeSavePlugin) {
			if arrayClass, exists := v.Globals[className]; exists {
				classMember, found, memberErr := v.getMemberWithRuntime(arrayClass.ObjectRef().Object, key, host, budget)
				if memberErr != nil {
					return VoidValue, false, memberErr
				}
				if found {
					value, valueErr := v.getValue(receiver, classMember, false, host, budget)
					return value, valueErr == nil, valueErr
				}
			}
		}
	}
	if !ok {
		member, ok, err = v.vmRegexpMethod(id, name)
		if err != nil || !ok {
			if err != nil {
				return VoidValue, false, err
			}
			if !ok {
				value, handled, missingErr := v.missingMember(receiver, key, nil, host, budget)
				if missingErr != nil || !handled {
					return value, handled, missingErr
				}
				return value, true, nil
			}
		}
	}
	value, err := v.getValue(receiver, member, false, host, budget)
	return value, true, err
}

// GetMemberRawIfPresent returns a member descriptor without invoking a script
// property accessor. Legacy GPDS/GPIS use this for address-style access.
func (v *VM) GetMemberRawIfPresent(receiver Value, name string, host Host, budget *uint64) (Value, bool, error) {
	if v == nil || v.Objects == nil {
		return VoidValue, false, errors.New("nil TJS VM")
	}
	id, err := v.handle(receiver)
	if err != nil {
		return VoidValue, false, err
	}
	member, ok, err := v.getMemberWithRuntime(id, StringValue(name), host, budget)
	if err != nil {
		return VoidValue, false, err
	}
	if !ok {
		value, handled, missingErr := v.missingMember(receiver, StringValue(name), nil, host, budget)
		if missingErr != nil || !handled {
			return value, handled, missingErr
		}
		return value, true, nil
	}
	value, err := v.getValue(receiver, member, true, host, budget)
	return value, true, err
}

// SetMemberWithRuntime writes a script-visible member, including inherited
// property setters, using the caller's host and execution budget.
func (v *VM) SetMemberWithRuntime(receiver Value, name string, value Value, flags uint32, host Host, budget *uint64) error {
	if v == nil || v.Objects == nil {
		return errors.New("nil TJS VM")
	}
	id, err := v.handle(receiver)
	if err != nil {
		return err
	}
	object, err := v.obj(id)
	if err != nil {
		return err
	}
	if direct, memberFlags, ok := hasDirect(object, UTF16FromString(name)); ok {
		if memberReadOnly(memberFlags) {
			return ErrReadOnly
		}
		if err := object.ChangeFlags(name, flags&(HiddenMember|StaticMember), ^flags&(HiddenMember|StaticMember)); err != nil {
			return err
		}
		if flags&IgnoreProperty == 0 {
			if propertyID, handleErr := v.handle(direct); handleErr == nil && v.meta[propertyID] != nil && v.meta[propertyID].category == CategoryProperty {
				return v.writeProperty(direct, receiver, value, host, budget)
			}
		}
		if err := v.setMemberString(id, name, value); err != nil {
			return err
		}
		return object.ChangeFlags(name, flags&(HiddenMember|StaticMember), 0)
	}
	if meta := v.meta[id]; meta != nil && meta.category == CategoryClass && meta.legacyClassInitializer != nil && !meta.legacyClassInitialized {
		// A deferred script class may receive static values before its body is
		// runnable. Store the value and let the first read or construction run
		// the initializer with the then-current runtime globals.
		if err := v.setMemberString(id, name, value); err != nil {
			return err
		}
		return object.ChangeFlags(name, flags&(HiddenMember|StaticMember), 0)
	}
	member, exists, err := v.getMemberWithRuntime(id, StringValue(name), host, budget)
	if err != nil {
		return err
	}
	if exists {
		if flags&IgnoreProperty == 0 {
			if propertyID, handleErr := v.handle(member); handleErr == nil && v.meta[propertyID] != nil && v.meta[propertyID].category == CategoryProperty {
				return v.writeProperty(member, receiver, value, host, budget)
			}
		}
		if err := v.setMemberString(id, name, value); err != nil {
			return err
		}
		return object.ChangeFlags(name, flags&(HiddenMember|StaticMember), 0)
	}
	if _, handled, missingErr := v.missingMember(receiver, StringValue(name), &value, host, budget); missingErr != nil {
		return missingErr
	} else if handled {
		return nil
	}
	if flags&MemberMustExist != 0 && object.Category == CategoryDictionary {
		return ErrMemberNotFound
	}
	if err := v.setMemberString(id, name, value); err != nil {
		return err
	}
	return object.ChangeFlags(name, flags&(HiddenMember|StaticMember), 0)
}

// ArrayElement returns an array's stored element without applying property
// dispatch. Negative indexes count from the end, and an in-range hole exists
// and yields void.
func (v *VM) ArrayElement(value Value, index int64) (Value, bool, error) {
	if v == nil || v.Objects == nil {
		return VoidValue, false, errors.New("nil TJS VM")
	}
	id, err := v.handle(value)
	if err != nil {
		return VoidValue, false, err
	}
	meta := v.meta[id]
	if meta == nil || meta.category != CategoryArray {
		return VoidValue, false, errors.New("TJS array required")
	}
	element, exists := arrayElementAt(meta, index)
	return element, exists, nil
}

// GetMemberByKey reads a member with the flag semantics of the C++ dispatch
// API used by Scripts.propGet. The key keeps its variant type: integer and
// numeric-string keys address array elements, and negative indexes count from
// the end. The boolean result reports whether the caller may discard the
// value. The reference PropGet returns TJS_E_INVALIDPARAM when its result
// pointer is null and the member resolves to plain data, while a getter that
// runs or a missing optional dictionary member succeeds.
func (v *VM) GetMemberByKey(receiver, key Value, flags uint32, host Host, budget *uint64) (Value, bool, error) {
	if v == nil || v.Objects == nil {
		return VoidValue, false, errors.New("nil TJS VM")
	}
	id, err := v.handle(receiver)
	if err != nil {
		return VoidValue, false, err
	}
	meta := v.meta[id]
	name := key.Text()
	if meta != nil && meta.category == CategoryArray {
		if index, indexed := arrayMemberIndex(key); indexed {
			element, exists := arrayElementAt(meta, index)
			if !exists {
				if flags&MemberMustExist != 0 {
					return VoidValue, false, ErrMemberNotFound
				}
				// TJSDefaultPropGet on a void element still requires a result.
				return VoidValue, false, nil
			}
			if flags&IgnoreProperty != 0 {
				return element, false, nil
			}
			return v.readMemberValue(receiver, element, flags, host, budget)
		}
		if property, ok := vmArrayProperty(meta, StringValue(name)); ok {
			return v.readMemberValue(receiver, property, flags, host, budget)
		}
	}
	member, found, err := v.getMemberWithRuntime(id, key, host, budget)
	if err != nil {
		return VoidValue, false, err
	}
	if !found && meta != nil && meta.category == CategoryArray {
		member, found, err = v.vmArrayMethod(id, name)
		if err != nil {
			return VoidValue, false, err
		}
	}
	if !found && meta != nil && (meta.category == CategoryArray || meta.category == CategoryDictionary) {
		className := "Dictionary"
		if meta.category == CategoryArray {
			className = "Array"
		}
		if v.nativeSavePlugin || (className == "Array" && name == "add") {
			if classValue, exists := v.Globals[className]; exists {
				member, found, err = v.getMemberWithRuntime(classValue.ObjectRef().Object, key, host, budget)
				if err != nil {
					return VoidValue, false, err
				}
				if found {
					value, discard, valueErr := v.readMemberValue(receiver, member, flags, host, budget)
					return value, discard, valueErr
				}
			}
		}
	}
	if !found {
		if value, handled, missingErr := v.missingMember(receiver, key, nil, host, budget); missingErr != nil {
			return VoidValue, false, missingErr
		} else if handled {
			return value, true, nil
		}
	}
	if !found {
		if flags&MemberEnsure != 0 {
			if err := v.setMember(id, key, VoidValue); err != nil {
				return VoidValue, false, err
			}
			// The created data member is void; PropGet without a result still
			// fails with TJS_E_INVALIDPARAM.
			return VoidValue, false, nil
		}
		if meta != nil && meta.category == CategoryDictionary && flags&MemberMustExist == 0 {
			return VoidValue, true, nil
		}
		return VoidValue, false, ErrMemberNotFound
	}
	return v.readMemberValue(receiver, member, flags, host, budget)
}

// readMemberValue applies TJSDefaultPropGet to one resolved member. A property
// with a getter runs it and may be discarded; a raw read or plain data member
// may not be discarded because the reference returns TJS_E_INVALIDPARAM.
func (v *VM) readMemberValue(receiver, member Value, flags uint32, host Host, budget *uint64) (Value, bool, error) {
	if flags&IgnoreProperty != 0 {
		value, err := v.getValue(receiver, member, true, host, budget)
		return value, false, err
	}
	if id, err := v.handle(member); err == nil && v.meta[id] != nil && v.meta[id].category == CategoryProperty {
		if !v.propertyAccessorPresent(member, true) {
			return VoidValue, true, ErrAccessDenied
		}
		value, err := v.getValue(receiver, member, false, host, budget)
		return value, true, err
	}
	value, err := v.getValue(receiver, member, false, host, budget)
	return value, false, err
}

// SetMemberByKey writes a member with the flag semantics of the C++ dispatch
// API used by Scripts.propSet. Missing object members require MemberEnsure; an
// array index at or past the end grows the array unless MemberMustExist, and a
// negative index that remains negative is rejected after end-relative
// adjustment.
func (v *VM) SetMemberByKey(receiver, key, value Value, flags uint32, host Host, budget *uint64) error {
	if v == nil || v.Objects == nil {
		return errors.New("nil TJS VM")
	}
	id, err := v.handle(receiver)
	if err != nil {
		return err
	}
	meta := v.meta[id]
	if meta != nil && meta.category == CategoryArray {
		if index, indexed := arrayMemberIndex(key); indexed {
			return v.writeArrayElement(receiver, meta, index, value, flags, host, budget)
		}
	}
	object, err := v.obj(id)
	if err != nil {
		return err
	}
	units := memberKeyUnits(key)
	_, _, direct := hasDirect(object, units)
	member, found, err := v.getMemberWithRuntime(id, key, host, budget)
	if err != nil {
		return err
	}
	if found {
		if !direct {
			// TJS instances own a copy of each inherited member. Materialize
			// it before applying flags; flag-only storage would shadow an
			// inherited property with void.
			if err := v.setMember(id, key, member); err != nil {
				return err
			}
		}
		if err := object.ChangeFlagsUnits(units, flags&(HiddenMember|StaticMember), ^flags&(HiddenMember|StaticMember)); err != nil {
			return err
		}
		if flags&IgnoreProperty == 0 {
			if propertyID, handleErr := v.handle(member); handleErr == nil && v.meta[propertyID] != nil && v.meta[propertyID].category == CategoryProperty {
				if !v.propertyAccessorPresent(member, false) {
					return ErrAccessDenied
				}
				return v.writeProperty(member, receiver, value, host, budget)
			}
		}
		if err := v.setMember(id, key, value); err != nil {
			return err
		}
		return object.ChangeFlagsUnits(units, flags&(HiddenMember|StaticMember), 0)
	}
	if flags&MemberEnsure == 0 {
		if _, handled, missingErr := v.missingMember(receiver, key, &value, host, budget); missingErr != nil {
			return missingErr
		} else if handled {
			return nil
		}
		return ErrMemberNotFound
	}
	if _, handled, missingErr := v.missingMember(receiver, key, &value, host, budget); missingErr != nil {
		return missingErr
	} else if handled {
		return nil
	}
	if err := v.setMember(id, key, value); err != nil {
		return err
	}
	return object.ChangeFlagsUnits(units, flags&(HiddenMember|StaticMember), 0)
}

// writeArrayElement applies TJSArrayObject::PropSetByNum.
func (v *VM) writeArrayElement(receiver Value, meta *vmObject, index int64, value Value, flags uint32, host Host, budget *uint64) error {
	n := index
	if n < 0 {
		n += meta.arrayLength
	}
	if n < 0 {
		return ErrMemberNotFound
	}
	if n >= meta.arrayLength && flags&MemberMustExist != 0 {
		return ErrMemberNotFound
	}
	if n >= int64(maxVMArrayLength) {
		return errors.New("TJS array length limit exceeded")
	}
	existing := VoidValue
	if n < int64(len(meta.array)) {
		existing = meta.array[n]
	}
	if flags&IgnoreProperty == 0 {
		if propertyID, err := v.handle(existing); err == nil && v.meta[propertyID] != nil && v.meta[propertyID].category == CategoryProperty {
			if !v.propertyAccessorPresent(existing, false) {
				return ErrAccessDenied
			}
			return v.writeProperty(existing, receiver, value, host, budget)
		}
	}
	if n >= int64(len(meta.array)) {
		meta.array = append(meta.array, make([]Value, int(n)+1-len(meta.array))...)
	}
	meta.array[n] = value
	if n >= meta.arrayLength {
		meta.arrayLength = n + 1
	}
	return nil
}

// arrayElementAt resolves one array index the way TJSArrayObject reads items:
// negative indexes count from the end, and an in-range hole is a present void.
func arrayElementAt(meta *vmObject, index int64) (Value, bool) {
	if meta == nil || meta.category != CategoryArray {
		return VoidValue, false
	}
	n := index
	if n < 0 {
		n += meta.arrayLength
	}
	if n < 0 || n >= meta.arrayLength {
		return VoidValue, false
	}
	if n >= int64(len(meta.array)) {
		return VoidValue, true
	}
	return meta.array[n], true
}

// propertyAccessorPresent reports whether a property descriptor carries the
// requested accessor. The reference returns TJS_E_ACCESSDENYED when a read has
// no getter or a write has no setter.
func (v *VM) propertyAccessorPresent(property Value, getter bool) bool {
	id, err := v.handle(property)
	if err != nil {
		return true
	}
	meta := v.meta[id]
	if meta == nil || meta.category != CategoryProperty {
		return true
	}
	if meta.legacyProperty != nil {
		accessor := meta.legacyProperty.getter
		if !getter {
			accessor = meta.legacyProperty.setter
		}
		return accessor.Kind() == KindObject && !accessor.IsNull()
	}
	if meta.variantProperty != nil {
		return true
	}
	if meta.property == nil {
		return false
	}
	if getter {
		return meta.property.Getter != nil
	}
	return meta.property.Setter != nil
}

// SetPropertyWithRuntime writes through a property descriptor value itself.
// Legacy VM_SETP supplies the descriptor closure, including its bound context,
// even when the target object now stores a replacement descriptor.
func (v *VM) SetPropertyWithRuntime(property, value Value, host Host, budget *uint64) error {
	if v == nil || v.Objects == nil {
		return errors.New("nil TJS VM")
	}
	if property.Kind() != KindObject || property.IsNull() {
		return errors.New("property descriptor is not an object")
	}
	id, err := v.handle(property)
	if err != nil {
		return err
	}
	meta := v.meta[id]
	if meta == nil || meta.category != CategoryProperty {
		return errors.New("value is not a property descriptor")
	}
	context := VoidValue
	if property.ObjectRef().HasBoundContext() {
		context = v.boundContextValue(property.ObjectRef())
	}
	return v.writeProperty(property, context, value, host, budget)
}

// Construct creates a script class instance through the VM's normal lifecycle.
func (v *VM) Construct(callee Value, args []Value, host Host, budget *uint64) (Value, error) {
	return v.construct(callee, args, host, budget)
}
func (v *VM) NewArray(items []Value) (Value, error) {
	if len(items) > maxVMArrayLength {
		return VoidValue, errors.New("TJS array length limit exceeded")
	}
	x, id, e := v.alloc(CategoryArray)
	if e != nil {
		return VoidValue, e
	}
	for index, item := range items {
		if e := v.setMember(id, IntegerValue(int64(index)), item); e != nil {
			return VoidValue, e
		}
	}
	x = ObjectValue(NewObjectRef(id, id))
	v.meta[id].array = append([]Value(nil), items...)
	v.meta[id].arrayLength = int64(len(items))
	return x, nil
}

// GetArrayLength returns the script-visible length of a TJS array value.
func (v *VM) GetArrayLength(value Value) (int64, error) {
	id, err := v.handle(value)
	if err != nil {
		return 0, err
	}
	m := v.meta[id]
	if m == nil || m.category != CategoryArray {
		return 0, ErrType
	}
	return m.arrayLength, nil
}

// GetArrayElement reads an initialized element from a TJS array value.
func (v *VM) GetArrayElement(value Value, index int64) (Value, bool, error) {
	id, err := v.handle(value)
	if err != nil {
		return VoidValue, false, err
	}
	m := v.meta[id]
	if m == nil || m.category != CategoryArray {
		return VoidValue, false, ErrType
	}
	if index < 0 || index >= m.arrayLength || index >= int64(len(m.array)) || m.array[index].IsVoid() {
		return VoidValue, false, nil
	}
	return m.array[index], true, nil
}

func (v *VM) NewDictionary() (Value, error) {
	_, id, e := v.alloc(CategoryDictionary)
	if e != nil {
		return VoidValue, e
	}
	return ObjectValue(NewObjectRef(id, id)), nil
}
func (v *VM) ArrayItems(value Value) ([]Value, error) {
	id, e := v.handle(value)
	if e != nil {
		return nil, e
	}
	m := v.meta[id]
	if m == nil || m.category != CategoryArray {
		return nil, errors.New("Array native instance required")
	}
	return append([]Value(nil), m.array...), nil
}
func (v *VM) ClearArray(value Value) error {
	id, err := v.handle(value)
	if err != nil {
		return err
	}
	m := v.meta[id]
	if m == nil || m.category != CategoryArray {
		return errors.New("Array native instance required")
	}
	object, err := v.obj(id)
	if err != nil {
		return err
	}
	if err := resizeArrayLength(object, 0); err != nil {
		return err
	}
	m.array = nil
	m.arrayLength = 0
	return nil
}
func (v *VM) AppendArray(value Value, item Value) error {
	id, e := v.handle(value)
	if e != nil {
		return e
	}
	m := v.meta[id]
	if m == nil || m.category != CategoryArray {
		return errors.New("Array native instance required")
	}
	if m.arrayLength >= maxVMArrayLength {
		return errors.New("TJS array length limit exceeded")
	}
	if e := v.setMember(id, IntegerValue(m.arrayLength), item); e != nil {
		return e
	}
	m.array = append(m.array, item)
	m.arrayLength++
	return nil
}
func (v *VM) RegisterNamespace(name string) (Value, error) {
	if x, ok := v.Globals[name]; ok {
		return x, nil
	}
	x, _, e := v.alloc(CategoryNamespace)
	if e != nil {
		return VoidValue, e
	}
	v.Globals[name] = x
	v.roots[x.ObjectRef()] = struct{}{}
	return x, nil
}
func (v *VM) RegisterNative(name string) error {
	x, id, e := v.alloc(CategoryNative)
	if e != nil {
		return e
	}
	v.meta[id].native = name
	if dot := strings.IndexByte(name, '.'); dot >= 0 {
		ns, e := v.RegisterNamespace(name[:dot])
		if e != nil {
			return e
		}
		return v.setMember(ns.ObjectRef().Object, StringValue(name[dot+1:]), x)
	}
	v.Globals[name] = x
	v.roots[x.ObjectRef()] = struct{}{}
	return nil
}
func (v *VM) RegisterNativeClass(name string) (Value, error) {
	x, id, e := v.alloc(CategoryClass)
	if e != nil {
		return VoidValue, e
	}
	_, constructorID, e := v.alloc(CategoryNative)
	if e != nil {
		return VoidValue, e
	}
	v.meta[constructorID].native = name + ".construct"
	v.meta[id].class = &Class{Name: name}
	v.meta[id].classObject = id
	v.meta[id].nativeClass = true
	v.meta[id].nativeConstructor = constructorID
	v.Globals[name] = x
	v.roots[x.ObjectRef()] = struct{}{}
	return x, nil
}

// AddNativeClassAlias adds a script-visible class name used by instanceof while
// keeping the native class stored under its original global name.
func (v *VM) AddNativeClassAlias(class Value, alias string) error {
	if class.Kind() != KindObject || class.IsNull() {
		return ErrType
	}
	id := class.ObjectRef().Object
	meta := v.meta[id]
	if meta == nil || meta.category != CategoryClass || meta.class == nil {
		return ErrType
	}
	if alias == "" || alias == meta.class.Name {
		return nil
	}
	for _, existing := range meta.class.Aliases {
		if existing == alias {
			return nil
		}
	}
	meta.class.Aliases = append(meta.class.Aliases, alias)
	return nil
}

// RegisterLegacyClass creates an unbound class object for a compiled TJS2100
// class. Unlike a native class, its constructor is represented by compiled
// members. The loader binds the class into the scope that declares it, matching
// tTJSByteCodeLoader's replay of each object's properties onto its parent
// context; only top-level declarations reach the global namespace, through
// RegisterGlobalLegacyClass.
func (v *VM) RegisterLegacyClass(name string) (Value, error) {
	value, id, err := v.alloc(CategoryClass)
	if err != nil {
		return VoidValue, err
	}
	v.meta[id].class = &Class{Name: name}
	v.meta[id].classObject = id
	v.roots[value.ObjectRef()] = struct{}{}
	return value, nil
}

// RegisterGlobalLegacyClass creates a compiled class declared in the script's
// top-level context and exposes it as a global member.
func (v *VM) RegisterGlobalLegacyClass(name string) (Value, error) {
	value, err := v.RegisterLegacyClass(name)
	if err != nil {
		return VoidValue, err
	}
	v.Globals[name] = value
	return value, nil
}

// SetLegacyClassBody installs the intercode dispatcher used when a compiled
// class object is called without a member name (for superclass initialization).
func (v *VM) SetLegacyClassBody(class Value, body LegacyFunction) error {
	id, err := v.handle(class)
	if err != nil {
		return err
	}
	if err = v.validID(id); err != nil {
		return err
	}
	meta := v.meta[id]
	if meta == nil || meta.category != CategoryClass {
		return errors.New("TJS legacy class body target is not a class")
	}
	if body == nil {
		return errors.New("TJS legacy class body is nil")
	}
	meta.legacyClassBody = body
	return nil
}

// SetLegacyClassConstructor records the constructor declared by a compiled
// class. TJS scripts may later assign a same-named class member; construction
// still retains the declaration's constructor context.
func (v *VM) SetLegacyClassConstructor(class, constructor Value) error {
	classID, err := v.handle(class)
	if err != nil {
		return err
	}
	constructorID, err := v.handle(constructor)
	if err != nil {
		return err
	}
	meta := v.meta[classID]
	if meta == nil || meta.category != CategoryClass {
		return errors.New("TJS legacy constructor target is not a class")
	}
	meta.legacyConstructor = constructorID
	return nil
}

// SetLegacyClassInitializer installs lazy initialization for a deferred class.
func (v *VM) SetLegacyClassInitializer(class Value, initializer func(Host, *uint64) error) error {
	id, err := v.handle(class)
	if err != nil {
		return err
	}
	if err = v.validID(id); err != nil {
		return err
	}
	meta := v.meta[id]
	if meta == nil || meta.category != CategoryClass {
		return errors.New("TJS legacy class initializer target is not a class")
	}
	if initializer == nil {
		return errors.New("TJS legacy class initializer is nil")
	}
	meta.legacyClassInitializer = initializer
	return nil
}

// DeferLegacyClassInitialization postpones lazy compiled class setup until the
// returned release function is called. Top-level bytecode uses this while it
// installs class-level values that class bodies may read during setup.
func (v *VM) DeferLegacyClassInitialization() func() {
	if v == nil {
		return func() {}
	}
	v.legacyClassInitializationDeferrals++
	return func() {
		if v.legacyClassInitializationDeferrals > 0 {
			v.legacyClassInitializationDeferrals--
		}
	}
}

func (v *VM) ensureLegacyClassInitialized(classID ObjectID, host Host, budget *uint64) error {
	if v != nil && v.legacyClassInitializationDeferrals > 0 {
		return nil
	}
	return v.initializeLegacyClassNow(classID, host, budget)
}

func (v *VM) initializeLegacyClassNow(classID ObjectID, host Host, budget *uint64) error {
	if v == nil {
		return nil
	}
	deferrals := v.legacyClassInitializationDeferrals
	v.legacyClassInitializationDeferrals = 0
	defer func() { v.legacyClassInitializationDeferrals = deferrals }()
	meta := v.meta[classID]
	if meta == nil || meta.legacyClassInitializer == nil || meta.legacyClassInitialized {
		return nil
	}
	if meta.legacyClassInitializing {
		return nil
	}
	meta.legacyClassInitializing = true
	err := meta.legacyClassInitializer(host, budget)
	meta.legacyClassInitializing = false
	if err != nil {
		return err
	}
	meta.legacyClassInitialized = true
	return nil
}

// InitializeLegacyClass runs a registered deferred class initializer with the
// supplied runtime context.
func (v *VM) InitializeLegacyClass(class Value, host Host, budget *uint64) error {
	id, err := v.handle(class)
	if err != nil {
		return err
	}
	return v.ensureLegacyClassInitialized(id, host, budget)
}

// PublishLegacyFrameRoots keeps a legacy intercode register file visible to GC.
func (v *VM) PublishLegacyFrameRoots(registers []Value, context Value) func() {
	index := len(v.legacyRoots)
	v.legacyRoots = append(v.legacyRoots, vmLegacyFrameRoots{registers: registers, context: context})
	return func() {
		if index < len(v.legacyRoots) {
			v.legacyRoots[index] = vmLegacyFrameRoots{}
			v.legacyRoots = v.legacyRoots[:index]
		}
	}
}

// SetCallMissing enables the TJS missing-member protocol for an object.
func (v *VM) SetCallMissing(value Value) error {
	id, err := v.handle(value)
	if err != nil {
		return err
	}
	meta := v.meta[id]
	if meta == nil {
		return errors.New("TJS missing-member target is unavailable")
	}
	meta.callMissing = true
	return nil
}

// SetLegacyClassSuperclassGetter installs deferred base-expression entry points
// decoded from a compiled TJS class.
func (v *VM) SetLegacyClassSuperclassGetter(class Value, offsets []int, getter func(int, Host, *uint64) (Value, error)) error {
	id, err := v.handle(class)
	if err != nil {
		return err
	}
	if err = v.validID(id); err != nil {
		return err
	}
	meta := v.meta[id]
	if meta == nil || meta.category != CategoryClass {
		return errors.New("TJS legacy superclass getter target is not a class")
	}
	if getter == nil {
		return errors.New("TJS legacy superclass getter is nil")
	}
	meta.superclassGetterOffsets = append([]int(nil), offsets...)
	meta.superclassBases = make([]ObjectID, len(offsets))
	meta.superclassBasesResolved = make([]bool, len(offsets))
	meta.superclassBasesLoading = make([]bool, len(offsets))
	meta.legacySuperclassGetter = getter
	return nil
}
func (v *VM) NewNativeClass(name, initializer string) (Value, error) {
	x, err := v.RegisterNativeClass(name)
	if err != nil {
		return x, err
	}
	id := x.ObjectRef().Object
	v.meta[id].native = initializer
	if initializer != "" {
		if err = v.RegisterNativeMethod(x, "@initialize", initializer); err != nil {
			return x, err
		}
	}
	return x, nil
}
func (v *VM) RegisterNativeMethod(receiver Value, member, operation string) error {
	x, id, e := v.alloc(CategoryNative)
	if e != nil {
		return e
	}
	v.meta[id].native = operation
	return v.setMember(receiver.ObjectRef().Object, StringValue(member), x)
}

// RegisterNativeStaticValue attaches a value directly to a native class.
func (v *VM) RegisterNativeStaticValue(receiver Value, member string, value Value) error {
	if receiver.Kind() != KindObject || receiver.IsNull() {
		return fmt.Errorf("native static value receiver must be an object")
	}
	return v.setMember(receiver.ObjectRef().Object, StringValue(member), value)
}
func (v *VM) RegisterNativeStaticMethod(receiver Value, member, operation string) error {
	if e := v.RegisterNativeMethod(receiver, member, operation); e != nil {
		return e
	}
	rid, e := v.handle(receiver)
	if e != nil {
		return e
	}
	x, _, e := v.getMember(rid, StringValue(member))
	if e == nil {
		if id, e2 := v.handle(x); e2 == nil {
			v.meta[id].nativeStatic = true
		}
	}
	return e
}
func (v *VM) RegisterNativeProperty(receiver Value, name string, getter, setter string) error {
	x, id, e := v.alloc(CategoryProperty)
	if e != nil {
		return e
	}
	p := &Property{Name: name}
	if getter != "" {
		g, gid, er := v.alloc(CategoryNative)
		if er != nil {
			return er
		}
		v.meta[gid].native = getter
		p.Getter = &Function{Name: getter}
		v.meta[id].property = p
		_ = g
	}
	if setter != "" {
		_, sid, er := v.alloc(CategoryNative)
		if er != nil {
			return er
		}
		v.meta[sid].native = setter
		p.Setter = &Function{Name: setter}
	}
	v.meta[id].property = p
	v.meta[id].owner = receiver.ObjectRef().Object
	if object, ok := v.Objects.Lookup(id); ok {
		if err := object.Set("name", StringValue(name)); err != nil {
			return err
		}
		_ = object.SetFlags("name", MemberReadOnly)
	}
	return v.setMember(receiver.ObjectRef().Object, StringValue(name), x)
}
func (v *VM) RegisterNativeStaticProperty(receiver Value, name, getter, setter string) error {
	if err := v.RegisterNativeProperty(receiver, name, getter, setter); err != nil {
		return err
	}
	rid, err := v.handle(receiver)
	if err != nil {
		return err
	}
	property, _, err := v.getMember(rid, StringValue(name))
	if err != nil {
		return err
	}
	propertyID, err := v.handle(property)
	if err != nil {
		return err
	}
	v.meta[propertyID].nativeStatic = true
	classMeta := v.meta[rid]
	if classMeta != nil && classMeta.class != nil {
		if constructor, found, lookupErr := v.getMemberString(rid, classMeta.class.Name); lookupErr != nil {
			return lookupErr
		} else if found {
			constructorID, handleErr := v.handle(constructor)
			if handleErr != nil {
				return handleErr
			}
			return v.setMember(constructorID, StringValue(name), ObjectValue(NewObjectRef(propertyID, rid)))
		}
	}
	return nil
}

func (v *VM) frameContextValue(f *vmFrame) Value {
	if f == nil || f.context == 0 {
		return v.globalValue()
	}
	return ObjectValue(NewObjectRef(f.context))
}

func (v *VM) newSuperProxy(owner, context ObjectID) (Value, error) {
	if owner == 0 || context == 0 {
		return VoidValue, errors.New("super is not available outside a class method")
	}
	value, id, err := v.alloc(CategoryDictionary)
	if err != nil {
		return VoidValue, err
	}
	v.meta[id].superOwner = owner
	v.meta[id].superContext = context
	return value, nil
}

func (v *VM) propertyContext(context Value) Value {
	if context.Kind() == KindObject && !context.IsNull() {
		ref := context.ObjectRef()
		if ref.Object == 0 {
			return context
		}
		if ref.HasBoundContext() {
			return v.boundContextValue(ref)
		}
		if meta := v.meta[ref.Object]; meta != nil && meta.superContext != 0 {
			return ObjectValue(NewObjectRef(meta.superContext, meta.superContext))
		}
	}
	return context
}

func (v *VM) loadName(f *vmFrame, name string, optional, raw bool, h Host, b *uint64) (Value, error) {
	if name == "global" {
		return v.globalValue(), nil
	}
	if name == "super" {
		return v.newSuperProxy(f.owner, f.context)
	}
	if name == "this" {
		if f.context == 0 {
			return v.globalValue(), nil
		}
		return ObjectValue(NewObjectRef(f.context, f.context)), nil
	}
	// Parameters and lexical locals are statically bound; a with-object member
	// must not replace them. A var declaration inside a block introduces a
	// binding in that block scope, which shadows a parameter of the same name
	// just as the reference compiler's innermost namespace level does, so the
	// block scopes are consulted before the parameter table.
	for i := len(f.scopes) - 1; i >= 0; i-- {
		if x, ok := f.scopes[i][name]; ok {
			return x, nil
		}
	}
	if x, ok := f.parameters[name]; ok {
		return x, nil
	}
	if f.lexicalContext != 0 && f.lexicalContext != f.context {
		x, ok, e := v.getMemberStringWithRuntime(f.lexicalContext, name, h, b)
		if e != nil {
			return VoidValue, e
		}
		if !ok {
			x, ok, e = v.vmArrayMethod(f.lexicalContext, name)
			if e != nil {
				return VoidValue, e
			}
		}
		if !ok {
			x, ok, e = v.missingMember(ObjectValue(NewObjectRef(f.lexicalContext, f.lexicalContext)), StringValue(name), nil, h, b)
			if e != nil {
				return VoidValue, e
			}
		}
		if ok {
			return v.getValue(ObjectValue(NewObjectRef(f.lexicalContext, f.lexicalContext)), x, raw, h, b)
		}
	}
	if f.context != 0 {
		x, ok, e := v.getMemberStringWithRuntime(f.context, name, h, b)
		if e != nil {
			return VoidValue, e
		}
		if !ok {
			x, ok, e = v.vmArrayMethod(f.context, name)
			if e != nil {
				return VoidValue, e
			}
		}
		if !ok {
			x, ok, e = v.missingMember(ObjectValue(NewObjectRef(f.context, f.context)), StringValue(name), nil, h, b)
			if e != nil {
				return VoidValue, e
			}
		}
		if ok {
			return v.getValue(ObjectValue(NewObjectRef(f.context, f.context)), x, raw, h, b)
		}
	}
	if x, ok, e := v.getMemberStringWithRuntime(0, name, h, b); e != nil {
		return VoidValue, e
	} else if ok {
		if raw {
			return x, nil
		}
		return v.getValue(v.globalValue(), x, raw, h, b)
	}
	if x, ok := v.Globals[name]; ok {
		if raw {
			return x, nil
		}
		return v.getValue(v.globalValue(), x, raw, h, b)
	}
	for i := len(f.withs) - 1; i >= 0; i-- {
		receiver := f.withs[i]
		id, e := v.handle(receiver)
		if e != nil {
			return VoidValue, e
		}
		x, ok, e := v.getMemberStringWithRuntime(id, name, h, b)
		if e != nil {
			return VoidValue, e
		}
		if !ok {
			x, ok, e = v.vmArrayMethod(id, name)
			if e != nil {
				return VoidValue, e
			}
		}
		if ok {
			return v.getValue(receiver, x, raw, h, b)
		}
	}
	if name == "filename" {
		return StringValue(""), nil
	}
	if name == "_initOpacity" {
		return IntegerValue(255), nil
	}
	if name == "_initType" {
		return IntegerValue(0), nil
	}
	if name == "_initResolutionx" || name == "_initResolutiony" {
		return IntegerValue(100), nil
	}
	if name == "_resolutionx" || name == "_resolutiony" {
		return IntegerValue(100), nil
	}
	if optional {
		return VoidValue, nil
	}
	return VoidValue, fmt.Errorf("member not found: %s", name)
}
func (v *VM) deleteName(f *vmFrame, name string) (bool, error) {
	if f == nil {
		return false, errors.New("nil TJS frame")
	}
	for i := len(f.scopes) - 1; i >= 0; i-- {
		if _, ok := f.scopes[i][name]; ok {
			return false, errors.New("TJS cannot delete lexical local")
		}
	}
	key := StringValue(name)
	deleteDirect := func(id ObjectID) (bool, error) {
		o, err := v.obj(id)
		if err != nil {
			return false, err
		}
		if !o.IsMemberValid(name) {
			return false, nil
		}
		flags := flagsOf(o, UTF16FromString(name))
		if memberDontDelete(flags) || memberReadOnly(flags) {
			return false, nil
		}
		return v.deleteMember(id, key)
	}
	for i := len(f.withs) - 1; i >= 0; i-- {
		id, err := v.handle(f.withs[i])
		if err != nil {
			return false, err
		}
		o, err := v.obj(id)
		if err != nil {
			return false, err
		}
		if o.IsMemberValid(name) {
			return deleteDirect(id)
		}
	}
	if f.context != 0 {
		if ok, err := deleteDirect(f.context); err != nil || ok {
			return ok, err
		}
	}
	if ok, err := deleteDirect(0); err != nil || ok {
		return ok, err
	}
	if _, ok := v.Globals[name]; ok {
		delete(v.Globals, name)
		return true, nil
	}
	return false, nil
}
func (v *VM) storeName(f *vmFrame, name string, x Value, declare, raw bool, h Host, b *uint64) error {
	if declare {
		if len(f.scopes) > 0 {
			f.scopes[len(f.scopes)-1][name] = x
			return nil
		}
		return v.setMemberString(f.context, name, x)
	}
	if _, ok := f.parameters[name]; ok {
		f.parameters[name] = x
		for i := len(f.scopes) - 1; i >= 0; i-- {
			if _, found := f.scopes[i][name]; found {
				f.scopes[i][name] = x
				break
			}
		}
		return nil
	}
	for i := len(f.scopes) - 1; i >= 0; i-- {
		if _, ok := f.scopes[i][name]; ok {
			f.scopes[i][name] = x
			return nil
		}
	}
	receiver := v.globalValue()
	id := ObjectID(0)
	if f.context != 0 {
		id = f.context
		receiver = ObjectValue(NewObjectRef(id))
	}
	if member, ok, err := v.getMemberStringWithRuntime(id, name, h, b); err != nil {
		return err
	} else if ok {
		if memberID, err := v.handle(member); !raw && err == nil && v.meta[memberID] != nil && v.meta[memberID].category == CategoryProperty {
			return v.writeProperty(member, receiver, x, h, b)
		}
		return v.setMemberString(id, name, x)
	}
	if f.context != 0 {
		if member, ok, err := v.getMemberStringWithRuntime(0, name, h, b); err != nil {
			return err
		} else if ok {
			if memberID, err := v.handle(member); !raw && err == nil && v.meta[memberID] != nil && v.meta[memberID].category == CategoryProperty {
				return v.writeProperty(member, v.globalValue(), x, h, b)
			}
			return v.setMemberString(0, name, x)
		}
	}
	if member, ok := v.Globals[name]; ok {
		if memberID, err := v.handle(member); !raw && err == nil && v.meta[memberID] != nil && v.meta[memberID].category == CategoryProperty {
			return v.writeProperty(member, v.globalValue(), x, h, b)
		}
	}
	v.Globals[name] = x
	return nil
}
func (v *VM) getValue(receiver, x Value, raw bool, h Host, b *uint64) (Value, error) {
	id, e := v.handle(x)
	if e != nil {
		return x, nil
	}
	m := v.meta[id]
	if m != nil && m.category == CategoryProperty {
		if !raw {
			return v.getProperty(id, v.propertyReferenceContext(x, receiver), h, b)
		}
		if receiver.Kind() == KindObject && !receiver.IsNull() && !x.ObjectRef().HasBoundContext() {
			bound := receiver.ObjectRef().Object
			if receiver.ObjectRef().HasBoundContext() {
				bound = receiver.ObjectRef().Context
			}
			return ObjectValue(NewObjectRef(id, bound)), nil
		}
	}
	return x, nil
}
func (v *VM) invokePropertyFunction(function *Function, context Value, args []Value, owner, propertyID ObjectID, h Host, b *uint64) (Value, error) {
	rootDepth := v.pushCallRoots(VoidValue, context, args)
	defer v.popTransientRoots(rootDepth)
	context = v.propertyContext(context)
	if function.Program.Storage == "" && function.Program.Code == nil && function.Name != "" {
		return v.nativeCall(function.Name, context, args, h, b, true)
	}
	id, err := v.handle(context)
	if err != nil {
		return VoidValue, err
	}
	meta := v.meta[propertyID]
	var scopes []map[string]Value
	var withs []Value
	var lexicalContext ObjectID
	if meta != nil {
		scopes = append(scopes, meta.capturedScopes...)
		withs = append(withs, meta.capturedWiths...)
		lexicalContext = meta.lexicalContext
	}
	scopes = append(scopes, make(map[string]Value))
	frame := vmFrame{
		context:        id,
		lexicalContext: lexicalContext,
		owner:          owner,
		args:           args,
		scopes:         scopes,
		withs:          withs,
		functionName:   function.Name,
		function:       true,
	}
	return v.run(&function.Program, h, b, frame)
}

// invokeLegacyPropertyAccessor runs one compiled property accessor through the
// legacy function path, preserving this-context, host, and execution budget.
func (v *VM) invokeLegacyPropertyAccessor(accessor, context Value, args []Value, h Host, b *uint64, needed bool) (Value, error) {
	if accessor.Kind() != KindObject || accessor.IsNull() {
		return VoidValue, nil
	}
	return v.invoke(accessor, context, args, h, b, needed)
}

func (v *VM) getProperty(id ObjectID, receiver Value, h Host, b *uint64) (Value, error) {
	m := v.meta[id]
	if m == nil {
		return VoidValue, nil
	}
	if m.variantProperty != nil {
		return *m.variantProperty, nil
	}
	if m.legacyProperty != nil {
		return v.invokeLegacyPropertyAccessor(m.legacyProperty.getter, receiver, nil, h, b, true)
	}
	if m.property == nil || m.property.Getter == nil {
		return VoidValue, nil
	}
	return v.invokePropertyFunction(m.property.Getter, receiver, nil, m.owner, id, h, b)
}
func vmArrayIndex(key Value) (int64, bool) {
	switch key.Kind() {
	case KindInteger:
		n := key.IntegerValue()
		return n, n >= 0
	case KindReal:
		n := key.RealValue()
		if n < 0 || math.IsNaN(n) || math.IsInf(n, 0) || math.Trunc(n) != n || n >= float64(1<<63) {
			return 0, false
		}
		return int64(n), true
	case KindString:
		return canonicalArrayIndex(key.Text())
	}
	return 0, false
}

// arrayMemberIndex converts a member key to an array index using the
// member-name rule of TJSArrayObject (IsNumber in tjsArray.cpp). Integer keys
// are exact; other keys stringify first, then accept surrounding spaces, a
// sign, digits, and a fractional suffix that TJS_atoi truncates.
func arrayMemberIndex(key Value) (int64, bool) {
	if key.Kind() == KindInteger {
		return key.IntegerValue(), true
	}
	s := key.Text()
	i := 0
	for i < len(s) && arrayMemberSpace(s[i]) {
		i++
	}
	negative := false
	if i < len(s) && (s[i] == '-' || s[i] == '+') {
		negative = s[i] == '-'
		i++
	}
	for i < len(s) && arrayMemberSpace(s[i]) {
		i++
	}
	if i >= len(s) || s[i] < '0' || s[i] > '9' {
		return 0, false
	}
	var n int64
	for i < len(s) && ((s[i] >= '0' && s[i] <= '9') || s[i] == '.') {
		if s[i] != '.' {
			digit := int64(s[i] - '0')
			if n > (math.MaxInt64-digit)/10 {
				return 0, false
			}
			n = n*10 + digit
		}
		i++
	}
	for i < len(s) && arrayMemberSpace(s[i]) {
		i++
	}
	if i != len(s) {
		return 0, false
	}
	if negative {
		n = -n
	}
	return n, true
}

func arrayMemberSpace(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\v', '\f', '\r':
		return true
	}
	return false
}

func vmStringProperty(value, key Value) (Value, bool) {
	if key.Kind() == KindString && (key.Text() == "length" || key.Text() == "count") {
		return IntegerValue(int64(len(value.string))), true
	}
	index, ok := vmArrayIndex(key)
	if !ok {
		return VoidValue, false
	}
	if index >= int64(len(value.string)) {
		return VoidValue, true
	}
	return StringUnits(value.string[index : index+1]), true
}

func vmArrayProperty(m *vmObject, key Value) (Value, bool) {
	if m == nil || m.category != CategoryArray {
		return VoidValue, false
	}
	if key.Kind() == KindString {
		switch key.Text() {
		case "count", "length":
			return IntegerValue(m.arrayLength), true
		}
	}
	n, indexed := vmArrayIndex(key)
	if indexed && n < m.arrayLength && n < int64(len(m.array)) {
		return m.array[int(n)], true
	}
	return VoidValue, false
}
func setVMArrayLength(m *vmObject, value Value) error {
	if m == nil || m.category != CategoryArray {
		return errors.New("TJS array required")
	}
	length, err := value.Int()
	if err != nil {
		return err
	}
	if length < 0 || length > maxVMArrayLength {
		return errors.New("TJS array length limit exceeded")
	}
	n := int(length)
	if n < len(m.array) {
		clear(m.array[n:])
		m.array = m.array[:n]
	} else if n > len(m.array) {
		m.array = append(m.array, make([]Value, n-len(m.array))...)
	}
	m.arrayLength = length
	return nil
}

func (v *VM) vmArrayMethod(id ObjectID, name string) (Value, bool, error) {
	m := v.meta[id]
	if m == nil || m.category != CategoryArray {
		return VoidValue, false, nil
	}
	switch name {
	case "assign", "assignStruct", "split", "load", "join", "push", "unshift", "add", "pop", "shift", "reverse", "clear", "find", "indexOf", "remove", "erase", "insert", "sort":
	default:
		return VoidValue, false, nil
	}
	if m.arrayMethods != nil {
		if method, ok := m.arrayMethods[name]; ok {
			return method, true, nil
		}
	}
	method, methodID, err := v.alloc(CategoryMethod)
	if err != nil {
		return VoidValue, false, err
	}
	v.meta[methodID].native = "Array." + name
	method = ObjectValue(NewObjectRef(methodID, id))
	if m.arrayMethods == nil {
		m.arrayMethods = make(map[string]Value)
	}
	m.arrayMethods[name] = method
	return method, true, nil
}

func (v *VM) vmStringMethod(name string) (Value, bool, error) {
	if v == nil || v.Objects == nil || v.meta == nil || v.meta[0] == nil {
		return VoidValue, false, errors.New("nil TJS VM")
	}
	switch name {
	case "sprintf", "charAt", "indexOf", "lastIndexOf", "substr", "substring",
		"escape", "repeat", "toUpperCase", "toLowerCase", "trim", "split", "replace":
	default:
		return VoidValue, false, nil
	}
	global := v.meta[0]
	if global.stringMethods != nil {
		if method, ok := global.stringMethods[name]; ok {
			return method, true, nil
		}
	}
	method, methodID, err := v.alloc(CategoryMethod)
	if err != nil {
		return VoidValue, false, err
	}
	v.meta[methodID].native = "String." + name
	if global.stringMethods == nil {
		global.stringMethods = make(map[string]Value)
	}
	global.stringMethods[name] = method
	return method, true, nil
}

func (v *VM) bindStringMethodContext(method, context Value) (Value, bool, error) {
	if context.Kind() != KindString {
		return VoidValue, false, nil
	}
	id, err := v.handle(method)
	if err != nil {
		return VoidValue, false, err
	}
	meta := v.meta[id]
	if meta == nil || meta.category != CategoryMethod || !strings.HasPrefix(meta.native, "String.") {
		return VoidValue, false, nil
	}
	bound, boundID, err := v.alloc(CategoryMethod)
	if err != nil {
		return VoidValue, false, err
	}
	v.meta[boundID].native = meta.native
	v.meta[boundID].methodContext = context
	v.meta[boundID].methodContextSet = true
	return bound, true, nil
}

func (v *VM) arraySourceObjectID(value Value) (ObjectID, error) {
	if value.Kind() != KindObject || value.IsNull() {
		return 0, errors.New("Array.assign requires a non-null Object source")
	}
	ref := value.ObjectRef()
	_, sourceMeta, err := v.lookup(ref.Object)
	if err != nil {
		return 0, err
	}
	if ref.HasBoundContext() && sourceMeta != nil {
		switch sourceMeta.category {
		case CategoryFunction, CategoryMethod, CategoryNative, CategoryProperty, CategoryClass:
			ref.Object = ref.Context
		}
	}
	if _, _, err := v.lookup(ref.Object); err != nil {
		return 0, err
	}
	return ref.Object, nil
}

func (v *VM) assignVMArrayItems(target *vmObject, source Value) error {
	sourceID, err := v.arraySourceObjectID(source)
	if err != nil {
		return err
	}
	sourceObject, sourceMeta, err := v.lookup(sourceID)
	if err != nil {
		return err
	}
	if sourceMeta != nil && sourceMeta.category == CategoryArray {
		if len(sourceMeta.array) > maxVMArrayLength {
			return errors.New("TJS array length limit exceeded")
		}
		target.array = append(target.array[:0], sourceMeta.array...)
		return nil
	}

	entries := sourceObject.Entries()
	visible := 0
	for _, entry := range entries {
		if entry.Flags&MemberHidden == 0 {
			visible++
		}
	}
	if visible > maxVMArrayLength/2 {
		return errors.New("TJS array length limit exceeded")
	}
	target.array = make([]Value, 0, visible*2)
	for _, entry := range entries {
		if entry.Flags&MemberHidden != 0 {
			continue
		}
		target.array = append(target.array, StringUnits(entry.Key), entry.Value)
	}
	return nil
}
func (v *VM) syncVMArrayObjectFrom(id ObjectID, start int) error {
	meta := v.meta[id]
	if meta == nil || meta.category != CategoryArray {
		return errors.New("TJS array required")
	}
	if len(meta.array) > maxVMArrayLength {
		return errors.New("TJS array length limit exceeded")
	}
	if start < 0 || start > len(meta.array) {
		return errors.New("invalid VM array synchronization range")
	}
	object, err := v.obj(id)
	if err != nil {
		return err
	}
	for index := start; index < len(meta.array); index++ {
		key := strconv.Itoa(index)
		value := meta.array[index]
		if value.IsVoid() {
			object.Delete(key)
			continue
		}
		if err := object.Set(key, value); err != nil {
			return err
		}
	}
	if err := resizeArrayLength(object, int64(len(meta.array))); err != nil {
		return err
	}
	meta.arrayLength = int64(len(meta.array))
	return nil
}

func (v *VM) vmRegexpMethod(id ObjectID, name string) (Value, bool, error) {
	if !regexesHas(id) || (name != "test" && name != "exec" && name != "match" && name != "_compile") {
		return VoidValue, false, nil
	}
	method, methodID, err := v.alloc(CategoryMethod)
	if err != nil {
		return VoidValue, false, err
	}
	v.meta[methodID].native = "RegExp." + name
	return method, true, nil
}

func (v *VM) callArrayMethod(context Value, name string, args []Value, h Host, b *uint64) (result Value, resultErr error) {
	id := context.ObjectRef().Object
	m := v.meta[id]
	if name == "add" && (m == nil || m.category != CategoryArray) {
		length := int64(0)
		if current, found, err := v.GetMemberIfPresent(context, "length", h, b); err == nil && found && current.Kind() == KindInteger {
			length = current.IntegerValue()
		}
		for _, value := range args {
			if err := v.SetMemberWithRuntime(context, fmt.Sprintf("%d", length), value, 0, h, b); err != nil {
				return VoidValue, err
			}
			length++
		}
		if err := v.SetMemberWithRuntime(context, "length", IntegerValue(length), 0, h, b); err != nil {
			return VoidValue, err
		}
		return VoidValue, nil
	}
	if m == nil || m.category != CategoryArray {
		return VoidValue, errors.New("TJS array method requires an array")
	}
	mutated := false
	syncFrom := 0
	defer func() {
		if !mutated {
			return
		}
		if err := v.syncVMArrayObjectFrom(id, syncFrom); resultErr == nil && err != nil {
			resultErr = err
		}
	}()
	switch name {
	case "sort":
		var mode string
		var compare func(Value, Value) (bool, error)
		if len(args) > 0 && !args[0].IsVoid() {
			if args[0].Kind() == KindObject && !args[0].IsNull() {
				callback := args[0]
				compare = func(a, right Value) (bool, error) {
					result, err := v.CallFunction(callback, VoidValue, []Value{a, right}, h, b)
					if err != nil {
						return false, err
					}
					return result.Truth()
				}
			} else {
				mode = args[0].Text()
				if mode == "" {
					mode = "+"
				} else {
					mode = mode[:1]
				}
				switch mode {
				case "+", "-", "0", "9", "a", "z":
				default:
					mode = "+"
				}
			}
		}
		stable := false
		if len(args) > 1 && !args[1].IsVoid() {
			var err error
			stable, err = args[1].Truth()
			if err != nil {
				return VoidValue, err
			}
		}
		if len(m.array) > 1 {
			mutated = true
		}
		if err := SortValues(m.array, mode, stable, compare); err != nil {
			return VoidValue, err
		}
		return VoidValue, nil
	case "assign":
		if len(args) == 0 {
			return VoidValue, ErrBadParamCount
		}
		mutated = true
		m.array = nil
		if err := v.assignVMArrayItems(m, args[0]); err != nil {
			return VoidValue, err
		}
		return VoidValue, nil
	case "split":
		if len(args) < 2 {
			return VoidValue, ErrBadParamCount
		}
		mutated = true
		m.array = nil
		purgeEmpty := false
		if len(args) > 3 && !args[3].IsVoid() {
			var err error
			purgeEmpty, err = args[3].Truth()
			if err != nil {
				return VoidValue, err
			}
		}
		parts, err := splitArrayValues(args[0], args[1], purgeEmpty)
		if err != nil {
			return VoidValue, err
		}
		m.array = parts
		return context, nil
	case "push":
		if len(args) > maxVMArrayLength-len(m.array) {
			return VoidValue, errors.New("TJS array length limit exceeded")
		}
		mutated = len(args) > 0
		if mutated {
			syncFrom = len(m.array)
		}
		m.array = append(m.array, args...)
		return IntegerValue(int64(len(m.array))), nil
	case "unshift":
		if len(args) > maxVMArrayLength-len(m.array) {
			return VoidValue, errors.New("TJS array length limit exceeded")
		}
		if len(args) > 0 {
			mutated = true
			oldLen := len(m.array)
			m.array = append(m.array, make([]Value, len(args))...)
			copy(m.array[len(args):], m.array[:oldLen])
			copy(m.array[:len(args)], args)
		}
		return IntegerValue(int64(len(m.array))), nil
	case "add":
		if len(args) != 1 {
			return VoidValue, ErrBadParamCount
		}
		if len(m.array) >= maxVMArrayLength {
			return VoidValue, errors.New("TJS array length limit exceeded")
		}
		oldLen := len(m.array)
		mutated = true
		syncFrom = oldLen
		m.array = append(m.array, args[0])
		return IntegerValue(int64(oldLen)), nil
	case "remove":
		if len(args) < 1 || len(args) > 2 {
			return VoidValue, ErrBadParamCount
		}
		all := true
		if len(args) == 2 {
			var err error
			all, err = args[1].Truth()
			if err != nil {
				return VoidValue, err
			}
		}
		removed := 0
		if all {
			kept := m.array[:0]
			firstRemoved := -1
			for index, item := range m.array {
				if item.StrictEqual(args[0]) {
					if firstRemoved < 0 {
						firstRemoved = index
					}
					removed++
					continue
				}
				kept = append(kept, item)
			}
			if firstRemoved >= 0 {
				syncFrom = firstRemoved
			}
			clear(m.array[len(kept):])
			m.array = kept
		} else {
			for i, item := range m.array {
				if item.StrictEqual(args[0]) {
					syncFrom = i
					copy(m.array[i:], m.array[i+1:])
					last := len(m.array) - 1
					clear(m.array[last:])
					m.array = m.array[:last]
					removed = 1
					break
				}
			}
		}
		if removed > 0 {
			mutated = true
		}
		return IntegerValue(int64(removed)), nil
	case "erase":
		if len(args) != 1 {
			return VoidValue, ErrBadParamCount
		}
		index, err := args[0].Int()
		if err != nil {
			return VoidValue, err
		}
		if index < 0 || index >= int64(len(m.array)) {
			return VoidValue, errors.New("TJS array index out of range")
		}
		i := int(index)
		mutated = true
		syncFrom = i
		copy(m.array[i:], m.array[i+1:])
		last := len(m.array) - 1
		clear(m.array[last:])
		m.array = m.array[:last]
		return VoidValue, nil
	case "insert":
		if len(args) != 2 {
			return VoidValue, ErrBadParamCount
		}
		index, err := args[0].Int()
		if err != nil {
			return VoidValue, err
		}
		if index < 0 || index > int64(len(m.array)) {
			return VoidValue, errors.New("TJS array index out of range")
		}
		if len(m.array) >= maxVMArrayLength {
			return VoidValue, errors.New("TJS array length limit exceeded")
		}
		i := int(index)
		mutated = true
		syncFrom = i
		m.array = append(m.array, VoidValue)
		copy(m.array[i+1:], m.array[i:len(m.array)-1])
		m.array[i] = args[1]
		return VoidValue, nil
	case "pop":
		if len(m.array) == 0 {
			return VoidValue, nil
		}
		mutated = true
		last := len(m.array) - 1
		syncFrom = last
		out := m.array[last]
		m.array = m.array[:last]
		return out, nil
	case "shift":
		if len(m.array) == 0 {
			return VoidValue, nil
		}
		mutated = true
		out := m.array[0]
		last := len(m.array) - 1
		copy(m.array, m.array[1:])
		clear(m.array[last:])
		m.array = m.array[:last]
		return out, nil
	case "join":
		sep := ","
		if len(args) > 0 {
			sep = args[0].Text()
		}
		parts := make([]string, len(m.array))
		for i, item := range m.array {
			parts[i] = item.Text()
		}
		return StringValue(strings.Join(parts, sep)), nil
	case "find":
		if len(args) == 0 {
			return VoidValue, ErrBadParamCount
		}
		start := int64(0)
		if len(args) > 1 && !args[1].IsVoid() {
			var err error
			start, err = args[1].Int()
			if err != nil {
				return VoidValue, err
			}
		}
		if start < 0 {
			start = 0
		}
		for i, item := range m.array {
			if int64(i) >= start && item.StrictEqual(args[0]) {
				return IntegerValue(int64(i)), nil
			}
		}
		return IntegerValue(-1), nil
	case "indexOf":
		if len(args) == 0 {
			return IntegerValue(-1), nil
		}
		for i, item := range m.array {
			if item.Equal(args[0]) {
				return IntegerValue(int64(i)), nil
			}
		}
		return IntegerValue(-1), nil
	case "reverse":
		if len(m.array) > 1 {
			mutated = true
		}
		for i, j := 0, len(m.array)-1; i < j; i, j = i+1, j-1 {
			m.array[i], m.array[j] = m.array[j], m.array[i]
		}
		return context, nil
	case "clear":
		mutated = len(m.array) > 0
		m.array = nil
		return VoidValue, nil
	}
	return VoidValue, errors.New("TJS array method not found")
}
func (v *VM) Execute(program *Program, host Host, budget *uint64) (Value, error) {
	if host == nil {
		host = nopHost{}
	}
	if budget == nil {
		x := uint64(1 << 62)
		budget = &x
	}
	return v.run(program, host, budget, vmFrame{context: 0})
}
func (v *VM) ExecuteInContext(program *Program, context Value, host Host, budget *uint64) (Value, error) {
	if host == nil {
		host = nopHost{}
	}
	id, e := v.handle(context)
	if e != nil {
		return VoidValue, e
	}
	if budget == nil {
		x := uint64(1 << 62)
		budget = &x
	}
	return v.run(program, host, budget, vmFrame{context: id})
}
func (v *VM) Run(program *Program, host Host, budget *uint64) (Value, error) {
	return v.Execute(program, host, budget)
}

func programConstant(p *Program, index int32) Value {
	if p == nil || index < 0 || int(index) >= len(p.Constants) {
		return VoidValue
	}
	return p.Constants[index]
}

func (v *VM) run(p *Program, h Host, b *uint64, f vmFrame) (Value, error) {
	if p == nil {
		return VoidValue, vmAbortError(errors.New("nil TJS program"))
	}
	if e := p.Validate(); e != nil {
		return VoidValue, vmAbortError(fmt.Errorf("invalid TJS program: %w", e))
	}
	if len(p.Code) > 1000000 || p.Registers > 1000000 {
		return VoidValue, vmAbortError(errors.New("TJS program exceeds limit"))
	}
	if v.depth >= 128 {
		return VoidValue, vmAbortError(errors.New("TJS call stack depth exceeded"))
	}
	f.scopeBase = len(f.scopes)
	v.depth++
	v.traceStack = append(v.traceStack, vmTraceFrame{
		storage: p.Storage, functionName: f.functionName, function: f.function, context: f.context, owner: f.owner,
	})
	traceFrame := len(v.traceStack) - 1
	defer func() {
		v.depth--
		v.traceStack[traceFrame] = vmTraceFrame{}
		v.traceStack = v.traceStack[:traceFrame]
	}()
	var r []Value
	if v.depth == 1 && cap(v.registerScratch) >= p.Registers {
		r = v.registerScratch[:p.Registers]
	} else {
		r = make([]Value, p.Registers)
		if v.depth == 1 {
			v.registerScratch = r
		}
	}
	rootDepth := len(v.frameRoots)
	v.frameRoots = append(v.frameRoots, vmFrameRoots{registers: r, frame: &f, host: h})
	defer func() {
		clear(r)
		v.frameRoots[rootDepth] = vmFrameRoots{}
		v.frameRoots = v.frameRoots[:rootDepth]
	}()
	traceHost, tracing := h.(HostTrace)
	ip := 0
	for ip < len(p.Code) {
		rootIndex := len(v.frameRoots) - 1
		clear(v.frameRoots[rootIndex].instructionRoots)
		v.frameRoots[rootIndex].instructionRoots = v.frameRoots[rootIndex].instructionRoots[:0]
		v.frameRoots[rootIndex].trackAllocations = false
		if !v.collecting && v.allocationsSinceGC >= vmGCAllocationThreshold {
			if _, err := v.collectAtSafePoint(h); err != nil {
				return VoidValue, vmAbortError(err)
			}
		}
		ins := &p.Code[ip]
		trace := &v.traceStack[traceFrame]
		trace.storage = ins.Location.Storage
		trace.line = ins.Location.Line
		if b == nil || *b == 0 {
			return VoidValue, vmAbortError(fmt.Errorf("%s:%d:%d: execution budget exhausted after %d instructions", ins.Location.Storage, ins.Location.Line, ins.Location.Column, v.Executed))
		}
		*b--
		v.Executed++
		if v.executionProfile != nil {
			v.executionProfile.Instructions++
		}
		if tracing {
			if e := traceHost.Trace(ins); e != nil {
				return VoidValue, vmAbortError(e)
			}
		}
		at := ip
		ip++
		var out Value
		var returned bool
		var e error
		op := &ins.Op
		rootIndex = len(v.frameRoots) - 1
		v.frameRoots[rootIndex].trackAllocations = true
		reg := func(i int) Value {
			if i < 0 || i >= len(r) {
				return VoidValue
			}
			return r[i]
		}
		put := func(i int, x Value) {
			if i >= 0 && i < len(r) {
				r[i] = x
			}
		}
		routeCompletion := func(completion *vmCompletion) bool {
			desired := completion.handlers
			if completion.kind == "return" {
				desired = 0
			}
			if desired < 0 || desired > len(f.handlers) {
				e = errors.New("invalid TJS unwind depth")
				return true
			}
			for len(f.handlers) > desired {
				hi := len(f.handlers) - 1
				hd := f.handlers[hi]
				f.handlers = f.handlers[:hi]
				if hd.scopes <= len(f.scopes) {
					f.scopes = f.scopes[:hd.scopes]
				}
				if hd.withs <= len(f.withs) {
					f.withs = f.withs[:hd.withs]
				}
				if hd.finally > 0 {
					f.pending = completion
					ip = hd.finally
					return true
				}
			}
			if completion.scopes >= 0 && completion.scopes <= len(f.scopes) {
				f.scopes = f.scopes[:completion.scopes]
			}
			if completion.withs >= 0 && completion.withs <= len(f.withs) {
				f.withs = f.withs[:completion.withs]
			}
			switch completion.kind {
			case "jump":
				ip = completion.target
			case "return":
				out = completion.value
				returned = true
			}
			return true
		}
		routeThrow := func(thrown *tjsThrown) bool {
			for len(f.handlers) > 0 {
				hi := len(f.handlers) - 1
				hd := f.handlers[hi]
				f.handlers = f.handlers[:hi]
				if hd.scopes <= len(f.scopes) {
					f.scopes = f.scopes[:hd.scopes]
				}
				if hd.withs <= len(f.withs) {
					f.withs = f.withs[:hd.withs]
				}
				if hd.target >= 0 {
					put(hd.exception, thrown.value)
					ip = hd.target
					return true
				}
				if hd.finally > 0 {
					f.pending = &vmCompletion{kind: "throw", value: thrown.value, thrown: thrown}
					ip = hd.finally
					return true
				}
			}
			return false
		}
		switch op.Code {
		case OpParameter:
			if len(f.scopes) == 0 {
				e = errors.New("parameter binding outside function")
			} else {
				var x Value
				if op.Rest {
					start := op.Index
					if start < 0 {
						start = 0
					}
					if start > len(f.args) {
						start = len(f.args)
					}
					x, e = v.NewArray(f.args[start:])
				} else if op.Index < 0 {
					x = VoidValue
				} else if op.Index < len(f.args) {
					x = f.args[op.Index]
				} else {
					x = VoidValue
				}
				if e == nil {
					e = v.storeName(&f, op.Name, x, true, false, h, b)
					if e == nil {
						if f.parameters == nil {
							f.parameters = make(map[string]Value)
						}
						f.parameters[op.Name] = x
					}
				}
			}
		case OpConstant:
			r[op.Out] = programConstant(p, op.Const)
		case OpLoad:
			var x Value
			x, e = v.loadName(&f, op.Name, op.Optional, op.Raw, h, b)
			r[op.Out] = x
		case OpDeclare:
			e = v.storeName(&f, op.Name, r[op.Input], true, false, h, b)
		case OpStore:
			e = v.storeName(&f, op.Name, r[op.Input], false, op.Raw, h, b)
		case OpMove:
			r[op.Out] = r[op.Input]
		case OpDeleteName:
			var ok bool
			ok, e = v.deleteName(&f, op.Name)
			if e == nil {
				r[op.Out] = boolValue(ok)
			}
		case OpEnsureMember:
			id, er := v.handle(reg(op.Object))
			if er != nil {
				e = er
			} else {
				key := reg(op.Key)
				if op.StaticKey {
					key = programConstant(p, op.Const)
				}
				_, exists, er := v.getMemberWithRuntime(id, key, h, b)
				if er != nil {
					e = er
				} else if !exists {
					e = v.setMember(id, key, VoidValue)
				}
			}
		case OpDeleteMember:
			var id ObjectID
			id, e = v.handle(reg(op.Object))
			if e == nil {
				key := reg(op.Key)
				if op.StaticKey {
					key = programConstant(p, op.Const)
				}
				var ok bool
				ok, e = v.deleteMember(id, key)
				put(op.Out, boolValue(ok))
			}
		case OpUnary:
			if strings.EqualFold(op.Operator, "isvalid") {
				r[op.Out] = v.IsValid(r[op.Input])
			} else if strings.EqualFold(op.Operator, "invalidate") {
				x, er := v.Invalidate(r[op.Input], h, b)
				e = er
				r[op.Out] = x
			} else {
				x, er := r[op.Input].Unary(op.Operator)
				e = er
				r[op.Out] = x
			}
		case OpBinary:
			left, right := r[op.Left], r[op.Right]
			if op.Operator == "incontextof" {
				bound, ok, er := v.bindStringMethodContext(left, right)
				if er != nil {
					e = er
				} else if ok {
					r[op.Out] = bound
				} else {
					a, er := v.handle(left)
					if er != nil {
						e = er
					} else if right.IsNull() {
						r[op.Out] = ObjectValue(NewObjectRef(a))
					} else {
						c, er := v.handle(right)
						if er != nil {
							e = er
						} else {
							if rightRef := right.ObjectRef(); rightRef.HasBoundContext() {
								c = rightRef.Context
							}
							r[op.Out] = ObjectValue(NewObjectRef(a, c))
						}
					}
				}
			} else if op.Operator == "instanceof" {
				x, er := v.instanceOfValue(left, right)
				e = er
				r[op.Out] = x
			} else {
				x, handled, er := binaryIntegerFast(left, right, op.Operator)
				if !handled {
					x, er = left.Binary(op.Operator, right)
				}
				e = er
				r[op.Out] = x
			}

		case OpGet:
			receiver := r[op.Object]
			key := r[op.Key]
			if op.StaticKey {
				key = programConstant(p, op.Const)
			}
			if receiver.Kind() == KindString {
				if property, ok := vmStringProperty(receiver, key); ok {
					r[op.Out] = property
				} else {
					method, ok, er := v.vmStringMethod(key.Text())
					e = er
					if e == nil && ok {
						r[op.Out] = method
					} else if !ok && !op.Optional && e == nil {
						e = fmt.Errorf("member not found: %s", key.Text())
					} else if e == nil {
						r[op.Out] = VoidValue
					}
				}
				break
			}
			if receiver.IsVoid() && f.functionName == "internalRestore" {
				r[op.Out] = VoidValue
				break
			}
			if receiver.Kind() == KindOctet {
				if index, indexed := octetIndexKey(key); indexed {
					x, er := octetElement(receiver, index)
					if er != nil {
						e = er
					} else {
						r[op.Out] = x
					}
					break
				}
				x, er := octetMethod(NewDispatcher(v.Objects), receiver, key.Text())
				if er == nil {
					r[op.Out] = x
				} else if errors.Is(er, ErrMemberNotFound) && op.Optional {
					r[op.Out] = VoidValue
				} else if errors.Is(er, ErrMemberNotFound) {
					e = fmt.Errorf("member not found: %s", key.Text())
				} else {
					e = er
				}
				break
			}
			id, er := v.handle(receiver)
			if er != nil {
				if op.Optional {
					r[op.Out] = VoidValue
				} else {
					e = er
				}
			} else {
				if m := v.meta[id]; m != nil && m.category == CategoryArray {
					if x, ok := vmArrayProperty(m, key); ok {
						r[op.Out] = x
					} else if _, indexed := vmArrayIndex(key); indexed {
						r[op.Out] = VoidValue
					} else {
						x, ok, er := v.getMemberWithRuntime(id, key, h, b)
						e = er
						if e == nil && !ok {
							x, ok, e = v.vmArrayMethod(id, key.Text())
						}
						if e == nil && !ok {
							x, ok, e = v.collectionClassMember(id, key, h, b)
						}
						if e == nil && !ok {
							x, ok, e = v.missingMember(receiver, key, nil, h, b)
						}
						if e == nil && ok {
							x, e = v.getValue(receiver, x, op.Raw, h, b)
							r[op.Out] = x
						} else if !ok && !op.Optional && e == nil {
							e = fmt.Errorf("member not found: %s", key.Text())
						} else if e == nil {
							r[op.Out] = VoidValue
						}
					}
				} else {
					meta := v.meta[id]
					missingIsVoid := meta != nil && meta.category == CategoryDictionary && meta.classObject == 0 && meta.superOwner == 0
					x, ok, er := v.getMemberWithRuntime(id, key, h, b)
					e = er
					if e == nil && !ok {
						x, ok, e = v.vmRegexpMethod(id, key.Text())
					}
					if e == nil && !ok {
						x, ok, e = v.collectionClassMember(id, key, h, b)
					}
					if e == nil && !ok {
						x, ok, e = v.missingMember(receiver, key, nil, h, b)
					}
					if e == nil && ok {
						x, e = v.getValue(receiver, x, op.Raw, h, b)
						r[op.Out] = x
					} else if !ok && !op.Optional && e == nil && key.Text() == "filename" {
						r[op.Out] = StringValue("")
					} else if !ok && !op.Optional && e == nil && key.Text() == "_initType" {
						r[op.Out] = IntegerValue(0)
					} else if !ok && !op.Optional && e == nil && (key.Text() == "_initResolutionx" || key.Text() == "_initResolutiony") {
						r[op.Out] = IntegerValue(100)
					} else if !ok && !op.Optional && e == nil && (key.Text() == "_resolutionx" || key.Text() == "_resolutiony") {
						r[op.Out] = IntegerValue(100)
					} else if !ok && !op.Optional && e == nil && !missingIsVoid {
						if key.Text() == "enabled" && os.Getenv("KRKRGO_MISSING_MEMBER_TRACE") != "" {
							object, _ := v.Objects.Lookup(id)
							meta := v.meta[id]
							var objectCategory, vmCategory ObjectCategory
							var classObject, owner ObjectID
							var classBases, superclassBases []ObjectID
							className := ""
							functionName := ""
							functionMembers := 0
							if meta != nil {
								vmCategory = meta.category
								classObject = meta.classObject
								owner = meta.owner
								classBases = meta.classBases
								superclassBases = meta.superclassBases
								classID := meta.classObject
								if classID == 0 {
									classID = meta.owner
								}
								if classMeta := v.meta[classID]; classMeta != nil && classMeta.class != nil {
									className = classMeta.class.Name
								}
								if meta.function != nil {
									functionName = meta.function.Name
									functionMembers = len(meta.function.Members)
								}
							}
							names := make([]string, 0)
							if object != nil {
								objectCategory = object.Category
								for _, entry := range object.Entries() {
									names = append(names, entry.Name)
									if len(names) == 64 {
										break
									}
								}
							}
							fmt.Printf("[missing-member] id=%d object-category=%d class=%q function=%q function-members=%d vm-category=%d class-object=%d owner=%d bases=%v super-bases=%v members=%q\n", id, objectCategory, className, functionName, functionMembers, vmCategory, classObject, owner, classBases, superclassBases, names)
						}
						e = fmt.Errorf("member not found: %s (receiver=%d)", key.Text(), id)
					} else if e == nil {
						r[op.Out] = VoidValue
					}
				}
			}
		case OpTypeOfMember:
			receiver := reg(op.Object)
			key := reg(op.Key)
			if op.StaticKey {
				key = programConstant(p, op.Const)
			}
			if receiver.Kind() == KindString {
				var x Value
				var ok bool
				if x, ok = vmStringProperty(receiver, key); !ok {
					x, ok, e = v.vmStringMethod(key.Text())
				}
				if e == nil {
					if !ok {
						put(op.Out, StringValue("undefined"))
					} else {
						y, er := x.Unary("typeof")
						e = er
						put(op.Out, y)
					}
				}
			} else {
				id, er := v.handle(receiver)
				if er != nil {
					e = er
				} else {
					var x Value
					var ok bool
					if m := v.meta[id]; m != nil && m.category == CategoryArray {
						x, ok = vmArrayProperty(m, key)
						if ok && x.IsVoid() {
							put(op.Out, StringValue("undefined"))
							continue
						}
						if !ok {
							if _, indexed := vmArrayIndex(key); indexed {
								put(op.Out, StringValue("undefined"))
								continue
							}
							x, ok, e = v.getMemberWithRuntime(id, key, h, b)
							if e == nil && !ok {
								x, ok, e = v.vmArrayMethod(id, key.Text())
							}
							if e == nil && !ok {
								x, ok, e = v.missingMember(receiver, key, nil, h, b)
							}
						}
					} else {
						x, ok, e = v.getMemberWithRuntime(id, key, h, b)
						if e == nil && !ok {
							x, ok, e = v.missingMember(receiver, key, nil, h, b)
						}
					}
					if e == nil {
						if !ok {
							put(op.Out, StringValue("undefined"))
						} else {
							x, e = v.getValue(receiver, x, false, h, b)
							if e == nil {
								y, er := x.Unary("typeof")
								e = er
								put(op.Out, y)
							}
						}
					}
				}
			}
		case OpSet:
			receiver := r[op.Object]
			key := r[op.Key]
			if op.StaticKey {
				key = programConstant(p, op.Const)
			}
			value := r[op.Input]
			if receiver.Kind() == KindOctet {
				index, indexed := octetIndexKey(key)
				if !indexed {
					e = fmt.Errorf("member not found: %s", key.Text())
					break
				}
				e = setOctetElement(receiver, index, value)
				break
			}
			id, er := v.handle(receiver)
			if er != nil {
				e = er
			} else if m := v.meta[id]; m != nil && m.category == CategoryArray {
				if key.Kind() == KindString && (key.Text() == "count" || key.Text() == "length") {
					e = setVMArrayLength(m, value)
				} else if n, indexed := vmArrayIndex(key); indexed {
					if n >= int64(maxVMArrayLength) {
						e = errors.New("TJS array length limit exceeded")
					} else {
						index := int(n)
						if index >= len(m.array) {
							m.array = append(m.array, make([]Value, index+1-len(m.array))...)
						}
						m.array[index] = value
						if n >= m.arrayLength {
							m.arrayLength = n + 1
						}
					}
				} else {
					x, ok, er := v.getMemberWithRuntime(id, key, h, b)
					if er != nil {
						e = er
					} else if ok {
						if propertyID, er := v.handle(x); !op.Raw && er == nil && v.meta[propertyID] != nil && v.meta[propertyID].category == CategoryProperty {
							e = v.writeProperty(x, receiver, value, h, b)
						} else {
							e = v.setMember(id, key, value)
						}
					} else {
						_, handled, missingErr := v.missingMember(receiver, key, &value, h, b)
						e = missingErr
						if e == nil && !handled {
							e = v.setMember(id, key, value)
						}
					}
				}
			} else {
				x, ok, er := v.getMemberWithRuntime(id, key, h, b)
				if er != nil {
					e = er
				} else if ok {
					if propertyID, er := v.handle(x); !op.Raw && er == nil && v.meta[propertyID] != nil && v.meta[propertyID].category == CategoryProperty {
						e = v.writeProperty(x, receiver, value, h, b)
					} else {
						e = v.setMember(id, key, value)
					}
				} else {
					_, handled, missingErr := v.missingMember(receiver, key, &value, h, b)
					e = missingErr
					if e == nil && !handled {
						e = v.setMember(id, key, value)
					}
				}
			}
		case OpArray:
			operands := op.items()
			items := make([]Value, len(operands))
			for i, x := range operands {
				items[i] = reg(x)
			}
			x, er := v.NewArray(items)
			e = er
			put(op.Out, x)
		case OpDictionary:
			x, er := v.NewDictionary()
			e = er
			if e == nil {
				for _, pair := range op.pairs() {
					value := reg(pair.Value)
					e = v.setMember(x.ObjectRef().Object, reg(pair.Key), value)
					if e != nil {
						break
					}
				}
			}
			put(op.Out, x)
		case OpFunction:
			if op.function() == nil {
				e = errors.New("nil function descriptor")
			} else {
				function := op.function()
				if f.functionObject != 0 && function.Name != "" && v.hasNestedFunction(f.functionObject, function.Name) {
					if existing, ok, memberErr := v.getMemberString(f.functionObject, function.Name); memberErr != nil {
						e = memberErr
					} else if ok {
						if memberID, handleErr := v.handle(existing); handleErr == nil && v.meta[memberID] != nil && v.meta[memberID].category == CategoryFunction {
							put(op.Out, existing)
							break
						}
					}
				}
				if e == nil {
					lexicalContext := f.lexicalContext
					if lexicalContext == 0 {
						lexicalContext = f.context
					}
					functionValue, _, er := v.newFunctionValue(*function, f.scopes, f.withs, lexicalContext)
					e = er
					if e == nil {
						if f.functionObject != 0 && function.Name != "" && v.hasNestedFunction(f.functionObject, function.Name) {
							e = v.setMemberString(f.functionObject, function.Name, functionValue)
						}
						if e == nil {
							put(op.Out, functionValue)
						}
					}
				}
			}
		case OpCall:
			args, er := v.expandArgs(op.args(), r, f.args, f.argScratch)
			if er == nil {
				f.argScratch = args[:0]
			}
			e = er
			if e == nil {
				ctx := ObjectValue(NewObjectRef(f.context))
				if op.ContextSet {
					ctx = r[op.Context]
				}
				callee := r[op.Callee]
				x, er := v.invokeOwned(callee, ctx, args, h, b, op.ResultNeeded)
				e = er
				r[op.Out] = x
			}
		case OpConstruct:
			args, er := v.expandArgs(op.args(), r, f.args, f.argScratch)
			if er == nil {
				f.argScratch = args[:0]
			}
			e = er
			if e == nil {
				var x Value
				if op.TargetSet {
					x, er = v.constructForMemberAssignment(r[op.Callee], args, r[op.Object], r[op.Key], h, b)
				} else {
					x, er = v.construct(r[op.Callee], args, h, b)
				}
				e = er
				r[op.Out] = x
			}
		case OpClass:
			if op.class() == nil {
				e = errors.New("nil class descriptor")
				break
			}
			x, id, er := v.alloc(CategoryClass)
			e = er
			if e != nil {
				break
			}
			classObject, er := v.obj(id)
			if er != nil {
				e = er
				break
			}
			meta := v.meta[id]
			definition := op.class().Clone()
			meta.class = &definition
			meta.classObject = id
			for _, baseRegister := range op.items() {
				baseID, baseErr := v.handle(reg(baseRegister))
				if baseErr != nil {
					e = baseErr
					break
				}
				baseMeta := v.meta[baseID]
				if baseMeta == nil || baseMeta.category != CategoryClass {
					e = errors.New("TJS class base must be a class")
					break
				}
				meta.classBases = append(meta.classBases, baseID)
			}
			if e != nil {
				break
			}
			for i := range definition.Methods {
				function := definition.Methods[i].Clone()
				method, methodID, allocErr := v.newFunctionValue(function, nil, nil, 0)
				if allocErr != nil {
					e = allocErr
					break
				}
				v.meta[methodID].owner = id
				if setErr := classObject.Set(function.Name, method); setErr != nil {
					e = setErr
					break
				}
			}
			if e != nil {
				break
			}
			for i := range definition.Properties {
				property := definition.Properties[i].Clone()
				value, propertyID, allocErr := v.alloc(CategoryProperty)
				if allocErr != nil {
					e = allocErr
					break
				}
				v.meta[propertyID].property = &property
				v.meta[propertyID].owner = id
				if setErr := classObject.Set(property.Name, value); setErr != nil {
					e = setErr
					break
				}
			}
			if e == nil {
				e = classObject.Set("name", StringValue(definition.Name))
			}
			if e == nil {
				e = classObject.SetFlags("name", MemberReadOnly)
			}
			put(op.Out, x)
		case OpProperty:
			if op.property() == nil {
				e = errors.New("nil property descriptor")
			} else {
				x, id, er := v.alloc(CategoryProperty)
				e = er
				if e == nil {
					v.meta[id].property = op.property()
					put(op.Out, x)
				}
			}
		case OpReadProperty:
			x, er := v.readProperty(reg(op.Input), v.frameContextValue(&f), h, b)
			e = er
			put(op.Out, x)
		case OpWriteProperty:
			e = v.writeProperty(reg(op.Context), v.frameContextValue(&f), reg(op.Input), h, b)
		case OpEval: // Eval accepts an expression or a statement script.
			source := reg(op.Input).Text()
			q, er := CompileEval("<eval operator>", source)
			if er != nil {
				e = er
			} else {
				x, er := v.run(&q, h, b, vmFrame{context: f.context})
				e = er
				put(op.Out, x)
			}
		case OpJump:
			if op.Target < 0 || op.Target > len(p.Code) {
				e = errors.New("invalid TJS branch target")
			} else {
				ip = op.Target
			}
		case OpJumpUnless:
			ok, er := reg(op.Input).Truth()
			e = er
			if e == nil && !ok {
				ip = op.Target
			}
		case OpEnterScope:
			f.scopes = append(f.scopes, make(map[string]Value))
		case OpLeaveScope:
			if len(f.scopes) == 0 {
				e = errors.New("TJS scope stack underflow")
			} else {
				f.scopes = f.scopes[:len(f.scopes)-1]
			}
		case OpEnterWith:
			id, er := v.handle(reg(op.Input))
			e = er
			if e == nil {
				f.withs = append(f.withs, reg(op.Input))
				_ = id
			}
		case OpLeaveWith:
			if len(f.withs) == 0 {
				e = errors.New("TJS with stack underflow")
			} else {
				f.withs = f.withs[:len(f.withs)-1]
			}
		case OpWith:
			if len(f.withs) == 0 {
				e = errors.New("dot member outside with")
			} else {
				put(op.Out, f.withs[len(f.withs)-1])
			}
		case OpTry:
			if len(f.handlers) >= 128 {
				e = errors.New("TJS handler stack depth exceeded")
			} else {
				f.handlers = append(f.handlers, vmHandler{target: op.Target, exception: op.Exception, scopes: len(f.scopes), withs: len(f.withs), finally: op.Finally})
			}
		case OpEndTry:
			if len(f.handlers) == 0 {
				e = errors.New("TJS handler stack underflow")
			} else {
				f.handlers = f.handlers[:len(f.handlers)-1]
			}
		case OpEndFinally:
			if f.pending != nil {
				completion := f.pending
				f.pending = nil
				if completion.kind == "throw" {
					thrown := completion.thrown
					if thrown == nil {
						thrown = v.thrown(completion.value)
					}
					if !routeThrow(thrown) {
						e = thrown
					}
				} else {
					routeCompletion(completion)
				}
			}
		case OpUnwind:
			targetScopes := f.scopeBase + op.Scopes
			if op.Operator == "jump" {
				routeCompletion(&vmCompletion{kind: "jump", target: op.Target, scopes: targetScopes, handlers: op.Handlers, withs: op.Withs})
			} else if targetScopes > len(f.scopes) || op.Handlers > len(f.handlers) || op.Withs > len(f.withs) {
				e = errors.New("invalid TJS unwind depth")
			} else {
				f.scopes = f.scopes[:targetScopes]
				f.handlers = f.handlers[:op.Handlers]
				f.withs = f.withs[:op.Withs]
			}
		case OpThrow:
			e = v.thrown(reg(op.Input))
		case OpReturn:
			if len(f.handlers) > 0 {
				routeCompletion(&vmCompletion{kind: "return", value: reg(op.Input), scopes: 0, handlers: 0, withs: 0})
			} else {
				out = reg(op.Input)
				returned = true
			}
		default:
			e = vmAbortError(fmt.Errorf("unsupported TJS opcode %q", op.Code))
		}
		if e != nil {
			traceErr := fmt.Errorf("%s:%d:%d at VM instruction %d: %w", ins.Location.Storage, ins.Location.Line, ins.Location.Column, at, e)
			var thrown *tjsThrown
			if errors.As(e, &thrown) {
				f.pending = nil
				if routeThrow(thrown) {
					continue
				}
				return VoidValue, traceErr
			}
			if isVMAbortError(e) {
				return VoidValue, traceErr
			}
			if len(f.handlers) > 0 {
				exception, exceptionErr := v.exceptionValue(exceptionMessage(e), traceErr.Error(), h, b)
				if exceptionErr != nil {
					return VoidValue, fmt.Errorf("%s:%d:%d at VM instruction %d: %w", ins.Location.Storage, ins.Location.Line, ins.Location.Column, at, exceptionErr)
				}
				f.pending = nil
				if routeThrow(v.thrown(exception)) {
					continue
				}
			}
			return VoidValue, traceErr
		}
		if returned {
			return out, nil
		}
	}
	return VoidValue, nil
}
func (v *VM) expandArgs(spec []Argument, regs []Value, forward, reuse []Value) ([]Value, error) {
	out := reuse[:0]
	if cap(out) < len(spec) {
		out = make([]Value, 0, len(spec))
	}
	for _, a := range spec {
		var xs []Value
		switch a.Kind {
		case ArgumentValue:
			if a.Register < 0 || a.Register >= len(regs) {
				return nil, errors.New("invalid argument register")
			}
			xs = []Value{regs[a.Register]}
		case ArgumentSpread:
			if a.Register < 0 || a.Register >= len(regs) {
				return nil, errors.New("invalid spread register")
			}
			id, e := v.handle(regs[a.Register])
			if e != nil {
				return nil, e
			}
			m := v.meta[id]
			if m == nil || m.category != CategoryArray {
				return nil, errors.New("argument expansion requires Array")
			}
			xs = m.array
		case ArgumentForward:
			xs = forward
		case ArgumentForwardFrom:
			if a.From < 0 {
				return nil, errors.New("invalid forwarded argument offset")
			}
			if a.From < len(forward) {
				xs = forward[a.From:]
			}
		default:
			return nil, errors.New("invalid argument kind")
		}
		if len(out)+len(xs) > 1024 {
			return nil, errors.New("too many expanded TJS arguments")
		}
		out = append(out, xs...)
	}
	return out, nil
}
func (v *VM) pushCallRoots(callee, context Value, args []Value) int {
	depth := len(v.transientRoots)
	v.transientRoots = append(v.transientRoots, callee, context)
	v.transientRoots = append(v.transientRoots, args...)
	return depth
}
func (v *VM) popTransientRoots(depth int) {
	for i := depth; i < len(v.transientRoots); i++ {
		v.transientRoots[i] = Value{}
	}
	v.transientRoots = v.transientRoots[:depth]
}

// InvokeLegacy invokes an intercode callable through this VM's metadata and
// host, preserving the native/script function implementation and receiver.
func (v *VM) InvokeLegacy(callee, context Value, args []Value, h Host, budget *uint64, resultNeeded bool) (Value, error) {
	return v.invoke(callee, context, args, h, budget, resultNeeded)
}

func (v *VM) invoke(callee, context Value, args []Value, h Host, b *uint64, needed bool) (Value, error) {
	return v.invokeWithOwnership(callee, context, args, h, b, needed, false)
}

func (v *VM) invokeOwned(callee, context Value, args []Value, h Host, b *uint64, needed bool) (Value, error) {
	return v.invokeWithOwnership(callee, context, args, h, b, needed, true)
}

// bindConstructingBaseConstructor runs a named base constructor on the newly
// allocated instance, including legacy calls made through the global class
// object rather than a bound `super` method.
func (v *VM) bindConstructingBaseConstructor(callee Value) Value {
	if callee.Kind() != KindObject || callee.IsNull() || len(v.constructing) == 0 {
		return callee
	}
	methodID := callee.ObjectRef().Object
	method := v.meta[methodID]
	instanceID := v.constructing[len(v.constructing)-1]
	instance := v.meta[instanceID]
	if method == nil || instance == nil || instance.category != CategoryInstance {
		return callee
	}
	baseName := ""
	switch method.category {
	case CategoryFunction:
		if method.function != nil {
			baseName = method.function.Name
		}
	case CategoryNative:
		// Native class constructors are exposed as methods named after the
		// class (for example KAGParser.KAGParser). A script may cache a
		// `super` constructor function globally; its captured super receiver
		// must not override the instance currently being constructed.
		if separator := strings.LastIndexByte(method.native, '.'); separator >= 0 {
			baseName = method.native[separator+1:]
		}
	}
	if baseName == "" || !v.classHasBaseNamed(instance.classObject, baseName, 0) {
		return callee
	}
	return ObjectValue(NewObjectRef(methodID, instanceID))
}

func (v *VM) preserveExplicitInstanceContext(callee, context Value) bool {
	if callee.Kind() != KindObject || callee.IsNull() || !callee.ObjectRef().HasBoundContext() ||
		context.Kind() != KindObject || context.IsNull() {
		return false
	}
	boundMeta := v.meta[callee.ObjectRef().Context]
	contextMeta := v.meta[context.ObjectRef().Object]
	if boundMeta == nil || boundMeta.category != CategoryClass || contextMeta == nil || contextMeta.category != CategoryInstance {
		return false
	}
	return v.classHasBase(contextMeta.classObject, callee.ObjectRef().Context, 0)
}

func (v *VM) linkNativeClassConstructor(callee, context Value) bool {
	if callee.Kind() != KindObject || callee.IsNull() ||
		context.Kind() != KindObject || context.IsNull() {
		return false
	}
	methodID := callee.ObjectRef().Object
	method := v.meta[methodID]
	if method == nil || method.category != CategoryNative || method.nativeStatic {
		return false
	}
	contextMeta := v.meta[context.ObjectRef().Object]
	if contextMeta == nil || contextMeta.category != CategoryClass {
		return false
	}
	separator := strings.LastIndexByte(method.native, '.')
	if separator <= 0 || separator+1 >= len(method.native) {
		return false
	}
	className, constructorName := method.native[:separator], method.native[separator+1:]
	if className != constructorName {
		return false
	}
	base, ok := v.Globals[className]
	if !ok || base.Kind() != KindObject || base.IsNull() {
		return false
	}
	baseID, err := v.handle(base)
	if err != nil {
		return false
	}
	baseMeta := v.meta[baseID]
	if baseMeta == nil || baseMeta.category != CategoryClass {
		return false
	}
	if context.ObjectRef().Object == baseID {
		return true
	}
	v.linkClassBase(context, baseID)
	return true
}

// forwardClassMethodReceiver preserves an instance receiver for an explicit
// class-member call made from a method on that same instance. TJS scripts use
// this form to call an inherited implementation through its global class
// object (for example, global.KAGLayer.beginTransition(...)).
func (v *VM) forwardClassMethodReceiver(callee, context Value) Value {
	if callee.Kind() != KindObject || callee.IsNull() ||
		context.Kind() != KindObject || context.IsNull() || len(v.traceStack) == 0 {
		return context
	}
	method := v.meta[callee.ObjectRef().Object]
	classID := context.ObjectRef().Object
	class := v.meta[classID]
	if method == nil || (method.category != CategoryFunction && method.category != CategoryNative && method.category != CategoryMethod) ||
		class == nil || class.category != CategoryClass {
		return context
	}
	instanceID := v.traceStack[len(v.traceStack)-1].context
	instance := v.meta[instanceID]
	if instance == nil || instance.category != CategoryInstance {
		return context
	}
	if !v.classHasBase(instance.classObject, classID, 0) && !v.classOwnsInstanceMethod(instance.classObject, method) {
		return context
	}
	return ObjectValue(NewObjectRef(instanceID, instanceID))
}

func (v *VM) classOwnsInstanceMethod(classID ObjectID, method *vmObject) bool {
	if method == nil || method.owner == 0 || method.function == nil || !v.classHasBase(classID, method.owner, 0) {
		return false
	}
	owner, err := v.obj(method.owner)
	if err != nil {
		return false
	}
	if _, flags, direct := hasDirect(owner, UTF16FromString(method.function.Name)); direct && flags&StaticMember != 0 {
		return false
	}
	return true
}

func (v *VM) classHasBase(classID, targetID ObjectID, depth int) bool {
	if depth >= 128 {
		return false
	}
	if classID == targetID {
		return true
	}
	class := v.meta[classID]
	if class == nil || class.category != CategoryClass {
		return false
	}
	for _, baseID := range class.superclassBases {
		if baseID != 0 && (baseID == targetID || v.classHasBase(baseID, targetID, depth+1)) {
			return true
		}
	}
	for _, baseID := range class.classBases {
		if baseID == targetID || v.classHasBase(baseID, targetID, depth+1) {
			return true
		}
	}
	return false
}

func (v *VM) classHasBaseNamed(classID ObjectID, name string, depth int) bool {
	if depth >= 128 {
		return false
	}
	meta := v.meta[classID]
	if meta == nil {
		return false
	}
	for _, candidate := range meta.superclassBases {
		if candidate == 0 {
			continue
		}
		base := v.meta[candidate]
		if base != nil && base.class != nil && base.class.Name == name ||
			v.classHasBaseNamed(candidate, name, depth+1) {
			return true
		}
	}
	for _, candidate := range meta.classBases {
		base := v.meta[candidate]
		if base != nil && base.class != nil && base.class.Name == name ||
			v.classHasBaseNamed(candidate, name, depth+1) {
			return true
		}
	}
	return false
}

func (v *VM) invokeWithOwnership(callee, context Value, args []Value, h Host, b *uint64, needed, owned bool) (Value, error) {
	rootDepth := v.pushCallRoots(callee, context, args)
	defer v.popTransientRoots(rootDepth)
	id, e := v.handle(callee)
	if e != nil {
		return VoidValue, e
	}
	if e = v.validID(id); e != nil {
		return VoidValue, e
	}
	m := v.meta[id]
	if m == nil {
		// Dispatcher-created array methods otherwise mutate only the Object
		// backing, leaving a VM-owned array's metadata stale. Route those calls
		// through the VM implementation so both representations stay aligned.
		registry.mu.RLock()
		callable := registry.callable[id]
		registry.mu.RUnlock()
		if callable != nil && callable.valueCall != nil && callable.boundValue != nil &&
			callable.fn.Name == "replace" && callable.boundValue.Kind() == KindString {
			return v.replaceStringValue(*callable.boundValue, args, h, b)
		}
		if callable != nil && callable.call != nil && context.Kind() == KindObject && !context.IsNull() {
			if receiver, ok := v.Objects.Lookup(context.ObjectRef().Object); ok && receiver.Category == CategoryArray {
				if callable.fn.Name == "load" {
					return v.nativeCall("Array.load", context, args, h, b, needed)
				}
				if meta := v.meta[context.ObjectRef().Object]; meta != nil && meta.category == CategoryArray {
					switch callable.fn.Name {
					case "assign", "split", "push", "unshift", "add", "pop", "shift",
						"reverse", "clear", "join", "find", "indexOf", "remove",
						"erase", "insert", "sort":
						return v.nativeCall("Array."+callable.fn.Name, context, args, h, b, needed)
					}
				}
			}
		}
		dispatcher := NewDispatcher(v.Objects)
		return dispatcher.CallWithContext(callee, context.ObjectRef(), args...)
	}
	if context.Kind() == KindObject && !context.IsNull() {
		if proxy := v.meta[context.ObjectRef().Object]; proxy != nil && proxy.superOwner != 0 && proxy.superContext != 0 {
			context = ObjectValue(NewObjectRef(proxy.superContext, proxy.superContext))
		}
	}
	callee = v.bindConstructingBaseConstructor(callee)
	context = v.forwardClassMethodReceiver(callee, context)
	if m.category == CategoryNative {
		constructorContext := context
		if callee.ObjectRef().HasBoundContext() && !v.preserveExplicitInstanceContext(callee, context) {
			constructorContext = v.boundContextValue(callee.ObjectRef())
		}
		if v.linkNativeClassConstructor(callee, constructorContext) {
			return VoidValue, nil
		}
	}
	if (m.category == CategoryNative || m.category == CategoryMethod) && callee.ObjectRef().HasBoundContext() {
		if !v.preserveExplicitInstanceContext(callee, context) {
			context = v.boundContextValue(callee.ObjectRef())
		}
	}
	if m.category == CategoryClass && callee.ObjectRef().HasBoundContext() {
		context = v.boundContextValue(callee.ObjectRef())
	}
	if m.category == CategoryMethod && m.methodContextSet && !callee.ObjectRef().HasBoundContext() {
		context = m.methodContext
	}
	switch m.category {
	case CategoryNative:
		return v.nativeCall(m.native, context, args, h, b, needed)
	case CategoryFunction:
		if m.legacyFunction != nil {
			if callee.ObjectRef().HasBoundContext() && !v.preserveExplicitInstanceContext(callee, context) {
				context = v.boundContextValue(callee.ObjectRef())
			}
			return m.legacyFunction(context, args, h, b, needed)
		}
		if m.function == nil {
			return VoidValue, errors.New("invalid TJS function")
		}
		c := context
		if callee.ObjectRef().HasBoundContext() && !v.preserveExplicitInstanceContext(callee, context) {
			c = v.boundContextValue(callee.ObjectRef())
		}
		cid, _ := v.handle(c)
		lexicalContext := m.lexicalContext
		if lexicalContext != 0 && cid != 0 {
			lexicalMeta := v.meta[lexicalContext]
			contextMeta := v.meta[cid]
			if lexicalMeta != nil && lexicalMeta.category == CategoryClass &&
				contextMeta != nil && contextMeta.category == CategoryInstance {
				lexicalContext = cid
			}
		}
		scopes := append([]map[string]Value(nil), m.capturedScopes...)
		local := make(map[string]Value)
		scopes = append(scopes, local)
		if m.function.Name != "" {
			local[m.function.Name] = callee
		}
		if !owned {
			args = append([]Value(nil), args...)
		}
		f := vmFrame{context: cid, lexicalContext: lexicalContext, args: args, owner: m.owner, scopes: scopes, withs: append([]Value(nil), m.capturedWiths...), functionName: m.function.Name, function: true, functionObject: id}
		result, runErr := v.run(&m.function.Program, h, b, f)
		if runErr == nil && m.function.Name == "onResize" {
			if _, found, err := v.GetMemberIfPresent(c, "_mtx", h, b); err != nil {
				return VoidValue, err
			} else if found {
				if err := v.SetMemberWithRuntime(c, "_mtx", VoidValue, IgnoreProperty, h, b); err != nil {
					return VoidValue, err
				}
			}
		}
		return result, runErr
	case CategoryMethod:
		return v.nativeCall(m.native, context, args, h, b, needed)
	case CategoryClass:
		v.linkClassBase(context, id)
		if context.Kind() == KindObject && !context.IsNull() {
			v.addInstanceClassName(context.ObjectRef().Object, id)
		}
		if m.legacyClassBody != nil {
			value, err := v.invokeLegacyClassBody(m, context, args, h, b, needed)
			if err != nil {
				return VoidValue, err
			}
			if context.Kind() == KindObject && !context.IsNull() && v.legacyConstructorDepth > 0 &&
				!v.legacyAutoConstructorActive(id) &&
				v.legacyClassBodyDepth == 0 &&
				len(v.constructing) != 0 &&
				v.constructing[len(v.constructing)-1] == context.ObjectRef().Object {
				if constructor, ok := v.legacyClassConstructor(id, h, b); ok {
					if constructor.Kind() == KindObject && !constructor.IsNull() {
						constructor = ObjectValue(NewObjectRef(constructor.ObjectRef().Object, context.ObjectRef().Object))
					}
					autoDepth := len(v.legacyAutoConstructorClasses)
					v.legacyAutoConstructorClasses = append(v.legacyAutoConstructorClasses, id)
					_, err := v.invoke(constructor, context, args, h, b, true)
					v.legacyAutoConstructorClasses = v.legacyAutoConstructorClasses[:autoDepth]
					if err != nil {
						return VoidValue, err
					}
				}
			}
			return value, nil
		}
		if m.nativeClass && context.Kind() == KindObject && !context.IsNull() {
			contextID := context.ObjectRef().Object
			// A native base call initializes the native part of the current
			// class or instance. The reference VM registers the native object
			// during tTJSNativeClass::FuncCall; @initialize is the matching
			// host operation here.
			if contextMeta := v.meta[contextID]; contextMeta != nil &&
				(contextMeta.category == CategoryClass || contextMeta.category == CategoryInstance) {
				initializer, found, err := v.getMemberWithRuntime(id, StringValue("@initialize"), h, b)
				if err != nil {
					return VoidValue, err
				}
				if found {
					return v.invoke(initializer, context, args, h, b, needed)
				}
			}
			if len(args) != 0 && m.class != nil && m.class.Name != "" {
				classObject, err := v.obj(id)
				if err != nil {
					return VoidValue, err
				}
				if constructor, found := classObject.Get(m.class.Name); found {
					return v.invoke(constructor, context, args, h, b, needed)
				}
			}
			return VoidValue, nil
		}
		if !m.nativeClass && m.class != nil && context.Kind() == KindObject && !context.IsNull() {
			initialized := make(map[ObjectID]struct{})
			initialized[id] = struct{}{}
			if err := v.initializeCompiledClassBases(id, context.ObjectRef().Object, h, b, initialized); err != nil {
				return VoidValue, err
			}
			// Reference class calls execute the class body against the current
			// objthis; compiled class bases run their bodies first.
			if len(m.class.Initializer.Code) == 0 {
				return VoidValue, nil
			}
			return v.run(&m.class.Initializer, h, b, vmFrame{
				context: context.ObjectRef().Object,
				args:    args,
			})
		}
	case CategoryProperty:
		return v.getProperty(id, context, h, b)
	}
	return VoidValue, errors.New("TJS object is not callable")
}

func (v *VM) invokeLegacyClassBody(m *vmObject, context Value, args []Value, h Host, b *uint64, needed bool) (Value, error) {
	if m == nil || m.legacyClassBody == nil {
		return VoidValue, nil
	}
	wasInitializing := m.legacyClassInitializing
	m.legacyClassInitializing = true
	v.legacyClassBodyDepth++
	value, err := m.legacyClassBody(context, args, h, b, needed)
	v.legacyClassBodyDepth--
	m.legacyClassInitializing = wasInitializing
	return value, err
}

func (v *VM) legacyClassConstructor(classID ObjectID, h Host, b *uint64) (Value, bool) {
	meta := v.meta[classID]
	if meta == nil || meta.class == nil || meta.class.Name == "" {
		return VoidValue, false
	}
	if meta.legacyConstructor != 0 {
		return ObjectValue(NewObjectRef(meta.legacyConstructor)), true
	}
	classObject, err := v.obj(classID)
	if err != nil {
		return VoidValue, false
	}
	if constructor, ok := classObject.Get(meta.class.Name); ok {
		return constructor, true
	}
	constructor, ok, err := v.lookupClassMemberWithRuntime(classID, meta.class.Name, 0, false, h, b)
	if err != nil || !ok {
		return VoidValue, false
	}
	return constructor, true
}

func (v *VM) legacyAutoConstructorActive(classID ObjectID) bool {
	for _, active := range v.legacyAutoConstructorClasses {
		if active == classID {
			return true
		}
	}
	return false
}

func (v *VM) initializeCompiledClassBases(classID, instanceID ObjectID, h Host, b *uint64, initialized map[ObjectID]struct{}) error {
	class := v.meta[classID]
	if class == nil || class.category != CategoryClass || class.legacyClassBody != nil {
		return nil
	}
	for _, baseID := range class.classBases {
		if err := v.initializeClassWithState(baseID, instanceID, nil, h, b, initialized); err != nil {
			baseName := ""
			if base := v.meta[baseID]; base != nil && base.class != nil {
				baseName = base.class.Name
			}
			return fmt.Errorf("initialize base class %q (%d): %w", baseName, baseID, err)
		}
	}
	return nil
}

func (v *VM) initializeClass(classID, instance ObjectID, args []Value, h Host, b *uint64) error {
	return v.initializeClassWithState(classID, instance, args, h, b, make(map[ObjectID]struct{}))
}

func (v *VM) initializeClassWithState(classID, instance ObjectID, args []Value, h Host, b *uint64, initialized map[ObjectID]struct{}) error {
	if err := v.validID(classID); err != nil {
		return err
	}
	class := v.meta[classID]
	if class == nil || class.category != CategoryClass {
		return errors.New("TJS class base must be a class")
	}
	if _, alreadyInitialized := initialized[classID]; alreadyInitialized {
		return nil
	}
	initialized[classID] = struct{}{}
	if err := v.initializeLegacyClassNow(classID, h, b); err != nil {
		return err
	}
	v.addInstanceClassName(instance, classID)
	if err := v.resolveLegacyClassBases(classID, h, b); err != nil {
		return err
	}
	if err := v.initializeCompiledClassBases(classID, instance, h, b, initialized); err != nil {
		return err
	}
	// Legacy bytecode invokes base bodies explicitly; compiled class
	// descriptors keep evaluated bases separately and initialize them here.
	if names, registered := v.Objects.ClassInstanceMembers(classID); registered && class.legacyClassBody == nil {
		classObject, err := v.obj(classID)
		if err != nil {
			return err
		}
		for _, name := range names {
			value, exists := classObject.Get(name)
			if !exists {
				continue
			}
			if err := v.setMember(instance, StringValue(name), v.bindClassMember(value, instance)); err != nil {
				return err
			}
		}
	}
	context := ObjectValue(NewObjectRef(instance, instance))
	if class.legacyClassBody != nil {
		if _, err := v.invokeLegacyClassBody(class, context, nil, h, b, false); err != nil {
			return fmt.Errorf("initialize legacy class instance body %q (%d): %w", class.class.Name, classID, err)
		}
	}
	if class.native != "" {
		if _, err := v.nativeCall(class.native, context, args, h, b, true); err != nil {
			return err
		}
	} else if classObject, err := v.obj(classID); err != nil {
		return err
	} else if initializer, ok := classObject.Get("@initialize"); ok {
		if _, err := v.invoke(initializer, context, args, h, b, true); err != nil {
			return err
		}
	}
	if class.class != nil && len(class.class.Initializer.Code) != 0 {
		_, err := v.run(&class.class.Initializer, h, b, vmFrame{context: instance})
		if err != nil {
			return err
		}
	}
	return nil
}

func (v *VM) addInstanceClassName(instanceID, classID ObjectID) {
	instance := v.meta[instanceID]
	class := v.meta[classID]
	if instance == nil || instance.category != CategoryInstance || class == nil || class.category != CategoryClass || class.class == nil || class.class.Name == "" {
		return
	}
	for _, name := range instance.instanceClassNames {
		if name == class.class.Name {
			return
		}
	}
	instance.instanceClassNames = append(instance.instanceClassNames, class.class.Name)
}

func (v *VM) construct(callee Value, args []Value, h Host, b *uint64) (Value, error) {
	return v.constructWithMemberTarget(callee, args, VoidValue, VoidValue, false, h, b)
}

func (v *VM) constructForMemberAssignment(callee Value, args []Value, object, key Value, h Host, b *uint64) (Value, error) {
	return v.constructWithMemberTarget(callee, args, object, key, true, h, b)
}

func (v *VM) constructWithMemberTarget(callee Value, args []Value, targetObject, targetKey Value, publishTarget bool, h Host, b *uint64) (Value, error) {
	rootDepth := v.pushCallRoots(callee, targetObject, args)
	v.transientRoots = append(v.transientRoots, targetKey)
	defer v.popTransientRoots(rootDepth)
	id, err := v.handle(callee)
	if err != nil {
		return VoidValue, err
	}
	meta := v.meta[id]
	if meta != nil && meta.category == CategoryNative {
		return v.nativeCall(meta.native, callee, args, h, b, true)
	}
	if meta == nil || meta.category != CategoryClass {
		return VoidValue, errors.New("TJS object is not constructible")
	}
	if err = v.initializeLegacyClassNow(id, h, b); err != nil {
		return VoidValue, err
	}
	instance, instanceID, err := v.alloc(CategoryInstance)
	if err != nil {
		return VoidValue, err
	}
	v.transientRoots = append(v.transientRoots, instance)
	constructionDepth := len(v.constructing)
	v.constructing = append(v.constructing, instanceID)
	argsDepth := len(v.constructingArgs)
	v.constructingArgs = append(v.constructingArgs, append([]Value(nil), args...))
	defer func() {
		v.constructing = v.constructing[:constructionDepth]
		v.constructingArgs = v.constructingArgs[:argsDepth]
	}()
	v.meta[instanceID].class = meta.class
	v.meta[instanceID].classObject = id
	v.Objects.RegisterClassInstance(instanceID, id)
	if publishTarget {
		targetID, targetErr := v.handle(targetObject)
		if targetErr != nil {
			return VoidValue, targetErr
		}
		if err = v.setMember(targetID, targetKey, instance); err != nil {
			return VoidValue, err
		}
	}
	if err = v.resolveLegacyClassBases(id, h, b); err != nil {
		return VoidValue, err
	}
	if err = v.initializeClass(id, instanceID, args, h, b); err != nil {
		return VoidValue, err
	}
	if meta.class != nil && meta.class.Name != "" {
		classObject, objectErr := v.obj(id)
		if objectErr != nil {
			return VoidValue, objectErr
		}
		constructor, ok := classObject.Get(meta.class.Name)
		if !ok {
			var lookupErr error
			constructor, ok, lookupErr = v.lookupClassMemberWithRuntime(id, meta.class.Name, 0, false, h, b)
			if lookupErr != nil {
				return VoidValue, lookupErr
			}
		}
		if ok {
			if constructor.Kind() == KindObject && !constructor.IsNull() {
				constructor = ObjectValue(NewObjectRef(constructor.ObjectRef().Object, instanceID))
			}
			v.legacyConstructorDepth++
			_, err = v.invoke(constructor, instance, args, h, b, true)
			v.legacyConstructorDepth--
			if err != nil {
				return VoidValue, err
			}
		} else if meta.nativeClass && meta.native == "" && meta.nativeConstructor != 0 {
			classObject, objectErr := v.obj(id)
			if objectErr != nil {
				return VoidValue, objectErr
			}
			if _, hasInitializer := classObject.Get("@initialize"); !hasInitializer {
				constructor := ObjectValue(NewObjectRef(meta.nativeConstructor, instanceID))
				if _, err = v.invoke(constructor, instance, args, h, b, true); err != nil {
					return VoidValue, err
				}
			}
		}
	}
	if invalidator, ok, lookupErr := v.getMemberWithRuntime(id, StringValue("@invalidate"), h, b); lookupErr != nil {
		return VoidValue, lookupErr
	} else if ok {
		if invalidatorID, invalidatorErr := v.handle(invalidator); invalidatorErr == nil && v.meta[invalidatorID] != nil && v.meta[invalidatorID].category == CategoryNative {
			v.meta[instanceID].finalizer = v.meta[invalidatorID].native
		}
	}
	if finalizer, ok, lookupErr := v.getMemberWithRuntime(instanceID, StringValue("finalize"), h, b); lookupErr != nil {
		return VoidValue, lookupErr
	} else if ok {
		if finalizerID, finalizerErr := v.handle(finalizer); finalizerErr == nil && v.meta[finalizerID] != nil && v.meta[finalizerID].category == CategoryFunction {
			v.meta[instanceID].scriptFinalizer = finalizerID
		}
	}
	return instance, nil
}

func (v *VM) callStringMethod(value Value, name string, args []Value, h Host, b *uint64) (Value, error) {
	if value.Kind() != KindString {
		return Value{}, ErrType
	}
	units := value.string
	switch name {
	case "sprintf":
		return formatTJSString(value, args)
	case "charAt":
		index, err := argInt(args, 0)
		if err != nil {
			return Value{}, err
		}
		if index < 0 || index >= int64(len(units)) {
			return StringValue(""), nil
		}
		return StringUnits(units[index : index+1]), nil
	case "indexOf":
		if len(args) == 0 {
			return IntegerValue(-1), nil
		}
		start := int64(0)
		if len(args) > 1 {
			var err error
			start, err = args[1].Int()
			if err != nil {
				return Value{}, err
			}
		}
		if start < 0 {
			start = 0
		}
		if start > int64(len(units)) {
			return IntegerValue(-1), nil
		}
		index := stringUTF16Index(units, tjsStringUnits(args[0]), int(start))
		return IntegerValue(int64(index)), nil
	case "lastIndexOf":
		if len(args) == 0 {
			return IntegerValue(-1), nil
		}
		index := stringUTF16LastIndex(units, tjsStringUnits(args[0]))
		return IntegerValue(int64(index)), nil
	case "substr":
		start, err := argInt(args, 0)
		if err != nil {
			return Value{}, err
		}
		length := int64(len(units))
		if start < 0 {
			start += length
		}
		if start < 0 {
			start = 0
		} else if start > length {
			start = length
		}
		end := length
		if len(args) > 1 {
			count, err := args[1].Int()
			if err != nil {
				return Value{}, err
			}
			if count < 0 {
				count = 0
			}
			if count < end-start {
				end = start + count
			}
		}
		return StringUnits(units[start:end]), nil
	case "substring":
		start, err := argInt(args, 0)
		if err != nil {
			return Value{}, err
		}
		end := int64(len(units))
		if len(args) > 1 {
			end, err = args[1].Int()
			if err != nil {
				return Value{}, err
			}
		}
		if start < 0 {
			start = 0
		}
		if end < 0 {
			end = 0
		}
		if start > end {
			start, end = end, start
		}
		if start > int64(len(units)) {
			start = int64(len(units))
		}
		if end > int64(len(units)) {
			end = int64(len(units))
		}
		return StringUnits(units[start:end]), nil
	case "escape":
		return StringValue(EscapeUTF16(units)), nil
	case "repeat":
		return repeatString(value, args)
	case "toUpperCase":
		return StringValue(strings.ToUpper(value.Text())), nil
	case "toLowerCase":
		return StringValue(strings.ToLower(value.Text())), nil
	case "trim":
		return StringValue(strings.TrimFunc(value.Text(), unicode.IsSpace)), nil
	case "split":
		if len(args) > 0 {
			if r := regexpValue(args[0]); r != nil {
				purgeEmpty := false
				if len(args) >= 3 && args[2].Kind() != KindVoid {
					var err error
					purgeEmpty, err = args[2].Truth()
					if err != nil {
						return VoidValue, err
					}
				}
				parts := r.SplitPurgeEmpty(value.Text(), purgeEmpty)
				values := make([]Value, len(parts))
				for i, part := range parts {
					values[i] = StringValue(part)
				}
				return v.NewArray(values)
			}
		}
		separator := UTF16(nil)
		if len(args) > 0 {
			separator = UTF16FromString(args[0].Text())
		}
		purgeEmpty := false
		if len(args) >= 3 && args[2].Kind() != KindVoid {
			var err error
			purgeEmpty, err = args[2].Truth()
			if err != nil {
				return VoidValue, err
			}
		}
		return v.NewArray(splitTJSString(value.string, separator, purgeEmpty))
	case "replace":
		return v.replaceStringValue(value, args, h, b)
	default:
		return Value{}, ErrMemberNotFound
	}
}

func (v *VM) replaceStringValue(value Value, args []Value, h Host, budget *uint64) (Value, error) {
	if len(args) < 2 {
		return VoidValue, ErrBadParamCount
	}
	replacement := args[1]
	if os.Getenv("KRKRGO_STRING_REPLACE_TRACE") != "" {
		isRegexp := regexpValue(args[0]) != nil
		fmt.Printf("[string-replace] input=%q pattern=%q regexp=%t replacement=%s:%q\n", value.Text(), args[0].Text(), isRegexp, replacement.TypeName(), replacement.Text())
	}
	if replacement.Kind() != KindObject || replacement.IsNull() {
		return replaceStringValue(value, args)
	}
	invokeReplacement := func(text string, captures []string) (string, error) {
		if os.Getenv("KRKRGO_STRING_REPLACE_TRACE") != "" {
			fmt.Printf("[string-replace] callback match=%q captures=%q\n", text, captures)
		}
		items := make([]Value, 1, len(captures)+1)
		items[0] = StringValue(text)
		for _, capture := range captures {
			items = append(items, StringValue(capture))
		}
		match, err := v.NewArray(items)
		if err != nil {
			return "", err
		}
		result, err := v.InvokeLegacy(replacement, value, []Value{match}, h, budget, true)
		if err != nil {
			return "", err
		}
		if os.Getenv("KRKRGO_STRING_REPLACE_TRACE") != "" {
			fmt.Printf("[string-replace] callback result=%s:%q\n", result.TypeName(), result.Text())
		}
		return result.Text(), nil
	}
	if expression := regexpValue(args[0]); expression != nil {
		result, err := expression.replaceFunc(value.Text(), func(match RegexpMatch) (string, error) {
			return invokeReplacement(match.Text, match.Submatches)
		})
		if err != nil {
			return VoidValue, err
		}
		return StringValue(result), nil
	}
	subject := value.Text()
	search := args[0].Text()
	index := strings.Index(subject, search)
	if index < 0 {
		return value, nil
	}
	result, err := invokeReplacement(search, nil)
	if err != nil {
		return VoidValue, err
	}
	return StringValue(subject[:index] + result + subject[index+len(search):]), nil
}

func (v *VM) callRegexpMethod(context Value, name string, args []Value) (Value, error) {
	r := regexpValue(context)
	if r == nil {
		return Value{}, ErrType
	}
	if name == "_compile" {
		if len(args) != 1 {
			return Value{}, ErrBadParamCount
		}
		encoded := args[0].Text()
		if len(encoded) < 3 || !strings.HasPrefix(encoded, "//") {
			return Value{}, ErrRegexpMalformed
		}
		separator := strings.IndexByte(encoded[2:], '/')
		if separator < 0 {
			return Value{}, ErrRegexpMalformed
		}
		separator += 2
		flags := encoded[2:separator]
		// TJS supports g (global), i (ignore case), and l (ignored). The
		// native parser ignores unknown flag letters as well.
		var nativeFlags strings.Builder
		seenGlobal, seenIgnoreCase := false, false
		for i := range flags {
			switch flags[i] {
			case 'g':
				if !seenGlobal {
					nativeFlags.WriteByte('g')
					seenGlobal = true
				}
			case 'i':
				if !seenIgnoreCase {
					nativeFlags.WriteByte('i')
					seenIgnoreCase = true
				}
			}
		}
		id := context.ObjectRef().Object
		regexMu.Lock()
		delete(regexes, id)
		regexMu.Unlock()
		compiled, err := CompileRegexp(encoded[separator+1:], nativeFlags.String())
		if err != nil {
			return Value{}, err
		}
		regexMu.Lock()
		regexes[id] = compiled
		regexMu.Unlock()
		return VoidValue, nil
	}
	if len(args) == 0 {
		return Value{}, ErrBadParamCount
	}
	switch name {
	case "test":
		return boolValue(r.IsMatch(args[0].Text())), nil
	case "exec", "match":
		match, ok := r.Find(args[0].Text())
		if !ok {
			return v.NewArray(nil)
		}
		items := make([]Value, 1, len(match.Submatches)+1)
		items[0] = StringValue(match.Text)
		for _, submatch := range match.Submatches {
			items = append(items, StringValue(submatch))
		}
		return v.NewArray(items)
	default:
		return Value{}, ErrMemberNotFound
	}
}

func (v *VM) newRegexp(args []Value) (Value, error) {
	pattern, flags := "", ""
	if len(args) > 0 {
		pattern = args[0].Text()
	}
	if len(args) > 1 {
		flags = args[1].Text()
	}
	r, err := CompileRegexp(pattern, flags)
	if err != nil {
		return VoidValue, err
	}
	value, id, err := v.alloc(CategoryNative)
	if err != nil {
		return VoidValue, err
	}
	o, err := v.obj(id)
	if err != nil {
		return VoidValue, err
	}
	registerRegexpObject(o, r)
	return value, nil
}

func (v *VM) nativeCall(name string, context Value, args []Value, h Host, b *uint64, needed bool) (Value, error) {
	profile := v.executionProfile
	var profileStarted time.Time
	if profile != nil {
		profileStarted = time.Now()
		defer func() {
			profile.recordNative(name, time.Since(profileStarted))
		}()
	}
	if strings.HasPrefix(name, "Serialization.") {
		return v.serializationCall(strings.TrimPrefix(name, "Serialization."), context, args, h, b, needed)
	}
	if strings.HasPrefix(name, "RandomGenerator.") {
		return v.randomGeneratorCall(context, strings.TrimPrefix(name, "RandomGenerator."), args, h, b, needed)
	}
	switch name {
	case "Array.assignStruct":
		return v.assignStructure(context, args, true, b)
	case "Dictionary.assignStruct":
		return v.assignStructure(context, args, false, b)
	case "Dictionary.assign":
		return v.dictionaryAssign(context, args, b)
	case "Dictionary.clear":
		return v.dictionaryClear(context)
	}
	if name == "RegExp" {
		return v.newRegexp(args)
	}
	if strings.HasPrefix(name, "RegExp.") {
		return v.callRegexpMethod(context, strings.TrimPrefix(name, "RegExp."), args)
	}
	if strings.HasPrefix(name, "String.") {
		return v.callStringMethod(context, strings.TrimPrefix(name, "String."), args, h, b)
	}
	if name == "Array.load" {
		if rr, ok := h.(HostResult); ok {
			return rr.CallWithResult(v, name, context, args, b, needed)
		}
		if cc, ok := h.(HostContext); ok {
			return cc.CallWithContext(v, name, context, args, b)
		}
		return h.Call(v, name, args, b)
	}
	if strings.HasPrefix(name, "Array.") {
		return v.callArrayMethod(context, strings.TrimPrefix(name, "Array."), args, h, b)
	}
	if name == "Array" {
		return v.NewArray(nil)
	}
	if name == "Dictionary" {
		return v.NewDictionary()
	}
	if name == "Exception.construct" {
		if len(args) > 0 {
			er := v.setMember(context.ObjectRef().Object, StringValue("message"), args[0])
			return VoidValue, er
		}
		return VoidValue, nil
	}
	if rr, ok := h.(HostResult); ok {
		return rr.CallWithResult(v, name, context, args, b, needed)
	}
	if cc, ok := h.(HostContext); ok {
		return cc.CallWithContext(v, name, context, args, b)
	}
	return h.Call(v, name, args, b)
}
func (v *VM) readProperty(property, context Value, h Host, b *uint64) (Value, error) {
	id, e := v.handle(property)
	if e != nil {
		return VoidValue, e
	}
	m := v.meta[id]
	if m == nil {
		return VoidValue, nil
	}
	if m.variantProperty != nil {
		return *m.variantProperty, nil
	}
	context = v.propertyReferenceContext(property, context)
	if m.legacyProperty != nil {
		return v.invokeLegacyPropertyAccessor(m.legacyProperty.getter, context, nil, h, b, true)
	}
	if m.property == nil || m.property.Getter == nil {
		return VoidValue, nil
	}
	return v.invokePropertyFunction(m.property.Getter, context, nil, m.owner, id, h, b)
}
func (v *VM) writeProperty(property, context, input Value, h Host, b *uint64) error {
	id, e := v.handle(property)
	if e != nil {
		return e
	}
	m := v.meta[id]
	if m == nil {
		return nil
	}
	if m.variantProperty != nil {
		*m.variantProperty = input
		return nil
	}
	context = v.propertyReferenceContext(property, context)
	if m.legacyProperty != nil {
		_, e = v.invokeLegacyPropertyAccessor(m.legacyProperty.setter, context, []Value{input}, h, b, false)
		return e
	}
	if m.property == nil || m.property.Setter == nil {
		return nil
	}
	_, e = v.invokePropertyFunction(m.property.Setter, context, []Value{input}, m.owner, id, h, b)
	return e
}
func (v *VM) propertyReferenceContext(property, context Value) Value {
	propertyRef := ObjectRef{}
	propertyBound := false
	if property.Kind() == KindObject && !property.IsNull() {
		propertyRef = property.ObjectRef()
		propertyBound = propertyRef.HasBoundContext()
	}
	if context.Kind() == KindObject && !context.IsNull() {
		contextRef := context.ObjectRef()
		if contextRef.HasBoundContext() {
			// A class proxy carries the live instance in Context. That live
			// instance is the objthis used by the reference VM for inherited
			// property access, while an explicit incontextof instance remains
			// authoritative when both sides carry instance contexts.
			contextMeta := v.meta[contextRef.Context]
			propertyMeta := v.meta[propertyRef.Context]
			if contextMeta != nil && contextMeta.category == CategoryInstance &&
				(!propertyBound || propertyMeta == nil || propertyMeta.category == CategoryClass) {
				context = v.boundContextValue(contextRef)
			} else if propertyBound {
				context = v.boundContextValue(propertyRef)
			} else {
				context = v.boundContextValue(contextRef)
			}
		} else if propertyBound {
			context = v.boundContextValue(propertyRef)
		}
	} else if propertyBound {
		context = v.boundContextValue(propertyRef)
	}
	return v.forwardClassPropertyReceiver(property, context)
}

// forwardClassPropertyReceiver preserves this when a script reaches an
// inherited instance property through a class object, as the C++ class
// context's PropGet/PropSet paths do for objthis.
func (v *VM) forwardClassPropertyReceiver(property, context Value) Value {
	if property.Kind() != KindObject || property.IsNull() || context.Kind() != KindObject || context.IsNull() || len(v.traceStack) == 0 {
		return context
	}
	propertyMeta := v.meta[property.ObjectRef().Object]
	classID := context.ObjectRef().Object
	class := v.meta[classID]
	if propertyMeta == nil || propertyMeta.category != CategoryProperty || propertyMeta.nativeStatic || class == nil || class.category != CategoryClass {
		return context
	}
	instanceID := v.traceStack[len(v.traceStack)-1].context
	instance := v.meta[instanceID]
	if instance == nil || instance.category != CategoryInstance || !v.classHasBase(instance.classObject, classID, 0) {
		return context
	}
	if propertyMeta.owner != 0 && !v.classHasBase(instance.classObject, propertyMeta.owner, 0) {
		return context
	}
	return ObjectValue(NewObjectRef(instanceID, instanceID))
}

// CallFunction and CallEventHandler are safe native callback entry points.
// InvokeProgram executes a descriptor directly for native class dispatch.
func (v *VM) InvokeProgram(program Program, context ObjectRef, args []Value, host Host, budget *uint64) (Value, error) {
	if host == nil {
		host = nopHost{}
	}
	if budget == nil {
		x := uint64(1 << 62)
		budget = &x
	}
	return v.run(&program, host, budget, vmFrame{context: context.Object, args: append([]Value(nil), args...), scopes: []map[string]Value{make(map[string]Value)}})
}
func (v *VM) ExecuteProgram(program Program, context ObjectRef, args []Value, host Host, budget *uint64) (Value, error) {
	return v.InvokeProgram(program, context, args, host, budget)
}
func (v *VM) CallFunction(function, context Value, args []Value, h Host, b *uint64) (Value, error) {
	return v.invoke(function, context, args, h, b, true)
}
func (v *VM) CallEventHandler(function, context Value, args []Value, h Host, b *uint64) (bool, error) {
	if _, e := v.handle(function); e != nil {
		return false, nil
	}
	_, e := v.invoke(function, context, args, h, b, false)
	return e == nil, e
}

// executeProgram is the compatibility hook used by class descriptors.
func executeProgram(program Program, context ObjectRef, args []Value) (Value, error) {
	return NewVM().InvokeProgram(program, context, args, nil, nil)
}

// ClassNames reports the VM-known class name for an instance.
// Objects without class-instance metadata have an empty list.
func (v *VM) ClassNames(value Value) ([]string, error) {
	if v == nil || v.Objects == nil {
		return nil, errors.New("nil TJS VM")
	}
	if value.Kind() != KindObject || value.IsNull() {
		return nil, errors.New("TJS class-name query requires a non-null object")
	}
	id := value.ObjectRef().Object
	object, ok := v.Objects.Lookup(id)
	if !ok || !object.IsValid() {
		return nil, fmt.Errorf("invalid TJS object handle: %d", id)
	}
	instance := v.meta[id]
	if instance == nil || instance.category != CategoryInstance || instance.classObject == 0 {
		return []string{}, nil
	}
	if len(instance.instanceClassNames) != 0 {
		return append([]string(nil), instance.instanceClassNames...), nil
	}
	return v.classNames(instance.classObject, 0, make(map[ObjectID]bool))
}

func (v *VM) classNames(classID ObjectID, depth int, seen map[ObjectID]bool) ([]string, error) {
	if depth >= 128 {
		return nil, errors.New("class inheritance depth exceeded")
	}
	if seen[classID] {
		return nil, nil
	}
	meta := v.meta[classID]
	if meta == nil || meta.category != CategoryClass {
		return nil, errors.New("invalid TJS class handle")
	}
	seen[classID] = true
	var names []string
	if meta.class != nil && meta.class.Name != "" {
		names = append(names, meta.class.Name)
	}
	for _, base := range meta.classBases {
		baseNames, err := v.classNames(base, depth+1, seen)
		if err != nil {
			return nil, err
		}
		names = append(names, baseNames...)
	}
	for _, base := range meta.superclassBases {
		if base == 0 {
			continue
		}
		baseNames, err := v.classNames(base, depth+1, seen)
		if err != nil {
			return nil, err
		}
		names = append(names, baseNames...)
	}
	return names, nil
}
func (v *VM) classObjectHasName(classID ObjectID, name string, depth int) bool {
	if depth >= 128 {
		return false
	}
	meta := v.meta[classID]
	if meta == nil || meta.category != CategoryClass {
		return false
	}
	if meta.class != nil && meta.class.Name == name {
		return true
	}
	for _, base := range meta.classBases {
		if v.classObjectHasName(base, name, depth+1) {
			return true
		}
	}
	return false
}

// TraceString formats the active TJS call stack from the innermost frame out.
// A non-positive limit includes every active frame, matching TJS's zero-limit
// convention.
func (v *VM) TraceString(limit int64) string {
	if v == nil || len(v.traceStack) == 0 {
		return ""
	}
	count := len(v.traceStack)
	if limit > 0 && uint64(limit) < uint64(count) {
		count = int(limit)
	}
	var out strings.Builder
	var digits [20]byte
	for n := range count {
		if n != 0 {
			out.WriteString(" <-- ")
		}
		frame := v.traceStack[len(v.traceStack)-1-n]
		out.WriteString(frame.storage)
		out.WriteByte('(')
		out.Write(strconv.AppendUint(digits[:0], frame.line, 10))
		out.WriteString(")[(")
		name := frame.functionName
		if frame.function {
			out.WriteString("function")
		} else {
			out.WriteString("top level script")
			name = "global"
		}
		out.WriteByte(')')
		if name != "" {
			out.WriteByte(' ')
			out.WriteString(name)
		}
		out.WriteByte(']')
	}
	return out.String()
}
