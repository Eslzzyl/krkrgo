package runtime

import (
	"encoding/binary"
	"hash/maphash"
	"math"
)

type RenderPlan struct {
	Width, Height int32
	Background    [4]uint8
	Nodes         []RenderNode
	ChildIndices  []int
	// StateHashValid marks a hash produced by CaptureRenderPlan.
	StateHash       uint64
	StateHashValid  bool
	StateHashStable bool
}
type RenderNode struct {
	ID                                                      LayerID
	Bounds                                                  Rect
	Opacity                                                 uint8
	Visible                                                 bool
	TransitionHidden                                        bool
	Type                                                    LayerType
	Hit                                                     HitType
	HoldAlpha                                               bool
	ImageWidth, ImageHeight, ImageLeft, ImageTop            int32
	RGBA                                                    []byte
	Motion                                                  *RenderMotion
	ImageHash                                               uint64
	ImageBaseHash                                           uint64
	ImageOpsHash                                            uint64
	ImageOps                                                []RenderImageOp
	ImageDirty                                              Rect
	ImageDirtyValid                                         bool
	GPUStretch                                              bool
	GPUStretchSourceWidth, GPUStretchSourceHeight           int32
	GPUStretchSourceX, GPUStretchSourceY                    int32
	GPUStretchCropWidth, GPUStretchCropHeight               int32
	GPUStretchDestinationX, GPUStretchDestinationY          int32
	GPUStretchDestinationWidth, GPUStretchDestinationHeight int32
	gpuStretchSourceBitmap                                  *Bitmap
	gpuImageOpBitmaps                                       []*Bitmap
	gpuImageOpLayers                                        []*Layer
	Clip                                                    Rect
	ClipSet                                                 bool
	ChildStart, ChildCount                                  int
	Transition                                              *RenderTransition
}

type RenderImageOpKind uint8

const (
	RenderImageOpCopy RenderImageOpKind = iota
	RenderImageOpOperate
	RenderImageOpAffine
)

type RenderImageSourceKind uint8

const (
	RenderImageSourceLayer RenderImageSourceKind = iota + 1
	RenderImageSourceBitmap
)

// RenderImageOp is a backend-neutral image operation. SourceRGBA is borrowed
// from the runtime until the next render-plan capture.
type RenderImageOp struct {
	Kind          RenderImageOpKind
	SourceKind    RenderImageSourceKind
	SourceID      uint64
	SourceRGBA    []byte
	SourceWidth   int32
	SourceHeight  int32
	SourceHash    uint64
	SourceOpaque  bool
	DX, DY        int32
	SX, SY        int32
	Width, Height int32
	Mode, Opacity int32
	Face          int32
	HoldAlpha     bool
	Clip          Rect
	Matrix        [6]float64
	Filter        int32
	Clear         bool
}

// RenderMotion describes one deferred Motion.Player composition. Draws map
// source-image pixels directly into the owning layer's image coordinates.
// RGBA slices are borrowed from the motion resource until the next frame.
type RenderMotion struct {
	Filter int64
	Scale  int32
	// AtlasKey groups Motion layers that can share source textures, including
	// the child layers created by SeparateLayerAdaptor.
	AtlasKey string
	// Width and Height are the owning layer's canvas dimensions in device
	// pixels; particle emitters use them for off-screen culling.
	Width, Height int
	Draws         []RenderMotionDraw
	Items         []RenderMotionItem
}

// MotionVertex is a GPU mesh vertex. Positions are in Motion canvas pixels;
// texture coordinates are in source-image pixels.
type MotionVertex struct {
	X, Y float32
	U, V float32
}

// RenderMotionItem is one ordered GPU Motion item. A group owns an off-screen
// target and applies its masks before compositing into the parent target.
type RenderMotionItem struct {
	SortKey float64
	Draw    RenderMotionDraw
	Group   *RenderMotionGroup
}

type RenderMotionGroup struct {
	Items    []RenderMotionItem
	Masks    []RenderMotionMask
	MaskMode int64
}

type RenderMotionMask struct {
	Items     []RenderMotionItem
	Operation uint8
}

// MotionBlendMode is the Photoshop blend operation a motion frame applies when
// it composites into the owning canvas. The values follow the reference's
// resolveBlendOperationModeLike_0x6C7440 mapping of the raw "bm" low nibble
// (PlayerRenderInternal.cpp:423-442).
type MotionBlendMode uint8

const (
	MotionBlendAlpha MotionBlendMode = iota
	MotionBlendAdditive
	MotionBlendSubtractive
	MotionBlendMultiplicative
	MotionBlendScreen
)

type RenderMotionDraw struct {
	// LayerID is local to LayerTreeKey and follows NodeTree's per-node
	// allocation order. Zero is reserved for baked whole-canvas draws.
	LayerID       int
	LayerTreeKey  string
	Viewport      [4]float64
	HasViewport   bool
	SourceKey     string
	RGBA          []byte
	Width, Height int32
	A, B, C, D    float64
	TX, TY        float64
	Opacity       uint8
	Opaque        bool
	Premultiplied bool
	Blend         MotionBlendMode
	SortKey       float64
	Vertices      []MotionVertex
	Indices       []uint16
	// Baked marks a canvas-sized frame produced for one motion tick (mesh or
	// stencil composition). The pixels change every frame and have no reusable
	// source identity, so the host streams them into one texture per layer.
	Baked bool
}

type RenderTransition struct {
	Method                                RenderTransitionMethod
	DestinationRGBA, SourceRGBA, Rule     []byte
	ImageWidth, ImageHeight               int32
	RuleWidth, RuleHeight                 int32
	Phase, Vague                          uint32
	Opaque, ZeroDestinationOpaque         bool
	WithChildren                          bool
	DestinationHash, SourceHash, RuleHash uint64
}
type RenderTransitionMethod uint8

const (
	RenderTransitionCrossfade RenderTransitionMethod = iota
	RenderTransitionUniversal
)

func deferredImageSourceRevision(sourceLayer *Layer, sourceBitmap *Bitmap, fallback uint64) uint64 {
	switch {
	case sourceLayer != nil:
		return sourceLayer.renderImageRevision()
	case sourceBitmap != nil:
		return sourceBitmap.imageRevision()
	case fallback != 0:
		return fallback
	default:
		return 1
	}
}

func renderPlanImageRevision(layer *Layer) uint64 {
	if layer == nil {
		return 1
	}
	if pending := layer.gpuStretch; pending != nil {
		sourceRevision := deferredImageSourceRevision(pending.sourceLayer, pending.sourceBitmap, pending.sourceRevision)
		return mixGPUStretchImageRevision(pending.sourceID, sourceRevision, pending.sourceWidth, pending.sourceHeight)
	}
	return layer.renderImageRevision()
}

func mixGPUStretchImageRevision(sourceID, revision uint64, width, height int32) uint64 {
	value := sourceID ^ 0x9e3779b97f4a7c15
	value ^= revision + 0x9e3779b97f4a7c15 + (value << 6) + (value >> 2)
	value ^= uint64(uint32(width))<<32 | uint64(uint32(height))
	value ^= value >> 30
	value *= 0xbf58476d1ce4e5b9
	value ^= value >> 27
	value *= 0x94d049bb133111eb
	value ^= value >> 31
	if value == 0 {
		return 1
	}
	return value
}

func renderImageOp(op layerImageOp) RenderImageOp {
	rendered := RenderImageOp{
		Kind:         RenderImageOpKind(op.kind),
		SourceID:     op.sourceID,
		SourceRGBA:   op.sourcePixels,
		SourceWidth:  op.sourceWidth,
		SourceHeight: op.sourceHeight,
		DX:           op.dx,
		DY:           op.dy,
		SX:           op.sx,
		SY:           op.sy,
		Width:        op.width,
		Height:       op.height,
		Mode:         op.mode,
		Opacity:      op.opacity,
		Face:         op.face,
		HoldAlpha:    op.holdAlpha,
		Clip:         op.clip,
		Matrix:       op.matrix,
		Filter:       op.filter,
		Clear:        op.clear,
	}
	if op.sourceLayer != nil {
		rendered.SourceKind = RenderImageSourceLayer
		if rendered.SourceID == 0 {
			rendered.SourceID = uint64(op.sourceLayer.ID)
		}
		rendered.SourceHash = deferredImageSourceRevision(op.sourceLayer, nil, op.sourceRevision)
		rendered.SourceOpaque = op.sourceOpaque
		if len(rendered.SourceRGBA) == 0 {
			rendered.SourceRGBA = op.sourceLayer.RGBA
		}
	} else if op.sourceBitmap != nil {
		rendered.SourceKind = RenderImageSourceBitmap
		rendered.SourceHash = deferredImageSourceRevision(nil, op.sourceBitmap, op.sourceRevision)
		if len(rendered.SourceRGBA) == 0 {
			rendered.SourceRGBA = op.sourceBitmap.RGBA
		}
	}
	if rendered.SourceHash == 0 {
		rendered.SourceHash = 1
	}
	return rendered
}

func renderImageOpsHash(layer *Layer) uint64 {
	if layer == nil {
		return 1
	}
	value := layer.renderImageRevision()
	mix := func(part uint64) {
		value ^= part + 0x9e3779b97f4a7c15 + (value << 6) + (value >> 2)
	}
	for _, op := range layer.gpuImageOps {
		sourceRevision := deferredImageSourceRevision(op.sourceLayer, op.sourceBitmap, op.sourceRevision)
		mix(sourceRevision)
		mix(op.sourceID)
		mix(uint64(op.kind))
		mix(uint64(op.sourceWidth)<<32 | uint64(uint32(op.sourceHeight)))
		mix(uint64(uint32(op.dx))<<32 | uint64(uint32(op.dy)))
		mix(uint64(uint32(op.sx))<<32 | uint64(uint32(op.sy)))
		mix(uint64(uint32(op.width))<<32 | uint64(uint32(op.height)))
		mix(uint64(uint32(op.mode))<<32 | uint64(uint32(op.opacity)))
		mix(uint64(uint32(op.face))<<32 | uint64(boolUint64(op.holdAlpha)))
		mix(uint64(uint32(op.clip.X))<<32 | uint64(uint32(op.clip.Y)))
		mix(uint64(uint32(op.clip.Width))<<32 | uint64(uint32(op.clip.Height)))
		mix(uint64(uint32(op.filter))<<32 | uint64(boolUint64(op.clear)))
		for _, part := range op.matrix {
			mix(math.Float64bits(part))
		}
	}
	if value == 0 {
		return 1
	}
	return value
}

// CaptureRenderPlan returns a borrowed, backend-neutral view of the current tree.
func (s *Services) CaptureRenderPlan() (RenderPlan, error) {
	return s.captureRenderPlanInto(nil)
}

// captureRenderPlanInto fills dst when provided. The returned plan borrows the
// destination slices until the next capture into the same destination.
func (s *Services) captureRenderPlanInto(dst *RenderPlan) (RenderPlan, error) {
	var p RenderPlan
	var previousHash uint64
	var previousHashValid bool
	if dst != nil {
		previousHash = dst.StateHash
		previousHashValid = dst.StateHashValid
		p = *dst
		p.Nodes = p.Nodes[:0]
		p.ChildIndices = p.ChildIndices[:0]
		p.StateHash = 0
		p.StateHashValid = false
		p.StateHashStable = false
	}
	if s.Compositor == nil && s.Root == nil {
		if dst != nil {
			*dst = p
		}
		return p, nil
	}
	if s.Compositor == nil {
		s.Compositor = NewCompositor(s.Root.Bounds.Width, s.Root.Bounds.Height)
		s.Compositor.Root = s.Root
	}
	c := s.Compositor
	if c.Root != nil {
		if err := s.validateRootTree(c.Root); err != nil {
			return p, err
		}
	}
	p.Width, p.Height, p.Background = c.Width, c.Height, c.Background
	seed := s.frameCacheSeed
	if seed == (maphash.Seed{}) {
		seed = maphash.MakeSeed()
		s.frameCacheSeed = seed
	}
	var bytesHash maphash.Hash
	bytesHash.SetSeed(seed)
	hashBytes := func(b []byte) uint64 {
		bytesHash.Reset()
		_, _ = bytesHash.Write(b)
		return bytesHash.Sum64()
	}
	var state maphash.Hash
	state.SetSeed(seed)
	var word [8]byte
	write := func(v uint64) {
		binary.LittleEndian.PutUint64(word[:], v)
		_, _ = state.Write(word[:])
	}
	write(uint64(uint32(p.Width)))
	write(uint64(uint32(p.Height)))
	for _, v := range p.Background {
		write(uint64(v))
	}
	type renderHashes struct {
		rgba, province, transition uint64
	}
	writeLayerState := func(layer *Layer, visible bool, hashes renderHashes) {
		var values [42]uint64
		values[0] = 1
		values[1] = uint64(uint32(layer.Bounds.X))
		values[2] = uint64(uint32(layer.Bounds.Y))
		values[3] = uint64(uint32(layer.Bounds.Width))
		values[4] = uint64(uint32(layer.Bounds.Height))
		values[5] = uint64(layer.Opacity)
		values[6] = boolUint64(layer.Visible)
		values[7] = uint64(layer.Type)
		values[8] = uint64(uint32(layer.ImageWidth))
		values[9] = uint64(uint32(layer.ImageHeight))
		values[10] = uint64(uint32(layer.ImageLeft))
		values[11] = uint64(uint32(layer.ImageTop))
		values[12] = boolUint64(layer.clipSet)
		values[13] = uint64(uint32(layer.clip.X))
		values[14] = uint64(uint32(layer.clip.Y))
		values[15] = uint64(uint32(layer.clip.Width))
		values[16] = uint64(uint32(layer.clip.Height))
		values[17] = boolUint64(layer.transitionHidden)
		values[18] = boolUint64(layer.inTransition)
		values[19] = uint64(len(layer.children))
		values[20] = uint64(uint32(layer.ProvinceWidth))
		values[21] = uint64(uint32(layer.ProvinceHeight))
		values[22] = uint64(len(layer.RGBA))
		values[23] = uint64(len(layer.Province))
		values[24] = uint64(len(layer.transitionPixels))
		if layer.gpuStretch != nil {
			values[25] = 1
			values[26] = uint64(uint32(layer.gpuStretch.sourceWidth))
			values[27] = uint64(uint32(layer.gpuStretch.sourceHeight))
			values[28] = uint64(uint32(layer.gpuStretch.destinationWidth))
			values[29] = uint64(uint32(layer.gpuStretch.destinationHeight))
			values[30] = uint64(uint32(layer.gpuStretch.stretchType))
			values[31] = uint64(uint32(layer.gpuStretch.sourceX))
			values[32] = uint64(uint32(layer.gpuStretch.sourceY))
			values[33] = uint64(uint32(layer.gpuStretch.cropWidth))
			values[34] = uint64(uint32(layer.gpuStretch.cropHeight))
			values[35] = uint64(uint32(layer.gpuStretch.destinationX))
			values[36] = uint64(uint32(layer.gpuStretch.destinationY))
		}
		values[37] = layer.renderImageRevision()
		values[38] = uint64(uint32(layer.Face))
		values[39] = boolUint64(layer.HoldAlpha)
		values[40] = layer.renderBaseImageRevision()
		values[41] = uint64(len(layer.gpuImageOps))
		var encoded [len(values) * 8]byte
		for i, value := range values {
			binary.LittleEndian.PutUint64(encoded[i*8:], value)
		}
		_, _ = state.Write(encoded[:])
		if visible {
			write(hashes.rgba)
			write(hashes.province)
			write(hashes.transition)
		}
	}
	writeNodeState := func(node *RenderNode) {
		write(uint64(node.ID))
		write(uint64(uint32(node.Bounds.X)))
		write(uint64(uint32(node.Bounds.Y)))
		write(uint64(uint32(node.Bounds.Width)))
		write(uint64(uint32(node.Bounds.Height)))
		write(uint64(node.Opacity))
		write(uint64(node.ImageHash))
		write(uint64(node.ImageOpsHash))
		write(uint64(node.ChildStart))
		write(uint64(node.ChildCount))
		if node.Transition != nil {
			write(uint64(node.Transition.Method))
			write(uint64(node.Transition.Phase))
			write(uint64(node.Transition.Vague))
			write(boolUint64(node.Transition.Opaque))
			write(boolUint64(node.Transition.ZeroDestinationOpaque))
			write(boolUint64(node.Transition.WithChildren))
			write(node.Transition.DestinationHash)
			write(node.Transition.SourceHash)
			write(node.Transition.RuleHash)
		}
	}
	var walk func(*Layer, int32, int32, LayerType, bool) (int, error)
	walk = func(l *Layer, px, py int32, target LayerType, ancestorsVisible bool) (int, error) {
		if l == nil {
			return -1, nil
		}
		if (l.Type == LayerEffect || l.Type == LayerFilter) && !l.transitionHidden && l.Visible && l.Opacity != 0 {
			return -1, unsupportedLayerType(l)
		}
		i := len(p.Nodes)
		p.Nodes = append(p.Nodes, RenderNode{})
		n := &p.Nodes[i]
		n.ID = l.ID
		n.Bounds = Rect{X: px + l.Bounds.X, Y: py + l.Bounds.Y, Width: l.Bounds.Width, Height: l.Bounds.Height}
		n.Opacity = clampLayerOpacity(l.Opacity)
		n.Visible = l.Visible
		n.TransitionHidden = l.transitionHidden
		n.Type = l.Type
		n.Hit = l.Hit
		n.HoldAlpha = l.HoldAlpha
		n.ImageWidth = l.deviceWidth()
		n.ImageHeight = l.deviceHeight()
		n.ImageLeft = l.ImageLeft
		n.ImageTop = l.ImageTop
		n.ImageBaseHash = l.renderBaseImageRevision()
		n.RGBA = l.RGBA
		if (!s.gpuCapture || len(l.transitionPixels) != 0) && len(l.gpuImageOps) != 0 {
			if err := l.materializeGPUImageOps(); err != nil {
				return -1, err
			}
			n.RGBA = l.RGBA
			n.ImageBaseHash = l.renderBaseImageRevision()
		}
		if s.gpuCapture && len(l.gpuImageOps) != 0 && len(l.transitionPixels) == 0 {
			n.ImageOps = n.ImageOps[:0]
			for _, op := range l.gpuImageOps {
				n.ImageOps = append(n.ImageOps, renderImageOp(op))
				if op.sourceBitmap != nil {
					n.gpuImageOpBitmaps = append(n.gpuImageOpBitmaps, op.sourceBitmap)
				}
				if op.sourceLayer != nil && op.sourceLayer != l {
					n.gpuImageOpLayers = append(n.gpuImageOpLayers, op.sourceLayer)
				}
			}
			n.ImageOpsHash = renderImageOpsHash(l)
		}
		hashes := renderHashes{}
		if l.gpuStretch != nil && len(l.transitionPixels) == 0 {
			pending := l.gpuStretch
			fullDestination := pending.destinationX <= 0 && pending.destinationY <= 0 &&
				pending.destinationX+pending.destinationWidth >= l.ImageWidth &&
				pending.destinationY+pending.destinationHeight >= l.ImageHeight &&
				(!l.clipSet || (l.clip.X <= 0 && l.clip.Y <= 0 &&
					l.clip.Right() >= l.ImageWidth && l.clip.Bottom() >= l.ImageHeight))
			if len(pending.sourcePixels) != 0 && fullDestination {
				n.GPUStretch = true
				n.GPUStretchSourceWidth = pending.sourceWidth
				n.GPUStretchSourceHeight = pending.sourceHeight
				n.GPUStretchSourceX = pending.sourceX
				n.GPUStretchSourceY = pending.sourceY
				n.GPUStretchCropWidth = pending.cropWidth
				n.GPUStretchCropHeight = pending.cropHeight
				n.GPUStretchDestinationX = pending.destinationX
				n.GPUStretchDestinationY = pending.destinationY
				n.GPUStretchDestinationWidth = pending.destinationWidth
				n.GPUStretchDestinationHeight = pending.destinationHeight
				n.RGBA = pending.sourcePixels
			} else {
				if err := l.materializeGPUStretch(); err != nil {
					return -1, err
				}
				n.RGBA = l.RGBA
			}
			n.gpuStretchSourceBitmap = pending.sourceBitmap
		}
		if l.gpuMotion != nil && len(l.transitionPixels) == 0 {
			if s.gpuCapture {
				n.Motion = l.gpuMotion
				n.RGBA = nil
			} else {
				if err := l.materializeGPUMotion(); err != nil {
					return -1, err
				}
				n.RGBA = l.RGBA
			}
		}
		transitionHashCached := l.transitionHashesValid && l.inTransition &&
			(l.transitionMethod == "crossfade" || l.transitionMethod == "universal")
		if len(l.transitionPixels) == 0 {
			if s.gpuCapture {
				if len(l.gpuImageOps) != 0 {
					hashes.rgba = renderImageOpsHash(l)
				} else {
					hashes.rgba = renderPlanImageRevision(l)
				}
			} else {
				hashes.rgba = hashBytes(n.RGBA)
			}
			n.ImageHash = hashes.rgba
		} else {
			if !transitionHashCached {
				hashes.transition = hashBytes(l.transitionPixels)
				n.ImageHash = hashes.transition
			}
			n.RGBA = l.transitionPixels
		}
		n.Clip = l.clip
		n.ClipSet = l.clipSet
		if l.inTransition && (l.transitionMethod == "crossfade" || l.transitionMethod == "universal") {
			method := RenderTransitionCrossfade
			if l.transitionMethod == "universal" {
				method = RenderTransitionUniversal
			}
			phaseMax := uint64(255)
			if method == RenderTransitionUniversal {
				phaseMax += uint64(l.transitionVague)
			}
			elapsed := uint64(0)
			if l.transitionTick > l.transitionStart {
				elapsed = l.transitionTick - l.transitionStart
			}
			phase := phaseMax
			if elapsed < l.transitionDuration && l.transitionDuration != 0 {
				phase = elapsed * phaseMax / l.transitionDuration
			}
			if transitionHashCached {
				hashes.transition = transitionPixelsHash(l, phase)
				n.ImageHash = hashes.transition
			}
			destinationHash, sourceHash, ruleHash := l.transitionDestHash, l.transitionSourceHash, l.transitionRuleHash
			if !transitionHashCached {
				destinationHash = hashBytes(l.transitionDestImage)
				sourceHash = hashBytes(l.transitionSourceImage)
				ruleHash = hashBytes(l.transitionRule)
			}
			n.Transition = &RenderTransition{
				Method:                method,
				DestinationRGBA:       l.transitionDestImage,
				SourceRGBA:            l.transitionSourceImage,
				Rule:                  l.transitionRule,
				ImageWidth:            l.deviceWidth(),
				ImageHeight:           l.deviceHeight(),
				RuleWidth:             l.deviceWidth(),
				RuleHeight:            l.deviceHeight(),
				Phase:                 uint32(phase),
				Vague:                 uint32(l.transitionVague),
				Opaque:                l.transitionOpaque,
				ZeroDestinationOpaque: l.transitionZeroOpaque,
				WithChildren:          l.transitionChildren,
				DestinationHash:       destinationHash,
				SourceHash:            sourceHash,
				RuleHash:              ruleHash,
			}
		}
		visible := ancestorsVisible && !l.transitionHidden && l.Visible && l.Opacity != 0
		if visible {
			if s.gpuCapture {
				hashes.province = renderPlanImageRevision(l)
			} else {
				hashes.province = hashBytes(l.Province)
			}
		}
		childTarget := l.Type
		if childTarget == LayerBinder {
			childTarget = target
		}
		// Keep each parent's child slots contiguous; recursion appends descendant slots separately.
		parentX, parentY := p.Nodes[i].Bounds.X, p.Nodes[i].Bounds.Y
		var childIndexStorage [8]int
		childIndices := childIndexStorage[:0]
		if len(l.children) > len(childIndexStorage) {
			childIndices = make([]int, 0, len(l.children))
		}
		for _, ch := range l.children {
			if ch == nil {
				continue
			}
			childIndex := len(p.Nodes)
			if _, err := walk(ch, parentX, parentY, childTarget, visible); err != nil {
				return -1, err
			}
			childIndices = append(childIndices, childIndex)
		}
		childStart := len(p.ChildIndices)
		p.ChildIndices = append(p.ChildIndices, childIndices...)
		p.Nodes[i].ChildStart = childStart
		p.Nodes[i].ChildCount = len(childIndices)
		writeLayerState(l, visible, hashes)
		writeNodeState(&p.Nodes[i])
		dirty, dirtyValid := l.takeImageDirtyRect()
		if p.Nodes[i].Transition == nil && !p.Nodes[i].GPUStretch && p.Nodes[i].Motion == nil {
			p.Nodes[i].ImageDirty, p.Nodes[i].ImageDirtyValid = dirty, dirtyValid
		}
		return i, nil
	}
	if c.Root != nil {
		if _, err := walk(c.Root, 0, 0, c.Root.Type, true); err != nil {
			return RenderPlan{}, err
		}
	}
	p.StateHash = state.Sum64()
	p.StateHashValid = true
	p.StateHashStable = previousHashValid && previousHash == p.StateHash
	if dst != nil {
		*dst = p
	}
	return p, nil
}
