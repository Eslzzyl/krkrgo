package ffmpeg

import (
	"fmt"
	"math"
	"runtime"
	"sort"
	"strings"
	"sync"
	"unsafe"
)

const (
	MaxSourceBytes = 256 * 1024 * 1024
	MaxFrameBytes  = 64 * 1024 * 1024
	MaxPacketBytes = 16 * 1024 * 1024
	MaxAudioBlocks = 32
)

// Metadata describes the selected ASF/WMV media streams.
type Metadata struct {
	Name          string
	Format        string
	Width         uint32
	Height        uint32
	FPS           float64
	Frames        uint64
	DurationMS    uint64
	AudioRate     uint32
	AudioChannels uint8
	HasAudio      bool
}

// Frame is one converted RGBA video frame.
type Frame struct {
	Index         uint64
	Width, Height int32
	PTSMS         uint64
	RGBA          []byte
}

// AudioBlock is interleaved native-rate float PCM.
type AudioBlock struct {
	StartMS    uint64
	SampleRate uint32
	Channels   uint8
	Samples    []float32
}

type streamState struct {
	index       int32
	codec       uintptr
	context     uintptr
	frame       uintptr
	codecID     int32
	codecParams avCodecParametersABI
	timeBase    avRational
	frameRate   avRational
	channels    int
	sampleRate  int
}

// Decoder owns an FFmpeg demuxer and its selected audio/video decoder contexts.
type Decoder struct {
	mu       sync.Mutex
	loader   *Loader
	api      *api
	name     string
	format   uintptr
	io       *avioState
	packet   uintptr
	video    *streamState
	audio    *streamState
	metadata Metadata
	sws      uintptr
	eof      bool
	drained  bool
	closed   bool
	frameSeq uint64
	audioPos uint64
	pending  []Frame
	audioOut []AudioBlock
	last     *Frame
	requireVideo bool
	audioUnlimited bool
}

// OpenBytes opens a bounded memory-backed media source with the default loader.
func OpenBytes(name string, data []byte) (*Decoder, error) {
	loader, err := DefaultLoader()
	if err != nil {
		return nil, err
	}
	return loader.openBytes(name, data, true)
}

// OpenAudioBytes opens an audio-only or mixed media source with the default loader.
func OpenAudioBytes(name string, data []byte) (*Decoder, error) {
	loader, err := DefaultLoader()
	if err != nil {
		return nil, err
	}
	return loader.openBytes(name, data, false)
}

// OpenBytes opens a bounded memory-backed media source with this loader.
func (l *Loader) OpenBytes(name string, data []byte) (*Decoder, error) {
	return l.openBytes(name, data, true)
}

// OpenAudioBytes opens an audio-only or mixed media source with this loader.
func (l *Loader) OpenAudioBytes(name string, data []byte) (*Decoder, error) {
	return l.openBytes(name, data, false)
}

func (l *Loader) openBytes(name string, data []byte, requireVideo bool) (*Decoder, error) {
	if err := l.checkOpen(); err != nil {
		return nil, err
	}
	if len(data) == 0 || len(data) > MaxSourceBytes {
		return nil, fmt.Errorf("%w: source size %d", ErrMalformed, len(data))
	}
	if strings.TrimSpace(name) == "" {
		name = "<memory>"
	}
	d := &Decoder{loader: l, api: &l.api, name: name, requireVideo: requireVideo}
	ioState, err := newAVIO(d.api, data)
	if err != nil {
		return nil, fmt.Errorf("%w: allocate custom AVIO: %v", ErrUnavailable, err)
	}
	d.io = ioState
	ctx := d.api.avformatAllocContext()
	if ctx == 0 {
		d.close()
		return nil, fmt.Errorf("%w: allocate AVFormatContext", ErrUnavailable)
	}
	d.format = ctx
	format := (*avFormatContextABI)(unsafe.Pointer(ctx))
	format.PB = ioState.ctx
	format.Flags |= avFormatCustomIO
	url := cString("memory:" + name)
	var options uintptr
	if ret := d.api.avformatOpenInput(&d.format, &url[0], 0, &options); ret < 0 || d.format == 0 {
		d.close()
		return nil, fmt.Errorf("%w: avformat_open_input returned %d", ErrMalformed, ret)
	}
	if ret := d.api.avformatFindStreamInfo(d.format, 0); ret < 0 {
		d.close()
		return nil, fmt.Errorf("%w: avformat_find_stream_info returned %d", ErrMalformed, ret)
	}
	if err := d.discover(); err != nil {
		d.close()
		return nil, err
	}
	d.packet = d.api.avPacketAlloc()
	if d.packet == 0 {
		d.close()
		return nil, fmt.Errorf("%w: allocate AVPacket", ErrUnavailable)
	}
	return d, nil
}

func (d *Decoder) discover() error {
	format := (*avFormatContextABI)(unsafe.Pointer(d.format))
	if format.StreamCount == 0 || format.Streams == 0 || format.StreamCount > 64 {
		return fmt.Errorf("%w: invalid stream count %d", ErrMalformed, format.StreamCount)
	}
	var durationMS uint64
	if format.Duration > 0 {
		durationMS = uint64(format.Duration / 1000)
	}
	for i := uint32(0); i < format.StreamCount; i++ {
		streamPtr := ptrAt(format.Streams, uintptr(i))
		if streamPtr == 0 {
			continue
		}
		stream := (*avStreamABI)(unsafe.Pointer(streamPtr))
		if stream.CodecPar == 0 {
			continue
		}
		params := *(*avCodecParametersABI)(unsafe.Pointer(stream.CodecPar))
		state := &streamState{index: int32(i), codecID: params.CodecID, codecParams: params, timeBase: stream.TimeBase, frameRate: stream.AverageFrameRate}
		if stream.Duration > 0 && stream.TimeBase.Den > 0 {
			candidate := rescaleMS(stream.Duration, stream.TimeBase)
			if candidate > durationMS {
				durationMS = candidate
			}
		}
		switch params.CodecType {
		case mediaTypeVideo:
			if d.video == nil && params.Width > 0 && params.Height > 0 {
				state.frameRate = chooseRate(stream.AverageFrameRate, stream.RealFrameRate, params.FrameRate)
				d.video = state
			}
		case mediaTypeAudio:
			if d.audio == nil && params.SampleRate > 0 {
				state.sampleRate = int(params.SampleRate)
				state.channels = int(params.ChannelLayout.Channels)
				if state.channels <= 0 {
					state.channels = 2
				}
				d.audio = state
			}
		}
	}
	if d.video == nil && d.requireVideo {
		return fmt.Errorf("%w: no decodable video stream", ErrUnsupported)
	}
	if d.video != nil {
		if err := d.openCodec(d.video); err != nil {
			return err
		}
	}
	if d.audio != nil {
		if err := d.openCodec(d.audio); err != nil {
			if d.requireVideo {
				d.audio = nil
			} else {
				return err
			}
		}
	}
	if d.video == nil && d.audio == nil {
		return fmt.Errorf("%w: no decodable audio or video stream", ErrUnsupported)
	}
	fps := float64(0)
	frames := uint64(0)
	var width, height uint32
	if d.video != nil {
		fps = rationalFloat(d.video.frameRate)
		width = uint32(d.video.codecParams.Width)
		height = uint32(d.video.codecParams.Height)
		stream := (*avStreamABI)(unsafe.Pointer(ptrAt(format.Streams, uintptr(d.video.index))))
		if stream.FrameCount > 0 {
			frames = uint64(stream.FrameCount)
		}
	}
	if frames == 0 && durationMS > 0 && fps > 0 {
		frames = uint64(math.Ceil(float64(durationMS) * fps / 1000))
	}
	if d.video != nil && frames == 0 {
		frames = 1
	}
	meta := Metadata{Name: d.name, Format: "FFmpeg", Width: width, Height: height, FPS: fps, Frames: frames, DurationMS: durationMS}
	if d.video != nil {
		meta.Format = "ASF/FFmpeg"
	}
	if d.audio != nil {
		meta.HasAudio = true
		meta.AudioRate = uint32(d.audio.sampleRate)
		meta.AudioChannels = uint8(d.audio.channels)
	}
	if d.video != nil && meta.FPS <= 0 {
		meta.FPS = 1
	}
	if d.video != nil && meta.DurationMS == 0 {
		meta.DurationMS = uint64(math.Ceil(float64(meta.Frames) * 1000 / meta.FPS))
	}
	d.metadata = meta
	return nil
}

func chooseRate(values ...avRational) avRational {
	for _, value := range values {
		if value.Num > 0 && value.Den > 0 {
			return value
		}
	}
	return avRational{}
}

func rationalFloat(value avRational) float64 {
	if value.Num <= 0 || value.Den <= 0 {
		return 0
	}
	return float64(value.Num) / float64(value.Den)
}

func rescaleMS(value int64, timeBase avRational) uint64 {
	if value == avNoPTSValue || timeBase.Num <= 0 || timeBase.Den <= 0 || value < 0 {
		return 0
	}
	seconds := float64(value) * float64(timeBase.Num) / float64(timeBase.Den)
	if seconds <= 0 {
		return 0
	}
	return uint64(seconds * 1000)
}

func (d *Decoder) openCodec(state *streamState) error {
	codec := d.api.avcodecFindDecoder(state.codecID)
	if codec == 0 {
		return fmt.Errorf("%w: codec id %d", ErrUnsupported, state.codecID)
	}
	context := d.api.avcodecAllocContext3(codec)
	if context == 0 {
		return fmt.Errorf("%w: allocate codec context", ErrUnavailable)
	}
	state.context = context
	if ret := d.api.avcodecParametersToContext(context, uintptr(unsafe.Pointer(&state.codecParams))); ret < 0 {
		d.freeCodec(state)
		return fmt.Errorf("%w: copy codec parameters returned %d", ErrMalformed, ret)
	}
	if ret := d.api.avcodecOpen2(context, codec, 0); ret < 0 {
		d.freeCodec(state)
		return fmt.Errorf("%w: open codec returned %d", ErrUnsupported, ret)
	}
	state.frame = d.api.avFrameAlloc()
	if state.frame == 0 {
		d.freeCodec(state)
		return fmt.Errorf("%w: allocate codec frame", ErrUnavailable)
	}
	state.codec = codec
	return nil
}

func (d *Decoder) freeCodec(state *streamState) {
	if state == nil {
		return
	}
	if state.frame != 0 {
		frame := state.frame
		d.api.avFrameFree(&frame)
		state.frame = 0
	}
	if state.context != 0 {
		context := state.context
		d.api.avcodecFreeContext(&context)
		state.context = 0
	}
}

// Metadata returns a copy of the selected stream metadata.
func (d *Decoder) Metadata() Metadata {
	if d == nil {
		return Metadata{}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.metadata
}

// DecodeUntil decodes packets until the newest frame at or before targetMS is available.
// Audio blocks decoded during the same pass are returned in native sample format.
func (d *Decoder) DecodeUntil(targetMS uint64) (*Frame, []AudioBlock, error) {
	if d == nil {
		return nil, nil, ErrClosed
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil, nil, ErrClosed
	}
	for {
		frame := d.promote(targetMS)
		if frame != nil || d.eof {
			return frame, d.takeAudio(), nil
		}
		if err := d.readPacket(); err != nil {
			return nil, d.takeAudio(), err
		}
	}
}

func (d *Decoder) promote(targetMS uint64) *Frame {
	if len(d.pending) == 0 {
		return nil
	}
	best := -1
	for i := range d.pending {
		if d.pending[i].PTSMS <= targetMS {
			best = i
		}
	}
	if best < 0 {
		return nil
	}
	frame := d.pending[best]
	d.pending = append(d.pending[:0], d.pending[best+1:]...)
	d.last = &frame
	return &frame
}

func (d *Decoder) takeAudio() []AudioBlock {
	out := d.audioOut
	d.audioOut = nil
	return out
}

func (d *Decoder) readPacket() error {
	if d.packet == 0 {
		return fmt.Errorf("%w: packet is unavailable", ErrClosed)
	}
	if ret := d.api.avReadFrame(d.format, d.packet); ret < 0 {
		if !d.drained {
			if err := d.drain(); err != nil {
				return err
			}
		}
		d.eof = true
		return nil
	}
	packet := (*avPacketABI)(unsafe.Pointer(d.packet))
	if packet.Size < 0 || packet.Size > MaxPacketBytes {
		d.api.avPacketUnref(d.packet)
		return fmt.Errorf("%w: packet size %d", ErrMalformed, packet.Size)
	}
	var err error
	if d.video != nil && packet.StreamIndex == d.video.index {
		err = d.decodePacket(d.video)
	} else if d.audio != nil && packet.StreamIndex == d.audio.index {
		err = d.decodePacket(d.audio)
	}
	d.api.avPacketUnref(d.packet)
	return err
}

func (d *Decoder) audioIndex() int32 {
	if d.audio == nil {
		return -1
	}
	return d.audio.index
}

func (d *Decoder) decodePacket(state *streamState) error {
	if ret := d.api.avcodecSendPacket(state.context, d.packet); ret < 0 && ret != avErrorAgain {
		return fmt.Errorf("%w: avcodec_send_packet returned %d", ErrMalformed, ret)
	}
	return d.receiveFrames(state)
}

func (d *Decoder) receiveFrames(state *streamState) error {
	for {
		ret := d.api.avcodecReceiveFrame(state.context, state.frame)
		if ret == avErrorAgain || ret == avErrorEOF {
			return nil
		}
		if ret < 0 {
			return fmt.Errorf("%w: avcodec_receive_frame returned %d", ErrMalformed, ret)
		}
		if state == d.video {
			frame, err := d.convertVideo(state)
			if err != nil {
				return err
			}
			d.pending = append(d.pending, *frame)
			if len(d.pending) > 8 {
				sort.SliceStable(d.pending, func(i, j int) bool { return d.pending[i].PTSMS < d.pending[j].PTSMS })
				d.pending = d.pending[len(d.pending)-8:]
			}
		} else {
			block, err := d.convertAudio(state)
			if err != nil {
				return err
			}
			if block != nil {
				d.audioOut = append(d.audioOut, *block)
				if !d.audioUnlimited && len(d.audioOut) > MaxAudioBlocks {
					return fmt.Errorf("%w: decoded audio queue exceeds %d blocks", ErrMalformed, MaxAudioBlocks)
				}
			}
		}
		d.api.avFrameUnref(state.frame)
	}
}

func (d *Decoder) drain() error {
	d.drained = true
	for _, state := range []*streamState{d.video, d.audio} {
		if state == nil || state.context == 0 {
			continue
		}
		if ret := d.api.avcodecSendPacket(state.context, 0); ret < 0 && ret != avErrorEOF && ret != avErrorAgain {
			return fmt.Errorf("%w: drain packet returned %d", ErrMalformed, ret)
		}
		if err := d.receiveFrames(state); err != nil {
			return err
		}
	}
	return nil
}

// Seek flushes both codec contexts and positions the custom input reader.
func (d *Decoder) Seek(positionMS uint64) error {
	if d == nil {
		return ErrClosed
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return ErrClosed
	}
	if positionMS > d.metadata.DurationMS {
		positionMS = d.metadata.DurationMS
	}
	if ret := d.api.avSeekFrame(d.format, -1, int64(positionMS)*1000, avSeekBackward); ret < 0 {
		return fmt.Errorf("%w: av_seek_frame returned %d", ErrMalformed, ret)
	}
	if d.video != nil {
		d.api.avcodecFlushBuffers(d.video.context)
		d.api.avFrameUnref(d.video.frame)
	}
	if d.audio != nil {
		d.api.avcodecFlushBuffers(d.audio.context)
		d.api.avFrameUnref(d.audio.frame)
	}
	d.api.avPacketUnref(d.packet)
	d.pending = nil
	d.audioOut = nil
	d.last = nil
	d.eof = false
	d.drained = false
	d.frameSeq = 0
	d.audioPos = positionMS
	return nil
}

func (d *Decoder) close() error {
	if d == nil || d.closed {
		return nil
	}
	d.closed = true
	if d.sws != 0 {
		d.api.swsFreeContext(d.sws)
		d.sws = 0
	}
	if d.video != nil {
		d.freeCodec(d.video)
	}
	if d.audio != nil {
		d.freeCodec(d.audio)
	}
	if d.packet != 0 {
		packet := d.packet
		d.api.avPacketFree(&packet)
		d.packet = 0
	}
	if d.format != 0 {
		format := d.format
		d.api.avformatCloseInput(&format)
		d.format = 0
	}
	if d.io != nil {
		d.io.close()
		d.io = nil
	}
	return nil
}

// Close releases all FFmpeg decoder state.
func (d *Decoder) Close() error {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.close()
}

func (d *Decoder) keepAlive() {
	runtime.KeepAlive(d)
}
