package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/eslzzyl/krkrgo/internal/host"
	"github.com/eslzzyl/krkrgo/internal/krkrgocli"
	"github.com/eslzzyl/krkrgo/internal/tjs"
)

func TestParseLaunchArgs(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want krkrgocli.LaunchOptions
	}{
		{name: "defaults", want: krkrgocli.LaunchOptions{Budget: ^uint64(0), Renderer: "gpu"}},
		{
			name: "project without diagnostic budget",
			args: []string{"-project", "/games/sample"},
			want: krkrgocli.LaunchOptions{Project: "/games/sample", Budget: ^uint64(0), Renderer: "gpu"},
		},
		{
			name: "project options",
			args: []string{"-project", "/games/sample", "-save-dir", "/tmp/sample-saves", "-budget", "42"},
			want: krkrgocli.LaunchOptions{Project: "/games/sample", SaveDir: "/tmp/sample-saves", Budget: 42, Renderer: "gpu"},
		},
		{
			name: "explicit CPU renderer",
			args: []string{"-renderer", "cpu"},
			want: krkrgocli.LaunchOptions{Budget: ^uint64(0), Renderer: "cpu"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := krkrgocli.ParseLaunchArgs(tt.args)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("options = %#v, want %#v", got, tt.want)
			}
		})
	}
}
func TestParseLaunchArgsRejectsUnknownRenderer(t *testing.T) {
	if _, err := krkrgocli.ParseLaunchArgs([]string{"-renderer", "vulkan"}); err == nil {
		t.Fatal("unknown renderer was accepted")
	}
}

func TestProjectStartupRunsWithoutExplicitBudget(t *testing.T) {
	project := t.TempDir()
	startup := []byte("var n=0; while(n<400000) n++;")
	if err := os.WriteFile(filepath.Join(project, "startup.tjs"), startup, 0o644); err != nil {
		t.Fatal(err)
	}

	opts, err := krkrgocli.ParseLaunchArgs([]string{"-project", project})
	if err != nil {
		t.Fatal(err)
	}
	session, err := krkrgocli.NewSession(opts.Project, opts.SaveDir, opts.Budget)
	if err != nil {
		t.Fatalf("launch project without -budget: %v", err)
	}
	defer session.Close()
	got, err := session.Evaluate("n")
	if err != nil {
		t.Fatal(err)
	}
	if value, _ := got.Int(); value != 400_000 {
		t.Fatalf("startup loop result = %v, want 400000", got)
	}
}

func TestProjectStartupUsesScriptWindowAsMainWindow(t *testing.T) {
	project := t.TempDir()
	startup := []byte(`
class KAGWindow extends Window {
	function KAGWindow() {
		super.Window();
		caption = "seen";
	}
}
window = new KAGWindow();
`)
	if err := os.WriteFile(filepath.Join(project, "startup.tjs"), startup, 0o644); err != nil {
		t.Fatal(err)
	}

	session, err := krkrgocli.NewSession(project, "", 10_000)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	caption, err := session.Evaluate("Window.mainWindow.caption")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := caption.Text(), "seen"; got != want {
		t.Fatalf("startup caption = %q, want %q", got, want)
	}

	mainWindow, err := session.Evaluate("Window.mainWindow")
	if err != nil {
		t.Fatal(err)
	}
	if mainWindow.Kind() != tjs.KindObject {
		t.Fatalf("Window.mainWindow = %#v", mainWindow)
	}
	scriptWindow, err := session.Evaluate("window")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := mainWindow.ObjectRef().Object, scriptWindow.ObjectRef().Object; got != want {
		t.Fatalf("startup main window = %d, script Window = %d", got, want)
	}
	if got, want := uint64(mainWindow.ObjectRef().Object), session.Services.MainWindow; got != want {
		t.Fatalf("startup main window = %d, registered main window = %d", got, want)
	}
	if got := len(session.Services.WindowObjects); got != 1 {
		t.Fatalf("registered Window count = %d, want only the script-created main Window", got)
	}
}

func TestNewSessionWithoutProjectKeepsEmptyHostWindow(t *testing.T) {
	session, err := krkrgocli.NewSession("", "", 10_000)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	root := session.Services.Root
	if root == nil || root.ChildCount() != 0 || root.Bounds.Width != host.DefaultWidth || root.Bounds.Height != host.DefaultHeight {
		t.Fatalf("empty host root = %#v", root)
	}
	if session.Services.MainWindow == 0 || session.Services.Window(tjs.ObjectID(session.Services.MainWindow)) == nil {
		t.Fatal("no-project launch has no registered host Window")
	}
	mainWindow, err := session.Evaluate("Window.mainWindow")
	if err != nil {
		t.Fatal(err)
	}
	if mainWindow.Kind() != tjs.KindObject {
		t.Fatalf("Window.mainWindow = %#v", mainWindow)
	}
	if got, want := uint64(mainWindow.ObjectRef().Object), session.Services.MainWindow; got != want {
		t.Fatalf("no-project main window = %d, registered host window = %d", got, want)
	}
}

func TestProjectStartupUsesScriptLayerAsCompositorRoot(t *testing.T) {
	project := t.TempDir()
	startup := []byte(`
class TestWindow extends Window {
	var primary;
	function TestWindow() {
		super.Window();
		primary = new Layer(this, null);
		primary.setSize(17, 13);
		add(primary);
	}
}
window = new TestWindow();
`)
	if err := os.WriteFile(filepath.Join(project, "startup.tjs"), startup, 0o644); err != nil {
		t.Fatal(err)
	}

	session, err := krkrgocli.NewSession(project, "", 10_000)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	primary, err := session.Evaluate("window.primary")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := session.Services.Root, session.Services.Layer(primary.ObjectRef().Object); got != want {
		t.Fatalf("compositor root = %p, primary Layer = %p", got, want)
	}
	owner, err := session.Evaluate("window.primary.window === window")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := owner.Truth(); err != nil || !ok {
		t.Fatalf("primary.window === window = %v, %v", ok, err)
	}
	frame, err := session.CaptureFrame()
	if err != nil {
		t.Fatal(err)
	}
	if frame.Width != 17 || frame.Height != 13 {
		t.Fatalf("frame size = %dx%d, want 17x13", frame.Width, frame.Height)
	}
}
