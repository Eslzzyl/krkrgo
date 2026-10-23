package runtime

import (
	"fmt"
	"math"
	goruntime "runtime"
	"sort"
	"strings"
	"sync"

	"github.com/eslzzyl/krkrgo/internal/assets"
)

type motionResource struct {
	path                          string
	doc                           *assets.Document
	moduleDoc                     *assets.Document
	data                          []byte
	images                        map[string]*assets.Image
	premultiplied                 map[string][]byte
	colored                       map[string]*assets.Image
	opaque                        map[string]bool
	stack                         map[string]bool
	canvas                        *assets.Image
	canvasChara                   string
	canvasMotion                  string
	canvasTick                    float64
	canvasFilter                  int64
	canvasVariables               string
	canvasMask                    int64
	canvasCompletion              int64
	canvasFreeze                  bool
	canvasAttached                string
	canvasZFactor                 float64
	canvasIndependentLayerInherit bool
	canvasRootCoordinateMode      int
	canvasParentFrameLoopTime     float64
	canvasHasParentFrameLoopTime  bool
	canvasFrameLastTime           float64
	canvasAnyTimelinePlaying      bool
	canvasRootState               motionPlayerRootState
	canvasHasRootState            bool
	canvasPreview                 bool
	canvasValid                   bool
}

func motionClearSourceCaches(resource *motionResource) {
	if resource == nil {
		return
	}
	clear(resource.images)
	clear(resource.premultiplied)
	clear(resource.colored)
	clear(resource.opaque)
}

func (l *Layer) materializeGPUMotion() error {
	if l == nil || l.gpuMotion == nil {
		return nil
	}
	if l.ImageWidth <= 0 || l.ImageHeight <= 0 {
		return fmt.Errorf("%w: deferred motion has invalid layer size", ErrInvalidLayer)
	}
	want := int(l.deviceWidth()) * int(l.deviceHeight()) * 4
	if len(l.RGBA) != want {
		l.RGBA = make([]byte, want)
	}
	clear(l.RGBA)
	motion := l.gpuMotion
	for _, draw := range motion.Draws {
		if draw.Width <= 0 || draw.Height <= 0 ||
			uint64(draw.Width)*uint64(draw.Height)*4 != uint64(len(draw.RGBA)) {
			return fmt.Errorf("%w: deferred motion source has invalid size", ErrInvalidLayer)
		}
		ctx := motionRenderContext{
			x:         draw.TX - float64(l.deviceWidth())/2,
			y:         draw.TY - float64(l.deviceHeight())/2,
			opacity:   draw.Opacity,
			blendMode: int64(draw.Blend),
			matrix:    motionMatrix{a: draw.A, b: draw.B, c: draw.C, d: draw.D},
		}
		source := &assets.Image{Width: uint32(draw.Width), Height: uint32(draw.Height), RGBA: draw.RGBA}
		if err := blendMotionImage(l.RGBA, int(l.deviceWidth()), int(l.deviceHeight()), source, 0, 0, motion.Filter, nil, false, ctx); err != nil {
			return err
		}
	}
	l.gpuMotion = nil
	l.imageModified = true
	l.markImageDirty()
	return nil
}

type motionMatrix struct {
	a, b, c, d float64
}

var identityMotionMatrix = motionMatrix{a: 1, d: 1}

type motionTransformState struct {
	flipX, flipY bool
	angle        float64
	scaleX       float64
	scaleY       float64
	slantX       float64
	slantY       float64
}

var identityMotionTransform = motionTransformState{scaleX: 1, scaleY: 1}

type motionViewport struct {
	minX, minY float64
	maxX, maxY float64
}

type motionRenderContext struct {
	x, y                float64
	rootX, rootY, rootZ float64
	opacity             uint8
	blendMode           int64
	// layerID follows NodeTree's two-ID allocation per node. The first ID is
	// used for a node's primary render layer.
	layerID int
	// layerTreeKey identifies the persistent player tree owning layerID.
	layerTreeKey string
	// childTick is the elapsed time of a nested child motion resolved from the
	// parent loop time (PlayerUpdateChildMotion.cpp:170-205).
	childTick float64
	// z accumulates the frame coordinate's third component; the reference sorts
	// prepared items by it (PlayerRenderItems.cpp:980-989).
	z float64
	// coordinateMode selects the reference's 3D placement for this node's
	// children: a parent in mode 1 mixes child x into z instead of y
	// (PlayerUpdateLayerEval.cpp:905-925).
	coordinateMode          int
	matrix                  motionMatrix
	rootMatrix              motionMatrix
	transform               motionTransformState
	rootTransform           motionTransformState
	rootOpacity             uint8
	independentLayerInherit bool
	inheritMask             int
	inheritParent           *motionRenderContext
	evalNode                map[string]assets.Value
	evalTick                float64
	evalPlayerTick          float64
	viewport                motionViewport
	hasViewport             bool
	// meshAncestors carries the control nets of every visible mesh ancestor
	// whose meshSyncChildMask combines with children, nearest ancestor last.
	// Descendant mesh points are evaluated through this chain
	// (PlayerUpdateGeometry.cpp:448-531).
	meshAncestors                []motionMeshControlNet
	targetNodes                  map[string]motionTargetPosition
	anchorOutputs                map[string]motionAnchorNodeOutput
	anchorNamespace              string
	anchorColors                 [4]uint32
	hasAnchorColors              bool
	anchorOriginX, anchorOriginY float64
	hasAnchorOrigin              bool
	// meshChildDeform is the current node's own mesh mapping for its direct
	// children. Earlier ancestors already contribute to this node's position.
	meshChildDeform *motionMeshControlNet
	zFactor         float64
	hasZFactor      bool
}

type motionTargetPosition struct {
	x, y, z float64
}

func renderPSBMotion(resource *motionResource, chara, motion string, tick float64, filter int64) (*assets.Image, error) {
	return renderPSBMotionVariables(resource, chara, motion, tick, filter, nil)
}

// motionRenderEval carries the per-draw state the reference layer evaluator
// reads: the parameter table (normalized variable values consumed by
// parameterize nodes), the emote-like body freeze, the player maskMode, the
// completionType visibility bitmask, and the MultiCache attached snapshots
// used for source/nested-motion lookup.
type motionRenderEval struct {
	filter     int64
	params     []float64
	freezeBody bool
	emoteMode  bool
	maskMode   int64
	completion int64
	attached   []*motionResource
	// inParticle suppresses emitter recursion while a particle template is
	// being drawn.
	inParticle              bool
	zFactor                 float64
	hasZFactor              bool
	independentLayerInherit bool
	rootCoordinateMode      int
	parentFrameLoopTime     float64
	hasParentFrameLoopTime  bool
	frameLastTime           float64
	anyTimelinePlaying      bool
	rootState               motionPlayerRootState
	hasRootState            bool
	anchorOutputs           map[string]motionAnchorNodeOutput
	// preview mirrors Player._preview: item preparation keeps only nodes whose
	// priorDraw flag is set and never expands child players or particles
	// (PlayerRenderItems.cpp:507-511,558-560).
	preview bool
	// stats collects renderer work for Player.processedMeshVerticesNum.
	stats *motionRenderStats
}

// motionRenderStats accumulates per-render counters the player exposes as
// properties. The reference keeps them on the player and increments them while
// updating geometry (PlayerUpdateGeometry.cpp:518-523).
type motionRenderStats struct {
	meshVertices int
}

type motionRenderOptions struct {
	rootCoordinateMode     int
	emoteMode              bool
	parentFrameLoopTime    float64
	hasParentFrameLoopTime bool
	frameLastTime          float64
	anyTimelinePlaying     bool
	rootState              motionPlayerRootState
	hasRootState           bool
	anchorOutputs          map[string]motionAnchorNodeOutput
	preview                bool
	stats                  *motionRenderStats
}

func motionRenderOptionsValue(options []motionRenderOptions) motionRenderOptions {
	if len(options) == 0 {
		return motionRenderOptions{}
	}
	return options[0]
}

func motionPlayerRootStateFromContext(ctx motionRenderContext) motionPlayerRootState {
	return motionPlayerRootState{
		x: ctx.x, y: ctx.y, z: ctx.z,
		matrix: ctx.matrix, rootMatrix: ctx.rootMatrix,
		transform: ctx.transform, rootTransform: ctx.rootTransform,
		opacity: ctx.opacity, rootOpacity: ctx.rootOpacity,
		blendMode: ctx.blendMode, coordinateMode: ctx.coordinateMode,
		active: true, visible: true,
		zFactor: ctx.zFactor, hasZFactor: ctx.hasZFactor,
	}
}

func motionRootRenderContext(eval motionRenderEval, zFactor float64) motionRenderContext {
	ctx := motionRenderContext{
		opacity: 255, blendMode: 16,
		matrix: identityMotionMatrix, rootMatrix: identityMotionMatrix,
		transform: identityMotionTransform, rootTransform: identityMotionTransform,
		rootOpacity: 255,
	}
	if eval.hasRootState {
		root := eval.rootState
		ctx.x, ctx.y, ctx.z = root.x, root.y, root.z
		ctx.rootX, ctx.rootY, ctx.rootZ = root.x, root.y, root.z
		ctx.matrix, ctx.rootMatrix = root.matrix, root.rootMatrix
		ctx.transform, ctx.rootTransform = root.transform, root.rootTransform
		ctx.opacity, ctx.rootOpacity = root.opacity, root.rootOpacity
		ctx.blendMode, ctx.coordinateMode = root.blendMode, root.coordinateMode
	}
	ctx.independentLayerInherit = eval.independentLayerInherit
	ctx.zFactor, ctx.hasZFactor = zFactor, true
	ctx.anchorOutputs = eval.anchorOutputs
	return ctx
}

func motionNestedEvalTick(node, content map[string]assets.Value, tick float64, eval motionRenderEval) float64 {
	clockTick := tick
	if eval.hasParentFrameLoopTime {
		clockTick = eval.parentFrameLoopTime
	}
	return motionNestedTickAt(node, content, clockTick, tick)
}

func motionNestedChildEval(eval motionRenderEval, nested map[string]assets.Value, frameLoopTime float64, rootCoordinateMode int) (float64, motionRenderEval) {
	frameLoopTime = motionWrapNestedTime(frameLoopTime, nested, eval.frameLastTime < 0)
	frameLoopTime = math.Max(frameLoopTime, 0)
	totalFrames, ok := motionTotalFramesValue(nested)
	if !ok {
		totalFrames = 0
	}
	totalFrames = math.Max(totalFrames, 0)
	childEval := eval
	childEval.rootCoordinateMode = rootCoordinateMode
	childEval.parentFrameLoopTime = frameLoopTime
	childEval.hasParentFrameLoopTime = true
	return math.Min(frameLoopTime, totalFrames), childEval
}

func motionWrapNestedTime(frameLoopTime float64, nested map[string]assets.Value, backwards bool) float64 {
	if !backwards {
		return frameLoopTime
	}
	totalFrames, ok := motionTotalFramesValue(nested)
	if !ok || totalFrames <= 0 {
		return frameLoopTime
	}
	loopEnd, ok := motionLoopTimeValue(nested)
	if !ok || loopEnd < 0 || frameLoopTime < totalFrames {
		return frameLoopTime
	}
	if loopEnd >= totalFrames {
		return totalFrames
	}
	for guard := 0; frameLoopTime >= totalFrames && guard < 1024; guard++ {
		frameLoopTime = frameLoopTime - totalFrames + loopEnd
	}
	if frameLoopTime >= totalFrames {
		return totalFrames
	}
	return frameLoopTime
}

func motionTotalFramesValue(motion map[string]assets.Value) (float64, bool) {
	for _, key := range []string{"lastTime", "frameCount", "frame_count", "totalFrameCount", "total_frame_count", "frames", "length", "end"} {
		if value, exists := motion[key]; exists {
			if number, ok := psbNumber(value); ok {
				return number, true
			}
		}
	}
	return 0, false
}

func motionLoopTimeValue(motion map[string]assets.Value) (float64, bool) {
	if value, exists := motion["loopTime"]; exists {
		if number, ok := psbNumber(value); ok {
			return number, true
		}
	}
	for _, key := range []string{"loop", "repeat", "is_loop"} {
		if value, exists := motion[key]; exists {
			if loop, ok := value.(bool); ok {
				if loop {
					return 0, true
				}
				return -1, true
			}
		}
	}
	return 0, false
}

// renderPSBMotionVariables renders a motion with parameterized layers resolved
// from the normalized parameter table. Nodes whose PSB metadata carries a
// numeric "parameterize" index select their frame at that parameter entry's
// value instead of the motion clock, matching frameSelectionTimeLike_0x6B7E44
// (PlayerUpdateLayerEval.cpp:313-326). An emote-like motion also freezes every
// non-parameterized node at time 0 (PlayerUpdateLayerEval.cpp:730-738).
func renderPSBMotionVariables(resource *motionResource, chara, motion string, tick float64, filter int64, params []float64) (*assets.Image, error) {
	return renderPSBMotionOptions(resource, chara, motion, tick, filter, params, len(params) > 0, 0, 0, nil)
}

func motionAttachmentsSignature(attached []*motionResource) string {
	if len(attached) == 0 {
		return ""
	}
	var builder strings.Builder
	for _, resource := range attached {
		if resource != nil {
			fmt.Fprintf(&builder, "%p:", resource)
			builder.WriteString(resource.path)
		}
		builder.WriteByte(0)
	}
	return builder.String()
}

func renderPSBMotionOptions(resource *motionResource, chara, motion string, tick float64, filter int64, params []float64, freezeBody bool, maskMode int64, completion int64, attached []*motionResource) (*assets.Image, error) {
	return renderPSBMotionOptionsZFactor(resource, chara, motion, tick, filter, params, freezeBody, maskMode, completion, 1, attached)
}

func renderPSBMotionOptionsZFactor(resource *motionResource, chara, motion string, tick float64, filter int64, params []float64, freezeBody bool, maskMode int64, completion int64, zFactor float64, attached []*motionResource) (*assets.Image, error) {
	return renderPSBMotionOptionsZFactorInherit(resource, chara, motion, tick, filter, params, freezeBody, maskMode, completion, zFactor, attached, false)
}

func renderPSBMotionOptionsZFactorInherit(resource *motionResource, chara, motion string, tick float64, filter int64, params []float64, freezeBody bool, maskMode int64, completion int64, zFactor float64, attached []*motionResource, independentLayerInherit bool, options ...motionRenderOptions) (*assets.Image, error) {
	return renderPSBMotionOptionsZFactorInheritDimensions(resource, chara, motion, tick, filter, params, freezeBody, maskMode, completion, zFactor, attached, independentLayerInherit, 0, 0, options...)
}

func renderPSBMotionOptionsZFactorInheritDimensions(resource *motionResource, chara, motion string, tick float64, filter int64, params []float64, freezeBody bool, maskMode int64, completion int64, zFactor float64, attached []*motionResource, independentLayerInherit bool, canvasWidth, canvasHeight int, options ...motionRenderOptions) (*assets.Image, error) {
	if resource == nil || resource.doc == nil {
		return nil, fmt.Errorf("motion resource is unavailable")
	}
	root, ok := resource.doc.Root.(map[string]assets.Value)
	if !ok {
		return nil, fmt.Errorf("motion PSB root is not a dictionary")
	}
	width := psbMotionInt(root["screenSize"], "width", 640)
	height := psbMotionInt(root["screenSize"], "height", 480)
	if canvasWidth > 0 {
		width = canvasWidth
	}
	if canvasHeight > 0 {
		height = canvasHeight
	}
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("motion PSB has invalid canvas size %dx%d", width, height)
	}
	paramsKey := motionVariablesSignature(params)
	attachedKey := motionAttachmentsSignature(attached)
	renderOptions := motionRenderOptionsValue(options)
	anchorOutputs := motionAnchorOutputSignature(renderOptions.anchorOutputs)
	if anchorOutputs != "" {
		paramsKey += "\x00anchor:" + anchorOutputs
	}
	rootCoordinate := renderOptions.rootCoordinateMode
	if resource.canvasValid && resource.canvas != nil &&
		resource.canvas.Width == uint32(width) && resource.canvas.Height == uint32(height) &&
		resource.canvasChara == chara && resource.canvasMotion == motion &&
		resource.canvasTick == tick && resource.canvasFilter == filter &&
		resource.canvasZFactor == zFactor &&
		resource.canvasIndependentLayerInherit == independentLayerInherit &&
		resource.canvasRootCoordinateMode == rootCoordinate &&
		resource.canvasParentFrameLoopTime == renderOptions.parentFrameLoopTime &&
		resource.canvasHasParentFrameLoopTime == renderOptions.hasParentFrameLoopTime &&
		resource.canvasFrameLastTime == renderOptions.frameLastTime &&
		resource.canvasAnyTimelinePlaying == renderOptions.anyTimelinePlaying &&
		resource.canvasRootState == renderOptions.rootState &&
		resource.canvasHasRootState == renderOptions.hasRootState &&
		resource.canvasPreview == renderOptions.preview &&
		resource.canvasVariables == paramsKey && resource.canvasMask == maskMode &&
		resource.canvasCompletion == completion &&
		resource.canvasFreeze == freezeBody &&
		resource.canvasAttached == attachedKey {
		return resource.canvas, nil
	}
	var canvas *assets.Image
	if resource.canvas == nil || resource.canvas.Width != uint32(width) || resource.canvas.Height != uint32(height) {
		canvas = &assets.Image{Width: uint32(width), Height: uint32(height), RGBA: make([]byte, width*height*4)}
		resource.canvas = canvas
		resource.canvasValid = false
	} else {
		canvas = resource.canvas
		clear(canvas.RGBA)
	}
	pixels := canvas.RGBA
	objects, _ := root["object"].(map[string]assets.Value)
	character, _ := objects[chara].(map[string]assets.Value)
	motions, _ := character["motion"].(map[string]assets.Value)
	motionValue, _ := motions[motion].(map[string]assets.Value)
	layers := motionMotionLayerList(resource, motionValue)
	if len(layers) == 0 {
		resource.canvasChara, resource.canvasMotion = chara, motion
		resource.canvasTick, resource.canvasFilter = tick, filter
		resource.canvasVariables, resource.canvasMask = paramsKey, maskMode
		resource.canvasCompletion = completion
		resource.canvasFreeze = freezeBody
		resource.canvasAttached = attachedKey
		resource.canvasZFactor = zFactor
		resource.canvasIndependentLayerInherit = independentLayerInherit
		resource.canvasRootCoordinateMode = rootCoordinate
		resource.canvasParentFrameLoopTime = renderOptions.parentFrameLoopTime
		resource.canvasHasParentFrameLoopTime = renderOptions.hasParentFrameLoopTime
		resource.canvasFrameLastTime = renderOptions.frameLastTime
		resource.canvasAnyTimelinePlaying = renderOptions.anyTimelinePlaying
		resource.canvasRootState = renderOptions.rootState
		resource.canvasHasRootState = renderOptions.hasRootState
		resource.canvasPreview = renderOptions.preview
		resource.canvasValid = true
		return canvas, nil
	}
	if resource.stack == nil {
		resource.stack = make(map[string]bool)
	} else {
		clear(resource.stack)
	}
	eval := motionRenderEval{
		filter: filter, params: params, freezeBody: freezeBody,
		maskMode: maskMode, completion: completion, attached: attached,
		zFactor: zFactor, hasZFactor: true, independentLayerInherit: independentLayerInherit,
		rootCoordinateMode:     rootCoordinate,
		parentFrameLoopTime:    renderOptions.parentFrameLoopTime,
		hasParentFrameLoopTime: renderOptions.hasParentFrameLoopTime,
		frameLastTime:          renderOptions.frameLastTime,
		anyTimelinePlaying:     renderOptions.anyTimelinePlaying,
		rootState:              renderOptions.rootState,
		hasRootState:           renderOptions.hasRootState,
		anchorOutputs:          renderOptions.anchorOutputs,
		preview:                renderOptions.preview,
		stats:                  renderOptions.stats,
	}
	ctx := motionRootRenderContext(eval, zFactor)
	if err := renderMotionValue(resource, root, layers, tick, eval, ctx, pixels, width, height, resource.stack); err != nil {
		return nil, err
	}
	resource.canvasChara, resource.canvasMotion = chara, motion
	resource.canvasTick, resource.canvasFilter = tick, filter
	resource.canvasVariables, resource.canvasMask = paramsKey, maskMode
	resource.canvasFreeze = freezeBody
	resource.canvasCompletion = completion
	resource.canvasAttached = attachedKey
	resource.canvasZFactor = zFactor
	resource.canvasIndependentLayerInherit = independentLayerInherit
	resource.canvasRootCoordinateMode = rootCoordinate
	resource.canvasParentFrameLoopTime = renderOptions.parentFrameLoopTime
	resource.canvasHasParentFrameLoopTime = renderOptions.hasParentFrameLoopTime
	resource.canvasFrameLastTime = renderOptions.frameLastTime
	resource.canvasAnyTimelinePlaying = renderOptions.anyTimelinePlaying
	resource.canvasRootState = renderOptions.rootState
	resource.canvasHasRootState = renderOptions.hasRootState
	resource.canvasPreview = renderOptions.preview
	resource.canvasValid = true
	return canvas, nil
}

// motionDrawList is the CPU counterpart of the reference's prepared render
// item list. Every draw is collected first and stable-sorted by the node's
// accumulated z (PlayerRenderItems.cpp:594-606, 983-988); equal keys keep
// collection order, matching std::stable_sort.
type motionDrawList struct {
	items []motionDrawItem
}

// motionDrawItem is one deferred CPU draw. The closure receives the target
// buffer at execution time so group buffers stay private to their group.
type motionDrawItem struct {
	z    float64
	draw func(dst []byte) error
}

func (list *motionDrawList) add(z float64, draw func(dst []byte) error) {
	list.items = append(list.items, motionDrawItem{z: z, draw: draw})
}

// draw rasterizes the collected items in sort-key order.
func (list *motionDrawList) draw(pixels []byte) error {
	if len(list.items) == 0 {
		return nil
	}
	sort.SliceStable(list.items, func(i, j int) bool { return list.items[i].z < list.items[j].z })
	for _, item := range list.items {
		if err := item.draw(pixels); err != nil {
			return err
		}
	}
	return nil
}

// renderMotionValue renders one player frame into pixels.
func renderMotionValue(resource *motionResource, root map[string]assets.Value, value assets.Value, tick float64, eval motionRenderEval, parent motionRenderContext, pixels []byte, width, height int, stack map[string]bool) error {
	var list motionDrawList
	if err := collectMotionValue(resource, root, value, tick, eval, parent, &list, width, height, stack); err != nil {
		return err
	}
	return list.draw(pixels)
}

func collectMotionValue(resource *motionResource, root map[string]assets.Value, value assets.Value, tick float64, eval motionRenderEval, parent motionRenderContext, list *motionDrawList, width, height int, stack map[string]bool) error {
	if parent.targetNodes == nil {
		parent.targetNodes = motionIndexTargetNodes(value, tick, eval, parent)
	}
	switch node := value.(type) {
	case []assets.Value:
		for _, child := range node {
			if err := collectMotionValue(resource, root, child, tick, eval, parent, list, width, height, stack); err != nil {
				return err
			}
		}
		return nil
	case map[string]assets.Value:
		// The reference evaluates the node type mask while deciding whether a
		// node contributes its own draw item; child nodes are separate items in
		// the flattened node list (PlayerRenderItems.cpp:430-533), so a masked
		// container keeps its children. Gate only this node's frame here.
		drawFrame := motionNodeAllowed(node, eval.completion, eval.preview)
		ctx := parent
		if frameList, ok := node["frameList"].([]assets.Value); ok {
			nodeTick := motionNodeTick(node, tick, eval)
			content, visible := psbMotionResolvedContent(frameList, nodeTick, psbMotionInt(node, "type", 0), psbMotionInt(node, "coordinate", 0))
			if !visible {
				return nil
			}
			ctx = motionEvalNodeContext(parent, content, node, nodeTick, tick)
			if ctx.hasAnchorColors {
				content = motionAnchorTintContent(content, motionAnchorNodeOutput{active: true, hasColors: true, colors: ctx.anchorColors})
			}
			if motionNestedMotionSource(content) {
				ctx.childTick = motionNestedEvalTick(node, content, nodeTick, eval)
			}
			// Preview preparation keeps only priorDraw items and skips
			// child-player and particle expansion (PlayerRenderItems.cpp:507-511,558-560).
			if eval.preview && !motionNodePriorDraw(node) {
				drawFrame = false
			}
			if drawFrame || (!eval.preview && (motionNestedMotionSource(content) || motionParticleSource(content))) {
				if err := collectMotionFrame(resource, root, content, node, eval, ctx, nodeTick, list, width, height, stack); err != nil {
					return err
				}
				ctx = motionMeshAncestorContext(resource, root, node, content, eval, ctx, width, height)
			}
		}
		if maskGroup := motionStencilGroup(node); maskGroup {
			// A type-12 node with stencil bit 4 composites its subtree into a
			// private buffer and applies its stencil children's alpha before
			// the group reaches the parent (PlayerRenderExecute.cpp:760-870).
			var inner motionDrawList
			if err := collectMotionChildren(resource, root, node, tick, eval, ctx, &inner, width, height, stack, true); err != nil {
				return err
			}
			groupCtx := ctx
			list.add(ctx.z, func(dst []byte) error {
				group := make([]byte, len(dst))
				if err := inner.draw(group); err != nil {
					return err
				}
				if err := applyMotionMaskChildren(resource, root, node, tick, eval, groupCtx, group, width, height, stack); err != nil {
					return err
				}
				blendMotionBuffer(dst, group)
				return nil
			})
			return nil
		}
		return collectMotionChildren(resource, root, node, tick, eval, ctx, list, width, height, stack, false)
	}
	return nil
}

func collectMotionChildren(resource *motionResource, root map[string]assets.Value, node map[string]assets.Value, tick float64, eval motionRenderEval, ctx motionRenderContext, list *motionDrawList, width, height int, stack map[string]bool, skipMaskChildren bool) error {
	if children, ok := node["children"]; ok {
		if err := collectMotionValueFiltered(resource, root, children, tick, eval, ctx, list, width, height, stack, skipMaskChildren); err != nil {
			return err
		}
	}
	return nil
}

func collectMotionValueFiltered(resource *motionResource, root map[string]assets.Value, value assets.Value, tick float64, eval motionRenderEval, parent motionRenderContext, list *motionDrawList, width, height int, stack map[string]bool, skipMaskChildren bool) error {
	if !skipMaskChildren {
		return collectMotionValue(resource, root, value, tick, eval, parent, list, width, height, stack)
	}
	children, ok := value.([]assets.Value)
	if !ok {
		return collectMotionValue(resource, root, value, tick, eval, parent, list, width, height, stack)
	}
	for _, child := range children {
		if node, ok := child.(map[string]assets.Value); ok && motionStencilMask(node) {
			continue
		}
		if err := collectMotionValue(resource, root, child, tick, eval, parent, list, width, height, stack); err != nil {
			return err
		}
	}
	return nil
}

// motionNodeAllowed mirrors the reference completionType gate
// (PlayerRenderItems.cpp:129-132): 0x1441 allows node types
// 0/6/10/12 and a non-zero completionType additionally allows type 3.
//
// The mask selects whether a node contributes its own draw item. It says
// nothing about the node's children, which the reference keeps in its
// flattened node list, so callers walk children unconditionally.
func motionNodeAllowed(node map[string]assets.Value, completion int64, preview bool) bool {
	value, ok := psbNumber(node["type"])
	if !ok {
		return true
	}
	nodeType := int(value)
	if nodeType < 0 || nodeType > 31 {
		return false
	}
	// Item masks: 1089 (0x441) normally, 1097 (0x449) for preview, both plus
	// the type-12 stencil bit the runtime always keeps
	// (PlayerRenderItems.cpp:556-559).
	bitmask := int64(0x1441)
	if completion != 0 || preview {
		bitmask = 0x1449
	}
	return bitmask&(int64(1)<<nodeType) != 0
}

// motionNodePriorDraw reports a node's emoteEdit priorDraw flag, which the
// reference reads for force-visible nodes and uses to gate preview items
// (PlayerUpdateGeometry.cpp:172-194; PlayerRenderItems.cpp:558-560).
func motionNodePriorDraw(node map[string]assets.Value) bool {
	edit, ok := node["emoteEdit"].(map[string]assets.Value)
	if !ok {
		return false
	}
	// PSB writes the flag as a number in some modules and a boolean in others
	// (PlayerUpdateGeometry.cpp:176-183 accepts PSBNumber and PSBBool).
	if flag, ok := edit["priorDraw"].(bool); ok {
		return flag
	}
	value, ok := psbNumber(edit["priorDraw"])
	return ok && value != 0
}

// motionNestedMotionSource reports whether a resolved frame expands a child
// motion instead of drawing a source image. The reference expands those nodes
// into the child player's render items (PlayerRenderItems.cpp:514-525) before
// the node type mask applies, so their draw stays enabled for every node type.
func motionNestedMotionSource(content map[string]assets.Value) bool {
	source, _ := content["src"].(string)
	return strings.HasPrefix(source, "motion/")
}

// motionParticleSource reports whether a frame runs a type-4 particle emitter.
// The reference expands particles before the node type mask applies
// (PlayerRenderItems.cpp:516-519).
func motionParticleSource(content map[string]assets.Value) bool {
	source, _ := content["src"].(string)
	return strings.HasPrefix(source, "particle/")
}

// motionStencilGroup reports whether the node is the reference's special
// stencilComposite parent (nodeType 12 with stencilType bit 4).
func motionStencilGroup(node map[string]assets.Value) bool {
	return psbMotionInt(node, "type", -1) == 12 && psbMotionInt(node, "stencilType", 0)&4 != 0
}

// motionStencilMask reports whether the node supplies a mask for its
// stencilComposite parent.
func motionStencilMask(node map[string]assets.Value) bool {
	switch psbMotionInt(node, "stencilType", 0) {
	case 1, 2, 5, 6:
		return true
	}
	return false
}

// applyMotionMaskChildren applies the reference alpha-mask compositor
// (PlayerRenderInternal.cpp:620-750) to a stencilComposite group buffer. Each
// mask child selects its operation through stencilType; maskMode selects
// thresholding or alpha arithmetic.
func applyMotionMaskChildren(resource *motionResource, root map[string]assets.Value, node map[string]assets.Value, tick float64, eval motionRenderEval, ctx motionRenderContext, pixels []byte, width, height int, stack map[string]bool) error {
	thresholdMask := eval.maskMode == 0
	for _, child := range motionStencilMaskChildren(node) {
		maskNode, ok := child.(map[string]assets.Value)
		if !ok {
			continue
		}
		itemFlags := psbMotionInt(maskNode, "stencilType", 0)
		mask := make([]byte, len(pixels))
		if err := renderMotionValue(resource, root, child, tick, eval, ctx, mask, width, height, stack); err != nil {
			return err
		}
		left, top, right, bottom := 0, 0, width, height
		if itemFlags != 1 {
			bounds := motionBounds{}
			motionValueBounds(resource, root, child, tick, eval, ctx, width, height, &bounds, stack)
			if !bounds.ok {
				continue
			}
			left, top = max(0, bounds.left), max(0, bounds.top)
			right, bottom = min(width, bounds.right), min(height, bounds.bottom)
		}
		for y := top; y < bottom; y++ {
			for x := left; x < right; x++ {
				offset := (y*width + x) * 4
				srcAlpha := int(mask[offset+3])
				dstAlpha := int(pixels[offset+3])
				switch itemFlags {
				case 1:
					if thresholdMask {
						if srcAlpha < 64 {
							pixels[offset+3] = 0
						}
					} else {
						pixels[offset+3] = byte(dstAlpha * srcAlpha / 255)
					}
				case 2:
					if thresholdMask {
						if srcAlpha >= 64 {
							pixels[offset+3] = 0
						}
					} else {
						pixels[offset+3] = byte((255 - srcAlpha) * dstAlpha / 255)
					}
				case 5, 6:
					if thresholdMask {
						if srcAlpha >= 64 {
							pixels[offset+3] = 255
						}
					} else {
						pixels[offset+3] = byte(srcAlpha + (255-srcAlpha)*dstAlpha/255)
					}
				}
			}
		}
	}
	return nil
}

func motionStencilMaskChildren(node map[string]assets.Value) []assets.Value {
	children := make([]assets.Value, 0, 4)
	list, _ := node["children"].([]assets.Value)
	for _, item := range list {
		child, ok := item.(map[string]assets.Value)
		if ok && motionStencilMask(child) {
			children = append(children, item)
		}
	}
	return children
}

func blendMotionBuffer(destination, source []byte) {
	for offset := 0; offset+3 < len(source) && offset+3 < len(destination); offset += 4 {
		if source[offset+3] == 0 {
			continue
		}
		blendMotionPixel(destination[offset:], source[offset:], source[offset+3], MotionBlendAlpha)
	}
}

type motionBounds struct {
	left, top, right, bottom int
	ok                       bool
}

func (b *motionBounds) add(left, top, right, bottom int) {
	if !b.ok {
		b.left, b.top, b.right, b.bottom = left, top, right, bottom
		b.ok = true
		return
	}
	b.left = min(b.left, left)
	b.top = min(b.top, top)
	b.right = max(b.right, right)
	b.bottom = max(b.bottom, bottom)
}

func motionValueBounds(resource *motionResource, root map[string]assets.Value, value assets.Value, tick float64, eval motionRenderEval, parent motionRenderContext, width, height int, bounds *motionBounds, stack map[string]bool) {
	if parent.targetNodes == nil {
		parent.targetNodes = motionIndexTargetNodes(value, tick, eval, parent)
	}
	switch node := value.(type) {
	case []assets.Value:
		for _, child := range node {
			motionValueBounds(resource, root, child, tick, eval, parent, width, height, bounds, stack)
		}
	case map[string]assets.Value:
		drawFrame := motionNodeAllowed(node, eval.completion, eval.preview)
		ctx := parent
		if frameList, ok := node["frameList"].([]assets.Value); ok {
			nodeTick := motionNodeTick(node, tick, eval)
			content, visible := psbMotionResolvedContent(frameList, nodeTick, psbMotionInt(node, "type", 0), psbMotionInt(node, "coordinate", 0))
			if !visible {
				return
			}
			ctx = motionEvalNodeContext(parent, content, node, nodeTick, tick)
			if motionNestedMotionSource(content) {
				ctx.childTick = motionNestedEvalTick(node, content, nodeTick, eval)
			}
			if drawFrame || motionNestedMotionSource(content) || motionParticleSource(content) {
				motionFrameBounds(resource, root, content, node, ctx, eval, nodeTick, width, height, bounds, stack)
			}
		}
		if child, ok := node["children"]; ok {
			motionValueBounds(resource, root, child, tick, eval, ctx, width, height, bounds, stack)
		}
	}
}

func motionFrameBounds(resource *motionResource, root map[string]assets.Value, content map[string]assets.Value, layer map[string]assets.Value, ctx motionRenderContext, eval motionRenderEval, tick float64, width, height int, bounds *motionBounds, stack map[string]bool) {
	source, _ := content["src"].(string)
	if strings.HasPrefix(source, "particle/") {
		return
	}
	if strings.HasPrefix(source, "src/") {
		// A deformed ancestor can move this source outside its affine image
		// bounds. Stencil groups must cover the resulting geometry.
		if psbMotionInt(layer, "meshTransform", 0) == 1 || len(ctx.meshAncestors) != 0 {
			bounds.add(0, 0, width, height)
			return
		}
		_, image, originX, originY, err := motionSourceImageAny(resource, eval.attached, root, source)
		if err != nil {
			return
		}
		if ctx.hasAnchorOrigin {
			originX, originY = ctx.anchorOriginX, ctx.anchorOriginY
		}
		originX += psbMotionFloat(content, "ox", 0)
		originY += psbMotionFloat(content, "oy", 0)
		centerX := float64(width)/2 + ctx.x
		centerY := float64(height)/2 + motionProjectedY(ctx)
		minX, minY := math.Inf(1), math.Inf(1)
		maxX, maxY := math.Inf(-1), math.Inf(-1)
		for _, corner := range [][2]float64{
			{-0.5 - originX, -0.5 - originY},
			{float64(image.Width) - 0.5 - originX, -0.5 - originY},
			{float64(image.Width) - 0.5 - originX, float64(image.Height) - 0.5 - originY},
			{-0.5 - originX, float64(image.Height) - 0.5 - originY},
		} {
			x := centerX + ctx.matrix.a*corner[0] + ctx.matrix.b*corner[1]
			y := centerY + ctx.matrix.c*corner[0] + ctx.matrix.d*corner[1]
			minX, maxX = math.Min(minX, x), math.Max(maxX, x)
			minY, maxY = math.Min(minY, y), math.Max(maxY, y)
		}
		bounds.add(int(math.Floor(minX)), int(math.Floor(minY)), int(math.Ceil(maxX)), int(math.Ceil(maxY)))
		return
	}
	if strings.HasPrefix(source, "motion/") {
		parts := strings.Split(strings.TrimPrefix(source, "motion/"), "/")
		if len(parts) >= 2 && !stack[parts[0]+"/"+parts[1]] {
			key := parts[0] + "/" + parts[1]
			stack[key] = true
			owner, ownerRoot, nested := motionLookupMotion(resource, eval.attached, parts[0], parts[1])
			if nestedLayers := motionMotionLayerList(owner, nested); len(nestedLayers) > 0 {
				childTick, childEval := motionNestedChildEval(eval, nested, ctx.childTick, ctx.coordinateMode)
				childCtx := motionChildOriginContext(ctx, content, layer, tick, eval)
				motionValueBounds(owner, ownerRoot, nestedLayers, childTick, childEval, childCtx, width, height, bounds, stack)
			}
			delete(stack, key)
		}
	}
}

// motionVariablesSignature is a stable digest of the normalized parameter
// values that parameterize layer selection, used as part of the render cache
// key.
func motionVariablesSignature(params []float64) string {
	if len(params) == 0 {
		return ""
	}
	var builder strings.Builder
	for index, value := range params {
		fmt.Fprintf(&builder, "%d=%v;", index, value)
	}
	return builder.String()
}

// motionNodeTick returns the frame-selection time for a layer node. The
// reference reads node+8->value for a parameterized node (PSB "parameterize"
// is the parameter-table index) and otherwise uses the Player timeline time;
// emote-like motions freeze non-parameterized nodes at time 0
// (PlayerUpdateLayerEval.cpp:313-326,730-738).

// motionClipStartTime returns the selected frame's timestamp. The reference
// stores frameA.time in the active slot and starts a child at
// parentLoopTime - clipStartTime + timeOffset (PlayerInternal.h:1485,
// PlayerUpdateChildMotion.cpp:170-205).
func motionClipStartTime(node map[string]assets.Value, tick float64) float64 {
	frames, ok := node["frameList"].([]assets.Value)
	if !ok {
		return 0
	}
	index, ok := psbMotionKeyframeIndex(frames, tick)
	if !ok {
		return 0
	}
	frame, ok := frames[index].(map[string]assets.Value)
	if !ok {
		return 0
	}
	return psbMotionFloat(frame, "time", 0)
}

// motionNestedTick advances a child motion from the parent timeline. The time
// offset belongs to the PSB motion sub-object, matching
// PlayerInternal.h:1124-1158 and PlayerUpdateChildMotion.cpp:170-205.
func motionNestedTick(node map[string]assets.Value, content map[string]assets.Value, tick float64) float64 {
	return motionNestedTickAt(node, content, tick, tick)
}

func motionNestedTickAt(node map[string]assets.Value, content map[string]assets.Value, clockTick, selectionTick float64) float64 {
	motionValue, _ := content["motion"].(map[string]assets.Value)
	elapsed := clockTick - motionClipStartTime(node, selectionTick) + psbMotionFloat(motionValue, "timeOffset", 0)
	if elapsed < 0 {
		return 0
	}
	return elapsed
}

func motionNodeTick(node map[string]assets.Value, tick float64, eval motionRenderEval) float64 {
	if value, ok := psbNumber(node["parameterize"]); ok {
		index := int(value)
		if index >= 0 && index < len(eval.params) {
			return eval.params[index]
		}
		return 0
	}
	if eval.freezeBody {
		return 0
	}
	return tick
}

func buildGPUMotion(resource *motionResource, chara, motion string, tick float64, filter int64, drawMatrix motionDrawMatrix, scale int32) (*RenderMotion, error) {
	return buildGPUMotionVariables(resource, chara, motion, tick, filter, drawMatrix, scale, nil, false, 0, 0, nil)
}

func buildGPUMotionVariables(resource *motionResource, chara, motion string, tick float64, filter int64, drawMatrix motionDrawMatrix, scale int32, params []float64, freezeBody bool, maskMode int64, completion int64, attached []*motionResource) (*RenderMotion, error) {
	return buildGPUMotionVariablesZFactor(resource, chara, motion, tick, filter, drawMatrix, scale, params, freezeBody, maskMode, completion, 1, attached)
}

func buildGPUMotionVariablesZFactor(resource *motionResource, chara, motion string, tick float64, filter int64, drawMatrix motionDrawMatrix, scale int32, params []float64, freezeBody bool, maskMode int64, completion int64, zFactor float64, attached []*motionResource) (*RenderMotion, error) {
	return buildGPUMotionVariablesZFactorInherit(resource, chara, motion, tick, filter, drawMatrix, scale, params, freezeBody, maskMode, completion, zFactor, attached, false)
}

func buildGPUMotionVariablesZFactorInherit(resource *motionResource, chara, motion string, tick float64, filter int64, drawMatrix motionDrawMatrix, scale int32, params []float64, freezeBody bool, maskMode int64, completion int64, zFactor float64, attached []*motionResource, independentLayerInherit bool, options ...motionRenderOptions) (*RenderMotion, error) {
	if resource == nil || resource.doc == nil {
		return nil, fmt.Errorf("motion resource is unavailable")
	}
	root, ok := resource.doc.Root.(map[string]assets.Value)
	if !ok {
		return nil, fmt.Errorf("motion PSB root is not a dictionary")
	}
	if scale < 1 {
		scale = 1
	}
	renderOptions := motionRenderOptionsValue(options)
	width := psbMotionInt(root["screenSize"], "width", 0) * int(scale)
	height := psbMotionInt(root["screenSize"], "height", 0) * int(scale)
	result := &RenderMotion{Filter: filter, Scale: scale, Width: width, Height: height, Draws: make([]RenderMotionDraw, 0, 8)}
	determinant := drawMatrix.a*drawMatrix.d - drawMatrix.b*drawMatrix.c
	if math.Abs(determinant) < 1e-9 || math.IsNaN(determinant) || math.IsInf(determinant, 0) {
		return result, nil
	}
	objects, _ := root["object"].(map[string]assets.Value)
	character, _ := objects[chara].(map[string]assets.Value)
	motions, _ := character["motion"].(map[string]assets.Value)
	motionValue, _ := motions[motion].(map[string]assets.Value)
	layers := motionMotionLayerList(resource, motionValue)
	if len(layers) == 0 {
		return result, nil
	}
	eval := motionRenderEval{
		filter: filter, params: params, freezeBody: freezeBody,
		maskMode: maskMode, completion: completion, attached: attached,
		zFactor: zFactor, hasZFactor: true, independentLayerInherit: independentLayerInherit,
		rootCoordinateMode:     renderOptions.rootCoordinateMode,
		emoteMode:              renderOptions.emoteMode,
		parentFrameLoopTime:    renderOptions.parentFrameLoopTime,
		hasParentFrameLoopTime: renderOptions.hasParentFrameLoopTime,
		frameLastTime:          renderOptions.frameLastTime,
		anyTimelinePlaying:     renderOptions.anyTimelinePlaying,
		rootState:              renderOptions.rootState,
		hasRootState:           renderOptions.hasRootState,
		anchorOutputs:          renderOptions.anchorOutputs,
		preview:                renderOptions.preview,
		stats:                  renderOptions.stats,
	}
	// The reference adds the camera offset to prepared item corners after
	// drawAffineMatrix * vertex has already been computed, i.e. it is a
	// translation in final item space, not a pre-affine canvas shift
	// (PlayerRenderItems.cpp:1174-1215). Folding it into the draw matrix
	// translation reproduces that exactly.
	if offsetX, offsetY, ok := motionCameraOffset(layers, tick, eval, drawMatrix); ok {
		drawMatrix = motionDrawMatrixWithCameraOffset(drawMatrix, offsetX, offsetY)
	}
	if motionTreeNeedsBakedGPU(resource, attached, layers, make(map[string]bool)) {
		// A stencil composite needs a private group buffer and alpha mask
		// application; mesh nodes need a triangle grid. Bake the tree at
		// canvas resolution and hand the result to the GPU as one transformed
		// draw so CPU and GPU execute the same pixels.
		frame, err := renderPSBMotionOptionsZFactorInherit(resource, chara, motion, tick, filter, params, freezeBody, maskMode, completion, zFactor, attached, independentLayerInherit, renderOptions)
		if err != nil {
			return nil, err
		}
		source := frame
		if scale > 1 {
			source = &assets.Image{
				Width:  frame.Width * uint32(scale),
				Height: frame.Height * uint32(scale),
				RGBA:   scaleRGBA(frame.RGBA, int32(frame.Width), int32(frame.Height), scale),
			}
		}
		// RenderMotionDraw translations identify the source image's top-left
		// corner. The CPU canvas compositor maps the motion canvas center to
		// drawMatrix.tx/ty, so shift the baked source by its transformed center.
		centerX, centerY := float64(source.Width)/2, float64(source.Height)/2
		result.Draws = append(result.Draws, RenderMotionDraw{
			LayerTreeKey: fmt.Sprintf("%p/%s/%s", resource, chara, motion),
			SourceKey:    fmt.Sprintf("%s/%t/%d/%g/%t/%g/%t/%#v/%t/%t/%s", motionBakedSourceKeyWithMatrix(resource, chara, motion, tick, filter, params, freezeBody, maskMode, completion, zFactor, attached, drawMatrix), independentLayerInherit, eval.rootCoordinateMode, eval.parentFrameLoopTime, eval.hasParentFrameLoopTime, eval.frameLastTime, eval.anyTimelinePlaying, eval.rootState, eval.hasRootState, eval.preview, motionAnchorOutputSignature(eval.anchorOutputs)),
			RGBA:         source.RGBA,
			Width:        int32(source.Width),
			Height:       int32(source.Height),
			A:            drawMatrix.a,
			B:            drawMatrix.b,
			C:            drawMatrix.c,
			D:            drawMatrix.d,
			TX:           drawMatrix.tx*float64(scale) - drawMatrix.a*centerX - drawMatrix.b*centerY,
			TY:           drawMatrix.ty*float64(scale) - drawMatrix.c*centerX - drawMatrix.d*centerY,
			Opacity:      255,
			Baked:        true,
		})
		return result, nil
	}
	if resource.stack == nil {
		resource.stack = make(map[string]bool)
	} else {
		clear(resource.stack)
	}
	ctx := motionRootRenderContext(eval, zFactor)
	eval.zFactor, eval.hasZFactor = zFactor, true
	ctx.layerTreeKey = fmt.Sprintf("%p/%s/%s", resource, chara, motion)
	nextLayerID := 1 // NodeTree assigns two resource-manager layer IDs per node.
	if err := collectGPUMotionValue(resource, root, layers, tick, eval, ctx, drawMatrix, result, resource.stack, &nextLayerID); err != nil {
		return nil, err
	}
	// The reference stable-sorts prepared items by accumulated posZ
	// (PlayerRenderItems.cpp:980-989).
	if len(result.Draws) > 1 {
		sort.SliceStable(result.Draws, func(i, j int) bool {
			return result.Draws[i].SortKey < result.Draws[j].SortKey
		})
	}
	return result, nil
}

func motionBakedSourceKey(resource *motionResource, chara, motion string, tick float64, filter int64, params []float64, freezeBody bool, maskMode, completion int64, attached []*motionResource) string {
	return fmt.Sprintf("%p\x00baked\x00%s/%s/%v/%d/%t/%d/%d/%s/%s",
		resource, chara, motion, tick, filter, freezeBody, maskMode, completion,
		motionVariablesSignature(params), motionAttachmentsSignature(attached))
}

// motionBakedSourceKeyWithMatrix extends the baked source identity with the
// draw affine because the camera-node offset is rounded through that affine
// and folded into the draw translation the baked source uses.
func motionBakedSourceKeyWithMatrix(resource *motionResource, chara, motion string, tick float64, filter int64, params []float64, freezeBody bool, maskMode, completion int64, zFactor float64, attached []*motionResource, drawMatrix motionDrawMatrix) string {
	return fmt.Sprintf("%s\x00%g,%g,%g,%g,%g,%g,%g",
		motionBakedSourceKey(resource, chara, motion, tick, filter, params, freezeBody, maskMode, completion, attached),
		drawMatrix.a, drawMatrix.b, drawMatrix.c, drawMatrix.d, drawMatrix.tx, drawMatrix.ty, zFactor)
}

func motionTreeNeedsBakedGPU(resource *motionResource, attached []*motionResource, value assets.Value, stack map[string]bool) bool {
	switch node := value.(type) {
	case []assets.Value:
		for _, child := range node {
			if motionTreeNeedsBakedGPU(resource, attached, child, stack) {
				return true
			}
		}
	case map[string]assets.Value:
		if motionStencilGroup(node) || psbMotionInt(node, "meshTransform", 0) != 0 || psbMotionInt(node, "type", -1) == 7 {
			return true
		}
		if frames, ok := node["frameList"].([]assets.Value); ok {
			for _, frameValue := range frames {
				frame, _ := frameValue.(map[string]assets.Value)
				content, _ := frame["content"].(map[string]assets.Value)
				source, _ := content["src"].(string)
				if !strings.HasPrefix(source, "motion/") {
					continue
				}
				parts := strings.Split(strings.TrimPrefix(source, "motion/"), "/")
				if len(parts) < 2 {
					continue
				}
				key := parts[0] + "/" + parts[1]
				if stack[key] {
					continue
				}
				stack[key] = true
				owner, _, nested := motionLookupMotion(resource, attached, parts[0], parts[1])
				needsBaked := motionTreeNeedsBakedGPU(owner, attached, motionMotionLayerList(owner, nested), stack)
				delete(stack, key)
				if needsBaked {
					return true
				}
			}
		}
		if motionTreeNeedsBakedGPU(resource, attached, node["children"], stack) {
			return true
		}
	}
	return false
}

func collectGPUMotionValue(resource *motionResource, root map[string]assets.Value, value assets.Value, tick float64, eval motionRenderEval, parent motionRenderContext, drawMatrix motionDrawMatrix, result *RenderMotion, stack map[string]bool, nextLayerID *int) error {
	if parent.targetNodes == nil {
		parent.targetNodes = motionIndexTargetNodes(value, tick, eval, parent)
	}
	switch node := value.(type) {
	case []assets.Value:
		for _, child := range node {
			if err := collectGPUMotionValue(resource, root, child, tick, eval, parent, drawMatrix, result, stack, nextLayerID); err != nil {
				return err
			}
		}
	case map[string]assets.Value:
		layerID := *nextLayerID
		*nextLayerID = layerID + 2
		drawFrame := motionNodeAllowed(node, eval.completion, eval.preview)
		ctx := parent
		ctx.layerID = layerID
		if frameList, ok := node["frameList"].([]assets.Value); ok {
			nodeTick := motionNodeTick(node, tick, eval)
			content, visible := psbMotionResolvedContent(frameList, nodeTick, psbMotionInt(node, "type", 0), psbMotionInt(node, "coordinate", 0))
			if !visible {
				if children, ok := node["children"]; ok {
					reserveGPUMotionLayerIDs(children, nextLayerID)
				}
				return nil
			}
			ctx = motionEvalNodeContext(parent, content, node, nodeTick, tick)
			ctx.layerID = layerID
			if ctx.hasAnchorColors {
				content = motionAnchorTintContent(content, motionAnchorNodeOutput{active: true, hasColors: true, colors: ctx.anchorColors})
			}
			if motionNestedMotionSource(content) {
				ctx.childTick = motionNestedEvalTick(node, content, nodeTick, eval)
			}
			// Preview preparation keeps only priorDraw items and skips
			// child-player and particle expansion (PlayerRenderItems.cpp:507-511,558-560).
			if eval.preview && !motionNodePriorDraw(node) {
				drawFrame = false
			}
			if drawFrame || (!eval.preview && (motionNestedMotionSource(content) || motionParticleSource(content))) {
				if err := collectGPUMotionFrame(resource, root, content, node, eval, ctx, drawMatrix, nodeTick, result, stack); err != nil {
					return err
				}
			}
		}
		if children, ok := node["children"]; ok {
			if err := collectGPUMotionValue(resource, root, children, tick, eval, ctx, drawMatrix, result, stack, nextLayerID); err != nil {
				return err
			}
		}
	}
	return nil
}

// reserveGPUMotionLayerIDs preserves the static NodeTree allocation order when
// an animated parent frame hides its descendants for the current render tick.
func reserveGPUMotionLayerIDs(value assets.Value, nextLayerID *int) {
	switch node := value.(type) {
	case []assets.Value:
		for _, child := range node {
			reserveGPUMotionLayerIDs(child, nextLayerID)
		}
	case map[string]assets.Value:
		*nextLayerID += 2
		if children, ok := node["children"]; ok {
			reserveGPUMotionLayerIDs(children, nextLayerID)
		}
	}
}

func collectGPUMotionFrame(resource *motionResource, root map[string]assets.Value, content map[string]assets.Value, layer map[string]assets.Value, eval motionRenderEval, ctx motionRenderContext, drawMatrix motionDrawMatrix, tick float64, result *RenderMotion, stack map[string]bool) error {
	if psbMotionInt(layer, "type", 0) == 4 {
		return collectGPUMotionParticles(resource, layer, content, ctx, eval, drawMatrix, tick, result, stack)
	}
	source, _ := content["src"].(string)
	if source == "" {
		return nil
	}
	if strings.HasPrefix(source, "particle/") {
		return collectGPUMotionParticles(resource, layer, content, ctx, eval, drawMatrix, tick, result, stack)
	}
	if strings.HasPrefix(source, "src/") {
		owner, image, originX, originY, err := motionSourceImageAny(resource, eval.attached, root, source)
		if err != nil {
			return err
		}
		if ctx.hasAnchorOrigin {
			originX, originY = ctx.anchorOriginX, ctx.anchorOriginY
		}
		originX += psbMotionFloat(content, "ox", 0)
		originY += psbMotionFloat(content, "oy", 0)
		transform, tx, ty := motionGPUTransform(drawMatrix, ctx, originX, originY)
		key := source
		if owner.path != "" {
			key = owner.path + "\x00" + source
		}
		frame, tintKey := motionFrameImageTinted(owner, source, image, content, ctx.blendMode)
		key += tintKey
		pixels := frame.RGBA
		scale := result.Scale
		rgba := pixels
		drawWidth, drawHeight := int32(image.Width), int32(image.Height)
		if scale > 1 {
			rgba = scaleRGBA(pixels, int32(image.Width), int32(image.Height), scale)
			drawWidth *= scale
			drawHeight *= scale
		}
		blend := motionBlendMode(ctx.blendMode)
		if blend == MotionBlendSubtractive {
			rgba = subtractiveMotionRGBA(rgba)
			key += "|subtractive"
		}
		result.Draws = append(result.Draws, RenderMotionDraw{
			LayerID:      ctx.layerID,
			LayerTreeKey: ctx.layerTreeKey,
			Viewport:     [4]float64{ctx.viewport.minX, ctx.viewport.minY, ctx.viewport.maxX, ctx.viewport.maxY},
			HasViewport:  ctx.hasViewport,
			SourceKey:    key,
			RGBA:         rgba,
			Width:        drawWidth,
			Height:       drawHeight,
			A:            transform.a,
			B:            transform.b,
			C:            transform.c,
			D:            transform.d,
			TX:           tx * float64(scale),
			TY:           ty * float64(scale),
			Opacity:      ctx.opacity,
			Blend:        blend,
			SortKey:      ctx.z,
		})
		return nil
	}
	if strings.HasPrefix(source, "motion/") {
		parts := strings.Split(strings.TrimPrefix(source, "motion/"), "/")
		if len(parts) >= 2 {
			key := parts[0] + "/" + parts[1]
			if !stack[key] {
				stack[key] = true
				owner, ownerRoot, nested := motionLookupMotion(resource, eval.attached, parts[0], parts[1])
				if nestedLayers := motionMotionLayerList(owner, nested); len(nestedLayers) > 0 {
					childCtx := motionChildOriginContext(ctx, content, layer, tick, eval)
					childCtx.layerTreeKey = fmt.Sprintf("%s/node:%d/motion:%s", ctx.layerTreeKey, ctx.layerID, key)
					nestedLayerID := 1
					childTick, childEval := motionNestedChildEval(eval, nested, ctx.childTick, ctx.coordinateMode)
					if err := collectGPUMotionValue(owner, ownerRoot, nestedLayers, childTick, childEval, childCtx, drawMatrix, result, stack, &nestedLayerID); err != nil {
						return err
					}
				}
				delete(stack, key)
			}
		}
	}
	return nil
}

// subtractiveMotionRGBA rewrites a premultiplied sprite into the complement
// the reversed-subtraction blend consumes: RGB' = alpha - RGB. PsSubBlend
// wants dst - alpha*(1-src), and alpha - RGB is alpha*(1-src) in premultiplied
// form (RenderManager_ogl.cpp:3725-3731).
func subtractiveMotionRGBA(pixels []byte) []byte {
	out := make([]byte, len(pixels))
	for offset := 0; offset+3 < len(pixels); offset += 4 {
		alpha := uint32(pixels[offset+3])
		complement := func(value byte) byte {
			if uint32(value) >= alpha {
				return 0
			}
			return byte(alpha - uint32(value))
		}
		out[offset+0] = complement(pixels[offset+0])
		out[offset+1] = complement(pixels[offset+1])
		out[offset+2] = complement(pixels[offset+2])
		out[offset+3] = pixels[offset+3]
	}
	return out
}

func motionGPUTransform(drawMatrix motionDrawMatrix, ctx motionRenderContext, originX, originY float64) (motionMatrix, float64, float64) {
	transform := motionMatrix{
		a: drawMatrix.a*ctx.matrix.a + drawMatrix.b*ctx.matrix.c,
		b: drawMatrix.a*ctx.matrix.b + drawMatrix.b*ctx.matrix.d,
		c: drawMatrix.c*ctx.matrix.a + drawMatrix.d*ctx.matrix.c,
		d: drawMatrix.c*ctx.matrix.b + drawMatrix.d*ctx.matrix.d,
	}
	tx := drawMatrix.tx + drawMatrix.a*ctx.x + drawMatrix.b*motionProjectedY(ctx) - transform.a*originX - transform.b*originY
	ty := drawMatrix.ty + drawMatrix.c*ctx.x + drawMatrix.d*motionProjectedY(ctx) - transform.c*originX - transform.d*originY
	return transform, tx, ty
}

// collectMotionFrame defers a node frame's draw. Source sprites and meshes
// become one draw item keyed by the node's accumulated z; particles and nested
// motions expand into the same list, matching the reference's flattened item
// list (PlayerRenderItems.cpp:500-530).
func collectMotionFrame(resource *motionResource, root map[string]assets.Value, content map[string]assets.Value, layer map[string]assets.Value, eval motionRenderEval, ctx motionRenderContext, tick float64, list *motionDrawList, width, height int, stack map[string]bool) error {
	if psbMotionInt(layer, "type", 0) == 4 {
		// A particle emitter contributes spawned particles only, never its own
		// source image (PlayerRenderItems.cpp:240-250).
		return collectMotionParticles(resource, layer, content, ctx, eval, tick, list, width, height, stack)
	}
	source, _ := content["src"].(string)
	if source == "" {
		return nil
	}
	if strings.HasPrefix(source, "particle/") {
		return collectMotionParticles(resource, layer, content, ctx, eval, tick, list, width, height, stack)
	}
	if strings.HasPrefix(source, "src/") {
		list.add(ctx.z, func(dst []byte) error {
			return drawMotionSourceFrame(resource, root, content, layer, eval, ctx, dst, width, height)
		})
		return nil
	}
	if strings.HasPrefix(source, "motion/") {
		parts := strings.Split(strings.TrimPrefix(source, "motion/"), "/")
		if len(parts) >= 2 {
			key := parts[0] + "/" + parts[1]
			if !stack[key] {
				stack[key] = true
				owner, ownerRoot, nested := motionLookupMotion(resource, eval.attached, parts[0], parts[1])
				if nestedLayers := motionMotionLayerList(owner, nested); len(nestedLayers) > 0 {
					childCtx := motionChildOriginContext(ctx, content, layer, tick, eval)
					childTick, childEval := motionNestedChildEval(eval, nested, ctx.childTick, ctx.coordinateMode)
					if err := collectMotionValue(owner, ownerRoot, nestedLayers, childTick, childEval, childCtx, list, width, height, stack); err != nil {
						return err
					}
				}
				delete(stack, key)
			}
		}
	}
	return nil
}

// motionChildOriginContext shifts a child motion's root by the node's origin
// offset, which the reference applies to the child player rather than to each
// of its layers (PlayerUpdateChildMotion.cpp:454-470).
func motionChildOriginContext(ctx motionRenderContext, content, node map[string]assets.Value, tick float64, eval motionRenderEval) motionRenderContext {
	parentAngle := ctx.transform.angle * 180 / math.Pi
	computedAngle, hasAngle, mode := 0.0, false, 0
	if motionValue, ok := content["motion"].(map[string]assets.Value); ok {
		// PlayerInternal.h initializes the motion block with dt=1. Child root
		// angle and matrix propagation follow PlayerUpdateChildMotion.cpp.
		mask := psbMotionInt(motionValue, "mask", 0)
		mode = 1
		if mask&0x2 != 0 {
			mode = psbMotionInt(motionValue, "dt", 1)
		}
		dofst := 0.0
		if mask&0x8 != 0 {
			dofst = psbMotionFloat(motionValue, "dofst", 0)
		}
		if mask&0x4 != 0 && psbMotionInt(motionValue, "docmpl", 0) != 0 {
			dofst = motionChildAngleBase(node, tick, dofst)
		}
		if mode == 1 {
			computedAngle = dofst + parentAngle
			hasAngle = true
		} else if mode == 2 || mode == 3 || mode == 4 {
			if mode == 3 {
				frames, _ := node["frameList"].([]assets.Value)
				index, ok := psbMotionKeyframeIndex(frames, tick)
				if !ok || index >= len(frames)-1 {
					mode = 0
				} else if frame, ok := frames[index].(map[string]assets.Value); !ok || int(psbMotionFloat(frame, "type", 0)) != 3 {
					mode = 0
				}
			}
			if mode == 2 {
				dx, dy := 0.0, 0.0
				coordinateMode := psbMotionInt(node, "coordinate", 0)
				if coordinateMode == 0 || coordinateMode == 1 {
					if !eval.anyTimelinePlaying {
						dx, dy = motionChildStoppedPositionDelta(ctx, eval, coordinateMode)
					}
					computedAngle = dofst + math.Atan2(dy, dx)*180/math.Pi
				}
				hasAngle = true
			} else if mode == 3 {
				dx, dy := 0.0, 0.0
				coordinateMode := psbMotionInt(node, "coordinate", 0)
				if coordinateMode == 0 || coordinateMode == 1 {
					dx, dy, _ = motionChildPositionDelta(node, tick, coordinateMode)
					computedAngle = dofst + math.Atan2(dy, dx)*180/math.Pi
				}
				hasAngle = true
			} else if target, found := ctx.targetNodes[psbMotionString(motionValue, "dtgt")]; found {
				coordinateMode := psbMotionInt(node, "coordinate", 0)
				if coordinateMode == 0 {
					computedAngle = dofst + math.Atan2(target.y-ctx.y, target.x-ctx.x)*180/math.Pi
				} else if coordinateMode == 1 {
					computedAngle = dofst + math.Atan2(target.z-ctx.z, target.x-ctx.x)*180/math.Pi
				}
				hasAngle = true
			}
		}
	}
	if mode != 1 && mode != 0 {
		for computedAngle < 0 {
			computedAngle += 360
		}
		for computedAngle >= 360 {
			computedAngle -= 360
		}
	}
	ctx.transform.angle = computedAngle * math.Pi / 180
	if !hasAngle && computedAngle != parentAngle {
		delta := (computedAngle - parentAngle) * math.Pi / 180
		if ctx.transform.flipX != ctx.transform.flipY {
			delta = -delta
		}
		cosine, sine := math.Cos(delta), math.Sin(delta)
		matrix := ctx.matrix
		ctx.matrix = motionMatrix{
			a: cosine*matrix.a + sine*matrix.b,
			b: cosine*matrix.b - matrix.a*sine,
			c: cosine*matrix.c + sine*matrix.d,
			d: cosine*matrix.d - matrix.c*sine,
		}
	}
	ctx.targetNodes = nil
	ctx.anchorNamespace = motionAnchorContextKey(ctx.anchorNamespace, node)
	ctx.rootMatrix = ctx.matrix
	ctx.rootTransform = ctx.transform
	ctx.rootOpacity = ctx.opacity
	ctx.rootX, ctx.rootY, ctx.rootZ = ctx.x, ctx.y, ctx.z
	originX := psbMotionFloat(content, "ox", 0)
	originY := psbMotionFloat(content, "oy", 0)
	if originX == 0 && originY == 0 {
		return ctx
	}
	shiftX := -(ctx.matrix.a*originX + ctx.matrix.b*originY)
	shiftY := -(ctx.matrix.c*originX + ctx.matrix.d*originY)
	ctx.x += shiftX
	ctx.rootX += shiftX
	if ctx.coordinateMode != 0 {
		ctx.z += shiftY
		ctx.rootZ += shiftY
	} else {
		ctx.y += shiftY
		ctx.rootY += shiftY
	}
	return ctx
}

func motionIndexTargetNodes(value assets.Value, tick float64, eval motionRenderEval, parent motionRenderContext) map[string]motionTargetPosition {
	targets := make(map[string]motionTargetPosition)
	var visit func(assets.Value, motionRenderContext)
	visit = func(value assets.Value, inherited motionRenderContext) {
		switch node := value.(type) {
		case []assets.Value:
			for _, child := range node {
				visit(child, inherited)
			}
		case map[string]assets.Value:
			ctx := inherited
			if frames, ok := node["frameList"].([]assets.Value); ok {
				nodeTick := motionNodeTick(node, tick, eval)
				content, visible := psbMotionResolvedContent(frames, nodeTick, psbMotionInt(node, "type", 0), psbMotionInt(node, "coordinate", 0))
				if !visible {
					return
				}
				ctx = motionEvalNodeContext(inherited, content, node, nodeTick, tick)
				if motionNestedMotionSource(content) {
					ctx.childTick = motionNestedEvalTick(node, content, nodeTick, eval)
				}
			}
			if label := psbLabel(node); label != "" {
				targets[label] = motionTargetPosition{x: ctx.x, y: ctx.y, z: ctx.z}
			}
			if children, ok := node["children"]; ok {
				visit(children, ctx)
			}
		}
	}
	visit(value, parent)
	return targets
}

func motionChildAngleBase(node map[string]assets.Value, tick, dofst float64) float64 {
	frames, _ := node["frameList"].([]assets.Value)
	index, ok := psbMotionKeyframeIndex(frames, tick)
	if !ok || index+1 >= len(frames) {
		return dofst
	}
	active, activeOK := frames[index].(map[string]assets.Value)
	next, nextOK := frames[index+1].(map[string]assets.Value)
	if !activeOK || !nextOK || int(psbMotionFloat(active, "type", 0)) != 3 {
		return dofst
	}
	activeContent, _ := active["content"].(map[string]assets.Value)
	nextContent, _ := next["content"].(map[string]assets.Value)
	activeMotion, _ := activeContent["motion"].(map[string]assets.Value)
	nextMotion, _ := nextContent["motion"].(map[string]assets.Value)
	if activeMotion == nil || nextMotion == nil {
		return dofst
	}
	nextMode := 1
	if psbMotionInt(nextMotion, "mask", 0)&0x2 != 0 {
		nextMode = psbMotionInt(nextMotion, "dt", 1)
	}
	if nextMode == 0 {
		return dofst
	}
	if psbMotionInt(nextMotion, "mask", 0)&0x8 == 0 {
		return dofst
	}
	currentStart, nextStart := psbMotionFloat(active, "time", 0), psbMotionFloat(next, "time", 0)
	if nextStart == currentStart {
		return dofst
	}
	ratio := (tick - currentStart) / (nextStart - currentStart)
	if activeContent["acc"] != nil {
		ratio = psbMotionCurveRatio(activeContent["acc"], ratio)
	}
	other := psbMotionFloat(nextMotion, "dofst", 0)
	if dofst >= other && dofst-other > 180 {
		other += 360
	} else if other > dofst && other-dofst > 180 {
		other -= 360
	}
	value := dofst*(1-ratio) + other*ratio
	if value < 0 {
		value += 360
	}
	if value >= 360 {
		value -= 360
	}
	return value
}

func motionChildPositionDelta(node map[string]assets.Value, tick float64, coordinateMode int) (float64, float64, bool) {
	frames, _ := node["frameList"].([]assets.Value)
	from, visible := psbMotionResolvedContent(frames, tick, psbMotionInt(node, "type", 0), coordinateMode)
	if !visible {
		return 0, 0, false
	}
	to, visible := psbMotionResolvedContent(frames, tick+0.0001, psbMotionInt(node, "type", 0), coordinateMode)
	if !visible {
		return 0, 0, false
	}
	fromCoord, toCoord := psbMotionCoordValues(from["coord"]), psbMotionCoordValues(to["coord"])
	if len(fromCoord) == 0 || len(toCoord) == 0 {
		return 0, 0, false
	}
	dx := toCoord[0] - fromCoord[0]
	if coordinateMode == 1 {
		return dx, toCoord[2] - fromCoord[2], true
	}
	if coordinateMode == 0 {
		return dx, toCoord[1] - fromCoord[1], true
	}
	return 0, 0, false
}

func motionChildStoppedPositionDelta(current motionRenderContext, eval motionRenderEval, coordinateMode int) (float64, float64) {
	deltaTime := eval.frameLastTime
	if deltaTime <= 0 {
		deltaTime = 1
	}
	chain := make([]motionRenderContext, 0, 8)
	for cursor := &current; cursor != nil && cursor.evalNode != nil; cursor = cursor.inheritParent {
		chain = append(chain, *cursor)
	}
	if len(chain) == 0 {
		return 0, 0
	}
	previous := motionRenderContext{}
	if parent := chain[len(chain)-1].inheritParent; parent != nil {
		previous = *parent
	}
	for index := len(chain) - 1; index >= 0; index-- {
		entry := chain[index]
		node := entry.evalNode
		frames, _ := node["frameList"].([]assets.Value)
		previousPlayerTick := entry.evalPlayerTick - deltaTime
		selectionTick := motionNodeTick(node, previousPlayerTick, eval)
		content, visible := psbMotionResolvedContent(frames, selectionTick, psbMotionInt(node, "type", 0), psbMotionInt(node, "coordinate", 0))
		if !visible {
			return 0, 0
		}
		previous = motionEvalNodeContext(previous, content, node, selectionTick, previousPlayerTick)
	}
	if coordinateMode == 1 {
		return current.x - previous.x, current.z - previous.z
	}
	return current.x - previous.x, current.y - previous.y
}

// drawMotionSourceFrame rasterizes one source sprite or mesh frame.
func drawMotionSourceFrame(resource *motionResource, root map[string]assets.Value, content map[string]assets.Value, layer map[string]assets.Value, eval motionRenderEval, ctx motionRenderContext, pixels []byte, width, height int) error {
	source, _ := content["src"].(string)
	owner, image, originX, originY, err := motionSourceImageAny(resource, eval.attached, root, source)
	if err != nil {
		return err
	}
	if ctx.hasAnchorOrigin {
		originX, originY = ctx.anchorOriginX, ctx.anchorOriginY
	}
	opaque := motionSourceOpaque(owner, source, image)
	var premultiplied []byte
	if eval.filter != 0 {
		premultiplied = motionPremultipliedImage(owner, source, image)
	}
	imageOut := motionFrameImage(owner, source, image, content, ctx.blendMode)
	if imageOut != image {
		// The cached premultiplied copy belongs to the untinted source;
		// let the bilinear sampler premultiply the colored pixels instead.
		premultiplied = nil
	}
	if psbMotionInt(layer, "meshTransform", 0) != 0 {
		renderMotionMeshFrame(layer, content, ctx, imageOut, originX+psbMotionFloat(content, "ox", 0), originY+psbMotionFloat(content, "oy", 0), pixels, width, height, eval.stats)
		return nil
	}
	return blendMotionImage(pixels, width, height, imageOut, originX+psbMotionFloat(content, "ox", 0), originY+psbMotionFloat(content, "oy", 0), eval.filter, premultiplied, opaque, ctx)
}

// motionMeshControlNetPoints is the PSB mesh control-net dimension.
const motionMeshControlNetPoints = 16

// motionMeshControlNet is the world-space 4x4 Bezier control net a
// meshTransform node contributes to its descendants plus the inverse of the
// affine mapping from normalized (u,v) to the node's undeformed pixels.
// Control points are stored row-major with u varying fastest, matching
// PlayerUpdateGeometry.cpp:418-437 and the PSB mesh.bp layout documented in
// MOTIONPLAYER_PSB_STRUCT.md §9.
type motionMeshControlNet struct {
	points           [motionMeshControlNetPoints][2]float64
	originX, originY float64
	invM11, invM12   float64
	invM21, invM22   float64
	flags            int
}

// evaluate returns the Bezier patch surface point at normalized (u,v).
func (net *motionMeshControlNet) evaluate(u, v float64) (float64, float64) {
	su, sv := 1-u, 1-v
	bu := [4]float64{su * su * su, 3 * su * su * u, 3 * su * u * u, u * u * u}
	bv := [4]float64{sv * sv * sv, 3 * sv * sv * v, 3 * sv * v * v, v * v * v}
	x, y := 0.0, 0.0
	for row := range 4 {
		for column := range 4 {
			weight := bv[row] * bu[column]
			point := &net.points[row*4+column]
			x += point[0] * weight
			y += point[1] * weight
		}
	}
	return x, y
}

// deform maps a canvas-space point through the ancestor's normalized space
// and back through the ancestor's deformed surface
// (PlayerUpdateGeometry.cpp:479-508).
func (net *motionMeshControlNet) deform(x, y float64) (float64, float64) {
	u, v := net.normalized(x, y)
	return net.evaluate(u, v)
}

func (net *motionMeshControlNet) normalized(x, y float64) (float64, float64) {
	tx := x - net.originX
	ty := y - net.originY
	return net.invM11*tx + net.invM12*ty, net.invM21*tx + net.invM22*ty
}

// deformOffset applies the control net to an absolute canvas position,
// matching the reference's parent mesh deformation of child positions
// (PlayerUpdateLayersInternal.h:362-407).
func (net *motionMeshControlNet) deformOffset(x, y float64) (float64, float64) {
	return net.deform(x, y)
}

func (net *motionMeshControlNet) childAngleScale(x, y float64, inheritMask int) (float64, float64) {
	u, v := net.normalized(x, y)
	deformedX, deformedY := net.evaluate(u, v)
	deformedU, deformedV := net.normalized(deformedX, deformedY)
	angle, scale := 0.0, 1.0
	evaluate := func(u, v float64) (float64, float64) {
		return net.evaluate(u, v)
	}
	if net.flags&2 != 0 && inheritMask&0x10 != 0 {
		const epsilon = 0.0001
		x1, y1 := evaluate(deformedU-epsilon, deformedV)
		x2, y2 := evaluate(deformedU+epsilon, deformedV)
		x3, y3 := evaluate(deformedU, deformedV-epsilon)
		x4, y4 := evaluate(deformedU, deformedV+epsilon)
		a1 := math.Atan2(y3-y4, x4-x3)
		a2 := math.Atan2(x2-x1, y2-y1)
		angle = (a1 + a2) * 0.5 * 180 / math.Pi
	}
	if net.flags&4 != 0 && inheritMask&0x60 != 0 {
		const epsilon = 0.0001
		x1, y1 := evaluate(deformedU-epsilon, deformedV)
		x2, y2 := evaluate(deformedU+epsilon, deformedV)
		x3, y3 := evaluate(deformedU, deformedV-epsilon)
		x4, y4 := evaluate(deformedU, deformedV+epsilon)
		dx, dy := x2-x1, y2-y1
		area1 := math.Abs(dx*(y4-y1)-dy*(x4-x1)) * 0.5
		area2 := math.Abs(dx*(y3-y1)-dy*(x3-x1)) * 0.5
		scale = math.Sqrt(area1+area2+area2+area1) / 0.0002
	}
	return angle, scale
}

// motionMeshControlNetAt builds the control net declared by content.mesh.bp.
// The reference reads bp as 16 normalized control points with u varying
// fastest; a missing, null, malformed, or undeformed unit grid has no
// deformation and returns false so callers keep the exact affine mesh path.
func motionMeshControlNetAt(content map[string]assets.Value, ctx motionRenderContext, originX, originY, clipW, clipH, width, height float64) (motionMeshControlNet, bool) {
	mesh, _ := content["mesh"].(map[string]assets.Value)
	bp := psbMotionList(mesh["bp"])
	if len(bp) < 32 {
		return motionMeshControlNet{}, false
	}
	deformed := false
	for index := range 16 {
		u, v := bp[index*2], bp[index*2+1]
		if math.IsNaN(u) || math.IsInf(u, 0) || math.IsNaN(v) || math.IsInf(v, 0) {
			return motionMeshControlNet{}, false
		}
		if math.Abs(u-float64(index%4)/3) > 1e-6 || math.Abs(v-float64(index/4)/3) > 1e-6 {
			deformed = true
		}
	}
	if !deformed {
		return motionMeshControlNet{}, false
	}
	mw11 := ctx.matrix.a * clipW
	mw12 := ctx.matrix.b * clipH
	mw21 := ctx.matrix.c * clipW
	mw22 := ctx.matrix.d * clipH
	det := mw11*mw22 - mw12*mw21
	if math.Abs(det) < 1e-10 || math.IsNaN(det) || math.IsInf(det, 0) {
		return motionMeshControlNet{}, false
	}
	centerX := width/2 + ctx.x
	centerY := height/2 + motionProjectedY(ctx)
	var net motionMeshControlNet
	net.invM11 = mw22 / det
	net.invM12 = -mw12 / det
	net.invM21 = -mw21 / det
	net.invM22 = mw11 / det
	net.originX = centerX - (ctx.matrix.a*originX + ctx.matrix.b*originY)
	net.originY = centerY - (ctx.matrix.c*originX + ctx.matrix.d*originY)
	for index := range 16 {
		u, v := bp[index*2], bp[index*2+1]
		localX, localY := u*clipW-originX, v*clipH-originY
		net.points[index][0] = centerX + ctx.matrix.a*localX + ctx.matrix.b*localY
		net.points[index][1] = centerY + ctx.matrix.c*localX + ctx.matrix.d*localY
	}
	return net, true
}

// motionMeshAncestorContext carries the node's mesh sync mask to direct
// children and adds its surface to the descendant mesh chain when bit 0 is set.
func motionMeshAncestorContext(resource *motionResource, root map[string]assets.Value, node map[string]assets.Value, content map[string]assets.Value, eval motionRenderEval, ctx motionRenderContext, width, height int) motionRenderContext {
	flags := int(psbMotionInt(node, "meshSyncChildMask", 0)) & 7
	if psbMotionInt(node, "meshTransform", 0) != 1 || flags == 0 {
		return ctx
	}
	source, _ := content["src"].(string)
	if !strings.HasPrefix(source, "src/") {
		return ctx
	}
	_, image, originX, originY, err := motionSourceImageAny(resource, eval.attached, root, source)
	if err != nil || image == nil {
		return ctx
	}
	originX += psbMotionFloat(content, "ox", 0)
	originY += psbMotionFloat(content, "oy", 0)
	clipW := psbMotionFloat(content, "width", float64(image.Width))
	clipH := psbMotionFloat(content, "height", float64(image.Height))
	if clipW <= 0 || clipH <= 0 {
		return ctx
	}
	net, ok := motionMeshControlNetAt(content, ctx, originX, originY, clipW, clipH, float64(width), float64(height))
	if !ok {
		return ctx
	}
	net.flags = flags
	if flags&1 != 0 {
		ancestors := make([]motionMeshControlNet, len(ctx.meshAncestors)+1)
		copy(ancestors, ctx.meshAncestors)
		ancestors[len(ctx.meshAncestors)] = net
		ctx.meshAncestors = ancestors
	}
	ctx.meshChildDeform = &net
	return ctx
}

// renderMotionMeshFrame draws a meshTransform node using the reference's
// grid construction and per-cell two-triangle draw (PlayerUpdateGeometry.cpp:
// 290-370, PlayerRenderTargets.cpp:498-552). meshType 1 builds the bilinear
// grid at meshDivision resolution; other non-zero mesh types have no control
// points and are skipped, matching the "!copied → continue" reference path.
// A content.mesh.bp control net deforms the node's own surface, and every
// ancestor net collected in ctx.meshAncestors deforms the grid points in turn
// (0x6BD118..0x6BD380).
func renderMotionMeshFrame(layer map[string]assets.Value, content map[string]assets.Value, ctx motionRenderContext, image *assets.Image, originX, originY float64, pixels []byte, width, height int, stats *motionRenderStats) {
	if image == nil || psbMotionInt(layer, "meshTransform", 0) != 1 {
		return
	}
	clipW := psbMotionFloat(content, "width", float64(image.Width))
	clipH := psbMotionFloat(content, "height", float64(image.Height))
	if clipW <= 0 || clipH <= 0 {
		return
	}
	divTotal := psbMotionInt(layer, "meshDivision", 0)
	if divTotal > 50 {
		divTotal = 50
	}
	if divTotal < 1 {
		divTotal = 4
	}
	divX := int(float64(divTotal)*clipW/(clipW+clipH)) + 1
	divY := divTotal - divX + 2
	if divX < 2 || divY < 2 {
		return
	}
	points := make([]motionPoint, divX*divY)
	centerX := float64(width)/2 + ctx.x
	centerY := float64(height)/2 + motionProjectedY(ctx)
	sourceWidth, sourceHeight := float64(image.Width), float64(image.Height)
	controlNet, deformed := motionMeshControlNetAt(content, ctx, originX, originY, clipW, clipH, float64(width), float64(height))
	if stats != nil && len(ctx.meshAncestors) != 0 {
		// Each ancestor maps this node's control points plus its origin
		// (PlayerUpdateGeometry.cpp:518-523).
		stats.meshVertices += len(ctx.meshAncestors) * (motionMeshControlNetPoints + 1)
	}
	gridX := make([]float64, divX)
	gridY := make([]float64, divY)
	for index := range divX {
		gridX[index] = float64(index) / float64(divX-1)
	}
	for index := range divY {
		gridY[index] = float64(index) / float64(divY-1)
	}
	for gy := range divY {
		for gx := range divX {
			u, v := gridX[gx], gridY[gy]
			var px, py float64
			if deformed {
				px, py = controlNet.evaluate(u, v)
			} else {
				localX, localY := u*clipW-originX, v*clipH-originY
				px = centerX + ctx.matrix.a*localX + ctx.matrix.b*localY
				py = centerY + ctx.matrix.c*localX + ctx.matrix.d*localY
			}
			for index := len(ctx.meshAncestors) - 1; index >= 0; index-- {
				px, py = ctx.meshAncestors[index].deform(px, py)
			}
			points[gy*divX+gx] = motionPoint{
				x: px,
				y: py,
				u: u * sourceWidth,
				v: v * sourceHeight,
			}
		}
	}
	for gy := range divY - 1 {
		for gx := range divX - 1 {
			p0 := points[gy*divX+gx]
			p1 := points[gy*divX+gx+1]
			p2 := points[(gy+1)*divX+gx]
			p3 := points[(gy+1)*divX+gx+1]
			rasterMotionTriangle(pixels, width, height, image, p0, p1, p2, ctx.opacity, motionBlendMode(ctx.blendMode), ctx.viewport, ctx.hasViewport)
			rasterMotionTriangle(pixels, width, height, image, p1, p2, p3, ctx.opacity, motionBlendMode(ctx.blendMode), ctx.viewport, ctx.hasViewport)
		}
	}
}

type motionPoint struct {
	x, y float64
	u, v float64
}

func rasterMotionTriangle(dst []byte, width, height int, image *assets.Image, p0, p1, p2 motionPoint, opacity uint8, blend MotionBlendMode, viewport motionViewport, hasViewport bool) {
	area := (p1.x-p0.x)*(p2.y-p0.y) - (p2.x-p0.x)*(p1.y-p0.y)
	if math.Abs(area) < 1e-9 || opacity == 0 {
		return
	}
	minX := max(0, int(math.Floor(math.Min(p0.x, math.Min(p1.x, p2.x)))))
	maxX := min(width, int(math.Ceil(math.Max(p0.x, math.Max(p1.x, p2.x)))))
	minY := max(0, int(math.Floor(math.Min(p0.y, math.Min(p1.y, p2.y)))))
	maxY := min(height, int(math.Ceil(math.Max(p0.y, math.Max(p1.y, p2.y)))))
	if hasViewport {
		clipLeft, clipTop, clipRight, clipBottom := motionViewportPixels(viewport, width, height)
		minX, minY = max(minX, clipLeft), max(minY, clipTop)
		maxX, maxY = min(maxX, clipRight), min(maxY, clipBottom)
	}
	if minX >= maxX || minY >= maxY {
		return
	}
	sourceWidth, sourceHeight := int(image.Width), int(image.Height)
	for y := minY; y < maxY; y++ {
		py := float64(y) + 0.5
		for x := minX; x < maxX; x++ {
			px := float64(x) + 0.5
			e0 := (p1.x-px)*(p2.y-py) - (p2.x-px)*(p1.y-py)
			e1 := (p2.x-px)*(p0.y-py) - (p0.x-px)*(p2.y-py)
			e2 := (p0.x-px)*(p1.y-py) - (p1.x-px)*(p0.y-py)
			positive := area > 0
			if !motionTriangleEdgeCovered(e0, p1, p2, positive) ||
				!motionTriangleEdgeCovered(e1, p2, p0, positive) ||
				!motionTriangleEdgeCovered(e2, p0, p1, positive) {
				continue
			}
			w0, w1, w2 := e0/area, e1/area, e2/area
			sourceX := int(w0*p0.u + w1*p1.u + w2*p2.u)
			sourceY := int(w0*p0.v + w1*p1.v + w2*p2.v)
			if sourceX < 0 {
				sourceX = 0
			} else if sourceX >= sourceWidth {
				sourceX = sourceWidth - 1
			}
			if sourceY < 0 {
				sourceY = 0
			} else if sourceY >= sourceHeight {
				sourceY = sourceHeight - 1
			}
			source := image.RGBA[(sourceY*sourceWidth+sourceX)*4:]
			alpha := uint16(source[3]) * uint16(opacity) / 255
			if alpha == 0 {
				continue
			}
			blendMotionPixel(dst[(y*width+x)*4:], source, uint8(alpha), blend)
		}
	}
}

// motionTriangleEdgeCovered applies the top-left fill rule used by hardware
// triangle rasterizers. Shared mesh edges then contribute each pixel once,
// preventing translucent texels from being blended twice along grid seams.
func motionTriangleEdgeCovered(value float64, start, end motionPoint, positive bool) bool {
	if positive {
		if value < 0 {
			return false
		}
	} else {
		if value > 0 {
			return false
		}
		start, end = end, start
	}
	if value == 0 {
		dx, dy := end.x-start.x, end.y-start.y
		return dy < 0 || (dy == 0 && dx > 0)
	}
	return true
}

func motionViewportPixels(viewport motionViewport, width, height int) (int, int, int, int) {
	centerX, centerY := float64(width)/2, float64(height)/2
	return int(math.Floor(centerX + viewport.minX)),
		int(math.Floor(centerY + viewport.minY)),
		int(math.Ceil(centerX + viewport.maxX)),
		int(math.Ceil(centerY + viewport.maxY))
}

// motionLookupMotion resolves a motion/ source in the primary snapshot first,
// then in the MultiCache attached snapshots (addEmoteFile links them without
// rewriting the primary tree).
func motionLookupMotion(resource *motionResource, attached []*motionResource, chara, motion string) (*motionResource, map[string]assets.Value, map[string]assets.Value) {
	candidates := make([]*motionResource, 0, len(attached)+1)
	candidates = append(candidates, resource)
	candidates = append(candidates, attached...)
	for _, candidate := range candidates {
		if candidate == nil || candidate.doc == nil {
			continue
		}
		root, _ := candidate.doc.Root.(map[string]assets.Value)
		objects, _ := root["object"].(map[string]assets.Value)
		character, _ := objects[chara].(map[string]assets.Value)
		motions, _ := character["motion"].(map[string]assets.Value)
		if nested, ok := motions[motion].(map[string]assets.Value); ok {
			return candidate, root, nested
		}
	}
	return resource, nil, nil
}

// motionSourceImageAny resolves a src/ reference in the primary snapshot and,
// failing that, in the attached snapshots.
func motionSourceImageAny(resource *motionResource, attached []*motionResource, root map[string]assets.Value, source string) (*motionResource, *assets.Image, float64, float64, error) {
	image, originX, originY, err := motionSourceImage(resource, root, source)
	if err == nil {
		return resource, image, originX, originY, nil
	}
	for _, candidate := range attached {
		if candidate == nil || candidate.doc == nil {
			continue
		}
		candidateRoot, _ := candidate.doc.Root.(map[string]assets.Value)
		image, originX, originY, candidateErr := motionSourceImage(candidate, candidateRoot, source)
		if candidateErr == nil {
			return candidate, image, originX, originY, nil
		}
	}
	return nil, nil, 0, 0, err
}

// motionFrameColors reads the frame content color payload. PSB stores four
// packed RGBA corner colors under content.color (keys "0".."3") or a scalar
// broadcast; the default 0xFF808080 and opaque white are no-ops
// (PlayerInternal.h:1105-1120, SourceCache.cpp:84-147).
func motionFrameColors(content map[string]assets.Value) ([4]uint32, bool) {
	colors := [4]uint32{0xFF808080, 0xFF808080, 0xFF808080, 0xFF808080}
	switch value := content["color"].(type) {
	case map[string]assets.Value:
		for index, key := range []string{"0", "1", "2", "3"} {
			if number, ok := psbNumber(value[key]); ok {
				colors[index] = uint32(int64(number))
			}
		}
	case int64:
		packed := uint32(value)
		colors = [4]uint32{packed, packed, packed, packed}
	case float64:
		packed := uint32(int64(value))
		colors = [4]uint32{packed, packed, packed, packed}
	}
	defaultColors := colors[0] == 0xFF808080 && colors[1] == 0xFF808080 &&
		colors[2] == 0xFF808080 && colors[3] == 0xFF808080
	opaqueWhite := colors[0]&colors[1]&colors[2]&colors[3] == 0xFFFFFFFF
	return colors, !defaultColors && !opaqueWhite
}

func motionFrameImage(resource *motionResource, source string, image *assets.Image, content map[string]assets.Value, blendMode int64) *assets.Image {
	tinted, _ := motionFrameImageTinted(resource, source, image, content, blendMode)
	return tinted
}

// motionFrameImageTinted returns the tinted frame and the identity that
// separates it from other tints of the same source. The GPU motion atlas keys
// uploads by source identity, so it appends this identity to its source key.
func motionFrameImageTinted(resource *motionResource, source string, image *assets.Image, content map[string]assets.Value, blendMode int64) (*assets.Image, string) {
	colors, colored := motionFrameColors(content)
	if !colored || image == nil {
		return image, ""
	}
	if resource.colored == nil {
		resource.colored = make(map[string]*assets.Image)
	}
	key := fmt.Sprintf("\x00%08x%08x%08x%08x/%d", colors[0], colors[1], colors[2], colors[3], blendMode)
	if cached := resource.colored[source+key]; cached != nil && len(cached.RGBA) == len(image.RGBA) {
		return cached, key
	}
	divisor := 255
	if blendMode&0xF0 == 0x10 {
		divisor = 128
	}
	pixels := motionCornerTint(image, colors, divisor)
	tinted := &assets.Image{Width: image.Width, Height: image.Height, RGBA: pixels}
	resource.colored[source+key] = tinted
	return tinted, key
}

// motionCornerTint mirrors applyPackedCornerTintLike_0x6A7518: bilinear corner
// interpolation with a blend-mode-specific divisor, each channel clamped at 255.
func motionCornerTint(image *assets.Image, colors [4]uint32, divisor int) []byte {
	width, height := int(image.Width), int(image.Height)
	pixels := make([]byte, len(image.RGBA))
	if width <= 0 || height <= 0 {
		return pixels
	}
	corner := func(packed uint32) [4]int {
		return [4]int{
			int(packed & 0xFF), int(packed >> 8 & 0xFF),
			int(packed >> 16 & 0xFF), int(packed >> 24 & 0xFF),
		}
	}
	topLeft, topRight := corner(colors[0]), corner(colors[1])
	bottomRight, bottomLeft := corner(colors[2]), corner(colors[3])
	spanX, spanY := max(width-1, 1), max(height-1, 1)
	lerp := func(from, to, pos, span int) int {
		if span <= 0 {
			return from
		}
		return from + pos*(to-from)/span
	}
	for y := range height {
		var rowLeft, rowRight [4]int
		for channel := range 4 {
			rowLeft[channel] = lerp(topLeft[channel], bottomLeft[channel], y, spanY)
			rowRight[channel] = lerp(topRight[channel], bottomRight[channel], y, spanY)
		}
		for x := range width {
			offset := (y*width + x) * 4
			for channel := range 4 {
				tint := lerp(rowLeft[channel], rowRight[channel], x, spanX)
				value := tint * int(image.RGBA[offset+channel]) / divisor
				pixels[offset+channel] = uint8(min(value, 255))
			}
		}
	}
	return pixels
}

// psbMotionKeyframeIndex returns the slot active at tick following the
// reference evaluator: a time before the first entry selects the first entry and
// the final entry is never selected because it only supplies an interpolation
// target.
func psbMotionKeyframeIndex(frames []assets.Value, tick float64) (int, bool) {
	if len(frames) == 0 {
		return 0, false
	}
	selected := 0
	for index, value := range frames {
		frame, ok := value.(map[string]assets.Value)
		if !ok {
			continue
		}
		if psbMotionFloat(frame, "time", 0) > tick {
			if index > 0 {
				selected = index - 1
			}
			break
		}
		selected = index
	}
	if len(frames) > 1 && selected > len(frames)-2 {
		selected = len(frames) - 2
	}
	return selected, true
}

// psbMotionResolvedContent returns the content a frame list draws at tick. The
// active entry's own content is the state (absent properties fall back to the
// format defaults), and an entry whose type is 3 blends toward the next entry
// that carries content. Coordinates ease through the entry's ccc curve, zoom
// through zcc, angle through acc, and slant through scc, while opacity
// interpolates linearly, matching the reference's dual-slot evaluation.
func psbMotionResolvedContent(frames []assets.Value, tick float64, nodeType, coordinateMode int) (map[string]assets.Value, bool) {
	index, ok := psbMotionKeyframeIndex(frames, tick)
	if !ok {
		return nil, false
	}
	active, ok := frames[index].(map[string]assets.Value)
	if !ok {
		return nil, false
	}
	activeContent, _ := active["content"].(map[string]assets.Value)
	if activeContent == nil {
		return nil, false
	}
	content := psbMotionFrameContent(activeContent, nodeType)
	if index+1 >= len(frames) || int(psbMotionFloat(active, "type", 0)) != 3 {
		return content, true
	}
	next, ok := frames[index+1].(map[string]assets.Value)
	if !ok {
		return content, true
	}
	nextFrameContent, _ := next["content"].(map[string]assets.Value)
	if nextFrameContent == nil {
		return content, true
	}
	nextContent := psbMotionFrameContent(nextFrameContent, nodeType)
	ratio := psbMotionBlendRatio(active, next, tick)
	interpolated := make(map[string]assets.Value, len(content)+len(nextContent))
	for key, value := range content {
		interpolated[key] = value
	}
	if _, ok := interpolated["src"]; !ok {
		for _, key := range []string{"src", "ox", "oy"} {
			if value, ok := nextContent[key]; ok {
				interpolated[key] = value
			}
		}
	}
	coordRatio := psbMotionCurveRatio(content["ccc"], ratio)
	zoomRatio := psbMotionCurveRatio(content["zcc"], ratio)
	angleRatio := psbMotionCurveRatio(content["acc"], ratio)
	slantRatio := psbMotionCurveRatio(content["scc"], ratio)
	if values := psbMotionCoordValues(content["coord"]); len(values) > 0 {
		interpolated["coord"] = psbMotionInterpolateCoord(values, psbMotionCoordValues(nextContent["coord"]), coordRatio, content["cp"], coordinateMode)
	} else if values := psbMotionCoordValues(nextContent["coord"]); len(values) > 0 {
		interpolated["coord"] = psbMotionInterpolateCoord([]float64{0, 0, 0}, values, coordRatio, content["cp"], coordinateMode)
	}
	if _, ok := content["opa"]; ok || nextContent["opa"] != nil {
		interpolated["opa"] = psbMotionRoundLikeReference(psbMotionBlendFloat(content["opa"], nextContent["opa"], 255, ratio))
	}
	for _, key := range []string{"zx", "zy", "sx", "sy", "ox", "oy", "fx", "fy"} {
		if content[key] == nil && nextContent[key] == nil {
			continue
		}
		defaultValue := 0.0
		switch key {
		case "zx", "zy":
			defaultValue = 1
		}
		iratio := ratio
		switch key {
		case "zx", "zy":
			iratio = zoomRatio
		case "angle":
			iratio = angleRatio
		case "sx", "sy":
			iratio = slantRatio
		}
		interpolated[key] = psbMotionBlendFloat(content[key], nextContent[key], defaultValue, iratio)
	}
	if content["angle"] != nil || nextContent["angle"] != nil {
		interpolated["angle"] = psbMotionBlendAngle(content["angle"], nextContent["angle"], angleRatio)
	}
	return interpolated, true
}

func psbMotionFrameContent(content map[string]assets.Value, nodeType int) map[string]assets.Value {
	filtered := make(map[string]assets.Value, len(content))
	for key, value := range content {
		filtered[key] = value
	}
	// PSB frames carry a mask field that selects the attributes stored in this
	// frame. A fixture or frame without that field keeps every attribute; the
	// reference source reads typed fields directly and has no mask-based
	// rejection, so an absent mask means "all attributes present".
	if _, hasMask := content["mask"]; !hasMask {
		sourceEnabled := nodeType >= 0 && nodeType < 63 && (uint64(0x1849)&(uint64(1)<<uint(nodeType))) != 0
		// Particle emitters (type 4) keep their template source; they spawn
		// particles instead of drawing it (PlayerRenderItems.cpp:240-250).
		if !sourceEnabled && nodeType != 4 {
			delete(filtered, "src")
			delete(filtered, "icon")
			delete(filtered, "srcList")
		}
		return filtered
	}
	mask := psbMotionInt(content, "mask", 0)
	for key, bit := range map[string]int{
		"ox": 0x001, "oy": 0x001, "coord": 0x002,
		"fx": 0x004, "fy": 0x008, "angle": 0x010,
		"zx": 0x020, "zy": 0x040, "sx": 0x080, "sy": 0x100,
		"color": 0x200, "opa": 0x400, "ccc": 0x800,
		"acc": 0x1000, "zcc": 0x2000, "scc": 0x4000,
		"occ": 0x8000, "motion": 0x80000,
	} {
		if mask&bit == 0 {
			delete(filtered, key)
		}
	}
	sourceEnabled := nodeType >= 0 && nodeType < 63 && (uint64(0x1849)&(uint64(1)<<uint(nodeType))) != 0
	// Particle emitters (type 4) keep their template source: the reference
	// spawns particles for those nodes without drawing the source itself
	// (PlayerRenderItems.cpp:240-250).
	if !sourceEnabled && nodeType != 4 {
		delete(filtered, "src")
		delete(filtered, "icon")
		delete(filtered, "srcList")
	}
	return filtered
}

func psbMotionBlendAngle(value, next assets.Value, ratio float64) float64 {
	from := psbMotionFloatValue(value, 0)
	to := psbMotionFloatValue(next, 0)
	if from >= to {
		if from-to > 180 {
			to += 360
		}
	} else if to-from > 180 {
		to -= 360
	}
	angle := from*(1-ratio) + to*ratio
	if angle < 0 {
		angle += 360
	} else if angle >= 360 {
		angle -= 360
	}
	return angle
}

func psbMotionBlendRatio(active, next map[string]assets.Value, tick float64) float64 {
	start := psbMotionFloat(active, "time", 0)
	end := psbMotionFloat(next, "time", 0)
	if end <= start {
		return 1
	}
	ratio := (tick - start) / (end - start)
	return math.Min(math.Max(ratio, 0), 1)
}

// psbMotionCurveRatio evaluates a frame content curve at ratio, returning ratio
// unchanged when the entry carries no usable curve.
func psbMotionCurveRatio(value assets.Value, ratio float64) float64 {
	curve, ok := value.(map[string]assets.Value)
	if !ok {
		return ratio
	}
	xs := psbMotionList(curve["x"])
	ys := psbMotionList(curve["y"])
	if len(xs) < 2 || len(xs) != len(ys) {
		return ratio
	}
	if xs[0] >= ratio {
		return ys[0]
	}
	if xs[len(xs)-1] <= ratio {
		return ys[len(ys)-1]
	}
	index := 0
	for index < len(xs) && xs[index] < ratio {
		index += 3
	}
	if index < 3 || index >= len(xs) {
		return ratio
	}
	p0, p1, p2, p3 := ys[index-3], ys[index-2], ys[index-1], ys[index]
	u := 1 - ratio
	return u*u*u*p0 + 3*u*u*ratio*p1 + 3*u*ratio*ratio*p2 + ratio*ratio*ratio*p3
}

func psbMotionBlendFloat(value, next assets.Value, fallback, ratio float64) float64 {
	from, to := fallback, fallback
	if value != nil {
		from = psbMotionFloatValue(value, fallback)
	}
	if next != nil {
		to = psbMotionFloatValue(next, fallback)
	}
	return from*(1-ratio) + to*ratio
}

func psbMotionRoundLikeReference(value float64) float64 {
	if value < 0 {
		return math.Ceil(value - 0.5)
	}
	return math.Floor(value + 0.5)
}

func psbMotionInterpolateCoord(from, to []float64, ratio float64, curveValue assets.Value, coordinateMode int) []assets.Value {
	positions := make([]assets.Value, 3)
	var start, end [3]float64
	for i := range 3 {
		if i < len(from) {
			start[i] = from[i]
		}
		if i < len(to) {
			end[i] = to[i]
		}
	}
	if start == end {
		for i := range positions {
			positions[i] = start[i]
		}
		return positions
	}
	cosine, sine, ok := psbMotionControlPointAt(curveValue, ratio)
	if !ok {
		for i := range positions {
			positions[i] = start[i]*(1-ratio) + end[i]*ratio
		}
		return positions
	}
	switch coordinateMode {
	case 0:
		dx, dy := end[0]-start[0], end[1]-start[1]
		positions[0] = start[0] + dx*cosine - dy*sine
		positions[1] = start[1] + dx*sine + dy*cosine
		positions[2] = start[2]*(1-ratio) + end[2]*ratio
	case 1:
		dx, dz := end[0]-start[0], end[2]-start[2]
		positions[0] = start[0] + dx*cosine - dz*sine
		positions[1] = start[1]*(1-ratio) + end[1]*ratio
		positions[2] = start[2] + dz*cosine + dx*sine
	default:
		for i := range positions {
			positions[i] = start[i]
		}
	}
	return positions
}

func psbMotionControlPointAt(value assets.Value, input float64) (float64, float64, bool) {
	curve, ok := value.(map[string]assets.Value)
	if !ok {
		return 1, 0, false
	}
	x, y, knots := psbMotionList(curve["x"]), psbMotionList(curve["y"]), psbMotionList(curve["t"])
	segments, _ := curve["s"].([]assets.Value)
	if len(knots) < 2 || len(x) < 4 || len(y) < 4 {
		return 1, 0, false
	}
	segmentIndex, mainIndex := 0, 0
	for i := 1; i < len(knots); i++ {
		mainIndex += 3
		segmentIndex = i - 1
		if knots[i] >= input {
			break
		}
	}
	if segmentIndex < 0 || segmentIndex >= len(segments) {
		return 1, 0, false
	}
	start, end := knots[segmentIndex], knots[segmentIndex+1]
	local := 0.0
	if end != start {
		local = (input - start) / (end - start)
	}
	param := local
	if segment, ok := segments[segmentIndex].(map[string]assets.Value); ok {
		sx, sy, sp := psbMotionList(segment["x"]), psbMotionList(segment["y"]), psbMotionList(segment["p"])
		if len(sx) > 0 && len(sx) == len(sy) {
			if sx[0] >= local {
				param = sy[0]
			} else if sx[len(sx)-1] <= local {
				param = sy[len(sy)-1]
			} else {
				subIndex := 0
				for i := 1; i < len(sx); i++ {
					if sx[i] >= local {
						subIndex = i - 1
						break
					}
					subIndex = i - 1
				}
				dx := sx[subIndex+1] - sx[subIndex]
				if dx != 0 {
					u := (local - sx[subIndex]) / dx
					p0, p1 := 0.0, 0.0
					if subIndex < len(sp) {
						p0 = sp[subIndex]
					}
					if subIndex+1 < len(sp) {
						p1 = sp[subIndex+1]
					}
					v := 1 - u
					param = dx*dx*((u*u*u-u)*p1+(v*v*v-v)*p0)/6 + u*sy[subIndex+1] + v*sy[subIndex]
				}
			}
		}
	}
	if mainIndex < 3 || mainIndex >= len(x) || mainIndex >= len(y) {
		return 1, 0, false
	}
	u := 1 - param
	cosine := u*u*u*x[mainIndex-3] + 3*u*u*param*x[mainIndex-2] + 3*u*param*param*x[mainIndex-1] + param*param*param*x[mainIndex]
	sine := u*u*u*y[mainIndex-3] + 3*u*u*param*y[mainIndex-2] + 3*u*param*param*y[mainIndex-1] + param*param*param*y[mainIndex]
	return cosine, sine, true
}

// psbMotionCoordValues reads the list and dictionary coordinate encodings
// accepted by the reference frame parser (PlayerInternal.h:942-960).
func psbMotionCoordValues(value assets.Value) []float64 {
	if values := psbMotionList(value); len(values) > 0 {
		return values
	}
	dictionary, ok := value.(map[string]assets.Value)
	if !ok {
		return nil
	}
	return []float64{
		psbMotionFloatValue(dictionary["0"], 0),
		psbMotionFloatValue(dictionary["1"], 0),
		psbMotionFloatValue(dictionary["2"], 0),
	}
}

func motionEvalNodeContext(parent motionRenderContext, content, node map[string]assets.Value, selectionTick, playerTick float64) motionRenderContext {
	ctx := psbMotionContext(parent, content, node)
	ctx.evalNode = node
	ctx.evalTick = selectionTick
	ctx.evalPlayerTick = playerTick
	return ctx
}

func psbMotionContext(parent motionRenderContext, content map[string]assets.Value, layer map[string]assets.Value) motionRenderContext {
	x, y, coordZ := motionNodeCoord(content)
	flags := psbMotionInt(layer, "inheritMask", 0x1FC)
	transformParent := parent
	for transformParent.inheritMask&0x00400000 != 0 && transformParent.inheritParent != nil {
		transformParent = *transformParent.inheritParent
	}
	localTransform := motionTransformFromContent(content)
	transform := inheritMotionTransform(transformParent.transform, localTransform, flags)
	matrix := multiplyMotionMatrix(transformParent.matrix, psbMotionLocalMatrix(layer, content))
	if flags&0x1FC != 0x1FC {
		if transformParent.independentLayerInherit {
			matrix = psbMotionLocalMatrixState(layer, transform)
		} else {
			matrix = multiplyMotionMatrix(transformParent.rootMatrix, psbMotionLocalMatrixState(layer, motionRelativeRootTransform(transform, transformParent.rootTransform, flags)))
		}
	}
	baseOpacity := transformParent.opacity
	if flags&0x400 == 0 {
		if transformParent.independentLayerInherit {
			baseOpacity = 255
		} else {
			baseOpacity = transformParent.rootOpacity
		}
	}
	ctx := motionRenderContext{
		opacity:                 baseOpacity,
		blendMode:               transformParent.blendMode,
		rootX:                   transformParent.rootX,
		rootY:                   transformParent.rootY,
		rootZ:                   transformParent.rootZ,
		childTick:               parent.childTick,
		coordinateMode:          psbMotionInt(layer, "coordinate", 0),
		matrix:                  matrix,
		rootMatrix:              transformParent.rootMatrix,
		transform:               transform,
		rootTransform:           transformParent.rootTransform,
		rootOpacity:             transformParent.rootOpacity,
		independentLayerInherit: transformParent.independentLayerInherit,
		inheritMask:             flags,
		inheritParent:           &parent,
		layerTreeKey:            parent.layerTreeKey,
		viewport:                parent.viewport,
		hasViewport:             parent.hasViewport,
		meshAncestors:           parent.meshAncestors,
		anchorOutputs:           parent.anchorOutputs,
		anchorNamespace:         parent.anchorNamespace,
		zFactor:                 parent.zFactor,
		hasZFactor:              parent.hasZFactor,
	}
	// A sync node copies the parent's state and contributes no transform of
	// its own (PlayerUpdateLayerEval.cpp:856-881).
	sync := false
	if value, ok := psbNumber(content["sync"]); ok && value != 0 {
		sync = true
		ctx.x, ctx.y, ctx.z = transformParent.x, transformParent.y, transformParent.z
		ctx.matrix = transformParent.matrix
		ctx.rootMatrix = transformParent.rootMatrix
		ctx.transform = transformParent.transform
		ctx.rootTransform = transformParent.rootTransform
		ctx.rootOpacity = transformParent.rootOpacity
		ctx.independentLayerInherit = transformParent.independentLayerInherit
	}
	if sync {
		ctx.meshAncestors = parent.meshAncestors
		if psbMotionInt(layer, "type", -1) == 10 {
			if output, exists := parent.anchorOutputs[motionAnchorContextKey(parent.anchorNamespace, layer)]; exists {
				ctx = motionAnchorApplyOutput(ctx, output)
			}
		}
		return ctx
	}
	if transformParent.coordinateMode != 0 {
		// 3D placement: child x and z pass through the parent matrix and y
		// stays literal (PlayerUpdateLayerEval.cpp:912-920).
		worldX := transformParent.matrix.a*x + transformParent.matrix.b*coordZ
		worldZ := transformParent.matrix.c*x + transformParent.matrix.d*coordZ
		ctx.x = worldX + transformParent.x
		ctx.y = y + transformParent.y
		ctx.z = worldZ + transformParent.z
	} else {
		worldX := transformParent.matrix.a*x + transformParent.matrix.b*y
		worldY := transformParent.matrix.c*x + transformParent.matrix.d*y
		ctx.x = worldX + transformParent.x
		ctx.y = worldY + transformParent.y
		ctx.z = transformParent.z + coordZ
	}
	if parent.meshChildDeform != nil {
		net := parent.meshChildDeform
		projectedY := motionProjectedY(ctx)
		positionX, positionY := ctx.x, projectedY
		// Only shape (1) and camera (5) nodes take their position through the
		// parent mesh chain; image nodes deform their vertices instead
		// (PlayerUpdateGeometry.cpp:218-256, 406-420).
		nodeType := psbMotionInt(layer, "type", 0)
		if net.flags&1 != 0 && (nodeType == 1 || nodeType == 5) {
			ctx.x, projectedY = net.deformOffset(ctx.x, projectedY)
			if parent.coordinateMode != 0 {
				ctx.z = projectedY - ctx.y
			} else {
				ctx.y = projectedY - ctx.z
			}
		}
		angleDelta, scaleFactor := net.childAngleScale(positionX, positionY, flags)
		if angleDelta != 0 || scaleFactor != 1 {
			ctx.transform.angle += angleDelta
			if flags&0x20 != 0 {
				ctx.transform.scaleX *= scaleFactor
			}
			if flags&0x40 != 0 {
				ctx.transform.scaleY *= scaleFactor
			}
			if flags&0x1FC == 0x1FC {
				adjusted := make(map[string]assets.Value, len(content)+3)
				for key, value := range content {
					adjusted[key] = value
				}
				if angleDelta != 0 {
					adjusted["angle"] = psbMotionFloat(content, "angle", 0) + angleDelta
				}
				if flags&0x20 != 0 {
					adjusted["zx"] = psbMotionFloat(content, "zx", 1) * scaleFactor
				}
				if flags&0x40 != 0 {
					adjusted["zy"] = psbMotionFloat(content, "zy", 1) * scaleFactor
				}
				ctx.matrix = multiplyMotionMatrix(transformParent.matrix, psbMotionLocalMatrix(layer, adjusted))
			} else {
				if transformParent.independentLayerInherit {
					ctx.matrix = psbMotionLocalMatrixState(layer, ctx.transform)
				} else {
					ctx.matrix = multiplyMotionMatrix(transformParent.rootMatrix, psbMotionLocalMatrixState(layer, motionRelativeRootTransform(ctx.transform, transformParent.rootTransform, flags)))
				}
			}
		}
	}
	if opacity, ok := content["opa"]; ok {
		value := psbMotionFloatValue(opacity, 255)
		if value < 0 {
			value = 0
		}
		if value > 255 {
			value = 255
		}
		ctx.opacity = uint8(float64(baseOpacity) * value / 255)
	}
	if blendMode, ok := psbNumber(content["bm"]); ok {
		ctx.blendMode = int64(blendMode)
	}
	ctx = motionShapeAABBContext(ctx, layer, content)
	if psbMotionInt(layer, "type", -1) == 10 {
		if output, exists := parent.anchorOutputs[motionAnchorContextKey(parent.anchorNamespace, layer)]; exists {
			ctx = motionAnchorApplyOutput(ctx, output)
		}
	}
	return ctx
}

func motionRelativeRootTransform(state, root motionTransformState, flags int) motionTransformState {
	if flags&0x004 != 0 {
		state.flipX = state.flipX != root.flipX
	}
	if flags&0x008 != 0 {
		state.flipY = state.flipY != root.flipY
	}
	if flags&0x010 != 0 {
		state.angle -= root.angle
	}
	if flags&0x020 != 0 && root.scaleX != 0 {
		state.scaleX /= root.scaleX
	}
	if flags&0x040 != 0 && root.scaleY != 0 {
		state.scaleY /= root.scaleY
	}
	if flags&0x080 != 0 {
		state.slantX -= root.slantX
	}
	if flags&0x100 != 0 {
		state.slantY -= root.slantY
	}
	return state
}

func motionShapeAABBContext(ctx motionRenderContext, layer, content map[string]assets.Value) motionRenderContext {
	if psbMotionInt(layer, "type", -1) != 7 {
		return ctx
	}
	ox, oy := psbMotionFloat(content, "ox", 0), psbMotionFloat(content, "oy", 0)
	extentX := (ctx.matrix.a + ctx.matrix.b) * 16
	extentY := (ctx.matrix.c + ctx.matrix.d) * 16
	offsetX := ox*ctx.matrix.a + oy*ctx.matrix.b
	offsetY := ox*ctx.matrix.c + oy*ctx.matrix.d
	centerY := motionProjectedY(ctx)
	clip := motionViewport{
		minX: ctx.x - extentX - offsetX,
		maxX: ctx.x + extentX - offsetX,
		minY: centerY - extentY - offsetY,
		maxY: centerY + extentY - offsetY,
	}
	if clip.minX > clip.maxX {
		clip.minX, clip.maxX = clip.maxX, clip.minX
	}
	if clip.minY > clip.maxY {
		clip.minY, clip.maxY = clip.maxY, clip.minY
	}
	if ctx.hasViewport {
		clip.minX = math.Max(clip.minX, ctx.viewport.minX)
		clip.minY = math.Max(clip.minY, ctx.viewport.minY)
		clip.maxX = math.Min(clip.maxX, ctx.viewport.maxX)
		clip.maxY = math.Min(clip.maxY, ctx.viewport.maxY)
	}
	ctx.viewport = clip
	ctx.hasViewport = true
	return ctx
}

// motionProjectedY applies Player::_zFactor's default value of 1 when a node
// position reaches the 2D render plane (PlayerUpdateGeometry.cpp:270-273).
func motionProjectedY(ctx motionRenderContext) float64 {
	factor := 1.0
	if ctx.hasZFactor {
		factor = ctx.zFactor
	}
	return ctx.y + ctx.z*factor
}

func motionEvalZFactor(eval motionRenderEval) float64 {
	if eval.hasZFactor {
		return eval.zFactor
	}
	return 1
}

func motionTransformFromContent(content map[string]assets.Value) motionTransformState {
	return motionTransformState{
		flipX:  psbMotionInt(content, "fx", 0) != 0,
		flipY:  psbMotionInt(content, "fy", 0) != 0,
		angle:  psbMotionFloat(content, "angle", 0),
		scaleX: psbMotionFloat(content, "zx", 1),
		scaleY: psbMotionFloat(content, "zy", 1),
		slantX: psbMotionFloat(content, "sx", 0),
		slantY: psbMotionFloat(content, "sy", 0),
	}
}

func inheritMotionTransform(parent, local motionTransformState, flags int) motionTransformState {
	if flags&0x004 != 0 {
		local.flipX = local.flipX != parent.flipX
	}
	if flags&0x008 != 0 {
		local.flipY = local.flipY != parent.flipY
	}
	if flags&0x010 != 0 {
		local.angle += parent.angle
	}
	if flags&0x020 != 0 {
		local.scaleX *= parent.scaleX
	}
	if flags&0x040 != 0 {
		local.scaleY *= parent.scaleY
	}
	if flags&0x080 != 0 {
		local.slantX += parent.slantX
	}
	if flags&0x100 != 0 {
		local.slantY += parent.slantY
	}
	return local
}

func psbMotionLocalMatrixState(layer map[string]assets.Value, state motionTransformState) motionMatrix {
	return psbMotionLocalMatrixStateOrder(layer, state, motionDefaultTransformOrder)
}

func psbMotionLocalMatrixStateOrder(layer map[string]assets.Value, state motionTransformState, defaultOrder []int) motionMatrix {
	content := map[string]assets.Value{
		"fx": float64(boolInt(state.flipX)), "fy": float64(boolInt(state.flipY)),
		"angle": state.angle, "zx": state.scaleX, "zy": state.scaleY,
		"sx": state.slantX, "sy": state.slantY,
	}
	return psbMotionLocalMatrixOrder(layer, content, defaultOrder)
}

// motionHitTestScene carries the resource and canvas dimensions the hit test
// needs to reconstruct the ancestor control nets used by the renderer.
type motionHitTestScene struct {
	resource *motionResource
	root     map[string]assets.Value
	width    int
	height   int
}

func hitTestMotionLayer(resource *motionResource, root map[string]assets.Value, layers []assets.Value, tick float64, eval motionRenderEval, label string, x, y float64) (bool, bool) {
	scene := motionHitTestScene{
		resource: resource,
		root:     root,
		width:    psbMotionInt(root["screenSize"], "width", 640),
		height:   psbMotionInt(root["screenSize"], "height", 480),
	}
	zFactor := motionEvalZFactor(eval)
	context := motionRootRenderContext(eval, zFactor)
	return hitTestMotionValue(scene, layers, tick, eval, context, label, x, y)
}

func hitTestMotionValue(scene motionHitTestScene, value assets.Value, tick float64, eval motionRenderEval, parent motionRenderContext, label string, x, y float64) (bool, bool) {
	found, hit := false, false
	switch node := value.(type) {
	case []assets.Value:
		for _, child := range node {
			childFound, childHit := hitTestMotionValue(scene, child, tick, eval, parent, label, x, y)
			if childFound {
				found, hit = true, childHit
			}
		}
	case map[string]assets.Value:
		ctx := parent
		if frames, ok := node["frameList"].([]assets.Value); ok {
			content, visible := psbMotionResolvedContent(frames, motionNodeTick(node, tick, eval), psbMotionInt(node, "type", 0), psbMotionInt(node, "coordinate", 0))
			if !visible {
				return false, false
			}
			ctx = motionEvalNodeContext(parent, content, node, motionNodeTick(node, tick, eval), tick)
			// The reference resolves a label through the node label map and hit
			// tests that node's shape geometry, regardless of the node type
			// (PlayerLayerQuery.cpp:217-273); non-shape containers carry type 0
			// geometry and fall through to the default miss.
			if psbLabel(node) == label {
				insideViewport := !ctx.hasViewport || (x >= ctx.viewport.minX && x <= ctx.viewport.maxX && y >= ctx.viewport.minY && y <= ctx.viewport.maxY)
				found, hit = true, insideViewport && hitMotionShape(psbMotionInt(node, "shape", 0), ctx, content, x, y)
			}
			ctx = motionMeshAncestorContext(scene.resource, scene.root, node, content, eval, ctx, scene.width, scene.height)
		}
		if children, ok := node["children"]; ok {
			childFound, childHit := hitTestMotionValue(scene, children, tick, eval, ctx, label, x, y)
			if childFound {
				found, hit = true, childHit
			}
		}
	}
	return found, hit
}

func hitMotionShape(shape int, ctx motionRenderContext, content map[string]assets.Value, x, y float64) bool {
	// Shape nodes inherit the combination meshes of their ancestors through
	// the node position computed in psbMotionContext, which applies the parent
	// chain once for types 1 and 5 (PlayerUpdateGeometry.cpp:218-256). The
	// shape extent and orientation stay those of the node's own accumulated
	// affine transform.
	centerX, centerY := ctx.x, motionProjectedY(ctx)
	dx, dy := x-centerX, y-centerY
	scaleX := math.Hypot(ctx.matrix.a, ctx.matrix.c)
	scaleY := math.Hypot(ctx.matrix.b, ctx.matrix.d)
	switch shape {
	case 1:
		radius := scaleX * 8
		return dx*dx+dy*dy <= radius*radius
	case 2:
		halfWidth, halfHeight := scaleX*8, scaleY*8
		return dx >= -halfWidth && dx < halfWidth && dy >= -halfHeight && dy < halfHeight
	case 3:
		originX, originY := psbMotionFloat(content, "ox", 0), psbMotionFloat(content, "oy", 0)
		clipX := ctx.matrix.a*originX + ctx.matrix.b*originY
		clipY := ctx.matrix.c*originX + ctx.matrix.d*originY
		corners := [4][2]float64{
			{-8, -8}, {8, -8}, {8, 8}, {-8, 8},
		}
		var vertices [8]float64
		for index, corner := range corners {
			vertices[index*2] = centerX + ctx.matrix.a*corner[0] + ctx.matrix.b*corner[1] - clipX
			vertices[index*2+1] = centerY + ctx.matrix.c*corner[0] + ctx.matrix.d*corner[1] - clipY
		}
		return motionQuadContains(x, y, vertices)
	default:
		return false
	}
}

// motionQuadContains matches HitTestInternal.h type 3: the point must be on
// the same side of all four edges, inclusive, with the winding taken from the
// first three vertices.
func motionQuadContains(x, y float64, vertices [8]float64) bool {
	cross := func(ax, ay, bx, by, px, py float64) float64 {
		return (bx-ax)*(py-ay) - (by-ay)*(px-ax)
	}
	winding := 1.0
	if cross(vertices[0], vertices[1], vertices[2], vertices[3], vertices[4], vertices[5]) < 0 {
		winding = -1
	}
	edges := [4][4]float64{
		{vertices[0], vertices[1], vertices[2], vertices[3]},
		{vertices[2], vertices[3], vertices[4], vertices[5]},
		{vertices[4], vertices[5], vertices[6], vertices[7]},
		{vertices[6], vertices[7], vertices[0], vertices[1]},
	}
	for _, edge := range edges {
		if winding*cross(edge[0], edge[1], edge[2], edge[3], x, y) < 0 {
			return false
		}
	}
	return true
}

func psbMotionLocalMatrix(layer, content map[string]assets.Value) motionMatrix {
	return psbMotionLocalMatrixOrder(layer, content, motionDefaultTransformOrder)
}

// motionDefaultTransformOrder is MotionNode's default [0,1,2,3]
// (MotionNode.h:61, PlayerUpdateLayersInternal.h:272-278).
var motionDefaultTransformOrder = []int{0, 1, 2, 3}

func psbMotionLocalMatrixOrder(layer, content map[string]assets.Value, defaultOrder []int) motionMatrix {
	matrix := identityMotionMatrix
	order := append([]int(nil), defaultOrder...)
	if values := psbMotionList(layer["transformOrder"]); len(values) > 0 {
		for i := 0; i < len(values) && i < len(order); i++ {
			order[i] = int(values[i])
		}
	}
	zoomX := psbMotionFloat(content, "zx", 1)
	zoomY := psbMotionFloat(content, "zy", 1)
	angle := psbMotionFloat(content, "angle", 0) * math.Pi / 180
	slantX := psbMotionFloat(content, "sx", 0)
	slantY := psbMotionFloat(content, "sy", 0)
	for _, transform := range order {
		switch transform {
		case 0:
			if psbMotionInt(content, "fx", 0) != 0 {
				matrix.a = -matrix.a
				matrix.b = -matrix.b
			}
			if psbMotionInt(content, "fy", 0) != 0 {
				matrix.c = -matrix.c
				matrix.d = -matrix.d
			}
		case 1:
			if angle != 0 {
				cosine, sine := math.Cos(angle), math.Sin(angle)
				matrix = motionMatrix{
					a: cosine*matrix.a - sine*matrix.c,
					b: cosine*matrix.b - sine*matrix.d,
					c: sine*matrix.a + cosine*matrix.c,
					d: sine*matrix.b + cosine*matrix.d,
				}
			}
		case 2:
			matrix.a *= zoomX
			matrix.b *= zoomX
			matrix.c *= zoomY
			matrix.d *= zoomY
		case 3:
			if slantX != 0 || slantY != 0 {
				matrix = motionMatrix{
					a: matrix.a + slantX*matrix.c,
					b: matrix.b + slantX*matrix.d,
					c: slantY*matrix.a + matrix.c,
					d: slantY*matrix.b + matrix.d,
				}
			}
		}
	}
	return matrix
}

func multiplyMotionMatrix(a, b motionMatrix) motionMatrix {
	return motionMatrix{
		a: a.a*b.a + a.b*b.c,
		b: a.a*b.b + a.b*b.d,
		c: a.c*b.a + a.d*b.c,
		d: a.c*b.b + a.d*b.d,
	}
}

// invertMotionMatrix returns the 2x2 inverse, or the identity for a
// degenerate matrix.
func invertMotionMatrix(matrix motionMatrix) motionMatrix {
	det := matrix.a*matrix.d - matrix.b*matrix.c
	if math.Abs(det) < 1e-12 {
		return identityMotionMatrix
	}
	inverse := 1 / det
	return motionMatrix{
		a: matrix.d * inverse,
		b: -matrix.b * inverse,
		c: -matrix.c * inverse,
		d: matrix.a * inverse,
	}
}

// motionCameraAccumulated is the subset of MotionNode::AccumulatedState that
// the camera-node pass reads. Positions are world-space after the parent chain
// has been composed exactly as PlayerUpdateLayerEval.cpp:899-930 does.
type motionCameraAccumulated struct {
	x, y, z        float64
	matrix         motionMatrix
	rootMatrix     motionMatrix
	transform      motionTransformState
	rootTransform  motionTransformState
	inheritMask    int
	inheritParent  *motionCameraAccumulated
	active         bool
	coordinateMode int
}

type motionCameraPhaseState struct {
	target                      motionCameraAccumulated
	hasMinX, hasMaxX, hasTrackX bool
	hasMinY, hasMaxY, hasTrackY bool
	hasMinZ, hasMaxZ, hasTrackZ bool
	minX, maxX, trackX          float64
	minY, maxY, trackY          float64
	minZ, maxZ, trackZ          float64
	hasCamera                   bool
	cameraX, cameraY, cameraZ   float64
}

// motionCameraOffset mirrors the camera-constraint and camera-node phases:
// type-9 constraints translate non-root nodes when no active camera node is
// present; otherwise the first active type-5 node supplies the render offset.
func motionCameraOffset(layers assets.Value, tick float64, eval motionRenderEval, drawMatrix motionDrawMatrix) (float64, float64, bool) {
	phase := motionCameraPhase(layers, tick, eval)
	if phase.hasCamera {
		rootY := phase.target.y*motionEvalZFactor(eval) + phase.target.z
		cameraY := phase.cameraY*motionEvalZFactor(eval) + phase.cameraZ
		dx := phase.target.x - phase.cameraX
		dy := rootY - cameraY
		// The reference truncates value+0.5 and stores the result as float32.
		return float64(int(drawMatrix.a*dx + drawMatrix.b*dy + 0.5)),
			float64(int(drawMatrix.c*dx + drawMatrix.d*dy + 0.5)), true
	}
	if eval.emoteMode {
		return 0, 0, false
	}
	offsetX, offsetY, offsetZ := motionCameraConstraintOffset(phase)
	if offsetX == 0 && offsetY == 0 && offsetZ == 0 {
		return 0, 0, false
	}
	projectedY := offsetY*motionEvalZFactor(eval) + offsetZ
	return drawMatrix.a*offsetX + drawMatrix.b*projectedY,
		drawMatrix.c*offsetX + drawMatrix.d*projectedY, true
}

func motionCameraPhase(layers assets.Value, tick float64, eval motionRenderEval) motionCameraPhaseState {
	root := motionCameraAccumulated{matrix: identityMotionMatrix, rootMatrix: identityMotionMatrix, transform: identityMotionTransform, rootTransform: identityMotionTransform, active: true, coordinateMode: eval.rootCoordinateMode}
	if eval.hasRootState {
		state := eval.rootState
		root.x, root.y, root.z = state.x, state.y, state.z
		root.matrix, root.rootMatrix = state.matrix, state.rootMatrix
		root.transform, root.rootTransform = state.transform, state.rootTransform
		root.coordinateMode = state.coordinateMode
	}
	phase := motionCameraPhaseState{target: root}
	motionCameraOffsetValue(layers, tick, eval, root, &phase)
	return phase
}

func motionCameraConstraintOffset(phase motionCameraPhaseState) (float64, float64, float64) {
	selectAxis := func(hasTrack, hasMax, hasMin bool, track, maxValue, minValue float64) float64 {
		if hasTrack {
			return track
		}
		if hasMax {
			return maxValue
		}
		if hasMin {
			return minValue
		}
		return 0
	}
	return selectAxis(phase.hasTrackX, phase.hasMaxX, phase.hasMinX, phase.trackX, phase.maxX, phase.minX),
		selectAxis(phase.hasTrackY, phase.hasMaxY, phase.hasMinY, phase.trackY, phase.maxY, phase.minY),
		selectAxis(phase.hasTrackZ, phase.hasMaxZ, phase.hasMinZ, phase.trackZ, phase.maxZ, phase.minZ)
}

// motionCameraDrawMatrix resolves the motion's first active camera node and
// folds its offset into the draw-matrix translation, which is where the
// reference applies _cameraOffset (PlayerRenderItems.cpp:1174-1215).
func motionCameraDrawMatrix(resource *motionResource, chara, motion string, tick float64, params []float64, freezeBody bool, maskMode, completion int64, attached []*motionResource, drawMatrix motionDrawMatrix) motionDrawMatrix {
	return motionCameraDrawMatrixZFactor(resource, chara, motion, tick, params, freezeBody, maskMode, completion, 1, attached, drawMatrix)
}

func motionCameraDrawMatrixZFactor(resource *motionResource, chara, motion string, tick float64, params []float64, freezeBody bool, maskMode, completion int64, zFactor float64, attached []*motionResource, drawMatrix motionDrawMatrix) motionDrawMatrix {
	return motionCameraDrawMatrixZFactorInherit(resource, chara, motion, tick, params, freezeBody, maskMode, completion, zFactor, attached, drawMatrix, false)
}

func motionCameraDrawMatrixZFactorInherit(resource *motionResource, chara, motion string, tick float64, params []float64, freezeBody bool, maskMode, completion int64, zFactor float64, attached []*motionResource, drawMatrix motionDrawMatrix, independentLayerInherit bool, options ...motionRenderOptions) motionDrawMatrix {
	layers := motionLayerList(resource, chara, motion)
	if len(layers) == 0 {
		return drawMatrix
	}
	renderOptions := motionRenderOptionsValue(options)
	eval := motionRenderEval{
		params: params, freezeBody: freezeBody, maskMode: maskMode,
		completion: completion, attached: attached,
		zFactor: zFactor, hasZFactor: true, independentLayerInherit: independentLayerInherit,
		rootCoordinateMode:     renderOptions.rootCoordinateMode,
		emoteMode:              renderOptions.emoteMode,
		parentFrameLoopTime:    renderOptions.parentFrameLoopTime,
		hasParentFrameLoopTime: renderOptions.hasParentFrameLoopTime,
		frameLastTime:          renderOptions.frameLastTime,
		anyTimelinePlaying:     renderOptions.anyTimelinePlaying,
		rootState:              renderOptions.rootState,
		hasRootState:           renderOptions.hasRootState,
		anchorOutputs:          renderOptions.anchorOutputs,
		preview:                renderOptions.preview,
		stats:                  renderOptions.stats,
	}
	offsetX, offsetY, ok := motionCameraOffset(layers, tick, eval, drawMatrix)
	if !ok {
		return drawMatrix
	}
	return motionDrawMatrixWithCameraOffset(drawMatrix, offsetX, offsetY)
}

// motionLayerList returns the motion's PSB "layer" array. The reference
// flattens this array plus each node's "children" under a synthetic root
// (NodeTree.cpp:333-378).
func motionLayerList(resource *motionResource, chara, motion string) []assets.Value {
	if resource == nil || resource.doc == nil {
		return nil
	}
	return motionLayerListFromDocument(resource.doc, chara, motion)
}

func motionLayerListFromDocument(doc *assets.Document, chara, motion string) []assets.Value {
	if doc == nil {
		return nil
	}
	root, ok := doc.Root.(map[string]assets.Value)
	if !ok {
		return nil
	}
	objects, _ := root["object"].(map[string]assets.Value)
	character, _ := objects[chara].(map[string]assets.Value)
	motions, _ := character["motion"].(map[string]assets.Value)
	motionValue, _ := motions[motion].(map[string]assets.Value)
	return motionMotionLayerListFromDocument(doc, motionValue)
}

func motionMotionLayerList(resource *motionResource, motionValue map[string]assets.Value) []assets.Value {
	if resource == nil || resource.doc == nil || motionValue == nil {
		return nil
	}
	return motionMotionLayerListFromDocument(resource.doc, motionValue)
}

func motionMotionLayerListFromDocument(doc *assets.Document, motionValue map[string]assets.Value) []assets.Value {
	if doc == nil || motionValue == nil {
		return nil
	}
	rootLayers := motionSnapshotLayerList(doc)
	layers, _ := motionValue["layer"].([]assets.Value)
	directLayers := make([]assets.Value, 0, len(layers))
	for _, item := range layers {
		layer, ok := item.(map[string]assets.Value)
		if !ok {
			continue
		}
		if _, labeled := motionPSBString(layer, "label", "name", "id"); labeled {
			directLayers = append(directLayers, layer)
		}
	}
	if len(directLayers) > 0 {
		return directLayers
	}
	if resolved := motionResolveLayerReferences(layers, rootLayers); len(resolved) > 0 {
		return resolved
	}
	if priorities, ok := motionValue["priority"].([]assets.Value); ok {
		for _, item := range priorities {
			priority, ok := item.(map[string]assets.Value)
			if !ok {
				continue
			}
			content, _ := priority["content"].([]assets.Value)
			if resolved := motionResolveLayerReferences(content, rootLayers); len(resolved) > 0 {
				return resolved
			}
		}
	}
	return rootLayers
}

func collectMotionLayerNames(value assets.Value, names map[string]struct{}) {
	switch node := value.(type) {
	case []assets.Value:
		for _, child := range node {
			collectMotionLayerNames(child, names)
		}
	case map[string]assets.Value:
		if label := psbLabel(node); label != "" {
			names[label] = struct{}{}
		}
		if child, found := node["children"]; found {
			collectMotionLayerNames(child, names)
		}
	}
}

func findMotionLayerByLabel(value assets.Value, label string) map[string]assets.Value {
	return findMotionLayerByLabelInDocument(nil, []assets.Value{value}, label)
}

func findMotionLayerByLabelInDocument(doc *assets.Document, value assets.Value, label string) map[string]assets.Value {
	var found map[string]assets.Value
	layers := motionSnapshotLayerList(doc)
	var visit func(assets.Value)
	visit = func(value assets.Value) {
		switch node := value.(type) {
		case []assets.Value:
			for _, child := range node {
				visit(child)
			}
		case map[string]assets.Value:
			if psbLabel(node) == label {
				found = node
			}
			if children, exists := node["children"].([]assets.Value); exists {
				for _, child := range motionResolveLayerReferences(children, layers) {
					visit(child)
				}
			}
		}
	}
	visit(value)
	return found
}

func motionCameraOffsetValue(value assets.Value, tick float64, eval motionRenderEval, parent motionCameraAccumulated, phase *motionCameraPhaseState) {
	switch node := value.(type) {
	case []assets.Value:
		for _, child := range node {
			motionCameraOffsetValue(child, tick, eval, parent, phase)
		}
	case map[string]assets.Value:
		acc := parent
		transformParent := parent
		for transformParent.inheritMask&0x00400000 != 0 && transformParent.inheritParent != nil {
			transformParent = *transformParent.inheritParent
		}
		acc.x, acc.y, acc.z = parent.x, parent.y, parent.z
		acc.matrix = parent.matrix
		acc.rootMatrix = transformParent.rootMatrix
		acc.rootTransform = transformParent.rootTransform
		acc.coordinateMode = parent.coordinateMode
		acc.inheritMask = psbMotionInt(node, "inheritMask", 0x1FC)
		// A node only stays active when it resolves visible content and every
		// ancestor is active (PlayerUpdateLayerEval.cpp:875-880).
		acc.active = false
		if frameList, ok := node["frameList"].([]assets.Value); ok {
			if content, visible := psbMotionResolvedContent(frameList, motionNodeTick(node, tick, eval), psbMotionInt(node, "type", 0), psbMotionInt(node, "coordinate", 0)); visible {
				// A sync node copies the parent state and stays inactive
				// (PlayerUpdateLayerEval.cpp:857-880).
				sync, hasSync := psbNumber(content["sync"])
				if hasSync && sync != 0 {
					acc.x, acc.y, acc.z = transformParent.x, transformParent.y, transformParent.z
					acc.matrix = transformParent.matrix
					acc.transform = transformParent.transform
					acc.rootTransform = transformParent.rootTransform
				} else {
					x, y, z := motionNodeCoord(content)
					flags := psbMotionInt(node, "inheritMask", 0x1FC)
					acc.transform = inheritMotionTransform(transformParent.transform, motionTransformFromContent(content), flags)
					acc.inheritMask = flags
					acc.inheritParent = &parent
					if flags&0x1FC == 0x1FC {
						acc.matrix = multiplyMotionMatrix(transformParent.matrix, psbMotionLocalMatrixOrder(node, content, motionDefaultTransformOrder))
					} else if eval.independentLayerInherit {
						acc.matrix = psbMotionLocalMatrixStateOrder(node, acc.transform, motionDefaultTransformOrder)
					} else {
						acc.matrix = multiplyMotionMatrix(transformParent.rootMatrix, psbMotionLocalMatrixStateOrder(node, motionRelativeRootTransform(acc.transform, transformParent.rootTransform, flags), motionDefaultTransformOrder))
					}
					if transformParent.coordinateMode != 0 {
						worldX := transformParent.matrix.a*x + transformParent.matrix.b*z
						worldZ := transformParent.matrix.c*x + transformParent.matrix.d*z
						acc.x = worldX + transformParent.x
						acc.y = y + transformParent.y
						acc.z = worldZ + transformParent.z
					} else {
						worldX := transformParent.matrix.a*x + transformParent.matrix.b*y
						worldY := transformParent.matrix.c*x + transformParent.matrix.d*y
						acc.x = worldX + transformParent.x
						acc.y = worldY + transformParent.y
						acc.z = z + transformParent.z
					}
					acc.active = parent.active
				}
			}
		}
		acc.coordinateMode = psbMotionInt(node, "coordinate", 0)
		if psbMotionInt(node, "type", -1) == 9 && acc.active {
			motionAccumulateCameraConstraint(phase, node, acc)
		}
		if psbMotionInt(node, "type", -1) == 5 && acc.active && !phase.hasCamera {
			phase.hasCamera = true
			phase.cameraX, phase.cameraY, phase.cameraZ = acc.x, acc.y, acc.z
		}
		if children, ok := node["children"]; ok {
			motionCameraOffsetValue(children, tick, eval, acc, phase)
		}
	}
}

func motionAccumulateCameraConstraint(phase *motionCameraPhaseState, node map[string]assets.Value, constraint motionCameraAccumulated) {
	typeCode := psbMotionInt(node, "anchor", 0)
	if constraint.transform.flipX {
		if typeCode == 0 {
			typeCode = 2
		} else if typeCode == 2 {
			typeCode = 0
		}
	}
	if constraint.transform.flipY {
		if typeCode == 3 {
			typeCode = 5
		} else if typeCode == 5 {
			typeCode = 3
		}
	}
	accumulateMin := func(delta float64, value *float64, found *bool) {
		if delta < 0 && (!*found || delta < *value) {
			*value, *found = delta, true
		}
	}
	accumulateMax := func(delta float64, value *float64, found *bool) {
		if delta > 0 && (!*found || delta > *value) {
			*value, *found = delta, true
		}
	}
	switch typeCode {
	case 0:
		accumulateMin(phase.target.x-constraint.x, &phase.minX, &phase.hasMinX)
	case 1:
		phase.trackX, phase.hasTrackX = phase.target.x-constraint.x, true
	case 2:
		accumulateMax(phase.target.x-constraint.x, &phase.maxX, &phase.hasMaxX)
	case 3:
		accumulateMin(phase.target.y-constraint.y, &phase.minY, &phase.hasMinY)
	case 4:
		phase.trackY, phase.hasTrackY = phase.target.y-constraint.y, true
	case 5:
		accumulateMax(phase.target.y-constraint.y, &phase.maxY, &phase.hasMaxY)
	case 6:
		accumulateMin(phase.target.z-constraint.z, &phase.minZ, &phase.hasMinZ)
	case 7:
		phase.trackZ, phase.hasTrackZ = phase.target.z-constraint.z, true
	case 8:
		accumulateMax(phase.target.z-constraint.z, &phase.maxZ, &phase.hasMaxZ)
	}
}

// motionNodeCoord reads "coord" [x,y,z] with the reference's list-or-dict
// fallback (PlayerInternal.h:942-960).
func motionNodeCoord(content map[string]assets.Value) (float64, float64, float64) {
	values := psbMotionCoordValues(content["coord"])
	var x, y, z float64
	if len(values) > 0 {
		x = values[0]
	}
	if len(values) > 1 {
		y = values[1]
	}
	if len(values) > 2 {
		z = values[2]
	}
	return x, y, z
}

func motionSourceImage(resource *motionResource, root map[string]assets.Value, source string) (*assets.Image, float64, float64, error) {
	if resource.images == nil {
		resource.images = make(map[string]*assets.Image)
	}
	parts := strings.Split(strings.TrimPrefix(source, "src/"), "/")
	if len(parts) != 2 {
		return nil, 0, 0, fmt.Errorf("unsupported motion image source %q", source)
	}
	key := parts[0] + "/" + parts[1]
	if image := resource.images[key]; image != nil {
		metadata, err := motionSourceMetadata(root, parts[0], parts[1])
		if err != nil {
			return nil, 0, 0, err
		}
		return image, psbMotionFloat(metadata, "originX", 0), psbMotionFloat(metadata, "originY", 0), nil
	}
	metadata, err := motionSourceMetadata(root, parts[0], parts[1])
	if err != nil {
		return nil, 0, 0, err
	}
	resourceValue, ok := metadata["pixel"].(assets.Resource)
	if !ok {
		return nil, 0, 0, fmt.Errorf("motion image %q has no pixel resource", source)
	}
	if resourceValue.Offset < 0 || resourceValue.Length < 0 || resourceValue.Offset > len(resource.data) || resourceValue.Length > len(resource.data)-resourceValue.Offset {
		return nil, 0, 0, fmt.Errorf("motion image %q resource range is invalid", source)
	}
	width := psbMotionInt(metadata, "width", 0)
	height := psbMotionInt(metadata, "height", 0)
	var image *assets.Image
	palette, hasPalette, paletteErr := motionPalette(resource.data, metadata)
	if paletteErr != nil {
		return nil, 0, 0, fmt.Errorf("decode motion image %q palette: %w", source, paletteErr)
	}
	compressed := strings.EqualFold(psbMotionString(metadata, "compress"), "RL")
	imageData := resource.data[resourceValue.Offset : resourceValue.Offset+resourceValue.Length]
	if hasPalette {
		var indices []byte
		if compressed {
			indices, err = assets.DecodePSBRLEIndices(imageData, width, height)
		} else {
			indices = append([]byte(nil), imageData...)
			if len(indices) != width*height {
				return nil, 0, 0, fmt.Errorf("indexed motion image %q size %d, want %d", source, len(indices), width*height)
			}
		}
		image, err = decodePSBIndexed(indices, palette, width, height)
	} else if compressed {
		image, err = assets.DecodePSBRLE(imageData, width, height)
	} else {
		image, err = decodePSBRaw(imageData, width, height)
	}
	if err != nil {
		return nil, 0, 0, fmt.Errorf("decode motion image %q: %w", source, err)
	}
	resource.images[key] = image
	return image, psbMotionFloat(metadata, "originX", 0), psbMotionFloat(metadata, "originY", 0), nil
}
func motionSourceOpaque(resource *motionResource, source string, image *assets.Image) bool {
	if resource.opaque == nil {
		resource.opaque = make(map[string]bool)
	}
	if opaque, ok := resource.opaque[source]; ok {
		return opaque
	}
	opaque := true
	for offset := 3; offset < len(image.RGBA); offset += 4 {
		if image.RGBA[offset] != 255 {
			opaque = false
			break
		}
	}
	resource.opaque[source] = opaque
	return opaque
}

func motionPremultipliedImage(resource *motionResource, source string, image *assets.Image) []byte {
	if resource.premultiplied == nil {
		resource.premultiplied = make(map[string][]byte)
	}
	if pixels := resource.premultiplied[source]; len(pixels) == len(image.RGBA) {
		return pixels
	}
	pixels := make([]byte, len(image.RGBA))
	for offset := 0; offset+3 < len(image.RGBA); offset += 4 {
		alpha := uint16(image.RGBA[offset+3])
		pixels[offset+3] = byte(alpha)
		pixels[offset] = byte((uint16(image.RGBA[offset])*alpha + 127) / 255)
		pixels[offset+1] = byte((uint16(image.RGBA[offset+1])*alpha + 127) / 255)
		pixels[offset+2] = byte((uint16(image.RGBA[offset+2])*alpha + 127) / 255)
	}
	resource.premultiplied[source] = pixels
	return pixels
}

func motionPalette(data []byte, metadata map[string]assets.Value) ([]byte, bool, error) {
	paletteValue, ok := metadata["pal"].(assets.Resource)
	if !ok {
		return nil, false, nil
	}
	if paletteValue.Offset < 0 || paletteValue.Length < 0 || paletteValue.Offset > len(data) || paletteValue.Length > len(data)-paletteValue.Offset {
		return nil, false, fmt.Errorf("palette resource range is invalid")
	}
	palette := data[paletteValue.Offset : paletteValue.Offset+paletteValue.Length]
	if len(palette) == 0 || len(palette)%4 != 0 {
		return nil, false, fmt.Errorf("palette size %d is not a multiple of four", len(palette))
	}
	return palette, true, nil
}

func decodePSBIndexed(indices, palette []byte, width, height int) (*assets.Image, error) {
	if width <= 0 || height <= 0 || len(indices) != width*height {
		return nil, fmt.Errorf("indexed motion image size %d, want %d", len(indices), width*height)
	}
	pixels := make([]byte, len(indices)*4)
	for i, index := range indices {
		offset := int(index) * 4
		if offset+4 > len(palette) {
			return nil, fmt.Errorf("palette index %d is outside %d entries", index, len(palette)/4)
		}
		pixels[i*4] = palette[offset+2]
		pixels[i*4+1] = palette[offset+1]
		pixels[i*4+2] = palette[offset]
		pixels[i*4+3] = palette[offset+3]
	}
	return &assets.Image{Width: uint32(width), Height: uint32(height), RGBA: pixels}, nil
}

func motionSourceMetadata(root map[string]assets.Value, part, name string) (map[string]assets.Value, error) {
	source, _ := root["source"].(map[string]assets.Value)
	partValue, _ := source[part].(map[string]assets.Value)
	icons, _ := partValue["icon"].(map[string]assets.Value)
	metadata, _ := icons[name].(map[string]assets.Value)
	if metadata == nil {
		return nil, fmt.Errorf("motion image source %s/%s is missing", part, name)
	}
	return metadata, nil
}

func decodePSBRaw(data []byte, width, height int) (*assets.Image, error) {
	want := width * height * 4
	if width <= 0 || height <= 0 || len(data) != want {
		return nil, fmt.Errorf("raw motion image size %d, want %d", len(data), want)
	}
	pixels := make([]byte, 0, want)
	for offset := 0; offset < len(data); offset += 4 {
		pixels = append(pixels, data[offset+2], data[offset+1], data[offset], data[offset+3])
	}
	return &assets.Image{Width: uint32(width), Height: uint32(height), RGBA: pixels}, nil
}

type motionPremultSample struct {
	red, green, blue, alpha uint32
}

func motionBilinearRGBA(source, premultiplied []byte, width, height int, x, y float64) [4]byte {
	var pixel [4]byte
	sample := motionBilinearPremult(source, premultiplied, width, height, x, y)
	if sample.alpha == 0 {
		return pixel
	}
	pixel[3] = byte(sample.alpha)
	pixel[0] = byte(min((sample.red*255+sample.alpha/2)/sample.alpha, uint32(255)))
	pixel[1] = byte(min((sample.green*255+sample.alpha/2)/sample.alpha, uint32(255)))
	pixel[2] = byte(min((sample.blue*255+sample.alpha/2)/sample.alpha, uint32(255)))
	return pixel
}

func motionBilinearPremult(source, premultiplied []byte, width, height int, x, y float64) motionPremultSample {
	var sample motionPremultSample
	if width <= 0 || height <= 0 {
		return sample
	}
	x0, y0 := int(x), int(y)
	fx, fy := x-float64(x0), y-float64(y0)
	if x < 0 {
		x0--
		fx++
	}
	if y < 0 {
		y0--
		fy++
	}
	if x0 < 0 {
		x0 = 0
	} else if x0 >= width {
		x0 = width - 1
	}
	if y0 < 0 {
		y0 = 0
	} else if y0 >= height {
		y0 = height - 1
	}
	x1, y1 := x0+1, y0+1
	if x1 >= width {
		x1 = width - 1
		fx = 0
	}
	if y1 >= height {
		y1 = height - 1
		fy = 0
	}
	if fx < 0 {
		fx = 0
	} else if fx > 1 {
		fx = 1
	}
	if fy < 0 {
		fy = 0
	} else if fy > 1 {
		fy = 1
	}
	wx := uint64(fx*256 + 0.5)
	wy := uint64(fy*256 + 0.5)
	if wx > 256 {
		wx = 256
	}
	if wy > 256 {
		wy = 256
	}
	ix, iy := uint64(256)-wx, uint64(256)-wy
	w00, w10 := ix*iy, wx*iy
	w01, w11 := ix*wy, wx*wy
	i00 := (y0*width + x0) * 4
	i10 := (y0*width + x1) * 4
	i01 := (y1*width + x0) * 4
	i11 := (y1*width + x1) * 4
	a00, a10 := uint64(source[i00+3]), uint64(source[i10+3])
	a01, a11 := uint64(source[i01+3]), uint64(source[i11+3])
	alpha := a00*w00 + a10*w10 + a01*w01 + a11*w11
	sample.alpha = uint32((alpha + 32768) / 65536)
	if alpha == 0 {
		return sample
	}
	var red, green, blue uint64
	if len(premultiplied) == len(source) {
		red = uint64(premultiplied[i00])*w00 +
			uint64(premultiplied[i10])*w10 +
			uint64(premultiplied[i01])*w01 +
			uint64(premultiplied[i11])*w11
		green = uint64(premultiplied[i00+1])*w00 +
			uint64(premultiplied[i10+1])*w10 +
			uint64(premultiplied[i01+1])*w01 +
			uint64(premultiplied[i11+1])*w11
		blue = uint64(premultiplied[i00+2])*w00 +
			uint64(premultiplied[i10+2])*w10 +
			uint64(premultiplied[i01+2])*w01 +
			uint64(premultiplied[i11+2])*w11
	} else {
		red = uint64(source[i00])*a00/255*w00 +
			uint64(source[i10])*a10/255*w10 +
			uint64(source[i01])*a01/255*w01 +
			uint64(source[i11])*a11/255*w11
		green = uint64(source[i00+1])*a00/255*w00 +
			uint64(source[i10+1])*a10/255*w10 +
			uint64(source[i01+1])*a01/255*w01 +
			uint64(source[i11+1])*a11/255*w11
		blue = uint64(source[i00+2])*a00/255*w00 +
			uint64(source[i10+2])*a10/255*w10 +
			uint64(source[i01+2])*a01/255*w01 +
			uint64(source[i11+2])*a11/255*w11
	}
	sample.red = uint32((red + 32768) / 65536)
	sample.green = uint32((green + 32768) / 65536)
	sample.blue = uint32((blue + 32768) / 65536)
	return sample
}

func blendMotionImage(dst []byte, width, height int, src *assets.Image, originX, originY float64, filter int64, premultiplied []byte, opaque bool, ctx motionRenderContext) error {
	matrix := ctx.matrix
	determinant := matrix.a*matrix.d - matrix.b*matrix.c
	if math.Abs(determinant) < 1e-9 {
		return nil
	}
	centerX := float64(width)/2 + ctx.x
	centerY := float64(height)/2 + motionProjectedY(ctx)
	blend := motionBlendMode(ctx.blendMode)
	if !ctx.hasViewport && matrix.a == 1 && matrix.b == 0 && matrix.c == 0 && matrix.d == 1 {
		offsetXFloat := centerX - originX
		offsetYFloat := centerY - originY
		offsetX, offsetY := int(math.Round(offsetXFloat)), int(math.Round(offsetYFloat))
		if offsetXFloat == float64(offsetX) && offsetYFloat == float64(offsetY) {
			sourceLeft := maxInt(0, -offsetX)
			sourceTop := maxInt(0, -offsetY)
			sourceRight := minInt(int(src.Width), width-offsetX)
			sourceBottom := minInt(int(src.Height), height-offsetY)
			if sourceLeft < sourceRight && sourceTop < sourceBottom {
				if opaque && ctx.opacity == 255 && blend == MotionBlendAlpha {
					rowPixels := sourceRight - sourceLeft
					for sourceY := sourceTop; sourceY < sourceBottom; sourceY++ {
						sourceRow := src.RGBA[(sourceY*int(src.Width)+sourceLeft)*4:]
						destinationRow := dst[((sourceY+offsetY)*width+sourceLeft+offsetX)*4:]
						copy(destinationRow[:rowPixels*4], sourceRow[:rowPixels*4])
					}
					return nil
				}
				for sourceY := sourceTop; sourceY < sourceBottom; sourceY++ {
					sourceRow := src.RGBA[(sourceY*int(src.Width)+sourceLeft)*4:]
					destinationRow := dst[((sourceY+offsetY)*width+sourceLeft+offsetX)*4:]
					for sourceX := range sourceRight - sourceLeft {
						sourcePixel := sourceRow[sourceX*4:]
						if sourcePixel[3] == 0 || ctx.opacity == 0 {
							continue
						}
						alpha := uint16(sourcePixel[3]) * uint16(ctx.opacity) / 255
						blendMotionPixel(destinationRow[sourceX*4:], sourcePixel, uint8(alpha), blend)
					}
				}
				return nil
			}
		}
	}
	corners := [][2]float64{
		{-0.5 - originX, -0.5 - originY},
		{float64(src.Width) - 0.5 - originX, -0.5 - originY},
		{float64(src.Width) - 0.5 - originX, float64(src.Height) - 0.5 - originY},
		{-0.5 - originX, float64(src.Height) - 0.5 - originY},
	}
	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	for _, corner := range corners {
		x := centerX + matrix.a*corner[0] + matrix.b*corner[1]
		y := centerY + matrix.c*corner[0] + matrix.d*corner[1]
		minX = math.Min(minX, x)
		minY = math.Min(minY, y)
		maxX = math.Max(maxX, x)
		maxY = math.Max(maxY, y)
	}
	left := maxInt(0, int(math.Floor(minX)))
	top := maxInt(0, int(math.Floor(minY)))
	right := minInt(width, int(math.Ceil(maxX)))
	bottom := minInt(height, int(math.Ceil(maxY)))
	if ctx.hasViewport {
		clipLeft, clipTop, clipRight, clipBottom := motionViewportPixels(ctx.viewport, width, height)
		left, top = maxInt(left, clipLeft), maxInt(top, clipTop)
		right, bottom = minInt(right, clipRight), minInt(bottom, clipBottom)
	}
	if left >= right || top >= bottom {
		return nil
	}
	inverse := motionMatrix{
		a: matrix.d / determinant,
		b: -matrix.b / determinant,
		c: -matrix.c / determinant,
		d: matrix.a / determinant,
	}
	rows := bottom - top
	workers := 1
	if rows >= 96 {
		workers = min(goruntime.GOMAXPROCS(0), rows/48)
	}
	if workers <= 1 {
		blendMotionImageRows(dst, width, src, left, top, right, bottom, filter, premultiplied, ctx, inverse, centerX, centerY, originX, originY)
		return nil
	}
	var wait sync.WaitGroup
	wait.Add(workers)
	for worker := 0; worker < workers; worker++ {
		rowStart := top + rows*worker/workers
		rowEnd := top + rows*(worker+1)/workers
		go func() {
			defer wait.Done()
			blendMotionImageRows(dst, width, src, left, rowStart, right, rowEnd, filter, premultiplied, ctx, inverse, centerX, centerY, originX, originY)
		}()
	}
	wait.Wait()
	return nil
}

func blendMotionImageRows(dst []byte, width int, src *assets.Image, left, top, right, bottom int, filter int64, premultiplied []byte, ctx motionRenderContext, inverse motionMatrix, centerX, centerY, originX, originY float64) {
	blend := motionBlendMode(ctx.blendMode)
	xStart := float64(left) - centerX
	baseSourceX := inverse.a*xStart + originX
	baseSourceY := inverse.c*xStart + originY
	for destY := top; destY < bottom; destY++ {
		deltaY := float64(destY) - centerY
		sourceX := baseSourceX + inverse.b*deltaY
		sourceY := baseSourceY + inverse.d*deltaY
		for destX := left; destX < right; destX++ {
			if sourceX >= -0.5 && sourceX < float64(src.Width)-0.5 &&
				sourceY >= -0.5 && sourceY < float64(src.Height)-0.5 {
				dstPixel := dst[(destY*width+destX)*4:]
				if filter == 0 {
					var pixel [4]byte
					sourceIX := int(math.Floor(sourceX + 0.5))
					sourceIY := int(math.Floor(sourceY + 0.5))
					if sourceIX < 0 {
						sourceIX = 0
					} else if sourceIX >= int(src.Width) {
						sourceIX = int(src.Width) - 1
					}
					if sourceIY < 0 {
						sourceIY = 0
					} else if sourceIY >= int(src.Height) {
						sourceIY = int(src.Height) - 1
					}
					copy(pixel[:], src.RGBA[(sourceIY*int(src.Width)+sourceIX)*4:])
					if pixel[3] != 0 && ctx.opacity != 0 {
						alpha := uint16(pixel[3]) * uint16(ctx.opacity) / 255
						blendMotionPixel(dstPixel, pixel[:], uint8(alpha), blend)
					}
				} else {
					sample := motionBilinearPremult(src.RGBA, premultiplied, int(src.Width), int(src.Height), sourceX, sourceY)
					if sample.alpha != 0 && ctx.opacity != 0 {
						blendMotionPremultPixel(dstPixel, sample, ctx.opacity, blend)
					}
				}
			}
			sourceX += inverse.a
			sourceY += inverse.c
		}
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// motionBlendMode maps the raw frame "bm" value onto the reference blend
// operation: resolveBlendOperationModeLike_0x6C7440 reads the low nibble
// (PlayerRenderInternal.cpp:423-442).
func motionBlendMode(raw int64) MotionBlendMode {
	switch raw & 0x0F {
	case 1:
		return MotionBlendAdditive
	case 2, 5:
		return MotionBlendSubtractive
	case 3:
		return MotionBlendMultiplicative
	case 4:
		return MotionBlendScreen
	}
	return MotionBlendAlpha
}

func unpremultiplyChannel(value, alpha uint32) byte {
	if alpha == 0 {
		return 0
	}
	straight := (value*255 + alpha/2) / alpha
	if straight > 255 {
		straight = 255
	}
	return byte(straight)
}

func blendMotionPixel(dst, src []byte, alpha uint8, blend MotionBlendMode) {
	if blend != MotionBlendAlpha {
		blendMotionPixelMode(dst, src, alpha, blend)
		return
	}
	if alpha == 255 {
		copy(dst, src[:4])
		return
	}
	srcA := uint32(alpha)
	dstA := uint32(dst[3])
	outA := srcA + dstA*(255-srcA)/255
	if outA == 0 {
		return
	}
	for i := 0; i < 3; i++ {
		value := uint32(src[i])*srcA + uint32(dst[i])*dstA*(255-srcA)/255
		dst[i] = uint8(value / outA)
	}
	dst[3] = uint8(outA)
}

// blendMotionPixelMode applies the Photoshop blend operations the reference
// selects in resolveBlendOperationModeLike_0x6C7440 (PlayerRenderInternal.cpp:
// 423-442). The destination approaches the blended color by the source alpha,
// matching the shader mix() in RenderManager_ogl.cpp:3695-3775.
func blendMotionPixelMode(dst, src []byte, alpha uint8, blend MotionBlendMode) {
	srcA := uint32(alpha)
	if srcA == 0 {
		return
	}
	dstA := uint32(dst[3])
	outA := srcA + dstA*(255-srcA)/255
	if outA == 0 {
		return
	}
	inverse := 255 - srcA
	for i := 0; i < 3; i++ {
		d := uint32(dst[i])
		s := uint32(src[i])
		var blended uint32
		switch blend {
		case MotionBlendAdditive:
			blended = min(d+s, 255)
		case MotionBlendSubtractive:
			if d+s > 255 {
				blended = d + s - 255
			}
		case MotionBlendMultiplicative:
			blended = (d*s + 127) / 255
		case MotionBlendScreen:
			blended = d + s - (d*s+127)/255
		}
		dst[i] = uint8((d*inverse + blended*srcA + 127) / 255)
	}
	dst[3] = uint8(outA)
}
func blendMotionPremultPixel(dst []byte, sample motionPremultSample, opacity uint8, blend MotionBlendMode) {
	srcA := sample.alpha * uint32(opacity) / 255
	if srcA == 0 {
		return
	}
	if blend != MotionBlendAlpha {
		src := [4]byte{
			unpremultiplyChannel(sample.red, sample.alpha),
			unpremultiplyChannel(sample.green, sample.alpha),
			unpremultiplyChannel(sample.blue, sample.alpha),
			byte(sample.alpha),
		}
		blendMotionPixelMode(dst, src[:], uint8(srcA), blend)
		return
	}
	srcR := sample.red * uint32(opacity) / 255
	srcG := sample.green * uint32(opacity) / 255
	srcB := sample.blue * uint32(opacity) / 255
	if srcA == 255 {
		dst[0], dst[1], dst[2], dst[3] = byte(srcR), byte(srcG), byte(srcB), 255
		return
	}
	dstA := uint32(dst[3])
	inverse := 255 - srcA
	outA := srcA + dstA*inverse/255
	if outA == 0 {
		return
	}
	dst[0] = byte((srcR*255 + uint32(dst[0])*dstA*inverse/255) / outA)
	dst[1] = byte((srcG*255 + uint32(dst[1])*dstA*inverse/255) / outA)
	dst[2] = byte((srcB*255 + uint32(dst[2])*dstA*inverse/255) / outA)
	dst[3] = byte(outA)
}

func psbMotionString(value assets.Value, key string) string {
	if object, ok := value.(map[string]assets.Value); ok {
		if result, ok := object[key].(string); ok {
			return result
		}
	}
	return ""
}

func psbMotionInt(value assets.Value, key string, fallback int) int {
	result := psbMotionFloat(value, key, float64(fallback))
	return int(result)
}

func psbMotionFloat(value assets.Value, key string, fallback float64) float64 {
	if object, ok := value.(map[string]assets.Value); ok {
		return psbMotionFloatValue(object[key], fallback)
	}
	return fallback
}

func psbMotionFloatValue(value assets.Value, fallback float64) float64 {
	switch number := value.(type) {
	case int64:
		return float64(number)
	case float64:
		return number
	default:
		return fallback
	}
}

func psbMotionList(value assets.Value) []float64 {
	items, _ := value.([]assets.Value)
	result := make([]float64, 0, len(items))
	for _, item := range items {
		result = append(result, psbMotionFloatValue(item, 0))
	}
	return result
}

// motionDrawMatrix is the canvas-to-layer transform a script supplies through
// Motion.Player.setDrawAffineTranslateMatrix. The translation carries the draw
// area center, matching the motion source layer whose 1x1 image the script
// centers inside its owner layer.
type motionDrawMatrix struct {
	a, b, c, d float64
	tx, ty     float64
}

func identityMotionDrawMatrix() motionDrawMatrix { return motionDrawMatrix{a: 1, d: 1} }

// motionDrawMatrixWithCameraOffset applies a camera-node offset in final item
// space. Player_applyTranslateOffset adds _cameraOffset to prepared corners,
// viewports, and mesh points after drawAffineMatrix has already been applied
// (PlayerRenderItems.cpp:1174-1215), so the translation belongs on the draw
// matrix itself rather than on canvas-space coordinates.
func motionDrawMatrixWithCameraOffset(matrix motionDrawMatrix, offsetX, offsetY float64) motionDrawMatrix {
	matrix.tx += offsetX
	matrix.ty += offsetY
	return matrix
}

// motionTranslateCanvas returns a copy of frame shifted by an integer pixel
// offset. The camera offset is integer-rounded by the reference, so this keeps
// the canvas-adopt fallback (a target layer without a bitmap) camera-aware
// without re-sampling.
func motionTranslateCanvas(frame *assets.Image, offsetX, offsetY int) *assets.Image {
	if frame == nil || (offsetX == 0 && offsetY == 0) {
		return frame
	}
	width, height := int(frame.Width), int(frame.Height)
	pixels := make([]byte, len(frame.RGBA))
	for y := range height {
		sourceY := y - offsetY
		if sourceY < 0 || sourceY >= height {
			continue
		}
		for x := range width {
			sourceX := x - offsetX
			if sourceX < 0 || sourceX >= width {
				continue
			}
			sourceOffset := (sourceY*width + sourceX) * 4
			destinationOffset := (y*width + x) * 4
			copy(pixels[destinationOffset:destinationOffset+4],
				frame.RGBA[sourceOffset:sourceOffset+4])
		}
	}
	return &assets.Image{Width: frame.Width, Height: frame.Height, RGBA: pixels}
}

// drawMotionCanvasInto composites the rendered motion canvas into an existing
// layer bitmap. Destination pixels sample the source, so scaled transforms stay
// gap-free; rotation and shearing are supported through the inverse transform.
func drawMotionCanvasInto(layer *Layer, frame *assets.Image, matrix motionDrawMatrix, filter int64) {
	width, height := int(layer.deviceWidth()), int(layer.deviceHeight())
	canvasWidth, canvasHeight := int(frame.Width), int(frame.Height)
	if width <= 0 || height <= 0 || canvasWidth <= 0 || canvasHeight <= 0 {
		return
	}
	if len(layer.RGBA) < width*height*4 || len(frame.RGBA) < canvasWidth*canvasHeight*4 {
		return
	}
	det := matrix.a*matrix.d - matrix.b*matrix.c
	if det == 0 || math.IsNaN(det) || math.IsInf(det, 0) {
		return
	}
	originX, originY := float64(canvasWidth)/2, float64(canvasHeight)/2
	if matrix.a == 1 && matrix.b == 0 && matrix.c == 0 && matrix.d == 1 {
		offsetXFloat, offsetYFloat := matrix.tx-originX, matrix.ty-originY
		offsetX, offsetY := int(math.Round(offsetXFloat)), int(math.Round(offsetYFloat))
		if offsetXFloat == float64(offsetX) && offsetYFloat == float64(offsetY) {
			sourceLeft := max(0, -offsetX)
			sourceTop := max(0, -offsetY)
			sourceRight := min(canvasWidth, width-offsetX)
			sourceBottom := min(canvasHeight, height-offsetY)
			if sourceLeft < sourceRight && sourceTop < sourceBottom {
				opaque := true
				for sourceY := sourceTop; sourceY < sourceBottom && opaque; sourceY++ {
					for sourceX := sourceLeft; sourceX < sourceRight; sourceX++ {
						if frame.RGBA[(sourceY*canvasWidth+sourceX)*4+3] != 255 {
							opaque = false
							break
						}
					}
				}
				for sourceY := sourceTop; sourceY < sourceBottom; sourceY++ {
					sourceRow := frame.RGBA[(sourceY*canvasWidth+sourceLeft)*4:]
					destinationRow := layer.RGBA[((sourceY+offsetY)*width+sourceLeft+offsetX)*4:]
					rowPixels := sourceRight - sourceLeft
					if opaque {
						copy(destinationRow[:rowPixels*4], sourceRow[:rowPixels*4])
						continue
					}
					for sourceX := range rowPixels {
						blendMotionPixels(destinationRow[sourceX*4:], sourceRow[sourceX*4:])
					}
				}
				return
			}
		}
	}
	left, top := math.Inf(1), math.Inf(1)
	right, bottom := math.Inf(-1), math.Inf(-1)
	for _, cornerX := range [2]float64{-originX - 0.5, float64(canvasWidth) - 0.5 - originX} {
		for _, cornerY := range [2]float64{-originY - 0.5, float64(canvasHeight) - 0.5 - originY} {
			x := matrix.a*cornerX + matrix.b*cornerY + matrix.tx
			y := matrix.c*cornerX + matrix.d*cornerY + matrix.ty
			left, top = math.Min(left, x), math.Min(top, y)
			right, bottom = math.Max(right, x), math.Max(bottom, y)
		}
	}
	startX := max(int(math.Floor(left)), 0)
	startY := max(int(math.Floor(top)), 0)
	endX := min(int(math.Ceil(right)), width-1)
	endY := min(int(math.Ceil(bottom)), height-1)
	inverse := motionMatrix{
		a: matrix.d / det,
		b: -matrix.b / det,
		c: -matrix.c / det,
		d: matrix.a / det,
	}
	rows := endY - startY + 1
	workers := 1
	if rows >= 96 {
		workers = min(goruntime.GOMAXPROCS(0), rows/48)
	}
	if workers <= 1 {
		drawMotionCanvasRows(layer.RGBA, width, frame.RGBA, canvasWidth, startX, startY, endX, endY, filter, inverse, matrix, originX, originY)
		return
	}
	var wait sync.WaitGroup
	wait.Add(workers)
	for worker := 0; worker < workers; worker++ {
		rowStart := startY + rows*worker/workers
		rowEnd := startY + rows*(worker+1)/workers - 1
		go func() {
			defer wait.Done()
			drawMotionCanvasRows(layer.RGBA, width, frame.RGBA, canvasWidth, startX, rowStart, endX, rowEnd, filter, inverse, matrix, originX, originY)
		}()
	}
	wait.Wait()
}

func drawMotionCanvasRows(destination []byte, destinationWidth int, source []byte, sourceWidth, startX, startY, endX, endY int, filter int64, inverse motionMatrix, matrix motionDrawMatrix, originX, originY float64) {
	sourceHeight := len(source) / (sourceWidth * 4)
	stepX, stepY := inverse.a, inverse.c
	for destY := startY; destY <= endY; destY++ {
		centerY := float64(destY) - matrix.ty
		centerX := float64(startX) - matrix.tx
		sourceX := inverse.a*centerX + inverse.b*centerY + originX
		sourceY := inverse.c*centerX + inverse.d*centerY + originY
		for destX := startX; destX <= endX; destX++ {
			if sourceX >= -0.5 && sourceX < float64(sourceWidth)-0.5 &&
				sourceY >= -0.5 && sourceY < float64(sourceHeight)-0.5 {
				var pixel [4]byte
				if filter == 0 {
					sourceIX := int(math.Floor(sourceX + 0.5))
					sourceIY := int(math.Floor(sourceY + 0.5))
					if sourceIX < 0 {
						sourceIX = 0
					} else if sourceIX >= sourceWidth {
						sourceIX = sourceWidth - 1
					}
					if sourceIY < 0 {
						sourceIY = 0
					} else if sourceIY >= sourceHeight {
						sourceIY = sourceHeight - 1
					}
					copy(pixel[:], source[(sourceIY*sourceWidth+sourceIX)*4:])
				} else {
					pixel = motionBilinearRGBA(source, nil, sourceWidth, sourceHeight, sourceX, sourceY)
				}
				blendMotionPixels(destination[(destY*destinationWidth+destX)*4:], pixel[:])
			}
			sourceX += stepX
			sourceY += stepY
		}
	}
}

func blendMotionPixels(destination, source []byte) {
	alpha := uint32(source[3])
	if alpha == 0 {
		return
	}
	if alpha == 0xff {
		destination[0], destination[1], destination[2], destination[3] = source[0], source[1], source[2], source[3]
		return
	}
	inverse := 0xff - alpha
	for channel := range 3 {
		destination[channel] = byte((uint32(source[channel])*alpha + uint32(destination[channel])*inverse) / 0xff)
	}
	destination[3] = byte(alpha + uint32(destination[3])*inverse/0xff)
}
