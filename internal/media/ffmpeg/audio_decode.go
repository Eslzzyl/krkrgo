package ffmpeg

import (
	"fmt"
	"math"
)

// PCM is the runtime-compatible signed-16 interleaved audio representation.
type PCM struct {
	Channels   uint8
	SampleRate uint32
	Samples    []int16
}

// DecodeAudioBytes decodes a WAVE, Ogg/Vorbis, ASF/WMA, or other enabled audio source.
func DecodeAudioBytes(name string, data []byte, maxFrames int) (PCM, error) {
	loader, err := DefaultLoader()
	if err != nil {
		return PCM{}, err
	}
	return loader.DecodeAudioBytes(name, data, maxFrames)
}

// DecodeAudioBytes decodes an audio source through this loader.
func (l *Loader) DecodeAudioBytes(name string, data []byte, maxFrames int) (PCM, error) {
	if maxFrames < 0 {
		return PCM{}, fmt.Errorf("%w: negative audio frame limit", ErrMalformed)
	}
	decoder, err := l.OpenAudioBytes(name, data)
	if err != nil {
		return PCM{}, err
	}
	defer decoder.Close()
	return decoder.decodeAllAudio(maxFrames)
}

func (d *Decoder) decodeAllAudio(maxFrames int) (PCM, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return PCM{}, ErrClosed
	}
	if d.audio == nil {
		return PCM{}, fmt.Errorf("%w: source has no audio stream", ErrUnsupported)
	}
	d.audioUnlimited = true
	defer func() { d.audioUnlimited = false }()
	blocks := make([]AudioBlock, 0, 16)
	frames := 0
	for !d.eof {
		if err := d.readPacket(); err != nil {
			return PCM{}, err
		}
		newBlocks := d.takeAudio()
		for _, block := range newBlocks {
			blockFrames := len(block.Samples) / int(block.Channels)
			if blockFrames > maxFrames-frames {
				return PCM{}, fmt.Errorf("%w: decoded audio exceeds frame limit", ErrMalformed)
			}
			frames += blockFrames
			blocks = append(blocks, block)
		}
	}
	for _, block := range d.takeAudio() {
		blockFrames := len(block.Samples) / int(block.Channels)
		if blockFrames > maxFrames-frames {
			return PCM{}, fmt.Errorf("%w: decoded audio exceeds frame limit", ErrMalformed)
		}
		frames += blockFrames
		blocks = append(blocks, block)
	}
	pcm := PCM{Channels: uint8(d.audio.channels), SampleRate: uint32(d.audio.sampleRate)}
	if pcm.Channels == 0 || pcm.SampleRate == 0 {
		return PCM{}, fmt.Errorf("%w: invalid audio metadata", ErrMalformed)
	}
	pcm.Samples = make([]int16, 0, frames*int(pcm.Channels))
	for _, block := range blocks {
		for _, sample := range block.Samples {
			pcm.Samples = append(pcm.Samples, floatToInt16(sample))
		}
	}
	return pcm, nil
}

func floatToInt16(sample float32) int16 {
	if math.IsNaN(float64(sample)) {
		return 0
	}
	if sample <= -1 {
		return -32768
	}
	if sample >= 1 {
		return 32767
	}
	return int16(sample * 32767)
}
