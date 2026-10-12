package runtime

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/eslzzyl/krkrgo/internal/tjs"
)

func TestSystemNativePropertiesAndMethods(t *testing.T) {
	services := DefaultServices()
	session := NewSession(services, 10000)
	defer session.Close()
	for _, source := range []string{"System.eventDisabled = true", "System.exitOnWindowClose = false", "System.drawThreadNum = 0", "System.exitOnNoWindowStartup = false"} {
		if _, err := session.Evaluate(source); err != nil {
			t.Fatalf("%s: %v", source, err)
		}
	}
	value, err := session.Evaluate(`System.eventDisabled == true && System.exitOnWindowClose == false && System.drawThreadNum == 0 && System.exitOnNoWindowStartup == false`)
	if err != nil {
		t.Fatal(err)
	}
	truth, err := value.Truth()
	if err != nil || !truth {
		t.Fatalf("System mutable properties = %v, %v", value, err)
	}
	value, err = session.Evaluate(`[System.versionString, System.versionInformation, System.platformName, System.osName, System.processorNum, System.exeBits, System.osBits]`)
	if err != nil {
		t.Fatal(err)
	}
	items, err := session.VM.ArrayItems(value)
	if err != nil || len(items) != 7 {
		t.Fatalf("System information = %v, %v", items, err)
	}
	if items[0].Text() != "krkrgo" || items[2].Text() == "" || items[3].Text() == "" {
		t.Fatalf("System information values = %#v", items)
	}
	for _, item := range items[4:] {
		if n, err := item.Int(); err != nil || n <= 0 {
			t.Fatalf("System numeric property = %v, %v", item, err)
		}
	}
	color, err := session.Evaluate(`System.toActualColor(4279383126)`)
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := color.Int(); n != 0x123456 {
		t.Fatalf("System.toActualColor = %#x", n)
	}
	for range 8 {
		uuid, err := session.Evaluate("System.createUUID()")
		if err != nil || !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(uuid.Text()) {
			t.Fatalf("System.createUUID = %q, %v", uuid.Text(), err)
		}
	}
	if _, err := session.Evaluate("System.exit(3)"); !errors.Is(err, ErrSystemExit) {
		t.Fatalf("System.exit error = %v", err)
	}
	if !services.Terminated || services.TerminationCode != 3 {
		t.Fatalf("termination state = %v/%d", services.Terminated, services.TerminationCode)
	}
}
func TestSystemReadRegValueIsARegistryMethod(t *testing.T) {
	session := NewSession(DefaultServices(), 10000)
	defer session.Close()

	value, err := session.Evaluate(`typeof System.readRegValue == "Object"`)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := value.Truth(); err != nil || !ok {
		t.Fatalf("System.readRegValue is not registered: %v, %v", value, err)
	}
	value, err = session.Evaluate(`typeof readRegValue`)
	if err != nil {
		t.Fatal(err)
	}
	if value.Text() == "Object" {
		t.Fatal("System.readRegValue leaked into the global namespace")
	}
	value, err = session.Evaluate(`System.readRegValue("HKEY_CURRENT_USER\\Software\\krkrgo-test-key-that-does-not-exist\\missing-value")`)
	if err != nil {
		t.Fatal(err)
	}
	if value.Kind() != tjs.KindVoid {
		t.Fatalf("missing registry value = %v, want void", value)
	}
	if _, err := session.Evaluate(`System.readRegValue(42)`); err == nil {
		t.Fatal("System.readRegValue accepted a non-string key")
	}
}

func TestSystemGraphicCacheLimitCompatibility(t *testing.T) {
	services := DefaultServices()
	session := NewSession(services, 10000)
	defer session.Close()

	autoLimit := services.graphicCacheSystemLimit
	tests := []struct {
		name   string
		source string
		want   uint64
	}{
		{name: "automatic", source: "System.graphicCacheLimit = gcsAuto; System.graphicCacheLimit", want: autoLimit},
		{name: "positive", source: "System.graphicCacheLimit = 17; System.graphicCacheLimit", want: 17},
		{name: "negative", source: "System.graphicCacheLimit = -2; System.graphicCacheLimit", want: autoLimit},
		{name: "zero", source: "System.graphicCacheLimit = 0; System.graphicCacheLimit", want: 0},
		{name: "positive capped at automatic maximum", source: "System.graphicCacheLimit = 999999999; System.graphicCacheLimit", want: autoLimit},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := session.Evaluate(test.source[:strings.Index(test.source, ";")]); err != nil {
				t.Fatalf("%s: %v", test.source, err)
			}
			value, err := session.Evaluate("System.graphicCacheLimit")
			if err != nil {
				t.Fatalf("%s: %v", test.source, err)
			}
			if got := value.IntegerValue(); got != int64(test.want) {
				t.Fatalf("%s = %d, want %d", test.source, got, test.want)
			}
		})
	}
}

func TestSystemStartupNatives(t *testing.T) {
	services := DefaultServices()
	session := NewSession(services, 10000)
	defer session.Close()

	lock, err := session.Evaluate(`System.createAppLock("startup")`)
	if err != nil || lock.IntegerValue() != 1 {
		t.Fatalf("first System.createAppLock = %v, %v", lock, err)
	}
	lock, err = session.Evaluate(`System.createAppLock("startup")`)
	if err != nil || lock.IntegerValue() != 0 {
		t.Fatalf("repeated System.createAppLock = %v, %v", lock, err)
	}
	sharedSession := NewSession(services, 10000)
	defer sharedSession.Close()
	lock, err = sharedSession.Evaluate(`System.createAppLock("startup")`)
	if err != nil || lock.IntegerValue() != 0 {
		t.Fatalf("System.createAppLock across sessions sharing services = %v, %v", lock, err)
	}

	isolatedServices := DefaultServices()
	isolatedSession := NewSession(isolatedServices, 10000)
	defer isolatedSession.Close()
	lock, err = isolatedSession.Evaluate(`System.createAppLock("startup")`)
	if err != nil || lock.IntegerValue() != 1 {
		t.Fatalf("System.createAppLock leaked across services = %v, %v", lock, err)
	}

	if _, err := session.Evaluate(`System.inform("startup message", "caption")`); err != nil {
		t.Fatalf("System.inform = %v", err)
	}
	if len(services.Messages) != 1 || services.Messages[0] != "startup message" {
		t.Fatalf("System.inform messages = %v", services.Messages)
	}
	if _, err := session.Evaluate(`Debug.logAsError()`); err != nil {
		t.Fatalf("Debug.logAsError = %v", err)
	}
	if !services.TraceEnabled {
		t.Fatal("Debug.logAsError did not enable trace logging")
	}

	for _, source := range []string{
		`System.createAppLock()`,
		`System.inform()`,
		`System.inform("text", "caption", "extra")`,
		`Debug.logAsError("extra")`,
	} {
		if _, err := session.Evaluate(source); err == nil {
			t.Fatalf("%s unexpectedly succeeded", source)
		}
	}
}

func TestSystemArgumentNatives(t *testing.T) {
	services := DefaultServices()
	session := NewSession(services, 10000)
	defer session.Close()

	value, err := session.Evaluate(`typeof System.getArgument("-ovr")`)
	if err != nil || value.Text() != "void" {
		t.Fatalf("System.getArgument with no command-line arguments = %v, %v", value, err)
	}

	services.mu.Lock()
	services.Arguments["-ovr"] = `System.title = "override"`
	services.mu.Unlock()
	value, err = session.Evaluate(`System.getArgument("-ovr")`)
	if err != nil || value.Text() != `System.title = "override"` {
		t.Fatalf("System.getArgument existing argument = %v, %v", value, err)
	}

	if _, err = session.Evaluate(`System.setArgument("-synthetic", 123)`); err != nil {
		t.Fatal(err)
	}
	value, err = session.Evaluate(`typeof System.getArgument("-synthetic") + "|" + System.getArgument("-synthetic")`)
	if err != nil || value.Text() != "String|123" {
		t.Fatalf("System.setArgument numeric value = %v, %v", value, err)
	}
	if _, err = session.Evaluate(`System.setArgument("bare", "x")`); err != nil {
		t.Fatal(err)
	}
	value, err = session.Evaluate(`typeof System.getArgument("BARE")`)
	if err != nil || value.Text() != "void" {
		t.Fatalf("System.getArgument case sensitivity = %v, %v", value, err)
	}
	if _, err = session.Evaluate(`System.setArgument("-synthetic", void)`); err != nil {
		t.Fatal(err)
	}
	value, err = session.Evaluate(`typeof System.getArgument("-synthetic") + "|" + System.getArgument("-synthetic")`)
	if err != nil || value.Text() != "String|" {
		t.Fatalf("System.setArgument void value = %v, %v", value, err)
	}

	for _, source := range []string{`System.getArgument()`, `System.setArgument("missing-value")`} {
		if _, err := session.Evaluate(source); err == nil {
			t.Fatalf("%s unexpectedly succeeded", source)
		}
	}
}

func TestSystemContinuousHandlersAndEventDisabled(t *testing.T) {
	services := DefaultServices()
	session := NewSession(services, 10000)
	defer session.Close()
	if _, err := session.executeSource("<system-test>", "var continuousCalls = 0; function continuous(t){ continuousCalls++; } System.addContinuousHandler(continuous);", false); err != nil {
		t.Fatal(err)
	}
	if err := session.Tick(1); err != nil {
		t.Fatal(err)
	}
	value, err := session.Evaluate("continuousCalls")
	if err != nil || value.IntegerValue() != 1 {
		t.Fatalf("continuousCalls = %v, %v", value, err)
	}
	if _, err := session.Evaluate("System.eventDisabled = true"); err != nil {
		t.Fatal(err)
	}
	if err := session.Tick(2); err != nil {
		t.Fatal(err)
	}
	value, err = session.Evaluate("continuousCalls")
	if err != nil || value.IntegerValue() != 1 {
		t.Fatalf("disabled continuousCalls = %v, %v", value, err)
	}
	for _, source := range []string{"System.eventDisabled = false", "System.removeContinuousHandler(continuous)"} {
		if _, err := session.Evaluate(source); err != nil {
			t.Fatalf("%s: %v", source, err)
		}
	}
	if err := session.Tick(3); err != nil {
		t.Fatal(err)
	}
	value, err = session.Evaluate("continuousCalls")
	if err != nil || value.IntegerValue() != 1 {
		t.Fatalf("removed continuousCalls = %v, %v", value, err)
	}
}
func TestSystemContinuousHandlersKeepBoundContextsDistinct(t *testing.T) {
	session := NewSession(DefaultServices(), 10_000)
	defer session.Close()
	if _, err := session.executeSource("<system-bound-handlers-test>", `
		class Handler {
			var calls = 0;
			function run(tick) { calls++; }
		}
		global.leftHandler = new Handler();
		global.rightHandler = new Handler();
		System.addContinuousHandler(leftHandler.run);
		System.addContinuousHandler(rightHandler.run);
	`, false); err != nil {
		t.Fatal(err)
	}
	if err := session.Tick(1); err != nil {
		t.Fatal(err)
	}
	value, err := session.Evaluate("leftHandler.calls == 1 && rightHandler.calls == 1")
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := value.Truth(); !ok {
		t.Fatal("bound continuous handlers on distinct instances were deduplicated")
	}
	if _, err := session.Evaluate("System.removeContinuousHandler(leftHandler.run)"); err != nil {
		t.Fatal(err)
	}
	if err := session.Tick(2); err != nil {
		t.Fatal(err)
	}
	value, err = session.Evaluate("leftHandler.calls == 1 && rightHandler.calls == 2")
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := value.Truth(); !ok {
		t.Fatal("removing one bound method removed another instance's handler")
	}
}

func TestSystemContinuousHandlerMutationUsesTickSnapshot(t *testing.T) {
	services := DefaultServices()
	session := NewSession(services, 10_000)
	defer session.Close()
	if _, err := session.executeSource("<system-continuous-snapshot-test>", `
		var calls = 0;
		function first(t) {
			calls = calls * 10 + 1;
			System.removeContinuousHandler(second);
			System.addContinuousHandler(third);
		}
		function second(t) { calls = calls * 10 + 2; }
		function third(t) { calls = calls * 10 + 3; }
		System.addContinuousHandler(first);
		System.addContinuousHandler(second);
	`, false); err != nil {
		t.Fatal(err)
	}
	if err := session.Tick(1); err != nil {
		t.Fatal(err)
	}
	value, err := session.Evaluate("calls")
	if err != nil || value.IntegerValue() != 12 {
		t.Fatalf("first snapshot calls = %v, %v; want 12", value, err)
	}
	if err := session.Tick(2); err != nil {
		t.Fatal(err)
	}
	value, err = session.Evaluate("calls")
	if err != nil || value.IntegerValue() != 1213 {
		t.Fatalf("second snapshot calls = %v, %v; want 1213", value, err)
	}
}

func TestSystemContinuousHandlerErrorRemovesFailingHandler(t *testing.T) {
	services := DefaultServices()
	session := NewSession(services, 10_000)
	defer session.Close()
	if _, err := session.executeSource("<system-continuous-error-test>", `
		var calls = 0;
		function broken(t) { calls++; throw "continuous failure"; }
		System.addContinuousHandler(broken);
	`, false); err != nil {
		t.Fatal(err)
	}
	if err := services.invokeContinuousHandlers(session.VM, 1, &session.Budget); err == nil {
		t.Fatal("failing continuous callback returned no error")
	}
	if len(services.ContinuousHandlers) != 0 {
		t.Fatalf("failing handler remains registered: %d", len(services.ContinuousHandlers))
	}
	if err := services.invokeContinuousHandlers(session.VM, 2, &session.Budget); err != nil {
		t.Fatalf("second dispatch after failing handler: %v", err)
	}
	value, err := session.Evaluate("calls")
	if err != nil || value.IntegerValue() != 1 {
		t.Fatalf("failing callback calls = %v, %v; want 1", value, err)
	}
}

func TestSystemUnsupportedCacheOperationIsExplicit(t *testing.T) {
	session := NewSession(DefaultServices(), 10000)
	defer session.Close()
	if _, err := session.Evaluate("System.clearGraphicCache()"); !errors.Is(err, ErrUnsupportedDevice) {
		t.Fatalf("clearGraphicCache error = %v", err)
	}
	if _, err := session.Evaluate("System.assignMessage('missing', 'message')"); err != nil {
		t.Fatal(err)
	}
	value, err := session.Evaluate("System.assignMessage('missing', 'message')")
	if err != nil || value.IntegerValue() != 0 {
		t.Fatalf("assignMessage unknown = %v, %v", value, err)
	}
}
func TestSystemProjectEnvironmentPropertiesAndWindowTitle(t *testing.T) {
	for _, layout := range []struct {
		name    string
		useData bool
	}{
		{name: "project root"},
		{name: "project root with data directory", useData: true},
	} {
		t.Run(layout.name, func(t *testing.T) {
			project := t.TempDir()
			storageProject := project
			if layout.useData {
				storageProject = filepath.Join(project, "data")
				if err := os.MkdirAll(storageProject, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(storageProject, "startup.tjs"), nil, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(project, "game.exe"), nil, 0o644); err != nil {
				t.Fatal(err)
			}

			session, err := OpenSession(project, "", 10000)
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()

			project, err = filepath.EvalSymlinks(project)
			if err != nil {
				t.Fatal(err)
			}
			wantExePath := filepath.ToSlash(project)
			if !strings.HasSuffix(wantExePath, "/") {
				wantExePath += "/"
			}
			wantExeName := filepath.ToSlash(filepath.Join(project, "game.exe"))
			wantSaveURI := localStorageDirectoryURI(session.Services.SaveDir)

			value, err := session.Evaluate(`[System.exePath, System.exeName, System.dataPath, System.personalPath, System.appDataPath, System.title]`)
			if err != nil {
				t.Fatal(err)
			}
			items, err := session.VM.ArrayItems(value)
			if err != nil || len(items) != 6 {
				t.Fatalf("System project properties = %v, %v", items, err)
			}
			want := []string{wantExePath, wantExeName, wantSaveURI, wantSaveURI, wantSaveURI, "krkrgo"}
			for i, expected := range want {
				if got := items[i].Text(); got != expected {
					t.Errorf("System property %d = %q, want %q", i, got, expected)
				}
			}
			for _, source := range []string{`System.exePath = "override"`, `System.exeName = "override"`, `System.dataPath = "override"`} {
				_, _ = session.Evaluate(source)
			}
			after, err := session.Evaluate(`[System.exePath, System.exeName, System.dataPath]`)
			if err != nil {
				t.Fatal(err)
			}
			afterItems, err := session.VM.ArrayItems(after)
			if err != nil {
				t.Fatal(err)
			}
			for i := range want[:3] {
				if got := afterItems[i].Text(); got != want[i] {
					t.Errorf("read-only System property %d changed to %q, want %q", i, got, want[i])
				}
			}

			if _, err := session.Evaluate(`System.title = "app title"`); err != nil {
				t.Fatal(err)
			}
			if _, err := session.Evaluate(`window = new Window()`); err != nil {
				t.Fatal(err)
			}

			windowValue, err := session.Evaluate("window")
			if err != nil {
				t.Fatal(err)
			}
			window := session.Services.Window(windowValue.ObjectRef().Object)
			if window == nil || window.Caption != "app title" {
				t.Fatalf("window caption after System.title assignment = %#v", window)
			}
			if _, err := session.Evaluate(`window.caption = "window caption"`); err != nil {
				t.Fatal(err)
			}
			if got := session.Services.systemTitle(); got != "window caption" {
				t.Fatalf("System.title after Window.caption assignment = %q", got)
			}
			if _, err := session.Evaluate(`System.title = "second app title"`); err != nil {
				t.Fatal(err)
			}
			if got := window.Caption; got != "second app title" {
				t.Fatalf("window caption after second System.title assignment = %q", got)
			}
			value, err = session.Evaluate(`System.title == "second app title" && window.caption == "second app title"`)
			if err != nil {
				t.Fatal(err)
			}
			truth, err := value.Truth()
			if err != nil || !truth {
				t.Fatalf("System.title and Window.caption did not stay synchronized: %v, %v", value, err)
			}
		})
	}
}

func TestSystemProjectEnvironmentPropertiesWithoutProject(t *testing.T) {
	session := NewSession(DefaultServices(), 10000)
	defer session.Close()

	value, err := session.Evaluate(`[System.exePath, System.exeName, System.dataPath, System.personalPath, System.appDataPath, System.title]`)
	if err != nil {
		t.Fatal(err)
	}
	items, err := session.VM.ArrayItems(value)
	if err != nil || len(items) != 6 {
		t.Fatalf("System properties without project = %v, %v", items, err)
	}
	want := []string{"", "", "file://./", "file://./", "file://./", "krkrgo"}
	for i, expected := range want {
		if got := items[i].Text(); got != expected {
			t.Errorf("System property %d without project = %q, want %q", i, got, expected)
		}
	}
}

func TestSystemGetKeyStateTracksHeadlessInput(t *testing.T) {
	services := DefaultServices()
	session := NewSession(services, 10000)
	defer session.Close()
	if _, err := session.Evaluate(`window = new Window()`); err != nil {
		t.Fatal(err)
	}
	windowValue, err := session.Evaluate("window")
	if err != nil {
		t.Fatal(err)
	}
	windowID := uint64(windowValue.ObjectRef().Object)

	check := func(source string, want int64) {
		t.Helper()
		value, err := session.Evaluate(source)
		if err != nil {
			t.Fatalf("%s: %v", source, err)
		}
		got, err := value.Int()
		if err != nil || got != want {
			t.Fatalf("%s = %d, %v; want %d", source, got, err, want)
		}
	}

	check("System.getKeyState(65)", 0)
	check("System.getKeyState(65, false)", 0)
	if err := session.Input(windowID, KeyDown(65, 0)); err != nil {
		t.Fatal(err)
	}
	check("System.getKeyState(65)", 1)
	check("System.getKeyState(65, false)", 1)
	if err := session.Input(windowID, KeyUp(65, 0)); err != nil {
		t.Fatal(err)
	}
	check("System.getKeyState(65)", 0)

	if err := session.Input(windowID, KeyDown(65, 0)); err != nil {
		t.Fatal(err)
	}
	check("System.getKeyState(65, false)", 1)
	check("System.getKeyState(65, false)", 0)
	check("System.getKeyState(65)", 1)
	if err := session.Input(windowID, KeyUp(65, 0)); err != nil {
		t.Fatal(err)
	}

	if err := session.Input(windowID, KeyDown(65, 0)); err != nil {
		t.Fatal(err)
	}
	if err := session.Input(windowID, InputEvent{Kind: InputFocus, Focused: false}); err != nil {
		t.Fatal(err)
	}
	check("System.getKeyState(65)", 0)
	check("System.getKeyState(65, false)", 1)
	check("System.getKeyState(65, false)", 0)

	if err := session.Input(windowID, PointerDown(0, 0, 0, 0)); err != nil {
		t.Fatal(err)
	}
	check("System.getKeyState(1)", 1)
	if err := session.Input(windowID, PointerUp(0, 0, 0, 0)); err != nil {
		t.Fatal(err)
	}
	check("System.getKeyState(1)", 0)

	if _, err := session.Evaluate("System.getKeyState()"); err == nil {
		t.Fatal("System.getKeyState accepted a missing key")
	}
}

func TestSystemGetTickCountAdvancesDeterministicWaits(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()
	if _, err := session.executeSource("<virtual-wait-test>", `
function Wait(time) {
	var before = System.getTickCount();
	while(before + time >= System.getTickCount()) {}
}
var waitStarted = System.getTickCount();
Wait(3);
var waited = System.getTickCount() - waitStarted;
`, false); err != nil {
		t.Fatal(err)
	}
	waited, err := session.Evaluate("waited")
	if err != nil {
		t.Fatal(err)
	}
	if value, err := waited.Int(); err != nil || value < 3 || value > 5 {
		t.Fatalf("virtual wait duration = %v, %v; want 3..5 ms", waited, err)
	}
	if session.Services.TimeMS != 0 {
		t.Fatalf("busy wait advanced host clock to %d ms", session.Services.TimeMS)
	}
	if session.Budget == 0 {
		t.Fatal("busy wait exhausted the VM budget")
	}
	if err := session.Tick(20); err != nil {
		t.Fatal(err)
	}
	tick, err := session.Evaluate("System.getTickCount()")
	if err != nil {
		t.Fatal(err)
	}
	if value, err := tick.Int(); err != nil || value != 20 {
		t.Fatalf("tick count after replay tick = %v, %v; want 20", tick, err)
	}
}
