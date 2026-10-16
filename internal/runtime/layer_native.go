package runtime

import (
	"fmt"
	"image/color"
	"math"
	"strings"

	"github.com/eslzzyl/krkrgo/internal/tjs"
)

const nativeLayerDefaultSize int32 = 32

type layerColor struct {
	r, g, b, a uint8
}

func nativeLayerDefaults(id tjs.ObjectID) *Layer {
	l := NewLayer(LayerID(id))
	l.Bounds = Rect{Width: nativeLayerDefaultSize, Height: nativeLayerDefaultSize}
	l.Visible = false
	l.HitThreshold = 16
	l.RGBA = make([]byte, nativeLayerDefaultSize*nativeLayerDefaultSize*4)
	fillLayerNeutral(l.RGBA, l.Type)
	l.ImageWidth, l.ImageHeight = nativeLayerDefaultSize, nativeLayerDefaultSize
	_ = l.SetClip(Rect{Width: nativeLayerDefaultSize, Height: nativeLayerDefaultSize})
	return l
}

func layerColorValue(value tjs.Value) (layerColor, error) {
	n, err := value.Int()
	if err != nil {
		return layerColor{}, err
	}
	return layerColor{r: uint8(uint64(n) >> 16), g: uint8(uint64(n) >> 8), b: uint8(n), a: uint8(uint64(n) >> 24)}, nil
}

func (s *Services) layerForContext(context tjs.Value, operation string) (*Layer, tjs.ObjectID, error) {
	if context.Kind() != tjs.KindObject || context.IsNull() {
		return nil, 0, fmt.Errorf("Layer.%s requires non-null object context", operation)
	}
	ref := context.ObjectRef()
	id := ref.Object
	if ref.HasBoundContext() {
		id = ref.Context
	}
	s.ensureInit()
	s.mu.RLock()
	l := s.Layers[id]
	s.mu.RUnlock()
	if l == nil {
		return nil, id, fmt.Errorf("Layer.%s: context has no Layer native instance (object %d, bound context %d)", operation, id, context.ObjectRef().Context)
	}
	return l, id, nil
}

func (s *Services) layerFromValue(value tjs.Value) (*Layer, tjs.ObjectID, error) {
	if value.Kind() != tjs.KindObject || value.IsNull() {
		return nil, 0, fmt.Errorf("expected a non-null Layer object")
	}
	id := value.ObjectRef().Object
	s.mu.RLock()
	l := s.Layers[id]
	s.mu.RUnlock()
	if l == nil {
		return nil, id, fmt.Errorf("object %d is not a Layer", id)
	}
	return l, id, nil
}

func (s *Services) Layer(id tjs.ObjectID) *Layer {
	if s == nil {
		return nil
	}
	s.ensureInit()
	s.mu.RLock()
	l := s.Layers[id]
	s.mu.RUnlock()
	return l
}

// layerOwnerAndParentLocked resolves the optional Window and Layer constructor
// arguments. The caller must hold s.mu.
func (s *Services) layerOwnerAndParentLocked(args []tjs.Value) (*Layer, tjs.ObjectID, error) {
	var parent *Layer
	var ownerID tjs.ObjectID
	if len(args) > 0 && args[0].Kind() == tjs.KindObject && !args[0].IsNull() {
		argID := args[0].ObjectRef().Object
		parent = s.Layers[argID]
		if parent == nil && s.WindowObjects[argID] != nil {
			ownerID = argID
		}
	}
	if len(args) > 1 && args[1].Kind() == tjs.KindObject && !args[1].IsNull() {
		parent = s.Layers[args[1].ObjectRef().Object]
		if parent == nil {
			return nil, 0, fmt.Errorf("object %d is not a Layer", args[1].ObjectRef().Object)
		}
	}
	if parent != nil && ownerID == 0 {
		ownerID = tjs.ObjectID(parent.windowID)
	}
	return parent, ownerID, nil
}

func (s *Services) queueLayerPaint(layer *Layer) {
	if layer == nil || layer.callOnPaint {
		return
	}
	layer.callOnPaint = true
	s.pendingPaintIDs = append(s.pendingPaintIDs, tjs.ObjectID(layer.ID))
}

func (s *Services) layerNative(vm *tjs.VM, context tjs.Value, operation string, args []tjs.Value, budget *uint64) (tjs.Value, error) {
	s.ensureInit()
	if context.Kind() != tjs.KindObject || context.IsNull() {
		return tjs.VoidValue, fmt.Errorf("Layer.%s requires non-null object context", operation)
	}
	id := context.ObjectRef().Object
	if operation == "@initialize" || operation == "Layer" {
		s.mu.Lock()
		if l := s.Layers[id]; l != nil {
			// Native base initialization runs before a script-derived Layer's
			// constructor. Its @initialize call has no arguments; honor the
			// later explicit Layer(window, parent) call rather than treating it
			// as an already-constructed instance.
			if operation == "Layer" && l.windowID == 0 && l.Parent == nil && len(args) > 0 {
				parent, ownerID, err := s.layerOwnerAndParentLocked(args)
				if err != nil {
					s.mu.Unlock()
					return tjs.VoidValue, err
				}
				if parent != nil {
					if err := parent.AddChild(l); err != nil {
						s.mu.Unlock()
						return tjs.VoidValue, err
					}
				}
				l.windowID = uint64(ownerID)
				if parent == nil && ownerID != 0 {
					if window := s.WindowObjects[ownerID]; window != nil && window.primaryLayer == 0 {
						window.primaryLayer = LayerID(id)
					}
				}
				s.mu.Unlock()
				if parent != nil {
					if err := s.syncLayerChildren(vm); err != nil {
						s.mu.Lock()
						_ = parent.RemoveChild(l)
						l.windowID = 0
						s.mu.Unlock()
						return tjs.VoidValue, err
					}
				}
				return tjs.VoidValue, nil
			}
			s.mu.Unlock()
			return tjs.VoidValue, nil
		}
		if len(s.Layers) >= 10_000 {
			s.mu.Unlock()
			return tjs.VoidValue, fmt.Errorf("Layer count limit exceeded")
		}
		l := nativeLayerDefaults(id)
		parent, ownerID, err := s.layerOwnerAndParentLocked(args)
		if err != nil {
			s.mu.Unlock()
			return tjs.VoidValue, err
		}
		l.windowID = uint64(ownerID)
		s.Layers[id] = l
		if parent == nil && s.Root == nil {
			l.Visible = true
			l.Type = LayerOpaque
			l.HitThreshold = 0
			if err := s.attachRoot(l); err != nil {
				delete(s.Layers, id)
				s.mu.Unlock()
				return tjs.VoidValue, err
			}
		} else if parent != nil {
			if err := parent.AddChild(l); err != nil {
				delete(s.Layers, id)
				s.mu.Unlock()
				return tjs.VoidValue, err
			}
		}
		if ownerID != 0 {
			if window := s.WindowObjects[ownerID]; window != nil && window.primaryLayer == 0 {
				window.primaryLayer = LayerID(id)
				if s.Root == nil || s.Root.windowID == 0 || s.Root == parent {
					l.Visible = true
					l.Type = LayerOpaque
					if err := s.attachRoot(l); err != nil {
						s.mu.Unlock()
						return tjs.VoidValue, err
					}
				}
			}
		}
		s.mu.Unlock()
		if parent != nil {
			if err := s.syncLayerChildren(vm); err != nil {
				s.mu.Lock()
				_ = parent.RemoveChild(l)
				delete(s.Layers, id)
				s.mu.Unlock()
				return tjs.VoidValue, err
			}
		}
		return tjs.VoidValue, nil
	}
	if operation == "@invalidate" || operation == "finalize" {
		return tjs.VoidValue, s.invalidateLayer(vm, id)
	}
	l, _, err := s.layerForContext(context, operation)
	if err != nil {
		if vm != nil {
			if object, ok := vm.Objects.Lookup(id); ok {
				return tjs.VoidValue, fmt.Errorf("%w (TJS category %d)", err, object.Category)
			}
		}
		return tjs.VoidValue, err
	}
	arg := func(i int) (tjs.Value, error) {
		if i < 0 || i >= len(args) {
			return tjs.VoidValue, fmt.Errorf("Layer.%s: missing argument %d", operation, i)
		}
		return args[i], nil
	}
	intArg := func(i int) (int32, error) {
		v, e := arg(i)
		if e != nil {
			return 0, e
		}
		n, e := v.Int()
		if e != nil || n < math.MinInt32 || n > math.MaxInt32 {
			if e != nil {
				return 0, e
			}
			return 0, fmt.Errorf("Layer.%s: integer argument exceeds int32", operation)
		}
		return int32(n), nil
	}
	boolArg := func(i int) (bool, error) {
		v, e := arg(i)
		if e != nil {
			return false, e
		}
		return v.Truth()
	}
	if strings.HasPrefix(operation, "get:") {
		return s.layerGet(vm, l, id, strings.TrimPrefix(operation, "get:"), budget)
	}
	if strings.HasPrefix(operation, "set:") {
		property := strings.TrimPrefix(operation, "set:")
		result, setErr := s.layerSet(vm, l, id, property, arg, intArg, boolArg, budget)
		s.syncRootGeometry()
		if setErr == nil && (property == "order" || property == "absolute") {
			setErr = s.syncLayerChildren(vm)
		}
		return result, setErr
	}
	defer s.syncRootGeometry()
	switch operation {
	case "openMovie", "startMovie", "stopMovie", "isPlayingMovie":
		return tjs.VoidValue, UnsupportedDevice("layerExMovie video decoding and presentation")
	case "beginTransition", "stopTransition":
		return s.layerTransition(vm, context, operation, args, budget)
	case "update":
		if len(args) != 0 {
			if len(args) < 4 {
				return tjs.VoidValue, fmt.Errorf("Layer.update requires 0 or at least 4 arguments")
			}
			for i := 0; i < 4; i++ {
				if _, err := intArg(i); err != nil {
					return tjs.VoidValue, err
				}
			}
		}
		// UpdateByScript queues onPaint until the next paint pass. A headless
		// Session dispatches that callback when it captures the next frame.
		s.queueLayerPaint(l)
		return tjs.VoidValue, nil
	case "onHitTest":
		if len(args) < 3 {
			return tjs.VoidValue, fmt.Errorf("Layer.onHitTest requires x, y, and hit")
		}
		hit, err := boolArg(2)
		if err != nil {
			return tjs.VoidValue, err
		}
		if window := layerWindowState(s, l); window != nil {
			window.hitTestWork = hit
		} else {
			l.hitTestWork = hit
		}
		return tjs.VoidValue, nil
	case "onMouseMove", "onMouseDown", "onMouseUp", "onClick", "onDoubleClick", "onMouseEnter", "onMouseLeave", "onMouseWheel", "onKeyDown", "onKeyUp", "onKeyPress", "onFocus", "onBlur", "onBeforeFocus", "onSearchPrevFocusable", "onSearchNextFocusable", "onPaint", "onTransitionCompleted", "onNodeEnabled", "onNodeDisabled", "onTouchDown", "onTouchUp", "onTouchMove", "onTouchScaling", "onTouchRotate", "onMultiTouch":
		return tjs.VoidValue, nil
	case "setPos":
		x, err := intArg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		y, err := intArg(1)
		if err != nil {
			return tjs.VoidValue, err
		}
		var width, height int32
		hasSize := len(args) > 2
		if hasSize {
			width, err = intArg(2)
			if err != nil {
				return tjs.VoidValue, err
			}
			height, err = intArg(3)
			if err != nil {
				return tjs.VoidValue, err
			}
		}
		s.mu.Lock()
		if hasSize {
			err = l.SetSize(width, height)
		}
		if err == nil {
			l.SetPosition(x, y)
		}
		s.mu.Unlock()
		return tjs.VoidValue, err
	case "setSize":
		width, err := intArg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		height, err := intArg(1)
		if err != nil {
			return tjs.VoidValue, err
		}
		s.mu.Lock()
		err = l.SetSize(width, height)
		s.mu.Unlock()
		return tjs.VoidValue, err
	case "setSizeToImageSize":
		s.mu.Lock()
		err := l.SetSizeToImageSize()
		s.mu.Unlock()
		return tjs.VoidValue, err
	case "setImageSize":
		width, err := intArg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		height, err := intArg(1)
		if err != nil {
			return tjs.VoidValue, err
		}
		s.mu.Lock()
		err = l.SetImageSize(width, height)
		if err == nil {
			l.imageModified = true
		}
		s.mu.Unlock()
		return tjs.VoidValue, err
	case "setImagePos":
		x, err := intArg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		y, err := intArg(1)
		if err != nil {
			return tjs.VoidValue, err
		}
		s.mu.Lock()
		err = l.SetImagePosition(x, y)
		s.mu.Unlock()
		return tjs.VoidValue, err
	case "setClip":
		if len(args) == 0 {
			s.mu.Lock()
			resetNativeLayerClip(l)
			s.mu.Unlock()
			return tjs.VoidValue, nil
		}
		x, err := intArg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		y, err := intArg(1)
		if err != nil {
			return tjs.VoidValue, err
		}
		width, err := intArg(2)
		if err != nil {
			return tjs.VoidValue, err
		}
		height, err := intArg(3)
		if err != nil {
			return tjs.VoidValue, err
		}
		s.mu.Lock()
		err = l.SetClip(Rect{X: x, Y: y, Width: width, Height: height})
		s.mu.Unlock()
		return tjs.VoidValue, err
	case "resetClip":
		s.mu.Lock()
		resetNativeLayerClip(l)
		s.mu.Unlock()
		return tjs.VoidValue, nil
	case "loadImages":
		return s.layerLoadImages(vm, l, id, args)
	case "loadProvinceImage":
		return tjs.VoidValue, s.layerLoadProvinceImage(l, id, args)
	case "saveLayerImage":
		return tjs.VoidValue, s.layerSaveImage(l, id, args, budget)
	case "light", "copyRect", "operateRect", "stretchCopy", "assignImages", "doBoxBlur", "adjustGamma", "doGrayScale":
		err := s.layerImageNative(l, operation, args)
		if err == nil {
			s.mu.Lock()
			l.imageModified = true
			l.markImageDirty()
			s.mu.Unlock()
		}
		return tjs.VoidValue, err
	case "affineCopy":
		err := s.layerAffineCopy(l, args)
		if err == nil {
			l.markImageDirty()
		}
		return tjs.VoidValue, err
	case "fillRect", "colorRect":
		s.mu.Lock()
		err := s.layerFillRect(l, operation, args, intArg)
		if err == nil {
			l.markImageDirty()
		}
		s.mu.Unlock()
		return tjs.VoidValue, err
	case "piledCopy", "pileRect":
		err := s.layerPiledCopy(l, id, args, budget, operation)
		if err == nil {
			l.markImageDirty()
		}
		return tjs.VoidValue, err
	case "bringToFront":
		s.mu.Lock()
		err := l.SetOrderIndex(math.MaxInt32)
		s.mu.Unlock()
		if err != nil {
			return tjs.VoidValue, err
		}
		return tjs.VoidValue, s.syncLayerChildren(vm)
	case "drawText":
		err := s.layerDrawText(l, args, budget)
		if err == nil {
			l.markImageDirty()
		}
		return tjs.VoidValue, err
	case "move":
		if len(args) == 1 {
			index, err := intArg(0)
			if err != nil {
				return tjs.VoidValue, err
			}
			s.mu.Lock()
			parent := l.Parent
			if parent == nil {
				s.mu.Unlock()
				return tjs.VoidValue, ErrLayerTree
			}
			err = parent.MoveChild(l, int(index))
			s.mu.Unlock()
			if err != nil {
				return tjs.VoidValue, err
			}
			return tjs.VoidValue, s.syncLayerChildren(vm)
		}
		if len(args) < 2 {
			return tjs.VoidValue, fmt.Errorf("Layer.move: requires index or x,y")
		}
		x, err := intArg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		y, err := intArg(1)
		if err != nil {
			return tjs.VoidValue, err
		}
		var width, height int32
		hasSize := len(args) > 2
		if hasSize {
			width, err = intArg(2)
			if err != nil {
				return tjs.VoidValue, err
			}
			height, err = intArg(3)
			if err != nil {
				return tjs.VoidValue, err
			}
		}
		s.mu.Lock()
		l.SetPosition(x, y)
		if hasSize {
			err = l.SetSize(width, height)
		}
		s.mu.Unlock()
		return tjs.VoidValue, err
	case "addChild", "removeChild":
		child, _, err := func() (*Layer, tjs.ObjectID, error) {
			if len(args) == 0 {
				return nil, 0, fmt.Errorf("Layer.%s: missing argument 0", operation)
			}
			return s.layerFromValue(args[0])
		}()
		if err != nil {
			return tjs.VoidValue, err
		}
		s.mu.Lock()
		if operation == "addChild" {
			err = l.AddChild(child)
		} else {
			err = l.RemoveChild(child)
		}
		s.mu.Unlock()
		if err != nil {
			return tjs.VoidValue, err
		}
		return tjs.VoidValue, s.syncLayerChildren(vm)
	case "getLayerAt":
		x, err := intArg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		y, err := intArg(1)
		if err != nil {
			return tjs.VoidValue, err
		}
		hit, err := s.hitTestLayer(vm, l, x, y, budget)
		if err != nil {
			return tjs.VoidValue, err
		}
		return layerValue(hit), nil
	case "getMainPixel", "getMaskPixel":
		x, err := intArg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		y, err := intArg(1)
		if err != nil {
			return tjs.VoidValue, err
		}
		s.mu.RLock()
		p, err := layerPixel(l, x, y)
		if err != nil {
			s.mu.RUnlock()
			return tjs.VoidValue, err
		}
		var value int64
		if operation == "getMaskPixel" {
			value = int64(p[3])
		} else {
			value = int64(p[0])<<16 | int64(p[1])<<8 | int64(p[2])
		}
		s.mu.RUnlock()
		return tjs.IntegerValue(value), nil
	case "setMainPixel", "setMaskPixel":
		x, err := intArg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		y, err := intArg(1)
		if err != nil {
			return tjs.VoidValue, err
		}
		value, err := intArg(2)
		if err != nil {
			return tjs.VoidValue, err
		}
		s.mu.Lock()
		p, err := layerPixel(l, x, y)
		if err == nil {
			if operation == "setMaskPixel" {
				p[3] = uint8(value)
			} else {
				p[0], p[1], p[2] = uint8(uint64(value)>>16), uint8(uint64(value)>>8), uint8(value)
			}
			l.imageModified = true
			l.markImageDirty()
		}
		s.mu.Unlock()
		return tjs.VoidValue, err
	case "getProvincePixel":
		x, err := intArg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		y, err := intArg(1)
		if err != nil {
			return tjs.VoidValue, err
		}
		s.mu.RLock()
		var value byte
		if validLayerImageSize(l.ProvinceWidth, l.ProvinceHeight, len(l.Province), 1) &&
			x >= 0 && y >= 0 && x < l.ProvinceWidth && y < l.ProvinceHeight {
			value = l.Province[int(y)*int(l.ProvinceWidth)+int(x)]
		}
		s.mu.RUnlock()
		return tjs.IntegerValue(int64(value)), nil
	case "setProvincePixel":
		x, err := intArg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		y, err := intArg(1)
		if err != nil {
			return tjs.VoidValue, err
		}
		value, err := intArg(2)
		if err != nil {
			return tjs.VoidValue, err
		}
		s.mu.Lock()
		if len(l.Province) == 0 {
			width, height := l.ImageWidth, l.ImageHeight
			if len(l.RGBA) == 0 {
				width, height = l.Bounds.Width, l.Bounds.Height
			}
			if width <= 0 || height <= 0 || uint64(width)*uint64(height) > maxLayerPixels {
				s.mu.Unlock()
				return tjs.VoidValue, fmt.Errorf("%w: cannot allocate province image", ErrInvalidLayer)
			}
			l.Province = make([]byte, int(uint64(width)*uint64(height)))
			l.ProvinceWidth, l.ProvinceHeight = width, height
		}
		if !validLayerImageSize(l.ProvinceWidth, l.ProvinceHeight, len(l.Province), 1) {
			s.mu.Unlock()
			return tjs.VoidValue, ErrProvinceSizeMismatch
		}
		clip, clipSet := l.Clip()
		if x >= 0 && y >= 0 && x < l.ProvinceWidth && y < l.ProvinceHeight &&
			(!clipSet || clip.Contains(x, y)) {
			l.Province[int(y)*int(l.ProvinceWidth)+int(x)] = uint8(value)
			l.imageModified = true
			l.markImageDirty()
		}
		s.mu.Unlock()
		return tjs.VoidValue, nil
	case "setMode":
		return tjs.VoidValue, s.layerSetMode(vm, l, budget)
	case "removeMode":
		return tjs.VoidValue, s.layerRemoveMode(vm, l, budget)
	case "focus":
		forward := true
		if len(args) > 0 {
			var e error
			forward, e = args[0].Truth()
			if e != nil {
				return tjs.VoidValue, e
			}
		}
		changed, e := s.layerSetFocus(vm, layerWindowState(s, l), l, forward, budget)
		if e != nil {
			return tjs.VoidValue, e
		}
		return tjs.IntegerValue(boolInt(changed)), nil
	default:
		return tjs.VoidValue, fmt.Errorf("%w: Layer.%s", ErrUnsupportedNative, operation)
	}
}

func (s *Services) attachRoot(l *Layer) error {
	if l == nil {
		return ErrInvalidLayer
	}
	s.Root = l
	s.Compositor = NewCompositor(l.Bounds.Width, l.Bounds.Height)
	s.Compositor.SetRoot(l)
	return nil
}

func (s *Services) syncRootGeometry() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Root == nil || s.Compositor == nil {
		return
	}
	s.Compositor.Width = s.Root.Bounds.Width
	s.Compositor.Height = s.Root.Bounds.Height
	s.Compositor.SetRoot(s.Root)
}

func (s *Services) syncLayerChildren(vm *tjs.VM) error {
	if s == nil || vm == nil {
		if vm == nil && s != nil {
			return fmt.Errorf("Layer.children synchronization requires VM")
		}
		return nil
	}
	type syncEntry struct {
		array    tjs.Value
		children []tjs.Value
	}
	s.mu.RLock()
	entries := make([]syncEntry, 0, len(s.LayerChildren))
	for id, array := range s.LayerChildren {
		entry := syncEntry{array: array}
		if parent := s.Layers[id]; parent != nil {
			for _, child := range parent.Children() {
				childID := tjs.ObjectID(child.ID)
				candidate, found := s.Layers[childID]
				if !found || candidate != child {
					found = false
					for candidateID, candidate := range s.Layers {
						if candidate == child {
							childID, found = candidateID, true
							break
						}
					}
				}
				if found {
					entry.children = append(entry.children, tjs.ObjectIDValue(childID))
				}
			}
		}
		entries = append(entries, entry)
	}
	s.mu.RUnlock()
	// A VM array is mutable VM state. Serialize writes, but never hold the
	// Services lock while calling into the VM (GC can call back into Services).
	s.childrenSyncMu.Lock()
	defer s.childrenSyncMu.Unlock()
	for _, entry := range entries {
		if err := vm.ClearArray(entry.array); err != nil {
			return err
		}
		for _, child := range entry.children {
			if err := vm.AppendArray(entry.array, child); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Services) invalidateLayer(vm *tjs.VM, id tjs.ObjectID) error {
	s.mu.Lock()
	l := s.Layers[id]
	if l == nil {
		s.mu.Unlock()
		return nil
	}
	for _, candidate := range s.Layers {
		if candidate == l || candidate.transitionSource == l {
			s.cancelLayerTransition(candidate)
		}
	}
	if l.Parent != nil {
		_ = l.Parent.RemoveChild(l)
	}
	for _, child := range l.Children() {
		_ = l.RemoveChild(child)
	}
	if s.Root == l {
		s.Root = nil
		if s.Compositor != nil {
			s.Compositor.SetRoot(nil)
		}
	}
	if window := s.WindowObjects[tjs.ObjectID(l.windowID)]; window != nil && window.primaryLayer == LayerID(id) {
		window.primaryLayer = 0
	}
	l.windowID = 0
	s.releaseLayerDefaultFontLocked(id)
	removedChildren, hadChildren := s.LayerChildren[id]
	delete(s.LayerCursors, id)
	delete(s.Layers, id)
	delete(s.LayerChildren, id)
	s.mu.Unlock()

	err := s.syncLayerChildren(vm)
	if hadChildren && vm != nil {
		if clearErr := vm.ClearArray(removedChildren); err == nil {
			err = clearErr
		}
	}
	return err
}

func (s *Services) layerGet(vm *tjs.VM, l *Layer, id tjs.ObjectID, key string, budget *uint64) (tjs.Value, error) {
	if key == "imageLeft" || key == "imageTop" || key == "imageWidth" || key == "imageHeight" {
		s.mu.RLock()
		hasImage := len(l.RGBA) != 0
		s.mu.RUnlock()
		if !hasImage {
			return tjs.VoidValue, fmt.Errorf("%w: layer has no image", ErrInvalidLayer)
		}
	}
	if key == "cursor" {
		s.mu.RLock()
		cursor, ok := s.LayerCursors[id]
		s.mu.RUnlock()
		if !ok {
			cursor = tjs.IntegerValue(0)
		}
		return cursor, nil
	}
	if key == "font" {
		return s.layerFont(vm, id, budget)
	}
	if key == "children" {
		if vm == nil {
			return tjs.VoidValue, fmt.Errorf("Layer.children requires VM")
		}
		s.mu.RLock()
		children, ok := s.LayerChildren[id]
		s.mu.RUnlock()
		if !ok {
			created, err := vm.NewArray(nil)
			if err != nil {
				return tjs.VoidValue, err
			}
			s.mu.Lock()
			children, ok = s.LayerChildren[id]
			if !ok {
				children = created
				s.LayerChildren[id] = children
			}
			s.mu.Unlock()
		}
		if err := s.syncLayerChildren(vm); err != nil {
			return tjs.VoidValue, err
		}
		return children, nil
	}

	switch key {
	case "focusable":
		s.mu.RLock()
		focusable := l.Focusable
		s.mu.RUnlock()
		return tjs.IntegerValue(boolInt(focusable)), nil
	case "nodeEnabled":
		return tjs.IntegerValue(boolInt(layerNodeEnabled(s, l))), nil
	case "nodeFocusable":
		return tjs.IntegerValue(boolInt(layerNodeFocusable(s, l))), nil
	case "focused":
		window := layerWindowState(s, l)
		return tjs.IntegerValue(boolInt(window != nil && window.focusedLayer == l.ID)), nil
	}
	switch key {
	case "neutralColor":
		switch l.Type {
		case LayerSubtractive, LayerMultiplicative, LayerDarken, LayerPSSubtractive,
			LayerPSMultiplicative, LayerPSColorBurn, LayerPSDarken:
			return tjs.IntegerValue(0xffffff), nil
		default:
			return tjs.IntegerValue(0), nil
		}
	case "callOnPaint":
		return tjs.IntegerValue(boolInt(l.callOnPaint)), nil
	case "parent":
		if l.Parent == nil {
			return tjs.NullValue, nil
		}
		for childID, candidate := range s.Layers {
			if candidate == l.Parent {
				return tjs.ObjectIDValue(childID), nil
			}
		}
		return tjs.NullValue, nil
	case "order":
		return tjs.IntegerValue(int64(l.orderIndex())), nil
	case "absoluteOrderMode":
		return tjs.IntegerValue(boolInt(l.absoluteOrderMode)), nil
	case "absolute":
		return tjs.IntegerValue(int64(l.absoluteOrderValue())), nil
	case "window":
		if windowID := tjs.ObjectID(l.windowID); windowID != 0 && s.WindowObjects[windowID] != nil {
			return tjs.ObjectIDValue(windowID), nil
		}
		return tjs.NullValue, nil
	case "isPrimary":
		window := s.WindowObjects[tjs.ObjectID(l.windowID)]
		return tjs.IntegerValue(boolInt(window != nil && window.primaryLayer == LayerID(id))), nil
	case "left":
		return tjs.IntegerValue(int64(l.Bounds.X)), nil
	case "top":
		return tjs.IntegerValue(int64(l.Bounds.Y)), nil
	case "width":
		return tjs.IntegerValue(int64(l.Bounds.Width)), nil
	case "height":
		return tjs.IntegerValue(int64(l.Bounds.Height)), nil
	case "imageLeft":
		return tjs.IntegerValue(int64(l.ImageLeft)), nil
	case "imageTop":
		return tjs.IntegerValue(int64(l.ImageTop)), nil
	case "imageWidth":
		return tjs.IntegerValue(int64(l.ImageWidth)), nil
	case "imageHeight":
		return tjs.IntegerValue(int64(l.ImageHeight)), nil
	case "visible":
		return tjs.IntegerValue(boolInt(l.Visible)), nil
	case "nodeVisible":
		for current := l; current != nil; current = current.Parent {
			if !current.Visible {
				return tjs.IntegerValue(0), nil
			}
		}
		return tjs.IntegerValue(1), nil
	case "opacity":
		return tjs.IntegerValue(int64(l.Opacity)), nil
	case "enabled":
		return tjs.IntegerValue(boolInt(l.Enabled)), nil
	case "eventTransparent":
		return tjs.IntegerValue(boolInt(l.EventTransparent)), nil
	case "imageModified":
		return tjs.IntegerValue(boolInt(l.imageModified)), nil

	case "type":
		return tjs.IntegerValue(int64(l.Type)), nil
	case "face":
		return tjs.IntegerValue(int64(l.Face)), nil
	case "holdAlpha":
		return tjs.IntegerValue(boolInt(l.HoldAlpha)), nil
	case "hitType":
		return tjs.IntegerValue(int64(l.Hit)), nil
	case "hitThreshold":
		return tjs.IntegerValue(int64(l.HitThreshold)), nil
	case "cursorX":
		return tjs.IntegerValue(int64(l.CursorX)), nil
	case "cursorY":
		return tjs.IntegerValue(int64(l.CursorY)), nil
	case "name":
		return tjs.StringValue(l.Name), nil
	case "hasImage":
		return tjs.IntegerValue(boolInt(len(l.RGBA) != 0)), nil
	case "clipLeft", "clipTop", "clipWidth", "clipHeight":
		clip, _ := l.Clip()
		switch key {
		case "clipLeft":
			return tjs.IntegerValue(int64(clip.X)), nil
		case "clipTop":
			return tjs.IntegerValue(int64(clip.Y)), nil
		case "clipWidth":
			return tjs.IntegerValue(int64(clip.Width)), nil
		default:
			return tjs.IntegerValue(int64(clip.Height)), nil
		}
	default:
		return tjs.VoidValue, fmt.Errorf("%w: unsupported Layer property %s", ErrUnsupportedNative, key)
	}
}

func (s *Services) layerFont(vm *tjs.VM, id tjs.ObjectID, budget *uint64) (tjs.Value, error) {
	if vm == nil {
		return tjs.VoidValue, fmt.Errorf("Layer.font requires VM")
	}
	s.mu.RLock()
	if fontID := s.LayerFonts[id]; fontID != 0 && s.Fonts[fontID] != nil {
		s.mu.RUnlock()
		return tjs.ObjectIDValue(fontID), nil
	}
	layerExists := s.Layers[id] != nil
	s.mu.RUnlock()
	if !layerExists {
		return tjs.VoidValue, fmt.Errorf("Layer.font: context has no Layer native instance")
	}

	fontClass, ok := vm.Globals["Font"]
	if !ok {
		return tjs.VoidValue, fmt.Errorf("Layer.font requires registered Font class")
	}
	font, err := vm.Construct(fontClass, nil, s, budget)
	if err != nil {
		return tjs.VoidValue, err
	}
	fontID := font.ObjectRef().Object

	s.mu.Lock()
	if s.Layers[id] == nil {
		delete(s.Fonts, fontID)
		s.mu.Unlock()
		return tjs.VoidValue, fmt.Errorf("Layer.font: context has no Layer native instance")
	}
	if currentID := s.LayerFonts[id]; currentID != 0 && s.Fonts[currentID] != nil {
		delete(s.Fonts, fontID)
		s.mu.Unlock()
		return tjs.ObjectIDValue(currentID), nil
	}
	s.releaseLayerDefaultFontLocked(id)
	s.LayerFonts[id] = fontID
	s.LayerDefaultFonts[id] = fontID
	s.mu.Unlock()
	return font, nil
}

func (s *Services) releaseLayerDefaultFontLocked(id tjs.ObjectID) {
	if fontID := s.LayerDefaultFonts[id]; fontID != 0 {
		delete(s.Fonts, fontID)
	}
	delete(s.LayerDefaultFonts, id)
	delete(s.LayerFonts, id)
}
func (s *Services) layerSet(vm *tjs.VM, l *Layer, id tjs.ObjectID, key string, arg func(int) (tjs.Value, error), intArg func(int) (int32, error), boolArg func(int) (bool, error), budget *uint64) (tjs.Value, error) {
	value, err := arg(0)
	if err != nil {
		return tjs.VoidValue, err
	}
	if key == "focusable" {
		enabled, err := value.Truth()
		if err != nil {
			return tjs.VoidValue, err
		}
		s.mu.Lock()
		wasFocusable := l.Focusable
		l.Focusable = enabled
		s.mu.Unlock()
		if wasFocusable && !enabled {
			window := layerWindowState(s, l)
			if window != nil && window.focusedLayer == LayerID(id) {
				target := layerNextFocusable(s, layerTreeRoot(l), l)
				if _, err := s.layerSetFocus(vm, window, target, true, budget); err != nil {
					return tjs.VoidValue, err
				}
			}
		}
		return tjs.VoidValue, nil
	}
	if key == "parent" {
		if value.Kind() == tjs.KindObject && value.IsNull() {
			s.mu.Lock()
			parent := l.Parent
			if parent == nil {
				s.mu.Unlock()
				return tjs.VoidValue, nil
			}
			if err := parent.RemoveChild(l); err != nil {
				s.mu.Unlock()
				return tjs.VoidValue, err
			}
			s.mu.Unlock()
			return tjs.VoidValue, s.syncLayerChildren(vm)
		}
		if value.Kind() != tjs.KindObject || value.IsNull() {
			return tjs.VoidValue, fmt.Errorf("expected a non-null Layer object")
		}
		parentID := value.ObjectRef().Object
		s.mu.Lock()
		parent := s.Layers[parentID]
		if parent == nil {
			s.mu.Unlock()
			return tjs.VoidValue, fmt.Errorf("object %d is not a Layer", parentID)
		}
		if parent == l {
			s.mu.Unlock()
			return tjs.VoidValue, ErrLayerTree
		}
		for ancestor := parent; ancestor != nil; ancestor = ancestor.Parent {
			if ancestor == l {
				s.mu.Unlock()
				return tjs.VoidValue, ErrLayerTree
			}
		}
		if l.Parent == parent {
			s.mu.Unlock()
			return tjs.VoidValue, nil
		}
		if l.Parent != nil {
			if err := l.Parent.RemoveChild(l); err != nil {
				s.mu.Unlock()
				return tjs.VoidValue, err
			}
		}
		if err := parent.AddChild(l); err != nil {
			s.mu.Unlock()
			return tjs.VoidValue, err
		}
		s.mu.Unlock()
		return tjs.VoidValue, s.syncLayerChildren(vm)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch key {
	case "window", "isPrimary":
		return tjs.VoidValue, nil
	case "order":
		n, e := intArg(0)
		if e == nil {
			e = l.SetOrderIndex(n)
		}
		return tjs.VoidValue, e
	case "absolute":
		n, e := intArg(0)
		if e == nil {
			e = l.SetAbsoluteOrderIndex(n)
		}
		return tjs.VoidValue, e
	case "absoluteOrderMode":
		b, e := boolArg(0)
		if e == nil {
			l.SetAbsoluteOrderMode(b)
		}
		return tjs.VoidValue, e
	case "callOnPaint":
		b, e := boolArg(0)
		if e == nil {
			if b {
				s.queueLayerPaint(l)
			} else {
				l.callOnPaint = false
			}
		}
		return tjs.VoidValue, e
	case "left":
		n, e := intArg(0)
		if e != nil {
			return tjs.VoidValue, e
		}
		l.Bounds.X = n
		l.markRenderDirty()
		return tjs.VoidValue, nil
	case "top":
		n, e := intArg(0)
		if e != nil {
			return tjs.VoidValue, e
		}
		l.Bounds.Y = n
		l.markRenderDirty()
		return tjs.VoidValue, nil
	case "width":
		n, e := intArg(0)
		if e == nil {
			e = l.SetSize(n, l.Bounds.Height)
		}
		return tjs.VoidValue, e
	case "height":
		n, e := intArg(0)
		if e == nil {
			e = l.SetSize(l.Bounds.Width, n)
		}
		return tjs.VoidValue, e
	case "imageLeft":
		n, e := intArg(0)
		if e != nil {
			return tjs.VoidValue, e
		}
		return tjs.VoidValue, l.SetImagePosition(n, l.ImageTop)
	case "imageTop":
		n, e := intArg(0)
		if e != nil {
			return tjs.VoidValue, e
		}
		return tjs.VoidValue, l.SetImagePosition(l.ImageLeft, n)
	case "imageWidth":
		n, e := intArg(0)
		if e != nil {
			return tjs.VoidValue, e
		}
		return tjs.VoidValue, l.SetImageWidth(n)
	case "imageHeight":
		n, e := intArg(0)
		if e != nil {
			return tjs.VoidValue, e
		}
		return tjs.VoidValue, l.SetImageHeight(n)
	case "visible":
		b, e := boolArg(0)
		if e != nil {
			return tjs.VoidValue, e
		}
		l.Visible = b
		l.markRenderDirty()
		return tjs.VoidValue, nil
	case "opacity":
		n, e := intArg(0)
		if e == nil {
			e = l.SetOpacity(int(n))
		}
		return tjs.VoidValue, e
	case "enabled":
		b, e := boolArg(0)
		if e != nil {
			return tjs.VoidValue, e
		}
		l.Enabled = b
		return tjs.VoidValue, nil
	case "eventTransparent":
		b, e := boolArg(0)
		if e != nil {
			return tjs.VoidValue, e
		}
		l.EventTransparent = b
		return tjs.VoidValue, nil
	case "type":
		n, e := intArg(0)
		if e == nil {
			e = l.SetType(LayerType(n))
		}
		return tjs.VoidValue, e
	case "imageModified":
		b, e := boolArg(0)
		if e != nil {
			return tjs.VoidValue, e
		}
		l.imageModified = b
		return tjs.VoidValue, nil

	case "face":
		n, e := intArg(0)
		if e != nil {
			return tjs.VoidValue, e
		}
		l.Face = n
		l.markRenderDirty()
		return tjs.VoidValue, nil
	case "holdAlpha":
		b, e := boolArg(0)
		if e != nil {
			return tjs.VoidValue, e
		}
		l.HoldAlpha = b
		l.markRenderDirty()
		return tjs.VoidValue, nil
	case "hitType":
		n, e := intArg(0)
		if e == nil && (n < 0 || n > 1) {
			e = fmt.Errorf("%w: hit type %d", ErrInvalidLayer, n)
		}
		if e == nil {
			l.SetHitType(HitType(n))
		}
		return tjs.VoidValue, e
	case "hitThreshold":
		n, e := intArg(0)
		if e == nil {
			e = l.SetHitThreshold(int(n))
		}
		return tjs.VoidValue, e
	case "cursor":
		if value.Kind() == tjs.KindString {
			s.LayerCursors[id] = value
			return tjs.VoidValue, nil
		}
		n, e := intArg(0)
		if e != nil {
			return tjs.VoidValue, e
		}
		s.LayerCursors[id] = tjs.IntegerValue(int64(n))
		return tjs.VoidValue, nil
	case "cursorX":
		n, e := intArg(0)
		if e != nil {
			return tjs.VoidValue, e
		}
		l.CursorX = n
		return tjs.VoidValue, nil
	case "cursorY":
		n, e := intArg(0)
		if e != nil {
			return tjs.VoidValue, e
		}
		l.CursorY = n
		return tjs.VoidValue, nil
	case "name":
		l.Name = value.Text()
		return tjs.VoidValue, nil
	case "hasImage":
		b, e := boolArg(0)
		if e != nil {
			return tjs.VoidValue, e
		}
		if !b {
			l.ClearImage()
			l.ResetClip()
			l.imageModified = true
			return tjs.VoidValue, nil
		}
		if l.Type == LayerBinder || l.Type == LayerEffect || l.Type == LayerFilter {
			return tjs.VoidValue, fmt.Errorf("%w: layer type cannot have an image", ErrInvalidLayer)
		}
		if len(l.RGBA) == 0 {
			e = resizeLayerImage(l, layerNativeDimension(l.Bounds.Width), layerNativeDimension(l.Bounds.Height))
			if e == nil {
				l.ImageLeft, l.ImageTop = 0, 0
				l.imageModified = true
			}
		}
		return tjs.VoidValue, e
	case "clipLeft", "clipTop", "clipWidth", "clipHeight":
		clip, _ := l.Clip()
		n, e := intArg(0)
		if e != nil {
			return tjs.VoidValue, e
		}
		switch key {
		case "clipLeft":
			clip.X = n
		case "clipTop":
			clip.Y = n
		case "clipWidth":
			clip.Width = n
		default:
			clip.Height = n
		}
		return tjs.VoidValue, l.SetClip(clip)
	case "font":
		if value.Kind() == tjs.KindObject && value.IsNull() {
			s.releaseLayerDefaultFontLocked(id)
			return tjs.VoidValue, nil
		}
		if value.Kind() != tjs.KindObject || value.IsNull() {
			return tjs.VoidValue, fmt.Errorf("Layer.font requires a Font object")
		}
		fontID := value.ObjectRef().Object
		if s.Fonts[fontID] == nil {
			return tjs.VoidValue, fmt.Errorf("Layer.font requires a Font object")
		}
		if s.LayerFonts[id] != fontID {
			s.releaseLayerDefaultFontLocked(id)
		}
		s.LayerFonts[id] = fontID
		return tjs.VoidValue, nil
	default:
		return tjs.VoidValue, fmt.Errorf("%w: unsupported Layer property %s", ErrUnsupportedNative, key)
	}
}

func layerNativeDimension(v int32) int32 {
	if v <= 0 {
		return nativeLayerDefaultSize
	}
	return v
}

func layerPixel(l *Layer, x, y int32) ([]byte, error) {
	if l == nil || len(l.RGBA) == 0 {
		return nil, fmt.Errorf("%w: layer has no image", ErrInvalidLayer)
	}
	if x < 0 || y < 0 || x >= l.ImageWidth || y >= l.ImageHeight {
		return nil, fmt.Errorf("%w: pixel outside image", ErrInvalidLayer)
	}
	return l.RGBA[(y*l.ImageWidth+x)*4:], nil
}

func resetNativeLayerClip(l *Layer) {
	if l == nil {
		return
	}
	if l.ImageWidth <= 0 || l.ImageHeight <= 0 {
		l.ResetClip()
		return
	}
	_ = l.SetClip(Rect{Width: l.ImageWidth, Height: l.ImageHeight})
}

func layerRect(l *Layer, x, y, w, h int32) (Rect, error) {
	if l == nil || len(l.RGBA) == 0 {
		return Rect{}, fmt.Errorf("%w: layer has no image", ErrInvalidLayer)
	}
	if w < 0 || h < 0 {
		return Rect{}, fmt.Errorf("%w: negative rectangle", ErrInvalidLayer)
	}
	if int64(x)+int64(w) > int64(math.MaxInt32) || int64(y)+int64(h) > int64(math.MaxInt32) {
		return Rect{}, fmt.Errorf("%w: rectangle exceeds bounds", ErrInvalidLayer)
	}
	r := Rect{X: x, Y: y, Width: w, Height: h}
	clip, ok := l.Clip()
	if ok {
		r = r.Intersect(clip)
	}
	return r.Intersect(Rect{Width: l.ImageWidth, Height: l.ImageHeight}), nil
}

func straightAlphaWeight(destination, source uint8) int {
	if source == 0 {
		return 255
	}
	at := float32(source) / 255
	bt := float32(destination) / 255
	c := bt / at
	c /= float32(1 - float64(bt) + float64(c))
	return min(int(c*255), 255)
}

func (s *Services) layerFillRect(l *Layer, operation string, args []tjs.Value, intArg func(int) (int32, error)) error {
	if len(args) < 5 {
		return fmt.Errorf("Layer.%s: missing argument %d", operation, len(args))
	}
	x, e := intArg(0)
	if e != nil {
		return e
	}
	y, e := intArg(1)
	if e != nil {
		return e
	}
	w, e := intArg(2)
	if e != nil {
		return e
	}
	h, e := intArg(3)
	if e != nil {
		return e
	}
	c, e := layerColorValue(args[4])
	if e != nil {
		return e
	}
	face := l.drawFace()
	opacity := int32(255)
	if operation == "colorRect" && len(args) > 5 && args[5].Kind() != tjs.KindVoid {
		opacity, e = intArg(5)
		if e != nil {
			return e
		}
		if face == layerFaceAddAlpha && opacity < 0 {
			return fmt.Errorf("%w: negative opacity on add-alpha face", ErrInvalidLayer)
		}
		if face != layerFaceAlpha || opacity >= 0 {
			opacity = min(opacity, int32(255))
		}
		if face != layerFaceAlpha && opacity < 0 {
			opacity = 0
		}
	}
	if operation == "colorRect" && face != layerFaceMask && face != layerFaceProvince &&
		face != layerFaceAlpha && face != layerFaceOpaque && face != layerFaceAddAlpha {
		return fmt.Errorf("%w: invalid colorRect draw face %d", ErrInvalidLayer, face)
	}
	if operation == "fillRect" && face != layerFaceAlpha && face != layerFaceOpaque &&
		face != layerFaceMask && face != layerFaceProvince && face != layerFaceAddAlpha {
		return fmt.Errorf("%w: invalid fillRect draw face %d", ErrInvalidLayer, face)
	}
	if len(l.RGBA) == 0 {
		if l.ImageWidth <= 0 || l.ImageHeight <= 0 {
			return nil
		}
		if err := resizeLayerImage(l, l.ImageWidth, l.ImageHeight); err != nil {
			return err
		}
	}
	r, e := layerRect(l, x, y, w, h)
	if e != nil {
		return e
	}
	if r.Empty() {
		return nil
	}
	if face == layerFaceProvince {
		if len(l.Province) == 0 && c.b == 0 {
			return nil
		}
		if len(l.Province) == 0 {
			l.Province = make([]byte, int(uint64(l.ImageWidth)*uint64(l.ImageHeight)))
			l.ProvinceWidth, l.ProvinceHeight = l.ImageWidth, l.ImageHeight
		}
		if c.b == 0 && r == (Rect{Width: l.ImageWidth, Height: l.ImageHeight}) {
			l.ClearProvince()
			return nil
		}
		for py := r.Y; py < r.Bottom(); py++ {
			for px := r.X; px < r.Right(); px++ {
				l.Province[py*l.ProvinceWidth+px] = c.b
			}
		}
		return nil
	}
	if operation == "colorRect" && (face == layerFaceMask || face == layerFaceProvince) {
		opacity = 255
	}
	if operation == "colorRect" && opacity == 0 {
		return nil
	}
	removing := operation == "colorRect" && face == layerFaceAlpha && opacity < 0
	strength := int(opacity)
	if removing {
		strength = int(min(-int64(opacity), int64(255)))
	} else {
		strength = max(0, min(strength, 255))
	}
	for py := r.Y; py < r.Bottom(); py++ {
		for px := r.X; px < r.Right(); px++ {
			i := int(py*l.ImageWidth+px) * 4
			p := l.RGBA[i : i+4]
			if operation == "fillRect" || face == layerFaceMask {
				switch face {
				case layerFaceMask:
					p[3] = c.b
				case layerFaceOpaque:
					p[0], p[1], p[2] = c.r, c.g, c.b
					if !l.HoldAlpha {
						p[3] = c.a
					}
				default:
					p[0], p[1], p[2], p[3] = c.r, c.g, c.b, c.a
				}
				continue
			}
			if removing {
				p[3] = byte((int(p[3]) * (255 - strength)) >> 8)
				continue
			}
			if strength == 255 {
				p[0], p[1], p[2] = c.r, c.g, c.b
				if face != layerFaceOpaque {
					p[3] = 255
				}
				continue
			}
			switch face {
			case layerFaceAlpha:
				weight := straightAlphaWeight(p[3], byte(strength))
				p[0] = byte(int(p[0]) + ((int(c.r)-int(p[0]))*weight)>>8)
				p[1] = byte(int(p[1]) + ((int(c.g)-int(p[1]))*weight)>>8)
				p[2] = byte(int(p[2]) + ((int(c.b)-int(p[2]))*weight)>>8)
				p[3] = byte(255 - ((255-int(p[3]))*(255-strength))/255)
			case layerFaceOpaque:
				p[0] = byte((int(p[0])*(255-strength) + int(c.r)*strength) >> 8)
				p[1] = byte((int(p[1])*(255-strength) + int(c.g)*strength) >> 8)
				p[2] = byte((int(p[2])*(255-strength) + int(c.b)*strength) >> 8)
			case layerFaceAddAlpha:
				alpha := strength + (strength >> 7)
				p[0] = byte(min(255, int(p[0])-((int(p[0])*alpha)>>8)+((int(c.r)*alpha)>>8)))
				p[1] = byte(min(255, int(p[1])-((int(p[1])*alpha)>>8)+((int(c.g)*alpha)>>8)))
				p[2] = byte(min(255, int(p[2])-((int(p[2])*alpha)>>8)+((int(c.b)*alpha)>>8)))
				p[3] = byte(min(255, int(p[3])-((int(p[3])*alpha)>>8)+alpha))
			}
		}
	}
	l.imageModified = true
	return nil
}

func (s *Services) layerDrawText(l *Layer, args []tjs.Value, budget *uint64) error {
	if len(args) < 4 {
		return fmt.Errorf("Layer.drawText: missing argument %d", len(args))
	}
	x, e := args[0].Int()
	if e != nil {
		return e
	}
	y, e := args[1].Int()
	if e != nil {
		return e
	}
	if x < math.MinInt32 || x > math.MaxInt32 || y < math.MinInt32 || y > math.MaxInt32 {
		return fmt.Errorf("Layer.drawText: position exceeds int32")
	}
	text := args[2].Text()
	if e = chargeTextBudget(budget, len(text)); e != nil {
		return e
	}
	s.mu.RLock()
	hasImage := len(l.RGBA) != 0
	face, holdAlpha := l.drawFace(), l.HoldAlpha
	s.mu.RUnlock()
	if !hasImage {
		return fmt.Errorf("%w: layer has no image", ErrInvalidLayer)
	}
	if face != layerFaceAlpha && face != layerFaceOpaque && face != layerFaceAddAlpha {
		return fmt.Errorf("%w: invalid drawText draw face %d", ErrInvalidLayer, face)
	}
	faceName := ""
	foreground := color.Color(color.White)
	if len(args) > 3 && args[3].Kind() != tjs.KindVoid {
		if args[3].Kind() == tjs.KindString {
			faceName = args[3].Text()
		} else {
			c, e := layerColorValue(args[3])
			if e != nil {
				return e
			}
			// drawText color is RGB; opacity is the separate fifth argument.
			foreground = color.NRGBA{R: c.r, G: c.g, B: c.b, A: 255}
		}
	}
	opacity := int64(255)
	var selected *FontState
	s.mu.RLock()
	if len(args) > 4 && args[4].Kind() != tjs.KindVoid {
		if args[4].Kind() == tjs.KindString {
			faceName = args[4].Text()
		} else if args[4].Kind() == tjs.KindObject && !args[4].IsNull() {
			f := s.Fonts[args[4].ObjectRef().Object]
			if f == nil {
				s.mu.RUnlock()
				return fmt.Errorf("Layer.drawText: face is not a Font")
			}
			configured := *f
			selected = &configured
		} else {
			opacity, e = args[4].Int()
			if e != nil {
				s.mu.RUnlock()
				return fmt.Errorf("Layer.drawText: invalid opacity: %w", e)
			}
			if opacity < 0 {
				opacity = 0
			} else if opacity > 255 {
				opacity = 255
			}
		}
	}
	if selected == nil {
		if fontID := s.LayerFonts[tjs.ObjectID(l.ID)]; fontID != 0 {
			if f := s.Fonts[fontID]; f != nil {
				configured := *f
				selected = &configured
			}
		}
	}
	state := newFontState()
	if s.FontSystem != nil && strings.TrimSpace(s.FontSystem.DefaultFont) != "" {
		state.Face = s.FontSystem.DefaultFont
	}
	s.mu.RUnlock()
	if selected != nil {
		state = selected
	}
	if faceName != "" {
		state.Face = faceName
	}
	if opacity != 255 {
		fg := color.NRGBAModel.Convert(foreground).(color.NRGBA)
		fg.A = byte(int(fg.A) * int(opacity) / 255)
		foreground = fg
	}
	fontFace, e := s.fontFace(state)
	if e != nil {
		return e
	}
	img, e := RasterizeText(fontFace, text, foreground)
	if e != nil {
		return e
	}
	s.mu.Lock()
	if len(l.RGBA) == 0 {
		s.mu.Unlock()
		return fmt.Errorf("%w: layer has no image", ErrInvalidLayer)
	}
	clip, _ := l.Clip()
	for sy := range img.Rect.Dy() {
		for sx := range img.Rect.Dx() {
			dx, dy := int32(x)+int32(sx), int32(y)+int32(sy)
			if dx < 0 || dy < 0 || dx >= l.ImageWidth || dy >= l.ImageHeight || (l.clipSet && !clip.Contains(dx, dy)) {
				continue
			}
			si := (sy*img.Rect.Dx() + sx) * 4
			di := (dy*l.ImageWidth + dx) * 4
			layerDrawTextPixel(l.RGBA[di:di+4], img.Pix[si:si+4], face, holdAlpha)
		}
	}
	s.mu.Unlock()
	return nil
}

// source is a premultiplied RGBA pixel from RasterizeText; layer pixels use
// straight RGB plus alpha. dfAlpha (bmAlphaOnAlpha in C++) composites the
// glyph coverage into that straight-alpha destination.
func layerDrawTextPixel(destination, source []byte, face int32, holdAlpha bool) {
	sourceAlpha, destinationAlpha := int(source[3]), int(destination[3])
	if sourceAlpha == 0 {
		return
	}
	if face == layerFaceOpaque && holdAlpha {
		for i := range 3 {
			sourceColor := int(source[i]) * 255 / sourceAlpha
			destination[i] = byte((sourceColor*sourceAlpha + int(destination[i])*(255-sourceAlpha)) / 255)
		}
		return
	}
	if face == layerFaceAddAlpha {
		for i := range 3 {
			destination[i] = byte(min(255, int(destination[i])+int(source[i])))
		}
		destination[3] = byte(min(255, destinationAlpha+sourceAlpha))
		return
	}
	outAlpha := sourceAlpha + destinationAlpha*(255-sourceAlpha)/255
	if outAlpha != 0 {
		for i := range 3 {
			// source is premultiplied; destination is straight-alpha. Keep
			// both terms in the same scaled units before unpremultiplying.
			premultiplied := int(source[i])*255 + int(destination[i])*destinationAlpha*(255-sourceAlpha)/255
			destination[i] = byte((premultiplied + outAlpha/2) / outAlpha)
		}
	}
	destination[3] = byte(outAlpha)
}
