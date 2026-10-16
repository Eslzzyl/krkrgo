package assets

import (
	"bytes"
	"crypto/sha256"
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
func TestImageDecodeCacheMaintainsIndexedLRU(t *testing.T) {
	cache := imageDecodeCache{entries: make(map[[sha256.Size]byte]*imageDecodeCacheEntry)}
	keys := [3][sha256.Size]byte{{0}, {1}, {2}}
	for i, key := range keys {
		entry := &imageDecodeCacheEntry{key: key, image: Image{RGBA: []byte{byte(i)}}}
		cache.entries[key] = entry
		cache.appendNewest(entry)
		cache.bytes++
	}
	cache.touch(cache.entries[keys[0]])
	if cache.oldest != cache.entries[keys[1]] || cache.newest != cache.entries[keys[0]] {
		t.Fatalf("LRU order after hit = oldest:%v newest:%v", cache.oldest.key, cache.newest.key)
	}
	cache.removeOldest()
	if _, ok := cache.entries[keys[1]]; ok {
		t.Fatal("least-recently-used entry survived eviction")
	}
	if cache.bytes != 2 {
		t.Fatalf("cache bytes after eviction = %d, want 2", cache.bytes)
	}
}

var benchmarkImageCachePixel byte

func BenchmarkImageDecodeCacheLargeLookup(b *testing.B) {
	const entries = 4096
	data := []byte("image-cache-large-lookup")
	targetKey := sha256.Sum256(data)
	storage := &Storage{limits: core.Limits{CacheBytes: entries * 4}}
	cache := &storage.imageCache
	cache.entries = make(map[[sha256.Size]byte]*imageDecodeCacheEntry, entries)
	for i := 0; i < entries; i++ {
		var key [sha256.Size]byte
		if i == 0 {
			key = targetKey
		} else {
			key[0] = byte(i)
			key[1] = byte(i >> 8)
		}
		entry := &imageDecodeCacheEntry{key: key, image: Image{RGBA: []byte{1, 2, 3, 255}}}
		cache.entries[key] = entry
		cache.appendNewest(entry)
		cache.bytes += 4
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		image, err := storage.DecodeImage(data)
		if err != nil {
			b.Fatal(err)
		}
		benchmarkImageCachePixel = image.RGBA[0]
	}
}
