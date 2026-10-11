package assets

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// ImageMetadata extracts the image metadata keys used by Kirikiri. Unknown
// formats return an empty map, matching the engine's optional metadata path.
func ImageMetadata(data []byte) (map[string]string, error) {
	tags := make(map[string]string)
	if bytes.HasPrefix(data, []byte("TLG0.0\x00sds\x1a")) {
		return tlgMetadata(data, tags)
	}
	if bytes.HasPrefix(data, []byte(pngSignature)) {
		return pngMetadata(data, tags)
	}
	return tags, nil
}

// Metadata is an alias for ImageMetadata.
func Metadata(data []byte) (map[string]string, error) { return ImageMetadata(data) }

func tlgMetadata(data []byte, tags map[string]string) (map[string]string, error) {
	r := mediaReader{data: data[11:]}
	raw, err := r.u32()
	if err != nil {
		return nil, malformed("TLG metadata", err.Error())
	}
	if _, err := r.take(uint64(raw)); err != nil {
		return nil, malformed("TLG metadata", err.Error())
	}
	for !r.done() {
		kind, err := r.take(4)
		if err != nil {
			return nil, malformed("TLG metadata", err.Error())
		}
		size, err := r.u32()
		if err != nil {
			return nil, malformed("TLG metadata", err.Error())
		}
		body, err := r.take(uint64(size))
		if err != nil {
			return nil, malformed("TLG metadata", err.Error())
		}
		if string(kind) != "tags" {
			continue
		}
		for len(body) != 0 {
			key, rest, err := readTLGTagString(body)
			if err != nil {
				return nil, err
			}
			body = rest
			if len(body) == 0 || body[0] != '=' {
				return nil, malformed("TLG metadata", "invalid tag separator")
			}
			value, rest, err := readTLGTagString(body[1:])
			if err != nil {
				return nil, err
			}
			body = rest
			if len(body) == 0 || body[0] != ',' {
				return nil, malformed("TLG metadata", "invalid tag terminator")
			}
			body = body[1:]
			if len(tags) >= 4096 {
				return nil, malformed("TLG metadata", "metadata limit exceeded")
			}
			tags[key] = value
		}
	}
	return tags, nil
}

func readTLGTagString(data []byte) (string, []byte, error) {
	colon := bytes.IndexByte(data, ':')
	if colon < 0 {
		return "", nil, malformed("TLG metadata", "invalid tag length")
	}
	length, err := strconv.ParseUint(string(data[:colon]), 10, 64)
	if err != nil {
		return "", nil, malformed("TLG metadata", "invalid tag length")
	}
	if length > 1024*1024 || length > uint64(len(data)-colon-1) {
		return "", nil, malformed("TLG metadata", "tag length exceeds limit")
	}
	start := colon + 1
	end := start + int(length)
	if !utf8.Valid(data[start:end]) {
		return "", nil, malformed("TLG metadata", "tag is not UTF-8")
	}
	return string(data[start:end]), data[end:], nil
}

func pngMetadata(data []byte, tags map[string]string) (map[string]string, error) {
	if len(data) < 8 {
		return nil, malformed("PNG metadata", "truncated signature")
	}
	pos := 8
	for pos < len(data) {
		if len(data)-pos < 8 {
			return nil, malformed("PNG metadata", "truncated chunk header")
		}
		size := uint64(binary.BigEndian.Uint32(data[pos : pos+4]))
		if size > uint64(len(data)-pos-8) {
			return nil, malformed("PNG metadata", "truncated chunk")
		}
		kind := data[pos+4 : pos+8]
		body := data[pos+8 : pos+8+int(size)]
		pos += 12 + int(size)
		if (bytes.Equal(kind, []byte("oFFs")) || bytes.Equal(kind, []byte("pHYs")) || strings.EqualFold(string(kind), "vpAg")) && len(body) < 9 {
			return nil, malformed("PNG metadata", "truncated metadata chunk")
		}
		var xKey, yKey, unitKey string
		switch {
		case bytes.Equal(kind, []byte("oFFs")):
			xKey, yKey, unitKey = "offs_x", "offs_y", "offs_unit"
		case bytes.Equal(kind, []byte("pHYs")):
			xKey, yKey, unitKey = "reso_x", "reso_y", "reso_unit"
		case strings.EqualFold(string(kind), "vpAg"):
			xKey, yKey, unitKey = "vpag_w", "vpag_h", "vpag_unit"
		}
		if xKey != "" {
			tags[xKey] = fmt.Sprint(int32(binary.BigEndian.Uint32(body[:4])))
			tags[yKey] = fmt.Sprint(int32(binary.BigEndian.Uint32(body[4:8])))
			unit := "unknown"
			if bytes.Equal(kind, []byte("pHYs")) {
				if body[8] == 1 {
					unit = "meter"
				}
			} else {
				switch body[8] {
				case 0:
					unit = "pixel"
				case 1:
					unit = "micrometer"
				}
			}
			tags[unitKey] = unit
		}
		if bytes.Equal(kind, []byte("IEND")) {
			break
		}
	}
	return tags, nil
}

type mediaReader struct {
	data []byte
	pos  uint64
}

func (r *mediaReader) done() bool { return r.pos == uint64(len(r.data)) }

func (r *mediaReader) take(n uint64) ([]byte, error) {
	if n > uint64(len(r.data))-r.pos {
		return nil, fmt.Errorf("truncated input")
	}
	start, end := int(r.pos), int(r.pos+n)
	r.pos += n
	return r.data[start:end], nil
}

func (r *mediaReader) u32() (uint32, error) {
	data, err := r.take(4)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(data), nil
}
