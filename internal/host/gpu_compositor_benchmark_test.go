package host

import (
	"fmt"
	"testing"

	runtime "github.com/eslzzyl/krkrgo/internal/runtime"
)

func BenchmarkGPUComposeNativeLayers(b *testing.B) {
	const width, height, layers = 1280, 720, 8
	for _, layerType := range []struct {
		name string
		typ  runtime.LayerType
	}{
		{name: "opaque", typ: runtime.LayerOpaque},
		{name: "alpha", typ: runtime.LayerAlpha},
	} {
		b.Run(layerType.name+"/stable", func(b *testing.B) {
			benchmarkGPUCompose(b, benchmarkGPUPlan(width, height, layers, layerType.typ), false)
		})
		b.Run(layerType.name+"/dynamic", func(b *testing.B) {
			benchmarkGPUCompose(b, benchmarkGPUPlan(width, height, layers, layerType.typ), true)
		})
	}
}

func BenchmarkGPUComposeMotionSourceSharing(b *testing.B) {
	for _, shared := range []bool{false, true} {
		name := "per-layer"
		if shared {
			name = "shared"
		}
		b.Run(name, func(b *testing.B) {
			plan := benchmarkMotionSourcePlan(shared)
			compositor, err := NewGPUCompositor()
			if err != nil {
				b.Fatal(err)
			}
			defer compositor.Deallocate()
			b.ResetTimer()
			for index := range b.N {
				key := fmt.Sprintf("motion/churn/%d", index)
				for nodeIndex := 1; nodeIndex < len(plan.Nodes); nodeIndex++ {
					plan.Nodes[nodeIndex].Motion.Draws[0].SourceKey = key
				}
				if _, err := compositor.Compose(plan); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkGPUComposeMotionSharedStable(b *testing.B) {
	plan := benchmarkMotionSourcePlan(true)
	compositor, err := NewGPUCompositor()
	if err != nil {
		b.Fatal(err)
	}
	defer compositor.Deallocate()
	if _, err := compositor.Compose(plan); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for range b.N {
		if _, err := compositor.Compose(plan); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkGPUComposeMotionFlatItems(b *testing.B) {
	plan := benchmarkMotionFlatItemsPlan()
	compositor, err := NewGPUCompositor()
	if err != nil {
		b.Fatal(err)
	}
	defer compositor.Deallocate()
	if _, err := compositor.Compose(plan); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for range b.N {
		if _, err := compositor.Compose(plan); err != nil {
			b.Fatal(err)
		}
	}
}

func benchmarkMotionFlatItemsPlan() runtime.RenderPlan {
	const width, height, items, sourceSize = 128, 128, 8, 16
	pixels := make([]byte, sourceSize*sourceSize*4)
	for index := 0; index < len(pixels); index += 4 {
		pixels[index+0], pixels[index+1], pixels[index+2], pixels[index+3] = 255, 64, 32, 255
	}
	draws := make([]runtime.RenderMotionDraw, items)
	motionItems := make([]runtime.RenderMotionItem, items)
	for index := range draws {
		draws[index] = runtime.RenderMotionDraw{
			SourceKey: fmt.Sprintf("motion/flat/%d", index),
			RGBA:      pixels,
			Width:     sourceSize,
			Height:    sourceSize,
			Opaque:    true,
			A:         1,
			D:         1,
			TX:        float64(index * sourceSize),
			Opacity:   255,
		}
		motionItems[index] = runtime.RenderMotionItem{SortKey: float64(index), Draw: draws[index]}
	}
	return runtime.RenderPlan{
		Width:        width,
		Height:       height,
		ChildIndices: []int{1},
		Nodes: []runtime.RenderNode{
			{ID: 1, Type: runtime.LayerBinder, Visible: true, Opacity: 255, ChildCount: 1},
			{ID: 2, Bounds: runtime.Rect{Width: width, Height: height}, Type: runtime.LayerAlpha, Visible: true, Opacity: 255, ImageWidth: width, ImageHeight: height,
				Motion: &runtime.RenderMotion{AtlasKey: "benchmark/flat", Draws: draws, Items: motionItems}},
		},
	}
}

func benchmarkMotionSourcePlan(shared bool) runtime.RenderPlan {
	const width, height, layers, sourceSize = 64, 64, 8, 64
	pixels := make([]byte, sourceSize*sourceSize*4)
	for index := 0; index < len(pixels); index += 4 {
		pixels[index+0], pixels[index+1], pixels[index+2], pixels[index+3] = 255, 64, 32, 255
	}
	plan := runtime.RenderPlan{
		Width:        width,
		Height:       height,
		ChildIndices: make([]int, layers),
		Nodes:        make([]runtime.RenderNode, layers+1),
	}
	plan.Nodes[0] = runtime.RenderNode{ID: 1, Type: runtime.LayerBinder, Visible: true, Opacity: 255, ChildCount: layers}
	for index := 0; index < layers; index++ {
		motion := &runtime.RenderMotion{Draws: []runtime.RenderMotionDraw{{SourceKey: "motion/initial", RGBA: pixels, Width: sourceSize, Height: sourceSize, Opaque: true, A: 1, D: 1, Opacity: 255}}}
		if shared {
			motion.AtlasKey = "benchmark/shared"
		}
		plan.ChildIndices[index] = index + 1
		plan.Nodes[index+1] = runtime.RenderNode{
			ID: runtime.LayerID(index + 2), Bounds: runtime.Rect{Width: width, Height: height}, Type: runtime.LayerAlpha,
			Visible: true, Opacity: 255, ImageWidth: width, ImageHeight: height, Motion: motion,
		}
	}
	return plan
}

func benchmarkGPUCompose(b *testing.B, plan runtime.RenderPlan, dynamic bool) {
	plan.StateHashStable = !dynamic
	compositor, err := NewGPUCompositor()
	if err != nil {
		b.Fatal(err)
	}
	defer compositor.Deallocate()
	if dynamic {
		if _, err := compositor.Compose(plan); err != nil {
			b.Fatal(err)
		}
		b.ResetTimer()
		for i := range b.N {
			plan.StateHash = uint64(i + 2)
			if _, err := compositor.Compose(plan); err != nil {
				b.Fatal(err)
			}
		}
		return
	}
	if _, err := compositor.composeStable(plan); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for range b.N {
		if _, err := compositor.composeStable(plan); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEvictMotionAtlasEntries(b *testing.B) {
	benchmarkEvictMotionAtlasEntries(b, false)
}

func BenchmarkEvictMotionAtlasEntriesLegacy(b *testing.B) {
	benchmarkEvictMotionAtlasEntries(b, true)
}

func benchmarkEvictMotionAtlasEntries(b *testing.B, legacy bool) {
	const entries = 64
	atlas := &gpuMotionAtlas{entries: make(map[string]*gpuMotionAtlasEntry, entries)}
	draws := make([]runtime.RenderMotionDraw, entries)
	for index := range entries {
		key := "motion/source/" + string(rune('a'+index))
		atlas.entries[key] = &gpuMotionAtlasEntry{}
		draws[index] = runtime.RenderMotionDraw{SourceKey: key}
	}
	compositor := &GPUCompositor{
		motionAtlases: map[runtime.LayerID]*gpuMotionAtlas{1: atlas},
	}
	b.ResetTimer()
	for range b.N {
		if legacy {
			live := make(map[string]struct{}, len(draws))
			for _, draw := range draws {
				if !draw.Baked {
					live[motionSourceKey(draw)] = struct{}{}
				}
			}
			for key := range atlas.entries {
				if _, ok := live[key]; !ok {
					delete(atlas.entries, key)
				}
			}
			continue
		}
		compositor.evictMotionAtlasEntries(1, draws)
	}
}

func benchmarkGPUPlan(width, height, layers int, layerType runtime.LayerType) runtime.RenderPlan {
	plan := runtime.RenderPlan{
		Width:          int32(width),
		Height:         int32(height),
		Nodes:          make([]runtime.RenderNode, layers+1),
		StateHash:      1,
		StateHashValid: true,
		ChildIndices:   make([]int, layers),
	}
	plan.Nodes[0] = runtime.RenderNode{
		ID:         1,
		Bounds:     runtime.Rect{Width: int32(width), Height: int32(height)},
		Opacity:    255,
		Visible:    true,
		Type:       runtime.LayerBinder,
		ChildCount: layers,
	}
	for i := range layers {
		pixels := make([]byte, width*height*4)
		for p := 0; p < len(pixels); p += 4 {
			pixels[p] = byte(32 + i*7)
			pixels[p+1] = byte(64 + i*5)
			pixels[p+2] = byte(96 + i*3)
			pixels[p+3] = 255
			if layerType == runtime.LayerAlpha {
				pixels[p+3] = 192
			}
		}
		plan.ChildIndices[i] = i + 1
		plan.Nodes[i+1] = runtime.RenderNode{
			ID:          runtime.LayerID(i + 2),
			Bounds:      runtime.Rect{Width: int32(width), Height: int32(height)},
			Opacity:     255,
			Visible:     true,
			Type:        layerType,
			ImageWidth:  int32(width),
			ImageHeight: int32(height),
			RGBA:        pixels,
			ImageHash:   uint64(i + 2),
		}
	}
	return plan
}
