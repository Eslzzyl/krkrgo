package host

import (
	"testing"
	"time"

	runtime "github.com/eslzzyl/krkrgo/internal/runtime"
)

func TestLayoutTracksRuntimeCanvasBeforeAndAfterFrame(t *testing.T) {
	services := runtime.DefaultServices()
	if err := services.SetFrameSize(320, 200); err != nil {
		t.Fatal(err)
	}
	game := NewGame(runtime.NewSession(services, 32))
	defer game.Session().Close()
	w, h := game.Layout(1, 1)
	if w != 320 || h != 200 {
		t.Fatalf("initial layout = %d,%d", w, h)
	}
	game.imageWidth, game.imageHeight = 640, 360
	w, h = game.Layout(1, 1)
	if w != 640 || h != 360 {
		t.Fatalf("captured layout = %d,%d", w, h)
	}
}

func TestPresentationBufferReusesAndUpdatesFrameStorage(t *testing.T) {
	game := &Game{}
	first := runtime.FrameSnapshot{Width: 2, Height: 1, RGBA: []byte{1, 2, 3, 255, 5, 6, 7, 255}}
	if err := game.updatePresentation(first); err != nil {
		t.Fatal(err)
	}
	if game.imageWidth != 2 || game.imageHeight != 1 || string(game.pixels) != string(first.RGBA) {
		t.Fatalf("first frame was not copied: %#v", game)
	}
	image := game.image
	second := runtime.FrameSnapshot{Width: 2, Height: 1, RGBA: []byte{8, 7, 6, 255, 4, 3, 2, 255}}
	if err := game.updatePresentation(second); err != nil {
		t.Fatal(err)
	}
	if game.image != image || string(game.pixels) != string(second.RGBA) {
		t.Fatalf("same-size frame did not reuse/update presentation buffer")
	}
}

func TestPresentationOpaqueFullFrameUsesBulkPixelsAndCountsChanges(t *testing.T) {
	game := &Game{renderStats: renderStats{enabled: true}}
	first := runtime.FrameSnapshot{Width: 2, Height: 2, RGBA: []byte{
		10, 20, 30, 255, 40, 50, 60, 255,
		70, 80, 90, 255, 100, 110, 120, 255,
	}}
	if err := game.updatePresentation(first); err != nil {
		t.Fatal(err)
	}
	next := runtime.FrameSnapshot{Width: 2, Height: 2, RGBA: []byte{
		1, 2, 3, 255, 4, 5, 6, 255,
		7, 8, 9, 255, 10, 11, 12, 255,
	}}
	if err := game.updatePresentation(next); err != nil {
		t.Fatal(err)
	}
	if !game.presentationOpaque || game.presentationChanges != 4 {
		t.Fatalf("opaque diagnostics = opaque:%v changes:%d, want opaque:true changes:4", game.presentationOpaque, game.presentationChanges)
	}
	if string(game.pixels) != string(next.RGBA) {
		t.Fatalf("opaque full-frame pixels = %v, want %v", game.pixels, next.RGBA)
	}
	if len(game.uploadPixels) != 0 {
		t.Fatalf("full-frame upload used dirty buffer of length %d", len(game.uploadPixels))
	}
}

func TestPresentationPartialAlphaPreservesPremultipliedPixelEquivalence(t *testing.T) {
	game := &Game{}
	frame := runtime.FrameSnapshot{Width: 2, Height: 1, RGBA: []byte{
		200, 100, 50, 128,
		90, 80, 70, 0,
	}}
	if err := game.updatePresentation(frame); err != nil {
		t.Fatal(err)
	}
	want := []byte{100, 50, 25, 128, 0, 0, 0, 0}
	if game.presentationOpaque || string(game.pixels) != string(want) {
		t.Fatalf("partial-alpha presentation = opaque:%v pixels:%v, want opaque:false pixels:%v", game.presentationOpaque, game.pixels, want)
	}
}

func TestPresentationPremultipliesStraightAlpha(t *testing.T) {
	game := &Game{}
	frame := runtime.FrameSnapshot{Width: 1, Height: 1, RGBA: []byte{1, 2, 3, 128}}
	if err := game.updatePresentation(frame); err != nil {
		t.Fatal(err)
	}
	want := []byte{1, 1, 2, 128}
	if string(game.pixels) != string(want) {
		t.Fatalf("premultiplied pixels = %v, want %v", game.pixels, want)
	}
}
func TestPresentationResizesWindowToNativeFrameDimensions(t *testing.T) {
	var sizes [][2]int
	game := &Game{resizeWindow: func(width, height int) {
		sizes = append(sizes, [2]int{width, height})
	}}
	first := runtime.FrameSnapshot{Width: 640, Height: 480, RGBA: make([]byte, 640*480*4)}
	if err := game.updatePresentation(first); err != nil {
		t.Fatal(err)
	}
	second := runtime.FrameSnapshot{Width: 1024, Height: 768, RGBA: make([]byte, 1024*768*4)}
	if err := game.updatePresentation(second); err != nil {
		t.Fatal(err)
	}
	if len(sizes) != 2 || sizes[0] != [2]int{640, 480} || sizes[1] != [2]int{1024, 768} {
		t.Fatalf("window resize requests = %v, want native frame sizes [[640 480] [1024 768]]", sizes)
	}
}
func TestPresentationSkipsOnlyConfiguredInitialWindowResize(t *testing.T) {
	var sizes [][2]int
	game := NewGameWithInitialWindowSize(nil, 640, 480)
	game.resizeWindow = func(width, height int) {
		sizes = append(sizes, [2]int{width, height})
	}
	first := runtime.FrameSnapshot{Width: 640, Height: 480, RGBA: make([]byte, 640*480*4)}
	if err := game.updatePresentation(first); err != nil {
		t.Fatal(err)
	}
	if len(sizes) != 0 {
		t.Fatalf("initial matching size resize requests = %v, want none", sizes)
	}
	second := runtime.FrameSnapshot{Width: 800, Height: 600, RGBA: make([]byte, 800*600*4)}
	if err := game.updatePresentation(second); err != nil {
		t.Fatal(err)
	}
	if len(sizes) != 1 || sizes[0] != [2]int{800, 600} {
		t.Fatalf("resolution-change resize requests = %v, want [[800 600]]", sizes)
	}
}

func TestPresentationResizesWhenInitialFrameDiffersFromConfiguredSize(t *testing.T) {
	var sizes [][2]int
	game := NewGameWithInitialWindowSize(nil, 640, 480)
	game.resizeWindow = func(width, height int) {
		sizes = append(sizes, [2]int{width, height})
	}
	frame := runtime.FrameSnapshot{Width: 800, Height: 600, RGBA: make([]byte, 800*600*4)}
	if err := game.updatePresentation(frame); err != nil {
		t.Fatal(err)
	}
	if len(sizes) != 1 || sizes[0] != [2]int{800, 600} {
		t.Fatalf("initial mismatch resize requests = %v, want [[800 600]]", sizes)
	}
}

func TestPresentationBufferRejectsMalformedFrames(t *testing.T) {
	game := &Game{}
	if err := game.updatePresentation(runtime.FrameSnapshot{Width: 1, Height: 1, RGBA: []byte{1}}); err == nil {
		t.Fatal("accepted malformed RGBA frame")
	}
}

func TestPresentationUploadsOnlyChangedRegion(t *testing.T) {
	game := &Game{}
	frame := runtime.FrameSnapshot{Width: 4, Height: 4, RGBA: make([]byte, 4*4*4)}
	for i := 3; i < len(frame.RGBA); i += 4 {
		frame.RGBA[i] = 255
	}
	if err := game.updatePresentation(frame); err != nil {
		t.Fatal(err)
	}
	pixel := (2*4 + 1) * 4
	frame.RGBA[pixel], frame.RGBA[pixel+1], frame.RGBA[pixel+2] = 64, 128, 192
	if err := game.updatePresentation(frame); err != nil {
		t.Fatal(err)
	}
	want := []byte{64, 128, 192, 255}
	if string(game.uploadPixels) != string(want) {
		t.Fatalf("partial upload pixels = %v, want %v", game.uploadPixels, want)
	}
	if string(game.pixels[pixel:pixel+4]) != string(want) {
		t.Fatalf("presentation pixels = %v, want %v", game.pixels[pixel:pixel+4], want)
	}
}

func TestRenderStatsRecordFirstLifecycleAndUploadRequests(t *testing.T) {
	var stats renderStats
	start := time.Now()
	stats.recordFirstUpdate(start)
	stats.recordFirstUpdate(start.Add(time.Second))
	stats.recordDrawEntry(start.Add(25 * time.Millisecond))
	stats.recordDrawEntry(start.Add(time.Second))
	stats.recordSubImage(3 * time.Millisecond)
	stats.recordSubImage(5 * time.Millisecond)
	stats.recordWritePixels(7 * time.Millisecond)
	stats.recordWritePixels(9 * time.Millisecond)

	if stats.firstUpdateAt != start || stats.firstDrawAt != start.Add(25*time.Millisecond) {
		t.Fatalf("first lifecycle timestamps = %v, %v", stats.firstUpdateAt, stats.firstDrawAt)
	}
	if got := stats.firstDrawAfterUpdate(); got != 25*time.Millisecond {
		t.Fatalf("first draw after update = %s, want 25ms", got)
	}
	if stats.subImageCalls != 2 || stats.firstSubImage != 3*time.Millisecond {
		t.Fatalf("subimage diagnostics = calls:%d first:%s", stats.subImageCalls, stats.firstSubImage)
	}
	if stats.writePixelsCalls != 2 || stats.firstWritePixels != 7*time.Millisecond || stats.writePixelsTotal != 16*time.Millisecond {
		t.Fatalf("WritePixels diagnostics = calls:%d first:%s total:%s", stats.writePixelsCalls, stats.firstWritePixels, stats.writePixelsTotal)
	}
}

func BenchmarkUpdatePresentation720pOpaque(b *testing.B) {
	frame := runtime.FrameSnapshot{Width: 1280, Height: 720, RGBA: make([]byte, 1280*720*4)}
	for i := 0; i < len(frame.RGBA); i += 4 {
		frame.RGBA[i+0], frame.RGBA[i+1], frame.RGBA[i+2], frame.RGBA[i+3] = 32, 64, 96, 255
	}
	game := &Game{}
	if err := game.updatePresentation(frame); err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(frame.RGBA)))
	for b.Loop() {
		if err := game.updatePresentation(frame); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkUpdatePresentation720pSinglePixelChange(b *testing.B) {
	frame := runtime.FrameSnapshot{Width: 1280, Height: 720, RGBA: make([]byte, 1280*720*4)}
	for i := 3; i < len(frame.RGBA); i += 4 {
		frame.RGBA[i] = 255
	}
	game := &Game{}
	if err := game.updatePresentation(frame); err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(frame.RGBA)))
	pixel := (360*1280 + 640) * 4
	value := byte(1)
	for b.Loop() {
		frame.RGBA[pixel] = value
		value ^= 1
		if err := game.updatePresentation(frame); err != nil {
			b.Fatal(err)
		}
	}
}
