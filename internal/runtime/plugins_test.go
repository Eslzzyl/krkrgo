package runtime

import (
	"errors"
	"testing"
)

func TestBuiltinPluginLinksNormalizePathsCaseAndExtensions(t *testing.T) {
	services := DefaultServices()
	session := NewSession(services, 100_000)
	defer session.Close()
	for _, link := range []string{
		`Plugins.link("plugin\\KAGParser.TPM")`,
		`Plugins.link("PLUGIN/kagparser.dll")`,
		`Plugins.link("Plugin/KAGParserEx.TPM")`,
		`Plugins.link("plugin/Menu.TPM")`,
		`Plugins.link("PLUGIN\\menu.DLL")`,
		`Plugins.link("plugin/windowEx.TPM")`,
		`Plugins.link("PLUGIN/WINDOWEX.dll")`,
		`Plugins.link("plugin/LayerExImage.TPM")`,
		`Plugins.link("PLUGIN\\layereximage.dll")`,
		`Plugins.link("plugin/TextRender.TPM")`,
		`Plugins.link("plugin/LayerExRaster.TPM")`,
		`Plugins.link("plugin/LayerExDraw.TPM")`,
	} {
		if _, err := session.Evaluate(link); err != nil {
			t.Fatalf("%s: %v", link, err)
		}
	}
	if len(services.Plugins) != 7 {
		t.Fatalf("normalized plugin list = %v, want one entry per built-in module", services.Plugins)
	}
	for _, source := range []string{
		`parser = new KAGParser(); parser.loadScenario("alias.ks", "hello")`,
		`window = new Window(); window.registerExEvent()`,
		`menu = new MenuItem(window, "menu")`,
		`layer = new Layer(); layer.setImageSize(1,1); layer.light(0,0)`,
	} {
		if _, err := session.executeSource("<plugin-alias-test>", source, false); err != nil {
			t.Fatalf("built-in module after link (%s): %v", source, err)
		}
	}
	if _, err := session.Evaluate(`MenuItem.textToKeycode.f4 == 115`); err != nil {
		t.Fatal(err)
	}
}

func TestMigratedPluginLinksExposeTheirSurfaces(t *testing.T) {
	services := DefaultServices()
	session := NewSession(services, 100_000)
	defer session.Close()

	for _, link := range []string{
		`Plugins.link("plugin/fstat.dll")`,
		`Plugins.link("plugin/wutcwf.dll")`,
		`Plugins.link("plugin/psd.dll")`,
	} {
		if _, err := session.Evaluate(link); err != nil {
			t.Fatalf("%s: %v", link, err)
		}
	}
	for _, source := range []string{
		`typeof Storages.searchPath === "Object"`,
		`typeof PSD === "Object"`,
	} {
		value, err := session.Evaluate(source)
		if err != nil {
			t.Fatalf("%s: %v", source, err)
		}
		if ok, _ := value.Truth(); !ok {
			t.Fatalf("%s = false", source)
		}
	}
}

func TestKirikiroid2TextEncodingPatch(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()

	if _, err := session.Evaluate(`Plugins.link("kirikiroid2.dll"); Storages.setTextEncoding("gbk");`); err != nil {
		t.Fatalf("Kirikiroid2 encoding patch: %v", err)
	}
	value, err := session.Evaluate(`Scripts.textEncoding`)
	if err != nil {
		t.Fatal(err)
	}
	if got := value.Text(); got != "gbk" {
		t.Fatalf("Scripts.textEncoding = %q, want gbk", got)
	}
}

func TestPackinOnePluginLinkIsIdempotentAndListed(t *testing.T) {
	services := DefaultServices()
	session := NewSession(services, 100_000)
	defer session.Close()

	for _, source := range []string{
		`Plugins.link("plugin/PackinOne.dll")`,
		`Plugins.link("PLUGIN\\packinone.tpm")`,
	} {
		if _, err := session.Evaluate(source); err != nil {
			t.Fatalf("%s: %v", source, err)
		}
	}
	if len(services.Plugins) != 1 || pluginLinkKey(services.Plugins[0]) != "plugin/packinone.dll" {
		t.Fatalf("PackinOne link list = %v, want one normalized entry", services.Plugins)
	}
	value, err := session.Evaluate(`Plugins.getList()[0]`)
	if err != nil {
		t.Fatal(err)
	}
	if value.Text() != "plugin/PackinOne.dll" {
		t.Fatalf("Plugins.getList()[0] = %q, want first linked spelling", value.Text())
	}
}

func TestPackinOneLinksDuringStartup(t *testing.T) {
	session := newScriptsTestSession(t, map[string][]byte{
		"startup.tjs": []byte(`Plugins.link("PackinOne.dll");`),
	})
	if _, err := session.Startup(); err != nil {
		t.Fatalf("startup linking PackinOne.dll: %v", err)
	}
	if len(session.Services.Plugins) != 1 || pluginLinkKey(session.Services.Plugins[0]) != "packinone.dll" {
		t.Fatalf("startup plugin list = %v, want PackinOne.dll", session.Services.Plugins)
	}
}

func TestPluginLinkKeyNormalizesWindowsPathsAndTPMExtension(t *testing.T) {
	for _, test := range []struct{ name, want string }{
		{`Plugin\\KAGParser.TPM`, "plugin/kagparser.dll"},
		{`PLUGIN/kagparser.dll`, "plugin/kagparser.dll"},
		{`plugin/./KAGPARSER.dll`, "plugin/kagparser.dll"},
		{`plugins/KAGParserEx.dll`, "plugins/kagparser.dll"},
	} {
		if key := pluginLinkKey(test.name); key != test.want {
			t.Errorf("pluginLinkKey(%q) = %q, want %q", test.name, key, test.want)
		}
	}
}
func TestExtransPluginLinkIsExplicitlySupportedAsLinkOnly(t *testing.T) {
	services := DefaultServices()
	session := NewSession(services, 100_000)
	defer session.Close()

	for _, link := range []string{
		`Plugins.link("plugin/extrans.dll")`,
		`Plugins.link("PLUGIN\\EXTRANS.TPM")`,
		`Plugins.link("plugin/extNagano.dll")`,
	} {
		if _, err := session.Evaluate(link); err != nil {
			t.Fatalf("%s: %v", link, err)
		}
	}
	if len(services.Plugins) != 2 {
		t.Fatalf("linked transition plugins = %v, want extrans and extNagano", services.Plugins)
	}
	if _, err := session.Evaluate(`Plugins.link("unimplemented-extrans.dll")`); err == nil {
		t.Fatal("unimplemented plugin link unexpectedly succeeded")
	}
}
func TestScriptsExPluginLinkIsNormalizedIdempotentAndExposesImplementedHelpers(t *testing.T) {
	services := DefaultServices()
	session := NewSession(services, 100_000)
	defer session.Close()

	for _, source := range []string{
		`Plugins.link("plugin/ScriptsEx.TPM")`,
		`Plugins.link("PLUGIN\\scriptsex.dll")`,
	} {
		if _, err := session.Evaluate(source); err != nil {
			t.Fatalf("%s: %v", source, err)
		}
	}
	if len(services.Plugins) != 1 || pluginLinkKey(services.Plugins[0]) != "plugin/scriptsex.dll" {
		t.Fatalf("ScriptsEx link list = %v, want one normalized entry", services.Plugins)
	}
	value, err := session.Evaluate(`Plugins.getList()[0]`)
	if err != nil || value.Text() != "plugin/ScriptsEx.TPM" {
		t.Fatalf("Plugins.getList()[0] = %q, err=%v; want first linked spelling", value.Text(), err)
	}

	if _, err := session.executeSource("<scriptsex-link-test>", `
		global.scriptsex_keys = Scripts.getObjectKeys(%[alpha:1]).join(",");
		global.scriptsex_count = Scripts.getObjectCount(%[alpha:1]);
		global.scriptsex_equal = Scripts.equalStruct([1], [1]);
	`, false); err != nil {
		t.Fatalf("implemented ScriptsEx helpers after link: %v", err)
	}
	for name, want := range map[string]string{
		"scriptsex_keys":  "alpha",
		"scriptsex_count": "1",
		"scriptsex_equal": "1",
	} {
		got, err := session.Evaluate("global." + name)
		if err != nil || got.Text() != want {
			t.Errorf("%s = %q, err=%v; want %q", name, got.Text(), err, want)
		}
	}
}

func TestWindowsOnlyPluginLinksReportTheUnavailableModule(t *testing.T) {
	services := DefaultServices()
	session := NewSession(services, 100_000)
	defer session.Close()

	for _, link := range []string{
		`Plugins.link("plugin/win32ole.dll")`,
		`Plugins.link("PLUGIN\\KRKRSTEAM.TPM")`,
		`Plugins.link("plugin/SteamDrawDevice.dll")`,
		`Plugins.link("plugin/k2compat.dll")`,
		`Plugins.link("plugin/kagexopt.dll")`,
		`Plugins.link("plugin/wuopus.dll")`,
	} {
		if _, err := session.Evaluate(link); !errors.Is(err, ErrUnsupportedNative) {
			t.Errorf("%s error = %v, want ErrUnsupportedNative", link, err)
		}
	}
	if len(services.Plugins) != 0 {
		t.Fatalf("unavailable Windows-only links were recorded: %v", services.Plugins)
	}
}

func TestKrmovieLinkInstallsNoFabricatedMovieClass(t *testing.T) {
	services := DefaultServices()
	session := NewSession(services, 100_000)
	defer session.Close()

	if _, err := session.Evaluate(`Plugins.link("plugin/krmovie.dll")`); err != nil {
		t.Fatalf("krmovie.dll link: %v", err)
	}
	// The reference module supplies the core VideoOverlay backend and registers no
	// TJS names, so the link must not invent a movie class or its compatibility
	// globals.
	for _, source := range []string{
		`typeof global.GFX_Movie == "undefined"`,
		`typeof global.Types == "undefined"`,
		`typeof global.Entries == "undefined"`,
	} {
		value, err := session.Evaluate(source)
		if err != nil {
			t.Fatalf("%s: %v", source, err)
		}
		ok, err := value.Truth()
		if err != nil || !ok {
			t.Errorf("%s = %v, err=%v; want the global to remain undefined", source, value, err)
		}
	}
}

func TestDrawDeviceD2DLinkKeepsTheCurrentHostDevice(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()

	if _, err := session.Evaluate(`Plugins.link("DrawDeviceD2D.dll")`); err != nil {
		t.Fatalf("DrawDeviceD2D link: %v", err)
	}
	for _, source := range []string{
		`typeof global.DrawDeviceD2D == "undefined"`,
		`typeof global.KAGWindow_createDrawDevice == "undefined"`,
	} {
		value, err := session.Evaluate(source)
		if err != nil {
			t.Fatalf("%s: %v", source, err)
		}
		if ok, err := value.Truth(); err != nil || !ok {
			t.Errorf("%s = %v, err=%v; want true", source, value, err)
		}
	}
}
