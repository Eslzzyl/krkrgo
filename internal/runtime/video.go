package runtime

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"

	"github.com/eslzzyl/krkrgo/internal/assets"
	"github.com/eslzzyl/krkrgo/internal/media/ffmpeg"
	"github.com/eslzzyl/krkrgo/internal/tjs"
)

const (
	MaxVideoFrames      = 8
	MaxVideoAudioBlocks = 32
	MaxVideoSourceBytes = 256 * 1024 * 1024
)

var (
	ErrVideoClosed      = errors.New("video overlay is closed")
	ErrVideoNoSource    = errors.New("video overlay has no source")
	ErrVideoUnsupported = errors.New("unsupported video")
	ErrVideoQueueFull   = errors.New("video frame queue is full")
)

// VideoError preserves the source name while retaining a typed underlying cause.
type VideoError struct {
	Source string
	Err    error
}

func (e *VideoError) Error() string {
	if e == nil {
		return "video error"
	}
	if e.Source == "" {
		return "video: " + e.Err.Error()
	}
	return fmt.Sprintf("video %s: %v", e.Source, e.Err)
}
func (e *VideoError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}
func videoErr(source string, err error) error {
	if err == nil {
		return nil
	}
	return &VideoError{Source: source, Err: err}
}

// VideoStatus follows VideoOvlIntf transport state.
type VideoStatus string

const (
	VideoUnloaded VideoStatus = "unload"
	VideoStopped  VideoStatus = "stop"
	VideoPlaying  VideoStatus = "play"
	VideoPaused   VideoStatus = "pause"
)

// VideoEvent is delivered in deterministic queue order after Advance.
type VideoEvent struct {
	Generation uint64
	Kind       string // status, frame, period
	Status     VideoStatus
	Frame      uint64
	Reason     int
}

// VideoSource is bounded metadata parsed from an AMV or FFmpeg source.
type VideoSource struct {
	Name          string
	Format        string
	Width, Height uint32
	FPS           float64
	Frames        uint64
	DurationMS    uint64
	AudioRate     uint32
	AudioChannels uint8
	HasAudio      bool
}

// VideoFrame is decoded RGBA. FFmpeg frames are converted to RGBA by the
// sidecar decoder; AMV frames are decoded by the repository decoder.
type VideoFrame struct {
	Index         uint64
	Width, Height int32
	PTSMS         uint64
	RGBA          []byte
}

// VideoAudioBlock is bounded interleaved float PCM at its native sample rate.
type VideoAudioBlock struct {
	StartMS    uint64
	SampleRate uint32
	Channels   uint8
	Samples    []float32
}

// VideoOverlay is a deterministic, bounded movie state model. AMV sources
// retain their checked movie and one decoded frame; FFmpeg sources retain
// bounded decoded state. Audio and host-queued frames are independently
// bounded by their respective limits.
type VideoOverlay struct {
	mu             sync.Mutex
	owner          tjs.ObjectID
	source         *VideoSource
	ffmpeg         *ffmpeg.Decoder
	status         VideoStatus
	amv            *assets.Movie
	positionMS     float64
	rate           float64
	volume         int32
	balance        int32
	bounds         Rect
	visible        bool
	looping        bool
	mode           int32
	layers         [2]*Layer
	segment        [2]int64
	period         int64
	generation     uint64
	events         []VideoEvent
	frameQueue     []VideoFrame
	audioQueue     []VideoAudioBlock
	current        *VideoFrame
	clockMS        uint64
	clockSet       bool
	callback       func(VideoEvent)
	sidecarSkipped bool
}

func NewVideoOverlay() *VideoOverlay {
	return &VideoOverlay{status: VideoUnloaded, rate: 1, volume: 100000, segment: [2]int64{-1, -1}, period: -1, visible: true}
}
func (v *VideoOverlay) ensure() error {
	if v == nil {
		return ErrVideoClosed
	}
	return nil
}
func (v *VideoOverlay) Status() VideoStatus {
	if v == nil {
		return VideoUnloaded
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.status
}
func (v *VideoOverlay) Generation() uint64 {
	if v == nil {
		return 0
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.generation
}
func (v *VideoOverlay) Source() (VideoSource, bool) {
	if v == nil {
		return VideoSource{}, false
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.source == nil {
		return VideoSource{}, false
	}
	return *v.source, true
}
func (v *VideoOverlay) Position() uint64 {
	if v == nil {
		return 0
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.positionMS <= 0 {
		return 0
	}
	return uint64(v.positionMS)
}
func (v *VideoOverlay) Frame() uint64 {
	if v == nil {
		return 0
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.frameAtLocked()
}
func (v *VideoOverlay) Bounds() Rect {
	if v == nil {
		return Rect{}
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.bounds
}
func (v *VideoOverlay) Visible() bool {
	if v == nil {
		return false
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.visible
}
func (v *VideoOverlay) Looping() bool {
	if v == nil {
		return false
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.looping
}
func (v *VideoOverlay) Mode() int32 {
	if v == nil {
		return 0
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.mode
}
func (v *VideoOverlay) SegmentLoop() (int64, int64) {
	if v == nil {
		return -1, -1
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.segment[0], v.segment[1]
}
func (v *VideoOverlay) Rate() float64 {
	if v == nil {
		return 0
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.rate
}
func (v *VideoOverlay) Volume() int32 {
	if v == nil {
		return 0
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.volume
}
func (v *VideoOverlay) Balance() int32 {
	if v == nil {
		return 0
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.balance
}
func (v *VideoOverlay) SetEventCallback(cb func(VideoEvent)) {
	if v != nil {
		v.mu.Lock()
		v.callback = cb
		v.mu.Unlock()
	}
}
func (v *VideoOverlay) PeriodEvent() int64 {
	if v == nil {
		return -1
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.period
}
func (v *VideoOverlay) Events() []VideoEvent {
	if v == nil {
		return nil
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	out := append([]VideoEvent(nil), v.events...)
	v.events = v.events[:0]
	return out
}
func (v *VideoOverlay) DrainEvents() []VideoEvent { return v.Events() }
func (v *VideoOverlay) SetOwner(id tjs.ObjectID) {
	if v != nil {
		v.mu.Lock()
		v.owner = id
		v.mu.Unlock()
	}
}
func (v *VideoOverlay) Owner() tjs.ObjectID {
	if v == nil {
		return 0
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.owner
}

func (v *VideoOverlay) emitLocked(e VideoEvent) {
	if len(v.events) < 256 {
		v.events = append(v.events, e)
	}
	if v.callback != nil {
		v.callback(e)
	}
}
func (v *VideoOverlay) setStatusLocked(s VideoStatus) {
	if v.status != s {
		v.status = s
		v.emitLocked(VideoEvent{Generation: v.generation, Kind: "status", Status: s})
	}
}

// failOpen turns a missing or ABI-incompatible sidecar into a logged no-op and
// keeps script playback calls tolerant until the overlay is reused with a
// supported source.
func (v *VideoOverlay) failOpen(name string, err error) error {
	if !sidecarUnavailable(err) {
		return videoErr(name, err)
	}
	reportSidecarUnavailable(err)
	v.mu.Lock()
	v.sidecarSkipped = true
	v.mu.Unlock()
	return nil
}

func (v *VideoOverlay) frameAtLocked() uint64 {
	if v.source == nil || v.source.Frames == 0 || v.source.FPS <= 0 {
		return 0
	}
	n := uint64(math.Floor(v.positionMS * v.source.FPS / 1000))
	if n >= v.source.Frames {
		n = v.source.Frames - 1
	}
	return n
}

// Open parses source metadata and admits bounded decoded frames.
func (v *VideoOverlay) Open(name string, data []byte) error {
	if err := v.ensure(); err != nil {
		return err
	}
	if name == "" {
		name = "<memory>"
	}
	if len(data) > MaxVideoSourceBytes {
		return videoErr(name, fmt.Errorf("source exceeds %d bytes", MaxVideoSourceBytes))
	}
	if len(data) >= 4 && string(data[:4]) == "AJPM" {
		owned := append([]byte(nil), data...)
		movie, err := assets.ParseAMV(owned)
		if err != nil {
			return videoErr(name, err)
		}
		meta, err := videoSourceFromAMV(name, movie)
		if err != nil {
			return err
		}
		image, err := movie.DecodePacket(0)
		if err != nil {
			return videoErr(name, err)
		}
		initial, err := videoFrameFromAMV(0, image)
		if err != nil {
			return videoErr(name, err)
		}
		if err := validateAMVFrame(&meta, initial); err != nil {
			return videoErr(name, err)
		}
		return v.installSource(name, meta, movie, nil, initial)
	}
	decoder, err := ffmpeg.OpenBytes(name, data)
	if err != nil {
		return v.failOpen(name, err)
	}
	meta := videoSourceFromFFmpeg(decoder.Metadata())
	initial, audio, err := decoder.DecodeUntil(0)
	if err != nil || initial == nil {
		_ = decoder.Close()
		if err == nil {
			err = ErrVideoUnsupported
		}
		return videoErr(name, err)
	}
	if err := v.installSource(name, meta, nil, decoder, &VideoFrame{
		Index: initial.Index, Width: initial.Width, Height: initial.Height,
		PTSMS: initial.PTSMS, RGBA: initial.RGBA,
	}); err != nil {
		_ = decoder.Close()
		return err
	}
	if err := v.enqueueFFmpegAudio(audio); err != nil {
		v.Close()
		return videoErr(name, err)
	}
	return nil
}

func videoSourceFromAMV(name string, movie *assets.Movie) (VideoSource, error) {
	if movie == nil {
		return VideoSource{}, videoErr(name, ErrVideoUnsupported)
	}
	fps := float64(movie.Header.FPSRate) / float64(movie.Header.FPSScale)
	if fps <= 0 {
		return VideoSource{}, videoErr(name, ErrVideoUnsupported)
	}
	return VideoSource{
		Name: name, Format: "AMV",
		Width: uint32(movie.Header.Width), Height: uint32(movie.Header.Height),
		FPS: fps, Frames: uint64(movie.Header.FrameCount),
		DurationMS: uint64(float64(movie.Header.FrameCount) * 1000 / fps),
	}, nil
}

func videoFrameFromAMV(index uint64, image *assets.Image) (*VideoFrame, error) {
	if image == nil {
		return nil, nil
	}
	if image.Width == 0 || image.Height == 0 ||
		uint64(image.Width)*uint64(image.Height) > 64*1024*1024 ||
		uint64(len(image.RGBA)) != uint64(image.Width)*uint64(image.Height)*4 ||
		image.Width > uint32(math.MaxInt32) || image.Height > uint32(math.MaxInt32) {
		return nil, fmt.Errorf("invalid AlphaMovie image dimensions")
	}
	return &VideoFrame{
		Index: index, Width: int32(image.Width), Height: int32(image.Height),
		RGBA: image.RGBA,
	}, nil
}
func validateAMVFrame(source *VideoSource, frame *VideoFrame) error {
	if frame == nil {
		return nil
	}
	if source == nil || frame.Width <= 0 || frame.Height <= 0 ||
		uint32(frame.Width) > source.Width || uint32(frame.Height) > source.Height {
		return fmt.Errorf("AlphaMovie frame exceeds source bounds")
	}
	return nil
}

func videoSourceFromFFmpeg(meta ffmpeg.Metadata) VideoSource {
	return VideoSource{
		Name: meta.Name, Format: meta.Format, Width: meta.Width, Height: meta.Height,
		FPS: meta.FPS, Frames: meta.Frames, DurationMS: meta.DurationMS,
		AudioRate: meta.AudioRate, AudioChannels: meta.AudioChannels, HasAudio: meta.HasAudio,
	}
}

func (v *VideoOverlay) enqueueFFmpegAudio(blocks []ffmpeg.AudioBlock) error {
	if len(blocks) == 0 {
		return nil
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.appendFFmpegAudioLocked(blocks)
}

func (v *VideoOverlay) appendFFmpegAudioLocked(blocks []ffmpeg.AudioBlock) error {
	for _, block := range blocks {
		if block.Channels == 0 || block.SampleRate == 0 ||
			len(block.Samples)%int(block.Channels) != 0 ||
			len(block.Samples)/int(block.Channels) > MaxAudioBlockFrames ||
			!allFinite(block.Samples) {
			return fmt.Errorf("invalid FFmpeg audio block")
		}
		if len(v.audioQueue) >= MaxVideoAudioBlocks {
			return ErrVideoQueueFull
		}
		v.audioQueue = append(v.audioQueue, VideoAudioBlock{
			StartMS: block.StartMS, SampleRate: block.SampleRate, Channels: block.Channels,
			Samples: append([]float32(nil), block.Samples...),
		})
	}
	return nil
}

func (v *VideoOverlay) installSource(name string, meta VideoSource, movie *assets.Movie, decoder *ffmpeg.Decoder, initial *VideoFrame) error {
	if meta.Width == 0 || meta.Height == 0 || meta.Frames == 0 || !isFinite(meta.FPS) || meta.FPS <= 0 {
		return videoErr(name, ErrVideoUnsupported)
	}
	if meta.DurationMS == 0 {
		meta.DurationMS = uint64(float64(meta.Frames) * 1000 / meta.FPS)
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.ffmpeg != nil && v.ffmpeg != decoder {
		_ = v.ffmpeg.Close()
	}
	v.generation++
	v.events = nil
	v.source = &meta
	v.ffmpeg = decoder
	v.amv = movie
	v.positionMS = 0
	v.clockSet = false
	v.frameQueue = nil
	v.audioQueue = nil
	v.current = initial
	v.sidecarSkipped = false
	v.bounds.Width = int32(meta.Width)
	v.bounds.Height = int32(meta.Height)
	v.setStatusLocked(VideoStopped)
	return nil
}

func (v *VideoOverlay) setSourceMetadata(name string, meta VideoSource) error {
	return v.installSource(name, meta, nil, nil, nil)
}

// SetSourceMetadata installs metadata supplied by a host decoder. It does not
// fabricate any frame pixels; callers must enqueue decoded VideoFrame values.
func (v *VideoOverlay) SetSourceMetadata(meta VideoSource) error {
	if v == nil {
		return ErrVideoClosed
	}
	return v.setSourceMetadata(meta.Name, meta)
}

func (v *VideoOverlay) CurrentFrame() (VideoFrame, bool) {
	if v == nil {
		return VideoFrame{}, false
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.current == nil {
		return VideoFrame{}, false
	}
	f := *v.current
	f.RGBA = append([]byte(nil), f.RGBA...)
	return f, true
}

func (v *VideoOverlay) Close() {
	if v == nil {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.generation++
	v.source = nil
	if v.ffmpeg != nil {
		_ = v.ffmpeg.Close()
		v.ffmpeg = nil
	}
	v.amv = nil
	v.current = nil
	v.frameQueue = nil
	v.audioQueue = nil
	v.positionMS = 0
	v.clockSet = false
	v.events = v.events[:0]
	v.setStatusLocked(VideoUnloaded)
}
func (v *VideoOverlay) Play() error  { return v.transport(VideoPlaying) }
func (v *VideoOverlay) Pause() error { return v.transport(VideoPaused) }
func (v *VideoOverlay) Stop() error  { return v.transport(VideoStopped) }
func (v *VideoOverlay) transport(s VideoStatus) error {
	if err := v.ensure(); err != nil {
		return err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.source == nil {
		if s == VideoStopped || v.sidecarSkipped {
			return nil
		}
		return ErrVideoNoSource
	}
	v.setStatusLocked(s)
	return nil
}
func (v *VideoOverlay) Rewind() error { return v.Seek(0) }
func (v *VideoOverlay) Prepare() error {
	if err := v.Rewind(); err != nil {
		return err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.sidecarSkipped {
		return nil
	}
	v.emitLocked(VideoEvent{Generation: v.generation, Kind: "frame", Frame: 0})
	v.emitLocked(VideoEvent{Generation: v.generation, Kind: "period", Reason: 2})
	v.setStatusLocked(VideoPaused)
	return nil
}
func (v *VideoOverlay) Seek(positionMS uint64) error {
	if err := v.ensure(); err != nil {
		return err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.source == nil {
		if v.sidecarSkipped {
			return nil
		}
		return ErrVideoNoSource
	}
	if positionMS > v.source.DurationMS {
		positionMS = v.source.DurationMS
	}
	if v.ffmpeg != nil {
		if err := v.ffmpeg.Seek(positionMS); err != nil {
			return videoErr(v.source.Name, err)
		}
	}
	v.positionMS = float64(positionMS)
	v.current = nil
	return v.presentLocked()
}
func (v *VideoOverlay) SetFrame(frame uint64) error {
	if err := v.ensure(); err != nil {
		return err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.source == nil {
		return nil
	}
	if frame >= v.source.Frames {
		frame = v.source.Frames - 1
	}
	v.positionMS = float64(frame) * 1000 / v.source.FPS
	if v.ffmpeg != nil {
		if err := v.ffmpeg.Seek(uint64(v.positionMS)); err != nil {
			return videoErr(v.source.Name, err)
		}
	}
	v.current = nil
	return v.presentLocked()
}
func (v *VideoOverlay) SetRate(rate float64) error {
	if !isFinite(rate) || rate <= 0 || rate > 16 {
		return fmt.Errorf("unsupported movie playback rate %v", rate)
	}
	v.mu.Lock()
	v.rate = rate
	v.mu.Unlock()
	return nil
}
func (v *VideoOverlay) SetVolume(volume int32) error {
	if volume < 0 || volume > 100000 {
		return fmt.Errorf("audio volume out of range: %d", volume)
	}
	v.mu.Lock()
	v.volume = volume
	v.mu.Unlock()
	return nil
}
func (v *VideoOverlay) SetBalance(balance int32) error {
	if balance < -100000 || balance > 100000 {
		return fmt.Errorf("audio balance out of range: %d", balance)
	}
	v.mu.Lock()
	v.balance = balance
	v.mu.Unlock()
	return nil
}
func isFinite(x float64) bool { return !math.IsNaN(x) && !math.IsInf(x, 0) }
func (v *VideoOverlay) SetBounds(r Rect) error {
	if r.Width < 0 || r.Height < 0 {
		return fmt.Errorf("negative video bounds")
	}
	v.mu.Lock()
	v.bounds = r
	v.mu.Unlock()
	return nil
}
func (v *VideoOverlay) SetPosition(x, y int32) {
	if v != nil {
		v.mu.Lock()
		v.bounds.X = x
		v.bounds.Y = y
		v.mu.Unlock()
	}
}
func (v *VideoOverlay) SetSize(w, h int32) error {
	v.mu.Lock()
	r := v.bounds
	r.Width = w
	r.Height = h
	v.mu.Unlock()
	return v.SetBounds(r)
}
func (v *VideoOverlay) SetVisible(on bool) {
	if v != nil {
		v.mu.Lock()
		v.visible = on
		for _, l := range v.layers {
			if l != nil {
				l.SetVisible(on)
			}
		}
		v.mu.Unlock()
	}
}
func (v *VideoOverlay) SetLoop(on bool) {
	if v != nil {
		v.mu.Lock()
		v.looping = on
		v.mu.Unlock()
	}
}
func (v *VideoOverlay) SetMode(mode int32) {
	if v != nil {
		v.mu.Lock()
		v.mode = mode
		v.mu.Unlock()
	}
}
func (v *VideoOverlay) SetSegmentLoop(start, end int64) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.source == nil {
		return nil
	}
	if start < 0 || end <= start {
		return fmt.Errorf("invalid video segment %d..%d", start, end)
	}
	v.segment = [2]int64{start, end}
	return nil
}
func (v *VideoOverlay) CancelSegmentLoop() {
	if v != nil {
		v.mu.Lock()
		v.segment = [2]int64{-1, -1}
		v.mu.Unlock()
	}
}
func (v *VideoOverlay) SetPeriodEvent(frame int64) {
	if v != nil {
		v.mu.Lock()
		v.period = frame
		v.mu.Unlock()
	}
}
func (v *VideoOverlay) CancelPeriodEvent() {
	if v != nil {
		v.mu.Lock()
		v.period = -1
		v.mu.Unlock()
	}
}
func (v *VideoOverlay) SetLayer(index int, layer *Layer) error {
	if index < 0 || index > 1 {
		return fmt.Errorf("video layer index out of range")
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if layer != nil && layer.ID == 0 {
		return fmt.Errorf("video layer has invalid id")
	}
	v.layers[index] = layer
	return v.presentLocked()
}
func (v *VideoOverlay) SetLayerTarget(index int, layer *Layer) error { return v.SetLayer(index, layer) }
func (v *VideoOverlay) Layer(index int) *Layer {
	if v == nil || index < 0 || index > 1 {
		return nil
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.layers[index]
}

func (v *VideoOverlay) QueueFrame(frame VideoFrame) error {
	if v == nil {
		return ErrVideoClosed
	}
	if frame.Width <= 0 || frame.Height <= 0 || len(frame.RGBA) != int(frame.Width)*int(frame.Height)*4 {
		return fmt.Errorf("invalid video frame dimensions")
	}
	frame.RGBA = append([]byte(nil), frame.RGBA...)
	v.mu.Lock()
	defer v.mu.Unlock()
	if len(v.frameQueue) >= MaxVideoFrames {
		return ErrVideoQueueFull
	}
	v.frameQueue = append(v.frameQueue, frame)
	return nil
}
func (v *VideoOverlay) QueueAudio(block VideoAudioBlock) error {
	if v == nil {
		return ErrVideoClosed
	}
	if block.Channels == 0 || block.SampleRate == 0 || len(block.Samples)%int(block.Channels) != 0 {
		return fmt.Errorf("invalid video audio block")
	}
	if !allFinite(block.Samples) {
		return fmt.Errorf("non-finite video audio sample")
	}
	block.Samples = append([]float32(nil), block.Samples...)
	v.mu.Lock()
	defer v.mu.Unlock()
	if len(v.audioQueue) >= MaxVideoAudioBlocks {
		return ErrVideoQueueFull
	}
	v.audioQueue = append(v.audioQueue, block)
	return nil
}
func allFinite(xs []float32) bool {
	for _, x := range xs {
		if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) {
			return false
		}
	}
	return true
}
func (v *VideoOverlay) AudioBlocks() []VideoAudioBlock {
	if v == nil {
		return nil
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	out := make([]VideoAudioBlock, len(v.audioQueue))
	for i, b := range v.audioQueue {
		out[i] = b
		out[i].Samples = append([]float32(nil), b.Samples...)
	}
	return out
}
func (v *VideoOverlay) presentLocked() error {
	idx := v.frameAtLocked()
	shouldPresent := false
	if v.ffmpeg != nil {
		frame, audio, err := v.ffmpeg.DecodeUntil(uint64(v.positionMS))
		if err != nil {
			return videoErr(v.source.Name, err)
		}
		if err := v.appendFFmpegAudioLocked(audio); err != nil {
			return videoErr(v.source.Name, err)
		}
		if frame != nil {
			frame.Index = idx
			v.current = &VideoFrame{Index: frame.Index, Width: frame.Width, Height: frame.Height, PTSMS: frame.PTSMS, RGBA: frame.RGBA}
		}
		shouldPresent = v.current != nil
	} else if v.amv != nil {
		if v.current == nil || v.current.Index != idx {
			image, err := v.amv.DecodePacket(int(idx))
			if err != nil {
				return videoErr(v.source.Name, err)
			}
			frame, err := videoFrameFromAMV(idx, image)
			if err != nil {
				return videoErr(v.source.Name, err)
			}
			if err := validateAMVFrame(v.source, frame); err != nil {
				return videoErr(v.source.Name, err)
			}
			v.current = frame
		}
		shouldPresent = v.current != nil
	} else if v.current != nil && v.current.Index == idx {
		shouldPresent = true
	} else {
		for i := range v.frameQueue {
			if v.frameQueue[i].Index == idx {
				f := v.frameQueue[i]
				v.current = &f
				shouldPresent = true
				break
			}
		}
	}
	if !shouldPresent || v.current == nil {
		return nil
	}
	for _, l := range v.layers {
		if l != nil {
			if err := l.SetRGBA(v.current.Width, v.current.Height, v.current.RGBA); err != nil {
				return err
			}
		}
	}
	return nil
}

func (v *VideoOverlay) SetClock(timeMS uint64) {
	if v != nil {
		v.mu.Lock()
		v.clockMS = timeMS
		v.clockSet = true
		v.mu.Unlock()
	}
}

// Advance advances against an absolute deterministic millisecond clock.
func (v *VideoOverlay) Advance(timeMS uint64) error {
	if err := v.ensure(); err != nil {
		return err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.clockSet {
		v.clockSet = true
		v.clockMS = timeMS
		return nil
	}
	if timeMS < v.clockMS {
		return ErrClockBackwards
	}
	delta := float64(timeMS-v.clockMS) * v.rate
	v.clockMS = timeMS
	if v.status != VideoPlaying || v.source == nil {
		return nil
	}
	old := v.frameAtLocked()
	v.positionMS += delta
	duration := float64(v.source.DurationMS)
	if duration <= 0 {
		return nil
	}
	periodReason := -1
	if v.segment[1] > 0 && v.source.FPS > 0 {
		start := float64(max64(v.segment[0], 0)) * 1000 / v.source.FPS
		end := float64(v.segment[1]) * 1000 / v.source.FPS
		if end > start && v.positionMS >= end {
			v.positionMS = start + math.Mod(v.positionMS-end, end-start)
			periodReason = 3
		}
	} else if v.positionMS >= duration && v.looping {
		v.positionMS = math.Mod(v.positionMS, duration)
		periodReason = 0
	}
	ended := v.positionMS >= duration
	now := v.frameAtLocked()
	if now != old || (v.amv != nil && (v.current == nil || v.current.Index != now)) || v.ffmpeg != nil {
		if err := v.presentLocked(); err != nil {
			return err
		}
	}
	if periodReason >= 0 {
		v.emitLocked(VideoEvent{Generation: v.generation, Kind: "period", Reason: periodReason})
	}
	if now != old {
		v.emitLocked(VideoEvent{Generation: v.generation, Kind: "frame", Frame: now})
	}
	if v.period >= 0 && int64(now) >= v.period {
		v.period = -1
		v.emitLocked(VideoEvent{Generation: v.generation, Kind: "period", Reason: 1})
	}
	if ended {
		v.setStatusLocked(VideoStopped)
	}
	return nil
}

// RegisterVideo attaches a host-created overlay to a stable script object.
func (s *Services) RegisterVideo(id tjs.ObjectID, video *VideoOverlay) error {
	if s == nil || id == 0 || video == nil {
		return fmt.Errorf("invalid video registration")
	}
	s.ensureInit()
	if s.Videos == nil {
		s.Videos = make(map[tjs.ObjectID]*VideoOverlay)
	}
	previous := s.Videos[id]
	if previous != nil && previous != video {
		s.AudioMixer.RemoveVideo(previous)
		previous.Close()
	}
	video.SetClock(s.TimeMS)
	s.Videos[id] = video
	s.AudioMixer.AddVideo(video)
	return nil
}

func (s *Services) Video(id tjs.ObjectID) *VideoOverlay {
	if s == nil {
		return nil
	}
	s.ensureInit()
	return s.Videos[id]
}

// VideoNative invokes the script-facing operation set used by VideoOverlay.
func (s *Services) VideoNative(vm *tjs.VM, context tjs.Value, operation string, args []tjs.Value, budget *uint64) (result tjs.Value, callErr error) {
	if context.Kind() != tjs.KindObject {
		return tjs.VoidValue, fmt.Errorf("VideoOverlay requires object context")
	}
	id := context.ObjectRef().Object
	if id == 0 {
		return tjs.VoidValue, fmt.Errorf("VideoOverlay requires non-null context")
	}
	s.ensureInit()
	if s.Videos == nil {
		s.Videos = make(map[tjs.ObjectID]*VideoOverlay)
	}
	v := s.Videos[id]
	if operation == "@initialize" {
		if v == nil {
			v = NewVideoOverlay()
			s.Videos[id] = v
			s.AudioMixer.AddVideo(v)
		}
		return tjs.VoidValue, nil
	}
	if operation == "@invalidate" {
		s.AudioMixer.RemoveVideo(v)
		if v != nil {
			v.Close()
		}
		delete(s.Videos, id)
		return tjs.VoidValue, nil
	}
	if v == nil {
		v = NewVideoOverlay()
		s.Videos[id] = v
		s.AudioMixer.AddVideo(v)
	}
	defer func() {
		if callErr != nil {
			return
		}
		if err := s.dispatchVideoEvents(vm, id, budget); err != nil {
			result = tjs.VoidValue
			callErr = err
		}
	}()
	arg := func(i int) (tjs.Value, error) {
		if i >= len(args) {
			return tjs.VoidValue, fmt.Errorf("VideoOverlay: missing argument %d", i)
		}
		return args[i], nil
	}
	intArg := func(i int) (int64, error) {
		x, err := arg(i)
		if err != nil {
			return 0, err
		}
		return x.Int()
	}
	switch operation {
	case "VideoOverlay", "AlphaMovie":
		return tjs.VoidValue, nil
	case "open":
		x, err := arg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		name := x.Text()
		data, err := s.readStorage(strings.SplitN(name, "?", 2)[0])
		if err != nil {
			return tjs.VoidValue, videoErr(name, err)
		}
		if err := v.Open(name, data); err != nil {
			return tjs.VoidValue, err
		}
		v.SetClock(s.TimeMS)
		return tjs.VoidValue, nil
	case "play":
		return tjs.VoidValue, v.Play()
	case "pause":
		return tjs.VoidValue, v.Pause()
	case "stop":
		return tjs.VoidValue, v.Stop()
	case "close", "finalize":
		v.Close()
		return tjs.VoidValue, nil
	case "rewind":
		return tjs.VoidValue, v.Rewind()
	case "prepare":
		return tjs.VoidValue, v.Prepare()
	case "setPos":
		x, err := intArg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		y, err := intArg(1)
		if err != nil {
			return tjs.VoidValue, err
		}
		v.SetPosition(int32(x), int32(y))
		return tjs.VoidValue, nil
	case "set:left":
		x, err := intArg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		r := v.Bounds()
		r.X = int32(x)
		return tjs.VoidValue, v.SetBounds(r)
	case "set:top":
		y, err := intArg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		r := v.Bounds()
		r.Y = int32(y)
		return tjs.VoidValue, v.SetBounds(r)
	case "set:width":
		w, err := intArg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		r := v.Bounds()
		r.Width = int32(w)
		return tjs.VoidValue, v.SetBounds(r)
	case "set:height":
		h, err := intArg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		r := v.Bounds()
		r.Height = int32(h)
		return tjs.VoidValue, v.SetBounds(r)
	case "setSize":
		w, err := intArg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		h, err := intArg(1)
		if err != nil {
			return tjs.VoidValue, err
		}
		return tjs.VoidValue, v.SetSize(int32(w), int32(h))
	case "setBounds":
		x, err := intArg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		y, err := intArg(1)
		if err != nil {
			return tjs.VoidValue, err
		}
		w, err := intArg(2)
		if err != nil {
			return tjs.VoidValue, err
		}
		h, err := intArg(3)
		if err != nil {
			return tjs.VoidValue, err
		}
		return tjs.VoidValue, v.SetBounds(Rect{X: int32(x), Y: int32(y), Width: int32(w), Height: int32(h)})
	case "setSegmentLoop":
		a, err := intArg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		b, err := intArg(1)
		if err != nil {
			return tjs.VoidValue, err
		}
		return tjs.VoidValue, v.SetSegmentLoop(a, b)
	case "cancelSegmentLoop":
		v.CancelSegmentLoop()
		return tjs.VoidValue, nil
	case "setPeriodEvent":
		if len(args) == 0 {
			v.CancelPeriodEvent()
			return tjs.VoidValue, nil
		}
		n, err := intArg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		v.SetPeriodEvent(n)
		return tjs.VoidValue, nil
	case "cancelPeriodEvent":
		v.CancelPeriodEvent()
		return tjs.VoidValue, nil
	case "set:position":
		n, err := intArg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		if n < 0 {
			n = 0
		}
		return tjs.VoidValue, v.Seek(uint64(n))
	case "set:frame":
		n, err := intArg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		if n < 0 {
			n = 0
		}
		return tjs.VoidValue, v.SetFrame(uint64(n))
	case "set:playRate":
		x, err := arg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		r, err := x.Real()
		if err != nil {
			return tjs.VoidValue, err
		}
		return tjs.VoidValue, v.SetRate(r)
	case "set:audioVolume":
		n, err := intArg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		return tjs.VoidValue, v.SetVolume(int32(n))
	case "set:segmentLoopStartFrame", "set:segmentLoopEndFrame":
		n, err := intArg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		a, b := v.SegmentLoop()
		if operation == "set:segmentLoopStartFrame" {
			a = n
		} else {
			b = n
		}
		return tjs.VoidValue, v.SetSegmentLoop(a, b)
	case "set:periodEventFrame":
		n, err := intArg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		v.SetPeriodEvent(n)
		return tjs.VoidValue, nil
	case "set:audioBalance":
		n, err := intArg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		return tjs.VoidValue, v.SetBalance(int32(n))
	case "set:visible":
		x, err := arg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		b, err := x.Truth()
		if err != nil {
			return tjs.VoidValue, err
		}
		v.SetVisible(b)
		return tjs.VoidValue, nil
	case "set:loop":
		x, err := arg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		b, err := x.Truth()
		if err != nil {
			return tjs.VoidValue, err
		}
		v.SetLoop(b)
		return tjs.VoidValue, nil
	case "set:mode":
		n, err := intArg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		v.SetMode(int32(n))
		return tjs.VoidValue, nil
	case "get:position":
		return tjs.IntegerValue(int64(v.Position())), nil
	case "get:frame":
		return tjs.IntegerValue(int64(v.Frame())), nil
	case "get:originalWidth", "get:originalHeight", "get:fps", "get:numberOfFrame", "get:totalTime":
		m, ok := v.Source()
		if !ok {
			return tjs.IntegerValue(0), nil
		}
		switch operation {
		case "get:originalWidth":
			return tjs.IntegerValue(int64(m.Width)), nil
		case "get:originalHeight":
			return tjs.IntegerValue(int64(m.Height)), nil
		case "get:fps":
			return tjs.RealValue(m.FPS), nil
		case "get:numberOfFrame":
			return tjs.IntegerValue(int64(m.Frames)), nil
		default:
			return tjs.IntegerValue(int64(m.DurationMS)), nil
		}
	case "get:playRate":
		return tjs.RealValue(v.Rate()), nil
	case "get:audioVolume":
		return tjs.IntegerValue(int64(v.Volume())), nil
	case "get:audioBalance":
		return tjs.IntegerValue(int64(v.Balance())), nil
	case "get:left":
		return tjs.IntegerValue(int64(v.Bounds().X)), nil
	case "get:top":
		return tjs.IntegerValue(int64(v.Bounds().Y)), nil
	case "get:width":
		return tjs.IntegerValue(int64(v.Bounds().Width)), nil
	case "get:height":
		return tjs.IntegerValue(int64(v.Bounds().Height)), nil
	case "get:visible":
		return tjs.IntegerValue(boolInt(v.Visible())), nil
	case "get:loop":
		return tjs.IntegerValue(boolInt(v.Looping())), nil
	case "get:mode":
		return tjs.IntegerValue(int64(v.Mode())), nil
	case "get:segmentLoopStartFrame":
		a, _ := v.SegmentLoop()
		return tjs.IntegerValue(a), nil
	case "get:segmentLoopEndFrame":
		_, b := v.SegmentLoop()
		return tjs.IntegerValue(b), nil
	case "set:layer1", "set:layer2":
		value, err := arg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		index := 0
		if operation == "set:layer2" {
			index = 1
		}
		var layer *Layer
		if !value.IsVoid() && !(value.Kind() == tjs.KindObject && value.IsNull()) {
			if value.Kind() != tjs.KindObject {
				return tjs.VoidValue, fmt.Errorf("VideoOverlay.%s requires a Layer or null", operation)
			}
			layer = s.Layer(value.ObjectRef().Object)
			if layer == nil {
				return tjs.VoidValue, fmt.Errorf("VideoOverlay.%s requires a Layer object", operation)
			}
		}
		return tjs.VoidValue, v.SetLayer(index, layer)
	case "get:layer1", "get:layer2":
		index := 0
		if operation == "get:layer2" {
			index = 1
		}
		return layerValue(v.Layer(index)), nil
	case "get:periodEventFrame":
		return tjs.IntegerValue(v.PeriodEvent()), nil
	case "setMixingLayer", "resetMixingLayer", "set:mixingMovieAlpha", "set:mixingMovieBGColor", "set:contrast", "set:brightness", "set:hue", "set:saturation", "selectAudioStream":
		return tjs.VoidValue, videoErr("", ErrVideoUnsupported)
	case "onStatusChanged", "onCallbackCommand", "onPeriod", "onFrameUpdate":
		return tjs.VoidValue, nil
	case "get:numberOfAudioStream":
		return tjs.IntegerValue(0), nil
	case "get:enabledAudioStream", "get:enabledVideoStream":
		return tjs.IntegerValue(0), nil
	case "get:numberOfVideoStream":
		return tjs.IntegerValue(1), nil
	default:
		return tjs.VoidValue, fmt.Errorf("unknown VideoOverlay operation: %s", operation)
	}
}
func (s *Services) dispatchVideoEvents(vm *tjs.VM, id tjs.ObjectID, budget *uint64) error {
	video := s.Video(id)
	if video == nil {
		return nil
	}
	for {
		events := video.Events()
		if len(events) == 0 {
			return nil
		}
		for _, event := range events {
			if event.Generation != video.Generation() {
				continue
			}
			object, exists := vm.Objects.Lookup(id)
			if !exists || !object.IsValid() {
				continue
			}
			name := ""
			var args []tjs.Value
			switch event.Kind {
			case "status":
				name = "onStatusChanged"
				args = []tjs.Value{tjs.StringValue(string(event.Status))}
			case "frame":
				name = "onFrameUpdate"
				args = []tjs.Value{tjs.IntegerValue(int64(event.Frame))}
			case "period":
				name = "onPeriod"
				args = []tjs.Value{tjs.IntegerValue(int64(event.Reason))}
			default:
				continue
			}
			receiver := tjs.ObjectValue(tjs.NewObjectRef(id))
			fn, err := vm.GetMember(receiver, name, s, budget)
			if err != nil {
				return err
			}
			if fn.IsVoid() {
				continue
			}
			if _, err := vm.CallEventHandler(fn, receiver, args, s, budget); err != nil {
				return err
			}
		}
	}
}

func boolInt(v bool) int64 {
	if v {
		return 1
	}
	return 0
}
