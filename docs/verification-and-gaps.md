# Verification and Cross-Cutting Gaps

## Recorded verification

The following commands were run after the current changes on the Darwin arm64 environment and passed:

```sh
go test ./... -count=1
GOOS=darwin GOARCH=arm64 go build ./...
go vet ./...
```

The README associates these checks with Ebitengine v2.10.4 and the desktop Pure Go path. The build check above covers the current Darwin target only.

The workspace contract records a separate previous cross-platform baseline:

```sh
GOOS=windows GOARCH=amd64 go build ./...
GOOS=linux GOARCH=amd64 go build ./...
GOOS=darwin GOARCH=amd64 go build ./...
```

That triple-desktop baseline predates audio-adapter integration. It must not be interpreted as current cross-platform verification after later changes. The contract says cross-platform builds are run for an explicit platform assessment, release, or user request.

## Cross-cutting status and gaps

The codebase is incremental and bounded; registered method names do not guarantee that every operation is implemented. Unsupported operations return explicit errors where the source says they are unavailable. Domain details and Go source references are in:

- [TJS and KAG](tjs-kag.md): compiled TJS2 storage input, selected `Scripts` operations, KAG inline `[iscript]`/`[endscript]`, nested macro definitions, and broader C++ KAG tags.
- [Assets, storage, and media](assets-storage-media.md): non-file storage backends, C++ media semantics, and unsupported codec/mode coverage.
- [Runtime and native services](runtime-natives.md): process/host semantics, injected dialogs/shell, and unavailable devices.
- [Graphics, layers, and text](graphics-layer-text.md): Bitmap supports PNG/BMP/JPEG saving with bounded metadata/options and asynchronous loading; color-key and raw-pointer paths remain unsupported. ImageFunction operations, complete layer effects, and full text/font behavior remain bounded.
- [Audio and video](audio-video.md): MPEG picture/chroma limits, audio/video stream selection and processing limits, and headless samples versus device output.
- [Host, Ebitengine, and platforms](host-platform.md): generic project launching, platform backend qualifications, mobile policy, and Linux clipboard utilities.

## Lovelyday headless acceptance

The project-backed replay uses the tool's actual CLI form:

```sh
go run ./cmd/krkrgo-tool replay --project DIR --input replay.json --output result.json --step-ms 1000 --budget 100000000
```

The full interactive replay ran from 0 through 120,000 ms using 1,000 ms steps and a 100,000,000-instruction budget, and exited with status 0. Its duration is virtual replay time, not wall-clock waiting; the budget is an instruction limit.

The completed 120-second replay exercised KAG macro argument and line/comment handling, pointer-to-script interaction, TJS `Math`, virtual-time waits, native `Date` and `Storages.fstat`, XP3-backed media/resource paths, TLG6 decoding, universal rule-image transitions, and the macOS MS P Mincho to Hiragino Mincho ProN font fallback. These results cover the exercised run, not complete C++ parity. WMV/ASF support is provided by the FFmpeg-backed `VideoOverlay` path, but this replay did not exercise those formats.

A separate save/load recovery replay continued into chapter two. Stage 43 reached `@0002A_0030.ks`, line 675, with `hitret_cnt=90`, and exited with status 0; replaying the stage-42 checkpoint reproduced it exactly. The earlier 93-second Voice/ImageDraw script error is resolved. This records the verified recovery range, not a claim that the entire game has been cleared. Focused `TextStream.write` offset/mode behavior tests and the full test suite passed; the write semantics are documented in [Runtime and native services](runtime-natives.md).

## Local acceptance fixture

The CAWorks project is an external local acceptance input; its source and game resources remain outside this repository. The Go launcher supports both project layouts described in [Host, Ebitengine, and platforms](host-platform.md), and the TJS runtime implements the `kirikiriz=1` preprocessing environment.

An empty-input replay originally reproduced the first-tick exception `member not found: Date` in the `startanchor` path. The existing TJS `Date` implementation is now registered, and the focused bookmark-timestamp test passes.

The verified interactive CAWorks replay starts with a pointer click at `(1147,462)`, advances through the message glyph clicks at `(28,34)` and `(28,64)`, waits for the `boom.wav` stop callback, and activates the page-break control at `(28,94)`. Repeated page navigation reaches `66000ms` with checkpoints at `45000ms`, `48000ms`, `58000ms`, and `66000ms`; every checkpoint has no replay error. The replay covers script `onHitTest`, constructed-instance identity, inherited sound callbacks, automatic garbage collection during native calls, and KAG repeated-label jumping.

The first actual page-break failure was `KAG label not found: *1:2` in `1.ks`. CAWorks declares `*1|` followed by repeated `*|` labels; KAGParserEx indexes those occurrences as `*1`, `*1:2`, and so on. Go now preserves those aliases and resolves `*1:2` to the second occurrence at line 30. The runtime also preserves the original `.asd` scenario storage separately from an explicit tag `storage` attribute, loads the ASCII animation scenario, and supports the exercised `Layer.copyRect` paths.

A second replay enters `setting.ks` through the title setting button at `(1147,532)`, then activates the rendered `return` link with pointer coordinates `(1084,666)`. The final checkpoint reports `curStorage=0.ks`, `curLabel=*1:2`, `curLine=34`, `lastTagName=s`, and `status=0`, with no replay error. This verifies the `ignoreCR` parser property, message-layer Province hit testing, dynamic KAG entity attributes for `storage` and `target`, and the return path.

The original CAWorks scenario requests Japanese MS Gothic, which is unavailable on the Darwin host. The external acceptance copy uses the real `Hiragino Mincho ProN` family for that project-specific setting; the runtime keeps generic system discovery and real-face fallback for other requests.

Focused runtime verification also passes for KAGParser `curLineStr`, `clear`, `assign`, `popMacroArgs`, active `mp`/`macroParams`, nearest-neighbor general affine matrices including rotation, and pure-Go ImageFunction text/glyph rendering. The implementation keeps explicit unsupported errors for non-nearest sampling, `hda`, rotated point-mode transforms, shadows, and non-256-level glyph masks.

The current Darwin verification commands are:

```sh
go test ./... -count=1
GOOS=darwin GOARCH=arm64 go build ./...
go vet ./...
```

The focused KAG/font replay and test checks also pass; broader C++ compatibility remains incomplete, and the domain documents list the remaining unsupported operations and platform-specific limits.

**C++ reference:** [Kirikiri Z source](https://github.com/krkrz/krkrz). Recorded verification context is the workspace execution contract and the README status statement.
