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

Status: pending.

- Add `--memprofile` and `--stats` to `krkrgo-tool replay`.
- Add `VM.MemoryStats()` for object, member, key, UTF-16, octet, array, metadata,
  registry, and GC workspace counters.
- Add `Session.MemoryStats()` for Services maps and layer buffers.
- Add `GPUCompositor.Stats()` for texture, atlas, upload, and target bytes.
- Record amairo, islanddiary, and lovelyday baselines with replay SHA, wall clock,
  heap profiles, and process working set.
- Add retention benchmarks for VM creation, session close, image load, and member
  access.

Foundational instrumentation; dedicated tests are deferred per the current
execution instruction.

## Phase 1: registry and VM lifetime

Status: pending.

- Attach an owner scope to callable, class, instance, and property registry entries.
- Drop registry entries when VM GC removes object IDs.
- Add `VM.Release()`; call it from `Session.Close()` before clearing the VM.
- Remove the unused `programClasses` name lookup and the mismatched
  `Class.Name`/`Program.Storage` key path.
- Keep `NewClassWithBases` as the explicit base-class API.
- Clear `ObjectTable.instanceClasses` on object removal.
- Verify repeated session creation and close leaves registry counts at baseline.
- Verify two VMs with the same class name remain isolated.

## Phase 2: runtime resource reclamation

Status: pending.

- Add optional `HostObjectsCollected` hook invoked after GC removes object IDs.
- Clear object-keyed `Services` maps from the hook.
- Add `LayerResourceSink` and release GPU textures, motion targets, and motion
  atlases when layers are deleted or collected.
- Keep finalization and GC cleanup idempotent; invoke the hook outside VM locks.
- Audit every object-keyed map for an owner and a deletion path.

## Phase 3: GPU and image memory

Status: pending.

- Remove the per-texture upload buffer; reuse one compositor scratch buffer.
- Keep motion atlas uploads until the atlas growth path copies existing views on
  the GPU instead of rebuilding from retained CPU pixels.
- Add ownership-transfer APIs for layer images and provinces; copy only when
  render scale is greater than one.
- Borrow immutable motion source pixels when `scale == 1`.
- Evaluate transition buffer ownership after profiling.
- Verify upload bytes stay bounded and texture counts follow released layers.

Display-only follow-up: pixel-level GPU readback, VRAM accounting, present/VSync
timing, and interactive GPU frame time. Recorded, not started.

## Phase 4: TJS object and member layout

Status: pending.

- Remove the duplicate UTF-16 key from `member`; derive enumeration keys from the
  binary map key.
- Lazily allocate member maps for objects without members.
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

Status: pending.

- Release numeric decode pools after constant materialization.
- Reconcile string and octet pool storage with constant values.
- Replace per-object `offsetToIP` maps with sorted offsets and binary search when
  profiling shows a meaningful share.
- Add a runtime-only source compilation mode that retains trace data when trace is
  enabled.

## Phase 7: GC and arrays

Status: pending.

- Maintain an append-only object ID order and drop the per-collection sort.
- Report reclaimed bytes per collection.
- Evaluate sparse and numeric array representations from workload statistics.
- Defer generational collection until lifetime data supports it.

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
| pending | Plan document |
