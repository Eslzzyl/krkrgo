package runtime

import (
	"bytes"
	"testing"

	"github.com/eslzzyl/krkrgo/internal/assets"
	"github.com/eslzzyl/krkrgo/internal/tjs"
)

// emotePlayerTestResource builds a deterministic motion PSB document with real
// metadata: parameterized layers at distinct coordinates, each with two distinct
// 4x4 icon frames, a timelineControl table, and a variableList whose frames
// select each layer. The renderer resolves each parameter through the Emote
// variable table, so setVariable changes the corresponding pixels.
func emotePlayerTestResource(path string) *motionResource {
	icon := func() assets.Value {
		return map[string]assets.Value{
			"width":   int64(4),
			"height":  int64(4),
			"originX": int64(0),
			"originY": int64(0),
		}
	}
	frame := func(src string, time float64, x, y int64) assets.Value {
		return map[string]assets.Value{
			"time": time,
			"content": map[string]assets.Value{
				"src":   src,
				"coord": []assets.Value{x, y},
			},
		}
	}
	layer := func(label string, parameterize, x, y int64) assets.Value {
		return map[string]assets.Value{
			"label":        label,
			"parameterize": parameterize,
			"shape":        int64(2),
			"frameList": []assets.Value{
				frame("src/chara/mouth0", 0, x, y),
				frame("src/chara/mouth1", 1, x, y),
				frame("src/chara/mouth1", 2, x, y),
			},
		}
	}
	variable := func(label string) assets.Value {
		return map[string]assets.Value{
			"label": label,
			"frameList": []assets.Value{
				map[string]assets.Value{"label": "off", "f": float64(0)},
				map[string]assets.Value{"label": "on", "f": float64(1)},
			},
		}
	}
	parameter := func(id string) assets.Value {
		return map[string]assets.Value{
			"id": id, "rangeBegin": float64(0), "rangeEnd": float64(1),
			"division": float64(1),
		}
	}
	base := map[string]assets.Value{
		"chara":  "chara",
		"motion": "idle",
		"variableList": []assets.Value{
			variable("mouth"), variable("eye"), variable("free"), variable("bustx"),
		},
		"parameterList": []assets.Value{
			parameter("mouth"), parameter("eye"), parameter("free"), parameter("bustx"),
		},
		"eyeControl": []assets.Value{
			map[string]assets.Value{"enabled": true, "label": "eye"},
		},
		"mouthControl": []assets.Value{
			map[string]assets.Value{"enabled": true, "label": "mouth", "talkLabel": "talk"},
		},
		"bustControl": []assets.Value{
			map[string]assets.Value{"enabled": true, "var_lr": "bustx", "var_ud": "busty"},
		},
		"selectorControl": []assets.Value{
			map[string]assets.Value{
				"enabled": true, "label": "sel",
				"optionList": []assets.Value{
					map[string]assets.Value{"label": "eye", "offValue": float64(0), "onValue": float64(1)},
				},
			},
		},
		"timelineControl": []assets.Value{
			map[string]assets.Value{
				"label": "idle", "lastTime": int64(60),
				"loopBegin": int64(0), "loopEnd": int64(59),
				"variableList": []assets.Value{
					map[string]assets.Value{
						"label": "mouth",
						"frameList": []assets.Value{
							map[string]assets.Value{"time": float64(0), "type": int64(1), "content": map[string]assets.Value{"value": float64(0)}},
							map[string]assets.Value{"time": float64(1), "type": int64(1), "content": map[string]assets.Value{"value": float64(1)}},
							map[string]assets.Value{"time": float64(2), "type": int64(1), "content": map[string]assets.Value{"value": float64(0)}},
						},
					},
				},
			},
			map[string]assets.Value{"label": "blink", "diff": true, "lastTime": int64(30)},
		},
	}
	return &motionResource{
		path: path,
		doc: &assets.Document{Root: map[string]assets.Value{
			"metadata":   map[string]assets.Value{"base": base},
			"screenSize": map[string]assets.Value{"width": int64(8), "height": int64(8)},
			"source": map[string]assets.Value{
				"chara": map[string]assets.Value{
					"icon": map[string]assets.Value{
						"mouth0": icon(),
						"mouth1": icon(),
					},
				},
			},
			"object": map[string]assets.Value{
				"chara": map[string]assets.Value{
					"motion": map[string]assets.Value{
						"idle": map[string]assets.Value{
							"width": int64(8), "height": int64(8),
							"layer": []assets.Value{
								layer("mouth", 0, 0, 0),
								layer("eye", 1, 0, 0),
								layer("free", 2, 0, 0),
								layer("bust", 3, 0, 0),
							},
						},
					},
				},
			},
		}},
		images: map[string]*assets.Image{
			"chara/mouth0": {Width: 4, Height: 4, RGBA: emoteTestSolid(4, 4, 255, 0, 0, 255)},
			"chara/mouth1": {Width: 4, Height: 4, RGBA: emoteTestSolid(4, 4, 0, 0, 255, 255)},
		},
	}
}
func emotePlayerTestResourceForLayer(path, label string) *motionResource {
	resource := emotePlayerTestResource(path)
	root := resource.doc.Root.(map[string]assets.Value)
	objects := root["object"].(map[string]assets.Value)
	character := objects["chara"].(map[string]assets.Value)
	motions := character["motion"].(map[string]assets.Value)
	motion := motions["idle"].(map[string]assets.Value)
	layers := motion["layer"].([]assets.Value)
	for _, value := range layers {
		layer := value.(map[string]assets.Value)
		if psbLabel(layer) == label {
			motion["layer"] = []assets.Value{layer}
			return resource
		}
	}
	panic("missing synthetic motion layer: " + label)
}

func emoteTestSolid(width, height int, r, g, b, a byte) []byte {
	pixels := make([]byte, width*height*4)
	for i := 0; i+3 < len(pixels); i += 4 {
		pixels[i], pixels[i+1], pixels[i+2], pixels[i+3] = r, g, b, a
	}
	return pixels
}

func TestEmotePlayerLinkSeedAndConstruction(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()

	if _, err := session.executeSource("<emoteplayer-link-test>", `
		Plugins.link("emoteplayer.dll");
		Motion.ResourceManager.setEmotePSBDecryptSeed(742877301);
		seed = Motion.ResourceManager.getEmotePSBDecryptSeed();
		rm = new Motion.ResourceManager(null, 1024);
		emote = new Motion.EmotePlayer(rm);
		classRegistered = Motion.EmotePlayer !== void;
		emote.chara = "chara";
		emote.motion = "idle";
		emote.completionType = 3;
		emote.outline = 7;
		emote.visible = false;
		moduleDefault = emote.module === void;
	`, false); err != nil {
		t.Fatalf("link emoteplayer.dll and construct EmotePlayer: %v", err)
	}
	value, err := session.Evaluate(`seed + "|" + classRegistered + "|" + emote.chara + "|" + emote.motion + "|" + emote.completionType + "|" + emote.outline + "|" + emote.visible + "|" + moduleDefault`)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := value.Text(), "742877301|1|chara|idle|3|7|0|1"; got != want {
		t.Fatalf("EmotePlayer construction state = %q, want %q", got, want)
	}
}

// TestEmotePlayerMetadataDrawVariablesTimelines drives the real consumer path:
// PSB metadata supplies the timeline and variable tables, the variable selects
// the drawn layer frame, and the timeline/serialize/clone surface round-trips.
func TestEmotePlayerMetadataDrawVariablesTimelines(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()
	services := session.Services

	if _, err := session.executeSource("<emoteplayer-setup>", `
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
	services.setMotionResource(manager.ObjectRef().Object, emotePlayerTestResourceForLayer("synthetic.psb", "mouth"))

	if _, err := session.executeSource("<emoteplayer-consumer>", `
		emote.motionKey = "synthetic.psb";
		moduleLoaded = emote.module !== void;
		key = emote.motionKey;
		chara = emote.chara;
		started = emote.play("idle");
		emote.progress(0);

		mainLabel = emote.getMainTimelineLabelList()[0];
		diffLabel = emote.getDiffTimelineLabelList()[0];
		mainCount = emote.countMainTimelines();
		diffCount = emote.countDiffTimelines();

		varCount = emote.countVariables();
		varLabel = emote.getVariableLabelAt(0);
		varFrames = emote.countVariableFrameAt(0);
		frameLabel = emote.getVariableFrameLabelAt(0, 1);
		frameValue = emote.getVariableFrameValueAt(0, 1);
		frameListLength = emote.getVariableFrameList("mouth").length;

		emote.setVariable("mouth", 0);
		redLayer = new Layer();
		emote.draw(redLayer);
		mouthValue = emote.getVariable("mouth");
		emote.setVariable("mouth", 1);
		blueLayer = new Layer();
		emote.draw(blueLayer);

		emote.playTimeline("idle", 1);
		playing = emote.isTimelinePlaying("idle");
		playingCount = emote.countPlayingTimelines();
		playingLabel = emote.getPlayingTimelineLabelAt(0);
		playingFlags = emote.getPlayingTimelineFlagsAt(0);
		loop = emote.getLoopTimeline("idle");
		totalFrames = emote.getTimelineTotalFrameCount("idle");
		emote.progress(34);
		emote.setTimelineBlendRatio("idle", 0.5);
		blend = emote.getTimelineBlendRatio("idle");
		infoCount = emote.getPlayingTimelineInfoList().length;

		serialized = emote.serialize();
		cloned = emote.clone();
		cloneVariable = cloned.getVariable("mouth");
		clonePlaying = cloned.isTimelinePlaying("idle");
		cloneChara = cloned.chara;

		other = new Motion.EmotePlayer(rm);
		other.motionKey = "synthetic.psb";
		other.unserialize(serialized);
		restoredPlaying = other.isTimelinePlaying("idle");
		restoredVariable = other.getVariable("mouth");
		restoredChara = other.chara;

		emote.stopTimeline("idle");
		stopped = emote.isTimelinePlaying("idle");
		emote.setOuterForce(1, 2);
		outer = emote.getOuterForce();
		outerForce = outer.x + "|" + outer.y;

		emote.setCoord(0, 0);
		emote.setScale(1);
		hit = emote.contains("mouth", 1, 1);
		miss = emote.contains("absent", 1, 1);
		shapeMiss = emote.contains("mouth", 9, 9);
		shapeEdgeMiss = emote.contains("mouth", 8, 0);
		outside = emote.contains(20, 20);

		transA = new Layer();
		transA.setImageSize(8, 8);
		emote.draw(transA);
		emote.setCoord(4, 4);
		transB = new Layer();
		transB.setImageSize(8, 8);
		emote.draw(transB);
		hiddenTarget = new Layer();
		hiddenTarget.visible = false;
		emote.draw(hiddenTarget);
		hiddenTargetVisible = hiddenTarget.visible;

		emote.visible = false;
		hidden = new Layer();
		emote.draw(hidden);
		emote.visible = true;
	`, false); err != nil {
		t.Fatalf("drive EmotePlayer consumer path: %v", err)
	}

	for _, check := range []struct {
		expression string
		want       string
	}{
		{`key + "|" + moduleLoaded + "|" + chara + "|" + started`, "synthetic.psb|1|chara|1"},
		{`mainLabel + "|" + diffLabel + "|" + mainCount + "|" + diffCount`, "idle|blink|1|1"},
		{`varCount + "|" + varLabel + "|" + varFrames + "|" + frameLabel + "|" + frameValue + "|" + frameListLength`, "7|mouth|2|on|1|2"},
		{`playing + "|" + playingCount + "|" + playingLabel + "|" + playingFlags + "|" + loop + "|" + totalFrames`, "1|1|idle|1|0|60"},
		{`blend + "|" + infoCount + "|" + stopped`, "0.5|1|0"},
		{`cloneVariable + "|" + clonePlaying + "|" + cloneChara`, "+0.0|0|chara"},
		{`restoredPlaying + "|" + restoredVariable + "|" + restoredChara`, "1|1|chara"},
		{`outerForce`, "1|2"},
		{`hit + "|" + miss + "|" + shapeMiss + "|" + shapeEdgeMiss + "|" + outside`, "1|0|0|0|0"},
		{`hiddenTargetVisible`, "0"},
	} {
		value, err := session.Evaluate(check.expression)
		if err != nil {
			t.Fatalf("%s: %v", check.expression, err)
		}
		if got := value.Text(); got != check.want {
			t.Fatalf("%s = %q, want %q", check.expression, got, check.want)
		}
	}

	if got := emoteTestPixel(emoteTestLayer(t, session, `redLayer`)); got != [4]byte{255, 0, 0, 255} {
		t.Fatalf("variable 0 frame pixel = %v, want opaque red", got)
	}
	if got := emoteTestPixel(emoteTestLayer(t, session, `blueLayer`)); got != [4]byte{0, 0, 255, 255} {
		t.Fatalf("variable 1 frame pixel = %v, want opaque blue", got)
	}
	if got := emoteTestPixelAt(emoteTestLayer(t, session, `transB`), 4, 4); got != [4]byte{0, 0, 255, 255} {
		t.Fatalf("translated draw pixel = %v, want the blue frame at (4,4)", got)
	}
	if got := emoteTestPixelAt(emoteTestLayer(t, session, `transA`), 0, 0); got != [4]byte{0, 0, 255, 255} {
		t.Fatalf("origin draw pixel = %v, want the blue frame at (0,0)", got)
	}
	if got := emoteTestPixel(emoteTestLayer(t, session, `hidden`)); got[3] != 0 {
		t.Fatalf("hidden player drew %v, want no pixels", got)
	}
}

func emoteTestLayer(t *testing.T, session *Session, expression string) *Layer {
	t.Helper()
	value, err := session.Evaluate(expression)
	if err != nil {
		t.Fatal(err)
	}
	layer := session.Services.Layer(value.ObjectRef().Object)
	if layer == nil {
		t.Fatalf("%s is not a Layer", expression)
	}
	return layer
}

func emoteTestPixel(layer *Layer) [4]byte {
	if len(layer.RGBA) < 4 {
		return [4]byte{}
	}
	return [4]byte{layer.RGBA[0], layer.RGBA[1], layer.RGBA[2], layer.RGBA[3]}
}

func emoteTestPixelAt(layer *Layer, x, y int) [4]byte {
	width := int(layer.deviceWidth())
	offset := (y*width + x) * 4
	if width <= 0 || offset < 0 || offset+3 >= len(layer.RGBA) {
		return [4]byte{}
	}
	return [4]byte{layer.RGBA[offset], layer.RGBA[offset+1], layer.RGBA[offset+2], layer.RGBA[offset+3]}
}

// TestEmotePlayerVariableRoutingDrivePixels verifies controllerBindings.type
// routing on the real draw path: type 4 transition is inert on the emote
// progress path, transition 0 writes the eval value, type 8 updates its
// selector options, a label without a binding writes directly, and type 0 only
// marks state dirty.
func TestEmotePlayerVariableRoutingDrivePixels(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()
	services := session.Services
	if _, err := session.executeSource("<emoteplayer-routing>", `
		Plugins.link("emoteplayer.dll");
		rm = new Motion.ResourceManager(null, 1024);
		eyePlayer = new Motion.EmotePlayer(rm);
		freePlayer = new Motion.EmotePlayer(rm);
		bustPlayer = new Motion.EmotePlayer(rm);
	`, false); err != nil {
		t.Fatalf("link emoteplayer.dll: %v", err)
	}
	manager, err := session.Evaluate(`rm`)
	if err != nil {
		t.Fatal(err)
	}
	managerID := manager.ObjectRef().Object
	services.setMotionResource(managerID, emotePlayerTestResourceForLayer("eye.psb", "eye"))
	services.setMotionResource(managerID, emotePlayerTestResourceForLayer("free.psb", "free"))
	services.setMotionResource(managerID, emotePlayerTestResourceForLayer("bust.psb", "bust"))
	if _, err := session.executeSource("<emoteplayer-routing-consumer>", `
		eyePlayer.motionKey = "eye.psb";
		eyePlayer.play("idle");
		eyeDefault = new Layer(); eyePlayer.draw(eyeDefault);
		eyePlayer.setVariable("eye", 1, 6);
		eyeTransition = new Layer(); eyePlayer.draw(eyeTransition);
		eyePlayer.setVariable("eye", 1);
		eyeDirect = new Layer(); eyePlayer.draw(eyeDirect);
		eyePlayer.setVariable("eye", 0);
		eyePlayer.setVariable("sel", 0);
		selector = new Layer(); eyePlayer.draw(selector);
		freePlayer.motionKey = "free.psb";
		freePlayer.play("idle");
		freePlayer.setVariable("free", 1);
		free = new Layer(); freePlayer.draw(free);
		bustPlayer.motionKey = "bust.psb";
		bustPlayer.play("idle");
		bustPlayer.setVariable("bustx", 1);
		bust = new Layer(); bustPlayer.draw(bust);
	`, false); err != nil {
		t.Fatalf("drive variable routing: %v", err)
	}
	red := [4]byte{255, 0, 0, 255}
	blue := [4]byte{0, 0, 255, 255}
	if got := emoteTestPixelAt(emoteTestLayer(t, session, `eyeDefault`), 0, 0); got != red {
		t.Fatalf("default eye pixel = %v, want red", got)
	}
	if got := emoteTestPixelAt(emoteTestLayer(t, session, `eyeTransition`), 0, 0); got != red {
		t.Fatalf("transition eye pixel = %v, want red (animator bucket is not stepped on the emote path)", got)
	}
	if got := emoteTestPixelAt(emoteTestLayer(t, session, `eyeDirect`), 0, 0); got != blue {
		t.Fatalf("direct eye pixel = %v, want blue", got)
	}
	if got := emoteTestPixelAt(emoteTestLayer(t, session, `selector`), 0, 0); got != blue {
		t.Fatalf("selector eye pixel = %v, want blue", got)
	}
	if got := emoteTestPixelAt(emoteTestLayer(t, session, `free`), 0, 0); got != blue {
		t.Fatalf("unbound variable pixel = %v, want blue", got)
	}
	if got := emoteTestPixelAt(emoteTestLayer(t, session, `bust`), 0, 0); got != red {
		t.Fatalf("bust control pixel = %v, want red (type 0 is state-only)", got)
	}
}

// TestEmotePlayerTimelineControlCrossingChangesPixels verifies timeline control
// frames feed setVariableResolvedWeight and therefore the parameterized layer.
func TestEmotePlayerTimelineControlCrossingChangesPixels(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()
	services := session.Services
	if _, err := session.executeSource("<emoteplayer-timeline>", `
		Plugins.link("emoteplayer.dll");
		rm = new Motion.ResourceManager(null, 1024);
		emote = new Motion.EmotePlayer(rm);
	`, false); err != nil {
		t.Fatalf("link emoteplayer.dll: %v", err)
	}
	manager, err := session.Evaluate(`rm`)
	if err != nil {
		t.Fatal(err)
	}
	services.setMotionResource(manager.ObjectRef().Object, emotePlayerTestResourceForLayer("synthetic.psb", "mouth"))
	if _, err := session.executeSource("<emoteplayer-timeline-consumer>", `
		emote.motionKey = "synthetic.psb";
		emote.play("idle");
		before = new Layer(); emote.draw(before);
		emote.progress(25);
		after = new Layer(); emote.draw(after);
		mouth = emote.getVariable("mouth");
	`, false); err != nil {
		t.Fatalf("drive timeline crossing: %v", err)
	}
	if got := emoteTestPixelAt(emoteTestLayer(t, session, `before`), 0, 0); got != [4]byte{255, 0, 0, 255} {
		t.Fatalf("pre-crossing mouth pixel = %v, want red", got)
	}
	if got := emoteTestPixelAt(emoteTestLayer(t, session, `after`), 0, 0); got != [4]byte{0, 0, 255, 255} {
		t.Fatalf("post-crossing mouth pixel = %v, want blue", got)
	}
	value, err := session.Evaluate(`mouth`)
	if err != nil {
		t.Fatal(err)
	}
	if value.Text() != "1" {
		t.Fatalf("mouth after crossing = %s, want 1", value.Text())
	}
}

// TestMotionResourceManagerMultiCache verifies the reference manager keeps
// every loaded module keyed by path alias, unload drops one module, and
// clearCache drops the store.
func TestMotionResourceManagerMultiCache(t *testing.T) {
	services := DefaultServices()
	manager := tjs.ObjectID(424242)
	first := emotePlayerTestResource("a.psb")
	second := emotePlayerTestResource("b.psb")
	services.setMotionResource(manager, first)
	firstModuleID := tjs.ObjectID(424243)
	secondModuleID := tjs.ObjectID(424244)
	services.MotionModules[firstModuleID] = first
	services.MotionModules[secondModuleID] = second
	services.rememberMotionResourceCachePath(manager, first, "lzfs://./a.psb")
	services.rememberMotionResourceCachePath(manager, second, "b-alias.psb")
	services.setMotionResource(manager, second)
	if got := services.motionResource("a.psb", manager); got != first {
		t.Fatalf("a.psb lookup = %p, want %p", got, first)
	}
	if got := services.motionResource("lzfs://./b.psb", manager); got != second {
		t.Fatalf("alias lookup = %p, want %p", got, second)
	}
	if got := len(services.uniqueMotionResources(manager)); got != 2 {
		t.Fatalf("unique modules = %d, want 2", got)
	}
	if !services.unloadMotionResource(manager, "a.psb") {
		t.Fatal("unload a.psb reported no module")
	}
	if got := services.motionResource("a.psb", manager); got != nil {
		t.Fatalf("a.psb still cached after unload: %p", got)
	}
	if _, ok := services.MotionModules[firstModuleID]; ok {
		t.Fatal("unloaded module snapshot remains registered")
	}
	if paths := services.motionResourceCachePaths(manager, first); len(paths) != 0 {
		t.Fatalf("unloaded module cache paths remain: %v", paths)
	}
	if got := services.motionResource("b.psb", manager); got != second {
		t.Fatalf("b.psb lookup after unload = %p, want %p", got, second)
	}
	services.clearMotionResourceCache(manager)
	if got := services.motionResource("b.psb", manager); got != nil {
		t.Fatalf("b.psb still cached after clearCache: %p", got)
	}
	if _, ok := services.MotionModules[secondModuleID]; ok {
		t.Fatal("cleared module snapshot remains registered")
	}
	if paths := services.motionResourceCachePaths(manager, second); len(paths) != 0 {
		t.Fatalf("cleared module cache paths remain: %v", paths)
	}
}

func TestMotionRenderVisibilityUsesMotionMode(t *testing.T) {
	resource := emotePlayerTestResource("completion.psb")
	root := resource.doc.Root.(map[string]assets.Value)
	objects := root["object"].(map[string]assets.Value)
	character := objects["chara"].(map[string]assets.Value)
	motions := character["motion"].(map[string]assets.Value)
	motion := motions["idle"].(map[string]assets.Value)
	motion["layer"] = []assets.Value{
		map[string]assets.Value{
			"type": int64(3),
			"frameList": []assets.Value{
				map[string]assets.Value{
					"time": int64(0),
					"content": map[string]assets.Value{
						"src":   "src/chara/mouth0",
						"coord": []assets.Value{int64(0), int64(0)},
					},
				},
			},
		},
	}
	hidden, err := renderPSBMotionOptions(resource, "chara", "idle", 0, 0, nil, false, 0, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	hiddenPixels := append([]byte(nil), hidden.RGBA...)
	for _, channel := range hiddenPixels {
		if channel != 0 {
			t.Fatal("normal Motion.Player rendered a type-3 node")
		}
	}
	visible, err := renderPSBMotionOptionsZFactorInherit(
		resource, "chara", "idle", 0, 0, nil, false, 0, 0, 1, nil, false,
		motionRenderOptions{emoteMode: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(hiddenPixels, visible.RGBA) {
		t.Fatal("emote-like visibility did not render the type-3 node")
	}
	if keyA, keyB := motionBakedSourceKey(resource, "chara", "idle", 0, 0, nil, false, 0, 0, nil),
		motionBakedSourceKey(resource, "chara", "idle", 0, 0, nil, false, 0, 1, nil); keyA == keyB {
		t.Fatal("GPU baked source key omits completionType")
	}
}

func TestEmotePlayerProgressUsesFrameTimeForPlainMotion(t *testing.T) {
	services := DefaultServices()
	resource := emotePlayerTestResource("plain-motion.psb")
	root := resource.doc.Root.(map[string]assets.Value)
	metadata := root["metadata"].(map[string]assets.Value)
	base := metadata["base"].(map[string]assets.Value)
	for _, key := range []string{
		"variableList", "parameterList", "eyeControl", "mouthControl",
		"bustControl", "selectorControl", "timelineControl",
	} {
		delete(base, key)
	}
	motion := motionTestIdle(resource)
	motion["lastTime"] = float64(91)
	motion["layer"] = []assets.Value{
		map[string]assets.Value{
			"type": int64(0),
			"frameList": []assets.Value{
				map[string]assets.Value{
					"time":    float64(0),
					"content": map[string]assets.Value{"src": "src/chara/mouth0"},
				},
				map[string]assets.Value{
					"time":    float64(19),
					"content": map[string]assets.Value{"src": "src/chara/mouth1"},
				},
				map[string]assets.Value{"time": float64(91)},
			},
		},
	}
	state := &motionPlayerState{
		resource: resource, chara: "chara", motion: "idle",
		speed: true, speedSet: true, playing: true,
	}
	emote := newEmotePlayerState()
	services.emoteApplyResourceMetadata(emote, resource)
	if emote.freezeBodyMotion() {
		t.Fatal("plain motion entered variable-driven body mode")
	}
	interval := 20 * 1000.0 / 60.0
	if _, err := services.emotePlayerProgress(state, emote, []tjs.Value{tjs.RealValue(interval)}); err != nil {
		t.Fatal(err)
	}
	if state.clampedEvalTime < 19 || state.clampedEvalTime > 20 {
		t.Fatalf("plain motion clamped time = %.3f, want frame time near 20", state.clampedEvalTime)
	}
	frame, err := renderPSBMotionOptions(resource, "chara", "idle", state.clampedEvalTime, 0, nil, false, 0, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	pixel := frame.RGBA[(4*8+4)*4:]
	if pixel[2] != 255 || pixel[3] != 255 {
		t.Fatalf("plain motion frame pixel = %v, want opaque blue", pixel[:4])
	}
}
func TestM2CheesewareLayerAppearsAtFrame19(t *testing.T) {
	resource := emotePlayerTestResource("m2-cheeseware.psb")
	root := resource.doc.Root.(map[string]assets.Value)
	metadata := root["metadata"].(map[string]assets.Value)
	base := metadata["base"].(map[string]assets.Value)
	for _, key := range []string{
		"variableList", "parameterList", "eyeControl", "mouthControl",
		"bustControl", "selectorControl", "timelineControl",
	} {
		delete(base, key)
	}
	motion := motionTestIdle(resource)
	motion["lastTime"] = float64(91)
	letter := map[string]assets.Value{
		"type": int64(0),
		"frameList": []assets.Value{
			map[string]assets.Value{"time": float64(0)},
			map[string]assets.Value{
				"time":    float64(19),
				"content": map[string]assets.Value{"src": "src/chara/mouth1"},
			},
			map[string]assets.Value{"time": float64(91)},
		},
	}
	strLocate := map[string]assets.Value{
		"type":     int64(2),
		"children": []assets.Value{letter},
	}
	strClip := map[string]assets.Value{
		"type":     int64(7),
		"children": []assets.Value{strLocate},
	}
	motion["layer"] = []assets.Value{strClip}

	frameAt := func(tick float64) *assets.Image {
		frame, err := renderPSBMotionOptions(resource, "chara", "idle", tick, 0, nil, false, 0, 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		return frame
	}
	early := frameAt(0)
	if pixel := early.RGBA[(4*8+4)*4:]; pixel[3] != 0 {
		t.Fatalf("cheeseware layer at frame 0 = %v, want transparent", pixel[:4])
	}
	visible := frameAt(20)
	if pixel := visible.RGBA[(4*8+4)*4:]; pixel[2] != 255 || pixel[3] != 255 {
		t.Fatalf("cheeseware layer at frame 20 = %v, want opaque blue", pixel[:4])
	}
}

func TestMotionFrameTintUsesBlendModeDivisor(t *testing.T) {
	resource := emotePlayerTestResource("tint.psb")
	image := &assets.Image{Width: 1, Height: 1, RGBA: []byte{255, 255, 255, 255}}
	content := map[string]assets.Value{
		"color": map[string]assets.Value{
			"0": int64(0xFF404040), "1": int64(0xFF404040),
			"2": int64(0xFF404040), "3": int64(0xFF404040),
		},
	}
	normal := motionFrameImage(resource, "src/color", image, content, 16)
	if got := normal.RGBA; !bytes.Equal(got, []byte{127, 127, 127, 255}) {
		t.Fatalf("normal blend tint = %v, want [127 127 127 255]", got)
	}
	additive := motionFrameImage(resource, "src/color", image, content, 0)
	if got := additive.RGBA; !bytes.Equal(got, []byte{64, 64, 64, 255}) {
		t.Fatalf("non-normal blend tint = %v, want [64 64 64 255]", got)
	}
}

func TestMotionStencilUsesMaskChildOperation(t *testing.T) {
	resource := emotePlayerTestResource("mask.psb")
	root := resource.doc.Root.(map[string]assets.Value)
	resource.images["chara/mouth0"] = &assets.Image{
		Width: 4, Height: 4, RGBA: emoteTestSolid(4, 4, 0, 0, 0, 0),
	}
	mask := map[string]assets.Value{
		"stencilType": int64(1),
		"frameList": []assets.Value{
			map[string]assets.Value{
				"time": int64(0),
				"content": map[string]assets.Value{
					"src":   "src/chara/mouth0",
					"coord": []assets.Value{int64(0), int64(0)},
				},
			},
		},
	}
	group := map[string]assets.Value{
		"type": int64(12), "stencilType": int64(4),
		"children": []assets.Value{mask},
	}
	ctx := motionRenderContext{opacity: 255, blendMode: 16, matrix: identityMotionMatrix}
	pixels := emoteTestSolid(8, 8, 255, 0, 0, 255)
	if err := applyMotionMaskChildren(resource, root, group, 0, motionRenderEval{maskMode: 1}, ctx, pixels, 8, 8, nil); err != nil {
		t.Fatal(err)
	}
	for offset := 3; offset < len(pixels); offset += 4 {
		if pixels[offset] != 0 {
			t.Fatal("stencilType 1 did not clear transparent mask pixels")
		}
	}
	mask["stencilType"] = int64(2)
	pixels = emoteTestSolid(8, 8, 255, 0, 0, 255)
	if err := applyMotionMaskChildren(resource, root, group, 0, motionRenderEval{maskMode: 1}, ctx, pixels, 8, 8, nil); err != nil {
		t.Fatal(err)
	}
	if pixels[3] != 255 {
		t.Fatal("stencilType 2 cleared pixels with a transparent mask")
	}
}

func TestEmotePlayerSkipToSyncSeeksTimelines(t *testing.T) {
	services := DefaultServices()
	state := &motionPlayerState{
		frameTick: 5, syncWaiting: true, syncActive: true,
	}
	emote := newEmotePlayerState()
	looping := &emoteTimelineState{
		label: "loop", playing: true, loop: true, control: true,
		lastTime: 10, totalFrames: 10, loopBegin: 0, loopEnd: 9,
	}
	oneShot := &emoteTimelineState{
		label: "once", playing: true, control: true,
		lastTime: 5, totalFrames: 5, loopBegin: -1, loopEnd: -1,
	}
	emote.timelines = map[string]*emoteTimelineState{
		"loop": looping, "once": oneShot,
	}
	emote.playingOrder = []string{"loop", "once"}
	services.emoteSkipToSync(state, emote)
	if looping.time != 10 || oneShot.time != 10 {
		t.Fatalf("timeline times = loop:%v once:%v, want 10", looping.time, oneShot.time)
	}
	if !looping.playing || oneShot.playing {
		t.Fatalf("timeline playing states = loop:%v once:%v, want true:false", looping.playing, oneShot.playing)
	}
	if len(emote.playingOrder) != 1 || emote.playingOrder[0] != "loop" {
		t.Fatalf("playing timeline order = %v, want [loop]", emote.playingOrder)
	}
	if state.frameTick != 5 {
		t.Fatalf("motion frame tick = %v, want unchanged 5", state.frameTick)
	}
	if state.syncWaiting || state.syncActive {
		t.Fatalf("sync flags remained set: waiting=%v active=%v", state.syncWaiting, state.syncActive)
	}
}
func TestEmoteTimelineChainFlagResetsPositionAndControlState(t *testing.T) {
	services := DefaultServices()
	emote := newEmotePlayerState()
	timeline := &emoteTimelineState{
		label: "idle", playing: false, time: 17, control: true,
		controlInit: true, controlLast: 16,
		controlCursor: []int{2}, controlValues: []float64{0.75},
		controlAnims: []emoteAnimator{{currentValue: 0.75}},
		blendRatio:   0.25,
	}
	emote.timelines["idle"] = timeline
	services.emotePlayTimeline(emote, "idle", motionPlayFlagChain)
	// Player::playTimeline resets the position and control window for every
	// call, the chain flag included (PlayerTimeline.cpp:236-251).
	if timeline.time != 0 || !timeline.controlInit || timeline.controlLast != 0 ||
		len(timeline.controlCursor) != 0 || len(timeline.controlValues) != 0 ||
		len(timeline.controlAnims) != 0 {
		t.Fatalf("playTimeline retained timeline position: %+v", timeline)
	}
	if !timeline.playing || timeline.blendRatio != 1 ||
		timeline.flags != motionPlayFlagChain {
		t.Fatalf("chain playback state = %+v", timeline)
	}
}
func TestEmotePlayerSingleCacheBindsSelectedModule(t *testing.T) {
	session := NewSession(DefaultServices(), 10_000)
	defer session.Close()
	if _, err := session.executeSource("<emoteplayer-cache-module-setup>", `
		Plugins.link("emoteplayer.dll");
		rm = new Motion.ResourceManager(null, 1024);
		emote = new Motion.EmotePlayer(rm);
		moduleSnapshot = new Dictionary();
	`, false); err != nil {
		t.Fatal(err)
	}
	managerValue, err := session.Evaluate(`rm`)
	if err != nil {
		t.Fatal(err)
	}
	moduleValue, err := session.Evaluate(`moduleSnapshot`)
	if err != nil {
		t.Fatal(err)
	}
	manager := managerValue.ObjectRef().Object
	resource := emotePlayerTestResource("single-cache.psb")
	session.Services.setMotionResource(manager, resource)
	session.Services.registerMotionModule(moduleValue, resource)
	cache, err := session.VM.NewDictionary()
	if err != nil {
		t.Fatal(err)
	}
	if err := tjs.SetMember(session.VM.Objects, cache, "single-cache.psb", moduleValue); err != nil {
		t.Fatal(err)
	}
	if err := tjs.SetMember(session.VM.Objects, managerValue, "_motionResourceCache", cache); err != nil {
		t.Fatal(err)
	}
	session.Services.rememberMotionResourceCachePath(manager, resource, "single-cache.psb")
	if _, err := session.Evaluate(`emote.play("idle"); selectedModule = emote.module === moduleSnapshot`); err != nil {
		t.Fatal(err)
	}
	value, err := session.Evaluate(`selectedModule`)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := value.Truth(); !ok {
		t.Fatal("single-cache play did not expose the selected module snapshot")
	}
}
