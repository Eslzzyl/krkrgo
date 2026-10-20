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
	class, err := vm.NewNativeClass("WIN32Dialog", "WIN32Dialog.WIN32Dialog.@initialize")
	if err != nil {
		return err
	}
	for name, value := range map[string]int64{
		"ICC_USEREX_CLASSES": 512,
		"WS_POPUP":           2147483648, "WS_CAPTION": 12582912, "WS_DLGFRAME": 4194304,
		"WS_THICKFRAME": 262144, "WS_MINIMIZEBOX": 131072, "WS_MAXIMIZEBOX": 65536,
		"WS_SYSMENU": 524288, "WS_GROUP": 131072, "WS_DISABLED": 134217728,
		"WS_CLIPCHILDREN": 33554432, "WS_CLIPSIBLINGS": 67108864, "WS_CHILDWINDOW": 1073741824,
		"WS_OVERLAPPEDWINDOW": 13565952, "WS_EX_TOOLWINDOW": 128,
		"WS_EX_APPWINDOW": 262144, "WS_EX_ACCEPTFILES": 16, "WS_EX_DLGMODALFRAME": 1, "WS_EX_WINDOWEDGE": 256,
		"WS_EX_CLIENTEDGE": 512, "DS_SETFONT": 64, "DS_SHELLFONT": 72,
		"DS_MODALFRAME": 128, "DS_CENTER": 2048, "FW_NORMAL": 400, "FW_BOLD": 700,
		"ES_LEFT": 0, "ES_CENTER": 1, "ES_RIGHT": 2, "ES_MULTILINE": 4,
		"ES_AUTOVSCROLL": 64, "ES_AUTOHSCROLL": 128, "ES_READONLY": 2048,
		"ES_WANTRETURN": 4096, "ES_NOHIDESEL": 256, "ES_NUMBER": 8192,
		"WS_BORDER": 8388608, "WS_TABSTOP": 65536, "WS_VSCROLL": 2097152,
		"WS_HSCROLL": 1048576, "WS_CHILD": 1073741824, "WS_VISIBLE": 268435456,
		"BUTTON": 128, "EDIT": 129, "STATIC": 130, "LISTBOX": 131, "SCROLLBAR": 132,
		"COMBOBOX":   133,
		"LBS_NOTIFY": 1, "CBS_DROPDOWN": 2, "CBS_DROPDOWNLIST": 3, "CBS_AUTOHSCROLL": 64,
		"CBS_HASSTRINGS": 512, "CBS_SORT": 256, "CBN_SELCHANGE": 1, "CBN_EDITCHANGE": 5,
		"CB_DELETESTRING": 324, "CB_GETCOUNT": 326, "CB_GETCURSEL": 327, "CB_SETCURSEL": 334,
		"EM_SETSEL": 177, "EM_SCROLLCARET": 183, "EN_CHANGE": 768, "BN_CLICKED": 0,
		"LVS_REPORT": 1, "LVS_SINGLESEL": 4, "LVS_SHOWSELALWAYS": 8,
		"LVS_EX_FULLROWSELECT": 32, "LVS_EX_GRIDLINES": 1, "TBS_HORZ": 0, "TBS_VERT": 2,
		"TBS_AUTOTICKS": 1, "TBS_NOTICKS": 16, "TBS_ENABLESELRANGE": 32,
		"TCS_TABS": 0, "TCS_BUTTONS": 256, "TCS_SINGLELINE": 0, "TCS_MULTILINE": 512,
		"TVS_HASLINES": 2, "TVS_LINESATROOT": 4, "TVS_HASBUTTONS": 1,
		"SS_LEFT": 0, "SS_CENTER": 1, "SS_RIGHT": 2, "SS_CENTERIMAGE": 512,
		"SS_SUNKEN": 4096, "BS_PUSHBUTTON": 0, "BS_DEFPUSHBUTTON": 1, "BS_FLAT": 32768,
		"IDOK": 1, "IDCANCEL": 2, "MB_OK": 0,
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
	comboBoxEx := tjs.StringValue("ComboBoxEx32")
	vm.Globals["COMBOBOXEX"] = comboBoxEx
	if err := vm.RegisterNativeStaticValue(class, "COMBOBOXEX", comboBoxEx); err != nil {
		return err
	}
	for _, property := range []string{"left", "top", "width", "height", "title", "visible", "isValid"} {
		setter := "WIN32Dialog.set:" + property
		if property == "isValid" {
			setter = ""
		}
		if err := vm.RegisterNativeProperty(class, property, "WIN32Dialog.get:"+property, setter); err != nil {
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
		{"Blob", []string{"Blob", "ReferPointer", "finalize", "getByte", "getDWord", "getText", "getWord", "setByte", "setDWord", "setDWordLong", "setText", "setWord"}},
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
