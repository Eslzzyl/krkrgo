package runtime

import (
	"encoding/binary"
	"testing"

	"github.com/eslzzyl/krkrgo/internal/assets"
)

const (
	testTCWFHeaderSize      = 24
	testTCWFBlockHeaderSize = 32
)

type testTCWFBlock struct {
	sample0 int16
	sample1 int16
}

// testTCWFFixture builds fixed-size blocks with a zero predictor and zero
// nibble payload, so each block renders sample0 followed by repeated sample1.
func testTCWFFixture(channels uint8, rate uint32, samplesPerBlock, bytesPerBlock int, blocks []testTCWFBlock) []byte {
	if channels == 0 || len(blocks)%int(channels) != 0 {
		panic("testTCWFFixture: block list must contain whole block groups")
	}
	out := make([]byte, testTCWFHeaderSize, testTCWFHeaderSize+len(blocks)*bytesPerBlock)
	copy(out, "TCWF0\x1a")
	out[6] = channels
	binary.LittleEndian.PutUint32(out[8:12], rate)
	binary.LittleEndian.PutUint32(out[12:16], uint32(len(blocks)/int(channels)))
	binary.LittleEndian.PutUint32(out[16:20], uint32(bytesPerBlock))
	binary.LittleEndian.PutUint32(out[20:24], uint32(samplesPerBlock))
	for _, block := range blocks {
		encoded := make([]byte, bytesPerBlock)
		binary.LittleEndian.PutUint16(encoded[0:2], uint16(block.sample0))
		binary.LittleEndian.PutUint16(encoded[2:4], uint16(block.sample1))
		binary.LittleEndian.PutUint16(encoded[4:6], 16)
		out = append(out, encoded...)
	}
	return out
}

func testTCWFMonoFixture() []byte {
	return testTCWFFixture(1, 8000, 8, 38, []testTCWFBlock{
		{sample0: 10, sample1: 20},
		{sample0: 30, sample1: 40},
		{sample0: 50, sample1: 60},
	})
}

func testTCWFMonoExpected() []int16 {
	seeds := [][2]int16{{10, 20}, {30, 40}, {50, 60}}
	out := make([]int16, 0, len(seeds)*8)
	for _, seed := range seeds {
		out = append(out, seed[0])
		for range 7 {
			out = append(out, seed[1])
		}
	}
	return out
}

func requireRuntimeSamples(t *testing.T, got []int16, want []int16) {
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

func TestWaveSoundBufferOpenBytesTCWF(t *testing.T) {
	sound := NewWaveSoundBuffer()
	info := assets.LoopInfo{Labels: []assets.Label{{Position: 4, Name: "ignored"}}}
	if err := sound.OpenBytes(testTCWFMonoFixture(), info); err != nil {
		t.Fatalf("OpenBytes TCWF: %v", err)
	}
	if sound.Status() != SoundStop {
		t.Fatalf("status = %q, want %q", sound.Status(), SoundStop)
	}
	if sound.Channels() != 1 || sound.Bits() != 16 || sound.Frequency() != 8000 {
		t.Fatalf("format = %dch %dbit %dHz", sound.Channels(), sound.Bits(), sound.Frequency())
	}
	if frames := sound.Stream().Audio().Frames(); frames != 24 {
		t.Fatalf("decoded frames = %d, want 24", frames)
	}
	if sound.TotalTime() != 3 {
		t.Fatalf("total time = %dms, want 3ms", sound.TotalTime())
	}
	// TCWF carries no loop metadata, so caller metadata is not applied.
	if labels := sound.Labels(); len(labels) != 0 {
		t.Fatalf("TCWF labels = %v, want none", labels)
	}

	if err := sound.Play(); err != nil {
		t.Fatalf("play: %v", err)
	}
	sound.DrainEvents()
	want := testTCWFMonoExpected()
	offset := 0
	for _, pull := range []int{8, 1, 7, 9} {
		block, err := sound.RenderSource(pull)
		if err != nil {
			t.Fatalf("render %d frames: %v", pull, err)
		}
		end := minInt(offset+len(block.Samples), len(want))
		requireRuntimeSamples(t, block.Samples, want[offset:end])
		offset += len(block.Samples)
	}
	if offset != len(want) {
		t.Fatalf("rendered %d samples, want %d", offset, len(want))
	}
	if sound.Status() != SoundStop {
		t.Fatalf("status after EOF = %q, want %q", sound.Status(), SoundStop)
	}
	if sound.SamplePosition() != uint64(len(want)) {
		t.Fatalf("position after EOF = %d, want %d", sound.SamplePosition(), len(want))
	}
	events := sound.Events()
	if len(events) != 1 || events[0].Type != SoundEventStatus || events[0].Status != SoundStop {
		t.Fatalf("EOF events = %v", events)
	}
}

func TestWaveSoundBufferOpenBytesTCWFStereo(t *testing.T) {
	data := testTCWFFixture(2, 44_100, 2, testTCWFBlockHeaderSize, []testTCWFBlock{
		{sample0: 100, sample1: 200},
		{sample0: 300, sample1: 400},
	})
	sound := NewWaveSoundBuffer()
	if err := sound.OpenBytes(data, assets.LoopInfo{}); err != nil {
		t.Fatalf("OpenBytes stereo TCWF: %v", err)
	}
	if sound.Channels() != 2 || sound.Frequency() != 44_100 {
		t.Fatalf("format = %dch %dHz", sound.Channels(), sound.Frequency())
	}
	if frames := sound.Stream().Audio().Frames(); frames != 2 {
		t.Fatalf("stereo frames = %d, want 2", frames)
	}
	requireRuntimeSamples(t, sound.Stream().Audio().Samples, []int16{100, 300, 200, 400})
}

func TestSoundStreamTCWFBlockAndSampleBoundaries(t *testing.T) {
	audio, info, err := assets.DecodeTCWF(testTCWFMonoFixture())
	if err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	want := testTCWFMonoExpected()

	whole, err := NewSoundStream(audio, assets.LoopInfo{})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	block, err := whole.Render(len(want))
	if err != nil {
		t.Fatalf("render all: %v", err)
	}
	requireRuntimeSamples(t, block.Samples, want)

	cases := []struct {
		name   string
		seek   uint64
		frames int
		want   []int16
	}{
		{name: "block zero", seek: 0, frames: info.SamplesPerBlock, want: want[0:8]},
		{name: "block one", seek: 8, frames: 8, want: want[8:16]},
		{name: "block two", seek: 16, frames: 8, want: want[16:24]},
		{name: "last sample of block", seek: 7, frames: 1, want: want[7:8]},
		{name: "inside block", seek: 9, frames: 4, want: want[9:13]},
		{name: "final sample", seek: 23, frames: 4, want: want[23:24]},
		{name: "end of stream", seek: 24, frames: 4, want: nil},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			stream, err := NewSoundStream(audio, assets.LoopInfo{})
			if err != nil {
				t.Fatalf("stream: %v", err)
			}
			if err := stream.Seek(test.seek); err != nil {
				t.Fatalf("seek %d: %v", test.seek, err)
			}
			block, err := stream.Render(test.frames)
			if err != nil {
				t.Fatalf("render after seek %d: %v", test.seek, err)
			}
			requireRuntimeSamples(t, block.Samples, test.want)
		})
	}

	stream, err := NewSoundStream(audio, assets.LoopInfo{})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if err := stream.Seek(25); err == nil {
		t.Fatal("seek past the last frame unexpectedly succeeded")
	}
	if stream.Position() != 0 {
		t.Fatalf("failed seek moved position to %d", stream.Position())
	}

	// Splitting exactly at block boundaries must equal one large pull.
	split, err := NewSoundStream(audio, assets.LoopInfo{})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	var joined []int16
	for range info.Blocks {
		part, err := split.Render(info.SamplesPerBlock)
		if err != nil {
			t.Fatalf("split render: %v", err)
		}
		joined = append(joined, part.Samples...)
	}
	requireRuntimeSamples(t, joined, want)
	short, err := split.Render(1)
	if err != nil {
		t.Fatalf("render after end: %v", err)
	}
	if len(short.Samples) != 0 {
		t.Fatalf("render after end = %v, want empty", short.Samples)
	}
}
