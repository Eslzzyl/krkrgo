package host

import (
	"math"
	"testing"

	"github.com/eslzzyl/krkrgo/internal/runtime"
)

func TestNativeOutputUsesOneToOneInputAndLayout(t *testing.T) {
	services := runtime.DefaultServices()
	session := runtime.NewSession(services, 1000)
	defer session.Close()

	game := NewGame(session)
	game.deviceScaleFactor = func() float64 { return 1 }
	if game.poller.scale != 1 {
		t.Fatalf("poller scale = %d, want 1", game.poller.scale)
	}
	if got, want := game.poller.logical(1280), int32(1280); got != want {
		t.Fatalf("logical cursor = %d, want %d", got, want)
	}
	game.imageWidth, game.imageHeight = 1280, 720
	if width, height := game.Layout(1280, 720); width != 1280 || height != 720 {
		t.Fatalf("Layout = %dx%d, want 1280x720", width, height)
	}
}

func TestWindowScaleAndOutputScaleUsePhysicalTarget(t *testing.T) {
	services := runtime.DefaultServices()
	session := runtime.NewSession(services, 1000)
	defer session.Close()

	game := NewGame(session)
	game.deviceScaleFactor = func() float64 { return 1 }
	game.SetWindowScale(1.5)
	game.SetOutputScale(2)
	width, height := game.logicalWindowSize(2560, 1440)
	deviceScale := 1.0
	wantWidth := int(math.Round(1280 * 2 * 1.5 / deviceScale))
	wantHeight := int(math.Round(720 * 2 * 1.5 / deviceScale))
	if width != wantWidth || height != wantHeight {
		t.Fatalf("window size = %dx%d, want %dx%d", width, height, wantWidth, wantHeight)
	}
	if game.poller.scale != 2 {
		t.Fatalf("poller scale = %d, want 2", game.poller.scale)
	}
}
