package wmv3

import "fmt"

// BitReader reads bits MSB-first from a byte slice, in the same bit order
// as FFmpeg's get_bits: the first bit of the stream is bit 7 of byte zero.
// Every read validates its full range before advancing, so a failed read
// leaves Pos unchanged and never panics or reads past Data.
type BitReader struct {
	Data []byte
	Pos  int // bit index of the next bit to read
}

// NewBitReader returns a reader positioned at the first bit of data.
func NewBitReader(data []byte) *BitReader { return &BitReader{Data: data} }

// Remaining reports how many unread bits are left.
func (r *BitReader) Remaining() int {
	if r.Pos < 0 || r.Pos >= len(r.Data)*8 {
		return 0
	}
	return len(r.Data)*8 - r.Pos
}

// Bits reads n bits, 0 through 32, as an unsigned MSB-first integer.
// Bits(0) returns 0 without consuming input. Reading past the end of the
// data returns an error wrapping ErrTruncated and leaves Pos unchanged.
// An n outside [0,32] is a caller bug and returns a plain error.
func (r *BitReader) Bits(n int) (uint32, error) {
	if n < 0 || n > 32 {
		return 0, fmt.Errorf("bit count %d out of range [0,32]", n)
	}
	if n == 0 {
		return 0, nil
	}
	if err := r.check(n); err != nil {
		return 0, err
	}
	var v uint32
	p := r.Pos
	for n > 0 {
		bitOff := p & 7
		take := 8 - bitOff
		if take > n {
			take = n
		}
		chunk := uint32(r.Data[p>>3]) >> (8 - bitOff - take)
		v = v<<uint(take) | chunk&((1<<uint(take))-1)
		p += take
		n -= take
	}
	r.Pos = p
	return v, nil
}

// Bit reads a single bit.
func (r *BitReader) Bit() (bool, error) {
	v, err := r.Bits(1)
	return v != 0, err
}

// Skip advances past n bits without returning them.
func (r *BitReader) Skip(n int) error {
	if n < 0 {
		return fmt.Errorf("skip count %d out of range", n)
	}
	if n == 0 {
		return nil
	}
	if err := r.check(n); err != nil {
		return err
	}
	r.Pos += n
	return nil
}

// check reports ErrTruncated unless n further bits are available starting
// at Pos. r.Pos <= len(r.Data)*8 is maintained, so the arithmetic below
// cannot underflow or overflow.
func (r *BitReader) check(n int) error {
	if r.Pos < 0 || r.Pos > len(r.Data)*8 || n > len(r.Data)*8-r.Pos {
		return fmt.Errorf("bit %d: need %d more bits: %w", r.Pos, n, ErrTruncated)
	}
	return nil
}
