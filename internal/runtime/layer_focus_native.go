package runtime

import (
	"fmt"
	"github.com/eslzzyl/krkrgo/internal/tjs"
)

func layerIsWithin(layer, ancestor *Layer) bool {
	for current := layer; current != nil; current = current.Parent {
		if current == ancestor {
			return true
		}
	}
	return false
}

func layerWindowState(s *Services, layer *Layer) *WindowState {
	if s == nil || layer == nil || layer.windowID == 0 {
		return nil
	}
	s.mu.RLock()
	window := s.WindowObjects[tjs.ObjectID(layer.windowID)]
	s.mu.RUnlock()
	return window
}

func layerTreeRoot(layer *Layer) *Layer {
	for layer != nil && layer.Parent != nil {
		layer = layer.Parent
	}
	return layer
}

func layerTreeNodes(root *Layer) []*Layer {
	if root == nil {
		return nil
	}
	nodes := make([]*Layer, 0, 8)
	pending := []*Layer{root}
	for len(pending) != 0 {
		last := len(pending) - 1
		layer := pending[last]
		pending = pending[:last]
		if layer == nil {
			continue
		}
		nodes = append(nodes, layer)
		children := layer.Children()
		for i := len(children) - 1; i >= 0; i-- {
			pending = append(pending, children[i])
		}
	}
	return nodes
}

func layerNodeEnabled(s *Services, layer *Layer) bool {
	for current := layer; current != nil; current = current.Parent {
		if !current.Enabled {
			return false
		}
	}
	window := layerWindowState(s, layer)
	if window == nil || len(window.modalLayers) == 0 {
		return true
	}
	modal := s.Layer(tjs.ObjectID(window.modalLayers[len(window.modalLayers)-1]))
	return modal == nil || layerIsWithin(layer, modal)
}

func layerNodeVisible(layer *Layer) bool {
	for current := layer; current != nil; current = current.Parent {
		if !current.Visible {
			return false
		}
	}
	return true
}

func layerNodeFocusable(s *Services, layer *Layer) bool {
	return layer != nil && layer.Focusable && layerNodeEnabled(s, layer) && layerNodeVisible(layer)
}

func layerValue(layer *Layer) tjs.Value {
	if layer == nil {
		return tjs.NullValue
	}
	return tjs.ObjectValue(tjs.NewObjectRef(tjs.ObjectID(layer.ID)))
}

func (s *Services) layerEvent(vm *tjs.VM, layer *Layer, name string, args []tjs.Value, budget *uint64) error {
	if vm == nil || layer == nil {
		return nil
	}
	context := layerValue(layer)
	handler, err := vm.GetMember(context, name, s, budget)
	if err != nil || handler.Kind() == tjs.KindVoid {
		return err
	}
	_, err = vm.CallFunction(handler, context, args, s, budget)
	return err
}

func layerEnabledSnapshot(s *Services, window *WindowState, root *Layer) map[LayerID]bool {
	before := make(map[LayerID]bool)
	for _, layer := range layerTreeNodes(root) {
		if layer.windowID == 0 || s.WindowObjects[tjs.ObjectID(layer.windowID)] == window {
			before[layer.ID] = layerNodeEnabled(s, layer)
		}
	}
	return before
}

func (s *Services) layerNotifyEnabled(vm *tjs.VM, before map[LayerID]bool, root *Layer, budget *uint64) error {
	for _, layer := range layerTreeNodes(root) {
		old, found := before[layer.ID]
		if !found || s.Layer(tjs.ObjectID(layer.ID)) != layer {
			continue
		}
		now := layerNodeEnabled(s, layer)
		if now == old {
			continue
		}
		event := "onNodeDisabled"
		if now {
			event = "onNodeEnabled"
		}
		if err := s.layerEvent(vm, layer, event, nil, budget); err != nil {
			return err
		}
	}
	return nil
}

func (s *Services) layerSetFocus(vm *tjs.VM, window *WindowState, target *Layer, forward bool, budget *uint64) (bool, error) {
	if window == nil {
		return false, nil
	}
	if target != nil && !layerNodeFocusable(s, target) {
		return false, nil
	}
	previous := s.Layer(tjs.ObjectID(window.focusedLayer))
	if previous == target {
		return false, nil
	}
	if target != nil {
		if err := s.layerEvent(vm, target, "onBeforeFocus", []tjs.Value{
			layerValue(target), layerValue(previous), tjs.IntegerValue(boolInt(forward)),
		}, budget); err != nil {
			return false, err
		}
		if s.Layer(tjs.ObjectID(target.ID)) != target || !layerNodeFocusable(s, target) {
			return false, nil
		}
	}
	if window.focusChanging {
		return false, fmt.Errorf("cannot change focus while processing focus callbacks")
	}
	window.focusChanging = true
	window.focusedLayer = 0
	if target != nil {
		window.focusedLayer = target.ID
	}
	if err := s.layerEvent(vm, previous, "onBlur", []tjs.Value{layerValue(target)}, budget); err != nil {
		window.focusChanging = false
		return false, err
	}
	if err := s.layerEvent(vm, target, "onFocus", []tjs.Value{
		layerValue(previous), tjs.IntegerValue(boolInt(forward)),
	}, budget); err != nil {
		window.focusChanging = false
		return false, err
	}
	window.focusChanging = false
	return true, nil
}

func (s *Services) layerSetMode(vm *tjs.VM, layer *Layer, budget *uint64) error {
	window := layerWindowState(s, layer)
	if window == nil {
		return nil
	}
	root := layerTreeRoot(layer)
	before := layerEnabledSnapshot(s, window, root)
	layer.Visible = true
	layer.markRenderDirty()
	var result error
	if len(window.modalLayers) != 0 {
		current := s.Layer(tjs.ObjectID(window.modalLayers[len(window.modalLayers)-1]))
		if current != nil && current != layer && layerIsWithin(current, layer) {
			result = fmt.Errorf("cannot set mode to an ancestor or current modal Layer")
		}
	}
	for parent := layer.Parent; result == nil && parent != nil; parent = parent.Parent {
		if !parent.Visible {
			result = fmt.Errorf("cannot set mode under an invisible Layer")
		}
	}
	if result == nil && !layer.Enabled {
		result = fmt.Errorf("cannot set mode to a disabled Layer")
	}
	if result == nil {
		var first *Layer
		for _, candidate := range layerTreeNodes(layer) {
			if layerNodeFocusable(s, candidate) {
				first = candidate
				break
			}
		}
		if _, err := s.layerSetFocus(vm, window, first, true, budget); err != nil {
			result = err
		}
	}
	if result == nil && s.Layer(tjs.ObjectID(layer.ID)) == layer && layerWindowState(s, layer) == window {
		found := false
		for _, current := range window.modalLayers {
			if current == layer.ID {
				found = true
				break
			}
		}
		if !found {
			window.modalLayers = append(window.modalLayers, layer.ID)
		}
	}
	if err := s.layerNotifyEnabled(vm, before, root, budget); result == nil {
		result = err
	}
	return result
}

func layerNextFocusable(s *Services, root, from *Layer) *Layer {
	nodes := layerTreeNodes(root)
	if len(nodes) == 0 {
		return nil
	}
	start := -1
	for i, layer := range nodes {
		if layer == from {
			start = i
			break
		}
	}
	if start < 0 {
		return nil
	}
	for step := 1; step < len(nodes); step++ {
		candidate := nodes[(start+step)%len(nodes)]
		if layerNodeFocusable(s, candidate) {
			return candidate
		}
	}
	return nil
}

func (s *Services) layerRemoveMode(vm *tjs.VM, layer *Layer, budget *uint64) error {
	window := layerWindowState(s, layer)
	if window == nil {
		return nil
	}
	removed := false
	for _, current := range window.modalLayers {
		if current == layer.ID {
			removed = true
			break
		}
	}
	if !removed {
		return nil
	}
	root := layerTreeRoot(layer)
	before := layerEnabledSnapshot(s, window, root)
	kept := window.modalLayers[:0]
	for _, current := range window.modalLayers {
		if current != layer.ID {
			kept = append(kept, current)
		}
	}
	window.modalLayers = kept
	if _, err := s.layerSetFocus(vm, window, layerNextFocusable(s, layerTreeRoot(layer), layer), true, budget); err != nil {
		return err
	}
	return s.layerNotifyEnabled(vm, before, layerTreeRoot(layer), budget)
}
