package runtime

import (
	"fmt"

	"github.com/eslzzyl/krkrgo/internal/tjs"
)

// installGetAboutNative confirms the session-registered getabout.dll export.
// The method is available for the lifetime of a Session; linking records plugin
// compatibility but does not dynamically extend the System namespace.
func installGetAboutNative(vm *tjs.VM) error {
	if vm == nil {
		return fmt.Errorf("getabout.dll requires a VM")
	}
	system, ok := vm.Globals["System"]
	if !ok {
		return fmt.Errorf("getabout.dll requires the System namespace")
	}
	if _, err := vm.GetMember(system, "getAboutString", nil, nil); err != nil {
		return fmt.Errorf("getabout.dll requires the session System.getAboutString method: %w", err)
	}
	return nil
}

// installWin32DialogNative evaluates the C++ plugin's WIN32Dialog script class
// using the existing host dialog service. System.inform returns void in TJS,
// hence !System.inform(...) evaluates to true after a successful dialog.
func installWin32DialogNative(vm *tjs.VM) error {
	if vm == nil {
		return fmt.Errorf("win32dialog.dll requires a VM")
	}
	if _, installed := vm.Globals["WIN32Dialog"]; installed {
		return nil
	}
	class, err := vm.RegisterNativeClass("WIN32Dialog")
	if err != nil {
		return err
	}
	for name, value := range map[string]int64{
		"ES_LEFT": 0, "ES_CENTER": 1, "ES_RIGHT": 2, "ES_MULTILINE": 4,
		"ES_AUTOVSCROLL": 64, "ES_AUTOHSCROLL": 128, "ES_READONLY": 2048,
		"ES_WANTRETURN": 4096, "ES_NOHIDESEL": 256, "ES_NUMBER": 8192,
		"WS_BORDER": 8388608, "WS_TABSTOP": 65536, "WS_VSCROLL": 2097152,
		"WS_HSCROLL": 1048576, "WS_CHILD": 1073741824, "WS_VISIBLE": 268435456,
		"LBS_NOTIFY": 1, "CBS_DROPDOWN": 2, "CBS_DROPDOWNLIST": 3, "CBS_AUTOHSCROLL": 64,
		"CBS_HASSTRINGS": 512, "CBS_SORT": 256, "CBN_SELCHANGE": 1, "CBN_EDITCHANGE": 5,
		"LVS_REPORT": 1, "LVS_SINGLESEL": 4, "LVS_SHOWSELALWAYS": 8,
		"LVS_EX_FULLROWSELECT": 32, "LVS_EX_GRIDLINES": 1, "TBS_HORZ": 0, "TBS_VERT": 2,
		"TBS_AUTOTICKS": 1, "TBS_NOTICKS": 16, "TBS_ENABLESELRANGE": 32,
		"TCS_TABS": 0, "TCS_BUTTONS": 256, "TCS_SINGLELINE": 0, "TCS_MULTILINE": 512,
		"TVS_HASLINES": 2, "TVS_LINESATROOT": 4, "TVS_HASBUTTONS": 1,
		"SS_LEFT": 0, "SS_CENTER": 1, "SS_RIGHT": 2, "BS_PUSHBUTTON": 0,
		"BS_DEFPUSHBUTTON": 1, "IDOK": 1, "IDCANCEL": 2, "MB_OK": 0,
		"MB_OKCANCEL": 1, "MB_YESNO": 4, "SW_SHOW": 5, "SW_HIDE": 0,
		"TCIF_TEXT": 1, "TCIF_IMAGE": 2, "TCIF_PARAM": 8, "TCM_INSERTITEM": 4926,
		"TCM_DELETEITEM": 4872, "TCM_GETCURSEL": 4875, "TCM_SETCURSEL": 4876,
		"PBM_SETPOS": 1026, "PBM_GETPOS": 1032, "PBS_MARQUEE": 8,
	} {
		value := tjs.IntegerValue(value)
		vm.Globals[name] = value
		if err := vm.RegisterNativeStaticValue(class, name, value); err != nil {
			return err
		}
	}
	for _, name := range []string{"Header", "Items", "Bitmap", "SolidBrush", "DrawItem", "Notify", "Blob"} {
		nested, err := vm.RegisterNativeClass(name)
		if err != nil {
			return err
		}
		if err := vm.RegisterNativeStaticValue(class, name, nested); err != nil {
			return err
		}
	}
	return vm.RegisterNativeMethod(class, "messageBox", "WIN32Dialog.messageBox")
}

func (s *Services) win32DialogNative(operation string, args []tjs.Value) (tjs.Value, error) {
	if operation == "construct" {
		return tjs.VoidValue, nil
	}
	if operation != "messageBox" {
		switch operation {
		case "WIN32Dialog.add", "WIN32Dialog.open", "WIN32Dialog.close",
			"WIN32Dialog.show", "WIN32Dialog.hide", "WIN32Dialog.setItemText",
			"add", "open", "close", "show", "hide", "setItemText":
			return tjs.VoidValue, nil
		case "WIN32Dialog.getItemText", "getItemText":
			return tjs.StringValue(""), nil
		default:
			return tjs.VoidValue, fmt.Errorf("unknown WIN32Dialog operation %q", operation)
		}
	}
	if len(args) > 3 {
		return tjs.VoidValue, fmt.Errorf("WIN32Dialog.messageBox accepts message, caption, and type")
	}
	message, caption := "", ""
	if len(args) > 0 && !args[0].IsVoid() {
		message = args[0].Text()
	}
	if len(args) > 1 && !args[1].IsVoid() {
		caption = args[1].Text()
	}
	if s.Dialogs == nil {
		return tjs.VoidValue, ErrDialogUnavailable
	}
	if _, err := s.Dialogs.ShowMessage(caption, message); err != nil {
		return tjs.VoidValue, err
	}
	return tjs.IntegerValue(1), nil
}

// installFFTGraphNative installs the C++ plugin's drawFFTGraph global but
// reports the missing renderer contract explicitly rather than claiming a draw.
func installFFTGraphNative(vm *tjs.VM, services *Services) error {
	if vm == nil || services == nil {
		return fmt.Errorf("fftgraph.dll requires a VM and services")
	}
	if _, installed := vm.Globals["drawFFTGraph"]; installed {
		return nil
	}
	if _, ok := services.Natives.Entry("drawFFTGraph"); !ok {
		if err := services.Natives.RegisterFunc("drawFFTGraph", func(_ tjs.Value, _ []tjs.Value) (tjs.Value, error) {
			return tjs.VoidValue, UnsupportedDevice("fftgraph renderer")
		}); err != nil {
			return err
		}
	}
	return vm.RegisterNative("drawFFTGraph")
}
