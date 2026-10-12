# krkrgo Handoff

Status: 2026-10-12
Repository: `/Users/eslzzyl/WorkSpace/Go/krkrgo`

## Current objectives

1. Translate the FFmpeg WMV3 decoder to pure Go and play the Lovelyday startup movie `testdata/lovelyday/movie.xp3 > logo.wmv`.
2. Continue the Amairo startup black-screen fix.

No subagents are currently running. The WMV3 and ASF agents used `cline-pass/mimo-v2.6-flash`. The Amairo takeover agent used `cline-pass/deepseek-v4.1-flash:high`.

## Commits

- `538f559 Update documentation execution contract`
  - Added `agents/krkrgo-execution-contract.md`.
  - Removed all `CGO_ENABLED=0` occurrences from repository Markdown documentation.

The current code and WMV3 assessment document listed below are intentionally included in the pending commit created after this handoff document.

## Verification completed

The following commands passed against the current worktree:

```text
go test ./... -count=1
go vet ./...
go build ./...
```

Focused checks also passed:

```text
go test ./internal/assets/wmv3 -count=1
go test ./internal/runtime/... ./internal/tjs/... -count=1
```

The WMV3 package currently reports 33 passing tests. The ASF package has no implementation tests yet.

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

The following files contain the current generic runtime fix:

- `internal/runtime/kag_native.go`
- `internal/tjs/assign_struct.go`
- `internal/tjs/assign_struct_test.go`
- `internal/tjs/gc.go`
- `internal/tjs/vm.go`
- `internal/runtime/amairo_startup_test.go`

Root causes identified by the Amairo investigation:

1. KAG tag elements lacked the `taglist` member expected by the KAGEnvironment `getCommandTarget` path. The `[ev file=attention notrans]` tag was consumed without creating the `ev` environment object or loading `attention.png`.
2. Dictionary methods registered as static members leaked into Dictionary instances. KAG scripts inspect fields such as `elm.clear`; the leaked instance method changed the `syscover` branch and prevented the white cover from being shown.

The fix adds a KAGParserEx-compatible taglist, restores static-only Dictionary members, and adds a focused startup test. The test verifies that `ev` contains `imageFile == "attention"`, has a shown display state, and that the startup frame sequence changes from the old solid-black sequence. Current runtime and TJS tests pass.

Remaining Amairo blocker:

- The opening script reaches `[stage ...]` and stops because `Layer.affineCopy` is unimplemented. The environment image now decodes into an affine source layer, but the affine source pool/player path still needs to render it into the scene. This prevents claiming a complete Amairo visual fix.
- PSB/motion forms of `[ev storage=... psb chara=... motion=...]` remain separate capability work. The Windows-only `AmairoIsleNauts.tpm` remains unavailable to the pure-Go runtime.

## Pending worktree files

The pending commit includes:

```text
docs/wmv3-go-port-assessment.md
internal/assets/asf/doc.go
internal/assets/wmv3/bitplane.go
internal/assets/wmv3/bitreader.go
internal/assets/wmv3/bitreader_test.go
internal/assets/wmv3/decoder.go
internal/assets/wmv3/doc.go
internal/assets/wmv3/extradata.go
internal/assets/wmv3/extradata_test.go
internal/assets/wmv3/frameheader.go
internal/assets/wmv3/frameheader_test.go
internal/assets/wmv3/tables.go
internal/assets/wmv3/vlc.go
internal/runtime/amairo_startup_test.go
internal/runtime/kag_native.go
internal/tjs/assign_struct.go
internal/tjs/assign_struct_test.go
internal/tjs/gc.go
internal/tjs/vm.go
```

Temporary `cmd/tmpasflist/main.go` was removed before this handoff.

## Recommended next order

1. Implement bounded ASF parsing and media-object reassembly for `logo.wmv`.
2. Implement WMV3 macroblock decode, coefficients, transforms, motion compensation, and YUV output.
3. Connect ASF video access units to the WMV3 decoder and `VideoOverlay` clock/frame queue.
4. Add WMA2 only if audio parity is required for the opening movie milestone.
5. Generalize `imageAffine` into `Layer.affineCopy`, then verify the real Amairo opening window and keep the focused startup test.
