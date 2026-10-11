package runtime

import (
	"fmt"

	"github.com/eslzzyl/krkrgo/internal/assets"
)

const (
	SoundUnload    = "unload"
	SoundStop      = "stop"
	SoundPlay      = "play"
	fadeBeatMS     = uint64(60)
	maxSoundEvents = 65_536
)

const (
	SoundEventStatus = "onStatusChanged"
	SoundEventLabel  = "onLabel"
)

// SoundEvent is a deferred callback-equivalent emitted in deterministic order.
type SoundEvent struct {
	Generation uint64
	Type       string
	Status     string
	Label      string
}

// WaveSoundBuffer is the headless transport/state portion of Kirikiri's
// WaveSoundBuffer. It owns decoded source state but no device or goroutine.
type WaveSoundBuffer struct {
	loaded        *loadedSound
	status        string
	generation    uint64
	events        []SoundEvent
	frequency     int32
	volume        int32
	volume2       int32
	pan           int32
	paused        bool
	looping       bool
	useVisBuffer  bool
	sampleCount   int
	sampleAhead   int
	sampleConfigInitialized bool
	positionClock uint64
	fade          *soundFade
	fadePending   bool
}

type loadedSound struct {
	audio    assets.Audio
	stream   *SoundStream
	position uint64
	ended    bool
	mixer    MixerSource
}

type soundFade struct {
	target, delta    int32
	remaining, delay uint64
}

// NewWaveSoundBuffer creates an unloaded sound with Kirikiri defaults.
func NewWaveSoundBuffer() *WaveSoundBuffer {
	return &WaveSoundBuffer{status: SoundUnload, volume: 100_000, volume2: 100_000}
}

// Status reports unload, stop, or play.
func (s *WaveSoundBuffer) Status() string {
	if s == nil {
		return SoundUnload
	}
	return s.status
}

// Generation identifies the current transport instance for deferred callbacks.
func (s *WaveSoundBuffer) Generation() uint64 {
	if s == nil {
		return 0
	}
	return s.generation
}
func (s *WaveSoundBuffer) Volume() int32 {
	if s == nil {
		return 0
	}
	return s.volume
}
func (s *WaveSoundBuffer) SetVolume(v int32) {
	if s != nil {
		s.volume = clampSound(v)
	}
}
func (s *WaveSoundBuffer) Volume2() int32 {
	if s == nil {
		return 0
	}
	return s.volume2
}
func (s *WaveSoundBuffer) SetVolume2(v int32) {
	if s != nil {
		s.volume2 = clampSound(v)
	}
}
func (s *WaveSoundBuffer) Pan() int32 {
	if s == nil {
		return 0
	}
	return s.pan
}
func (s *WaveSoundBuffer) SetPan(v int32) {
	if s != nil {
		if v < -100000 {
			v = -100000
		}
		if v > 100000 {
			v = 100000
		}
		s.pan = v
	}
}
func (s *WaveSoundBuffer) Paused() bool { return s != nil && s.paused }
func (s *WaveSoundBuffer) SetPaused(v bool) {
	if s != nil {
		s.paused = v
	}
}
func (s *WaveSoundBuffer) Looping() bool { return s != nil && s.looping }
func (s *WaveSoundBuffer) SetLooping(v bool) {
	if s == nil || s.looping == v {
		return
	}
	s.looping = v
	if s.loaded == nil {
		return
	}
	loaded := s.loaded
	loaded.stream.LoopAtEnd = v
	position := loaded.position
	// Mixer lookahead may have advanced the stream past the audible position.
	// Reconcile both sides before changing the render mode so stale EOF and
	// prefetched samples cannot suppress the newly enabled loop.
	_ = loaded.stream.Seek(position)
	loaded.mixer.ResetAt(position)
	loaded.ended = false
}
func (s *WaveSoundBuffer) Frequency() int32 {
	if s == nil {
		return 0
	}
	return s.frequency
}
func (s *WaveSoundBuffer) SetFrequency(v int32) error {
	if v <= 0 || v > 768000 {
		return fmt.Errorf("sound frequency exceeds supported range")
	}
	if s != nil {
		s.frequency = v
	}
	return nil
}
func (s *WaveSoundBuffer) UseVisBuffer() bool { return s != nil && s.useVisBuffer }
func (s *WaveSoundBuffer) SetUseVisBuffer(v bool) {
	if s != nil {
		s.useVisBuffer = v
	}
}
// configureSampleProperties applies the extension defaults on first use, matching
// the native hook's lazy per-instance state.
func (s *WaveSoundBuffer) configureSampleProperties(count, ahead int) {
	if s == nil || s.sampleConfigInitialized {
		return
	}
	s.sampleCount = count
	s.sampleAhead = ahead
	s.sampleConfigInitialized = true
	s.useVisBuffer = true
}

func (s *WaveSoundBuffer) sampleValue() float64 {
	if s == nil || s.loaded == nil || s.sampleCount <= 0 || len(s.loaded.audio.Samples) == 0 {
		return 0
	}
	audio := s.loaded.audio
	channels := int(audio.Channels)
	if channels <= 0 {
		return 0
	}
	start := int64(s.loaded.position) + int64(s.sampleAhead)
	frames := int64(audio.Frames())
	if start < 0 {
		start = 0
	}
	if start >= frames {
		return 0
	}
	end := start + int64(s.sampleCount)
	if end > frames {
		end = frames
	}
	var max int32
	for frame := start; frame < end; frame++ {
		value := int32(audio.Samples[int(frame)*channels])
		if value < 0 {
			value = -value
		}
		if value > max {
			max = value
		}
	}
	peak := float64(max) / 32768.0
	return peak * peak
}


// OpenAudio installs a decoded PCM source and optional loop metadata.
func (s *WaveSoundBuffer) OpenAudio(audio assets.Audio, info assets.LoopInfo) error {
	if s == nil {
		return fmt.Errorf("nil sound buffer")
	}
	stream, err := NewSoundStream(audio, info)
	if err != nil {
		return err
	}
	stream.LoopAtEnd = s.looping
	emitStopEvent := s.status == SoundUnload
	if err := s.Stop(); err != nil {
		return err
	}
	s.loaded = &loadedSound{audio: audio, stream: stream}
	s.paused = false
	s.frequency = int32(audio.SampleRate)
	s.status = SoundStop
	if emitStopEvent {
		s.pushEvent(SoundEvent{Generation: s.generation, Type: SoundEventStatus, Status: SoundStop})
	}
	return nil
}

// Open is a concise alias for OpenAudio.
func (s *WaveSoundBuffer) Open(audio assets.Audio, info assets.LoopInfo) error {
	return s.OpenAudio(audio, info)
}

// OpenBytes decodes a WAVE/Ogg resource through the assets media boundary.
// Ogg/Vorbis remains an explicit unsupported error when no decoder is linked.
func (s *WaveSoundBuffer) OpenBytes(data []byte, info assets.LoopInfo) error {
	audio, err := assets.ReadAudio(data, 32_000_000)
	if err != nil {
		return err
	}
	return s.OpenAudio(audio, info)
}

// Play starts a loaded source and clears stale source events.
func (s *WaveSoundBuffer) Play() error {
	if s == nil {
		return fmt.Errorf("nil sound buffer")
	}
	if s.status != SoundPlay && s.loaded != nil {
		position := s.loaded.stream.Position()
		if s.loaded.ended || position >= uint64(s.loaded.audio.Frames()) {
			if err := s.loaded.stream.Seek(0); err != nil {
				return err
			}
			position = 0
		}
		s.loaded.ended = false
		s.loaded.mixer.ResetAt(position)
		s.loaded.position = position
		s.generation++
		s.events = s.events[:0]
		s.status = SoundPlay
		s.pushEvent(SoundEvent{Generation: s.generation, Type: SoundEventStatus, Status: SoundPlay})
	}
	return nil
}

// Stop changes transport status and rewinds to source frame zero.
func (s *WaveSoundBuffer) Stop() error {
	if s == nil {
		return fmt.Errorf("nil sound buffer")
	}
	s.generation++
	s.events = s.events[:0]
	if s.status != SoundUnload {
		s.status = SoundStop
		s.pushEvent(SoundEvent{Generation: s.generation, Type: "onStatusChanged", Status: SoundStop})
	}
	if s.loaded != nil {
		if err := s.loaded.stream.Seek(0); err != nil {
			return err
		}
		s.loaded.mixer.ResetAt(0)
		s.loaded.position = 0
		s.loaded.ended = false
	}
	return nil
}

// SetPosition sets playback in milliseconds using the active source rate.
func (s *WaveSoundBuffer) SetPosition(milliseconds uint64) error {
	if s == nil || s.loaded == nil {
		return fmt.Errorf("cannot seek an unloaded sound")
	}
	if s.frequency <= 0 {
		return fmt.Errorf("sound output format is not initialized")
	}
	if milliseconds > ^uint64(0)/uint64(s.frequency) {
		return fmt.Errorf("sound position overflow")
	}
	return s.SetSamplePosition(milliseconds * uint64(s.frequency) / 1000)
}
func (s *WaveSoundBuffer) SetSamplePosition(position uint64) error {
	if s == nil || s.loaded == nil {
		return fmt.Errorf("cannot seek an unloaded sound")
	}
	if position > uint64(s.loaded.audio.Frames()) {
		return nil
	}
	if err := s.loaded.stream.Seek(position); err != nil {
		return err
	}
	s.loaded.mixer.ResetAt(position)
	s.loaded.position = position
	s.loaded.ended = false
	s.generation++
	s.events = s.events[:0]
	return nil
}
func (s *WaveSoundBuffer) Position() uint64 {
	if s == nil || s.loaded == nil || s.frequency <= 0 {
		return 0
	}
	return s.SamplePosition() * 1000 / uint64(s.frequency)
}
func (s *WaveSoundBuffer) SamplePosition() uint64 {
	if s == nil || s.loaded == nil {
		return 0
	}
	return s.loaded.position
}
func (s *WaveSoundBuffer) TotalTime() uint64 {
	if s == nil || s.loaded == nil || s.frequency <= 0 {
		return 0
	}
	return uint64(s.loaded.audio.Frames()) * 1000 / uint64(s.frequency)
}
func (s *WaveSoundBuffer) Channels() uint8 {
	if s == nil || s.loaded == nil {
		return 0
	}
	return s.loaded.audio.Channels
}
func (s *WaveSoundBuffer) Bits() int {
	if s == nil || s.loaded == nil {
		return 0
	}
	return 16
}
func (s *WaveSoundBuffer) Stream() *SoundStream {
	if s == nil || s.loaded == nil {
		return nil
	}
	return s.loaded.stream
}
func (s *WaveSoundBuffer) Labels() []assets.Label {
	if s == nil || s.loaded == nil {
		return nil
	}
	return s.loaded.stream.Labels()
}

// RenderSource pulls source-rate PCM and queues labels/EOF status events.
func (s *WaveSoundBuffer) RenderSource(frames int) (AudioBlock, error) {
	if s == nil || s.loaded == nil {
		return AudioBlock{}, fmt.Errorf("playing sound has no source")
	}
	if s.status != SoundPlay || s.paused || s.loaded.ended {
		return AudioBlock{}, nil
	}
	staged := cloneLoadedSound(s.loaded)
	if staged.stream.Position() != staged.position {
		if err := staged.stream.Seek(staged.position); err != nil {
			return AudioBlock{}, err
		}
	}
	// Direct pulls invalidate any prefetched interpolation queue. Always
	// reset it, including when the stream happened not to be ahead.
	staged.mixer.ResetAt(staged.position)
	block, err := staged.stream.Render(frames)
	if err != nil {
		return AudioBlock{}, err
	}
	staged.position = staged.stream.Position()
	stagedEvents := append([]SoundEvent(nil), s.events...)
	for _, label := range block.Labels {
		stagedEvents = appendBoundedEvent(stagedEvents, SoundEvent{Generation: s.generation, Type: SoundEventLabel, Label: label.Name})
	}
	if len(block.Samples)/int(staged.audio.Channels) < frames {
		staged.ended = true
		stagedEvents = appendBoundedEvent(stagedEvents, SoundEvent{Generation: s.generation, Type: SoundEventStatus, Status: SoundStop})
	}
	*s.loaded = *staged
	s.events = stagedEvents
	if staged.ended {
		s.status = SoundStop
	}
	return block, nil
}

// Render is an alias for RenderSource.
func (s *WaveSoundBuffer) Render(frames int) (AudioBlock, error) { return s.RenderSource(frames) }

// Fade schedules the canonical 60-ms-beat integer fade.
func (s *WaveSoundBuffer) Fade(target, durationMS int32, delayMS ...int32) error {
	if s == nil {
		return fmt.Errorf("nil sound buffer")
	}
	delay := int32(0)
	if len(delayMS) > 0 {
		delay = delayMS[0]
	}
	if durationMS <= 0 || delay < 0 {
		return fmt.Errorf("invalid sound fade duration or delay")
	}
	if s.fade != nil {
		s.fade = nil
		s.pushEvent(SoundEvent{Generation: s.generation, Type: "onFadeCompleted"})
	}
	target = clampSound(target)
	delta := (int64(target) - int64(s.volume)) * int64(fadeBeatMS) / int64(durationMS)
	s.fade = &soundFade{target: target, delta: int32(delta), remaining: uint64(durationMS) / fadeBeatMS, delay: uint64(delay)}
	if durationMS < int32(fadeBeatMS) && delay == 0 {
		s.fade = nil
		s.volume = target
		s.pushEvent(SoundEvent{Generation: s.generation, Type: "onFadeCompleted"})
	}
	return nil
}

// StopFade completes a pending fade immediately.
func (s *WaveSoundBuffer) StopFade() {
	if s != nil && s.fade != nil {
		target := s.fade.target
		s.fade = nil
		s.volume = clampSound(target)
		s.pushEvent(SoundEvent{Generation: s.generation, Type: "onFadeCompleted"})
	}
}

// Advance advances fades by an absolute host clock in milliseconds.
func (s *WaveSoundBuffer) Advance(timeMS uint64) {
	if s == nil {
		return
	}
	beats := timeMS/60 - s.positionClock/60
	s.positionClock = timeMS
	s.advanceBeats(beats)
}
func (s *WaveSoundBuffer) AdvanceBeats(beats uint64) {
	if s != nil {
		s.advanceBeats(beats)
	}
}
func (s *WaveSoundBuffer) advanceBeats(beats uint64) {
	if s.fade == nil {
		return
	}
	delayed := (s.fade.delay + fadeBeatMS - 1) / fadeBeatMS
	if delayed > beats {
		delayed = beats
	}
	if delayed > 0 {
		consumed := delayed * fadeBeatMS
		if consumed >= s.fade.delay {
			s.fade.delay = 0
		} else {
			s.fade.delay -= consumed
		}
		beats -= delayed
	}
	if s.fade.delay == 0 && s.fade.remaining == 0 {
		s.volume = clampSound(s.fade.target)
		s.fade = nil
		s.fadePending = true
		s.pushEvent(SoundEvent{Generation: s.generation, Type: "onFadeCompleted"})
		return
	}
	if beats == 0 || s.fade.remaining == 0 {
		return
	}
	if beats >= s.fade.remaining {
		s.volume = clampSound(s.fade.target)
		s.fade = nil
		s.fadePending = true
		s.pushEvent(SoundEvent{Generation: s.generation, Type: "onFadeCompleted"})
	} else {
		s.volume = clampSound(s.volume + s.fade.delta*int32(beats))
		s.fade.remaining -= beats
	}
}

// DrainEvents removes and returns deferred events in insertion order.
func (s *WaveSoundBuffer) DrainEvents() []SoundEvent {
	if s == nil {
		return nil
	}
	out := s.events
	s.events = nil
	return out
}
func (s *WaveSoundBuffer) Events() []SoundEvent {
	if s == nil {
		return nil
	}
	return append([]SoundEvent(nil), s.events...)
}
func (s *WaveSoundBuffer) pushEvent(event SoundEvent) {
	if len(s.events) < maxSoundEvents {
		s.events = append(s.events, event)
	}
}
func appendBoundedEvent(events []SoundEvent, event SoundEvent) []SoundEvent {
	if len(events) >= maxSoundEvents {
		return events
	}
	return append(events, event)
}
func clampSound(v int32) int32 {
	if v < 0 {
		return 0
	}
	if v > 100000 {
		return 100000
	}
	return v
}
