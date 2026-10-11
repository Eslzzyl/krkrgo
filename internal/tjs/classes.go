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
}

type classMeta struct {
	definition Class
	object     *Object
	bases      []*classMeta
}

type propertyMeta struct{ property Property }

type dispatchRegistry struct {
	mu             sync.RWMutex
	callable       map[ObjectID]*callableMeta
	classes        map[ObjectID]*classMeta
	objects        map[ObjectID]*classMeta
	props          map[ObjectID]*propertyMeta
	programClasses map[string]*classMeta
}

var registry = dispatchRegistry{
	callable:       make(map[ObjectID]*callableMeta),
	classes:        make(map[ObjectID]*classMeta),
	objects:        make(map[ObjectID]*classMeta),
	props:          make(map[ObjectID]*propertyMeta),
	programClasses: make(map[string]*classMeta),
}

func newFunctionObject(table *ObjectTable, fn Function, call NativeFunc, valueCall ValueFunc, bound ObjectRef, boundValue *Value) *Object {
	var o *Object
	if table != nil {
		o = table.New(CategoryFunction)
	} else {
		o = NewObject(CategoryFunction)
	}
	registry.mu.Lock()
	registry.callable[o.ID] = &callableMeta{fn: fn, call: call, valueCall: valueCall, bound: bound, boundValue: boundValue}
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
	record := &classMeta{definition: definition.Clone(), object: classObject}
	registry.mu.Lock()
	registry.classes[classObject.ID] = record
	registry.programClasses[definition.Name] = record
	registry.mu.Unlock()
	for _, base := range definition.Bases {
		if b := classFromProgram(base); b != nil {
			record.bases = append(record.bases, b)
		}
	}
	for _, method := range definition.Methods {
		fn := newFunctionObject(table, method, nil, nil, ObjectRef{}, nil)
		_ = classObject.Set(method.Name, ObjectValue(fn.Ref()))
	}
	for _, property := range definition.Properties {
		// Properties are descriptors, not values.  A marker function lets class
		// lookup find the descriptor without inventing another Value kind.
		marker := newFunctionObject(table, Function{Name: property.Name}, nil, nil, ObjectRef{}, nil)
		registry.mu.Lock()
		registry.props[marker.ID] = &propertyMeta{property: property.Clone()}
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
	for _, base := range bases {
		if base != nil {
			if parent := registry.classes[base.ID]; parent != nil {
				record.bases = append(record.bases, parent)
			}
		}
	}
	registry.mu.Unlock()
	return object
}

func classFromProgram(program Program) *classMeta {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	return registry.programClasses[program.Storage]
}

func classForObject(id ObjectID) *classMeta {
	registry.mu.RLock()
	c := registry.objects[id]
	registry.mu.RUnlock()
	return c
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
	registry.objects[instance.ID] = c
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
	if target == nil || got == nil {
		return false
	}
	return classIncludes(got, target)
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
	registry.props[marker.ID] = &propertyMeta{property: property.Clone()}
	registry.mu.Unlock()
	if err := object.Set(property.Name, ObjectValue(marker.Ref())); err != nil {
		return err
	}
	return object.SetFlags(property.Name, MemberDontDelete)
}
