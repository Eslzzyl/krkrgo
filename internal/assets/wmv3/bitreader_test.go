package wmv3

import (
	"errors"
	"testing"
)

func TestBitReaderMSBFirstAcrossBytes(t *testing.T) {
	r := NewBitReader([]byte{0xa5, 0x3c})
	if v, err := r.Bits(4); err != nil || v != 0b1010 || r.Pos != 4 {
		t.Fatalf("Bits(4) = %d, %v, pos %d", v, err, r.Pos)
	}
	// Bits 4..11 cross the byte boundary: low nibble of 0xa5 then the
	// high nibble of 0x3c.
	if v, err := r.Bits(8); err != nil || v != 0x53 || r.Pos != 12 {
		t.Fatalf("Bits(8) = %#x, %v, pos %d", v, err, r.Pos)
	}
	if v, err := r.Bits(4); err != nil || v != 0b1100 || r.Pos != 16 {
		t.Fatalf("Bits(4) = %d, %v, pos %d", v, err, r.Pos)
	}
	if got := r.Remaining(); got != 0 {
		t.Fatalf("Remaining = %d after full consume", got)
	}
	if _, err := r.Bits(1); !errors.Is(err, ErrTruncated) {
		t.Fatalf("overread: want ErrTruncated, got %v", err)
	}
	if r.Pos != 16 {
		t.Fatalf("failed read moved pos to %d", r.Pos)
	}
}

func TestBitReaderSingleBits(t *testing.T) {
	r := NewBitReader([]byte{0x01, 0x01})
	for i := range 7 {
		if b, err := r.Bit(); err != nil || b {
			t.Fatalf("leading bit %d = %v, %v", i, b, err)
		}
	}
	if b, err := r.Bit(); err != nil || !b {
		t.Fatalf("bit 7 = %v, %v", b, err)
	}
	for i := range 7 {
		if b, err := r.Bit(); err != nil || b {
			t.Fatalf("second byte bit %d = %v, %v", i, b, err)
		}
	}
	if b, err := r.Bit(); err != nil || !b {
		t.Fatalf("bit 15 = %v, %v", b, err)
	}
}

func TestBitReaderZeroAndRange(t *testing.T) {
	r := NewBitReader([]byte{0xff})
	if v, err := r.Bits(0); err != nil || v != 0 || r.Pos != 0 {
		t.Fatalf("Bits(0) = %d, %v, pos %d", v, err, r.Pos)
	}
	if _, err := r.Bits(33); err == nil {
		t.Fatal("Bits(33) succeeded")
	}
	if _, err := r.Bits(-1); err == nil {
		t.Fatal("Bits(-1) succeeded")
	}
	if err := r.Skip(-1); err == nil {
		t.Fatal("Skip(-1) succeeded")
	}
	if r.Pos != 0 {
		t.Fatalf("invalid counts moved pos to %d", r.Pos)
	}
	if _, err := NewBitReader(nil).Bits(1); !errors.Is(err, ErrTruncated) {
		t.Fatalf("empty input: want ErrTruncated, got %v", err)
	}
}

func TestBitReaderSkipBounds(t *testing.T) {
	r := NewBitReader([]byte{0xff, 0xff})
	if err := r.Skip(0); err != nil || r.Pos != 0 {
		t.Fatalf("Skip(0): %v, pos %d", err, r.Pos)
	}
	if err := r.Skip(9); err != nil || r.Pos != 9 {
		t.Fatalf("Skip(9): %v, pos %d", err, r.Pos)
	}
	if got := r.Remaining(); got != 7 {
		t.Fatalf("Remaining = %d, want 7", got)
	}
	if err := r.Skip(8); !errors.Is(err, ErrTruncated) {
		t.Fatalf("Skip past end: want ErrTruncated, got %v", err)
	}
	if r.Pos != 9 {
		t.Fatalf("failed skip moved pos to %d", r.Pos)
	}
	if err := r.Skip(7); err != nil || r.Pos != 16 {
		t.Fatalf("final Skip(7): %v, pos %d", err, r.Pos)
	}
	if got := r.Remaining(); got != 0 {
		t.Fatalf("Remaining = %d at end", got)
	}
}
