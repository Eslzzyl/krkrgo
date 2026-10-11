package runtime

import (
	"errors"
	"testing"

	"github.com/eslzzyl/krkrgo/internal/tjs"
)

type fakeClipboardBackend struct {
	text    string
	present bool
	reads   int
}

func (b *fakeClipboardBackend) ReadText() (string, bool, error) {
	b.reads++
	return b.text, b.present, nil
}

func (b *fakeClipboardBackend) WriteText(text string) error {
	b.text = text
	b.present = true
	return nil
}

func TestClipboardNativeStaticContract(t *testing.T) {
	backend := &fakeClipboardBackend{}
	services := DefaultServices()
	services.clipboardBackend = backend
	session := NewSession(services, 1000)

	value, err := session.Evaluate("Clipboard.asText")
	if err != nil {
		t.Fatal(err)
	}
	if !value.IsVoid() {
		t.Fatalf("absent Clipboard.asText = %#v, want void", value)
	}

	value, err = session.Evaluate("Clipboard.hasFormat(1)")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := value.Int(); got != 0 {
		t.Fatalf("Clipboard.hasFormat(1) without text = %d, want 0", got)
	}

	reads := backend.reads
	value, err = session.Evaluate("Clipboard.hasFormat(99)")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := value.Int(); got != 0 {
		t.Fatalf("Clipboard.hasFormat(99) = %d, want 0", got)
	}
	if backend.reads != reads {
		t.Fatalf("unsupported format read clipboard %d times, want no additional read", backend.reads-reads)
	}

	backend.present = true
	value, err = session.Evaluate("Clipboard.asText")
	if err != nil {
		t.Fatal(err)
	}
	if value.Kind() != tjs.KindString || value.Text() != "" {
		t.Fatalf("present empty Clipboard.asText = %#v, want an empty string", value)
	}
	value, err = session.Evaluate("Clipboard.hasFormat(1)")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := value.Int(); got != 1 {
		t.Fatalf("Clipboard.hasFormat(1) with empty text = %d, want 1", got)
	}
	backend.present = false

	if _, err := session.Evaluate(`Clipboard.asText = "clipboard text"`); err != nil {
		t.Fatal(err)
	}
	if !backend.present || backend.text != "clipboard text" {
		t.Fatalf("clipboard after setter = (%q, %t)", backend.text, backend.present)
	}

	value, err = session.Evaluate("Clipboard.asText")
	if err != nil {
		t.Fatal(err)
	}
	if value.Kind() != tjs.KindString || value.Text() != "clipboard text" {
		t.Fatalf("Clipboard.asText = %#v, want string %q", value, "clipboard text")
	}

	value, err = session.Evaluate("Clipboard.hasFormat(1)")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := value.Int(); got != 1 {
		t.Fatalf("Clipboard.hasFormat(1) with text = %d, want 1", got)
	}

	if _, err := session.Evaluate("new Clipboard()"); !errors.Is(err, ErrUnsupportedNative) {
		t.Fatalf("Clipboard constructor error = %v, want ErrUnsupportedNative", err)
	}
}
