# Memory Optimization Plan

Status: in progress.

This plan is derived from the current source tree. `docs/tjs-memory-optimization.md`
is treated as a reference checklist, not as the task definition. The ordering below
reflects measured code structure: TJS lifetime issues, runtime resource retention,
GPU-side CPU copies, object layout, transient allocation, decoded metadata, and GC
ordering.

## Status legend

- Done: implemented and committed.
- Pending: implementation work remains.
- Display-only: recorded here; requires a real or virtual display and is not started.

## Code audit summary

| Finding | Location | Effect |
| --- | --- | --- |
| `Object()` native construction registers two callables with closures capturing the VM; no registry deletion path exists | `internal/runtime/services.go:2162`, `internal/runtime/services.go:2182`, `internal/tjs/classes.go:53` | Registry growth per construction; closed sessions stay reachable |
| `programClasses` is written with `Class.Name` and read with `Program.Storage` | `internal/tjs/classes.go:99`, `internal/tjs/classes.go:149` | Key mismatch; only tests exercise this path |
| Each GPU texture retains a full premultiplied RGBA upload buffer | `internal/host/gpu_compositor.go:194` | CPU-side texture memory doubles |
| Motion atlas entries retain upload buffers until `Deallocate` | `internal/host/gpu_compositor.go:262` | Retention grows with distinct motion sources |
| `scaleRGBA` copies even when `scale == 1` | `internal/runtime/layer.go:357` | Extra full-image allocation during loads and motion draws |
| Transition state keeps three device-sized RGBA buffers per layer | `internal/runtime/layer_transition_native.go:230` | About 10.5 MiB per 1280x720 transitioning layer |
| Numeric decode pools survive constant materialization | `internal/tjs/bytecode_decode.go:64` | Retained arrays without consumers |
| GC sorts every object ID on every collection | `internal/tjs/vm.go:379` | O(n log n) pause growth |
| Object-keyed `Services` maps rely on explicit finalization | `internal/runtime/services.go:69` | Entries can outlive script references |

## Phase 0: measurement baseline

Status: done for replay measurement.

Completed:

- `krkrgo-tool replay --memprofile` writes an in-use heap profile.
- `krkrgo-tool replay --stats` writes exact `runtime.MemStats` after a forced GC.
- `GPUCompositor.Stats()` reports texture, atlas, upload-scratch, and target bytes.
- amairo 10-second replay baselines recorded for `2ff712c` and current commits.
- Per-commit attribution recorded below.
- `BenchmarkVMMemberAccess` and `BenchmarkGPUComposeNativeLayers` compared.

Remaining: `VM.MemoryStats()` and `Session.MemoryStats()` counters; islanddiary and
lovelyday baselines; process working-set sampling.

### Measured results

Method: amairo, deterministic replay at 10,000 ms, 16 ms step, budget `1<<30`,
three runs per revision. Statistics are taken after `runtime.GC()` and before
`Session.Close`. All runs produced the same replay JSON SHA-256.

| Metric | Baseline `2ff712c` | Current `9d110f2` | Change |
| --- | ---: | ---: | ---: |
| HeapAlloc | 743.86 MB | 737.81 MB | -6.05 MB (-0.81%) |
| HeapObjects | 505,485 | 436,482 | -69,003 (-13.65%) |
| TotalAlloc | 4,308.35 MB | 4,273.61 MB | -34.75 MB (-0.81%) |
| Mallocs | 5,352,160 | 5,156,097 | -196,063 (-3.66%) |
| HeapSys | 1,658.16 MB | 1,610.87 MB | -47.28 MB (-2.85%) |

Per-commit attribution, single run each:

| Revision | HeapAlloc | HeapObjects | Note |
| --- | ---: | ---: | --- |
| `2ff712c` | 743.86 MB | 505,485 | baseline |
| `3573662` | 744.03 MB | 505,560 | registry lifetime; no measured heap delta in this replay |
| `50b9e4b` | 737.66 MB | 436,437 | member key removal and lazy maps: dominant saving |
| `5a6c021` | 737.59 MB | 436,563 | image ownership: TotalAlloc -21.4 MB |
| `822b9e8` | 737.58 MB | 436,537 | decoded pools: within noise |
| `7044208` | 737.85 MB | 436,533 | GC order: within noise |
| `04c0b38` | 737.86 MB | 436,503 | bitmap ownership: within noise |
| `9d110f2` | 737.81 MB | 436,482 | VM-owned Object helper callables: within noise |

Benchmarks:

- `BenchmarkVMMemberAccess/dynamic-add-delete`: 48 B/op and 3 allocs/op
  before; 32 B/op and 2 allocs/op after.
- Other member access cases keep their B/op.
- `BenchmarkGPUComposeNativeLayers`: B/op and ns/op unchanged. The upload
  saving is retained footprint, not per-compose work; the scratch test covers it.
- `BenchmarkVMHandle/valid-object` varied between runs in both revisions; no
  stable conclusion.

Structural tests added and passing:

- `TestVMDispatchRegistryEntriesFollowCollection`
- `TestVMReleaseDropsDispatchRegistry`
- `TestGPUCompositorStatsTrackUploadScratch`

Dominant retained allocations in the 10-second profile, outside the completed
items:

| Flat | Allocation |
| ---: | --- |
| 286.24 MB | system font discovery (`os.readFileContents`, `ScanSystemFonts`) |
| 209.88 MB | source compiler instruction emission (`compiler.emit`) |
| 53.24 MB | `runtime.resizeLayerImage` |

These are recorded as the next measurement-backed targets.

## Phase 1: registry and VM lifetime

Status: done.

Completed:

- Registry entries carry an owning object table; GC removal drops matching entries.
- `VM.Release()` drops VM script state and registry entries; `Session.Close()` calls it.
- The mismatched `programClasses` name lookup was removed; `NewClassWithBases` is the explicit base API.
- `ObjectTable.Remove` clears instance-class bookkeeping.

Remaining: cross-VM class isolation test; repeated-session heap baseline.

## Phase 2: runtime resource reclamation

Status: pending.

- Add optional `HostObjectsCollected` hook invoked after GC removes object IDs.
- Clear object-keyed `Services` maps from the hook.
- Add `LayerResourceSink` and release GPU textures, motion targets, and motion
  atlases when layers are deleted or collected.
- Keep finalization and GC cleanup idempotent; invoke the hook outside VM locks.
- Audit every object-keyed map for an owner and a deletion path.

## Phase 3: GPU and image memory

Status: in progress.

Completed:

- Per-texture upload buffers were removed; one compositor scratch buffer serves all uploads.
- `GPUCompositor.Stats()` reports logical texture, atlas, upload-scratch, and target bytes.
- Layer, bitmap, and province ownership-transfer APIs adopt decoded buffers at scale 1.
- Motion draw plans borrow immutable source pixels at scale 1.
- Motion atlas growth copies existing views on the GPU; entries retain no CPU pixel buffers.

Remaining:

- Evaluate transition buffer ownership after profiling.
- Verify upload bytes stay bounded and texture counts follow released layers.

Display-only follow-up: pixel-level GPU readback, VRAM accounting, present/VSync
timing, and interactive GPU frame time. Recorded, not started.

## Phase 4: TJS object and member layout

Status: in progress.

Completed:

- `member` no longer stores a second UTF-16 key; enumeration derives keys from the binary map key.
- Member maps allocate lazily on first insertion.

- Evaluate a split `vmObject` core/ext representation only after stats confirm
  simple-object overhead dominates.
- Defer shape/slot class representation until workload data supports it.

## Phase 5: transient execution allocation

Status: pending.

- Skip scope maps for functions with no locals.
- Reuse argument buffers per call depth with GC-root visibility.
- Add internal read-only UTF-16 and octet accessors.
- Use borrowed arrays for internal iteration.
- Keep the public copying accessors.
- Do not repeat measured regressions: register-initialization removal, source
  compilation cache, compact instruction-name storage.

## Phase 6: decoded and compiled metadata

Status: in progress.

Completed:

- Numeric and octet decode pools are released after constant materialization.

- Reconcile string pool storage with constant values.
- Replace per-object `offsetToIP` maps with sorted offsets and binary search when
  profiling shows a meaningful share.
- Add a runtime-only source compilation mode that retains trace data when trace is
  enabled.

## Phase 7: GC and arrays

Status: in progress.

Completed:

- VM allocation order is maintained directly; GC no longer sorts all object IDs.

- Report reclaimed bytes per collection.
- Evaluate sparse and numeric array representations from workload statistics.
- Defer generational collection until lifetime data supports it.

## Phase 8: measurement-backed follow-ups

Status: pending.

- Load system fonts lazily. `NewServices` calls `DiscoverSystemFonts`, which reads
  and parses every font file; the 10-second profile retains about 286 MB.
- Reduce `compiler.emit` metadata. The profile retains about 210 MB across script
  compilation; this exceeds all completed TJS layout savings.
- Inspect `resizeLayerImage` retained bytes (about 53 MB) and its relation to
  layer attachment and render scale.

## Headless verification matrix

| Area | Headless | Notes |
| --- | --- | --- |
| TJS heap, registry, GC, session lifetime | Yes | `go test` and replay |
| Services maps, layers, bitmaps, sounds, videos | Yes | Replay drives KCP/KAG paths |
| Image decode, color key, mask, province | Yes | CPU path |
| CPU transitions and composition | Yes | `Session.Frame` |
| GPU compositor stats and buffer ownership | Yes | Offscreen `GPUCompositor` |
| GPU pixel output | Conditional | Requires graphics context; Xvfb or desktop session |
| VRAM and present timing | No | Real device required |
| Interactive GPU frame time | No | Real window required |
| Real audio and input devices | No | Device required |

## Verification commands

```sh
go test ./internal/tjs ./internal/runtime ./internal/assets ./internal/host
go run ./cmd/krkrgo-tool replay --project testdata/amairo --input replays/input.json --output temp/replay-result.json
go test ./... -run '^$' -bench 'BenchmarkVM|BenchmarkLayer' -benchmem
```

## Progress log

| Commit | Scope |
| --- | --- |
| 2ff712c | Plan document |
| 3573662 | Registry scope and VM release |
| 8861e2c | GPU upload scratch |
| 4d3034f | Replay heap profile flag |
| 50b9e4b | Member key and lazy map |
| 5a6c021 | Image buffer ownership |
| 822b9e8 | Decoded pool release |
| 7044208 | Allocation-order GC |
| e21e25d | Motion atlas GPU copy |
| 04c0b38 | Bitmap buffer ownership |
| 0468944 | Exact replay memory statistics |
| 9d110f2 | VM-owned Object helper callables |
