package runtime

import (
	"fmt"
	"math"
	goruntime "runtime"
	"strings"
	"sync"

	"github.com/eslzzyl/krkrgo/internal/assets"
)

type motionResource struct {
	path          string
	doc           *assets.Document
	data          []byte
	images        map[string]*assets.Image
	premultiplied map[string][]byte
	opaque        map[string]bool
	stack         map[string]bool
	canvas        *assets.Image
	canvasChara   string
	canvasMotion  string
	canvasTick    float64
	canvasFilter  int64
	canvasValid   bool
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
			x:       draw.TX - float64(l.deviceWidth())/2,
			y:       draw.TY - float64(l.deviceHeight())/2,
			opacity: draw.Opacity,
			matrix:  motionMatrix{a: draw.A, b: draw.B, c: draw.C, d: draw.D},
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

type motionRenderContext struct {
	x, y    float64
	opacity uint8
	matrix  motionMatrix
}

func renderPSBMotion(resource *motionResource, chara, motion string, tick float64, filter int64) (*assets.Image, error) {
	if resource == nil || resource.doc == nil {
		return nil, fmt.Errorf("motion resource is unavailable")
	}
	root, ok := resource.doc.Root.(map[string]assets.Value)
	if !ok {
		return nil, fmt.Errorf("motion PSB root is not a dictionary")
	}
	width := psbMotionInt(root["screenSize"], "width", 640)
	height := psbMotionInt(root["screenSize"], "height", 480)
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("motion PSB has invalid canvas size %dx%d", width, height)
	}
	if resource.canvasValid && resource.canvas != nil &&
		resource.canvas.Width == uint32(width) && resource.canvas.Height == uint32(height) &&
		resource.canvasChara == chara && resource.canvasMotion == motion &&
		resource.canvasTick == tick && resource.canvasFilter == filter {
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
	layers, _ := motionValue["layer"].([]assets.Value)
	if len(layers) == 0 {
		resource.canvasChara, resource.canvasMotion = chara, motion
		resource.canvasTick, resource.canvasFilter = tick, filter
		resource.canvasValid = true
		return canvas, nil
	}
	if resource.stack == nil {
		resource.stack = make(map[string]bool)
	} else {
		clear(resource.stack)
	}
	ctx := motionRenderContext{opacity: 255, matrix: identityMotionMatrix}
	if err := renderMotionValue(resource, root, layers, tick, filter, ctx, pixels, width, height, resource.stack); err != nil {
		return nil, err
	}
	resource.canvasChara, resource.canvasMotion = chara, motion
	resource.canvasTick, resource.canvasFilter = tick, filter
	resource.canvasValid = true
	return canvas, nil
}

func renderMotionValue(resource *motionResource, root map[string]assets.Value, value assets.Value, tick float64, filter int64, parent motionRenderContext, pixels []byte, width, height int, stack map[string]bool) error {
	switch node := value.(type) {
	case []assets.Value:
		for _, child := range node {
			if err := renderMotionValue(resource, root, child, tick, filter, parent, pixels, width, height, stack); err != nil {
				return err
			}
		}
		return nil
	case map[string]assets.Value:
		ctx := parent
		if frameList, ok := node["frameList"].([]assets.Value); ok {
			content, visible := psbMotionResolvedContent(frameList, tick)
			if visible {
				ctx = psbMotionContext(parent, content, node)
				if err := renderMotionFrame(resource, root, content, node, filter, ctx, pixels, width, height, stack); err != nil {
					return err
				}
			}
		}
		if children, ok := node["children"]; ok {
			if err := renderMotionValue(resource, root, children, tick, filter, ctx, pixels, width, height, stack); err != nil {
				return err
			}
		}
		if layers, ok := node["layer"]; ok {
			if err := renderMotionValue(resource, root, layers, tick, filter, ctx, pixels, width, height, stack); err != nil {
				return err
			}
		}
	}
	return nil
}
func buildGPUMotion(resource *motionResource, chara, motion string, tick float64, filter int64, drawMatrix motionDrawMatrix, scale int32) (*RenderMotion, error) {
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
	result := &RenderMotion{Filter: filter, Scale: scale, Draws: make([]RenderMotionDraw, 0, 8)}
	determinant := drawMatrix.a*drawMatrix.d - drawMatrix.b*drawMatrix.c
	if math.Abs(determinant) < 1e-9 || math.IsNaN(determinant) || math.IsInf(determinant, 0) {
		return result, nil
	}
	objects, _ := root["object"].(map[string]assets.Value)
	character, _ := objects[chara].(map[string]assets.Value)
	motions, _ := character["motion"].(map[string]assets.Value)
	motionValue, _ := motions[motion].(map[string]assets.Value)
	layers, _ := motionValue["layer"].([]assets.Value)
	if len(layers) == 0 {
		return result, nil
	}
	if resource.stack == nil {
		resource.stack = make(map[string]bool)
	} else {
		clear(resource.stack)
	}
	ctx := motionRenderContext{opacity: 255, matrix: identityMotionMatrix}
	if err := collectGPUMotionValue(resource, root, layers, tick, filter, ctx, drawMatrix, result, resource.stack); err != nil {
		return nil, err
	}
	return result, nil
}

func collectGPUMotionValue(resource *motionResource, root map[string]assets.Value, value assets.Value, tick float64, filter int64, parent motionRenderContext, drawMatrix motionDrawMatrix, result *RenderMotion, stack map[string]bool) error {
	switch node := value.(type) {
	case []assets.Value:
		for _, child := range node {
			if err := collectGPUMotionValue(resource, root, child, tick, filter, parent, drawMatrix, result, stack); err != nil {
				return err
			}
		}
	case map[string]assets.Value:
		ctx := parent
		if frameList, ok := node["frameList"].([]assets.Value); ok {
			content, visible := psbMotionResolvedContent(frameList, tick)
			if visible {
				ctx = psbMotionContext(parent, content, node)
				if err := collectGPUMotionFrame(resource, root, content, ctx, filter, drawMatrix, result, stack); err != nil {
					return err
				}
			}
		}
		if children, ok := node["children"]; ok {
			if err := collectGPUMotionValue(resource, root, children, tick, filter, ctx, drawMatrix, result, stack); err != nil {
				return err
			}
		}
		if layers, ok := node["layer"]; ok {
			if err := collectGPUMotionValue(resource, root, layers, tick, filter, ctx, drawMatrix, result, stack); err != nil {
				return err
			}
		}
	}
	return nil
}

func collectGPUMotionFrame(resource *motionResource, root map[string]assets.Value, content map[string]assets.Value, ctx motionRenderContext, filter int64, drawMatrix motionDrawMatrix, result *RenderMotion, stack map[string]bool) error {
	source, _ := content["src"].(string)
	if source == "" {
		return nil
	}
	if strings.HasPrefix(source, "src/") {
		image, originX, originY, err := motionSourceImage(resource, root, source)
		if err != nil {
			return err
		}
		originX += psbMotionFloat(content, "ox", 0)
		originY += psbMotionFloat(content, "oy", 0)
		transform, tx, ty := motionGPUTransform(drawMatrix, ctx, originX, originY)
		key := source
		if resource.path != "" {
			key = resource.path + "\x00" + source
		}
		scale := result.Scale
		result.Draws = append(result.Draws, RenderMotionDraw{
			SourceKey: key,
			RGBA:      scaleRGBA(image.RGBA, int32(image.Width), int32(image.Height), scale),
			Width:     int32(image.Width) * scale,
			Height:    int32(image.Height) * scale,
			A:         transform.a,
			B:         transform.b,
			C:         transform.c,
			D:         transform.d,
			TX:        tx * float64(scale),
			TY:        ty * float64(scale),
			Opacity:   ctx.opacity,
		})
		return nil
	}
	if strings.HasPrefix(source, "motion/") {
		parts := strings.Split(strings.TrimPrefix(source, "motion/"), "/")
		if len(parts) >= 2 {
			key := parts[0] + "/" + parts[1]
			if !stack[key] {
				stack[key] = true
				objects, _ := root["object"].(map[string]assets.Value)
				character, _ := objects[parts[0]].(map[string]assets.Value)
				motions, _ := character["motion"].(map[string]assets.Value)
				nested, _ := motions[parts[1]].(map[string]assets.Value)
				if nestedLayers, ok := nested["layer"]; ok {
					if err := collectGPUMotionValue(resource, root, nestedLayers, 0, filter, ctx, drawMatrix, result, stack); err != nil {
						return err
					}
				}
				delete(stack, key)
			}
		}
	}
	return nil
}

func motionGPUTransform(drawMatrix motionDrawMatrix, ctx motionRenderContext, originX, originY float64) (motionMatrix, float64, float64) {
	transform := motionMatrix{
		a: drawMatrix.a*ctx.matrix.a + drawMatrix.b*ctx.matrix.c,
		b: drawMatrix.a*ctx.matrix.b + drawMatrix.b*ctx.matrix.d,
		c: drawMatrix.c*ctx.matrix.a + drawMatrix.d*ctx.matrix.c,
		d: drawMatrix.c*ctx.matrix.b + drawMatrix.d*ctx.matrix.d,
	}
	tx := drawMatrix.tx + drawMatrix.a*ctx.x + drawMatrix.b*ctx.y - transform.a*originX - transform.b*originY
	ty := drawMatrix.ty + drawMatrix.c*ctx.x + drawMatrix.d*ctx.y - transform.c*originX - transform.d*originY
	return transform, tx, ty
}

func renderMotionFrame(resource *motionResource, root map[string]assets.Value, content map[string]assets.Value, layer map[string]assets.Value, filter int64, ctx motionRenderContext, pixels []byte, width, height int, stack map[string]bool) error {
	source, _ := content["src"].(string)
	if source == "" {
		return nil
	}
	if strings.HasPrefix(source, "src/") {
		image, originX, originY, err := motionSourceImage(resource, root, source)
		if err != nil {
			return err
		}
		opaque := motionSourceOpaque(resource, source, image)
		var premultiplied []byte
		if filter != 0 {
			premultiplied = motionPremultipliedImage(resource, source, image)
		}
		return blendMotionImage(pixels, width, height, image, originX+psbMotionFloat(content, "ox", 0), originY+psbMotionFloat(content, "oy", 0), filter, premultiplied, opaque, ctx)
	}
	if strings.HasPrefix(source, "motion/") {
		parts := strings.Split(strings.TrimPrefix(source, "motion/"), "/")
		if len(parts) >= 2 {
			key := parts[0] + "/" + parts[1]
			if !stack[key] {
				stack[key] = true
				objects, _ := root["object"].(map[string]assets.Value)
				character, _ := objects[parts[0]].(map[string]assets.Value)
				motions, _ := character["motion"].(map[string]assets.Value)
				nested, _ := motions[parts[1]].(map[string]assets.Value)
				if nestedLayers, ok := nested["layer"]; ok {
					if err := renderMotionValue(resource, root, nestedLayers, 0, filter, ctx, pixels, width, height, stack); err != nil {
						return err
					}
				}
				delete(stack, key)
			}
		}
		return nil
	}
	icon, _ := content["icon"].(string)
	if icon == "" {
		icon = source
	}
	if icon == "" {
		return nil
	}
	return nil
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
func psbMotionResolvedContent(frames []assets.Value, tick float64) (map[string]assets.Value, bool) {
	index, ok := psbMotionKeyframeIndex(frames, tick)
	if !ok {
		return nil, false
	}
	active, ok := frames[index].(map[string]assets.Value)
	if !ok {
		return nil, false
	}
	content, _ := active["content"].(map[string]assets.Value)
	if content == nil {
		return nil, false
	}
	if index+1 >= len(frames) || int(psbMotionFloat(active, "type", 0)) != 3 {
		return content, true
	}
	next, ok := frames[index+1].(map[string]assets.Value)
	if !ok {
		return content, true
	}
	nextContent, _ := next["content"].(map[string]assets.Value)
	if nextContent == nil {
		return content, true
	}
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
	if values := psbMotionList(content["coord"]); len(values) > 0 {
		interpolated["coord"] = psbMotionBlendList(values, psbMotionList(nextContent["coord"]), coordRatio)
	} else if values := psbMotionList(nextContent["coord"]); len(values) > 0 {
		interpolated["coord"] = psbMotionBlendList([]float64{0, 0, 0}, values, coordRatio)
	}
	if _, ok := content["opa"]; ok || nextContent["opa"] != nil {
		interpolated["opa"] = psbMotionBlendFloat(content["opa"], nextContent["opa"], 255, ratio)
	}
	for _, key := range []string{"zx", "zy", "angle", "sx", "sy", "ox", "oy", "fx", "fy"} {
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
	return interpolated, true
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

func psbMotionBlendList(value, next []float64, ratio float64) []assets.Value {
	length := max(len(value), len(next))
	blended := make([]assets.Value, length)
	for index := range length {
		from, to := 0.0, 0.0
		if index < len(value) {
			from = value[index]
		}
		if index < len(next) {
			to = next[index]
		}
		blended[index] = from*(1-ratio) + to*ratio
	}
	return blended
}

func psbMotionContext(parent motionRenderContext, content map[string]assets.Value, layer map[string]assets.Value) motionRenderContext {
	coord := psbMotionList(content["coord"])
	x, y := 0.0, 0.0
	if len(coord) > 0 {
		x = coord[0]
	}
	if len(coord) > 1 {
		y = coord[1]
	}
	transformedX := parent.matrix.a*x + parent.matrix.b*y
	transformedY := parent.matrix.c*x + parent.matrix.d*y
	ctx := motionRenderContext{
		x:       parent.x + transformedX,
		y:       parent.y + transformedY,
		opacity: parent.opacity,
		matrix:  multiplyMotionMatrix(parent.matrix, psbMotionLocalMatrix(layer, content)),
	}
	if opacity, ok := content["opa"]; ok {
		value := psbMotionFloatValue(opacity, 255)
		if value < 0 {
			value = 0
		}
		if value > 255 {
			value = 255
		}
		ctx.opacity = uint8(float64(parent.opacity) * value / 255)
	}
	return ctx
}

func psbMotionLocalMatrix(layer, content map[string]assets.Value) motionMatrix {
	matrix := identityMotionMatrix
	order := []int{0, 3, 2, 1}
	if values := psbMotionList(layer["transformOrder"]); len(values) > 0 {
		order = make([]int, len(values))
		for i, value := range values {
			order[i] = int(value)
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
	centerY := float64(height)/2 + ctx.y
	if matrix.a == 1 && matrix.b == 0 && matrix.c == 0 && matrix.d == 1 {
		offsetXFloat := centerX - originX
		offsetYFloat := centerY - originY
		offsetX, offsetY := int(math.Round(offsetXFloat)), int(math.Round(offsetYFloat))
		if offsetXFloat == float64(offsetX) && offsetYFloat == float64(offsetY) {
			sourceLeft := maxInt(0, -offsetX)
			sourceTop := maxInt(0, -offsetY)
			sourceRight := minInt(int(src.Width), width-offsetX)
			sourceBottom := minInt(int(src.Height), height-offsetY)
			if sourceLeft < sourceRight && sourceTop < sourceBottom {
				if opaque && ctx.opacity == 255 {
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
						blendMotionPixel(destinationRow[sourceX*4:], sourcePixel, uint8(alpha))
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
						blendMotionPixel(dstPixel, pixel[:], uint8(alpha))
					}
				} else {
					sample := motionBilinearPremult(src.RGBA, premultiplied, int(src.Width), int(src.Height), sourceX, sourceY)
					if sample.alpha != 0 && ctx.opacity != 0 {
						blendMotionPremultPixel(dstPixel, sample, ctx.opacity)
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

func blendMotionPixel(dst, src []byte, alpha uint8) {
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
func blendMotionPremultPixel(dst []byte, sample motionPremultSample, opacity uint8) {
	srcA := sample.alpha * uint32(opacity) / 255
	if srcA == 0 {
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

// drawMotionCanvasInto composites the rendered motion canvas into an existing
// layer bitmap. Destination pixels sample the source, so scaled transforms stay
// gap-free; rotation and shearing are supported through the inverse transform.
func drawMotionCanvasInto(layer *Layer, frame *assets.Image, matrix motionDrawMatrix, filter int64) {
	scale := layer.RenderScale()
	if scale > 1 {
		frame = &assets.Image{
			Width:  frame.Width * uint32(scale),
			Height: frame.Height * uint32(scale),
			RGBA:   scaleRGBA(frame.RGBA, int32(frame.Width), int32(frame.Height), scale),
		}
		matrix.tx *= float64(scale)
		matrix.ty *= float64(scale)
	}
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
