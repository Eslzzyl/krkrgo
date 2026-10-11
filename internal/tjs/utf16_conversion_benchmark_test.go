package tjs

import (
	"testing"
	"unicode/utf16"
)

var benchmarkUTF16Sink UTF16
var benchmarkUTF16StringSink string

func benchmarkUTF16Strings() []struct {
	name string
	s    string
} {
	return []struct {
		name string
		s    string
	}{
		{"ascii-short", "the quick brown fox"},
		{"ascii-long", string(make([]byte, 4096))},
		{"japanese", "こんにちは世界"},
		{"emoji", "a😀b🌍c"},
		{"mixed", "ASCII-日本語-😀-text"},
		{"invalid-utf8", string([]byte{'a', 0xff, 'b', 0xe2, 0x82})},
	}
}

func BenchmarkUTF16FromString(b *testing.B) {
	for _, test := range benchmarkUTF16Strings() {
		b.Run(test.name+"/stdlib", func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				benchmarkUTF16Sink = UTF16(utf16.Encode([]rune(test.s)))
			}
		})
		b.Run(test.name+"/current", func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				benchmarkUTF16Sink = UTF16FromString(test.s)
			}
		})
	}
}

func BenchmarkUTF16ToString(b *testing.B) {
	for _, test := range benchmarkUTF16Strings() {
		units := utf16.Encode([]rune(test.s))
		b.Run(test.name+"/stdlib", func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				benchmarkUTF16StringSink = string(utf16.Decode(units))
			}
		})
		b.Run(test.name+"/String", func(b *testing.B) {
			value := UTF16(units)
			b.ReportAllocs()
			for range b.N {
				benchmarkUTF16StringSink = value.String()
			}
		})
		b.Run(test.name+"/UTF16ToString", func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				benchmarkUTF16StringSink = UTF16ToString(units)
			}
		})
	}
}
