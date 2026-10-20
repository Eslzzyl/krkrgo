package runtime

import (
	"testing"

	"github.com/eslzzyl/krkrgo/internal/tjs"
)

func TestWin32DialogComboBoxExInputHistoryAndFrame(t *testing.T) {
	services := DefaultServices()
	session := NewSession(services, 100_000)
	t.Cleanup(func() { _ = session.Close() })
	_, err := session.Evaluate(`
		Plugins.link("win32dialog.dll");
		window = new Window();
		class TestDialog extends WIN32Dialog {
			function TestDialog() { super.WIN32Dialog(null); }
			function onNotify(id, nm) {
				if (nm.code == -806 && nm.getDWord(0) == 2) {
					enteredText = getItemText(id);
					var blob = new global.WIN32Dialog.Blob(72);
					blob.setText(16, enteredText);
					sendItemMessage(id, 0x40B, 0, blob.pointer);
					setItemText(id, "");
					invalidate blob;
				}
			}
		}
		dialog = new TestDialog();
		header = new WIN32Dialog.Header();
		header.store(%[title:"Console", cx:365, cy:240]);
		item = new WIN32Dialog.Items();
		item.store(%[id:101, x:0, y:180, cx:300, cy:18, windowClass:COMBOBOXEX, style:WS_VISIBLE|WS_TABSTOP, title:""]);
		dialog.makeTemplate(header, item);
		controlsInitialized = WIN32Dialog.initCommonControlsEx(ICC_USEREX_CLASSES);
		dialog.open(null);
		dialog.setItemFocus(101);
	`)
	if err != nil {
		t.Fatalf("create ComboBoxEx dialog: %v", err)
	}
	if got := evalInteger(t, session, "ICC_USEREX_CLASSES"); got != 512 {
		t.Fatalf("ICC_USEREX_CLASSES = %d, want 512", got)
	}
	if got := evalInteger(t, session, "WS_CLIPCHILDREN"); got != 33554432 {
		t.Fatalf("WS_CLIPCHILDREN = %d, want 33554432", got)
	}
	if got := evalInteger(t, session, "controlsInitialized"); got != 1 {
		t.Fatalf("initCommonControlsEx result = %d, want 1", got)
	}
	if err := session.Input(services.MainWindow, TextInput("1 + 2")); err != nil {
		t.Fatalf("type command: %v", err)
	}
	if err := session.Input(services.MainWindow, KeyDown(0x0d, 0)); err != nil {
		t.Fatalf("press Enter: %v", err)
	}
	if value, err := session.Evaluate("enteredText"); err != nil || value.Text() != "1 + 2" {
		t.Fatalf("Enter notification text = %q, err=%v", value.Text(), err)
	}
	if got := evalInteger(t, session, "dialog.sendItemMessage(101, 0x146, 0, 0)"); got != 1 {
		t.Fatalf("combo history count = %d, want 1", got)
	}
	if value, err := session.Evaluate("dialog.getItemText(101)"); err != nil || value.Text() != "" {
		t.Fatalf("command text after Enter = %q, err=%v", value.Text(), err)
	}
	if err := session.Input(services.MainWindow, KeyDown(0x26, 0)); err != nil {
		t.Fatalf("press ArrowUp: %v", err)
	}
	if value, err := session.Evaluate("dialog.getItemText(101)"); err != nil || value.Text() != "1 + 2" {
		t.Fatalf("command history selection = %q, err=%v", value.Text(), err)
	}
	frame, _ := services.Win32DialogFrame(1280, 720)
	if frame == nil || frame.RGBAAt(0, 0).A != 0 || frame.RGBAAt(640, 360).A == 0 {
		t.Fatalf("dialog presentation frame is missing or misplaced: frame=%v", frame != nil)
	}
}

func TestWin32DialogTextInputContextAndIMECommit(t *testing.T) {
	services := DefaultServices()
	session := NewSession(services, 100_000)
	t.Cleanup(func() { _ = session.Close() })
	_, err := session.Evaluate(`
		Plugins.link("win32dialog.dll");
		window = new Window();
		changes = 0;
		class TestDialog extends WIN32Dialog {
			function TestDialog() { super.WIN32Dialog(null); }
			function onCommand(id, code) { changes = changes + 1; }
		}
		dialog = new TestDialog();
		header = new WIN32Dialog.Header();
		header.store(%[title:"Console", cx:365, cy:240]);
		item = new WIN32Dialog.Items();
		item.store(%[id:101, x:0, y:180, cx:300, cy:18, windowClass:COMBOBOXEX, style:WS_VISIBLE|WS_TABSTOP, title:""]);
		dialog.makeTemplate(header, item);
		dialog.open(null);
		dialog.setItemFocus(101);
		dialog.setItemText(101, "A猫");
	`)
	if err != nil {
		t.Fatalf("create editable dialog: %v", err)
	}
	context, ok := services.Win32DialogTextInputContext(1280, 720)
	if !ok {
		t.Fatal("focused editable control did not provide an IME context")
	}
	if context.OriginalText != "A猫" || context.TextBeforeCaret != "A猫" || context.TextAfterCaret != "" || context.CaretBounds.Empty() {
		t.Fatalf("IME context = %+v", context)
	}
	_, revisionBefore := services.Win32DialogFrame(1280, 720)
	services.Win32DialogSetComposition("仮名", len("仮"))
	frame, revisionAfter := services.Win32DialogFrame(1280, 720)
	if frame == nil || revisionAfter <= revisionBefore {
		t.Fatalf("IME composition frame was not refreshed: frame=%v revision=%d/%d", frame != nil, revisionBefore, revisionAfter)
	}
	if err := session.ApplyWin32DialogTextInputCommit(context, "語", nil); err != nil {
		t.Fatalf("apply IME commit: %v", err)
	}
	if value, err := session.Evaluate("dialog.getItemText(101)"); err != nil || value.Text() != "A猫語" {
		t.Fatalf("committed text = %q, err=%v", value.Text(), err)
	}
	context, ok = services.Win32DialogTextInputContext(1280, 720)
	if !ok {
		t.Fatal("IME context disappeared after commit")
	}
	if err := session.ApplyWin32DialogTextInputCommit(context, "漢", &Win32DialogTextReplacement{Before: "pre", After: "post"}); err != nil {
		t.Fatalf("apply surrounding-text replacement: %v", err)
	}
	if value, err := session.Evaluate("dialog.getItemText(101)"); err != nil || value.Text() != "pre漢post" {
		t.Fatalf("replacement text = %q, err=%v", value.Text(), err)
	}
}

func evalInteger(t *testing.T, session *Session, source string) int64 {
	t.Helper()
	value, err := session.Evaluate(source)
	if err != nil {
		t.Fatalf("evaluate %q: %v", source, err)
	}
	if value.Kind() != tjs.KindInteger {
		t.Fatalf("evaluate %q kind = %v, want integer", source, value.Kind())
	}
	return value.IntegerValue()
}
