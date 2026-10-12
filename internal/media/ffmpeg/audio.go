package ffmpeg

import (
	"fmt"
	"math"
	"unsafe"
)

func (d *Decoder) convertAudio(state *streamState) (*AudioBlock, error) {
	frame := (*avFrameABI)(unsafe.Pointer(state.frame))
	channels := state.channels
	if channels <= 0 {
		channels = int(d.metadata.AudioChannels)
	}
	if channels <= 0 || channels > 2 || frame.SampleCount <= 0 || frame.SampleCount > 1<<20 {
		return nil, fmt.Errorf("%w: unsupported audio layout channels=%d samples=%d", ErrUnsupported, channels, frame.SampleCount)
	}
	rate := state.sampleRate
	if frame.SampleRate > 0 {
		rate = int(frame.SampleRate)
	}
	if rate <= 0 || rate > 768000 {
		return nil, fmt.Errorf("%w: invalid audio sample rate %d", ErrMalformed, rate)
	}
	count := int64(frame.SampleCount) * int64(channels)
	if count <= 0 || count > MaxPacketBytes {
		return nil, fmt.Errorf("%w: audio block exceeds memory limit", ErrMalformed)
	}
	samples := make([]float32, int(count))
	planar := frame.Format == sampleFormatU8P || frame.Format == sampleFormatS16P || frame.Format == sampleFormatS32P || frame.Format == sampleFormatFLTP || frame.Format == sampleFormatDBLP
	for sample := 0; sample < int(frame.SampleCount); sample++ {
		for channel := 0; channel < channels; channel++ {
			plane := frame.Data[0]
			if planar {
				if frame.ExtendedData != 0 {
					plane = ptrAt(frame.ExtendedData, uintptr(channel))
				} else if channel < len(frame.Data) {
					plane = frame.Data[channel]
				}
			}
			if plane == 0 {
				return nil, fmt.Errorf("%w: audio plane %d is empty", ErrMalformed, channel)
			}
			index := sample
			if !planar {
				index = sample*channels + channel
			}
			value := readAudioSample(plane, index, int(frame.Format))
			if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
				return nil, fmt.Errorf("%w: unsupported audio sample format %d", ErrUnsupported, frame.Format)
			}
			samples[sample*channels+channel] = value
		}
	}
	pts := frame.PTS
	if pts == avNoPTSValue {
		pts = frame.PacketDTS
	}
	timeBase := frame.TimeBase
	if timeBase.Num <= 0 || timeBase.Den <= 0 {
		timeBase = state.timeBase
	}
	start := rescaleMS(pts, timeBase)
	if pts == avNoPTSValue || (start == 0 && d.audioPos > 0) {
		start = d.audioPos
	}
	d.audioPos = start + uint64(uint64(frame.SampleCount)*1000/uint64(rate))
	return &AudioBlock{StartMS: start, SampleRate: uint32(rate), Channels: uint8(channels), Samples: samples}, nil
}

func readAudioSample(base uintptr, index int, format int) float32 {
	switch format {
	case sampleFormatU8, sampleFormatU8P:
		return (float32(*(*uint8)(unsafe.Pointer(base+uintptr(index)))) - 128) / 128
	case sampleFormatS16, sampleFormatS16P:
		return float32(*(*int16)(unsafe.Pointer(base+uintptr(index*2)))) / 32768
	case sampleFormatS32, sampleFormatS32P:
		return float32(float64(*(*int32)(unsafe.Pointer(base+uintptr(index*4)))) / 2147483648)
	case sampleFormatFLT, sampleFormatFLTP:
		return *(*float32)(unsafe.Pointer(base + uintptr(index*4)))
	case sampleFormatDBL, sampleFormatDBLP:
		return float32(*(*float64)(unsafe.Pointer(base + uintptr(index*8))))
	default:
		return float32(math.NaN())
	}
}
