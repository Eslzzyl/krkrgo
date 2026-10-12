package runtime

import (
	"strings"
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
	if _, err := session.Evaluate(`rm.requireLayerId("layer")`); err == nil || !strings.Contains(err.Error(), "unsupported Motion.ResourceManager operation") {
		t.Fatalf("unimplemented resource-manager method error = %v, want explicit unsupported error", err)
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
	if got, want := value.IntegerValue(), int64(17); got != want {
		t.Fatalf("active motion replay reset tick to %d, want %d", got, want)
	}
}
