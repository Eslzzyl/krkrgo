package runtime

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/eslzzyl/krkrgo/internal/tjs"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

const (
	win32ICCUserExClasses = 0x00000200
	win32WMInitDialog     = 0x0110
	win32WMCommand        = 0x0111
	win32ENChange         = 0x0300
	win32BNClicked        = 0
	win32CBAddString      = 0x0143
	win32CBDeleteString   = 0x0144
	win32CBGetCount       = 0x0146
	win32CBGetCurSel      = 0x0147
	win32CBSetCurSel      = 0x014E
	win32CBEMInsertItemW  = 0x040B
	win32WMClose          = 0x0010
	win32ShiftControl     = 0x04
	win32ShiftStateShift  = 0x01
)

type win32DialogControl struct {
	id               int64
	class            string
	x                int
	y                int
	width            int
	height           int
	text             string
	style            int64
	enabled          bool
	readOnly         bool
	items            []string
	selection        int
	caret            int
	anchor           int
	composition      string
	compositionCaret int
}

type win32DialogState struct {
	objectID      tjs.ObjectID
	title         string
	x             int
	y             int
	width         int
	height        int
	positioned    bool
	visible       bool
	valid         bool
	focusedID     int64
	controls      map[int64]*win32DialogControl
	controlOrder  []int64
	pressedID     int64
	dragging      bool
	dragOffsetX   int
	dragOffsetY   int
	dropdownID    int64
	historyCursor int
	historyDraft  string
	frameRevision uint64
	frameWidth    int
	frameHeight   int
	frame         *image.RGBA
}

type win32DialogBlobState struct {
	size     int
	text     map[int]string
	integers map[int]int64
}

type win32DialogPresentation struct {
	frame  *image.RGBA
	width  int
	height int
	rev    uint64
}

// Win32DialogTextInputContext describes the focused portable edit control to a
// host IME. Byte offsets match Go strings; caret bounds use logical pixels.
type Win32DialogTextInputContext struct {
	DialogID           uint64
	ControlID          int64
	OriginalText       string
	SelectionStart     int
	SelectionEnd       int
	SelectionStartByte int
	SelectionEndByte   int
	TextBeforeCaret    string
	TextAfterCaret     string
	CaretBounds        image.Rectangle
}

// Win32DialogTextReplacement carries the surrounding text returned by an IME.
type Win32DialogTextReplacement struct {
	Before string
	After  string
}

func (s *Services) win32DialogState(context tjs.Value, create bool) (*win32DialogState, error) {
	if context.Kind() != tjs.KindObject || context.IsNull() {
		return nil, fmt.Errorf("WIN32Dialog requires an object context")
	}
	id := context.ObjectRef().Object
	if s.win32Dialogs == nil {
		s.win32Dialogs = make(map[tjs.ObjectID]*win32DialogState)
	}
	state := s.win32Dialogs[id]
	if state == nil && create {
		state = &win32DialogState{
			objectID:      id,
			width:         640,
			height:        480,
			valid:         true,
			controls:      make(map[int64]*win32DialogControl),
			historyCursor: -1,
		}
		s.win32Dialogs[id] = state
	}
	if state == nil {
		return nil, nil
	}
	return state, nil
}

func win32DialogObject(vm *tjs.VM, value tjs.Value) (*tjs.Object, bool) {
	if vm == nil || value.Kind() != tjs.KindObject || value.IsNull() {
		return nil, false
	}
	object, ok := vm.Objects.Lookup(value.ObjectRef().Object)
	return object, ok
}

func win32DialogMember(vm *tjs.VM, receiver tjs.Value, name string) (tjs.Value, bool) {
	object, ok := win32DialogObject(vm, receiver)
	if !ok {
		return tjs.VoidValue, false
	}
	return object.Get(name)
}

func win32DialogCopyMembers(vm *tjs.VM, receiver, source tjs.Value) error {
	destination, ok := win32DialogObject(vm, receiver)
	if !ok {
		return fmt.Errorf("WIN32Dialog.store requires a valid receiver")
	}
	input, ok := win32DialogObject(vm, source)
	if !ok {
		return fmt.Errorf("WIN32Dialog.store requires an object value")
	}
	for _, entry := range input.Entries() {
		if err := destination.Set(entry.Name, entry.Value); err != nil {
			return err
		}
	}
	return nil
}

func win32DialogValueInt(value tjs.Value) int64 {
	switch value.Kind() {
	case tjs.KindInteger:
		return value.IntegerValue()
	case tjs.KindReal:
		return int64(value.RealValue())
	case tjs.KindString:
		parsed, _ := strconv.ParseInt(strings.TrimSpace(value.Text()), 0, 64)
		return parsed
	default:
		return 0
	}
}

func win32DialogValueString(value tjs.Value) string {
	if value.IsVoid() || value.IsNull() {
		return ""
	}
	return value.Text()
}

func win32DialogSetMember(vm *tjs.VM, receiver tjs.Value, name string, value tjs.Value, host *Services, budget *uint64) error {
	return vm.SetMemberWithRuntime(receiver, name, value, 0, host, budget)
}

func (s *Services) invalidateWin32DialogFrame() {
	s.win32DialogRevision++
	s.win32DialogPresentation = win32DialogPresentation{}
}

func (s *Services) win32DialogControl(state *win32DialogState, id int64) *win32DialogControl {
	if state == nil {
		return nil
	}
	return state.controls[id]
}

func win32DialogSetText(control *win32DialogControl, text string) {
	if control == nil {
		return
	}
	control.text = text
	control.caret = utf8.RuneCountInString(text)
	control.anchor = control.caret
	control.composition = ""
	control.compositionCaret = 0
}

func (s *Services) win32DialogNative(vm *tjs.VM, context tjs.Value, operation string, args []tjs.Value, budget *uint64) (tjs.Value, error) {
	if vm == nil {
		return tjs.VoidValue, fmt.Errorf("WIN32Dialog requires a VM")
	}
	s.ensureInit()

	if operation == "initCommonControls" || operation == "initCommonControlsEx" {
		if len(args) > 1 {
			return tjs.VoidValue, fmt.Errorf("WIN32Dialog.%s accepts at most one argument", operation)
		}
		return tjs.IntegerValue(1), nil
	}
	if operation == "messageBox" {
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

	if operation == "Header.store" || operation == "Items.store" {
		if len(args) != 1 {
			return tjs.VoidValue, fmt.Errorf("WIN32Dialog.%s accepts one object", operation)
		}
		if err := win32DialogCopyMembers(vm, context, args[0]); err != nil {
			return tjs.VoidValue, err
		}
		return tjs.IntegerValue(1), nil
	}
	if strings.HasPrefix(operation, "Blob.") {
		return s.win32DialogBlobNative(vm, context, strings.TrimPrefix(operation, "Blob."), args, budget)
	}
	if strings.HasPrefix(operation, "Notify.") {
		return s.win32DialogNotifyNative(vm, context, strings.TrimPrefix(operation, "Notify."), args)
	}
	if operation == "construct" || operation == "WIN32Dialog" || strings.HasSuffix(operation, ".@initialize") {
		if operation == "Blob.@initialize" {
			return s.win32DialogBlobNative(vm, context, "@initialize", args, budget)
		}
		if operation == "WIN32Dialog.@initialize" || operation == "WIN32Dialog" {
			_, err := s.win32DialogState(context, true)
			return tjs.VoidValue, err
		}
		return tjs.VoidValue, nil
	}

	state, err := s.win32DialogState(context, true)
	if err != nil {
		return tjs.VoidValue, err
	}
	if state == nil {
		return tjs.VoidValue, fmt.Errorf("WIN32Dialog.%s requires a dialog receiver", operation)
	}

	switch operation {
	case "makeTemplate":
		if len(args) < 1 {
			return tjs.VoidValue, fmt.Errorf("WIN32Dialog.makeTemplate requires a header")
		}
		if err := s.win32DialogMakeTemplate(vm, state, args); err != nil {
			return tjs.VoidValue, err
		}
		return tjs.IntegerValue(1), nil
	case "open":
		state.visible = true
		state.valid = true
		if !state.positioned {
			rect := s.win32DialogBounds(state, int(s.Display.ScreenWidth), int(s.Display.ScreenHeight))
			state.x, state.y, state.positioned = rect.Min.X, rect.Min.Y, true
		}
		s.invalidateWin32DialogFrame()
		if err := s.win32DialogCall(vm, context, "onInit", []tjs.Value{
			tjs.IntegerValue(win32WMInitDialog), tjs.IntegerValue(0), tjs.IntegerValue(0),
		}, budget); err != nil {
			return tjs.VoidValue, err
		}
		if state.focusedID == 0 {
			for _, id := range state.controlOrder {
				control := state.controls[id]
				if control != nil && control.enabled && (control.class == "combobox" || control.class == "comboboxex") {
					state.focusedID = id
					break
				}
			}
		}
		return tjs.IntegerValue(1), nil
	case "close", "closeProgress":
		state.visible = false
		state.dropdownID = 0
		s.invalidateWin32DialogFrame()
		return tjs.IntegerValue(1), nil
	case "show":
		if len(args) > 0 {
			state.visible = win32DialogValueInt(args[0]) != 0
		}
		s.invalidateWin32DialogFrame()
		return tjs.IntegerValue(1), nil
	case "finalize", "@invalidate":
		state.visible = false
		state.valid = false
		s.invalidateWin32DialogFrame()
		return tjs.VoidValue, nil
	case "add":
		if len(args) != 1 {
			return tjs.VoidValue, fmt.Errorf("WIN32Dialog.add accepts one item")
		}
		return tjs.IntegerValue(0), nil
	case "setItemText":
		if len(args) < 2 {
			return tjs.VoidValue, fmt.Errorf("WIN32Dialog.setItemText requires an id and text")
		}
		id := win32DialogValueInt(args[0])
		text := win32DialogValueString(args[1])
		if id == 0 {
			state.title = text
		} else {
			win32DialogSetText(state.controls[id], text)
		}
		s.invalidateWin32DialogFrame()
		return tjs.IntegerValue(1), nil
	case "getItemText":
		if len(args) < 1 {
			return tjs.VoidValue, fmt.Errorf("WIN32Dialog.getItemText requires an id")
		}
		id := win32DialogValueInt(args[0])
		if id == 0 {
			return tjs.StringValue(state.title), nil
		}
		if control := state.controls[id]; control != nil {
			return tjs.StringValue(control.text), nil
		}
		return tjs.StringValue(""), nil
	case "setItemEnabled":
		if len(args) < 2 {
			return tjs.VoidValue, fmt.Errorf("WIN32Dialog.setItemEnabled requires an id and value")
		}
		if control := state.controls[win32DialogValueInt(args[0])]; control != nil {
			control.enabled = win32DialogValueInt(args[1]) != 0
		}
		s.invalidateWin32DialogFrame()
		return tjs.IntegerValue(1), nil
	case "getItemEnabled":
		if len(args) < 1 {
			return tjs.VoidValue, fmt.Errorf("WIN32Dialog.getItemEnabled requires an id")
		}
		if control := state.controls[win32DialogValueInt(args[0])]; control != nil && control.enabled {
			return tjs.IntegerValue(1), nil
		}
		return tjs.IntegerValue(0), nil
	case "setItemFocus":
		if len(args) < 1 {
			return tjs.VoidValue, fmt.Errorf("WIN32Dialog.setItemFocus requires an id")
		}
		state.focusedID = win32DialogValueInt(args[0])
		state.historyCursor = -1
		s.invalidateWin32DialogFrame()
		return tjs.IntegerValue(1), nil
	case "setItemPos", "setItemSize":
		if len(args) < 3 {
			return tjs.VoidValue, fmt.Errorf("WIN32Dialog.%s requires an id and two coordinates", operation)
		}
		if control := state.controls[win32DialogValueInt(args[0])]; control != nil {
			if operation == "setItemPos" {
				control.x, control.y = int(win32DialogValueInt(args[1])), int(win32DialogValueInt(args[2]))
			} else {
				control.width, control.height = int(win32DialogValueInt(args[1])), int(win32DialogValueInt(args[2]))
			}
		}
		s.invalidateWin32DialogFrame()
		return tjs.IntegerValue(1), nil
	case "setPos":
		if len(args) > 0 && !args[0].IsVoid() {
			state.x = int(win32DialogValueInt(args[0]))
			state.positioned = true
		}
		if len(args) > 1 && !args[1].IsVoid() {
			state.y = int(win32DialogValueInt(args[1]))
			state.positioned = true
		}
		if len(args) > 2 && !args[2].IsVoid() {
			state.width = int(win32DialogValueInt(args[2]))
		}
		if len(args) > 3 && !args[3].IsVoid() {
			state.height = int(win32DialogValueInt(args[3]))
		}
		s.invalidateWin32DialogFrame()
		return tjs.IntegerValue(1), nil
	case "setSize":
		if len(args) > 0 && !args[0].IsVoid() {
			state.width = int(win32DialogValueInt(args[0]))
		}
		if len(args) > 1 && !args[1].IsVoid() {
			state.height = int(win32DialogValueInt(args[1]))
		}
		s.invalidateWin32DialogFrame()
		return tjs.IntegerValue(1), nil
	case "getBaseUnits":
		return win32DialogDictionary(vm, map[string]tjs.Value{"h": tjs.IntegerValue(7), "v": tjs.IntegerValue(16)})
	case "getClientRect":
		return win32DialogDictionary(vm, map[string]tjs.Value{
			"x": tjs.IntegerValue(0), "y": tjs.IntegerValue(0),
			"w": tjs.IntegerValue(int64(state.width)), "h": tjs.IntegerValue(int64(state.height)),
		})
	case "getItem", "isExistentItem":
		if len(args) < 1 {
			return tjs.VoidValue, fmt.Errorf("WIN32Dialog.%s requires an id", operation)
		}
		_, exists := state.controls[win32DialogValueInt(args[0])]
		if operation == "isExistentItem" {
			if exists {
				return tjs.IntegerValue(1), nil
			}
			return tjs.IntegerValue(0), nil
		}
		if exists {
			return args[0], nil
		}
		return tjs.VoidValue, nil
	case "getItemClassName":
		if len(args) > 0 {
			if control := state.controls[win32DialogValueInt(args[0])]; control != nil {
				return tjs.StringValue(control.class), nil
			}
		}
		return tjs.StringValue(""), nil
	case "sendItemMessage":
		return s.win32DialogSendItemMessage(vm, state, context, args, budget)
	case "getCurSel":
		if len(args) > 0 {
			if control := state.controls[win32DialogValueInt(args[0])]; control != nil {
				return tjs.IntegerValue(int64(control.selection)), nil
			}
		}
		return tjs.IntegerValue(-1), nil
	case "setCurSel":
		if len(args) > 1 {
			if control := state.controls[win32DialogValueInt(args[0])]; control != nil {
				control.selection = int(win32DialogValueInt(args[1]))
				if control.selection >= 0 && control.selection < len(control.items) {
					win32DialogSetText(control, control.items[control.selection])
				}
			}
		}
		s.invalidateWin32DialogFrame()
		return tjs.IntegerValue(1), nil
	case "getItemLeft", "getItemTop", "getItemWidth", "getItemHeight", "getItemID", "getItemInt", "getItemLong":
		return s.win32DialogGetItemNumber(state, operation, args)
	case "bringToFront", "invalidateAll", "invalidateRect", "lockItemUpdate", "unlockItemUpdate", "onCommand", "onNotify", "onInit", "onSize", "onHScroll", "onVScroll", "setMessageResult":
		s.invalidateWin32DialogFrame()
		return tjs.IntegerValue(0), nil
	case "get:left", "get:top", "get:width", "get:height", "get:title", "get:visible", "get:isValid":
		return win32DialogGetProperty(state, strings.TrimPrefix(operation, "get:")), nil
	case "getWindowRect":
		return win32DialogDictionary(vm, map[string]tjs.Value{
			"x": tjs.IntegerValue(int64(state.x)), "y": tjs.IntegerValue(int64(state.y)),
			"w": tjs.IntegerValue(int64(state.width)), "h": tjs.IntegerValue(int64(state.height)),
		})
	case "set:left", "set:top", "set:width", "set:height", "set:title", "set:visible":
		if len(args) < 1 {
			return tjs.VoidValue, fmt.Errorf("WIN32Dialog.%s requires a value", operation)
		}
		if err := win32DialogSetProperty(s, state, strings.TrimPrefix(operation, "set:"), args[0]); err != nil {
			return tjs.VoidValue, err
		}
		return tjs.VoidValue, nil
	default:
		return tjs.VoidValue, fmt.Errorf("unsupported WIN32Dialog operation %q with %d arguments", operation, len(args))
	}
}

func win32DialogDictionary(vm *tjs.VM, values map[string]tjs.Value) (tjs.Value, error) {
	result, err := vm.NewDictionary()
	if err != nil {
		return tjs.VoidValue, err
	}
	object, ok := vm.Objects.Lookup(result.ObjectRef().Object)
	if !ok {
		return tjs.VoidValue, fmt.Errorf("WIN32Dialog result dictionary is unavailable")
	}
	for name, value := range values {
		if err := object.Set(name, value); err != nil {
			return tjs.VoidValue, err
		}
	}
	return result, nil
}

func (s *Services) win32DialogCall(vm *tjs.VM, receiver tjs.Value, name string, args []tjs.Value, budget *uint64) error {
	method, err := vm.GetMember(receiver, name, s, budget)
	if err != nil {
		return err
	}
	if method.IsVoid() {
		return nil
	}
	_, err = vm.CallFunction(method, receiver, args, s, budget)
	return err
}

func (s *Services) win32DialogMakeTemplate(vm *tjs.VM, state *win32DialogState, args []tjs.Value) error {
	header := args[0]
	get := func(object tjs.Value, name string) tjs.Value {
		value, _ := win32DialogMember(vm, object, name)
		return value
	}
	state.title = win32DialogValueString(get(header, "title"))
	state.width = max(1, int(win32DialogValueInt(get(header, "cx")))*7/4) + 16
	state.height = max(1, int(win32DialogValueInt(get(header, "cy")))*16/8) + 35
	state.positioned = !get(header, "x").IsVoid() || !get(header, "y").IsVoid()
	state.x = int(win32DialogValueInt(get(header, "x")))
	state.y = int(win32DialogValueInt(get(header, "y")))
	state.controls = make(map[int64]*win32DialogControl, len(args)-1)
	state.controlOrder = state.controlOrder[:0]
	for _, itemValue := range args[1:] {
		id := win32DialogValueInt(get(itemValue, "id"))
		classValue := get(itemValue, "windowClass")
		class := strings.ToLower(win32DialogValueString(classValue))
		if classValue.Kind() == tjs.KindInteger {
			switch classValue.IntegerValue() {
			case 0x80:
				class = "button"
			case 0x81:
				class = "edit"
			case 0x82:
				class = "static"
			case 0x85:
				class = "combobox"
			}
		}
		if strings.Contains(class, "comboboxex") {
			class = "comboboxex"
		} else if strings.Contains(class, "combobox") {
			class = "combobox"
		} else if strings.Contains(class, "button") {
			class = "button"
		} else if strings.Contains(class, "static") {
			class = "static"
		} else if strings.Contains(class, "edit") {
			class = "edit"
		}
		style := win32DialogValueInt(get(itemValue, "style"))
		control := &win32DialogControl{
			id:        id,
			class:     class,
			x:         int(win32DialogValueInt(get(itemValue, "x"))) * 7 / 4,
			y:         int(win32DialogValueInt(get(itemValue, "y"))) * 16 / 8,
			width:     max(1, int(win32DialogValueInt(get(itemValue, "cx")))*7/4),
			height:    max(1, int(win32DialogValueInt(get(itemValue, "cy")))*16/8),
			text:      win32DialogValueString(get(itemValue, "title")),
			style:     style,
			enabled:   style&0x08000000 == 0,
			readOnly:  style&0x00000800 != 0,
			selection: -1,
		}
		control.caret = utf8.RuneCountInString(control.text)
		control.anchor = control.caret
		state.controls[id] = control
		state.controlOrder = append(state.controlOrder, id)
	}
	state.frame = nil
	s.invalidateWin32DialogFrame()
	return nil
}

func win32DialogGetProperty(state *win32DialogState, property string) tjs.Value {
	switch property {
	case "left":
		return tjs.IntegerValue(int64(state.x))
	case "top":
		return tjs.IntegerValue(int64(state.y))
	case "width":
		return tjs.IntegerValue(int64(state.width))
	case "height":
		return tjs.IntegerValue(int64(state.height))
	case "title":
		return tjs.StringValue(state.title)
	case "visible":
		if state.visible {
			return tjs.IntegerValue(1)
		}
	case "isValid":
		if state.valid {
			return tjs.IntegerValue(1)
		}
	}
	return tjs.IntegerValue(0)
}

func win32DialogSetProperty(s *Services, state *win32DialogState, property string, value tjs.Value) error {
	switch property {
	case "left":
		state.x, state.positioned = int(win32DialogValueInt(value)), true
	case "top":
		state.y, state.positioned = int(win32DialogValueInt(value)), true
	case "width":
		state.width = max(1, int(win32DialogValueInt(value)))
	case "height":
		state.height = max(1, int(win32DialogValueInt(value)))
	case "title":
		state.title = win32DialogValueString(value)
	case "visible":
		state.visible = win32DialogValueInt(value) != 0
	}
	s.invalidateWin32DialogFrame()
	return nil
}

func (s *Services) win32DialogGetItemNumber(state *win32DialogState, operation string, args []tjs.Value) (tjs.Value, error) {
	if len(args) < 1 {
		return tjs.VoidValue, fmt.Errorf("WIN32Dialog.%s requires an id", operation)
	}
	control := state.controls[win32DialogValueInt(args[0])]
	if control == nil {
		return tjs.IntegerValue(0), nil
	}
	var value int
	switch operation {
	case "getItemLeft":
		value = control.x
	case "getItemTop":
		value = control.y
	case "getItemWidth":
		value = control.width
	case "getItemHeight":
		value = control.height
	case "getItemID":
		value = int(control.id)
	}
	return tjs.IntegerValue(int64(value)), nil
}

func (s *Services) win32DialogSendItemMessage(vm *tjs.VM, state *win32DialogState, receiver tjs.Value, args []tjs.Value, budget *uint64) (tjs.Value, error) {
	if len(args) < 2 {
		return tjs.VoidValue, fmt.Errorf("WIN32Dialog.sendItemMessage requires id and message")
	}
	id := win32DialogValueInt(args[0])
	message := win32DialogValueInt(args[1])
	wparam, lparam := int64(0), int64(0)
	if len(args) > 2 {
		wparam = win32DialogValueInt(args[2])
	}
	if len(args) > 3 {
		lparam = win32DialogValueInt(args[3])
	}
	control := state.controls[id]
	if control == nil {
		return tjs.IntegerValue(-1), nil
	}
	result := int64(0)
	switch message {
	case win32CBGetCount:
		result = int64(len(control.items))
	case win32CBGetCurSel:
		result = int64(control.selection)
	case win32CBSetCurSel:
		control.selection = int(wparam)
		if control.selection >= 0 && control.selection < len(control.items) {
			win32DialogSetText(control, control.items[control.selection])
		}
		result = int64(control.selection)
	case win32CBDeleteString:
		index := int(wparam)
		if index < 0 || index >= len(control.items) {
			result = -1
		} else {
			control.items = append(control.items[:index], control.items[index+1:]...)
			if control.selection >= len(control.items) {
				control.selection = len(control.items) - 1
			}
			result = int64(len(control.items))
		}
	case win32CBAddString:
		text := ""
		if len(args) > 3 {
			text = win32DialogValueString(args[3])
		}
		control.items = append(control.items, text)
		result = int64(len(control.items) - 1)
	case win32CBEMInsertItemW:
		blobID := tjs.ObjectID(lparam)
		if blob := s.win32DialogBlobs[blobID]; blob != nil {
			text := blob.text[8]
			if value, ok := blob.text[16]; ok {
				text = value
			}
			control.items = append(control.items, text)
			result = int64(len(control.items) - 1)
		} else {
			result = -1
		}
	case 0x000C: // WM_SETTEXT
		if len(args) > 3 {
			win32DialogSetText(control, win32DialogValueString(args[3]))
		}
	case 0x000D: // WM_GETTEXT
		return tjs.StringValue(control.text), nil
	case 0x000E: // WM_GETTEXTLENGTH
		result = int64(utf8.RuneCountInString(control.text))
	case 0x000B, 0x00B1, 0x00B7: // WM_SETREDRAW, EM_SETSEL, EM_SCROLLCARET
		result = 1
	default:
		return tjs.IntegerValue(0), nil
	}
	s.invalidateWin32DialogFrame()
	return tjs.IntegerValue(result), nil
}

func (s *Services) win32DialogBlobNative(vm *tjs.VM, context tjs.Value, operation string, args []tjs.Value, budget *uint64) (tjs.Value, error) {
	if context.Kind() != tjs.KindObject || context.IsNull() {
		return tjs.VoidValue, fmt.Errorf("WIN32Dialog.Blob requires an object context")
	}
	id := context.ObjectRef().Object
	if s.win32DialogBlobs == nil {
		s.win32DialogBlobs = make(map[tjs.ObjectID]*win32DialogBlobState)
	}
	blob := s.win32DialogBlobs[id]
	if operation == "@initialize" || operation == "Blob" {
		size := 0
		if len(args) > 0 {
			size = int(win32DialogValueInt(args[0]))
		}
		if size < 0 || size > 1<<20 {
			return tjs.VoidValue, fmt.Errorf("WIN32Dialog.Blob size must be between 0 and 1048576")
		}
		blob = &win32DialogBlobState{size: size, text: make(map[int]string), integers: make(map[int]int64)}
		s.win32DialogBlobs[id] = blob
		if object, ok := win32DialogObject(vm, context); ok {
			if err := object.Set("size", tjs.IntegerValue(int64(size))); err != nil {
				return tjs.VoidValue, err
			}
			if err := object.Set("pointer", tjs.IntegerValue(int64(id))); err != nil {
				return tjs.VoidValue, err
			}
		}
		return tjs.VoidValue, nil
	}
	if blob == nil {
		blob = &win32DialogBlobState{text: make(map[int]string), integers: make(map[int]int64)}
		s.win32DialogBlobs[id] = blob
	}
	if operation == "finalize" {
		delete(s.win32DialogBlobs, id)
		return tjs.VoidValue, nil
	}
	if len(args) == 0 {
		return tjs.VoidValue, fmt.Errorf("WIN32Dialog.Blob.%s requires an offset", operation)
	}
	offset := int(win32DialogValueInt(args[0]))
	if len(args) < 2 && strings.HasPrefix(operation, "set") {
		return tjs.VoidValue, fmt.Errorf("WIN32Dialog.Blob.%s requires a value", operation)
	}
	switch operation {
	case "setText":
		blob.text[offset] = win32DialogValueString(args[1])
	case "setDWord", "setDWordLong", "setWord", "setByte":
		blob.integers[offset] = win32DialogValueInt(args[1])
	case "getText":
		return tjs.StringValue(blob.text[offset]), nil
	case "getDWord", "getWord", "getByte":
		return tjs.IntegerValue(blob.integers[offset]), nil
	case "ReferPointer":
		return tjs.IntegerValue(int64(id)), nil
	default:
		return tjs.VoidValue, fmt.Errorf("unsupported WIN32Dialog.Blob operation %q", operation)
	}
	return tjs.VoidValue, nil
}

func (s *Services) win32DialogNotifyNative(vm *tjs.VM, context tjs.Value, operation string, args []tjs.Value) (tjs.Value, error) {
	if operation == "@initialize" || operation == "finalize" {
		return tjs.VoidValue, nil
	}
	object, ok := win32DialogObject(vm, context)
	if !ok {
		return tjs.VoidValue, fmt.Errorf("WIN32Dialog.Notify requires an object context")
	}
	if len(args) == 0 {
		return tjs.VoidValue, fmt.Errorf("WIN32Dialog.Notify.%s requires an offset", operation)
	}
	value, _ := object.Get("reason")
	switch operation {
	case "getDWord":
		return value, nil
	case "getWord":
		return tjs.IntegerValue(win32DialogValueInt(value) & 0xffff), nil
	case "getByte":
		return tjs.IntegerValue(win32DialogValueInt(value) & 0xff), nil
	default:
		return tjs.VoidValue, fmt.Errorf("unsupported WIN32Dialog.Notify operation %q", operation)
	}
}

func (s *Services) Win32DialogFrame(width, height int) (*image.RGBA, uint64) {
	if s == nil {
		return nil, 0
	}
	s.ensureInit()
	if width <= 0 {
		width = int(s.Display.ScreenWidth)
	}
	if height <= 0 {
		height = int(s.Display.ScreenHeight)
	}
	if width <= 0 || height <= 0 {
		return nil, s.win32DialogRevision
	}
	visible := make([]*win32DialogState, 0, len(s.win32Dialogs))
	for _, state := range s.win32Dialogs {
		if state != nil && state.visible && state.valid {
			visible = append(visible, state)
		}
	}
	if len(visible) == 0 {
		s.win32DialogPresentation = win32DialogPresentation{}
		return nil, s.win32DialogRevision
	}
	sort.Slice(visible, func(i, j int) bool { return visible[i].objectID < visible[j].objectID })
	if presentation := s.win32DialogPresentation; presentation.frame != nil &&
		presentation.rev == s.win32DialogRevision && presentation.width == width && presentation.height == height {
		return presentation.frame, presentation.rev
	}
	frame := image.NewRGBA(image.Rect(0, 0, width, height))
	for _, state := range visible {
		s.drawWin32Dialog(frame, state)
	}
	s.win32DialogPresentation = win32DialogPresentation{
		frame: frame, width: width, height: height, rev: s.win32DialogRevision,
	}
	return frame, s.win32DialogRevision
}

func (s *Services) win32DialogBounds(state *win32DialogState, screenWidth, screenHeight int) image.Rectangle {
	width := max(160, state.width)
	height := max(100, state.height)
	x, y := state.x, state.y
	if !state.positioned {
		x = (screenWidth - width) / 2
		y = (screenHeight - height) / 2
	}
	return image.Rect(x, y, x+width, y+height)
}

func (s *Services) drawWin32Dialog(canvas *image.RGBA, state *win32DialogState) {
	bounds := s.win32DialogBounds(state, canvas.Bounds().Dx(), canvas.Bounds().Dy())
	clipped := bounds.Intersect(canvas.Bounds())
	if clipped.Empty() {
		return
	}
	draw.Draw(canvas, clipped, &image.Uniform{C: color.RGBA{R: 54, G: 64, B: 78, A: 255}}, image.Point{}, draw.Src)
	outer := bounds.Inset(1)
	draw.Draw(canvas, outer.Intersect(canvas.Bounds()), &image.Uniform{C: color.RGBA{R: 242, G: 243, B: 245, A: 255}}, image.Point{}, draw.Src)
	titleHeight := 28
	titleRect := image.Rect(bounds.Min.X+1, bounds.Min.Y+1, bounds.Max.X-1, min(bounds.Min.Y+titleHeight, bounds.Max.Y-1))
	draw.Draw(canvas, titleRect.Intersect(canvas.Bounds()), &image.Uniform{C: color.RGBA{R: 38, G: 54, B: 78, A: 255}}, image.Point{}, draw.Src)
	face := s.win32DialogFontFace()
	defer face.Close()
	s.drawWin32DialogText(canvas, face, state.title, titleRect.Inset(8), color.White, 1)
	closeRect := image.Rect(bounds.Max.X-27, bounds.Min.Y+4, bounds.Max.X-5, bounds.Min.Y+24).Intersect(canvas.Bounds())
	draw.Draw(canvas, closeRect, &image.Uniform{C: color.RGBA{R: 185, G: 58, B: 58, A: 255}}, image.Point{}, draw.Src)
	s.drawWin32DialogText(canvas, face, "×", closeRect, color.White, 0)
	clientOrigin := image.Pt(bounds.Min.X+8, bounds.Min.Y+titleHeight+7)
	for _, id := range state.controlOrder {
		control := state.controls[id]
		if control == nil || control.style&0x10000000 == 0 {
			continue
		}
		rect := image.Rect(clientOrigin.X+control.x, clientOrigin.Y+control.y,
			clientOrigin.X+control.x+control.width, clientOrigin.Y+control.y+control.height)
		rect = rect.Intersect(canvas.Bounds())
		if rect.Empty() {
			continue
		}
		switch control.class {
		case "edit":
			if control.style&0x00000004 != 0 {
				s.drawWin32DialogMultiline(canvas, face, rect, control.text)
			} else {
				s.drawWin32DialogEdit(canvas, face, rect, control, control.id == state.focusedID)
			}
		case "combobox", "comboboxex":
			s.drawWin32DialogCombo(canvas, face, rect, control, control.id == state.focusedID)
		case "button":
			draw.Draw(canvas, rect, &image.Uniform{C: color.RGBA{R: 225, G: 228, B: 232, A: 255}}, image.Point{}, draw.Src)
			win32DialogFrameRect(canvas, rect, color.RGBA{R: 110, G: 116, B: 124, A: 255})
			s.drawWin32DialogText(canvas, face, control.text, rect.Inset(3), color.RGBA{R: 24, G: 28, B: 34, A: 255}, 0)
		case "static":
			s.drawWin32DialogText(canvas, face, control.text, rect, color.RGBA{R: 30, G: 34, B: 40, A: 255}, 1)
		}
	}
	if state.dropdownID != 0 {
		if control := state.controls[state.dropdownID]; control != nil && len(control.items) != 0 {
			combo := image.Rect(clientOrigin.X+control.x, clientOrigin.Y+control.y,
				clientOrigin.X+control.x+control.width, clientOrigin.Y+control.y+control.height)
			visibleItems := min(8, len(control.items))
			popup := image.Rect(combo.Min.X, combo.Max.Y, combo.Max.X, combo.Max.Y+visibleItems*20).Intersect(canvas.Bounds())
			draw.Draw(canvas, popup, &image.Uniform{C: color.White}, image.Point{}, draw.Src)
			win32DialogFrameRect(canvas, popup, color.RGBA{R: 88, G: 96, B: 106, A: 255})
			start := max(0, len(control.items)-visibleItems)
			for row := 0; row < visibleItems; row++ {
				itemIndex := start + row
				line := image.Rect(popup.Min.X+1, popup.Min.Y+row*20, popup.Max.X-1, popup.Min.Y+(row+1)*20)
				if itemIndex == control.selection {
					draw.Draw(canvas, line.Intersect(canvas.Bounds()), &image.Uniform{C: color.RGBA{R: 47, G: 104, B: 178, A: 255}}, image.Point{}, draw.Src)
				}
				var ink color.Color = color.RGBA{R: 28, G: 32, B: 38, A: 255}
				if itemIndex == control.selection {
					ink = color.RGBA{R: 255, G: 255, B: 255, A: 255}
				}
				s.drawWin32DialogText(canvas, face, control.items[itemIndex], line.Inset(4), ink, 1)
			}
		}
	}
}

// Win32DialogTextInputContext returns the currently focused editable control
// and its caret position for a platform IME.
func (s *Services) Win32DialogTextInputContext(width, height int) (Win32DialogTextInputContext, bool) {
	if s == nil {
		return Win32DialogTextInputContext{}, false
	}
	s.ensureInit()
	if width <= 0 {
		width = int(s.Display.ScreenWidth)
	}
	if height <= 0 {
		height = int(s.Display.ScreenHeight)
	}
	if width <= 0 {
		width = 1280
	}
	if height <= 0 {
		height = 720
	}
	states := make([]*win32DialogState, 0, len(s.win32Dialogs))
	for _, state := range s.win32Dialogs {
		if state != nil && state.visible && state.valid {
			states = append(states, state)
		}
	}
	sort.Slice(states, func(i, j int) bool { return states[i].objectID > states[j].objectID })
	if len(states) == 0 {
		return Win32DialogTextInputContext{}, false
	}
	state := states[0]
	control := state.controls[state.focusedID]
	if control == nil || !control.enabled || control.readOnly || control.style&0x10000000 == 0 {
		return Win32DialogTextInputContext{}, false
	}
	if control.class != "edit" && control.class != "combobox" && control.class != "comboboxex" {
		return Win32DialogTextInputContext{}, false
	}
	if control.class == "edit" && control.style&0x00000004 != 0 {
		return Win32DialogTextInputContext{}, false
	}
	runes := []rune(control.text)
	selectionStart := max(0, min(min(control.anchor, control.caret), len(runes)))
	selectionEnd := max(selectionStart, min(max(control.anchor, control.caret), len(runes)))
	startByte := len(string(runes[:selectionStart]))
	endByte := len(string(runes[:selectionEnd]))
	bounds := s.win32DialogBounds(state, width, height)
	controlRect := win32DialogControlRectAt(state, control, width, height)
	textRect := controlRect.Inset(4)
	if control.class == "combobox" || control.class == "comboboxex" {
		textRect = image.Rect(controlRect.Min.X+4, controlRect.Min.Y+1, controlRect.Max.X-24, controlRect.Max.Y-1)
	}
	face := s.win32DialogFontFace()
	caretX := textRect.Min.X + 4 + font.MeasureString(face, string(runes[:selectionStart])).Ceil()
	lineHeight := max(1, face.Metrics().Height.Ceil())
	face.Close()
	caretY := textRect.Min.Y + max(0, (textRect.Dy()-lineHeight)/2)
	return Win32DialogTextInputContext{
		DialogID: uint64(state.objectID), ControlID: control.id,
		OriginalText: control.text, SelectionStart: selectionStart, SelectionEnd: selectionEnd,
		SelectionStartByte: startByte, SelectionEndByte: endByte,
		TextBeforeCaret: control.text[:startByte], TextAfterCaret: control.text[endByte:],
		CaretBounds: image.Rect(caretX, caretY, caretX+1, caretY+lineHeight).Intersect(bounds),
	}, true
}

// Win32DialogSetComposition updates the preedit text shown in the focused field.
func (s *Services) Win32DialogSetComposition(text string, selectionStartByte int) {
	if s == nil {
		return
	}
	s.ensureInit()
	if text == "" {
		changed := false
		for _, state := range s.win32Dialogs {
			if state == nil {
				continue
			}
			for _, control := range state.controls {
				if control != nil && control.composition != "" {
					control.composition = ""
					control.compositionCaret = 0
					changed = true
				}
			}
		}
		if changed {
			s.invalidateWin32DialogFrame()
		}
		return
	}
	states := make([]*win32DialogState, 0, len(s.win32Dialogs))
	for _, state := range s.win32Dialogs {
		if state != nil && state.visible && state.valid {
			states = append(states, state)
		}
	}
	sort.Slice(states, func(i, j int) bool { return states[i].objectID > states[j].objectID })
	if len(states) == 0 {
		return
	}
	state := states[0]
	control := state.controls[state.focusedID]
	if control == nil || control.readOnly || control.style&0x10000000 == 0 {
		return
	}
	if control.class != "edit" && control.class != "combobox" && control.class != "comboboxex" {
		return
	}
	selectionStartByte = max(0, min(selectionStartByte, len(text)))
	for selectionStartByte > 0 && selectionStartByte < len(text) && !utf8.RuneStart(text[selectionStartByte]) {
		selectionStartByte--
	}
	caret := utf8.RuneCountInString(text[:selectionStartByte])
	if control.composition == text && control.compositionCaret == caret {
		return
	}
	control.composition = text
	control.compositionCaret = caret
	s.invalidateWin32DialogFrame()
}

// ApplyWin32DialogTextInputCommit applies text delivered by a host IME and
// emits the edit notification expected by the dialog script.
func (s *Session) ApplyWin32DialogTextInputCommit(context Win32DialogTextInputContext, text string, replacement *Win32DialogTextReplacement) error {
	if err := s.checkOpen(); err != nil {
		return err
	}
	state := s.Services.win32Dialogs[tjs.ObjectID(context.DialogID)]
	if state == nil || !state.visible || !state.valid || state.focusedID != context.ControlID {
		return nil
	}
	control := state.controls[context.ControlID]
	if control == nil || control.readOnly || control.text != context.OriginalText {
		return nil
	}
	if replacement != nil {
		startByte := max(0, context.SelectionStartByte-len(context.TextBeforeCaret))
		endByte := min(len(control.text), context.SelectionEndByte+len(context.TextAfterCaret))
		if startByte > endByte || startByte < len(control.text) && !utf8.RuneStart(control.text[startByte]) ||
			endByte < len(control.text) && !utf8.RuneStart(control.text[endByte]) {
			return fmt.Errorf("WIN32Dialog IME replacement offsets are invalid")
		}
		control.text = control.text[:startByte] + replacement.Before + text + replacement.After + control.text[endByte:]
		caretByte := startByte + len(replacement.Before) + len(text)
		control.caret = utf8.RuneCountInString(control.text[:caretByte])
		control.anchor = control.caret
	} else {
		control.anchor, control.caret = context.SelectionStart, context.SelectionEnd
		win32DialogReplaceSelection(control, text)
	}
	control.composition = ""
	control.compositionCaret = 0
	receiver := tjs.ObjectValue(tjs.NewObjectRef(tjs.ObjectID(context.DialogID)))
	return s.Services.win32DialogAfterEdit(s.VM, receiver, state, control, &s.Budget)
}

func (s *Services) win32DialogFontFace() font.Face {
	face, err := s.fontFace(&FontState{Face: defaultFontFaceName, Height: -13})
	if err == nil && face != nil {
		return face
	}
	return basicfont.Face7x13
}

func (s *Services) drawWin32DialogText(canvas *image.RGBA, face font.Face, text string, rect image.Rectangle, ink color.Color, align int) {
	if text == "" || rect.Empty() {
		return
	}
	destination, ok := canvas.SubImage(rect).(draw.Image)
	if !ok {
		return
	}
	width := font.MeasureString(face, text).Ceil()
	x := rect.Min.X + 4
	if align == 0 {
		x = rect.Min.X + max(0, (rect.Dx()-width)/2)
	} else if align == 2 {
		x = rect.Max.X - width - 4
	}
	metrics := face.Metrics()
	y := rect.Min.Y + max(0, (rect.Dy()-metrics.Height.Ceil())/2) + metrics.Ascent.Ceil()
	drawer := font.Drawer{Dst: destination, Src: image.NewUniform(ink), Face: face, Dot: fixed.P(x, y)}
	drawer.DrawString(text)
}

func (s *Services) drawWin32DialogMultiline(canvas *image.RGBA, face font.Face, rect image.Rectangle, text string) {
	background := color.RGBA{R: 0, G: 0, B: 128, A: 255}
	draw.Draw(canvas, rect, &image.Uniform{C: background}, image.Point{}, draw.Src)
	win32DialogFrameRect(canvas, rect, color.RGBA{R: 120, G: 126, B: 138, A: 255})
	lineHeight := max(1, face.Metrics().Height.Ceil())
	visibleLines := max(1, (rect.Dy()-8)/lineHeight)
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if len(lines) > visibleLines {
		lines = lines[len(lines)-visibleLines:]
	}
	y := rect.Min.Y + 4
	for _, line := range lines {
		lineRect := image.Rect(rect.Min.X+4, y, rect.Max.X-4, min(rect.Max.Y, y+lineHeight))
		s.drawWin32DialogText(canvas, face, line, lineRect, color.White, 1)
		y += lineHeight
		if y >= rect.Max.Y {
			break
		}
	}
}

func (s *Services) drawWin32DialogEdit(canvas *image.RGBA, face font.Face, rect image.Rectangle, control *win32DialogControl, focused bool) {
	draw.Draw(canvas, rect, &image.Uniform{C: color.RGBA{R: 0, G: 0, B: 128, A: 255}}, image.Point{}, draw.Src)
	win32DialogFrameRect(canvas, rect, color.RGBA{R: 120, G: 126, B: 138, A: 255})
	textRect := rect.Inset(4)
	s.drawWin32DialogFieldText(canvas, face, textRect, control, focused, color.White)
}

func (s *Services) drawWin32DialogCombo(canvas *image.RGBA, face font.Face, rect image.Rectangle, control *win32DialogControl, focused bool) {
	draw.Draw(canvas, rect, &image.Uniform{C: color.White}, image.Point{}, draw.Src)
	win32DialogFrameRect(canvas, rect, color.RGBA{R: 92, G: 99, B: 108, A: 255})
	button := image.Rect(rect.Max.X-22, rect.Min.Y, rect.Max.X, rect.Max.Y)
	draw.Draw(canvas, button, &image.Uniform{C: color.RGBA{R: 223, G: 226, B: 230, A: 255}}, image.Point{}, draw.Src)
	s.drawWin32DialogText(canvas, face, "▾", button, color.RGBA{R: 25, G: 29, B: 35, A: 255}, 0)
	textRect := image.Rect(rect.Min.X+4, rect.Min.Y+1, button.Min.X-2, rect.Max.Y-1)
	s.drawWin32DialogFieldText(canvas, face, textRect, control, focused, color.RGBA{R: 20, G: 23, B: 28, A: 255})
}

func (s *Services) drawWin32DialogFieldText(canvas *image.RGBA, face font.Face, rect image.Rectangle, control *win32DialogControl, focused bool, ink color.Color) {
	runes := []rune(control.text)
	selectionStart := max(0, min(min(control.anchor, control.caret), len(runes)))
	selectionEnd := max(selectionStart, min(max(control.anchor, control.caret), len(runes)))
	display := control.text
	caretRune := max(0, min(control.caret, len(runes)))
	compositionStartX := 0
	compositionWidth := 0
	if focused && control.composition != "" {
		prefix := string(runes[:selectionStart])
		suffix := string(runes[selectionEnd:])
		display = prefix + control.composition + suffix
		compositionStartX = rect.Min.X + 4 + font.MeasureString(face, prefix).Ceil()
		compositionWidth = font.MeasureString(face, control.composition).Ceil()
	}
	s.drawWin32DialogText(canvas, face, display, rect, ink, 1)
	if focused {
		var caretText string
		if control.composition != "" {
			prefix := string(runes[:selectionStart])
			preedit := []rune(control.composition)
			preeditCaret := max(0, min(control.compositionCaret, len(preedit)))
			caretText = prefix + string(preedit[:preeditCaret])
		} else {
			caretText = string(runes[:caretRune])
		}
		x := rect.Min.X + 4 + font.MeasureString(face, caretText).Ceil()
		draw.Draw(canvas, image.Rect(x, rect.Min.Y+2, x+1, rect.Max.Y-2), &image.Uniform{C: ink}, image.Point{}, draw.Src)
	}
	if focused && control.composition != "" && compositionWidth > 0 {
		draw.Draw(canvas, image.Rect(compositionStartX, rect.Max.Y-3, compositionStartX+compositionWidth, rect.Max.Y-2).Intersect(canvas.Bounds()), &image.Uniform{C: ink}, image.Point{}, draw.Src)
	}
}

func win32DialogFrameRect(canvas *image.RGBA, rect image.Rectangle, ink color.Color) {
	if rect.Empty() {
		return
	}
	line := image.NewUniform(ink)
	draw.Draw(canvas, image.Rect(rect.Min.X, rect.Min.Y, rect.Max.X, rect.Min.Y+1).Intersect(canvas.Bounds()), line, image.Point{}, draw.Src)
	draw.Draw(canvas, image.Rect(rect.Min.X, rect.Max.Y-1, rect.Max.X, rect.Max.Y).Intersect(canvas.Bounds()), line, image.Point{}, draw.Src)
	draw.Draw(canvas, image.Rect(rect.Min.X, rect.Min.Y, rect.Min.X+1, rect.Max.Y).Intersect(canvas.Bounds()), line, image.Point{}, draw.Src)
	draw.Draw(canvas, image.Rect(rect.Max.X-1, rect.Min.Y, rect.Max.X, rect.Max.Y).Intersect(canvas.Bounds()), line, image.Point{}, draw.Src)
}

func (s *Services) handleWin32DialogInput(vm *tjs.VM, event InputEvent, budget *uint64) (bool, error) {
	if s == nil || vm == nil {
		return false, nil
	}
	s.ensureInit()
	if event.Kind == InputClick && s.win32DialogSuppressClick {
		s.win32DialogSuppressClick = false
		return true, nil
	}
	if event.Kind != InputClick {
		s.win32DialogSuppressClick = false
	}
	states := make([]*win32DialogState, 0, len(s.win32Dialogs))
	for _, state := range s.win32Dialogs {
		if state != nil && state.visible && state.valid {
			states = append(states, state)
		}
	}
	if len(states) == 0 {
		return false, nil
	}
	sort.Slice(states, func(i, j int) bool { return states[i].objectID > states[j].objectID })
	width, height := int(s.Display.ScreenWidth), int(s.Display.ScreenHeight)
	if width <= 0 {
		width = 1280
	}
	if height <= 0 {
		height = 720
	}
	state := states[0]
	windowRect := s.win32DialogBounds(state, width, height)
	receiver := tjs.ObjectValue(tjs.NewObjectRef(state.objectID))

	switch event.Kind {
	case InputPointerMove:
		if state.dragging {
			state.x, state.y = int(event.X)-state.dragOffsetX, int(event.Y)-state.dragOffsetY
			state.positioned = true
			s.invalidateWin32DialogFrame()
			return true, nil
		}
		return false, nil
	case InputPointerDown:
		if event.Button != 0 {
			return image.Pt(int(event.X), int(event.Y)).In(windowRect), nil
		}
		point := image.Pt(int(event.X), int(event.Y))
		if state.dropdownID != 0 {
			if control := state.controls[state.dropdownID]; control != nil {
				combo := s.win32DialogControlRect(state, control)
				start := max(0, len(control.items)-8)
				popup := image.Rect(combo.Min.X, combo.Max.Y, combo.Max.X, combo.Max.Y+min(8, len(control.items))*20)
				if point.In(popup) {
					row := (point.Y - popup.Min.Y) / 20
					index := start + row
					if index >= 0 && index < len(control.items) {
						control.selection = index
						win32DialogSetText(control, control.items[index])
						state.focusedID = control.id
						state.dropdownID = 0
						state.historyCursor = -1
						s.invalidateWin32DialogFrame()
						if err := s.win32DialogCommand(vm, receiver, control.id, win32ENChange, budget); err != nil {
							return true, err
						}
						return true, nil
					}
				}
			}
			state.dropdownID = 0
			s.invalidateWin32DialogFrame()
		}
		if !point.In(windowRect) {
			return false, nil
		}
		closeRect := image.Rect(windowRect.Max.X-27, windowRect.Min.Y+4, windowRect.Max.X-5, windowRect.Min.Y+24)
		if point.In(closeRect) {
			state.pressedID = -1
			return true, nil
		}
		titleRect := image.Rect(windowRect.Min.X, windowRect.Min.Y, windowRect.Max.X, windowRect.Min.Y+28)
		if point.In(titleRect) {
			state.dragging = true
			state.dragOffsetX = point.X - windowRect.Min.X
			state.dragOffsetY = point.Y - windowRect.Min.Y
			return true, nil
		}
		state.pressedID = 0
		for _, id := range state.controlOrder {
			control := state.controls[id]
			if control == nil || !control.enabled || control.style&0x10000000 == 0 {
				continue
			}
			rect := s.win32DialogControlRect(state, control)
			if !point.In(rect) {
				continue
			}
			state.focusedID = id
			state.pressedID = id
			if control.class == "combobox" || control.class == "comboboxex" {
				state.historyCursor = -1
				if point.X >= rect.Max.X-22 {
					if state.dropdownID == id {
						state.dropdownID = 0
					} else {
						state.dropdownID = id
					}
				} else {
					state.dropdownID = 0
					character := max(0, (point.X-rect.Min.X-6)/8)
					control.caret = min(character, utf8.RuneCountInString(control.text))
					control.anchor = control.caret
				}
			} else if control.class == "edit" {
				character := max(0, (point.X-rect.Min.X-5)/8)
				control.caret = min(character, utf8.RuneCountInString(control.text))
				control.anchor = control.caret
			}
			s.invalidateWin32DialogFrame()
			return true, nil
		}
		return true, nil
	case InputPointerUp:
		if state.dragging {
			state.dragging = false
			return true, nil
		}
		pressedID := state.pressedID
		state.pressedID = 0
		if pressedID == -1 {
			if image.Pt(int(event.X), int(event.Y)).In(windowRect) {
				return true, s.win32DialogCommand(vm, receiver, 2, win32BNClicked, budget)
			}
			return true, nil
		}
		if pressedID != 0 {
			control := state.controls[pressedID]
			if control != nil && control.class == "button" && image.Pt(int(event.X), int(event.Y)).In(s.win32DialogControlRect(state, control)) {
				return true, s.win32DialogCommand(vm, receiver, control.id, win32BNClicked, budget)
			}
		}
		return image.Pt(int(event.X), int(event.Y)).In(windowRect), nil
	case InputClick, InputDoubleClick, InputWheel:
		if image.Pt(int(event.X), int(event.Y)).In(windowRect) {
			return true, nil
		}
		return false, nil
	case InputKeyDown:
		control := state.controls[state.focusedID]
		if control == nil {
			return false, nil
		}
		return true, s.win32DialogKeyDown(vm, receiver, state, control, event, budget)
	case InputKeyUp:
		return state.focusedID != 0, nil
	case InputText:
		control := state.controls[state.focusedID]
		if control == nil || control.readOnly || control.class != "combobox" && control.class != "comboboxex" && control.class != "edit" {
			return state.focusedID != 0, nil
		}
		if event.Text == "" {
			return true, nil
		}
		win32DialogReplaceSelection(control, event.Text)
		state.dropdownID = 0
		state.historyCursor = -1
		s.invalidateWin32DialogFrame()
		return true, s.win32DialogCommand(vm, receiver, control.id, win32ENChange, budget)
	default:
		return false, nil
	}
}

func (s *Services) win32DialogControlRect(state *win32DialogState, control *win32DialogControl) image.Rectangle {
	width, height := max(1, int(s.Display.ScreenWidth)), max(1, int(s.Display.ScreenHeight))
	return win32DialogControlRectAt(state, control, width, height)
}

func win32DialogControlRectAt(state *win32DialogState, control *win32DialogControl, width, height int) image.Rectangle {
	windowWidth, windowHeight := max(160, state.width), max(100, state.height)
	x, y := state.x, state.y
	if !state.positioned {
		x = (width - windowWidth) / 2
		y = (height - windowHeight) / 2
	}
	bounds := image.Rect(x, y, x+windowWidth, y+windowHeight)
	origin := image.Pt(bounds.Min.X+8, bounds.Min.Y+35)
	return image.Rect(origin.X+control.x, origin.Y+control.y, origin.X+control.x+control.width, origin.Y+control.y+control.height)
}

func win32DialogReplaceSelection(control *win32DialogControl, inserted string) {
	runes := []rune(control.text)
	start, end := min(control.anchor, control.caret), max(control.anchor, control.caret)
	start = max(0, min(start, len(runes)))
	end = max(start, min(end, len(runes)))
	next := make([]rune, 0, len(runes)+utf8.RuneCountInString(inserted))
	next = append(next, runes[:start]...)
	next = append(next, []rune(inserted)...)
	next = append(next, runes[end:]...)
	control.text = string(next)
	control.caret = start + utf8.RuneCountInString(inserted)
	control.anchor = control.caret
}

func win32DialogDeleteSelection(control *win32DialogControl) bool {
	runes := []rune(control.text)
	start, end := min(control.anchor, control.caret), max(control.anchor, control.caret)
	start = max(0, min(start, len(runes)))
	end = max(start, min(end, len(runes)))
	if start == end {
		return false
	}
	runes = append(runes[:start], runes[end:]...)
	control.text = string(runes)
	control.caret, control.anchor = start, start
	return true
}

func (s *Services) win32DialogKeyDown(vm *tjs.VM, receiver tjs.Value, state *win32DialogState, control *win32DialogControl, event InputEvent, budget *uint64) error {
	key := event.Key
	ctrl := event.Shift&win32ShiftControl != 0
	shift := event.Shift&win32ShiftStateShift != 0
	runes := []rune(control.text)
	length := len(runes)
	if ctrl {
		switch key {
		case 0x41: // Ctrl+A
			control.anchor, control.caret = 0, length
			s.invalidateWin32DialogFrame()
			return nil
		case 0x43: // Ctrl+C
			start, end := min(control.anchor, control.caret), max(control.anchor, control.caret)
			if start != end && s.clipboardBackend != nil {
				_ = s.clipboardBackend.WriteText(string(runes[start:end]))
			}
			return nil
		case 0x58: // Ctrl+X
			start, end := min(control.anchor, control.caret), max(control.anchor, control.caret)
			if start != end && s.clipboardBackend != nil {
				_ = s.clipboardBackend.WriteText(string(runes[start:end]))
			}
			if win32DialogDeleteSelection(control) {
				return s.win32DialogAfterEdit(vm, receiver, state, control, budget)
			}
			return nil
		case 0x56: // Ctrl+V
			if s.clipboardBackend != nil {
				text, ok, err := s.clipboardBackend.ReadText()
				if err == nil && ok {
					win32DialogReplaceSelection(control, text)
					return s.win32DialogAfterEdit(vm, receiver, state, control, budget)
				}
			}
			return nil
		}
	}
	switch key {
	case 0x0D: // Enter
		if control.class == "combobox" || control.class == "comboboxex" {
			state.dropdownID = 0
			state.historyCursor = -1
			s.invalidateWin32DialogFrame()
			return s.win32DialogNotify(vm, receiver, control.id, budget)
		}
		if control.class == "button" {
			return s.win32DialogCommand(vm, receiver, control.id, win32BNClicked, budget)
		}
		if control.class == "edit" && !control.readOnly && control.style&0x00000004 != 0 {
			win32DialogReplaceSelection(control, "\n")
			return s.win32DialogAfterEdit(vm, receiver, state, control, budget)
		}
		return nil
	case 0x1B: // Escape
		if state.dropdownID != 0 {
			state.dropdownID = 0
			s.invalidateWin32DialogFrame()
			return nil
		}
		return s.win32DialogCommand(vm, receiver, 2, win32BNClicked, budget)
	case 0x08: // Backspace
		if !control.readOnly {
			if !win32DialogDeleteSelection(control) && control.caret > 0 {
				control.anchor = control.caret - 1
				win32DialogDeleteSelection(control)
			}
			return s.win32DialogAfterEdit(vm, receiver, state, control, budget)
		}
	case 0x2E: // Delete
		if !control.readOnly {
			if !win32DialogDeleteSelection(control) && control.caret < length {
				control.anchor = control.caret + 1
				win32DialogDeleteSelection(control)
			}
			return s.win32DialogAfterEdit(vm, receiver, state, control, budget)
		}
	case 0x25, 0x27, 0x24, 0x23: // Left, Right, Home, End
		if control.readOnly {
			return nil
		}
		if !shift && control.anchor != control.caret {
			if key == 0x25 || key == 0x24 {
				control.caret = min(control.anchor, control.caret)
			} else {
				control.caret = max(control.anchor, control.caret)
			}
			control.anchor = control.caret
		} else {
			switch key {
			case 0x25:
				control.caret = max(0, control.caret-1)
			case 0x27:
				control.caret = min(length, control.caret+1)
			case 0x24:
				control.caret = 0
			case 0x23:
				control.caret = length
			}
			if !shift {
				control.anchor = control.caret
			}
		}
		s.invalidateWin32DialogFrame()
		return nil
	case 0x26, 0x28: // Up, Down
		if control.class == "combobox" || control.class == "comboboxex" {
			return s.win32DialogHistory(vm, receiver, state, control, key == 0x26, budget)
		}
	}
	return nil
}

func (s *Services) win32DialogAfterEdit(vm *tjs.VM, receiver tjs.Value, state *win32DialogState, control *win32DialogControl, budget *uint64) error {
	state.dropdownID = 0
	state.historyCursor = -1
	s.invalidateWin32DialogFrame()
	return s.win32DialogCommand(vm, receiver, control.id, win32ENChange, budget)
}

func (s *Services) win32DialogHistory(vm *tjs.VM, receiver tjs.Value, state *win32DialogState, control *win32DialogControl, previous bool, budget *uint64) error {
	count := len(control.items)
	if count == 0 {
		return nil
	}
	if state.historyCursor < 0 {
		state.historyCursor = count
		state.historyDraft = control.text
	}
	if previous {
		state.historyCursor = max(0, state.historyCursor-1)
	} else {
		state.historyCursor = min(count, state.historyCursor+1)
	}
	if state.historyCursor == count {
		win32DialogSetText(control, state.historyDraft)
		control.selection = -1
	} else {
		control.selection = state.historyCursor
		win32DialogSetText(control, control.items[state.historyCursor])
	}
	s.invalidateWin32DialogFrame()
	return s.win32DialogCommand(vm, receiver, control.id, win32ENChange, budget)
}

func (s *Services) win32DialogCommand(vm *tjs.VM, receiver tjs.Value, id int64, notification int32, budget *uint64) error {
	packed := int64(uint16(id)) | int64(uint16(notification))<<16
	return s.win32DialogCall(vm, receiver, "onCommand", []tjs.Value{
		tjs.IntegerValue(win32WMCommand), tjs.IntegerValue(packed), tjs.IntegerValue(0),
	}, budget)
}

func (s *Services) win32DialogNotify(vm *tjs.VM, receiver tjs.Value, id int64, budget *uint64) error {
	root, ok := vm.Globals["WIN32Dialog"]
	if !ok {
		return fmt.Errorf("WIN32Dialog.Notify class is unavailable")
	}
	notifyClass, err := vm.GetMember(root, "Notify", s, budget)
	if err != nil {
		return err
	}
	notify, err := vm.Construct(notifyClass, nil, s, budget)
	if err != nil {
		return err
	}
	object, ok := win32DialogObject(vm, notify)
	if !ok {
		return fmt.Errorf("WIN32Dialog.Notify object is unavailable")
	}
	if err := object.Set("code", tjs.IntegerValue(-806)); err != nil {
		return err
	}
	if err := object.Set("idFrom", tjs.IntegerValue(id)); err != nil {
		return err
	}
	if err := object.Set("reason", tjs.IntegerValue(2)); err != nil {
		return err
	}
	return s.win32DialogCall(vm, receiver, "onNotify", []tjs.Value{tjs.IntegerValue(id), notify}, budget)
}
