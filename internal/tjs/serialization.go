package tjs

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strings"
)

// RegisterSerialization installs saveStruct methods as ordinary native members.
func (v *VM) RegisterSerialization(plugin bool) error {
	v.nativeSave = true
	v.nativeSavePlugin = plugin
	for _, class := range []string{"Array", "Dictionary"} {
		receiver, ok := v.Globals[class]
		if !ok {
			var e error
			receiver, e = v.RegisterNativeClass(class)
			if e != nil {
				return e
			}
		}
		methods := []string{"loadStruct", "saveStruct"}
		if class == "Array" {
			methods = []string{"save", "load", "loadStruct", "saveStruct"}
		}
		if plugin {
			if class == "Array" {
				methods = []string{"save2", "saveStruct2", "toStructString"}
			} else {
				methods = []string{"saveStruct2", "toStructString"}
			}
		}
		for _, name := range methods {
			if e := v.RegisterNativeMethod(receiver, name, "Serialization."+class+"."+name); e != nil {
				return e
			}
		}
	}
	return nil
}
func (v *VM) RegisterSaveStruct() error       { return v.RegisterSerialization(true) }
func (v *VM) RegisterBinarySerializer() error { return v.RegisterSerialization(false) }

func quoteTJS(s string, escaped bool) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString("\\\"")
		case '\\':
			b.WriteString("\\\\")
		case '\n':
			if escaped {
				b.WriteString("\\n")
			} else {
				b.WriteRune(r)
			}
		case '\r':
			if escaped {
				b.WriteString("\\r")
			} else {
				b.WriteRune(r)
			}
		case '\t':
			if escaped {
				b.WriteString("\\t")
			} else {
				b.WriteRune(r)
			}
		default:
			if escaped && r < 32 {
				fmt.Fprintf(&b, "\\x%02x", r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

type textWriter struct {
	v      *VM
	core   bool
	typed  bool
	lf     string
	stack  map[ObjectID]bool
	depth  int
	budget *uint64
}

func serializationRealHex(value float64) string {
	if math.IsNaN(value) || math.IsInf(value, 0) || value == 0 {
		return RealText(value)
	}
	bits := math.Float64bits(value)
	sign := ""
	if value < 0 {
		sign = "-"
	}
	fraction := bits & ((uint64(1) << 52) - 1)
	exponent := int((bits>>52)&0x7ff) - 1023
	return fmt.Sprintf("%s0x1.%013Xp%d", sign, fraction, exponent)
}

func (w *textWriter) charge() error {
	if w.budget != nil {
		if *w.budget == 0 {
			return vmAbortError(errors.New("serialization budget exhausted"))
		}
		(*w.budget)--
	}
	if w.depth >= 128 {
		return vmAbortError(errors.New("serialization nesting limit exceeded"))
	}
	return nil
}
func (w *textWriter) value(x Value, indent int) (string, error) {
	if e := w.charge(); e != nil {
		return "", e
	}
	switch x.Kind() {
	case KindVoid:
		return "void", nil
	case KindInteger:
		if w.typed {
			return "int " + x.Text(), nil
		}
		return x.Text(), nil
	case KindReal:
		if w.typed {
			return "real " + serializationRealHex(x.RealValue()) + " /* " + x.Text() + " */", nil
		}
		return x.Text(), nil
	case KindString:
		return quoteTJS(x.Text(), w.core), nil
	case KindOctet:
		var b strings.Builder
		b.WriteString("<% ")
		for _, c := range x.Octets() {
			if w.core {
				fmt.Fprintf(&b, "%02X ", c)
			} else {
				fmt.Fprintf(&b, "%02x ", c)
			}
		}
		b.WriteString("%>")
		return b.String(), nil
	case KindObject:
		if x.IsNull() {
			return "null", nil
		}
		return w.object(x.ObjectRef().Object, indent)
	}
	return "", errors.New("invalid TJS value")
}
func (w *textWriter) object(id ObjectID, indent int) (string, error) {
	if e := w.charge(); e != nil {
		return "", e
	}
	if w.stack[id] {
		if w.core {
			return "null /* object recursion detected */", nil
		}
		return "", errors.New("cyclic object in saveStruct serialization")
	}
	o, e := w.v.obj(id)
	if e != nil {
		return "", e
	}
	m := w.v.meta[id]
	w.stack[id] = true
	defer delete(w.stack, id)
	if m != nil && m.category == CategoryArray {
		var b strings.Builder
		b.WriteByte('[')
		for i, x := range m.array {
			if i > 0 {
				b.WriteByte(',')
				if !w.typed {
					b.WriteString(w.lf)
				}
			}
			s, e := w.value(x, indent+1)
			if e != nil {
				return "", e
			}
			b.WriteString(s)
		}
		b.WriteByte(']')
		return b.String(), nil
	}
	entries := o.Entries()
	var b strings.Builder
	if w.core {
		b.WriteString("(const) ")
	}
	b.WriteString("%[")
	for i, en := range entries {
		if i > 0 {
			b.WriteByte(',')
			b.WriteString(w.lf)
		}
		if w.core {
			b.WriteString(strings.Repeat(" ", indent+1))
		}
		b.WriteString(quoteTJS(en.Name, w.core))
		if w.core {
			b.WriteString(" => ")
		} else {
			b.WriteString("=>")
		}
		s, e := w.value(en.Value, indent+1)
		if e != nil {
			return "", e
		}
		b.WriteString(s)
	}
	if len(entries) > 0 && w.core {
		b.WriteString(w.lf)
		b.WriteString(strings.Repeat(" ", indent))
	}
	b.WriteByte(']')
	return b.String(), nil
}
func (v *VM) SerializeValue(value Value, core bool, budget *uint64) (string, error) {
	return (&textWriter{v: v, core: core, lf: "\r\n", stack: make(map[ObjectID]bool), budget: budget}).value(value, 0)
}
func (v *VM) SerializeStructString(value Value, budget *uint64) (string, error) {
	return (&textWriter{v: v, typed: true, lf: "\r\n", stack: make(map[ObjectID]bool), budget: budget}).value(value, 0)
}
func (v *VM) Serialize(value Value) (string, error) { return v.SerializeValue(value, true, nil) }

func (v *VM) serializationCall(name string, receiver Value, args []Value, h Host, b *uint64, needed bool) (Value, error) {
	if !needed && strings.HasSuffix(name, "toStructString") {
		return VoidValue, nil
	}
	if strings.HasSuffix(name, ".loadStruct") {
		if len(args) == 0 {
			return VoidValue, ErrBadParamCount
		}
		if h == nil {
			return VoidValue, errors.New("loadStruct requires a VM host")
		}
		operation := "Serialization." + name
		if rr, ok := h.(HostResult); ok {
			return rr.CallWithResult(v, operation, receiver, args, b, needed)
		}
		if cc, ok := h.(HostContext); ok {
			return cc.CallWithContext(v, operation, receiver, args, b)
		}
		return h.Call(v, operation, args, b)
	}
	if strings.HasSuffix(name, ".toStructString") {
		text, e := v.SerializeStructString(receiver, b)
		if e != nil {
			return VoidValue, e
		}
		return StringValue(text), nil
	}
	core := strings.HasSuffix(name, ".save") || strings.HasSuffix(name, ".load") || strings.HasSuffix(name, ".saveStruct")
	if strings.HasSuffix(name, ".load") {
		return receiver, nil
	}
	text, e := v.SerializeValue(receiver, core, b)
	if e != nil {
		return VoidValue, e
	}
	if len(args) == 0 {
		return VoidValue, errors.New("save requires stream")
	}
	mode := VoidValue
	if len(args) > 1 {
		mode = args[1]
	}
	if h == nil {
		h = nopHost{}
	}
	if rr, ok := h.(HostResult); ok {
		_, e = rr.CallWithResult(v, "TextStream.write", receiver, []Value{args[0], StringValue(text), mode}, b, true)
	} else {
		_, e = h.Call(v, "TextStream.write", []Value{args[0], StringValue(text), mode}, b)
	}
	if e != nil {
		return VoidValue, e
	}
	return receiver, nil
}

var binaryHeader = []byte{'K', 'B', 'A', 'D', '1', '0', '0', 0}

func IsBinary(data []byte) bool {
	return len(data) >= len(binaryHeader) && bytes.Equal(data[:len(binaryHeader)], binaryHeader)
}
func (v *VM) BinarySerialize(value Value) ([]byte, error) {
	var b bytes.Buffer
	b.Write(binaryHeader)
	if e := v.writeBinary(&b, value, map[ObjectID]bool{}); e != nil {
		return nil, e
	}
	return b.Bytes(), nil
}
func (v *VM) writeBinary(b *bytes.Buffer, x Value, seen map[ObjectID]bool) error {
	switch x.Kind() {
	case KindVoid:
		b.WriteByte(0xc1)
	case KindObject:
		if x.IsNull() {
			b.WriteByte(0xc0)
			return nil
		}
		id := x.ObjectRef().Object
		if seen[id] {
			return errors.New("cyclic object in binary serialization")
		}
		seen[id] = true
		defer delete(seen, id)
		m := v.meta[id]
		if m != nil && m.category == CategoryArray {
			if len(m.array) > 65535 {
				return errors.New("binary array too large")
			}
			b.WriteByte(0xdc)
			binary.Write(b, binary.LittleEndian, uint16(len(m.array)))
			for _, y := range m.array {
				if e := v.writeBinary(b, y, seen); e != nil {
					return e
				}
			}
			return nil
		}
		o, e := v.obj(id)
		if e != nil {
			return e
		}
		es := o.Entries()
		if len(es) > 65535 {
			return errors.New("binary dictionary too large")
		}
		b.WriteByte(0xde)
		binary.Write(b, binary.LittleEndian, uint16(len(es)))
		for _, en := range es {
			if e := v.writeBinary(b, StringValue(en.Name), seen); e != nil {
				return e
			}
			if e := v.writeBinary(b, en.Value, seen); e != nil {
				return e
			}
		}
	case KindInteger:
		n := x.IntegerValue()
		if n >= 0 && n < 128 {
			b.WriteByte(byte(n))
		} else {
			b.WriteByte(0xd3)
			binary.Write(b, binary.LittleEndian, uint64(n))
		}
	case KindReal:
		b.WriteByte(0xcb)
		binary.Write(b, binary.LittleEndian, math.Float64bits(x.RealValue()))
	case KindString:
		u := x.UTF16()
		if len(u) < 32 {
			b.WriteByte(byte(0xa0 | len(u)))
		} else {
			b.WriteByte(0xda)
			binary.Write(b, binary.LittleEndian, uint16(len(u)))
		}
		for _, c := range u {
			binary.Write(b, binary.LittleEndian, c)
		}
	case KindOctet:
		q := x.Octets()
		if len(q) < 6 {
			b.WriteByte(byte(0xd4 + len(q)))
		} else {
			b.WriteByte(0xda)
			binary.Write(b, binary.LittleEndian, uint16(len(q)))
		}
		b.Write(q)
	default:
		return errors.New("unsupported binary value")
	}
	return nil
}

func (v *VM) BinaryDeserialize(data []byte) (Value, error) {
	if !IsBinary(data) {
		return VoidValue, errors.New("invalid K-BAD binary header")
	}
	r := binaryReader{data: data[len(binaryHeader):], vm: v}
	return r.value()
}

type binaryReader struct {
	data []byte
	pos  int
	vm   *VM
}

func (r *binaryReader) take(n int) ([]byte, error) {
	if n < 0 || r.pos+n > len(r.data) {
		return nil, errors.New("truncated K-BAD payload")
	}
	x := r.data[r.pos : r.pos+n]
	r.pos += n
	return x, nil
}
func (r *binaryReader) u16() (uint16, error) {
	x, e := r.take(2)
	if e != nil {
		return 0, e
	}
	return binary.LittleEndian.Uint16(x), nil
}
func (r *binaryReader) u64() (uint64, error) {
	x, e := r.take(8)
	if e != nil {
		return 0, e
	}
	return binary.LittleEndian.Uint64(x), nil
}
func (r *binaryReader) value() (Value, error) {
	x, e := r.take(1)
	if e != nil {
		return VoidValue, e
	}
	tag := x[0]
	switch {
	case tag <= 0x7f:
		return IntegerValue(int64(tag)), nil
	case tag >= 0xe0:
		return IntegerValue(int64(int8(tag))), nil
	case tag == 0xc0:
		return NullValue, nil
	case tag == 0xc1:
		return VoidValue, nil
	case tag == 0xcb:
		n, e := r.u64()
		if e != nil {
			return VoidValue, e
		}
		return RealValue(math.Float64frombits(n)), nil
	case tag == 0xd3:
		n, e := r.u64()
		if e != nil {
			return VoidValue, e
		}
		return IntegerValue(int64(n)), nil
	case tag >= 0xa0 && tag <= 0xbf:
		n := int(tag & 31)
		raw, e := r.take(n * 2)
		if e != nil {
			return VoidValue, e
		}
		u := make([]uint16, n)
		for i := range u {
			u[i] = binary.LittleEndian.Uint16(raw[i*2:])
		}
		return StringUnits(u), nil
	case tag == 0xdc:
		n, e := r.u16()
		if e != nil {
			return VoidValue, e
		}
		a := make([]Value, n)
		for i := range a {
			a[i], e = r.value()
			if e != nil {
				return VoidValue, e
			}
		}
		return r.vm.NewArray(a)
	case tag == 0xde:
		n, e := r.u16()
		if e != nil {
			return VoidValue, e
		}
		d, e := r.vm.NewDictionary()
		if e != nil {
			return VoidValue, e
		}
		for i := 0; i < int(n); i++ {
			k, e := r.value()
			if e != nil {
				return VoidValue, e
			}
			x, e := r.value()
			if e != nil {
				return VoidValue, e
			}
			if e = r.vm.setMember(d.ObjectRef().Object, k, x); e != nil {
				return VoidValue, e
			}
		}
		return d, nil
	}
	return VoidValue, fmt.Errorf("unsupported K-BAD tag 0x%02x", tag)
}
