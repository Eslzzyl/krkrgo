package assets

import "testing"

func TestStorageClearCacheClearsMountedArchiveSegments(t *testing.T) {
	archive := &Archive{
		cache: map[segmentCacheKey]*cachedSegment{
			{offset: 4, size: 3, packed: 2}: {offset: 4, size: 3, packed: 2, bytes: []byte{1, 2, 3}},
		},
		cacheBytes: 3,
	}
	storage := &Storage{Archives: []*Archive{archive, nil}}

	storage.ClearCache()
	if len(archive.cache) != 0 || archive.cacheBytes != 0 {
		t.Fatalf("cache remained after clear: segments=%d bytes=%d", len(archive.cache), archive.cacheBytes)
	}
}
func TestArchiveSegmentCacheMaintainsIndexedLRU(t *testing.T) {
	archive := &Archive{cache: make(map[segmentCacheKey]*cachedSegment)}
	segments := []Segment{{Offset: 1, Size: 1, PackedSize: 1}, {Offset: 2, Size: 1, PackedSize: 1}, {Offset: 3, Size: 1, PackedSize: 1}}
	for _, segment := range segments {
		entry := &cachedSegment{offset: segment.Offset, size: 1, packed: 1, bytes: []byte{1}}
		archive.cache[segmentCacheKeyFor(segment)] = entry
		archive.appendCachedSegment(entry)
		archive.cacheBytes++
	}
	archive.touchCachedSegment(archive.cache[segmentCacheKeyFor(segments[0])])
	if archive.cacheOldest != archive.cache[segmentCacheKeyFor(segments[1])] {
		t.Fatalf("oldest after hit = %#v, want second segment", archive.cacheOldest)
	}
	archive.removeOldestCachedSegment()
	if _, ok := archive.cache[segmentCacheKeyFor(segments[1])]; ok {
		t.Fatal("least-recently-used archive segment survived eviction")
	}
}
