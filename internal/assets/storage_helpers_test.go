package assets

import "testing"

func TestStorageClearCacheClearsMountedArchiveSegments(t *testing.T) {
	archive := &Archive{
		cache:      []cachedSegment{{offset: 4, size: 3, packed: 2, bytes: []byte{1, 2, 3}}},
		cacheBytes: 3,
	}
	storage := &Storage{Archives: []*Archive{archive, nil}}

	storage.ClearCache()
	if len(archive.cache) != 0 || archive.cacheBytes != 0 {
		t.Fatalf("cache remained after clear: segments=%d bytes=%d", len(archive.cache), archive.cacheBytes)
	}
}
