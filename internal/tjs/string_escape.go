package tjs

import (
	"fmt"
	"strings"
)

// EscapeString quotes a string using the escape syntax accepted by TJS string
// literals.  Non-ASCII characters are retained; UTF-16 code units which cannot
// be represented as ordinary text are written as \\uXXXX escapes.
func EscapeString(s string) string { return EscapeUTF16(UTF16FromString(s)) }

// EscapeUTF16 is EscapeString's UTF-16 preserving form.  In particular, an
// isolated surrogate is never silently converted to U+FFFD.
func EscapeUTF16(s []uint16) string {
	var b strings.Builder
	hex := false
	for i := 0; i < len(s); i++ {
		u := s[i]
		if u >= 0xd800 && u <= 0xdbff && i+1 < len(s) && s[i+1] >= 0xdc00 && s[i+1] <= 0xdfff {
			b.WriteString(NewUTF16(s[i : i+2]).String())
			i++
			hex = false
			continue
		}
		switch u {
		case '\\':
			b.WriteString(`\\`)
			hex = false
		case '"':
			b.WriteString(`\"`)
			hex = false
		case '\'':
			b.WriteString(`\'`)
			hex = false
		case '\a':
			b.WriteString(`\a`)
			hex = false
		case '\b':
			b.WriteString(`\b`)
			hex = false
		case '\f':
			b.WriteString(`\f`)
			hex = false
		case '\n':
			b.WriteString(`\n`)
			hex = false
		case '\r':
			b.WriteString(`\r`)
			hex = false
		case '\t':
			b.WriteString(`\t`)
			hex = false
		case '\v':
			b.WriteString(`\v`)
			hex = false
		default:
			if (hex && isHexUnit(u)) || u < 0x20 {
				fmt.Fprintf(&b, `\x%02x`, u)
				hex = true
			} else if u >= 0xd800 && u <= 0xdfff {
				fmt.Fprintf(&b, `\u%04X`, u)
				hex = false
			} else {
				b.WriteString(UTF16{u}.String())
				hex = false
			}
		}
	}
	return b.String()
}

func isHexUnit(u uint16) bool {
	return u >= '0' && u <= '9' || u >= 'a' && u <= 'f' || u >= 'A' && u <= 'F'
}

// UnescapeUTF16 parses TJS's string-literal escapes.  It returns UTF-16 units
// so that escaped isolated surrogates remain observable.  Unknown escapes are
// accepted as the escaped character, matching tjsLex's compatibility rule.
func UnescapeUTF16(s string) ([]uint16, error) {
	in := UTF16FromString(s)
	out := make([]uint16, 0, len(in))
	for i := 0; i < len(in); i++ {
		if in[i] != '\\' {
			out = append(out, in[i])
			continue
		}
		if i+1 >= len(in) {
			return nil, fmt.Errorf("unterminated string escape")
		}
		i++
		c := in[i]
		switch c {
		case 'a':
			out = append(out, '\a')
		case 'b':
			out = append(out, '\b')
		case 'f':
			out = append(out, '\f')
		case 'n':
			out = append(out, '\n')
		case 'r':
			out = append(out, '\r')
		case 't':
			out = append(out, '\t')
		case 'v':
			out = append(out, '\v')
		case '\\', '\'', '"', '?':
			out = append(out, c)
		case '\r':
			if i+1 < len(in) && in[i+1] == '\n' {
				i++
			}
		case '\n': // escaped physical newline is a continuation
		case 'x', 'X':
			if i+1 >= len(in) || !isHexUnit(in[i+1]) {
				return nil, fmt.Errorf("invalid \\x escape")
			}
			var n uint16
			count := 0
			for count < 4 && i+1 < len(in) {
				d, ok := hex16(in[i+1])
				if !ok {
					break
				}
				n = n<<4 | d
				i++
				count++
			}
			out = append(out, n)
		case 'u':
			if i+4 >= len(in) {
				return nil, fmt.Errorf("short \\u escape")
			}
			var n uint16
			for j := 1; j <= 4; j++ {
				d, ok := hex16(in[i+j])
				if !ok {
					return nil, fmt.Errorf("invalid \\u escape")
				}
				n = n<<4 | d
			}
			out = append(out, n)
			i += 4
		case 'U':
			if i+8 >= len(in) {
				return nil, fmt.Errorf("short \\U escape")
			}
			var n uint32
			for j := 1; j <= 8; j++ {
				d, ok := hex16(in[i+j])
				if !ok {
					return nil, fmt.Errorf("invalid \\U escape")
				}
				n = n<<4 | uint32(d)
			}
			if n > 0x10ffff || (n >= 0xd800 && n <= 0xdfff) {
				return nil, fmt.Errorf("invalid Unicode code point U+%X", n)
			}
			if n <= 0xffff {
				out = append(out, uint16(n))
			} else {
				n -= 0x10000
				out = append(out, uint16(0xd800+n/0x400), uint16(0xdc00+n%0x400))
			}
			i += 8
		default:
			if c == '0' {
				n := uint16(0)
				for i+1 < len(in) && in[i+1] >= '0' && in[i+1] <= '7' {
					i++
					n = n*8 + uint16(in[i]-'0')
				}
				out = append(out, n)
			} else {
				out = append(out, c)
			}
		}
	}
	return out, nil
}

// UnescapeString is the Go-string convenience form of UnescapeUTF16.
func UnescapeString(s string) (string, error) {
	u, err := UnescapeUTF16(s)
	if err != nil {
		return "", err
	}
	return UTF16ToString(u), nil
}

// ParseStringEscape is an explicit alias useful to lexer callers.
func ParseStringEscape(s string) (UTF16, error) { u, err := UnescapeUTF16(s); return UTF16(u), err }

func hex16(c uint16) (uint16, bool) {
	if c >= '0' && c <= '9' {
		return c - '0', true
	}
	if c >= 'a' && c <= 'f' {
		return c - 'a' + 10, true
	}
	if c >= 'A' && c <= 'F' {
		return c - 'A' + 10, true
	}
	return 0, false
}

// QuoteString returns a quoted TJS literal, including its delimiters.
func QuoteString(s string) string { return `"` + EscapeString(s) + `"` }

// UnquoteString accepts either single- or double-quoted TJS strings.
func UnquoteString(s string) (string, error) {
	if len(s) < 2 || (s[0] != '\'' && s[0] != '"') || s[len(s)-1] != s[0] {
		return "", fmt.Errorf("invalid quoted string")
	}
	return UnescapeString(s[1 : len(s)-1])
}
func Escape(s string) string            { return EscapeString(s) }
func Unescape(s string) (string, error) { return UnescapeString(s) }
