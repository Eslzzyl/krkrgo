package runtime

import "testing"

func TestLayerExDrawScriptSurfaceRasterizesShapesTransformsAndImages(t *testing.T) {
	session := NewSession(DefaultServices(), 200_000)
	t.Cleanup(func() { _ = session.Close() })
	if _, err := session.executeSource("<layerexdraw-surface>", `
Plugins.link("layerExDraw.dll");
layer = new Layer();
layer.setImageSize(8, 4);
layer.face = dfMain;
app = new GdiPlus.Appearance();
app.addBrush(0xffff0000);
layer.drawRectangle(app, 0, 0, 2, 2);
matrix = new GdiPlus.Matrix();
matrix.Translate(2, 0);
layer.setTransform(matrix);
layer.drawRectangle(app, 0, 0, 2, 2);
source = new Layer();
source.setImageSize(2, 1);
source.face = dfMain;
source.fillRect(0, 0, 1, 1, 0xffff0000);
source.fillRect(1, 0, 1, 1, 0xff00ff00);
layer.drawImage(4, 0, source);
crop = new Layer();
crop.setImageSize(1, 1);
crop.face = dfMain;
crop.drawImageRect(0, 0, source, 1, 0, 1, 1);
`, false); err != nil {
		t.Fatal(err)
	}
	value, err := session.Evaluate("layer")
	if err != nil {
		t.Fatal(err)
	}
	layer := session.Services.Layer(value.ObjectRef().Object)
	if layer == nil {
		t.Fatal("layer native object is unavailable")
	}
	pixel := func(x, y int32) [4]byte {
		i := (int(y)*int(layer.deviceWidth()) + int(x)) * 4
		return [4]byte{layer.RGBA[i], layer.RGBA[i+1], layer.RGBA[i+2], layer.RGBA[i+3]}
	}
	if got := pixel(0, 0); got != [4]byte{255, 0, 0, 255} {
		t.Fatalf("base rectangle pixel = %v, want red", got)
	}
	if got := pixel(2, 0); got != [4]byte{255, 0, 0, 255} {
		t.Fatalf("transformed rectangle pixel = %v, want red", got)
	}
	if got := pixel(6, 0); got != [4]byte{255, 0, 0, 255} {
		t.Fatalf("drawImage pixel = %v, want red", got)
	}
	cropValue, err := session.Evaluate("crop")
	if err != nil {
		t.Fatal(err)
	}
	cropLayer := session.Services.Layer(cropValue.ObjectRef().Object)
	if cropLayer == nil || len(cropLayer.RGBA) < 4 {
		t.Fatal("cropped image layer is unavailable")
	}
	if got := cropLayer.RGBA[:4]; got[0] != 0 || got[1] != 255 || got[2] != 0 || got[3] != 255 {
		t.Fatalf("drawImageRect pixel = %v, want green", got)
	}
}

func TestLayerExDrawRecordAndPointRectObjects(t *testing.T) {
	session := NewSession(DefaultServices(), 200_000)
	t.Cleanup(func() { _ = session.Close() })
	if _, err := session.executeSource("<layerexdraw-record>", `
Plugins.link("layerExDraw.dll");
layer = new Layer();
layer.setImageSize(4, 4);
layer.face = dfMain;
point = new GdiPlus.PointF(3, 5);
rect = new GdiPlus.RectF(point.x, point.y, 2, 3);
pointOK = point.Equals(new GdiPlus.PointF(3, 5));
rectOK = rect.width == 2 && rect.height == 3 && rect.right == 5 && rect.bottom == 8;
layer.record = true;
font = new GdiPlus.Font("", 12, GdiPlus.FontStyleRegular);
fontOK = font.emSize == 12 && typeof Font == "Object";
recordState = layer.record;
app = new GdiPlus.Appearance();
app.addBrush(0xff0000ff);
layer.drawEllipse(app, 0, 0, 2, 2);
recordImage = layer.getRecordImage();
recordKind = typeof recordImage;
recordWidth = recordImage === void ? -1 : recordImage.GetWidth();
recordHeight = recordImage === void ? -1 : recordImage.GetHeight();
`, false); err != nil {
		t.Fatal(err)
	}
	for expression, want := range map[string]int64{"pointOK": 1, "rectOK": 1, "fontOK": 1, "recordState": 1, "recordWidth": 4, "recordHeight": 4} {
		value, err := session.Evaluate(expression)
		if err != nil {
			t.Fatalf("%s: %v", expression, err)
		}
		got, err := value.Int()
		if err != nil || got != want {
			t.Fatalf("%s = %v (%v), want %d", expression, got, err, want)
		}
	}
	value, err := session.Evaluate("recordImage")
	if err != nil {
		t.Fatal(err)
	}
	if value.Kind() != 1 {
		t.Fatalf("recordImage kind = %v, want object", value.Kind())
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	session.Services.mu.RLock()
	drawStateCount, objectStateCount := len(session.Services.LayerExDraws), len(session.Services.LayerExObjects)
	session.Services.mu.RUnlock()
	if drawStateCount != 0 || objectStateCount != 0 {
		t.Fatalf("LayerExDraw state after session close = %d/%d, want 0/0", drawStateCount, objectStateCount)
	}
}
