package tjs

import (
	"math"
	"sync"
)

// Math is the stateless TJS Math namespace. Its methods mirror the native
// names while returning Go's IEEE-754 values (including NaN and infinities).
type Math struct{}

var TJSMath = Math{}

func (Math) Abs(x float64) float64      { return math.Abs(x) }
func (Math) Acos(x float64) float64     { return math.Acos(x) }
func (Math) Asin(x float64) float64     { return math.Asin(x) }
func (Math) Atan(x float64) float64     { return math.Atan(x) }
func (Math) Atan2(y, x float64) float64 { return math.Atan2(y, x) }
func (Math) Ceil(x float64) float64     { return math.Ceil(x) }
func (Math) Exp(x float64) float64      { return math.Exp(x) }
func (Math) Floor(x float64) float64    { return math.Floor(x) }
func (Math) Log(x float64) float64      { return math.Log(x) }
func (Math) Pow(x, y float64) float64   { return math.Pow(x, y) }
func (Math) Round(x float64) float64    { return math.Floor(x + 0.5) }
func (Math) Sin(x float64) float64      { return math.Sin(x) }
func (Math) Cos(x float64) float64      { return math.Cos(x) }
func (Math) Sqrt(x float64) float64     { return math.Sqrt(x) }
func (Math) Tan(x float64) float64      { return math.Tan(x) }

func (Math) Max(values ...float64) float64 {
	result := math.Inf(-1)
	for _, v := range values {
		if math.IsNaN(v) {
			return math.NaN()
		}
		if v == 0 && math.Signbit(result) {
			result = v
			continue
		}
		if v > result {
			result = v
		}
	}
	return result
}
func (Math) Min(values ...float64) float64 {
	result := math.Inf(1)
	for _, v := range values {
		if math.IsNaN(v) {
			return math.NaN()
		}
		if v == 0 && !math.Signbit(result) {
			result = v
			continue
		}
		if v < result {
			result = v
		}
	}
	return result
}

func MathAbs(x float64) float64         { return TJSMath.Abs(x) }
func MathAcos(x float64) float64        { return TJSMath.Acos(x) }
func MathAsin(x float64) float64        { return TJSMath.Asin(x) }
func MathAtan(x float64) float64        { return TJSMath.Atan(x) }
func MathAtan2(y, x float64) float64    { return TJSMath.Atan2(y, x) }
func MathCeil(x float64) float64        { return TJSMath.Ceil(x) }
func MathExp(x float64) float64         { return TJSMath.Exp(x) }
func MathFloor(x float64) float64       { return TJSMath.Floor(x) }
func MathLog(x float64) float64         { return TJSMath.Log(x) }
func MathPow(x, y float64) float64      { return TJSMath.Pow(x, y) }
func MathRound(x float64) float64       { return TJSMath.Round(x) }
func MathSin(x float64) float64         { return TJSMath.Sin(x) }
func MathCos(x float64) float64         { return TJSMath.Cos(x) }
func MathSqrt(x float64) float64        { return TJSMath.Sqrt(x) }
func MathTan(x float64) float64         { return TJSMath.Tan(x) }
func MathMax(values ...float64) float64 { return TJSMath.Max(values...) }
func MathMin(values ...float64) float64 { return TJSMath.Min(values...) }

// The package-level MathRandom generator is intentionally the same xorshift
// sequence used by tjsMath.cpp. Seed it explicitly in tests or hosts that
// need reproducibility.
type Xorshift struct{ state [4]uint32 }
type TJSXorshift = Xorshift

func NewXorshift(seed uint32) *Xorshift { x := &Xorshift{}; x.Seed(seed); return x }
func (x *Xorshift) Seed(seed uint32) {
	for i := uint32(0); i < 4; i++ {
		seed = 1812433253*(seed^(seed>>30)) + i
		x.state[i] = seed
	}
}
func (x *Xorshift) Uint32() uint32 {
	t := x.state[0] ^ (x.state[0] << 11)
	x.state[0], x.state[1], x.state[2] = x.state[1], x.state[2], x.state[3]
	x.state[3] = (x.state[3] ^ (x.state[3] >> 19)) ^ (t ^ (t >> 8))
	return x.state[3]
}
func (x *Xorshift) Float64() float64 { return float64(x.Uint32()) / 4294967296.0 }
func (x *Xorshift) Random() float64  { return x.Float64() }

var mathRandomMu sync.Mutex
var mathRandomSource = func() *Xorshift {
	x := NewXorshift(123456789)
	x.state = [4]uint32{123456789, 362436069, 521288629, 88675123}
	return x
}()

func SeedMathRandom(seed uint32) {
	mathRandomMu.Lock()
	mathRandomSource.Seed(seed)
	mathRandomMu.Unlock()
}
func MathRandom() float64 {
	mathRandomMu.Lock()
	defer mathRandomMu.Unlock()
	return mathRandomSource.Float64()
}
func (Math) Random() float64 { return MathRandom() }

const (
	MathE       = math.E
	MathLog2E   = math.Log2E
	MathLog10E  = math.Log10E
	MathLn10    = math.Ln10
	MathLn2     = math.Ln2
	MathPI      = math.Pi
	MathSqrt1_2 = math.Sqrt2 / 2
	MathSqrt2   = math.Sqrt2
)
