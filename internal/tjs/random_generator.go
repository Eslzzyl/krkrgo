package tjs

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"time"
)

var ErrNotReconstructiveRandomizeData = errors.New("TJS non-reconstructive randomize data")

const mtN = 624
const mtM = 397

// RandomGeneratorState is the reconstructible form emitted by Serialize.
// State is exactly 624 lower-case eight-digit hexadecimal words; Left and Next
// retain the generator cursor used by the native implementation.
type RandomGeneratorState struct {
	State string
	Left  int
	Next  int
}

type RandomGenerator struct {
	state [mtN]uint32
	left  int
	next  int
	ready bool
}

type TJSRandomGenerator = RandomGenerator

func NewRandomGenerator(seed ...any) *RandomGenerator {
	r := &RandomGenerator{}
	if len(seed) == 0 {
		r.Randomize(uint64(time.Now().UnixNano()))
		return r
	}
	_ = r.Randomize(seed[0])
	return r
}
func NewRandomGeneratorSeed(seed uint64) *RandomGenerator { return NewRandomGenerator(seed) }
func NewRandomGeneratorFromState(state RandomGeneratorState) (*RandomGenerator, error) {
	r := &RandomGenerator{}
	if err := r.Randomize(state); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *RandomGenerator) initSeed(seed uint32) {
	r.state[0] = seed
	for i := 1; i < mtN; i++ {
		r.state[i] = 1812433253*(r.state[i-1]^(r.state[i-1]>>30)) + uint32(i)
	}
	r.left, r.next, r.ready = 1, 0, true
}
func (r *RandomGenerator) initArray(keys []uint32) {
	r.initSeed(19650218)
	i, j := 1, 0
	n := mtN
	if len(keys) > n {
		n = len(keys)
	}
	for range n {
		r.state[i] = (r.state[i] ^ ((r.state[i-1] ^ (r.state[i-1] >> 30)) * 1664525)) + keys[j] + uint32(j)
		i++
		j++
		if i >= mtN {
			r.state[0] = r.state[mtN-1]
			i = 1
		}
		if j >= len(keys) {
			j = 0
		}
	}
	for k := mtN - 1; k > 0; k-- {
		r.state[i] = (r.state[i] ^ ((r.state[i-1] ^ (r.state[i-1] >> 30)) * 1566083941)) - uint32(i)
		i++
		if i >= mtN {
			r.state[0] = r.state[mtN-1]
			i = 1
		}
	}
	r.state[0] = 0x80000000
	r.left, r.next, r.ready = 1, 0, true
}

// Randomize accepts an integer seed or a RandomGeneratorState (and its pointer).
func (r *RandomGenerator) Randomize(seeds ...any) error {
	if len(seeds) == 0 {
		return r.Randomize(uint64(time.Now().UnixNano()))
	}
	if r == nil {
		return ErrNotReconstructiveRandomizeData
	}
	switch v := seeds[0].(type) {
	case RandomGeneratorState:
		return r.restore(v)
	case *RandomGeneratorState:
		if v == nil {
			return ErrNotReconstructiveRandomizeData
		}
		return r.restore(*v)
	case uint64:
		r.initArray([]uint32{uint32(v), uint32(v >> 32)})
		return nil
	case int64:
		r.initArray([]uint32{uint32(v), uint32(uint64(v) >> 32)})
		return nil
	case uint32:
		r.initArray([]uint32{v, 0})
		return nil
	case int:
		return r.Randomize(int64(v))
	case int32:
		return r.Randomize(int64(v))
	case uint:
		return r.Randomize(uint64(v))
	default:
		return ErrNotReconstructiveRandomizeData
	}
}

func (r *RandomGenerator) twist() {
	for i := range mtN {
		y := (r.state[i] & 0x80000000) | (r.state[(i+1)%mtN] & 0x7fffffff)
		r.state[i] = r.state[(i+mtM)%mtN] ^ (y >> 1)
		if y&1 != 0 {
			r.state[i] ^= 0x9908b0df
		}
	}
	r.left, r.next = mtN, 0
}
func (r *RandomGenerator) Uint32() uint32 {
	if r == nil || !r.ready {
		return 0
	}
	r.left--
	if r.left == 0 {
		r.twist()
	}
	y := r.state[r.next]
	r.next++
	y ^= y >> 11
	y ^= (y << 7) & 0x9d2c5680
	y ^= (y << 15) & 0xefc60000
	y ^= y >> 18
	return y
}
func (r *RandomGenerator) Random32() uint32 { return r.Uint32() }
func (r *RandomGenerator) Random() float64 {
	bits := uint64(r.Uint32()) | uint64(r.Uint32())<<32
	return math.Float64frombits((bits&0x000fffffffffffff)|0x3ff0000000000000) - 1
}
func (r *RandomGenerator) Float64() float64 { return r.Random() }
func (r *RandomGenerator) Random63() int64 {
	return int64((uint64(r.Uint32()) | uint64(r.Uint32())<<32) & 0x7fffffffffffffff)
}
func (r *RandomGenerator) Random64() int64 { return int64(uint64(r.Uint32()) | uint64(r.Uint32())<<32) }

func (r *RandomGenerator) Serialize() RandomGeneratorState {
	if r == nil || !r.ready {
		return RandomGeneratorState{}
	}
	buf := make([]byte, mtN*4)
	for i, n := range r.state {
		buf[i*4] = byte(n >> 24)
		buf[i*4+1] = byte(n >> 16)
		buf[i*4+2] = byte(n >> 8)
		buf[i*4+3] = byte(n)
	}
	return RandomGeneratorState{State: hex.EncodeToString(buf), Left: r.left, Next: r.next}
}
func (r *RandomGenerator) restore(s RandomGeneratorState) error {
	if len(s.State) != mtN*8 || s.Next < 0 || s.Next > mtN || s.Left < 1 || s.Left > mtN ||
		!(s.Next == 0 && s.Left == 1) && s.Left+s.Next != mtN+1 {
		return ErrNotReconstructiveRandomizeData
	}
	buf, err := hex.DecodeString(s.State)
	if err != nil || len(buf) != mtN*4 {
		return ErrNotReconstructiveRandomizeData
	}
	for i := range r.state {
		r.state[i] = uint32(buf[i*4])<<24 | uint32(buf[i*4+1])<<16 | uint32(buf[i*4+2])<<8 | uint32(buf[i*4+3])
	}
	r.left, r.next, r.ready = s.Left, s.Next, true
	return nil
}
func (r *RandomGenerator) State() RandomGeneratorState { return r.Serialize() }
func (s RandomGeneratorState) String() string {
	return fmt.Sprintf("state=%s left=%d next=%d", s.State, s.Left, s.Next)
}
