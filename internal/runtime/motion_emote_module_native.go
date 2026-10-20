package runtime

import (
	"fmt"

	"github.com/eslzzyl/krkrgo/internal/tjs"
)

// D3DEmoteModule is the module-scope class emoteplayer.dll registers next to
// EmotePlayer (main.cpp:565-587). Its fields are `inline static` in the
// reference (D3DEmoteModule.h:43-48), so every instance and every class-level
// access shares one store. That store lives on Services so each VM's runtime
// owns exactly one copy.
const (
	d3dEmoteTimelinePlayFlagParallel   = 1
	d3dEmoteTimelinePlayFlagSequential = 2
)

type d3dEmoteModuleState struct {
	maskMode                       int64
	maskRegionClipping             bool
	mipMapEnabled                  bool
	alphaOp                        int64
	protectTranslucentTextureColor bool
	pixelateDivision               int64
}

func newD3DEmoteModuleState() *d3dEmoteModuleState {
	return &d3dEmoteModuleState{maskMode: 1, pixelateDivision: 1}
}

// d3dEmoteModule returns the shared module state, creating it on first use
// under the services mutex like the nearby motion registries.
func (s *Services) d3dEmoteModule() *d3dEmoteModuleState {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.D3DEmoteModule == nil {
		s.D3DEmoteModule = newD3DEmoteModuleState()
	}
	return s.D3DEmoteModule
}

// d3dEmoteModuleHost resolves the runtime services behind a legacy call. The
// module class has no native operation prefix of its own, so its accessors are
// VM-owned legacy callables and reach the shared store through the host.
func d3dEmoteModuleHost(host tjs.Host) (*Services, error) {
	services, ok := host.(*Services)
	if !ok || services == nil {
		return nil, fmt.Errorf("D3DEmoteModule requires the runtime services host")
	}
	return services, nil
}

func d3dEmoteModuleGetter(name string) tjs.LegacyFunction {
	return func(_ tjs.Value, _ []tjs.Value, host tjs.Host, _ *uint64, _ bool) (tjs.Value, error) {
		services, err := d3dEmoteModuleHost(host)
		if err != nil {
			return tjs.VoidValue, err
		}
		state := services.d3dEmoteModule()
		services.mu.RLock()
		defer services.mu.RUnlock()
		switch name {
		case "maskMode":
			return tjs.IntegerValue(state.maskMode), nil
		case "maskRegionClipping":
			return tjs.IntegerValue(boolInt(state.maskRegionClipping)), nil
		case "mipMapEnabled":
			return tjs.IntegerValue(boolInt(state.mipMapEnabled)), nil
		case "alphaOp":
			return tjs.IntegerValue(state.alphaOp), nil
		case "protectTranslucentTextureColor":
			return tjs.IntegerValue(boolInt(state.protectTranslucentTextureColor)), nil
		case "pixelateDivision":
			return tjs.IntegerValue(state.pixelateDivision), nil
		default:
			return tjs.VoidValue, fmt.Errorf("unknown D3DEmoteModule property %q", name)
		}
	}
}

func d3dEmoteModuleSetter(name string) tjs.LegacyFunction {
	return func(_ tjs.Value, args []tjs.Value, host tjs.Host, _ *uint64, _ bool) (tjs.Value, error) {
		services, err := d3dEmoteModuleHost(host)
		if err != nil {
			return tjs.VoidValue, err
		}
		if len(args) < 1 {
			return tjs.VoidValue, fmt.Errorf("D3DEmoteModule.%s requires a value", name)
		}
		state := services.d3dEmoteModule()
		services.mu.Lock()
		defer services.mu.Unlock()
		switch name {
		case "maskMode":
			value, err := args[0].Int()
			if err != nil {
				return tjs.VoidValue, fmt.Errorf("D3DEmoteModule.maskMode requires an integer")
			}
			state.maskMode = value
		case "maskRegionClipping":
			state.maskRegionClipping = motionPlayerBool(args[0])
		case "mipMapEnabled":
			state.mipMapEnabled = motionPlayerBool(args[0])
		case "alphaOp":
			value, err := args[0].Int()
			if err != nil {
				return tjs.VoidValue, fmt.Errorf("D3DEmoteModule.alphaOp requires an integer")
			}
			state.alphaOp = value
		case "protectTranslucentTextureColor":
			state.protectTranslucentTextureColor = motionPlayerBool(args[0])
		case "pixelateDivision":
			value, err := args[0].Int()
			if err != nil {
				return tjs.VoidValue, fmt.Errorf("D3DEmoteModule.pixelateDivision requires an integer")
			}
			state.pixelateDivision = value
		default:
			return tjs.VoidValue, fmt.Errorf("unknown D3DEmoteModule property %q", name)
		}
		return tjs.VoidValue, nil
	}
}

// installD3DEmoteModuleNative registers the global D3DEmoteModule class. The
// reference class exposes constants, shared read/write properties, and
// setMaxTextureSize, which only logs (D3DEmoteModule.h:37-41).
func installD3DEmoteModuleNative(vm *tjs.VM) error {
	if vm == nil {
		return fmt.Errorf("D3DEmoteModule requires a VM")
	}
	if _, installed := vm.Globals["D3DEmoteModule"]; installed {
		return nil
	}
	class, err := vm.RegisterNativeClass("D3DEmoteModule")
	if err != nil {
		return err
	}
	for _, constant := range []struct {
		name  string
		value int64
	}{
		{"TimelinePlayFlagParallel", d3dEmoteTimelinePlayFlagParallel},
		{"TimelinePlayFlagSequential", d3dEmoteTimelinePlayFlagSequential},
	} {
		if err := vm.RegisterNativeStaticValue(class, constant.name, tjs.IntegerValue(constant.value)); err != nil {
			return err
		}
	}
	// connect attaches one VM-owned callable as a class member. Instances reach
	// it through the class lookup, and the shared store lives in Services.
	connect := func(name string, fn tjs.LegacyFunction) error {
		value, err := vm.RegisterLegacyFunction("D3DEmoteModule."+name, fn)
		if err != nil {
			return err
		}
		return vm.RegisterNativeStaticValue(class, name, value)
	}
	noop := func(tjs.Value, []tjs.Value, tjs.Host, *uint64, bool) (tjs.Value, error) {
		return tjs.VoidValue, nil
	}
	for _, name := range []string{"D3DEmoteModule", "@initialize", "@invalidate", "finalize"} {
		if err := connect(name, noop); err != nil {
			return err
		}
	}
	if err := connect("setMaxTextureSize", func(_ tjs.Value, args []tjs.Value, _ tjs.Host, _ *uint64, _ bool) (tjs.Value, error) {
		if len(args) < 2 {
			return tjs.VoidValue, fmt.Errorf("D3DEmoteModule.setMaxTextureSize requires width and height")
		}
		for _, arg := range args[:2] {
			switch arg.Kind() {
			case tjs.KindInteger, tjs.KindReal:
			default:
				return tjs.VoidValue, fmt.Errorf("D3DEmoteModule.setMaxTextureSize requires numeric width and height")
			}
		}
		// The reference logs and otherwise does nothing.
		return tjs.VoidValue, nil
	}); err != nil {
		return err
	}
	for _, property := range []string{
		"maskMode", "maskRegionClipping", "mipMapEnabled", "alphaOp",
		"protectTranslucentTextureColor", "pixelateDivision",
	} {
		getter, err := vm.RegisterLegacyFunction("D3DEmoteModule.get:"+property, d3dEmoteModuleGetter(property))
		if err != nil {
			return err
		}
		setter, err := vm.RegisterLegacyFunction("D3DEmoteModule.set:"+property, d3dEmoteModuleSetter(property))
		if err != nil {
			return err
		}
		descriptor, err := vm.RegisterLegacyProperty(property, getter, setter)
		if err != nil {
			return err
		}
		if err := vm.RegisterNativeStaticValue(class, property, descriptor); err != nil {
			return err
		}
	}
	return nil
}

// installD3DEmotePlayerNative registers the global D3DEmotePlayer class. The
// reference registration (main.cpp:589-687) lists the same members as
// Motion.EmotePlayer minus getMainTimelineLabelList, so every member maps to
// the operation string the EmotePlayer dispatcher already serves.
func installD3DEmotePlayerNative(vm *tjs.VM) error {
	if vm == nil {
		return fmt.Errorf("D3DEmotePlayer requires a VM")
	}
	if _, installed := vm.Globals["D3DEmotePlayer"]; installed {
		return nil
	}
	class, err := vm.RegisterNativeClass("D3DEmotePlayer")
	if err != nil {
		return err
	}
	for _, method := range []string{
		"@initialize", "@invalidate", "D3DEmotePlayer", "finalize",
		"create", "load", "clone", "serialize", "unserialize",
		"show", "hide", "assignState", "initPhysics",
		"setRot", "setRotate", "getRot",
		"setCoord", "setScale", "getScale", "setMirror",
		"setColor", "getColor",
		"countVariables", "getVariableLabelAt", "countVariableFrameAt",
		"getVariableFrameLabelAt", "getVariableFrameValueAt",
		"setVariable", "getVariable", "getVariableFrameList",
		"startWind", "stopWind",
		"countMainTimelines", "getMainTimelineLabelAt",
		"countDiffTimelines", "getDiffTimelineLabelAt", "getDiffTimelineLabelList",
		"countPlayingTimelines", "getPlayingTimelineLabelAt",
		"getPlayingTimelineFlagsAt", "isLoopTimeline", "getLoopTimeline",
		"getTimelineTotalFrameCount", "play", "playTimeline",
		"isTimelinePlaying", "getTimelinePlaying", "stopTimeline", "setTimeline",
		"setTimelineBlendRatio", "getTimelineBlendRatio",
		"fadeInTimeline", "fadeOutTimeline", "getPlayingTimelineInfoList",
		"skip", "skipToSync", "addPlayCallback", "pass",
		"progress", "draw", "setDrawAffineTranslateMatrix",
		"setOuterForce", "getOuterForce", "contains",
	} {
		if err := vm.RegisterNativeMethod(class, method, "Motion.EmotePlayer."+method); err != nil {
			return err
		}
	}
	for _, property := range []string{
		"completionType", "chara", "motion", "motionKey", "maskMode",
		"outline", "priorDraw", "frameLastTime", "frameLoopTime", "loopTime",
		"processedMeshVerticesNum", "visible", "smoothing",
		"meshDivisionRatio", "queing", "hairScale", "partsScale", "bustScale",
		"bodyScale", "useD3D", "progress", "modified", "drawvisible",
		"drawOpacity", "opengl",
	} {
		if err := vm.RegisterNativeProperty(class, property,
			"Motion.EmotePlayer.get:"+property, "Motion.EmotePlayer.set:"+property); err != nil {
			return err
		}
	}
	for _, property := range []string{"module", "animating", "playCallback"} {
		if err := vm.RegisterNativeProperty(class, property,
			"Motion.EmotePlayer.get:"+property, ""); err != nil {
			return err
		}
	}
	// The reference registers progress() after the progress property, so the
	// callable native member is the observable final member (mirrors
	// Motion.EmotePlayer).
	if err := vm.RegisterNativeMethod(class, "progress", "Motion.EmotePlayer.progress"); err != nil {
		return err
	}
	return nil
}
