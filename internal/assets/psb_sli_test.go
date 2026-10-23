package assets

import (
	"encoding/binary"
	"errors"
	"testing"
)

func minimalPSB(value []byte) []byte {
	return minimalPSBVersion(2, value)
}

func minimalPSBVersion(version uint16, value []byte) []byte {
	headerLength := 40
	if version == 3 {
		headerLength = 44
	} else if version == 4 {
		headerLength = 56
	}
	d := append([]byte{'P', 'S', 'B', 0, byte(version), byte(version >> 8), 0, 0}, make([]byte, headerLength-8)...)
	for i, n := range []uint32{uint32(headerLength), uint32(headerLength), uint32(headerLength + 6), uint32(headerLength + 9), uint32(headerLength + 9), uint32(headerLength + 12), uint32(headerLength + 15 + len(value)), uint32(headerLength + 15)} {
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

func TestPSBVersion4UsesExtendedHeader(t *testing.T) {
	doc, err := ParsePSB(minimalPSBVersion(4, []byte{5, 0xff}))
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := doc.Root.(int64); !ok || got != -1 || doc.Version != 4 {
		t.Fatalf("version 4 root = %#v, version=%d", doc.Root, doc.Version)
	}
}
func TestPSBEmoteSeedDecryptsEqualOffsetRegion(t *testing.T) {
	const seed = int32(742877301)
	data := minimalPSB([]byte{5, 0xff})
	binary.LittleEndian.PutUint32(data[8:], 40)
	encrypted, err := decryptEmotePSB(data, uint32(seed))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := ParsePSBWithSeed(encrypted, seed)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := doc.Root.(int64); !ok || got != -1 {
		t.Fatalf("root = %#v, want int64(-1)", doc.Root)
	}
}

func TestPSBAllowsZeroHeaderLength(t *testing.T) {
	data := minimalPSB([]byte{4})
	binary.LittleEndian.PutUint32(data[8:], 0)
	if _, err := ParsePSB(data); err != nil {
		t.Fatalf("zero header length rejected: %v", err)
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
