package runtime

import (
	"bytes"
	"testing"

	"github.com/eslzzyl/krkrgo/internal/assets"
)

func TestBlendMotionImageLinearFilterPreservesTransparentEdgeColor(t *testing.T) {
	src := &assets.Image{
		Width:  2,
		Height: 1,
		RGBA:   []byte{255, 0, 0, 255, 0, 0, 255, 0},
	}
	dst := make([]byte, 2*4)
	ctx := motionRenderContext{opacity: 255, matrix: identityMotionMatrix}
	if err := blendMotionImage(dst, 2, 1, src, 1.5, 0.5, 1, nil, false, ctx); err != nil {
		t.Fatalf("blend motion image: %v", err)
	}
	if got := dst[:4]; got[0] != 255 || got[1] != 0 || got[2] != 0 || got[3] != 128 {
		t.Fatalf("linear sample = %v, want premultiplied-alpha red at half opacity", got)
	}
}

func TestBlendMotionImageIdentityTranslationUsesSourcePixels(t *testing.T) {
	src := &assets.Image{
		Width:  2,
		Height: 1,
		RGBA:   []byte{255, 0, 0, 255, 0, 0, 255, 0},
	}
	dst := []byte{0, 255, 0, 255, 0, 255, 0, 255}
	ctx := motionRenderContext{opacity: 255, matrix: identityMotionMatrix}

	if err := blendMotionImage(dst, 2, 1, src, 1, 0.5, 1, nil, false, ctx); err != nil {
		t.Fatalf("blend motion image: %v", err)
	}
	if got := dst[:4]; got[0] != 255 || got[1] != 0 || got[2] != 0 || got[3] != 255 {
		t.Fatalf("identity sample = %v, want opaque red", got)
	}
	if got := dst[4:]; got[0] != 0 || got[1] != 255 || got[2] != 0 || got[3] != 255 {
		t.Fatalf("transparent source changed destination = %v", got)
	}
}

func TestBlendMotionImageIdentityTranslationBlendsTranslucentPixels(t *testing.T) {
	src := &assets.Image{
		Width:  1,
		Height: 1,
		RGBA:   []byte{53, 211, 231, 128},
	}
	dst := []byte{255, 255, 255, 255}
	ctx := motionRenderContext{opacity: 255, matrix: identityMotionMatrix}

	if err := blendMotionImage(dst, 1, 1, src, 0.5, 0.5, 1, nil, false, ctx); err != nil {
		t.Fatal(err)
	}
	if got, want := dst, []byte{153, 232, 242, 255}; !bytes.Equal(got, want) {
		t.Fatalf("translucent identity sample = %v, want %v", got, want)
	}
}

func TestBlendMotionImageOpaqueIdentityCopiesRows(t *testing.T) {
	src := &assets.Image{
		Width:  2,
		Height: 1,
		RGBA:   []byte{255, 0, 0, 255, 0, 0, 255, 255},
	}
	dst := []byte{0, 255, 0, 255, 0, 255, 0, 255}
	ctx := motionRenderContext{opacity: 255, matrix: identityMotionMatrix}

	if err := blendMotionImage(dst, 2, 1, src, 1, 0.5, 1, nil, true, ctx); err != nil {
		t.Fatalf("blend opaque motion image: %v", err)
	}
	if got := dst; got[0] != 255 || got[1] != 0 || got[2] != 0 || got[3] != 255 ||
		got[4] != 0 || got[5] != 0 || got[6] != 255 || got[7] != 255 {
		t.Fatalf("opaque identity copy = %v, want source pixels", got)
	}
}

func TestMaterializeGPUMotionUsesDeferredSourceTransform(t *testing.T) {
	layer := NewLayer(1)
	if err := layer.SetRGBA(2, 1, make([]byte, 2*4)); err != nil {
		t.Fatal(err)
	}
	layer.gpuMotion = &RenderMotion{
		Filter: 1,
		Draws: []RenderMotionDraw{{
			SourceKey: "test/source",
			RGBA:      []byte{255, 0, 0, 255},
			Width:     1,
			Height:    1,
			A:         1,
			D:         1,
			TX:        1,
			Opacity:   255,
		}},
	}
	if err := layer.materializeGPUMotion(); err != nil {
		t.Fatal(err)
	}
	if layer.gpuMotion != nil {
		t.Fatal("deferred motion was retained after materialization")
	}
	if got := layer.RGBA[:4]; got[3] != 0 {
		t.Fatalf("materialized first pixel = %v, want transparent", got)
	}
	if got := layer.RGBA[4:]; got[0] != 255 || got[1] != 0 || got[2] != 0 || got[3] != 255 {
		t.Fatalf("materialized translated pixel = %v, want opaque red", got)
	}
}

func motionTestIdle(resource *motionResource) map[string]assets.Value {
	root := resource.doc.Root.(map[string]assets.Value)
	objects := root["object"].(map[string]assets.Value)
	character := objects["chara"].(map[string]assets.Value)
	motions := character["motion"].(map[string]assets.Value)
	return motions["idle"].(map[string]assets.Value)
}

func motionTestSpriteLayer(source string) assets.Value {
	return map[string]assets.Value{
		"type": int64(0),
		"frameList": []assets.Value{
			map[string]assets.Value{
				"time":    float64(0),
				"content": map[string]assets.Value{"src": source},
			},
		},
	}
}

// A masked container (type 2) produces no draw item of its own, but the
// reference keeps its children as separate items in the flattened node list.
func TestMotionDrawsChildrenOfMaskedContainer(t *testing.T) {
	resource := emotePlayerTestResource("container.psb")
	motionTestIdle(resource)["layer"] = []assets.Value{
		map[string]assets.Value{
			"type":     int64(2),
			"label":    "slide",
			"children": []assets.Value{motionTestSpriteLayer("src/chara/mouth0")},
		},
	}
	frame, err := renderPSBMotionOptions(resource, "chara", "idle", 0, 0, nil, false, 0, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := frame.RGBA[(4*8+4)*4:]; got[0] != 255 || got[3] != 255 {
		t.Fatalf("masked container child pixel = %v, want opaque red", got[:4])
	}
}

// A type-3 node expands its child motion before the node type mask applies.
func TestMotionDrawsNestedMotionUnderChildPlayerNode(t *testing.T) {
	resource := emotePlayerTestResource("nested.psb")
	root := resource.doc.Root.(map[string]assets.Value)
	objects := root["object"].(map[string]assets.Value)
	motions := objects["chara"].(map[string]assets.Value)["motion"].(map[string]assets.Value)
	motions["nested"] = map[string]assets.Value{
		"layer": []assets.Value{motionTestSpriteLayer("src/chara/mouth1")},
	}
	motions["idle"].(map[string]assets.Value)["layer"] = []assets.Value{
		map[string]assets.Value{
			"type": int64(3),
			"frameList": []assets.Value{
				map[string]assets.Value{
					"time":    float64(0),
					"content": map[string]assets.Value{"src": "motion/chara/nested"},
				},
			},
		},
	}
	frame, err := renderPSBMotionOptions(resource, "chara", "idle", 0, 0, nil, false, 0, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := frame.RGBA[(4*8+4)*4:]; got[2] != 255 || got[3] != 255 {
		t.Fatalf("nested motion pixel = %v, want opaque blue", got[:4])
	}
}

// The GPU atlas keys uploads by source identity, so a tinted frame needs a key
// distinct from the untinted source and from other tints.
func TestGPUMotionSourceKeySeparatesTints(t *testing.T) {
	resource := emotePlayerTestResource("tintkey.psb")
	motionTestIdle(resource)["layer"] = []assets.Value{
		map[string]assets.Value{
			"type": int64(0),
			"frameList": []assets.Value{
				map[string]assets.Value{
					"time": float64(0),
					"content": map[string]assets.Value{
						"src": "src/chara/mouth0", "color": int64(0xff0000ff),
					},
				},
				map[string]assets.Value{
					"time": float64(1),
					"content": map[string]assets.Value{
						"src": "src/chara/mouth0", "color": int64(0xff000000),
					},
				},
				map[string]assets.Value{
					"time": float64(2),
					"content": map[string]assets.Value{
						"src": "src/chara/mouth0", "color": int64(0xffffffff),
					},
				},
			},
		},
	}
	build := func(tick float64) RenderMotionDraw {
		motion, err := buildGPUMotionVariables(resource, "chara", "idle", tick, 0,
			identityMotionDrawMatrix(), 1, nil, false, 0, 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(motion.Draws) != 1 {
			t.Fatalf("draws at tick %v = %d, want 1", tick, len(motion.Draws))
		}
		return motion.Draws[0]
	}
	red, black := build(0), build(1)
	if red.SourceKey == black.SourceKey {
		t.Fatalf("tinted keys collide: %q", red.SourceKey)
	}
	if bytes.Equal(red.RGBA, black.RGBA) {
		t.Fatalf("tint variants produced identical pixels: key=%q rgba=%v / key=%q rgba=%v", red.SourceKey, red.RGBA[:4], black.SourceKey, black.RGBA[:4])
	}
}

// Stencil groups and meshes bake the frame for each tick; the host streams
// those draws instead of caching them by source identity.
func TestGPUMotionMarksBakedFramesForStreaming(t *testing.T) {
	resource := emotePlayerTestResource("baked.psb")
	motionTestIdle(resource)["layer"] = []assets.Value{
		map[string]assets.Value{
			"type":        int64(12),
			"stencilType": int64(4),
			"children":    []assets.Value{motionTestSpriteLayer("src/chara/mouth0")},
		},
	}
	motion, err := buildGPUMotionVariables(resource, "chara", "idle", 0, 0,
		identityMotionDrawMatrix(), 1, nil, false, 0, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(motion.Draws) != 1 || !motion.Draws[0].Baked {
		t.Fatalf("baked draws = %+v, want one streamed draw", motion.Draws)
	}
}
