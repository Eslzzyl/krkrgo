# Graphics Composition: CPU vs GPU

This document records the composition-architecture findings from a review of the
Go, Rust, and C++ reference implementations. It is an analysis, not a porting
guide; the actionable migration is the last section.

## Current Go architecture

The Go runtime composites on the CPU and presents on the GPU.

- [`compositor.go`](../internal/runtime/compositor.go) performs per-pixel layer
  composition: z-order stacking, crossfade, text rasterization, and dirty-bounds
  tracking produce a 1280×720 RGBA snapshot.
- [`layer_draw.go`](../internal/runtime/layer_draw.go) does per-layer pixel
  operations (image, text, clip, transition).
- [`layer_transition_native.go`](../internal/runtime/layer_transition_native.go)
  runs crossfade/universal transitions as CPU formulas with a table-based
  zero-to-opaque fast path.
- [`host/ebiten.go`](../internal/host/ebiten.go) uploads the snapshot via
  `WritePixels` to an `ebiten.Image` and presents it with `DrawImage` at 1:1
  scale. The window is resized to the frame size, so Ebitengine does no
  resampling.

The GPU performs only the final blit. The `CGO_ENABLED=0` baseline applies to
runtime code (TJS VM, compositor, interpreter), not to Ebitengine: the Metal,
Direct3D, and OpenGL backends use `github.com/ebitengine/purego` (cgo-free FFI),
so the presentation layer already runs without cgo.

## Three-implementation comparison

All three implementations share the CPU-composition + GPU-blit structure in the
paths the Go and Rust ports actually exercise:

| | C++ (`krkrz`) | Rust (`krkrz_rs`) | Go (`krkrgo`) |
| --- | --- | --- | --- |
| Composition | CPU (`InternalComplete2`) | CPU (`layer/draw.rs` → full RGBA `Image`) | CPU (`compositor.go`) |
| Upload | `glTexSubImage2D` to persistent texture | `queue.write_texture` | `WritePixels` to `ebiten.Image` |
| Present | `glDrawArrays` final blit | render pipeline blit + `frame.present()` | `DrawImage` |
| GPU composition | **Yes** (`InternalComplete2_GPU` → `Draw_GPU`) | No | No |

The C++ and Rust paths that run today are CPU composition. The Rust presenter
labels its texture `"Kirikiri CPU frame"` and uploads a CPU-generated RGBA
`image.rgba`. The Go and Rust ports are structurally identical.

## Key finding: C++ has a dual path, GPU by default

The C++ compositor selects between CPU and GPU composition at runtime.
`LayerIntf.cpp:70-75`:

```cpp
static bool IsGPU() {
    static bool isGPU = !TVPIsSoftwareRenderManager() &&
        !IndividualConfigManager::GetInstance()->GetValue<bool>(
            "ogl_accurate_render", false);
    return isGPU;  // default true
}
```

`IsGPU()` returns **true by default**. It is false only when the software
render manager is active or the `ogl_accurate_render` debug flag is set. The
completion dispatch (`LayerIntf.cpp:7632-7636`, `7654-7658`) branches on it:

```cpp
if(IsGPU()) {
    InternalComplete2_GPU(updateregion.GetBound(), drawable);
} else {
    InternalComplete2(updateregion, drawable);
}
```

The GPU path (`LayerIntf.cpp:6849-6968`, `Draw_GPU`) walks the layer tree and,
for each layer, uploads its texture and composites it onto the target with GPU
shader render methods (`Copy`, `BoxBlur`, `AdjustGamma`, `UnivTrans`,
`AlphaTest`, `BlendColor`) defined in
`visual/ogl/RenderManager_ogl.cpp`. `tTVPOGLTexture2D` manages GPU textures with
`UpdateTextureData` / `glTexSubImage2D` partial updates. The CPU path
(`InternalDrawNoCache_CPU`, `LayerIntf.cpp:6971-7123`) is the fallback.

Compressed texture support (ASTC, ETC2/EAC, PVRTC) in
`visual/ogl/{astcrt,etcpak,pvrtc}.cpp` is a texture-storage optimization for
mobile GPU memory.

**Implication:** the Go and Rust ports ported the C++ fallback path. The
default C++ path composites on the GPU and has been exercised on mobile
hardware (Android/iOS via Cocos2d + OpenGL ES). CPU composition is not the
reference default; it is the reference fallback.

## anime4k is orthogonal to the composition path

anime4k is a fragment-shader post-processing pass (edge detection +
denoise + sharpen, the FFXQ/FFSR family). It operates on an already-composited
RGBA frame and does not depend on how that frame was produced. It slots into
`host/ebiten.go` `Draw` as `screen.DrawRectShader(g.imageWidth, g.imageHeight,
shader, &ebiten.DrawRectShaderOptions{...})`, replacing `DrawImage`.

Ebitengine v2.10.4 compiles one Kage/GLSL source to all backends via
`internal/shaderir/{glsl,hlsl,msl,pssl}` (OpenGL/Direct3D/Metal/PS5). No per-
platform shader variants are needed. `DrawRectShader` requires the source image
size to equal the rect size (`image.go:1097-1099`); upscaling is done by the
`GeoM` matrix, not by resizing the source image.

This is independent of the CPU-vs-GPU composition decision: anime4k can be
added on top of CPU composition today, and remains valid after a GPU
composition migration.

## Performance context

Measured on Darwin/arm64, 1280×720, `lovelyday` project
(see [performance.md](performance.md)):

- CPU composition averages ~9 ms/frame in the first interval, ~5.5 ms in the
  next, with static scenes near ~3.7 ms.
- The 60 FPS budget is 16.6 ms; the 120 FPS budget is 8.3 ms.
- Composition cost scales roughly linearly with pixel count:
  - 720p (921,600 px): ~9 ms → fits 60 FPS, exceeds 120 FPS budget.
  - 1080p (2,073,600 px): ~20 ms → exceeds both budgets.
  - 4K (8,294,400 px): ~80 ms → exceeds both budgets by ~4-10×.

The frame rate is engine-driven (Ebitengine runs `Update`/`Draw` at display
refresh). The runtime advances a fixed 16 ms tick per `Update`
(`ebiten.go:337`, `fixedTickMS = 16`), so game speed is currently bound to
refresh rate: ~0.96× at 60 Hz, ~1.92× at 120 Hz. Decoupling requires a
wall-clock accumulator; this is orthogonal to the composition path and
preserves replay determinism when fed a fixed elapsed time.

## GPU composition migration

The migration ports the C++ `Draw_GPU` path to Ebitengine. The layer-tree
traversal, blend-mode mapping, and dirty-region logic are reusable; the
per-pixel CPU loops become texture uploads + `DrawImage`/`DrawRectShader`.

| Area | Change | Risk |
| --- | --- | --- |
| `compositor.go` | CPU pixel loop → draw-list assembly | Low |
| `layer_draw.go` | per-layer pixel ops → texture upload + `DrawImage` | Low |
| `layer_transition_native.go` | CPU formulas → GPU blend + shader | **High** |
| `layer_native.go` | bitmap fields → texture references | Low |
| `services.go` | `CaptureFrameForPresentation` output → texture list | Low |
| `ebiten.go` | upload + `DrawImage` → multi-texture composition | Low |
| `bitmap_native.go` | decode stays CPU; storage may change | Low |
| `image_function_native.go` | text rasterization stays CPU | Low |

The reference is the C++ `Draw_GPU` path, which is already mobile-validated.
Text rasterization stays on the CPU (sfnt/opentype) and renders to a small
buffer uploaded as a texture; this is a hybrid path, not full GPU text.

### The hard part: transition GPU equivalents

Transitions are where the CPU fast paths (table lookup, per-pixel formulas)
have no direct GPU equivalent and where the Ebitengine Kage subset is most
likely to constrain. `Layer.light`/`AdjustGamma` and the crossfade blend have
straight mappings; the universal transition (rule-image reveal, `options.vague`
edge width) needs a custom shader driven by a rule texture, matching the C++
`tTVPOGLRenderMethod_UnivTrans`.

### Replay determinism

GPU rendering is not bit-exact across drivers. The C++ `ogl_accurate_render`
flag exists for exactly this reason. The Go port needs an equivalent internal
fallback to the CPU path for:

1. Replay validation (`krkrgo-tool replay` requires bit-exact frames; the
   headless path already bypasses Ebitengine and composites on the CPU).
2. Shader-compile failure fallback, when a transition shader exceeds the
   Ebitengine Kage subset.

This is an internal flag, not a user-facing setting, mirroring the C++
default-GPU / CPU-fallback structure.

## Recommendation

1. Add anime4k on the current CPU path first (`DrawRectShader` in `Draw`). It
   is independent of the composition decision and delivers 720p→1080p
   upscaling immediately.
2. Before committing to the GPU migration, translate the C++
   `tTVPOGLRenderMethod_UnivTrans` shader to Ebitengine Kage/GLSL and verify it
   compiles on the macOS Metal backend. This de-risks the highest-risk item
   (transition GPU equivalents) before the rest of the migration has meaning.
3. Migrate the remaining composition to GPU in phases, keeping the CPU path as
   the internal replay/fallback route.
