package tjs

import (
	"reflect"
	"testing"
	"unicode/utf16"
)

func TestUTF16FromStringMatchesStandardLibrary(t *testing.T) {
	cases := []struct {
		name string
		s    string
	}{
		{"empty", ""},
		{"ascii-and-nul", "ASCII\x00text"},
		{"bmp", "こんにちはé漢"},
		{"surrogate-pair", "a😀🌍z"},
		{"mixed", "ASCII-日本語-😀-text"},
		{"invalid-utf8", string([]byte{'a', 0xff, 'b', 0xe2, 0x82})},
		{"invalid-utf8-surrogate", string([]byte{0xed, 0xa0, 0x80})},
		{"long", string(make([]byte, 4096))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want := utf16.Encode([]rune(tc.s))
			if got := []uint16(UTF16FromString(tc.s)); !reflect.DeepEqual(got, want) {
				t.Fatalf("UTF16FromString code units mismatch: got %04x, want %04x", got, want)
			}
		})
	}
}

func TestUTF16ToStringMatchesStandardLibrary(t *testing.T) {
	cases := []struct {
		name  string
		units []uint16
	}{
		{"empty", nil},
		{"ascii-and-nul", []uint16{'A', 0, 'Z'}},
		{"bmp", []uint16{'こ', '漢', 0x00e9}},
		{"surrogate-pair", []uint16{'a', 0xd83d, 0xde00, 'z'}},
		{"isolated-high-surrogate", []uint16{'a', 0xd800, 'z'}},
		{"isolated-low-surrogate", []uint16{0xdc00}},
		{"long", make([]uint16, 4096)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want := string(utf16.Decode(tc.units))
			if got := UTF16ToString(tc.units); got != want {
				t.Fatalf("UTF16ToString mismatch: got %q, want %q", got, want)
			}
			if got := UTF16(tc.units).String(); got != want {
				t.Fatalf("UTF16.String mismatch: got %q, want %q", got, want)
			}
		})
	}
}
