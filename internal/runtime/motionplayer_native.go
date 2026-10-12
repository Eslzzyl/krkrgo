package runtime

import (
	"fmt"

	"github.com/eslzzyl/krkrgo/internal/tjs"
)

// installMotionPlayerNative installs the portable Motion namespace and its
// storage-backed ResourceManager subset. Motion's animation/rendering classes
// remain unavailable; the namespace itself is not an empty success shim.
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
	for _, method := range []string{"requireLayerId", "releaseLayerId"} {
		if err := vm.RegisterNativeMethod(resourceManager, method, "Motion.ResourceManager.unsupported:"+method); err != nil {
			return err
		}
	}
	return nil
}

func (s *Services) motionResourceManagerNative(vm *tjs.VM, context tjs.Value, operation string, args []tjs.Value) (tjs.Value, error) {
	switch operation {
	case "@initialize", "@invalidate", "ResourceManager", "finalize":
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
		if tjs.HasMember(vm.Objects, cache, path) {
			return tjs.GetMember(vm.Objects, cache, path)
		}
		value, err := s.loadPSBRoot(vm, path)
		if err != nil {
			return tjs.VoidValue, err
		}
		if err := tjs.SetMember(vm.Objects, cache, path, value); err != nil {
			return tjs.VoidValue, err
		}
		return value, nil
	case "unload":
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("Motion.ResourceManager.unload: missing storage path")
		}
		cache, err := s.motionResourceCache(vm, context)
		if err != nil {
			return tjs.VoidValue, err
		}
		return tjs.VoidValue, tjs.DeleteMember(vm.Objects, cache, args[0].Text())
	case "clearCache":
		cache, err := vm.NewDictionary()
		if err != nil {
			return tjs.VoidValue, err
		}
		return tjs.VoidValue, tjs.SetMember(vm.Objects, context, "_motionResourceCache", cache)
	case "findSource":
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("Motion.ResourceManager.findSource: missing storage path")
		}
		cache, err := s.motionResourceCache(vm, context)
		if err != nil {
			return tjs.VoidValue, err
		}
		if tjs.HasMember(vm.Objects, cache, args[0].Text()) {
			return tjs.GetMember(vm.Objects, cache, args[0].Text())
		}
		return tjs.VoidValue, nil
	default:
		return tjs.VoidValue, fmt.Errorf("unsupported Motion.ResourceManager operation %q", operation)
	}
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
