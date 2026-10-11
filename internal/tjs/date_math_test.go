package tjs

import (
	"math"
	"testing"
	"time"
)

func TestDateNumericAndTimezoneForms(t *testing.T) {
	d := NewDate(2024, 0, 2, 3, 4, 5)
	if !d.IsValid() || d.GetYear() != 2024 || d.GetMonth() != 0 || d.GetDate() != 2 || d.GetHours() != 3 || d.GetMinutes() != 4 || d.GetSeconds() != 5 {
		t.Fatalf("numeric date = %#v (%s)", d, d)
	}
	ms, err := ParseDateString("2024-01-02T03:04:05+09:00")
	if err != nil {
		t.Fatal(err)
	}
	if got := time.UnixMilli(ms).UTC(); got.Hour() != 18 || got.Day() != 1 {
		t.Fatalf("offset parse = %v", got)
	}
	if _, err := ParseDateString("not a date"); err != ErrCannotParseDate {
		t.Fatalf("parse error = %v", err)
	}
}

func TestDateFixedFormattingAndMutation(t *testing.T) {
	d := DateFromMilliseconds(time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC).UnixMilli())
	if d.Format() == "" || len(d.Format()) != len("0000-00-00 00:00:00") {
		t.Fatalf("format = %q", d.Format())
	}
	if err := d.SetMonth(1); err != nil {
		t.Fatal(err)
	}
	if d.GetMonth() != 1 {
		t.Fatalf("set month = %d", d.GetMonth())
	}
}

func TestMathSignedZeroAndXorshift(t *testing.T) {
	if !math.Signbit(TJSMath.Min(0, math.Copysign(0, -1))) {
		t.Fatal("min lost negative zero")
	}
	if math.Signbit(TJSMath.Max(0, math.Copysign(0, -1))) {
		t.Fatal("max selected negative zero")
	}
	a, b := NewXorshift(1), NewXorshift(1)
	for range 8 {
		if a.Uint32() != b.Uint32() {
			t.Fatal("xorshift not deterministic")
		}
	}
}

func TestRandomGeneratorReconstruction(t *testing.T) {
	a := NewRandomGenerator(uint64(1234))
	for range 13 {
		_ = a.Uint32()
	}
	state := a.Serialize()
	b := NewRandomGenerator(uint32(1))
	if err := b.Randomize(state); err != nil {
		t.Fatal(err)
	}
	for range 20 {
		if a.Uint32() != b.Uint32() {
			t.Fatal("reconstructed sequence differs")
		}
	}
}

func TestArraySortModes(t *testing.T) {
	values := []Value{StringValue("10"), StringValue("2"), StringValue("1")}
	if err := SortValues(values, "0", true, nil); err != nil {
		t.Fatal(err)
	}
	if values[0].Text() != "1" || values[2].Text() != "10" {
		t.Fatalf("numeric sort = %v", values)
	}
}
