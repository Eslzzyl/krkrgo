package runtime

import (
	"math"
	"path/filepath"
	goruntime "runtime"
	"testing"

	"github.com/eslzzyl/krkrgo/internal/assets"
	"github.com/eslzzyl/krkrgo/internal/core"
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

func TestLoadMotionResourceAcceptsRJTitleBackground(t *testing.T) {
	_, source, _, ok := goruntime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	project := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	storage, err := assets.OpenStorage(project, core.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	services := NewServices(storage, t.TempDir(), core.DefaultLimits())
	resource, err := services.loadMotionResource("startup.tjs/motion/other/title_bg.mtn")
	if err != nil {
		t.Fatalf("load RJ title background motion: %v", err)
	}
	if resource == nil || resource.doc == nil {
		t.Fatal("RJ title background motion returned no decoded document")
	}
	frame, err := renderPSBMotion(resource, "title_bg", "normal", 0, 0)
	if err != nil {
		t.Fatalf("render RJ default title frame: %v", err)
	}
	if frame == nil || frame.Width <= 0 || frame.Height <= 0 {
		t.Fatalf("render RJ default title frame returned invalid image: %#v", frame)
	}
}

func TestMotionPlayerLinkExtendsExistingMotionNamespace(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()

	if _, err := session.executeSource("<motionplayer-existing-namespace-test>", `
		class Motion { class ResourceManager {} }
		Plugins.link("motionplayer.dll");
		resourceManager = new Motion.ResourceManager(null, 1024);
		layerID = resourceManager.requireLayerId();
		resourceManager.releaseLayerId(layerID);
	`, false); err != nil {
		t.Fatalf("extend existing Motion namespace: %v", err)
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

func TestMotionPlayerPassReleasesSyncWait(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()

	if _, err := session.executeSource("<motionplayer-pass-test>", `
		Plugins.link("plugin/MotionPlayer.TPM");
		player = new Motion.Player(null);
		player.syncWaiting = true;
		player.syncActive = true;
		player.pass();
		waiting = player.syncWaiting;
		active = player.syncActive;
	`, false); err != nil {
		t.Fatalf("release Motion.Player sync wait: %v", err)
	}
	value, err := session.Evaluate(`waiting === false && active === false`)
	if err != nil {
		t.Fatal(err)
	}
	if value.Text() != "1" {
		t.Fatalf("Motion.Player.pass left sync state active: %q", value.Text())
	}
}

func TestMotionPlayerSetColorUpdatesColorWeight(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()

	if _, err := session.executeSource("<motionplayer-set-color-test>", `
		Plugins.link("plugin/MotionPlayer.TPM");
		player = new Motion.Player(null);
		player.setColor(0x11223344);
		color = player.colorWeight;
	`, false); err != nil {
		t.Fatalf("set Motion.Player color: %v", err)
	}
	value, err := session.Evaluate(`color`)
	if err != nil {
		t.Fatal(err)
	}
	color, err := value.Int()
	if err != nil {
		t.Fatal(err)
	}
	if color != 0x11223344 {
		t.Fatalf("Motion.Player.setColor returned %#x, want %#x", color, int64(0x11223344))
	}
}
func TestMotionPlayerD3DAdaptorRendersHeadlessCPUFrame(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()
	services := session.Services

	if _, err := session.executeSource("<motionplayer-headless-adaptor-setup>", `
		Plugins.link("emoteplayer.dll");
		Plugins.link("layerExDraw.dll");
		rm = new Motion.ResourceManager(null, 1024);
		player = new Motion.EmotePlayer(rm);
		clearPlayer = new Motion.Player(rm);
		headlessWindow = %[];
		adaptor = new Motion.D3DAdaptor(headlessWindow, 8, 8, 4, 4);
		adaptor.setClearColor(0xFF010203);
		clearPlayer.clear(adaptor);
		clearLayer = new Layer();
		adaptor.captureCanvas(clearLayer);
		clearPixel = clearLayer.getMainPixel(7, 7);
	`, false); err != nil {
		t.Fatalf("construct headless motion adaptor: %v", err)
	}
	manager, err := session.Evaluate(`rm`)
	if err != nil {
		t.Fatal(err)
	}
	services.setMotionResource(manager.ObjectRef().Object, emotePlayerTestResourceForLayer("headless.psb", "mouth"))

	if _, err := session.executeSource("<motionplayer-headless-adaptor-draw>", `
		player.motionKey = "headless.psb";
		player.play("idle");
		player.progress(0);
		player.draw(adaptor);
		out = new Layer();
		adaptor.captureCanvas(out);
	`, false); err != nil {
		t.Fatalf("draw motion through headless adaptor: %v", err)
	}
	value, err := session.Evaluate(`[clearPixel, out.getMainPixel(1, 1), out.getMaskPixel(1, 1)].join(",")`)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := value.Text(), "66051,16711680,255"; got != want {
		t.Fatalf("headless adaptor capture = %q, want %q", got, want)
	}
}
func TestMotionPlayerInvalidateRemovesNativeState(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()
	if _, err := session.executeSource("<motionplayer-invalidate-test>", `
		Plugins.link("plugin/MotionPlayer.TPM");
		player = new Motion.Player(null);
	`, false); err != nil {
		t.Fatalf("construct motion player: %v", err)
	}
	value, err := session.Evaluate(`player`)
	if err != nil {
		t.Fatal(err)
	}
	id := value.ObjectRef().Object
	if _, err := session.VM.Invalidate(value, session.Services, &session.Budget); err != nil {
		t.Fatalf("invalidate motion player: %v", err)
	}
	session.Services.mu.RLock()
	_, found := session.Services.MotionPlayers[id]
	session.Services.mu.RUnlock()
	if found {
		t.Fatalf("native Motion.Player state %d survived invalidation", id)
	}
}
