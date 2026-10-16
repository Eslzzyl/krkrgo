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
- `internal/host/ebiten.go` reuses the upload buffer, avoids repeated
  premultiplication for opaque data, computes changed bounds during the
  upload scan, and selects subimage or full-image writes by changed area.
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
- `internal/runtime/bitmap_native.go` decodes `Bitmap.loadAsync` on a
  background goroutine and posts a bounded main-session completion event;
  generation checks discard completions after object finalization.
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

- **C++ (`/Users/eslzzyl/WorkSpace/krkrz`):**
  `environ/win32/Application.cpp:347-366,536-582` creates an asynchronous
  image-loader thread for `Bitmap.loadAsync`, runs startup scripts, and uses
  an event-driven message loop. `Layer.loadImages` remains synchronous.
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
