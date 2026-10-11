package runtime

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/eslzzyl/krkrgo/internal/assets"
)

func TestNativeLayerPiledCopyClipsAndCopiesMainAndMask(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	if _, err := session.executeSource("<layer-piled-copy-test>", `
	source = new Layer(); destination = new Layer();
	source.setImageSize(3, 1); destination.setImageSize(3, 1);
	source.setMainPixel(0, 0, 0x112233); source.setMaskPixel(0, 0, 10);
	source.setMainPixel(1, 0, 0x445566); source.setMaskPixel(1, 0, 20);
	source.setMainPixel(2, 0, 0x778899); source.setMaskPixel(2, 0, 30);
	destination.setMainPixel(0, 0, 0xaabbcc); destination.setMaskPixel(0, 0, 40);
	destination.setMainPixel(1, 0, 0x101112); destination.setMaskPixel(1, 0, 50);
	destination.setMainPixel(2, 0, 0x334455); destination.setMaskPixel(2, 0, 60);
	source.visible = false; source.opacity = 0;
	destination.setClip(1, 0, 1, 1);
	destination.face = dfMain;
	destination.piledCopy(0, 0, source, 0, 0, 3, 1);
	`, false); err != nil {
		t.Fatal(err)
	}
	read := func(source string) int64 {
		t.Helper()
		value, err := session.Evaluate(source)
		if err != nil {
			t.Fatalf("%s: %v", source, err)
		}
		got, err := value.Int()
		if err != nil {
			t.Fatalf("%s: %v", source, err)
		}
		return got
	}
	for _, tc := range []struct {
		expression string
		want       int64
	}{
		{`destination.getMainPixel(0, 0)`, 0xaabbcc},
		{`destination.getMaskPixel(0, 0)`, 40},
		{`destination.getMainPixel(1, 0)`, 0x445566},
		{`destination.getMaskPixel(1, 0)`, 20},
		{`destination.getMainPixel(2, 0)`, 0x334455},
		{`destination.getMaskPixel(2, 0)`, 60},
	} {
		if got := read(tc.expression); got != tc.want {
			t.Errorf("%s = %#x, want %#x", tc.expression, got, tc.want)
		}
	}
}

func TestNativeLayerPiledCopyComposesSourceChildren(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	if _, err := session.executeSource("<layer-piled-copy-children-test>", `
	source = new Layer(); child = new Layer(); destination = new Layer();
	source.setImageSize(2, 1); source.setMainPixel(0, 0, 0xff0000); source.setMaskPixel(0, 0, 255);
	source.setMainPixel(1, 0, 0x00ff00); source.setMaskPixel(1, 0, 255);
	child.setImageSize(1, 1); child.setMainPixel(0, 0, 0x0000ff); child.setMaskPixel(0, 0, 255);
	child.setPos(1, 0, 1, 1); child.visible = true; source.addChild(child);
	source.visible = false; source.opacity = 0;
	destination.setImageSize(2, 1);
	destination.piledCopy(0, 0, source, 0, 0, 2, 1);
	`, false); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		expression string
		want       int64
	}{
		{`destination.getMainPixel(0, 0)`, 0xff0000},
		{`destination.getMainPixel(1, 0)`, 0x0000ff},
		{`destination.getMaskPixel(1, 0)`, 255},
	} {
		value, err := session.Evaluate(tc.expression)
		if err != nil {
			t.Fatal(err)
		}
		got, err := value.Int()
		if err != nil || got != tc.want {
			t.Fatalf("%s = %d, %v; want %d", tc.expression, got, err, tc.want)
		}
	}
}
func TestNativeLayerPileRectDefaultsComposesAndClips(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	if _, err := session.executeSource("<layer-pile-rect-test>", `
	source = new Layer(); child = new Layer(); destination = new Layer();
	source.setImageSize(2, 1); child.setImageSize(1, 1); destination.setImageSize(2, 1);
	source.setMainPixel(0, 0, 0xff0000); source.setMaskPixel(0, 0, 255);
	source.setMainPixel(1, 0, 0x00ff00); source.setMaskPixel(1, 0, 255);
	child.setMainPixel(0, 0, 0x0000ff); child.setMaskPixel(0, 0, 255);
	child.setPos(1, 0, 1, 1); child.opacity = 128; child.visible = true; source.addChild(child);
	destination.setMainPixel(0, 0, 0x112233); destination.setMaskPixel(0, 0, 17);
	destination.face = dfMask; destination.setClip(1, 0, 1, 1);
	destination.pileRect(0, 0, source);
	`, false); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		expression string
		want       int64
	}{
		{`destination.getMainPixel(0, 0)`, 0x112233},
		{`destination.getMaskPixel(0, 0)`, 17},
		{`destination.getMainPixel(1, 0)`, 0x007f80},
		{`destination.getMaskPixel(1, 0)`, 255},
	} {
		value, err := session.Evaluate(tc.expression)
		if err != nil {
			t.Fatal(err)
		}
		got, err := value.Int()
		if err != nil || got != tc.want {
			t.Fatalf("%s = %#x, %v; want %#x", tc.expression, got, err, tc.want)
		}
	}
}

func TestNativeLayerSaveLayerImageBMP8UsesSaveDirectory(t *testing.T) {
	saveDir := t.TempDir()
	services := DefaultServices()
	services.SetSaveDir(saveDir)
	session := NewSession(services, 100_000)
	if _, err := session.executeSource("<layer-save-image-test>", `
	layer = new Layer(); layer.setImageSize(2, 1);
	layer.setMainPixel(0, 0, 0xff0000); layer.setMaskPixel(0, 0, 255);
	layer.setMainPixel(1, 0, 0x0000ff); layer.setMaskPixel(1, 0, 255);
	layer.saveLayerImage("thumbnail.bmp", "bmp8");
	`, false); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(saveDir, "thumbnail.bmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 54+1024+4 || string(data[:2]) != "BM" {
		t.Fatalf("saved BMP8 is truncated or has invalid signature: %d bytes", len(data))
	}
	if got := binary.LittleEndian.Uint16(data[28:30]); got != 8 {
		t.Fatalf("BMP bit depth = %d, want 8", got)
	}
	if got := binary.LittleEndian.Uint32(data[18:22]); got != 2 {
		t.Fatalf("BMP width = %d, want 2", got)
	}
	if got := binary.LittleEndian.Uint32(data[22:26]); got != 1 {
		t.Fatalf("BMP height = %d, want 1", got)
	}
	// The existing encoder uses the official 6x7x6 palette layout, with the
	// 0x010000 entry stored as [R, G, B, reserved].
	redEntry := data[54+42*4 : 54+43*4]
	if redEntry[0] != 51 || redEntry[1] != 0 || redEntry[2] != 0 || redEntry[3] != 0 {
		t.Fatalf("BMP8 palette entry 42 = %v, want [51 0 0 0]", redEntry)
	}
}

func TestNativeLayerSaveLayerImagePNGPreservesStraightAlpha(t *testing.T) {
	saveDir := t.TempDir()
	services := DefaultServices()
	services.SetSaveDir(saveDir)
	session := NewSession(services, 100_000)
	if _, err := session.executeSource("<layer-save-image-png-test>", `
	layer = new Layer(); layer.setImageSize(1, 1);
	layer.setMainPixel(0, 0, 0x804020); layer.setMaskPixel(0, 0, 128);
	layer.saveLayerImage("alpha.png", "png");
	`, false); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(saveDir, "alpha.png"))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := assets.DecodeImage(data)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := decoded.RGBA, []byte{0x80, 0x40, 0x20, 128}; string(got) != string(want) {
		t.Fatalf("saved PNG pixel = %v, want straight-alpha %v", got, want)
	}
}
