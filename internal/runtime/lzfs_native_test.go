package runtime

import (
	"bytes"
	"testing"

	"github.com/pierrec/lz4/v4"
)

func encodeLZFSFrame(t *testing.T, contents []byte) []byte {
	t.Helper()
	var encoded bytes.Buffer
	writer := lz4.NewWriter(&encoded)
	if _, err := writer.Write(contents); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

func TestLZFSPluginStorageMedia(t *testing.T) {
	want := bytes.Repeat([]byte("am-title-resource-"), 512)
	frame := encodeLZFSFrame(t, want)
	plain := []byte("plain resource")
	session := newScriptsTestSession(t, map[string][]byte{
		"compressed.bin": frame,
		"plain.bin":      plain,
	})

	if _, err := session.Services.readStorage("lzfs://./compressed.bin"); err == nil {
		t.Fatal("lzfs path read succeeded before linking lzfs.dll")
	}
	if _, err := session.Evaluate(`Plugins.link("plugin/LZFS.TPM")`); err != nil {
		t.Fatalf("link lzfs.dll: %v", err)
	}

	for name, wantData := range map[string][]byte{
		"lzfs://./compressed.bin": want,
		"lzfs://./plain.bin":      plain,
	} {
		got, err := session.Services.readStorage(name)
		if err != nil {
			t.Fatalf("readStorage(%q): %v", name, err)
		}
		if !bytes.Equal(got, wantData) {
			t.Errorf("readStorage(%q) = %d bytes, want %d", name, len(got), len(wantData))
		}
	}

	if _, err := session.executeSource("<lzfs-media-surface>", `
		lzfs_exists = Storages.isExistentStorage("lzfs://./compressed.bin");
		lzfs_missing = Storages.isExistentStorage("lzfs://./missing.bin");
		lzfs_local = Storages.getLocalName("lzfs://./compressed.bin");
		lzfs_list_count = Storages.getListAt("lzfs://./").length;
		lzfs_dirlist_count = Storages.dirlist("lzfs://./").length;
		lzfs_full = Storages.getFullPath("lzfs://./compressed.bin");
		lzfs_placed = Storages.getPlacedPath("lzfs://./compressed.bin");
		lzfs_fstat_size = Storages.fstat("lzfs://./compressed.bin").size;
	`, false); err != nil {
		t.Fatalf("LZFS storage media operations: %v", err)
	}
	for name, want := range map[string]string{
		"lzfs_exists":        "1",
		"lzfs_missing":       "0",
		"lzfs_local":         "",
		"lzfs_list_count":    "0",
		"lzfs_dirlist_count": "0",
		"lzfs_full":          "lzfs://./compressed.bin",
		"lzfs_placed":        "lzfs://./compressed.bin",
		"lzfs_fstat_size":    "9216",
	} {
		got, err := session.Evaluate("global." + name)
		if err != nil || got.Text() != want {
			t.Errorf("%s = %q, err=%v; want %q", name, got.Text(), err, want)
		}
	}
}

func TestLZFSPluginLinkIsListedAndIdempotent(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()
	for _, source := range []string{
		`Plugins.link("plugin/lzfs.dll")`,
		`Plugins.link("PLUGIN\\LZFS.TPM")`,
	} {
		if _, err := session.Evaluate(source); err != nil {
			t.Fatalf("%s: %v", source, err)
		}
	}
	if len(session.Services.Plugins) != 1 || pluginLinkKey(session.Services.Plugins[0]) != "plugin/lzfs.dll" {
		t.Fatalf("linked plugins = %v, want one normalized LZFS entry", session.Services.Plugins)
	}
	if _, err := session.Services.readStorage("lzfs://./missing.bin"); err == nil {
		t.Fatal("missing underlying resource unexpectedly read")
	}
}
