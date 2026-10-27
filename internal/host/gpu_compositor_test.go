package host

import (
	"image"
	"reflect"
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

func TestMotionGroupBoundsCropsOffscreenTargets(t *testing.T) {
	group := &runtime.RenderMotionGroup{
		Items: []runtime.RenderMotionItem{{Draw: runtime.RenderMotionDraw{
			Width: 12, Height: 8, A: 1, D: 1, TX: 96, TY: 72,
		}}},
		Masks: []runtime.RenderMotionMask{{Items: []runtime.RenderMotionItem{{Draw: runtime.RenderMotionDraw{
			Width: 4, Height: 6, A: 1, D: 1, TX: 104, TY: 76,
		}}}}},
	}
	got := motionBoundsRect(motionGroupBounds(group), 0, 0, 320, 240)
	want := image.Rect(96, 72, 108, 80)
	if got != want {
		t.Fatalf("motion group crop = %v, want %v", got, want)
	}
}

func TestMotionGroupBoundsKeepsMaskOnlyWhenOperationAddsAlpha(t *testing.T) {
	content := runtime.RenderMotionItem{Draw: runtime.RenderMotionDraw{
		Width: 8, Height: 8, A: 1, D: 1, TX: 10, TY: 10,
	}}
	mask := runtime.RenderMotionItem{Draw: runtime.RenderMotionDraw{
		Width: 6, Height: 6, A: 1, D: 1, TX: 30, TY: 30,
	}}
	group := &runtime.RenderMotionGroup{
		Items: []runtime.RenderMotionItem{content},
		Masks: []runtime.RenderMotionMask{{Items: []runtime.RenderMotionItem{mask}, Operation: 1}},
	}
	if got, want := motionBoundsRect(motionGroupBounds(group), 0, 0, 64, 64), image.Rect(10, 10, 18, 18); got != want {
		t.Fatalf("threshold mask crop = %v, want %v", got, want)
	}
	group.Masks[0].Operation = 5
	if got, want := motionBoundsRect(motionGroupBounds(group), 0, 0, 64, 64), image.Rect(10, 10, 36, 36); got != want {
		t.Fatalf("union mask crop = %v, want %v", got, want)
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

func TestDirectMotionAlphaMatchesGroupedComposition(t *testing.T) {
	draw := runtime.RenderMotionDraw{
		SourceKey: "motion/direct-alpha",
		RGBA:      []byte{255, 0, 0, 255},
		Width:     1,
		Height:    1,
		A:         1,
		D:         1,
		Opacity:   128,
	}
	plan := func(grouped bool) runtime.RenderPlan {
		motion := &runtime.RenderMotion{Draws: []runtime.RenderMotionDraw{draw}}
		if grouped {
			motion.Items = []runtime.RenderMotionItem{{Group: &runtime.RenderMotionGroup{
				Items: []runtime.RenderMotionItem{{Draw: draw}},
			}}}
		}
		return runtime.RenderPlan{
			Width: 2, Height: 1, Background: [4]uint8{10, 20, 30, 255},
			ChildIndices: []int{1},
			Nodes: []runtime.RenderNode{
				{ID: 1, Type: runtime.LayerBinder, Visible: true, Opacity: 255, ChildCount: 1},
				{ID: 2, Bounds: runtime.Rect{Width: 1, Height: 1}, Type: runtime.LayerAlpha, Visible: true, Opacity: 255,
					ImageWidth: 1, ImageHeight: 1, Motion: motion},
			},
		}
	}
	compose := func(plan runtime.RenderPlan) (bool, float32) {
		compositor, err := NewGPUCompositor()
		if err != nil {
			t.Fatal(err)
		}
		defer compositor.Deallocate()
		if _, err := compositor.Compose(plan); err != nil {
			t.Fatal(err)
		}
		var alpha float32
		if len(compositor.motionVertices) != 0 {
			alpha = compositor.motionVertices[0].ColorA
		}
		return compositor.motionTargets[2] != nil, alpha
	}
	directTarget, directAlpha := compose(plan(false))
	groupedTarget, _ := compose(plan(true))
	if directTarget {
		t.Fatal("direct alpha Motion allocated an intermediate target")
	}
	if !groupedTarget {
		t.Fatal("grouped Motion did not retain the intermediate target")
	}
	if want := float32(128) / 255; directAlpha != want {
		t.Fatalf("direct alpha vertex = %g, want %g", directAlpha, want)
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
				Filter: 1,
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
	if got := atlas.texture.Bounds().Dy(); got != 8 {
		t.Fatalf("motion atlas height = %d, want 8", got)
	}
	if first, second := atlas.entries["motion/a"], atlas.entries["motion/b"]; first.storage.Intersect(second.storage).Dx() != 0 || first.storage.Intersect(second.storage).Dy() != 0 {
		t.Fatalf("motion atlas storage overlaps: first=%v second=%v", first.storage, second.storage)
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

func TestMotionPaddedUploadReplicatesEdges(t *testing.T) {
	compositor := &GPUCompositor{}
	src := []byte{
		10, 20, 30, 255, 40, 50, 60, 128,
		70, 80, 90, 64, 100, 110, 120, 0,
	}
	got := compositor.motionPaddedUpload(src, 2, 2)
	row := 4 * 4
	want := []byte{
		10, 20, 30, 255, 10, 20, 30, 255, 40, 50, 60, 128, 40, 50, 60, 128,
		10, 20, 30, 255, 10, 20, 30, 255, 40, 50, 60, 128, 40, 50, 60, 128,
		70, 80, 90, 64, 70, 80, 90, 64, 100, 110, 120, 0, 100, 110, 120, 0,
		70, 80, 90, 64, 70, 80, 90, 64, 100, 110, 120, 0, 100, 110, 120, 0,
	}
	if len(got) != row*4 || !reflect.DeepEqual(got, want) {
		t.Fatalf("padded upload = %v, want %v", got, want)
	}
}

func TestMotionAtlasCopyPositionNormalizesSourceBounds(t *testing.T) {
	x, y := motionAtlasCopyPosition(image.Rect(7, 11, 9, 15), 2, 3)
	if x != -5 || y != -8 {
		t.Fatalf("motion atlas copy position = (%g, %g), want (-5, -8)", x, y)
	}
}
func TestMotionViewportClipRectUsesCanvasCoordinates(t *testing.T) {
	draw := runtime.RenderMotionDraw{
		HasViewport: true,
		Viewport:    [4]float64{-2, -1, 3, 4},
	}
	got, ok := motionViewportClipRect(draw, 10, 8, 2, 1, image.Rect(0, 0, 10, 8))
	if !ok {
		t.Fatal("viewport clip was rejected")
	}
	if want := image.Rect(1, 2, 6, 7); got != want {
		t.Fatalf("viewport clip = %v, want %v", got, want)
	}
}

func TestMotionHasViewportIncludesItems(t *testing.T) {
	motion := &runtime.RenderMotion{
		Items: []runtime.RenderMotionItem{{
			Draw: runtime.RenderMotionDraw{HasViewport: true},
		}},
	}
	if !motionHasViewport(motion) {
		t.Fatal("item viewport was not detected")
	}
}

func TestGPUCompositorSharesMotionAtlasAcrossLayers(t *testing.T) {
	compositor, err := NewGPUCompositor()
	if err != nil {
		t.Fatal(err)
	}
	defer compositor.Deallocate()
	draw := runtime.RenderMotionDraw{
		SourceKey: "motion/shared",
		RGBA:      []byte{255, 0, 0, 255},
		Width:     1,
		Height:    1,
		A:         1,
		D:         1,
		Opacity:   255,
	}
	plan := runtime.RenderPlan{
		Width:        2,
		Height:       1,
		ChildIndices: []int{1, 2},
		Nodes: []runtime.RenderNode{
			{ID: 1, Type: runtime.LayerBinder, Visible: true, Opacity: 255, ChildCount: 2},
			{ID: 2, Bounds: runtime.Rect{Width: 1, Height: 1}, Type: runtime.LayerAlpha, Visible: true, Opacity: 255, ImageWidth: 1, ImageHeight: 1, Motion: &runtime.RenderMotion{AtlasKey: "shared", Draws: []runtime.RenderMotionDraw{draw}}},
			{ID: 3, Bounds: runtime.Rect{X: 1, Width: 1, Height: 1}, Type: runtime.LayerAlpha, Visible: true, Opacity: 255, ImageWidth: 1, ImageHeight: 1, Motion: &runtime.RenderMotion{AtlasKey: "shared", Draws: []runtime.RenderMotionDraw{draw}}},
		},
	}
	if _, err := compositor.Compose(plan); err != nil {
		t.Fatal(err)
	}
	atlas := compositor.sharedMotionAtlases["shared"]
	if atlas == nil || len(atlas.entries) != 1 {
		t.Fatalf("shared motion atlas = %#v, want one source entry", atlas)
	}
	plan.Nodes[0].ChildCount = 0
	plan.ChildIndices = nil
	if _, err := compositor.Compose(plan); err != nil {
		t.Fatal(err)
	}
	if _, err := compositor.Compose(plan); err != nil {
		t.Fatal(err)
	}
	if len(compositor.sharedMotionAtlases) != 0 {
		t.Fatalf("stale shared motion atlases = %d, want 0", len(compositor.sharedMotionAtlases))
	}
}

func TestGPUCompositorFlushesSharedBatchBeforeAtlasRebuild(t *testing.T) {
	compositor, err := NewGPUCompositor()
	if err != nil {
		t.Fatal(err)
	}
	defer compositor.Deallocate()
	plan := runtime.RenderPlan{
		Width:        4,
		Height:       2,
		ChildIndices: []int{1, 2},
		Nodes: []runtime.RenderNode{
			{ID: 1, Type: runtime.LayerBinder, Visible: true, Opacity: 255, ChildCount: 2},
			{ID: 2, Bounds: runtime.Rect{Width: 1, Height: 1}, Type: runtime.LayerAlpha, Visible: true, Opacity: 255, ImageWidth: 1, ImageHeight: 1, Motion: &runtime.RenderMotion{
				AtlasKey: "shared-rebuild",
				Draws:    []runtime.RenderMotionDraw{{SourceKey: "motion/small", RGBA: []byte{255, 0, 0, 255}, Width: 1, Height: 1, A: 1, D: 1, Opacity: 255}},
			}},
			{ID: 3, Bounds: runtime.Rect{X: 1, Width: 2, Height: 2}, Type: runtime.LayerAlpha, Visible: true, Opacity: 255, ImageWidth: 2, ImageHeight: 2, Motion: &runtime.RenderMotion{
				AtlasKey: "shared-rebuild",
				Draws:    []runtime.RenderMotionDraw{{SourceKey: "motion/large", RGBA: []byte{0, 255, 0, 255, 0, 255, 0, 255, 0, 255, 0, 255, 0, 255, 0, 255}, Width: 2, Height: 2, A: 1, D: 1, Opacity: 255}},
			}},
		},
	}
	if _, err := compositor.Compose(plan); err != nil {
		t.Fatal(err)
	}
	atlas := compositor.sharedMotionAtlases["shared-rebuild"]
	if atlas == nil || atlas.texture == nil || len(atlas.entries) != 2 {
		t.Fatalf("rebuilt shared motion atlas = %#v, want two live entries", atlas)
	}
	if atlas.entries["motion/small"].view == nil || atlas.entries["motion/large"].view == nil {
		t.Fatal("rebuilt shared motion atlas has an empty source view")
	}
}

func TestGPUCompositorRebuildsPaddedAtlasWithNarrowEntry(t *testing.T) {
	compositor, err := NewGPUCompositor()
	if err != nil {
		t.Fatal(err)
	}
	defer compositor.Deallocate()
	plan := runtime.RenderPlan{
		Width:        4,
		Height:       2,
		ChildIndices: []int{1, 2},
		Nodes: []runtime.RenderNode{
			{ID: 1, Type: runtime.LayerBinder, Visible: true, Opacity: 255, ChildCount: 2},
			{ID: 2, Bounds: runtime.Rect{Width: 2, Height: 2}, Type: runtime.LayerAlpha, Visible: true, Opacity: 255, ImageWidth: 2, ImageHeight: 2, Motion: &runtime.RenderMotion{
				AtlasKey: "padded-rebuild",
				Filter:   1,
				Draws: []runtime.RenderMotionDraw{{
					SourceKey: "motion/a-large",
					RGBA:      []byte{0, 255, 0, 255, 0, 255, 0, 255, 0, 255, 0, 255, 0, 255, 0, 255},
					Width:     2,
					Height:    2,
					A:         1,
					D:         1,
					Opacity:   255,
				}},
			}},
			{ID: 3, Bounds: runtime.Rect{X: 2, Width: 1, Height: 1}, Type: runtime.LayerAlpha, Visible: true, Opacity: 255, ImageWidth: 1, ImageHeight: 1, Motion: &runtime.RenderMotion{
				AtlasKey: "padded-rebuild",
				Filter:   1,
				Draws: []runtime.RenderMotionDraw{{
					SourceKey: "motion/z-small",
					RGBA:      []byte{255, 0, 0, 255},
					Width:     1,
					Height:    1,
					A:         1,
					D:         1,
					Opacity:   255,
				}},
			}},
		},
	}
	if _, err := compositor.Compose(plan); err != nil {
		t.Fatal(err)
	}
	atlas := compositor.sharedMotionAtlases["padded-rebuild"]
	if atlas == nil || !atlas.padding || len(atlas.entries) != 2 {
		t.Fatalf("padded rebuilt atlas = %#v, want two entries", atlas)
	}
	if got := atlas.entries["motion/a-large"].storage.Dx(); got != 4 {
		t.Fatalf("large storage width = %d, want 4", got)
	}
	if got := atlas.entries["motion/z-small"].storage.Dx(); got != 3 {
		t.Fatalf("small storage width = %d, want 3", got)
	}
}

func TestGPUCompositorReusesSharedMotionAtlasSlot(t *testing.T) {
	compositor, err := NewGPUCompositor()
	if err != nil {
		t.Fatal(err)
	}
	defer compositor.Deallocate()
	plan := runtime.RenderPlan{
		Width:        1,
		Height:       1,
		ChildIndices: []int{1},
		Nodes: []runtime.RenderNode{
			{ID: 1, Type: runtime.LayerBinder, Visible: true, Opacity: 255, ChildCount: 1},
			{ID: 2, Bounds: runtime.Rect{Width: 1, Height: 1}, Type: runtime.LayerAlpha, Visible: true, Opacity: 255, ImageWidth: 1, ImageHeight: 1, Motion: &runtime.RenderMotion{
				AtlasKey: "shared",
				Draws:    []runtime.RenderMotionDraw{{SourceKey: "motion/a", RGBA: []byte{255, 0, 0, 255}, Width: 1, Height: 1, A: 1, D: 1, Opacity: 255}},
			}},
		},
	}
	if _, err := compositor.Compose(plan); err != nil {
		t.Fatal(err)
	}
	texture := compositor.sharedMotionAtlases["shared"].texture
	plan.Nodes[1].Motion.Draws[0].SourceKey = "motion/b"
	if _, err := compositor.Compose(plan); err != nil {
		t.Fatal(err)
	}
	atlas := compositor.sharedMotionAtlases["shared"]
	if atlas == nil || atlas.texture != texture || len(atlas.entries) != 1 {
		t.Fatalf("shared atlas reuse = %#v, want same texture with one active entry", atlas)
	}
}

func TestGPUCompositorReusesPaddedSharedMotionAtlasSlot(t *testing.T) {
	compositor, err := NewGPUCompositor()
	if err != nil {
		t.Fatal(err)
	}
	defer compositor.Deallocate()
	plan := func(key string) runtime.RenderPlan {
		return runtime.RenderPlan{
			Width:  1,
			Height: 1,
			Nodes: []runtime.RenderNode{{
				ID:          21,
				Bounds:      runtime.Rect{Width: 1, Height: 1},
				Type:        runtime.LayerAlpha,
				Visible:     true,
				Opacity:     255,
				ImageWidth:  1,
				ImageHeight: 1,
				Motion: &runtime.RenderMotion{
					AtlasKey: "padded-shared",
					Filter:   1,
					Draws: []runtime.RenderMotionDraw{{
						SourceKey: key, RGBA: []byte{255, 0, 0, 255}, Width: 1, Height: 1,
						A: 1, D: 1, Opacity: 255,
					}},
				},
			}},
		}
	}
	if _, err := compositor.Compose(plan("motion/a")); err != nil {
		t.Fatal(err)
	}
	atlas := compositor.sharedMotionAtlases["padded-shared"]
	if atlas == nil || !atlas.padding || atlas.texture == nil {
		t.Fatalf("padded shared atlas = %#v", atlas)
	}
	texture := atlas.texture
	if _, err := compositor.Compose(plan("motion/b")); err != nil {
		t.Fatal(err)
	}
	atlas = compositor.sharedMotionAtlases["padded-shared"]
	if atlas == nil || atlas.texture != texture || len(atlas.entries) != 1 || atlas.entries["motion/b"] == nil {
		t.Fatalf("padded shared slot reuse = %#v, want same texture with motion/b", atlas)
	}
}

func TestGPUCompositorPadsExistingAtlasWhenFilterChanges(t *testing.T) {
	compositor, err := NewGPUCompositor()
	if err != nil {
		t.Fatal(err)
	}
	defer compositor.Deallocate()
	plan := func(filter int64) runtime.RenderPlan {
		return runtime.RenderPlan{
			Width:  1,
			Height: 1,
			Nodes: []runtime.RenderNode{{
				ID:          31,
				Bounds:      runtime.Rect{Width: 1, Height: 1},
				Type:        runtime.LayerAlpha,
				Visible:     true,
				Opacity:     255,
				ImageWidth:  1,
				ImageHeight: 1,
				Motion: &runtime.RenderMotion{
					AtlasKey: "filter-change",
					Filter:   filter,
					Draws: []runtime.RenderMotionDraw{{
						SourceKey: "motion/filter-change",
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
	}
	if _, err := compositor.Compose(plan(0)); err != nil {
		t.Fatal(err)
	}
	if _, err := compositor.Compose(plan(1)); err != nil {
		t.Fatal(err)
	}
	atlas := compositor.sharedMotionAtlases["filter-change"]
	if atlas == nil {
		t.Fatalf("filter-change atlas = %#v, want padded source upload", atlas)
	}
	entry := atlas.entries["motion/filter-change"]
	if !atlas.padding || entry == nil || len(entry.upload) != 3*3*4 {
		t.Fatalf("filter-change atlas = %#v, want padded source upload", atlas)
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

func TestPremultipliedPixelsCopiesFullyOpaqueSource(t *testing.T) {
	src := []byte{200, 100, 50, 255, 10, 20, 30, 255}
	dst := make([]byte, len(src))
	premultipliedPixels(src, dst, false)
	for i := range src {
		if dst[i] != src[i] {
			t.Fatalf("opaque source pixel[%d] = %d, want %d; got %v", i, dst[i], src[i], dst)
		}
	}
}

func TestPremultipliedUploadReusesFullyOpaqueSource(t *testing.T) {
	compositor := &GPUCompositor{}
	src := []byte{200, 100, 50, 255}
	got := compositor.premultipliedUpload(src, false)
	if len(got) == 0 || &got[0] != &src[0] {
		t.Fatalf("opaque upload allocated a copy: got=%p src=%p", &got[0], &src[0])
	}
}

func TestPremultipliedUploadConvertsTranslucentSource(t *testing.T) {
	compositor := &GPUCompositor{}
	src := []byte{200, 100, 50, 128}
	got := compositor.premultipliedUpload(src, false)
	if &got[0] == &src[0] || !reflect.DeepEqual(got, []byte{100, 50, 25, 128}) {
		t.Fatalf("translucent upload = %v, source=%v", got, src)
	}
}

func TestMotionUploadTrustsKnownOpaqueSource(t *testing.T) {
	compositor := &GPUCompositor{}
	src := []byte{200, 100, 50, 255}
	got := compositor.motionUpload(src, true, false)
	if len(got) == 0 || &got[0] != &src[0] {
		t.Fatalf("known opaque upload allocated a copy: got=%p src=%p", &got[0], &src[0])
	}
}

func TestMotionUploadTrustsPremultipliedSource(t *testing.T) {
	compositor := &GPUCompositor{}
	src := []byte{78, 103, 28, 128}
	got := compositor.motionUpload(src, false, true)
	if len(got) == 0 || &got[0] != &src[0] {
		t.Fatalf("premultiplied upload allocated a copy: got=%p src=%p", &got[0], &src[0])
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

func TestGPUTexturePoolReusesReleasedImage(t *testing.T) {
	compositor, err := NewGPUCompositor()
	if err != nil {
		t.Fatal(err)
	}
	defer compositor.Deallocate()
	first := compositor.acquireTexture(4, 3)
	compositor.releaseTexture(first, 4, 3)
	second := compositor.acquireTexture(4, 3)
	if second != first {
		t.Fatal("texture pool did not reuse the released image")
	}
	if got := second.Bounds().Size(); got.X != 4 || got.Y != 3 {
		t.Fatalf("pooled texture size = %v, want 4x3", got)
	}
	second.Deallocate()
}

func TestPremultipliedRegionOnlyConvertsDirtyRows(t *testing.T) {
	source := []byte{
		10, 20, 30, 255, 40, 50, 60, 255, 70, 80, 90, 255,
		11, 21, 31, 128, 41, 51, 61, 128, 71, 81, 91, 128,
		12, 22, 32, 0, 42, 52, 62, 0, 72, 82, 92, 0,
	}
	dst := make([]byte, 2*1*4)
	premultipliedRegion(source, 3, runtime.Rect{X: 1, Y: 1, Width: 2, Height: 1}, dst, false)
	want := []byte{21, 26, 31, 128, 36, 41, 46, 128}
	if string(dst) != string(want) {
		t.Fatalf("premultiplied region = %v, want %v", dst, want)
	}
}

func TestGPUCompositorReleasesInvisibleLayerTextures(t *testing.T) {
	compositor, err := NewGPUCompositor()
	if err != nil {
		t.Fatal(err)
	}
	defer compositor.Deallocate()
	node := runtime.RenderNode{
		ID: 1, Bounds: runtime.Rect{Width: 2, Height: 2}, Type: runtime.LayerAlpha,
		Visible: true, Opacity: 255, ImageWidth: 2, ImageHeight: 2,
		RGBA: []byte{
			1, 2, 3, 255, 4, 5, 6, 255,
			7, 8, 9, 255, 10, 11, 12, 255,
		},
		ImageHash: 1,
	}
	plan := runtime.RenderPlan{Width: 2, Height: 2, Nodes: []runtime.RenderNode{node}}
	if _, err := compositor.Compose(plan); err != nil {
		t.Fatal(err)
	}
	if len(compositor.textures) == 0 {
		t.Fatal("visible layer did not allocate a texture")
	}
	plan.Nodes[0].Visible = false
	if _, err := compositor.Compose(plan); err != nil {
		t.Fatal(err)
	}
	if len(compositor.textures) != 0 {
		t.Fatalf("invisible layer retained %d textures", len(compositor.textures))
	}
	if compositor.pooledBytes == 0 {
		t.Fatal("released texture was not returned to the pool")
	}
}

func TestGPUCompositorStatsTrackUploadScratch(t *testing.T) {
	compositor, err := NewGPUCompositor()
	if err != nil {
		t.Fatal(err)
	}
	defer compositor.Deallocate()
	rgba := []byte{
		1, 2, 3, 128, 4, 5, 6, 255, 7, 8, 9, 255, 10, 11, 12, 255,
		13, 14, 15, 255, 16, 17, 18, 255, 19, 20, 21, 255, 22, 23, 24, 255,
		25, 26, 27, 255, 28, 29, 30, 255, 31, 32, 33, 255, 34, 35, 36, 255,
		37, 38, 39, 255, 40, 41, 42, 255, 43, 44, 45, 255, 46, 47, 48, 255,
	}
	plan := runtime.RenderPlan{
		Width:  4,
		Height: 4,
		Nodes: []runtime.RenderNode{{
			ID:          1,
			Bounds:      runtime.Rect{Width: 4, Height: 4},
			Type:        runtime.LayerOpaque,
			Visible:     true,
			Opacity:     255,
			ImageWidth:  4,
			ImageHeight: 4,
			RGBA:        rgba,
			ImageHash:   1,
		}},
	}
	if _, err := compositor.Compose(plan); err != nil {
		t.Fatal(err)
	}
	stats := compositor.Stats()
	if stats.TextureBytes < int64(len(rgba)) {
		t.Fatalf("texture bytes = %d, want at least %d", stats.TextureBytes, len(rgba))
	}
	if stats.UploadScratchBytes != int64(len(rgba)) {
		t.Fatalf("upload scratch bytes = %d, want %d", stats.UploadScratchBytes, len(rgba))
	}
	if _, err := compositor.Compose(plan); err != nil {
		t.Fatal(err)
	}
	if got := compositor.Stats(); got.UploadScratchBytes != stats.UploadScratchBytes || got.Textures != stats.Textures {
		t.Fatalf("compose changed storage: first=%+v second=%+v", stats, got)
	}
}

func TestGPUCompositorStreamsBakedMotionDraws(t *testing.T) {
	compositor, err := NewGPUCompositor()
	if err != nil {
		t.Fatal(err)
	}
	defer compositor.Deallocate()
	plan := func(red byte) runtime.RenderPlan {
		return runtime.RenderPlan{
			Width:  1,
			Height: 1,
			Nodes: []runtime.RenderNode{{
				ID:          9,
				Bounds:      runtime.Rect{Width: 1, Height: 1},
				Type:        runtime.LayerAlpha,
				Visible:     true,
				Opacity:     255,
				ImageWidth:  1,
				ImageHeight: 1,
				Motion: &runtime.RenderMotion{
					Draws: []runtime.RenderMotionDraw{{
						SourceKey: "baked/tick-0",
						RGBA:      []byte{red, 0, 0, 255},
						Width:     1,
						Height:    1,
						A:         1,
						D:         1,
						Opacity:   255,
						Baked:     true,
					}},
				},
			}},
		}
	}
	if _, err := compositor.Compose(plan(255)); err != nil {
		t.Fatal(err)
	}
	stream := compositor.motionStreams[9]
	if stream == nil || stream.image == nil {
		t.Fatal("baked motion draw did not allocate a stream texture")
	}
	if atlas := compositor.motionAtlases[9]; atlas != nil && len(atlas.entries) != 0 {
		t.Fatalf("baked draw entered the source atlas: %d entries", len(atlas.entries))
	}
	image := stream.image
	if _, err := compositor.Compose(plan(0)); err != nil {
		t.Fatal(err)
	}
	if compositor.motionStreams[9].image != image {
		t.Fatal("baked motion draw reallocated the stream texture")
	}
}

func TestGPUCompositorEvictsStaleMotionSources(t *testing.T) {
	compositor, err := NewGPUCompositor()
	if err != nil {
		t.Fatal(err)
	}
	defer compositor.Deallocate()
	node := func(draws ...runtime.RenderMotionDraw) runtime.RenderPlan {
		return runtime.RenderPlan{
			Width:  1,
			Height: 1,
			Nodes: []runtime.RenderNode{{
				ID:          11,
				Bounds:      runtime.Rect{Width: 1, Height: 1},
				Type:        runtime.LayerAlpha,
				Visible:     true,
				Opacity:     255,
				ImageWidth:  1,
				ImageHeight: 1,
				Motion:      &runtime.RenderMotion{Draws: draws},
			}},
		}
	}
	a := runtime.RenderMotionDraw{SourceKey: "motion/a", RGBA: []byte{255, 0, 0, 255}, Width: 1, Height: 1, A: 1, D: 1, Opacity: 255}
	b := runtime.RenderMotionDraw{SourceKey: "motion/b", RGBA: []byte{0, 255, 0, 255}, Width: 1, Height: 1, A: 1, D: 1, Opacity: 255}
	if _, err := compositor.Compose(node(a, b)); err != nil {
		t.Fatal(err)
	}
	if got := len(compositor.motionAtlases[11].entries); got != 2 {
		t.Fatalf("atlas entries = %d, want 2", got)
	}
	if _, err := compositor.Compose(node(a)); err != nil {
		t.Fatal(err)
	}
	atlas := compositor.motionAtlases[11]
	if len(atlas.entries) != 1 || atlas.entries["motion/a"] == nil {
		t.Fatalf("atlas entries after eviction = %v, want only motion/a", len(atlas.entries))
	}
}

func TestGPUMotionBlendFactors(t *testing.T) {
	if got := gpuMotionBlend(runtime.MotionBlendAlpha); got != ebiten.BlendSourceOver {
		t.Fatalf("alpha blend = %+v, want source-over", got)
	}
	add := gpuMotionBlend(runtime.MotionBlendAdditive)
	if add.BlendFactorSourceRGB != ebiten.BlendFactorOne ||
		add.BlendFactorDestinationRGB != ebiten.BlendFactorOne ||
		add.BlendFactorDestinationAlpha != ebiten.BlendFactorOneMinusSourceAlpha {
		t.Fatalf("additive blend = %+v", add)
	}
	mul := gpuMotionBlend(runtime.MotionBlendMultiplicative)
	if mul.BlendFactorSourceRGB != ebiten.BlendFactorDestinationColor ||
		mul.BlendFactorDestinationRGB != ebiten.BlendFactorOneMinusSourceAlpha ||
		mul.BlendFactorSourceAlpha != ebiten.BlendFactorZero ||
		mul.BlendFactorDestinationAlpha != ebiten.BlendFactorOne {
		t.Fatalf("multiplicative blend = %+v", mul)
	}
	screen := gpuMotionBlend(runtime.MotionBlendScreen)
	if screen.BlendFactorSourceRGB != ebiten.BlendFactorOneMinusDestinationColor ||
		screen.BlendFactorDestinationRGB != ebiten.BlendFactorOne {
		t.Fatalf("screen blend = %+v", screen)
	}
	sub := gpuMotionBlend(runtime.MotionBlendSubtractive)
	if sub.BlendOperationRGB != ebiten.BlendOperationReverseSubtract ||
		sub.BlendFactorSourceRGB != ebiten.BlendFactorOne ||
		sub.BlendFactorDestinationRGB != ebiten.BlendFactorOne ||
		sub.BlendFactorSourceAlpha != ebiten.BlendFactorZero ||
		sub.BlendFactorDestinationAlpha != ebiten.BlendFactorOne ||
		sub.BlendOperationAlpha != ebiten.BlendOperationAdd {
		t.Fatalf("subtractive blend = %+v", sub)
	}
}
