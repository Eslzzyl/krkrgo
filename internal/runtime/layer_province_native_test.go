package runtime

import "testing"

func TestNativeLayerProvincePixels(t *testing.T) {
	services := DefaultServices()
	session := NewSession(services, 10_000)
	if _, err := session.executeSource("<layer-province-test>", `l = new Layer(); l.setImageSize(4,3)`, false); err != nil {
		t.Fatal(err)
	}
	layerValue, err := session.Evaluate("l")
	if err != nil {
		t.Fatal(err)
	}
	layer := services.Layer(layerValue.ObjectRef().Object)
	if layer == nil {
		t.Fatal("constructed Layer was not registered")
	}

	readPixel := func(source string) int64 {
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
	if got := [3]int64{readPixel(`l.getProvincePixel(0,0)`), readPixel(`l.getProvincePixel(-1,0)`), readPixel(`l.getProvincePixel(4,0)`)}; got != [3]int64{} {
		t.Fatalf("missing/out-of-range province pixels = %v, want zeroes", got)
	}
	if len(layer.Province) != 0 {
		t.Fatal("reading a missing province image allocated storage")
	}

	if _, err := session.Evaluate(`l.setClip(1,1,2,1)`); err != nil {
		t.Fatal(err)
	}
	value, err := session.Evaluate(`l.setProvincePixel(0,1,5)`)
	if err != nil {
		t.Fatal(err)
	}
	if !value.IsVoid() {
		t.Fatalf("setProvincePixel result = %v, want void", value)
	}
	if len(layer.Province) != 12 || layer.ProvinceWidth != 4 || layer.ProvinceHeight != 3 {
		t.Fatalf("lazy province image = %dx%d with %d bytes, want 4x3 with 12 bytes", layer.ProvinceWidth, layer.ProvinceHeight, len(layer.Province))
	}
	if got := readPixel(`l.getProvincePixel(0,1)`); got != 0 {
		t.Fatalf("pixel outside clip = %d, want 0", got)
	}

	for _, source := range []string{`l.setProvincePixel(1,1,258)`, `l.setProvincePixel(2,1,255)`} {
		if _, err := session.Evaluate(source); err != nil {
			t.Fatalf("%s: %v", source, err)
		}
	}
	if got := [4]int64{
		readPixel(`l.getProvincePixel(0,1)`),
		readPixel(`l.getProvincePixel(1,1)`),
		readPixel(`l.getProvincePixel(2,1)`),
		readPixel(`l.getProvincePixel(3,1)`),
	}; got != [4]int64{0, 2, 255, 0} {
		t.Fatalf("province pixels = %v, want [0 2 255 0]", got)
	}

	if _, err := session.executeSource("<layer-province-test>", `l.setClip(-10,-10,20,20); l.setProvincePixel(4,1,9); l.setProvincePixel(-1,1,9)`, false); err != nil {
		t.Fatal(err)
	}
	if got := [2]int64{readPixel(`l.getProvincePixel(4,1)`), readPixel(`l.getProvincePixel(-1,1)`)}; got != [2]int64{} {
		t.Fatalf("out-of-image province pixels = %v, want zeroes", got)
	}
}
