package runtime

import (
	"strings"
	"testing"
)

func TestGetAboutPluginRegistersSystemMethod(t *testing.T) {
	session := NewSession(DefaultServices(), 10_000)
	defer session.Close()
	if _, err := session.executeSource("<getabout-before-link>", `beforeAbout = System.getAboutString()`, false); err != nil {
		t.Fatal(err)
	}
	value, err := session.Evaluate(`beforeAbout`)
	if err != nil {
		t.Fatal(err)
	}
	if value.Text() != systemVersionInformation {
		t.Fatalf("System.getAboutString() before plugin link = %q, want %q", value.Text(), systemVersionInformation)
	}
	if _, err := session.executeSource("<getabout-link-and-call>", `Plugins.link("plugin/getabout.dll"); linkedAbout = System.getAboutString()`, false); err != nil {
		t.Fatal(err)
	}
	value, err = session.Evaluate(`linkedAbout`)
	if err != nil {
		t.Fatal(err)
	}
	if value.Text() != systemVersionInformation {
		t.Fatalf("System.getAboutString() during plugin link = %q, want %q", value.Text(), systemVersionInformation)
	}
	if _, err = session.executeSource("<getabout-after-link>", `afterAbout = System.getAboutString()`, false); err != nil {
		t.Fatal(err)
	}
	value, err = session.Evaluate(`afterAbout`)
	if err != nil {
		t.Fatal(err)
	}
	if value.Text() != systemVersionInformation {
		t.Fatalf("System.getAboutString() after plugin link = %q, want %q", value.Text(), systemVersionInformation)
	}
	if len(session.Services.Plugins) != 1 {
		t.Fatalf("getabout plugin list = %v", session.Services.Plugins)
	}
}

func TestWin32DialogMessageBoxUsesSystemInformSemantics(t *testing.T) {
	var request DialogRequest
	services := DefaultServices()
	services.Dialogs.SetHandler(func(r DialogRequest) (DialogResult, error) {
		request = r
		return DialogResult{ButtonIndex: 0, Accepted: true}, nil
	})
	session := NewSession(services, 10_000)
	defer session.Close()
	if _, err := session.Evaluate(`Plugins.link("PLUGIN\\Win32Dialog.DLL")`); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Evaluate(`Plugins.link("plugin/win32dialog.tpm")`); err != nil {
		t.Fatal(err)
	}
	value, err := session.Evaluate(`(new WIN32Dialog()).messageBox("body", "caption", 42)`)
	if err != nil {
		t.Fatal(err)
	}
	if value.IntegerValue() != 1 {
		t.Fatalf("WIN32Dialog.messageBox return = %d, want !void == true", value.IntegerValue())
	}
	if request.Title != "caption" || request.Message != "body" || len(request.Buttons) != 1 || request.Buttons[0] != "OK" {
		t.Fatalf("dialog request = %#v", request)
	}
	if len(services.Plugins) != 1 {
		t.Fatalf("case/extension normalized plugin list = %v", services.Plugins)
	}
}

func TestWin32DialogWithoutHandlerIsUnsupported(t *testing.T) {
	session := NewSession(DefaultServices(), 10_000)
	defer session.Close()
	if _, err := session.Evaluate(`Plugins.link("win32dialog.dll")`); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Evaluate(`(new WIN32Dialog()).messageBox("body", "caption", 2)`); err == nil || !strings.Contains(err.Error(), ErrDialogUnavailable.Error()) {
		t.Fatalf("WIN32Dialog.messageBox without handler error = %v", err)
	}
}

func TestFFTGraphPluginNeverClaimsDrawingSuccess(t *testing.T) {
	session := NewSession(DefaultServices(), 10_000)
	defer session.Close()
	if _, err := session.Evaluate(`Plugins.link("plugin\\FFTGraph.TPM")`); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Evaluate(`Plugins.link("PLUGIN/fftgraph.dll")`); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Evaluate(`drawFFTGraph()`); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("drawFFTGraph error = %v, want explicit unsupported renderer error", err)
	}
	if len(session.Services.Plugins) != 1 {
		t.Fatalf("case/extension normalized plugin list = %v", session.Services.Plugins)
	}
}
