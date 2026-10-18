package host

import (
	"testing"

	runtime "github.com/eslzzyl/krkrgo/internal/runtime"
	"github.com/hajimehoshi/ebiten/v2"
)

func TestGPUShaderProgramsCompile(t *testing.T) {
	methods, crossfade, universal, err := compileGPUShaders()
	if err != nil {
		t.Fatal(err)
	}
	if len(methods) == 0 || crossfade == nil || universal == nil {
		t.Fatalf("compiled shaders = methods:%d crossfade:%v universal:%v", len(methods), crossfade != nil, universal != nil)
	}
	for _, shader := range methods {
		if shader == nil {
			t.Fatal("blend shader registry contains nil shader")
		}
	}
	crossfade.Deallocate()
	universal.Deallocate()
	seen := make(map[*ebiten.Shader]struct{})
	for _, shader := range methods {
		if _, ok := seen[shader]; ok {
			continue
		}
		seen[shader] = struct{}{}
		shader.Deallocate()
	}
}

func TestGPUCompositorComposeCreatesNativeTarget(t *testing.T) {
	compositor, err := NewGPUCompositor()
	if err != nil {
		t.Fatal(err)
	}
	defer compositor.Deallocate()
	plan := runtime.RenderPlan{
		Width:  1,
		Height: 1,
		Nodes: []runtime.RenderNode{{
			ID:          1,
			Bounds:      runtime.Rect{Width: 1, Height: 1},
			Type:        runtime.LayerOpaque,
			Visible:     true,
			Opacity:     255,
			ImageWidth:  1,
			ImageHeight: 1,
			RGBA:        []byte{32, 64, 128, 255},
			ImageHash:   1,
		}},
	}
	image, err := compositor.Compose(plan)
	if err != nil {
		t.Fatal(err)
	}
	if got := image.Bounds().Size(); got.X != 1 || got.Y != 1 {
		t.Fatalf("composited target size = %v", got)
	}
}

func TestGPUCompositorComposesDeferredMotionTarget(t *testing.T) {
	compositor, err := NewGPUCompositor()
	if err != nil {
		t.Fatal(err)
	}
	defer compositor.Deallocate()
	plan := runtime.RenderPlan{
		Width:  1,
		Height: 1,
		Nodes: []runtime.RenderNode{{
			ID:          1,
			Bounds:      runtime.Rect{Width: 1, Height: 1},
			Type:        runtime.LayerAlpha,
			Visible:     true,
			Opacity:     255,
			ImageWidth:  1,
			ImageHeight: 1,
			Motion: &runtime.RenderMotion{
				Filter: 1,
				Draws: []runtime.RenderMotionDraw{{
					SourceKey: "test/motion",
					RGBA:      []byte{255, 0, 0, 255},
					Width:     1,
					Height:    1,
					A:         1,
					D:         1,
					Opacity:   255,
				}},
			},
		}},
	}
	image, err := compositor.Compose(plan)
	if err != nil {
		t.Fatal(err)
	}
	if got := image.Bounds().Size(); got.X != 1 || got.Y != 1 {
		t.Fatalf("deferred motion target size = %v", got)
	}
}

func TestGPUCompositorPacksMotionSourcesIntoReusableAtlas(t *testing.T) {
	compositor, err := NewGPUCompositor()
	if err != nil {
		t.Fatal(err)
	}
	defer compositor.Deallocate()
	plan := runtime.RenderPlan{
		Width:  2,
		Height: 1,
		Nodes: []runtime.RenderNode{{
			ID:          7,
			Bounds:      runtime.Rect{Width: 2, Height: 1},
			Type:        runtime.LayerAlpha,
			Visible:     true,
			Opacity:     255,
			ImageWidth:  2,
			ImageHeight: 1,
			Motion: &runtime.RenderMotion{
				Draws: []runtime.RenderMotionDraw{
					{SourceKey: "motion/a", RGBA: []byte{255, 0, 0, 255}, Width: 1, Height: 1, A: 1, D: 1, Opacity: 255},
					{SourceKey: "motion/b", RGBA: []byte{0, 255, 0, 255}, Width: 1, Height: 1, A: 1, D: 1, Opacity: 255},
				},
			},
		}},
	}
	if _, err := compositor.Compose(plan); err != nil {
		t.Fatal(err)
	}
	atlas := compositor.motionAtlases[7]
	if atlas == nil || atlas.texture == nil {
		t.Fatal("motion atlas was not allocated")
	}
	if len(atlas.entries) != 2 {
		t.Fatalf("motion atlas entries = %d, want 2", len(atlas.entries))
	}
	if got := atlas.texture.Bounds().Dy(); got != 2 {
		t.Fatalf("motion atlas height = %d, want 2", got)
	}
	texture := atlas.texture
	views := map[string]*ebiten.Image{
		"a": atlas.entries["motion/a"].view,
		"b": atlas.entries["motion/b"].view,
	}
	if _, err := compositor.Compose(plan); err != nil {
		t.Fatal(err)
	}
	if compositor.motionAtlases[7].texture != texture {
		t.Fatal("unchanged motion sources rebuilt the atlas")
	}
	if compositor.motionAtlases[7].entries["motion/a"].view != views["a"] ||
		compositor.motionAtlases[7].entries["motion/b"].view != views["b"] {
		t.Fatal("unchanged motion sources replaced atlas views")
	}
}

func TestPremultipliedPixelsMatchesPresentationContract(t *testing.T) {
	src := []byte{200, 100, 50, 128, 10, 20, 30, 255}
	dst := make([]byte, len(src))
	premultipliedPixels(src, dst, false)
	want := []byte{100, 50, 25, 128, 10, 20, 30, 255}
	for i := range want {
		if dst[i] != want[i] {
			t.Fatalf("premultiplied[%d] = %d, want %d; got %v", i, dst[i], want[i], dst)
		}
	}
}

func TestPremultipliedPixelsPreservesOpaqueRGB(t *testing.T) {
	src := []byte{200, 100, 50, 0, 10, 20, 30, 128}
	dst := make([]byte, len(src))
	premultipliedPixels(src, dst, true)
	want := []byte{200, 100, 50, 255, 10, 20, 30, 255}
	for i := range want {
		if dst[i] != want[i] {
			t.Fatalf("opaque premultiplied[%d] = %d, want %d; got %v", i, dst[i], want[i], dst)
		}
	}
}

func TestGPUSourceRectCropMapping(t *testing.T) {
	base := runtime.RenderNode{
		GPUStretch:                  true,
		GPUStretchSourceWidth:       4,
		GPUStretchSourceHeight:      4,
		GPUStretchCropWidth:         2,
		GPUStretchCropHeight:        2,
		GPUStretchSourceX:           1,
		GPUStretchSourceY:           1,
		GPUStretchDestinationWidth:  8,
		GPUStretchDestinationHeight: 8,
	}
	rect, scaleX, scaleY := gpuSourceRect(base, 0, 0, 8, 8)
	if rect.Min.X != 1 || rect.Min.Y != 1 || rect.Max.X != 3 || rect.Max.Y != 3 {
		t.Fatalf("crop source rect = %v, want (1,1)-(3,3)", rect)
	}
	if scaleX != 4 || scaleY != 4 {
		t.Fatalf("crop scale = %v,%v, want 4,4", scaleX, scaleY)
	}

	offset := base
	offset.GPUStretchDestinationX = 2
	offset.GPUStretchDestinationY = 2
	rect, _, _ = gpuSourceRect(offset, 2, 2, 10, 10)
	if rect.Min.X != 1 || rect.Min.Y != 1 || rect.Max.X != 3 || rect.Max.Y != 3 {
		t.Fatalf("offset source rect = %v, want (1,1)-(3,3)", rect)
	}

	zeroCrop := base
	zeroCrop.GPUStretchCropWidth = 0
	zeroCrop.GPUStretchCropHeight = 0
	rect, scaleX, scaleY = gpuSourceRect(zeroCrop, 0, 0, 8, 8)
	if rect.Min.X != 0 || rect.Min.Y != 0 || rect.Max.X != 4 || rect.Max.Y != 4 {
		t.Fatalf("zero-crop fallback rect = %v, want full source", rect)
	}
	if scaleX != 1 || scaleY != 1 {
		t.Fatalf("zero-crop scale = %v,%v, want 1,1", scaleX, scaleY)
	}
}
