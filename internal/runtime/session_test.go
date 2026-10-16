package runtime

import (
	"bytes"
	"encoding/binary"
	"errors"
	"github.com/eslzzyl/krkrgo/internal/assets"
	"github.com/eslzzyl/krkrgo/internal/tjs"
	"hash/maphash"
	"log"
	"strings"
	"testing"
	"time"
)

type testClock uint64

func (c *testClock) NowMS() uint64 { return uint64(*c) }

func TestSessionEvaluateAndBudget(t *testing.T) {
	svc := DefaultServices()
	clock := testClock(0)
	svc.SetClock(&clock)
	s := NewSession(svc, 128)
	v, err := s.Evaluate("1 + 2")
	if err != nil {
		t.Fatal(err)
	}
	if n, err := v.Int(); err != nil || n != 3 {
		t.Fatalf("value=%v err=%v", v, err)
	}
	if err := s.Tick(10); err != nil {
		t.Fatal(err)
	}
	if svc.TimeMS != 10 {
		t.Fatalf("clock=%d", svc.TimeMS)
	}
	if err := s.Tick(9); !errors.Is(err, ErrClockBackwards) {
		t.Fatalf("backwards tick=%v", err)
	}
}
func TestTickForPresentationWithAudioDeltaKeepsLogicalClock(t *testing.T) {
	services := DefaultServices()
	services.SetRoot(NewRootLayer(1, 1, 1))
	session := NewSession(services, 10_000)
	defer session.Close()
	frame, err := session.TickForPresentationWithAudioDelta(16, 32)
	if err != nil {
		t.Fatal(err)
	}
	if frame.Empty() {
		t.Fatal("audio-delta presentation returned an empty frame")
	}
	if session.Services.TimeMS != 16 {
		t.Fatalf("logical time = %d, want 16", session.Services.TimeMS)
	}
	if got, want := len(session.AudioOutput), 32*OutputRate/1000*2; got != want {
		t.Fatalf("audio samples = %d, want %d", got, want)
	}
}

func TestCaptureFrameBeforeRootExists(t *testing.T) {
	services := DefaultServices()
	frame, err := services.CaptureFrame()
	if err != nil {
		t.Fatal(err)
	}
	if !frame.Empty() {
		t.Fatalf("frame before root initialization = %#v, want empty frame", frame)
	}
}

func TestSessionFrameAndAudioOwnership(t *testing.T) {
	s := NewSession(DefaultServices(), 16)
	root := NewRootLayer(1, 2, 1)
	if err := root.SetRGBA(2, 1, []byte{255, 0, 0, 255, 0, 255, 0, 255}); err != nil {
		t.Fatal(err)
	}
	s.Services.SetRoot(root)
	frame, err := s.CaptureFrame()
	if err != nil {
		t.Fatal(err)
	}
	if frame.Width != 2 || frame.Height != 1 || len(frame.RGBA) != 8 {
		t.Fatalf("frame=%#v", frame)
	}
	s.AudioOutput = []float32{1, 2}
	got := s.TakeAudio()
	if len(got) != 2 || len(s.AudioOutput) != 0 {
		t.Fatalf("audio=%v remaining=%v", got, s.AudioOutput)
	}
}

func TestSessionBorrowedFrameMatchesOwnedFrame(t *testing.T) {
	s := NewSession(DefaultServices(), 16)
	root := NewRootLayer(1, 2, 1)
	if err := root.SetRGBA(2, 1, []byte{255, 0, 0, 255, 0, 255, 0, 255}); err != nil {
		t.Fatal(err)
	}
	s.Services.SetRoot(root)
	borrowed, err := s.CaptureFrameBorrowed()
	if err != nil {
		t.Fatal(err)
	}
	owned, err := s.CaptureFrame()
	if err != nil {
		t.Fatal(err)
	}
	if borrowed.Width != owned.Width || borrowed.Height != owned.Height ||
		!bytes.Equal(borrowed.RGBA, owned.RGBA) || !bytes.Equal(borrowed.Province, owned.Province) {
		t.Fatalf("borrowed frame=%#v, owned frame=%#v", borrowed, owned)
	}
}

func TestServicesSetRootNilClearsCapturedScene(t *testing.T) {
	services := DefaultServices()
	root := NewRootLayer(1, 1, 1)
	if err := root.SetRGBA(1, 1, []byte{255, 0, 0, 255}); err != nil {
		t.Fatal(err)
	}
	services.SetRoot(root)
	if _, err := services.CaptureFrame(); err != nil {
		t.Fatal(err)
	}
	services.SetRoot(nil)
	frame, err := services.CaptureFrame()
	if err != nil {
		t.Fatal(err)
	}
	if services.Compositor == nil || services.Compositor.Root != nil {
		t.Fatal("nil root remained attached to compositor")
	}
	for _, pixel := range frame.RGBA {
		if pixel != 0 {
			t.Fatalf("cleared frame contains stale pixel data: %v", frame.RGBA)
		}
	}
}

func TestServicesCaptureFrameReuseTracksLayerStateAndOwnsSnapshots(t *testing.T) {
	services := DefaultServices()
	root := NewRootLayer(1, 1, 1)
	if err := root.SetRGBA(1, 1, []byte{255, 0, 0, 255}); err != nil {
		t.Fatal(err)
	}
	services.SetRoot(root)

	first, err := services.CaptureFrame()
	if err != nil {
		t.Fatal(err)
	}
	first.RGBA[0] = 0
	second, err := services.CaptureFrame()
	if err != nil {
		t.Fatal(err)
	}
	if second.RGBA[0] != 255 {
		t.Fatalf("returned snapshot mutation leaked into cached frame: %v", second.RGBA)
	}
	second.RGBA[0] = 0

	root.RGBA[0] = 0
	root.RGBA[2] = 255
	third, err := services.CaptureFrame()
	if err != nil {
		t.Fatal(err)
	}
	if third.RGBA[0] != 0 || third.RGBA[2] != 255 {
		t.Fatalf("direct layer image mutation was not captured: %v", third.RGBA)
	}

	if err := services.SetFrameSize(2, 1); err != nil {
		t.Fatal(err)
	}
	resized, err := services.CaptureFrame()
	if err != nil {
		t.Fatal(err)
	}
	if resized.Width != 2 || resized.Height != 1 || len(resized.RGBA) != 8 {
		t.Fatalf("resize retained stale frame dimensions: %#v", resized)
	}
}

func TestServicesCaptureFrameCacheTracksLayerMetadata(t *testing.T) {
	services := DefaultServices()
	root := NewRootLayer(1, 1, 1)
	if err := root.SetRGBA(1, 1, []byte{255, 0, 0, 255}); err != nil {
		t.Fatal(err)
	}
	services.SetRoot(root)
	if _, err := services.CaptureFrame(); err != nil {
		t.Fatal(err)
	}

	root.Opacity = 0
	frame, err := services.CaptureFrame()
	if err != nil {
		t.Fatal(err)
	}
	if frame.RGBA[3] != 0 {
		t.Fatalf("cached frame ignored direct opacity change: %v", frame.RGBA)
	}
}

func TestServicesCaptureFrameCacheTracksDirectProvinceMutation(t *testing.T) {
	services := DefaultServices()
	root := NewRootLayer(1, 1, 1)
	if err := root.SetRGBA(1, 1, []byte{255, 0, 0, 255}); err != nil {
		t.Fatal(err)
	}
	if err := root.SetProvince(1, 1, []byte{1}); err != nil {
		t.Fatal(err)
	}
	services.SetRoot(root)
	if _, err := services.CaptureFrame(); err != nil {
		t.Fatal(err)
	}

	root.Province[0] = 7
	frame, err := services.CaptureFrame()
	if err != nil {
		t.Fatal(err)
	}
	if len(frame.Province) != 1 || frame.Province[0] != 7 {
		t.Fatalf("cached frame ignored direct province mutation: %v", frame.Province)
	}
}

func TestServicesCaptureFrameSkipsHashingHiddenLayerPixels(t *testing.T) {
	services := DefaultServices()
	root := NewRootLayer(1, 1, 1)
	child := NewLayer(2)
	if err := child.SetRGBA(1, 1, []byte{255, 0, 0, 255}); err != nil {
		t.Fatal(err)
	}
	child.Opacity = 0
	if err := root.AddChild(child); err != nil {
		t.Fatal(err)
	}
	services.SetRoot(root)
	if _, err := services.CaptureFrame(); err != nil {
		t.Fatal(err)
	}
	fingerprint := services.frameCacheHash

	child.RGBA[0], child.RGBA[1] = 0, 255
	frame, err := services.CaptureFrame()
	if err != nil {
		t.Fatal(err)
	}
	if services.frameCacheHash != fingerprint {
		t.Fatal("hidden layer pixel mutation changed render fingerprint")
	}
	if frame.RGBA[0] != 0 || frame.RGBA[1] != 0 {
		t.Fatalf("hidden pixel mutation changed frame: %v", frame.RGBA)
	}

	child.Opacity = 255
	frame, err = services.CaptureFrame()
	if err != nil {
		t.Fatal(err)
	}
	if frame.RGBA[0] != 0 || frame.RGBA[1] != 255 {
		t.Fatalf("showing layer did not capture latest direct pixel mutation: %v", frame.RGBA)
	}
}

func TestServicesCaptureFrameCacheTracksTransitionPixelsAndChildOrder(t *testing.T) {
	services := DefaultServices()
	root := NewRootLayer(1, 1, 1)
	back := NewLayer(2)
	front := NewLayer(3)
	if err := back.SetRGBA(1, 1, []byte{255, 0, 0, 255}); err != nil {
		t.Fatal(err)
	}
	if err := front.SetRGBA(1, 1, []byte{0, 0, 255, 255}); err != nil {
		t.Fatal(err)
	}
	if err := root.AddChild(back); err != nil {
		t.Fatal(err)
	}
	if err := root.AddChild(front); err != nil {
		t.Fatal(err)
	}
	services.SetRoot(root)

	first, err := services.CaptureFrameForPresentation()
	if err != nil {
		t.Fatal(err)
	}
	hit, err := services.CaptureFrameForPresentation()
	if err != nil {
		t.Fatal(err)
	}
	if &first.RGBA[0] != &hit.RGBA[0] {
		t.Fatal("unchanged render state did not reuse the cached frame")
	}
	if err := root.MoveChild(back, 1); err != nil {
		t.Fatal(err)
	}
	reordered, err := services.CaptureFrameForPresentation()
	if err != nil {
		t.Fatal(err)
	}
	if reordered.RGBA[0] != 255 || reordered.RGBA[2] != 0 {
		t.Fatalf("child reorder did not invalidate composited output: %v", reordered.RGBA)
	}

	// Even when transition pixels are not currently selected for drawing,
	// their directly mutable storage remains part of visible render state.
	back.transitionPixels = []byte{0, 255, 0, 255}
	withTransitionStorage, err := services.CaptureFrameForPresentation()
	if err != nil {
		t.Fatal(err)
	}
	cachedStorage := services.frameCacheHash
	back.transitionPixels[1] = 0
	back.transitionPixels[2] = 255
	updated, err := services.CaptureFrameForPresentation()
	if err != nil {
		t.Fatal(err)
	}
	if services.frameCacheHash == cachedStorage {
		t.Fatal("direct transition-pixel mutation did not invalidate the cache")
	}
	if updated.RGBA[0] != withTransitionStorage.RGBA[0] {
		t.Fatal("unused transition pixels changed current frame bytes")
	}
}

func BenchmarkHashLayerRenderState(b *testing.B) {
	root := NewRootLayer(1, 1280, 720)
	root.RGBA = make([]byte, 1280*720*4)
	for i := range root.RGBA {
		root.RGBA[i] = byte(i)
	}
	for i := 0; i < 16; i++ {
		child := NewLayer(LayerID(i + 2))
		if err := child.SetRGBA(64, 64, make([]byte, 64*64*4)); err != nil {
			b.Fatal(err)
		}
		if err := root.AddChild(child); err != nil {
			b.Fatal(err)
		}
	}
	seed := maphash.MakeSeed()
	var result uint64
	b.SetBytes(int64(len(root.RGBA)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var state maphash.Hash
		state.SetSeed(seed)
		err := hashLayerRenderState(&state, root, func(value uint64) {
			var word [8]byte
			binary.LittleEndian.PutUint64(word[:], value)
			_, _ = state.Write(word[:])
		})
		if err != nil {
			b.Fatal(err)
		}
		result = state.Sum64()
	}
	_ = result
}
func TestServicesDynamicCaptureReusesScratchWithoutAliasingSnapshots(t *testing.T) {
	services := DefaultServices()
	root := NewRootLayer(1, 1, 1)
	if err := root.SetRGBA(1, 1, []byte{10, 20, 30, 255}); err != nil {
		t.Fatal(err)
	}
	if err := root.SetProvince(1, 1, []byte{3}); err != nil {
		t.Fatal(err)
	}
	root.inTransition = true
	services.SetRoot(root)

	first, err := services.CaptureFrame()
	if err != nil {
		t.Fatal(err)
	}
	firstRGBA, firstProvince := append([]byte(nil), first.RGBA...), append([]byte(nil), first.Province...)
	scratchRGBA, scratchProvince := &services.dynamicRGBA[0], &services.dynamicProvince[0]
	if scratchRGBA == &first.RGBA[0] || scratchProvince == &first.Province[0] {
		t.Fatal("dynamic scratch buffer escaped in the returned frame")
	}

	root.RGBA[0], root.Province[0] = 99, 7
	second, err := services.CaptureFrame()
	if err != nil {
		t.Fatal(err)
	}
	if &services.dynamicRGBA[0] != scratchRGBA || &services.dynamicProvince[0] != scratchProvince {
		t.Fatal("dynamic composition did not reuse its scratch buffers")
	}
	if !bytes.Equal(first.RGBA, firstRGBA) || !bytes.Equal(first.Province, firstProvince) {
		t.Fatalf("later capture mutated prior snapshot: RGBA=%v Province=%v", first.RGBA, first.Province)
	}
	if second.RGBA[0] != 99 || second.Province[0] != 7 {
		t.Fatalf("second frame did not reflect updated pixels: RGBA=%v Province=%v", second.RGBA, second.Province)
	}
}

func TestCaptureFrameForPresentationBorrowsDynamicAndCachedBuffers(t *testing.T) {
	services := DefaultServices()
	root := NewRootLayer(1, 1, 1)
	if err := root.SetRGBA(1, 1, []byte{10, 20, 30, 255}); err != nil {
		t.Fatal(err)
	}
	root.inTransition = true
	services.SetRoot(root)

	first, err := services.CaptureFrameForPresentation()
	if err != nil {
		t.Fatal(err)
	}
	if first.RenderStateHashValid {
		t.Fatal("dynamic presentation frame exposed a static render-state hash")
	}
	if &first.RGBA[0] != &services.dynamicRGBA[0] || &first.Province[0] != &services.dynamicProvince[0] {
		t.Fatal("presentation frame does not borrow dynamic composition buffers")
	}
	root.RGBA[0] = 99
	second, err := services.CaptureFrameForPresentation()
	if err != nil {
		t.Fatal(err)
	}
	if &first.RGBA[0] != &second.RGBA[0] || second.RGBA[0] != 99 {
		t.Fatal("dynamic presentation frame was not reused on the next capture")
	}
	if second.RenderStateHashValid {
		t.Fatal("dynamic presentation frame exposed a static render-state hash")
	}

	root.inTransition = false
	cached, err := services.CaptureFrameForPresentation()
	if err != nil {
		t.Fatal(err)
	}
	if &cached.RGBA[0] != &services.frameCache.RGBA[0] {
		t.Fatal("static presentation frame does not borrow the cached frame")
	}
	if !cached.RenderStateHashValid {
		t.Fatal("static presentation frame did not expose a render-state hash")
	}
	cachedAgain, err := services.CaptureFrameForPresentation()
	if err != nil {
		t.Fatal(err)
	}
	if !cachedAgain.RenderStateHashValid || cachedAgain.RenderStateHash != cached.RenderStateHash {
		t.Fatalf("static render-state hash changed across cache hit: first=%#x second=%#x", cached.RenderStateHash, cachedAgain.RenderStateHash)
	}
}

func newOpaqueTransitionServices(t *testing.T) (*Services, *Layer, []byte) {
	t.Helper()
	services := DefaultServices()
	root := NewRootLayer(1, 2, 2)
	base := NewLayer(2)
	base.Bounds = Rect{Width: 2, Height: 2}
	if err := base.SetRGBA(2, 2, []byte{
		1, 2, 3, 255, 4, 5, 6, 255,
		7, 8, 9, 255, 10, 11, 12, 255,
	}); err != nil {
		t.Fatal(err)
	}
	if err := root.AddChild(base); err != nil {
		t.Fatal(err)
	}
	transition := NewLayer(3)
	transition.Bounds = Rect{Width: 2, Height: 2}
	if err := transition.SetRGBA(2, 2, []byte{
		20, 21, 22, 255, 23, 24, 25, 255,
		26, 27, 28, 255, 29, 30, 31, 255,
	}); err != nil {
		t.Fatal(err)
	}
	pixels := []byte{
		40, 41, 42, 255, 43, 44, 45, 255,
		46, 47, 48, 255, 49, 50, 51, 255,
	}
	transition.inTransition = true
	transition.transitionOpaque = true
	transition.transitionPixels = pixels
	if err := root.AddChild(transition); err != nil {
		t.Fatal(err)
	}
	services.SetRoot(root)
	return services, transition, pixels
}

func TestCaptureFrameForPresentationBorrowsFullScreenOpaqueTransition(t *testing.T) {
	services, transition, pixels := newOpaqueTransitionServices(t)
	expected, err := services.Compositor.Compose()
	if err != nil {
		t.Fatal(err)
	}
	frame, err := services.CaptureFrameForPresentation()
	if err != nil {
		t.Fatal(err)
	}
	if &frame.RGBA[0] != &pixels[0] {
		t.Fatal("presentation did not borrow the full-screen transition pixels")
	}
	if !frame.Opaque {
		t.Fatal("opaque transition frame did not carry the alpha assertion")
	}
	if !bytes.Equal(frame.RGBA, expected.RGBA) || len(frame.Province) != 0 {
		t.Fatalf("borrowed transition frame differs from composition: RGBA=%v Province=%v expected=%v",
			frame.RGBA, frame.Province, expected.RGBA)
	}
	if got, ok := services.Compositor.fullScreenOpaqueTransition(); !ok || got != transition {
		t.Fatal("strict full-screen transition path was not eligible")
	}
}

func TestFullScreenOpaqueTransitionAllowsCoveredProvince(t *testing.T) {
	for _, test := range []struct {
		name  string
		layer func(*Layer) *Layer
	}{
		{name: "ancestor", layer: func(root *Layer) *Layer { return root }},
		{name: "earlier sibling", layer: func(root *Layer) *Layer { return root.children[0] }},
	} {
		t.Run(test.name, func(t *testing.T) {
			services, transition, _ := newOpaqueTransitionServices(t)
			covered := test.layer(services.Root)
			if err := covered.SetProvince(2, 2, []byte{1, 2, 3, 4}); err != nil {
				t.Fatal(err)
			}
			if got, ok := services.Compositor.fullScreenOpaqueTransition(); !ok || got != transition {
				t.Fatal("Province covered by the full-screen transition prevented eligibility")
			}
		})
	}
}

func TestFullScreenOpaqueTransitionDiagnosticReasons(t *testing.T) {
	tests := []struct {
		name   string
		reason string
		mutate func(*Layer)
	}{
		{
			name:   "non-opaque transition",
			reason: "transition_opaque",
			mutate: func(layer *Layer) { layer.transitionOpaque = false },
		},
		{
			name:   "non-alpha type",
			reason: "transition_type",
			mutate: func(layer *Layer) { layer.Type = LayerOpaque },
		},
		{
			name:   "partial opacity",
			reason: "transition_opacity",
			mutate: func(layer *Layer) { layer.Opacity = 128 },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			services, transition, _ := newOpaqueTransitionServices(t)
			test.mutate(transition)
			if _, ok, reason := services.Compositor.fullScreenOpaqueTransitionDiagnostic(); ok || reason != test.reason {
				t.Fatalf("diagnostic = (ok=%t, reason=%q), want (false, %q)", ok, reason, test.reason)
			}
		})
	}
}

func TestPresentationTransitionEligibilityDiagnostics(t *testing.T) {
	t.Setenv("KRKRGO_RENDER_STATS", "1")
	services, _, _ := newOpaqueTransitionServices(t)
	if _, err := services.CaptureFrameForPresentation(); err != nil {
		t.Fatal(err)
	}
	overlay := NewLayer(7)
	overlay.Bounds = Rect{Width: 1, Height: 1}
	if err := overlay.SetRGBA(1, 1, []byte{10, 20, 30, 255}); err != nil {
		t.Fatal(err)
	}
	if err := services.Root.AddChild(overlay); err != nil {
		t.Fatal(err)
	}
	if _, err := services.CaptureFrameForPresentation(); err != nil {
		t.Fatal(err)
	}
	diag := services.renderDiag
	if diag.presentationTransitionCandidates != 2 || diag.presentationTransitionHits != 1 ||
		diag.presentationTransitionFailure != "descendant/later overlay" || diag.presentationTransitionFailures != 1 {
		t.Fatalf("unexpected presentation transition diagnostics: candidates=%d hits=%d failure=%q failures=%d",
			diag.presentationTransitionCandidates, diag.presentationTransitionHits,
			diag.presentationTransitionFailure, diag.presentationTransitionFailures)
	}
}

func TestPresentationTransitionDiagnosticsDisabled(t *testing.T) {
	t.Setenv("KRKRGO_RENDER_STATS", "")
	services, _, _ := newOpaqueTransitionServices(t)
	if _, err := services.CaptureFrameForPresentation(); err != nil {
		t.Fatal(err)
	}
	diag := services.renderDiag
	if diag.presentationTransitionCandidates != 0 || diag.presentationTransitionHits != 0 ||
		diag.presentationTransitionFailure != "" || diag.presentationTransitionFailures != 0 {
		t.Fatalf("disabled render stats collected transition diagnostics: %+v", diag)
	}
}

func TestCaptureFrameForPresentationFallsBackWhenTransitionDoesNotCoverScene(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Layer, *Layer) error
	}{
		{
			name: "visible later sibling",
			mutate: func(root, transition *Layer) error {
				overlay := NewLayer(4)
				overlay.Bounds = Rect{Width: 1, Height: 1}
				if err := overlay.SetRGBA(1, 1, []byte{200, 100, 50, 255}); err != nil {
					return err
				}
				return root.AddChild(overlay)
			},
		},
		{
			name: "visible later sibling Province",
			mutate: func(root, transition *Layer) error {
				overlay := NewLayer(6)
				overlay.Bounds = Rect{Width: 1, Height: 1}
				if err := overlay.SetRGBA(1, 1, []byte{200, 100, 50, 255}); err != nil {
					return err
				}
				if err := overlay.SetProvince(1, 1, []byte{7}); err != nil {
					return err
				}
				return root.AddChild(overlay)
			},
		},
		{
			name: "visible descendant",
			mutate: func(root, transition *Layer) error {
				child := NewLayer(5)
				child.Bounds = Rect{Width: 1, Height: 1}
				return transition.AddChild(child)
			},
		},
		{
			name: "clip",
			mutate: func(root, transition *Layer) error {
				transition.clipSet = true
				transition.clip = Rect{Width: 1, Height: 2}
				return nil
			},
		},
		{
			name: "offset",
			mutate: func(root, transition *Layer) error {
				transition.ImageLeft = 1
				return nil
			},
		},
		{
			name: "province",
			mutate: func(root, transition *Layer) error {
				return transition.SetProvince(2, 2, []byte{1, 0, 0, 0})
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			services, transition, pixels := newOpaqueTransitionServices(t)
			if err := test.mutate(services.Root, transition); err != nil {
				t.Fatal(err)
			}
			expected, err := services.Compositor.Compose()
			if err != nil {
				t.Fatal(err)
			}
			frame, err := services.CaptureFrameForPresentation()
			if err != nil {
				t.Fatal(err)
			}
			if &frame.RGBA[0] == &pixels[0] {
				t.Fatal("unsafe transition borrowed path was used")
			}
			if !bytes.Equal(frame.RGBA, expected.RGBA) || !bytes.Equal(frame.Province, expected.Province) {
				t.Fatalf("fallback frame differs from composition: got RGBA=%v Province=%v expected RGBA=%v Province=%v",
					frame.RGBA, frame.Province, expected.RGBA, expected.Province)
			}
		})
	}
}

func TestCaptureFrameClonesFullScreenTransitionPixels(t *testing.T) {
	services, _, pixels := newOpaqueTransitionServices(t)
	frame, err := services.CaptureFrame()
	if err != nil {
		t.Fatal(err)
	}
	if &frame.RGBA[0] == &pixels[0] || !bytes.Equal(frame.RGBA, pixels) {
		t.Fatal("public capture did not return an owned copy of transition pixels")
	}
	frame.RGBA[0] = 0
	if pixels[0] != 40 {
		t.Fatal("mutating public capture changed transition pixels")
	}
}

func TestServicesCaptureFrameDoesNotHashActiveTransitionPixels(t *testing.T) {
	services := DefaultServices()
	root := NewRootLayer(1, 1, 1)
	if err := root.SetRGBA(1, 1, []byte{255, 0, 0, 255}); err != nil {
		t.Fatal(err)
	}
	services.SetRoot(root)
	if _, err := services.CaptureFrame(); err != nil {
		t.Fatal(err)
	}
	root.inTransition = true
	root.transitionPixels = []byte{0, 255, 0, 255}

	frame, err := services.CaptureFrame()
	if err != nil {
		t.Fatal(err)
	}
	if frame.RGBA[1] != 255 || frame.RGBA[0] != 0 {
		t.Fatalf("active transition frame = %v, want green", frame.RGBA)
	}
	if services.frameCacheValid {
		t.Fatal("active transition frame must not be retained as a reusable snapshot")
	}
}

func TestDispatchEventsDropsStaleSoundGeneration(t *testing.T) {
	services := DefaultServices()
	session := NewSession(services, 100)
	sound := NewWaveSoundBuffer()
	obj := session.VM.Objects.New(tjs.CategoryDictionary)
	if err := services.RegisterSound(obj.ID, sound); err != nil {
		t.Fatal(err)
	}
	services.SoundEvents[obj.ID] = []SoundEvent{
		{Generation: sound.Generation() + 1, Type: SoundEventStatus, Status: SoundPlay},
	}
	if err := services.Events.PostSoundBatch(uint64(obj.ID), EventTrigger, PriorityNormal, 1); err != nil {
		t.Fatal(err)
	}
	if err := session.DispatchEvents(); err != nil {
		t.Fatal(err)
	}
	if pending := services.SoundEvents[obj.ID]; len(pending) != 0 {
		t.Fatalf("stale sound events retained: %+v", pending)
	}
}

func TestServicesNativeMessagesAndTrace(t *testing.T) {
	s := NewSession(DefaultServices(), 32)
	s.Services.TraceEnabled = true
	if _, err := s.Evaluate("Debug.message('hello')"); err != nil {
		t.Fatal(err)
	}
	if len(s.Services.Messages) != 1 || s.Services.Messages[0] != "hello" {
		t.Fatalf("messages=%v", s.Services.Messages)
	}
	if len(s.Services.TraceEvents) == 0 {
		t.Fatal("trace is empty")
	}
	if !errors.Is(ErrUnsupportedDevice, ErrUnsupportedDevice) {
		t.Fatal("sentinel broken")
	}
}

func TestConsoleGlobalAliasesDebugConsoleAndLogs(t *testing.T) {
	session := NewSession(DefaultServices(), 128)
	if _, err := session.Evaluate(`global.consoleFromDebug = Debug.console; global.consoleAlias = console; console.info("console message"); Debug.console.visible = true;`); err != nil {
		t.Fatal(err)
	}
	got, err := session.Evaluate(`consoleFromDebug === consoleAlias && consoleAlias === Debug.console && Debug.console.visible`)
	if err != nil {
		t.Fatal(err)
	}
	if value, err := got.Int(); err != nil || value != 1 {
		t.Fatalf("console aliases/visible = %v, %v; want true", got, err)
	}
	if messages := session.Services.Messages; len(messages) != 1 || messages[0] != "console message" {
		t.Fatalf("console messages = %v, want [console message]", messages)
	}
}

func TestDispatchEventsDropsEveryStaleSoundTrigger(t *testing.T) {
	services := DefaultServices()
	session := NewSession(services, 100)
	sound := NewWaveSoundBuffer()
	obj := session.VM.Objects.New(tjs.CategoryDictionary)
	if err := services.RegisterSound(obj.ID, sound); err != nil {
		t.Fatal(err)
	}
	fires := 0
	if err := obj.Set("onFire", tjs.NewNativeFunction(session.VM.Objects, "onFire", func(_ tjs.ObjectRef, _ []tjs.Value) (tjs.Value, error) {
		fires++
		return tjs.VoidValue, nil
	})); err != nil {
		t.Fatal(err)
	}
	services.SoundEvents[obj.ID] = []SoundEvent{
		{Generation: sound.Generation() + 1, Type: SoundEventStatus, Status: SoundPlay},
		{Generation: sound.Generation() + 1, Type: SoundEventLabel, Label: "stale"},
	}
	if err := services.Events.PostSoundBatch(uint64(obj.ID), EventTrigger, PriorityNormal, 2); err != nil {
		t.Fatal(err)
	}
	if err := session.DispatchEvents(); err != nil {
		t.Fatal(err)
	}
	if fires != 0 {
		t.Fatalf("stale sound triggers called onFire %d times", fires)
	}
	if len(services.SoundEvents[obj.ID]) != 0 || services.Events.Len() != 0 {
		t.Fatalf("stale sound state retained: events=%v queue=%d", services.SoundEvents[obj.ID], services.Events.Len())
	}
}

func TestQueueSoundEventsRetriesAfterFullEventQueue(t *testing.T) {
	services := DefaultServices()
	sound := NewWaveSoundBuffer()
	const id = tjs.ObjectID(7)
	if err := services.RegisterSound(id, sound); err != nil {
		t.Fatal(err)
	}
	sound.pushEvent(SoundEvent{Generation: sound.Generation(), Type: SoundEventStatus, Status: SoundStop})
	if err := services.Events.SetMaxPending(1); err != nil {
		t.Fatal(err)
	}
	if err := services.Events.Post(99, EventTimer, PriorityNormal); err != nil {
		t.Fatal(err)
	}
	if err := services.queueSoundEvents(id, sound); err == nil {
		t.Fatal("full event queue accepted sound batch")
	}
	if len(sound.Events()) != 1 || len(services.SoundEvents[id]) != 0 {
		t.Fatalf("failed enqueue lost events: source=%v queued=%v", sound.Events(), services.SoundEvents[id])
	}
	through := services.Events.BeginBatch()
	if _, ok := services.Events.Pop(through, PriorityNormal); !ok {
		t.Fatal("failed to remove queue filler")
	}
	if err := services.queueSoundEvents(id, sound); err != nil {
		t.Fatal(err)
	}
	if len(sound.Events()) != 0 || len(services.SoundEvents[id]) != 1 {
		t.Fatalf("retry state source=%v queued=%v", sound.Events(), services.SoundEvents[id])
	}
	pending := services.Events.Pending()
	if len(pending) != 1 || pending[0].Kind != EventTrigger || !pending[0].Sound {
		t.Fatalf("unexpected retry queue=%v", pending)
	}
}

func TestRejectedTickDoesNotConsumeBudgetOrAudioState(t *testing.T) {
	session := NewSession(DefaultServices(), 10)
	if err := session.Tick(10); err != nil {
		t.Fatal(err)
	}
	wantBudget := session.Budget
	wantTime := session.Services.TimeMS
	wantAudio := append([]float32(nil), session.AudioOutput...)
	if err := session.Tick(9); !errors.Is(err, ErrClockBackwards) {
		t.Fatalf("backwards tick=%v", err)
	}
	if session.Budget != wantBudget || session.Services.TimeMS != wantTime || len(session.AudioOutput) != len(wantAudio) {
		t.Fatalf("backwards tick mutated state: budget=%d time=%d audio=%d", session.Budget, session.Services.TimeMS, len(session.AudioOutput))
	}
	if err := session.Tick(wantTime + uint64(MaxAudioBlockFrames/48+1)); err == nil {
		t.Fatal("oversized tick accepted")
	}
	if session.Budget != wantBudget || session.Services.TimeMS != wantTime || len(session.AudioOutput) != len(wantAudio) {
		t.Fatalf("oversized tick mutated state: budget=%d time=%d audio=%d", session.Budget, session.Services.TimeMS, len(session.AudioOutput))
	}
}

func TestTickRollbackRestoresAudioAndFailedEvent(t *testing.T) {
	services := DefaultServices()
	session := NewSession(services, 100)
	sound := NewWaveSoundBuffer()
	if err := sound.OpenAudio(assets.Audio{Channels: 1, SampleRate: OutputRate, Samples: deliverySamples(2_000)}, assets.LoopInfo{}); err != nil {
		t.Fatal(err)
	}
	if err := sound.Play(); err != nil {
		t.Fatal(err)
	}
	sound.DrainEvents()
	object := session.VM.Objects.New(tjs.CategoryDictionary)
	if err := services.RegisterSound(object.ID, sound); err != nil {
		t.Fatal(err)
	}
	fail := true
	callbackErr := errors.New("timer callback failed")
	if err := session.RegisterNative("testOnTimer", func(_ tjs.Value, _ []tjs.Value) (tjs.Value, error) {
		if fail {
			return tjs.VoidValue, callbackErr
		}
		return tjs.VoidValue, nil
	}); err != nil {
		t.Fatal(err)
	}
	callback, ok := session.VM.Globals["testOnTimer"]
	if !ok {
		t.Fatal("registered timer callback is missing")
	}
	if err := object.Set("onTimer", callback); err != nil {
		t.Fatal(err)
	}
	if err := services.Events.Post(uint64(object.ID), EventTimer, PriorityNormal); err != nil {
		t.Fatal(err)
	}
	if err := session.Tick(1); !errors.Is(err, callbackErr) {
		t.Fatalf("first tick error = %v, want callback error", err)
	}
	if services.TimeMS != 0 || sound.SamplePosition() != 0 || services.Events.Len() != 1 || session.Budget != 100 {
		t.Fatalf("failed tick was not rolled back: time=%d position=%d events=%d budget=%d", services.TimeMS, sound.SamplePosition(), services.Events.Len(), session.Budget)
	}
	fail = false
	if err := session.Tick(1); err != nil {
		t.Fatal(err)
	}
	if services.TimeMS != 1 || sound.SamplePosition() != 48 || services.Events.Len() != 0 {
		t.Fatalf("retry state: time=%d position=%d events=%d", services.TimeMS, sound.SamplePosition(), services.Events.Len())
	}
}

func TestTickRollbackRestoresDirectMixerSource(t *testing.T) {
	services := DefaultServices()
	session := NewSession(services, 100)
	sound := NewWaveSoundBuffer()
	if err := sound.OpenAudio(assets.Audio{Channels: 1, SampleRate: OutputRate, Samples: []int16{1234}}, assets.LoopInfo{}); err != nil {
		t.Fatal(err)
	}
	if err := sound.Play(); err != nil {
		t.Fatal(err)
	}
	sound.DrainEvents()
	services.AudioMixer.Add(sound)
	object := session.VM.Objects.New(tjs.CategoryDictionary)
	fail := true
	callbackErr := errors.New("direct mixer callback failed")
	if err := session.RegisterNative("testDirectMixerRollback", func(_ tjs.Value, _ []tjs.Value) (tjs.Value, error) {
		if fail {
			return tjs.VoidValue, callbackErr
		}
		return tjs.VoidValue, nil
	}); err != nil {
		t.Fatal(err)
	}
	callback := session.VM.Globals["testDirectMixerRollback"]
	if err := object.Set("onTimer", callback); err != nil {
		t.Fatal(err)
	}
	if err := services.Events.Post(uint64(object.ID), EventTimer, PriorityNormal); err != nil {
		t.Fatal(err)
	}
	if err := session.Tick(1); !errors.Is(err, callbackErr) {
		t.Fatalf("first direct mixer tick error=%v, want callback error", err)
	}
	if services.TimeMS != 0 || sound.SamplePosition() != 0 || sound.Status() != SoundPlay || len(sound.Events()) != 0 {
		t.Fatalf("direct mixer source was not restored: time=%d position=%d status=%q events=%v", services.TimeMS, sound.SamplePosition(), sound.Status(), sound.Events())
	}
	fail = false
	if err := session.Tick(1); err != nil {
		t.Fatal(err)
	}
	if sound.SamplePosition() != 1 || sound.Status() != SoundStop {
		t.Fatalf("direct mixer retry state: position=%d status=%q", sound.SamplePosition(), sound.Status())
	}
}

func TestTickRollbackRestoresEOFStatusForRepeatableRetry(t *testing.T) {
	services := DefaultServices()
	session := NewSession(services, 100)
	sound := NewWaveSoundBuffer()
	if err := sound.OpenAudio(assets.Audio{Channels: 1, SampleRate: OutputRate, Samples: []int16{1234}}, assets.LoopInfo{}); err != nil {
		t.Fatal(err)
	}
	if err := sound.Play(); err != nil {
		t.Fatal(err)
	}
	sound.DrainEvents()
	object := session.VM.Objects.New(tjs.CategoryDictionary)
	if err := services.RegisterSound(object.ID, sound); err != nil {
		t.Fatal(err)
	}
	fail := true
	callbackErr := errors.New("EOF retry callback failed")
	if err := session.RegisterNative("testEOFRetry", func(_ tjs.Value, _ []tjs.Value) (tjs.Value, error) {
		if fail {
			return tjs.VoidValue, callbackErr
		}
		return tjs.VoidValue, nil
	}); err != nil {
		t.Fatal(err)
	}
	callback := session.VM.Globals["testEOFRetry"]
	if err := object.Set("onTimer", callback); err != nil {
		t.Fatal(err)
	}
	if err := services.Events.Post(uint64(object.ID), EventTimer, PriorityNormal); err != nil {
		t.Fatal(err)
	}
	if err := session.Tick(1); !errors.Is(err, callbackErr) {
		t.Fatalf("first EOF tick error=%v, want callback error", err)
	}
	if sound.Status() != SoundPlay || sound.SamplePosition() != 0 {
		t.Fatalf("failed EOF tick state status=%q position=%d", sound.Status(), sound.SamplePosition())
	}
	fail = false
	if err := session.Tick(1); err != nil {
		t.Fatal(err)
	}
	if sound.Status() != SoundStop || sound.SamplePosition() != 1 {
		t.Fatalf("retry EOF state status=%q position=%d", sound.Status(), sound.SamplePosition())
	}
}

func TestDispatchEventsSeparatesOrdinaryAndSoundTriggers(t *testing.T) {
	services := DefaultServices()
	session := NewSession(services, 100)
	sound := NewWaveSoundBuffer()
	obj := session.VM.Objects.New(tjs.CategoryDictionary)
	if err := services.RegisterSound(obj.ID, sound); err != nil {
		t.Fatal(err)
	}
	order := make([]string, 0, 2)
	if err := session.RegisterNative("testOnFire", func(_ tjs.Value, _ []tjs.Value) (tjs.Value, error) {
		order = append(order, "fire")
		return tjs.VoidValue, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := session.RegisterNative("testOnLabel", func(_ tjs.Value, _ []tjs.Value) (tjs.Value, error) {
		order = append(order, "sound")
		return tjs.VoidValue, nil
	}); err != nil {
		t.Fatal(err)
	}
	fireCallback, ok := session.VM.Globals["testOnFire"]
	if !ok {
		t.Fatal("registered fire callback is missing")
	}
	soundCallback, ok := session.VM.Globals["testOnLabel"]
	if !ok {
		t.Fatal("registered sound callback is missing")
	}
	if err := obj.Set("onFire", fireCallback); err != nil {
		t.Fatal(err)
	}
	if err := obj.Set("onLabel", soundCallback); err != nil {
		t.Fatal(err)
	}
	services.SoundEvents[obj.ID] = []SoundEvent{{Generation: sound.Generation(), Type: SoundEventLabel, Label: "label"}}
	if err := services.Events.Post(uint64(obj.ID), EventTrigger, PriorityNormal); err != nil {
		t.Fatal(err)
	}
	if err := services.Events.PostSoundBatch(uint64(obj.ID), EventTrigger, PriorityNormal, 1); err != nil {
		t.Fatal(err)
	}
	if err := session.DispatchEvents(); err != nil {
		t.Fatal(err)
	}
	if len(order) != 2 || order[0] != "fire" || order[1] != "sound" {
		t.Fatalf("mixed trigger order=%v", order)
	}
}

func TestCaptureFrameStatsReportCompositionCachingAndContentChanges(t *testing.T) {
	t.Setenv("KRKRGO_RENDER_STATS", "1")
	t.Setenv("KRKRGO_RENDER_CONTENT_HASH", "1")
	var output bytes.Buffer
	previousOutput, previousFlags, previousPrefix := log.Writer(), log.Flags(), log.Prefix()
	log.SetOutput(&output)
	log.SetFlags(0)
	log.SetPrefix("")
	t.Cleanup(func() {
		log.SetOutput(previousOutput)
		log.SetFlags(previousFlags)
		log.SetPrefix(previousPrefix)
	})

	services := DefaultServices()
	root := NewRootLayer(1, 1, 1)
	if err := root.SetRGBA(1, 1, []byte{255, 0, 0, 255}); err != nil {
		t.Fatal(err)
	}
	services.SetRoot(root)
	if _, err := services.CaptureFrame(); err != nil {
		t.Fatal(err)
	}
	if _, err := services.CaptureFrame(); err != nil {
		t.Fatal(err)
	}
	root.RGBA[0] = 0
	root.RGBA[1] = 255
	if _, err := services.CaptureFrame(); err != nil {
		t.Fatal(err)
	}
	if _, err := services.CaptureFrame(); err != nil {
		t.Fatal(err)
	}
	video := NewVideoOverlay()
	if err := video.SetSourceMetadata(VideoSource{Name: "test.mpg", Format: "MPEG", Width: 1, Height: 1, FPS: 1, Frames: 1, DurationMS: 1000}); err != nil {
		t.Fatal(err)
	}
	if err := video.Play(); err != nil {
		t.Fatal(err)
	}
	if err := services.RegisterVideo(tjs.ObjectID(1), video); err != nil {
		t.Fatal(err)
	}
	if _, err := services.CaptureFrame(); err != nil {
		t.Fatal(err)
	}
	root.inTransition = true
	root.transitionPixels = []byte{0, 0, 255, 255}
	services.renderDiag.started = time.Now().Add(-2 * time.Second)
	if _, err := services.CaptureFrame(); err != nil {
		t.Fatal(err)
	}

	for _, expected := range []string{
		"captures=6", "compose=4", "cache_hit=2", "cache_miss=4", "cache_invalidations=2",
		"render_state_changes=1", "dynamic=2", "transitions=1", "videos=2",
		"validate_avg=", "transition_walk_avg=", "compose_avg=", "cache_hash_avg=", "cache_lookup_avg=",
		"frame_copy_avg=", "record_avg=", "content_hash=enabled", "content_changes=2",
	} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("runtime frame stats lack %q: %s", expected, output.String())
		}
	}
}

func TestCaptureFrameStatsDisableContentHashByDefault(t *testing.T) {
	t.Setenv("KRKRGO_RENDER_STATS", "1")
	t.Setenv("KRKRGO_RENDER_CONTENT_HASH", "")
	var output bytes.Buffer
	previousOutput, previousFlags, previousPrefix := log.Writer(), log.Flags(), log.Prefix()
	log.SetOutput(&output)
	log.SetFlags(0)
	log.SetPrefix("")
	t.Cleanup(func() {
		log.SetOutput(previousOutput)
		log.SetFlags(previousFlags)
		log.SetPrefix(previousPrefix)
	})

	services := DefaultServices()
	services.renderDiag.started = time.Now().Add(-2 * time.Second)
	frame := FrameSnapshot{RGBA: make([]byte, 1280*720*4)}
	services.recordFrameCapture(frame, false, true, false, 1, true, 0, 0, frameCaptureTimings{})
	if services.renderDiag.contentValid {
		t.Fatal("content hash was computed while KRKRGO_RENDER_CONTENT_HASH is disabled")
	}
	if got := output.String(); !strings.Contains(got, "content_hash=disabled") || !strings.Contains(got, "content_changes=disabled") || !strings.Contains(got, "cache_hit=1") {
		t.Fatalf("runtime stats lack useful disabled-hash counters: %s", got)
	}
}
