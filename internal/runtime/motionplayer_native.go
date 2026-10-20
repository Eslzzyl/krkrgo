package runtime

import (
	"fmt"

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
	for _, method := range []string{"setEmotePSBDecryptSeed", "getEmotePSBDecryptSeed"} {
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
		"initPhysics",
		"progress", "clear", "draw", "setVariable", "getVariable",
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
		"requireLayerId", "releaseLayerId", "isPlaying",
		"isExistMotion", "findMotion",
		"unload", "unloadAll",
		"releaseSyncWait", "getLayerNames",
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
	for _, method := range []string{"@initialize", "@invalidate", "SeparateLayerAdaptor", "finalize"} {
		if err := vm.RegisterNativeMethod(adaptor, method, "Motion.SeparateLayerAdaptor."+method); err != nil {
			return err
		}
	}
	return nil
}

func (s *Services) motionResourceManagerNative(vm *tjs.VM, context tjs.Value, operation string, args []tjs.Value) (tjs.Value, error) {
	switch operation {
	case "@initialize", "@invalidate", "ResourceManager", "finalize":
		return tjs.VoidValue, nil
	case "setEmotePSBDecryptSeed":
		if len(args) != 1 {
			return tjs.VoidValue, fmt.Errorf("Motion.ResourceManager.setEmotePSBDecryptSeed requires one integer")
		}
		seed, err := args[0].Int()
		if err != nil {
			return tjs.VoidValue, err
		}
		if seed < -2147483648 || seed > 2147483647 {
			return tjs.VoidValue, fmt.Errorf("Motion.ResourceManager.setEmotePSBDecryptSeed: seed out of range")
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
	case "requireLayerId":
		if len(args) != 0 {
			return tjs.VoidValue, fmt.Errorf("Motion.ResourceManager.requireLayerId takes no arguments")
		}
		return tjs.IntegerValue(s.requireMotionLayerID(context.ObjectRef().Object, "", false)), nil
	case "releaseLayerId":
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("Motion.ResourceManager.releaseLayerId requires an id")
		}
		id, err := args[0].Int()
		if err != nil {
			return tjs.VoidValue, err
		}
		s.releaseMotionLayerID(context.ObjectRef().Object, id)
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
		manager := context.ObjectRef().Object
		resource := s.motionResource(path, manager)
		if resource != nil {
			value, found, err := s.motionModuleFromCache(vm, cache, manager, path)
			if err != nil {
				return tjs.VoidValue, err
			}
			if found {
				return value, nil
			}
			value, err = psbValueToTJS(vm, resource.doc.Root, resource.data, 0)
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
			return tjs.VoidValue, err
		}
		s.setMotionResource(manager, resource)
		value, err := psbValueToTJS(vm, resource.doc.Root, resource.data, 0)
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
		manager := context.ObjectRef().Object
		resource := s.motionResource(path, manager)
		paths := motionResourcePathAliases(path)
		if resource != nil {
			paths = append(paths, s.motionResourceCachePaths(manager, resource)...)
			paths = append(paths, motionResourcePathAliases(resource.path)...)
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
		s.clearMotionResourceCache(context.ObjectRef().Object)
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
		manager := context.ObjectRef().Object
		resource := s.motionResource(path, manager)
		if resource == nil {
			return tjs.VoidValue, nil
		}
		value, found, err := s.motionModuleFromCache(vm, cache, manager, path)
		if err != nil {
			return tjs.VoidValue, err
		}
		if found {
			return value, nil
		}
		value, err = psbValueToTJS(vm, resource.doc.Root, resource.data, 0)
		if err != nil {
			return tjs.VoidValue, err
		}
		s.registerMotionModule(value, resource)
		if err := tjs.SetMember(vm.Objects, cache, path, value); err != nil {
			return tjs.VoidValue, err
		}
		s.rememberMotionResourceCachePath(manager, resource, path)
		return value, nil
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
	for _, alias := range motionResourcePathAliases(path) {
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
