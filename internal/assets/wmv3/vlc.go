// Scalar bitstream primitives translated from FFmpeg libavcodec/unary.h
// (get_unary) and libavcodec/get_bits.h + bitstream_template.h
// (decode012, decode210), LGPL-2.1-or-later; see doc.go for the full
// license notice.
package wmv3

import "fmt"

// Unary decodes a truncated unary code: the count of consecutive !stop
// bits before the first stop bit, capped at maxLen. This matches
// get_unary(gb, stop, len), which returns len when no stop bit appears.
// Unary(0) returns 0 without consuming input.
func Unary(r *BitReader, stop bool, maxLen int) (int, error) {
	if maxLen < 0 {
		return 0, fmt.Errorf("unary length %d out of range", maxLen)
	}
	for i := range maxLen {
		b, err := r.Bit()
		if err != nil {
			return 0, err
		}
		if b == stop {
			return i, nil
		}
	}
	return maxLen, nil
}

// Decode012 decodes the truncated unary code for values 0, 1, 2: a zero
// bit selects 0, otherwise the next bit adds 1. Matches decode012.
func Decode012(r *BitReader) (int, error) {
	b, err := r.Bit()
	if err != nil {
		return 0, err
	}
	if !b {
		return 0, nil
	}
	b, err = r.Bit()
	if err != nil {
		return 0, err
	}
	if b {
		return 2, nil
	}
	return 1, nil
}

// Decode210 decodes the truncated unary code for values 2, 1, 0: a one
// bit selects 0, otherwise 2 minus the next bit. Matches decode210.
func Decode210(r *BitReader) (int, error) {
	b, err := r.Bit()
	if err != nil {
		return 0, err
	}
	if b {
		return 0, nil
	}
	b, err = r.Bit()
	if err != nil {
		return 0, err
	}
	if b {
		return 1, nil
	}
	return 2, nil
}

// decodeVLC matches one code from a (code, length) codebook stored
// MSB-first, as transcribed in tables.go from libavcodec/vc1_vlc_data.h.
// It consumes exactly the matched code's bits and returns its symbol
// index. Every codebook used here is prefix-free in the FFmpeg source,
// so the first (shortest) match is the unique match. No match reports
// an error wrapping ErrInvalidValue.
func decodeVLC(r *BitReader, codes []uint16, bits []uint8) (int, error) {
	max := 0
	for _, n := range bits {
		if int(n) > max {
			max = int(n)
		}
	}
	var v uint16
	for n := range max {
		b, err := r.Bit()
		if err != nil {
			return 0, err
		}
		v = v<<1 | uint16(boolToInt(b))
		for s := range codes {
			if int(bits[s]) == n+1 && codes[s] == v {
				return s, nil
			}
		}
	}
	return 0, fmt.Errorf("no VLC match: %w", ErrInvalidValue)
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// IntensityLUT fills the intensity-compensation lookup tables exactly as
// the INIT_LUT macro in libavcodec/vc1.c does: scale is lumscale+32
// (or -64 when lumscale is 0) with shift derived from lumshift, and each
// entry chains from the base tables when chain is set. The frame header
// records only LumScale/LumShift; motion compensation consumes these
// tables once the macroblock layer exists.
func IntensityLUT(lumscale, lumshift uint8, baseY, baseUV *[256]uint8, chain bool) (luty, lutuv [256]uint8) {
	var scale, shift int
	if lumscale == 0 {
		scale = -64
		shift = (255 - int(lumshift)*2) * 64
		if lumshift > 31 {
			shift += 128 << 6
		}
	} else {
		scale = int(lumscale) + 32
		if lumshift > 31 {
			shift = (int(lumshift) - 64) * 64
		} else {
			shift = int(lumshift) << 6
		}
	}
	for i := range 256 {
		iy, iu := i, i
		if chain {
			iy, iu = int(baseY[i]), int(baseUV[i])
		}
		luty[i] = clipUint8((scale*iy + shift + 32) >> 6)
		lutuv[i] = clipUint8((scale*(iu-128) + 128*64 + 32) >> 6)
	}
	return luty, lutuv
}

func clipUint8(v int) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}
