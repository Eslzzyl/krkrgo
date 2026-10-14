# krkrgo Handoff

Status: 2026-10-14
Repository: `/Users/eslzzyl/WorkSpace/Go/krkrgo` (macOS) and `D:/WorkSpace/Go/krkrgo` (Windows)

## Current objectives

1. Prioritize repairing `testdata/islanddiary` startup and continue generic TJS2100 instruction migration.
2. Complete instruction coverage, run focused tests and full build checks, then re-run the IslandDiary startup command.
3. Deferred follow-up: after the TJS2100 migration and startup verification are complete, rename krkrgo's internal `legacy` files, symbols, comments, tests, and documentation to the official `InterCode`/`TJS2100` terminology. Preserve behavior during the rename.
4. Continue the Amairo startup black-screen investigation.
## IslandDiary startup status

The launcher was left unchanged. The latest targeted startup command was:

```text
go run ./cmd/krkrgo/main.go run -project ./testdata/islanddiary/
```

Startup now preloads the project `system/win32dialog.tjs` script and installs a portable `WIN32Dialog` native registry before loading `startup.tjs`. The current blocker is:

```text
Scripts.execStorage storage "uimain.tjs"
legacy intercode object 0 code offset 281: CALLD "add" lookup
TJS member not found
```

The preload path proves that `WIN32GenericDialogEX` is now defined. The remaining missing surface is the `add` operation used during `uimain.tjs` class initialization.

The startup path now includes:

- project `system/win32dialog.tjs` loading through the existing storage decoder;
- portable `WIN32Dialog` constants for the initial dialog/control styles;
- nested `Header`, `Items`, `Bitmap`, `SolidBrush`, `DrawItem`, `Notify`, and `Blob` native class values.

The Rust reference for this surface is `reference/krkrz_rs/crates/krkrz_runtime/src/dialog.rs` with data in `reference/krkrz_rs/crates/krkrz_runtime/data/win32dialog.json`. The current registry migration is partial; `add` remains unresolved during `uimain.tjs` initialization.

Latest targeted startup command:

```text
go run ./cmd/krkrgo/main.go run -project ./testdata/islanddiary/
```

The command loads `Initialize.tjs`, `Override.tjs`, `system/win32dialog.tjs`, and `uimain.tjs`, then exits with the missing `add` member above.


Completed during this session:
- GPDS/GPIS now preserve `IgnoreProperty` and can read raw property descriptors.
- Nested legacy function/class properties replay onto their loader parent.
- Legacy intercode registers are published to VM GC tracing during nested source execution.
- Compiled function/class/property objects and valid decoded object constants are rooted for script-block lifetime.
- `Scripts.setCallMissing` registers the missing-member protocol flag.
- TJS eval callbacks use `CompileEval`, allowing statement scripts such as generated property declarations.
- ADDCI/REGMEMBER accepts any valid dispatch object, matching the official `ClassInstanceInfo` dispatch entry.
- Dispatcher-created string-split arrays are registered in the active object table.
- Legacy member resolution falls back to the dispatcher for dynamic members and primitive receivers.

Focused and full verification passed after the latest implementation steps:

```text
go test ./internal/tjs ./internal/runtime -count=1
go test ./... -count=1
go build ./...
```

The worktree is clean except for this handoff update. Continue by implementing the real `WIN32GenericDialogEX` plugin surface or loading its project script before `uimain.tjs`; preserve the existing Windows plugin semantics.

## GPU composition status

The interactive Ebitengine path now defaults to GPU composition, with `-renderer gpu|cpu` on `krkrgo run`. The CPU compositor remains available through `NewCPUGame`, GPU initialization fallback, and headless replay.

The GPU composition logic may still be incomplete relative to the CPU semantic reference. Animation and transition coverage received follow-up fixes, but broader visual parity remains unverified across project content and supported layer combinations.

GPU composition performance has not been measured. No benchmark, frame-time comparison, memory profile, or driver/platform comparison is currently available.

Recent automated checks passed:

```text
go test ./... -count=1
go build ./...
```

Kage shader sampling uses the v2.10 `imageSrcNAtFromSrc0Pos` APIs, and GPU resource release uses `Deallocate`.

No subagents are currently running.

## Commits

- `538f559 Update documentation execution contract`
  - Added `agents/krkrgo-execution-contract.md`.
  - Removed all `CGO_ENABLED=0` occurrences from repository Markdown documentation.
- `febd39c Fix Windows storage paths and the Amairo opening sequence`
  - Windows drive-qualified storage names, `Layer.affineCopy`, `callOnPaint`, and the TJS2 `with` scope rules; see "Amairo status" below.

- `747dc38 Support TJS string count and indexing`
  - Adds UTF-16 `count` and numeric string properties to VM and Dispatcher paths with focused coverage.
- `b22bec7 Materialize legacy collapsed arguments`
  - Implements TJS2100 `CollapseBase` arrays in legacy frame setup with focused coverage.
- `3cdcb5a Preserve missing errors in legacy base lookup`
  - Keeps `MemberMustExist` on the global fallback for legacy `extends` resolution.
## Verification completed

The current Go test suite passes on the development host:

```text
go test ./...
```

## Amairo status

Verified on the Windows host (`D:/WorkSpace/Go/krkrgo`):

```text
go build -o temp/krkrgo.exe ./cmd/krkrgo
./temp/krkrgo.exe run -project testdata/amairo
```

Earlier Amairo notes describe the macOS state. On Windows the game exited before creating a window, so three separate blockers had to be fixed before the opening sequence ran at all. Each one is now implemented:

1. `unsupported storage media: d` from `startup.tjs` → `Initialize.tjs:21` (`Storages.getPlacedPath`). `assets.Storage` parsed a drive-qualified Windows path as a URI media name (`D:` → media `d`). Added `core.LocalStorageName` (encode) and `core.LocalStorageNativePath` (decode) implementing the reference spelling from `reference/krkr2/cpp/core/base/impl/StorageImpl.cpp:430`: `C:\games\x` ⇄ `file://./c:/games/x`. `storageMediaSeparator`, `storagePath`, `localFileMediaPath`, `FullPath`, `LocalName`, `StatSize`, save-storage name handling, and `Storages.addArchive` now use it, `System.dataPath` uses the same spelling, and `normalize` resolves the archive file path before appending `>member` because `>` is not a legal Windows filename character.
2. `member not found: affineCopy` from `AffineLayer.tjs:540` → `510` → `BMPBaseAffineSourceLayer.tjs:352` → `330`. `Layer.affineCopy` is implemented in `internal/runtime/layer_image.go` (`affineCopyPixels` rasterizer plus the `layerAffineCopy` entry point): points and matrix modes, nearest and linear sampling, `clear` filling the clipping rectangle with the layer's neutral color, bmCopy channel selection per draw face, and a source snapshot when the source plane is the destination plane. Semantics follow `tTJSNI_BaseLayer::AffineCopy` (`reference/krkr2/cpp/core/visual/LayerIntf.cpp:4722`) and `tTVPBaseBitmap::AffineBlt` (`LayerBitmapIntf.cpp:3960`).
3. `member not found: callOnPaint` from `world.tjs:1389` and `affinelayer.tjs:545`. `callOnPaint` is a script-visible native Layer property in the reference (`LayerIntf.cpp:11321`) but was a private Go field; it is now a registered native property with getter and setter.
4. The stage matrix held NaN. Root cause: the VM resolved unqualified names inside `with` blocks through the with-object, so `with (world.env) { cameraoffsetz = .cameraoffsetz; }` (`world.tjs:555`) wrote to `world.env` instead of the new `EnvCameraObject`. `camerainfo.cameraoffsetz` stayed void, `zorder` computed `void / void * 100` = NaN (`world.tjs:295`), and `EnvGraphicLayer.calcAffineMatrix` called `mtx.scale(NaN, NaN)` (`world.tjs:470`), which failed the stage draw. TJS2 binds unqualified names to parameters/locals and then `this`, and only the `.name` spelling uses the with object (`reference/krkr2/cpp/core/tjs2/tjsInterCodeGen.cpp`, T_SYMBOL / T_THIS_PROXY / T_WITHDOT_PROXY, plus `AddLocalVariable`). `vm.loadName` and `vm.storeName` now follow that, the dead `DeclareWith` op field was removed, and `TestUninitializedVarInWithShadowsMemberWithoutOverwritingIt` pins the reference behaviour.

Recorded behavior: the desktop recording `录屏2026-10-12 23.17.46.mov` contains 338 video frames at 57.53 fps. Frames 1-135 show the notice image; frames 136-178 are black; frames 179-188 show the Yuzusoft animation; frame 189 is black; frames 190-195 show the next Yuzusoft animation; frames 196-208 show the 640x480 white motion target with partial M2 artwork; frames 209-338 are black. The white target begins at 3.408333 seconds and the persistent black frame begins at 3.633333 seconds.

The recorded renderer trace showed `Motion.Player.play("same-motion")` being called on every continuous update. The native implementation now preserves an active motion when `play` repeats the same motion, with generic coverage in `TestMotionPlayerPlayPreservesActiveMotionProgress`.

User verification after this change reported no visible change. The Amairo runtime investigation ends here at the user's instruction; this handoff makes no claim of visual resolution.

Implemented in the current worktree:

- `Motion.Player` resource-manager/player/adaptor runtime surface.
- PSB RL decoding for BGRA pixels and indexed-palette pixels.
- Motion-layer image composition and `Motion.SeparateLayerAdaptor` target binding.
- Repeated active-motion play calls preserve progress.
- Invalid bound continuous-handler cleanup after script-object invalidation.
- Script `finalize` execution during TJS object invalidation, before native finalization.
- FreeMote reference clone at `reference/FreeMote`.
- Full repository verification currently passes with `go test ./... -count=1`; temporary game-specific startup/timeline tests were removed.

Known unfinished Amairo work:

- Broader PSB motion parity: nested motion timing, interpolation, affine transforms, and additional pixel formats require separate validation.
- Window-drag behavior remains a separate host-window issue in `internal/host/ebiten.go`.

## Pending worktree files

The current worktree contains the startup/plugin migration changes plus this handoff update:

```text
HANDOFF.md
internal/runtime/lightweight_plugins_native.go
internal/runtime/scripts_native.go
internal/runtime/services.go
internal/runtime/session.go
```

## Recommended next order

1. Implement or load the real `WIN32GenericDialogEX` plugin surface before `uimain.tjs` startup.
2. Amairo: validate broader PSB motion parity, then fix the separate window-drag issue in `internal/host/ebiten.go`.
