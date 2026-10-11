package runtime

import "testing"

func TestDisplayHeadlessAndMonitorSelection(t *testing.T) {
	d := NewDisplay()
	got := d.PrimaryDisplay()
	if got.Name != "Headless" || got.Bounds != [4]int32{0, 0, 1280, 720} || got.Work != got.Bounds {
		t.Fatalf("headless display = %#v", got)
	}
	displays := []Monitor{
		{Name: "left", Primary: false, Bounds: [4]int32{-800, 0, 800, 600}, Work: [4]int32{-800, 20, 800, 580}},
		{Name: "main", Primary: true, Bounds: [4]int32{0, 0, 1280, 720}, Work: [4]int32{0, 0, 1280, 700}},
	}
	if err := d.SetDisplays(displays); err != nil {
		t.Fatal(err)
	}
	if got, ok := d.DisplayForRect([4]int32{-700, 40, 320, 240}, false); !ok || got.Name != "left" {
		t.Fatalf("overlap display = %#v, %v", got, ok)
	}
	if got, ok := d.MonitorInfoForPoint(2000, 100, false); ok || got.Name != "" {
		t.Fatalf("non-overlap nearest=false = %#v, %v", got, ok)
	}
	if got, ok := d.MonitorInfoForPoint(2000, 100, true); !ok || got.Name != "main" {
		t.Fatalf("nearest display = %#v, %v", got, ok)
	}
}

func TestWindowBoundsShowCommandsAndResizeMetadata(t *testing.T) {
	w := NewWindow()
	w.SetFrameInsets([4]int32{4, 24, 4, 4})
	w.SetPos(30, 40)
	if err := w.SetSize(320, 240); err != nil {
		t.Fatal(err)
	}
	if got := w.ClientRect(); got != [4]int32{34, 64, 312, 212} {
		t.Fatalf("client rect = %v", got)
	}
	if got := w.WindowRect(); got != [4]int32{30, 40, 320, 240} {
		t.Fatalf("window rect = %v", got)
	}
	if !w.ResizePending || w.ResizeGeneration == 0 {
		t.Fatalf("resize metadata = %#v", w.FrameInfo(312, 212))
	}
	w.ConsumeResize()
	w.ApplyShowCommand(ShowMaximize, [4]int32{0, 0, 1280, 700})
	if !w.Maximized || w.Width() != 1280 || w.Height() != 700 {
		t.Fatalf("maximized state = %#v", w)
	}
	w.ApplyShowCommand(ShowMinimize, [4]int32{0, 0, 1280, 700})
	if !w.Minimized || w.Maximized {
		t.Fatalf("minimized state = %#v", w)
	}
	w.ApplyShowCommand(ShowRestore, [4]int32{0, 0, 1280, 700})
	// Restoring a minimized maximized window returns to the maximized state,
	// matching Win32/Kirikiri's restore-maximized behavior.
	if w.Minimized || !w.Maximized || w.WindowRect() != [4]int32{0, 0, 1280, 700} {
		t.Fatalf("restored maximized state = %#v", w)
	}
	w.ApplyShowCommand(ShowRestore, [4]int32{0, 0, 1280, 700})
	if w.Minimized || w.Maximized || w.WindowRect() != [4]int32{30, 40, 320, 240} {
		t.Fatalf("restored normal state = %#v", w)
	}
}

func TestWindowFullscreenFitQuakeAndDrawDevice(t *testing.T) {
	w := NewWindow()
	if err := w.SetInnerSize(640, 480); err != nil {
		t.Fatal(err)
	}
	if err := w.SetZoom(3, 2); err != nil {
		t.Fatal(err)
	}
	if got := w.DrawRect(640, 480); got != [4]int32{0, 0, 960, 720} {
		t.Fatalf("zoom draw rect = %v", got)
	}
	if err := w.EnterFullScreen([4]int32{0, 0, 1280, 720}); err != nil {
		t.Fatal(err)
	}
	if got := w.DrawRect(640, 480); got != [4]int32{160, 0, 960, 720} {
		t.Fatalf("fullscreen draw rect = %v", got)
	}
	w.SetLayerPos(2, -1)
	if got := w.DrawRect(640, 480); got != [4]int32{163, -1, 960, 720} {
		t.Fatalf("quake draw rect = %v", got)
	}
	w.LeaveFullScreen()
	if got := w.DrawRect(640, 480); got != [4]int32{3, -1, 960, 720} {
		t.Fatalf("windowed draw rect = %v", got)
	}
	w.SetFitToClient(true)
	if err := w.SetInnerSize(300, 100); err != nil {
		t.Fatal(err)
	}
	if got := w.DrawRect(640, 480); got != [4]int32{83, 0, 133, 100} {
		t.Fatalf("fit draw rect = %v", got)
	}
	device := NewDrawDevice(7)
	w.SetDrawDevice(device)
	device.Recreate()
	info := w.FrameInfo(640, 480)
	if info.DrawDeviceID != 7 || info.DrawDeviceGeneration != 1 || info.DrawRect != w.DrawRect(640, 480) {
		t.Fatalf("frame info = %#v", info)
	}
}
