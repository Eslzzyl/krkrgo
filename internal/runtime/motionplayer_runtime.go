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
	attached                 []*motionResource
	emote                    *emotePlayerState
	metadataResource         *motionResource
	chara                    string
	motion                   string
	x                        float64
	y                        float64
	stealthMotion            string
	frameTick                float64
	frameLastTime            float64
	frameLoopTime            float64
	loopTime                 float64
	loopTimeSet              bool
	speed                    bool
	speedSet                 bool
	zFactor                  float64
	zFactorSet               bool
	playing                  bool
	metadata                 tjs.Value
	priorDraw                float64
	processedMeshVerticesNum int64
	queuing                  bool
	directEdit               bool
	selectorEnabled          bool
	syncWaiting              bool
	syncActive               bool
	hasCamera                bool
	cameraActive             bool
	stereovisionActive       bool
	cameraAlive              bool
	cameraTarget             tjs.Value
	cameraPosition           tjs.Value
	cameraFOV                float64
	cameraOffsetX            float64
	cameraOffsetY            float64
	canvasCaptureEnabled     bool
	clearEnabled             bool
	d3dDrawMode              bool
	hitThreshold             float64
	preview                  bool
	outsideFactor            float64
	stealthChara             string
	project                  tjs.Value
	meshline                 string
	busy                     bool
	colorWeight              uint32
	independentLayerInherit  bool
	// held marks a one-shot motion that reached its last time: the reference
	// stops advancing the timeline at that point and keeps the evaluated pose,
	// and the layer keeps drawing the clamped frame. A repeat apply of the same
	// motion keeps the held pose; an explicit stop clears the mark so a later
	// apply starts the motion again.
	held           bool
	completionType int64
	maskMode       int64
	outline        string
	onAction       tjs.Value
	tags           tjs.Value
	variableKeys   tjs.Value
	drawMatrix     motionDrawMatrix
	playerID       tjs.ObjectID
	gpuMotion      *RenderMotion
	gpuMotionValid bool
	gpuMotionKey   motionPlayerGPUKey
}

type motionPlayerGPUKey struct {
	resource                *motionResource
	chara, motion           string
	tick                    float64
	zFactor                 float64
	matrix                  motionDrawMatrix
	scale                   int32
	maskMode                int64
	completion              int64
	freezeBody              bool
	variables               string
	independentLayerInherit bool
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
	modules        map[string]*motionResource
	order          []*motionResource
	cachePaths     map[*motionResource]map[string]struct{}
	lastLoaded     *motionResource
	lastPath       string
	usedLayerIDs   map[int64]struct{}
	layerIDsByName map[string]int64
	layerNamesByID map[int64]string
	nextLayerID    int64
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
		cache = &motionResourceStore{
			modules:        make(map[string]*motionResource),
			usedLayerIDs:   make(map[int64]struct{}),
			layerIDsByName: make(map[string]int64),
			layerNamesByID: make(map[int64]string),
			nextLayerID:    1,
		}
		s.MotionResources[manager] = cache
	}
	return cache
}

func (s *Services) requireMotionLayerID(manager tjs.ObjectID, name string, named bool) int64 {
	s.ensureInit()
	s.mu.Lock()
	defer s.mu.Unlock()
	cache := s.motionResourceStoreForLocked(manager)
	if named {
		if id := cache.layerIDsByName[name]; id != 0 {
			return id
		}
	}
	if cache.nextLayerID < 1 {
		cache.nextLayerID = 1
	}
	for {
		if _, used := cache.usedLayerIDs[cache.nextLayerID]; !used {
			break
		}
		cache.nextLayerID++
	}
	id := cache.nextLayerID
	cache.nextLayerID++
	cache.usedLayerIDs[id] = struct{}{}
	if named {
		cache.layerIDsByName[name] = id
		cache.layerNamesByID[id] = name
	}
	return id
}

func (s *Services) releaseMotionLayerID(manager tjs.ObjectID, id int64) {
	if id == 0 {
		return
	}
	s.ensureInit()
	s.mu.Lock()
	defer s.mu.Unlock()
	cache := s.MotionResources[manager]
	if cache == nil {
		return
	}
	delete(cache.usedLayerIDs, id)
	if name, found := cache.layerNamesByID[id]; found {
		delete(cache.layerIDsByName, name)
		delete(cache.layerNamesByID, id)
	}
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
	case "initPhysics":
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
		property := strings.TrimPrefix(operation, "set:")
		if property == "motion" || property == "motionKey" {
			return tjs.VoidValue, s.motionPlayerSetMotion(vm, context, state, args[0])
		}
		return tjs.VoidValue, s.motionPlayerSet(state, property, args[0])
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
		if state.resource == nil && state.project.Kind() == tjs.KindObject && !state.project.IsNull() {
			s.mu.RLock()
			state.resource = s.MotionModules[state.project.ObjectRef().Object]
			s.mu.RUnlock()
			if state.resource != nil {
				if state.chara == "" {
					state.chara = motionResourceMetadataString(state.resource, "chara")
					if state.chara == "" {
						state.chara = motionResourceDefaultChara(state.resource)
					}
				}
			}
		}
		if motion == "" {
			// Empty request restarts the loaded motion from its origin unless
			// the chain flag keeps the current position.
			if flags&motionPlayFlagChain == 0 {
				state.frameTick = 0
				state.frameLoopTime = 0
				state.queuing = true
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
		state.loopTimeSet = false
		if flags&motionPlayFlagChain == 0 {
			state.frameTick = 0
			state.frameLoopTime = 0
			state.queuing = true
		}
		state.playing = true
		state.held = false
		emote := s.motionPlayerEmote(state)
		s.emoteStartMotion(state, emote, motion, flags)
		motionTrace("play id=%d chara=%q motion=%q flags=%d", id, state.chara, state.motion, flags)
		return tjs.VoidValue, nil
	case "stop":
		state.playing = false
		state.held = false
		return tjs.IntegerValue(1), nil
	case "isPlaying":
		playing := state.emote != nil && len(state.emote.playingOrder) > 0
		state.playing = playing
		return tjs.IntegerValue(boolInt(playing)), nil
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
		// Stopped players with active timeline labels continue timeline progress;
		// a stopped player with no active labels keeps its evaluated pose.
		if !state.playing && (state.emote == nil || len(state.emote.playingOrder) == 0) {
			return tjs.VoidValue, nil
		}
		if !motionPlayerSpeed(state) {
			return tjs.VoidValue, nil
		}
		frames := float64(interval) * motionFramesPerMillisecond
		state.frameLastTime = frames
		emote := s.motionPlayerEmote(state)
		if emote.freezeBodyMotion() {
			if frames > 0 {
				state.frameLoopTime += frames
				emote.frameLastTime = frames
				emote.frameLoopTime += frames
				emote.loopTime += frames
				s.emoteAdvanceTimelines(emote, frames)
			}
			s.emoteApplyEvalPostProcess(emote)
			state.playing = len(emote.playingOrder) > 0
			state.syncActive = state.syncWaiting && state.playing
			return tjs.VoidValue, nil
		}
		if state.queuing {
			state.queuing = false
			state.syncActive = state.syncWaiting && state.playing
			return tjs.VoidValue, nil
		}
		if frames > 0 {
			emote.evalResults = make(map[string]float64)
			emote.evalOrder = nil
			s.emoteAdvanceTimelines(emote, frames)
			s.emoteStepControllerBuckets(emote, frames)
		}
		s.emoteApplyEvalPostProcess(emote)
		state.playing = len(emote.playingOrder) > 0
		state.syncActive = state.syncWaiting && state.playing
		state.frameLoopTime += frames
		state.frameTick += frames
		if resource := s.playerResource(state); resource != nil {
			last := motionLastTime(resource, state.chara, state.motion)
			if last >= 0 && state.frameTick >= last {
				advanceMotionClock(state, last, motionPlayerLoopTime(state, resource))
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
	case "getCameraOffset":
		return state.cameraPosition, nil
	case "setCameraOffset":
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("Motion.Player.setCameraOffset requires a point")
		}
		state.cameraPosition = args[0]
		if args[0].Kind() == tjs.KindObject && !args[0].IsNull() {
			if x, found, err := vm.GetMemberIfPresent(args[0], "x", s, nil); err == nil && found {
				if n, err := x.Real(); err == nil {
					state.cameraOffsetX = float64(float32(n))
				}
			}
			if y, found, err := vm.GetMemberIfPresent(args[0], "y", s, nil); err == nil && found {
				if n, err := y.Real(); err == nil {
					state.cameraOffsetY = float64(float32(n))
				}
			}
		}
		return tjs.VoidValue, nil
	case "setStereovisionCameraPosition":
		if len(args) < 3 {
			return tjs.VoidValue, fmt.Errorf("Motion.Player.setStereovisionCameraPosition requires x, y, z")
		}
		values := make([]tjs.Value, 3)
		for i := range 3 {
			n, err := args[i].Real()
			if err != nil {
				return tjs.VoidValue, err
			}
			values[i] = tjs.RealValue(n)
		}
		position, err := vm.NewArray(values)
		state.cameraPosition = position
		return tjs.VoidValue, err
	case "modifyRoot":
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("Motion.Player.modifyRoot requires data")
		}
		state.project = args[0]
		return tjs.VoidValue, nil
	case "skipToSync":
		if resource := s.playerResource(state); resource != nil {
			if last := motionLastTime(resource, state.chara, state.motion); last >= 0 {
				state.frameTick = last
			}
		}
		return tjs.VoidValue, nil
	case "requireLayerId":
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("Motion.Player.requireLayerId requires a layer name")
		}
		return tjs.IntegerValue(s.requireMotionLayerID(state.resourceManager, args[0].Text(), true)), nil
	case "releaseLayerId":
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("Motion.Player.releaseLayerId requires an id")
		}
		id, err := args[0].Int()
		if err != nil {
			return tjs.VoidValue, err
		}
		s.releaseMotionLayerID(state.resourceManager, id)
		return tjs.VoidValue, nil
	case "isExistMotion":
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("Motion.Player.isExistMotion requires a motion key")
		}
		resource := s.motionResource(args[0].Text(), state.resourceManager)
		return tjs.IntegerValue(boolInt(resource != nil)), nil
	case "findMotion":
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("Motion.Player.findMotion requires a motion key")
		}
		resource := s.motionResource(args[0].Text(), state.resourceManager)
		if resource == nil {
			return tjs.VoidValue, nil
		}
		module, found, err := s.motionModuleValueForResource(vm, state.resourceManager, resource)
		if err != nil {
			return tjs.VoidValue, err
		}
		if !found {
			return tjs.VoidValue, nil
		}
		state.resource = resource
		state.metadataResource = nil
		s.motionPlayerEmote(state)
		return module, nil
	case "setVariable":
		if len(args) < 2 {
			return tjs.VoidValue, fmt.Errorf("Motion.Player.setVariable requires name and value")
		}
		value, err := args[1].Real()
		if err != nil {
			return tjs.VoidValue, err
		}
		mode := int64(0)
		if len(args) > 2 && args[2].Kind() != tjs.KindVoid {
			mode, err = args[2].Int()
			if err != nil {
				return tjs.VoidValue, err
			}
		}
		emote := s.motionPlayerEmote(state)
		s.emoteWriteEvalMode(emote, args[0].Text(), mode, value)
		return tjs.VoidValue, nil
	case "getVariable":
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("Motion.Player.getVariable requires a label")
		}
		return tjs.RealValue(emoteVariableValue(s.motionPlayerEmote(state), args[0].Text())), nil
	case "countVariables", "getVariableLabelAt", "countVariableFrameAt",
		"getVariableFrameLabelAt", "getVariableFrameValueAt", "getVariableRange",
		"getVariableFrameList", "getTimelinePlaying", "countMainTimelines",
		"getMainTimelineLabelAt", "getMainTimelineLabelList", "countDiffTimelines",
		"getDiffTimelineLabelAt", "getDiffTimelineLabelList", "getLoopTimeline",
		"countPlayingTimelines", "getPlayingTimelineLabelAt",
		"getPlayingTimelineFlagsAt", "getTimelineTotalFrameCount", "playTimeline",
		"stopTimeline", "setTimelineBlendRatio", "getTimelineBlendRatio",
		"fadeInTimeline", "fadeOutTimeline", "getPlayingTimelineInfoList":
		emote := s.motionPlayerEmote(state)
		result, err := s.motionEmotePlayerNative(vm, context, operation, args)
		if err != nil {
			return result, err
		}
		if operation == "playTimeline" || operation == "stopTimeline" {
			state.playing = len(emote.playingOrder) > 0
			state.syncActive = state.syncWaiting && state.playing
		}
		return result, nil
	default:
		return tjs.VoidValue, fmt.Errorf("unsupported Motion.Player operation %q", operation)
	}
}

func (s *Services) motionPlayerSetMotion(vm *tjs.VM, context tjs.Value, state *motionPlayerState, value tjs.Value) error {
	motion := value.Text()
	if state.motion == motion {
		return nil
	}
	request, err := vm.NewDictionary()
	if err != nil {
		return err
	}
	if err := tjs.SetMember(vm.Objects, request, "chara", tjs.StringValue(state.chara)); err != nil {
		return err
	}
	if err := tjs.SetMember(vm.Objects, request, "motion", tjs.StringValue(motion)); err != nil {
		return err
	}
	handler, found, err := vm.GetMemberIfPresent(context, "onFindMotion", s, nil)
	if err == nil && found && !handler.IsVoid() {
		result, callErr := vm.CallFunction(handler, context, []tjs.Value{request}, s, nil)
		if callErr == nil && result.Kind() == tjs.KindObject && !result.IsNull() {
			if chara, present, getErr := vm.GetMemberIfPresent(result, "chara", s, nil); getErr == nil && present && !chara.IsVoid() {
				state.chara = chara.Text()
			}
			if resolved, present, getErr := vm.GetMemberIfPresent(result, "motion", s, nil); getErr == nil && present && !resolved.IsVoid() {
				motion = resolved.Text()
			}
		}
	}
	state.motion = motion
	state.frameTick = 0
	state.frameLastTime = 0
	state.frameLoopTime = 0
	state.loopTimeSet = false
	state.playing = motion != ""
	state.held = false
	state.queuing = motion != ""
	state.drawMatrix = identityMotionDrawMatrix()
	state.metadataResource = nil
	if state.emote != nil {
		s.emoteResetRuntime(state.emote)
	}
	state.variableKeys, err = vm.NewArray(nil)
	return err
}

func (s *Services) motionPlayer(id tjs.ObjectID) *motionPlayerState {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.MotionPlayers[id]
	if state == nil {
		state = &motionPlayerState{speed: true, speedSet: true, zFactor: 1, zFactorSet: true, cameraFOV: 60, colorWeight: 0xFF808080, drawMatrix: identityMotionDrawMatrix()}
		s.MotionPlayers[id] = state
	}
	return state
}

func (s *Services) motionPlayerGet(vm *tjs.VM, state *motionPlayerState, property string) (tjs.Value, error) {
	switch property {
	case "chara":
		return tjs.StringValue(state.chara), nil
	case "motion", "motionKey":
		return tjs.StringValue(state.motion), nil
	case "x", "left":
		return tjs.RealValue(state.x), nil
	case "y", "top":
		return tjs.RealValue(state.y), nil
	case "metadata":
		return state.metadata, nil
	case "stealthMotion":
		return tjs.StringValue(state.stealthMotion), nil
	case "tickCount":
		// Reference Player.h: the TJS property converts the frame clock back to
		// milliseconds and clamps non-positive clocks to zero.
		if state.frameTick > 0 {
			return tjs.RealValue(state.frameTick * 1000 / 60), nil
		}
		return tjs.RealValue(0), nil
	case "frameTickCount":
		return tjs.RealValue(state.frameTick), nil
	case "frameLastTime":
		return tjs.RealValue(state.frameLastTime), nil
	case "frameLoopTime":
		return tjs.RealValue(state.frameLoopTime), nil
	case "priorDraw":
		return tjs.RealValue(state.priorDraw), nil
	case "processedMeshVerticesNum":
		return tjs.IntegerValue(state.processedMeshVerticesNum), nil
	case "queuing":
		return tjs.IntegerValue(boolInt(state.queuing)), nil
	case "directEdit":
		return tjs.IntegerValue(boolInt(state.directEdit)), nil
	case "selectorEnabled":
		return tjs.IntegerValue(boolInt(state.selectorEnabled)), nil
	case "syncWaiting":
		return tjs.IntegerValue(boolInt(state.syncWaiting)), nil
	case "syncActive":
		return tjs.IntegerValue(boolInt(state.syncActive)), nil
	case "hasCamera":
		return tjs.IntegerValue(boolInt(state.hasCamera)), nil
	case "cameraActive":
		return tjs.IntegerValue(boolInt(state.cameraActive)), nil
	case "stereovisionActive":
		return tjs.IntegerValue(boolInt(state.stereovisionActive)), nil
	case "cameraTarget":
		return state.cameraTarget, nil
	case "cameraPosition":
		return state.cameraPosition, nil
	case "cameraFOV":
		return tjs.RealValue(state.cameraFOV), nil
	case "cameraAlive":
		return tjs.IntegerValue(boolInt(state.cameraAlive)), nil
	case "canvasCaptureEnabled":
		return tjs.IntegerValue(boolInt(state.canvasCaptureEnabled)), nil
	case "clearEnabled":
		return tjs.IntegerValue(boolInt(state.clearEnabled)), nil
	case "hitThreshold":
		return tjs.RealValue(state.hitThreshold), nil
	case "preview":
		return tjs.IntegerValue(boolInt(state.preview)), nil
	case "outsideFactor":
		return tjs.RealValue(state.outsideFactor), nil
	case "resourceManager":
		if state.resourceManager == 0 {
			return tjs.NullValue, nil
		}
		return tjs.ObjectIDValue(state.resourceManager), nil
	case "stealthChara":
		return tjs.StringValue(state.stealthChara), nil
	case "project":
		return state.project, nil
	case "useD3D":
		return tjs.IntegerValue(boolInt(state.d3dDrawMode)), nil
	case "meshline":
		return tjs.StringValue(state.meshline), nil
	case "busy":
		return tjs.IntegerValue(boolInt(state.busy)), nil
	case "loopTime":
		if state.loopTimeSet {
			return tjs.RealValue(state.loopTime), nil
		}
		if resource := s.playerResource(state); resource != nil {
			return tjs.RealValue(motionLoopTime(resource, state.chara, state.motion)), nil
		}
		return tjs.RealValue(0), nil
	case "speed":
		if motionPlayerSpeed(state) {
			return tjs.IntegerValue(1), nil
		}
		return tjs.IntegerValue(0), nil
	case "zFactor":
		return tjs.RealValue(motionPlayerZFactor(state)), nil
	case "playing", "allplaying":
		if state.playing {
			return tjs.IntegerValue(1), nil
		}
		return tjs.IntegerValue(0), nil
	case "tags":
		if state.tags.IsVoid() {
			return vm.NewArray(nil)
		}
		return state.tags, nil
	case "variableKeys":
		if state.variableKeys.IsVoid() {
			return vm.NewArray(nil)
		}
		return state.variableKeys, nil
	case "completionType":
		return tjs.IntegerValue(state.completionType), nil
	case "maskMode":
		return tjs.IntegerValue(state.maskMode), nil
	case "colorWeight":
		return tjs.IntegerValue(int64(motionSwapPackedRB(state.colorWeight))), nil
	case "independentLayerInherit":
		return tjs.IntegerValue(boolInt(state.independentLayerInherit)), nil
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
	case "x", "left":
		n, err := value.Real()
		if err != nil {
			return err
		}
		state.x = n
	case "y", "top":
		n, err := value.Real()
		if err != nil {
			return err
		}
		state.y = n
	case "stealthMotion":
		state.stealthMotion = value.Text()
	case "motion", "motionKey":
		motion := value.Text()
		if state.motion == motion {
			return nil
		}
		// Reference Player::setMotion starts the requested motion through the
		// load path; a repeat of the loaded key only reselects it.
		state.motion = motion
		state.frameTick = 0
		state.loopTimeSet = false
		state.playing = motion != ""
		state.held = false
	case "metadata":
		state.metadata = value
	case "tickCount":
		n, err := value.Real()
		if err != nil {
			return err
		}
		state.frameTick = n * motionFramesPerMillisecond
		// An explicit seek moves the pose, so a later repeat of the same
		// motion starts over instead of holding.
		state.held = false
	case "frameTickCount":
		n, err := value.Real()
		if err != nil {
			return err
		}
		state.frameTick = n
		state.held = false
	case "frameLastTime":
		n, err := value.Real()
		if err != nil {
			return err
		}
		state.frameLastTime = n
	case "frameLoopTime":
		n, err := value.Real()
		if err != nil {
			return err
		}
		state.frameLoopTime = n
	case "priorDraw":
		n, err := value.Real()
		if err != nil {
			return err
		}
		state.priorDraw = n
	case "processedMeshVerticesNum":
		n, err := value.Int()
		if err != nil {
			return err
		}
		state.processedMeshVerticesNum = n
	case "queuing":
		state.queuing = motionPlayerBool(value)
	case "directEdit":
		state.directEdit = motionPlayerBool(value)
	case "selectorEnabled":
		state.selectorEnabled = motionPlayerBool(value)
	case "syncWaiting":
		state.syncWaiting = motionPlayerBool(value)
	case "syncActive":
		state.syncActive = motionPlayerBool(value)
	case "hasCamera":
		state.hasCamera = motionPlayerBool(value)
	case "cameraActive":
		state.cameraActive = motionPlayerBool(value)
	case "stereovisionActive":
		state.stereovisionActive = motionPlayerBool(value)
	case "cameraTarget":
		state.cameraTarget = value
	case "cameraPosition":
		state.cameraPosition = value
	case "cameraFOV":
		n, err := value.Real()
		if err != nil {
			return err
		}
		state.cameraFOV = n
	case "cameraAlive":
		state.cameraAlive = motionPlayerBool(value)
	case "canvasCaptureEnabled":
		state.canvasCaptureEnabled = motionPlayerBool(value)
	case "clearEnabled":
		state.clearEnabled = motionPlayerBool(value)
	case "hitThreshold":
		n, err := value.Real()
		if err != nil {
			return err
		}
		state.hitThreshold = n
	case "preview":
		state.preview = motionPlayerBool(value)
	case "outsideFactor":
		n, err := value.Real()
		if err != nil {
			return err
		}
		state.outsideFactor = n
	case "resourceManager":
		if value.Kind() == tjs.KindObject && !value.IsNull() {
			state.resourceManager = value.ObjectRef().Object
		} else {
			state.resourceManager = 0
		}
	case "stealthChara":
		state.stealthChara = value.Text()
	case "project":
		state.project = value
	case "useD3D":
		state.d3dDrawMode = motionPlayerBool(value)
	case "meshline":
		state.meshline = value.Text()
	case "loopTime":
		n, err := value.Real()
		if err != nil {
			return err
		}
		state.loopTime = n
		state.loopTimeSet = true
	case "speed":
		n, err := value.Real()
		if err != nil {
			return err
		}
		state.speed = n != 0
		state.speedSet = true
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
	case "colorWeight":
		n, err := value.Int()
		if err != nil {
			return err
		}
		state.colorWeight = motionSwapPackedRB(uint32(n))
	case "independentLayerInherit":
		state.independentLayerInherit = motionPlayerBool(value)
	case "outline":
		state.outline = value.Text()
	case "onAction":
		state.onAction = value
	case "tags":
		state.tags = value
	case "variableKeys":
		state.variableKeys = value
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

func (s *Services) motionPlayerEmote(state *motionPlayerState) *emotePlayerState {
	if state.emote == nil {
		state.emote = newEmotePlayerState()
	}
	resource := s.playerResource(state)
	if resource != nil && state.metadataResource != resource {
		s.emoteResetRuntime(state.emote)
		s.emoteApplyResourceMetadata(state.emote, resource)
		state.metadataResource = resource
	}
	return state.emote
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

func motionPlayerLoopTime(state *motionPlayerState, resource *motionResource) float64 {
	if state == nil {
		return -1
	}
	if state.loopTimeSet {
		return state.loopTime
	}
	return motionLoopTime(resource, state.chara, state.motion)
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

func motionPlayerSpeed(state *motionPlayerState) bool {
	return state == nil || (!state.speedSet && !state.speed) || state.speed
}

func motionPlayerBool(value tjs.Value) bool {
	n, err := value.Int()
	return err == nil && n != 0
}

func motionSwapPackedRB(color uint32) uint32 {
	return color&0xFF00FF00 | (color>>16)&0xFF | (color&0xFF)<<16
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
	drawMatrix.tx += drawMatrix.a*state.x + drawMatrix.b*state.y
	drawMatrix.ty += drawMatrix.c*state.x + drawMatrix.d*state.y
	drawMatrix.tx += state.cameraOffsetX
	drawMatrix.ty += state.cameraOffsetY
	if s.gpuCapture && layerHasCanvas(layer) && layer.Type == LayerAlpha {
		key := motionPlayerGPUKey{
			resource: resource, chara: state.chara, motion: state.motion,
			tick: state.frameTick, matrix: drawMatrix,
			zFactor: motionPlayerZFactor(state),
			scale:   layer.RenderScale(), maskMode: maskMode,
			completion: state.completionType, freezeBody: freezeBody,
			independentLayerInherit: state.independentLayerInherit,
			variables:               motionVariablesSignature(params) + "\x00" + motionAttachmentsSignature(attached),
		}
		motion := state.gpuMotion
		if !state.gpuMotionValid || state.gpuMotionKey != key {
			var err error
			motion, err = buildGPUMotionVariablesZFactorInherit(resource, state.chara, state.motion, state.frameTick, 0, drawMatrix, layer.RenderScale(), params, freezeBody, maskMode, state.completionType, motionPlayerZFactor(state), attached, state.independentLayerInherit)
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
	frame, err := renderPSBMotionOptionsZFactorInherit(resource, state.chara, state.motion, state.frameTick, 0, params, freezeBody, maskMode, state.completionType, motionPlayerZFactor(state), attached, state.independentLayerInherit)
	if err != nil {
		return err
	}
	// Camera nodes offset prepared items in final draw space, so the offset
	// joins the draw matrix translation (PlayerRenderItems.cpp:1174-1215).
	baseDrawMatrix := drawMatrix
	drawMatrix = motionCameraDrawMatrixZFactorInherit(resource, state.chara, state.motion, state.frameTick, params, freezeBody, maskMode, state.completionType, motionPlayerZFactor(state), attached, drawMatrix, state.independentLayerInherit)
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
