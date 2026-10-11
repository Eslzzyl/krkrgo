package core

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestStorageNameNormalization(t *testing.T) {
	got, err := StorageName(`A\B//../日.TJS`)
	if err != nil || got != "a/日.tjs" {
		t.Fatalf("StorageName normalized to %q, %v", got, err)
	}
	got, err = StorageName("Ä.TJS")
	if err != nil || got != "Ä.tjs" {
		t.Fatalf("StorageName changed non-ASCII case: %q, %v", got, err)
	}
	for _, name := range []string{"../../bad", "/tmp/x", `C:\x`, "", "a\x00b", "a/.."} {
		if _, err := StorageName(name); err == nil || !errors.Is(err, ErrInvalidStorageName) {
			t.Errorf("StorageName(%q) error = %v", name, err)
		}
	}
}

func TestLocalStoragePathPrefix(t *testing.T) {
	for _, test := range []struct{ input, want string }{
		{"file://./savedata/a", "/savedata/a"},
		{"file://.", "file://."},
		{"file://../savedata", "file://../savedata"},
		{"/already/local", "/already/local"},
	} {
		if got := LocalStoragePath(test.input); got != test.want {
			t.Errorf("LocalStoragePath(%q) = %q, want %q", test.input, got, test.want)
		}
	}
}

func TestLimitsDefaults(t *testing.T) {
	want := Limits{IndexBytes: 64 << 20, FileBytes: 512 << 20, CacheBytes: 64 << 20, Entries: 1_000_000}
	if got := DefaultLimits(); got != want {
		t.Fatalf("DefaultLimits() = %+v, want %+v", got, want)
	}
}

func TestInputActionJSONMatchesRustShape(t *testing.T) {
	cases := []struct {
		action InputAction
		json   string
	}{
		{NewAdvanceAction(), `{"type":"advance"}`},
		{NewPointerAction(-3, 4, false), `{"type":"pointer","x":-3,"y":4,"pressed":false}`},
		{NewKeyAction("Enter", false), `{"type":"key","code":"Enter","pressed":false}`},
	}
	for _, test := range cases {
		got, err := json.Marshal(test.action)
		if err != nil || string(got) != test.json {
			t.Errorf("Marshal(%+v) = %s, %v; want %s", test.action, got, err, test.json)
		}
		var roundTrip InputAction
		if err := json.Unmarshal(got, &roundTrip); err != nil || roundTrip != test.action {
			t.Errorf("round trip = %+v, %v; want %+v", roundTrip, err, test.action)
		}
	}
}

func TestKeyCodeUsesStableVirtualKeys(t *testing.T) {
	for _, test := range []struct {
		name string
		want int32
	}{
		{"A", 0x41},
		{"Enter", 0x0d},
		{"Comma", 0xbc},
		{"F13", 0x7c},
		{"Numpad9", 0x69},
	} {
		got, ok := KeyCode(test.name)
		if !ok || got != test.want {
			t.Errorf("KeyCode(%q) = %x, %v; want %x", test.name, got, ok, test.want)
		}
	}
	if _, ok := KeyCode("unknown"); ok {
		t.Fatal("unknown key accepted")
	}
}

func TestSaveDirectoryPolicy(t *testing.T) {
	project := t.TempDir()
	want := filepath.Join(project, "savedata")
	if got, err := SaveDirectory(project, nil); err != nil || got != want {
		t.Fatalf("default SaveDirectory = %q, %v; want %q", got, err, want)
	}
	if _, err := os.Stat(want); !os.IsNotExist(err) {
		t.Fatalf("default SaveDirectory created %q", want)
	}
	if err := os.Mkdir(want, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := SaveDirectory(project, &want); err != nil || got != want {
		t.Fatalf("savedata override = %q, %v; want %q", got, err, want)
	}
	for _, path := range []string{project, filepath.Join(project, "data")} {
		if _, err := SaveDirectory(project, &path); err == nil || !errors.Is(err, ErrInvalidSaveDirectory) {
			t.Errorf("SaveDirectory(%q) error = %v", path, err)
		}
	}
}

func TestSaveDirectoryChecksSymlinkAncestors(t *testing.T) {
	project := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(outside, "game")
	if err := os.Symlink(project, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	unsafe := filepath.Join(link, "new", "saves")
	if _, err := SaveDirectory(project, &unsafe); err == nil {
		t.Fatal("symlinked path into project accepted")
	}
	safe := filepath.Join(outside, "new", "saves")
	got, err := SaveDirectory(project, &safe)
	if err != nil || got != safe {
		t.Fatalf("outside save path = %q, %v; want %q", got, err, safe)
	}
}
