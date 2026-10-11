package host

import (
	"encoding/binary"
	"errors"
	"io"
	"math"
	"testing"
)

func TestAudioOutputQueueFormatBackpressureAndClose(t *testing.T) {
	output := &AudioOutput{queue: newAudioQueue(2)}
	if err := output.Write([]float32{1}); !errors.Is(err, ErrAudioOutputFormat) {
		t.Fatalf("odd sample block error = %v, want %v", err, ErrAudioOutputFormat)
	}
	input := []float32{1, -1, 0.25, -0.5}
	if err := output.Write(input); err != nil {
		t.Fatal(err)
	}
	if got := output.PendingFrames(); got != 2 {
		t.Fatalf("pending frames = %d, want 2", got)
	}
	if err := output.Write([]float32{3, 4}); !errors.Is(err, ErrAudioOutputBackpressure) {
		t.Fatalf("full queue error = %v, want %v", err, ErrAudioOutputBackpressure)
	}

	partial := make([]byte, 4)
	if n, err := output.queue.Read(partial); err != nil || n != len(partial) {
		t.Fatalf("partial read = (%d, %v), want (%d, nil)", n, err, len(partial))
	}
	if got := math.Float32frombits(binary.LittleEndian.Uint32(partial)); got != input[0] {
		t.Fatalf("first sample = %v, want %v", got, input[0])
	}
	remaining := make([]byte, 12)
	if n, err := output.queue.Read(remaining); err != nil || n != len(remaining) {
		t.Fatalf("remaining read = (%d, %v), want (%d, nil)", n, err, len(remaining))
	}
	for i := range 3 {
		got := math.Float32frombits(binary.LittleEndian.Uint32(remaining[i*4:]))
		if got != input[i+1] {
			t.Fatalf("sample %d = %v, want %v", i+1, got, input[i+1])
		}
	}

	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	if err := output.Write([]float32{0, 0}); !errors.Is(err, ErrAudioOutputClosed) {
		t.Fatalf("closed write error = %v, want %v", err, ErrAudioOutputClosed)
	}
	if n, err := output.queue.Read(make([]byte, 8)); n != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("closed read = (%d, %v), want (0, EOF)", n, err)
	}
}
func TestAudioOutputDiagnosticsCountAcceptedPCM(t *testing.T) {
	output := &AudioOutput{queue: newAudioQueue(2), diagnostics: true}
	if err := output.Write([]float32{0, 0.5, -0.25, 0}); err != nil {
		t.Fatal(err)
	}
	if err := output.Write([]float32{1, 1}); !errors.Is(err, ErrAudioOutputBackpressure) {
		t.Fatalf("full queue error = %v, want %v", err, ErrAudioOutputBackpressure)
	}
	if got, want := output.Diagnostics(), (AudioOutputStats{Blocks: 1, Frames: 2, NonZeroSamples: 2, Peak: 0.5}); got != want {
		t.Fatalf("diagnostics = %+v, want %+v", got, want)
	}
}

func TestAudioOutputDiagnosticsDisabled(t *testing.T) {
	output := &AudioOutput{queue: newAudioQueue(1)}
	if err := output.Write([]float32{0.5, -0.5}); err != nil {
		t.Fatal(err)
	}
	if got := output.Diagnostics(); got != (AudioOutputStats{}) {
		t.Fatalf("disabled diagnostics = %+v, want zero stats", got)
	}
}
