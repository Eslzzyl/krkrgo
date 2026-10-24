package runtime

import "testing"

func TestMotionPlayerAllPlayingUsesPlayerFlag(t *testing.T) {
	services := &Services{}
	state := &motionPlayerState{playing: true}

	if !services.motionPlayerAllPlaying(state) {
		t.Fatal("allplaying did not reflect the player flag")
	}

	state.playing = false
	if services.motionPlayerAllPlaying(state) {
		t.Fatal("allplaying stayed true after the player flag was cleared")
	}
}

func TestEmotePlayerSkipToSyncUsesActiveTimelineLength(t *testing.T) {
	services := &Services{}
	state := &motionPlayerState{}
	emote := newEmotePlayerState()
	timeline := &emoteTimelineState{
		label:       "once",
		playing:     true,
		totalFrames: 48,
		loop:        false,
	}
	emote.timelines = map[string]*emoteTimelineState{"once": timeline}
	emote.playingOrder = []string{"once"}

	services.emoteSkipToSync(state, emote)

	if timeline.time != 48 || timeline.playing {
		t.Fatalf("timeline after skip = time:%v playing:%v, want 48:false", timeline.time, timeline.playing)
	}
	if len(emote.playingOrder) != 0 {
		t.Fatalf("playing order after skip = %v, want empty", emote.playingOrder)
	}
	if state.clampedEvalTime != 48 || state.frameLoopTime != 48 {
		t.Fatalf("player time after skip = clamped:%v loop:%v, want 48:48", state.clampedEvalTime, state.frameLoopTime)
	}
}

func TestMotionPlayerActiveClipTimeFallsBackToMotionClock(t *testing.T) {
	state := &motionPlayerState{frameTick: 180}
	runtime := &emotePlayerState{timelines: map[string]*emoteTimelineState{}}

	if got := motionPlayerActiveClipTime(state, runtime); got != 180 {
		t.Fatalf("motion clock fallback = %v, want 180", got)
	}

	state.held = true
	runtime.timelines["title"] = &emoteTimelineState{time: 0, totalFrames: 180}
	if got := motionPlayerActiveClipTime(state, runtime); got != 180 {
		t.Fatalf("held motion clock = %v, want 180", got)
	}
}
