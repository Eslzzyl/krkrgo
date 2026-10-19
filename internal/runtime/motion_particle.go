package runtime

import (
	"encoding/binary"
	"hash/fnv"
	"math"

	"github.com/eslzzyl/krkrgo/internal/assets"
)

// motionParticle is one live emitter particle. Positions are relative to the
// emitter node in canvas pixels.
type motionParticle struct {
	x, y     float64
	scale    float64
	age      float64
	template string
}

// motionStringList decodes a PSB string array such as particleMotionList.
func motionStringList(value assets.Value) []string {
	items, _ := value.([]assets.Value)
	if len(items) == 0 {
		return nil
	}
	strings := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok && text != "" {
			strings = append(strings, text)
		}
	}
	return strings
}

// motionParticleRandoms returns a stable sequence for one particle birth. The
// reference draws from the player's TJS random generator
// (PlayerUpdateParticles.cpp:506-520); a per-birth hash keeps CPU and GPU
// renders identical and replays deterministic.
func motionParticleRandoms(label string, birth int) [5]float64 {
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(label))
	var buffer [8]byte
	binary.LittleEndian.PutUint64(buffer[:], uint64(birth))
	_, _ = hash.Write(buffer[:])
	state := hash.Sum64()
	next := func() float64 {
		state += 0x9E3779B97F4A7C15
		value := state
		value = (value ^ (value >> 30)) * 0xBF58476D1CE4E5B9
		value = (value ^ (value >> 27)) * 0x94D049BB133111EB
		value ^= value >> 31
		return float64(value>>11) / float64(1<<53)
	}
	var result [5]float64
	for index := range result {
		result[index] = next()
	}
	return result
}

// motionParticles simulates the live particles of a type-4 emitter node at
// tick. The reference emits one particle per frame per node and reads the
// ranges from the frame's "prt" table (PlayerUpdateParticles.cpp:490-560,
// 640-760). Emission restarts from tick zero when the motion loops, which the
// frame-list walk already guarantees by resetting the tick.
func motionParticles(node map[string]assets.Value, content map[string]assets.Value, tick float64) []motionParticle {
	templates := motionStringList(node["particleMotionList"])
	if len(templates) == 0 {
		return nil
	}
	maxNum := psbMotionInt(node, "particleMaxNum", 0)
	if maxNum <= 0 {
		return nil
	}
	if maxNum > 512 {
		maxNum = 512
	}
	parameters, _ := content["prt"].(map[string]assets.Value)
	if parameters == nil {
		return nil
	}
	fmin := psbMotionFloat(parameters, "fmin", 0)
	fmax := psbMotionFloat(parameters, "fmax", fmin)
	if fmax < fmin {
		fmax = fmin
	}
	vmin := psbMotionFloat(parameters, "vmin", 0)
	vmax := psbMotionFloat(parameters, "vmax", vmin)
	amin := psbMotionFloat(parameters, "amin", 0)
	amax := psbMotionFloat(parameters, "amax", amin)
	zmin := psbMotionFloat(parameters, "zmin", 1)
	zmax := psbMotionFloat(parameters, "zmax", zmin)
	trigger := psbMotionInt(parameters, "trigger", 0)
	if trigger == 0 && fmin <= 0 && vmin <= 0 && vmax <= 0 {
		return nil
	}
	accel := psbMotionFloat(node, "particleAccelRatio", 1)
	if accel <= 0 {
		accel = 1
	}
	label := psbLabel(node)
	if label == "" {
		label = "particle"
	}

	last := math.Floor(tick)
	first := last - math.Ceil(fmax)
	if first < 0 {
		first = 0
	}
	if births := int(last-first) + 1; births > maxNum {
		first = last - float64(maxNum-1)
	}
	particles := make([]motionParticle, 0, maxNum)
	for birth := int(first); birth <= int(last); birth++ {
		values := motionParticleRandoms(label, birth)
		life := fmin + (fmax-fmin)*values[3]
		age := tick - float64(birth)
		if age < 0 || age > life {
			continue
		}
		angle := (amin + (amax-amin)*values[0]) * math.Pi / 180
		speed := vmin + (vmax-vmin)*values[1]
		travel := age
		if accel != 1 {
			travel = (math.Pow(accel, age) - 1) / (accel - 1)
		}
		scale := zmin + (zmax-zmin)*values[2]
		template := templates[int(values[4]*float64(len(templates)))%len(templates)]
		particles = append(particles, motionParticle{
			x:        math.Cos(angle) * speed * travel,
			y:        math.Sin(angle) * speed * travel,
			scale:    scale,
			age:      age,
			template: template,
		})
	}
	return particles
}

// motionParticleTransform composes the emitter context with one particle's
// offset and scale.
func motionParticleTransform(ctx motionRenderContext, particle motionParticle) motionRenderContext {
	ctx.x += particle.x
	ctx.y += particle.y
	ctx.childTick = 0
	ctx.matrix = multiplyMotionMatrix(ctx.matrix, motionMatrix{a: particle.scale, d: particle.scale})
	return ctx
}

// motionParticleLayers resolves the motion module a particle template names.
func motionParticleLayers(resource *motionResource, attached []*motionResource, template string) (*motionResource, map[string]assets.Value, []assets.Value) {
	path := template
	if index := len("motion/"); len(path) > index && path[:index] == "motion/" {
		path = path[index:]
	}
	parts := splitMotionPath(path)
	if len(parts) < 2 {
		return nil, nil, nil
	}
	owner, root, nested := motionLookupMotion(resource, attached, parts[0], parts[1])
	layers, _ := nested["layer"].([]assets.Value)
	return owner, root, layers
}

func splitMotionPath(path string) []string {
	parts := make([]string, 0, 3)
	start := 0
	for index := 0; index <= len(path); index++ {
		if index != len(path) && path[index] != '/' {
			continue
		}
		parts = append(parts, path[start:index])
		start = index + 1
	}
	return parts
}

// renderMotionParticles draws a type-4 emitter's live particles into the CPU
// motion canvas.
func renderMotionParticles(resource *motionResource, layer map[string]assets.Value, content map[string]assets.Value, ctx motionRenderContext, eval motionRenderEval, tick float64, pixels []byte, width, height int, stack map[string]bool) error {
	if eval.inParticle {
		return nil
	}
	particles := motionParticles(layer, content, tick)
	if len(particles) == 0 {
		return nil
	}
	eval.inParticle = true
	for _, particle := range particles {
		owner, ownerRoot, layers := motionParticleLayers(resource, eval.attached, particle.template)
		if len(layers) == 0 {
			continue
		}
		particleCtx := motionParticleTransform(ctx, particle)
		if err := renderMotionValue(owner, ownerRoot, layers, particle.age, eval, particleCtx, pixels, width, height, stack); err != nil {
			return err
		}
	}
	return nil
}

// collectGPUMotionParticles emits one deferred draw group per live particle.
func collectGPUMotionParticles(resource *motionResource, layer map[string]assets.Value, content map[string]assets.Value, ctx motionRenderContext, eval motionRenderEval, drawMatrix motionDrawMatrix, tick float64, result *RenderMotion, stack map[string]bool) error {
	if eval.inParticle {
		return nil
	}
	particles := motionParticles(layer, content, tick)
	if len(particles) == 0 {
		return nil
	}
	eval.inParticle = true
	for _, particle := range particles {
		owner, ownerRoot, layers := motionParticleLayers(resource, eval.attached, particle.template)
		if len(layers) == 0 {
			continue
		}
		particleCtx := motionParticleTransform(ctx, particle)
		if err := collectGPUMotionValue(owner, ownerRoot, layers, particle.age, eval, particleCtx, drawMatrix, result, stack); err != nil {
			return err
		}
	}
	return nil
}
