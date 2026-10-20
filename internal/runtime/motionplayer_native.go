package runtime

import (
	"errors"
	"fmt"

	"github.com/eslzzyl/krkrgo/internal/assets"
	"github.com/eslzzyl/krkrgo/internal/tjs"
)

// installMotionPlayerNative installs the portable Motion namespace, PSB
// resource loading, and the player surface used by motion-backed layers.
func installMotionPlayerNative(vm *tjs.VM) error {
	if vm == nil {
		return fmt.Errorf("motionplayer.dll requires a VM")
	}
	if _, installed := vm.Globals["Motion"]; installed {
		return nil
	}
	motion, err := vm.RegisterNativeClass("Motion")
	if err != nil {
		return err
	}
	class, ok := vm.Objects.Lookup(motion.ObjectRef().Object)
	if !ok {
		return fmt.Errorf("Motion native namespace is not registered")
	}
	for name, value := range map[string]int64{
		"LayerTypeObj": 0, "LayerTypeShape": 1, "LayerTypeLayout": 2,
		"LayerTypeMotion": 3, "LayerTypeParticle": 4, "LayerTypeCamera": 5,
		"ShapeTypePoint": 0, "ShapeTypeCircle": 1, "ShapeTypeRect": 2,
		"ShapeTypeQuad": 3,
		"PlayFlagForce": 1, "PlayFlagChain": 2, "PlayFlagAsCan": 4,
		"PlayFlagJoin": 8, "PlayFlagStealth": 16,
		"TransformOrderFlip": 0, "TransformOrderAngle": 1,
		"TransformOrderZoom": 2, "TransformOrderSlant": 3,
		"CoordinateRecutangularXY": 0, "CoordinateRecutangularXZ": 1,
		"MaskModeStencil": 0, "MaskModeAlpha": 1,
	} {
		if err := class.Set(name, tjs.IntegerValue(value)); err != nil {
			return err
		}
	}
	resourceManager, err := vm.RegisterNativeClass("Motion.ResourceManager")
	if err != nil {
		return err
	}
	if err := class.Set("ResourceManager", resourceManager); err != nil {
		return err
	}
	for _, method := range []string{"@initialize", "@invalidate", "ResourceManager", "finalize", "load", "loadSource", "unload", "clearCache", "findSource"} {
		if err := vm.RegisterNativeMethod(resourceManager, method, "Motion.ResourceManager."+method); err != nil {
			return err
		}
	}
	for _, method := range []string{"getEmotePSBDecryptSeed", "setEmotePSBDecryptSeed", "setEmotePSBDecryptFunc"} {
		if err := vm.RegisterNativeStaticMethod(resourceManager, method, "Motion.ResourceManager."+method); err != nil {
			return err
		}
	}
	for _, method := range []string{"requireLayerId", "releaseLayerId"} {
		if err := vm.RegisterNativeMethod(resourceManager, method, "Motion.ResourceManager."+method); err != nil {
			return err
		}
	}
	player, err := vm.RegisterNativeClass("Motion.Player")
	if err != nil {
		return err
	}
	if err := class.Set("Player", player); err != nil {
		return err
	}
	for _, method := range []string{
		"@initialize", "@invalidate", "Player", "finalize", "play", "stop",
		"random", "initPhysics", "frameProgress",
		"progress", "draw", "setVariable", "getVariable",
		"countVariables", "getVariableLabelAt", "countVariableFrameAt",
		"getVariableFrameLabelAt", "getVariableFrameValueAt", "getVariableRange",
		"getVariableFrameList", "getTimelinePlaying", "countMainTimelines",
		"getMainTimelineLabelAt", "getMainTimelineLabelList", "countDiffTimelines",
		"getDiffTimelineLabelAt", "getDiffTimelineLabelList", "getLoopTimeline",
		"countPlayingTimelines", "getPlayingTimelineLabelAt",
		"getPlayingTimelineFlagsAt", "getTimelineTotalFrameCount", "playTimeline",
		"stopTimeline", "setTimelineBlendRatio", "getTimelineBlendRatio",
		"fadeInTimeline", "fadeOutTimeline", "getPlayingTimelineInfoList",
		"getCameraOffset", "setCameraOffset", "setStereovisionCameraPosition",
		"modifyRoot", "skipToSync", "setDrawAffineTranslateMatrix",
		"isSelectorTarget", "deactivateSelectorTarget",
		"captureCanvas", "clearCache", "clear",
		"findSource", "loadSource",
		"getCommandList", "onFindMotion",
		"setHairScale", "setPartsScale", "setBustScale", "setRotate", "setMirror",
		"requireLayerId", "releaseLayerId", "isPlaying",
		"isExistMotion", "findMotion",
		"unload", "unloadAll",
		"releaseSyncWait", "getLayerNames",
		"getLayerMotion", "getLayerGetter", "getLayerGetterList",
		"copyRect", "adjustGamma", "unloadUnusedTextures", "doAlphaMaskOperation",
		"setClearColor", "setResizable", "removeAllTextures", "removeAllBg",
		"removeAllCaption", "registerBg", "registerCaption", "alphaOpAdd",
		"setSize", "calcViewParam", "setFlip", "setOpacity", "setVisible",
		"setSlant", "setZoom",
		"serialize", "unserialize", "motionList", "getD3DAvailable",
		"debugPrint", "emoteEdit",
	} {
		if err := vm.RegisterNativeMethod(player, method, "Motion.Player."+method); err != nil {
			return err
		}
	}
	for _, property := range []string{
		"chara", "motion", "stealthMotion", "tickCount", "frameTickCount",
		"motionKey", "metadata", "x", "y", "left", "top", "frameLastTime",
		"frameLoopTime", "loopTime",
		"priorDraw", "processedMeshVerticesNum", "queuing", "directEdit",
		"selectorEnabled", "syncWaiting", "syncActive", "hasCamera",
		"cameraActive", "stereovisionActive", "cameraTarget", "cameraPosition",
		"cameraFOV", "cameraAlive", "speed", "playing", "allplaying",
		"canvasCaptureEnabled", "clearEnabled", "hitThreshold", "preview",
		"outsideFactor", "resourceManager", "stealthChara", "project",
		"useD3D", "meshline", "busy",
		"tags", "variableKeys", "completionType", "maskMode", "colorWeight", "zFactor",
		"independentLayerInherit",
		"outline", "onAction",
	} {
		setter := "Motion.Player.set:" + property
		switch property {
		case "playing", "allplaying", "busy":
			setter = ""
		}
		if err := vm.RegisterNativeProperty(player, property, "Motion.Player.get:"+property, setter); err != nil {
			return err
		}
	}
	adaptor, err := vm.RegisterNativeClass("Motion.SeparateLayerAdaptor")
	if err != nil {
		return err
	}
	if err := class.Set("SeparateLayerAdaptor", adaptor); err != nil {
		return err
	}
	for _, method := range []string{"@initialize", "@invalidate", "SeparateLayerAdaptor", "finalize", "clear", "assign", "layerTreeOwnerInterface"} {
		if err := vm.RegisterNativeMethod(adaptor, method, "Motion.SeparateLayerAdaptor."+method); err != nil {
			return err
		}
	}
	for _, property := range []string{"absolute", "targetLayer"} {
		if err := vm.RegisterNativeProperty(adaptor, property, "Motion.SeparateLayerAdaptor.get:"+property, "Motion.SeparateLayerAdaptor.set:"+property); err != nil {
			return err
		}
	}
	for _, spec := range []struct {
		name       string
		properties []string
	}{
		{name: "Point", properties: []string{"type", "x", "y"}},
		{name: "Circle", properties: []string{"type", "x", "y", "r"}},
		{name: "Rect", properties: []string{"type", "l", "t", "w", "h"}},
		{name: "Quad", properties: []string{"type", "p"}},
		{name: "LayerGetter", properties: []string{
			"type", "label", "visible", "branchVisible", "layerVisible",
			"x", "y", "left", "top", "flipX", "flipY", "zoomX", "zoomY",
			"angleDeg", "angleRad", "slantX", "slantY", "originX", "originY",
			"opacity", "mtx", "vtx", "color", "bezierPatch", "shape", "motion", "particle",
		}},
		{name: "SourceCache", properties: []string{"bufLayer"}},
		{name: "D3DAdaptor", properties: []string{"visible", "alphaOpAdd", "canvasCaptureEnabled", "clearEnabled"}},
		{name: "ObjSource"},
	} {
		motionValueClass, err := vm.RegisterNativeClass("Motion." + spec.name)
		if err != nil {
			return err
		}
		if err := class.Set(spec.name, motionValueClass); err != nil {
			return err
		}
		if err := vm.RegisterNativeMethod(motionValueClass, "@initialize", "Motion."+spec.name+".construct"); err != nil {
			return err
		}
		if err := vm.RegisterNativeMethod(motionValueClass, "@invalidate", "Motion."+spec.name+".invalidate"); err != nil {
			return err
		}
		for _, method := range []string{"contains"} {
			if spec.name == "Point" || spec.name == "Circle" || spec.name == "Rect" || spec.name == "Quad" {
				if err := vm.RegisterNativeMethod(motionValueClass, method, "Motion."+spec.name+"."+method); err != nil {
					return err
				}
			}
		}
		if spec.name == "SourceCache" {
			for _, method := range []string{"loadSource", "clearCache"} {
				if err := vm.RegisterNativeMethod(motionValueClass, method, "Motion.SourceCache."+method); err != nil {
					return err
				}
			}
		}
		if spec.name == "D3DAdaptor" {
			for _, method := range []string{"setPos", "setSize", "setClearColor", "setResizable", "removeAllTextures", "removeAllBg", "removeAllCaption", "registerBg", "registerCaption", "unloadUnusedTextures", "captureCanvas"} {
				if err := vm.RegisterNativeMethod(motionValueClass, method, "Motion.D3DAdaptor."+method); err != nil {
					return err
				}
			}
		}
		for _, property := range spec.properties {
			setter := ""
			if spec.name == "D3DAdaptor" {
				setter = "Motion.D3DAdaptor.set:" + property
			}
			if err := vm.RegisterNativeProperty(motionValueClass, property, "Motion."+spec.name+".get:"+property, setter); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Services) motionResourceManagerID(id tjs.ObjectID) tjs.ObjectID {
	if id == 0 {
		return 0
	}
	s.ensureInit()
	s.mu.RLock()
	for i := 0; i < len(s.MotionResourceManagerAliases); i++ {
		parent := s.MotionResourceManagerAliases[id]
		if parent == 0 || parent == id {
			break
		}
		id = parent
	}
	s.mu.RUnlock()
	return id
}

func (s *Services) newPlayerResourceManagerView(vm *tjs.VM, source tjs.Value) (tjs.Value, tjs.ObjectID, error) {
	var sourceID tjs.ObjectID
	cacheSize := int64(20 * 1024 * 1024)
	if source.Kind() == tjs.KindObject && !source.IsNull() {
		sourceID = s.motionResourceManagerID(source.ObjectRef().Object)
		s.mu.RLock()
		state := s.MotionValueObjects[source.ObjectRef().Object]
		isResourceManager := state != nil && state.class == "ResourceManager"
		if isResourceManager {
			if value, ok := state.values["cacheSize"]; ok {
				if n, err := value.Int(); err == nil && n > 0 {
					cacheSize = n
				}
			}
		}
		s.mu.RUnlock()
		if !isResourceManager {
			return tjs.VoidValue, 0, fmt.Errorf("Motion.Player requires a Motion.ResourceManager")
		}
	}
	motionNamespace, found := vm.Globals["Motion"]
	if !found {
		return tjs.VoidValue, 0, fmt.Errorf("Motion namespace is unavailable")
	}
	class, err := vm.GetMember(motionNamespace, "ResourceManager", s, nil)
	if err != nil {
		return tjs.VoidValue, 0, err
	}
	view, err := vm.Construct(class, []tjs.Value{tjs.NullValue, tjs.IntegerValue(cacheSize)}, s, nil)
	if err != nil {
		return tjs.VoidValue, 0, err
	}
	if view.Kind() != tjs.KindObject || view.IsNull() {
		return tjs.VoidValue, 0, fmt.Errorf("Motion.Player ResourceManager view construction returned no object")
	}
	viewID := view.ObjectRef().Object
	if sourceID == 0 {
		sourceID = viewID
	} else {
		s.mu.Lock()
		s.MotionResourceManagerAliases[viewID] = sourceID
		s.mu.Unlock()
	}
	return view, sourceID, nil
}

func (s *Services) motionResourceManagerNative(vm *tjs.VM, context tjs.Value, operation string, args []tjs.Value) (tjs.Value, error) {
	s.ensureInit()
	objectID := context.ObjectRef().Object
	managerID := s.motionResourceManagerID(objectID)
	switch operation {
	case "@initialize", "ResourceManager":
		if len(args) != 2 {
			return tjs.VoidValue, fmt.Errorf("Motion.ResourceManager requires a KAG object and cache size")
		}
		if args[0].Kind() != tjs.KindObject {
			return tjs.VoidValue, fmt.Errorf("Motion.ResourceManager requires a KAG object")
		}
		cacheSize, err := args[1].Int()
		if err != nil {
			return tjs.VoidValue, err
		}
		if cacheSize <= 0 {
			cacheSize = 20 * 1024 * 1024
		}
		id := context.ObjectRef().Object
		s.mu.Lock()
		state := s.MotionValueObjects[id]
		if state == nil {
			state = newMotionValueObjectState("ResourceManager")
			s.MotionValueObjects[id] = state
		}
		state.values["cacheSize"] = tjs.IntegerValue(cacheSize)
		s.mu.Unlock()
		if args[0].IsNull() {
			return tjs.VoidValue, nil
		}
		for _, key := range []string{"ShortCutInitialPadKeyMap", "ShortCutInitialGamePadKeyMap", "_proceedingKeyList"} {
			value, found, getErr := vm.GetMemberIfPresent(args[0], key, s, nil)
			if getErr == nil && found && !value.IsVoid() {
				continue
			}
			dictionary, err := vm.NewDictionary()
			if err != nil {
				return tjs.VoidValue, err
			}
			if err := vm.SetMemberWithRuntime(args[0], key, dictionary, 0, s, nil); err != nil {
				return tjs.VoidValue, err
			}
		}
		return tjs.VoidValue, nil
	case "@invalidate", "finalize":
		s.mu.Lock()
		delete(s.MotionResourceManagerAliases, objectID)
		s.mu.Unlock()
		return tjs.VoidValue, nil
	case "setEmotePSBDecryptSeed":
		if len(args) != 1 {
			return tjs.VoidValue, fmt.Errorf("Motion.ResourceManager.setEmotePSBDecryptSeed requires one integer")
		}
		if args[0].Kind() != tjs.KindInteger {
			return tjs.VoidValue, fmt.Errorf("Motion.ResourceManager.setEmotePSBDecryptSeed requires an integer value")
		}
		seed, err := args[0].Int()
		if err != nil {
			return tjs.VoidValue, err
		}
		s.mu.Lock()
		s.EmotePSBDecryptSeed = int32(seed)
		s.mu.Unlock()
		return tjs.VoidValue, nil
	case "getEmotePSBDecryptSeed":
		s.mu.RLock()
		seed := s.EmotePSBDecryptSeed
		s.mu.RUnlock()
		return tjs.IntegerValue(int64(seed)), nil
	case "setEmotePSBDecryptFunc":
		if len(args) != 1 {
			return tjs.VoidValue, fmt.Errorf("Motion.ResourceManager.setEmotePSBDecryptFunc requires one value")
		}
		s.mu.Lock()
		s.EmotePSBDecryptFunc = args[0]
		s.mu.Unlock()
		return tjs.VoidValue, nil
	case "requireLayerId":
		if len(args) != 0 {
			return tjs.VoidValue, fmt.Errorf("Motion.ResourceManager.requireLayerId takes no arguments")
		}
		return tjs.IntegerValue(s.requireMotionLayerID(managerID, "", false)), nil
	case "releaseLayerId":
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("Motion.ResourceManager.releaseLayerId requires an id")
		}
		id, err := args[0].Int()
		if err != nil {
			return tjs.VoidValue, err
		}
		s.releaseMotionLayerID(managerID, id)
		return tjs.VoidValue, nil
	case "load", "loadSource":
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("Motion.ResourceManager.%s: missing storage path", operation)
		}
		cache, err := s.motionResourceCache(vm, context)
		if err != nil {
			return tjs.VoidValue, err
		}
		path := args[0].Text()
		manager := managerID
		resource := s.motionResource(path, manager)
		if resource != nil {
			value, found, err := s.motionModuleValueForResource(vm, manager, resource)
			if err != nil {
				return tjs.VoidValue, err
			}
			if found {
				return value, nil
			}
			value, err = psbValueToTJS(vm, motionModuleDocument(resource).Root, resource.data, 0)
			if err != nil {
				return tjs.VoidValue, err
			}
			s.registerMotionModule(value, resource)
			if err := tjs.SetMember(vm.Objects, cache, path, value); err != nil {
				return tjs.VoidValue, err
			}
			s.rememberMotionResourceCachePath(manager, resource, path)
			return value, nil
		}
		resource, err = s.loadMotionResource(path)
		if err != nil {
			if errors.Is(err, assets.ErrNotFound) {
				return tjs.VoidValue, nil
			}
			return tjs.VoidValue, err
		}
		s.setMotionResource(manager, resource)
		value, err := psbValueToTJS(vm, motionModuleDocument(resource).Root, resource.data, 0)
		if err != nil {
			return tjs.VoidValue, err
		}
		s.registerMotionModule(value, resource)
		if err := tjs.SetMember(vm.Objects, cache, path, value); err != nil {
			return tjs.VoidValue, err
		}
		s.rememberMotionResourceCachePath(manager, resource, path)
		return value, nil
	case "unload":
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("Motion.ResourceManager.unload: missing storage path")
		}
		cache, err := s.motionResourceCache(vm, context)
		if err != nil {
			return tjs.VoidValue, err
		}
		path := args[0].Text()
		manager := managerID
		resource := s.motionResource(path, manager)
		paths := s.motionResourcePathAliases(path)
		if resource != nil {
			paths = append(paths, s.motionResourceCachePaths(manager, resource)...)
			paths = append(paths, s.motionResourcePathAliases(resource.path)...)
		}
		for _, cachePath := range paths {
			if tjs.HasMember(vm.Objects, cache, cachePath) {
				if err := tjs.DeleteMember(vm.Objects, cache, cachePath); err != nil {
					return tjs.VoidValue, err
				}
			}
		}
		s.unloadMotionResource(manager, path)
		return tjs.VoidValue, nil
	case "clearCache":
		cache, err := vm.NewDictionary()
		if err != nil {
			return tjs.VoidValue, err
		}
		s.clearMotionResourceCache(managerID)
		return tjs.VoidValue, tjs.SetMember(vm.Objects, context, "_motionResourceCache", cache)
	case "findSource":
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("Motion.ResourceManager.findSource: missing storage path")
		}
		cache, err := s.motionResourceCache(vm, context)
		if err != nil {
			return tjs.VoidValue, err
		}
		path := args[0].Text()
		manager := managerID
		candidates := append([]string{path}, motionResourceLookupCandidates(path)...)
		for _, candidate := range candidates {
			resource := s.motionResource(candidate, manager)
			if resource == nil {
				continue
			}
			value, found, err := s.motionModuleValueForResource(vm, manager, resource)
			if err != nil {
				return tjs.VoidValue, err
			}
			if found {
				return value, nil
			}
			value, err = psbValueToTJS(vm, motionModuleDocument(resource).Root, resource.data, 0)
			if err != nil {
				return tjs.VoidValue, err
			}
			s.registerMotionModule(value, resource)
			if err := tjs.SetMember(vm.Objects, cache, candidate, value); err != nil {
				return tjs.VoidValue, err
			}
			s.rememberMotionResourceCachePath(manager, resource, candidate)
			return value, nil
		}
		return tjs.VoidValue, nil
	default:
		return tjs.VoidValue, fmt.Errorf("unsupported Motion.ResourceManager operation %q", operation)
	}
}

func (s *Services) motionModuleValueForResource(vm *tjs.VM, manager tjs.ObjectID, resource *motionResource) (tjs.Value, bool, error) {
	if vm == nil || resource == nil {
		return tjs.VoidValue, false, nil
	}
	paths := s.motionResourceCachePaths(manager, resource)
	if len(paths) == 0 {
		return tjs.VoidValue, false, nil
	}
	cache, err := s.motionResourceCache(vm, tjs.ObjectIDValue(manager))
	if err != nil {
		return tjs.VoidValue, false, err
	}
	for _, path := range paths {
		value, found, err := s.motionModuleFromCache(vm, cache, manager, path)
		if err != nil {
			return tjs.VoidValue, false, err
		}
		if found {
			return value, true, nil
		}
	}
	return tjs.VoidValue, false, nil
}

func (s *Services) motionModuleFromCache(vm *tjs.VM, cache tjs.Value, manager tjs.ObjectID, path string) (tjs.Value, bool, error) {
	expected := s.motionResource(path, manager)
	if expected == nil {
		return tjs.VoidValue, false, nil
	}
	for _, alias := range s.motionResourcePathAliases(path) {
		if !tjs.HasMember(vm.Objects, cache, alias) {
			continue
		}
		value, err := tjs.GetMember(vm.Objects, cache, alias)
		if err != nil {
			return tjs.VoidValue, false, err
		}
		if value.Kind() != tjs.KindObject || value.IsNull() {
			continue
		}
		s.mu.RLock()
		resource := s.MotionModules[value.ObjectRef().Object]
		s.mu.RUnlock()
		if resource == expected {
			return value, true, nil
		}
	}
	return tjs.VoidValue, false, nil
}

// registerMotionModule lets EmotePlayer.load(module) resolve the snapshot the
// same way the reference lookupModuleSnapshot does.
func (s *Services) registerMotionModule(value tjs.Value, resource *motionResource) {
	if resource == nil || value.Kind() != tjs.KindObject || value.IsNull() {
		return
	}
	s.mu.Lock()
	s.MotionModules[value.ObjectRef().Object] = resource
	s.mu.Unlock()
}

func (s *Services) motionResourceCache(vm *tjs.VM, context tjs.Value) (tjs.Value, error) {
	if tjs.HasMember(vm.Objects, context, "_motionResourceCache") {
		return tjs.GetMember(vm.Objects, context, "_motionResourceCache")
	}
	cache, err := vm.NewDictionary()
	if err != nil {
		return tjs.VoidValue, err
	}
	if err := tjs.SetMember(vm.Objects, context, "_motionResourceCache", cache); err != nil {
		return tjs.VoidValue, err
	}
	return cache, nil
}
