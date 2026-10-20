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
	// resource is the PSB module a player bound explicitly (EmotePlayer's
	// motionKey/module path). When set it takes precedence over the
	// resource-manager lookup so playback never depends on whichever module the
	// manager loaded last.
	resource *motionResource
	// attached holds the secondary snapshots MultiCache linked to the primary
	// (EmotePlayer::play calls addEmoteFile for every non-primary cached
	// module); source and nested-motion lookup falls back to them.
	attached      []*motionResource
	emote         *emotePlayerState
	chara         string
	motion        string
	stealthMotion string
	frameTick     float64
	speed         float64
	zFactor       float64
	zFactorSet    bool
	playing       bool
	// held marks a one-shot motion that reached its last time: the reference
	// stops advancing the timeline at that point and keeps the evaluated pose,
	// and the layer keeps drawing the clamped frame. A repeat apply of the same
	// motion keeps the held pose; an explicit stop clears the mark so a later
	// apply starts the motion again.
	held           bool
	syncWaiting    bool
	syncActive     bool
	completionType int64
	maskMode       int64
	outline        string
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
	zFactor       float64
	matrix        motionDrawMatrix
	scale         int32
	maskMode      int64
	completion    int64
	freezeBody    bool
	variables     string
}

func (s *Services) loadMotionResource(path string) (*motionResource, error) {
	motionTrace("load path=%q", path)
	data, err := s.readStorage(path)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	seed := s.EmotePSBDecryptSeed
	s.mu.RUnlock()
	doc, err := assets.ParsePSBWithSeed(data, seed)
	if err != nil {
		return nil, err
	}
	return &motionResource{path: path, doc: doc, data: data}, nil
}

// motionResourceStore is the Go counterpart of the reference ResourceManager
// State::loadedModules (ResourceManager.h:42-58): one manager holds every loaded
// PSB keyed by path alias, and a second load adds an entry instead of replacing
// the first. order preserves insertion order so multi-cache primary selection
// and equality checks stay deterministic.
type motionResourceStore struct {
	modules    map[string]*motionResource
	order      []*motionResource
	cachePaths map[*motionResource]map[string]struct{}
	lastLoaded *motionResource
	lastPath   string
}

// motionResourcePathAliases mirrors buildPathCacheKeys (ResourceManager.cpp:36)
// without host path resolution: the raw storage path, the lzfs-trimmed path,
// and a slash-normalized clean spelling all address the same entry.
func motionResourcePathAliases(path string) []string {
	aliases := make([]string, 0, 4)
	add := func(value string) {
		if value == "" {
			return
		}
		for _, existing := range aliases {
			if existing == value {
				return
			}
		}
		aliases = append(aliases, value)
	}
	add(path)
	trimmed := path
	for _, prefix := range []string{"lzfs://./", "lzfs://", "file://./", "file://"} {
		trimmed = strings.TrimPrefix(trimmed, prefix)
	}
	trimmed = strings.TrimPrefix(trimmed, "./")
	add(trimmed)
	add(strings.ReplaceAll(trimmed, "\\", "/"))
	return aliases
}

func (s *Services) motionResourceStoreForLocked(manager tjs.ObjectID) *motionResourceStore {
	cache := s.MotionResources[manager]
	if cache == nil {
		cache = &motionResourceStore{modules: make(map[string]*motionResource)}
		s.MotionResources[manager] = cache
	}
	return cache
}

func (s *Services) motionResource(path string, manager tjs.ObjectID) *motionResource {
	s.ensureInit()
	s.mu.RLock()
	cache := s.MotionResources[manager]
	var resource *motionResource
	if cache != nil {
		for _, alias := range motionResourcePathAliases(path) {
			if candidate := cache.modules[alias]; candidate != nil {
				resource = candidate
				break
			}
		}
	}
	s.mu.RUnlock()
	return resource
}

func (s *Services) setMotionResource(manager tjs.ObjectID, resource *motionResource) {
	if resource == nil {
		return
	}
	s.ensureInit()
	s.mu.Lock()
	cache := s.motionResourceStoreForLocked(manager)
	for _, alias := range motionResourcePathAliases(resource.path) {
		cache.modules[alias] = resource
	}
	known := false
	for _, candidate := range cache.order {
		if candidate == resource {
			known = true
			break
		}
	}
	if !known {
		cache.order = append(cache.order, resource)
	}
	cache.lastLoaded = resource
	cache.lastPath = resource.path
	s.mu.Unlock()
}

func (s *Services) rememberMotionResourceCachePath(manager tjs.ObjectID, resource *motionResource, path string) {
	if resource == nil || path == "" {
		return
	}
	s.ensureInit()
	s.mu.Lock()
	cache := s.motionResourceStoreForLocked(manager)
	if cache.cachePaths == nil {
		cache.cachePaths = make(map[*motionResource]map[string]struct{})
	}
	paths := cache.cachePaths[resource]
	if paths == nil {
		paths = make(map[string]struct{})
		cache.cachePaths[resource] = paths
	}
	paths[path] = struct{}{}
	s.mu.Unlock()
}

func (s *Services) motionResourceCachePaths(manager tjs.ObjectID, resource *motionResource) []string {
	if resource == nil {
		return nil
	}
	s.ensureInit()
	s.mu.RLock()
	cache := s.MotionResources[manager]
	var paths []string
	if cache != nil {
		for path := range cache.cachePaths[resource] {
			paths = append(paths, path)
		}
	}
	s.mu.RUnlock()
	return paths
}

// uniqueMotionResources returns the distinct modules a manager holds in load
// order. The reference derives its play mode from uniqueCachedModules()
// (EmotePlayer.cpp:676): one motion module selects SingleCache, several select
// MultiCache.
func (s *Services) uniqueMotionResources(manager tjs.ObjectID) []*motionResource {
	s.ensureInit()
	s.mu.RLock()
	defer s.mu.RUnlock()
	cache := s.MotionResources[manager]
	if cache == nil {
		return nil
	}
	return append([]*motionResource(nil), cache.order...)
}

// unloadMotionResource drops every alias that points at the module addressed by
// path, matching collectModuleCacheKeys + unregisterModuleSnapshot
// (ResourceManager.cpp:59-75,172-204).
func (s *Services) unloadMotionResource(manager tjs.ObjectID, path string) bool {
	s.ensureInit()
	s.mu.Lock()
	defer s.mu.Unlock()
	cache := s.MotionResources[manager]
	if cache == nil {
		return false
	}
	var target *motionResource
	for _, alias := range motionResourcePathAliases(path) {
		if candidate := cache.modules[alias]; candidate != nil {
			target = candidate
			break
		}
	}
	if target == nil {
		return false
	}
	for alias, candidate := range cache.modules {
		if candidate == target {
			delete(cache.modules, alias)
		}
	}
	for index, candidate := range cache.order {
		if candidate == target {
			cache.order = append(cache.order[:index], cache.order[index+1:]...)
			break
		}
	}
	if cache.lastLoaded == target {
		cache.lastLoaded = nil
		cache.lastPath = ""
	}
	for id, module := range s.MotionModules {
		if module == target {
			delete(s.MotionModules, id)
		}
	}
	delete(cache.cachePaths, target)
	return true
}

// clearMotionResourceCache releases every module and layer-id mapping the
// manager held (ResourceManager.cpp:207-220).
func (s *Services) clearMotionResourceCache(manager tjs.ObjectID) {
	s.ensureInit()
	s.mu.Lock()
	cache := s.MotionResources[manager]
	if cache != nil {
		owned := make(map[*motionResource]struct{}, len(cache.order))
		for _, resource := range cache.order {
			owned[resource] = struct{}{}
		}
		for id, resource := range s.MotionModules {
			if _, ok := owned[resource]; ok {
				delete(s.MotionModules, id)
			}
		}
		delete(s.MotionResources, manager)
	}
	s.mu.Unlock()
}

func (s *Services) lastMotionResource(manager tjs.ObjectID) *motionResource {
	s.ensureInit()
	s.mu.RLock()
	defer s.mu.RUnlock()
	cache := s.MotionResources[manager]
	if cache == nil {
		return nil
	}
	return cache.lastLoaded
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
		state = &motionPlayerState{speed: 1, zFactor: 1, zFactorSet: true, drawMatrix: identityMotionDrawMatrix()}
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
	case "zFactor":
		return tjs.RealValue(motionPlayerZFactor(state)), nil
	case "playing", "allplaying":
		if state.playing {
			return tjs.IntegerValue(1), nil
		}
		return tjs.IntegerValue(0), nil
	case "tags":
		return vm.NewArray(nil)
	case "completionType":
		return tjs.IntegerValue(state.completionType), nil
	case "maskMode":
		return tjs.IntegerValue(state.maskMode), nil
	case "outline":
		return tjs.StringValue(state.outline), nil
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
	case "zFactor":
		n, err := value.Real()
		if err != nil {
			return err
		}
		state.zFactor = n
		state.zFactorSet = true
	case "completionType":
		n, err := value.Int()
		if err != nil {
			return err
		}
		state.completionType = n
	case "maskMode":
		n, err := value.Int()
		if err != nil {
			return err
		}
		state.maskMode = n
	case "outline":
		state.outline = value.Text()
	case "onAction":
		state.onAction = value
	default:
		return fmt.Errorf("unsupported Motion.Player property %q", property)
	}
	return nil
}

func (s *Services) playerResource(state *motionPlayerState) *motionResource {
	if state.resource != nil {
		return state.resource
	}
	s.ensureInit()
	s.mu.RLock()
	cache := s.MotionResources[state.resourceManager]
	s.mu.RUnlock()
	if cache == nil {
		return nil
	}
	if len(cache.order) == 1 {
		return cache.order[0]
	}
	// Motion.Player historically used whatever module the manager loaded last;
	// keep that fallback only for players that did not bind an explicit module,
	// while EmotePlayer always pins its own resource.
	return cache.lastLoaded
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

func motionPlayerZFactor(state *motionPlayerState) float64 {
	if state == nil || (!state.zFactorSet && state.zFactor == 0) {
		return 1
	}
	return state.zFactor
}

func (s *Services) drawMotionTarget(vm *tjs.VM, state *motionPlayerState, value tjs.Value) error {
	resource := s.playerResource(state)
	if resource == nil {
		return fmt.Errorf("Motion.Player.draw has no loaded PSB resource")
	}
	layer, err := s.motionLayerFromValue(vm, value)
	if err != nil {
		return err
	}
	// EmotePlayer carries coord/scale/rot/mirror state the shared player does
	// not; compose it with the explicit draw matrix so Motion.Player (emote ==
	// nil) keeps using state.drawMatrix unchanged.
	drawMatrix := state.drawMatrix
	var params []float64
	var attached []*motionResource
	maskMode := state.maskMode
	freezeBody := false
	if state.emote != nil {
		drawMatrix = state.emote.drawMatrixFor(drawMatrix)
		params = state.emote.renderParameters()
		attached = state.attached
		maskMode = state.emote.maskMode
		freezeBody = state.emote.freezeBodyMotion()
	}
	if s.gpuCapture && layerHasCanvas(layer) && layer.Type == LayerAlpha {
		key := motionPlayerGPUKey{
			resource: resource, chara: state.chara, motion: state.motion,
			tick: state.frameTick, matrix: drawMatrix,
			zFactor: motionPlayerZFactor(state),
			scale:   layer.RenderScale(), maskMode: maskMode,
			completion: state.completionType, freezeBody: freezeBody,
			variables: motionVariablesSignature(params) + "\x00" + motionAttachmentsSignature(attached),
		}
		motion := state.gpuMotion
		if !state.gpuMotionValid || state.gpuMotionKey != key {
			var err error
			motion, err = buildGPUMotionVariablesZFactor(resource, state.chara, state.motion, state.frameTick, 0, drawMatrix, layer.RenderScale(), params, freezeBody, maskMode, state.completionType, motionPlayerZFactor(state), attached)
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
	frame, err := renderPSBMotionOptionsZFactor(resource, state.chara, state.motion, state.frameTick, 0, params, freezeBody, maskMode, state.completionType, motionPlayerZFactor(state), attached)
	if err != nil {
		return err
	}
	// Camera nodes offset prepared items in final draw space, so the offset
	// joins the draw matrix translation (PlayerRenderItems.cpp:1174-1215).
	baseDrawMatrix := drawMatrix
	drawMatrix = motionCameraDrawMatrixZFactor(resource, state.chara, state.motion, state.frameTick, params, freezeBody, maskMode, state.completionType, motionPlayerZFactor(state), attached, drawMatrix)
	cameraOffsetX := drawMatrix.tx - baseDrawMatrix.tx
	cameraOffsetY := drawMatrix.ty - baseDrawMatrix.ty
	s.mu.Lock()
	defer s.mu.Unlock()
	layer.gpuMotion = nil
	// A motion layer draws into the canvas its owner layer already owns, so the
	// layer keeps the size its KAG definition assigned. Only a layer without a
	// bitmap adopts the motion canvas size.
	if !layerHasCanvas(layer) {
		if cameraOffsetX != 0 || cameraOffsetY != 0 {
			// This fallback adopts the canvas verbatim, so the camera offset is
			// applied as an integer canvas translation rather than through a
			// draw affine.
			frame = motionTranslateCanvas(frame, int(cameraOffsetX), int(cameraOffsetY))
		}
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
	drawMotionCanvasInto(layer, frame, drawMatrix, 0)
	layer.Visible = true
	layer.imageModified = true
	layer.markImageDirty()
	return nil
}

func layerHasCanvas(layer *Layer) bool {
	return layer.validImage()
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
