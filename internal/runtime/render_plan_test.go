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
	if !p.StateHashValid {
		t.Fatal("captured RenderPlan did not mark StateHashValid")
	}
	if len(p.Nodes) != 4 || len(p.ChildIndices) != 3 {
		t.Fatalf("topology = %d nodes, %v", len(p.Nodes), p.ChildIndices)
	}
	for i, id := range []LayerID{1, 2, 3, 4} {
		if p.Nodes[i].ID != id {
			t.Errorf("node %d id=%d", i, id)
		}
	}
	if !bytes.Equal([]byte{2, 1, 3}, []byte{byte(p.ChildIndices[0]), byte(p.ChildIndices[1]), byte(p.ChildIndices[2])}) {
		t.Errorf("child indices=%v", p.ChildIndices)
	}
	if p.Nodes[0].ChildStart != 1 || p.Nodes[0].ChildCount != 2 || p.Nodes[1].ChildStart != 0 || p.Nodes[1].ChildCount != 1 {
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
func TestCaptureRenderPlanReusesBackingStorage(t *testing.T) {
	root := NewRootLayer(1, 16, 16)
	child := NewLayer(2)
	if err := child.SetRGBA(2, 2, make([]byte, 16)); err != nil {
		t.Fatal(err)
	}
	if err := root.AddChild(child); err != nil {
		t.Fatal(err)
	}
	services := DefaultServices()
	services.Root = root
	var scratch RenderPlan
	first, err := services.captureRenderPlanInto(&scratch)
	if err != nil {
		t.Fatal(err)
	}
	firstNode, firstChild := &first.Nodes[0], &first.ChildIndices[0]
	second, err := services.captureRenderPlanInto(&scratch)
	if err != nil {
		t.Fatal(err)
	}
	if first.StateHashStable {
		t.Fatal("first RenderPlan capture was marked stable")
	}
	if !second.StateHashStable {
		t.Fatal("repeated RenderPlan capture was not marked stable")
	}
	if &second.Nodes[0] != firstNode {
		t.Fatal("render-plan node storage was not reused")
	}
	if &second.ChildIndices[0] != firstChild {
		t.Fatal("render-plan child-index storage was not reused")
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
func TestTickForGPUPresentationReusesStaticPlanAndInvalidatesOnLayerChange(t *testing.T) {
	services := DefaultServices()
	root := NewRootLayer(1, 4, 4)
	if err := root.SetRGBA(4, 4, make([]byte, 4*4*4)); err != nil {
		t.Fatal(err)
	}
	child := NewLayer(2)
	if err := child.SetRGBA(2, 2, make([]byte, 2*2*4)); err != nil {
		t.Fatal(err)
	}
	if err := root.AddChild(child); err != nil {
		t.Fatal(err)
	}
	services.SetRoot(root)
	session := NewSession(services, 10_000)
	defer session.Close()
	first, err := session.TickForGPUPresentation(0)
	if err != nil {
		t.Fatal(err)
	}
	second, err := session.TickForGPUPresentation(16)
	if err != nil {
		t.Fatal(err)
	}
	if !second.StateHashStable || len(first.Nodes) != len(second.Nodes) ||
		&first.Nodes[0] != &second.Nodes[0] {
		t.Fatalf("static GPU plan was not reused: first=%+v second=%+v", first, second)
	}
	child.SetPosition(1, 0)
	third, err := session.TickForGPUPresentation(32)
	if err != nil {
		t.Fatal(err)
	}
	if third.StateHashStable || third.Nodes[1].Bounds.X != 1 {
		t.Fatalf("GPU plan cache did not invalidate after layer change: %+v", third.Nodes[1])
	}
}
func TestGPUPlanImageRevisionTracksLayerMutation(t *testing.T) {
	root := NewRootLayer(1, 2, 1)
	if err := root.SetRGBA(2, 1, []byte{1, 2, 3, 255, 4, 5, 6, 255}); err != nil {
		t.Fatal(err)
	}
	child := NewLayer(2)
	if err := child.SetRGBA(2, 1, []byte{10, 20, 30, 255, 40, 50, 60, 255}); err != nil {
		t.Fatal(err)
	}
	if err := root.AddChild(child); err != nil {
		t.Fatal(err)
	}
	services := DefaultServices()
	services.Root = root
	services.gpuCapture = true
	first, err := services.CaptureRenderPlan()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.SetRGBA(2, 1, []byte{99, 20, 30, 255, 40, 50, 60, 255}); err != nil {
		t.Fatal(err)
	}
	second, err := services.CaptureRenderPlan()
	if err != nil {
		t.Fatal(err)
	}
	if first.Nodes[1].ImageHash == second.Nodes[1].ImageHash {
		t.Fatal("GPU image revision did not change after layer mutation")
	}
}
func TestGPUPlanCacheTracksBitmapStretchSource(t *testing.T) {
	services := DefaultServices()
	root := NewRootLayer(1, 4, 1)
	if err := root.SetRGBA(4, 1, make([]byte, 4*4)); err != nil {
		t.Fatal(err)
	}
	source, err := newBitmap(2, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !root.deferGPUStretchCopy(nativeLayerImage{
		bitmap: source, width: 2, height: 1, pixels: source.RGBA,
	}, 4, 1, 2, 1, 1) {
		t.Fatal("bitmap GPU stretch was not deferred")
	}
	services.Root = root
	session := NewSession(services, 10_000)
	defer session.Close()
	first, err := session.TickForGPUPresentation(0)
	if err != nil {
		t.Fatal(err)
	}
	firstHash := first.Nodes[0].ImageHash
	if _, err := session.TickForGPUPresentation(16); err != nil {
		t.Fatal(err)
	}
	if !session.gpuPlan.StateHashStable {
		t.Fatal("unchanged bitmap stretch plan was not reused")
	}
	source.markDirty()
	third, err := session.TickForGPUPresentation(32)
	if err != nil {
		t.Fatal(err)
	}
	if third.StateHashStable || firstHash == third.Nodes[0].ImageHash {
		t.Fatal("bitmap stretch source mutation did not invalidate the GPU plan")
	}
}

func TestGPUStretchRenderPlanDefersAndCPUCaptureMaterializes(t *testing.T) {
	root := NewRootLayer(1, 4, 2)
	if err := root.SetRGBA(4, 2, make([]byte, 4*2*4)); err != nil {
		t.Fatal(err)
	}
	source := NewLayer(2)
	if err := source.SetRGBA(2, 1, []byte{
		10, 20, 30, 255, 40, 50, 60, 255,
	}); err != nil {
		t.Fatal(err)
	}
	if !root.deferGPUStretchCopy(nativeLayerImage{
		layer: source, width: source.ImageWidth, height: source.ImageHeight, pixels: source.RGBA,
	}, 4, 2, 2, 1, 1) {
		t.Fatal("full-image GPU stretch was not deferred")
	}
	services := DefaultServices()
	services.Root = root
	plan, err := services.CaptureRenderPlan()
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Nodes) != 1 || !plan.Nodes[0].GPUStretch ||
		plan.Nodes[0].GPUStretchSourceWidth != 2 || plan.Nodes[0].GPUStretchSourceHeight != 1 ||
		!bytes.Equal(plan.Nodes[0].RGBA, source.RGBA) {
		t.Fatalf("GPU stretch render node = %+v", plan.Nodes[0])
	}
	if _, err := services.CaptureFrame(); err != nil {
		t.Fatal(err)
	}
	if root.gpuStretch != nil {
		t.Fatal("CPU capture did not materialize pending GPU stretch")
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

func TestCaptureRenderPlanCachedTransitionHashesTrackPhase(t *testing.T) {
	services := benchmarkTransitionRenderPlanServices(true)
	first, err := services.CaptureRenderPlan()
	if err != nil {
		t.Fatal(err)
	}
	destination := services.Root.ChildAt(0)
	if destination == nil || first.Nodes[1].Transition == nil {
		t.Fatal("cached transition render plan is missing its destination node")
	}
	transition := first.Nodes[1].Transition
	if transition.DestinationHash != destination.transitionDestHash ||
		transition.SourceHash != destination.transitionSourceHash ||
		transition.RuleHash != destination.transitionRuleHash {
		t.Fatalf("cached transition hashes = (%d, %d, %d), want (%d, %d, %d)",
			transition.DestinationHash, transition.SourceHash, transition.RuleHash,
			destination.transitionDestHash, destination.transitionSourceHash, destination.transitionRuleHash)
	}
	oldImageHash := first.Nodes[1].ImageHash
	destination.transitionTick++
	second, err := services.CaptureRenderPlan()
	if err != nil {
		t.Fatal(err)
	}
	if second.Nodes[1].ImageHash == oldImageHash {
		t.Fatal("cached transition image hash did not change with phase")
	}
}

func benchmarkTransitionRenderPlanServices(cached bool) *Services {
	const width, height = 1280, 720
	root := NewRootLayer(1, width, height)
	destination := NewLayer(2)
	source := NewLayer(3)
	destinationPixels := make([]byte, width*height*4)
	sourcePixels := make([]byte, width*height*4)
	rule := make([]byte, width*height)
	for i := range destinationPixels {
		destinationPixels[i] = byte(i)
		sourcePixels[i] = byte(i >> 3)
	}
	for i := range rule {
		rule[i] = byte(i >> 4)
	}
	if err := destination.SetRGBA(width, height, destinationPixels); err != nil {
		panic(err)
	}
	if err := source.SetRGBA(width, height, sourcePixels); err != nil {
		panic(err)
	}
	destination.inTransition = true
	destination.transitionMethod = "universal"
	destination.transitionChildren = true
	destination.transitionDestImage = append([]byte(nil), destinationPixels...)
	destination.transitionSourceImage = append([]byte(nil), sourcePixels...)
	destination.transitionPixels = append([]byte(nil), destinationPixels...)
	destination.transitionRule = rule
	destination.transitionTick = 127
	destination.transitionDuration = 255
	destination.transitionVague = 64
	destination.transitionHashesValid = cached
	if cached {
		destination.transitionDestHash = hashTransitionBytes(destination.transitionDestImage)
		destination.transitionSourceHash = hashTransitionBytes(destination.transitionSourceImage)
		destination.transitionRuleHash = hashTransitionBytes(destination.transitionRule)
	}
	if err := root.AddChild(destination); err != nil {
		panic(err)
	}
	services := DefaultServices()
	services.Root = root
	return services
}

func BenchmarkCaptureRenderPlanUniversalTransition(b *testing.B) {
	for _, cached := range []struct {
		name  string
		value bool
	}{
		{name: "cached", value: true},
		{name: "full_hash", value: false},
	} {
		b.Run(cached.name, func(b *testing.B) {
			services := benchmarkTransitionRenderPlanServices(cached.value)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				services.TimeMS = uint64(i)
				if _, err := services.CaptureRenderPlan(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
