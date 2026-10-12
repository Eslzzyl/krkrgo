# krkrgo Continuous Execution Contract

## Goal and authority
- Keep the long-running migration active. The official C++ project at `/Users/eslzzyl/WorkSpace/Go/krkrgo/reference/krkrz` is the sole semantic and acceptance authority.
- The Rust project at `/Users/eslzzyl/WorkSpace/Go/krkrgo/reference/krkrz_rs` is an implementation reference and compatibility aid.
- The krkr2 project at `/Users/eslzzyl/WorkSpace/Go/krkrgo/reference/krkr2-main` is an additional implementation reference.
- The Go project at `/Users/eslzzyl/WorkSpace/Go/krkrgo` uses Ebitengine.
- Continue toward broad C++ alignment. Treat partial milestones as progress, never as project completion.

## Delegation
- All newly spawned subagents use `cline-pass/deepseek-v4.1-flash:high` through the current session's `task.agentModelOverrides`; do not write global model configuration.
- The total active subagent limit is two. Enforce it in dispatch and keep the session `task.maxConcurrency` at 2.
- Already-running agents retain their launch-time model and continue bounded work.
- Repetitive implementation and data collection go to subagents. The parent owns direction, contract decisions, code review, acceptance, integration, and necessary corrective edits.
- Subagents MAY run tests and builds when assigned.
- Formatting MAY be deferred; the parent runs the relevant Go formatter during review.
- Git commits are performed only by the parent; subagents MUST NOT commit.
- No network access, code downloads, or C++ reference builds.
- Filesystem search scope is explicit: subagents MUST limit reads and searches to directories named in the task, the Go project, `/Users/eslzzyl/WorkSpace/Go/krkrgo/reference/krkrz`, `/Users/eslzzyl/WorkSpace/Go/krkrgo/reference/krkrz_rs`, `/Users/eslzzyl/WorkSpace/Go/krkrgo/reference/krkr2-main`, named test-project directories, and explicitly declared module-cache paths. They MUST NOT recursively search the home directory or filesystem root.

## Authority and implementation policy
- Go behavior follows verified official C++ behavior. Rust behavior never overrides a conflicting C++ result.
- Reuse existing Go and pure-Go libraries when their behavior covers the requirement. Implement Krkr-specific semantics locally when a library cannot express them.
- macOS, Linux, and Windows are first-class desktop platforms and SHOULD remain CGO-free. Android and iOS remain supported; mobile CGO is permitted.
- New desktop dependencies MUST be pure Go. New dependencies require license, maintenance, API, platform-build, and semantic review.
- Migrated plugin behavior MUST be pure Go and cross-platform where portable; Windows-only native backends remain explicit capability gaps.

## Budget behavior
- Official C++ TJS execution has no global instruction budget, automatic VM timeout, or automatic VM yield.
- Normal Go launcher and replay startup use unlimited execution semantics by default.
- `-budget` and `--budget` are explicit diagnostic/test limits, not required startup parameters.
- Unlimited execution must propagate through Session, VM, nested native calls, KAG evaluation, and resource operations without a finite sentinel masquerading as the C++ default.
- Storage, allocation, pixel, and other resource limits remain separate from script execution budget.

## Verification
- Daily verification runs only the current Darwin arm64 platform: relevant tests, build, and `go vet` as applicable.
- Cross-platform builds run only for an explicit platform assessment, release check, or direct user request.
- Runtime and GUI verification is allowed when the user requests demo validation. CUA must be used for an interactive window and the process must not be left blocking unattended.
- Keep user-visible errors contextual: storage, line, column, tag, VM location, and the latest checkpoint when available.

## Documentation
- Repository documentation is written in English.
- `docs/` is split by domain. README stays focused on project entry points and high-level status; detailed implementation notes belong in the relevant domain document.

## Verified implementation facts
- `Window.focusedLayer` now has the C++-compatible native getter/setter path; Meta/Command input no longer closes the CAWorks process in the verified interaction.
- Default launcher execution is unlimited; explicit budget flags remain available for diagnostics and tests.
- XP3 reading supports bare archives and embedded MZ offsets. The pure-Go extraction tool verified the provided lovelyday archive set and extracted the startup tree.
- Script loading supports the provided project's mixed UTF-8, UTF-16, and nested Shift-JIS inputs. Explicit `Scripts.textEncoding` remains strict after assignment; implicit storage decoding uses compatibility detection.
- `@set(...)` preprocessing, `Array.load`, TJS `void` string conversion, and `\=` integer-division assignment are implemented with focused tests.
- `Layer.loadProvinceImage` reads indexed images, preserves palette indices, and tiles undersized images to the main-image dimensions as the C++ graphics loader does. The lovelyday `meswin_map.png` case is the current launcher checkpoint.
- Ebitengine presentation now requests native window dimensions when the runtime frame size changes. A fresh CUA screenshot of lovelyday's first frame shows crisp Japanese text and the prior blurry-font symptom is not present in the verified surface.
- `NewServices` now retains `Storage`, `SaveDir`, `Limits`, the discovered `FontBook`, and its bound `FontSystem`. `OpenSession` keeps the project root for project/data layouts, mounts data-directory XP3 archives alongside root archives, adds `data/` as a search path, and accepts executable-relative qualified archive names. Focused tests pass.
- The actual lovelyday launcher passed storage, root/data archive lookup, and font selection, displayed the first frame, then reached `logo.wmv` and exited with the typed Windows Media backend error.
- `System.getAboutString`, `WIN32Dialog.messageBox`, and `drawFFTGraph` now match the exact local krkr2 symbol names; their focused behavior is covered.
- TJS production optimization now includes direct UTF-16 equality in `internal/tjs/value.go`, direct UTF-16-unit member access with delayed fallback conversion in `internal/tjs/vm.go`, defensive-copy removal in `internal/tjs/utf16.go`, and the direct Go-string property-name key path in `internal/tjs/object.go`/`internal/tjs/vm.go`; focused semantic tests and darwin/arm64 benchmarks show the measured local gains documented in `docs/tjs-optimization.md`.
- TJS predecode/private opcode and HostTrace assertion-hoist candidates remain out of production: the former lacks a safe measured design for mutable `Program` values, and the latter regressed the measured VM benchmark.
- After the member-access and UTF-16 changes, `go test ./... -count=1`, `go build ./...`, and `go vet ./...` passed on darwin/arm64.
- `VM.handle` now returns a package-level sentinel for non-object/null values, preserving error text and VMAbortError wrapping while removing repeated error allocation; focused `errors.Is`, delete-source, benchmark, full test, build, and vet checks pass.
- The invoke review identified `expandArgs` and script-frame argument copying; the measured owned-slice implementation now covers this candidate, while broader scratch-buffer reuse remains unimplemented.
- TJS script-function OpCall now has an owned argument-slice path: `expandArgs` output is retained as the frame's read-only args slice, avoiding the second copy; native/method/public invoke paths remain unchanged. Forward/spread/rest/closure tests, call benchmarks, full test, build, and vet checks pass.
- The property-name key optimization removes repeated UTF16FromString/UTF16.Encode work from ordinary VM name lookup and mutation while retaining UTF16-unit APIs for Value keys and isolated-surrogate semantics. After gofmt, focused property/key tests, `go test ./... -count=1`, `go build ./...`, and `go vet ./...` pass on darwin/arm64; the direct name path still allocates its binary key string.
- The follow-up property-frame review made no production change. Existing getter/setter benchmarks remain at 23/32 allocations per call, and frame-scope reuse or lazy lexical storage needs a separate design preserving captured scopes, parameter binding, GC roots, receiver/context, reentrancy, budget/trace/source, and exception/finally behavior.
- Static Go/C++ comparison confirms the main remaining allocation gap: Go `invokePropertyFunction` creates a lexical-scope slice and map, setter input creates a one-element argument slice, and `run` allocates registers; C++ `ExecuteAsFunction` obtains a contiguous `TJSVariantArrayStack` block with reserved getter/setter slots and pointer/register argument transfer. A Go frame arena or compact/lazy scope design remains a future candidate; no safe stable production change was found.
- The lovelyday current-platform binary profile is renderer-dominated: `syscall.rawsyscalln` 28.54%, `runtime.blendPixelForTarget` 22.32%, `drawContext.drawLayerTo` 19.10%, `Layer.stretchCopyPixels` 2.30%, `vorbis.imdct` 1.50%, and `VM.run` 0.69%. Alloc-space is about 3.79 GB for the replay, with `os.readFileContents` 1.05 GB, `clearRGBA` 0.25 GB, and audio/resource sites smaller; non-rendering review produced no stable safe optimization.
- Renderer fast path in `internal/runtime/layer_draw.go` copies straight RGB/effective alpha for transparent destinations in `LayerAlpha` and `LayerAddAlpha`; exhaustive source-alpha/opacity tests pass. `BenchmarkComposeFullFrame` improved from 7.984–8.029 ms/op to 5.564–5.580 ms/op in the 1280x720 RGBA+Province fixture, with 2 allocs/op and byte-identical lovelyday replay output. Whole-replay timing showed no stable speedup, so the gain remains scoped to the compositor fixture.
- `clearRGBA` now uses geometric copy-fill for non-zero backgrounds and returns safely for zero-sized outputs. Exhaustive byte tests cover zero dimensions; the 1280x720 non-zero-color clear benchmark improved from about 868.9 to 203.5 microseconds/op in the agent's five-run comparison, with one allocation and about 3.686 MB/op unchanged. Parent's rerun measured 227–246 microseconds/op.
- `drawContext.drawLayerTo` now hoists the Province/image compatibility check and reuses row offsets. The parent verified the inner-loop benchmark at about 5.3–5.6 ms/op with 2 allocations and byte-identical full lovelyday replay output; the focused improvement was about 5.7% after the transparent-destination fast path.
- The official C++ window path uses an event-driven message pump, ComplexRect dirty-region updates, persistent GPU texture lock/copy/unlock, explicit Present, optional VSync timing thread, and independent media clocks. Rust `krkrz_rs` uses a winit 16 ms wall-clock gate, skips unchanged frame capture, uploads full frames to a persistent wgpu texture, and calls FIFO `frame.present`. Go now adds changed-bounds subimage upload, while full capture/premultiply scanning and Ebitengine presentation remain.
- The required krkr2 path `/Users/eslzzyl/WorkSpace/Go/krkrgo/reference/krkr2-main` is now available for direct read/glob inspection; implementation facts must cite files under this path.
- `internal/host/ebiten.go` now has an opaque-pixel conversion fast path and changed-bounds subimage upload below half-frame area. Darwin arm64 720p benchmarks are about 2.24–2.32 ms/op with zero allocations for unchanged opaque frames and 2.24–2.35 ms/op with 5–6 B/op amortized for a single-pixel change; the earlier baseline was 4.740 ms/op. Focused host tests, full tests, build, vet and byte-identical replay checks pass.

- lovelyday startup stops at `logo.wmv`: the Go runtime classifies ASF/WMV as `ErrVideoWindowsMediaBackendUnavailable` wrapped by `ErrVideoUnsupported`. Official C++ `krmovie` uses Windows Media/DirectShow; no pure-Go WMV/VC-1 decoder is present.
- Current testdata validation is the active workstream. `testdata/amairo` reaches the initial 1280×720 frame with empty replay input; fresh-save replay reaches 30 seconds without an error and reports `custom.ks:*caution`. The 10-second checkpoint reports `System.title="天色幻想岛 (V1.2016.02.07)"` and `typeof Window.mainWindow="Object"`. The stable frame hash remains `771fa16b358e3195e00973cb8baa582a84f3c38d9ab2218742169515211b9889`.
- Generic fixes in this path include Window rectangle dictionaries, `RegExp.match`, `Storages.getLocalName`, Window.drawDevice, VM static-property resolution for legacy TJS2100, and WindowEx/MenuItem installation.
- `testdata/islanddiary` now passes EXTRY, SUB, legacy `RegExp._compile`, ScriptsEx link/partial reflection, and the observed BGM class constant lookup. With temporary `/tmp/krkrgo-islanddiary-data/Config.tjs -> main/config.tjs`, replay loads AppConfig, Config, KAG startup, LayerEx.tjs, and `system/bgm.tjs`; the current exact blocker is compiled class object 1 initialization at legacy offset 15: `CALL Object: TJS object is not callable`. The preceding `property target is void` was caused by the missing ContextType=6 class materialization. Reference path semantics still leave the extracted `main/config.tjs` outside the selected project root.
- Storage offset handling now applies `oN` before bytecode detection and text decoding, matching C++ and Rust. Focused storage tests pass. A full current-platform test/build passed after RegExp, offset, and ScriptsEx changes; it must be rerun after the compiled-class test fix.
- The TJS2100 bridge currently covers the observed startup instructions through `TYPEOFD` and `NEW`, including register copies/clears, truth flags, conditional jumps, string/numeric conversion, addition, property access, calls, eval, construction, explicit script context, VM-owned script-function constants, primitive method callables, Array.load, SUB, and the newer control-flow/return/delete instructions. Legacy `EXTRY` falls through on the normal path; explicit `THROW` transfers to the nearest handler and unhandled throws retain the script exception. Additional execution coverage remains driven by direct startup errors.
- Parent commit `f22e596` (`Expand testdata runtime compatibility`) contains the accumulated pure-Go runtime/TJS/storage/native/test changes; `go test ./... -count=1` and `go build ./...` passed on darwin/arm64, and the worktree was clean immediately after commit.
- Launcher review confirms the launcher process/catalog path is healthy. Amairo reaches a real Ebitengine window, but CUA/runtime diagnostics show a non-empty compositor root and visible primary child whose 1280x960 RGBA is fully transparent black; host Update/Draw/WritePixels presents that frame normally. The remaining black-screen cause is in Amairo script/runtime layer population, not launcher or Ebitengine presentation.
- IslandDiary launched from its original project root currently fails because startup's compiled `Initialize.tjs` cannot resolve `Config.tjs`; the extracted archive contains `main/config.tjs`. A temporary root symlink advances execution through AppConfig, Config, KAG scripts, ScriptsEx, BGM classes, and generic class CALL, then reaches `DialogLayer.tjs` where `LinkEditLayerBase` is undefined because `EditLayer.tjs` is absent from the game's explicit load list. Cross-reference confirms no generic dependency scanner or implicit preload is part of official startup semantics.
- After every parent git commit, run `git push` immediately; the `f22e596` commit was pushed and the remote reported `Everything up-to-date`.
- Commit `4d21464` (`Add archive auto-path coverage`) adds focused storage and runtime tests proving `Storages.addAutoPath("main/")` resolves bare `Config.tjs` to the archive member `main/config.tjs`; the parent corrected the expected `getPlacedPath` archive URI, ran the focused tests successfully, committed, and pushed.
- Commit `9815533` (`Forward inherited TJS method receivers`) adds generic receiver forwarding for explicit global base-class member calls from derived instances, with a focused VM test; the full current-platform test suite and CGO-free build passed, and the commit was pushed.
- Post-fix Amairo CUA capture still showed a fully black real window. The synthetic `GraphicLayer.beginTransition` path now registers a native transition, invokes completion/`atEndOfTransition`, and calls `exchangeForeBack`; the natural `extra.ks:*page` route was not reached by the available probe, so this commit is not claimed as an Amairo black-screen fix.
- Game-specific adapters, script-name branches, or asset-specific fixes MUST NOT enter `internal/`; temporary probes may live only in explicitly named temporary paths and must be removed. Production changes must repair game-independent behavior justified by official/reference semantics. Unit or synthetic probes never count as delivery when the real game window still fails; real-window pixel/screenshot verification is required for a visual claim.
- Commit `95a6764` (`Implement legacy TYPEOF opcode`) adds generic TJS2100 in-place `TYPEOF` semantics, coverage for primitive/object/class/function values, and docs; full current-platform tests/build passed, the commit was pushed. Diagnostic explicit-main replay advanced IslandDiary past `append_init.tjs` TYPEOF to the next DialogLayer initialization error; production still does not inject `main/`.
- Commit `95606de` (`Implement lazy legacy superclass getters`) adds generic TJS2100 deferred superclass evaluation with reverse-precedence lazy caching, execution host/budget propagation, recursion/error context, class lookup, inherited receiver lookup, construction initialization, and compiled offset binder support. The focused and full current-platform tests plus CGO-free build passed, and the parent pushed the commit. IslandDiary's explicit-main diagnostic still stops at `DialogLayer.tjs` because `system/editlayer.tjs` is absent from the observed explicit load sequence; production does not inject it.
- Commit `74b0da8` (`Match reference layer neutral pixels`) aligns generic layer allocation/resizing pixels with the official C++ and Rust neutral colors, including transparent-white defaults and per-layer-type resize fills, with focused coverage. Current-platform full tests, CGO-free build, and vet passed; Amairo natural startup probes showed opaque all-zero main layers before this fix, but the real GUI still requires a fresh screenshot before any visual claim.
- Commit `e748b5d` (`Load layer image sidecars`) adds generic C++/Rust-compatible `_m` mask alpha replacement and `_p` province sidecar loading/tiling to `Layer.loadImages`, with focused tests. Current-platform full tests, CGO-free build, and vet passed; Amairo's inspected `bgimage.xp3` and `evimage.xp3` contained no sidecar assets, so this change is not claimed as the black-window fix.
- Island path audits confirmed Go archive/storage normalization and explicit auto-path lookup match the C++ and Rust behavior. `main/config.tjs` resolves after explicit `main/`, while the original startup still does not register that path; no generic path fix or implicit injection is justified. The parent reproduced the original failure: `Config.tjs が見つかりません`.
- Amairo post-fix natural probes remain static black: 0/5/8/12s frames have RGB zero and alpha 255 with the same hash. Temporary native-operation tracing found the first zero writes in script-issued full-frame `fillRect(..., color=0)` on the primary/top/visible child layers; C++ `LayerIntf.cpp` preserves that supplied zero fill, so allocation, `SetType`, and composition are not the cause. No generic rendering fix is justified; real GUI launch currently returns `ui: no monitor was found at initializeGLFW` in the shell environment.
- Commit `fcfbd25` (`Document layer neutral and sidecar support`) updates `docs/graphics-layer-text.md` for reference neutral pixels and automatic `_m`/`_p` sidecars; the parent pushed it.

## Session discipline
- The user has explicitly voided the previous interruption pause rule. User requirements that request execution MUST trigger the corresponding tool call immediately; an answer-only response is insufficient.
- When the user says continue, keep the project moving; do not turn an implementation mistake into a project pause.
- Keep goal and todo state consistent with current work. Remove obsolete blocked items instead of carrying historical blockers forward.
- Before continuing after a context change, reread this contract, the active goal, and todo state.

## Current session overrides
- The active subagent concurrency cap is 2. This cap applies to every new delegation and remains active after context compaction.

## Delegation history
- Earlier tasks used `cline-pass/mimo-v2.6-flash`; already-running agents retain their launch-time model.
- The latest user instruction selected `cline-pass/deepseek-v4.1-flash:high` for subsequent delegation and kept the concurrency cap at 2.

## Current work priority
- The current primary objective is a pure-Go WMV3 decoder and playback of `testdata/lovelyday/movie.xp3 > logo.wmv`.
- The secondary objective is the Amairo startup black-screen investigation and fix.
- Future delegation uses `cline-pass/deepseek-v4.1-flash:high` with no global configuration changes.
- The two-agent concurrency cap applies across all subagent types and must be enforced operationally.
- The WMV3 assessment document is present at `docs/wmv3-go-port-assessment.md`; the ASF and WMV3 implementation work remains subject to coordinator review and end-to-end playback verification.

## Session handoff
- The local `krkrgo-execution-contract.md` is migrated to `agents/krkrgo-execution-contract.md` in this project.
