package assets

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"unicode/utf8"
)

// ErrInvalidPSB identifies malformed or unsupported PSB input.
var ErrInvalidPSB = errors.New("invalid PSB")

// Value is a decoded PSB value. Values are represented by the corresponding
// Go scalar, []Value, map[string]Value, or Resource value.
type Value any

// Resource refers to an opaque resource in the PSB resource chunk area.
type Resource struct {
	ResourceIndex uint32 `json:"resource_index"`
	Offset        int    `json:"offset"`
	Length        int    `json:"length"`
}

// Document is a PSB v2, v3, or v4 structured-data document.
type Document struct {
	Version uint16      `json:"version"`
	Root    Value       `json:"root"`
	Objects []PSBObject `json:"-"`
}

// PSBObject records a decoded dictionary and its key path in PSB traversal
// order. Motion snapshots use this order when resolving numeric layer
// references, matching the reference parser's depth-first dictionary walk.
type PSBObject struct {
	Path  []string
	Value map[string]Value
}

type psbParser struct {
	data         []byte
	names        map[uint32]string
	strings      []uint32
	stringsData  int
	chunkOffsets []uint32
	chunkLengths []uint32
	chunkData    int
	remaining    int
	outputBytes  int
	active       map[int]bool
	objects      []PSBObject
}

func psbErr(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidPSB, fmt.Sprintf(format, args...))
}

func psbSlice(data []byte, offset, length int) ([]byte, error) {
	if offset < 0 || length < 0 || offset > len(data) || length > len(data)-offset {
		return nil, psbErr("data range outside input")
	}
	return data[offset : offset+length], nil
}

func psbUint(data []byte, offset, width int) (uint64, error) {
	if width < 0 || width > 8 {
		return 0, psbErr("invalid integer width %d", width)
	}
	b, err := psbSlice(data, offset, width)
	if err != nil {
		return 0, err
	}
	var value uint64
	for i, c := range b {
		value |= uint64(c) << (8 * i)
	}
	return value, nil
}

func psbArray(data []byte, offset int) ([]uint32, int, error) {
	t, err := psbUint(data, offset, 1)
	if err != nil {
		return nil, 0, err
	}
	if t < 13 || t > 20 {
		return nil, 0, psbErr("invalid array count type %d", t)
	}
	width := int(t - 12)
	count64, err := psbUint(data, offset+1, width)
	if err != nil {
		return nil, 0, err
	}
	if count64 > 1_000_000 {
		return nil, 0, psbErr("array too large")
	}
	count := int(count64)
	et, err := psbUint(data, offset+1+width, 1)
	if err != nil {
		return nil, 0, err
	}
	if et < 13 || et > 20 {
		return nil, 0, psbErr("invalid array element type %d", et)
	}
	elem := int(et - 12)
	if count > (int(^uint(0)>>1)-offset-2-width)/elem {
		return nil, 0, psbErr("array size overflows input range")
	}
	start := offset + 2 + width
	length := count * elem
	bytes, err := psbSlice(data, start, length)
	if err != nil {
		return nil, 0, err
	}
	values := make([]uint32, count)
	for i := range values {
		v, err := psbUint(bytes, i*elem, elem)
		if err != nil {
			return nil, 0, err
		}
		if v > uint64(^uint32(0)) {
			return nil, 0, psbErr("array element exceeds 32-bit offset")
		}
		values[i] = uint32(v)
	}
	return values, start + length, nil
}

// ParsePSBWithSeed decrypts an E-mote PSB v2 metadata region with the
// ResourceManager seed before decoding it.
func ParsePSBWithSeed(data []byte, seed int32) (*Document, error) {
	if seed <= 0 {
		return ParsePSB(data)
	}
	decrypted, err := decryptEmotePSB(data, uint32(seed))
	if err != nil {
		return nil, err
	}
	return ParsePSB(decrypted)
}

func decryptEmotePSB(data []byte, seed uint32) ([]byte, error) {
	if len(data) < 40 || string(data[:4]) != "PSB\x00" {
		return nil, psbErr("not a PSB container")
	}
	version := binary.LittleEndian.Uint16(data[4:6])
	if version != 2 {
		return nil, psbErr("encrypted seed is unsupported for version %d", version)
	}
	offsetEncrypt := binary.LittleEndian.Uint32(data[8:12])
	offsetChunkOffsets := binary.LittleEndian.Uint32(data[24:28])
	if uint64(offsetEncrypt) > uint64(len(data)) ||
		uint64(offsetChunkOffsets) > uint64(len(data)) ||
		offsetEncrypt >= offsetChunkOffsets {
		return nil, psbErr("invalid encrypted region")
	}

	out := append([]byte(nil), data...)
	key := [4]uint32{0x075BCD15, 0x159A55E5, 0x1F123BB5, seed}
	var value uint32
	for i := int(offsetEncrypt); i < int(offsetChunkOffsets); i++ {
		if value == 0 {
			b := key[3]
			a := key[0] ^ key[0]<<11
			key[0], key[1], key[2] = key[1], key[2], b
			c := a ^ b ^ ((a ^ (b >> 11)) >> 8)
			key[3], value = c, c
		}
		out[i] ^= byte(value)
		value >>= 8
	}
	return out, nil
}

// ParsePSB decodes a bounded PSB v2, v3, or v4 document.
func ParsePSB(data []byte) (*Document, error) {
	magic, err := psbSlice(data, 0, 4)
	if err != nil || string(magic) != "PSB\x00" {
		return nil, psbErr("not a PSB container")
	}
	version, err := psbUint(data, 4, 2)
	if err != nil {
		return nil, err
	}
	if version != 2 && version != 3 && version != 4 {
		return nil, psbErr("unsupported version %d", version)
	}
	encrypted, err := psbUint(data, 6, 2)
	if err != nil {
		return nil, err
	}
	if encrypted != 0 {
		return nil, psbErr("encrypted headers are unsupported")
	}
	headerLength := uint64(40)
	if version == 3 {
		headerLength = 44
	} else if version == 4 {
		headerLength = 56
	}
	var offsets [8]int
	for i := range offsets {
		n, err := psbUint(data, 8+i*4, 4)
		if err != nil {
			return nil, err
		}
		if i == 0 && n == 0 {
			continue
		}
		if n > uint64(len(data)) || n < headerLength {
			return nil, psbErr("invalid header offset")
		}
		offsets[i] = int(n)
	}
	chunkData := offsets[6]
	metadata, err := psbSlice(data, 0, chunkData)
	if err != nil {
		return nil, err
	}
	charset, next, err := psbArray(metadata, offsets[1])
	if err != nil {
		return nil, err
	}
	nameData, next, err := psbArray(metadata, next)
	if err != nil {
		return nil, err
	}
	nameIndexes, _, err := psbArray(metadata, next)
	if err != nil {
		return nil, err
	}
	names, err := psbDecodeNames(charset, nameData, nameIndexes)
	if err != nil {
		return nil, err
	}
	stringsTable, _, err := psbArray(metadata, offsets[2])
	if err != nil {
		return nil, err
	}
	chunkOffsets, _, err := psbArray(metadata, offsets[4])
	if err != nil {
		return nil, err
	}
	chunkLengths, _, err := psbArray(metadata, offsets[5])
	if err != nil {
		return nil, err
	}
	if len(chunkOffsets) != len(chunkLengths) {
		return nil, psbErr("chunk table lengths disagree")
	}
	for i := range chunkOffsets {
		o := uint64(chunkData) + uint64(chunkOffsets[i])
		n := uint64(chunkLengths[i])
		if o > uint64(len(data)) || n > uint64(len(data))-o {
			return nil, psbErr("resource chunk outside input")
		}
	}
	parser := &psbParser{
		data: metadata, names: names, strings: stringsTable, stringsData: offsets[3],
		chunkOffsets: chunkOffsets, chunkLengths: chunkLengths, chunkData: chunkData,
		remaining: 1_000_000, outputBytes: 256 << 20, active: make(map[int]bool),
	}
	root, err := parser.value(offsets[7], 0, nil)
	if err != nil {
		return nil, err
	}
	return &Document{Version: uint16(version), Root: root, Objects: parser.objects}, nil
}

func psbDecodeNames(charset, nameData, nameIndexes []uint32) (map[uint32]string, error) {
	names := make(map[uint32]string, len(nameIndexes))
	remainingNameBytes := 64 << 20
	for id, start := range nameIndexes {
		if uint64(start) >= uint64(len(nameData)) {
			return nil, psbErr("name index outside name data")
		}
		nameBytes := make([]byte, 0, 32)
		seen := make(map[uint32]struct{})
		index := nameData[start]
		for index != 0 {
			if uint64(index) >= uint64(len(nameData)) {
				return nil, psbErr("name reference outside name data")
			}
			if _, exists := seen[index]; exists {
				return nil, psbErr("cyclic name data")
			}
			seen[index] = struct{}{}
			if len(nameBytes) >= 4096 {
				return nil, psbErr("name exceeds length limit")
			}
			code := nameData[index]
			if uint64(code) >= uint64(len(charset)) {
				return nil, psbErr("name character outside charset")
			}
			delta := charset[code]
			nameBytes = append(nameBytes, byte(index-delta))
			index = code
		}
		for left, right := 0, len(nameBytes)-1; left < right; left, right = left+1, right-1 {
			nameBytes[left], nameBytes[right] = nameBytes[right], nameBytes[left]
		}
		if len(nameBytes) > remainingNameBytes {
			return nil, psbErr("name table allocation budget exceeded")
		}
		remainingNameBytes -= len(nameBytes)
		names[uint32(id)] = string(nameBytes)
	}
	return names, nil
}

func (p *psbParser) allocate(n int) error {
	if n < 0 || n > p.outputBytes {
		return psbErr("output allocation budget exceeded")
	}
	p.outputBytes -= n
	return nil
}

func (p *psbParser) offset(base int, delta uint32) (int, error) {
	if base < 0 || base > len(p.data) || uint64(delta) > uint64(len(p.data)-base) {
		return 0, psbErr("value offset outside metadata")
	}
	return base + int(delta), nil
}

func (p *psbParser) value(offset, depth int, path []string) (Value, error) {
	if depth >= 128 || p.remaining <= 0 {
		return nil, psbErr("object budget exceeded")
	}
	p.remaining--
	if p.active[offset] {
		return nil, psbErr("cyclic value offset")
	}
	p.active[offset] = true
	defer delete(p.active, offset)
	tag, err := psbUint(p.data, offset, 1)
	if err != nil {
		return nil, err
	}
	switch {
	case tag == 1:
		return nil, nil
	case tag == 2:
		return true, nil
	case tag == 3:
		return false, nil
	case tag == 4:
		return int64(0), nil
	case tag >= 5 && tag <= 12:
		width := int(tag - 4)
		n, err := psbUint(p.data, offset+1, width)
		if err != nil {
			return nil, err
		}
		if width < 8 {
			bits := uint(width * 8)
			if n&(uint64(1)<<(bits-1)) != 0 {
				n |= ^uint64(0) << bits
			}
		}
		return int64(n), nil
	case tag >= 0x15 && tag <= 0x18:
		id, err := psbUint(p.data, offset+1, int(tag-0x14))
		if err != nil {
			return nil, err
		}
		if id >= uint64(len(p.strings)) {
			return nil, psbErr("string ID outside table")
		}
		pos64 := uint64(p.stringsData) + uint64(p.strings[id])
		if pos64 > uint64(len(p.data)) {
			return nil, psbErr("string outside metadata")
		}
		rest := p.data[int(pos64):]
		end := 0
		for end < len(rest) && rest[end] != 0 {
			end++
		}
		if end == len(rest) {
			return nil, psbErr("unterminated string")
		}
		if err := p.allocate(end); err != nil {
			return nil, err
		}
		if !utf8.Valid(rest[:end]) {
			return nil, psbErr("invalid UTF-8 string")
		}
		return string(rest[:end]), nil
	case tag >= 0x19 && tag <= 0x1c:
		id, err := psbUint(p.data, offset+1, int(tag-0x18))
		if err != nil {
			return nil, err
		}
		if id >= uint64(len(p.chunkOffsets)) {
			return nil, psbErr("resource ID outside table")
		}
		o := uint64(p.chunkData) + uint64(p.chunkOffsets[id])
		return Resource{ResourceIndex: uint32(id), Offset: int(o), Length: int(p.chunkLengths[id])}, nil
	case tag == 0x1d:
		return float64(0), nil
	case tag == 0x1e:
		n, err := psbUint(p.data, offset+1, 4)
		if err != nil {
			return nil, err
		}
		return float64(math.Float32frombits(uint32(n))), nil
	case tag == 0x1f:
		n, err := psbUint(p.data, offset+1, 8)
		if err != nil {
			return nil, err
		}
		return math.Float64frombits(n), nil
	case tag == 0x20:
		items, base, err := psbArray(p.data, offset+1)
		if err != nil {
			return nil, err
		}
		if len(items) > p.outputBytes/16 {
			return nil, psbErr("list allocation budget exceeded")
		}
		if err := p.allocate(len(items) * 16); err != nil {
			return nil, err
		}
		result := make([]Value, len(items))
		for i, item := range items {
			child, offsetErr := p.offset(base, item)
			if offsetErr != nil {
				return nil, offsetErr
			}
			result[i], err = p.value(child, depth+1, append(path, fmt.Sprintf("%d", i)))
			if err != nil {
				return nil, err
			}
		}
		return result, nil
	case tag == 0x21:
		keys, next, err := psbArray(p.data, offset+1)
		if err != nil {
			return nil, err
		}
		values, base, err := psbArray(p.data, next)
		if err != nil {
			return nil, err
		}
		if len(keys) != len(values) {
			return nil, psbErr("object keys and values disagree")
		}
		if len(keys) > p.outputBytes/128 {
			return nil, psbErr("object allocation budget exceeded")
		}
		if err := p.allocate(len(keys) * 128); err != nil {
			return nil, err
		}
		result := make(map[string]Value, len(keys))
		p.objects = append(p.objects, PSBObject{Path: append([]string(nil), path...), Value: result})
		for i, key := range keys {
			name, ok := p.names[key]
			if !ok {
				return nil, psbErr("object key missing from name table")
			}
			if err := p.allocate(len(name)); err != nil {
				return nil, err
			}
			if _, exists := result[name]; exists {
				return nil, psbErr("duplicate object key")
			}
			child, offsetErr := p.offset(base, values[i])
			if offsetErr != nil {
				return nil, offsetErr
			}
			v, err := p.value(child, depth+1, append(path, name))
			if err != nil {
				return nil, err
			}
			result[name] = v
		}
		return result, nil
	default:
		return nil, psbErr("unsupported type %#x at %#x", tag, offset)
	}
}
