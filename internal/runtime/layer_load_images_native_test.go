package runtime

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/eslzzyl/krkrgo/internal/assets"
	"github.com/eslzzyl/krkrgo/internal/core"
	"github.com/eslzzyl/krkrgo/internal/tjs"
)

func writeLayerImagePNG(t *testing.T, path string) {
	t.Helper()
	im := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	copy(im.Pix, []byte{1, 2, 3, 255, 10, 20, 30, 64})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, im); err != nil {
		t.Fatal(err)
	}
	data := encoded.Bytes()
	body := make([]byte, 9)
	binary.BigEndian.PutUint32(body[0:4], 6)
	binary.BigEndian.PutUint32(body[4:8], 7)
	body[8] = 0
	chunk := make([]byte, 4+4+len(body)+4)
	binary.BigEndian.PutUint32(chunk[:4], uint32(len(body)))
	copy(chunk[4:8], "oFFs")
	copy(chunk[8:8+len(body)], body)
	binary.BigEndian.PutUint32(chunk[8+len(body):], crc32.ChecksumIEEE(chunk[4:8+len(body)]))
	data = append(append(append([]byte(nil), data[:33]...), chunk...), data[33:]...)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}
func writeLayerImagePNGSize(t *testing.T, path string, width, height int) {
	t.Helper()
	im := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			im.SetNRGBA(x, y, color.NRGBA{R: uint8(x + 1), G: uint8(y + 1), B: 3, A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, im); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeProvincePatternPNG(t *testing.T, path string, width, height int) {
	t.Helper()
	palette := color.Palette{color.Black, color.RGBA{R: 255, A: 255}, color.RGBA{G: 255, A: 255}, color.RGBA{B: 255, A: 255}}
	im := image.NewPaletted(image.Rect(0, 0, width, height), palette)
	for y := range height {
		for x := range width {
			im.SetColorIndex(x, y, uint8((x+y*width)%3+1))
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, im); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestNativeLayerLoadImagesReadsStorageAndReturnsTags(t *testing.T) {
	project := t.TempDir()
	writeLayerImagePNG(t, filepath.Join(project, "art", "sprite.png"))
	storage, err := assets.OpenStorage(project, core.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	services := NewServices(storage, t.TempDir(), core.DefaultLimits())
	session := NewSession(services, 20_000)
	if _, err := session.Evaluate(`l = new Layer()`); err != nil {
		t.Fatal(err)
	}
	info, err := session.Evaluate(`l.loadImages('art/sprite')`)
	if err != nil {
		t.Fatal(err)
	}
	if info.Kind() != tjs.KindObject || info.IsNull() {
		t.Fatalf("loadImages metadata=%v, want dictionary", info)
	}
	for source, want := range map[string]int64{
		"l.width": 2, "l.height": 1, "l.imageWidth": 2, "l.imageHeight": 1,
		"l.getMainPixel(0,0)": 0x010203, "l.getMaskPixel(0,0)": 255,
		"l.getMainPixel(1,0)": 0x0a141e, "l.getMaskPixel(1,0)": 64,
	} {
		value, err := session.Evaluate(source)
		if err != nil {
			t.Fatalf("%s: %v", source, err)
		}
		got, err := value.Int()
		if err != nil || got != want {
			t.Fatalf("%s=%d, err=%v; want %d", source, got, err, want)
		}
	}

	if _, err := session.Evaluate(`l.loadImages(%[storage:"art/sprite"])`); err != nil {
		t.Fatalf("Layer.loadImages KAG tag object: %v", err)
	}
	for name, want := range map[string]string{"offs_x": "6", "offs_y": "7", "offs_unit": "pixel"} {
		value, err := session.VM.GetMember(info, name, session.Services, &session.Budget)
		if err != nil || value.Text() != want {
			t.Fatalf("info.%s=%q, err=%v; want %q", name, value.Text(), err, want)
		}
	}

	if _, err := session.Evaluate(`l.loadImages('art/sprite.png', 0x010203)`); err != nil {
		t.Fatal(err)
	}
	for source, want := range map[string]int64{"l.getMaskPixel(0,0)": 0, "l.getMaskPixel(1,0)": 255} {
		value, err := session.Evaluate(source)
		if err != nil {
			t.Fatalf("%s: %v", source, err)
		}
		got, err := value.Int()
		if err != nil || got != want {
			t.Fatalf("%s=%d, err=%v; want %d", source, got, err, want)
		}
	}
	if _, err := session.Evaluate(`l.loadImages('art/sprite.png', 0x0a141e)`); err != nil {
		t.Fatal(err)
	}
	for source, want := range map[string]int64{
		"l.getMainPixel(1,0)": 0x0a141e,
		"l.getMaskPixel(1,0)": 0,
	} {
		value, err := session.Evaluate(source)
		if err != nil {
			t.Fatalf("%s: %v", source, err)
		}
		got, err := value.Int()
		if err != nil || got != want {
			t.Fatalf("%s=%d, err=%v; want %d", source, got, err, want)
		}
	}
}

func TestKAGImageTagHandlerLoadsAndComposesLayer(t *testing.T) {
	project := t.TempDir()
	writeLayerImagePNG(t, filepath.Join(project, "bg0.png"))
	storage, err := assets.OpenStorage(project, core.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	session := NewSession(NewServices(storage, t.TempDir(), core.DefaultLimits()), 20_000)
	if _, err := session.executeSource("<kag-image-tag-test>", `
		root = new Layer();
		fore = new Layer(null, root);
		base = new Layer(null, fore);
		fore.visible = true;
		base.visible = true;
		fore.base = base;
		kag = new KAGParser();
		kag.loadScenario("0.ks", '[image storage="bg0" page=fore layer=base]');
		options = kag.getNextTag();
		handlerCalls = 0;
		imageTagHandler = function(options) {
			if (options.tagname == "image" && options.storage == "bg0" &&
				options.page == "fore" && options.layer == "base") {
				fore.base.loadImages(options);
				handlerCalls = handlerCalls + 1;
			}
		};
	`, false); err != nil {
		t.Fatalf("prepare CAWorks image tag handler: %v", err)
	}
	before, err := session.CaptureFrame()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.executeSource("<kag-image-tag-dispatch>", `imageTagHandler(options)`, false); err != nil {
		t.Fatalf("dispatch CAWorks image tag through TJS handler: %v", err)
	}
	for source, want := range map[string]int64{
		"handlerCalls":           1,
		"base.hasImage":          1,
		"base.getMainPixel(0,0)": 0x010203,
	} {
		value, err := session.Evaluate(source)
		if err != nil {
			t.Fatalf("%s: %v", source, err)
		}
		got, err := value.Int()
		if err != nil || got != want {
			t.Fatalf("%s=%d, err=%v; want %d", source, got, err, want)
		}
	}

	after, err := session.CaptureFrame()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(before.RGBA, after.RGBA) {
		t.Fatal("CaptureFrame did not reflect the TJS handler's Layer.loadImages call")
	}
	if got, want := after.RGBA[:4], []byte{1, 2, 3, 255}; !bytes.Equal(got, want) {
		t.Fatalf("composited image pixel = %v, want %v", got, want)
	}
}

func TestNativeLayerLoadImagesErrorsPreserveState(t *testing.T) {
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "bad.png"), []byte("not a PNG"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "unsupported.jxr"), []byte("JXR"), 0o644); err != nil {
		t.Fatal(err)
	}
	storage, err := assets.OpenStorage(project, core.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	session := NewSession(NewServices(storage, "", core.DefaultLimits()), 20_000)
	if _, err := session.Evaluate(`l = new Layer()`); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Evaluate(`l.loadImages()`); err == nil {
		t.Fatal("loadImages without storage succeeded")
	}
	if _, err := session.Evaluate(`l.loadImages('missing.png')`); !errors.Is(err, assets.ErrNotFound) {
		t.Fatalf("missing storage error=%v", err)
	}
	if _, err := session.Evaluate(`l.loadImages('unsupported.jxr')`); !errors.Is(err, assets.ErrUnsupported) {
		t.Fatalf("unsupported format error=%v", err)
	}
	if _, err := session.Evaluate(`l.loadImages('bad.png')`); !errors.Is(err, assets.ErrMalformed) {
		t.Fatalf("malformed image error=%v", err)
	}
	for source, want := range map[string]int64{"l.imageWidth": 32, "l.imageHeight": 32, "l.getMaskPixel(0,0)": 0} {
		value, err := session.Evaluate(source)
		if err != nil {
			t.Fatalf("%s: %v", source, err)
		}
		got, err := value.Int()
		if err != nil || got != want {
			t.Fatalf("%s=%d, err=%v; want %d", source, got, err, want)
		}
	}
}
func writeProvincePNG(t *testing.T, path string, width, height int) {
	t.Helper()
	palette := color.Palette{color.Black, color.RGBA{R: 255, A: 255}, color.RGBA{G: 255, A: 255}}
	im := image.NewPaletted(image.Rect(0, 0, width, height), palette)
	if width > 0 && height > 0 {
		im.SetColorIndex(0, 0, 1)
		if width > 1 {
			im.SetColorIndex(1, 0, 2)
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, im); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestNativeLayerLoadProvinceImageLoadsIndexedHitData(t *testing.T) {
	project := t.TempDir()
	writeLayerImagePNG(t, filepath.Join(project, "art", "sprite.png"))
	writeProvincePNG(t, filepath.Join(project, "art", "map.png"), 2, 1)
	writeProvincePNG(t, filepath.Join(project, "art", "wrong.png"), 3, 1)
	storage, err := assets.OpenStorage(project, core.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	services := NewServices(storage, t.TempDir(), core.DefaultLimits())
	session := NewSession(services, 20_000)
	if _, err := session.executeSource("<load-province-image>", `l = new Layer(); l.loadImages('art/sprite')`, false); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Evaluate(`l.loadProvinceImage('art/map')`); err != nil {
		t.Fatal(err)
	}
	layerValue, err := session.Evaluate("l")
	if err != nil {
		t.Fatal(err)
	}
	layer := services.Layers[tjs.ObjectID(layerValue.ObjectRef().Object)]
	if layer == nil || layer.ProvinceWidth != 2 || layer.ProvinceHeight != 1 ||
		len(layer.Province) != 2 || layer.Province[0] != 1 || layer.Province[1] != 2 {
		t.Fatalf("province plane = %#v; want 2x1 [1 2]", layer)
	}
	layer.Visible = true
	layer.Hit = HitProvince
	layer.Opacity = 255
	if err := layer.SetBounds(Rect{Width: 2, Height: 1}); err != nil {
		t.Fatal(err)
	}
	if layer.HitTest(0, 0) != layer || layer.HitTest(1, 0) != layer || layer.HitTest(2, 0) != nil {
		t.Fatal("province data did not drive layer hit testing")
	}
	for source, want := range map[string]int64{
		"l.getProvincePixel(0,0)": 1,
		"l.getProvincePixel(1,0)": 2,
		"l.getProvincePixel(2,0)": 0,
		"l.getMainPixel(0,0)":     0x010203,
	} {
		value, err := session.Evaluate(source)
		if err != nil {
			t.Fatalf("%s: %v", source, err)
		}
		got, err := value.Int()
		if err != nil || got != want {
			t.Fatalf("%s=%d, err=%v; want %d", source, got, err, want)
		}
	}
	if _, err := session.Evaluate(`l.loadProvinceImage('art/wrong')`); !errors.Is(err, ErrProvinceSizeMismatch) {
		t.Fatalf("dimension mismatch error=%v", err)
	}
	if len(layer.Province) != 0 {
		t.Fatalf("failed province load retained province data: %v", layer.Province)
	}
	if _, err := session.Evaluate(`l.loadProvinceImage('art/missing')`); !errors.Is(err, assets.ErrNotFound) {
		t.Fatalf("missing province error=%v", err)
	}
	if _, err := session.executeSource("<load-province-image-empty>", `empty = new Layer(); empty.hasImage = false; empty.loadProvinceImage('art/map')`, false); !errors.Is(err, ErrInvalidLayer) {
		t.Fatalf("province load without main image error=%v", err)
	}
}
func TestNativeLayerLoadProvinceImageTilesToMainSize(t *testing.T) {
	for _, tc := range []struct {
		name          string
		width, height int
		want          []byte
	}{
		{name: "horizontal", width: 4, height: 2, want: []byte{1, 2, 1, 2, 3, 1, 3, 1}},
		{name: "vertical", width: 2, height: 4, want: []byte{1, 2, 3, 1, 1, 2, 3, 1}},
		{name: "both", width: 4, height: 4, want: []byte{1, 2, 1, 2, 3, 1, 3, 1, 1, 2, 1, 2, 3, 1, 3, 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			project := t.TempDir()
			writeLayerImagePNGSize(t, filepath.Join(project, "art", "sprite.png"), tc.width, tc.height)
			writeProvincePatternPNG(t, filepath.Join(project, "art", "map.png"), 2, 2)
			storage, err := assets.OpenStorage(project, core.DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			services := NewServices(storage, t.TempDir(), core.DefaultLimits())
			session := NewSession(services, 20_000)
			defer session.Close()
			if _, err := session.executeSource("<province-tiling>", `l = new Layer(); l.loadImages('art/sprite'); l.loadProvinceImage('art/map')`, false); err != nil {
				t.Fatal(err)
			}
			value, err := session.Evaluate("l")
			if err != nil {
				t.Fatal(err)
			}
			layer := services.Layers[tjs.ObjectID(value.ObjectRef().Object)]
			if layer == nil || layer.ProvinceWidth != int32(tc.width) ||
				layer.ProvinceHeight != int32(tc.height) || !bytes.Equal(layer.Province, tc.want) {
				t.Fatalf("province = %dx%d %v; want %dx%d %v", layer.ProvinceWidth,
					layer.ProvinceHeight, layer.Province, tc.width, tc.height, tc.want)
			}
			layer.Visible, layer.Hit, layer.Opacity = true, HitProvince, 255
			if err := layer.SetBounds(Rect{Width: int32(tc.width), Height: int32(tc.height)}); err != nil {
				t.Fatal(err)
			}
			if layer.HitTest(0, 0) != layer {
				t.Fatal("tiled province did not drive hit testing")
			}
			root := NewRootLayer(99, int32(tc.width), int32(tc.height))
			if err := root.AddChild(layer); err != nil {
				t.Fatal(err)
			}
			compositor := Compositor{Width: int32(tc.width), Height: int32(tc.height)}
			frame, err := compositor.Compose(root)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(frame.Province, tc.want) {
				t.Fatalf("composited province = %v; want %v", frame.Province, tc.want)
			}
		})
	}
}

func TestNativeLayerLoadProvinceImageRejectsMalformedData(t *testing.T) {
	project := t.TempDir()
	writeLayerImagePNGSize(t, filepath.Join(project, "art", "sprite.png"), 2, 1)
	if err := os.MkdirAll(filepath.Join(project, "art"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "art", "malformed.png"), []byte("not an image"), 0o644); err != nil {
		t.Fatal(err)
	}
	storage, err := assets.OpenStorage(project, core.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	session := NewSession(NewServices(storage, t.TempDir(), core.DefaultLimits()), 20_000)
	defer session.Close()
	if _, err := session.executeSource("<province-malformed>", `l = new Layer(); l.loadImages('art/sprite')`, false); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Evaluate(`l.loadProvinceImage('art/malformed')`); err == nil {
		t.Fatal("malformed province image was accepted")
	}
}
