package host

import (
	"fmt"

	"github.com/hajimehoshi/ebiten/v2"
)

// PostProcessor upscales a composed frame. Process owns the returned image
// until the next call or Deallocate.
type PostProcessor interface {
	Process(src *ebiten.Image) (*ebiten.Image, error)
	Reset()
	Deallocate()
}

type anime4KPostProcessor struct {
	pass1 *ebiten.Shader
	pass2 *ebiten.Shader
	pass3 *ebiten.Shader
	pass4 *ebiten.Shader
	final *ebiten.Shader

	featureA *ebiten.Image
	featureB *ebiten.Image
	output   *ebiten.Image

	passVertices  []ebiten.Vertex
	finalVertices []ebiten.Vertex
	indices       []uint16
	options       ebiten.DrawTrianglesShaderOptions

	width, height int
}

// NewAnime4KPostProcessor compiles the Anime4K Fast/S shader chain.
func NewAnime4KPostProcessor() (PostProcessor, error) {
	p := &anime4KPostProcessor{}
	shaders := []struct {
		name   string
		source string
		dst    **ebiten.Shader
	}{
		{"pass1", anime4KPass1ShaderSource, &p.pass1},
		{"pass2", anime4KPass2ShaderSource, &p.pass2},
		{"pass3", anime4KPass3ShaderSource, &p.pass3},
		{"pass4", anime4KPass4ShaderSource, &p.pass4},
		{"final", anime4KFinalShaderSource, &p.final},
	}
	for _, entry := range shaders {
		shader, err := ebiten.NewShader([]byte(entry.source))
		if err != nil {
			p.Deallocate()
			return nil, fmt.Errorf("compile Anime4K %s shader: %w", entry.name, err)
		}
		*entry.dst = shader
	}
	p.indices = []uint16{0, 1, 2, 2, 1, 3}
	p.options.Blend = gpuReplaceBlend
	return p, nil
}

func (p *anime4KPostProcessor) Process(src *ebiten.Image) (*ebiten.Image, error) {
	if p == nil {
		return nil, fmt.Errorf("nil Anime4K post-processor")
	}
	if src == nil {
		return nil, fmt.Errorf("nil Anime4K source")
	}
	bounds := src.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("invalid Anime4K source %dx%d", width, height)
	}
	if p.width != width || p.height != height || p.featureA == nil {
		p.releaseTargets()
		p.width, p.height = width, height
		p.featureA = newGPUImage(width, height)
		p.featureB = newGPUImage(width, height)
		p.output = newGPUImage(width*2, height*2)
		p.passVertices = fullQuadVertices(width, height)
		p.finalVertices = scaledQuadVertices(width*2, height*2, width, height)
	}

	p.draw(p.featureA, src, p.pass1, p.passVertices)
	p.draw(p.featureB, p.featureA, p.pass2, p.passVertices)
	p.draw(p.featureA, p.featureB, p.pass3, p.passVertices)
	p.draw(p.featureB, p.featureA, p.pass4, p.passVertices)
	p.drawFinal(p.output, p.featureB, src)
	return p.output, nil
}

func (p *anime4KPostProcessor) draw(target, source *ebiten.Image, shader *ebiten.Shader, vertices []ebiten.Vertex) {
	p.options.Images[0] = source
	p.options.Images[1] = nil
	p.options.Images[2] = nil
	p.options.Images[3] = nil
	target.DrawTrianglesShader(vertices, p.indices, shader, &p.options)
}

func (p *anime4KPostProcessor) drawFinal(target, features, source *ebiten.Image) {
	p.options.Images[0] = features
	p.options.Images[1] = source
	p.options.Images[2] = nil
	p.options.Images[3] = nil
	target.DrawTrianglesShader(p.finalVertices, p.indices, p.final, &p.options)
}

func fullQuadVertices(width, height int) []ebiten.Vertex {
	return scaledQuadVertices(width, height, width, height)
}

func scaledQuadVertices(dstWidth, dstHeight, srcWidth, srcHeight int) []ebiten.Vertex {
	dw, dh := float32(dstWidth), float32(dstHeight)
	sw, sh := float32(srcWidth), float32(srcHeight)
	return []ebiten.Vertex{
		{DstX: 0, DstY: 0, SrcX: 0, SrcY: 0},
		{DstX: dw, DstY: 0, SrcX: sw, SrcY: 0},
		{DstX: 0, DstY: dh, SrcX: 0, SrcY: sh},
		{DstX: dw, DstY: dh, SrcX: sw, SrcY: sh},
	}
}

func (p *anime4KPostProcessor) Reset() {
	if p == nil {
		return
	}
	p.releaseTargets()
	p.width, p.height = 0, 0
}

func (p *anime4KPostProcessor) releaseTargets() {
	if p.featureA != nil {
		p.featureA.Deallocate()
		p.featureA = nil
	}
	if p.featureB != nil {
		p.featureB.Deallocate()
		p.featureB = nil
	}
	if p.output != nil {
		p.output.Deallocate()
		p.output = nil
	}
}

func (p *anime4KPostProcessor) Deallocate() {
	if p == nil {
		return
	}
	p.releaseTargets()
	for _, shader := range []*ebiten.Shader{p.pass1, p.pass2, p.pass3, p.pass4, p.final} {
		if shader != nil {
			shader.Deallocate()
		}
	}
	p.pass1, p.pass2, p.pass3, p.pass4, p.final = nil, nil, nil, nil, nil
}
