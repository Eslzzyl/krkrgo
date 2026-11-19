//go:build linux

package host

import (
	"fmt"
	"sync"

	"github.com/ebitengine/purego"

	runtime "github.com/eslzzyl/krkrgo/internal/runtime"
)

type linuxCursorDevice struct {
	once        sync.Once
	display     uintptr
	root        uintptr
	openDisplay func(*byte) uintptr
	defaultRoot func(uintptr) uintptr
	warpPointer func(uintptr, uintptr, uintptr, int32, int32, uint32, uint32, int32, int32) int32
	flush       func(uintptr) int32
	err         error
}

func newCursorDevice() runtime.CursorDevice { return &linuxCursorDevice{} }

func (d *linuxCursorDevice) initialize() {
	handle, err := purego.Dlopen("libX11.so.6", purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		handle, err = purego.Dlopen("libX11.so", purego.RTLD_NOW|purego.RTLD_LOCAL)
	}
	if err != nil {
		d.err = err
		return
	}
	register := func(name string, target any) error {
		address, lookupErr := purego.Dlsym(handle, name)
		if lookupErr != nil {
			return lookupErr
		}
		purego.RegisterFunc(target, address)
		return nil
	}
	if err := register("XOpenDisplay", &d.openDisplay); err != nil {
		d.err = err
		return
	}
	if err := register("XDefaultRootWindow", &d.defaultRoot); err != nil {
		d.err = err
		return
	}
	if err := register("XWarpPointer", &d.warpPointer); err != nil {
		d.err = err
		return
	}
	if err := register("XFlush", &d.flush); err != nil {
		d.err = err
		return
	}
	d.display = d.openDisplay(nil)
	if d.display == 0 {
		d.err = fmt.Errorf("XOpenDisplay returned nil")
		return
	}
	d.root = d.defaultRoot(d.display)
	if d.root == 0 {
		d.err = fmt.Errorf("XDefaultRootWindow returned nil")
	}
}

func (d *linuxCursorDevice) SetPosition(x, y int32) error {
	if d == nil {
		return runtime.UnsupportedDevice("cursor positioning")
	}
	d.once.Do(d.initialize)
	if d.err != nil || d.display == 0 || d.root == 0 || d.warpPointer == nil || d.flush == nil {
		return fmt.Errorf("%w: X11 cursor positioning: %v", runtime.ErrUnsupportedDevice, d.err)
	}
	if result := d.warpPointer(d.display, 0, d.root, 0, 0, 0, 0, x, y); result == 0 {
		return runtime.UnsupportedDevice("cursor positioning")
	}
	d.flush(d.display)
	return nil
}
