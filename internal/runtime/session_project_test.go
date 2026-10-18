package runtime

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestOpenSessionResolvesStartupDirectory(t *testing.T) {
	tests := []struct {
		name            string
		input           func(root string) string
		startup         func(root string) string
		defaultSaveRoot func(root string) string
	}{
		{
			name: "project root",
			input: func(root string) string {
				return root
			},
			startup: func(root string) string {
				return root
			},
			defaultSaveRoot: func(root string) string {
				return root
			},
		},
		{
			name: "project root with data directory",
			input: func(root string) string {
				return root
			},
			startup: func(root string) string {
				return filepath.Join(root, "data")
			},
			defaultSaveRoot: func(root string) string {
				return root
			},
		},
		{
			name: "data directory",
			input: func(root string) string {
				return filepath.Join(root, "data")
			},
			startup: func(root string) string {
				return filepath.Join(root, "data")
			},
			defaultSaveRoot: func(root string) string {
				return filepath.Join(root, "data")
			},
		},
	}
	saveCases := []struct {
		name     string
		explicit bool
	}{
		{name: "default"},
		{name: "explicit save directory", explicit: true},
	}
	for _, tt := range tests {
		tt := tt
		for _, saveCase := range saveCases {
			saveCase := saveCase
			t.Run(tt.name+"/"+saveCase.name, func(t *testing.T) {
				root := t.TempDir()
				startupDir := tt.startup(root)
				if err := os.MkdirAll(startupDir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(startupDir, "startup.tjs"), nil, 0o644); err != nil {
					t.Fatal(err)
				}

				input := tt.input(root)
				defaultSaveRoot, err := filepath.EvalSymlinks(tt.defaultSaveRoot(root))
				if err != nil {
					t.Fatal(err)
				}
				saveDir := ""
				wantSaveDir := filepath.Join(defaultSaveRoot, "savedata")
				if saveCase.explicit {
					saveDir = filepath.Join(root, "savedata", "explicit")
					wantSaveDir = saveDir
				}
				session, err := OpenSession(input, saveDir, 100)
				if err != nil {
					t.Fatal(err)
				}
				defer session.Close()

				wantStorageProject, err := filepath.EvalSymlinks(input)
				if err != nil {
					t.Fatal(err)
				}
				if got := session.Services.Storage.Project; got != wantStorageProject {
					t.Fatalf("storage project = %q, want %q", got, wantStorageProject)
				}
				if got := session.Services.SaveDir; got != wantSaveDir {
					t.Fatalf("save directory = %q, want %q", got, wantSaveDir)
				}
				dataPath, err := session.Evaluate("System.dataPath")
				if err != nil {
					t.Fatal(err)
				}
				wantDataPath := localStorageDirectoryURI(wantSaveDir)
				if got := dataPath.Text(); got != wantDataPath {
					t.Fatalf("System.dataPath = %q, want %q", got, wantDataPath)
				}
			})
		}
	}
}

func TestOpenSessionMissingStartupReportsCandidatePaths(t *testing.T) {
	project := t.TempDir()
	_, err := OpenSession(project, "", 100)
	if err == nil {
		t.Fatal("OpenSession unexpectedly accepted a project without startup.tjs")
	}
	for _, path := range []string{filepath.Join(project, "startup.tjs"), filepath.Join(project, "data", "startup.tjs")} {
		if !strings.Contains(err.Error(), strconv.Quote(path)) {
			t.Errorf("error %q does not identify candidate path %q", err, path)
		}
	}
}
func TestOpenSessionKeepsProjectRootAndDataResources(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "startup.tjs"), []byte("global.started = true;"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "loose.tjs"), []byte("data loose"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "movie.xp3"), storagesTestXP3("movie.dat", []byte("root archive")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "image00.xp3"), storagesTestXP3("image.dat", []byte("data archive")), 0o644); err != nil {
		t.Fatal(err)
	}

	session, err := OpenSession(root, "", 1000)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	closeArchivesOnCleanup(t, session.Services.Storage)
	wantRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := session.Services.Storage.Project; got != wantRoot {
		t.Fatalf("storage project = %q, want executable root %q", got, wantRoot)
	}
	if got, err := session.Services.Storage.Read("startup.tjs"); err != nil || string(got) != "global.started = true;" {
		t.Fatalf("read data startup = %q, %v", got, err)
	}
	if got, err := session.Services.Storage.Read("loose.tjs"); err != nil || string(got) != "data loose" {
		t.Fatalf("read data loose file = %q, %v", got, err)
	}
	if got, err := session.Services.Storage.Read("image00.xp3>image.dat"); err != nil || string(got) != "data archive" {
		t.Fatalf("read data archive resource = %q, %v", got, err)
	}
	exePath, err := session.Evaluate("System.exePath")
	if err != nil {
		t.Fatal(err)
	}
	dataArchiveName := filepath.Join(exePath.Text(), "data", "image00.xp3") + ">image.dat"
	if _, err := session.Services.Storage.Resolve(dataArchiveName); err != nil {
		t.Fatalf("resolve data archive through System.exePath = %v", err)
	}
	if got, err := session.Services.Storage.Read(dataArchiveName); err != nil || string(got) != "data archive" {
		t.Fatalf("read data archive through System.exePath = %q, %v", got, err)
	}
	rootArchiveName := filepath.Join(exePath.Text(), "movie.xp3") + ">movie.dat"
	if got, err := session.Services.Storage.Read(rootArchiveName); err != nil || string(got) != "root archive" {
		t.Fatalf("read root archive through System.exePath = %q, %v", got, err)
	}
}
