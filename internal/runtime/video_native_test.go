package runtime

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/eslzzyl/krkrgo/internal/assets"
	"github.com/eslzzyl/krkrgo/internal/core"
)

func TestEmptyWMVStatusCompletesMovieCloseFlow(t *testing.T) {
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "logo.wmv"), []byte("unused movie payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	storage, err := assets.OpenStorage(project, core.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	services := NewServices(storage, t.TempDir(), core.DefaultLimits())
	session := NewSession(services, 20_000)
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Errorf("close session: %v", err)
		}
	})

	_, err = session.executeSource("<empty-wmv-close-test>", `
class EmptyMovie extends VideoOverlay {
	var MovieState = "";
	function onStatusChanged(status) { this.MovieState = status; }
	function DefaultAction() {
		if (this.MovieState == "stop") {
			this.close();
			global.movieClosed = true;
		}
	}
}
movie = new EmptyMovie();
global.movieClosed = false;
movie.open("logo.wmv");
global.movieStateAfterOpen = movie.MovieState;
movie.play();
global.movieStateAfterPlay = movie.MovieState;
movie.DefaultAction();
`, false)
	if err != nil {
		t.Fatalf("empty WMV script: %v", err)
	}
	for expression, want := range map[string]string{
		"global.movieStateAfterOpen": "stop",
		"global.movieStateAfterPlay": "stop",
	} {
		value, err := session.Evaluate(expression)
		if err != nil || value.Text() != want {
			t.Errorf("%s = %v, err=%v; want %q", expression, value, err, want)
		}
	}
	closed, err := session.Evaluate("global.movieClosed")
	truth, truthErr := closed.Truth()
	if err != nil || truthErr != nil || !truth {
		t.Fatalf("CloseMovie flow completed = %v, eval err=%v, truth err=%v; want true", closed, err, truthErr)
	}
}
