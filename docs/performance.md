# Performance report

## Scope and current result

This report evaluates the Go application in a real window with the
`lovelyday` project on the Darwin/arm64 workstation. The target is smooth
startup and animation in that window. Headless replay and isolated benchmarks
are supporting data only.

For reproducible no-window measurements, see [Headless performance baseline](performance-headless.md).

The latest controlled run used:

```sh
go build -trimpath -o /tmp/krkrgo-transition-zeroopaque ./cmd/krkrgo
KRKRGO_RENDER_STATS=1 /tmp/krkrgo-transition-zeroopaque run -project /Users/eslzzyl/Games/lovelyday
```

The diagnostic log is `/private/tmp/krkrgo-transition-zeroopaque.log`. One process
and one real window were used; the command was terminated after the measured
startup and steady-state intervals.

The first visible transition interval reached 63.4 Update callbacks/s, but
only 25.7 Draw callbacks/s. Its averages were 14.57 ms for `Update`, 9.41 ms
for frame capture, and 2.04 ms for presentation preparation. The next
interval reached 61.0 Update callbacks/s and 90.0 Draw callbacks/s, with
10.32 ms Update, 6.85 ms capture, and 1.38 ms presentation preparation.
Later static intervals stayed near 60 Update callbacks/s and 120 Draw
callbacks/s, with about 4.0 ms Update and 3.7 ms capture.

The first Draw entry arrived 63.3 ms after the first Update entry, and the
first interval's largest Draw interval was 166.9 ms. Host-side first image
creation took 4.6 µs, pixel-buffer allocation took 105.4 µs, the first
`WritePixels` call took about 304 µs, and the configured initial window resize
was skipped. These timings place the measured startup hitch outside the
instrumented Go image allocation and upload stages. Ebitengine's public API
does not expose the platform swap boundary, so the remaining startup hitch
cannot be assigned to a specific native window or swap operation here.

The steady state is smooth at the measured callback cadence. The startup
transition still has visible frame pacing loss, so the current implementation
does not meet a 60-FPS startup target.

## Startup behavior

The first interval includes synchronous project work:

- `sys_source/Init.tjs` executes for about 620.5 ms before the first scene
  settles.
- The `caution` image read takes about 20.1 ms and decode takes about 15.1 ms
  in the measured run.
- The runtime transition diagnostic reports 60 transition attempts and zero
  full-screen opaque-presentation hits in the first interval. The endpoint
  alpha mode is `source=zero,destination=opaque`; the zero-to-opaque
  crossfade fast path is active.
- Runtime composition averages 9.37 ms in the first interval and 5.53 ms in
  the next interval. Validation and transition-tree discovery each remain
  below 10 µs per capture.

The C++ startup path creates an asynchronous image-loader thread before
startup scripts, but that facility serves `Bitmap.loadAsync`. Its
`Layer.loadImages` path remains synchronous. The Go path already runs
`Session.Startup` before `ebiten.RunGame`.
A generic pre-window `Tick` or frame capture would change input-before-tick
ordering, script `onPaint` timing, event dispatch, and transition state. The
current runtime keeps that work in the normal host lifecycle.

## Instrumentation

`KRKRGO_RENDER_STATS=1` records Update and Draw callback counts, runtime tick
time, frame capture time, presentation preparation time, and the Go
`DrawImage` callback. Ebitengine does not expose its platform `Present` or
swap-chain boundary through the current public API, so these logs do not claim
hardware refresh timing or end-to-end displayed-frame latency.

The host uses a fixed 16 ms runtime tick. `ActualFPS` and `ActualTPS` come
from Ebitengine and are included as the closest available real-window timing
signals.

## Single-stall trace

`KRKRGO_STALL_TRACE=1` enables thresholded frame, Draw, `Layer.loadImages`,
and `Bitmap.loadAsync` records. The default threshold is 20 ms; override it
with `KRKRGO_STALL_THRESHOLD_MS`. Records use structured key-value fields and
are written through the normal Go logger, so redirect stderr to preserve the
trace:

```sh
go build -trimpath -o temp/krkrgo-stall ./cmd/krkrgo
KRKRGO_RENDER_STATS=1 \
KRKRGO_STALL_TRACE=1 \
KRKRGO_STALL_THRESHOLD_MS=20 \
temp/krkrgo-stall run -project /path/to/project 2>temp/stall-trace.log
```

Frame records contain logical time, frame interval, Update, runtime tick,
audio, and presentation durations. With `KRKRGO_RENDER_STATS=1`, long runtime
ticks also report `advance_clock`, event dispatch, layer transitions,
`capture_frame`, and transition completion. Resource records split image work
into storage read, decode, color-key, mask, province, metadata, and Layer
commit stages. The trace is opt-in and emits records only after a stage or
frame interval crosses the threshold.

`KRKRGO_AUDIO_DIAGNOSTICS=1` extends the exit audio summary with output-queue
wait counts and durations. A wait after the first accepted block identifies
device-side queue starvation during a long runtime or presentation stage.

## Implemented optimizations

- `internal/runtime/layer_draw.go` clips drawing to the effective destination
  region, hoists compatibility checks, specializes alpha handling, and uses
  opaque transition row and full-frame copy paths.
- `internal/runtime/layer_transition_native.go` classifies transition endpoint
  alpha planes once. Fully opaque transitions use the opaque blend path; the
  common zero-to-opaque crossfade uses a specialized integer destination
  scaling path. Generic and universal transitions retain their existing
  formulas.
- `internal/runtime/compositor.go` reuses presentation scratch buffers, uses
  geometric copy-fill for RGBA clears, and skips the RGBA background fill when a
  conservative full-canvas opaque layer overwrites every pixel. The 1280×720
  Darwin/arm64 fixture measured 0.629 ms/op with the skip versus 0.712 ms/op
  with the equivalent fill; both measured paths report two allocations.
- `internal/runtime/services.go` provides borrowed presentation frames for the
  synchronous host path. Public `CaptureFrame` keeps clone ownership
  semantics. Render-state caching skips composition for unchanged static
  scenes, while transitions and videos remain dynamic.
- `internal/runtime/services.go` caches validated layer-tree checks by root
  revision, removing repeated full-tree map construction during capture while
  retaining validation after structural or render-affecting mutations.
- `internal/host/ebiten.go` reuses the upload buffer, avoids repeated
  premultiplication for opaque data, computes changed bounds during the
  upload scan, and selects subimage or full-image writes by changed area.
- The final Ebitengine canvas draw uses linear filtering, preserving smooth
  image edges when the logical canvas is presented at a fractional window scale.
- When an Ebitengine audio output is attached, the host mixer follows elapsed
  wall time between updates while the script clock remains on the fixed replay
  timeline, keeping PCM production aligned with a presentation stall.
- The live audio adapter caps each wall-time mix block to current queue
  headroom, preventing transient startup backpressure from terminating the
  host update loop.
- `internal/host/ebiten.go` compares changed rows with `bytes.Equal` when the
  previous presentation surface is opaque. Opaque dynamic frames now find
  changed bounds without a per-pixel scan of unchanged rows; a transition to
  transparency falls back to the premultiplication path.
- `internal/host/ebiten.go` shares one row-indexed premultiplication and
  changed-bounds scan between the first mixed-alpha frame and later
  transparent updates.
- `internal/runtime/layer_draw.go` copies straight-alpha pixels directly when
  the destination alpha is zero and the target is non-opaque. The fallback
  formulas remain unchanged for opaque destinations and mixed-alpha pixels.
- `internal/tjs/value.go` and `internal/tjs/vm.go` use a direct integer
  arithmetic, comparison, logical, and bitwise path in `OpBinary`.
- `internal/tjs/gc.go` reuses the mark map, pending-ID slice, host list, and
  sorted ID workspace on each VM instead of allocating those collector
  structures for every collection.
- `internal/runtime/session.go` transfers the pending `onPaint` ID slice into
  the capture pass instead of cloning the queue on every frame. Sorting,
  dispatch, and error requeue behavior remain unchanged.
- `internal/runtime/session.go` reuses pending-paint, video, sound, transition,
  and deferred-completion workspaces. Empty ID sets skip sorting.
- `internal/runtime/layer_draw.go` routes generic pixels directly to the
  general blend helper after the specialized opaque-alpha path has been
  selected, removing an unreachable per-pixel dispatch branch.
- `internal/tjs/compiler.go` emits static string member keys as opcode
  operands. `internal/tjs/vm.go` fetches instructions by pointer and writes
  validated common register operands directly; `internal/tjs/bytecode_exec.go`
  keeps Legacy instruction dispatch on pointers.
- `internal/assets/image_cache.go` and `internal/assets/xp3.go` use indexed
  intrusive LRU caches for decoded images and archive segments. Hit lookup and
  LRU movement no longer scan the full cache.
- `internal/assets/storage.go` and `internal/runtime/layer_load_images_native.go` resolve image sidecar candidates from one cached directory index and skip archive reads when the catalog has no candidate. The focused missing-candidate benchmark measured 13.174 µs/op versus 44.460 µs/op for repeated individual probes, with allocations reduced from 187 to 49 per operation.
- `internal/assets/storage.go` batches missing sidecar candidates across the
  configured loose and archive-qualified search paths. Search-path directory
  indexes are reused for the candidate set, and mounted archive identity is
  resolved once per search path before member probes.
- `internal/assets/media.go` returns an aligned straight-alpha `image.NRGBA`
  decode directly after transparent-pixel normalization, removing the second
  full-image copy for the common PNG decode shape.
- `internal/runtime/layer_image.go` unrolls the four RGBA channels in the
  bilinear `stretchCopy` inner loop while preserving the existing byte-parity
  behavior.
- `internal/runtime/layer_image.go` performs premultiplied-alpha interpolation
  for linear `affineCopy` samples, preserving transparent edge colors when
  animated source images are uploaded to Ebitengine's premultiplied textures.
- `internal/runtime/motion_render.go` applies each Motion.Player completion filter
  through the PSB motion compositor. Linear motion frames and the final motion
  canvas use premultiplied-alpha bilinear sampling, preserving transparent
  image edges while animated artwork is transformed.
- Motion PSB canvases, recursion workspaces, and the last rendered pose now
  reuse per-resource storage. Identity translations use direct pixel
  composition; opaque identity regions use row copies; transformed frames use
  an integer-weighted premultiplied-alpha sampler with independent destination
  rows processed across available Go workers. These paths reduce CPU work and
  per-frame allocation pressure.
- GPU presentation now carries normal-alpha Motion.Player source images and
  composed affine transforms as deferred render descriptors.
  `internal/host/gpu_compositor.go` caches source textures, composes each
  Motion target in a reusable GPU image with `GeoM` and the requested filter,
  then feeds that image through the existing layer blend path. Other Motion
  layer types retain the CPU compositor. CPU frame capture materializes the
  same descriptor through the existing compositor.
- `internal/runtime/motionplayer_runtime.go` reuses the GPU Motion pose descriptor
  while its resource, tick, filter, and transform are unchanged. The host GPU
  compositor packs all source views for one Motion node into a reusable atlas,
  so successive transformed draws share one backing texture.
- `internal/runtime/layer_image.go` recognizes pixel-aligned identity affine
  copies, including a taller destination canvas, and copies rows directly
  instead of running the bilinear sampler. The amairo pre-title path had been
  sending a 1280×720 source through that sampler into a 1280×960 destination
  on every frame.
- `internal/runtime/layer_transition_native.go` keeps crossfade and universal
  transition pixel blending out of GPU-capture ticks. The render plan already
  carries both endpoints, rule, and phase to the host compositor; CPU capture
  retains the original pixel blend.
- `internal/runtime/layer_image.go` uses a row-copy path when `stretchCopy`
  keeps source and destination dimensions identical, skipping interpolation
  setup for identity-size transfers.
- `internal/runtime/layer_load_images_native.go` overlaps large main-image
  decode with mask and province sidecar lookup. Small images retain the
  synchronous path, keeping goroutine overhead out of the common small-image
  case.
- `internal/runtime/bitmap_native.go` now decodes `Bitmap.loadAsync` through
  the per-Storage decoded-image cache. A completed background preload supplies
  the cached decode to a later `Layer.loadImages` call instead of repeating
  the image decoder on the session thread. Completion transfers the decoded
  RGBA ownership into the Bitmap, so the main-session handoff does not copy
  the full image.
- `internal/runtime/services.go` carries a stable render-state hash on static
  presentation frames. `internal/host/ebiten.go` uses it to skip repeated
  opaque-frame scans; the Windows amd64 benchmark reduced the stable
  1280×720 presentation path from 138.061 µs/op to 2.822 ns/op.
- `internal/host/gpu_compositor.go` draws native opaque layers and binder-target
  alpha layers directly into the current target, removing one full-frame
  ping-pong copy per layer. The Windows amd64 benchmark measured median
  `Compose` time reductions of 37.5% for opaque layers and 44.3% for alpha
  layers, with allocations reduced from 64 to 40 per operation.
- `internal/runtime/render_plan.go` marks repeated stable `RenderPlan`
  captures. `internal/host/gpu_compositor.go` reuses the current target for
  matching stable hashes. The Windows amd64 stable-plan benchmark measured
  5.435 µs/op to 2.987 ns/op for opaque plans and 5.303 µs/op to
  3.327 ns/op for alpha plans. Dynamic plans continue through the original
  `Compose` path.
- `internal/runtime/layer.go`, `internal/runtime/bitmap_native.go`,
  `internal/runtime/session.go`, and `internal/runtime/render_plan.go` track
  render-affecting Layer and Bitmap revisions. GPU RenderPlan capture reuses
  an unchanged static plan and uses revision tokens for layer image, Province,
  and deferred Bitmap stretch sources, so repeated frames skip full
  source-buffer hashing. CPU frame capture retains content hashing for
  caller-mutable pixel slices.
- `internal/runtime/layer_transition_native.go` uses a channel-specialized
  universal-transition kernel with row-free pixel indexing and direct RGBA
  arithmetic. The 1280×720 fixture measured about 3.03 ms/op versus
  5.05 ms/op for the former channel loop, with zero allocations.
- `internal/runtime/render_plan.go` caches hashes for immutable transition
  source, destination, and rule buffers, and derives the mutable transition
  image hash from phase metadata. The 1280×720 render-plan fixture measured
  about 0.51 µs/op versus 1.29 ms/op for full buffer hashing.
- `internal/host/gpu_compositor.go` clears the front target once per compose;
  shader paths replace the complete back target through the existing
  front-to-back copy.
- `runtime.FrameSnapshot.Opaque` carries the full-screen opaque-transition
  alpha assertion to the host, so the presentation path skips the redundant
  alpha classification scan.
- `internal/tjs/bytecode_decode.go` and `internal/tjs/legacy_intercode.go`
  cache Legacy instruction boundaries and offset-to-IP tables per decoded
  object. On Windows amd64, the full `testdata/amairo` headless replay changed
  from 15.535 s to 13.191 s for the 1 s checkpoint and from 126.510 s to
  61.061 s for the 10 s checkpoint. The 1 s and 10 s replay JSON SHA-256
  values remained identical.
- `internal/host/gpu_compositor.go` creates shader `uniforms` maps only for
  transition and generic shader layers. Native opaque/alpha layers now skip
  that temporary map; the Windows amd64 dynamic native-layer benchmark reduced
  allocations from 40 to 32 per operation and bytes from 6157 to 5830 for
  opaque layers. Alpha layers also reduced allocations from 40 to 32 and bytes
  from 6326 to 5954; timing stayed within benchmark noise.
- `internal/krkrgocli` passes the already configured initial window dimensions
  to `host.NewGameWithInitialWindowSize`. The host skips one redundant
  same-size resize and continues to resize on the first mismatch or later
  runtime resolution changes.
- Startup diagnostics split the first host surface, pixel buffer, upload
  buffer, subimage, and `WritePixels` stages when
  `KRKRGO_RENDER_STATS=1` is enabled.
- `internal/assets/image_cache.go` provides a bounded per-`Storage` decoded
  image cache. Its content-based key lets extensionless and explicit
  extension aliases share decoded data. Cache clearing invalidates entries,
  and callers receive owned pixel buffers.
- Full RGBA content hashing is opt-in through
  `KRKRGO_RENDER_CONTENT_HASH=1`; normal render diagnostics count frames
  without hashing every pixel.
- `internal/tjs/vm.go` mirrors VM-array mutations from the first changed index.
  `add` and `push` copy only appended values; shifts, removals, and reordering
  synchronize the affected suffix.

## Reference implementation comparison

- **C++ (`/Users/eslzzyl/WorkSpace/Go/krkrgo/reference/krkr2-main`):**
  `visual/GraphicsLoadThread.cpp:128-177` inserts a completed
  `Bitmap.loadAsync` decode into the graphic cache before dispatching
  `onLoaded`. `visual/GraphicsLoaderIntf.cpp:1878-1970` checks that cache
  before synchronous layer-facing graphic loads and can retain GPU textures.
  `visual/LayerIntf.cpp:2822-2915` still keeps `Layer.loadImages` synchronous.
- **C++ (`/Users/eslzzyl/WorkSpace/krkrz`):**
  `visual/LayerManager.cpp:1043-1086` and
  `visual/win32/DrawDevice.cpp:580-613` manage dirty regions and draw-device
  updates.
  `visual/win32/BasicDrawDevice.cpp:613-700` updates a persistent GPU texture
  with clipped rows; `visual/win32/BasicDrawDevice.cpp:542-577` presents it.
  `visual/win32/VSyncTimingThread.cpp:14-152` supplies optional VSync timing.
  Media playback has independent DirectShow/Media Foundation clocks.
- **Rust (`/Users/eslzzyl/WorkSpace/Rust/krkrz_rs`):**
  `crates/krkrz_cli/src/native_host.rs:105,148,477-491,525-586` uses a
  winit event loop, a wall-clock 16 ms gate, and redraw requests for changed
  frames. `crates/krkrz_cli/src/presenter.rs:81-173` uses a persistent wgpu
  texture, `queue.write_texture`, and `frame.present`. Runtime snapshot
  comparison in `crates/krkrz_runtime/src/layer/draw.rs:434-498` skips
  unchanged captures; transitions and video remain conservative.
- **Go:** `internal/host/ebiten.go` performs input, runtime ticking, frame
  capture, and upload preparation in Ebitengine `Update`, then submits the
  image from `Draw`. Opaque dynamic frames use row-level byte comparisons;
  mixed-alpha frames retain the premultiplication scan. Ebitengine owns actual
  presentation. There is no separate Go VSync thread.

These are source comparisons. This report contains no side-by-side C++ or
Rust real-window timing.

## Supporting measurements

The following measurements quantify isolated paths and are not acceptance
results:

- `BenchmarkBlendCrossfadeZeroOpaque1280x720` measured about 2.19 ms/op for
  the specialized path versus about 2.95 ms/op for the generic path on
  Darwin/arm64, with zero allocations.
- The `drawLayerTo` inner-loop fixture moved from a mean of about 5.606 ms/op
  to about 5.289 ms/op, approximately 5.7% faster.
- `BenchmarkClearRGBA720p` moved from about 868.9 µs/op to about 203.5 µs/op
  after geometric clear filling.
- The Darwin/arm64 host fixture measured unchanged opaque frames at about
  2.24–2.32 ms/op with zero allocations; a single-pixel change measured about
  2.24–2.35 ms/op.
- Final Darwin/arm64 focused run after the row-level opaque upload path measured
  `BenchmarkUpdatePresentation720pSinglePixelChange` at 201.8 µs/op versus
  2.119 ms/op before the change, a 90.5% reduction. The benchmark remained at
  zero allocations.
- The transparent-destination compositor fixture measured 4.780 ms/op after
  the direct alpha-over-transparent path versus 8.195 ms/op before it, a
  41.7% reduction with two allocations per operation.
- `BenchmarkVMRunPhases/repeated-run` measured 275.3 µs/op after the direct
  integer opcode path versus 277.1 µs/op before it, a 0.6% reduction.
  Allocation volume remained 940,072 B/op and 2,518 allocs/op in this
  workload.
- An older replay comparison showed 10.08 s after versus 9.97 s before and
  did not establish a whole-replay speedup.
- Latest Darwin/arm64 VM dispatch measurements are about 107.7 µs/op with
  11,440 B/op and 907 allocs/op for `VMRunPhases/repeated-run`. The direct
  binary-register path was about 114 µs/op immediately before that change.
- `VMStaticMemberAccess` measured about 2.31 µs/op, 2,289 B/op, and
  12 allocs/op for static keys versus about 2.94 µs/op, 2,315 B/op, and
  15 allocs/op for dynamic keys.
- `VMPropertyAccess` measured about 4.47 µs/op, 4,400 B/op, and 31 allocs/op
  after common member operations switched to direct validated register access.
- Mixed-alpha `BenchmarkUpdatePresentation720pTransparentSinglePixelChange`
  measured about 5.4 ms/op with zero reported allocations after the initial and
  subsequent transparent paths were unified into one row-indexed scan.
- 4,096-entry indexed cache fixtures measured about 91 ns/op with one
  allocation for decoded-image lookup and about 17.8 ns/op with zero
  allocations for XP3 segment lookup.

## Remaining measured scope

All executable high-value paths covered by the current public runtime and host
contracts have an implementation and focused package verification. The items
below are explicit scope or measurement limits:
- The amairo pre-title trace still contains application-level long ticks after
  the identity-copy fix: a 28.378 ms `attention.png` decode and 22–25 ms timer
  callbacks on the title startup path were measured separately from GPU
  composition. These stages require their own script/resource scheduling
  measurement before a semantic change.

- `Bitmap.loadAsync` has an implementation; `Layer.loadImages` remains
  synchronous, and the accepted headless corpus has no independent startup
  resource-switch workload for measuring prefetch.
- Dynamic transitions produce a full transition pixel plane on each tick;
  dirty-region reuse requires unified layer invalidation metadata.
- The Go host captures each dynamic frame. Opaque frames use row-level bounds;
  full-screen opaque transitions carry an alpha assertion, while mixed-alpha
  frames require their full premultiplication scan.
- Ebitengine owns platform presentation, GPU queueing, and display refresh;
  these timings are outside the public API used here.
- No equivalent real-window C++ or Rust run has been measured on this
  workstation.

## WMV status

WMV/ASF remains a separate capability gap. The Go path currently keeps an
empty `VideoOverlay` placeholder without decoded video frames or audio. The
WMV path is excluded from the animation measurements above.
