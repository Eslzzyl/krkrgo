package tjs

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

type vmNativeErrorHost struct{ err error }

func (h vmNativeErrorHost) Call(_ *VM, _ string, _ []Value, _ *uint64) (Value, error) {
	return VoidValue, h.err
}

func TestVMNativeHostErrorsBecomeCatchableExceptions(t *testing.T) {
	program, err := compile("native-error.tjs", `try { failNative(); } catch(e) { return [e.message,e.trace!==void].join('|'); } return "miss";`)
	if err != nil {
		t.Fatal(err)
	}
	vm := NewVM()
	if err := vm.RegisterNative("failNative"); err != nil {
		t.Fatal(err)
	}
	budget := uint64(1000)
	got, err := vm.Execute(&program, vmNativeErrorHost{err: errors.New("native failure")}, &budget)
	if err != nil {
		t.Fatal(err)
	}
	if want := "native failure|1"; got.Text() != want {
		t.Fatalf("caught native error = %q, want %q", got.Text(), want)
	}
}
func TestVMNativeHostErrorsKeepWrappedContext(t *testing.T) {
	program, err := compile("native-error-context.tjs", `try { failNative(); } catch(e) { return e.message; } return "miss";`)
	if err != nil {
		t.Fatal(err)
	}
	vm := NewVM()
	if err := vm.RegisterNative("failNative"); err != nil {
		t.Fatal(err)
	}
	budget := uint64(1000)
	got, err := vm.Execute(&program, vmNativeErrorHost{err: fmt.Errorf("Layer.loadImages %q: %w", "空_昼.png", errors.New("unsupported media codec"))}, &budget)
	if err != nil {
		t.Fatal(err)
	}
	if want := `Layer.loadImages "空_昼.png": unsupported media codec`; got.Text() != want {
		t.Fatalf("caught native error = %q, want %q", got.Text(), want)
	}
}

func TestVMUncaughtThrownObjectsUseScalarMessage(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
		avoid  string
	}{
		{
			name:   "exception message",
			source: `throw new Exception("uncaught failure");`,
			want:   "TJS throw: uncaught failure",
			avoid:  "(object)",
		},
		{
			name:   "message captured before finally",
			source: `var e = new Exception("before finally"); try { throw e; } finally { e.message = "after finally"; }`,
			want:   "TJS throw: before finally",
			avoid:  "after finally",
		},
		{
			name:   "non-scalar message falls back to thrown value",
			source: `throw %[message: %[nested: 1]];`,
			want:   "TJS throw: (object)",
			avoid:  "nested",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			program, err := compile("uncaught-message.tjs", test.source)
			if err != nil {
				t.Fatal(err)
			}
			budget := uint64(1000)
			_, err = NewVM().Execute(&program, vmTestHost{}, &budget)
			if err == nil {
				t.Fatal("uncaught throw succeeded")
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("uncaught error = %v, want containing %q", err, test.want)
			}
			if strings.Contains(err.Error(), test.avoid) {
				t.Fatalf("uncaught error = %v, must not contain %q", err, test.avoid)
			}
		})
	}
}

func TestVMHostAbortRemainsFatalInsideTryCatch(t *testing.T) {
	program, err := compile("native-abort.tjs", `try { failNative(); } catch(e) { return "caught"; } return "miss";`)
	if err != nil {
		t.Fatal(err)
	}
	vm := NewVM()
	if err := vm.RegisterNative("failNative"); err != nil {
		t.Fatal(err)
	}
	budget := uint64(1000)
	_, err = vm.Execute(&program, vmNativeErrorHost{err: &VMAbortError{Cause: errors.New("native abort")}}, &budget)
	if err == nil || !strings.Contains(err.Error(), "native abort") {
		t.Fatalf("fatal native error = %v, want uncaught native abort", err)
	}
}

func TestVMHandleInvalidValuesPreserveErrorAndAbortWrapping(t *testing.T) {
	vm := NewVM()
	for _, value := range []Value{IntegerValue(1), NullValue} {
		_, err := vm.handle(value)
		if err == nil || err.Error() != "TJS value is not a non-null object" {
			t.Fatalf("handle(%v) error = %v, want exact non-object error", value, err)
		}
		if !errors.Is(err, errTJSNonNullObject) {
			t.Fatalf("handle(%v) error = %v, want handle sentinel", value, err)
		}
	}
	wrapped := vmAbortError(errTJSNonNullObject)
	var aborted *VMAbortError
	if !errors.As(wrapped, &aborted) {
		t.Fatalf("wrapped handle error = %T, want VMAbortError", wrapped)
	}
	if !errors.Is(wrapped, errTJSNonNullObject) || wrapped.Error() != "TJS value is not a non-null object" {
		t.Fatalf("wrapped handle error = %v, want original text and cause", wrapped)
	}
}
