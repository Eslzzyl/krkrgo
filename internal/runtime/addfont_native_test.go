package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eslzzyl/krkrgo/internal/assets"
	"github.com/eslzzyl/krkrgo/internal/core"
	"golang.org/x/image/font/gofont/goregular"
)

func TestAddFontPluginRegistersStorageFontInRuntimeFontBook(t *testing.T) {
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "font.ttf"), goregular.TTF, 0o600); err != nil {
		t.Fatal(err)
	}
	storage, err := assets.OpenStorage(project, core.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	services := NewServices(storage, "", core.Limits{})
	services.SetFontBook(NewFontBook())
	session := NewSession(services, 100_000)
	defer session.Close()

	value, err := session.Evaluate(`typeof System.addFont != "Object"`)
	if err != nil {
		t.Fatal(err)
	}
	if linked, _ := value.Truth(); !linked {
		t.Fatal("System.addFont is exposed before addFont.dll is linked")
	}
	if _, err := session.executeSource("<addfont-link>", `Plugins.link("addFont.dll")`, false); err != nil {
		t.Fatal(err)
	}
	value, err = session.Evaluate(`typeof System.addFont == "Object"`)
	if err != nil {
		t.Fatal(err)
	}
	if linked, _ := value.Truth(); !linked {
		t.Fatal("addFont.dll link did not expose System.addFont")
	}
	registered, err := session.Evaluate(`System.addFont("font.ttf", false)`)
	if err != nil {
		t.Fatal(err)
	}
	if count, err := registered.Int(); err != nil || count != 1 {
		t.Fatalf("System.addFont result = %v, %v; want one registered face", registered, err)
	}
	value, err = session.Evaluate(`(new Font()).getList().join("|")`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(value.Text(), "Go") {
		t.Fatalf("Font.getList() = %q, want registered Go family", value.Text())
	}
	if _, err := session.executeSource("<addfont-font-width>", `font = new Font(); font.face = "Go"`, false); err != nil {
		t.Fatal(err)
	}
	value, err = session.Evaluate(`font.getTextWidth("abc")`)
	if err != nil {
		t.Fatal(err)
	}
	if width, err := value.Int(); err != nil || width <= 0 {
		t.Fatalf("registered font text width = %v, %v", value, err)
	}
}

func TestUnknownPluginStillFailsExplicitly(t *testing.T) {
	session := NewSession(DefaultServices(), 10_000)
	defer session.Close()
	if _, err := session.Evaluate(`Plugins.link("unknown-plugin.dll")`); err == nil || !strings.Contains(err.Error(), ErrUnsupportedNative.Error()) {
		t.Fatalf("unknown plugin link error = %v, want explicit unsupported error", err)
	}
}
