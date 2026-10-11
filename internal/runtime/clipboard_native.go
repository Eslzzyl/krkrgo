package runtime

import (
	"fmt"

	"github.com/atotto/clipboard"
	"github.com/eslzzyl/krkrgo/internal/tjs"
)

const clipboardTextFormat = 1

type clipboardBackend interface {
	ReadText() (text string, present bool, err error)
	WriteText(text string) error
}

type systemClipboardBackend struct{}

func (systemClipboardBackend) ReadText() (string, bool, error) {
	text, err := clipboard.ReadAll()
	if err != nil {
		return "", false, err
	}
	// The selected library exposes text contents rather than format metadata;
	// an empty read is therefore treated as an absent text format.
	return text, text != "", nil
}

func (systemClipboardBackend) WriteText(text string) error {
	return clipboard.WriteAll(text)
}

func (s *Services) clipboardNative(operation string, args []tjs.Value) (tjs.Value, error) {
	s.mu.RLock()
	backend := s.clipboardBackend
	s.mu.RUnlock()
	if backend == nil {
		backend = systemClipboardBackend{}
	}

	switch operation {
	case "@initialize":
		return tjs.VoidValue, fmt.Errorf("%w: Clipboard is a static class", ErrUnsupportedNative)
	case "hasFormat":
		if len(args) < 1 {
			return tjs.VoidValue, fmt.Errorf("Clipboard.hasFormat requires a format")
		}
		format, err := args[0].Int()
		if err != nil {
			return tjs.VoidValue, fmt.Errorf("Clipboard.hasFormat format: %w", err)
		}
		if format != clipboardTextFormat {
			return tjs.IntegerValue(0), nil
		}
		_, present, err := backend.ReadText()
		if err != nil {
			return tjs.VoidValue, err
		}
		return tjs.IntegerValue(boolInt(present)), nil
	case "get:asText":
		text, present, err := backend.ReadText()
		if err != nil {
			return tjs.VoidValue, err
		}
		if !present {
			return tjs.VoidValue, nil
		}
		return tjs.StringValue(text), nil
	case "set:asText":
		if len(args) < 1 {
			return tjs.VoidValue, fmt.Errorf("Clipboard.asText setter requires a value")
		}
		if err := backend.WriteText(args[0].Text()); err != nil {
			return tjs.VoidValue, err
		}
		return tjs.VoidValue, nil
	default:
		return tjs.VoidValue, fmt.Errorf("%w: Clipboard.%s", ErrUnsupportedNative, operation)
	}
}
