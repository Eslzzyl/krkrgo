package tjs

import "sync"

// NativeFunc is the small host-facing function ABI used by dispatch.  Context
// is the bound object (or the explicit objthis supplied to Call).
type NativeFunc func(context ObjectRef, args []Value) (Value, error)

// ValueFunc is useful for methods of primitive values, which do not have an
// ObjectID to use as context.
type ValueFunc func(context Value, args []Value) (Value, error)

type callableMeta struct {
	fn         Function
	call       NativeFunc
	valueCall  ValueFunc
	bound      ObjectRef
	boundValue *Value
	scope      *ObjectTable
}

type classMeta struct {
	definition Class
	object     *Object
	bases      []*classMeta
	scope      *ObjectTable
}

type propertyMeta struct {
	property Property
	scope    *ObjectTable
}

type instanceMeta struct {
	class *classMeta
	scope *ObjectTable
}

type dispatchRegistry struct {
	mu       sync.RWMutex
	callable map[ObjectID]*callableMeta
	classes  map[ObjectID]*classMeta
	objects  map[ObjectID]*instanceMeta
	props    map[ObjectID]*propertyMeta
}

var registry = dispatchRegistry{
	callable: make(map[ObjectID]*callableMeta),
	classes:  make(map[ObjectID]*classMeta),
	objects:  make(map[ObjectID]*instanceMeta),
	props:    make(map[ObjectID]*propertyMeta),
}

// dropIDs removes registry entries owned by scope for the object IDs that VM
// collection has just removed. Entries owned by other tables remain intact.
func (r *dispatchRegistry) dropIDs(scope *ObjectTable, ids []ObjectID) {
	if scope == nil || len(ids) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, id := range ids {
		if meta := r.callable[id]; meta != nil && meta.scope == scope {
			delete(r.callable, id)
		}
		if meta := r.classes[id]; meta != nil && meta.scope == scope {
			delete(r.classes, id)
		}
		if meta := r.objects[id]; meta != nil && (meta.scope == scope || (meta.class != nil && meta.class.scope == scope)) {
			delete(r.objects, id)
		}
		if meta := r.props[id]; meta != nil && meta.scope == scope {
			delete(r.props, id)
		}
	}
}

// scopeCount reports the number of registry entries owned by scope. It is used
// by lifetime tests and diagnostics.
func (r *dispatchRegistry) scopeCount(scope *ObjectTable) int {
	if scope == nil {
		return 0
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	count := 0
	for _, meta := range r.callable {
		if meta != nil && meta.scope == scope {
			count++
		}
	}
	for _, meta := range r.classes {
		if meta != nil && meta.scope == scope {
			count++
		}
	}
	for _, meta := range r.objects {
		if meta != nil && (meta.scope == scope || (meta.class != nil && meta.class.scope == scope)) {
			count++
		}
	}
	for _, meta := range r.props {
		if meta != nil && meta.scope == scope {
			count++
		}
	}
	return count
}

// dropScope releases every registry entry owned by scope.
func (r *dispatchRegistry) dropScope(scope *ObjectTable) {
	if scope == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, meta := range r.callable {
		if meta != nil && meta.scope == scope {
			delete(r.callable, id)
		}
	}
	for id, meta := range r.classes {
		if meta != nil && meta.scope == scope {
			delete(r.classes, id)
		}
	}
	for id, meta := range r.objects {
		if meta != nil && meta.scope == scope {
			delete(r.objects, id)
		}
	}
	for id, meta := range r.props {
		if meta != nil && meta.scope == scope {
			delete(r.props, id)
		}
	}
}

func newFunctionObject(table *ObjectTable, fn Function, call NativeFunc, valueCall ValueFunc, bound ObjectRef, boundValue *Value) *Object {
	var o *Object
	if table != nil {
		o = table.New(CategoryFunction)
	} else {
		o = NewObject(CategoryFunction)
	}
	registry.mu.Lock()
	registry.callable[o.ID] = &callableMeta{fn: fn, call: call, valueCall: valueCall, bound: bound, boundValue: boundValue, scope: table}
	registry.mu.Unlock()
	_ = o.Set("name", StringValue(fn.Name))
	_ = o.SetFlags("name", MemberReadOnly)
	return o
}

// FunctionValue turns a descriptor into a callable script value.  Programs
// are invoked by the VM boundary in invokeProgram; native functions may use
// NativeFunc directly.
func FunctionValue(fn Function) Value {
	return ObjectValue(NewObjectRef(newFunctionObject(nil, fn, nil, nil, ObjectRef{}, nil).ID))
}

// NewNativeFunction registers a host callable as a TJS function value.
func NewNativeFunction(table *ObjectTable, name string, fn NativeFunc) Value {
	o := newFunctionObject(table, Function{Name: name}, fn, nil, ObjectRef{}, nil)
	return ObjectValue(o.Ref())
}

// NewNativeFunctionForVM allocates a host callable in the VM heap so normal
// collection removes both the object and its dispatch entry.
func NewNativeFunctionForVM(vm *VM, name string, fn NativeFunc) Value {
	if vm == nil || vm.Objects == nil {
		return VoidValue
	}
	value, id, err := vm.alloc(CategoryFunction)
	if err != nil {
		return VoidValue
	}
	registry.mu.Lock()
	registry.callable[id] = &callableMeta{fn: Function{Name: name}, call: fn, scope: vm.Objects}
	registry.mu.Unlock()
	if object, ok := vm.Objects.Lookup(id); ok {
		_ = object.Set("name", StringValue(name))
		_ = object.SetFlags("name", MemberReadOnly)
	}
	return value
}

// Bind returns a closure with the supplied object as its this context.
func Bind(function Value, context ObjectRef) (Value, error) {
	if function.Kind() != KindObject {
		return Value{}, ErrType
	}
	registry.mu.RLock()
	meta := registry.callable[function.ObjectRef().Object]
	registry.mu.RUnlock()
	if meta == nil {
		return Value{}, ErrType
	}
	clone := newFunctionObject(nil, meta.fn, meta.call, meta.valueCall, context, meta.boundValue)
	return ObjectValue(clone.Ref(context.Object)), nil
}

// NewClass creates a class object.  Class descriptors remain value types and
// can safely be reused to create instances.
func NewClass(table *ObjectTable, definition Class) *Object {
	if table == nil {
		table = NewObjectTable()
	}
	classObject := table.New(CategoryClass)
	record := &classMeta{definition: definition.Clone(), object: classObject, scope: table}
	registry.mu.Lock()
	registry.classes[classObject.ID] = record
	registry.mu.Unlock()
	for _, method := range definition.Methods {
		fn := newFunctionObject(table, method, nil, nil, ObjectRef{}, nil)
		_ = classObject.Set(method.Name, ObjectValue(fn.Ref()))
	}
	for _, property := range definition.Properties {
		// Properties are descriptors, not values.  A marker function lets class
		// lookup find the descriptor without inventing another Value kind.
		marker := newFunctionObject(table, Function{Name: property.Name}, nil, nil, ObjectRef{}, nil)
		registry.mu.Lock()
		registry.props[marker.ID] = &propertyMeta{property: property.Clone(), scope: table}
		registry.mu.Unlock()
		_ = classObject.Set(property.Name, ObjectValue(marker.Ref()))
	}
	_ = classObject.Set("name", StringValue(definition.Name))
	_ = classObject.SetFlags("name", MemberReadOnly)
	return classObject
}

// NewClassObject is an explicit spelling for callers that prefer the C++
// terminology.
func NewClassObject(table *ObjectTable, definition Class) *Object { return NewClass(table, definition) }

// NewClassWithBases creates a class whose base identities are already
// materialized as class objects. It complements serialized Class.Bases, whose
// entries are concrete bytecode Programs.
func NewClassWithBases(table *ObjectTable, definition Class, bases ...*Object) *Object {
	object := NewClass(table, definition)
	registry.mu.Lock()
	record := registry.classes[object.ID]
	if record != nil {
		for _, base := range bases {
			if base != nil {
				if parent := registry.classes[base.ID]; parent != nil {
					record.bases = append(record.bases, parent)
				}
			}
		}
	}
	registry.mu.Unlock()
	return object
}

func classForObject(id ObjectID) *classMeta {
	registry.mu.RLock()
	c := registry.objects[id]
	registry.mu.RUnlock()
	if c == nil {
		return nil
	}
	return c.class
}

// NewInstance constructs an object from a class object and invokes its
// initializer.  Base initializers are called before the derived initializer.
func NewInstance(table *ObjectTable, classObject *Object, args ...Value) (*Object, error) {
	if classObject == nil {
		return nil, ErrType
	}
	registry.mu.RLock()
	c := registry.classes[classObject.ID]
	registry.mu.RUnlock()
	if c == nil {
		return nil, ErrType
	}
	if table == nil {
		table = NewObjectTable()
	}
	instance := table.New(CategoryInstance)
	registry.mu.Lock()
	registry.objects[instance.ID] = &instanceMeta{class: c, scope: table}
	registry.mu.Unlock()
	if err := constructClass(c, instance, args); err != nil {
		return nil, err
	}
	return instance, nil
}

func constructClass(c *classMeta, instance *Object, args []Value) error {
	for _, base := range c.bases {
		if err := constructClass(base, instance, args); err != nil {
			return err
		}
	}
	if len(c.definition.Initializer.Code) == 0 {
		return nil
	}
	return invokeProgram(c.definition.Initializer, instance.Ref(), args)
}

// IsInstanceOf implements TJS's class ancestry check.
func IsInstanceOf(instance Value, class *Object) bool {
	if instance.Kind() != KindObject || class == nil {
		return false
	}
	registry.mu.RLock()
	target := registry.classes[class.ID]
	got := registry.objects[instance.ObjectRef().Object]
	registry.mu.RUnlock()
	if target == nil || got == nil || got.class == nil {
		return false
	}
	return classIncludes(got.class, target)
}

func classIncludes(got, target *classMeta) bool {
	if got == target {
		return true
	}
	for _, base := range got.bases {
		if classIncludes(base, target) {
			return true
		}
	}
	return false
}

func lookupClassMember(c *classMeta, name string) (Value, *propertyMeta, bool) {
	if c == nil {
		return Value{}, nil, false
	}
	if v, ok := c.object.Get(name); ok {
		registry.mu.RLock()
		p := registry.props[v.ObjectRef().Object]
		registry.mu.RUnlock()
		return v, p, true
	}
	for _, base := range c.bases {
		if v, p, ok := lookupClassMember(base, name); ok {
			return v, p, true
		}
	}
	return Value{}, nil, false
}

func invokeProgram(program Program, context ObjectRef, args []Value) error {
	_, err := callProgram(program, context, args)
	return err
}

func callProgram(program Program, context ObjectRef, args []Value) (Value, error) {
	return executeProgram(program, context, args)
}

// InstanceOf is the short spelling used by callers implementing the TJS
// instanceof operator.
func InstanceOf(instance Value, class *Object) bool { return IsInstanceOf(instance, class) }

func (o *Object) IsInstanceOf(class *Object) bool {
	if o == nil {
		return false
	}
	return IsInstanceOf(ObjectValue(o.Ref()), class)
}

// DefineProperty attaches an accessor descriptor to an ordinary object.
func DefineProperty(table *ObjectTable, object *Object, property Property) error {
	if object == nil {
		return errInvalidObject
	}
	marker := newFunctionObject(table, Function{Name: property.Name}, nil, nil, ObjectRef{}, nil)
	registry.mu.Lock()
	registry.props[marker.ID] = &propertyMeta{property: property.Clone(), scope: table}
	registry.mu.Unlock()
	if err := object.Set(property.Name, ObjectValue(marker.Ref())); err != nil {
		return err
	}
	return object.SetFlags(property.Name, MemberDontDelete)
}
