package host

import (
	"fmt"
	"image"

	"github.com/hajimehoshi/ebiten/v2"
)

// Upscaler selects the frame post-processor.
type Upscaler string

const (
	UpscalerOff      Upscaler = ""
	UpscalerBilinear Upscaler = "bilinear"
	UpscalerBicubic  Upscaler = "bicubic"
	UpscalerAnime4K  Upscaler = "anime4k"
)

func newPostProcessor(mode Upscaler) (PostProcessor, error) {
	switch mode {
	case UpscalerBilinear:
		return NewBilinearPostProcessor()
	case UpscalerBicubic:
		return NewBicubicPostProcessor()
	case UpscalerAnime4K:
		return NewAnime4KPostProcessor()
	default:
		return nil, fmt.Errorf("unsupported upscaler %q", mode)
	}
}

type bilinearPostProcessor struct {
	output  *ebiten.Image
	options ebiten.DrawImageOptions
	width   int
	height  int
}

func (p *bilinearPostProcessor) Process(src *ebiten.Image) (*ebiten.Image, error) {
	if p == nil || src == nil {
		return nil, fmt.Errorf("nil bilinear source")
	}
	bounds := src.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("invalid bilinear source %dx%d", width, height)
	}
	if p.width != width || p.height != height || p.output == nil {
		p.releaseTargets()
		p.width, p.height = width, height
		p.output = newGPUImage(width*2, height*2)
	}
	p.options.GeoM.Reset()
	p.options.GeoM.Scale(2, 2)
	p.options.Filter = ebiten.FilterLinear
	p.options.Blend = ebiten.BlendSourceOver
	p.output.DrawImage(src, &p.options)
	drawScaledEdges(p.output, src, bounds)
	return p.output, nil
}

func drawScaledEdges(output, src *ebiten.Image, bounds image.Rectangle) {
	edge := ebiten.DrawImageOptions{Filter: ebiten.FilterNearest, Blend: gpuReplaceBlend}
	draw := func(rect image.Rectangle, tx, ty float64) {
		part := src.SubImage(rect).(*ebiten.Image)
		edge.GeoM.Reset()
		edge.GeoM.Scale(2, 2)
		edge.GeoM.Translate(-float64(rect.Min.X)*2+tx, -float64(rect.Min.Y)*2+ty)
		output.DrawImage(part, &edge)
	}
	draw(image.Rect(bounds.Min.X, bounds.Min.Y, bounds.Min.X+1, bounds.Max.Y), 0, 0)
	draw(image.Rect(bounds.Max.X-1, bounds.Min.Y, bounds.Max.X, bounds.Max.Y), float64((bounds.Dx()-1)*2), 0)
	draw(image.Rect(bounds.Min.X, bounds.Min.Y, bounds.Max.X, bounds.Min.Y+1), 0, 0)
	draw(image.Rect(bounds.Min.X, bounds.Max.Y-1, bounds.Max.X, bounds.Max.Y), 0, float64((bounds.Dy()-1)*2))
}

func (p *bilinearPostProcessor) Reset() {
	if p != nil {
		p.releaseTargets()
		p.width, p.height = 0, 0
	}
}

func (p *bilinearPostProcessor) Deallocate() {
	if p != nil {
		p.releaseTargets()
		p.width, p.height = 0, 0
	}
}

func (p *bilinearPostProcessor) releaseTargets() {
	if p.output != nil {
		p.output.Deallocate()
		p.output = nil
	}
}

type shaderScalerPostProcessor struct {
	shader   *ebiten.Shader
	output   *ebiten.Image
	vertices []ebiten.Vertex
	indices  []uint16
	options  ebiten.DrawTrianglesShaderOptions
	width    int
	height   int
}

func newShaderScaler(source string) (*shaderScalerPostProcessor, error) {
	shader, err := ebiten.NewShader([]byte(source))
	if err != nil {
		return nil, fmt.Errorf("compile upscaler shader: %w", err)
	}
	return &shaderScalerPostProcessor{
		shader:  shader,
		indices: []uint16{0, 1, 2, 2, 1, 3},
		options: ebiten.DrawTrianglesShaderOptions{Blend: gpuReplaceBlend},
	}, nil
}

// NewBilinearPostProcessor creates a 2x bilinear scaler.
func NewBilinearPostProcessor() (PostProcessor, error) {
	return &bilinearPostProcessor{}, nil
}

// NewBicubicPostProcessor creates a 2x Catmull-Rom scaler.
func NewBicubicPostProcessor() (PostProcessor, error) {
	return newShaderScaler(bicubicShaderSource)
}

func (p *shaderScalerPostProcessor) Process(src *ebiten.Image) (*ebiten.Image, error) {
	if p == nil || src == nil {
		return nil, fmt.Errorf("nil upscaler source")
	}
	bounds := src.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("invalid upscaler source %dx%d", width, height)
	}
	if p.width != width || p.height != height || p.output == nil {
		p.releaseTargets()
		p.width, p.height = width, height
		p.output = newGPUImage(width*2, height*2)
		p.vertices = scaledQuadVertices(width*2, height*2, width, height)
	}
	p.options.Images[0] = src
	p.options.Images[1] = nil
	p.options.Images[2] = nil
	p.options.Images[3] = nil
	p.output.DrawTrianglesShader(p.vertices, p.indices, p.shader, &p.options)
	drawScaledEdges(p.output, src, bounds)
	return p.output, nil
}

func (p *shaderScalerPostProcessor) Reset() {
	if p != nil {
		p.releaseTargets()
		p.width, p.height = 0, 0
	}
}

func (p *shaderScalerPostProcessor) Deallocate() {
	if p != nil {
		p.releaseTargets()
		if p.shader != nil {
			p.shader.Deallocate()
			p.shader = nil
		}
		p.width, p.height = 0, 0
	}
}

func (p *shaderScalerPostProcessor) releaseTargets() {
	if p.output != nil {
		p.output.Deallocate()
		p.output = nil
	}
}

const bicubicShaderSource = `//kage:unit pixels
package main

func cubic(x float) float {
	x = abs(x)
	if x <= 1 {
		return (1.5*x-2.5)*x*x+1
	}
	if x < 2 {
		return ((-0.5*x+2.5)*x-4)*x+2
	}
	return 0
}

func tex(pos vec2) vec4 {
	return imageSrc0At(clamp(pos, vec2(0.5), imageSrc0Size()-vec2(0.5)))
}

func Fragment(dstPos vec4, src0Pos vec2, color vec4) vec4 {
	s := dstPos.xy * 0.5
	base := floor(s - vec2(0.5))
	f := s - (base + vec2(0.5))
	total := vec4(0.0)
	total += tex(base+vec2(0.5,0.5)) * (cubic(f.x+1.0-0.0)*cubic(f.y+1.0-0.0))
	total += tex(base+vec2(1.5,0.5)) * (cubic(f.x+1.0-1.0)*cubic(f.y+1.0-0.0))
	total += tex(base+vec2(2.5,0.5)) * (cubic(f.x+1.0-2.0)*cubic(f.y+1.0-0.0))
	total += tex(base+vec2(3.5,0.5)) * (cubic(f.x+1.0-3.0)*cubic(f.y+1.0-0.0))
	total += tex(base+vec2(0.5,1.5)) * (cubic(f.x+1.0-0.0)*cubic(f.y+1.0-1.0))
	total += tex(base+vec2(1.5,1.5)) * (cubic(f.x+1.0-1.0)*cubic(f.y+1.0-1.0))
	total += tex(base+vec2(2.5,1.5)) * (cubic(f.x+1.0-2.0)*cubic(f.y+1.0-1.0))
	total += tex(base+vec2(3.5,1.5)) * (cubic(f.x+1.0-3.0)*cubic(f.y+1.0-1.0))
	total += tex(base+vec2(0.5,2.5)) * (cubic(f.x+1.0-0.0)*cubic(f.y+1.0-2.0))
	total += tex(base+vec2(1.5,2.5)) * (cubic(f.x+1.0-1.0)*cubic(f.y+1.0-2.0))
	total += tex(base+vec2(2.5,2.5)) * (cubic(f.x+1.0-2.0)*cubic(f.y+1.0-2.0))
	total += tex(base+vec2(3.5,2.5)) * (cubic(f.x+1.0-3.0)*cubic(f.y+1.0-2.0))
	total += tex(base+vec2(0.5,3.5)) * (cubic(f.x+1.0-0.0)*cubic(f.y+1.0-3.0))
	total += tex(base+vec2(1.5,3.5)) * (cubic(f.x+1.0-1.0)*cubic(f.y+1.0-3.0))
	total += tex(base+vec2(2.5,3.5)) * (cubic(f.x+1.0-2.0)*cubic(f.y+1.0-3.0))
	total += tex(base+vec2(3.5,3.5)) * (cubic(f.x+1.0-3.0)*cubic(f.y+1.0-3.0))
	return clamp(total, vec4(0.0), vec4(1.0))
}
`
