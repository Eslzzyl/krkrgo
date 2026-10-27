package host

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"log"
	"math"
	"os"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/exp/textinput"

	runtime "github.com/eslzzyl/krkrgo/internal/runtime"
)

const (
	DefaultWidth  = 1280
	DefaultHeight = 720
	// Interactive runtime time follows elapsed wall-clock time. Headless
	// replay supplies its own deterministic timestamps through Session.Tick.
	maxWallClockDelta = time.Second
)

type rendererMode uint8

const (
	rendererGPU rendererMode = iota
	rendererCPU
)

// Game is the Ebitengine presentation boundary for one runtime Session. It
// owns only presentation resources and input sampling; VM and window state stay
// in runtime.Session and runtime.Services.
type Game struct {
	session              *runtime.Session
	poller               InputPoller
	runtimeEvents        []runtime.InputEvent
	audioOutput          *AudioOutput
	clockAt              time.Time
	clockRemainder       time.Duration
	timeMS               uint64
	windowID             uint64
	resizeWindow         func(int, int)
	initialWindowSizeSet bool
	initialWindowWidth   int
	initialWindowHeight  int
	startupTiming        bool
	startupStarted       time.Time
	startupFirstUpdate   bool
	startupFirstDraw     bool

	image                   *ebiten.Image
	win32DialogImage        *ebiten.Image
	win32DialogRevision     uint64
	win32DialogWidth        int
	win32DialogHeight       int
	textComposer            textinput.Composer
	textIMEContext          runtime.Win32DialogTextInputContext
	textIMEContextValid     bool
	textIMECommits          []win32DialogIMECommit
	imageWidth              int
	imageHeight             int
	pixels                  []byte
	uploadPixels            []byte
	presentationOpaque      bool
	presentationChanges     int
	presentationStateHash   uint64
	presentationStateValid  bool
	presentationInitialized bool
	frameSerial             uint64
	renderStats             renderStats
	rendererMode            rendererMode
	gpu                     *GPUCompositor
	gpuImage                *ebiten.Image
	gpuInitAttempted        bool

	upscalerRequested     Upscaler
	upscalerInitAttempted bool
	postProcessor         PostProcessor
	postOutput            *ebiten.Image
	postStateHash         uint64
	postStateValid        bool
	outputScale           int
	windowScale           float64
	deviceScaleFactor     func() float64
}

type win32DialogIMECommit struct {
	context     runtime.Win32DialogTextInputContext
	text        string
	replacement *runtime.Win32DialogTextReplacement
}

// renderStats is opt-in to keep clock reads and logging out of normal runs.
type renderStats struct {
	enabled          bool
	stallTrace       bool
	started          time.Time
	lastUpdateStart  time.Time
	updates          int
	changedFrames    int
	draws            int
	newFrameDraws    int
	repeatedDraws    int
	serial           uint64
	update           time.Duration
	tick             time.Duration
	audio            time.Duration
	capture          time.Duration
	upload           time.Duration
	draw             time.Duration
	drawInterval     time.Duration
	drawIntervals    int
	minDrawInterval  time.Duration
	maxDrawInterval  time.Duration
	lastDrawAt       time.Time
	lastDrawSerial   uint64
	changedPixels    int64
	totalPixels      int64
	firstUpdateAt    time.Time
	firstDrawAt      time.Time
	firstSurface     time.Duration
	firstNewImage    time.Duration
	firstResize      time.Duration
	firstPixelsAlloc time.Duration
	firstUploadAlloc time.Duration
	firstSubImage    time.Duration
	firstWritePixels time.Duration
	writePixelsTotal time.Duration
	writePixelsCalls int
	subImageCalls    int
}

func (s *renderStats) recordFirstUpdate(at time.Time) {
	if s.firstUpdateAt.IsZero() {
		s.firstUpdateAt = at
	}
}

func (s *renderStats) recordDrawEntry(at time.Time) {
	if s.firstDrawAt.IsZero() {
		s.firstDrawAt = at
	}
}

func (s *renderStats) recordWritePixels(duration time.Duration) {
	if s.writePixelsCalls == 0 {
		s.firstWritePixels = duration
	}
	s.writePixelsCalls++
	s.writePixelsTotal += duration
}

func (s *renderStats) recordSubImage(duration time.Duration) {
	if s.subImageCalls == 0 {
		s.firstSubImage = duration
	}
	s.subImageCalls++
}

func (s *renderStats) recordUpdate(serial, logicalMS uint64, changedPixels, totalPixels int, updateStarted time.Time, update, tick, audio, capture, upload time.Duration) {
	if s.stallTrace {
		frameInterval := time.Duration(0)
		if !s.lastUpdateStart.IsZero() {
			frameInterval = updateStarted.Sub(s.lastUpdateStart)
		}
		runtime.TraceStallFrame(serial, logicalMS, frameInterval, update, tick, audio, upload)
		s.lastUpdateStart = updateStarted
	}
	s.updates++
	s.serial = serial
	if changedPixels > 0 {
		s.changedFrames++
	}
	s.changedPixels += int64(changedPixels)
	s.totalPixels += int64(totalPixels)
	s.update += update
	s.tick += tick
	s.audio += audio
	s.capture += capture
	s.upload += upload
}
func (s *renderStats) firstDrawAfterUpdate() time.Duration {
	if s.firstUpdateAt.IsZero() || s.firstDrawAt.IsZero() {
		return 0
	}
	return s.firstDrawAt.Sub(s.firstUpdateAt)
}

func (s *renderStats) writePixels(target *ebiten.Image, pixels []byte) {
	if !s.enabled {
		target.WritePixels(pixels)
		return
	}
	started := time.Now()
	target.WritePixels(pixels)
	s.recordWritePixels(time.Since(started))
}

func (s *renderStats) subImage(target *ebiten.Image, bounds image.Rectangle) *ebiten.Image {
	if !s.enabled {
		return target.SubImage(bounds).(*ebiten.Image)
	}
	started := time.Now()
	result := target.SubImage(bounds).(*ebiten.Image)
	s.recordSubImage(time.Since(started))
	return result
}

func (s *renderStats) recordDraw(serial uint64, draw time.Duration) {
	now := time.Now()
	var interval time.Duration
	if !s.lastDrawAt.IsZero() {
		interval = now.Sub(s.lastDrawAt)
	}
	if s.stallTrace {
		runtime.TraceStallDraw(serial, interval, draw)
	}
	s.lastDrawAt = now
	if !s.enabled {
		return
	}
	if s.started.IsZero() {
		s.started = now
	}
	if interval != 0 {
		s.drawInterval += interval
		s.drawIntervals++
		if s.minDrawInterval == 0 || interval < s.minDrawInterval {
			s.minDrawInterval = interval
		}
		if interval > s.maxDrawInterval {
			s.maxDrawInterval = interval
		}
	}
	s.draws++
	s.draw += draw
	if serial == 0 {
		s.repeatedDraws++
	} else if s.draws > 1 && serial == s.lastDrawSerial {
		s.repeatedDraws++
	} else {
		s.newFrameDraws++
	}
	s.lastDrawSerial = serial
	elapsed := now.Sub(s.started)
	if elapsed < time.Second {
		return
	}
	avg := func(total time.Duration, count int) time.Duration {
		if count == 0 {
			return 0
		}
		return total / time.Duration(count)
	}
	changePercent := 0.0
	if s.totalPixels != 0 {
		changePercent = 100 * float64(s.changedPixels) / float64(s.totalPixels)
	}
	log.Printf("window-render-stats interval=%s updates=%d update_fps=%.1f changed_frames=%d changed_frame_pct=%.1f content_changed_pixels_pct=%.2f frame_serial=%d draw_calls=%d draw_fps=%.1f draw_interval_avg=%s draw_interval_min=%s draw_interval_max=%s submitted_serial=%d new_frame_draws=%d repeated_frame_draws=%d update_avg=%s tick_avg=%s audio_avg=%s capture_avg=%s upload_avg=%s drawimage_avg=%s first_update_start=recorded first_draw_entry_after_update=%s first_surface_create=%s first_new_image=%s first_window_resize=%s first_pixel_buffer_alloc=%s first_upload_buffer_alloc=%s first_subimage_request=%s subimage_requests=%d first_writepixels=%s writepixels_requests=%d writepixels_avg=%s vsync_enabled=%t actual_fps=%.1f actual_tps=%.1f present_swap_serial=unavailable",
		elapsed.Round(time.Millisecond),
		s.updates, float64(s.updates)/elapsed.Seconds(),
		s.changedFrames, percentage(s.changedFrames, s.updates), changePercent, s.serial,
		s.draws, float64(s.draws)/elapsed.Seconds(),
		avg(s.drawInterval, s.drawIntervals), s.minDrawInterval, s.maxDrawInterval,
		s.lastDrawSerial, s.newFrameDraws, s.repeatedDraws,
		avg(s.update, s.updates),
		avg(s.tick, s.updates),
		avg(s.audio, s.updates),
		avg(s.capture, s.updates),
		avg(s.upload, s.updates),
		avg(s.draw, s.draws),
		s.firstDrawAfterUpdate(), s.firstSurface, s.firstNewImage, s.firstResize,
		s.firstPixelsAlloc, s.firstUploadAlloc, s.firstSubImage, s.subImageCalls,
		s.firstWritePixels, s.writePixelsCalls, avg(s.writePixelsTotal, s.writePixelsCalls),
		ebiten.IsVsyncEnabled(), ebiten.ActualFPS(), ebiten.ActualTPS())
	s.started = now
	s.updates, s.changedFrames, s.draws, s.newFrameDraws, s.repeatedDraws = 0, 0, 0, 0, 0
	s.update, s.tick, s.audio, s.capture, s.upload, s.draw = 0, 0, 0, 0, 0, 0
	s.changedPixels, s.totalPixels = 0, 0
	s.drawInterval, s.drawIntervals, s.minDrawInterval, s.maxDrawInterval = 0, 0, 0, 0
}

func percentage(part, total int) float64 {
	if total == 0 {
		return 0
	}
	return 100 * float64(part) / float64(total)
}

// NewGame creates an Ebitengine game around an already constructed runtime
// session. A nil session is retained so Update can return a useful error rather
// than panicking, which is friendlier to embedding applications.
func NewGame(session *runtime.Session) *Game {
	return newGameWithRenderer(session, rendererGPU)
}

// NewCPUGame creates an Ebitengine game that presents the existing CPU
// compositor path. It is intended for deterministic presentation and hosts
// that require the software renderer explicitly.
func NewCPUGame(session *runtime.Session) *Game {
	return newGameWithRenderer(session, rendererCPU)
}

// NewCPUGameWithInitialWindowSize creates a CPU-rendered Game with a
// preconfigured initial window size.
func NewCPUGameWithInitialWindowSize(session *runtime.Session, width, height int, windowScale float64) *Game {
	game := newGameWithRenderer(session, rendererCPU)
	game.initialWindowSizeSet = true
	game.initialWindowWidth = width
	game.initialWindowHeight = height
	game.SetWindowScale(windowScale)
	return game
}

// NewGameWithInitialWindowSize creates a Game when the caller has already
// configured the Ebitengine window to the given initial size.
func NewGameWithInitialWindowSize(session *runtime.Session, width, height int, windowScale float64) *Game {
	game := newGameWithRenderer(session, rendererGPU)
	game.initialWindowSizeSet = true
	game.initialWindowWidth = width
	game.initialWindowHeight = height
	game.SetWindowScale(windowScale)
	return game
}

func newGame(session *runtime.Session) *Game {
	return newGameWithRenderer(session, rendererGPU)
}

func newGameWithRenderer(session *runtime.Session, mode rendererMode) *Game {
	game := &Game{
		session:       session,
		runtimeEvents: make([]runtime.InputEvent, 0, len(trackedKeys)+8),
		resizeWindow:  ebiten.SetWindowSize,
		rendererMode:  mode,
		outputScale:   1,
		windowScale:   1,
		poller:        InputPoller{scale: 1},
		deviceScaleFactor: func() float64 {
			if monitor := ebiten.Monitor(); monitor != nil {
				return monitor.DeviceScaleFactor()
			}
			return 1
		},
	}
	game.renderStats.enabled = os.Getenv("KRKRGO_RENDER_STATS") == "1"
	game.renderStats.stallTrace = runtime.StallTraceEnabled()
	if session != nil && session.Services != nil {
		game.windowID = session.Services.MainWindow
	}
	game.initTextComposer()
	return game
}

func (g *Game) initTextComposer() {
	g.textComposer.OnNewSession = func() *textinput.SessionOptions {
		if g == nil || g.session == nil || g.session.Services == nil || len(g.textIMECommits) != 0 {
			return nil
		}
		width, height := g.Layout(0, 0)
		context, ok := g.session.Services.Win32DialogTextInputContext(width, height)
		if !ok {
			g.textIMEContextValid = false
			return nil
		}
		g.textIMEContext = context
		g.textIMEContextValid = true
		return &textinput.SessionOptions{
			CaretBounds:     context.CaretBounds,
			TextBeforeCaret: context.TextBeforeCaret,
			TextAfterCaret:  context.TextAfterCaret,
		}
	}
	g.textComposer.OnComposition = func(composition *textinput.Composition) {
		if g == nil || g.session == nil || g.session.Services == nil || composition == nil {
			return
		}
		selectionStart, _ := composition.SelectionRangeInBytes()
		g.session.Services.Win32DialogSetComposition(composition.Text(), selectionStart)
	}
	g.textComposer.OnCommit = func(commit *textinput.Commit) {
		if g == nil || commit == nil || !g.textIMEContextValid {
			return
		}
		pending := win32DialogIMECommit{context: g.textIMEContext, text: commit.Text()}
		if replaceBefore, replaceAfter := commit.IsSurroundingTextReplaced(); replaceBefore || replaceAfter {
			before, after := commit.SurroundingText()
			pending.replacement = &runtime.Win32DialogTextReplacement{Before: before, After: after}
		}
		g.textIMECommits = append(g.textIMECommits, pending)
	}
}

func (g *Game) updateWin32DialogTextInput(events []runtime.InputEvent) (active, handled bool, err error) {
	if g == nil || g.session == nil || g.session.Services == nil {
		return false, false, nil
	}
	width, height := g.Layout(0, 0)
	_, active = g.session.Services.Win32DialogTextInputContext(width, height)
	if !active {
		g.textComposer.Cancel()
		g.textIMEContextValid = false
	}
	handled, err = g.textComposer.Update()
	if err != nil {
		return active, handled, fmt.Errorf("update text input composer: %w", err)
	}
	confirm := false
	for _, event := range events {
		switch event.Kind {
		case runtime.InputPointerDown:
			confirm = true
		case runtime.InputFocus:
			if !event.Focused {
				confirm = true
			}
		case runtime.InputKeyDown:
			if !handled {
				confirm = true
			}
		}
	}
	if confirm {
		g.textComposer.Confirm()
	}
	if err := g.flushWin32DialogIMECommits(); err != nil {
		return active, handled, err
	}
	return active, handled, nil
}

func (g *Game) flushWin32DialogIMECommits() error {
	if g == nil || g.session == nil {
		return nil
	}
	pending := g.textIMECommits
	g.textIMECommits = nil
	for _, commit := range pending {
		if err := g.session.ApplyWin32DialogTextInputCommit(commit.context, commit.text, commit.replacement); err != nil {
			return fmt.Errorf("apply text input commit: %w", err)
		}
	}
	return nil
}

// Session returns the runtime owned by the game. The returned pointer must not
// be closed while the game is running.
func (g *Game) Session() *runtime.Session {
	if g == nil {
		return nil
	}
	return g.session
}

// SetWindowID selects the runtime window receiving sampled input. Embedders
// with multiple windows should call this before RunGame.
func (g *Game) SetWindowID(id uint64) {
	if g != nil {
		g.windowID = id
	}
}

// SetStartupTiming enables command-start-relative phase records for the first
// host callbacks. Ebitengine does not expose its native window visibility
// callback; first Update and first Draw are the available host milestones.
func (g *Game) SetStartupTiming(started time.Time) {
	if g == nil || started.IsZero() {
		return
	}
	g.startupTiming = os.Getenv("KRKRGO_STARTUP_TIMING") == "1"
	if g.startupTiming {
		g.startupStarted = started
	}
}

func (g *Game) logStartupPhase(phase string) {
	if g == nil || !g.startupTiming || g.startupStarted.IsZero() {
		return
	}
	log.Printf("krkrgo-startup phase=%s since-run-start=%s", phase, time.Since(g.startupStarted))
}

// Update polls device state, dispatches transitions to the runtime, and then
// advances the runtime by elapsed wall-clock time. GPU mode consumes a
// borrowed RenderPlan synchronously; CPU mode retains the RGBA presentation path.
func (g *Game) Update() error {
	if g == nil || g.session == nil {
		return fmt.Errorf("host game has no runtime session")
	}
	if g.session.Services == nil {
		return fmt.Errorf("runtime session has no services")
	}
	services := g.session.Services
	if services.ExitRequested() {
		return ebiten.Termination
	}

	statsEnabled := g.renderStats.enabled
	traceEnabled := g.renderStats.stallTrace
	timingEnabled := statsEnabled || traceEnabled
	var updateStarted time.Time
	if timingEnabled {
		updateStarted = time.Now()
		if statsEnabled {
			g.renderStats.recordFirstUpdate(updateStarted)
		}
	}
	if !g.startupFirstUpdate {
		g.startupFirstUpdate = true
		g.logStartupPhase("first-update")
	}
	g.runtimeEvents = g.poller.PollRuntime(g.runtimeEvents[:0])
	if g.windowID == 0 {
		return fmt.Errorf("runtime session has no input window")
	}
	textInputActive, composerHandled, inputErr := g.updateWin32DialogTextInput(g.runtimeEvents)
	if inputErr != nil {
		return inputErr
	}
	for _, event := range g.runtimeEvents {
		if textInputActive && event.Kind == runtime.InputText {
			continue
		}
		if composerHandled && event.Kind == runtime.InputKeyDown {
			continue
		}
		if err := g.session.Input(g.windowID, event); err != nil {
			return fmt.Errorf("dispatch input: %w", err)
		}
	}
	if services.ExitRequested() {
		return ebiten.Termination
	}
	var err error
	if g.rendererMode == rendererGPU && !g.gpuInitAttempted {
		g.gpuInitAttempted = true
		g.gpu, err = NewGPUCompositor()
		if err != nil {
			log.Printf("krkrgo: GPU renderer unavailable, falling back to CPU: %v", err)
			g.rendererMode = rendererCPU
			g.gpu = nil
			g.upscalerRequested = UpscalerOff
			g.setOutputScale(1)
		}
	}
	if g.rendererMode == rendererGPU && g.upscalerRequested != UpscalerOff && !g.upscalerInitAttempted {
		g.upscalerInitAttempted = true
		post, err := newPostProcessor(g.upscalerRequested)
		if err != nil {
			log.Printf("krkrgo: upscaler unavailable, using native scaling: %v", err)
			g.upscalerRequested = UpscalerOff
			g.setOutputScale(1)
		} else {
			g.postProcessor = post
		}
	}
	now := time.Now()
	logicalDeltaMS := g.wallClockDeltaMilliseconds(now)
	nextTimeMS := g.timeMS + logicalDeltaMS
	realtimeAudio := g.audioOutput != nil
	audioDeltaMS := logicalDeltaMS
	if realtimeAudio {
		audioDeltaMS = g.limitAudioDeltaMilliseconds(audioDeltaMS)
	}
	var tickTime time.Duration
	var captureTime time.Duration
	var frame runtime.FrameSnapshot
	var plan runtime.RenderPlan
	var totalPixels int
	if g.rendererMode == rendererGPU {
		if timingEnabled {
			stageStarted := time.Now()
			if realtimeAudio {
				plan, err = g.session.TickForGPUPresentationWithAudioDelta(nextTimeMS, audioDeltaMS)
			} else {
				plan, err = g.session.TickForGPUPresentation(nextTimeMS)
			}
			tickTime = time.Since(stageStarted)
		} else {
			if realtimeAudio {
				plan, err = g.session.TickForGPUPresentationWithAudioDelta(nextTimeMS, audioDeltaMS)
			} else {
				plan, err = g.session.TickForGPUPresentation(nextTimeMS)
			}
		}
		totalPixels = int(plan.Width) * int(plan.Height)
	} else {
		if timingEnabled {
			stageStarted := time.Now()
			if realtimeAudio {
				frame, err = g.session.TickForPresentationWithAudioDelta(nextTimeMS, audioDeltaMS)
			} else {
				frame, err = g.session.TickForPresentation(nextTimeMS)
			}
			tickTime = time.Since(stageStarted)
		} else {
			if realtimeAudio {
				frame, err = g.session.TickForPresentationWithAudioDelta(nextTimeMS, audioDeltaMS)
			} else {
				frame, err = g.session.TickForPresentation(nextTimeMS)
			}
		}
		totalPixels = int(frame.Width) * int(frame.Height)
	}
	if err != nil {
		if errors.Is(err, runtime.ErrSystemExit) {
			return ebiten.Termination
		}
		return fmt.Errorf("runtime tick and capture: %w", err)
	}
	if services.ExitRequested() {
		return ebiten.Termination
	}
	var audioTime time.Duration
	if timingEnabled {
		stageStarted := time.Now()
		err := g.deliverAudio()
		audioTime = time.Since(stageStarted)
		if err != nil {
			return fmt.Errorf("audio output: %w", err)
		}
	} else if err := g.deliverAudio(); err != nil {
		return fmt.Errorf("audio output: %w", err)
	}
	var presentationTime time.Duration
	if timingEnabled {
		stageStarted := time.Now()
		if g.rendererMode == rendererGPU {
			g.gpuImage, err = g.composeGPUPlan(plan)
			if err != nil {
				err = fmt.Errorf("GPU composition: %w", err)
			} else {
				err = g.updateGPUPresentation(plan)
			}
		} else {
			err = g.updatePresentation(frame)
		}
		presentationTime = time.Since(stageStarted)
	} else if g.rendererMode == rendererGPU {
		g.gpuImage, err = g.composeGPUPlan(plan)
		if err != nil {
			err = fmt.Errorf("GPU composition: %w", err)
		} else {
			err = g.updateGPUPresentation(plan)
		}
	} else {
		err = g.updatePresentation(frame)
	}
	if err != nil {
		return err
	}
	logicalMS := g.timeMS
	g.timeMS = nextTimeMS
	if timingEnabled {
		g.frameSerial++
		g.renderStats.recordUpdate(g.frameSerial, logicalMS, g.presentationChanges, totalPixels, updateStarted, time.Since(updateStarted), tickTime, audioTime, captureTime, presentationTime)
	}
	return nil
}

func (g *Game) wallClockDeltaMilliseconds(now time.Time) uint64 {
	if g == nil {
		return 0
	}
	if g.clockAt.IsZero() {
		g.clockAt = now
		return 0
	}
	elapsed := now.Sub(g.clockAt)
	g.clockAt = now
	if elapsed < 0 {
		elapsed = 0
	}
	if elapsed > maxWallClockDelta {
		elapsed = maxWallClockDelta
		g.clockRemainder = 0
	}
	total := elapsed + g.clockRemainder
	milliseconds := total / time.Millisecond
	g.clockRemainder = total % time.Millisecond
	return uint64(milliseconds)
}

func (g *Game) composeGPUPlan(plan runtime.RenderPlan) (*ebiten.Image, error) {
	var base *ebiten.Image
	var err error
	if plan.StateHashStable && plan.StateHashValid {
		base, err = g.gpu.composeStable(plan)
	} else {
		g.gpu.invalidateStateCache()
		base, err = g.gpu.Compose(plan)
	}
	if err != nil {
		return nil, err
	}
	if g.upscalerRequested == UpscalerOff || g.postProcessor == nil {
		return base, nil
	}
	baseWidth, baseHeight := base.Bounds().Dx(), base.Bounds().Dy()
	if plan.StateHashValid && g.postStateValid && g.postStateHash == plan.StateHash &&
		g.postOutput != nil && g.postOutput.Bounds().Dx() == baseWidth*2 && g.postOutput.Bounds().Dy() == baseHeight*2 {
		return g.postOutput, nil
	}
	out, err := g.postProcessor.Process(base)
	if err != nil {
		log.Printf("krkrgo: post-processing failed, using native scaling: %v", err)
		g.upscalerRequested = UpscalerOff
		g.postStateValid = false
		g.setOutputScale(1)
		return base, nil
	}
	g.postOutput = out
	g.postStateHash = plan.StateHash
	g.postStateValid = plan.StateHashValid
	return out, nil
}

// EnableUpscaler requests a frame post-processor. The GPU renderer falls back
// to native scaling if shader compilation fails.
func (g *Game) EnableUpscaler(mode Upscaler) {
	if g != nil {
		g.upscalerRequested = mode
	}
}

// SetOutputScale sets the presentation scale used when frame upscaling is active.
func (g *Game) SetOutputScale(scale int) {
	if g == nil || scale < 1 {
		return
	}
	g.setOutputScale(scale)
}

func (g *Game) setOutputScale(scale int) {
	if scale < 1 {
		scale = 1
	}
	g.outputScale = scale
	g.poller.scale = int32(scale)
}

// SetWindowScale sets the requested physical window multiplier.
func (g *Game) SetWindowScale(scale float64) {
	if g == nil || scale <= 0 {
		return
	}
	g.windowScale = scale
}

// Deallocate releases GPU presentation resources owned by the game.
func (g *Game) Deallocate() {
	if g == nil {
		return
	}
	if g.postProcessor != nil {
		g.postProcessor.Deallocate()
		g.postProcessor = nil
	}
	if g.gpu != nil {
		g.gpu.Deallocate()
		g.gpu = nil
	}
}

func (g *Game) updateGPUPresentation(plan runtime.RenderPlan) error {
	if plan.Width <= 0 || plan.Height <= 0 {
		return fmt.Errorf("runtime returned invalid render-plan dimensions")
	}
	if g.gpuImage == nil {
		return fmt.Errorf("GPU compositor returned no image")
	}
	width, height := g.gpuImage.Bounds().Dx(), g.gpuImage.Bounds().Dy()
	changed := g.imageWidth != width || g.imageHeight != height
	g.imageWidth, g.imageHeight = width, height
	g.presentationChanges = 0
	if changed {
		logicalWidth, logicalHeight := g.logicalWindowSize(width, height)
		resizeWindow := true
		if g.initialWindowSizeSet {
			resizeWindow = logicalWidth != g.initialWindowWidth || logicalHeight != g.initialWindowHeight
			g.initialWindowSizeSet = false
		}
		if resizeWindow && g.resizeWindow != nil {
			g.resizeWindow(logicalWidth, logicalHeight)
		}
	}
	g.presentationChanges = 1
	return nil
}

func (g *Game) rememberPresentationState(frame runtime.FrameSnapshot) {
	g.presentationStateHash = frame.RenderStateHash
	g.presentationStateValid = frame.RenderStateHashValid
}

func (g *Game) updateOpaquePresentation(frame runtime.FrameSnapshot, width, height int) (bool, error) {
	if !g.presentationOpaque {
		return false, nil
	}
	rowBytes := width * 4
	minX, minY, maxX, maxY := width, height, -1, -1
	for y := range height {
		rowStart := y * rowBytes
		current := g.pixels[rowStart : rowStart+rowBytes]
		next := frame.RGBA[rowStart : rowStart+rowBytes]
		if bytes.Equal(current, next) {
			continue
		}
		for x := range width {
			i := x * 4
			if !frame.Opaque && next[i+3] != 255 {
				return false, nil
			}
			if current[i] == next[i] && current[i+1] == next[i+1] &&
				current[i+2] == next[i+2] && current[i+3] == next[i+3] {
				continue
			}
			if g.renderStats.enabled {
				g.presentationChanges++
			}
			if x < minX {
				minX = x
			}
			if x > maxX {
				maxX = x
			}
			if y < minY {
				minY = y
			}
			if y > maxY {
				maxY = y
			}
		}
	}
	if maxX < 0 {
		g.presentationInitialized = true
		g.rememberPresentationState(frame)
		return true, nil
	}
	copy(g.pixels, frame.RGBA)
	g.presentationOpaque = true
	g.presentationInitialized = true
	dirtyWidth, dirtyHeight := maxX-minX+1, maxY-minY+1
	if dirtyWidth*dirtyHeight*2 < width*height {
		dirtySize := dirtyWidth * dirtyHeight * 4
		if cap(g.uploadPixels) < dirtySize {
			if g.renderStats.enabled && g.renderStats.firstUploadAlloc == 0 {
				stageStarted := time.Now()
				g.uploadPixels = make([]byte, dirtySize)
				g.renderStats.firstUploadAlloc = time.Since(stageStarted)
			} else {
				g.uploadPixels = make([]byte, dirtySize)
			}
		} else {
			g.uploadPixels = g.uploadPixels[:dirtySize]
		}
		for y := range dirtyHeight {
			src := ((minY+y)*width + minX) * 4
			dst := y * dirtyWidth * 4
			copy(g.uploadPixels[dst:dst+dirtyWidth*4], g.pixels[src:src+dirtyWidth*4])
		}
		g.renderStats.writePixels(g.renderStats.subImage(g.image, image.Rect(minX, minY, maxX+1, maxY+1)), g.uploadPixels)
	} else {
		g.renderStats.writePixels(g.image, g.pixels)
	}
	g.rememberPresentationState(frame)
	return true, nil
}

func (g *Game) updateTransparentPresentation(frame runtime.FrameSnapshot, width, height, n int) error {
	allOpaque := true
	minX, minY, maxX, maxY := width, height, -1, -1
	if g.renderStats.enabled {
		g.presentationChanges = 0
	}
	for y := 0; y < height; y++ {
		rowStart := y * width * 4
		for x := 0; x < width; x++ {
			i := rowStart + x*4
			alpha := frame.RGBA[i+3]
			pixelChanged := false
			if alpha == 255 {
				pixelChanged = g.pixels[i] != frame.RGBA[i] ||
					g.pixels[i+1] != frame.RGBA[i+1] ||
					g.pixels[i+2] != frame.RGBA[i+2] ||
					g.pixels[i+3] != alpha
				if pixelChanged {
					g.pixels[i] = frame.RGBA[i]
					g.pixels[i+1] = frame.RGBA[i+1]
					g.pixels[i+2] = frame.RGBA[i+2]
					g.pixels[i+3] = alpha
				}
			} else {
				allOpaque = false
				a := uint16(alpha)
				for channel := 0; channel < 3; channel++ {
					value := byte((uint16(frame.RGBA[i+channel])*a + 127) / 255)
					if g.pixels[i+channel] != value {
						pixelChanged = true
						g.pixels[i+channel] = value
					}
				}
				if g.pixels[i+3] != alpha {
					pixelChanged = true
					g.pixels[i+3] = alpha
				}
			}
			if pixelChanged {
				if g.renderStats.enabled {
					g.presentationChanges++
				}
				if x < minX {
					minX = x
				}
				if x > maxX {
					maxX = x
				}
				if y < minY {
					minY = y
				}
				if y > maxY {
					maxY = y
				}
			}
		}
	}
	g.presentationOpaque = allOpaque
	g.presentationInitialized = true
	if maxX < 0 {
		g.rememberPresentationState(frame)
		return nil
	}
	dirtyWidth, dirtyHeight := maxX-minX+1, maxY-minY+1
	if dirtyWidth*dirtyHeight*2 < width*height {
		dirtySize := dirtyWidth * dirtyHeight * 4
		if cap(g.uploadPixels) < dirtySize {
			if g.renderStats.enabled && g.renderStats.firstUploadAlloc == 0 {
				stageStarted := time.Now()
				g.uploadPixels = make([]byte, dirtySize)
				g.renderStats.firstUploadAlloc = time.Since(stageStarted)
			} else {
				g.uploadPixels = make([]byte, dirtySize)
			}
		} else {
			g.uploadPixels = g.uploadPixels[:dirtySize]
		}
		for y := range dirtyHeight {
			src := ((minY+y)*width + minX) * 4
			dst := y * dirtyWidth * 4
			copy(g.uploadPixels[dst:dst+dirtyWidth*4], g.pixels[src:src+dirtyWidth*4])
		}
		g.renderStats.writePixels(g.renderStats.subImage(g.image, image.Rect(minX, minY, maxX+1, maxY+1)), g.uploadPixels)
		g.rememberPresentationState(frame)
		return nil
	}
	g.rememberPresentationState(frame)
	return nil
}

func (g *Game) updatePresentation(frame runtime.FrameSnapshot) error {
	if frame.Empty() {
		return fmt.Errorf("runtime returned an empty frame")
	}
	if frame.Width <= 0 || frame.Height <= 0 {
		return fmt.Errorf("runtime returned invalid frame dimensions")
	}
	width, height := int(frame.Width), int(frame.Height)
	if width > int(^uint(0)>>1)/height || width*height > int(^uint(0)>>1)/4 {
		return fmt.Errorf("runtime frame dimensions overflow host limits")
	}
	n := width * height * 4
	if len(frame.RGBA) != n {
		return fmt.Errorf("runtime frame has %d RGBA bytes, want %d", len(frame.RGBA), n)
	}
	g.presentationChanges = 0
	if frame.RenderStateHashValid &&
		g.presentationStateValid &&
		g.image != nil &&
		g.imageWidth == width &&
		g.imageHeight == height &&
		g.presentationStateHash == frame.RenderStateHash {
		return nil
	}
	if g.image == nil || g.imageWidth != width || g.imageHeight != height {
		var surfaceStarted time.Time
		if g.renderStats.enabled && g.renderStats.firstSurface == 0 {
			surfaceStarted = time.Now()
			stageStarted := time.Now()
			g.image = ebiten.NewImage(width, height)
			g.renderStats.firstNewImage = time.Since(stageStarted)
		} else {
			g.image = ebiten.NewImage(width, height)
		}
		g.imageWidth, g.imageHeight = width, height
		if g.renderStats.enabled && g.renderStats.firstPixelsAlloc == 0 {
			stageStarted := time.Now()
			g.pixels = make([]byte, n)
			g.renderStats.firstPixelsAlloc = time.Since(stageStarted)
		} else {
			g.pixels = make([]byte, n)
		}
		g.presentationOpaque = false
		g.presentationInitialized = false
		// Keep the presentation surface at the runtime's native pixel size.
		// The OS window uses logical dimensions so Retina backing pixels map 1:1.
		logicalWidth, logicalHeight := g.logicalWindowSize(width, height)
		resizeWindow := true
		if g.initialWindowSizeSet {
			resizeWindow = logicalWidth != g.initialWindowWidth || logicalHeight != g.initialWindowHeight
			g.initialWindowSizeSet = false
		}
		if resizeWindow && g.resizeWindow != nil {
			if !surfaceStarted.IsZero() {
				stageStarted := time.Now()
				g.resizeWindow(logicalWidth, logicalHeight)
				g.renderStats.firstResize = time.Since(stageStarted)
			} else {
				g.resizeWindow(logicalWidth, logicalHeight)
			}
		}
		if !surfaceStarted.IsZero() {
			g.renderStats.firstSurface = g.renderStats.firstNewImage + g.renderStats.firstResize
		}
	}
	if frame.Opaque {
		g.presentationOpaque = true
	}
	// Opaque frames already use the same byte representation in the runtime
	// snapshot and Ebitengine image. Avoid the per-pixel conversion and dirty
	// bounds scan when a static scene is presented again.
	if g.presentationOpaque && bytes.Equal(g.pixels, frame.RGBA) {
		g.rememberPresentationState(frame)
		return nil
	}
	if g.presentationOpaque {
		if handled, err := g.updateOpaquePresentation(frame, width, height); handled || err != nil {
			return err
		}
	}
	if g.presentationInitialized && !g.presentationOpaque {
		return g.updateTransparentPresentation(frame, width, height, n)
	}
	allOpaque := true
	minX, minY, maxX, maxY := width, height, -1, -1
	for pixel := 0; pixel < width*height; pixel++ {
		i := pixel * 4
		if frame.RGBA[i+3] != 255 {
			allOpaque = false
			break
		}
		if g.pixels[i] == frame.RGBA[i] &&
			g.pixels[i+1] == frame.RGBA[i+1] &&
			g.pixels[i+2] == frame.RGBA[i+2] &&
			g.pixels[i+3] == 255 {
			continue
		}
		if g.renderStats.enabled {
			g.presentationChanges++
		}
		x, y := pixel%width, pixel/width
		if x < minX {
			minX = x
		}
		if x > maxX {
			maxX = x
		}
		if y < minY {
			minY = y
		}
		if y > maxY {
			maxY = y
		}
	}
	if allOpaque {
		// Opaque pixels have exactly the runtime snapshot's byte representation.
		// Once alpha is proven, copy in bulk instead of premultiplying and
		// updating each pixel's channels individually.
		if maxX < 0 {
			g.presentationOpaque = true
			g.presentationInitialized = true
			g.rememberPresentationState(frame)
			return nil
		}
		copy(g.pixels, frame.RGBA)
		g.presentationOpaque = true
		dirtyWidth, dirtyHeight := maxX-minX+1, maxY-minY+1
		if dirtyWidth*dirtyHeight*2 < width*height {
			dirtySize := dirtyWidth * dirtyHeight * 4
			if cap(g.uploadPixels) < dirtySize {
				if g.renderStats.enabled && g.renderStats.firstUploadAlloc == 0 {
					stageStarted := time.Now()
					g.uploadPixels = make([]byte, dirtySize)
					g.renderStats.firstUploadAlloc = time.Since(stageStarted)
				} else {
					g.uploadPixels = make([]byte, dirtySize)
				}
			} else {
				g.uploadPixels = g.uploadPixels[:dirtySize]
			}
			for y := range dirtyHeight {
				src := ((minY+y)*width + minX) * 4
				dst := y * dirtyWidth * 4
				copy(g.uploadPixels[dst:dst+dirtyWidth*4], g.pixels[src:src+dirtyWidth*4])
			}
			g.renderStats.writePixels(g.renderStats.subImage(g.image, image.Rect(minX, minY, maxX+1, maxY+1)), g.uploadPixels)
			g.presentationInitialized = true
			g.rememberPresentationState(frame)
			return nil
		}
		g.renderStats.writePixels(g.image, g.pixels)
		g.presentationInitialized = true
		g.rememberPresentationState(frame)
		return nil
	}
	return g.updateTransparentPresentation(frame, width, height, n)
}

func (g *Game) logicalWindowSize(width, height int) (int, int) {
	nominalW := width
	nominalH := height
	outputScale := 1.0
	if g != nil && g.outputScale > 1 {
		outputScale = float64(g.outputScale)
		nominalW = int(math.Round(float64(width) / outputScale))
		nominalH = int(math.Round(float64(height) / outputScale))
	}

	scale := 1.0
	if g != nil && g.windowScale > 0 {
		scale = g.windowScale
	}

	targetPhysW := float64(nominalW) * outputScale * scale
	targetPhysH := float64(nominalH) * outputScale * scale

	deviceScale := 1.0
	if g != nil && g.deviceScaleFactor != nil {
		if s := g.deviceScaleFactor(); s >= 1.0 {
			deviceScale = s
		}
	}

	winW := int(math.Round(targetPhysW / deviceScale))
	winH := int(math.Round(targetPhysH / deviceScale))
	return winW, winH
}

// Draw presents the latest runtime frame. Ebitengine applies Layout's logical
// canvas scaling with linear sampling to keep fractional window scales smooth.
func (g *Game) Draw(screen *ebiten.Image) {
	if g == nil {
		return
	}
	if !g.startupFirstDraw {
		g.startupFirstDraw = true
		g.logStartupPhase("first-draw")
	}
	if g.renderStats.enabled {
		g.renderStats.recordDrawEntry(time.Now())
	}
	surface := g.image
	if g.rendererMode == rendererGPU {
		surface = g.gpuImage
	}
	if screen == nil || surface == nil {
		return
	}
	// The logical canvas is commonly presented at a fractional window scale.
	// Linear sampling removes nearest-neighbor stair steps at that final scale.
	options := &ebiten.DrawImageOptions{Filter: ebiten.FilterLinear}
	if g.renderStats.enabled || g.renderStats.stallTrace {
		started := time.Now()
		screen.DrawImage(surface, options)
		g.renderStats.recordDraw(g.frameSerial, time.Since(started))
		g.drawWin32DialogOverlay(screen)
		return
	}
	screen.DrawImage(surface, options)
	g.drawWin32DialogOverlay(screen)
}

func (g *Game) drawWin32DialogOverlay(screen *ebiten.Image) {
	if g == nil || screen == nil || g.session == nil || g.session.Services == nil {
		return
	}
	width, height := g.Layout(0, 0)
	frame, revision := g.session.Services.Win32DialogFrame(width, height)
	if frame == nil {
		g.win32DialogImage = nil
		g.win32DialogRevision = revision
		g.win32DialogWidth, g.win32DialogHeight = 0, 0
		return
	}
	bounds := frame.Bounds()
	if g.win32DialogImage == nil || revision != g.win32DialogRevision ||
		bounds.Dx() != g.win32DialogWidth || bounds.Dy() != g.win32DialogHeight {
		g.win32DialogImage = ebiten.NewImage(bounds.Dx(), bounds.Dy())
		g.win32DialogImage.WritePixels(frame.Pix)
		g.win32DialogRevision = revision
		g.win32DialogWidth, g.win32DialogHeight = bounds.Dx(), bounds.Dy()
	}
	screen.DrawImage(g.win32DialogImage, nil)
}

// Layout supplies the stable logical canvas dimensions. Runtime frame sizes
// take precedence once a frame has been captured.
func (g *Game) Layout(_, _ int) (int, int) {
	if g != nil && g.imageWidth > 0 && g.imageHeight > 0 {
		return g.imageWidth, g.imageHeight
	}
	if g != nil && g.session != nil && g.session.Services != nil && g.session.Services.Compositor != nil {
		w, h := g.session.Services.Compositor.Width, g.session.Services.Compositor.Height
		if w > 0 && h > 0 {
			return int(w), int(h)
		}
	}
	return DefaultWidth, DefaultHeight
}
