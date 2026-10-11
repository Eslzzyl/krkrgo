package assets

import (
	"encoding/binary"
	"fmt"
)

// Reader consumes a bounded little-endian byte slice. All reads advance only
// after their complete range has been validated.
type Reader struct {
	Data []byte
	Pos  int
}

func NewReader(data []byte) *Reader { return &Reader{Data: data} }

func (r *Reader) Take(n int) ([]byte, error) {
	if n < 0 || r.Pos < 0 || r.Pos > len(r.Data) || n > len(r.Data)-r.Pos {
		return nil, fmt.Errorf("truncated input at %#x: need %d bytes", r.Pos, n)
	}
	end := r.Pos + n
	out := r.Data[r.Pos:end]
	r.Pos = end
	return out, nil
}

func (r *Reader) U16() (uint16, error) {
	b, err := r.Take(2)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint16(b), nil
}

func (r *Reader) U32() (uint32, error) {
	b, err := r.Take(4)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(b), nil
}

func (r *Reader) U64() (uint64, error) {
	b, err := r.Take(8)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint64(b), nil
}

func (r *Reader) Done() bool { return r.Pos == len(r.Data) }

// Chunk reads a four-byte tag followed by a u64 byte length.
func (r *Reader) Chunk() ([4]byte, []byte, error) {
	var tag [4]byte
	b, err := r.Take(4)
	if err != nil {
		return tag, nil, err
	}
	copy(tag[:], b)
	length, err := r.U64()
	if err != nil {
		return tag, nil, err
	}
	if length > uint64(len(r.Data)-r.Pos) {
		return tag, nil, fmt.Errorf("truncated input at %#x: need %d bytes", r.Pos, length)
	}
	out, err := r.Take(int(length))
	return tag, out, err
}
