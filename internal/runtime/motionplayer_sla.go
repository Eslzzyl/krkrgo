package runtime

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/eslzzyl/krkrgo/internal/assets"
	"github.com/eslzzyl/krkrgo/internal/tjs"
)

type motionSeparateLayerBaseKey struct {
	tree    string
	layerID int
}

func (s *Services) clearMotionSeparateLayerAdaptor(vm *tjs.VM, id tjs.ObjectID, adaptor *motionValueObjectState) error {
	s.mu.RLock()
	values := make([]tjs.Value, 0, len(adaptor.managedLayers))
	for _, value := range adaptor.managedLayers {
		values = append(values, value)
	}
	s.mu.RUnlock()
	var firstErr error
	for _, value := range values {
		if vm != nil {
			if _, err := vm.Invalidate(value, s, nil); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	s.mu.Lock()
	if s.MotionValueObjects[id] == adaptor {
		adaptor.managedLayers = make(map[string]tjs.Value)
		adaptor.values["privateRenderTarget"] = tjs.VoidValue
	}
	s.mu.Unlock()
	if err := s.syncLayerChildren(vm); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

func (s *Services) drawMotionSeparateLayerAdaptor(vm *tjs.VM, player *motionPlayerState, adaptorValue tjs.Value, adaptor *motionValueObjectState) error {
	s.mu.Lock()
	targetValue := adaptor.values["targetLayer"]
	owner := adaptor.values["owner"]
	absolute, _ := adaptor.values["absolute"].Int()
	previous := adaptor.managedLayers
	current := adaptor.layerScratch
	if current == nil {
		current = make(map[string]tjs.Value, len(previous))
	}
	clear(current)
	occurrences := adaptor.layerCounts
	if occurrences == nil {
		occurrences = make(map[motionSeparateLayerBaseKey]int)
	}
	clear(occurrences)
	retired := adaptor.layerRetired
	if retired == nil {
		retired = make(map[tjs.ObjectID]tjs.Value)
	}
	clear(retired)
	s.mu.Unlock()
	if targetValue.Kind() != tjs.KindObject || targetValue.IsNull() {
		return nil
	}
	targetLayer, err := s.motionLayerFromValue(vm, targetValue)
	if err != nil {
		return err
	}
	canvasWidth, canvasHeight := int(targetLayer.Bounds.Width), int(targetLayer.Bounds.Height)
	if canvasWidth <= 0 {
		canvasWidth = int(targetLayer.ImageWidth)
	}
	if canvasHeight <= 0 {
		canvasHeight = int(targetLayer.ImageHeight)
	}
	if canvasWidth <= 0 || canvasHeight <= 0 {
		motionTrace("sla-draw skipped id=%d layer=%d bounds=%dx%d image=%dx%d (no canvas)", player.playerID, targetLayer.ID, targetLayer.Bounds.Width, targetLayer.Bounds.Height, targetLayer.ImageWidth, targetLayer.ImageHeight)
		return nil
	}
	resource := s.playerResource(player)
	if resource == nil {
		return fmt.Errorf("Motion.Player.draw has no loaded PSB resource")
	}
	if err := motionPlayerCalcViewParam(vm, player); err != nil {
		return err
	}
	drawMatrix := player.drawMatrix
	var params []float64
	var attached []*motionResource
	maskMode := player.maskMode
	freezeBody := false
	if player.emote != nil {
		drawMatrix = player.emote.drawMatrixFor(drawMatrix, player)
		params = player.emote.renderParameters()
		attached = player.attached
		freezeBody = player.emote.freezeBodyMotion()
	}
	drawMatrix.tx += drawMatrix.a*player.x + drawMatrix.b*player.y
	drawMatrix.ty += drawMatrix.c*player.x + drawMatrix.d*player.y
	drawMatrix.tx += player.cameraOffsetX
	drawMatrix.ty += player.cameraOffsetY
	motion, err := buildGPUMotionVariablesZFactorInherit(
		resource, player.chara, player.motion, player.clampedEvalTime, 0, drawMatrix,
		1, params, freezeBody, maskMode, player.completionType,
		motionPlayerZFactor(player), attached, player.independentLayerInherit,
		motionRenderOptions{rootCoordinateMode: player.rootCoordinateMode, parentFrameLoopTime: player.frameLoopTime, hasParentFrameLoopTime: true, frameLastTime: player.frameLastTime, anyTimelinePlaying: len(player.playingTimelineLabels) > 0, rootState: player.rootState, hasRootState: player.hasRootState, anchorOutputs: motionAnchorOutputs(player.anchorStates), preview: player.preview})
	if err != nil {
		return err
	}
	for index := range motion.Draws {
		motion.Draws[index].LayerTreeKey = fmt.Sprintf("player:%d/tree:%d/%s", player.playerID, player.motionTreeGeneration, motion.Draws[index].LayerTreeKey)
	}
	motion.Width, motion.Height = canvasWidth, canvasHeight
	sequence := int32(0)
	for _, draw := range motion.Draws {
		if draw.SourceKey == "" || draw.Opacity == 0 || draw.Width <= 0 || draw.Height <= 0 {
			continue
		}
		clipLeft, clipTop, clipRight, clipBottom, visible := motionSeparateLayerClip(draw, canvasWidth, canvasHeight)
		if !visible {
			continue
		}
		baseKey := motionSeparateLayerBaseKey{tree: draw.LayerTreeKey, layerID: draw.LayerID}
		occurrence := occurrences[baseKey]
		occurrences[baseKey] = occurrence + 1
		key := motionSeparateLayerKey(draw.LayerTreeKey, draw.LayerID, occurrence)
		childValue := previous[key]
		child, _, layerErr := s.layerFromValue(childValue)
		if layerErr != nil || child.Parent != targetLayer {
			if layerErr == nil {
				retired[childValue.ObjectRef().Object] = childValue
			}
			layerClass, found := vm.Globals["Layer"]
			if !found {
				return fmt.Errorf("Motion.SeparateLayerAdaptor requires Layer class")
			}
			args := []tjs.Value{owner, targetValue}
			childValue, err = vm.Construct(layerClass, args, s, nil)
			if err != nil {
				return err
			}
			child, _, err = s.layerFromValue(childValue)
			if err != nil {
				return err
			}
		}
		width, height := int32(clipRight-clipLeft), int32(clipBottom-clipTop)
		layerType := motionSeparateLayerType(draw.Blend)
		s.mu.Lock()
		if err := child.SetSize(width, height); err != nil {
			s.mu.Unlock()
			return err
		}
		localDraw := motionSeparateLayerDraw(draw, clipLeft, clipTop)
		if s.gpuCapture {
			deferred := child.motionScratch
			if deferred == nil {
				deferred = &RenderMotion{Draws: make([]RenderMotionDraw, 1)}
				child.motionScratch = deferred
			} else if cap(deferred.Draws) < 1 {
				deferred.Draws = make([]RenderMotionDraw, 1)
			} else {
				deferred.Draws = deferred.Draws[:1]
			}
			deferred.Filter = motion.Filter
			deferred.Scale = motion.Scale
			deferred.AtlasKey = motion.AtlasKey
			deferred.Width = int(width)
			deferred.Height = int(height)
			deferred.Items = nil
			deferred.Draws[0] = localDraw
			if err := child.setDeferredMotion(width, height, deferred); err != nil {
				s.mu.Unlock()
				return err
			}
		} else {
			pixels := make([]byte, int(width)*int(height)*4)
			matrix := motionRenderContext{
				x:         localDraw.TX - float64(width)/2,
				y:         localDraw.TY - float64(height)/2,
				opacity:   255,
				blendMode: int64(localDraw.Blend),
				matrix:    motionMatrix{a: localDraw.A, b: localDraw.B, c: localDraw.C, d: localDraw.D},
			}
			source := &assets.Image{Width: uint32(localDraw.Width), Height: uint32(localDraw.Height), RGBA: localDraw.RGBA}
			if err := blendMotionImage(pixels, int(width), int(height), source, 0, 0, 0, nil, false, matrix); err != nil {
				s.mu.Unlock()
				return err
			}
			if err := child.SetRGBAOwned(width, height, pixels); err != nil {
				s.mu.Unlock()
				return err
			}
		}
		child.SetPosition(int32(clipLeft), int32(clipTop))
		if err := child.SetType(layerType); err != nil {
			s.mu.Unlock()
			return err
		}
		if err := child.SetOpacity(int(draw.Opacity)); err != nil {
			s.mu.Unlock()
			return err
		}
		child.Visible = true
		child.SetHitThreshold(256)
		if err := child.SetAbsoluteOrderIndex(int32(absolute) + sequence); err != nil {
			s.mu.Unlock()
			return err
		}
		s.mu.Unlock()
		sequence++
		current[key] = childValue
	}
	for key, value := range previous {
		if _, keep := current[key]; !keep && value.Kind() == tjs.KindObject && !value.IsNull() {
			retired[value.ObjectRef().Object] = value
		}
	}
	for _, value := range retired {
		if vm != nil {
			if _, err := vm.Invalidate(value, s, nil); err != nil {
				return err
			}
		}
	}
	s.mu.Lock()
	if s.MotionValueObjects[adaptorValue.ObjectRef().Object] == adaptor {
		adaptor.managedLayers = current
		adaptor.layerScratch = previous
		adaptor.layerCounts = occurrences
		adaptor.layerRetired = retired
	}
	s.mu.Unlock()
	return s.syncLayerChildren(vm)
}

func motionSeparateLayerKey(tree string, layerID, occurrence int) string {
	var buffer [256]byte
	key := buffer[:0]
	key = append(key, tree...)
	key = append(key, 0)
	key = strconv.AppendInt(key, int64(layerID), 10)
	key = append(key, 0)
	key = strconv.AppendInt(key, int64(occurrence), 10)
	return string(key)
}

func motionSeparateLayerDraw(draw RenderMotionDraw, clipLeft, clipTop int) RenderMotionDraw {
	draw.TX -= float64(clipLeft)
	draw.TY -= float64(clipTop)
	// The separate child layer owns the Motion draw's opacity and blend mode.
	// The deferred draw itself therefore copies the source with source-over.
	draw.Opacity = 255
	draw.Blend = MotionBlendAlpha
	draw.Viewport = [4]float64{}
	draw.HasViewport = false
	draw.SortKey = 0
	return draw
}

func (s *Services) assignMotionSeparateLayerAdaptor(vm *tjs.VM, id tjs.ObjectID, target *motionValueObjectState, sourceValue tjs.Value) error {
	if sourceValue.Kind() != tjs.KindObject || sourceValue.IsNull() || sourceValue.ObjectRef().Object == id {
		return nil
	}
	s.mu.RLock()
	source := s.MotionValueObjects[sourceValue.ObjectRef().Object]
	if source == nil || source.class != "SeparateLayerAdaptor" {
		s.mu.RUnlock()
		return nil
	}
	sourceLayers := make(map[string]tjs.Value, len(source.managedLayers))
	for key, value := range source.managedLayers {
		sourceLayers[key] = value
	}
	targetLayers := make(map[string]tjs.Value, len(target.managedLayers))
	for key, value := range target.managedLayers {
		targetLayers[key] = value
	}
	targetValue := target.values["targetLayer"]
	targetParentValue := targetValue
	owner := target.values["owner"]
	absolute, _ := target.values["absolute"].Int()
	s.mu.RUnlock()
	if targetValue.Kind() != tjs.KindObject || targetValue.IsNull() {
		return nil
	}
	targetLayer, err := s.motionLayerFromValue(vm, targetValue)
	if err != nil {
		return err
	}
	current := make(map[string]tjs.Value, len(sourceLayers))
	retired := make(map[tjs.ObjectID]tjs.Value)
	sequence := int32(0)
	keys := make([]string, 0, len(sourceLayers))
	for key := range sourceLayers {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		index := func(key string) int {
			prefix, _, _ := strings.Cut(key, "\x00")
			value, _ := strconv.Atoi(prefix)
			return value
		}
		left, right := index(keys[i]), index(keys[j])
		if left != right {
			return left < right
		}
		return keys[i] < keys[j]
	})
	for _, key := range keys {
		sourceValue := sourceLayers[key]
		sourceLayer, _, err := s.layerFromValue(sourceValue)
		if err != nil {
			continue
		}
		targetValue := targetLayers[key]
		targetLayerNode, _, layerErr := s.layerFromValue(targetValue)
		if layerErr != nil || targetLayerNode.Parent != targetLayer {
			if layerErr == nil {
				retired[targetValue.ObjectRef().Object] = targetValue
			}
			layerClass, found := vm.Globals["Layer"]
			if !found {
				return fmt.Errorf("Motion.SeparateLayerAdaptor.assign requires Layer class")
			}
			targetValue, err = vm.Construct(layerClass, []tjs.Value{owner, targetParentValue}, s, nil)
			if err != nil {
				return err
			}
			targetLayerNode, _, err = s.layerFromValue(targetValue)
			if err != nil {
				return err
			}
		}
		s.mu.Lock()
		if err := targetLayerNode.AssignImages(sourceLayer); err != nil {
			s.mu.Unlock()
			return err
		}
		if err := targetLayerNode.SetSize(sourceLayer.Bounds.Width, sourceLayer.Bounds.Height); err != nil {
			s.mu.Unlock()
			return err
		}
		targetLayerNode.Visible = sourceLayer.Visible
		if err := targetLayerNode.SetOpacity(int(sourceLayer.Opacity)); err != nil {
			s.mu.Unlock()
			return err
		}
		if err := targetLayerNode.SetType(sourceLayer.Type); err != nil {
			s.mu.Unlock()
			return err
		}
		targetLayerNode.SetPosition(sourceLayer.Bounds.X, sourceLayer.Bounds.Y)
		targetLayerNode.SetHitThreshold(256)
		if err := targetLayerNode.SetAbsoluteOrderIndex(int32(absolute) + sequence); err != nil {
			s.mu.Unlock()
			return err
		}
		s.mu.Unlock()
		sequence++
		current[key] = targetValue
	}
	for key, value := range targetLayers {
		if _, keep := current[key]; !keep && value.Kind() == tjs.KindObject && !value.IsNull() {
			retired[value.ObjectRef().Object] = value
		}
	}
	for _, value := range retired {
		if vm != nil {
			if _, err := vm.Invalidate(value, s, nil); err != nil {
				return err
			}
		}
	}
	s.mu.Lock()
	if s.MotionValueObjects[id] == target {
		target.managedLayers = current
	}
	s.mu.Unlock()
	return s.syncLayerChildren(vm)
}

func motionSeparateLayerType(blend MotionBlendMode) LayerType {
	switch blend {
	case MotionBlendAdditive:
		return LayerAdditive
	case MotionBlendSubtractive:
		return LayerSubtractive
	case MotionBlendMultiplicative:
		return LayerMultiplicative
	case MotionBlendScreen:
		return LayerScreen
	default:
		return LayerAlpha
	}
}

func motionSeparateLayerClip(draw RenderMotionDraw, width, height int) (int, int, int, int, bool) {
	left, top := math.Inf(1), math.Inf(1)
	right, bottom := math.Inf(-1), math.Inf(-1)
	for _, point := range [][2]float64{
		{-0.5, -0.5}, {float64(draw.Width) - 0.5, -0.5},
		{float64(draw.Width) - 0.5, float64(draw.Height) - 0.5},
		{-0.5, float64(draw.Height) - 0.5},
	} {
		x := draw.A*point[0] + draw.B*point[1] + draw.TX
		y := draw.C*point[0] + draw.D*point[1] + draw.TY
		left, top = math.Min(left, x), math.Min(top, y)
		right, bottom = math.Max(right, x), math.Max(bottom, y)
	}
	clipLeft := max(int(math.Floor(left)), 0)
	clipTop := max(int(math.Floor(top)), 0)
	clipRight := min(int(math.Ceil(right)), width)
	clipBottom := min(int(math.Ceil(bottom)), height)
	if draw.HasViewport {
		clipLeft = max(clipLeft, int(math.Floor(draw.Viewport[0])))
		clipTop = max(clipTop, int(math.Floor(draw.Viewport[1])))
		clipRight = min(clipRight, int(math.Ceil(draw.Viewport[2])))
		clipBottom = min(clipBottom, int(math.Ceil(draw.Viewport[3])))
	}
	return clipLeft, clipTop, clipRight, clipBottom, clipLeft < clipRight && clipTop < clipBottom
}
