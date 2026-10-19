package runtime

import (
	"fmt"
	"math"
	"strings"

	"github.com/eslzzyl/krkrgo/internal/tjs"
)

var windowNotificationNumbers = map[string]int{
	"NULL": 0x0000, "CREATE": 0x0001, "DESTROY": 0x0002, "MOVE": 0x0003, "SIZE": 0x0005,
	"ACTIVATE": 0x0006, "SETFOCUS": 0x0007, "KILLFOCUS": 0x0008, "ENABLE": 0x000A,
	"SETREDRAW": 0x000B, "SETTEXT": 0x000C, "GETTEXT": 0x000D, "GETTEXTLENGTH": 0x000E,
	"PAINT": 0x000F, "CLOSE": 0x0010, "QUERYENDSESSION": 0x0011, "QUIT": 0x0012,
	"QUERYOPEN": 0x0013, "ERASEBKGND": 0x0014, "SYSCOLORCHANGE": 0x0015, "ENDSESSION": 0x0016,
	"SHOWWINDOW": 0x0018, "CTLCOLOR": 0x0019, "WININICHANGE": 0x001A, "SETTINGCHANGE": 0x001A,
	"DEVMODECHANGE": 0x001B, "ACTIVATEAPP": 0x001C, "FONTCHANGE": 0x001D, "TIMECHANGE": 0x001E,
	"CANCELMODE": 0x001F, "SETCURSOR": 0x0020, "MOUSEACTIVATE": 0x0021, "CHILDACTIVATE": 0x0022,
	"QUEUESYNC": 0x0023, "GETMINMAXINFO": 0x0024, "PAINTICON": 0x0026, "ICONERASEBKGND": 0x0027,
	"NEXTDLGCTL": 0x0028, "SPOOLERSTATUS": 0x002A, "DRAWITEM": 0x002B, "MEASUREITEM": 0x002C,
	"DELETEITEM": 0x002D, "VKEYTOITEM": 0x002E, "CHARTOITEM": 0x002F, "SETFONT": 0x0030,
	"GETFONT": 0x0031, "SETHOTKEY": 0x0032, "GETHOTKEY": 0x0033, "QUERYDRAGICON": 0x0037,
	"COMPAREITEM": 0x0039, "GETOBJECT": 0x003D, "COMPACTING": 0x0041, "COMMNOTIFY": 0x0044,
	"WINDOWPOSCHANGING": 0x0046, "WINDOWPOSCHANGED": 0x0047, "POWER": 0x0048, "COPYDATA": 0x004A,
	"CANCELJOURNAL": 0x004B, "NOTIFY": 0x004E, "INPUTLANGCHANGEREQUEST": 0x0050, "INPUTLANGCHANGE": 0x0051,
	"TCARD": 0x0052, "HELP": 0x0053, "USERCHANGED": 0x0054, "NOTIFYFORMAT": 0x0055,
	"CONTEXTMENU": 0x007B, "STYLECHANGING": 0x007C, "STYLECHANGED": 0x007D, "DISPLAYCHANGE": 0x007E,
	"GETICON": 0x007F, "SETICON": 0x0080, "NCCREATE": 0x0081, "NCDESTROY": 0x0082,
	"NCCALCSIZE": 0x0083, "NCHITTEST": 0x0084, "NCPAINT": 0x0085, "NCACTIVATE": 0x0086,
	"GETDLGCODE": 0x0087, "SYNCPAINT": 0x0088, "NCMOUSEMOVE": 0x00A0, "NCLBUTTONDOWN": 0x00A1,
	"NCLBUTTONUP": 0x00A2, "NCLBUTTONDBLCLK": 0x00A3, "NCRBUTTONDOWN": 0x00A4,
	"NCRBUTTONUP": 0x00A5, "NCRBUTTONDBLCLK": 0x00A6, "NCMBUTTONDOWN": 0x00A7,
	"NCMBUTTONUP": 0x00A8, "NCMBUTTONDBLCLK": 0x00A9, "NCXBUTTONDOWN": 0x00AB,
	"NCXBUTTONUP": 0x00AC, "NCXBUTTONDBLCLK": 0x00AD, "INPUT_DEVICE_CHANGE": 0x00FE, "INPUT": 0x00FF,
	"KEYFIRST": 0x0100, "KEYDOWN": 0x0100, "KEYUP": 0x0101, "CHAR": 0x0102,
	"DEADCHAR": 0x0103, "SYSKEYDOWN": 0x0104, "SYSKEYUP": 0x0105, "SYSCHAR": 0x0106,
	"SYSDEADCHAR": 0x0107, "UNICHAR": 0x0109, "KEYLAST": 0x0109,
	"IME_STARTCOMPOSITION": 0x010D, "IME_ENDCOMPOSITION": 0x010E, "IME_COMPOSITION": 0x010F, "IME_KEYLAST": 0x010F,
	"INITDIALOG": 0x0110, "COMMAND": 0x0111, "SYSCOMMAND": 0x0112, "TIMER": 0x0113,
	"HSCROLL": 0x0114, "VSCROLL": 0x0115, "INITMENU": 0x0116, "INITMENUPOPUP": 0x0117,
	"MENUSELECT": 0x011F, "MENUCHAR": 0x0120, "ENTERIDLE": 0x0121, "MENURBUTTONUP": 0x0122,
	"MENUDRAG": 0x0123, "MENUGETOBJECT": 0x0124, "UNINITMENUPOPUP": 0x0125, "MENUCOMMAND": 0x0126,
	"CHANGEUISTATE": 0x0127, "UPDATEUISTATE": 0x0128, "QUERYUISTATE": 0x0129,
	"CTLCOLORMSGBOX": 0x0132, "CTLCOLOREDIT": 0x0133, "CTLCOLORLISTBOX": 0x0134, "CTLCOLORBTN": 0x0135,
	"CTLCOLORDLG": 0x0136, "CTLCOLORSCROLLBAR": 0x0137, "CTLCOLORSTATIC": 0x0138,
	"MOUSEFIRST": 0x0200, "MOUSEMOVE": 0x0200, "LBUTTONDOWN": 0x0201, "LBUTTONUP": 0x0202,
	"LBUTTONDBLCLK": 0x0203, "RBUTTONDOWN": 0x0204, "RBUTTONUP": 0x0205, "RBUTTONDBLCLK": 0x0206,
	"MBUTTONDOWN": 0x0207, "MBUTTONUP": 0x0208, "MBUTTONDBLCLK": 0x0209, "MOUSEWHEEL": 0x020A,
	"XBUTTONDOWN": 0x020B, "XBUTTONUP": 0x020C, "XBUTTONDBLCLK": 0x020D, "MOUSEHWHEEL": 0x020E,
	"MOUSELAST": 0x020E, "PARENTNOTIFY": 0x0210, "ENTERMENULOOP": 0x0211, "EXITMENULOOP": 0x0212,
	"NEXTMENU": 0x0213, "SIZING": 0x0214, "CAPTURECHANGED": 0x0215, "MOVING": 0x0216,
	"POWERBROADCAST": 0x0218, "DEVICECHANGE": 0x0219, "MDICREATE": 0x0220, "MDIDESTROY": 0x0221,
	"MDIACTIVATE": 0x0222, "MDIRESTORE": 0x0223, "MDINEXT": 0x0224, "MDIMAXIMIZE": 0x0225,
	"MDITILE": 0x0226, "MDICASCADE": 0x0227, "MDIICONARRANGE": 0x0228, "MDIGETACTIVE": 0x0229,
	"MDISETMENU": 0x0230, "ENTERSIZEMOVE": 0x0231, "EXITSIZEMOVE": 0x0232, "DROPFILES": 0x0233,
	"MDIREFRESHMENU": 0x0234, "IME_SETCONTEXT": 0x0281, "IME_NOTIFY": 0x0282,
	"IME_CONTROL": 0x0283, "IME_COMPOSITIONFULL": 0x0284, "IME_SELECT": 0x0285, "IME_CHAR": 0x0286,
	"IME_REQUEST": 0x0288, "IME_KEYDOWN": 0x0290, "IME_KEYUP": 0x0291, "MOUSEHOVER": 0x02A1,
	"MOUSELEAVE": 0x02A3, "NCMOUSEHOVER": 0x02A0, "NCMOUSELEAVE": 0x02A2, "WTSSESSION_CHANGE": 0x02B1,
	"TABLET_FIRST": 0x02C0, "TABLET_LAST": 0x02DF, "CUT": 0x0300, "COPY": 0x0301,
	"PASTE": 0x0302, "CLEAR": 0x0303, "UNDO": 0x0304, "RENDERFORMAT": 0x0305,
	"RENDERALLFORMATS": 0x0306, "DESTROYCLIPBOARD": 0x0307, "DRAWCLIPBOARD": 0x0308,
	"PAINTCLIPBOARD": 0x0309, "VSCROLLCLIPBOARD": 0x030A, "SIZECLIPBOARD": 0x030B,
	"ASKCBFORMATNAME": 0x030C, "CHANGECBCHAIN": 0x030D, "HSCROLLCLIPBOARD": 0x030E,
	"QUERYNEWPALETTE": 0x030F, "PALETTEISCHANGING": 0x0310, "PALETTECHANGED": 0x0311,
	"HOTKEY": 0x0312, "PRINT": 0x0317, "PRINTCLIENT": 0x0318, "APPCOMMAND": 0x0319,
	"THEMECHANGED": 0x031A, "CLIPBOARDUPDATE": 0x031D, "DWMCOMPOSITIONCHANGED": 0x031E,
	"DWMNCRENDERINGCHANGED": 0x031F, "DWMCOLORIZATIONCOLORCHANGED": 0x0320,
	"DWMWINDOWMAXIMIZEDCHANGE": 0x0321, "GETTITLEBARINFOEX": 0x033F,
	"HANDHELDFIRST": 0x0358, "HANDHELDLAST": 0x035F, "AFXFIRST": 0x0360, "AFXLAST": 0x037F,
	"PENWINFIRST": 0x0380, "PENWINLAST": 0x038F,
}
var windowNotificationNames = func() map[int]string {
	names := make(map[int]string, len(windowNotificationNumbers))
	for name, number := range windowNotificationNumbers {
		names[number] = name
	}
	// Win32 aliases have a conventional reverse spelling; match windowEx's
	// reverse notification table rather than relying on Go map iteration.
	for number, name := range map[int]string{
		0x001A: "SETTINGCHANGE",
		0x0100: "KEYDOWN",
		0x0109: "KEYLAST",
		0x010F: "IME_KEYLAST",
		0x0200: "MOUSEMOVE",
		0x020E: "MOUSELAST",
	} {
		names[number] = name
	}
	return names
}()

func windowObjectContext(context tjs.Value, operation string) (tjs.ObjectID, error) {
	if context.Kind() != tjs.KindObject || context.IsNull() || context.ObjectRef().Object == 0 {
		return 0, fmt.Errorf("Window.%s requires non-null object context", operation)
	}
	return context.ObjectRef().Object, nil
}

func (s *Services) windowForContext(context tjs.Value, operation string) (*WindowState, tjs.ObjectID, error) {
	id, err := windowObjectContext(context, operation)
	if err != nil {
		return nil, 0, err
	}
	window := s.Window(id)
	if window == nil {
		return nil, id, fmt.Errorf("Window.%s: %w", operation, ErrInvalidWindow)
	}
	return window, id, nil
}

func windowIntArg(args []tjs.Value, index int, operation string) (int32, error) {
	if index < 0 || index >= len(args) {
		return 0, fmt.Errorf("Window.%s: missing argument %d", operation, index)
	}
	n, err := args[index].Int()
	if err != nil {
		return 0, err
	}
	if n < math.MinInt32 || n > math.MaxInt32 {
		return 0, fmt.Errorf("Window.%s: integer argument exceeds int32", operation)
	}
	return int32(n), nil
}

func windowBoolArg(args []tjs.Value, index int, operation string) (bool, error) {
	if index < 0 || index >= len(args) {
		return false, fmt.Errorf("Window.%s: missing argument %d", operation, index)
	}
	return args[index].Truth()
}

func windowRectValue(vm *tjs.VM, rect [4]int32) (tjs.Value, error) {
	value, err := vm.NewDictionary()
	if err != nil {
		return tjs.VoidValue, err
	}
	object, ok := vm.Objects.Lookup(value.ObjectRef().Object)
	if !ok {
		return tjs.VoidValue, fmt.Errorf("window rectangle object was not registered")
	}
	for i, name := range [...]string{"x", "y", "w", "h"} {
		if err := object.Set(name, tjs.IntegerValue(int64(rect[i]))); err != nil {
			return tjs.VoidValue, err
		}
	}
	return value, nil
}

func rectangleValue(vm *tjs.VM, rect [4]int32) (tjs.Value, error) {
	value, err := vm.NewDictionary()
	if err != nil {
		return tjs.VoidValue, err
	}
	object, ok := vm.Objects.Lookup(value.ObjectRef().Object)
	if !ok {
		return tjs.VoidValue, fmt.Errorf("monitor rectangle object was not registered")
	}
	_ = object.Set("x", tjs.IntegerValue(int64(rect[0])))
	_ = object.Set("y", tjs.IntegerValue(int64(rect[1])))
	_ = object.Set("w", tjs.IntegerValue(int64(rect[2])))
	_ = object.Set("h", tjs.IntegerValue(int64(rect[3])))
	return value, nil
}

func monitorValue(vm *tjs.VM, monitor Monitor, intersect *[4]int32) (tjs.Value, error) {
	value, err := vm.NewDictionary()
	if err != nil {
		return tjs.VoidValue, err
	}
	object, ok := vm.Objects.Lookup(value.ObjectRef().Object)
	if !ok {
		return tjs.VoidValue, fmt.Errorf("monitor object was not registered")
	}
	_ = object.Set("name", tjs.StringValue(monitor.Name))
	primary := tjs.IntegerValue(0)
	if monitor.Primary {
		primary = tjs.IntegerValue(1)
	}
	_ = object.Set("primary", primary)
	monitorRect, err := rectangleValue(vm, monitor.Bounds)
	if err != nil {
		return tjs.VoidValue, err
	}
	workRect, err := rectangleValue(vm, monitor.Work)
	if err != nil {
		return tjs.VoidValue, err
	}
	_ = object.Set("monitor", monitorRect)
	_ = object.Set("work", workRect)
	_ = object.Set("left", tjs.IntegerValue(int64(monitor.Bounds[0])))
	_ = object.Set("top", tjs.IntegerValue(int64(monitor.Bounds[1])))
	_ = object.Set("width", tjs.IntegerValue(int64(monitor.Bounds[2])))
	_ = object.Set("height", tjs.IntegerValue(int64(monitor.Bounds[3])))
	_ = object.Set("workLeft", tjs.IntegerValue(int64(monitor.Work[0])))
	_ = object.Set("workTop", tjs.IntegerValue(int64(monitor.Work[1])))
	_ = object.Set("workWidth", tjs.IntegerValue(int64(monitor.Work[2])))
	_ = object.Set("workHeight", tjs.IntegerValue(int64(monitor.Work[3])))
	if intersect != nil {
		intersectRect, err := rectangleValue(vm, *intersect)
		if err != nil {
			return tjs.VoidValue, err
		}
		_ = object.Set("intersect", intersectRect)
	}
	return value, nil
}

func (s *Services) windowNative(vm *tjs.VM, context tjs.Value, operation string, args []tjs.Value, budget *uint64) (tjs.Value, error) {
	s.ensureInit()
	switch operation {
	case "getNotificationNum":
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("Window.getNotificationNum: missing argument 0")
		}
		key, err := args[0].Unary("string")
		if err != nil {
			return tjs.VoidValue, err
		}
		if number, ok := windowNotificationNumbers[key.Text()]; ok {
			return tjs.IntegerValue(int64(number)), nil
		}
		return tjs.IntegerValue(-1), nil
	case "getNotificationName":
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("Window.getNotificationName: missing argument 0")
		}
		number, err := args[0].Int()
		if err != nil {
			return tjs.VoidValue, err
		}
		return tjs.StringValue(windowNotificationNames[int(int32(number))]), nil
	}
	if operation == "@initialize" || operation == "Window" {
		id, err := windowObjectContext(context, operation)
		if err != nil {
			return tjs.VoidValue, err
		}
		if s.Window(id) == nil {
			if err := s.RegisterWindow(id, NewWindow()); err != nil {
				return tjs.VoidValue, err
			}
		}
		return tjs.VoidValue, nil
	}
	if operation == "@invalidate" {
		if context.Kind() == tjs.KindObject && !context.IsNull() {
			id := context.ObjectRef().Object
			children, started := s.beginWindowInvalidation(id)
			if !started {
				return tjs.VoidValue, nil
			}
			if vm != nil {
				for _, child := range children {
					_, _ = vm.Invalidate(child, s, budget)
				}
			}
			err := s.destroyWindowMenu(vm, id, budget)
			s.UnregisterWindow(id)
			if err != nil {
				return tjs.VoidValue, err
			}
		}
		return tjs.VoidValue, nil
	}
	if operation == "finalize" {
		return tjs.VoidValue, nil
	}
	if operation == "get:mainWindow" {
		if s.MainWindow == 0 || s.Window(tjs.ObjectID(s.MainWindow)) == nil {
			return tjs.NullValue, nil
		}
		return tjs.ObjectValue(tjs.NewObjectRef(tjs.ObjectID(s.MainWindow))), nil
	}
	window, id, err := s.windowForContext(context, operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	if strings.HasPrefix(operation, "get:") {
		property := strings.TrimPrefix(operation, "get:")
		switch property {
		case "menu":
			return s.windowMenu(vm, id, budget)
		case "caption":
			return tjs.StringValue(window.Caption), nil
		case "visible":
			return tjs.IntegerValue(windowBoolInt(window.Visible)), nil
		case "minimized":
			return tjs.IntegerValue(windowBoolInt(window.Minimized)), nil
		case "maximized":
			return tjs.IntegerValue(windowBoolInt(window.Maximized)), nil
		case "left":
			return tjs.IntegerValue(int64(window.Left)), nil
		case "top":
			return tjs.IntegerValue(int64(window.Top)), nil
		case "width":
			return tjs.IntegerValue(int64(window.Width())), nil
		case "height":
			return tjs.IntegerValue(int64(window.Height())), nil
		case "innerWidth":
			return tjs.IntegerValue(int64(window.InnerWidth)), nil
		case "innerHeight":
			return tjs.IntegerValue(int64(window.InnerHeight)), nil
		case "borderStyle":
			return tjs.IntegerValue(int64(window.BorderStyle)), nil
		case "fullScreen":
			return tjs.IntegerValue(windowBoolInt(window.IsFullScreen())), nil
		case "fitToClient":
			return tjs.IntegerValue(windowBoolInt(window.FitToClient)), nil
		case "zoom":
			return tjs.IntegerValue(int64(window.ZoomNumer)), nil
		case "zoomNumer":
			return tjs.IntegerValue(int64(window.ZoomNumer)), nil
		case "zoomDenom":
			return tjs.IntegerValue(int64(window.ZoomDenom)), nil
		case "layerLeft":
			return tjs.IntegerValue(int64(window.LayerLeft)), nil
		case "layerTop":
			return tjs.IntegerValue(int64(window.LayerTop)), nil
		case "mouseCursor", "mouseCursorState":
			return tjs.IntegerValue(int64(window.MouseCursorState)), nil
		case "disableResize":
			return tjs.IntegerValue(windowBoolInt(window.DisableResize)), nil
		case "disableMove":
			return tjs.IntegerValue(windowBoolInt(window.DisableMove)), nil
		case "enableNCMouseEvent":
			return tjs.IntegerValue(windowBoolInt(window.EnableNCMouseEvent)), nil
		case "exSystemMenu":
			if window.ExSystemMenu.Kind() == tjs.KindVoid {
				return tjs.NullValue, nil
			}
			return window.ExSystemMenu, nil
		case "drawDevice":
			if window.DrawDeviceObject.Kind() == tjs.KindVoid {
				return tjs.NullValue, nil
			}
			return window.DrawDeviceObject, nil
		case "windowRect":
			return windowRectValue(vm, window.WindowRect())
		case "clientRect":
			return windowRectValue(vm, window.ClientRect())
		case "primaryLayer":
			layer := s.Layer(tjs.ObjectID(window.primaryLayer))
			if layer == nil {
				return tjs.VoidValue, fmt.Errorf("Window.primaryLayer: window has no primary layer")
			}
			return layerValue(layer), nil
		case "focusedLayer":
			layer := s.Layer(tjs.ObjectID(window.focusedLayer))
			if layer == nil {
				return tjs.NullValue, nil
			}
			return layerValue(layer), nil
		default:
			return tjs.VoidValue, fmt.Errorf("%w: Window.get:%s", ErrUnsupportedNative, property)
		}
	}
	if strings.HasPrefix(operation, "set:") {
		property := strings.TrimPrefix(operation, "set:")
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("Window.set:%s: missing value", property)
		}
		switch property {
		case "drawDevice":
			current := window.DrawDeviceObject
			if current.Kind() == tjs.KindObject && !current.IsNull() && !current.StrictEqual(args[0]) && vm != nil {
				if _, err := vm.Invalidate(current, s, budget); err != nil {
					return tjs.VoidValue, err
				}
			}
			window.DrawDeviceObject = args[0]
			return tjs.VoidValue, nil
		case "focusedLayer":
			var target *Layer
			value := args[0]
			if value.Kind() == tjs.KindObject && !value.IsNull() {
				var layerErr error
				target, _, layerErr = s.layerFromValue(value)
				if layerErr != nil {
					return tjs.VoidValue, fmt.Errorf("Window.set:focusedLayer: %w", layerErr)
				}
			}
			_, focusErr := s.layerSetFocus(vm, window, target, false, budget)
			return tjs.VoidValue, focusErr
		case "caption":
			s.setWindowCaption(uint64(id), args[0].Text())
		case "visible":
			value, e := args[0].Truth()
			if e != nil {
				return tjs.VoidValue, e
			}
			window.SetVisible(value)
		case "minimized":
			value, e := args[0].Truth()
			if e != nil {
				return tjs.VoidValue, e
			}
			if value {
				window.Minimize(s.Display.DesktopBounds())
			} else {
				window.Restore(s.Display.DesktopBounds())
			}
		case "maximized":
			value, e := args[0].Truth()
			if e != nil {
				return tjs.VoidValue, e
			}
			if value {
				window.Maximize(s.Display.DesktopBounds())
			} else {
				window.Restore(s.Display.DesktopBounds())
			}
		case "left":
			value, e := windowIntValue(args[0])
			if e != nil {
				return tjs.VoidValue, e
			}
			window.SetPos(value, window.Top)
		case "top":
			value, e := windowIntValue(args[0])
			if e != nil {
				return tjs.VoidValue, e
			}
			window.SetPos(window.Left, value)
		case "width":
			value, e := windowIntValue(args[0])
			if e != nil {
				return tjs.VoidValue, e
			}
			if e = window.SetWidth(value); e != nil {
				return tjs.VoidValue, e
			}
		case "height":
			value, e := windowIntValue(args[0])
			if e != nil {
				return tjs.VoidValue, e
			}
			if e = window.SetHeight(value); e != nil {
				return tjs.VoidValue, e
			}
		case "innerWidth":
			value, e := windowIntValue(args[0])
			if e != nil {
				return tjs.VoidValue, e
			}
			if e = window.SetInnerSize(value, window.InnerHeight); e != nil {
				return tjs.VoidValue, e
			}
		case "innerHeight":
			value, e := windowIntValue(args[0])
			if e != nil {
				return tjs.VoidValue, e
			}
			if e = window.SetInnerSize(window.InnerWidth, value); e != nil {
				return tjs.VoidValue, e
			}
		case "borderStyle":
			value, e := windowIntValue(args[0])
			if e != nil {
				return tjs.VoidValue, e
			}
			window.SetBorderStyle(value)
		case "fullScreen":
			value, e := args[0].Truth()
			if e != nil {
				return tjs.VoidValue, e
			}
			if e = s.Display.SetWindowFullScreen(uint64(id), value); e != nil {
				return tjs.VoidValue, e
			}
		case "fitToClient":
			value, e := args[0].Truth()
			if e != nil {
				return tjs.VoidValue, e
			}
			window.SetFitToClient(value)
		case "zoom":
			value, e := windowIntValue(args[0])
			if e != nil {
				return tjs.VoidValue, e
			}
			if e = window.SetZoomNumer(value); e != nil {
				return tjs.VoidValue, e
			}
		case "zoomNumer":
			value, e := windowIntValue(args[0])
			if e != nil {
				return tjs.VoidValue, e
			}
			if e = window.SetZoomNumer(value); e != nil {
				return tjs.VoidValue, e
			}
		case "zoomDenom":
			value, e := windowIntValue(args[0])
			if e != nil {
				return tjs.VoidValue, e
			}
			if e = window.SetZoomDenom(value); e != nil {
				return tjs.VoidValue, e
			}
		case "layerLeft":
			value, e := windowIntValue(args[0])
			if e != nil {
				return tjs.VoidValue, e
			}
			window.SetLayerPos(value, window.LayerTop)
		case "layerTop":
			value, e := windowIntValue(args[0])
			if e != nil {
				return tjs.VoidValue, e
			}
			window.SetLayerPos(window.LayerLeft, value)
		case "mouseCursor", "mouseCursorState":
			value, e := windowIntValue(args[0])
			if e != nil {
				return tjs.VoidValue, e
			}
			window.MouseCursorState = value
		case "disableResize":
			value, e := args[0].Truth()
			if e != nil {
				return tjs.VoidValue, e
			}
			window.DisableResize = value
		case "disableMove":
			value, e := args[0].Truth()
			if e != nil {
				return tjs.VoidValue, e
			}
			window.DisableMove = value
		case "enableNCMouseEvent":
			value, e := args[0].Truth()
			if e != nil {
				return tjs.VoidValue, e
			}
			window.EnableNCMouseEvent = value
		case "exSystemMenu":
			if args[0].Kind() != tjs.KindObject {
				return tjs.VoidValue, fmt.Errorf("Window.set:exSystemMenu requires an Object")
			}
			window.ExSystemMenu = args[0]
		default:
			return tjs.VoidValue, fmt.Errorf("%w: Window.set:%s", ErrUnsupportedNative, property)
		}
		return tjs.VoidValue, nil
	}
	switch operation {
	case "resetWindowIcon":
		return tjs.VoidValue, UnsupportedDevice("native window icon")
	case "setWindowIcon":
		return tjs.VoidValue, UnsupportedDevice("native window icon")
	case "setOverlayBitmap":
		return tjs.VoidValue, UnsupportedDevice("native window overlay bitmap")
	case "resetExSystemMenu":
		return tjs.VoidValue, nil
	case "ncHitTest":
		if _, err := windowIntArg(args, 0, operation); err != nil {
			return tjs.VoidValue, err
		}
		if _, err := windowIntArg(args, 1, operation); err != nil {
			return tjs.VoidValue, err
		}
		return tjs.VoidValue, UnsupportedDevice("native non-client hit testing")
	case "focusMenuByKey":
		if _, err := windowIntArg(args, 0, operation); err != nil {
			return tjs.VoidValue, err
		}
		return tjs.VoidValue, UnsupportedDevice("native window menu focus")
	case "bringToFront":
		// The portable runtime does not own a native window manager to raise.
		return tjs.VoidValue, nil
	case "bringTo", "sendToBack":
		return tjs.VoidValue, UnsupportedDevice("native window z-order")
	case "setMessageHook":
		on := false
		if len(args) > 0 {
			value, e := args[0].Int()
			if e != nil {
				return tjs.VoidValue, e
			}
			on = value != 0
		}
		if len(args) > 1 {
			number := -1
			key := args[1]
			if key.Kind() == tjs.KindString {
				var ok bool
				number, ok = windowNotificationNumbers[key.String()]
				if !ok {
					return tjs.VoidValue, fmt.Errorf("Unknown failure : FFFFFFFF")
				}
			} else {
				value, e := key.Int()
				if e != nil {
					return tjs.VoidValue, e
				}
				number = int(int32(value))
			}
			if number < 0 || number >= 0x400 {
				return tjs.VoidValue, fmt.Errorf("Unknown failure : FFFFFFFF")
			}
			// The C++ per-message setter validates the ID but returns the global enable state.
			return tjs.IntegerValue(windowBoolInt(window.EnableWinMsgHook)), nil
		}
		window.EnableWinMsgHook = on
		return tjs.IntegerValue(windowBoolInt(window.EnableWinMsgHook)), nil
	case "onResize", "onMouseMove", "onMouseDown", "onMouseUp", "onClick", "onDoubleClick", "onMouseLeave", "onMouseWheel", "onKeyDown", "onKeyUp", "onKeyPress", "onActivate", "onDeactivate":
		return tjs.VoidValue, nil
	case "registerExEvent":
		events := [...]string{"onResizing", "onMoving", "onMove", "onNcMouseMove"}
		for index, event := range events {
			window.ExEvents[index] = tjs.HasMember(vm.Objects, context, event)
		}
		window.ExEventRegistered = true
	case "setLayerPos":
		x, e := windowIntArg(args, 0, operation)
		if e != nil {
			return tjs.VoidValue, e
		}
		y, e := windowIntArg(args, 1, operation)
		if e != nil {
			return tjs.VoidValue, e
		}
		window.SetLayerPos(x, y)
	case "setPos":
		x, e := windowIntArg(args, 0, operation)
		if e != nil {
			return tjs.VoidValue, e
		}
		y, e := windowIntArg(args, 1, operation)
		if e != nil {
			return tjs.VoidValue, e
		}
		window.SetPos(x, y)
	case "setSize", "resize":
		width, e := windowIntArg(args, 0, operation)
		if e != nil {
			return tjs.VoidValue, e
		}
		height, e := windowIntArg(args, 1, operation)
		if e != nil {
			return tjs.VoidValue, e
		}
		if e = window.SetSize(width, height); e != nil {
			return tjs.VoidValue, e
		}
	case "setInnerSize":
		width, e := windowIntArg(args, 0, operation)
		if e != nil {
			return tjs.VoidValue, e
		}
		height, e := windowIntArg(args, 1, operation)
		if e != nil {
			return tjs.VoidValue, e
		}
		if e = window.SetInnerSize(width, height); e != nil {
			return tjs.VoidValue, e
		}
	case "setZoom":
		numer, e := windowIntArg(args, 0, operation)
		if e != nil {
			return tjs.VoidValue, e
		}
		denom := int32(1)
		if len(args) > 1 {
			denom, e = windowIntArg(args, 1, operation)
			if e != nil {
				return tjs.VoidValue, e
			}
		}
		if e = window.SetZoom(numer, denom); e != nil {
			return tjs.VoidValue, e
		}
	case "add", "remove":
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("Window.%s: missing argument 0", operation)
		}
		if args[0].Kind() != tjs.KindObject {
			return tjs.VoidValue, fmt.Errorf("Window.%s requires an Object", operation)
		}
		if operation == "add" {
			err = s.addWindowObject(id, args[0])
		} else {
			err = s.removeWindowObject(id, args[0])
		}
		if err != nil {
			return tjs.VoidValue, err
		}
	case "close":
		window.Close()
	case "showModal":
		window.ShowModal()
	case "hideMouseCursor":
		window.MouseCursorState = 1
	case "show":
		if len(args) == 0 {
			window.SetVisible(true)
		} else {
			value, e := windowBoolArg(args, 0, operation)
			if e != nil {
				return tjs.VoidValue, e
			}
			window.SetVisible(value)
		}
	case "hide":
		window.SetVisible(false)
	case "minimize":
		window.Minimize(s.Display.DesktopBounds())
	case "maximize":
		window.Maximize(s.Display.DesktopBounds())
	case "restore", "showRestore":
		window.Restore(s.Display.DesktopBounds())
	case "getWindowRect":
		return windowRectValue(vm, window.WindowRect())
	case "getClientRect":
		return windowRectValue(vm, window.ClientRect())
	case "getNormalRect":
		return windowRectValue(vm, window.NormalRect())
	case "disableResize":
		window.DisableResize = true
	case "disableMove":
		window.DisableMove = true
	case "enableNCMouseEvent":
		window.EnableNCMouseEvent = true
	default:
		return tjs.VoidValue, fmt.Errorf("%w: Window.%s", ErrUnsupportedNative, operation)
	}
	return tjs.VoidValue, nil
}

func windowIntValue(value tjs.Value) (int32, error) {
	n, err := value.Int()
	if err != nil {
		return 0, err
	}
	if n < math.MinInt32 || n > math.MaxInt32 {
		return 0, fmt.Errorf("integer argument exceeds int32")
	}
	return int32(n), nil
}

func windowBoolInt(value bool) int64 {
	if value {
		return 1
	}
	return 0
}

func (s *Services) systemWindowNative(vm *tjs.VM, operation string, args []tjs.Value) (tjs.Value, error) {
	s.ensureInit()
	switch operation {
	case "getDisplayMonitors":
		var query *[4]int32
		if len(args) != 0 && len(args) != 4 {
			return tjs.VoidValue, fmt.Errorf("System.getDisplayMonitors requires 0 or 4 arguments")
		}
		if len(args) == 4 {
			rect := [4]int32{}
			for index := range rect {
				value, err := windowIntValue(args[index])
				if err != nil {
					return tjs.VoidValue, err
				}
				rect[index] = value
			}
			query = &rect
		}
		values := make([]tjs.Value, 0, len(s.Display.DisplayList()))
		for _, monitor := range s.Display.DisplayList() {
			var intersect *[4]int32
			if query != nil {
				clip := intersection(*query, monitor.Bounds)
				if clip[2] <= 0 || clip[3] <= 0 {
					continue
				}
				intersect = &clip
			}
			value, err := monitorValue(vm, monitor, intersect)
			if err != nil {
				return tjs.VoidValue, err
			}
			values = append(values, value)
		}
		return vm.NewArray(values)
	case "getMonitorInfo":
		nearest := false
		if len(args) > 0 {
			value, err := args[0].Int()
			if err != nil {
				return tjs.VoidValue, err
			}
			nearest = value != 0
		}
		var monitor Monitor
		var ok bool
		switch len(args) {
		case 0:
			monitor, ok = s.Display.PrimaryDisplay(), true
		case 2:
			if args[1].Kind() != tjs.KindObject || args[1].IsNull() {
				return tjs.VoidValue, ErrInvalidWindow
			}
			window := s.Window(args[1].ObjectRef().Object)
			if window == nil {
				return tjs.VoidValue, ErrInvalidWindow
			}
			monitor, ok = s.Display.DisplayForRect(window.WindowRect(), nearest)
		case 3:
			x, err := windowIntValue(args[1])
			if err != nil {
				return tjs.VoidValue, err
			}
			y, err := windowIntValue(args[2])
			if err != nil {
				return tjs.VoidValue, err
			}
			monitor, ok = s.Display.MonitorInfoForPoint(x, y, nearest)
		case 5:
			rect := [4]int32{}
			for index := range rect {
				value, err := windowIntValue(args[index+1])
				if err != nil {
					return tjs.VoidValue, err
				}
				rect[index] = value
			}
			monitor, ok = s.Display.MonitorInfo(rect, nearest)
		default:
			return tjs.VoidValue, fmt.Errorf("System.getMonitorInfo requires 0, 2, 3 or 5 arguments")
		}
		if !ok {
			return tjs.VoidValue, nil
		}
		return monitorValue(vm, monitor, nil)
	case "getCursorPos", "setCursorPos":
		return tjs.VoidValue, UnsupportedDevice("cursor positioning")
	default:
		return tjs.VoidValue, fmt.Errorf("%w: System.%s", ErrUnsupportedNative, operation)
	}
}
