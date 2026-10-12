package tjs

import (
	"encoding/binary"
	"fmt"
	"math"
	"unicode/utf16"
)

// BytecodeConstant is one DATA-backed value referenced by an object's variant table.
type BytecodeConstant struct {
	Type  int16
	Index int16
	Value Value
}

// BytecodeObject retains the executable and dispatch metadata from one OBJS record.
type BytecodeObject struct {
	Parent, Name                                     int32
	ContextType                                      uint32
	MaxVariables, VariableReserve, MaxFrames         uint32
	ArgumentCount, UnnamedArgumentBase, CollapseBase uint32
	PropertySetter, PropertyGetter, SuperclassGetter uint32
	CodePositions, SourcePositions                   []uint32
	Code                                             []int16
	Constants                                        []BytecodeConstant
	SuperclassGetters                                []int32
	Properties                                       []BytecodeProperty
}

// BytecodeProperty binds a property string index to an object index.
type BytecodeProperty struct{ Name, Object int32 }

// DecodedBytecode is the validated TJS2100 data section and object table.
type DecodedBytecode struct {
	Info     BytecodeInfo
	Bytes    []byte
	Shorts   []int16
	Integers []int32
	Longs    []int64
	Reals    []float64
	Strings  []string
	Octets   [][]byte
	Objects  []BytecodeObject
}

// DecodeBytecode validates the complete container before decoding any records.
func DecodeBytecode(data []byte) (*DecodedBytecode, error) {
	info, err := ValidateBytecode(data)
	if err != nil {
		return nil, err
	}
	d := &DecodedBytecode{Info: info}
	r := bytecodeReader{data: data, off: 20, end: 12 + int(binary.LittleEndian.Uint32(data[16:20]))}
	readCount := func(what string) (uint32, error) { return bytecodeCount(&r, what) }
	count, _ := readCount("byte")
	d.Bytes = append([]byte(nil), data[r.off:r.off+int(count)]...)
	r.off += (int(count) + 3) &^ 3
	count, _ = readCount("short")
	d.Shorts = make([]int16, count)
	for i := range d.Shorts {
		d.Shorts[i] = int16(binary.LittleEndian.Uint16(data[r.off:]))
		r.off += 2
	}
	r.off += int(count&1) * 2
	count, _ = readCount("integer")
	d.Integers = make([]int32, count)
	for i := range d.Integers {
		d.Integers[i] = int32(binary.LittleEndian.Uint32(data[r.off:]))
		r.off += 4
	}
	count, _ = readCount("long")
	d.Longs = make([]int64, count)
	for i := range d.Longs {
		d.Longs[i] = int64(binary.LittleEndian.Uint64(data[r.off:]))
		r.off += 8
	}
	count, _ = readCount("real")
	d.Reals = make([]float64, count)
	for i := range d.Reals {
		d.Reals[i] = math.Float64frombits(binary.LittleEndian.Uint64(data[r.off:]))
		r.off += 8
	}
	count, _ = readCount("string")
	d.Strings = make([]string, count)
	for i := range d.Strings {
		n, _ := readCount("string character")
		units := make([]uint16, n)
		for j := range units {
			units[j] = binary.LittleEndian.Uint16(data[r.off:])
			r.off += 2
		}
		d.Strings[i] = string(utf16.Decode(units))
		r.off += int(n&1) * 2
	}
	count, _ = readCount("octet")
	d.Octets = make([][]byte, count)
	for i := range d.Octets {
		n, _ := readCount("octet byte")
		d.Octets[i] = append([]byte(nil), data[r.off:r.off+int(n)]...)
		r.off += (int(n) + 3) &^ 3
	}
	r = bytecodeReader{data: data, off: 12 + int(binary.LittleEndian.Uint32(data[16:20])) + 8, end: len(data)}
	r.off += 4 // top-level index; already retained by Info
	objects, _ := readBytecodeCountAt(&r, "object")
	d.Objects = make([]BytecodeObject, objects)
	for oi := range d.Objects {
		o := &d.Objects[oi]
		r.off += 8
		o.Parent, _ = r.i32("parent index")
		o.Name, _ = r.i32("object name index")
		o.ContextType, _ = r.u32("context type")
		o.MaxVariables, _ = r.u32("maximum variable count")
		o.VariableReserve, _ = r.u32("variable reserve count")
		o.MaxFrames, _ = r.u32("maximum frame count")
		o.ArgumentCount, _ = r.u32("argument count")
		o.UnnamedArgumentBase, _ = r.u32("unnamed argument base")
		o.CollapseBase, _ = r.u32("collapse base")
		o.PropertySetter, _ = r.u32("property setter index")
		o.PropertyGetter, _ = r.u32("property getter index")
		o.SuperclassGetter, _ = r.u32("superclass getter index")
		n, _ := readCount("source position")
		o.CodePositions = make([]uint32, n)
		for j := range o.CodePositions {
			o.CodePositions[j], _ = r.u32("source code position")
		}
		o.SourcePositions = make([]uint32, n)
		for j := range o.SourcePositions {
			o.SourcePositions[j], _ = r.u32("source position")
		}
		n, _ = readCount("code unit")
		o.Code = make([]int16, n)
		for j := range o.Code {
			o.Code[j] = int16(binary.LittleEndian.Uint16(data[r.off:]))
			r.off += 2
		}
		r.off += int(n&1) * 2
		n, _ = readCount("variant")
		o.Constants = make([]BytecodeConstant, n)
		for j := range o.Constants {
			typ := int16(binary.LittleEndian.Uint16(data[r.off:]))
			ix := int16(binary.LittleEndian.Uint16(data[r.off+2:]))
			r.off += 4
			c := BytecodeConstant{Type: typ, Index: ix}
			switch typ {
			case 0:
				c.Value = VoidValue
			case 1:
				c.Value = NullValue
			case 2, 10:
				c.Value = VoidValue
			case 3:
				c.Value = StringValue(d.Strings[ix])
			case 4:
				c.Value = OctetValue(d.Octets[ix])
			case 5:
				c.Value = RealValue(d.Reals[ix])
			case 6:
				c.Value = IntegerValue(int64(d.Bytes[ix]))
			case 7:
				c.Value = IntegerValue(int64(d.Shorts[ix]))
			case 8:
				c.Value = IntegerValue(int64(d.Integers[ix]))
			case 9:
				c.Value = IntegerValue(d.Longs[ix])
			}
			o.Constants[j] = c
		}
		n, _ = readCount("superclass getter")
		o.SuperclassGetters = make([]int32, n)
		for j := range o.SuperclassGetters {
			o.SuperclassGetters[j], _ = r.i32("superclass getter")
		}
		n, _ = readCount("property")
		o.Properties = make([]BytecodeProperty, n)
		for j := range o.Properties {
			o.Properties[j].Name, _ = r.i32("property name")
			o.Properties[j].Object, _ = r.i32("property object")
		}
	}
	return d, nil
}

func readBytecodeCountAt(r *bytecodeReader, what string) (uint32, error) {
	n, e := r.u32(what)
	if e != nil {
		return 0, e
	}
	if n > bytecodeMaxCount {
		return 0, fmt.Errorf("compiled TJS2 bytecode: unreasonable %s count %d", what, n)
	}
	return n, nil
}
