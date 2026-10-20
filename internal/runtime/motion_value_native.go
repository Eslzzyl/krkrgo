package runtime

import (
	"fmt"

	"github.com/eslzzyl/krkrgo/internal/tjs"
)

type motionValueObjectState struct {
	class         string
	values        map[string]tjs.Value
	cache         map[string]tjs.Value
	buffer        []byte
	managedLayers map[string]tjs.Value
}

func newMotionValueObjectState(class string) *motionValueObjectState {
	state := &motionValueObjectState{class: class, values: make(map[string]tjs.Value)}
	switch class {
	case "Point":
		state.values["type"] = tjs.IntegerValue(0)
		state.values["x"] = tjs.RealValue(0)
		state.values["y"] = tjs.RealValue(0)
	case "Circle":
		state.values["type"] = tjs.IntegerValue(1)
		state.values["x"] = tjs.RealValue(0)
		state.values["y"] = tjs.RealValue(0)
		state.values["r"] = tjs.RealValue(0)
	case "Rect":
		state.values["type"] = tjs.IntegerValue(2)
		state.values["l"] = tjs.RealValue(0)
		state.values["t"] = tjs.RealValue(0)
		state.values["w"] = tjs.RealValue(0)
		state.values["h"] = tjs.RealValue(0)
	case "Quad":
		state.values["type"] = tjs.IntegerValue(3)
		state.values["p"] = tjs.VoidValue
	case "LayerGetter":
		state.values["type"] = tjs.IntegerValue(0)
		state.values["label"] = tjs.StringValue("")
		state.values["visible"] = tjs.IntegerValue(1)
		state.values["branchVisible"] = tjs.IntegerValue(1)
		state.values["layerVisible"] = tjs.IntegerValue(1)
		for _, name := range []string{"x", "y", "left", "top", "angleDeg", "angleRad", "slantX", "slantY", "originX", "originY"} {
			state.values[name] = tjs.RealValue(0)
		}
		for _, name := range []string{"flipX", "flipY"} {
			state.values[name] = tjs.IntegerValue(0)
		}
		for _, name := range []string{"zoomX", "zoomY"} {
			state.values[name] = tjs.RealValue(1)
		}
		state.values["opacity"] = tjs.IntegerValue(255)
		for _, name := range []string{"mtx", "vtx", "color", "bezierPatch", "shape", "motion", "particle"} {
			state.values[name] = tjs.VoidValue
		}
	case "SourceCache":
		state.values["bufLayer"] = tjs.VoidValue
		state.cache = make(map[string]tjs.Value)
	case "D3DAdaptor":
		state.values["visible"] = tjs.IntegerValue(0)
		state.values["alphaOpAdd"] = tjs.IntegerValue(0)
		state.values["canvasCaptureEnabled"] = tjs.IntegerValue(0)
		state.values["clearEnabled"] = tjs.IntegerValue(1)
		for _, name := range []string{"width", "height", "centerX", "centerY", "clearColor", "resizable"} {
			state.values[name] = tjs.IntegerValue(0)
		}
		state.values["window"] = tjs.VoidValue
	case "SeparateLayerAdaptor":
		state.values["absolute"] = tjs.IntegerValue(0)
		state.values["targetLayer"] = tjs.VoidValue
		state.values["privateRenderTarget"] = tjs.VoidValue
		state.managedLayers = make(map[string]tjs.Value)
	case "ResourceManager":
		state.values["cacheSize"] = tjs.IntegerValue(20 * 1024 * 1024)
	}
	return state
}

func (s *Services) motionValueObjectNative(vm *tjs.VM, context tjs.Value, class, operation string, args []tjs.Value) (tjs.Value, error) {
	s.ensureInit()
	id := context.ObjectRef().Object
	if operation == "invalidate" {
		s.mu.Lock()
		delete(s.MotionValueObjects, id)
		s.mu.Unlock()
		return tjs.VoidValue, nil
	}
	if operation == "construct" {
		state := newMotionValueObjectState(class)
		if class == "D3DAdaptor" {
			if len(args) < 5 {
				return tjs.VoidValue, fmt.Errorf("Motion.D3DAdaptor requires a Window and four integer arguments")
			}
			if args[0].Kind() != tjs.KindObject || args[0].IsNull() {
				return tjs.VoidValue, fmt.Errorf("Motion.D3DAdaptor requires a Window object")
			}
			if windowClass, found := vm.Globals["Window"]; found && windowClass.Kind() == tjs.KindObject && !windowClass.IsNull() {
				if classObject, exists := vm.Objects.Lookup(windowClass.ObjectRef().Object); exists && !tjs.InstanceOf(args[0], classObject) {
					return tjs.VoidValue, fmt.Errorf("Motion.D3DAdaptor requires a Window object")
				}
			}
			state.values["window"] = args[0]
			for index, property := range []string{"width", "height", "centerX", "centerY"} {
				value, err := args[index+1].Int()
				if err != nil {
					return tjs.VoidValue, err
				}
				state.values[property] = tjs.IntegerValue(value)
			}
			if err := motionD3DAdaptorResize(state); err != nil {
				return tjs.VoidValue, err
			}
		}
		if class == "SourceCache" {
			if len(args) != 2 {
				return tjs.VoidValue, fmt.Errorf("Motion.SourceCache requires owner and layerType")
			}
			layerType, err := args[1].Int()
			if err != nil {
				return tjs.VoidValue, err
			}
			state.values["owner"] = args[0]
			state.values["layerType"] = tjs.IntegerValue(layerType)
			bufLayer, err := s.motionSourceCacheBufLayer(vm, args[0])
			if err != nil {
				return tjs.VoidValue, err
			}
			state.values["bufLayer"] = bufLayer
		}
		s.mu.Lock()
		s.MotionValueObjects[id] = state
		s.mu.Unlock()
		return tjs.VoidValue, nil
	}
	s.mu.RLock()
	state := s.MotionValueObjects[id]
	s.mu.RUnlock()
	if state == nil || state.class != class {
		return tjs.VoidValue, fmt.Errorf("Motion.%s instance state is unavailable", class)
	}
	if class == "SourceCache" {
		switch operation {
		case "clearCache":
			clear(state.cache)
			return tjs.VoidValue, nil
		case "loadSource":
			if len(args) < 2 {
				return tjs.VoidValue, fmt.Errorf("Motion.SourceCache.loadSource requires keyOrSource and currentSource")
			}
			key := ""
			key = s.motionSourceCacheKey(vm, args[0])
			if key == "" && len(args) > 1 {
				key = s.motionSourceCacheKey(vm, args[1])
			}
			if key == "" {
				return tjs.VoidValue, nil
			}
			if value, found := state.cache[key]; found {
				return value, nil
			}
			if len(args) > 1 && args[1].Kind() != tjs.KindVoid {
				state.cache[key] = args[1]
				return args[1], nil
			}
			return tjs.VoidValue, nil
		}
	}
	if class == "D3DAdaptor" {
		if len(operation) > 4 && operation[:4] == "set:" {
			property := operation[4:]
			if _, exists := state.values[property]; !exists {
				return tjs.VoidValue, fmt.Errorf("unsupported Motion.D3DAdaptor property %q", property)
			}
			if len(args) == 0 {
				return tjs.VoidValue, fmt.Errorf("Motion.D3DAdaptor.%s requires a value", property)
			}
			if property == "visible" || property == "alphaOpAdd" || property == "canvasCaptureEnabled" || property == "clearEnabled" {
				state.values[property] = tjs.IntegerValue(int64(boolInt(motionPlayerBool(args[0]))))
			} else {
				state.values[property] = args[0]
			}
			return tjs.VoidValue, nil
		}
		switch operation {
		case "setPos", "removeAllTextures", "removeAllBg", "removeAllCaption", "registerBg", "registerCaption", "unloadUnusedTextures":
			return tjs.VoidValue, nil
		case "setSize":
			if len(args) < 2 {
				return tjs.VoidValue, fmt.Errorf("Motion.D3DAdaptor.setSize requires width and height")
			}
			oldWidth, _ := state.values["width"].Int()
			oldHeight, _ := state.values["height"].Int()
			var width, height int64
			for index := 0; index < 2; index++ {
				value, err := args[index].Int()
				if err != nil {
					return tjs.VoidValue, err
				}
				if index == 0 {
					width = value
				} else {
					height = value
				}
			}
			if width == oldWidth && height == oldHeight {
				return tjs.VoidValue, nil
			}
			state.values["width"] = tjs.IntegerValue(width)
			state.values["height"] = tjs.IntegerValue(height)
			if err := motionD3DAdaptorResize(state); err != nil {
				return tjs.VoidValue, err
			}
			return tjs.VoidValue, nil
		case "setClearColor":
			if len(args) == 0 {
				return tjs.VoidValue, fmt.Errorf("Motion.D3DAdaptor.setClearColor requires a color")
			}
			state.values["clearColor"] = args[0]
			return tjs.VoidValue, nil
		case "setResizable":
			if len(args) == 0 {
				return tjs.VoidValue, fmt.Errorf("Motion.D3DAdaptor.setResizable requires a flag")
			}
			state.values["resizable"] = tjs.IntegerValue(int64(boolInt(motionPlayerBool(args[0]))))
			return tjs.VoidValue, nil
		case "captureCanvas":
			if len(args) == 0 {
				return tjs.VoidValue, fmt.Errorf("Motion.D3DAdaptor.captureCanvas requires a layer")
			}
			layer, err := s.motionLayerFromValue(vm, args[0])
			if err != nil {
				return tjs.VoidValue, err
			}
			width, _ := state.values["width"].Int()
			height, _ := state.values["height"].Int()
			if width <= 0 || height <= 0 {
				return tjs.VoidValue, nil
			}
			if err := layer.SetRGBA(int32(width), int32(height), state.buffer); err != nil {
				return tjs.VoidValue, err
			}
			return args[0], nil
		}
	}
	if len(operation) > 4 && operation[:4] == "get:" {
		property := operation[4:]
		value, found := state.values[property]
		if !found {
			return tjs.VoidValue, fmt.Errorf("unsupported Motion.%s property %q", class, property)
		}
		return value, nil
	}
	if operation != "contains" {
		return tjs.VoidValue, fmt.Errorf("unsupported Motion.%s operation %q", class, operation)
	}
	if len(args) < 2 {
		return tjs.VoidValue, fmt.Errorf("Motion.%s.contains requires x and y", class)
	}
	x, err := args[0].Real()
	if err != nil {
		return tjs.VoidValue, err
	}
	y, err := args[1].Real()
	if err != nil {
		return tjs.VoidValue, err
	}
	inside := false
	switch class {
	case "Circle":
		cx, _ := state.values["x"].Real()
		cy, _ := state.values["y"].Real()
		radius, _ := state.values["r"].Real()
		dx, dy := x-cx, y-cy
		inside = dx*dx+dy*dy <= radius*radius
	case "Rect":
		left, _ := state.values["l"].Real()
		top, _ := state.values["t"].Real()
		width, _ := state.values["w"].Real()
		height, _ := state.values["h"].Real()
		inside = x >= left && x < left+width && y >= top && y < top+height
	case "Point", "Quad":
		inside = false
	default:
		return tjs.VoidValue, fmt.Errorf("Motion.%s.contains is unavailable", class)
	}
	return tjs.IntegerValue(int64(boolInt(inside))), nil
}

func (s *Services) motionSourceCacheBufLayer(vm *tjs.VM, owner tjs.Value) (tjs.Value, error) {
	if owner.Kind() != tjs.KindObject || owner.IsNull() {
		return tjs.VoidValue, nil
	}
	parent := tjs.VoidValue
	if primary, found, err := vm.GetMemberIfPresent(owner, "primaryLayer", s, nil); err == nil && found && primary.Kind() == tjs.KindObject && !primary.IsNull() {
		if _, _, layerErr := s.layerFromValue(primary); layerErr == nil {
			parent = primary
		}
	}
	if parent.Kind() == tjs.KindVoid {
		if _, _, layerErr := s.layerFromValue(owner); layerErr == nil {
			parent = owner
		}
	}
	layerClass, found := vm.Globals["Layer"]
	if !found {
		return tjs.VoidValue, nil
	}
	layer, err := vm.Construct(layerClass, []tjs.Value{owner, parent}, s, nil)
	if err != nil {
		return tjs.VoidValue, err
	}
	if layer.Kind() != tjs.KindObject || layer.IsNull() {
		return tjs.VoidValue, nil
	}
	if nativeLayer, _, err := s.layerFromValue(layer); err == nil {
		s.mu.Lock()
		nativeLayer.Type = LayerAlpha
		nativeLayer.Visible = false
		nativeLayer.SetAbsoluteOrderMode(false)
		s.mu.Unlock()
	}
	return layer, nil
}

func motionD3DAdaptorResize(state *motionValueObjectState) error {
	width, _ := state.values["width"].Int()
	height, _ := state.values["height"].Int()
	if width <= 0 || height <= 0 {
		state.buffer = nil
		return nil
	}
	if uint64(width)*uint64(height) > uint64(^uint(0)>>1)/4 {
		return fmt.Errorf("Motion.D3DAdaptor dimensions exceed buffer limits")
	}
	state.buffer = make([]byte, int(width*height*4))
	return nil
}

func (s *Services) motionSourceCacheKey(vm *tjs.VM, value tjs.Value) string {
	if value.Kind() == tjs.KindString {
		return value.Text()
	}
	if value.Kind() != tjs.KindObject || value.IsNull() {
		if value.Kind() == tjs.KindVoid || value.IsNull() {
			return ""
		}
		return value.Text()
	}
	for _, property := range []string{"src", "key"} {
		member, found, err := vm.GetMemberIfPresent(value, property, s, nil)
		if err == nil && found && member.Kind() != tjs.KindVoid && member.Text() != "" {
			return member.Text()
		}
	}
	return ""
}
