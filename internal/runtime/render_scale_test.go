package runtime

import (
	"testing"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
)

func TestLayerScaleAllocatesDevicePixelBlocks(t *testing.T) {
	layer := NewLayer(1)
	layer.SetScale(2)
	if err := layer.SetRGBA(2, 2, []byte{
		255, 0, 0, 255, 0, 255, 0, 255,
		0, 0, 255, 255, 255, 255, 0, 255,
	}); err != nil {
		t.Fatal(err)
	}
	if layer.ImageWidth != 2 || layer.ImageHeight != 2 {
		t.Fatalf("logical image dimensions = %dx%d", layer.ImageWidth, layer.ImageHeight)
	}
	if got, want := len(layer.RGBA), 4*4*4; got != want {
		t.Fatalf("device buffer length = %d, want %d", got, want)
	}
	width := int(layer.deviceWidth())
	for y := range 4 {
		for x := range 4 {
			logicalX, logicalY := x/2, y/2
			source := (logicalY*2 + logicalX) * 4
			got := layer.RGBA[(y*width+x)*4 : (y*width+x)*4+4]
			want := []byte{
				255, 0, 0, 255, 0, 255, 0, 255,
				0, 0, 255, 255, 255, 255, 0, 255,
			}[source : source+4]
			if string(got) != string(want) {
				t.Fatalf("pixel (%d,%d) = %v, want %v", x, y, got, want)
			}
		}
	}
	if err := fillLayerPixelBlock(layer, 1, 1, [4]byte{1, 2, 3, 4}); err != nil {
		t.Fatal(err)
	}
	for y := 2; y < 4; y++ {
		for x := 2; x < 4; x++ {
			if got := layer.RGBA[(y*width+x)*4 : (y*width+x)*4+4]; string(got) != string([]byte{1, 2, 3, 4}) {
				t.Fatalf("block pixel (%d,%d) = %v", x, y, got)
			}
		}
	}
}

func TestCompositorScaleUsesDeviceFrame(t *testing.T) {
	services := DefaultServices()
	services.SetRenderScale(2)
	root := NewRootLayer(1, 2, 2)
	child := NewLayer(2)
	child.SetScale(2)
	if err := child.SetRGBA(2, 2, []byte{
		255, 0, 0, 255, 255, 0, 0, 255,
		255, 0, 0, 255, 255, 0, 0, 255,
	}); err != nil {
		t.Fatal(err)
	}
	child.Visible = true
	if err := root.AddChild(child); err != nil {
		t.Fatal(err)
	}
	services.SetRoot(root)
	frame, err := services.Compositor.Compose()
	if err != nil {
		t.Fatal(err)
	}
	if frame.Width != 4 || frame.Height != 4 {
		t.Fatalf("frame = %dx%d, want 4x4", frame.Width, frame.Height)
	}
	for y := range 4 {
		for x := range 4 {
			pixel := frame.RGBA[(y*4+x)*4 : (y*4+x)*4+4]
			if string(pixel) != string([]byte{255, 0, 0, 255}) {
				t.Fatalf("pixel (%d,%d) = %v", x, y, pixel)
			}
		}
	}
}

type scaleRecordingFace struct{ font.Face }

func TestDrawTextRequestsScaledFontSize(t *testing.T) {
	services := DefaultServices()
	services.SetRenderScale(2)
	book := NewFontBook()
	sizes := make([]float64, 0, 2)
	if err := book.RegisterFactory("ScaleFont", func(size float64) (FontFace, error) {
		sizes = append(sizes, size)
		return &scaleRecordingFace{Face: basicfont.Face7x13}, nil
	}); err != nil {
		t.Fatal(err)
	}
	services.SetFontBook(book)
	services.SetFontSystem(&FontSystem{Book: book, DefaultFont: "ScaleFont", DefaultSize: 12})
	session := NewSession(services, 1_000_000)
	defer session.Close()
	if _, err := session.Evaluate(`
		w = new Window();
		root = new Layer(w);
		root.setSize(32, 32);
		root.setImageSize(32, 32);
		root.visible = true;
		root.fillRect(0, 0, 32, 32, 0xff000000);
		root.drawText(1, 1, "A", 0xffffff);
	`); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, size := range sizes {
		if size == 24 {
			found = true
		}
	}
	if !found {
		t.Fatalf("font sizes requested = %v, want 24", sizes)
	}
	if got, want := len(session.Services.Root.RGBA), 64*64*4; got != want {
		t.Fatalf("root device buffer length = %d, want %d", got, want)
	}
}

func TestBitmapScaleAllocatesDeviceBuffer(t *testing.T) {
	services := DefaultServices()
	services.SetRenderScale(2)
	session := NewSession(services, 1_000_000)
	defer session.Close()
	if _, err := session.Evaluate(`
		b = new Bitmap(2, 2);
		b.setPixel(1, 1, 0xff0000);
	`); err != nil {
		t.Fatal(err)
	}
	var bitmap *Bitmap
	for _, candidate := range services.Bitmaps {
		bitmap = candidate
	}
	if bitmap == nil {
		t.Fatal("no bitmap registered")
	}
	if got, want := len(bitmap.RGBA), 4*4*4; got != want {
		t.Fatalf("device buffer length = %d, want %d", got, want)
	}
	width := int(bitmap.deviceWidth())
	for y := 2; y < 4; y++ {
		for x := 2; x < 4; x++ {
			pixel := bitmap.RGBA[(y*width+x)*4 : (y*width+x)*4+4]
			if string(pixel) != string([]byte{255, 0, 0, 0}) {
				t.Fatalf("block pixel (%d,%d) = %v", x, y, pixel)
			}
		}
	}
}

func TestSetRenderScaleResamplesBitmaps(t *testing.T) {
	services := DefaultServices()
	bitmap, err := newBitmapScaled(2, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	bitmap.RGBA = []byte{
		255, 0, 0, 255, 0, 255, 0, 255,
		0, 0, 255, 255, 255, 255, 0, 255,
	}
	services.ensureInit()
	services.Bitmaps[1] = bitmap
	services.SetRenderScale(2)
	if bitmap.Scale != 2 {
		t.Fatalf("bitmap scale = %d, want 2", bitmap.Scale)
	}
	if got, want := len(bitmap.RGBA), 4*4*4; got != want {
		t.Fatalf("device buffer length = %d, want %d", got, want)
	}
}

func TestRenderPlanCarriesAndConsumesDirtyRect(t *testing.T) {
	services := DefaultServices()
	root := NewRootLayer(1, 4, 4)
	child := NewLayer(2)
	if err := child.SetRGBA(4, 4, make([]byte, 4*4*4)); err != nil {
		t.Fatal(err)
	}
	if err := root.AddChild(child); err != nil {
		t.Fatal(err)
	}
	services.SetRoot(root)
	if _, err := services.CaptureRenderPlan(); err != nil {
		t.Fatal(err)
	}
	child.markImageDirtyRect(Rect{X: 1, Y: 2, Width: 2, Height: 1})
	plan, err := services.CaptureRenderPlan()
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Nodes[1].ImageDirtyValid || plan.Nodes[1].ImageDirty != (Rect{X: 1, Y: 2, Width: 2, Height: 1}) {
		t.Fatalf("dirty rect = %+v valid=%t", plan.Nodes[1].ImageDirty, plan.Nodes[1].ImageDirtyValid)
	}
	next, err := services.CaptureRenderPlan()
	if err != nil {
		t.Fatal(err)
	}
	if next.Nodes[1].ImageDirtyValid {
		t.Fatal("dirty rect was not consumed by the previous capture")
	}
}

func TestParallelRowsCoversEveryRowOnce(t *testing.T) {
	const rows = 97
	seen := make([]int, rows)
	parallelRows(rows, 1, func(start, end int) {
		for row := start; row < end; row++ {
			seen[row]++
		}
	})
	for row, count := range seen {
		if count != 1 {
			t.Fatalf("row %d visited %d times", row, count)
		}
	}
}

func TestRowAllOpaqueHandlesWordAndTailPixels(t *testing.T) {
	opaque := []byte{1, 2, 3, 255, 4, 5, 6, 255, 7, 8, 9, 255}
	if !rowAllOpaque(opaque) {
		t.Fatal("opaque row rejected")
	}
	opaque[len(opaque)-1] = 254
	if rowAllOpaque(opaque) {
		t.Fatal("translucent tail pixel accepted")
	}
}
