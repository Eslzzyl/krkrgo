package assets

import "fmt"

// DecodePSBRLE decodes a Kirikiri motion-image RL resource into straight-alpha
// RGBA pixels. Kirikiri's Windows motion resources use little-endian RGBA8;
// the byte order in the resource is BGRA, matching the native bitmap layout.
func DecodePSBRLE(data []byte, width, height int) (*Image, error) {
	raw, err := decodePSBRLEBytes(data, width, height, 4)
	if err != nil {
		return nil, err
	}
	pixels := make([]byte, 0, len(raw))
	for offset := 0; offset < len(raw); offset += 4 {
		pixels = append(pixels, raw[offset+2], raw[offset+1], raw[offset], raw[offset+3])
	}
	return &Image{Width: uint32(width), Height: uint32(height), RGBA: pixels}, nil
}

// DecodePSBRLEIndices decodes an RL resource containing one palette index per
// pixel. The caller expands the indices through the PSB palette.
func DecodePSBRLEIndices(data []byte, width, height int) ([]byte, error) {
	return decodePSBRLEBytes(data, width, height, 1)
}

func decodePSBRLEBytes(data []byte, width, height, align int) ([]byte, error) {
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("invalid PSB image dimensions %dx%d", width, height)
	}
	if align <= 0 {
		return nil, fmt.Errorf("invalid PSB RL alignment %d", align)
	}
	pixelCount := int64(width) * int64(height)
	if pixelCount > int64(int(^uint(0)>>1))/int64(align) {
		return nil, fmt.Errorf("PSB image dimensions overflow")
	}
	want := int(pixelCount) * align
	pixels := make([]byte, 0, want)
	for offset := 0; offset < len(data); {
		command := data[offset]
		offset++
		if command&0x80 != 0 {
			count := int(command&0x7f) + 3
			if offset+align > len(data) {
				return nil, fmt.Errorf("truncated PSB RL repeat payload")
			}
			if count > (want-len(pixels))/align {
				return nil, fmt.Errorf("PSB RL output exceeds image size")
			}
			pixel := data[offset : offset+align]
			offset += align
			for i := 0; i < count; i++ {
				pixels = append(pixels, pixel...)
			}
			continue
		}
		count := (int(command) + 1) * align
		if count > want-len(pixels) || offset+count > len(data) {
			return nil, fmt.Errorf("truncated PSB RL literal payload")
		}
		pixels = append(pixels, data[offset:offset+count]...)
		offset += count
	}
	if len(pixels) != want {
		return nil, fmt.Errorf("PSB RL output size %d, want %d", len(pixels), want)
	}
	return pixels, nil
}
