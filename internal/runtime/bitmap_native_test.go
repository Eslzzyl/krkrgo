package runtime

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/eslzzyl/krkrgo/internal/assets"
	"github.com/eslzzyl/krkrgo/internal/core"
	"github.com/eslzzyl/krkrgo/internal/tjs"
)

func TestNativeBitmapPixelsResizeCopyAndLifecycle(t *testing.T) {
	services := DefaultServices()
	session := NewSession(services, 20_000)
	if _, err := session.executeSource("<bitmap>", `a = new Bitmap(2, 2); a.setPixel(0, 0, 1122867); a.setMaskPixel(0, 0, 127); b = new Bitmap(); b.copyFrom(a)`, false); err != nil {
		t.Fatal(err)
	}
	value, err := session.Evaluate("a.getPixel(0, 0)")
	if err != nil {
		t.Fatal(err)
	}
	pixel, err := value.Int()
	if err != nil || pixel != 1122867 {
		t.Fatalf("pixel=%d err=%v", pixel, err)
	}
	value, err = session.Evaluate("a.getMaskPixel(0, 0)")
	if err != nil {
		t.Fatal(err)
	}
	mask, err := value.Int()
	if err != nil || mask != 127 {
		t.Fatalf("mask=%d err=%v", mask, err)
	}
	if _, err := session.Evaluate("a.setSize(3, 1)"); err != nil {
		t.Fatal(err)
	}
	width, err := session.Evaluate("a.width")
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := width.Int(); n != 3 {
		t.Fatalf("width=%d", n)
	}
	copied, err := session.Evaluate("b.getPixel(0, 0)")
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := copied.Int(); n != 1122867 {
		t.Fatalf("copied pixel=%d", n)
	}
	if _, err := session.Evaluate("a.buffer"); !errors.Is(err, ErrUnsupportedDevice) {
		t.Fatalf("buffer error=%v", err)
	}
	if len(services.Bitmaps) != 2 {
		t.Fatalf("bitmaps=%d", len(services.Bitmaps))
	}
}

func TestNativeBitmapStorageLoadSaveAndHeader(t *testing.T) {
	project := t.TempDir()
	imagePath := filepath.Join(project, "source.png")
	encoded := image.NewRGBA(image.Rect(0, 0, 1, 1))
	encoded.Pix = []byte{1, 2, 3, 255}
	file, err := os.Create(imagePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, encoded); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	storage, err := assets.OpenStorage(project, core.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	services := NewServices(storage, t.TempDir(), core.DefaultLimits())
	session := NewSession(services, 20_000)
	if _, err := session.Evaluate(`a = new Bitmap('source.png')`); err != nil {
		t.Fatal(err)
	}
	if value, err := session.Evaluate("a.width"); err != nil {
		t.Fatal(err)
	} else if n, _ := value.Int(); n != 1 {
		t.Fatalf("loaded width=%d", n)
	}
	for _, source := range []string{
		`a.save('saved.png', 'png')`,
		`a.save('saved.bmp')`,
		`a.save('saved24.bmp', 'bmp24')`,
		`a.save('saved8.bmp', 'bmp8')`,
	} {
		if _, err := session.Evaluate(source); err != nil {
			t.Fatalf("%s: %v", source, err)
		}
	}
	bitmapValue, err := session.Evaluate("a")
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := session.VM.NewDictionary()
	if err != nil {
		t.Fatal(err)
	}
	metadataObject, ok := session.VM.Objects.Lookup(metadata.ObjectRef().Object)
	if !ok {
		t.Fatal("save metadata dictionary was not allocated")
	}
	if err := metadataObject.Set("bpp", tjs.IntegerValue(1)); err != nil {
		t.Fatal(err)
	}
	if _, err := services.bitmapNative(session.VM, bitmapValue, "save", []tjs.Value{
		tjs.StringValue("saved-option.bmp"), tjs.StringValue("bmp"), metadata,
	}, nil); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		bpp  uint16
	}{
		{name: "saved.bmp", bpp: 32},
		{name: "saved24.bmp", bpp: 24},
		{name: "saved8.bmp", bpp: 8},
		{name: "saved-option.bmp", bpp: 24},
	} {
		data, err := os.ReadFile(filepath.Join(services.SaveDir, tc.name))
		if err != nil {
			t.Fatal(err)
		}
		if len(data) < 54 || string(data[:2]) != "BM" {
			t.Fatalf("%s has a truncated or invalid BMP header", tc.name)
		}
		if got := binary.LittleEndian.Uint16(data[28:30]); got != tc.bpp {
			t.Fatalf("%s BMP header bit depth=%d, want %d", tc.name, got, tc.bpp)
		}
		decoded, err := assets.DecodeImage(data)
		if err != nil {
			t.Fatalf("decode %s: %v", tc.name, err)
		}
		if decoded.Width != 1 || decoded.Height != 1 || len(decoded.RGBA) != 4 {
			t.Fatalf("decoded %s image=%+v", tc.name, decoded)
		}
		if tc.bpp == 32 && string(decoded.RGBA) != string([]byte{1, 2, 3, 255}) {
			t.Fatalf("decoded default BMP pixel=%v", decoded.RGBA)
		}
	}
	pngData, err := os.ReadFile(filepath.Join(services.SaveDir, "saved.png"))
	if err != nil {
		t.Fatal(err)
	}
	decodedPNG, err := assets.DecodeImage(pngData)
	if err != nil {
		t.Fatalf("decode saved PNG: %v", err)
	}
	if decodedPNG.Width != 1 || decodedPNG.Height != 1 || string(decodedPNG.RGBA) != string([]byte{1, 2, 3, 255}) {
		t.Fatalf("decoded PNG image=%+v", decodedPNG)
	}

	option, err := session.Evaluate("Bitmap.getSaveOption('bmp')")
	if err != nil {
		t.Fatal(err)
	}
	optionObject, ok := session.VM.Objects.Lookup(option.ObjectRef().Object)
	if !ok {
		t.Fatal("BMP save-option dictionary was not allocated")
	}
	bppOption, ok := optionObject.Get("bpp")
	if !ok {
		t.Fatal("BMP save-option is missing bpp")
	}
	bppObject, ok := session.VM.Objects.Lookup(bppOption.ObjectRef().Object)
	if !ok {
		t.Fatal("BMP bpp save-option dictionary was not allocated")
	}
	typeValue, ok := bppObject.Get("type")
	if !ok || typeValue.Text() != "select" {
		t.Fatalf("BMP bpp option type=%q", typeValue.Text())
	}
	descValue, ok := bppObject.Get("desc")
	if !ok || descValue.Text() != "bpp" {
		t.Fatalf("BMP bpp option description=%q", descValue.Text())
	}
	itemsValue, ok := bppObject.Get("items")
	if !ok {
		t.Fatal("BMP bpp save-option is missing items")
	}
	items, err := session.VM.ArrayItems(itemsValue)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 || items[0].Text() != "32" || items[1].Text() != "24" || items[2].Text() != "8" {
		t.Fatalf("BMP bpp items=%v", items)
	}
	defaultValue, ok := bppObject.Get("default")
	if !ok {
		t.Fatal("BMP bpp save-option is missing default")
	}
	if value, err := defaultValue.Int(); err != nil || value != 0 {
		t.Fatalf("BMP bpp default=%v err=%v", defaultValue, err)
	}
	for _, format := range []string{"bmp24", "bmp8", "bmp32", ".bmp", ".dib"} {
		value, err := services.bitmapStaticNative(session.VM, "getSaveOption", []tjs.Value{tjs.StringValue(format)})
		if err != nil || value.IsVoid() {
			t.Fatalf("getSaveOption(%q)=%v err=%v", format, value, err)
		}
	}
	pngOptionType, err := session.Evaluate("Bitmap.getSaveOption('png').type")
	if err != nil || pngOptionType.Text() != "png" {
		t.Fatalf("PNG save option type=%q err=%v", pngOptionType.Text(), err)
	}
	if value, err := session.Evaluate("Bitmap.getSaveOption('jpeg')"); err != nil || !value.IsVoid() {
		t.Fatalf("unsupported save option=%v err=%v", value, err)
	}
	if _, err := session.Evaluate(`a.save('unsupported.jpg', 'jpeg')`); !errors.Is(err, ErrUnsupportedDevice) {
		t.Fatalf("unsupported encoder error=%v", err)
	}
	if _, err := session.Evaluate(`Bitmap.loadHeader('source.png').height`); err != nil {
		t.Fatal(err)
	}
}

func TestNativeBitmapJPEGSaveAndOptions(t *testing.T) {
	project := t.TempDir()
	storage, err := assets.OpenStorage(project, core.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	services := NewServices(storage, t.TempDir(), core.DefaultLimits())
	session := NewSession(services, 20_000)
	if _, err := session.Evaluate(`a = new Bitmap(64, 64)`); err != nil {
		t.Fatal(err)
	}
	bitmapValue, err := session.Evaluate("a")
	if err != nil {
		t.Fatal(err)
	}
	bitmap := services.Bitmap(bitmapValue.ObjectRef().Object)
	if bitmap == nil {
		t.Fatal("constructed Bitmap was not registered")
	}
	for y := uint32(0); y < bitmap.Height; y++ {
		for x := uint32(0); x < bitmap.Width; x++ {
			i := (int(y)*int(bitmap.Width) + int(x)) * 4
			bitmap.RGBA[i] = byte((x*31 + y*17 + x*y%29) % 256)
			bitmap.RGBA[i+1] = byte((x*5 + y*43 + x*y*3%41) % 256)
			bitmap.RGBA[i+2] = byte((x*19 + y*11 + x*y*7%67) % 256)
			bitmap.RGBA[i+3] = byte((x + y) % 256)
		}
	}
	for _, source := range []string{
		`a.save('jpeg-default.jpg', 'jpg')`,
		`a.save('jpeg-q90.jpg', 'jpg90')`,
		`a.save('jpeg-low.jpg', 'jpg1')`,
		`a.save('jpeg-high.jpg', 'jpg100')`,
		`a.save('jpeg-q40.jpg', 'jpg40')`,
		`a.save('jpeg-zero.jpg', 'jpg0')`,
		`a.save('jpeg-ten.jpg', 'jpg10')`,
		`a.save('jpeg-over.jpg', 'jpg101')`,
		`a.save('jpeg-alias.jpg', '.jpg')`,
		`a.save('jpeg-alias.jpeg', '.jpeg')`,
		`a.save('jpeg-alias.jif', '.jif')`,
	} {
		if _, err := session.Evaluate(source); err != nil {
			t.Fatalf("%s: %v", source, err)
		}
	}
	for _, tc := range []struct {
		name    string
		mode    string
		quality int64
	}{
		{name: "jpeg-meta-override.jpg", mode: "jpg100", quality: 40},
		{name: "jpeg-meta-low.jpg", mode: "jpg100", quality: 0},
		{name: "jpeg-meta-high.jpg", mode: "jpg1", quality: 101},
	} {
		metadata, err := session.VM.NewDictionary()
		if err != nil {
			t.Fatal(err)
		}
		object, ok := session.VM.Objects.Lookup(metadata.ObjectRef().Object)
		if !ok {
			t.Fatal("JPEG save metadata dictionary was not allocated")
		}
		if err := object.Set("quality", tjs.IntegerValue(tc.quality)); err != nil {
			t.Fatal(err)
		}
		if _, err := services.bitmapNative(session.VM, bitmapValue, "save", []tjs.Value{
			tjs.StringValue(tc.name), tjs.StringValue(tc.mode), metadata,
		}, nil); err != nil {
			t.Fatalf("save with quality %d: %v", tc.quality, err)
		}
	}
	readSaved := func(name string) []byte {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(services.SaveDir, name))
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	for _, name := range []string{
		"jpeg-default.jpg", "jpeg-q90.jpg", "jpeg-low.jpg", "jpeg-high.jpg", "jpeg-q40.jpg",
		"jpeg-zero.jpg", "jpeg-ten.jpg", "jpeg-over.jpg", "jpeg-alias.jpg", "jpeg-alias.jpeg",
		"jpeg-alias.jif", "jpeg-meta-override.jpg", "jpeg-meta-low.jpg", "jpeg-meta-high.jpg",
	} {
		data := readSaved(name)
		decoded, err := assets.DecodeImage(data)
		if err != nil {
			t.Fatalf("decode saved JPEG %s: %v", name, err)
		}
		if decoded.Width != 64 || decoded.Height != 64 {
			t.Fatalf("decoded JPEG %s dimensions=%dx%d", name, decoded.Width, decoded.Height)
		}
	}
	if !bytes.Equal(readSaved("jpeg-default.jpg"), readSaved("jpeg-q90.jpg")) {
		t.Fatal("bare jpg mode did not use the default quality 90")
	}
	if !bytes.Equal(readSaved("jpeg-zero.jpg"), readSaved("jpeg-ten.jpg")) {
		t.Fatal("zero JPEG mode quality did not fall back to quality 10")
	}
	if !bytes.Equal(readSaved("jpeg-over.jpg"), readSaved("jpeg-high.jpg")) {
		t.Fatal("JPEG mode quality above 100 was not clamped to 100")
	}
	if !bytes.Equal(readSaved("jpeg-meta-override.jpg"), readSaved("jpeg-q40.jpg")) {
		t.Fatal("JPEG quality metadata did not override the mode quality")
	}
	if !bytes.Equal(readSaved("jpeg-meta-low.jpg"), readSaved("jpeg-low.jpg")) {
		t.Fatal("JPEG quality metadata below 1 was not clamped to 1")
	}
	if !bytes.Equal(readSaved("jpeg-meta-high.jpg"), readSaved("jpeg-high.jpg")) {
		t.Fatal("JPEG quality metadata above 100 was not clamped to 100")
	}
	if low, high := readSaved("jpeg-low.jpg"), readSaved("jpeg-high.jpg"); len(high) <= len(low) {
		t.Fatalf("JPEG quality did not affect compression: quality 1=%d bytes, quality 100=%d bytes", len(low), len(high))
	}
	for _, format := range []string{"jpg", "jpg90", ".jpg", ".jpeg", ".jif"} {
		option, err := services.bitmapStaticNative(session.VM, "getSaveOption", []tjs.Value{tjs.StringValue(format)})
		if err != nil || option.IsVoid() {
			t.Fatalf("getSaveOption(%q)=%v err=%v", format, option, err)
		}
	}
	if option, err := services.bitmapStaticNative(session.VM, "getSaveOption", []tjs.Value{tjs.StringValue("jpeg")}); err != nil || !option.IsVoid() {
		t.Fatalf("C++-unsupported JPEG mode getSaveOption(%q)=%v err=%v", "jpeg", option, err)
	}
	option, err := services.bitmapStaticNative(session.VM, "getSaveOption", []tjs.Value{tjs.StringValue("jpg")})
	if err != nil {
		t.Fatal(err)
	}
	optionObject, ok := session.VM.Objects.Lookup(option.ObjectRef().Object)
	if !ok {
		t.Fatal("JPEG save-option dictionary was not allocated")
	}
	qualityValue, ok := optionObject.Get("quality")
	if !ok {
		t.Fatal("JPEG save-option is missing quality")
	}
	qualityObject, ok := session.VM.Objects.Lookup(qualityValue.ObjectRef().Object)
	if !ok {
		t.Fatal("JPEG quality save-option dictionary was not allocated")
	}
	if value, ok := qualityObject.Get("type"); !ok || value.Text() != "range" {
		t.Fatalf("JPEG quality option type=%q", value.Text())
	}
	for _, tc := range []struct {
		key  string
		want int64
	}{
		{key: "min", want: 1},
		{key: "max", want: 100},
		{key: "default", want: 90},
	} {
		value, ok := qualityObject.Get(tc.key)
		if !ok {
			t.Fatalf("JPEG quality save-option is missing %s", tc.key)
		}
		got, err := value.Int()
		if err != nil || got != tc.want {
			t.Fatalf("JPEG quality %s=%d err=%v, want %d", tc.key, got, err, tc.want)
		}
	}
	if value, ok := qualityObject.Get("desc"); !ok || value.Text() != "100 is high quality, 1 is low quality" {
		t.Fatalf("JPEG quality description=%q", value.Text())
	}
	for _, key := range []string{"subsampling", "dct", "progressive"} {
		if _, ok := optionObject.Get(key); ok {
			t.Fatalf("JPEG save options unexpectedly advertise unsupported %s", key)
		}
	}
	for _, tc := range []struct {
		key   string
		value int64
	}{
		{key: "subsampling", value: 0},
		{key: "dct", value: 0},
		{key: "progressive", value: 1},
	} {
		metadata, err := session.VM.NewDictionary()
		if err != nil {
			t.Fatal(err)
		}
		object, ok := session.VM.Objects.Lookup(metadata.ObjectRef().Object)
		if !ok {
			t.Fatal("JPEG save metadata dictionary was not allocated")
		}
		if err := object.Set(tc.key, tjs.IntegerValue(tc.value)); err != nil {
			t.Fatal(err)
		}
		if _, err := services.bitmapNative(session.VM, bitmapValue, "save", []tjs.Value{
			tjs.StringValue("unsupported-" + tc.key + ".jpg"), tjs.StringValue("jpg"), metadata,
		}, nil); !errors.Is(err, ErrUnsupportedDevice) {
			t.Fatalf("JPEG save with %s metadata returned %v, want ErrUnsupportedDevice", tc.key, err)
		}
	}
	if _, err := session.Evaluate(`a.save('unsupported.tlg', 'tlg')`); !errors.Is(err, ErrUnsupportedDevice) {
		t.Fatalf("unsupported encoder error=%v", err)
	}
}
