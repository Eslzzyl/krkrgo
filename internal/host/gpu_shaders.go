package host

import (
	"fmt"

	runtime "github.com/eslzzyl/krkrgo/internal/runtime"
	"github.com/hajimehoshi/ebiten/v2"
)

// gpuMethodKey is deliberately richer than a LayerType: target alpha and the
// hold-alpha flag change the output contract even when the blend mode matches.
type gpuMethodKey struct {
	layerType  runtime.LayerType
	targetType runtime.LayerType
	holdAlpha  bool
}

const gpuBlendShaderSource = `//kage:unit pixels
package main

var Mode float
var TargetType float
var Opacity float
var HoldAlpha float
var SourceOrigin vec2
var SourceScale vec2
var SourceSize vec2
var SourceLinear float
func clamp01(v float) float {
	if v < 0 { return 0 }
	if v > 1 { return 1 }
	return v
}
func minf(a, b float) float { if a < b { return a }; return b }
func maxf(a, b float) float { if a > b { return a }; return b }
func dodge(a, b float) float {
	if b >= 1 { return 1 }
	return minf(1, a/(1-b))
}
func absf(v float) float { if v < 0 { return -v }; return v }
func burn(a, b float) float {
	if b <= 0 { return 0 }
	return maxf(0, 1-(1-a)/b)
}
func overlay(a, b float) float {
	if a < .5 { return 2*a*b }
	return 1-2*(1-a)*(1-b)
}
func blendChannel(base, source float, mode float) float {
	if mode == 1 { return source }
	if mode == 3 || mode == 14 { return minf(1, base+source) }
	if mode == 4 || mode == 15 { return maxf(0, base-source) }
	if mode == 5 || mode == 16 { return base*source }
	if mode == 11 || mode == 17 { return 1-(1-base)*(1-source) }
	if mode == 8 || mode == 18 || mode == 19 { return dodge(base, source) }
	if mode == 9 || mode == 27 { return minf(base, source) }
	if mode == 10 || mode == 26 { return maxf(base, source) }
	if mode == 20 { return overlay(base, source) }
	if mode == 21 { return overlay(source, base) }
	if mode == 22 { return base*(1-source)+overlay(base, source)*source }
	if mode == 23 { return burn(base, source) }
	if mode == 24 || mode == 25 { return absf(base-source) }
	if mode == 28 { return base+source-2*base*source }
	return source
}

func sourceSample(pos vec2) vec4 {
	if SourceLinear <= 0 {
		return imageSrc1AtFromSrc0Pos(pos)
	}
	p := pos - vec2(0.5)
	f := fract(p)
	base := floor(p) + vec2(0.5)
	minPos := vec2(0.5)
	maxPos := SourceSize - vec2(0.5)
	tl := imageSrc1AtFromSrc0Pos(clamp(base, minPos, maxPos))
	tr := imageSrc1AtFromSrc0Pos(clamp(base+vec2(1, 0), minPos, maxPos))
	bl := imageSrc1AtFromSrc0Pos(clamp(base+vec2(0, 1), minPos, maxPos))
	br := imageSrc1AtFromSrc0Pos(clamp(base+vec2(1, 1), minPos, maxPos))
	return mix(mix(tl, tr, f.x), mix(bl, br, f.x), f.y)
}

func Fragment(dstPos vec4, src0Pos vec2, color vec4) vec4 {
	d := imageSrc0At(src0Pos)
	s := sourceSample((src0Pos-SourceOrigin)*SourceScale)
	// Ebitengine textures are premultiplied. Blend in straight color space and
	// premultiply exactly once on return.
	da := clamp01(d.a)
	sa := clamp01(s.a)
	drgb := d.rgb
	srgb := s.rgb
	if da > 0 { drgb /= da }
	if sa > 0 { srgb /= sa }
	op := clamp01(Opacity)
	if Mode == 1 {
		outA := op + da*(1-op)
		outRGB := drgb*(1-op) + srgb*op
		return vec4(outRGB*outA, outA)
	}
	if Mode == 2 {
		saop := sa*op
		if saop <= 0 { return d }
		if TargetType == 1 {
			outA := da*(1-saop)+sa*saop
			return vec4((drgb*(1-saop)+srgb*saop)*outA, outA)
		}
		outA := saop+da*(1-saop)
		if outA <= 0 { return vec4(0) }
		outRGB := (srgb*saop+drgb*da*(1-saop))/outA
		return vec4(outRGB*outA, outA)
	}
	if Mode == 12 && TargetType != 12 { return d }
	factor := op
	if Mode >= 13 { factor = sa*op }
	if factor <= 0 { return d }
	blend := vec3(blendChannel(drgb.r, srgb.r, Mode), blendChannel(drgb.g, srgb.g, Mode), blendChannel(drgb.b, srgb.b, Mode))
	outRGB := drgb*(1-factor)+blend*factor
	outA := da
	if TargetType != 2 && TargetType != 12 && !(TargetType >= 13 && TargetType <= 28) {
		outA = da*(1-factor)+sa*factor
	}
	if Mode == 12 {
		outA = minf(1, da+sa*op)
	}
	if HoldAlpha > 0 { outA = da }
	return vec4(outRGB*outA, outA)
}
`

const gpuCrossfadeShaderSource = `//kage:unit pixels
package main

var Phase float
var SourceOrigin vec2
var Opaque float
var ZeroDestinationOpaque float
var LayerOpacity float
func clamp01(v float) float {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
func unpremultiply(v vec4) vec4 {
	if v.a <= 0 {
		return vec4(0)
	}
	return vec4(v.rgb/v.a, v.a)
}
func applyOpacity(dst, src vec4, op float) vec4 {
	op = clamp01(op)
	sa := src.a * op
	oa := sa + dst.a*(1-sa)
	return vec4(src.rgb*op+dst.rgb*(1-sa), oa)
}
func Fragment(dstPos vec4, src0Pos vec2, color vec4) vec4 {
	d := imageSrc1AtFromSrc0Pos(src0Pos-SourceOrigin)
	s := imageSrc2AtFromSrc0Pos(src0Pos-SourceOrigin)
	p := clamp01(Phase)
	var out vec4
	if ZeroDestinationOpaque > 0 {
		q := 1 - p
		out = vec4(d.rgb*q*q, q)
	} else if Opaque > 0 {
		out = vec4(mix(d.rgb, s.rgb, p), 1)
	} else {
		ds := unpremultiply(d)
		ss := unpremultiply(s)
		mixed := mix(ds, ss, p)
		out = vec4(mixed.rgb*mixed.a, mixed.a)
	}
	return applyOpacity(imageSrc0At(src0Pos), out, LayerOpacity)
}

`
const gpuUniversalShaderSource = `//kage:unit pixels
package main

var Phase float
var Vague float
var SourceOrigin vec2
var RuleOrigin vec2
var LayerOpacity float
func clamp01(v float) float {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
func unpremultiply(v vec4) vec4 {
	if v.a <= 0 {
		return vec4(0)
	}
	return vec4(v.rgb/v.a, v.a)
}
func applyOpacity(dst, src vec4, op float) vec4 {
	op = clamp01(op)
	if op >= 1 {
		return src
	}
	sa := src.a * op
	oa := sa + dst.a*(1-sa)
	return vec4(src.rgb*op+dst.rgb*(1-sa), oa)
}
func Fragment(dstPos vec4, src0Pos vec2, color vec4) vec4 {
	d := unpremultiply(imageSrc1AtFromSrc0Pos(src0Pos-SourceOrigin))
	s := unpremultiply(imageSrc2AtFromSrc0Pos(src0Pos-SourceOrigin))
	r := imageSrc3AtFromSrc0Pos(src0Pos-RuleOrigin).r
	v := Vague
	if v < 0 {
		v = 0
	}
	a := 0.0
	if r < Phase-v {
		a = 1
	}
	if r >= Phase-v && r < Phase {
		if v == 0 {
			a = 1
		} else {
			a = 1 - (r-(Phase-v))/v
		}
	}
	mixed := mix(d, s, clamp01(a))
	out := vec4(mixed.rgb*mixed.a, mixed.a)
	return applyOpacity(imageSrc0At(src0Pos), out, LayerOpacity)
}
`

// gpuShaderSources is the one-source-per-method registry. All supported blend
// keys intentionally point at the same generic source: Mode is a uniform, so
// the driver compiles only one blend program.
var gpuShaderSources = map[gpuMethodKey][]byte{}

func initGPUShaderRegistry() {
	if len(gpuShaderSources) != 0 {
		return
	}
	for typ := runtime.LayerBinder; typ <= runtime.LayerPSExclusion; typ++ {
		if typ == runtime.LayerEffect || typ == runtime.LayerFilter {
			continue
		}
		for target := runtime.LayerBinder; target <= runtime.LayerPSExclusion; target++ {
			for _, hold := range []bool{false, true} {
				gpuShaderSources[gpuMethodKey{typ, target, hold}] = []byte(gpuBlendShaderSource)
			}
		}
	}
}

func compileGPUShaders() (map[gpuMethodKey]*ebiten.Shader, *ebiten.Shader, *ebiten.Shader, error) {
	initGPUShaderRegistry()
	compiled := make(map[string]*ebiten.Shader, 3)
	get := func(name string, source string) (*ebiten.Shader, error) {
		if shader := compiled[name]; shader != nil {
			return shader, nil
		}
		shader, err := ebiten.NewShader([]byte(source))
		if err != nil {
			return nil, fmt.Errorf("compile GPU %s shader: %w", name, err)
		}
		compiled[name] = shader
		return shader, nil
	}
	blend, err := get("blend", gpuBlendShaderSource)
	if err != nil {
		return nil, nil, nil, err
	}
	crossfade, err := get("crossfade", gpuCrossfadeShaderSource)
	if err != nil {
		blend.Deallocate()
		return nil, nil, nil, err
	}
	universal, err := get("universal", gpuUniversalShaderSource)
	if err != nil {
		blend.Deallocate()
		crossfade.Deallocate()
		return nil, nil, nil, err
	}
	methods := make(map[gpuMethodKey]*ebiten.Shader, len(gpuShaderSources))
	for key := range gpuShaderSources {
		methods[key] = blend
	}
	return methods, crossfade, universal, nil
}
