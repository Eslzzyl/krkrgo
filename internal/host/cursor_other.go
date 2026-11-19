//go:build !darwin && !linux && !windows

package host

import runtime "github.com/eslzzyl/krkrgo/internal/runtime"

type unsupportedCursorDevice struct{}

func newCursorDevice() runtime.CursorDevice { return unsupportedCursorDevice{} }

func (unsupportedCursorDevice) SetPosition(int32, int32) error {
	return runtime.UnsupportedDevice("cursor positioning")
}
