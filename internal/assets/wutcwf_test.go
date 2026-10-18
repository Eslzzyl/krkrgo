package assets

import (
	"encoding/binary"
	"errors"
	"testing"
)

type tcwfTestPeak struct {
	pos    uint16
	revise int16
}

type tcwfTestBlock struct {
	sample0   int16
	sample1   int16
	idelta    int16
	bpred     byte
	stepIndex byte
	peaks     [tcwfPeakCount]tcwfTestPeak
	payload   []byte
}

// tcwfTestFile builds a synthetic container. blocks holds per-channel blocks
// in stream order, so stereo callers list right then left for every block group.
func tcwfTestFile(channels byte, rate uint32, bytesPerBlock, samplesPerBlock int, blocks []tcwfTestBlock) []byte {
	if channels == 0 || len(blocks)%int(channels) != 0 {
		panic("tcwfTestFile: block list must contain whole block groups")
	}
	out := make([]byte, tcwfHeaderSize, tcwfHeaderSize+len(blocks)*bytesPerBlock)
	copy(out, tcwfMagic)
	out[6] = channels
	binary.LittleEndian.PutUint32(out[8:12], rate)
	binary.LittleEndian.PutUint32(out[12:16], uint32(len(blocks)/int(channels)))
	binary.LittleEndian.PutUint32(out[16:20], uint32(bytesPerBlock))
	binary.LittleEndian.PutUint32(out[20:24], uint32(samplesPerBlock))
	for _, block := range blocks {
		out = append(out, tcwfTestBlockBytes(bytesPerBlock, block)...)
	}
	return out
}

func tcwfTestBlockBytes(bytesPerBlock int, block tcwfTestBlock) []byte {
	out := make([]byte, bytesPerBlock)
	binary.LittleEndian.PutUint16(out[0:2], uint16(block.sample0))
	binary.LittleEndian.PutUint16(out[2:4], uint16(block.sample1))
	binary.LittleEndian.PutUint16(out[4:6], uint16(block.idelta))
	out[6] = block.bpred
	out[7] = block.stepIndex
	for i, peak := range block.peaks {
		binary.LittleEndian.PutUint16(out[8+i*4:10+i*4], peak.pos)
		binary.LittleEndian.PutUint16(out[10+i*4:12+i*4], uint16(peak.revise))
	}
	if len(block.payload) > bytesPerBlock-tcwfBlockHeaderSize {
		panic("tcwfTestBlockBytes: payload exceeds block")
	}
	copy(out[tcwfBlockHeaderSize:], block.payload)
	return out
}

func requireSamples(t *testing.T, got, want []int16) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("sample count = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sample[%d] = %d, want %d (%v)", i, got[i], want[i], got)
		}
	}
}

func TestIsTCWF(t *testing.T) {
	if !IsTCWF([]byte(tcwfMagic)) {
		t.Fatal("magic-only prefix not recognized")
	}
	if !IsTCWF(append([]byte(tcwfMagic), 1, 2, 3)) {
		t.Fatal("signature with trailing bytes not recognized")
	}
	for _, data := range [][]byte{nil, []byte("TCWF"), []byte("TCWF0"), []byte("TCWF1\x1a")} {
		if IsTCWF(data) {
			t.Fatalf("IsTCWF(%q) = true", data)
		}
	}
}

func TestDecodeTCWFMonoMultipleBlocks(t *testing.T) {
	data := tcwfTestFile(1, 8000, tcwfBlockHeaderSize, 2, []tcwfTestBlock{
		{sample0: 100, sample1: 200},
		{sample0: -100, sample1: -200},
	})
	audio, info, err := DecodeTCWF(data)
	if err != nil {
		t.Fatalf("DecodeTCWF: %v", err)
	}
	if audio.Channels != 1 || audio.SampleRate != 8000 {
		t.Fatalf("audio format = %dch %dHz", audio.Channels, audio.SampleRate)
	}
	if audio.Frames() != 4 {
		t.Fatalf("frames = %d, want 4", audio.Frames())
	}
	requireSamples(t, audio.Samples, []int16{100, 200, -100, -200})
	want := TCWFInfo{Channels: 1, SampleRate: 8000, Blocks: 2, BytesPerBlock: 32, SamplesPerBlock: 2}
	if info != want {
		t.Fatalf("info = %+v, want %+v", info, want)
	}
}

func TestDecodeTCWFStereoRightThenLeft(t *testing.T) {
	data := tcwfTestFile(2, 44_100, 33, 3, []tcwfTestBlock{
		{sample0: 1000, sample1: 2000, idelta: 16, payload: []byte{0x00}},
		{sample0: -1000, sample1: -2000, idelta: 16, payload: []byte{0x00}},
	})
	audio, info, err := DecodeTCWF(data)
	if err != nil {
		t.Fatalf("DecodeTCWF: %v", err)
	}
	if audio.Channels != 2 || audio.SampleRate != 44_100 {
		t.Fatalf("audio format = %dch %dHz", audio.Channels, audio.SampleRate)
	}
	requireSamples(t, audio.Samples, []int16{
		1000, -1000,
		2000, -2000,
		2000, -2000,
	})
	if info.Blocks != 1 || info.SamplesPerBlock != 3 {
		t.Fatalf("info = %+v", info)
	}
}

func TestDecodeTCWFMSAndIMACombination(t *testing.T) {
	data := tcwfTestFile(1, 8000, 33, 3, []tcwfTestBlock{
		{idelta: 16, payload: []byte{0x41}}, // MS nibble 1, IMA nibble 4
	})
	audio, _, err := DecodeTCWF(data)
	if err != nil {
		t.Fatalf("DecodeTCWF: %v", err)
	}
	// MS: 1*16 + ((0*256 + 0*0) >> 8) = 16.
	// IMA: step 7 with code 4 adds 7, so the sum is 23.
	requireSamples(t, audio.Samples, []int16{0, 0, 23})
}

func TestDecodeTCWFAdaptiveCoefficients(t *testing.T) {
	data := tcwfTestFile(1, 8000, 33, 3, []tcwfTestBlock{
		{sample0: 1000, sample1: 2000, idelta: 100, bpred: 1, payload: []byte{0x02}},
	})
	audio, _, err := DecodeTCWF(data)
	if err != nil {
		t.Fatalf("DecodeTCWF: %v", err)
	}
	// predict = (2000*512 + 1000*-256) >> 8 = 3000; current = 2*100 + 3000.
	// The IMA nibble is zero, so it adds no delta at step index 0.
	requireSamples(t, audio.Samples, []int16{1000, 2000, 3200})
}

func TestDecodeTCWFPeakCorrection(t *testing.T) {
	data := tcwfTestFile(1, 8000, 34, 4, []tcwfTestBlock{
		{
			sample0: 100, sample1: 200, idelta: 16,
			payload: []byte{0x00, 0x00},
			peaks: [tcwfPeakCount]tcwfTestPeak{
				{pos: 0, revise: 50},
				{pos: 0, revise: 100},
				{pos: 1, revise: -200},
				{pos: 2, revise: 300},
				{pos: 3, revise: -32768},
				{pos: 65535, revise: 0},
			},
		},
	})
	audio, _, err := DecodeTCWF(data)
	if err != nil {
		t.Fatalf("DecodeTCWF: %v", err)
	}
	// Corrections apply in header order: 100-50=50, 50-100=-50,
	// 200-(-200)=400, 200-300=-100, 200+32768 saturates, zero revision ignored.
	requireSamples(t, audio.Samples, []int16{-50, 400, -100, 32767})
}

func TestDecodeTCWFPeakIsPerChannel(t *testing.T) {
	right := tcwfTestBlock{sample0: 100, sample1: 200}
	right.peaks[0] = tcwfTestPeak{pos: 1, revise: 50}
	left := tcwfTestBlock{sample0: 300, sample1: 400}
	data := tcwfTestFile(2, 8000, tcwfBlockHeaderSize, 2, []tcwfTestBlock{right, left})
	audio, _, err := DecodeTCWF(data)
	if err != nil {
		t.Fatalf("DecodeTCWF: %v", err)
	}
	requireSamples(t, audio.Samples, []int16{100, 300, 150, 400})
}

func TestDecodeTCWFTrailingBytesIgnored(t *testing.T) {
	valid := tcwfTestFile(1, 8000, tcwfBlockHeaderSize, 2, []tcwfTestBlock{{sample0: 7, sample1: 8}})
	padded := append(append([]byte(nil), valid...), 0xde, 0xad, 0xbe, 0xef)
	audio, _, err := DecodeTCWF(padded)
	if err != nil {
		t.Fatalf("DecodeTCWF with trailing bytes: %v", err)
	}
	requireSamples(t, audio.Samples, []int16{7, 8})
}

func TestDecodeTCWFZeroBlocks(t *testing.T) {
	data := tcwfTestFile(1, 8000, tcwfBlockHeaderSize, 2, nil)
	audio, info, err := DecodeTCWF(data)
	if err != nil {
		t.Fatalf("DecodeTCWF: %v", err)
	}
	if len(audio.Samples) != 0 || audio.Frames() != 0 || info.Blocks != 0 {
		t.Fatalf("zero-block decode = %+v, %+v", audio, info)
	}
}

func TestDecodeTCWFErrors(t *testing.T) {
	valid := tcwfTestFile(1, 8000, tcwfBlockHeaderSize, 2, []tcwfTestBlock{{sample0: 1, sample1: 2}})
	badMagic := append([]byte(nil), valid...)
	copy(badMagic, "TCWF1\x1a")

	headerOnly := func(channels byte, rate uint32, blocks uint32, bytesPerBlock, samplesPerBlock uint32) []byte {
		out := make([]byte, tcwfHeaderSize)
		copy(out, tcwfMagic)
		out[6] = channels
		binary.LittleEndian.PutUint32(out[8:12], rate)
		binary.LittleEndian.PutUint32(out[12:16], blocks)
		binary.LittleEndian.PutUint32(out[16:20], bytesPerBlock)
		binary.LittleEndian.PutUint32(out[20:24], samplesPerBlock)
		return out
	}

	tests := []struct {
		name string
		data []byte
		want error
	}{
		{name: "short header", data: valid[:tcwfHeaderSize-1], want: ErrMalformed},
		{name: "bad magic", data: badMagic, want: ErrMalformed},
		{name: "zero channels", data: headerOnly(0, 8000, 1, 32, 2), want: ErrMalformed},
		{name: "unsupported channels", data: headerOnly(3, 8000, 1, 32, 2), want: ErrUnsupported},
		{name: "zero sample rate", data: headerOnly(1, 0, 1, 32, 2), want: ErrMalformed},
		{name: "sample rate above limit", data: headerOnly(1, 768_001, 1, 32, 2), want: ErrMalformed},
		{name: "negative blocks", data: headerOnly(1, 8000, ^uint32(0), 32, 2), want: ErrMalformed},
		{name: "samples per block below two", data: headerOnly(1, 8000, 1, 32, 1), want: ErrMalformed},
		{name: "block smaller than header", data: headerOnly(1, 8000, 1, 31, 2), want: ErrMalformed},
		{name: "payload cannot hold samples", data: headerOnly(1, 8000, 1, 32, 3), want: ErrMalformed},
		{
			name: "truncated block data",
			data: tcwfTestFile(1, 8000, tcwfBlockHeaderSize, 2, []tcwfTestBlock{
				{sample0: 1, sample1: 2},
			})[:tcwfHeaderSize+tcwfBlockHeaderSize-1],
			want: ErrMalformed,
		},
		{
			name: "invalid predictor",
			data: tcwfTestFile(1, 8000, 32, 2, []tcwfTestBlock{{bpred: 7}}),
			want: ErrMalformed,
		},
		{
			name: "invalid IMA step index",
			data: tcwfTestFile(1, 8000, 32, 2, []tcwfTestBlock{{stepIndex: 89}}),
			want: ErrMalformed,
		},
		{
			name: "peak position outside block",
			data: tcwfTestFile(1, 8000, 32, 2, []tcwfTestBlock{{
				peaks: [tcwfPeakCount]tcwfTestPeak{{pos: 2, revise: 1}},
			}}),
			want: ErrMalformed,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := DecodeTCWF(test.data)
			if !errors.Is(err, test.want) {
				t.Fatalf("DecodeTCWF error = %v, want %v", err, test.want)
			}
		})
	}
}
