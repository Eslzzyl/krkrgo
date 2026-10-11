package runtime

import "errors"

const (
	maxInt32Value int64 = 1<<31 - 1
	minInt32Value int64 = -1 << 31
)

var (
	ErrInvalidDisplay = errors.New("invalid display")
	ErrInvalidWindow  = errors.New("invalid window")
	ErrUnsupported    = errors.New("unsupported runtime operation")
)

// Monitor describes a physical display. Rectangles use x, y, width, height
// coordinates in the host desktop space.
type Monitor struct {
	Name    string
	Primary bool
	Bounds  [4]int32
	Work    [4]int32
}

// DisplayState owns the host display topology used by headless window state.
// A zero-value DisplayState is valid and uses a 1280x720 headless display.
type DisplayState struct {
	ScreenWidth  uint32
	ScreenHeight uint32
	Displays     []Monitor
	Windows      map[uint64]*WindowState
	MainWindow   uint64
	nextWindowID uint64
}

// Display is kept as the concise name used by host integrations.
type Display = DisplayState

func NewDisplay() *DisplayState {
	return &DisplayState{ScreenWidth: 1280, ScreenHeight: 720, Windows: make(map[uint64]*WindowState), nextWindowID: 1}
}

func NewDisplayState() *DisplayState { return NewDisplay() }

func (d *DisplayState) ensureInit() {
	if d.ScreenWidth == 0 {
		d.ScreenWidth = 1280
	}
	if d.ScreenHeight == 0 {
		d.ScreenHeight = 720
	}
	if d.Windows == nil {
		d.Windows = make(map[uint64]*WindowState)
	}
	if d.nextWindowID == 0 {
		d.nextWindowID = 1
	}
}

// SetScreenSize sets the dimensions used by the headless fallback monitor.
func (d *DisplayState) SetScreenSize(width, height uint32) error {
	if width == 0 || height == 0 || width > 32768 || height > 32768 {
		return ErrInvalidDisplay
	}
	d.ScreenWidth, d.ScreenHeight = width, height
	return nil
}

// SetDisplays replaces the host topology after validating its geometry.
func (d *DisplayState) SetDisplays(displays []Monitor) error {
	if len(displays) == 0 || len(displays) > 64 {
		return ErrInvalidDisplay
	}
	primary := 0
	for _, monitor := range displays {
		if monitor.Primary {
			primary++
		}
		for _, rect := range [][4]int32{monitor.Bounds, monitor.Work} {
			if err := validateDisplayRect(rect); err != nil {
				return err
			}
		}
		if intersection(monitor.Bounds, monitor.Work) != monitor.Work {
			return ErrInvalidDisplay
		}
	}
	if primary != 1 {
		return ErrInvalidDisplay
	}
	d.Displays = append(d.Displays[:0], displays...)
	return nil
}

func validateDisplayRect(rect [4]int32) error {
	if rect[2] <= 0 || rect[3] <= 0 || rect[2] > 32768 || rect[3] > 32768 {
		return ErrInvalidDisplay
	}
	if int64(rect[0])+int64(rect[2]) < minInt32Value || int64(rect[0])+int64(rect[2]) > maxInt32Value || int64(rect[1])+int64(rect[3]) < minInt32Value || int64(rect[1])+int64(rect[3]) > maxInt32Value {
		return ErrInvalidDisplay
	}
	return nil
}

func rectEdges(rect [4]int32) [4]int64 {
	return [4]int64{int64(rect[0]), int64(rect[1]), int64(rect[0]) + int64(rect[2]), int64(rect[1]) + int64(rect[3])}
}

func intersection(a, b [4]int32) [4]int32 {
	xa, ya, ra, da := rectEdges(a)[0], rectEdges(a)[1], rectEdges(a)[2], rectEdges(a)[3]
	xb, yb, rb, db := rectEdges(b)[0], rectEdges(b)[1], rectEdges(b)[2], rectEdges(b)[3]
	x, y, r, bottom := max64(xa, xb), max64(ya, yb), min64(ra, rb), min64(da, db)
	if x >= r || y >= bottom {
		return [4]int32{}
	}
	return [4]int32{int32(x), int32(y), int32(r - x), int32(bottom - y)}
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func rectDistance(a, b [4]int32) uint64 {
	aa, bb := rectEdges(a), rectEdges(b)
	x := max64(max64(aa[0]-bb[2], bb[0]-aa[2]), 0)
	y := max64(max64(aa[1]-bb[3], bb[1]-aa[3]), 0)
	sx, sy := distanceSquare(uint64(x)), distanceSquare(uint64(y))
	if ^uint64(0)-sx < sy {
		return ^uint64(0)
	}
	return sx + sy
}
func distanceSquare(value uint64) uint64 {
	const maxUint64 = ^uint64(0)
	if value != 0 && value > maxUint64/value {
		return maxUint64
	}
	return value * value
}

func (d *DisplayState) PrimaryDisplay() Monitor {
	for _, monitor := range d.Displays {
		if monitor.Primary {
			return monitor
		}
	}
	d.ensureInit()
	bounds := [4]int32{0, 0, int32(min32(d.ScreenWidth)), int32(min32(d.ScreenHeight))}
	return Monitor{Name: "Headless", Primary: true, Bounds: bounds, Work: bounds}
}

func min32(v uint32) uint32 {
	if uint64(v) > uint64(maxInt32Value) {
		return uint32(maxInt32Value)
	}
	return v
}

func (d *DisplayState) displayList() []Monitor {
	if len(d.Displays) == 0 {
		return []Monitor{d.PrimaryDisplay()}
	}
	return append([]Monitor(nil), d.Displays...)
}

func (d *DisplayState) DisplayList() []Monitor { return d.displayList() }

// DisplayForRect selects the display with the greatest overlap. If nearest is
// true and there is no overlap, the closest display is selected.
func (d *DisplayState) DisplayForRect(rect [4]int32, nearest bool) (Monitor, bool) {
	if rect[2] <= 0 {
		rect[2] = 1
	}
	if rect[3] <= 0 {
		rect[3] = 1
	}
	var best Monitor
	found := false
	area := int64(0)
	list := d.displayList()
	for _, monitor := range list {
		overlap := intersection(rect, monitor.Bounds)
		size := int64(overlap[2]) * int64(overlap[3])
		if size > area {
			area, best, found = size, monitor, true
		}
	}
	if found || !nearest {
		return best, found
	}
	for i, monitor := range list {
		if i == 0 || rectDistance(rect, monitor.Bounds) < rectDistance(rect, best.Bounds) {
			best = monitor
		}
	}
	return best, len(list) != 0
}

func (d *DisplayState) ApplicationDisplay() Monitor {
	if d.MainWindow != 0 && d.Windows != nil {
		if window := d.Windows[d.MainWindow]; window != nil {
			if monitor, ok := d.DisplayForRect(window.WindowRect(), false); ok {
				return monitor
			}
		}
	}
	return d.PrimaryDisplay()
}

func (d *DisplayState) DesktopBounds() [4]int32 { return d.ApplicationDisplay().Work }

// RegisterWindow adds a window to the display state and returns its stable id.
func (d *DisplayState) RegisterWindow(window *WindowState) uint64 {
	d.ensureInit()
	id := d.nextWindowID
	d.nextWindowID++
	d.Windows[id] = window
	if d.MainWindow == 0 {
		d.MainWindow = id
	}
	return id
}

func (d *DisplayState) MonitorInfo(rect [4]int32, nearest bool) (Monitor, bool) {
	return d.DisplayForRect(rect, nearest)
}

// MonitorInfoForPoint uses a one-pixel query, matching MonitorFromPoint.
func (d *DisplayState) MonitorInfoForPoint(x, y int32, nearest bool) (Monitor, bool) {
	return d.DisplayForRect([4]int32{x, y, 1, 1}, nearest)
}

// MonitorsIntersect returns displays intersecting rect and their clipped area.
func (d *DisplayState) MonitorsIntersect(rect [4]int32) []MonitorIntersection {
	var result []MonitorIntersection
	for _, monitor := range d.displayList() {
		if clip := intersection(rect, monitor.Bounds); clip[2] > 0 && clip[3] > 0 {
			result = append(result, MonitorIntersection{Monitor: monitor, Intersect: clip})
		}
	}
	return result
}

type MonitorIntersection struct {
	Monitor   Monitor
	Intersect [4]int32
}

// SetWindowFullScreen applies fullscreen to one registered window. Only one
// window retains fullscreen state, matching Kirikiri's global presenter.
func (d *DisplayState) SetWindowFullScreen(id uint64, enabled bool) error {
	w := d.Windows[id]
	if w == nil || !w.Constructed {
		return ErrInvalidWindow
	}
	if !enabled {
		w.LeaveFullScreen()
		return nil
	}
	monitor, ok := d.DisplayForRect(w.WindowRect(), true)
	if !ok {
		return ErrInvalidDisplay
	}
	for otherID, other := range d.Windows {
		if otherID != id && other != nil {
			other.LeaveFullScreen()
		}
	}
	return w.EnterFullScreen(monitor.Bounds)
}
