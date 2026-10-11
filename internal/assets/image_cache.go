package assets

import (
	"crypto/sha256"
	"sync"
)

const maxDecodedImageCacheEntries = 4096

type imageDecodeCacheEntry struct {
	key   [sha256.Size]byte
	image Image
}

type imageDecodeCache struct {
	mu         sync.Mutex
	entries    []imageDecodeCacheEntry
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
	for i := range cache.entries {
		if cache.entries[i].key == key {
			entry := cache.entries[i]
			copy(cache.entries[i:], cache.entries[i+1:])
			cache.entries[len(cache.entries)-1] = entry
			cache.mu.Unlock()
			return cloneImage(entry.image), nil
		}
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
	for i := range cache.entries {
		if cache.entries[i].key == key {
			entry := cache.entries[i]
			copy(cache.entries[i:], cache.entries[i+1:])
			cache.entries[len(cache.entries)-1] = entry
			return cloneImage(entry.image), nil
		}
	}
	decodedBytes := uint64(len(decoded.RGBA))
	for len(cache.entries) > 0 && (len(cache.entries) >= maxDecodedImageCacheEntries || cache.bytes > s.limits.CacheBytes-decodedBytes) {
		cache.bytes -= uint64(len(cache.entries[0].image.RGBA))
		cache.entries = cache.entries[1:]
	}
	cache.entries = append(cache.entries, imageDecodeCacheEntry{key: key, image: cloneImage(decoded)})
	cache.bytes += uint64(len(decoded.RGBA))
	return decoded, nil
}

func cloneImage(image Image) Image {
	return Image{Width: image.Width, Height: image.Height, RGBA: append([]byte(nil), image.RGBA...)}
}

func (cache *imageDecodeCache) clear() {
	cache.mu.Lock()
	cache.generation++
	cache.entries = nil
	cache.bytes = 0
	cache.mu.Unlock()
}
