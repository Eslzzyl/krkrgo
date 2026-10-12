package runtime

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/eslzzyl/krkrgo/internal/kag"
)

func TestKAGParserStoreRestore(t *testing.T) {
	p, err := NewKAGParser("scenario.ks", "*start\nhello [r]world\n")
	if err != nil {
		t.Fatal(err)
	}
	if tok := p.NextToken(); tok == nil || tok.Kind != kag.KindLabel {
		t.Fatalf("first token = %#v", tok)
	}
	if got := p.GetCurLineStr(); got != "*start" {
		t.Fatalf("current line after label = %q, want %q", got, "*start")
	}
	saved := p.Store()
	if saved.CurrentLine != 1 {
		t.Fatalf("stored current line = %d, want 1", saved.CurrentLine)
	}
	if tok := p.NextToken(); tok == nil || tok.Kind != kag.KindText {
		t.Fatalf("second token = %#v", tok)
	}
	if tok := p.NextToken(); tok == nil || tok.Kind != kag.KindTag {
		t.Fatalf("third token = %#v", tok)
	}
	if err := p.RestoreState(saved); err != nil {
		t.Fatal(err)
	}
	if got := p.GetCurLineStr(); got != "*start" {
		t.Fatalf("restored current line = %q, want %q", got, "*start")
	}
	if tok := p.NextToken(); tok == nil || tok.Kind != kag.KindText {
		t.Fatalf("restored token = %#v", tok)
	}
	object := p.StoreObject()
	if _, ok := object.Get("position"); !ok {
		t.Fatal("TJS state omitted position")
	}
	if got, ok := object.Get("current_line"); !ok || got.IntegerValue() != 2 {
		t.Fatalf("TJS state current_line = %v, %v; want 2", got, ok)
	}
	if err := p.Restore(object); err != nil {
		t.Fatal(err)
	}
	if got := p.GetCurLineStr(); got != "hello [r]world" {
		t.Fatalf("object-restored current line = %q, want %q", got, "hello [r]world")
	}
}

func TestKAGParserRestoreAcceptsLegacyLineCheckpoint(t *testing.T) {
	savedParser, err := NewKAGParser("legacy.ks", "text\n;original comment")
	if err != nil {
		t.Fatal(err)
	}
	state := savedParser.Store()

	changedSource, err := NewKAGParser("legacy.ks", "text\n;changed comment")
	if err != nil {
		t.Fatal(err)
	}
	if err := changedSource.RestoreState(state); err == nil {
		t.Fatal("checkpoint accepted a scenario with different physical lines")
	}

	state.Fingerprint = legacyScenarioFingerprint(savedParser.Scenario())
	state.CurrentLine = 0
	if err := changedSource.RestoreState(state); err != nil {
		t.Fatalf("legacy checkpoint was not accepted: %v", err)
	}
	if got := changedSource.GetCurLineStr(); got != "text" {
		t.Fatalf("legacy checkpoint current line = %q, want best-effort %q", got, "text")
	}
}

func TestSaveStorageAtomicContained(t *testing.T) {
	project := t.TempDir()
	s, err := NewSaveStorage(project, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Write("slot/one.sav", []byte("new")); err != nil {
		t.Fatal(err)
	}
	got, err := s.Read("slot/one.sav")
	if err != nil || string(got) != "new" {
		t.Fatalf("read = %q, %v", got, err)
	}
	uri := localStorageURI(filepath.Join(project, "savedata", "slot", "uri.sav"))
	if err := s.Write(uri, []byte("uri")); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Read(uri); err != nil || string(got) != "uri" {
		t.Fatalf("URI read = %q, %v", got, err)
	}
	if err := s.Write("../escape", []byte("bad")); err == nil || !errors.Is(err, ErrInvalidSaveName) {
		t.Fatalf("escape error = %v", err)
	}
	outside := filepath.Join(s.Root, "outside")
	if err := os.Symlink(t.TempDir(), outside); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := s.Write("outside/escape", []byte("bad")); err == nil {
		t.Fatal("write followed symlink parent")
	}
}

func TestLocalFileURIStorageAndSystemDataPath(t *testing.T) {
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "startup.tjs"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	projectFile := filepath.Join(project, "project.txt")
	if err := os.WriteFile(projectFile, []byte("project content"), 0o644); err != nil {
		t.Fatal(err)
	}
	saveDir := filepath.Join(t.TempDir(), "saves")
	session, err := OpenSession(project, saveDir, 50_000)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	dataPathValue, err := session.Evaluate("System.dataPath")
	if err != nil {
		t.Fatal(err)
	}
	wantDataPath := localStorageDirectoryURI(saveDir)
	if got := dataPathValue.Text(); got != wantDataPath {
		t.Fatalf("System.dataPath = %q, want %q", got, wantDataPath)
	}
	if _, err := session.Evaluate(`TextStream.write(System.dataPath + "slot/data.txt", "save content")`); err != nil {
		t.Fatal(err)
	}
	value, err := session.Evaluate(`TextStream.read(System.dataPath + "slot/data.txt")`)
	if err != nil || value.Text() != "save content" {
		t.Fatalf("read through System.dataPath = %q, %v", value.Text(), err)
	}
	if value, err := session.Evaluate(`Storages.isExistentStorage(System.dataPath + "slot/data.txt")`); err != nil {
		t.Fatal(err)
	} else if got, _ := value.Int(); got != 1 {
		t.Fatalf("save URI existence = %d, want 1", got)
	}
	if data, err := os.ReadFile(filepath.Join(saveDir, "slot", "data.txt")); err != nil || string(data) != "save content" {
		t.Fatalf("System.dataPath write = %q, %v", data, err)
	}

	projectURI := localStorageURI(projectFile)
	if value, err := session.Evaluate("Storages.isExistentStorage(" + strconv.Quote(projectURI) + ")"); err != nil {
		t.Fatal(err)
	} else if got, _ := value.Int(); got != 1 {
		t.Fatalf("project URI existence = %d, want 1", got)
	}
	value, err = session.Evaluate("TextStream.read(" + strconv.Quote(projectURI) + ")")
	if err != nil || value.Text() != "project content" {
		t.Fatalf("project URI read = %q, %v", value.Text(), err)
	}
	if _, err := session.Evaluate("TextStream.write(" + strconv.Quote(projectURI) + `, "save overlay")`); err != nil {
		t.Fatal(err)
	}
	value, err = session.Evaluate("TextStream.read(" + strconv.Quote(projectURI) + ")")
	if err != nil || value.Text() != "save overlay" {
		t.Fatalf("project URI overlay read = %q, %v", value.Text(), err)
	}
	if data, err := os.ReadFile(projectFile); err != nil || string(data) != "project content" {
		t.Fatalf("project file changed through save URI: %q, %v", data, err)
	}
	if data, err := os.ReadFile(filepath.Join(saveDir, "project.txt")); err != nil || string(data) != "save overlay" {
		t.Fatalf("project URI write did not use save overlay: %q, %v", data, err)
	}

	if _, err := session.Evaluate(`TextStream.write("relative.sav", "relative")`); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(saveDir, "relative.sav")); err != nil || string(data) != "relative" {
		t.Fatalf("relative storage write = %q, %v", data, err)
	}
	outsideURI := localStorageURI(filepath.Join(t.TempDir(), "outside.txt"))
	if _, err := session.Evaluate("TextStream.write(" + strconv.Quote(outsideURI) + `, "outside")`); err == nil || !errors.Is(err, ErrInvalidSaveName) {
		t.Fatalf("outside project URI write error = %v, want ErrInvalidSaveName", err)
	}
	if value, err := session.Evaluate("Storages.isExistentStorage(" + strconv.Quote(outsideURI) + ")"); err != nil {
		t.Fatal(err)
	} else if got, _ := value.Int(); got != 0 {
		t.Fatalf("outside project URI existence = %d, want 0", got)
	}
	headless := NewSession(DefaultServices(), 50_000)
	defer headless.Close()
	headlessSaveDir := filepath.Join(t.TempDir(), "saves")
	headless.Services.SetSaveDir(headlessSaveDir)
	if _, err := headless.Evaluate(`TextStream.write(System.dataPath + "headless.txt", "headless save")`); err != nil {
		t.Fatal(err)
	}
	value, err = headless.Evaluate(`TextStream.read(System.dataPath + "headless.txt")`)
	if err != nil || value.Text() != "headless save" {
		t.Fatalf("headless System.dataPath read = %q, %v", value.Text(), err)
	}
	if value, err := headless.Evaluate(`Storages.isExistentStorage(System.dataPath + "headless.txt")`); err != nil {
		t.Fatal(err)
	} else if got, _ := value.Int(); got != 1 {
		t.Fatalf("headless save URI existence = %d, want 1", got)
	}
	if data, err := os.ReadFile(filepath.Join(headlessSaveDir, "headless.txt")); err != nil || string(data) != "headless save" {
		t.Fatalf("headless save file = %q, %v", data, err)
	}

}

func TestTextStreamWriteOffsetAndSaveStruct(t *testing.T) {
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "startup.tjs"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	saveDir := filepath.Join(t.TempDir(), "saves")
	session, err := OpenSession(project, saveDir, 100_000)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	if _, err := session.Evaluate(`TextStream.write(System.dataPath + "thumbnail.bmp", "BMP-prefix")`); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Evaluate(`TextStream.write(System.dataPath + "thumbnail.bmp", "replacement", "o4")`); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Evaluate(`TextStream.write(System.dataPath + "thumbnail.bmp", "+", "o15")`); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(saveDir, "thumbnail.bmp"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "BMP-replacement+" {
		t.Fatalf("overwrite and append result = %q, want %q", data, "BMP-replacement+")
	}

	if _, err := session.executeSource("<save-struct-offset-test>", `dict = new Dictionary(); dict.answer = 42; (Dictionary.saveStruct incontextof dict)(System.dataPath + "thumbnail.bmp", "zo10");`, false); err != nil {
		t.Fatal(err)
	}

	data, err = os.ReadFile(filepath.Join(saveDir, "thumbnail.bmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 10 || string(data[:10]) != "BMP-replac" {
		t.Fatalf("offset write changed thumbnail prefix: %q", data)
	}
	value, err := session.Evaluate(`Scripts.evalStorage("thumbnail.bmp", "o10").answer`)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := value.Int(); err != nil || got != 42 {
		t.Fatalf("saveStruct offset value = %v, %v; want 42", value, err)
	}
}

func TestInjectedServicesAndPluginIdempotence(t *testing.T) {
	d := NewDialogService(func(r DialogRequest) (DialogResult, error) { return DialogResult{Accepted: true, ButtonIndex: 0}, nil })
	ok, err := d.Confirm("title", "message")
	if err != nil || !ok {
		t.Fatalf("confirm = %v, %v", ok, err)
	}
	calls := 0
	registry := NewPluginRegistry()
	if err := registry.Register("sample", func() error { calls++; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register("sample", func() error { calls += 100; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := registry.Install("sample", nil); err != nil {
		t.Fatal(err)
	}
	if err := registry.Install("sample", nil); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("plugin calls = %d", calls)
	}
}
