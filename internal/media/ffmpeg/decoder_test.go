package ffmpeg

import "testing"

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
