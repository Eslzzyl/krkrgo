package core

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestProjectExecutableSelectionAndOverride(t *testing.T) {
	parent := t.TempDir()
	project := filepath.Join(parent, "Another_Game")
	if err := os.Mkdir(project, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"AnotherGame.EXE", "updater.exe", "unins000.exe", "launcher.exe"} {
		if err := os.WriteFile(filepath.Join(project, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, ok, err := ProjectExecutable(project, nil)
	want := filepath.Join(project, "AnotherGame.EXE")
	if err != nil || !ok || got != want {
		t.Fatalf("discovery = %q, %v, %v; want %q", got, ok, err, want)
	}
	explicit := "launcher.exe"
	got, ok, err = ProjectExecutable(project, &explicit)
	if err != nil || !ok || got != filepath.Join(project, explicit) {
		t.Fatalf("explicit selection = %q, %v, %v", got, ok, err)
	}
	if err := os.WriteFile(filepath.Join(parent, "outside.exe"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, choice := range []string{"missing.exe", filepath.Join("..", "outside.exe")} {
		if _, _, err := ProjectExecutable(project, &choice); err == nil || !errors.Is(err, ErrInvalidProjectExecutable) {
			t.Errorf("explicit %q error = %v", choice, err)
		}
	}

}

func TestProjectExecutableAmbiguity(t *testing.T) {
	project := filepath.Join(t.TempDir(), "generic")
	if err := os.Mkdir(project, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"novel.exe", "unins000.exe"} {
		if err := os.WriteFile(filepath.Join(project, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, ok, err := ProjectExecutable(project, nil)
	if err != nil || !ok || got != filepath.Join(project, "novel.exe") {
		t.Fatalf("single non-helper = %q, %v, %v", got, ok, err)
	}
	if err := os.WriteFile(filepath.Join(project, "launcher.exe"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got, ok, err := ProjectExecutable(project, nil); err != nil || ok || got != "" {
		t.Fatalf("ambiguous discovery = %q, %v, %v", got, ok, err)
	}
}
