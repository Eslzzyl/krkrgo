package tjs

import (
	"testing"
)

func benchmarkUTF16Pair(size int) (UTF16, UTF16) {
	a := make(UTF16, size)
	b := make(UTF16, size)
	for i := range a {
		a[i] = uint16('a' + i%23)
		b[i] = a[i]
	}
	return a, b
}

func BenchmarkCompareUTF16(b *testing.B) {
	for _, size := range []int{32, 1024, 65536} {
		a, same := benchmarkUTF16Pair(size)
		firstDiff := append(UTF16(nil), same...)
		firstDiff[0]++
		lastDiff := append(UTF16(nil), same...)
		lastDiff[len(lastDiff)-1]++
		longer := append(append(UTF16(nil), same...), 'x')
		for _, pair := range []struct {
			name string
			b    UTF16
		}{
			{"equal", same},
			{"first-different", firstDiff},
			{"last-different", lastDiff},
			{"length-prefix", longer},
		} {
			b.Run(pair.name+"/"+benchmarkSize(size), func(b *testing.B) {
				b.ReportAllocs()
				for range b.N {
					benchmarkCompareSink = compareUTF16(a, pair.b)
				}
			})
		}
	}
}

func BenchmarkEqualUTF16(b *testing.B) {
	for _, size := range []int{4, 32, 1024, 65536} {
		a, same := benchmarkUTF16Pair(size)
		firstDiff := append(UTF16(nil), same...)
		firstDiff[0]++
		lastDiff := append(UTF16(nil), same...)
		lastDiff[len(lastDiff)-1]++
		shorter := same[:len(same)-1]
		for _, pair := range []struct {
			name string
			b    UTF16
		}{
			{"equal", same},
			{"first-different", firstDiff},
			{"last-different", lastDiff},
			{"length-prefix", shorter},
		} {
			b.Run(pair.name+"/"+benchmarkSize(size), func(b *testing.B) {
				b.ReportAllocs()
				for range b.N {
					benchmarkEqualSink = equalUTF16(a, pair.b)
				}
			})
		}
	}
}
func BenchmarkEqualUTF16CompareBaseline(b *testing.B) {
	for _, size := range []int{4, 32, 1024, 65536} {
		a, same := benchmarkUTF16Pair(size)
		diff := append(UTF16(nil), same...)
		diff[len(diff)-1]++
		for _, pair := range []struct {
			name string
			b    UTF16
		}{{"equal", same}, {"last-different", diff}} {
			b.Run(pair.name+"/"+benchmarkSize(size), func(b *testing.B) {
				b.ReportAllocs()
				for range b.N {
					benchmarkEqualSink = compareUTF16(a, pair.b) == 0
				}
			})
		}
	}
}

func BenchmarkEqualUTF16Unicode(b *testing.B) {
	pairs := []struct {
		name string
		a, b UTF16
	}{
		{"japanese", UTF16FromString("こんにちは世界"), UTF16FromString("こんにちは世界")},
		{"surrogate-pair", UTF16FromString("a😀b"), UTF16FromString("a😀b")},
		{"isolated-surrogate", UTF16{0xd800, 'x'}, UTF16{0xd800, 'x'}},
	}
	for _, pair := range pairs {
		b.Run(pair.name, func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				benchmarkEqualSink = equalUTF16(pair.a, pair.b)
			}
		})
	}
}

var benchmarkEqualSink bool
var benchmarkCompareSink int

func benchmarkSize(size int) string {
	switch size {
	case 4:
		return "4"
	case 32:
		return "32"
	case 1024:
		return "1024"
	default:
		return "65536"
	}
}
