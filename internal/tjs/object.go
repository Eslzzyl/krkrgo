package tjs

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf16"
	"unicode/utf8"
)

// ObjectCategory identifies the engine-side dispatch object implementation.
type ObjectCategory uint8

const (
	CategoryGlobal ObjectCategory = iota
	CategoryNamespace
	CategoryDictionary
	CategoryArray
	CategoryFunction
	CategoryClass
	CategoryInstance
	CategoryProperty
	CategoryNative
	CategoryMethod
)

var nextObjectID atomic.Uint64

// Function, Property, and Class are descriptors used by the compiler/runtime
// boundary. Program is intentionally opaque here: the VM/compiler owns its
// concrete representation and this package only preserves the descriptor.
type Function struct {
	Name       string
	Parameters []string
	Rest       string
	Program    Program
}

type Property struct {
	Name   string
	Getter *Function
	Setter *Function
}

type Class struct {
	Name        string
	Bases       []Program
	Methods     []Function
	Properties  []Property
	Initializer Program
}

func (f Function) Clone() Function {
	return Function{Name: f.Name, Parameters: append([]string(nil), f.Parameters...), Rest: f.Rest, Program: f.Program}
}
func (p Property) Clone() Property {
	var g, s *Function
	if p.Getter != nil {
		x := p.Getter.Clone()
		g = &x
	}
	if p.Setter != nil {
		x := p.Setter.Clone()
		s = &x
	}
	return Property{Name: p.Name, Getter: g, Setter: s}
}
func (c Class) Clone() Class {
	r := Class{Name: c.Name, Initializer: c.Initializer}
	r.Bases = append([]Program(nil), c.Bases...)
	r.Methods = make([]Function, len(c.Methods))
	for i := range c.Methods {
		r.Methods[i] = c.Methods[i].Clone()
	}
	r.Properties = make([]Property, len(c.Properties))
	for i := range c.Properties {
		r.Properties[i] = c.Properties[i].Clone()
	}
	return r
}

// PropertyEntry is an enumerated object member. Key retains the TJS UTF-16
// name, while Name is convenient for ordinary Unicode member names.
type PropertyEntry struct {
	Key   UTF16
	Name  string
	Value Value
	Flags uint32
	Valid bool
}

type member struct {
	key   UTF16
	value Value
	flags uint32
	valid bool
}

// Object is a stable-identity property container. Its address is never used as
// script identity; ID remains stable even if Go moves references to the object.
type Object struct {
	mu          sync.RWMutex
	ID          ObjectID
	Category    ObjectCategory
	members     map[string]member
	arrayLength int64
	valid       bool
}

func NewObject(categories ...ObjectCategory) *Object {
	category := CategoryDictionary
	if len(categories) != 0 {
		category = categories[0]
	}
	id := ObjectID(nextObjectID.Add(1))
	return &Object{ID: id, Category: category, members: make(map[string]member), valid: true}
}
func (o *Object) Ref(context ...ObjectID) ObjectRef {
	if o == nil {
		return ObjectRef{}
	}
	var c ObjectID
	if len(context) > 0 {
		c = context[0]
	}
	return ObjectRef{Object: o.ID, Context: c}
}
func (o *Object) IsValid() bool { return o != nil && o.validRead() }
func (o *Object) validRead() bool {
	if o == nil {
		return false
	}
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.valid
}
func (o *Object) Invalidate() {
	if o == nil {
		return
	}
	o.mu.Lock()
	o.valid = false
	o.mu.Unlock()
}

var errInvalidObject = errors.New("TJS object is invalid")

func (o *Object) checkValid() error {
	if o == nil || !o.validRead() {
		return errInvalidObject
	}
	return nil
}

func memberKey(units UTF16) string {
	b := make([]byte, len(units)*2)
	for i, unit := range units {
		b[2*i] = byte(unit)
		b[2*i+1] = byte(unit >> 8)
	}
	return string(b)
}

// memberKeyString converts a Go string name directly to the binary UTF-16 key
// representation used by memberKey. Like ranging over a string, invalid UTF-8
// bytes decode as one RuneError each.
func memberKeyString(name string) string {
	var key strings.Builder
	key.Grow(len(name) * 2)
	for len(name) > 0 {
		r, size := utf8.DecodeRuneInString(name)
		name = name[size:]
		if r <= 0xffff {
			unit := uint16(r)
			key.WriteByte(byte(unit))
			key.WriteByte(byte(unit >> 8))
			continue
		}
		high, low := utf16.EncodeRune(r)
		key.WriteByte(byte(high))
		key.WriteByte(byte(high >> 8))
		key.WriteByte(byte(low))
		key.WriteByte(byte(low >> 8))
	}
	return key.String()
}

func (o *Object) Set(name string, value Value) error { return o.SetString(name, value) }

// SetString sets a member addressed by a Go string name without first
// materializing UTF-16 units on an existing-member update.
func (o *Object) SetString(name string, value Value) error {
	if o == nil {
		return errInvalidObject
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.valid {
		return errInvalidObject
	}
	if o.Category == CategoryArray {
		if index, indexed := canonicalArrayIndex(name); indexed {
			if index >= int64(maxVMArrayLength) {
				return fmt.Errorf("TJS array length limit exceeded")
			}
			if index >= o.arrayLength {
				o.arrayLength = index + 1
			}
		}
	}
	k := memberKeyString(name)
	m, ok := o.members[k]
	if !ok {
		units := UTF16FromString(name)
		m = member{key: NewUTF16(units), valid: true}
	}
	m.value = value
	m.valid = true
	o.members[k] = m
	return nil
}

func (o *Object) SetUnits(key UTF16, value Value) error {
	if o == nil {
		return errInvalidObject
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.valid {
		return errInvalidObject
	}
	if o.Category == CategoryArray {
		if index, indexed := canonicalArrayIndex(key.String()); indexed {
			if index >= int64(maxVMArrayLength) {
				return fmt.Errorf("TJS array length limit exceeded")
			}
			if index >= o.arrayLength {
				o.arrayLength = index + 1
			}
		}
	}
	k := memberKey(key)
	m, ok := o.members[k]
	if !ok {
		m = member{key: NewUTF16(key), valid: true}
	}
	m.value = value
	m.valid = true
	o.members[k] = m
	return nil
}
func (o *Object) SetFlags(name string, flags uint32) error {
	if o == nil {
		return errInvalidObject
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.valid {
		return errInvalidObject
	}
	k := memberKey(UTF16FromString(name))
	m, ok := o.members[k]
	if !ok {
		m = member{key: UTF16FromString(name), valid: true}
	}
	m.flags = flags
	m.valid = true
	o.members[k] = m
	return nil
}
func (o *Object) Get(name string) (Value, bool) { return o.GetString(name) }
func (o *Object) GetString(name string) (Value, bool) {
	if o == nil {
		return Value{}, false
	}
	o.mu.RLock()
	defer o.mu.RUnlock()
	if !o.valid {
		return Value{}, false
	}
	m, ok := o.members[memberKeyString(name)]
	if !ok || !m.valid {
		return Value{}, false
	}
	return m.value, true
}
func (o *Object) GetUnits(key UTF16) (Value, bool) {
	if o == nil {
		return Value{}, false
	}
	o.mu.RLock()
	defer o.mu.RUnlock()
	if !o.valid {
		return Value{}, false
	}
	m, ok := o.members[memberKey(key)]
	if !ok || !m.valid {
		return Value{}, false
	}
	return m.value, true
}
func (o *Object) Delete(name string) bool { return o.DeleteString(name) }
func (o *Object) DeleteString(name string) bool {
	if o == nil {
		return false
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.valid {
		return false
	}
	k := memberKeyString(name)
	if _, ok := o.members[k]; !ok {
		return false
	}
	delete(o.members, k)
	return true
}
func (o *Object) DeleteUnits(key UTF16) bool {
	if o == nil {
		return false
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.valid {
		return false
	}
	k := memberKey(key)
	if _, ok := o.members[k]; !ok {
		return false
	}
	delete(o.members, k)
	return true
}
func (o *Object) InvalidateMember(name string) bool {
	return o.InvalidateMemberUnits(UTF16FromString(name))
}
func (o *Object) InvalidateMemberUnits(key UTF16) bool {
	if o == nil {
		return false
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.valid {
		return false
	}
	k := memberKey(key)
	m, ok := o.members[k]
	if !ok {
		return false
	}
	m.valid = false
	o.members[k] = m
	return true
}
func (o *Object) IsMemberValid(name string) bool {
	if o == nil {
		return false
	}
	o.mu.RLock()
	defer o.mu.RUnlock()
	m, ok := o.members[memberKeyString(name)]
	return o.valid && ok && m.valid
}

// Entries returns members in UTF-16 lexicographic order, matching Rust's
// BTreeMap and the engine's deterministic enumeration behaviour.
func (o *Object) Entries() []PropertyEntry {
	if o == nil {
		return nil
	}
	o.mu.RLock()
	defer o.mu.RUnlock()
	if !o.valid {
		return nil
	}
	all := make([]member, 0, len(o.members))
	for _, m := range o.members {
		if m.valid {
			all = append(all, m)
		}
	}
	sort.Slice(all, func(i, j int) bool { return compareUTF16(all[i].key, all[j].key) < 0 })
	out := make([]PropertyEntry, len(all))
	for i, m := range all {
		out[i] = PropertyEntry{Key: NewUTF16(m.key), Name: m.key.String(), Value: m.value, Flags: m.flags, Valid: m.valid}
	}
	return out
}
func (o *Object) Enumerate() []UTF16 {
	entries := o.Entries()
	out := make([]UTF16, len(entries))
	for i, e := range entries {
		out[i] = e.Key
	}
	return out
}
func (o *Object) Keys() []string {
	entries := o.Entries()
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Name
	}
	return out
}
func (o *Object) Len() int {
	if o == nil {
		return 0
	}
	o.mu.RLock()
	defer o.mu.RUnlock()
	if !o.valid {
		return 0
	}
	n := 0
	for _, m := range o.members {
		if m.valid {
			n++
		}
	}
	return n
}

// ObjectTable is an explicit identity table used by an engine/VM. It does not
// infer identity from pointers and keeps invalidated objects addressable until
// the owner deliberately removes them.
type ObjectTable struct {
	mu                  sync.RWMutex
	objects             map[ObjectID]*Object
	classInstanceNames  map[ObjectID][]string
	registeredClassInfo map[ObjectID]bool
	instanceClasses     map[ObjectID]ObjectID
}

func NewObjectTable() *ObjectTable {
	return &ObjectTable{
		objects:             make(map[ObjectID]*Object),
		classInstanceNames:  make(map[ObjectID][]string),
		registeredClassInfo: make(map[ObjectID]bool),
		instanceClasses:     make(map[ObjectID]ObjectID),
	}
}
func (t *ObjectTable) RegisterClassInstance(instanceID, classID ObjectID) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.instanceClasses[instanceID] = classID
	t.mu.Unlock()
}

func (t *ObjectTable) ClassForInstance(instanceID ObjectID) (ObjectID, bool) {
	if t == nil {
		return 0, false
	}
	t.mu.RLock()
	classID, ok := t.instanceClasses[instanceID]
	t.mu.RUnlock()
	return classID, ok
}
func (t *ObjectTable) New(categories ...ObjectCategory) *Object {
	o := NewObject(categories...)
	t.mu.Lock()
	t.objects[o.ID] = o
	t.mu.Unlock()
	return o
}
func (t *ObjectTable) Add(o *Object) error {
	if t == nil || o == nil {
		return errInvalidObject
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.objects[o.ID]; ok {
		return errors.New("duplicate TJS object ID")
	}
	t.objects[o.ID] = o
	return nil
}
func (t *ObjectTable) Lookup(id ObjectID) (*Object, bool) {
	if t == nil {
		return nil, false
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	o, ok := t.objects[id]
	return o, ok
}
func (t *ObjectTable) AddClassInstanceMember(objectID ObjectID, name string) error {
	if t == nil {
		return errInvalidObject
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.objects[objectID]; !ok {
		return errors.New("TJS class instance metadata target is unavailable")
	}
	t.classInstanceNames[objectID] = append(t.classInstanceNames[objectID], name)
	return nil
}

func (t *ObjectTable) RegisterClassInstanceMembers(objectID ObjectID) error {
	if t == nil {
		return errInvalidObject
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.objects[objectID]; !ok {
		return errors.New("TJS class instance metadata target is unavailable")
	}
	t.registeredClassInfo[objectID] = true
	return nil
}

func (t *ObjectTable) ClassInstanceMembers(classID ObjectID) ([]string, bool) {
	if t == nil {
		return nil, false
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	if !t.registeredClassInfo[classID] {
		return nil, false
	}
	return append([]string(nil), t.classInstanceNames[classID]...), true
}
func (t *ObjectTable) Remove(id ObjectID) bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.objects[id]; !ok {
		return false
	}
	delete(t.objects, id)
	delete(t.classInstanceNames, id)
	delete(t.registeredClassInfo, id)
	return true
}
func (t *ObjectTable) Ref(id ObjectID, context ...ObjectID) (ObjectRef, bool) {
	o, ok := t.Lookup(id)
	if !ok || !o.IsValid() {
		return ObjectRef{}, false
	}
	return o.Ref(context...), true
}
