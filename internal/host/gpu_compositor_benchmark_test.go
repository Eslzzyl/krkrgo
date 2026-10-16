package host

import (
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
		b.Run(layerType.name, func(b *testing.B) {
			plan := benchmarkGPUPlan(width, height, layers, layerType.typ)
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
		})
	}
}

func benchmarkGPUPlan(width, height, layers int, layerType runtime.LayerType) runtime.RenderPlan {
	plan := runtime.RenderPlan{
		Width:        int32(width),
		Height:       int32(height),
		Nodes:        make([]runtime.RenderNode, layers+1),
		ChildIndices: make([]int, layers),
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
