package runtime

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/eslzzyl/krkrgo/internal/assets"
	"github.com/eslzzyl/krkrgo/internal/core"
	"github.com/eslzzyl/krkrgo/internal/tjs"
)

// registerStoragesFstatMethods exposes the fstat.dll additions on the Storages
// class. The session registration table owns the shipped list; registering the
// same operations here keeps the behavior test independent of that list.
func registerStoragesFstatMethods(t *testing.T, session *Session) {
	t.Helper()
	storages, ok := session.VM.Globals["Storages"]
	if !ok {
		t.Fatal("Storages class is not registered")
	}
	for _, method := range []string{
		"clearStorageCaches", "getTime", "setTime", "getLastModifiedFileTime",
		"setLastModifiedFileTime", "exportFile", "deleteFile", "truncateFile",
		"moveFile", "dirtree", "removeDirectory", "createDirectory",
		"createDirectoryNoNormalize", "changeDirectory", "setFileAttributes",
		"resetFileAttributes", "getFileAttributes", "selectDirectory",
		"isExistentDirectory", "copyFile", "copyFileNoNormalize",
		"isExistentStorageNoSearchNoNormalize", "getDisplayName", "searchPath",
	} {
		if err := session.VM.RegisterNativeStaticMethod(storages, method, "Storages."+method); err != nil {
			t.Fatalf("register Storages.%s: %v", method, err)
		}
	}
}

func fstatTestSession(t *testing.T, project, saveDir string) *Session {
	t.Helper()
	storage, err := assets.OpenStorage(project, core.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	closeArchivesOnCleanup(t, storage)
	session := NewSession(NewServices(storage, saveDir, core.DefaultLimits()), 50_000)
	t.Cleanup(func() { _ = session.Close() })
	registerStoragesFstatMethods(t, session)
	return session
}

// fstatTestProject builds one project with loose files, a directory tree, a
// search directory, and one mounted archive used for read-only checks.
func fstatTestProject(t *testing.T) (project, saveDir string, archiveEntry []byte) {
	t.Helper()
	project = t.TempDir()
	saveDir = filepath.Join(t.TempDir(), "saves")
	if err := os.MkdirAll(saveDir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, data string) {
		path := filepath.Join(project, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("base.txt", "0123456789")
	write("tree/alpha/one.txt", "one")
	write("tree/alpha/beta/two.txt", "two")
	write("tree/top.txt", "top")
	write("search/Found.TJS", "search path")
	if err := os.MkdirAll(filepath.Join(project, "emptydir"), 0o755); err != nil {
		t.Fatal(err)
	}
	archiveEntry = []byte("archive resource")
	if err := os.WriteFile(filepath.Join(project, "Mounted.XP3"), storagesTestXP3("archive/Entry.TJS", archiveEntry), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(saveDir, "overlay.txt"), []byte("overlay"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(saveDir, "attr.txt"), []byte("attribute"), 0o644); err != nil {
		t.Fatal(err)
	}
	return project, saveDir, archiveEntry
}

func fstatEvalInt(t *testing.T, session *Session, source string) int64 {
	t.Helper()
	value, err := session.Evaluate(source)
	if err != nil {
		t.Fatalf("%s: %v", source, err)
	}
	n, err := value.Int()
	if err != nil {
		t.Fatalf("%s returned %s: %v", source, value.TypeName(), err)
	}
	return n
}

func fstatEvalText(t *testing.T, session *Session, source string) string {
	t.Helper()
	value, err := session.Evaluate(source)
	if err != nil {
		t.Fatalf("%s: %v", source, err)
	}
	return value.Text()
}

func TestStoragesFstatTimeQueries(t *testing.T) {
	project, saveDir, archiveEntry := fstatTestProject(t)
	session := fstatTestSession(t, project, saveDir)

	info, err := os.Stat(filepath.Join(project, "base.txt"))
	if err != nil {
		t.Fatal(err)
	}
	mtimeSeconds := info.ModTime().Unix()
	if got := fstatEvalInt(t, session, `Storages.getTime("base.txt").mtime.getTime()`); got != mtimeSeconds*1000 {
		t.Errorf("getTime mtime = %d, want %d", got, mtimeSeconds*1000)
	}
	if got := fstatEvalInt(t, session, `Storages.getTime("base.txt").atime.getTime()`); got == 0 {
		t.Error("getTime atime is zero")
	}
	if _, err := session.Evaluate(`Storages.getTime("missing.txt")`); !errors.Is(err, assets.ErrNotFound) {
		t.Errorf("getTime(missing) error = %v, want ErrNotFound", err)
	}
	if _, err := session.Evaluate(`Storages.getTime("Mounted.XP3>archive/Entry.TJS")`); !errors.Is(err, ErrStorageReadOnly) {
		t.Errorf("getTime(archive member) error = %v, want ErrStorageReadOnly", err)
	}
	if _, err := session.Evaluate(`Storages.getTime()`); err == nil {
		t.Error("getTime without an argument succeeded")
	}

	wantModified := time.Unix(1_500_000_000, 0)
	if got := fstatEvalInt(t, session, `Storages.setTime("overlay.txt", %[mtime: 1500000000, atime: 1500000000])`); got != 1 {
		t.Fatalf("setTime = %d, want 1", got)
	}
	overlayInfo, err := os.Stat(filepath.Join(saveDir, "overlay.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !overlayInfo.ModTime().Equal(wantModified) {
		t.Errorf("setTime mtime = %v, want %v", overlayInfo.ModTime(), wantModified)
	}
	if got := fstatEvalInt(t, session, `Storages.getLastModifiedFileTime("overlay.txt")`); got != 1_500_000_000 {
		t.Errorf("getLastModifiedFileTime = %d, want 1500000000", got)
	}
	if got := fstatEvalInt(t, session, `Storages.setLastModifiedFileTime("overlay.txt", 1600000000)`); got != 1 {
		t.Fatalf("setLastModifiedFileTime = %d, want 1", got)
	}
	if got := fstatEvalInt(t, session, `Storages.setLastModifiedFileTime("missing.txt", 1600000000)`); got != 0 {
		t.Errorf("setLastModifiedFileTime(missing) = %d, want 0", got)
	}
	if got := fstatEvalInt(t, session, `Storages.getLastModifiedFileTime("missing.txt")`); got != 0 {
		t.Errorf("getLastModifiedFileTime(missing) = %d, want 0", got)
	}
	if _, err := session.Evaluate(`Storages.setTime("base.txt", %[mtime: 1500000000])`); !errors.Is(err, ErrStorageReadOnly) {
		t.Errorf("setTime(project file) error = %v, want ErrStorageReadOnly", err)
	}
	if got := fstatEvalInt(t, session, `Storages.setTime("missing.txt", %[mtime: 1500000000])`); got != 0 {
		t.Errorf("setTime(missing) = %d, want 0", got)
	}
	if _, err := session.Evaluate(`Storages.setTime("overlay.txt", %[ctime: 1500000000])`); err == nil {
		t.Error("setTime with only ctime unexpectedly succeeded")
	}

	// setTime accepts the Date objects that getTime returns.
	if _, err := session.Evaluate(`var ages = Storages.getTime("overlay.txt"); ages.mtime.setTime(1700000000000); Storages.setTime("overlay.txt", ages);`); err != nil {
		t.Fatalf("setTime with Date dictionary: %v", err)
	}
	if got := fstatEvalInt(t, session, `Storages.getLastModifiedFileTime("overlay.txt")`); got != 1_700_000_000 {
		t.Errorf("setTime from Date mtime = %d, want 1700000000", got)
	}

	if _, err := session.Evaluate(`Storages.exportFile("Mounted.XP3>archive/Entry.TJS", "exported.txt")`); err != nil {
		t.Fatalf("exportFile: %v", err)
	}
	exported, err := os.ReadFile(filepath.Join(saveDir, "exported.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(exported) != string(archiveEntry) {
		t.Errorf("exportFile content = %q, want %q", exported, archiveEntry)
	}
	if _, err := session.Evaluate(`Storages.exportFile("missing.txt", "exported.txt")`); err == nil {
		t.Error("exportFile with a missing source succeeded")
	}
	if _, err := session.Evaluate(`Storages.exportFile("base.txt", "Mounted.XP3>archive/Entry.TJS")`); !errors.Is(err, ErrStorageReadOnly) {
		t.Errorf("exportFile into archive error = %v, want ErrStorageReadOnly", err)
	}
	if _, err := session.Evaluate(`Storages.clearStorageCaches()`); err != nil {
		t.Errorf("clearStorageCaches: %v", err)
	}
}

func TestStoragesFstatFileMutations(t *testing.T) {
	project, saveDir, _ := fstatTestProject(t)
	session := fstatTestSession(t, project, saveDir)

	if got := fstatEvalInt(t, session, `Storages.copyFile("base.txt", "copy.txt")`); got != 1 {
		t.Fatalf("copyFile = %d, want 1", got)
	}
	if data, err := os.ReadFile(filepath.Join(saveDir, "copy.txt")); err != nil || string(data) != "0123456789" {
		t.Fatalf("copyFile destination = %q, %v", data, err)
	}
	if data, err := os.ReadFile(filepath.Join(project, "base.txt")); err != nil || string(data) != "0123456789" {
		t.Errorf("copyFile changed the project source = %q, %v", data, err)
	}
	if got := fstatEvalInt(t, session, `Storages.copyFile("base.txt", "copy.txt")`); got != 1 {
		t.Errorf("copyFile overwrite = %d, want 1", got)
	}
	if got := fstatEvalInt(t, session, `Storages.copyFile("missing.txt", "copy.txt")`); got != 0 {
		t.Errorf("copyFile(missing source) = %d, want 0", got)
	}
	if got := fstatEvalInt(t, session, `Storages.copyFile("Mounted.XP3>archive/Entry.TJS", "copy.txt")`); got != 0 {
		t.Errorf("copyFile(archive source) = %d, want 0", got)
	}
	if _, err := session.Evaluate(`Storages.copyFile("base.txt", "Mounted.XP3>archive/Entry.TJS")`); !errors.Is(err, ErrStorageReadOnly) {
		t.Errorf("copyFile(archive destination) error = %v, want ErrStorageReadOnly", err)
	}

	if err := os.WriteFile(filepath.Join(saveDir, "move-from.txt"), []byte("move me"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := fstatEvalInt(t, session, `Storages.moveFile("move-from.txt", "moved.txt")`); got != 1 {
		t.Fatalf("moveFile = %d, want 1", got)
	}
	if _, err := os.Stat(filepath.Join(saveDir, "move-from.txt")); !os.IsNotExist(err) {
		t.Errorf("moveFile left the source: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(saveDir, "moved.txt")); err != nil || string(data) != "move me" {
		t.Fatalf("moveFile destination = %q, %v", data, err)
	}
	if got := fstatEvalInt(t, session, `Storages.moveFile("missing.txt", "moved.txt")`); got != 0 {
		t.Errorf("moveFile(missing) = %d, want 0", got)
	}
	if _, err := session.Evaluate(`Storages.moveFile("base.txt", "moved-base.txt")`); !errors.Is(err, ErrStorageReadOnly) {
		t.Errorf("moveFile(project source) error = %v, want ErrStorageReadOnly", err)
	}

	if got := fstatEvalInt(t, session, `Storages.truncateFile("copy.txt", 3)`); got != 1 {
		t.Fatalf("truncateFile = %d, want 1", got)
	}
	if data, err := os.ReadFile(filepath.Join(saveDir, "copy.txt")); err != nil || string(data) != "012" {
		t.Fatalf("truncateFile result = %q, %v", data, err)
	}
	if got := fstatEvalInt(t, session, `Storages.truncateFile("copy.txt", 5)`); got != 1 {
		t.Fatalf("truncateFile extend = %d, want 1", got)
	}
	if data, err := os.ReadFile(filepath.Join(saveDir, "copy.txt")); err != nil || string(data) != "012\x00\x00" {
		t.Fatalf("truncateFile extend result = %q, %v", data, err)
	}
	if got := fstatEvalInt(t, session, `Storages.truncateFile("missing.txt", 1)`); got != 0 {
		t.Errorf("truncateFile(missing) = %d, want 0", got)
	}
	if _, err := session.Evaluate(`Storages.truncateFile("copy.txt", -1)`); err == nil {
		t.Error("truncateFile with a negative size succeeded")
	}
	if _, err := session.Evaluate(`Storages.truncateFile("base.txt", 1)`); !errors.Is(err, ErrStorageReadOnly) {
		t.Errorf("truncateFile(project file) error = %v, want ErrStorageReadOnly", err)
	}

	if got := fstatEvalInt(t, session, `Storages.deleteFile("moved.txt")`); got != 1 {
		t.Fatalf("deleteFile = %d, want 1", got)
	}
	if _, err := os.Stat(filepath.Join(saveDir, "moved.txt")); !os.IsNotExist(err) {
		t.Errorf("deleteFile left the file: %v", err)
	}
	if got := fstatEvalInt(t, session, `Storages.deleteFile("moved.txt")`); got != 0 {
		t.Errorf("deleteFile after removal = %d, want 0", got)
	}
	if _, err := session.Evaluate(`Storages.deleteFile("Mounted.XP3>archive/Entry.TJS")`); !errors.Is(err, ErrStorageReadOnly) {
		t.Errorf("deleteFile(archive member) error = %v, want ErrStorageReadOnly", err)
	}
	if _, err := session.Evaluate(`Storages.deleteFile("base.txt")`); !errors.Is(err, ErrStorageReadOnly) {
		t.Errorf("deleteFile(project file) error = %v, want ErrStorageReadOnly", err)
	}

	if got := fstatEvalInt(t, session, `Storages.copyFileNoNormalize("tree/top.txt", "rawname.bin", true)`); got != 1 {
		t.Fatalf("copyFileNoNormalize = %d, want 1", got)
	}
	if data, err := os.ReadFile(filepath.Join(saveDir, "rawname.bin")); err != nil || string(data) != "top" {
		t.Fatalf("copyFileNoNormalize destination = %q, %v", data, err)
	}
	if got := fstatEvalInt(t, session, `Storages.copyFileNoNormalize("tree/top.txt", "rawname.bin", true)`); got != 0 {
		t.Errorf("copyFileNoNormalize failIfExist = %d, want 0", got)
	}
	if got := fstatEvalInt(t, session, `Storages.copyFileNoNormalize("tree/alpha/one.txt", "rawname.bin")`); got != 1 {
		t.Errorf("copyFileNoNormalize overwrite = %d, want 1", got)
	}
	if data, err := os.ReadFile(filepath.Join(saveDir, "rawname.bin")); err != nil || string(data) != "one" {
		t.Fatalf("copyFileNoNormalize overwrite result = %q, %v", data, err)
	}
	if got := fstatEvalInt(t, session, `Storages.copyFileNoNormalize("missing.txt", "rawname.bin")`); got != 0 {
		t.Errorf("copyFileNoNormalize(missing) = %d, want 0", got)
	}
	if _, err := session.Evaluate(`Storages.copyFileNoNormalize("tree/top.txt", "Mounted.XP3>archive/Entry.TJS")`); !errors.Is(err, ErrStorageReadOnly) {
		t.Errorf("copyFileNoNormalize(archive destination) error = %v, want ErrStorageReadOnly", err)
	}

	if _, err := session.Evaluate(`Storages.addAutoPath("search/")`); err != nil {
		t.Fatal(err)
	}
	if got := fstatEvalInt(t, session, `Storages.copyFile("Found.TJS", "found-copy.txt")`); got != 1 {
		t.Errorf("copyFile through an auto-path = %d, want 1", got)
	}
	if data, err := os.ReadFile(filepath.Join(saveDir, "found-copy.txt")); err != nil || string(data) != "search path" {
		t.Errorf("copyFile auto-path destination = %q, %v", data, err)
	}

	for _, source := range []string{
		`Storages.deleteFile("../escape.txt")`,
		`Storages.truncateFile("../escape.txt", 1)`,
		`Storages.copyFile("base.txt", "../escape.txt")`,
		`Storages.moveFile("../escape.txt", "x.txt")`,
	} {
		if _, err := session.Evaluate(source); !errors.Is(err, core.ErrInvalidStorageName) {
			t.Errorf("%s error = %v, want ErrInvalidStorageName", source, err)
		}
	}
}

func TestStoragesFstatDirectories(t *testing.T) {
	project, saveDir, _ := fstatTestProject(t)
	session := fstatTestSession(t, project, saveDir)

	if got := fstatEvalText(t, session, `Storages.dirtree("tree/").join("|")`); got != "alpha/|alpha/beta/|alpha/beta/two.txt|alpha/one.txt|top.txt" {
		t.Errorf("dirtree = %q", got)
	}
	if got := fstatEvalText(t, session, `Storages.dirtree("tree/", true).join("|")`); got != "alpha/|alpha/beta/" {
		t.Errorf("dirtree directory-only = %q", got)
	}
	if got := fstatEvalText(t, session, `Storages.dirtree("missing/").join("|")`); got != "" {
		t.Errorf("dirtree(missing) = %q, want empty", got)
	}
	if _, err := session.Evaluate(`Storages.dirtree("Mounted.XP3>archive/")`); err == nil {
		t.Error("dirtree of an archive directory succeeded")
	}

	if got := fstatEvalInt(t, session, `Storages.createDirectory("tree/overlay/")`); got != 1 {
		t.Fatalf("createDirectory = %d, want 1", got)
	}
	if info, err := os.Stat(filepath.Join(saveDir, "tree", "overlay")); err != nil || !info.IsDir() {
		t.Fatalf("createDirectory result: %v", err)
	}
	if got := fstatEvalInt(t, session, `Storages.createDirectory("tree/overlay/")`); got != 0 {
		t.Errorf("createDirectory existing = %d, want 0", got)
	}
	if got := fstatEvalText(t, session, `Storages.dirtree("tree/").join("|")`); got != "alpha/|alpha/beta/|alpha/beta/two.txt|alpha/one.txt|overlay/|top.txt" {
		t.Errorf("dirtree after overlay directory = %q", got)
	}
	if got := fstatEvalInt(t, session, `Storages.isExistentDirectory("tree/overlay/")`); got != 1 {
		t.Errorf("isExistentDirectory overlay = %d, want 1", got)
	}
	if got := fstatEvalInt(t, session, `Storages.isExistentDirectory("tree/")`); got != 1 {
		t.Errorf("isExistentDirectory project = %d, want 1", got)
	}
	if got := fstatEvalInt(t, session, `Storages.isExistentDirectory("missing/")`); got != 0 {
		t.Errorf("isExistentDirectory missing = %d, want 0", got)
	}
	if got := fstatEvalInt(t, session, `Storages.isExistentDirectory("Mounted.XP3>archive/")`); got != 0 {
		t.Errorf("isExistentDirectory archive = %d, want 0", got)
	}
	if _, err := session.Evaluate(`Storages.isExistentDirectory("tree")`); err == nil {
		t.Error("isExistentDirectory without a trailing separator succeeded")
	}

	if got := fstatEvalInt(t, session, `Storages.createDirectoryNoNormalize("rawcase/")`); got != 1 {
		t.Fatalf("createDirectoryNoNormalize = %d, want 1", got)
	}
	if info, err := os.Stat(filepath.Join(saveDir, "rawcase")); err != nil || !info.IsDir() {
		t.Fatalf("createDirectoryNoNormalize result: %v", err)
	}
	if got := fstatEvalInt(t, session, `Storages.createDirectoryNoNormalize("rawcase/")`); got != 0 {
		t.Errorf("createDirectoryNoNormalize existing = %d, want 0", got)
	}

	// A non-empty overlay directory cannot be removed, matching unlink.
	if err := os.MkdirAll(filepath.Join(saveDir, "nonempty"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(saveDir, "nonempty", "file.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := fstatEvalInt(t, session, `Storages.removeDirectory("nonempty/")`); got != 0 {
		t.Errorf("removeDirectory non-empty = %d, want 0", got)
	}
	if got := fstatEvalInt(t, session, `Storages.removeDirectory("tree/overlay/")`); got != 1 {
		t.Errorf("removeDirectory = %d, want 1", got)
	}
	if _, err := os.Stat(filepath.Join(saveDir, "tree", "overlay")); !os.IsNotExist(err) {
		t.Errorf("removeDirectory left the directory: %v", err)
	}
	if got := fstatEvalInt(t, session, `Storages.removeDirectory("missing/")`); got != 0 {
		t.Errorf("removeDirectory(missing) = %d, want 0", got)
	}
	if _, err := session.Evaluate(`Storages.removeDirectory("emptydir/")`); !errors.Is(err, ErrStorageReadOnly) {
		t.Errorf("removeDirectory(project directory) error = %v, want ErrStorageReadOnly", err)
	}
	if _, err := session.Evaluate(`Storages.removeDirectory("tree")`); err == nil {
		t.Error("removeDirectory without a trailing separator succeeded")
	}
	if _, err := session.Evaluate(`Storages.createDirectory("../escape/")`); !errors.Is(err, core.ErrInvalidStorageName) {
		t.Errorf("createDirectory escape error = %v, want ErrInvalidStorageName", err)
	}
	if _, err := session.Evaluate(`Storages.createDirectory("tree")`); err == nil {
		t.Error("createDirectory without a trailing separator succeeded")
	}
	if _, err := session.Evaluate(`Storages.changeDirectory("tree/")`); !errors.Is(err, ErrUnsupportedDevice) {
		t.Errorf("changeDirectory error = %v, want ErrUnsupportedDevice", err)
	}
	if _, err := session.Evaluate(`Storages.changeDirectory("tree")`); err == nil {
		t.Error("changeDirectory without a trailing separator succeeded")
	}
}

func TestStoragesFstatAttributesAndPathQueries(t *testing.T) {
	project, saveDir, _ := fstatTestProject(t)
	session := fstatTestSession(t, project, saveDir)

	archiveAttrib := int64(fileAttributeArchive)
	if got := fstatEvalInt(t, session, `Storages.getFileAttributes("attr.txt")`); got != archiveAttrib {
		t.Errorf("getFileAttributes writable file = %#x, want %#x", got, archiveAttrib)
	}
	if got := fstatEvalInt(t, session, `Storages.getFileAttributes("missing.txt")`); got != 0 {
		t.Errorf("getFileAttributes missing = %#x, want 0", got)
	}
	if _, err := session.Evaluate(`Storages.getFileAttributes("Mounted.XP3>archive/Entry.TJS")`); !errors.Is(err, ErrStorageReadOnly) {
		t.Errorf("getFileAttributes archive error = %v, want ErrStorageReadOnly", err)
	}
	if got := fstatEvalInt(t, session, `Storages.getFileAttributes("tree/")`); got&fileAttributeDirectory == 0 {
		t.Errorf("getFileAttributes directory = %#x, want the directory bit", got)
	}
	if got := fstatEvalInt(t, session, `Storages.setFileAttributes("attr.txt", 1)`); got != 1 {
		t.Fatalf("setFileAttributes = %d, want 1", got)
	}
	if got := fstatEvalInt(t, session, `Storages.getFileAttributes("attr.txt")`); got != archiveAttrib|fileAttributeReadOnly {
		t.Errorf("getFileAttributes read-only = %#x, want %#x", got, archiveAttrib|fileAttributeReadOnly)
	}
	if got := fstatEvalInt(t, session, `Storages.resetFileAttributes("attr.txt", 1)`); got != 1 {
		t.Fatalf("resetFileAttributes = %d, want 1", got)
	}
	if got := fstatEvalInt(t, session, `Storages.getFileAttributes("attr.txt")`); got != archiveAttrib {
		t.Errorf("getFileAttributes after reset = %#x, want %#x", got, archiveAttrib)
	}
	if got := fstatEvalInt(t, session, `Storages.setFileAttributes("missing.txt", 1)`); got != 0 {
		t.Errorf("setFileAttributes missing = %d, want 0", got)
	}
	if got := fstatEvalInt(t, session, `Storages.resetFileAttributes("missing.txt", 1)`); got != 0 {
		t.Errorf("resetFileAttributes missing = %d, want 0", got)
	}
	if _, err := session.Evaluate(`Storages.setFileAttributes("attr.txt", 0x2)`); !errors.Is(err, ErrUnsupportedDevice) {
		t.Errorf("setFileAttributes hidden error = %v, want ErrUnsupportedDevice", err)
	}
	if _, err := session.Evaluate(`Storages.setFileAttributes("base.txt", 1)`); !errors.Is(err, ErrStorageReadOnly) {
		t.Errorf("setFileAttributes project file error = %v, want ErrStorageReadOnly", err)
	}

	if got := fstatEvalText(t, session, `Storages.getDisplayName("base.txt")`); got != "base.txt" {
		t.Errorf("getDisplayName = %q", got)
	}
	if got := fstatEvalText(t, session, `Storages.getDisplayName("tree/")`); got != "tree" {
		t.Errorf("getDisplayName directory = %q", got)
	}
	if got := fstatEvalText(t, session, `Storages.getDisplayName("missing.txt")`); got != "" {
		t.Errorf("getDisplayName missing = %q, want empty", got)
	}
	if _, err := session.Evaluate(`Storages.getDisplayName("Mounted.XP3>archive/Entry.TJS")`); !errors.Is(err, ErrStorageReadOnly) {
		t.Errorf("getDisplayName archive error = %v, want ErrStorageReadOnly", err)
	}

	if got := fstatEvalInt(t, session, `Storages.isExistentStorageNoSearchNoNormalize("tree/top.txt")`); got != 1 {
		t.Errorf("isExistentStorageNoSearchNoNormalize existing = %d, want 1", got)
	}
	if got := fstatEvalInt(t, session, `Storages.isExistentStorageNoSearchNoNormalize("missing.txt")`); got != 0 {
		t.Errorf("isExistentStorageNoSearchNoNormalize missing = %d, want 0", got)
	}
	if got := fstatEvalInt(t, session, `Storages.isExistentStorageNoSearchNoNormalize("Mounted.XP3>archive/Entry.TJS")`); got != 1 {
		t.Errorf("isExistentStorageNoSearchNoNormalize archive = %d, want 1", got)
	}
	if _, err := session.Evaluate(`Storages.addAutoPath("search/")`); err != nil {
		t.Fatal(err)
	}
	if got := fstatEvalInt(t, session, `Storages.isExistentStorage("Found.TJS")`); got != 1 {
		t.Errorf("isExistentStorage search path = %d, want 1", got)
	}
	if got := fstatEvalInt(t, session, `Storages.isExistentStorageNoSearchNoNormalize("Found.TJS")`); got != 0 {
		t.Errorf("isExistentStorageNoSearchNoNormalize search path = %d, want 0", got)
	}
	if got := fstatEvalInt(t, session, `Storages.isExistentStorageNoSearchNoNormalize("search/Found.TJS")`); got != 1 {
		t.Errorf("isExistentStorageNoSearchNoNormalize explicit path = %d, want 1", got)
	}

	searchDir := t.TempDir()
	toolPath := filepath.Join(searchDir, "fstat-tool.txt")
	if err := os.WriteFile(toolPath, []byte("tool"), 0o644); err != nil {
		t.Fatal(err)
	}
	absTool, err := filepath.Abs(toolPath)
	if err != nil {
		t.Fatal(err)
	}
	wantSearch := core.LocalStorageName(filepath.Clean(absTool))
	if got := fstatEvalText(t, session, `Storages.searchPath("fstat-tool.txt", `+strconv.Quote(searchDir)+`)`); got != wantSearch {
		t.Errorf("searchPath = %q, want %q", got, wantSearch)
	}
	value, err := session.Evaluate(`Storages.searchPath("fstat-missing.txt", ` + strconv.Quote(searchDir) + `)`)
	if err != nil {
		t.Fatal(err)
	}
	if value.Kind() != tjs.KindVoid {
		t.Errorf("searchPath missing = %s, want void", value.TypeName())
	}
	if _, err := session.Evaluate(`Storages.searchPath()`); err == nil {
		t.Error("searchPath without a file name succeeded")
	}

	if _, err := session.Evaluate(`Storages.selectDirectory()`); err == nil {
		t.Error("selectDirectory without a window succeeded")
	}
	if _, err := session.Evaluate(`Storages.selectDirectory(0)`); !errors.Is(err, ErrUnsupportedDevice) {
		t.Errorf("selectDirectory error = %v, want ErrUnsupportedDevice", err)
	}
}

func TestStoragesFstatReadOnlyAndEscapes(t *testing.T) {
	project, saveDir, _ := fstatTestProject(t)
	session := fstatTestSession(t, project, saveDir)

	for _, source := range []string{
		`Storages.getTime("Mounted.XP3>archive/Entry.TJS")`,
		`Storages.getLastModifiedFileTime("Mounted.XP3>archive/Entry.TJS")`,
		`Storages.getFileAttributes("Mounted.XP3>archive/Entry.TJS")`,
		`Storages.getDisplayName("Mounted.XP3>archive/Entry.TJS")`,
		`Storages.setTime("Mounted.XP3>archive/Entry.TJS", %[mtime: 1])`,
		`Storages.setLastModifiedFileTime("Mounted.XP3>archive/Entry.TJS", 1)`,
		`Storages.deleteFile("Mounted.XP3>archive/Entry.TJS")`,
		`Storages.truncateFile("Mounted.XP3>archive/Entry.TJS", 1)`,
		`Storages.moveFile("overlay.txt", "Mounted.XP3>archive/Entry.TJS")`,
		`Storages.removeDirectory("Mounted.XP3>archive/")`,
		`Storages.createDirectory("Mounted.XP3>archive/")`,
		`Storages.createDirectoryNoNormalize("Mounted.XP3>archive/")`,
		`Storages.setFileAttributes("Mounted.XP3>archive/Entry.TJS", 1)`,
		`Storages.resetFileAttributes("Mounted.XP3>archive/Entry.TJS", 1)`,
		`Storages.exportFile("base.txt", "Mounted.XP3>archive/Entry.TJS")`,
		`Storages.copyFile("base.txt", "Mounted.XP3>archive/Entry.TJS")`,
		`Storages.copyFileNoNormalize("base.txt", "Mounted.XP3>archive/Entry.TJS")`,
	} {
		if _, err := session.Evaluate(source); !errors.Is(err, ErrStorageReadOnly) {
			t.Errorf("%s error = %v, want ErrStorageReadOnly", source, err)
		}
	}

	for _, source := range []string{
		`Storages.getTime("../escape.txt")`,
		`Storages.getLastModifiedFileTime("../escape.txt")`,
		`Storages.getFileAttributes("../escape.txt")`,
		`Storages.getDisplayName("../escape.txt")`,
		`Storages.setTime("../escape.txt", %[mtime: 1])`,
		`Storages.setLastModifiedFileTime("../escape.txt", 1)`,
		`Storages.deleteFile("../escape.txt")`,
		`Storages.truncateFile("../escape.txt", 1)`,
		`Storages.moveFile("../escape.txt", "x.txt")`,
		`Storages.removeDirectory("../escape/")`,
		`Storages.createDirectory("../escape/")`,
		`Storages.setFileAttributes("../escape.txt", 1)`,
		`Storages.resetFileAttributes("../escape.txt", 1)`,
		`Storages.dirtree("../escape/")`,
		`Storages.isExistentDirectory("../escape/")`,
		`Storages.copyFile("base.txt", "../escape.txt")`,
	} {
		if _, err := session.Evaluate(source); !errors.Is(err, core.ErrInvalidStorageName) {
			t.Errorf("%s error = %v, want ErrInvalidStorageName", source, err)
		}
	}

	for _, source := range []string{
		`Storages.createDirectoryNoNormalize("../escape/")`,
		`Storages.copyFileNoNormalize("base.txt", "../escape.txt")`,
		`Storages.exportFile("base.txt", "../escape.txt")`,
	} {
		if _, err := session.Evaluate(source); !errors.Is(err, ErrInvalidSaveName) {
			t.Errorf("%s error = %v, want ErrInvalidSaveName", source, err)
		}
	}

	if _, err := session.Evaluate(`Storages.deleteFile("Mounted.XP3>archive/Entry.TJS")`); err == nil {
		t.Error("archive delete unexpectedly succeeded")
	}
	if got := fstatEvalInt(t, session, `Storages.isExistentStorageNoSearchNoNormalize("../escape.txt")`); got != 0 {
		t.Errorf("isExistentStorageNoSearchNoNormalize escape = %d, want 0", got)
	}
}
