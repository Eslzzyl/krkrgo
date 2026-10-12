package assets

import (
	"bytes"
	"testing"
)

func TestDecodePSBRLE(t *testing.T) {
	data := []byte{
		0x00, 0x03, 0x02, 0x01, 0x04,
		0x80, 0x30, 0x20, 0x10, 0xff,
	}
	image, err := DecodePSBRLE(data, 4, 1)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{
		0x01, 0x02, 0x03, 0x04,
		0x10, 0x20, 0x30, 0xff,
		0x10, 0x20, 0x30, 0xff,
		0x10, 0x20, 0x30, 0xff,
	}
	if !bytes.Equal(image.RGBA, want) {
		t.Fatalf("decoded pixels = %v, want %v", image.RGBA, want)
	}
}
func TestDecodePSBRLEIndices(t *testing.T) {
	data := []byte{0x01, 0x02, 0x01, 0x80, 0x07}
	indices, err := DecodePSBRLEIndices(data, 5, 1)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{0x02, 0x01, 0x07, 0x07, 0x07}
	if !bytes.Equal(indices, want) {
		t.Fatalf("decoded indices = %v, want %v", indices, want)
	}
}

func TestDecodePSBRLERejectsMalformedOutput(t *testing.T) {
	for name, data := range map[string][]byte{
		"truncated repeat":  {0x80, 0x00},
		"truncated literal": {0x01, 0x00, 0x00, 0x00},
		"oversized repeat":  {0xff, 0x00, 0x00, 0x00, 0xff},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodePSBRLE(data, 1, 1); err == nil {
				t.Fatal("malformed PSB RL data decoded successfully")
			}
		})
	}
}
