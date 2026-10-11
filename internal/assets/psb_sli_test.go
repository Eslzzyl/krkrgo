package assets

import (
	"encoding/binary"
	"errors"
	"testing"
)

func minimalPSB(value []byte) []byte {
	d := append([]byte("PSB\x00\x02\x00\x00\x00"), make([]byte, 32)...)
	for i, n := range []uint32{40, 40, 46, 49, 49, 52, uint32(55 + len(value)), 55} {
		binary.LittleEndian.PutUint32(d[8+i*4:], n)
	}
	for range 5 {
		d = append(d, 13, 0, 13)
	}
	d = append(d, value...)
	return d
}

func TestPSBScalarsAndLists(t *testing.T) {
	doc, err := ParsePSB(minimalPSB([]byte{5, 0xff}))
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := doc.Root.(int64); !ok || got != -1 {
		t.Fatalf("root = %#v, want int64(-1)", doc.Root)
	}
	list := minimalPSB([]byte{0x20, 13, 2, 13, 0, 1, 2, 3})
	doc, err = ParsePSB(list)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := doc.Root.([]Value)
	if !ok || len(got) != 2 || got[0] != true || got[1] != false {
		t.Fatalf("list = %#v", doc.Root)
	}
}

func TestPSBRejectsTruncation(t *testing.T) {
	full := minimalPSB([]byte{4})
	for n := range full {
		if _, err := ParsePSB(full[:n]); err == nil {
			t.Fatalf("accepted truncation at %d", n)
		} else if !errors.Is(err, ErrInvalidPSB) {
			t.Fatalf("error at %d is not typed: %v", n, err)
		}
	}
}

func TestSLIConditionsAndFrames(t *testing.T) {
	info, err := ParseSLI("#2.00\nLink { From=6; To=2; }\nLink { From=6; To=3; Condition=gt; RefValue=1; CondVar=0; }\nLabel { Position=3; Name='日本語; label'; }")
	if err != nil {
		t.Fatal(err)
	}
	flags := [16]int32{}
	if got := info.NextLink(0, &flags); got == nil || got.To != 2 {
		t.Fatalf("first link = %#v", got)
	}
	flags[0] = 2
	if got := info.NextLink(0, &flags); got == nil || got.To != 3 {
		t.Fatalf("conditional link = %#v", got)
	}
	if len(info.Labels) != 1 || info.Labels[0].Name != "日本語; label" {
		t.Fatalf("labels = %#v", info.Labels)
	}
	if err := info.ValidateFrames(5); err == nil {
		t.Fatal("accepted out-of-range link")
	}
}

func TestSLILegacyAndMalformed(t *testing.T) {
	old, err := ParseSLI("LoopStart=2\nLoopLength=4\n")
	if err != nil || len(old.Links) != 1 || old.Links[0].From != 6 || old.Links[0].To != 2 {
		t.Fatalf("legacy = %#v, err %v", old, err)
	}
	for _, source := range []string{
		"#3.00",
		"#2.00\nLink { From=-1; }",
		"#2.00\nLink { CondVar=16; }",
		"#2.00\nLabel { Name='unterminated; }",
		"LoopStart=18446744073709551615\nLoopLength=1",
		"#2.00\nLink { From=3; From=4; }",
		"#2.00\nLink { Unknown=0; }",
	} {
		if _, err := ParseSLI(source); err == nil {
			t.Fatalf("accepted malformed SLI %q", source)
		}
	}
}
