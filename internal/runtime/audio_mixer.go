package runtime

import (
	"fmt"
	"math"

	"github.com/eslzzyl/krkrgo/internal/assets"
)

const (
	// OutputRate is the fixed host mixer rate.
	OutputRate     = 48_000
	maxMixerFrames = MaxAudioBlockFrames
)

// MixerSource is the bounded streaming resampler state associated with one
// source. It intentionally stores only a small pending PCM queue and never
// accumulates an entire decoded file.
type MixerSource struct {
	phase            uint64
	position         uint64
	timeline         uint64
	decoded          uint64
	pending          [][2]float32
	pendingPositions []uint64
	labels           []pendingLabel
	eof              bool
	linkLoop         bool
	flagEpoch        uint64
}

type pendingLabel struct {
	at    uint64
	label AudioLabel
}

func (s MixerSource) clone() MixerSource {
	clone := s
	clone.pending = append([][2]float32(nil), s.pending...)
	clone.pendingPositions = append([]uint64(nil), s.pendingPositions...)
	clone.labels = append([]pendingLabel(nil), s.labels...)
	return clone
}

// Reset discards interpolation history and pending source data.
func (s *MixerSource) Reset() { *s = MixerSource{} }

func (s *MixerSource) ResetAt(position uint64) {
	if s == nil {
		return
	}
	epoch := s.flagEpoch
	*s = MixerSource{position: position, timeline: position, decoded: position, flagEpoch: epoch}
}

// Position reports the logical source frame consumed by the output clock.
func (s *MixerSource) Position() uint64 {
	if s == nil {
		return 0
	}
	return s.position
}

// AudioMixer mixes WaveSoundBuffers into bounded interleaved stereo output.
type AudioMixer struct {
	GlobalVolume int32
	sources      []*WaveSoundBuffer
	output       []float32
}

// NewAudioMixer creates a mixer with full global volume.
func NewAudioMixer() *AudioMixer { return &AudioMixer{GlobalVolume: 100_000} }

// GetGlobalVolume reports the deterministic headless mix gain.
func (m *AudioMixer) GetGlobalVolume() int32 {
	if m == nil {
		return 0
	}
	return clampSound(m.GlobalVolume)
}

// SetGlobalVolume updates the deterministic headless mix gain.
func (m *AudioMixer) SetGlobalVolume(v int32) {
	if m != nil {
		m.GlobalVolume = clampSound(v)
	}
}

// Add registers a source once. Nil sources are ignored.
func (m *AudioMixer) Add(source *WaveSoundBuffer) {
	if m == nil || source == nil {
		return
	}
	for _, current := range m.sources {
		if current == source {
			return
		}
	}
	m.sources = append(m.sources, source)
}

// Remove unregisters a source.
func (m *AudioMixer) Remove(source *WaveSoundBuffer) {
	if m == nil {
		return
	}
	for i, current := range m.sources {
		if current == source {
			copy(m.sources[i:], m.sources[i+1:])
			m.sources[len(m.sources)-1] = nil
			m.sources = m.sources[:len(m.sources)-1]
			return
		}
	}
}

// Mix renders frames at exactly 48 kHz and replaces (rather than appends to)
// the previous output. The returned slice is owned by the mixer and remains
// valid until the next Mix call.
func (m *AudioMixer) Mix(frames int) ([]float32, error) {
	if m == nil {
		return nil, fmt.Errorf("nil audio mixer")
	}
	if frames < 0 || frames > maxMixerFrames {
		return nil, fmt.Errorf("audio tick exceeds frame limit")
	}
	if frames > math.MaxInt/2 {
		return nil, fmt.Errorf("audio output size overflow")
	}
	output := make([]float32, frames*2)
	if frames == 0 {
		m.output = output
		return m.output, nil
	}
	global := clampGain(m.GlobalVolume)
	type stagedSource struct {
		source *WaveSoundBuffer
		loaded *loadedSound
		status string
		events []SoundEvent
	}
	staged := make([]stagedSource, 0, len(m.sources))
	for _, source := range m.sources {
		if source == nil || source.status != SoundPlay || source.paused || source.loaded == nil || source.loaded.ended {
			continue
		}
		if source.frequency <= 0 {
			continue
		}
		loaded := cloneLoadedSound(source.loaded)
		events := append([]SoundEvent(nil), source.events...)
		volume := clampGain(source.volume) * clampGain(source.volume2) * global
		gain := [2]float32{volume * (1 - maxFloat(float32(source.pan)/100000, 0)), volume * (1 + minFloat(float32(source.pan)/100000, 0))}
		labels, ended, err := loaded.mix(output, gain, uint32(source.frequency))
		if err != nil {
			return nil, err
		}
		neededEvents := len(labels)
		if ended {
			neededEvents++
		}
		if neededEvents > maxSoundEvents-len(events) {
			return nil, fmt.Errorf("sound event queue limit exceeded")
		}
		for _, label := range labels {
			events = append(events, SoundEvent{Generation: source.generation, Type: SoundEventLabel, Label: label.Name})
		}
		status := source.status
		if ended {
			status = SoundStop
			events = append(events, SoundEvent{Generation: source.generation, Type: SoundEventStatus, Status: string(SoundStop)})
			loaded.ended = true
		}
		staged = append(staged, stagedSource{source: source, loaded: loaded, status: status, events: events})
	}
	for i := range output {
		if output[i] < -1 {
			output[i] = -1
		}
		if output[i] > 1 {
			output[i] = 1
		}
	}
	for _, state := range staged {
		*state.source.loaded = *state.loaded
		state.source.status = state.status
		state.source.events = state.events
	}
	m.output = output
	return m.output, nil
}

func cloneLoadedSound(source *loadedSound) *loadedSound {
	if source == nil {
		return nil
	}
	clone := *source
	clone.stream = cloneSoundStream(source.stream)
	clone.mixer = source.mixer.clone()
	return &clone
}

func cloneSoundStream(source *SoundStream) *SoundStream {
	if source == nil {
		return nil
	}
	clone := *source
	clone.info.Links = append([]assets.Link(nil), source.info.Links...)
	clone.info.Labels = append([]assets.Label(nil), source.info.Labels...)
	clone.sortedLabels = append([]assets.Label(nil), source.sortedLabels...)
	if source.crossfade != nil {
		fade := *source.crossfade
		fade.samples = append([]int16(nil), source.crossfade.samples...)
		clone.crossfade = &fade
	}
	return &clone
}

// Render is an alias for Mix, useful to hosts that own the output clock.
func (m *AudioMixer) Render(frames int) ([]float32, error) { return m.Mix(frames) }

// MixMilliseconds renders a bounded whole-millisecond host tick.
func (m *AudioMixer) MixMilliseconds(milliseconds uint64) ([]float32, error) {
	if milliseconds > uint64(maxMixerFrames/48) {
		return nil, fmt.Errorf("audio tick exceeds frame limit")
	}
	return m.Mix(int(milliseconds * 48))
}

// MixSource mixes one stream directly into target. It is public for hosts that
// do not use WaveSoundBuffer, while WaveSoundBuffer uses the same state below.
func (s *MixerSource) MixSource(stream *SoundStream, frames int, frequency uint32, target []float32, gain [2]float32) ([]AudioLabel, bool, error) {
	if s == nil || stream == nil {
		return nil, false, fmt.Errorf("nil audio source")
	}
	if frames < 0 || frames > maxMixerFrames || len(target) < frames*2 {
		return nil, false, fmt.Errorf("invalid mixer output bounds")
	}
	if frequency == 0 || frequency > 768_000 {
		return nil, false, fmt.Errorf("sound frequency exceeds supported range")
	}
	target = target[:frames*2]
	total := uint64(stream.audio.Frames())
	if s.flagEpoch != stream.flagEpoch {
		position := s.position
		phase := s.phase
		if err := stream.Seek(position); err != nil {
			return nil, false, err
		}
		*s = MixerSource{phase: phase, position: position, flagEpoch: stream.flagEpoch}
	}
	looping := total != 0 && !s.eof && (stream.LoopAtEnd || (!stream.IgnoreLinks && s.linkLoop))
	if !s.linkLoop && !stream.IgnoreLinks && stream.info.NextLink(stream.Position(), &stream.flags) != nil {
		s.linkLoop = true
		looping = total != 0 && !s.eof
	}
	if !looping && s.timeline > total {
		s.timeline = total
		s.position = total
		s.phase = 0
	}
	end := s.phase + uint64(frames)*uint64(frequency)
	advance := end / OutputRate
	if advance > maxMixerFrames-2 || s.timeline > ^uint64(0)-advance-2 {
		return nil, false, fmt.Errorf("resampled audio tick exceeds source frame limit")
	}
	needed := s.timeline + advance + 2
	if needed > s.decoded && !s.eof {
		requested := needed - s.decoded
		if requested > maxMixerFrames {
			return nil, false, fmt.Errorf("resampled audio tick exceeds source frame limit")
		}
		channels := int(stream.audio.Channels)
		if channels > 2 {
			return nil, false, fmt.Errorf("unsupported native audio mixing of more than two source channels")
		}
		rendered, err := stream.render(int(requested))
		if err != nil {
			return nil, false, err
		}
		block := rendered.AudioBlock
		got := uint64(len(block.Samples) / channels)
		if len(rendered.sourcePositions) != int(got) {
			return nil, false, fmt.Errorf("audio source frame mapping is incomplete")
		}
		for i := 0; i+channels <= len(block.Samples); i += channels {
			left := float32(block.Samples[i]) / 32768
			right := left
			if channels == 2 {
				right = float32(block.Samples[i+1]) / 32768
			}
			s.pending = append(s.pending, [2]float32{left, right})
		}
		s.pendingPositions = append(s.pendingPositions, rendered.sourcePositions...)
		for _, label := range block.Labels {
			s.labels = append(s.labels, pendingLabel{at: s.decoded + uint64(label.Offset), label: label})
		}
		s.decoded += got
		if got < requested {
			s.eof = true
		}
	}
	// A short source render is authoritative: a metadata link that did not
	// match the current flags (or was already behind a seek) is not a loop.
	looping = looping && !s.eof
	if s.eof {
		s.linkLoop = false
	}
	base := s.decoded - uint64(len(s.pending))
	for i := range frames {
		local := s.timeline - base
		frac := float32(s.phase) / float32(OutputRate)
		var sample [2]float32
		if (looping || s.timeline < total) && local < uint64(len(s.pending)) {
			sample = s.pending[local]
		}
		if (looping || s.timeline < total) && local+1 < uint64(len(s.pending)) {
			next := s.pending[local+1]
			sample[0] += (next[0] - sample[0]) * frac
			sample[1] += (next[1] - sample[1]) * frac
		}
		target[i*2] += sample[0] * gain[0]
		target[i*2+1] += sample[1] * gain[1]
		s.phase += uint64(frequency)
		step := s.phase / OutputRate
		s.phase %= OutputRate
		s.timeline += step
		if looping {
			s.position = s.sourcePositionAt(s.timeline, total)
		} else if s.timeline >= total {
			s.position = total
			s.phase = 0
		} else {
			s.position = s.sourcePositionAt(s.timeline, total)
		}
	}
	if s.timeline > base {
		drop := len(s.pending)
		if distance := s.timeline - base; distance < uint64(drop) {
			drop = int(distance)
		}
		copy(s.pending, s.pending[drop:])
		s.pending = s.pending[:len(s.pending)-drop]
		copy(s.pendingPositions, s.pendingPositions[drop:])
		s.pendingPositions = s.pendingPositions[:len(s.pendingPositions)-drop]
	}
	labels := make([]AudioLabel, 0, len(s.labels))
	outPosition := s.timeline
	for len(s.labels) > 0 && s.labels[0].at < outPosition {
		labels = append(labels, s.labels[0].label)
		copy(s.labels, s.labels[1:])
		s.labels = s.labels[1:]
	}
	return labels, s.eof && s.timeline >= s.decoded, nil
}

func (s *MixerSource) sourcePositionAt(timeline, total uint64) uint64 {
	if s == nil {
		return 0
	}
	base := s.decoded - uint64(len(s.pending))
	if timeline >= base {
		index := timeline - base
		if index < uint64(len(s.pendingPositions)) {
			return s.pendingPositions[index]
		}
	}
	if s.eof && timeline >= s.decoded {
		return total
	}
	return s.position
}

func (l *loadedSound) mix(target []float32, gain [2]float32, frequency uint32) ([]AudioLabel, bool, error) {
	if l == nil || l.stream == nil {
		return nil, false, fmt.Errorf("nil loaded sound")
	}
	labels, ended, err := l.mixer.MixSource(l.stream, len(target)/2, frequency, target, gain)
	if err != nil {
		return nil, false, err
	}
	l.position = l.mixer.position
	return labels, ended, nil
}

func clampGain(v int32) float32 {
	if v < 0 {
		v = 0
	}
	if v > 100000 {
		v = 100000
	}
	return float32(v) / 100000
}
func maxFloat(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}
func minFloat(a, b float32) float32 {
	if a < b {
		return a
	}
	return b
}
