package runtime

import (
	"fmt"
	"strings"

	"github.com/eslzzyl/krkrgo/internal/assets"
	"github.com/eslzzyl/krkrgo/internal/tjs"
)

type motionPlayerState struct {
	resourceManager tjs.ObjectID
	chara           string
	motion          string
	tickCount       int64
	speed           float64
	playing         bool
	completionType  int64
	outline         int64
	onAction        tjs.Value
}

func (s *Services) loadMotionResource(path string) (*motionResource, error) {
	data, err := s.readStorage(path)
	if err != nil {
		return nil, err
	}
	doc, err := assets.ParsePSB(data)
	if err != nil {
		return nil, err
	}
	return &motionResource{path: path, doc: doc, data: data}, nil
}

func (s *Services) motionResource(path string, manager tjs.ObjectID) *motionResource {
	s.ensureInit()
	s.mu.RLock()
	resource := s.MotionResources[manager]
	s.mu.RUnlock()
	if resource == nil || resource.path != path {
		return nil
	}
	return resource
}

func (s *Services) setMotionResource(manager tjs.ObjectID, resource *motionResource) {
	s.ensureInit()
	s.mu.Lock()
	s.MotionResources[manager] = resource
	s.mu.Unlock()
}

func (s *Services) motionPlayerNative(vm *tjs.VM, context tjs.Value, operation string, args []tjs.Value) (tjs.Value, error) {
	s.ensureInit()
	id := context.ObjectRef().Object
	switch operation {
	case "@initialize", "Player":
		state := s.motionPlayer(id)
		if len(args) > 0 && args[0].Kind() == tjs.KindObject && !args[0].IsNull() {
			state.resourceManager = args[0].ObjectRef().Object
		}
		return tjs.VoidValue, nil
	case "@invalidate", "finalize":
		s.mu.Lock()
		delete(s.MotionPlayers, id)
		s.mu.Unlock()
		return tjs.VoidValue, nil
	}
	state := s.motionPlayer(id)
	if strings.HasPrefix(operation, "get:") {
		return s.motionPlayerGet(vm, state, strings.TrimPrefix(operation, "get:"))
	}
	if strings.HasPrefix(operation, "set:") {
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("Motion.Player.%s requires a value", strings.TrimPrefix(operation, "set:"))
		}
		return tjs.VoidValue, s.motionPlayerSet(state, strings.TrimPrefix(operation, "set:"), args[0])
	}
	switch operation {
	case "play":
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("Motion.Player.play requires a motion name")
		}
		motion := args[0].Text()
		if state.playing && state.motion == motion {
			return tjs.VoidValue, nil
		}
		state.motion = motion
		state.tickCount = 0
		state.playing = true
		return tjs.VoidValue, nil
	case "stop":
		state.playing = false
		return tjs.VoidValue, nil
	case "progress":
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("Motion.Player.progress requires an interval")
		}
		interval, err := args[0].Int()
		if err != nil {
			return tjs.VoidValue, err
		}
		if interval < 0 {
			interval = 0
		}
		state.tickCount += int64(float64(interval) * state.speed)
		if resource := s.playerResource(state); resource != nil {
			if last := motionLastTime(resource, state.chara, state.motion); last >= 0 && state.tickCount >= last {
				state.tickCount = last
				state.playing = false
			}
		}
		return tjs.VoidValue, nil
	case "clear":
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("Motion.Player.clear requires a target layer")
		}
		return tjs.VoidValue, s.clearMotionTarget(vm, args[0])
	case "draw":
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("Motion.Player.draw requires a target layer")
		}
		return tjs.VoidValue, s.drawMotionTarget(vm, state, args[0])
	case "setDrawAffineTranslateMatrix":
		return tjs.VoidValue, nil
	case "skipToSync":
		if resource := s.playerResource(state); resource != nil {
			if last := motionLastTime(resource, state.chara, state.motion); last >= 0 {
				state.tickCount = last
			}
		}
		return tjs.VoidValue, nil
	case "setVariable":
		if len(args) < 2 {
			return tjs.VoidValue, fmt.Errorf("Motion.Player.setVariable requires name and value")
		}
		return tjs.VoidValue, nil
	case "getVariable":
		return tjs.VoidValue, nil
	default:
		return tjs.VoidValue, fmt.Errorf("unsupported Motion.Player operation %q", operation)
	}
}

func (s *Services) motionPlayer(id tjs.ObjectID) *motionPlayerState {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.MotionPlayers[id]
	if state == nil {
		state = &motionPlayerState{speed: 1}
		s.MotionPlayers[id] = state
	}
	return state
}

func (s *Services) motionPlayerGet(vm *tjs.VM, state *motionPlayerState, property string) (tjs.Value, error) {
	switch property {
	case "chara":
		return tjs.StringValue(state.chara), nil
	case "motion":
		return tjs.StringValue(state.motion), nil
	case "tickCount":
		return tjs.IntegerValue(state.tickCount), nil
	case "speed":
		return tjs.RealValue(state.speed), nil
	case "playing", "allplaying":
		if state.playing {
			return tjs.IntegerValue(1), nil
		}
		return tjs.IntegerValue(0), nil
	case "tags":
		return vm.NewArray(nil)
	case "completionType":
		return tjs.IntegerValue(state.completionType), nil
	case "outline":
		return tjs.IntegerValue(state.outline), nil
	case "onAction":
		return state.onAction, nil
	default:
		return tjs.VoidValue, fmt.Errorf("unsupported Motion.Player property %q", property)
	}
}

func (s *Services) motionPlayerSet(state *motionPlayerState, property string, value tjs.Value) error {
	switch property {
	case "chara":
		state.chara = value.Text()
	case "motion":
		state.motion = value.Text()
	case "tickCount":
		n, err := value.Int()
		if err != nil {
			return err
		}
		state.tickCount = n
	case "speed":
		n, err := value.Real()
		if err != nil {
			return err
		}
		state.speed = n
	case "completionType":
		n, err := value.Int()
		if err != nil {
			return err
		}
		state.completionType = n
	case "outline":
		n, err := value.Int()
		if err != nil {
			return err
		}
		state.outline = n
	case "onAction":
		state.onAction = value
	default:
		return fmt.Errorf("unsupported Motion.Player property %q", property)
	}
	return nil
}

func (s *Services) playerResource(state *motionPlayerState) *motionResource {
	s.mu.RLock()
	resource := s.MotionResources[state.resourceManager]
	if resource == nil {
		for _, candidate := range s.MotionResources {
			resource = candidate
			break
		}
	}
	s.mu.RUnlock()
	return resource
}

func motionLastTime(resource *motionResource, chara, motion string) int64 {
	if resource == nil || resource.doc == nil {
		return -1
	}
	root, _ := resource.doc.Root.(map[string]assets.Value)
	objects, _ := root["object"].(map[string]assets.Value)
	character, _ := objects[chara].(map[string]assets.Value)
	motions, _ := character["motion"].(map[string]assets.Value)
	value, _ := motions[motion].(map[string]assets.Value)
	if value == nil {
		return -1
	}
	return int64(psbMotionFloat(value, "lastTime", -1))
}

func (s *Services) motionLayerFromValue(_ *tjs.VM, value tjs.Value) (*Layer, error) {
	ref := value.ObjectRef()
	layer, _, err := s.layerFromValue(value)
	if err == nil {
		return layer, nil
	}
	s.mu.RLock()
	if ref.HasBoundContext() {
		layer = s.Layers[ref.Context]
	}
	ownerID := s.MotionAdaptors[ref.Object]
	if ownerID == 0 {
		ownerID = s.MotionAdaptors[ref.Context]
	}
	if layer == nil {
		layer = s.Layers[ownerID]
	}
	s.mu.RUnlock()
	if layer == nil {
		return nil, err
	}
	return layer, nil
}

func (s *Services) clearMotionTarget(vm *tjs.VM, value tjs.Value) error {
	layer, err := s.motionLayerFromValue(vm, value)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(layer.RGBA) == 0 {
		return nil
	}
	for i := range layer.RGBA {
		layer.RGBA[i] = 0
	}
	layer.imageModified = true
	return nil
}

func (s *Services) drawMotionTarget(vm *tjs.VM, state *motionPlayerState, value tjs.Value) error {
	resource := s.playerResource(state)
	if resource == nil {
		return fmt.Errorf("Motion.Player.draw has no loaded PSB resource")
	}
	frame, err := renderPSBMotion(resource, state.chara, state.motion, state.tickCount)
	if err != nil {
		return err
	}
	layer, err := s.motionLayerFromValue(vm, value)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := layer.SetRGBA(int32(frame.Width), int32(frame.Height), frame.RGBA); err != nil {
		return err
	}
	if err := layer.SetSizeToImageSize(); err != nil {
		return err
	}
	layer.Visible = true
	layer.imageModified = true
	return nil
}
func (s *Services) motionSeparateLayerAdaptorNative(context tjs.Value, operation string, args []tjs.Value) (tjs.Value, error) {
	id := context.ObjectRef().Object
	switch operation {
	case "@initialize", "SeparateLayerAdaptor":
		if len(args) > 0 && args[0].Kind() == tjs.KindObject && !args[0].IsNull() {
			s.mu.Lock()
			s.MotionAdaptors[id] = args[0].ObjectRef().Object
			s.mu.Unlock()
		}
		return tjs.VoidValue, nil
	case "@invalidate", "finalize":
		s.mu.Lock()
		delete(s.MotionAdaptors, id)
		s.mu.Unlock()
		return tjs.VoidValue, nil
	default:
		return tjs.VoidValue, fmt.Errorf("unsupported Motion.SeparateLayerAdaptor operation %q", operation)
	}
}
