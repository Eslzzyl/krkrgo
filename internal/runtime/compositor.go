package runtime

import (
	"fmt"
)

// FrameSnapshot is an immutable-by-convention CPU frame for an Ebitengine
// host. RGBA is tightly packed non-premultiplied RGBA; Province is an optional
// one-byte-per-pixel hit-test map. Compose returns fresh buffers each frame.
type FrameSnapshot struct {
	Width, Height int32
	RGBA          []byte
	Province      []byte
	// Opaque asserts that every RGBA pixel has alpha 255.
	Opaque bool
	// RenderStateHash identifies a stable static capture when valid.
	RenderStateHash      uint64
	RenderStateHashValid bool
}

func (f FrameSnapshot) Empty() bool { return f.Width <= 0 || f.Height <= 0 || len(f.RGBA) == 0 }
func (f FrameSnapshot) Image() (width, height int32, rgba []byte) {
	return f.Width, f.Height, append([]byte(nil), f.RGBA...)
}
func (f FrameSnapshot) Clone() FrameSnapshot {
	return FrameSnapshot{
		Width:                f.Width,
		Height:               f.Height,
		RGBA:                 append([]byte(nil), f.RGBA...),
		Province:             append([]byte(nil), f.Province...),
		Opaque:               f.Opaque,
		RenderStateHash:      f.RenderStateHash,
		RenderStateHashValid: f.RenderStateHashValid,
	}
}

// Compositor owns a layer tree root and performs deterministic CPU composition.
type Compositor struct {
	Root          *Layer
	Width, Height int32
	Scale         int32
	Background    [4]uint8
}

func NewCompositor(width, height int32) *Compositor {
	return &Compositor{Width: width, Height: height, Scale: 1}
}
func NewCompositorWithScale(width, height, scale int32) *Compositor {
	if scale < 1 {
		scale = 1
	}
	return &Compositor{Width: width * scale, Height: height * scale, Scale: scale}
}
func NewCompositorWithRoot(root *Layer, width, height int32) *Compositor {
	return &Compositor{Root: root, Width: width, Height: height, Scale: 1}
}
func (c *Compositor) SetRoot(root *Layer)            { c.Root = root }
func (c *Compositor) SetBackground(r, g, b, a uint8) { c.Background = [4]uint8{r, g, b, a} }

func (c *Compositor) canSkipBackgroundFill() bool {
	if c == nil || c.Root == nil || c.Root.transitionHidden || !c.Root.Visible || c.Root.Opacity == 0 {
		return false
	}
	if opaqueFullCanvasLayer(c.Root, 0, 0, c.Width, c.Height) {
		return true
	}
	if c.Root.Type == LayerEffect || c.Root.Type == LayerFilter {
		return false
	}
	ax, ay := c.Root.Bounds.X*c.Root.RenderScale(), c.Root.Bounds.Y*c.Root.RenderScale()
	for _, child := range c.Root.children {
		if opaqueFullCanvasLayer(child, ax, ay, c.Width, c.Height) {
			return true
		}
	}
	return false
}

func opaqueFullCanvasLayer(layer *Layer, parentX, parentY, width, height int32) bool {
	if layer == nil || layer.transitionHidden || !layer.Visible || layer.Opacity != 255 ||
		layer.clipSet || layer.Type == LayerBinder {
		return false
	}
	scale := layer.RenderScale()
	pixels := layer.RGBA
	if layer.inTransition && layer.transitionOpaque {
		pixels = layer.transitionPixels
	}
	if layer.Type != LayerOpaque && !(layer.Type == LayerAlpha && layer.inTransition && layer.transitionOpaque) {
		return false
	}
	if len(pixels) != int(uint64(width)*uint64(height)*4) {
		return false
	}
	ax := parentX + layer.Bounds.X*scale
	ay := parentY + layer.Bounds.Y*scale
	drawWidth, drawHeight := layer.Bounds.Width*scale, layer.Bounds.Height*scale
	if drawWidth == 0 {
		drawWidth = layer.deviceWidth()
	}
	if drawHeight == 0 {
		drawHeight = layer.deviceHeight()
	}
	return ax+layer.ImageLeft*scale == 0 && ay+layer.ImageTop*scale == 0 &&
		drawWidth == width && drawHeight == height &&
		layer.deviceWidth() == width && layer.deviceHeight() == height
}

// Compose creates a complete frame. A layer of type Effect or Filter returns
// ErrUnsupportedLayerType rather than silently producing an approximation.
func (c *Compositor) Compose(roots ...*Layer) (FrameSnapshot, error) {
	return c.compose(false, roots...)
}

// composeValidated is used by Services after validating the same root before
// inspecting it for transitions or hashing its render state.
func (c *Compositor) composeValidated() (FrameSnapshot, error) {
	return c.composeInto(nil, nil, true)
}

// composeInto composes into caller-owned scratch buffers. Compose continues to
// use fresh buffers; Services retains these for snapshots or synchronous
// borrowed presentation frames.
func (c *Compositor) composeInto(rgba, province []byte, skipValidation bool) (FrameSnapshot, error) {
	if c == nil || c.Width <= 0 || c.Height <= 0 || uint64(c.Width)*uint64(c.Height) > maxLayerPixels {
		return FrameSnapshot{}, fmt.Errorf("%w: invalid compositor dimensions", ErrInvalidLayer)
	}
	if c.Root == nil {
		pixels := int(c.Width) * int(c.Height)
		rgba = resizeBytes(rgba, pixels*4)
		province = resizeBytes(province, pixels)
		fillRGBA(rgba, c.Background)
		clear(province)
		return FrameSnapshot{Width: c.Width, Height: c.Height, RGBA: rgba, Province: province}, nil
	}
	if !skipValidation {
		if err := validateTree(c.Root, nil); err != nil {
			return FrameSnapshot{}, err
		}
	}
	pixels := int(c.Width) * int(c.Height)
	rgba = resizeBytes(rgba, pixels*4)
	province = resizeBytes(province, pixels)
	if !c.canSkipBackgroundFill() {
		fillRGBA(rgba, c.Background)
	}
	clear(province)
	f := FrameSnapshot{Width: c.Width, Height: c.Height, RGBA: rgba, Province: province}
	ctx := drawContext{width: c.Width, height: c.Height, scale: c.Scale, rgba: f.RGBA, province: f.Province}
	if err := ctx.drawLayer(c.Root, 0, 0); err != nil {
		return FrameSnapshot{}, err
	}
	return f, nil
}

// fullScreenOpaqueTransition finds the single transition layer whose pixels
// can stand in for a fully composed frame. It is deliberately conservative:
// uncertain tree structure or province output always uses the compositor.
func (c *Compositor) fullScreenOpaqueTransition() (*Layer, bool) {
	if c == nil || c.Root == nil || c.Width <= 0 || c.Height <= 0 ||
		uint64(c.Width)*uint64(c.Height) > maxLayerPixels {
		return nil, false
	}
	var candidate *Layer
	count := 0
	var visit func(*Layer, int64, int64, bool) bool
	visit = func(layer *Layer, parentX, parentY int64, ancestorsVisible bool) bool {
		if layer == nil {
			return true
		}
		visible := ancestorsVisible && !layer.transitionHidden && layer.Visible && layer.Opacity != 0
		if !visible {
			return true
		}
		ax, ay := parentX+int64(layer.Bounds.X), parentY+int64(layer.Bounds.Y)
		if layer.inTransition && layer.transitionOpaque && layer.Type == LayerAlpha &&
			layer.Opacity == 255 && !layer.clipSet &&
			ax+int64(layer.ImageLeft) == 0 && ay+int64(layer.ImageTop) == 0 &&
			layer.ImageWidth == c.Width && layer.ImageHeight == c.Height &&
			(layer.Bounds.Width == 0 || layer.Bounds.Width == c.Width) &&
			(layer.Bounds.Height == 0 || layer.Bounds.Height == c.Height) &&
			uint64(len(layer.transitionPixels)) == uint64(c.Width)*uint64(c.Height)*4 &&
			len(layer.Province) == 0 {
			count++
			candidate = layer
			for _, child := range layer.children {
				if visibleLayerSubtree(child) {
					return false
				}
			}
		}
		for i, child := range layer.children {
			before := count
			if !visit(child, ax, ay, true) {
				return false
			}
			if count > before {
				for _, later := range layer.children[i+1:] {
					if visibleLayerSubtree(later) {
						return false
					}
				}
			}
		}
		return true
	}
	if !visit(c.Root, 0, 0, true) || count != 1 {
		return nil, false
	}
	return candidate, true
}

func (c *Compositor) fullScreenOpaqueTransitionDiagnostic() (*Layer, bool, string) {
	if c == nil || c.Root == nil || c.Width <= 0 || c.Height <= 0 ||
		uint64(c.Width)*uint64(c.Height) > maxLayerPixels {
		return nil, false, "geometry"
	}
	reason := "no candidate"
	setReason := func(value string) {
		if reason == "no candidate" {
			reason = value
		}
	}
	var candidate *Layer
	count := 0
	var visit func(*Layer, int64, int64, bool) bool
	visit = func(layer *Layer, parentX, parentY int64, ancestorsVisible bool) bool {
		if layer == nil {
			return true
		}
		visible := ancestorsVisible && !layer.transitionHidden && layer.Visible && layer.Opacity != 0
		if !visible {
			return true
		}
		ax, ay := parentX+int64(layer.Bounds.X), parentY+int64(layer.Bounds.Y)
		if layer.inTransition {
			if !layer.transitionOpaque {
				setReason("transition_opaque")
			} else if layer.Type != LayerAlpha {
				setReason("transition_type")
			} else if layer.Opacity != 255 {
				setReason("transition_opacity")
			} else if layer.clipSet {
				setReason("clip")
			} else if len(layer.Province) != 0 {
				setReason("province")
			} else if ax+int64(layer.ImageLeft) != 0 || ay+int64(layer.ImageTop) != 0 ||
				layer.ImageWidth != c.Width || layer.ImageHeight != c.Height ||
				(layer.Bounds.Width != 0 && layer.Bounds.Width != c.Width) ||
				(layer.Bounds.Height != 0 && layer.Bounds.Height != c.Height) ||
				uint64(len(layer.transitionPixels)) != uint64(c.Width)*uint64(c.Height)*4 {
				setReason("geometry")
			} else {
				count++
				candidate = layer
				for _, child := range layer.children {
					if visibleLayerSubtree(child) {
						setReason("descendant/later overlay")
						return false
					}
				}
			}
		}
		for i, child := range layer.children {
			before := count
			if !visit(child, ax, ay, true) {
				return false
			}
			if count > before {
				for _, later := range layer.children[i+1:] {
					if visibleLayerSubtree(later) {
						setReason("descendant/later overlay")
						return false
					}
				}
			}
		}
		return true
	}
	if !visit(c.Root, 0, 0, true) || count != 1 {
		if count > 1 && reason == "no candidate" {
			reason = "descendant/later overlay"
		}
		return nil, false, reason
	}
	return candidate, true, ""
}

func visibleLayerSubtree(layer *Layer) bool {
	if layer == nil || layer.transitionHidden || !layer.Visible || layer.Opacity == 0 {
		return false
	}
	for _, child := range layer.children {
		if visibleLayerSubtree(child) {
			return true
		}
	}
	return true
}

func resizeBytes(buffer []byte, size int) []byte {
	if cap(buffer) < size {
		return make([]byte, size)
	}
	return buffer[:size]
}

func fillRGBA(out []byte, color [4]uint8) {
	if len(out) == 0 || color == [4]uint8{} {
		clear(out)
		return
	}
	out[0], out[1], out[2], out[3] = color[0], color[1], color[2], color[3]
	for filled := 4; filled < len(out); {
		n := filled
		if n > len(out)-filled {
			n = len(out) - filled
		}
		copy(out[filled:filled+n], out[:n])
		filled += n
	}
}

func (c *Compositor) compose(skipValidation bool, roots ...*Layer) (FrameSnapshot, error) {
	if len(roots) > 1 {
		return FrameSnapshot{}, fmt.Errorf("%w: Compose accepts at most one root", ErrInvalidLayer)
	}
	root := c.Root
	if len(roots) == 1 {
		root = roots[0]
	}
	if root != c.Root {
		return (&Compositor{Root: root, Width: c.Width, Height: c.Height, Background: c.Background}).composeInto(nil, nil, skipValidation)
	}
	return c.composeInto(nil, nil, skipValidation)
}

// ComposeRoot is a convenience for callers rendering a transient root.
func (c *Compositor) ComposeRoot(root *Layer) (FrameSnapshot, error) {
	if c == nil {
		return FrameSnapshot{}, fmt.Errorf("%w: nil compositor", ErrInvalidLayer)
	}
	return c.Compose(root)
}

func clearRGBA(width, height int32, color [4]uint8) []byte {
	out := make([]byte, int(width)*int(height)*4)
	fillRGBA(out, color)
	return out
}

func validateTree(l, expectedParent *Layer) error {
	return validateTreeSeen(l, expectedParent, make(map[LayerID]struct{}))
}

func validateTreeSeen(l, expectedParent *Layer, ids map[LayerID]struct{}) error {
	if l == nil {
		return nil
	}
	if l.Parent != expectedParent {
		return fmt.Errorf("%w: parent pointer mismatch", ErrLayerTree)
	}
	if l.Bounds.Width < 0 || l.Bounds.Height < 0 {
		return fmt.Errorf("%w: negative bounds", ErrInvalidLayer)
	}
	if !supportedLayerType(l.Type) {
		return fmt.Errorf("%w: layer %d type %d", ErrUnsupportedLayerType, l.ID, l.Type)
	}
	if _, ok := ids[l.ID]; ok {
		return fmt.Errorf("%w: duplicate layer id %d", ErrLayerTree, l.ID)
	}
	ids[l.ID] = struct{}{}
	for _, child := range l.children {
		if child == nil {
			return fmt.Errorf("%w: nil child", ErrLayerTree)
		}
		if err := validateTreeSeen(child, l, ids); err != nil {
			return err
		}
	}
	return nil
}

func supportedLayerType(t LayerType) bool {
	switch t {
	case LayerBinder, LayerOpaque, LayerAlpha, LayerAdditive, LayerSubtractive,
		LayerMultiplicative, LayerDodge, LayerDarken, LayerLighten, LayerScreen,
		LayerAddAlpha, LayerPSNormal, LayerPSAdditive, LayerPSSubtractive,
		LayerPSMultiplicative, LayerPSScreen, LayerPSOverlay, LayerPSHardLight,
		LayerPSSoftLight, LayerPSColorDodge, LayerPSColorDodge5, LayerPSColorBurn,
		LayerPSLighten, LayerPSDarken, LayerPSDifference, LayerPSDifference5,
		LayerPSExclusion:
		return true
	default:
		return false
	}
}

// HitTest returns the foremost enabled visible layer under compositor-space
// coordinates. Children are searched front-to-back, matching draw order.
func (c *Compositor) HitTest(x, y int32) (LayerID, bool) {
	if c == nil || c.Root == nil {
		return 0, false
	}
	if x < 0 || y < 0 || x >= c.Width || y >= c.Height {
		return 0, false
	}
	if l := hitLayer(c.Root, x, y, 0, 0); l != nil {
		return l.ID, true
	}
	return 0, false
}
