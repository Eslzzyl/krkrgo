package assets

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"fmt"
	hashadler "hash/adler32"
	"io"
	"os"
	"sync"
	"unicode/utf16"

	"github.com/eslzzyl/krkrgo/internal/core"
)

var (
	ErrMalformed = errors.New("malformed asset")
	ErrLimit     = errors.New("asset limit exceeded")
	ErrNotFound  = errors.New("asset not found")
)

var MAGIC = []byte{'X', 'P', '3', '\r', '\n', ' ', '\n', 0x1a, 0x8b, 0x67, 0x01}
var Magic = MAGIC

type Segment struct {
	Compressed bool
	Offset     uint64
	Size       uint64
	PackedSize uint64
}

type Entry struct {
	Name         string
	OriginalName string
	Encrypted    bool
	Size         uint64
	PackedSize   uint64
	Hash         uint32
	Segments     []Segment
}

type segmentCacheKey struct {
	offset, size, packed uint64
	compressed           bool
}

type cachedSegment struct {
	offset, size, packed uint64
	compressed           bool
	bytes                []byte
	older, newer         *cachedSegment
}

type Archive struct {
	Path    string
	Entries map[string]Entry

	file        *os.File
	length      uint64
	baseOffset  uint64
	limits      core.Limits
	mu          sync.Mutex
	cache       map[segmentCacheKey]*cachedSegment
	cacheOldest *cachedSegment
	cacheNewest *cachedSegment
	cacheBytes  uint64
}

func OpenArchive(path string, limits core.Limits) (*Archive, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	a := &Archive{Path: path, Entries: make(map[string]Entry), file: f, length: uint64(st.Size()), limits: limits}
	if limits.IndexBytes == 0 || limits.FileBytes == 0 || limits.Entries == 0 {
		f.Close()
		return nil, fmt.Errorf("%w: zero XP3 limit", ErrLimit)
	}
	if err := a.parse(); err != nil {
		f.Close()
		return nil, err
	}
	return a, nil
}

func OpenXP3(path string, limits core.Limits) (*Archive, error) { return OpenArchive(path, limits) }
func Open(path string, limits core.Limits) (*Archive, error)    { return OpenArchive(path, limits) }

func (a *Archive) Close() error {
	if a == nil || a.file == nil {
		return nil
	}
	return a.file.Close()
}

func (a *Archive) ClearCache() {
	a.mu.Lock()
	a.cache = nil
	a.cacheOldest = nil
	a.cacheNewest = nil
	a.cacheBytes = 0
	a.mu.Unlock()
}

func checkRange(offset, size, total uint64) error {
	if offset > total || size > total-offset {
		return fmt.Errorf("%w: range %#x+%#x exceeds %#x", ErrMalformed, offset, size, total)
	}
	return nil
}

func (a *Archive) readAt(offset, size uint64) ([]byte, error) {
	if err := checkRange(offset, size, a.length); err != nil {
		return nil, err
	}
	if offset > uint64(^uint64(0)>>1) || size > uint64(int(^uint(0)>>1)) {
		return nil, fmt.Errorf("%w: read too large", ErrLimit)
	}
	buf := make([]byte, int(size))
	if _, err := a.file.ReadAt(buf, int64(offset)); err != nil {
		return nil, err
	}
	return buf, nil
}

// readArchiveAt reads an archive-relative range, translating it to the file
// offset exactly once. Segment and index offsets remain archive-relative.
func (a *Archive) readArchiveAt(offset, size uint64) ([]byte, error) {
	if a.baseOffset > a.length {
		return nil, fmt.Errorf("%w: invalid XP3 archive base offset", ErrMalformed)
	}
	if err := checkRange(offset, size, a.length-a.baseOffset); err != nil {
		return nil, err
	}
	if a.baseOffset > ^uint64(0)-offset {
		return nil, fmt.Errorf("%w: XP3 file offset overflow", ErrMalformed)
	}
	return a.readAt(a.baseOffset+offset, size)
}

// findArchiveOffset locates a bare XP3 archive or one embedded at a paragraph
// boundary in an MZ executable. maxScan bounds the number of file bytes scanned.
func findArchiveOffset(file *os.File, length, maxScan uint64) (uint64, error) {
	var header [11]byte
	n, err := file.ReadAt(header[:], 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return 0, err
	}
	if n == len(header) && bytes.Equal(header[:], MAGIC) {
		return 0, nil
	}
	if n < 2 || header[0] != 'M' || header[1] != 'Z' {
		return 0, fmt.Errorf("%w: file is not an XP3 archive", ErrMalformed)
	}

	scanEnd := length
	if maxScan < scanEnd {
		scanEnd = maxScan
	}
	const scanChunkSize uint64 = 256 << 10
	const markerLen uint64 = 11
	if scanEnd < 16 || scanEnd-16 < markerLen {
		return 0, fmt.Errorf("%w: MZ executable contains no XP3 marker", ErrMalformed)
	}
	buffer := make([]byte, int(scanChunkSize)+len(MAGIC)-1)
	for chunkStart := uint64(16); chunkStart <= scanEnd-markerLen; {
		readSize := scanEnd - chunkStart
		if readSize > uint64(len(buffer)) {
			readSize = uint64(len(buffer))
		}
		read, err := file.ReadAt(buffer[:int(readSize)], int64(chunkStart))
		if err != nil && !errors.Is(err, io.EOF) {
			return 0, err
		}
		if uint64(read) != readSize {
			return 0, io.ErrUnexpectedEOF
		}
		for p := 0; p < int(scanChunkSize) && p+len(MAGIC) <= read; p += 16 {
			if bytes.Equal(buffer[p:p+len(MAGIC)], MAGIC) {
				return chunkStart + uint64(p), nil
			}
		}
		if scanEnd-chunkStart <= scanChunkSize {
			break
		}
		chunkStart += scanChunkSize
	}
	return 0, fmt.Errorf("%w: MZ executable contains no XP3 marker within scan limit", ErrMalformed)
}

func inflate(data []byte, size, limit uint64) ([]byte, error) {
	if size > limit {
		return nil, fmt.Errorf("%w: decompressed data exceeds limit (%d > %d)", ErrLimit, size, limit)
	}
	if size > uint64(int(^uint(0)>>1)-1) {
		return nil, fmt.Errorf("%w: decompressed data too large", ErrLimit)
	}
	br := bytes.NewReader(data)
	zr, err := zlib.NewReader(br)
	if err != nil {
		return nil, fmt.Errorf("%w: zlib: %v", ErrMalformed, err)
	}
	out, err := io.ReadAll(io.LimitReader(zr, int64(size)+1))
	if err != nil {
		zr.Close()
		return nil, fmt.Errorf("%w: zlib: %v", ErrMalformed, err)
	}
	if err = zr.Close(); err != nil {
		return nil, fmt.Errorf("%w: zlib: %v", ErrMalformed, err)
	}
	if uint64(len(out)) != size {
		return nil, fmt.Errorf("%w: decompressed size mismatch: expected %d, got %d", ErrMalformed, size, len(out))
	}
	if br.Len() != 0 {
		return nil, fmt.Errorf("%w: trailing compressed data", ErrMalformed)
	}
	return out, nil
}

func (a *Archive) parse() error {
	baseOffset, err := findArchiveOffset(a.file, a.length, a.limits.FileBytes)
	if err != nil {
		return err
	}
	a.baseOffset = baseOffset
	archiveLength := a.length - baseOffset

	b, err := a.readArchiveAt(uint64(len(MAGIC)), 8)
	if err != nil {
		return err
	}
	next := binary.LittleEndian.Uint64(b)
	seen := make(map[uint64]struct{})
	var totalIndex uint64
	for {
		if len(seen) >= 1024 {
			return fmt.Errorf("%w: cyclic or excessive XP3 index chain", ErrMalformed)
		}
		if _, ok := seen[next]; ok {
			return fmt.Errorf("%w: cyclic or excessive XP3 index chain", ErrMalformed)
		}
		seen[next] = struct{}{}
		h, err := a.readArchiveAt(next, 9)
		if err != nil {
			return err
		}
		flags := h[0]
		if flags&^byte(0x81) != 0 {
			return fmt.Errorf("%w: unsupported XP3 index flags %#x", ErrMalformed, flags)
		}
		packed := binary.LittleEndian.Uint64(h[1:])
		unpacked := packed
		if flags&1 != 0 {
			b, err := a.readArchiveAt(next+9, 8)
			if err != nil {
				return err
			}
			unpacked = binary.LittleEndian.Uint64(b)
		}
		if unpacked > ^uint64(0)-totalIndex {
			return fmt.Errorf("%w: index size overflow", ErrLimit)
		}
		totalIndex += unpacked
		if totalIndex > a.limits.IndexBytes || packed > a.limits.IndexBytes {
			return fmt.Errorf("%w: XP3 index exceeds limit", ErrLimit)
		}
		start := next + 9
		if flags&1 != 0 {
			start += 8
		}
		packedBytes, err := a.readArchiveAt(start, packed)
		if err != nil {
			return err
		}
		indexBytes := packedBytes
		if flags&1 != 0 {
			indexBytes, err = inflate(packedBytes, unpacked, a.limits.IndexBytes)
			if err != nil {
				return err
			}
		}
		r := NewReader(indexBytes)
		for !r.Done() {
			tag, data, err := r.Chunk()
			if err != nil {
				return fmt.Errorf("%w: XP3 index %#x: %v", ErrMalformed, next, err)
			}
			if string(tag[:]) != "File" {
				continue
			}
			entry, err := parseEntry(data, archiveLength, a.limits)
			if err != nil {
				return fmt.Errorf("%w: XP3 index %#x: %v", ErrMalformed, next, err)
			}
			if uint64(len(a.Entries)) >= a.limits.Entries {
				return fmt.Errorf("%w: too many XP3 entries", ErrLimit)
			}
			if _, ok := a.Entries[entry.Name]; ok {
				return fmt.Errorf("%w: duplicate XP3 entry %s", ErrMalformed, entry.Name)
			}
			a.Entries[entry.Name] = entry
		}
		if flags&0x80 == 0 {
			break
		}
		b, err = a.readArchiveAt(start+packed, 8)
		if err != nil {
			return err
		}
		next = binary.LittleEndian.Uint64(b)
	}
	return nil
}

func (a *Archive) Read(name string) ([]byte, error) {
	n, err := core.StorageName(name)
	if err != nil {
		return nil, err
	}
	e, ok := a.Entries[n]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, n)
	}
	return a.ReadRange(n, 0, e.Size)
}

func (a *Archive) ReadRange(name string, offset, size uint64) ([]byte, error) {
	n, err := core.StorageName(name)
	if err != nil {
		return nil, err
	}
	e, ok := a.Entries[n]
	if !ok {
		return nil, fmt.Errorf("%w: missing %s", ErrNotFound, n)
	}
	if err := checkRange(offset, size, e.Size); err != nil {
		return nil, err
	}
	if e.Encrypted {
		all, err := a.readEncodedRange(e, 0, e.Size)
		if err != nil {
			return nil, err
		}
		if hashadler.Checksum(all) != e.Hash {
			return nil, fmt.Errorf("%w: protected entry is not checksum-verified plaintext", ErrMalformed)
		}
		return append([]byte(nil), all[offset:offset+size]...), nil
	}
	return a.readEncodedRange(e, offset, size)
}

func (a *Archive) readEncodedRange(e Entry, offset, size uint64) ([]byte, error) {
	var sumSize, sumPacked uint64
	for _, s := range e.Segments {
		if s.Size > ^uint64(0)-sumSize || s.PackedSize > ^uint64(0)-sumPacked {
			return nil, fmt.Errorf("%w: segment size overflow", ErrLimit)
		}
		sumSize += s.Size
		sumPacked += s.PackedSize
	}
	if sumSize != e.Size || sumPacked != e.PackedSize {
		return nil, fmt.Errorf("%w: segment totals disagree with file sizes: %s", ErrMalformed, e.Name)
	}
	if err := checkRange(offset, size, e.Size); err != nil {
		return nil, err
	}
	if size > uint64(int(^uint(0)>>1)) {
		return nil, fmt.Errorf("%w: read too large", ErrLimit)
	}
	result := make([]byte, 0, int(size))
	end := offset + size
	var base uint64
	for _, s := range e.Segments {
		segmentEnd := base + s.Size
		if base < end && segmentEnd > offset {
			start := uint64(0)
			if offset > base {
				start = offset - base
			}
			stop := end - base
			if stop > s.Size {
				stop = s.Size
			}
			data, err := a.segment(s)
			if err != nil {
				return nil, err
			}
			result = append(result, data[int(start):int(stop)]...)
		}
		base = segmentEnd
	}
	if uint64(len(result)) != size {
		return nil, fmt.Errorf("%w: incomplete segmented read: %s", ErrMalformed, e.Name)
	}
	return result, nil
}

func segmentCacheKeyFor(s Segment) segmentCacheKey {
	return segmentCacheKey{offset: s.Offset, size: s.Size, packed: s.PackedSize, compressed: s.Compressed}
}

func (a *Archive) appendCachedSegment(entry *cachedSegment) {
	entry.older = a.cacheNewest
	entry.newer = nil
	if a.cacheNewest != nil {
		a.cacheNewest.newer = entry
	} else {
		a.cacheOldest = entry
	}
	a.cacheNewest = entry
}

func (a *Archive) touchCachedSegment(entry *cachedSegment) {
	if entry == a.cacheNewest {
		return
	}
	if entry.older != nil {
		entry.older.newer = entry.newer
	} else {
		a.cacheOldest = entry.newer
	}
	if entry.newer != nil {
		entry.newer.older = entry.older
	}
	a.appendCachedSegment(entry)
}

func (a *Archive) removeOldestCachedSegment() {
	entry := a.cacheOldest
	if entry == nil {
		return
	}
	delete(a.cache, segmentCacheKey{offset: entry.offset, size: entry.size, packed: entry.packed, compressed: entry.compressed})
	a.cacheBytes -= uint64(len(entry.bytes))
	a.cacheOldest = entry.newer
	if a.cacheOldest == nil {
		a.cacheNewest = nil
	} else {
		a.cacheOldest.older = nil
	}
}

func (a *Archive) segment(s Segment) ([]byte, error) {
	key := segmentCacheKeyFor(s)
	a.mu.Lock()
	if entry, ok := a.cache[key]; ok {
		a.touchCachedSegment(entry)
		a.mu.Unlock()
		// Segment bytes are immutable to callers; readEncodedRange only
		// appends from them into its separately owned result buffer.
		return entry.bytes, nil
	}
	a.mu.Unlock()
	var data []byte
	var err error
	if s.Compressed {
		var packed []byte
		packed, err = a.readArchiveAt(s.Offset, s.PackedSize)
		if err == nil {
			data, err = inflate(packed, s.Size, a.limits.FileBytes)
		}
	} else {
		data, err = a.readArchiveAt(s.Offset, s.Size)
	}
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if entry, ok := a.cache[key]; ok {
		a.touchCachedSegment(entry)
		return entry.bytes, nil
	}
	if uint64(len(data)) <= a.limits.CacheBytes {
		for a.cacheBytes+uint64(len(data)) > a.limits.CacheBytes && len(a.cache) > 0 {
			a.removeOldestCachedSegment()
		}
		if a.cache == nil {
			a.cache = make(map[segmentCacheKey]*cachedSegment)
		}
		entry := &cachedSegment{
			offset: s.Offset, size: s.Size, packed: s.PackedSize,
			compressed: s.Compressed, bytes: data,
		}
		a.cache[key] = entry
		a.appendCachedSegment(entry)
		a.cacheBytes += uint64(len(data))
	}
	return data, nil
}

func (a *Archive) Verify(name string) error {
	n, err := core.StorageName(name)
	if err != nil {
		return err
	}
	data, err := a.Read(n)
	if err != nil {
		return err
	}
	e, ok := a.Entries[n]
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, n)
	}
	if hashadler.Checksum(data) != e.Hash {
		return fmt.Errorf("%w: Adler-32 mismatch for %s", ErrMalformed, n)
	}
	return nil
}

func parseEntry(data []byte, length uint64, limits core.Limits) (Entry, error) {
	r := NewReader(data)
	var info bool
	var name string
	var originalName string
	var encrypted bool
	var size, packedSize uint64
	var segments []Segment
	var haveSegments, haveHash bool
	var hash uint32
	for !r.Done() {
		tag, chunk, err := r.Chunk()
		if err != nil {
			return Entry{}, err
		}
		c := NewReader(chunk)
		switch string(tag[:]) {
		case "info":
			if info {
				return Entry{}, fmt.Errorf("duplicate info chunk")
			}
			info = true
			flags, err := c.U32()
			if err != nil {
				return Entry{}, err
			}
			if flags&^uint32(0x80000000) != 0 {
				return Entry{}, fmt.Errorf("unsupported XP3 file flags %#x", flags)
			}
			encrypted = flags != 0
			size, err = c.U64()
			if err != nil {
				return Entry{}, err
			}
			packedSize, err = c.U64()
			if err != nil {
				return Entry{}, err
			}
			if size > limits.FileBytes || packedSize > limits.FileBytes {
				return Entry{}, fmt.Errorf("%w: XP3 file exceeds limit", ErrLimit)
			}
			count, err := c.U16()
			if err != nil {
				return Entry{}, err
			}
			if uint64(count)*2 > uint64(len(chunk)-c.Pos) {
				return Entry{}, fmt.Errorf("truncated UTF-16 name")
			}
			units := make([]uint16, count)
			for i := range units {
				units[i], err = c.U16()
				if err != nil {
					return Entry{}, err
				}
			}
			raw := utf16.Decode(units)
			originalName = string(raw)
			name, err = core.StorageName(originalName)
			if err != nil {
				return Entry{}, err
			}
		case "segm":
			if haveSegments || len(chunk)%28 != 0 {
				return Entry{}, fmt.Errorf("invalid segment table")
			}
			haveSegments = true
			segments = make([]Segment, 0, len(chunk)/28)
			for !c.Done() {
				flags, err := c.U32()
				if err != nil {
					return Entry{}, err
				}
				if flags > 1 {
					return Entry{}, fmt.Errorf("unsupported segment flags %#x", flags)
				}
				s := Segment{Compressed: flags != 0}
				s.Offset, err = c.U64()
				if err != nil {
					return Entry{}, err
				}
				s.Size, err = c.U64()
				if err != nil {
					return Entry{}, err
				}
				s.PackedSize, err = c.U64()
				if err != nil {
					return Entry{}, err
				}
				if err = checkRange(s.Offset, s.PackedSize, length); err != nil {
					return Entry{}, err
				}
				if s.Size > limits.FileBytes || s.PackedSize > limits.FileBytes {
					return Entry{}, fmt.Errorf("%w: segment exceeds limit", ErrLimit)
				}
				if !s.Compressed && s.Size != s.PackedSize {
					return Entry{}, fmt.Errorf("raw segment size mismatch")
				}
				segments = append(segments, s)
			}
		case "adlr":
			if haveHash || len(chunk) != 4 {
				return Entry{}, fmt.Errorf("invalid checksum chunk")
			}
			haveHash = true
			hash, err = c.U32()
			if err != nil {
				return Entry{}, err
			}
		}
	}
	if !info {
		return Entry{}, fmt.Errorf("missing info chunk")
	}
	if !haveSegments {
		return Entry{}, fmt.Errorf("missing segment table")
	}
	if !haveHash {
		return Entry{}, fmt.Errorf("missing checksum")
	}
	var ss, sp uint64
	for _, s := range segments {
		if s.Size > ^uint64(0)-ss || s.PackedSize > ^uint64(0)-sp {
			return Entry{}, fmt.Errorf("%w: segment size overflow", ErrLimit)
		}
		ss += s.Size
		sp += s.PackedSize
	}
	if ss > limits.FileBytes || sp > limits.FileBytes {
		return Entry{}, fmt.Errorf("%w: segment totals exceed limit", ErrLimit)
	}
	return Entry{Name: name, OriginalName: originalName, Encrypted: encrypted, Size: size, PackedSize: packedSize, Hash: hash, Segments: segments}, nil
}

func Adler32(data []byte) uint32 { return hashadler.Checksum(data) }
func adler32(data []byte) uint32 { return hashadler.Checksum(data) }
