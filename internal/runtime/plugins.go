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

// LayerExImage's game-facing TJS helpers compose Layer's built-in pixel
// operations; unavailable extension operations still fail through Layer's
// normal ErrUnsupportedNative path instead of being claimed as successful.
func isImplementedNativePlugin(key string) bool {
	switch path.Base(key) {
	case "addfont.dll", "alphamovie.dll", "csvparser.dll", "extnagano.dll", "extrans.dll", "fftgraph.dll", "getabout.dll", "getsample.dll", "kagparser.dll", "layereximage.dll", "menu.dll", "motionplayer.dll", "packinone.dll", "psbfile.dll", "scriptsex.dll", "varfile.dll", "win32dialog.dll", "windowex.dll":
		return true
	default:
		return false
	}
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
