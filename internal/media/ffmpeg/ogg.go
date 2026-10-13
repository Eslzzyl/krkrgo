package ffmpeg

import (
	"bytes"
	"fmt"
	"io"

	"github.com/jfreymuth/oggvorbis"
)

func isOgg(data []byte) bool {
	return bytes.HasPrefix(data, []byte("OggS"))
}

func decodeOggBytes(data []byte, maxFrames int) (PCM, error) {
	if maxFrames < 0 {
		return PCM{}, fmt.Errorf("%w: negative audio frame limit", ErrMalformed)
	}
	r, err := oggvorbis.NewReader(bytes.NewReader(data))
	if err != nil {
		return PCM{}, fmt.Errorf("%w: Ogg/Vorbis header: %v", ErrMalformed, err)
	}
	channels := r.Channels()
	if channels < 1 || channels > 2 {
		return PCM{}, fmt.Errorf("%w: Ogg/Vorbis channel count %d", ErrUnsupported, channels)
	}
	sampleRate := r.SampleRate()
	if sampleRate <= 0 {
		return PCM{}, fmt.Errorf("%w: Ogg/Vorbis sample rate %d", ErrMalformed, sampleRate)
	}

	const chunkFrames = 4096
	samples := make([]int16, 0, minInt(maxFrames, chunkFrames)*channels)
	buffer := make([]float32, chunkFrames*channels)
	for {
		n, readErr := r.Read(buffer)
		if n < 0 || n > len(buffer) || n%channels != 0 {
			return PCM{}, fmt.Errorf("%w: invalid Ogg/Vorbis sample count %d", ErrMalformed, n)
		}
		if n != 0 {
			frames := n / channels
			if len(samples)/channels > maxFrames-frames {
				return PCM{}, fmt.Errorf("%w: decoded audio exceeds frame limit", ErrMalformed)
			}
			for _, sample := range buffer[:n] {
				samples = append(samples, floatToInt16(sample))
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return PCM{}, fmt.Errorf("%w: Ogg/Vorbis decode: %v", ErrMalformed, readErr)
		}
		if n == 0 {
			return PCM{}, fmt.Errorf("%w: Ogg/Vorbis decoder produced no samples", ErrMalformed)
		}
	}
	return PCM{Channels: uint8(channels), SampleRate: uint32(sampleRate), Samples: samples}, nil
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
