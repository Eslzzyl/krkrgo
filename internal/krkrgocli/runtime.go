package krkrgocli

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"runtime/pprof"
	"strings"
	"sync"
	"time"

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
	Project  string
	SaveDir  string
	Budget   uint64
	Renderer string
}

const (
	rendererGPU = "gpu"
	rendererCPU = "cpu"
)

func ParseLaunchArgs(args []string) (LaunchOptions, error) {
	opts := LaunchOptions{Budget: unlimitedBudget, Renderer: rendererGPU}
	fs := flag.NewFlagSet("krkrgo run", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&opts.Project, "project", "", "project root or data directory")
	fs.StringVar(&opts.SaveDir, "save-dir", "", "save-data directory")
	fs.Uint64Var(&opts.Budget, "budget", unlimitedBudget, "diagnostic session instruction limit (default: unlimited)")
	fs.StringVar(&opts.Renderer, "renderer", rendererGPU, "renderer backend: gpu or cpu (default: gpu)")
	if err := fs.Parse(args); err != nil {
		return LaunchOptions{}, err
	}
	if opts.Renderer != rendererGPU && opts.Renderer != rendererCPU {
		return LaunchOptions{}, fmt.Errorf("unsupported renderer %q; want gpu or cpu", opts.Renderer)
	}
	if fs.NArg() != 0 {
		return LaunchOptions{}, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	return opts, nil
}

func NewSession(project, saveDir string, budget uint64) (*runtime.Session, error) {
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
		if _, err := session.Startup(); err != nil {
			_ = session.Close()
			return nil, fmt.Errorf("startup: %w", err)
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
	opts, err := ParseLaunchArgs(args)
	if err == flag.ErrHelp {
		return
	}
	if err != nil {
		log.Fatal(formatErrorChain(err))
	}
	log.Printf("krkrgo: opening project %q", opts.Project)
	session, err := NewSession(opts.Project, opts.SaveDir, opts.Budget)
	if err != nil {
		log.Fatal(formatErrorChain(err))
	}
	log.Printf("krkrgo: creating presentation window")
	defer func() { _ = session.Close() }()

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
	logAudioOutput := func() {
		stats := audioOutput.Diagnostics()
		log.Printf("audio output state: ready=%t pending_frames=%d accepted_blocks=%d accepted_frames=%d nonzero_samples=%d peak=%g queue_waits=%d queue_wait_ms=%g queue_max_wait_ms=%g",
			audioContext.IsReady(), audioOutput.PendingFrames(), stats.Blocks, stats.Frames, stats.NonZeroSamples, stats.Peak,
			stats.QueueWaits, float64(stats.QueueWaitNanos)/1e6, float64(stats.MaxQueueWaitNanos)/1e6)
	}
	if audioDiagnostics {
		log.Printf("audio output attached: sample_rate=%d ready=%t", audioContext.SampleRate(), audioContext.IsReady())
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
