package runtime

import "testing"

func TestMotionSeparateLayerDrawUsesCanvasCoordinates(t *testing.T) {
	draw := RenderMotionDraw{
		Width:  59,
		Height: 63,
		A:      1,
		D:      1,
		TX:     -243,
		TY:     15,
	}
	left, top, right, bottom, visible := motionSeparateLayerClip(draw, 960, 540)
	if !visible {
		t.Fatal("centered motion draw was clipped as invisible")
	}
	if left != 236 || top != 284 || right != 296 || bottom != 348 {
		t.Fatalf("motion clip = [%d,%d,%d,%d], want [236,284,296,348]", left, top, right, bottom)
	}
	local := motionSeparateLayerDraw(draw, left, top, 960, 540)
	if local.TX != 1 || local.TY != 1 {
		t.Fatalf("local motion position = (%g,%g), want (1,1)", local.TX, local.TY)
	}
}

func TestMotionSeparateLayerClipCentersViewport(t *testing.T) {
	draw := RenderMotionDraw{
		Width:       4,
		Height:      4,
		A:           1,
		D:           1,
		TX:          -4,
		TY:          -3,
		HasViewport: true,
		Viewport:    [4]float64{-2, -1, 3, 4},
	}
	left, top, right, bottom, visible := motionSeparateLayerClip(draw, 10, 8)
	if !visible {
		t.Fatal("viewport motion draw was clipped as invisible")
	}
	if left != 3 || top != 3 || right != 5 || bottom != 5 {
		t.Fatalf("motion viewport clip = [%d,%d,%d,%d], want [3,3,5,5]", left, top, right, bottom)
	}
}
