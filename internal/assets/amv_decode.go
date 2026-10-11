package assets

import (
	"compress/zlib"
	"io"
	"math"
)

var amvZigzag = [64]int{0, 1, 8, 16, 9, 2, 3, 10, 17, 24, 32, 25, 18, 11, 4, 5, 12, 19, 26, 33, 40, 48, 41, 34, 27, 20, 13, 6, 7, 14, 21, 28, 35, 42, 49, 56, 57, 50, 43, 36, 29, 22, 15, 23, 30, 37, 44, 51, 58, 59, 52, 45, 38, 31, 39, 46, 53, 60, 61, 54, 47, 55, 62, 63}

var amvLumaDCLengths = [16]byte{0, 1, 5, 1, 1, 1, 1, 1, 1, 0, 0, 0, 0, 0, 0, 0}
var amvLumaDCValues = []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}
var amvChromaDCLengths = [16]byte{0, 3, 1, 1, 1, 1, 1, 1, 1, 1, 1, 0, 0, 0, 0, 0}
var amvChromaDCValues = []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}
var amvLumaACLengths = [16]byte{0, 2, 1, 3, 3, 2, 4, 3, 5, 5, 4, 4, 0, 0, 1, 0x7d}
var amvChromaACLengths = [16]byte{0, 2, 1, 2, 4, 4, 3, 4, 7, 5, 4, 4, 0, 1, 2, 0x77}
var amvLumaACValues = []byte{
	1, 2, 3, 0, 4, 0x11, 5, 0x12, 0x21, 0x31, 0x41, 6, 0x13, 0x51, 0x61, 7, 0x22, 0x71, 0x14, 0x32, 0x81, 0x91, 0xa1, 8, 0x23, 0x42, 0xb1, 0xc1, 0x15, 0x52, 0xd1, 0xf0, 0x24, 0x33, 0x62, 0x72, 0x82, 9, 0xa, 0x16, 0x17, 0x18, 0x19, 0x1a, 0x25, 0x26, 0x27, 0x28, 0x29, 0x2a, 0x34, 0x35, 0x36, 0x37, 0x38, 0x39, 0x3a, 0x43, 0x44, 0x45, 0x46, 0x47, 0x48, 0x49, 0x4a, 0x53, 0x54, 0x55, 0x56, 0x57, 0x58, 0x59, 0x5a, 0x63, 0x64, 0x65, 0x66, 0x67, 0x68, 0x69, 0x6a, 0x73, 0x74, 0x75, 0x76, 0x77, 0x78, 0x79, 0x7a, 0x83, 0x84, 0x85, 0x86, 0x87, 0x88, 0x89, 0x8a, 0x92, 0x93, 0x94, 0x95, 0x96, 0x97, 0x98, 0x99, 0x9a, 0xa2, 0xa3, 0xa4, 0xa5, 0xa6, 0xa7, 0xa8, 0xa9, 0xaa, 0xb2, 0xb3, 0xb4, 0xb5, 0xb6, 0xb7, 0xb8, 0xb9, 0xba, 0xc2, 0xc3, 0xc4, 0xc5, 0xc6, 0xc7, 0xc8, 0xc9, 0xca, 0xd2, 0xd3, 0xd4, 0xd5, 0xd6, 0xd7, 0xd8, 0xd9, 0xda, 0xe1, 0xe2, 0xe3, 0xe4, 0xe5, 0xe6, 0xe7, 0xe8, 0xe9, 0xea, 0xf1, 0xf2, 0xf3, 0xf4, 0xf5, 0xf6, 0xf7, 0xf8, 0xf9, 0xfa,
}
var amvChromaACValues = []byte{
	0, 1, 2, 3, 0x11, 4, 5, 0x21, 0x31, 6, 0x12, 0x41, 0x51, 7, 0x61, 0x71, 0x13, 0x22, 0x32, 0x81, 8, 0x14, 0x42, 0x91, 0xa1, 0xb1, 0xc1, 9, 0x23, 0x33, 0x52, 0xf0, 0x15, 0x62, 0x72, 0xd1, 0xa, 0x16, 0x24, 0x34, 0xe1, 0x25, 0xf1, 0x17, 0x18, 0x19, 0x1a, 0x26, 0x27, 0x28, 0x29, 0x2a, 0x35, 0x36, 0x37, 0x38, 0x39, 0x3a, 0x43, 0x44, 0x45, 0x46, 0x47, 0x48, 0x49, 0x4a, 0x53, 0x54, 0x55, 0x56, 0x57, 0x58, 0x59, 0x5a, 0x63, 0x64, 0x65, 0x66, 0x67, 0x68, 0x69, 0x6a, 0x73, 0x74, 0x75, 0x76, 0x77, 0x78, 0x79, 0x7a, 0x82, 0x83, 0x84, 0x85, 0x86, 0x87, 0x88, 0x89, 0x8a, 0x92, 0x93, 0x94, 0x95, 0x96, 0x97, 0x98, 0x99, 0x9a, 0xa2, 0xa3, 0xa4, 0xa5, 0xa6, 0xa7, 0xa8, 0xa9, 0xaa, 0xb2, 0xb3, 0xb4, 0xb5, 0xb6, 0xb7, 0xb8, 0xb9, 0xba, 0xc2, 0xc3, 0xc4, 0xc5, 0xc6, 0xc7, 0xc8, 0xc9, 0xca, 0xd2, 0xd3, 0xd4, 0xd5, 0xd6, 0xd7, 0xd8, 0xd9, 0xda, 0xe2, 0xe3, 0xe4, 0xe5, 0xe6, 0xe7, 0xe8, 0xe9, 0xea, 0xf2, 0xf3, 0xf4, 0xf5, 0xf6, 0xf7, 0xf8, 0xf9, 0xfa,
}

type amvBits struct {
	b   []byte
	bit int
}

func (r *amvBits) read(n int) (int, error) {
	if n < 0 || n > 24 {
		return 0, amvErr(AMVMalformed, -1, "invalid bit count")
	}
	if n == 0 {
		return 0, nil
	}
	if n > len(r.b)*8-r.bit {
		return 0, amvErr(AMVTruncated, -1, "truncated AlphaMovie entropy stream")
	}
	v := 0
	for i := range n {
		v = (v << 1) | int((r.b[(r.bit+i)>>3]>>uint(7-((r.bit+i)&7)))&1)
	}
	r.bit += n
	return v, nil
}
func (r *amvBits) signed(n int) (int, error) {
	if n == 0 {
		return 0, nil
	}
	if n > 11 {
		return 0, amvErr(AMVMalformed, -1, "invalid coefficient size")
	}
	v, err := r.read(n)
	if err != nil {
		return 0, err
	}
	if v < 1<<(n-1) {
		return v - (1 << n) + 1, nil
	}
	return v, nil
}
func (r *amvBits) symbol(counts [16]byte, values []byte) (byte, error) {
	code, first, index := 0, 0, 0
	for _, count := range counts {
		bit, err := r.read(1)
		if err != nil {
			return 0, err
		}
		code = (code << 1) | bit
		n := int(count)
		if code >= first && code < first+n {
			at := index + code - first
			if at < 0 || at >= len(values) {
				return 0, amvErr(AMVMalformed, -1, "invalid AlphaMovie Huffman table")
			}
			return values[at], nil
		}
		index += n
		first = (first + n) << 1
	}
	return 0, amvErr(AMVMalformed, -1, "invalid AlphaMovie Huffman code")
}

func amvBlock(bits *amvBits, chroma bool, pred *int, quant []byte) ([64]byte, error) {
	var out [64]byte
	var c [64]int16
	var dl, al [16]byte
	var dv, av []byte
	if chroma {
		dl, dv, al, av = amvChromaDCLengths, amvChromaDCValues, amvChromaACLengths, amvChromaACValues
	} else {
		dl, dv, al, av = amvLumaDCLengths, amvLumaDCValues, amvLumaACLengths, amvLumaACValues
	}
	s, e := bits.symbol(dl, dv)
	if e != nil {
		return out, e
	}
	d, e := bits.signed(int(s))
	if e != nil {
		return out, e
	}
	if (d > 0 && *pred > int(^uint(0)>>1)-d) || (d < 0 && *pred < -int(^uint(0)>>1)-1-d) {
		return out, amvErr(AMVMalformed, -1, "DC predictor overflow")
	}
	*pred += d
	c[0] = int16(*pred)
	k := 1
	for k < 64 {
		s, e = bits.symbol(al, av)
		if e != nil {
			return out, e
		}
		if s == 0 {
			break
		}
		if s == 0xf0 {
			k += 16
			if k > 64 {
				return out, amvErr(AMVMalformed, -1, "AC run exceeds block")
			}
			continue
		}
		k += int(s >> 4)
		if k >= 64 || (s&15) == 0 {
			return out, amvErr(AMVMalformed, -1, "invalid AC run")
		}
		v, e := bits.signed(int(s & 15))
		if e != nil {
			return out, e
		}
		c[amvZigzag[k]] = int16(v)
		k++
	}
	amvIDCT(&c, quant, &out)
	return out, nil
}

func amvClamp(v int64) byte {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return byte(v)
}
func amvIDCT(c *[64]int16, q []byte, out *[64]byte) {
	// Direct Annex K evaluation keeps the transform bounded and gives the
	// same integer pixel range as the optimized AlphaMovie transform.
	for y := range 8 {
		for x := range 8 {
			var sum float64
			for v := range 8 {
				for u := range 8 {
					cv, cu := 1.0, 1.0
					if v == 0 {
						cv = 1 / math.Sqrt2
					}
					if u == 0 {
						cu = 1 / math.Sqrt2
					}
					sum += cu * cv * float64(c[v*8+u]*int16(q[v*8+u])) *
						math.Cos(float64((2*x+1)*u)*math.Pi/16) *
						math.Cos(float64((2*y+1)*v)*math.Pi/16)
				}
			}
			(*out)[y*8+x] = amvClamp(int64(math.Round(sum/4 + 128)))
		}
	}
}

// DecodePacket reconstructs one rectangle without carrying pixels from an
// earlier packet. Empty packets return (nil, nil).
func (m *Movie) DecodePacket(index int) (*Image, error) {
	if m == nil || index < 0 || index >= len(m.Packets) {
		return nil, amvErr(AMVRange, -1, "packet index out of range")
	}
	p := m.Packets[index]
	if p.Width == 0 {
		return nil, nil
	}
	w, h := int(p.Width), int(p.Height)
	if uint64(w)*uint64(h) > amvMaxPixels {
		return nil, amvErr(AMVMalformed, p.Offset, "rectangle exceeds pixel limit")
	}
	if len(m.Quantization) < 2 {
		return nil, amvErr(AMVMalformed, p.Offset, "missing quantization tables")
	}
	var alpha []byte
	compressed, e := m.CompressedAlpha(index)
	if e != nil {
		return nil, e
	}
	if compressed != nil {
		zr, e := zlib.NewReader(io.LimitReader(bytesReader(compressed), int64(w*h)+1))
		if e != nil {
			return nil, amvErr(AMVMalformed, p.AlphaStart, "invalid compressed alpha")
		}
		alpha, e = io.ReadAll(io.LimitReader(zr, int64(w*h)+1))
		closeErr := zr.Close()
		if e != nil {
			return nil, amvErr(AMVMalformed, p.AlphaStart, "invalid compressed alpha")
		}
		if closeErr != nil {
			return nil, amvErr(AMVMalformed, p.AlphaStart, "invalid compressed alpha")
		}
		if len(alpha) != w*h {
			return nil, amvErr(AMVMalformed, p.AlphaStart, "alpha plane size mismatch")
		}
	}
	entropy, e := m.Entropy(index)
	if e != nil {
		return nil, e
	}
	bits := amvBits{b: entropy}
	tables := make([][]byte, len(m.Quantization))
	for i, q := range m.Quantization {
		if len(q) != 64 {
			return nil, amvErr(AMVMalformed, p.Offset, "invalid quantization table")
		}
		tables[i] = q
	}
	rgba := make([]byte, w*h*4)
	chromaPred, lumaPred := 0, 0
	for my := 0; my < h; my += 16 {
		for mx := 0; mx < w; mx += 16 {
			cb, e := amvBlock(&bits, true, &chromaPred, tables[1])
			if e != nil {
				return nil, e
			}
			cr, e := amvBlock(&bits, true, &chromaPred, tables[1])
			if e != nil {
				return nil, e
			}
			var ys [4][64]byte
			for i := range ys {
				ys[i], e = amvBlock(&bits, false, &lumaPred, tables[0])
				if e != nil {
					return nil, e
				}
			}
			var as [4][64]byte
			for i := range as {
				for j := range as[i] {
					as[i][j] = 255
				}
			}
			if m.Header.AlphaEncoding == AlphaDCT {
				if len(tables) < 3 {
					return nil, amvErr(AMVMalformed, p.Offset, "missing alpha quantization table")
				}
				for i := range as {
					as[i], e = amvBlock(&bits, false, &lumaPred, tables[2])
					if e != nil {
						return nil, e
					}
				}
			}
			for y := 0; y < 16 && my+y < h; y++ {
				for x := 0; x < 16 && mx+x < w; x++ {
					bi := (y/8)*2 + x/8
					at := (y%8)*8 + x%8
					yy := int64(ys[bi][at])
					u := int64(cb[(y/2)*8+x/2]) - 128
					v := int64(cr[(y/2)*8+x/2]) - 128
					idx := (my+y)*w + mx + x
					z := rgba[idx*4 : idx*4+4]
					z[0] = amvClamp(yy*1000 + 1402*v)
					z[0] = amvClamp((yy*1000 + 1402*v + 500) / 1000)
					z[1] = amvClamp((yy*1000 - 344*u - 714*v + 500) / 1000)
					z[2] = amvClamp((yy*1000 + 1772*u + 500) / 1000)
					if alpha != nil {
						z[3] = alpha[idx]
					} else {
						z[3] = as[bi][at]
					}
				}
			}
		}
	}
	return &Image{Width: uint32(w), Height: uint32(h), RGBA: rgba}, nil
}
func (m *Movie) Decode(index int) (*Image, error) { return m.DecodePacket(index) }

// bytesReader avoids exposing a mutable bytes.Buffer to the decoder.
type amvByteReader struct {
	b []byte
	n int
}

func bytesReader(b []byte) io.Reader { return &amvByteReader{b: b} }
func (r *amvByteReader) Read(p []byte) (int, error) {
	if r.n == len(r.b) {
		return 0, io.EOF
	}
	n := copy(p, r.b[r.n:])
	r.n += n
	return n, nil
}

var _ io.Reader = (*amvByteReader)(nil)
