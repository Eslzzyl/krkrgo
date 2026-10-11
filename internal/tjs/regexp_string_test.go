package tjs

import (
	"errors"
	"testing"
)

func TestStringEscapeUTF16(t *testing.T) {
	got := EscapeString("a\n\\\"")
	if got != `a\n\\\"` {
		t.Fatalf("escape = %q", got)
	}
	if got := EscapeString("😀"); got != "😀" {
		t.Fatalf("astral escape = %q", got)
	}
	u, err := UnescapeUTF16(`a\n\uD800`)
	if err != nil {
		t.Fatal(err)
	}
	if len(u) != 3 || u[1] != '\n' || u[2] != 0xd800 {
		t.Fatalf("unescape units = %#v", u)
	}
}

func TestFormatString(t *testing.T) {
	got, err := FormatString("%s %04d %.2f %%", StringValue("ok"), IntegerValue(7), RealValue(1.25))
	if err != nil {
		t.Fatal(err)
	}
	if got != "ok 0007 1.25 %" {
		t.Fatalf("format = %q", got)
	}
}

func TestRegexpUTF16AndReplacement(t *testing.T) {
	r, err := CompileRegexp("(b)", "g")
	if err != nil {
		t.Fatal(err)
	}
	m, ok := r.Find("😀b")
	if !ok || m.Index != 2 || m.Length != 1 || len(m.Submatches) != 1 || m.Submatches[0] != "b" {
		t.Fatalf("match = %#v, %v", m, ok)
	}
	if got := r.Replace("abcb", "<$1>"); got != "a<b>c<b>" {
		t.Fatalf("replace = %q", got)
	}
}

func TestRegexpErrors(t *testing.T) {
	if _, err := CompileRegexp("(", ""); err == nil || !errors.Is(err, ErrRegexpMalformed) {
		t.Fatalf("malformed error = %v", err)
	}
	if _, err := CompileRegexp(`a(?=b)`, ""); err == nil || !errors.Is(err, ErrRegexpUnsupported) {
		t.Fatalf("unsupported error = %v", err)
	}
}
