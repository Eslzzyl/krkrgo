package runtime

import (
	"strconv"
	"strings"
	"testing"
)

func TestTOMLPluginDecodesStorageValues(t *testing.T) {
	session := newScriptsTestSession(t, map[string][]byte{
		"settings.toml": []byte(`title = "简体中文"
enabled = true
count = 42
ratio = 1.25
features = ["read", "write"]

[display]
width = 1280
height = 720

[[profiles]]
name = "normal"
active = true

[[profiles]]
name = "compact"
active = false
`),
	})

	for _, method := range []string{"tomlDecodeFromStorage", "tomlDecodeFromString", "tomlEncode"} {
		got, err := session.Evaluate("typeof Scripts." + method)
		if err != nil || got.Text() != "undefined" {
			t.Fatalf("%s before plugin link = %q, err=%v; want undefined", method, got.Text(), err)
		}
	}
	if _, err := session.executeSource("<toml-plugin-test>", `
		Plugins.link("plugin/toml.dll");
		decoded = Scripts.tomlDecodeFromStorage("settings.toml");
	`, false); err != nil {
		t.Fatalf("decode settings.toml: %v", err)
	}
	got, err := session.Evaluate(`
		typeof Scripts.tomlDecodeFromStorage == "Object" &&
		typeof Scripts.tomlDecodeFromString == "Object" &&
		typeof Scripts.tomlEncode == "Object" &&
		decoded instanceof "Dictionary" &&
		decoded.title === "简体中文" &&
		decoded.enabled === 1 &&
		decoded.count === 42 &&
		decoded.ratio === 1.25 &&
		decoded.features instanceof "Array" &&
		decoded.features.length === 2 &&
		decoded.display instanceof "Dictionary" &&
		decoded.display.width === 1280 &&
		decoded.profiles instanceof "Array" &&
		decoded.profiles.length === 2 &&
		decoded.profiles[1].name === "compact" &&
		decoded.profiles[1].active === 0
	`)
	if err != nil {
		t.Fatalf("inspect decoded TOML value: %v", err)
	}
	if truth, err := got.Truth(); err != nil || !truth {
		t.Fatalf("decoded TOML predicate = %v, err=%v; want true", got, err)
	}
}

func TestTOMLPluginStringDecodeAndEncodeRoundTrip(t *testing.T) {
	session := newScriptsTestSession(t, nil)
	source := "title = \"from string\"\ncount = 7\n[window]\nwidth = 1280\n"
	script := `Plugins.link("toml.dll");
		decodedText = Scripts.tomlDecodeFromString(` + strconv.Quote(source) + `);
		encodedText = Scripts.tomlEncode(decodedText);
		roundTrip = Scripts.tomlDecodeFromString(encodedText);`
	if _, err := session.executeSource("<toml-string-test>", script, false); err != nil {
		t.Fatalf("decode and encode TOML string: %v", err)
	}
	got, err := session.Evaluate(`roundTrip.title === "from string" && roundTrip.count === 7 && roundTrip.window.width === 1280`)
	if err != nil {
		t.Fatalf("inspect TOML round trip: %v", err)
	}
	if truth, err := got.Truth(); err != nil || !truth {
		t.Fatalf("round-trip predicate = %v, err=%v; want true", got, err)
	}
}

func TestTOMLPluginReportsStorageAndParseErrors(t *testing.T) {
	session := newScriptsTestSession(t, map[string][]byte{
		"broken.toml": []byte("value = [1,\n"),
	})
	if _, err := session.Evaluate(`Plugins.link("toml.dll")`); err != nil {
		t.Fatalf("link toml.dll: %v", err)
	}
	if _, err := session.Evaluate(`Scripts.tomlDecodeFromStorage("missing.toml")`); err == nil || !strings.Contains(err.Error(), "missing.toml") {
		t.Fatalf("missing storage error = %v; want storage path", err)
	}
	if _, err := session.Evaluate(`Scripts.tomlDecodeFromStorage("broken.toml")`); err == nil || !strings.Contains(err.Error(), "broken.toml") {
		t.Fatalf("malformed TOML error = %v; want storage path", err)
	}
	if _, err := session.Evaluate(`Scripts.tomlDecodeFromString("value = [1,")`); err == nil || !strings.Contains(err.Error(), "tomlDecodeFromString") {
		t.Fatalf("malformed string TOML error = %v; want method name", err)
	}
	if _, err := session.Evaluate(`Scripts.tomlEncode(null)`); err == nil || !strings.Contains(err.Error(), "TOML does not support null") {
		t.Fatalf("encoding null error = %v; want unsupported-value error", err)
	}
}
