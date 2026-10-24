package runtime

import (
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
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
// motionplayer/main.cpp). The reference reloads a same-key motion when Force
// or AsCan is present.
const (
	motionPlayFlagForce   = 1
	motionPlayFlagChain   = 2
	motionPlayFlagAsCan   = 4
	motionPlayFlagJoin    = 8
	motionPlayFlagStealth = 16
)

type motionPlayerState struct {
	resourceManager      tjs.ObjectID
	resourceManagerValue tjs.Value
	// resource is the PSB module a player bound explicitly (EmotePlayer's
	// motionKey/module path). When set it takes precedence over the
	// resource-manager lookup so playback never depends on whichever module the
	// manager loaded last.
	resource *motionResource
	// motionsByKey mirrors PlayerRuntime::motionsByKey. ResourceManager keeps
	// the shared module cache; this map keeps the per-Player lookup keys.
	motionsByKey map[string]*motionResource
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
	clampedEvalTime          float64
	frameLastTime            float64
	frameLoopTime            float64
	loopTime                 float64
	loopTimeSet              bool
	speed                    bool
	speedSet                 bool
	zFactor                  float64
	zFactorSet               bool
	playing                  bool
	motionUnloaded           bool
	motionTreeGeneration     uint64
	motionClockActive        bool
	playerTimelineResource   *motionResource
	playerTimelineLoaded     bool
	playerTimelines          map[string]*emoteTimelineState
	playerTimelineOrder      []string
	playerMainTimelines      []string
	playerDiffTimelines      []string
	playingTimelineLabels    []string
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
	hairScale                float64
	partsScale               float64
	bustScale                float64
	rotateAngle              float64
	emoteScale               float64
	rootFlipX                bool
	mirrorEvalEnabled        bool
	viewFlip                 bool
	viewOpacity              float64
	viewVisible              bool
	viewSlant                float64
	viewZoom                 float64
	lastViewParam            tjs.Value
	lastCanvas               tjs.Value
	clearColor               int64
	resizable                bool
	width, height            int64
	alphaOpCounter           int64
	backgrounds              []tjs.Value
	captions                 []tjs.Value
	// held marks a one-shot motion that reached its last time: the reference
	// stops advancing the timeline at that point and keeps the evaluated pose,
	// and the layer keeps drawing the clamped frame. A repeat apply of the same
	// motion keeps the held pose; an explicit stop clears the mark so a later
	// apply starts the motion again.
	held                    bool
	completionType          int64
	maskMode                int64
	rootCoordinateMode      int
	rootState               motionPlayerRootState
	hasRootState            bool
	outline                 string
	onAction                tjs.Value
	tags                    tjs.Value
	variableKeys            tjs.Value
	drawMatrix              motionDrawMatrix
	playerID                tjs.ObjectID
	particleArrays          map[int]tjs.Value
	motionChildren          map[int]tjs.Value
	particleChildren        map[int]map[string]tjs.Value
	anchorStates            map[string]motionAnchorNodeState
	sourceCache             map[string]tjs.Value
	sourceCacheResolved     map[string]string
	randomGenerator         tjs.Value
	disabledSelectorTargets map[string]bool
	gpuMotion               *RenderMotion
	gpuMotionScratch        *RenderMotion
	gpuMotionValid          bool
	gpuMotionKey            motionPlayerGPUKey
}

type motionPlayerGPUKey struct {
	resource                *motionResource
	chara, motion           string
	tick                    float64
	parentFrameLoopTime     float64
	hasParentFrameLoopTime  bool
	zFactor                 float64
	rootCoordinateMode      int
	rootState               motionPlayerRootState
	hasRootState            bool
	matrix                  motionDrawMatrix
	scale                   int32
	maskMode                int64
	completion              int64
	freezeBody              bool
	variables               string
	independentLayerInherit bool
	preview                 bool
}

type motionPlayerRootState struct {
	x, y, z            float64
	matrix, rootMatrix motionMatrix
	transform          motionTransformState
	rootTransform      motionTransformState
	opacity            uint8
	rootOpacity        uint8
	blendMode          int64
	coordinateMode     int
	active             bool
	visible            bool
	zFactor            float64
	hasZFactor         bool
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
	moduleDoc, err := assets.ParsePSBWithSeed(data, seed)
	if err != nil {
		return nil, err
	}
	runtimeDoc := cloneMotionDocument(moduleDoc)
	resolveMotionPSBReferences(runtimeDoc)
	return &motionResource{path: path, doc: runtimeDoc, moduleDoc: moduleDoc, data: data}, nil
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

// motionResourcePathAliases mirrors the raw and lzfs-trimmed keys from
// buildPathCacheKeys (ResourceManager.cpp:36). Services adds the resolved
// placed-path key when its storage can resolve the name.
func motionResourcePathAliases(path string) []string {
	aliases := make([]string, 0, 2)
	if path == "" {
		return aliases
	}
	aliases = append(aliases, path)
	trimmed := strings.TrimPrefix(path, "lzfs://./")
	if trimmed != path {
		aliases = append(aliases, trimmed)
	}
	return aliases
}

func (s *Services) motionResourcePathAliases(path string) []string {
	aliases := motionResourcePathAliases(path)
	if s == nil || s.Storage == nil || path == "" {
		return aliases
	}
	placed, err := s.Storage.PlacedPath(path)
	if err != nil || placed == "" {
		return aliases
	}
	found := false
	for _, existing := range aliases {
		if existing == placed {
			found = true
			break
		}
	}
	if !found {
		aliases = append(aliases, placed)
	}
	return aliases
}

func (s *Services) motionResourcePathMatches(left, right string) bool {
	leftAliases := s.motionResourcePathAliases(left)
	for _, alias := range s.motionResourcePathAliases(right) {
		for _, candidate := range leftAliases {
			if alias == candidate {
				return true
			}
		}
	}
	return false
}

func motionResourceLookupCandidates(name string) []string {
	if name == "" {
		return nil
	}
	if strings.ContainsAny(name, "/\\") || strings.Contains(name, ".") {
		return []string{name}
	}
	return []string{name + ".mtn", name + ".psb", "motion/" + name + ".mtn", "motion/" + name + ".psb"}
}

func (s *Services) cacheMotionForPlayer(state *motionPlayerState, request string, candidate string, resource *motionResource) {
	if state == nil || resource == nil {
		return
	}
	if state.motionsByKey == nil {
		state.motionsByKey = make(map[string]*motionResource)
	}
	if request != "" {
		if state.motionsByKey[request] == nil {
			state.motionsByKey[request] = resource
		}
	}
	if candidate != "" {
		if state.motionsByKey[candidate] == nil {
			state.motionsByKey[candidate] = resource
		}
	}
	for _, alias := range s.motionResourcePathAliases(resource.path) {
		if state.motionsByKey[alias] == nil {
			state.motionsByKey[alias] = resource
		}
	}
}

func (s *Services) resolveMotionForPlayer(state *motionPlayerState, name string) *motionResource {
	if state == nil || name == "" {
		return nil
	}
	if resource := state.motionsByKey[name]; resource != nil {
		return resource
	}
	for _, candidate := range motionResourceLookupCandidates(name) {
		if s != nil && s.Storage != nil {
			placed, err := s.Storage.PlacedPath(candidate)
			if err == nil && placed != "" {
				if resource := state.motionsByKey[placed]; resource != nil {
					s.cacheMotionForPlayer(state, name, candidate, resource)
					return resource
				}
				resource, err := s.loadMotionResource(placed)
				if err == nil && resource != nil {
					s.cacheMotionForPlayer(state, name, candidate, resource)
					return resource
				}
			}
		}
		if resource := state.motionsByKey[candidate]; resource != nil {
			s.cacheMotionForPlayer(state, name, candidate, resource)
			return resource
		}
		resource := s.motionResource(candidate, state.resourceManager)
		if resource == nil {
			loaded, err := s.loadMotionResource(candidate)
			if err != nil {
				continue
			}
			resource = loaded
		}
		s.cacheMotionForPlayer(state, name, candidate, resource)
		return resource
	}
	return nil
}

func motionSourcePathLike(value string) bool {
	return strings.ContainsAny(value, "/\\") || strings.Contains(value, ".") || strings.Contains(value, "://")
}

func motionPlayerSourceCandidates(resource *motionResource, name string) []string {
	candidates := []string{name}
	if resource == nil || resource.doc == nil || name == "" {
		return candidates
	}
	root, _ := resource.doc.Root.(map[string]assets.Value)
	var found []string
	var visit func(assets.Value, string, []string)
	visit = func(value assets.Value, parentKey string, path []string) {
		switch item := value.(type) {
		case string:
			key := strings.ToLower(parentKey)
			if motionSourcePathLike(item) || strings.Contains(key, "source") || key == "src" || key == "path" || key == "file" {
				found = append(found, item)
			}
		case assets.Resource:
			joined := strings.Join(path, "/")
			if motionSourcePathLike(joined) {
				found = append(found, joined)
			}
		case []assets.Value:
			for _, child := range item {
				visit(child, parentKey, path)
			}
		case map[string]assets.Value:
			keys := make([]string, 0, len(item))
			for key := range item {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				visit(item[key], key, append(append([]string(nil), path...), key))
			}
		}
	}
	visit(root, "", nil)
	baseName := func(path string) string {
		if slash := strings.LastIndexAny(path, "/\\"); slash >= 0 {
			path = path[slash+1:]
		}
		if dot := strings.LastIndex(path, "."); dot > 0 {
			path = path[:dot]
		}
		return path
	}
	for _, candidate := range found {
		if candidate == name || baseName(candidate) == name {
			candidates = append(candidates, candidate)
			for _, alias := range motionResourceStorageAliases(resource.path) {
				candidates = append(candidates, "psb://"+alias+"/"+candidate)
			}
			if !strings.ContainsAny(candidate, "/\\") && resource.path != "" {
				if slash := strings.LastIndexAny(resource.path, "/\\>"); slash >= 0 && slash+1 < len(resource.path) {
					candidates = append(candidates, resource.path[:slash+1]+candidate)
				}
			}
		}
	}
	return candidates
}

func motionResourceStorageAliases(path string) []string {
	if path == "" {
		return nil
	}
	aliases := []string{path}
	if slash := strings.LastIndexAny(path, "/\\>"); slash >= 0 && slash+1 < len(path) {
		aliases = append(aliases, path[slash+1:])
	}
	return aliases
}

func motionEmbeddedResourceData(resource *motionResource, name string) []byte {
	if resource == nil || resource.doc == nil || name == "" {
		return nil
	}
	root, _ := resource.doc.Root.(map[string]assets.Value)
	var visit func(assets.Value, []string) []byte
	visit = func(value assets.Value, path []string) []byte {
		switch item := value.(type) {
		case assets.Resource:
			if strings.Join(path, "/") == name && item.Offset >= 0 && item.Length >= 0 && item.Offset <= len(resource.data) && item.Length <= len(resource.data)-item.Offset {
				return resource.data[item.Offset : item.Offset+item.Length]
			}
		case []assets.Value:
			for index, child := range item {
				if data := visit(child, append(append([]string(nil), path...), strconv.Itoa(index))); data != nil {
					return data
				}
			}
		case map[string]assets.Value:
			for key, child := range item {
				if data := visit(child, append(append([]string(nil), path...), key)); data != nil {
					return data
				}
			}
		}
		return nil
	}
	return visit(root, nil)
}

func (s *Services) motionPlayerFindSource(vm *tjs.VM, state *motionPlayerState, name string) (tjs.Value, error) {
	if state == nil || name == "" || state.resourceManager == 0 {
		return tjs.VoidValue, nil
	}
	if value := state.sourceCache[name]; value.Kind() != tjs.KindVoid {
		return value, nil
	}
	for _, candidate := range motionPlayerSourceCandidates(s.playerResource(state), name) {
		if strings.HasPrefix(candidate, "psb://") {
			value, err := s.motionPlayerLoadEmbeddedSource(vm, state, candidate)
			if err == nil && value.Kind() != tjs.KindVoid {
				if state.sourceCache == nil {
					state.sourceCache = make(map[string]tjs.Value)
				}
				state.sourceCache[name] = value
				state.sourceCache[candidate] = value
				if state.sourceCacheResolved == nil {
					state.sourceCacheResolved = make(map[string]string)
				}
				state.sourceCacheResolved[name] = candidate
				state.sourceCacheResolved[candidate] = candidate
				return value, nil
			}
		}
		if s.Storage == nil {
			continue
		}
		placed, err := s.Storage.PlacedPath(candidate)
		if err != nil || placed == "" {
			continue
		}
		value, err := s.motionResourceManagerNative(vm, tjs.ObjectIDValue(state.resourceManager), "load", []tjs.Value{tjs.StringValue(placed)})
		if err != nil || value.Kind() == tjs.KindVoid {
			continue
		}
		if state.sourceCache == nil {
			state.sourceCache = make(map[string]tjs.Value)
		}
		state.sourceCache[name] = value
		state.sourceCache[placed] = value
		if state.sourceCacheResolved == nil {
			state.sourceCacheResolved = make(map[string]string)
		}
		state.sourceCacheResolved[name] = placed
		state.sourceCacheResolved[placed] = placed
		return value, nil
	}
	return tjs.VoidValue, nil
}

func (s *Services) motionPlayerLoadEmbeddedSource(vm *tjs.VM, state *motionPlayerState, uri string) (tjs.Value, error) {
	parts := strings.SplitN(strings.TrimPrefix(uri, "psb://"), "/", 2)
	if len(parts) != 2 {
		return tjs.VoidValue, nil
	}
	resources := append([]*motionResource{state.resource}, state.attached...)
	for _, owner := range resources {
		if owner == nil {
			continue
		}
		aliasMatches := false
		for _, alias := range motionResourceStorageAliases(owner.path) {
			if alias == parts[0] {
				aliasMatches = true
				break
			}
		}
		if !aliasMatches {
			continue
		}
		data := motionEmbeddedResourceData(owner, parts[1])
		if len(data) == 0 {
			continue
		}
		s.mu.RLock()
		seed := s.EmotePSBDecryptSeed
		s.mu.RUnlock()
		doc, err := assets.ParsePSBWithSeed(data, seed)
		if err != nil {
			continue
		}
		module, err := psbValueToTJS(vm, doc.Root, data, 0)
		if err != nil {
			return tjs.VoidValue, err
		}
		resource := &motionResource{path: uri, doc: doc, moduleDoc: doc, data: data}
		s.registerMotionModule(module, resource)
		s.setMotionResource(state.resourceManager, resource)
		return module, nil
	}
	return tjs.VoidValue, nil
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
		for _, alias := range s.motionResourcePathAliases(path) {
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
	for _, alias := range s.motionResourcePathAliases(resource.path) {
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
	for _, alias := range s.motionResourcePathAliases(path) {
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
	if cache.lastPath == path {
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
	if operation == "@invalidate" || operation == "finalize" {
		s.mu.Lock()
		delete(s.MotionPlayers, id)
		s.mu.Unlock()
		return tjs.VoidValue, nil
	}
	state := s.motionPlayer(id)
	switch operation {
	case "play", "progress", "get:variableKeys", "getVariable", "countVariables", "getVariableLabelAt",
		"countVariableFrameAt", "getVariableFrameLabelAt", "getVariableFrameValueAt",
		"getVariableRange", "getVariableFrameList", "getTimelinePlaying",
		"countMainTimelines", "getMainTimelineLabelAt", "getMainTimelineLabelList",
		"countDiffTimelines", "getDiffTimelineLabelAt", "getDiffTimelineLabelList",
		"getLoopTimeline", "countPlayingTimelines", "getPlayingTimelineLabelAt",
		"getPlayingTimelineFlagsAt", "getTimelineTotalFrameCount", "playTimeline",
		"setTimelineBlendRatio", "getPlayingTimelineInfoList", "skipToSync",
		"serialize", "getLayerNames", "getLayerMotion", "getLayerGetter",
		"getLayerGetterList":
		if err := s.motionPlayerEnsureLoaded(vm, state); err != nil {
			return tjs.VoidValue, err
		}
	case "fadeInTimeline":
		label := ""
		if len(args) > 0 {
			label = args[0].Text()
		}
		alreadyPlaying := false
		for _, playingLabel := range state.playingTimelineLabels {
			if playingLabel == label {
				alreadyPlaying = true
				break
			}
		}
		if !alreadyPlaying {
			if err := s.motionPlayerEnsureLoaded(vm, state); err != nil {
				return tjs.VoidValue, err
			}
		}
	case "draw", "captureCanvas":
		if len(args) > 0 && args[0].Kind() == tjs.KindObject && !args[0].IsNull() {
			if err := s.motionPlayerEnsureLoaded(vm, state); err != nil {
				return tjs.VoidValue, err
			}
		}
	}
	switch operation {
	case "@initialize", "Player":
		if state.resourceManagerValue.IsVoid() {
			source := tjs.NullValue
			if len(args) > 0 {
				source = args[0]
			}
			view, managerID, err := s.newPlayerResourceManagerView(vm, source)
			if err != nil {
				return tjs.VoidValue, err
			}
			state.resourceManager = managerID
			state.resourceManagerValue = view
		}
		return tjs.VoidValue, nil
	case "initPhysics":
		return tjs.VoidValue, nil
	case "random":
		// The reference constructs `new Math.RandomGenerator()` per player and
		// calls the object's random() method; child players inherit the same
		// object (PlayerCore.cpp:201-207,1318-1333).
		generator, err := s.motionPlayerRandomGenerator(vm, state)
		if err != nil {
			return tjs.VoidValue, err
		}
		method, err := vm.GetMember(generator, "random", s, nil)
		if err != nil {
			return tjs.VoidValue, err
		}
		result, err := vm.InvokeLegacy(method, generator, nil, s, nil, true)
		if err != nil {
			return tjs.VoidValue, err
		}
		number, err := result.Real()
		if err != nil {
			return tjs.VoidValue, err
		}
		return tjs.RealValue(number), nil
	case "frameProgress":
		frames := 0.0
		if len(args) > 0 && args[0].Kind() != tjs.KindVoid {
			value, err := args[0].Real()
			if err != nil {
				return tjs.VoidValue, err
			}
			frames = value
		}
		s.motionPlayerFrameProgress(id, state, frames)
		return tjs.VoidValue, nil
	case "captureCanvas":
		if len(args) == 0 || args[0].Kind() != tjs.KindObject || args[0].IsNull() {
			return state.lastCanvas, nil
		}
		if s.playerResource(state) == nil {
			return state.lastCanvas, nil
		}
		state.playerID = id
		if err := s.drawMotionTarget(vm, state, args[0]); err != nil {
			return tjs.VoidValue, err
		}
		state.lastCanvas = args[0]
		return args[0], nil
	case "clearCache":
		motionClearSourceCaches(s.playerResource(state))
		state.sourceCache = nil
		state.sourceCacheResolved = nil
		state.gpuMotion = nil
		state.gpuMotionValid = false
		state.lastCanvas = tjs.VoidValue
		return tjs.VoidValue, nil
	case "findSource", "loadSource":
		if len(args) == 0 {
			return tjs.VoidValue, nil
		}
		name := s.motionSourceCacheKey(vm, args[0])
		source, err := s.motionPlayerFindSource(vm, state, name)
		if err != nil {
			return tjs.VoidValue, err
		}
		if operation == "findSource" {
			return source, nil
		}
		return tjs.VoidValue, nil
	case "setHairScale", "setPartsScale", "setBustScale", "setRotate":
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("Motion.Player.%s requires a value", operation)
		}
		value, err := args[0].Real()
		if err != nil {
			return tjs.VoidValue, err
		}
		switch operation {
		case "setHairScale":
			state.hairScale = value
		case "setPartsScale":
			state.partsScale = value
		case "setBustScale":
			state.bustScale = value
		case "setRotate":
			state.rotateAngle = value
			state.gpuMotionValid = false
			if state.emote == nil {
				state.emote = newEmotePlayerState()
			}
			state.emote.rot = value
		}
		return tjs.VoidValue, nil
	case "setMirror":
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("Motion.Player.setMirror requires a flag")
		}
		mirror := motionPlayerBool(args[0])
		changed := state.rootFlipX != mirror || state.mirrorEvalEnabled != mirror
		state.rootFlipX = mirror
		state.mirrorEvalEnabled = state.rootFlipX
		if changed {
			state.gpuMotionValid = false
			emote := s.motionPlayerEmote(state)
			emote.mirror = state.mirrorEvalEnabled
			emote.animators = make(map[int]map[string]*emoteAnimator)
			emote.evalResults = make(map[string]float64)
			emote.evalOrder = nil
			if state.selectorEnabled {
				s.emoteSyncSelectorControls(state, emote)
			}
		}
		return tjs.VoidValue, nil
	case "copyRect", "adjustGamma", "unloadUnusedTextures", "doAlphaMaskOperation":
		return tjs.VoidValue, nil
	case "clear":
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("Motion.Player.clear requires a target layer")
		}
		if adaptor := s.motionSeparateLayerAdaptorState(args[0]); adaptor != nil {
			return tjs.VoidValue, s.clearMotionSeparateLayerAdaptor(vm, args[0].ObjectRef().Object, adaptor)
		}
		return tjs.VoidValue, s.clearMotionTarget(vm, args[0])
	case "setClearColor":
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("Motion.Player.setClearColor requires a color")
		}
		color, err := args[0].Int()
		if err != nil {
			return tjs.VoidValue, err
		}
		state.clearColor = color
		return tjs.VoidValue, nil
	case "setResizable":
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("Motion.Player.setResizable requires a flag")
		}
		state.resizable = motionPlayerBool(args[0])
		return tjs.VoidValue, nil
	case "removeAllTextures":
		motionClearSourceCaches(s.playerResource(state))
		state.sourceCache = nil
		state.sourceCacheResolved = nil
		state.gpuMotion = nil
		state.gpuMotionValid = false
		return tjs.VoidValue, nil
	case "removeAllBg":
		state.backgrounds = nil
		return tjs.VoidValue, nil
	case "removeAllCaption":
		state.captions = nil
		return tjs.VoidValue, nil
	case "registerBg":
		if len(args) > 0 {
			state.backgrounds = append(state.backgrounds, args[0])
		}
		return tjs.VoidValue, nil
	case "registerCaption":
		if len(args) > 0 {
			state.captions = append(state.captions, args[0])
		}
		return tjs.VoidValue, nil
	case "alphaOpAdd":
		state.alphaOpCounter++
		return tjs.IntegerValue(state.alphaOpCounter), nil
	case "setSize":
		if len(args) < 2 {
			return tjs.VoidValue, fmt.Errorf("Motion.Player.setSize requires width and height")
		}
		width, err := args[0].Int()
		if err != nil {
			return tjs.VoidValue, err
		}
		height, err := args[1].Int()
		if err != nil {
			return tjs.VoidValue, err
		}
		state.width, state.height = width, height
		return tjs.VoidValue, nil
	case "setFlip":
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("Motion.Player.setFlip requires a flag")
		}
		state.viewFlip = motionPlayerBool(args[0])
		return tjs.VoidValue, nil
	case "setOpacity", "setSlant", "setZoom":
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("Motion.Player.%s requires a value", operation)
		}
		number, err := args[0].Real()
		if err != nil {
			return tjs.VoidValue, err
		}
		switch operation {
		case "setOpacity":
			state.viewOpacity = number
		case "setSlant":
			state.viewSlant = number
		case "setZoom":
			state.viewZoom = number
		}
		return tjs.VoidValue, nil
	case "setVisible":
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("Motion.Player.setVisible requires a flag")
		}
		state.viewVisible = motionPlayerBool(args[0])
		return tjs.VoidValue, nil
	case "calcViewParam":
		return tjs.VoidValue, motionPlayerCalcViewParam(vm, state)
	}
	if strings.HasPrefix(operation, "get:") {
		return s.motionPlayerGet(vm, state, strings.TrimPrefix(operation, "get:"))
	}
	if strings.HasPrefix(operation, "set:") {
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("Motion.Player.%s requires a value", strings.TrimPrefix(operation, "set:"))
		}
		property := strings.TrimPrefix(operation, "set:")
		if property == "motion" {
			return tjs.VoidValue, s.motionPlayerSetMotion(vm, context, state, args[0])
		}
		if property == "motionKey" {
			return tjs.VoidValue, s.motionPlayerSetMotionKey(vm, state, args[0])
		}
		return tjs.VoidValue, s.motionPlayerSet(state, property, args[0])
	}
	switch operation {
	case "play":
		motion := ""
		flags := int64(0)
		if len(args) > 0 && args[0].Kind() != tjs.KindVoid {
			if args[0].Kind() == tjs.KindInteger || args[0].Kind() == tjs.KindReal {
				value, err := args[0].Int()
				if err != nil {
					return tjs.VoidValue, err
				}
				flags = value
			} else {
				motion = args[0].Text()
			}
		}
		if len(args) > 1 && args[1].Kind() != tjs.KindVoid {
			value, err := args[1].Int()
			if err != nil {
				return tjs.VoidValue, err
			}
			flags = value
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
		if state.resource == nil && motion != "" {
			if err := s.motionPlayerSetMotionKey(vm, state, tjs.StringValue(motion)); err != nil {
				return tjs.VoidValue, err
			}
			if err := s.motionPlayerEnsureLoaded(vm, state); err != nil {
				return tjs.VoidValue, err
			}
		}
		if state.resource == nil {
			return tjs.IntegerValue(0), nil
		}
		if flags&motionPlayFlagStealth != 0 && motion != "" {
			state.stealthMotion = motion
		}
		if motion == "" {
			// Empty request restarts the loaded motion from its origin unless
			// the chain flag keeps the current position.
			if flags&motionPlayFlagChain == 0 {
				state.frameTick = 0
				state.clampedEvalTime = 0
				state.frameLoopTime = 0
				state.queuing = true
			}
			state.motionClockActive = state.motion != ""
			state.held = false
			s.motionPlayerApplyLoopTime(state)
			emote := s.motionPlayerEmote(state)
			timelineRuntime := s.motionPlayerTimelineRuntime(state, emote)
			started := s.emoteStartMotion(state, timelineRuntime, "", flags)
			s.motionPlayerCommitTimelineRuntime(state, emote, timelineRuntime)
			state.playing = len(state.playingTimelineLabels) > 0
			return tjs.IntegerValue(boolInt(started)), nil
		}
		if state.motion != motion || flags&(motionPlayFlagForce|motionPlayFlagAsCan) != 0 {
			state.particleArrays = nil
			state.motionChildren = nil
			state.particleChildren = nil
			state.anchorStates = nil
		}
		if flags&motionPlayFlagStealth == 0 {
			state.motion = motion
		}
		state.motionUnloaded = false
		state.motionClockActive = true
		state.playingTimelineLabels = nil
		state.playerTimelineLoaded = false
		if flags&motionPlayFlagChain == 0 {
			state.frameTick = 0
			state.clampedEvalTime = 0
			state.frameLoopTime = 0
			state.queuing = true
		}
		s.motionPlayerApplyLoopTime(state)
		state.playing = true
		state.held = false
		emote := s.motionPlayerEmote(state)
		timelineRuntime := s.motionPlayerTimelineRuntime(state, emote)
		started := s.emoteStartMotion(state, timelineRuntime, motion, flags)
		s.motionPlayerCommitTimelineRuntime(state, emote, timelineRuntime)
		state.playing = len(state.playingTimelineLabels) > 0
		state.motionClockActive = state.motion != ""
		motionTrace("play id=%d chara=%q motion=%q flags=%d", id, state.chara, state.motion, flags)
		return tjs.IntegerValue(boolInt(started)), nil
	case "onFindMotion":
		if len(args) == 0 {
			return tjs.VoidValue, nil
		}
		flags := int64(0)
		if len(args) > 1 && args[1].Kind() != tjs.KindVoid {
			value, err := args[1].Int()
			if err != nil {
				return tjs.VoidValue, err
			}
			flags = value
		}
		if err := s.motionPlayerFindMotion(vm, state, args[0].Text(), flags); err != nil {
			return tjs.VoidValue, err
		}
		return tjs.VoidValue, nil
	case "serialize":
		emote := s.motionPlayerEmote(state)
		timelineRuntime := s.motionPlayerTimelineRuntime(state, emote)
		result, err := s.emotePlayerSerialize(vm, state, timelineRuntime)
		s.motionPlayerCommitTimelineRuntime(state, emote, timelineRuntime)
		return result, err
	case "unserialize":
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("Motion.Player.unserialize requires data")
		}
		if err := s.motionPlayerUnserialize(vm, state, args[0]); err != nil {
			return tjs.VoidValue, err
		}
		return tjs.VoidValue, nil
	case "motionList":
		paths := make([]tjs.Value, 0)
		seen := make(map[string]struct{})
		for _, resource := range state.motionsByKey {
			if resource == nil || resource.path == "" {
				continue
			}
			if _, exists := seen[resource.path]; exists {
				continue
			}
			seen[resource.path] = struct{}{}
			paths = append(paths, tjs.StringValue(resource.path))
		}
		return vm.NewArray(paths)
	case "getD3DAvailable":
		return tjs.IntegerValue(1), nil
	case "debugPrint":
		motionTrace("debug id=%d motion=%q frame=%.3f", id, state.motion, state.frameTick)
		return tjs.VoidValue, nil
	case "emoteEdit":
		state.directEdit = true
		if len(args) > 0 {
			state.tags = args[0]
		}
		return tjs.VoidValue, nil
	case "stop":
		state.playing = false
		return tjs.IntegerValue(1), nil
	case "isPlaying":
		state.playing = len(state.playingTimelineLabels) > 0
		return tjs.IntegerValue(boolInt(state.playing)), nil
	case "progress":
		interval := 0.0
		if len(args) > 0 && args[0].Kind() != tjs.KindVoid {
			value, err := args[0].Real()
			if err != nil {
				return tjs.VoidValue, err
			}
			interval = value
		}
		if interval < 0 || interval > motionProgressMaxMS {
			interval = 0
		}
		emote := s.motionPlayerEmote(state)
		freezeBody := emote.freezeBodyMotion()
		timelineRuntime := s.motionPlayerTimelineRuntime(state, emote)
		if !motionPlayerSpeed(state) && !freezeBody {
			// progress still runs updateLayers after frameProgress returns for
			// a paused player, and that layer update clears queuing.
			s.motionPlayerClearQueuingAfterLayerUpdate(state)
			return tjs.RealValue(motionPlayerProgressCompat(state)), nil
		}
		frames := float64(interval) * motionFramesPerMillisecond
		state.frameLastTime = frames
		if freezeBody {
			if motionPlayerSpeed(state) && frames > 0 {
				state.frameLoopTime += frames
				state.loopTime += frames
				emote.frameLastTime = frames
				emote.frameLoopTime += frames
				emote.loopTime += frames
				s.emoteAdvanceTimelines(timelineRuntime, frames)
			}
			s.emoteApplyEvalPostProcess(timelineRuntime)
			s.motionPlayerCommitTimelineRuntime(state, emote, timelineRuntime)
			state.playing = len(state.playingTimelineLabels) > 0
			state.syncActive = state.syncWaiting && state.playing
			s.motionPlayerClearQueuingAfterLayerUpdate(state)
			return tjs.RealValue(motionPlayerProgressCompat(state)), nil
		}
		s.motionPlayerFrameProgress(id, state, frames)
		// Motion.Player.progress runs updateLayers immediately after
		// frameProgress; updateLayers clears the reference player's queuing
		// flag. A direct frameProgress call leaves that flag unchanged.
		s.motionPlayerClearQueuingAfterLayerUpdate(state)
		if os.Getenv("KRKRGO_MOTION_TRACE") != "" {
			motionTrace("progress id=%d interval=%d frame=%.2f playing=%v", id, interval, state.frameTick, state.playing)
		}
		return tjs.RealValue(motionPlayerProgressCompat(state)), nil
	case "draw":
		if len(args) == 0 || args[0].Kind() != tjs.KindObject || args[0].IsNull() {
			return tjs.VoidValue, nil
		}
		if s.playerResource(state) == nil {
			return tjs.VoidValue, nil
		}
		motionTrace("draw time=%d id=%d motion=%q frame=%.2f playing=%v", s.Timers.Now(), id, state.motion, state.frameTick, state.playing)
		state.playerID = id
		if err := s.drawMotionTarget(vm, state, args[0]); err != nil {
			return tjs.VoidValue, err
		}
		state.lastCanvas = args[0]
		return tjs.VoidValue, nil
	case "setDrawAffineTranslateMatrix":
		values := [6]float64{1, 0, 0, 1, 0, 0}
		if len(args) >= 6 {
			for i := range values {
				if args[i].Kind() == tjs.KindVoid {
					return tjs.VoidValue, fmt.Errorf("Motion.Player.setDrawAffineTranslateMatrix requires six numeric values")
				}
				number, err := args[i].Real()
				if err != nil {
					return tjs.VoidValue, err
				}
				values[i] = number
			}
		} else if len(args) == 1 && args[0].Kind() == tjs.KindObject && !args[0].IsNull() {
			for index, property := range []string{"m11", "m21", "m12", "m22", "m14", "m24"} {
				value, found, err := vm.GetMemberIfPresent(args[0], property, s, nil)
				if err != nil || !found || value.Kind() == tjs.KindVoid {
					continue
				}
				number, err := value.Real()
				if err != nil {
					return tjs.VoidValue, err
				}
				values[index] = number
			}
		} else {
			return tjs.VoidValue, fmt.Errorf("Motion.Player.setDrawAffineTranslateMatrix requires one matrix object or six values")
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
		if state.resource == nil {
			return tjs.VoidValue, nil
		}
		emote := s.motionPlayerEmote(state)
		runtime := s.motionPlayerTimelineRuntime(state, emote)
		syncTime := 0.0
		for _, timeline := range runtime.timelines {
			if !timeline.control {
				continue
			}
			syncTime = math.Max(syncTime, timeline.lastTime)
			if timeline.loopEnd >= timeline.loopBegin {
				syncTime = math.Max(syncTime, timeline.loopEnd)
			}
		}
		syncTime = math.Max(syncTime, motionField(state.resource, state.chara, state.motion, "totalFrames", 0))
		for _, label := range runtime.playingOrder {
			if timeline := runtime.timelines[label]; timeline != nil && timeline.totalFrames > 0 {
				syncTime = math.Max(syncTime, timeline.totalFrames)
			}
		}
		for _, timeline := range runtime.timelines {
			timeline.time = syncTime
			if timeline.control {
				s.emoteResetTimelineControl(runtime, timeline, syncTime)
			}
			if timeline.totalFrames > 0 && syncTime >= timeline.totalFrames && !timeline.loop {
				timeline.playing = false
			}
		}
		playing := runtime.playingOrder[:0]
		for _, label := range runtime.playingOrder {
			if timeline := runtime.timelines[label]; timeline != nil && timeline.playing {
				playing = append(playing, label)
			}
		}
		runtime.playingOrder = playing
		s.emoteApplyEvalPostProcess(runtime)
		s.motionPlayerCommitTimelineRuntime(state, emote, runtime)
		state.frameLoopTime = syncTime
		state.clampedEvalTime = syncTime
		state.syncWaiting = false
		state.syncActive = false
		state.playing = len(state.playingTimelineLabels) > 0
		return tjs.VoidValue, nil
	case "releaseSyncWait":
		state.syncWaiting = false
		state.syncActive = false
		return tjs.VoidValue, nil
	case "getLayerNames":
		resource := s.playerResource(state)
		layers := motionLayerList(resource, state.chara, state.motion)
		labels := make(map[string]struct{})
		collectMotionLayerNames(layers, labels)
		names := make([]string, 0, len(labels))
		for name := range labels {
			names = append(names, name)
		}
		sort.Strings(names)
		values := make([]tjs.Value, len(names))
		for index, name := range names {
			values[index] = tjs.StringValue(name)
		}
		return vm.NewArray(values)
	case "getCommandList":
		values := motionPlayerCommandCandidates(state.resource, state.chara, state.motion)
		items := make([]tjs.Value, len(values))
		for index, value := range values {
			items[index] = tjs.StringValue(value)
		}
		return vm.NewArray(items)
	case "getLayerMotion":
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("Motion.Player.getLayerMotion requires a layer name")
		}
		resource := s.playerResource(state)
		moduleDoc := motionModuleDocument(resource)
		layers := motionLayerListFromDocument(moduleDoc, state.chara, state.motion)
		layer := findMotionLayerByLabelInDocument(moduleDoc, layers, args[0].Text())
		if layer == nil || resource == nil {
			return tjs.VoidValue, nil
		}
		return psbValueToTJS(vm, layer, resource.data, 0)
	case "getLayerGetter", "getLayerGetterList":
		getters, err := s.motionPlayerLayerGetters(vm, state)
		if err != nil {
			return tjs.VoidValue, err
		}
		if operation == "getLayerGetter" {
			if len(args) == 0 {
				return tjs.VoidValue, fmt.Errorf("Motion.Player.getLayerGetter requires a layer name")
			}
			label := args[0].Text()
			var found tjs.Value
			for _, getter := range getters {
				value, err := vm.GetMember(getter, "label", s, nil)
				if err != nil {
					return tjs.VoidValue, err
				}
				if value.Text() == label {
					found = getter
				}
			}
			if found.Kind() == tjs.KindVoid {
				return tjs.VoidValue, nil
			}
			return found, nil
		}
		return vm.NewArray(getters)
	case "isSelectorTarget":
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("Motion.Player.isSelectorTarget requires a target name")
		}
		emote := s.motionPlayerEmote(state)
		_, exists := emote.selectorControls[args[0].Text()]
		return tjs.IntegerValue(boolInt(exists && !state.disabledSelectorTargets[args[0].Text()])), nil
	case "deactivateSelectorTarget":
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("Motion.Player.deactivateSelectorTarget requires a target name")
		}
		if state.disabledSelectorTargets == nil {
			state.disabledSelectorTargets = make(map[string]bool)
		}
		state.disabledSelectorTargets[args[0].Text()] = true
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
		resource := s.resolveMotionForPlayer(state, args[0].Text())
		return tjs.IntegerValue(boolInt(resource != nil)), nil
	case "findMotion":
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("Motion.Player.findMotion requires a motion key")
		}
		name := args[0].Text()
		resource := s.resolveMotionForPlayer(state, name)
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
		state.motion = name
		state.metadataResource = nil
		state.playingTimelineLabels = nil
		state.playerTimelineLoaded = false
		state.particleArrays = nil
		state.motionChildren = nil
		state.particleChildren = nil
		state.anchorStates = nil
		state.gpuMotion = nil
		state.gpuMotionValid = false
		s.motionPlayerEmote(state)
		if err := s.motionPlayerSyncVariableKeys(vm, state); err != nil {
			return tjs.VoidValue, err
		}
		return module, nil
	case "unload":
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("Motion.Player.unload requires a motion key")
		}
		key := args[0].Text()
		if key == "" {
			return tjs.VoidValue, nil
		}
		for cachedKey, resolvedKey := range state.sourceCacheResolved {
			if cachedKey == key || resolvedKey == key {
				delete(state.sourceCache, cachedKey)
				delete(state.sourceCacheResolved, cachedKey)
			}
		}
		delete(state.sourceCache, key)
		delete(state.sourceCacheResolved, key)
		activeResourceUnloaded := false
		for cachedKey, resource := range state.motionsByKey {
			if cachedKey == key || (resource != nil && resource.path == key) {
				if resource != nil && resource == state.resource {
					activeResourceUnloaded = true
				}
				delete(state.motionsByKey, cachedKey)
			}
		}
		if state.resource != nil && activeResourceUnloaded {
			state.resource = nil
			state.particleArrays = nil
			state.motionChildren = nil
			state.particleChildren = nil
			state.anchorStates = nil
			state.metadataResource = nil
			state.motionUnloaded = true
			state.motionClockActive = false
			state.playingTimelineLabels = nil
			state.playerTimelineLoaded = false
			state.held = false
			if state.emote != nil {
				s.emoteResetRuntime(state.emote)
			}
		}
		return tjs.VoidValue, nil
	case "unloadAll":
		s.clearMotionResourceCache(state.resourceManager)
		if state.resourceManager != 0 {
			manager := tjs.ObjectIDValue(state.resourceManager)
			cache, err := vm.NewDictionary()
			if err != nil {
				return tjs.VoidValue, err
			}
			if err := tjs.SetMember(vm.Objects, manager, "_motionResourceCache", cache); err != nil {
				return tjs.VoidValue, err
			}
		}
		state.resource = nil
		state.attached = nil
		state.particleArrays = nil
		state.motionChildren = nil
		state.particleChildren = nil
		state.anchorStates = nil
		state.metadataResource = nil
		state.motionsByKey = nil
		state.sourceCache = nil
		state.sourceCacheResolved = nil
		state.motion = ""
		state.motionClockActive = false
		state.playingTimelineLabels = nil
		state.playerTimelineLoaded = false
		state.held = false
		state.variableKeys = tjs.VoidValue
		state.drawMatrix = identityMotionDrawMatrix()
		state.lastCanvas = tjs.VoidValue
		state.lastViewParam = tjs.VoidValue
		state.motionUnloaded = true
		if state.emote != nil {
			s.emoteResetRuntime(state.emote)
		}
		return tjs.VoidValue, nil
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
		timelineRuntime := s.motionPlayerTimelineRuntime(state, emote)
		result, err := s.motionEmotePlayerNativeWithState(vm, context, operation, args, state, timelineRuntime, false)
		if err != nil {
			return result, err
		}
		s.motionPlayerCommitTimelineRuntime(state, emote, timelineRuntime)
		if operation == "playTimeline" || operation == "stopTimeline" {
			state.playing = len(state.playingTimelineLabels) > 0
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
	return s.motionPlayerSetMotionKey(vm, state, tjs.StringValue(motion))
}

// motionKey is a conventional native property in the reference registration;
// unlike the raw-callback motion property, it bypasses onFindMotion.
func (s *Services) motionPlayerSetMotionKey(vm *tjs.VM, state *motionPlayerState, value tjs.Value) error {
	motion := value.Text()
	if state.motion == motion {
		return nil
	}
	state.motionTreeGeneration++
	state.frameLastTime = 0
	state.clampedEvalTime = 0
	state.drawMatrix = identityMotionDrawMatrix()
	state.resource = nil
	state.metadataResource = nil
	state.motionClockActive = false
	state.playerTimelineLoaded = false
	state.playingTimelineLabels = nil
	state.gpuMotion = nil
	state.gpuMotionValid = false
	state.particleArrays = nil
	state.motionChildren = nil
	state.particleChildren = nil
	state.anchorStates = nil
	if state.emote != nil {
		s.emoteResetRuntime(state.emote)
	}
	variableKeys, err := vm.NewArray(nil)
	if err != nil {
		return err
	}
	state.variableKeys = variableKeys
	state.motion = motion
	state.motionUnloaded = false
	state.held = false
	return s.motionPlayerInitMotionProperty(vm, state)
}

// motionPlayerInitMotionProperty mirrors Player::setMotion: resolve and
// initialize the active module, while leaving timeline playback to play().
func (s *Services) motionPlayerInitMotionProperty(vm *tjs.VM, state *motionPlayerState) error {
	motion := state.motion
	manager := state.resourceManager
	var resource *motionResource
	if state.project.Kind() == tjs.KindObject && !state.project.IsNull() {
		s.mu.RLock()
		resource = s.MotionModules[state.project.ObjectRef().Object]
		s.mu.RUnlock()
	}
	looksLikeStoragePath := strings.ContainsAny(motion, "/\\") || strings.Contains(motion, ".")
	if resource == nil && looksLikeStoragePath {
		resource = s.resolveMotionForPlayer(state, motion)
	}
	if resource == nil {
		resource = s.lastMotionResource(manager)
	}
	if resource == nil && motion != "" {
		resource = s.resolveMotionForPlayer(state, motion)
	}
	if resource == nil {
		return nil
	}
	state.frameTick = 0
	state.clampedEvalTime = 0
	state.frameLoopTime = 0
	state.resource = resource
	state.metadataResource = nil
	state.playerTimelineLoaded = false
	s.motionPlayerApplyLoopTime(state)
	emote := s.motionPlayerEmote(state)
	runtime := s.motionPlayerTimelineRuntime(state, emote)
	state.playingTimelineLabels = nil
	// PlayerCore.cpp:402-405: assigning the motion property starts the
	// requested clip, so the KAG layer that copies motion/tickCount through
	// properties (MotionAffineSourceLayer.clone) resumes playback without a
	// separate play() call.
	started := s.emoteStartMotion(state, runtime, motion, 0)
	state.playing = started
	state.motionClockActive = motion != ""
	state.queuing = true
	state.frameLoopTime = 0
	s.motionPlayerCommitTimelineRuntime(state, emote, runtime)
	if err := s.motionPlayerSyncVariableKeys(vm, state); err != nil {
		return err
	}
	state.gpuMotion = nil
	state.gpuMotionValid = false
	return nil
}

func (s *Services) motionPlayerFrameProgress(id tjs.ObjectID, state *motionPlayerState, frames float64) {
	if !motionPlayerSpeed(state) {
		return
	}
	state.frameLastTime = frames
	emote := s.motionPlayerEmote(state)
	runtime := s.motionPlayerTimelineRuntime(state, emote)
	runtime.evalResults = make(map[string]float64)
	runtime.evalOrder = nil
	if state.queuing {
		s.motionPlayerCommitTimelineRuntime(state, emote, runtime)
		state.playing = len(state.playingTimelineLabels) > 0
		state.syncActive = state.syncWaiting && state.playing
		return
	}
	if frames > 0 {
		s.emoteAdvanceTimelines(runtime, frames)
		s.emoteStepControllerBuckets(runtime, frames)
	}
	s.emoteApplyEvalPostProcess(runtime)
	s.motionPlayerCommitTimelineRuntime(state, emote, runtime)
	state.playing = len(state.playingTimelineLabels) > 0
	state.syncActive = state.syncWaiting && state.playing
	state.frameLoopTime += frames
	state.loopTime += frames
	state.frameTick += frames
	state.clampedEvalTime = motionPlayerActiveClipTime(state, runtime)
	if resource := s.playerResource(state); resource != nil {
		last := motionLastTime(resource, state.chara, state.motion)
		if last >= 0 && state.frameTick >= last {
			advanceMotionClock(state, last, motionPlayerLoopTime(state, resource))
			if !state.playing {
				motionTrace("progress-stop id=%d motion=%q frame=%.2f last=%.2f", id, state.motion, state.frameTick, last)
			}
		}
	}
	state.playing = len(state.playingTimelineLabels) > 0
	state.syncActive = state.syncWaiting && state.playing
}

func (s *Services) motionPlayerClearQueuingAfterLayerUpdate(state *motionPlayerState) {
	resource := s.playerResource(state)
	if resource == nil || len(motionLayerList(resource, state.chara, state.motion)) == 0 {
		return
	}
	state.queuing = false
	s.motionPlayerUpdateAnchorStates(state)
}

func (s *Services) motionPlayerFindMotion(vm *tjs.VM, state *motionPlayerState, motion string, flags int64) error {
	if flags&motionPlayFlagStealth != 0 && state.project.IsVoid() {
		state.stealthMotion = motion
		return nil
	}
	resourceHasClip := func(resource *motionResource, label string) bool {
		if resource == nil || resource.doc == nil {
			return false
		}
		root, _ := resource.doc.Root.(map[string]assets.Value)
		objects, _ := root["object"].(map[string]assets.Value)
		if character, ok := objects[state.chara].(map[string]assets.Value); ok {
			motions, _ := character["motion"].(map[string]assets.Value)
			_, exists := motions[label]
			return exists
		}
		return false
	}
	if state.motion == motion && state.resource != nil && flags&(motionPlayFlagForce|motionPlayFlagAsCan) == 0 {
		return nil
	}
	state.motionTreeGeneration++
	state.anchorStates = nil
	var resource *motionResource
	if state.project.Kind() == tjs.KindObject && !state.project.IsNull() {
		s.mu.RLock()
		resource = s.MotionModules[state.project.ObjectRef().Object]
		s.mu.RUnlock()
		if resource != nil && state.chara == "" {
			state.chara = motionResourceMetadataString(resource, "chara")
			if state.chara == "" {
				state.chara = motionResourceDefaultChara(resource)
			}
		}
		if resourceHasClip(resource, motion) {
			state.resource = resource
		}
	}
	if resource == nil || !resourceHasClip(resource, motion) {
		paths := make([]string, 0, 2)
		if state.chara != "" && !strings.ContainsAny(motion, "/\\") {
			paths = append(paths, "motion/"+state.chara+"/"+motion)
		}
		paths = append(paths, motion)
		for _, path := range paths {
			resource = s.motionResource(path, state.resourceManager)
			if resource == nil {
				loaded, err := s.loadMotionResource(path)
				if err != nil {
					continue
				}
				resource = loaded
				s.setMotionResource(state.resourceManager, resource)
			}
			if resource != nil {
				break
			}
		}
	}
	if resource != nil {
		state.resource = resource
		state.metadataResource = nil
		state.playerTimelineLoaded = false
		if state.chara == "" {
			state.chara = motionResourceMetadataString(resource, "chara")
			if state.chara == "" {
				state.chara = motionResourceDefaultChara(resource)
			}
		}
		if state.resourceManager != 0 {
			manager := tjs.ObjectIDValue(state.resourceManager)
			module, err := s.motionResourceManagerNative(vm, manager, "loadSource", []tjs.Value{tjs.StringValue(resource.path)})
			if err != nil {
				return err
			}
			state.project = module
		}
	}
	if resource != nil {
		s.cacheMotionForPlayer(state, motion, resource.path, resource)
	}
	state.motion = motion
	state.motionUnloaded = false
	state.playingTimelineLabels = nil
	state.playerTimelineLoaded = false
	if flags&motionPlayFlagChain == 0 {
		state.frameTick = 0
		state.frameLoopTime = 0
		state.queuing = true
	}
	emote := s.motionPlayerEmote(state)
	runtime := s.motionPlayerTimelineRuntime(state, emote)
	s.emoteStartMotion(state, runtime, motion, flags&^motionPlayFlagStealth)
	s.motionPlayerCommitTimelineRuntime(state, emote, runtime)
	if err := s.motionPlayerSyncVariableKeys(vm, state); err != nil {
		return err
	}
	state.playing = len(state.playingTimelineLabels) > 0
	state.motionClockActive = state.motion != ""
	s.motionPlayerConsumeStealth(state)
	return nil
}

// motionPlayerConsumeStealth mirrors the stealth tail of Player::onFindMotion:
// a pending stealth motion records the chara that was active when the next
// motion loads and is then cleared (PlayerMotionLoad.cpp:135-140).
func (s *Services) motionPlayerConsumeStealth(state *motionPlayerState) {
	if state == nil || state.stealthMotion == "" {
		return
	}
	state.stealthChara = state.chara
	state.stealthMotion = ""
}

func (s *Services) motionPlayer(id tjs.ObjectID) *motionPlayerState {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.MotionPlayers[id]
	if state == nil {
		state = &motionPlayerState{speed: true, speedSet: true, zFactor: 1, zFactorSet: true, cameraFOV: 60, colorWeight: 0xFF808080, viewOpacity: 1, viewVisible: true, viewZoom: 1, emoteScale: 1, hairScale: 1, partsScale: 1, bustScale: 1, drawMatrix: identityMotionDrawMatrix(), randomGenerator: tjs.VoidValue}
		s.MotionPlayers[id] = state
	}
	return state
}

// motionPlayerRandomGenerator returns the player's TJS random generator,
// creating it on first use. The reference builds it in the Player constructor
// with `new Math.RandomGenerator()` and hands the same object to child players
// (PlayerCore.cpp:201-207; PlayerMotionLoad.cpp:217-220).
func (s *Services) motionPlayerRandomGenerator(vm *tjs.VM, state *motionPlayerState) (tjs.Value, error) {
	if state.randomGenerator.Kind() == tjs.KindObject && !state.randomGenerator.IsNull() {
		return state.randomGenerator, nil
	}
	if vm == nil {
		return tjs.VoidValue, fmt.Errorf("Motion.Player.random requires a VM")
	}
	math, found := vm.Globals["Math"]
	if !found || math.Kind() != tjs.KindObject || math.IsNull() {
		return tjs.VoidValue, fmt.Errorf("Motion.Player.random requires Math.RandomGenerator")
	}
	class, err := vm.GetMember(math, "RandomGenerator", s, nil)
	if err != nil {
		return tjs.VoidValue, err
	}
	generator, err := vm.Construct(class, nil, s, nil)
	if err != nil {
		return tjs.VoidValue, err
	}
	state.randomGenerator = generator
	return generator, nil
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
		// Reference Player.h: the TJS property converts the accumulated frame
		// counter back to
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
		return state.resourceManagerValue, nil
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
		return tjs.RealValue(state.loopTime), nil
	case "speed":
		if motionPlayerSpeed(state) {
			return tjs.IntegerValue(1), nil
		}
		return tjs.IntegerValue(0), nil
	case "zFactor":
		return tjs.RealValue(motionPlayerZFactor(state)), nil
	case "playing":
		if state.playing {
			return tjs.IntegerValue(1), nil
		}
		return tjs.IntegerValue(0), nil
	case "allplaying":
		return tjs.IntegerValue(boolInt(s.motionPlayerAllPlaying(state))), nil
	case "tags":
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
		state.motionUnloaded = false
		state.frameTick = 0
		state.clampedEvalTime = 0
		state.playing = motion != ""
		state.motionClockActive = motion != ""
		state.playingTimelineLabels = nil
		state.playerTimelineLoaded = false
		state.held = false
	case "metadata":
		state.metadata = value
	case "tickCount":
		n, err := value.Real()
		if err != nil {
			return err
		}
		state.frameTick = n * motionFramesPerMillisecond
	case "frameTickCount":
		n, err := value.Real()
		if err != nil {
			return err
		}
		state.frameTick = n
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
		enabled := motionPlayerBool(value)
		if state.selectorEnabled != enabled {
			state.selectorEnabled = enabled
			emote := s.motionPlayerEmote(state)
			s.emoteSyncSelectorControls(state, emote)
		}
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
		state.resourceManagerValue = value
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

func (s *Services) motionPlayerAllPlaying(state *motionPlayerState) bool {
	if state == nil {
		return false
	}
	if state.playing {
		return true
	}
	for _, child := range state.motionChildren {
		if child.Kind() != tjs.KindObject || child.IsNull() {
			continue
		}
		if s.motionPlayerAllPlaying(s.motionPlayer(child.ObjectRef().Object)) {
			return true
		}
	}
	return false
}

func motionNestedTreePlaying(resource *motionResource, attached []*motionResource, chara, motion string, tick, frameLoopTime, frameLastTime float64, params []float64, stack map[string]bool) bool {
	if resource == nil {
		return false
	}
	key := resource.path + "\x00" + chara + "\x00" + motion
	if stack[key] {
		return false
	}
	stack[key] = true
	defer delete(stack, key)
	layers := motionLayerList(resource, chara, motion)
	var visit func(assets.Value) bool
	visit = func(value assets.Value) bool {
		switch node := value.(type) {
		case []assets.Value:
			for _, child := range node {
				if visit(child) {
					return true
				}
			}
		case map[string]assets.Value:
			frames, _ := node["frameList"].([]assets.Value)
			content, visible := psbMotionResolvedContent(frames, motionNodeTick(node, tick, motionRenderEval{params: params}), psbMotionInt(node, "type", 0), psbMotionInt(node, "coordinate", 0))
			if visible && psbMotionInt(node, "type", 0) == 3 {
				if source, ok := content["src"].(string); ok && strings.HasPrefix(source, "motion/") {
					parts := splitMotionPath(strings.TrimPrefix(source, "motion/"))
					if len(parts) >= 2 {
						owner, _, nested := motionLookupMotion(resource, attached, parts[0], parts[1])
						if owner != nil && nested != nil {
							childTick := motionNestedTickAt(node, content, frameLoopTime, tick)
							childTick = motionWrapNestedTime(childTick, nested, frameLastTime < 0)
							totalFrames, ok := motionTotalFramesValue(nested)
							if !ok {
								totalFrames = 0
							}
							loopTime, hasLoopTime := motionLoopTimeValue(nested)
							if (hasLoopTime && loopTime >= 0) || childTick < totalFrames {
								return true
							}
							childFrameLoopTime := math.Max(childTick, 0)
							childTick = math.Min(childFrameLoopTime, math.Max(totalFrames, 0))
							if motionNestedTreePlaying(owner, attached, parts[0], parts[1], childTick, childFrameLoopTime, frameLastTime, params, stack) {
								return true
							}
						}
					}
				}
			}
			if child, exists := node["children"]; exists && visit(child) {
				return true
			}
		}
		return false
	}
	for _, layer := range layers {
		if visit(layer) {
			return true
		}
	}
	return false
}

func (s *Services) motionPlayerEmote(state *motionPlayerState) *emotePlayerState {
	if state.emote == nil {
		state.emote = newEmotePlayerState()
	}
	// PlayerRuntime::activeMotion is populated only by ensureMotionLoaded or an
	// explicit bind operation. Looking at the manager's last-loaded resource
	// here would activate a module on methods that the reference leaves lazy.
	resource := state.resource
	if resource != nil && state.metadataResource != resource {
		s.emoteResetRuntime(state.emote)
		s.emoteApplyResourceMetadata(state.emote, resource)
		state.metadataResource = resource
	}
	return state.emote
}

func (s *Services) motionPlayerEnsureLoaded(vm *tjs.VM, state *motionPlayerState) error {
	if state == nil {
		return nil
	}
	if state.resource == nil {
		var resource *motionResource
		if state.project.Kind() == tjs.KindObject && !state.project.IsNull() {
			s.mu.RLock()
			resource = s.MotionModules[state.project.ObjectRef().Object]
			s.mu.RUnlock()
		}
		motion := state.motion
		looksLikeStoragePath := strings.ContainsAny(motion, "/\\") || strings.Contains(motion, ".")
		if resource == nil && looksLikeStoragePath {
			resource = s.resolveMotionForPlayer(state, motion)
		}
		if resource == nil {
			resource = s.lastMotionResource(state.resourceManager)
		}
		if resource == nil && motion != "" {
			resource = s.resolveMotionForPlayer(state, motion)
		}
		if resource != nil {
			state.resource = resource
			state.metadataResource = nil
			state.motionTreeGeneration++
			state.playerTimelineLoaded = false
			state.playerTimelines = nil
			state.playingTimelineLabels = nil
			state.particleArrays = nil
			state.motionChildren = nil
			state.particleChildren = nil
			state.anchorStates = nil
			state.gpuMotion = nil
			state.gpuMotionValid = false
		}
	}
	if state.resource != nil && (state.metadataResource != state.resource || state.variableKeys.IsVoid()) {
		s.motionPlayerEmote(state)
		return s.motionPlayerSyncVariableKeys(vm, state)
	}
	return nil
}

func (s *Services) motionPlayerSyncVariableKeys(vm *tjs.VM, state *motionPlayerState) error {
	if vm == nil || state == nil {
		return nil
	}
	emote := s.motionPlayerEmote(state)
	values := make([]tjs.Value, len(emote.variableOrder))
	for index, label := range emote.variableOrder {
		values[index] = tjs.StringValue(label)
	}
	keys, err := vm.NewArray(values)
	if err != nil {
		return err
	}
	state.variableKeys = keys
	return nil
}

// motionPlayerTimelineRuntime gives Motion.Player an independent timeline
// runtime while reusing the parsed timeline definitions and variable tables.
func (s *Services) motionPlayerTimelineRuntime(state *motionPlayerState, emote *emotePlayerState) *emotePlayerState {
	if !state.playerTimelineLoaded || state.playerTimelineResource != state.metadataResource {
		state.playerTimelineLoaded = true
		state.playerTimelineResource = state.metadataResource
		state.playerTimelines = make(map[string]*emoteTimelineState, len(emote.timelines))
		for label, timeline := range emote.timelines {
			copied := *timeline
			copied.tracks = append([]emoteTimelineTrack(nil), timeline.tracks...)
			copied.controlCursor = append([]int(nil), timeline.controlCursor...)
			copied.controlValues = append([]float64(nil), timeline.controlValues...)
			copied.controlAnims = append([]emoteAnimator(nil), timeline.controlAnims...)
			state.playerTimelines[label] = &copied
		}
		state.playerTimelineOrder = append([]string(nil), emote.timelineOrder...)
		state.playerMainTimelines = append([]string(nil), emote.mainTimelines...)
		state.playerDiffTimelines = append([]string(nil), emote.diffTimelines...)
		state.playingTimelineLabels = nil
	}
	runtime := *emote
	runtime.timelines = state.playerTimelines
	runtime.timelineOrder = state.playerTimelineOrder
	runtime.mainTimelines = state.playerMainTimelines
	runtime.diffTimelines = state.playerDiffTimelines
	runtime.playingOrder = append([]string(nil), state.playingTimelineLabels...)
	return &runtime
}

func motionPlayerActiveClipTime(state *motionPlayerState, runtime *emotePlayerState) float64 {
	if state == nil || runtime == nil {
		return 0
	}
	if timeline := runtime.timelines[state.motion]; timeline != nil {
		return timeline.time
	}
	for _, label := range runtime.playingOrder {
		if timeline := runtime.timelines[label]; timeline != nil {
			return timeline.time
		}
	}
	primary := runtime.mainTimelines
	if len(primary) == 0 {
		primary = runtime.diffTimelines
	}
	for _, label := range primary {
		if timeline := runtime.timelines[label]; timeline != nil {
			return timeline.time
		}
	}
	if len(runtime.timelineOrder) > 0 {
		if timeline := runtime.timelines[runtime.timelineOrder[0]]; timeline != nil {
			return timeline.time
		}
	}
	return 0
}

func (s *Services) motionPlayerCommitTimelineRuntime(state *motionPlayerState, emote, runtime *emotePlayerState) {
	state.playerTimelines = runtime.timelines
	state.playerTimelineOrder = runtime.timelineOrder
	state.playerMainTimelines = runtime.mainTimelines
	state.playerDiffTimelines = runtime.diffTimelines
	state.playingTimelineLabels = append(state.playingTimelineLabels[:0], runtime.playingOrder...)
	emote.evalResults = runtime.evalResults
	emote.evalOrder = runtime.evalOrder
	emote.modified = runtime.modified
}

func (s *Services) motionPlayerUnserialize(vm *tjs.VM, state *motionPlayerState, data tjs.Value) error {
	if data.Kind() != tjs.KindObject || data.IsNull() {
		return nil
	}
	read := func(key string) (tjs.Value, bool) {
		value, found, err := vm.GetMemberIfPresent(data, key, s, nil)
		return value, found && err == nil
	}
	if value, found := read("chara"); found && value.Kind() != tjs.KindVoid {
		state.chara = value.Text()
	}
	if value, found := read("motion"); found && value.Kind() != tjs.KindVoid {
		motion := value.Text()
		if state.motion != motion {
			if err := s.motionPlayerSetMotionKey(vm, state, tjs.StringValue(motion)); err != nil {
				return err
			}
		} else if s.playerResource(state) == nil {
			if err := s.motionPlayerInitMotionProperty(vm, state); err != nil {
				return err
			}
		}
	}
	if value, found := read("tickcount"); found && value.Kind() != tjs.KindVoid {
		tickCount, err := value.Real()
		if err != nil {
			return err
		}
		state.frameTick = tickCount * motionFramesPerMillisecond
	}
	if value, found := read("speed"); found && value.Kind() != tjs.KindVoid {
		speed, err := value.Real()
		if err != nil {
			return err
		}
		state.speed = speed != 0
		state.speedSet = true
	}
	if value, found := read("outline"); found && value.Kind() != tjs.KindVoid {
		state.outline = value.Text()
	}

	emote := s.motionPlayerEmote(state)
	timelineRuntime := s.motionPlayerTimelineRuntime(state, emote)
	if value, found := read("variables"); found && value.Kind() == tjs.KindObject && !value.IsNull() {
		if object, ok := vm.Objects.Lookup(value.ObjectRef().Object); ok {
			for _, label := range object.Keys() {
				stored, err := tjs.GetMember(vm.Objects, value, label)
				if err != nil || stored.Kind() == tjs.KindVoid {
					continue
				}
				number, err := stored.Real()
				if err != nil {
					continue
				}
				s.emoteWriteEvalMode(emote, label, 0, number)
			}
		}
	}

	restoredTimelines := false
	if value, found := read("timelines"); found && value.Kind() == tjs.KindObject && !value.IsNull() {
		if s.playerResource(state) == nil {
			if err := s.motionPlayerInitMotionProperty(vm, state); err != nil {
				return err
			}
			emote = s.motionPlayerEmote(state)
			timelineRuntime = s.motionPlayerTimelineRuntime(state, emote)
		}
		timelineRuntime.playingOrder = nil
		state.playingTimelineLabels = nil
		items, err := vm.ArrayItems(value)
		if err == nil {
			for _, item := range items {
				if item.Kind() != tjs.KindObject || item.IsNull() {
					continue
				}
				labelValue, found, err := vm.GetMemberIfPresent(item, "label", s, nil)
				if err != nil || !found || labelValue.Kind() == tjs.KindVoid {
					continue
				}
				label := labelValue.Text()
				timeline := timelineRuntime.timelines[label]
				if timeline == nil {
					continue
				}
				restoredTimelines = true
				timeline.playing = true
				timelineRuntime.playingOrder = append(timelineRuntime.playingOrder, label)
				timeline.controlInit = false
				timeline.controlLast = timeline.time
				timeline.controlCursor = nil
				timeline.controlValues = nil
				timeline.controlAnims = nil
				if flags, found, err := vm.GetMemberIfPresent(item, "flags", s, nil); err == nil && found && flags.Kind() != tjs.KindVoid {
					if number, err := flags.Int(); err == nil {
						timeline.flags = number
					}
				}
				if currentTime, found, err := vm.GetMemberIfPresent(item, "currentTime", s, nil); err == nil && found && currentTime.Kind() != tjs.KindVoid {
					if number, err := currentTime.Real(); err == nil {
						timeline.time = number
					}
				}
				if blendRatio, found, err := vm.GetMemberIfPresent(item, "blendRatio", s, nil); err == nil && found && blendRatio.Kind() != tjs.KindVoid {
					if number, err := blendRatio.Real(); err == nil {
						timeline.blendRatio = number
					}
				}
			}
		}
	}
	if !restoredTimelines {
		if s.playerResource(state) == nil {
			if err := s.motionPlayerInitMotionProperty(vm, state); err != nil {
				return err
			}
			emote = s.motionPlayerEmote(state)
			timelineRuntime = s.motionPlayerTimelineRuntime(state, emote)
		}
		primary := timelineRuntime.mainTimelines
		if len(primary) == 0 {
			primary = timelineRuntime.diffTimelines
		}
		for _, label := range primary {
			s.emotePlayTimeline(timelineRuntime, label, motionPlayFlagForce)
		}
	}
	s.motionPlayerCommitTimelineRuntime(state, emote, timelineRuntime)
	state.playing = len(state.playingTimelineLabels) > 0
	return nil
}

// motionField reads one numeric field of a motion entry, returning fallback
// when the resource, the character, or the motion is unavailable.
func motionField(resource *motionResource, chara, motion, key string, fallback float64) float64 {
	return psbMotionFloat(motionEntry(resource, chara, motion), key, fallback)
}

func motionEntry(resource *motionResource, chara, motion string) map[string]assets.Value {
	if resource == nil || resource.doc == nil {
		return nil
	}
	root, _ := resource.doc.Root.(map[string]assets.Value)
	objects, _ := root["object"].(map[string]assets.Value)
	character, _ := objects[chara].(map[string]assets.Value)
	motions, _ := character["motion"].(map[string]assets.Value)
	value, _ := motions[motion].(map[string]assets.Value)
	return value
}

func motionLastTime(resource *motionResource, chara, motion string) float64 {
	if totalFrames, ok := motionTotalFramesValue(motionEntry(resource, chara, motion)); ok {
		return totalFrames
	}
	return -1
}

func motionLoopTime(resource *motionResource, chara, motion string) float64 {
	if loopTime, ok := motionLoopTimeValue(motionEntry(resource, chara, motion)); ok {
		return loopTime
	}
	return -1
}

func (s *Services) motionPlayerApplyLoopTime(state *motionPlayerState) {
	if state == nil || state.resource == nil {
		return
	}
	if loop, ok := motionLoopTimeValue(motionEntry(state.resource, state.chara, state.motion)); ok {
		state.loopTime = loop
		state.loopTimeSet = false
	}
}

func motionPlayerLoopTime(state *motionPlayerState, resource *motionResource) float64 {
	if state == nil {
		return -1
	}
	if !state.loopTimeSet {
		return motionLoopTime(resource, state.chara, state.motion)
	}
	return state.loopTime
}

func motionPlayerCalcViewParam(vm *tjs.VM, state *motionPlayerState) error {
	if vm == nil {
		return nil
	}
	view, err := vm.NewDictionary()
	if err != nil {
		return err
	}
	members := map[string]tjs.Value{
		"flip":        tjs.IntegerValue(boolInt(state.viewFlip)),
		"opacity":     tjs.RealValue(state.viewOpacity),
		"visible":     tjs.IntegerValue(boolInt(state.viewVisible)),
		"slant":       tjs.RealValue(state.viewSlant),
		"zoom":        tjs.RealValue(state.viewZoom),
		"zFactor":     tjs.RealValue(motionPlayerZFactor(state)),
		"colorWeight": tjs.IntegerValue(int64(motionSwapPackedRB(state.colorWeight))),
	}
	for name, value := range members {
		if err := tjs.SetMember(vm.Objects, view, name, value); err != nil {
			return err
		}
	}
	state.lastViewParam = view
	return nil
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
		state.motionClockActive = false
		state.held = true
		return
	}
	for guard := 0; state.frameTick >= last && guard < 1024; guard++ {
		state.frameTick += loop - last
	}
	if state.frameTick >= last {
		state.frameTick = last
		state.playing = false
		state.motionClockActive = false
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

func motionPlayerCommandCandidates(resource *motionResource, chara, motion string) []string {
	if resource == nil || resource.doc == nil {
		return nil
	}
	root, _ := resource.doc.Root.(map[string]assets.Value)
	objects, _ := root["object"].(map[string]assets.Value)
	character, _ := objects[chara].(map[string]assets.Value)
	motions, _ := character["motion"].(map[string]assets.Value)
	active, _ := motions[motion].(map[string]assets.Value)
	if active == nil {
		return nil
	}
	return motionCollectCommandCandidates(active)
}

func motionPlayerProgressCompat(state *motionPlayerState) float64 {
	if state == nil {
		return 1
	}
	if len(state.playerTimelines) == 0 {
		if state.playing {
			return 0
		}
		return 1
	}
	sawTimeline := false
	anyPlaying := false
	progress := 0.0
	for _, timeline := range state.playerTimelines {
		if timeline == nil {
			continue
		}
		sawTimeline = true
		anyPlaying = anyPlaying || timeline.playing
		if timeline.totalFrames > 0 {
			progress = math.Max(progress, math.Max(0, math.Min(1, timeline.time/timeline.totalFrames)))
		} else if !timeline.playing {
			progress = math.Max(progress, 1)
		}
	}
	if !sawTimeline {
		if state.playing {
			return 0
		}
		return 1
	}
	if !anyPlaying {
		return 1
	}
	return progress
}

func motionCollectCommandCandidates(value assets.Value) []string {
	seen := make(map[string]struct{})
	var result []string
	var visit func(assets.Value, []string, string)
	visit = func(current assets.Value, path []string, key string) {
		add := func(candidate string) {
			if candidate == "" {
				return
			}
			if _, exists := seen[candidate]; exists {
				return
			}
			seen[candidate] = struct{}{}
			result = append(result, candidate)
		}
		switch node := current.(type) {
		case string:
			lower := strings.ToLower(node)
			key = strings.ToLower(key)
			if strings.HasSuffix(lower, ".psb") || strings.HasSuffix(lower, ".pimg") ||
				strings.HasSuffix(lower, ".png") || strings.HasSuffix(lower, ".jpg") ||
				strings.HasSuffix(lower, ".jpeg") || strings.HasSuffix(lower, ".bmp") ||
				strings.HasSuffix(lower, ".tlg") || strings.HasSuffix(lower, ".webp") ||
				strings.Contains(key, "source") || strings.Contains(key, "path") ||
				strings.Contains(key, "file") || key == "src" {
				add(node)
			}
		case assets.Resource:
			joined := strings.Join(path, "/")
			if strings.Contains(joined, "/") {
				add(joined)
			}
		case []assets.Value:
			for index, child := range node {
				visit(child, append(path, fmt.Sprint(index)), "")
			}
		case map[string]assets.Value:
			keys := make([]string, 0, len(node))
			for childKey := range node {
				keys = append(keys, childKey)
			}
			sort.Strings(keys)
			for _, childKey := range keys {
				visit(node[childKey], append(path, childKey), childKey)
			}
		}
	}
	visit(value, nil, "")
	return result
}

func motionPlayerBool(value tjs.Value) bool {
	n, err := value.Int()
	return err == nil && n != 0
}

func motionSwapPackedRB(color uint32) uint32 {
	return color&0xFF00FF00 | (color>>16)&0xFF | (color&0xFF)<<16
}

func (s *Services) drawMotionTarget(vm *tjs.VM, state *motionPlayerState, value tjs.Value) error {
	if state != nil && state.hasRootState && (!state.rootState.active || !state.rootState.visible) {
		return nil
	}
	if adaptor := s.motionSeparateLayerAdaptorState(value); adaptor != nil {
		return s.drawMotionSeparateLayerAdaptor(vm, state, value, adaptor)
	}
	if d3dState := s.motionD3DAdaptorState(value); d3dState != nil {
		width, _ := d3dState.values["width"].Int()
		height, _ := d3dState.values["height"].Int()
		if width <= 0 || height <= 0 || !motionPlayerBool(d3dState.values["canvasCaptureEnabled"]) {
			return nil
		}
		layer := NewLayer(LayerID(value.ObjectRef().Object))
		layer.Bounds = Rect{Width: int32(width), Height: int32(height)}
		layer.ImageWidth, layer.ImageHeight = int32(width), int32(height)
		layer.RGBA = append([]byte(nil), d3dState.buffer...)
		if motionPlayerBool(d3dState.values["clearEnabled"]) {
			clearColor, _ := d3dState.values["clearColor"].Int()
			color := uint32(clearColor)
			for index := 0; index+3 < len(layer.RGBA); index += 4 {
				layer.RGBA[index] = byte(color >> 16)
				layer.RGBA[index+1] = byte(color >> 8)
				layer.RGBA[index+2] = byte(color)
				layer.RGBA[index+3] = byte(color >> 24)
			}
		}
		if err := s.drawMotionTargetLayer(vm, state, layer, true); err != nil {
			return err
		}
		s.mu.Lock()
		if current := s.MotionValueObjects[value.ObjectRef().Object]; current == d3dState {
			current.buffer = append(current.buffer[:0], layer.RGBA...)
		}
		s.mu.Unlock()
		return nil
	}
	layer, err := s.motionLayerFromValue(vm, value)
	if err != nil {
		return err
	}
	return s.drawMotionTargetLayer(vm, state, layer, false)
}

func (s *Services) motionSeparateLayerAdaptorState(value tjs.Value) *motionValueObjectState {
	if value.Kind() != tjs.KindObject || value.IsNull() {
		return nil
	}
	s.mu.RLock()
	state := s.MotionValueObjects[value.ObjectRef().Object]
	if state == nil || state.class != "SeparateLayerAdaptor" {
		state = nil
	}
	s.mu.RUnlock()
	return state
}

func (s *Services) motionD3DAdaptorState(value tjs.Value) *motionValueObjectState {
	if value.Kind() != tjs.KindObject || value.IsNull() {
		return nil
	}
	s.mu.RLock()
	state := s.MotionValueObjects[value.ObjectRef().Object]
	if state == nil || state.class != "D3DAdaptor" {
		state = nil
	}
	s.mu.RUnlock()
	return state
}

func (s *Services) drawMotionTargetLayer(vm *tjs.VM, state *motionPlayerState, layer *Layer, d3dTarget bool) error {
	resource := s.playerResource(state)
	if resource == nil {
		return fmt.Errorf("Motion.Player.draw has no loaded PSB resource")
	}
	if err := motionPlayerCalcViewParam(vm, state); err != nil {
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
		drawMatrix = state.emote.drawMatrixFor(drawMatrix, state)
		params = state.emote.renderParameters()
		attached = state.attached
		freezeBody = state.emote.freezeBodyMotion()
	}
	drawMatrix.tx += drawMatrix.a*state.x + drawMatrix.b*state.y
	drawMatrix.ty += drawMatrix.c*state.x + drawMatrix.d*state.y
	drawMatrix.tx += state.cameraOffsetX
	drawMatrix.ty += state.cameraOffsetY
	renderStats := &motionRenderStats{}
	renderOptions := motionRenderOptions{
		rootCoordinateMode:     state.rootCoordinateMode,
		emoteMode:              motionResourceEmoteMode(resource),
		parentFrameLoopTime:    state.frameLoopTime,
		hasParentFrameLoopTime: true,
		frameLastTime:          state.frameLastTime,
		anyTimelinePlaying:     len(state.playingTimelineLabels) > 0,
		rootState:              state.rootState,
		hasRootState:           state.hasRootState,
		anchorOutputs:          motionAnchorOutputs(state.anchorStates),
		preview:                state.preview,
		stats:                  renderStats,
	}
	if !d3dTarget && s.gpuCapture && layerHasCanvas(layer) && layer.Type == LayerAlpha {
		key := motionPlayerGPUKey{
			resource: resource, chara: state.chara, motion: state.motion,
			tick: state.clampedEvalTime, parentFrameLoopTime: state.frameLoopTime, hasParentFrameLoopTime: true, matrix: drawMatrix,
			zFactor:            motionPlayerZFactor(state),
			rootCoordinateMode: state.rootCoordinateMode,
			rootState:          state.rootState, hasRootState: state.hasRootState,
			scale: 1, maskMode: maskMode,
			completion: state.completionType, freezeBody: freezeBody,
			independentLayerInherit: state.independentLayerInherit,
			variables:               motionVariablesSignature(params) + "\x00" + motionAttachmentsSignature(attached) + "\x00anchor:" + motionAnchorOutputSignature(motionAnchorOutputs(state.anchorStates)),
			preview:                 state.preview,
		}
		motion := state.gpuMotion
		rebuilt := false
		if !state.gpuMotionValid || state.gpuMotionKey != key {
			var err error
			motion, err = buildGPUMotionVariablesZFactorInheritReuse(s.motionScratchForBuild(state, layer), resource, state.chara, state.motion, state.clampedEvalTime, 0, drawMatrix, 1, params, freezeBody, maskMode, state.completionType, motionPlayerZFactor(state), attached, state.independentLayerInherit, renderOptions)
			if err != nil {
				return err
			}
			rebuilt = true
			state.gpuMotionScratch = state.gpuMotion
			state.gpuMotion, state.gpuMotionKey, state.gpuMotionValid = motion, key, true
			// The counter reports the mesh vertices this draw rasterized; a
			// cached build keeps the previous total, like a skipped geometry
			// update (PlayerUpdateGeometry.cpp:518-523).
			state.processedMeshVerticesNum += int64(renderStats.meshVertices)
		}
		s.mu.Lock()
		changed := layer.gpuMotion != motion
		layer.gpuMotion = motion
		layer.Visible = true
		if changed || rebuilt {
			layer.imageModified = true
			layer.markImageDirty()
		}
		s.mu.Unlock()
		if motionTraceEnabled() {
			motionTrace("gpu-draw time=%d id=%d type=%d canvas=%v motion=%q frame=%.2f draws=%d cached=%v", s.Timers.Now(), state.playerID, layer.Type, layerHasCanvas(layer), state.motion, state.frameTick, len(motion.Draws), !changed)
		}
		return nil
	}
	var frame *assets.Image
	var err error
	if d3dTarget {
		frame, err = renderPSBMotionOptionsZFactorInheritDimensions(resource, state.chara, state.motion, state.clampedEvalTime, 0, params, freezeBody, maskMode, state.completionType, motionPlayerZFactor(state), attached, state.independentLayerInherit, int(layer.ImageWidth), int(layer.ImageHeight), renderOptions)
	} else {
		frame, err = renderPSBMotionOptionsZFactorInherit(resource, state.chara, state.motion, state.clampedEvalTime, 0, params, freezeBody, maskMode, state.completionType, motionPlayerZFactor(state), attached, state.independentLayerInherit, renderOptions)
	}
	if err != nil {
		return err
	}
	state.processedMeshVerticesNum += int64(renderStats.meshVertices)
	// Camera nodes offset prepared items in final draw space, so the offset
	// joins the draw matrix translation (PlayerRenderItems.cpp:1174-1215).
	baseDrawMatrix := drawMatrix
	drawMatrix = motionCameraDrawMatrixZFactorInherit(resource, state.chara, state.motion, state.clampedEvalTime, params, freezeBody, maskMode, state.completionType, motionPlayerZFactor(state), attached, drawMatrix, state.independentLayerInherit, renderOptions)
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

func (s *Services) motionScratchForBuild(state *motionPlayerState, target *Layer) *RenderMotion {
	if s == nil || state == nil || state.gpuMotionScratch == nil {
		return nil
	}
	scratch := state.gpuMotionScratch
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, candidate := range s.Layers {
		if candidate != nil && candidate != target && candidate.gpuMotion == scratch {
			return nil
		}
	}
	return scratch
}

func layerHasCanvas(layer *Layer) bool {
	return layer.validImage()
}
func (s *Services) motionSeparateLayerAdaptorNative(vm *tjs.VM, context tjs.Value, operation string, args []tjs.Value) (tjs.Value, error) {
	id := context.ObjectRef().Object
	switch operation {
	case "@initialize", "SeparateLayerAdaptor":
		state := newMotionValueObjectState("SeparateLayerAdaptor")
		if len(args) > 0 && args[0].Kind() == tjs.KindObject && !args[0].IsNull() {
			state.values["targetLayer"] = args[0]
			if owner, found, err := vm.GetMemberIfPresent(args[0], "window", s, nil); err == nil && found {
				state.values["owner"] = owner
			}
			s.mu.Lock()
			s.MotionAdaptors[id] = args[0].ObjectRef().Object
			s.MotionValueObjects[id] = state
			s.mu.Unlock()
		} else {
			s.mu.Lock()
			s.MotionValueObjects[id] = state
			s.mu.Unlock()
		}
		return tjs.VoidValue, nil
	case "@invalidate", "finalize":
		s.mu.RLock()
		state := s.MotionValueObjects[id]
		s.mu.RUnlock()
		if state != nil {
			if err := s.clearMotionSeparateLayerAdaptor(vm, id, state); err != nil {
				return tjs.VoidValue, err
			}
		}
		s.mu.Lock()
		delete(s.MotionAdaptors, id)
		delete(s.MotionValueObjects, id)
		s.mu.Unlock()
		return tjs.VoidValue, nil
	case "set:absolute", "set:targetLayer":
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("Motion.SeparateLayerAdaptor.%s requires a value", strings.TrimPrefix(operation, "set:"))
		}
		s.mu.Lock()
		state := s.MotionValueObjects[id]
		if state == nil {
			state = newMotionValueObjectState("SeparateLayerAdaptor")
			s.MotionValueObjects[id] = state
		}
		if operation == "set:absolute" {
			value, err := args[0].Int()
			if err != nil {
				s.mu.Unlock()
				return tjs.VoidValue, err
			}
			state.values["absolute"] = tjs.IntegerValue(value)
		} else {
			state.values["targetLayer"] = args[0]
			if args[0].Kind() == tjs.KindObject && !args[0].IsNull() {
				s.MotionAdaptors[id] = args[0].ObjectRef().Object
			} else {
				delete(s.MotionAdaptors, id)
			}
		}
		s.mu.Unlock()
		return tjs.VoidValue, nil
	case "get:absolute", "get:targetLayer":
		property := strings.TrimPrefix(operation, "get:")
		s.mu.RLock()
		state := s.MotionValueObjects[id]
		var value tjs.Value
		if state != nil {
			value = state.values[property]
		}
		s.mu.RUnlock()
		if state == nil {
			return tjs.VoidValue, nil
		}
		return value, nil
	case "clear":
		s.mu.RLock()
		state := s.MotionValueObjects[id]
		s.mu.RUnlock()
		if state == nil {
			return tjs.VoidValue, nil
		}
		return tjs.VoidValue, s.clearMotionSeparateLayerAdaptor(vm, id, state)
	case "assign":
		if len(args) == 0 || args[0].Kind() != tjs.KindObject || args[0].IsNull() {
			return tjs.VoidValue, nil
		}
		s.mu.RLock()
		target := s.MotionValueObjects[id]
		s.mu.RUnlock()
		if target == nil {
			return tjs.VoidValue, nil
		}
		return tjs.VoidValue, s.assignMotionSeparateLayerAdaptor(vm, id, target, args[0])
	case "layerTreeOwnerInterface":
		s.mu.RLock()
		state := s.MotionValueObjects[id]
		var owner tjs.Value
		if state != nil {
			owner = state.values["owner"]
		}
		s.mu.RUnlock()
		if owner.Kind() != tjs.KindObject || owner.IsNull() {
			return tjs.VoidValue, nil
		}
		value, found, err := vm.GetMemberIfPresent(owner, "layerTreeOwnerInterface", s, nil)
		if err != nil || !found {
			return tjs.VoidValue, nil
		}
		return value, nil
	default:
		return tjs.VoidValue, fmt.Errorf("unsupported Motion.SeparateLayerAdaptor operation %q", operation)
	}
}
