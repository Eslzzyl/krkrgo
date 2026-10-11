//go:build goexperiment.simd

package tjs

import (
	"strconv"
	"testing"

	"simd"
)

// compareUTF16SIMD is a benchmark-only prototype. It preserves lexicographic
// UTF-16 code-unit ordering and falls back to the scalar tail.
func compareUTF16SIMD(a, b UTF16) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	lanes := simd.BroadcastUint16s(0).Len()
	var maskLanes [64]int16
	for i := 0; i+lanes <= n; i += lanes {
		mask := simd.LoadUint16s(a[i : i+lanes]).Equal(simd.LoadUint16s(b[i : i+lanes]))
		mask.ToInt16s().Store(maskLanes[:lanes])
		for lane, equal := range maskLanes[:lanes] {
			if equal == 0 {
				if a[i+lane] < b[i+lane] {
					return -1
				}
				return 1
			}
		}
	}
	for i := n - n%lanes; i < n; i++ {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	if len(a) < len(b) {
		return -1
	}
	if len(a) > len(b) {
		return 1
	}
	return 0
}

func TestCompareUTF16SIMDDifferential(t *testing.T) {
	for _, size := range []int{0, 1, 7, 8, 9, 31, 32, 33, 1024} {
		a, b := benchmarkUTF16Pair(size)
		for _, mutate := range []func(UTF16){
			func(UTF16) {},
			func(s UTF16) {
				if len(s) > 0 {
					s[0]++
				}
			},
			func(s UTF16) {
				if len(s) > 0 {
					s[len(s)-1]++
				}
			},
		} {
			candidate := append(UTF16(nil), b...)
			mutate(candidate)
			if got, want := compareUTF16SIMD(a, candidate), compareUTF16(a, candidate); got != want {
				t.Fatalf("size %d: SIMD compare = %d, scalar = %d", size, got, want)
			}
		}
		longer := append(append(UTF16(nil), a...), 'z')
		if got, want := compareUTF16SIMD(a, longer), compareUTF16(a, longer); got != want {
			t.Fatalf("size %d prefix: SIMD compare = %d, scalar = %d", size, got, want)
		}
	}
}

func BenchmarkCompareUTF16SIMD(b *testing.B) {
	for _, size := range []int{32, 1024, 65536} {
		a, same := benchmarkUTF16Pair(size)
		lastDiff := append(UTF16(nil), same...)
		lastDiff[len(lastDiff)-1]++
		b.Run("equal/"+strconv.Itoa(size), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				benchmarkCompareSink = compareUTF16SIMD(a, same)
			}
		})
		b.Run("last-different/"+strconv.Itoa(size), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				benchmarkCompareSink = compareUTF16SIMD(a, lastDiff)
			}
		})
	}
}
