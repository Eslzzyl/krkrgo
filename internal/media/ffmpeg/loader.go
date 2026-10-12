package ffmpeg

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/ebitengine/purego"
)

const (
	avutilABIMajor  = 61
	avcodecABIMajor = 63
	avformatABIMajor = 63
	swscaleABIMajor = 10
)

var (
	ErrUnavailable = errors.New("FFmpeg shared libraries unavailable")
	ErrABIMismatch = errors.New("FFmpeg ABI mismatch")
	ErrClosed      = errors.New("FFmpeg decoder is closed")
	ErrMalformed   = errors.New("FFmpeg media input is malformed")
	ErrUnsupported = errors.New("FFmpeg media stream is unsupported")
)

// LoadError retains the library, candidate paths, and underlying loader error.
type LoadError struct {
	Library   string
	Candidates []string
	Err       error
}

func (e *LoadError) Error() string {
	if e == nil {
		return "FFmpeg library load error"
	}
	return fmt.Sprintf("FFmpeg %s: %v (candidates: %s)", e.Library, e.Err, strings.Join(e.Candidates, ", "))
}
func (e *LoadError) Unwrap() error { if e == nil { return nil }; return e.Err }

// Options controls dynamic-library discovery.
type Options struct {
	SearchPaths []string
}

// Loader owns one ABI-compatible FFmpeg library set.
type Loader struct {
	mu       sync.Mutex
	libs     map[string]sharedLibrary
	api      api
	closed   bool
}

var defaultLoader struct {
	once sync.Once
	load *Loader
	err  error
}

// Open loads avutil, avcodec, avformat, and swscale from one runtime library set.
func Open(opts Options) (*Loader, error) {
	l := &Loader{libs: make(map[string]sharedLibrary)}
	paths := searchPaths(opts.SearchPaths)
	for _, component := range []string{"avutil", "avcodec", "avformat", "swscale"} {
		lib, err := loadComponent(component, paths)
		if err != nil {
			l.closeLoaded()
			return nil, err
		}
		l.libs[component] = lib
	}
	if err := l.api.bind(l.libs); err != nil {
		l.closeLoaded()
		return nil, err
	}
	if err := l.api.checkABI(); err != nil {
		l.closeLoaded()
		return nil, err
	}
	return l, nil
}

// DefaultLoader uses the explicit directory, executable sidecar, and platform search paths.
func DefaultLoader() (*Loader, error) {
	defaultLoader.once.Do(func() {
		defaultLoader.load, defaultLoader.err = Open(Options{})
	})
	return defaultLoader.load, defaultLoader.err
}

func (l *Loader) closeLoaded() {
	for _, lib := range l.libs {
		_ = lib.close()
	}
	l.libs = nil
}

// Close releases the four dynamic-library handles. Existing decoders must be closed first.
func (l *Loader) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	for _, lib := range l.libs {
		if err := lib.close(); err != nil {
			return err
		}
	}
	l.libs = nil
	return nil
}

func (l *Loader) checkOpen() error {
	if l == nil {
		return ErrUnavailable
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return ErrClosed
	}
	return nil
}

func searchPaths(explicit []string) []string {
	paths := append([]string(nil), explicit...)
	if env := strings.TrimSpace(os.Getenv("KRKRGO_FFMPEG_DIR")); env != "" {
		paths = append(paths, env)
	}
	if executable, err := os.Executable(); err == nil {
		paths = append(paths, filepath.Join(filepath.Dir(executable), "lib", "ffmpeg"))
	}
	if cwd, err := os.Getwd(); err == nil {
		paths = append(paths, filepath.Join(cwd, "lib", "ffmpeg"), cwd)
	}
	paths = append(paths, "")
	seen := make(map[string]struct{}, len(paths))
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		path = filepath.Clean(path)
		if path == "." && runtime.GOOS != "windows" {
			path = ""
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	return out
}

func loadComponent(component string, paths []string) (sharedLibrary, error) {
	candidates := make([]string, 0, len(paths)*4)
	for _, dir := range paths {
		for _, name := range libraryNames(component) {
			candidate := name
			if dir != "" {
				candidate = filepath.Join(dir, name)
			}
			candidates = append(candidates, candidate)
			lib, err := openSharedLibrary(candidate)
			if err == nil {
				return lib, nil
			}
		}
	}
	return sharedLibrary{}, &LoadError{Library: component, Candidates: candidates, Err: ErrUnavailable}
}

func libraryNames(component string) []string {
	major := map[string]int{"avutil": avutilABIMajor, "avcodec": avcodecABIMajor, "avformat": avformatABIMajor, "swscale": swscaleABIMajor}[component]
	switch runtime.GOOS {
	case "darwin":
		return []string{fmt.Sprintf("lib%s.%d.dylib", component, major), "lib" + component + ".dylib"}
	case "windows":
		return []string{fmt.Sprintf("%s-%d.dll", component, major), component + ".dll"}
	default:
		return []string{fmt.Sprintf("lib%s.so.%d", component, major), "lib" + component + ".so"}
	}
}

type api struct {
	avutilVersion func() uint32
	avcodecVersion func() uint32
	avformatVersion func() uint32
	swscaleVersion func() uint32

	avformatAllocContext func() uintptr
	avformatOpenInput func(*uintptr, *byte, uintptr, *uintptr) int32
	avformatFindStreamInfo func(uintptr, uintptr) int32
	avFindBestStream func(uintptr, int32, int32, int32, *uintptr, int32) int32
	avReadFrame func(uintptr, uintptr) int32
	avSeekFrame func(uintptr, int32, int64, int32) int32
	avformatCloseInput func(*uintptr)

	avioAllocContext func(*byte, int32, int32, uintptr, uintptr, uintptr, uintptr) uintptr
	avioContextFree func(*uintptr)

	avcodecFindDecoder func(int32) uintptr
	avcodecAllocContext3 func(uintptr) uintptr
	avcodecParametersToContext func(uintptr, uintptr) int32
	avcodecOpen2 func(uintptr, uintptr, uintptr) int32
	avcodecFreeContext func(*uintptr)
	avcodecSendPacket func(uintptr, uintptr) int32
	avcodecReceiveFrame func(uintptr, uintptr) int32
	avcodecFlushBuffers func(uintptr)

	avPacketAlloc func() uintptr
	avPacketUnref func(uintptr)
	avPacketFree func(*uintptr)
	avFrameAlloc func() uintptr
	avFrameUnref func(uintptr)
	avFrameFree func(*uintptr)
	avMalloc func(uintptr) uintptr
	avFree func(uintptr)

	swsGetContext func(int32, int32, int32, int32, int32, int32, int32, uintptr, uintptr, uintptr) uintptr
	swsScale func(uintptr, uintptr, uintptr, int32, int32, uintptr, uintptr) int32
	swsFreeContext func(uintptr)
}

func (a *api) bind(libs map[string]sharedLibrary) error {
	bind := func(component, name string, dst any) error {
		address, err := libs[component].symbol(name)
		if err != nil {
			return fmt.Errorf("%s.%s: %w", component, name, err)
		}
		purego.RegisterFunc(dst, address)
		return nil
	}
	bindings := []struct{ component, name string; dst any }{
		{"avutil", "avutil_version", &a.avutilVersion}, {"avcodec", "avcodec_version", &a.avcodecVersion},
		{"avformat", "avformat_version", &a.avformatVersion}, {"swscale", "swscale_version", &a.swscaleVersion},
		{"avformat", "avformat_alloc_context", &a.avformatAllocContext}, {"avformat", "avformat_open_input", &a.avformatOpenInput},
		{"avformat", "avformat_find_stream_info", &a.avformatFindStreamInfo}, {"avformat", "av_find_best_stream", &a.avFindBestStream},
		{"avformat", "av_read_frame", &a.avReadFrame}, {"avformat", "av_seek_frame", &a.avSeekFrame}, {"avformat", "avformat_close_input", &a.avformatCloseInput},
		{"avformat", "avio_alloc_context", &a.avioAllocContext}, {"avformat", "avio_context_free", &a.avioContextFree},
		{"avcodec", "avcodec_find_decoder", &a.avcodecFindDecoder}, {"avcodec", "avcodec_alloc_context3", &a.avcodecAllocContext3},
		{"avcodec", "avcodec_parameters_to_context", &a.avcodecParametersToContext}, {"avcodec", "avcodec_open2", &a.avcodecOpen2},
		{"avcodec", "avcodec_free_context", &a.avcodecFreeContext}, {"avcodec", "avcodec_send_packet", &a.avcodecSendPacket},
		{"avcodec", "avcodec_receive_frame", &a.avcodecReceiveFrame}, {"avcodec", "avcodec_flush_buffers", &a.avcodecFlushBuffers},
		{"avcodec", "av_packet_alloc", &a.avPacketAlloc}, {"avcodec", "av_packet_unref", &a.avPacketUnref}, {"avcodec", "av_packet_free", &a.avPacketFree},
		{"avutil", "av_frame_alloc", &a.avFrameAlloc}, {"avutil", "av_frame_unref", &a.avFrameUnref}, {"avutil", "av_frame_free", &a.avFrameFree},
		{"avutil", "av_malloc", &a.avMalloc}, {"avutil", "av_free", &a.avFree}, {"swscale", "sws_getContext", &a.swsGetContext},
		{"swscale", "sws_scale", &a.swsScale}, {"swscale", "sws_freeContext", &a.swsFreeContext},
	}
	for _, item := range bindings {
		if err := bind(item.component, item.name, item.dst); err != nil {
			return fmt.Errorf("%w: %v", ErrUnavailable, err)
		}
	}
	return nil
}

func (a *api) checkABI() error {
	checks := []struct{ name string; got, want uint32 }{
		{"avutil", a.avutilVersion() >> 16, avutilABIMajor},
		{"avcodec", a.avcodecVersion() >> 16, avcodecABIMajor},
		{"avformat", a.avformatVersion() >> 16, avformatABIMajor},
		{"swscale", a.swscaleVersion() >> 16, swscaleABIMajor},
	}
	for _, check := range checks {
		if check.got != check.want {
			return fmt.Errorf("%w: %s major %d, want %d", ErrABIMismatch, check.name, check.got, check.want)
		}
	}
	return nil
}
