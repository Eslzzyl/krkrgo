# Frame Upscaling

`krkrgo` supports optional 2× frame post-processing after GPU composition. The
feature is disabled by default and selected with `-upscaler`.

```sh
krkrgo run -project <dir> -render-scale 2 -upscaler bilinear
krkrgo run -project <dir> -render-scale 2 -upscaler bicubic
krkrgo run -project <dir> -render-scale 2 -upscaler anime4k
```

`-anime4k` is an alias for `-upscaler anime4k`.

## Modes

| Mode | Backend | Cost | Notes |
|---|---|---|---|
| `off` | — | none | Default; existing native path |
| `bilinear` | Ebitengine `DrawImage` | one scaled draw plus edge strips | Linear 2× scaling |
| `bicubic` | Kage shader | one full-frame pass, 4×4 taps | Catmull-Rom 2× scaling |
| `anime4k` | Kage shader chain | five full-frame passes | Anime4K Fast/S CNN |

All enabled modes require the GPU renderer and a resolved render scale of `2`.
The runtime composes at 1× and the host presents the 2× result. `-render-scale`
values other than `2` are rejected for these modes.

## Pipeline

1. The runtime composes the frame at 1×.
2. `GPUCompositor` returns the composed `*ebiten.Image`.
3. `Game.composeGPUPlan` applies the selected `PostProcessor`.
4. The 2× result is cached by `RenderPlan.StateHash`.
5. `Game.Draw` presents the 2× image; input coordinates are scaled by
   `Game.outputScale`.

The host keeps the runtime render scale at `1` while the presentation scale is
`2`. `Game.SetOutputScale(2)` updates both window sizing and `InputPoller.scale`,
so mouse and touch coordinates continue to map to the runtime's 1× layer
coordinates.

## PostProcessor Interface

```go
type PostProcessor interface {
    Process(src *ebiten.Image) (*ebiten.Image, error)
    Reset()
    Deallocate()
}
```

Implementations own their output texture. `Process` reuses preallocated
textures, vertices, and draw options when the source size is unchanged.

## Bilinear

`internal/host/scalers.go` draws the source to a 2× target with linear
sampling. A second set of edge strips copies the source's outer rows and columns
to the output border, keeping opaque frames opaque at the edges.

## Bicubic

`internal/host/scalers.go` implements a Catmull-Rom 4×4 kernel in Kage:

- Destination pixel `(X, Y)` maps to source coordinate `(X / 2, Y / 2)`.
- Four source taps per axis are weighted with the Catmull-Rom kernel.
- Sampling is clamped to the source bounds.
- Edge strips are copied to the output border, matching the bilinear edge fix.

## Anime4K Fast/S

`internal/host/anime4k.go` implements the upstream Anime4K Fast/S network as
five Kage passes:

| Pass | Operation |
|---|---|
| 1 | 4×3×3×3 convolution |
| 2 | 4×3×3×8 convolution with ReLU split |
| 3 | 4×3×3×8 convolution with ReLU split |
| 4 | 4×3×3×8 convolution with ReLU split |
| 5 | Depth-to-Space 2× and source blend |

Kage offscreen textures are RGBA8. Feature values are encoded with an offset
range of `[-2, 2]`, giving a quantization step of about `0.0157`. Pass 2–4
decode the previous pass, apply positive and negative ReLU branches, and encode
the result.

The final pass maps destination `(X, Y)` to source
`(floor(X/2), floor(Y/2))` and selects the feature channel with
`(X mod 2) + (Y mod 2) * 2`. The selected channel is added to a bilinear sample
of the original frame.

The processor allocates two 1× feature textures and one 2× output texture. It
reuses vertices, indices, and draw options across frames. `DrawTrianglesShader`
still has fixed internal Ebitengine submission allocations; the host code adds
no per-frame maps or slices.

## Cache and Fallback

- The 2× output is keyed by `RenderPlan.StateHash`.
- A stable hash reuses the cached output and skips all shader work.
- A source-size change reallocates the feature and output textures.
- Shader compilation failure, processing failure, or GPU initialization failure
  disables the upscaler and restores native scaling.
- `Reset` releases intermediate and output textures while keeping compiled
  shaders.

## Limitations

- GPU renderer only. `-renderer cpu` with an enabled upscaler is rejected.
- Fixed 2× output. `3×` and `4×` are not supported by these modes.
- Whole-frame processing includes text and UI.
- No temporal history, motion vectors, or per-layer processing.
- Anime4K uses RGBA8 intermediates; upstream uses FP16 and may produce slightly
  different results.
- Ebitengine does not expose native FP16 offscreen textures.

## Licensing

`internal/host/ANIME4K_LICENSE` contains the MIT notice for the Anime4K Fast/S
weights and algorithm. Magpie is GPL-3.0 and is not copied into this repository.

## Verification

```sh
go test ./internal/host
go test ./internal/krkrgocli
```

The host tests cover shader compilation, output size, reset, nil input, and
scaler mode dispatch. Cross-platform verification uses:

```sh
GOOS=darwin GOARCH=amd64 go build ./...
GOOS=windows GOARCH=amd64 go build ./...
GOOS=linux GOARCH=amd64 go build ./...
```
