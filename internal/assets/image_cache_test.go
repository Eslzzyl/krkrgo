package assets

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/eslzzyl/krkrgo/internal/core"
)

func TestStorageImageCacheAliasesOwnershipAndClear(t *testing.T) {
	project := t.TempDir()
	path := filepath.Join(project, "art", "icon.png")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	source := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	source.SetNRGBA(0, 0, color.NRGBA{R: 10, G: 20, B: 30, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, source); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	storage, err := OpenStorage(project, core.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	firstBytes, err := storage.Read("art/icon.png")
	if err != nil {
		t.Fatal(err)
	}
	// Extensionless and explicit names resolve to identical source content in
	// the runtime, so the cache must key by content rather than storage name.
	secondBytes := append([]byte(nil), firstBytes...)
	first, err := storage.DecodeImage(firstBytes)
	if err != nil {
		t.Fatal(err)
	}
	first.RGBA[0] = 99 // Model Layer color-key mutation of its owned pixels.
	second, err := storage.DecodeImage(secondBytes)
	if err != nil {
		t.Fatal(err)
	}
	if got := second.RGBA; !bytes.Equal(got, []byte{10, 20, 30, 255}) {
		t.Fatalf("cache hit returned mutated pixels: %v", got)
	}
	if len(storage.imageCache.entries) != 1 {
		t.Fatalf("identical source bytes created %d cache entries, want 1", len(storage.imageCache.entries))
	}

	// Different source content must resolve to a different cache key.
	source.SetNRGBA(0, 0, color.NRGBA{R: 40, G: 50, B: 60, A: 255})
	encoded.Reset()
	if err := png.Encode(&encoded, source); err != nil {
		t.Fatal(err)
	}
	updated, err := storage.DecodeImage(encoded.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if got := updated.RGBA; !bytes.Equal(got, []byte{40, 50, 60, 255}) {
		t.Fatalf("changed source returned stale pixels: %v", got)
	}
	if len(storage.imageCache.entries) != 2 {
		t.Fatalf("changed source cache entries=%d, want 2 content identities", len(storage.imageCache.entries))
	}

	storage.ClearCache()
	if len(storage.imageCache.entries) != 0 || storage.imageCache.bytes != 0 {
		t.Fatalf("decoded-image cache survived clear: entries=%d bytes=%d", len(storage.imageCache.entries), storage.imageCache.bytes)
	}
	storage.limits.CacheBytes = 3 // The decoded 1x1 RGBA requires four bytes.
	if _, err := storage.DecodeImage(firstBytes); err != nil {
		t.Fatal(err)
	}
	if len(storage.imageCache.entries) != 0 || storage.imageCache.bytes != 0 {
		t.Fatalf("oversized decoded image entered cache: entries=%d bytes=%d", len(storage.imageCache.entries), storage.imageCache.bytes)
	}
}
