package tjs

import (
	"encoding/binary"
	"fmt"
)

const (
	bytecodeFileTag  = "TJS2"
	bytecodeVersion  = "100\x00"
	bytecodeMaxCount = 1 << 24
)

// BytecodeInfo describes a structurally validated TJS2100 file. The current
// interpreter does not yet execute the legacy intercode instruction set.
type BytecodeInfo struct {
	ObjectCount int
	TopLevel    int32
}

type bytecodeReader struct {
	data []byte
	off  int
	end  int
}

func (r *bytecodeReader) need(n int, what string) error {
	if n < 0 || r.off > r.end || n > r.end-r.off {
		return fmt.Errorf("compiled TJS2 bytecode: truncated %s at offset %d", what, r.off)
	}
	return nil
}
func (r *bytecodeReader) u32(what string) (uint32, error) {
	if err := r.need(4, what); err != nil {
		return 0, err
	}
	v := binary.LittleEndian.Uint32(r.data[r.off : r.off+4])
	r.off += 4
	return v, nil
}
func (r *bytecodeReader) i32(what string) (int32, error) {
	v, err := r.u32(what)
	return int32(v), err
}
func (r *bytecodeReader) skip(n uint64, what string) error {
	if n > uint64(r.end-r.off) {
		return fmt.Errorf("compiled TJS2 bytecode: invalid %s length at offset %d", what, r.off)
	}
	r.off += int(n)
	return nil
}
func bytecodeCount(r *bytecodeReader, what string) (uint32, error) {
	v, err := r.u32(what)
	if err != nil {
		return 0, err
	}
	if v > bytecodeMaxCount {
		return 0, fmt.Errorf("compiled TJS2 bytecode: unreasonable %s count %d", what, v)
	}
	return v, nil
}

// ValidateBytecode validates the TJS2100 container and all DATA/OBJS records
// before any bytecode can be dispatched. It never treats bytecode as text.
func ValidateBytecode(data []byte) (BytecodeInfo, error) {
	var info BytecodeInfo
	if len(data) < 20 {
		return info, fmt.Errorf("compiled TJS2 bytecode: truncated file header")
	}
	if string(data[:4]) != bytecodeFileTag {
		return info, fmt.Errorf("compiled TJS2 bytecode: invalid TJS2 tag")
	}
	if string(data[4:8]) != bytecodeVersion {
		return info, fmt.Errorf("compiled TJS2 bytecode: unsupported version %q", data[4:8])
	}
	if uint64(binary.LittleEndian.Uint32(data[8:12])) != uint64(len(data)) {
		return info, fmt.Errorf("compiled TJS2 bytecode: declared file length does not match input")
	}
	if string(data[12:16]) != "DATA" {
		return info, fmt.Errorf("compiled TJS2 bytecode: missing DATA section")
	}
	dataSize := uint64(binary.LittleEndian.Uint32(data[16:20]))
	if dataSize < 8 || dataSize > uint64(len(data)-12) {
		return info, fmt.Errorf("compiled TJS2 bytecode: invalid DATA section length %d", dataSize)
	}
	dataEnd := 12 + int(dataSize)
	r := bytecodeReader{data: data, off: 20, end: dataEnd}
	byteCount, err := bytecodeCount(&r, "byte")
	if err != nil {
		return info, err
	}
	if err = r.skip(uint64((byteCount+3)&^3), "byte array"); err != nil {
		return info, err
	}
	shortCount, err := bytecodeCount(&r, "short")
	if err != nil {
		return info, err
	}
	if err = r.skip(uint64(shortCount)*2+uint64(shortCount&1)*2, "short array"); err != nil {
		return info, err
	}
	longCount, err := bytecodeCount(&r, "integer")
	if err != nil {
		return info, err
	}
	if err = r.skip(uint64(longCount)*4, "integer array"); err != nil {
		return info, err
	}
	longLongCount, err := bytecodeCount(&r, "long")
	if err != nil {
		return info, err
	}
	if err = r.skip(uint64(longLongCount)*8, "long array"); err != nil {
		return info, err
	}
	doubleCount, err := bytecodeCount(&r, "real")
	if err != nil {
		return info, err
	}
	if err = r.skip(uint64(doubleCount)*8, "real array"); err != nil {
		return info, err
	}
	stringCount, err := bytecodeCount(&r, "string")
	if err != nil {
		return info, err
	}
	for i := uint32(0); i < stringCount; i++ {
		chars, e := bytecodeCount(&r, "string character")
		if e != nil {
			return info, e
		}
		if e = r.skip(uint64(chars)*2, "string"); e != nil {
			return info, e
		}
		if e = r.skip(uint64(chars&1)*2, "string alignment"); e != nil {
			return info, e
		}
	}
	octetCount, err := bytecodeCount(&r, "octet")
	if err != nil {
		return info, err
	}
	for i := uint32(0); i < octetCount; i++ {
		n, e := bytecodeCount(&r, "octet byte")
		if e != nil {
			return info, e
		}
		padded := (uint64(n) + 3) &^ uint64(3)
		if e = r.skip(padded, "octet"); e != nil {
			return info, e
		}
	}
	if r.off != dataEnd {
		return info, fmt.Errorf("compiled TJS2 bytecode: DATA section has %d trailing or missing bytes", dataEnd-r.off)
	}
	if dataEnd+8 > len(data) || string(data[dataEnd:dataEnd+4]) != "OBJS" {
		return info, fmt.Errorf("compiled TJS2 bytecode: missing OBJS section")
	}
	objSize := uint64(binary.LittleEndian.Uint32(data[dataEnd+4 : dataEnd+8]))
	if objSize < 8 || objSize != uint64(len(data)-dataEnd) {
		return info, fmt.Errorf("compiled TJS2 bytecode: invalid OBJS section length %d at offset %d in %d-byte file", objSize, dataEnd, len(data))
	}
	r = bytecodeReader{data: data, off: dataEnd + 8, end: len(data)}
	top, e := r.i32("top-level object index")
	if e != nil {
		return info, e
	}
	objects, e := bytecodeCount(&r, "object")
	if e != nil {
		return info, e
	}
	info.ObjectCount = int(objects)
	info.TopLevel = top
	if top < -1 || top >= int32(objects) {
		return info, fmt.Errorf("compiled TJS2 bytecode: invalid top-level object index %d", top)
	}
	for i := uint32(0); i < objects; i++ {
		tag, e := r.u32("object tag")
		if e != nil {
			return info, e
		}
		if tag != binary.LittleEndian.Uint32([]byte("TJS2")) {
			return info, fmt.Errorf("compiled TJS2 bytecode: invalid object %d tag", i)
		}
		// Per-object byte length is reserved in this format; C++ reads fields sequentially.
		_, e = r.u32("object size")
		if e != nil {
			return info, e
		}
		parent, e := r.i32("parent index")
		if e != nil {
			return info, e
		}
		if parent < -1 || parent >= int32(objects) {
			return info, fmt.Errorf("compiled TJS2 bytecode: invalid parent index %d", parent)
		}
		name, e := r.i32("object name index")
		if e != nil {
			return info, e
		}
		if name < 0 || uint32(name) >= stringCount {
			return info, fmt.Errorf("compiled TJS2 bytecode: invalid object name index %d", name)
		}
		for _, field := range []string{"context type", "maximum variable count", "variable reserve count", "maximum frame count", "argument count", "unnamed argument base", "collapse base", "property setter index", "property getter index", "superclass getter index"} {
			if _, e = r.u32(field); e != nil {
				return info, e
			}
		}
		sources, e := bytecodeCount(&r, "source position")
		if e != nil {
			return info, e
		}
		if e = r.skip(uint64(sources)*8, "source positions"); e != nil {
			return info, e
		}
		codeCount, e := bytecodeCount(&r, "code unit")
		if e != nil {
			return info, e
		}
		if e = r.skip(uint64(codeCount)*2+uint64(codeCount&1)*2, "code"); e != nil {
			return info, e
		}
		variants, e := bytecodeCount(&r, "variant")
		if e != nil {
			return info, e
		}
		if e = r.need(int(uint64(variants)*4), "variant data"); e != nil {
			return info, e
		}
		for j := uint32(0); j < variants; j++ {
			base := r.off + int(j)*4
			t := int16(binary.LittleEndian.Uint16(data[base : base+2]))
			ix := int16(binary.LittleEndian.Uint16(data[base+2 : base+4]))
			var limit uint32
			switch t {
			case 3:
				limit = stringCount
			case 4:
				limit = octetCount
			case 5:
				limit = doubleCount
			case 6:
				limit = byteCount
			case 7:
				limit = shortCount
			case 8:
				limit = longCount
			case 9:
				limit = longLongCount
			case 2, 10:
				if ix < 0 || uint32(ix) >= objects {
					return info, fmt.Errorf("compiled TJS2 bytecode: invalid object variant index %d", ix)
				}
			case 0, 1:
			default:
				return info, fmt.Errorf("compiled TJS2 bytecode: unknown variant type %d", t)
			}
			if limit != 0 && (ix < 0 || uint32(ix) >= limit) {
				return info, fmt.Errorf("compiled TJS2 bytecode: invalid constant index %d for type %d", ix, t)
			}
		}
		r.off += int(variants) * 4
		getters, e := bytecodeCount(&r, "superclass getter")
		if e != nil {
			return info, e
		}
		if e = r.skip(uint64(getters)*4, "superclass getters"); e != nil {
			return info, e
		}
		props, e := bytecodeCount(&r, "property")
		if e != nil {
			return info, e
		}
		if e = r.need(int(uint64(props)*8), "properties"); e != nil {
			return info, e
		}
		for j := uint32(0); j < props; j++ {
			base := r.off + int(j)*8
			pn := int32(binary.LittleEndian.Uint32(data[base : base+4]))
			po := int32(binary.LittleEndian.Uint32(data[base+4 : base+8]))
			if pn < 0 || uint32(pn) >= stringCount || po < 0 || uint32(po) >= objects {
				return info, fmt.Errorf("compiled TJS2 bytecode: invalid property reference")
			}
		}
		r.off += int(props) * 8
	}
	if r.off != r.end {
		return info, fmt.Errorf("compiled TJS2 bytecode: OBJS section has %d trailing bytes", r.end-r.off)
	}
	return info, nil
}
