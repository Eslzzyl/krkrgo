package runtime

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/eslzzyl/krkrgo/internal/assets"
	"github.com/eslzzyl/krkrgo/internal/core"
	"github.com/eslzzyl/krkrgo/internal/tjs"
)

func TestStoragesAddArchiveThroughLegacyGlobalProxy(t *testing.T) {
	session := NewSession(DefaultServices(), 50_000)
	defer session.Close()

	globalObject, ok := session.VM.Objects.Lookup(0)
	if !ok {
		t.Fatal("TJS global object is missing")
	}
	storages := session.VM.Globals["Storages"]
	if err := globalObject.Set("Storages", storages); err != nil {
		t.Fatal(err)
	}
	bytecode := &tjs.DecodedBytecode{Objects: []tjs.BytecodeObject{{
		MaxVariables: 4,
		Code: []int16{
			103, 1, -2, 0, // GPD r1, this-proxy, Storages
			103, 2, 1, 1, // GPD r2, r1, addArchive
			1, 3, 2, // CONST r3, "missing.xp3"
			99, 0, 2, 1, 3, // CALL (discarded result), r2, 1 arg (r3)
			119,
		},
		Constants: []tjs.BytecodeConstant{
			{Value: tjs.StringValue("Storages")},
			{Value: tjs.StringValue("addArchive")},
			{Value: tjs.StringValue("missing.xp3")},
		},
	}}}
	global := tjs.ObjectValue(tjs.NewObjectRef(0, 1))
	invoke := func(function, receiver tjs.Value, args []tjs.Value, resultNeeded bool) (tjs.Value, error) {
		return session.VM.InvokeLegacy(function, receiver, args, session.Services, &session.Budget, resultNeeded)
	}
	if _, err := bytecode.ExecuteLegacyWithContextAndConstruction(0, global, global, nil, invoke, nil, session.VM.Objects); !errors.Is(err, ErrUnsupportedDevice) {
		t.Fatalf("legacy Storages.addArchive error = %v, want ErrUnsupportedDevice", err)
	}
}

func TestStoragesStaticPathHelpersWithoutStorage(t *testing.T) {
	session := NewSession(DefaultServices(), 50_000)
	defer session.Close()

	for _, test := range []struct {
		method string
		name   string
		want   string
	}{
		{"extractStorageExt", "dir/file.tar.gz", ".gz"},
		{"extractStorageExt", "dir/.profile", ".profile"},
		{"extractStorageExt", "archive.xp3>dir/file", ""},
		{"extractStorageExt", "archive.xp3>dir/file.TJS", ".TJS"},
		{"extractStorageName", "archive.xp3>dir/File.TJS", "File.TJS"},
		{"extractStoragePath", "archive.xp3>dir/File.TJS", "archive.xp3>dir/"},
		{"chopStorageExt", "archive.xp3>dir/File.TJS", "archive.xp3>dir/File"},
		{"chopStorageExt", "dir/no-extension", "dir/no-extension"},
		{"chopStorageExt", "dir/.profile", "dir/"},
	} {
		value, err := session.Evaluate("Storages." + test.method + "('" + test.name + "')")
		if err != nil {
			t.Fatalf("%s(%q): %v", test.method, test.name, err)
		}
		if got := value.Text(); got != test.want {
			t.Errorf("%s(%q) = %q, want %q", test.method, test.name, got, test.want)
		}
	}

	for _, source := range []string{
		"Storages.addAutoPath('path/')",
		"Storages.removeAutoPath('path/')",
		"Storages.getFullPath('path/file.tjs')",
		"Storages.getPlacedPath('path/file.tjs')",
		"Storages.getLocalName('path/file.tjs')",
		"Storages.isExistentStorage('path/file.tjs')",
		"Storages.fstat('path/file.tjs')",
		"Storages.addArchive('path.xp3')",
		"Storages.clearArchiveCache()",
	} {

		if _, err := session.Evaluate(source); !errors.Is(err, ErrUnsupportedDevice) {
			t.Errorf("%s error = %v, want ErrUnsupportedDevice", source, err)
		}
	}
	if _, err := session.Evaluate("Storages.addArchive()"); err == nil {
		t.Fatal("Storages.addArchive without an argument unexpectedly succeeded")
	}

	if _, err := session.Evaluate("new Storages()"); err == nil {
		t.Fatal("Storages unexpectedly allowed instance construction")
	}
}

func TestStoragesStorageBackedOperations(t *testing.T) {
	project := t.TempDir()
	searchDirs := []string{filepath.Join(project, "lookup"), filepath.Join(project, "alternate")}
	searchContents := [][]byte{[]byte("first search path"), []byte("second search path")}
	for i, dir := range searchDirs {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "Found.TJS"), searchContents[i], 0o644); err != nil {
			t.Fatal(err)
		}
	}
	archivePath := filepath.Join(project, "Mounted.XP3")
	archiveContents := []byte("archive resource")
	if err := os.WriteFile(archivePath, storagesTestXP3("archive/Entry.TJS", archiveContents), 0o644); err != nil {
		t.Fatal(err)
	}
	storage, err := assets.OpenStorage(project, core.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, archive := range storage.Archives {
			if err := archive.Close(); err != nil {
				t.Errorf("close mounted archive: %v", err)
			}
		}
	}()
	session := NewSession(NewServices(storage, "", core.DefaultLimits()), 50_000)
	defer session.Close()

	wantFile := expectedFileStorageURI(filepath.Join(storage.Project, "lookup", "found.tjs"))
	wantArchive := expectedFileStorageURI(filepath.Join(storage.Project, "archive.xp3")) + ">file.tjs"
	wantArchiveMember := expectedFileStorageURI(filepath.Join(storage.Project, "mounted.xp3")) + ">archive/entry.tjs"
	absoluteProjectPath := filepath.ToSlash(storage.Project) + "/LOOKUP/../LOOKUP/FOUND.TJS"
	fileURIInput := "file://." + filepath.ToSlash(storage.Project) + "/LOOKUP/../LOOKUP/FOUND.TJS"
	for _, test := range []struct {
		source string
		want   string
	}{
		{"Storages.getLocalName('lookup/Found.TJS')", filepath.Join(storage.Project, "lookup", "Found.TJS")},
		{"Storages.getFullPath('LOOKUP/../LOOKUP/FOUND.TJS')", wantFile},
		{"Storages.getFullPath('LOOKUP/./FOUND.TJS')", wantFile},
		{"Storages.getFullPath(" + strconv.Quote(`LOOKUP\FOUND.TJS`) + ")", wantFile},
		{"Storages.getFullPath('LOOKUP/..')", expectedFileStorageURI(storage.Project + string(os.PathSeparator))},
		{"Storages.getFullPath('.')", expectedFileStorageURI(storage.Project + string(os.PathSeparator))},
		{"Storages.getFullPath('archive.xp3>Dir/../File.TJS')", wantArchive},
		{"Storages.getFullPath(" + strconv.Quote(absoluteProjectPath) + ")", wantFile},
		{"Storages.getFullPath(" + strconv.Quote(fileURIInput) + ")", wantFile},
		{"Storages.getFullPath('file:')", "file://./"},
		{"Storages.getFullPath('')", ""},
	} {
		value, err := session.Evaluate(test.source)
		if err != nil {
			t.Fatalf("%s: %v", test.source, err)
		}
		if got := value.Text(); got != test.want {
			t.Errorf("%s = %q, want %q", test.source, got, test.want)
		}
	}

	for _, test := range []struct {
		method string
		name   string
		want   string
	}{
		{"getFullPath", "http://example.test/file.tjs", "unsupported storage media: http"},
		{"getPlacedPath", "https://example.test/file.tjs", "unsupported storage media: https"},
		{"getLocalName", "https://example.test/file.tjs", "unsupported storage media: https"},
	} {
		source := "Storages." + test.method + "(" + strconv.Quote(test.name) + ")"
		if _, err := session.Evaluate(source); err == nil || !errors.Is(err, assets.ErrUnsupportedStorageMedia) {
			t.Errorf("%s error = %v, want ErrUnsupportedStorageMedia", source, err)
		}
	}
	if _, err := session.Evaluate(`Storages.getLocalName("Mounted.XP3>archive/Entry.TJS")`); err == nil {
		t.Fatal("archive member unexpectedly had a local filesystem name")
	}
	if _, err := session.Evaluate(`Storages.getFullPath("file://remote/share/file.tjs")`); err == nil {
		t.Error("non-current file domain unexpectedly succeeded")
	}

	if _, err := session.Evaluate("Storages.addAutoPath('lookup/')"); err != nil {
		t.Fatal(err)
	}

	if _, err := session.executeSource("<storages-fstat-test>", `looseInfo = Storages.fstat(Storages.getPlacedPath("lookup/Found.TJS")); archiveInfo = Storages.fstat(Storages.getPlacedPath("Mounted.XP3>archive/Entry.TJS"));`, false); err != nil {
		t.Fatal(err)
	}
	value, err := session.Evaluate(`looseInfo.size == ` + strconv.Itoa(len(searchContents[0])) + ` && archiveInfo.size == ` + strconv.Itoa(len(archiveContents)))
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := value.Truth(); !ok {
		detail, _ := session.Evaluate(`[looseInfo.size, archiveInfo.size].join("|")`)
		t.Fatalf("Storages.fstat sizes = %v, want loose %d and archive member %d (%v)", value, len(searchContents[0]), len(archiveContents), detail)
	}
	checkPlacedPath := func(source, want string) {
		t.Helper()
		value, err := session.Evaluate(source)
		if err != nil {
			t.Fatalf("%s: %v", source, err)
		}
		if got := value.Text(); got != want {
			t.Errorf("%s = %q, want %q", source, got, want)
		}
	}
	checkPlacedPath("Storages.getPlacedPath("+strconv.Quote(absoluteProjectPath)+")", wantFile)
	checkPlacedPath("Storages.getPlacedPath("+strconv.Quote(fileURIInput)+")", wantFile)
	checkPlacedPath("Storages.getPlacedPath('found.tjs')", wantFile)
	checkPlacedPath("Storages.getPlacedPath('MOUNTED.XP3>ARCHIVE/ENTRY.TJS')", wantArchiveMember)
	if data, err := storage.Read("MOUNTED.XP3>ARCHIVE/ENTRY.TJS"); err != nil || string(data) != string(archiveContents) {
		t.Errorf("Storage.Read archive member = %q, %v", data, err)
	}
	if value, err := session.Evaluate("Storages.isExistentStorage('found.tjs')"); err != nil {
		t.Fatal(err)
	} else if got, _ := value.Int(); got != 1 {
		t.Errorf("existing storage result = %d, want 1", got)
	}
	if data, err := storage.Read("found.tjs"); err != nil || string(data) != string(searchContents[0]) {
		t.Errorf("Storage.Read after first auto path = %q, %v", data, err)
	}
	if data, err := storage.Read(fileURIInput); err != nil || string(data) != string(searchContents[0]) {
		t.Errorf("Storage.Read through project file URI = %q, %v", data, err)
	}

	if _, err := session.Evaluate("Storages.addAutoPath('alternate/')"); err != nil {
		t.Fatal(err)
	}
	checkPlacedPath("Storages.getPlacedPath('found.tjs')", expectedFileStorageURI(filepath.Join(storage.Project, "alternate", "found.tjs")))
	if data, err := storage.Read("found.tjs"); err != nil || string(data) != string(searchContents[1]) {
		t.Errorf("Storage.Read after second auto path = %q, %v", data, err)
	}
	if _, err := session.Evaluate("Storages.removeAutoPath('alternate/')"); err != nil {
		t.Fatal(err)
	}
	checkPlacedPath("Storages.getPlacedPath('found.tjs')", wantFile)
	if value, err := session.Evaluate("Storages.getPlacedPath('missing.tjs')"); err != nil {
		t.Fatal(err)
	} else if got := value.Text(); got != "" {
		t.Errorf("missing placed path = %q, want empty string", got)
	}
	checkPlacedPath("Storages.getPlacedPath('')", "")
	if value, err := session.Evaluate("Storages.isExistentStorage('missing.tjs')"); err != nil {
		t.Fatal(err)
	} else if got, _ := value.Int(); got != 0 {
		t.Errorf("missing storage result = %d, want 0", got)
	}
	if value, err := session.Evaluate("Storages.isExistentStorage('')"); err != nil {
		t.Fatal(err)
	} else if got, _ := value.Int(); got != 0 {
		t.Errorf("empty storage existence result = %d, want 0", got)
	}
	if _, err := session.Evaluate("Storages.removeAutoPath('lookup/')"); err != nil {
		t.Fatal(err)
	}
	if value, err := session.Evaluate("Storages.isExistentStorage('found.tjs')"); err != nil {
		t.Fatal(err)
	} else if got, _ := value.Int(); got != 0 {
		t.Errorf("storage after removing auto path = %d, want 0", got)
	}
	if _, err := session.Evaluate("Storages.addAutoPath('lookup')"); err == nil {
		t.Fatal("addAutoPath accepted a path without a trailing delimiter")
	}
	if _, err := session.Evaluate("Storages.clearArchiveCache()"); err != nil {
		t.Fatal(err)
	}
}

func TestStoragesAddArchiveMountsProjectArchive(t *testing.T) {
	project := t.TempDir()
	storage, err := assets.OpenStorage(project, core.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	session := NewSession(NewServices(storage, "", core.DefaultLimits()), 50_000)
	defer session.Close()
	defer func() {
		for _, archive := range storage.Archives {
			if err := archive.Close(); err != nil {
				t.Errorf("close mounted archive: %v", err)
			}
		}
	}()

	archivePath := filepath.Join(project, "dynamic.xp3")
	if err := os.WriteFile(archivePath, storagesTestXP3("nested/entry.tjs", []byte("mounted")), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Evaluate(`Storages.addArchive("dynamic.xp3")`); err != nil {
		t.Fatalf("Storages.addArchive: %v", err)
	}
	if got, err := storage.Read("dynamic.xp3>nested/entry.tjs"); err != nil || string(got) != "mounted" {
		t.Errorf("mounted archive read = %q, %v; want %q", got, err, "mounted")
	}
	if got, err := session.Evaluate(`Storages.isExistentStorage("dynamic.xp3>nested/entry.tjs")`); err != nil {
		t.Fatal(err)
	} else if value, _ := got.Int(); value != 1 {
		t.Errorf("mounted archive existence = %d, want 1", value)
	}
	if _, err := session.Evaluate(`Storages.addArchive("dynamic.xp3>nested/entry.tjs")`); err == nil {
		t.Error("Storages.addArchive accepted an archive member as an archive path")
	}
	if _, err := session.Evaluate(`Storages.addArchive("missing.xp3")`); err == nil {
		t.Error("Storages.addArchive accepted a missing archive")
	}
}

func expectedFileStorageURI(input string) string {
	normalized := []byte(filepath.ToSlash(input))
	for i, c := range normalized {
		if c >= 'A' && c <= 'Z' {
			normalized[i] = c + ('a' - 'A')
		}
	}
	return "file://." + string(normalized)
}

func storagesTestXP3(name string, contents []byte) []byte {
	archive := make([]byte, len(assets.MAGIC)+8)
	copy(archive, assets.MAGIC)
	dataOffset := uint64(len(archive))
	archive = append(archive, contents...)

	nameUnits := utf16.Encode([]rune(name))
	info := storagesTestU32(nil, 0)
	info = storagesTestU64(info, uint64(len(contents)))
	info = storagesTestU64(info, uint64(len(contents)))
	info = storagesTestU16(info, uint16(len(nameUnits)))
	for _, unit := range nameUnits {
		info = storagesTestU16(info, unit)
	}
	entry := storagesTestChunk(nil, "info", info)

	segment := storagesTestU32(nil, 0)
	segment = storagesTestU64(segment, dataOffset)
	segment = storagesTestU64(segment, uint64(len(contents)))
	segment = storagesTestU64(segment, uint64(len(contents)))
	entry = storagesTestChunk(entry, "segm", segment)
	entry = storagesTestChunk(entry, "adlr", storagesTestU32(nil, assets.Adler32(contents)))
	index := storagesTestChunk(nil, "File", entry)

	indexOffset := uint64(len(archive))
	binary.LittleEndian.PutUint64(archive[len(assets.MAGIC):], indexOffset)
	archive = append(archive, 0)
	archive = storagesTestU64(archive, uint64(len(index)))
	return append(archive, index...)
}

func storagesTestChunk(dst []byte, tag string, data []byte) []byte {
	dst = append(dst, tag...)
	dst = storagesTestU64(dst, uint64(len(data)))
	return append(dst, data...)
}

func storagesTestU16(dst []byte, value uint16) []byte {
	var data [2]byte
	binary.LittleEndian.PutUint16(data[:], value)
	return append(dst, data[:]...)
}

func storagesTestU32(dst []byte, value uint32) []byte {
	var data [4]byte
	binary.LittleEndian.PutUint32(data[:], value)
	return append(dst, data[:]...)
}

func storagesTestU64(dst []byte, value uint64) []byte {
	var data [8]byte
	binary.LittleEndian.PutUint64(data[:], value)
	return append(dst, data[:]...)
}

func TestStoragesPathHelpersRecognizeBackslashDelimiter(t *testing.T) {
	if got := extractStorageExt(`folder\\name.TJS`); got != ".TJS" {
		t.Errorf("extension = %q, want .TJS", got)
	}
	if got := extractStorageName(`archive.xp3>folder\\name.TJS`); got != "name.TJS" {
		t.Errorf("name = %q, want name.TJS", got)
	}
	if got := extractStoragePath(`archive.xp3>folder\\name.TJS`); got != `archive.xp3>folder\\` {
		t.Errorf("path = %q, want archive.xp3>folder\\", got)
	}
	if got := chopStorageExt(`folder\\name.TJS`); got != `folder\\name` {
		t.Errorf("chopped path = %q, want folder\\name", got)
	}
}
func TestStoragesAutoPathFindsIslandDiaryArchiveConfig(t *testing.T) {
	project := filepath.Join("..", "..", "testdata", "islanddiary")
	storage, err := assets.OpenStorage(project, core.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, archive := range storage.Archives {
			if err := archive.Close(); err != nil {
				t.Errorf("close archive: %v", err)
			}
		}
	}()
	session := NewSession(NewServices(storage, "", core.DefaultLimits()), 50_000)
	defer session.Close()
	if _, err := session.Evaluate(`Storages.addAutoPath("main/")`); err != nil {
		t.Fatal(err)
	}
	placed, err := session.Evaluate(`Storages.getPlacedPath("Config.tjs")`)
	if err != nil {
		t.Fatal(err)
	}
	if got := placed.Text(); !strings.HasSuffix(got, ">main/config.tjs") {
		t.Fatalf("Storages.getPlacedPath(Config.tjs) = %q, want archive member >main/config.tjs", got)
	}
	if _, err := storage.Read("Config.tjs"); err != nil {
		t.Fatalf("read archive config through auto path: %v", err)
	}
}
