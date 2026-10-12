package tjs

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
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

type vmObject struct {
	category          ObjectCategory
	native            string
	function          *Function
	legacyFunction    LegacyFunction
	legacyClassBody   LegacyFunction
	owner             ObjectID
	class             *Class
	classObject       ObjectID
	classBases        []ObjectID
	nativeClass       bool
	nativeConstructor ObjectID
	property          *Property
	array             []Value
	arrayLength       int64
	arrayMethods      map[string]Value
	dictionaryMethods map[string]Value
	stringMethods     map[string]Value
	methodContext     Value
	methodContextSet  bool
	superContext      ObjectID
	superOwner        ObjectID
	capturedScopes    []map[string]Value
	capturedWiths     []Value
	lexicalContext    ObjectID
	nativeStatic      bool
	finalizer         string
	finalizing        bool
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

const (
	maxVMArrayLength = 100000
	maxVMObjectCount = 100000
)

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

func rootErrorMessage(err error) string {
	for err != nil {
		cause := errors.Unwrap(err)
		if cause == nil {
			return err.Error()
		}
		err = cause
	}
	return ""
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
	pending        *vmCompletion
	functionName   string
	function       bool
}

type vmTraceFrame struct {
	storage      string
	line         uint64
	functionName string
	function     bool
	context      ObjectID
	owner        ObjectID
}

// vmFrameRoots captures references visible at an interpreter safe point, plus
// allocations still being assembled by the current multi-allocation opcode.
type vmFrameRoots struct {
	registers        []Value
	frame            *vmFrame
	host             Host
	trackAllocations bool
	instructionRoots []Value
}

// VM owns the script heap and the global namespace. Objects are retained in an
// explicit table; collection removes unreachable entries but IDs are allocated
// from the process-wide monotonic counter and are never reused.
type VM struct {
	mu                 sync.RWMutex
	Objects            *ObjectTable
	Globals            map[string]Value
	Executed           uint64
	allocationsSinceGC uint64
	collecting         bool
	gcSafeDepth        int
	depth              int
	roots              map[ObjectRef]struct{}
	frameRoots         []vmFrameRoots
	transientRoots     []Value
	meta               map[ObjectID]*vmObject
	literals           map[ObjectID]Value
	nativeSave         bool
	nativeSavePlugin   bool
	traceStack         []vmTraceFrame
	constructing       []ObjectID
}

func NewVM() *VM {
	table := NewObjectTable()
	global := &Object{ID: 0, Category: CategoryGlobal, members: make(map[string]member), valid: true}
	_ = table.Add(global)
	vm := &VM{Objects: table, Globals: make(map[string]Value), roots: make(map[ObjectRef]struct{}), meta: make(map[ObjectID]*vmObject), literals: make(map[ObjectID]Value)}
	vm.meta[0] = &vmObject{category: CategoryGlobal}
	for _, name := range []string{"Array", "Dictionary", "RegExp"} {
		_ = vm.RegisterNative(name)
	}
	array := vm.Globals["Array"]
	for _, method := range []string{"assign", "assignStruct", "split", "load", "find", "sort"} {
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
	_, _ = vm.RegisterNativeClass("Exception")
	_ = vm.RegisterSerialization(false)
	return vm
}
func NewVm() *VM                 { return NewVM() }
func (v *VM) globalValue() Value { return ObjectValue(NewObjectRef(0, 1)) }
func (v *VM) lookup(id ObjectID) (*Object, *vmObject, error) {
	if v == nil || v.Objects == nil {
		return nil, nil, errors.New("nil TJS VM")
	}
	o, ok := v.Objects.Lookup(id)
	if !ok {
		return nil, nil, fmt.Errorf("invalid TJS object handle: %d", id)
	}
	return o, v.meta[id], nil
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
	if v.depth > 0 && !v.collecting && (v.allocationsSinceGC >= vmGCAllocationThreshold || len(v.meta) >= maxVMObjectCount) {
		if len(v.frameRoots) != v.depth {
			return VoidValue, 0, vmAbortError(errors.New("TJS allocation reached an incomplete collection safe point"))
		}
		if _, err := v.collectAtSafePoint(v.frameRoots[len(v.frameRoots)-1].host); err != nil {
			return VoidValue, 0, vmAbortError(err)
		}
	}
	if len(v.meta) >= maxVMObjectCount {
		return VoidValue, 0, vmAbortError(errors.New("TJS object allocation limit exceeded"))
	}
	o := v.Objects.New(category)
	id := o.ID
	v.meta[id] = &vmObject{category: category}
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
func (v *VM) ObjectsSize() int {
	if v == nil || v.Objects == nil {
		return 0
	}
	return len(v.meta)
}
func (v *VM) allIDs() []ObjectID {
	if v == nil || v.Objects == nil {
		return nil
	} // ObjectTable intentionally has no iterator; metadata includes all VM allocations.
	out := make([]ObjectID, 0, len(v.meta))
	for id := range v.meta {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
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
		return e
	}
	return o.SetUnits(memberKeyUnits(key), val)
}

func (v *VM) setMemberString(id ObjectID, name string, val Value) error {
	o, e := v.obj(id)
	if e != nil {
		return e
	}
	return o.SetString(name, val)
}
func (v *VM) lookupClassMember(classID ObjectID, name string, depth int, skipStatic bool) (Value, bool, error) {
	if depth >= 128 {
		return VoidValue, false, errors.New("class inheritance depth exceeded")
	}
	classObject, err := v.obj(classID)
	if err != nil {
		return VoidValue, false, err
	}
	if value, ok := classObject.Get(name); ok {
		if !skipStatic {
			return value, true, nil
		}
		if memberID, memberErr := v.handle(value); memberErr != nil || v.meta[memberID] == nil || !v.meta[memberID].nativeStatic {
			return value, true, nil
		}
	}
	meta := v.meta[classID]
	if meta == nil {
		return VoidValue, false, nil
	}
	for i := len(meta.classBases) - 1; i >= 0; i-- {
		value, ok, err := v.lookupClassMember(meta.classBases[i], name, depth+1, skipStatic)
		if err != nil || ok {
			return value, ok, err
		}
	}
	return VoidValue, false, nil
}

func (v *VM) bindClassMember(value Value, context ObjectID) Value {
	if value.Kind() != KindObject || value.ObjectRef().Context != 0 {
		return value
	}
	return ObjectValue(NewObjectRef(value.ObjectRef().Object, context))
}
func (v *VM) linkClassBase(receiver Value, baseID ObjectID) {
	if receiver.Kind() != KindObject || receiver.IsNull() {
		return
	}
	meta := v.meta[receiver.ObjectRef().Object]
	if meta != nil && meta.category == CategoryInstance {
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
	o, e := v.obj(id)
	if e != nil {
		return VoidValue, false, e
	}
	if x, ok := o.GetUnits(memberKeyUnits(key)); ok {
		return x, true, nil
	}
	return v.getMemberFallback(id, key.Text())
}

func (v *VM) getMemberString(id ObjectID, name string) (Value, bool, error) {
	o, e := v.obj(id)
	if e != nil {
		return VoidValue, false, e
	}
	if x, ok := o.GetString(name); ok {
		return x, true, nil
	}
	return v.getMemberFallback(id, name)
}

func (v *VM) getMemberFallback(id ObjectID, name string) (Value, bool, error) {
	if id == 0 {
		if x, ok := v.Globals[name]; ok {
			return x, true, nil
		}
	}
	meta := v.meta[id]
	if meta == nil {
		return VoidValue, false, nil
	}
	if meta.superOwner != 0 {
		if _, err := v.obj(meta.superOwner); err != nil {
			return VoidValue, false, err
		}
		ownerMeta := v.meta[meta.superOwner]
		if ownerMeta != nil {
			for i := len(ownerMeta.classBases) - 1; i >= 0; i-- {
				if value, ok, lookupErr := v.lookupClassMember(ownerMeta.classBases[i], name, 0, false); lookupErr != nil {
					return VoidValue, false, lookupErr
				} else if ok {
					return v.bindClassMember(value, meta.superContext), true, nil
				}
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
					return v.bindClassMember(initializer, meta.superContext), true, nil
				}
				if baseMeta.nativeConstructor != 0 {
					return ObjectValue(NewObjectRef(baseMeta.nativeConstructor, meta.superContext)), true, nil
				}
			}
		}
		return VoidValue, false, nil
	}
	if meta.classObject != 0 {
		value, ok, err := v.lookupClassMember(meta.classObject, name, 0, meta.category == CategoryInstance)
		if err != nil || !ok {
			return value, ok, err
		}
		if meta.category == CategoryInstance {
			value = v.bindClassMember(value, id)
		}
		return value, true, nil
	}
	if meta.category == CategoryClass {
		return v.lookupClassMember(id, name, 0, false)
	}
	if meta.category == CategoryDictionary {
		return v.vmDictionaryMethod(id, name)
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
	member, ok, err := v.getMember(id, key)
	if err != nil {
		return VoidValue, false, err
	}
	if !ok {
		member, ok, err = v.vmRegexpMethod(id, name)
		if err != nil || !ok {
			return VoidValue, ok, err
		}
	}
	value, err := v.getValue(receiver, member, false, host, budget)
	return value, true, err
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
	id, e := v.handle(value)
	if e != nil {
		return e
	}
	m := v.meta[id]
	if m == nil || m.category != CategoryArray {
		return errors.New("Array native instance required")
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

// RegisterLegacyClass creates a class object for a compiled TJS2100 class.
// Unlike a native class, its constructor is represented by compiled members.
func (v *VM) RegisterLegacyClass(name string) (Value, error) {
	value, id, err := v.alloc(CategoryClass)
	if err != nil {
		return VoidValue, err
	}
	v.meta[id].class = &Class{Name: name}
	v.meta[id].classObject = id
	v.Globals[name] = value
	v.roots[value.ObjectRef()] = struct{}{}
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
		if ref.Context != 0 {
			return ObjectValue(NewObjectRef(ref.Context, ref.Context))
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
	// must not replace them.
	if x, ok := f.parameters[name]; ok {
		return x, nil
	}
	for i := len(f.scopes) - 1; i >= 0; i-- {
		if x, ok := f.scopes[i][name]; ok {
			return x, nil
		}
	}
	for i := len(f.withs) - 1; i >= 0; i-- {
		receiver := f.withs[i]
		id, e := v.handle(receiver)
		if e != nil {
			return VoidValue, e
		}
		x, ok, e := v.getMemberString(id, name)
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
			if meta := v.meta[id]; meta != nil && meta.category == CategoryClass && meta.class != nil && meta.class.Name == name {
				return receiver, nil
			}
			return v.getValue(receiver, x, raw, h, b)
		}
	}
	if f.lexicalContext != 0 && f.lexicalContext != f.context {
		x, ok, e := v.getMemberString(f.lexicalContext, name)
		if e != nil {
			return VoidValue, e
		}
		if !ok {
			x, ok, e = v.vmArrayMethod(f.lexicalContext, name)
			if e != nil {
				return VoidValue, e
			}
		}
		if ok {
			return v.getValue(ObjectValue(NewObjectRef(f.lexicalContext, f.lexicalContext)), x, raw, h, b)
		}
	}
	if f.context != 0 {
		x, ok, e := v.getMemberString(f.context, name)
		if e != nil {
			return VoidValue, e
		}
		if !ok {
			x, ok, e = v.vmArrayMethod(f.context, name)
			if e != nil {
				return VoidValue, e
			}
		}
		if ok {
			return v.getValue(ObjectValue(NewObjectRef(f.context, f.context)), x, raw, h, b)
		}
	}
	if x, ok, e := v.getMemberString(0, name); e != nil {
		return VoidValue, e
	} else if ok {
		return v.getValue(v.globalValue(), x, raw, h, b)
	}
	if x, ok := v.Globals[name]; ok {
		return v.getValue(v.globalValue(), x, raw, h, b)
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
func (v *VM) storeName(f *vmFrame, name string, x Value, declare, raw, declareWith bool, h Host, b *uint64) error {
	if declare {
		for i := len(f.withs) - 1; i >= 0; i-- {
			receiver := f.withs[i]
			id, err := v.handle(receiver)
			if err != nil {
				return err
			}
			member, ok, err := v.getMemberString(id, name)
			if err != nil {
				return err
			}
			if !ok {
				continue
			}
			if !declareWith {
				// An uninitialized var declaration does not assign to a with
				// member or introduce a local that masks it.
				return nil
			}
			if memberID, err := v.handle(member); !raw && err == nil && v.meta[memberID] != nil && v.meta[memberID].category == CategoryProperty {
				return v.writeProperty(member, receiver, x, h, b)
			}
			return v.setMemberString(id, name, x)
		}
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
	for i := len(f.withs) - 1; i >= 0; i-- {
		receiver := f.withs[i]
		id, err := v.handle(receiver)
		if err != nil {
			return err
		}
		member, ok, err := v.getMemberString(id, name)
		if err != nil {
			return err
		}
		if ok {
			if memberID, err := v.handle(member); !raw && err == nil && v.meta[memberID] != nil && v.meta[memberID].category == CategoryProperty {
				return v.writeProperty(member, receiver, x, h, b)
			}
			return v.setMemberString(id, name, x)
		}
	}
	receiver := v.globalValue()
	id := ObjectID(0)
	if f.context != 0 {
		id = f.context
		receiver = ObjectValue(NewObjectRef(id))
	}
	if member, ok, err := v.getMemberString(id, name); err != nil {
		return err
	} else if ok {
		if memberID, err := v.handle(member); !raw && err == nil && v.meta[memberID] != nil && v.meta[memberID].category == CategoryProperty {
			return v.writeProperty(member, receiver, x, h, b)
		}
		return v.setMemberString(id, name, x)
	}
	if f.context != 0 {
		if member, ok, err := v.getMemberString(0, name); err != nil {
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
	}
	return x, nil
}
func (v *VM) invokePropertyFunction(function *Function, context Value, args []Value, owner ObjectID, h Host, b *uint64) (Value, error) {
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
	frame := vmFrame{
		context:      id,
		owner:        owner,
		args:         args,
		scopes:       []map[string]Value{make(map[string]Value)},
		functionName: function.Name,
		function:     true,
	}
	return v.run(&function.Program, h, b, frame)
}

func (v *VM) getProperty(id ObjectID, receiver Value, h Host, b *uint64) (Value, error) {
	m := v.meta[id]
	if m == nil || m.property == nil || m.property.Getter == nil {
		return VoidValue, nil
	}
	return v.invokePropertyFunction(m.property.Getter, receiver, nil, m.owner, h, b)
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

func vmStringProperty(value, key Value) (Value, bool) {
	if key.Kind() == KindString && key.Text() == "length" {
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
	if ref.Context != 0 {
		ref.Object = ref.Context
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

func (v *VM) callArrayMethod(context Value, name string, args []Value, h Host, b *uint64) (Value, error) {
	id := context.ObjectRef().Object
	m := v.meta[id]
	if m == nil || m.category != CategoryArray {
		return VoidValue, errors.New("TJS array method requires an array")
	}
	defer func() { m.arrayLength = int64(len(m.array)) }()
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
		if err := SortValues(m.array, mode, stable, compare); err != nil {
			return VoidValue, err
		}
		return VoidValue, nil
	case "assign":
		if len(args) == 0 {
			return VoidValue, ErrBadParamCount
		}
		m.array = nil
		if err := v.assignVMArrayItems(m, args[0]); err != nil {
			return VoidValue, err
		}
		return VoidValue, nil
	case "split":
		if len(args) < 2 {
			return VoidValue, ErrBadParamCount
		}
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
		m.array = append(m.array, args...)
		return IntegerValue(int64(len(m.array))), nil
	case "unshift":
		if len(args) > maxVMArrayLength-len(m.array) {
			return VoidValue, errors.New("TJS array length limit exceeded")
		}
		if len(args) > 0 {
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
			for _, item := range m.array {
				if item.StrictEqual(args[0]) {
					removed++
					continue
				}
				kept = append(kept, item)
			}
			clear(m.array[len(kept):])
			m.array = kept
		} else {
			for i, item := range m.array {
				if item.StrictEqual(args[0]) {
					copy(m.array[i:], m.array[i+1:])
					last := len(m.array) - 1
					clear(m.array[last:])
					m.array = m.array[:last]
					removed = 1
					break
				}
			}
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
		m.array = append(m.array, VoidValue)
		copy(m.array[i+1:], m.array[i:len(m.array)-1])
		m.array[i] = args[1]
		return VoidValue, nil
	case "pop":
		if len(m.array) == 0 {
			return VoidValue, nil
		}
		last := len(m.array) - 1
		out := m.array[last]
		m.array = m.array[:last]
		return out, nil
	case "shift":
		if len(m.array) == 0 {
			return VoidValue, nil
		}
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
		for i, j := 0, len(m.array)-1; i < j; i, j = i+1, j-1 {
			m.array[i], m.array[j] = m.array[j], m.array[i]
		}
		return context, nil
	case "clear":
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
	r := make([]Value, p.Registers)
	rootDepth := len(v.frameRoots)
	v.frameRoots = append(v.frameRoots, vmFrameRoots{registers: r, frame: &f, host: h})
	defer func() {
		v.frameRoots[rootDepth] = vmFrameRoots{}
		v.frameRoots = v.frameRoots[:rootDepth]
	}()
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
		ins := p.Code[ip]
		trace := &v.traceStack[traceFrame]
		trace.storage = ins.Location.Storage
		trace.line = ins.Location.Line
		if b == nil || *b == 0 {
			return VoidValue, vmAbortError(fmt.Errorf("%s:%d:%d: execution budget exhausted after %d instructions", ins.Location.Storage, ins.Location.Line, ins.Location.Column, v.Executed))
		}
		*b--
		v.Executed++
		if t, ok := h.(HostTrace); ok {
			if e := t.Trace(&ins); e != nil {
				return VoidValue, vmAbortError(e)
			}
		}
		at := ip
		ip++
		var out Value
		var returned bool
		var e error
		op := ins.Op
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
					e = v.storeName(&f, op.Name, x, true, false, false, h, b)
					if e == nil {
						if f.parameters == nil {
							f.parameters = make(map[string]Value)
						}
						f.parameters[op.Name] = x
					}
				}
			}
		case OpConstant:
			put(op.Out, op.Value)
		case OpConstantObject:
			x, e := v.materialize(op.Literal, b)
			if e == nil && op.Identity != 0 {
				if old, ok := v.literals[op.Identity]; ok {
					x = old
				} else {
					v.literals[op.Identity] = x
				}
			}
			put(op.Out, x)
		case OpLoad:
			var x Value
			x, e = v.loadName(&f, op.Name, op.Optional, op.Raw, h, b)
			put(op.Out, x)
		case OpDeclare:
			e = v.storeName(&f, op.Name, reg(op.Input), true, false, op.DeclareWith, h, b)
		case OpStore:
			e = v.storeName(&f, op.Name, reg(op.Input), false, op.Raw, false, h, b)
		case OpMove:
			put(op.Out, reg(op.Input))
		case OpDeleteName:
			var ok bool
			ok, e = v.deleteName(&f, op.Name)
			if e == nil {
				put(op.Out, boolValue(ok))
			}
		case OpEnsureMember:
			id, er := v.handle(reg(op.Object))
			if er != nil {
				e = er
			} else {
				_, exists, er := v.getMember(id, reg(op.Key))
				if er != nil {
					e = er
				} else if !exists {
					e = v.setMember(id, reg(op.Key), VoidValue)
				}
			}
		case OpDeleteMember:
			var id ObjectID
			id, e = v.handle(reg(op.Object))
			if e == nil {
				var ok bool
				ok, e = v.deleteMember(id, reg(op.Key))
				put(op.Out, boolValue(ok))
			}
		case OpUnary:
			if strings.EqualFold(op.Operator, "isvalid") {
				put(op.Out, v.IsValid(reg(op.Input)))
			} else if strings.EqualFold(op.Operator, "invalidate") {
				x, er := v.Invalidate(reg(op.Input), h, b)
				e = er
				put(op.Out, x)
			} else {
				x, er := reg(op.Input).Unary(op.Operator)
				e = er
				put(op.Out, x)
			}
		case OpBinary:
			if op.Operator == "incontextof" {
				left, right := reg(op.Left), reg(op.Right)
				bound, ok, er := v.bindStringMethodContext(left, right)
				if er != nil {
					e = er
				} else if ok {
					put(op.Out, bound)
				} else {
					a, er := v.handle(left)
					if er != nil {
						e = er
					} else if right.IsNull() {
						put(op.Out, ObjectValue(NewObjectRef(a)))
					} else {
						c, er := v.handle(right)
						if er != nil {
							e = er
						} else {
							if context := right.ObjectRef().Context; context != 0 {
								c = context
							}
							put(op.Out, ObjectValue(NewObjectRef(a, c)))
						}
					}
				}
			} else if op.Operator == "instanceof" {
				x, er := v.instanceOfValue(reg(op.Left), reg(op.Right))
				e = er
				put(op.Out, x)
			} else {
				x, er := reg(op.Left).Binary(op.Operator, reg(op.Right))
				e = er
				put(op.Out, x)
			}

		case OpGet:
			receiver := reg(op.Object)
			key := reg(op.Key)
			if receiver.Kind() == KindString {
				if property, ok := vmStringProperty(receiver, key); ok {
					put(op.Out, property)
				} else {
					method, ok, er := v.vmStringMethod(key.Text())
					e = er
					if e == nil && ok {
						put(op.Out, method)
					} else if !ok && !op.Optional && e == nil {
						e = fmt.Errorf("member not found: %s", key.Text())
					} else if e == nil {
						put(op.Out, VoidValue)
					}
				}
				break
			}
			id, er := v.handle(receiver)
			if er != nil {
				if op.Optional {
					put(op.Out, VoidValue)
				} else {
					e = er
				}
			} else {
				if m := v.meta[id]; m != nil && m.category == CategoryArray {
					if x, ok := vmArrayProperty(m, key); ok {
						put(op.Out, x)
					} else if _, indexed := vmArrayIndex(key); indexed {
						put(op.Out, VoidValue)
					} else {
						x, ok, er := v.getMember(id, key)
						e = er
						if e == nil && !ok {
							x, ok, e = v.vmArrayMethod(id, key.Text())
						}
						if e == nil && ok {
							x, e = v.getValue(receiver, x, op.Raw, h, b)
							put(op.Out, x)
						} else if !ok && !op.Optional && e == nil {
							e = fmt.Errorf("member not found: %s", key.Text())
						} else if e == nil {
							put(op.Out, VoidValue)
						}
					}
				} else {
					meta := v.meta[id]
					missingIsVoid := meta != nil && meta.category == CategoryDictionary && meta.classObject == 0 && meta.superOwner == 0
					x, ok, er := v.getMember(id, key)
					e = er
					if e == nil && !ok {
						x, ok, e = v.vmRegexpMethod(id, key.Text())
					}
					if e == nil && ok {
						x, e = v.getValue(receiver, x, op.Raw, h, b)
						put(op.Out, x)
					} else if !ok && !op.Optional && e == nil && !missingIsVoid {
						e = fmt.Errorf("member not found: %s", key.Text())
					} else if e == nil {
						put(op.Out, VoidValue)
					}
				}
			}
		case OpTypeOfMember:
			receiver := reg(op.Object)
			key := reg(op.Key)
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
							x, ok, e = v.getMember(id, key)
							if e == nil && !ok {
								x, ok, e = v.vmArrayMethod(id, key.Text())
							}
						}
					} else {
						x, ok, e = v.getMember(id, key)
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
			receiver := reg(op.Object)
			id, er := v.handle(receiver)
			if er != nil {
				e = er
			} else if m := v.meta[id]; m != nil && m.category == CategoryArray {
				key := reg(op.Key)
				if key.Kind() == KindString && (key.Text() == "count" || key.Text() == "length") {
					e = setVMArrayLength(m, reg(op.Input))
				} else if n, indexed := vmArrayIndex(key); indexed {
					if n >= int64(maxVMArrayLength) {
						e = errors.New("TJS array length limit exceeded")
					} else {
						index := int(n)
						if index >= len(m.array) {
							m.array = append(m.array, make([]Value, index+1-len(m.array))...)
						}
						m.array[index] = reg(op.Input)
						if n >= m.arrayLength {
							m.arrayLength = n + 1
						}
					}
				} else {
					x, ok, er := v.getMember(id, key)
					if er != nil {
						e = er
					} else if ok {
						if propertyID, er := v.handle(x); !op.Raw && er == nil && v.meta[propertyID] != nil && v.meta[propertyID].category == CategoryProperty {
							e = v.writeProperty(x, reg(op.Object), reg(op.Input), h, b)
						} else {
							e = v.setMember(id, key, reg(op.Input))
						}
					} else {
						e = v.setMember(id, key, reg(op.Input))
					}
				}
			} else {
				key := reg(op.Key)
				x, ok, er := v.getMember(id, key)
				if er != nil {
					e = er
				} else if ok {
					if propertyID, er := v.handle(x); !op.Raw && er == nil && v.meta[propertyID] != nil && v.meta[propertyID].category == CategoryProperty {
						e = v.writeProperty(x, reg(op.Object), reg(op.Input), h, b)
					} else {
						e = v.setMember(id, key, reg(op.Input))
					}
				} else {
					e = v.setMember(id, key, reg(op.Input))
				}
			}
		case OpArray:
			items := make([]Value, len(op.Items))
			for i, x := range op.Items {
				items[i] = reg(x)
			}
			x, er := v.NewArray(items)
			e = er
			put(op.Out, x)
		case OpDictionary:
			x, er := v.NewDictionary()
			e = er
			if e == nil {
				for _, pair := range op.Pairs {
					value := reg(pair.Value)
					e = v.setMember(x.ObjectRef().Object, reg(pair.Key), value)
					if e != nil {
						break
					}
				}
			}
			put(op.Out, x)
		case OpFunction:
			if op.Function == nil {
				e = errors.New("nil function descriptor")
			} else {
				x, id, er := v.alloc(CategoryFunction)
				e = er
				if e == nil {
					v.meta[id].function = op.Function
					v.meta[id].capturedScopes = append([]map[string]Value(nil), f.scopes...)
					v.meta[id].capturedWiths = append([]Value(nil), f.withs...)
					v.meta[id].lexicalContext = f.lexicalContext
					if v.meta[id].lexicalContext == 0 {
						v.meta[id].lexicalContext = f.context
					}
					put(op.Out, x)
				}
			}
		case OpCall:
			args, er := v.expandArgs(op.Args, r, f.args)
			e = er
			if e == nil {
				ctx := ObjectValue(NewObjectRef(f.context))
				if op.ContextSet {
					ctx = reg(op.Context)
				}
				callee := reg(op.Callee)
				x, er := v.invokeOwned(callee, ctx, args, h, b, op.ResultNeeded)
				e = er
				put(op.Out, x)
			}
		case OpConstruct:
			args, er := v.expandArgs(op.Args, r, f.args)
			e = er
			if e == nil {
				var x Value
				if op.TargetSet {
					x, er = v.constructForMemberAssignment(reg(op.Callee), args, reg(op.Object), reg(op.Key), h, b)
				} else {
					x, er = v.construct(reg(op.Callee), args, h, b)
				}
				e = er
				put(op.Out, x)
			}
		case OpClass:
			if op.Class == nil {
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
			definition := op.Class.Clone()
			meta.class = &definition
			meta.classObject = id
			for _, baseRegister := range op.Items {
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
				method, methodID, allocErr := v.alloc(CategoryFunction)
				if allocErr != nil {
					e = allocErr
					break
				}
				v.meta[methodID].function = &function
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
			if op.Property == nil {
				e = errors.New("nil property descriptor")
			} else {
				x, id, er := v.alloc(CategoryProperty)
				e = er
				if e == nil {
					v.meta[id].property = op.Property
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
				exception, exceptionErr := v.exceptionValue(rootErrorMessage(e), traceErr.Error(), h, b)
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
func (v *VM) expandArgs(spec []Argument, regs []Value, forward []Value) ([]Value, error) {
	out := make([]Value, 0, len(spec))
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

func (v *VM) classHasBaseNamed(classID ObjectID, name string, depth int) bool {
	if depth >= 128 {
		return false
	}
	meta := v.meta[classID]
	if meta == nil {
		return false
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
		// Dispatcher-created array methods normally run through the object
		// dispatcher. Array.load is VM-host-backed, so route that exact native
		// member through the same host path as source-compiled Array.load.
		registry.mu.RLock()
		callable := registry.callable[id]
		registry.mu.RUnlock()
		if callable != nil && callable.fn.Name == "load" && context.Kind() == KindObject && !context.IsNull() {
			if receiver, ok := v.Objects.Lookup(context.ObjectRef().Object); ok && receiver.Category == CategoryArray {
				return v.nativeCall("Array.load", context, args, h, b, needed)
			}
		}
		dispatcher := NewDispatcher(v.Objects)
		return dispatcher.CallWithContext(callee, context.ObjectRef(), args...)
	}
	callee = v.bindConstructingBaseConstructor(callee)
	if context.Kind() == KindObject && !context.IsNull() {
		if proxy := v.meta[context.ObjectRef().Object]; proxy != nil && proxy.superOwner != 0 && proxy.superContext != 0 {
			context = ObjectValue(NewObjectRef(proxy.superContext, proxy.superContext))
		}
	}
	if (m.category == CategoryNative || m.category == CategoryMethod) && callee.ObjectRef().Context != 0 {
		context = ObjectValue(NewObjectRef(callee.ObjectRef().Context, callee.ObjectRef().Context))
	}
	if m.category == CategoryMethod && m.methodContextSet && callee.ObjectRef().Context == 0 {
		context = m.methodContext
	}
	switch m.category {
	case CategoryNative:
		return v.nativeCall(m.native, context, args, h, b, needed)
	case CategoryFunction:
		if m.legacyFunction != nil {
			if callee.ObjectRef().Context != 0 {
				context = ObjectValue(NewObjectRef(callee.ObjectRef().Context, callee.ObjectRef().Context))
			}
			return m.legacyFunction(context, args, h, b, needed)
		}
		if m.function == nil {
			return VoidValue, errors.New("invalid TJS function")
		}
		c := context
		if callee.ObjectRef().Context != 0 {
			c = ObjectValue(NewObjectRef(callee.ObjectRef().Context, callee.ObjectRef().Context))
		}
		cid, _ := v.handle(c)
		scopes := append([]map[string]Value(nil), m.capturedScopes...)
		local := make(map[string]Value)
		scopes = append(scopes, local)
		if m.function.Name != "" {
			local[m.function.Name] = callee
		}
		if !owned {
			args = append([]Value(nil), args...)
		}
		f := vmFrame{context: cid, lexicalContext: m.lexicalContext, args: args, owner: m.owner, scopes: scopes, withs: append([]Value(nil), m.capturedWiths...), functionName: m.function.Name, function: true}
		return v.run(&m.function.Program, h, b, f)
	case CategoryClass:
		v.linkClassBase(context, id)
		if m.legacyClassBody != nil {
			return m.legacyClassBody(context, args, h, b, needed)
		}
		if m.nativeClass && context.Kind() == KindObject && !context.IsNull() {
			return VoidValue, nil
		}
	case CategoryMethod:
		return v.nativeCall(m.native, context, args, h, b, needed)
	case CategoryProperty:
		return v.getProperty(id, context, h, b)
	}
	return VoidValue, errors.New("TJS object is not callable")
}
func (v *VM) initializeClass(classID, instance ObjectID, args []Value, h Host, b *uint64, depth int) error {
	if depth >= 128 {
		return errors.New("class inheritance depth exceeded")
	}
	if err := v.validID(classID); err != nil {
		return err
	}
	class := v.meta[classID]
	if class == nil || class.category != CategoryClass {
		return errors.New("TJS class base must be a class")
	}
	for _, base := range class.classBases {
		if err := v.initializeClass(base, instance, nil, h, b, depth+1); err != nil {
			return err
		}
	}
	if names, registered := v.Objects.ClassInstanceMembers(classID); registered {
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
		return err
	}
	return nil
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
	instance, instanceID, err := v.alloc(CategoryInstance)
	if err != nil {
		return VoidValue, err
	}
	v.transientRoots = append(v.transientRoots, instance)
	constructionDepth := len(v.constructing)
	v.constructing = append(v.constructing, instanceID)
	defer func() { v.constructing = v.constructing[:constructionDepth] }()
	v.meta[instanceID].class = meta.class
	v.meta[instanceID].classObject = id
	if publishTarget {
		targetID, targetErr := v.handle(targetObject)
		if targetErr != nil {
			return VoidValue, targetErr
		}
		if err = v.setMember(targetID, targetKey, instance); err != nil {
			return VoidValue, err
		}
	}
	if err = v.initializeClass(id, instanceID, args, h, b, 0); err != nil {
		return VoidValue, err
	}
	if meta.class != nil && meta.class.Name != "" {
		if constructor, ok, lookupErr := v.lookupClassMember(id, meta.class.Name, 0, false); lookupErr != nil {
			return VoidValue, lookupErr
		} else if ok {
			constructor = v.bindClassMember(constructor, instanceID)
			if _, err = v.invoke(constructor, instance, args, h, b, true); err != nil {
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
	if invalidator, ok, lookupErr := v.getMember(id, StringValue("@invalidate")); lookupErr != nil {
		return VoidValue, lookupErr
	} else if ok {
		if invalidatorID, invalidatorErr := v.handle(invalidator); invalidatorErr == nil && v.meta[invalidatorID] != nil && v.meta[invalidatorID].category == CategoryNative {
			v.meta[instanceID].finalizer = v.meta[invalidatorID].native
		}
	}
	return instance, nil
}

func (v *VM) callStringMethod(value Value, name string, args []Value) (Value, error) {
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
		return replaceStringValue(value, args)
	default:
		return Value{}, ErrMemberNotFound
	}
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
			if name == "match" {
				return v.NewArray(nil)
			}
			return NullValue, nil
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
	if strings.HasPrefix(name, "Serialization.") {
		return v.serializationCall(strings.TrimPrefix(name, "Serialization."), context, args, h, b, needed)
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
		return v.callStringMethod(context, strings.TrimPrefix(name, "String."), args)
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
	if m == nil || m.property == nil || m.property.Getter == nil {
		return VoidValue, nil
	}
	context = v.propertyReferenceContext(property, context)
	return v.invokePropertyFunction(m.property.Getter, context, nil, m.owner, h, b)
}
func (v *VM) writeProperty(property, context, input Value, h Host, b *uint64) error {
	id, e := v.handle(property)
	if e != nil {
		return e
	}
	m := v.meta[id]
	if m == nil || m.property == nil || m.property.Setter == nil {
		return nil
	}
	context = v.propertyReferenceContext(property, context)
	_, e = v.invokePropertyFunction(m.property.Setter, context, []Value{input}, m.owner, h, b)
	return e
}
func (v *VM) propertyReferenceContext(property, context Value) Value {
	if property.Kind() == KindObject && !property.IsNull() {
		if bound := property.ObjectRef().Context; bound != 0 {
			return ObjectValue(NewObjectRef(bound, bound))
		}
	}
	return context
}
func (v *VM) materialize(l Literal, b *uint64) (Value, error) {
	if e := l.Validate(); e != nil {
		return VoidValue, e
	}
	switch l.Kind() {
	case LiteralValueKind:
		x, _ := l.Value()
		return x, nil
	case LiteralArrayKind:
		xs := l.Array()
		a := make([]Value, len(xs))
		for i := range xs {
			x, e := v.materialize(xs[i], b)
			if e != nil {
				return VoidValue, e
			}
			a[i] = x
		}
		return v.NewArray(a)
	case LiteralDictionaryKind:
		x, e := v.NewDictionary()
		if e != nil {
			return VoidValue, e
		}
		for _, p := range l.Dictionary() {
			k, e := v.materialize(p.Key, b)
			if e != nil {
				return VoidValue, e
			}
			y, e := v.materialize(p.Value, b)
			if e != nil {
				return VoidValue, e
			}
			if e = v.setMember(x.ObjectRef().Object, k, y); e != nil {
				return VoidValue, e
			}
		}
		return x, nil
	}
	return VoidValue, errors.New("invalid TJS literal")
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
	return v.classNames(instance.classObject, 0)
}

func (v *VM) classNames(classID ObjectID, depth int) ([]string, error) {
	if depth >= 128 {
		return nil, errors.New("class inheritance depth exceeded")
	}
	meta := v.meta[classID]
	if meta == nil || meta.category != CategoryClass {
		return nil, errors.New("invalid TJS class handle")
	}
	var names []string
	if meta.class != nil && meta.class.Name != "" {
		names = append(names, meta.class.Name)
	}
	for _, base := range meta.classBases {
		baseNames, err := v.classNames(base, depth+1)
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
