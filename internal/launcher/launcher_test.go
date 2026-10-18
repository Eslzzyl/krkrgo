package launcher

import (
	"encoding/binary"
	"hash/adler32"
	"os"
	"path/filepath"
	"testing"
	"unicode/utf16"

	"github.com/eslzzyl/krkrgo/internal/assets"
)

func TestCatalogRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "launcher.json")
	c, err := LoadCatalog(path)
	if err != nil || len(c.Games) != 0 {
		t.Fatalf("empty catalog = %#v, %v", c, err)
	}
	c.Games = append(c.Games, Game{Title: "Game", Project: t.TempDir(), SaveDir: "/save", Budget: 42})
	if err := SaveCatalog(path, c); err != nil {
		t.Fatal(err)
	}
	got, err := LoadCatalog(path)
	if err != nil || len(got.Games) != 1 || got.Games[0].Title != "Game" || got.Games[0].Budget != 42 {
		t.Fatalf("loaded catalog = %#v, %v", got, err)
	}
}

func TestInspectProjectRootAndDataStartup(t *testing.T) {
	for _, location := range []string{".", "data"} {
		project := t.TempDir()
		dir := project
		if location == "data" {
			dir = filepath.Join(project, location)
			if err := os.Mkdir(dir, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(dir, "startup.tjs"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
		wantProject, err := filepath.EvalSymlinks(project)
		if err != nil {
			t.Fatal(err)
		}
		game, err := InspectProject(project)
		if err != nil || game.Project != wantProject || game.Title != filepath.Base(project) {
			t.Fatalf("InspectProject(%q) = %#v, %v", location, game, err)
		}
	}
}

func TestInspectProjectXP3Startup(t *testing.T) {
	project := t.TempDir()
	archive := filepath.Join(project, "data.xp3")
	if err := os.WriteFile(archive, testXP3Archive("startup.tjs", nil), 0o644); err != nil {
		t.Fatal(err)
	}
	wantProject, err := filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatal(err)
	}
	game, err := InspectProject(project)
	if err != nil || game.Project != wantProject || game.Title != filepath.Base(project) {
		t.Fatalf("InspectProject(XP3) = %#v, %v", game, err)
	}
}

func testXP3Archive(name string, contents []byte) []byte {
	archive := make([]byte, len(assets.Magic)+8)
	copy(archive, assets.Magic)
	dataOffset := uint64(len(archive))
	archive = append(archive, contents...)

	nameUnits := utf16.Encode([]rune(name))
	info := testXP3U32(nil, 0)
	info = testXP3U64(info, uint64(len(contents)))
	info = testXP3U64(info, uint64(len(contents)))
	info = testXP3U16(info, uint16(len(nameUnits)))
	for _, unit := range nameUnits {
		info = testXP3U16(info, unit)
	}
	entry := testXP3Chunk(nil, "info", info)

	segment := testXP3U32(nil, 0)
	segment = testXP3U64(segment, dataOffset)
	segment = testXP3U64(segment, uint64(len(contents)))
	segment = testXP3U64(segment, uint64(len(contents)))
	entry = testXP3Chunk(entry, "segm", segment)
	entry = testXP3Chunk(entry, "adlr", testXP3U32(nil, adler32.Checksum(contents)))
	index := testXP3Chunk(nil, "File", entry)

	indexOffset := uint64(len(archive))
	binary.LittleEndian.PutUint64(archive[len(assets.Magic):], indexOffset)
	archive = append(archive, 0)
	archive = testXP3U64(archive, uint64(len(index)))
	return append(archive, index...)
}

func testXP3Chunk(dst []byte, tag string, data []byte) []byte {
	dst = append(dst, tag...)
	dst = testXP3U64(dst, uint64(len(data)))
	return append(dst, data...)
}

func testXP3U16(dst []byte, value uint16) []byte {
	var data [2]byte
	binary.LittleEndian.PutUint16(data[:], value)
	return append(dst, data[:]...)
}

func testXP3U32(dst []byte, value uint32) []byte {
	var data [4]byte
	binary.LittleEndian.PutUint32(data[:], value)
	return append(dst, data[:]...)
}

func testXP3U64(dst []byte, value uint64) []byte {
	var data [8]byte
	binary.LittleEndian.PutUint64(data[:], value)
	return append(dst, data[:]...)
}
