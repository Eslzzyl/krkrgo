# Graphics Composition: CPU and GPU

This document records the composition contract and the implementation facts used by
krkrgo. The CPU compositor remains the semantic reference for headless execution,
while interactive presentation uses the GPU path by default.

## Current architecture

The runtime owns layer state, image decoding, text rasterization, media decoding,
hit testing, and transition timing. `internal/runtime/compositor.go` retains the
complete CPU pixel compositor and `FrameSnapshot` API. It is used by replay,
explicit CPU presentation, and as the fallback when GPU initialization fails.

Interactive Ebitengine presentation captures a borrowed `runtime.RenderPlan` in one
update tick. A plan contains ordered nodes, child-index topology, clips, inherited
binder target information, image offsets, and transition inputs. It does not clone
frame RGBA data: node and transition slices are borrowed until the synchronous host
consumes the plan.

The GPU compositor uploads direct (non-premultiplied) runtime RGBA into cached
premultiplied textures. Uploads are selected by layer identity, dimensions, and the
runtime content hash, so callers may mutate an image directly and the next plan
observes the changed hash. Rule textures expand indexed rule bytes to RGBA with the
rule value in red and opaque alpha.

## GPU composition contract

Interactive Ebitengine mode defaults to GPU. CPU composition remains available for
explicit CPU renderer, GPU initialization failure, and all headless replay. The GPU
composition uses two render targets (ping-pong): each layer operation copies the
current destination to the other target. `LayerOpaque` uses a dedicated GPU copy:
opacity 255 uses replace blending, while lower opacity uses source-over after the
source texture has been made opaque. Normal `LayerAlpha` layers directly under a
binder use Ebitengine source-over blending, preserving the CPU compositor's fade
semantics. Other supported layer modes and transitions use Kage shaders with
replace blending. Opaque-layer textures preserve RGB while forcing alpha to 1
during premultiplication. Offscreen and source images use unmanaged Ebitengine
images, matching krkr2's independently updated GPU textures.

Layer shaders receive the destination and source images plus uniforms for opacity,
source/destination rectangles, target layer type, and hold-alpha. Supported blend
modes are selected by the layer type, inherited binder target, and hold-alpha. Effect
and filter layers remain explicitly unsupported. Clip intersection, absolute
positions, child order, and transition-with-children semantics follow the CPU
compositor's observable rules.

Crossfade shaders receive destination/source textures and phase, including opaque
and zero-destination-opaque classifications. Mixed-alpha crossfade and universal
rule-reveal paths unpremultiply endpoint colors for the runtime's straight-alpha
interpolation, then premultiply the shader result before replace blending.
Universal transition shaders receive destination/source/rule textures, phase,
vague, and the planned rule dimensions. Transitions are therefore presented
without a per-frame CPU readback.

## Reference implementation facts

The krkr2 renderer's compile-time default renderer is `software`. OpenGL is an
experimental renderer selected through configuration; it is not the default fact on
which krkrgo's presentation policy is based. krkrgo instead uses Ebitengine's
existing platform backends and Kage shaders, without adding an OpenGL, Direct3D,
Vulkan, Cocos2d-x, or cgo dependency.

## Performance and correctness notes

The CPU path still provides exact deterministic snapshots for replay and the stable
software reference used for parity tests. The GPU path avoids full-frame CPU copies
and readbacks during interactive presentation, while retaining CPU-side preparation
for text, bitmaps, video, and image APIs. Window layout follows the runtime frame
size, so presentation does not introduce an additional resampling step.

GPU output is compared with the CPU reference using per-channel tolerances. Exact
bytes can differ across graphics drivers, shader precision, and premultiplied
intermediate rounding; such differences are expected and must not alter layer order,
visibility, alpha semantics, transition phase, or hit testing.
