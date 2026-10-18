//go:build arm64

package simd

// AllAlphaOpaque reports whether every pixel in a packed RGBA row has alpha
// 255. The row length is expected to be a multiple of four.
//
//go:noescape
func AllAlphaOpaque(row []byte) bool

//go:noescape
func scale2xRow(dst, src []byte)

// alphaOverOpaqueRowSIMD processes a full number of 16-byte groups and
// returns the number of bytes written.
//
//go:noescape
func alphaOverOpaqueRowSIMD(dst, src []byte) int
