package runtime

import (
	"fmt"

	"github.com/eslzzyl/krkrgo/internal/tjs"
)

type padState struct {
	properties map[string]tjs.Value
}

func newPadState() *padState {
	return &padState{properties: map[string]tjs.Value{
		"text": tjs.StringValue(""), "fileName": tjs.StringValue(""),
		"color": tjs.IntegerValue(0), "visible": tjs.IntegerValue(0),
		"title": tjs.StringValue(""), "fontColor": tjs.IntegerValue(0),
		"fontHeight": tjs.IntegerValue(10), "fontSize": tjs.IntegerValue(10),
		"fontBold": tjs.IntegerValue(0), "fontItalic": tjs.IntegerValue(0),
		"fontUnderline": tjs.IntegerValue(0), "fontStrikeOut": tjs.IntegerValue(0),
		"fontFace": tjs.StringValue(""), "readOnly": tjs.IntegerValue(1),
		"wordWrap": tjs.IntegerValue(0), "opacity": tjs.IntegerValue(255),
		"showStatusBar": tjs.IntegerValue(0), "showScrollBars": tjs.IntegerValue(0),
		"statusText": tjs.StringValue(""), "borderStyle": tjs.IntegerValue(0),
		"width": tjs.IntegerValue(100), "height": tjs.IntegerValue(100),
		"left": tjs.IntegerValue(0), "top": tjs.IntegerValue(0),
	}}
}

func installPadNative(vm *tjs.VM) error {
	if _, installed := vm.Globals["Pad"]; installed {
		return nil
	}
	class, err := vm.RegisterNativeClass("Pad")
	if err != nil {
		return err
	}
	for _, method := range []string{"@initialize", "@invalidate", "Pad", "finalize", "clear", "print", "setPos", "setSize", "show", "hide", "registerExEvent", "bringToFront"} {
		if err := vm.RegisterNativeMethod(class, method, "Pad."+method); err != nil {
			return err
		}
	}
	for _, property := range []string{"text", "fileName", "color", "visible", "title", "fontColor", "fontHeight", "fontSize", "fontBold", "fontItalic", "fontUnderline", "fontStrikeOut", "fontFace", "readOnly", "wordWrap", "opacity", "showStatusBar", "showScrollBars", "statusText", "borderStyle", "width", "height", "left", "top"} {
		if err := vm.RegisterNativeProperty(class, property, "Pad.get:"+property, "Pad.set:"+property); err != nil {
			return err
		}
	}
	return nil
}

func (s *Services) padNative(context tjs.Value, operation string, args []tjs.Value) (tjs.Value, error) {
	if context.Kind() != tjs.KindObject || context.IsNull() || context.ObjectRef().Object == 0 {
		return tjs.VoidValue, fmt.Errorf("Pad.%s requires non-null object context", operation)
	}
	id := context.ObjectRef().Object
	if s.Pads == nil {
		s.Pads = make(map[tjs.ObjectID]*padState)
	}
	state := s.Pads[id]
	if operation == "@initialize" || operation == "Pad" {
		if state == nil {
			state = newPadState()
			s.Pads[id] = state
		}
		return tjs.VoidValue, nil
	}
	if operation == "@invalidate" || operation == "finalize" {
		delete(s.Pads, id)
		return tjs.VoidValue, nil
	}
	if state == nil {
		return tjs.VoidValue, fmt.Errorf("Pad.%s context has no native instance", operation)
	}
	property := ""
	if len(operation) > 4 && operation[:4] == "get:" {
		property = operation[4:]
		if value, ok := state.properties[property]; ok {
			return value, nil
		}
		return tjs.VoidValue, nil
	}
	if len(operation) > 4 && operation[:4] == "set:" {
		property = operation[4:]
		if len(args) != 1 {
			return tjs.VoidValue, fmt.Errorf("Pad.%s expects one value", operation)
		}
		state.properties[property] = args[0]
		return tjs.VoidValue, nil
	}
	switch operation {
	case "clear":
		state.properties["text"] = tjs.StringValue("")
		return tjs.VoidValue, nil
	case "setPos", "setSize":
		keys := []string{"left", "top", "width", "height"}
		start, count := 0, 4
		if operation == "setSize" {
			start, count = 2, 2
		}
		if len(args) > count {
			return tjs.VoidValue, fmt.Errorf("Pad.%s accepts at most %d arguments", operation, count)
		}
		for i, value := range args {
			if !value.IsVoid() {
				state.properties[keys[start+i]] = value
			}
		}
		return tjs.VoidValue, nil
	case "show":
		state.properties["visible"] = tjs.IntegerValue(1)
		return tjs.VoidValue, nil
	case "hide":
		state.properties["visible"] = tjs.IntegerValue(0)
		return tjs.VoidValue, nil
	case "registerExEvent", "bringToFront":
		// The host-neutral Pad has no window manager to register with or raise.
		return tjs.VoidValue, nil
	case "print":
		// Pad is a host-neutral compatibility surface; text is retained for
		// scripts and rendering requests do not imply a native window exists.
		return tjs.VoidValue, nil
	default:
		return tjs.VoidValue, fmt.Errorf("unknown Pad operation %q", operation)
	}
}
