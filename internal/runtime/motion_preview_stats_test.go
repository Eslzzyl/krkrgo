package runtime

import (
	"testing"

	"github.com/eslzzyl/krkrgo/internal/assets"
)

// TestMotionPreviewKeepsOnlyPriorDrawItems pins Player.preview: item
// preparation drops nodes without an emoteEdit priorDraw flag and never
// expands child players or particles (PlayerRenderItems.cpp:507-511,558-560;
// PlayerRenderExecute.cpp:908-910).
func TestMotionPreviewKeepsOnlyPriorDrawItems(t *testing.T) {
	render := func(preview bool, priorDraw bool) []byte {
		resource := emotePlayerTestResource("preview.psb")
		sprite := motionPreviewSprite()
		if priorDraw {
			sprite["emoteEdit"] = map[string]assets.Value{"priorDraw": int64(1)}
		}
		motionTestIdle(resource)["layer"] = []assets.Value{sprite}
		frame, err := renderPSBMotionOptionsZFactorInherit(resource, "chara", "idle", 0, 0, nil, false, 0, 0, 1, nil, false, motionRenderOptions{preview: preview})
		if err != nil {
			t.Fatal(err)
		}
		return frame.RGBA
	}
	opaque := func(pixels []byte) int {
		count := 0
		for offset := 3; offset < len(pixels); offset += 4 {
			if pixels[offset] != 0 {
				count++
			}
		}
		return count
	}
	if got := opaque(render(false, false)); got == 0 {
		t.Fatal("non-preview render drew no pixels")
	}
	if got := opaque(render(true, false)); got != 0 {
		t.Fatalf("preview without priorDraw drew %d pixels, want none", got)
	}
	if got := opaque(render(true, true)); got == 0 {
		t.Fatal("preview kept no priorDraw pixels")
	}
}

func motionPreviewSprite() map[string]assets.Value {
	return map[string]assets.Value{
		"type": int64(0),
		"frameList": []assets.Value{map[string]assets.Value{
			"time": float64(0),
			"content": map[string]assets.Value{
				"src":   "src/chara/mouth0",
				"coord": []assets.Value{float64(0), float64(0)},
			},
		}},
	}
}

// TestMotionProcessedMeshVerticesCountsAncestorCascades pins
// Player.processedMeshVerticesNum: every mesh-enabled ancestor maps this node's
// control points plus its origin (PlayerUpdateGeometry.cpp:518-523).
func TestMotionProcessedMeshVerticesCountsAncestorCascades(t *testing.T) {
	single := motionMeshDeformResource("mesh-count.psb", 1, motionMeshScaledControlNet(0.75))
	stats := &motionRenderStats{}
	if _, err := renderPSBMotionOptionsZFactorInherit(single, "chara", "idle", 0, 0, nil, false, 0, 0, 1, nil, false, motionRenderOptions{stats: stats}); err != nil {
		t.Fatal(err)
	}
	if stats.meshVertices != motionMeshControlNetPoints+1 {
		t.Fatalf("one ancestor counted %d vertices, want %d", stats.meshVertices, motionMeshControlNetPoints+1)
	}

	chained := motionMeshDeformResource("mesh-count-chain.psb", 1, motionMeshScaledControlNet(0.75))
	motion := motionTestIdle(chained)
	parent := motion["layer"].([]assets.Value)[0].(map[string]assets.Value)
	parentFrame := parent["frameList"].([]assets.Value)[0].(map[string]assets.Value)
	parentContent := parentFrame["content"].(map[string]assets.Value)
	parentContent["coord"] = []assets.Value{int64(0), int64(0)}
	grandparent := map[string]assets.Value{
		"label":             "grandparent",
		"type":              int64(0),
		"meshTransform":     int64(1),
		"meshSyncChildMask": int64(1),
		"meshDivision":      int64(4),
		"frameList": []assets.Value{
			motionMeshDeformFrame("src/chara/empty", 16, 16, -8, -8, motionMeshScaledControlNet(0.75)),
		},
		"children": []assets.Value{parent},
	}
	motion["layer"] = []assets.Value{grandparent}
	chainedStats := &motionRenderStats{}
	if _, err := renderPSBMotionOptionsZFactorInherit(chained, "chara", "idle", 0, 0, nil, false, 0, 0, 1, nil, false, motionRenderOptions{stats: chainedStats}); err != nil {
		t.Fatal(err)
	}
	// The parent mesh node cascades through the grandparent (17) and the child
	// cascades through both (34).
	if want := 3 * (motionMeshControlNetPoints + 1); chainedStats.meshVertices != want {
		t.Fatalf("chained meshes counted %d vertices, want %d", chainedStats.meshVertices, want)
	}
}
