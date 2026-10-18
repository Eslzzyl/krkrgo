package simd

// AlphaOverOpaqueRow blends a packed RGBA source row over a destination row
// whose alpha is known to be 255. Source alpha 0 keeps the destination and
// source alpha 255 replaces the color. The destination alpha stays 255.
func AlphaOverOpaqueRow(dst, src []byte) {
	limit := min(len(dst), len(src))
	limit -= limit % 4
	dst, src = dst[:limit], src[:limit]
	processed := alphaOverOpaqueRowSIMD(dst, src)
	for i := processed; i+3 < limit; i += 4 {
		alphaOverOpaquePixel(dst[i:i+4], src[i:i+4])
	}
}

func alphaOverOpaquePixel(dst, src []byte) {
	alpha := int(src[3])
	switch alpha {
	case 0:
		return
	case 255:
		dst[0], dst[1], dst[2], dst[3] = src[0], src[1], src[2], 255
		return
	}
	inverse := 255 - alpha
	for channel := 0; channel < 3; channel++ {
		dst[channel] = byte((int(dst[channel])*inverse + int(src[channel])*alpha + 127) / 255)
	}
	dst[3] = 255
}
