package runtime

import (
	"fmt"
	"github.com/eslzzyl/krkrgo/internal/tjs"
	"strings"
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

// installKrMovieNative registers the static surface consumed by GFX_Movie.tjs.
func installKrMovieNative(vm *tjs.VM) error {
	if vm == nil {
		return fmt.Errorf("krmovie.dll requires a VM")
	}
	if _, installed := vm.Globals["GFX_Movie"]; installed {
		return nil
	}
	class, err := vm.NewNativeClass("GFX_Movie", "")
	if err != nil {
		return err
	}
	types, err := vm.NewArray(nil)
	if err != nil {
		return err
	}
	entries, err := vm.NewArray(nil)
	if err != nil {
		return err
	}
	if err := vm.SetMemberWithRuntime(types, "Entries", entries, 0, nil, nil); err != nil {
		return err
	}
	vm.Globals["Entries"] = entries
	vm.Globals["Types"] = types
	return vm.RegisterNativeStaticValue(class, "Types", types)
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
	class, err := vm.NewNativeClass("WIN32Dialog", "WIN32Dialog.WIN32Dialog.@initialize")
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
	rootMethods := []string{
		"WIN32Dialog", "add", "bringToFront", "chooseColor", "close", "closeProgress",
		"deleteAllTab", "deleteTab", "finalize", "getBaseUnits", "getClientRect",
		"getCurSel", "getItem", "getItemClassName", "getItemEnabled", "getItemHeight",
		"getItemID", "getItemInt", "getItemLeft", "getItemLong", "getItemText",
		"getStringAddress", "getWindowRect", "initCommonControls", "initCommonControlsEx",
		"insertTab", "invalidateAll", "invalidateRect", "isExistentItem", "loadResource",
		"lockItemUpdate", "makeTemplate", "mapRect", "messageBox", "onCommand",
		"onHScroll", "onInit", "onNotify", "onSize", "onVScroll", "open",
		"openProgress", "openPropertySheet", "propSheetMessage", "selectTab",
		"sendItemMessage", "setCurSel", "setItemBitmap", "setItemEnabled",
		"setItemFocus", "setItemInt", "setItemLong", "setItemPos", "setItemSize",
		"setItemText", "setMessageResult", "setPos", "setScrollInfo", "setSize",
		"show", "unlockItemUpdate",
	}
	for _, name := range rootMethods {
		if name == "messageBox" {
			continue
		}
		if err := vm.RegisterNativeMethod(class, name, "WIN32Dialog."+name); err != nil {
			return err
		}
	}
	for _, spec := range []struct {
		name    string
		methods []string
	}{
		{"Header", []string{"Header", "finalize", "store"}},
		{"Items", []string{"Items", "finalize", "store"}},
		{"Bitmap", []string{"Bitmap", "finalize"}},
		{"SolidBrush", []string{"SolidBrush", "finalize"}},
		{"DrawItem", []string{"DrawItem", "draw", "finalize"}},
		{"Notify", []string{"Notify", "finalize", "getByte", "getDWord", "getWord"}},
		{"Blob", []string{"Blob", "ReferPointer", "finalize", "getByte", "getDWord", "getText", "getWord", "setByte", "setDWord", "setText", "setWord"}},
	} {
		nested, err := vm.NewNativeClass(spec.name, "WIN32Dialog."+spec.name+".@initialize")
		if err != nil {
			return err
		}
		for _, method := range spec.methods {
			if method == spec.name {
				continue
			}
			if err := vm.RegisterNativeMethod(nested, method, "WIN32Dialog."+spec.name+"."+method); err != nil {
				return err
			}
		}
		if err := vm.RegisterNativeStaticValue(class, spec.name, nested); err != nil {
			return err
		}
	}
	if err := vm.RegisterNativeMethod(class, "messageBox", "WIN32Dialog.messageBox"); err != nil {
		return err
	}
	return vm.RegisterNative("getBookMarkFileNameAtNum")
}

func (s *Services) win32DialogNative(operation string, args []tjs.Value) (tjs.Value, error) {
	if operation == "construct" || operation == "WIN32Dialog" || strings.HasSuffix(operation, ".@initialize") {
		return tjs.VoidValue, nil
	}
	if operation == "add" {
		if len(args) != 1 {
			return tjs.VoidValue, fmt.Errorf("WIN32Dialog.add accepts one item")
		}
		return tjs.IntegerValue(0), nil
	}
	if operation != "messageBox" {
		return tjs.VoidValue, fmt.Errorf("unsupported WIN32Dialog operation %q with %d arguments", operation, len(args))
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
