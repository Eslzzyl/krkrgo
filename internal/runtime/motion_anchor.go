package runtime

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/eslzzyl/krkrgo/internal/assets"
)

type motionAnchorNodeState struct {
	opacityScale float64
	colorScale   [16]float64
	output       motionAnchorNodeOutput
	initialized  bool
}

type motionAnchorNodeOutput struct {
	x, y, z           float64
	matrix            motionMatrix
	transform         motionTransformState
	opacity           uint8
	originX, originY  float64
	width, height     float64
	colors            [4]uint32
	hasColors, active bool
}

func motionAnchorKey(node map[string]assets.Value) string {
	return fmt.Sprintf("node:%p", node)
}

func motionAnchorContextKey(namespace string, node map[string]assets.Value) string {
	key := motionAnchorKey(node)
	if namespace == "" {
		return key
	}
	return namespace + "/" + key
}

func motionAnchorOutputs(states map[string]motionAnchorNodeState) map[string]motionAnchorNodeOutput {
	if len(states) == 0 {
		return nil
	}
	outputs := make(map[string]motionAnchorNodeOutput, len(states))
	for key, state := range states {
		if state.output.active {
			outputs[key] = state.output
		}
	}
	return outputs
}

func motionAnchorChildStates(states map[string]motionAnchorNodeState, namespace string) map[string]motionAnchorNodeState {
	if len(states) == 0 || namespace == "" {
		return nil
	}
	prefix := namespace + "/"
	childStates := make(map[string]motionAnchorNodeState)
	for key, state := range states {
		if strings.HasPrefix(key, prefix) {
			childStates[strings.TrimPrefix(key, prefix)] = state
		}
	}
	return childStates
}

func motionAnchorOutputSignature(outputs map[string]motionAnchorNodeOutput) string {
	if len(outputs) == 0 {
		return ""
	}
	keys := make([]string, 0, len(outputs))
	for key := range outputs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var signature strings.Builder
	for _, key := range keys {
		output := outputs[key]
		fmt.Fprintf(&signature, "%s:%g,%g,%g:%g,%g,%g,%g:%g,%g,%g,%g:%g,%g,%g,%g,%g:%t,%t:%d:%t:%08x,%08x,%08x,%08x;",
			key, output.x, output.y, output.z,
			output.matrix.a, output.matrix.b, output.matrix.c, output.matrix.d,
			output.transform.angle, output.transform.scaleX, output.transform.scaleY, output.transform.slantX,
			output.transform.slantY, output.originX, output.originY, output.width, output.height,
			output.transform.flipX, output.transform.flipY,
			output.opacity, output.hasColors,
			output.colors[0], output.colors[1], output.colors[2], output.colors[3])
	}
	return signature.String()
}

func motionAnchorApplyOutput(ctx motionRenderContext, output motionAnchorNodeOutput) motionRenderContext {
	if !output.active {
		return ctx
	}
	ctx.x, ctx.y, ctx.z = output.x, output.y, output.z
	ctx.matrix, ctx.transform = output.matrix, output.transform
	ctx.opacity = output.opacity
	ctx.anchorColors, ctx.hasAnchorColors = output.colors, output.hasColors
	ctx.anchorOriginX, ctx.anchorOriginY = output.originX, output.originY
	ctx.hasAnchorOrigin = true
	return ctx
}

func motionAnchorTintContent(content map[string]assets.Value, output motionAnchorNodeOutput) map[string]assets.Value {
	if !output.active || !output.hasColors {
		return content
	}
	copyContent := make(map[string]assets.Value, len(content)+1)
	for key, value := range content {
		copyContent[key] = value
	}
	colors := make(map[string]assets.Value, 4)
	for index, key := range []string{"0", "1", "2", "3"} {
		colors[key] = int64(output.colors[index])
	}
	copyContent["color"] = colors
	return copyContent
}

func motionAnchorDimensions(resource *motionResource, attached []*motionResource, content map[string]assets.Value) (float64, float64) {
	width, height := psbMotionFloat(content, "width", 0), psbMotionFloat(content, "height", 0)
	if width > 0 && height > 0 {
		return width, height
	}
	source, _ := content["src"].(string)
	parts := strings.Split(strings.TrimPrefix(source, "src/"), "/")
	if len(parts) == 2 {
		resources := make([]*motionResource, 0, len(attached)+1)
		resources = append(resources, resource)
		resources = append(resources, attached...)
		for _, candidate := range resources {
			if candidate == nil || candidate.doc == nil {
				continue
			}
			root, _ := candidate.doc.Root.(map[string]assets.Value)
			metadata, err := motionSourceMetadata(root, parts[0], parts[1])
			if err == nil {
				if width <= 0 {
					width = psbMotionFloat(metadata, "width", width)
				}
				if height <= 0 {
					height = psbMotionFloat(metadata, "height", height)
				}
				if width > 0 && height > 0 {
					return width, height
				}
			}
		}
	}
	if width <= 0 {
		width = 32
	}
	if height <= 0 {
		height = 32
	}
	return width, height
}

func motionAnchorInitialState() motionAnchorNodeState {
	state := motionAnchorNodeState{opacityScale: 1, initialized: true}
	for index := range state.colorScale {
		state.colorScale[index] = 1
	}
	return state
}

func motionAnchorUpdateNode(state motionAnchorNodeState, node, content map[string]assets.Value, ctx motionRenderContext, frameLastTime float64, resource *motionResource, attached []*motionResource, independentLayerInherit bool) motionAnchorNodeState {
	if !state.initialized {
		state = motionAnchorInitialState()
	}
	dampPow := math.Abs(frameLastTime) / 60
	if dampPow == 0 {
		state.output.active = false
		return state
	}
	width, height := motionAnchorDimensions(resource, attached, content)
	transform := ctx.transform
	if transform.angle >= 180 {
		transform.angle = 360 - (360-transform.angle)*dampPow
	} else {
		transform.angle *= dampPow
	}
	transform.scaleX = math.Pow(transform.scaleX*32/width, dampPow)
	transform.scaleY = math.Pow(transform.scaleY*32/height, dampPow)
	transform.slantX *= dampPow
	transform.slantY *= dampPow
	matrix := psbMotionLocalMatrixStateOrder(node, transform, motionDefaultTransformOrder)
	if !independentLayerInherit {
		matrix = multiplyMotionMatrix(ctx.rootMatrix, matrix)
	}
	opacityFloat := float64(ctx.opacity)
	if opacityFloat == 0 {
		opacityFloat = 1
	}
	newOpacity := math.Pow(opacityFloat/255, dampPow) * 255 * state.opacityScale
	newOpacity = math.Max(0, math.Min(255, newOpacity))
	opacity := int(newOpacity)
	if newOpacity != 0 {
		state.opacityScale = 1
	}
	colors, _ := motionFrameColors(content)
	allEqual := colors[0] == colors[1] && colors[1] == colors[2] && colors[2] == colors[3]
	defaultGray := allEqual && colors[0] == 0xFF808080
	hasColors := !defaultGray
	if hasColors {
		iterations := 4
		if allEqual {
			iterations = 1
		}
		for colorIndex := 0; colorIndex < iterations; colorIndex++ {
			packed := colors[colorIndex]
			var channels [4]byte
			for channel := range channels {
				channels[channel] = byte(packed >> (8 * channel))
			}
			for channel := 0; channel < 3; channel++ {
				value := float64(channels[channel])
				if value == 0 {
					value = 1
				}
				result := 255 * math.Pow(value/255, dampPow) * state.colorScale[colorIndex*4+channel]
				result = math.Max(0, math.Min(255, result))
				integer := int(result)
				if integer != 0 {
					state.colorScale[colorIndex*4+channel] = result / float64(integer)
				}
				channels[channel] = byte(integer)
			}
			alpha := float64(channels[3]) / 255
			if alpha == 0 {
				alpha = 1.0 / 255
			}
			result := 255 * math.Pow(alpha, dampPow) * state.colorScale[colorIndex*4+3]
			result = math.Max(0, math.Min(255, result))
			integer := int(result)
			if integer != 0 {
				state.colorScale[colorIndex*4+3] = result / float64(integer)
			}
			channels[3] = byte(integer)
			colors[colorIndex] = uint32(channels[0]) | uint32(channels[1])<<8 | uint32(channels[2])<<16 | uint32(channels[3])<<24
		}
		if allEqual {
			colors[1], colors[2], colors[3] = colors[0], colors[0], colors[0]
		}
	}
	state.output = motionAnchorNodeOutput{
		x:      ctx.rootX + dampPow*(ctx.x-ctx.rootX),
		y:      ctx.rootY + dampPow*(ctx.y-ctx.rootY),
		z:      ctx.rootZ + dampPow*(ctx.z-ctx.rootZ),
		matrix: matrix, transform: transform, opacity: uint8(opacity),
		originX: width * 0.5, originY: height * 0.5, width: width, height: height,
		colors: colors, hasColors: hasColors, active: true,
	}
	return state
}

func (s *Services) motionPlayerUpdateAnchorStates(state *motionPlayerState) {
	if state == nil {
		return
	}
	resource := s.playerResource(state)
	layers := motionLayerList(resource, state.chara, state.motion)
	if resource == nil || len(layers) == 0 {
		return
	}
	if motionResourceEmoteMode(resource) || (state.emote != nil && state.emote.freezeBodyMotion()) {
		state.anchorStates = nil
		return
	}
	if state.anchorStates == nil {
		state.anchorStates = make(map[string]motionAnchorNodeState)
	}
	var params []float64
	attached := state.attached
	freezeBody := false
	if state.emote != nil {
		params = state.emote.renderParameters()
		freezeBody = state.emote.freezeBodyMotion()
	}
	eval := motionRenderEval{
		params: params, attached: attached, freezeBody: freezeBody,
		completion: state.completionType, rootCoordinateMode: state.rootCoordinateMode,
		parentFrameLoopTime: state.frameLoopTime, hasParentFrameLoopTime: true,
		frameLastTime: state.frameLastTime, anyTimelinePlaying: len(state.playingTimelineLabels) > 0,
		rootState: state.rootState, hasRootState: state.hasRootState,
	}
	root := motionRootRenderContext(eval, motionPlayerZFactor(state))
	root.anchorOutputs = nil
	stack := make(map[string]bool)
	seen := make(map[string]struct{})
	var visit func(*motionResource, map[string]assets.Value, assets.Value, float64, motionRenderEval, motionRenderContext)
	visit = func(currentResource *motionResource, currentRoot map[string]assets.Value, value assets.Value, tick float64, currentEval motionRenderEval, parent motionRenderContext) {
		switch node := value.(type) {
		case []assets.Value:
			for _, child := range node {
				visit(currentResource, currentRoot, child, tick, currentEval, parent)
			}
		case map[string]assets.Value:
			ctx := parent
			var content map[string]assets.Value
			if frames, ok := node["frameList"].([]assets.Value); ok {
				nodeTick := motionNodeTick(node, tick, currentEval)
				var visible bool
				content, visible = psbMotionResolvedContent(frames, nodeTick, psbMotionInt(node, "type", 0), psbMotionInt(node, "coordinate", 0))
				if !visible {
					return
				}
				ctx = motionEvalNodeContext(parent, content, node, nodeTick, tick)
				if psbMotionInt(node, "type", -1) == 10 {
					key := motionAnchorContextKey(parent.anchorNamespace, node)
					seen[key] = struct{}{}
					anchor := motionAnchorUpdateNode(state.anchorStates[key], node, content, ctx, currentEval.frameLastTime, currentResource, currentEval.attached, state.independentLayerInherit)
					state.anchorStates[key] = anchor
					ctx = motionAnchorApplyOutput(ctx, anchor.output)
				}
			}
			if motionNestedMotionSource(content) {
				parts := splitMotionPath(strings.TrimPrefix(psbMotionString(content, "src"), "motion/"))
				if len(parts) >= 2 {
					chara, motion := parts[0], parts[1]
					owner, ownerRoot, nested := motionLookupMotion(currentResource, currentEval.attached, chara, motion)
					if owner != nil && nested != nil {
						treeKey := fmt.Sprintf("%p/%s/%s", owner, chara, motion)
						if !stack[treeKey] {
							if nestedLayers := motionMotionLayerList(owner, nested); len(nestedLayers) > 0 {
								stack[treeKey] = true
								childCtx := motionChildOriginContext(ctx, content, node, tick, currentEval)
								childTick, childEval := motionNestedChildEval(currentEval, nested, childCtx.childTick, childCtx.coordinateMode)
								childEval.anchorOutputs = nil
								visit(owner, ownerRoot, nestedLayers, childTick, childEval, childCtx)
								delete(stack, treeKey)
							}
						}
					}
				}
			}
			if children, ok := node["children"]; ok {
				visit(currentResource, currentRoot, children, tick, currentEval, ctx)
			}
		}
	}
	visit(resource, func() map[string]assets.Value {
		root, _ := resource.doc.Root.(map[string]assets.Value)
		return root
	}(), layers, state.clampedEvalTime, eval, root)
	for key, anchor := range state.anchorStates {
		if _, exists := seen[key]; !exists {
			anchor.output.active = false
			state.anchorStates[key] = anchor
		}
	}
}
