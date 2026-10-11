package runtime

import (
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/eslzzyl/krkrgo/internal/tjs"
)

type menuItemState struct {
	constructed     bool
	actionOwner     tjs.Value
	window          tjs.Value
	caption         tjs.Value
	checked         bool
	enabled         bool
	visible         bool
	radio           bool
	group           int32
	shortcut        int32
	parent          tjs.ObjectID
	displayParent   tjs.ObjectID
	children        []tjs.ObjectID
	displayChildren []tjs.ObjectID
	array           tjs.Value
	arrayDirty      bool
}

type menuPopupRequest struct {
	item      tjs.ObjectID
	arguments []tjs.Value
}

// MenuPopupRequest is the headless host's retained popup request. Rendering is
// deliberately left to a host; the runtime keeps the requested item and args.
type MenuPopupRequest struct {
	Item      tjs.ObjectID
	Arguments []tjs.Value
}

type menuModel struct {
	installed           bool
	items               map[tjs.ObjectID]*menuItemState
	roots               map[tjs.ObjectID]tjs.Value
	textToKeycode       tjs.Value
	keycodeToText       tjs.Value
	textToKeycodeValues map[string]int32
	keycodeToTextValues map[int32]string
	lastPopup           *menuPopupRequest
}

func (s *Services) menuState() *menuModel {
	if s.menuData.items == nil {
		s.menuData.items = make(map[tjs.ObjectID]*menuItemState)
	}
	if s.menuData.roots == nil {
		s.menuData.roots = make(map[tjs.ObjectID]tjs.Value)
	}
	return &s.menuData
}

func menuPluginPath(name string) string {
	return pluginLinkKey(name)
}

func isMenuPlugin(name string) bool {
	return path.Base(menuPluginPath(name)) == "menu.dll"
}

func (s *Services) installMenuNative(vm *tjs.VM) error {
	model := s.menuState()
	if model.installed {
		if _, ok := vm.Globals["MenuItem"]; ok {
			return nil
		}
	}
	class, err := vm.RegisterNativeClass("MenuItem")
	if err != nil {
		return err
	}
	for _, method := range []string{"@initialize", "@invalidate", "MenuItem", "finalize", "add", "insert", "remove", "popup", "onClick", "fireClick"} {
		if err := vm.RegisterNativeMethod(class, method, "MenuItem."+method); err != nil {
			return err
		}
	}
	for _, property := range []string{"caption", "checked", "enabled", "group", "radio", "shortcut", "visible", "parent", "children", "root", "window", "index", "HMENU"} {
		setter := "MenuItem.set:" + property
		switch property {
		case "parent", "children", "root", "window", "HMENU":
			setter = ""
		}
		if err := vm.RegisterNativeProperty(class, property, "MenuItem.get:"+property, setter); err != nil {
			return err
		}
	}
	for _, property := range []string{"textToKeycode", "keycodeToText"} {
		if err := vm.RegisterNativeStaticProperty(class, property, "MenuItem.get:"+property, ""); err != nil {
			return err
		}
	}
	if err := model.installKeyMaps(vm); err != nil {
		return err
	}
	model.installed = true
	return nil
}

func (m *menuModel) installKeyMaps(vm *tjs.VM) error {
	if m.textToKeycode.Kind() == tjs.KindObject && !m.textToKeycode.IsNull() && m.keycodeToText.Kind() == tjs.KindObject && !m.keycodeToText.IsNull() {
		return nil
	}
	names := map[int32]string{
		8: "Backspace", 9: "Tab", 12: "Num 5", 13: "Enter", 16: "Shift", 17: "Ctrl", 18: "Alt", 19: "Pause", 20: "Caps Lock", 27: "Esc", 32: "Space", 33: "Page Up", 34: "Page Down", 35: "End", 36: "Home", 37: "Left", 38: "Up", 39: "Right", 40: "Down", 44: "Print Screen", 45: "Insert", 46: "Delete", 106: "Num *", 107: "Num +", 109: "Num -", 110: "Num Del", 111: "Num /", 144: "Num Lock", 145: "Scroll Lock",
	}
	for key := int32(48); key <= 57; key++ {
		names[key] = string(rune(key))
	}
	for key := int32(65); key <= 90; key++ {
		names[key] = string(rune(key))
	}
	for key := int32(96); key <= 105; key++ {
		names[key] = fmt.Sprintf("Num %d", key-96)
	}
	for key := int32(112); key <= 135; key++ {
		names[key] = fmt.Sprintf("F%d", key-111)
	}

	textToKeycode, err := vm.NewDictionary()
	if err != nil {
		return err
	}
	textObject, ok := vm.Objects.Lookup(textToKeycode.ObjectRef().Object)
	if !ok {
		return errors.New("MenuItem keycode dictionary was not registered")
	}
	keys := make([]tjs.Value, 256)
	for i := range keys {
		keys[i] = tjs.VoidValue
	}
	m.textToKeycodeValues = make(map[string]int32, len(names)+4)
	m.keycodeToTextValues = make(map[int32]string, len(names))
	for key, name := range names {
		lower := strings.ToLower(name)
		if err := textObject.Set(lower, tjs.IntegerValue(int64(key))); err != nil {
			return err
		}
		m.textToKeycodeValues[lower] = key
		m.keycodeToTextValues[key] = name
		keys[key] = tjs.StringValue(name)
	}
	for alias, key := range map[string]int32{"bksp": 8, "pgup": 33, "pgdn": 34, "del": 46} {
		if err := textObject.Set(alias, tjs.IntegerValue(int64(key))); err != nil {
			return err
		}
		m.textToKeycodeValues[alias] = key
	}
	keycodeToText, err := vm.NewArray(keys)
	if err != nil {
		return err
	}
	m.textToKeycode = textToKeycode
	m.keycodeToText = keycodeToText
	return nil
}

func (s *Services) menuItem(context tjs.Value, operation string) (tjs.ObjectID, *menuItemState, error) {
	if context.Kind() != tjs.KindObject || context.IsNull() || context.ObjectRef().Object == 0 {
		return 0, nil, fmt.Errorf("MenuItem.%s requires a non-null object context", operation)
	}
	id := context.ObjectRef().Object
	item := s.menuState().items[id]
	if item == nil {
		return id, nil, fmt.Errorf("MenuItem.%s context has no native instance", operation)
	}
	if !item.constructed {
		return id, nil, fmt.Errorf("MenuItem.%s constructor has not run", operation)
	}
	return id, item, nil
}

func menuItemValue(id tjs.ObjectID) tjs.Value {
	return tjs.ObjectIDValue(id)
}

func menuArg(args []tjs.Value, index int, operation string) (tjs.Value, error) {
	if index < 0 || index >= len(args) {
		return tjs.VoidValue, fmt.Errorf("MenuItem.%s: missing argument %d", operation, index)
	}
	return args[index], nil
}

func menuIntArg(args []tjs.Value, index int, operation string) (int64, error) {
	value, err := menuArg(args, index, operation)
	if err != nil {
		return 0, err
	}
	return value.Int()
}

func menuBool(value tjs.Value) (bool, error) {
	return value.Truth()
}
func menuBoolInt(value bool) int64 {
	if value {
		return 1
	}
	return 0
}

func menuCharge(budget *uint64) error {
	if budget == nil {
		return nil
	}
	if *budget == 0 {
		return ErrBudgetExceeded
	}
	*budget = *budget - 1
	return nil
}

func (s *Services) menuIndex(id tjs.ObjectID) int64 {
	item := s.menuData.items[id]
	if item == nil || item.displayParent == 0 {
		return -1
	}
	parent := s.menuData.items[item.displayParent]
	if parent == nil {
		return -1
	}
	for index, child := range parent.displayChildren {
		if child == id {
			return int64(index)
		}
	}
	return -1
}

func (s *Services) menuRoot(id tjs.ObjectID, budget *uint64) (tjs.ObjectID, error) {
	seen := make(map[tjs.ObjectID]struct{})
	for {
		if _, _, err := s.menuItem(menuItemValue(id), "root"); err != nil {
			return 0, err
		}
		if _, exists := seen[id]; exists {
			return 0, errors.New("MenuItem hierarchy contains a cycle")
		}
		seen[id] = struct{}{}
		item := s.menuData.items[id]
		if item.parent == 0 {
			return id, nil
		}
		if err := menuCharge(budget); err != nil {
			return 0, err
		}
		id = item.parent
	}
}

func (s *Services) windowMenu(vm *tjs.VM, windowID tjs.ObjectID, budget *uint64) (tjs.Value, error) {
	model := s.menuState()
	if !model.installed {
		return tjs.VoidValue, errors.New("menu plugin is not loaded")
	}
	window := s.Window(windowID)
	if window == nil || !window.Constructed {
		return tjs.VoidValue, errors.New("Window constructor has not run")
	}
	if root, ok := model.roots[windowID]; ok {
		return root, nil
	}
	class, ok := vm.Globals["MenuItem"]
	if !ok {
		return tjs.VoidValue, errors.New("menu plugin is not loaded")
	}
	windowValue := menuItemValue(windowID)
	root, err := vm.Construct(class, []tjs.Value{windowValue, windowValue}, s, budget)
	if err != nil {
		return tjs.VoidValue, err
	}
	model.roots[windowID] = root
	return root, nil
}

func (s *Services) destroyWindowMenu(vm *tjs.VM, windowID tjs.ObjectID, budget *uint64) error {
	model := s.menuState()
	root, ok := model.roots[windowID]
	if !ok {
		return nil
	}
	delete(model.roots, windowID)
	_, err := vm.Invalidate(root, s, budget)
	return err
}

func (s *Services) menuAttach(parentID, childID tjs.ObjectID, index *int32, budget *uint64) error {
	_, parent, err := s.menuItem(menuItemValue(parentID), "add")
	if err != nil {
		return err
	}
	_, child, err := s.menuItem(menuItemValue(childID), "add")
	if err != nil {
		return err
	}
	ancestor := parentID
	for ancestor != 0 {
		if err := menuCharge(budget); err != nil {
			return err
		}
		if ancestor == childID {
			return errors.New("MenuItem hierarchy cannot contain a cycle")
		}
		item := s.menuData.items[ancestor]
		if item == nil {
			break
		}
		ancestor = item.displayParent
	}
	remaining := len(parent.displayChildren)
	if child.displayParent == parentID {
		remaining--
	}
	insertIndex := remaining
	if index != nil {
		insertIndex = int(*index)
		if insertIndex < 0 {
			insertIndex = 0
		}
	}
	if insertIndex > remaining {
		return errors.New("MenuItem insertion index out of range")
	}
	if child.displayParent != 0 {
		oldParent := s.menuData.items[child.displayParent]
		if oldParent != nil {
			oldParent.displayChildren = removeMenuID(oldParent.displayChildren, childID)
		}
	}
	child.displayParent = parentID
	parent.displayChildren = insertMenuID(parent.displayChildren, insertIndex, childID)
	if !containsMenuID(parent.children, childID) {
		parent.children = append(parent.children, childID)
		parent.arrayDirty = true
		child.parent = parentID
	}
	if child.checked && child.radio {
		return s.menuCheckRadio(childID, budget)
	}
	return nil
}

func (s *Services) menuCheckRadio(id tjs.ObjectID, budget *uint64) error {
	item := s.menuData.items[id]
	if item == nil {
		return errors.New("MenuItem does not exist")
	}
	if parent := s.menuData.items[item.displayParent]; parent != nil {
		for _, siblingID := range parent.displayChildren {
			if err := menuCharge(budget); err != nil {
				return err
			}
			sibling := s.menuData.items[siblingID]
			if sibling != nil && sibling.group == item.group && sibling.radio {
				sibling.checked = false
			}
		}
	}
	item.checked = true
	return nil
}

func (s *Services) menuReconsiderRadio(id tjs.ObjectID, budget *uint64) error {
	item := s.menuData.items[id]
	if item == nil || !item.radio || !item.checked {
		return nil
	}
	parent := s.menuData.items[item.displayParent]
	if parent == nil {
		return nil
	}
	for _, siblingID := range parent.displayChildren {
		if err := menuCharge(budget); err != nil {
			return err
		}
		sibling := s.menuData.items[siblingID]
		if sibling != nil && sibling.radio && sibling.checked && sibling.group == item.group {
			item.checked = false
			break
		}
	}
	return nil
}

func containsMenuID(items []tjs.ObjectID, id tjs.ObjectID) bool {
	for _, item := range items {
		if item == id {
			return true
		}
	}
	return false
}

func removeMenuID(items []tjs.ObjectID, id tjs.ObjectID) []tjs.ObjectID {
	for index, item := range items {
		if item == id {
			copy(items[index:], items[index+1:])
			return items[:len(items)-1]
		}
	}
	return items
}

func insertMenuID(items []tjs.ObjectID, index int, id tjs.ObjectID) []tjs.ObjectID {
	items = append(items, 0)
	copy(items[index+1:], items[index:len(items)-1])
	items[index] = id
	return items
}

func (s *Services) menuChildren(vm *tjs.VM, item *menuItemState, budget *uint64) (tjs.Value, error) {
	if item.array.Kind() != tjs.KindObject || item.array.IsNull() {
		array, err := vm.NewArray(nil)
		if err != nil {
			return tjs.VoidValue, err
		}
		item.array = array
		item.arrayDirty = true
	}
	if item.arrayDirty {
		if err := vm.ClearArray(item.array); err != nil {
			return tjs.VoidValue, err
		}
		for _, child := range item.children {
			if err := menuCharge(budget); err != nil {
				return tjs.VoidValue, err
			}
			if err := vm.AppendArray(item.array, menuItemValue(child)); err != nil {
				return tjs.VoidValue, err
			}
		}
		item.arrayDirty = false
	}
	return item.array, nil
}

func menuShortcutString(code int32, names map[int32]string) string {
	var value strings.Builder
	for _, flag := range []struct {
		bit  int32
		name string
	}{{4, "Shift+"}, {8, "Ctrl+"}, {16, "Alt+"}} {
		if code>>16&flag.bit != 0 {
			value.WriteString(flag.name)
		}
	}
	key := code & 0xffff
	if key >= 8 && key <= 255 {
		value.WriteString(names[key])
	}
	return value.String()
}

func (s *Services) menuShortcut(value tjs.Value) int32 {
	text := strings.ToLower(strings.SplitN(value.Text(), "\x00", 2)[0])
	flags, tail := int32(0), 0
	for _, prefix := range []struct {
		text string
		flag int32
	}{{"shift+", 4}, {"ctrl+", 8}, {"alt+", 16}} {
		if index := strings.Index(text, prefix.text); index >= 0 {
			flags |= prefix.flag
			if end := index + len(prefix.text); end > tail {
				tail = end
			}
		}
	}
	key, ok := s.menuData.textToKeycodeValues[text[tail:]]
	if !ok {
		return 0
	}
	return (flags|1)<<16 | (key & 0xffff)
}

func (s *Services) menuClick(vm *tjs.VM, id tjs.ObjectID, item *menuItemState, budget *uint64) (tjs.Value, error) {
	if item.actionOwner.Kind() != tjs.KindObject || item.actionOwner.IsNull() {
		return tjs.VoidValue, nil
	}
	action, err := vm.GetMember(item.actionOwner, "action", s, budget)
	if err != nil {
		return tjs.VoidValue, err
	}
	if action.IsVoid() {
		return tjs.VoidValue, nil
	}
	event, err := vm.NewDictionary()
	if err != nil {
		return tjs.VoidValue, err
	}
	eventObject, ok := vm.Objects.Lookup(event.ObjectRef().Object)
	if !ok {
		return tjs.VoidValue, errors.New("MenuItem click event was not registered")
	}
	if err := eventObject.Set("type", tjs.StringValue("onClick")); err != nil {
		return tjs.VoidValue, err
	}
	if err := eventObject.Set("target", menuItemValue(id)); err != nil {
		return tjs.VoidValue, err
	}
	return vm.CallFunction(action, item.actionOwner, []tjs.Value{event}, s, budget)
}

func (s *Services) menuNative(vm *tjs.VM, context tjs.Value, operation string, args []tjs.Value, budget *uint64) (tjs.Value, error) {
	model := s.menuState()
	if operation == "get:textToKeycode" {
		return model.textToKeycode, nil
	}
	if operation == "get:keycodeToText" {
		return model.keycodeToText, nil
	}
	if context.Kind() != tjs.KindObject || context.IsNull() || context.ObjectRef().Object == 0 {
		return tjs.VoidValue, fmt.Errorf("MenuItem.%s requires an object context", operation)
	}
	id := context.ObjectRef().Object
	if operation == "@initialize" {
		model.items[id] = &menuItemState{actionOwner: tjs.NullValue, window: tjs.NullValue, caption: tjs.StringValue(""), enabled: true, visible: true, group: -1, arrayDirty: true}
		return tjs.VoidValue, nil
	}
	if operation == "@invalidate" {
		return tjs.VoidValue, s.invalidateMenuItem(vm, id, budget)
	}
	if operation == "MenuItem" {
		return tjs.VoidValue, s.constructMenuItem(id, args)
	}
	_, item, err := s.menuItem(context, operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	if strings.HasPrefix(operation, "get:") {
		switch strings.TrimPrefix(operation, "get:") {
		case "caption":
			return item.caption, nil
		case "checked":
			return tjs.IntegerValue(menuBoolInt(item.checked)), nil
		case "enabled":
			return tjs.IntegerValue(menuBoolInt(item.enabled)), nil
		case "visible":
			return tjs.IntegerValue(menuBoolInt(item.visible)), nil
		case "radio":
			return tjs.IntegerValue(menuBoolInt(item.radio)), nil
		case "group":
			return tjs.IntegerValue(int64(item.group)), nil
		case "index":
			return tjs.IntegerValue(s.menuIndex(id)), nil
		case "parent":
			if item.parent == 0 {
				return tjs.NullValue, nil
			}
			return menuItemValue(item.parent), nil
		case "window":
			return item.window, nil
		case "root":
			root, err := s.menuRoot(id, budget)
			if err != nil {
				return tjs.VoidValue, err
			}
			return menuItemValue(root), nil
		case "shortcut":
			return tjs.StringValue(menuShortcutString(item.shortcut, model.keycodeToTextValues)), nil
		case "children":
			return s.menuChildren(vm, item, budget)
		case "HMENU":
			return tjs.IntegerValue(0), nil
		default:
			return tjs.VoidValue, fmt.Errorf("unsupported MenuItem property: %s", operation)
		}
	}
	if strings.HasPrefix(operation, "set:") {
		value, err := menuArg(args, 0, operation)
		if err != nil {
			return tjs.VoidValue, err
		}
		switch strings.TrimPrefix(operation, "set:") {
		case "caption":
			item.caption = tjs.StringValue(value.Text())
		case "checked":
			checked, err := menuBool(value)
			if err != nil {
				return tjs.VoidValue, err
			}
			if checked && item.radio {
				return tjs.VoidValue, s.menuCheckRadio(id, budget)
			}
			item.checked = checked
		case "enabled":
			item.enabled, err = menuBool(value)
		case "visible":
			item.visible, err = menuBool(value)
		case "radio":
			var radio bool
			radio, err = menuBool(value)
			if err == nil && radio != item.radio {
				item.radio = radio
				err = s.menuReconsiderRadio(id, budget)
			}
		case "group":
			var group int64
			group, err = value.Int()
			if err == nil {
				item.group = int32(group)
				err = s.menuReconsiderRadio(id, budget)
			}
		case "shortcut":
			item.shortcut = s.menuShortcut(value)
		case "index":
			var index int64
			index, err = value.Int()
			if err == nil {
				if s.menuIndex(id) != index {
					parent := s.menuData.items[item.displayParent]
					if parent == nil {
						return tjs.VoidValue, errors.New("cannot reorder a detached MenuItem")
					}
					count := len(parent.displayChildren)
					if index < 0 {
						index = 0
					}
					if index >= int64(count) {
						return tjs.VoidValue, errors.New("MenuItem index out of range")
					}
					parent.displayChildren = removeMenuID(parent.displayChildren, id)
					parent.displayChildren = insertMenuID(parent.displayChildren, int(index), id)
					if item.radio && item.checked {
						err = s.menuCheckRadio(id, budget)
					}
				}
			}
		default:
			return tjs.VoidValue, fmt.Errorf("unsupported MenuItem property: %s", operation)
		}
		return tjs.VoidValue, err
	}

	switch operation {
	case "finalize":
		return tjs.VoidValue, nil
	case "add", "insert":
		child, err := menuArg(args, 0, operation)
		if err != nil {
			return tjs.VoidValue, err
		}
		if child.Kind() != tjs.KindObject || child.IsNull() || child.ObjectRef().Object == 0 {
			return tjs.VoidValue, errors.New("MenuItem child must be a non-null object")
		}
		var index *int32
		if operation == "insert" {
			n, err := menuIntArg(args, 1, operation)
			if err != nil {
				return tjs.VoidValue, err
			}
			i := int32(n)
			index = &i
		}
		return tjs.VoidValue, s.menuAttach(id, child.ObjectRef().Object, index, budget)
	case "remove":
		child, err := menuArg(args, 0, operation)
		if err != nil {
			return tjs.VoidValue, err
		}
		if child.Kind() != tjs.KindObject || child.IsNull() || child.ObjectRef().Object == 0 {
			return tjs.VoidValue, errors.New("MenuItem child must be a non-null object")
		}
		childID := child.ObjectRef().Object
		if _, _, err := s.menuItem(child, operation); err != nil {
			return tjs.VoidValue, err
		}
		if !containsMenuID(item.displayChildren, childID) {
			return tjs.VoidValue, errors.New("MenuItem is not a child of this menu")
		}
		item.displayChildren = removeMenuID(item.displayChildren, childID)
		if containsMenuID(item.children, childID) {
			item.children = removeMenuID(item.children, childID)
			item.arrayDirty = true
			s.menuData.items[childID].parent = 0
		}
		s.menuData.items[childID].displayParent = 0
		return tjs.VoidValue, nil
	case "popup":
		model.lastPopup = &menuPopupRequest{item: id, arguments: append([]tjs.Value(nil), args...)}
		return tjs.VoidValue, nil
	case "onClick":
		return s.menuClick(vm, id, item, budget)
	case "fireClick":
		ancestor := id
		for ancestor != 0 {
			if err := menuCharge(budget); err != nil {
				return tjs.VoidValue, err
			}
			menu := model.items[ancestor]
			if menu == nil {
				return tjs.VoidValue, errors.New("MenuItem hierarchy is invalid")
			}
			if !menu.enabled {
				return tjs.VoidValue, nil
			}
			ancestor = menu.displayParent
		}
		root, err := s.menuRoot(id, budget)
		if err != nil {
			return tjs.VoidValue, err
		}
		if model.items[root].window.IsNull() || model.items[root].window.IsVoid() {
			return tjs.VoidValue, nil
		}
		handler, err := vm.GetMember(context, "onClick", s, budget)
		if err != nil {
			return tjs.VoidValue, err
		}
		if handler.IsVoid() {
			return tjs.VoidValue, nil
		}
		return vm.CallFunction(handler, context, nil, s, budget)
	default:
		return tjs.VoidValue, fmt.Errorf("unsupported MenuItem operation: %s", operation)
	}
}

func (s *Services) constructMenuItem(id tjs.ObjectID, args []tjs.Value) error {
	model := s.menuState()
	previous := model.items[id]
	if previous == nil {
		return errors.New("context has no MenuItem native instance")
	}
	owner, err := menuArg(args, 0, "MenuItem")
	if err != nil {
		return err
	}
	if owner.Kind() != tjs.KindObject || owner.IsNull() {
		return errors.New("MenuItem requires an object action owner")
	}
	var root tjs.Value
	if len(args) > 1 && args[1].Kind() == tjs.KindObject && !args[1].IsNull() {
		root = args[1]
	}
	windowID := tjs.ObjectID(0)
	if root.Kind() == tjs.KindObject && !root.IsNull() {
		windowID = root.ObjectRef().Object
	} else if model.installed {
		windowID = owner.ObjectRef().Object
	}
	if windowID != 0 {
		window := s.Window(windowID)
		if window == nil || !window.Constructed {
			return errors.New("MenuItem requires a constructed Window")
		}
	}
	caption := tjs.StringValue("")
	if root.IsVoid() && len(args) > 1 {
		caption = tjs.StringValue(args[1].Text())
	}
	if previous.displayParent != 0 {
		if parent := model.items[previous.displayParent]; parent != nil {
			parent.displayChildren = removeMenuID(parent.displayChildren, id)
		}
	}
	for _, childID := range previous.displayChildren {
		if child := model.items[childID]; child != nil && child.displayParent == id {
			child.displayParent = 0
		}
	}
	item := &menuItemState{
		constructed: true, actionOwner: owner, window: tjs.NullValue, caption: caption, enabled: true, visible: true,
		group: -1, parent: previous.parent, children: previous.children, array: previous.array,
		arrayDirty: previous.arrayDirty, displayChildren: nil,
	}
	if root.Kind() == tjs.KindObject && !root.IsNull() {
		item.window = menuItemValue(windowID)
	}
	model.items[id] = item
	return nil
}

func (s *Services) invalidateMenuItem(vm *tjs.VM, id tjs.ObjectID, budget *uint64) error {
	model := s.menuState()
	item := model.items[id]
	if item == nil {
		return nil
	}
	children := append([]tjs.ObjectID(nil), item.children...)
	for _, child := range item.displayChildren {
		if !containsMenuID(children, child) {
			children = append(children, child)
		}
	}
	for _, child := range children {
		if err := menuCharge(budget); err != nil {
			return err
		}
		if model.items[child] != nil {
			if _, err := vm.Invalidate(menuItemValue(child), s, budget); err != nil {
				return err
			}
		}
	}
	if parent := model.items[item.displayParent]; parent != nil {
		parent.displayChildren = removeMenuID(parent.displayChildren, id)
	}
	if parent := model.items[item.parent]; parent != nil {
		parent.children = removeMenuID(parent.children, id)
		parent.displayChildren = removeMenuID(parent.displayChildren, id)
		parent.arrayDirty = true
	}
	for _, childID := range item.children {
		if child := model.items[childID]; child != nil && child.parent == id {
			child.parent = 0
		}
	}
	for _, childID := range item.displayChildren {
		if child := model.items[childID]; child != nil && child.displayParent == id {
			child.displayParent = 0
		}
	}
	delete(model.items, id)
	for windowID, root := range model.roots {
		if root.Kind() == tjs.KindObject && !root.IsNull() && root.ObjectRef().Object == id {
			delete(model.roots, windowID)
		}
	}
	if model.lastPopup != nil && model.lastPopup.item == id {
		model.lastPopup = nil
	}
	return nil
}

func (s *Services) LastMenuPopup() (MenuPopupRequest, bool) {
	if s == nil || s.menuData.lastPopup == nil {
		return MenuPopupRequest{}, false
	}
	request := s.menuData.lastPopup
	return MenuPopupRequest{Item: request.item, Arguments: append([]tjs.Value(nil), request.arguments...)}, true
}

func (m *menuModel) gcRoots() []tjs.Value {
	roots := make([]tjs.Value, 0, 4)
	if m.textToKeycode.Kind() == tjs.KindObject && !m.textToKeycode.IsNull() {
		roots = append(roots, m.textToKeycode)
	}
	if m.keycodeToText.Kind() == tjs.KindObject && !m.keycodeToText.IsNull() {
		roots = append(roots, m.keycodeToText)
	}
	if m.lastPopup != nil {
		roots = append(roots, menuItemValue(m.lastPopup.item))
		roots = append(roots, m.lastPopup.arguments...)
	}
	return roots
}

func (m *menuModel) gcTrace(id tjs.ObjectID) []tjs.Value {
	var refs []tjs.Value
	if root, ok := m.roots[id]; ok {
		refs = append(refs, root)
	}
	item := m.items[id]
	if item == nil {
		return refs
	}
	refs = append(refs, item.actionOwner, item.window, item.caption)
	if item.parent != 0 {
		refs = append(refs, menuItemValue(item.parent))
	}
	if item.displayParent != 0 {
		refs = append(refs, menuItemValue(item.displayParent))
	}
	for _, child := range item.children {
		refs = append(refs, menuItemValue(child))
	}
	for _, child := range item.displayChildren {
		refs = append(refs, menuItemValue(child))
	}
	if item.array.Kind() == tjs.KindObject && !item.array.IsNull() {
		refs = append(refs, item.array)
	}
	return refs
}
func (m *menuModel) prune(vm *tjs.VM) {
	if vm == nil {
		return
	}
	invalid := make(map[tjs.ObjectID]struct{})
	for id := range m.items {
		if vm.IsValid(tjs.ObjectIDValue(id)).IntegerValue() == 0 {
			invalid[id] = struct{}{}
		}
	}
	if len(invalid) != 0 {
		for id := range invalid {
			delete(m.items, id)
		}
		for _, item := range m.items {
			children := item.children[:0]
			for _, child := range item.children {
				if _, dead := invalid[child]; !dead {
					children = append(children, child)
				}
			}
			if len(children) != len(item.children) {
				item.arrayDirty = true
			}
			item.children = children
			displayChildren := item.displayChildren[:0]
			for _, child := range item.displayChildren {
				if _, dead := invalid[child]; !dead {
					displayChildren = append(displayChildren, child)
				}
			}
			item.displayChildren = displayChildren
			if _, dead := invalid[item.parent]; dead {
				item.parent = 0
			}
			if _, dead := invalid[item.displayParent]; dead {
				item.displayParent = 0
			}
		}
	}
	for windowID, root := range m.roots {
		if vm.IsValid(root).IntegerValue() == 0 {
			delete(m.roots, windowID)
		}
	}
	if m.lastPopup != nil {
		if _, dead := invalid[m.lastPopup.item]; dead || vm.IsValid(menuItemValue(m.lastPopup.item)).IntegerValue() == 0 {
			m.lastPopup = nil
		}
	}
}
