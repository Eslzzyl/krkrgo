package runtime

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/eslzzyl/krkrgo/internal/assets"
	"github.com/eslzzyl/krkrgo/internal/core"
	"github.com/eslzzyl/krkrgo/internal/tjs"
)

// InputKind is the portable host event discriminator.
type InputKind uint8

const (
	InputPointerMove InputKind = iota
	InputPointerDown
	InputPointerUp
	InputClick
	InputDoubleClick
	InputPointerLeave
	InputWheel
	InputKeyDown
	InputKeyUp
	InputText
	InputFocus
)

// InputEvent is intentionally a value type so replay hosts can queue it.
type InputEvent struct {
	Kind               InputKind
	X, Y               int32
	Button, Shift, Key int32
	Delta              int32
	Text               string
	Focused            bool
}

func PointerMove(x, y int32) InputEvent { return InputEvent{Kind: InputPointerMove, X: x, Y: y} }
func PointerDown(x, y, button, shift int32) InputEvent {
	return InputEvent{Kind: InputPointerDown, X: x, Y: y, Button: button, Shift: shift}
}
func PointerUp(x, y, button, shift int32) InputEvent {
	return InputEvent{Kind: InputPointerUp, X: x, Y: y, Button: button, Shift: shift}
}
func KeyDown(key, shift int32) InputEvent {
	return InputEvent{Kind: InputKeyDown, Key: key, Shift: shift}
}
func KeyUp(key, shift int32) InputEvent { return InputEvent{Kind: InputKeyUp, Key: key, Shift: shift} }
func TextInput(text string) InputEvent  { return InputEvent{Kind: InputText, Text: text} }

// Session owns one VM, its host services, and the shared execution budget.
type gpuPlanBitmapDependency struct {
	bitmap   *Bitmap
	revision uint64
}

type Session struct {
	VM                           *tjs.VM
	Services                     *Services
	Budget                       uint64
	AudioOutput                  []float32
	preprocessor                 *tjs.Preprocessor
	startupTiming                bool
	initErr                      error
	closed                       bool
	mu                           sync.Mutex
	deferTransitionCompletions   bool
	pendingTransitionCompletions []tjs.ObjectID
	videoIDsScratch              []tjs.ObjectID
	videoScratch                 map[tjs.ObjectID]*VideoOverlay
	soundIDsScratch              []tjs.ObjectID
	soundScratch                 map[tjs.ObjectID]*WaveSoundBuffer
	gpuCapture                   bool
	gpuPlan                      RenderPlan
	gpuPlanCacheValid            bool
	gpuPlanCacheRoot             *Layer
	gpuPlanCacheRevision         uint64
	gpuPlanCacheWidth            int32
	gpuPlanCacheHeight           int32
	gpuPlanCacheBackground       [4]uint8
	gpuPlanCacheBitmaps          []gpuPlanBitmapDependency
}

func NewSession(services *Services, budget uint64) *Session {
	if services == nil {
		services = DefaultServices()
	}
	services.ensureInit()
	vm := tjs.NewVM()
	preprocessor := newRuntimePreprocessor()
	s := &Session{VM: vm, Services: services, Budget: budget, preprocessor: preprocessor}
	services.bindSessionPreprocessor(vm, preprocessor)
	s.initErr = registerCoreConstants(vm)
	_ = s.registerNatives()
	return s
}

func New(services *Services, budget uint64) *Session { return NewSession(services, budget) }
func Open(project, saveDir string, budget uint64) (*Session, error) {
	return OpenSession(project, saveDir, budget)
}
func OpenSession(project, saveDir string, budget uint64) (*Session, error) {
	storageStarted := time.Now()
	limits := core.DefaultLimits()
	storage, defaultSaveDir, err := openProjectStorage(project, limits)
	if err != nil {
		return nil, err
	}
	if os.Getenv("KRKRGO_STARTUP_TIMING") == "1" {
		log.Printf("krkrgo-startup phase=storage-open elapsed=%s project=%q archives=%d", time.Since(storageStarted), project, len(storage.Archives))
	}
	sessionStarted := time.Now()
	session, err := fromStorage(storage, saveDir, defaultSaveDir, budget)
	if err != nil {
		return nil, err
	}
	if os.Getenv("KRKRGO_STARTUP_TIMING") == "1" {
		log.Printf("krkrgo-startup phase=session-create elapsed=%s", time.Since(sessionStarted))
	}
	return session, nil
}

// openProjectStorage returns project-root storage and its project-level default save path.
// When startup.tjs lives in data/, the data directory is searched after the project
// root so both data resources and root-level executable archives remain available.
func openProjectStorage(project string, limits core.Limits) (*assets.Storage, string, error) {
	storage, err := assets.OpenStorage(project, limits)
	if err != nil {
		return nil, "", fmt.Errorf("open project directory %q: %w", project, err)
	}
	if _, err := storage.Resolve("startup.tjs"); err == nil {
		return storage, filepath.Join(storage.Project, "savedata"), nil
	} else if !errors.Is(err, assets.ErrNotFound) {
		return nil, "", fmt.Errorf("resolve project startup %q: %w", filepath.Join(project, "startup.tjs"), err)
	}

	dataDir := filepath.Join(storage.Project, "data")
	dataStorage, err := assets.OpenStorage(dataDir, limits)
	if err != nil {
		return nil, "", fmt.Errorf("project startup.tjs not found; checked %q and %q: %w",
			filepath.Join(project, "startup.tjs"), filepath.Join(project, "data", "startup.tjs"), err)
	}
	if _, err := dataStorage.Resolve("startup.tjs"); err == nil {
		for _, archive := range dataStorage.Archives {
			if err := storage.Mount(archive.Path, limits); err != nil {
				for _, mounted := range storage.Archives {
					_ = mounted.Close()
				}
				for _, remaining := range dataStorage.Archives {
					_ = remaining.Close()
				}
				return nil, "", fmt.Errorf("mount project data archive %q: %w", archive.Path, err)
			}
		}
		for _, archive := range dataStorage.Archives {
			if err := archive.Close(); err != nil {
				return nil, "", fmt.Errorf("close project data archive %q: %w", archive.Path, err)
			}
		}
		if err := storage.AddSearchPath("data/"); err != nil {
			return nil, "", fmt.Errorf("add project data search path: %w", err)
		}
		return storage, filepath.Join(storage.Project, "savedata"), nil
	} else if !errors.Is(err, assets.ErrNotFound) {
		for _, archive := range dataStorage.Archives {
			_ = archive.Close()
		}
		return nil, "", fmt.Errorf("resolve project startup %q: %w", filepath.Join(project, "data", "startup.tjs"), err)
	}
	for _, archive := range dataStorage.Archives {
		_ = archive.Close()
	}
	return nil, "", fmt.Errorf("project startup.tjs not found; checked %q and %q",
		filepath.Join(project, "startup.tjs"), filepath.Join(project, "data", "startup.tjs"))
}
func FromStorage(storage *assets.Storage, saveDir string, budget uint64) (*Session, error) {
	return fromStorage(storage, saveDir, "", budget)
}

func fromStorage(storage *assets.Storage, saveDir, defaultSaveDir string, budget uint64) (*Session, error) {
	if storage == nil {
		return nil, fmt.Errorf("nil storage")
	}
	if saveDir == "" {
		if defaultSaveDir == "" {
			defaultSaveDir = filepath.Join(storage.Project, "savedata")
		}
		saveDir = defaultSaveDir
	} else {
		var override = saveDir
		resolved, err := core.SaveDirectory(storage.Project, &override)
		if err != nil {
			return nil, err
		}
		saveDir = resolved
	}
	return NewSession(NewServices(storage, saveDir, core.DefaultLimits()), budget), nil
}
func (s *Session) registerNatives() error {
	if s == nil || s.VM == nil || s.Services == nil {
		return fmt.Errorf("nil session")
	}
	if err := RegisterDefaultNatives(s.Services.Natives); err != nil {
		return err
	}
	for _, ns := range []string{"System", "Debug", "Plugins", "Storages", "Scripts"} {
		if _, err := s.VM.RegisterNamespace(ns); err != nil {
			return err
		}
	}
	for _, name := range s.Services.Natives.Names() {
		if err := s.VM.RegisterNative(name); err != nil {
			return err
		}
	}
	window, err := s.VM.RegisterNativeClass("Window")
	if err != nil {
		return err
	}
	windowClass, ok := s.VM.Objects.Lookup(window.ObjectRef().Object)
	if !ok {
		return fmt.Errorf("Window native class is not registered")
	}
	for name, value := range map[string]int64{
		"nchtError": 65534, "nchtTransparent": 65535, "nchtNoWhere": 0,
		"nchtClient": 1, "nchtCaption": 2, "nchtSysMenu": 3, "nchtSize": 4,
		"nchtGrowBox": 4, "nchtMenu": 5, "nchtHScroll": 6, "nchtVScroll": 7,
		"nchtMinButton": 8, "nchtReduce": 8, "nchtMaxButton": 9, "nchtZoom": 9,
		"nchtLeft": 10, "nchtRight": 11, "nchtTop": 12, "nchtTopLeft": 13,
		"nchtTopRight": 14, "nchtBottom": 15, "nchtBottomLeft": 16,
		"nchtBottomRight": 17, "nchtBorder": 18,
	} {
		if err := windowClass.Set(name, tjs.IntegerValue(value)); err != nil {
			return err
		}
	}
	if err := s.VM.RegisterNativeStaticMethod(window, "getNotificationNum", "Window.getNotificationNum"); err != nil {
		return err
	}
	if err := s.VM.RegisterNativeStaticMethod(window, "getNotificationName", "Window.getNotificationName"); err != nil {
		return err
	}
	for _, m := range []string{"resetWindowIcon", "setWindowIcon", "setOverlayBitmap", "resetExSystemMenu", "ncHitTest", "focusMenuByKey", "bringTo", "bringToFront", "sendToBack"} {
		if err := s.VM.RegisterNativeMethod(window, m, "Window."+m); err != nil {
			return err
		}
	}
	for _, m := range []string{"@initialize", "@invalidate", "Window", "finalize", "add", "remove", "close", "showModal", "setPos", "setLayerPos", "setSize", "setInnerSize", "resize", "setZoom", "show", "hide", "minimize", "maximize", "restore", "showRestore", "getWindowRect", "getClientRect", "getNormalRect", "registerExEvent", "setMessageHook", "hideMouseCursor", "disableResize", "disableMove", "enableNCMouseEvent", "onResize", "onMouseMove", "onMouseDown", "onMouseUp", "onClick", "onDoubleClick", "onMouseLeave", "onMouseWheel", "onKeyDown", "onKeyUp", "onKeyPress", "onActivate", "onDeactivate"} {
		if err := s.VM.RegisterNativeMethod(window, m, "Window."+m); err != nil {
			return err
		}
	}
	for _, property := range []string{"caption", "visible", "minimized", "maximized", "left", "top", "layerLeft", "layerTop", "width", "height", "innerWidth", "innerHeight", "borderStyle", "fullScreen", "fitToClient", "zoom", "zoomNumer", "zoomDenom", "mouseCursor", "mouseCursorState", "disableResize", "disableMove", "enableNCMouseEvent", "exSystemMenu", "windowRect", "clientRect", "normalRect", "drawDevice", "focusedLayer", "primaryLayer"} {
		setter := "Window.set:" + property
		switch property {
		case "windowRect", "clientRect", "normalRect", "primaryLayer":
			setter = ""
		}
		if err := s.VM.RegisterNativeProperty(window, property, "Window.get:"+property, setter); err != nil {
			return err
		}
	}
	if err := s.VM.RegisterNativeStaticProperty(window, "mainWindow", "Window.get:mainWindow", ""); err != nil {
		return err
	}
	if err := s.VM.RegisterNativeProperty(window, "menu", "Window.get:menu", ""); err != nil {
		return err
	}
	layer, err := s.VM.RegisterNativeClass("Layer")
	if err != nil {
		return err
	}
	for _, method := range []string{"@initialize", "@invalidate", "Layer", "finalize", "update", "setCursorPos", "setPos", "setSize", "setSizeToImageSize", "setImageSize", "setImagePos", "setClip", "resetClip", "loadImages", "loadProvinceImage", "saveLayerImage", "piledCopy", "pileRect", "fillRect", "colorRect", "copyRect", "operateRect", "stretchCopy", "affineCopy", "assignImages", "doBoxBlur", "adjustGamma", "doGrayScale", "drawText", "bringToFront", "move", "addChild", "removeChild", "getLayerAt", "getMainPixel", "setMainPixel", "getMaskPixel", "setMaskPixel", "getProvincePixel", "setProvincePixel", "setMode", "removeMode", "focus", "beginTransition", "stopTransition", "openMovie", "startMovie", "stopMovie", "isPlayingMovie", "onMouseMove", "onHitTest", "onMouseDown", "onMouseUp", "onClick", "onDoubleClick", "onMouseEnter", "onMouseLeave", "onMouseWheel", "onKeyDown", "onKeyUp", "onKeyPress", "onFocus", "onBlur", "onBeforeFocus", "onSearchPrevFocusable", "onSearchNextFocusable", "onNodeEnabled", "onNodeDisabled", "onTouchDown", "onTouchUp", "onTouchMove", "onTouchScaling", "onTouchRotate", "onMultiTouch", "onTransitionCompleted", "onPaint"} {
		if err := s.VM.RegisterNativeMethod(layer, method, "Layer."+method); err != nil {
			return err
		}
	}
	if err := s.VM.RegisterNativeMethod(layer, "light", "Layer.light"); err != nil {
		return err
	}
	for _, property := range []string{"parent", "children", "order", "absolute", "absoluteOrderMode", "window", "isPrimary", "left", "top", "width", "height", "imageLeft", "imageTop", "imageWidth", "imageHeight", "visible", "nodeVisible", "opacity", "enabled", "eventTransparent", "type", "face", "holdAlpha", "hitType", "hitThreshold", "name", "hasImage", "imageModified", "clipLeft", "clipTop", "clipWidth", "clipHeight", "cursor", "cursorX", "cursorY", "hint", "showParentHint", "neutralColor", "callOnPaint", "font", "focusable", "nodeEnabled", "nodeFocusable", "focused"} {
		setter := "Layer.set:" + property
		switch property {
		case "children", "nodeVisible", "nodeEnabled", "nodeFocusable", "focused", "window", "isPrimary", "neutralColor":
		}
		if err := s.VM.RegisterNativeProperty(layer, property, "Layer.get:"+property, setter); err != nil {
			return err
		}
	}
	bitmap, err := s.VM.RegisterNativeClass("Bitmap")
	if err != nil {
		return err
	}
	for _, method := range []string{"@initialize", "@invalidate", "Bitmap", "finalize", "getPixel", "setPixel", "getMaskPixel", "setMaskPixel", "independ", "setSize", "copyFrom", "save", "load", "loadAsync", "onLoaded"} {
		if err := s.VM.RegisterNativeMethod(bitmap, method, "Bitmap."+method); err != nil {
			return err
		}
	}
	for _, method := range []string{"loadHeader", "getSaveOption"} {
		if err := s.VM.RegisterNativeStaticMethod(bitmap, method, "Bitmap."+method); err != nil {
			return err
		}
	}
	for _, property := range []string{"width", "height", "buffer", "bufferForWrite", "bufferPitch", "loading"} {
		setter := "Bitmap.set:" + property
		if property == "buffer" || property == "bufferForWrite" || property == "bufferPitch" || property == "loading" {
			setter = ""
		}
		if err := s.VM.RegisterNativeProperty(bitmap, property, "Bitmap.get:"+property, setter); err != nil {
			return err
		}
	}
	clipboard, err := s.VM.RegisterNativeClass("Clipboard")
	if err != nil {
		return err
	}
	if err := s.VM.RegisterNativeMethod(clipboard, "@initialize", "Clipboard.@initialize"); err != nil {
		return err
	}
	if err := s.VM.RegisterNativeStaticMethod(clipboard, "hasFormat", "Clipboard.hasFormat"); err != nil {
		return err
	}
	if err := s.VM.RegisterNativeStaticProperty(clipboard, "asText", "Clipboard.get:asText", "Clipboard.set:asText"); err != nil {
		return err
	}
	imageFunction, err := s.VM.RegisterNativeClass("ImageFunction")
	if err != nil {
		return err
	}
	for _, method := range []string{"@initialize", "@invalidate", "ImageFunction", "finalize"} {
		if err := s.VM.RegisterNativeMethod(imageFunction, method, "ImageFunction."+method); err != nil {
			return err
		}
	}
	for _, method := range []string{"operateAffine", "operateRect", "operateStretch", "flipLR", "flipUD", "adjustGamma", "doBoxBlur", "doGrayScale", "fillRect", "colorRect", "drawText", "drawGlyph"} {
		if err := s.VM.RegisterNativeStaticMethod(imageFunction, method, "ImageFunction."+method); err != nil {
			return err
		}
	}
	storages, err := s.VM.RegisterNativeClass("Storages")
	if err != nil {
		return err
	}
	if err := s.VM.RegisterNativeMethod(storages, "@initialize", "Storages.@initialize"); err != nil {
		return err
	}
	for _, method := range []string{"addArchive", "setXP3ArchiveExtractionFilter", "addAutoPath", "removeAutoPath", "getFullPath", "getPlacedPath", "getLocalName", "isExistentStorage", "isExistentDirectory", "getListAt", "fstat", "dirlist", "dirlistEx", "extractStorageExt", "extractStorageName", "extractStoragePath", "chopStorageExt", "clearArchiveCache", "clearStorageCaches", "getTime", "setTime", "getLastModifiedFileTime", "setLastModifiedFileTime", "exportFile", "deleteFile", "truncateFile", "moveFile", "dirtree", "removeDirectory", "createDirectory", "createDirectoryNoNormalize", "changeDirectory", "setFileAttributes", "resetFileAttributes", "getFileAttributes", "selectDirectory", "copyFile", "copyFileNoNormalize", "isExistentStorageNoSearchNoNormalize", "getDisplayName", "searchPath"} {
		if err := s.VM.RegisterNativeStaticMethod(storages, method, "Storages."+method); err != nil {
			return err
		}
	}
	font, err := s.VM.RegisterNativeClass("Font")
	if err != nil {
		return err
	}
	for _, method := range []string{"@initialize", "@invalidate", "Font", "finalize", "getTextWidth", "getTextHeight", "getEscWidthX", "getEscWidthY", "getEscHeightX", "getEscHeightY", "getList", "mapPrerenderedFont", "unmapPrerenderedFont"} {
		if err := s.VM.RegisterNativeMethod(font, method, "Font."+method); err != nil {
			return err
		}
	}
	for _, property := range []string{"face", "height", "bold", "italic", "underline", "strikeout", "angle"} {
		if err := s.VM.RegisterNativeProperty(font, property, "Font.get:"+property, "Font.set:"+property); err != nil {
			return err
		}
	}
	textRender, err := s.VM.RegisterNativeClass("TextRenderBase")
	if err != nil {
		return err
	}
	for _, method := range []string{"@initialize", "@invalidate", "TextRenderBase", "finalize", "calcLineOffset", "calcShowCount", "clear", "done", "getCharacters", "getKeyWait", "newline", "onEval", "render", "resetFont", "resetStyle", "setDefault", "setFont", "setOption", "setRenderSize", "setStyle"} {
		if err := s.VM.RegisterNativeMethod(textRender, method, "TextRenderBase."+method); err != nil {
			return err
		}
	}
	for _, field := range []string{"Align", "BigFontSize", "Bold", "ChColor", "Edge", "EdgeColor", "Face", "FontSize", "Italic", "LineSize", "LineSpacing", "Pitch", "RubyOffset", "RubySize", "Shadow", "ShadowColor", "ShadowDiff", "SmallFontSize", "Valign"} {
		if err := s.VM.RegisterNativeProperty(textRender, "default"+field, "TextRenderBase.get:default"+field, "TextRenderBase.set:default"+field); err != nil {
			return err
		}
	}
	for _, property := range []string{"fontScale", "timeScale", "vertical"} {
		if err := s.VM.RegisterNativeProperty(textRender, property, "TextRenderBase.get:"+property, "TextRenderBase.set:"+property); err != nil {
			return err
		}
	}
	for _, property := range []string{"maxScrollLine", "maxScrollOffset", "renderBottom", "renderCount", "renderDelay", "renderLeft", "renderLines", "renderOver", "renderRight", "renderText", "renderTop"} {
		if err := s.VM.RegisterNativeProperty(textRender, property, "TextRenderBase.get:"+property, ""); err != nil {
			return err
		}
	}
	video, err := s.VM.RegisterNativeClass("VideoOverlay")
	if err != nil {
		return err
	}
	for _, m := range []string{"@initialize", "@invalidate", "VideoOverlay", "finalize", "open", "play", "stop", "close", "setPos", "setSize", "setBounds", "pause", "rewind", "prepare", "setSegmentLoop", "cancelSegmentLoop", "setPeriodEvent", "cancelPeriodEvent", "selectAudioStream", "setMixingLayer", "resetMixingLayer", "onStatusChanged", "onCallbackCommand", "onPeriod", "onFrameUpdate"} {
		if err := s.VM.RegisterNativeMethod(video, m, "VideoOverlay."+m); err != nil {
			return err
		}
	}
	for _, property := range []string{"position", "left", "top", "width", "height", "originalWidth", "originalHeight", "visible", "loop", "frame", "fps", "numberOfFrame", "totalTime", "layer1", "layer2", "mode", "playRate", "segmentLoopStartFrame", "segmentLoopEndFrame", "periodEventFrame", "audioBalance", "audioVolume", "numberOfAudioStream", "enabledAudioStream", "numberOfVideoStream", "enabledVideoStream", "mixingMovieAlpha", "mixingMovieBGColor"} {
		getter := "VideoOverlay.get:" + property
		setter := ""
		switch property {
		case "originalWidth", "originalHeight", "fps", "numberOfFrame", "totalTime", "segmentLoopStartFrame", "segmentLoopEndFrame", "numberOfAudioStream", "numberOfVideoStream":
		default:
			setter = "VideoOverlay.set:" + property
		}
		if err := s.VM.RegisterNativeProperty(video, property, getter, setter); err != nil {
			return err
		}
	}
	sound, err := s.VM.RegisterNativeClass("WaveSoundBuffer")
	if err != nil {
		return err
	}
	for _, m := range []string{"@initialize", "@invalidate", "WaveSoundBuffer", "finalize", "close", "open", "openBytes", "play", "stop", "pause", "rewind", "setPos", "setSamplePosition", "fade", "stopFade", "onStatusChanged", "onLabel", "onFadeCompleted", "setDevice", "getDevice", "setGlobalVolume", "getGlobalVolume", "stopAll"} {
		if err := s.VM.RegisterNativeMethod(sound, m, "WaveSoundBuffer."+m); err != nil {
			return err
		}
	}
	for _, property := range []string{"status", "position", "samplePosition", "totalTime", "frequency", "bits", "channels", "volume", "volume2", "pan", "paused", "looping", "useVisBuffer", "labels"} {
		setter := ""
		switch property {
		case "status", "position", "samplePosition", "totalTime", "bits", "channels", "labels":
		default:
			setter = "WaveSoundBuffer.set:" + property
		}
		if err := s.VM.RegisterNativeProperty(sound, property, "WaveSoundBuffer.get:"+property, setter); err != nil {
			return err
		}
	}
	for _, property := range []string{"globalVolume", "globalFocusMode"} {
		if err := s.VM.RegisterNativeStaticProperty(sound, property, "WaveSoundBuffer.get:"+property, "WaveSoundBuffer.set:"+property); err != nil {
			return err
		}
	}
	vocoder, err := s.VM.RegisterNativeClass("PhaseVocoder")
	if err != nil {
		return err
	}
	delete(s.VM.Globals, "PhaseVocoder")
	for _, method := range []string{"@initialize", "@invalidate", "PhaseVocoder", "finalize"} {
		if err := s.VM.RegisterNativeMethod(vocoder, method, "PhaseVocoder."+method); err != nil {
			return err
		}
	}
	for _, property := range []string{"window", "overlap", "pitch", "time", "interface"} {
		setter := "PhaseVocoder.set:" + property
		if property == "interface" {
			setter = ""
		}
		if err := s.VM.RegisterNativeProperty(vocoder, property, "PhaseVocoder.get:"+property, setter); err != nil {
			return err
		}
	}
	if err := s.VM.RegisterNativeStaticValue(sound, "PhaseVocoder", vocoder); err != nil {
		return err
	}
	midi, err := s.VM.RegisterNativeClass("MIDISoundBuffer")
	if err != nil {
		return err
	}
	for _, method := range []string{"@initialize", "@invalidate", "MIDISoundBuffer", "finalize", "open", "play", "stop", "fade", "stopFade", "onStatusChanged", "onFadeCompleted"} {
		if err := s.VM.RegisterNativeMethod(midi, method, "MIDISoundBuffer."+method); err != nil {
			return err
		}
	}
	for _, property := range []string{"position", "paused", "totalTime", "looping", "volume", "volume2", "status"} {
		getter := "MIDISoundBuffer.get:" + property
		setter := "MIDISoundBuffer.set:" + property
		if property == "status" {
			setter = ""
		}
		if err := s.VM.RegisterNativeProperty(midi, property, getter, setter); err != nil {
			return err
		}
	}
	cdda, err := s.VM.RegisterNativeClass("CDDASoundBuffer")
	if err != nil {
		return err
	}
	for _, method := range []string{"@initialize", "@invalidate", "CDDASoundBuffer", "finalize", "open", "play", "stop", "fade", "stopFade", "onStatusChanged", "onFadeCompleted"} {
		if err := s.VM.RegisterNativeMethod(cdda, method, "CDDASoundBuffer."+method); err != nil {
			return err
		}
	}
	for _, property := range []string{"position", "paused", "totalTime", "looping", "volume", "volume2", "status"} {
		getter := "CDDASoundBuffer.get:" + property
		setter := "CDDASoundBuffer.set:" + property
		if property == "status" {
			setter = ""
		}
		if err := s.VM.RegisterNativeProperty(cdda, property, getter, setter); err != nil {
			return err
		}
	}
	date, err := s.VM.RegisterNativeClass("Date")
	if err != nil {
		return err
	}
	for _, method := range []string{"@initialize", "@invalidate", "finalize", "getDate", "getDay", "getHours", "getMinutes", "getMonth", "getSeconds", "getTime", "getYear", "parse", "setDate", "setHours", "setMinutes", "setMonth", "setSeconds", "setTime", "setYear"} {
		if err := s.VM.RegisterNativeMethod(date, method, "Date."+method); err != nil {
			return err
		}
	}
	if err := s.VM.RegisterNativeStaticMethod(date, "getTimezoneOffset", "Date.getTimezoneOffset"); err != nil {
		return err
	}
	timer, err := s.VM.RegisterNativeClass("Timer")
	if err != nil {
		return err
	}
	for _, method := range []string{"@initialize", "@invalidate", "Timer", "finalize", "onTimer"} {
		if err := s.VM.RegisterNativeMethod(timer, method, "Timer."+method); err != nil {
			return err
		}
	}
	for _, property := range []string{"interval", "enabled", "capacity", "mode"} {
		if err := s.VM.RegisterNativeProperty(timer, property, "Timer.get:"+property, "Timer.set:"+property); err != nil {
			return err
		}
	}
	rect, err := s.VM.RegisterNativeClass("Rect")
	if err != nil {
		return err
	}
	for _, method := range []string{"@initialize", "@invalidate", "Rect", "finalize", "isEmpty", "setSize", "setOffset", "addOffset", "clear", "set", "clip", "union", "intersects", "included", "includedPos", "equal"} {
		if err := s.VM.RegisterNativeMethod(rect, method, "Rect."+method); err != nil {
			return err
		}
	}
	for _, property := range []string{"width", "height", "left", "top", "right", "bottom", "nativeArray"} {
		setter := "Rect.set:" + property
		if property == "nativeArray" {
			setter = ""
		}
		if err := s.VM.RegisterNativeProperty(rect, property, "Rect.get:"+property, setter); err != nil {
			return err
		}
	}
	asyncTrigger, err := s.VM.RegisterNativeClass("AsyncTrigger")
	if err != nil {
		return err
	}
	for _, method := range []string{"@initialize", "@invalidate", "AsyncTrigger", "finalize", "trigger", "cancel", "onFire"} {
		if err := s.VM.RegisterNativeMethod(asyncTrigger, method, "AsyncTrigger."+method); err != nil {
			return err
		}
	}
	for _, property := range []string{"cached", "mode"} {
		if err := s.VM.RegisterNativeProperty(asyncTrigger, property, "AsyncTrigger.get:"+property, "AsyncTrigger.set:"+property); err != nil {
			return err
		}
	}
	kagParserClass, err := s.VM.RegisterNativeClass("KAGParser")
	if err != nil {
		return err
	}
	for _, method := range []string{"@initialize", "@invalidate", "KAGParser", "finalize", "interrupt", "resetInterrupt", "loadScenario", "goToLabel", "callLabel", "return", "clear", "assign", "clearCallStack", "popMacroArgs", "getNextTag", "step", "run", "store", "restore"} {
		if err := s.VM.RegisterNativeMethod(kagParserClass, method, "KAGParser."+method); err != nil {
			return err
		}
	}
	for _, property := range []string{"curLabel", "curLine", "curLineStr", "curPos", "curStorage", "callStackDepth", "mp", "macroParams", "macros", "paramMacros", "ignoreCR", "processSpecialTags", "multiLineTagEnabled", "debugLevel"} {
		setter := ""
		switch property {
		case "ignoreCR", "processSpecialTags", "multiLineTagEnabled", "debugLevel", "macros":
			setter = "KAGParser.set:" + property
		}
		if err := s.VM.RegisterNativeProperty(kagParserClass, property, "KAGParser.get:"+property, setter); err != nil {
			return err
		}
	}
	csvParser, err := s.VM.RegisterNativeClass("CSVParser")
	if err != nil {
		return err
	}
	for _, method := range []string{"@initialize", "@invalidate", "CSVParser", "finalize", "init", "initStorage", "getNextLine", "parse", "parseStorage"} {
		if err := s.VM.RegisterNativeMethod(csvParser, method, "CSVParser."+method); err != nil {
			return err
		}
	}
	if err := s.VM.RegisterNativeProperty(csvParser, "currentLineNumber", "CSVParser.get:currentLineNumber", ""); err != nil {
		return err
	}
	system, err := s.VM.RegisterNamespace("System")
	if err != nil {
		return err
	}
	for _, property := range []string{"screenWidth", "screenHeight", "desktopLeft", "desktopTop", "desktopWidth", "desktopHeight", "exePath", "exeName", "dataPath", "personalPath", "appDataPath", "title", "graphicCacheLimit"} {
		setter := ""
		if property == "graphicCacheLimit" || property == "title" {
			setter = "System.set:" + property
		}
		if err := s.VM.RegisterNativeProperty(system, property, "System.get:"+property, setter); err != nil {
			return err
		}
	}
	for _, property := range []string{"versionString", "versionInformation", "eventDisabled", "platformName", "osName", "exitOnWindowClose", "drawThreadNum", "processorNum", "exeBits", "osBits", "exitOnNoWindowStartup"} {
		setter := ""
		switch property {
		case "eventDisabled", "exitOnWindowClose", "drawThreadNum", "exitOnNoWindowStartup":
			setter = "System.set:" + property
		}
		if err := s.VM.RegisterNativeProperty(system, property, "System.get:"+property, setter); err != nil {
			return err
		}
	}
	for _, method := range []string{"terminate", "exit", "addContinuousHandler", "removeContinuousHandler", "toActualColor", "clearGraphicCache", "touchImages", "createUUID", "createAppLock", "getArgument", "setArgument", "inform", "getKeyState", "assignMessage", "doCompact", "getAboutString", "readRegValue"} {
		if err := s.VM.RegisterNativeMethod(system, method, "System."+method); err != nil {
			return err
		}
	}
	debug, err := s.VM.RegisterNamespace("Debug")
	if err != nil {
		return err
	}
	if err := s.VM.RegisterNativeProperty(debug, "console", "Debug.get:console", ""); err != nil {
		return err
	}
	if err := s.registerScriptsNative(); err != nil {
		return err
	}
	if err := s.Services.installMenuNative(s.VM); err != nil {
		return err
	}
	return installPadNative(s.VM)
}
func (s *Session) RegisterNative(name string, fn NativeFunc) error {
	if err := s.checkOpen(); err != nil {
		return err
	}
	if err := s.Services.Natives.RegisterFunc(name, fn); err != nil {
		return err
	}
	return s.VM.RegisterNative(name)
}
func (s *Session) checkOpen() error {
	if s == nil || s.closed {
		return ErrSessionClosed
	}
	if s.VM == nil || s.Services == nil {
		return fmt.Errorf("invalid session")
	}
	if s.initErr != nil {
		return s.initErr
	}
	if s.preprocessor == nil {
		s.preprocessor = newRuntimePreprocessor()
		s.Services.bindSessionPreprocessor(s.VM, s.preprocessor)
	}
	return nil
}
func (s *Session) consume(n uint64) error {
	if n > s.Budget {
		return ErrBudgetExceeded
	}
	s.Budget -= n
	return nil
}

func (s *Session) logStartupElapsed(phase string, started time.Time) {
	if s == nil || !s.startupTiming {
		return
	}
	log.Printf("krkrgo-startup phase=%s elapsed=%s", phase, time.Since(started))
}

func (s *Session) executeSource(name, source string, expression bool) (tjs.Value, error) {
	if err := s.checkOpen(); err != nil {
		return tjs.VoidValue, err
	}
	compileStarted := time.Now()
	p, err := tjs.CompileWithPreprocessor(name, source, expression, s.preprocessor)
	if s.startupTiming {
		log.Printf("krkrgo-startup phase=script-compile name=%q elapsed=%s bytes=%d error=%v", name, time.Since(compileStarted), len(source), err)
	}
	if err != nil {
		return tjs.VoidValue, err
	}
	executeStarted := time.Now()
	value, err := s.VM.Execute(&p, s.Services, &s.Budget)
	if s.startupTiming {
		log.Printf("krkrgo-startup phase=script-execute name=%q elapsed=%s error=%v", name, time.Since(executeStarted), err)
	}
	return value, err
}
func (s *Session) ExecuteStorage(name string) (tjs.Value, error) {
	return s.executeStorage(name, false)
}

func (s *Session) executeStorage(name string, useConfiguredEncoding bool) (tjs.Value, error) {
	if err := s.checkOpen(); err != nil {
		return tjs.VoidValue, err
	}
	readStarted := time.Now()
	b, err := s.Services.readStorage(name)
	if s.startupTiming {
		log.Printf("krkrgo-startup phase=script-read name=%q elapsed=%s bytes=%d error=%v", name, time.Since(readStarted), len(b), err)
	}
	if err != nil {
		return tjs.VoidValue, err
	}
	if len(b) >= 4 && string(b[:4]) == "TJS2" {
		executeStarted := time.Now()
		value, err := executeTJS2Bytecode(s.VM, b, tjs.ObjectValue(tjs.NewObjectRef(0, 0)), s.Services, &s.Budget, name)
		if s.startupTiming {
			log.Printf("krkrgo-startup phase=script-bytecode-execute name=%q elapsed=%s bytes=%d error=%v", name, time.Since(executeStarted), len(b), err)
		}
		return value, err
	}
	decodeStarted := time.Now()
	var src string
	if useConfiguredEncoding {
		src, err = s.Services.decodeScriptStorage(b)
	} else {
		src, err = decodeScriptStorageDefault(b)
	}
	if s.startupTiming {
		log.Printf("krkrgo-startup phase=script-decode name=%q elapsed=%s bytes=%d decoded_bytes=%d error=%v", name, time.Since(decodeStarted), len(b), len(src), err)
	}
	if err != nil {
		return tjs.VoidValue, err
	}
	return s.executeSource(name, src, false)
}
func (s *Session) loadXP3ArchiveExtractionFilter() error {
	if s.Services == nil || s.Services.Storage == nil {
		return nil
	}
	storage := s.Services.Storage
	path := filepath.Join(storage.Project, "xp3filter.tjs")
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("stat XP3 extraction filter %q: %w", path, err)
	}
	readStarted := time.Now()
	sourceBytes, err := storage.Read("xp3filter.tjs")
	if s.startupTiming {
		log.Printf("krkrgo-startup phase=script-read name=%q elapsed=%s bytes=%d error=%v", "xp3filter.tjs", time.Since(readStarted), len(sourceBytes), err)
	}
	if err != nil {
		return fmt.Errorf("read XP3 extraction filter: %w", err)
	}
	decodeStarted := time.Now()
	source, err := decodeScriptStorageDefault(sourceBytes)
	if s.startupTiming {
		log.Printf("krkrgo-startup phase=script-decode name=%q elapsed=%s bytes=%d decoded_bytes=%d error=%v", "xp3filter.tjs", time.Since(decodeStarted), len(sourceBytes), len(source), err)
	}
	if err != nil {
		return fmt.Errorf("decode XP3 extraction filter: %w", err)
	}
	return nil
}

func (s *Session) Startup() (tjs.Value, error) {
	timing := os.Getenv("KRKRGO_STARTUP_TIMING") == "1"
	previousTiming := s.startupTiming
	s.startupTiming = timing
	defer func() {
		s.startupTiming = previousTiming
	}()
	filterStarted := time.Now()
	if err := s.loadXP3ArchiveExtractionFilter(); err != nil {
		return tjs.VoidValue, err
	}
	s.logStartupElapsed("startup-filter", filterStarted)
	nativeStarted := time.Now()
	if _, installed := s.VM.Globals["exists"]; !installed {
		if err := s.VM.RegisterNative("exists"); err != nil {
			return tjs.VoidValue, err
		}
	}
	if _, installed := s.VM.Globals["list"]; !installed {
		if err := s.VM.RegisterNative("list"); err != nil {
			return tjs.VoidValue, err
		}
	}
	if _, installed := s.VM.Globals["goBackHistory"]; !installed {
		if err := s.VM.RegisterNative("goBackHistory"); err != nil {
			return tjs.VoidValue, err
		}
	}
	if _, installed := s.VM.Globals["goBackTitle"]; !installed {
		if err := s.VM.RegisterNative("goBackTitle"); err != nil {
			return tjs.VoidValue, err
		}
	}
	if _, installed := s.VM.Globals["endRecollection"]; !installed {
		if err := s.VM.RegisterNative("endRecollection"); err != nil {
			return tjs.VoidValue, err
		}
	}
	if _, installed := s.VM.Globals["loadEmergency"]; !installed {
		if err := s.VM.RegisterNative("loadEmergency"); err != nil {
			return tjs.VoidValue, err
		}
	}
	if _, installed := s.VM.Globals["cancel"]; !installed {
		if err := s.VM.RegisterNative("cancel"); err != nil {
			return tjs.VoidValue, err
		}
	}
	if _, installed := s.VM.Globals["isDefined"]; !installed {
		if err := s.VM.RegisterNative("isDefined"); err != nil {
			return tjs.VoidValue, err
		}
	}
	if _, installed := s.VM.Globals["set"]; !installed {
		if err := s.VM.RegisterNative("set"); err != nil {
			return tjs.VoidValue, err
		}
	}
	if _, installed := s.VM.Globals["get"]; !installed {
		if err := s.VM.RegisterNative("get"); err != nil {
			return tjs.VoidValue, err
		}
	}
	if _, installed := s.VM.Globals["load"]; !installed {
		if err := s.VM.RegisterNative("load"); err != nil {
			return tjs.VoidValue, err
		}
	}
	if _, installed := s.VM.Globals["getValue"]; !installed {
		if err := s.VM.RegisterNative("getValue"); err != nil {
			return tjs.VoidValue, err
		}
	}
	if _, installed := s.VM.Globals["showHistoryByKey"]; !installed {
		if err := s.VM.RegisterNative("showHistoryByKey"); err != nil {
			return tjs.VoidValue, err
		}
	}
	if _, installed := s.VM.Globals["callHook"]; !installed {
		if err := s.VM.RegisterNative("callHook"); err != nil {
			return tjs.VoidValue, err
		}
	}
	if _, installed := s.VM.Globals["checkSave"]; !installed {
		if err := s.VM.RegisterNative("checkSave"); err != nil {
			return tjs.VoidValue, err
		}
	}
	if _, installed := s.VM.Globals["updateQuickSaveDataExists"]; !installed {
		if err := s.VM.RegisterNative("updateQuickSaveDataExists"); err != nil {
			return tjs.VoidValue, err
		}
	}
	if _, installed := s.VM.Globals["entryKagHooks"]; !installed {
		if err := s.VM.RegisterNative("entryKagHooks"); err != nil {
			return tjs.VoidValue, err
		}
	}
	if _, installed := s.VM.Globals["onResetWindowSizeMenuItemClick"]; !installed {
		if err := s.VM.RegisterNative("onResetWindowSizeMenuItemClick"); err != nil {
			return tjs.VoidValue, err
		}
	}
	if _, installed := s.VM.Globals["onDisableWinwodResizeMenuItemClick"]; !installed {
		if err := s.VM.RegisterNative("onDisableWinwodResizeMenuItemClick"); err != nil {
			return tjs.VoidValue, err
		}
	}
	if err := s.Services.preloadWin32Dialog(s.VM, &s.Budget); err != nil {
		return tjs.VoidValue, err
	}
	if _, installed := s.VM.Globals["getWindowResMenuItem"]; !installed {
		if err := s.VM.RegisterNative("getWindowResMenuItem"); err != nil {
			return tjs.VoidValue, err
		}
	}
	if _, installed := s.VM.Globals["add"]; !installed {
		if err := s.VM.RegisterNative("add"); err != nil {
			return tjs.VoidValue, err
		}
	}
	if _, installed := s.VM.Globals["tagApply"]; !installed {
		if err := s.VM.RegisterNative("tagApply"); err != nil {
			return tjs.VoidValue, err
		}
	}
	if _, installed := s.VM.Globals["setup"]; !installed {
		if err := s.VM.RegisterNative("setup"); err != nil {
			return tjs.VoidValue, err
		}
	}
	if _, installed := s.VM.Globals["createBookMarkSubMenus"]; !installed {
		if err := s.VM.RegisterNative("createBookMarkSubMenus"); err != nil {
			return tjs.VoidValue, err
		}
	}
	if _, installed := s.VM.Globals["registerExEvent"]; !installed {
		if err := s.VM.RegisterNative("registerExEvent"); err != nil {
			return tjs.VoidValue, err
		}
	}
	if _, installed := s.VM.Globals["setWindowsClickEventHook"]; !installed {
		if err := s.VM.RegisterNative("setWindowsClickEventHook"); err != nil {
			return tjs.VoidValue, err
		}
	}
	if _, installed := s.VM.Globals["createMenuBarHideMenu"]; !installed {
		if err := s.VM.RegisterNative("createMenuBarHideMenu"); err != nil {
			return tjs.VoidValue, err
		}
	}
	s.logStartupElapsed("startup-native-setup", nativeStarted)
	return s.executeStorage("startup.tjs", true)
}
func (s *Session) Evaluate(source string) (tjs.Value, error) {
	if err := s.checkOpen(); err != nil {
		return tjs.VoidValue, err
	}
	program, expressionErr := tjs.CompileWithPreprocessor("<tool>", source, true, s.preprocessor)
	if expressionErr == nil {
		return s.VM.Execute(&program, s.Services, &s.Budget)
	}
	// Evaluate is commonly used by embedders for either a single expression
	// (whose value is returned) or a statement sequence. Retry in script mode
	// only when expression parsing fails; script-mode diagnostics remain
	// authoritative when the input is not a valid expression or script.
	if split := strings.LastIndexByte(source, ';'); split >= 0 {
		prefix := source[:split+1]
		suffix := strings.TrimSpace(source[split+1:])
		if suffix != "" {
			prefixProgram, prefixErr := tjs.CompileWithPreprocessor("<tool>", prefix, false, s.preprocessor)
			if prefixErr == nil {
				resultProgram, resultErr := tjs.CompileWithPreprocessor("<tool>", suffix, true, s.preprocessor)
				if resultErr == nil {
					if _, err := s.VM.Execute(&prefixProgram, s.Services, &s.Budget); err != nil {
						return tjs.VoidValue, err
					}
					return s.VM.Execute(&resultProgram, s.Services, &s.Budget)
				}
			}
		}
	}
	program, err := tjs.CompileWithPreprocessor("<tool>", source, false, s.preprocessor)
	if err != nil {
		return tjs.VoidValue, err
	}
	return s.VM.Execute(&program, s.Services, &s.Budget)
}
func (s *Session) AdvanceClock(timeMS uint64) error {
	if err := s.checkOpen(); err != nil {
		return err
	}
	if timeMS < s.Services.TimeMS {
		return ErrClockBackwards
	}
	profile := os.Getenv("KRKRGO_RENDER_STATS") != ""
	var started, stageStarted time.Time
	var timersTime, schedulerTime, continuousTime, videosTime, soundsTime, transitionsTime time.Duration
	var schedulerCallbacks, videoCount, soundCount int
	if profile {
		started = time.Now()
		stageStarted = started
	}
	if s.Services.Timers != nil {
		if err := s.Services.Timers.Advance(timeMS); err != nil {
			return err
		}
	}
	if profile {
		timersTime = time.Since(stageStarted)
		stageStarted = time.Now()
	}
	if s.Services.Scheduler != nil {
		callbacks, err := s.Services.Scheduler.Advance(timeMS)
		if err != nil {
			return err
		}
		schedulerCallbacks = len(callbacks)
		for _, callback := range callbacks {
			if callback.Name != "" {
				if _, err := s.Services.Call(s.VM, callback.Name, nil, &s.Budget); err != nil {
					return err
				}
			}
		}
	}
	if profile {
		schedulerTime = time.Since(stageStarted)
		stageStarted = time.Now()
	}
	if err := s.Services.invokeContinuousHandlers(s.VM, timeMS, &s.Budget); err != nil {
		return err
	}
	if profile {
		continuousTime = time.Since(stageStarted)
		stageStarted = time.Now()
	}
	videoIDs := s.videoIDsScratch[:0]
	for id := range s.Services.Videos {
		videoIDs = append(videoIDs, id)
	}
	s.videoIDsScratch = videoIDs
	if len(videoIDs) > 1 {
		sort.Slice(videoIDs, func(i, j int) bool { return videoIDs[i] < videoIDs[j] })
	}
	videoCount = len(videoIDs)
	for _, id := range videoIDs {
		video := s.Services.Videos[id]
		if err := video.Advance(timeMS); err != nil {
			return err
		}
		if err := s.Services.dispatchVideoEvents(s.VM, id, &s.Budget); err != nil {
			return err
		}
	}
	if profile {
		videosTime = time.Since(stageStarted)
		stageStarted = time.Now()
	}
	s.Services.mu.RLock()
	soundIDs := s.soundIDsScratch[:0]
	if s.soundScratch == nil {
		s.soundScratch = make(map[tjs.ObjectID]*WaveSoundBuffer, len(s.Services.Sounds))
	} else {
		clear(s.soundScratch)
	}
	for id, sound := range s.Services.Sounds {
		soundIDs = append(soundIDs, id)
		s.soundScratch[id] = sound
	}
	s.Services.mu.RUnlock()
	s.soundIDsScratch = soundIDs
	if len(soundIDs) > 1 {
		sort.Slice(soundIDs, func(i, j int) bool { return soundIDs[i] < soundIDs[j] })
	}
	soundCount = len(soundIDs)
	for _, id := range soundIDs {
		sound := s.soundScratch[id]
		if sound == nil {
			continue
		}
		sound.Advance(timeMS)
		if err := s.Services.queueSoundEvents(id, sound); err != nil {
			return err
		}
	}
	if profile {
		soundsTime = time.Since(stageStarted)
		stageStarted = time.Now()
	}
	pending, err := s.Services.advanceLayerTransitionsDeferred(s.VM, timeMS, false, &s.Budget, s.deferTransitionCompletions)
	if err != nil {
		return err
	}
	if s.deferTransitionCompletions {
		s.pendingTransitionCompletions = append(s.pendingTransitionCompletions, pending...)
	}
	if profile {
		transitionsTime = time.Since(stageStarted)
		elapsed := time.Since(started)
		if elapsed >= 5*time.Millisecond {
			log.Printf("krkrgo advance_clock stages: elapsed=%s timers=%s scheduler=%s scheduler_callbacks=%d continuous_handlers=%s videos=%s video_count=%d sounds=%s sound_count=%d layer_transitions=%s",
				elapsed, timersTime, schedulerTime, schedulerCallbacks, continuousTime, videosTime, videoCount, soundsTime, soundCount, transitionsTime)
		}
	}
	s.Services.TimeMS = timeMS
	s.Services.clockPolls = 0
	return nil
}
func (s *Session) queueSoundEventsAfterMix() error {
	s.Services.mu.RLock()
	ids := make([]tjs.ObjectID, 0, len(s.Services.Sounds))
	sounds := make(map[tjs.ObjectID]*WaveSoundBuffer, len(s.Services.Sounds))
	for id, sound := range s.Services.Sounds {
		ids = append(ids, id)
		sounds[id] = sound
	}
	s.Services.mu.RUnlock()
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		if sound := sounds[id]; sound != nil {
			if err := s.Services.queueSoundEvents(id, sound); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Session) Tick(timeMS uint64) error { return s.TickWithFrameSink(timeMS, nil) }
func (s *Session) TickWithFrameSink(timeMS uint64, sink func(uint64, FrameSnapshot)) error {
	if err := s.checkOpen(); err != nil {
		return err
	}
	s.Services.ensureInit()
	if timeMS < s.Services.TimeMS {
		return ErrClockBackwards
	}
	delta := timeMS - s.Services.TimeMS
	if delta > uint64(MaxAudioBlockFrames/48) {
		return fmt.Errorf("audio tick exceeds frame limit")
	}
	snapshot := s.tickSnapshot()
	restore := func() { snapshot.restore(s) }
	if err := s.consume(1); err != nil {
		return err
	}
	if err := s.AdvanceClock(timeMS); err != nil {
		restore()
		return err
	}
	out, err := s.Services.AudioMixer.MixMilliseconds(delta)
	if err != nil {
		restore()
		return err
	}
	s.AudioOutput = out
	if err := s.queueSoundEventsAfterMix(); err != nil {
		restore()
		return err
	}
	if err := s.DispatchEvents(); err != nil {
		restore()
		return err
	}
	if sink != nil {
		if err := s.Services.advanceLayerTransitions(s.VM, s.Services.TimeMS, true, &s.Budget); err != nil {
			restore()
			return err
		}
		frame, err := s.captureFrame()
		if err != nil {
			restore()
			return err
		}
		if !frame.Empty() {
			sink(s.Services.MainWindow, frame)
		}
	}
	return nil
}

// TickForPresentation advances the session and returns the terminal transition
// frame before exchanging completed transition trees or invoking callbacks.
// The returned frame uses the borrowed presentation buffer contract.
func (s *Session) TickForPresentation(timeMS uint64) (FrameSnapshot, error) {
	return s.tickForPresentation(timeMS, nil)
}

// TickForPresentationWithAudioDelta keeps logical script time deterministic
// while allowing a real-time host to request the audio duration elapsed since
// its previous update.
func (s *Session) TickForPresentationWithAudioDelta(timeMS, audioMilliseconds uint64) (FrameSnapshot, error) {
	return s.tickForPresentation(timeMS, &audioMilliseconds)
}

func (s *Session) tickForPresentation(timeMS uint64, audioMilliseconds *uint64) (FrameSnapshot, error) {
	if err := s.checkOpen(); err != nil {
		return FrameSnapshot{}, err
	}
	s.Services.ensureInit()
	if timeMS < s.Services.TimeMS {
		return FrameSnapshot{}, ErrClockBackwards
	}
	delta := timeMS - s.Services.TimeMS
	mixDelta := delta
	if audioMilliseconds != nil {
		mixDelta = *audioMilliseconds
	}
	if delta > uint64(MaxAudioBlockFrames/48) || mixDelta > uint64(MaxAudioBlockFrames/48) {
		return FrameSnapshot{}, fmt.Errorf("audio tick exceeds frame limit")
	}
	profile := os.Getenv("KRKRGO_RENDER_STATS") != "" || StallTraceEnabled()
	tickStarted := time.Time{}
	var mixerTime, advanceTime, dispatchTime, transitionTime, captureTime, completionTime time.Duration
	if profile {
		tickStarted = time.Now()
	}
	snapshot := s.tickSnapshot()
	restore := func() {
		s.deferTransitionCompletions = false
		s.pendingTransitionCompletions = nil
		snapshot.restore(s)
	}
	s.deferTransitionCompletions = true
	s.pendingTransitionCompletions = nil
	if err := s.consume(1); err != nil {
		restore()
		return FrameSnapshot{}, err
	}
	stageStarted := time.Time{}
	if profile {
		stageStarted = time.Now()
	}
	if err := s.AdvanceClock(timeMS); err != nil {
		if profile {
			advanceTime = time.Since(stageStarted)
		}
		restore()
		return FrameSnapshot{}, err
	}
	if profile {
		advanceTime = time.Since(stageStarted)
		stageStarted = time.Now()
	}
	out, err := s.Services.AudioMixer.MixMilliseconds(mixDelta)
	if profile {
		mixerTime = time.Since(stageStarted)
	}
	if err != nil {
		restore()
		return FrameSnapshot{}, err
	}
	s.AudioOutput = out
	if err := s.queueSoundEventsAfterMix(); err != nil {
		restore()
		return FrameSnapshot{}, err
	}
	if err := s.DispatchEvents(); err != nil {
		if profile {
			dispatchTime = time.Since(stageStarted)
		}
		restore()
		return FrameSnapshot{}, err
	}
	if profile {
		dispatchTime = time.Since(stageStarted)
		stageStarted = time.Now()
	}
	pending, err := s.Services.advanceLayerTransitionsDeferred(s.VM, s.Services.TimeMS, true, &s.Budget, true)
	if profile {
		transitionTime = time.Since(stageStarted)
	}
	if err != nil {
		restore()
		return FrameSnapshot{}, err
	}
	s.pendingTransitionCompletions = append(s.pendingTransitionCompletions, pending...)
	if profile {
		stageStarted = time.Now()
	}
	frame, err := s.captureFrameFor(true, false)
	if profile {
		captureTime = time.Since(stageStarted)
	}
	if err != nil {
		restore()
		return FrameSnapshot{}, err
	}
	if profile {
		stageStarted = time.Now()
	}
	if err := s.Services.completeLayerTransitions(s.VM, s.pendingTransitionCompletions, &s.Budget); err != nil {
		if profile {
			completionTime = time.Since(stageStarted)
		}
		restore()
		return FrameSnapshot{}, err
	}
	if profile {
		completionTime = time.Since(stageStarted)
		elapsed := time.Since(tickStarted)
		threshold := 20 * time.Millisecond
		if StallTraceEnabled() {
			threshold = StallTraceThreshold()
		}
		if elapsed >= threshold {
			log.Printf("krkrgo runtime tick stages: elapsed=%s audio_mix=%s advance_clock=%s dispatch_events=%s layer_transitions=%s capture_frame=%s transition_completion=%s",
				elapsed, mixerTime, advanceTime, dispatchTime, transitionTime, captureTime, completionTime)
		}
	}
	s.deferTransitionCompletions = false
	s.pendingTransitionCompletions = nil
	return frame, nil
}

// TickForGPUPresentation advances the session using the same deterministic
// tick ordering as CPU presentation and returns a borrowed render plan.
func (s *Session) TickForGPUPresentation(timeMS uint64) (RenderPlan, error) {
	return s.tickForGPUPresentation(timeMS, nil)
}

// TickForGPUPresentationWithAudioDelta keeps logical script time deterministic
// while allowing a real-time host to request the elapsed audio duration.
func (s *Session) TickForGPUPresentationWithAudioDelta(timeMS, audioMilliseconds uint64) (RenderPlan, error) {
	return s.tickForGPUPresentation(timeMS, &audioMilliseconds)
}

func (s *Session) tickForGPUPresentation(timeMS uint64, audioMilliseconds *uint64) (RenderPlan, error) {
	s.gpuCapture = true
	s.Services.gpuCapture = true
	defer func() {
		s.gpuCapture = false
		s.Services.gpuCapture = false
	}()
	if _, err := s.tickForPresentation(timeMS, audioMilliseconds); err != nil {
		return RenderPlan{}, err
	}
	return s.gpuPlan, nil
}

func (s *Session) TickWithFrame(timeMS uint64, sink func(uint64, FrameSnapshot)) error {
	return s.TickWithFrameSink(timeMS, sink)
}

type waveSoundSnapshot struct {
	sound                   *WaveSoundBuffer
	loadedRef               *loadedSound
	loaded                  *loadedSound
	status                  string
	generation              uint64
	events                  []SoundEvent
	frequency               int32
	volume                  int32
	volume2                 int32
	pan                     int32
	paused                  bool
	looping                 bool
	useVisBuffer            bool
	sampleCount             int
	sampleAhead             int
	sampleConfigInitialized bool
	positionClock           uint64
	fade                    *soundFade
	fadePending             bool
}

type tickSnapshot struct {
	budget                uint64
	timeMS                uint64
	clockPolls            uint64
	audioOutput           []float32
	events                *EventQueue
	eventState            eventQueueSnapshot
	scheduler             *Scheduler
	schedulerState        schedulerSnapshot
	timers                *TimerRegistry
	timerState            timerRegistrySnapshot
	sounds                map[tjs.ObjectID]waveSoundSnapshot
	mixerSounds           map[*WaveSoundBuffer]waveSoundSnapshot
	soundEvents           map[tjs.ObjectID][]SoundEvent
	videos                map[*VideoOverlay]videoTickSnapshot
	mixer                 *AudioMixer
	mixerVolume           int32
	mixerSources          []*WaveSoundBuffer
	mixerVideos           []*VideoOverlay
	mixerVideoStates      map[*VideoOverlay]videoMixerState
	mixerOutput           []float32
	mixerOwner            *AudioMixer
	systemEventDisabled   bool
	exitOnWindowClose     bool
	drawThreadNum         int64
	exitOnNoWindowStartup bool
	terminated            bool
	terminationCode       int64
	continuousHandlers    []tjs.Value
}

func snapshotWaveSound(sound *WaveSoundBuffer) waveSoundSnapshot {
	state := waveSoundSnapshot{sound: sound, loadedRef: sound.loaded, loaded: cloneLoadedSound(sound.loaded), status: sound.status, generation: sound.generation, events: append([]SoundEvent(nil), sound.events...), frequency: sound.frequency, volume: sound.volume, volume2: sound.volume2, pan: sound.pan, paused: sound.paused, looping: sound.looping, useVisBuffer: sound.useVisBuffer, sampleCount: sound.sampleCount, sampleAhead: sound.sampleAhead, sampleConfigInitialized: sound.sampleConfigInitialized, positionClock: sound.positionClock, fadePending: sound.fadePending}
	if sound.fade != nil {
		fade := *sound.fade
		state.fade = &fade
	}
	return state
}

func (s *Session) tickSnapshot() tickSnapshot {
	s.Services.mu.RLock()
	serviceSounds := make(map[tjs.ObjectID]*WaveSoundBuffer, len(s.Services.Sounds))
	for id, sound := range s.Services.Sounds {
		serviceSounds[id] = sound
	}
	serviceVideos := make(map[*VideoOverlay]struct{}, len(s.Services.Videos))
	for _, video := range s.Services.Videos {
		if video != nil {
			serviceVideos[video] = struct{}{}
		}
	}
	serviceSoundEvents := make(map[tjs.ObjectID][]SoundEvent, len(s.Services.SoundEvents))
	for id, events := range s.Services.SoundEvents {
		serviceSoundEvents[id] = append([]SoundEvent(nil), events...)
	}
	continuousHandlers := append([]tjs.Value(nil), s.Services.ContinuousHandlers...)
	systemEventDisabled := s.Services.EventDisabled
	exitOnWindowClose := s.Services.ExitOnWindowClose
	drawThreadNum := s.Services.DrawThreadNum
	exitOnNoWindowStartup := s.Services.ExitOnNoWindowStartup
	terminated := s.Services.Terminated
	terminationCode := s.Services.TerminationCode
	s.Services.mu.RUnlock()
	snapshot := tickSnapshot{
		budget: s.Budget, timeMS: s.Services.TimeMS, clockPolls: s.Services.clockPolls,
		audioOutput: append([]float32(nil), s.AudioOutput...), events: s.Services.Events,
		scheduler: s.Services.Scheduler, timers: s.Services.Timers, mixer: s.Services.AudioMixer,
		sounds:      make(map[tjs.ObjectID]waveSoundSnapshot, len(serviceSounds)),
		mixerSounds: make(map[*WaveSoundBuffer]waveSoundSnapshot),
		soundEvents: make(map[tjs.ObjectID][]SoundEvent, len(serviceSoundEvents)),
		videos:      make(map[*VideoOverlay]videoTickSnapshot, len(serviceVideos)),
		mixerOwner:  s.Services.audioMixerOwner, systemEventDisabled: systemEventDisabled,
		exitOnWindowClose: exitOnWindowClose, drawThreadNum: drawThreadNum,
		exitOnNoWindowStartup: exitOnNoWindowStartup, terminated: terminated,
		terminationCode: terminationCode, continuousHandlers: continuousHandlers,
	}
	if snapshot.events != nil {
		snapshot.eventState = snapshot.events.snapshot()
	}
	if snapshot.scheduler != nil {
		snapshot.schedulerState = snapshot.scheduler.snapshot()
	}
	if snapshot.timers != nil {
		snapshot.timerState = snapshot.timers.snapshot()
	}
	if snapshot.mixer != nil {
		snapshot.mixerVolume = snapshot.mixer.GlobalVolume
		snapshot.mixerSources = append([]*WaveSoundBuffer(nil), snapshot.mixer.sources...)
		snapshot.mixerVideos = append([]*VideoOverlay(nil), snapshot.mixer.videos...)
		snapshot.mixerVideoStates = make(map[*VideoOverlay]videoMixerState, len(snapshot.mixer.videoStates))
		for video, state := range snapshot.mixer.videoStates {
			snapshot.mixerVideoStates[video] = state.clone()
		}
		snapshot.mixerOutput = append([]float32(nil), snapshot.mixer.output...)
	}
	for video := range serviceVideos {
		snapshot.videos[video] = snapshotVideo(video)
	}
	for id, sound := range serviceSounds {
		if sound == nil {
			continue
		}
		state := snapshotWaveSound(sound)
		snapshot.sounds[id] = state
		if _, ok := snapshot.mixerSounds[sound]; !ok {
			snapshot.mixerSounds[sound] = state
		}
	}
	if snapshot.mixer != nil {
		for _, sound := range snapshot.mixer.sources {
			if sound == nil {
				continue
			}
			if _, ok := snapshot.mixerSounds[sound]; !ok {
				snapshot.mixerSounds[sound] = snapshotWaveSound(sound)
			}
		}
	}
	for id, events := range serviceSoundEvents {
		snapshot.soundEvents[id] = events
	}
	return snapshot
}

func restoreWaveSoundSnapshot(state waveSoundSnapshot) {
	sound := state.sound
	if sound == nil {
		return
	}
	if state.loadedRef != nil && state.loaded != nil {
		*state.loadedRef = *state.loaded
		sound.loaded = state.loadedRef
	} else {
		sound.loaded = nil
	}
	sound.generation = state.generation
	sound.status = state.status
	sound.events = append(sound.events[:0], state.events...)
	sound.frequency = state.frequency
	sound.volume = state.volume
	sound.volume2 = state.volume2
	sound.pan = state.pan
	sound.paused = state.paused
	sound.looping = state.looping
	sound.useVisBuffer = state.useVisBuffer
	sound.sampleCount = state.sampleCount
	sound.sampleAhead = state.sampleAhead
	sound.sampleConfigInitialized = state.sampleConfigInitialized
	sound.positionClock = state.positionClock
	sound.fadePending = state.fadePending
	sound.fade = nil
	if state.fade != nil {
		fade := *state.fade
		sound.fade = &fade
	}
}
func (snapshot tickSnapshot) restore(s *Session) {
	if s == nil || s.Services == nil {
		return
	}

	s.Budget = snapshot.budget
	s.Services.TimeMS = snapshot.timeMS
	s.Services.clockPolls = snapshot.clockPolls
	s.AudioOutput = append(s.AudioOutput[:0], snapshot.audioOutput...)
	s.Services.Events = snapshot.events
	if snapshot.events != nil {
		snapshot.events.restore(snapshot.eventState)
	}
	s.Services.Scheduler = snapshot.scheduler
	if snapshot.scheduler != nil {
		snapshot.scheduler.restore(snapshot.schedulerState)
	}
	s.Services.Timers = snapshot.timers
	if snapshot.timers != nil {
		snapshot.timers.events = snapshot.events
		snapshot.timers.restore(snapshot.timerState)
	}
	for _, state := range snapshot.mixerSounds {
		restoreWaveSoundSnapshot(state)
	}
	for _, state := range snapshot.videos {
		restoreVideoSnapshot(state)
	}
	s.Services.mu.Lock()
	s.Services.Sounds = make(map[tjs.ObjectID]*WaveSoundBuffer, len(snapshot.sounds))
	for id, state := range snapshot.sounds {
		if state.sound != nil {
			s.Services.Sounds[id] = state.sound
		}
	}
	s.Services.SoundEvents = make(map[tjs.ObjectID][]SoundEvent, len(snapshot.soundEvents))
	for id, events := range snapshot.soundEvents {
		s.Services.SoundEvents[id] = append([]SoundEvent(nil), events...)
	}
	s.Services.EventDisabled = snapshot.systemEventDisabled
	s.Services.ExitOnWindowClose = snapshot.exitOnWindowClose
	s.Services.DrawThreadNum = snapshot.drawThreadNum
	s.Services.ExitOnNoWindowStartup = snapshot.exitOnNoWindowStartup
	s.Services.Terminated = snapshot.terminated
	s.Services.TerminationCode = snapshot.terminationCode
	s.Services.ContinuousHandlers = append(s.Services.ContinuousHandlers[:0], snapshot.continuousHandlers...)
	s.Services.mu.Unlock()
	s.Services.AudioMixer = snapshot.mixer
	if snapshot.mixer != nil {
		snapshot.mixer.GlobalVolume = snapshot.mixerVolume
		snapshot.mixer.sources = append(snapshot.mixer.sources[:0], snapshot.mixerSources...)
		snapshot.mixer.videos = append(snapshot.mixer.videos[:0], snapshot.mixerVideos...)
		if snapshot.mixer.videoStates == nil {
			snapshot.mixer.videoStates = make(map[*VideoOverlay]videoMixerState)
		}
		clear(snapshot.mixer.videoStates)
		for video, state := range snapshot.mixerVideoStates {
			snapshot.mixer.videoStates[video] = state.clone()
		}
		snapshot.mixer.output = append(snapshot.mixer.output[:0], snapshot.mixerOutput...)
	}
	s.Services.audioMixerOwner = snapshot.mixerOwner
}
func (s *Session) dispatchBitmapLoad(target uint64) error {
	result, ok := s.Services.takeBitmapAsyncResult(tjs.ObjectID(target))
	if !ok {
		return nil
	}
	targetID := tjs.ObjectID(target)
	if _, exists := s.VM.Objects.Lookup(targetID); !exists {
		return nil
	}
	receiver := tjs.ObjectValue(tjs.NewObjectRef(targetID))
	fn, err := s.VM.GetMember(receiver, "onLoaded", s.Services, &s.Budget)
	if err != nil {
		return err
	}
	if fn.IsVoid() {
		return nil
	}
	meta := tjs.NullValue
	asyncValue := tjs.IntegerValue(1)
	errorValue := tjs.IntegerValue(0)
	message := ""
	if result.err == nil {
		meta, err = bitmapMetadata(s.VM, result.data, result.image.Width, result.image.Height)
		if err != nil {
			return err
		}
	} else {
		errorValue = tjs.IntegerValue(1)
		message = result.err.Error()
	}
	_, err = s.VM.CallEventHandler(fn, receiver,
		[]tjs.Value{meta, asyncValue, errorValue, tjs.StringValue(message)},
		s.Services, &s.Budget)
	return err
}

func (s *Session) DispatchEvents() error {
	if err := s.checkOpen(); err != nil {
		return err
	}
	profile := os.Getenv("KRKRGO_RENDER_STATS") != ""
	dispatchStarted := time.Time{}
	var popTime, callbackTime time.Duration
	var popped, callbacks int
	if profile {
		dispatchStarted = time.Now()
		defer func() {
			elapsed := time.Since(dispatchStarted)
			if elapsed >= 5*time.Millisecond {
				log.Printf("krkrgo event dispatch stages: elapsed=%s popped=%d callbacks=%d queue_pop=%s callback=%s other=%s", elapsed, popped, callbacks, popTime, callbackTime, elapsed-popTime-callbackTime)
			}
		}()
	}
	s.Services.mu.RLock()
	disabled := s.Services.EventDisabled
	s.Services.mu.RUnlock()
	if disabled {
		return nil
	}
	for _, priority := range []Priority{PriorityExclusive, PriorityNormal, PriorityIdle} {
		through := s.Services.Events.BeginBatch()
		for {
			popStarted := time.Time{}
			if profile {
				popStarted = time.Now()
			}
			event, ok := s.Services.Events.Pop(through, priority)
			if profile {
				popTime += time.Since(popStarted)
			}
			if !ok {
				break
			}
			popped++
			if event.Target == 0 {
				continue
			}
			targetID := tjs.ObjectID(event.Target)
			if event.Kind == EventBitmapLoad {
				if err := s.dispatchBitmapLoad(event.Target); err != nil {
					return err
				}
				continue
			}
			soundTrigger := event.Sound || event.Kind == EventSoundTrigger
			if soundTrigger {
				s.Services.mu.Lock()
				pending, ok := s.Services.SoundEvents[targetID]
				if !ok || len(pending) == 0 {
					delete(s.Services.SoundEvents, targetID)
					s.Services.mu.Unlock()
					continue
				}
				sound := s.Services.Sounds[targetID]
				if sound == nil {
					delete(s.Services.SoundEvents, targetID)
					s.Services.mu.Unlock()
					continue
				}
				soundEvent := pending[0]
				if len(pending) == 1 {
					delete(s.Services.SoundEvents, targetID)
				} else {
					s.Services.SoundEvents[targetID] = pending[1:]
				}
				s.Services.mu.Unlock()
				if soundEvent.Generation != sound.Generation() {
					continue
				}
				if object, exists := s.VM.Objects.Lookup(targetID); !exists || !object.IsValid() {
					continue
				}
				// Sound callbacks may be inherited script methods, not instance fields.
				receiver := tjs.ObjectValue(tjs.NewObjectRef(targetID))
				fn, err := s.VM.GetMember(receiver, soundEvent.Type, s.Services, &s.Budget)
				if err != nil {
					return err
				}
				if fn.IsVoid() {
					continue
				}
				args := []tjs.Value{}
				if soundEvent.Type == SoundEventStatus {
					args = []tjs.Value{tjs.StringValue(soundEvent.Status)}
				} else if soundEvent.Type == SoundEventLabel {
					args = []tjs.Value{tjs.StringValue(soundEvent.Label)}
				}
				callbackStarted := time.Time{}
				if profile {
					callbackStarted = time.Now()
				}
				if _, err := s.VM.CallEventHandler(fn, receiver, args, s.Services, &s.Budget); err != nil {
					return err
				}
				if profile {
					elapsed := time.Since(callbackStarted)
					callbackTime += elapsed
					callbacks++
					if elapsed >= time.Millisecond {
						log.Printf("krkrgo event callback: kind=sound/%s priority=%d target=%d elapsed=%s", soundEvent.Type, priority, targetID, elapsed)
					}
				}
				continue
			}
			obj, ok := s.VM.Objects.Lookup(targetID)
			if !ok {
				continue
			}
			name := "onFire"
			if event.Kind == EventTimer {
				name = "onTimer"
			}
			fn, ok := obj.Get(name)
			if !ok {
				continue
			}
			callbackStarted := time.Time{}
			if profile {
				callbackStarted = time.Now()
			}
			if _, err := s.VM.CallFunction(fn, tjs.ObjectValue(tjs.NewObjectRef(targetID)), nil, s.Services, &s.Budget); err != nil {
				return err
			}
			if profile {
				elapsed := time.Since(callbackStarted)
				callbackTime += elapsed
				callbacks++
				if elapsed >= time.Millisecond {
					log.Printf("krkrgo event callback: kind=%d handler=%s priority=%d target=%d elapsed=%s", event.Kind, name, priority, targetID, elapsed)
				}
			}
		}
		if s.Services.Events.ExclusivePosted() {
			continue
		}
	}
	return nil
}
func (s *Session) TakeAudio() []float32 {
	out := append([]float32(nil), s.AudioOutput...)
	s.AudioOutput = nil
	return out
}
func (s *Session) captureFrame() (FrameSnapshot, error) {
	return s.captureFrameFor(false, false)
}

func (s *Session) gpuPlanReusable() bool {
	if s == nil || s.Services == nil || s.Services.Compositor == nil {
		return false
	}
	root := s.Services.Compositor.Root
	if root == nil || !s.gpuPlanCacheValid || s.gpuPlanCacheRoot != root ||
		s.gpuPlanCacheRevision != root.subtreeRevision ||
		s.gpuPlanCacheWidth != s.Services.Compositor.Width ||
		s.gpuPlanCacheHeight != s.Services.Compositor.Height ||
		s.gpuPlanCacheBackground != s.Services.Compositor.Background {
		return false
	}
	for _, dependency := range s.gpuPlanCacheBitmaps {
		if dependency.bitmap == nil || dependency.bitmap.imageRevision() != dependency.revision {
			return false
		}
	}
	return true
}

func (s *Session) rememberGPUPlanCache() {
	root := s.Services.Compositor.Root
	s.gpuPlanCacheValid = root != nil
	s.gpuPlanCacheRoot = root
	s.gpuPlanCacheBitmaps = s.gpuPlanCacheBitmaps[:0]
	if root == nil {
		return
	}
	s.gpuPlanCacheRevision = root.subtreeRevision
	s.gpuPlanCacheWidth = s.Services.Compositor.Width
	s.gpuPlanCacheHeight = s.Services.Compositor.Height
	s.gpuPlanCacheBackground = s.Services.Compositor.Background
	for _, node := range s.gpuPlan.Nodes {
		bitmap := node.gpuStretchSourceBitmap
		if bitmap == nil {
			continue
		}
		known := false
		for _, dependency := range s.gpuPlanCacheBitmaps {
			if dependency.bitmap == bitmap {
				known = true
				break
			}
		}
		if !known {
			s.gpuPlanCacheBitmaps = append(s.gpuPlanCacheBitmaps, gpuPlanBitmapDependency{
				bitmap: bitmap, revision: bitmap.imageRevision(),
			})
		}
	}
}

func (s *Session) captureFrameFor(forPresentation, borrowed bool) (FrameSnapshot, error) {
	s.Services.mu.Lock()
	ids := s.Services.pendingPaintIDs
	s.Services.pendingPaintIDs = s.Services.pendingPaintScratch[:0]
	s.Services.mu.Unlock()
	defer func() {
		s.Services.mu.Lock()
		s.Services.pendingPaintScratch = ids[:0]
		s.Services.mu.Unlock()
	}()
	if len(ids) > 1 {
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	}
	for _, id := range ids {
		layer := s.Services.Layer(id)
		if layer == nil || !layer.callOnPaint {
			continue
		}
		layer.callOnPaint = false
		if err := s.Services.layerEvent(s.VM, layer, "onPaint", nil, &s.Budget); err != nil {
			layer.callOnPaint = true
			s.Services.mu.Lock()
			s.Services.pendingPaintIDs = append(s.Services.pendingPaintIDs, id)
			s.Services.mu.Unlock()
			return FrameSnapshot{}, err
		}
	}
	if s.gpuCapture {
		if s.gpuPlanReusable() {
			s.gpuPlan.StateHashStable = true
			return FrameSnapshot{}, nil
		}
		s.gpuPlan = RenderPlan{
			Nodes:        s.gpuPlan.Nodes[:0],
			ChildIndices: s.gpuPlan.ChildIndices[:0],
		}
		plan, err := s.Services.captureRenderPlanInto(&s.gpuPlan)
		if err != nil {
			return FrameSnapshot{}, err
		}
		s.gpuPlan = plan
		s.rememberGPUPlanCache()
		return FrameSnapshot{}, nil
	}
	if forPresentation {
		return s.Services.CaptureFrameForPresentation()
	}
	if borrowed {
		return s.Services.CaptureFrameBorrowed()
	}
	return s.Services.CaptureFrame()
}

func (s *Session) Frame() (FrameSnapshot, error) {
	return s.frame(false)
}

// CaptureFrameForPresentation returns borrowed bytes for synchronous
// presentation. The slices are valid only until the next frame capture and
// must not be retained or mutated by the caller.
func (s *Session) CaptureFrameForPresentation() (FrameSnapshot, error) {
	return s.frame(true)
}
func (s *Session) CaptureFrameBorrowed() (FrameSnapshot, error) {
	return s.frameWithCapture(false, true)
}
func (s *Session) frame(forPresentation bool) (FrameSnapshot, error) {
	return s.frameWithCapture(forPresentation, false)
}
func (s *Session) frameWithCapture(forPresentation, borrowed bool) (FrameSnapshot, error) {
	if err := s.checkOpen(); err != nil {
		return FrameSnapshot{}, err
	}
	if err := s.Services.advanceLayerTransitions(s.VM, s.Services.TimeMS, true, &s.Budget); err != nil {
		return FrameSnapshot{}, err
	}
	return s.captureFrameFor(forPresentation, borrowed)
}

func (s *Session) CaptureFrame() (FrameSnapshot, error) { return s.Frame() }
func (s *Session) CollectGarbage(roots []tjs.Value) (int, error) {
	if err := s.checkOpen(); err != nil {
		return 0, err
	}
	for _, root := range roots {
		s.Services.AddRoot(root)
	}
	// GC traces VM arrays retained in LayerChildren; serialize it with array
	// Clear/Append operations while keeping the Services mapping lock free.
	s.Services.childrenSyncMu.Lock()
	n, err := s.VM.CollectGarbage(s.Services)
	s.Services.childrenSyncMu.Unlock()
	for _, root := range roots {
		s.Services.RemoveRoot(root)
	}
	if err == nil {
		s.Services.menuData.prune(s.VM)
	}
	return n, err
}
func (s *Session) GC(roots ...tjs.Value) error { _, err := s.CollectGarbage(roots); return err }
func (s *Session) Input(window any, event any) error {
	if err := s.checkOpen(); err != nil {
		return err
	}
	id, err := windowID(window)
	if err != nil {
		return err
	}
	w := s.Services.Window(tjs.ObjectID(id))
	if w == nil {
		return ErrInvalidWindow
	}
	ev, err := normalizeInput(event)
	if err != nil {
		return err
	}
	switch ev.Kind {
	case InputPointerMove, InputPointerDown, InputPointerUp, InputClick, InputDoubleClick, InputWheel:
		for _, layer := range s.Services.Layers {
			if layer.windowID != id {
				continue
			}
			x, y := ev.X, ev.Y
			for parent := layer; parent != nil; parent = parent.Parent {
				x -= parent.Bounds.X
				y -= parent.Bounds.Y
			}
			layer.CursorX, layer.CursorY = x, y
		}
	}
	name, args := eventArgs(ev)
	if name == "" {
		return nil
	}
	s.Services.updateInputState(id, ev)
	context := tjs.ObjectValue(tjs.NewObjectRef(tjs.ObjectID(id)))
	if _, ok := s.VM.Objects.Lookup(tjs.ObjectID(id)); ok {
		fn, lookupErr := s.VM.GetMember(context, name, s.Services, &s.Budget)
		if lookupErr != nil {
			return lookupErr
		}
		if fn.Kind() != tjs.KindVoid {
			_, err = s.VM.CallFunction(fn, context, args, s.Services, &s.Budget)
		}
	}
	if err != nil {
		return err
	}
	return s.dispatchLayerMouseInput(id, w, ev)
}
func windowID(window any) (uint64, error) {
	switch x := window.(type) {
	case uint64:
		return x, nil
	case tjs.ObjectID:
		return uint64(x), nil
	case tjs.Value:
		if x.Kind() != tjs.KindObject || x.IsNull() {
			return 0, ErrInvalidWindow
		}
		return uint64(x.ObjectRef().Object), nil
	default:
		return 0, ErrInvalidWindow
	}
}
func normalizeInput(event any) (InputEvent, error) {
	switch x := event.(type) {
	case InputEvent:
		return x, nil
	case core.InputAction:
		switch x.Type {
		case core.InputActionPointer:
			if x.Pressed {
				return InputEvent{Kind: InputPointerDown, X: x.X, Y: x.Y}, nil
			}
			return InputEvent{Kind: InputPointerUp, X: x.X, Y: x.Y}, nil
		case core.InputActionKey:
			key, ok := core.KeyCode(x.Code)
			if !ok {
				return InputEvent{}, fmt.Errorf("unsupported key code %q", x.Code)
			}
			if x.Pressed {
				return InputEvent{Kind: InputKeyDown, Key: key}, nil
			}
			return InputEvent{Kind: InputKeyUp, Key: key}, nil
		default:
			return InputEvent{}, nil
		}
	default:
		return InputEvent{}, fmt.Errorf("unsupported input event %T", event)
	}
}
func eventArgs(ev InputEvent) (string, []tjs.Value) {
	n := func(v int32) tjs.Value { return tjs.IntegerValue(int64(v)) }
	switch ev.Kind {
	case InputPointerMove:
		return "onMouseMove", []tjs.Value{n(ev.X), n(ev.Y), n(ev.Shift)}
	case InputPointerDown:
		return "onMouseDown", []tjs.Value{n(ev.X), n(ev.Y), n(ev.Button), n(ev.Shift)}
	case InputPointerUp:
		return "onMouseUp", []tjs.Value{n(ev.X), n(ev.Y), n(ev.Button), n(ev.Shift)}
	case InputClick:
		return "onClick", []tjs.Value{n(ev.X), n(ev.Y)}
	case InputDoubleClick:
		return "onDoubleClick", []tjs.Value{n(ev.X), n(ev.Y)}
	case InputPointerLeave:
		return "onMouseLeave", nil
	case InputWheel:
		return "onMouseWheel", []tjs.Value{n(ev.Shift), n(ev.Delta), n(ev.X), n(ev.Y)}
	case InputKeyDown:
		return "onKeyDown", []tjs.Value{n(ev.Key), n(ev.Shift)}
	case InputKeyUp:
		return "onKeyUp", []tjs.Value{n(ev.Key), n(ev.Shift)}
	case InputText:
		return "onKeyPress", []tjs.Value{tjs.StringValue(ev.Text)}
	case InputFocus:
		if ev.Focused {
			return "onActivate", nil
		}
		return "onDeactivate", nil
	}
	return "", nil
}
func (s *Session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.Services != nil {
		s.Services.ensureInit()
		s.Services.unbindSessionPreprocessor(s.VM, s.preprocessor)
		s.Services.mu.RLock()
		sounds := make([]*WaveSoundBuffer, 0, len(s.Services.Sounds))
		for _, sound := range s.Services.Sounds {
			sounds = append(sounds, sound)
		}
		videos := make([]*VideoOverlay, 0, len(s.Services.Videos))
		for _, video := range s.Services.Videos {
			videos = append(videos, video)
		}
		s.Services.mu.RUnlock()
		for _, sound := range sounds {
			s.Services.AudioMixer.Remove(sound)
		}
		for _, video := range videos {
			s.Services.AudioMixer.RemoveVideo(video)
			if video != nil {
				video.Close()
			}
		}
		if s.Services.Storage != nil {
			s.Services.Storage.SetExtractionFilter(nil)
		}
		s.Services.mu.Lock()
		s.Services.xp3ExtractionFilter = tjs.VoidValue
		s.Services.Videos = make(map[tjs.ObjectID]*VideoOverlay)
		s.Services.audioMixerOwner = nil
		s.Services.mu.Unlock()
		s.AudioOutput = nil
	}
	if s.VM != nil {
		clearPSDDocuments(s.VM)
		_ = s.VM.Release()
		s.VM = nil
	}
	return nil
}

var _ tjs.Host = (*Services)(nil)
var _ tjs.HostContext = (*Services)(nil)
var _ tjs.HostResult = (*Services)(nil)
var _ tjs.HostTrace = (*Services)(nil)
var _ tjs.HostRoots = (*Services)(nil)
var _ tjs.HostTraceObjects = (*Services)(nil)
