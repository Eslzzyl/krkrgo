# krkrgo Handoff

Status: 2026-10-12
Repository: `/Users/eslzzyl/WorkSpace/Go/krkrgo` (macOS) and `D:/WorkSpace/Go/krkrgo` (Windows)

## Current objectives

1. Translate the FFmpeg WMV3 decoder to pure Go and play the Lovelyday startup movie `testdata/lovelyday/movie.xp3 > logo.wmv`.
2. Continue the Amairo startup black-screen fix.

No subagents are currently running. The WMV3 and ASF agents used `cline-pass/mimo-v2.6-flash`. The Amairo takeover agent used `cline-pass/deepseek-v4.1-flash:high`.

The Amairo work below was verified on the Windows host. Earlier Amairo and WMV3 notes were written on macOS; platform assumptions in the storage and host layers only surface on Windows.

## Commits

- `538f559 Update documentation execution contract`
  - Added `agents/krkrgo-execution-contract.md`.
  - Removed all `CGO_ENABLED=0` occurrences from repository Markdown documentation.
- `febd39c Fix Windows storage paths and the Amairo opening sequence`
  - Windows drive-qualified storage names, `Layer.affineCopy`, `callOnPaint`, and the TJS2 `with` scope rules; see "Amairo status" below.

The current code and WMV3 assessment document listed below are intentionally included in the pending commit created after this handoff document.

## Verification completed

The following commands passed against the current worktree on Windows:

```text
go test ./... -count=1
go vet ./...
go build ./...
```

Focused checks also passed:

```text
go test ./internal/assets/... ./internal/core/... -count=1
go test ./internal/runtime/... ./internal/tjs/... -count=1
```

The WMV3 package currently reports 33 passing tests. The ASF package has no implementation tests yet.

Windows-specific test facts discovered while fixing Amairo:

- `internal/assets` and `internal/runtime` tests leaked XP3 file handles, so `t.TempDir` cleanup failed on Windows. New helpers `closeArchivesOnCleanup` close the archives on test cleanup.
- Symlink tests must tolerate a missing `SeCreateSymbolicLinkPrivilege`; `os.Symlink` fails with "A required privilege is not held by the client" on this machine (Developer Mode is off), so `TestSaveStorageAtomicContained` now skips that part with `t.Skipf`.

## WMV3 implementation status

Implemented under `internal/assets/wmv3/`:

- Existing bounded MSB-first bit reader and WMV3 Simple/Main extradata parser.
- Scalar VLC, unary, bitplane, intensity-compensation, quantizer, motion-mode, transform, and B-fraction support needed by the frame-header layer.
- `NewDecoder`, persistent decoder state, `Frame`, `FrameHeader`, picture-type handling, and explicit unsupported-picture errors.
- I/P/B/BI/X8 frame-header parsing, including PQ, HalfPQ, PQuantizer, MV modes, bitplanes, DQuant, transform fields, AC/DC table selectors, and B-fraction fields.
- FFmpeg LGPL provenance in package documentation.
- Focused deterministic tests in `frameheader_test.go` plus existing parser and bit-reader tests.

Verified behavior:

- Simple/Main sequence and frame-header parsing compiles and passes its package tests.
- The decoder has a stable shape for the next macroblock implementation stage.
- `Decode` still returns `ErrPictureUnavailable` for I/P pictures because the picture layer is not implemented.
- B/BI/X8 and other unsupported paths return typed unsupported errors.

Missing WMV3 work:

- Macroblock parsing and coefficient VLC tables.
- DC/AC prediction and inverse transforms.
- Motion-vector prediction, luma/chroma motion compensation, and reference pictures.
- YUV420 output and frame reorder/flush behavior.
- ASF access-unit input, runtime `VideoOverlay` integration, clock handling, and playback.

Consequently, `logo.wmv` still does not play.

## ASF status

`internal/assets/asf/doc.go` contains the package-level design/provenance note. The ASF implementation agent stopped before writing the parser at the user's instruction.

Verified target facts from `logo.wmv`:

- 732,593 bytes.
- ASF header: 5,319 bytes, 7 objects.
- 61 packets, 11,920-byte packet size.
- Video stream 2: WMV3, 1280x720, 5-byte extradata.
- Audio stream 1: WMA2, 44.1 kHz, stereo, 10-byte extradata.
- Packet layout uses ECC, multi-payload video, replicated object data, and fragmented media objects.
- Packet walk yielded 106 video access units, one keyframe, first video unit 4,508 bytes at PTS 0, and 10 audio units with monotonic timestamps after preroll adjustment.

Missing ASF work:

- Header, file-properties, and stream-descriptor parser.
- Bounded packet iterator.
- ECC, payload, replicated-data, fragment, timestamp, and keyframe parsing.
- Media-object reassembly.
- Malformed-input and target-fixture tests.

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

Current behaviour: `testdata/amairo` starts, runs at least 45 s of host frames with no logged exception, and the frame sequence advances past the white `[syscover]` frame that `TestAmairoStartupCoverAndNoticeImage` already asserted.

Remaining Amairo blockers (reproduced on Windows, not yet fixed):

- After the initial white frame the window stays black: the stage/opening picture is still not visible. The affine draw itself now runs without exceptions, so this is a composition problem after `Layer.affineCopy` (layer visibility/order, clip, or update propagation to the compositor).
- Dragging the window freezes the process; the freeze is in the Ebitengine host window loop (`internal/host/ebiten.go`).
- PSB/motion forms of `[ev storage=... psb chara=... motion=...]` remain separate capability work. The Windows-only `AmairoIsleNauts.tpm` remains unavailable to the pure-Go runtime.

## Pending worktree files

The Amairo fix touched:

```text
HANDOFF.md
internal/assets/storage.go
internal/assets/storage_path_test.go
internal/core/core.go
internal/core/project.go
internal/runtime/layer_image.go
internal/runtime/layer_native.go
internal/runtime/runtime_kag_save_test.go
internal/runtime/save_storage.go
internal/runtime/session.go
internal/runtime/session_project_test.go
internal/runtime/storages_native.go
internal/runtime/storages_native_test.go
internal/runtime/system_native.go
internal/runtime/system_native_test.go
internal/tjs/compiler.go
internal/tjs/sound_expression_test.go
internal/tjs/vm.go
```

The WMV3/ASF files listed by the previous handoff are unchanged by this work.

## Recommended next order

1. Implement bounded ASF parsing and media-object reassembly for `logo.wmv`.
2. Implement WMV3 macroblock decode, coefficients, transforms, motion compensation, and YUV output.
3. Connect ASF video access units to the WMV3 decoder and `VideoOverlay` clock/frame queue.
4. Add WMA2 only if audio parity is required for the opening movie milestone.
5. Amairo: find why the composited frame stays black after the `[syscover]` frame (start from `EnvGraphicLayer` visibility/order and the update path after `Layer.affineCopy`), then fix the window-drag freeze in `internal/host/ebiten.go`.
