package runtime

import (
	"math"
	"testing"
)

func TestMotionPlayerLinkInstallsPortableResourceManagerSurface(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()

	if _, err := session.executeSource("<motionplayer-native-test>", `
		Plugins.link("plugin/MotionPlayer.TPM");
		rm = new Motion.ResourceManager(null, 1024);
		constants = [Motion.LayerTypeMotion, Motion.PlayFlagChain,
			Motion.ShapeTypeQuad, Motion.TransformOrderSlant,
			Motion.CoordinateRecutangularXZ, Motion.MaskModeAlpha].join(",");
		missingSource = rm.findSource("missing.psb") === void;
		layerID = rm.requireLayerId();
		rm.releaseLayerId(layerID);
		player = new Motion.Player(rm);
		playerLayerID = player.requireLayerId("layer");
		playerLayerIDAgain = player.requireLayerId("layer");
		rm.clearCache();
		rm.unload("missing.psb");
	`, false); err != nil {
		t.Fatalf("link and use Motion.ResourceManager: %v", err)
	}
	value, err := session.Evaluate(`constants + "|" + missingSource`)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := value.Text(), "3,2,3,3,1,1|1"; got != want {
		t.Fatalf("Motion constants and findSource = %q, want %q", got, want)
	}
	value, err = session.Evaluate(`playerLayerID === playerLayerIDAgain`)
	if err != nil {
		t.Fatal(err)
	}
	if value.Text() != "1" {
		t.Fatalf("named player layer id was not reused: %q", value.Text())
	}
}

func TestMotionPlayerPlayPreservesActiveMotionProgress(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()

	if _, err := session.executeSource("<motionplayer-play-test>", `
		Plugins.link("plugin/MotionPlayer.TPM");
		player = new Motion.Player(null);
		player.chara = "generic";
		player.play("motion");
		player.progress(17);
		player.play("motion");
		tick = player.tickCount;
	`, false); err != nil {
		t.Fatalf("play and progress motion player: %v", err)
	}
	value, err := session.Evaluate(`tick`)
	if err != nil {
		t.Fatal(err)
	}
	// tickCount is the millisecond conversion of the 60 fps frame clock, so the
	// round trip of progress(17) lands within floating point noise of 17.
	tick, err := value.Real()
	if err != nil {
		t.Fatalf("tickCount value: %v", err)
	}
	if math.Abs(tick-17) > 1e-9 {
		t.Fatalf("active motion replay reset tick to %v, want 17", tick)
	}
}
