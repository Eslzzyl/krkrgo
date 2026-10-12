package runtime

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"

	"github.com/eslzzyl/krkrgo/internal/assets"
	"github.com/eslzzyl/krkrgo/internal/tjs"
)

const (
	MaxVideoFrames      = 8
	MaxVideoAudioBlocks = 32
	MaxVideoSourceBytes = 256 * 1024 * 1024
	MaxVideoFrameBytes  = 64 * 1024 * 1024
)

var (
	ErrVideoClosed                            = errors.New("video overlay is closed")
	ErrVideoNoSource                          = errors.New("video overlay has no source")
	ErrVideoUnsupported                       = errors.New("unsupported video")
	ErrVideoWindowsMediaBackendUnavailable    = errors.New("Windows Media ASF/WMV backend unavailable")
	ErrVideoWindowsMediaCodecUnsupported      = errors.New("unsupported Windows Media stream codec")
	ErrVideoWindowsMediaStreamInfoUnavailable = errors.New("Windows Media stream information unavailable")
	ErrVideoQueueFull                         = errors.New("video frame queue is full")
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

// VideoSource is bounded metadata parsed from an MPEG/AMV source.
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

// VideoFrame is decoded RGBA. MPEG frames are converted from the assets
// decoder's progressive 4:2:0 planar output using BT.601 limited-range math.
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
// retain their checked movie and one decoded frame; MPEG sources retain at
// most MaxVideoFrames converted RGBA frames. Audio and host-queued frames are
// independently bounded by their respective limits.
type VideoOverlay struct {
	mu               sync.Mutex
	owner            tjs.ObjectID
	source           *VideoSource
	windowsMediaStub bool
	status           VideoStatus
	amv              *assets.Movie
	mpeg             []VideoFrame
	positionMS       float64
	rate             float64
	volume           int32
	balance          int32
	bounds           Rect
	visible          bool
	looping          bool
	mode             int32
	layers           [2]*Layer
	segment          [2]int64
	period           int64
	generation       uint64
	events           []VideoEvent
	frameQueue       []VideoFrame
	audioQueue       []VideoAudioBlock
	current          *VideoFrame
	clockMS          uint64
	clockSet         bool
	callback         func(VideoEvent)
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
	if isWindowsMediaSource(name, data) {
		return v.installWindowsMediaStub(name)
	}
	if len(data) >= 4 && string(data[:4]) == "AJPM" {
		// Movie.Data is intentionally retained by the overlay. Copy the input
		// once so callers may reuse their storage after Open.
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
	meta, mpeg, err := decodeMPEGSource(name, data)
	if err != nil {
		return err
	}
	return v.installSource(name, meta, nil, mpeg, &mpeg[0])
}

// installWindowsMediaStub accepts Windows Media sources while their decoder is
// pending. It keeps the runtime transport callable without presenting pixels.
func (v *VideoOverlay) installWindowsMediaStub(name string) error {
	if err := v.ensure(); err != nil {
		return err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.generation++
	v.events = nil
	v.source = nil
	v.amv = nil
	v.mpeg = nil
	v.positionMS = 0
	v.clockSet = false
	v.frameQueue = nil
	v.audioQueue = nil
	v.current = nil
	v.windowsMediaStub = true
	v.bounds = Rect{}
	v.setStatusLocked(VideoStopped)
	return nil
}

func isWindowsMediaSource(name string, data []byte) bool {
	// ASF Header Object GUID (the signature used by ASF, including WMV files).
	const asfHeader = "\x30\x26\xb2\x75\x8e\x66\xcf\x11\xa6\xd9\x00\xaa\x00\x62\xce\x6c"
	if len(data) >= len(asfHeader) && string(data[:len(asfHeader)]) == asfHeader {
		return true
	}
	lowerName := strings.ToLower(strings.TrimSpace(name))
	for _, suffix := range []string{".asf", ".wma", ".wmv"} {
		if strings.HasSuffix(lowerName, suffix) {
			return true
		}
	}
	return false
}

// windowsMediaStreamCodecs extracts only codec identifiers from bounded ASF
// Stream Properties Objects. It does not demux packets or imply decoder support.
func windowsMediaStreamCodecs(data []byte) []string {
	const (
		headerGUID           = "\x30\x26\xb2\x75\x8e\x66\xcf\x11\xa6\xd9\x00\xaa\x00\x62\xce\x6c"
		streamPropertiesGUID = "\x91\x07\xdc\xb7\xb7\xa9\xcf\x11\x8e\xe6\x00\xc0\x0c\x20\x53\x65"
		videoMediaGUID       = "\xc0\xef\x19\xbc\x4d\x5b\xcf\x11\xa8\xfd\x00\x80\x5f\x5c\x44\x2b"
		audioMediaGUID       = "\x40\x9e\x69\xf8\x4d\x5b\xcf\x11\xa8\xfd\x00\x80\x5f\x5c\x44\x2b"
	)
	if len(data) < 30 || string(data[:16]) != headerGUID {
		return nil
	}
	headerSize := binary.LittleEndian.Uint64(data[16:24])
	if headerSize > uint64(len(data)) || headerSize < 30 {
		return nil
	}
	count := binary.LittleEndian.Uint32(data[24:28])
	offset := uint64(30)
	codecs := make([]string, 0, 2)
	for i := uint32(0); i < count && offset+24 <= headerSize; i++ {
		objectSize := binary.LittleEndian.Uint64(data[offset+16 : offset+24])
		if objectSize < 24 || objectSize > headerSize-offset {
			return codecs
		}
		if string(data[offset:offset+16]) == streamPropertiesGUID && objectSize >= 78 {
			streamType := string(data[offset+24 : offset+40])
			typeSize := uint64(binary.LittleEndian.Uint32(data[offset+64 : offset+68]))
			streamNumber := binary.LittleEndian.Uint16(data[offset+72:offset+74]) & 0x7f
			typeStart := offset + 78
			if typeSize <= objectSize-78 {
				switch streamType {
				case videoMediaGUID:
					codec := fmt.Sprintf("video stream %d with unknown codec", streamNumber)
					// Video media type data: width, height, reserved byte,
					// BITMAPINFOHEADER size, then its compression FOURCC.
					if typeSize >= 31 {
						fourcc := strings.TrimRight(string(data[typeStart+27:typeStart+31]), "\x00 ")
						if fourcc != "" {
							codec = fmt.Sprintf("video stream %d codec %s", streamNumber, fourcc)
						}
					}
					codecs = append(codecs, codec)
				case audioMediaGUID:
					codec := fmt.Sprintf("audio stream %d with unknown codec", streamNumber)
					if typeSize >= 2 {
						tag := binary.LittleEndian.Uint16(data[typeStart : typeStart+2])
						switch tag {
						case 0x0161:
							codec = fmt.Sprintf("audio stream %d codec WMA2 (0x0161)", streamNumber)
						case 0x0162:
							codec = fmt.Sprintf("audio stream %d codec WMA Pro (0x0162)", streamNumber)
						case 0x0163:
							codec = fmt.Sprintf("audio stream %d codec WMA Lossless (0x0163)", streamNumber)
						default:
							codec = fmt.Sprintf("audio stream %d codec tag 0x%04x", streamNumber, tag)
						}
					}
					codecs = append(codecs, codec)
				}
			}
		}
		offset += objectSize
	}
	return codecs
}

func decodeMPEGSource(name string, data []byte) (VideoSource, []VideoFrame, error) {
	s, err := assets.ParseMPEGStream(data)
	if err != nil {
		return VideoSource{}, nil, videoErr(name, err)
	}
	if s.Sequence == nil {
		return VideoSource{}, nil, videoErr(name, ErrVideoUnsupported)
	}
	fps := float64(s.Sequence.FrameRateNumerator) / float64(s.Sequence.FrameRateDenominator)
	if fps <= 0 {
		return VideoSource{}, nil, videoErr(name, ErrVideoUnsupported)
	}
	decoded, err := assets.DecodeMPEG2(data)
	if err != nil {
		return VideoSource{}, nil, videoErr(name, err)
	}
	if len(decoded) == 0 {
		return VideoSource{}, nil, videoErr(name, ErrVideoUnsupported)
	}
	if len(decoded) > MaxVideoFrames {
		return VideoSource{}, nil, videoErr(name, fmt.Errorf("%w: decoded frame count %d exceeds %d", ErrVideoUnsupported, len(decoded), MaxVideoFrames))
	}
	frames := make([]VideoFrame, 0, len(decoded))
	for i := range decoded {
		if decoded[i].Width != s.Sequence.Width || decoded[i].Height != s.Sequence.Height {
			return VideoSource{}, nil, videoErr(name, fmt.Errorf("MPEG frame dimensions do not match sequence"))
		}
		frame, e := videoFrameFromMPEG(uint64(i), &decoded[i])
		if e != nil {
			return VideoSource{}, nil, videoErr(name, e)
		}
		frames = append(frames, *frame)
	}
	meta := VideoSource{Name: name, Format: "MPEG", Width: s.Sequence.Width, Height: s.Sequence.Height,
		FPS: fps, Frames: uint64(len(frames)), DurationMS: uint64(float64(len(frames)) * 1000 / fps)}
	return meta, frames, nil
}

func videoFrameFromMPEG(index uint64, frame *assets.MPEGFrame) (*VideoFrame, error) {
	if frame == nil || frame.Format != assets.MPEGYUV420 || !frame.Progressive || frame.PictureType != 1 {
		return nil, fmt.Errorf("%w: MPEG frame is not a progressive I-frame 4:2:0 picture", ErrVideoUnsupported)
	}
	if frame.Width == 0 || frame.Height == 0 || frame.Width > math.MaxInt32 || frame.Height > math.MaxInt32 {
		return nil, fmt.Errorf("invalid MPEG frame dimensions")
	}
	w, h := int(frame.Width), int(frame.Height)
	pixels := uint64(w) * uint64(h)
	if pixels > uint64(MaxVideoFrameBytes/4) || pixels > uint64(int(^uint(0)>>1))/4 {
		return nil, fmt.Errorf("MPEG frame dimensions exceed memory limit")
	}
	cw, ch := (w+1)/2, (h+1)/2
	yPlane, uPlane, vPlane := frame.Y, frame.U, frame.V
	if len(yPlane.Data) == 0 && len(frame.Planes[0].Data) != 0 {
		yPlane, uPlane, vPlane = frame.Planes[0], frame.Planes[1], frame.Planes[2]
	}
	if err := validateMPEGPlane(yPlane, w, h, "Y"); err != nil {
		return nil, err
	}
	if err := validateMPEGPlane(uPlane, cw, ch, "U"); err != nil {
		return nil, err
	}
	if err := validateMPEGPlane(vPlane, cw, ch, "V"); err != nil {
		return nil, err
	}
	rgba := make([]byte, int(pixels)*4)
	// BT.601 limited-range: C=Y-16, D=U-128, E=V-128;
	// R=(298C+409E+128)>>8, G=(298C-100D-208E+128)>>8,
	// B=(298C+516D+128)>>8, with each result clipped to 0..255.
	for y := range h {
		for x := range w {
			yv := int(yPlane.Data[y*yPlane.Stride+x]) - 16
			uv := int(uPlane.Data[(y/2)*uPlane.Stride+x/2]) - 128
			vv := int(vPlane.Data[(y/2)*vPlane.Stride+x/2]) - 128
			r := (298*yv + 409*vv + 128) >> 8
			g := (298*yv - 100*uv - 208*vv + 128) >> 8
			b := (298*yv + 516*uv + 128) >> 8
			o := (y*w + x) * 4
			rgba[o], rgba[o+1], rgba[o+2], rgba[o+3] = clipMPEGByte(r), clipMPEGByte(g), clipMPEGByte(b), 255
		}
	}
	ptsMS := uint64(0)
	if frame.HasPTS {
		// MPEG timestamps are 90 kHz; VideoFrame timestamps are milliseconds.
		ptsMS = frame.PTS / 90
	}
	return &VideoFrame{Index: index, Width: int32(w), Height: int32(h), PTSMS: ptsMS, RGBA: rgba}, nil
}

func validateMPEGPlane(plane assets.MPEGPixelPlane, width, height int, name string) error {
	if plane.Width != width || plane.Height != height || plane.Stride < width || plane.Stride <= 0 {
		return fmt.Errorf("invalid MPEG %s plane dimensions", name)
	}
	need := uint64(plane.Stride)*uint64(height-1) + uint64(width)
	if need > uint64(len(plane.Data)) {
		return fmt.Errorf("invalid MPEG %s plane data", name)
	}
	return nil
}

func clipMPEGByte(value int) byte {
	if value < 0 {
		return 0
	}
	if value > 255 {
		return 255
	}
	return byte(value)
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

func (v *VideoOverlay) installSource(name string, meta VideoSource, movie *assets.Movie, mpeg []VideoFrame, initial *VideoFrame) error {
	if meta.Width == 0 || meta.Height == 0 || meta.Frames == 0 || !isFinite(meta.FPS) || meta.FPS <= 0 {
		return videoErr(name, ErrVideoUnsupported)
	}
	if meta.DurationMS == 0 {
		meta.DurationMS = uint64(float64(meta.Frames) * 1000 / meta.FPS)
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.generation++
	v.events = nil
	v.source = &meta
	v.windowsMediaStub = false
	v.amv = movie
	v.mpeg = mpeg
	v.positionMS = 0
	v.clockSet = false
	v.frameQueue = nil
	v.audioQueue = nil
	v.current = initial
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
	v.windowsMediaStub = false
	v.amv = nil
	v.mpeg = nil
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
	if v.windowsMediaStub {
		v.setStatusLocked(VideoStopped)
		return nil
	}
	if v.source == nil {
		if s == VideoStopped {
			return nil
		}
		return ErrVideoNoSource
	}
	v.setStatusLocked(s)
	return nil
}
func (v *VideoOverlay) Rewind() error { return v.Seek(0) }
func (v *VideoOverlay) Prepare() error {
	if v.windowsMediaStub {
		return nil
	}
	if err := v.Rewind(); err != nil {
		return err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
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
	if v.windowsMediaStub {
		return nil
	}
	if v.source == nil {
		return ErrVideoNoSource
	}
	if positionMS > v.source.DurationMS {
		positionMS = v.source.DurationMS
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
	if v.windowsMediaStub {
		return nil
	}
	if v.source == nil {
		return ErrVideoNoSource
	}
	if frame >= v.source.Frames {
		frame = v.source.Frames - 1
	}
	v.positionMS = float64(frame) * 1000 / v.source.FPS
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
	if v.amv != nil {
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
	} else if v.mpeg != nil {
		if idx < uint64(len(v.mpeg)) {
			v.current = &v.mpeg[idx]
			shouldPresent = true
		}
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
	if now != old || (v.amv != nil && (v.current == nil || v.current.Index != now)) {
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
	video.SetClock(s.TimeMS)
	s.Videos[id] = video
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
			s.Videos[id] = NewVideoOverlay()
		}
		return tjs.VoidValue, nil
	}
	if operation == "@invalidate" {
		delete(s.Videos, id)
		return tjs.VoidValue, nil
	}
	if v == nil {
		v = NewVideoOverlay()
		s.Videos[id] = v
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
