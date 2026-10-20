package runtime

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/eslzzyl/krkrgo/internal/assets"
	"github.com/eslzzyl/krkrgo/internal/tjs"
)

// Motion.EmotePlayer is the emoteplayer.dll class (reference
// reference/krkr2-main/cpp/plugins/motionplayer/EmotePlayer.h/.cpp and its NCB
// registration in main.cpp:299). The reference class is a thin shell around
// Player: it owns a ResourceManager, a motion key naming a loaded PSB module, a
// clip label, variables, timelines, and draw/transform/color state, and it
// delegates playback, progress, and draw to Player.
//
// This runtime keeps the same split. emotePlayerState holds the shell state;
// motionPlayerState (motionplayer_runtime.go) holds the shared playback clock,
// bound PSB resource, and render path, so Motion.Player and Motion.EmotePlayer
// share one implementation. The draw path applies Emote transforms, per-frame
// PSB corner colors, masks, parameterized frames, and generated mesh grids.
// Timelines and variable controllers feed the eval values used by those frames;
// fields that the reference stores without a render consumer remain state-only.
type emoteVariableFrame struct {
	label string
	value float64
}

// emoteVariableState is the reference _variableValues entry: the latest value
// written by setVariable or by the eval-result sink. Metadata frames and ranges
// are introspection data only.
type emoteVariableState struct {
	frames   []emoteVariableFrame
	value    float64
	rangeMin float64
	rangeMax float64
	rangeSet bool
	set      bool
}

// emoteKeyframe and emoteAnimator implement stepQueuedAnimatorLike_0x67D01C
// (PlayerFrameProgress.cpp:13-66).
type emoteKeyframe struct {
	value    float64
	duration float64
	weight   float64
}

type emoteAnimator struct {
	queue        []emoteKeyframe
	active       bool
	startValue   float64
	currentValue float64
	targetValue  float64
	progress     float64
	duration     float64
	weight       float64
}

// emoteParameterEntry mirrors MotionParameterEntry (PlayerVariable.cpp:179-241):
// PSB "parameterize" indexes this table, not a variable label.
type emoteParameterEntry struct {
	id             string
	discretization bool
	rangeBegin     float64
	rangeEnd       float64
	rangeScale     float64
	value          float64
	mode           int64
	bound          bool
}

// emoteControllerBinding mirrors VariableControllerBinding: type 0..8, source
// list name, and the metadata role (var_lr, label, talkLabel, ...).
type emoteControllerBinding struct {
	typ    int
	index  int
	source string
	role   string
}

type emoteSelectorOption struct {
	label    string
	offValue float64
	onValue  float64
}

type emoteSelectorControl struct {
	options []emoteSelectorOption
}

type emoteClampControl struct {
	typ   int
	varLR string
	varUD string
	min   float64
	max   float64
}

type emoteTimelineFrame struct {
	time     float64
	value    float64
	weight   float64
	typeZero bool
}

type emoteTimelineTrack struct {
	label   string
	instant bool
	frames  []emoteTimelineFrame
}

type emoteTimelineState struct {
	label         string
	playing       bool
	flags         int64
	blendRatio    float64
	time          float64
	loop          bool
	lastTime      float64
	loopBegin     float64
	loopEnd       float64
	totalFrames   float64
	loopTime      float64
	control       bool
	tracks        []emoteTimelineTrack
	controlCursor []int
	controlValues []float64
	controlAnims  []emoteAnimator
	blendAnimator emoteAnimator
	blendAutoStop bool
	controlInit   bool
	controlLast   float64
}

type emoteOuterForce struct {
	active     bool
	x, y       float64
	transition float64
	ease       float64
}

type emotePlayerState struct {
	motionKey string
	// module is the TJS view of the loaded PSB module exposed through the
	// read-only module property; it is refreshed whenever a resource binds.
	module       tjs.Value
	moduleMotion string
	moduleChara  string
	clipLabel    string
	isSelfClear  bool

	maskMode                 int64
	priorDraw                float64
	frameLastTime            float64
	frameLoopTime            float64
	loopTime                 float64
	processedMeshVerticesNum int64

	visible      bool
	smoothing    bool
	queuing      bool
	useD3D       bool
	modified     bool
	drawVisible  bool
	opengl       bool
	playCallback bool

	meshDivisionRatio float64
	hairScale         float64
	partsScale        float64
	bustScale         float64
	bodyScale         float64
	drawOpacity       float64
	progress          float64

	// Transform state pushed into the shared draw matrix.
	rot    float64
	coordX float64
	coordY float64
	scale  float64
	mirror bool

	// color is the packed Emote shell value. The reference stores it but does
	// not consume it in rendering; visible tint comes from PSB frame content.
	color uint32

	// variables are _variableValues; variableOrder preserves the reference
	// variableLabels order (variableList then control/selector/instant labels).
	variables     map[string]*emoteVariableState
	variableOrder []string

	// evalResults are the per-progress _evalResultValues, rebuilt from
	// controller animators and timeline contributions.
	evalResults map[string]float64
	evalOrder   []string

	// parameters is the normalized parameter table parameterize nodes index.
	parameters    []*emoteParameterEntry
	parameterById map[string]int

	controllerBindings map[string]emoteControllerBinding
	animators          map[int]map[string]*emoteAnimator
	selectorControls   map[string]*emoteSelectorControl
	clampControls      []emoteClampControl
	mirrorLabels       []string
	instantVariables   map[string]bool

	// timelines are the PSB timeline states; timelineOrder preserves metadata
	// order for the label getters, playingOrder the reference play order.
	timelines      map[string]*emoteTimelineState
	timelineOrder  []string
	mainTimelines  []string
	diffTimelines  []string
	playingOrder   []string
	timelineTotals map[string]float64
	timelineLoops  map[string]bool

	// wind and outer force are the physics inputs the reference forwards to
	// Player; PlayerCore.cpp stores them without consuming them in geometry or
	// rendering (startWind/stopWind/setOuterForce are state sinks).
	windActive    bool
	windMin       float64
	windMax       float64
	windAmp       float64
	windFreqX     float64
	windFreqY     float64
	windScaledAmp float64
	windPhase     float64
	windPrevPhase float64
	windCounter   int64
	outerForce    map[string]emoteOuterForce
	physicsRule   tjs.Value
	physicsInit   bool
}

func newEmotePlayerState() *emotePlayerState {
	return &emotePlayerState{
		module:             tjs.VoidValue,
		physicsRule:        tjs.VoidValue,
		visible:            true,
		smoothing:          true,
		drawVisible:        true,
		meshDivisionRatio:  1,
		hairScale:          1,
		partsScale:         1,
		bustScale:          1,
		bodyScale:          1,
		drawOpacity:        1,
		scale:              1,
		color:              0x00FFFFFF,
		variables:          make(map[string]*emoteVariableState),
		evalResults:        make(map[string]float64),
		parameterById:      make(map[string]int),
		controllerBindings: make(map[string]emoteControllerBinding),
		animators:          make(map[int]map[string]*emoteAnimator),
		selectorControls:   make(map[string]*emoteSelectorControl),
		instantVariables:   make(map[string]bool),
		timelines:          make(map[string]*emoteTimelineState),
		timelineTotals:     make(map[string]float64),
		timelineLoops:      make(map[string]bool),
		outerForce:         make(map[string]emoteOuterForce),
	}
}

// installEmotePlayerNative registers the full Motion.EmotePlayer surface. The
// reference emoteplayer.dll loads motionplayer.dll as a dependency and then
// registers EmotePlayer into the Motion namespace (main.cpp:562), so the
// portable namespace is installed first.
func installEmotePlayerNative(vm *tjs.VM) error {
	if vm == nil {
		return fmt.Errorf("emoteplayer.dll requires a VM")
	}
	if err := installMotionPlayerNative(vm); err != nil {
		return err
	}
	motion, ok := vm.Globals["Motion"]
	if !ok || motion.Kind() != tjs.KindObject || motion.IsNull() {
		return fmt.Errorf("Motion native namespace is not registered")
	}
	class, ok := vm.Objects.Lookup(motion.ObjectRef().Object)
	if !ok {
		return fmt.Errorf("Motion native namespace is not registered")
	}
	if _, installed := class.Get("EmotePlayer"); installed {
		return nil
	}
	emote, err := vm.RegisterNativeClass("Motion.EmotePlayer")
	if err != nil {
		return err
	}
	if err := class.Set("EmotePlayer", emote); err != nil {
		return err
	}
	for _, method := range []string{
		"@initialize", "@invalidate", "EmotePlayer", "finalize",
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
		"getMainTimelineLabelList", "countDiffTimelines",
		"getDiffTimelineLabelAt", "getDiffTimelineLabelList",
		"countPlayingTimelines", "getPlayingTimelineLabelAt",
		"getPlayingTimelineFlagsAt", "isLoopTimeline", "getLoopTimeline",
		"getTimelineTotalFrameCount", "playTimeline", "isTimelinePlaying",
		"getTimelinePlaying", "stopTimeline", "setTimeline",
		"setTimelineBlendRatio", "getTimelineBlendRatio",
		"fadeInTimeline", "fadeOutTimeline", "getPlayingTimelineInfoList",
		"skip", "skipToSync", "addPlayCallback", "pass",
		"play", "progress", "draw", "setDrawAffineTranslateMatrix",
		"setOuterForce", "getOuterForce", "contains",
	} {
		if err := vm.RegisterNativeMethod(emote, method, "Motion.EmotePlayer."+method); err != nil {
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
		if err := vm.RegisterNativeProperty(emote, property,
			"Motion.EmotePlayer.get:"+property, "Motion.EmotePlayer.set:"+property); err != nil {
			return err
		}
	}
	for _, property := range []string{"module", "animating", "playCallback"} {
		if err := vm.RegisterNativeProperty(emote, property,
			"Motion.EmotePlayer.get:"+property, ""); err != nil {
			return err
		}
	}
	// The reference registers progress() after the progress property, so the
	// callable native member is the observable final member.
	if err := vm.RegisterNativeMethod(emote, "progress", "Motion.EmotePlayer.progress"); err != nil {
		return err
	}
	return nil
}

// emotePlayerStateFor resolves the shared player state and its EmotePlayer
// shell state, both keyed by the TJS object.
func (s *Services) emotePlayerStateFor(id tjs.ObjectID) (*motionPlayerState, *emotePlayerState) {
	state := s.motionPlayer(id)
	s.mu.Lock()
	if state.emote == nil {
		state.emote = newEmotePlayerState()
	}
	emote := state.emote
	s.mu.Unlock()
	return state, emote
}

func (s *Services) motionEmotePlayerNative(vm *tjs.VM, context tjs.Value, operation string, args []tjs.Value) (tjs.Value, error) {
	if context.Kind() != tjs.KindObject || context.IsNull() {
		return tjs.VoidValue, fmt.Errorf("Motion.EmotePlayer.%s requires an instance", operation)
	}
	id := context.ObjectRef().Object
	switch operation {
	case "@initialize", "EmotePlayer":
		state, _ := s.emotePlayerStateFor(id)
		// Reference EmotePlayer(ResourceManager) only stores the manager; it
		// does not bind whichever module the manager happened to load.
		if len(args) > 0 && args[0].Kind() == tjs.KindObject && !args[0].IsNull() {
			state.resourceManager = s.motionResourceManagerID(args[0].ObjectRef().Object)
		}
		return tjs.VoidValue, nil
	case "@invalidate", "finalize":
		s.mu.Lock()
		delete(s.MotionPlayers, id)
		s.mu.Unlock()
		return tjs.VoidValue, nil
	}
	state, emote := s.emotePlayerStateFor(id)
	return s.motionEmotePlayerNativeWithState(vm, context, operation, args, state, emote, true)
}

func (s *Services) motionEmotePlayerNativeWithState(vm *tjs.VM, context tjs.Value, operation string, args []tjs.Value, state *motionPlayerState, emote *emotePlayerState, emoteAPI bool) (tjs.Value, error) {
	if emoteAPI {
		switch operation {
		case "getVariable", "getVariableFrameList", "countVariables", "getVariableLabelAt", "countVariableFrameAt",
			"getVariableFrameLabelAt", "getVariableFrameValueAt", "getTimelinePlaying", "isTimelinePlaying",
			"countMainTimelines", "getMainTimelineLabelAt", "getMainTimelineLabelList",
			"countDiffTimelines", "getDiffTimelineLabelAt", "getDiffTimelineLabelList", "getLoopTimeline",
			"isLoopTimeline", "countPlayingTimelines", "getPlayingTimelineLabelAt", "getPlayingTimelineFlagsAt",
			"getTimelineTotalFrameCount", "playTimeline", "setTimelineBlendRatio", "getPlayingTimelineInfoList",
			"skip", "skipToSync":
			if err := s.motionPlayerEnsureLoaded(vm, state); err != nil {
				return tjs.VoidValue, err
			}
		}
	}
	if strings.HasPrefix(operation, "get:") {
		return s.emotePlayerGet(state, emote, strings.TrimPrefix(operation, "get:"))
	}
	if strings.HasPrefix(operation, "set:") {
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("Motion.EmotePlayer.%s requires a value", strings.TrimPrefix(operation, "set:"))
		}
		return tjs.VoidValue, s.emotePlayerSet(vm, state, emote, strings.TrimPrefix(operation, "set:"), args[0])
	}
	switch operation {
	case "create":
		// create() is the reference destroy/reset path: it clears the module
		// and the Player snapshot but keeps the motion key and manager.
		emote.module = tjs.VoidValue
		emote.clipLabel = ""
		state.resource = nil
		state.attached = nil
		state.motion = ""
		state.playing = false
		state.held = false
		state.frameTick = 0
		state.clampedEvalTime = 0
		s.emoteResetRuntime(emote)
		emote.modified = true
		return tjs.VoidValue, nil
	case "load":
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("Motion.EmotePlayer.load requires a module")
		}
		if args[0].Kind() == tjs.KindString {
			return tjs.VoidValue, s.emotePlayerBindMotionKey(vm, state, emote, args[0].Text())
		}
		emote.module = args[0]
		state.resource = nil
		if args[0].Kind() == tjs.KindObject && !args[0].IsNull() {
			s.mu.RLock()
			state.resource = s.MotionModules[args[0].ObjectRef().Object]
			s.mu.RUnlock()
		}
		state.attached = nil
		s.emoteResetRuntime(emote)
		if state.resource != nil {
			s.emoteApplyResourceMetadata(emote, state.resource)
			emote.moduleMotion = motionResourceMetadataString(state.resource, "motion")
			emote.moduleChara = motionResourceMetadataString(state.resource, "chara")
			if emote.moduleChara == "" {
				emote.moduleChara = motionResourceDefaultChara(state.resource)
			}
		}
		emote.modified = true
		return tjs.VoidValue, nil
	case "clone":
		return s.emotePlayerClone(vm, state, emote)
	case "serialize":
		return s.emotePlayerSerialize(vm, state, emote)
	case "unserialize":
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("Motion.EmotePlayer.unserialize requires data")
		}
		if err := s.emotePlayerUnserialize(vm, state, emote, args[0]); err != nil {
			return tjs.VoidValue, err
		}
		return tjs.VoidValue, nil
	case "show":
		emote.visible = true
		state.viewVisible = true
		return tjs.VoidValue, nil
	case "hide":
		emote.visible = false
		state.viewVisible = false
		return tjs.VoidValue, nil
	case "assignState":
		state.viewVisible = emote.visible && emote.drawVisible
		state.d3dDrawMode = emote.useD3D
		state.hairScale = emote.hairScale
		state.partsScale = emote.partsScale
		state.bustScale = emote.bustScale
		emote.modified = false
		return tjs.VoidValue, nil
	case "initPhysics":
		if len(args) > 0 {
			emote.physicsRule = args[0]
		}
		emote.physicsInit = true
		emote.modified = true
		return tjs.VoidValue, nil
	case "setRot", "setRotate":
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("Motion.EmotePlayer.%s requires an angle", operation)
		}
		rot, err := args[0].Real()
		if err != nil {
			return tjs.VoidValue, err
		}
		emote.rot = rot
		state.rotateAngle = rot
		emote.modified = true
		return tjs.VoidValue, nil
	case "getRot":
		// Reference binary returns a hardcoded 0.0 (EmotePlayer.cpp:326); the
		// tracked angle still drives the draw matrix.
		return tjs.RealValue(0), nil
	case "setCoord":
		if len(args) < 2 {
			return tjs.VoidValue, fmt.Errorf("Motion.EmotePlayer.setCoord requires x and y")
		}
		x, err := args[0].Real()
		if err != nil {
			return tjs.VoidValue, err
		}
		y, err := args[1].Real()
		if err != nil {
			return tjs.VoidValue, err
		}
		emote.coordX, emote.coordY = x, y
		state.x, state.y = x, y
		emote.modified = true
		return tjs.VoidValue, nil
	case "setScale":
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("Motion.EmotePlayer.setScale requires a scale")
		}
		scale, err := args[0].Real()
		if err != nil {
			return tjs.VoidValue, err
		}
		emote.scale = scale
		state.emoteScale = scale
		emote.modified = true
		return tjs.VoidValue, nil
	case "getScale":
		// Reference binary returns a hardcoded 1.0 (EmotePlayer.cpp:388); the
		// tracked scale still drives the draw matrix.
		return tjs.RealValue(1), nil
	case "setMirror":
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("Motion.EmotePlayer.setMirror requires a flag")
		}
		mirror, err := args[0].Truth()
		if err != nil {
			return tjs.VoidValue, err
		}
		changed := state.rootFlipX != mirror || state.mirrorEvalEnabled != mirror
		state.rootFlipX = mirror
		state.mirrorEvalEnabled = mirror
		emote.mirror = mirror
		if changed {
			state.gpuMotionValid = false
			emote.animators = make(map[int]map[string]*emoteAnimator)
			emote.evalResults = make(map[string]float64)
			emote.evalOrder = nil
			if state.selectorEnabled {
				s.emoteSyncSelectorControls(state, emote)
			}
		}
		emote.modified = true
		return tjs.VoidValue, nil
	case "setColor":
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("Motion.EmotePlayer.setColor requires a color")
		}
		color, err := args[0].Int()
		if err != nil {
			return tjs.VoidValue, err
		}
		emote.color = uint32(color)
		emote.modified = true
		return tjs.VoidValue, nil
	case "getColor":
		// Reference returns zero; setColor stores animator state without a
		// render consumer (EmotePlayer.cpp:427, PlayerCore.cpp:1660).
		return tjs.IntegerValue(0), nil
	case "countVariables":
		return tjs.IntegerValue(int64(len(emote.variableOrder))), nil
	case "getVariableLabelAt":
		idx, err := emoteIndexArg(args, 0, "getVariableLabelAt")
		if err != nil {
			return tjs.VoidValue, err
		}
		if idx < 0 || idx >= int64(len(emote.variableOrder)) {
			return tjs.StringValue(""), nil
		}
		return tjs.StringValue(emote.variableOrder[idx]), nil
	case "countVariableFrameAt":
		idx, err := emoteIndexArg(args, 0, "countVariableFrameAt")
		if err != nil {
			return tjs.VoidValue, err
		}
		if idx < 0 || idx >= int64(len(emote.variableOrder)) {
			return tjs.IntegerValue(0), nil
		}
		return tjs.IntegerValue(int64(len(emote.variables[emote.variableOrder[idx]].frames))), nil
	case "getVariableFrameLabelAt":
		idx, err := emoteIndexArg(args, 0, "getVariableFrameLabelAt")
		if err != nil {
			return tjs.VoidValue, err
		}
		frame, err := emoteIndexArg(args, 1, "getVariableFrameLabelAt")
		if err != nil {
			return tjs.VoidValue, err
		}
		return tjs.StringValue(emoteVariableFrameLabel(emote, idx, frame)), nil
	case "getVariableFrameValueAt":
		idx, err := emoteIndexArg(args, 0, "getVariableFrameValueAt")
		if err != nil {
			return tjs.VoidValue, err
		}
		frame, err := emoteIndexArg(args, 1, "getVariableFrameValueAt")
		if err != nil {
			return tjs.VoidValue, err
		}
		return tjs.RealValue(emoteVariableFrameValue(emote, idx, frame)), nil
	case "setVariable":
		if len(args) < 2 {
			return tjs.VoidValue, fmt.Errorf("Motion.EmotePlayer.setVariable requires a label and value")
		}
		label := args[0].Text()
		value, err := args[1].Real()
		if err != nil {
			return tjs.VoidValue, err
		}
		transition := 0.0
		if len(args) > 2 && args[2].Kind() != tjs.KindVoid {
			if transition, err = args[2].Real(); err != nil {
				return tjs.VoidValue, err
			}
		}
		ease := 0.0
		if len(args) > 3 && args[3].Kind() != tjs.KindVoid {
			if ease, err = args[3].Real(); err != nil {
				return tjs.VoidValue, err
			}
		}
		s.emoteSetVariable(emote, label, value, transition, ease)
		emote.modified = true
		return tjs.VoidValue, nil
	case "getVariable":
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("Motion.EmotePlayer.getVariable requires a label")
		}
		return tjs.RealValue(emoteVariableValue(emote, args[0].Text())), nil
	case "getVariableRange":
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("Motion.EmotePlayer.getVariableRange requires a label")
		}
		variable := emote.variables[args[0].Text()]
		if variable == nil || !variable.rangeSet {
			return tjs.VoidValue, nil
		}
		return vm.NewArray([]tjs.Value{tjs.RealValue(variable.rangeMin), tjs.RealValue(variable.rangeMax)})
	case "getVariableFrameList":
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("Motion.EmotePlayer.getVariableFrameList requires a label")
		}
		return s.emoteVariableFrameList(vm, emote, args[0].Text())
	case "startWind":
		if len(args) < 5 {
			return tjs.VoidValue, fmt.Errorf("Motion.EmotePlayer.startWind requires minAngle, maxAngle, amplitude, freqX, freqY")
		}
		values, err := emoteRealArgs(args, 5)
		if err != nil {
			return tjs.VoidValue, err
		}
		s.emoteStartWind(emote, values[0], values[1], values[2], values[3], values[4])
		emote.modified = true
		return tjs.VoidValue, nil
	case "stopWind":
		s.emoteStopWind(emote)
		emote.modified = true
		return tjs.VoidValue, nil
	case "countMainTimelines":
		return tjs.IntegerValue(int64(len(emote.mainTimelines))), nil
	case "getMainTimelineLabelAt":
		idx, err := emoteIndexArg(args, 0, "getMainTimelineLabelAt")
		if err != nil {
			return tjs.VoidValue, err
		}
		return tjs.StringValue(emoteTimelineLabelAt(emote.mainTimelines, idx)), nil
	case "getMainTimelineLabelList":
		return vm.NewArray(emoteStringValues(emote.mainTimelines))
	case "countDiffTimelines":
		return tjs.IntegerValue(int64(len(emote.diffTimelines))), nil
	case "getDiffTimelineLabelAt":
		idx, err := emoteIndexArg(args, 0, "getDiffTimelineLabelAt")
		if err != nil {
			return tjs.VoidValue, err
		}
		return tjs.StringValue(emoteTimelineLabelAt(emote.diffTimelines, idx)), nil
	case "getDiffTimelineLabelList":
		return vm.NewArray(emoteStringValues(emote.diffTimelines))
	case "countPlayingTimelines":
		return tjs.IntegerValue(int64(len(s.emotePlayingTimelines(emote)))), nil
	case "getPlayingTimelineLabelAt":
		idx, err := emoteIndexArg(args, 0, "getPlayingTimelineLabelAt")
		if err != nil {
			return tjs.VoidValue, err
		}
		playing := s.emotePlayingTimelines(emote)
		if idx < 0 || idx >= int64(len(playing)) {
			return tjs.StringValue(""), nil
		}
		return tjs.StringValue(playing[idx].label), nil
	case "getPlayingTimelineFlagsAt":
		idx, err := emoteIndexArg(args, 0, "getPlayingTimelineFlagsAt")
		if err != nil {
			return tjs.VoidValue, err
		}
		playing := s.emotePlayingTimelines(emote)
		if idx < 0 || idx >= int64(len(playing)) {
			return tjs.IntegerValue(0), nil
		}
		return tjs.IntegerValue(playing[idx].flags), nil
	case "isLoopTimeline", "getLoopTimeline":
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("Motion.EmotePlayer.%s requires a label", operation)
		}
		return emoteBoolValue(s.emoteTimelineLoop(emote, args[0].Text())), nil
	case "getTimelineTotalFrameCount":
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("Motion.EmotePlayer.getTimelineTotalFrameCount requires a label")
		}
		return tjs.IntegerValue(int64(s.emoteTimelineTotalFrames(emote, args[0].Text()))), nil
	case "playTimeline":
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("Motion.EmotePlayer.playTimeline requires a label")
		}
		flags := int64(0)
		if len(args) > 1 && args[1].Kind() != tjs.KindVoid {
			value, err := args[1].Int()
			if err != nil {
				return tjs.VoidValue, err
			}
			flags = value
		}
		s.emotePlayTimeline(emote, args[0].Text(), flags)
		state.playing = len(emote.playingOrder) > 0
		if emoteAPI {
			emote.modified = true
		}
		return tjs.VoidValue, nil
	case "isTimelinePlaying", "getTimelinePlaying":
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("Motion.EmotePlayer.%s requires a label", operation)
		}
		timeline := emote.timelines[args[0].Text()]
		return emoteBoolValue(timeline != nil && timeline.playing), nil
	case "stopTimeline":
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("Motion.EmotePlayer.stopTimeline requires a label")
		}
		s.emoteStopTimeline(emote, args[0].Text())
		state.playing = len(emote.playingOrder) > 0
		return tjs.VoidValue, nil
	case "setTimeline":
		// EmotePlayer::setTimeline ignores the loop argument and calls
		// Player::playTimeline(label, 0) (EmotePlayer.cpp:633-636).
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("Motion.EmotePlayer.setTimeline requires a label")
		}
		s.emotePlayTimeline(emote, args[0].Text(), 0)
		state.playing = len(emote.playingOrder) > 0
		return tjs.VoidValue, nil
	case "setTimelineBlendRatio":
		if len(args) < 2 {
			return tjs.VoidValue, fmt.Errorf("Motion.EmotePlayer.setTimelineBlendRatio requires a label and ratio")
		}
		ratio, err := args[1].Real()
		if err != nil {
			return tjs.VoidValue, err
		}
		timeline := s.emoteEnsureTimeline(emote, args[0].Text())
		timeline.blendRatio = ratio
		timeline.blendAnimator = emoteAnimator{}
		timeline.blendAutoStop = false
		return tjs.VoidValue, nil
	case "getTimelineBlendRatio":
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("Motion.EmotePlayer.getTimelineBlendRatio requires a label")
		}
		if timeline := emote.timelines[args[0].Text()]; timeline != nil {
			return tjs.RealValue(timeline.blendRatio), nil
		}
		return tjs.RealValue(1), nil
	case "fadeInTimeline":
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("Motion.EmotePlayer.fadeInTimeline requires a label")
		}
		duration, err := emoteOptionalReal(args, 1)
		if err != nil {
			return tjs.VoidValue, err
		}
		label := args[0].Text()
		if emoteAPI && !emoteContainsString(emote.playingOrder, label) {
			if err := s.motionPlayerEnsureLoaded(vm, state); err != nil {
				return tjs.VoidValue, err
			}
		}
		if emoteAPI && duration <= 0 {
			flags, err := emoteOptionalInt(args, 2)
			if err != nil {
				return tjs.VoidValue, err
			}
			s.emotePlayTimeline(emote, label, flags)
			state.playing = len(emote.playingOrder) > 0
			emote.modified = true
			return tjs.VoidValue, nil
		}
		if !emoteContainsString(emote.playingOrder, label) {
			s.emotePlayTimeline(emote, label, 3)
			s.emoteSetTimelineBlendLike(emote, label, false, 0, 0, 0)
		}
		s.emoteSetTimelineBlendLike(emote, label, false, 1, duration, 0)
		state.playing = len(emote.playingOrder) > 0
		if emoteAPI {
			emote.modified = true
		}
		return tjs.VoidValue, nil
	case "fadeOutTimeline":
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("Motion.EmotePlayer.fadeOutTimeline requires a label")
		}
		duration, err := emoteOptionalReal(args, 1)
		if err != nil {
			return tjs.VoidValue, err
		}
		label := args[0].Text()
		if emoteAPI && duration <= 0 {
			s.emoteStopTimeline(emote, label)
			state.playing = len(emote.playingOrder) > 0
			return tjs.VoidValue, nil
		}
		s.emoteSetTimelineBlendLike(emote, label, true, 0, duration, 0)
		if emoteAPI {
			emote.modified = true
		}
		return tjs.VoidValue, nil
	case "getPlayingTimelineInfoList":
		return s.emotePlayingTimelineInfoList(vm, emote)
	case "skip", "skipToSync":
		s.emoteSkipToSync(state, emote)
		return tjs.VoidValue, nil
	case "addPlayCallback":
		emote.playCallback = true
		return tjs.VoidValue, nil
	case "pass":
		state.syncWaiting = false
		state.syncActive = false
		emote.modified = true
		return tjs.VoidValue, nil
	case "play":
		return s.emotePlayerPlay(vm, state, emote, args)
	case "progress":
		return s.emotePlayerProgress(state, emote, args)
	case "draw":
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("Motion.EmotePlayer.draw requires a target layer")
		}
		if !emote.visible || !emote.drawVisible || state.resource == nil {
			return tjs.VoidValue, nil
		}
		state.playerID = context.ObjectRef().Object
		if err := s.drawMotionTarget(vm, state, args[0]); err != nil {
			return tjs.VoidValue, err
		}
		emote.modified = true
		return tjs.VoidValue, nil
	case "setDrawAffineTranslateMatrix":
		return s.motionPlayerNative(vm, context, operation, args)
	case "setOuterForce":
		if len(args) != 2 && len(args) < 3 {
			return tjs.VoidValue, fmt.Errorf("Motion.EmotePlayer.setOuterForce requires x, y or label, x and y")
		}
		label, xIndex, yIndex := "bust", 0, 1
		if len(args) >= 3 {
			label, xIndex, yIndex = strings.ToLower(args[0].Text()), 1, 2
		}
		x, err := args[xIndex].Real()
		if err != nil {
			return tjs.VoidValue, err
		}
		y, err := args[yIndex].Real()
		if err != nil {
			return tjs.VoidValue, err
		}
		transition, err := emoteOptionalReal(args, yIndex+1)
		if err != nil {
			return tjs.VoidValue, err
		}
		ease, err := emoteOptionalReal(args, yIndex+2)
		if err != nil {
			return tjs.VoidValue, err
		}
		switch label {
		case "bust", "h", "parts":
			emote.outerForce[label] = emoteOuterForce{active: true, x: x, y: y, transition: transition, ease: ease}
		default:
			return tjs.VoidValue, nil
		}
		emote.modified = true
		return tjs.VoidValue, nil
	case "getOuterForce":
		force := emote.outerForce["bust"]
		object, err := vm.NewDictionary()
		if err != nil {
			return tjs.VoidValue, err
		}
		if err := tjs.SetMember(vm.Objects, object, "x", tjs.RealValue(force.x)); err != nil {
			return tjs.VoidValue, err
		}
		if err := tjs.SetMember(vm.Objects, object, "y", tjs.RealValue(force.y)); err != nil {
			return tjs.VoidValue, err
		}
		return object, nil
	case "contains":
		return s.emotePlayerContains(state, emote, args)
	default:
		return tjs.VoidValue, fmt.Errorf("%w: Motion.EmotePlayer.%s", ErrUnsupportedNative, operation)
	}
}

func (s *Services) emotePlayerGet(state *motionPlayerState, emote *emotePlayerState, property string) (tjs.Value, error) {
	switch property {
	case "module":
		return emote.module, nil
	case "completionType":
		return tjs.IntegerValue(state.completionType), nil
	case "chara":
		return tjs.StringValue(state.chara), nil
	case "motion":
		if emote.clipLabel != "" {
			return tjs.StringValue(emote.clipLabel), nil
		}
		return tjs.StringValue(state.motion), nil
	case "motionKey":
		return tjs.StringValue(emote.motionKey), nil
	case "maskMode":
		return tjs.IntegerValue(state.maskMode), nil
	case "outline":
		return tjs.StringValue(state.outline), nil
	case "priorDraw":
		return tjs.RealValue(state.priorDraw), nil
	case "frameLastTime":
		return tjs.RealValue(state.frameLastTime), nil
	case "frameLoopTime":
		return tjs.RealValue(state.frameLoopTime), nil
	case "loopTime":
		return tjs.RealValue(state.loopTime), nil
	case "processedMeshVerticesNum":
		return tjs.IntegerValue(state.processedMeshVerticesNum), nil
	case "visible":
		return emoteBoolValue(emote.visible), nil
	case "smoothing":
		return emoteBoolValue(emote.smoothing), nil
	case "queing":
		return emoteBoolValue(emote.queuing), nil
	case "useD3D":
		return emoteBoolValue(emote.useD3D), nil
	case "modified":
		return emoteBoolValue(emote.modified), nil
	case "drawvisible":
		return emoteBoolValue(emote.drawVisible), nil
	case "opengl":
		return emoteBoolValue(emote.opengl), nil
	case "playCallback":
		return emoteBoolValue(emote.playCallback), nil
	case "animating":
		return emoteBoolValue(s.motionPlayerAllPlaying(state)), nil
	case "meshDivisionRatio":
		return tjs.RealValue(emote.meshDivisionRatio), nil
	case "hairScale":
		return tjs.RealValue(emote.hairScale), nil
	case "partsScale":
		return tjs.RealValue(emote.partsScale), nil
	case "bustScale":
		return tjs.RealValue(emote.bustScale), nil
	case "bodyScale":
		return tjs.RealValue(emote.bodyScale), nil
	case "drawOpacity":
		return tjs.RealValue(emote.drawOpacity), nil
	case "progress":
		return tjs.RealValue(emote.progress), nil
	default:
		return tjs.VoidValue, fmt.Errorf("%w: Motion.EmotePlayer.%s", ErrUnsupportedNative, property)
	}
}

func (s *Services) emotePlayerSet(vm *tjs.VM, state *motionPlayerState, emote *emotePlayerState, property string, value tjs.Value) error {
	switch property {
	case "completionType":
		n, err := value.Int()
		if err != nil {
			return err
		}
		state.completionType = n
	case "chara":
		state.chara = value.Text()
	case "motion":
		emote.clipLabel = value.Text()
		emote.modified = true
	case "motionKey":
		return s.emotePlayerBindMotionKey(vm, state, emote, value.Text())
	case "maskMode":
		n, err := value.Int()
		if err != nil {
			return err
		}
		state.maskMode = n
	case "outline":
		state.outline = value.Text()
	case "priorDraw":
		n, err := value.Real()
		if err != nil {
			return err
		}
		state.priorDraw = n
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
	case "loopTime":
		n, err := value.Real()
		if err != nil {
			return err
		}
		state.loopTime = n
	case "processedMeshVerticesNum":
		n, err := value.Int()
		if err != nil {
			return err
		}
		state.processedMeshVerticesNum = n
	case "visible":
		v, err := value.Truth()
		if err != nil {
			return err
		}
		emote.visible = v
		state.viewVisible = v
	case "smoothing":
		v, err := value.Truth()
		if err != nil {
			return err
		}
		emote.smoothing = v
	case "queing":
		v, err := value.Truth()
		if err != nil {
			return err
		}
		emote.queuing = v
	case "useD3D":
		v, err := value.Truth()
		if err != nil {
			return err
		}
		emote.useD3D = v
		state.d3dDrawMode = v
	case "modified":
		v, err := value.Truth()
		if err != nil {
			return err
		}
		emote.modified = v
	case "drawvisible":
		v, err := value.Truth()
		if err != nil {
			return err
		}
		emote.drawVisible = v
	case "opengl":
		v, err := value.Truth()
		if err != nil {
			return err
		}
		emote.opengl = v
	case "meshDivisionRatio":
		n, err := value.Real()
		if err != nil {
			return err
		}
		emote.meshDivisionRatio = n
	case "hairScale":
		n, err := value.Real()
		if err != nil {
			return err
		}
		emote.hairScale = n
		state.hairScale = n
		emote.modified = true
	case "partsScale":
		n, err := value.Real()
		if err != nil {
			return err
		}
		emote.partsScale = n
		state.partsScale = n
		emote.modified = true
	case "bustScale":
		n, err := value.Real()
		if err != nil {
			return err
		}
		emote.bustScale = n
		state.bustScale = n
		emote.modified = true
	case "bodyScale":
		n, err := value.Real()
		if err != nil {
			return err
		}
		emote.bodyScale = n
	case "drawOpacity":
		n, err := value.Real()
		if err != nil {
			return err
		}
		emote.drawOpacity = n
	case "progress":
		n, err := value.Real()
		if err != nil {
			return err
		}
		emote.progress = n
	default:
		return fmt.Errorf("%w: Motion.EmotePlayer.%s", ErrUnsupportedNative, property)
	}
	return nil
}

// emotePlayerBindMotionKey mirrors Player::bindMotionModuleKey: the key must
// name a PSB module the ResourceManager already holds; binding installs the
// snapshot without starting playback and does not load from storage.
func (s *Services) emotePlayerBindMotionKey(vm *tjs.VM, state *motionPlayerState, emote *emotePlayerState, key string) error {
	if key == "" {
		emote.motionKey = ""
		emote.module = tjs.VoidValue
		emote.moduleMotion, emote.moduleChara = "", ""
		state.resource = nil
		state.attached = nil
		s.emoteResetRuntime(emote)
		emote.modified = true
		return nil
	}
	resource := s.motionResource(key, state.resourceManager)
	if resource == nil {
		// Reference warns and returns without binding when the manager has no
		// module under that key.
		return nil
	}
	state.resource = resource
	state.attached = nil
	module, err := psbValueToTJS(vm, resource.doc.Root, resource.data, 0)
	if err != nil {
		return err
	}
	if module.Kind() == tjs.KindObject && !module.IsNull() {
		s.mu.Lock()
		s.MotionModules[module.ObjectRef().Object] = resource
		s.mu.Unlock()
	}
	emote.motionKey = key
	emote.module = module
	s.emoteResetRuntime(emote)
	s.emoteApplyResourceMetadata(emote, resource)
	state.metadataResource = resource
	state.chara = motionResourceMetadataString(resource, "chara")
	if state.chara == "" {
		state.chara = motionResourceDefaultChara(resource)
	}
	emote.modified = true
	return nil
}

// emoteResetRuntime clears the state loadFromSnapshot(nullptr) resets while
// leaving the Emote shell properties (visibility, transforms, scales) intact.
func (s *Services) emoteResetRuntime(emote *emotePlayerState) {
	emote.moduleMotion, emote.moduleChara = "", ""
	emote.variables = make(map[string]*emoteVariableState)
	emote.variableOrder = nil
	emote.evalResults = make(map[string]float64)
	emote.evalOrder = nil
	emote.parameters = nil
	emote.parameterById = make(map[string]int)
	emote.controllerBindings = make(map[string]emoteControllerBinding)
	emote.animators = make(map[int]map[string]*emoteAnimator)
	emote.selectorControls = make(map[string]*emoteSelectorControl)
	emote.clampControls = nil
	emote.mirrorLabels = nil
	emote.instantVariables = make(map[string]bool)
	emote.timelines = make(map[string]*emoteTimelineState)
	emote.timelineTotals = make(map[string]float64)
	emote.timelineLoops = make(map[string]bool)
	emote.timelineOrder = nil
	emote.mainTimelines = nil
	emote.diffTimelines = nil
	emote.playingOrder = nil
}

// emoteApplyResourceMetadata installs the parameter, control, variable and
// timeline tables declared by metadata/base.
func (s *Services) emoteApplyResourceMetadata(emote *emotePlayerState, resource *motionResource) {
	metadata := motionResourceEmoteMetadata(resource)
	emote.variables = metadata.variables
	emote.variableOrder = metadata.variableOrder
	emote.parameters = metadata.parameters
	emote.parameterById = metadata.parameterById
	emote.controllerBindings = metadata.controllerBindings
	emote.selectorControls = metadata.selectors
	emote.clampControls = metadata.clamps
	emote.mirrorLabels = metadata.mirrorLabels
	emote.instantVariables = metadata.instants
	emote.mainTimelines = metadata.mainTimelines
	emote.diffTimelines = metadata.diffTimelines
	emote.timelines = metadata.timelines
	emote.timelineOrder = metadata.timelineOrder
	emote.timelineTotals = metadata.timelineTotals
	emote.timelineLoops = metadata.timelineLoops
	for label, timeline := range emote.timelines {
		if loop, ok := metadata.timelineLoops[label]; ok {
			timeline.loop = loop
		}
	}
	emote.moduleMotion = motionResourceMetadataBaseString(resource, "motion")
	emote.moduleChara = motionResourceMetadataBaseString(resource, "chara")
	if emote.moduleChara == "" {
		emote.moduleChara = motionResourceDefaultChara(resource)
	}
}

// emotePlayerPlay follows the reference play() three start modes: a bound
// motionKey module, a single cached motion PSB, or a MultiCache composite whose
// primary is the first cached module carrying chara+motion metadata.
func (s *Services) emotePlayerPlay(vm *tjs.VM, state *motionPlayerState, emote *emotePlayerState, args []tjs.Value) (tjs.Value, error) {
	label := ""
	if len(args) > 0 && args[0].Kind() != tjs.KindVoid {
		label = args[0].Text()
	}
	flags := int64(0)
	if len(args) > 1 && args[1].Kind() != tjs.KindVoid {
		value, err := args[1].Int()
		if err != nil {
			return tjs.VoidValue, err
		}
		flags = value
	}
	if label == "" {
		label = emote.clipLabel
	}
	clipLookup := ""
	selfClear := true
	bound := state.resource
	if bound != nil {
		clipLookup = emote.moduleMotion
		if clipLookup == "" {
			clipLookup = label
		}
	} else {
		cached := s.uniqueMotionResources(state.resourceManager)
		switch {
		case len(cached) == 1 && motionModuleIsMotion(cached[0]):
			selfClear = true
			if state.resource == nil {
				state.resource = cached[0]
				emote.motionKey = cached[0].path
				module, _, err := s.motionModuleValueForResource(vm, state.resourceManager, cached[0])
				if err != nil {
					return tjs.VoidValue, err
				}
				emote.module = module
				s.emoteResetRuntime(emote)
				s.emoteApplyResourceMetadata(emote, cached[0])
				state.chara = emote.moduleChara
			}
			clipLookup = label
		case len(cached) > 1:
			selfClear = false
			var primary *motionResource
			for _, candidate := range cached {
				chara := motionResourceMetadataBaseString(candidate, "chara")
				motion := motionResourceMetadataBaseString(candidate, "motion")
				if chara == "" || motion == "" {
					continue
				}
				if primary == nil {
					primary = candidate
					clipLookup = motion
				}
			}
			if primary != nil {
				state.resource = primary
				emote.motionKey = primary.path
				module, _, err := s.motionModuleValueForResource(vm, state.resourceManager, primary)
				if err != nil {
					return tjs.VoidValue, err
				}
				emote.module = module
				s.emoteResetRuntime(emote)
				s.emoteApplyResourceMetadata(emote, primary)
				state.chara = motionResourceMetadataBaseString(primary, "chara")
				state.attached = make([]*motionResource, 0, len(cached)-1)
				for _, candidate := range cached {
					if candidate != primary {
						state.attached = append(state.attached, candidate)
					}
				}
			}
		}
	}
	if flags&motionPlayFlagStealth != 0 {
		state.stealthMotion = clipLookup
		return tjs.IntegerValue(0), nil
	}
	if clipLookup == "" {
		if label == "" {
			return tjs.IntegerValue(0), fmt.Errorf("motionplayer: EmotePlayer.play() could not resolve motion module")
		}
		clipLookup = label
	}
	if state.resource == nil {
		return tjs.IntegerValue(0), nil
	}
	emote.clipLabel = label
	emote.isSelfClear = selfClear
	emote.progress = 0
	state.frameTick = 0
	state.clampedEvalTime = 0
	state.frameLoopTime = 0
	state.speed = true
	state.speedSet = true
	emote.frameLoopTime = 0
	started := s.emoteStartMotion(state, emote, clipLookup, flags)
	if !started && label != "" && label != clipLookup {
		started = s.emoteStartMotion(state, emote, label, flags)
	}
	state.playing = true
	emote.modified = true
	return emoteBoolValue(started), nil
}

// emoteStartMotion is Player::playMotionLike_0x6B2284: select the clip and start
// the matching timeline, or the primary main/diff timelines when the label is
// not a timeline.
func (s *Services) emoteStartMotion(state *motionPlayerState, emote *emotePlayerState, lookup string, flags int64) bool {
	if flags&motionPlayFlagForce != 0 {
		s.emoteStopTimeline(emote, "")
	}
	_, hasTimeline := emote.timelines[lookup]
	primary := emote.mainTimelines
	if len(primary) == 0 {
		primary = emote.diffTimelines
	}
	if !hasTimeline && len(primary) == 0 {
		return false
	}
	if lookup != "" && flags&motionPlayFlagStealth == 0 {
		state.motion = lookup
	}
	state.playing = true
	state.held = false
	if flags&motionPlayFlagChain == 0 {
		state.frameTick = 0
	}
	if hasTimeline {
		s.emoteStartTimeline(emote, lookup, flags)
		return true
	}
	for _, timelineLabel := range primary {
		s.emoteStartTimeline(emote, timelineLabel, flags)
	}
	return true
}

func (s *Services) emoteSkipToSync(state *motionPlayerState, emote *emotePlayerState) {
	syncTime := 0.0
	for _, timeline := range emote.timelines {
		if !timeline.control {
			continue
		}
		syncTime = math.Max(syncTime, timeline.lastTime)
		if timeline.loopEnd >= timeline.loopBegin {
			syncTime = math.Max(syncTime, timeline.loopEnd)
		}
	}
	if resource := s.playerResource(state); resource != nil {
		syncTime = math.Max(syncTime, motionField(resource, state.chara, state.motion, "totalFrames", 0))
	}
	for _, timeline := range emote.timelines {
		timeline.time = syncTime
		if timeline.control {
			s.emoteResetTimelineControl(emote, timeline, syncTime)
		}
		if timeline.totalFrames > 0 && syncTime >= timeline.totalFrames && !timeline.loop {
			timeline.playing = false
		}
	}
	playing := emote.playingOrder[:0]
	for _, label := range emote.playingOrder {
		if timeline := emote.timelines[label]; timeline != nil && timeline.playing {
			playing = append(playing, label)
		}
	}
	emote.playingOrder = playing
	s.emoteApplyEvalPostProcess(emote)
	state.syncWaiting = false
	state.syncActive = false
	state.frameLoopTime = syncTime
	state.clampedEvalTime = syncTime
	emote.modified = true
}

// emotePlayerProgress follows Player::progressMsLike_0x6D2A54. Emote-like
// motions take progressEmoteLike_sdl3, which advances timelines and applies the
// eval post-process/sync pass but never steps variable animator buckets;
// non-emote motions clear eval values and step the type4/5/6/8/7 buckets.
func (s *Services) emotePlayerProgress(state *motionPlayerState, emote *emotePlayerState, args []tjs.Value) (tjs.Value, error) {
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
	frames := interval * motionFramesPerMillisecond
	emote.progress += interval
	if !motionPlayerSpeed(state) && !emote.freezeBodyMotion() {
		emote.modified = true
		return tjs.VoidValue, nil
	}
	state.frameLastTime = frames
	emote.frameLastTime = frames
	if interval > 0 && motionPlayerSpeed(state) {
		if state.playing {
			state.frameTick += frames
			if resource := s.playerResource(state); resource != nil {
				if last := motionLastTime(resource, state.chara, state.motion); last >= 0 && state.frameTick >= last {
					advanceMotionClock(state, last, motionPlayerLoopTime(state, resource))
				}
			}
		}
		state.frameLoopTime += frames
		state.loopTime += frames
		emote.frameLoopTime += frames
		emote.loopTime += frames
		s.emoteAdvanceTimelines(emote, frames)
	}
	if emote.freezeBodyMotion() {
		// progressEmoteLike_sdl3: no eval clear, no controller bucket stepping.
		s.emoteApplyEvalPostProcess(emote)
	} else {
		emote.evalResults = make(map[string]float64)
		emote.evalOrder = nil
		s.emoteStepControllerBuckets(emote, frames)
		s.emoteApplyEvalPostProcess(emote)
	}
	state.syncActive = state.syncWaiting && len(emote.playingOrder) > 0
	state.clampedEvalTime = 0
	emote.modified = true
	return tjs.VoidValue, nil
}

// emoteStepControllerBuckets mirrors frameProgress's 1.1-frame chunks and the
// fixed type4 -> type5 -> type6 -> type8 -> type7 order.
func (s *Services) emoteStepControllerBuckets(emote *emotePlayerState, dt float64) {
	remaining := dt
	for remaining > 0 {
		step := min(remaining, 1.1)
		for _, typ := range []int{4, 5, 6, 8, 7} {
			bucket := emote.animators[typ]
			for label, state := range bucket {
				value, _ := stepEmoteAnimator(state, step)
				s.emoteWriteEval(emote, label, value)
			}
		}
		s.emoteRefreshFixedOutputs(emote)
		remaining -= step
	}
}

func (s *Services) emoteRefreshFixedOutputs(emote *emotePlayerState) {
	for label, binding := range emote.controllerBindings {
		switch binding.typ {
		case 4, 5, 6, 7, 8:
		default:
			continue
		}
		value := 0.0
		if animator := emote.animator(binding.typ, label); animator != nil {
			value = animator.currentValue
		} else if variable := emote.variables[label]; variable != nil && variable.set {
			value = variable.value
		} else {
			value = emoteVariableValue(emote, label)
		}
		s.emoteWriteEval(emote, label, value)
	}
}

// emoteApplyEvalPostProcess mirrors applyEvalResultPostProcessLike_0x67CC9C.
func (s *Services) emoteApplyEvalPostProcess(emote *emotePlayerState) {
	order := append([]string(nil), emote.evalOrder...)
	for _, label := range order {
		value := emote.evalResults[label]
		value += emoteTimelineContribution(emote, label)
		if emoteShouldMirror(emote, label) {
			value = -value
		}
		s.emoteWriteEval(emote, label, value)
	}
	s.emoteApplyClampControls(emote)
	faceTalk, hasFaceTalk := emote.evalResults["face_talk"]
	if !hasFaceTalk {
		if variable := emote.variables["face_talk"]; variable != nil && variable.set {
			faceTalk, hasFaceTalk = variable.value, true
		}
	}
	if hasFaceTalk {
		for label, binding := range emote.controllerBindings {
			if binding.typ == 6 && binding.role == "talkLabel" {
				s.emoteWriteEval(emote, label, faceTalk)
			}
		}
	}
}

func emoteShouldMirror(emote *emotePlayerState, label string) bool {
	if emote == nil || !emote.mirror {
		return false
	}
	for _, candidate := range emote.mirrorLabels {
		if candidate == label {
			return true
		}
	}
	return false
}

func emoteTimelineContribution(emote *emotePlayerState, label string) float64 {
	total := 0.0
	for _, timelineLabel := range emote.playingOrder {
		timeline := emote.timelines[timelineLabel]
		if timeline == nil || timeline.flags&motionPlayFlagChain == 0 {
			continue
		}
		for index, track := range timeline.tracks {
			if track.instant || len(track.frames) == 0 || track.label != label || index >= len(timeline.controlValues) {
				continue
			}
			total += timeline.controlValues[index] * timeline.blendRatio
		}
	}
	return total
}

func (s *Services) emoteApplyClampControls(emote *emotePlayerState) {
	for _, binding := range emote.clampControls {
		if binding.varLR == "" || binding.varUD == "" {
			continue
		}
		valueRange := binding.max - binding.min
		if math.Abs(valueRange) <= 1e-7 {
			continue
		}
		lrValue, ok := emote.evalResults[binding.varLR]
		if !ok {
			lrValue = emoteVariableRawValue(emote, binding.varLR)
		}
		udValue, ok := emote.evalResults[binding.varUD]
		if !ok {
			udValue = emoteVariableRawValue(emote, binding.varUD)
		}
		lrNorm := (lrValue-binding.min)/valueRange*2 - 1
		udNorm := (udValue-binding.min)/valueRange*2 - 1
		if lrNorm != 0 && udNorm != 0 {
			if binding.typ == 1 {
				radius := math.Hypot(lrNorm, udNorm)
				if radius > 1 {
					angle := math.Atan2(udNorm, lrNorm)
					lrNorm, udNorm = math.Cos(angle), math.Sin(angle)
				}
			} else {
				ratio := math.Abs(lrNorm / udNorm)
				if ratio > 1 {
					ratio = 1 / ratio
				}
				inverseLength := 1 / math.Sqrt(ratio*ratio+1)
				projectedX, projectedY := lrNorm*inverseLength, udNorm*inverseLength
				projectedLength := math.Hypot(projectedX, projectedY)
				if projectedLength > 0 {
					scale := (1-math.Cos(ratio*math.Pi/2))*((math.Sin(projectedLength*math.Pi/2)/projectedLength)-1) + 1
					lrNorm, udNorm = projectedX*scale, projectedY*scale
				}
			}
		}
		lrFinal := binding.min + valueRange*(lrNorm+1)*0.5
		udFinal := binding.min + valueRange*(udNorm+1)*0.5
		if emoteShouldMirror(emote, binding.varLR) {
			lrFinal = -lrFinal
		}
		s.emoteWriteEval(emote, binding.varLR, lrFinal)
		s.emoteWriteEval(emote, binding.varUD, udFinal)
	}
}

// emoteVariableRawValue is initialParameterRawValueLike_0x6B1ABC: exact label
// first, then the suffix after the last "::" or "/", over variables then eval
// results.
func emoteVariableRawValue(emote *emotePlayerState, label string) float64 {
	if value, ok := emoteVariableLookup(emote, label); ok {
		return value
	}
	return 0
}

func emoteVariableLookup(emote *emotePlayerState, label string) (float64, bool) {
	labels := []string{label}
	if index := strings.LastIndex(label, "::"); index >= 0 {
		labels = append(labels, label[index+2:])
	} else if index := strings.LastIndex(label, "/"); index >= 0 {
		labels = append(labels, label[index+1:])
	}
	for _, candidate := range labels {
		if variable := emote.variables[candidate]; variable != nil && variable.set {
			return variable.value, true
		}
	}
	for _, candidate := range labels {
		if value, ok := emote.evalResults[candidate]; ok {
			return value, true
		}
	}
	return 0, false
}

func (e *emotePlayerState) animator(typ int, label string) *emoteAnimator {
	if bucket := e.animators[typ]; bucket != nil {
		return bucket[label]
	}
	return nil
}

func (e *emotePlayerState) ensureAnimator(typ int, label string) *emoteAnimator {
	bucket := e.animators[typ]
	if bucket == nil {
		bucket = make(map[string]*emoteAnimator)
		e.animators[typ] = bucket
	}
	animator := bucket[label]
	if animator == nil {
		animator = &emoteAnimator{}
		bucket[label] = animator
	}
	return animator
}

func (e *emotePlayerState) eraseAnimator(label string) {
	for _, bucket := range e.animators {
		delete(bucket, label)
	}
}

func (e *emotePlayerState) ensureVariable(label string) *emoteVariableState {
	variable := e.variables[label]
	if variable == nil {
		variable = &emoteVariableState{}
		e.variables[label] = variable
	}
	return variable
}

// emoteWriteEval is writeEvalResultValueLike_0x6C4668: the eval result and
// _variableValues both receive the value.
func (s *Services) emoteWriteEval(emote *emotePlayerState, label string, value float64) {
	if label == "" {
		return
	}
	emote.evalResults[label] = value
	if !emoteContainsString(emote.evalOrder, label) {
		emote.evalOrder = append(emote.evalOrder, label)
	}
	variable := emote.ensureVariable(label)
	variable.value = value
	variable.set = true
}

func (s *Services) emoteWriteEvalMode(emote *emotePlayerState, label string, mode int64, value float64) {
	s.emoteWriteEval(emote, label, value)
	if label == "" {
		return
	}
	suffix := label
	if index := strings.LastIndex(label, "::"); index >= 0 {
		suffix = label[index+2:]
	} else if index := strings.LastIndex(label, "/"); index >= 0 {
		suffix = label[index+1:]
	}
	for _, parameter := range emote.parameters {
		if parameter.id == label || (suffix != "" && parameter.id == suffix) {
			parameter.value = emoteNormalizeParameter(parameter, value)
			parameter.mode = mode
			parameter.bound = true
		}
	}
}

func (e *emotePlayerState) ensureEvalSlot(label string) {
	if _, ok := e.evalResults[label]; !ok {
		e.evalResults[label] = 0
		if !emoteContainsString(e.evalOrder, label) {
			e.evalOrder = append(e.evalOrder, label)
		}
	}
}

// freezeBodyMotion mirrors isEmoteLikeMotion: a motion with variable labels
// (including control labels) freezes non-parameterized nodes at time 0.
func (e *emotePlayerState) freezeBodyMotion() bool {
	return e != nil && len(e.variableOrder) > 0
}

// renderParameters normalizes the parameter table from the current variable
// values, matching syncParameterEntriesFromVariablesLike_sdl3.
func (e *emotePlayerState) renderParameters() []float64 {
	if e == nil || len(e.parameters) == 0 {
		return nil
	}
	values := make([]float64, len(e.parameters))
	for index, parameter := range e.parameters {
		if parameter.bound {
			values[index] = parameter.value
			continue
		}
		raw, _ := emoteVariableLookup(e, parameter.id)
		values[index] = emoteNormalizeParameter(parameter, raw)
	}
	return values
}

func emoteNormalizeParameter(parameter *emoteParameterEntry, raw float64) float64 {
	valueRange := parameter.rangeEnd - parameter.rangeBegin
	if valueRange == 0 || parameter.rangeScale == 0 {
		return 0
	}
	value := raw
	if parameter.discretization {
		value = float64(int(raw))
	}
	low := math.Min(parameter.rangeBegin, parameter.rangeEnd)
	high := math.Max(parameter.rangeBegin, parameter.rangeEnd)
	value = math.Min(math.Max(value, low), high)
	return (value - parameter.rangeBegin) * parameter.rangeScale
}

// emoteStartWind is Player::startWind (PlayerCore.cpp:1600-1633): negative
// amplitude swaps the angle bounds, degenerate input stops wind, and
// meshDivisionRatio divides the stored scaled amplitude. The state has no
// geometry/render consumer in the reference.
func (s *Services) emoteStartWind(emote *emotePlayerState, minAngle, maxAngle, amplitude, freqX, freqY float64) {
	absoluteAmplitude := math.Abs(amplitude)
	normalizedMin, normalizedMax := minAngle, maxAngle
	if amplitude < 0 {
		normalizedMin, normalizedMax = maxAngle, minAngle
	}
	if absoluteAmplitude == 0 || normalizedMin == normalizedMax || (freqX == 0 && freqY == 0) {
		s.emoteStopWind(emote)
		return
	}
	rebuild := !emote.windActive || emote.windMin != normalizedMin || emote.windMax != normalizedMax
	if rebuild {
		emote.windPhase, emote.windPrevPhase = 0, 0
	}
	direction := 1.0
	if emote.windPrevPhase > emote.windPhase {
		direction = -1
	}
	ratio := emote.meshDivisionRatio
	if ratio == 0 {
		ratio = 1
	}
	emote.windActive = true
	emote.windMin, emote.windMax = normalizedMin, normalizedMax
	emote.windAmp, emote.windFreqX, emote.windFreqY = absoluteAmplitude, freqX, freqY
	emote.windScaledAmp = direction * absoluteAmplitude / ratio
	emote.windCounter = 0
	emote.modified = true
}

func (s *Services) emoteStopWind(emote *emotePlayerState) {
	emote.windActive = false
	emote.windMin, emote.windMax = 0, 0
	emote.windAmp, emote.windFreqX, emote.windFreqY = 0, 0, 0
	emote.windScaledAmp, emote.windPhase, emote.windPrevPhase = 0, 0, 0
	emote.windCounter = 0
}

func emoteVariableEaseWeight(ease float64) float64 {
	switch {
	case ease > 0:
		return ease + 1
	case ease < 0:
		return 1 / (1 - ease)
	default:
		return 1
	}
}

func emoteTimelineEaseWeight(ease float64) float64 {
	switch {
	case ease == 0:
		return 1
	case ease > 0:
		return ease + 1
	default:
		return 1 / (1 - ease)
	}
}

func (s *Services) emoteSetVariable(emote *emotePlayerState, label string, value, transition, ease float64) {
	if label == "" {
		return
	}
	s.emoteSetVariableResolved(emote, label, value, transition, emoteVariableEaseWeight(ease))
}

// emoteSetVariableResolved is setVariableResolvedWeightLike_0x671228: route by
// controllerBindings.type. Types 0-3 only mark the player dirty; 4/5/7/6 use
// per-type animator buckets; 8 drives selector options; labels without a
// binding write the eval map immediately.
func (s *Services) emoteSetVariableResolved(emote *emotePlayerState, label string, value, transition, weight float64) {
	binding, hasBinding := emote.controllerBindings[label]
	if !hasBinding {
		emote.eraseAnimator(label)
		s.emoteWriteEval(emote, label, value)
		return
	}
	switch binding.typ {
	case 0, 1, 2, 3:
		return
	case 4, 5, 7:
		state := emote.ensureAnimator(binding.typ, label)
		emote.ensureEvalSlot(label)
		s.emoteQueueController(emote, label, state, emoteVariableRawValue(emote, label), value, transition, weight)
	case 8:
		if selector, ok := emote.selectorControls[label]; ok {
			selected := int(value)
			emote.eraseAnimator(label)
			s.emoteWriteEval(emote, label, float64(selected))
			for index, option := range selector.options {
				target := option.offValue
				if index == selected {
					target = option.onValue
				}
				current, _ := emoteVariableLookup(emote, option.label)
				optionRange := math.Abs(option.onValue - option.offValue)
				scaledTransition := 0.0
				if transition > 0 && optionRange > 1e-7 {
					scaledTransition = math.Abs(target-current) / optionRange * transition
				}
				state := emote.ensureAnimator(8, option.label)
				emote.ensureEvalSlot(option.label)
				s.emoteQueueController(emote, option.label, state, current, target, scaledTransition, weight)
			}
			return
		}
		state := emote.ensureAnimator(8, label)
		emote.ensureEvalSlot(label)
		s.emoteQueueController(emote, label, state, emoteVariableRawValue(emote, label), value, transition, weight)
	case 6:
		if binding.role == "label" {
			emote.eraseAnimator(label)
			s.emoteWriteEval(emote, label, float64(int(value)))
			return
		}
		state := emote.ensureAnimator(6, label)
		emote.ensureEvalSlot(label)
		s.emoteQueueController(emote, label, state, emoteVariableRawValue(emote, label), value, transition, weight)
	}
}

// emoteSyncSelectorControls mirrors Player::syncSelectorControlsLike_0x670D1C.
// The reference clears each selector and option's transient variable state,
// then writes selector index zero when selector handling is enabled.
func (s *Services) emoteSyncSelectorControls(state *motionPlayerState, emote *emotePlayerState) {
	if state == nil || emote == nil || state.resource == nil {
		return
	}
	resetVariable := func(label string) {
		if label == "" {
			return
		}
		emote.eraseAnimator(label)
		delete(emote.evalResults, label)
		for i, existing := range emote.evalOrder {
			if existing == label {
				emote.evalOrder = append(emote.evalOrder[:i], emote.evalOrder[i+1:]...)
				break
			}
		}
		if variable := emote.variables[label]; variable != nil {
			variable.value = 0
			variable.set = false
		}
	}
	for label, selector := range emote.selectorControls {
		resetVariable(label)
		for _, option := range selector.options {
			resetVariable(option.label)
		}
		if state.selectorEnabled {
			s.emoteSetVariableResolved(emote, label, 0, 0, 1)
		}
	}
}

func (s *Services) emoteQueueController(emote *emotePlayerState, label string, state *emoteAnimator, current, requested, transition, weight float64) {
	if transition <= 0 {
		state.queue = nil
		state.active = false
		state.currentValue = requested
		state.startValue = requested
		state.targetValue = requested
		state.progress = 1
		state.duration = 0
		state.weight = weight
		s.emoteWriteEval(emote, label, requested)
		return
	}
	if !state.active {
		state.queue = nil
		state.active = false
		state.currentValue = current
		state.startValue = current
		state.targetValue = current
		state.progress = 1
		state.duration = 0
	}
	state.queue = append(state.queue, emoteKeyframe{value: requested, duration: transition, weight: weight})
	s.emoteWriteEval(emote, label, state.currentValue)
}

// stepEmoteAnimator is stepQueuedAnimatorLike_0x67D01C.
func stepEmoteAnimator(state *emoteAnimator, dt float64) (float64, bool) {
	remaining := math.Max(dt, 0)
	for remaining > 0 {
		if !state.active {
			if len(state.queue) == 0 {
				return state.currentValue, false
			}
			frame := state.queue[0]
			state.queue = state.queue[1:]
			state.startValue = state.currentValue
			state.targetValue = frame.value
			state.duration = math.Max(frame.duration, 1e-6)
			state.weight = frame.weight
			state.progress = 0
			state.active = true
		}
		remainingDuration := state.duration * math.Max(0, 1-state.progress)
		consume := math.Min(remaining, remainingDuration)
		if state.duration > 0 {
			state.progress = math.Min(1, state.progress+consume/state.duration)
		} else {
			state.progress = 1
		}
		ratio := math.Pow(math.Min(math.Max(state.progress, 0), 1), state.weight)
		state.currentValue = state.startValue + (state.targetValue-state.startValue)*ratio
		remaining -= consume
		if state.progress >= 1 {
			state.currentValue = state.targetValue
			state.active = false
		}
		if consume <= 0 {
			break
		}
	}
	return state.currentValue, state.active || len(state.queue) > 0
}

func (s *Services) emoteEnsureTimeline(emote *emotePlayerState, label string) *emoteTimelineState {
	timeline := emote.timelines[label]
	if timeline == nil {
		timeline = &emoteTimelineState{label: label, blendRatio: 1, lastTime: -1, loopBegin: -1, loopEnd: -1, loopTime: -1}
		emote.timelines[label] = timeline
		if !emoteContainsString(emote.timelineOrder, label) {
			emote.timelineOrder = append(emote.timelineOrder, label)
		}
	}
	return timeline
}

func (s *Services) emotePlayTimeline(emote *emotePlayerState, label string, flags int64) {
	s.emotePlayTimelineWithReset(emote, label, flags, true)
}

func (s *Services) emoteStartTimeline(emote *emotePlayerState, label string, flags int64) {
	if _, exists := emote.timelines[label]; !exists || label == "" {
		return
	}
	timeline := emote.timelines[label]
	if !emoteContainsString(emote.playingOrder, label) {
		emote.playingOrder = append(emote.playingOrder, label)
	}
	timeline.flags = flags
	timeline.playing = true
	timeline.blendRatio = 1
	if flags&motionPlayFlagChain == 0 {
		timeline.time = 0
		timeline.controlInit = false
		timeline.controlLast = 0
		timeline.controlCursor = nil
		timeline.controlValues = nil
		timeline.controlAnims = nil
		if timeline.control {
			s.emoteResetTimelineControl(emote, timeline, 0)
		}
	}
}

func (s *Services) emotePlayTimelineWithReset(emote *emotePlayerState, label string, flags int64, resetPosition bool) {
	if _, exists := emote.timelines[label]; !exists || label == "" {
		return
	}
	if flags&1 != 0 {
		s.emoteStopTimeline(emote, "")
	}
	timeline := s.emoteEnsureTimeline(emote, label)
	if !emoteContainsString(emote.playingOrder, label) {
		emote.playingOrder = append(emote.playingOrder, label)
	}
	timeline.flags = flags
	timeline.playing = true
	timeline.blendRatio = 1
	timeline.blendAnimator = emoteAnimator{}
	timeline.blendAutoStop = false
	if resetPosition {
		timeline.time = 0
		timeline.controlInit = false
		timeline.controlLast = 0
		timeline.controlCursor = nil
		timeline.controlValues = nil
		timeline.controlAnims = nil
		if timeline.control {
			s.emoteResetTimelineControl(emote, timeline, 0)
		}
	}
}

func (s *Services) emoteStopTimeline(emote *emotePlayerState, label string) {
	reset := func(timeline *emoteTimelineState) {
		timeline.playing = false
		timeline.blendRatio = 1
		timeline.blendAnimator = emoteAnimator{}
		timeline.blendAutoStop = false
		timeline.controlInit = false
		timeline.controlCursor = nil
		timeline.controlValues = nil
		timeline.controlAnims = nil
	}
	if label == "" {
		for _, timeline := range emote.timelines {
			reset(timeline)
		}
		emote.playingOrder = nil
		return
	}
	if timeline := emote.timelines[label]; timeline != nil {
		reset(timeline)
	}
	for index, candidate := range emote.playingOrder {
		if candidate == label {
			emote.playingOrder = append(emote.playingOrder[:index], emote.playingOrder[index+1:]...)
			break
		}
	}
}

func (s *Services) emotePlayingTimelines(emote *emotePlayerState) []*emoteTimelineState {
	playing := make([]*emoteTimelineState, 0, len(emote.playingOrder))
	for _, label := range emote.playingOrder {
		if timeline := emote.timelines[label]; timeline != nil && timeline.playing {
			playing = append(playing, timeline)
		}
	}
	return playing
}

func (s *Services) emotePlayingTimelineInfoList(vm *tjs.VM, emote *emotePlayerState) (tjs.Value, error) {
	playing := s.emotePlayingTimelines(emote)
	items := make([]tjs.Value, 0, len(playing))
	for _, timeline := range playing {
		info, err := vm.NewDictionary()
		if err != nil {
			return tjs.VoidValue, err
		}
		for key, value := range map[string]tjs.Value{
			"label":       tjs.StringValue(timeline.label),
			"flags":       tjs.IntegerValue(timeline.flags),
			"loop":        emoteBoolValue(timeline.loop),
			"playing":     emoteBoolValue(timeline.playing),
			"currentTime": tjs.RealValue(timeline.time),
			"totalFrames": tjs.RealValue(timeline.totalFrames),
			"blendRatio":  tjs.RealValue(timeline.blendRatio),
		} {
			if err := tjs.SetMember(vm.Objects, info, key, value); err != nil {
				return tjs.VoidValue, err
			}
		}
		items = append(items, info)
	}
	return vm.NewArray(items)
}

func (s *Services) emoteTimelineLoop(emote *emotePlayerState, label string) bool {
	timeline := emote.timelines[label]
	if timeline == nil {
		return false
	}
	if timeline.control {
		// Reference getLoopTimeline: timelineControl.lastTime < 0.
		return timeline.lastTime < 0
	}
	if loop, ok := emote.timelineLoops[label]; ok {
		return loop
	}
	return timeline.loop
}

func (s *Services) emoteTimelineTotalFrames(emote *emotePlayerState, label string) float64 {
	timeline := emote.timelines[label]
	if timeline == nil {
		return emote.timelineTotals[label]
	}
	if timeline.control && timeline.loopEnd >= timeline.loopBegin {
		return timeline.loopEnd - timeline.loopBegin + 1
	}
	if timeline.totalFrames != 0 {
		return timeline.totalFrames
	}
	return emote.timelineTotals[label]
}

// emoteSetTimelineBlendLike is setTimelineBlendLike_0x6735AC.
func (s *Services) emoteSetTimelineBlendLike(emote *emotePlayerState, label string, autoStop bool, value, transition, ease float64) {
	timeline := emote.timelines[label]
	if timeline == nil {
		return
	}
	timeline.blendAutoStop = autoStop
	weight := emoteTimelineEaseWeight(ease)
	if transition <= 0 {
		timeline.blendAnimator = emoteAnimator{}
		timeline.blendAnimator.currentValue = value
		timeline.blendAnimator.startValue = value
		timeline.blendAnimator.targetValue = value
		timeline.blendAnimator.progress = 1
		timeline.blendAnimator.weight = weight
		timeline.blendRatio = value
		return
	}
	timeline.blendAnimator.queue = append(timeline.blendAnimator.queue, emoteKeyframe{value: value, duration: transition, weight: weight})
	if !timeline.blendAnimator.active && len(timeline.blendAnimator.queue) == 1 && timeline.blendAnimator.progress >= 1 {
		timeline.blendAnimator.startValue = timeline.blendAnimator.currentValue
		timeline.blendAnimator.targetValue = timeline.blendAnimator.currentValue
	}
}

// emoteAdvanceTimelines advances playing timelines by dt frames, applies
// control windows, and steps the flags&2 internal-route animators
// (preProgressPlayingTimelinesLike_0x671764).
func (s *Services) emoteAdvanceTimelines(emote *emotePlayerState, dt float64) {
	if dt <= 0 {
		return
	}
	for _, label := range append([]string(nil), emote.playingOrder...) {
		timeline := emote.timelines[label]
		if timeline == nil || !timeline.playing {
			continue
		}
		if timeline.control && timeline.flags&motionPlayFlagChain != 0 {
			stepEmoteAnimator(&timeline.blendAnimator, dt)
			timeline.blendRatio = timeline.blendAnimator.currentValue
			for index := range timeline.controlAnims {
				value, _ := stepEmoteAnimator(&timeline.controlAnims[index], dt)
				timeline.controlValues[index] = value
			}
		}
		target := timeline.time + dt
		if timeline.control {
			s.emoteApplyTimelineControlWindow(emote, timeline, target, true)
		}
		if timeline.loopBegin < 0 {
			lastTime := timeline.lastTime
			if lastTime < 0 {
				lastTime = timeline.totalFrames
			}
			if lastTime > 0 && target >= lastTime {
				if !timeline.control && timeline.loop {
					// wrapTimelineCurrentTime with loopTime 0: restart at 0.
					for guard := 0; target >= lastTime && guard < 1024; guard++ {
						target -= lastTime
					}
					timeline.time = target
				} else {
					timeline.time = lastTime
					if timeline.blendAutoStop && (timeline.blendAnimator.active || len(timeline.blendAnimator.queue) > 0) {
						// Keep playing until the fade-out animator drains.
					} else {
						timeline.playing = false
					}
				}
			} else {
				timeline.time = target
			}
		} else if timeline.loopEnd > timeline.loopBegin {
			for guard := 0; target >= timeline.loopEnd && guard < 1024; guard++ {
				target = timeline.loopBegin + (target - timeline.loopEnd)
				s.emoteResetTimelineControl(emote, timeline, timeline.loopBegin)
			}
			timeline.time = target
		} else {
			timeline.time = target
		}
		if timeline.control && timeline.flags&motionPlayFlagChain != 0 && timeline.blendAutoStop &&
			!timeline.blendAnimator.active && len(timeline.blendAnimator.queue) == 0 && timeline.time >= timeline.lastTime {
			timeline.playing = false
		}
	}
	// Drop timelines that stopped during advancement.
	playing := emote.playingOrder[:0]
	for _, label := range emote.playingOrder {
		if timeline := emote.timelines[label]; timeline != nil && timeline.playing {
			playing = append(playing, label)
		}
	}
	emote.playingOrder = playing
}

func (s *Services) emoteResetTimelineControl(emote *emotePlayerState, timeline *emoteTimelineState, time float64) {
	timeline.controlCursor = make([]int, len(timeline.tracks))
	timeline.controlValues = make([]float64, len(timeline.tracks))
	timeline.controlAnims = make([]emoteAnimator, len(timeline.tracks))
	for index, track := range timeline.tracks {
		cursor := -1
		for frameIndex, frame := range track.frames {
			if frame.time > time {
				break
			}
			cursor = frameIndex
		}
		timeline.controlCursor[index] = cursor
		active := cursor
		for active >= 0 && track.frames[active].typeZero {
			active--
		}
		if active < 0 {
			continue
		}
		frame := track.frames[active]
		transition := 0.0
		if next := active + 1; next < len(track.frames) {
			transition = math.Max(track.frames[next].time-time-1, 0)
		}
		if timeline.flags&motionPlayFlagChain != 0 && !track.instant {
			scheduleControlAnimator(timeline, index, frame.value, transition, frame.weight)
		} else {
			s.emoteSetVariableResolved(emote, track.label, frame.value, transition, frame.weight)
		}
	}
	timeline.controlInit = true
	timeline.controlLast = time
}

func (s *Services) emoteApplyTimelineControlWindow(emote *emotePlayerState, timeline *emoteTimelineState, target float64, inclusiveEnd bool) {
	if len(timeline.controlCursor) != len(timeline.tracks) {
		timeline.controlCursor = make([]int, len(timeline.tracks))
		for index := range timeline.controlCursor {
			timeline.controlCursor[index] = -1
		}
	}
	if len(timeline.controlValues) < len(timeline.tracks) {
		timeline.controlValues = append(timeline.controlValues, make([]float64, len(timeline.tracks)-len(timeline.controlValues))...)
	}
	if len(timeline.controlAnims) < len(timeline.tracks) {
		timeline.controlAnims = append(timeline.controlAnims, make([]emoteAnimator, len(timeline.tracks)-len(timeline.controlAnims))...)
	}
	for index, track := range timeline.tracks {
		if track.label == "" || len(track.frames) == 0 {
			continue
		}
		if timeline.flags&4 != 0 && track.instant {
			continue
		}
		internalRoute := timeline.flags&motionPlayFlagChain != 0 && !track.instant
		cursor := timeline.controlCursor[index]
		lastCursor := len(track.frames) - 1
		if cursor >= lastCursor {
			continue
		}
		for cursor+1 < len(track.frames) {
			nextIndex := cursor + 1
			next := track.frames[nextIndex]
			crossed := next.time < target
			if inclusiveEnd {
				crossed = next.time <= target
			}
			if !crossed {
				break
			}
			if !next.typeZero && nextIndex+1 < len(track.frames) {
				following := track.frames[nextIndex+1]
				transition := math.Max(following.time-target-1, 0)
				if internalRoute {
					scheduleControlAnimator(timeline, index, next.value, transition, next.weight)
				} else {
					s.emoteSetVariableResolved(emote, track.label, next.value, transition, next.weight)
				}
			}
			cursor = nextIndex
		}
		timeline.controlCursor[index] = cursor
	}
	timeline.time = target
	timeline.controlLast = target
}

func scheduleControlAnimator(timeline *emoteTimelineState, index int, value, transition, weight float64) {
	if index >= len(timeline.controlAnims) {
		return
	}
	animator := &timeline.controlAnims[index]
	target := value
	if transition <= 0 {
		animator.queue = nil
		animator.active = false
		animator.currentValue = target
		animator.startValue = target
		animator.targetValue = target
		animator.progress = 1
		animator.duration = 0
		animator.weight = weight
		if index < len(timeline.controlValues) {
			timeline.controlValues[index] = target
		}
		return
	}
	animator.queue = append(animator.queue, emoteKeyframe{value: target, duration: transition, weight: weight})
	if !animator.active && len(animator.queue) == 1 && animator.progress >= 1 {
		animator.startValue = animator.currentValue
		animator.targetValue = animator.currentValue
	}
}

func (s *Services) emoteVariableFrameList(vm *tjs.VM, emote *emotePlayerState, label string) (tjs.Value, error) {
	variable := emote.variables[label]
	if variable == nil {
		return vm.NewArray(nil)
	}
	items := make([]tjs.Value, 0, len(variable.frames))
	for _, frame := range variable.frames {
		entry, err := vm.NewDictionary()
		if err != nil {
			return tjs.VoidValue, err
		}
		if err := tjs.SetMember(vm.Objects, entry, "label", tjs.StringValue(frame.label)); err != nil {
			return tjs.VoidValue, err
		}
		if err := tjs.SetMember(vm.Objects, entry, "frame", tjs.RealValue(frame.value)); err != nil {
			return tjs.VoidValue, err
		}
		if err := tjs.SetMember(vm.Objects, entry, "value", tjs.RealValue(frame.value)); err != nil {
			return tjs.VoidValue, err
		}
		items = append(items, entry)
	}
	return vm.NewArray(items)
}

// emotePlayerContains implements the reference AABB hit test: the player's
// motion bounds scaled and translated by the current transform. The label form
// resolves the active PSB layer shape in motion-local coordinates.
func (s *Services) emotePlayerContains(state *motionPlayerState, emote *emotePlayerState, args []tjs.Value) (tjs.Value, error) {
	if !emote.visible {
		return tjs.IntegerValue(0), nil
	}
	if len(args) == 0 {
		return tjs.VoidValue, fmt.Errorf("Motion.EmotePlayer.contains requires coordinates")
	}
	var x, y float64
	if len(args) >= 3 {
		label := args[0].Text()
		if label == "" {
			return tjs.IntegerValue(0), nil
		}
		var err error
		if x, err = args[1].Real(); err != nil {
			return tjs.VoidValue, err
		}
		if y, err = args[2].Real(); err != nil {
			return tjs.VoidValue, err
		}
		resource := s.playerResource(state)
		if resource == nil || resource.doc == nil {
			return tjs.IntegerValue(0), nil
		}
		root, _ := resource.doc.Root.(map[string]assets.Value)
		motion := motionResourceMotionEntry(root, state.chara, state.motion)
		layers, _ := motion["layer"].([]assets.Value)
		eval := motionRenderEval{
			params: emote.renderParameters(), freezeBody: emote.freezeBodyMotion(),
			completion: state.completionType, attached: state.attached,
		}
		found, hit := hitTestMotionLayer(resource, root, layers, state.clampedEvalTime, eval, label, x, y)
		return emoteBoolValue(found && hit), nil
	}
	if len(args) == 2 {
		var err error
		if x, err = args[0].Real(); err != nil {
			return tjs.VoidValue, err
		}
		if y, err = args[1].Real(); err != nil {
			return tjs.VoidValue, err
		}
	} else {
		return tjs.VoidValue, fmt.Errorf("Motion.EmotePlayer.contains requires coordinates")
	}
	width, height := s.emoteMotionSize(state)
	if width <= 0 || height <= 0 {
		return tjs.IntegerValue(0), nil
	}
	scale := emote.scale
	inside := x >= emote.coordX && x <= emote.coordX+width*scale && y >= emote.coordY && y <= emote.coordY+height*scale
	return emoteBoolValue(inside), nil
}

func (s *Services) emoteMotionHasLayer(state *motionPlayerState, label string) bool {
	resource := s.playerResource(state)
	if resource == nil || resource.doc == nil {
		return false
	}
	root, _ := resource.doc.Root.(map[string]assets.Value)
	motionValue := motionResourceMotionEntry(root, state.chara, state.motion)
	layers, _ := motionValue["layer"].([]assets.Value)
	return motionLayerListHasLabel(layers, label)
}

func motionLayerListHasLabel(layers []assets.Value, label string) bool {
	for _, item := range layers {
		layer, ok := item.(map[string]assets.Value)
		if !ok {
			continue
		}
		if psbLabel(layer) == label {
			return true
		}
		if nested, ok := layer["children"].([]assets.Value); ok && motionLayerListHasLabel(nested, label) {
			return true
		}
	}
	return false
}

func (s *Services) emoteMotionSize(state *motionPlayerState) (float64, float64) {
	resource := s.playerResource(state)
	if resource == nil || resource.doc == nil {
		return 0, 0
	}
	root, _ := resource.doc.Root.(map[string]assets.Value)
	motionValue := motionResourceMotionEntry(root, state.chara, state.motion)
	width := psbMotionFloat(motionValue, "width", 0)
	height := psbMotionFloat(motionValue, "height", 0)
	if width <= 0 || height <= 0 {
		width = psbMotionFloat(root["screenSize"], "width", 0)
		height = psbMotionFloat(root["screenSize"], "height", 0)
	}
	return width, height
}

func motionResourceMotionEntry(root map[string]assets.Value, chara, motion string) map[string]assets.Value {
	objects, _ := root["object"].(map[string]assets.Value)
	character, _ := objects[chara].(map[string]assets.Value)
	motions, _ := character["motion"].(map[string]assets.Value)
	value, _ := motions[motion].(map[string]assets.Value)
	return value
}

// emotePlayerSerialize follows Player::serialize: chara, motion, tickcount,
// speed, outline, the metadata variable values, and the playing timeline info.
func (s *Services) emotePlayerSerialize(vm *tjs.VM, state *motionPlayerState, emote *emotePlayerState) (tjs.Value, error) {
	object, err := vm.NewDictionary()
	if err != nil {
		return tjs.VoidValue, err
	}
	tickCount := 0.0
	if state.frameTick > 0 {
		tickCount = state.frameTick * 1000 / 60
	}
	fields := map[string]tjs.Value{
		"chara":     tjs.StringValue(state.chara),
		"motion":    tjs.StringValue(state.motion),
		"tickcount": tjs.RealValue(tickCount),
		"speed":     tjs.IntegerValue(boolInt(motionPlayerSpeed(state))),
		"outline":   tjs.StringValue(state.outline),
	}
	for key, value := range fields {
		if err := tjs.SetMember(vm.Objects, object, key, value); err != nil {
			return tjs.VoidValue, err
		}
	}
	variables, err := vm.NewDictionary()
	if err != nil {
		return tjs.VoidValue, err
	}
	seen := make(map[string]bool)
	for _, label := range emote.variableOrder {
		seen[label] = true
		if err := tjs.SetMember(vm.Objects, variables, label, tjs.RealValue(emoteVariableValue(emote, label))); err != nil {
			return tjs.VoidValue, err
		}
	}
	for label, variable := range emote.variables {
		if seen[label] {
			continue
		}
		value := variable.value
		if !variable.set {
			value = emoteVariableValue(emote, label)
		}
		if err := tjs.SetMember(vm.Objects, variables, label, tjs.RealValue(value)); err != nil {
			return tjs.VoidValue, err
		}
	}
	if err := tjs.SetMember(vm.Objects, object, "variables", variables); err != nil {
		return tjs.VoidValue, err
	}
	timelines, err := s.emoteTimelineSerialization(vm, emote)
	if err != nil {
		return tjs.VoidValue, err
	}
	if err := tjs.SetMember(vm.Objects, object, "timelines", timelines); err != nil {
		return tjs.VoidValue, err
	}
	return object, nil
}

func (s *Services) emoteTimelineSerialization(vm *tjs.VM, emote *emotePlayerState) (tjs.Value, error) {
	playing := s.emotePlayingTimelines(emote)
	items := make([]tjs.Value, 0, len(playing))
	for _, timeline := range playing {
		entry, err := vm.NewDictionary()
		if err != nil {
			return tjs.VoidValue, err
		}
		for key, value := range map[string]tjs.Value{
			"label":       tjs.StringValue(timeline.label),
			"flags":       tjs.IntegerValue(timeline.flags),
			"loop":        emoteBoolValue(timeline.loop),
			"playing":     emoteBoolValue(timeline.playing),
			"currentTime": tjs.RealValue(timeline.time),
			"totalFrames": tjs.RealValue(timeline.totalFrames),
			"blendRatio":  tjs.RealValue(timeline.blendRatio),
		} {
			if err := tjs.SetMember(vm.Objects, entry, key, value); err != nil {
				return tjs.VoidValue, err
			}
		}
		items = append(items, entry)
	}
	return vm.NewArray(items)
}

func (s *Services) emotePlayerUnserialize(vm *tjs.VM, state *motionPlayerState, emote *emotePlayerState, data tjs.Value) error {
	if data.Kind() != tjs.KindObject || data.IsNull() {
		return nil
	}
	read := func(key string) (tjs.Value, bool) {
		if !tjs.HasMember(vm.Objects, data, key) {
			return tjs.VoidValue, false
		}
		value, err := tjs.GetMember(vm.Objects, data, key)
		if err != nil {
			return tjs.VoidValue, false
		}
		return value, true
	}
	readString := func(key string, target *string) {
		if value, ok := read(key); ok && value.Kind() != tjs.KindVoid {
			*target = value.Text()
		}
	}
	readReal := func(key string, target *float64) {
		if value, ok := read(key); ok && value.Kind() != tjs.KindVoid {
			if n, err := value.Real(); err == nil {
				*target = n
			}
		}
	}
	readString("chara", &state.chara)
	readString("motion", &state.motion)
	readString("outline", &state.outline)
	tickCount := 0.0
	readReal("tickcount", &tickCount)
	state.frameTick = tickCount * motionFramesPerMillisecond
	if value, ok := read("speed"); ok && value.Kind() != tjs.KindVoid {
		if n, err := value.Real(); err == nil {
			state.speed = n != 0
			state.speedSet = true
		}
	}
	state.held = false
	if value, ok := read("variables"); ok && value.Kind() == tjs.KindObject && !value.IsNull() {
		if object, ok := vm.Objects.Lookup(value.ObjectRef().Object); ok {
			for _, label := range object.Keys() {
				entry, err := tjs.GetMember(vm.Objects, value, label)
				if err != nil || entry.Kind() == tjs.KindVoid {
					continue
				}
				if n, err := entry.Real(); err == nil {
					s.emoteSetVariableResolved(emote, label, n, 0, 1)
				}
			}
		}
	}
	emote.playingOrder = nil
	if value, ok := read("timelines"); ok && value.Kind() == tjs.KindObject && !value.IsNull() {
		items, err := vm.ArrayItems(value)
		if err == nil {
			for _, item := range items {
				if item.Kind() != tjs.KindObject || item.IsNull() {
					continue
				}
				labelValue, err := tjs.GetMember(vm.Objects, item, "label")
				if err != nil {
					continue
				}
				timeline := s.emoteEnsureTimeline(emote, labelValue.Text())
				if v, err := tjs.GetMember(vm.Objects, item, "playing"); err == nil {
					if b, err := v.Truth(); err == nil {
						timeline.playing = b
					}
				}
				if v, err := tjs.GetMember(vm.Objects, item, "flags"); err == nil {
					if n, err := v.Int(); err == nil {
						timeline.flags = n
					}
				}
				if v, err := tjs.GetMember(vm.Objects, item, "loop"); err == nil {
					if b, err := v.Truth(); err == nil {
						timeline.loop = b
					}
				}
				if v, err := tjs.GetMember(vm.Objects, item, "currentTime"); err == nil {
					if n, err := v.Real(); err == nil {
						timeline.time = n
					}
				}
				if v, err := tjs.GetMember(vm.Objects, item, "totalFrames"); err == nil {
					if n, err := v.Real(); err == nil {
						timeline.totalFrames = n
					}
				}
				if v, err := tjs.GetMember(vm.Objects, item, "blendRatio"); err == nil {
					if n, err := v.Real(); err == nil {
						timeline.blendRatio = n
					}
				}
				if timeline.playing {
					emote.playingOrder = append(emote.playingOrder, timeline.label)
				}
			}
		}
	}
	s.emoteEnsurePlayingOrder(emote)
	return nil
}

func (s *Services) emoteEnsurePlayingOrder(emote *emotePlayerState) {
	for label, timeline := range emote.timelines {
		if timeline.playing && !emoteContainsString(emote.playingOrder, label) {
			emote.playingOrder = append(emote.playingOrder, label)
		}
	}
}

// emotePlayerClone constructs a new Motion.EmotePlayer through the VM and
// copies the shell state, matching the reference clone() which creates an
// adaptor for a copy of the same state.
func (s *Services) emotePlayerClone(vm *tjs.VM, state *motionPlayerState, emote *emotePlayerState) (tjs.Value, error) {
	motion, ok := vm.Globals["Motion"]
	if !ok || motion.Kind() != tjs.KindObject || motion.IsNull() {
		return tjs.VoidValue, fmt.Errorf("Motion namespace is not registered")
	}
	class, err := vm.GetMember(motion, "EmotePlayer", s, nil)
	if err != nil {
		return tjs.VoidValue, err
	}
	instance, err := vm.Construct(class, nil, s, nil)
	if err != nil {
		return tjs.VoidValue, err
	}
	if instance.Kind() != tjs.KindObject || instance.IsNull() {
		return tjs.VoidValue, fmt.Errorf("Motion.EmotePlayer clone did not produce an instance")
	}
	cloneState, cloneEmote := s.emotePlayerStateFor(instance.ObjectRef().Object)
	cloneState.resourceManager = 0
	cloneState.resource = state.resource
	cloneState.speed = true
	cloneState.speedSet = true
	cloneState.drawMatrix = identityMotionDrawMatrix()
	copyEmotePlayerState(cloneEmote, emote)
	s.emoteResetRuntime(cloneEmote)
	s.emoteApplyResourceMetadata(cloneEmote, state.resource)
	cloneState.viewVisible = cloneEmote.visible
	cloneState.d3dDrawMode = cloneEmote.useD3D
	cloneState.hairScale = cloneEmote.hairScale
	cloneState.partsScale = cloneEmote.partsScale
	cloneState.bustScale = cloneEmote.bustScale
	cloneState.chara = cloneEmote.moduleChara
	cloneState.motion = ""
	cloneState.stealthMotion = ""
	cloneState.frameTick = 0
	cloneState.playing = false
	cloneState.held = false
	cloneState.syncWaiting = false
	cloneState.syncActive = false
	cloneState.completionType = 0
	cloneState.outline = ""
	cloneState.onAction = tjs.VoidValue
	cloneState.attached = nil
	return instance, nil
}

func copyEmotePlayerState(target, source *emotePlayerState) {
	target.motionKey = source.motionKey
	target.module = source.module
	target.moduleMotion = source.moduleMotion
	target.moduleChara = source.moduleChara
	target.clipLabel = source.clipLabel
	target.isSelfClear = source.isSelfClear
	target.visible = source.visible
	target.smoothing = source.smoothing
	target.queuing = source.queuing
	target.useD3D = source.useD3D
	target.modified = source.modified
	target.drawVisible = source.drawVisible
	target.opengl = source.opengl
	target.playCallback = source.playCallback
	target.meshDivisionRatio = source.meshDivisionRatio
	target.hairScale = source.hairScale
	target.partsScale = source.partsScale
	target.bustScale = source.bustScale
	target.bodyScale = source.bodyScale
	target.drawOpacity = source.drawOpacity
	target.progress = source.progress
	target.rot = source.rot
	target.coordX = source.coordX
	target.coordY = source.coordY
	target.scale = source.scale
	target.mirror = source.mirror
	target.color = source.color
	target.windActive = source.windActive
	target.windMin = source.windMin
	target.windMax = source.windMax
	target.windAmp = source.windAmp
	target.windFreqX = source.windFreqX
	target.windFreqY = source.windFreqY
	target.windScaledAmp = source.windScaledAmp
	target.windPhase = source.windPhase
	target.windPrevPhase = source.windPrevPhase
	target.windCounter = source.windCounter
	target.physicsRule = source.physicsRule
	target.physicsInit = source.physicsInit
	target.variableOrder = append([]string(nil), source.variableOrder...)
	target.variables = make(map[string]*emoteVariableState, len(source.variables))
	for label, variable := range source.variables {
		copied := *variable
		copied.frames = append([]emoteVariableFrame(nil), variable.frames...)
		target.variables[label] = &copied
	}
	target.evalResults = make(map[string]float64, len(source.evalResults))
	for label, value := range source.evalResults {
		target.evalResults[label] = value
	}
	target.evalOrder = append([]string(nil), source.evalOrder...)
	target.parameters = append([]*emoteParameterEntry(nil), source.parameters...)
	target.parameterById = make(map[string]int, len(source.parameterById))
	for label, index := range source.parameterById {
		target.parameterById[label] = index
	}
	target.controllerBindings = make(map[string]emoteControllerBinding, len(source.controllerBindings))
	for label, binding := range source.controllerBindings {
		target.controllerBindings[label] = binding
	}
	target.animators = make(map[int]map[string]*emoteAnimator, len(source.animators))
	for typ, bucket := range source.animators {
		copiedBucket := make(map[string]*emoteAnimator, len(bucket))
		for label, animator := range bucket {
			copied := *animator
			copied.queue = append([]emoteKeyframe(nil), animator.queue...)
			copiedBucket[label] = &copied
		}
		target.animators[typ] = copiedBucket
	}
	target.selectorControls = make(map[string]*emoteSelectorControl, len(source.selectorControls))
	for label, selector := range source.selectorControls {
		copied := *selector
		copied.options = append([]emoteSelectorOption(nil), selector.options...)
		target.selectorControls[label] = &copied
	}
	target.clampControls = append([]emoteClampControl(nil), source.clampControls...)
	target.mirrorLabels = append([]string(nil), source.mirrorLabels...)
	target.instantVariables = make(map[string]bool, len(source.instantVariables))
	for label, instant := range source.instantVariables {
		target.instantVariables[label] = instant
	}
	target.timelineOrder = append([]string(nil), source.timelineOrder...)
	target.mainTimelines = append([]string(nil), source.mainTimelines...)
	target.diffTimelines = append([]string(nil), source.diffTimelines...)
	target.playingOrder = append([]string(nil), source.playingOrder...)
	target.timelineTotals = make(map[string]float64, len(source.timelineTotals))
	for label, total := range source.timelineTotals {
		target.timelineTotals[label] = total
	}
	target.timelineLoops = make(map[string]bool, len(source.timelineLoops))
	for label, loop := range source.timelineLoops {
		target.timelineLoops[label] = loop
	}
	target.timelines = make(map[string]*emoteTimelineState, len(source.timelines))
	for label, timeline := range source.timelines {
		copied := *timeline
		copied.tracks = append([]emoteTimelineTrack(nil), timeline.tracks...)
		copied.controlCursor = append([]int(nil), timeline.controlCursor...)
		copied.controlValues = append([]float64(nil), timeline.controlValues...)
		copied.controlAnims = append([]emoteAnimator(nil), timeline.controlAnims...)
		target.timelines[label] = &copied
	}
	target.outerForce = make(map[string]emoteOuterForce, len(source.outerForce))
	for label, force := range source.outerForce {
		target.outerForce[label] = force
	}
}

// drawMatrixFor composes the EmotePlayer transform with the shared explicit
// draw matrix: the explicit setDrawAffineTranslateMatrix transform is the outer
// transform, the emote coord/scale/rot/mirror transform the inner one. rot is
// in radians, as the reference setRotate stores it.
func (e *emotePlayerState) drawMatrixFor(outer motionDrawMatrix, state *motionPlayerState) motionDrawMatrix {
	if e == nil || state == nil {
		return outer
	}
	scale := state.emoteScale
	if state.rootFlipX {
		scale = -scale
	}
	cos, sin := math.Cos(state.rotateAngle), math.Sin(state.rotateAngle)
	inner := motionDrawMatrix{
		a: scale * cos, b: -scale * sin,
		c: scale * sin, d: scale * cos,
	}
	return motionDrawMatrix{
		a:  outer.a*inner.a + outer.b*inner.c,
		b:  outer.a*inner.b + outer.b*inner.d,
		c:  outer.c*inner.a + outer.d*inner.c,
		d:  outer.c*inner.b + outer.d*inner.d,
		tx: outer.a*inner.tx + outer.b*inner.ty + outer.tx,
		ty: outer.c*inner.tx + outer.d*inner.ty + outer.ty,
	}
}

// --- PSB metadata parsing ---

type emoteMetadata struct {
	variables          map[string]*emoteVariableState
	variableOrder      []string
	parameters         []*emoteParameterEntry
	parameterById      map[string]int
	controllerBindings map[string]emoteControllerBinding
	selectors          map[string]*emoteSelectorControl
	clamps             []emoteClampControl
	mirrorLabels       []string
	instants           map[string]bool
	mainTimelines      []string
	diffTimelines      []string
	timelines          map[string]*emoteTimelineState
	timelineOrder      []string
	timelineTotals     map[string]float64
	timelineLoops      map[string]bool
}

func motionResourceBase(root map[string]assets.Value) map[string]assets.Value {
	if metadata, ok := root["metadata"].(map[string]assets.Value); ok {
		if base, ok := metadata["base"].(map[string]assets.Value); ok {
			return base
		}
	}
	return root
}

func motionResourceMetadataString(resource *motionResource, key string) string {
	if resource == nil || resource.doc == nil {
		return ""
	}
	root, _ := resource.doc.Root.(map[string]assets.Value)
	base := motionResourceBase(root)
	value, _ := base[key].(string)
	return value
}

func motionResourceMetadataBaseString(resource *motionResource, key string) string {
	return motionResourceMetadataString(resource, key)
}

func motionModuleIsMotion(resource *motionResource) bool {
	if resource == nil || resource.doc == nil {
		return false
	}
	root, _ := resource.doc.Root.(map[string]assets.Value)
	if value, ok := psbNumber(root["type"]); ok {
		return int(value) == 0
	}
	_, hasMotion := root["object"]
	return hasMotion
}

func motionResourceEmoteMetadata(resource *motionResource) emoteMetadata {
	metadata := emoteMetadata{
		variables:          make(map[string]*emoteVariableState),
		parameterById:      make(map[string]int),
		controllerBindings: make(map[string]emoteControllerBinding),
		selectors:          make(map[string]*emoteSelectorControl),
		instants:           make(map[string]bool),
		timelines:          make(map[string]*emoteTimelineState),
		timelineTotals:     make(map[string]float64),
		timelineLoops:      make(map[string]bool),
	}
	if resource == nil || resource.doc == nil {
		return metadata
	}
	root, _ := resource.doc.Root.(map[string]assets.Value)
	base := motionResourceBase(root)
	appendLabel := func(label string) {
		if !emoteContainsString(metadata.variableOrder, label) {
			metadata.variableOrder = append(metadata.variableOrder, label)
		}
		if metadata.variables[label] == nil {
			metadata.variables[label] = &emoteVariableState{}
		}
	}
	// variableList
	if list, ok := base["variableList"].([]assets.Value); ok {
		for _, item := range list {
			dict, ok := item.(map[string]assets.Value)
			if !ok {
				continue
			}
			label := psbLabel(dict)
			if label == "" {
				continue
			}
			variable := metadata.variables[label]
			if variable == nil {
				variable = &emoteVariableState{}
				metadata.variables[label] = variable
			}
			appendLabel(label)
			minValue := math.Inf(1)
			if frames, ok := dict["frameList"].([]assets.Value); ok {
				for index, frameItem := range frames {
					if frameDict, ok := frameItem.(map[string]assets.Value); ok {
						frameLabel := psbLabel(frameDict)
						if frameLabel == "" {
							frameLabel = fmt.Sprintf("%d", index)
						}
						value, _ := psbNumber(frameDict["f"])
						variable.frames = append(variable.frames, emoteVariableFrame{label: frameLabel, value: value})
						minValue = math.Min(minValue, value)
						continue
					}
					if value, ok := psbNumber(frameItem); ok {
						variable.frames = append(variable.frames, emoteVariableFrame{label: fmt.Sprintf("%d", index), value: value})
						minValue = math.Min(minValue, value)
					}
				}
			}
			if !math.IsInf(minValue, 1) {
				variable.rangeMin = minValue
				variable.rangeMax = math.Inf(-1)
				for _, frame := range variable.frames {
					variable.rangeMax = math.Max(variable.rangeMax, frame.value)
				}
				variable.rangeSet = true
			}
		}
	}
	record := func(label string, typ, index int, source, role string) {
		if label == "" {
			return
		}
		appendLabel(label)
		metadata.controllerBindings[label] = emoteControllerBinding{typ: typ, index: index, source: source, role: role}
	}
	collectControls := func(listKey string, typ int, roles [][2]string) {
		list, _ := base[listKey].([]assets.Value)
		for index, item := range list {
			dict, ok := item.(map[string]assets.Value)
			if !ok || !psbBool(dict["enabled"]) {
				continue
			}
			for _, role := range roles {
				if label, ok := dict[role[0]].(string); ok && label != "" {
					record(label, typ, index, listKey, role[1])
				}
			}
		}
	}
	collectControls("bustControl", 0, [][2]string{{"var_lr", "var_lr"}, {"var_ud", "var_ud"}})
	collectControls("hairControl", 1, [][2]string{{"var_lr", "var_lr"}, {"var_lrm", "var_lrm"}, {"var_ud", "var_ud"}})
	collectControls("partsControl", 2, [][2]string{{"var_lr", "var_lr"}, {"var_lrm", "var_lrm"}, {"var_ud", "var_ud"}})
	collectControls("loopControl", 3, [][2]string{{"var_loop", "var_loop"}})
	collectControls("eyeControl", 4, [][2]string{{"label", "label"}})
	collectControls("eyebrowControl", 5, [][2]string{{"label", "label"}})
	collectControls("mouthControl", 6, [][2]string{{"label", "label"}, {"talkLabel", "talkLabel"}})
	collectControls("transitionControl", 7, [][2]string{{"label", "label"}})
	// selectorControl: disabled entries also erase a prior binding.
	if list, ok := base["selectorControl"].([]assets.Value); ok {
		for index, item := range list {
			dict, ok := item.(map[string]assets.Value)
			if !ok {
				continue
			}
			label := psbLabel(dict)
			if label == "" {
				continue
			}
			if !psbBool(dict["enabled"]) {
				delete(metadata.controllerBindings, label)
				continue
			}
			selector := &emoteSelectorControl{}
			if options, ok := dict["optionList"].([]assets.Value); ok {
				for _, optionItem := range options {
					option, ok := optionItem.(map[string]assets.Value)
					if !ok {
						continue
					}
					optionLabel := psbLabel(option)
					if optionLabel == "" {
						continue
					}
					offValue, _ := psbNumber(option["offValue"])
					onValue, _ := psbNumber(option["onValue"])
					selector.options = append(selector.options, emoteSelectorOption{label: optionLabel, offValue: offValue, onValue: onValue})
				}
			}
			metadata.selectors[label] = selector
			record(label, 8, index, "selectorControl", "label")
		}
	}
	if list, ok := base["clampControl"].([]assets.Value); ok {
		for _, item := range list {
			dict, ok := item.(map[string]assets.Value)
			if !ok || !psbBool(dict["enabled"]) {
				continue
			}
			typ, _ := psbNumber(dict["type"])
			minValue, _ := psbNumber(dict["min"])
			maxValue, _ := psbNumber(dict["max"])
			varLR, _ := dict["var_lr"].(string)
			varUD, _ := dict["var_ud"].(string)
			metadata.clamps = append(metadata.clamps, emoteClampControl{
				typ: int(typ), varLR: varLR, varUD: varUD, min: minValue, max: maxValue,
			})
		}
	}
	if mirror, ok := base["mirrorControl"].(map[string]assets.Value); ok {
		if list, ok := mirror["variableMatchList"].([]assets.Value); ok {
			for _, item := range list {
				if label, ok := item.(string); ok && label != "" && !emoteContainsString(metadata.mirrorLabels, label) {
					metadata.mirrorLabels = append(metadata.mirrorLabels, label)
				}
			}
		}
	}
	if list, ok := base["instantVariableList"].([]assets.Value); ok {
		for _, item := range list {
			label := ""
			switch value := item.(type) {
			case string:
				label = value
			case map[string]assets.Value:
				label = psbLabel(value)
			}
			if label == "" {
				continue
			}
			metadata.instants[label] = true
			appendLabel(label)
		}
	}
	// parameterList
	if list, ok := base["parameterList"].([]assets.Value); ok {
		for _, item := range list {
			dict, ok := item.(map[string]assets.Value)
			if !ok {
				continue
			}
			entry := &emoteParameterEntry{}
			entry.id, _ = dict["id"].(string)
			entry.discretization = psbBool(dict["discretization"])
			entry.rangeBegin, _ = psbNumber(dict["rangeBegin"])
			entry.rangeEnd, _ = psbNumber(dict["rangeEnd"])
			valueRange := entry.rangeEnd - entry.rangeBegin
			division, hasDivision := psbNumber(dict["division"])
			if !hasDivision {
				division = valueRange
				if division <= 0 {
					division = 1
				}
			}
			if valueRange != 0 && division > 0 {
				entry.rangeScale = division / valueRange
			}
			if entry.id != "" {
				metadata.parameterById[entry.id] = len(metadata.parameters)
			}
			metadata.parameters = append(metadata.parameters, entry)
		}
	}
	// timelineControl
	if list, ok := base["timelineControl"].([]assets.Value); ok {
		for _, item := range list {
			dict, ok := item.(map[string]assets.Value)
			if !ok {
				continue
			}
			label := psbLabel(dict)
			if label == "" {
				continue
			}
			isDiff := psbBool(dict["diff"])
			if isDiff {
				metadata.diffTimelines = append(metadata.diffTimelines, label)
			} else {
				metadata.mainTimelines = append(metadata.mainTimelines, label)
			}
			timeline := &emoteTimelineState{label: label, blendRatio: 1, loopBegin: -1, loopEnd: -1, lastTime: -1, loopTime: -1, control: true}
			if value, ok := psbNumber(dict["loopBegin"]); ok {
				timeline.loopBegin = value
			}
			if value, ok := psbNumber(dict["loopEnd"]); ok {
				timeline.loopEnd = value
			}
			if value, ok := psbNumber(dict["lastTime"]); ok {
				timeline.lastTime = value
			}
			for _, key := range []string{"frameCount", "frame_count", "totalFrameCount", "total_frame_count", "frames", "length", "end"} {
				if value, ok := psbNumber(dict[key]); ok {
					timeline.totalFrames = value
					break
				}
			}
			if tracks, ok := dict["variableList"].([]assets.Value); ok {
				for _, trackItem := range tracks {
					trackDict, ok := trackItem.(map[string]assets.Value)
					if !ok {
						continue
					}
					trackLabel := psbLabel(trackDict)
					if trackLabel == "" {
						continue
					}
					track := emoteTimelineTrack{label: trackLabel, instant: metadata.instants[trackLabel]}
					if frames, ok := trackDict["frameList"].([]assets.Value); ok {
						for _, frameItem := range frames {
							frameDict, ok := frameItem.(map[string]assets.Value)
							if !ok {
								continue
							}
							frame := emoteTimelineFrame{}
							frame.time, _ = psbNumber(frameDict["time"])
							frameType, _ := psbNumber(frameDict["type"])
							frame.typeZero = int(frameType) == 0
							if !frame.typeZero {
								if content, ok := frameDict["content"].(map[string]assets.Value); ok {
									frame.value, _ = psbNumber(content["value"])
									easing, _ := psbNumber(content["easing"])
									frame.weight = emoteTimelineEaseWeight(easing)
								}
							}
							track.frames = append(track.frames, frame)
						}
					}
					timeline.tracks = append(timeline.tracks, track)
				}
			}
			metadata.timelines[label] = timeline
		}
	}
	metadata.timelineOrder = append(metadata.timelineOrder, metadata.mainTimelines...)
	for _, label := range metadata.diffTimelines {
		if !emoteContainsString(metadata.timelineOrder, label) {
			metadata.timelineOrder = append(metadata.timelineOrder, label)
		}
	}
	// Clip totals/loop flags for getTimelineTotalFrameCount/getLoopTimeline.
	// Clip-derived timelines mirror RuntimeSupport.cpp:1041-1047: every motion
	// clip is a main timeline carrying its own totalFrames and loop time. PSB
	// modules without timelineControl (KAG motion layers such as yuzulogo.psb)
	// are driven entirely through these entries; timelineControl metadata
	// replaces them when present.
	clipDriven := len(metadata.mainTimelines) == 0 && len(metadata.diffTimelines) == 0
	if objects, ok := root["object"].(map[string]assets.Value); ok {
		characters := make([]string, 0, len(objects))
		for name := range objects {
			characters = append(characters, name)
		}
		sort.Strings(characters)
		for _, name := range characters {
			character, ok := objects[name].(map[string]assets.Value)
			if !ok {
				continue
			}
			motions, _ := character["motion"].(map[string]assets.Value)
			labels := make([]string, 0, len(motions))
			for label := range motions {
				labels = append(labels, label)
			}
			sort.Strings(labels)
			for _, label := range labels {
				motionDict, ok := motions[label].(map[string]assets.Value)
				if !ok {
					continue
				}
				total := 0.0
				for _, key := range []string{"lastTime", "frameCount", "frame_count", "totalFrameCount", "total_frame_count", "frames", "length", "end"} {
					if value, ok := psbNumber(motionDict[key]); ok {
						total = value
						break
					}
				}
				metadata.timelineTotals[label] = total
				loop := false
				loopTime := -1.0
				if value, ok := psbNumber(motionDict["loopTime"]); ok {
					loopTime = value
					loop = value >= 0
				} else if value, ok := motionDict["loop"]; ok {
					loop = psbBool(value)
				} else if value, ok := motionDict["repeat"]; ok {
					loop = psbBool(value)
				} else if value, ok := motionDict["is_loop"]; ok {
					loop = psbBool(value)
				}
				if loop {
					loopTime = math.Max(loopTime, 0)
				}
				metadata.timelineLoops[label] = loop
				if !clipDriven {
					continue
				}
				if _, exists := metadata.timelines[label]; exists {
					continue
				}
				metadata.timelines[label] = &emoteTimelineState{
					label: label, blendRatio: 1,
					loopBegin: -1, loopEnd: -1, lastTime: -1,
					loop: loop, loopTime: loopTime, totalFrames: total,
				}
				metadata.mainTimelines = append(metadata.mainTimelines, label)
			}
		}
	}
	if clipDriven {
		for _, label := range metadata.mainTimelines {
			if !emoteContainsString(metadata.timelineOrder, label) {
				metadata.timelineOrder = append(metadata.timelineOrder, label)
			}
		}
	}
	return metadata
}

func motionResourceDefaultChara(resource *motionResource) string {
	if resource == nil || resource.doc == nil {
		return ""
	}
	root, _ := resource.doc.Root.(map[string]assets.Value)
	objects, _ := root["object"].(map[string]assets.Value)
	if len(objects) != 1 {
		return ""
	}
	for name := range objects {
		return name
	}
	return ""
}

// --- small helpers ---

func emoteBoolValue(b bool) tjs.Value {
	if b {
		return tjs.IntegerValue(1)
	}
	return tjs.IntegerValue(0)
}

func emoteIndexArg(args []tjs.Value, index int, method string) (int64, error) {
	if index >= len(args) || args[index].Kind() == tjs.KindVoid {
		return 0, fmt.Errorf("Motion.EmotePlayer.%s requires an index", method)
	}
	return args[index].Int()
}

func emoteOptionalReal(args []tjs.Value, index int) (float64, error) {
	if index >= len(args) || args[index].Kind() == tjs.KindVoid {
		return 0, nil
	}
	return args[index].Real()
}

func emoteOptionalInt(args []tjs.Value, index int) (int64, error) {
	if index >= len(args) || args[index].Kind() == tjs.KindVoid {
		return 0, nil
	}
	return args[index].Int()
}

func emoteRealArgs(args []tjs.Value, count int) ([]float64, error) {
	values := make([]float64, count)
	for i := range count {
		if i >= len(args) || args[i].Kind() == tjs.KindVoid {
			continue
		}
		value, err := args[i].Real()
		if err != nil {
			return nil, err
		}
		values[i] = value
	}
	return values, nil
}

func emoteStringValues(labels []string) []tjs.Value {
	values := make([]tjs.Value, len(labels))
	for i, label := range labels {
		values[i] = tjs.StringValue(label)
	}
	return values
}

func emoteTimelineLabelAt(labels []string, index int64) string {
	if index < 0 || index >= int64(len(labels)) {
		return ""
	}
	return labels[index]
}

func emoteContainsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func emoteVariableValue(emote *emotePlayerState, label string) float64 {
	if variable := emote.variables[label]; variable != nil {
		if variable.set {
			return variable.value
		}
		if len(variable.frames) > 0 {
			return variable.frames[0].value
		}
		return variable.rangeMin
	}
	return 0
}

func emoteVariableFrameLabel(emote *emotePlayerState, index, frame int64) string {
	if index < 0 || index >= int64(len(emote.variableOrder)) {
		return ""
	}
	variable := emote.variables[emote.variableOrder[index]]
	if variable == nil || frame < 0 || frame >= int64(len(variable.frames)) {
		return ""
	}
	return variable.frames[frame].label
}

func emoteVariableFrameValue(emote *emotePlayerState, index, frame int64) float64 {
	if index < 0 || index >= int64(len(emote.variableOrder)) {
		return 0
	}
	variable := emote.variables[emote.variableOrder[index]]
	if variable == nil || frame < 0 || frame >= int64(len(variable.frames)) {
		return 0
	}
	return variable.frames[frame].value
}

func psbLabel(dict map[string]assets.Value) string {
	for _, key := range []string{"label", "name", "id"} {
		if value, ok := dict[key].(string); ok && value != "" {
			return value
		}
	}
	return ""
}

func psbBool(value assets.Value) bool {
	switch v := value.(type) {
	case bool:
		return v
	case int64:
		return v != 0
	case float64:
		return v != 0
	}
	return false
}

func psbNumber(value assets.Value) (float64, bool) {
	switch v := value.(type) {
	case int64:
		return float64(v), true
	case float64:
		return v, true
	}
	return 0, false
}
