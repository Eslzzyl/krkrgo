package tjs

import (
	"strings"
	"testing"
)

type benchmarkTraceHost struct{ traces uint64 }

func (h *benchmarkTraceHost) Call(_ *VM, _ string, _ []Value, _ *uint64) (Value, error) {
	return VoidValue, nil
}

func (h *benchmarkTraceHost) Trace(_ *Instruction) error {
	h.traces++
	return nil
}

var benchmarkVMValue Value

func BenchmarkVMHostTraceCheck(b *testing.B) {
	program, err := compile("dispatch-benchmark.tjs", `
		var i = 0;
		var sum = 0;
		while (i < 100) {
			sum += i;
			i++;
		}
		return sum;
	`)
	if err != nil {
		b.Fatal(err)
	}
	vm := NewVM()
	b.Run("without-trace", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			budget := uint64(100000)
			value, err := vm.Execute(&program, vmTestHost{}, &budget)
			if err != nil {
				b.Fatal(err)
			}
			benchmarkVMValue = value
		}
	})
	b.Run("with-trace", func(b *testing.B) {
		host := &benchmarkTraceHost{}
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			budget := uint64(100000)
			value, err := vm.Execute(&program, host, &budget)
			if err != nil {
				b.Fatal(err)
			}
			benchmarkVMValue = value
		}
	})
}

// BenchmarkVMRunPhases keeps compilation, one-time VM setup, and repeated
// execution separate so a per-run instruction preparation cost cannot hide in
// the compilation measurement.
func BenchmarkVMRunPhases(b *testing.B) {
	const source = `
		var i = 0;
		var sum = 0;
		while (i < 100) {
			sum += i;
			i++;
		}
		return sum;
	`
	program, err := compile("dispatch-benchmark.tjs", source)
	if err != nil {
		b.Fatal(err)
	}

	b.Run("compile-only", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			compiled, err := compile("dispatch-benchmark.tjs", source)
			if err != nil {
				b.Fatal(err)
			}
			benchmarkVMValue = IntegerValue(int64(len(compiled.Code)))
		}
	})
	b.Run("compile-and-first-run", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			compiled, err := compile("dispatch-benchmark.tjs", source)
			if err != nil {
				b.Fatal(err)
			}
			vm := NewVM()
			budget := uint64(100000)
			value, err := vm.Execute(&compiled, vmTestHost{}, &budget)
			if err != nil {
				b.Fatal(err)
			}
			benchmarkVMValue = value
		}
	})
	b.Run("first-run", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			vm := NewVM()
			budget := uint64(100000)
			value, err := vm.Execute(&program, vmTestHost{}, &budget)
			if err != nil {
				b.Fatal(err)
			}
			benchmarkVMValue = value
		}
	})
	b.Run("repeated-run", func(b *testing.B) {
		vm := NewVM()
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			budget := uint64(100000)
			value, err := vm.Execute(&program, vmTestHost{}, &budget)
			if err != nil {
				b.Fatal(err)
			}
			benchmarkVMValue = value
		}
	})
	b.Run("repeated-run-with-trace", func(b *testing.B) {
		vm := NewVM()
		host := &benchmarkTraceHost{}
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			budget := uint64(100000)
			value, err := vm.Execute(&program, host, &budget)
			if err != nil {
				b.Fatal(err)
			}
			benchmarkVMValue = value
		}
	})
	b.Run("budget-exhaustion-with-trace-and-source", func(b *testing.B) {
		vm := NewVM()
		host := &benchmarkTraceHost{}
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			budget := uint64(2)
			_, err := vm.Execute(&program, host, &budget)
			if err == nil || !strings.Contains(err.Error(), "execution budget exhausted") || !strings.Contains(err.Error(), "dispatch-benchmark.tjs") {
				b.Fatalf("budget exhaustion error = %v", err)
			}
		}
	})
}
