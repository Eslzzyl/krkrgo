package assets

import (
	"reflect"
	"strings"
	"testing"
)

func TestTextModesAndUnits(t *testing.T) {
	want := []uint16{0, 7, 10, 13, 0x20, 0x65e5, 0xd800, 0xdc00, 0xdfff, 0xffff}
	for _, mode := range []string{"", "c", "c1", "c2", "z", "z0", "z9"} {
		gotBytes, err := EncodeUnits(want, mode)
		if err != nil {
			t.Fatalf("EncodeUnits(%q): %v", mode, err)
		}
		got, err := DecodeUnits(gotBytes)
		if err != nil {
			t.Fatalf("DecodeUnits(%q): %v", mode, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("mode %q: got %#v, want %#v", mode, got, want)
		}
	}
	if _, err := Decode(append([]byte{0xff, 0xfe}, []byte{0x00, 0xd8}...)); err == nil {
		t.Error("Decode accepted isolated surrogate")
	}
	if got, err := Decode([]byte{0xef, 0xbb, 0xbf, 'h', 'i'}); err != nil || got != "hi" {
		t.Errorf("UTF-8 BOM: %q, %v", got, err)
	}
	if got, err := Decode([]byte{0x93, 0xfa, 0x96, 0x7b, 0x8c, 0xea}); err != nil || got != "日本語" {
		t.Errorf("Shift-JIS: %q, %v", got, err)
	}
}
func TestGBKScriptFixture(t *testing.T) {
	// These bytes are copied from the BOM-less legacy text in
	// testdata/amairo's sysscn/system.tjs.
	data := []byte{
		'/', '/', ' ',
		0xb8, 0xf7, 0xb7, 0x4e, 0xbd, 0x4b, 0xc1, 0xcb, 0x84, 0x49,
		0xc0, 0xed, 0xa4, 0xce, 0xb5, 0xc7, 0xe5, 0x68, 0x3f, 0x8c, 0x67, 0xd0, 0xd0,
	}
	got, err := Decode(data)
	if err != nil {
		t.Fatalf("Decode GBK fixture: %v", err)
	}
	if got != "// 各種終了処理の登録?実行" {
		t.Fatalf("Decode GBK fixture = %q", got)
	}
}

func TestShiftJISUnits(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want []uint16
	}{
		{name: "ASCII", data: []byte("ABC"), want: []uint16{'A', 'B', 'C'}},
		{name: "half-width katakana", data: []byte{0xa1, 0xdf}, want: []uint16{0xff61, 0xff9f}},
		{name: "double-byte Japanese", data: []byte{0x82, 0xa0, 0x93, 0xfa, 0x96, 0x7b, 0x8c, 0xea}, want: []uint16{0x3042, 0x65e5, 0x672c, 0x8a9e}},
		{name: "supplementary-plane UTF-8", data: []byte("😀"), want: []uint16{0xd83d, 0xde00}},
	}
	for _, tt := range tests {
		got, err := DecodeUnits(tt.data)
		if err != nil {
			t.Errorf("DecodeUnits(%s): %v", tt.name, err)
			continue
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("DecodeUnits(%s) = %#v, want %#v", tt.name, got, tt.want)
		}
	}

	for _, data := range [][]byte{
		{0x80},       // invalid lead
		{0x82, 0x7f}, // invalid trail
		{0x82},       // truncated double-byte character
	} {
		if got, err := DecodeUnits(data); err == nil {
			t.Errorf("DecodeUnits(%x) = %#v, want error", data, got)
		}
	}
	if got, err := DecodeUnits([]byte{0x81, 0xad}); err != nil || !reflect.DeepEqual(got, []uint16{0xfffd}) {
		t.Errorf("DecodeUnits(unmapped character) = %#v, %v, want replacement character", got, err)
	}
}

func TestTextMalformedInputs(t *testing.T) {
	for _, data := range [][]byte{{0xfe, 0xfe}, {0xfe, 0xfe, 3, 0xff, 0xfe}, {0xff, 0xfe, 0x41}, {0x82}} {
		if _, err := Decode(data); err == nil {
			t.Errorf("Decode accepted malformed stream %x", data)
		}
	}
	for _, mode := range []string{"c0", "c9", "x", "z10"} {
		if _, err := EncodeUnits(nil, mode); err == nil {
			t.Errorf("EncodeUnits accepted mode %q", mode)
		}
	}
}

func TestCxOtomeRoundTripAndChunkBoundaries(t *testing.T) {
	cx, err := OtomeDomain()
	if err != nil {
		t.Fatal(err)
	}
	plain := make([]byte, 70000)
	for i := range plain {
		plain[i] = byte(i)
	}
	whole := append([]byte(nil), plain...)
	cx.Apply(0x12345678, 0, whole)
	chunked := append([]byte(nil), plain...)
	for offset := 0; offset < len(chunked); offset += 137 {
		end := offset + 137
		if end > len(chunked) {
			end = len(chunked)
		}
		cx.Apply(0x12345678, uint64(offset), chunked[offset:end])
	}
	if !reflect.DeepEqual(whole, chunked) {
		t.Fatal("chunked Cx application differs from whole application")
	}
	cx.Apply(0x12345678, 0, whole)
	if !reflect.DeepEqual(plain, whole) {
		t.Fatal("Cx application is not involutive")
	}
}

func TestCxProfileValidation(t *testing.T) {
	bad := `{"m_mask":1,"m_offset":0,"PrologOrder":[0,0,1],"OddBranchOrder":[0,1,2,3,4,5],"EvenBranchOrder":[0,1,2,3,4,5,6,7],"ControlBlock":[]}`
	if _, err := FromJSON(bad); err == nil || !strings.Contains(err.Error(), "1024") {
		t.Fatalf("unexpected profile error: %v", err)
	}
}
