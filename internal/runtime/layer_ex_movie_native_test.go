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
	if _, err := session.Evaluate(`
		movieEvents = "";
		layer.onStartMovie = function() { movieEvents = movieEvents + "start>"; };
		layer.onUpdateMovie = function() { movieEvents = movieEvents + "update>"; };
		layer.onStopMovie = function() { movieEvents = movieEvents + "stop>"; };
	`); err != nil {
		t.Fatal(err)
	}

	value, err = session.Evaluate(`layer.startMovie(true); movieEvents`)
	if err != nil {
		t.Fatal(err)
	}
	if value.Text() != "start>" {
		t.Fatalf("start callback sequence = %q", value.Text())
	}
	if err := session.AdvanceClock(500); err != nil {
		t.Fatal(err)
	}
	value, err = session.Evaluate("movieEvents")
	if err != nil {
		t.Fatal(err)
	}
	if value.Text() != "start>update>" {
		t.Fatalf("update callback sequence = %q", value.Text())
	}

	value, err = session.Evaluate(`layer.stopMovie(); movieEvents`)
	if err != nil {
		t.Fatal(err)
	}
	if value.Text() != "start>update>stop>" {
		t.Fatalf("manual stop callback sequence = %q", value.Text())
	}

	value, err = session.Evaluate(`movieEvents = ""; layer.openMovie("movie.amv", false); layer.startMovie(false); movieEvents`)
	if err != nil {
		t.Fatal(err)
	}
	if value.Text() != "start>" {
		t.Fatalf("one-shot start callback sequence = %q", value.Text())
	}
	if err := session.AdvanceClock(2000); err != nil {
		t.Fatal(err)
	}
	value, err = session.Evaluate("movieEvents")
	if err != nil {
		t.Fatal(err)
	}
	if value.Text() != "start>update>stop>" {
		t.Fatalf("ended movie callback sequence = %q", value.Text())
	}

	if _, err := session.Evaluate(`layer.openMovie("movie.amv", true)`); err != nil {
		t.Fatal(err)
	}
	if layer.ImageWidth != 8 || layer.ImageHeight != 16 || layer.Type != LayerAlpha {
		t.Fatalf("alpha movie layer = type %d, image %dx%d", layer.Type, layer.ImageWidth, layer.ImageHeight)
	}
}
