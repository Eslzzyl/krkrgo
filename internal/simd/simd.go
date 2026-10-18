package simd

// Scale2xRow writes a 2x nearest-neighbor expansion of src into dst. dst must
// hold at least 2*len(src) bytes.
func Scale2xRow(dst, src []byte) {
	if len(src) == 0 {
		return
	}
	size := len(src) * 2
	if len(dst) < size {
		panic("simd: Scale2xRow destination is too small")
	}
	scale2xRow(dst[:size], src)
}
