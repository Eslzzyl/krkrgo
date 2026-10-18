package runtime

import (
	"errors"
	"fmt"
	"path"
	"reflect"
	"sort"
	"strings"
	"sync"

	"github.com/eslzzyl/krkrgo/internal/tjs"
)

// pluginLinkKey matches plugin paths using Windows-style path and extension
// semantics while retaining the spelling supplied by the caller for display.
func pluginLinkKey(name string) string {
	clean := strings.ToLower(path.Clean(strings.ReplaceAll(name, `\`, "/")))
	ext := path.Ext(clean)
	if ext == ".tpm" {
		clean = strings.TrimSuffix(clean, ext) + ".dll"
	}
	if path.Base(clean) == "kagparserex.dll" {
		clean = path.Join(path.Dir(clean), "kagparser.dll")
	}
	return clean
}

// nativePluginProviders maps the plugin module names the runtime answers to the
// facility that satisfies them, so a link is accepted only when the capability
// the reference module supplies exists in this runtime and no module's API is
// fabricated by a link. Names outside the table fail through
// ErrUnsupportedNative, matching the reference plugin managers, which register
// nothing for a module they do not hold (reference/krkrz/base/win32/PluginImpl.cpp:442
// throws from the plugin constructor; reference/krkr2-main/cpp/core/plugin/ncbind.cpp:12
// returns false).
//
// Windows-only modules are deliberately absent: the runtime holds no
// cross-platform implementation of what they do, and macOS/Linux cannot load the
// shipped DLLs, so linking them must report the unavailable module instead of a
// false success.
//
//   - win32ole.dll: COM/OLE automation; neither reference ships it
//     (reference/krkr2-main/docs/KRKR_PLUGINS.md has no entry).
//   - krkrsteam.dll, steamdrawdevice.dll: Steamworks integration and its draw
//     device; the portable reference only compiles them with the Steam SDK
//     (reference/krkr2-main/cpp/plugins/CMakeLists.txt:23).
//   - k2compat.dll: KAG's krkrz-only window/console compatibility plugin, absent
//     from both reference plugin inventories.
//   - kagexopt.dll: absent from both references and never linked by the shipped
//     games' scripts.
//   - wuopus.dll: the runtime's audio path decodes no Opus; the FFmpeg sidecar
//     only enables the Ogg/Vorbis decoder (scripts/build-ffmpeg.sh:183).
var nativePluginProviders = map[string]string{
	"addfont.dll":       "System.addFont registers storage font data (system_native.go; cpp/plugins/addFont.cpp)",
	"alphamovie.dll":    "AlphaMovie path/rectangle playback (alpha_movie_native.go; assets/amv_decode.go)",
	"csvparser.dll":     "CSVParser (csv_parser_native.go; cpp/plugins/csvParser.cpp)",
	"extnagano.dll":     "link-only: the module installs transition providers without TJS globals; Layer.beginTransition supports its spin provider alongside crossfade and universal",
	"extrans.dll":       "link-only: the module installs transition providers without TJS globals; Layer.beginTransition keeps its built-in crossfade/universal providers and rejects the other extrans providers",
	"fftgraph.dll":      "drawFFTGraph global; graph rendering reports an unsupported device (lightweight_plugins_native.go; cpp/plugins/fftgraph.cpp)",
	"fstat.dll":         "Storages file metadata and mutation helpers (storages_native.go; cpp/plugins/fstat)",
	"getabout.dll":      "System.getAboutString (system_native.go; cpp/plugins/getabout.cpp)",
	"getsample.dll":     "WaveSoundBuffer sample extensions (sample_native.go; cpp/plugins/getSample.cpp)",
	"kagparser.dll":     "built-in KAGParser; KAGParserEx.dll resolves here (kag_native.go; cpp/plugins/KAGParser/kagparserex.cpp)",
	"krmovie.dll":       "link-only: the module backs the core VideoOverlay class and registers no TJS names, so the link installs nothing on top of the runtime's own VideoOverlay and ASF/AMV decoders (video.go, assets/amv.go; krkrz/movie/win32/krmovie.cpp, cpp/core/movie/ffmpeg)",
	"layerexbtoa.dll":   "link-only: callers guard on the missing Layer method, and Layer's built-in pixel operations (layer_image.go) are all the runtime claims; other extension operations report ErrUnsupportedNative",
	"layerexdraw.dll":   "link-only: the module's GDI+ surface is not implemented, so Layer reports the missing operations instead of fabricating them (layer_native.go, image_function_native.go)",
	"layereximage.dll":  "link-only: the game's LayerEx.tjs helpers compose Layer's built-in pixel operations (layer_image.go); other extension operations report ErrUnsupportedNative",
	"layerexraster.dll": "link-only: callers guard on the missing Layer method, and Layer's built-in pixel operations (layer_image.go) are all the runtime claims; other extension operations report ErrUnsupportedNative",
	"menu.dll":          "shared MenuItem surface (menu_native.go)",
	"motionplayer.dll":  "Motion resource manager subset with layer/shape/play constants (motionplayer_native.go; cpp/plugins/motionplayer)",
	"packinone.dll":     "component container for the ScriptsEx/saveStruct/csvParser/LayerExImage exports the runtime already provides (scripts_ex_native.go, tjs/serialization.go, csv_parser_native.go)",
	"psbfile.dll":       "PSBFile (psbfile_native.go; cpp/plugins/psbfile)",
	"psd.dll":           "PSD parser and portable PSD class (psd_native.go, assets/psd.go; cpp/plugins/psdfile)",
	"scriptsex.dll":     "ScriptsEx helpers on the built-in Scripts class (scripts_ex_native.go; cpp/plugins/scriptsEx.cpp)",
	"textrender.dll":    "TextRenderBase (text_render_native.go; cpp/plugins/TextRender.cpp)",
	"varfile.dll":       "var:// storage subset (var_storage_native.go; cpp/plugins/varfile.cpp)",
	"win32dialog.dll":   "portable WIN32Dialog with the host dialog handler (lightweight_plugins_native.go; cpp/plugins/win32dialog.cpp)",
	"windowex.dll":      "MenuItem surface plus Window extension natives (menu_native.go, window_native.go; cpp/plugins/windowEx.cpp)",
	"wutcwf.dll":        "TCWF0 ADPCM decoding in WaveSoundBuffer (assets/wutcwf.go; cpp/plugins/wutcwf.cpp)",
	"wuvorbis.dll":      "link-only: Ogg Vorbis decoding is already in the core sound path (sound.go with the FFmpeg sidecar; cpp/core/sound/VorbisWaveDecoder.cpp)",
}

func isImplementedNativePlugin(key string) bool {
	_, ok := nativePluginProviders[path.Base(key)]
	return ok
}

var ErrPluginName = errors.New("invalid plugin name")
var ErrPluginRegistrar = errors.New("invalid plugin registrar")

// PluginRegistrar is the narrow registration ABI used by host plugins.
type PluginRegistrar interface{ Register(*tjs.ObjectTable) error }

type Plugin struct {
	Name     string
	Register func(*tjs.ObjectTable) error
}

type pluginEntry struct {
	name         string
	register     func(*tjs.ObjectTable) error
	installed    map[*tjs.ObjectTable]bool
	installedNil bool
}

// PluginRegistry serializes registration and remembers installed plugins. A
// second registration of an already-known name is intentionally a no-op: host
// startup paths commonly discover the same native plugin more than once.
type PluginRegistry struct {
	mu      sync.Mutex
	plugins map[string]*pluginEntry
}

func NewPluginRegistry() *PluginRegistry {
	return &PluginRegistry{plugins: make(map[string]*pluginEntry)}
}
func (r *PluginRegistry) ensure() {
	if r.plugins == nil {
		r.plugins = make(map[string]*pluginEntry)
	}
}

// Register accepts Plugin, PluginRegistrar, or a function with signature
// func(*tjs.ObjectTable) error / func() error. The interface form keeps host
// adapters independent from a concrete plugin package.
func (r *PluginRegistry) Register(name string, registrar any) error {
	if r == nil {
		return fmt.Errorf("%w: nil registry", ErrPluginName)
	}
	if name == "" {
		return fmt.Errorf("%w: empty plugin name", ErrPluginName)
	}
	fn, err := pluginFunc(name, registrar)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensure()
	if _, exists := r.plugins[name]; exists {
		return nil
	}
	r.plugins[name] = &pluginEntry{name: name, register: fn, installed: make(map[*tjs.ObjectTable]bool)}
	return nil
}
func (r *PluginRegistry) RegisterPlugin(plugin Plugin) error { return r.Register(plugin.Name, plugin) }

func pluginFunc(name string, registrar any) (func(*tjs.ObjectTable) error, error) {
	if registrar != nil {
		v := reflect.ValueOf(registrar)
		if (v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface || v.Kind() == reflect.Map || v.Kind() == reflect.Slice || v.Kind() == reflect.Func) && v.IsNil() {
			return nil, fmt.Errorf("%w: %s", ErrPluginRegistrar, name)
		}
	}
	if p, ok := registrar.(Plugin); ok {
		if p.Register == nil {
			return nil, fmt.Errorf("%w: %s", ErrPluginRegistrar, name)
		}
		return p.Register, nil
	}
	if p, ok := registrar.(PluginRegistrar); ok {
		return p.Register, nil
	}
	if fn, ok := registrar.(func(*tjs.ObjectTable) error); ok {
		return fn, nil
	}
	if fn, ok := registrar.(func() error); ok {
		return func(*tjs.ObjectTable) error { return fn() }, nil
	}
	return nil, fmt.Errorf("%w: %s", ErrPluginRegistrar, name)
}

func (r *PluginRegistry) Install(name string, table *tjs.ObjectTable) error {
	if r == nil {
		return fmt.Errorf("%w: nil registry", ErrPluginName)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.plugins[name]
	if !ok {
		return fmt.Errorf("plugin %q not registered", name)
	}
	if table == nil {
		if entry.installedNil {
			return nil
		}
	} else if entry.installed[table] {
		return nil
	}
	if err := entry.register(table); err != nil {
		return err
	}
	if table == nil {
		entry.installedNil = true
	} else {
		entry.installed[table] = true
	}
	return nil
}
func (r *PluginRegistry) InstallAll(table *tjs.ObjectTable) error {
	for _, name := range r.Names() {
		if err := r.Install(name, table); err != nil {
			return err
		}
	}
	return nil
}
func (r *PluginRegistry) Has(name string) bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.plugins[name]
	return ok
}
func (r *PluginRegistry) Names() []string {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	names := make([]string, 0, len(r.plugins))
	for name := range r.plugins {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
func (r *PluginRegistry) Len() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.plugins)
}
