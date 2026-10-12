package runtime

import (
	"errors"
	"testing"

)

func TestInvalidWMVReportsSourceContext(t *testing.T) {
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
