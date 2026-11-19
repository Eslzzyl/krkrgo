//go:build windows

package host

import (
	"fmt"

	"golang.org/x/sys/windows"

	runtime "github.com/eslzzyl/krkrgo/internal/runtime"
)

type windowsCursorDevice struct {
	setCursorPos *windows.LazyProc
}

func newCursorDevice() runtime.CursorDevice {
	return &windowsCursorDevice{
		setCursorPos: windows.NewLazySystemDLL("user32.dll").NewProc("SetCursorPos"),
	}
}

func (d *windowsCursorDevice) SetPosition(x, y int32) error {
	if d == nil || d.setCursorPos == nil {
		return runtime.UnsupportedDevice("cursor positioning")
	}
	if result, _, err := d.setCursorPos.Call(uintptr(int64(x)), uintptr(int64(y))); result == 0 {
		if err != nil {
			return fmt.Errorf("%w: SetCursorPos: %v", runtime.ErrUnsupportedDevice, err)
		}
		return runtime.UnsupportedDevice("cursor positioning")
	}
	return nil
}
