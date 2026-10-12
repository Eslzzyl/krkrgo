package assets

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eslzzyl/krkrgo/internal/core"
)

func TestLoosePathExactCaseFastPath(t *testing.T) {
	project := t.TempDir()
	path := filepath.Join(project, "image.png")
	if err := os.WriteFile(path, []byte("image"), 0o600); err != nil {
		t.Fatal(err)
	}
	storage, err := OpenStorage(project, core.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}

	want, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := storage.loosePath("image.png")
	if err != nil {
		t.Fatalf("loosePath() error = %v", err)
	}
	if got != want {
		t.Fatalf("loosePath() = %q, want exact file path %q", got, want)
	}
}

func TestExactLoosePathMissingCandidateIsAMiss(t *testing.T) {
	project := t.TempDir()
	storage, err := OpenStorage(project, core.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}

	if path, ok := storage.exactLoosePath("missing.png"); ok || path != "" {
		t.Fatalf("exactLoosePath() = %q, %t, want an empty miss", path, ok)
	}
}

func TestLoosePathFallsBackToCaseInsensitiveLookup(t *testing.T) {
	project := t.TempDir()
	path := filepath.Join(project, "Mixed", "Path", "Image.PNG")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("image"), 0o600); err != nil {
		t.Fatal(err)
	}
	storage, err := OpenStorage(project, core.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}

	got, err := storage.loosePath("mixed/path/image.png")
	if err != nil {
		t.Fatalf("loosePath() error = %v", err)
	}
	gotInfo, err := os.Stat(got)
	if err != nil {
		t.Fatal(err)
	}
	wantInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(gotInfo, wantInfo) {
		t.Fatalf("loosePath() = %q, does not identify case-insensitive path %q", got, path)
	}
}

func TestLoosePathEnforcesSymlinkProjectRoot(t *testing.T) {
	project := t.TempDir()
	outside := t.TempDir()
	inside := filepath.Join(project, "inside.txt")
	outsideFile := filepath.Join(outside, "outside.txt")
	if err := os.WriteFile(inside, []byte("inside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outsideFile, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(inside, filepath.Join(project, "in-link.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(outsideFile, filepath.Join(project, "out-link.txt")); err != nil {
		t.Fatal(err)
	}
	storage, err := OpenStorage(project, core.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}

	want, err := filepath.EvalSymlinks(inside)
	if err != nil {
		t.Fatal(err)
	}
	got, err := storage.loosePath("in-link.txt")
	if err != nil {
		t.Fatalf("internal symlink lookup error = %v", err)
	}
	if got != want {
		t.Fatalf("internal symlink lookup = %q, want %q", got, want)
	}
	if _, err := storage.loosePath("out-link.txt"); err == nil || !strings.Contains(err.Error(), "storage symlink escapes game directory") {
		t.Fatalf("external symlink lookup error = %v, want project-root rejection", err)
	}
}

func TestNormalizeStorageNameAcceptsLowercasedProjectAbsolutePaths(t *testing.T) {
	project := filepath.Join(t.TempDir(), "MixedProject")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	storage, err := OpenStorage(project, core.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, archive := range storage.Archives {
			archive.Close()
		}
	}()

	archivePath := filepath.Join(storage.Project, "data.xp3")
	fullPath, err := storage.FullPath(archivePath + ">main/AnimationLayer.tjs")
	if err != nil {
		t.Fatal(err)
	}
	got, err := storage.NormalizeStorageName(fullPath)
	if err != nil {
		t.Fatalf("normalize FullPath result %q: %v", fullPath, err)
	}
	if got != "data.xp3>main/animationlayer.tjs" {
		t.Fatalf("normalized archive storage name = %q, want data.xp3>main/animationlayer.tjs", got)
	}

	absolute := filepath.ToSlash(filepath.Join(storage.Project, "data.xp3")) + ">main/AnimationLayer.tjs"
	got, err = storage.NormalizeStorageName(absolute)
	if err != nil {
		t.Fatalf("normalize absolute archive storage name %q: %v", absolute, err)
	}
	if got != "data.xp3>main/animationlayer.tjs" {
		t.Fatalf("normalized absolute archive storage name = %q, want data.xp3>main/animationlayer.tjs", got)
	}
}

func TestNormalizeStorageNameRejectsAbsolutePathsOutsideProject(t *testing.T) {
	project := t.TempDir()
	outside := t.TempDir()
	storage, err := OpenStorage(project, core.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}

	name := filepath.Join(outside, "outside.xp3") + ">main/script.tjs"
	if got, err := storage.NormalizeStorageName(name); err == nil {
		t.Fatalf("absolute path outside project normalized to %q", got)
	}
}

func TestNormalizeStorageNameRejectsProjectSymlinkEscape(t *testing.T) {
	project := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(project, "outside")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	storage, err := OpenStorage(project, core.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}

	if got, err := storage.NormalizeStorageName(filepath.Join(project, "outside", "file.tjs")); err == nil {
		t.Fatalf("project symlink escape normalized to %q", got)
	}
}

func BenchmarkStorageImageExtensionProbe(b *testing.B) {
	project := b.TempDir()
	storage, err := OpenStorage(project, core.DefaultLimits())
	if err != nil {
		b.Fatal(err)
	}
	extensions := []string{".jxr", ".tlg6", ".tlg5", ".tlg", ".png", ".jif", ".jpg", ".jpeg", ".dib", ".bmp", ".gif"}
	for _, benchmark := range []struct {
		name string
		read func(string) error
	}{
		{
			name: "optimized",
			read: func(name string) error {
				_, err := storage.Read(name)
				return err
			},
		},
		{
			name: "resolve_then_read",
			read: func(name string) error {
				resolved, err := storage.Resolve(name)
				if err != nil {
					return err
				}
				if _, err := storage.loosePath(resolved); err != nil {
					return err
				}
				if _, _, ok := storage.archiveLocation(resolved); !ok {
					return ErrNotFound
				}
				return nil
			},
		},
	} {
		b.Run(benchmark.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				for _, ext := range extensions {
					err := benchmark.read("image/frm_quickbord" + ext)
					if !errors.Is(err, ErrNotFound) {
						b.Fatalf("candidate lookup error = %v, want not found", err)
					}
				}
			}
		})
	}
}

func TestReadRootFastPathPreservesLooseOverlayAndCaseAliases(t *testing.T) {
	project := t.TempDir()
	archivePath := filepath.Join(project, "data.xp3")
	if err := os.WriteFile(archivePath, xp3TestArchive("image.png", []byte("archive")), 0o600); err != nil {
		t.Fatal(err)
	}
	storage, err := OpenStorage(project, core.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, archive := range storage.Archives {
			archive.Close()
		}
	}()
	got, err := storage.Read("IMAGE.PNG")
	if err != nil || string(got) != "archive" {
		t.Fatalf("case-insensitive archive read = %q, %v", got, err)
	}
	if err := os.WriteFile(filepath.Join(project, "IMAGE.PNG"), []byte("loose"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err = storage.Read("image.png")
	if err != nil || string(got) != "loose" {
		t.Fatalf("loose overlay read = %q, %v", got, err)
	}
}

func TestStorageDirectoryIndexReusesScanAndRefreshesOnMetadataChange(t *testing.T) {
	project := t.TempDir()
	storage, err := OpenStorage(project, core.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	firstInfo, err := os.Stat(project)
	if err != nil {
		t.Fatal(err)
	}
	first, err := storage.directoryEntries(project, firstInfo)
	if err != nil {
		t.Fatal(err)
	}
	secondInfo, err := os.Stat(project)
	if err != nil {
		t.Fatal(err)
	}
	second, err := storage.directoryEntries(project, secondInfo)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprintf("%p", first) != fmt.Sprintf("%p", second) {
		t.Fatal("unchanged directory was rescanned instead of reusing its index")
	}
	if err := os.WriteFile(filepath.Join(project, "appeared.txt"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	changedInfo, err := os.Stat(project)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := storage.directoryEntries(project, changedInfo)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprintf("%p", second) == fmt.Sprintf("%p", changed) {
		t.Fatal("changed directory reused a stale index")
	}
	if _, ok := changed["appeared.txt"]; !ok {
		t.Fatal("refreshed index omitted newly created entry")
	}
}

func TestStorageDirectoryCacheInvalidation(t *testing.T) {
	project, nextProject := t.TempDir(), t.TempDir()
	storage, err := OpenStorage(project, core.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	warm := func(path string) {
		t.Helper()
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := storage.directoryEntries(path, info); err != nil {
			t.Fatal(err)
		}
	}
	warm(project)
	if err := storage.AddSearchPath("data/"); err != nil {
		t.Fatal(err)
	}
	if len(storage.directories) != 0 {
		t.Fatal("AddSearchPath retained directory indexes")
	}
	warm(project)
	if err := storage.RemoveSearchPath("data/"); err != nil {
		t.Fatal(err)
	}
	if len(storage.directories) != 0 {
		t.Fatal("RemoveSearchPath retained directory indexes")
	}
	warm(project)
	storage.ClearCache()
	if len(storage.directories) != 0 {
		t.Fatal("ClearCache retained directory indexes")
	}
	warm(project)
	storage.Project = nextProject
	warm(nextProject)
	if len(storage.directories) != 1 {
		t.Fatalf("project change left stale indexes: %d", len(storage.directories))
	}
	archivePath := filepath.Join(nextProject, "mounted.xp3")
	if err := os.WriteFile(archivePath, xp3TestArchive("entry.txt", []byte("archive")), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := storage.Mount(archivePath, core.DefaultLimits()); err != nil {
		t.Fatal(err)
	}
	if len(storage.directories) != 0 {
		t.Fatal("Mount retained directory indexes")
	}
}

func TestStorageDirectoryIndexTracksFilesystemAndSearchPrecedence(t *testing.T) {
	project := t.TempDir()
	first := filepath.Join(project, "data")
	second := filepath.Join(project, "sys_source")
	if err := os.MkdirAll(first, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(second, 0o700); err != nil {
		t.Fatal(err)
	}
	storage, err := OpenStorage(project, core.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	// These are the ordinary directory auto paths registered by lovelyday's
	// Init.tjs, plus the archive-qualified path used by useArchiveIfExists.
	for _, path := range []string{"./", "data/", "sys_source/", "macro/"} {
		if err := storage.AddSearchPath(path); err != nil {
			t.Fatalf("AddSearchPath(%q): %v", path, err)
		}
	}
	archivePath := filepath.Join(project, "image00.xp3")
	if err := os.WriteFile(archivePath, xp3TestArchive("image/only.png", []byte("archive")), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := storage.Mount(archivePath, core.DefaultLimits()); err != nil {
		t.Fatal(err)
	}
	if err := storage.AddSearchPath("image00.xp3>image/"); err != nil {
		t.Fatal(err)
	}
	if resolved, err := storage.Resolve("only.png"); err != nil || resolved != "image00.xp3>image/only.png" {
		t.Fatalf("archive-qualified resolution = %q, %v", resolved, err)
	}

	// Warm an absent component lookup, then ensure metadata changes expose
	// files created and removed after indexing.
	if _, err := storage.Resolve("data/new.png"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("initial miss = %v, want ErrNotFound", err)
	}
	newFile := filepath.Join(first, "New.PNG")
	if err := os.WriteFile(newFile, []byte("created"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := storage.Read("data/new.png")
	if err != nil || string(got) != "created" {
		t.Fatalf("read created file = %q, %v", got, err)
	}
	if err := os.Remove(newFile); err != nil {
		t.Fatal(err)
	}
	if _, err := storage.Resolve("data/new.png"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted file lookup = %v, want ErrNotFound", err)
	}

	for _, name := range []string{"Case.PNG", "case.png"} {
		if err := os.WriteFile(filepath.Join(first, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	upper, upperErr := os.Stat(filepath.Join(first, "Case.PNG"))
	lower, lowerErr := os.Stat(filepath.Join(first, "case.png"))
	if upperErr == nil && lowerErr == nil && !os.SameFile(upper, lower) {
		if _, err := storage.Resolve("data/CASE.png"); err == nil || !strings.Contains(err.Error(), "ambiguous case-insensitive") {
			t.Fatalf("case collision error = %v", err)
		}
	} else {
		t.Log("filesystem is case-insensitive; distinct case-collision entries cannot be created")
	}

	// Later auto paths win over earlier paths.
	for dir, data := range map[string]string{first: "data", second: "sys"} {
		if err := os.WriteFile(filepath.Join(dir, "same.txt"), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, err = storage.Read("same.txt")
	if err != nil || string(got) != "sys" {
		t.Fatalf("search path precedence read = %q, %v", got, err)
	}
}

func TestStorageDirectoryIndexRechecksSymlinkTargets(t *testing.T) {
	project, outside := t.TempDir(), t.TempDir()
	internal := filepath.Join(project, "inside.txt")
	external := filepath.Join(outside, "outside.txt")
	link := filepath.Join(project, "link.txt")
	for path, contents := range map[string]string{internal: "inside", external: "outside"} {
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(internal, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	storage, err := OpenStorage(project, core.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if got, err := storage.Read("link.txt"); err != nil || string(got) != "inside" {
		t.Fatalf("initial in-root symlink = %q, %v", got, err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, link); err != nil {
		t.Fatal(err)
	}
	if _, err := storage.Read("link.txt"); err == nil || !strings.Contains(err.Error(), "storage symlink escapes") {
		t.Fatalf("retargeted symlink error = %v", err)
	}
}

func BenchmarkLovelydayStorageExtensionProbes(b *testing.B) {
	project := b.TempDir()
	for _, dir := range []string{"data", "sys_source", "macro"} {
		if err := os.MkdirAll(filepath.Join(project, dir), 0o700); err != nil {
			b.Fatal(err)
		}
	}
	// The Init.tjs ArchiveStruct list, including its data-qualified archive.
	archives := []string{
		"scenario.xp3", "image01.xp3", "image02.xp3", "image02_2.xp3",
		"image03.xp3", "image04.xp3", "image04_2.xp3", "image05.xp3",
		"media.xp3", "movie.xp3", "voice01.xp3", "voice02.xp3",
		"voice02_2.xp3", "data/image00.xp3", "image06.xp3",
	}
	for _, archive := range archives {
		path := filepath.Join(project, filepath.FromSlash(archive))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			b.Fatal(err)
		}
		entry, contents := "unused.txt", []byte("unused")
		if archive == "image01.xp3" {
			entry, contents = "frm_quickbord.png", []byte("hit")
		}
		if err := os.WriteFile(path, xp3TestArchive(entry, contents), 0o600); err != nil {
			b.Fatal(err)
		}
	}
	storage, err := OpenStorage(project, core.DefaultLimits())
	if err != nil {
		b.Fatal(err)
	}
	if err := storage.Mount(filepath.Join(project, "data", "image00.xp3"), core.DefaultLimits()); err != nil {
		b.Fatal(err)
	}
	// Mirror the ordinary Init.tjs paths and archive-qualified useArchiveIfExists
	// paths in precedence order.
	for _, path := range []string{"./", "data/", "sys_source/", "macro/"} {
		if err := storage.AddSearchPath(path); err != nil {
			b.Fatal(err)
		}
	}
	for _, archive := range archives {
		if err := storage.AddSearchPath(archive + ">"); err != nil {
			b.Fatal(err)
		}
	}
	extensions := []string{".jxr", ".tlg6", ".tlg5", ".tlg", ".png", ".jif", ".jpg", ".jpeg", ".dib", ".bmp", ".gif"}
	for _, mode := range []string{"cold", "warm"} {
		b.Run(mode, func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				for _, ext := range extensions {
					name := "frm_quickbord" + ext
					_, err := storage.Read(name)
					if ext == ".png" {
						if err != nil {
							b.Fatalf("extension hit: %v", err)
						}
					} else if !errors.Is(err, ErrNotFound) {
						b.Fatalf("extension miss = %v, want ErrNotFound", err)
					}
					if mode == "cold" {
						storage.clearDirectoryIndexes()
					}
				}
			}
		})
	}
}
func TestStorageArchiveMemberFoundThroughAutoPath(t *testing.T) {
	project := t.TempDir()
	archivePath := filepath.Join(project, "data.xp3")
	want := []byte("archive config")
	if err := os.WriteFile(archivePath, xp3TestArchive("main/config.tjs", want), 0o600); err != nil {
		t.Fatal(err)
	}
	storage, err := OpenStorage(project, core.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.Mount(archivePath, core.DefaultLimits()); err != nil {
		t.Fatal(err)
	}
	if err := storage.AddSearchPath("main/"); err != nil {
		t.Fatal(err)
	}
	if resolved, err := storage.Resolve("Config.tjs"); err != nil || resolved != "main/config.tjs" {
		t.Fatalf("Resolve(Config.tjs) = %q, %v; want main/config.tjs", resolved, err)
	}
	if got, err := storage.Read("Config.tjs"); err != nil || string(got) != string(want) {
		t.Fatalf("Read(Config.tjs) = %q, %v; want archive member", got, err)
	}
}
