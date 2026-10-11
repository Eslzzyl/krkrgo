package tjs

import (
	"math"
	"testing"
)

func TestValueUTF16AndCoercion(t *testing.T) {
	v := StringValue("a😀")
	if got := v.UTF16().Len(); got != 3 {
		t.Fatalf("UTF-16 length = %d, want 3", got)
	}
	isolated := StringUnits([]uint16{0xd800, 'x'})
	joined, err := isolated.Binary("+", StringValue("y"))
	if err != nil {
		t.Fatal(err)
	}
	if got := joined.UTF16().Units(); len(got) != 3 || got[0] != 0xd800 || got[1] != 'x' || got[2] != 'y' {
		t.Fatalf("joined UTF-16 = %#v", got)
	}
	if truth, err := StringValue("2tail").Truth(); err != nil || !truth {
		t.Fatalf("truth = %v, %v", truth, err)
	}
	if StringValue("2tail").Equal(IntegerValue(2)) {
		t.Fatal("numeric-looking string must retain text equality")
	}
}
func TestUTF16EqualityAndOrderingBoundaries(t *testing.T) {
	tests := []struct {
		name string
		a, b UTF16
		eq   bool
		cmp  int
	}{
		{"empty", nil, UTF16{}, true, 0},
		{"equal ascii", UTF16{'t', 'e', 's', 't'}, UTF16{'t', 'e', 's', 't'}, true, 0},
		{"first difference", UTF16{1, 'a'}, UTF16{2, 'a'}, false, -1},
		{"last difference", UTF16{'a', 0xd83d, 0xde00}, UTF16{'a', 0xd83d, 0xde01}, false, -1},
		{"short prefix", UTF16{'a', 'b'}, UTF16{'a', 'b', 0}, false, -1},
		{"long prefix", UTF16{'a', 'b', 0}, UTF16{'a', 'b'}, false, 1},
		{"isolated surrogate", UTF16{0xd800}, UTF16{0xd800}, true, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := equalUTF16(tt.a, tt.b); got != tt.eq {
				t.Errorf("equalUTF16(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.eq)
			}
			if got := compareUTF16(tt.a, tt.b); got != tt.cmp {
				t.Errorf("compareUTF16(%v, %v) = %d, want %d", tt.a, tt.b, got, tt.cmp)
			}
			if got := StringUnits(tt.a).StrictEqual(StringUnits(tt.b)); got != tt.eq {
				t.Errorf("StrictEqual() = %v, want %v", got, tt.eq)
			}
		})
	}
}

func TestValueNumericOperators(t *testing.T) {
	low, high := IntegerValue(9007199254740992), IntegerValue(9007199254740993)
	if low.Equal(high) {
		t.Fatal("integer equality lost low bits")
	}
	if got, _ := IntegerValue(3).Binary("/", IntegerValue(2)); got.Kind() != KindReal || got.RealValue() != 1.5 {
		t.Fatalf("division = %#v", got)
	}
	if _, err := IntegerValue(1).Binary("%", IntegerValue(0)); err == nil {
		t.Fatal("division by zero accepted")
	}
	if got, _ := RealValue(math.Copysign(0, -1)).Unary("string"); got.Text() != "-0.0" {
		t.Fatalf("signed zero = %q", got.Text())
	}
}

func TestObjectIdentityOrderingAndInvalidation(t *testing.T) {
	table := NewObjectTable()
	a := table.New()
	b := table.New()
	if a.ID == 0 || a.ID == b.ID {
		t.Fatal("object IDs are not stable/unique")
	}
	if ref, ok := table.Ref(a.ID); !ok || ref.Object != a.ID {
		t.Fatal("object reference lookup failed")
	}
	if err := a.Set("z", IntegerValue(1)); err != nil {
		t.Fatal(err)
	}
	if err := a.Set("a", IntegerValue(2)); err != nil {
		t.Fatal(err)
	}
	keys := a.Keys()
	if len(keys) != 2 || keys[0] != "a" || keys[1] != "z" {
		t.Fatalf("ordered keys = %#v", keys)
	}
	a.Invalidate()
	if a.IsValid() || a.IsMemberValid("a") {
		t.Fatal("invalidation did not propagate")
	}
	if _, ok := a.Get("a"); ok {
		t.Fatal("invalid object returned a property")
	}
	if _, ok := table.Ref(a.ID); ok {
		t.Fatal("invalid object returned a reference")
	}
}

func TestLiteralValidation(t *testing.T) {
	if err := DictionaryLiteral(DictEntry(ValueLiteral(StringValue("k")), ValueLiteral(IntegerValue(1)))).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := ValueLiteral(ObjectIDValue(1)).Validate(); err == nil {
		t.Fatal("non-null object literal accepted")
	}
}
