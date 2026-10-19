package runtime

import (
	"fmt"
	"hash/crc32"
	"hash/maphash"
	"math"
	"os"
	"sort"

	"github.com/eslzzyl/krkrgo/internal/assets"
	"github.com/eslzzyl/krkrgo/internal/tjs"
)

var transitionHashSeed = maphash.MakeSeed()

func hashTransitionBytes(pixels []byte) uint64 {
	var hash maphash.Hash
	hash.SetSeed(transitionHashSeed)
	_, _ = hash.Write(pixels)
	return hash.Sum64()
}

func transitionPixelsHash(layer *Layer, phase uint64) uint64 {
	hash := layer.transitionDestHash
	hash ^= layer.transitionSourceHash + 0x9e3779b97f4a7c15 + (hash << 6) + (hash >> 2)
	hash ^= layer.transitionRuleHash + uint64(phase)*0x517cc1b727220a95
	hash ^= uint64(layer.transitionVague) * 0x94d049bb133111eb
	return hash
}

type transitionAlphaClass uint8

const (
	transitionAlphaUnknown transitionAlphaClass = iota
	transitionAlphaZero
	transitionAlphaOpaque
	transitionAlphaMixed
)

func (class transitionAlphaClass) String() string {
	switch class {
	case transitionAlphaZero:
		return "zero"
	case transitionAlphaOpaque:
		return "opaque"
	case transitionAlphaMixed:
		return "mixed"
	default:
		return "unknown"
	}
}

func classifyRGBAAlpha(pixels []byte) transitionAlphaClass {
	if len(pixels) == 0 || len(pixels)%4 != 0 {
		return transitionAlphaUnknown
	}
	first := pixels[3]
	allSame := true
	for i := 7; i < len(pixels); i += 4 {
		if pixels[i] != first {
			allSame = false
			break
		}
	}
	if allSame {
		if first == 0 {
			return transitionAlphaZero
		}
		if first == 255 {
			return transitionAlphaOpaque
		}
	}
	return transitionAlphaMixed
}

func (s *Services) loadTransitionRule(name string, width, height int32) ([]byte, error) {
	data, err := s.readLayerImage(name)
	if err != nil {
		return nil, err
	}
	image, err := assets.DecodeTransitionRuleImage(data)
	if err != nil {
		return nil, fmt.Errorf("transition rule %q: %w", name, err)
	}
	if image.Width == 0 || image.Height == 0 || len(image.Pixels) != int(image.Width)*int(image.Height) {
		return nil, fmt.Errorf("transition rule %q has invalid dimensions", name)
	}
	pixelCount := uint64(width) * uint64(height)
	if pixelCount > uint64(maxLayerPixels) {
		return nil, fmt.Errorf("transition rule dimensions exceed layer pixel limit")
	}
	if image.Width == uint32(width) && image.Height == uint32(height) {
		return image.Pixels, nil
	}
	resized := make([]byte, int(pixelCount))
	for y := range height {
		sourceY := uint64(y) * uint64(image.Height) / uint64(height)
		for x := range width {
			sourceX := uint64(x) * uint64(image.Width) / uint64(width)
			resized[int(y)*int(width)+int(x)] = image.Pixels[int(sourceY)*int(image.Width)+int(sourceX)]
		}
	}
	return resized, nil
}

func (s *Services) layerTransition(vm *tjs.VM, context tjs.Value, operation string, args []tjs.Value, budget *uint64) (tjs.Value, error) {
	layer, _, err := s.layerForContext(context, operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	if operation == "stopTransition" {
		// The native method takes no semantic arguments. TJS still permits
		// callers to supply extras, which the original Layer implementation
		// ignores (including stopAllTransitions(true) in KAG).
		return tjs.VoidValue, s.stopLayerTransition(vm, layer, true, budget)
	}
	if len(args) < 3 || len(args) > 4 {
		return tjs.VoidValue, fmt.Errorf("Layer.beginTransition requires method, withchildren, source, and options")
	}
	method := args[0].Text()
	if method != "crossfade" && method != "universal" && method != "spin" {
		return tjs.VoidValue, fmt.Errorf("Layer.beginTransition: unsupported transition provider %q", method)
	}
	if layer.inTransition {
		return tjs.VoidValue, fmt.Errorf("Layer.beginTransition: current transition must be stopped first")
	}
	withChildren := true
	if !args[1].IsVoid() {
		withChildren, err = args[1].Truth()
		if err != nil {
			return tjs.VoidValue, err
		}
	}
	source, _, err := s.layerFromValue(args[2])
	if err != nil {
		return tjs.VoidValue, fmt.Errorf("Layer.beginTransition source: %w", err)
	}
	if source == layer || source.transitionSource == layer {
		return tjs.VoidValue, fmt.Errorf("Layer.beginTransition: invalid transition source")
	}
	if layerTreeRoot(source) != layerTreeRoot(layer) {
		return tjs.VoidValue, fmt.Errorf("Layer.beginTransition: transition layers must belong to the same tree")
	}
	if len(args) != 4 || args[3].Kind() != tjs.KindObject || args[3].IsNull() {
		return tjs.VoidValue, fmt.Errorf("Layer.beginTransition: options.time is required")
	}
	options, ok := vm.Objects.Lookup(args[3].ObjectRef().Object)
	if !ok || !options.IsValid() {
		return tjs.VoidValue, fmt.Errorf("Layer.beginTransition: invalid options object")
	}
	timeValue, ok := options.Get("time")
	if !ok || timeValue.IsVoid() {
		return tjs.VoidValue, fmt.Errorf("Layer.beginTransition: options.time is required")
	}
	time, err := timeValue.Int()
	if err != nil {
		return tjs.VoidValue, fmt.Errorf("Layer.beginTransition: options.time: %w", err)
	}
	if time < 2 {
		time = 2
	}
	callback, _ := options.Get("callback")
	if !callback.IsVoid() && (callback.Kind() != tjs.KindObject || callback.IsNull()) {
		return tjs.VoidValue, fmt.Errorf("Layer.beginTransition: options.callback must be a function")
	}
	exchange := false
	if value, ok := options.Get("exchange"); ok && !value.IsVoid() {
		exchange, err = value.Truth()
		if err != nil {
			return tjs.VoidValue, fmt.Errorf("Layer.beginTransition: options.exchange: %w", err)
		}
	}
	selfUpdate := false
	if value, ok := options.Get("selfupdate"); ok && !value.IsVoid() {
		selfUpdate, err = value.Truth()
		if err != nil {
			return tjs.VoidValue, fmt.Errorf("Layer.beginTransition: options.selfupdate: %w", err)
		}
	}
	if withChildren && (layer.Bounds.Width <= 0 || layer.Bounds.Height <= 0 || layer.Bounds.Width != source.Bounds.Width || layer.Bounds.Height != source.Bounds.Height) {
		return tjs.VoidValue, fmt.Errorf("Layer.beginTransition: transition layer sizes do not match")
	}
	if layer.ImageWidth <= 0 || layer.ImageHeight <= 0 || layer.ImageWidth != source.ImageWidth || layer.ImageHeight != source.ImageHeight || len(layer.RGBA) == 0 || len(layer.RGBA) != len(source.RGBA) {
		return tjs.VoidValue, fmt.Errorf("Layer.beginTransition: transition source and destination images must have matching sizes")
	}
	type1, type2, accel := int32(0), int32(0), int32(0)
	if method == "spin" {
		readSpinOption := func(name string, defaultValue int32) (int32, error) {
			value, ok := options.Get(name)
			if !ok || value.IsVoid() {
				return defaultValue, nil
			}
			n, err := value.Int()
			if err != nil || n < -1 || n > 11 {
				return 0, fmt.Errorf("Layer.beginTransition: options.%s must be an integer from -1 through 11", name)
			}
			return int32(n), nil
		}
		type1, err = readSpinOption("type1", type1)
		if err != nil {
			return tjs.VoidValue, err
		}
		type2, err = readSpinOption("type2", type2)
		if err != nil {
			return tjs.VoidValue, err
		}
		if value, ok := options.Get("accel"); ok && !value.IsVoid() {
			n, intErr := value.Int()
			if intErr != nil || n < math.MinInt32 || n > math.MaxInt32 {
				return tjs.VoidValue, fmt.Errorf("Layer.beginTransition: options.accel must be a signed 32-bit integer")
			}
			accel = int32(n)
		}
	}

	vague := int32(0)
	var rule []byte
	if method == "universal" {
		vague = 64
		if value, ok := options.Get("vague"); ok && !value.IsVoid() {
			n, intErr := value.Int()
			if intErr != nil || n < 0 || n > math.MaxInt32-255 {
				return tjs.VoidValue, fmt.Errorf("Layer.beginTransition: options.vague must be a non-negative 32-bit integer")
			}
			vague = int32(n)
		}
		ruleValue, ok := options.Get("rule")
		if !ok || ruleValue.IsVoid() || ruleValue.Text() == "" {
			return tjs.VoidValue, fmt.Errorf("Layer.beginTransition: options.rule is required for universal transitions")
		}
		rule, err = s.loadTransitionRule(ruleValue.Text(), layer.deviceWidth(), layer.deviceHeight())
		if err != nil {
			return tjs.VoidValue, fmt.Errorf("Layer.beginTransition rule: %w", err)
		}
	}
	layer.transitionDestImage = append(layer.transitionDestImage[:0], layer.RGBA...)
	layer.transitionSourceImage = append(layer.transitionSourceImage[:0], source.RGBA...)
	if s.gpuCapture && (method == "crossfade" || method == "universal") {
		// These methods are evaluated by the GPU shader from the endpoint
		// textures; the CPU phase buffer is materialized only on demand.
		layer.transitionPixels = nil
	} else {
		layer.transitionPixels = append(layer.transitionPixels[:0], layer.RGBA...)
	}
	layer.transitionDestHash = hashTransitionBytes(layer.transitionDestImage)
	layer.transitionSourceHash = hashTransitionBytes(layer.transitionSourceImage)
	layer.transitionRuleHash = hashTransitionBytes(rule)
	layer.transitionHashesValid = true
	layer.transitionOpaque, layer.transitionZeroOpaque, layer.transitionDestAlpha, layer.transitionSourceAlpha =
		classifyTransitionAlphaPlanes(layer.transitionDestImage, layer.transitionSourceImage)
	layer.transitionOpaque = method == "crossfade" && layer.transitionOpaque
	layer.transitionZeroOpaque = method == "crossfade" && layer.transitionZeroOpaque
	layer.inTransition = true
	layer.transitionSource = source
	layer.transitionMethod = method
	layer.transitionType1 = type1
	layer.transitionType2 = type2
	layer.transitionAccel = accel
	layer.transitionExchange = exchange
	layer.transitionChildren = withChildren
	layer.transitionCallback = callback
	layer.transitionDuration = uint64(time)
	layer.transitionStart = s.TimeMS
	if !callback.IsVoid() {
		// A callback supplies transition-relative ticks, initialized at zero.
		layer.transitionStart = 0
	}
	layer.transitionTick = layer.transitionStart
	layer.transitionVague = vague
	layer.transitionRule = rule
	layer.transitionSelfUpdate = selfUpdate
	source.transitionHidden = true
	layer.markRenderDirty()
	source.markRenderDirty()
	return tjs.VoidValue, nil
}

func (s *Services) cancelLayerTransition(layer *Layer) {
	if layer == nil || !layer.inTransition {
		return
	}
	if layer.transitionSource != nil {
		layer.transitionSource.transitionHidden = false
		layer.transitionSource.markRenderDirty()
	}
	layer.inTransition = false
	layer.transitionSource = nil
	layer.transitionMethod = ""
	layer.transitionType1 = 0
	layer.transitionType2 = 0
	layer.transitionAccel = 0
	layer.transitionChildren = false
	layer.transitionExchange = false
	layer.transitionCallback = tjs.VoidValue
	layer.transitionVague = 0
	layer.transitionRule = nil
	layer.transitionDestImage = nil
	layer.transitionSourceImage = nil
	layer.transitionPixels = nil
	layer.transitionOpaque = false
	layer.transitionZeroOpaque = false
	layer.transitionDestHash = 0
	layer.transitionSourceHash = 0
	layer.transitionRuleHash = 0
	layer.transitionHashesValid = false
	layer.transitionSourceAlpha = transitionAlphaUnknown
	layer.transitionDestAlpha = transitionAlphaUnknown
	layer.markRenderDirty()
}

func (s *Services) advanceLayerTransitions(vm *tjs.VM, timeMS uint64, selfUpdate bool, budget *uint64) error {
	_, err := s.advanceLayerTransitionsDeferred(vm, timeMS, selfUpdate, budget, false)
	return err
}

func (s *Services) advanceLayerTransitionsDeferred(vm *tjs.VM, timeMS uint64, selfUpdate bool, budget *uint64, deferCompletion bool) ([]tjs.ObjectID, error) {
	s.mu.RLock()
	ids := s.transitionIDsScratch[:0]
	for id, layer := range s.Layers {
		if layer.inTransition && layer.transitionSelfUpdate == selfUpdate {
			ids = append(ids, id)
		}
	}
	s.mu.RUnlock()
	s.transitionIDsScratch = ids
	if len(ids) > 1 {
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	}
	var pending []tjs.ObjectID
	if deferCompletion {
		pending = s.pendingTransitionScratch[:0]
		s.pendingTransitionScratch = pending
	}
	for _, id := range ids {
		layer := s.Layer(id)
		if layer == nil || !layer.inTransition {
			continue
		}
		tick := timeMS
		callback := layer.transitionCallback
		if callback.Kind() == tjs.KindObject {
			value, err := vm.CallFunction(callback, tjs.ObjectValue(tjs.NewObjectRef(id)), nil, s, budget)
			if err != nil {
				return nil, err
			}
			n, err := value.Int()
			if err != nil || n < 0 {
				return nil, fmt.Errorf("Layer.beginTransition callback must return a non-negative time")
			}
			tick = uint64(n)
		}
		layer = s.Layer(id)
		if layer == nil || !layer.inTransition {
			continue
		}
		if callback.IsVoid() && tick == layer.transitionTick {
			continue
		}
		layer.transitionTick = tick
		elapsed := uint64(0)
		if tick > layer.transitionStart {
			elapsed = tick - layer.transitionStart
		}
		phaseMax := uint64(255)
		if layer.transitionMethod == "universal" {
			phaseMax += uint64(layer.transitionVague)
		}
		phase := phaseMax
		if elapsed < layer.transitionDuration {
			phase = elapsed * phaseMax / layer.transitionDuration
		}
		// GPU render plans carry transition endpoints and phase to the host;
		// keep the CPU pixel buffer untouched for those two GPU-native methods.
		if !(s.gpuCapture && (layer.transitionMethod == "crossfade" || layer.transitionMethod == "universal")) {
			if len(layer.transitionPixels) == 0 {
				layer.transitionPixels = make([]byte, len(layer.transitionDestImage))
			}
			if layer.transitionMethod == "spin" {
				blendSpin(layer.transitionPixels, layer.transitionDestImage, layer.transitionSourceImage,
					layer.deviceWidth(), layer.deviceHeight(), phase, layer.transitionType1, layer.transitionType2, layer.transitionAccel)
			} else if layer.transitionMethod == "universal" {
				blendUniversal(layer.transitionPixels, layer.transitionDestImage, layer.transitionSourceImage,
					layer.transitionRule, phase, layer.transitionVague)
			} else if layer.transitionZeroOpaque {
				blendZeroOpaqueCrossfade(layer.transitionPixels, layer.transitionDestImage, layer.transitionSourceImage, phase)
			} else {
				blendCrossfade(layer.transitionPixels, layer.transitionDestImage, layer.transitionSourceImage, phase, layer.transitionOpaque)
			}
		}
		layer.markRenderDirty()
		if elapsed >= layer.transitionDuration {
			if os.Getenv("KRKRGO_TRANSITION_TRACE") == "1" {
				fmt.Fprintf(os.Stderr, "transition-trace final time=%d layer=%d source=%d method=%s tick=%d elapsed=%d duration=%d phase=%d dest=%08x src=%08x pixels=%08x\n",
					timeMS, layer.ID, layer.transitionSource.ID, layer.transitionMethod, tick, elapsed, layer.transitionDuration, phase,
					crc32.ChecksumIEEE(layer.transitionDestImage), crc32.ChecksumIEEE(layer.transitionSourceImage), crc32.ChecksumIEEE(layer.transitionPixels))
			}
			if deferCompletion {
				pending = append(pending, id)
			} else if err := s.stopLayerTransition(vm, layer, true, budget); err != nil {
				return nil, err
			}
		}
	}
	return pending, nil
}

func (s *Services) completeLayerTransitions(vm *tjs.VM, ids []tjs.ObjectID, budget *uint64) error {
	for _, id := range ids {
		if layer := s.Layer(id); layer != nil && layer.inTransition {
			if err := s.stopLayerTransition(vm, layer, true, budget); err != nil {
				return err
			}
		}
	}
	return nil
}

func classifyTransitionAlphaPlanes(dest, src []byte) (opaque, zeroOpaque bool, destAlpha, srcAlpha transitionAlphaClass) {
	if len(dest) == 0 || len(dest) != len(src) || len(dest)%4 != 0 {
		return false, false, transitionAlphaUnknown, transitionAlphaUnknown
	}
	destZero, destOpaque := true, true
	srcZero, srcOpaque := true, true
	for i := 3; i < len(dest); i += 4 {
		destByte, srcByte := dest[i], src[i]
		destZero = destZero && destByte == 0
		destOpaque = destOpaque && destByte == 255
		srcZero = srcZero && srcByte == 0
		srcOpaque = srcOpaque && srcByte == 255
		if !destZero && !destOpaque && !srcZero && !srcOpaque {
			break
		}
	}
	switch {
	case destZero:
		destAlpha = transitionAlphaZero
	case destOpaque:
		destAlpha = transitionAlphaOpaque
	default:
		destAlpha = transitionAlphaMixed
	}
	switch {
	case srcZero:
		srcAlpha = transitionAlphaZero
	case srcOpaque:
		srcAlpha = transitionAlphaOpaque
	default:
		srcAlpha = transitionAlphaMixed
	}
	return destOpaque && srcOpaque, destOpaque && srcZero, destAlpha, srcAlpha
}

func blendCrossfade(out, dest, src []byte, phase uint64, opaque bool) {
	if phase == 0 {
		copy(out, dest)
		return
	}
	if phase >= 255 {
		copy(out, src)
		return
	}
	p := int(phase)
	if opaque {
		q := 255 - p
		for i := range len(out) / 4 {
			offset := i * 4
			out[offset] = byte((int(dest[offset])*q + int(src[offset])*p + 127) / 255)
			out[offset+1] = byte((int(dest[offset+1])*q + int(src[offset+1])*p + 127) / 255)
			out[offset+2] = byte((int(dest[offset+2])*q + int(src[offset+2])*p + 127) / 255)
			out[offset+3] = 255
		}
		return
	}
	for i := range out {
		d, s := int(dest[i]), int(src[i])
		out[i] = byte((d*(255-p) + s*p + 127) / 255)
	}
}
func blendUniversal(out, dest, src, rule []byte, phase uint64, vague int32) {
	if phase == 0 {
		copy(out, dest)
		return
	}
	phaseValue := int64(phase)
	vagueValue := int64(vague)
	low := phaseValue - vagueValue
	for pixel, ruleByte := range rule {
		offset := pixel << 2
		ruleValue := int64(ruleByte)
		opacity := int64(0)
		if ruleValue < low {
			opacity = 255
		} else if ruleValue < phaseValue {
			if vagueValue != 0 {
				opacity = 255 - (ruleValue-low)*255/vagueValue
			} else {
				opacity = 255
			}
		}
		dest0, dest1 := int64(dest[offset]), int64(dest[offset+1])
		dest2, dest3 := int64(dest[offset+2]), int64(dest[offset+3])
		src0, src1 := int64(src[offset]), int64(src[offset+1])
		src2, src3 := int64(src[offset+2]), int64(src[offset+3])
		out[offset] = byte(dest0 + ((src0 - dest0) * opacity >> 8))
		out[offset+1] = byte(dest1 + ((src1 - dest1) * opacity >> 8))
		out[offset+2] = byte(dest2 + ((src2 - dest2) * opacity >> 8))
		out[offset+3] = byte(dest3 + ((src3 - dest3) * opacity >> 8))
	}
}

func blendZeroOpaqueCrossfade(out, dest, src []byte, phase uint64) {
	if phase == 0 {
		copy(out, dest)
		return
	}
	if phase >= 255 {
		copy(out, src)
		return
	}
	q := 255 - int(phase)
	for i := range len(out) / 4 {
		offset := i * 4
		out[offset] = byte((int(dest[offset])*q + 127) / 255)
		out[offset+1] = byte((int(dest[offset+1])*q + 127) / 255)
		out[offset+2] = byte((int(dest[offset+2])*q + 127) / 255)
		out[offset+3] = byte(q)
	}
}
func blendSpin(out, dest, src []byte, width, height int32, phase uint64, type1, type2, accel int32) {
	if phase == 0 {
		copy(out, dest)
		return
	}
	if phase >= 255 {
		copy(out, src)
		return
	}
	if type1 == -1 && type2 == -1 {
		blendCrossfade(out, dest, src, phase, false)
		return
	}
	progress := spinTransitionProgress(float64(phase)/255, accel)
	clear(out)
	if progress < 0.5 {
		turn := progress * 2
		if type1 == -1 {
			blendSpinFlat(out, dest, 1-turn)
		} else {
			drawSpinImage(out, dest, width, height, math.Cos(turn*math.Pi/2), type1)
		}
		return
	}
	turn := (progress - 0.5) * 2
	if type2 == -1 {
		blendSpinFlat(out, src, turn)
	} else {
		drawSpinImage(out, src, width, height, math.Sin(turn*math.Pi/2), type2)
	}
}

func spinTransitionProgress(progress float64, accel int32) float64 {
	if accel == 0 {
		return progress
	}
	power := int64(accel)
	if power < 0 {
		power = -power
	}
	if power > 7 {
		power = 7
	}
	power++
	if accel < 0 {
		return 1 - math.Pow(1-progress, float64(power))
	}
	return math.Pow(progress, float64(power))
}

func blendSpinFlat(out, image []byte, amount float64) {
	if amount <= 0 {
		return
	}
	if amount >= 1 {
		copy(out, image)
		return
	}
	factor := int(math.Round(amount * 255))
	for i, value := range image {
		out[i] = byte(int(value) * factor / 255)
	}
}

func drawSpinImage(out, image []byte, width, height int32, scale float64, kind int32) {
	if scale <= 0 {
		return
	}
	if scale > 1 {
		scale = 1
	}
	visibleWidth := int(math.Round(float64(width) * scale))
	if visibleWidth <= 0 {
		return
	}
	pivotRight := kind&1 != 0
	start := 0
	if pivotRight {
		start = int(width) - visibleWidth
	}
	for y := range int(height) {
		outRow := y * int(width) * 4
		imageRow := outRow
		for offset := range visibleWidth {
			sourceX := int(float64(offset) / scale)
			if sourceX >= int(width) {
				sourceX = int(width) - 1
			}
			outOffset := outRow + (start+offset)*4
			imageOffset := imageRow + sourceX*4
			copy(out[outOffset:outOffset+4], image[imageOffset:imageOffset+4])
		}
	}
}

// exchangeLayerTree mirrors Layer.exchange: the layers exchange positions, and
// children either follow their layer (withChildren) or remain in place.
func (s *Services) exchangeLayerTree(a, b *Layer, keepChildren bool) {
	if a == nil || b == nil || a == b {
		return
	}
	swap := func(layer *Layer) *Layer {
		switch layer {
		case a:
			return b
		case b:
			return a
		default:
			return layer
		}
	}
	ancestorChild := func(layer, ancestor *Layer) *Layer {
		for node := layer; node != nil; node = node.Parent {
			if node.Parent == ancestor {
				return node
			}
		}
		return nil
	}
	aParent, bParent := a.Parent, b.Parent
	aPath, bPath := ancestorChild(b, a), ancestorChild(a, b)
	aChildren := append([]*Layer(nil), a.children...)
	bChildren := append([]*Layer(nil), b.children...)
	aAbsolute, bAbsolute := a.absoluteOrderIndex, b.absoluteOrderIndex

	for _, layer := range s.Layers {
		for i, child := range layer.children {
			layer.children[i] = swap(child)
		}
	}
	if keepChildren {
		a.children = swappedLayerRefs(bChildren, swap)
		b.children = swappedLayerRefs(aChildren, swap)
	} else if aPath != nil {
		children := make([]*Layer, 0, len(aChildren)-1)
		for _, child := range aChildren {
			if child != aPath {
				children = append(children, child)
			}
		}
		a.children = children
		b.children = append(bChildren, aPath)
		if aPath == b {
			b.children[len(b.children)-1] = a
		}
	} else if bPath != nil {
		children := make([]*Layer, 0, len(bChildren)-1)
		for _, child := range bChildren {
			if child != bPath {
				children = append(children, child)
			}
		}
		b.children = children
		a.children = append(aChildren, bPath)
		if bPath == a {
			a.children[len(a.children)-1] = b
		}
	}
	a.Parent, b.Parent = swap(bParent), swap(aParent)
	a.absoluteOrderIndex, b.absoluteOrderIndex = bAbsolute, aAbsolute
	for _, parent := range s.Layers {
		for _, child := range parent.children {
			child.Parent = parent
		}
	}

	if s.Root == a || s.Root == b {
		oldRoot := s.Root
		s.Root = swap(oldRoot)
		s.syncRootGeometry()
		if window := s.WindowObjects[tjs.ObjectID(s.Root.windowID)]; window != nil &&
			window.primaryLayer == oldRoot.ID {
			window.primaryLayer = s.Root.ID
		}
	}
}

func swappedLayerRefs(layers []*Layer, swap func(*Layer) *Layer) []*Layer {
	result := make([]*Layer, len(layers))
	for i, layer := range layers {
		result[i] = swap(layer)
	}
	return result
}

func (s *Services) stopLayerTransition(vm *tjs.VM, layer *Layer, notify bool, budget *uint64) error {
	if !layer.inTransition {
		return nil
	}
	source := layer.transitionSource
	if source == nil {
		s.cancelLayerTransition(layer)
		return nil
	}
	if layer.transitionExchange {
		destX, destY, destVisible := layer.Bounds.X, layer.Bounds.Y, layer.Visible
		srcX, srcY, srcVisible := source.Bounds.X, source.Bounds.Y, source.Visible
		layer.RGBA = append(layer.RGBA[:0], layer.transitionSourceImage...)
		source.RGBA = append(source.RGBA[:0], layer.transitionDestImage...)
		layer.ImageWidth, source.ImageWidth = source.ImageWidth, layer.ImageWidth
		layer.ImageHeight, source.ImageHeight = source.ImageHeight, layer.ImageHeight
		layer.ProvinceWidth, source.ProvinceWidth = source.ProvinceWidth, layer.ProvinceWidth
		layer.ProvinceHeight, source.ProvinceHeight = source.ProvinceHeight, layer.ProvinceHeight
		layer.ImageLeft, source.ImageLeft = source.ImageLeft, layer.ImageLeft
		layer.ImageTop, source.ImageTop = source.ImageTop, layer.ImageTop
		layer.Bounds.X, layer.Bounds.Y, layer.Visible = srcX, srcY, srcVisible
		source.Bounds.X, source.Bounds.Y, source.Visible = destX, destY, destVisible
		s.exchangeLayerTree(layer, source, !layer.transitionChildren)
	} else {
		// KAG's simple completion callback assigns the source to the existing
		// layer; preserve source pixels and tree identity for that path.
		layer.RGBA = append(layer.RGBA[:0], layer.transitionSourceImage...)
	}
	layer.markImageDirty()
	source.markImageDirty()
	source.transitionHidden = false
	layer.markRenderDirty()
	source.markRenderDirty()
	layer.inTransition = false
	layer.transitionSource = nil
	layer.transitionMethod = ""
	layer.transitionType1 = 0
	layer.transitionType2 = 0
	layer.transitionAccel = 0
	layer.transitionExchange = false
	layer.transitionChildren = false
	layer.transitionCallback = tjs.VoidValue
	layer.transitionDuration = 0
	layer.transitionDestImage = nil
	layer.transitionSourceImage = nil
	layer.transitionPixels = nil
	layer.transitionOpaque = false
	layer.transitionZeroOpaque = false
	if err := s.syncLayerChildren(vm); err != nil {
		return err
	}
	if !notify {
		return nil
	}
	context := tjs.ObjectValue(tjs.NewObjectRef(tjs.ObjectID(layer.ID)))
	function, err := vm.GetMember(context, "onTransitionCompleted", s, budget)
	if err != nil {
		return err
	}
	if function.Kind() != tjs.KindVoid {
		_, err = vm.CallFunction(function, context, []tjs.Value{context, tjs.ObjectValue(tjs.NewObjectRef(tjs.ObjectID(source.ID)))}, s, budget)
	}
	return err
}
