package tjs

import "testing"

var benchmarkCallValue Value

type benchmarkCallHost struct{}

func (benchmarkCallHost) Call(_ *VM, _ string, args []Value, _ *uint64) (Value, error) {
	if len(args) != 0 {
		return args[0], nil
	}
	return IntegerValue(1), nil
}

func BenchmarkVMCallPaths(b *testing.B) {
	paths := []string{"native", "script", "method", "closure"}
	forms := []string{"none", "fixed", "forward", "rest", "spread"}
	for _, path := range paths {
		for _, form := range forms {
			b.Run(path+"/"+form, func(b *testing.B) {
				setup, call := callBenchmarkSources(path, form)
				setupProgram, err := compile("call-benchmark-setup.tjs", setup)
				if err != nil {
					b.Fatal(err)
				}
				program, err := compile("call-benchmark.tjs", "return "+call+";")
				if err != nil {
					b.Fatal(err)
				}
				vm := NewVM()
				if path == "native" {
					if err := vm.RegisterNative("bench"); err != nil {
						b.Fatal(err)
					}
				}
				host := benchmarkCallHost{}
				budget := uint64(1 << 62)
				if _, err := vm.Execute(&setupProgram, host, &budget); err != nil {
					b.Fatal(err)
				}
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					value, err := vm.Execute(&program, host, &budget)
					if err != nil {
						b.Fatal(err)
					}
					benchmarkCallValue = value
				}
			})
		}
	}
}

func callBenchmarkSources(path, form string) (setup, call string) {
	switch path {
	case "script":
		setup = "function target(a,b,r*){return a;}"
	case "method":
		setup = "class C { function target(a,b,r*){return a;} } var c=new C();"
	case "closure":
		setup = "var target=(function(a,b,r*){return a;});"
	}
	target := "bench"
	if path != "native" {
		target = "target"
	}
	if path == "method" {
		target = "c.target"
	}
	args := ""
	switch form {
	case "fixed":
		args = "1,2"
	case "forward":
		setup += " function forward(x,y){return " + target + "(*); }"
		target = "forward"
		args = "1,2"
	case "rest":
		args = "1,2,3,4"
	case "spread":
		setup += " var xs=[1,2];"
		args = "xs*"
	}
	return setup, target + "(" + args + ")"
}
