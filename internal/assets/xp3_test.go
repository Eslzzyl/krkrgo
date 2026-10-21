package assets

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"unicode/utf16"

	"github.com/eslzzyl/krkrgo/internal/core"
)

func TestOpenArchiveSupportsBareAndMZEmbeddedXP3(t *testing.T) {
	contents := []byte("embedded XP3 payload")
	archiveBytes := xp3TestArchive("data/message.txt", contents)

	tests := []struct {
		name       string
		prefix     []byte
		baseOffset uint64
	}{
		{name: "bare", baseOffset: 0},
		{name: "MZ embedded", prefix: xp3TestMZPrefix(64), baseOffset: 64},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fileBytes := append(append([]byte(nil), test.prefix...), archiveBytes...)
			path := xp3TestWriteFile(t, fileBytes)
			archive, err := OpenArchive(path, core.DefaultLimits())
			if err != nil {
				t.Fatalf("OpenArchive() error = %v", err)
			}
			defer archive.Close()

			if archive.baseOffset != test.baseOffset {
				t.Fatalf("archive base offset = %d, want %d", archive.baseOffset, test.baseOffset)
			}
			got, err := archive.Read("data/message.txt")
			if err != nil {
				t.Fatalf("Read() error = %v", err)
			}
			if !bytes.Equal(got, contents) {
				t.Fatalf("Read() = %q, want %q", got, contents)
			}
			if err := archive.Verify("data/message.txt"); err != nil {
				t.Fatalf("Verify() error = %v", err)
			}
		})
	}
}

func TestOpenArchiveReportsNonArchiveSeparatelyFromMalformedXP3(t *testing.T) {
	path := xp3TestWriteFile(t, []byte("7z signature"))
	archive, err := OpenArchive(path, core.DefaultLimits())
	if archive != nil {
		archive.Close()
		t.Fatal("OpenArchive() returned an archive for a non-XP3 file")
	}
	if !errors.Is(err, ErrMalformed) || !errors.Is(err, ErrNotArchive) {
		t.Fatalf("OpenArchive() error = %v, want ErrMalformed and ErrNotArchive", err)
	}
}

func TestProtectedXP3EntryUsesFilterBeforeChecksum(t *testing.T) {
	plain := []byte("filter-mutated plaintext")
	ciphertext := append([]byte(nil), plain...)
	for i := range ciphertext {
		ciphertext[i] ^= 0x35
	}
	archive, err := OpenArchive(xp3TestWriteFile(t, xp3TestProtectedArchive("data/protected.bin", ciphertext, plain)), core.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()

	calls := 0
	archive.SetExtractionFilter(func(info ExtractionFilterInfo) error {
		calls++
		if info.FileHash != Adler32(plain) || info.FileName != "data/protected.bin" || info.Offset != 0 {
			t.Fatalf("filter info = %+v", info)
		}
		for i := range info.Buffer {
			info.Buffer[i] ^= 0x35
		}
		return nil
	})
	got, err := archive.Read("data/protected.bin")
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if !bytes.Equal(got, plain) || calls != 1 {
		t.Fatalf("Read() = %q, callback calls = %d; want %q and one call", got, calls, plain)
	}

	callbackErr := errors.New("filter failure")
	archive.SetExtractionFilter(func(ExtractionFilterInfo) error { return callbackErr })
	if _, err := archive.Read("data/protected.bin"); !errors.Is(err, callbackErr) {
		t.Fatalf("Read() error = %v, want filter error %v", err, callbackErr)
	}
}

func TestOpenStorageSkipsNonXP3FilesWithXP3Extension(t *testing.T) {
	project := t.TempDir()
	validPath := filepath.Join(project, "data.xp3")
	if err := os.WriteFile(validPath, xp3TestArchive("startup.tjs", []byte("startup")), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "patch.xp3"), []byte("7z signature"), 0o600); err != nil {
		t.Fatal(err)
	}
	storage, err := OpenStorage(project, core.DefaultLimits())
	if err != nil {
		t.Fatalf("OpenStorage() error = %v", err)
	}
	defer func() {
		for _, archive := range storage.Archives {
			_ = archive.Close()
		}
	}()
	if len(storage.Archives) != 1 {
		t.Fatalf("mounted archives = %d, want only data.xp3", len(storage.Archives))
	}
	got, err := storage.Read("startup.tjs")
	if err != nil || string(got) != "startup" {
		t.Fatalf("Read(startup.tjs) = %q, %v, want startup,nil", got, err)
	}
}

func TestOpenStorageAcceptsDirectXP3File(t *testing.T) {
	root := t.TempDir()
	archivePath := filepath.Join(root, "data.xp3")
	if err := os.WriteFile(archivePath, xp3TestArchive("startup.tjs", []byte("startup")), 0o600); err != nil {
		t.Fatal(err)
	}
	storage, err := OpenStorage(archivePath, core.DefaultLimits())
	if err != nil {
		t.Fatalf("OpenStorage() error = %v", err)
	}
	defer func() {
		for _, archive := range storage.Archives {
			_ = archive.Close()
		}
	}()
	wantProject, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if storage.Project != wantProject {
		t.Fatalf("storage project = %q, want %q", storage.Project, wantProject)
	}
	if got, err := storage.Read("startup.tjs"); err != nil || string(got) != "startup" {
		t.Fatalf("Read(startup.tjs) = %q, %v", got, err)
	}
}

func TestOpenArchiveRejectsMZWithoutAlignedXP3Marker(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{name: "no marker", data: append(xp3TestMZPrefix(64), bytes.Repeat([]byte{0x90}, 64)...)},
		{
			name: "unaligned marker",
			data: func() []byte {
				archive := xp3TestArchive("data/message.txt", []byte("payload"))
				data := xp3TestMZPrefix(17)
				return append(data, archive...)
			}(),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := xp3TestWriteFile(t, test.data)
			archive, err := OpenArchive(path, core.DefaultLimits())
			if archive != nil {
				archive.Close()
				t.Fatal("OpenArchive() returned an archive for an invalid MZ file")
			}
			if !errors.Is(err, ErrMalformed) || !errors.Is(err, ErrNotArchive) {
				t.Fatalf("OpenArchive() error = %v, want ErrMalformed and ErrNotArchive", err)
			}
		})
	}
}

func xp3TestArchive(name string, contents []byte) []byte {
	archive := make([]byte, len(MAGIC)+8)
	copy(archive, MAGIC)
	dataOffset := uint64(len(archive))
	archive = append(archive, contents...)

	nameUnits := utf16.Encode([]rune(name))
	info := xp3TestU32(nil, 0)
	info = xp3TestU64(info, uint64(len(contents)))
	info = xp3TestU64(info, uint64(len(contents)))
	info = xp3TestU16(info, uint16(len(nameUnits)))
	for _, unit := range nameUnits {
		info = xp3TestU16(info, unit)
	}
	entry := xp3TestChunk(nil, "info", info)

	segment := xp3TestU32(nil, 0)
	segment = xp3TestU64(segment, dataOffset)
	segment = xp3TestU64(segment, uint64(len(contents)))
	segment = xp3TestU64(segment, uint64(len(contents)))
	entry = xp3TestChunk(entry, "segm", segment)
	entry = xp3TestChunk(entry, "adlr", xp3TestU32(nil, Adler32(contents)))
	index := xp3TestChunk(nil, "File", entry)

	indexOffset := uint64(len(archive))
	binary.LittleEndian.PutUint64(archive[len(MAGIC):], indexOffset)
	archive = append(archive, 0)
	archive = xp3TestU64(archive, uint64(len(index)))
	archive = append(archive, index...)
	return archive
}

func xp3TestProtectedArchive(name string, ciphertext, plaintext []byte) []byte {
	archive := make([]byte, len(MAGIC)+8)
	copy(archive, MAGIC)
	dataOffset := uint64(len(archive))
	archive = append(archive, ciphertext...)

	nameUnits := utf16.Encode([]rune(name))
	info := xp3TestU32(nil, 0x80000000)
	info = xp3TestU64(info, uint64(len(plaintext)))
	info = xp3TestU64(info, uint64(len(ciphertext)))
	info = xp3TestU16(info, uint16(len(nameUnits)))
	for _, unit := range nameUnits {
		info = xp3TestU16(info, unit)
	}
	entry := xp3TestChunk(nil, "info", info)
	segment := xp3TestU32(nil, 0)
	segment = xp3TestU64(segment, dataOffset)
	segment = xp3TestU64(segment, uint64(len(plaintext)))
	segment = xp3TestU64(segment, uint64(len(ciphertext)))
	entry = xp3TestChunk(entry, "segm", segment)
	entry = xp3TestChunk(entry, "adlr", xp3TestU32(nil, Adler32(plaintext)))
	index := xp3TestChunk(nil, "File", entry)

	indexOffset := uint64(len(archive))
	binary.LittleEndian.PutUint64(archive[len(MAGIC):], indexOffset)
	archive = append(archive, 0)
	archive = xp3TestU64(archive, uint64(len(index)))
	archive = append(archive, index...)
	return archive
}

func xp3TestChunk(dst []byte, tag string, data []byte) []byte {
	dst = append(dst, tag...)
	dst = xp3TestU64(dst, uint64(len(data)))
	return append(dst, data...)
}

func xp3TestU16(dst []byte, value uint16) []byte {
	var data [2]byte
	binary.LittleEndian.PutUint16(data[:], value)
	return append(dst, data[:]...)
}

func xp3TestU32(dst []byte, value uint32) []byte {
	var data [4]byte
	binary.LittleEndian.PutUint32(data[:], value)
	return append(dst, data[:]...)
}

func TestReadRangeOwnsBytesReturnedFromSegmentCache(t *testing.T) {
	plain := []byte("cached segment")
	archive := &Archive{
		limits: core.Limits{CacheBytes: uint64(len(plain))},
		Entries: map[string]Entry{
			"data/file.bin": {
				Name:       "data/file.bin",
				Size:       uint64(len(plain)),
				PackedSize: uint64(len(plain)),
				Segments:   []Segment{{Compressed: true, Offset: 1, Size: uint64(len(plain)), PackedSize: uint64(len(plain))}},
			},
		},
		cache: map[segmentCacheKey]*cachedSegment{
			{offset: 1, size: uint64(len(plain)), packed: uint64(len(plain)), compressed: true}: {
				offset: 1, size: uint64(len(plain)), packed: uint64(len(plain)), compressed: true, bytes: plain,
			},
		},
		cacheBytes: uint64(len(plain)),
	}
	first, err := archive.Read("data/file.bin")
	if err != nil {
		t.Fatal(err)
	}
	first[0] = 'X'
	second, err := archive.Read("data/file.bin")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(second, plain) {
		t.Fatalf("mutating returned read changed cache: got %q, want %q", second, plain)
	}
}

func BenchmarkCachedSegmentRead(b *testing.B) {
	data := make([]byte, 1<<20)
	archive := &Archive{
		limits: core.Limits{CacheBytes: uint64(len(data))},
		cache: map[segmentCacheKey]*cachedSegment{
			{offset: 1, size: uint64(len(data)), packed: uint64(len(data)), compressed: true}: {
				offset: 1, size: uint64(len(data)), packed: uint64(len(data)), compressed: true, bytes: data,
			},
		},
		cacheBytes: uint64(len(data)),
	}
	segment := Segment{Offset: 1, Size: uint64(len(data)), PackedSize: uint64(len(data))}
	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for range b.N {
		if _, err := archive.segment(segment); err != nil {
			b.Fatal(err)
		}
	}
}

var benchmarkSegmentCacheByte byte

func BenchmarkCachedSegmentLargeLookup(b *testing.B) {
	const entries = 4096
	archive := &Archive{
		limits: core.Limits{CacheBytes: entries},
		cache:  make(map[segmentCacheKey]*cachedSegment, entries),
	}
	for i := 0; i < entries; i++ {
		segment := Segment{Offset: uint64(i), Size: 1, PackedSize: 1}
		entry := &cachedSegment{
			offset: segment.Offset, size: segment.Size, packed: segment.PackedSize,
			bytes: []byte{1},
		}
		archive.cache[segmentCacheKeyFor(segment)] = entry
		archive.appendCachedSegment(entry)
		archive.cacheBytes++
	}
	target := Segment{Offset: 0, Size: 1, PackedSize: 1}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		data, err := archive.segment(target)
		if err != nil {
			b.Fatal(err)
		}
		benchmarkSegmentCacheByte = data[0]
	}
}

func TestRawSegmentReadUsesBoundedCacheAndClear(t *testing.T) {
	contents := []byte("raw XP3 segment")
	path := xp3TestWriteFile(t, xp3TestArchive("data/raw.bin", contents))
	archive, err := OpenArchive(path, core.Limits{
		IndexBytes: 1 << 20, FileBytes: 1 << 20, CacheBytes: uint64(len(contents)), Entries: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	for range 2 {
		got, err := archive.Read("data/raw.bin")
		if err != nil || !bytes.Equal(got, contents) {
			t.Fatalf("Read() = %q, %v; want %q", got, err, contents)
		}
	}
	key := segmentCacheKeyFor(archive.Entries["data/raw.bin"].Segments[0])
	entry, ok := archive.cache[key]
	if len(archive.cache) != 1 || !ok || archive.cacheBytes != uint64(len(contents)) || entry.compressed {
		t.Fatalf("raw segment cache = %#v (%d bytes)", archive.cache, archive.cacheBytes)
	}
	archive.ClearCache()
	if len(archive.cache) != 0 || archive.cacheBytes != 0 {
		t.Fatalf("cache survived clear: segments=%d bytes=%d", len(archive.cache), archive.cacheBytes)
	}
}

func BenchmarkRepeatedRawSegmentRead(b *testing.B) {
	contents := bytes.Repeat([]byte("raw-data"), 1<<16)
	path := filepath.Join(b.TempDir(), "raw.xp3")
	if err := os.WriteFile(path, xp3TestArchive("data/raw.bin", contents), 0o600); err != nil {
		b.Fatal(err)
	}
	for _, benchmark := range []struct {
		name       string
		cacheBytes uint64
	}{
		{name: "cache_hit", cacheBytes: uint64(len(contents))},
		{name: "cache_disabled"},
	} {
		b.Run(benchmark.name, func(b *testing.B) {
			limits := core.DefaultLimits()
			limits.CacheBytes = benchmark.cacheBytes
			archive, err := OpenArchive(path, limits)
			if err != nil {
				b.Fatal(err)
			}
			defer archive.Close()
			b.ReportAllocs()
			b.SetBytes(int64(len(contents)))
			b.ResetTimer()
			for range b.N {
				if _, err := archive.Read("data/raw.bin"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
func xp3TestU64(dst []byte, value uint64) []byte {
	var data [8]byte
	binary.LittleEndian.PutUint64(data[:], value)
	return append(dst, data[:]...)
}

func xp3TestMZPrefix(length int) []byte {
	prefix := bytes.Repeat([]byte{0x90}, length)
	copy(prefix, []byte{'M', 'Z'})
	return prefix
}

func xp3TestWriteFile(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "archive.xp3")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write XP3 test file: %v", err)
	}
	return path
}
