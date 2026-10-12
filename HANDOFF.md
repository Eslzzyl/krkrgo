# krkrgo Handoff

Status: 2026-10-12
Repository: `/Users/eslzzyl/WorkSpace/Go/krkrgo` (macOS) and `D:/WorkSpace/Go/krkrgo` (Windows)

## Current objectives

1. Build and package the FFmpeg 9.0.2 shared-library sidecar for the six desktop matrix targets.
2. Validate FFmpeg-backed ASF/WMV3/WMA2 playback against the local Lovelyday fixture.
3. Continue the Amairo startup black-screen investigation.

No subagents are currently running. The current media path uses purego bindings and does not run an FFmpeg executable.

## Commits

- `538f559 Update documentation execution contract`
  - Added `agents/krkrgo-execution-contract.md`.
  - Removed all `CGO_ENABLED=0` occurrences from repository Markdown documentation.
- `febd39c Fix Windows storage paths and the Amairo opening sequence`
  - Windows drive-qualified storage names, `Layer.affineCopy`, `callOnPaint`, and the TJS2 `with` scope rules; see "Amairo status" below.

The FFmpeg sidecar implementation is in the current worktree and has not been built locally.

## Verification completed

The current Go test suite passes on the development host:

```text
go test ./...
```

The sidecar build is defined by `scripts/build-ffmpeg.sh` and `.github/workflows/build-ffmpeg.yml`; no FFmpeg build has been executed locally.


## FFmpeg media status

Implemented in `internal/media/ffmpeg/`:

- purego dynamic loading for `avutil`, `avcodec`, `avformat`, and `swscale`;
- bounded memory-backed AVIO input;
- ASF/WMV/WMA stream discovery, WMV3 frame conversion, and WMA2 PCM conversion;
- runtime `VideoOverlay` integration with the shared 48 kHz stereo mixer;
- seek, close, tick snapshot, and error-retry state handling.

The unfinished pure-Go WMV3 and ASF packages, Ogg/Vorbis Go decoder, and their tests have been removed. `WaveSoundBuffer` retains bounded WAVE fallback decoding when the FFmpeg sidecar is absent.

The sidecar build is implemented by `scripts/build-ffmpeg.sh` and `.github/workflows/build-ffmpeg.yml`. The workflow pins the latest FFmpeg 9.0.2 source archive with SHA-256 verification. The build produces the four shared libraries, FFmpeg LGPL license, source version, configure manifest, and ABI probe output for each matrix target. Hardware acceleration and FFmpeg command-line programs are disabled.

The sidecar has not been built locally. Full Go tests pass with `go test ./...`.

## Remaining FFmpeg work

The following verification is still pending:

- Run the manually triggered six-target FFmpeg 9.0.2 sidecar workflow and retain the uploaded artifacts.
- Validate `testdata/lovelyday/movie.xp3 > logo.wmv` with the sidecar: metadata, first-frame RGBA output, WMA2 PCM output, seek, rewind, loop, and close.
- Run the clean-staging loader integration test for each native target and complete the dynamic dependency scan.
- Add and run fixture golden checks for the first-frame dimensions, PTS, RGBA hash, audio rate/channel count, and fixed PCM hash.

The FFmpeg build has not been run locally. The workflow is manual-only.

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

The current worktree includes:

```text
HANDOFF.md
reference/FreeMote
internal/assets/psb_image.go
internal/assets/psb_image_test.go
internal/assets/storage.go
internal/assets/storage_path_test.go
internal/assets/text_cx_test.go
internal/core/core.go
internal/core/project.go
internal/runtime/layer_image.go
internal/runtime/layer_image_native_test.go
internal/runtime/layer_native.go
internal/runtime/motion_render.go
internal/runtime/motionplayer_native.go
internal/runtime/motionplayer_runtime.go
internal/runtime/runtime_kag_save_test.go
internal/runtime/save_storage.go
internal/runtime/services.go
internal/runtime/session.go
internal/runtime/session_project_test.go
internal/runtime/storages_native.go
internal/runtime/storages_native_test.go
internal/runtime/system_native.go
internal/runtime/system_native_test.go
internal/tjs/compiler.go
internal/tjs/gc.go
internal/tjs/lifetime.go
internal/tjs/sound_expression_test.go
internal/tjs/vm.go
internal/tjs/vm_test.go
```

The prior WMV3/ASF handoff notes are superseded by the FFmpeg sidecar path.

## Recommended next order

1. Build the six sidecar artifacts in the GitHub Actions matrix.
2. Run clean-staging loader integration with the local `logo.wmv` fixture.
3. Validate playback, seek, rewind, loop, audio output, and close/reopen events.
4. Amairo: validate broader PSB motion parity, then fix the separate window-drag issue in `internal/host/ebiten.go`.
