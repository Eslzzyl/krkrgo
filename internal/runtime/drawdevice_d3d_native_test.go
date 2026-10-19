package runtime

import (
	"strings"
	"testing"
)

func TestDrawDeviceD3DLinkInstallsNekoD3DSurface(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()

	if _, err := session.executeSource("<d3d-link-test>", `
		Plugins.link("plugin/drawdeviceD3DZ.dll");
		Plugins.link("plugin/drawdeviceD3D.dll");
		class RefImage extends D3DImage {
			var refCount;
			function RefImage(device) {
				super.D3DImage(...);
				refCount = 1;
			}
			function addRef() {
				++refCount;
				return this;
			}
		}
		class AffineLayer extends D3DLayer {
			function AffineLayer(device) {
				super.D3DLayer(device);
			}
		}
		image = new RefImage(null);
		layer = new AffineLayer(null);
		constants = [D3DLayer.DrawPlaneFront, D3DLayer.DrawPlaneBack, D3DLayer.DrawPlaneBoth].join(",");
		device = new D3D(64, 32);
		drawDevice = new DrawDeviceD3D(64, 32);
		hasSurface = [typeof D3DPicture, typeof D3DImage, typeof D3DLayer, typeof D3D, typeof DrawDeviceD3D].join(",");
		size = device.width + "x" + device.height;
	`, false); err != nil {
		t.Fatalf("link and derive the Neko D3D classes: %v", err)
	}
	value, err := session.Evaluate(`hasSurface + "|" + constants + "|" + size + "|" + image.width + "x" + image.height + "|" + layer.drawPlane`)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := value.Text(), "Object,Object,Object,Object,Object|0,1,2|64x32|0x0|0"; got != want {
		t.Fatalf("D3D surface = %q, want %q", got, want)
	}
	if len(session.Services.Plugins) != 2 {
		t.Fatalf("linked D3D modules = %v, want D3DZ and D3D", session.Services.Plugins)
	}
}

func TestD3DImageLoadAndPictureCapturePreservePixels(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()

	if _, err := session.executeSource("<d3d-image-capture-test>", `
		Plugins.link("plugin/drawdeviceD3DZ.dll");
		source = new Layer();
		source.setImageSize(2, 2);
		source.fillRect(0, 0, 2, 2, 0xFF112233);

		device = new D3D(4, 4);
		device.clearColor = 0xFF000000;
		image = new D3DImage(device);
		image.load(source);
		layer = new D3DLayer(device);
		picture = new D3DPicture(layer, image);
		picture.assignImageRange(0, 0, image.width, image.height, 0, 0);
		picture.setCoord(0, 0);
		picture.opacity = 255;
		picture.blendMode = 1;
		layer.drawPlane = D3DLayer.DrawPlaneFront;
		layer.visible = true;

		device.update(16);
		out = new Layer();
		device.capture(out, 0);
	`, false); err != nil {
		t.Fatalf("load D3DImage and capture a picture: %v", err)
	}
	value, err := session.Evaluate(`[image.width, image.height, out.width, out.height, out.getMainPixel(0,0), out.getMainPixel(1,1), out.getMainPixel(3,3), out.getMaskPixel(0,0)].join(",")`)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := value.Text(), "2,2,4,4,1122867,1122867,0,255"; got != want {
		t.Fatalf("D3DImage/picture capture = %q, want %q", got, want)
	}
}

func TestD3DLayerMatrixScalesCapturedPicture(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()

	if _, err := session.executeSource("<d3d-layer-matrix-test>", `
		Plugins.link("plugin/drawdeviceD3DZ.dll");
		source = new Layer();
		source.setImageSize(2, 2);
		source.fillRect(0, 0, 2, 2, 0xFF224466);
		device = new D3D(4, 4);
		device.clearColor = 0xFF000000;
		image = new D3DImage(device);
		image.load(source);
		layer = new D3DLayer(device);
		picture = new D3DPicture(layer, image);
		picture.assignImageRange(0, 0, 2, 2, 0, 0);
		picture.opacity = 255;
		picture.blendMode = 1;
		layer.setMatrix(2, 0, 0, 0, 0, 2, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1);
		device.update(16);
		out = new Layer();
		device.capture(out, 0);
	`, false); err != nil {
		t.Fatalf("render a D3DLayer matrix: %v", err)
	}
	value, err := session.Evaluate(`[out.getMainPixel(0,0), out.getMainPixel(3,3), out.getMainPixel(3,0), out.getMainPixel(0,3)].join(",")`)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := value.Text(), "2245734,2245734,2245734,2245734"; got != want {
		t.Fatalf("matrix-scaled layer pixels = %q, want %q", got, want)
	}
}

func TestD3DImageAndPictureRejectInvalidInputs(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()

	if _, err := session.executeSource("<d3d-invalid-test>", `
		Plugins.link("plugin/drawdeviceD3DZ.dll");
		device = new D3D(2, 2);
		image = new D3DImage(device);
		source = new Layer();
		source.setImageSize(1, 1);
		source.fillRect(0, 0, 1, 1, 0xFF000000);
		image.load(source);
		layer = new D3DLayer(device);
		picture = new D3DPicture(layer, image);
	`, false); err != nil {
		t.Fatalf("prepare D3D objects: %v", err)
	}
	for _, test := range []struct {
		source string
		want   string
	}{
		{`image.load("not-an-image")`, "D3DImage.load: source must be a Layer, Bitmap, or D3DImage"},
		{`picture.assignImageRange(0, 0, 2, 2, 0, 0)`, "D3DPicture.assignImageRange: source range"},
		{`new D3DPicture(null, image)`, "D3DPicture: first argument must be a D3DLayer"},
		{`new D3DPicture(layer, null)`, "D3DPicture: second argument must be a D3DImage"},
	} {
		if _, err := session.Evaluate(test.source); err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("%s error = %v, want %q", test.source, err, test.want)
		}
	}
}

func TestDrawDeviceD3DConfigurationAndTransitionsAreStateful(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()

	if _, err := session.executeSource("<d3d-plugin-test>", `Plugins.link("plugin/drawdeviceD3D.dll")`, false); err != nil {
		t.Fatal(err)
	}
	if _, err := session.executeSource("<d3d-drawdevice-test>", `
		device = new DrawDeviceD3D(8, 4);
		device.setSize(16, 8);
		device.setPrimarySize(4, 2);
		device.setScreenRect(1, 2, 3, 4);
		device.setOffset(5, 6);
		device.clearColor = 0xFF010203;
		device.stretchType = 2;
		device.bicubicParam = 0.25;
		device.startTransition(%[time: 100]);
		device.transState = 0.5;
		device.stopTransition();
		emoteEnabled = device.checkEnable("emote");
		emoteModule = device.getModule("emote");
	`, false); err != nil {
		t.Fatalf("configure DrawDeviceD3D: %v", err)
	}
	value, err := session.Evaluate(`[device.width, device.height, device.clearColor, device.transState, device.stretchType, device.bicubicParam, emoteEnabled, emoteModule === void].join(",")`)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := value.Text(), "16,8,4278256131,+0.0,2,0.25,0,1"; got != want {
		t.Fatalf("DrawDeviceD3D state = %q, want %q", got, want)
	}
}

func TestD3DImageReloadAndLayerRegistryUpdates(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()

	if _, err := session.executeSource("<d3d-image-reload-test>", `
		Plugins.link("plugin/drawdeviceD3DZ.dll");
		source = new Layer();
		source.setImageSize(2, 2);
		source.fillRect(0, 0, 2, 2, 0xFF102030);
		source.fillRect(1, 1, 1, 1, 0xFF405060);
		device = new D3D(2, 2);
		first = new D3DImage(device);
		first.load(source);
		second = new D3DImage(device);
		second.load(first);
		bitmap = new Bitmap(1, 1);
		bitmap.setPixel(0, 0, 0x123456);
		bitmap.setMaskPixel(0, 0, 255);
		third = new D3DImage(device);
		third.load(bitmap);
		layer = new D3DLayer(device);
		layer.frontIndex = 7;
		layer.backIndex = 8;
		layer.setClip(0, 0, 1, 1);
		registered = device.primaryLayers.count + "," + device.children.count;
		device.remove(layer);
		removed = device.primaryLayers.count + "," + device.children.count;
	`, false); err != nil {
		t.Fatalf("reload D3DImage and update layer registry: %v", err)
	}
	value, err := session.Evaluate(`[second.width, second.height, third.width, third.height, layer.frontIndex, layer.backIndex, registered, removed].join(",")`)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := value.Text(), "2,2,1,1,7,8,1,1,0,0"; got != want {
		t.Fatalf("reloaded image and registry = %q, want %q", got, want)
	}
}

func TestD3DPictureRangesClipAndLayerDraw(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()

	if _, err := session.executeSource("<d3d-picture-range-test>", `
		Plugins.link("plugin/drawdeviceD3DZ.dll");
		source = new Layer();
		source.setImageSize(2, 2);
		source.fillRect(0, 0, 2, 2, 0xFFFFFFFF);
		device = new D3D(2, 2);
		device.clearColor = 0xFF000000;
		image = new D3DImage(device);
		image.load(source);
		layer = new D3DLayer(device);
		picture = new D3DPicture(layer, image);
		picture.assignImageRange(0, 0, 1, 1, 1, 1);
		picture.opacity = 255;
		picture.blendMode = 1;
		picture.draw();
		layer.draw();
		device.update(0);
		out = new Layer();
		device.capture(out, 0);
		corner = out.getMainPixel(1,1);

		picture.clearImageRange();
		layer.setClip(0, 0, 1, 1);
		layer.update(0);
		clipped = new Layer();
		device.capture(clipped, 0);
	`, false); err != nil {
		t.Fatalf("exercise picture ranges, clip, and layer draw: %v", err)
	}
	value, err := session.Evaluate(`[corner, clipped.getMainPixel(0,0), clipped.getMainPixel(1,1)].join(",")`)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := value.Text(), "16777215,16777215,0"; got != want {
		t.Fatalf("picture range/clip pixels = %q, want %q", got, want)
	}
}
