package runtime

import (
	"math"
	"math/rand/v2"
	"slices"
	"strconv"
	"testing"

	"github.com/eslzzyl/krkrgo/internal/tjs"
)

func TestLayerUpdateInvalidatesAffineMatrixCache(t *testing.T) {
	session := NewSession(DefaultServices(), 10_000)
	t.Cleanup(func() { _ = session.Close() })
	if _, err := session.executeSource("<affine-cache-test>", `
class AffineLike extends Layer {
	var _mtx;
	var _doAffine;
	var next;
	function AffineLike() {
		super.Layer();
		_mtx = void;
		_doAffine = 2;
		next = 0;
	}
	property matrix {
		getter() {
			if (_mtx === void) _mtx = ++next;
			return _mtx;
		}
	}
	function calcAffine() {
		if (_doAffine < 2) {
			_mtx = void;
			_doAffine = 2;
		}
		update(0, 0, 1, 1);
	}
}
layer = new AffineLike();
first = layer.matrix;
layer.calcAffine();
second = layer.matrix;
`, false); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]int64{"first": 1, "second": 2} {
		value, err := session.Evaluate(name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got, err := value.Int()
		if err != nil || got != want {
			t.Fatalf("%s = %v, %v; want %d", name, value, err, want)
		}
	}
}

func TestNativeLayerOperateRectAndStretchCopyPixels(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	if _, err := session.executeSource("<layer-image-test>", `
	dst = new Layer(); src = new Layer();
	dst.setImageSize(4, 1); src.setImageSize(2, 1);
	dst.face = dfMain;
	dst.fillRect(0, 0, 4, 1, 0x7f102030);
	dst.holdAlpha = true;
	src.face = dfMain; src.type = ltOpaque;
	src.fillRect(0, 0, 1, 1, 0x80112233);
	src.fillRect(1, 0, 1, 1, 0xffaabbcc);
	dst.operateRect(1, 0, src, 0, 0, 1, 1);
	`, false); err != nil {
		t.Fatal(err)
	}
	readInt := func(source string) int64 {
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
	if got := [2]int64{readInt(`dst.getMainPixel(1,0)`), readInt(`dst.getMaskPixel(1,0)`)}; got != [2]int64{0x112233, 127} {
		t.Fatalf("operateRect copied pixel = %#x,%d, want %#x,127", got[0], got[1], 0x112233)
	}
	if _, err := session.executeSource("<layer-image-test>", `dst.holdAlpha = false; dst.face = dfBoth; dst.stretchCopy(0, 0, 4, 1, src, 0, 0, 2, 1, stNearest)`, false); err != nil {
		t.Fatal(err)
	}
	want := [4]int64{0x112233, 0x112233, 0xaabbcc, 0xaabbcc}
	for x, expected := range want {
		if got := readInt("dst.getMainPixel(" + strconv.Itoa(x) + ",0)"); got != expected {
			t.Fatalf("stretchCopy pixel %d = %#x, want %#x", x, got, expected)
		}
	}
}

func TestLayerConvertTypeMatchesReference(t *testing.T) {
	layer := NewLayer(1)
	layer.RGBA = []byte{200, 100, 50, 128}
	layer.Face = layerFaceAddAlpha
	if err := layer.ConvertType(layerFaceAlpha); err != nil {
		t.Fatal(err)
	}
	if got, want := layer.RGBA, []byte{100, 50, 25, 128}; !slices.Equal(got, want) {
		t.Fatalf("alpha to additive-alpha pixels = %v, want %v", got, want)
	}

	layer.Face = layerFaceAlpha
	if err := layer.ConvertType(layerFaceAddAlpha); err != nil {
		t.Fatal(err)
	}
	if got, want := layer.RGBA, []byte{199, 99, 49, 128}; !slices.Equal(got, want) {
		t.Fatalf("additive-alpha to alpha pixels = %v, want %v", got, want)
	}

	session := NewSession(DefaultServices(), 100_000)
	value, err := session.executeSource("<layer-convert-type-test>", `
layer = new Layer();
layer.face = dfAddAlpha;
layer.setImageSize(1, 1);
layer.fillRect(0, 0, 1, 1, 0x80402010);
layer.convertType(dfAlpha);
return layer;
`, false)
	if err != nil {
		t.Fatalf("TJS Layer.convertType dispatch: %v", err)
	}
	if value.Kind() != tjs.KindObject || value.IsNull() {
		t.Fatalf("Layer.convertType returned %v, want layer object", value)
	}
	native := session.Services.Layer(value.ObjectRef().Object)
	if native == nil || !native.callOnPaint {
		t.Fatalf("Layer.convertType did not queue onPaint")
	}
}
func TestBilinearRGBAPreservesTransparentEdgeColor(t *testing.T) {
	source := []byte{255, 0, 0, 255, 0, 0, 0, 0}
	got := bilinearRGBA(source, 0, 4, 0, 4, 0.5, 0, true)
	if got != ([4]byte{255, 0, 0, 128}) {
		t.Fatalf("premultiplied bilinear edge = %v, want [255 0 0 128]", got)
	}
}

func TestStretchCopyPixelsMatchesReference(t *testing.T) {
	random := rand.New(rand.NewPCG(0x2119, 0x34c1))
	for iteration := range 500 {
		sourceWidth, sourceHeight := int32(1+random.IntN(12)), int32(1+random.IntN(12))
		cropX, cropY := int32(random.IntN(int(sourceWidth))), int32(random.IntN(int(sourceHeight)))
		cropWidth := int32(1 + random.IntN(int(sourceWidth-cropX)))
		cropHeight := int32(1 + random.IntN(int(sourceHeight-cropY)))
		targetWidth, targetHeight := int32(1+random.IntN(13)), int32(1+random.IntN(13))
		outputWidth, outputHeight := int32(1+random.IntN(14)), int32(1+random.IntN(14))
		dx, dy := int32(random.IntN(int(targetWidth+outputWidth))-int(outputWidth/2)), int32(random.IntN(int(targetHeight+outputHeight))-int(outputHeight/2))
		filter := int32(random.IntN(3))
		face := []int32{layerFaceAlpha, layerFaceOpaque, layerFaceAddAlpha}[random.IntN(3)]
		holdAlpha := random.IntN(2) == 0
		alias := random.IntN(4) == 0 && sourceWidth == targetWidth && sourceHeight == targetHeight

		source := make([]byte, int(sourceWidth*sourceHeight*4))
		for i := range source {
			source[i] = byte(random.IntN(256))
		}
		destination := make([]byte, int(targetWidth*targetHeight*4))
		for i := range destination {
			destination[i] = byte(random.IntN(256))
		}
		layer := NewLayer(1)
		if err := layer.SetBounds(Rect{Width: targetWidth, Height: targetHeight}); err != nil {
			t.Fatal(err)
		}
		if alias {
			destination = source
		}
		if err := layer.SetRGBA(targetWidth, targetHeight, destination); err != nil {
			t.Fatal(err)
		}
		layer.Face, layer.HoldAlpha = face, holdAlpha
		if alias {
			source = layer.RGBA
		}
		sourceForReference := append([]byte(nil), source...)
		want := append([]byte(nil), layer.RGBA...)
		referenceStretchCopyPixels(want, targetWidth, dx, dy, outputWidth, outputHeight,
			sourceForReference, sourceWidth, cropX, cropY, cropWidth, cropHeight, filter, face, holdAlpha)
		if err := layer.stretchCopyPixels(source, sourceWidth, sourceHeight, dx, dy, outputWidth, outputHeight, cropX, cropY, cropWidth, cropHeight, filter); err != nil {
			t.Fatalf("iteration %d: %v", iteration, err)
		}
		for offset := range layer.RGBA {
			if layer.RGBA[offset] != want[offset] {
				t.Fatalf("iteration %d differs from reference at byte %d (got=%d want=%d, alias=%t filter=%d face=%d holdAlpha=%t src=%dx%d crop=(%d,%d %dx%d) dst=%dx%d output=(%d,%d %dx%d))", iteration, offset, layer.RGBA[offset], want[offset], alias, filter, face, holdAlpha, sourceWidth, sourceHeight, cropX, cropY, cropWidth, cropHeight, targetWidth, targetHeight, dx, dy, outputWidth, outputHeight)
			}
		}
	}
}
func TestStretchCopyPixelsLinearExhaustiveByteParity(t *testing.T) {
	const sourceWidth, sourceHeight = 256, 3
	const outputWidth, outputHeight = 511, 7
	source := make([]byte, sourceWidth*sourceHeight*4)
	for y := range sourceHeight {
		for x := range sourceWidth {
			offset := (y*sourceWidth + x) * 4
			source[offset], source[offset+1] = byte(x), byte(255-x)
			source[offset+2], source[offset+3] = byte(y*127), byte((x+y*85)%256)
		}
	}
	layer := NewLayer(1)
	if err := layer.SetBounds(Rect{Width: outputWidth, Height: outputHeight}); err != nil {
		t.Fatal(err)
	}
	if err := layer.SetRGBA(outputWidth, outputHeight, make([]byte, outputWidth*outputHeight*4)); err != nil {
		t.Fatal(err)
	}
	want := append([]byte(nil), layer.RGBA...)
	referenceStretchCopyPixels(want, outputWidth, 0, 0, outputWidth, outputHeight,
		source, sourceWidth, 0, 0, sourceWidth, sourceHeight, 2, layerFaceAlpha, false)
	if err := layer.stretchCopyPixels(source, sourceWidth, sourceHeight, 0, 0, outputWidth, outputHeight, 0, 0, sourceWidth, sourceHeight, 2); err != nil {
		t.Fatal(err)
	}
	for offset := range want {
		if layer.RGBA[offset] != want[offset] {
			t.Fatalf("linear exhaustive-byte parity differs at byte %d (got=%d want=%d)", offset, layer.RGBA[offset], want[offset])
		}
	}
}
func TestStretchCopyPixelsAliasAndDirectParity(t *testing.T) {
	const width, height = int32(4), int32(3)
	initial := make([]byte, width*height*4)
	for i := range initial {
		initial[i] = byte(i*37 + 11)
	}
	for _, filter := range []int32{0, 1, 2} {
		for _, face := range []int32{layerFaceAlpha, layerFaceOpaque, layerFaceAddAlpha} {
			for _, holdAlpha := range []bool{false, true} {
				alias := NewLayer(1)
				if err := alias.SetBounds(Rect{Width: width, Height: height}); err != nil {
					t.Fatal(err)
				}
				if err := alias.SetRGBA(width, height, initial); err != nil {
					t.Fatal(err)
				}
				alias.Face, alias.HoldAlpha = face, holdAlpha
				if err := alias.stretchCopyPixels(alias.RGBA, width, height, 0, 0, width, height, 0, 0, width, height, filter); err != nil {
					t.Fatal(err)
				}

				source := append([]byte(nil), initial...)
				direct := NewLayer(2)
				if err := direct.SetBounds(Rect{Width: width, Height: height}); err != nil {
					t.Fatal(err)
				}
				if err := direct.SetRGBA(width, height, initial); err != nil {
					t.Fatal(err)
				}
				direct.Face, direct.HoldAlpha = face, holdAlpha
				if err := direct.stretchCopyPixels(source, width, height, 0, 0, width, height, 0, 0, width, height, filter); err != nil {
					t.Fatal(err)
				}
				for offset := range alias.RGBA {
					if alias.RGBA[offset] != direct.RGBA[offset] {
						t.Fatalf("alias/direct mismatch filter=%d face=%d holdAlpha=%t at byte %d: %d != %d",
							filter, face, holdAlpha, offset, alias.RGBA[offset], direct.RGBA[offset])
					}
				}
			}
		}
	}
}

func TestStretchCopyPixelsPartialOverlapUsesSnapshot(t *testing.T) {
	const width, height = int32(2), int32(2)
	backing := make([]byte, width*height*4+4)
	for i := range backing {
		backing[i] = byte(i*31 + 7)
	}
	source := backing[:width*height*4]
	sourceSnapshot := append([]byte(nil), source...)
	layer := NewLayer(1)
	if err := layer.SetBounds(Rect{Width: width, Height: height}); err != nil {
		t.Fatal(err)
	}
	layer.RGBA = backing[4:]
	layer.ImageWidth, layer.ImageHeight = width, height
	want := append([]byte(nil), layer.RGBA...)
	referenceStretchCopyPixels(want, width, 0, 0, width, height,
		sourceSnapshot, width, 0, 0, width, height, 2, layerFaceAlpha, false)
	if err := layer.stretchCopyPixels(source, width, height, 0, 0, width, height, 0, 0, width, height, 2); err != nil {
		t.Fatal(err)
	}
	for offset := range want {
		if layer.RGBA[offset] != want[offset] {
			t.Fatalf("partial-overlap stretchCopy differs at byte %d (got=%d want=%d)", offset, layer.RGBA[offset], want[offset])
		}
	}
}

func referenceStretchCopyPixels(destination []byte, destinationWidth, dx, dy, width, height int32, source []byte, sourceWidth, sx, sy, cropWidth, cropHeight, filter, face int32, holdAlpha bool) {
	outLeft := max64(int64(dx), 0)
	outTop := max64(int64(dy), 0)
	outRight := min(int64(dx)+int64(width), int64(destinationWidth))
	outBottom := min(int64(dy)+int64(height), int64(len(destination))/(int64(destinationWidth)*4))
	if outRight <= outLeft || outBottom <= outTop {
		return
	}
	outWidth, outHeight := outRight-outLeft, outBottom-outTop
	output := make([]byte, int(outWidth*outHeight)*4)
	for y := int64(0); y < outHeight; y++ {
		for x := int64(0); x < outWidth; x++ {
			u := (float64(outLeft-int64(dx)+x) + 0.5) / float64(width)
			v := (float64(outTop-int64(dy)+y) + 0.5) / float64(height)
			px := float64(sx) + u*float64(cropWidth) - 0.5
			py := float64(sy) + v*float64(cropHeight) - 0.5
			if filter == 0 {
				ix := int(math.Floor(px + 0.5))
				iy := int(math.Floor(py + 0.5))
				ix = max(int(sx), min(ix, int(sx+cropWidth-1)))
				iy = max(int(sy), min(iy, int(sy+cropHeight-1)))
				sourceOffset := (iy*int(sourceWidth) + ix) * 4
				destinationOffset := int((y*outWidth + x) * 4)
				copy(output[destinationOffset:destinationOffset+4], source[sourceOffset:sourceOffset+4])
				continue
			}
			minX, minY := int(math.Floor(px)), int(math.Floor(py))
			maxX, maxY := min(minX+1, int(sx+cropWidth-1)), min(minY+1, int(sy+cropHeight-1))
			minX, minY = max(minX, int(sx)), max(minY, int(sy))
			fx, fy := px-float64(minX), py-float64(minY)
			if fx < 0 {
				fx = 0
			}
			if fy < 0 {
				fy = 0
			}
			if fx > 1 {
				fx = 1
			}
			if fy > 1 {
				fy = 1
			}
			indices := [4]int{(minY*int(sourceWidth) + minX) * 4, (minY*int(sourceWidth) + maxX) * 4, (maxY*int(sourceWidth) + minX) * 4, (maxY*int(sourceWidth) + maxX) * 4}
			destinationOffset := int((y*outWidth + x) * 4)
			for channel := range 4 {
				topValue := float64(source[indices[0]+channel])*(1-fx) + float64(source[indices[1]+channel])*fx
				bottomValue := float64(source[indices[2]+channel])*(1-fx) + float64(source[indices[3]+channel])*fx
				output[destinationOffset+channel] = uint8(math.Round(topValue*(1-fy) + bottomValue*fy))
			}
		}
	}
	for y := int64(0); y < outHeight; y++ {
		for x := int64(0); x < outWidth; x++ {
			di := ((outTop+y)*int64(destinationWidth) + outLeft + x) * 4
			si := (y*outWidth + x) * 4
			copyCount := 4
			if face == layerFaceOpaque && holdAlpha {
				copyCount = 3
			}
			copy(destination[di:di+int64(copyCount)], output[si:si+int64(copyCount)])
		}
	}
}

func BenchmarkStretchCopyPixels(b *testing.B) {
	for _, size := range []struct {
		name                      string
		sourceWidth, sourceHeight int32
		outputWidth, outputHeight int32
	}{
		{name: "1280x720", sourceWidth: 640, sourceHeight: 360, outputWidth: 1280, outputHeight: 720},
		{name: "title-small-640x360", sourceWidth: 320, sourceHeight: 180, outputWidth: 640, outputHeight: 360},
	} {
		b.Run(size.name, func(b *testing.B) {
			source := NewLayer(1)
			if err := source.SetBounds(Rect{Width: size.sourceWidth, Height: size.sourceHeight}); err != nil {
				b.Fatal(err)
			}
			sourcePixels := make([]byte, int(size.sourceWidth*size.sourceHeight*4))
			for i := 0; i < len(sourcePixels); i += 4 {
				sourcePixels[i], sourcePixels[i+1], sourcePixels[i+2], sourcePixels[i+3] = byte(i), byte(i>>3), byte(i>>7), 255
			}
			if err := source.SetRGBA(size.sourceWidth, size.sourceHeight, sourcePixels); err != nil {
				b.Fatal(err)
			}
			dst := NewLayer(2)
			if err := dst.SetBounds(Rect{Width: size.outputWidth, Height: size.outputHeight}); err != nil {
				b.Fatal(err)
			}
			if err := dst.SetRGBA(size.outputWidth, size.outputHeight, make([]byte, int(size.outputWidth*size.outputHeight*4))); err != nil {
				b.Fatal(err)
			}
			b.SetBytes(int64(size.outputWidth * size.outputHeight * 4))
			for _, filter := range []struct {
				name  string
				value int32
			}{{"nearest", 0}, {"linear", 2}} {
				b.Run(filter.name, func(b *testing.B) {
					b.ReportAllocs()
					b.ResetTimer()
					for range b.N {
						if err := dst.stretchCopyPixels(source.RGBA, source.ImageWidth, source.ImageHeight, 0, 0, size.outputWidth, size.outputHeight, 0, 0, size.sourceWidth, size.sourceHeight, filter.value); err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		})
	}
}

func TestNativeLayerImageTransformsAndAssignImages(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	if _, err := session.executeSource("<layer-image-transform-test>", `
	src = new Layer(); dst = new Layer();
	src.setImageSize(3, 1); src.face = dfMain;
	src.fillRect(0, 0, 3, 1, 0xff000000);
	src.setMainPixel(0, 0, 0x400000); src.setMainPixel(1, 0, 0x404040); src.setMainPixel(2, 0, 0x787878);
	src.doGrayScale();
	src.adjustGamma(0.5, 0, 255, 0.5, 0, 255, 0.5, 0, 255);
	src.doBoxBlur(1, 0);
	dst.assignImages(src);
	`, false); err != nil {
		t.Fatal(err)
	}
	readInt := func(source string) int64 {
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
	if got := [3]int64{readInt(`src.imageWidth`), readInt(`dst.imageWidth`), readInt(`dst.getMainPixel(1,0)`)}; got != [3]int64{3, 3, 0x181818} {
		t.Fatalf("layer image copy/transform state = %v, want image widths 3 and gray pixel 0x181818", got)
	}
	if got := readInt(`src.getMainPixel(1,0)`); got != 0x181818 {
		t.Fatalf("transformed source center=%#x, want %#x", got, 0x181818)
	}
}
func TestNativeGPUCaptureDefersFullImageStretchCopy(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()
	session.Services.gpuCapture = true
	if _, err := session.executeSource("<gpu-stretch-native-test>", `
src = new Layer(); dst = new Layer();
src.setImageSize(2, 1); dst.setImageSize(8, 4);
src.fillRect(0, 0, 2, 1, 0xff204060);
dst.stretchCopy(0, 0, 4, 2, src, 0, 0, 2, 1, stFastLinear);
`, false); err != nil {
		t.Fatal(err)
	}
	value, err := session.Evaluate("dst")
	if err != nil {
		t.Fatal(err)
	}
	layer := session.Services.Layers[value.ObjectRef().Object]
	if layer == nil || layer.gpuStretch == nil {
		t.Fatal("native GPU capture did not defer full-image stretchCopy")
	}
}

func TestNativeGPUCaptureDefersGenericImageOperations(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()
	session.Services.gpuCapture = true
	if _, err := session.executeSource("<gpu-image-op-native-test>", `
src = new Layer(); copied = new Layer(); blended = new Layer(); affine = new Layer();
src.setImageSize(2, 1); copied.setImageSize(2, 1); blended.setImageSize(2, 1); affine.setImageSize(2, 1);
src.fillRect(0, 0, 2, 1, 0xff204060);
copied.copyRect(0, 0, src, 0, 0, 2, 1);
blended.operateRect(0, 0, src, 0, 0, 2, 1);
affine.affineCopy(src, 0, 0, 2, 1, false, 0, 0, 2, 0, 0, 1);
`, false); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"copied", "blended", "affine"} {
		value, err := session.Evaluate(name)
		if err != nil {
			t.Fatal(err)
		}
		layer := session.Services.Layers[value.ObjectRef().Object]
		if layer == nil || len(layer.gpuImageOps) != 1 {
			t.Fatalf("%s deferred operations = %d", name, len(layer.gpuImageOps))
		}
	}
	session.Services.gpuCapture = false
	value, err := session.Evaluate("copied.getMainPixel(0,0)")
	if err != nil {
		t.Fatal(err)
	}
	got, err := value.Int()
	if err != nil {
		t.Fatal(err)
	}
	if got != 0x204060 {
		t.Fatalf("materialized deferred copy pixel = %#x, want %#x", got, 0x204060)
	}
}

func TestMaterializeDeferredImageOpUsesRecordedFaceAndHoldAlpha(t *testing.T) {
	source := []byte{
		29, 171, 223, 255, 29, 171, 223, 255,
		29, 171, 223, 255, 29, 171, 223, 255,
	}
	native := nativeLayerImage{width: 2, height: 2, pixels: source, mode: int32(LayerAlpha)}

	// operateRect recorded with holdAlpha keeps the transparent destination
	// alpha even after the issuing script restored holdAlpha.
	operated := NewLayer(1)
	if err := operated.SetRGBA(2, 2, make([]byte, 2*2*4)); err != nil {
		t.Fatal(err)
	}
	operated.HoldAlpha = true
	if !operated.deferGPUOperateRect(native, 0, 0, 0, 0, 2, 2, int32(LayerPSOverlay), 255) {
		t.Fatal("operateRect was not deferred")
	}
	operated.HoldAlpha = false
	if err := operated.materializeGPUImageOps(); err != nil {
		t.Fatal(err)
	}
	for i := 3; i < len(operated.RGBA); i += 4 {
		if operated.RGBA[i] != 0 {
			t.Fatalf("operated alpha[%d] = %d, want 0 from recorded holdAlpha", i/4, operated.RGBA[i])
		}
	}

	// copyRect recorded with an opaque face and holdAlpha copies RGB only.
	copied := NewLayer(2)
	if err := copied.SetRGBA(2, 2, make([]byte, 2*2*4)); err != nil {
		t.Fatal(err)
	}
	copied.Face = layerFaceOpaque
	copied.HoldAlpha = true
	if !copied.deferGPUCopyRect(native, 0, 0, 0, 0, 2, 2) {
		t.Fatal("copyRect was not deferred")
	}
	copied.HoldAlpha = false
	if err := copied.materializeGPUImageOps(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < len(copied.RGBA); i += 4 {
		if copied.RGBA[i] != 29 || copied.RGBA[i+1] != 171 || copied.RGBA[i+2] != 223 || copied.RGBA[i+3] != 0 {
			t.Fatalf("copied pixel[%d] = %v, want source RGB with recorded holdAlpha", i/4, copied.RGBA[i:i+4])
		}
	}
}

func TestNativeLayerLightAdjustsRGBAndPreservesAlpha(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	if _, err := session.executeSource("<layer-light-test>", `
src = new Layer(); src.setImageSize(4, 1); src.face = dfBoth;
src.fillRect(0, 0, 1, 1, 0x80406080);
src.fillRect(1, 0, 1, 1, 0xff646464);
src.fillRect(2, 0, 1, 1, 0x40ffffff);
src.fillRect(3, 0, 1, 1, 0x00112233);
src.light(10, 20);
`, false); err != nil {
		t.Fatal(err)
	}
	for x, want := range []int64{0x3d648a, 0x686868, 0xffffff, 0x112233} {
		value, err := session.Evaluate("src.getMainPixel(" + strconv.Itoa(x) + ",0)")
		if err != nil {
			t.Fatal(err)
		}
		got, err := value.Int()
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("RGB pixel %d = %#x, want %#x", x, got, want)
		}
		alpha, err := session.Evaluate("src.getMaskPixel(" + strconv.Itoa(x) + ",0)")
		if err != nil {
			t.Fatal(err)
		}
		gotAlpha, err := alpha.Int()
		if err != nil {
			t.Fatal(err)
		}
		if wantAlpha := []int64{128, 255, 64, 0}[x]; gotAlpha != wantAlpha {
			t.Errorf("alpha pixel %d = %d, want %d", x, gotAlpha, wantAlpha)
		}
	}
}

func TestNativeLayerCopyRectLayerBitmapAndOverlap(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	if _, err := session.executeSource("<layer-copy-rect-test>", `
strip = new Layer(); cell = new Layer();
strip.setImageSize(384, 24); cell.setImageSize(24, 24);
strip.face = dfBoth; cell.face = dfBoth;
strip.fillRect(0, 0, 384, 24, 0xff010203);
strip.setMainPixel(0, 0, 0x445566); strip.setMaskPixel(0, 0, 0x77);
strip.setMainPixel(24, 0, 0x112233); strip.setMaskPixel(24, 0, 0x44);
strip.setMainPixel(47, 23, 0xaabbcc); strip.setMaskPixel(47, 23, 0xee);
cell.fillRect(0, 0, 24, 24, 0xff778899);
cell.copyRect(0, 0, strip, 24, 0, 24, 24);

bitmap = new Bitmap(1, 1); bitmap.setPixel(0, 0, 0xabcdef); bitmap.setMaskPixel(0, 0, 0x35);
bitmapDst = new Layer(); bitmapDst.setImageSize(1, 1); bitmapDst.face = dfBoth;
bitmapDst.copyRect(0, 0, bitmap, 0, 0, 1, 1);

overlap = new Layer(); overlap.setImageSize(4, 1); overlap.face = dfBoth;
overlap.fillRect(0, 0, 1, 1, 0x11111111); overlap.fillRect(1, 0, 1, 1, 0x22222222);
overlap.fillRect(2, 0, 1, 1, 0x33333333); overlap.fillRect(3, 0, 1, 1, 0x44444444);
overlap.copyRect(1, 0, overlap, 0, 0, 3, 1);
overlap.face = dfOpaque; overlap.holdAlpha = true;
overlap.copyRect(0, 0, overlap, 1, 0, 3, 1);
`, false); err != nil {
		t.Fatal(err)
	}
	readInt := func(source string) int64 {
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

	if got := [4]int64{
		readInt(`cell.getMainPixel(0,0)`), readInt(`cell.getMaskPixel(0,0)`),
		readInt(`cell.getMainPixel(23,23)`), readInt(`cell.getMaskPixel(23,23)`),
	}; got != [4]int64{0x112233, 0x44, 0xaabbcc, 0xee} {
		t.Fatalf("copyRect CAWorks strip cell = %#x,%d,%#x,%d", got[0], got[1], got[2], got[3])
	}
	if got := [2]int64{readInt(`bitmapDst.getMainPixel(0,0)`), readInt(`bitmapDst.getMaskPixel(0,0)`)}; got != [2]int64{0xabcdef, 0x35} {
		t.Fatalf("copyRect Bitmap pixel = %#x,%d, want %#x,%d", got[0], got[1], 0xabcdef, 0x35)
	}
	wantMain := [4]int64{0x111111, 0x222222, 0x333333, 0x333333}
	wantMask := [4]int64{0x11, 0x11, 0x22, 0x33}
	for x := range wantMain {
		main := readInt("overlap.getMainPixel(" + strconv.Itoa(x) + ",0)")
		mask := readInt("overlap.getMaskPixel(" + strconv.Itoa(x) + ",0)")
		if main != wantMain[x] || mask != wantMask[x] {
			t.Fatalf("copyRect overlap/holdAlpha pixel %d = %#x,%d, want %#x,%d", x, main, mask, wantMain[x], wantMask[x])
		}
	}
}
func TestNativeLayerStitchWrappedCopyTilesAndClips(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	if _, err := session.executeSource("<layer-stitch-wrapped-copy-test>", `
if (!Storages.isExistentStorage("plugin64/layerStwCopy.dll")) throw "layerStwCopy.dll unavailable";
Plugins.link("layerStwCopy.dll");
src = new Layer(); dst = new Layer(); edge = new Layer();
src.setImageSize(2, 2); dst.setImageSize(4, 3); edge.setImageSize(1, 2);
src.face = dfMain; dst.face = dfMain; edge.face = dfMain;
src.setMainPixel(0, 0, 0x110101); src.setMainPixel(1, 0, 0x220202);
src.setMainPixel(0, 1, 0x330303); src.setMainPixel(1, 1, 0x440404);
dst.stitchWrappedCopy(-1, 0, 5, 3, src, 0, 0, 2, 2, []);
src.setClip(1, 0, 1, 2);
edge.stitchWrappedCopy(0, 0, 1, 2, src, 0, 0, 2, 2, []);
`, false); err != nil {
		t.Fatal(err)
	}

	want := [3][4]int64{
		{0x220202, 0x110101, 0x220202, 0x110101},
		{0x440404, 0x330303, 0x440404, 0x330303},
		{0x220202, 0x110101, 0x220202, 0x110101},
	}
	for y, row := range want {
		for x, expected := range row {
			value, err := session.Evaluate("dst.getMainPixel(" + strconv.Itoa(x) + "," + strconv.Itoa(y) + ")")
			if err != nil {
				t.Fatal(err)
			}
			got, err := value.Int()
			if err != nil {
				t.Fatal(err)
			}
			if got != expected {
				t.Errorf("pixel (%d,%d) = %#x, want %#x", x, y, got, expected)
			}
		}
	}
	for y, expected := range []int64{0x220202, 0x440404} {
		value, err := session.Evaluate("edge.getMainPixel(0," + strconv.Itoa(y) + ")")
		if err != nil {
			t.Fatal(err)
		}
		got, err := value.Int()
		if err != nil || got != expected {
			t.Fatalf("edge pixel y=%d = %#x, want %#x (err %v)", y, got, expected, err)
		}
	}
}

func TestNativeLayerAffineCopyPointsPreservesPixelAlignment(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	if _, err := session.executeSource("<layer-affine-points-test>", `
src = new Layer(); dst = new Layer();
src.setImageSize(2, 1); src.face = dfBoth;
src.fillRect(0, 0, 1, 1, 0xff102030);
src.fillRect(1, 0, 1, 1, 0xffa0b0c0);
dst.setImageSize(2, 1); dst.face = dfBoth;
dst.affineCopy(src, 0, 0, 2, 1, false, -0.5, -0.5, 1.5, -0.5, -0.5, 0.5, stNearest | stRefNoClip, true);
`, false); err != nil {
		t.Fatal(err)
	}
	read := func(expression string) int64 {
		t.Helper()
		value, err := session.Evaluate(expression)
		if err != nil {
			t.Fatal(expression, err)
		}
		result, err := value.Int()
		if err != nil {
			t.Fatal(expression, err)
		}
		return result
	}
	if got := [2]int64{read(`dst.getMainPixel(0,0)`), read(`dst.getMainPixel(1,0)`)}; got != [2]int64{0x102030, 0xa0b0c0} {
		t.Fatalf("affineCopy points RGB = %#x, want %#x", got, [2]int64{0x102030, 0xa0b0c0})
	}
	if got := [2]int64{read(`dst.getMaskPixel(0,0)`), read(`dst.getMaskPixel(1,0)`)}; got != [2]int64{255, 255} {
		t.Fatalf("affineCopy points alpha = %v, want [255 255]", got)
	}
}

func TestNativeLayerAffineCopyScaledPointsWritesAlpha(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	if _, err := session.executeSource("<layer-affine-scaled-points-test>", `
src = new Layer(); dst = new Layer();
src.setImageSize(4, 4); src.face = dfBoth;
src.fillRect(0, 0, 4, 4, 0xff123456);
dst.setImageSize(20, 20); dst.face = dfBoth;
dst.affineCopy(src, 0, 0, 4, 4, false, 9.5, 9.5, 11.5, 9.5, 9.5, 11.5, stNearest | stRefNoClip, true);
`, false); err != nil {
		t.Fatal(err)
	}
	value, err := session.Evaluate(`dst.getMaskPixel(10,10)`)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := value.Int(); err != nil {
		t.Fatal(err)
	} else if got != 255 {
		t.Fatalf("scaled affineCopy alpha = %d, want 255", got)
	}
}

func TestNativeLayerAffineCopyInterpolationStaysInsideSourceImage(t *testing.T) {
	// A bilinear sample on the last source row or column reads its neighbour
	// tap one past the plane; the copy must clamp those taps to the image.
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()
	if _, err := session.executeSource("<layer-affine-interpolation-test>", `
src = new Layer(); dst = new Layer();
src.setImageSize(2, 2); src.face = dfBoth;
src.fillRect(0, 0, 2, 2, 0xff204060);
dst.setImageSize(2, 2); dst.face = dfBoth;
dst.affineCopy(src, 0, 0, 2, 2, false, 0.25, 0.25, 2.25, 0.25, 0.25, 2.25, stRefNoClip, true);
interpolated = dst.getMainPixel(1, 1);
`, false); err != nil {
		t.Fatal(err)
	}
	value, err := session.Evaluate(`interpolated`)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := value.Int(); err != nil {
		t.Fatal(err)
	} else if got != 0x204060 {
		t.Fatalf("interpolated edge pixel = %#x, want %#x", got, 0x204060)
	}
}

func TestLayerAffineCopyClampsInterpolationTapsToPlane(t *testing.T) {
	// Sampling near the bottom-right corner with bilinear filtering must clamp
	// the neighbour taps to the source plane instead of reading past it.
	source := &Layer{ImageWidth: 2, ImageHeight: 2, RGBA: make([]byte, 16)}
	for i := range 4 {
		source.RGBA[i*4], source.RGBA[i*4+1], source.RGBA[i*4+2], source.RGBA[i*4+3] = 0x20, 0x40, 0x60, 0xff
	}
	destination := &Layer{ImageWidth: 2, ImageHeight: 2, RGBA: make([]byte, 16)}
	err := destination.affineCopyPixels(source.RGBA, 2, 2, 0, 0, 2, 2,
		[6]float64{1, 0, 0, 1, -1.4, -1.4}, 1, false)
	if err != nil {
		t.Fatalf("affineCopyPixels: %v", err)
	}
	if got := [4]byte{destination.RGBA[0], destination.RGBA[1], destination.RGBA[2], destination.RGBA[3]}; got != [4]byte{0x20, 0x40, 0x60, 0xff} {
		t.Fatalf("corner pixel = %v, want [32 64 96 255]", got)
	}
}

func TestLayerAffineCopyIdentityCopiesIntoTallerDestination(t *testing.T) {
	source := &Layer{
		Type:        LayerAlpha,
		ImageWidth:  2,
		ImageHeight: 2,
		RGBA: []byte{
			10, 20, 30, 40, 50, 60, 70, 80,
			90, 100, 110, 120, 130, 140, 150, 160,
		},
	}
	destination := &Layer{
		Type:        LayerAlpha,
		ImageWidth:  2,
		ImageHeight: 3,
		RGBA:        make([]byte, 24),
	}
	for i := range destination.RGBA {
		destination.RGBA[i] = 255
	}
	if err := destination.affineCopyPixels(source.RGBA, 2, 2, 0, 0, 2, 2,
		[6]float64{1, 0, 0, 1, 0, 0}, 1, true); err != nil {
		t.Fatalf("affineCopyPixels: %v", err)
	}
	want := []byte{
		10, 20, 30, 40, 50, 60, 70, 80,
		90, 100, 110, 120, 130, 140, 150, 160,
		255, 255, 255, 0, 255, 255, 255, 0,
	}
	if string(destination.RGBA) != string(want) {
		t.Fatalf("identity affine result = %v, want %v", destination.RGBA, want)
	}
}

func TestLayerOperateRectAlphaFaceOverTransparentBackdrop(t *testing.T) {
	// Layered image composition copies button artwork with operateRect from a
	// composite onto an alpha face that a preceding clear left fully
	// transparent. bmAlpha must hand the layer the source pixels there; keeping
	// the zero backdrop color instead renders the artwork black.
	session := NewSession(DefaultServices(), 100_000)
	if _, err := session.executeSource("<layer-alpha-face-test>", `
	src = new Layer(); dst = new Layer();
	src.setImageSize(1, 1); dst.setImageSize(1, 1);
	src.face = dfBoth; src.fillRect(0, 0, 1, 1, 0x802468ac);
	dst.face = dfBoth; dst.fillRect(0, 0, 1, 1, 0x00000000);
	dst.operateRect(0, 0, src, 0, 0, 1, 1, omAlpha);
	`, false); err != nil {
		t.Fatal(err)
	}
	readInt := func(source string) int64 {
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
	if got := [2]int64{readInt(`dst.getMainPixel(0,0)`), readInt(`dst.getMaskPixel(0,0)`)}; got != [2]int64{0x2468ac, 0x80} {
		t.Fatalf("alpha face copy over transparent backdrop = %#x,%#x, want %#x,0x80", got[0], got[1], 0x2468ac)
	}
}

func TestLayerOperateRectAlphaFaceUpdatesAlphaWithHoldAlpha(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	if _, err := session.executeSource("<layer-alpha-face-hold-test>", `
		src = new Layer(); dst = new Layer();
		src.setImageSize(1, 1); dst.setImageSize(1, 1);
		src.face = dfBoth; src.fillRect(0, 0, 1, 1, 0x80112233);
		dst.face = dfBoth; dst.fillRect(0, 0, 1, 1, 0x00000000);
		dst.holdAlpha = true;
		dst.operateRect(0, 0, src, 0, 0, 1, 1, omAlpha);
	`, false); err != nil {
		t.Fatal(err)
	}
	value, err := session.Evaluate(`dst.getMaskPixel(0,0)`)
	if err != nil {
		t.Fatal(err)
	}
	alpha, err := value.Int()
	if err != nil {
		t.Fatal(err)
	}
	if alpha != 0x80 {
		t.Fatalf("alpha face with holdAlpha = %#x, want %#x", alpha, 0x80)
	}
}

func TestLayerOperateRectPhotoshopBlendAlphaRule(t *testing.T) {
	// tvpps.c TVPPS_ALPHABLEND stores the blended source, whose alpha channel is
	// the source's, and its _HDA form keeps the destination alpha byte. A
	// Photoshop blend over a transparent backdrop therefore paints the source
	// alpha instead of an opaque result.
	session := NewSession(DefaultServices(), 100_000)
	if _, err := session.executeSource("<layer-ps-blend-test>", `
	function blendInto(hold) {
		var src = new Layer(), dst = new Layer();
		src.setImageSize(1, 1); dst.setImageSize(1, 1);
		src.face = dfMain; src.fillRect(0, 0, 1, 1, 0x801dabdf);
		dst.face = dfMain; dst.fillRect(0, 0, 1, 1, 0x00000000);
		dst.holdAlpha = hold;
		dst.operateRect(0, 0, src, 0, 0, 1, 1, omPsOverlay, 255);
		return dst.getMaskPixel(0, 0);
	}
	held = blendInto(true); composed = blendInto(false);
	`, false); err != nil {
		t.Fatal(err)
	}
	readInt := func(source string) int64 {
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
	if got := [2]int64{readInt("held"), readInt("composed")}; got != [2]int64{0, 0x80} {
		t.Fatalf("photoshop overlay alpha = hold:%#x plain:%#x, want 0x0 and 0x80", got[0], got[1])
	}
}
