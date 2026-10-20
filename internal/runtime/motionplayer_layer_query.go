package runtime

import (
	"fmt"
	"math"
	"strings"

	"github.com/eslzzyl/krkrgo/internal/assets"
	"github.com/eslzzyl/krkrgo/internal/tjs"
)

func (s *Services) motionPlayerLayerGetters(vm *tjs.VM, state *motionPlayerState) ([]tjs.Value, error) {
	resource := s.playerResource(state)
	layers := motionLayerList(resource, state.chara, state.motion)
	if resource == nil || len(layers) == 0 {
		return nil, nil
	}
	var params []float64
	var attached []*motionResource
	maskMode := state.maskMode
	freezeBody := false
	if state.emote != nil {
		params = state.emote.renderParameters()
		attached = state.attached
		freezeBody = state.emote.freezeBodyMotion()
	}
	eval := motionRenderEval{
		params: params, attached: attached, maskMode: maskMode,
		freezeBody: freezeBody, emoteMode: motionResourceEmoteMode(resource), completion: state.completionType,
		rootCoordinateMode:  state.rootCoordinateMode,
		parentFrameLoopTime: state.frameLoopTime, hasParentFrameLoopTime: true,
		frameLastTime: state.frameLastTime, anyTimelinePlaying: len(state.playingTimelineLabels) > 0,
		rootState: state.rootState, hasRootState: state.hasRootState,
		anchorOutputs: motionAnchorOutputs(state.anchorStates),
	}
	root := motionRootRenderContext(eval, motionPlayerZFactor(state))
	var constraintX, constraintY, constraintZ float64
	if !eval.emoteMode {
		phase := motionCameraPhase(layers, state.clampedEvalTime, eval)
		constraintX, constraintY, constraintZ = motionCameraConstraintOffset(phase)
	}
	root.targetNodes = motionIndexTargetNodes(layers, state.clampedEvalTime, eval, root)
	result := make([]tjs.Value, 0)
	nodeIndex := 0
	var visit func(assets.Value, motionRenderContext, bool, bool, bool) error
	visit = func(value assets.Value, parent motionRenderContext, parentVisible, parentBranchVisible, isRoot bool) error {
		switch node := value.(type) {
		case []assets.Value:
			for _, child := range node {
				if err := visit(child, parent, parentVisible, parentBranchVisible, isRoot); err != nil {
					return err
				}
			}
		case map[string]assets.Value:
			currentNodeIndex := nodeIndex
			nodeIndex++
			ctx := parent
			var content map[string]assets.Value
			contentVisible := false
			nodeTick := state.clampedEvalTime
			if frames, ok := node["frameList"].([]assets.Value); ok {
				nodeTick = motionNodeTick(node, state.clampedEvalTime, eval)
				content, contentVisible = psbMotionResolvedContent(frames, nodeTick, psbMotionInt(node, "type", 0), psbMotionInt(node, "coordinate", 0))
				if contentVisible {
					ctx = motionEvalNodeContext(parent, content, node, nodeTick, state.clampedEvalTime)
					if ctx.hasAnchorColors {
						content = motionAnchorTintContent(content, motionAnchorNodeOutput{active: true, hasColors: true, colors: ctx.anchorColors})
					}
					if motionNestedMotionSource(content) {
						ctx.childTick = motionNestedTickAt(node, content, state.frameLoopTime, nodeTick)
					}
				}
			}
			visible := parentVisible && contentVisible
			branchVisible := parentBranchVisible && contentVisible
			if psbMotionInt(content, "sync", 0) != 0 {
				branchVisible = false
			}
			layerContext := ctx
			layerContext.x += constraintX
			layerContext.y += constraintY
			layerContext.z += constraintZ
			if !isRoot {
				values, err := motionLayerGetterValues(vm, resource, state.attached, node, content, layerContext, visible, branchVisible, state.completionType)
				if err != nil {
					return err
				}
				if psbMotionInt(node, "type", 0) == 4 {
					values["particle"], err = s.motionPlayerLayerParticles(vm, state, resource, currentNodeIndex, node, content, layerContext)
					if err != nil {
						return err
					}
				} else if psbMotionInt(node, "type", 0) == 3 {
					child, err := s.motionPlayerLayerChild(vm, state, resource, currentNodeIndex, node, content, layerContext, nodeTick, visible, branchVisible)
					if err != nil {
						return err
					}
					values["motion"] = child
				}
				getter, err := s.newMotionLayerGetter(vm, values)
				if err != nil {
					return err
				}
				result = append(result, getter)
			}
			if child, ok := node["children"]; ok {
				if err := visit(child, ctx, visible, branchVisible, false); err != nil {
					return err
				}
			}
		}
		return nil
	}
	rootVisible, rootActive := true, true
	if state.hasRootState {
		rootVisible, rootActive = state.rootState.visible, state.rootState.active
	}
	for _, layer := range layers {
		if err := visit(layer, root, rootVisible, rootActive, false); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (s *Services) motionPlayerLayerChild(vm *tjs.VM, state *motionPlayerState, resource *motionResource, nodeIndex int, node, content map[string]assets.Value, context motionRenderContext, selectionTick float64, visible, active bool) (tjs.Value, error) {
	if state.motionChildren == nil {
		state.motionChildren = make(map[int]tjs.Value)
	}
	child, exists := state.motionChildren[nodeIndex]
	if !exists {
		var err error
		child, err = s.motionPlayerNewChild(vm, state)
		if err != nil {
			return tjs.VoidValue, err
		}
		state.motionChildren[nodeIndex] = child
	}

	childState := s.motionPlayer(child.ObjectRef().Object)
	childState.resourceManager = state.resourceManager
	childState.randomGenerator = state.randomGenerator
	childState.project = state.project
	if childState.project.Kind() != tjs.KindObject && state.resource != nil {
		if module, found, err := s.motionModuleValueForResource(vm, state.resourceManager, state.resource); err != nil {
			return tjs.VoidValue, err
		} else if found {
			childState.project = module
		}
	}
	childState.attached = state.attached
	childRootContext := motionChildOriginContext(context, content, node, selectionTick, motionRenderEval{
		frameLastTime: state.frameLastTime, anyTimelinePlaying: len(state.playingTimelineLabels) > 0,
		anchorOutputs: motionAnchorOutputs(state.anchorStates),
	})
	childState.rootState = motionPlayerRootStateFromContext(childRootContext)
	childState.anchorStates = motionAnchorChildStates(state.anchorStates, childRootContext.anchorNamespace)
	childState.hasRootState = true
	childState.rootState.coordinateMode = psbMotionInt(node, "coordinate", 0)
	childState.rootState.visible = visible
	childState.rootState.active = active
	childState.rootCoordinateMode = childState.rootState.coordinateMode
	childState.zFactor = motionPlayerZFactor(state)
	childState.zFactorSet = true
	source, _ := content["src"].(string)
	if source != "" {
		previousChara, previousMotion := childState.chara, childState.motion
		parts := splitMotionPath(source)
		chara, motion := source, source
		switch {
		case len(parts) >= 3:
			chara, motion = parts[1], parts[2]
		case len(parts) == 2:
			chara, motion = parts[0], parts[1]
		}
		childState.chara = chara
		playFlags := motionPlayerChildPlayFlags(state, node, content)
		if previousChara != chara || previousMotion != motion || playFlags&(motionPlayFlagForce|motionPlayFlagAsCan) != 0 {
			if err := s.motionPlayerFindMotion(vm, childState, motion, playFlags); err != nil {
				return tjs.VoidValue, err
			}
		}
		owner, _, nested := motionLookupMotion(resource, state.attached, chara, motion)
		if owner != nil && nested != nil {
			childState.resource = owner
			childState.metadataResource = nil
			if module, found, err := s.motionModuleValueForResource(vm, state.resourceManager, owner); err != nil {
				return tjs.VoidValue, err
			} else if found {
				childState.project = module
			}
			childTick := motionNestedTickAt(node, content, state.frameLoopTime, selectionTick)
			childTick = motionWrapNestedTime(childTick, nested, state.frameLastTime < 0)
			totalFrames, ok := motionTotalFramesValue(nested)
			if !ok {
				totalFrames = 0
			}
			loopTime, hasLoopTime := motionLoopTimeValue(nested)
			looping := hasLoopTime && loopTime >= 0
			childState.frameLoopTime = math.Max(childTick, 0)
			childState.clampedEvalTime = math.Min(childState.frameLoopTime, math.Max(totalFrames, 0))
			childState.motionClockActive = looping || childTick < totalFrames
			childState.playing = childState.motionClockActive
			childState.held = !childState.motionClockActive && childTick >= totalFrames
		} else {
			childState.playing = false
			childState.motionClockActive = false
		}
	} else {
		childState.playing = false
		childState.motionClockActive = false
	}
	return child, nil
}

func motionPlayerChildPlayFlags(parent *motionPlayerState, node, content map[string]assets.Value) int64 {
	flags := int64(0)
	if motionValue, ok := content["motion"].(map[string]assets.Value); ok {
		mask := psbMotionInt(motionValue, "mask", 0)
		if mask&0x1 != 0 {
			flags |= int64(psbMotionInt(motionValue, "flags", 0))
		}
	}
	flags |= motionPlayerChildParameterMode(parent, node)
	return flags
}

func motionPlayerChildParameterMode(parent *motionPlayerState, node map[string]assets.Value) int64 {
	if parent == nil || parent.emote == nil {
		return 0
	}
	resource := parent.resource
	if resource == nil {
		resource = parent.metadataResource
	}
	motionValue := motionEntry(resource, parent.chara, parent.motion)
	if motionValue == nil {
		return 0
	}
	entries, _ := motionValue["parameter"].([]assets.Value)
	defaultIndex := -1
	if parameterize, ok := motionValue["parameterize"].(map[string]assets.Value); ok {
		entries = []assets.Value{parameterize}
		defaultIndex = 0
	} else if value, ok := psbNumber(motionValue["parameterize"]); ok {
		defaultIndex = int(value)
	}
	index := defaultIndex
	if value, ok := psbNumber(node["parameterize"]); ok {
		index = int(value)
	}
	if index < 0 || index >= len(entries) {
		return 0
	}
	entry, _ := entries[index].(map[string]assets.Value)
	id, _ := entry["id"].(string)
	parameterIndex, found := parent.emote.parameterById[id]
	if !found || parameterIndex < 0 || parameterIndex >= len(parent.emote.parameters) {
		return 0
	}
	return parent.emote.parameters[parameterIndex].mode
}

func (s *Services) motionPlayerNewChild(vm *tjs.VM, parent *motionPlayerState) (tjs.Value, error) {
	motionNamespace, found := vm.Globals["Motion"]
	if !found {
		return tjs.VoidValue, fmt.Errorf("Motion namespace is unavailable")
	}
	playerClass, err := vm.GetMember(motionNamespace, "Player", s, nil)
	if err != nil {
		return tjs.VoidValue, err
	}
	args := []tjs.Value(nil)
	if parent.resourceManager != 0 {
		args = []tjs.Value{tjs.ObjectIDValue(parent.resourceManager)}
	}
	child, err := vm.Construct(playerClass, args, s, nil)
	if err != nil {
		return tjs.VoidValue, err
	}
	if child.Kind() != tjs.KindObject || child.IsNull() {
		return tjs.VoidValue, fmt.Errorf("Motion.Player child construction returned no object")
	}
	return child, nil
}

func (s *Services) motionPlayerLayerParticles(vm *tjs.VM, parent *motionPlayerState, resource *motionResource, nodeIndex int, node, content map[string]assets.Value, ctx motionRenderContext) (tjs.Value, error) {
	if parent.particleArrays == nil {
		parent.particleArrays = make(map[int]tjs.Value)
	}
	array, exists := parent.particleArrays[nodeIndex]
	if !exists {
		var err error
		array, err = vm.NewArray([]tjs.Value{})
		if err != nil {
			return tjs.VoidValue, err
		}
		parent.particleArrays[nodeIndex] = array
	}
	if parent.particleChildren == nil {
		parent.particleChildren = make(map[int]map[string]tjs.Value)
	}
	children := parent.particleChildren[nodeIndex]
	if children == nil {
		children = make(map[string]tjs.Value)
	}
	particles := motionParticles(resource, parent.attached, node, content, ctx, parent.frameLoopTime, int(parent.width), int(parent.height))
	values := make([]tjs.Value, 0, len(particles))
	live := make(map[string]tjs.Value, len(particles))
	for _, particle := range particles {
		key := fmt.Sprintf("%s:%g", particle.template, particle.birth)
		child, exists := children[key]
		if !exists {
			var err error
			child, err = s.motionPlayerNewChild(vm, parent)
			if err != nil {
				return tjs.VoidValue, err
			}
		}
		particleContext := motionParticleTransform(ctx, particle)
		childState := s.motionPlayer(child.ObjectRef().Object)
		childState.resourceManager = parent.resourceManager
		childState.randomGenerator = parent.randomGenerator
		childState.attached = parent.attached
		childState.x, childState.y = 0, 0
		childState.rootState = motionPlayerRootStateFromContext(particleContext)
		childState.rootState.coordinateMode = 0
		childState.hasRootState = true
		childState.rootCoordinateMode = 0
		childState.frameLoopTime = particle.age
		childState.clampedEvalTime = particle.age
		childState.rotateAngle = particle.angle
		childState.viewZoom = particle.scale
		childState.playing, childState.motionClockActive = true, true
		childState.held = false
		path := strings.TrimPrefix(particle.template, "motion/")
		parts := splitMotionPath(path)
		if len(parts) >= 2 {
			owner, _, nested := motionLookupMotion(resource, parent.attached, parts[0], parts[1])
			if owner != nil && nested != nil {
				childState.resource = owner
				childState.chara, childState.motion = parts[0], parts[1]
				childState.metadataResource = nil
				if module, found, err := s.motionModuleValueForResource(vm, parent.resourceManager, owner); err != nil {
					return tjs.VoidValue, err
				} else if found {
					childState.project = module
				}
			}
		}
		values = append(values, child)
		live[key] = child
	}
	if err := vm.ClearArray(array); err != nil {
		return tjs.VoidValue, err
	}
	for _, child := range values {
		if err := vm.AppendArray(array, child); err != nil {
			return tjs.VoidValue, err
		}
	}
	parent.particleChildren[nodeIndex] = live
	return array, nil
}

func motionLayerGetterValues(vm *tjs.VM, resource *motionResource, attached []*motionResource, node, content map[string]assets.Value, ctx motionRenderContext, visible, branchVisible bool, completion int64) (map[string]tjs.Value, error) {
	values := map[string]tjs.Value{
		"type":          tjs.IntegerValue(int64(psbMotionInt(node, "type", 0))),
		"label":         tjs.StringValue(psbLabel(node)),
		"visible":       tjs.IntegerValue(boolInt(visible)),
		"branchVisible": tjs.IntegerValue(boolInt(branchVisible)),
		"layerVisible":  tjs.IntegerValue(boolInt(motionNodeAllowed(node, completion))),
		"x":             tjs.RealValue(ctx.x),
		"y":             tjs.RealValue(ctx.y),
		"left":          tjs.RealValue(ctx.x),
		"top":           tjs.RealValue(ctx.y),
		"flipX":         tjs.IntegerValue(boolInt(ctx.transform.flipX)),
		"flipY":         tjs.IntegerValue(boolInt(ctx.transform.flipY)),
		"zoomX":         tjs.RealValue(ctx.transform.scaleX),
		"zoomY":         tjs.RealValue(ctx.transform.scaleY),
		"angleRad":      tjs.RealValue(ctx.transform.angle),
		"angleDeg":      tjs.RealValue(ctx.transform.angle * 180 / 3.141592653589793),
		"slantX":        tjs.RealValue(ctx.transform.slantX),
		"slantY":        tjs.RealValue(ctx.transform.slantY),
		"opacity":       tjs.IntegerValue(int64(ctx.opacity)),
	}
	var originX, originY, width, height float64
	if source, _ := content["src"].(string); source != "" {
		root, _ := resource.doc.Root.(map[string]assets.Value)
		_, image, ox, oy, err := motionSourceImageAny(resource, attached, root, source)
		if err == nil && image != nil {
			originX, originY = ox, oy
			width, height = float64(image.Width), float64(image.Height)
		}
	}
	if ctx.hasAnchorOrigin {
		originX, originY = ctx.anchorOriginX, ctx.anchorOriginY
		width, height = ctx.anchorOutputs[motionAnchorKey(node)].width, ctx.anchorOutputs[motionAnchorKey(node)].height
	}
	values["originX"] = tjs.RealValue(originX)
	values["originY"] = tjs.RealValue(originY)
	matrix, err := vm.NewArray([]tjs.Value{
		tjs.RealValue(ctx.matrix.a), tjs.RealValue(ctx.matrix.b),
		tjs.RealValue(ctx.matrix.c), tjs.RealValue(ctx.matrix.d),
		tjs.RealValue(ctx.x), tjs.RealValue(ctx.y),
	})
	if err != nil {
		return nil, err
	}
	values["mtx"] = matrix
	clipOriginX := originX + psbMotionFloat(content, "ox", 0)
	clipOriginY := originY + psbMotionFloat(content, "oy", 0)
	vertexX := ctx.x - ctx.matrix.a*clipOriginX - ctx.matrix.b*clipOriginY
	vertexY := ctx.y + ctx.z*ctx.zFactor - ctx.matrix.c*clipOriginX - ctx.matrix.d*clipOriginY
	vertices := [8]float64{
		vertexX, vertexY,
		vertexX + ctx.matrix.a*width, vertexY + ctx.matrix.c*width,
		vertexX + ctx.matrix.a*width + ctx.matrix.b*height, vertexY + ctx.matrix.c*width + ctx.matrix.d*height,
		vertexX + ctx.matrix.b*height, vertexY + ctx.matrix.d*height,
	}
	vertexValues := make([]tjs.Value, len(vertices))
	for index, value := range vertices {
		vertexValues[index] = tjs.RealValue(float64(float32(value)))
	}
	values["vtx"], err = vm.NewArray(vertexValues)
	if err != nil {
		return nil, err
	}
	colors, _ := motionFrameColors(content)
	colorValues := []tjs.Value{
		tjs.IntegerValue(int64(colors[0] & 0xff)),
		tjs.IntegerValue(int64((colors[0] >> 8) & 0xff)),
		tjs.IntegerValue(int64((colors[0] >> 16) & 0xff)),
		tjs.IntegerValue(int64((colors[0] >> 24) & 0xff)),
	}
	values["color"], err = vm.NewArray(colorValues)
	if err != nil {
		return nil, err
	}
	return values, nil
}

func (s *Services) newMotionLayerGetter(vm *tjs.VM, values map[string]tjs.Value) (tjs.Value, error) {
	motion, ok := vm.Globals["Motion"]
	if !ok {
		return tjs.VoidValue, fmt.Errorf("Motion namespace is unavailable")
	}
	class, err := vm.GetMember(motion, "LayerGetter", s, nil)
	if err != nil {
		return tjs.VoidValue, err
	}
	instance, err := vm.Construct(class, nil, s, nil)
	if err != nil {
		return tjs.VoidValue, err
	}
	if instance.Kind() != tjs.KindObject || instance.IsNull() {
		return tjs.VoidValue, fmt.Errorf("Motion.LayerGetter construction returned no object")
	}
	id := instance.ObjectRef().Object
	s.mu.Lock()
	state := s.MotionValueObjects[id]
	if state == nil {
		state = newMotionValueObjectState("LayerGetter")
		s.MotionValueObjects[id] = state
	}
	for name, value := range values {
		state.values[name] = value
	}
	s.mu.Unlock()
	return instance, nil
}
