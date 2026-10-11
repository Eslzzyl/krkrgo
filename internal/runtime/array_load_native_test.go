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
