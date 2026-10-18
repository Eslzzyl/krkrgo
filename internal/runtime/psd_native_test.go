package runtime

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eslzzyl/krkrgo/internal/assets"
	"github.com/eslzzyl/krkrgo/internal/core"
)

func psdNativeBe16(v uint16) []byte { return []byte{byte(v >> 8), byte(v)} }
func psdNativeBe32(v int32) []byte {
	return []byte{byte(uint32(v) >> 24), byte(uint32(v) >> 16), byte(uint32(v) >> 8), byte(v)}
}

func psdNativeLayerExtra() []byte {
	var extra []byte
	extra = append(extra, psdNativeBe32(0)...) // mask size
	extra = append(extra, psdNativeBe32(0)...) // blending ranges
	extra = append(extra, 5)
	extra = append(extra, []byte("Layer")...)
	extra = append(extra, 0, 0) // Pascal name padding
	return extra
}

// psdNativeFixture builds a 2x2 three-channel RGB document with one 2x2
// solid layer and a raw composite image.
func psdNativeFixture(mode uint16) []byte {
	planes := [][]byte{
		{10, 20, 30, 40},
		{50, 60, 70, 80},
		{90, 100, 110, 120},
	}
	layerPlanes := [][]byte{
		{255, 0, 0, 255},
		{0, 255, 0, 255},
		{0, 0, 255, 255},
	}
	var layerInfo []byte
	layerInfo = append(layerInfo, psdNativeBe16(1)...)
	layerInfo = append(layerInfo, psdNativeBe32(0)...) // top
	layerInfo = append(layerInfo, psdNativeBe32(0)...) // left
	layerInfo = append(layerInfo, psdNativeBe32(2)...) // bottom
	layerInfo = append(layerInfo, psdNativeBe32(2)...) // right
	layerInfo = append(layerInfo, psdNativeBe16(3)...)
	var channelData []byte
	for id, plane := range layerPlanes {
		payload := append(psdNativeBe16(0), plane...)
		layerInfo = append(layerInfo, psdNativeBe16(uint16(id))...)
		layerInfo = append(layerInfo, psdNativeBe32(int32(len(payload)))...)
		channelData = append(channelData, payload...)
	}
	layerInfo = append(layerInfo, []byte("8BIMnorm")...)
	layerInfo = append(layerInfo, 255, 0, 0, 0)
	extra := psdNativeLayerExtra()
	layerInfo = append(layerInfo, psdNativeBe32(int32(len(extra)))...)
	layerInfo = append(layerInfo, extra...)
	layerInfo = append(layerInfo, channelData...)

	var layerMask []byte
	layerMask = append(layerMask, psdNativeBe32(int32(len(layerInfo)))...)
	layerMask = append(layerMask, layerInfo...)
	layerMask = append(layerMask, psdNativeBe32(0)...)

	var data []byte
	data = append(data, []byte("8BPS")...)
	data = append(data, psdNativeBe16(1)...)
	data = append(data, make([]byte, 6)...)
	data = append(data, psdNativeBe16(3)...)
	data = append(data, psdNativeBe32(2)...)
	data = append(data, psdNativeBe32(2)...)
	data = append(data, psdNativeBe16(8)...)
	data = append(data, psdNativeBe16(mode)...)
	data = append(data, psdNativeBe32(0)...)
	data = append(data, psdNativeBe32(0)...)
	data = append(data, psdNativeBe32(int32(len(layerMask)))...)
	data = append(data, layerMask...)
	data = append(data, psdNativeBe16(0)...)
	for _, plane := range planes {
		data = append(data, plane...)
	}
	return data
}

func psdNativeWrite(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sample.psd")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func psdNativeQuote(path string) string {
	path = strings.ReplaceAll(path, "\\", "\\\\")
	return strings.ReplaceAll(path, "'", "\\'")
}

func psdNativeSession(t *testing.T) *Session {
	t.Helper()
	session := NewSession(DefaultServices(), 4_000_000)
	if err := installPSDNative(session.VM, session.Services); err != nil {
		t.Fatal(err)
	}
	return session
}

func psdNativeInt(t *testing.T, session *Session, expression string) int64 {
	t.Helper()
	value, err := session.Evaluate(expression)
	if err != nil {
		t.Fatalf("%s: %v", expression, err)
	}
	number, err := value.Int()
	if err != nil {
		t.Fatalf("%s: %v", expression, err)
	}
	return number
}

func psdNativeString(t *testing.T, session *Session, expression string) string {
	t.Helper()
	value, err := session.Evaluate(expression)
	if err != nil {
		t.Fatalf("%s: %v", expression, err)
	}
	return value.Text()
}

func TestPSDNativeLoadCompositeAndLayers(t *testing.T) {
	session := psdNativeSession(t)
	path := psdNativeWrite(t, psdNativeFixture(uint16(assets.PSDColorModeRGB)))
	if _, err := session.executeSource("<psd-native-test>", `
psd = new PSD();
loaded = psd.load('`+psdNativeQuote(path)+`');
layer = new Layer();
psd.getLayerData(layer, 0);
raw = new Layer();
psd.getLayerDataRaw(raw, 0);
mask = new Layer();
psd.getLayerDataMask(mask, 0);
blend = new Layer();
blended = psd.getBlend(blend);
info = psd.getLayerInfo(0);
`, false); err != nil {
		t.Fatal(err)
	}
	if got := psdNativeInt(t, session, "loaded"); got != 1 {
		t.Fatalf("load = %d, want 1", got)
	}
	for expression, want := range map[string]int64{
		"psd.width":       2,
		"psd.height":      2,
		"psd.channels":    3,
		"psd.depth":       8,
		"psd.color_mode":  3,
		"psd.layer_count": 1,
	} {
		if got := psdNativeInt(t, session, expression); got != want {
			t.Fatalf("%s = %d, want %d", expression, got, want)
		}
	}
	if got := psdNativeInt(t, session, "psd.getLayerType(0)"); got != int64(assets.PSDLayerNormal) {
		t.Fatalf("getLayerType = %d", got)
	}
	if got := psdNativeString(t, session, "psd.getLayerName(0)"); got != "Layer" {
		t.Fatalf("getLayerName = %q", got)
	}
	for expression, want := range map[string]int64{
		"layer.left":                  0,
		"layer.top":                   0,
		"layer.width":                 2,
		"layer.height":                2,
		"layer.opacity":               255,
		"layer.type":                  13,
		"layer.getMainPixel(0, 0)":    0xff0000,
		"layer.getMainPixel(1, 1)":    0xffffff,
		"raw.getMainPixel(1, 0)":      0x00ff00,
		"mask.width":                  1,
		"mask.height":                 1,
		"blended":                     1,
		"blend.width":                 2,
		"blend.height":                2,
		"blend.getMainPixel(1, 0)":    0x143c64,
		"info.top":                    0,
		"info.left":                   0,
		"info.width":                  2,
		"info.height":                 2,
		"info.opacity":                255,
		"info.fill_opacity":           255,
		"info.mask":                   0,
		"info.blend_mode":             0,
		"info.layer_type":             0,
		"info.visible":                1,
		"info.layer_id":               -1,
		"info.transparency_protected": 0,
		"info.pixel_data_irrelevant":  0,
	} {
		if got := psdNativeInt(t, session, expression); got != want {
			t.Fatalf("%s = %d, want %d", expression, got, want)
		}
	}
	if got := psdNativeString(t, session, "info.name"); got != "Layer" {
		t.Fatalf("info.name = %q", got)
	}
	if _, err := session.Evaluate("PSD.clearStorageCache()"); err != nil {
		t.Fatalf("clearStorageCache: %v", err)
	}
}

func TestPSDNativeStorageNameAndMalformedInput(t *testing.T) {
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, "images"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(project, "images", "stored.psd")
	if err := os.WriteFile(path, psdNativeFixture(uint16(assets.PSDColorModeRGB)), 0o644); err != nil {
		t.Fatal(err)
	}
	storage, err := assets.NewStorage(project, core.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	services := DefaultServices()
	services.Storage = storage
	session := NewSession(services, 4_000_000)
	if err := installPSDNative(session.VM, session.Services); err != nil {
		t.Fatal(err)
	}
	if _, err := session.executeSource("<psd-storage-test>", `
stored = new PSD();
storedLoaded = stored.load('images/stored.psd');
missing = new PSD();
missingLoaded = missing.load('images/absent.psd');
broken = new PSD();
brokenLoaded = broken.load('`+psdNativeQuote(filepath.Join(t.TempDir(), "broken.psd"))+`');
`, false); err != nil {
		t.Fatal(err)
	}
	if got := psdNativeInt(t, session, "storedLoaded"); got != 1 {
		t.Fatalf("storage load = %d", got)
	}
	if got := psdNativeInt(t, session, "stored.width"); got != 2 {
		t.Fatalf("storage width = %d", got)
	}
	if got := psdNativeInt(t, session, "missingLoaded"); got != 0 {
		t.Fatalf("missing load = %d", got)
	}
	if got := psdNativeInt(t, session, "missing.width"); got != -1 {
		t.Fatalf("missing width = %d", got)
	}
	if got := psdNativeInt(t, session, "brokenLoaded"); got != 0 {
		t.Fatalf("unreadable load = %d", got)
	}
	// A file whose header is malformed loads as false and leaves no document.
	broken := psdNativeWrite(t, []byte("8BPS\x00\x01\x00\x00\x00\x00\x00\x00\x00\x03"))
	brokenSession := psdNativeSession(t)
	if _, err := brokenSession.executeSource("<psd-broken-test>", `
bad = new PSD();
badLoaded = bad.load('`+psdNativeQuote(broken)+`');
`, false); err != nil {
		t.Fatal(err)
	}
	if got := psdNativeInt(t, brokenSession, "badLoaded"); got != 0 {
		t.Fatalf("malformed load = %d", got)
	}
	if got := psdNativeInt(t, brokenSession, "bad.layer_count"); got != -1 {
		t.Fatalf("malformed layer_count = %d", got)
	}
}

func TestPSDNativeDefersUnsupportedFeatures(t *testing.T) {
	session := psdNativeSession(t)
	lab := psdNativeWrite(t, psdNativeFixture(uint16(assets.PSDColorModeLAB)))
	if _, err := session.executeSource("<psd-unsupported-test>", `
lab = new PSD();
labLoaded = lab.load('`+psdNativeQuote(lab)+`');
labLayer = new Layer();
`, false); err != nil {
		t.Fatal(err)
	}
	if got := psdNativeInt(t, session, "labLoaded"); got != 1 {
		t.Fatalf("Lab load = %d", got)
	}
	if _, err := session.Evaluate("lab.getBlend(labLayer)"); !errors.Is(err, assets.ErrPSDUnsupported) {
		t.Fatalf("Lab getBlend error = %v, want ErrPSDUnsupported", err)
	}
	if _, err := session.Evaluate("lab.getLayerData(labLayer, 0)"); !errors.Is(err, assets.ErrPSDUnsupported) {
		t.Fatalf("Lab getLayerData error = %v, want ErrPSDUnsupported", err)
	}
}

func TestPSDNativeLayerValidation(t *testing.T) {
	session := psdNativeSession(t)
	path := psdNativeWrite(t, psdNativeFixture(uint16(assets.PSDColorModeRGB)))
	if _, err := session.executeSource("<psd-layer-validation>", `
psd = new PSD();
psd.load('`+psdNativeQuote(path)+`');
`, false); err != nil {
		t.Fatal(err)
	}
	for _, expression := range []string{
		"psd.getLayerType(4)",
		"psd.getLayerName(-1)",
		"psd.getLayerInfo(2)",
	} {
		if _, err := session.Evaluate(expression); err == nil || !strings.Contains(err.Error(), "not such layer") {
			t.Fatalf("%s error = %v, want not such layer", expression, err)
		}
	}
	if _, err := session.Evaluate("psd.getLayerData(new Object(), 0)"); err == nil || !strings.Contains(err.Error(), "not layer") {
		t.Fatalf("non-layer error = %v", err)
	}
	if _, err := session.executeSource("<psd-unloaded>", "unloaded = new PSD();", false); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Evaluate("unloaded.getLayerType(0)"); err == nil || !strings.Contains(err.Error(), "no data") {
		t.Fatalf("unloaded error = %v", err)
	}
}

func TestPSDNativeCompositeRawPixels(t *testing.T) {
	// Directly exercise the composite fixture bytes to guard the native test
	// builder against drifting from assets.ParsePSD semantics.
	doc, err := assets.ParsePSD(psdNativeFixture(uint16(assets.PSDColorModeRGB)))
	if err != nil {
		t.Fatal(err)
	}
	image, err := doc.Composite()
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{10, 50, 90, 255, 20, 60, 100, 255, 30, 70, 110, 255, 40, 80, 120, 255}
	if !bytes.Equal(image.RGBA, want) {
		t.Fatalf("composite = %v, want %v", image.RGBA, want)
	}
}
