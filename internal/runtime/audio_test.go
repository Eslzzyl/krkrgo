package runtime

import (
	"testing"

	"github.com/eslzzyl/krkrgo/internal/assets"
)

func testStereoAudio() assets.Audio {
	return assets.Audio{Channels: 2, SampleRate: 80, Samples: []int16{0, 0, 100, -100, 200, -200, 300, -300, 400, -400, 500, -500, 600, -600, 700, -700, 800, -800, 900, -900}}
}

func TestAudioBlockKeepsUnkeyedCompatibility(t *testing.T) {
	block := AudioBlock{[]int16{1}, []AudioLabel{{Offset: 0, Name: "start"}}}
	if len(block.Samples) != 1 || len(block.Labels) != 1 {
		t.Fatalf("unexpected audio block=%+v", block)
	}
}

func TestSoundStreamLoopLabelsAndSplitPulls(t *testing.T) {
	info := assets.LoopInfo{Links: []assets.Link{{From: 6, To: 2}}, Labels: []assets.Label{{Position: 3, Name: "mouth"}}}
	whole, err := NewSoundStream(testStereoAudio(), info)
	if err != nil {
		t.Fatal(err)
	}
	want, err := whole.Render(12)
	if err != nil {
		t.Fatal(err)
	}
	if got := []int16{want.Samples[0], want.Samples[2], want.Samples[4], want.Samples[6], want.Samples[8], want.Samples[10], want.Samples[12]}; len(got) != 7 || got[6] != 200 {
		t.Fatalf("unexpected loop samples: %v", got)
	}
	if len(want.Labels) != 3 || want.Labels[0].Offset != 3 || want.Labels[1].Offset != 7 || want.Labels[2].Offset != 11 {
		t.Fatalf("unexpected labels: %+v", want.Labels)
	}

	split, err := NewSoundStream(testStereoAudio(), info)
	if err != nil {
		t.Fatal(err)
	}
	var got []int16
	for remaining := 12; remaining > 0; {
		n := 2
		if n > remaining {
			n = remaining
		}
		block, e := split.Render(n)
		if e != nil {
			t.Fatal(e)
		}
		got = append(got, block.Samples...)
		remaining -= n
	}
	if len(got) != len(want.Samples) {
		t.Fatalf("split length %d, whole %d", len(got), len(want.Samples))
	}
	for i := range got {
		if got[i] != want.Samples[i] {
			t.Fatalf("split sample %d = %d, whole %d", i, got[i], want.Samples[i])
		}
	}
}

func TestAudioMixerStereoResampleAndBound(t *testing.T) {
	sound := NewWaveSoundBuffer()
	if err := sound.OpenAudio(assets.Audio{Channels: 1, SampleRate: 48_000, Samples: []int16{32767, 32767, 32767}}, assets.LoopInfo{}); err != nil {
		t.Fatal(err)
	}
	if err := sound.Play(); err != nil {
		t.Fatal(err)
	}
	mixer := NewAudioMixer()
	mixer.Add(sound)
	out, err := mixer.Mix(2)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 4 || out[0] <= 0.99 || out[1] <= 0.99 {
		t.Fatalf("unexpected stereo output: %v", out)
	}
	if _, err := mixer.Mix(MaxAudioBlockFrames + 1); err == nil {
		t.Fatal("unbounded mixer tick accepted")
	}
}

func TestSessionAudioDeliveryMixesBoundedTick(t *testing.T) {
	services := DefaultServices()
	session := NewSession(services, 100)
	sound := NewWaveSoundBuffer()
	if err := sound.OpenAudio(assets.Audio{Channels: 1, SampleRate: OutputRate, Samples: deliverySamples(2_000)}, assets.LoopInfo{}); err != nil {
		t.Fatal(err)
	}
	if err := sound.Play(); err != nil {
		t.Fatal(err)
	}
	if err := services.RegisterSound(1, sound); err != nil {
		t.Fatal(err)
	}
	if err := session.Tick(20); err != nil {
		t.Fatal(err)
	}
	out := session.TakeAudio()
	if len(out) != 960*2 {
		t.Fatalf("20 ms output samples=%d, want %d", len(out), 960*2)
	}
	nonzero := false
	for _, sample := range out {
		if sample != 0 {
			nonzero = true
			break
		}
	}
	if !nonzero {
		t.Fatal("playing sound produced silent output")
	}
}

func TestSessionAudioDeliverySilenceAndPartitioning(t *testing.T) {
	services := DefaultServices()
	session := NewSession(services, 100)
	sound := NewWaveSoundBuffer()
	if err := sound.OpenAudio(assets.Audio{Channels: 1, SampleRate: OutputRate, Samples: deliverySamples(2_000)}, assets.LoopInfo{}); err != nil {
		t.Fatal(err)
	}
	if err := sound.Play(); err != nil {
		t.Fatal(err)
	}
	sound.SetPaused(true)
	if err := services.RegisterSound(1, sound); err != nil {
		t.Fatal(err)
	}
	if err := session.Tick(20); err != nil {
		t.Fatal(err)
	}
	paused := session.TakeAudio()
	if len(paused) != 960*2 || !allAudioZero(paused) {
		t.Fatalf("paused output len=%d samples=%v", len(paused), paused[:minAudioTestInt(len(paused), 4)])
	}

	empty := NewSession(DefaultServices(), 100)
	if err := empty.Tick(20); err != nil {
		t.Fatal(err)
	}
	if out := empty.TakeAudio(); len(out) != 960*2 || !allAudioZero(out) {
		t.Fatalf("empty output len=%d", len(out))
	}

	partitionedServices := DefaultServices()
	partitioned := NewSession(partitionedServices, 100)
	partitionedSound := NewWaveSoundBuffer()
	if err := partitionedSound.OpenAudio(assets.Audio{Channels: 1, SampleRate: OutputRate, Samples: deliverySamples(2_000)}, assets.LoopInfo{}); err != nil {
		t.Fatal(err)
	}
	if err := partitionedSound.Play(); err != nil {
		t.Fatal(err)
	}
	if err := partitionedServices.RegisterSound(1, partitionedSound); err != nil {
		t.Fatal(err)
	}
	if err := partitioned.Tick(10); err != nil {
		t.Fatal(err)
	}
	first := partitioned.TakeAudio()
	if err := partitioned.Tick(20); err != nil {
		t.Fatal(err)
	}
	second := partitioned.TakeAudio()
	if len(first) != 480*2 || len(second) != 480*2 {
		t.Fatalf("partitioned output lengths=%d,%d", len(first), len(second))
	}
}

func TestSessionAudioDeliveryRejectsOversizedTickWithoutMutation(t *testing.T) {
	session := NewSession(DefaultServices(), 100)
	if err := session.Tick(20); err != nil {
		t.Fatal(err)
	}
	want := append([]float32(nil), session.AudioOutput...)
	err := session.Tick(session.Services.TimeMS + uint64(MaxAudioBlockFrames/48+1))
	if err == nil {
		t.Fatal("oversized audio tick accepted")
	}
	if session.Services.TimeMS != 20 {
		t.Fatalf("clock advanced after oversized tick: %d", session.Services.TimeMS)
	}
	if len(session.AudioOutput) != len(want) {
		t.Fatalf("audio output length changed from %d to %d", len(want), len(session.AudioOutput))
	}
	for i := range want {
		if session.AudioOutput[i] != want[i] {
			t.Fatalf("audio output changed at %d", i)
		}
	}
}

func deliverySamples(frames int) []int16 {
	samples := make([]int16, frames)
	for i := range samples {
		samples[i] = 16_384
	}
	return samples
}

func allAudioZero(samples []float32) bool {
	for _, sample := range samples {
		if sample != 0 {
			return false
		}
	}
	return true
}

func minAudioTestInt(value, limit int) int {
	if value < limit {
		return value
	}
	return limit
}

func TestSoundFadeAndOptions(t *testing.T) {
	sound := NewWaveSoundBuffer()
	if err := sound.Fade(0, 180, 0); err != nil {
		t.Fatal(err)
	}
	sound.Advance(60)
	if sound.Volume() != 66667 {
		t.Fatalf("volume after first beat = %d", sound.Volume())
	}
	sound.Advance(180)
	if sound.Volume() != 0 {
		t.Fatalf("volume after fade = %d", sound.Volume())
	}
	options := NewSoundOptions()
	args := []int{1, 80, 1, 0, 20, 1, 70, 1, 1, 30, 1, 60, 1, 0, 40}
	if err := options.Init(args...); err != nil {
		t.Fatal(err)
	}
	state, err := options.GetState()
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{80, 0, 20, 70, 1, 30, 60, 0, 40}
	for i := range want {
		if state[i] != want[i] {
			t.Fatalf("option %d = %d, want %d", i, state[i], want[i])
		}
	}
}

func TestSoundEOFStopsAndReplaysFromFrameZero(t *testing.T) {
	sound := NewWaveSoundBuffer()
	if err := sound.OpenAudio(assets.Audio{Channels: 1, SampleRate: OutputRate, Samples: []int16{1000, 2000}}, assets.LoopInfo{}); err != nil {
		t.Fatal(err)
	}
	if err := sound.Play(); err != nil {
		t.Fatal(err)
	}
	sound.DrainEvents()
	mixer := NewAudioMixer()
	mixer.Add(sound)
	if _, err := mixer.Mix(1); err != nil {
		t.Fatal(err)
	}
	if _, err := mixer.Mix(1); err != nil {
		t.Fatal(err)
	}
	if sound.Status() != SoundStop {
		t.Fatalf("natural EOF status=%q", sound.Status())
	}
	out, err := mixer.Mix(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || out[0] != 0 || out[1] != 0 {
		t.Fatalf("stopped output=%v", out)
	}
	if err := sound.Play(); err != nil {
		t.Fatal(err)
	}
	out, err = mixer.Mix(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || out[0] < 0.03 || out[0] > 0.04 || out[1] < 0.03 || out[1] > 0.04 {
		t.Fatalf("replayed output=%v", out)
	}
}

func TestSoundLoopingInvalidatesMixerLookahead(t *testing.T) {
	sound := NewWaveSoundBuffer()
	if err := sound.OpenAudio(assets.Audio{Channels: 1, SampleRate: OutputRate, Samples: []int16{1000, 2000, 3000}}, assets.LoopInfo{}); err != nil {
		t.Fatal(err)
	}
	if err := sound.Play(); err != nil {
		t.Fatal(err)
	}
	mixer := NewAudioMixer()
	mixer.Add(sound)
	if _, err := mixer.Mix(2); err != nil {
		t.Fatal(err)
	}
	if sound.Status() != SoundPlay || sound.SamplePosition() != 2 {
		t.Fatalf("lookahead changed playback state: status=%q position=%d", sound.Status(), sound.SamplePosition())
	}
	sound.SetLooping(true)
	if sound.Stream().Position() != sound.SamplePosition() {
		t.Fatalf("looping left stream ahead: stream=%d audible=%d", sound.Stream().Position(), sound.SamplePosition())
	}
	if _, err := mixer.Mix(2); err != nil {
		t.Fatal(err)
	}
	out := mixer.output
	if len(out) != 4 || out[0] < 0.09 || out[0] > 0.10 || out[1] < 0.09 || out[1] > 0.10 || out[2] < 0.03 || out[2] > 0.04 || out[3] < 0.03 || out[3] > 0.04 {
		t.Fatalf("looped output after mode change=%v", out)
	}
	if sound.Status() != SoundPlay {
		t.Fatalf("looping stopped source after mode change: %q", sound.Status())
	}
}

func TestSoundRenderSourceEOFStopsImmediately(t *testing.T) {
	sound := NewWaveSoundBuffer()
	if err := sound.OpenAudio(assets.Audio{Channels: 1, SampleRate: OutputRate, Samples: []int16{1000}}, assets.LoopInfo{}); err != nil {
		t.Fatal(err)
	}
	if err := sound.Play(); err != nil {
		t.Fatal(err)
	}
	sound.DrainEvents()
	block, err := sound.RenderSource(2)
	if err != nil {
		t.Fatal(err)
	}
	if len(block.Samples) != 1 || sound.Status() != SoundStop {
		t.Fatalf("block=%v status=%q", block.Samples, sound.Status())
	}
}

func TestAudioMixerUsesEffectiveSoundFrequency(t *testing.T) {
	sound := NewWaveSoundBuffer()
	if err := sound.OpenAudio(assets.Audio{Channels: 1, SampleRate: OutputRate, Samples: []int16{0, 10000, 20000, 30000}}, assets.LoopInfo{}); err != nil {
		t.Fatal(err)
	}
	if err := sound.SetFrequency(OutputRate / 2); err != nil {
		t.Fatal(err)
	}
	if err := sound.Play(); err != nil {
		t.Fatal(err)
	}
	if sound.Frequency() != OutputRate/2 {
		t.Fatalf("frequency reset on Play: %d", sound.Frequency())
	}
	out, err := func() ([]float32, error) {
		mixer := NewAudioMixer()
		mixer.Add(sound)
		return mixer.Mix(2)
	}()
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 4 || out[2] < 0.1 || out[2] > 0.2 || out[3] < 0.1 || out[3] > 0.2 {
		t.Fatalf("effective frequency output=%v", out)
	}
}

func TestAudioMixerRejectsMultichannelWithoutConsuming(t *testing.T) {
	sound := NewWaveSoundBuffer()
	if err := sound.OpenAudio(assets.Audio{Channels: 3, SampleRate: OutputRate, Samples: []int16{1, 2, 3, 4, 5, 6}}, assets.LoopInfo{}); err != nil {
		t.Fatal(err)
	}
	if err := sound.Play(); err != nil {
		t.Fatal(err)
	}
	mixer := NewAudioMixer()
	mixer.Add(sound)
	if _, err := mixer.Mix(1); err == nil {
		t.Fatal("multichannel source was accepted")
	}
	if got := sound.SamplePosition(); got != 0 {
		t.Fatalf("multichannel source consumed %d frames", got)
	}
}

func TestAudioMixerFailureIsAtomicAndRetryable(t *testing.T) {
	valid := NewWaveSoundBuffer()
	if err := valid.OpenAudio(assets.Audio{Channels: 1, SampleRate: OutputRate, Samples: []int16{16_384, 16_384, 16_384, 16_384}}, assets.LoopInfo{}); err != nil {
		t.Fatal(err)
	}
	if err := valid.Play(); err != nil {
		t.Fatal(err)
	}
	mixer := NewAudioMixer()
	mixer.Add(valid)
	first, err := mixer.Mix(1)
	if err != nil {
		t.Fatal(err)
	}
	wantOutput := append([]float32(nil), first...)
	if err := valid.SetSamplePosition(0); err != nil {
		t.Fatal(err)
	}
	bad := NewWaveSoundBuffer()
	if err := bad.OpenAudio(assets.Audio{Channels: 3, SampleRate: OutputRate, Samples: []int16{1, 2, 3, 4, 5, 6}}, assets.LoopInfo{}); err != nil {
		t.Fatal(err)
	}
	if err := bad.Play(); err != nil {
		t.Fatal(err)
	}
	mixer.Add(bad)
	if _, err := mixer.Mix(1); err == nil {
		t.Fatal("multichannel source was accepted")
	}
	if valid.SamplePosition() != 0 || bad.SamplePosition() != 0 {
		t.Fatalf("failed mix consumed sources: valid=%d bad=%d", valid.SamplePosition(), bad.SamplePosition())
	}
	for i, sample := range mixer.output {
		if sample != wantOutput[i] {
			t.Fatalf("failed mix changed output at %d: got %v want %v", i, sample, wantOutput[i])
		}
	}
	mixer.Remove(bad)
	if _, err := mixer.Mix(1); err != nil {
		t.Fatal(err)
	}
	if valid.SamplePosition() != 1 {
		t.Fatalf("retry consumed %d frames, want 1", valid.SamplePosition())
	}
}

func TestSoundPlaybackPositionTracksAudibleFrames(t *testing.T) {
	sound := NewWaveSoundBuffer()
	if err := sound.OpenAudio(assets.Audio{Channels: 1, SampleRate: 96_000, Samples: []int16{1000, 2000, 3000, 4000, 5000, 6000, 7000, 8000, 9000, 10000}}, assets.LoopInfo{}); err != nil {
		t.Fatal(err)
	}
	if err := sound.Play(); err != nil {
		t.Fatal(err)
	}
	mixer := NewAudioMixer()
	mixer.Add(sound)
	if _, err := mixer.Mix(1); err != nil {
		t.Fatal(err)
	}
	if got := sound.SamplePosition(); got != 2 {
		t.Fatalf("mixed playback position=%d, want 2", got)
	}
	if err := sound.SetSamplePosition(3); err != nil {
		t.Fatal(err)
	}
	if got := sound.SamplePosition(); got != 3 {
		t.Fatalf("seek playback position=%d, want 3", got)
	}
	if _, err := mixer.Mix(1); err != nil {
		t.Fatal(err)
	}
	if got := sound.SamplePosition(); got != 5 {
		t.Fatalf("post-seek playback position=%d, want 5", got)
	}
}

func TestSoundRenderSourceUpdatesPlaybackPosition(t *testing.T) {
	sound := NewWaveSoundBuffer()
	if err := sound.OpenAudio(assets.Audio{Channels: 1, SampleRate: OutputRate, Samples: []int16{1000, 2000}}, assets.LoopInfo{}); err != nil {
		t.Fatal(err)
	}
	if err := sound.Play(); err != nil {
		t.Fatal(err)
	}
	if _, err := sound.RenderSource(1); err != nil {
		t.Fatal(err)
	}
	if got := sound.SamplePosition(); got != 1 {
		t.Fatalf("direct render playback position=%d, want 1", got)
	}
}

func TestDelayedSubBeatFadeCompletesAfterDelay(t *testing.T) {
	sound := NewWaveSoundBuffer()
	if err := sound.Fade(0, 30, 100); err != nil {
		t.Fatal(err)
	}
	sound.Advance(60)
	if sound.Volume() != 100_000 || len(sound.Events()) != 0 {
		t.Fatalf("fade completed before delay: volume=%d events=%v", sound.Volume(), sound.Events())
	}
	sound.Advance(120)
	if sound.Volume() != 0 || sound.fade != nil {
		t.Fatalf("delayed sub-beat fade state: volume=%d fade=%v", sound.Volume(), sound.fade)
	}
	events := sound.Events()
	if len(events) != 1 || events[0].Type != "onFadeCompleted" {
		t.Fatalf("completion events=%v", events)
	}
}

func TestRejectedMultichannelTickPreservesSessionState(t *testing.T) {
	services := DefaultServices()
	session := NewSession(services, 3)
	session.AudioOutput = []float32{0.25, -0.5}
	sound := NewWaveSoundBuffer()
	if err := sound.OpenAudio(assets.Audio{Channels: 3, SampleRate: OutputRate, Samples: []int16{1, 2, 3, 4, 5, 6}}, assets.LoopInfo{}); err != nil {
		t.Fatal(err)
	}
	if err := sound.Play(); err != nil {
		t.Fatal(err)
	}
	if err := services.RegisterSound(1, sound); err != nil {
		t.Fatal(err)
	}
	if err := session.Tick(1); err == nil {
		t.Fatal("multichannel tick was accepted")
	}
	if session.Budget != 3 || services.TimeMS != 0 || len(session.AudioOutput) != 2 || session.AudioOutput[0] != 0.25 || session.AudioOutput[1] != -0.5 || sound.SamplePosition() != 0 {
		t.Fatalf("rejected tick mutated state: budget=%d time=%d audio=%v position=%d", session.Budget, services.TimeMS, session.AudioOutput, sound.SamplePosition())
	}
}

func TestOpenAudioMalformedReplacementPreservesPlayback(t *testing.T) {
	sound := NewWaveSoundBuffer()
	if err := sound.OpenAudio(assets.Audio{Channels: 1, SampleRate: OutputRate, Samples: []int16{1000, 2000}}, assets.LoopInfo{}); err != nil {
		t.Fatal(err)
	}
	if err := sound.Play(); err != nil {
		t.Fatal(err)
	}
	oldStream, oldStatus, oldGeneration, oldEvents := sound.Stream(), sound.Status(), sound.Generation(), sound.Events()
	if err := sound.OpenAudio(assets.Audio{Channels: 1, SampleRate: 0, Samples: []int16{3000}}, assets.LoopInfo{}); err == nil {
		t.Fatal("malformed replacement accepted")
	}
	if sound.Stream() != oldStream || sound.Status() != oldStatus || sound.Generation() != oldGeneration {
		t.Fatalf("replacement mutated source/status/generation")
	}
	gotEvents := sound.Events()
	if len(gotEvents) != len(oldEvents) {
		t.Fatalf("pending events changed from %d to %d", len(oldEvents), len(gotEvents))
	}
	for i := range oldEvents {
		if gotEvents[i] != oldEvents[i] {
			t.Fatalf("pending event %d changed: %#v -> %#v", i, oldEvents[i], gotEvents[i])
		}
	}
}

func TestSoundStreamSmoothLinkSplitPulls(t *testing.T) {
	samples := make([]int16, 100)
	for i := range samples {
		samples[i] = int16(i)
	}
	info := assets.LoopInfo{Links: []assets.Link{{From: 60, To: 20, Smooth: true, Variable: -1}}, Labels: []assets.Label{{Position: 20, Name: "loop"}, {Position: 59, Name: "tail"}}}
	whole, err := NewSoundStream(assets.Audio{Channels: 1, SampleRate: 1000, Samples: samples}, info)
	if err != nil {
		t.Fatal(err)
	}
	want, err := whole.Render(50)
	if err != nil {
		t.Fatal(err)
	}
	split, err := NewSoundStream(assets.Audio{Channels: 1, SampleRate: 1000, Samples: samples}, info)
	if err != nil {
		t.Fatal(err)
	}
	var got []int16
	var labels []AudioLabel
	for remaining := 50; remaining > 0; {
		block, renderErr := split.Render(1)
		if renderErr != nil {
			t.Fatal(renderErr)
		}
		base := len(got)
		got = append(got, block.Samples...)
		for _, label := range block.Labels {
			labels = append(labels, AudioLabel{Offset: base + label.Offset, Name: label.Name})
		}
		remaining--
	}
	if len(got) != len(want.Samples) {
		t.Fatalf("split length %d, whole %d", len(got), len(want.Samples))
	}
	for i := range got {
		if got[i] != want.Samples[i] {
			t.Fatalf("split sample %d = %d, whole %d", i, got[i], want.Samples[i])
		}
	}
	if len(labels) != len(want.Labels) {
		t.Fatalf("split labels=%v, whole=%v", labels, want.Labels)
	}
	for i := range labels {
		if labels[i] != want.Labels[i] {
			t.Fatalf("split label %d = %#v, whole %#v", i, labels[i], want.Labels[i])
		}
	}
}

func TestSoundStreamSmoothFadeIgnoresDisabledFollowingLink(t *testing.T) {
	samples := make([]int16, 100)
	info := assets.LoopInfo{Links: []assets.Link{
		{From: 30, To: 10, Variable: 0, Condition: assets.ConditionEqual, Reference: 1},
		{From: 60, To: 20, Smooth: true, Variable: -1},
	}}
	stream, err := NewSoundStream(assets.Audio{Channels: 1, SampleRate: 1000, Samples: samples}, info)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Render(70); err != nil {
		t.Fatal(err)
	}
	if got := stream.Position(); got != 70 {
		t.Fatalf("smooth fade position with disabled link=%d, want 70", got)
	}
}

func TestSoundStreamLabelsPreserveFileOrderAndIsolation(t *testing.T) {
	info := assets.LoopInfo{Labels: []assets.Label{{Position: 3, Name: "third"}, {Position: 1, Name: "first"}, {Position: 2, Name: "second"}}}
	stream, err := NewSoundStream(assets.Audio{Channels: 1, SampleRate: OutputRate, Samples: []int16{1, 2, 3, 4}}, info)
	if err != nil {
		t.Fatal(err)
	}
	labels := stream.Labels()
	labels[0] = assets.Label{Position: 99, Name: "changed"}
	beforeRender := stream.Labels()
	if len(beforeRender) != 3 || beforeRender[0].Name != "third" || beforeRender[0].Position != 3 {
		t.Fatalf("labels changed through caller copy: %#v", beforeRender)
	}
	if _, err := stream.Render(4); err != nil {
		t.Fatal(err)
	}
	labels = stream.Labels()
	if len(labels) != 3 || labels[0].Name != "third" || labels[1].Name != "first" || labels[2].Name != "second" {
		t.Fatalf("labels changed file order: %#v", labels)
	}
	labels[1].Name = "changed after render"
	afterRender := stream.Labels()
	if afterRender[1].Name != "first" {
		t.Fatalf("labels changed after render through caller copy: %#v", afterRender)
	}
}

func TestSoundRenderSourceReconcilesMixerPrefetch(t *testing.T) {
	sound := NewWaveSoundBuffer()
	if err := sound.OpenAudio(assets.Audio{Channels: 1, SampleRate: 96_000, Samples: []int16{10, 20, 30, 40, 50, 60}}, assets.LoopInfo{}); err != nil {
		t.Fatal(err)
	}
	if err := sound.Play(); err != nil {
		t.Fatal(err)
	}
	mixer := NewAudioMixer()
	mixer.Add(sound)
	if _, err := mixer.Mix(1); err != nil {
		t.Fatal(err)
	}
	block, err := sound.RenderSource(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(block.Samples) != 1 || block.Samples[0] != 30 || sound.SamplePosition() != 3 {
		t.Fatalf("direct render after mix block=%v position=%d", block.Samples, sound.SamplePosition())
	}
}

func TestSoundRenderSourceErrorIsTransactional(t *testing.T) {
	sound := NewWaveSoundBuffer()
	if err := sound.OpenAudio(assets.Audio{Channels: 1, SampleRate: OutputRate, Samples: []int16{10, 20}}, assets.LoopInfo{}); err != nil {
		t.Fatal(err)
	}
	if err := sound.Play(); err != nil {
		t.Fatal(err)
	}
	sound.DrainEvents()
	if _, err := sound.RenderSource(MaxAudioBlockFrames + 1); err == nil {
		t.Fatal("oversized direct render accepted")
	}
	if sound.SamplePosition() != 0 || sound.Stream().Position() != 0 || len(sound.Events()) != 0 || sound.Status() != SoundPlay {
		t.Fatalf("failed direct render mutated state: position=%d stream=%d events=%v status=%q", sound.SamplePosition(), sound.Stream().Position(), sound.Events(), sound.Status())
	}
	block, err := sound.RenderSource(1)
	if err != nil || len(block.Samples) != 1 || block.Samples[0] != 10 {
		t.Fatalf("retry block=%v err=%v", block.Samples, err)
	}
}

func TestAudioMixerClampsLogicalEOFPosition(t *testing.T) {
	sound := NewWaveSoundBuffer()
	if err := sound.OpenAudio(assets.Audio{Channels: 1, SampleRate: 1000, Samples: []int16{1000, 2000}}, assets.LoopInfo{}); err != nil {
		t.Fatal(err)
	}
	if err := sound.Play(); err != nil {
		t.Fatal(err)
	}
	mixer := NewAudioMixer()
	mixer.Add(sound)
	out, err := mixer.Mix(1000)
	if err != nil {
		t.Fatal(err)
	}
	if sound.SamplePosition() > 2 || sound.Position() != sound.TotalTime() || sound.Status() != SoundStop {
		t.Fatalf("EOF position=%d total=%d time=%d/%d status=%q", sound.SamplePosition(), 2, sound.Position(), sound.TotalTime(), sound.Status())
	}
	if len(out) < 4 || !allAudioZero(out[len(out)-4:]) {
		t.Fatalf("post-EOF output was not silence: %v", out[len(out)-4:])
	}
}

func TestAudioMixerLoopingSourceKeepsAudibleTimeline(t *testing.T) {
	sound := NewWaveSoundBuffer()
	if err := sound.OpenAudio(assets.Audio{Channels: 1, SampleRate: OutputRate, Samples: []int16{16_384}}, assets.LoopInfo{}); err != nil {
		t.Fatal(err)
	}
	sound.SetLooping(true)
	if err := sound.Play(); err != nil {
		t.Fatal(err)
	}
	mixer := NewAudioMixer()
	mixer.Add(sound)
	out, err := mixer.Mix(2)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 4 || out[0] < 0.49 || out[2] < 0.49 || out[1] < 0.49 || out[3] < 0.49 {
		t.Fatalf("looping output=%v", out)
	}
	if sound.Status() != SoundPlay || sound.SamplePosition() != 0 {
		t.Fatalf("looping state status=%q position=%d", sound.Status(), sound.SamplePosition())
	}
}

func TestAudioMixerNonmatchingLinkStopsAtTotalTime(t *testing.T) {
	sound := NewWaveSoundBuffer()
	info := assets.LoopInfo{Links: []assets.Link{{From: 1, To: 0, Variable: 0, Condition: assets.ConditionEqual, Reference: 1}}}
	if err := sound.OpenAudio(assets.Audio{Channels: 1, SampleRate: OutputRate, Samples: []int16{1000, 2000}}, info); err != nil {
		t.Fatal(err)
	}
	if err := sound.Play(); err != nil {
		t.Fatal(err)
	}
	mixer := NewAudioMixer()
	mixer.Add(sound)
	if _, err := mixer.Mix(4); err != nil {
		t.Fatal(err)
	}
	if sound.SamplePosition() != 2 || sound.Position() != sound.TotalTime() || sound.Status() != SoundStop {
		t.Fatalf("nonmatching link EOF position=%d time=%d/%d status=%q", sound.SamplePosition(), sound.Position(), sound.TotalTime(), sound.Status())
	}
}

func TestAudioMixerSeekPastLinkStopsAtTotalTime(t *testing.T) {
	sound := NewWaveSoundBuffer()
	info := assets.LoopInfo{Links: []assets.Link{{From: 2, To: 0}}}
	if err := sound.OpenAudio(assets.Audio{Channels: 1, SampleRate: OutputRate, Samples: []int16{1000, 2000, 3000, 4000}}, info); err != nil {
		t.Fatal(err)
	}
	if err := sound.Play(); err != nil {
		t.Fatal(err)
	}
	if err := sound.SetSamplePosition(3); err != nil {
		t.Fatal(err)
	}
	mixer := NewAudioMixer()
	mixer.Add(sound)
	if _, err := mixer.Mix(2); err != nil {
		t.Fatal(err)
	}
	if sound.SamplePosition() != 4 || sound.Position() != sound.TotalTime() || sound.Status() != SoundStop {
		t.Fatalf("seek-past-link EOF position=%d time=%d/%d status=%q", sound.SamplePosition(), sound.Position(), sound.TotalTime(), sound.Status())
	}
}

func TestOpenAudioReplacementQueuesOneStopEvent(t *testing.T) {
	sound := NewWaveSoundBuffer()
	audio := assets.Audio{Channels: 1, SampleRate: OutputRate, Samples: []int16{1000, 2000}}
	if err := sound.OpenAudio(audio, assets.LoopInfo{}); err != nil {
		t.Fatal(err)
	}
	sound.DrainEvents()
	if err := sound.Play(); err != nil {
		t.Fatal(err)
	}
	sound.DrainEvents()
	if err := sound.OpenAudio(audio, assets.LoopInfo{}); err != nil {
		t.Fatal(err)
	}
	events := sound.Events()
	if len(events) != 1 || events[0].Type != SoundEventStatus || events[0].Status != SoundStop {
		t.Fatalf("replacement events=%v, want one stop status event", events)
	}
}

func TestRegisteredSoundFadeUsesSessionClock(t *testing.T) {
	services := DefaultServices()
	session := NewSession(services, 100)
	if err := session.Tick(120); err != nil {
		t.Fatal(err)
	}
	sound := NewWaveSoundBuffer()
	if err := services.RegisterSound(1, sound); err != nil {
		t.Fatal(err)
	}
	if err := sound.Fade(0, 120); err != nil {
		t.Fatal(err)
	}
	if err := session.Tick(180); err != nil {
		t.Fatal(err)
	}
	if got := sound.Volume(); got != 50_000 {
		t.Fatalf("registered fade volume=%d, want 50000", got)
	}
}

func TestFadeClampsOverflowTargetBeforeArithmetic(t *testing.T) {
	sound := NewWaveSoundBuffer()
	if err := sound.Fade(int32(^uint32(0)>>1), 120); err != nil {
		t.Fatal(err)
	}
	sound.AdvanceBeats(1)
	if got := sound.Volume(); got != 100_000 {
		t.Fatalf("overflow target first beat volume=%d", got)
	}
	sound.AdvanceBeats(1)
	if got := sound.Volume(); got != 100_000 || sound.fade != nil {
		t.Fatalf("overflow target completion volume=%d fade=%v", got, sound.fade)
	}
	events := sound.Events()
	if len(events) != 1 || events[0].Type != "onFadeCompleted" {
		t.Fatalf("overflow target events=%v", events)
	}
}

func TestAudioMixerTracksSLIJumpPosition(t *testing.T) {
	sound := NewWaveSoundBuffer()
	info := assets.LoopInfo{Links: []assets.Link{{From: 3, To: 1, Variable: -1}}}
	audio := assets.Audio{Channels: 1, SampleRate: 96_000, Samples: []int16{1000, 2000, 3000, 4000, 5000, 6000}}
	if err := sound.OpenAudio(audio, info); err != nil {
		t.Fatal(err)
	}
	if err := sound.Play(); err != nil {
		t.Fatal(err)
	}
	mixer := NewAudioMixer()
	mixer.Add(sound)
	out, err := mixer.Mix(2)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 4 || out[2] < 0.09 || out[2] > 0.1 {
		t.Fatalf("jump output=%v, want second frame from source 2", out)
	}
	if got := sound.SamplePosition(); got != 2 {
		t.Fatalf("jump playback position=%d, want source 2", got)
	}
	block, err := sound.RenderSource(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(block.Samples) != 1 || block.Samples[0] != 3000 {
		t.Fatalf("direct render after jump=%v, want source sample 3000", block.Samples)
	}
}

func TestAudioMixerInvalidatesPendingConditionalLinkOnFlagChange(t *testing.T) {
	sound := NewWaveSoundBuffer()
	info := assets.LoopInfo{Links: []assets.Link{{From: 3, To: 1, Variable: 0, Condition: assets.ConditionEqual, Reference: 1}}}
	audio := assets.Audio{Channels: 1, SampleRate: 96_000, Samples: []int16{1000, 2000, 3000, 4000, 5000, 6000}}
	if err := sound.OpenAudio(audio, info); err != nil {
		t.Fatal(err)
	}
	if err := sound.Stream().SetFlag(0, 1); err != nil {
		t.Fatal(err)
	}
	if err := sound.Play(); err != nil {
		t.Fatal(err)
	}
	mixer := NewAudioMixer()
	mixer.Add(sound)
	if _, err := mixer.Mix(1); err != nil {
		t.Fatal(err)
	}
	if got := sound.SamplePosition(); got != 2 {
		t.Fatalf("conditional loop position=%d, want 2", got)
	}
	if err := sound.Stream().SetFlag(0, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := mixer.Mix(1); err != nil {
		t.Fatal(err)
	}
	out, err := mixer.Mix(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || out[0] < 0.15 || out[0] > 0.16 {
		t.Fatalf("flag-toggle output=%v, want source sample 5000 after link disabled", out)
	}
}
