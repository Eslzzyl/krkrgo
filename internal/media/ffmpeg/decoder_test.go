package ffmpeg

import (
	"runtime"
	"testing"
	"unsafe"
)

func TestQueueVideoFrameRetainsEarliestPendingFrames(t *testing.T) {
	decoder := &Decoder{}
	for index := maxPendingFrames + 2; index >= 0; index-- {
		decoder.queueVideoFrame(Frame{PTSMS: uint64(index)})
	}

	if len(decoder.pending) != maxPendingFrames {
		t.Fatalf("pending frame count = %d, want %d", len(decoder.pending), maxPendingFrames)
	}
	for index, frame := range decoder.pending {
		if frame.PTSMS != uint64(index) {
			t.Fatalf("pending[%d].PTSMS = %d, want %d", index, frame.PTSMS, index)
		}
	}
}

func TestDecodePacketRetriesWhenSendReturnsAgain(t *testing.T) {
	decoder, state, frame, sample := newFakeAudioDecoder()
	sendCalls, receiveCalls := 0, 0
	decoder.packet = 1
	decoder.api.avcodecSendPacket = func(uintptr, uintptr) int32 {
		sendCalls++
		if sendCalls == 1 {
			return avErrorAgain
		}
		return 0
	}
	decoder.api.avcodecReceiveFrame = func(uintptr, uintptr) int32 {
		receiveCalls++
		if receiveCalls == 1 {
			*sample = 0.25
			return 0
		}
		return avErrorAgain
	}

	if err := decoder.decodePacket(state); err != nil {
		t.Fatalf("decode packet = %v", err)
	}
	if sendCalls != 2 {
		t.Fatalf("send calls = %d, want retry after EAGAIN", sendCalls)
	}
	if len(decoder.audioOut) != 1 || decoder.audioOut[0].Samples[0] != 0.25 {
		t.Fatalf("decoded audio = %+v, want the frame received before retry", decoder.audioOut)
	}
	runtime.KeepAlive(frame)
	runtime.KeepAlive(sample)
}

func TestDrainRetriesNullPacketAfterEAGAIN(t *testing.T) {
	decoder, _, frame, sample := newFakeAudioDecoder()
	sendCalls, receiveCalls := 0, 0
	decoder.api.avcodecSendPacket = func(uintptr, uintptr) int32 {
		sendCalls++
		if sendCalls == 1 {
			return avErrorAgain
		}
		return 0
	}
	decoder.api.avcodecReceiveFrame = func(uintptr, uintptr) int32 {
		receiveCalls++
		switch receiveCalls {
		case 1:
			*sample = 0.25
			return 0
		case 2:
			return avErrorAgain
		case 3:
			*sample = 0.5
			return 0
		default:
			return avErrorEOF
		}
	}

	if err := decoder.drain(); err != nil {
		t.Fatalf("drain = %v", err)
	}
	if sendCalls != 2 {
		t.Fatalf("null packet send calls = %d, want retry after EAGAIN", sendCalls)
	}
	if !decoder.drained {
		t.Fatal("decoder was not marked drained")
	}
	if len(decoder.audioOut) != 2 || decoder.audioOut[0].Samples[0] != 0.25 || decoder.audioOut[1].Samples[0] != 0.5 {
		t.Fatalf("drained audio = %+v, want both pending frames", decoder.audioOut)
	}
	runtime.KeepAlive(frame)
	runtime.KeepAlive(sample)
}

func TestReceiveFramesCoalescesOversizedAudioBurst(t *testing.T) {
	decoder, state, frame, sample := newFakeAudioDecoder()
	const blocks = MaxAudioBlocks + 19
	receiveCalls := 0
	decoder.api.avcodecReceiveFrame = func(uintptr, uintptr) int32 {
		receiveCalls++
		if receiveCalls > blocks {
			return avErrorAgain
		}
		*sample = float32(receiveCalls)
		return 0
	}

	received, eof, err := decoder.receiveFrames(state)
	if err != nil {
		t.Fatalf("receive audio burst = %v", err)
	}
	if eof || received != blocks {
		t.Fatalf("receive status = (%d, eof=%t), want (%d, eof=false)", received, eof, blocks)
	}
	if len(decoder.audioOut) > MaxAudioBlocks {
		t.Fatalf("audio block count = %d, exceeds limit %d", len(decoder.audioOut), MaxAudioBlocks)
	}
	got := 0
	for _, block := range decoder.audioOut {
		for _, sample := range block.Samples {
			got++
			if sample != float32(got) {
				t.Fatalf("decoded sample %d = %g, want %d", got, sample, got)
			}
		}
	}
	if got != blocks {
		t.Fatalf("decoded sample count = %d, want %d", got, blocks)
	}
	runtime.KeepAlive(frame)
	runtime.KeepAlive(sample)
}

func newFakeAudioDecoder() (*Decoder, *streamState, *avFrameABI, *float32) {
	frame := new(avFrameABI)
	sample := new(float32)
	frame.Data[0] = uintptr(unsafe.Pointer(sample))
	frame.SampleCount = 1
	frame.Format = sampleFormatFLT
	state := &streamState{
		context:    1,
		frame:      uintptr(unsafe.Pointer(frame)),
		channels:   1,
		sampleRate: 48000,
	}
	decoder := &Decoder{api: &api{avFrameUnref: func(uintptr) {}}, audio: state}
	return decoder, state, frame, sample
}
