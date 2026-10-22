package krkrgocli

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"runtime/pprof"
	"strings"
	"sync"
	"time"

	"github.com/eslzzyl/krkrgo/internal/assets"
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/audio"

	"github.com/eslzzyl/krkrgo/internal/host"
	"github.com/eslzzyl/krkrgo/internal/runtime"
	"github.com/eslzzyl/krkrgo/internal/tjs"
)

const (
	defaultWidth    = host.DefaultWidth
	defaultHeight   = host.DefaultHeight
	unlimitedBudget = ^uint64(0)
)

type LaunchOptions struct {
	Project       string
	SaveDir       string
	Budget        uint64
	Renderer      string
	RenderScale   int
	Upscaler      string
	Anime4K       bool
	GameArguments map[string]string
}

const (
	rendererGPU = "gpu"
	rendererCPU = "cpu"
)

func ParseLaunchArgs(args []string) (LaunchOptions, error) {
	opts := LaunchOptions{Budget: unlimitedBudget, Renderer: rendererGPU, RenderScale: 1}
	var gameArgs []string
	fs := flag.NewFlagSet("krkrgo run", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&opts.Project, "project", "", "project directory or XP3 entry file")
	fs.StringVar(&opts.SaveDir, "save-dir", "", "save-data directory")
	fs.Uint64Var(&opts.Budget, "budget", unlimitedBudget, "diagnostic session instruction limit (default: unlimited)")
	fs.StringVar(&opts.Renderer, "renderer", rendererGPU, "renderer backend: gpu or cpu (default: gpu)")
	fs.IntVar(&opts.RenderScale, "render-scale", 1, "rendering pixel density: 1-4, or 0 for the monitor scale (default: 1)")
	fs.StringVar(&opts.Upscaler, "upscaler", "off", "frame upscaler: off, bilinear, bicubic, anime4k (requires -render-scale 2)")
	fs.BoolVar(&opts.Anime4K, "anime4k", false, "alias for -upscaler anime4k")
	fs.Func("game-arg", "pass a command-line option to the game's System.getArgument", func(value string) error {
		gameArgs = append(gameArgs, value)
		return nil
	})
	if err := fs.Parse(args); err != nil {
		return LaunchOptions{}, err
	}
	if opts.Renderer != rendererGPU && opts.Renderer != rendererCPU {
		return LaunchOptions{}, fmt.Errorf("unsupported renderer %q; want gpu or cpu", opts.Renderer)
	}
	if opts.RenderScale < 0 || opts.RenderScale > 4 {
		return LaunchOptions{}, fmt.Errorf("unsupported render scale %d; want 0 through 4", opts.RenderScale)
	}
	if opts.Upscaler == "off" {
		opts.Upscaler = ""
	}
	if opts.Anime4K {
		if opts.Upscaler != "" && opts.Upscaler != "anime4k" {
			return LaunchOptions{}, fmt.Errorf("-anime4k conflicts with -upscaler %s", opts.Upscaler)
		}
		opts.Upscaler = "anime4k"
	}
	switch opts.Upscaler {
	case "", "bilinear", "bicubic", "anime4k":
	default:
		return LaunchOptions{}, fmt.Errorf("unsupported upscaler %q; want off, bilinear, bicubic, or anime4k", opts.Upscaler)
	}
	if fs.NArg() != 0 {
		return LaunchOptions{}, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	var err error
	opts.GameArguments, err = parseGameArguments(gameArgs)
	if err != nil {
		return LaunchOptions{}, err
	}
	return opts, nil
}

func parseGameArguments(args []string) (map[string]string, error) {
	arguments := make(map[string]string, len(args))
	for _, arg := range args {
		if len(arg) < 2 || arg[0] != '-' {
			return nil, fmt.Errorf("game argument %q must start with '-'", arg)
		}
		name, value, hasValue := strings.Cut(arg, "=")
		if len(name) < 2 {
			return nil, fmt.Errorf("game argument %q has no option name", arg)
		}
		if !hasValue {
			value = "yes"
		}
		if _, exists := arguments[name]; !exists {
			arguments[name] = value
		}
	}
	return arguments, nil
}

func NewSession(project, saveDir string, budget uint64) (*runtime.Session, error) {
	return NewSessionWithScale(project, saveDir, budget, 1)
}

func NewSessionWithScale(project, saveDir string, budget uint64, scale int32) (*runtime.Session, error) {
	return NewSessionWithScaleAndArguments(project, saveDir, budget, scale, nil)
}

func NewSessionWithScaleAndArguments(project, saveDir string, budget uint64, scale int32, gameArguments map[string]string) (*runtime.Session, error) {
	var (
		session *runtime.Session
		err     error
	)
	if project == "" {
		session = runtime.NewSession(runtime.DefaultServices(), budget)
	} else {
		session, err = runtime.OpenSession(project, saveDir, budget)
		if err != nil {
			return nil, fmt.Errorf("open project: %w", err)
		}
	}
	return finishSession(session, project, scale, gameArguments)
}

// NewSessionWithStorageAndScaleAndArguments starts a session over a platform
// storage backend while keeping saves in the supplied writable directory.
func NewSessionWithStorageAndScaleAndArguments(storage *assets.Storage, saveDir string, budget uint64, scale int32, gameArguments map[string]string) (*runtime.Session, error) {
	session, err := runtime.OpenSessionWithStorage(storage, saveDir, budget)
	if err != nil {
		return nil, fmt.Errorf("open storage project: %w", err)
	}
	return finishSession(session, storage.Project, scale, gameArguments)
}

// NewSessionWithStorageAndSaveBackend starts a session with a platform-backed
// writable save tree, such as Android's Storage Access Framework.
func NewSessionWithStorageAndSaveBackend(storage *assets.Storage, saveDir string, saveBackend runtime.SaveStorageBackend, budget uint64, scale int32, gameArguments map[string]string) (*runtime.Session, error) {
	session, err := runtime.OpenSessionWithStorageAndSaveBackend(storage, saveDir, saveBackend, budget)
	if err != nil {
		return nil, fmt.Errorf("open storage project: %w", err)
	}
	return finishSession(session, storage.Project, scale, gameArguments)
}

func finishSession(session *runtime.Session, project string, scale int32, gameArguments map[string]string) (*runtime.Session, error) {
	if session == nil {
		return nil, fmt.Errorf("nil session")
	}
	session.Services.SetArguments(gameArguments)
	session.Services.SetRenderScale(scale)

	if project == "" {
		// Keep an empty host scene for non-project launches. Project startup
		// must let its first script-created primary Layer become the root.
		root := runtime.NewRootLayer(1, defaultWidth, defaultHeight)
		session.Services.SetRoot(root)
	}
	if err := session.Services.SetFrameSize(defaultWidth, defaultHeight); err != nil {
		_ = session.Close()
		return nil, err
	}

	if project == "" {
		// Without a project there is no script-created Window to host the
		// empty scene. Keep a minimal host Window for that mode; project
		// startup must let its first script-created Window become mainWindow.
		window := runtime.NewWindow()
		window.SetCaption("krkrgo")
		if err := window.SetInnerSize(defaultWidth, defaultHeight); err != nil {
			_ = session.Close()
			return nil, err
		}
		windowObject := session.VM.Objects.New(tjs.CategoryInstance)
		if err := session.Services.RegisterWindow(windowObject.ID, window); err != nil {
			_ = session.Close()
			return nil, err
		}
		session.Services.MainWindow = uint64(windowObject.ID)
	}

	if project != "" {
		log.Printf("krkrgo: starting project startup: %s", project)
		startupPhaseStarted := time.Now()
		if _, err := session.Startup(); err != nil {
			_ = session.Close()
			return nil, fmt.Errorf("startup: %w", err)
		}
		if os.Getenv("KRKRGO_STARTUP_TIMING") == "1" {
			log.Printf("krkrgo-startup phase=session-startup elapsed=%s", time.Since(startupPhaseStarted))
		}
		log.Printf("krkrgo: project startup completed")
	}
	return session, nil
}

func startCPUProfileFromEnv() func() {
	path := os.Getenv("KRKRGO_CPU_PROFILE")
	durationValue := os.Getenv("KRKRGO_CPU_PROFILE_DURATION")
	if path == "" && durationValue == "" {
		return nil
	}
	if path == "" || durationValue == "" {
		log.Printf("CPU profile requires both KRKRGO_CPU_PROFILE and KRKRGO_CPU_PROFILE_DURATION")
		return nil
	}
	duration, err := time.ParseDuration(durationValue)
	if err != nil || duration <= 0 {
		log.Printf("invalid KRKRGO_CPU_PROFILE_DURATION %q", durationValue)
		return nil
	}

	file, err := os.Create(path)
	if err != nil {
		log.Printf("create CPU profile %q: %v", path, err)
		return nil
	}
	if err := pprof.StartCPUProfile(file); err != nil {
		log.Printf("start CPU profile %q: %v", path, err)
		if closeErr := file.Close(); closeErr != nil {
			log.Printf("close CPU profile %q: %v", path, closeErr)
		}
		return nil
	}

	var stopOnce sync.Once
	stop := func() {
		stopOnce.Do(func() {
			pprof.StopCPUProfile()
			if err := file.Close(); err != nil {
				log.Printf("close CPU profile %q: %v", path, err)
			}
		})
	}
	timer := time.NewTimer(duration)
	done := make(chan struct{})
	go func() {
		select {
		case <-timer.C:
			stop()
		case <-done:
			timer.Stop()
		}
	}()
	var cleanupOnce sync.Once
	return func() {
		cleanupOnce.Do(func() {
			close(done)
			timer.Stop()
			stop()
		})
	}
}
func formatErrorChain(err error) string {
	if err == nil {
		return ""
	}
	lines := make([]string, 0, 4)
	for depth := 0; err != nil; depth++ {
		message := err.Error()
		next := errors.Unwrap(err)
		if next != nil {
			suffix := ": " + next.Error()
			message = strings.TrimSuffix(message, suffix)
		}
		if depth > 0 {
			message = "\t" + message
		}
		lines = append(lines, message)
		err = next
	}
	return strings.Join(lines, "\n")
}

func RunGame(args []string) {
	startupStarted := time.Now()
	startupTiming := os.Getenv("KRKRGO_STARTUP_TIMING") == "1"
	if startupTiming {
		log.Printf("krkrgo-startup phase=run-start")
	}
	opts, err := ParseLaunchArgs(args)
	if err == flag.ErrHelp {
		return
	}
	if err != nil {
		log.Fatal(formatErrorChain(err))
	}
	log.Printf("krkrgo: opening project %q", opts.Project)
	scale := int32(opts.RenderScale)
	if scale == 0 {
		scale = displayRenderScale()
	}
	if opts.Upscaler != "" {
		if opts.Renderer != rendererGPU {
			log.Fatalf("%s upscaling requires the gpu renderer", opts.Upscaler)
		}
		if scale != 2 {
			log.Fatalf("%s upscaling requires -render-scale 2; resolved scale is %d", opts.Upscaler, scale)
		}
		// The frame post-processor consumes a 1x composition and produces 2x.
		scale = 1
	}
	session, err := NewSessionWithScaleAndArguments(opts.Project, opts.SaveDir, opts.Budget, scale, opts.GameArguments)
	if err != nil {
		log.Fatal(formatErrorChain(err))
	}
	if startupTiming {
		log.Printf("krkrgo-startup phase=session-open elapsed=%s", time.Since(startupStarted))
	}
	log.Printf("krkrgo: creating presentation window")

	ebiten.SetWindowSize(defaultWidth, defaultHeight)
	ebiten.SetWindowTitle("krkrgo")
	stopCPUProfile := startCPUProfileFromEnv()
	if stopCPUProfile != nil {
		defer stopCPUProfile()
	}
	var game *host.Game
	if opts.Renderer == rendererCPU {
		game = host.NewCPUGameWithInitialWindowSize(session, defaultWidth, defaultHeight)
	} else {
		game = host.NewGameWithInitialWindowSize(session, defaultWidth, defaultHeight)
	}
	game.SetStartupTiming(startupStarted)
	defer game.Deallocate()
	if opts.Upscaler != "" {
		game.EnableUpscaler(host.Upscaler(opts.Upscaler))
		game.SetOutputScale(2)
	}
	audioDiagnostics := os.Getenv("KRKRGO_AUDIO_DIAGNOSTICS") == "1"
	audioContext := audio.NewContext(runtime.OutputRate)
	audioOutput, err := host.NewAudioOutput(audioContext, host.AudioOutputOptions{Diagnostics: audioDiagnostics})
	if err != nil {
		log.Fatal(err)
	}
	if err := game.AttachAudioOutput(audioOutput); err != nil {
		_ = audioOutput.Close()
		log.Fatal(err)
	}
	if startupTiming {
		log.Printf("krkrgo-startup phase=event-loop-ready elapsed=%s", time.Since(startupStarted))
	}
	logAudioOutput := func() {
		stats := audioOutput.Diagnostics()
		log.Printf("audio output state: ready=%t pending_frames=%d accepted_blocks=%d accepted_frames=%d nonzero_samples=%d peak=%g queue_waits=%d queue_wait_ms=%g queue_max_wait_ms=%g",
			audioContext.IsReady(), audioOutput.PendingFrames(), stats.Blocks, stats.Frames, stats.NonZeroSamples, stats.Peak,
			stats.QueueWaits, float64(stats.QueueWaitNanos)/1e6, float64(stats.MaxQueueWaitNanos)/1e6)
	}
	if audioDiagnostics {
		log.Printf("audio output attached: sample_rate=%d ready=%t", audioContext.SampleRate(), audioContext.IsReady())
	}
	if startupTiming {
		log.Printf("krkrgo-startup phase=event-loop-enter elapsed=%s", time.Since(startupStarted))
	}
	log.Printf("krkrgo: entering Ebitengine event loop")
	if err := ebiten.RunGame(game); err != nil {
		_ = audioOutput.Close()
		if stopCPUProfile != nil {
			stopCPUProfile()
		}
		if audioDiagnostics {
			logAudioOutput()
		}
		log.Fatal(err)
	}
	if err := audioOutput.Close(); err != nil {
		log.Printf("close audio output: %v", err)
	}
	if audioDiagnostics {
		logAudioOutput()
	}
}

// displayRenderScale rounds the monitor's device scale factor to an integer
// render density. Ebitengine may not have initialized a monitor before RunGame;
// use scale 1 until that monitor becomes available.
func displayRenderScale() int32 {
	return displayScaleForMonitor(ebiten.Monitor())
}

func displayScaleForMonitor(monitor *ebiten.MonitorType) int32 {
	if monitor == nil {
		return 1
	}
	return renderScaleFromDeviceScale(monitor.DeviceScaleFactor())
}

func renderScaleFromDeviceScale(scale float64) int32 {
	if scale < 1 {
		return 1
	}
	if scale > 4 {
		scale = 4
	}
	return int32(math.Round(scale))
}
