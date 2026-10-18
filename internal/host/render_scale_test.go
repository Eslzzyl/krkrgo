package host

import (
	"testing"

	"github.com/eslzzyl/krkrgo/internal/runtime"
)

func TestDeviceScaleKeepsWindowLogicalAndLayoutDeviceSized(t *testing.T) {
	services := runtime.DefaultServices()
	services.SetRenderScale(2)
	session := runtime.NewSession(services, 1000)
	defer session.Close()

	game := NewGame(session)
	if game.poller.scale != 2 {
		t.Fatalf("poller scale = %d, want 2", game.poller.scale)
	}
	if got, want := game.poller.logical(2560), int32(1280); got != want {
		t.Fatalf("logical cursor = %d, want %d", got, want)
	}
	game.imageWidth, game.imageHeight = 2560, 1440
	if width, height := game.Layout(1280, 720); width != 2560 || height != 1440 {
		t.Fatalf("Layout = %dx%d, want 2560x1440", width, height)
	}
	if width, height := game.logicalWindowSize(2560, 1440); width != 1280 || height != 720 {
		t.Fatalf("logical window size = %dx%d, want 1280x720", width, height)
	}
}

func TestAnime4KOutputScaleKeepsWindowLogicalAndLayoutDeviceSized(t *testing.T) {
	services := runtime.DefaultServices()
	session := runtime.NewSession(services, 1000)
	defer session.Close()

	game := NewGame(session)
	game.SetOutputScale(2)
	game.imageWidth, game.imageHeight = 2560, 1440
	if width, height := game.Layout(1280, 720); width != 2560 || height != 1440 {
		t.Fatalf("Layout = %dx%d, want 2560x1440", width, height)
	}
	if width, height := game.logicalWindowSize(2560, 1440); width != 1280 || height != 720 {
		t.Fatalf("logical window size = %dx%d, want 1280x720", width, height)
	}
}

func TestAnime4KOutputScaleUpdatesInputPoller(t *testing.T) {
	services := runtime.DefaultServices()
	session := runtime.NewSession(services, 1000)
	defer session.Close()

	game := NewGame(session)
	game.SetOutputScale(2)
	if game.poller.scale != 2 {
		t.Fatalf("poller scale = %d, want 2", game.poller.scale)
	}
	if got, want := game.poller.logical(2560), int32(1280); got != want {
		t.Fatalf("logical cursor = %d, want %d", got, want)
	}
}
