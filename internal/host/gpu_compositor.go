package host

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"sort"

	runtime "github.com/eslzzyl/krkrgo/internal/runtime"
	"github.com/eslzzyl/krkrgo/internal/simd"
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
	opaque        bool
	uploaded      bool
}

type gpuTextureSize struct {
	width, height int32
}
type gpuMotionAtlasEntry struct {
	rect           image.Rectangle
	storage        image.Rectangle
	view           *ebiten.Image
	width          int32
	height         int32
	hash           uint64
	lastUsed       uint64
	sharedLastUsed uint64
}

// gpuMotionAtlas keeps all source views for one motion node on one backing
// texture, allowing successive transformed draws to stay in the same batch.
type gpuMotionAtlas struct {
	texture                    *ebiten.Image
	width, height              int
	usedHeight, capacityHeight int
	padding                    bool
	frame                      uint64
	lastSharedUse              uint64
	entries                    map[string]*gpuMotionAtlasEntry
	freeSlots                  []image.Rectangle
}

// gpuMotionStream holds the canvas a mesh or stencil motion bakes for one tick.
// Those pixels have no reusable source identity, so they replace the previous
// frame in one texture instead of entering the source atlas.
type gpuMotionStream struct {
	image         *ebiten.Image
	width, height int32
}

// GPUCompositor consumes the borrowed runtime RenderPlan synchronously. The
// plan's pixel slices are never retained; one shared scratch buffer carries
// premultiplied pixels into Ebitengine and is reused across uploads.
type GPUCompositor struct {
	width, height       int
	front, back         *ebiten.Image
	textures            map[gpuTextureKey]*gpuTexture
	usedTextures        map[gpuTextureKey]struct{}
	texturePool         map[gpuTextureSize][]*ebiten.Image
	pooledBytes         int64
	motionTargets       map[runtime.LayerID]*ebiten.Image
	motionAtlases       map[runtime.LayerID]*gpuMotionAtlas
	sharedMotionAtlases map[string]*gpuMotionAtlas
	motionStreams       map[runtime.LayerID]*gpuMotionStream
	shaders             map[gpuMethodKey]*ebiten.Shader
	vertices            []ebiten.Vertex
	motionVertices      []ebiten.Vertex
	motionIndices       []uint16
	motionTriangles     ebiten.DrawTrianglesOptions
	motionBatching      bool
	motionBatchActive   bool
	motionBatchTarget   *ebiten.Image
	motionBatchAtlas    *gpuMotionAtlas
	motionBatchTexture  *ebiten.Image
	motionBatchFilter   ebiten.Filter
	motionBatchRect     image.Rectangle
	indices             []uint16
	uploadScratch       []byte
	motionPaddedScratch []byte
	uniforms            map[string]any
	sourceOrigin        [2]float32
	sourceScale         [2]float32
	sourceOffset        [2]float32
	sourceMin           [2]float32
	sourceMax           [2]float32
	ruleOrigin          [2]float32
	crossfade           *ebiten.Shader
	universal           *ebiten.Shader
	motionMask          *ebiten.Shader
	motionFrame         uint64
	stateHash           uint64
	stateHashValid      bool
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
	motionMask, err := compileMotionMaskShader()
	if err != nil {
		crossfade.Deallocate()
		universal.Deallocate()
		return nil, err
	}
	return &GPUCompositor{
		textures:            make(map[gpuTextureKey]*gpuTexture),
		usedTextures:        make(map[gpuTextureKey]struct{}),
		texturePool:         make(map[gpuTextureSize][]*ebiten.Image),
		motionTargets:       make(map[runtime.LayerID]*ebiten.Image),
		motionAtlases:       make(map[runtime.LayerID]*gpuMotionAtlas),
		sharedMotionAtlases: make(map[string]*gpuMotionAtlas),
		motionStreams:       make(map[runtime.LayerID]*gpuMotionStream),
		shaders:             methods,
		crossfade:           crossfade,
		universal:           universal,
		motionMask:          motionMask,
		indices:             []uint16{0, 1, 2, 2, 1, 3},
	}, nil
}

// GPUStats reports CPU-side GPU compositor storage. It excludes driver VRAM.
type GPUStats struct {
	Textures           int
	TextureBytes       int64
	MotionAtlases      int
	MotionAtlasBytes   int64
	UploadScratchBytes int64
	TargetBytes        int64
}

// Stats returns current logical storage sizes. The values are safe to inspect
// headlessly and do not require a presented frame.
func (c *GPUCompositor) Stats() GPUStats {
	if c == nil {
		return GPUStats{}
	}
	var stats GPUStats
	for _, texture := range c.textures {
		if texture == nil || texture.image == nil {
			continue
		}
		stats.Textures++
		stats.TextureBytes += int64(texture.width) * int64(texture.height) * 4
	}
	for _, atlas := range c.motionAtlases {
		if atlas == nil {
			continue
		}
		stats.MotionAtlases++
		if atlas.texture != nil {
			stats.MotionAtlasBytes += int64(atlas.width) * int64(atlas.height) * 4
		}
	}
	for _, atlas := range c.sharedMotionAtlases {
		if atlas == nil {
			continue
		}
		stats.MotionAtlases++
		if atlas.texture != nil {
			stats.MotionAtlasBytes += int64(atlas.width) * int64(atlas.height) * 4
		}
	}
	stats.UploadScratchBytes = int64(cap(c.uploadScratch))
	if padded := int64(cap(c.motionPaddedScratch)); padded > stats.UploadScratchBytes {
		stats.UploadScratchBytes = padded
	}
	if c.front != nil && c.back != nil {
		stats.TargetBytes = 2 * int64(c.width) * int64(c.height) * 4
	}
	return stats
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
	if c.motionMask != nil {
		seen[c.motionMask] = struct{}{}
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
	for key, atlas := range c.sharedMotionAtlases {
		if atlas != nil && atlas.texture != nil {
			atlas.texture.Deallocate()
		}
		delete(c.sharedMotionAtlases, key)
	}
	for id, stream := range c.motionStreams {
		if stream != nil && stream.image != nil {
			stream.image.Deallocate()
		}
		delete(c.motionStreams, id)
	}
	c.shaders = nil
	c.crossfade, c.universal = nil, nil
	c.vertices, c.motionVertices, c.motionIndices, c.indices = nil, nil, nil, nil
	c.motionPaddedScratch = nil
	c.motionFrame = 0
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
	if simd.AllAlphaOpaque(src) {
		copy(dst, src)
		return
	}
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

// scratch returns a compositor-owned upload buffer of exactly n bytes. The
// buffer is valid until the next call and is never retained with a texture.
func (c *GPUCompositor) scratch(n int) []byte {
	if cap(c.uploadScratch) < n {
		c.uploadScratch = make([]byte, n)
	} else {
		c.uploadScratch = c.uploadScratch[:n]
	}
	return c.uploadScratch
}

func (c *GPUCompositor) premultipliedUpload(src []byte, opaque bool) []byte {
	if simd.AllAlphaOpaque(src) {
		return src
	}
	upload := c.scratch(len(src))
	premultipliedPixels(src, upload, opaque)
	return upload
}

func (c *GPUCompositor) motionUpload(src []byte, opaque, premultiplied bool) []byte {
	if opaque || premultiplied {
		return src
	}
	return c.premultipliedUpload(src, false)
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
		c.textures[key] = t
		t.hash = ^uint64(0)
	}
	if t.hash != hash || t.opaque != opaque {
		partial := dirtyValid && t.uploaded && t.opaque == opaque && dirty.X >= 0 && dirty.Y >= 0 &&
			dirty.Right() <= width && dirty.Bottom() <= height && !dirty.Empty() &&
			int64(dirty.Width)*int64(dirty.Height) < int64(width)*int64(height)
		if partial {
			upload := c.scratch(int(dirty.Width) * int(dirty.Height) * 4)
			premultipliedRegion(src, width, dirty, upload, opaque)
			t.image.SubImage(image.Rect(int(dirty.X), int(dirty.Y), int(dirty.Right()), int(dirty.Bottom()))).(*ebiten.Image).WritePixels(upload)
		} else {
			upload := c.premultipliedUpload(src, opaque)
			t.image.WritePixels(upload)
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

const motionAtlasPadding = 1

func motionAtlasStorageSize(width, height int32, padded bool) (int, int) {
	if !padded {
		return int(width), int(height)
	}
	return int(width) + motionAtlasPadding*2, int(height) + motionAtlasPadding*2
}

func motionAtlasContentRect(storage image.Rectangle, width, height int32, padded bool) image.Rectangle {
	if !padded {
		return image.Rect(storage.Min.X, storage.Min.Y, storage.Min.X+int(width), storage.Min.Y+int(height))
	}
	return image.Rect(
		storage.Min.X+motionAtlasPadding,
		storage.Min.Y+motionAtlasPadding,
		storage.Min.X+motionAtlasPadding+int(width),
		storage.Min.Y+motionAtlasPadding+int(height),
	)
}

func (c *GPUCompositor) motionPaddedUpload(src []byte, width, height int32) []byte {
	storageWidth, storageHeight := motionAtlasStorageSize(width, height, true)
	rowBytes := int(width) * 4
	storageRowBytes := storageWidth * 4
	n := storageRowBytes * storageHeight
	if cap(c.motionPaddedScratch) < n {
		c.motionPaddedScratch = make([]byte, n)
	} else {
		c.motionPaddedScratch = c.motionPaddedScratch[:n]
	}
	out := c.motionPaddedScratch
	for y := 0; y < int(height); y++ {
		srcRow := src[y*rowBytes : (y+1)*rowBytes]
		rowStart := (y + motionAtlasPadding) * storageRowBytes
		dstRow := out[rowStart+motionAtlasPadding*4 : rowStart+storageRowBytes]
		copy(dstRow, srcRow)
		copy(out[rowStart:rowStart+4], srcRow[:4])
		rightStart := rowStart + storageRowBytes - 4
		copy(out[rightStart:rightStart+4], srcRow[len(srcRow)-4:])
	}
	copy(out[:storageRowBytes], out[storageRowBytes:2*storageRowBytes])
	last := (storageHeight - 1) * storageRowBytes
	previous := (storageHeight - 2) * storageRowBytes
	copy(out[last:last+storageRowBytes], out[previous:previous+storageRowBytes])
	return out
}

func motionAtlasStorageRect(entry *gpuMotionAtlasEntry) image.Rectangle {
	if entry == nil {
		return image.Rectangle{}
	}
	if !entry.storage.Empty() {
		return entry.storage
	}
	return image.Rect(
		entry.rect.Min.X-motionAtlasPadding,
		entry.rect.Min.Y-motionAtlasPadding,
		entry.rect.Max.X+motionAtlasPadding,
		entry.rect.Max.Y+motionAtlasPadding,
	)
}

func (c *GPUCompositor) writeMotionAtlasEntry(atlas *gpuMotionAtlas, entry *gpuMotionAtlasEntry, upload []byte) {
	if atlas == nil || atlas.texture == nil || entry == nil {
		return
	}
	entry.storage = motionAtlasStorageRect(entry)
	storage := atlas.texture.SubImage(entry.storage).(*ebiten.Image)
	storage.WritePixels(upload)
	entry.view = atlas.texture.SubImage(entry.rect).(*ebiten.Image)
}

func drawMotionAtlasCopy(dst, src *ebiten.Image, x, y float64) {
	if dst == nil || src == nil {
		return
	}
	var geo ebiten.GeoM
	geo.Translate(x, y)
	dst.DrawImage(src, &ebiten.DrawImageOptions{
		GeoM:           geo,
		Blend:          gpuReplaceBlend,
		Filter:         ebiten.FilterNearest,
		DisableMipmaps: true,
	})
}

func copyMotionAtlasEntry(dst, src *ebiten.Image, rect image.Rectangle, padded bool) {
	if dst == nil || src == nil || rect.Empty() {
		return
	}
	bounds := src.Bounds()
	drawMotionAtlasCopy(dst, src, float64(rect.Min.X), float64(rect.Min.Y))
	if !padded {
		return
	}
	top := src.SubImage(image.Rect(bounds.Min.X, bounds.Min.Y, bounds.Max.X, bounds.Min.Y+1)).(*ebiten.Image)
	bottom := src.SubImage(image.Rect(bounds.Min.X, bounds.Max.Y-1, bounds.Max.X, bounds.Max.Y)).(*ebiten.Image)
	left := src.SubImage(image.Rect(bounds.Min.X, bounds.Min.Y, bounds.Min.X+1, bounds.Max.Y)).(*ebiten.Image)
	right := src.SubImage(image.Rect(bounds.Max.X-1, bounds.Min.Y, bounds.Max.X, bounds.Max.Y)).(*ebiten.Image)
	drawMotionAtlasCopy(dst, top, float64(rect.Min.X), float64(rect.Min.Y-motionAtlasPadding))
	drawMotionAtlasCopy(dst, bottom, float64(rect.Min.X), float64(rect.Max.Y))
	drawMotionAtlasCopy(dst, left, float64(rect.Min.X-motionAtlasPadding), float64(rect.Min.Y))
	drawMotionAtlasCopy(dst, right, float64(rect.Max.X), float64(rect.Min.Y))
	topLeft := src.SubImage(image.Rect(bounds.Min.X, bounds.Min.Y, bounds.Min.X+1, bounds.Min.Y+1)).(*ebiten.Image)
	topRight := src.SubImage(image.Rect(bounds.Max.X-1, bounds.Min.Y, bounds.Max.X, bounds.Min.Y+1)).(*ebiten.Image)
	bottomLeft := src.SubImage(image.Rect(bounds.Min.X, bounds.Max.Y-1, bounds.Min.X+1, bounds.Max.Y)).(*ebiten.Image)
	bottomRight := src.SubImage(image.Rect(bounds.Max.X-1, bounds.Max.Y-1, bounds.Max.X, bounds.Max.Y)).(*ebiten.Image)
	drawMotionAtlasCopy(dst, topLeft, float64(rect.Min.X-motionAtlasPadding), float64(rect.Min.Y-motionAtlasPadding))
	drawMotionAtlasCopy(dst, topRight, float64(rect.Max.X), float64(rect.Min.Y-motionAtlasPadding))
	drawMotionAtlasCopy(dst, bottomLeft, float64(rect.Min.X-motionAtlasPadding), float64(rect.Max.Y))
	drawMotionAtlasCopy(dst, bottomRight, float64(rect.Max.X), float64(rect.Max.Y))
}

func (c *GPUCompositor) enableMotionAtlasPadding(atlas *gpuMotionAtlas) {
	if atlas == nil || atlas.padding {
		return
	}
	atlas.padding = true
	if len(atlas.entries) == 0 {
		if atlas.texture != nil {
			atlas.texture.Deallocate()
		}
		atlas.texture = nil
		atlas.width, atlas.height = 0, 0
		atlas.usedHeight, atlas.capacityHeight = 0, 0
		atlas.freeSlots = atlas.freeSlots[:0]
		return
	}
	c.rebuildMotionAtlas(atlas)
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
	atlas.freeSlots = atlas.freeSlots[:0]
	keys := make([]string, 0, len(atlas.entries))
	for key := range atlas.entries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	width, usedHeight := 1, 0
	for _, key := range keys {
		entry := atlas.entries[key]
		entryWidth, _ := motionAtlasStorageSize(entry.width, entry.height, atlas.padding)
		if entryWidth > width {
			width = entryWidth
		}
		_, entryHeight := motionAtlasStorageSize(entry.width, entry.height, atlas.padding)
		usedHeight += entryHeight
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
		_, entryHeight := motionAtlasStorageSize(entry.width, entry.height, atlas.padding)
		entry.storage = image.Rect(0, nextY, width, nextY+entryHeight)
		entry.rect = motionAtlasContentRect(entry.storage, entry.width, entry.height, atlas.padding)
		if entry.view != nil {
			copyMotionAtlasEntry(texture, entry.view, entry.rect, atlas.padding)
			entry.view = texture.SubImage(entry.rect).(*ebiten.Image)
		} else {
			entry.view = nil
		}
		nextY += entryHeight
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

func (c *GPUCompositor) motionAtlasSource(nodeID runtime.LayerID, atlas *gpuMotionAtlas, shared bool, padded bool, draw runtime.RenderMotionDraw) (*ebiten.Image, error) {
	sourceKey := motionSourceKey(draw)
	if draw.Width <= 0 || draw.Height <= 0 ||
		uint64(draw.Width)*uint64(draw.Height)*4 != uint64(len(draw.RGBA)) {
		return nil, fmt.Errorf("invalid deferred motion source for layer %d", nodeID)
	}
	if atlas == nil {
		return nil, fmt.Errorf("motion source atlas missing for layer %d", nodeID)
	}
	if padded {
		c.enableMotionAtlasPadding(atlas)
	}
	hash := motionSourceHash(draw)
	entry := atlas.entries[sourceKey]
	if entry != nil && entry.width == draw.Width && entry.height == draw.Height && entry.hash == hash && entry.view != nil {
		if shared {
			entry.sharedLastUsed = c.motionFrame
			atlas.lastSharedUse = c.motionFrame
		}
		return entry.view, nil
	}
	upload := c.motionUpload(draw.RGBA, draw.Opaque, draw.Premultiplied)
	if entry != nil && entry.width == draw.Width && entry.height == draw.Height && entry.view != nil {
		entry.hash = hash
		if shared {
			entry.sharedLastUsed = c.motionFrame
			atlas.lastSharedUse = c.motionFrame
		}
		if atlas.padding {
			upload = c.motionPaddedUpload(upload, draw.Width, draw.Height)
		}
		c.writeMotionAtlasEntry(atlas, entry, upload)
		return entry.view, nil
	}
	entry = &gpuMotionAtlasEntry{width: draw.Width, height: draw.Height, hash: hash}
	if previous := atlas.entries[sourceKey]; previous != nil && atlas.texture != nil && !previous.rect.Empty() {
		atlas.freeSlots = append(atlas.freeSlots, motionAtlasStorageRect(previous))
	}
	if shared {
		entry.sharedLastUsed = c.motionFrame
		atlas.lastSharedUse = c.motionFrame
	}
	atlas.entries[sourceKey] = entry
	if shared && len(atlas.freeSlots) == 0 {
		for staleKey, stale := range atlas.entries {
			if staleKey == sourceKey || stale == nil || stale.sharedLastUsed == c.motionFrame || stale.rect.Empty() {
				continue
			}
			atlas.freeSlots = append(atlas.freeSlots, motionAtlasStorageRect(stale))
			delete(atlas.entries, staleKey)
			break
		}
	}
	for index, slot := range atlas.freeSlots {
		storageWidth, storageHeight := motionAtlasStorageSize(draw.Width, draw.Height, atlas.padding)
		if slot.Dx() < storageWidth || slot.Dy() < storageHeight {
			continue
		}
		entry.storage = image.Rect(slot.Min.X, slot.Min.Y, slot.Min.X+storageWidth, slot.Min.Y+storageHeight)
		entry.rect = motionAtlasContentRect(entry.storage, entry.width, entry.height, atlas.padding)
		if atlas.padding {
			upload = c.motionPaddedUpload(upload, draw.Width, draw.Height)
		}
		c.writeMotionAtlasEntry(atlas, entry, upload)
		last := len(atlas.freeSlots) - 1
		atlas.freeSlots[index] = atlas.freeSlots[last]
		atlas.freeSlots = atlas.freeSlots[:last]
		return entry.view, nil
	}
	storageWidth, storageHeight := motionAtlasStorageSize(draw.Width, draw.Height, atlas.padding)
	if atlas.texture != nil && storageWidth <= atlas.width &&
		atlas.usedHeight+storageHeight <= atlas.capacityHeight {
		entry.storage = image.Rect(0, atlas.usedHeight, storageWidth, atlas.usedHeight+storageHeight)
		entry.rect = motionAtlasContentRect(entry.storage, entry.width, entry.height, atlas.padding)
		if atlas.padding {
			upload = c.motionPaddedUpload(upload, draw.Width, draw.Height)
		}
		c.writeMotionAtlasEntry(atlas, entry, upload)
		atlas.usedHeight += storageHeight
		return entry.view, nil
	}
	c.rebuildMotionAtlas(atlas)
	if entry.view == nil {
		if atlas.padding {
			upload = c.motionPaddedUpload(upload, draw.Width, draw.Height)
		}
		c.writeMotionAtlasEntry(atlas, entry, upload)
	}
	return entry.view, nil
}

func (c *GPUCompositor) motionAtlasFor(nodeID runtime.LayerID, key string) (*gpuMotionAtlas, bool) {
	if key == "" {
		if c.motionAtlases == nil {
			c.motionAtlases = make(map[runtime.LayerID]*gpuMotionAtlas)
		}
		atlas := c.motionAtlases[nodeID]
		if atlas == nil {
			atlas = &gpuMotionAtlas{entries: make(map[string]*gpuMotionAtlasEntry)}
			c.motionAtlases[nodeID] = atlas
		}
		return atlas, false
	}
	if c.sharedMotionAtlases == nil {
		c.sharedMotionAtlases = make(map[string]*gpuMotionAtlas)
	}
	atlas := c.sharedMotionAtlases[key]
	if atlas == nil {
		atlas = &gpuMotionAtlas{entries: make(map[string]*gpuMotionAtlasEntry)}
		c.sharedMotionAtlases[key] = atlas
	}
	return atlas, true
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
	atlas, shared := c.motionAtlasFor(node.ID, motion.AtlasKey)
	// Drop sources this frame no longer uses before uploading the new ones.
	if !shared {
		c.evictMotionAtlasEntries(node.ID, motion.Draws)
	}
	if c.motionBatching && shared && filter == ebiten.FilterLinear && !atlas.padding && len(atlas.entries) != 0 {
		c.flushMotionBatch()
	}
	for _, draw := range motion.Draws {
		if draw.Baked {
			continue
		}
		if _, err := c.motionAtlasSource(node.ID, atlas, shared, filter == ebiten.FilterLinear, draw); err != nil {
			return nil, err
		}
	}
	if len(motion.Items) != 0 {
		if err := c.drawMotionItems(node.ID, atlas, target, motion.Items, filter, 0, 0); err != nil {
			return nil, err
		}
		return target, nil
	}
	if err := c.drawMotionDraws(node.ID, atlas, target, motion.Draws, filter, 0, 0); err != nil {
		return nil, err
	}
	return target, nil
}

func (c *GPUCompositor) drawDirectMotionAlpha(node runtime.RenderNode, x0, y0, x1, y1 int64) (bool, error) {
	motion := node.Motion
	if motion == nil || !motionSupportsDirectAlpha(motion) {
		return false, nil
	}
	atlas, shared := c.motionAtlasFor(node.ID, motion.AtlasKey)
	if !shared {
		c.evictMotionAtlasEntries(node.ID, motion.Draws)
	}
	filter := motionFilter(motion)
	if c.motionBatching && shared && filter == ebiten.FilterLinear && !atlas.padding && len(atlas.entries) != 0 {
		c.flushMotionBatch()
	}
	for _, draw := range motion.Draws {
		if draw.Baked {
			continue
		}
		if c.motionBatching && shared {
			key := motionSourceKey(draw)
			entry := atlas.entries[key]
			if entry == nil || entry.view == nil || entry.width != draw.Width || entry.height != draw.Height || entry.hash != motionSourceHash(draw) {
				c.flushMotionBatch()
			}
		}
		if _, err := c.motionAtlasSource(node.ID, atlas, shared, filter == ebiten.FilterLinear, draw); err != nil {
			return true, err
		}
	}
	left := int64(node.Bounds.X) + int64(node.ImageLeft) + x0
	top := int64(node.Bounds.Y) + int64(node.ImageTop) + y0
	right := int64(node.Bounds.X) + int64(node.ImageLeft) + x1
	bottom := int64(node.Bounds.Y) + int64(node.ImageTop) + y1
	rect := image.Rect(int(left), int(top), int(right), int(bottom)).Intersect(c.front.Bounds())
	if rect.Empty() {
		return true, nil
	}
	target := c.front
	if rect != c.front.Bounds() {
		target = c.front.SubImage(rect).(*ebiten.Image)
	}
	originX := -float64(node.Bounds.X + node.ImageLeft)
	originY := -float64(node.Bounds.Y + node.ImageTop)
	opacity := float32(node.Opacity) / 255
	if c.motionBatching && motion.AtlasKey != "" {
		return true, c.appendDirectMotionBatch(node.ID, atlas, target, rect, filter, motion, originX, originY, opacity)
	}
	if c.motionBatching {
		c.flushMotionBatch()
	}
	if len(motion.Items) != 0 {
		return true, c.drawMotionItemsWithOpacity(node.ID, atlas, target, motion.Items, filter, originX, originY, opacity)
	}
	return true, c.drawMotionBatchWithOpacity(node.ID, atlas, target, motion.Draws, filter, originX, originY, opacity)
}

func motionSupportsDirectAlpha(motion *runtime.RenderMotion) bool {
	if motion == nil {
		return false
	}
	for _, draw := range motion.Draws {
		if draw.Baked || draw.Blend != runtime.MotionBlendAlpha {
			return false
		}
	}
	if len(motion.Items) == 0 {
		return true
	}
	for _, item := range motion.Items {
		if item.Group != nil || item.Draw.Baked || item.Draw.Blend != runtime.MotionBlendAlpha {
			return false
		}
	}
	return true
}

func motionDirectCandidate(node runtime.RenderNode, targetType runtime.LayerType) bool {
	return node.Transition == nil && node.Type == runtime.LayerAlpha &&
		targetType == runtime.LayerBinder && node.Motion != nil
}

func (c *GPUCompositor) flushMotionBatch() {
	if !c.motionBatchActive {
		return
	}
	if c.motionBatchTarget != nil && c.motionBatchTexture != nil && len(c.motionVertices) != 0 {
		c.motionTriangles.Blend = gpuMotionBlend(runtime.MotionBlendAlpha)
		c.motionTriangles.Filter = c.motionBatchFilter
		c.motionTriangles.DisableMipmaps = true
		c.motionTriangles.ColorScaleMode = ebiten.ColorScaleModePremultipliedAlpha
		c.motionBatchTarget.DrawTriangles(c.motionVertices, c.motionIndices, c.motionBatchTexture, &c.motionTriangles)
	}
	c.motionVertices = c.motionVertices[:0]
	c.motionIndices = c.motionIndices[:0]
	c.motionBatchActive = false
	c.motionBatchTarget = nil
	c.motionBatchAtlas = nil
	c.motionBatchTexture = nil
	c.motionBatchRect = image.Rectangle{}
}

func (c *GPUCompositor) appendDirectMotionBatch(nodeID runtime.LayerID, atlas *gpuMotionAtlas, target *ebiten.Image, rect image.Rectangle, filter ebiten.Filter, motion *runtime.RenderMotion, originX, originY float64, opacity float32) error {
	if len(motion.Items) == 0 && len(motion.Draws) == 0 {
		return nil
	}
	if !c.motionBatchActive || c.motionBatchAtlas != atlas || c.motionBatchTexture != atlas.texture ||
		c.motionBatchFilter != filter || c.motionBatchRect != rect {
		c.flushMotionBatch()
		c.motionBatchActive = true
		c.motionBatchTarget = target
		c.motionBatchAtlas = atlas
		c.motionBatchTexture = atlas.texture
		c.motionBatchFilter = filter
		c.motionBatchRect = rect
	}
	if len(motion.Items) != 0 {
		for _, item := range motion.Items {
			if err := c.appendMotionBatchDraw(nodeID, atlas, item.Draw, originX, originY, opacity); err != nil {
				return err
			}
		}
		return nil
	}
	for _, draw := range motion.Draws {
		if err := c.appendMotionBatchDraw(nodeID, atlas, draw, originX, originY, opacity); err != nil {
			return err
		}
	}
	return nil
}

func motionFilter(motion *runtime.RenderMotion) ebiten.Filter {
	if motion != nil && motion.Filter != 0 {
		return ebiten.FilterLinear
	}
	return ebiten.FilterNearest
}

func (c *GPUCompositor) drawMotionItems(nodeID runtime.LayerID, atlas *gpuMotionAtlas, target *ebiten.Image, items []runtime.RenderMotionItem, filter ebiten.Filter, originX, originY float64) error {
	return c.drawMotionItemsWithOpacity(nodeID, atlas, target, items, filter, originX, originY, 1)
}

func (c *GPUCompositor) drawMotionItemsWithOpacity(nodeID runtime.LayerID, atlas *gpuMotionAtlas, target *ebiten.Image, items []runtime.RenderMotionItem, filter ebiten.Filter, originX, originY float64, opacity float32) error {
	for index := 0; index < len(items); {
		if items[index].Group != nil {
			if err := c.drawMotionGroup(nodeID, atlas, target, items[index].Group, filter, originX, originY); err != nil {
				return err
			}
			index++
			continue
		}
		if items[index].Draw.Baked {
			if err := c.drawMotionDraw(nodeID, atlas, target, items[index].Draw, filter, originX, originY); err != nil {
				return err
			}
			index++
			continue
		}
		blend := items[index].Draw.Blend
		end := index + 1
		for end < len(items) && items[end].Group == nil && !items[end].Draw.Baked && items[end].Draw.Blend == blend {
			end++
		}
		if err := c.drawMotionItemBatchWithOpacity(nodeID, atlas, target, items[index:end], filter, originX, originY, opacity); err != nil {
			return err
		}
		index = end
	}
	return nil
}

func (c *GPUCompositor) drawMotionDraws(nodeID runtime.LayerID, atlas *gpuMotionAtlas, target *ebiten.Image, draws []runtime.RenderMotionDraw, filter ebiten.Filter, originX, originY float64) error {
	for index := 0; index < len(draws); {
		if draws[index].Baked {
			if err := c.drawMotionDraw(nodeID, atlas, target, draws[index], filter, originX, originY); err != nil {
				return err
			}
			index++
			continue
		}
		blend := draws[index].Blend
		end := index + 1
		for end < len(draws) && !draws[end].Baked && draws[end].Blend == blend {
			end++
		}
		if err := c.drawMotionBatch(nodeID, atlas, target, draws[index:end], filter, originX, originY); err != nil {
			return err
		}
		index = end
	}
	return nil
}

func (c *GPUCompositor) drawMotionItemBatch(nodeID runtime.LayerID, atlas *gpuMotionAtlas, target *ebiten.Image, items []runtime.RenderMotionItem, filter ebiten.Filter, originX, originY float64) error {
	return c.drawMotionItemBatchWithOpacity(nodeID, atlas, target, items, filter, originX, originY, 1)
}

func (c *GPUCompositor) drawMotionItemBatchWithOpacity(nodeID runtime.LayerID, atlas *gpuMotionAtlas, target *ebiten.Image, items []runtime.RenderMotionItem, filter ebiten.Filter, originX, originY float64, opacity float32) error {
	c.motionVertices = c.motionVertices[:0]
	c.motionIndices = c.motionIndices[:0]
	for _, item := range items {
		if err := c.appendMotionBatchDraw(nodeID, atlas, item.Draw, originX, originY, opacity); err != nil {
			return err
		}
	}
	if len(c.motionVertices) == 0 {
		return nil
	}
	c.motionTriangles.Blend = gpuMotionBlend(items[0].Draw.Blend)
	c.motionTriangles.Filter = filter
	c.motionTriangles.DisableMipmaps = true
	c.motionTriangles.ColorScaleMode = ebiten.ColorScaleModePremultipliedAlpha
	target.DrawTriangles(c.motionVertices, c.motionIndices, atlas.texture, &c.motionTriangles)
	return nil
}

func (c *GPUCompositor) drawMotionBatch(nodeID runtime.LayerID, atlas *gpuMotionAtlas, target *ebiten.Image, draws []runtime.RenderMotionDraw, filter ebiten.Filter, originX, originY float64) error {
	return c.drawMotionBatchWithOpacity(nodeID, atlas, target, draws, filter, originX, originY, 1)
}

func (c *GPUCompositor) drawMotionBatchWithOpacity(nodeID runtime.LayerID, atlas *gpuMotionAtlas, target *ebiten.Image, draws []runtime.RenderMotionDraw, filter ebiten.Filter, originX, originY float64, opacity float32) error {
	c.motionVertices = c.motionVertices[:0]
	c.motionIndices = c.motionIndices[:0]
	for _, draw := range draws {
		if err := c.appendMotionBatchDraw(nodeID, atlas, draw, originX, originY, opacity); err != nil {
			return err
		}
	}
	if len(c.motionVertices) == 0 {
		return nil
	}
	c.motionTriangles.Blend = gpuMotionBlend(draws[0].Blend)
	c.motionTriangles.Filter = filter
	c.motionTriangles.DisableMipmaps = true
	c.motionTriangles.ColorScaleMode = ebiten.ColorScaleModePremultipliedAlpha
	target.DrawTriangles(c.motionVertices, c.motionIndices, atlas.texture, &c.motionTriangles)
	return nil
}

func (c *GPUCompositor) appendMotionBatchDraw(nodeID runtime.LayerID, atlas *gpuMotionAtlas, draw runtime.RenderMotionDraw, originX, originY float64, opacity float32) error {
	if atlas == nil || atlas.texture == nil {
		return fmt.Errorf("motion source atlas missing for layer %d", nodeID)
	}
	entry := atlas.entries[motionSourceKey(draw)]
	if entry == nil || entry.view == nil {
		return fmt.Errorf("motion source %q missing from atlas for layer %d", motionSourceKey(draw), nodeID)
	}
	base := uint16(len(c.motionVertices))
	alpha := float32(draw.Opacity) / 255 * opacity
	if len(draw.Vertices) != 0 {
		for _, vertex := range draw.Vertices {
			c.motionVertices = append(c.motionVertices, ebiten.Vertex{
				DstX: vertex.X - float32(originX), DstY: vertex.Y - float32(originY),
				SrcX: float32(entry.rect.Min.X) + vertex.U, SrcY: float32(entry.rect.Min.Y) + vertex.V,
				ColorR: alpha, ColorG: alpha, ColorB: alpha, ColorA: alpha,
			})
		}
		for _, index := range draw.Indices {
			c.motionIndices = append(c.motionIndices, base+index)
		}
		return nil
	}
	width, height := float32(draw.Width), float32(draw.Height)
	points := [4][2]float32{{0, 0}, {width, 0}, {0, height}, {width, height}}
	for _, point := range points {
		c.motionVertices = append(c.motionVertices, ebiten.Vertex{
			DstX: float32(draw.A*float64(point[0]) + draw.B*float64(point[1]) + draw.TX - originX),
			DstY: float32(draw.C*float64(point[0]) + draw.D*float64(point[1]) + draw.TY - originY),
			SrcX: float32(entry.rect.Min.X) + point[0], SrcY: float32(entry.rect.Min.Y) + point[1],
			ColorR: alpha, ColorG: alpha, ColorB: alpha, ColorA: alpha,
		})
	}
	c.motionIndices = append(c.motionIndices, base, base+1, base+2, base+2, base+1, base+3)
	return nil
}

func (c *GPUCompositor) drawMotionItem(nodeID runtime.LayerID, atlas *gpuMotionAtlas, target *ebiten.Image, item runtime.RenderMotionItem, filter ebiten.Filter, originX, originY float64) error {
	if item.Group != nil {
		return c.drawMotionGroup(nodeID, atlas, target, item.Group, filter, originX, originY)
	}
	return c.drawMotionDraw(nodeID, atlas, target, item.Draw, filter, originX, originY)
}

func (c *GPUCompositor) drawMotionDraw(nodeID runtime.LayerID, atlas *gpuMotionAtlas, target *ebiten.Image, draw runtime.RenderMotionDraw, filter ebiten.Filter, originX, originY float64) error {
	var source *ebiten.Image
	var err error
	if draw.Baked {
		source, err = c.motionStreamSource(nodeID, draw)
		if err != nil {
			return err
		}
	} else {
		if atlas == nil {
			return fmt.Errorf("motion source atlas missing for layer %d", nodeID)
		}
		entry := atlas.entries[motionSourceKey(draw)]
		if entry == nil || entry.view == nil {
			return fmt.Errorf("motion source %q missing from atlas for layer %d", motionSourceKey(draw), nodeID)
		}
		source = entry.view
	}
	if len(draw.Vertices) != 0 {
		c.motionVertices = c.motionVertices[:0]
		if cap(c.motionVertices) < len(draw.Vertices) {
			c.motionVertices = make([]ebiten.Vertex, 0, len(draw.Vertices))
		}
		bounds := source.Bounds()
		alpha := float32(draw.Opacity) / 255
		for _, vertex := range draw.Vertices {
			c.motionVertices = append(c.motionVertices, ebiten.Vertex{
				DstX: vertex.X - float32(originX), DstY: vertex.Y - float32(originY),
				SrcX:   float32(bounds.Min.X) + vertex.U,
				SrcY:   float32(bounds.Min.Y) + vertex.V,
				ColorR: alpha, ColorG: alpha, ColorB: alpha, ColorA: alpha,
			})
		}
		c.motionIndices = c.motionIndices[:0]
		for _, index := range draw.Indices {
			c.motionIndices = append(c.motionIndices, index)
		}
		target.DrawTriangles(c.motionVertices, c.motionIndices, source, &ebiten.DrawTrianglesOptions{
			Blend: gpuMotionBlend(draw.Blend), Filter: filter, DisableMipmaps: true,
			ColorScaleMode: ebiten.ColorScaleModePremultipliedAlpha,
		})
		return nil
	}
	var geo ebiten.GeoM
	geo.SetElement(0, 0, draw.A)
	geo.SetElement(0, 1, draw.B)
	geo.SetElement(0, 2, draw.TX-originX)
	geo.SetElement(1, 0, draw.C)
	geo.SetElement(1, 1, draw.D)
	geo.SetElement(1, 2, draw.TY-originY)
	options := &ebiten.DrawImageOptions{
		GeoM:           geo,
		Blend:          gpuMotionBlend(draw.Blend),
		Filter:         filter,
		DisableMipmaps: true,
	}
	options.ColorScale.ScaleAlpha(float32(draw.Opacity) / 255)
	target.DrawImage(source, options)
	return nil
}

func (c *GPUCompositor) drawMotionGroup(nodeID runtime.LayerID, atlas *gpuMotionAtlas, target *ebiten.Image, group *runtime.RenderMotionGroup, filter ebiten.Filter, originX, originY float64) error {
	width, height := target.Bounds().Dx(), target.Bounds().Dy()
	parentBounds := motionPixelBounds{minX: originX, minY: originY, maxX: originX + float64(width), maxY: originY + float64(height), valid: true}
	groupBounds := motionGroupBounds(group).intersect(parentBounds)
	if !groupBounds.valid {
		return nil
	}
	groupRect := motionBoundsRect(groupBounds, int(originX), int(originY), width, height)
	if groupRect.Empty() {
		return nil
	}
	groupTarget := c.acquireTexture(int32(groupRect.Dx()), int32(groupRect.Dy()))
	groupTarget.Clear()
	if err := c.drawMotionItems(nodeID, atlas, groupTarget, group.Items, filter, float64(groupRect.Min.X), float64(groupRect.Min.Y)); err != nil {
		c.releaseTexture(groupTarget, int32(groupRect.Dx()), int32(groupRect.Dy()))
		return err
	}
	for _, mask := range group.Masks {
		maskTarget := c.acquireTexture(int32(groupRect.Dx()), int32(groupRect.Dy()))
		maskTarget.Clear()
		if err := c.drawMotionItems(nodeID, atlas, maskTarget, mask.Items, filter, float64(groupRect.Min.X), float64(groupRect.Min.Y)); err != nil {
			c.releaseTexture(maskTarget, int32(groupRect.Dx()), int32(groupRect.Dy()))
			c.releaseTexture(groupTarget, int32(groupRect.Dx()), int32(groupRect.Dy()))
			return err
		}
		next, err := c.applyMotionMask(groupTarget, maskTarget, int32(mask.Operation), group.MaskMode)
		c.releaseTexture(maskTarget, int32(groupRect.Dx()), int32(groupRect.Dy()))
		if err != nil {
			c.releaseTexture(groupTarget, int32(groupRect.Dx()), int32(groupRect.Dy()))
			return err
		}
		c.releaseTexture(groupTarget, int32(groupRect.Dx()), int32(groupRect.Dy()))
		groupTarget = next
	}
	var geo ebiten.GeoM
	geo.Translate(float64(groupRect.Min.X)-originX, float64(groupRect.Min.Y)-originY)
	target.DrawImage(groupTarget, &ebiten.DrawImageOptions{GeoM: geo, Blend: gpuMotionBlend(runtime.MotionBlendAlpha)})
	c.releaseTexture(groupTarget, int32(groupRect.Dx()), int32(groupRect.Dy()))
	return nil
}

type motionPixelBounds struct {
	minX, minY float64
	maxX, maxY float64
	valid      bool
}

func (b motionPixelBounds) union(other motionPixelBounds) motionPixelBounds {
	if !other.valid {
		return b
	}
	if !b.valid {
		return other
	}
	return motionPixelBounds{
		minX:  math.Min(b.minX, other.minX),
		minY:  math.Min(b.minY, other.minY),
		maxX:  math.Max(b.maxX, other.maxX),
		maxY:  math.Max(b.maxY, other.maxY),
		valid: true,
	}
}

func (b motionPixelBounds) intersect(other motionPixelBounds) motionPixelBounds {
	if !b.valid || !other.valid {
		return motionPixelBounds{}
	}
	b.minX = math.Max(b.minX, other.minX)
	b.minY = math.Max(b.minY, other.minY)
	b.maxX = math.Min(b.maxX, other.maxX)
	b.maxY = math.Min(b.maxY, other.maxY)
	if b.maxX <= b.minX || b.maxY <= b.minY {
		return motionPixelBounds{}
	}
	return b
}

func motionGroupBounds(group *runtime.RenderMotionGroup) motionPixelBounds {
	if group == nil {
		return motionPixelBounds{}
	}
	var bounds motionPixelBounds
	for _, item := range group.Items {
		bounds = bounds.union(motionItemBounds(item))
	}
	for _, mask := range group.Masks {
		if mask.Operation != 5 && mask.Operation != 6 {
			continue
		}
		for _, item := range mask.Items {
			bounds = bounds.union(motionItemBounds(item))
		}
	}
	return bounds
}

func motionItemBounds(item runtime.RenderMotionItem) motionPixelBounds {
	if item.Group != nil {
		return motionGroupBounds(item.Group)
	}
	return motionDrawBounds(item.Draw)
}

func motionDrawBounds(draw runtime.RenderMotionDraw) motionPixelBounds {
	var bounds motionPixelBounds
	if len(draw.Vertices) != 0 {
		for _, vertex := range draw.Vertices {
			bounds = bounds.union(motionPixelBounds{minX: float64(vertex.X), minY: float64(vertex.Y), maxX: float64(vertex.X), maxY: float64(vertex.Y), valid: true})
		}
	} else if draw.Width > 0 && draw.Height > 0 {
		for _, point := range [][2]float64{{0, 0}, {float64(draw.Width), 0}, {0, float64(draw.Height)}, {float64(draw.Width), float64(draw.Height)}} {
			x := draw.A*point[0] + draw.B*point[1] + draw.TX
			y := draw.C*point[0] + draw.D*point[1] + draw.TY
			bounds = bounds.union(motionPixelBounds{minX: x, minY: y, maxX: x, maxY: y, valid: true})
		}
	}
	if !bounds.valid {
		return bounds
	}
	return bounds
}

func motionBoundsRect(bounds motionPixelBounds, originX, originY, width, height int) image.Rectangle {
	if !bounds.valid {
		return image.Rect(originX, originY, originX+width, originY+height)
	}
	left := maxInt(originX, int(math.Floor(bounds.minX)))
	top := maxInt(originY, int(math.Floor(bounds.minY)))
	right := minInt(originX+width, int(math.Ceil(bounds.maxX)))
	bottom := minInt(originY+height, int(math.Ceil(bounds.maxY)))
	return image.Rect(left, top, right, bottom)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (c *GPUCompositor) applyMotionMask(group, mask *ebiten.Image, operation int32, maskMode int64) (*ebiten.Image, error) {
	width, height := group.Bounds().Dx(), group.Bounds().Dy()
	result := c.acquireTexture(int32(width), int32(height))
	c.vertices = c.vertices[:0]
	c.vertices = append(c.vertices,
		ebiten.Vertex{DstX: 0, DstY: 0, SrcX: 0, SrcY: 0},
		ebiten.Vertex{DstX: float32(width), DstY: 0, SrcX: float32(width), SrcY: 0},
		ebiten.Vertex{DstX: 0, DstY: float32(height), SrcX: 0, SrcY: float32(height)},
		ebiten.Vertex{DstX: float32(width), DstY: float32(height), SrcX: float32(width), SrcY: float32(height)},
	)
	uniforms := c.shaderUniforms()
	uniforms["Operation"] = float32(operation)
	uniforms["Threshold"] = boolToFloat(maskMode == 0)
	result.DrawTrianglesShader(c.vertices, c.indices, c.motionMask, &ebiten.DrawTrianglesShaderOptions{
		Blend:    gpuReplaceBlend,
		Uniforms: uniforms,
		Images:   [4]*ebiten.Image{group, mask, nil, nil},
	})
	return result, nil
}

// evictMotionAtlasEntries removes sources the current frame does not draw and
// repacks the atlas, so switching motions on one layer cannot accumulate
// canvases or retired source images.
func (c *GPUCompositor) evictMotionAtlasEntries(nodeID runtime.LayerID, draws []runtime.RenderMotionDraw) {
	atlas := c.motionAtlases[nodeID]
	if atlas == nil || len(atlas.entries) == 0 {
		return
	}
	atlas.frame++
	frame := atlas.frame
	for _, draw := range draws {
		if draw.Baked {
			continue
		}
		if entry := atlas.entries[motionSourceKey(draw)]; entry != nil {
			entry.lastUsed = frame
		}
	}
	removed := false
	for key, entry := range atlas.entries {
		if entry == nil || entry.lastUsed != frame {
			if entry != nil && atlas.texture != nil && !entry.rect.Empty() {
				atlas.freeSlots = append(atlas.freeSlots, motionAtlasStorageRect(entry))
			}
			delete(atlas.entries, key)
			removed = true
		}
	}
	if removed && len(atlas.entries) != 0 {
		c.rebuildMotionAtlas(atlas)
	}
}

func (c *GPUCompositor) evictSharedMotionAtlases() {
	for key, atlas := range c.sharedMotionAtlases {
		if atlas == nil {
			delete(c.sharedMotionAtlases, key)
			continue
		}
		if len(atlas.entries) == 0 {
			if c.motionFrame > atlas.lastSharedUse+1 {
				if atlas.texture != nil {
					atlas.texture.Deallocate()
				}
				delete(c.sharedMotionAtlases, key)
			}
			continue
		}
		removed := false
		for sourceKey, entry := range atlas.entries {
			if entry == nil || entry.sharedLastUsed != c.motionFrame {
				if entry != nil && atlas.texture != nil && !entry.rect.Empty() {
					atlas.freeSlots = append(atlas.freeSlots, motionAtlasStorageRect(entry))
				}
				delete(atlas.entries, sourceKey)
				removed = true
			}
		}
		if removed && len(atlas.entries) != 0 {
			c.rebuildMotionAtlas(atlas)
		}
		if len(atlas.entries) == 0 && c.motionFrame > atlas.lastSharedUse+1 {
			if atlas.texture != nil {
				atlas.texture.Deallocate()
			}
			delete(c.sharedMotionAtlases, key)
		}
	}
}

// motionStreamSource uploads one baked frame into a per-layer texture that is
// replaced on every tick.
func (c *GPUCompositor) motionStreamSource(nodeID runtime.LayerID, draw runtime.RenderMotionDraw) (*ebiten.Image, error) {
	if draw.Width <= 0 || draw.Height <= 0 ||
		uint64(draw.Width)*uint64(draw.Height)*4 != uint64(len(draw.RGBA)) {
		return nil, fmt.Errorf("invalid streamed motion source for layer %d", nodeID)
	}
	if c.motionStreams == nil {
		c.motionStreams = make(map[runtime.LayerID]*gpuMotionStream)
	}
	stream := c.motionStreams[nodeID]
	if stream == nil || stream.width != draw.Width || stream.height != draw.Height {
		if stream != nil && stream.image != nil {
			stream.image.Deallocate()
		}
		stream = &gpuMotionStream{
			image:  newGPUImage(int(draw.Width), int(draw.Height)),
			width:  draw.Width,
			height: draw.Height,
		}
		c.motionStreams[nodeID] = stream
	}
	upload := c.motionUpload(draw.RGBA, draw.Opaque, draw.Premultiplied)
	stream.image.WritePixels(upload)
	return stream.image, nil
}

// gpuMotionBlend maps a motion blend operation onto Ebitengine blend factors.
// The sources are premultiplied, so the reference's mix(dst, blend, srcAlpha)
// forms reduce to these factor sets (RenderManager_ogl.cpp:3695-3775).
func gpuMotionBlend(mode runtime.MotionBlendMode) ebiten.Blend {
	switch mode {
	case runtime.MotionBlendAdditive:
		// mix(dst, clamp(src+dst), alpha) == dst + premultiplied src.
		return ebiten.Blend{
			BlendFactorSourceRGB:        ebiten.BlendFactorOne,
			BlendFactorDestinationRGB:   ebiten.BlendFactorOne,
			BlendFactorSourceAlpha:      ebiten.BlendFactorOne,
			BlendFactorDestinationAlpha: ebiten.BlendFactorOneMinusSourceAlpha,
			BlendOperationRGB:           ebiten.BlendOperationAdd,
			BlendOperationAlpha:         ebiten.BlendOperationAdd,
		}
	case runtime.MotionBlendMultiplicative:
		// dst*(1-alpha) + dst*src == dst*(1 - alpha + alpha*src).
		return ebiten.Blend{
			BlendFactorSourceRGB:        ebiten.BlendFactorDestinationColor,
			BlendFactorDestinationRGB:   ebiten.BlendFactorOneMinusSourceAlpha,
			BlendFactorSourceAlpha:      ebiten.BlendFactorZero,
			BlendFactorDestinationAlpha: ebiten.BlendFactorOne,
			BlendOperationRGB:           ebiten.BlendOperationAdd,
			BlendOperationAlpha:         ebiten.BlendOperationAdd,
		}
	case runtime.MotionBlendScreen:
		// dst + premultiplied src*(1-dst).
		return ebiten.Blend{
			BlendFactorSourceRGB:        ebiten.BlendFactorOneMinusDestinationColor,
			BlendFactorDestinationRGB:   ebiten.BlendFactorOne,
			BlendFactorSourceAlpha:      ebiten.BlendFactorZero,
			BlendFactorDestinationAlpha: ebiten.BlendFactorOne,
			BlendOperationRGB:           ebiten.BlendOperationAdd,
			BlendOperationAlpha:         ebiten.BlendOperationAdd,
		}
	case runtime.MotionBlendSubtractive:
		// PsSubBlend is mix(dst, clamp(dst + src - 1), srcAlpha) in straight
		// alpha (RenderManager_ogl.cpp:3725-3731), i.e. dst - srcAlpha*(1-src)
		// before the clamp. The motion draw layer uploads premultiplied
		// complements (alpha - color) for this mode, so a reversed subtraction
		// against the destination reproduces the unclamped part exactly.
		return ebiten.Blend{
			BlendFactorSourceRGB:        ebiten.BlendFactorOne,
			BlendFactorDestinationRGB:   ebiten.BlendFactorOne,
			BlendFactorSourceAlpha:      ebiten.BlendFactorZero,
			BlendFactorDestinationAlpha: ebiten.BlendFactorOne,
			BlendOperationRGB:           ebiten.BlendOperationReverseSubtract,
			BlendOperationAlpha:         ebiten.BlendOperationAdd,
		}
	}
	return ebiten.BlendSourceOver
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
	if c.motionBatching && !motionDirectCandidate(n, targetType) {
		c.flushMotionBatch()
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
			directMotion := false
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
					if n.Type == runtime.LayerAlpha && targetType == runtime.LayerBinder {
						directMotion, err = c.drawDirectMotionAlpha(n, x0, y0, x1, y1)
					}
					if !directMotion && c.motionBatching {
						c.flushMotionBatch()
					}
					if !directMotion && err == nil {
						source, err = c.composeMotion(n)
					}
				} else {
					if n.GPUStretch {
						sourceWidth, sourceHeight = n.GPUStretchSourceWidth, n.GPUStretchSourceHeight
					}
					source, err = c.texture(gpuTextureKey{id: n.ID, role: gpuTextureLayer}, sourceWidth, sourceHeight, n.RGBA, n.ImageHash, n.Type == runtime.LayerOpaque, n.ImageDirty, n.ImageDirtyValid && !n.GPUStretch && n.Motion == nil)
				}
				if err != nil {
					return err
				}
				if !directMotion {
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
	}
	if n.ChildCount > 0 && n.Type != runtime.LayerBinder && c.motionBatching {
		c.flushMotionBatch()
	}
	childTarget := n.Type
	if n.Type == runtime.LayerBinder {
		childTarget = targetType
	}
	previousBatching := c.motionBatching
	batchChildren := n.Type == runtime.LayerBinder
	if batchChildren {
		c.motionBatching = true
	}
	for i := range n.ChildCount {
		pos := n.ChildStart + i
		if pos < 0 || pos >= len(plan.ChildIndices) {
			if batchChildren {
				c.flushMotionBatch()
				c.motionBatching = previousBatching
			}
			return fmt.Errorf("invalid child range for layer %d", n.ID)
		}
		if err := c.drawNode(plan, plan.ChildIndices[pos], childTarget); err != nil {
			if batchChildren {
				c.flushMotionBatch()
				c.motionBatching = previousBatching
			}
			return err
		}
	}
	if batchChildren {
		c.flushMotionBatch()
		c.motionBatching = previousBatching
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
		c.textures[key] = t
	}
	if t.hash != tr.RuleHash {
		upload := c.scratch(len(tr.Rule) * 4)
		for i, value := range tr.Rule {
			upload[i*4], upload[i*4+1], upload[i*4+2], upload[i*4+3] = value, 0, 0, 255
		}
		t.image.WritePixels(upload)
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
	c.motionFrame++
	c.beginTextureFrame()
	if len(plan.Nodes) != 0 {
		root := 0
		if err := c.drawNode(plan, root, plan.Nodes[root].Type); err != nil {
			return nil, err
		}
	}
	c.evictUnusedTextures()
	c.evictSharedMotionAtlases()
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
