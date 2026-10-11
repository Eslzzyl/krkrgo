package runtime

import (
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
	} {
		if _, err := session.Evaluate(link); err != nil {
			t.Fatalf("%s: %v", link, err)
		}
	}

	if len(services.Plugins) != 3 {
		t.Fatalf("normalized plugin list = %v, want one entry per built-in module", services.Plugins)
	}
	for _, source := range []string{
		`parser = new KAGParser(); parser.loadScenario("alias.ks", "hello")`,
		`window = new Window(); window.registerExEvent()` ,
		`menu = new MenuItem(window, "menu")`,
	} {
		if _, err := session.executeSource("<plugin-alias-test>", source, false); err != nil {
			t.Fatalf("built-in module after link (%s): %v", source, err)
		}
	}
	if _, err := session.Evaluate(`MenuItem.textToKeycode.f4 == 115`); err != nil {
		t.Fatal(err)
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
