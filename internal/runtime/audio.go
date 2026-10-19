package runtime

import (
	"fmt"
	"sync"

	"github.com/eslzzyl/krkrgo/internal/assets"
)

const (
	// MaxAudioBlockFrames bounds every decoder/mixer pull. It is deliberately
	// shared by the source and host seams so a caller cannot request an
	// unbounded allocation through either API.
	MaxAudioBlockFrames = 1_000_000
	maxAudioLabels      = 65_536
)

// AudioLabel is a source-frame label emitted by a SoundStream render.
type AudioLabel struct {
	Offset int
	Name   string
}

// AudioBlock is interleaved signed 16-bit PCM and labels relative to its
// first frame. A short Samples slice denotes end-of-source.
type AudioBlock struct {
	Samples []int16
	Labels  []AudioLabel
}

// soundRender carries source-frame mapping across the private mixer seam while
// keeping AudioBlock's public field layout compatible with unkeyed literals.
type soundRender struct {
	AudioBlock
	sourcePositions []uint64
}

// SoundStream is a deterministic, source-rate PCM reader. Loop links and
// labels are read from SLI metadata; all state needed to make split pulls
// equivalent to one large pull is retained in the stream.
type SoundStream struct {
	audio        assets.Audio
	info         assets.LoopInfo
	flagMu       sync.RWMutex
	flags        [16]int32
	flagEpoch    uint64
	sortedLabels []assets.Label
	position     uint64
	crossfade    *crossFade
	IgnoreLinks  bool
	LoopAtEnd    bool
}

type crossFade struct {
	samples          []int16
	cursor           int
	sourceStart      uint64
	destinationStart uint64
	beforeFrames     uint64
	afterFrames      uint64
}

func NewSoundStream(audio assets.Audio, info assets.LoopInfo) (*SoundStream, error) {
	info.Links = append([]assets.Link(nil), info.Links...)
	info.Labels = append([]assets.Label(nil), info.Labels...)
	if audio.Channels == 0 || audio.SampleRate == 0 || audio.SampleRate > 768_000 {
		return nil, fmt.Errorf("invalid PCM stream format")
	}
	if len(audio.Samples)%int(audio.Channels) != 0 {
		return nil, fmt.Errorf("incomplete PCM frame")
	}
	if err := info.ValidateFrames(uint64(audio.Frames())); err != nil {
		return nil, err
	}
	for _, label := range info.Labels {
		if len(label.Name) > 0 && label.Name[0] == ':' {
			return nil, fmt.Errorf("SLI label expressions are not implemented")
		}
	}
	// Sort links now; labels retain the SLI file order and are sorted privately
	// on the first render.
	info.SortLinks()
	return &SoundStream{audio: audio, info: info}, nil
}

// Audio returns the immutable decoded source metadata. The sample slice must
// be treated as read-only while the stream is in use.
func (s *SoundStream) Audio() assets.Audio {
	if s == nil {
		return assets.Audio{}
	}
	return s.audio
}

// Position is the current source frame position.
func (s *SoundStream) Position() uint64 {
	if s == nil {
		return 0
	}
	return s.position
}

// Seek moves to a source frame and discards an in-progress crossfade.
func (s *SoundStream) Seek(position uint64) error {
	if s == nil {
		return fmt.Errorf("nil sound stream")
	}
	if position > uint64(s.audio.Frames()) {
		return fmt.Errorf("sound seek is outside audio frames")
	}
	s.position = position
	s.crossfade = nil
	return nil
}

// SetFlag sets one conditional-loop flag, clamped to Kirikiri's range.
func (s *SoundStream) SetFlag(index int, value int32) error {
	if s == nil || index < 0 || index >= len(s.flags) {
		return fmt.Errorf("sound flag index out of range")
	}
	if value < 0 {
		value = 0
	} else if value > 9999 {
		value = 9999
	}
	s.flagMu.Lock()
	s.flags[index] = value
	s.flagEpoch++
	s.flagMu.Unlock()
	return nil
}

// Flag reads one conditional-loop flag.
func (s *SoundStream) Flag(index int) (int32, error) {
	if s == nil || index < 0 || index >= len(s.flags) {
		return 0, fmt.Errorf("sound flag index out of range")
	}
	s.flagMu.RLock()
	defer s.flagMu.RUnlock()
	return s.flags[index], nil
}

func (s *SoundStream) ResetFlags() {
	if s != nil {
		s.flagMu.Lock()
		s.flags = [16]int32{}
		s.flagEpoch++
		s.flagMu.Unlock()
	}
}

func (s *SoundStream) flagSnapshot() ([16]int32, uint64) {
	if s == nil {
		return [16]int32{}, 0
	}
	s.flagMu.RLock()
	defer s.flagMu.RUnlock()
	return s.flags, s.flagEpoch
}

// Labels returns a copy of SLI labels in their original file order.
func (s *SoundStream) Labels() []assets.Label {
	if s == nil {
		return nil
	}
	return append([]assets.Label(nil), s.info.Labels...)
}

// Render pulls up to frames source-rate frames. The returned block is short
// at EOF. Labels use output offsets, and crossfade state persists across calls.
func (s *SoundStream) Render(frames int) (AudioBlock, error) {
	if s == nil {
		return AudioBlock{}, fmt.Errorf("nil sound stream")
	}
	if frames < 0 || frames > MaxAudioBlockFrames {
		return AudioBlock{}, fmt.Errorf("audio block exceeds frame limit")
	}
	rendered, err := s.render(frames)
	if err != nil {
		return AudioBlock{}, err
	}
	return rendered.AudioBlock, nil
}

func (s *SoundStream) render(frames int) (soundRender, error) {
	if s.sortedLabels == nil && len(s.info.Labels) != 0 {
		s.sortedLabels = append([]assets.Label(nil), s.info.Labels...)
		// The assets sorter is stable, preserving equal-position file order.
		sortLabelsStable(s.sortedLabels)
	}
	channels := int(s.audio.Channels)
	total := uint64(s.audio.Frames())
	out := soundRender{}
	if frames == 0 {
		return out, nil
	}
	// Capacity is bounded by the caller's frame limit and source channels.
	out.Samples = make([]int16, 0, frames*channels)
	written, jumps := 0, 0
	labels := s.sortedLabels
	for written < frames {
		if s.crossfade != nil {
			fade := s.crossfade
			remaining := (len(fade.samples) - fade.cursor) / channels
			count := minInt(frames-written, remaining)
			if count <= 0 {
				return soundRender{}, fmt.Errorf("audio source cannot make progress")
			}
			fadeFrame := uint64(fade.cursor / channels)
			sourceRemaining := uint64(0)
			if fadeFrame < fade.beforeFrames {
				sourceRemaining = fade.beforeFrames - fadeFrame
			}
			first := minU64(sourceRemaining, uint64(count))
			if err := appendAudioLabels(&out.AudioBlock, labels, fade.sourceStart+fadeFrame, first, written); err != nil {
				return soundRender{}, err
			}
			if uint64(count) > first {
				destinationOffset := fadeFrame + first - fade.beforeFrames
				if err := appendAudioLabels(&out.AudioBlock, labels, fade.destinationStart+destinationOffset, uint64(count)-first, written+int(first)); err != nil {
					return soundRender{}, err
				}
			}
			end := fade.cursor + count*channels
			out.Samples = append(out.Samples, fade.samples[fade.cursor:end]...)
			for frame := range first {
				out.sourcePositions = append(out.sourcePositions, fade.sourceStart+fadeFrame+frame)
			}
			for frame := first; frame < uint64(count); frame++ {
				out.sourcePositions = append(out.sourcePositions, fade.destinationStart+fadeFrame+frame-first)
			}
			fade.cursor = end
			s.position += uint64(count)
			written += count
			if end == len(fade.samples) {
				s.position = fade.destinationStart + fade.afterFrames
				s.crossfade = nil
			}
			continue
		}
		var link *assets.Link
		if !s.IgnoreLinks {
			link = s.info.NextLink(s.position, &s.flags)
		}
		next := total
		if link != nil {
			if link.From == s.position {
				jumps++
				if jumps >= 10 {
					return soundRender{}, fmt.Errorf("SLI links form a cycle without producing audio")
				}
				s.position = link.To
				continue
			}
			next = link.From
			if link.Smooth {
				half := uint64(s.audio.SampleRate) * 25 / 1000
				before := minU64(half, minU64(link.From, link.To))
				if link.From-before > s.position {
					next = link.From - before
				} else if s.crossfade == nil {
					if err := s.prepareFade(link, half); err != nil {
						return soundRender{}, err
					}
					if s.crossfade != nil {
						continue
					}
					// A zero-width smooth link is still a link, but must not
					// turn into a zero-progress render error.
					s.position = link.To
					continue
				}
			}
		}
		if s.position == total {
			if !s.LoopAtEnd || total == 0 {
				break
			}
			s.position = 0
			continue
		}
		count := int(minU64(next-s.position, uint64(frames-written)))
		if count <= 0 {
			return soundRender{}, fmt.Errorf("audio source cannot make progress")
		}
		if err := appendAudioLabels(&out.AudioBlock, labels, s.position, uint64(count), written); err != nil {
			return soundRender{}, err
		}
		start := int(s.position) * channels
		out.Samples = append(out.Samples, s.audio.Samples[start:start+count*channels]...)
		for frame := range uint64(count) {
			out.sourcePositions = append(out.sourcePositions, s.position+frame)
		}
		s.position += uint64(count)
		written += count
		jumps = 0
	}
	return out, nil
}

func appendAudioLabels(out *AudioBlock, labels []assets.Label, position uint64, frames uint64, offset int) error {
	if frames == 0 {
		return nil
	}
	first := firstLabelAtOrAfter(labels, position)
	for _, label := range labels[first:] {
		if label.Position >= position+frames {
			break
		}
		if len(out.Labels) >= maxAudioLabels {
			return fmt.Errorf("audio label output limit exceeded")
		}
		out.Labels = append(out.Labels, AudioLabel{Offset: offset + int(label.Position-position), Name: label.Name})
	}
	return nil
}

func (s *SoundStream) prepareFade(link *assets.Link, half uint64) error {
	total := uint64(s.audio.Frames())
	if s.position > link.From {
		return fmt.Errorf("invalid SLI crossfade range")
	}
	before := link.From - s.position
	after := minU64(half, minU64(total-link.From, total-link.To))
	if next := s.info.NextLink(link.To+1, &s.flags); next != nil && next.From > link.To {
		after = minU64(after, next.From-link.To)
	}
	if before > link.To {
		return fmt.Errorf("invalid SLI crossfade range")
	}
	channels := int(s.audio.Channels)
	if before+after > uint64(MaxAudioBlockFrames) {
		return fmt.Errorf("audio crossfade exceeds frame limit")
	}
	samples := make([]int16, 0, int(before+after)*channels)
	for _, part := range []struct{ offset, frames, ratioStart uint64 }{{0, before, 0}, {before, after, uint64(1) << 31}} {
		if part.frames == 0 {
			continue
		}
		step := (uint64(1) << 31) / part.frames
		for frame := range part.frames {
			ratio := part.ratioStart + frame*step
			from := int(s.position+part.offset+frame) * channels
			to := int(link.To-before+part.offset+frame) * channels
			for channel := range channels {
				a := int64(s.audio.Samples[from+channel])
				b := int64(s.audio.Samples[to+channel])
				blended := (b * int64(ratio) >> 32) + (a * int64((uint64(1)<<32)-ratio) >> 32)
				samples = append(samples, int16(blended))
			}
		}
	}
	if len(samples) != 0 {
		s.crossfade = &crossFade{samples: samples, sourceStart: s.position, destinationStart: link.To - before, beforeFrames: before, afterFrames: after}
	}
	return nil
}

func firstLabelAtOrAfter(labels []assets.Label, position uint64) int {
	lo, hi := 0, len(labels)
	for lo < hi {
		mid := lo + (hi-lo)/2
		if labels[mid].Position < position {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

func sortLabelsStable(labels []assets.Label) {
	// Insertion sort avoids importing a second sorting convention and is
	// bounded by the SLI event limit.
	for i := 1; i < len(labels); i++ {
		v := labels[i]
		j := i
		for j > 0 && labels[j-1].Position > v.Position {
			labels[j] = labels[j-1]
			j--
		}
		labels[j] = v
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func minU64(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}
