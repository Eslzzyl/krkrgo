package runtime

import (
	"math"
	"math/rand/v2"
	"strconv"
	"testing"
)

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
