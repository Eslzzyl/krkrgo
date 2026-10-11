package tjs

import (
	"errors"
	"testing"
)

func TestVMStringSprintfConversionsAndFlags(t *testing.T) {
	got := executeVMSource(t, `
return "%s|%c|%04d|%#x|%u|%.2f|%e|%g|%%".sprintf("cat", "XYZ", -12, 42, -1, 1.25, 12.5, 12.0);
`)
	want := "cat|X|-012|0x2a|18446744073709551615|1.25|1.250000e+01|12|%"
	if got.Text() != want {
		t.Fatalf("sprintf = %q, want %q", got.Text(), want)
	}
}

func TestVMStringSprintfDynamicWidthPrecisionAndUTF16(t *testing.T) {
	got := executeVMSource(t, `
return "[%*.*s][%-4s][%.1s]".sprintf(5, 2, "😀z", "x", "😀z");
`)
	want := "[   😀][x   ][\uFFFD]"
	if got.Text() != want {
		t.Fatalf("sprintf = %q, want %q", got.Text(), want)
	}
	if got, err := formatTJSString(StringValue("%q"), []Value{IntegerValue(1)}); got.Kind() != KindVoid || !errors.Is(err, errInvalidFormatString) {
		t.Fatalf("invalid format result=%v error=%v, want invalid format", got, err)
	}
	got = executeVMSource(t, `return "%.1s".sprintf("😀z");`)
	if got.Text() != "�" {
		t.Fatalf("UTF-16 precision result = %q, want replacement for one surrogate code unit", got.Text())
	}
}

func TestVMStringSprintfErrors(t *testing.T) {
	vm := NewVM()
	for _, tc := range []struct {
		format string
		args   []Value
		want   error
	}{
		{"%d", nil, ErrBadParamCount},
		{"%q", []Value{IntegerValue(1)}, errInvalidFormatString},
		{"%", nil, errInvalidFormatString},
	} {
		if _, err := vm.callStringMethod(StringValue(tc.format), "sprintf", tc.args); !errors.Is(err, tc.want) {
			t.Errorf("sprintf(%q) error = %v, want %v", tc.format, err, tc.want)
		}
	}
}
