package runtime

import (
	"strings"
	"testing"

	"github.com/eslzzyl/krkrgo/internal/tjs"
)

func TestPadLogWindowScriptContract(t *testing.T) {
	session := NewSession(DefaultServices(), 10_000)
	defer session.Close()
	value, err := session.Evaluate(`
		class LogWindowPad extends Pad {
			function LogWindowPad() {
				super.Pad();
				borderStyle = 3; color = 0; fontColor = 0xffffff;
				fontFace = "sans"; readOnly = false; wordWrap = true;
				showScrollBars = 2; height = 10; title = "Log";
				registerExEvent(); clear();
			}
			function clear() { text = ""; statusText = "ready"; }
			function setPos(x, y, w, h) { left=x; top=y; setSize(w,h); }
			function setSize(w, h) { width=w; height=h; }
			function print(shortmsg, fullmsg, tag) { text += shortmsg; }
			function updateText() {}
		}
		var pad = new LogWindowPad();
		pad.setPos(12, 34, 500, 240);
		pad.print("first", "full", "tag");
		pad.visible = true;
		var before = [pad.left, pad.top, pad.width, pad.height, pad.visible,
			pad.title, pad.text, pad.statusText, pad.showScrollBars].join("|");
		pad.bringToFront();
		pad.hide();
		before + "|" + pad.visible + "|" + (pad instanceof Pad)
	`)
	if err != nil {
		t.Fatalf("LogWindow Pad script failed: %v", err)
	}
	if got, want := value.Text(), "12|34|500|240|1|Log|first|ready|2|0|1"; got != want {
		t.Fatalf("Pad script result = %q, want %q", got, want)
	}
}

func TestPadNativeRegistryIsSessionLocal(t *testing.T) {
	first := NewSession(DefaultServices(), 10_000)
	defer first.Close()
	second := NewSession(DefaultServices(), 10_000)
	defer second.Close()
	instances := make([]tjs.ObjectID, 2)
	for i, session := range []*Session{first, second} {
		if _, err := session.Evaluate(`var p = new Pad(); p.text = "independent"; p.text`); err != nil {
			t.Fatal(err)
		}
		value, err := session.Evaluate("p")
		if err != nil {
			t.Fatal(err)
		}
		instances[i] = value.ObjectRef().Object
	}
	firstState, secondState := first.Services.Pads[instances[0]], second.Services.Pads[instances[1]]
	if firstState == nil || secondState == nil {
		t.Fatal("Pad native state was not initialized")
	}
	if firstState == secondState || firstState.properties["text"].Text() != "independent" || secondState.properties["text"].Text() != "independent" {
		t.Fatal("Pad native state was shared or did not retain text")
	}
}

func TestPadUnknownNativeOperationIsExplicit(t *testing.T) {
	session := NewSession(DefaultServices(), 10_000)
	defer session.Close()
	_, err := session.Evaluate(`var p = new Pad(); p.unsupportedPadOperation()`)
	if err == nil || !strings.Contains(err.Error(), "member not found") {
		t.Fatalf("unknown Pad method error = %v", err)
	}
}
