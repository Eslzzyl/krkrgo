// Bitplane decoding translated from libavcodec/vc1.c bitplane_decoding
// (VC-1 section 8.7), LGPL-2.1-or-later; see doc.go for the full
// license notice.
package wmv3

import "fmt"

// DecodeBitplane decodes one VC-1 bitplane of width by height bits in
// row-major order. It follows vc1.c bitplane_decoding with stride equal
// to width, which is all the progressive Simple/Main frame header needs:
// the planes are macroblock grids owned by the returned slice.
//
// The INVERT bit and the IMODE VLC select the coding mode. IMODE_RAW
// sets raw, in which case plane is nil and the macroblock layer reads
// one bit per block directly (FFmpeg's raw_flag path); invert is then
// ignored. Otherwise plane holds the decoded bits and raw is false.
//
// width and height are macroblock counts supplied by the Decoder, and
// the cell count is capped so corrupt dimensions cannot force a large
// allocation.
func DecodeBitplane(r *BitReader, width, height int) (plane []uint8, raw bool, imode int, err error) {
	if width < 1 || height < 1 || int64(width)*int64(height) > maxBitplaneCells {
		return nil, false, 0, fmt.Errorf("bitplane %dx%d: %w", width, height, ErrInvalidValue)
	}
	invert, err := r.Bit()
	if err != nil {
		return nil, false, 0, err
	}
	imode, err = decodeVLC(r, imodeCodes, imodeBits)
	if err != nil {
		return nil, false, 0, fmt.Errorf("bitplane imode: %w", err)
	}
	if imode == ImodeRaw {
		return nil, true, imode, nil
	}
	plane = make([]uint8, width*height)
	switch imode {
	case ImodeNorm2, ImodeDiff2:
		err = decodeNorm2(r, plane, width, height)
	case ImodeNorm6, ImodeDiff6:
		err = decodeNorm6(r, plane, width, height)
	case ImodeRowSkip:
		err = decodeRowskip(r, plane, width, height, width)
	case ImodeColSkip:
		err = decodeColskip(r, plane, width, height, width)
	default:
		return nil, false, imode, fmt.Errorf("bitplane imode %d: %w", imode, ErrInvalidValue)
	}
	if err != nil {
		return nil, false, imode, err
	}
	if imode == ImodeDiff2 || imode == ImodeDiff6 {
		applyDiff(plane, width, height, width, invert)
	} else if invert {
		for i := range plane {
			plane[i] ^= 1
		}
	}
	return plane, false, imode, nil
}

// maxBitplaneCells caps one bitplane allocation: 512x512 macroblocks,
// well above the 80x45 grid of a 1280x720 frame.
const maxBitplaneCells = 512 * 512

func decodeNorm2(r *BitReader, plane []uint8, width, height int) error {
	i := 0
	if (width*height)&1 != 0 {
		b, err := r.Bit()
		if err != nil {
			return err
		}
		plane[0] = boolToUint8(b)
		i = 1
	}
	for ; i < width*height; i += 2 {
		code, err := decodeVLC(r, norm2Codes, norm2Bits)
		if err != nil {
			return fmt.Errorf("norm2: %w", err)
		}
		plane[i] = uint8(code & 1)
		plane[i+1] = uint8(code >> 1)
	}
	return nil
}

func decodeNorm6(r *BitReader, plane []uint8, width, height int) error {
	stride := width
	if height%3 == 0 && width%3 != 0 {
		// 2x3 tiling plus a leftover column handled by colskip.
		for y := 0; y < height; y += 3 {
			row := plane[y*stride:]
			for x := width & 1; x < width; x += 2 {
				code, err := decodeVLC(r, norm6Codes, norm6Bits)
				if err != nil {
					return fmt.Errorf("norm6: %w", err)
				}
				row[x] = uint8(code>>0) & 1
				row[x+1] = uint8(code>>1) & 1
				row[x+stride] = uint8(code>>2) & 1
				row[x+1+stride] = uint8(code>>3) & 1
				row[x+2*stride] = uint8(code>>4) & 1
				row[x+1+2*stride] = uint8(code>>5) & 1
			}
		}
		if width&1 != 0 {
			if err := decodeColskipCols(r, plane, 0, 1, height, stride); err != nil {
				return err
			}
		}
		return nil
	}
	// 3x2 tiling plus leftover columns (colskip) and a leftover row
	// (rowskip), matching the else branch in vc1.c.
	off := (height & 1) * stride
	for y := height & 1; y < height; y += 2 {
		row := plane[off+(y-(height&1))*stride:]
		for x := width % 3; x < width; x += 3 {
			code, err := decodeVLC(r, norm6Codes, norm6Bits)
			if err != nil {
				return fmt.Errorf("norm6: %w", err)
			}
			row[x] = uint8(code>>0) & 1
			row[x+1] = uint8(code>>1) & 1
			row[x+2] = uint8(code>>2) & 1
			row[x+stride] = uint8(code>>3) & 1
			row[x+1+stride] = uint8(code>>4) & 1
			row[x+2+stride] = uint8(code>>5) & 1
		}
	}
	if x := width % 3; x != 0 {
		if err := decodeColskipCols(r, plane, 0, x, height, stride); err != nil {
			return err
		}
	}
	if height&1 != 0 {
		if err := decodeRowskipRow(r, plane[width%3:], width-width%3); err != nil {
			return err
		}
	}
	return nil
}

func decodeRowskip(r *BitReader, plane []uint8, width, height, stride int) error {
	for y := range height {
		if err := decodeRowskipRow(r, plane[y*stride:], width); err != nil {
			return err
		}
	}
	return nil
}

func decodeRowskipRow(r *BitReader, row []uint8, width int) error {
	skip, err := r.Bit()
	if err != nil {
		return err
	}
	if !skip {
		for x := range width {
			row[x] = 0
		}
		return nil
	}
	for x := range width {
		b, err := r.Bit()
		if err != nil {
			return err
		}
		row[x] = boolToUint8(b)
	}
	return nil
}

func decodeColskip(r *BitReader, plane []uint8, width, height, stride int) error {
	return decodeColskipCols(r, plane, 0, width, height, stride)
}

// decodeColskipCols decodes ncols columns starting at col x0. A short
// plane is a truncated stream and surfaces ErrTruncated through BitReader.
func decodeColskipCols(r *BitReader, plane []uint8, x0, ncols, height, stride int) error {
	for x := x0; x < x0+ncols; x++ {
		skip, err := r.Bit()
		if err != nil {
			return err
		}
		if !skip {
			for y := range height {
				plane[y*stride+x] = 0
			}
			continue
		}
		for y := range height {
			b, err := r.Bit()
			if err != nil {
				return err
			}
			plane[y*stride+x] = boolToUint8(b)
		}
	}
	return nil
}

// applyDiff applies the DIFF-N differential operator in place, exactly
// as the tail of vc1.c bitplane_decoding does.
func applyDiff(plane []uint8, width, height, stride int, invert bool) {
	iv := boolToUint8(invert)
	plane[0] ^= iv
	for x := 1; x < width; x++ {
		plane[x] ^= plane[x-1]
	}
	for y := 1; y < height; y++ {
		row := plane[y*stride : y*stride+width]
		prev := plane[(y-1)*stride : (y-1)*stride+width]
		row[0] ^= prev[0]
		for x := 1; x < width; x++ {
			if row[x-1] != prev[x] {
				row[x] ^= iv
			} else {
				row[x] ^= row[x-1]
			}
		}
	}
}

func boolToUint8(b bool) uint8 {
	if b {
		return 1
	}
	return 0
}
