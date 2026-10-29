package runtime

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/eslzzyl/krkrgo/internal/assets"
	"github.com/eslzzyl/krkrgo/internal/core"
)

func TestNativeArrayLoadReadsTextLines(t *testing.T) {
	session := newScriptsTestSession(t, map[string][]byte{
		"lines.txt": []byte("first\r\n\rsecond\n"),
		"utf16.txt": scriptsTestUTF16LE("日本語\r\n二行"),
	})
	if _, err := session.executeSource("<array-load-test>", `a = new Array(); a.push("old"); returned = a.load("lines.txt");`, false); err != nil {
		t.Fatalf("Array.load text: %v", err)
	}
	if got, err := session.Evaluate(`returned === a && a.length == 3 && a[0] == "first" && a[1] == "" && a[2] == "second"`); err != nil {
		t.Fatal(err)
	} else if ok, _ := got.Truth(); !ok {
		t.Fatalf("Array.load line contents or return value = %v", got)
	}
	if _, err := session.executeSource("<array-load-utf16-test>", `b = new Array(); b.load("utf16.txt");`, false); err != nil {
		t.Fatalf("Array.load UTF-16 text: %v", err)
	}
	got, err := session.Evaluate(`b.length == 2 && b[0] == "日本語" && b[1] == "二行"`)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := got.Truth(); !ok {
		t.Fatalf("Array.load UTF-16 lines = %v", got)
	}
}

func TestNativeArrayLoadErrorsPreserveReceiver(t *testing.T) {
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "malformed.txt"), []byte{0xff, 0xfe, 0x00, 0xd8}, 0o644); err != nil {
		t.Fatal(err)
	}
	storage, err := assets.OpenStorage(project, core.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	session := NewSession(NewServices(storage, "", core.DefaultLimits()), 1_000_000)
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Errorf("close session: %v", err)
		}
		for _, archive := range storage.Archives {
			if err := archive.Close(); err != nil {
				t.Errorf("close storage archive: %v", err)
			}
		}
	})
	if _, err := session.executeSource("<array-load-error-test>", `a = ["retained"];`, false); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Evaluate(`a.load("missing.txt")`); !errors.Is(err, assets.ErrNotFound) {
		t.Fatalf("missing Array.load error = %v, want ErrNotFound", err)
	}
	if _, err := session.Evaluate(`a.load("malformed.txt")`); err == nil {
		t.Fatal("malformed text Array.load succeeded")
	}
	got, err := session.Evaluate(`a.length == 1 && a[0] == "retained"`)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := got.Truth(); !ok {
		t.Fatalf("Array.load failure changed receiver: %v", got)
	}
}

func TestNativeArraySave2WritesArrayLines(t *testing.T) {
	session := newScriptsTestSession(t, nil)
	saveDir := t.TempDir()
	session.Services.SetSaveDir(saveDir)

	if _, err := session.executeSource("<array-save2-test>", `
Plugins.link("savestruct.dll");
source = ["name=\"甲\"", "count=\"2\""];
source.save2("settings.cfu");
loaded = new Array();
loaded.load("settings.cfu");
`, false); err != nil {
		t.Fatalf("Array.save2: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(saveDir, "settings.cfu"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(data), "name=\"甲\"\r\ncount=\"2\"\r\n"; got != want {
		t.Fatalf("Array.save2 data = %q, want %q", got, want)
	}
	got, err := session.Evaluate(`loaded.length == 2 && loaded[0] == "name=\"甲\"" && loaded[1] == "count=\"2\""`)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := got.Truth(); !ok {
		t.Fatalf("Array.save2 round trip = %v", got)
	}
}

func TestTextStreamReadHonorsBinaryOffset(t *testing.T) {
	session := newScriptsTestSession(t, nil)
	saveDir := t.TempDir()
	session.Services.SetSaveDir(saveDir)
	data := append([]byte("BMP-prefix"), []byte(`%["answer" => 42]`)...)
	if err := os.WriteFile(filepath.Join(saveDir, "thumbnail.bmp"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := session.Evaluate(`TextStream.read("thumbnail.bmp", "o10")`)
	if err != nil {
		t.Fatalf("TextStream.read offset: %v", err)
	}
	if got.Text() != `%["answer" => 42]` {
		t.Fatalf("TextStream.read offset = %q", got.Text())
	}
}
