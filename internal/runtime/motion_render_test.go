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

func TestMotionBlendModesMatchReference(t *testing.T) {
	opaque := func() []byte { return []byte{100, 100, 100, 255} }
	src := []byte{100, 100, 100, 255}
	cases := []struct {
		mode MotionBlendMode
		want byte
	}{
		{MotionBlendAdditive, 200},
		{MotionBlendSubtractive, 0},
		{MotionBlendMultiplicative, 39},
		{MotionBlendScreen, 161},
	}
	for _, test := range cases {
		dst := opaque()
		blendMotionPixel(dst, src, 255, test.mode)
		if dst[0] != test.want || dst[1] != test.want || dst[2] != test.want || dst[3] != 255 {
			t.Fatalf("mode %d result = %v, want %d and opaque", test.mode, dst, test.want)
		}
	}
	half := opaque()
	blendMotionPixel(half, src, 128, MotionBlendAdditive)
	if half[0] != 150 || half[3] != 255 {
		t.Fatalf("half-alpha additive = %v, want 150 with opaque alpha", half)
	}
	half = opaque()
	blendMotionPixel(half, src, 128, MotionBlendMultiplicative)
	if half[0] != 69 {
		t.Fatalf("half-alpha multiply = %v, want 69", half)
	}
}

func TestMotionBlendModeSelectsFromRawValue(t *testing.T) {
	cases := map[int64]MotionBlendMode{
		0:  MotionBlendAlpha,
		1:  MotionBlendAdditive,
		2:  MotionBlendSubtractive,
		5:  MotionBlendSubtractive,
		3:  MotionBlendMultiplicative,
		19: MotionBlendMultiplicative,
		4:  MotionBlendScreen,
	}
	for raw, want := range cases {
		if got := motionBlendMode(raw); got != want {
			t.Fatalf("motionBlendMode(%d) = %d, want %d", raw, got, want)
		}
	}
}

func TestMotionBlendImageAppliesMultiply(t *testing.T) {
	src := &assets.Image{Width: 1, Height: 1, RGBA: []byte{100, 100, 100, 255}}
	dst := []byte{100, 100, 100, 255}
	ctx := motionRenderContext{opacity: 255, blendMode: 3, matrix: identityMotionMatrix}
	if err := blendMotionImage(dst, 1, 1, src, 0.5, 0.5, 0, nil, true, ctx); err != nil {
		t.Fatal(err)
	}
	if dst[0] != 39 || dst[3] != 255 {
		t.Fatalf("multiply compose = %v, want 39 and opaque", dst)
	}
}

func TestMotionNestedTickFollowsSourceStart(t *testing.T) {
	node := map[string]assets.Value{
		"frameList": []assets.Value{
			map[string]assets.Value{"time": float64(0), "content": map[string]assets.Value{"src": "src/chara/mouth0"}},
			map[string]assets.Value{"time": float64(10), "content": map[string]assets.Value{"src": "motion/chara/nested"}},
			map[string]assets.Value{"time": float64(20), "content": map[string]assets.Value{"src": "motion/chara/other"}},
			map[string]assets.Value{"time": float64(30), "content": map[string]assets.Value{"src": "motion/chara/other"}},
		},
	}
	content := map[string]assets.Value{"src": "motion/chara/nested"}
	if got := motionNestedTick(node, content, 12); got != 2 {
		t.Fatalf("nested tick just after the source change = %v, want 2", got)
	}
	if got := motionNestedTick(node, content, 15); got != 5 {
		t.Fatalf("nested tick after the source change = %v, want 5", got)
	}
	if got := motionNestedTick(node, content, 22); got != 2 {
		t.Fatalf("nested tick after the second change = %v, want 2", got)
	}
}

func TestMotionNestedMotionAdvancesWithParentTime(t *testing.T) {
	resource := emotePlayerTestResource("nestedtime.psb")
	root := resource.doc.Root.(map[string]assets.Value)
	objects := root["object"].(map[string]assets.Value)
	motions := objects["chara"].(map[string]assets.Value)["motion"].(map[string]assets.Value)
	// A nested motion carries its own length; the reference clamps the child
	// tick to the clip's total frames (PlayerUpdateChildMotion.cpp:300-340).
	motions["nested"] = map[string]assets.Value{
		"lastTime": int64(4),
		"layer": []assets.Value{
			map[string]assets.Value{
				"type": int64(0),
				"frameList": []assets.Value{
					map[string]assets.Value{"time": float64(0), "content": map[string]assets.Value{"src": "src/chara/mouth0"}},
					map[string]assets.Value{"time": float64(2), "content": map[string]assets.Value{"src": "src/chara/mouth1"}},
					map[string]assets.Value{"time": float64(4), "content": map[string]assets.Value{"src": "src/chara/mouth0"}},
				},
			},
		},
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
	render := func(tick float64) []byte {
		frame, err := renderPSBMotionOptions(resource, "chara", "idle", tick, 0, nil, false, 0, 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		return frame.RGBA[(4*8+4)*4:]
	}
	if got := render(0); got[0] != 255 || got[2] != 0 {
		t.Fatalf("nested frame at t=0 = %v, want red", got[:4])
	}
	if got := render(2); got[2] != 255 || got[0] != 0 {
		t.Fatalf("nested frame at t=2 = %v, want blue", got[:4])
	}
}

func TestMotionParticlesEmitAndExpire(t *testing.T) {
	resource := emotePlayerTestResource("particles.psb")
	root := resource.doc.Root.(map[string]assets.Value)
	objects := root["object"].(map[string]assets.Value)
	motions := objects["chara"].(map[string]assets.Value)["motion"].(map[string]assets.Value)
	motions["nested"] = map[string]assets.Value{
		"lastTime": int64(10),
		"layer":    []assets.Value{motionTestSpriteLayer("src/chara/mouth0")},
	}
	layer := map[string]assets.Value{
		"label":              "flower",
		"type":               int64(4),
		"particleMaxNum":     int64(8),
		"particleAccelRatio": float64(1),
		"particleMotionList": []assets.Value{"motion/chara/nested"},
	}
	content := map[string]assets.Value{
		"src": "particle/point",
		"prt": map[string]assets.Value{
			"amin": float64(0), "amax": float64(0),
			"vmin": float64(0), "vmax": float64(0),
			"fmin": float64(2), "fmax": float64(2),
			"zmin": float64(1), "zmax": float64(1),
			"trigger": int64(1),
		},
	}
	ctx := motionRenderContext{opacity: 255, matrix: identityMotionMatrix}
	if got := len(motionParticles(resource, nil, layer, content, ctx, 0, 0, 0)); got != 1 {
		t.Fatalf("particles at tick 0 = %d, want 1", got)
	}
	if got := len(motionParticles(resource, nil, layer, content, ctx, 5, 0, 0)); got != 2 {
		t.Fatalf("particles at tick 5 = %d, want the 2-particle burst", got)
	}
	if got := len(motionParticles(resource, nil, layer, content, ctx, 20, 0, 0)); got != 0 {
		t.Fatalf("particles at tick 20 = %d, want 0 after the template ends", got)
	}
}

// A particle is spawned at the emitter's accumulated position for its birth
// frame. Emitters with inheritVelocity 0 leave it there while the emitter
// moves; mode 2 carries it along.
func TestMotionParticlesAnchorAtBirthPosition(t *testing.T) {
	resource := emotePlayerTestResource("anchor.psb")
	root := resource.doc.Root.(map[string]assets.Value)
	motions := root["object"].(map[string]assets.Value)["chara"].(map[string]assets.Value)["motion"].(map[string]assets.Value)
	motions["nested"] = map[string]assets.Value{
		"lastTime": int64(20),
		"layer":    []assets.Value{motionTestSpriteLayer("src/chara/mouth0")},
	}
	content := func(x float64) map[string]assets.Value {
		return map[string]assets.Value{
			"src":   "particle/point",
			"coord": []assets.Value{x, float64(0)},
			"prt": map[string]assets.Value{
				"amin": float64(0), "amax": float64(0),
				"vmin": float64(0), "vmax": float64(0),
				"fmin": float64(1), "fmax": float64(1),
				"zmin": float64(1), "zmax": float64(1),
				"range":   float64(0),
				"trigger": int64(1),
			},
		}
	}
	layer := func(inheritVelocity int64) map[string]assets.Value {
		return map[string]assets.Value{
			"label":                   "anchor",
			"type":                    int64(4),
			"particleMaxNum":          int64(4),
			"particleInheritVelocity": inheritVelocity,
			"particleMotionList":      []assets.Value{"motion/chara/nested"},
			"frameList": []assets.Value{
				map[string]assets.Value{"time": float64(0), "content": content(0)},
				map[string]assets.Value{"time": float64(10), "content": content(10)},
				map[string]assets.Value{"time": float64(20), "content": content(20)},
			},
		}
	}
	// The caller's context is the emitter at tick 10 (x = 10).
	ctx := motionRenderContext{opacity: 255, matrix: identityMotionMatrix, x: 10}
	static := motionParticles(resource, nil, layer(0), content(10), ctx, 10, 0, 0)
	if len(static) != 1 {
		t.Fatalf("particles = %d, want 1", len(static))
	}
	if static[0].x != -10 {
		t.Fatalf("inheritVelocity 0 particle x = %.1f, want -10 relative to the moved emitter", static[0].x)
	}
	carried := motionParticles(resource, nil, layer(2), content(10), ctx, 10, 0, 0)
	if len(carried) != 1 {
		t.Fatalf("particles = %d, want 1", len(carried))
	}
	if carried[0].x != 0 {
		t.Fatalf("inheritVelocity 2 particle x = %.1f, want 0 following the emitter", carried[0].x)
	}
}

// The reference stable-sorts prepared items by accumulated z, so a later
// sibling with a lower z paints first.
func TestMotionSortsDrawsByAccumulatedZ(t *testing.T) {
	resource := emotePlayerTestResource("zorder.psb")
	sprite := func(source string, z float64) assets.Value {
		return map[string]assets.Value{
			"type": int64(0),
			"frameList": []assets.Value{map[string]assets.Value{
				"time": float64(0),
				"content": map[string]assets.Value{
					"src":   source,
					"coord": []assets.Value{float64(0), float64(0), z},
				},
			}},
		}
	}
	motionTestIdle(resource)["layer"] = []assets.Value{
		sprite("src/chara/mouth1", 1),
		sprite("src/chara/mouth0", 0),
	}
	frame, err := renderPSBMotionOptions(resource, "chara", "idle", 0, 0, nil, false, 0, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Each 4x4 sprite is centered at canvas (4,4); the z=1 sprite also shifts
	// one row down through the z projection, so (4,5) is covered by both and
	// shows the last drawn sprite.
	if got := frame.RGBA[(5*8+4)*4:]; got[2] != 255 || got[0] != 0 {
		t.Fatalf("pixel = %v, want the z=1 blue sprite on top", got[:4])
	}
}

// A parent in coordinateMode 1 places children through the parent matrix in
// the x/z plane, leaving y literal; mode 0 rotates the same coordinates into
// x/y (PlayerUpdateLayerEval.cpp:912-920). The z=1 projection maps the child
// back onto the same row, so the two modes are distinguished by the context
// fields rather than by drawn pixels.
func TestMotionCoordinateModePlacesChildrenInXZ(t *testing.T) {
	build := func(coordinate int64) motionRenderContext {
		root := motionRootRenderContext(motionRenderEval{zFactor: 1, hasZFactor: true}, 1)
		parentContent := map[string]assets.Value{"angle": float64(90)}
		parentLayer := map[string]assets.Value{"type": int64(0), "coordinate": coordinate}
		parent := psbMotionContext(root, parentContent, parentLayer)
		childContent := map[string]assets.Value{
			"coord": []assets.Value{float64(10), float64(0), float64(0)},
		}
		return psbMotionContext(parent, childContent, map[string]assets.Value{"type": int64(0)})
	}
	if got := build(1); got.y != 0 || got.z != 10 {
		t.Fatalf("coordinate mode 1 child context = y %.3f z %.3f, want y 0 z 10", got.y, got.z)
	}
	if got := build(0); got.y != 10 || got.z != 0 {
		t.Fatalf("coordinate mode 0 child context = y %.3f z %.3f, want y 10 z 0", got.y, got.z)
	}
}

func TestSubtractiveMotionRGBAComplementsPremultipliedPixels(t *testing.T) {
	got := subtractiveMotionRGBA([]byte{100, 50, 200, 128})
	want := []byte{28, 78, 0, 128}
	if !bytes.Equal(got, want) {
		t.Fatalf("complement = %v, want %v", got, want)
	}
}

func TestMotionParticleEmitterDrawsTemplate(t *testing.T) {
	resource := emotePlayerTestResource("particle.psb")
	root := resource.doc.Root.(map[string]assets.Value)
	objects := root["object"].(map[string]assets.Value)
	motions := objects["chara"].(map[string]assets.Value)["motion"].(map[string]assets.Value)
	motions["nested"] = map[string]assets.Value{
		"layer": []assets.Value{motionTestSpriteLayer("src/chara/mouth0")},
	}
	motions["idle"].(map[string]assets.Value)["layer"] = []assets.Value{
		map[string]assets.Value{
			"label":              "flower",
			"type":               int64(4),
			"particleMaxNum":     int64(4),
			"particleMotionList": []assets.Value{"motion/chara/nested"},
			"frameList": []assets.Value{
				map[string]assets.Value{
					"time": float64(0),
					"content": map[string]assets.Value{
						"src": "particle/point",
						"prt": map[string]assets.Value{
							"amin": float64(0), "amax": float64(0),
							"vmin": float64(0), "vmax": float64(0),
							"fmin": float64(4), "fmax": float64(4),
							"zmin": float64(1), "zmax": float64(1),
							"trigger": int64(1),
						},
					},
				},
			},
		},
	}
	frame, err := renderPSBMotionOptions(resource, "chara", "idle", 2, 0, nil, false, 0, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	opaque := 0
	for offset := 3; offset < len(frame.RGBA); offset += 4 {
		if frame.RGBA[offset] != 0 {
			opaque++
		}
	}
	if opaque == 0 {
		t.Fatal("particle emitter drew no template pixels")
	}
}
