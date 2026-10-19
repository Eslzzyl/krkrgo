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
	angle    float64
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

// motionParticleRandoms returns a stable sequence for one emission. The
// reference draws from the player's TJS random generator
// (PlayerUpdateParticles.cpp:660-770); a per-emission hash keeps CPU, GPU, and
// replay renders identical.
func motionParticleRandoms(label string, index int) [6]float64 {
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(label))
	var buffer [8]byte
	binary.LittleEndian.PutUint64(buffer[:], uint64(index))
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
	var result [6]float64
	for index := range result {
		result[index] = next()
	}
	return result
}

// motionParticleLifetime returns the template motion duration in frames. A
// particle child plays the template motion and is erased when that motion
// stops (PlayerUpdateParticles.cpp:900-930).
func motionParticleLifetime(resource *motionResource, attached []*motionResource, template string) float64 {
	path := template
	if index := len("motion/"); len(path) > index && path[:index] == "motion/" {
		path = path[index:]
	}
	parts := splitMotionPath(path)
	if len(parts) < 2 {
		return 0
	}
	_, _, nested := motionLookupMotion(resource, attached, parts[0], parts[1])
	return float64(psbMotionInt(nested, "lastTime", 0))
}

// motionParticles simulates the live particles of a type-4 emitter node at
// tick. Emission intervals come from the frame's "prt" table, spawn offsets,
// direction, spread, angle, and zoom follow PlayerUpdateParticles.cpp:640-860,
// and the lifetime is the template motion's total frame count.
func motionParticles(resource *motionResource, attached []*motionResource, node map[string]assets.Value, content map[string]assets.Value, ctx motionRenderContext, tick float64, width, height int) []motionParticle {
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
	vmin := psbMotionFloat(parameters, "vmin", 0)
	vmax := psbMotionFloat(parameters, "vmax", vmin)
	amin := psbMotionFloat(parameters, "amin", 0)
	amax := psbMotionFloat(parameters, "amax", amin)
	zmin := psbMotionFloat(parameters, "zmin", 1)
	zmax := psbMotionFloat(parameters, "zmax", zmin)
	spread := psbMotionFloat(parameters, "range", 0)
	trigger := psbMotionInt(parameters, "trigger", 0)
	if fmin <= 0 && fmax <= 0 && vmin <= 0 && vmax <= 0 {
		return nil
	}
	label := psbLabel(node)
	if label == "" {
		label = "particle"
	}
	// trigger 1 counts the burst the emitter spawns, one particle per frame
	// (PlayerUpdateParticles.cpp:515-520); trigger 0 spaces emissions by
	// 60/frequency frames.
	count := -1
	interval := 1
	switch trigger {
	case 1:
		seed := motionParticleRandoms(label, -1)
		count = int(math.Round(fmin + (fmax-fmin)*seed[0]))
		if count < 0 {
			count = 0
		}
	case 0:
		if fmin > 0 {
			interval = int(math.Round(60 / fmin))
			if interval < 1 {
				interval = 1
			}
		}
	}
	accel := psbMotionFloat(node, "particleAccelRatio", 1)
	if accel <= 0 {
		accel = 1
	}
	flyDirection := psbMotionInt(node, "particleFlyDirection", 0)
	flyType := psbMotionInt(node, "particle", 0)
	applyZoom := psbMotionInt(node, "particleApplyZoomToVelocity", 0)
	inheritAngle := psbMotionInt(node, "particleInheritAngle", 0) != 0
	deleteOutside := psbMotionInt(node, "particleDeleteOutsideScreen", 0) != 0
	baseAngle := math.Atan2(ctx.matrix.b, ctx.matrix.a)

	lifetimes := make(map[string]float64, len(templates))
	last := math.Floor(tick)
	particles := make([]motionParticle, 0, maxNum)
	for emitted, birth := 0, 0.0; birth <= last; emitted++ {
		if count >= 0 && emitted >= count {
			break
		}
		if trigger == 0 && int(birth)%interval != 0 {
			birth++
			continue
		}
		values := motionParticleRandoms(label, emitted)
		template := templates[int(values[5]*float64(len(templates)))%len(templates)]
		lifetime, known := lifetimes[template]
		if !known {
			lifetime = motionParticleLifetime(resource, attached, template)
			lifetimes[template] = lifetime
		}
		if lifetime <= 0 {
			lifetime = last - birth
		}
		age := tick - birth
		if age < 0 {
			break
		}
		if age <= lifetime {
			offsetX, offsetY := motionParticleOffset(flyType, values[0])
			speed := vmin + (vmax-vmin)*values[1]
			direction := baseAngle
			switch flyDirection {
			case 1:
				direction = math.Atan2(offsetY, offsetX) + math.Pi
			case 2:
				distance := math.Hypot(offsetX, offsetY)
				decay := accel
				normalized := lifetime / 60
				switch {
				case decay == 1:
					if normalized > 0 {
						speed = distance / normalized
					}
				case decay > 0 && normalized > 0:
					speed = distance * math.Log(decay) / (math.Pow(decay, normalized) - 1)
				}
				direction = math.Atan2(offsetY, offsetX) + math.Pi
				speed /= 60
			}
			totalAngle := direction + (values[2]*2-1)*spread*math.Pi/180
			angle := amin + (amax-amin)*values[3]
			zoom := zmin + (zmax-zmin)*values[4]
			if applyZoom == 1 {
				speed *= zoom
			} else if applyZoom == 2 && zoom != 0 {
				speed /= zoom
			}
			travel := age
			if accel != 1 {
				rate := math.Pow(accel, 1.0/60)
				if rate != 1 {
					travel = (1 - math.Pow(rate, age)) / (1 - rate)
				}
			}
			x := offsetX + math.Cos(totalAngle)*speed*travel
			y := offsetY + math.Sin(totalAngle)*speed*travel
			if inheritAngle {
				angle += math.Mod(totalAngle*180/math.Pi+180, 360)
			}
			if deleteOutside && width > 0 && height > 0 {
				if x < -float64(width) || x > 2*float64(width) || y < -float64(height) || y > 2*float64(height) {
					goto next
				}
			}
			particles = append(particles, motionParticle{
				x:        x,
				y:        y,
				angle:    angle,
				scale:    zoom,
				age:      age,
				template: template,
			})
			if len(particles) > maxNum {
				particles = particles[1:]
			}
		}
	next:
		birth++
	}
	return particles
}

// motionParticleOffset mirrors the fly-direction spawn offsets
// (PlayerUpdateParticles.cpp:640-655): 1 is a 16px disk, 2 a 16px box.
func motionParticleOffset(flyType int, random float64) (float64, float64) {
	switch flyType {
	case 1:
		angle := random * 2 * math.Pi
		radius := math.Sqrt(random) * 16
		return math.Cos(angle) * radius, math.Sin(angle) * radius
	case 2:
		return random*32 - 16, random*32 - 16
	}
	return 0, 0
}

// motionParticleTransform composes the emitter context with one particle's
// offset, rotation, and zoom.
func motionParticleTransform(ctx motionRenderContext, particle motionParticle) motionRenderContext {
	ctx.x += particle.x
	ctx.y += particle.y
	ctx.childTick = 0
	radians := particle.angle * math.Pi / 180
	cosine, sine := math.Cos(radians), math.Sin(radians)
	local := motionMatrix{
		a: cosine * particle.scale,
		b: -sine * particle.scale,
		c: sine * particle.scale,
		d: cosine * particle.scale,
	}
	ctx.matrix = multiplyMotionMatrix(ctx.matrix, local)
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
	particles := motionParticles(resource, eval.attached, layer, content, ctx, tick, width, height)
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
	particles := motionParticles(resource, eval.attached, layer, content, ctx, tick, result.Width, result.Height)
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
