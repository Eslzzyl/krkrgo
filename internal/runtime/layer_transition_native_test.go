package runtime

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/eslzzyl/krkrgo/internal/assets"
	"github.com/eslzzyl/krkrgo/internal/core"
)

func TestLayerTransitionLifecycleAndCompletionCallback(t *testing.T) {
	session := NewSession(DefaultServices(), 10_000)
	if _, err := session.executeSource("<transition-lifecycle-setup>", `
		rootLayer = new Layer();
		rootLayer.setSize(32, 32);
		sourceLayer = new Layer(null, rootLayer);
		destinationLayer = new Layer(null, rootLayer);
		sourceLayer.fillRect(0, 0, 32, 32, 0xff000000);
		destinationLayer.fillRect(0, 0, 32, 32, 0xffffffff);
		sourceLayer.visible = true;
		destinationLayer.visible = true;
		clock = 0;
		transitionTicks = -1;
		transitionDestination = null;
		transitionSource = null;
		destinationLayer.onTransitionCompleted = function(dest, src) {
			transitionDestination = dest;
			transitionSource = src;
		};
		destinationLayer.beginTransition("crossfade", true, sourceLayer, %[
			time: 10,
			callback: function() { transitionTicks = clock; return clock; }
		]);
	`, false); err != nil {
		t.Fatal(err)
	}
	destinationValue, err := session.Evaluate("destinationLayer")
	if err != nil {
		t.Fatal(err)
	}
	destination := session.Services.Layer(destinationValue.ObjectRef().Object)
	if destination == nil || !destination.transitionOpaque {
		t.Fatal("opaque crossfade did not cache opaque endpoint mode")
	}
	value, err := session.Evaluate(`transitionTicks == -1`)
	if err != nil {
		t.Fatal(err)
	}
	truth, err := value.Truth()
	if err != nil {
		t.Fatal(err)
	}
	if !truth {
		t.Fatal("transition callback was invoked during beginTransition")
	}
	if _, err := session.Evaluate(`clock = 5`); err != nil {
		t.Fatal(err)
	}
	if err := session.AdvanceClock(5); err != nil {
		t.Fatal(err)
	}
	frame, err := session.Frame()
	if err != nil {
		t.Fatal(err)
	}
	if pixel := frame.RGBA[:4]; pixel[0] != 128 || pixel[1] != 128 || pixel[2] != 128 || pixel[3] != 255 {
		t.Fatalf("halfway crossfade pixel = %v; want opaque midpoint gray", pixel)
	}
	stopResult, err := session.Evaluate(`destinationLayer.stopTransition()`)
	if err != nil {
		t.Fatal(err)
	}
	if !stopResult.IsVoid() {
		t.Fatalf("stopTransition() result = %v, want void", stopResult)
	}
	if destination.transitionOpaque {
		t.Fatal("stopping transition did not clear cached opaque endpoint mode")
	}
	value, err = session.Evaluate(`transitionTicks == 5 && transitionDestination === destinationLayer && transitionSource === sourceLayer && destinationLayer.getMainPixel(0,0) == 0`)
	if err != nil {
		t.Fatal(err)
	}
	truth, err = value.Truth()
	if err != nil {
		t.Fatal(err)
	}
	if !truth {
		t.Fatalf("transition completion state = %v, want callback tick, exact objects, and exchanged pixels", value)
	}
}

func TestLayerUniversalTransitionUsesGrayscaleRule(t *testing.T) {
	project := t.TempDir()
	rule := image.NewGray(image.Rect(0, 0, 16, 16))
	for y := range 16 {
		for x := range 16 {
			if x >= 8 {
				rule.SetGray(x, y, color.Gray{Y: 255})
			}
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, rule); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "rule.png"), encoded.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	storage, err := assets.NewStorage(project, core.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	session := NewSession(NewServices(storage, "", core.DefaultLimits()), 100_000)
	if _, err := session.executeSource("<universal-transition-setup>", `
		rootLayer = new Layer();
		rootLayer.setSize(16, 16);
		sourceLayer = new Layer(null, rootLayer);
		destinationLayer = new Layer(null, rootLayer);
		sourceLayer.setImageSize(16, 16);
		destinationLayer.setImageSize(16, 16);
		sourceLayer.fillRect(0, 0, 16, 16, 0xff000000);
		destinationLayer.fillRect(0, 0, 16, 16, 0xffffffff);
		sourceLayer.visible = true;
		destinationLayer.visible = true;
		destinationLayer.beginTransition("universal", true, sourceLayer, %[
			time: 10, vague: 64, rule: "rule.png"
		]);
	`, false); err != nil {
		t.Fatal(err)
	}
	if err := session.AdvanceClock(5); err != nil {
		t.Fatal(err)
	}
	destination, _, err := session.Services.layerFromValue(session.VM.Globals["destinationLayer"])
	if err != nil {
		t.Fatal(err)
	}
	if len(destination.transitionRule) != int(destination.ImageWidth*destination.ImageHeight) {
		t.Fatalf("transition rule length=%d, want image size %dx%d", len(destination.transitionRule), destination.ImageWidth, destination.ImageHeight)
	}
	if got := destination.transitionPixels[:4]; got[0] != 0 || got[1] != 0 || got[2] != 0 || got[3] != 255 {
		t.Fatalf("low-rule transition pixel = %v, want transitioned black", got)
	}
	highPixel := int(destination.ImageWidth / 2)
	if destination.transitionRule[highPixel] != 255 {
		t.Fatalf("resized high-rule sample=%d, want 255", destination.transitionRule[highPixel])
	}
	offset := highPixel * 4
	if got := destination.transitionPixels[offset : offset+4]; got[0] != 255 || got[1] != 255 || got[2] != 255 || got[3] != 255 {
		t.Fatalf("high-rule transition pixel = %v, want unchanged white", got)
	}
	if err := session.AdvanceClock(10); err != nil {
		t.Fatal(err)
	}
	value, err := session.Evaluate(`destinationLayer.getMainPixel(0, 0) == 0`)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := value.Truth()
	if err != nil || !ok {
		t.Fatalf("destination after universal transition=%v, err=%v; want source image", value, err)
	}
}

func TestLayerTransitionAutomaticCompletionAndIdleStop(t *testing.T) {
	session := NewSession(DefaultServices(), 10_000)
	if _, err := session.executeSource("<transition-auto-completion-setup>", `
		rootLayer = new Layer();
		rootLayer.setSize(1, 1);
		sourceLayer = new Layer(null, rootLayer);
		destinationLayer = new Layer(null, rootLayer);
		sourceLayer.fillRect(0, 0, 1, 1, 0xff000000);
		destinationLayer.fillRect(0, 0, 1, 1, 0xffffffff);
		sourceLayer.visible = true;
		destinationLayer.visible = true;
		completedDestination = null;
		completedSource = null;
		destinationLayer.onTransitionCompleted = function(dest, src) {
			completedDestination = dest;
			completedSource = src;
		};
		destinationLayer.beginTransition("crossfade", true, sourceLayer, %[time:10]);
	`, false); err != nil {
		t.Fatal(err)
	}
	if err := session.AdvanceClock(10); err != nil {
		t.Fatal(err)
	}
	value, err := session.Evaluate(`
		completedDestination === destinationLayer &&
		completedSource === sourceLayer &&
		destinationLayer.getMainPixel(0,0) == 0
	`)
	if err != nil {
		t.Fatal(err)
	}
	truth, err := value.Truth()
	if err != nil {
		t.Fatal(err)
	}
	if !truth {
		details, _ := session.Evaluate(`[completedDestination === destinationLayer, completedSource === sourceLayer, destinationLayer.getMainPixel(0,0)].join("|")`)
		t.Fatalf("automatic transition completion = %v (%v), want exact callback objects and exchanged images", value, details)
	}
	if _, err := session.Evaluate(`destinationLayer.stopTransition()`); err != nil {
		t.Fatalf("idle stopTransition: %v", err)
	}
	if _, err := session.Evaluate(`destinationLayer.stopTransition(true)`); err != nil {
		t.Fatalf("stopTransition(true): %v", err)
	}
}

func TestLayerTransitionWithChildrenExchangesSubtrees(t *testing.T) {
	session := NewSession(DefaultServices(), 10_000)
	if _, err := session.executeSource("<transition-subtree-setup>", `
		rootLayer = new Layer();
		rootLayer.setSize(1, 1);
		destinationLayer = new Layer(null, rootLayer);
		sourceLayer = new Layer(null, rootLayer);
		destinationLayer.fillRect(0, 0, 1, 1, 0xffffffff);
		sourceLayer.fillRect(0, 0, 1, 1, 0xff000000);
		destinationChild = new Layer(null, destinationLayer);
		sourceChild = new Layer(null, sourceLayer);
		destinationLayer.beginTransition("crossfade", true, sourceLayer, %[time:10]);
		destinationLayer.stopTransition();
	`, false); err != nil {
		t.Fatal(err)
	}
	value, err := session.Evaluate(`
		rootLayer.children[0] === sourceLayer &&
		rootLayer.children[1] === destinationLayer &&
		destinationLayer.children[0] === destinationChild &&
		sourceLayer.children[0] === sourceChild &&
		destinationChild.parent === destinationLayer &&
		sourceChild.parent === sourceLayer
	`)
	if err != nil {
		t.Fatal(err)
	}
	truth, err := value.Truth()
	if err != nil {
		t.Fatal(err)
	}
	if !truth {
		t.Fatalf("withchildren subtree exchange = %v", value)
	}
}

func TestLayerTransitionSelfUpdateAdvancesDuringFrames(t *testing.T) {
	session := NewSession(DefaultServices(), 10_000)
	if _, err := session.executeSource("<transition-selfupdate-setup>", `
		rootLayer = new Layer();
		rootLayer.setSize(1, 1);
		destinationLayer = new Layer(null, rootLayer);
		sourceLayer = new Layer(null, rootLayer);
		destinationLayer.fillRect(0, 0, 1, 1, 0xffffffff);
		sourceLayer.fillRect(0, 0, 1, 1, 0xff000000);
		destinationLayer.visible = true;
		sourceLayer.visible = true;
		clock = 0;
		updates = 0;
		completions = 0;
		destinationLayer.onTransitionCompleted = function() { completions++; };
		destinationLayer.beginTransition("crossfade", true, sourceLayer, %[
			time: 10,
			selfupdate: true,
			callback: function() { updates++; return clock; }
		]);
	`, false); err != nil {
		t.Fatal(err)
	}
	if err := session.AdvanceClock(5); err != nil {
		t.Fatal(err)
	}
	value, err := session.Evaluate(`updates == 0 && completions == 0`)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := value.Truth(); !ok {
		t.Fatalf("selfupdate transition advanced during clock update: %v", value)
	}
	if _, err := session.Evaluate(`clock = 5`); err != nil {
		t.Fatal(err)
	}
	frame, err := session.Frame()
	if err != nil {
		t.Fatal(err)
	}
	if pixel := frame.RGBA[:4]; pixel[0] != 128 || pixel[1] != 128 || pixel[2] != 128 || pixel[3] != 255 {
		t.Fatalf("selfupdate midpoint pixel = %v, want opaque gray", pixel)
	}
	if err := session.AdvanceClock(10); err != nil {
		t.Fatal(err)
	}
	value, err = session.Evaluate(`updates == 1 && completions == 0`)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := value.Truth(); !ok {
		t.Fatalf("selfupdate transition advanced outside frame: %v", value)
	}
	if _, err := session.Evaluate(`clock = 10`); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Frame(); err != nil {
		t.Fatal(err)
	}
	value, err = session.Evaluate(`updates == 2 && completions == 1 && destinationLayer.getMainPixel(0,0) == 0`)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := value.Truth(); !ok {
		t.Fatalf("selfupdate completion state = %v", value)
	}
}
func TestTickForPresentationCapturesTerminalTransitionBeforeCompletion(t *testing.T) {
	session := NewSession(DefaultServices(), 10_000)
	defer session.Close()
	if _, err := session.executeSource("<presentation-transition-setup>", `
		rootLayer = new Layer();
		rootLayer.setSize(1, 1);
		destinationLayer = new Layer(null, rootLayer);
		sourceLayer = new Layer(null, rootLayer);
		destinationLayer.fillRect(0, 0, 1, 1, 0xffffffff);
		sourceLayer.fillRect(0, 0, 1, 1, 0xff000000);
		destinationLayer.visible = true;
		sourceLayer.visible = true;
		completions = 0;
		callbackRan = false;
		destinationLayer.onTransitionCompleted = function() {
			completions++;
			callbackRan = true;
			rootLayer.fillRect(0, 0, 1, 1, 0xffff0000);
		};
		destinationLayer.beginTransition("crossfade", true, sourceLayer, %[time: 10]);
	`, false); err != nil {
		t.Fatal(err)
	}

	frame, err := session.TickForPresentation(10)
	if err != nil {
		t.Fatal(err)
	}
	if got := frame.RGBA[:4]; got[0] != 0 || got[1] != 0 || got[2] != 0 || got[3] != 255 {
		t.Fatalf("terminal transition frame = %v, want opaque black before callback mutation", got)
	}
	value, err := session.Evaluate(`completions == 1 && callbackRan`)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := value.Truth(); !ok {
		t.Fatalf("completion callback state = %v, want one completion and callback mutation", value)
	}
	if frame.RGBA[0] != 0 {
		t.Fatalf("borrowed presentation frame changed after completion callback: %v", frame.RGBA[:4])
	}
	if _, err := session.TickForPresentation(10); err != nil {
		t.Fatal(err)
	}
	value, err = session.Evaluate(`completions == 1`)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := value.Truth(); !ok {
		t.Fatalf("completion callback ran more than once: %v", value)
	}
}

func TestLayerTransitionRejectsInvalidSourceAndBusyDestination(t *testing.T) {
	session := NewSession(DefaultServices(), 10_000)
	if _, err := session.executeSource("<transition-invalid-source-setup>", `root = new Layer(); transitionLayer = new Layer(null, root); transitionSourceLayer = new Layer(null, root);`, false); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Evaluate(`transitionLayer.beginTransition("crossfade", true, new Object(), %[time:10])`); err == nil {
		t.Fatal("beginTransition accepted an object without a native Layer")
	}
	if _, err := session.executeSource("<transition-busy-destination>", `transitionLayer.beginTransition("crossfade", true, transitionSourceLayer, %[time:10]); transitionLayer.beginTransition("crossfade", true, transitionSourceLayer, %[time:10])`, false); err == nil {
		t.Fatal("beginTransition accepted an already transitioning destination")
	}
}
func TestBlendCrossfadeMatchesGenericPixelFormula(t *testing.T) {
	dest := []byte{12, 80, 190, 255, 255, 4, 89, 255, 30, 50, 70, 120, 255, 0, 7, 200}
	src := []byte{250, 20, 1, 255, 0, 210, 30, 255, 90, 3, 240, 200, 1, 255, 128, 10}
	for _, phase := range []uint64{0, 1, 64, 127, 254, 255} {
		got := make([]byte, len(dest))
		blendCrossfade(got, dest, src, phase, false)
		for i := range got {
			d, s, p := int(dest[i]), int(src[i]), int(phase)
			want := byte((d*(255-p) + s*p + 127) / 255)
			if got[i] != want {
				t.Fatalf("phase %d byte %d = %d, want generic blend %d", phase, i, got[i], want)
			}
		}
	}
}

func TestBlendCrossfadeTransparentExhaustiveBytePairs(t *testing.T) {
	const pairs = 256 * 256
	dest := make([]byte, pairs*4)
	src := make([]byte, pairs*4)
	out := make([]byte, pairs*4)
	for d := range 256 {
		for s := range 256 {
			i := (d*256 + s) * 4
			for c := range 4 {
				dest[i+c], src[i+c] = byte(d), byte(s)
			}
		}
	}
	for phase := range 256 {
		blendCrossfade(out, dest, src, uint64(phase), false)
		for pair := range pairs {
			i := pair * 4
			got := out[i]
			want := byte((int(dest[i])*(255-phase) + int(src[i])*phase + 127) / 255)
			if phase == 0 {
				want = dest[i]
			} else if phase == 255 {
				want = src[i]
			}
			if got != want {
				t.Fatalf("phase %d destination %d source %d = %d, want %d", phase, dest[i], src[i], got, want)
			}
		}
	}
	for _, phase := range []uint64{256, ^uint64(0)} {
		blendCrossfade(out, dest, src, phase, false)
		for i, got := range out {
			if got != src[i] {
				t.Fatalf("phase %d byte %d = %d, want source %d", phase, i, got, src[i])
			}
		}
	}

}

func TestBlendCrossfadeOpaquePixelsMatchGenericFormula(t *testing.T) {
	dest := []byte{12, 80, 190, 255, 255, 4, 89, 255}
	src := []byte{250, 20, 1, 255, 0, 210, 30, 255}
	for phase := range 257 {
		got := make([]byte, len(dest))
		blendCrossfade(got, dest, src, uint64(phase), true)
		for i := range got {
			d, s := int(dest[i]), int(src[i])
			want := byte((d*(255-phase) + s*phase + 127) / 255)
			if phase >= 255 {
				want = src[i]
			}
			if got[i] != want {
				t.Fatalf("phase %d byte %d = %d, want generic blend %d", phase, i, got[i], want)
			}
		}
	}
}

func TestClassifyRGBAAlphaPlanes(t *testing.T) {
	tests := []struct {
		name   string
		pixels []byte
		want   transitionAlphaClass
	}{
		{name: "all transparent", pixels: []byte{1, 2, 3, 0, 4, 5, 6, 0}, want: transitionAlphaZero},
		{name: "all opaque", pixels: []byte{1, 2, 3, 255, 4, 5, 6, 255}, want: transitionAlphaOpaque},
		{name: "mixed opaque and transparent", pixels: []byte{1, 2, 3, 0, 4, 5, 6, 255}, want: transitionAlphaMixed},
		{name: "intermediate alpha", pixels: []byte{1, 2, 3, 127, 4, 5, 6, 127}, want: transitionAlphaMixed},
		{name: "empty", want: transitionAlphaUnknown},
		{name: "partial pixel", pixels: []byte{1, 2, 3}, want: transitionAlphaUnknown},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := classifyRGBAAlpha(test.pixels); got != test.want {
				t.Fatalf("classifyRGBAAlpha() = %s, want %s", got, test.want)
			}
		})
	}
}
func TestBlendZeroOpaqueCrossfadeByteExactAllPhases(t *testing.T) {
	const pairs = 256 * 256
	dest := make([]byte, pairs*4)
	src := make([]byte, pairs*4)
	out := make([]byte, pairs*4)
	for d := range 256 {
		for s := range 256 {
			i := (d*256 + s) * 4
			dest[i], dest[i+1], dest[i+2], dest[i+3] = byte(d), byte(s), byte(d^s), 255
			src[i], src[i+1], src[i+2], src[i+3] = byte(s), byte(d), byte(d+s), 0
		}
	}
	for phase := range 256 {
		blendZeroOpaqueCrossfade(out, dest, src, uint64(phase))
		for pair := range pairs {
			i := pair * 4
			for channel := range 4 {
				var want byte
				switch {
				case phase == 0:
					want = dest[i+channel]
				case phase == 255:
					want = src[i+channel]
				case channel == 3:
					want = byte(255 - phase)
				default:
					want = byte((int(dest[i+channel])*(255-phase) + 127) / 255)
				}
				if got := out[i+channel]; got != want {
					t.Fatalf("phase %d pair %d channel %d = %d, want %d", phase, pair, channel, got, want)
				}
			}
		}
	}
}

func TestTransitionAlphaClassificationSelectsOnlyZeroOpaqueMode(t *testing.T) {
	opaque, zeroOpaque, destClass, srcClass := classifyTransitionAlphaPlanes(
		[]byte{1, 2, 3, 255, 4, 5, 6, 255},
		[]byte{7, 8, 9, 0, 10, 11, 12, 0},
	)
	if opaque || !zeroOpaque || destClass != transitionAlphaOpaque || srcClass != transitionAlphaZero {
		t.Fatalf("zero→opaque classification = (%t, %t, %s, %s)", opaque, zeroOpaque, destClass, srcClass)
	}
	_, mixedZeroOpaque, _, _ := classifyTransitionAlphaPlanes(
		[]byte{1, 2, 3, 255, 4, 5, 6, 254},
		[]byte{7, 8, 9, 0, 10, 11, 12, 0},
	)
	if mixedZeroOpaque {
		t.Fatal("mixed destination alpha selected zero→opaque mode")
	}
}

func BenchmarkBlendCrossfadeZeroOpaque1280x720(b *testing.B) {
	const width, height = 1280, 720
	pixels := width * height
	dest := make([]byte, pixels*4)
	src := make([]byte, pixels*4)
	out := make([]byte, pixels*4)
	for i := range pixels {
		offset := i * 4
		dest[offset] = byte(i)
		dest[offset+1] = byte(i >> 3)
		dest[offset+2] = byte(i >> 7)
		dest[offset+3] = 255
		src[offset] = byte(i >> 1)
		src[offset+1] = byte(i >> 5)
		src[offset+2] = byte(i >> 9)
		src[offset+3] = 0
	}
	b.SetBytes(int64(len(out)))
	b.ReportAllocs()
	for _, phase := range []uint64{1, 64, 127, 254} {
		b.Run(fmt.Sprintf("generic_phase_%d", phase), func(b *testing.B) {
			for range b.N {
				blendCrossfade(out, dest, src, phase, false)
			}
		})
		b.Run(fmt.Sprintf("specialized_phase_%d", phase), func(b *testing.B) {
			for range b.N {
				blendZeroOpaqueCrossfade(out, dest, src, phase)
			}
		})
	}
}

func BenchmarkBlendCrossfadeTransparent1280x720(b *testing.B) {
	const width, height = 1280, 720
	pixels := width * height
	dest := make([]byte, pixels*4)
	src := make([]byte, pixels*4)
	out := make([]byte, pixels*4)
	for i := range pixels {
		offset := i * 4
		dest[offset] = byte(i)
		dest[offset+1] = byte(i >> 3)
		dest[offset+2] = byte(i >> 7)
		dest[offset+3] = byte(i >> 11)
		src[offset] = byte(i >> 1)
		src[offset+1] = byte(i >> 5)
		src[offset+2] = byte(i >> 9)
		src[offset+3] = byte(i >> 13)
	}
	b.SetBytes(int64(len(out)))
	b.ReportAllocs()
	for _, phase := range []uint64{1, 64, 127, 254} {
		b.Run(fmt.Sprintf("phase_%d", phase), func(b *testing.B) {
			for range b.N {
				blendCrossfade(out, dest, src, phase, false)
			}
		})
	}
}
func BenchmarkBlendCrossfadeOpaque1280x720(b *testing.B) {
	const width, height = 1280, 720
	pixels := width * height
	dest := make([]byte, pixels*4)
	src := make([]byte, pixels*4)
	out := make([]byte, pixels*4)
	for i := range pixels {
		offset := i * 4
		dest[offset] = byte(i)
		dest[offset+1] = byte(i >> 3)
		dest[offset+2] = byte(i >> 7)
		dest[offset+3] = 255
		src[offset] = byte(i >> 1)
		src[offset+1] = byte(i >> 5)
		src[offset+2] = byte(i >> 9)
		src[offset+3] = 255
	}
	b.SetBytes(int64(len(out)))
	b.ReportAllocs()
	for _, phase := range []uint64{1, 64, 127, 254} {
		b.Run(fmt.Sprintf("phase_%d", phase), func(b *testing.B) {
			for range b.N {
				blendCrossfade(out, dest, src, phase, true)
			}
		})
	}
}
