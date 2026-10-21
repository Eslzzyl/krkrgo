package runtime

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/eslzzyl/krkrgo/internal/assets"
	"github.com/eslzzyl/krkrgo/internal/core"
)

func TestReadVideoStorageFindsMPEGWithoutExtension(t *testing.T) {
	project := t.TempDir()
	want := []byte("MPEG data")
	if err := os.WriteFile(filepath.Join(project, "logo.mpg"), want, 0o644); err != nil {
		t.Fatal(err)
	}
	storage, err := assets.OpenStorage(project, core.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	services := NewServices(storage, "", core.DefaultLimits())
	name, got, err := services.readVideoStorage("logo")
	if err != nil {
		t.Fatalf("read extensionless video: %v", err)
	}
	if name != "logo.mpg" || string(got) != string(want) {
		t.Fatalf("resolved video = %q / %q, want logo.mpg / %q", name, got, want)
	}
}

func TestInvalidWMVReportsSourceContext(t *testing.T) {
	requireFFmpegSidecar(t)
	v := NewVideoOverlay()
	err := v.Open("logo.wmv", []byte("invalid movie payload"))
	if err == nil {
		t.Fatal("invalid WMV input was accepted")
	}
	var ve *VideoError
	if !errors.As(err, &ve) || ve.Source != "logo.wmv" {
		t.Fatalf("WMV source context = %v", err)
	}
}
func TestVideoStopAndVisibilityWithoutSource(t *testing.T) {
	session := NewSession(DefaultServices(), 10_000)
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Errorf("close session: %v", err)
		}
	})

	if _, err := session.executeSource("<video-empty-stop-test>", `
movie = new VideoOverlay();
movie.visible = false;
movie.stop();
global.videoVisibleAfterStop = movie.visible;
`, false); err != nil {
		t.Fatalf("stop/hide empty video: %v", err)
	}
	visible, err := session.Evaluate("global.videoVisibleAfterStop")
	if err != nil {
		t.Fatal(err)
	}
	if truth, err := visible.Truth(); err != nil || truth {
		t.Fatalf("visible after hiding empty video = %v, err=%v; want false", visible, err)
	}

	video := NewVideoOverlay()
	if err := video.Play(); !errors.Is(err, ErrVideoNoSource) {
		t.Fatalf("play without source error = %v; want %v", err, ErrVideoNoSource)
	}
}
