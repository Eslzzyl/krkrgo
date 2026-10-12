package runtime

import "github.com/eslzzyl/krkrgo/internal/media/ffmpeg"

type videoTickSnapshot struct {
	video      *VideoOverlay
	source     *VideoSource
	decoder    *ffmpeg.Decoder
	status     VideoStatus
	positionMS float64
	clockMS    uint64
	clockSet   bool
	current    *VideoFrame
	frames     []VideoFrame
	audio      []VideoAudioBlock
	events     []VideoEvent
	segment    [2]int64
	period     int64
	generation uint64
	looping    bool
	rate       float64
	volume     int32
	balance    int32
	bounds     Rect
	visible    bool
	mode       int32
}

func snapshotVideo(video *VideoOverlay) videoTickSnapshot {
	state := videoTickSnapshot{video: video}
	if video == nil {
		return state
	}
	video.mu.Lock()
	defer video.mu.Unlock()
	state.source = video.source
	state.decoder = video.ffmpeg
	state.status = video.status
	state.positionMS = video.positionMS
	state.clockMS = video.clockMS
	state.clockSet = video.clockSet
	state.segment = video.segment
	state.period = video.period
	state.generation = video.generation
	state.looping = video.looping
	state.rate = video.rate
	state.volume = video.volume
	state.balance = video.balance
	state.bounds = video.bounds
	state.visible = video.visible
	state.mode = video.mode
	state.events = append([]VideoEvent(nil), video.events...)
	state.frames = cloneVideoFrames(video.frameQueue)
	state.audio = cloneVideoAudio(video.audioQueue)
	if video.current != nil {
		current := *video.current
		current.RGBA = append([]byte(nil), current.RGBA...)
		state.current = &current
	}
	return state
}

func restoreVideoSnapshot(state videoTickSnapshot) {
	video := state.video
	if video == nil {
		return
	}
	video.mu.Lock()
	defer video.mu.Unlock()
	if video.ffmpeg != nil && video.ffmpeg != state.decoder {
		_ = video.ffmpeg.Close()
	}
	video.source = state.source
	video.ffmpeg = state.decoder
	video.status = state.status
	video.positionMS = state.positionMS
	video.clockMS = state.clockMS
	video.clockSet = state.clockSet
	video.current = nil
	if state.current != nil {
		current := *state.current
		current.RGBA = append([]byte(nil), state.current.RGBA...)
		video.current = &current
	}
	video.frameQueue = cloneVideoFrames(state.frames)
	video.audioQueue = cloneVideoAudio(state.audio)
	video.events = append(video.events[:0], state.events...)
	video.segment = state.segment
	video.period = state.period
	video.generation = state.generation
	video.looping = state.looping
	video.rate = state.rate
	video.volume = state.volume
	video.balance = state.balance
	video.bounds = state.bounds
	video.visible = state.visible
	video.mode = state.mode
	if video.ffmpeg != nil {
		_ = video.ffmpeg.Seek(uint64(maxFloat64(state.positionMS, 0)))
	}
}

func cloneVideoFrames(frames []VideoFrame) []VideoFrame {
	out := make([]VideoFrame, len(frames))
	for i, frame := range frames {
		out[i] = frame
		out[i].RGBA = append([]byte(nil), frame.RGBA...)
	}
	return out
}

func cloneVideoAudio(blocks []VideoAudioBlock) []VideoAudioBlock {
	out := make([]VideoAudioBlock, len(blocks))
	for i, block := range blocks {
		out[i] = block
		out[i].Samples = append([]float32(nil), block.Samples...)
	}
	return out
}

func maxFloat64(value, minimum float64) float64 {
	if value < minimum {
		return minimum
	}
	return value
}
