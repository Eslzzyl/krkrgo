package version

import "testing"

func TestProjectVersion(t *testing.T) {
	if Version != "0.0.1" {
		t.Fatalf("Version = %q, want 0.0.1", Version)
	}
	if Build != "1" {
		t.Fatalf("Build = %q, want 1", Build)
	}
}
