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
	StateHash     uint64
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

func renderBytesHash(seed maphash.Seed, b []byte) uint64 {
	var h maphash.Hash
	h.SetSeed(seed)
	_, _ = h.Write(b)
	return h.Sum64()
}

// CaptureRenderPlan returns a borrowed, backend-neutral view of the current tree.
func (s *Services) CaptureRenderPlan() (RenderPlan, error) {
	var p RenderPlan
	if s.Compositor == nil && s.Root == nil {
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
	var walk func(*Layer, int32, int32, LayerType) (int, error)
	walk = func(l *Layer, px, py int32, target LayerType) (int, error) {
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
		n.ImageHash = renderBytesHash(seed, n.RGBA)
		n.Clip = l.clip
		n.ClipSet = l.clipSet
		if l.inTransition && len(l.transitionPixels) != 0 {
			n.RGBA = l.transitionPixels
			n.ImageHash = renderBytesHash(seed, n.RGBA)
		}
		if l.inTransition {
			method := RenderTransitionCrossfade
			if l.transitionMethod != "crossfade" {
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
				DestinationHash:       renderBytesHash(seed, l.transitionDestImage),
				SourceHash:            renderBytesHash(seed, l.transitionSourceImage),
				RuleHash:              renderBytesHash(seed, l.transitionRule),
			}
		}
		childTarget := l.Type
		if childTarget == LayerBinder {
			childTarget = target
		}
		childStart := len(p.ChildIndices)
		p.Nodes[i].ChildStart = childStart
		parentX, parentY := p.Nodes[i].Bounds.X, p.Nodes[i].Bounds.Y
		childCount := 0
		for _, ch := range l.children {
			if ch == nil {
				continue
			}
			childSlot := len(p.ChildIndices)
			childIndex := len(p.Nodes)
			p.ChildIndices = append(p.ChildIndices, childIndex)
			childCount++
			ci, err := walk(ch, parentX, parentY, childTarget)
			if err != nil {
				return -1, err
			}
			p.ChildIndices[childSlot] = ci
		}
		p.Nodes[i].ChildCount = childCount
		return i, nil
	}
	if c.Root != nil {
		if _, err := walk(c.Root, 0, 0, c.Root.Type); err != nil {
			return RenderPlan{}, err
		}
	}
	if err := hashLayerRenderState(&state, c.Root, write); err != nil {
		return RenderPlan{}, err
	}
	for _, n := range p.Nodes {
		write(uint64(n.ID))
		write(uint64(uint32(n.Bounds.X)))
		write(uint64(uint32(n.Bounds.Y)))
		write(uint64(uint32(n.Bounds.Width)))
		write(uint64(uint32(n.Bounds.Height)))
		write(uint64(n.Opacity))
		write(uint64(n.ImageHash))
		write(uint64(n.ChildStart))
		write(uint64(n.ChildCount))
		if n.Transition != nil {
			write(uint64(n.Transition.Method))
			write(uint64(n.Transition.Phase))
			write(uint64(n.Transition.Vague))
			write(boolUint64(n.Transition.Opaque))
			write(boolUint64(n.Transition.ZeroDestinationOpaque))
			write(boolUint64(n.Transition.WithChildren))
			write(n.Transition.DestinationHash)
			write(n.Transition.SourceHash)
			write(n.Transition.RuleHash)
		}
	}
	p.StateHash = state.Sum64()
	return p, nil
}
