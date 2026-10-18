//go:build !arm64 && !amd64

package simd

import "encoding/binary"

func AllAlphaOpaque(row []byte) bool {
	for len(row) >= 8 {
		value := binary.LittleEndian.Uint64(row)
		if value&0xff000000ff000000 != 0xff000000ff000000 {
			return false
		}
		row = row[8:]
	}
	for i := 3; i < len(row); i += 4 {
		if row[i] != 255 {
			return false
		}
	}
	return true
}

func scale2xRow(dst, src []byte) {
	for i := 0; i+3 < len(src); i += 4 {
		pixel := binary.LittleEndian.Uint32(src[i:])
		binary.LittleEndian.PutUint32(dst[i*2:], pixel)
		binary.LittleEndian.PutUint32(dst[i*2+4:], pixel)
	}
}

func alphaOverOpaqueRowSIMD(dst, src []byte) int { return 0 }
