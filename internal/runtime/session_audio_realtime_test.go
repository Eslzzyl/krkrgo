package runtime

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/eslzzyl/krkrgo/internal/assets"
	"github.com/eslzzyl/krkrgo/internal/tjs"
)

func TestRealtimeAudioMovesMixingOutOfTick(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()

	sound := NewWaveSoundBuffer()
	samples := make([]int16, 4800)
	for i := range samples {
		samples[i] = int16(i%200 - 100)
	}
	if err := sound.OpenAudio(assets.Audio{Channels: 1, SampleRate: 44_100, Samples: samples}, assets.LoopInfo{}); err != nil {
		t.Fatal(err)
	}
	if err := sound.Play(); err != nil {
		t.Fatal(err)
	}
	if err := session.Services.RegisterSound(1, sound); err != nil {
		t.Fatal(err)
	}

	session.EnableRealtimeAudio()
	if err := session.Tick(100); err != nil {
		t.Fatal(err)
	}
	if len(session.AudioOutput) != 0 {
		t.Fatalf("tick produced %d realtime audio samples", len(session.AudioOutput))
	}
	if position := sound.SamplePosition(); position != 0 {
		t.Fatalf("tick advanced realtime sound position to %d", position)
	}

	out, err := session.MixRealtimeAudio(960)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1920 {
		t.Fatalf("realtime mix returned %d samples, want 1920", len(out))
	}
	if position := sound.SamplePosition(); position == 0 {
		t.Fatalf("realtime mix did not advance the sound: status=%s freq=%d", sound.Status(), sound.Frequency())
	}

	session.DisableRealtimeAudio()
	if err := session.Tick(200); err != nil {
		t.Fatal(err)
	}
	if len(session.AudioOutput) == 0 {
		t.Fatal("tick produced no audio after realtime audio was disabled")
	}
}

func TestRealtimeAudioMixesWhileSoundOpens(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()
	if _, err := session.Evaluate("sound = new WaveSoundBuffer()"); err != nil {
		t.Fatal(err)
	}
	session.EnableRealtimeAudio()

	stop := make(chan struct{})
	mixErr := make(chan error, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := session.MixRealtimeAudio(64); err != nil {
				select {
				case mixErr <- err:
				default:
				}
				return
			}
		}
	}()

	source := "sound.openBytes(" + tjsOctetLiteral(testTCWFMonoFixture()) + ")"
	for range 20 {
		if _, err := session.Evaluate(source); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()
	select {
	case err := <-mixErr:
		t.Fatal(err)
	default:
	}
}

func TestRealtimeAudioPositionExcludesQueuedLead(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()
	sound := NewWaveSoundBuffer()
	samples := make([]int16, 48_000)
	for i := range samples {
		samples[i] = int16(i % 200)
	}
	if err := sound.OpenAudio(assets.Audio{Channels: 1, SampleRate: 48_000, Samples: samples}, assets.LoopInfo{}); err != nil {
		t.Fatal(err)
	}
	sound.SetLooping(true)
	if err := sound.Play(); err != nil {
		t.Fatal(err)
	}
	if err := session.Services.RegisterSound(1, sound); err != nil {
		t.Fatal(err)
	}
	session.EnableRealtimeAudio()
	if _, err := session.MixRealtimeAudio(960); err != nil {
		t.Fatal(err)
	}
	raw := sound.SamplePosition()
	if raw == 0 {
		t.Fatal("mixer did not advance the sound")
	}
	if audible := session.Services.playbackSamplePosition(sound); audible >= raw {
		t.Fatalf("audible position %d did not trail mixer position %d", audible, raw)
	}
	session.SetAudioPlayedFrames(960)
	if audible := session.Services.playbackSamplePosition(sound); audible != raw {
		t.Fatalf("audible position after full playback = %d, want %d", audible, raw)
	}
}

func TestRealtimeAudioDefersEventsUntilAudible(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()
	sound := NewWaveSoundBuffer()
	samples := make([]int16, 480)
	if err := sound.OpenAudio(assets.Audio{Channels: 1, SampleRate: 48_000, Samples: samples}, assets.LoopInfo{}); err != nil {
		t.Fatal(err)
	}
	if err := sound.Play(); err != nil {
		t.Fatal(err)
	}
	if err := session.Services.RegisterSound(1, sound); err != nil {
		t.Fatal(err)
	}
	const id = tjs.ObjectID(1)
	session.EnableRealtimeAudio()
	if _, err := session.MixRealtimeAudio(960); err != nil {
		t.Fatal(err)
	}
	if err := session.Services.queueSoundEvents(id, sound); err != nil {
		t.Fatal(err)
	}
	hasStop := func(events []SoundEvent) bool {
		for _, event := range events {
			if event.Type == SoundEventStatus && event.Status == SoundStop {
				return true
			}
		}
		return false
	}
	if hasStop(session.Services.SoundEvents[id]) || !hasStop(sound.Events()) {
		t.Fatalf("stop event fired before the device position: queued=%v pending=%v", session.Services.SoundEvents[id], sound.Events())
	}
	if status := sound.Status(); status != SoundPlay {
		t.Fatalf("status before the device position = %q, want %q", status, SoundPlay)
	}
	session.SetAudioPlayedFrames(960)
	if err := session.Services.queueSoundEvents(id, sound); err != nil {
		t.Fatal(err)
	}
	if !hasStop(session.Services.SoundEvents[id]) || hasStop(sound.Events()) {
		t.Fatalf("stop event did not fire after playback: queued=%v pending=%v", session.Services.SoundEvents[id], sound.Events())
	}
	if status := sound.Status(); status != SoundStop {
		t.Fatalf("status after the device position = %q, want %q", status, SoundStop)
	}
}

func TestRealtimeAudioOpensSoundsAsynchronously(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()
	if _, err := session.Evaluate("sound = new WaveSoundBuffer()"); err != nil {
		t.Fatal(err)
	}
	value, err := session.Evaluate("sound")
	if err != nil {
		t.Fatal(err)
	}
	sound := session.Services.Sound(value.ObjectRef().Object)
	if sound == nil {
		t.Fatal("sound object was not registered")
	}
	session.EnableRealtimeAudio()
	source := "sound.openBytes(" + tjsOctetLiteral(testTCWFMonoFixture()) + ")"
	if _, err := session.Evaluate(source); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Evaluate("sound.play()"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		session.Services.audioMu.Lock()
		loaded := sound.loaded != nil
		status := sound.Status()
		session.Services.audioMu.Unlock()
		if loaded && status == SoundPlay {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("asynchronous open did not commit: loaded=%t status=%q", loaded, status)
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := session.Evaluate(`sound.open("missing.wav")`); !errors.Is(err, ErrUnsupportedDevice) {
		t.Fatalf("missing source error = %v, want ErrUnsupportedDevice", err)
	}
}
