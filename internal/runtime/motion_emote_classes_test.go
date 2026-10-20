package runtime

import (
	"strings"
	"testing"
)

// TestD3DEmoteModuleClass covers the module-scope class emoteplayer.dll
// registers next to EmotePlayer (main.cpp:565-587): constructibility, the
// timeline constants, the shared property store, and the setMaxTextureSize
// stub.
func TestD3DEmoteModuleClass(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()

	if _, err := session.executeSource("<d3demotemodule-class>", `
		Plugins.link("emoteplayer.dll");
		moduleType = typeof D3DEmoteModule;
		moduleIsClass = D3DEmoteModule instanceof "Class";
		first = new D3DEmoteModule();
		instanceType = typeof first;
		defaultMaskMode = first.maskMode;
		defaultRegionClipping = first.maskRegionClipping;
		defaultMipMap = first.mipMapEnabled;
		defaultAlphaOp = first.alphaOp;
		defaultProtectColor = first.protectTranslucentTextureColor;
		defaultPixelateDivision = first.pixelateDivision;
		parallelFlag = D3DEmoteModule.TimelinePlayFlagParallel;
		sequentialFlag = D3DEmoteModule.TimelinePlayFlagSequential;
	`, false); err != nil {
		t.Fatalf("link emoteplayer.dll and construct D3DEmoteModule: %v", err)
	}
	value, err := session.Evaluate(`moduleType + "|" + moduleIsClass + "|" + instanceType + "|" + defaultMaskMode + "|" + defaultRegionClipping + "|" + defaultMipMap + "|" + defaultAlphaOp + "|" + defaultProtectColor + "|" + defaultPixelateDivision + "|" + parallelFlag + "|" + sequentialFlag`)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := value.Text(), "Object|1|Object|1|0|0|0|0|1|1|2"; got != want {
		t.Fatalf("D3DEmoteModule construction state = %q, want %q", got, want)
	}
}

// TestD3DEmoteModuleSharedState verifies that the property store is shared
// across instances and class-level access, matching the reference's
// `inline static` fields (D3DEmoteModule.h:43-48).
func TestD3DEmoteModuleSharedState(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()

	if _, err := session.executeSource("<d3demotemodule-shared>", `
		Plugins.link("emoteplayer.dll");
		first = new D3DEmoteModule();
		second = new D3DEmoteModule();
		first.maskMode = 3;
		first.alphaOp = 7;
		first.pixelateDivision = 4;
		first.maskRegionClipping = 1;
		first.mipMapEnabled = 1;
		first.protectTranslucentTextureColor = 1;
		readbacks = first.maskMode + "/" + first.alphaOp + "/" + first.pixelateDivision + "/" + first.maskRegionClipping + "/" + first.mipMapEnabled + "/" + first.protectTranslucentTextureColor;
		sharedReadbacks = second.maskMode + "/" + second.alphaOp + "/" + second.pixelateDivision + "/" + second.maskRegionClipping + "/" + second.mipMapEnabled + "/" + second.protectTranslucentTextureColor;
		D3DEmoteModule.maskMode = 9;
		classLevelReadback = second.maskMode;
		second.alphaOp = 2;
		classLevelAlphaOp = D3DEmoteModule.alphaOp;
	`, false); err != nil {
		t.Fatalf("round-trip D3DEmoteModule properties: %v", err)
	}
	value, err := session.Evaluate(`readbacks + "|" + sharedReadbacks + "|" + classLevelReadback + "|" + classLevelAlphaOp`)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := value.Text(), "3/7/4/1/1/1|3/7/4/1/1/1|9|2"; got != want {
		t.Fatalf("D3DEmoteModule shared state = %q, want %q", got, want)
	}
}

// TestD3DEmoteModuleSetMaxTextureSize covers the logging stub's argument
// contract: two numeric arguments succeed, a bare call raises a TJS error.
func TestD3DEmoteModuleSetMaxTextureSize(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()

	if _, err := session.executeSource("<d3demotemodule-maxtexture>", `
		Plugins.link("emoteplayer.dll");
		accepted = false;
		try {
			D3DEmoteModule.setMaxTextureSize(64, 64);
			accepted = true;
		} catch (e) {
			accepted = false;
			acceptError = e.message;
		}
		rejected = false;
		try {
			D3DEmoteModule.setMaxTextureSize();
		} catch (e) {
			rejected = true;
			rejectError = e.message;
		}
	`, false); err != nil {
		t.Fatalf("call D3DEmoteModule.setMaxTextureSize: %v", err)
	}
	value, err := session.Evaluate(`accepted + "|" + rejected + "|" + rejectError`)
	if err != nil {
		t.Fatal(err)
	}
	got := value.Text()
	if !strings.HasPrefix(got, "1|1|") {
		t.Fatalf("setMaxTextureSize results = %q, want accepted and rejected with an error message", got)
	}
	if !strings.Contains(got, "setMaxTextureSize requires width and height") {
		t.Fatalf("setMaxTextureSize error = %q, want the missing-argument message", got)
	}
}

// TestD3DEmotePlayerClass covers the global D3DEmotePlayer class: it
// constructs from a ResourceManager and every EmotePlayer-shaped member is
// served by the existing dispatcher (main.cpp:589-687).
func TestD3DEmotePlayerClass(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()

	if _, err := session.executeSource("<d3demoteplayer-class>", `
		Plugins.link("emoteplayer.dll");
		playerType = typeof D3DEmotePlayer;
		playerIsClass = D3DEmotePlayer instanceof "Class";
		rm = new Motion.ResourceManager(null, 1024);
		p = new D3DEmotePlayer(rm);
		instanceType = typeof p;
		p.chara = "chara";
		p.motion = "idle";
		chara = p.chara;
		motion = p.motion;
		moduleDefault = p.module === void;
		variableCount = p.countVariables();
		p.progress(1);
		progressCalled = true;
	`, false); err != nil {
		t.Fatalf("link emoteplayer.dll and construct D3DEmotePlayer: %v", err)
	}
	value, err := session.Evaluate(`playerType + "|" + playerIsClass + "|" + instanceType + "|" + chara + "|" + motion + "|" + moduleDefault + "|" + variableCount + "|" + progressCalled`)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := value.Text(), "Object|1|Object|chara|idle|1|0|1"; got != want {
		t.Fatalf("D3DEmotePlayer state = %q, want %q", got, want)
	}
}
