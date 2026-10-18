package assets

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

// TCWF0 is Kirikiri's fixed-block ADPCM wave container, decoded by the wutcwf
// plugin. Each block stores one channel; stereo blocks are written right then
// left. The payload of a block is split into MS ADPCM low nibbles and IMA
// ADPCM high nibbles that are decoded in lockstep and summed.
const (
	tcwfMagic           = "TCWF0\x1a"
	tcwfHeaderSize      = 24
	tcwfBlockHeaderSize = 32
	tcwfMaxChannels     = 2
	tcwfPeakCount       = 6
	tcwfMaxSampleRate   = 768_000
)

// Tables mirror reference/krkr2-main/cpp/plugins/wutcwf.cpp.
var (
	tcwfAdaptationTable = [16]int{
		230, 230, 230, 230, 307, 409, 512, 614,
		768, 614, 512, 409, 307, 230, 230, 230,
	}
	tcwfAdaptCoeff1    = [7]int{256, 512, 0, 192, 240, 460, 392}
	tcwfAdaptCoeff2    = [7]int{0, -256, 0, 64, 0, -208, -232}
	tcwfIMAIndexAdjust = [16]int{
		-1, -1, -1, -1,
		2, 4, 6, 8,
		-1, -1, -1, -1,
		2, 4, 6, 8,
	}
	tcwfIMAStepSize = [89]int{
		7, 8, 9, 10, 11, 12, 13, 14, 16, 17,
		19, 21, 23, 25, 28, 31, 34, 37, 41, 45,
		50, 55, 60, 66, 73, 80, 88, 97, 107, 118,
		130, 143, 157, 173, 190, 209, 230, 253, 279, 307,
		337, 371, 408, 449, 494, 544, 598, 658, 724, 796,
		876, 963, 1060, 1166, 1282, 1411, 1552, 1707, 1878, 2066,
		2272, 2499, 2749, 3024, 3327, 3660, 4026, 4428, 4871, 5358,
		5894, 6484, 7132, 7845, 8630, 9493, 10442, 11487, 12635, 13899,
		15289, 16818, 18500, 20350, 22385, 24623, 27086, 29794, 32767,
	}
)

// TCWFInfo reports the source metadata of a decoded TCWF stream.
type TCWFInfo struct {
	Channels        uint8
	SampleRate      uint32
	Blocks          int
	BytesPerBlock   int
	SamplesPerBlock int
}

// IsTCWF reports whether data starts with the TCWF0 signature. It does not
// validate the rest of the container.
func IsTCWF(data []byte) bool {
	return bytes.HasPrefix(data, []byte(tcwfMagic))
}

// DecodeTCWF decodes a complete TCWF0 resource into interleaved signed 16-bit
// PCM. Blocks is the header's declared block count; truncated block data is an
// error rather than a short source, matching the block-at-a-time reference
// decoder's dependency on fixed block strides. Bytes after the declared blocks
// are ignored.
func DecodeTCWF(data []byte) (Audio, TCWFInfo, error) {
	var zero TCWFInfo
	if len(data) < tcwfHeaderSize {
		return Audio{}, zero, malformed("TCWF", "truncated header")
	}
	if !IsTCWF(data) {
		return Audio{}, zero, malformed("TCWF", "invalid signature")
	}
	channels := int(data[6])
	frequency := int(int32(binary.LittleEndian.Uint32(data[8:12])))
	blocks := int(int32(binary.LittleEndian.Uint32(data[12:16])))
	bytesPerBlock := int(int32(binary.LittleEndian.Uint32(data[16:20])))
	samplesPerBlock := int(int32(binary.LittleEndian.Uint32(data[20:24])))

	switch {
	case channels == 0:
		return Audio{}, zero, malformed("TCWF", "zero channel count")
	case channels > tcwfMaxChannels:
		return Audio{}, zero, fmt.Errorf("%w: TCWF channels %d", ErrUnsupported, channels)
	case frequency <= 0 || frequency > tcwfMaxSampleRate:
		return Audio{}, zero, malformed("TCWF", "sample rate out of range")
	case blocks < 0:
		return Audio{}, zero, malformed("TCWF", "negative block count")
	case samplesPerBlock < 2:
		return Audio{}, zero, malformed("TCWF", "samples per block below two")
	case bytesPerBlock < tcwfBlockHeaderSize:
		return Audio{}, zero, malformed("TCWF", "block smaller than its header")
	case bytesPerBlock-tcwfBlockHeaderSize < samplesPerBlock-2:
		return Audio{}, zero, malformed("TCWF", "block payload cannot hold sample count")
	}

	need := uint64(tcwfHeaderSize)
	if blocks > 0 {
		unit := uint64(bytesPerBlock) * uint64(channels)
		if unit > (^uint64(0)-need)/uint64(blocks) {
			return Audio{}, zero, malformed("TCWF", "block data size overflow")
		}
		need += unit * uint64(blocks)
	}
	if uint64(len(data)) < need {
		return Audio{}, zero, malformed("TCWF", "truncated block data")
	}

	// The block/sample constraints bound the PCM payload by the input size, so
	// only integer overflow needs an explicit guard here.
	totalSamples := uint64(blocks) * uint64(samplesPerBlock) * uint64(channels)
	if totalSamples > uint64(maxInt()) {
		return Audio{}, zero, malformed("TCWF", "decoded PCM size overflow")
	}
	samples := make([]int16, int(totalSamples))
	for block := range blocks {
		base := tcwfHeaderSize + block*channels*bytesPerBlock
		for channel := range channels {
			blockData := data[base+channel*bytesPerBlock : base+(channel+1)*bytesPerBlock]
			if err := decodeTCWFBlock(samples, block*samplesPerBlock, channels, channel, samplesPerBlock, blockData); err != nil {
				return Audio{}, zero, err
			}
		}
	}
	info := TCWFInfo{
		Channels:        uint8(channels),
		SampleRate:      uint32(frequency),
		Blocks:          blocks,
		BytesPerBlock:   bytesPerBlock,
		SamplesPerBlock: samplesPerBlock,
	}
	return Audio{Channels: uint8(channels), SampleRate: uint32(frequency), Samples: samples}, info, nil
}

// decodeTCWFBlock expands one channel block into dst, starting at sample frame
// frameStart. dst is interleaved with channels stride.
func decodeTCWFBlock(dst []int16, frameStart, channels, channel, samplesPerBlock int, block []byte) error {
	sample0 := int(int16(binary.LittleEndian.Uint16(block[0:2])))
	sample1 := int(int16(binary.LittleEndian.Uint16(block[2:4])))
	idelta := int(int16(binary.LittleEndian.Uint16(block[4:6])))
	bpred := int(block[6])
	stepIndex := int(block[7])
	if bpred >= len(tcwfAdaptCoeff1) {
		return malformed("TCWF", "invalid ADPCM predictor")
	}
	if stepIndex >= len(tcwfIMAStepSize) {
		return malformed("TCWF", "invalid IMA step index")
	}
	// A zero revision disables a peak entry; the reference ignores its
	// position completely in that case.
	for i := range tcwfPeakCount {
		peak := block[8+i*4:]
		if int16(binary.LittleEndian.Uint16(peak[2:4])) == 0 {
			continue
		}
		if int(binary.LittleEndian.Uint16(peak[0:2])) >= samplesPerBlock {
			return malformed("TCWF", "peak position outside block")
		}
	}

	index := frameStart*channels + channel
	dst[index] = int16(sample0)
	dst[index+channels] = int16(sample1)

	data := block[tcwfBlockHeaderSize:]
	index += 2 * channels

	// MS ADPCM: low nibbles.
	previous1, previous2 := sample1, sample0
	for k, p := 2, 0; k < samplesPerBlock; k, p = k+1, p+1 {
		bytecode := int(data[p] & 0x0F)
		ideltaSave := idelta
		idelta = (tcwfAdaptationTable[bytecode] * idelta) >> 8
		if idelta < 16 {
			idelta = 16
		}
		if bytecode&0x8 != 0 {
			bytecode -= 0x10
		}
		predict := ((previous1 * tcwfAdaptCoeff1[bpred]) + (previous2 * tcwfAdaptCoeff2[bpred])) >> 8
		current := bytecode*ideltaSave + predict
		current = clampTCWFSample(current)
		dst[index] = int16(current)
		previous2, previous1 = previous1, current
		index += channels
	}

	// IMA ADPCM: high nibbles summed onto the MS result.
	previous := 0
	index = frameStart*channels + channel + 2*channels
	for k, p := 2, 0; k < samplesPerBlock; k, p = k+1, p+1 {
		bytecode := int((data[p] >> 4) & 0x0F)
		step := tcwfIMAStepSize[stepIndex]
		current := previous
		diff := step >> 3
		if bytecode&1 != 0 {
			diff += step >> 2
		}
		if bytecode&2 != 0 {
			diff += step >> 1
		}
		if bytecode&4 != 0 {
			diff += step
		}
		if bytecode&8 != 0 {
			diff = -diff
		}
		current += diff
		current = clampTCWFSample(current)
		stepIndex += tcwfIMAIndexAdjust[bytecode]
		if stepIndex < 0 {
			stepIndex = 0
		} else if stepIndex >= len(tcwfIMAStepSize) {
			stepIndex = len(tcwfIMAStepSize) - 1
		}
		previous = current
		combined := clampTCWFSample(int(dst[index]) + current)
		dst[index] = int16(combined)
		index += channels
	}

	// Unexpected peak correction, applied in header order.
	for i := range tcwfPeakCount {
		peak := block[8+i*4:]
		revise := int(int16(binary.LittleEndian.Uint16(peak[2:4])))
		if revise == 0 {
			continue
		}
		position := int(binary.LittleEndian.Uint16(peak[0:2]))
		index := (frameStart+position)*channels + channel
		dst[index] = int16(clampTCWFSample(int(dst[index]) - revise))
	}
	return nil
}

func clampTCWFSample(value int) int {
	if value > 32767 {
		return 32767
	}
	if value < -32768 {
		return -32768
	}
	return value
}
