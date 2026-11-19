//go:build darwin

package host

import (
	"fmt"
	"sync"

	"github.com/ebitengine/purego"

	runtime "github.com/eslzzyl/krkrgo/internal/runtime"
)

type darwinCGPoint struct {
	x float64
	y float64
}

type darwinCursorDevice struct {
	once sync.Once
	warp func(darwinCGPoint) int32
	err  error
}

func newCursorDevice() runtime.CursorDevice { return &darwinCursorDevice{} }

func (d *darwinCursorDevice) initialize() {
	handle, err := purego.Dlopen(
		"/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics",
		purego.RTLD_NOW|purego.RTLD_LOCAL,
	)
	if err != nil {
		d.err = err
		return
	}
	address, err := purego.Dlsym(handle, "CGWarpMouseCursorPosition")
	if err != nil {
		d.err = err
		return
	}
	purego.RegisterFunc(&d.warp, address)
}

func (d *darwinCursorDevice) SetPosition(x, y int32) error {
	if d == nil {
		return runtime.UnsupportedDevice("cursor positioning")
	}
	d.once.Do(d.initialize)
	if d.err != nil || d.warp == nil {
		return fmt.Errorf("%w: CoreGraphics cursor positioning: %v", runtime.ErrUnsupportedDevice, d.err)
	}
	if result := d.warp(darwinCGPoint{x: float64(x), y: float64(y)}); result != 0 {
		return fmt.Errorf("%w: CGWarpMouseCursorPosition returned %d", runtime.ErrUnsupportedDevice, result)
	}
	return nil
}
