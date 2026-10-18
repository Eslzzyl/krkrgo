package runtime

import "github.com/eslzzyl/krkrgo/internal/simd"

// rowAllOpaque reports whether every pixel in a packed RGBA row has alpha 255.
func rowAllOpaque(row []byte) bool {
	return simd.AllAlphaOpaque(row)
}
