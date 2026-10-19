package runtime

import (
	"errors"
	"fmt"

	"github.com/eslzzyl/krkrgo/internal/assets"
	"github.com/eslzzyl/krkrgo/internal/tjs"
)

// LayerID is the stable identity of a layer. IDs are supplied by the caller;
// the runtime never renumbers layers when their order changes.
type LayerID uint64

// Rect is an integer rectangle. X and Y are relative to the containing layer
// when used as layer bounds or a clip rectangle.
type Rect struct {
	X, Y          int32
	Width, Height int32
}

// LayerRect is a descriptive alias used by host integrations.
type LayerRect = Rect

func (r Rect) Empty() bool   { return r.Width <= 0 || r.Height <= 0 }
func (r Rect) Right() int32  { return r.X + r.Width }
func (r Rect) Bottom() int32 { return r.Y + r.Height }
func (r Rect) Contains(x, y int32) bool {
	return x >= r.X && y >= r.Y && x < r.Right() && y < r.Bottom()
}
func (r Rect) Intersect(b Rect) Rect {
	left, top := r.X, r.Y
	if b.X > left {
		left = b.X
	}
	if b.Y > top {
		top = b.Y
	}
	right, bottom := r.Right(), r.Bottom()
	if b.Right() < right {
		right = b.Right()
	}
	if b.Bottom() < bottom {
		bottom = b.Bottom()
	}
	if right <= left || bottom <= top {
		return Rect{}
	}
	return Rect{X: left, Y: top, Width: right - left, Height: bottom - top}
}

var (
	ErrInvalidLayer         = errors.New("invalid layer")
	ErrLayerTree            = errors.New("invalid layer tree")
	ErrUnsupportedLayerType = errors.New("unsupported layer type")
	ErrImageSizeMismatch    = errors.New("layer image size mismatch")
	ErrProvinceSizeMismatch = errors.New("layer province size mismatch")
)

// LayerType values intentionally match tTVPLayerType in drawable.h.
type LayerType uint8

const (
	LayerBinder LayerType = iota
	LayerOpaque
	LayerAlpha
	LayerAdditive
	LayerSubtractive
	LayerMultiplicative
	LayerEffect
	LayerFilter
	LayerDodge
	LayerDarken
	LayerLighten
	LayerScreen
	LayerAddAlpha
	LayerPSNormal
	LayerPSAdditive
	LayerPSSubtractive
	LayerPSMultiplicative
	LayerPSScreen
	LayerPSOverlay
	LayerPSHardLight
	LayerPSSoftLight
	LayerPSColorDodge
	LayerPSColorDodge5
	LayerPSColorBurn
	LayerPSLighten
	LayerPSDarken
	LayerPSDifference
	LayerPSDifference5
	LayerPSExclusion
)

// LayerType aliases keep call sites close to the C++ names while retaining a
// single underlying discriminator for serialization.
type BlendMode = LayerType

const (
	LayerTypeBinder           = LayerBinder
	LayerTypeOpaque           = LayerOpaque
	LayerTypeAlpha            = LayerAlpha
	LayerTypeAdditive         = LayerAdditive
	LayerTypeSubtractive      = LayerSubtractive
	LayerTypeMultiplicative   = LayerMultiplicative
	LayerTypeEffect           = LayerEffect
	LayerTypeFilter           = LayerFilter
	LayerTypeDodge            = LayerDodge
	LayerTypeDarken           = LayerDarken
	LayerTypeLighten          = LayerLighten
	LayerTypeScreen           = LayerScreen
	LayerTypeAddAlpha         = LayerAddAlpha
	LayerTypePSNormal         = LayerPSNormal
	LayerTypePSAdditive       = LayerPSAdditive
	LayerTypePSSubtractive    = LayerPSSubtractive
	LayerTypePSMultiplicative = LayerPSMultiplicative
	LayerTypePSScreen         = LayerPSScreen
	LayerTypePSOverlay        = LayerPSOverlay
	LayerTypePSHardLight      = LayerPSHardLight
	LayerTypePSSoftLight      = LayerPSSoftLight
	LayerTypePSColorDodge     = LayerPSColorDodge
	LayerTypePSColorDodge5    = LayerPSColorDodge5
	LayerTypePSColorBurn      = LayerPSColorBurn
	LayerTypePSLighten        = LayerPSLighten
	LayerTypePSDarken         = LayerPSDarken
	LayerTypePSDifference     = LayerPSDifference
	LayerTypePSDifference5    = LayerPSDifference5
	LayerTypePSExclusion      = LayerPSExclusion
)

// Common aliases use the terminology found in Kirikiri scripts and C++.
const (
	LayerTransparent    = LayerAlpha
	LayerCoverRect      = LayerOpaque
	BlendOpaque         = LayerOpaque
	BlendAlpha          = LayerAlpha
	BlendAdditive       = LayerAdditive
	BlendSubtractive    = LayerSubtractive
	BlendMultiplicative = LayerMultiplicative
	BlendScreen         = LayerScreen
	BlendAddAlpha       = LayerAddAlpha
)

const (
	layerFaceAlpha    int32 = 0
	layerFaceOpaque   int32 = 1
	layerFaceMask     int32 = 2
	layerFaceProvince int32 = 3
	layerFaceAddAlpha int32 = 4
	layerFaceAuto     int32 = 128
)

// HitType selects the image used for input hit testing.
type HitType uint8

const (
	HitMask HitType = iota
	HitProvince
)

const (
	HitTypeMask     = HitMask
	HitTypeProvince = HitProvince
)

// Layer owns its pixel buffers and children. RGBA is tightly packed,
// non-premultiplied RGBA, as assets.Image. Province is one byte per pixel.
type Layer struct {
	ID               LayerID
	Name             string
	Bounds           Rect
	Scale            int32
	Opacity          uint8
	Visible          bool
	Enabled          bool
	EventTransparent bool
	Focusable        bool
	Type             LayerType
	Hit              HitType
	HitThreshold     int

	RGBA                          []byte
	Province                      []byte
	ImageWidth, ImageHeight       int32
	ProvinceWidth, ProvinceHeight int32
	ImageLeft, ImageTop           int32
	Face                          int32
	HoldAlpha                     bool
	CursorX, CursorY              int32
	Hint                          string
	ShowParentHint                bool

	Parent   *Layer
	children []*Layer

	inTransition          bool
	transitionSource      *Layer
	transitionMethod      string
	transitionType1       int32
	transitionType2       int32
	transitionAccel       int32
	transitionChildren    bool
	transitionCallback    tjs.Value
	transitionDuration    uint64
	transitionStart       uint64
	transitionTick        uint64
	transitionSelfUpdate  bool
	transitionVague       int32
	transitionRule        []byte
	transitionDestImage   []byte
	transitionSourceImage []byte
	transitionPixels      []byte
	transitionOpaque      bool
	transitionZeroOpaque  bool
	transitionDestHash    uint64
	transitionSourceHash  uint64
	transitionRuleHash    uint64
	transitionHashesValid bool
	transitionSourceAlpha transitionAlphaClass
	transitionDestAlpha   transitionAlphaClass
	transitionHidden      bool
	absoluteOrderIndex    int32
	absoluteOrderMode     bool
	clip                  Rect
	clipSet               bool
	hitTestWork           bool
	callOnPaint           bool
	gpuStretch            *gpuStretchCopy
	gpuMotion             *RenderMotion
	imageModified         bool
	imageRevision         uint64
	subtreeRevision       uint64
	windowID              uint64
}

func (l *Layer) markRenderDirty() {
	for current := l; current != nil; current = current.Parent {
		current.subtreeRevision++
	}
}

func (l *Layer) markImageDirty() {
	if l == nil {
		return
	}
	l.imageRevision++
	l.markRenderDirty()
}

func (l *Layer) renderImageRevision() uint64 {
	if l == nil || l.imageRevision == 0 {
		return 1
	}
	return l.imageRevision
}

type gpuStretchCopy struct {
	sourceLayer       *Layer
	sourceBitmap      *Bitmap
	sourcePixels      []byte
	sourceID          uint64
	sourceWidth       int32
	sourceHeight      int32
	sourceX           int32
	sourceY           int32
	cropWidth         int32
	cropHeight        int32
	destinationX      int32
	destinationY      int32
	destinationWidth  int32
	destinationHeight int32
	stretchType       int32
}

// NewLayer constructs a layer with an immutable-by-convention stable ID.
func NewLayer(id LayerID) *Layer {
	return &Layer{ID: id, Scale: 1, Opacity: 255, Visible: true, Enabled: true,
		Type: LayerAlpha, Hit: HitMask, HitThreshold: 1, Face: 128,
		imageRevision: 1, subtreeRevision: 1}
}

// RenderScale returns the layer's pixel density. Layers created outside
// Services use the logical resolution.
func (l *Layer) RenderScale() int32 {
	if l == nil || l.Scale < 1 {
		return 1
	}
	return l.Scale
}

func (l *Layer) deviceWidth() int32 {
	if l == nil {
		return 0
	}
	return l.ImageWidth * l.RenderScale()
}

func (l *Layer) deviceHeight() int32 {
	if l == nil {
		return 0
	}
	return l.ImageHeight * l.RenderScale()
}

func (l *Layer) devicePixelCount() int64 {
	if l == nil {
		return 0
	}
	return int64(l.deviceWidth()) * int64(l.deviceHeight())
}

func (l *Layer) validImage() bool {
	if l == nil {
		return false
	}
	w, h := l.deviceWidth(), l.deviceHeight()
	return validLayerImageSize(w, h, len(l.RGBA), 4)
}

// resampleRGBA resizes a packed RGBA image with nearest-neighbor sampling.
func resampleRGBA(source []byte, sourceWidth, sourceHeight, width, height int32) []byte {
	if sourceWidth == width && sourceHeight == height {
		return append([]byte(nil), source...)
	}
	out := make([]byte, int64(width)*int64(height)*4)
	for y := int32(0); y < height; y++ {
		sourceY := int64(y) * int64(sourceHeight) / int64(height)
		for x := int32(0); x < width; x++ {
			sourceX := int64(x) * int64(sourceWidth) / int64(width)
			si := (sourceY*int64(sourceWidth) + sourceX) * 4
			di := (int64(y)*int64(width) + int64(x)) * 4
			copy(out[di:di+4], source[si:si+4])
		}
	}
	return out
}

// downscaleRGBA converts a device-resolution RGBA image back to logical pixels
// by sampling the top-left device pixel of each scale block.
func downscaleRGBA(source []byte, deviceWidth, deviceHeight, scale int32) []byte {
	if scale <= 1 {
		return append([]byte(nil), source...)
	}
	width, height := deviceWidth/scale, deviceHeight/scale
	out := make([]byte, int(width)*int(height)*4)
	for y := int32(0); y < height; y++ {
		for x := int32(0); x < width; x++ {
			si := ((int64(y)*int64(scale))*int64(deviceWidth) + int64(x)*int64(scale)) * 4
			di := (int64(y)*int64(width) + int64(x)) * 4
			copy(out[di:di+4], source[si:si+4])
		}
	}
	return out
}

// scaleRGBA resamples a packed RGBA image by nearest-neighbor replication.
func scaleRGBA(source []byte, width, height, scale int32) []byte {
	if scale <= 1 {
		return append([]byte(nil), source...)
	}
	out := make([]byte, int(int64(width)*int64(height)*int64(scale)*int64(scale))*4)
	destinationWidth := int(width * scale)
	for y := int32(0); y < height; y++ {
		for sy := int32(0); sy < scale; sy++ {
			row := (int(y)*int(scale) + int(sy)) * destinationWidth
			for x := int32(0); x < width; x++ {
				si := (int(y)*int(width) + int(x)) * 4
				for sx := int32(0); sx < scale; sx++ {
					di := (row + int(x*scale) + int(sx)) * 4
					copy(out[di:di+4], source[si:si+4])
				}
			}
		}
	}
	return out
}

// SetScale changes the layer's pixel density and resamples its image buffers.
// Logical dimensions and coordinates are unchanged.
func (l *Layer) SetScale(scale int32) {
	if l == nil {
		return
	}
	if scale < 1 {
		scale = 1
	}
	if l.Scale == scale {
		return
	}
	oldScale := l.RenderScale()
	l.Scale = scale
	if len(l.RGBA) != 0 && l.ImageWidth > 0 && l.ImageHeight > 0 {
		sourceWidth := l.ImageWidth * oldScale
		sourceHeight := l.ImageHeight * oldScale
		l.RGBA = resampleRGBA(l.RGBA, sourceWidth, sourceHeight, l.deviceWidth(), l.deviceHeight())
		if l.inTransition {
			l.transitionPixels = resampleRGBA(l.transitionPixels, sourceWidth, sourceHeight, l.deviceWidth(), l.deviceHeight())
			l.transitionDestImage = resampleRGBA(l.transitionDestImage, sourceWidth, sourceHeight, l.deviceWidth(), l.deviceHeight())
			l.transitionSourceImage = resampleRGBA(l.transitionSourceImage, sourceWidth, sourceHeight, l.deviceWidth(), l.deviceHeight())
			l.transitionRule = resampleGray(l.transitionRule, sourceWidth, sourceHeight, l.deviceWidth(), l.deviceHeight())
			l.transitionHashesValid = false
		}
	}
	l.gpuStretch = nil
	l.gpuMotion = nil
	l.imageRevision++
	l.markRenderDirty()
}

func resampleGray(pixels []byte, sourceWidth, sourceHeight, width, height int32) []byte {
	if len(pixels) == 0 || sourceWidth <= 0 || sourceHeight <= 0 || width <= 0 || height <= 0 {
		return nil
	}
	if sourceWidth == width && sourceHeight == height {
		return pixels
	}
	if len(pixels) != int(sourceWidth)*int(sourceHeight) {
		return nil
	}
	out := make([]byte, int(width)*int(height))
	for y := range height {
		sourceY := int64(y) * int64(sourceHeight) / int64(height)
		for x := range width {
			sourceX := int64(x) * int64(sourceWidth) / int64(width)
			out[int(y)*int(width)+int(x)] = pixels[sourceY*int64(sourceWidth)+sourceX]
		}
	}
	return out
}

func NewRootLayer(id LayerID, width, height int32) *Layer {
	l := NewLayer(id)
	l.Bounds = Rect{Width: width, Height: height}
	return l
}

func (l *Layer) Children() []*Layer { return append([]*Layer(nil), l.children...) }
func (l *Layer) ChildCount() int    { return len(l.children) }
func (l *Layer) ChildAt(index int) *Layer {
	if index < 0 || index >= len(l.children) {
		return nil
	}
	return l.children[index]
}

// AddChild appends a child at the front-to-back drawing order. Reordering does
// not alter IDs and cannot create a cycle.
func (l *Layer) AddChild(child *Layer) error {
	if l == nil || child == nil || child == l {
		return ErrLayerTree
	}
	for p := l; p != nil; p = p.Parent {
		if p == child {
			return ErrLayerTree
		}
	}
	if child.Parent != nil {
		return ErrLayerTree
	}
	for _, c := range l.children {
		if c.ID == child.ID {
			return fmt.Errorf("%w: duplicate layer id %d", ErrLayerTree, child.ID)
		}
	}
	if l.absoluteOrderMode {
		if n := len(l.children); n > 0 {
			child.absoluteOrderIndex = l.children[n-1].absoluteOrderIndex + 1
		}
	}
	l.children = append(l.children, child)
	child.Parent = l
	l.markRenderDirty()
	return nil
}

func (l *Layer) RemoveChild(child *Layer) error {
	if l == nil || child == nil || child.Parent != l {
		return ErrLayerTree
	}
	for i, c := range l.children {
		if c == child {
			copy(l.children[i:], l.children[i+1:])
			l.children[len(l.children)-1] = nil
			l.children = l.children[:len(l.children)-1]
			child.Parent = nil
			l.markRenderDirty()
			return nil
		}
	}
	return ErrLayerTree
}

// MoveChild changes sibling order; zero is backmost and count is frontmost.
func (l *Layer) MoveChild(child *Layer, index int) error {
	if l == nil || child == nil || child.Parent != l || index < 0 || index >= len(l.children) {
		return ErrLayerTree
	}
	from := -1
	for i, c := range l.children {
		if c == child {
			from = i
			break
		}
	}
	if from < 0 {
		return ErrLayerTree
	}
	if from == index {
		return nil
	}
	if from < index {
		copy(l.children[from:], l.children[from+1:index+1])
	} else {
		copy(l.children[index+1:], l.children[index:from])
	}
	l.children[index] = child
	l.markRenderDirty()
	return nil
}
func (l *Layer) orderIndex() int32 {
	if l == nil || l.Parent == nil {
		return 0
	}
	for i, child := range l.Parent.children {
		if child == l {
			return int32(i)
		}
	}
	return -1
}

func (l *Layer) absoluteOrderValue() int32 {
	if l == nil || l.Parent == nil {
		return 0
	}
	if l.Parent.absoluteOrderMode {
		return l.absoluteOrderIndex
	}
	return l.orderIndex()
}

// SetOrderIndex moves this layer to an index in its parent's relative order.
// As in the native runtime, assigning order disables absolute ordering on the
// parent and clamps the requested index to the available sibling range.
func (l *Layer) SetOrderIndex(index int32) error {
	if l == nil || l.Parent == nil {
		return ErrLayerTree
	}
	parent := l.Parent
	if l.orderIndex() < 0 {
		return ErrLayerTree
	}
	if len(parent.children) == 0 {
		return ErrLayerTree
	}
	if index < 0 {
		index = 0
	}
	if int64(index) >= int64(len(parent.children)) {
		index = int32(len(parent.children) - 1)
	}
	parent.absoluteOrderMode = false
	parent.markRenderDirty()
	return parent.MoveChild(l, int(index))
}

// SetAbsoluteOrderIndex positions this layer among siblings by absolute index.
// Equal absolute indices are resolved deterministically by inserting before
// them when moving backward and after them when moving forward.
func (l *Layer) SetAbsoluteOrderIndex(index int32) error {
	if l == nil || l.Parent == nil {
		return ErrLayerTree
	}
	parent := l.Parent
	from := l.orderIndex()
	if from < 0 {
		return ErrLayerTree
	}
	parent.SetAbsoluteOrderMode(true)
	to := len(parent.children)
	for i, child := range parent.children {
		if child.absoluteOrderIndex >= index {
			to = i
			break
		}
	}
	if int(from) < to {
		to--
	}
	if err := parent.MoveChild(l, to); err != nil {
		return err
	}
	l.absoluteOrderIndex = index
	l.markRenderDirty()
	return nil
}

// SetAbsoluteOrderMode switches the ordering mode used for this layer's
// children. Entering absolute mode snapshots their current relative indices.
func (l *Layer) SetAbsoluteOrderMode(enabled bool) {
	if l == nil || l.absoluteOrderMode == enabled {
		return
	}
	l.absoluteOrderMode = enabled
	if enabled {
		for i, child := range l.children {
			child.absoluteOrderIndex = int32(i)
		}
	}
	l.markRenderDirty()
}

func (l *Layer) SetBounds(r Rect) error {
	if r.Width < 0 || r.Height < 0 {
		return fmt.Errorf("%w: negative bounds", ErrInvalidLayer)
	}
	l.Bounds = r
	l.markRenderDirty()
	return nil
}
func (l *Layer) SetClip(r Rect) error {
	if r.Width < 0 || r.Height < 0 {
		return fmt.Errorf("%w: negative clip", ErrInvalidLayer)
	}
	l.clip, l.clipSet = r, true
	l.markRenderDirty()
	return nil
}
func (l *Layer) ResetClip() {
	l.clip, l.clipSet = Rect{}, false
	l.markRenderDirty()
}
func (l *Layer) Clip() (Rect, bool) { return l.clip, l.clipSet }

const (
	maxLayerPixels    = uint64(64 * 1024 * 1024)
	maxLayerDimension = int32(32768)
)

func validLayerImageSize(w, h int32, n, bytesPerPixel int) bool {
	return w > 0 && h > 0 && uint64(w)*uint64(h) <= maxLayerPixels && uint64(w)*uint64(h)*uint64(bytesPerPixel) == uint64(n)
}

func (l *Layer) SetRGBA(width, height int32, rgba []byte) error {
	return l.setRGBA(width, height, rgba, false)
}

// SetRGBAOwned adopts rgba when no render scaling is required. The caller must
// not mutate or reuse the buffer after the call.
func (l *Layer) SetRGBAOwned(width, height int32, rgba []byte) error {
	return l.setRGBA(width, height, rgba, true)
}

func (l *Layer) setRGBA(width, height int32, rgba []byte, owned bool) error {
	if !validLayerImageSize(width, height, len(rgba), 4) {
		return fmt.Errorf("%w: %dx%d", ErrImageSizeMismatch, width, height)
	}
	if len(l.Province) != 0 && (l.ProvinceWidth != width || l.ProvinceHeight != height) {
		return ErrProvinceSizeMismatch
	}
	l.gpuStretch = nil
	l.gpuMotion = nil
	if owned && l.RenderScale() <= 1 {
		l.RGBA = rgba
	} else {
		l.RGBA = scaleRGBA(rgba, width, height, l.RenderScale())
	}
	l.ImageWidth, l.ImageHeight = width, height
	if l.Bounds.Width == 0 {
		l.Bounds.Width = width
	}
	if l.Bounds.Height == 0 {
		l.Bounds.Height = height
	}
	l.markImageDirty()
	return nil
}

func (l *Layer) SetPosition(x, y int32) {
	l.Bounds.X, l.Bounds.Y = x, y
	l.markRenderDirty()
}
func (l *Layer) SetSize(width, height int32) error {
	if l == nil {
		return ErrInvalidLayer
	}
	if width < 0 || height < 0 {
		return fmt.Errorf("%w: negative bounds", ErrInvalidLayer)
	}
	if width > maxLayerDimension || height > maxLayerDimension {
		return fmt.Errorf("%w: layer dimension exceeds limit", ErrInvalidLayer)
	}
	imageWidth, imageHeight := l.ImageWidth, l.ImageHeight
	imageLeft := maxInt32(l.ImageLeft, width-maxInt32(imageWidth, width))
	imageTop := maxInt32(l.ImageTop, height-maxInt32(imageHeight, height))
	if len(l.RGBA) != 0 && (width > imageWidth || height > imageHeight) {
		if err := resizeLayerImage(l, maxInt32(width, imageWidth), maxInt32(height, imageHeight)); err != nil {
			return err
		}
	}
	l.Bounds.Width, l.Bounds.Height = width, height
	if len(l.RGBA) != 0 {
		l.ImageLeft, l.ImageTop = imageLeft, imageTop
	}
	l.markRenderDirty()
	return nil
}

// SetImageSize resizes the image while preserving the overlapping pixels and
// keeping the layer bounds covered, as Layer.SetImageSize does in Kirikiri.
func (l *Layer) SetImageSize(width, height int32) error {
	if l == nil {
		return fmt.Errorf("%w: layer has no image", ErrInvalidLayer)
	}
	if err := l.materializeGPUMotion(); err != nil {
		return err
	}
	if err := l.materializeGPUStretch(); err != nil {
		return err
	}
	if width < 0 || height < 0 || uint64(width)*uint64(height) > maxLayerPixels {
		return fmt.Errorf("%w: image dimensions exceed limit", ErrInvalidLayer)
	}
	if width == 0 || height == 0 {
		l.ClearImage()
		return nil
	}
	if len(l.RGBA) == 0 {
		l.ImageWidth, l.ImageHeight = width, height
		if l.Bounds.Width < width {
			l.Bounds.Width = width
		}
		if l.Bounds.Height < height {
			l.Bounds.Height = height
		}
		l.markImageDirty()
		return nil
	}
	if width == l.ImageWidth && height == l.ImageHeight {
		return nil
	}
	bounds := l.Bounds
	imageLeft, imageTop := l.ImageLeft, l.ImageTop
	if width < bounds.Width {
		imageLeft = 0
		bounds.Width = width
	}
	if int64(width)+int64(imageLeft) < int64(bounds.Width) {
		imageLeft = bounds.Width - width
	}
	if height < bounds.Height {
		imageTop = 0
		bounds.Height = height
	}
	if int64(height)+int64(imageTop) < int64(bounds.Height) {
		imageTop = bounds.Height - height
	}
	if err := resizeLayerImage(l, width, height); err != nil {
		return err
	}
	l.Bounds, l.ImageLeft, l.ImageTop = bounds, imageLeft, imageTop
	return nil
}

func (l *Layer) SetImageWidth(width int32) error {
	if l == nil || len(l.RGBA) == 0 {
		return fmt.Errorf("%w: layer has no image", ErrInvalidLayer)
	}
	return l.SetImageSize(width, l.ImageHeight)
}

func (l *Layer) SetImageHeight(height int32) error {
	if l == nil || len(l.RGBA) == 0 {
		return fmt.Errorf("%w: layer has no image", ErrInvalidLayer)
	}
	return l.SetImageSize(l.ImageWidth, height)
}

func (l *Layer) SetSizeToImageSize() error {
	if l == nil || len(l.RGBA) == 0 {
		return fmt.Errorf("%w: layer has no image", ErrInvalidLayer)
	}
	if l.ImageWidth <= 0 || l.ImageHeight <= 0 {
		return nil
	}
	return l.SetSize(l.ImageWidth, l.ImageHeight)
}

func maxInt32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}

func resizeLayerImage(l *Layer, width, height int32) error {
	if l == nil || width <= 0 || height <= 0 || uint64(width)*uint64(height) > maxLayerPixels {
		return fmt.Errorf("%w: image dimensions exceed limit", ErrInvalidLayer)
	}
	scale := l.RenderScale()
	deviceWidth, deviceHeight := width*scale, height*scale
	pixels := uint64(deviceWidth) * uint64(deviceHeight)
	oldRGBA, oldWidth, oldHeight := l.RGBA, l.deviceWidth(), l.deviceHeight()
	if len(oldRGBA) != 0 && !validLayerImageSize(oldWidth, oldHeight, len(oldRGBA), 4) {
		return fmt.Errorf("%w: invalid existing image dimensions", ErrInvalidLayer)
	}
	if len(l.Province) != 0 && (!validLayerImageSize(l.ProvinceWidth, l.ProvinceHeight, len(l.Province), 1) ||
		(len(oldRGBA) != 0 && (l.ProvinceWidth != l.ImageWidth || l.ProvinceHeight != l.ImageHeight))) {
		return ErrProvinceSizeMismatch
	}
	rgba := make([]byte, int(pixels*4))
	fillLayerNeutral(rgba, l.Type)
	if len(oldRGBA) != 0 {
		copyWidth, copyHeight := min(deviceWidth, oldWidth), min(deviceHeight, oldHeight)
		rowBytes := int(copyWidth) * 4
		for y := range int(copyHeight) {
			dst := y * int(deviceWidth) * 4
			src := y * int(oldWidth) * 4
			copy(rgba[dst:dst+rowBytes], oldRGBA[src:src+rowBytes])
		}
	}
	var province []byte
	if len(l.Province) != 0 {
		province = make([]byte, int(uint64(width)*uint64(height)))
		copyWidth, copyHeight := min(width, l.ProvinceWidth), min(height, l.ProvinceHeight)
		for y := range int(copyHeight) {
			dst := y * int(width)
			src := y * int(l.ProvinceWidth)
			copy(province[dst:dst+int(copyWidth)], l.Province[src:src+int(copyWidth)])
		}
	}
	l.RGBA, l.ImageWidth, l.ImageHeight = rgba, width, height
	if len(l.Province) != 0 {
		l.Province, l.ProvinceWidth, l.ProvinceHeight = province, width, height
	}
	l.markImageDirty()
	l.ResetClip()
	return l.SetClip(Rect{Width: width, Height: height})
}
func (l *Layer) SetVisible(visible bool) {
	l.Visible = visible
	l.markRenderDirty()
}
func (l *Layer) SetEnabled(enabled bool) { l.Enabled = enabled }
func (l *Layer) SetOpacity(opacity int) error {
	if opacity < 0 || opacity > 255 {
		return fmt.Errorf("%w: opacity %d", ErrInvalidLayer, opacity)
	}
	l.Opacity = uint8(opacity)
	l.markRenderDirty()
	return nil
}
func (l *Layer) SetHitType(hit HitType) { l.Hit = hit }
func (l *Layer) SetHitThreshold(threshold int) error {
	if threshold < 0 || threshold > 256 {
		return fmt.Errorf("%w: hit threshold %d", ErrInvalidLayer, threshold)
	}
	l.HitThreshold = threshold
	return nil
}
func (l *Layer) GetParent() *Layer {
	if l == nil {
		return nil
	}
	return l.Parent
}
func (l *Layer) GetChildren() []*Layer { return l.Children() }

func layerNeutralColor(t LayerType) [4]byte {
	switch t {
	case LayerAdditive, LayerDodge, LayerLighten, LayerScreen, LayerAddAlpha,
		LayerPSNormal, LayerPSAdditive, LayerPSScreen,
		LayerPSColorDodge, LayerPSColorDodge5, LayerPSLighten,
		LayerPSDifference, LayerPSDifference5, LayerPSExclusion:
		return [4]byte{0, 0, 0, 0}
	case LayerPSOverlay, LayerPSHardLight, LayerPSSoftLight:
		return [4]byte{128, 128, 128, 0}
	default:
		return [4]byte{255, 255, 255, 0}
	}
}

func fillLayerNeutral(rgba []byte, layerType LayerType) {
	neutral := layerNeutralColor(layerType)
	for i := 0; i+3 < len(rgba); i += 4 {
		copy(rgba[i:i+4], neutral[:])
	}
}

// SetImage accepts the canonical decoded asset representation.
func (l *Layer) SetImage(im assets.Image) error {
	return l.SetRGBA(int32(im.Width), int32(im.Height), im.RGBA)
}

// SetImageOwned adopts im.RGBA when no render scaling is required. The caller
// must not mutate or reuse the buffer after the call.
func (l *Layer) SetImageOwned(im assets.Image) error {
	return l.SetRGBAOwned(int32(im.Width), int32(im.Height), im.RGBA)
}
func (l *Layer) ClearImage() {
	l.gpuStretch = nil
	l.gpuMotion = nil
	l.RGBA = nil
	l.ImageWidth, l.ImageHeight = 0, 0
	l.markImageDirty()
	l.ClearProvince()
}

func (l *Layer) SetProvince(width, height int32, province []byte) error {
	return l.setProvince(width, height, province, false)
}

// SetProvinceOwned adopts province without copying. The caller must not mutate
// or reuse the buffer after the call.
func (l *Layer) SetProvinceOwned(width, height int32, province []byte) error {
	return l.setProvince(width, height, province, true)
}

func (l *Layer) setProvince(width, height int32, province []byte, owned bool) error {
	if province == nil {
		l.Province = nil
		l.ProvinceWidth, l.ProvinceHeight = 0, 0
		l.markImageDirty()
		return nil
	}
	if !validLayerImageSize(width, height, len(province), 1) {
		return fmt.Errorf("%w: %dx%d", ErrProvinceSizeMismatch, width, height)
	}
	if len(l.RGBA) != 0 && (l.ImageWidth != width || l.ImageHeight != height) {
		return ErrProvinceSizeMismatch
	}
	if owned {
		l.Province = province
	} else {
		l.Province = append(l.Province[:0], province...)
	}
	l.ProvinceWidth, l.ProvinceHeight = width, height
	l.markImageDirty()
	return nil
}
func (l *Layer) ClearProvince() {
	l.Province = nil
	l.ProvinceWidth, l.ProvinceHeight = 0, 0
	l.markImageDirty()
}

func (l *Layer) SetImagePosition(x, y int32) error {
	if l == nil || len(l.RGBA) == 0 {
		return fmt.Errorf("%w: layer has no image", ErrInvalidLayer)
	}
	if x > 0 || y > 0 || int64(l.ImageWidth)+int64(x) < int64(l.Bounds.Width) ||
		int64(l.ImageHeight)+int64(y) < int64(l.Bounds.Height) {
		return fmt.Errorf("%w: image position leaves layer bounds uncovered", ErrInvalidLayer)
	}
	l.ImageLeft, l.ImageTop = x, y
	l.markRenderDirty()
	return nil
}
func (l *Layer) IsSeen() bool { return l != nil && l.Visible && l.Opacity != 0 }
func (l *Layer) drawFace() int32 {
	if l == nil || l.Face == layerFaceAuto {
		if l == nil {
			return layerFaceAuto
		}
		switch l.Type {
		case LayerAlpha, LayerPSNormal, LayerPSAdditive, LayerPSSubtractive, LayerPSMultiplicative,
			LayerPSScreen, LayerPSOverlay, LayerPSHardLight, LayerPSSoftLight, LayerPSColorDodge,
			LayerPSColorDodge5, LayerPSColorBurn, LayerPSLighten, LayerPSDarken, LayerPSDifference,
			LayerPSDifference5, LayerPSExclusion:
			return layerFaceAlpha
		case LayerAddAlpha:
			return layerFaceAddAlpha
		default:
			return layerFaceOpaque
		}
	}
	return l.Face
}
func (l *Layer) AbsolutePosition() (int32, int32) {
	var x, y int32
	for p := l; p != nil; p = p.Parent {
		x += p.Bounds.X
		y += p.Bounds.Y
	}
	return x, y
}

// SetType accepts recognized C++ layer discriminators. Binder, effect and
// filter types cannot own image buffers; Compose reports effect/filter unsupported.
func (l *Layer) SetType(t LayerType) error {
	if l == nil || (!supportedLayerType(t) && t != LayerEffect && t != LayerFilter) {
		return fmt.Errorf("%w: type %d", ErrUnsupportedLayerType, t)
	}
	if l.Type == t {
		return nil
	}
	if t == LayerBinder || t == LayerEffect || t == LayerFilter {
		l.Type = t
		l.ClearImage()
		l.ResetClip()
		return nil
	}
	if len(l.RGBA) == 0 {
		if err := resizeLayerImage(l, l.Bounds.Width, l.Bounds.Height); err != nil {
			fmt.Printf("legacy layer SetType resize width=%d height=%d image=%dx%d type=%d error=%v\n", l.Bounds.Width, l.Bounds.Height, l.ImageWidth, l.ImageHeight, l.Type, err)
			return err
		}
		l.ImageLeft, l.ImageTop = 0, 0
	}
	l.Type = t
	l.markRenderDirty()
	return nil
}
