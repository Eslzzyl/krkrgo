package assets

import (
	"bytes"
	"errors"
	"testing"

	"github.com/pierrec/lz4/v4"
)

func makeLZFSFrame(t *testing.T, contents []byte, options ...lz4.Option) []byte {
	t.Helper()
	var encoded bytes.Buffer
	writer := lz4.NewWriter(&encoded)
	if err := writer.Apply(options...); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(contents); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

func TestDecodeLZFSOrdinaryAndInvalidHeader(t *testing.T) {
	for _, source := range [][]byte{
		[]byte("plain content"),
		{0x04, 0x22, 0x4d, 0x18, 0xff, 0x01},
	} {
		got, err := DecodeLZFS(source, 1024)
		if err != nil || !bytes.Equal(got, source) {
			t.Fatalf("DecodeLZFS(%x) = %x, %v; want unchanged contents", source, got, err)
		}
	}
}

func TestDecodeLZFSFrame(t *testing.T) {
	want := bytes.Repeat([]byte("lzfs-data-"), 4096)
	frame := makeLZFSFrame(
		t,
		want,
		lz4.SizeOption(uint64(len(want))),
		lz4.BlockSizeOption(lz4.Block64Kb),
		lz4.BlockChecksumOption(true),
		lz4.ChecksumOption(true),
	)
	got, err := DecodeLZFS(frame, uint64(len(want)))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("decoded frame length = %d, want %d", len(got), len(want))
	}
}

func TestDecodeLZFSFrameLimitAndCorruption(t *testing.T) {
	want := bytes.Repeat([]byte("limited-data-"), 128)
	frame := makeLZFSFrame(t, want)
	if _, err := DecodeLZFS(frame, uint64(len(want)-1)); !errors.Is(err, ErrLimit) {
		t.Fatalf("over-limit frame error = %v, want ErrLimit", err)
	}
	if _, err := DecodeLZFS(frame[:len(frame)-4], uint64(len(want))); !errors.Is(err, ErrLZFSDecompress) {
		t.Fatalf("truncated frame error = %v, want ErrLZFSDecompress", err)
	}
}
