package assets

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"hash/crc32"
	"io"
)

// IndexedImage preserves palette indices for palettized images. Palette nil
// denotes grayscale, in which case Pixels contains expanded 8-bit luminance.
type IndexedImage struct {
	Width   uint32
	Height  uint32
	Pixels  []byte
	Palette [][3]byte
}

// DecodeIndexedImage decodes palettized or grayscale PNG and BMP images while
// preserving palette indices and grayscale values.
func DecodeIndexedImage(data []byte) (IndexedImage, error) {
	if bytes.HasPrefix(data, []byte("BM")) {
		return decodeBMPIndexed(data)
	}
	if bytes.HasPrefix(data, []byte(pngSignature)) {
		return decodePNGIndexed(data)
	}
	return IndexedImage{}, unsupported("indexed image format")
}

// DecodeTransitionRuleImage converts a transition rule image into one byte per
// pixel. KAG rule images are ordinary RGBA PNG/WebP files whose RGB luminance
// selects the transition threshold; only palettized and grayscale PNG/BMP data
// carry indices directly.
func DecodeTransitionRuleImage(data []byte) (IndexedImage, error) {
	image, err := DecodeImage(data)
	if err != nil {
		return IndexedImage{}, err
	}
	pixels := make([]byte, uint64(image.Width)*uint64(image.Height))
	for i := range pixels {
		offset := i * 4
		luma := (299*uint32(image.RGBA[offset]) + 587*uint32(image.RGBA[offset+1]) + 114*uint32(image.RGBA[offset+2]) + 500) / 1000
		pixels[i] = byte(luma)
	}
	return IndexedImage{Width: image.Width, Height: image.Height, Pixels: pixels}, nil
}

// DecodeIndexed is an alias for DecodeIndexedImage.
func DecodeIndexed(data []byte) (IndexedImage, error) { return DecodeIndexedImage(data) }

// Decode is a method-shaped adapter for IndexedImage::decode.
func (IndexedImage) Decode(data []byte) (IndexedImage, error) {
	return DecodeIndexedImage(data)
}

const pngSignature = "\x89PNG\r\n\x1a\n"

func decodePNGIndexed(data []byte) (IndexedImage, error) {
	if len(data) < 8 {
		return IndexedImage{}, malformed("PNG", "truncated signature")
	}
	pos := 8
	var width, height uint32
	var depth, colorType, interlace byte
	var palette [][3]byte
	var idat bytes.Buffer
	seenHeader, seenEnd := false, false
	for pos < len(data) {
		if len(data)-pos < 12 {
			return IndexedImage{}, malformed("PNG", "truncated chunk")
		}
		size := uint64(binary.BigEndian.Uint32(data[pos : pos+4]))
		if size > uint64(len(data)-pos-12) {
			return IndexedImage{}, malformed("PNG", "truncated chunk data")
		}
		kind := data[pos+4 : pos+8]
		body := data[pos+8 : pos+8+int(size)]
		wantCRC := binary.BigEndian.Uint32(data[pos+8+int(size) : pos+12+int(size)])
		crc := crc32.NewIEEE()
		_, _ = crc.Write(kind)
		_, _ = crc.Write(body)
		if crc.Sum32() != wantCRC {
			return IndexedImage{}, malformed("PNG", "chunk CRC mismatch")
		}
		pos += 12 + int(size)
		switch string(kind) {
		case "IHDR":
			if seenHeader || len(body) != 13 || body[10] != 0 || body[11] != 0 {
				return IndexedImage{}, malformed("PNG", "invalid IHDR")
			}
			seenHeader = true
			width = binary.BigEndian.Uint32(body[:4])
			height = binary.BigEndian.Uint32(body[4:8])
			depth, colorType, interlace = body[8], body[9], body[12]
			if err := checkDimensions(width, height); err != nil {
				return IndexedImage{}, err
			}
			if interlace != 0 {
				return IndexedImage{}, unsupported("interlaced indexed PNG")
			}
			if (colorType != 0 && colorType != 3) || (depth != 1 && depth != 2 && depth != 4 && depth != 8) {
				return IndexedImage{}, unsupported("PNG color type or depth")
			}
		case "PLTE":
			if len(body) == 0 || len(body)%3 != 0 || len(body)/3 > 256 {
				return IndexedImage{}, malformed("PNG", "invalid palette")
			}
			palette = make([][3]byte, len(body)/3)
			for i := range palette {
				copy(palette[i][:], body[i*3:i*3+3])
			}
		case "IDAT":
			if _, err := idat.Write(body); err != nil {
				return IndexedImage{}, err
			}
		case "IEND":
			if len(body) != 0 {
				return IndexedImage{}, malformed("PNG", "invalid IEND")
			}
			seenEnd = true
			pos = len(data)
		}
	}
	if !seenHeader || !seenEnd {
		return IndexedImage{}, malformed("PNG", "missing required chunk")
	}
	if colorType == 3 && len(palette) == 0 {
		return IndexedImage{}, malformed("PNG", "palette missing")
	}
	if colorType == 0 && len(palette) != 0 {
		// A PLTE chunk is legal for grayscale PNG, but it is not the image's
		// palette and must not change grayscale semantics.
		palette = nil
	}
	rowBytes := (int(width)*int(depth) + 7) / 8
	rawSize := uint64(height) * uint64(rowBytes+1)
	if rawSize > maxPixels+uint64(height) || rawSize > uint64(^uint(0)>>1) {
		return IndexedImage{}, malformed("PNG", "scanline buffer exceeds limit")
	}
	zr, err := zlib.NewReader(bytes.NewReader(idat.Bytes()))
	if err != nil {
		return IndexedImage{}, malformed("PNG", "invalid image data: "+err.Error())
	}
	raw, err := io.ReadAll(io.LimitReader(zr, int64(rawSize)+1))
	closeErr := zr.Close()
	if err != nil {
		return IndexedImage{}, malformed("PNG", "invalid image data: "+err.Error())
	}
	if closeErr != nil {
		return IndexedImage{}, malformed("PNG", "invalid image data: "+closeErr.Error())
	}
	if uint64(len(raw)) != rawSize {
		return IndexedImage{}, malformed("PNG", "invalid scanline data size")
	}
	pixels := make([]byte, int(uint64(width)*uint64(height)))
	previous := make([]byte, rowBytes)
	current := make([]byte, rowBytes)
	maxValue := (1 << depth) - 1
	out := 0
	for y := range int(height) {
		filter := raw[y*(rowBytes+1)]
		copy(current, raw[y*(rowBytes+1)+1:y*(rowBytes+1)+1+rowBytes])
		if err := unfilterPNG(current, previous, filter, 1); err != nil {
			return IndexedImage{}, err
		}
		for x := range int(width) {
			value := (current[x*int(depth)/8] >> (8 - depth - byte(x*int(depth)%8))) & byte(maxValue)
			if colorType == 0 {
				pixels[out] = byte(int(value) * 255 / maxValue)
			} else {
				if int(value) >= len(palette) {
					return IndexedImage{}, malformed("PNG", "palette index out of range")
				}
				pixels[out] = value
			}
			out++
		}
		previous, current = current, previous
	}
	return IndexedImage{Width: width, Height: height, Pixels: pixels, Palette: palette}, nil
}

func unfilterPNG(row, previous []byte, filter byte, bytesPerPixel int) error {
	if filter > 4 {
		return malformed("PNG", "invalid scanline filter")
	}
	for i := range row {
		left := byte(0)
		if i >= bytesPerPixel {
			left = row[i-bytesPerPixel]
		}
		up := previous[i]
		upperLeft := byte(0)
		if i >= bytesPerPixel {
			upperLeft = previous[i-bytesPerPixel]
		}
		switch filter {
		case 1:
			row[i] += left
		case 2:
			row[i] += up
		case 3:
			row[i] += byte((uint16(left) + uint16(up)) / 2)
		case 4:
			p := int(left) + int(up) - int(upperLeft)
			pa, pb, pc := absInt(p-int(left)), absInt(p-int(up)), absInt(p-int(upperLeft))
			predictor := left
			if pb < pa && pb <= pc {
				predictor = up
			} else if pc < pa && pc < pb {
				predictor = upperLeft
			}
			row[i] += predictor
		}
	}
	return nil
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func decodeBMPImage(data []byte) (Image, error) {
	decoded, err := parseBMP(data, false)
	if err != nil {
		return Image{}, err
	}
	return Image{Width: decoded.width, Height: decoded.height, RGBA: decoded.rgba}, nil
}

func decodeBMPIndexed(data []byte) (IndexedImage, error) {
	decoded, err := parseBMP(data, true)
	if err != nil {
		return IndexedImage{}, err
	}
	if decoded.palette == nil {
		return IndexedImage{}, malformed("BMP", "image is not palettized")
	}
	return IndexedImage{Width: decoded.width, Height: decoded.height, Pixels: decoded.pixels, Palette: decoded.palette}, nil
}

type bmpDecoded struct {
	width, height uint32
	rgba          []byte
	pixels        []byte
	palette       [][3]byte
}

func parseBMP(data []byte, indexed bool) (bmpDecoded, error) {
	if len(data) < 26 || string(data[:2]) != "BM" {
		return bmpDecoded{}, malformed("BMP", "invalid header")
	}
	offBits := uint64(binary.LittleEndian.Uint32(data[10:14]))
	dibSize := uint64(binary.LittleEndian.Uint32(data[14:18]))
	if dibSize < 40 || dibSize > uint64(len(data)-14) {
		return bmpDecoded{}, unsupported("BMP DIB header")
	}
	dib := data[14 : 14+int(dibSize)]
	w := int64(int32(binary.LittleEndian.Uint32(dib[4:8])))
	h := int64(int32(binary.LittleEndian.Uint32(dib[8:12])))
	if w <= 0 || h == 0 || h == -1<<63 || w > int64(maxImageWidth) || h > int64(maxImageHeight) || -h > int64(maxImageHeight) {
		return bmpDecoded{}, malformed("BMP", "invalid dimensions")
	}
	width, height := uint32(w), uint32(h)
	if h < 0 {
		height = uint32(-h)
	}
	if err := checkDimensions(width, height); err != nil {
		return bmpDecoded{}, err
	}
	if binary.LittleEndian.Uint16(dib[12:14]) != 1 {
		return bmpDecoded{}, malformed("BMP", "invalid planes")
	}
	bits := binary.LittleEndian.Uint16(dib[14:16])
	compression := binary.LittleEndian.Uint32(dib[16:20])
	if indexed {
		if bits != 1 && bits != 4 && bits != 8 {
			return bmpDecoded{}, malformed("BMP", "province BMP must be palettized")
		}
		if compression != 0 {
			return bmpDecoded{}, unsupported("compressed indexed BMP")
		}
	} else if bits != 1 && bits != 4 && bits != 8 && bits != 16 && bits != 24 && bits != 32 {
		return bmpDecoded{}, unsupported("BMP bit depth")
	}
	if compression != 0 {
		return bmpDecoded{}, unsupported("compressed BMP")
	}
	paletteCount := uint32(0)
	if bits <= 8 {
		paletteCount = binary.LittleEndian.Uint32(dib[32:36])
		if paletteCount == 0 {
			paletteCount = 1 << bits
		}
		if paletteCount > 256 {
			return bmpDecoded{}, malformed("BMP", "invalid palette size")
		}
	}
	paletteStart := uint64(14) + dibSize
	paletteBytes := uint64(paletteCount) * 4
	if paletteStart+paletteBytes > uint64(len(data)) || offBits > uint64(len(data)) || offBits < paletteStart+paletteBytes {
		return bmpDecoded{}, malformed("BMP", "invalid pixel offset")
	}
	var palette [][3]byte
	if bits <= 8 {
		palette = make([][3]byte, paletteCount)
		for i := range palette {
			entry := data[paletteStart+uint64(i)*4 : paletteStart+uint64(i+1)*4]
			palette[i] = [3]byte{entry[2], entry[1], entry[0]}
		}
	}
	rowBytes := (uint64(width)*uint64(bits) + 31) / 32 * 4
	pixelBytes := rowBytes * uint64(height)
	if pixelBytes > uint64(len(data))-offBits {
		return bmpDecoded{}, malformed("BMP", "truncated pixel data")
	}
	if indexed {
		pixels := make([]byte, uint64(width)*uint64(height))
		for y := range int(height) {
			srcY := y
			if h > 0 {
				srcY = int(height) - 1 - y
			}
			row := data[offBits+uint64(srcY)*rowBytes : offBits+uint64(srcY+1)*rowBytes]
			for x := range int(width) {
				var index byte
				switch bits {
				case 8:
					index = row[x]
				case 4:
					index = row[x/2]
					if x&1 == 0 {
						index >>= 4
					} else {
						index &= 0x0f
					}
				case 1:
					index = (row[x/8] >> (7 - uint(x&7))) & 1
				}
				if int(index) >= len(palette) {
					return bmpDecoded{}, malformed("BMP", "palette index out of range")
				}
				pixels[y*int(width)+x] = index
			}
		}
		return bmpDecoded{width: width, height: height, pixels: pixels, palette: palette}, nil
	}
	rgba := make([]byte, uint64(width)*uint64(height)*4)
	for y := range int(height) {
		srcY := y
		if h > 0 {
			srcY = int(height) - 1 - y
		}
		row := data[offBits+uint64(srcY)*rowBytes : offBits+uint64(srcY+1)*rowBytes]
		for x := range int(width) {
			var r, g, b, a byte = 0, 0, 0, 255
			switch bits {
			case 1, 4, 8:
				idx := byte(0)
				if bits == 8 {
					idx = row[x]
				} else if bits == 4 {
					idx = row[x/2]
					if x&1 == 0 {
						idx >>= 4
					} else {
						idx &= 0x0f
					}
				} else {
					idx = (row[x/8] >> (7 - uint(x&7))) & 1
				}
				if int(idx) >= len(palette) {
					return bmpDecoded{}, malformed("BMP", "palette index out of range")
				}
				r, g, b = palette[idx][0], palette[idx][1], palette[idx][2]
			case 24:
				b, g, r = row[x*3], row[x*3+1], row[x*3+2]
			case 32:
				b, g, r, a = row[x*4], row[x*4+1], row[x*4+2], row[x*4+3]
			case 16:
				v := binary.LittleEndian.Uint16(row[x*2 : x*2+2])
				r, g, b = byte((v>>10)&31)<<3, byte((v>>5)&31)<<3, byte(v&31)<<3
			}
			o := (y*int(width) + x) * 4
			rgba[o], rgba[o+1], rgba[o+2], rgba[o+3] = r, g, b, a
		}
	}
	return bmpDecoded{width: width, height: height, rgba: rgba}, nil
}
