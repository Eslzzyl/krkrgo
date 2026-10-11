package kag

import "unicode"

func utf16Width(r rune) int {
	if r > 0xffff {
		return 2
	}
	return 1
}

func isKAGSpace(r rune) bool { return unicode.IsSpace(r) }

// asciiLower matches Rust's str::to_ascii_lowercase rather than applying
// Unicode case folding to command and attribute names.
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

// scenarioLines follows Rust str::lines: CR, LF, and CRLF terminate lines;
// a final terminator does not create an extra empty line.
func scenarioLines(source string) []string {
	lines := make([]string, 0, 16)
	start := 0
	for i := 0; i < len(source); i++ {
		if source[i] != '\r' && source[i] != '\n' {
			continue
		}
		lines = append(lines, source[start:i])
		if source[i] == '\r' && i+1 < len(source) && source[i+1] == '\n' {
			i++
		}
		start = i + 1
	}
	if start < len(source) {
		lines = append(lines, source[start:])
	}
	return lines
}
