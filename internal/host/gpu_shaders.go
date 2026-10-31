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
var SourceOffset vec2
var SourceMin vec2
var SourceMax vec2
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
	if mode == 8 || mode == 21 || mode == 22 { return dodge(base, source) }
	if mode == 9 || mode == 25 { return minf(base, source) }
	if mode == 10 || mode == 24 { return maxf(base, source) }
	if mode == 18 { return overlay(base, source) }
	if mode == 19 { return overlay(source, base) }
	if mode == 20 { return base*(1-source)+overlay(base, source)*source }
	if mode == 23 { return burn(base, source) }
	if mode == 26 || mode == 27 { return absf(base-source) }
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
	minPos := SourceMin
	maxPos := SourceMax
	tl := imageSrc1AtFromSrc0Pos(clamp(base, minPos, maxPos))
	tr := imageSrc1AtFromSrc0Pos(clamp(base+vec2(1, 0), minPos, maxPos))
	bl := imageSrc1AtFromSrc0Pos(clamp(base+vec2(0, 1), minPos, maxPos))
	br := imageSrc1AtFromSrc0Pos(clamp(base+vec2(1, 1), minPos, maxPos))
	return mix(mix(tl, tr, f.x), mix(bl, br, f.x), f.y)
}

func Fragment(dstPos vec4, src0Pos vec2, color vec4) vec4 {
	d := imageSrc0At(src0Pos)
	s := sourceSample(SourceOffset+(src0Pos-SourceOrigin)*SourceScale)
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

const gpuMotionMaskShaderSource = `//kage:unit pixels
package main

var Operation float
var Threshold float

func Fragment(dstPos vec4, src0Pos vec2, color vec4) vec4 {
	group := imageSrc0At(src0Pos)
	mask := imageSrc1AtFromSrc0Pos(src0Pos)
	alpha := group.a
	if Threshold > 0 {
		if Operation == 1 {
			if mask.a < 64.0/255.0 { alpha = 0 }
		} else if Operation == 2 {
			if mask.a >= 64.0/255.0 { alpha = 0 }
		} else if Operation == 5 || Operation == 6 {
			if mask.a >= 64.0/255.0 { alpha = 1 }
		}
	} else {
		if Operation == 1 {
			alpha *= mask.a
		} else if Operation == 2 {
			alpha *= 1-mask.a
		} else if Operation == 5 || Operation == 6 {
			alpha = mask.a+(1-mask.a)*alpha
		}
	}
	if group.a > 0 {
		group.rgb *= alpha / group.a
	} else {
		group.rgb = vec3(0)
	}
	group.a = alpha
	return group
}
`

const gpuImageOpShaderSource = `//kage:unit pixels
package main

var Mode float
var TargetType float
var Opacity float
var HoldAlpha float
var SourceOrigin vec2
var SourceScale vec2
var SourceOffset vec2
var SourceMin vec2
var SourceMax vec2
var SourceLinear float
var Affine float
var AffineInverse vec4
var AffineTranslation vec2
var SourceRectOrigin vec2
var SourceRectSize vec2

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
func blendChannel(base, source, mode float) float {
	if mode == 1 { return source }
	if mode == 3 || mode == 14 { return minf(1, base+source) }
	if mode == 4 || mode == 15 { return maxf(0, base-source) }
	if mode == 5 || mode == 16 { return base*source }
	if mode == 11 || mode == 17 { return 1-(1-base)*(1-source) }
	if mode == 8 || mode == 21 || mode == 22 { return dodge(base, source) }
	if mode == 9 || mode == 25 { return minf(base, source) }
	if mode == 10 || mode == 24 { return maxf(base, source) }
	if mode == 18 { return overlay(base, source) }
	if mode == 19 { return overlay(source, base) }
	if mode == 20 { return base*(1-source)+overlay(base, source)*source }
	if mode == 23 { return burn(base, source) }
	if mode == 26 || mode == 27 { return absf(base-source) }
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
	tl := imageSrc1AtFromSrc0Pos(clamp(base, SourceMin, SourceMax))
	tr := imageSrc1AtFromSrc0Pos(clamp(base+vec2(1, 0), SourceMin, SourceMax))
	bl := imageSrc1AtFromSrc0Pos(clamp(base+vec2(0, 1), SourceMin, SourceMax))
	br := imageSrc1AtFromSrc0Pos(clamp(base+vec2(1, 1), SourceMin, SourceMax))
	return mix(mix(tl, tr, f.x), mix(bl, br, f.x), f.y)
}
func Fragment(dstPos vec4, src0Pos vec2, color vec4) vec4 {
	d := imageSrc0At(src0Pos)
	pos := SourceOffset+(src0Pos-SourceOrigin)*SourceScale
	if Affine > 0 {
		delta := src0Pos-AffineTranslation
		u := AffineInverse.x*delta.x + AffineInverse.y*delta.y
		v := AffineInverse.z*delta.x + AffineInverse.w*delta.y
		if u < -0.5 || u >= SourceRectSize.x-0.5 || v < -0.5 || v >= SourceRectSize.y-0.5 {
			return d
		}
		pos = SourceRectOrigin + vec2(u, v)
	}
	s := sourceSample(pos)
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
	if Mode == 12 { outA = minf(1, da+sa*op) }
	if HoldAlpha > 0 { outA = da }
	return vec4(outRGB*outA, outA)
}
`

const gpuCopyOpShaderSource = `//kage:unit pixels
package main

var SourceOrigin vec2
var SourceScale vec2
var SourceOffset vec2
var SourceLinear float
var SourceMin vec2
var SourceMax vec2
var Face float
var HoldAlpha float

func sourceSample(pos vec2) vec4 {
	if SourceLinear <= 0 { return imageSrc1AtFromSrc0Pos(pos) }
	p := pos - vec2(0.5)
	f := fract(p)
	base := floor(p) + vec2(0.5)
	tl := imageSrc1AtFromSrc0Pos(clamp(base, SourceMin, SourceMax))
	tr := imageSrc1AtFromSrc0Pos(clamp(base+vec2(1, 0), SourceMin, SourceMax))
	bl := imageSrc1AtFromSrc0Pos(clamp(base+vec2(0, 1), SourceMin, SourceMax))
	br := imageSrc1AtFromSrc0Pos(clamp(base+vec2(1, 1), SourceMin, SourceMax))
	return mix(mix(tl, tr, f.x), mix(bl, br, f.x), f.y)
}
func Fragment(dstPos vec4, src0Pos vec2, color vec4) vec4 {
	d := imageSrc0At(src0Pos)
	s := sourceSample(SourceOffset+(src0Pos-SourceOrigin)*SourceScale)
	if Face == 1 && HoldAlpha > 0 {
		rgb := vec3(0)
		if s.a > 0 { rgb = s.rgb/s.a }
		return vec4(rgb*d.a, d.a)
	}
	return s
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

func compileMotionMaskShader() (*ebiten.Shader, error) {
	shader, err := ebiten.NewShader([]byte(gpuMotionMaskShaderSource))
	if err != nil {
		return nil, fmt.Errorf("compile GPU Motion mask shader: %w", err)
	}
	return shader, nil
}

func compileGPUImageOpShaders() (*ebiten.Shader, *ebiten.Shader, error) {
	imageOp, err := ebiten.NewShader([]byte(gpuImageOpShaderSource))
	if err != nil {
		return nil, nil, fmt.Errorf("compile GPU image operation shader: %w", err)
	}
	copyOp, err := ebiten.NewShader([]byte(gpuCopyOpShaderSource))
	if err != nil {
		imageOp.Deallocate()
		return nil, nil, fmt.Errorf("compile GPU copy operation shader: %w", err)
	}
	return imageOp, copyOp, nil
}
