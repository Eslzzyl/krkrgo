package runtime

import (
	"bytes"
	"testing"

	"github.com/eslzzyl/krkrgo/internal/assets"
	"github.com/eslzzyl/krkrgo/internal/tjs"
)

// motionCameraTestResource builds a 16x16 motion canvas with a 4x4 opaque
// block at coord (4,4). The camera layers are top-level siblings of the block,
// matching the PSB "layer" array the reference flattens under its synthetic
// root (NodeTree.cpp:369-378).
func motionCameraTestResource(path string, cameras []assets.Value) *motionResource {
	icon := map[string]assets.Value{
		"width":   int64(4),
		"height":  int64(4),
		"originX": int64(0),
		"originY": int64(0),
	}
	block := map[string]assets.Value{
		"label": "block",
		"type":  int64(0),
		"frameList": []assets.Value{
			map[string]assets.Value{
				"time": float64(0),
				"content": map[string]assets.Value{
					"src":   "src/chara/block",
					"coord": []assets.Value{int64(4), int64(4)},
				},
			},
		},
	}
	layers := append(append([]assets.Value(nil), cameras...), block)
	return &motionResource{
		path: path,
		doc: &assets.Document{Root: map[string]assets.Value{
			"screenSize": map[string]assets.Value{"width": int64(16), "height": int64(16)},
			"source": map[string]assets.Value{
				"chara": map[string]assets.Value{
					"icon": map[string]assets.Value{"block": icon},
				},
			},
			"object": map[string]assets.Value{
				"chara": map[string]assets.Value{
					"motion": map[string]assets.Value{
						"idle": map[string]assets.Value{
							"width":  int64(16),
							"height": int64(16),
							"layer":  layers,
						},
					},
				},
			},
		}},
		images: map[string]*assets.Image{
			"chara/block": {Width: 4, Height: 4, RGBA: motionCameraTestSolid(4, 4, 255, 0, 0, 255)},
		},
	}
}

func motionCameraTestSolid(width, height int, r, g, b, a byte) []byte {
	pixels := make([]byte, width*height*4)
	for offset := 0; offset+3 < len(pixels); offset += 4 {
		pixels[offset], pixels[offset+1] = r, g
		pixels[offset+2], pixels[offset+3] = b, a
	}
	return pixels
}

// motionCameraTestNode builds a nodeType=5 layer. An inactive camera carries a
// frame without content, which is how the reference evaluator marks a node
// invisible (PlayerInternal.h:1455-1484, parseFrame content==nullptr).
func motionCameraTestNode(coords []int64, active bool) assets.Value {
	frame := map[string]assets.Value{"time": float64(0)}
	if active {
		coord := make([]assets.Value, len(coords))
		for index, value := range coords {
			coord[index] = value
		}
		frame["content"] = map[string]assets.Value{"coord": coord}
	}
	return map[string]assets.Value{
		"label":     "camera",
		"type":      int64(5),
		"frameList": []assets.Value{frame},
	}
}

// motionCameraTestDraw runs the real EmotePlayer CPU draw path: render the
// canvas, resolve the camera offset, fold it into the draw matrix, and
// rasterize into the target layer (drawMotionTarget in
// motionplayer_runtime.go).
func motionCameraTestDraw(t *testing.T, resource *motionResource, drawMatrix motionDrawMatrix) *Layer {
	t.Helper()
	services := DefaultServices()
	layer := NewLayer(1)
	layer.ImageWidth, layer.ImageHeight = 16, 16
	layer.RGBA = make([]byte, 16*16*4)
	services.Layers[tjs.ObjectID(1)] = layer
	state := &motionPlayerState{
		resource:   resource,
		chara:      "chara",
		motion:     "idle",
		drawMatrix: drawMatrix,
		speed:      1,
	}
	value := tjs.ObjectValue(tjs.NewObjectRef(tjs.ObjectID(1)))
	if err := services.drawMotionTarget(nil, state, value); err != nil {
		t.Fatalf("draw %s: %v", resource.path, err)
	}
	return layer
}

// motionCameraTestDrawAdopt runs the draw path with a freshly constructed Layer
// that has no canvas yet, so drawMotionTarget adopts the motion canvas instead
// of compositing through a draw matrix.
func motionCameraTestDrawAdopt(t *testing.T, resource *motionResource, drawMatrix motionDrawMatrix) *Layer {
	t.Helper()
	services := DefaultServices()
	layer := NewLayer(1)
	services.Layers[tjs.ObjectID(1)] = layer
	state := &motionPlayerState{
		resource:   resource,
		chara:      "chara",
		motion:     "idle",
		drawMatrix: drawMatrix,
		speed:      1,
	}
	value := tjs.ObjectValue(tjs.NewObjectRef(tjs.ObjectID(1)))
	if err := services.drawMotionTarget(nil, state, value); err != nil {
		t.Fatalf("draw %s: %v", resource.path, err)
	}
	return layer
}

// motionCameraTestBounds returns the inclusive-exclusive alpha bounds of a
// layer bitmap.
func motionCameraTestBounds(layer *Layer) (left, top, right, bottom int) {
	width, height := int(layer.ImageWidth), int(layer.ImageHeight)
	left, top = width, height
	for y := range height {
		for x := range width {
			if layer.RGBA[(y*width+x)*4+3] == 0 {
				continue
			}
			left, top = min(left, x), min(top, y)
			right, bottom = max(right, x+1), max(bottom, y+1)
		}
	}
	return left, top, right, bottom
}

// TestEmoteDrawCameraNodeOffsetsVisiblePixels pins the user-visible effect of
// an active camera node through EmotePlayer.draw: the reference offsets every
// prepared render item by -(camera - root) transformed through the draw
// affine, including posZ through zFactor (PlayerUpdateGeometry.cpp:800-812,
// PlayerRenderItems.cpp:1174-1215).
func TestEmoteDrawCameraNodeOffsetsVisiblePixels(t *testing.T) {
	baseline := motionCameraTestDraw(t,
		motionCameraTestResource("camera-none.psb", nil), identityMotionDrawMatrix())
	shifted := motionCameraTestDraw(t,
		motionCameraTestResource("camera-shift.psb",
			[]assets.Value{motionCameraTestNode([]int64{3, 2, 1}, true)}),
		identityMotionDrawMatrix())

	baseLeft, baseTop, baseRight, baseBottom := motionCameraTestBounds(baseline)
	left, top, right, bottom := motionCameraTestBounds(shifted)
	if baseRight-baseLeft != 4 || baseBottom-baseTop != 4 {
		t.Fatalf("baseline block bounds = (%d,%d)-(%d,%d), want a 4x4 block",
			baseLeft, baseTop, baseRight, baseBottom)
	}
	if left != baseLeft-2 || right != baseRight-2 {
		t.Fatalf("camera x offset moved block to %d..%d, want %d..%d",
			left, right, baseLeft-2, baseRight-2)
	}
	if top != baseTop-2 || bottom != baseBottom-2 {
		t.Fatalf("camera y/z offset moved block to %d..%d, want %d..%d",
			top, bottom, baseTop-2, baseBottom-2)
	}
}

// TestEmoteDrawInactiveCameraKeepsPixels verifies that a type-5 node without
// active content leaves the scene untouched, preserving the no-camera output.
func TestEmoteDrawInactiveCameraKeepsPixels(t *testing.T) {
	baseline := motionCameraTestDraw(t,
		motionCameraTestResource("camera-none.psb", nil), identityMotionDrawMatrix())
	inactive := motionCameraTestDraw(t,
		motionCameraTestResource("camera-inactive.psb",
			[]assets.Value{motionCameraTestNode([]int64{6, 6, 0}, false)}),
		identityMotionDrawMatrix())

	if !bytes.Equal(inactive.RGBA, baseline.RGBA) {
		t.Fatal("inactive camera node changed rendered pixels")
	}
}

// TestEmoteDrawCameraNodeShiftsAdoptedCanvas covers the fallback branch used by
// a target Layer without a bitmap (`new Layer()` in script): EmotePlayer.draw
// adopts the motion canvas, so the camera offset is applied as an integer
// canvas translation.
func TestEmoteDrawCameraNodeShiftsAdoptedCanvas(t *testing.T) {
	baseline := motionCameraTestDrawAdopt(t,
		motionCameraTestResource("camera-adopt-none.psb", nil), identityMotionDrawMatrix())
	shifted := motionCameraTestDrawAdopt(t,
		motionCameraTestResource("camera-adopt-shift.psb",
			[]assets.Value{motionCameraTestNode([]int64{3, 2, 1}, true)}),
		identityMotionDrawMatrix())

	baseLeft, baseTop, baseRight, baseBottom := motionCameraTestBounds(baseline)
	left, top, right, bottom := motionCameraTestBounds(shifted)
	if left != baseLeft-2 || right != baseRight-2 ||
		top != baseTop-2 || bottom != baseBottom-2 {
		t.Fatalf("adopted canvas camera bounds = (%d,%d)-(%d,%d), want (%d,%d)-(%d,%d)",
			left, top, right, bottom,
			baseLeft-2, baseTop-2, baseRight-2, baseBottom-2)
	}
}

// TestMotionCameraFirstActiveNodeAndRounding pins the first-active selection
// and the reference's truncating +0.5 rounding: with a=2 and camera x=-3 the
// raw offset is -5.5, which static_cast<int> truncates to -5 (math.Round would
// produce -6), while y=-1 with d=1 gives -0.5 and therefore 0
// (PlayerUpdateGeometry.cpp:809-812).
func TestMotionCameraFirstActiveNodeAndRounding(t *testing.T) {
	resource := motionCameraTestResource("camera-rounding.psb", []assets.Value{
		motionCameraTestNode([]int64{6, 6, 0}, false),
		motionCameraTestNode([]int64{3, 1, 0}, true),
		motionCameraTestNode([]int64{0, 0, 0}, true),
	})
	base := motionDrawMatrix{a: 2, d: 1}
	adjusted := motionCameraDrawMatrix(resource, "chara", "idle", 0, nil, false, 0, 0, nil, base)
	if adjusted.tx != -5 {
		t.Fatalf("camera x offset = %v, want truncating static_cast<int>(-5.5) = -5", adjusted.tx)
	}
	if adjusted.ty != 0 {
		t.Fatalf("camera y offset = %v, want truncating static_cast<int>(-0.5) = 0", adjusted.ty)
	}

	// The rounded offset must reach rasterized pixels in the Emote draw path.
	baseline := motionCameraTestDraw(t,
		motionCameraTestResource("camera-rounding-base.psb", nil), base)
	shifted := motionCameraTestDraw(t, resource, base)
	baseLeft, baseTop, baseRight, baseBottom := motionCameraTestBounds(baseline)
	left, top, right, bottom := motionCameraTestBounds(shifted)
	if left != baseLeft-5 || right != baseRight-5 {
		t.Fatalf("rounded camera x moved block to %d..%d, want %d..%d",
			left, right, baseLeft-5, baseRight-5)
	}
	if top != baseTop || bottom != baseBottom {
		t.Fatalf("truncated camera y moved block to %d..%d, want %d..%d",
			top, bottom, baseTop, baseBottom)
	}
}

// TestMotionCameraNestedNodeUsesAccumulatedWorldPosition verifies the
// root-relative accumulated position: the camera's parent translation composes
// with the camera's own coord exactly as PlayerUpdateLayerEval.cpp:899-930
// composes accumulated positions.
func TestMotionCameraNestedNodeUsesAccumulatedWorldPosition(t *testing.T) {
	nestedCamera := map[string]assets.Value{
		"label": "camera-parent",
		"type":  int64(0),
		"frameList": []assets.Value{
			map[string]assets.Value{
				"time":    float64(0),
				"content": map[string]assets.Value{"coord": []assets.Value{int64(2), int64(1)}},
			},
		},
		"children": []assets.Value{motionCameraTestNode([]int64{1, 1, 0}, true)},
	}
	resource := motionCameraTestResource("camera-nested.psb", []assets.Value{nestedCamera})
	adjusted := motionCameraDrawMatrix(resource, "chara", "idle", 0, nil, false, 0, 0, nil, identityMotionDrawMatrix())
	if adjusted.tx != -2 || adjusted.ty != -1 {
		t.Fatalf("nested camera offset = (%v,%v), want accumulated world (-2,-1)",
			adjusted.tx, adjusted.ty)
	}
}

// TestGPUCameraNodeOffsetShiftsDrawTranslation covers the deferred-render
// branch EmotePlayer.draw takes when the target layer is captured by the GPU:
// the camera translation reaches RenderMotionDraw translations, not only the
// baked CPU canvas.
func TestGPUCameraNodeOffsetShiftsDrawTranslation(t *testing.T) {
	baseResource := motionCameraTestResource("gpu-camera-none.psb", nil)
	cameraResource := motionCameraTestResource("gpu-camera-shift.psb",
		[]assets.Value{motionCameraTestNode([]int64{3, 2, 1}, true)})

	base, err := buildGPUMotionVariables(baseResource, "chara", "idle", 0, 0, identityMotionDrawMatrix(), 1, nil, false, 0, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	shifted, err := buildGPUMotionVariables(cameraResource, "chara", "idle", 0, 0, identityMotionDrawMatrix(), 1, nil, false, 0, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(base.Draws) != 1 || len(shifted.Draws) != 1 {
		t.Fatalf("draws = %d and %d, want one block each", len(base.Draws), len(shifted.Draws))
	}
	if shifted.Draws[0].TX != base.Draws[0].TX-2 || shifted.Draws[0].TY != base.Draws[0].TY-2 {
		t.Fatalf("GPU translation = (%v,%v), want (%v,%v)",
			shifted.Draws[0].TX, shifted.Draws[0].TY,
			base.Draws[0].TX-2, base.Draws[0].TY-2)
	}
}
