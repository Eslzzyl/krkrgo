package runtime

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/eslzzyl/krkrgo/internal/tjs"
)

// NativeFunc is a host callback independent of the VM registration spelling.
type NativeFunc func(context tjs.Value, args []tjs.Value) (tjs.Value, error)

type NativeEntry struct {
	Name      string
	Operation string
	Function  NativeFunc
}
type NativeRegistry struct {
	mu      sync.RWMutex
	entries map[string]NativeEntry
}

func NewNativeRegistry() *NativeRegistry {
	return &NativeRegistry{entries: make(map[string]NativeEntry)}
}
func (r *NativeRegistry) ensure() {
	if r.entries == nil {
		r.entries = make(map[string]NativeEntry)
	}
}
func (r *NativeRegistry) Register(name, operation string) error {
	if strings.TrimSpace(name) == "" || strings.TrimSpace(operation) == "" {
		return fmt.Errorf("invalid native registration")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensure()
	if _, ok := r.entries[name]; ok {
		return fmt.Errorf("native already registered: %s", name)
	}
	r.entries[name] = NativeEntry{Name: name, Operation: operation}
	return nil
}
func (r *NativeRegistry) RegisterFunc(name string, fn NativeFunc) error {
	if fn == nil {
		return fmt.Errorf("nil native function")
	}
	if err := r.Register(name, name); err != nil {
		return err
	}
	r.mu.Lock()
	e := r.entries[name]
	e.Function = fn
	r.entries[name] = e
	r.mu.Unlock()
	return nil
}
func (r *NativeRegistry) Replace(name, operation string) error {
	if strings.TrimSpace(name) == "" || strings.TrimSpace(operation) == "" {
		return fmt.Errorf("invalid native registration")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensure()
	r.entries[name] = NativeEntry{Name: name, Operation: operation}
	return nil
}
func (r *NativeRegistry) Lookup(name string) (NativeFunc, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.entries[name]
	return e.Function, ok
}
func (r *NativeRegistry) Entry(name string) (NativeEntry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.entries[name]
	return e, ok
}
func (r *NativeRegistry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.entries))
	for n := range r.entries {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
func (r *NativeRegistry) Entries() []NativeEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]NativeEntry, 0, len(r.entries))
	for _, e := range r.entries {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Install adds operation names to the VM. The VM dispatches calls back through
// Services, preserving one host boundary for native and replay execution.
func (r *NativeRegistry) Install(vm *tjs.VM) error {
	if vm == nil {
		return fmt.Errorf("nil TJS VM")
	}
	for _, e := range r.Entries() {
		if err := vm.RegisterNative(e.Operation); err != nil {
			return err
		}
	}
	return nil
}

func RegisterDefaultNatives(r *NativeRegistry) error {
	if r == nil {
		return fmt.Errorf("nil native registry")
	}
	for _, name := range []string{"Debug.message", "Debug.notice", "System.getTickCount", "System.getTickCount64", "System.get:screenWidth", "System.get:screenHeight", "System.get:desktopLeft", "System.get:desktopTop", "System.get:desktopWidth", "System.get:desktopHeight", "System.get:personalPath", "System.get:appDataPath", "System.get:graphicCacheLimit", "System.set:graphicCacheLimit", "Plugins.link", "Plugins.getList", "TextStream.read", "TextStream.write", "TextStream.writePlugin", "System.addContinuousHandler", "System.removeContinuousHandler", "Window.@initialize", "Window.@invalidate", "Console.@initialize", "Console.@invalidate", "Console.Console", "Console.finalize", "Debug.get:console", "VideoOverlay.@initialize", "VideoOverlay.@invalidate", "VideoOverlay.VideoOverlay", "VideoOverlay.finalize", "VideoOverlay.open", "VideoOverlay.play", "VideoOverlay.stop", "VideoOverlay.close", "VideoOverlay.setPos", "VideoOverlay.setSize", "VideoOverlay.setBounds", "VideoOverlay.pause", "VideoOverlay.rewind", "VideoOverlay.prepare", "VideoOverlay.setSegmentLoop", "VideoOverlay.cancelSegmentLoop", "VideoOverlay.setPeriodEvent", "VideoOverlay.cancelPeriodEvent", "VideoOverlay.selectAudioStream", "VideoOverlay.setMixingLayer", "VideoOverlay.resetMixingLayer", "VideoOverlay.onStatusChanged", "VideoOverlay.onCallbackCommand", "VideoOverlay.onPeriod", "VideoOverlay.onFrameUpdate", "VideoOverlay.get:position", "VideoOverlay.get:left", "VideoOverlay.get:top", "VideoOverlay.get:width", "VideoOverlay.get:height", "VideoOverlay.get:originalWidth", "VideoOverlay.get:originalHeight", "VideoOverlay.get:visible", "VideoOverlay.get:loop", "VideoOverlay.get:frame", "VideoOverlay.get:fps", "VideoOverlay.get:numberOfFrame", "VideoOverlay.get:totalTime", "VideoOverlay.get:layer1", "VideoOverlay.get:layer2", "VideoOverlay.get:mode", "VideoOverlay.get:playRate", "VideoOverlay.get:segmentLoopStartFrame", "VideoOverlay.get:segmentLoopEndFrame", "VideoOverlay.get:periodEventFrame", "VideoOverlay.get:audioBalance", "VideoOverlay.get:audioVolume", "VideoOverlay.get:numberOfAudioStream", "VideoOverlay.get:enabledAudioStream", "VideoOverlay.get:numberOfVideoStream", "VideoOverlay.get:enabledVideoStream", "VideoOverlay.get:mixingMovieAlpha", "VideoOverlay.get:mixingMovieBGColor", "VideoOverlay.set:left", "VideoOverlay.set:top", "VideoOverlay.set:width", "VideoOverlay.set:height", "VideoOverlay.set:visible", "VideoOverlay.set:loop", "VideoOverlay.set:mode", "VideoOverlay.set:position", "VideoOverlay.set:frame", "VideoOverlay.set:playRate", "VideoOverlay.set:audioBalance", "VideoOverlay.set:audioVolume", "VideoOverlay.set:segmentLoopStartFrame", "VideoOverlay.set:segmentLoopEndFrame", "VideoOverlay.set:periodEventFrame", "VideoOverlay.set:mixingMovieAlpha", "VideoOverlay.set:mixingMovieBGColor", "VideoOverlay.set:contrast", "VideoOverlay.set:brightness", "VideoOverlay.set:hue", "VideoOverlay.set:saturation"} {
		if _, ok := r.Entry(name); !ok {
			if err := r.Register(name, name); err != nil {
				return err
			}
		}
	}
	for _, name := range []string{"BookMarkIO", "_BookMarkIO", "getBookMarkFileNameAtNum", "_getBookMarkFileNameAtNum", "getSysSE"} {
		if _, ok := r.Entry(name); !ok {
			if err := r.Register(name, name); err != nil {
				return err
			}
		}
	}
	for _, name := range []string{"Console.log", "Console.info", "Console.warn", "Console.error", "Console.debug", "Console.trace", "Console.dir", "Console.getPlacement", "Console.getRect", "Console.setPos", "Console.restoreMaximize", "Console.bringTo", "Console.bringAfter"} {
		if _, ok := r.Entry(name); !ok {
			if err := r.Register(name, name); err != nil {
				return err
			}
		}
	}
	for _, name := range []string{"Debug.logAsError"} {
		if _, ok := r.Entry(name); !ok {
			if err := r.Register(name, name); err != nil {
				return err
			}
		}
	}
	for _, name := range []string{
		"System.terminate", "System.exit", "System.toActualColor", "System.clearGraphicCache", "System.touchImages", "System.createUUID", "System.createAppLock", "System.inform", "System.assignMessage", "System.doCompact",
		"System.get:exePath", "System.get:exeName", "System.get:dataPath", "System.get:title", "System.set:title",
		"System.get:versionString", "System.get:versionInformation", "System.get:eventDisabled", "System.set:eventDisabled", "System.get:platformName", "System.get:osName",
		"System.get:exitOnWindowClose", "System.set:exitOnWindowClose", "System.get:drawThreadNum", "System.set:drawThreadNum", "System.get:processorNum", "System.get:exeBits", "System.get:osBits", "System.get:exitOnNoWindowStartup", "System.set:exitOnNoWindowStartup",
		"System.getDisplayMonitors", "System.getMonitorInfo", "System.getCursorPos", "System.setCursorPos",
	} {
		if _, ok := r.Entry(name); !ok {
			if err := r.Register(name, name); err != nil {
				return err
			}
		}
	}
	for _, name := range []string{"WaveSoundBuffer.@initialize", "WaveSoundBuffer.@invalidate", "WaveSoundBuffer.WaveSoundBuffer", "WaveSoundBuffer.finalize", "WaveSoundBuffer.close", "WaveSoundBuffer.open", "WaveSoundBuffer.openBytes", "WaveSoundBuffer.play", "WaveSoundBuffer.stop", "WaveSoundBuffer.pause", "WaveSoundBuffer.rewind", "WaveSoundBuffer.setPos", "WaveSoundBuffer.setSamplePosition", "WaveSoundBuffer.fade", "WaveSoundBuffer.stopFade", "WaveSoundBuffer.onStatusChanged", "WaveSoundBuffer.onLabel", "WaveSoundBuffer.onFadeCompleted", "WaveSoundBuffer.get:status", "WaveSoundBuffer.get:position", "WaveSoundBuffer.get:samplePosition", "WaveSoundBuffer.get:totalTime", "WaveSoundBuffer.get:frequency", "WaveSoundBuffer.get:bits", "WaveSoundBuffer.get:channels", "WaveSoundBuffer.get:volume", "WaveSoundBuffer.get:volume2", "WaveSoundBuffer.get:pan", "WaveSoundBuffer.get:paused", "WaveSoundBuffer.get:looping", "WaveSoundBuffer.get:useVisBuffer", "WaveSoundBuffer.get:labels", "WaveSoundBuffer.set:volume", "WaveSoundBuffer.set:volume2", "WaveSoundBuffer.set:pan", "WaveSoundBuffer.set:paused", "WaveSoundBuffer.set:looping", "WaveSoundBuffer.set:frequency", "WaveSoundBuffer.set:useVisBuffer", "WaveSoundBuffer.set:position", "WaveSoundBuffer.set:samplePosition", "WaveSoundBuffer.setDevice", "WaveSoundBuffer.getDevice", "WaveSoundBuffer.setGlobalVolume", "WaveSoundBuffer.getGlobalVolume", "WaveSoundBuffer.stopAll"} {
		if _, ok := r.Entry(name); !ok {
			if err := r.Register(name, name); err != nil {
				return err
			}
		}
	}
	for _, name := range []string{"WaveSoundBuffer.get:sampleValue", "WaveSoundBuffer.get:sampleCount", "WaveSoundBuffer.set:sampleCount", "WaveSoundBuffer.get:sampleAhead", "WaveSoundBuffer.set:sampleAhead", "WaveSoundBuffer.setDefaultCounts", "WaveSoundBuffer.setDefaultAheads"} {
		if _, ok := r.Entry(name); !ok {
			if err := r.Register(name, name); err != nil {
				return err
			}
		}
	}
	for _, name := range []string{"Timer.@initialize", "Timer.@invalidate", "Timer.Timer", "Timer.finalize", "Timer.onTimer", "Timer.get:interval", "Timer.set:interval", "Timer.get:enabled", "Timer.set:enabled", "Timer.get:capacity", "Timer.set:capacity", "Timer.get:mode", "Timer.set:mode"} {
		if _, ok := r.Entry(name); !ok {
			if err := r.Register(name, name); err != nil {
				return err
			}
		}
	}
	for _, name := range []string{"Rect.@initialize", "Rect.@invalidate", "Rect.Rect", "Rect.finalize", "Rect.isEmpty", "Rect.setSize", "Rect.setOffset", "Rect.addOffset", "Rect.clear", "Rect.set", "Rect.clip", "Rect.union", "Rect.intersects", "Rect.included", "Rect.includedPos", "Rect.equal", "Rect.get:width", "Rect.set:width", "Rect.get:height", "Rect.set:height", "Rect.get:left", "Rect.set:left", "Rect.get:top", "Rect.set:top", "Rect.get:right", "Rect.set:right", "Rect.get:bottom", "Rect.set:bottom", "Rect.get:nativeArray"} {
		if _, ok := r.Entry(name); !ok {
			if err := r.Register(name, name); err != nil {
				return err
			}
		}
	}
	for _, name := range []string{"AsyncTrigger.@initialize", "AsyncTrigger.@invalidate", "AsyncTrigger.AsyncTrigger", "AsyncTrigger.finalize", "AsyncTrigger.trigger", "AsyncTrigger.cancel", "AsyncTrigger.onFire", "AsyncTrigger.get:cached", "AsyncTrigger.set:cached", "AsyncTrigger.get:mode", "AsyncTrigger.set:mode"} {
		if _, ok := r.Entry(name); !ok {
			if err := r.Register(name, name); err != nil {
				return err
			}
		}
	}
	return nil
}

// UnsupportedDevice reports an unavailable native device with a stable sentinel.
func UnsupportedDevice(device string) error {
	if device == "" {
		device = "device"
	}
	return fmt.Errorf("%w: %s", ErrUnsupportedDevice, device)
}
