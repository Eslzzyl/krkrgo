package host

import (
	"fmt"
	"image"
	"image/color"
	"sort"

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
	uploaded      bool
}

type gpuTextureSize struct {
	width, height int32
}
type gpuMotionAtlasEntry struct {
	rect   image.Rectangle
	view   *ebiten.Image
	width  int32
	height int32
	hash   uint64
	upload []byte
}

// gpuMotionAtlas keeps all source views for one motion node on one backing
// texture, allowing successive transformed draws to stay in the same batch.
type gpuMotionAtlas struct {
	texture                    *ebiten.Image
	width, height              int
	usedHeight, capacityHeight int
	entries                    map[string]*gpuMotionAtlasEntry
}

// GPUCompositor consumes the borrowed runtime RenderPlan synchronously. The
// plan's pixel slices are never retained; gpuTexture.upload owns only the
// premultiplied copy required by Ebitengine.
type GPUCompositor struct {
	width, height  int
	front, back    *ebiten.Image
	textures       map[gpuTextureKey]*gpuTexture
	usedTextures   map[gpuTextureKey]struct{}
	texturePool    map[gpuTextureSize][]*ebiten.Image
	pooledBytes    int64
	motionTargets  map[runtime.LayerID]*ebiten.Image
	motionAtlases  map[runtime.LayerID]*gpuMotionAtlas
	shaders        map[gpuMethodKey]*ebiten.Shader
	vertices       []ebiten.Vertex
	indices        []uint16
	uniforms       map[string]any
	sourceOrigin   [2]float32
	sourceScale    [2]float32
	sourceOffset   [2]float32
	sourceMin      [2]float32
	sourceMax      [2]float32
	ruleOrigin     [2]float32
	crossfade      *ebiten.Shader
	universal      *ebiten.Shader
	stateHash      uint64
	stateHashValid bool
}

var gpuReplaceBlend = ebiten.Blend{
	BlendFactorSourceRGB:        ebiten.BlendFactorOne,
	BlendFactorSourceAlpha:      ebiten.BlendFactorOne,
	BlendFactorDestinationRGB:   ebiten.BlendFactorZero,
	BlendFactorDestinationAlpha: ebiten.BlendFactorZero,
	BlendOperationRGB:           ebiten.BlendOperationAdd,
	BlendOperationAlpha:         ebiten.BlendOperationAdd,
}
var gpuUnmanagedImageOptions = ebiten.NewImageOptions{Unmanaged: true}

func newGPUImage(width, height int) *ebiten.Image {
	// Keep dynamic textures out of Ebitengine's atlas, matching krkr2's mutable
	// GPU texture path for video and render targets.
	return ebiten.NewImageWithOptions(image.Rect(0, 0, width, height), &gpuUnmanagedImageOptions)
}

func NewGPUCompositor() (*GPUCompositor, error) {
	methods, crossfade, universal, err := compileGPUShaders()
	if err != nil {
		return nil, err
	}
	return &GPUCompositor{
		textures:      make(map[gpuTextureKey]*gpuTexture),
		usedTextures:  make(map[gpuTextureKey]struct{}),
		texturePool:   make(map[gpuTextureSize][]*ebiten.Image),
		motionTargets: make(map[runtime.LayerID]*ebiten.Image),
		motionAtlases: make(map[runtime.LayerID]*gpuMotionAtlas),
		shaders:       methods,
		crossfade:     crossfade,
		universal:     universal,
		indices:       []uint16{0, 1, 2, 2, 1, 3},
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
	for size, images := range c.texturePool {
		for _, image := range images {
			if image != nil {
				image.Deallocate()
			}
		}
		delete(c.texturePool, size)
	}
	c.usedTextures = nil
	c.pooledBytes = 0
	for id, target := range c.motionTargets {
		if target != nil {
			target.Deallocate()
		}
		delete(c.motionTargets, id)
	}
	for id, atlas := range c.motionAtlases {
		if atlas != nil && atlas.texture != nil {
			atlas.texture.Deallocate()
		}
		delete(c.motionAtlases, id)
	}
	c.shaders = nil
	c.crossfade, c.universal = nil, nil
	c.vertices, c.indices = nil, nil
	c.stateHash, c.stateHashValid = 0, false
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
		c.front, c.back = newGPUImage(width, height), newGPUImage(width, height)
		c.width, c.height = width, height
	}
	bg := color.RGBA{R: background[0], G: background[1], B: background[2], A: background[3]}
	c.front.Fill(bg)
	// Shader paths replace the complete back target through copyFrontToBack
	// before drawing. Filling it here would repeat a full-frame GPU clear.
	return nil
}

const (
	gpuTexturePoolLimit     = 32
	gpuTexturePoolByteLimit = 256 << 20
)

func (c *GPUCompositor) acquireTexture(width, height int32) *ebiten.Image {
	size := gpuTextureSize{width: width, height: height}
	if images := c.texturePool[size]; len(images) != 0 {
		image := images[len(images)-1]
		c.texturePool[size] = images[:len(images)-1]
		c.pooledBytes -= int64(width) * int64(height) * 4
		if image != nil {
			return image
		}
	}
	return newGPUImage(int(width), int(height))
}

func (c *GPUCompositor) releaseTexture(image *ebiten.Image, width, height int32) {
	if image == nil {
		return
	}
	size := gpuTextureSize{width: width, height: height}
	bytes := int64(width) * int64(height) * 4
	if len(c.texturePool[size]) >= gpuTexturePoolLimit || c.pooledBytes+bytes > gpuTexturePoolByteLimit {
		image.Deallocate()
		return
	}
	c.texturePool[size] = append(c.texturePool[size], image)
	c.pooledBytes += bytes
}

func (c *GPUCompositor) beginTextureFrame() {
	clear(c.usedTextures)
}

func (c *GPUCompositor) evictUnusedTextures() {
	for key, texture := range c.textures {
		if _, ok := c.usedTextures[key]; ok {
			continue
		}
		if texture != nil && texture.image != nil {
			c.releaseTexture(texture.image, texture.width, texture.height)
		}
		delete(c.textures, key)
	}
}

func premultipliedRegion(src []byte, srcWidth int32, dirty runtime.Rect, dst []byte, opaque bool) {
	rowBytes := int(dirty.Width) * 4
	for y := int32(0); y < dirty.Height; y++ {
		sourceStart := (int(dirty.Y+y)*int(srcWidth) + int(dirty.X)) * 4
		source := src[sourceStart : sourceStart+rowBytes]
		target := dst[int(y)*rowBytes : int(y+1)*rowBytes]
		premultipliedPixels(source, target, opaque)
	}
}

func premultipliedPixels(src []byte, dst []byte, opaque bool) {
	for i := 0; i+3 < len(src); i += 4 {
		a := uint16(src[i+3])
		if opaque {
			a = 255
		}
		dst[i+3] = byte(a)
		dst[i] = byte((uint16(src[i])*a + 127) / 255)
		dst[i+1] = byte((uint16(src[i+1])*a + 127) / 255)
		dst[i+2] = byte((uint16(src[i+2])*a + 127) / 255)
	}
}

func (c *GPUCompositor) texture(key gpuTextureKey, width, height int32, src []byte, hash uint64, opaque bool, dirty runtime.Rect, dirtyValid bool) (*ebiten.Image, error) {
	if width <= 0 || height <= 0 || uint64(width)*uint64(height)*4 != uint64(len(src)) {
		return nil, fmt.Errorf("invalid GPU texture %d: %dx%d with %d bytes", key.id, width, height, len(src))
	}
	c.usedTextures[key] = struct{}{}
	t := c.textures[key]
	if t == nil || t.width != width || t.height != height || t.image == nil {
		if t != nil && t.image != nil {
			c.releaseTexture(t.image, t.width, t.height)
		}
		t = &gpuTexture{image: c.acquireTexture(width, height), width: width, height: height}
		t.upload = make([]byte, len(src))
		c.textures[key] = t
		t.hash = ^uint64(0)
	}
	if t.hash != hash || t.opaque != opaque || len(t.upload) != len(src) {
		partial := dirtyValid && t.uploaded && t.opaque == opaque && dirty.X >= 0 && dirty.Y >= 0 &&
			dirty.Right() <= width && dirty.Bottom() <= height && !dirty.Empty() &&
			int64(dirty.Width)*int64(dirty.Height) < int64(width)*int64(height)
		if partial {
			partialBytes := int(dirty.Width) * int(dirty.Height) * 4
			if cap(t.upload) < partialBytes {
				t.upload = make([]byte, partialBytes)
			} else {
				t.upload = t.upload[:partialBytes]
			}
			premultipliedRegion(src, width, dirty, t.upload, opaque)
			t.image.SubImage(image.Rect(int(dirty.X), int(dirty.Y), int(dirty.Right()), int(dirty.Bottom()))).(*ebiten.Image).WritePixels(t.upload)
			t.upload = t.upload[:len(src)]
		} else {
			if cap(t.upload) < len(src) {
				t.upload = make([]byte, len(src))
			} else {
				t.upload = t.upload[:len(src)]
			}
			premultipliedPixels(src, t.upload, opaque)
			t.image.WritePixels(t.upload)
		}
		t.hash = hash
		t.uploaded = true
	}
	t.opaque = opaque
	return t.image, nil
}

func (c *GPUCompositor) copyFrontToBack() {
	c.back.DrawImage(c.front, &ebiten.DrawImageOptions{Blend: gpuReplaceBlend})
}

func (c *GPUCompositor) shaderUniforms() map[string]any {
	if c.uniforms == nil {
		c.uniforms = make(map[string]any, 8)
	} else {
		clear(c.uniforms)
	}
	return c.uniforms
}

func (c *GPUCompositor) drawQuad(target *ebiten.Image, shader *ebiten.Shader, images [4]*ebiten.Image, x0, y0, x1, y1, sx, sy float32, uniforms map[string]any) {
	c.vertices = c.vertices[:0]
	c.vertices = append(c.vertices,
		ebiten.Vertex{DstX: x0, DstY: y0, SrcX: x0, SrcY: y0},
		ebiten.Vertex{DstX: x1, DstY: y0, SrcX: x1, SrcY: y0},
		ebiten.Vertex{DstX: x0, DstY: y1, SrcX: x0, SrcY: y1},
		ebiten.Vertex{DstX: x1, DstY: y1, SrcX: x1, SrcY: y1},
	)
	c.sourceOrigin[0], c.sourceOrigin[1] = sx, sy
	uniforms["SourceOrigin"] = c.sourceOrigin[:]
	target.DrawTrianglesShader(c.vertices, c.indices, shader, &ebiten.DrawTrianglesShaderOptions{
		Blend: gpuReplaceBlend, Uniforms: uniforms, Images: images,
	})
}

func motionSourceHash(draw runtime.RenderMotionDraw) uint64 {
	return uint64(len(draw.RGBA)) ^ uint64(draw.Width)<<32 ^ uint64(draw.Height)
}

func nextMotionAtlasCapacity(height int) int {
	capacity := 1
	for capacity < height {
		capacity <<= 1
	}
	return capacity
}

func (c *GPUCompositor) rebuildMotionAtlas(atlas *gpuMotionAtlas) {
	if atlas == nil {
		return
	}
	keys := make([]string, 0, len(atlas.entries))
	for key := range atlas.entries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	width, usedHeight := 1, 0
	for _, key := range keys {
		entry := atlas.entries[key]
		if int(entry.width) > width {
			width = int(entry.width)
		}
		usedHeight += int(entry.height)
	}
	if usedHeight == 0 {
		if atlas.texture != nil {
			atlas.texture.Deallocate()
		}
		atlas.texture, atlas.width, atlas.height = nil, 0, 0
		atlas.usedHeight, atlas.capacityHeight = 0, 0
		return
	}
	capacityHeight := nextMotionAtlasCapacity(usedHeight)
	nextY := 0
	texture := newGPUImage(width, capacityHeight)
	for _, key := range keys {
		entry := atlas.entries[key]
		entry.rect = image.Rect(0, nextY, int(entry.width), nextY+int(entry.height))
		entry.view = texture.SubImage(entry.rect).(*ebiten.Image)
		entry.view.WritePixels(entry.upload)
		nextY += int(entry.height)
	}
	if atlas.texture != nil {
		atlas.texture.Deallocate()
	}
	atlas.texture = texture
	atlas.width, atlas.height = width, capacityHeight
	atlas.usedHeight, atlas.capacityHeight = usedHeight, capacityHeight
}

func motionSourceKey(draw runtime.RenderMotionDraw) string {
	if draw.SourceKey != "" {
		return draw.SourceKey
	}
	return fmt.Sprintf("\x00%d:%d:%d", len(draw.RGBA), draw.Width, draw.Height)
}

func (c *GPUCompositor) motionAtlasSource(nodeID runtime.LayerID, draw runtime.RenderMotionDraw) (*ebiten.Image, error) {
	sourceKey := motionSourceKey(draw)
	if draw.Width <= 0 || draw.Height <= 0 ||
		uint64(draw.Width)*uint64(draw.Height)*4 != uint64(len(draw.RGBA)) {
		return nil, fmt.Errorf("invalid deferred motion source for layer %d", nodeID)
	}
	if c.motionAtlases == nil {
		c.motionAtlases = make(map[runtime.LayerID]*gpuMotionAtlas)
	}
	atlas := c.motionAtlases[nodeID]
	if atlas == nil {
		atlas = &gpuMotionAtlas{entries: make(map[string]*gpuMotionAtlasEntry)}
		c.motionAtlases[nodeID] = atlas
	}
	hash := motionSourceHash(draw)
	entry := atlas.entries[sourceKey]
	if entry != nil && entry.width == draw.Width && entry.height == draw.Height && entry.hash == hash && entry.view != nil {
		return entry.view, nil
	}
	upload := make([]byte, len(draw.RGBA))
	premultipliedPixels(draw.RGBA, upload, false)
	if entry != nil && entry.width == draw.Width && entry.height == draw.Height && entry.view != nil {
		entry.hash, entry.upload = hash, upload
		entry.view.WritePixels(upload)
		return entry.view, nil
	}
	entry = &gpuMotionAtlasEntry{
		width: draw.Width, height: draw.Height, hash: hash, upload: upload,
	}
	atlas.entries[sourceKey] = entry
	if atlas.texture != nil && int(draw.Width) <= atlas.width &&
		atlas.usedHeight+int(draw.Height) <= atlas.capacityHeight {
		entry.rect = image.Rect(0, atlas.usedHeight, int(draw.Width), atlas.usedHeight+int(draw.Height))
		entry.view = atlas.texture.SubImage(entry.rect).(*ebiten.Image)
		entry.view.WritePixels(upload)
		atlas.usedHeight += int(draw.Height)
		return entry.view, nil
	}
	c.rebuildMotionAtlas(atlas)
	return entry.view, nil
}

func (c *GPUCompositor) composeMotion(node runtime.RenderNode) (*ebiten.Image, error) {
	motion := node.Motion
	if motion == nil || node.ImageWidth <= 0 || node.ImageHeight <= 0 {
		return nil, fmt.Errorf("invalid deferred motion target for layer %d", node.ID)
	}
	if c.motionTargets == nil {
		c.motionTargets = make(map[runtime.LayerID]*ebiten.Image)
	}
	target := c.motionTargets[node.ID]
	if target == nil || target.Bounds().Dx() != int(node.ImageWidth) || target.Bounds().Dy() != int(node.ImageHeight) {
		if target != nil {
			target.Deallocate()
		}
		target = newGPUImage(int(node.ImageWidth), int(node.ImageHeight))
		c.motionTargets[node.ID] = target
	}
	target.Clear()
	filter := ebiten.FilterNearest
	if motion.Filter != 0 {
		filter = ebiten.FilterLinear
	}
	for _, draw := range motion.Draws {
		if _, err := c.motionAtlasSource(node.ID, draw); err != nil {
			return nil, err
		}
	}
	for _, draw := range motion.Draws {
		source := c.motionAtlases[node.ID].entries[motionSourceKey(draw)].view
		var geo ebiten.GeoM
		geo.SetElement(0, 0, draw.A)
		geo.SetElement(0, 1, draw.B)
		geo.SetElement(0, 2, draw.TX)
		geo.SetElement(1, 0, draw.C)
		geo.SetElement(1, 1, draw.D)
		geo.SetElement(1, 2, draw.TY)
		options := &ebiten.DrawImageOptions{
			GeoM:           geo,
			Blend:          ebiten.BlendSourceOver,
			Filter:         filter,
			DisableMipmaps: true,
		}
		options.ColorScale.ScaleAlpha(float32(draw.Opacity) / 255)
		target.DrawImage(source, options)
	}
	return target, nil
}

func (c *GPUCompositor) transitionCoversTarget(ax, ay, imageLeft, imageTop, x0, y0, x1, y1 int32) bool {
	return ax+imageLeft+x0 == 0 && ay+imageTop+y0 == 0 &&
		ax+imageLeft+x1 == int32(c.width) && ay+imageTop+y1 == int32(c.height)
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
			if n.Transition != nil {
				uniforms := c.shaderUniforms()
				tr := n.Transition
				dest, err := c.texture(gpuTextureKey{id: n.ID, role: gpuTextureDestination}, tr.ImageWidth, tr.ImageHeight, tr.DestinationRGBA, tr.DestinationHash, tr.Opaque, runtime.Rect{}, false)
				if err != nil {
					return err
				}
				src, err := c.texture(gpuTextureKey{id: n.ID, role: gpuTextureSource}, tr.ImageWidth, tr.ImageHeight, tr.SourceRGBA, tr.SourceHash, tr.Opaque, runtime.Rect{}, false)
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
					c.ruleOrigin[0], c.ruleOrigin[1] = float32(ax+n.ImageLeft), float32(ay+n.ImageTop)
					uniforms["RuleOrigin"] = c.ruleOrigin[:]
					images := [4]*ebiten.Image{c.front, dest, src, rule}
					if !c.transitionCoversTarget(ax, ay, n.ImageLeft, n.ImageTop, int32(x0), int32(y0), int32(x1), int32(y1)) {
						c.copyFrontToBack()
					}
					c.drawQuad(c.back, shader, images, float32(ax+n.ImageLeft+int32(x0)), float32(ay+n.ImageTop+int32(y0)), float32(ax+n.ImageLeft+int32(x1)), float32(ay+n.ImageTop+int32(y1)), float32(ax+n.ImageLeft), float32(ay+n.ImageTop), uniforms)
				} else {
					images := [4]*ebiten.Image{c.front, dest, src, nil}
					if !c.transitionCoversTarget(ax, ay, n.ImageLeft, n.ImageTop, int32(x0), int32(y0), int32(x1), int32(y1)) {
						c.copyFrontToBack()
					}
					c.drawQuad(c.back, shader, images, float32(ax+n.ImageLeft+int32(x0)), float32(ay+n.ImageTop+int32(y0)), float32(ax+n.ImageLeft+int32(x1)), float32(ay+n.ImageTop+int32(y1)), float32(ax+n.ImageLeft), float32(ay+n.ImageTop), uniforms)
				}
				c.front, c.back = c.back, c.front
			} else {
				sourceWidth, sourceHeight := n.ImageWidth, n.ImageHeight
				if n.Motion != nil {
					source, err = c.composeMotion(n)
				} else {
					if n.GPUStretch {
						sourceWidth, sourceHeight = n.GPUStretchSourceWidth, n.GPUStretchSourceHeight
					}
					source, err = c.texture(gpuTextureKey{id: n.ID, role: gpuTextureLayer}, sourceWidth, sourceHeight, n.RGBA, n.ImageHash, n.Type == runtime.LayerOpaque, n.ImageDirty, n.ImageDirtyValid && !n.GPUStretch && n.Motion == nil)
				}
				if err != nil {
					return err
				}
				if n.Type == runtime.LayerOpaque {
					c.drawOpaqueLayer(source, x0, y0, x1, y1, ax, ay, n)
				} else if n.Type == runtime.LayerAlpha && targetType == runtime.LayerBinder {
					c.drawAlphaLayer(source, x0, y0, x1, y1, ax, ay, n)
				} else {
					shader = c.shaders[gpuMethodKey{n.Type, targetType, n.HoldAlpha}]
					if shader == nil {
						return fmt.Errorf("unsupported GPU layer type %d", n.Type)
					}
					c.copyFrontToBack()
					uniforms := c.shaderUniforms()
					uniforms["Mode"] = float32(n.Type)
					uniforms["TargetType"] = float32(targetType)
					uniforms["Opacity"] = float32(n.Opacity) / 255
					uniforms["HoldAlpha"] = boolToFloat(n.HoldAlpha)
					destinationWidth, destinationHeight := n.ImageWidth, n.ImageHeight
					if n.GPUStretch {
						destinationWidth, destinationHeight = n.GPUStretchDestinationWidth, n.GPUStretchDestinationHeight
					}
					c.sourceScale[0], c.sourceScale[1] = 1, 1
					c.sourceOffset[0], c.sourceOffset[1] = 0, 0
					c.sourceMin[0], c.sourceMin[1] = 0.5, 0.5
					c.sourceMax[0], c.sourceMax[1] = float32(sourceWidth)-0.5, float32(sourceHeight)-0.5
					if n.GPUStretch && n.GPUStretchCropWidth > 0 && n.GPUStretchCropHeight > 0 &&
						destinationWidth > 0 && destinationHeight > 0 {
						c.sourceScale[0] = float32(n.GPUStretchCropWidth) / float32(destinationWidth)
						c.sourceScale[1] = float32(n.GPUStretchCropHeight) / float32(destinationHeight)
						c.sourceOffset[0] = float32(n.GPUStretchSourceX) - float32(n.GPUStretchDestinationX)*c.sourceScale[0]
						c.sourceOffset[1] = float32(n.GPUStretchSourceY) - float32(n.GPUStretchDestinationY)*c.sourceScale[1]
						c.sourceMin[0] = float32(n.GPUStretchSourceX) + 0.5
						c.sourceMin[1] = float32(n.GPUStretchSourceY) + 0.5
						c.sourceMax[0] = float32(n.GPUStretchSourceX+n.GPUStretchCropWidth) - 0.5
						c.sourceMax[1] = float32(n.GPUStretchSourceY+n.GPUStretchCropHeight) - 0.5
					}
					uniforms["SourceScale"] = c.sourceScale[:]
					uniforms["SourceOffset"] = c.sourceOffset[:]
					uniforms["SourceMin"] = c.sourceMin[:]
					uniforms["SourceMax"] = c.sourceMax[:]
					if n.GPUStretch || n.Motion != nil {
						uniforms["SourceLinear"] = float32(1)
					} else {
						uniforms["SourceLinear"] = float32(0)
					}
					c.drawQuad(c.back, shader, [4]*ebiten.Image{c.front, source, nil, nil}, float32(ax+n.ImageLeft+int32(x0)), float32(ay+n.ImageTop+int32(y0)), float32(ax+n.ImageLeft+int32(x1)), float32(ay+n.ImageTop+int32(y1)), float32(ax+n.ImageLeft), float32(ay+n.ImageTop), uniforms)
					c.front, c.back = c.back, c.front
				}
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

func gpuSourceRect(n runtime.RenderNode, x0, y0, x1, y1 int64) (image.Rectangle, float64, float64) {
	if !n.GPUStretch {
		return image.Rect(int(x0), int(y0), int(x1), int(y1)), 1, 1
	}
	sourceWidth, sourceHeight := int64(n.GPUStretchSourceWidth), int64(n.GPUStretchSourceHeight)
	destinationWidth, destinationHeight := int64(n.GPUStretchDestinationWidth), int64(n.GPUStretchDestinationHeight)
	cropWidth, cropHeight := int64(n.GPUStretchCropWidth), int64(n.GPUStretchCropHeight)
	sourceX := int64(n.GPUStretchSourceX)
	sourceY := int64(n.GPUStretchSourceY)
	destinationX := int64(n.GPUStretchDestinationX)
	destinationY := int64(n.GPUStretchDestinationY)
	if cropWidth <= 0 || cropHeight <= 0 || destinationWidth <= 0 || destinationHeight <= 0 {
		return image.Rect(0, 0, int(sourceWidth), int(sourceHeight)), 1, 1
	}
	sourceX0 := sourceX + (x0-destinationX)*cropWidth/destinationWidth
	sourceY0 := sourceY + (y0-destinationY)*cropHeight/destinationHeight
	sourceX1 := sourceX + (x1-destinationX)*cropWidth/destinationWidth
	sourceY1 := sourceY + (y1-destinationY)*cropHeight/destinationHeight
	if sourceX0 < 0 {
		sourceX0 = 0
	}
	if sourceY0 < 0 {
		sourceY0 = 0
	}
	if sourceX1 <= sourceX0 {
		sourceX1 = sourceX0 + 1
	}
	if sourceY1 <= sourceY0 {
		sourceY1 = sourceY0 + 1
	}
	if sourceX1 > sourceWidth {
		sourceX1 = sourceWidth
	}
	if sourceY1 > sourceHeight {
		sourceY1 = sourceHeight
	}
	return image.Rect(int(sourceX0), int(sourceY0), int(sourceX1), int(sourceY1)),
		float64(destinationWidth) / float64(cropWidth),
		float64(destinationHeight) / float64(cropHeight)
}

func (c *GPUCompositor) drawOpaqueLayer(source *ebiten.Image, x0, y0, x1, y1 int64, ax, ay int32, n runtime.RenderNode) {
	sourceRect, scaleX, scaleY := gpuSourceRect(n, x0, y0, x1, y1)
	visible := source.SubImage(sourceRect).(*ebiten.Image)
	blend := ebiten.BlendSourceOver
	if n.Opacity == 255 {
		blend = gpuReplaceBlend
	}
	filter := ebiten.FilterNearest
	if n.GPUStretch || n.Motion != nil {
		filter = ebiten.FilterLinear
	}
	options := &ebiten.DrawImageOptions{Blend: blend, Filter: filter, DisableMipmaps: n.GPUStretch || n.Motion != nil}
	if n.GPUStretch {
		options.GeoM.Scale(scaleX, scaleY)
		options.GeoM.Translate(float64(ax+n.ImageLeft+int32(x0))-float64(sourceRect.Min.X)*scaleX,
			float64(ay+n.ImageTop+int32(y0))-float64(sourceRect.Min.Y)*scaleY)
	} else {
		options.GeoM.Translate(float64(ax+n.ImageLeft+int32(x0)), float64(ay+n.ImageTop+int32(y0)))
	}
	options.ColorScale.ScaleAlpha(float32(n.Opacity) / 255)
	c.front.DrawImage(visible, options)
}

func (c *GPUCompositor) drawAlphaLayer(source *ebiten.Image, x0, y0, x1, y1 int64, ax, ay int32, n runtime.RenderNode) {
	sourceRect, scaleX, scaleY := gpuSourceRect(n, x0, y0, x1, y1)
	visible := source.SubImage(sourceRect).(*ebiten.Image)
	filter := ebiten.FilterNearest
	if n.GPUStretch || n.Motion != nil {
		filter = ebiten.FilterLinear
	}
	options := &ebiten.DrawImageOptions{Blend: ebiten.BlendSourceOver, Filter: filter, DisableMipmaps: n.GPUStretch || n.Motion != nil}
	if n.GPUStretch {
		options.GeoM.Scale(scaleX, scaleY)
		options.GeoM.Translate(float64(ax+n.ImageLeft+int32(x0))-float64(sourceRect.Min.X)*scaleX,
			float64(ay+n.ImageTop+int32(y0))-float64(sourceRect.Min.Y)*scaleY)
	} else {
		options.GeoM.Translate(float64(ax+n.ImageLeft+int32(x0)), float64(ay+n.ImageTop+int32(y0)))
	}
	options.ColorScale.ScaleAlpha(float32(n.Opacity) / 255)
	c.front.DrawImage(visible, options)
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
	key := gpuTextureKey{id: id, role: gpuTextureRule}
	c.usedTextures[key] = struct{}{}
	t := c.textures[key]
	if t == nil || t.width != tr.RuleWidth || t.height != tr.RuleHeight || t.image == nil {
		if t != nil && t.image != nil {
			c.releaseTexture(t.image, t.width, t.height)
		}
		t = &gpuTexture{image: c.acquireTexture(tr.RuleWidth, tr.RuleHeight), width: tr.RuleWidth, height: tr.RuleHeight, hash: ^uint64(0)}
		t.upload = make([]byte, len(tr.Rule)*4)
		c.textures[key] = t
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
	c.beginTextureFrame()
	if len(plan.Nodes) != 0 {
		root := 0
		if err := c.drawNode(plan, root, plan.Nodes[root].Type); err != nil {
			return nil, err
		}
	}
	c.evictUnusedTextures()
	return c.front, nil
}

func (c *GPUCompositor) composeStable(plan runtime.RenderPlan) (*ebiten.Image, error) {
	if c == nil {
		return nil, fmt.Errorf("nil GPU compositor")
	}
	if plan.Width <= 0 || plan.Height <= 0 {
		return nil, fmt.Errorf("invalid render-plan dimensions %dx%d", plan.Width, plan.Height)
	}
	if plan.StateHashValid && c.stateHashValid && c.stateHash == plan.StateHash {
		return c.front, nil
	}
	image, err := c.Compose(plan)
	if err != nil {
		return nil, err
	}
	if plan.StateHashValid {
		c.stateHash = plan.StateHash
		c.stateHashValid = true
	} else {
		c.stateHashValid = false
	}
	return image, nil
}

func (c *GPUCompositor) invalidateStateCache() {
	if c != nil {
		c.stateHashValid = false
	}
}
