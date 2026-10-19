package runtime

import (
	"bytes"
	"testing"

	"github.com/eslzzyl/krkrgo/internal/assets"
)

// motionMeshDeformFrame builds a single-frame layer content payload. bp is
// the PSB mesh.bp control net (16 normalized x/y pairs, u varying fastest).
func motionMeshDeformFrame(source string, width, height, coordX, coordY int64, bp []assets.Value) assets.Value {
	content := map[string]assets.Value{
		"src":    source,
		"width":  width,
		"height": height,
		"coord":  []assets.Value{coordX, coordY},
	}
	if bp != nil {
		content["mesh"] = map[string]assets.Value{"bp": bp}
	}
	return map[string]assets.Value{
		"time":    float64(0),
		"content": content,
	}
}

// motionMeshDeformResource nests a 4x4 red mesh surface under a transparent
// 16x16 mesh surface. The canvas is 16x16 and the parent surface spans the
// whole canvas, so the child renders at world x/y 12..16 unless an ancestor
// control net moves it.
func motionMeshDeformResource(path string, syncMask int64, bp []assets.Value) *motionResource {
	icon := func(width, height int64) assets.Value {
		return map[string]assets.Value{
			"width":   width,
			"height":  height,
			"originX": int64(0),
			"originY": int64(0),
		}
	}
	child := map[string]assets.Value{
		"label":         "child",
		"type":          int64(0),
		"meshTransform": int64(1),
		"meshDivision":  int64(4),
		"frameList":     []assets.Value{
			motionMeshDeformFrame("src/chara/block", 4, 4, 12, 12, nil),
		},
	}
	parent := map[string]assets.Value{
		"label":             "parent",
		"type":              int64(0),
		"meshTransform":     int64(1),
		"meshSyncChildMask": syncMask,
		"meshDivision":      int64(4),
		"frameList":         []assets.Value{
			motionMeshDeformFrame("src/chara/empty", 16, 16, -8, -8, bp),
		},
		"children":           []assets.Value{child},
	}
	return &motionResource{
		path: path,
		doc:  &assets.Document{Root: map[string]assets.Value{
			"screenSize": map[string]assets.Value{"width": int64(16), "height": int64(16)},
			"source": map[string]assets.Value{
				"chara": map[string]assets.Value{
					"icon": map[string]assets.Value{
						"empty": icon(16, 16),
						"block": icon(4, 4),
					},
				},
			},
			"object": map[string]assets.Value{
				"chara": map[string]assets.Value{
					"motion": map[string]assets.Value{
						"idle": map[string]assets.Value{
							"width":  int64(16),
							"height": int64(16),
							"layer":  []assets.Value{parent},
						},
					},
				},
			},
		}},
		images: map[string]*assets.Image{
			"chara/empty": {Width: 16, Height: 16, RGBA: make([]byte, 16*16*4)},
			"chara/block": {Width: 4, Height: 4, RGBA: emoteTestSolid(4, 4, 255, 0, 0, 255)},
		},
	}
}

// motionMeshScaledControlNet returns a unit 4x4 Bezier net whose columns and
// rows are scaled about the top-left corner. Its surface maps normalized
// (u,v) to (scale*u, scale*v).
func motionMeshScaledControlNet(scale float64) []assets.Value {
	bp := make([]assets.Value, 0, 32)
	for row := range 4 {
		for column := range 4 {
			bp = append(bp, float64(column)*scale/3, float64(row)*scale/3)
		}
	}
	return bp
}

func motionMeshAlphaBounds(image *assets.Image) (left, top, right, bottom, count int) {
	width, height := int(image.Width), int(image.Height)
	left, top = width, height
	for y := range height {
		for x := range width {
			if image.RGBA[(y*width+x)*4+3] == 0 {
				continue
			}
			left, top = min(left, x), min(top, y)
			right, bottom = max(right, x+1), max(bottom, y+1)
			count++
		}
	}
	if count == 0 {
		return 0, 0, 0, 0, 0
	}
	return left, top, right, bottom, count
}

func motionMeshAlphaAt(image *assets.Image, x, y int) uint8 {
	return image.RGBA[(y*int(image.Width)+x)*4+3]
}

// TestMotionMeshAncestorControlNetDeformsChildSurface pins the observable
// effect of parent mesh deformation: the same child surface renders as a
// 4x4 block at (12,12) with mesh combination off and as a 3x3 block at (9,9)
// when the parent's mesh.bp control net scales normalized space to 0.75.
func TestMotionMeshAncestorControlNetDeformsChildSurface(t *testing.T) {
	baselineResource := motionMeshDeformResource("mesh-baseline.psb", 0, nil)
	deformedResource := motionMeshDeformResource("mesh-deformed.psb", 1, motionMeshScaledControlNet(0.75))

	baseline, err := renderPSBMotionOptions(baselineResource, "chara", "idle", 0, 0, nil, false, 0, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	deformed, err := renderPSBMotionOptions(deformedResource, "chara", "idle", 0, 0, nil, false, 0, 0, nil)
	if err != nil {
		t.Fatal(err)
	}

	l0, t0, r0, b0, n0 := motionMeshAlphaBounds(baseline)
	if l0 != 12 || t0 != 12 || r0 != 16 || b0 != 16 || n0 != 16 {
		t.Fatalf("undeformed child bounds = (%d,%d)-(%d,%d) count %d, want (12,12)-(16,16) count 16", l0, t0, r0, b0, n0)
	}
	if motionMeshAlphaAt(baseline, 10, 10) != 0 {
		t.Fatal("undeformed child unexpectedly covers (10,10)")
	}

	l1, t1, r1, b1, n1 := motionMeshAlphaBounds(deformed)
	if l1 != 9 || t1 != 9 || r1 != 12 || b1 != 12 || n1 != 9 {
		t.Fatalf("deformed child bounds = (%d,%d)-(%d,%d) count %d, want (9,9)-(12,12) count 9", l1, t1, r1, b1, n1)
	}
	if motionMeshAlphaAt(deformed, 10, 10) == 0 {
		t.Fatal("deformed child did not move to (10,10)")
	}
	if motionMeshAlphaAt(deformed, 13, 13) != 0 {
		t.Fatal("deformed child still covers its undeformed position (13,13)")
	}
}

// TestMotionMeshAncestorChainComposesControlNets verifies the cascade walks
// beyond the immediate parent: two 0.75 control nets compose to 0.5625, so
// the child surface that occupies world 12..16 moves to 6.75..9.
func TestMotionMeshAncestorChainComposesControlNets(t *testing.T) {
	resource := motionMeshDeformResource("mesh-chain.psb", 1, motionMeshScaledControlNet(0.75))
	root := resource.doc.Root.(map[string]assets.Value)
	objects := root["object"].(map[string]assets.Value)
	character := objects["chara"].(map[string]assets.Value)
	motions := character["motion"].(map[string]assets.Value)
	motion := motions["idle"].(map[string]assets.Value)
	parent := motion["layer"].([]assets.Value)[0].(map[string]assets.Value)
	parentFrame := parent["frameList"].([]assets.Value)[0].(map[string]assets.Value)
	parentContent := parentFrame["content"].(map[string]assets.Value)
	// The grandparent supplies the canvas-wide frame; the parent now sits at
	// its center instead of supplying that frame itself.
	parentContent["coord"] = []assets.Value{int64(0), int64(0)}
	grandparent := map[string]assets.Value{
		"label":             "grandparent",
		"type":              int64(0),
		"meshTransform":     int64(1),
		"meshSyncChildMask": int64(1),
		"meshDivision":      int64(4),
		"frameList":         []assets.Value{
			motionMeshDeformFrame("src/chara/empty", 16, 16, -8, -8, motionMeshScaledControlNet(0.75)),
		},
		"children":           []assets.Value{parent},
	}
	motion["layer"] = []assets.Value{grandparent}

	image, err := renderPSBMotionOptions(resource, "chara", "idle", 0, 0, nil, false, 0, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	l, top, r, b, count := motionMeshAlphaBounds(image)
	if l != 7 || top != 7 || r != 9 || b != 9 || count != 4 {
		t.Fatalf("chained child bounds = (%d,%d)-(%d,%d) count %d, want (7,7)-(9,9) count 4", l, top, r, b, count)
	}
	if motionMeshAlphaAt(image, 7, 7) == 0 {
		t.Fatal("chained child did not reach (7,7)")
	}
	if motionMeshAlphaAt(image, 6, 6) != 0 {
		t.Fatal("chained child overshot to (6,6)")
	}
	if motionMeshAlphaAt(image, 9, 9) != 0 {
		t.Fatal("chained child overshot to (9,9)")
	}
}

// TestMotionMeshUnitControlNetKeepsAffineOutput verifies a unit mesh.bp control
// net is treated as no deformation: output stays byte-identical to the
// existing affine mesh path.
func TestMotionMeshUnitControlNetKeepsAffineOutput(t *testing.T) {
	plainResource := motionMeshDeformResource("mesh-plain.psb", 1, nil)
	unitResource := motionMeshDeformResource("mesh-unit.psb", 1, motionMeshScaledControlNet(1))

	plain, err := renderPSBMotionOptions(plainResource, "chara", "idle", 0, 0, nil, false, 0, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	unit, err := renderPSBMotionOptions(unitResource, "chara", "idle", 0, 0, nil, false, 0, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(plain.RGBA, unit.RGBA) {
		t.Fatal("unit control net changed the affine mesh output")
	}
	l, top, r, b, count := motionMeshAlphaBounds(unit)
	if l != 12 || top != 12 || r != 16 || b != 16 || count != 16 {
		t.Fatalf("unit control net bounds = (%d,%d)-(%d,%d) count %d, want (12,12)-(16,16) count 16", l, top, r, b, count)
	}
}

// motionMeshHitShapeResource nests a small circle shape under a canvas-wide
// mesh parent. The parent control net scales normalized surface coordinates
// to 0.75 when syncMask enables child combination, so the probe's hit center
// moves from (8,8) to (6,6).
func motionMeshHitShapeResource(path string, syncMask int64, bp []assets.Value, shapeZoom float64) *motionResource {
	probe := map[string]assets.Value{
		"label":     "probe",
		"type":      int64(1),
		"shape":     int64(1),
		"frameList": []assets.Value{
			map[string]assets.Value{
				"time":    float64(0),
				"content": map[string]assets.Value{
					"coord": []assets.Value{int64(16), int64(16)},
					"zx":    shapeZoom,
					"zy":    shapeZoom,
				},
			},
		},
	}
	parent := map[string]assets.Value{
		"label":             "surface",
		"type":              int64(0),
		"meshTransform":     int64(1),
		"meshSyncChildMask": syncMask,
		"meshDivision":      int64(4),
		"frameList": []assets.Value{
			motionMeshDeformFrame("src/chara/empty", 16, 16, -8, -8, bp),
		},
		"children": []assets.Value{probe},
	}
	return &motionResource{
		path: path,
		doc:  &assets.Document{Root: map[string]assets.Value{
			"metadata":   map[string]assets.Value{"base": map[string]assets.Value{
				"chara":           "chara",
				"motion":          "idle",
				"timelineControl": []assets.Value{
					map[string]assets.Value{"label": "idle", "lastTime": int64(10)},
				},
			}},
			"screenSize": map[string]assets.Value{"width": int64(16), "height": int64(16)},
			"source":     map[string]assets.Value{
				"chara": map[string]assets.Value{
					"icon": map[string]assets.Value{
						"empty": map[string]assets.Value{
							"width": int64(16), "height": int64(16),
							"originX": int64(0), "originY": int64(0),
						},
					},
				},
			},
			"object":     map[string]assets.Value{
				"chara": map[string]assets.Value{
					"motion": map[string]assets.Value{
						"idle": map[string]assets.Value{
							"width": int64(16), "height": int64(16),
							"layer": []assets.Value{parent},
						},
					},
				},
			},
		}},
		images: map[string]*assets.Image{
			"chara/empty": {Width: 16, Height: 16, RGBA: make([]byte, 16*16*4)},
		},
	}
}

// TestEmotePlayerContainsAncestorMeshDeformation drives the EmotePlayer label
// form of contains() and pins the geometry effect of a combined ancestor mesh:
// without combination the circle probes its authored center (8,8), and with
// combination it probes the deformed center (6,6) while keeping its authored
// radius. The near-edge points distinguish coverage inside the moved circle
// from a miss outside it.
func TestEmotePlayerContainsAncestorMeshDeformation(t *testing.T) {
	for _, test := range []struct {
		name     string
		resource *motionResource
		want     string
	}{
		{
			name:     "baseline",
			resource: motionMeshHitShapeResource("mesh-hit-baseline.psb", 0, motionMeshScaledControlNet(0.75), 0.25),
			want:     "0|1|1|0|0|0|0|0",
		},
		{
			name:     "deformed",
			resource: motionMeshHitShapeResource("mesh-hit-deformed.psb", 1, motionMeshScaledControlNet(0.75), 0.25),
			want:     "1|0|1|1|1|0|0|0",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			session := NewSession(DefaultServices(), 200_000)
			defer session.Close()
			if _, err := session.executeSource("<mesh-hit-link>", `
				Plugins.link("emoteplayer.dll");
				Motion.ResourceManager.setEmotePSBDecryptSeed(742877301);
				rm = new Motion.ResourceManager(null, 1024);
				emote = new Motion.EmotePlayer(rm);
			`, false); err != nil {
				t.Fatalf("link emoteplayer.dll: %v", err)
			}
			manager, err := session.Evaluate(`rm`)
			if err != nil {
				t.Fatal(err)
			}
			session.Services.setMotionResource(manager.ObjectRef().Object, test.resource)

			source := `
				emote.motionKey = "` + test.resource.path + `";
				emote.play("idle");
				emote.progress(0);
				result = emote.contains("probe", 6, 6) + "|" +
					emote.contains("probe", 8, 8) + "|" +
					emote.contains("probe", 7, 7) + "|" +
					emote.contains("probe", 7.9, 6) + "|" +
					emote.contains("probe", 4, 6) + "|" +
					emote.contains("probe", 3.9, 6) + "|" +
					emote.contains("probe", 8.1, 6) + "|" +
					emote.contains("absent", 6, 6);
			`
			if _, err := session.executeSource("<mesh-hit-contains>", source, false); err != nil {
				t.Fatalf("drive contains: %v", err)
			}
			value, err := session.Evaluate(`result`)
			if err != nil {
				t.Fatal(err)
			}
			if got := value.Text(); got != test.want {
				t.Fatalf("contains deformed-center|authored-center|shared-inside|deformed-near-edge|deformed-edge|deformed-outside|outside|absent = %q, want %q", got, test.want)
			}
		})
	}
}

// TestMotionQuadContainsMatchesReferenceWinding pins HitTestInternal.h type 3:
// all four edges are inclusive, and the winding comes from the first three
// vertices so either vertex order behaves identically.
func TestMotionQuadContainsMatchesReferenceWinding(t *testing.T) {
	square := [8]float64{-1, -1, 1, -1, 1, 1, -1, 1}
	for _, check := range []struct {
		x, y float64
		want bool
	}{
		{0, 0, true},
		{-1, -1, true},
		{1, 1, true},
		{1, 0, true},
		{1.01, 0, false},
		{0, -1.01, false},
	} {
		if got := motionQuadContains(check.x, check.y, square); got != check.want {
			t.Fatalf("motionQuadContains(%v,%v) = %v, want %v", check.x, check.y, got, check.want)
		}
	}
	reversed := [8]float64{-1, 1, 1, 1, 1, -1, -1, -1}
	if !motionQuadContains(0, 0, reversed) || !motionQuadContains(1, 0, reversed) {
		t.Fatal("reversed quad winding changed containment")
	}
}
