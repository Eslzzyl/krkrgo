package runtime

import (
	"bytes"
	"errors"
	"testing"
)

func TestComposeAlphaFastPathMatchesGeneralBlendOverOpaqueDestination(t *testing.T) {
	const width = 254
	root := NewRootLayer(1, width, 1)
	background := NewLayer(2)
	foreground := NewLayer(3)
	if err := background.SetBounds(Rect{Width: width, Height: 1}); err != nil {
		t.Fatal(err)
	}
	if err := foreground.SetBounds(Rect{Width: width, Height: 1}); err != nil {
		t.Fatal(err)
	}
	back := make([]byte, width*4)
	front := make([]byte, width*4)
	for x := range width {
		back[x*4], back[x*4+1], back[x*4+2], back[x*4+3] = byte(x*37), byte(x*71), byte(x*113), 255
		front[x*4], front[x*4+1], front[x*4+2], front[x*4+3] = byte(x*97), byte(x*53), byte(x*19), byte(x+1)
	}
	if err := background.SetRGBA(width, 1, back); err != nil {
		t.Fatal(err)
	}
	if err := foreground.SetRGBA(width, 1, front); err != nil {
		t.Fatal(err)
	}
	if err := root.AddChild(background); err != nil {
		t.Fatal(err)
	}
	if err := root.AddChild(foreground); err != nil {
		t.Fatal(err)
	}
	frame, err := NewCompositorWithRoot(root, width, 1).Compose()
	if err != nil {
		t.Fatal(err)
	}
	want := append([]byte(nil), back...)
	for x := range width {
		blendPixelForTarget(want[x*4:x*4+4], front[x*4:x*4+4], 255, LayerAlpha, LayerAlpha)
	}
	if !bytes.Equal(frame.RGBA, want) {
		t.Fatalf("alpha fast path differs from general blend")
	}
}
func TestDrawLayerAlphaFastPathMatchesBlendAcrossDestinationAlphaAndProvince(t *testing.T) {
	const width = 256
	layer := NewLayer(2)
	if err := layer.SetBounds(Rect{Width: width, Height: 1}); err != nil {
		t.Fatal(err)
	}
	src := make([]byte, width*4)
	province := make([]byte, width)
	initial := make([]byte, width*4)
	for alpha := range width {
		offset := alpha * 4
		src[offset], src[offset+1], src[offset+2], src[offset+3] = byte(alpha*17), byte(alpha*31), byte(alpha*59), byte(alpha)
		province[alpha] = byte(alpha)
		initial[offset], initial[offset+1], initial[offset+2], initial[offset+3] = byte(alpha*11), byte(alpha*23), byte(alpha*47), byte(alpha)
	}
	if err := layer.SetRGBA(width, 1, src); err != nil {
		t.Fatal(err)
	}
	if err := layer.SetProvince(width, 1, province); err != nil {
		t.Fatal(err)
	}
	for _, targetType := range []LayerType{LayerAlpha, LayerOpaque} {
		got := append([]byte(nil), initial...)
		gotProvince := make([]byte, width)
		for i := range gotProvince {
			gotProvince[i] = 9
		}
		draw := drawContext{width: width, height: 1, rgba: got, province: gotProvince}
		if err := draw.drawLayerTo(layer, 0, 0, targetType); err != nil {
			t.Fatal(err)
		}
		want := append([]byte(nil), initial...)
		wantProvince := make([]byte, width)
		for i := range wantProvince {
			wantProvince[i] = 9
			blendPixelForTarget(want[i*4:i*4+4], src[i*4:i*4+4], 255, LayerAlpha, targetType)
			if province[i] != 0 && src[i*4+3] != 0 {
				wantProvince[i] = province[i]
			}
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("target %d alpha blend differs from blendPixelForTarget", targetType)
		}
		if !bytes.Equal(gotProvince, wantProvince) {
			t.Fatalf("target %d province = %v, want %v", targetType, gotProvince, wantProvince)
		}
	}
}

func BenchmarkComposeAlphaOverOpaqueDestination(b *testing.B) {
	const width, height = 1280, 720
	root := NewRootLayer(1, width, height)
	background, foreground := NewLayer(2), NewLayer(3)
	for _, layer := range []*Layer{background, foreground} {
		if err := layer.SetBounds(Rect{Width: width, Height: height}); err != nil {
			b.Fatal(err)
		}
	}
	back := make([]byte, width*height*4)
	front := make([]byte, width*height*4)
	for pixel := range width * height {
		back[pixel*4], back[pixel*4+1], back[pixel*4+2], back[pixel*4+3] = 30, 90, 160, 255
		front[pixel*4], front[pixel*4+1], front[pixel*4+2], front[pixel*4+3] = 210, 120, 40, 127
	}
	if err := background.SetRGBA(width, height, back); err != nil {
		b.Fatal(err)
	}
	if err := foreground.SetRGBA(width, height, front); err != nil {
		b.Fatal(err)
	}
	if err := root.AddChild(background); err != nil {
		b.Fatal(err)
	}
	if err := root.AddChild(foreground); err != nil {
		b.Fatal(err)
	}
	compositor := NewCompositorWithRoot(root, width, height)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := compositor.Compose(); err != nil {
			b.Fatal(err)
		}
	}
}

func TestComposeOrderClipOpacityAndProvince(t *testing.T) {
	root := NewRootLayer(1, 3, 2)
	back := NewLayer(2)
	if err := back.SetBounds(Rect{X: 0, Y: 0, Width: 3, Height: 2}); err != nil {
		t.Fatal(err)
	}
	if err := back.SetRGBA(3, 2, []byte{
		255, 0, 0, 255, 255, 0, 0, 255, 255, 0, 0, 255,
		255, 0, 0, 255, 255, 0, 0, 255, 255, 0, 0, 255,
	}); err != nil {
		t.Fatal(err)
	}
	if err := back.SetProvince(3, 2, []byte{1, 1, 1, 1, 1, 1}); err != nil {
		t.Fatal(err)
	}
	front := NewLayer(3)
	if err := front.SetBounds(Rect{X: 1, Y: 0, Width: 2, Height: 1}); err != nil {
		t.Fatal(err)
	}
	if err := front.SetRGBA(2, 1, []byte{0, 0, 255, 128, 0, 0, 255, 128}); err != nil {
		t.Fatal(err)
	}
	if err := front.SetProvince(2, 1, []byte{2, 2}); err != nil {
		t.Fatal(err)
	}
	if err := root.AddChild(back); err != nil {
		t.Fatal(err)
	}
	if err := root.AddChild(front); err != nil {
		t.Fatal(err)
	}

	frame, err := NewCompositorWithRoot(root, 3, 2).Compose()
	if err != nil {
		t.Fatal(err)
	}
	if got := frame.RGBA[(0*3+0)*4 : (0*3+0)*4+4]; got[0] != 255 || got[1] != 0 || got[2] != 0 || got[3] != 255 {
		t.Fatalf("back pixel = %v", got)
	}
	if got := frame.RGBA[(0*3+1)*4 : (0*3+1)*4+4]; got[0] != 127 || got[2] != 128 || got[3] != 255 {
		t.Fatalf("blended pixel = %v", got)
	}
	if frame.Province[1] != 2 || frame.Province[0] != 1 {
		t.Fatalf("province = %v", frame.Province)
	}
}

func TestComposeOpaqueLayerCopiesRowsAndForcesAlpha(t *testing.T) {
	root := NewRootLayer(1, 2, 1)
	layer := NewLayer(2)
	layer.Type = LayerOpaque
	if err := layer.SetBounds(Rect{Width: 2, Height: 1}); err != nil {
		t.Fatal(err)
	}
	if err := layer.SetRGBA(2, 1, []byte{10, 20, 30, 0, 40, 50, 60, 128}); err != nil {
		t.Fatal(err)
	}
	if err := root.AddChild(layer); err != nil {
		t.Fatal(err)
	}
	frame, err := NewCompositorWithRoot(root, 2, 1).Compose()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := frame.RGBA, []byte{10, 20, 30, 255, 40, 50, 60, 255}; !bytes.Equal(got, want) {
		t.Fatalf("opaque compose = %v, want %v", got, want)
	}
}

func TestComposeFullyOpaqueAlphaLayerCopiesRows(t *testing.T) {
	root := NewRootLayer(1, 2, 1)
	layer := NewLayer(2)
	if err := layer.SetBounds(Rect{Width: 2, Height: 1}); err != nil {
		t.Fatal(err)
	}
	pixels := []byte{10, 20, 30, 255, 40, 50, 60, 255}
	if err := layer.SetRGBA(2, 1, pixels); err != nil {
		t.Fatal(err)
	}
	if err := root.AddChild(layer); err != nil {
		t.Fatal(err)
	}
	frame, err := NewCompositorWithRoot(root, 2, 1).Compose()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := frame.RGBA, pixels; !bytes.Equal(got, want) {
		t.Fatalf("fully opaque alpha compose = %v, want %v", got, want)
	}
}

func TestComposeAlphaEndpointFastPathPreservesProvince(t *testing.T) {
	root := NewRootLayer(1, 3, 1)
	back, layer := NewLayer(2), NewLayer(3)
	for _, current := range []*Layer{back, layer} {
		if err := current.SetBounds(Rect{Width: 3, Height: 1}); err != nil {
			t.Fatal(err)
		}
	}
	backPixels := []byte{30, 40, 50, 100, 20, 30, 40, 100, 10, 20, 30, 100}
	if err := back.SetRGBA(3, 1, backPixels); err != nil {
		t.Fatal(err)
	}
	if err := back.SetProvince(3, 1, []byte{9, 9, 9}); err != nil {
		t.Fatal(err)
	}
	pixels := []byte{90, 80, 70, 0, 10, 20, 30, 255, 40, 50, 60, 128}
	if err := layer.SetRGBA(3, 1, pixels); err != nil {
		t.Fatal(err)
	}
	if err := layer.SetProvince(3, 1, []byte{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	if err := root.AddChild(back); err != nil {
		t.Fatal(err)
	}
	if err := root.AddChild(layer); err != nil {
		t.Fatal(err)
	}
	frame, err := NewCompositorWithRoot(root, 3, 1).Compose()
	if err != nil {
		t.Fatal(err)
	}
	want := append([]byte(nil), backPixels...)
	for pixel := range 3 {
		blendPixelForTarget(want[pixel*4:pixel*4+4], pixels[pixel*4:pixel*4+4], 255, LayerAlpha, LayerAlpha)
	}
	if !bytes.Equal(frame.RGBA, want) {
		t.Fatalf("alpha endpoint compose = %v, want %v", frame.RGBA, want)
	}
	if got, want := frame.Province, []byte{9, 2, 3}; !bytes.Equal(got, want) {
		t.Fatalf("alpha endpoint province = %v, want %v", got, want)
	}
}

func TestComposeClipsSourceRowsAndColumnsToFrame(t *testing.T) {
	root := NewRootLayer(1, 2, 2)
	layer := NewLayer(2)
	if err := layer.SetBounds(Rect{X: -1, Y: -1, Width: 3, Height: 3}); err != nil {
		t.Fatal(err)
	}
	pixels := make([]byte, 3*3*4)
	province := make([]byte, 3*3)
	for i := range province {
		pixels[i*4], pixels[i*4+3] = byte(i+1), 255
		province[i] = byte(i + 1)
	}
	if err := layer.SetRGBA(3, 3, pixels); err != nil {
		t.Fatal(err)
	}
	if err := layer.SetProvince(3, 3, province); err != nil {
		t.Fatal(err)
	}
	if err := root.AddChild(layer); err != nil {
		t.Fatal(err)
	}

	frame, err := NewCompositorWithRoot(root, 2, 2).Compose()
	if err != nil {
		t.Fatal(err)
	}
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			sourceIndex := (y+1)*3 + x + 1
			destinationIndex := y*2 + x
			if got, want := frame.RGBA[destinationIndex*4], byte(sourceIndex+1); got != want {
				t.Errorf("pixel (%d,%d) red = %d, want %d", x, y, got, want)
			}
			if got, want := frame.Province[destinationIndex], byte(sourceIndex+1); got != want {
				t.Errorf("province (%d,%d) = %d, want %d", x, y, got, want)
			}
		}
	}
}
func TestComposeOpaqueTransitionRowsAndMixedAlphaFallback(t *testing.T) {
	t.Run("opaque transition pixels copy directly", func(t *testing.T) {
		root := NewRootLayer(1, 2, 1)
		layer := NewLayer(2)
		if err := layer.SetBounds(Rect{Width: 2, Height: 1}); err != nil {
			t.Fatal(err)
		}
		if err := layer.SetRGBA(2, 1, []byte{1, 2, 3, 0, 4, 5, 6, 0}); err != nil {
			t.Fatal(err)
		}
		layer.inTransition = true
		layer.transitionOpaque = true
		layer.transitionPixels = []byte{10, 20, 30, 255, 40, 50, 60, 255}
		if err := root.AddChild(layer); err != nil {
			t.Fatal(err)
		}

		frame, err := NewCompositorWithRoot(root, 2, 1).Compose()
		if err != nil {
			t.Fatal(err)
		}
		if got, want := frame.RGBA, layer.transitionPixels; !bytes.Equal(got, want) {
			t.Fatalf("opaque transition compose = %v, want %v", got, want)
		}
	})

	t.Run("mixed alpha retains generic blending", func(t *testing.T) {
		root := NewRootLayer(1, 3, 1)
		background := []byte{30, 40, 50, 255, 60, 70, 80, 255, 90, 100, 110, 255}
		if err := root.SetRGBA(3, 1, background); err != nil {
			t.Fatal(err)
		}
		layer := NewLayer(2)
		if err := layer.SetBounds(Rect{Width: 3, Height: 1}); err != nil {
			t.Fatal(err)
		}
		pixels := []byte{200, 10, 20, 255, 10, 200, 20, 128, 20, 30, 200, 0}
		if err := layer.SetRGBA(3, 1, pixels); err != nil {
			t.Fatal(err)
		}
		if err := root.AddChild(layer); err != nil {
			t.Fatal(err)
		}

		frame, err := NewCompositorWithRoot(root, 3, 1).Compose()
		if err != nil {
			t.Fatal(err)
		}
		want := append([]byte(nil), background...)
		for pixel := range 3 {
			blendPixelForTarget(want[pixel*4:pixel*4+4], pixels[pixel*4:pixel*4+4], 255, LayerAlpha, LayerAlpha)
		}
		if !bytes.Equal(frame.RGBA, want) {
			t.Fatalf("mixed-alpha compose = %v, want %v", frame.RGBA, want)
		}
	})
}

func TestComposeOpaqueFullFrameTransitionBulkCopyAndChildren(t *testing.T) {
	root := NewRootLayer(1, 2, 2)
	layer := NewLayer(2)
	if err := layer.SetBounds(Rect{Width: 2, Height: 2}); err != nil {
		t.Fatal(err)
	}
	transition := []byte{
		10, 20, 30, 255, 40, 50, 60, 255,
		70, 80, 90, 255, 100, 110, 120, 255,
	}
	if err := layer.SetRGBA(2, 2, []byte{
		1, 2, 3, 0, 4, 5, 6, 0,
		7, 8, 9, 0, 10, 11, 12, 0,
	}); err != nil {
		t.Fatal(err)
	}
	layer.inTransition = true
	layer.transitionOpaque = true
	layer.transitionPixels = transition

	child := NewLayer(3)
	if err := child.SetBounds(Rect{X: 1, Y: 0, Width: 1, Height: 1}); err != nil {
		t.Fatal(err)
	}
	childPixel := []byte{200, 150, 100, 128}
	if err := child.SetRGBA(1, 1, childPixel); err != nil {
		t.Fatal(err)
	}
	if err := layer.AddChild(child); err != nil {
		t.Fatal(err)
	}
	if err := root.AddChild(layer); err != nil {
		t.Fatal(err)
	}

	frame, err := NewCompositorWithRoot(root, 2, 2).Compose()
	if err != nil {
		t.Fatal(err)
	}
	want := append([]byte(nil), transition...)
	blendPixelForTarget(want[4:8], childPixel, 255, LayerAlpha, LayerAlpha)
	if !bytes.Equal(frame.RGBA, want) {
		t.Fatalf("full-frame transition with child = %v, want %v", frame.RGBA, want)
	}
}

func TestComposeOpaqueFullFrameTransitionFallsBackForOffsetAndClip(t *testing.T) {
	for _, test := range []struct {
		name string
		x    int32
		clip Rect
		set  bool
		want []byte
	}{
		{
			name: "offset",
			x:    1,
			want: []byte{0, 0, 0, 0, 10, 20, 30, 255, 40, 50, 60, 255},
		},
		{
			name: "clip",
			clip: Rect{X: 1, Width: 1, Height: 1},
			set:  true,
			want: []byte{0, 0, 0, 0, 40, 50, 60, 255},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			width, height := int32(2), int32(1)
			root := NewRootLayer(1, width+test.x, height)
			layer := NewLayer(2)
			if err := layer.SetBounds(Rect{X: test.x, Width: width, Height: height}); err != nil {
				t.Fatal(err)
			}
			if err := layer.SetRGBA(width, height, make([]byte, int(width*height*4))); err != nil {
				t.Fatal(err)
			}
			layer.inTransition = true
			layer.transitionOpaque = true
			layer.transitionPixels = []byte{10, 20, 30, 255, 40, 50, 60, 255}
			if test.set {
				layer.clipSet = true
				layer.clip = test.clip
			}
			if err := root.AddChild(layer); err != nil {
				t.Fatal(err)
			}
			frame, err := NewCompositorWithRoot(root, width+test.x, height).Compose()
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(frame.RGBA, test.want) {
				t.Fatalf("fallback compose = %v, want %v", frame.RGBA, test.want)
			}
		})
	}
}

func TestLayerHitTestOrderAndThreshold(t *testing.T) {
	root := NewRootLayer(1, 2, 1)
	bottom, top := NewLayer(2), NewLayer(3)
	for _, l := range []*Layer{bottom, top} {
		if err := l.SetRGBA(2, 1, []byte{10, 20, 30, 255, 10, 20, 30, 255}); err != nil {
			t.Fatal(err)
		}
		if err := root.AddChild(l); err != nil {
			t.Fatal(err)
		}
	}
	top.RGBA[3] = 0
	if id, ok := (&Compositor{Root: root, Width: 2, Height: 1}).HitTest(0, 0); !ok || id != bottom.ID {
		t.Fatalf("hit = %d, %v", id, ok)
	}
	top.RGBA[3] = 255
	if id, ok := (&Compositor{Root: root, Width: 2, Height: 1}).HitTest(0, 0); !ok || id != top.ID {
		t.Fatalf("front hit = %d, %v", id, ok)
	}
	if err := top.SetHitThreshold(256); err != nil {
		t.Fatal(err)
	}
	if top.HitThreshold != 256 {
		t.Fatalf("hit threshold = %d, want 256", top.HitThreshold)
	}
	if id, ok := (&Compositor{Root: root, Width: 2, Height: 1}).HitTest(0, 0); !ok || id != bottom.ID {
		t.Fatalf("hit at threshold 256 = %d, %v; want bottom layer %d", id, ok, bottom.ID)
	}
}

func TestLayerHitThresholdRejectsOutOfRangeValues(t *testing.T) {
	layer := NewLayer(1)
	for _, threshold := range []int{-1, 257} {
		err := layer.SetHitThreshold(threshold)
		if !errors.Is(err, ErrInvalidLayer) {
			t.Errorf("SetHitThreshold(%d) error = %v, want ErrInvalidLayer", threshold, err)
		}
	}
	if layer.HitThreshold != 1 {
		t.Fatalf("invalid threshold changed value to %d", layer.HitThreshold)
	}
}

func TestComposeRejectsAdvancedLayerType(t *testing.T) {
	root := NewRootLayer(1, 1, 1)
	child := NewLayer(2)
	child.Type = LayerEffect
	if err := root.AddChild(child); err != nil {
		t.Fatal(err)
	}
	_, err := NewCompositorWithRoot(root, 1, 1).Compose()
	if !errors.Is(err, ErrUnsupportedLayerType) {
		t.Fatalf("error = %v", err)
	}
}

func TestBlendPixelModesAndAlphaFaces(t *testing.T) {
	type modeCase struct {
		name string
		mode LayerType
		want [4]byte
	}
	cases := []modeCase{
		{"alpha", LayerAlpha, [4]byte{58, 154, 127, 227}},
		{"opaque", LayerOpaque, [4]byte{40, 180, 100, 255}},
		{"add", LayerAdditive, [4]byte{120, 255, 255, 200}},
		{"sub", LayerSubtractive, [4]byte{40, 0, 60, 200}},
		{"mul", LayerMultiplicative, [4]byte{12, 84, 62, 200}},
		{"dodge", LayerDodge, [4]byte{94, 255, 255, 200}},
		{"darken", LayerDarken, [4]byte{40, 120, 100, 200}},
		{"lighten", LayerLighten, [4]byte{80, 180, 160, 200}},
		{"screen", LayerScreen, [4]byte{108, 216, 198, 200}},
		{"addalpha", LayerAddAlpha, [4]byte{51, 137, 113, 255}},
		{"psnormal", LayerPSNormal, [4]byte{60, 150, 130, 200}},
		{"psadd", LayerPSAdditive, [4]byte{100, 188, 208, 200}},
		{"pssub", LayerPSSubtractive, [4]byte{60, 60, 110, 200}},
		{"psmul", LayerPSMultiplicative, [4]byte{46, 102, 111, 200}},
		{"psscreen", LayerPSScreen, [4]byte{94, 168, 179, 200}},
		{"psoverlay", LayerPSOverlay, [4]byte{52, 145, 150, 200}},
		{"pshardlight", LayerPSHardLight, [4]byte{52, 148, 142, 200}},
		{"pssoftlight", LayerPSSoftLight, [4]byte{75, 137, 156, 200}},
		{"psdodge", LayerPSColorDodge, [4]byte{87, 188, 208, 200}},
		{"psdodge5", LayerPSColorDodge5, [4]byte{87, 188, 208, 200}},
		{"psburn", LayerPSColorBurn, [4]byte{40, 92, 86, 200}},
		{"pslighten", LayerPSLighten, [4]byte{80, 150, 160, 200}},
		{"psdarken", LayerPSDarken, [4]byte{60, 120, 130, 200}},
		{"psdiff", LayerPSDifference, [4]byte{60, 90, 110, 200}},
		{"psdiff5", LayerPSDifference5, [4]byte{60, 90, 110, 200}},
		{"psexcl", LayerPSExclusion, [4]byte{88, 126, 147, 200}},
	}
	for _, tc := range cases {
		dst := []byte{80, 120, 160, 200}
		src := []byte{40, 180, 100, 128}
		target := LayerAlpha
		if tc.mode == LayerAddAlpha {
			target = LayerAddAlpha
		}
		blendPixelForTarget(dst, src, 255, tc.mode, target)
		for i := range dst {
			if dst[i] != tc.want[i] {
				t.Fatalf("%s pixel=%v want=%v", tc.name, dst, tc.want)
			}
		}
	}
}

func TestAddAlphaOnAlphaIsUnsupportedNoOp(t *testing.T) {
	dst := []byte{80, 120, 160, 200}
	src := []byte{40, 180, 100, 128}
	blendPixelForTarget(dst, src, 255, LayerAddAlpha, LayerAlpha)
	if want := [4]byte{80, 120, 160, 200}; [4]byte{dst[0], dst[1], dst[2], dst[3]} != want {
		t.Fatalf("addalpha on alpha pixel=%v want=%v", dst, want)
	}
}

func TestAlphaOnOpaqueUsesSourceAlpha(t *testing.T) {
	dst := []byte{80, 120, 160, 200}
	src := []byte{40, 180, 100, 128}
	blendPixelForTarget(dst, src, 255, LayerAlpha, LayerOpaque)
	if want := [4]byte{60, 150, 130, 164}; [4]byte{dst[0], dst[1], dst[2], dst[3]} != want {
		t.Fatalf("alpha on opaque pixel=%v want=%v", dst, want)
	}
}

func TestBlendPixelOpacityAndTargetAlpha(t *testing.T) {
	dst := []byte{80, 120, 160, 200}
	src := []byte{40, 180, 100, 128}
	blendPixelForTarget(dst, src, 128, LayerAdditive, LayerAlpha)
	if want := [4]byte{100, 188, 208, 200}; [4]byte{dst[0], dst[1], dst[2], dst[3]} != want {
		t.Fatalf("ordinary opacity pixel=%v want=%v", dst, want)
	}
	dst = []byte{80, 120, 160, 200}
	blendPixelForTarget(dst, src, 128, LayerPSNormal, LayerAlpha)
	if want := [4]byte{70, 135, 145, 200}; [4]byte{dst[0], dst[1], dst[2], dst[3]} != want {
		t.Fatalf("ps opacity pixel=%v want=%v", dst, want)
	}
	dst = []byte{80, 120, 160, 200}
	src[3] = 0
	blendPixelForTarget(dst, src, 128, LayerOpaque, LayerOpaque)
	if want := [4]byte{60, 150, 130, 227}; [4]byte{dst[0], dst[1], dst[2], dst[3]} != want {
		t.Fatalf("opaque pixel=%v want=%v", dst, want)
	}
	dst = []byte{80, 120, 160, 100}
	src = []byte{100, 200, 50, 100}
	blendPixelForTarget(dst, src, 255, LayerAddAlpha, LayerAddAlpha)
	if want := [4]byte{74, 136, 74, 200}; [4]byte{dst[0], dst[1], dst[2], dst[3]} != want {
		t.Fatalf("addalpha pixel=%v want=%v", dst, want)
	}
}
func TestBlendAlphaOnTransparentDestinationIsByteExact(t *testing.T) {
	for sourceAlpha := range 256 {
		for opacity := range 256 {
			src := []byte{31, 127, 244, byte(sourceAlpha)}
			sa := sourceAlpha * opacity / 255
			for _, mode := range []LayerType{LayerAlpha, LayerAddAlpha} {
				dst := []byte{0, 0, 0, 0}
				want := []byte{0, 0, 0, 0}
				if sa != 0 {
					for channel := range 3 {
						p := int(src[channel]) * sa
						want[channel] = byte((p*255 + sa*127) / (sa * 255))
					}
					want[3] = byte(sa)
				}
				blendPixelForTarget(dst, src, byte(opacity), mode, mode)
				for channel := range 4 {
					if dst[channel] != want[channel] {
						t.Fatalf("mode=%d source alpha=%d opacity=%d pixel=%v want=%v", mode, sourceAlpha, opacity, dst, want)
					}
				}
			}
		}
	}
}
func TestBlendOpaqueSourceEndpointsAreByteExact(t *testing.T) {
	for destinationAlpha := range 256 {
		for sourceAlphaIndex := range 2 {
			sourceAlpha := byte(sourceAlphaIndex * 255)
			src := []byte{31, 127, 244, sourceAlpha}
			dst := []byte{80, 120, 160, byte(destinationAlpha)}
			want := [4]byte{80, 120, 160, byte(destinationAlpha)}
			if sourceAlpha != 0 {
				want = [4]byte{src[0], src[1], src[2], 255}
			}
			blendPixelForTarget(dst, src, 255, LayerAlpha, LayerAlpha)
			if got := [4]byte{dst[0], dst[1], dst[2], dst[3]}; got != want {
				t.Fatalf("alpha source alpha=%d destination alpha=%d pixel=%v want=%v", sourceAlpha, destinationAlpha, got, want)
			}

			dst = []byte{80, 120, 160, byte(destinationAlpha)}
			want = [4]byte{src[0], src[1], src[2], 255}
			blendPixelForTarget(dst, src, 255, LayerOpaque, LayerAlpha)
			if got := [4]byte{dst[0], dst[1], dst[2], dst[3]}; got != want {
				t.Fatalf("opaque destination alpha=%d pixel=%v want=%v", destinationAlpha, got, want)
			}

			dst = []byte{80, 120, 160, byte(destinationAlpha)}
			want = [4]byte{80, 120, 160, byte(destinationAlpha)}
			if sourceAlpha != 0 {
				want = [4]byte{src[0], src[1], src[2], 255}
			}
			blendPixelForTarget(dst, src, 255, LayerAddAlpha, LayerAddAlpha)
			if got := [4]byte{dst[0], dst[1], dst[2], dst[3]}; got != want {
				t.Fatalf("addalpha source alpha=%d destination alpha=%d pixel=%v want=%v", sourceAlpha, destinationAlpha, got, want)
			}
		}
	}
}

func BenchmarkComposeFullFrame(b *testing.B) {
	const width, height = int32(1280), int32(720)
	root := NewRootLayer(1, width, height)
	layer := NewLayer(2)
	if err := layer.SetBounds(Rect{Width: width, Height: height}); err != nil {
		b.Fatal(err)
	}
	rgba := make([]byte, int(width*height)*4)
	for i := 0; i < len(rgba); i += 4 {
		rgba[i], rgba[i+1], rgba[i+2], rgba[i+3] = 80, 120, 200, 180
	}
	if err := layer.SetRGBA(width, height, rgba); err != nil {
		b.Fatal(err)
	}
	province := make([]byte, width*height)
	for i := range province {
		province[i] = byte(i%255 + 1)
	}
	if err := layer.SetProvince(width, height, province); err != nil {
		b.Fatal(err)
	}
	if err := root.AddChild(layer); err != nil {
		b.Fatal(err)
	}
	compositor := NewCompositorWithRoot(root, width, height)
	b.SetBytes(int64(width * height * 4))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := compositor.Compose(); err != nil {
			b.Fatal(err)
		}
	}
}
func BenchmarkClearRGBA720p(b *testing.B) {
	const width, height = int32(1280), int32(720)
	color := [4]uint8{17, 83, 149, 211}
	b.SetBytes(int64(width * height * 4))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = clearRGBA(width, height, color)
	}
}

func TestClearRGBAIsByteExact(t *testing.T) {
	const width, height = int32(3), int32(2)
	color := [4]uint8{17, 83, 149, 211}
	got := clearRGBA(width, height, color)
	for i := 0; i < len(got); i += 4 {
		if got[i] != color[0] || got[i+1] != color[1] || got[i+2] != color[2] || got[i+3] != color[3] {
			t.Fatalf("pixel %d = %v, want %v", i/4, got[i:i+4], color)
		}
	}
}

func TestClearRGBAZeroDimensions(t *testing.T) {
	color := [4]uint8{17, 83, 149, 211}
	for _, dimensions := range [][2]int32{{0, 0}, {0, 2}, {3, 0}} {
		if got := clearRGBA(dimensions[0], dimensions[1], color); len(got) != 0 {
			t.Errorf("clearRGBA(%d, %d) returned %d bytes, want none", dimensions[0], dimensions[1], len(got))
		}
	}
}
