package runtime

import (
	"fmt"
	"os"
	"strings"

	"github.com/eslzzyl/krkrgo/internal/assets"
	"github.com/eslzzyl/krkrgo/internal/tjs"
)

func motionTrace(format string, args ...any) {
	if os.Getenv("KRKRGO_MOTION_TRACE") == "" {
		return
	}
	fmt.Printf("motion-trace "+format+"\n", args...)
}

// Motion progress advances a frame clock at 60 fps: the reference converts the
// caller's millisecond delta with kMotionFramesPerMillisecond before stepping
// timelines and clips, so PSB frame times and lastTime share that unit.
const (
	motionFramesPerMillisecond = 60.0 / 1000.0
	motionProgressMaxMS        = 60000
)

// Motion.PlayFlag* values the native namespace exposes (cpp/plugins/
// motionplayer/main.cpp). The games apply a motion through
// MotionAffineSourceLayer.setOptions, which calls play(label, flags) once per
// frame with PlayFlagForce, so the same-label form of play has to keep the
// playback position: shipped krkr2 runs these games with that call pattern and
// a restart there would pin every cut-in to its first frame.
const (
	motionPlayFlagForce   = 1
	motionPlayFlagChain   = 2
	motionPlayFlagAsCan   = 4
	motionPlayFlagJoin    = 8
	motionPlayFlagStealth = 16
)

type motionPlayerState struct {
	resourceManager tjs.ObjectID
	chara           string
	motion          string
	stealthMotion   string
	frameTick       float64
	speed           float64
	playing         bool
	// held marks a one-shot motion that reached its last time: the reference
	// stops advancing the timeline at that point and keeps the evaluated pose,
	// and the layer keeps drawing the clamped frame. A repeat apply of the same
	// motion keeps the held pose; an explicit stop clears the mark so a later
	// apply starts the motion again.
	held           bool
	completionType int64
	outline        int64
	onAction       tjs.Value
	drawMatrix     motionDrawMatrix
	playerID       tjs.ObjectID
	gpuMotion      *RenderMotion
	gpuMotionValid bool
	gpuMotionKey   motionPlayerGPUKey
}

type motionPlayerGPUKey struct {
	resource      *motionResource
	chara, motion string
	tick          float64
	filter        int64
	matrix        motionDrawMatrix
}

func (s *Services) loadMotionResource(path string) (*motionResource, error) {
	motionTrace("load path=%q", path)
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
		flags := int64(0)
		if len(args) > 1 && args[1].Kind() != tjs.KindVoid {
			value, err := args[1].Int()
			if err != nil {
				return tjs.VoidValue, err
			}
			flags = value
		}
		if flags&motionPlayFlagStealth != 0 {
			// PlayFlagStealth stores the request without loading or starting it
			// (Player_playImpl commit step).
			state.stealthMotion = motion
			return tjs.VoidValue, nil
		}
		if motion == "" {
			// Empty request restarts the loaded motion from its origin unless
			// the chain flag keeps the current position.
			if flags&motionPlayFlagChain == 0 {
				state.frameTick = 0
			}
			state.playing = state.motion != ""
			state.held = false
			return tjs.VoidValue, nil
		}
		if state.motion == motion && (state.playing || state.held) {
			// Same request as the loaded motion: keep playing, or keep holding
			// the pose the motion reached. Layers re-apply their source options
			// every frame, so this form is the common case.
			return tjs.VoidValue, nil
		}
		state.motion = motion
		if flags&motionPlayFlagChain == 0 {
			state.frameTick = 0
		}
		state.playing = true
		state.held = false
		motionTrace("play id=%d chara=%q motion=%q flags=%d", id, state.chara, state.motion, flags)
		return tjs.VoidValue, nil
	case "stop":
		state.playing = false
		state.held = false
		return tjs.VoidValue, nil
	case "progress":
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("Motion.Player.progress requires an interval")
		}
		interval, err := args[0].Int()
		if err != nil {
			return tjs.VoidValue, err
		}
		if interval < 0 || interval > motionProgressMaxMS {
			interval = 0
		}
		// A player that was stopped or that reached the end of a one-shot
		// motion holds the pose it has; further progress calls do not move it.
		if !state.playing {
			return tjs.VoidValue, nil
		}
		state.frameTick += float64(interval) * motionFramesPerMillisecond * state.speed
		if resource := s.playerResource(state); resource != nil {
			last := motionLastTime(resource, state.chara, state.motion)
			if last >= 0 && state.frameTick >= last {
				advanceMotionClock(state, last, motionLoopTime(resource, state.chara, state.motion))
				if !state.playing {
					motionTrace("progress-stop id=%d motion=%q frame=%.2f last=%.2f", id, state.motion, state.frameTick, last)
				}
			}
		}
		if os.Getenv("KRKRGO_MOTION_TRACE") != "" {
			motionTrace("progress id=%d interval=%d frame=%.2f playing=%v", id, interval, state.frameTick, state.playing)
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
		motionTrace("draw time=%d id=%d motion=%q frame=%.2f playing=%v", s.Timers.Now(), id, state.motion, state.frameTick, state.playing)
		state.playerID = id
		return tjs.VoidValue, s.drawMotionTarget(vm, state, args[0])
	case "setDrawAffineTranslateMatrix":
		if len(args) < 6 {
			return tjs.VoidValue, fmt.Errorf("Motion.Player.setDrawAffineTranslateMatrix requires six values")
		}
		values := make([]float64, 0, 6)
		for i := range 6 {
			number, err := args[i].Real()
			if err != nil {
				return tjs.VoidValue, err
			}
			values = append(values, number)
		}
		state.drawMatrix = motionDrawMatrix{a: values[0], c: values[1], b: values[2], d: values[3], tx: values[4], ty: values[5]}
		motionTrace("draw-affine id=%d a=%.4f c=%.4f b=%.4f d=%.4f tx=%.4f ty=%.4f",
			id, values[0], values[1], values[2], values[3], values[4], values[5])
		return tjs.VoidValue, nil
	case "skipToSync":
		if resource := s.playerResource(state); resource != nil {
			if last := motionLastTime(resource, state.chara, state.motion); last >= 0 {
				state.frameTick = last
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
		state = &motionPlayerState{speed: 1, drawMatrix: identityMotionDrawMatrix()}
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
	case "stealthMotion":
		return tjs.StringValue(state.stealthMotion), nil
	case "tickCount":
		// Reference Player.h: the TJS property converts the frame clock back to
		// milliseconds and clamps non-positive clocks to zero.
		if state.frameTick > 0 {
			return tjs.RealValue(state.frameTick * 1000 / 60), nil
		}
		return tjs.RealValue(0), nil
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
	case "stealthMotion":
		state.stealthMotion = value.Text()
	case "motion":
		motion := value.Text()
		if state.motion == motion {
			return nil
		}
		// Reference Player::setMotion starts the requested motion through the
		// load path; a repeat of the loaded key only reselects it.
		state.motion = motion
		state.frameTick = 0
		state.playing = motion != ""
		state.held = false
	case "tickCount":
		n, err := value.Real()
		if err != nil {
			return err
		}
		state.frameTick = n * motionFramesPerMillisecond
		// An explicit seek moves the pose, so a later repeat of the same
		// motion starts over instead of holding.
		state.held = false
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

// motionField reads one numeric field of a motion entry, returning fallback
// when the resource, the character, or the motion is unavailable.
func motionField(resource *motionResource, chara, motion, key string, fallback float64) float64 {
	if resource == nil || resource.doc == nil {
		return fallback
	}
	root, _ := resource.doc.Root.(map[string]assets.Value)
	objects, _ := root["object"].(map[string]assets.Value)
	character, _ := objects[chara].(map[string]assets.Value)
	motions, _ := character["motion"].(map[string]assets.Value)
	value, _ := motions[motion].(map[string]assets.Value)
	if value == nil {
		return fallback
	}
	return psbMotionFloat(value, key, fallback)
}

func motionLastTime(resource *motionResource, chara, motion string) float64 {
	return motionField(resource, chara, motion, "lastTime", -1)
}

func motionLoopTime(resource *motionResource, chara, motion string) float64 {
	return motionField(resource, chara, motion, "loopTime", -1)
}

// advanceMotionClock moves the frame clock by one interval and applies the
// reference loop rule: a negative loopTime clamps at lastTime and ends
// playback, a loopTime inside the timeline wraps the clock back to the loop
// start and keeps playing.
func advanceMotionClock(state *motionPlayerState, last, loop float64) {
	if last < 0 || state.frameTick < last {
		return
	}
	if loop < 0 || loop >= last {
		state.frameTick = last
		state.playing = false
		state.held = true
		return
	}
	for guard := 0; state.frameTick >= last && guard < 1024; guard++ {
		state.frameTick += loop - last
	}
	if state.frameTick >= last {
		state.frameTick = last
		state.playing = false
		state.held = true
	}
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
	layer.gpuMotion = nil
	if len(layer.RGBA) == 0 {
		return nil
	}
	for i := range layer.RGBA {
		layer.RGBA[i] = 0
	}
	layer.imageModified = true
	layer.markImageDirty()
	return nil
}

func motionTraceEnabled() bool { return os.Getenv("KRKRGO_MOTION_TRACE") != "" }

func (s *Services) drawMotionTarget(vm *tjs.VM, state *motionPlayerState, value tjs.Value) error {
	resource := s.playerResource(state)
	if resource == nil {
		return fmt.Errorf("Motion.Player.draw has no loaded PSB resource")
	}
	layer, err := s.motionLayerFromValue(vm, value)
	if err != nil {
		return err
	}
	if s.gpuCapture && layerHasCanvas(layer) && layer.Type == LayerAlpha {
		key := motionPlayerGPUKey{
			resource: resource, chara: state.chara, motion: state.motion,
			tick: state.frameTick, filter: state.completionType, matrix: state.drawMatrix,
		}
		motion := state.gpuMotion
		if !state.gpuMotionValid || state.gpuMotionKey != key {
			var err error
			motion, err = buildGPUMotion(resource, state.chara, state.motion, state.frameTick, state.completionType, state.drawMatrix)
			if err != nil {
				return err
			}
			state.gpuMotion, state.gpuMotionKey, state.gpuMotionValid = motion, key, true
		}
		s.mu.Lock()
		changed := layer.gpuMotion != motion
		layer.gpuMotion = motion
		layer.Visible = true
		if changed {
			layer.imageModified = true
			layer.markImageDirty()
		}
		s.mu.Unlock()
		if motionTraceEnabled() {
			motionTrace("gpu-draw time=%d id=%d type=%d canvas=%v motion=%q frame=%.2f draws=%d cached=%v", s.Timers.Now(), state.playerID, layer.Type, layerHasCanvas(layer), state.motion, state.frameTick, len(motion.Draws), !changed)
		}
		return nil
	}
	frame, err := renderPSBMotion(resource, state.chara, state.motion, state.frameTick, state.completionType)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	layer.gpuMotion = nil
	// A motion layer draws into the canvas its owner layer already owns, so the
	// layer keeps the size its KAG definition assigned. Only a layer without a
	// bitmap adopts the motion canvas size.
	if !layerHasCanvas(layer) {
		if err := layer.SetRGBA(int32(frame.Width), int32(frame.Height), frame.RGBA); err != nil {
			return err
		}
		if err := layer.SetSizeToImageSize(); err != nil {
			return err
		}
		layer.Visible = true
		layer.imageModified = true
		layer.markImageDirty()
		return nil
	}
	drawMotionCanvasInto(layer, frame, state.drawMatrix, state.completionType)
	layer.Visible = true
	layer.imageModified = true
	layer.markImageDirty()
	return nil
}

func layerHasCanvas(layer *Layer) bool {
	if layer.ImageWidth <= 0 || layer.ImageHeight <= 0 {
		return false
	}
	return len(layer.RGBA) >= int(layer.ImageWidth)*int(layer.ImageHeight)*4
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
