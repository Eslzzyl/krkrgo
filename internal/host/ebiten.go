package host

import (
	"bytes"
	"fmt"
	"image"
	"log"
	"os"
	"time"

	"github.com/hajimehoshi/ebiten/v2"

	runtime "github.com/eslzzyl/krkrgo/internal/runtime"
)

const (
	DefaultWidth  = 1280
	DefaultHeight = 720
	// fixedTickMS is intentionally independent of wall-clock time. Ebiten may
	// call Update more or less often, but replaying updates uses this exact clock.
	fixedTickMS = uint64(16)
)

// Game is the Ebitengine presentation boundary for one runtime Session. It
// owns only presentation resources and input sampling; VM and window state stay
// in runtime.Session and runtime.Services.
type Game struct {
	session              *runtime.Session
	poller               InputPoller
	runtimeEvents        []runtime.InputEvent
	audioOutput          *AudioOutput
	timeMS               uint64
	windowID             uint64
	resizeWindow         func(int, int)
	initialWindowSizeSet bool
	initialWindowWidth   int
	initialWindowHeight  int

	image               *ebiten.Image
	imageWidth          int
	imageHeight         int
	pixels              []byte
	uploadPixels        []byte
	presentationOpaque  bool
	presentationChanges int
	frameSerial         uint64
	renderStats         renderStats
}

// renderStats is opt-in to keep clock reads and logging out of normal runs.
type renderStats struct {
	enabled          bool
	started          time.Time
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

func (s *renderStats) recordUpdate(serial uint64, changedPixels, totalPixels int, update, tick, audio, capture, upload time.Duration) {
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
	if s.started.IsZero() {
		s.started = now
	}
	if !s.lastDrawAt.IsZero() {
		interval := now.Sub(s.lastDrawAt)
		s.drawInterval += interval
		s.drawIntervals++
		if s.minDrawInterval == 0 || interval < s.minDrawInterval {
			s.minDrawInterval = interval
		}
		if interval > s.maxDrawInterval {
			s.maxDrawInterval = interval
		}
	}
	s.lastDrawAt = now
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
	return newGame(session)
}

// NewGameWithInitialWindowSize creates a Game when the caller has already
// configured the Ebitengine window to the given initial size.
func NewGameWithInitialWindowSize(session *runtime.Session, width, height int) *Game {
	game := newGame(session)
	game.initialWindowSizeSet = true
	game.initialWindowWidth = width
	game.initialWindowHeight = height
	return game
}

func newGame(session *runtime.Session) *Game {
	game := &Game{
		session:       session,
		runtimeEvents: make([]runtime.InputEvent, 0, len(trackedKeys)+8),
		resizeWindow:  ebiten.SetWindowSize,
	}
	game.renderStats.enabled = os.Getenv("KRKRGO_RENDER_STATS") == "1"
	if session != nil && session.Services != nil {
		game.windowID = session.Services.MainWindow
	}
	return game
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

// Update polls device state, dispatches transitions to the runtime, and then
// advances the runtime by one fixed deterministic tick. The latest complete
// RGBA snapshot is copied into a bounded presentation buffer.
func (g *Game) Update() error {
	if g == nil || g.session == nil {
		return fmt.Errorf("host game has no runtime session")
	}
	if g.session.Services == nil {
		return fmt.Errorf("runtime session has no services")
	}

	statsEnabled := g.renderStats.enabled
	var updateStarted time.Time
	if statsEnabled {
		updateStarted = time.Now()
		g.renderStats.recordFirstUpdate(updateStarted)
	}
	g.runtimeEvents = g.poller.PollRuntime(g.runtimeEvents[:0])
	if g.windowID == 0 {
		return fmt.Errorf("runtime session has no input window")
	}
	for _, event := range g.runtimeEvents {
		if err := g.session.Input(g.windowID, event); err != nil {
			return fmt.Errorf("dispatch input: %w", err)
		}
	}
	var tickTime time.Duration
	var captureTime time.Duration
	var frame runtime.FrameSnapshot
	var err error
	if statsEnabled {
		stageStarted := time.Now()
		frame, err = g.session.TickForPresentation(g.timeMS)
		tickTime = time.Since(stageStarted)
	} else {
		frame, err = g.session.TickForPresentation(g.timeMS)
	}
	if err != nil {
		return fmt.Errorf("runtime tick and capture: %w", err)
	}
	var audioTime time.Duration
	if statsEnabled {
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
	if statsEnabled {
		stageStarted := time.Now()
		err = g.updatePresentation(frame)
		presentationTime = time.Since(stageStarted)
	} else {
		err = g.updatePresentation(frame)
	}
	if err != nil {
		return err
	}
	g.timeMS += fixedTickMS
	if statsEnabled {
		g.frameSerial++
		g.renderStats.recordUpdate(g.frameSerial, g.presentationChanges, int(frame.Width)*int(frame.Height), time.Since(updateStarted), tickTime, audioTime, captureTime, presentationTime)
	}
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
		// Keep the presentation surface at the runtime's native pixel size.
		// Otherwise Layout resamples a differently-sized game canvas into the
		// launcher's fixed window, changing antialiased glyph coverage.
		resizeWindow := true
		if g.initialWindowSizeSet {
			resizeWindow = width != g.initialWindowWidth || height != g.initialWindowHeight
			g.initialWindowSizeSet = false
		}
		if resizeWindow && g.resizeWindow != nil {
			if !surfaceStarted.IsZero() {
				stageStarted := time.Now()
				g.resizeWindow(width, height)
				g.renderStats.firstResize = time.Since(stageStarted)
			} else {
				g.resizeWindow(width, height)
			}
		}
		if !surfaceStarted.IsZero() {
			g.renderStats.firstSurface = g.renderStats.firstNewImage + g.renderStats.firstResize
		}
	}
	// Opaque frames already use the same byte representation in the runtime
	// snapshot and Ebitengine image. Avoid the per-pixel conversion and dirty
	// bounds scan when a static scene is presented again.
	if g.presentationOpaque && bytes.Equal(g.pixels, frame.RGBA) {
		return nil
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
			return nil
		}
		g.renderStats.writePixels(g.image, g.pixels)
		return nil
	}
	allOpaque = false
	minX, minY, maxX, maxY = width, height, -1, -1
	if g.renderStats.enabled {
		g.presentationChanges = 0
	}
	for i := 0; i < n; i += 4 {
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
			x := (i / 4) % width
			y := (i / 4) / width
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
	g.presentationOpaque = allOpaque
	if maxX < 0 {
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
		return nil
	}
	g.renderStats.writePixels(g.image, g.pixels)
	return nil
}

// Draw presents the latest runtime frame. Ebitengine applies Layout's logical
// canvas scaling; no host-side stretching or second frame buffer is needed.
func (g *Game) Draw(screen *ebiten.Image) {
	if g == nil {
		return
	}
	if g.renderStats.enabled {
		g.renderStats.recordDrawEntry(time.Now())
	}
	if screen == nil || g.image == nil {
		return
	}
	if g.renderStats.enabled {
		started := time.Now()
		screen.DrawImage(g.image, nil)
		g.renderStats.recordDraw(g.frameSerial, time.Since(started))
		return
	}
	screen.DrawImage(g.image, nil)
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
