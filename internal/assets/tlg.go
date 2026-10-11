package assets

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// ErrTruncated identifies a recognized TLG stream that ends before its
// declared header, record, or compressed token is complete.
var ErrTruncated = errors.New("truncated asset")

const tlg5Header = "TLG5.0\x00raw\x1a"
const tlg6Header = "TLG6.0\x00raw\x1a"
const tlgSDSHeader = "TLG0.0\x00sds\x1a"

// DecodeTLG decodes the lossless TLG5 and TLG6 raw streams used by Kirikiri.
// Both formats are converted from their BGRA planes to the shared row-major
// RGBA Image representation.
func DecodeTLG(data []byte) (Image, error) {
	if len(data) < len("TLG") || string(data[:3]) != "TLG" {
		return Image{}, tlgMalformed("missing TLG signature")
	}
	if len(data) >= len(tlgSDSHeader) && string(data[:len(tlgSDSHeader)]) == tlgSDSHeader {
		r := tlgReader{data: data[len(tlgSDSHeader):]}
		rawLen, err := r.u32()
		if err != nil {
			return Image{}, err
		}
		raw, err := r.takeU64(uint64(rawLen))
		if err != nil {
			return Image{}, err
		}
		return decodeTLGRaw(raw)
	}
	return decodeTLGRaw(data)
}

func decodeTLGRaw(data []byte) (Image, error) {
	switch {
	case len(data) >= len(tlg5Header) && string(data[:len(tlg5Header)]) == tlg5Header:
		return decodeTLG5(data)
	case len(data) >= len(tlg6Header) && string(data[:len(tlg6Header)]) == tlg6Header:
		return decodeTLG6(data)
	case len(data) < len(tlg5Header) && len(data) >= 3 && string(data[:3]) == "TLG":
		return Image{}, tlgTruncated("incomplete TLG header")
	default:
		return Image{}, unsupported("TLG image version")
	}
}

type tlgReader struct {
	data []byte
	pos  int
}

func (r *tlgReader) take(n int) ([]byte, error) {
	if n < 0 || r.pos < 0 || r.pos > len(r.data) || n > len(r.data)-r.pos {
		return nil, tlgTruncated(fmt.Sprintf("truncated input at %#x: need %d bytes", r.pos, n))
	}
	out := r.data[r.pos : r.pos+n]
	r.pos += n
	return out, nil
}

func (r *tlgReader) takeU64(n uint64) ([]byte, error) {
	if n > uint64(len(r.data)-r.pos) || n > uint64(^uint(0)>>1) {
		return nil, tlgTruncated(fmt.Sprintf("truncated input at %#x: need %d bytes", r.pos, n))
	}
	return r.take(int(n))
}

func (r *tlgReader) u8() (byte, error) {
	b, err := r.take(1)
	if err != nil {
		return 0, err
	}
	return b[0], nil
}

func (r *tlgReader) u32() (uint32, error) {
	b, err := r.take(4)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(b), nil
}

func tlgMalformed(msg string) error { return malformed("TLG image", msg) }
func tlgTruncated(msg string) error {
	return &FormatError{Kind: ErrTruncated, Msg: "TLG image: " + msg}
}

func decodeTLG5(data []byte) (Image, error) {
	r := tlgReader{data: data[len(tlg5Header):]}
	channels, err := r.u8()
	if err != nil {
		return Image{}, err
	}
	if channels != 3 && channels != 4 {
		return Image{}, tlgMalformed(fmt.Sprintf("invalid TLG5 channel count %d", channels))
	}
	width, err := r.u32()
	if err != nil {
		return Image{}, err
	}
	height, err := r.u32()
	if err != nil {
		return Image{}, err
	}
	blockHeight, err := r.u32()
	if err != nil {
		return Image{}, err
	}
	if err := checkDimensions(width, height); err != nil {
		return Image{}, tlgMalformed("invalid TLG5 dimensions")
	}
	if blockHeight == 0 || blockHeight > maxImageHeight {
		return Image{}, tlgMalformed("invalid TLG5 block height")
	}
	blocks := (uint64(height) + uint64(blockHeight) - 1) / uint64(blockHeight)
	if blocks > uint64(len(data)/4) {
		return Image{}, tlgMalformed("invalid TLG5 block count")
	}
	blockSizes := make([]uint32, int(blocks))
	for i := range blockSizes {
		blockSizes[i], err = r.u32()
		if err != nil {
			return Image{}, err
		}
	}

	width64 := uint64(width)
	lineBytes64 := width64 * 4
	if lineBytes64 > uint64(tlgMaxInt()) {
		return Image{}, fmt.Errorf("%w: TLG5 scanline exceeds addressable memory", ErrLimit)
	}
	lineBytes := int(lineBytes64)
	pixelBytes64 := uint64(width) * uint64(height) * 4
	if pixelBytes64 > uint64(tlgMaxInt()) {
		return Image{}, fmt.Errorf("%w: TLG5 output exceeds addressable memory", ErrLimit)
	}
	image := Image{Width: width, Height: height, RGBA: make([]byte, int(pixelBytes64))}
	prev := make([]byte, lineBytes)
	cur := make([]byte, lineBytes)
	var dict [4096]byte
	dictPos := 0

	for block := 0; block < int(blocks); block++ {
		top := uint64(block) * uint64(blockHeight)
		rows := uint64(blockHeight)
		if rows > uint64(height)-top {
			rows = uint64(height) - top
		}
		size64 := rows * width64
		if size64 > uint64(tlgMaxInt()) || size64*uint64(channels) > maxPixels*4 {
			return Image{}, fmt.Errorf("%w: TLG5 block buffers exceed memory limit", ErrLimit)
		}
		size := int(size64)
		planes := make([][]byte, int(channels))
		blockStart := r.pos
		for c := range planes {
			mode, e := r.u8()
			if e != nil {
				return Image{}, e
			}
			packed, e := r.u32()
			if e != nil {
				return Image{}, e
			}
			plane, e := r.takeU64(uint64(packed))
			if e != nil {
				return Image{}, e
			}
			switch mode {
			case 0:
				if uint64(packed) > size64+10 {
					return Image{}, fmt.Errorf("%w: TLG5 compressed plane exceeds decoder buffer", ErrLimit)
				}
				planes[c] = make([]byte, size)
				if e := decodeTLG5Slide(planes[c], plane, &dict, &dictPos); e != nil {
					return Image{}, e
				}
			case 1:
				if uint64(packed) != size64 {
					return Image{}, tlgMalformed("TLG5 raw plane length mismatch")
				}
				planes[c] = plane
			default:
				return Image{}, unsupported(fmt.Sprintf("TLG5 plane encoding %d", mode))
			}
		}
		if uint64(r.pos-blockStart) != uint64(blockSizes[block]) {
			return Image{}, tlgMalformed("TLG5 block size mismatch")
		}

		for y := uint64(0); y < rows; y++ {
			var pc [4]byte
			for x := uint64(0); x < width64; x++ {
				i := int(y*width64 + x)
				b := planes[0][i]
				g := planes[1][i]
				rv := planes[2][i]
				b += g
				rv += g
				pc[0] += b
				pc[1] += g
				pc[2] += rv
				px := int(x) * 4
				cur[px] = pc[0] + prev[px]
				cur[px+1] = pc[1] + prev[px+1]
				cur[px+2] = pc[2] + prev[px+2]
				if channels == 4 {
					pc[3] += planes[3][i]
					cur[px+3] = pc[3] + prev[px+3]
				} else {
					cur[px+3] = 0xff
				}
				out := (int(top+y)*int(width) + int(x)) * 4
				image.RGBA[out] = cur[px+2]
				image.RGBA[out+1] = cur[px+1]
				image.RGBA[out+2] = cur[px]
				image.RGBA[out+3] = cur[px+3]
			}
			prev, cur = cur, prev
			for i := range cur {
				cur[i] = 0
			}
		}
	}
	if r.pos != len(r.data) {
		return Image{}, tlgMalformed("trailing TLG5 raw data")
	}
	return image, nil
}

func decodeTLG6(data []byte) (Image, error) {
	r := tlgReader{data: data[len(tlg6Header):]}
	colors, err := r.u8()
	if err != nil {
		return Image{}, err
	}
	if colors != 1 && colors != 3 && colors != 4 {
		return Image{}, tlgMalformed(fmt.Sprintf("invalid TLG6 channel count %d", colors))
	}
	dataFlag, err := r.u8()
	if err != nil {
		return Image{}, err
	}
	colorType, err := r.u8()
	if err != nil {
		return Image{}, err
	}
	externalTable, err := r.u8()
	if err != nil {
		return Image{}, err
	}
	if dataFlag != 0 {
		return Image{}, unsupported("TLG6 data flag")
	}
	if colorType != 0 {
		return Image{}, unsupported("TLG6 color type")
	}
	if externalTable != 0 {
		return Image{}, unsupported("TLG6 external Golomb table")
	}
	width, err := r.u32()
	if err != nil {
		return Image{}, err
	}
	height, err := r.u32()
	if err != nil {
		return Image{}, err
	}
	if err := checkDimensions(width, height); err != nil {
		return Image{}, tlgMalformed("invalid TLG6 dimensions")
	}
	maxBitLength, err := r.u32()
	if err != nil {
		return Image{}, err
	}
	if maxBitLength > uint32(^uint32(0)>>2) {
		return Image{}, tlgMalformed("invalid TLG6 bit length")
	}

	width64, height64 := uint64(width), uint64(height)
	xBlocks := (width64 + 7) / 8
	yBlocks := (height64 + 7) / 8
	filterCount := xBlocks * yBlocks
	if filterCount == 0 || filterCount > uint64(tlgMaxInt()) {
		return Image{}, fmt.Errorf("%w: TLG6 filter table exceeds addressable memory", ErrLimit)
	}
	filterPacked, err := r.u32()
	if err != nil {
		return Image{}, err
	}
	if uint64(filterPacked) > uint64(len(r.data)-r.pos) {
		return Image{}, tlgTruncated("truncated TLG6 filter stream")
	}
	filterInput, err := r.take(int(filterPacked))
	if err != nil {
		return Image{}, err
	}
	filterTypes := make([]byte, int(filterCount))
	var filterDict [4096]byte
	tlg6InitFilterDict(&filterDict)
	filterPos := 0
	if err := decodeTLG5Slide(filterTypes, filterInput, &filterDict, &filterPos); err != nil {
		return Image{}, err
	}

	pixelBytes64 := width64 * height64 * 4
	if pixelBytes64 > uint64(tlgMaxInt()) {
		return Image{}, fmt.Errorf("%w: TLG6 output exceeds addressable memory", ErrLimit)
	}
	lineBytes64 := width64 * 4
	if lineBytes64 > uint64(tlgMaxInt()) {
		return Image{}, fmt.Errorf("%w: TLG6 scanline exceeds addressable memory", ErrLimit)
	}
	image := Image{Width: width, Height: height, RGBA: make([]byte, int(pixelBytes64))}
	pixelCountLimit := width64 * 8
	if pixelCountLimit > uint64(tlgMaxInt()) || pixelCountLimit*4 > maxPixels*4 {
		return Image{}, fmt.Errorf("%w: TLG6 block buffer exceeds memory limit", ErrLimit)
	}
	pixelbuf := make([]uint32, int(pixelCountLimit))
	prevline := make([]uint32, int(width))
	curline := make([]uint32, int(width))
	initial := uint32(0)
	if colors == 3 {
		initial = 0xff000000
		for i := range prevline {
			prevline[i] = initial
		}
	}
	xBlockCount := int(xBlocks)
	mainCount := int(width / 8)
	fraction := int(width % 8)
	for y := uint64(0); y < height64; y += 8 {
		rows := int(height64 - y)
		if rows > 8 {
			rows = 8
		}
		pixelCount := rows * int(width)
		for i := range pixelbuf[:pixelCount] {
			pixelbuf[i] = 0
		}
		for c := range int(colors) {
			bitLengthWord, err := r.u32()
			if err != nil {
				return Image{}, err
			}
			method := bitLengthWord >> 30
			bitLength := bitLengthWord & 0x3fffffff
			if method != 0 {
				return Image{}, unsupported(fmt.Sprintf("TLG6 entropy coding method %d", method))
			}
			if bitLength > maxBitLength {
				return Image{}, tlgMalformed("TLG6 bit length exceeds declared maximum")
			}
			byteLength := uint64(bitLength+7) / 8
			if byteLength > uint64(len(r.data)-r.pos) {
				return Image{}, tlgTruncated("truncated TLG6 entropy stream")
			}
			bits, err := r.take(int(byteLength))
			if err != nil {
				return Image{}, err
			}
			br := tlg6BitReader{data: bits, bits: uint64(bitLength)}
			if err := decodeTLG6Golomb(&br, pixelbuf[:pixelCount], c, int(colors), pixelCount); err != nil {
				return Image{}, err
			}
		}
		filterOffset := int((y / 8) * xBlocks)
		for yy := 0; yy < rows; yy++ {
			if mainCount != 0 {
				oddskip := rows - 1 - 2*yy
				if err := decodeTLG6Line(prevline, curline, int(width), 0, mainCount,
					filterTypes[filterOffset:], rows*8, pixelbuf[8*yy:], initial, oddskip, (yy&1)^1); err != nil {
					return Image{}, err
				}
			}
			if mainCount != xBlockCount {
				oddskip := rows - 1 - 2*yy
				ww := fraction
				if ww == 0 {
					ww = 8
				}
				if err := decodeTLG6Line(prevline, curline, int(width), mainCount, xBlockCount,
					filterTypes[filterOffset:], rows*8, pixelbuf[ww*yy:], initial, oddskip, (yy&1)^1); err != nil {
					return Image{}, err
				}
			}
			out := int(y+uint64(yy)) * int(width) * 4
			for x := range int(width) {
				p := curline[x]
				if colors == 1 {
					v := byte(p)
					image.RGBA[out] = v
					image.RGBA[out+1] = v
					image.RGBA[out+2] = v
					image.RGBA[out+3] = 0xff
				} else {
					image.RGBA[out] = byte(p >> 16)
					image.RGBA[out+1] = byte(p >> 8)
					image.RGBA[out+2] = byte(p)
					image.RGBA[out+3] = byte(p >> 24)
				}
				out += 4
			}
			prevline, curline = curline, prevline
		}
	}
	if r.pos != len(r.data) {
		return Image{}, tlgMalformed("trailing TLG6 raw data")
	}
	return image, nil
}

func tlg6InitFilterDict(dict *[4096]byte) {
	pos := 0
	for i := range 32 {
		for j := range 16 {
			for range 4 {
				dict[pos] = byte(i)
				pos++
			}
			for range 4 {
				dict[pos] = byte(j)
				pos++
			}
		}
	}
}

type tlg6BitReader struct {
	data []byte
	bits uint64
	pos  uint64
}

func (b *tlg6BitReader) read(n uint) (uint32, error) {
	if b.pos > b.bits || uint64(n) > b.bits-b.pos {
		return 0, tlgTruncated("truncated TLG6 Golomb bitstream")
	}
	var value uint32
	for i := uint(0); i < n; i++ {
		if b.data[(b.pos+uint64(i))>>3]&(1<<((b.pos+uint64(i))&7)) != 0 {
			value |= 1 << i
		}
	}
	b.pos += uint64(n)
	return value, nil
}

func (b *tlg6BitReader) gamma() (uint32, error) {
	zeros := uint(0)
	for {
		v, err := b.read(1)
		if err != nil {
			return 0, err
		}
		if v != 0 {
			break
		}
		zeros++
		if zeros >= 31 {
			return 0, tlgMalformed("TLG6 Golomb run length is too large")
		}
	}
	rest, err := b.read(zeros)
	if err != nil {
		return 0, err
	}
	return (1 << zeros) | rest, nil
}
func (b *tlg6BitReader) golombQuotient() (uint32, error) {
	bytePos := b.pos >> 3
	if b.bits >= b.pos && b.bits-b.pos >= 32 && bytePos+3 < uint64(len(b.data)) {
		word := uint32(b.data[bytePos]) |
			uint32(b.data[bytePos+1])<<8 |
			uint32(b.data[bytePos+2])<<16 |
			uint32(b.data[bytePos+3])<<24
		if word>>uint(b.pos&7) == 0 {
			if bytePos+4 >= uint64(len(b.data)) {
				return 0, tlgTruncated("truncated TLG6 extended Golomb value")
			}
			quotient := uint32(b.data[bytePos+4])
			b.pos = (bytePos + 5) * 8
			if b.pos > b.bits {
				return 0, tlgTruncated("truncated TLG6 extended Golomb value")
			}
			return quotient, nil
		}
	}
	quotient := uint32(0)
	for {
		bit, err := b.read(1)
		if err != nil {
			return 0, err
		}
		if bit != 0 {
			return quotient, nil
		}
		if quotient == ^uint32(0) {
			return 0, tlgMalformed("TLG6 Golomb value is too large")
		}
		quotient++
	}
}

func decodeTLG6Golomb(br *tlg6BitReader, pixels []uint32, channel, colors, count int) error {
	if count == 0 {
		return nil
	}
	zeroBit, err := br.read(1)
	if err != nil {
		return err
	}
	zeroRun := zeroBit == 0
	n, a := 3, 0
	for pos := 0; pos < count; {
		run, err := br.gamma()
		if err != nil {
			return err
		}
		if run == 0 || uint64(run) > uint64(count-pos) {
			return tlgMalformed("TLG6 Golomb run exceeds block")
		}
		if zeroRun {
			pos += int(run)
			zeroRun = false
			continue
		}
		for i := uint32(0); i < run; i++ {
			if a < 0 || a >= len(tlg6GolombBitLengthTable) {
				return tlgMalformed(fmt.Sprintf("TLG6 Golomb predictor state %d exceeds table bound %d", a, len(tlg6GolombBitLengthTable)))
			}
			k := uint(tlg6GolombBitLengthTable[a][n])
			quotient, err := br.golombQuotient()
			if err != nil {
				return err
			}
			if quotient > ^uint32(0)>>k {
				return tlgMalformed("TLG6 Golomb value is too large")
			}
			remainder, err := br.read(k)
			if err != nil {
				return err
			}
			m := (quotient << k) | remainder
			var value int
			if m&1 != 0 {
				value = int(m>>1) + 1
			} else {
				value = -int(m>>1) - 1
			}
			p := &pixels[pos]
			if channel == 0 && colors != 1 {
				*p = uint32(byte(value))
			} else {
				shift := uint(channel * 8)
				*p = (*p &^ (0xff << shift)) | uint32(byte(value))<<shift
			}
			pos++
			a += int(m >> 1)
			n--
			if n < 0 {
				a >>= 1
				n = 3
			}
		}
		zeroRun = true
	}
	return nil
}

var tlg6GolombBitLengthTable = makeTLG6GolombBitLengthTable()

func makeTLG6GolombBitLengthTable() [1024][4]byte {
	var table [1024][4]byte
	compressed := [4][9]int{
		{3, 7, 15, 27, 63, 108, 223, 448, 130},
		{3, 5, 13, 24, 51, 95, 192, 384, 257},
		{2, 5, 12, 21, 39, 86, 155, 320, 384},
		{2, 3, 9, 18, 33, 61, 129, 258, 511},
	}
	for n := 0; n < 4; n++ {
		pos := 0
		for k, amount := range compressed[n] {
			for i := 0; i < amount; i++ {
				table[pos][n] = byte(k)
				pos++
			}
		}
	}
	return table
}

func decodeTLG6Line(prevline, curline []uint32, width, startBlock, blockLimit int,
	filtertypes []byte, skipblockbytes int, in []uint32, initialp uint32, oddskip, dir int) error {
	var p, up uint32
	startX := startBlock * 8
	if startBlock != 0 {
		p = curline[startX-1]
		up = prevline[startX-1]
	} else {
		p, up = initialp, initialp
	}
	idx := skipblockbytes * startBlock
	step := 1
	if dir&1 == 0 {
		step = -1
	}
	for block := startBlock; block < blockLimit; block++ {
		w := width - block*8
		if w > 8 {
			w = 8
		}
		if w <= 0 || block >= len(filtertypes) || filtertypes[block] > 31 {
			return tlgMalformed("invalid TLG6 filter block")
		}
		if step == -1 {
			idx += w - 1
		}
		if block&1 != 0 {
			idx += oddskip * w
		}
		baseX := block * 8
		for j := 0; j < w; j++ {
			if idx < 0 || idx >= len(in) {
				return tlgMalformed("TLG6 reordered block exceeds buffer")
			}
			x := baseX + j
			u := prevline[x]
			residual := in[idx]
			curline[x] = tlg6Predict(p, u, up, residual, filtertypes[block])
			p, up = curline[x], u
			idx += step
		}
		if step == 1 {
			idx += skipblockbytes - w
		} else {
			idx += skipblockbytes + 1
		}
		if block&1 != 0 {
			idx -= oddskip * w
		}
	}
	return nil
}

func tlg6Predict(p, u, up, in uint32, filter byte) uint32 {
	ib, ig, ir, ia := int8(byte(in)), int8(byte(in>>8)), int8(byte(in>>16)), int8(byte(in>>24))
	var b, g, r int8
	switch filter >> 1 {
	case 0:
		b, g, r = ib, ig, ir
	case 1:
		b, g, r = ib+ig, ig, ir+ig
	case 2:
		b, g, r = ib, ig+ib, ir+ib+ig
	case 3:
		b, g, r = ib+ir+ig, ig+ir, ir
	case 4:
		b, g, r = ib+ir, ig+ib+ir, ir+ib+ir+ig
	case 5:
		b, g, r = ib+ir, ig+ib+ir, ir
	case 6:
		b, g, r = ib+ig, ig, ir
	case 7:
		b, g, r = ib, ig+ib, ir
	case 8:
		b, g, r = ib, ig, ir+ig
	case 9:
		b, g, r = ib+ig+ir+ib, ig+ir+ib, ir+ib
	case 10:
		b, g, r = ib+ir, ig+ir, ir
	case 11:
		b, g, r = ib, ig+ib, ir+ib
	case 12:
		b, g, r = ib, ig+ir+ib, ir+ib
	case 13:
		b, g, r = ib+ig, ig+ir+ib+ig, ir+ib+ig
	case 14:
		b, g, r = ib+ig+ir, ig+ir, ir+ib+ig+ir
	case 15:
		b, g, r = ib, ig+(ib<<1), ir+(ib<<1)
	default:
		return 0
	}
	var pred uint32
	if filter&1 == 0 {
		pred = tlg6Med(p, u, up)
	} else {
		pred = tlg6Avg(p, u)
	}
	return uint32(byte(pred)+byte(b)) | uint32(byte(pred>>8)+byte(g))<<8 |
		uint32(byte(pred>>16)+byte(r))<<16 | uint32(byte(pred>>24)+byte(ia))<<24
}

func tlg6Med(a, b, c uint32) uint32 {
	var out uint32
	for shift := uint(0); shift < 32; shift += 8 {
		av, bv, cv := int(byte(a>>shift)), int(byte(b>>shift)), int(byte(c>>shift))
		minv, maxv := av, bv
		if minv > maxv {
			minv, maxv = maxv, minv
		}
		var v int
		if cv >= maxv {
			v = minv
		} else if cv < minv {
			v = maxv
		} else {
			v = av + bv - cv
		}
		out |= uint32(byte(v)) << shift
	}
	return out
}

func tlg6Avg(a, b uint32) uint32 {
	var out uint32
	for shift := uint(0); shift < 32; shift += 8 {
		v := (int(byte(a>>shift)) + int(byte(b>>shift)) + 1) >> 1
		out |= uint32(byte(v)) << shift
	}
	return out
}

func tlgMaxInt() int { return int(^uint(0) >> 1) }

func decodeTLG5Slide(dst, src []byte, dict *[4096]byte, dictPos *int) error {
	produced := 0
	flags := uint16(0)
	for len(src) != 0 {
		flags >>= 1
		if flags&0x100 == 0 {
			if len(src) < 1 {
				return tlgTruncated("incomplete TLG5 flag byte")
			}
			flags = uint16(src[0]) | 0xff00
			src = src[1:]
		}
		if flags&1 != 0 {
			if len(src) < 2 {
				return tlgTruncated("incomplete TLG5 back-reference")
			}
			mpos := int(src[0]) | int(src[1]&0x0f)<<8
			mlen := int(src[1]>>4) + 3
			src = src[2:]
			if mlen == 18 {
				if len(src) < 1 {
					return tlgTruncated("incomplete TLG5 match length")
				}
				mlen += int(src[0])
				src = src[1:]
			}
			if mlen > len(dst)-produced {
				return tlgMalformed("TLG5 compressed plane output exceeds declared size")
			}
			for i := 0; i < mlen; i++ {
				v := dict[mpos]
				dst[produced] = v
				dict[*dictPos] = v
				produced++
				mpos = (mpos + 1) & 4095
				*dictPos = (*dictPos + 1) & 4095
			}
		} else {
			if len(src) < 1 {
				return tlgTruncated("incomplete TLG5 literal")
			}
			if produced == len(dst) {
				return tlgMalformed("TLG5 compressed plane output exceeds declared size")
			}
			v := src[0]
			src = src[1:]
			dst[produced] = v
			dict[*dictPos] = v
			produced++
			*dictPos = (*dictPos + 1) & 4095
		}
	}
	if produced != len(dst) {
		return tlgMalformed("incomplete TLG5 compressed plane")
	}
	return nil
}
