package host

import (
	"fmt"
	"image/color"

	runtime "github.com/eslzzyl/krkrgo/internal/runtime"
	"github.com/hajimehoshi/ebiten/v2"
)

type gpuTextureRole uint8

const (
	gpuTextureLayer gpuTextureRole = iota
	gpuTextureDestination
	gpuTextureSource
	gpuTextureRule
)

type gpuTextureKey struct {
	id   runtime.LayerID
	role gpuTextureRole
}

type gpuTexture struct {
	image         *ebiten.Image
	width, height int32
	hash          uint64
	upload        []byte
	opaque        bool
}

// GPUCompositor consumes the borrowed runtime RenderPlan synchronously. The
// plan's pixel slices are never retained; gpuTexture.upload owns only the
// premultiplied copy required by Ebitengine.
type GPUCompositor struct {
	width, height int
	front, back   *ebiten.Image
	textures      map[gpuTextureKey]*gpuTexture
	shaders       map[gpuMethodKey]*ebiten.Shader
	vertices      []ebiten.Vertex
	indices       []uint16
	crossfade     *ebiten.Shader
	universal     *ebiten.Shader
}

var gpuReplaceBlend = ebiten.Blend{
	BlendFactorSourceRGB:        ebiten.BlendFactorOne,
	BlendFactorSourceAlpha:      ebiten.BlendFactorOne,
	BlendFactorDestinationRGB:   ebiten.BlendFactorZero,
	BlendFactorDestinationAlpha: ebiten.BlendFactorZero,
	BlendOperationRGB:           ebiten.BlendOperationAdd,
	BlendOperationAlpha:         ebiten.BlendOperationAdd,
}

func NewGPUCompositor() (*GPUCompositor, error) {
	methods, crossfade, universal, err := compileGPUShaders()
	if err != nil {
		return nil, err
	}
	return &GPUCompositor{
		textures:  make(map[gpuTextureKey]*gpuTexture),
		shaders:   methods,
		crossfade: crossfade,
		universal: universal,
		indices:   []uint16{0, 1, 2, 2, 1, 3},
	}, nil
}

func (c *GPUCompositor) Deallocate() {
	if c == nil {
		return
	}
	if c.front != nil {
		c.front.Deallocate()
		c.front = nil
	}
	if c.back != nil {
		c.back.Deallocate()
		c.back = nil
	}
	seen := make(map[*ebiten.Shader]struct{})
	for _, shader := range c.shaders {
		if shader != nil {
			seen[shader] = struct{}{}
		}
	}
	if c.crossfade != nil {
		seen[c.crossfade] = struct{}{}
	}
	if c.universal != nil {
		seen[c.universal] = struct{}{}
	}
	for shader := range seen {
		shader.Deallocate()
	}
	for key, texture := range c.textures {
		if texture != nil && texture.image != nil {
			texture.image.Deallocate()
		}
		delete(c.textures, key)
	}
	c.shaders = nil
	c.crossfade, c.universal = nil, nil
	c.vertices, c.indices = nil, nil
}

func (c *GPUCompositor) ensureTargets(width, height int, background [4]uint8) error {
	if width <= 0 || height <= 0 {
		return fmt.Errorf("invalid GPU render dimensions %dx%d", width, height)
	}
	if c.front == nil || c.back == nil || c.width != width || c.height != height {
		if c.front != nil {
			c.front.Deallocate()
		}
		if c.back != nil {
			c.back.Deallocate()
		}
		c.front, c.back = ebiten.NewImage(width, height), ebiten.NewImage(width, height)
		c.width, c.height = width, height
	}
	bg := color.RGBA{R: background[0], G: background[1], B: background[2], A: background[3]}
	c.front.Fill(bg)
	c.back.Fill(bg)
	return nil
}

func premultipliedPixels(src []byte, dst []byte) {
	for i := 0; i+3 < len(src); i += 4 {
		a := uint16(src[i+3])
		dst[i+3] = byte(a)
		dst[i] = byte((uint16(src[i])*a + 127) / 255)
		dst[i+1] = byte((uint16(src[i+1])*a + 127) / 255)
		dst[i+2] = byte((uint16(src[i+2])*a + 127) / 255)
	}
}

func (c *GPUCompositor) texture(key gpuTextureKey, width, height int32, src []byte, hash uint64, opaque bool) (*ebiten.Image, error) {
	if width <= 0 || height <= 0 || uint64(width)*uint64(height)*4 != uint64(len(src)) {
		return nil, fmt.Errorf("invalid GPU texture %d: %dx%d with %d bytes", key.id, width, height, len(src))
	}
	t := c.textures[key]
	if t == nil || t.width != width || t.height != height || t.image == nil {
		if t != nil && t.image != nil {
			t.image.Deallocate()
		}
		t = &gpuTexture{image: ebiten.NewImage(int(width), int(height)), width: width, height: height}
		t.upload = make([]byte, len(src))
		c.textures[key] = t
		t.hash = ^uint64(0)
	}
	if t.hash != hash || len(t.upload) != len(src) {
		if cap(t.upload) < len(src) {
			t.upload = make([]byte, len(src))
		} else {
			t.upload = t.upload[:len(src)]
		}
		premultipliedPixels(src, t.upload)
		t.image.WritePixels(t.upload)
		t.hash = hash
	}
	t.opaque = opaque
	return t.image, nil
}

func (c *GPUCompositor) copyFrontToBack() {
	c.back.DrawImage(c.front, &ebiten.DrawImageOptions{Blend: gpuReplaceBlend})
}

func (c *GPUCompositor) drawQuad(target *ebiten.Image, shader *ebiten.Shader, images [4]*ebiten.Image, x0, y0, x1, y1, sx, sy float32, uniforms map[string]any) {
	c.vertices = c.vertices[:0]
	c.vertices = append(c.vertices,
		ebiten.Vertex{DstX: x0, DstY: y0, SrcX: x0, SrcY: y0},
		ebiten.Vertex{DstX: x1, DstY: y0, SrcX: x1, SrcY: y0},
		ebiten.Vertex{DstX: x0, DstY: y1, SrcX: x0, SrcY: y1},
		ebiten.Vertex{DstX: x1, DstY: y1, SrcX: x1, SrcY: y1},
	)
	uniforms["SourceOrigin"] = []float32{sx, sy}
	target.DrawTrianglesShader(c.vertices, c.indices, shader, &ebiten.DrawTrianglesShaderOptions{
		Blend: gpuReplaceBlend, Uniforms: uniforms, Images: images,
	})
}
func (c *GPUCompositor) drawNode(plan runtime.RenderPlan, index int, targetType runtime.LayerType) error {
	if index < 0 || index >= len(plan.Nodes) {
		return fmt.Errorf("invalid render-plan node index %d", index)
	}
	n := plan.Nodes[index]
	if n.TransitionHidden || !n.Visible || n.Opacity == 0 {
		return nil
	}
	if n.Type == runtime.LayerEffect || n.Type == runtime.LayerFilter {
		return fmt.Errorf("%w: layer %d type %d", runtime.ErrUnsupportedLayerType, n.ID, n.Type)
	}
	ax, ay := n.Bounds.X, n.Bounds.Y
	drawW, drawH := n.Bounds.Width, n.Bounds.Height
	if drawW == 0 {
		drawW = n.ImageWidth
	}
	if drawH == 0 {
		drawH = n.ImageHeight
	}
	if n.Type != runtime.LayerBinder && n.ImageWidth > 0 && n.ImageHeight > 0 && drawW > 0 && drawH > 0 {
		localClip := runtime.Rect{Width: drawW, Height: drawH}
		if n.ClipSet {
			localClip = localClip.Intersect(n.Clip)
		}
		x0 := int64(localClip.X)
		y0 := int64(localClip.Y)
		x1 := int64(localClip.Right())
		y1 := int64(localClip.Bottom())
		if -int64(ax) > x0 {
			x0 = -int64(ax)
		}
		if -int64(ay) > y0 {
			y0 = -int64(ay)
		}
		if int64(c.width)-int64(ax) < x1 {
			x1 = int64(c.width) - int64(ax)
		}
		if int64(c.height)-int64(ay) < y1 {
			y1 = int64(c.height) - int64(ay)
		}
		x0 -= int64(n.ImageLeft)
		x1 -= int64(n.ImageLeft)
		y0 -= int64(n.ImageTop)
		y1 -= int64(n.ImageTop)
		if x0 < 0 {
			x0 = 0
		}
		if y0 < 0 {
			y0 = 0
		}
		if x1 > int64(n.ImageWidth) {
			x1 = int64(n.ImageWidth)
		}
		if y1 > int64(n.ImageHeight) {
			y1 = int64(n.ImageHeight)
		}
		if x0 < x1 && y0 < y1 {
			var source *ebiten.Image
			var shader *ebiten.Shader
			var err error
			uniforms := map[string]any{}
			if n.Transition != nil {
				tr := n.Transition
				dest, err := c.texture(gpuTextureKey{n.ID, gpuTextureDestination}, tr.ImageWidth, tr.ImageHeight, tr.DestinationRGBA, tr.DestinationHash, tr.Opaque)
				if err != nil {
					return err
				}
				src, err := c.texture(gpuTextureKey{n.ID, gpuTextureSource}, tr.ImageWidth, tr.ImageHeight, tr.SourceRGBA, tr.SourceHash, tr.Opaque)
				if err != nil {
					return err
				}
				source, shader = src, c.crossfade
				uniforms["Phase"] = float32(tr.Phase) / 255
				uniforms["Opaque"] = boolToFloat(tr.Opaque)
				uniforms["ZeroDestinationOpaque"] = boolToFloat(tr.ZeroDestinationOpaque)
				uniforms["LayerOpacity"] = float32(n.Opacity) / 255
				if tr.Method == runtime.RenderTransitionUniversal {
					rule, err := c.ruleTexture(n.ID, tr)
					if err != nil {
						return err
					}
					shader = c.universal
					uniforms["Phase"] = float32(tr.Phase) / 255
					uniforms["Vague"] = float32(tr.Vague) / 255
					uniforms["RuleOrigin"] = []float32{float32(ax + n.ImageLeft), float32(ay + n.ImageTop)}
					images := [4]*ebiten.Image{c.front, dest, src, rule}
					c.copyFrontToBack()
					c.drawQuad(c.back, shader, images, float32(ax+n.ImageLeft+int32(x0)), float32(ay+n.ImageTop+int32(y0)), float32(ax+n.ImageLeft+int32(x1)), float32(ay+n.ImageTop+int32(y1)), float32(ax+n.ImageLeft), float32(ay+n.ImageTop), uniforms)
				} else {
					images := [4]*ebiten.Image{c.front, dest, src, nil}
					c.copyFrontToBack()
					c.drawQuad(c.back, shader, images, float32(ax+n.ImageLeft+int32(x0)), float32(ay+n.ImageTop+int32(y0)), float32(ax+n.ImageLeft+int32(x1)), float32(ay+n.ImageTop+int32(y1)), float32(ax+n.ImageLeft), float32(ay+n.ImageTop), uniforms)
				}
				c.front, c.back = c.back, c.front
			} else {
				source, err = c.texture(gpuTextureKey{n.ID, gpuTextureLayer}, n.ImageWidth, n.ImageHeight, n.RGBA, n.ImageHash, n.Type == runtime.LayerOpaque)
				if err != nil {
					return err
				}
				shader = c.shaders[gpuMethodKey{n.Type, targetType, n.HoldAlpha}]
				if shader == nil {
					return fmt.Errorf("unsupported GPU layer type %d", n.Type)
				}
				c.copyFrontToBack()
				c.drawQuad(c.back, shader, [4]*ebiten.Image{c.front, source, nil, nil}, float32(ax+n.ImageLeft+int32(x0)), float32(ay+n.ImageTop+int32(y0)), float32(ax+n.ImageLeft+int32(x1)), float32(ay+n.ImageTop+int32(y1)), float32(ax+n.ImageLeft), float32(ay+n.ImageTop), map[string]any{
					"Mode": float32(n.Type), "TargetType": float32(targetType), "Opacity": float32(n.Opacity) / 255, "HoldAlpha": boolToFloat(n.HoldAlpha),
				})
				c.front, c.back = c.back, c.front
			}
		}
	}
	childTarget := n.Type
	if n.Type == runtime.LayerBinder {
		childTarget = targetType
	}
	for i := range n.ChildCount {
		pos := n.ChildStart + i
		if pos < 0 || pos >= len(plan.ChildIndices) {
			return fmt.Errorf("invalid child range for layer %d", n.ID)
		}
		if err := c.drawNode(plan, plan.ChildIndices[pos], childTarget); err != nil {
			return err
		}
	}
	return nil
}

func boolToFloat(v bool) float32 {
	if v {
		return 1
	}
	return 0
}

func (c *GPUCompositor) ruleTexture(id runtime.LayerID, tr *runtime.RenderTransition) (*ebiten.Image, error) {
	if tr.RuleWidth <= 0 || tr.RuleHeight <= 0 || uint64(tr.RuleWidth)*uint64(tr.RuleHeight) != uint64(len(tr.Rule)) {
		return nil, fmt.Errorf("invalid universal transition rule for layer %d", id)
	}
	t := c.textures[gpuTextureKey{id, gpuTextureRule}]
	if t == nil || t.width != tr.RuleWidth || t.height != tr.RuleHeight || t.image == nil {
		if t != nil && t.image != nil {
			t.image.Deallocate()
		}
		t = &gpuTexture{image: ebiten.NewImage(int(tr.RuleWidth), int(tr.RuleHeight)), width: tr.RuleWidth, height: tr.RuleHeight, hash: ^uint64(0)}
		t.upload = make([]byte, len(tr.Rule)*4)
		c.textures[gpuTextureKey{id, gpuTextureRule}] = t
	}
	if t.hash != tr.RuleHash {
		if cap(t.upload) < len(tr.Rule)*4 {
			t.upload = make([]byte, len(tr.Rule)*4)
		} else {
			t.upload = t.upload[:len(tr.Rule)*4]
		}
		for i, value := range tr.Rule {
			t.upload[i*4], t.upload[i*4+1], t.upload[i*4+2], t.upload[i*4+3] = value, 0, 0, 255
		}
		t.image.WritePixels(t.upload)
		t.hash = tr.RuleHash
	}
	return t.image, nil
}

func (c *GPUCompositor) Compose(plan runtime.RenderPlan) (*ebiten.Image, error) {
	if c == nil {
		return nil, fmt.Errorf("nil GPU compositor")
	}
	if plan.Width <= 0 || plan.Height <= 0 {
		return nil, fmt.Errorf("invalid render-plan dimensions %dx%d", plan.Width, plan.Height)
	}
	if err := c.ensureTargets(int(plan.Width), int(plan.Height), plan.Background); err != nil {
		return nil, err
	}
	if len(plan.Nodes) != 0 {
		root := 0
		if err := c.drawNode(plan, root, plan.Nodes[root].Type); err != nil {
			return nil, err
		}
	}
	return c.front, nil
}
