package assets

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"

	"github.com/pierrec/lz4/v4"
)

var ErrLZFSDecompress = errors.New("lzfs: Decompress fail")

const lzfsFrameMagic uint32 = 0x184d2204

// DecodeLZFS returns ordinary storage contents unchanged and expands valid
// LZ4 frames. Invalid frame headers are treated as ordinary data, matching the
// LZFS storage media implementation.
func DecodeLZFS(data []byte, maxOutput uint64) ([]byte, error) {
	if len(data) < 4 || binary.LittleEndian.Uint32(data[:4]) != lzfsFrameMagic {
		return data, nil
	}

	valid, err := lz4.ValidFrameHeader(data)
	if err != nil || !valid {
		return data, nil
	}

	reader := lz4.NewReader(bytes.NewReader(data))
	if err := reader.Apply(lz4.ConcurrencyOption(1)); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrLZFSDecompress, err)
	}
	limit := int64(math.MaxInt64)
	if maxOutput < math.MaxInt64 {
		limit = int64(maxOutput) + 1
	}
	decoded, err := io.ReadAll(io.LimitReader(reader, limit))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrLZFSDecompress, err)
	}
	if uint64(len(decoded)) > maxOutput {
		return nil, fmt.Errorf("%w: %w (limit %d bytes)", ErrLZFSDecompress, ErrLimit, maxOutput)
	}
	return decoded, nil
}
