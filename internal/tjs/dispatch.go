package tjs

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"
)

// Dispatcher is the stateful entry point used by a VM.  The object table is
// optional: objects carry stable IDs, while a table is only needed by callers
// that want to resolve IDs externally.
type Dispatcher struct{ Table *ObjectTable }

func NewDispatcher(table *ObjectTable) *Dispatcher { return &Dispatcher{Table: table} }

func (d *Dispatcher) object(ref ObjectRef) (*Object, error) {
	if ref.Object == 0 {
		return nil, ErrType
	}
	if d != nil && d.Table != nil {
		if o, ok := d.Table.Lookup(ref.Object); ok {
			if !o.IsValid() {
				return nil, errInvalidObject
			}
			return o, nil
		}
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	// Registry metadata does not retain objects, so a table is recommended.
	return nil, fmt.Errorf("TJS object %d is not registered", ref.Object)
}

func objectForValue(value Value) (*Object, error) {
	if value.Kind() != KindObject || value.IsNull() {
		return nil, ErrType
	}
	return nil, nil
}

func (d *Dispatcher) resolve(value Value) (*Object, error) {
	if value.Kind() != KindObject || value.IsNull() {
		return nil, ErrType
	}
	return d.object(value.ObjectRef())
}

func flagsOf(o *Object, key UTF16) uint32 {
	if o == nil {
		return 0
	}
	o.mu.RLock()
	defer o.mu.RUnlock()
	if m, ok := o.members[memberKey(key)]; ok && m.valid {
		return m.flags
	}
	return 0
}

func hasDirect(o *Object, key UTF16) (Value, uint32, bool) {
	if o == nil {
		return Value{}, 0, false
	}
	o.mu.RLock()
	defer o.mu.RUnlock()
	if !o.valid {
		return Value{}, 0, false
	}
	m, ok := o.members[memberKey(key)]
	return m.value, m.flags, ok && m.valid
}

func (d *Dispatcher) Get(target Value, name string, flags ...uint32) (Value, error) {
	var flag uint32
	if len(flags) != 0 {
		flag = flags[0]
	}
	if target.Kind() != KindObject || target.IsNull() {
		return primitiveGet(d, target, name)
	}
	o, err := d.resolve(target)
	if err != nil {
		return Value{}, err
	}
	key := UTF16FromString(name)
	if o.Category == CategoryArray && (name == "length" || name == "count") {
		if v, ok := arrayMethod(d, o, name); ok {
			return v, nil
		}
	}
	if v, _, ok := hasDirect(o, key); ok {
		if flag&IgnoreProperty == 0 {
			if out, handled, e := d.getProperty(v, o.Ref(), target); handled {
				return out, e
			}
		}
		return d.bindMember(v, o.Ref()), nil
	}
	if c := classForObject(o.ID); c != nil {
		if v, prop, ok := lookupClassMember(c, name); ok {
			if prop != nil && flag&IgnoreProperty == 0 {
				out, _, e := d.getPropertyDescriptor(prop, o.Ref(), target)
				return out, e
			}
			return d.bindMember(v, o.Ref()), nil
		}
	}
	if v, ok := builtinObjectGet(d, o, name); ok {
		return v, nil
	}
	if flag&MemberMustExist != 0 {
		return Value{}, ErrMemberNotFound
	}
	return VoidValue, nil
}

func (d *Dispatcher) getProperty(value Value, context ObjectRef, target Value) (Value, bool, error) {
	registry.mu.RLock()
	p := registry.props[value.ObjectRef().Object]
	registry.mu.RUnlock()
	if p == nil {
		return Value{}, false, nil
	}
	return d.getPropertyDescriptor(p, context, target)
}

func (d *Dispatcher) getPropertyDescriptor(p *propertyMeta, context ObjectRef, target Value) (Value, bool, error) {
	if p == nil || p.property.Getter == nil {
		return VoidValue, true, nil
	}
	v, err := callProgram(p.property.Getter.Program, context, nil)
	return v, true, err
}

func (d *Dispatcher) bindMember(value Value, context ObjectRef) Value {
	if value.Kind() != KindObject {
		return value
	}
	registry.mu.RLock()
	meta := registry.callable[value.ObjectRef().Object]
	registry.mu.RUnlock()
	if meta == nil || context.Object == 0 {
		return value
	}
	bound := newFunctionObject(dTable(d), meta.fn, meta.call, meta.valueCall, context, meta.boundValue)
	return ObjectValue(bound.Ref(context.Object))
}

func dTable(d *Dispatcher) *ObjectTable {
	if d == nil {
		return nil
	}
	return d.Table
}

func (d *Dispatcher) Set(target Value, name string, value Value, flags ...uint32) error {
	var flag uint32
	if len(flags) != 0 {
		flag = flags[0]
	}
	o, err := d.resolve(target)
	if err != nil {
		return err
	}
	key := UTF16FromString(name)
	if o.Category == CategoryArray && (name == "length" || name == "count") {
		return setArrayLength(o, value)
	}
	if o.Category == CategoryArray {
		if n, indexed := canonicalArrayIndex(name); indexed && n >= int64(maxVMArrayLength) {
			return fmt.Errorf("TJS array length limit exceeded")
		}
	}
	if existing, memberFlags, ok := hasDirect(o, key); ok {
		if memberReadOnly(memberFlags) {
			return ErrReadOnly
		}
		if flag&IgnoreProperty == 0 {
			if handled, e := d.setProperty(existing, o.Ref(), target, value); handled {
				return e
			}
		}
		return o.SetUnits(key, value)
	}
	if c := classForObject(o.ID); c != nil {
		if existing, prop, ok := lookupClassMember(c, name); ok {
			if prop != nil && flag&IgnoreProperty == 0 {
				if prop.property.Setter == nil {
					return ErrReadOnly
				}
				_, e := callProgram(prop.property.Setter.Program, o.Ref(), []Value{value})
				return e
			}
			_ = existing
			return ErrReadOnly
		}
	}
	if flag&MemberMustExist != 0 && o.Category == CategoryDictionary && !o.IsMemberValid(name) {
		return ErrMemberNotFound
	}
	return o.SetUnits(key, value)
}

func (d *Dispatcher) setProperty(value Value, context ObjectRef, target Value, next Value) (bool, error) {
	registry.mu.RLock()
	p := registry.props[value.ObjectRef().Object]
	registry.mu.RUnlock()
	if p == nil {
		return false, nil
	}
	if p.property.Setter == nil {
		return true, ErrReadOnly
	}
	_, err := callProgram(p.property.Setter.Program, context, []Value{next})
	return true, err
}

func (d *Dispatcher) Delete(target Value, name string, flags ...uint32) error {
	var flag uint32
	if len(flags) != 0 {
		flag = flags[0]
	}
	o, err := d.resolve(target)
	if err != nil {
		return err
	}
	key := UTF16FromString(name)
	_, memberFlags, ok := hasDirect(o, key)
	if !ok {
		if flag&MemberMustExist != 0 {
			return ErrMemberNotFound
		}
		return nil
	}
	if memberDontDelete(memberFlags) || memberReadOnly(memberFlags) {
		return ErrCannotDelete
	}
	if !o.DeleteUnits(key) {
		return ErrMemberNotFound
	}
	return nil
}

func (d *Dispatcher) Has(target Value, name string, flags ...uint32) bool {
	if target.Kind() != KindObject || target.IsNull() {
		_, err := primitiveGet(d, target, name)
		return err == nil
	}
	o, err := d.resolve(target)
	if err != nil {
		return false
	}
	if _, _, ok := hasDirect(o, UTF16FromString(name)); ok {
		return true
	}
	if c := classForObject(o.ID); c != nil {
		if _, _, ok := lookupClassMember(c, name); ok {
			return true
		}
	}
	_, ok := builtinObjectGet(d, o, name)
	return ok
}

func (d *Dispatcher) Call(function Value, args ...Value) (Value, error) {
	return d.CallWithContext(function, ObjectRef{}, args...)
}
func (d *Dispatcher) CallWithContext(function Value, context ObjectRef, args ...Value) (Value, error) {
	if function.Kind() != KindObject || function.IsNull() {
		return Value{}, ErrType
	}
	registry.mu.RLock()
	meta := registry.callable[function.ObjectRef().Object]
	registry.mu.RUnlock()
	if meta == nil {
		return Value{}, ErrType
	}
	if context.Object == 0 {
		context = meta.bound
	}
	if meta.call != nil {
		return meta.call(context, args)
	}
	if meta.valueCall != nil {
		if meta.boundValue != nil {
			return meta.valueCall(*meta.boundValue, args)
		}
		return Value{}, ErrType
	}
	return callProgram(meta.fn.Program, context, args)
}

func (d *Dispatcher) CallMember(target Value, name string, args ...Value) (Value, error) {
	v, err := d.Get(target, name, MemberMustExist)
	if err != nil {
		return Value{}, err
	}
	if target.Kind() == KindObject {
		return d.CallWithContext(v, target.ObjectRef(), args...)
	}
	return d.Call(v, args...)
}

func (d *Dispatcher) Operation(target Value, op Operation, param ...Value) (Value, error) {
	if op == OpInc || op == OpDec {
		one := IntegerValue(1)
		if op == OpDec {
			return target.Binary("-", one)
		}
		return target.Binary("+", one)
	}
	if len(param) == 0 {
		return Value{}, ErrBadParamCount
	}
	ops := map[Operation]string{OpBand: "&", OpBor: "|", OpBxor: "^", OpSub: "-", OpAdd: "+", OpMod: "%", OpDiv: "/", OpIDiv: "\\", OpMul: "*", OpLor: "||", OpLand: "&&", OpSAR: ">>", OpSAL: "<<", OpSR: ">>>"}
	name, ok := ops[op&Operation(0x1f)]
	if !ok {
		return Value{}, ErrNotImplemented
	}
	return target.Binary(name, param[0])
}

// Top-level aliases make direct use convenient and keep the API close to the
// C++ dispatch vocabulary.
func GetMember(table *ObjectTable, target Value, name string, flags ...uint32) (Value, error) {
	return NewDispatcher(table).Get(target, name, flags...)
}
func SetMember(table *ObjectTable, target Value, name string, value Value, flags ...uint32) error {
	return NewDispatcher(table).Set(target, name, value, flags...)
}
func DeleteMember(table *ObjectTable, target Value, name string, flags ...uint32) error {
	return NewDispatcher(table).Delete(target, name, flags...)
}
func HasMember(table *ObjectTable, target Value, name string) bool {
	return NewDispatcher(table).Has(target, name)
}
func CallMember(table *ObjectTable, target Value, name string, args ...Value) (Value, error) {
	return NewDispatcher(table).CallMember(target, name, args...)
}
func Invoke(table *ObjectTable, fn Value, args ...Value) (Value, error) {
	return NewDispatcher(table).Call(fn, args...)
}
func Operate(target Value, op Operation, param Value) (Value, error) {
	return NewDispatcher(nil).Operation(target, op, param)
}

func builtinObjectGet(d *Dispatcher, o *Object, name string) (Value, bool) {
	switch o.Category {
	case CategoryArray:
		return arrayMethod(d, o, name)
	case CategoryDictionary:
		return dictionaryMethod(d, o, name)
	case CategoryNative:
		if regexesHas(o.ID) {
			return regexpMethod(d, o, name)
		}
	}
	return Value{}, false
}

func regexesHas(id ObjectID) bool {
	regexMu.RLock()
	_, ok := regexes[id]
	regexMu.RUnlock()
	return ok
}

func primitiveGet(d *Dispatcher, target Value, name string) (Value, error) {
	switch target.Kind() {
	case KindString:
		return stringMethod(target, name)
	case KindOctet:
		return octetMethod(target, name)
	default:
		return VoidValue, ErrMemberNotFound
	}
}

func makeValueMethod(d *Dispatcher, name string, value Value, fn ValueFunc) Value {
	o := newFunctionObject(dTable(d), Function{Name: name}, nil, fn, ObjectRef{}, &value)
	return ObjectValue(o.Ref())
}

func stringUTF16Index(haystack, needle []uint16, start int) int {
	if start > len(haystack) {
		return -1
	}
	if len(needle) == 0 {
		return start
	}
	for i := start; i+len(needle) <= len(haystack); i++ {
		match := true
		for j, unit := range needle {
			if haystack[i+j] != unit {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

func stringUTF16LastIndex(haystack, needle []uint16) int {
	if len(needle) == 0 {
		return len(haystack)
	}
	for i := len(haystack) - len(needle); i >= 0; i-- {
		match := true
		for j, unit := range needle {
			if haystack[i+j] != unit {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}
func stringMethod(value Value, name string) (Value, error) {
	switch name {
	case "length":
		return IntegerValue(int64(len(value.string))), nil
	case "sprintf":
		return makeValueMethod(nil, name, value, func(v Value, a []Value) (Value, error) {
			return formatTJSString(v, a)
		}), nil
	case "charAt":
		return makeValueMethod(nil, name, value, func(v Value, a []Value) (Value, error) {
			i, e := argInt(a, 0)
			if e != nil {
				return Value{}, e
			}
			if i < 0 || i >= int64(len(v.string)) {
				return StringValue(""), nil
			}
			return StringUnits(v.string[i : i+1]), nil
		}), nil
	case "indexOf":
		return makeValueMethod(nil, name, value, func(v Value, a []Value) (Value, error) {
			if len(a) == 0 {
				return IntegerValue(-1), nil
			}
			u := v.string
			at := int64(0)
			if len(a) > 1 {
				var e error
				at, e = a[1].Int()
				if e != nil {
					return Value{}, e
				}
			}
			if at < 0 {
				at = 0
			}
			if at > int64(len(u)) {
				return IntegerValue(-1), nil
			}
			return IntegerValue(int64(stringUTF16Index(u, tjsStringUnits(a[0]), int(at)))), nil
		}), nil
	case "lastIndexOf":
		return makeValueMethod(nil, name, value, func(v Value, a []Value) (Value, error) {
			if len(a) == 0 {
				return IntegerValue(-1), nil
			}
			return IntegerValue(int64(stringUTF16LastIndex(v.string, tjsStringUnits(a[0])))), nil
		}), nil
	case "substr":
		return makeValueMethod(nil, name, value, func(v Value, a []Value) (Value, error) {
			start, e := argInt(a, 0)
			if e != nil {
				return Value{}, e
			}
			u := v.string
			if start < 0 {
				start += int64(len(u))
			}
			if start < 0 {
				start = 0
			}
			if start > int64(len(u)) {
				start = int64(len(u))
			}
			end := int64(len(u))
			if len(a) > 1 {
				n, e := a[1].Int()
				if e != nil {
					return Value{}, e
				}
				if n < 0 {
					n = 0
				}
				if n < end-start {
					end = start + n
				}
			}
			return StringUnits(u[start:end]), nil
		}), nil
	case "substring":
		return makeValueMethod(nil, name, value, func(v Value, a []Value) (Value, error) {
			start, e := argInt(a, 0)
			if e != nil {
				return Value{}, e
			}
			end := int64(len(v.string))
			if len(a) > 1 {
				end, e = a[1].Int()
				if e != nil {
					return Value{}, e
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
			u := v.string
			if start > int64(len(u)) {
				start = int64(len(u))
			}
			if end > int64(len(u)) {
				end = int64(len(u))
			}
			return StringUnits(u[start:end]), nil
		}), nil
	case "toUpperCase":
		return makeValueMethod(nil, name, value, func(v Value, a []Value) (Value, error) { return StringValue(strings.ToUpper(v.Text())), nil }), nil
	case "toLowerCase":
		return makeValueMethod(nil, name, value, func(v Value, a []Value) (Value, error) { return StringValue(strings.ToLower(v.Text())), nil }), nil
	case "trim":
		return makeValueMethod(nil, name, value, func(v Value, a []Value) (Value, error) {
			return StringValue(strings.TrimFunc(v.Text(), unicode.IsSpace)), nil
		}), nil
	case "split":
		return makeValueMethod(nil, name, value, func(v Value, a []Value) (Value, error) {
			sep := ""
			if len(a) > 0 {
				sep = a[0].Text()
			}
			o := NewObject(CategoryArray)
			if sep == "" {
				for _, r := range v.Text() {
					_ = o.Set(strconv.Itoa(int(r)), StringValue(string(r)))
				}
			} else {
				for i, x := range strings.Split(v.Text(), sep) {
					_ = o.Set(strconv.Itoa(i), StringValue(x))
				}
			}
			return ObjectValue(o.Ref()), nil
		}), nil
	case "replace":
		return makeValueMethod(nil, name, value, func(v Value, a []Value) (Value, error) {
			return replaceStringValue(v, a)
		}), nil
	}
	return VoidValue, ErrMemberNotFound
}

func octetMethod(value Value, name string) (Value, error) {
	switch name {
	case "length":
		return IntegerValue(int64(len(value.Octets()))), nil
	case "get":
		return makeValueMethod(nil, name, value, func(v Value, a []Value) (Value, error) {
			i, e := argInt(a, 0)
			if e != nil {
				return Value{}, e
			}
			b := v.Octets()
			if i < 0 || i >= int64(len(b)) {
				return IntegerValue(0), nil
			}
			return IntegerValue(int64(b[i])), nil
		}), nil
	case "subarray":
		return makeValueMethod(nil, name, value, func(v Value, a []Value) (Value, error) {
			start, e := argInt(a, 0)
			if e != nil {
				return Value{}, e
			}
			end := int64(len(v.Octets()))
			if len(a) > 1 {
				end, e = a[1].Int()
				if e != nil {
					return Value{}, e
				}
			}
			b := v.Octets()
			if start < 0 {
				start = 0
			}
			if end > int64(len(b)) {
				end = int64(len(b))
			}
			if start > end {
				start = end
			}
			return OctetValue(b[start:end]), nil
		}), nil
	case "toString":
		return makeValueMethod(nil, name, value, func(v Value, a []Value) (Value, error) { return StringValue(v.Text()), nil }), nil
	}
	return VoidValue, ErrMemberNotFound
}

func argInt(args []Value, n int) (int64, error) {
	if n >= len(args) {
		return 0, ErrBadParamCount
	}
	return args[n].Int()
}

func canonicalArrayIndex(name string) (int64, bool) {
	if name == "" || (len(name) > 1 && name[0] == '0') {
		return 0, false
	}
	for i := range name {
		if name[i] < '0' || name[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseInt(name, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

func arrayEntries(o *Object) []struct {
	i int64
	v Value
} {
	out := make([]struct {
		i int64
		v Value
	}, 0)
	for _, e := range o.Entries() {
		i, ok := canonicalArrayIndex(e.Name)
		if ok {
			out = append(out, struct {
				i int64
				v Value
			}{i, e.Value})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].i < out[j].i })
	return out
}

func arrayLength(o *Object) int64 {
	if o == nil {
		return 0
	}
	o.mu.RLock()
	defer o.mu.RUnlock()
	if !o.valid {
		return 0
	}
	return o.arrayLength
}

func resizeArrayLength(o *Object, length int64) error {
	if o == nil {
		return errInvalidObject
	}
	if length < 0 || length > maxVMArrayLength {
		return fmt.Errorf("TJS array length limit exceeded")
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.valid {
		return errInvalidObject
	}
	if length < o.arrayLength {
		for key, member := range o.members {
			if !member.valid {
				continue
			}
			if index, indexed := canonicalArrayIndex(member.key.String()); indexed && index >= length {
				delete(o.members, key)
			}
		}
	}
	o.arrayLength = length
	return nil
}

func setArrayLength(o *Object, value Value) error {
	length, err := value.Int()
	if err != nil {
		return err
	}
	return resizeArrayLength(o, length)
}
func tjsStringUnits(value Value) UTF16 {
	if value.Kind() == KindString {
		return value.string
	}
	if value.Kind() == KindVoid {
		return UTF16FromString("void")
	}
	return UTF16FromString(value.Text())
}

func splitArrayValues(pattern, subject Value, purgeEmpty bool) ([]Value, error) {
	if r := regexpValue(pattern); r != nil {
		texts := r.SplitPurgeEmpty(subject.Text(), purgeEmpty)
		if len(texts) > maxVMArrayLength {
			return nil, fmt.Errorf("TJS array length limit exceeded")
		}
		parts := make([]Value, len(texts))
		for i, text := range texts {
			parts[i] = StringValue(text)
		}
		return parts, nil
	}
	delimiters := tjsStringUnits(pattern)
	subjectUnits := tjsStringUnits(subject)
	for i, unit := range delimiters {
		if unit == 0 {
			delimiters = delimiters[:i]
			break
		}
	}
	for i, unit := range subjectUnits {
		if unit == 0 {
			subjectUnits = subjectUnits[:i]
			break
		}
	}
	var delimiterSet [1024]uint64
	for _, unit := range delimiters {
		delimiterSet[unit/64] |= uint64(1) << (unit % 64)
	}

	parts := make([]Value, 0)
	start := 0
	for i, unit := range subjectUnits {
		if delimiterSet[unit/64]&(uint64(1)<<(unit%64)) == 0 {
			continue
		}
		if !purgeEmpty || i > start {
			if len(parts) >= maxVMArrayLength {
				return nil, fmt.Errorf("TJS array length limit exceeded")
			}
			parts = append(parts, StringUnits(subjectUnits[start:i]))
		}
		start = i + 1
	}
	if !purgeEmpty || start < len(subjectUnits) {
		if len(parts) >= maxVMArrayLength {
			return nil, fmt.Errorf("TJS array length limit exceeded")
		}
		parts = append(parts, StringUnits(subjectUnits[start:]))
	}
	return parts, nil
}

func assignArrayItems(target, source *Object) error {
	if source.Category == CategoryArray {
		length := arrayLength(source)
		if length > int64(maxVMArrayLength) {
			return fmt.Errorf("TJS array length limit exceeded")
		}
		entries := arrayEntries(source)
		next := 0
		for index := int64(0); index < length; index++ {
			value := VoidValue
			if next < len(entries) && entries[next].i == index {
				value = entries[next].v
				next++
			}
			if err := target.Set(strconv.FormatInt(index, 10), value); err != nil {
				return err
			}
		}
		return resizeArrayLength(target, length)
	}

	entries := source.Entries()
	visible := 0
	for _, entry := range entries {
		if entry.Flags&MemberHidden == 0 {
			visible++
		}
	}
	if visible > maxVMArrayLength/2 {
		return fmt.Errorf("TJS array length limit exceeded")
	}
	index := 0
	for _, entry := range entries {
		if entry.Flags&MemberHidden != 0 {
			continue
		}
		if err := target.Set(strconv.Itoa(index), StringUnits(entry.Key)); err != nil {
			return err
		}
		if err := target.Set(strconv.Itoa(index+1), entry.Value); err != nil {
			return err
		}
		index += 2
	}
	return resizeArrayLength(target, int64(index))
}

func arraySourceObject(d *Dispatcher, value Value) (*Object, error) {
	if value.Kind() != KindObject || value.IsNull() {
		return nil, ErrType
	}
	ref := value.ObjectRef()
	if ref.Context != 0 {
		ref.Object = ref.Context
	}
	return d.object(ref)
}

func arrayMethod(d *Dispatcher, o *Object, name string) (Value, bool) {
	a := func(_ Value, args []Value) (Value, error) { return VoidValue, nil }
	switch name {
	case "length", "count":
		return IntegerValue(arrayLength(o)), true
	case "assign":
		a = func(_ Value, args []Value) (Value, error) {
			if len(args) == 0 {
				return VoidValue, ErrBadParamCount
			}
			if err := resizeArrayLength(o, 0); err != nil {
				return Value{}, err
			}
			source, err := arraySourceObject(d, args[0])
			if err != nil {
				return Value{}, err
			}
			return VoidValue, assignArrayItems(o, source)
		}
	case "split":
		a = func(_ Value, args []Value) (Value, error) {
			if len(args) < 2 {
				return VoidValue, ErrBadParamCount
			}
			if err := resizeArrayLength(o, 0); err != nil {
				return Value{}, err
			}
			purgeEmpty := false
			if len(args) > 3 && !args[3].IsVoid() {
				var err error
				purgeEmpty, err = args[3].Truth()
				if err != nil {
					return Value{}, err
				}
			}
			parts, err := splitArrayValues(args[0], args[1], purgeEmpty)
			if err != nil {
				return Value{}, err
			}
			for i, part := range parts {
				if err := o.Set(strconv.Itoa(i), part); err != nil {
					return Value{}, err
				}
			}
			if err := resizeArrayLength(o, int64(len(parts))); err != nil {
				return Value{}, err
			}
			return ObjectValue(o.Ref(o.ID)), nil
		}
	case "push":
		a = func(_ Value, args []Value) (Value, error) {
			n := arrayLength(o)
			if len(args) > 0 && (n >= int64(maxVMArrayLength) || len(args) > maxVMArrayLength-int(n)) {
				return VoidValue, fmt.Errorf("TJS array length limit exceeded")
			}
			for i, v := range args {
				if e := o.Set(strconv.FormatInt(n+int64(i), 10), v); e != nil {
					return Value{}, e
				}
			}
			return IntegerValue(n + int64(len(args))), nil
		}
	case "unshift":
		a = func(_ Value, args []Value) (Value, error) {
			n := arrayLength(o)
			if len(args) > 0 && (n >= int64(maxVMArrayLength) || len(args) > maxVMArrayLength-int(n)) {
				return VoidValue, fmt.Errorf("TJS array length limit exceeded")
			}
			if len(args) == 0 {
				return IntegerValue(n), nil
			}
			es := arrayEntries(o)
			for _, entry := range es {
				_ = o.Delete(strconv.FormatInt(entry.i, 10))
			}
			for _, entry := range es {
				if e := o.Set(strconv.FormatInt(entry.i+int64(len(args)), 10), entry.v); e != nil {
					return Value{}, e
				}
			}
			for i, value := range args {
				if e := o.Set(strconv.Itoa(i), value); e != nil {
					return Value{}, e
				}
			}
			if e := resizeArrayLength(o, n+int64(len(args))); e != nil {
				return Value{}, e
			}
			return IntegerValue(n + int64(len(args))), nil
		}
	case "add":
		a = func(_ Value, args []Value) (Value, error) {
			if len(args) != 1 {
				return VoidValue, ErrBadParamCount
			}
			n := arrayLength(o)
			if n >= int64(maxVMArrayLength) {
				return VoidValue, fmt.Errorf("TJS array length limit exceeded")
			}
			if e := o.Set(strconv.FormatInt(n, 10), args[0]); e != nil {
				return Value{}, e
			}
			return IntegerValue(n), nil
		}
	case "pop":
		a = func(_ Value, args []Value) (Value, error) {
			n := arrayLength(o)
			if n == 0 {
				return VoidValue, nil
			}
			name := strconv.FormatInt(n-1, 10)
			value, _ := o.Get(name)
			_ = o.Delete(name)
			if e := resizeArrayLength(o, n-1); e != nil {
				return Value{}, e
			}
			return value, nil
		}
	case "shift":
		a = func(_ Value, args []Value) (Value, error) {
			n := arrayLength(o)
			if n == 0 {
				return VoidValue, nil
			}
			first, _ := o.Get("0")
			es := arrayEntries(o)
			for _, entry := range es {
				_ = o.Delete(strconv.FormatInt(entry.i, 10))
			}
			for _, entry := range es {
				if entry.i > 0 {
					if e := o.Set(strconv.FormatInt(entry.i-1, 10), entry.v); e != nil {
						return Value{}, e
					}
				}
			}
			if e := resizeArrayLength(o, n-1); e != nil {
				return Value{}, e
			}
			return first, nil
		}
	case "join":
		a = func(_ Value, args []Value) (Value, error) {
			sep := ","
			if len(args) > 0 {
				sep = args[0].Text()
			}
			n := arrayLength(o)
			parts := make([]string, n)
			for _, entry := range arrayEntries(o) {
				parts[int(entry.i)] = entry.v.Text()
			}
			return StringValue(strings.Join(parts, sep)), nil
		}
	case "indexOf":
		a = func(_ Value, args []Value) (Value, error) {
			if len(args) == 0 {
				return IntegerValue(-1), nil
			}
			for _, entry := range arrayEntries(o) {
				if entry.v.Equal(args[0]) {
					return IntegerValue(entry.i), nil
				}
			}
			return IntegerValue(-1), nil
		}
	case "remove":
		a = func(_ Value, args []Value) (Value, error) {
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
			for _, entry := range arrayEntries(o) {
				if entry.v.StrictEqual(args[0]) {
					_ = o.Delete(strconv.FormatInt(entry.i, 10))
					removed++
					if !all {
						break
					}
				}
			}
			return IntegerValue(int64(removed)), nil
		}
	case "erase":
		a = func(_ Value, args []Value) (Value, error) {
			if len(args) != 1 {
				return VoidValue, ErrBadParamCount
			}
			index, err := args[0].Int()
			if err != nil {
				return VoidValue, err
			}
			n := arrayLength(o)
			if index < 0 || index >= n {
				return VoidValue, fmt.Errorf("TJS array index out of range")
			}
			es := arrayEntries(o)
			for _, entry := range es {
				if entry.i >= index {
					_ = o.Delete(strconv.FormatInt(entry.i, 10))
				}
			}
			for _, entry := range es {
				if entry.i > index {
					if e := o.Set(strconv.FormatInt(entry.i-1, 10), entry.v); e != nil {
						return Value{}, e
					}
				}
			}
			if e := resizeArrayLength(o, n-1); e != nil {
				return Value{}, e
			}
			return VoidValue, nil
		}
	case "insert":
		a = func(_ Value, args []Value) (Value, error) {
			if len(args) != 2 {
				return VoidValue, ErrBadParamCount
			}
			index, err := args[0].Int()
			if err != nil {
				return VoidValue, err
			}
			n := arrayLength(o)
			if index < 0 || index > n {
				return VoidValue, fmt.Errorf("TJS array index out of range")
			}
			if n >= int64(maxVMArrayLength) {
				return VoidValue, fmt.Errorf("TJS array length limit exceeded")
			}
			es := arrayEntries(o)
			for _, entry := range es {
				if entry.i >= index {
					_ = o.Delete(strconv.FormatInt(entry.i, 10))
				}
			}
			for _, entry := range es {
				if entry.i >= index {
					if e := o.Set(strconv.FormatInt(entry.i+1, 10), entry.v); e != nil {
						return Value{}, e
					}
				}
			}
			if e := o.Set(strconv.FormatInt(index, 10), args[1]); e != nil {
				return Value{}, e
			}
			if e := resizeArrayLength(o, n+1); e != nil {
				return Value{}, e
			}
			return VoidValue, nil
		}
	case "reverse":
		a = func(_ Value, args []Value) (Value, error) {
			n := arrayLength(o)
			es := arrayEntries(o)
			for _, entry := range es {
				_ = o.Delete(strconv.FormatInt(entry.i, 10))
			}
			for _, entry := range es {
				if e := o.Set(strconv.FormatInt(n-1-entry.i, 10), entry.v); e != nil {
					return Value{}, e
				}
			}
			return ObjectValue(o.Ref()), nil
		}
	case "clear":
		a = func(_ Value, args []Value) (Value, error) {
			if e := resizeArrayLength(o, 0); e != nil {
				return Value{}, e
			}
			return VoidValue, nil
		}
	default:
		return VoidValue, false
	}
	fn := NativeFunc(func(_ ObjectRef, args []Value) (Value, error) { return a(VoidValue, args) })
	return ObjectValue(newFunctionObject(dTable(d), Function{Name: name}, fn, nil, o.Ref(), nil).Ref(o.ID)), true
}
func dictionaryMethod(d *Dispatcher, o *Object, name string) (Value, bool) {
	var fn NativeFunc
	switch name {
	case "count", "length":
		fn = func(_ ObjectRef, _ []Value) (Value, error) { return IntegerValue(int64(o.Len())), nil }
	case "has":
		fn = func(_ ObjectRef, a []Value) (Value, error) {
			if len(a) == 0 {
				return Value{}, ErrBadParamCount
			}
			return boolValue(o.IsMemberValid(a[0].Text())), nil
		}
	case "clear":
		fn = func(_ ObjectRef, _ []Value) (Value, error) {
			for _, e := range o.Entries() {
				_ = o.Delete(e.Name)
			}
			return VoidValue, nil
		}
	default:
		return VoidValue, false
	}
	return ObjectValue(newFunctionObject(dTable(d), Function{Name: name}, fn, nil, o.Ref(), nil).Ref(o.ID)), true
}

var regexMu sync.RWMutex
var regexes = make(map[ObjectID]*Regexp)

func regexpValue(value Value) *Regexp {
	if value.Kind() != KindObject || value.IsNull() {
		return nil
	}
	regexMu.RLock()
	r := regexes[value.ObjectRef().Object]
	regexMu.RUnlock()
	return r
}

func registerRegexpObject(o *Object, r *Regexp) {
	regexMu.Lock()
	regexes[o.ID] = r
	regexMu.Unlock()
	_ = o.Set("source", StringValue(r.re.String()))
	_ = o.SetFlags("source", MemberReadOnly)
}

func replaceStringValue(value Value, args []Value) (Value, error) {
	if len(args) < 2 {
		return Value{}, ErrBadParamCount
	}
	if r := regexpValue(args[0]); r != nil {
		return StringValue(r.ReplaceString(value.Text(), args[1].Text())), nil
	}
	return StringValue(strings.Replace(value.Text(), args[0].Text(), args[1].Text(), 1)), nil
}

// NewRegExp creates a native regexp object.
func NewRegExp(table *ObjectTable, pattern, flags string) (*Object, error) {
	r, err := CompileRegexp(pattern, flags)
	if err != nil {
		return nil, err
	}
	var o *Object
	if table != nil {
		o = table.New(CategoryNative)
	} else {
		o = NewObject(CategoryNative)
	}
	registerRegexpObject(o, r)
	return o, nil
}

func regexpMethod(d *Dispatcher, o *Object, name string) (Value, bool) {
	switch name {
	case "test", "exec":
		fn := func(_ ObjectRef, args []Value) (Value, error) {
			return d.RegExpCall(o, name, args...)
		}
		return ObjectValue(newFunctionObject(dTable(d), Function{Name: name}, fn, nil, o.Ref(), nil).Ref(o.ID)), true
	}
	return Value{}, false
}

func (d *Dispatcher) RegExpCall(o *Object, name string, args ...Value) (Value, error) {
	regexMu.RLock()
	r := regexes[o.ID]
	regexMu.RUnlock()
	if r == nil {
		return Value{}, ErrType
	}
	if len(args) == 0 {
		return Value{}, ErrBadParamCount
	}
	s := args[0].Text()
	switch name {
	case "test":
		return boolValue(r.IsMatch(s)), nil
	case "exec":
		match, ok := r.Find(s)
		if !ok {
			return NullValue, nil
		}
		a := NewObject(CategoryArray)
		_ = a.Set("0", StringValue(match.Text))
		for i, submatch := range match.Submatches {
			_ = a.Set(strconv.Itoa(i+1), StringValue(submatch))
		}
		return ObjectValue(a.Ref()), nil
	default:
		return Value{}, ErrMemberNotFound
	}
}
