package tjs

import (
	"unicode/utf16"
)

// UTF16 is the storage type used by TJS strings. It deliberately permits
// isolated surrogate code units, which Go's rune strings cannot represent.
type UTF16 []uint16

func NewUTF16(units []uint16) UTF16  { return append(UTF16(nil), units...) }
func UTF16FromString(s string) UTF16 { return UTF16(utf16.Encode([]rune(s))) }
func (s UTF16) Units() []uint16      { return append([]uint16(nil), s...) }
func (s UTF16) Len() int             { return len(s) }
func (s UTF16) String() string       { return string(utf16.Decode([]uint16(s))) }
func (s UTF16) At(index int) (uint16, bool) {
	if index < 0 || index >= len(s) {
		return 0, false
	}
	return s[index], true
}
func (s UTF16) Slice(start, end int) UTF16 {
	if start < 0 {
		start = 0
	}
	if end < start {
		end = start
	}
	if start > len(s) {
		start = len(s)
	}
	if end > len(s) {
		end = len(s)
	}
	return NewUTF16(s[start:end])
}
func (s UTF16) Equal(other UTF16) bool              { return equalUTF16(s, other) }
func (s UTF16) Compare(other UTF16) int             { return compareUTF16(s, other) }
func UTF16Len(s string) int                         { return len(UTF16FromString(s)) }
func UTF16Index(s string, index int) (uint16, bool) { return UTF16FromString(s).At(index) }
func UTF16Slice(s string, start, end int) string {
	return UTF16FromString(s).Slice(start, end).String()
}
func UTF16ToString(units []uint16) string { return UTF16(units).String() }
func StringToUTF16(s string) []uint16     { return UTF16FromString(s).Units() }
func FromUTF16(units []uint16) UTF16      { return NewUTF16(units) }
