package runtime

import (
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"math"

	"github.com/eslzzyl/krkrgo/internal/assets"
)

// motionParticle is one live emitter particle. Positions are relative to the
// emitter node in canvas pixels.
type motionParticle struct {
	x, y, z  float64
	angle    float64
	scale    float64
	age      float64
	birth    float64
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
func motionParticleRandoms(label string, index int) [12]float64 {
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
	var result [12]float64
	for index := range result {
		result[index] = next()
	}
	return result
}

// motionParticleRandom hands out one emission's draws in the reference order:
// template index, spawn offset, speed, spread, angle, zoom
// (PlayerUpdateParticles.cpp:523-860).
type motionParticleRandom struct {
	values [12]float64
	next   int
}

func (random *motionParticleRandom) value() float64 {
	if random.next >= len(random.values) {
		return 0
	}
	value := random.values[random.next]
	random.next++
	return value
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
	// A looping template never stops playing, so its child is never erased.
	if loopTime, ok := motionLoopTimeValue(nested); ok && loopTime >= 0 {
		return math.Inf(1)
	}
	totalFrames, _ := motionTotalFramesValue(nested)
	return totalFrames
}

// motionParticleDuration returns the template motion's total frame count,
// which drives the decay-direction speed even when the template loops.
func motionParticleDuration(resource *motionResource, attached []*motionResource, template string) float64 {
	path := template
	if index := len("motion/"); len(path) > index && path[:index] == "motion/" {
		path = path[index:]
	}
	parts := splitMotionPath(path)
	if len(parts) < 2 {
		return 0
	}
	_, _, nested := motionLookupMotion(resource, attached, parts[0], parts[1])
	totalFrames, _ := motionTotalFramesValue(nested)
	return totalFrames
}

func motionResourceEmoteMode(resource *motionResource) bool {
	if resource == nil || resource.doc == nil {
		return false
	}
	root, _ := resource.doc.Root.(map[string]assets.Value)
	return psbMotionInt(root, "type", 0) == 1
}

// motionParticles simulates the live particles of a type-4 emitter node at
// tick. Emission, spawn offsets, direction, spread, angle, zoom, and the
// emitter's own velocity inheritance follow PlayerUpdateParticles.cpp:440-880;
// lifetimes come from the template motion's total frame count.
func motionParticles(resource *motionResource, attached []*motionResource, node map[string]assets.Value, content map[string]assets.Value, ctx motionRenderContext, tick float64, width, height int) []motionParticle {
	if motionResourceEmoteMode(resource) {
		return nil
	}
	templates := motionStringList(node["particleMotionList"])
	if len(templates) == 0 {
		return nil
	}
	maxNum := psbMotionInt(node, "particleMaxNum", 0)
	if maxNum <= 0 {
		return nil
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
	label := psbLabel(node)
	if label == "" {
		label = "particle"
	}
	// trigger 1 counts the burst the emitter spawns, one particle per frame
	// (PlayerUpdateParticles.cpp:515-520); trigger 0 spaces emissions by
	// 60/frequency frames (PlayerUpdateParticles.cpp:448-480).
	count := -1
	if trigger == 0 {
		if fmin <= 0 {
			return nil
		}
	} else if trigger == 1 {
		seed := motionParticleRandoms(label, -1)
		count = int(fmin + (fmax-fmin)*seed[0])
		if count <= 0 {
			return nil
		}
	} else {
		// Only the frequency and count trigger types emit; the reference's
		// switch leaves every other type with an empty emission count
		// (PlayerUpdateParticles.cpp:444-490).
		return nil
	}
	accel := psbMotionFloat(node, "particleAccelRatio", 1)
	if accel <= 0 {
		accel = 1
	}
	flyDirection := psbMotionInt(node, "particleFlyDirection", 0)
	flyType := psbMotionInt(node, "particle", 0)
	triVolume := psbMotionInt(node, "particleTriVolume", 0) != 0
	inheritVelocity := psbMotionInt(node, "particleInheritVelocity", 0)
	applyZoom := psbMotionInt(node, "particleApplyZoomToVelocity", 0)
	inheritAngle := psbMotionInt(node, "particleInheritAngle", 0) != 0
	deleteOutside := psbMotionInt(node, "particleDeleteOutsideScreen", 0) != 0
	coordinateMode := psbMotionInt(node, "coordinate", 0)
	// Each particle keeps the transform it had at birth, so the emitter's own
	// timeline supplies a per-birth matrix and position. The parent chain is
	// held at the current frame; that is exact for a static parent and matches
	// the static emitters the shipped data uses.
	frames, _ := node["frameList"].([]assets.Value)
	currentLocal := identityMotionMatrix
	currentX, currentY, currentZ := 0.0, 0.0, 0.0
	if len(frames) > 0 {
		if content, visible := psbMotionResolvedContent(frames, tick, psbMotionInt(node, "type", 0), psbMotionInt(node, "coordinate", 0)); visible {
			currentX, currentY, currentZ = motionNodeCoord(content)
			currentLocal = psbMotionLocalMatrix(node, content)
		}
	}
	parentMatrix := multiplyMotionMatrix(ctx.matrix, invertMotionMatrix(currentLocal))

	lifetimes := make(map[string]float64, len(templates))
	durations := make(map[string]float64, len(templates))
	last := math.Floor(tick)
	particles := make([]motionParticle, 0, maxNum)
	// Frequency mode primes its timer on activation and emits when the timer
	// reaches zero (PlayerUpdateParticles.cpp:452-466), so the first burst
	// lands one interval after activation.
	birth := 0.0
	if trigger == 0 {
		seed := motionParticleRandoms(label, -1)
		birth = 60.0 / fmin
		if fmax > 0 && fmax != fmin {
			birth = 60/fmin + (60/fmax-60/fmin)*seed[0]
		}
	}
	for emitted := 0; birth <= last; emitted++ {
		if count >= 0 && emitted >= count {
			break
		}
		values := motionParticleRandoms(label, emitted)
		random := motionParticleRandom{values: values}
		interval := 1.0
		if trigger == 0 {
			// The timer draw precedes creation, so it consumes the first value
			// (PlayerUpdateParticles.cpp:457-466).
			interval = 60.0 / fmin
			if fmax > 0 && fmax != fmin {
				interval = 60/fmin + (60/fmax-60/fmin)*random.value()
			}
			if interval < 1 {
				interval = 1
			}
		}
		template := templates[int(random.value()*float64(len(templates)))%len(templates)]
		lifetime, known := lifetimes[template]
		if !known {
			lifetime = motionParticleLifetime(resource, attached, template)
			lifetimes[template] = lifetime
		}
		if lifetime <= 0 {
			lifetime = 1
		}
		duration, knownDuration := durations[template]
		if !knownDuration {
			duration = motionParticleDuration(resource, attached, template)
			durations[template] = duration
		}
		age := tick - birth
		if age >= 0 && age <= lifetime {
			birthLocal := currentLocal
			birthX, birthY, birthZ := currentX, currentY, currentZ
			birthContent := content
			if len(frames) > 0 {
				if content, visible := psbMotionResolvedContent(frames, birth, psbMotionInt(node, "type", 0), psbMotionInt(node, "coordinate", 0)); visible {
					birthX, birthY, birthZ = motionNodeCoord(content)
					birthLocal = psbMotionLocalMatrix(node, content)
					birthContent = content
				}
			}
			emitMatrix := multiplyMotionMatrix(parentMatrix, birthLocal)
			// The reference adds the emitter's displacement from the particle's
			// birth update to that child's velocity
			// (PlayerUpdateParticles.cpp:870-880).
			var deltaX, deltaY, deltaZ float64
			if inheritVelocity == 1 {
				localX, localY, localZ := motionNodeLocalDelta(node, birth)
				if coordinateMode == 1 {
					deltaX = parentMatrix.a*localX + parentMatrix.b*localZ
					deltaZ = parentMatrix.c*localX + parentMatrix.d*localZ
					deltaY = localY
				} else {
					deltaX = parentMatrix.a*localX + parentMatrix.b*localY
					deltaY = parentMatrix.c*localX + parentMatrix.d*localY
					deltaZ = localZ
				}
			}
			// The reference spawns the child at the emitter's accumulated
			// position for that frame, so the delta back to the birth position
			// stays fixed in canvas space for inheritVelocity modes other
			// than 2.
			var baseX, baseY, baseZ float64
			if coordinateMode == 1 {
				baseX = parentMatrix.a*(birthX-currentX) + parentMatrix.b*(birthZ-currentZ)
				baseZ = parentMatrix.c*(birthX-currentX) + parentMatrix.d*(birthZ-currentZ)
				baseY = birthY - currentY
			} else {
				baseX = parentMatrix.a*(birthX-currentX) + parentMatrix.b*(birthY-currentY)
				baseY = parentMatrix.c*(birthX-currentX) + parentMatrix.d*(birthY-currentY)
				baseZ = birthZ - currentZ
			}
			offsetX, offsetY, offsetZ := motionParticleOffset(flyType, triVolume, &random)
			if offsetZ != 0 {
				// The reference scales the depth offset by sqrt(det) of the
				// emitter matrix (PlayerUpdateParticles.cpp:652-657).
				offsetZ *= math.Sqrt(emitMatrix.a*emitMatrix.d - emitMatrix.b*emitMatrix.c)
			}
			clipOriginX := psbMotionFloat(birthContent, "ox", 0)
			clipOriginY := psbMotionFloat(birthContent, "oy", 0)
			txOff := emitMatrix.a*(offsetX-clipOriginX) + emitMatrix.b*(offsetY-clipOriginY)
			tyOff := emitMatrix.c*(offsetX-clipOriginX) + emitMatrix.d*(offsetY-clipOriginY)

			speed := vmin
			if vmin != vmax {
				speed = vmin + (vmax-vmin)*random.value()
			}
			direction := math.Atan2(emitMatrix.b, emitMatrix.a)
			switch flyDirection {
			case 2:
				// Exponential decay toward the spawn offset
				// (PlayerUpdateParticles.cpp:690-706).
				distance := math.Sqrt(txOff*txOff + tyOff*tyOff + offsetZ*offsetZ)
				normalized := duration / 60
				if accel == 1 {
					if normalized > 0 {
						speed = distance / normalized
					}
				} else if normalized > 0 {
					speed = distance * math.Log(accel) / (math.Pow(accel, normalized) - 1)
				}
				direction = math.Atan2(tyOff, txOff) + math.Pi
				speed /= 60
			case 1:
				direction = math.Atan2(tyOff, txOff) + math.Pi
			}
			totalAngle := direction
			if spread != 0 {
				totalAngle += (spread*2*random.value() - spread) * math.Pi / 180
			}
			// Depth scale for 3D spawns (PlayerUpdateParticles.cpp:712-724).
			zoomScale := 1.0
			if (flyDirection == 1 || flyDirection == 2) && (txOff != 0 || tyOff != 0) && offsetZ != 0 {
				xyLength := math.Hypot(txOff, tyOff)
				zoomScale = xyLength / math.Hypot(xyLength, offsetZ)
			}
			var velocityX, velocityY, velocityZ float64
			var spawnX, spawnY, spawnZ float64
			if coordinateMode == 1 {
				// 3D placement puts the spawn height in y and the ground offset
				// in z (PlayerUpdateParticles.cpp:755-765).
				spawnX, spawnY, spawnZ = txOff, offsetZ, tyOff
				velocityX = zoomScale * speed * math.Cos(totalAngle)
				velocityZ = zoomScale * speed * math.Sin(totalAngle)
			} else {
				spawnX, spawnY, spawnZ = txOff, tyOff, offsetZ
				velocityX = zoomScale * speed * math.Cos(totalAngle)
				velocityY = zoomScale * speed * math.Sin(totalAngle)
			}
			angle := amin
			if amin != amax {
				angle = amin + (amax-amin)*random.value()
			}
			if ctx.transform.flipX != ctx.transform.flipY {
				angle = -angle
			}
			if inheritAngle {
				inheritedAngle := totalAngle
				if ctx.transform.flipX {
					inheritedAngle += math.Pi
				}
				angle += inheritedAngle * 180 / math.Pi
			}
			if inheritVelocity == 2 && inheritAngle {
				birthTransform := motionTransformFromContent(birthContent)
				if ctx.inheritParent != nil {
					birthTransform = inheritMotionTransform(ctx.inheritParent.transform, birthTransform, ctx.inheritMask)
				}
				angleDelta := ctx.transform.angle - birthTransform.angle
				if ctx.transform.flipX != ctx.transform.flipY {
					angleDelta = -angleDelta
				}
				angle += angleDelta
			}
			angle = math.Mod(angle, 360)
			if angle < 0 {
				angle += 360
			}
			zoom := zmin
			if zmin != zmax {
				zoom = zmin + (zmax-zmin)*random.value()
			}
			// applyZoomToVelocity is gated off in decay mode
			// (PlayerUpdateParticles.cpp:848-862).
			if flyDirection != 2 {
				if applyZoom == 1 {
					velocityX *= zoom
					velocityY *= zoom
					velocityZ *= zoom
				} else if applyZoom == 2 && zoom != 0 {
					velocityX /= zoom
					velocityY /= zoom
					velocityZ /= zoom
				}
			}
			if inheritVelocity == 1 {
				velocityX += deltaX
				velocityY += deltaY
				velocityZ += deltaZ
			}

			travel := age
			if accel != 1 {
				rate := math.Pow(accel, 1.0/60)
				if rate != 1 {
					travel = (1 - math.Pow(rate, age)) / (1 - rate)
				}
			}
			x := spawnX + velocityX*travel
			y := spawnY + velocityY*travel
			z := spawnZ + velocityZ*travel
			if inheritVelocity == 2 {
				// Mode 2 accumulates each frame's transform delta into the
				// existing children; over the lifetime the product reduces to
				// inv(birth) * current around the emitter
				// (PlayerUpdateParticles.cpp:250-330).
				rotate := multiplyMotionMatrix(invertMotionMatrix(birthLocal), currentLocal)
				rotatedX := rotate.a*x + rotate.b*y
				rotatedY := rotate.c*x + rotate.d*y
				x, y = rotatedX, rotatedY
				baseX, baseY, baseZ = 0, 0, 0
			}
			x += baseX
			y += baseY
			z += baseZ
			if deleteOutside && width > 0 && height > 0 {
				if x < -float64(width) || x > 2*float64(width) || y < -float64(height) || y > 2*float64(height) {
					goto next
				}
			}
			particles = append(particles, motionParticle{
				x:        x,
				y:        y,
				z:        z,
				angle:    angle,
				scale:    zoom,
				age:      age,
				birth:    birth,
				template: template,
			})
			if len(particles) > maxNum {
				particles = particles[1:]
			}
		}
	next:
		if trigger == 0 {
			birth += interval
		} else {
			birth++
		}
	}
	return particles
}

// motionNodeLocalDelta returns a node's local position change over one tick,
// the displacement the reference stores as deltaPosX/Y/Z.
func motionNodeLocalDelta(node map[string]assets.Value, tick float64) (float64, float64, float64) {
	frames, ok := node["frameList"].([]assets.Value)
	if !ok || len(frames) == 0 {
		return 0, 0, 0
	}
	nodeType := psbMotionInt(node, "type", 0)
	coordinateMode := psbMotionInt(node, "coordinate", 0)
	now, nowVisible := psbMotionResolvedContent(frames, tick, nodeType, coordinateMode)
	previous, previousVisible := psbMotionResolvedContent(frames, tick-1, nodeType, coordinateMode)
	if !nowVisible || !previousVisible {
		return 0, 0, 0
	}
	x0, y0, z0 := motionNodeCoord(previous)
	x1, y1, z1 := motionNodeCoord(now)
	return x1 - x0, y1 - y0, z1 - z0
}

// motionParticleOffset mirrors the spawn offsets
// (PlayerUpdateParticles.cpp:612-650): 1 is a 16px disk (sphere when
// particleTriVolume is set), 2 a 16px box.
func motionParticleOffset(flyType int, triVolume bool, random *motionParticleRandom) (float64, float64, float64) {
	switch flyType {
	case 1:
		if triVolume {
			r1 := random.value()
			r2 := random.value()
			r3 := random.value()
			phi := r2 * 2 * math.Pi
			theta := r1 * 2 * math.Pi
			radius := math.Cbrt(r3) * 16
			cosPhi := math.Cos(phi)
			return cosPhi * radius * math.Cos(theta),
				radius * cosPhi * math.Sin(theta),
				radius * math.Sin(phi)
		}
		angle := random.value() * 2 * math.Pi
		radius := math.Sqrt(random.value()) * 16
		return math.Cos(angle) * radius, math.Sin(angle) * radius, 0
	case 2:
		x := random.value()*32 - 16
		y := random.value()*32 - 16
		var z float64
		if triVolume {
			z = random.value()*32 - 16
		}
		return x, y, z
	}
	return 0, 0, 0
}

// motionParticleTransform composes the emitter context with one particle's
// offset, rotation, and zoom.
func motionParticleTransform(ctx motionRenderContext, particle motionParticle) motionRenderContext {
	ctx.x += particle.x
	ctx.y += particle.y
	ctx.z += particle.z
	ctx.childTick = 0
	// A particle child is a separate player. The emitter contributes the
	// spawn position, parent flips, and blend/color state; its root matrix and
	// transform do not carry into the template
	// (PlayerUpdateParticles.cpp:740-850).
	radians := particle.angle * math.Pi / 180
	cosine, sine := math.Cos(radians), math.Sin(radians)
	ctx.matrix = motionMatrix{
		a: cosine * particle.scale,
		b: -sine * particle.scale,
		c: sine * particle.scale,
		d: cosine * particle.scale,
	}
	ctx.rootMatrix = ctx.matrix
	ctx.coordinateMode = 0
	ctx.transform = motionTransformState{
		flipX: ctx.transform.flipX, flipY: ctx.transform.flipY,
		angle: particle.angle, scaleX: particle.scale, scaleY: particle.scale,
	}
	ctx.rootTransform = ctx.transform
	ctx.inheritMask = 0
	ctx.inheritParent = nil
	ctx.evalNode = nil
	ctx.targetNodes = nil
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

// collectMotionParticles appends a type-4 emitter's live particles to the CPU
// draw list, one deferred subtree per particle.
func collectMotionParticles(resource *motionResource, layer map[string]assets.Value, content map[string]assets.Value, ctx motionRenderContext, eval motionRenderEval, tick float64, list *motionDrawList, width, height int, stack map[string]bool) error {
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
		if err := collectMotionValue(owner, ownerRoot, layers, particle.age, eval, particleCtx, list, width, height, stack); err != nil {
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
	for particleIndex, particle := range particles {
		owner, ownerRoot, layers := motionParticleLayers(resource, eval.attached, particle.template)
		if len(layers) == 0 {
			continue
		}
		particleCtx := motionParticleTransform(ctx, particle)
		particleCtx.layerTreeKey = fmt.Sprintf("%s/emitter:%d/particle:%d/template:%s", ctx.layerTreeKey, ctx.layerID, particleIndex, particle.template)
		particleLayerID := 1
		if err := collectGPUMotionLayers(owner, ownerRoot, layers, particle.age, eval, particleCtx, drawMatrix, result, stack, &particleLayerID); err != nil {
			return err
		}
	}
	return nil
}
