package runtime

import (
	"errors"
	"fmt"
	"image/color"
	"testing"

	"golang.org/x/image/font/basicfont"
)

func TestImageFunctionStaticPixelOperations(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	if _, err := session.executeSource("<image-function>", `
a = new Bitmap(3, 2);
b = new Bitmap(2, 1);
b.setPixel(0, 0, 16711680);
b.setMaskPixel(0, 0, 255);
b.setPixel(1, 0, 65280);
b.setMaskPixel(1, 0, 255);
ImageFunction.operateRect(a, 1, 0, b);
ImageFunction.flipLR(a);
ImageFunction.doGrayScale(a, new Rect(0, 0, 2, 1));
`, false); err != nil {
		t.Fatal(err)
	}
	value, err := session.Evaluate("a.getPixel(1, 0)")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := value.Int(); got == 0 {
		t.Fatalf("ImageFunction did not mutate destination pixel: %d", got)
	}
	if _, err := session.Evaluate("ImageFunction.fillRect(a, 2151686160, new Rect(0, 1, 3, 2))"); err != nil {
		t.Fatal(err)
	}
	value, err = session.Evaluate("a.getMaskPixel(0, 1)")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := value.Int(); got != 0x80 {
		t.Fatalf("fillRect alpha=%d want=128", got)
	}
}

func TestImageFunctionAffineNearestRotation(t *testing.T) {
	session := NewSession(DefaultServices(), 20_000)
	if _, err := session.executeSource("<image-function-affine>", `
dst = new Bitmap(2, 2);
src = new Bitmap(2, 2);
src.setPixel(0, 0, 16711680);
src.setPixel(1, 0, 65280);
src.setPixel(0, 1, 255);
src.setPixel(1, 1, 16776960);
src.setMaskPixel(0, 0, 255);
src.setMaskPixel(1, 0, 255);
src.setMaskPixel(0, 1, 255);
src.setMaskPixel(1, 1, 255);
ImageFunction.operateAffine(dst, src, 0, 1, -1, 0, 1, 0, null, null, true, 2, 1, 255, 0, false);
`, false); err != nil {
		t.Fatal(err)
	}

	for _, pixel := range []struct {
		x, y int
		want int64
	}{
		{0, 0, 255},
		{1, 0, 16711680},
		{0, 1, 16776960},
		{1, 1, 65280},
	} {
		value, err := session.Evaluate(fmt.Sprintf("dst.getPixel(%d, %d)", pixel.x, pixel.y))
		if err != nil {
			t.Fatal(err)
		}
		if got, _ := value.Int(); got != pixel.want {
			t.Errorf("dst.getPixel(%d, %d)=%d want=%d", pixel.x, pixel.y, got, pixel.want)
		}
	}
}

func TestImageFunctionSupportsSamplingHDAAndMaskModes(t *testing.T) {
	session := NewSession(DefaultServices(), 20_000)
	if _, err := session.executeSource("<image-function-advanced>", `
src = new Bitmap(2, 2);
src.setPixel(0, 0, 16711680); src.setMaskPixel(0, 0, 255);
src.setPixel(1, 0, 65280); src.setMaskPixel(1, 0, 255);
src.setPixel(0, 1, 255); src.setMaskPixel(0, 1, 255);
src.setPixel(1, 1, 16776960); src.setMaskPixel(1, 1, 255);
dst = new Bitmap(4, 4);
for (y = 0; y < 4; y++) for (x = 0; x < 4; x++) {
  dst.setPixel(x, y, 8421504); dst.setMaskPixel(x, y, 100);
}
ImageFunction.operateStretch(dst, src, new Rect(0, 0, 4, 4), null, null, 2, 0, 255, 2, true);
heldAlpha = dst.getMaskPixel(0, 0);
ImageFunction.operateAffine(dst, src, 0, 0, 0, 4, 4, 0, null, null, false, 2, 0, 255, 1, true);
ImageFunction.adjustGamma(dst, 2, 10, 240, 1, 20, 220, 0.5, 0, 255, null, true);
ImageFunction.colorRect(dst, 64, 255, null, 2);
glyph = [2, 1, 0, 0, 0, 0, 0, <% 00 0f %>, 16];
font = new Font("", -13);
ImageFunction.drawGlyph(dst, 0, 0, glyph, 16777215, 255, true);
`, false); err != nil {
		t.Fatal(err)
	}
	value, err := session.Evaluate("heldAlpha")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := value.Int(); got != 100 {
		t.Fatalf("hda changed destination alpha: %d", got)
	}
	value, err = session.Evaluate("dst.getMaskPixel(0, 0)")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := value.Int(); got != 64 {
		t.Fatalf("mask face alpha=%d want=64", got)
	}
}
func TestImageFunctionDrawTextUsesSelectedFaceOpacityAndClip(t *testing.T) {
	services := DefaultServices()
	var requestedSize float64
	if err := services.RegisterFontFactory("DrawTextFace", func(size float64) (FontFace, error) {
		requestedSize = size
		return basicfont.Face7x13, nil
	}); err != nil {
		t.Fatal(err)
	}
	session := NewSession(services, 10_000)
	if _, err := session.executeSource("<image-function-draw-text>", `
bitmap = new Bitmap(8, 13);
font = new Font("DrawTextFace", -13);
ImageFunction.drawText(bitmap, font, -1, 1, "A", 16711680, 128, true, 0, 0, 0, 0, 0, 0, false, new Rect(0, 2, 4, 13));
`, false); err != nil {
		t.Fatal(err)
	}
	if requestedSize != 13 {
		t.Fatalf("selected Font face size = %v, want 13", requestedSize)
	}
	value, err := session.Evaluate("bitmap")
	if err != nil {
		t.Fatal(err)
	}
	bitmap := services.Bitmap(value.ObjectRef().Object)
	rendered, err := RasterizeText(basicfont.Face7x13, "A", color.White)
	if err != nil {
		t.Fatal(err)
	}
	for y := range int(bitmap.Height) {
		for x := range int(bitmap.Width) {
			var want [4]byte
			sourceX, sourceY := x+1, y-1
			if x < 4 && y >= 2 && sourceX < rendered.Rect.Dx() && sourceY >= 0 && sourceY < rendered.Rect.Dy() {
				coverage := rendered.Pix[sourceY*rendered.Stride+sourceX*4+3]
				alpha := int(coverage) * 128 / 255
				if alpha != 0 {
					want = [4]byte{255, 0, 0, byte(alpha)}
				}
			}
			index := (y*int(bitmap.Width) + x) * 4
			got := bitmap.RGBA[index : index+4]
			for channel := range want {
				if got[channel] != want[channel] {
					t.Fatalf("pixel (%d,%d) = %v, want %v", x, y, got, want)
				}
			}
		}
	}
}

func TestImageFunctionDrawGlyphUsesBitmapMaskOpacityAndClip(t *testing.T) {
	services := DefaultServices()
	if err := services.RegisterFontFace("GlyphFace", basicfont.Face7x13); err != nil {
		t.Fatal(err)
	}
	session := NewSession(services, 10_000)
	if _, err := session.executeSource("<image-function-draw-glyph>", `
bitmap = new Bitmap(4, 13);
font = new Font("GlyphFace", -13);
ImageFunction.drawText(bitmap, font, 0, 0, "", 16777215, 0);
glyph = [3, 2, 0, 4, 0, 0, 0, <% 00 80 ff 40 00 80 %>];
ImageFunction.drawGlyph(bitmap, -1, 0, glyph, 65280, 128, true, 0, 0, 0, 0, 0, 0, false, new Rect(0, 0, 2, 13));
`, false); err != nil {
		t.Fatal(err)
	}
	value, err := session.Evaluate("bitmap")
	if err != nil {
		t.Fatal(err)
	}
	bitmap := services.Bitmap(value.ObjectRef().Object)
	ascent, err := MetricsForFace(basicfont.Face7x13)
	if err != nil {
		t.Fatal(err)
	}
	top := ascent.Ascent - 4
	wantAlpha := map[[2]int]byte{
		{0, top}:     64,
		{1, top}:     128,
		{1, top + 1}: 64,
	}
	for y := range int(bitmap.Height) {
		for x := range int(bitmap.Width) {
			index := (y*int(bitmap.Width) + x) * 4
			got := bitmap.RGBA[index : index+4]
			alpha := wantAlpha[[2]int{x, y}]
			want := [4]byte{}
			if alpha != 0 {
				want = [4]byte{0, 255, 0, alpha}
			}
			for channel := range want {
				if got[channel] != want[channel] {
					t.Fatalf("pixel (%d,%d) = %v, want %v", x, y, got, want)
				}
			}
		}
	}
}
func TestImageFunctionDrawGlyphUsesSupportedBlendFaces(t *testing.T) {
	services := DefaultServices()
	if err := services.RegisterFontFace("BlendFace", basicfont.Face7x13); err != nil {
		t.Fatal(err)
	}
	metrics, err := MetricsForFace(basicfont.Face7x13)
	if err != nil {
		t.Fatal(err)
	}
	session := NewSession(services, 10_000)
	source := fmt.Sprintf(`
font = new Font("BlendFace", -13);
alpha = new Bitmap(1, 1);
opaque = new Bitmap(1, 1);
addAlpha = new Bitmap(1, 1);
glyph = [1, 1, 0, %d, 0, 0, 0, <%% ff %%>];
alpha.setPixel(0, 0, 255); alpha.setMaskPixel(0, 0, 128);
opaque.setPixel(0, 0, 255); opaque.setMaskPixel(0, 0, 128);
addAlpha.setPixel(0, 0, 255); addAlpha.setMaskPixel(0, 0, 128);
ImageFunction.drawText(alpha, font, 0, 0, "", 16777215, 0);
ImageFunction.drawText(opaque, font, 0, 0, "", 16777215, 0);
ImageFunction.drawText(addAlpha, font, 0, 0, "", 16777215, 0);
ImageFunction.drawGlyph(alpha, 0, 0, glyph, 16711680, 128, true, 0);
ImageFunction.drawGlyph(opaque, 0, 0, glyph, 16711680, 128, true, 1);
ImageFunction.drawGlyph(addAlpha, 0, 0, glyph, 16711680, 128, true, 4);
`, metrics.Ascent)
	if _, err := session.executeSource("<image-function-blend-faces>", source, false); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		want [4]byte
	}{
		{"alpha", [4]byte{171, 0, 85, 191}},
		{"opaque", [4]byte{128, 0, 127, 192}},
		{"addAlpha", [4]byte{128, 0, 64, 255}},
	} {
		value, err := session.Evaluate(test.name)
		if err != nil {
			t.Fatal(err)
		}
		bitmap := services.Bitmap(value.ObjectRef().Object)
		for channel, want := range test.want {
			if got := bitmap.RGBA[channel]; got != want {
				t.Errorf("%s blend pixel channel %d = %d, want %d", test.name, channel, got, want)
			}
		}
	}
}

func TestImageFunctionDrawTextAndGlyphSupportsOptions(t *testing.T) {
	services := DefaultServices()
	services.SetFontBook(NewFontBook())
	if err := services.RegisterFontFace("ImageFunctionFace", basicfont.Face7x13); err != nil {
		t.Fatal(err)
	}
	session := NewSession(services, 20_000)
	if _, err := session.executeSource("<image-function-draw-options>", `
bitmap = new Bitmap(8, 13);
font = new Font("ImageFunctionFace", -13);
glyph = [1, 1, 0, 0, 0, 0, 0, <% ff %>];
ImageFunction.drawText(bitmap, font, 0, 0, "A", 16777215, 255, false, 0, 255, 255, 1, 1, 1, true);
ImageFunction.drawText(bitmap, font, 0, 0, "A", 16711680, 128, true, 2);
ImageFunction.drawGlyph(bitmap, 0, 0, glyph, 65280, 255, false, 0, 255, 255, 1, 1, 1, true);
ImageFunction.drawGlyph(bitmap, 0, 0, glyph, 255, 128, true, 3);
`, false); err != nil {
		t.Fatal(err)
	}
	value, err := session.Evaluate(`
sum = 0;
for (y = 0; y < 13; y++) for (x = 0; x < 8; x++) sum += bitmap.getMaskPixel(x, y);
sum
`)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := value.Int(); got == 0 {
		t.Fatal("drawText and drawGlyph produced no pixels")
	}
	if _, err := session.Evaluate(`ImageFunction.drawText(bitmap, font, 0, 0, "Ω", 16777215)`); err != nil {
		t.Fatalf("missing glyph did not use the selected face fallback: %v", err)
	}
}

func TestImageFunctionRejectsUnsupportedRendering(t *testing.T) {
	session := NewSession(DefaultServices(), 10_000)
	if _, err := session.executeSource("<image-function-unsupported>", "a = new Bitmap(2, 2); ImageFunction.drawText(a, null, 0, 0, 'x', 16777215)", false); err == nil {
		t.Fatal("drawText accepted a null Font")
	}
	if _, err := session.Evaluate("ImageFunction.operateStretch(a, a, null, null, null, 2, 0, 255, 20)"); !errors.Is(err, ErrUnsupportedNative) {
		t.Fatalf("stretch type error=%v", err)
	}
}

func TestImageFunctionMutatingCallsReturnUpdatedRect(t *testing.T) {
	session := NewSession(DefaultServices(), 20_000)
	if _, err := session.executeSource("<image-function-results>", `
a = new Bitmap(8, 8);
b = new Bitmap(2, 2);
rFill = ImageFunction.fillRect(a, 16711680, new Rect(1, 2, 4, 6));
rRect = ImageFunction.operateRect(a, 3, 1, b, new Rect(0, 0, 2, 2));
rStretch = ImageFunction.operateStretch(a, b, new Rect(0, 0, 6, 4), null, null, 2, 0, 255, 1);
rAffine = ImageFunction.operateAffine(a, b, 0, 1, 1, 0, 1, 1, null, null, true, 2, 0, 255, 0);
rFlip = ImageFunction.flipLR(a);
resultOK = rFill.left == 1 && rFill.top == 2 && rFill.right == 4 && rFill.bottom == 6
  && rRect.left == 3 && rRect.top == 1 && rRect.right == 5 && rRect.bottom == 3
  && rStretch.left == 0 && rStretch.top == 0 && rStretch.right == 6 && rStretch.bottom == 4
  && rAffine.left == 1 && rAffine.top == 1 && rAffine.right == 3 && rAffine.bottom == 3
  && rFlip.left == 0 && rFlip.top == 0 && rFlip.right == 8 && rFlip.bottom == 8;
`, false); err != nil {
		t.Fatal(err)
	}
	value, err := session.Evaluate("resultOK ? 1 : 0")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := value.Int(); got != 1 {
		t.Fatal("mutating ImageFunction calls returned incorrect Rect values")
	}
}

func TestImageFunctionStretchFiltersUseDistinctKernels(t *testing.T) {
	services := DefaultServices()
	session := NewSession(services, 40_000)
	if _, err := session.executeSource("<image-function-filters>", `
src = new Bitmap(4, 1);
src.setPixel(0, 0, 0); src.setMaskPixel(0, 0, 255);
src.setPixel(1, 0, 16777215); src.setMaskPixel(1, 0, 255);
src.setPixel(2, 0, 4194304); src.setMaskPixel(2, 0, 255);
src.setPixel(3, 0, 8388608); src.setMaskPixel(3, 0, 255);
	linear = new Bitmap(3, 1);
	cubic = new Bitmap(3, 1);
	lanczos2 = new Bitmap(3, 1);
	lanczos3 = new Bitmap(3, 1);
	spline16 = new Bitmap(3, 1);
	spline36 = new Bitmap(3, 1);
	area = new Bitmap(3, 1);
	gaussian = new Bitmap(3, 1);
	blackmanSinc = new Bitmap(3, 1);
	ImageFunction.operateStretch(linear, src, new Rect(0, 0, 3, 1), null, null, 2, 0, 255, 1);
	ImageFunction.operateStretch(cubic, src, new Rect(0, 0, 3, 1), null, null, 2, 0, 255, 3);
	ImageFunction.operateStretch(lanczos2, src, new Rect(0, 0, 3, 1), null, null, 2, 0, 255, 6);
	ImageFunction.operateStretch(lanczos3, src, new Rect(0, 0, 3, 1), null, null, 2, 0, 255, 8);
	ImageFunction.operateStretch(spline16, src, new Rect(0, 0, 3, 1), null, null, 2, 0, 255, 10);
	ImageFunction.operateStretch(spline36, src, new Rect(0, 0, 3, 1), null, null, 2, 0, 255, 12);
	ImageFunction.operateStretch(area, src, new Rect(0, 0, 3, 1), null, null, 2, 0, 255, 14);
	ImageFunction.operateStretch(gaussian, src, new Rect(0, 0, 3, 1), null, null, 2, 0, 255, 16);
	ImageFunction.operateStretch(blackmanSinc, src, new Rect(0, 0, 3, 1), null, null, 2, 0, 255, 18);
`, false); err != nil {
		t.Fatal(err)
	}
	value, err := session.Evaluate("linear")
	if err != nil {
		t.Fatal(err)
	}
	linear := services.Bitmap(value.ObjectRef().Object)
	for _, name := range []string{"cubic", "lanczos2", "lanczos3", "spline16", "spline36", "area", "gaussian", "blackmanSinc"} {
		value, err := session.Evaluate(name)
		if err != nil {
			t.Fatal(err)
		}
		filtered := services.Bitmap(value.ObjectRef().Object)
		different := false
		for index, got := range filtered.RGBA {
			if got != linear.RGBA[index] {
				different = true
				break
			}
		}
		if !different {
			t.Errorf("%s produced the same pixels as bilinear filtering", name)
		}
	}
}
