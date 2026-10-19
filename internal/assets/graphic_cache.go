package assets

import "sync"

const maxGraphicCacheEntries = 4096

// GraphicCacheKey identifies one fully processed graphic request.
type GraphicCacheKey struct {
	Name     string
	ColorKey uint32
	Mode     uint8
	Width    uint32
	Height   uint32
}

// GraphicCacheEntry stores the result after image, mask, province, and color-key processing.
type GraphicCacheEntry struct {
	Width    uint32
	Height   uint32
	RGBA     []byte
	Province []byte
	Metadata map[string]string
}

type graphicCacheItem struct {
	key   GraphicCacheKey
	value GraphicCacheEntry
	older *graphicCacheItem
	newer *graphicCacheItem
}

type graphicCache struct {
	mu      sync.Mutex
	entries map[GraphicCacheKey]*graphicCacheItem
	oldest  *graphicCacheItem
	newest  *graphicCacheItem
	bytes   uint64
}

func (s *Storage) GraphicCacheGet(key GraphicCacheKey) (GraphicCacheEntry, bool) {
	if s == nil || s.limits.CacheBytes == 0 {
		return GraphicCacheEntry{}, false
	}
	cache := &s.graphicCache
	cache.mu.Lock()
	item, ok := cache.entries[key]
	if !ok {
		cache.mu.Unlock()
		return GraphicCacheEntry{}, false
	}
	cache.touch(item)
	value := cloneGraphicCacheEntry(item.value)
	cache.mu.Unlock()
	return value, true
}

func (s *Storage) GraphicCachePut(key GraphicCacheKey, value GraphicCacheEntry) {
	if s == nil || s.limits.CacheBytes == 0 {
		return
	}
	value = cloneGraphicCacheEntry(value)
	valueBytes := graphicCacheEntryBytes(value)
	if valueBytes > s.limits.CacheBytes {
		return
	}
	cache := &s.graphicCache
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.entries == nil {
		cache.entries = make(map[GraphicCacheKey]*graphicCacheItem)
	}
	if old, ok := cache.entries[key]; ok {
		cache.unlink(old)
		delete(cache.entries, key)
		cache.bytes -= graphicCacheEntryBytes(old.value)
	}
	for len(cache.entries) >= maxGraphicCacheEntries || cache.bytes > s.limits.CacheBytes-valueBytes {
		cache.removeOldest()
	}
	item := &graphicCacheItem{key: key, value: value}
	cache.entries[key] = item
	cache.appendNewest(item)
	cache.bytes += valueBytes
}

func (cache *graphicCache) appendNewest(item *graphicCacheItem) {
	item.older = cache.newest
	item.newer = nil
	if cache.newest != nil {
		cache.newest.newer = item
	} else {
		cache.oldest = item
	}
	cache.newest = item
}

func (cache *graphicCache) touch(item *graphicCacheItem) {
	if item == cache.newest {
		return
	}
	if item.older != nil {
		item.older.newer = item.newer
	} else {
		cache.oldest = item.newer
	}
	if item.newer != nil {
		item.newer.older = item.older
	}
	cache.appendNewest(item)
}

func (cache *graphicCache) unlink(item *graphicCacheItem) {
	if item.older != nil {
		item.older.newer = item.newer
	} else {
		cache.oldest = item.newer
	}
	if item.newer != nil {
		item.newer.older = item.older
	} else {
		cache.newest = item.older
	}
	item.older = nil
	item.newer = nil
}

func (cache *graphicCache) removeOldest() {
	item := cache.oldest
	if item == nil {
		return
	}
	cache.unlink(item)
	delete(cache.entries, item.key)
	cache.bytes -= graphicCacheEntryBytes(item.value)
}

func (cache *graphicCache) clear() {
	cache.mu.Lock()
	cache.entries = nil
	cache.oldest = nil
	cache.newest = nil
	cache.bytes = 0
	cache.mu.Unlock()
}

func cloneGraphicCacheEntry(value GraphicCacheEntry) GraphicCacheEntry {
	metadata := make(map[string]string, len(value.Metadata))
	for key, item := range value.Metadata {
		metadata[key] = item
	}
	return GraphicCacheEntry{
		Width:    value.Width,
		Height:   value.Height,
		RGBA:     append([]byte(nil), value.RGBA...),
		Province: append([]byte(nil), value.Province...),
		Metadata: metadata,
	}
}

func graphicCacheEntryBytes(value GraphicCacheEntry) uint64 {
	size := uint64(len(value.RGBA) + len(value.Province))
	for key, item := range value.Metadata {
		size += uint64(len(key) + len(item))
	}
	return size
}
