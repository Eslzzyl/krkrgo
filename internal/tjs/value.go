package tjs

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

// ValueKind is the run-time type tag of a TJS value.
type ValueKind uint8

const (
	KindVoid ValueKind = iota
	KindObject
	KindString
	KindOctet
	KindInteger
	KindReal
)

// Short aliases make switches over TJS values pleasant to read.
const (
	VoidKind    = KindVoid
	ObjectKind  = KindObject
	StringKind  = KindString
	OctetKind   = KindOctet
	IntegerKind = KindInteger
	RealKind    = KindReal
)

// ObjectID is a stable script object identity. Zero is the null object handle.
type ObjectID uint64

// ObjectRef is a dispatch object and its optional bound this context.
type ObjectRef struct {
	Object       ObjectID
	Context      ObjectID
	contextBound bool
}

func NewObjectRef(object ObjectID, context ...ObjectID) ObjectRef {
	ref := ObjectRef{Object: object}
	if len(context) != 0 {
		ref.Context = context[0]
		ref.contextBound = true
	}
	return ref
}

// HasBoundContext reports whether the reference carries an explicit this-context.
func (r ObjectRef) HasBoundContext() bool { return r.contextBound || r.Context != 0 }

// SameDispatch compares object identity and distinguishes null from a valid global dispatch.
func (r ObjectRef) SameDispatch(other ObjectRef) bool {
	return r.Object == other.Object && r.IsNull() == other.IsNull() &&
		r.Context == other.Context && r.HasBoundContext() == other.HasBoundContext()
}

// BoundContextRef returns the dispatch object carried as this-context.
func (r ObjectRef) BoundContextRef() ObjectRef {
	if !r.HasBoundContext() {
		return ObjectRef{}
	}
	return boundContextRef(r.Context)
}

func (r ObjectRef) IsNull() bool  { return r.Object == 0 && r.Context == 0 && !r.contextBound }
func (r ObjectRef) IsValid() bool { return r.Object != 0 || r.HasBoundContext() }

func boundContextRef(context ObjectID) ObjectRef {
	if context == 0 {
		return NewObjectRef(0, 0)
	}
	return NewObjectRef(context, context)
}

// Value is a TJS variant. Strings contain UTF-16 code units, not Go runes.
// Values are immutable from the script engine's point of view; constructors and
// accessors copy slices so an object cannot mutate a value behind another value.
type Value struct {
	kind    ValueKind
	integer int64
	real    float64
	string  UTF16
	octet   []byte
	object  ObjectRef
}

var VoidValue = Value{kind: KindVoid}
var NullValue = Value{kind: KindObject}

func Void() Value                      { return VoidValue }
func Null() Value                      { return NullValue }
func IntegerValue(n int64) Value       { return Value{kind: KindInteger, integer: n} }
func RealValue(n float64) Value        { return Value{kind: KindReal, real: n} }
func StringValue(s string) Value       { return Value{kind: KindString, string: UTF16FromString(s)} }
func StringUnits(units []uint16) Value { return Value{kind: KindString, string: NewUTF16(units)} }
func OctetValue(bytes []byte) Value {
	return Value{kind: KindOctet, octet: append([]byte(nil), bytes...)}
}
func ObjectValue(ref ObjectRef) Value    { return Value{kind: KindObject, object: ref} }
func ObjectIDValue(id ObjectID) Value    { return ObjectValue(NewObjectRef(id)) }
func NewInteger(n int64) Value           { return IntegerValue(n) }
func NewReal(n float64) Value            { return RealValue(n) }
func NewString(s string) Value           { return StringValue(s) }
func NewOctet(bytes []byte) Value        { return OctetValue(bytes) }
func String(s string) Value              { return StringValue(s) }
func Octet(bytes []byte) Value           { return OctetValue(bytes) }
func Integer(n int64) Value              { return IntegerValue(n) }
func Real(n float64) Value               { return RealValue(n) }
func NewObjectValue(ref ObjectRef) Value { return ObjectValue(ref) }
func ObjectValueID(id ObjectID) Value    { return ObjectIDValue(id) }

func (v Value) Kind() ValueKind      { return v.kind }
func (v Value) IsVoid() bool         { return v.kind == KindVoid }
func (v Value) IsNull() bool         { return v.kind == KindObject && v.object.IsNull() }
func (v Value) ObjectRef() ObjectRef { return v.object }
func (v Value) IntegerValue() int64  { return v.integer }
func (v Value) RealValue() float64   { return v.real }
func (v Value) UTF16() UTF16         { return append(UTF16(nil), v.string...) }
func (v Value) Octets() []byte       { return append([]byte(nil), v.octet...) }

func (v Value) TypeName() string {
	switch v.kind {
	case KindVoid:
		return "void"
	case KindInteger:
		return "Integer"
	case KindReal:
		return "Real"
	case KindString:
		return "String"
	case KindOctet:
		return "Octet"
	case KindObject:
		return "Object"
	default:
		return "void"
	}
}
func (k ValueKind) String() string {
	switch k {
	case KindVoid:
		return "void"
	case KindObject:
		return "Object"
	case KindString:
		return "String"
	case KindOctet:
		return "Octet"
	case KindInteger:
		return "Integer"
	case KindReal:
		return "Real"
	default:
		return "unknown"
	}
}

// Text implements TJS's value-to-string operation (rather than Go's Stringer).
func (v Value) Text() string {
	switch v.kind {
	case KindVoid:
		return ""
	case KindInteger:
		return strconv.FormatInt(v.integer, 10)
	case KindReal:
		return RealText(v.real)
	case KindString:
		return v.string.String()
	case KindOctet:
		var b strings.Builder
		b.WriteString("(octet)<% ")
		for i, x := range v.octet {
			if i != 0 {
				b.WriteByte(' ')
			}
			fmt.Fprintf(&b, "%02X", x)
		}
		b.WriteString(" %>")
		return b.String()
	case KindObject:
		if v.object.Object == 0 {
			return "(object)(0x00000000)"
		}
		return fmt.Sprintf("(object)(%d)", v.object.Object)
	default:
		return ""
	}
}

func (v Value) String() string { return v.Text() }

var (
	errObjectNumber = errors.New("cannot convert TJS Object to number")
	errOctetNumber  = errors.New("cannot convert TJS Octet to number")
	errOctetString  = errors.New("cannot convert TJS Octet to String")
)

func (v Value) Number() (Value, error) {
	switch v.kind {
	case KindVoid:
		return IntegerValue(0), nil
	case KindString:
		return ParseNumber(v.string.String()), nil
	case KindObject:
		return Value{}, errObjectNumber
	case KindOctet:
		return Value{}, errOctetNumber
	default:
		return v, nil
	}
}

func (v Value) Int() (int64, error) {
	n, err := v.Number()
	if err != nil {
		return 0, err
	}
	if n.kind == KindInteger {
		return n.integer, nil
	}
	return int64(n.real), nil
}
func (v Value) Integer() (int64, error) { return v.Int() }
func (v Value) Float() (float64, error) {
	n, err := v.Number()
	if err != nil {
		return 0, err
	}
	if n.kind == KindInteger {
		return float64(n.integer), nil
	}
	return n.real, nil
}
func (v Value) Real() (float64, error) { return v.Float() }

func (v Value) Truth() (bool, error) {
	switch v.kind {
	case KindReal:
		return v.real != 0, nil // NaN is true, as in C++'s != 0 test.
	case KindObject:
		return !v.object.IsNull(), nil
	case KindOctet:
		return len(v.octet) != 0, nil
	default:
		n, err := v.Int()
		return n != 0, err
	}
}

func (v Value) StrictEqual(other Value) bool {
	if v.kind != other.kind {
		return false
	}
	switch v.kind {
	case KindVoid:
		return true
	case KindInteger:
		return v.integer == other.integer
	case KindReal:
		return v.real == other.real
	case KindString:
		return equalUTF16(v.string, other.string)
	case KindOctet:
		return bytesEqual(v.octet, other.octet)
	case KindObject:
		return v.object.Object == other.object.Object && v.object.IsNull() == other.object.IsNull()
	default:
		return false
	}
}

func (v Value) Equal(other Value) bool {
	switch {
	case v.kind == KindInteger && other.kind == KindInteger:
		return v.integer == other.integer
	case v.kind == KindString && other.kind == KindString:
		return equalUTF16(v.string, other.string)
	case v.kind == KindOctet && other.kind == KindOctet:
		return bytesEqual(v.octet, other.octet)
	case v.kind == KindOctet || other.kind == KindOctet:
		return false
	case v.kind == KindObject && other.kind == KindObject:
		return v.object.Object == other.object.Object && v.object.IsNull() == other.object.IsNull()
	case v.kind == KindObject || other.kind == KindObject:
		return false
	}
	if v.kind == KindString || other.kind == KindString {
		return v.Text() == other.Text()
	}
	a, ea := v.Float()
	b, eb := other.Float()
	return ea == nil && eb == nil && a == b
}

func (v Value) Unary(op string) (Value, error) {
	switch op {
	case "+":
		return v.Number()
	case "-":
		n, err := v.Number()
		if err != nil {
			return Value{}, err
		}
		if n.kind == KindInteger {
			return IntegerValue(-n.integer), nil
		}
		return RealValue(-n.real), nil
	case "!":
		b, err := v.Truth()
		if err != nil {
			return Value{}, err
		}
		if b {
			return IntegerValue(0), nil
		}
		return IntegerValue(1), nil
	case "~":
		n, err := v.Int()
		if err != nil {
			return Value{}, err
		}
		return IntegerValue(^n), nil
	case "int":
		n, err := v.Int()
		if err != nil {
			return Value{}, err
		}
		return IntegerValue(n), nil
	case "real":
		n, err := v.Float()
		if err != nil {
			return Value{}, err
		}
		return RealValue(n), nil
	case "string":
		if v.kind == KindString {
			return StringUnits(v.string), nil
		}
		if v.kind == KindOctet {
			return Value{}, errOctetString
		}
		return StringValue(v.Text()), nil
	case "typeof":
		return StringValue(v.TypeName()), nil
	case "#":
		if v.kind == KindOctet {
			return Value{}, errOctetString
		}
		if v.kind == KindString {
			if len(v.string) == 0 {
				return IntegerValue(0), nil
			}
			return IntegerValue(int64(v.string[0])), nil
		}
		t := UTF16FromString(v.Text())
		if len(t) == 0 {
			return IntegerValue(0), nil
		}
		return IntegerValue(int64(t[0])), nil
	case "$":
		n, err := v.Int()
		if err != nil {
			return Value{}, err
		}
		return StringUnits([]uint16{uint16(n)}), nil
	case "octet":
		return v.ToOctet()
	default:
		return Value{}, fmt.Errorf("unsupported TJS unary operator %s", op)
	}
}

// ToOctet implements the reference VM's tTJSVariant::ToOctet conversion: the
// void value becomes the null octet and an octet is copied unchanged; every
// other kind raises the shared variant conversion error because TJS has no
// implicit string/number/object-to-octet coercion.
func (v Value) ToOctet() (Value, error) {
	switch v.kind {
	case KindVoid:
		return OctetValue(nil), nil
	case KindOctet:
		return OctetValue(v.octet), nil
	default:
		return Value{}, fmt.Errorf("cannot convert TJS %s to Octet", v.TypeName())
	}
}

func boolValue(b bool) Value {
	if b {
		return IntegerValue(1)
	}
	return IntegerValue(0)
}

func (v Value) Binary(op string, other Value) (Value, error) {
	switch op {
	case "&&":
		a, e := v.Truth()
		if e != nil {
			return Value{}, e
		}
		b, e := other.Truth()
		if e != nil {
			return Value{}, e
		}
		return boolValue(a && b), nil
	case "||":
		a, e := v.Truth()
		if e != nil {
			return Value{}, e
		}
		b, e := other.Truth()
		if e != nil {
			return Value{}, e
		}
		return boolValue(a || b), nil
	case "==":
		return boolValue(v.Equal(other)), nil
	case "!=":
		return boolValue(!v.Equal(other)), nil
	case "===":
		return boolValue(v.StrictEqual(other)), nil
	case "!==":
		return boolValue(!v.StrictEqual(other)), nil
	case "+":
		if v.kind == KindString || other.kind == KindString {
			if v.kind == KindOctet || other.kind == KindOctet {
				return Value{}, errOctetString
			}
			return StringUnits(append(v.toStringUnits(), other.toStringUnits()...)), nil
		}
	case "<", ">", "<=", ">=":
		var less, greater bool
		if v.kind == KindString && other.kind == KindString {
			less = compareUTF16(v.string, other.string) < 0
			greater = compareUTF16(v.string, other.string) > 0
		} else if v.kind == KindInteger && other.kind == KindInteger {
			less = v.integer < other.integer
			greater = v.integer > other.integer
		} else {
			a, e := v.Float()
			if e != nil {
				return Value{}, e
			}
			b, e := other.Float()
			if e != nil {
				return Value{}, e
			}
			less = a < b
			greater = a > b
		}
		switch op {
		case "<":
			return boolValue(less), nil
		case ">":
			return boolValue(greater), nil
		case "<=":
			return boolValue(!greater), nil
		default:
			return boolValue(!less), nil
		}
	case "&", "|", "^", "<<", ">>", ">>>", "\\", "%":
		a, e := v.Int()
		if e != nil {
			return Value{}, e
		}
		b, e := other.Int()
		if e != nil {
			return Value{}, e
		}
		switch op {
		case "&":
			return IntegerValue(a & b), nil
		case "|":
			return IntegerValue(a | b), nil
		case "^":
			return IntegerValue(a ^ b), nil
		case "<<":
			return IntegerValue(a << (uint(b) & 63)), nil
		case ">>":
			return IntegerValue(a >> (uint(b) & 63)), nil
		case ">>>":
			return IntegerValue(int64(uint64(a) >> (uint(b) & 63))), nil
		case "\\", "%":
			if b == 0 {
				return Value{}, errors.New("TJS integer division by zero")
			}
			if op == "\\" {
				return IntegerValue(a / b), nil
			}
			return IntegerValue(a % b), nil
		}
	}
	if op == "+" && v.kind == KindOctet && other.kind == KindOctet {
		if len(v.octet) > (64<<20)-len(other.octet) {
			return Value{}, errors.New("octet exceeds size limit")
		}
		return OctetValue(append(append([]byte(nil), v.octet...), other.octet...)), nil
	}
	a, e := v.Number()
	if e != nil {
		return Value{}, e
	}
	b, e := other.Number()
	if e != nil {
		return Value{}, e
	}
	if a.kind == KindInteger && b.kind == KindInteger {
		switch op {
		case "+":
			return IntegerValue(a.integer + b.integer), nil
		case "-":
			return IntegerValue(a.integer - b.integer), nil
		case "*":
			return IntegerValue(a.integer * b.integer), nil
		}
	}
	x, e := a.Float()
	if e != nil {
		return Value{}, e
	}
	y, e := b.Float()
	if e != nil {
		return Value{}, e
	}
	switch op {
	case "+":
		return RealValue(x + y), nil
	case "-":
		return RealValue(x - y), nil
	case "*":
		return RealValue(x * y), nil
	case "/":
		return RealValue(x / y), nil
	default:
		return Value{}, fmt.Errorf("unsupported TJS binary operator %s", op)
	}
}

func (v Value) toStringUnits() UTF16 {
	if v.kind == KindString {
		return append(UTF16(nil), v.string...)
	}
	return UTF16FromString(v.Text())
}

// ParseNumber performs TJS's numeric-prefix conversion (not JavaScript Number()).
func ParseNumber(text string) Value {
	s := text
	negative := strings.HasPrefix(s, "-")
	if strings.HasPrefix(s, "+") || negative {
		s = strings.TrimLeft(s[1:], " \t\r\n\v\f\uFFFF")
	}
	sign := int64(1)
	if negative {
		sign = -1
	}
	for _, x := range []struct {
		word  string
		value Value
	}{{"true", IntegerValue(sign)}, {"false", IntegerValue(0)}, {"NaN", RealValue(math.NaN())}, {"Infinity", RealValue(float64(sign) * math.Inf(1))}} {
		if strings.HasPrefix(s, x.word) {
			rest := s[len(x.word):]
			if !isNumericWordSuffix(rest) {
				return x.value
			}
		}
	}
	radix, prefix := 10, 0
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		radix, prefix = 16, 2
	} else if strings.HasPrefix(s, "0b") || strings.HasPrefix(s, "0B") {
		radix, prefix = 2, 2
	} else if len(s) > 1 && s[0] == '0' && s[1] >= '0' && s[1] <= '9' {
		radix, prefix = 8, 1
	}
	if radix != 10 {
		if radix == 16 && (strings.ContainsAny(s, ".pP")) {
			body := s[2:]
			mant, exp, ok := strings.Cut(body, "p")
			if !ok {
				mant, exp, ok = strings.Cut(body, "P")
			}
			if !ok {
				exp = "0"
			}
			n := 0.0
			fraction := false
			scale := 1.0
			for _, c := range mant {
				if c == '.' {
					fraction = true
					continue
				}
				d, e := strconv.ParseUint(string(c), 16, 4)
				if e != nil {
					break
				}
				if fraction {
					scale /= 16
					n += float64(d) * scale
				} else {
					n = n*16 + float64(d)
				}
			}
			ei, _ := strconv.Atoi(exp)
			if ei > 4096 {
				ei = 4096
			}
			if ei < -4096 {
				ei = -4096
			}
			n *= math.Pow(2, float64(ei))
			return RealValue(n * float64(sign))
		}
		digits := ""
		for _, c := range s[prefix:] {
			if digit, err := strconv.ParseUint(string(c), radix, 8); err == nil {
				digits += string(c)
				_ = digit
			} else {
				break
			}
		}
		var n uint64
		for _, c := range digits {
			d, _ := strconv.ParseUint(string(c), radix, 8)
			n = n*uint64(radix) + d
		}
		return IntegerValue(int64(n) * sign)
	}
	end, real := 0, false
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	if end < len(s) && s[end] == '.' {
		real = true
		end++
		for end < len(s) && s[end] >= '0' && s[end] <= '9' {
			end++
		}
	}
	if end < len(s) && (s[end] == 'e' || s[end] == 'E') {
		before := end
		end++
		if end < len(s) && (s[end] == '+' || s[end] == '-') {
			end++
		}
		first := end
		for end < len(s) && s[end] >= '0' && s[end] <= '9' {
			end++
		}
		if end == first {
			end = before
		} else {
			real = true
		}
	}
	body := s[:end]
	if real {
		n, _ := strconv.ParseFloat(body, 64)
		return RealValue(n * float64(sign))
	}
	var n uint64
	for _, digit := range body {
		n = n*10 + uint64(digit-'0')
	}
	return IntegerValue(int64(n) * sign)
}

func isASCIILetter(b byte) bool { return b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z' }
func isNumericWordSuffix(s string) bool {
	if s == "" {
		return false
	}
	r, _ := utf8.DecodeRuneInString(s)
	return isASCIILetter(byte(r)) || r == '_' || r >= 0x100
}
func equalUTF16(a, b UTF16) bool {
	if len(a) != len(b) {
		return false
	}
	for i, unit := range a {
		if unit != b[i] {
			return false
		}
	}
	return true
}
func compareUTF16(a, b UTF16) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := range n {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	if len(a) < len(b) {
		return -1
	}
	if len(a) > len(b) {
		return 1
	}
	return 0
}
func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// RealText follows tjsVariant's %.15g conventions, including signed zero and
// a three-digit exponent.
func RealText(value float64) string {
	if math.IsNaN(value) {
		return "NaN"
	}
	if math.IsInf(value, 1) {
		return "+Infinity"
	}
	if math.IsInf(value, -1) {
		return "-Infinity"
	}
	if value == 0 {
		if math.Signbit(value) {
			return "-0.0"
		}
		return "+0.0"
	}
	formatted := strconv.FormatFloat(math.Abs(value), 'e', 14, 64)
	parts := strings.SplitN(formatted, "e", 2)
	mant := parts[0]
	exp, _ := strconv.Atoi(parts[1])
	sign := ""
	if value < 0 {
		sign = "-"
	}
	if exp < -4 || exp >= 15 {
		mant = strings.TrimRight(strings.TrimRight(mant, "0"), ".")
		es := "+"
		if exp < 0 {
			es = "-"
			exp = -exp
		}
		return fmt.Sprintf("%s%se%s%03d", sign, mant, es, exp)
	}
	digits := strings.ReplaceAll(mant, ".", "")
	var body string
	if exp < 0 {
		body = "0." + strings.Repeat("0", -exp-1) + digits
	} else {
		split := exp + 1
		if split >= len(digits) {
			body = digits + strings.Repeat("0", split-len(digits))
		} else {
			body = digits[:split] + "." + digits[split:]
		}
	}
	body = strings.TrimRight(strings.TrimRight(body, "0"), ".")
	return sign + body
}
