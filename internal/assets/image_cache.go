package assets

import (
	"crypto/sha256"
	"sync"
)

const maxDecodedImageCacheEntries = 4096

type imageDecodeCacheEntry struct {
	key   [sha256.Size]byte
	image Image
	older *imageDecodeCacheEntry
	newer *imageDecodeCacheEntry
}

type imageDecodeCache struct {
	mu         sync.Mutex
	entries    map[[sha256.Size]byte]*imageDecodeCacheEntry
	oldest     *imageDecodeCacheEntry
	newest     *imageDecodeCacheEntry
	bytes      uint64
	generation uint64
}

// DecodeImage returns a decoded image owned by the caller. Cached image bytes
// are keyed by the source content, so loose-file edits and differently named
// aliases cannot return stale or cross-project results. RGBA is cloned on every
// hit because Layer.loadImages may apply a color key in place.
func (s *Storage) DecodeImage(data []byte) (Image, error) {
	if s == nil || len(data) == 0 || s.limits.CacheBytes == 0 {
		return DecodeImage(data)
	}
	key := sha256.Sum256(data)
	cache := &s.imageCache
	cache.mu.Lock()
	generation := cache.generation
	if entry, ok := cache.entries[key]; ok {
		cache.touch(entry)
		image := entry.image
		cache.mu.Unlock()
		return cloneImage(image), nil
	}
	cache.mu.Unlock()

	decoded, err := DecodeImage(data)
	if err != nil {
		return Image{}, err
	}
	if uint64(len(decoded.RGBA)) > s.limits.CacheBytes {
		return decoded, nil
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.generation != generation {
		return decoded, nil
	}
	if entry, ok := cache.entries[key]; ok {
		cache.touch(entry)
		return cloneImage(entry.image), nil
	}
	decodedBytes := uint64(len(decoded.RGBA))
	for len(cache.entries) > 0 && (len(cache.entries) >= maxDecodedImageCacheEntries || cache.bytes > s.limits.CacheBytes-decodedBytes) {
		cache.removeOldest()
	}
	if cache.entries == nil {
		cache.entries = make(map[[sha256.Size]byte]*imageDecodeCacheEntry)
	}
	entry := &imageDecodeCacheEntry{key: key, image: cloneImage(decoded)}
	cache.entries[key] = entry
	cache.appendNewest(entry)
	cache.bytes += decodedBytes
	return decoded, nil
}

func (cache *imageDecodeCache) appendNewest(entry *imageDecodeCacheEntry) {
	entry.older = cache.newest
	entry.newer = nil
	if cache.newest != nil {
		cache.newest.newer = entry
	} else {
		cache.oldest = entry
	}
	cache.newest = entry
}

func (cache *imageDecodeCache) touch(entry *imageDecodeCacheEntry) {
	if entry == cache.newest {
		return
	}
	if entry.older != nil {
		entry.older.newer = entry.newer
	} else {
		cache.oldest = entry.newer
	}
	if entry.newer != nil {
		entry.newer.older = entry.older
	}
	cache.appendNewest(entry)
}

func (cache *imageDecodeCache) removeOldest() {
	entry := cache.oldest
	if entry == nil {
		return
	}
	delete(cache.entries, entry.key)
	cache.bytes -= uint64(len(entry.image.RGBA))
	cache.oldest = entry.newer
	if cache.oldest == nil {
		cache.newest = nil
	} else {
		cache.oldest.older = nil
	}
}

func cloneImage(image Image) Image {
	return Image{Width: image.Width, Height: image.Height, RGBA: append([]byte(nil), image.RGBA...)}
}

func (cache *imageDecodeCache) clear() {
	cache.mu.Lock()
	cache.generation++
	cache.entries = nil
	cache.oldest = nil
	cache.newest = nil
	cache.bytes = 0
	cache.mu.Unlock()
}
