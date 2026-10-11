package runtime

import (
	"errors"
	"testing"
)

func TestLayerExMovieMethodsReportUnavailableVideoBackend(t *testing.T) {
	session := NewSession(DefaultServices(), 10_000)
	t.Cleanup(func() { _ = session.Close() })
	if _, err := session.Evaluate(`layer = new Layer()`); err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{
		`layer.openMovie("movie.mp4")`,
		`layer.startMovie()`,
		`layer.stopMovie()`,
		`layer.isPlayingMovie()`,
	} {
		if _, err := session.Evaluate(source); !errors.Is(err, ErrUnsupportedDevice) {
			t.Errorf("%s error = %v, want ErrUnsupportedDevice", source, err)
		}
	}
}
