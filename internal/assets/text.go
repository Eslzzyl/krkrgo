package assets

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/transform"
)

const maxTextUnits = 32 << 20
const maxTextBytes = 64 << 20

// Decode converts a Kirikiri text stream to a Go string. Unlike DecodeUnits,
// it rejects isolated UTF-16 surrogate code units.
func Decode(data []byte) (string, error) {
	units, err := DecodeUnits(data)
	if err != nil {
		return "", err
	}
	for i := 0; i < len(units); {
		u := units[i]
		if u >= 0xd800 && u <= 0xdbff {
			if i+1 >= len(units) || units[i+1] < 0xdc00 || units[i+1] > 0xdfff {
				return "", fmt.Errorf("invalid UTF-16 surrogate at code unit %d", i)
			}
			i += 2
		} else if u >= 0xdc00 && u <= 0xdfff {
			return "", fmt.Errorf("invalid UTF-16 surrogate at code unit %d", i)
		} else {
			i++
		}
	}
	return string(utf16.Decode(units)), nil
}

// DecodeUnits decodes a Kirikiri native text stream while retaining every
// UTF-16 code unit, including isolated surrogates.
func DecodeUnits(data []byte) ([]uint16, error) {
	if len(data) >= 2 && data[0] == 0xfe && data[1] == 0xfe {
		if len(data) < 5 {
			return nil, errors.New("truncated Kirikiri text header")
		}
		mode := data[2]
		if mode > 2 {
			return nil, fmt.Errorf("unsupported Kirikiri text cipher mode %d", mode)
		}
		if data[3] != 0xff || data[4] != 0xfe {
			return nil, errors.New("missing UTF-16 BOM in Kirikiri text stream")
		}
		payload := data[5:]
		var raw []byte
		var err error
		if mode == 2 {
			if len(payload) < 16 {
				return nil, errors.New("truncated compressed text header")
			}
			packed64 := binary.LittleEndian.Uint64(payload)
			raw64 := binary.LittleEndian.Uint64(payload[8:])
			if raw64 > maxTextBytes {
				return nil, fmt.Errorf("decompressed text exceeds %d bytes", maxTextBytes)
			}
			if packed64 > uint64(len(payload)-16) {
				return nil, errors.New("truncated compressed text")
			}
			if packed64 > uint64(maxInt()) {
				return nil, errors.New("compressed text is too large")
			}
			packed := payload[16 : 16+int(packed64)]
			if len(payload) != 16+int(packed64) {
				return nil, errors.New("trailing compressed text bytes")
			}
			raw, err = inflateText(packed, int(raw64))
			if err != nil {
				return nil, err
			}
		} else {
			raw = payload
		}
		if len(raw)%2 != 0 {
			return nil, errors.New("odd UTF-16 text length")
		}
		units := make([]uint16, len(raw)/2)
		for i := range units {
			n := binary.LittleEndian.Uint16(raw[i*2:])
			switch mode {
			case 0:
				if n >= 0x20 {
					n ^= ((n & 0xfe) << 8) ^ 1
				}
			case 1:
				n = ((n & 0xaaaa) >> 1) | ((n & 0x5555) << 1)
			}
			units[i] = n
		}
		return units, nil
	}
	if len(data) >= 2 && (data[0] == 0xff && data[1] == 0xfe || data[0] == 0xfe && data[1] == 0xff) {
		if len(data)%2 != 0 {
			return nil, errors.New("odd UTF-16 text length")
		}
		units := make([]uint16, (len(data)-2)/2)
		le := data[0] == 0xff
		for i := range units {
			p := data[2+i*2:]
			if le {
				units[i] = binary.LittleEndian.Uint16(p)
			} else {
				units[i] = binary.BigEndian.Uint16(p)
			}
		}
		return units, nil
	}
	if bytes.HasPrefix(data, []byte{0xef, 0xbb, 0xbf}) {
		data = data[3:]
		if !utf8.Valid(data) {
			return nil, errors.New("invalid UTF-8 source text")
		}
		return utf16.Encode([]rune(string(data))), nil
	}
	if utf8.Valid(data) {
		return utf16.Encode([]rune(string(data))), nil
	}
	return decodeShiftJIS(data)
}

func inflateText(packed []byte, expected int) ([]byte, error) {
	source := bytes.NewReader(packed)
	r, err := zlib.NewReader(source)
	if err != nil {
		return nil, fmt.Errorf("invalid compressed text: %w", err)
	}
	defer r.Close()
	out, err := io.ReadAll(io.LimitReader(r, int64(expected)+1))
	if err != nil {
		return nil, fmt.Errorf("decompress text: %w", err)
	}
	if len(out) != expected {
		return nil, fmt.Errorf("decompressed size mismatch: expected %d, got %d", expected, len(out))
	}
	var extra [1]byte
	n, readErr := r.Read(extra[:])
	if n != 0 {
		return nil, fmt.Errorf("decompressed size mismatch: expected %d, got more", expected)
	}
	if readErr != nil && readErr != io.EOF {
		return nil, fmt.Errorf("decompress text: %w", readErr)
	}
	if source.Len() != 0 {
		return nil, errors.New("trailing compressed data")
	}
	return out, nil
}

// EncodeUnits emits the native UTF-16LE stream. mode is empty, c/c1/c2, or
// z/z0..z9, with the same single-digit option parsing as Kirikiri.
func EncodeUnits(units []uint16, mode string) ([]byte, error) {
	if len(units) > maxTextUnits {
		return nil, errors.New("text stream exceeds 64 MiB limit")
	}
	cipher, level := 0, 6
	for i := 0; i < len(mode); i++ {
		switch mode[i] {
		case 'c':
			n := 1
			if i+1 < len(mode) && mode[i+1] >= '0' && mode[i+1] <= '9' {
				n = int(mode[i+1] - '0')
				i++
			}
			if n != 1 && n != 2 {
				return nil, errors.New("unsupported text cipher mode")
			}
			cipher = n
		case 'z':
			cipher = 2
			if i+1 < len(mode) && mode[i+1] >= '0' && mode[i+1] <= '9' {
				level = int(mode[i+1] - '0')
				i++
			}
		default:
			return nil, fmt.Errorf("unsupported text write mode: %s", mode)
		}
	}
	prefixLen := 2
	if cipher != 0 {
		prefixLen = 5
	}
	raw := make([]byte, len(units)*2)
	for i, n := range units {
		if cipher == 1 {
			n = ((n & 0xaaaa) >> 1) | ((n & 0x5555) << 1)
		}
		binary.LittleEndian.PutUint16(raw[i*2:], n)
	}
	out := make([]byte, prefixLen, prefixLen+len(raw)+16)
	if cipher != 0 {
		copy(out, []byte{0xfe, 0xfe, byte(cipher), 0xff, 0xfe})
	} else {
		copy(out, []byte{0xff, 0xfe})
	}
	if cipher == 2 {
		var packed bytes.Buffer
		zw, err := zlib.NewWriterLevel(&packed, level)
		if err != nil {
			return nil, fmt.Errorf("compress text: %w", err)
		}
		if _, err = zw.Write(raw); err != nil {
			return nil, fmt.Errorf("compress text: %w", err)
		}
		if err = zw.Close(); err != nil {
			return nil, fmt.Errorf("compress text: %w", err)
		}
		var lengths [16]byte
		binary.LittleEndian.PutUint64(lengths[0:], uint64(packed.Len()))
		binary.LittleEndian.PutUint64(lengths[8:], uint64(len(raw)))
		out = append(out, lengths[:]...)
		out = append(out, packed.Bytes()...)
	} else {
		out = append(out, raw...)
	}
	return out, nil
}

func maxInt() int { return int(^uint(0) >> 1) }

func decodeShiftJIS(data []byte) ([]uint16, error) {
	for i := 0; i < len(data); i++ {
		b := data[i]
		switch {
		case b <= 0x7f || b >= 0xa1 && b <= 0xdf:
			// ASCII and half-width katakana are single-byte characters.
		case b >= 0x81 && b <= 0x9f || b >= 0xe0 && b <= 0xfc:
			if i+1 >= len(data) {
				return nil, fmt.Errorf("invalid Shift-JIS source text at byte %d", i)
			}
			trail := data[i+1]
			if trail < 0x40 || trail > 0xfc || trail == 0x7f {
				return nil, fmt.Errorf("invalid Shift-JIS source text at byte %d", i)
			}
			i++
		default:
			return nil, fmt.Errorf("invalid Shift-JIS source text at byte %d", i)
		}
	}

	decoded, _, err := transform.Bytes(japanese.ShiftJIS.NewDecoder(), data)
	if err != nil {
		return nil, fmt.Errorf("invalid Shift-JIS source text: %w", err)
	}
	for _, r := range string(decoded) {
		if r == utf8.RuneError {
			return nil, errors.New("invalid Shift-JIS source text: unmapped character")
		}
	}
	return utf16.Encode([]rune(string(decoded))), nil
}
