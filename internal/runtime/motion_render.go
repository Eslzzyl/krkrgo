package runtime

import (
	"fmt"
	"math"
	"strings"

	"github.com/eslzzyl/krkrgo/internal/assets"
)

type motionResource struct {
	path   string
	doc    *assets.Document
	data   []byte
	images map[string]*assets.Image
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

func renderPSBMotion(resource *motionResource, chara, motion string, tick float64) (*assets.Image, error) {
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
	pixels := make([]byte, width*height*4)
	objects, _ := root["object"].(map[string]assets.Value)
	character, _ := objects[chara].(map[string]assets.Value)
	motions, _ := character["motion"].(map[string]assets.Value)
	motionValue, _ := motions[motion].(map[string]assets.Value)
	layers, _ := motionValue["layer"].([]assets.Value)
	if len(layers) == 0 {
		return &assets.Image{Width: uint32(width), Height: uint32(height), RGBA: pixels}, nil
	}
	ctx := motionRenderContext{opacity: 255, matrix: identityMotionMatrix}
	if err := renderMotionValue(resource, root, layers, tick, ctx, pixels, width, height, map[string]bool{}); err != nil {
		return nil, err
	}
	return &assets.Image{Width: uint32(width), Height: uint32(height), RGBA: pixels}, nil
}

func renderMotionValue(resource *motionResource, root map[string]assets.Value, value assets.Value, tick float64, parent motionRenderContext, pixels []byte, width, height int, stack map[string]bool) error {
	switch node := value.(type) {
	case []assets.Value:
		for _, child := range node {
			if err := renderMotionValue(resource, root, child, tick, parent, pixels, width, height, stack); err != nil {
				return err
			}
		}
		return nil
	case map[string]assets.Value:
		ctx := parent
		if frameList, ok := node["frameList"].([]assets.Value); ok {
			frame, active := psbMotionFrame(frameList, tick)
			if active {
				ctx = psbMotionContext(parent, frame, node)
				if err := renderMotionFrame(resource, root, frame, node, ctx, pixels, width, height, stack); err != nil {
					return err
				}
			}
		}
		if children, ok := node["children"]; ok {
			if err := renderMotionValue(resource, root, children, tick, ctx, pixels, width, height, stack); err != nil {
				return err
			}
		}
		if layers, ok := node["layer"]; ok {
			if err := renderMotionValue(resource, root, layers, tick, ctx, pixels, width, height, stack); err != nil {
				return err
			}
		}
	}
	return nil
}

func renderMotionFrame(resource *motionResource, root map[string]assets.Value, frame map[string]assets.Value, layer map[string]assets.Value, ctx motionRenderContext, pixels []byte, width, height int, stack map[string]bool) error {
	content, _ := frame["content"].(map[string]assets.Value)
	if content == nil {
		return nil
	}
	source, _ := content["src"].(string)
	if source == "" {
		return nil
	}
	if strings.HasPrefix(source, "src/") {
		image, originX, originY, err := motionSourceImage(resource, root, source)
		if err != nil {
			return err
		}
		return blendMotionImage(pixels, width, height, image, originX+psbMotionFloat(content, "ox", 0), originY+psbMotionFloat(content, "oy", 0), ctx)
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
					if err := renderMotionValue(resource, root, nestedLayers, 0, ctx, pixels, width, height, stack); err != nil {
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

func psbMotionFrame(frames []assets.Value, tick float64) (map[string]assets.Value, bool) {
	var selected map[string]assets.Value
	for _, value := range frames {
		frame, ok := value.(map[string]assets.Value)
		if !ok || psbMotionFloat(frame, "time", 0) > tick {
			break
		}
		selected = frame
	}
	if selected == nil {
		return nil, false
	}
	return selected, int(psbMotionFloat(selected, "type", 0)) != 0
}

func psbMotionContext(parent motionRenderContext, frame map[string]assets.Value, layer map[string]assets.Value) motionRenderContext {
	content, _ := frame["content"].(map[string]assets.Value)
	if content == nil {
		return parent
	}
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

func blendMotionImage(dst []byte, width, height int, src *assets.Image, originX, originY float64, ctx motionRenderContext) error {
	matrix := ctx.matrix
	determinant := matrix.a*matrix.d - matrix.b*matrix.c
	if math.Abs(determinant) < 1e-9 {
		return nil
	}
	centerX := float64(width)/2 + ctx.x
	centerY := float64(height)/2 + ctx.y
	corners := [][2]float64{
		{-originX, -originY},
		{float64(src.Width) - originX, -originY},
		{float64(src.Width) - originX, float64(src.Height) - originY},
		{-originX, float64(src.Height) - originY},
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
	for destY := top; destY < bottom; destY++ {
		for destX := left; destX < right; destX++ {
			deltaX := float64(destX) - centerX
			deltaY := float64(destY) - centerY
			sourceX := inverse.a*deltaX + inverse.b*deltaY + originX
			sourceY := inverse.c*deltaX + inverse.d*deltaY + originY
			sourceIX := int(math.Floor(sourceX))
			sourceIY := int(math.Floor(sourceY))
			if sourceIX < 0 || sourceIX >= int(src.Width) || sourceIY < 0 || sourceIY >= int(src.Height) {
				continue
			}
			pixel := src.RGBA[(sourceIY*int(src.Width)+sourceIX)*4:]
			if pixel[3] == 0 || ctx.opacity == 0 {
				continue
			}
			alpha := uint16(pixel[3]) * uint16(ctx.opacity) / 255
			dstPixel := dst[(destY*width+destX)*4:]
			blendMotionPixel(dstPixel, pixel, uint8(alpha))
		}
	}
	return nil
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
	srcA := uint16(alpha)
	dstA := uint16(dst[3])
	outA := srcA + dstA*(255-srcA)/255
	if outA == 0 {
		return
	}
	for i := 0; i < 3; i++ {
		value := uint16(src[i])*srcA + uint16(dst[i])*dstA*(255-srcA)/255
		dst[i] = uint8(value / outA)
	}
	dst[3] = uint8(outA)
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
func drawMotionCanvasInto(layer *Layer, frame *assets.Image, matrix motionDrawMatrix) {
	width, height := int(layer.ImageWidth), int(layer.ImageHeight)
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
	originX, originY := float64(canvasWidth-1)/2, float64(canvasHeight-1)/2
	left, top := math.Inf(1), math.Inf(1)
	right, bottom := math.Inf(-1), math.Inf(-1)
	for _, cornerX := range [2]float64{-originX, float64(canvasWidth-1) - originX} {
		for _, cornerY := range [2]float64{-originY, float64(canvasHeight-1) - originY} {
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
	for destY := startY; destY <= endY; destY++ {
		centerY := float64(destY) - matrix.ty
		for destX := startX; destX <= endX; destX++ {
			centerX := float64(destX) - matrix.tx
			sourceX := int(math.Round((matrix.d*centerX-matrix.b*centerY)/det + originX))
			sourceY := int(math.Round((matrix.a*centerY-matrix.c*centerX)/det + originY))
			if sourceX < 0 || sourceY < 0 || sourceX >= canvasWidth || sourceY >= canvasHeight {
				continue
			}
			blendMotionPixels(layer.RGBA[(destY*width+destX)*4:], frame.RGBA[(sourceY*canvasWidth+sourceX)*4:])
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
