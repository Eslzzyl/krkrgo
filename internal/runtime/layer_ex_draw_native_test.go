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
matrix.Translate(2, 0, 1);
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

func TestLayerExDrawReferenceGeometryCompatibility(t *testing.T) {
	session := NewSession(DefaultServices(), 200_000)
	t.Cleanup(func() { _ = session.Close() })
	if _, err := session.executeSource("<layerexdraw-geometry-compat>", `
Plugins.link("layerExDraw.dll");
Plugins.link("saveStruct.dll");
saveStructOK = [1, 2].toStructString() == "[int 1,int 2]";
p = new GdiPlus.PointF(3, 4, 5);
pointArity = p.x == 3 && p.y == 4;
p.PointF(8, 9);
constructorReuse = p.x == 8 && p.y == 9;
p.finalize();
pointFinalize = p.x == 8 && p.y == 9;
class P extends GdiPlus.PointF{function P(){super.PointF(2,3);}function sum(){return x+y;}}
subclass = new P();
pointSubclass = subclass.sum() == 5 && subclass instanceof "PointF" && subclass instanceof "P";
c = new GdiPlus.RectF(1, 1, 1, 1);
union = GdiPlus.RectF.Union(c, [0, 0, 0, 0], [1, 1, 0, 0]);
unionStatic = union == 0 && typeof c.Union == "undefined" && c.x == 1;
emptyUnion = GdiPlus.RectF.Union(c, [0, 0, 0, 0], [0, 0, 0, 0]) == 0;
q = 1.0 / 8388608;
m = new GdiPlus.Matrix(1, 1 + q, 1 - q, 1, 3, 4);
matrixStatus = !m.IsInvertible() && m.Invert() == 2 && m.Equals([1, 1 + q, 1 - q, 1, 3, 4]);
a = new GdiPlus.Matrix(); b = new GdiPlus.Matrix();
a.Translate(3, 4, 0); b.Translate(3, 4, 0);
a.Scale(2, 3, 0); b.Scale(2, 3, 1);
matrixOrder = a.Equals([2, 0, 0, 3, 3, 4]) && b.Equals([2, 0, 0, 3, 6, 12]);
statusMatrix = new GdiPlus.Matrix(0, 0, 0, 0, 5, 6);
matrixStatusSequence = statusMatrix.Invert() == 2 && statusMatrix.GetLastStatus() == 2 && statusMatrix.GetLastStatus() == 0 && statusMatrix.Translate(1, 2, 99) == 2 && statusMatrix.GetLastStatus() == 2 && statusMatrix.Reset() == 0;
pointGetterSource = new GdiPlus.PointF(0, 0); getterTrace = "";
class PointGetter{property x{getter(){getterTrace += "x"; pointGetterSource.x=3; return 3;}} property y{getter(){getterTrace += "y"; pointGetterSource.y=4; return 4;}}}
pointGetter = pointGetterSource.Equals(new PointGetter()) && pointGetterSource.x == 3 && pointGetterSource.y == 4 && getterTrace == "yyxx";
rectGetterSource = new GdiPlus.RectF(0, 0, 10, 10); rectTrace = "";
class RectGetter{property x{getter(){rectTrace += "a"; rectGetterSource.x=20; return 2;}} property y{getter(){rectTrace += "d"; rectGetterSource.y=30; return 3;}}}
rectGetterSource.InflatePoint(new RectGetter());
rectGetter = rectGetterSource.x == 18 && rectGetterSource.y == 27 && rectGetterSource.width == 14 && rectGetterSource.height == 16 && rectTrace == "ddaa";
matrixGetterSource = new GdiPlus.Matrix(); matrixTrace = "";
class MatrixGetter{property m11{getter(){matrixTrace += "a"; matrixGetterSource.Translate(1,2,0); return 2;}} property m22{getter(){matrixTrace += "d"; return 3;}}}
matrixGetterSource.Multiply(new MatrixGetter(), 0);
matrixGetter = matrixGetterSource.OffsetX() == 2 && matrixGetterSource.OffsetY() == 4 && matrixGetterSource.Equals([2,0,0,3,2,4]) && matrixTrace == "ddaa";
`, false); err != nil {
		t.Fatal(err)
	}
	for expression, want := range map[string]int64{
		"saveStructOK":         1,
		"pointArity":           1,
		"constructorReuse":     1,
		"pointFinalize":        1,
		"pointSubclass":        1,
		"unionStatic":          1,
		"emptyUnion":           1,
		"matrixStatus":         1,
		"matrixOrder":          1,
		"matrixStatusSequence": 1,
		"pointGetter":          1,
		"rectGetter":           1,
		"matrixGetter":         1,
	} {
		value, err := session.Evaluate(expression)
		if err != nil {
			t.Fatalf("%s: %v", expression, err)
		}
		got, err := value.Int()
		if err != nil || got != want {
			t.Fatalf("%s = %v (%v), want %d", expression, got, err, want)
		}
	}
}

func TestLayerExDrawBrushSemantics(t *testing.T) {
	session := NewSession(DefaultServices(), 300_000)
	t.Cleanup(func() { _ = session.Close() })
	if _, err := session.executeSource("<layerexdraw-brush-compat>", `
Plugins.link("layerExDraw.dll");
gradientLayer = new Layer();
gradientLayer.setImageSize(4, 1);
gradientLayer.face = dfMain;
gradient = new GdiPlus.Appearance();
gradient.addBrush(%[type:GdiPlus.BrushTypeLinearGradient, point1:[0, 0], point2:[4, 0], color1:0xffff0000, color2:0xff0000ff]);
gradientLayer.drawRectangle(gradient, 0, 0, 4, 1);
pathLayer = new Layer();
pathLayer.setImageSize(4, 4);
pathLayer.face = dfMain;
path = new GdiPlus.Appearance();
path.addBrush(%[type:GdiPlus.BrushTypePathGradient, points:[[0, 0], [4, 0], [4, 4], [0, 4]], centerColor:0xffffffff, surroundColors:[0xff000000]]);
pathLayer.drawRectangle(path, 0, 0, 4, 4);
hatchLayer = new Layer();
hatchLayer.setImageSize(8, 8);
hatchLayer.face = dfMain;
hatch = new GdiPlus.Appearance();
hatch.addBrush(%[type:GdiPlus.BrushTypeHatchFill, hatchStyle:GdiPlus.HatchStyleHorizontal, foreColor:0xffff0000, backColor:0xff0000ff]);
hatchLayer.drawRectangle(hatch, 0, 0, 8, 8);
textureSource = new Layer();
textureSource.setImageSize(2, 1);
textureSource.face = dfMain;
textureSource.fillRect(0, 0, 1, 1, 0xffff0000);
textureSource.fillRect(1, 0, 1, 1, 0xff00ff00);
textureLayer = new Layer();
textureLayer.setImageSize(4, 1);
textureLayer.face = dfMain;
texture = new GdiPlus.Appearance();
texture.addBrush(%[type:GdiPlus.BrushTypeTextureFill, image:textureSource, dstRect:[0, 0, 4, 1], wrapMode:GdiPlus.WrapModeTile]);
textureLayer.drawRectangle(texture, 0, 0, 4, 1);
flatLayer = new Layer();
flatLayer.setImageSize(7, 4);
flatLayer.face = dfMain;
flat = new GdiPlus.Appearance();
flat.addPen(0xffff0000, %[width:2, startCap:GdiPlus.DashCapFlat, endCap:GdiPlus.DashCapFlat]);
flatLayer.drawLine(flat, 2, 2, 5, 2);
roundLayer = new Layer();
roundLayer.setImageSize(7, 4);
roundLayer.face = dfMain;
round = new GdiPlus.Appearance();
round.addPen(0xffff0000, %[width:2, startCap:GdiPlus.DashCapRound, endCap:GdiPlus.DashCapRound]);
roundLayer.drawLine(round, 2, 2, 5, 2);
`, false); err != nil {
		t.Fatal(err)
	}
	pixel := func(name string, x, y int) [4]byte {
		value, err := session.Evaluate(name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		layer := session.Services.Layer(value.ObjectRef().Object)
		if layer == nil {
			t.Fatalf("%s is not a layer", name)
		}
		i := (y*int(layer.deviceWidth()) + x) * 4
		return [4]byte{layer.RGBA[i], layer.RGBA[i+1], layer.RGBA[i+2], layer.RGBA[i+3]}
	}
	left := pixel("gradientLayer", 0, 0)
	right := pixel("gradientLayer", 3, 0)
	if left[0] <= left[2] || right[2] <= right[0] {
		t.Fatalf("linear gradient direction = left %v, right %v", left, right)
	}
	center := pixel("pathLayer", 2, 2)
	corner := pixel("pathLayer", 0, 0)
	if center[0] <= corner[0] || center[1] <= corner[1] || center[2] <= corner[2] {
		t.Fatalf("path gradient center/corner = %v/%v", center, corner)
	}
	if got := pixel("hatchLayer", 2, 0); got != [4]byte{0, 0, 255, 255} {
		t.Fatalf("hatch background = %v", got)
	}
	if got := pixel("hatchLayer", 2, 4); got != [4]byte{255, 0, 0, 255} {
		t.Fatalf("hatch foreground = %v", got)
	}
	if got := pixel("textureLayer", 0, 0); got != [4]byte{255, 0, 0, 255} {
		t.Fatalf("texture first tile = %v", got)
	}
	if got := pixel("textureLayer", 3, 0); got != [4]byte{0, 255, 0, 255} {
		t.Fatalf("texture second tile = %v", got)
	}
	if got := pixel("flatLayer", 1, 2); got[3] != 0 {
		t.Fatalf("flat cap alpha = %v", got)
	}
	if got := pixel("roundLayer", 1, 2); got[3] == 0 {
		t.Fatalf("round cap alpha = %v", got)
	}
}
