package runtime

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/eslzzyl/krkrgo/internal/assets"
	"github.com/eslzzyl/krkrgo/internal/core"
)

func TestLayerExMovieUsesVideoOverlay(t *testing.T) {
	project := t.TempDir()
	entropy := runtimeAMVZeroEntropy()
	movieData := runtimeAMV([][]byte{entropy, entropy}, []byte{17, 231})
	if err := os.WriteFile(filepath.Join(project, "movie.amv"), movieData, 0o600); err != nil {
		t.Fatal(err)
	}
	storage, err := assets.NewStorage(project, core.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	session := NewSession(NewServices(storage, "", core.DefaultLimits()), 100_000)
	t.Cleanup(func() { _ = session.Close() })
	if _, err := session.Evaluate(`Plugins.link("plugin/layerExMovie.dll")`); err != nil {
		t.Fatal(err)
	}

	value, err := session.Evaluate(`layer = new Layer(); layer.openMovie("movie.amv", false); layer.hasImage && layer.imageWidth == 16 && layer.imageHeight == 16`)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := value.Truth()
	if err != nil || !opened {
		t.Fatalf("opened layer state = %v, err=%v", value, err)
	}
	layerValue, err := session.Evaluate("layer")
	if err != nil {
		t.Fatal(err)
	}
	layerID := layerValue.ObjectRef().Object
	layer := session.Services.Layer(layerID)
	if layer == nil {
		t.Fatal("movie layer was not registered")
	}

	value, err = session.Evaluate(`layer.startMovie(true); layer.isPlayingMovie()`)
	if err != nil {
		t.Fatal(err)
	}
	playing, err := value.Truth()
	if err != nil || !playing {
		t.Fatalf("playing state = %v, err=%v", value, err)
	}
	if err := session.AdvanceClock(500); err != nil {
		t.Fatal(err)
	}
	if len(layer.RGBA) < 4 || layer.RGBA[3] != 231 {
		t.Fatalf("advanced movie layer alpha = %d", layer.RGBA[3])
	}

	value, err = session.Evaluate(`layer.stopMovie(); layer.isPlayingMovie()`)
	if err != nil {
		t.Fatal(err)
	}
	playing, err = value.Truth()
	if err != nil || playing {
		t.Fatalf("stopped state = %v, err=%v", value, err)
	}
	if session.Services.LayerMovies[layerID] != nil {
		t.Fatal("stopped movie remained attached to the layer")
	}

	if _, err := session.Evaluate(`layer.openMovie("movie.amv", false); layer.startMovie(false)`); err != nil {
		t.Fatal(err)
	}
	if err := session.AdvanceClock(2000); err != nil {
		t.Fatal(err)
	}
	if movie := session.Services.LayerMovies[layerID]; movie != nil {
		_, source := movie.Source()
		t.Fatalf("ended movie remained attached to the layer: status=%q position=%d source=%t", movie.Status(), movie.Position(), source)
	}

	if _, err := session.Evaluate(`layer.openMovie("movie.amv", true)`); err != nil {
		t.Fatal(err)
	}
	if layer.ImageWidth != 8 || layer.ImageHeight != 16 || layer.Type != LayerAlpha {
		t.Fatalf("alpha movie layer = type %d, image %dx%d", layer.Type, layer.ImageWidth, layer.ImageHeight)
	}
}
