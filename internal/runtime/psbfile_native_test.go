package runtime

import (
	"encoding/binary"
	"testing"

	"github.com/eslzzyl/krkrgo/internal/tjs"
)

func testMinimalPSB(value []byte) []byte {
	data := append([]byte("PSB\x00\x02\x00\x00\x00"), make([]byte, 32)...)
	for i, offset := range []uint32{40, 40, 46, 49, 49, 52, uint32(55 + len(value)), 55} {
		binary.LittleEndian.PutUint32(data[8+i*4:], offset)
	}
	for range 5 {
		data = append(data, 13, 0, 13)
	}
	return append(data, value...)
}

func TestLoadPSBRootAcceptsEmbeddedOctet(t *testing.T) {
	services := &Services{}
	vm := tjs.NewVM()
	source := tjs.OctetValue(testMinimalPSB([]byte{5, 0xff}))

	got, err := services.loadPSBRoot(vm, source)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind() != tjs.KindInteger {
		t.Fatalf("root kind = %s, want Integer", got.Kind())
	}
	value, err := got.Int()
	if err != nil || value != -1 {
		t.Fatalf("root = %d, %v; want -1", value, err)
	}
}
