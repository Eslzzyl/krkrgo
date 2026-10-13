package runtime

import (
	"bytes"
	"strings"
	"testing"
)

func TestCaptureRenderPlanTreeAndHashes(t *testing.T) {
	root := NewRootLayer(1, 32, 24)
	root.SetPosition(4, 5)
	binder := NewLayer(2)
	if err := binder.SetType(LayerBinder); err != nil {
		t.Fatal(err)
	}
	binder.SetPosition(3, 4)
	binder.SetClip(Rect{X: 1, Y: 2, Width: 5, Height: 6})
	binder.SetImagePosition(7, 8)
	child := NewLayer(3)
	if err := child.SetBounds(Rect{Width: 1, Height: 1}); err != nil {
		t.Fatal(err)
	}
	child.SetPosition(6, 7)
	if err := child.SetRGBA(3, 3, make([]byte, 3*3*4)); err != nil {
		t.Fatal(err)
	}
	if err := child.SetImagePosition(-1, -1); err != nil {
		t.Fatal(err)
	}
	if err := binder.AddChild(child); err != nil {
		t.Fatal(err)
	}
	last := NewLayer(4)
	if err := last.SetRGBA(1, 1, []byte{40, 50, 60, 255}); err != nil {
		t.Fatal(err)
	}
	if err := root.AddChild(binder); err != nil {
		t.Fatal(err)
	}
	if err := root.AddChild(last); err != nil {
		t.Fatal(err)
	}
	s := DefaultServices()
	s.Root = root
	p, err := s.CaptureRenderPlan()
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Nodes) != 4 || len(p.ChildIndices) != 3 {
		t.Fatalf("topology = %d nodes, %v", len(p.Nodes), p.ChildIndices)
	}
	for i, id := range []LayerID{1, 2, 3, 4} {
		if p.Nodes[i].ID != id {
			t.Errorf("node %d id=%d", i, id)
		}
	}
	if !bytes.Equal([]byte{1, 2, 3}, []byte{byte(p.ChildIndices[0]), byte(p.ChildIndices[1]), byte(p.ChildIndices[2])}) {
		t.Errorf("child indices=%v", p.ChildIndices)
	}
	if p.Nodes[0].ChildStart != 0 || p.Nodes[0].ChildCount != 2 || p.Nodes[1].ChildStart != 1 || p.Nodes[1].ChildCount != 1 {
		t.Errorf("child ranges=%+v %+v", p.Nodes[0], p.Nodes[1])
	}
	if p.Nodes[2].Bounds.X != 13 || p.Nodes[2].Bounds.Y != 16 || p.Nodes[2].ImageLeft != -1 || p.Nodes[2].ImageTop != -1 {
		t.Errorf("absolute/image offsets=%+v child=%+v", p.Nodes[2].Bounds, p.Nodes[2])
	}
	if !p.Nodes[1].ClipSet || p.Nodes[1].Clip != (Rect{X: 1, Y: 2, Width: 5, Height: 6}) {
		t.Errorf("clip=%+v set=%v", p.Nodes[1].Clip, p.Nodes[1].ClipSet)
	}
	oldImage, oldState := p.Nodes[2].ImageHash, p.StateHash
	child.RGBA[0]++
	p2, err := s.CaptureRenderPlan()
	if err != nil {
		t.Fatal(err)
	}
	if p2.Nodes[2].ImageHash == oldImage || p2.StateHash == oldState {
		t.Fatal("direct RGBA mutation did not change hashes")
	}
}
func TestTickForGPUPresentationDispatchesOnPaintAndCapturesFrame(t *testing.T) {
	session := NewSession(DefaultServices(), 10_000)
	defer session.Close()
	if _, err := session.executeSource("<gpu-on-paint>", `
class PaintProbe extends Layer {
	var paints = 0;
	function PaintProbe(win) { super.Layer(win, null); setSize(4, 4); }
	function onPaint() { paints++; super.onPaint(...); }
}
window = new Window();
paintLayer = new PaintProbe(window);
paintLayer.update();
`, false); err != nil {
		t.Fatal(err)
	}
	plan, err := session.TickForGPUPresentation(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Nodes) == 0 {
		t.Fatal("GPU render plan has no root node")
	}
	value, err := session.Evaluate("paintLayer.paints")
	if err != nil {
		t.Fatal(err)
	}
	if got := value.Text(); got != "1" {
		t.Fatalf("GPU onPaint count = %s, want 1", got)
	}
	if _, err := session.Evaluate("paintLayer.update()"); err != nil {
		t.Fatal(err)
	}
	if _, err := session.TickForGPUPresentation(16); err != nil {
		t.Fatal(err)
	}
	value, err = session.Evaluate("paintLayer.paints")
	if err != nil {
		t.Fatal(err)
	}
	if got := value.Text(); got != "2" {
		t.Fatalf("GPU onPaint count after second tick = %s, want 2", got)
	}
}

func TestCaptureRenderPlanTransitionMetadata(t *testing.T) {
	root := NewRootLayer(1, 2, 2)
	d := NewLayer(2)
	src := NewLayer(3)
	d.SetRGBA(2, 2, []byte{1, 2, 3, 255, 4, 5, 6, 255, 7, 8, 9, 255, 10, 11, 12, 255})
	src.SetRGBA(2, 2, []byte{12, 11, 10, 255, 9, 8, 7, 255, 6, 5, 4, 255, 3, 2, 1, 255})
	root.AddChild(d)
	root.AddChild(src)
	d.inTransition, d.transitionMethod, d.transitionChildren = true, "universal", true
	d.transitionDestImage = []byte{1, 2}
	d.transitionSourceImage = []byte{3, 4}
	d.transitionRule = []byte{5, 6}
	d.transitionTick, d.transitionDuration = 123, 272
	d.transitionVague = 17
	d.transitionOpaque, d.transitionZeroOpaque = true, true
	s := DefaultServices()
	s.Root = root
	p, err := s.CaptureRenderPlan()
	if err != nil {
		t.Fatal(err)
	}
	tr := p.Nodes[1].Transition
	if tr == nil || tr.Method != RenderTransitionUniversal || tr.Phase != 123 || tr.Vague != 17 || !tr.Opaque || !tr.ZeroDestinationOpaque || !tr.WithChildren {
		t.Fatalf("transition=%+v", tr)
	}
	if !bytes.Equal(tr.DestinationRGBA, []byte{1, 2}) || !bytes.Equal(tr.SourceRGBA, []byte{3, 4}) || !bytes.Equal(tr.Rule, []byte{5, 6}) {
		t.Fatalf("transition buffers=%+v", tr)
	}
}

func TestCaptureRenderPlanRejectsEffectAndFilter(t *testing.T) {
	for _, typ := range []LayerType{LayerEffect, LayerFilter} {
		root := NewRootLayer(1, 1, 1)
		bad := NewLayer(2)
		if err := bad.SetType(typ); err != nil {
			t.Fatal(err)
		}
		root.AddChild(bad)
		s := DefaultServices()
		s.Root = root
		_, err := s.CaptureRenderPlan()
		if err == nil || !strings.Contains(err.Error(), "unsupported") {
			t.Fatalf("type %v error=%v", typ, err)
		}
	}
}
