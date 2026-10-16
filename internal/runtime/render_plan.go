package runtime

import (
	"encoding/binary"
	"hash/maphash"
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
	ID                                           LayerID
	Bounds                                       Rect
	Opacity                                      uint8
	Visible                                      bool
	TransitionHidden                             bool
	Type                                         LayerType
	Hit                                          HitType
	HoldAlpha                                    bool
	ImageWidth, ImageHeight, ImageLeft, ImageTop int32
	RGBA                                         []byte
	ImageHash                                    uint64
	Clip                                         Rect
	ClipSet                                      bool
	ChildStart, ChildCount                       int
	Transition                                   *RenderTransition
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
		if err := validateTree(c.Root, nil); err != nil {
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
		var values [25]uint64
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
		n.Opacity = l.Opacity
		n.Visible = l.Visible
		n.TransitionHidden = l.transitionHidden
		n.Type = l.Type
		n.Hit = l.Hit
		n.HoldAlpha = l.HoldAlpha
		n.ImageWidth = l.ImageWidth
		n.ImageHeight = l.ImageHeight
		n.ImageLeft = l.ImageLeft
		n.ImageTop = l.ImageTop
		n.RGBA = l.RGBA
		hashes := renderHashes{}
		transitionHashCached := l.transitionHashesValid && l.inTransition &&
			(l.transitionMethod == "crossfade" || l.transitionMethod == "universal")
		if len(l.transitionPixels) == 0 {
			hashes.rgba = hashBytes(l.RGBA)
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
				ImageWidth:            l.ImageWidth,
				ImageHeight:           l.ImageHeight,
				RuleWidth:             l.ImageWidth,
				RuleHeight:            l.ImageHeight,
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
			hashes.province = hashBytes(l.Province)
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
