package runtime

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/maphash"
	"log"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/eslzzyl/krkrgo/internal/assets"
	"github.com/eslzzyl/krkrgo/internal/core"
	"github.com/eslzzyl/krkrgo/internal/tjs"
)

var (
	ErrSessionClosed     = errors.New("runtime session is closed")
	ErrBudgetExceeded    = errors.New("runtime execution budget exceeded")
	ErrUnsupportedDevice = errors.New("runtime device is unsupported")
	ErrUnsupportedNative = errors.New("runtime native operation is unsupported")
	ErrClockBackwards    = errors.New("session clock cannot move backwards")
)

// Clock supplies the deterministic millisecond clock used by a session.
type Clock interface{ NowMS() uint64 }
type ClockFunc func() uint64

func (f ClockFunc) NowMS() uint64 {
	if f == nil {
		return 0
	}
	return f()
}

type wallClock struct{ origin time.Time }

func (c wallClock) NowMS() uint64 {
	if c.origin.IsZero() {
		return 0
	}
	d := time.Since(c.origin)
	if d <= 0 {
		return 0
	}
	return uint64(d / time.Millisecond)
}
func WallClock() Clock { return wallClock{origin: time.Now()} }

// TraceEvent is emitted for every executed instruction when tracing is enabled.
type TraceEvent struct {
	TimeMS    uint64 `json:"time_ms"`
	Storage   string `json:"storage"`
	Line      uint64 `json:"line"`
	Column    uint64 `json:"column"`
	Operation string `json:"operation"`
}
type keyInputState struct {
	keys    map[uint32]struct{}
	pressed map[uint32]struct{}
}

// Services owns host state and implements the TJS Host boundary. It deliberately
// contains no presentation thread: native hosts can consume FrameSnapshot values.
type Services struct {
	Storage *assets.Storage
	SaveDir string
	Limits  core.Limits

	varStorageVM            *tjs.VM
	csvParserVM             *tjs.VM
	graphicCacheSystemLimit uint64
	Clock                   Clock
	TimeMS                  uint64
	clockPolls              uint64
	EpochMS                 int64
	TraceEvents             []TraceEvent
	Messages                []string
	MessageMap              map[string]string
	TraceEnabled            bool
	console                 tjs.Value
	Windows                 map[uint64]*WindowState
	WindowObjects           map[tjs.ObjectID]*WindowState
	MainWindow              uint64
	Display                 *DisplayState

	// ScriptTextEncoding is the visible Scripts.textEncoding value. Scripts
	// storage operations use the native text-stream decoder until the script
	// explicitly selects an encoding.
	ScriptTextEncoding         string
	scriptTextEncodingExplicit bool

	// System state mirrors the mutable C++ System properties. The callback
	// slice is kept as a GC root through GCRoots while it is registered.
	SystemTitle              string
	systemTitleInitialized   bool
	systemExecutableName     string
	systemExecutableResolved bool

	EventDisabled            bool
	ExitOnWindowClose        bool
	DrawThreadNum            int64
	ExitOnNoWindowStartup    bool
	Terminated               bool
	TerminationCode          int64
	ContinuousHandlers       []tjs.Value
	Root                     *Layer
	Compositor               *Compositor
	validatedTreeRoot        *Layer
	validatedTreeRevision    uint64
	validatedTreeError       error
	gpuCapture               bool
	Scheduler                *Scheduler
	Events                   *EventQueue
	Timers                   *TimerRegistry
	TimerActions             map[tjs.ObjectID]timerAction
	Rects                    map[tjs.ObjectID]*scriptRect
	Dates                    map[tjs.ObjectID]*tjs.Date
	AsyncTriggers            map[tjs.ObjectID]*asyncTriggerState
	AsyncTriggerActions      map[tjs.ObjectID]timerAction
	Arguments                map[string]string
	Plugins                  []string
	menuData                 menuModel
	sampleDefaultCounts      int
	sampleDefaultAheads      int
	samplePluginVMs          map[*tjs.VM]struct{}
	KAGMacroObjects          map[tjs.ObjectID]tjs.Value
	Natives                  *NativeRegistry
	KAGParsers               map[tjs.ObjectID]*KAGParser
	CSVParsers               map[tjs.ObjectID]*csvParserState
	Videos                   map[tjs.ObjectID]*VideoOverlay
	PhaseVocoders            map[tjs.ObjectID]*phaseVocoderConfig
	Sounds                   map[tjs.ObjectID]*WaveSoundBuffer
	SoundEvents              map[tjs.ObjectID][]SoundEvent
	AudioMixer               *AudioMixer
	Dialogs                  *DialogService
	Layers                   map[tjs.ObjectID]*Layer
	pendingPaintIDs          []tjs.ObjectID
	pendingPaintScratch      []tjs.ObjectID
	transitionIDsScratch     []tjs.ObjectID
	pendingTransitionScratch []tjs.ObjectID
	MotionResources          map[tjs.ObjectID]*motionResource
	MotionPlayers            map[tjs.ObjectID]*motionPlayerState
	MotionAdaptors           map[tjs.ObjectID]tjs.ObjectID
	Bitmaps                  map[tjs.ObjectID]*Bitmap
	bitmapLoadGeneration     map[tjs.ObjectID]uint64
	bitmapLoadResults        map[tjs.ObjectID]bitmapAsyncResult
	Fonts                    map[tjs.ObjectID]*FontState
	TextRenderers            map[tjs.ObjectID]*textRenderState
	LayerFonts               map[tjs.ObjectID]tjs.ObjectID
	LayerDefaultFonts        map[tjs.ObjectID]tjs.ObjectID
	LayerChildren            map[tjs.ObjectID]tjs.Value
	LayerCursors             map[tjs.ObjectID]tjs.Value
	FontBook                 *FontBook
	FontSystem               *FontSystem
	Pads                     map[tjs.ObjectID]*padState
	Roots                    []tjs.Value
	scriptsInitialized       bool
	clipboardBackend         clipboardBackend
	preprocessor             *tjs.Preprocessor
	sessionPreprocessors     map[*tjs.VM]*tjs.Preprocessor
	// appLocks stays scoped to this Services instance, not package-global state.
	appLocks        map[string]struct{}
	inputStates     map[uint64]*keyInputState
	mu              sync.RWMutex
	initMu          sync.Mutex
	kagHookMu       sync.Mutex
	kagHookNatives  map[*tjs.VM]tjs.Value
	pendingKAGHooks map[*tjs.VM][]kagHookRegistration
	childrenSyncMu  sync.Mutex
	audioMixerOwner *AudioMixer
	frameCacheSeed  maphash.Seed
	frameCacheHash  uint64
	frameCache      FrameSnapshot
	frameCacheValid bool
	dynamicRGBA     []byte
	dynamicProvince []byte
	renderDiag      frameCaptureDiagnostics
}

type frameCaptureDiagnostics struct {
	started                          time.Time
	captures                         uint64
	composes                         uint64
	cacheHits                        uint64
	cacheMisses                      uint64
	cacheInvalidations               uint64
	renderChanges                    uint64
	dynamicFrames                    uint64
	transitions                      uint64
	videos                           uint64
	contentChanges                   uint64
	presentationTransitionCandidates uint64
	presentationTransitionHits       uint64
	presentationTransitionFailure    string
	presentationTransitionFailures   uint64
	transitionAlphaMode              string
	transitionAlphaFrames            uint64
	validateTime                     time.Duration
	transitionWalkTime               time.Duration
	composeTime                      time.Duration
	cacheHashTime                    time.Duration
	cacheLookupTime                  time.Duration
	frameCopyTime                    time.Duration
	recordTime                       time.Duration
	lastContent                      uint64
	contentValid                     bool
	contentSeed                      maphash.Seed
	contentHashEnabled               bool
	contentConfigLoaded              bool
	lastRenderState                  uint64
	renderStateValid                 bool
}

type frameCaptureTimings struct {
	validate       time.Duration
	transitionWalk time.Duration
	compose        time.Duration
	cacheHash      time.Duration
	cacheLookup    time.Duration
}

func (s *Services) recordFrameCapture(frame FrameSnapshot, composed, cacheHit, invalidated bool, renderState uint64, renderStateValid bool, transitions, videos int, timings frameCaptureTimings) {
	if os.Getenv("KRKRGO_RENDER_STATS") == "" {
		return
	}
	d := &s.renderDiag
	now := time.Now()
	if d.started.IsZero() {
		d.started = now
	}
	if !d.contentConfigLoaded {
		d.contentHashEnabled = os.Getenv("KRKRGO_RENDER_CONTENT_HASH") != ""
		if d.contentHashEnabled {
			d.contentSeed = maphash.MakeSeed()
		}
		d.contentConfigLoaded = true
	}
	d.captures++
	if composed {
		d.composes++
	}
	if cacheHit {
		d.cacheHits++
	} else {
		d.cacheMisses++
	}
	if invalidated {
		d.cacheInvalidations++
	}
	if renderStateValid {
		if d.renderStateValid && d.lastRenderState != renderState {
			d.renderChanges++
		}
		d.lastRenderState, d.renderStateValid = renderState, true
	} else {
		d.renderStateValid = false
	}
	if transitions != 0 || videos != 0 {
		d.dynamicFrames++
	}
	d.transitions += uint64(transitions)
	d.videos += uint64(videos)
	d.validateTime += timings.validate
	d.transitionWalkTime += timings.transitionWalk
	d.composeTime += timings.compose
	d.cacheHashTime += timings.cacheHash
	d.cacheLookupTime += timings.cacheLookup
	if d.contentHashEnabled {
		hash := maphash.Bytes(d.contentSeed, frame.RGBA)
		if d.contentValid && hash != d.lastContent {
			d.contentChanges++
		}
		d.lastContent, d.contentValid = hash, true
	}
	if elapsed := now.Sub(d.started); elapsed >= time.Second {
		contentHashStatus := "disabled"
		contentChanges := "disabled"
		if d.contentHashEnabled {
			contentHashStatus = "enabled"
			contentChanges = fmt.Sprintf("%d", d.contentChanges)
		}
		log.Printf("krkrgo runtime frame stats: captures=%d compose=%d cache_hit=%d cache_miss=%d cache_invalidations=%d render_state_changes=%d dynamic=%d transitions=%d videos=%d presentation_transition_candidates=%d presentation_transition_hits=%d presentation_transition_failure=%s:%d transition_alpha_mode=%s:%d validate_avg=%s transition_walk_avg=%s compose_avg=%s cache_hash_avg=%s cache_lookup_avg=%s frame_copy_avg=%s record_avg=%s content_hash=%s content_changes=%s elapsed=%s",
			d.captures, d.composes, d.cacheHits, d.cacheMisses, d.cacheInvalidations, d.renderChanges, d.dynamicFrames, d.transitions, d.videos,
			d.presentationTransitionCandidates, d.presentationTransitionHits, d.presentationTransitionFailure, d.presentationTransitionFailures,
			d.transitionAlphaMode, d.transitionAlphaFrames,
			avgFrameCaptureTime(d.validateTime, d.captures), avgFrameCaptureTime(d.transitionWalkTime, d.captures), avgFrameCaptureTime(d.composeTime, d.captures),
			avgFrameCaptureTime(d.cacheHashTime, d.captures), avgFrameCaptureTime(d.cacheLookupTime, d.captures),
			avgFrameCaptureTime(d.frameCopyTime, d.captures), avgFrameCaptureTime(d.recordTime, d.captures),
			contentHashStatus, contentChanges, elapsed.Round(time.Millisecond))
		*d = frameCaptureDiagnostics{
			started: now, contentSeed: d.contentSeed, contentHashEnabled: d.contentHashEnabled,
			contentConfigLoaded: d.contentConfigLoaded, lastContent: d.lastContent, contentValid: d.contentValid,
			lastRenderState: d.lastRenderState, renderStateValid: d.renderStateValid,
		}
	}
}

func avgFrameCaptureTime(total time.Duration, count uint64) time.Duration {
	if count == 0 {
		return 0
	}
	return total / time.Duration(count)
}

func newRuntimePreprocessor() *tjs.Preprocessor {
	preprocessor := tjs.NewPreprocessor()
	preprocessor.Define("kirikiriz", 1)
	return preprocessor
}

func (s *Services) preprocessorFor(vm *tjs.VM) *tjs.Preprocessor {
	s.ensureInit()
	s.mu.RLock()
	defer s.mu.RUnlock()
	if preprocessor := s.sessionPreprocessors[vm]; preprocessor != nil {
		return preprocessor
	}
	return s.preprocessor
}

func (s *Services) bindSessionPreprocessor(vm *tjs.VM, preprocessor *tjs.Preprocessor) {
	s.ensureInit()
	s.mu.Lock()
	s.sessionPreprocessors[vm] = preprocessor
	s.mu.Unlock()
}

func (s *Services) unbindSessionPreprocessor(vm *tjs.VM, preprocessor *tjs.Preprocessor) {
	s.mu.Lock()
	if s.sessionPreprocessors[vm] == preprocessor {
		delete(s.sessionPreprocessors, vm)
	}
	s.mu.Unlock()
}

func NewServices(storage *assets.Storage, saveDir string, limits core.Limits) *Services {
	if limits == (core.Limits{}) {
		limits = core.DefaultLimits()
	}
	events := NewEventQueue()
	book := NewFontBook()
	book.DiscoverSystemFonts()
	return &Services{
		Storage:                 storage,
		SaveDir:                 saveDir,
		Limits:                  limits,
		FontBook:                book,
		FontSystem:              NewFontSystem(book),
		graphicCacheSystemLimit: limits.CacheBytes,
		clipboardBackend:        systemClipboardBackend{},
		Events:                  events,
		ScriptTextEncoding:      "UTF-8",
		scriptsInitialized:      true,
		sampleDefaultCounts:     100,
		Dialogs:                 NewDialogService(nil),
		preprocessor:            newRuntimePreprocessor(),
		sessionPreprocessors:    make(map[*tjs.VM]*tjs.Preprocessor),
		appLocks:                make(map[string]struct{}),
		Dates:                   make(map[tjs.ObjectID]*tjs.Date),
		LayerCursors:            make(map[tjs.ObjectID]tjs.Value),
		Arguments:               make(map[string]string), MessageMap: make(map[string]string), Natives: NewNativeRegistry(), KAGParsers: make(map[tjs.ObjectID]*KAGParser), CSVParsers: make(map[tjs.ObjectID]*csvParserState), Videos: make(map[tjs.ObjectID]*VideoOverlay), Sounds: make(map[tjs.ObjectID]*WaveSoundBuffer), SoundEvents: make(map[tjs.ObjectID][]SoundEvent), Layers: make(map[tjs.ObjectID]*Layer), MotionResources: make(map[tjs.ObjectID]*motionResource), MotionPlayers: make(map[tjs.ObjectID]*motionPlayerState), MotionAdaptors: make(map[tjs.ObjectID]tjs.ObjectID), Bitmaps: make(map[tjs.ObjectID]*Bitmap), Fonts: make(map[tjs.ObjectID]*FontState), TextRenderers: make(map[tjs.ObjectID]*textRenderState), LayerFonts: make(map[tjs.ObjectID]tjs.ObjectID), LayerDefaultFonts: make(map[tjs.ObjectID]tjs.ObjectID), LayerChildren: make(map[tjs.ObjectID]tjs.Value),
	}
}
func DefaultServices() *Services { return NewServices(nil, "", core.DefaultLimits()) }
func (s *Services) ensureInit() {
	if s == nil {
		return
	}
	s.initMu.Lock()
	defer s.initMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.systemTitleInitialized {
		s.SystemTitle = systemVersionString
		s.systemTitleInitialized = true
	}
	if !s.systemExecutableResolved {
		if s.Storage != nil {
			if executable, ok, err := core.ProjectExecutable(s.Storage.Project, nil); err == nil && ok {
				s.systemExecutableName = executable
			}
		}
		s.systemExecutableResolved = true
	}
	if s.preprocessor == nil {
		s.preprocessor = newRuntimePreprocessor()
	}
	if s.sessionPreprocessors == nil {
		s.sessionPreprocessors = make(map[*tjs.VM]*tjs.Preprocessor)
	}
	if !s.scriptsInitialized {
		if s.ScriptTextEncoding == "" {
			s.ScriptTextEncoding = "UTF-8"
		}
		s.scriptsInitialized = true
	}
	if s.Clock == nil {
		s.Clock = WallClock()
	}
	if s.Windows == nil {
		s.Windows = make(map[uint64]*WindowState)
	}
	if s.WindowObjects == nil {
		s.WindowObjects = make(map[tjs.ObjectID]*WindowState)
	}
	if s.clipboardBackend == nil {
		s.clipboardBackend = systemClipboardBackend{}
	}
	if s.Display == nil {
		s.Display = NewDisplay()
	}
	if s.Scheduler == nil {
		s.Scheduler = &Scheduler{}
	}
	if s.Events == nil {
		s.Events = NewEventQueue()
	}
	if s.Timers == nil {
		s.Timers = NewTimerRegistry(s.Events)
	} else if s.Timers.EventQueue() != s.Events {
		s.Timers = NewTimerRegistry(s.Events)
	}
	if s.TimerActions == nil {
		s.TimerActions = make(map[tjs.ObjectID]timerAction)
	}
	if s.Rects == nil {
		s.Rects = make(map[tjs.ObjectID]*scriptRect)
	}
	if s.Dates == nil {
		s.Dates = make(map[tjs.ObjectID]*tjs.Date)
	}
	if s.AsyncTriggers == nil {
		s.AsyncTriggers = make(map[tjs.ObjectID]*asyncTriggerState)
	}
	if s.AsyncTriggerActions == nil {
		s.AsyncTriggerActions = make(map[tjs.ObjectID]timerAction)
	}
	if s.KAGMacroObjects == nil {
		s.KAGMacroObjects = make(map[tjs.ObjectID]tjs.Value)
	}
	if s.Arguments == nil {
		s.Arguments = make(map[string]string)
	}
	if s.MessageMap == nil {
		s.MessageMap = make(map[string]string)
	}
	if s.appLocks == nil {
		s.appLocks = make(map[string]struct{})
	}
	if s.inputStates == nil {
		s.inputStates = make(map[uint64]*keyInputState)
	}
	if s.Natives == nil {
		s.Natives = NewNativeRegistry()
	}
	if s.KAGParsers == nil {
		s.KAGParsers = make(map[tjs.ObjectID]*KAGParser)
	}
	if s.Videos == nil {
		s.Videos = make(map[tjs.ObjectID]*VideoOverlay)
	}
	if s.PhaseVocoders == nil {
		s.PhaseVocoders = make(map[tjs.ObjectID]*phaseVocoderConfig)
	}
	if s.Sounds == nil {
		s.Sounds = make(map[tjs.ObjectID]*WaveSoundBuffer)
	}
	if s.SoundEvents == nil {
		s.SoundEvents = make(map[tjs.ObjectID][]SoundEvent)
	}
	if s.Layers == nil {
		s.Layers = make(map[tjs.ObjectID]*Layer)
	}
	if s.Bitmaps == nil {
		s.Bitmaps = make(map[tjs.ObjectID]*Bitmap)
	}
	if s.bitmapLoadGeneration == nil {
		s.bitmapLoadGeneration = make(map[tjs.ObjectID]uint64)
	}
	if s.bitmapLoadResults == nil {
		s.bitmapLoadResults = make(map[tjs.ObjectID]bitmapAsyncResult)
	}
	if s.Fonts == nil {
		s.Fonts = make(map[tjs.ObjectID]*FontState)
	}
	if s.TextRenderers == nil {
		s.TextRenderers = make(map[tjs.ObjectID]*textRenderState)
	}
	if s.LayerFonts == nil {
		s.LayerFonts = make(map[tjs.ObjectID]tjs.ObjectID)
	}
	if s.LayerDefaultFonts == nil {
		s.LayerDefaultFonts = make(map[tjs.ObjectID]tjs.ObjectID)
	}
	if s.LayerChildren == nil {
		s.LayerChildren = make(map[tjs.ObjectID]tjs.Value)
	}
	if s.LayerCursors == nil {
		s.LayerCursors = make(map[tjs.ObjectID]tjs.Value)
	}
	if s.FontBook == nil {
		s.FontBook = NewFontBook()
	}
	if s.FontSystem == nil {
		s.FontSystem = NewFontSystem(s.FontBook)
	} else if s.FontSystem.Book == nil {
		s.FontSystem.Book = s.FontBook
	}
	if strings.TrimSpace(s.FontSystem.DefaultFont) == "" {
		s.FontSystem.DefaultFont = defaultFontFaceName
	}
	if s.AudioMixer == nil {
		s.AudioMixer = NewAudioMixer()
	}
	if s.audioMixerOwner != s.AudioMixer {
		for _, sound := range s.Sounds {
			s.AudioMixer.Add(sound)
		}
		for _, video := range s.Videos {
			s.AudioMixer.AddVideo(video)
		}
		s.audioMixerOwner = s.AudioMixer
	}
}
func (s *Services) SetClock(clock Clock) {
	s.ensureInit()
	if clock != nil {
		s.Clock = clock
	}
}
func (s *Services) SetStorage(storage *assets.Storage) {
	if s == nil {
		return
	}
	s.ensureInit()
	s.mu.Lock()
	s.Storage = storage
	s.systemExecutableName = ""
	s.systemExecutableResolved = false
	s.mu.Unlock()
}
func (s *Services) SetSaveDir(path string) { s.SaveDir = path }
func (s *Services) SetScreenSize(width, height uint32) error {
	s.ensureInit()
	return s.Display.SetScreenSize(width, height)
}
func (s *Services) SetDisplays(displays []Monitor) error {
	s.ensureInit()
	return s.Display.SetDisplays(displays)
}
func (s *Services) Schedule(delayMS uint64, name string) (uint64, error) {
	s.ensureInit()
	return s.Scheduler.Schedule(delayMS, name)
}
func (s *Services) CancelScheduled(id uint64) {
	if s.Scheduler != nil {
		s.Scheduler.Cancel(id)
	}
}
func (s *Services) PostEvent(target uint64, kind EventKind, priority Priority) error {
	s.ensureInit()
	return s.Events.Post(target, kind, priority)
}

func (s *Services) updateInputState(window uint64, event InputEvent) {
	if s == nil {
		return
	}
	var key uint32
	var down bool
	switch event.Kind {
	case InputKeyDown:
		key, down = uint32(event.Key), true
	case InputKeyUp:
		key = uint32(event.Key)
	case InputPointerDown, InputPointerUp:
		switch event.Button {
		case 0:
			key = 1
		case 1:
			key = 2
		case 2:
			key = 4
		}
		down = event.Kind == InputPointerDown
	case InputFocus:
		if event.Focused {
			return
		}
		s.mu.Lock()
		if state := s.inputStates[window]; state != nil {
			clear(state.keys)
		}
		s.mu.Unlock()
		return
	default:
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Windows[window] == nil {
		return
	}
	state := s.inputStates[window]
	if down {
		if state == nil {
			state = &keyInputState{}
			s.inputStates[window] = state
		}
		if state.keys == nil {
			state.keys = make(map[uint32]struct{})
		}
		if state.pressed == nil {
			state.pressed = make(map[uint32]struct{})
		}
		state.keys[key] = struct{}{}
		state.pressed[key] = struct{}{}
	} else if state != nil {
		delete(state.keys, key)
	}
}

func (s *Services) inputKeyState(key uint32, current bool) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	pressed := false
	for _, state := range s.inputStates {
		if state == nil {
			continue
		}
		if current {
			_, down := state.keys[key]
			pressed = pressed || down
		} else {
			_, recentlyPressed := state.pressed[key]
			delete(state.pressed, key)
			pressed = pressed || recentlyPressed
		}
	}
	return pressed
}

func (s *Services) readStorage(name string) ([]byte, error) {
	if isVarStorage(name) {
		if s.varStorageVM == nil {
			return nil, fmt.Errorf("varfile storage %q is unavailable until varfile.dll is linked", name)
		}
		return s.readVarStorage(s.varStorageVM, name)
	}
	if name == "" {
		return nil, fmt.Errorf("empty storage name")
	}
	if s.SaveDir != "" {
		save := SaveStorage{Root: s.SaveDir, Project: serviceProjectPath(s)}
		data, err := save.Read(name)
		if err == nil {
			return data, nil
		}
		if !errors.Is(err, ErrSaveNotFound) && !errors.Is(err, ErrInvalidSaveName) {
			return nil, err
		}
	}
	if s.Storage == nil {
		return nil, fmt.Errorf("%w: no storage", ErrUnsupportedDevice)
	}
	return s.Storage.Read(name)
}

func (s *Services) writeSave(name string, data []byte, offset int64) error {
	if isVarStorage(name) {
		if s.varStorageVM == nil {
			return fmt.Errorf("varfile storage %q is unavailable until varfile.dll is linked", name)
		}
		return s.writeVarStorage(s.varStorageVM, name, data)
	}
	if s.SaveDir == "" {
		return fmt.Errorf("%w: save storage is unavailable", ErrUnsupportedDevice)
	}
	save := SaveStorage{Root: s.SaveDir, Project: serviceProjectPath(s)}
	return save.writeAtMode(name, data, offset, 0o644)
}

// RegisterWindow associates a native object identity with host window state.
func (s *Services) RegisterWindow(id tjs.ObjectID, window *WindowState) error {
	s.ensureInit()
	if id == 0 || window == nil {
		return fmt.Errorf("invalid window")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.WindowObjects[id]; exists {
		return fmt.Errorf("window already registered")
	}
	s.WindowObjects[id] = window
	s.Windows[uint64(id)] = window
	s.Display.ensureInit()
	s.Display.Windows[uint64(id)] = window
	if s.MainWindow == 0 {
		s.MainWindow = uint64(id)
		if window.Caption == "" {
			window.Caption = s.SystemTitle
		} else {
			s.SystemTitle = window.Caption
		}
	}
	if s.Display.MainWindow == 0 {
		s.Display.MainWindow = uint64(id)
	}
	return nil
}
func (s *Services) systemTitle() string {
	if s == nil {
		return systemVersionString
	}
	s.ensureInit()
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.SystemTitle
}

func (s *Services) setSystemTitle(title string) {
	if s == nil {
		return
	}
	s.ensureInit()
	s.mu.Lock()
	s.SystemTitle = title
	s.systemTitleInitialized = true
	if window := s.Windows[s.MainWindow]; window != nil {
		window.SetCaption(title)
	}
	s.mu.Unlock()
}

func (s *Services) setWindowCaption(id uint64, caption string) {
	if s == nil {
		return
	}
	s.ensureInit()
	s.mu.Lock()
	if window := s.Windows[id]; window != nil {
		window.SetCaption(caption)
		if s.MainWindow == id {
			s.SystemTitle = caption
			s.systemTitleInitialized = true
		}
	}
	s.mu.Unlock()
}

const maxWindowObjects = 100_000

func (s *Services) addWindowObject(id tjs.ObjectID, value tjs.Value) error {
	s.ensureInit()
	s.mu.Lock()
	defer s.mu.Unlock()
	window := s.WindowObjects[id]
	if window == nil {
		return fmt.Errorf("Window.add: %w", ErrInvalidWindow)
	}
	if window.invalidating {
		return nil
	}
	for _, current := range window.registeredObjects {
		if current.StrictEqual(value) {
			return nil
		}
	}
	if len(window.registeredObjects) >= maxWindowObjects {
		return fmt.Errorf("Window.add: registered object limit exceeded")
	}
	window.registeredObjects = append(window.registeredObjects, value)
	return nil
}

func (s *Services) removeWindowObject(id tjs.ObjectID, value tjs.Value) error {
	s.ensureInit()
	s.mu.Lock()
	defer s.mu.Unlock()
	window := s.WindowObjects[id]
	if window == nil {
		return fmt.Errorf("Window.remove: %w", ErrInvalidWindow)
	}
	if window.invalidating {
		return nil
	}
	for i, current := range window.registeredObjects {
		if current.StrictEqual(value) {
			last := len(window.registeredObjects) - 1
			copy(window.registeredObjects[i:], window.registeredObjects[i+1:])
			window.registeredObjects[last] = tjs.VoidValue
			window.registeredObjects = window.registeredObjects[:last]
			break
		}
	}
	return nil
}

func (s *Services) beginWindowInvalidation(id tjs.ObjectID) ([]tjs.Value, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	window := s.WindowObjects[id]
	if window == nil || window.invalidating {
		return nil, false
	}
	window.invalidating = true
	window.Closed = true
	window.Visible = false
	window.Modal = false
	children := append([]tjs.Value(nil), window.registeredObjects...)
	if window.DrawDeviceObject.Kind() == tjs.KindObject && !window.DrawDeviceObject.IsNull() {
		children = append(children, window.DrawDeviceObject)
	}
	return children, true
}

func (s *Services) UnregisterWindow(id tjs.ObjectID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if window := s.WindowObjects[id]; window != nil {
		window.invalidating = true
		window.Closed = true
		window.Visible = false
		window.Modal = false
		window.registeredObjects = nil
		window.DrawDeviceObject = tjs.VoidValue
		window.primaryLayer = 0
	}
	for _, layer := range s.Layers {
		if layer.windowID == uint64(id) {
			layer.windowID = 0
		}
	}
	delete(s.inputStates, uint64(id))
	delete(s.WindowObjects, id)
	delete(s.Windows, uint64(id))
	if s.Display != nil {
		delete(s.Display.Windows, uint64(id))
	}
	if s.MainWindow == uint64(id) {
		s.MainWindow = 0
	}
	if s.Display != nil && s.Display.MainWindow == uint64(id) {
		s.Display.MainWindow = 0
	}
}
func (s *Services) Window(id tjs.ObjectID) *WindowState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.WindowObjects[id]
}
func (s *Services) SetRoot(root *Layer) {
	s.Root = root
	if root == nil {
		if s.Compositor != nil {
			s.Compositor.SetRoot(nil)
		}
		return
	}
	s.Compositor = NewCompositor(root.Bounds.Width, root.Bounds.Height)
	s.Compositor.SetRoot(root)
}
func (s *Services) SetFrameSize(width, height int32) error {
	if width <= 0 || height <= 0 {
		return ErrInvalidLayer
	}
	if s.Compositor == nil {
		s.Compositor = NewCompositor(width, height)
	} else {
		s.Compositor.Width, s.Compositor.Height = width, height
		if s.Root != nil && s.Root.windowID != 0 {
			if err := s.Root.SetSize(width, height); err != nil {
				return err
			}
		}
	}
	return nil
}
func (s *Services) CaptureFrame() (FrameSnapshot, error) {
	statsEnabled := os.Getenv("KRKRGO_RENDER_STATS") != ""
	frame, err := s.captureFrameWithPresentation(false)
	if err != nil {
		return FrameSnapshot{}, err
	}
	if !statsEnabled {
		return frame.Clone(), nil
	}
	started := time.Now()
	owned := frame.Clone()
	s.renderDiag.frameCopyTime += time.Since(started)
	return owned, nil
}

// CaptureFrameForPresentation returns a borrowed frame for synchronous host
// presentation. Its slices remain valid only until the next capture call on
// this Services; callers must not retain or mutate them.
func (s *Services) CaptureFrameForPresentation() (FrameSnapshot, error) {
	return s.captureFrameWithPresentation(true)
}

// CaptureFrameBorrowed returns a CPU frame without cloning its pixel buffers.
// The slices remain valid until the next capture call on this Services.
func (s *Services) CaptureFrameBorrowed() (FrameSnapshot, error) {
	return s.captureFrameWithPresentation(false)
}
func (s *Services) captureFrame() (FrameSnapshot, error) {
	return s.captureFrameWithPresentation(false)
}
func (s *Services) validateRootTree(root *Layer) error {
	if s == nil || root == nil {
		return nil
	}
	if s.validatedTreeRoot == root && s.validatedTreeRevision == root.subtreeRevision {
		return s.validatedTreeError
	}
	err := validateTree(root, nil)
	s.validatedTreeRoot = root
	s.validatedTreeRevision = root.subtreeRevision
	s.validatedTreeError = err
	return err
}

func materializeLayerTree(layer *Layer) error {
	if layer == nil {
		return nil
	}
	if err := layer.materializeGPUMotion(); err != nil {
		return err
	}
	if err := layer.materializeGPUStretch(); err != nil {
		return err
	}
	for _, child := range layer.children {
		if err := materializeLayerTree(child); err != nil {
			return err
		}
	}
	return nil
}

func (s *Services) captureFrameWithPresentation(presentation bool) (FrameSnapshot, error) {
	statsEnabled := os.Getenv("KRKRGO_RENDER_STATS") != ""
	var timings frameCaptureTimings
	if s.Compositor == nil && s.Root == nil {
		// A session can be captured before its script creates a root layer.
		// In that state there is no display-sized frame to compose yet.
		return FrameSnapshot{}, nil
	}
	if s.Compositor == nil {
		s.Compositor = NewCompositor(s.Root.Bounds.Width, s.Root.Bounds.Height)
		s.Compositor.Root = s.Root
	}
	c := s.Compositor
	if c.Root != nil {
		started := time.Time{}
		if statsEnabled {
			started = time.Now()
		}
		err := s.validateRootTree(c.Root)
		if statsEnabled {
			timings.validate = time.Since(started)
		}
		if err != nil {
			return FrameSnapshot{}, err
		}
	}
	if err := materializeLayerTree(c.Root); err != nil {
		return FrameSnapshot{}, err
	}
	var walkStarted time.Time
	if statsEnabled {
		walkStarted = time.Now()
	}
	transitions := activeLayerTransitionCount(c.Root)
	videos := s.playingVideoCount()
	if statsEnabled {
		timings.transitionWalk = time.Since(walkStarted)
	}
	if transitions != 0 || videos != 0 {
		if statsEnabled && transitions != 0 {
			mode := firstTransitionAlphaMode(c.Root)
			d := &s.renderDiag
			if mode != "" {
				if d.transitionAlphaMode == "" {
					d.transitionAlphaMode = mode
				}
				d.transitionAlphaFrames++
			}
		}
		// Transitions rewrite their full pixel buffer each update; playing
		// videos may replace RGBA from presentLocked. Both are guaranteed
		// dynamic, so hashing their image bytes before composition is wasted.
		invalidated := s.frameCacheValid
		s.frameCacheValid = false
		var started time.Time
		if statsEnabled {
			started = time.Now()
		}
		if presentation {
			var transition *Layer
			var ok bool
			if statsEnabled {
				var reason string
				transition, ok, reason = c.fullScreenOpaqueTransitionDiagnostic()
				d := &s.renderDiag
				d.presentationTransitionCandidates++
				if ok {
					d.presentationTransitionHits++
				} else {
					if d.presentationTransitionFailure == "" {
						d.presentationTransitionFailure = reason
					}
					d.presentationTransitionFailures++
				}
			} else {
				transition, ok = c.fullScreenOpaqueTransition()
			}
			if ok {
				composed := FrameSnapshot{Width: c.Width, Height: c.Height, RGBA: transition.transitionPixels, Opaque: true}
				s.recordFrameCaptureTimed(composed, true, false, invalidated, 0, false, transitions, videos, timings, statsEnabled)
				return composed, nil
			}
		}
		composed, err := c.composeInto(s.dynamicRGBA, s.dynamicProvince, true)
		if statsEnabled {
			timings.compose = time.Since(started)
		}
		if err != nil {
			return FrameSnapshot{}, err
		}
		s.dynamicRGBA, s.dynamicProvince = composed.RGBA, composed.Province
		s.recordFrameCaptureTimed(composed, true, false, invalidated, 0, false, transitions, videos, timings, statsEnabled)
		return composed, nil
	}
	if !s.frameCacheValid {
		s.frameCacheSeed = maphash.MakeSeed()
	}
	var state maphash.Hash
	state.SetSeed(s.frameCacheSeed)
	var word [8]byte
	write := func(v uint64) {
		binary.LittleEndian.PutUint64(word[:], v)
		_, _ = state.Write(word[:])
	}
	if statsEnabled {
		walkStarted = time.Now()
	}
	write(uint64(uint32(c.Width)))
	write(uint64(uint32(c.Height)))
	for _, component := range c.Background {
		write(uint64(component))
	}
	if err := hashLayerRenderState(&state, c.Root, write); err != nil {
		return FrameSnapshot{}, err
	}
	fingerprint := state.Sum64()
	if statsEnabled {
		timings.cacheHash = time.Since(walkStarted)
		walkStarted = time.Now()
	}
	if s.frameCacheValid && fingerprint == s.frameCacheHash {
		frame := s.frameCache
		if statsEnabled {
			timings.cacheLookup = time.Since(walkStarted)
		}
		s.recordFrameCaptureTimed(frame, false, true, false, fingerprint, true, 0, 0, timings, statsEnabled)
		return frame, nil
	}
	if statsEnabled {
		timings.cacheLookup = time.Since(walkStarted)
	}
	invalidated := s.frameCacheValid && fingerprint != s.frameCacheHash
	if statsEnabled {
		walkStarted = time.Now()
	}
	frame, err := c.composeValidated()
	if statsEnabled {
		timings.compose = time.Since(walkStarted)
	}
	if err != nil {
		return FrameSnapshot{}, err
	}
	frame.RenderStateHash = fingerprint
	frame.RenderStateHashValid = true
	s.frameCache = frame
	s.frameCacheHash = fingerprint
	s.frameCacheValid = true
	s.recordFrameCaptureTimed(frame, true, false, invalidated, fingerprint, true, 0, 0, timings, statsEnabled)
	return frame, nil
}

func (s *Services) recordFrameCaptureTimed(frame FrameSnapshot, composed, cacheHit, invalidated bool, renderState uint64, renderStateValid bool, transitions, videos int, timings frameCaptureTimings, statsEnabled bool) {
	if !statsEnabled {
		s.recordFrameCapture(frame, composed, cacheHit, invalidated, renderState, renderStateValid, transitions, videos, timings)
		return
	}
	started := time.Now()
	s.recordFrameCapture(frame, composed, cacheHit, invalidated, renderState, renderStateValid, transitions, videos, timings)
	s.renderDiag.recordTime += time.Since(started)
}

func (s *Services) playingVideoCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	count := 0
	for _, video := range s.Videos {
		if video.Status() == VideoPlaying {
			count++
		}
	}
	return count
}

func activeLayerTransitionCount(layer *Layer) int {
	if layer == nil {
		return 0
	}
	count := 0
	if layer.inTransition {
		count++
	}
	for _, child := range layer.children {
		count += activeLayerTransitionCount(child)
	}
	return count
}

func firstTransitionAlphaMode(layer *Layer) string {
	if layer == nil {
		return ""
	}
	if layer.inTransition {
		return fmt.Sprintf("source=%s,destination=%s", layer.transitionSourceAlpha, layer.transitionDestAlpha)
	}
	for _, child := range layer.children {
		if mode := firstTransitionAlphaMode(child); mode != "" {
			return mode
		}
	}
	return ""
}

// hashLayerRenderState fingerprints caller-visible render state, including
// exported pixel slices whose contents can change without a setter call.
func hashLayerRenderState(state *maphash.Hash, layer *Layer, write func(uint64)) error {
	return hashVisibleLayerRenderState(state, layer, write, true)
}

func hashVisibleLayerRenderState(state *maphash.Hash, layer *Layer, write func(uint64), ancestorsVisible bool) error {
	if layer == nil {
		write(0)
		return nil
	}
	var values [25]uint64
	values[0] = 1
	values[1] = uint64(uint32(layer.Bounds.X))
	values[2] = uint64(uint32(layer.Bounds.Y))
	values[3] = uint64(uint32(layer.Bounds.Width))
	values[4] = uint64(uint32(layer.Bounds.Height))
	values[5] = uint64(layer.Opacity)
	values[6] = boolUint64(layer.Visible)
	values[7] = uint64(layer.Type)
	values[8] = uint64(uint32(layer.ImageWidth))
	values[9] = uint64(uint32(layer.ImageHeight))
	values[10] = uint64(uint32(layer.ImageLeft))
	values[11] = uint64(uint32(layer.ImageTop))
	values[12] = boolUint64(layer.clipSet)
	values[13] = uint64(uint32(layer.clip.X))
	values[14] = uint64(uint32(layer.clip.Y))
	values[15] = uint64(uint32(layer.clip.Width))
	values[16] = uint64(uint32(layer.clip.Height))
	values[17] = boolUint64(layer.transitionHidden)
	values[18] = boolUint64(layer.inTransition)
	values[19] = uint64(len(layer.children))
	values[20] = uint64(uint32(layer.ProvinceWidth))
	values[21] = uint64(uint32(layer.ProvinceHeight))
	values[22] = uint64(len(layer.RGBA))
	values[23] = uint64(len(layer.Province))
	values[24] = uint64(len(layer.transitionPixels))
	var encoded [len(values) * 8]byte
	for i, value := range values {
		binary.LittleEndian.PutUint64(encoded[i*8:], value)
	}
	_, _ = state.Write(encoded[:])

	// Hidden subtrees are not traversed by drawLayerTo. Their metadata still
	// participates in the fingerprint, so making one visible invalidates the
	// cache, but scanning caller-mutable image bytes cannot affect output.
	visible := ancestorsVisible && !layer.transitionHidden && layer.Visible && layer.Opacity != 0
	if visible {
		// Hash every caller-visible source slice, not just the pixels selected
		// for this frame: callers can mutate these slices without a setter.
		_, _ = state.Write(layer.RGBA)
		_, _ = state.Write(layer.Province)
		_, _ = state.Write(layer.transitionPixels)
	}
	for _, child := range layer.children {
		if err := hashVisibleLayerRenderState(state, child, write, visible); err != nil {
			return err
		}
	}
	return nil
}

func boolUint64(value bool) uint64 {
	if value {
		return 1
	}
	return 0
}
func (s *Services) CaptureWindow(id uint64) (FrameSnapshot, error) {
	if id != 0 && s.MainWindow != id {
		s.mu.RLock()
		_, ok := s.Windows[id]
		s.mu.RUnlock()
		if !ok {
			return FrameSnapshot{}, ErrInvalidWindow
		}
	}
	return s.CaptureFrame()
}

func (s *Services) GCRoots() []tjs.Value {
	// Copy every host-owned handle while holding the mapping lock. The VM
	// collector consumes this detached snapshot after the lock is released.
	s.mu.RLock()
	out := append([]tjs.Value(nil), s.Roots...)
	if s.console.Kind() == tjs.KindObject && !s.console.IsNull() {
		out = append(out, s.console)
	}
	for id, window := range s.WindowObjects {
		out = append(out, tjs.ObjectIDValue(id))
		if window == nil {
			continue
		}
		for _, object := range window.registeredObjects {
			out = append(out, object)
			if object.Kind() == tjs.KindObject {
				ref := object.ObjectRef()
				if ref.Context != 0 && ref.Context != ref.Object {
					out = append(out, tjs.ObjectIDValue(ref.Context))
				}
			}
		}
		if window.ExSystemMenu.Kind() == tjs.KindObject && !window.ExSystemMenu.IsNull() {
			out = append(out, window.ExSystemMenu)
			ref := window.ExSystemMenu.ObjectRef()
			if ref.Context != 0 && ref.Context != ref.Object {
				out = append(out, tjs.ObjectIDValue(ref.Context))
			}
		}
		if window.DrawDeviceObject.Kind() == tjs.KindObject && !window.DrawDeviceObject.IsNull() {
			out = append(out, window.DrawDeviceObject)
			ref := window.DrawDeviceObject.ObjectRef()
			if ref.Context != 0 && ref.Context != ref.Object {
				out = append(out, tjs.ObjectIDValue(ref.Context))
			}
		}
	}
	for id := range s.Layers {
		out = append(out, tjs.ObjectIDValue(id))
		if layer := s.Layers[id]; layer != nil && layer.transitionCallback.Kind() == tjs.KindObject && !layer.transitionCallback.IsNull() {
			out = append(out, layer.transitionCallback)
		}
	}
	for _, value := range s.LayerChildren {
		if value.Kind() == tjs.KindObject && !value.IsNull() {
			out = append(out, value)
		}
	}
	for _, value := range s.LayerCursors {
		if value.Kind() == tjs.KindObject && !value.IsNull() {
			out = append(out, value)
		}
	}
	for _, parser := range s.CSVParsers {
		if parser != nil && parser.target.Kind() == tjs.KindObject && !parser.target.IsNull() {
			out = append(out, parser.target)
		}
	}
	for id := range s.Fonts {
		out = append(out, tjs.ObjectIDValue(id))
	}
	for id := range s.TextRenderers {
		out = append(out, tjs.ObjectIDValue(id))
	}
	if s.Timers != nil {
		for id := range s.Timers.timers {
			out = append(out, tjs.ObjectIDValue(tjs.ObjectID(id)))
		}
	}
	for _, action := range s.TimerActions {
		out = append(out, tjs.ObjectValue(action.owner))
	}
	for _, action := range s.AsyncTriggerActions {
		out = append(out, tjs.ObjectValue(action.owner))
	}
	for id := range s.Sounds {
		out = append(out, tjs.ObjectIDValue(id))
	}
	if s.Events != nil {
		for _, event := range s.Events.Pending() {
			if event.Target != 0 {
				out = append(out, tjs.ObjectIDValue(tjs.ObjectID(event.Target)))
			}
		}
	}
	for _, callback := range s.ContinuousHandlers {
		out = append(out, callback)
	}
	out = append(out, s.menuData.gcRoots()...)
	s.mu.RUnlock()
	s.kagHookMu.Lock()
	for _, registrations := range s.pendingKAGHooks {
		for _, registration := range registrations {
			out = append(out, registration.handle, registration.fn, registration.context)
		}
	}
	s.kagHookMu.Unlock()
	return out
}
func (s *Services) AddRoot(value tjs.Value) {
	s.mu.Lock()
	s.Roots = append(s.Roots, value)
	s.mu.Unlock()
}
func (s *Services) RemoveRoot(value tjs.Value) {
	s.mu.Lock()
	for i, x := range s.Roots {
		if x.StrictEqual(value) {
			s.Roots = append(s.Roots[:i], s.Roots[i+1:]...)
			break
		}
	}
	s.mu.Unlock()
}
func (s *Services) GCTrace(id tjs.ObjectID) []tjs.Value {
	return s.menuData.gcTrace(id)
}
func (s *Services) UnixTimeMS() int64 {
	n := s.TimeMS
	if n > uint64(^uint64(0)>>1) {
		n = uint64(^uint64(0) >> 1)
	}
	return s.EpochMS + int64(n)
}
func (s *Services) systemTickCount() int64 {
	s.clockPolls++
	milliseconds := s.TimeMS + s.clockPolls/100
	if milliseconds > uint64(^uint64(0)>>1) {
		milliseconds = uint64(^uint64(0) >> 1)
	}
	return int64(milliseconds)
}
func (s *Services) TraceInstruction(instruction *tjs.Instruction) error {
	if !s.TraceEnabled || instruction == nil {
		return nil
	}
	s.TraceEvents = append(s.TraceEvents, TraceEvent{TimeMS: s.TimeMS, Storage: instruction.Location.Storage, Line: instruction.Location.Line, Column: instruction.Location.Column, Operation: string(instruction.Op.Code)})
	return nil
}
func (s *Services) Trace(instruction *tjs.Instruction) error { return s.TraceInstruction(instruction) }

// RegisterSound attaches a headless WaveSoundBuffer to a stable script object.
func (s *Services) RegisterSound(id tjs.ObjectID, sound *WaveSoundBuffer) error {
	if s == nil || id == 0 || sound == nil {
		return fmt.Errorf("invalid sound registration")
	}
	s.ensureInit()
	s.mu.Lock()
	defer s.mu.Unlock()
	if previous := s.Sounds[id]; previous != sound {
		s.AudioMixer.Remove(previous)
		delete(s.SoundEvents, id)
		s.Events.Cancel(uint64(id), EventTrigger)
		s.Events.Cancel(uint64(id), EventSoundTrigger)
	}
	s.Sounds[id] = sound
	sound.positionClock = s.TimeMS
	s.AudioMixer.Add(sound)
	return nil
}
func (s *Services) Sound(id tjs.ObjectID) *WaveSoundBuffer {
	if s == nil {
		return nil
	}
	s.ensureInit()
	s.mu.RLock()
	sound := s.Sounds[id]
	s.mu.RUnlock()
	return sound
}
func (s *Services) DrainSoundEvents(id tjs.ObjectID) []SoundEvent {
	if s == nil {
		return nil
	}
	s.ensureInit()
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.SoundEvents[id]
	delete(s.SoundEvents, id)
	return out
}
func (s *Services) queueSoundEvents(id tjs.ObjectID, sound *WaveSoundBuffer) error {
	events := sound.Events()
	if len(events) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.SoundEvents[id])+len(events) > maxSoundEvents {
		return fmt.Errorf("sound event queue limit exceeded")
	}
	if err := s.Events.PostSoundBatch(uint64(id), EventTrigger, PriorityNormal, len(events)); err != nil {
		return err
	}
	s.SoundEvents[id] = append(s.SoundEvents[id], events...)
	sound.DrainEvents()
	return nil
}

func (s *Services) stopAllSounds() error {
	s.mu.RLock()
	ids := make([]tjs.ObjectID, 0, len(s.Sounds))
	for id := range s.Sounds {
		ids = append(ids, id)
	}
	s.mu.RUnlock()
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		sound := s.Sound(id)
		if sound == nil {
			continue
		}
		if err := sound.Stop(); err != nil {
			return err
		}
		if err := s.queueSoundEvents(id, sound); err != nil {
			return err
		}
	}
	return nil
}

// SoundNative invokes the script-facing WaveSoundBuffer operation set.
func (s *Services) SoundNative(vm *tjs.VM, context tjs.Value, operation string, args []tjs.Value) (tjs.Value, error) {

	s.ensureInit()
	if operation == "setDefaultCounts" || operation == "setDefaultAheads" {
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("WaveSoundBuffer.%s: missing argument 0", operation)
		}
		value, err := args[0].Int()
		if err != nil {
			return tjs.VoidValue, err
		}
		if operation == "setDefaultCounts" {
			if value < 0 {
				return tjs.VoidValue, fmt.Errorf("WaveSoundBuffer.setDefaultCounts requires a non-negative count")
			}
			s.sampleDefaultCounts = int(value)
		} else {
			s.sampleDefaultAheads = int(value)
		}
		return tjs.VoidValue, nil
	}
	if operation == "stopAll" {
		return tjs.VoidValue, s.stopAllSounds()
	}
	if operation == "get:globalVolume" || operation == "getGlobalVolume" {
		return tjs.IntegerValue(int64(s.AudioMixer.GetGlobalVolume())), nil
	}
	if operation == "set:globalVolume" || operation == "setGlobalVolume" {
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("WaveSoundBuffer: missing argument 0")
		}
		value, err := args[0].Int()
		if err != nil {
			return tjs.VoidValue, err
		}
		s.AudioMixer.SetGlobalVolume(int32(value))
		return tjs.VoidValue, nil
	}
	if operation == "get:globalFocusMode" {
		return tjs.IntegerValue(0), nil
	}
	if operation == "set:globalFocusMode" {
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("WaveSoundBuffer: missing argument 0")
		}
		value, err := args[0].Int()
		if err != nil {
			return tjs.VoidValue, err
		}
		if value == 0 {
			return tjs.VoidValue, nil
		}
		return tjs.VoidValue, UnsupportedDevice("audio focus mode")
	}
	if context.Kind() != tjs.KindObject || context.IsNull() {
		return tjs.VoidValue, fmt.Errorf("WaveSoundBuffer requires object context")
	}
	id := context.ObjectRef().Object
	if operation == "@initialize" || operation == "WaveSoundBuffer" {
		s.mu.RLock()
		sound := s.Sounds[id]
		s.mu.RUnlock()
		if sound == nil {
			if err := s.RegisterSound(id, NewWaveSoundBuffer()); err != nil {
				return tjs.VoidValue, err
			}
		} else {
			s.AudioMixer.Add(sound)
		}
		return tjs.VoidValue, nil
	}
	if operation == "@invalidate" || operation == "finalize" || operation == "close" {
		s.mu.Lock()
		sound := s.Sounds[id]
		delete(s.Sounds, id)
		delete(s.SoundEvents, id)
		s.mu.Unlock()
		if sound != nil {
			_ = sound.Stop()
			s.AudioMixer.Remove(sound)
		}
		s.Events.Cancel(uint64(id), EventTrigger)
		s.Events.Cancel(uint64(id), EventSoundTrigger)
		return tjs.VoidValue, nil
	}
	s.mu.RLock()
	sound := s.Sounds[id]
	s.mu.RUnlock()
	if sound == nil {
		sound = NewWaveSoundBuffer()
		if err := s.RegisterSound(id, sound); err != nil {
			return tjs.VoidValue, err
		}
	} else {
		s.AudioMixer.Add(sound)
	}
	arg := func(i int) (tjs.Value, error) {
		if i < 0 || i >= len(args) {
			return tjs.VoidValue, fmt.Errorf("WaveSoundBuffer: missing argument %d", i)
		}
		return args[i], nil
	}
	intArg := func(i int) (int64, error) {
		x, e := arg(i)
		if e != nil {
			return 0, e
		}
		return x.Int()
	}
	boolArg := func(i int) (bool, error) {
		x, e := arg(i)
		if e != nil {
			return false, e
		}
		return x.Truth()
	}
	queue := func() (tjs.Value, error) { return tjs.VoidValue, s.queueSoundEvents(id, sound) }
	if operation == "get:sampleValue" || operation == "get:sampleCount" || operation == "set:sampleCount" || operation == "get:sampleAhead" || operation == "set:sampleAhead" {
		sound.configureSampleProperties(s.sampleDefaultCounts, s.sampleDefaultAheads)
	}
	switch operation {
	case "open", "openBytes":
		x, e := arg(0)
		if e != nil {
			return tjs.VoidValue, e
		}
		var data []byte
		if x.Kind() == tjs.KindOctet {
			data = x.Octets()
		} else if operation == "openBytes" {
			return tjs.VoidValue, fmt.Errorf("WaveSoundBuffer.openBytes requires octets")
		} else {
			data, e = s.readStorage(strings.SplitN(x.Text(), "?", 2)[0])
			if e != nil {
				return tjs.VoidValue, e
			}
		}
		if e = sound.OpenBytes(data, assets.LoopInfo{}); e != nil {
			return tjs.VoidValue, e
		}
		sound.positionClock = s.TimeMS
		return queue()
	case "play":
		e := sound.Play()
		if e != nil {
			return tjs.VoidValue, e
		}
		return queue()
	case "stop", "rewind":
		e := sound.Stop()
		if e != nil {
			return tjs.VoidValue, e
		}
		return queue()
	case "pause":
		paused := true
		if len(args) > 0 {
			var e error
			paused, e = boolArg(0)
			if e != nil {
				return tjs.VoidValue, e
			}
		}
		sound.SetPaused(paused)
		return tjs.VoidValue, nil
	case "setPos":
		n, e := intArg(0)
		if e != nil {
			return tjs.VoidValue, e
		}
		if n < 0 {
			n = 0
		}
		return tjs.VoidValue, sound.SetPosition(uint64(n))
	case "setSamplePosition":
		n, e := intArg(0)
		if e != nil {
			return tjs.VoidValue, e
		}
		if n < 0 {
			n = 0
		}
		return tjs.VoidValue, sound.SetSamplePosition(uint64(n))
	case "fade":
		target, e := intArg(0)
		if e != nil {
			return tjs.VoidValue, e
		}
		duration, e := intArg(1)
		if e != nil {
			return tjs.VoidValue, e
		}
		var delay int64
		if len(args) > 2 {
			delay, e = intArg(2)
			if e != nil {
				return tjs.VoidValue, e
			}
		}
		sound.positionClock = s.TimeMS
		if e = sound.Fade(int32(target), int32(duration), int32(delay)); e != nil {
			return tjs.VoidValue, e
		}
		return queue()
	case "stopFade":
		sound.StopFade()
		return queue()
	case "onStatusChanged", "onLabel", "onFadeCompleted":
		return tjs.VoidValue, nil
	case "get:status":
		return tjs.StringValue(sound.Status()), nil
	case "get:position":
		return tjs.IntegerValue(int64(sound.Position())), nil
	case "get:samplePosition":
		return tjs.IntegerValue(int64(sound.SamplePosition())), nil
	case "get:totalTime":
		return tjs.IntegerValue(int64(sound.TotalTime())), nil
	case "get:frequency":
		return tjs.IntegerValue(int64(sound.Frequency())), nil
	case "get:bits":
		return tjs.IntegerValue(int64(sound.Bits())), nil
	case "get:channels":
		return tjs.IntegerValue(int64(sound.Channels())), nil
	case "get:volume":
		return tjs.IntegerValue(int64(sound.Volume())), nil
	case "get:volume2":
		return tjs.IntegerValue(int64(sound.Volume2())), nil
	case "get:pan":
		return tjs.IntegerValue(int64(sound.Pan())), nil
	case "get:paused":
		return tjs.IntegerValue(boolInt(sound.Paused())), nil
	case "get:looping":
		return tjs.IntegerValue(boolInt(sound.Looping())), nil
	case "get:useVisBuffer":
		return tjs.IntegerValue(boolInt(sound.UseVisBuffer())), nil
	case "get:sampleValue":
		sound.SetUseVisBuffer(true)
		return tjs.RealValue(sound.sampleValue()), nil
	case "get:sampleCount":
		return tjs.IntegerValue(int64(sound.sampleCount)), nil
	case "set:sampleCount":
		n, e := intArg(0)
		if e != nil {
			return tjs.VoidValue, e
		}
		if n < 0 {
			return tjs.VoidValue, fmt.Errorf("WaveSoundBuffer.sampleCount requires a non-negative count")
		}
		sound.sampleCount = int(n)
		return tjs.VoidValue, nil
	case "get:sampleAhead":
		return tjs.IntegerValue(int64(sound.sampleAhead)), nil
	case "set:sampleAhead":
		n, e := intArg(0)
		if e != nil {
			return tjs.VoidValue, e
		}
		sound.sampleAhead = int(n)
		return tjs.VoidValue, nil
	case "get:labels":
		if vm == nil {
			return tjs.VoidValue, fmt.Errorf("WaveSoundBuffer labels require VM")
		}
		labels := sound.Labels()
		values := make([]tjs.Value, len(labels))
		for i, label := range labels {
			values[i] = tjs.StringValue(label.Name)
		}
		return vm.NewArray(values)
	case "set:volume":
		n, e := intArg(0)
		if e != nil {
			return tjs.VoidValue, e
		}
		sound.SetVolume(int32(n))
		return tjs.VoidValue, nil
	case "set:volume2":
		n, e := intArg(0)
		if e != nil {
			return tjs.VoidValue, e
		}
		sound.SetVolume2(int32(n))
		return tjs.VoidValue, nil
	case "set:pan":
		n, e := intArg(0)
		if e != nil {
			return tjs.VoidValue, e
		}
		sound.SetPan(int32(n))
		return tjs.VoidValue, nil
	case "set:paused":
		b, e := boolArg(0)
		if e != nil {
			return tjs.VoidValue, e
		}
		sound.SetPaused(b)
		return tjs.VoidValue, nil
	case "set:looping":
		b, e := boolArg(0)
		if e != nil {
			return tjs.VoidValue, e
		}
		sound.SetLooping(b)
		return tjs.VoidValue, nil
	case "set:frequency":
		n, e := intArg(0)
		if e != nil {
			return tjs.VoidValue, e
		}
		return tjs.VoidValue, sound.SetFrequency(int32(n))
	case "set:useVisBuffer":
		b, e := boolArg(0)
		if e != nil {
			return tjs.VoidValue, e
		}
		sound.SetUseVisBuffer(b)
		return tjs.VoidValue, nil
	case "set:position":
		n, e := intArg(0)
		if e != nil {
			return tjs.VoidValue, e
		}
		if n < 0 {
			n = 0
		}
		return tjs.VoidValue, sound.SetPosition(uint64(n))
	case "set:samplePosition":
		n, e := intArg(0)
		if e != nil {
			return tjs.VoidValue, e
		}
		if n < 0 {
			n = 0
		}
		return tjs.VoidValue, sound.SetSamplePosition(uint64(n))
	case "setDevice", "getDevice":
		return tjs.VoidValue, UnsupportedDevice("audio device")
	default:
		return tjs.VoidValue, fmt.Errorf("unknown WaveSoundBuffer operation: %s", operation)
	}
}

// MIDISoundNative exposes the legacy MIDI buffer API when the host has no MIDI
// output backend. The reference runtime keeps this class registered in that
// configuration; its MIDI operations are disabled while base status and volume
// state remain available.
func (s *Services) MIDISoundNative(vm *tjs.VM, context tjs.Value, operation string, args []tjs.Value) (tjs.Value, error) {
	return s.disabledLegacySoundNative(vm, context, operation, args, "MIDISoundBuffer")
}

func (s *Services) CDDASoundNative(vm *tjs.VM, context tjs.Value, operation string, args []tjs.Value) (tjs.Value, error) {
	return s.disabledLegacySoundNative(vm, context, operation, args, "CDDASoundBuffer")
}

func (s *Services) disabledLegacySoundNative(vm *tjs.VM, context tjs.Value, operation string, args []tjs.Value, className string) (tjs.Value, error) {
	switch operation {
	case "@initialize":
		if _, err := s.SoundNative(vm, context, "@initialize", args); err != nil {
			return tjs.VoidValue, err
		}
		if context.Kind() == tjs.KindObject && !context.IsNull() {
			s.mu.Lock()
			if sound := s.Sounds[context.ObjectRef().Object]; sound != nil {
				sound.volume2 = 0
			}
			s.mu.Unlock()
		}
		return tjs.VoidValue, nil
	case "@invalidate", "finalize":
		return s.SoundNative(vm, context, operation, args)
	case "MIDISoundBuffer", "CDDASoundBuffer", "play", "stop", "stopFade", "onStatusChanged", "onFadeCompleted":
		return tjs.VoidValue, nil
	case "open":
		if len(args) < 1 {
			return tjs.VoidValue, fmt.Errorf("%s.open: missing argument 0", className)
		}
		return tjs.VoidValue, nil
	case "fade":
		if len(args) < 2 {
			return tjs.VoidValue, fmt.Errorf("%s.fade: missing argument", className)
		}
		return tjs.VoidValue, nil
	case "get:status", "get:volume", "set:volume":
		return s.SoundNative(vm, context, operation, args)
	case "get:volume2":
		return tjs.IntegerValue(0), nil
	case "set:volume2", "get:position", "set:position", "get:paused", "set:paused", "get:totalTime", "set:totalTime", "get:looping", "set:looping":
		return tjs.VoidValue, nil
	default:
		return tjs.VoidValue, fmt.Errorf("unsupported %s operation %q", className, operation)
	}
}

// Call implements tjs.Host. Context-sensitive operations are handled by
// CallWithContext, while unknown devices fail explicitly rather than no-oping.
func (s *Services) Call(vm *tjs.VM, name string, args []tjs.Value, budget *uint64) (tjs.Value, error) {
	return s.call(vm, name, tjs.Value{}, args, budget, true)
}
func (s *Services) CallWithContext(vm *tjs.VM, name string, context tjs.Value, args []tjs.Value, budget *uint64) (tjs.Value, error) {
	return s.call(vm, name, context, args, budget, true)
}
func (s *Services) preloadWin32Dialog(vm *tjs.VM, budget *uint64) error {
	if s == nil || s.Storage == nil {
		return nil
	}
	if _, err := s.readStorage("system/win32dialog.tjs"); err != nil {
		if errors.Is(err, assets.ErrNotFound) {
			return nil
		}
		return err
	}
	if err := installWin32DialogNative(vm); err != nil {
		return err
	}
	if _, ok := vm.Globals["WIN32GenericDialogEX"]; ok {
		return nil
	}
	return s.executePluginScript(vm, "system/win32dialog.tjs", budget)
}

func (s *Services) CallWithResult(vm *tjs.VM, name string, context tjs.Value, args []tjs.Value, budget *uint64, resultNeeded bool) (tjs.Value, error) {
	return s.call(vm, name, context, args, budget, resultNeeded)
}
func (s *Services) call(vm *tjs.VM, name string, context tjs.Value, args []tjs.Value, budget *uint64, resultNeeded bool) (tjs.Value, error) {
	if name == "addKagHookCallback" {
		return tjs.VoidValue, s.registerLegacyKAGHook(vm, args, budget)
	}
	if name == "getSysSE" {
		value, err := vm.NewDictionary()
		if err != nil {
			return tjs.VoidValue, err
		}
		object, ok := vm.Objects.Lookup(value.ObjectRef().Object)
		if !ok {
			return tjs.VoidValue, fmt.Errorf("getSysSE result object is unavailable")
		}
		if err := object.Set("storage", tjs.StringValue("")); err != nil {
			return tjs.VoidValue, err
		}
		if err := object.Set("buf", tjs.IntegerValue(0)); err != nil {
			return tjs.VoidValue, err
		}
		return value, nil
	}
	if name == "BookMarkIO" || name == "_BookMarkIO" || name == "getBookMarkFileNameAtNum" || name == "_getBookMarkFileNameAtNum" {
		return tjs.StringValue(""), nil
	}

	if name == "getBookMarkFileNameAtNum" {
		return tjs.StringValue(""), nil
	}
	if name == "goBackHistory" {
		return tjs.VoidValue, nil
	}
	if name == "goBackTitle" {
		return tjs.VoidValue, nil
	}
	if name == "endRecollection" {
		return tjs.VoidValue, nil
	}
	if name == "loadEmergency" {
		return tjs.VoidValue, nil
	}
	if name == "exists" {
		return s.storagesNative(vm, "isExistentStorage", args)
	}
	if name == "cancel" {
		return tjs.VoidValue, nil
	}
	if name == "set" {
		if context.Kind() != tjs.KindObject || context.IsNull() || len(args) < 2 {
			return tjs.VoidValue, fmt.Errorf("set requires an object, key, and value")
		}
		target, ok := vm.Objects.Lookup(context.ObjectRef().Object)
		if !ok {
			return tjs.VoidValue, fmt.Errorf("set target is unavailable")
		}
		return tjs.VoidValue, target.Set(args[0].Text(), args[1])
	}
	if name == "get" {
		if context.Kind() != tjs.KindObject || context.IsNull() || len(args) < 1 {
			return tjs.VoidValue, fmt.Errorf("get requires an object and key")
		}
		target, ok := vm.Objects.Lookup(context.ObjectRef().Object)
		if !ok {
			return tjs.VoidValue, fmt.Errorf("get target is unavailable")
		}
		if value, found := target.Get(args[0].Text()); found {
			return value, nil
		}
		return tjs.VoidValue, nil
	}
	if name == "load" {
		return tjs.VoidValue, nil
	}
	if name == "getValue" {
		if context.Kind() != tjs.KindObject || context.IsNull() || len(args) < 1 {
			return tjs.VoidValue, fmt.Errorf("getValue requires an object and key")
		}
		target, ok := vm.Objects.Lookup(context.ObjectRef().Object)
		if !ok {
			return tjs.VoidValue, fmt.Errorf("getValue target is unavailable")
		}
		if value, found := target.Get(args[0].Text()); found {
			return value, nil
		}
		return tjs.VoidValue, nil
	}
	if name == "showHistoryByKey" {
		return tjs.VoidValue, nil
	}
	if name == "callHook" {
		return tjs.VoidValue, nil
	}
	if name == "checkSave" {
		return tjs.IntegerValue(0), nil
	}
	if name == "updateQuickSaveDataExists" {
		return tjs.VoidValue, nil
	}
	if name == "entryKagHooks" {
		return tjs.VoidValue, nil
	}
	if name == "onResetWindowSizeMenuItemClick" {
		return tjs.VoidValue, nil
	}
	if name == "onDisableWinwodResizeMenuItemClick" {
		return tjs.VoidValue, nil
	}
	if name == "getWindowResMenuItem" {
		return tjs.IntegerValue(1), nil
	}
	if name == "add" {
		if context.Kind() == tjs.KindObject && !context.IsNull() && context.ObjectRef().Object != 0 {
			model := s.menuState()
			if _, ok := model.items[context.ObjectRef().Object]; ok {
				return s.menuNative(vm, context, "add", args, budget)
			}
		}
		return tjs.VoidValue, nil
	}
	if name == "tagApply" {
		if len(args) > 0 {
			return args[0], nil
		}
		return tjs.StringValue(""), nil
	}
	if name == "setup" {
		return tjs.VoidValue, nil
	}
	if name == "createBookMarkSubMenus" {
		return tjs.VoidValue, nil
	}
	if name == "registerExEvent" {
		return tjs.VoidValue, nil
	}
	if name == "setWindowsClickEventHook" {
		return tjs.VoidValue, nil
	}
	if name == "createMenuBarHideMenu" {
		return tjs.VoidValue, nil
	}
	if name == "isDefined" {
		if len(args) != 1 {
			return tjs.VoidValue, fmt.Errorf("isDefined expects one argument")
		}
		return tjs.IntegerValue(0), nil
	}
	if name == "Array.add" {
		if context.Kind() != tjs.KindObject || context.IsNull() {
			return tjs.VoidValue, fmt.Errorf("Array.add requires an object receiver")
		}
		target, ok := vm.Objects.Lookup(context.ObjectRef().Object)
		if !ok {
			return tjs.VoidValue, fmt.Errorf("Array.add target is unavailable")
		}
		length := int64(0)
		if current, found := target.Get("length"); found && current.Kind() == tjs.KindInteger {
			length = current.IntegerValue()
		}
		for _, value := range args {
			if err := target.Set(fmt.Sprintf("%d", length), value); err != nil {
				return tjs.VoidValue, err
			}
			length++
		}
		if err := target.Set("length", tjs.IntegerValue(length)); err != nil {
			return tjs.VoidValue, err
		}
		return tjs.VoidValue, nil
	}
	if name == "Object" {
		object, err := vm.NewDictionary()
		if err != nil {
			return tjs.VoidValue, err
		}
		add := tjs.NewNativeFunction(vm.Objects, "Object.add", func(context tjs.ObjectRef, args []tjs.Value) (tjs.Value, error) {
			target, ok := vm.Objects.Lookup(context.Object)
			if !ok {
				return tjs.VoidValue, fmt.Errorf("Object.add target is unavailable")
			}
			index := int64(0)
			if current, found := target.Get("length"); found && current.Kind() == tjs.KindInteger {
				index = current.IntegerValue()
			}
			for _, value := range args {
				if err := target.Set(fmt.Sprintf("%d", index), value); err != nil {
					return tjs.VoidValue, err
				}
				index++
			}
			if err := target.Set("length", tjs.IntegerValue(index)); err != nil {
				return tjs.VoidValue, err
			}
			return tjs.VoidValue, nil
		})
		set := tjs.NewNativeFunction(vm.Objects, "Object.set", func(context tjs.ObjectRef, args []tjs.Value) (tjs.Value, error) {
			if len(args) < 2 {
				return tjs.VoidValue, fmt.Errorf("Object.set requires key and value")
			}
			target, ok := vm.Objects.Lookup(context.Object)
			if !ok {
				return tjs.VoidValue, fmt.Errorf("Object.set target is unavailable")
			}
			if err := target.Set(args[0].Text(), args[1]); err != nil {
				return tjs.VoidValue, err
			}
			return tjs.VoidValue, nil
		})
		if err := vm.SetMemberWithRuntime(object, "add", add, 0, s, nil); err != nil {
			return tjs.VoidValue, err
		}
		if err := vm.SetMemberWithRuntime(object, "set", set, 0, s, nil); err != nil {
			return tjs.VoidValue, err
		}
		return object, nil
	}
	if name == "list" {
		return vm.NewArray(nil)
	}
	if strings.HasPrefix(name, "Math.") {
		return s.mathNative(strings.TrimPrefix(name, "Math."), args, resultNeeded)
	}
	if strings.HasPrefix(name, "Date.") {
		return s.dateNative(vm, context, strings.TrimPrefix(name, "Date."), args)
	}
	if strings.HasPrefix(name, "Timer.") {
		return s.timerNative(vm, context, strings.TrimPrefix(name, "Timer."), args, budget)
	}
	if strings.HasPrefix(name, "Rect.") {
		return s.rectNative(vm, context, strings.TrimPrefix(name, "Rect."), args)
	}
	if strings.HasPrefix(name, "AsyncTrigger.") {
		return s.asyncTriggerNative(vm, context, strings.TrimPrefix(name, "AsyncTrigger."), args, budget)
	}
	if strings.HasPrefix(name, "Window.") {
		return s.windowNative(vm, context, strings.TrimPrefix(name, "Window."), args, budget)
	}
	if strings.HasPrefix(name, "Pad.") {
		return s.padNative(context, strings.TrimPrefix(name, "Pad."), args)
	}
	if strings.HasPrefix(name, "WIN32Dialog.") {
		return s.win32DialogNative(strings.TrimPrefix(name, "WIN32Dialog."), args)
	}
	if strings.HasPrefix(name, "System.") {
		op := strings.TrimPrefix(name, "System.")
		switch op {
		case "getDisplayMonitors", "getMonitorInfo", "getCursorPos", "setCursorPos":
			return s.systemWindowNative(vm, op, args)
		default:
			return s.systemNative(vm, op, args, budget)
		}
	}
	if strings.HasPrefix(name, "KAGParser.") {
		return s.kagNativeCall(vm, name, context, args, budget)
	}
	if strings.HasPrefix(name, "CSVParser.") {
		return s.csvParserNative(vm, context, strings.TrimPrefix(name, "CSVParser."), args, budget)
	}
	if strings.HasPrefix(name, "MenuItem.") {
		return s.menuNative(vm, context, strings.TrimPrefix(name, "MenuItem."), args, budget)
	}
	if name == "Array.load" {
		if len(args) == 0 {
			return tjs.VoidValue, tjs.ErrBadParamCount
		}
		if context.Kind() != tjs.KindObject || context.IsNull() {
			return tjs.VoidValue, fmt.Errorf("Array.load requires an array receiver")
		}
		if args[0].Kind() != tjs.KindString {
			return tjs.VoidValue, fmt.Errorf("Array.load requires a storage name")
		}
		data, err := s.readStorage(args[0].Text())
		if err != nil {
			return tjs.VoidValue, err
		}
		text, err := assets.Decode(data)
		if err != nil {
			return tjs.VoidValue, err
		}
		lines := splitArrayLoadLines(text)
		if err := vm.ClearArray(context); err != nil {
			return tjs.VoidValue, err
		}
		for _, line := range lines {
			if err := vm.AppendArray(context, tjs.StringValue(line)); err != nil {
				return tjs.VoidValue, err
			}
		}
		return context, nil
	}

	s.ensureInit()
	if strings.HasPrefix(name, "Clipboard.") {
		return s.clipboardNative(strings.TrimPrefix(name, "Clipboard."), args)
	}
	if strings.HasPrefix(name, "Storages.") {
		return s.storagesNative(vm, strings.TrimPrefix(name, "Storages."), args)
	}
	if strings.HasPrefix(name, "Scripts.") {
		return s.scriptsNative(vm, strings.TrimPrefix(name, "Scripts."), args, budget, resultNeeded)
	}
	if strings.HasPrefix(name, "ImageFunction.") {
		return s.imageFunctionNative(vm, strings.TrimPrefix(name, "ImageFunction."), args)
	}
	if s.Natives != nil {
		if fn, ok := s.Natives.Lookup(name); ok && fn != nil {
			return fn(context, args)
		}
	}
	if strings.HasPrefix(name, "Bitmap.") {
		return s.bitmapNative(vm, context, strings.TrimPrefix(name, "Bitmap."), args, budget)
	}
	if strings.HasPrefix(name, "TextRenderBase.") {
		return s.textRenderNative(vm, context, strings.TrimPrefix(name, "TextRenderBase."), args, budget)
	}
	if strings.HasPrefix(name, "Layer.") {
		return s.layerNative(vm, context, strings.TrimPrefix(name, "Layer."), args, budget)
	}
	if strings.HasPrefix(name, "Font.") {
		return s.fontNative(vm, context, strings.TrimPrefix(name, "Font."), args, budget)
	}
	if strings.HasPrefix(name, "VideoOverlay.") {
		return s.VideoNative(vm, context, strings.TrimPrefix(name, "VideoOverlay."), args, budget)
	}
	if strings.HasPrefix(name, "AlphaMovie.") {
		return s.VideoNative(vm, context, strings.TrimPrefix(name, "AlphaMovie."), args, budget)
	}
	if strings.HasPrefix(name, "Motion.ResourceManager.") {
		return s.motionResourceManagerNative(vm, context, strings.TrimPrefix(name, "Motion.ResourceManager."), args)
	}
	if strings.HasPrefix(name, "Motion.Player.") {
		return s.motionPlayerNative(vm, context, strings.TrimPrefix(name, "Motion.Player."), args)
	}
	if strings.HasPrefix(name, "Motion.SeparateLayerAdaptor.") {
		return s.motionSeparateLayerAdaptorNative(context, strings.TrimPrefix(name, "Motion.SeparateLayerAdaptor."), args)
	}
	if strings.HasPrefix(name, "PSBFile.") {
		return s.psbFileNative(vm, context, strings.TrimPrefix(name, "PSBFile."), args)
	}
	if strings.HasPrefix(name, "CDDASoundBuffer.") {
		return s.CDDASoundNative(vm, context, strings.TrimPrefix(name, "CDDASoundBuffer."), args)
	}
	if strings.HasPrefix(name, "MIDISoundBuffer.") {
		return s.MIDISoundNative(vm, context, strings.TrimPrefix(name, "MIDISoundBuffer."), args)
	}
	if strings.HasPrefix(name, "PhaseVocoder.") {
		return s.PhaseVocoderNative(context, strings.TrimPrefix(name, "PhaseVocoder."), args)
	}
	if strings.HasPrefix(name, "WaveSoundBuffer.") {
		return s.SoundNative(vm, context, strings.TrimPrefix(name, "WaveSoundBuffer."), args)
	}
	if strings.HasPrefix(name, "Sound.") {
		operation := strings.TrimPrefix(name, "Sound.")
		switch operation {
		case "setDevice", "getDevice":
			return tjs.VoidValue, UnsupportedDevice("audio device")
		}
		return s.SoundNative(vm, context, operation, args)
	}
	arg := func(i int) (tjs.Value, error) {
		if i >= len(args) {
			return tjs.VoidValue, fmt.Errorf("%s: missing argument %d", name, i)
		}
		return args[i], nil
	}
	switch name {
	case "Debug.logAsError":
		// Use the existing diagnostic trace state for headless error logging.
		if len(args) != 0 {
			return tjs.VoidValue, fmt.Errorf("Debug.logAsError takes no arguments")
		}
		s.mu.Lock()
		s.TraceEnabled = true
		s.mu.Unlock()
		return tjs.VoidValue, nil
	case "Debug.message", "Debug.notice":
		s.Messages = append(s.Messages, joinValues(args))
		return tjs.VoidValue, nil
	case "System.getTickCount", "System.getTickCount64":
		return tjs.IntegerValue(s.systemTickCount()), nil
	case "System.get:screenWidth":
		return tjs.IntegerValue(int64(s.Display.PrimaryDisplay().Bounds[2])), nil
	case "System.get:screenHeight":
		return tjs.IntegerValue(int64(s.Display.PrimaryDisplay().Bounds[3])), nil
	case "System.get:desktopLeft":
		return tjs.IntegerValue(int64(s.Display.DesktopBounds()[0])), nil
	case "System.get:desktopTop":
		return tjs.IntegerValue(int64(s.Display.DesktopBounds()[1])), nil
	case "System.get:desktopWidth":
		return tjs.IntegerValue(int64(s.Display.DesktopBounds()[2])), nil
	case "System.get:desktopHeight":
		return tjs.IntegerValue(int64(s.Display.DesktopBounds()[3])), nil
	case "System.get:personalPath", "System.get:appDataPath":
		return tjs.StringValue(s.SaveDir), nil
	case "System.get:graphicCacheLimit":
		return tjs.IntegerValue(int64(s.Limits.CacheBytes)), nil
	case "System.set:graphicCacheLimit":
		x, e := arg(0)
		if e != nil {
			return tjs.VoidValue, e
		}
		n, e := x.Int()
		if e != nil {
			return tjs.VoidValue, e
		}
		if n < 0 {
			return tjs.VoidValue, fmt.Errorf("negative graphic cache limit")
		}
		s.Limits.CacheBytes = uint64(n)
		return tjs.VoidValue, nil
	case "Debug.get:console":
		s.mu.RLock()
		console := s.console
		s.mu.RUnlock()
		if console.Kind() == tjs.KindObject && !console.IsNull() {
			return console, nil
		}
		console, err := vm.NewDictionary()
		if err != nil {
			return tjs.VoidValue, err
		}
		object, ok := vm.Objects.Lookup(console.ObjectRef().Object)
		if !ok {
			return tjs.VoidValue, fmt.Errorf("console object allocation failed")
		}
		if err := object.Set("visible", tjs.IntegerValue(0)); err != nil {
			return tjs.VoidValue, err
		}
		for _, method := range []string{"log", "info", "warn", "error", "debug", "trace", "dir", "getPlacement", "getRect", "setPos", "restoreMaximize", "bringTo", "bringAfter"} {
			if err := vm.RegisterNativeMethod(console, method, "Console."+method); err != nil {
				return tjs.VoidValue, err
			}
		}
		s.mu.Lock()
		if s.console.Kind() == tjs.KindObject && !s.console.IsNull() {
			console = s.console
		} else {
			s.console = console
			vm.Globals["console"] = console
		}
		s.mu.Unlock()
		return console, nil
	case "Console.log", "Console.info", "Console.warn", "Console.error", "Console.debug", "Console.trace", "Console.dir":
		s.Messages = append(s.Messages, joinValues(args))
		return tjs.VoidValue, nil
	case "Console.getPlacement", "Console.getRect":
		return tjs.VoidValue, nil
	case "Console.setPos", "Console.restoreMaximize", "Console.bringTo", "Console.bringAfter":
		return tjs.VoidValue, nil
	case "Console.@initialize", "Console.@invalidate", "Console.Console", "Console.finalize":
		return tjs.VoidValue, nil
	case "Plugins.link":
		x, e := arg(0)
		if e != nil {
			return tjs.VoidValue, e
		}
		p := x.Text()
		key := pluginLinkKey(p)
		if !isImplementedNativePlugin(key) {
			return tjs.VoidValue, fmt.Errorf("%w: Plugins.link(%q)", ErrUnsupportedNative, p)
		}
		if path.Base(key) == "motionplayer.dll" {
			if e := installMotionPlayerNative(vm); e != nil {
				return tjs.VoidValue, e
			}
		}
		if strings.HasSuffix(key, "alphamovie.dll") {
			if e := installAlphaMovieNative(vm); e != nil {
				return tjs.VoidValue, e
			}
		}
		if strings.HasSuffix(key, "getabout.dll") {
			if e := installGetAboutNative(vm); e != nil {
				return tjs.VoidValue, e
			}
		}
		if strings.HasSuffix(key, "win32dialog.dll") {
			if e := installWin32DialogNative(vm); e != nil {
				return tjs.VoidValue, e
			}
			if _, ok := vm.Globals["WIN32GenericDialogEX"]; !ok {
				if e := s.executePluginScript(vm, "system/win32dialog.tjs", budget); e != nil {
					return tjs.VoidValue, fmt.Errorf("win32dialog.tjs: %w", e)
				}
			}
		}
		if strings.HasSuffix(key, "fftgraph.dll") {
			if e := installFFTGraphNative(vm, s); e != nil {
				return tjs.VoidValue, e
			}
		}
		if strings.HasSuffix(key, "psbfile.dll") {
			if e := installPSBFileNative(vm); e != nil {
				return tjs.VoidValue, e
			}
		}
		if strings.HasSuffix(key, "getsample.dll") {
			if s.samplePluginVMs == nil {
				s.samplePluginVMs = make(map[*tjs.VM]struct{})
			}
			if _, installed := s.samplePluginVMs[vm]; !installed {
				if e := installSampleNative(vm); e != nil {
					return tjs.VoidValue, e
				}
				s.samplePluginVMs[vm] = struct{}{}
			}
		}
		if path.Base(key) == "addfont.dll" {
			installed := false
			for _, plugin := range s.Plugins {
				if pluginLinkKey(plugin) == key {
					installed = true
					break
				}
			}
			if !installed {
				if e := vm.RegisterNative("System.addFont"); e != nil {
					return tjs.VoidValue, e
				}
			}
		}
		if isMenuPlugin(p) {
			if e := s.installMenuNative(vm); e != nil {
				return tjs.VoidValue, e
			}
		}
		if strings.HasSuffix(key, "varfile.dll") {
			s.varStorageVM = vm
		}
		if strings.HasSuffix(key, "csvparser.dll") {
			s.csvParserVM = vm
		}
		for _, v := range s.Plugins {
			if pluginLinkKey(v) == key {
				return tjs.VoidValue, nil
			}
		}
		s.Plugins = append(s.Plugins, p)
		sort.Strings(s.Plugins)
		return tjs.VoidValue, nil
	case "Plugins.getList":
		xs := make([]tjs.Value, len(s.Plugins))
		for i, p := range s.Plugins {
			xs[i] = tjs.StringValue(p)
		}
		return vm.NewArray(xs)
	case "TextStream.read":
		x, e := arg(0)
		if e != nil {
			return tjs.VoidValue, e
		}
		b, e := s.readStorage(x.Text())
		if e != nil {
			return tjs.VoidValue, e
		}
		u, e := assets.DecodeUnits(b)
		if e != nil {
			return tjs.VoidValue, e
		}
		return tjs.StringUnits(u), nil
	case "TextStream.write", "TextStream.writePlugin":
		x, e := arg(0)
		if e != nil {
			return tjs.VoidValue, e
		}
		y, e := arg(1)
		if e != nil {
			return tjs.VoidValue, e
		}
		var offset uint64
		if len(args) > 2 && !args[2].IsVoid() {
			offset, e = scriptReadOffset(args[2].Text())
			if e != nil {
				return tjs.VoidValue, fmt.Errorf("TextStream.write: %w", e)
			}
			if offset > uint64(^uint64(0)>>1) {
				return tjs.VoidValue, fmt.Errorf("TextStream.write: offset exceeds supported range")
			}
		}
		return tjs.VoidValue, s.writeSave(x.Text(), []byte(y.Text()), int64(offset))
	case "Window.@initialize":
		if context.Kind() != tjs.KindObject {
			return tjs.VoidValue, fmt.Errorf("Window requires object context")
		}
		id := context.ObjectRef().Object
		if s.Window(id) == nil {
			return tjs.VoidValue, s.RegisterWindow(id, NewWindow())
		}
		return tjs.VoidValue, nil
	case "Window.@invalidate":
		if context.Kind() == tjs.KindObject {
			s.UnregisterWindow(context.ObjectRef().Object)
		}
		return tjs.VoidValue, nil
	default:
		return tjs.VoidValue, fmt.Errorf("%w: %s", ErrUnsupportedNative, name)
	}
}
func splitArrayLoadLines(text string) []string {
	if text == "" {
		return nil
	}
	lines := make([]string, 0, strings.Count(text, "\n")+strings.Count(text, "\r")+1)
	start := 0
	for i := 0; i < len(text); i++ {
		if text[i] != '\r' && text[i] != '\n' {
			continue
		}
		lines = append(lines, text[start:i])
		if text[i] == '\r' && i+1 < len(text) && text[i+1] == '\n' {
			i++
		}
		start = i + 1
	}
	if start < len(text) {
		lines = append(lines, text[start:])
	}
	return lines
}
func mustDecodeUnits(data []byte) []uint16 {
	u, e := assets.DecodeUnits(data)
	if e != nil {
		return []uint16{}
	}
	return u
}
func joinValues(args []tjs.Value) string {
	out := ""
	for i, a := range args {
		if i > 0 {
			out += " "
		}
		out += a.Text()
	}
	return out
}
