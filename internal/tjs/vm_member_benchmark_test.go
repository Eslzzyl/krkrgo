package tjs

import "testing"

var benchmarkMemberValue Value

func BenchmarkVMMemberAccess(b *testing.B) {
	vm := NewVM()
	object, err := vm.NewDictionary()
	if err != nil {
		b.Fatal(err)
	}
	id := object.ObjectRef().Object
	key := StringValue("member")
	if err := vm.setMember(id, key, IntegerValue(1)); err != nil {
		b.Fatal(err)
	}
	vm.Globals["member"] = IntegerValue(1)

	classProgram, err := compile("member-benchmark.tjs", `
class Base { function inherited() { return 1; } }
class Child extends Base {}
var instance = new Child();
return instance;
`)
	if err != nil {
		b.Fatal(err)
	}
	classVM := NewVM()
	budget := uint64(100000)
	instance, err := classVM.Execute(&classProgram, vmTestHost{}, &budget)
	if err != nil {
		b.Fatal(err)
	}
	classID := instance.ObjectRef().Object
	inheritedKey := StringValue("inherited")

	b.Run("ordinary-hit", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			value, _, err := vm.getMember(id, key)
			if err != nil {
				b.Fatal(err)
			}
			benchmarkMemberValue = value
		}
	})
	b.Run("ordinary-miss", func(b *testing.B) {
		miss := StringValue("missing")
		b.ReportAllocs()
		for range b.N {
			_, ok, err := vm.getMember(id, miss)
			if err != nil || ok {
				b.Fatalf("miss lookup = (%v, %v)", ok, err)
			}
		}
	})
	b.Run("global-hit", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			value, _, err := vm.getMember(0, key)
			if err != nil {
				b.Fatal(err)
			}
			benchmarkMemberValue = value
		}
	})
	b.Run("class-base-hit", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			value, _, err := classVM.getMember(classID, inheritedKey)
			if err != nil {
				b.Fatal(err)
			}
			benchmarkMemberValue = value
		}
	})
	b.Run("ordinary-name-hit", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			value, _, err := vm.getMemberString(id, "member")
			if err != nil {
				b.Fatal(err)
			}
			benchmarkMemberValue = value
		}
	})
	b.Run("ordinary-name-miss", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			_, ok, err := vm.getMemberString(id, "missing")
			if err != nil || ok {
				b.Fatalf("miss lookup = (%v, %v)", ok, err)
			}
		}
	})
	b.Run("global-name-hit", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			value, _, err := vm.getMemberString(0, "member")
			if err != nil {
				b.Fatal(err)
			}
			benchmarkMemberValue = value
		}
	})
	b.Run("class-base-name-hit", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			value, _, err := classVM.getMemberString(classID, "inherited")
			if err != nil {
				b.Fatal(err)
			}
			benchmarkMemberValue = value
		}
	})
	b.Run("name-update", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if err := vm.setMemberString(id, "member", IntegerValue(2)); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("dynamic-add-delete", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if err := vm.setMember(id, key, IntegerValue(2)); err != nil {
				b.Fatal(err)
			}
			if _, err := vm.deleteMember(id, key); err != nil {
				b.Fatal(err)
			}
		}
	})
}
func BenchmarkVMHandle(b *testing.B) {
	vm := NewVM()
	object, err := vm.NewDictionary()
	if err != nil {
		b.Fatal(err)
	}
	values := []struct {
		name  string
		value Value
	}{
		{name: "non-object", value: IntegerValue(1)},
		{name: "null", value: NullValue},
		{name: "valid-object", value: object},
	}
	for _, tc := range values {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				id, err := vm.handle(tc.value)
				if tc.name == "valid-object" {
					if err != nil || id != object.ObjectRef().Object {
						b.Fatalf("handle = (%d, %v)", id, err)
					}
				} else if err == nil {
					b.Fatal("handle unexpectedly succeeded")
				}
			}
		})
	}
}

func BenchmarkVMPropertyAccess(b *testing.B) {
	const source = `
var backing = 1;
property value { getter() { return backing; } setter(v) { backing = v; } }
value = value + 1;
return value;
`
	program, err := compile("property-member-benchmark.tjs", source)
	if err != nil {
		b.Fatal(err)
	}
	vm := NewVM()
	b.ReportAllocs()
	for range b.N {
		budget := uint64(100000)
		value, err := vm.Execute(&program, vmTestHost{}, &budget)
		if err != nil {
			b.Fatal(err)
		}
		benchmarkMemberValue = value
	}
}
func BenchmarkVMPropertyNameAccess(b *testing.B) {
	for _, tc := range []struct {
		name   string
		source string
	}{
		{name: "getter", source: `var backing=1; property value { getter() { return backing; } setter(v) { backing=v; } } return value;`},
		{name: "setter", source: `var backing=1; property value { getter() { return backing; } setter(v) { backing=v; } } value=2; return backing;`},
	} {
		b.Run(tc.name, func(b *testing.B) {
			program, err := compile("property-name-benchmark.tjs", tc.source)
			if err != nil {
				b.Fatal(err)
			}
			vm := NewVM()
			b.ReportAllocs()
			for range b.N {
				budget := uint64(100000)
				value, err := vm.Execute(&program, vmTestHost{}, &budget)
				if err != nil {
					b.Fatal(err)
				}
				benchmarkMemberValue = value
			}
		})
	}
}
