package launcher

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildArgs(t *testing.T) {
	game := Game{
		Project:     "/games/example",
		SaveDir:     "/saves/example",
		Budget:      5000,
		Renderer:    RendererCPU,
		RenderScale: 2,
	}
	args, err := BuildArgs(game)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"run", "-project", "/games/example", "-save-dir", "/saves/example", "-budget", "5000", "-renderer", "cpu", "-render-scale", "2"}
	if strings.Join(args, " ") != strings.Join(want, " ") {
		t.Fatalf("BuildArgs = %q, want %q", args, want)
	}
}

func TestBuildArgsDefaults(t *testing.T) {
	args, err := BuildArgs(Game{Project: "/games/example", Renderer: RendererGPU})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"run", "-project", "/games/example", "-renderer", "gpu"}
	if strings.Join(args, " ") != strings.Join(want, " ") {
		t.Fatalf("BuildArgs = %q, want %q", args, want)
	}
}

func TestValidateGame(t *testing.T) {
	cases := []struct {
		game Game
		ok   bool
	}{
		{Game{Project: "/p", Renderer: RendererGPU}, true},
		{Game{Project: "/p", Renderer: ""}, true},
		{Game{Project: "/p", Renderer: RendererCPU, RenderScale: 4}, true},
		{Game{Project: "", Renderer: RendererGPU}, false},
		{Game{Project: "/p", Renderer: "vulkan"}, false},
		{Game{Project: "/p", Renderer: RendererGPU, RenderScale: 5}, false},
		{Game{Project: "/p", Renderer: RendererGPU, RenderScale: -1}, false},
	}
	for _, tc := range cases {
		err := ValidateGame(tc.game)
		if (err == nil) != tc.ok {
			t.Fatalf("ValidateGame(%#v) = %v, want ok %v", tc.game, err, tc.ok)
		}
	}
}

func TestCatalogAddMoveRemove(t *testing.T) {
	c := EmptyCatalog()
	first, err := c.Add(Game{Title: "First", Project: filepath.Join(t.TempDir(), "first")})
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == "" || first.Renderer != RendererGPU || first.AddedAt == 0 {
		t.Fatalf("Add defaults = %#v", first)
	}
	if _, err := c.Add(Game{Title: "Again", Project: first.Project}); err == nil {
		t.Fatal("Add accepted the same project twice")
	}
	second, err := c.Add(Game{Title: "Second", Project: filepath.Join(t.TempDir(), "second")})
	if err != nil {
		t.Fatal(err)
	}
	c.Move(1, 0)
	if c.Games[0].ID != second.ID || c.Games[1].ID != first.ID {
		t.Fatalf("Move order = %#v", c.Games)
	}
	if !c.Remove(first.ID) || len(c.Games) != 1 || c.Games[0].ID != second.ID {
		t.Fatalf("Remove = %#v", c.Games)
	}
}

func TestVisibleGames(t *testing.T) {
	games := []Game{
		{ID: "a", Title: "beta", Project: "/games/beta", LastPlayedAt: 20, AddedAt: 3},
		{ID: "b", Title: "Alpha", Project: "/games/alpha", LastPlayedAt: 10, AddedAt: 2},
		{ID: "c", Title: "gamma", Project: "/other/gamma", LastPlayedAt: 0, AddedAt: 1},
	}
	ids := func(indices []int) string {
		var names []string
		for _, i := range indices {
			names = append(names, games[i].ID)
		}
		return strings.Join(names, ",")
	}
	if got := ids(VisibleGames(games, "", SortManual)); got != "a,b,c" {
		t.Fatalf("manual order = %s", got)
	}
	if got := ids(VisibleGames(games, "", SortTitle)); got != "b,a,c" {
		t.Fatalf("title order = %s", got)
	}
	if got := ids(VisibleGames(games, "", SortRecent)); got != "a,b,c" {
		t.Fatalf("recent order = %s", got)
	}
	if got := ids(VisibleGames(games, "", SortAdded)); got != "a,b,c" {
		t.Fatalf("added order = %s", got)
	}
	if got := ids(VisibleGames(games, "alpha", SortManual)); got != "b" {
		t.Fatalf("title filter = %s", got)
	}
	if got := ids(VisibleGames(games, "other", SortManual)); got != "c" {
		t.Fatalf("path filter = %s", got)
	}
}
