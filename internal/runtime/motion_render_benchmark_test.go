package runtime

import (
	"fmt"
	"testing"

	"github.com/eslzzyl/krkrgo/internal/assets"
)

var motionMatrixBenchmarkSink motionMatrix
var motionCurveBenchmarkSink float64
var motionControlPointBenchmarkSink [3]float64
var motionFrameContentBenchmarkSink map[string]assets.Value

func BenchmarkMotionMeshAdjustedMatrix(b *testing.B) {
	layer := map[string]assets.Value{}
	content := map[string]assets.Value{
		"angle": float64(13), "zx": float64(1.1), "zy": float64(0.9),
		"fx": float64(1), "fy": float64(0), "sx": float64(0.1), "sy": float64(-0.05),
	}
	local := motionTransformFromContent(content)
	b.Run("map-copy", func(b *testing.B) {
		for range b.N {
			adjusted := make(map[string]assets.Value, len(content)+3)
			for key, value := range content {
				adjusted[key] = value
			}
			adjusted["angle"] = psbMotionFloat(content, "angle", 0) + 7
			adjusted["zx"] = psbMotionFloat(content, "zx", 1) * 1.05
			adjusted["zy"] = psbMotionFloat(content, "zy", 1) * 0.95
			motionMatrixBenchmarkSink = psbMotionLocalMatrix(layer, adjusted)
		}
	})
	b.Run("parsed-state", func(b *testing.B) {
		for range b.N {
			adjusted := local
			adjusted.angle += 7
			adjusted.scaleX *= 1.05
			adjusted.scaleY *= 0.95
			motionMatrixBenchmarkSink = psbMotionLocalMatrixState(layer, adjusted)
		}
	})
}

func BenchmarkMotionTransformOrder(b *testing.B) {
	layer := map[string]assets.Value{"transformOrder": []assets.Value{int64(2), int64(1), int64(0), int64(3)}}
	content := map[string]assets.Value{
		"fx": float64(1), "fy": float64(0), "angle": float64(13),
		"zx": float64(1.1), "zy": float64(0.9), "sx": float64(0.1), "sy": float64(-0.05),
	}
	b.Run("direct-order", func(b *testing.B) {
		for range b.N {
			_, motionMatrixBenchmarkSink = motionTransformAndMatrix(layer, content)
		}
	})
	b.Run("list-order", func(b *testing.B) {
		for range b.N {
			motionMatrixBenchmarkSink = motionTransformListMatrix(layer, content)
		}
	})
}

func motionTransformListMatrix(layer, content map[string]assets.Value) motionMatrix {
	state := motionTransformFromContent(content)
	order := motionDefaultTransformOrder
	if values := psbMotionList(layer["transformOrder"]); len(values) > 0 {
		var custom [4]int
		orderLen := len(values)
		if orderLen > len(custom) {
			orderLen = len(custom)
		}
		copy(custom[:], motionDefaultTransformOrder)
		for index := 0; index < orderLen; index++ {
			custom[index] = int(values[index])
		}
		order = custom[:]
	}
	return motionMatrixFromTransformState(order, state)
}

func BenchmarkMotionCurveRatio(b *testing.B) {
	curve := map[string]assets.Value{
		"x": []assets.Value{float64(0), float64(0.2), float64(0.7), float64(1)},
		"y": []assets.Value{float64(0), float64(0.1), float64(0.9), float64(1)},
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		motionCurveBenchmarkSink = psbMotionCurveRatio(curve, 0.43)
	}
}

func BenchmarkMotionControlPoint(b *testing.B) {
	curve := map[string]assets.Value{
		"x": []assets.Value{float64(0), float64(0.2), float64(0.7), float64(1)},
		"y": []assets.Value{float64(0), float64(0.1), float64(0.9), float64(1)},
		"t": []assets.Value{float64(0), float64(1)},
		"s": []assets.Value{map[string]assets.Value{
			"x": []assets.Value{float64(0), float64(1)},
			"y": []assets.Value{float64(0), float64(1)},
			"p": []assets.Value{float64(0), float64(0)},
		}},
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		cosine, sine, ok := psbMotionControlPointAt(curve, 0.43)
		if ok {
			motionControlPointBenchmarkSink = [3]float64{cosine, sine, 1}
		}
	}
}

func BenchmarkMotionFrameContentMasked(b *testing.B) {
	content := map[string]assets.Value{
		"mask": int64(0x7FFF), "coord": []assets.Value{float64(1), float64(2)},
		"angle": float64(15), "zx": float64(1), "zy": float64(1),
		"sx": float64(0), "sy": float64(0), "opa": float64(255),
		"ccc": map[string]assets.Value{}, "src": "src/chara/body",
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		motionFrameContentBenchmarkSink = psbMotionFrameContent(content, 0)
	}
}

func BenchmarkSortRenderMotionItems(b *testing.B) {
	items := make([]RenderMotionItem, 64)
	for index := range items {
		items[index].SortKey = float64((index * 37) % len(items))
	}
	working := make([]RenderMotionItem, len(items))
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		copy(working, items)
		sortRenderMotionItems(working)
	}
}

func BenchmarkMotionSeparateLayerKey(b *testing.B) {
	var key string
	b.ResetTimer()
	for index := range b.N {
		key = motionSeparateLayerKey("player:17/tree:4/chara/body", 23, index)
	}
	b.StopTimer()
	if key == "" {
		b.Fatal("empty key")
	}
}

func BenchmarkMotionSeparateLayerKeyFmt(b *testing.B) {
	var key string
	b.ResetTimer()
	for index := range b.N {
		key = fmt.Sprintf("%s\x00%d\x00%d", "player:17/tree:4/chara/body", 23, index)
	}
	b.StopTimer()
	if key == "" {
		b.Fatal("empty key")
	}
}

func BenchmarkBuildGPUMotionVariables(b *testing.B) {
	resource := emotePlayerTestResource("benchmark-motion.psb")
	matrix := identityMotionDrawMatrix()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		if _, err := buildGPUMotionVariables(resource, "chara", "idle", float64(index%2), 0, matrix, 1, nil, false, 0, 0, nil); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkBuildGPUMotionVariablesReuse(b *testing.B) {
	resource := emotePlayerTestResource("benchmark-motion-reuse.psb")
	matrix := identityMotionDrawMatrix()
	motion, err := buildGPUMotionVariables(resource, "chara", "idle", 0, 0, matrix, 1, nil, false, 0, 0, nil)
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		motion, err = buildGPUMotionVariablesZFactorInheritReuse(motion, resource, "chara", "idle", float64(index%2), 0, matrix, 1, nil, false, 0, 0, 1, nil, false)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkBuildGPUMotionVariablesSwapReuse(b *testing.B) {
	resource := emotePlayerTestResource("benchmark-motion-swap.psb")
	matrix := identityMotionDrawMatrix()
	current, err := buildGPUMotionVariables(resource, "chara", "idle", 0, 0, matrix, 1, nil, false, 0, 0, nil)
	if err != nil {
		b.Fatal(err)
	}
	scratch, err := buildGPUMotionVariables(resource, "chara", "idle", 1, 0, matrix, 1, nil, false, 0, 0, nil)
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		motion, err := buildGPUMotionVariablesZFactorInheritReuse(scratch, resource, "chara", "idle", float64(index%2), 0, matrix, 1, nil, false, 0, 0, 1, nil, false)
		if err != nil {
			b.Fatal(err)
		}
		scratch, current = current, motion
	}
	_ = current
}

func BenchmarkSubtractiveMotionRGBA(b *testing.B) {
	const width, height = 512, 512
	pixels := make([]byte, width*height*4)
	for index := range pixels {
		pixels[index] = byte(index*17 + 29)
	}
	b.SetBytes(int64(len(pixels)))
	b.ResetTimer()
	for range b.N {
		_ = subtractiveMotionRGBA(pixels)
	}
}

func BenchmarkSubtractiveMotionRGBACached(b *testing.B) {
	const width, height = 512, 512
	pixels := make([]byte, width*height*4)
	for index := range pixels {
		pixels[index] = byte(index*17 + 29)
	}
	resource := &motionResource{}
	_ = motionSubtractiveRGBA(resource, "source", pixels)
	b.SetBytes(int64(len(pixels)))
	b.ResetTimer()
	for range b.N {
		_ = motionSubtractiveRGBA(resource, "source", pixels)
	}
}

func BenchmarkScaleRGBA2x(b *testing.B) {
	const width, height = 512, 512
	pixels := make([]byte, width*height*4)
	for index := range pixels {
		pixels[index] = byte(index*13 + 7)
	}
	b.SetBytes(int64(len(pixels)))
	b.ResetTimer()
	for range b.N {
		_ = scaleRGBA(pixels, width, height, 2)
	}
}

func BenchmarkScaleRGBA2xCached(b *testing.B) {
	const width, height = 512, 512
	pixels := make([]byte, width*height*4)
	for index := range pixels {
		pixels[index] = byte(index*13 + 7)
	}
	resource := &motionResource{}
	image := &assets.Image{Width: width, Height: height, RGBA: pixels}
	_ = motionScaledImage(resource, "source/scale:2", image, 2)
	b.SetBytes(int64(len(pixels)))
	b.ResetTimer()
	for range b.N {
		_ = motionScaledImage(resource, "source/scale:2", image, 2)
	}
}
