package runtime

import (
	"fmt"

	"github.com/eslzzyl/krkrgo/internal/tjs"
)

// ShowCommand describes the three Win32-compatible visibility transitions.
type ShowCommand uint8

const (
	ShowMinimize ShowCommand = iota + 1
	ShowMaximize
	ShowRestore
)

// DrawDevice is the host-facing identity of a window drawing device. Recreate
// advances Generation while preserving ID, allowing presenters to discard
// stale GPU resources without changing script-visible layer state.
type DrawDevice struct{ ID, Generation uint64 }

func NewDrawDevice(id uint64) *DrawDevice { return &DrawDevice{ID: id} }
func (d *DrawDevice) Recreate() {
	if d != nil {
		d.Generation++
	}
}

type windowedBounds struct {
	left, top, width, height int32
	scale, origin            [2]int32
}

// WindowState contains deterministic window state shared by headless replay
// and a native presenter. Coordinates are desktop pixels; the primary layer
// dimensions remain logical pixels and are transformed by DrawRect.
type WindowState struct {
	Constructed             bool
	Visible                 bool
	Minimized               bool
	Maximized               bool
	Caption                 string
	MouseCursorState        int32
	BorderStyle             int32
	InnerWidth, InnerHeight int32
	FrameInsets             [4]int32 // left, top, right, bottom non-client extents
	ZoomNumer, ZoomDenom    int32
	FitToClient             bool
	Left, Top               int32
	LayerLeft, LayerTop     int32
	ResizePending           bool
	ResizeGeneration        uint64
	DrawDevice              *DrawDevice
	DisableResize           bool
	DisableMove             bool
	EnableNCMouseEvent      bool
	ExSystemMenu            tjs.Value // Script menu description retained by WindowEx.
	Closed                  bool    // Close has been requested or the instance invalidated.
	Modal                   bool    // Headless modal state; no platform modal loop is run.
	ExEvents                [4]bool // onResizing, onMoving, onMove, onNcMouseMove
	ExEventRegistered       bool
	EnableWinMsgHook         bool

	normalBounds      *[4]int32
	restoreMaximized  bool
	fullScreen        *windowedBounds
	registeredObjects []tjs.Value
	invalidating      bool
	primaryLayer      LayerID
	focusedLayer      LayerID
	mouseLayer        LayerID
	mouseCapture      [5]LayerID
	modalLayers       []LayerID
	focusChanging     bool
	hitTestWork       bool
}

// Window is the concise name used by host integrations.
type Window = WindowState

func NewWindow() *WindowState {
	return &WindowState{Constructed: true, BorderStyle: 2, InnerWidth: 10, InnerHeight: 10, ZoomNumer: 1, ZoomDenom: 1}
}
func NewWindowState() *WindowState { return NewWindow() }

func (w *WindowState) ensureDefaults() {
	if w.ZoomNumer == 0 && w.ZoomDenom == 0 {
		w.ZoomNumer, w.ZoomDenom = 1, 1
	}
	if w.BorderStyle == 0 && w.InnerWidth == 0 && w.InnerHeight == 0 && !w.Constructed {
		w.BorderStyle, w.InnerWidth, w.InnerHeight = 2, 10, 10
	}
}

func (w *WindowState) IsFullScreen() bool { return w != nil && w.fullScreen != nil }
func (w *WindowState) FullScreen() bool   { return w.IsFullScreen() }
func (w *WindowState) ClientRect() [4]int32 {
	insets := w.effectiveInsets()
	return [4]int32{satAdd(w.Left, insets[0]), satAdd(w.Top, insets[1]), w.InnerWidth, w.InnerHeight}
}
func (w *WindowState) WindowRect() [4]int32 {
	insets := w.effectiveInsets()
	return [4]int32{w.Left, w.Top, satAdd(satAdd(w.InnerWidth, insets[0]), insets[2]), satAdd(satAdd(w.InnerHeight, insets[1]), insets[3])}
}
func (w *WindowState) NormalRect() [4]int32 {
	if w.normalBounds != nil {
		return *w.normalBounds
	}
	return w.WindowRect()
}
func (w *WindowState) Width() int32  { return w.WindowRect()[2] }
func (w *WindowState) Height() int32 { return w.WindowRect()[3] }

func (w *WindowState) effectiveInsets() [4]int32 {
	if w.IsFullScreen() || w.BorderStyle == 0 {
		return [4]int32{}
	}
	return w.FrameInsets
}

func (w *WindowState) ApplyShowCommand(command ShowCommand, work [4]int32) {
	w.Closed = false
	switch command {
	case ShowMaximize:
		if !w.Maximized {
			if w.normalBounds == nil {
				r := w.WindowRect()
				w.normalBounds = &r
			}
			w.Left, w.Top = work[0], work[1]
			w.setOuterSize(work[2], work[3])
		}
		w.Minimized, w.Maximized, w.Visible = false, true, true
	case ShowMinimize:
		if !w.Minimized {
			if w.normalBounds == nil {
				r := w.WindowRect()
				w.normalBounds = &r
			}
			w.restoreMaximized = w.Maximized
			w.Minimized, w.Maximized, w.Visible = true, false, true
		}
	case ShowRestore:
		if w.Minimized && w.restoreMaximized {
			w.ApplyShowCommand(ShowMaximize, work)
		} else {
			if w.normalBounds != nil {
				r := *w.normalBounds
				w.normalBounds = nil
				w.Left, w.Top = r[0], r[1]
				w.setOuterSize(r[2], r[3])
			}
			w.Minimized, w.Maximized, w.Visible = false, false, true
		}
		w.restoreMaximized = false
	}
}

// Minimize, Maximize and Restore are named convenience forms of
// ApplyShowCommand for host adapters.
func (w *WindowState) Minimize(work [4]int32) { w.ApplyShowCommand(ShowMinimize, work) }
func (w *WindowState) Maximize(work [4]int32) { w.ApplyShowCommand(ShowMaximize, work) }
func (w *WindowState) Restore(work [4]int32)  { w.ApplyShowCommand(ShowRestore, work) }

func (w *WindowState) SetVisible(value bool) {
	w.Visible = value
	if value {
		w.Closed = false
	} else {
		w.Modal = false
	}
}
func (w *WindowState) Close() {
	w.Visible = false
	w.Minimized = false
	w.Closed = true
	w.Modal = false
}
func (w *WindowState) ShowModal() {
	w.Visible = true
	w.Minimized = false
	w.Closed = false
	w.Modal = true
}
func (w *WindowState) SetCaption(value string)        { w.Caption = value }
func (w *WindowState) SetBorderStyle(style int32)     { w.BorderStyle = style }
func (w *WindowState) SetPos(left, top int32)         { w.Left, w.Top = left, top }
func (w *WindowState) SetLayerPos(left, top int32)    { w.LayerLeft, w.LayerTop = left, top }
func (w *WindowState) SetFrameInsets(insets [4]int32) { w.FrameInsets = insets }
func (w *WindowState) SetFitToClient(value bool)      { w.FitToClient = value }

func validDimension(value int32) error {
	if value < 0 {
		return fmt.Errorf("%w: negative window dimension", ErrInvalidWindow)
	}
	if value > 32768 {
		return fmt.Errorf("%w: window dimension exceeds surface limit", ErrUnsupported)
	}
	return nil
}
func (w *WindowState) SetInnerSize(width, height int32) error {
	if err := validDimension(width); err != nil {
		return err
	}
	if err := validDimension(height); err != nil {
		return err
	}
	if width != w.InnerWidth || height != w.InnerHeight {
		w.InnerWidth, w.InnerHeight, w.ResizePending = width, height, true
		w.ResizeGeneration++
	}
	return nil
}
func (w *WindowState) SetSize(width, height int32) error {
	if err := validDimension(width); err != nil {
		return err
	}
	if err := validDimension(height); err != nil {
		return err
	}
	insets := w.effectiveInsets()
	return w.SetInnerSize(subOuter(width, insets[0], insets[2]), subOuter(height, insets[1], insets[3]))
}
func (w *WindowState) SetWidth(width int32) error   { return w.SetSize(width, w.Height()) }
func (w *WindowState) SetHeight(height int32) error { return w.SetSize(w.Width(), height) }
func (w *WindowState) ConsumeResize() bool {
	changed := w.ResizePending
	w.ResizePending = false
	return changed
}

func (w *WindowState) SetZoom(numer, denom int32) error {
	a, b := int64(numer), int64(denom)
	for b != 0 {
		a, b = b, a%b
	}
	if a == 0 {
		return fmt.Errorf("%w: window zoom ratio is undefined", ErrInvalidWindow)
	}
	w.ZoomNumer, w.ZoomDenom = int32(int64(numer)/a), int32(int64(denom)/a)
	return nil
}
func (w *WindowState) SetZoomNumer(value int32) error { return w.SetZoom(value, w.ZoomDenom) }
func (w *WindowState) SetZoomDenom(value int32) error { return w.SetZoom(w.ZoomNumer, value) }

// EnterFullScreen stores windowed state and scales the logical canvas to fit
// the selected display without stretching. Ratios within 3.4% of 1:1 remain
// unscaled, matching Kirikiri's legacy fullscreen behavior.
func (w *WindowState) EnterFullScreen(screen [4]int32) error {
	if w.IsFullScreen() {
		return nil
	}
	if err := validateDisplayRect(screen); err != nil {
		return fmt.Errorf("%w: invalid full-screen display size", ErrInvalidWindow)
	}
	width, height := max32(1, w.InnerWidth), max32(1, w.InnerHeight)
	sw, sh := screen[2], screen[3]
	scale := [2]int32{sw, width}
	if int64(sw)*int64(height) >= int64(sh)*int64(width) {
		scale = [2]int32{sh, height}
	}
	if float64(scale[0])/float64(scale[1]) > 1 && float64(scale[0])/float64(scale[1]) < 1.034 {
		scale = [2]int32{1, 1}
	}
	targetWidth := int32(int64(width) * int64(scale[0]) / int64(scale[1]))
	targetHeight := int32(int64(height) * int64(scale[0]) / int64(scale[1]))
	w.fullScreen = &windowedBounds{left: w.Left, top: w.Top, width: w.InnerWidth, height: w.InnerHeight, scale: scale, origin: [2]int32{(sw - targetWidth) / 2, (sh - targetHeight) / 2}}
	w.Left, w.Top = screen[0], screen[1]
	w.setSize(sw, sh)
	w.Visible, w.Minimized = true, false
	return nil
}
func (w *WindowState) LeaveFullScreen() {
	if w.fullScreen == nil {
		return
	}
	saved := *w.fullScreen
	w.fullScreen = nil
	w.Left, w.Top = saved.left, saved.top
	w.setSize(saved.width, saved.height)
	w.Visible = true
}
func (w *WindowState) SetFullScreen(enabled bool, display *DisplayState) error {
	if enabled {
		if display == nil {
			return fmt.Errorf("%w: no full-screen display is available", ErrInvalidDisplay)
		}
		monitor, ok := display.DisplayForRect(w.WindowRect(), true)
		if !ok {
			return fmt.Errorf("%w: no full-screen display is available", ErrInvalidDisplay)
		}
		return w.EnterFullScreen(monitor.Bounds)
	}
	w.LeaveFullScreen()
	return nil
}

// DrawRect returns the destination rectangle in physical client pixels for a
// logical layer of width by height. Layer offsets are intentionally scaled
// using truncation toward zero; destination dimensions use rounded MulDiv.
func (w *WindowState) DrawRect(width, height int32) [4]int32 {
	if w.FitToClient {
		pw, ph := int64(max32(1, width)), int64(max32(1, height))
		cw, ch := int64(max32(1, w.InnerWidth)), int64(max32(1, w.InnerHeight))
		var dw, dh int64
		if cw*ph <= ch*pw {
			dw, dh = cw, max64i(1, cw*ph/pw)
		} else {
			dw, dh = max64i(1, ch*pw/ph), ch
		}
		return [4]int32{satAdd(int32((cw-dw)/2), scaledOffset(w.LayerLeft, int32(dw), int32(pw))), satAdd(int32((ch-dh)/2), scaledOffset(w.LayerTop, int32(dh), int32(ph))), int32(dw), int32(dh)}
	}
	scale, origin := [2]int32{w.ZoomNumer, w.ZoomDenom}, [2]int32{}
	if w.fullScreen != nil {
		scale, origin = w.fullScreen.scale, w.fullScreen.origin
	}
	return [4]int32{satAdd(origin[0], scaledOffset(w.LayerLeft, scale[0], scale[1])), satAdd(origin[1], scaledOffset(w.LayerTop, scale[0], scale[1])), max32(1, mulDiv(width, scale[0], scale[1])), max32(1, mulDiv(height, scale[0], scale[1]))}
}

// FrameMetadata is the stable handoff consumed by an Ebitengine presenter.
type FrameMetadata struct {
	Width, Height                      int32
	DrawRect                           [4]int32
	ResizePending                      bool
	ResizeGeneration                   uint64
	DrawDeviceID, DrawDeviceGeneration uint64
}

func (w *WindowState) FrameInfo(layerWidth, layerHeight int32) FrameMetadata {
	result := FrameMetadata{Width: layerWidth, Height: layerHeight, DrawRect: w.DrawRect(layerWidth, layerHeight), ResizePending: w.ResizePending, ResizeGeneration: w.ResizeGeneration}
	if w.DrawDevice != nil {
		result.DrawDeviceID, result.DrawDeviceGeneration = w.DrawDevice.ID, w.DrawDevice.Generation
	}
	return result
}
func (w *WindowState) DrawDeviceEpoch() (id, generation uint64, ok bool) {
	if w.DrawDevice == nil {
		return 0, 0, false
	}
	return w.DrawDevice.ID, w.DrawDevice.Generation, true
}
func (w *WindowState) SetDrawDevice(device *DrawDevice) { w.DrawDevice = device }

// RecreateDrawDevice advances the current device epoch. A nil device is a
// valid headless state and returns false.
func (w *WindowState) RecreateDrawDevice() bool {
	if w.DrawDevice == nil {
		return false
	}
	w.DrawDevice.Recreate()
	return true
}
func (w *WindowState) DrawDeviceGeneration() uint64 {
	if w.DrawDevice == nil {
		return 0
	}
	return w.DrawDevice.Generation
}

type WindowFrameMetadata = FrameMetadata

func (w *WindowState) setSize(width, height int32) {
	if width != w.InnerWidth || height != w.InnerHeight {
		w.InnerWidth, w.InnerHeight, w.ResizePending = width, height, true
		w.ResizeGeneration++
	}
}
func (w *WindowState) setOuterSize(width, height int32) {
	insets := w.effectiveInsets()
	w.setSize(subOuter(width, insets[0], insets[2]), subOuter(height, insets[1], insets[3]))
}
func max32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}
func max64i(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
func subOuter(value, first, second int32) int32 {
	result := int64(value) - int64(first) - int64(second)
	if result < 0 {
		return 0
	}
	if result > maxInt32Value {
		return int32(maxInt32Value)
	}
	return int32(result)
}
func satAdd(a, b int32) int32 {
	v := int64(a) + int64(b)
	if v > maxInt32Value {
		return int32(maxInt32Value)
	}
	if v < minInt32Value {
		return int32(minInt32Value)
	}
	return int32(v)
}
func scaledOffset(value, numer, denom int32) int32 {
	if denom == 0 {
		return 0
	}
	return satInt64(int64(value) * int64(numer) / int64(denom))
}
func mulDiv(value, numer, denom int32) int32 {
	if denom == 0 {
		return -1
	}
	product, divisor := int64(value)*int64(numer), int64(denom)
	absDiv := divisor
	if absDiv < 0 {
		absDiv = -absDiv
	}
	absProduct := product
	if absProduct < 0 {
		absProduct = -absProduct
	}
	rounded := (absProduct + absDiv/2) / absDiv
	if (product < 0) != (divisor < 0) {
		rounded = -rounded
	}
	if rounded > maxInt32Value || rounded < minInt32Value {
		return -1
	}
	return int32(rounded)
}
func satInt64(v int64) int32 {
	if v > maxInt32Value {
		return int32(maxInt32Value)
	}
	if v < minInt32Value {
		return int32(minInt32Value)
	}
	return int32(v)
}
