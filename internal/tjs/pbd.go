package tjs

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"unicode/utf16"
)

var pbdNS0Magic = [...]byte{'T', 'J', 'S', '/', 'n', 's', '0', 0}

const (
	pbdHeaderSize     = 16
	pbdMaxDepth       = 256
	pbdMaxItems       = 1_000_000
	pbdMaxStringUnits = 64 * 1024 * 1024
	pbdTypeVoid       = 0
	pbdTypeString     = 2
	pbdTypeInteger    = 4
	pbdTypeReal       = 5
	pbdTypeArray      = 0x81
	pbdTypeDictionary = 0xc1
)

type pbdEntry struct {
	key   string
	value pbdNode
}

type pbdNode struct {
	typeCode byte
	text     string
	integer  int64
	real     float64
	items    []pbdNode
	entries  []pbdEntry
}

type pbdChecker struct {
	seed uint32
}

func (c *pbdChecker) byteForType(typeCode byte) byte {
	state := [4]byte{}
	binary.LittleEndian.PutUint32(state[:], c.seed)
	if typeCode == pbdTypeVoid {
		return state[2]
	}
	pbdRound(state[:])
	c.seed = binary.LittleEndian.Uint32(state[:])
	return state[2]
}

func pbdRound(state []byte) {
	a := state[0] ^ (state[0] * 2)
	b := a
	b >>= 2
	b ^= state[2]
	b >>= 3
	b ^= state[2]
	b ^= a
	state[0] = state[1]
	state[1] = state[2]
	state[2] = b
}

func pbdFinalCheck(seed uint32) uint32 {
	state := [4]byte{}
	binary.LittleEndian.PutUint32(state[:], seed)
	for i := 0; i < 3; i++ {
		pbdRound(state[:])
	}
	state[0], state[2] = state[2], state[0]
	return binary.LittleEndian.Uint32(state[:])
}

type pbdCursor struct {
	data []byte
	pos  int
}

func (c *pbdCursor) remaining() int {
	return len(c.data) - c.pos
}

func (c *pbdCursor) read(n int) ([]byte, error) {
	if n < 0 || c.pos > len(c.data)-n {
		return nil, fmt.Errorf("TJS/ns0 data is truncated")
	}
	value := c.data[c.pos : c.pos+n]
	c.pos += n
	return value, nil
}

func (c *pbdCursor) readByte() (byte, error) {
	value, err := c.read(1)
	if err != nil {
		return 0, err
	}
	return value[0], nil
}

func (c *pbdCursor) readU32() (uint32, error) {
	value, err := c.read(4)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(value), nil
}

func (c *pbdCursor) readI64() (int64, error) {
	value, err := c.read(8)
	if err != nil {
		return 0, err
	}
	return int64(binary.LittleEndian.Uint64(value)), nil
}

func (c *pbdCursor) readF64() (float64, error) {
	value, err := c.read(8)
	if err != nil {
		return 0, err
	}
	return mathFromBits(binary.LittleEndian.Uint64(value)), nil
}

func (c *pbdCursor) readString() (string, error) {
	units, err := c.readU32()
	if err != nil {
		return "", err
	}
	if units > pbdMaxStringUnits {
		return "", fmt.Errorf("TJS/ns0 string is too large")
	}
	count := int(units)
	if count > c.remaining()/2 {
		return "", fmt.Errorf("TJS/ns0 string is truncated")
	}
	raw, err := c.read(count * 2)
	if err != nil {
		return "", err
	}
	values := make([]uint16, count)
	for i := range values {
		values[i] = binary.LittleEndian.Uint16(raw[i*2:])
	}
	return string(utf16.Decode(values)), nil
}

func decodePBDNS0(data []byte) (pbdNode, error) {
	if len(data) < pbdHeaderSize || !bytes.Equal(data[:len(pbdNS0Magic)], pbdNS0Magic[:]) {
		return pbdNode{}, fmt.Errorf("unsupported TJS/ns0 data")
	}
	if binary.LittleEndian.Uint16(data[12:14]) != 0 || binary.LittleEndian.Uint16(data[14:16]) != 0 {
		return pbdNode{}, fmt.Errorf("TJS/ns0 header has unsupported fields")
	}
	cursor := &pbdCursor{data: data, pos: pbdHeaderSize}
	checker := &pbdChecker{seed: binary.LittleEndian.Uint32(data[8:12])}
	root, err := decodePBDValue(cursor, checker, 0)
	if err != nil {
		return pbdNode{}, err
	}
	checksum, err := cursor.readU32()
	if err != nil {
		return pbdNode{}, err
	}
	if checksum != pbdFinalCheck(checker.seed) {
		return pbdNode{}, fmt.Errorf("TJS/ns0 checksum mismatch")
	}
	if cursor.remaining() != 0 {
		return pbdNode{}, fmt.Errorf("TJS/ns0 has trailing data")
	}
	return root, nil
}

func decodePBDValue(cursor *pbdCursor, checker *pbdChecker, depth int) (pbdNode, error) {
	if depth > pbdMaxDepth {
		return pbdNode{}, fmt.Errorf("TJS/ns0 nesting limit exceeded")
	}
	typeCode, err := cursor.readByte()
	if err != nil {
		return pbdNode{}, err
	}
	check, err := cursor.readByte()
	if err != nil {
		return pbdNode{}, err
	}
	if expected := checker.byteForType(typeCode); check != expected {
		return pbdNode{}, fmt.Errorf("TJS/ns0 type check mismatch")
	}
	node := pbdNode{typeCode: typeCode}
	switch typeCode {
	case pbdTypeVoid:
		return node, nil
	case pbdTypeString:
		node.text, err = cursor.readString()
		return node, err
	case pbdTypeInteger:
		node.integer, err = cursor.readI64()
		return node, err
	case pbdTypeReal:
		node.real, err = cursor.readF64()
		return node, err
	case pbdTypeArray:
		count, readErr := cursor.readU32()
		if readErr != nil {
			return pbdNode{}, readErr
		}
		if count > pbdMaxItems {
			return pbdNode{}, fmt.Errorf("TJS/ns0 array is too large")
		}
		node.items = make([]pbdNode, 0, int(count))
		for i := uint32(0); i < count; i++ {
			item, itemErr := decodePBDValue(cursor, checker, depth+1)
			if itemErr != nil {
				return pbdNode{}, itemErr
			}
			node.items = append(node.items, item)
		}
		return node, nil
	case pbdTypeDictionary:
		count, readErr := cursor.readU32()
		if readErr != nil {
			return pbdNode{}, readErr
		}
		if count > pbdMaxItems {
			return pbdNode{}, fmt.Errorf("TJS/ns0 dictionary is too large")
		}
		node.entries = make([]pbdEntry, 0, int(count))
		for i := uint32(0); i < count; i++ {
			key, keyErr := cursor.readString()
			if keyErr != nil {
				return pbdNode{}, keyErr
			}
			value, valueErr := decodePBDValue(cursor, checker, depth+1)
			if valueErr != nil {
				return pbdNode{}, valueErr
			}
			node.entries = append(node.entries, pbdEntry{key: key, value: value})
		}
		return node, nil
	default:
		return pbdNode{}, fmt.Errorf("TJS/ns0 value type 0x%02x is unsupported", typeCode)
	}
}

func mathFromBits(bits uint64) float64 {
	return math.Float64frombits(bits)
}

func (v *VM) DeserializePBD(data []byte) (Value, error) {
	root, err := decodePBDNS0(data)
	if err != nil {
		return VoidValue, err
	}
	depth := len(v.transientRoots)
	defer v.popTransientRoots(depth)
	return v.pbdNodeValue(root, 0)
}

func (v *VM) pbdNodeValue(node pbdNode, depth int) (Value, error) {
	if depth > pbdMaxDepth {
		return VoidValue, fmt.Errorf("TJS/ns0 nesting limit exceeded")
	}
	switch node.typeCode {
	case pbdTypeVoid:
		return VoidValue, nil
	case pbdTypeString:
		return StringValue(node.text), nil
	case pbdTypeInteger:
		return IntegerValue(node.integer), nil
	case pbdTypeReal:
		return RealValue(node.real), nil
	case pbdTypeArray:
		items := make([]Value, len(node.items))
		for i, item := range node.items {
			value, err := v.pbdNodeValue(item, depth+1)
			if err != nil {
				return VoidValue, err
			}
			items[i] = value
			v.transientRoots = append(v.transientRoots, value)
		}
		return v.NewArray(items)
	case pbdTypeDictionary:
		values := make([]Value, len(node.entries))
		for i, entry := range node.entries {
			value, err := v.pbdNodeValue(entry.value, depth+1)
			if err != nil {
				return VoidValue, err
			}
			values[i] = value
			v.transientRoots = append(v.transientRoots, value)
		}
		result, err := v.NewDictionary()
		if err != nil {
			return VoidValue, err
		}
		for i, entry := range node.entries {
			if err := v.SetMemberWithRuntime(result, entry.key, values[i], 0, nil, nil); err != nil {
				return VoidValue, err
			}
		}
		return result, nil
	default:
		return VoidValue, fmt.Errorf("TJS/ns0 value type 0x%02x is unsupported", node.typeCode)
	}
}
