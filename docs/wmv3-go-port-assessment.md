# WMV3 Pure-Go Port Assessment

## Executive decision

A pure-Go WMV3 path is technically feasible, but it is a codec/container project rather than a small `VideoOverlay` extension. The local FFmpeg tree does not contain a small, self-contained `wmv3.c`: WMV3 is registered as a codec ID and uses the shared VC-1 decoder (`vc1dec.c`) plus the VC-1 parser, block, motion-compensation, prediction, loop-filter, DSP, VLC/data tables, and MPEG-video support selected by FFmpeg's build graph. ASF packet reconstruction is a separate project, and the `logo.wmv` target also contains WMA2 audio. A video-only port can be staged, but it is not end-to-end playback for the target asset.

**Recommendation:** proceed only as a gated, bounded, software-only decoder effort. First prove ASF + WMV3 progressive Simple/Main-profile video on a small fixture, then add the runtime frame path, and only then decide whether WMA2 is required for the product milestone. For `logo.wmv` parity, budget for WMA2 and synchronized audio as well as WMV3. Do not use cgo, a dynamic library, an FFmpeg executable, DirectShow, or Windows Media codecs.

The estimates below are engineering estimates, not measurements. No implementation was performed as part of this report; no FFmpeg or C++ build was run, and no network source was consulted.

## Evidence and local source map

All paths in this report are relative to the repository root unless marked otherwise. The authoritative C++ behavior is `reference/krkrz`; `reference/krkrz_rs` and `reference/krkr2-main` are supplemental.

### Container and packet layer (ASF/WMV)

| Responsibility | Local FFmpeg evidence | Porting implication |
|---|---|---|
| ASF identity and GUIDs | `reference/FFmpeg/libavformat/asf_tags.c:23-40,74-92`; `asf.h:72-100` | Implement exact 16-byte GUID matching and little-endian object sizes. The ASF header GUID is the same signature currently recognized by Go. |
| Header object and stream discovery | `libavformat/asfdec_o.c:1572-1648`; `asfdec_o.c:581-684` | Parse bounded objects, file properties, preroll/duration, stream number, media type, type-specific data, error-correction data, and stream metadata. Video and audio stream numbers are not necessarily array indexes. |
| Data object and seek/index setup | `libavformat/asfdec_o.c:795-829,832-884` | A reader needs the data-packet offset, packet count, optional Simple Index, and a defined policy for non-seekable/broadcast input. |
| Packet header/PPI/payload parsing | `libavformat/asf.h:131-189`; `asfdec_f.c:881-1120` | Handle optional error-correction bytes, variable-size packet/sequence/padding fields, single or multiple payloads, replicated data, media-object number, fragment offset/size, and timestamps. Every length must be checked before slicing. |
| Fragment reassembly | `libavformat/asfdec_f.c:1123-1299` | Fragments can span packets and must be accumulated by stream/media-object number before decoding. Reassembly also carries keyframe flags, timestamps, preroll adjustment, and optional descrambling. |
| Alternate demux implementation | `libavformat/asfdec_f.c` is registered as `ff_asf_demuxer` by `libavformat/Makefile:141-145`; `asfdec_o.c` is `ff_asf_o_demuxer` | Choose one narrowly scoped parser for ordinary local files. Do not translate both implementations unless compatibility evidence requires it. |
| Optional encryption/scrambling | `libavformat/asfcrypt.c`; `asfdec_f.c:1296-1299`; `asfdec_f.c:60-62` | Treat DRM/encrypted content and packet descrambling as explicit unsupported cases initially. They are not implied by ASF recognition. |

The Go implementation currently performs only bounded ASF recognition and limited stream-codec identification in `internal/runtime/video.go:398-477`; it does not demux packets or decode them.

### WMV3 / shared VC-1 video decoder

The decisive registration/build evidence is:

- `reference/FFmpeg/libavcodec/allcodecs.c:361-399` declares both VC-1 and WMV3 codec objects.
- `reference/FFmpeg/libavcodec/Makefile:790-792` builds the VC-1 decoder from `vc1dec.o vc1_block.o vc1_loopfilter.o vc1_mc.o vc1_pred.o vc1.o vc1data.o msmpeg4_vc1_data.o wmv2data.o`.
- `reference/FFmpeg/configure:3308-3309,3335-3336` selects `blockdsp h264qpel intrax8 mpegvideodec qpeldsp vc1dsp` for VC-1, then aliases `wmv3_decoder_select` to `vc1_decoder`.

The practical source/dependency map is:

| Layer | Files to study/translate | What it reconstructs |
|---|---|---|
| Codec entry and frame state machine | `libavcodec/vc1dec.c`, `vc1.h`, `vc1_common.h` | WMV3 codec registration, initialization, frame parsing, delayed/reordered output, profile setup, reference pictures, interlace/slice handling, and output format. `vc1dec.c:613-735` shows the WMV3 extradata path; `:775-933` shows frame-marker/unescape/slice handling. |
| Sequence/entry-point/frame headers | `vc1.c` | Simple/Main/Advanced profile headers, quantizer and motion-mode state, entry points, extradata, frame header parsing, and bitplane setup. Representative entry points are `vc1.c:276-287`, `:495-595`, and `:667-887`. |
| Macroblocks, VLC coefficients, transforms | `vc1_block.c`, `vc1acdata.h`, `vc1_vlc_data.h`, `vc1data.c` | Intra/inter block decoding, AC/DC prediction, escape codes, coded-block patterns, transform-size decisions, quantization, and macroblock loops. `vc1_block.c:514-581` and `:1080-1207` show the coefficient and transform hot paths. |
| Motion vectors and prediction | `vc1_pred.c`, `vc1_mc.c` | MV differential VLCs, 1-MV/4-MV and B-picture prediction, quarter-/half-pel interpolation, luma/chroma compensation, intensity compensation, and reference-frame access. |
| Loop filtering/overlap | `vc1_loopfilter.c`, VC-1 DSP functions | In-loop deblocking and overlap filtering, including profile/interlace variants. |
| Scalar DSP | `vc1dsp.c`, `vc1dsp.h` | Inverse transforms, overlap/filter operations, mspel, chroma interpolation, sprite helpers, start-code search, and emulation-prevention removal. The function table is visible in `vc1dsp.h:37-88`. |
| Shared tables | `msmpeg4_vc1_data.c/.h`, `wmv2data.c`, `vc1data.c`, `vc1_vlc_data.h`, `vc1acdata.h` | Large static VLC, scan, quantizer, motion, and profile tables. These tables are part of the correctness surface, not optional tuning. |
| Generic selected primitives | FFmpeg components named by `configure:3308`: `blockdsp`, `h264qpel`, `intrax8`, `mpegvideodec`, `qpeldsp`, `vc1dsp`, plus their selected tables/utilities | A Go port must either implement equivalent narrow primitives or deliberately replace them with clear Go implementations. Translating FFmpeg's entire generic MPEG video framework would be unnecessary scope. |

WMV3 has a distinct initialization branch: `vc1dec.c:634-660` reads a WMV3 sequence header directly from codec extradata. VC-1 Advanced-Profile transport markers and emulation-prevention handling are also present in the shared path. A first milestone should explicitly state which profiles, interlace modes, B-frame/reordering cases, and image/sprite codecs are supported; “WMV3” alone is not a sufficient acceptance definition.

### WMA2 audio implications

The target media is documented locally as WMV3/WMA2 (`docs/audio-video.md:15`). FFmpeg identifies WMA v2 as audio format tag `0x161` and requires codec-specific WAVEFORMATEX extra bytes; this is documented in `libavcodec/wmadec.c:23-33`. The build map is:

- `libavcodec/Makefile:853-856`: WMA v1/v2 use `wmadec.o wma.o wma_common.o aactab.o`.
- `configure:3325-3329`: WMA v1/v2 select `sinewin wma_freqs`.
- `wmadec.c:75-142` initializes extradata flags, shared WMA state, VLCs and MDCT/window data.
- `wmadec.c:450-783` decodes blocks; `:795-990` decodes WMA superframes and produces planar float samples.
- `wmadec.c:1032-1044` registers WMA2 using the same superframe decoder.
- `wma.c:1-30` and `wma_common.c` provide shared tables/state; `wmadata.h`, `aactab.c/.h`, `sinewin.c/.h`, and `wma_freqs.c/.h` are table/math dependencies.

Therefore a WMV3-only video decoder can prove picture output, but it cannot claim `logo.wmv` playback parity. Full target playback needs ASF audio stream parsing, WMA2 superframe boundaries, WMA extradata, float/int16 conversion, clock alignment, seek/reset behavior, and mixer integration. WMA2 is an independent risk and should not be hidden inside the video estimate.

## Comparison with the C++ reference and current Go

### Official C++ krkrz behavior

The Windows implementation is a platform graph, not a pure codec implementation:

1. `reference/krkrz/movie/win32/dsoverlay.cpp:48-79` detects Windows Media and dispatches to `BuildWMVGraph`.
2. `dsmovie.cpp:1603-1640` constructs a `CWMReader`/`CDemuxSource`, a DirectShow graph, and the Windows Media Video DMO wrapper (`CLSID_WMVDecoderDMO`), then connects it to a renderer.
3. `dsmovie.cpp:1642-1668` constructs the WMA decoder DMO wrapper (`CLSID_WMADecoderDMO`) and a DirectSound renderer when audio is present.
4. `CWMReader.cpp:310-348` loads `wmvcore.dll` and `WMCreateSyncReader`, opens the stream, obtains the profile/header, chooses the first audio and video stream (`:520-554`), and sets selected streams (`:46-57,426-432`).
5. `CWMReader.cpp:164-199` obtains timestamped samples and clean-point flags. `CWMReader.h:75-127` exposes duration, frame count, dimensions, frame rate, start/end range and rate controls.

This path depends on Windows COM/DirectShow/WMF/DMO/DirectSound and is deliberately excluded from the proposed Go design. It is useful as a semantic target for stream selection, timestamps, transport, volume/balance and status events, not as code to call or translate wholesale.

### Current Go behavior

- `internal/runtime/video.go:79-136` models bounded RGBA frames and native-rate audio blocks. `Open` handles AMV and progressive MPEG, while ASF/WMV/WMA is sent to `installWindowsMediaStub` (`:328-396`).
- The stub intentionally produces no source metadata, frame or audio. It is recognition/transport tolerance, not playback support (`docs/audio-video.md:11-21`).
- MPEG frames are currently converted from progressive 4:2:0 to RGBA; frame queues are bounded and copied (`video.go:908-1011`). A decoder-backed WMV path should preserve those ownership/bounds rules rather than retaining unbounded packets.
- `internal/assets/media.go:243-562` provides bounded WAVE and Ogg/Vorbis decoding into signed-16 PCM. `internal/runtime/sound.go:247-255` opens those formats, and `audio_mixer.go:64-220` mixes/resamples sources to fixed 48 kHz stereo. There is no ASF, WMA2, or compressed movie-audio path today.
- `go.mod:5-21` contains image/WAVE/Vorbis/runtime dependencies but no FFmpeg binding or media process dependency. This is compatible with the requested in-process, pure-Go direction.

The C++ path has richer stream selection and device rendering; Go has a deterministic, bounded headless model and host audio adapter. Integration should target the Go model's explicit frame/audio queues and clock, not reproduce DirectShow graph objects.

## Proposed pure-Go architecture

1. **`internal/assets/asf` (or equivalent assets package):** a byte-slice/reader-based bounded ASF header parser and packet iterator. Return stream descriptors (number, media type, codec tag, dimensions, rate, extradata, timestamps) and fragment-reassembled access units. Keep GUIDs and all wire parsing local; reject malformed lengths before allocation. Start with seekable ordinary ASF files and one selected video plus one selected audio stream.
2. **`internal/assets/wmv3`:** an independent bit reader, VLC decoder, WMV3 extradata/profile parser, reference-frame store, scalar inverse transforms, motion compensation, overlap/deblock filtering, and YUV420 frame output. Expose a small `Decode(accessUnit) -> YUV/RGBA frame` API; do not expose FFmpeg's `AVCodecContext`-shaped state.
3. **Narrow scalar primitives first:** use straightforward integer Go for correctness. Do not port architecture-specific assembly. Add optional Go/architecture optimizations only after bit-exact tests demonstrate a need.
4. **`internal/assets/wma2` (separate milestone):** decode WMA2 superframes using ASF's audio extradata and timestamps into bounded PCM blocks. Keep it independent from the WMV3 package so a video-only fixture can be debugged without audio state.
5. **Runtime adapter:** add a decoder-backed source to `VideoOverlay` that incrementally demuxes/decodes, queues bounded frames/audio, and maps timestamps to `Advance`/`SetClock`. Avoid pre-decoding an entire movie; keep packet, frame and audio limits consistent with existing `Max*` limits. The adapter must reset codec/reference state on seek or restart and must not silently fall back to the WMV stub after a recognized decoder error.
6. **Color/audio contract:** convert WMV3 YUV output with an explicitly documented matrix/range policy (the current MPEG path uses BT.601 limited-range conversion). Convert WMA2 output to the existing mixer representation and resample only at the mixer boundary.

## Phased plan and estimated effort

| Phase | Deliverable and exit condition | Estimate (engineer-days) |
|---|---|---:|
| 0. Scope/fixtures/licensing | Freeze supported WMV3 profiles and ASF variants; collect local fixtures and expected metadata/pixels/audio; decide whether WMA2 is in the release target; record FFmpeg provenance. | 3–5 |
| 1. Bounded ASF reader | Header/stream descriptors, WAVEFORMATEX and BITMAPINFO-style type data, packet PPI/payload parsing, fragment reassembly, timestamps/preroll, deterministic malformed-input errors. | 7–12 |
| 2. WMV3 bitstream foundation | Bit reader, start-code/unescape handling, WMV3 extradata, sequence/entry/frame headers, VLC tables and scan/quant tables. | 8–14 |
| 3. WMV3 scalar decoder | Intra/inter macroblocks, DC/AC prediction, transforms, MV prediction, mspel/qpel/chroma MC, overlap/deblock, reference/reorder and progressive Simple/Main fixtures. | 20–32 |
| 4. Frame/output adapter | YUV420 validation/conversion, bounded incremental source, `VideoOverlay` transport/events/seek/reset, frame queue and memory accounting. | 8–13 |
| 5. WMA2 (if end-to-end target) | WMA2 extradata, superframe/block decode, windows/MDCT/VLC tables, PCM conversion, ASF timestamps and clock/mixer adapter. | 15–25 |
| 6. Conformance and hardening | Golden fixtures, malformed/truncated corpus, property/fuzz tests, seek/restart/reorder tests, performance profiling, license notices and review. | 10–16 |

**Planning range:** video-only proof through runtime integration is approximately **46–76 engineer-days**. Full WMV3 + WMA2 `logo.wmv`-style playback is approximately **71–117 engineer-days**. These ranges assume one engineer familiar with bitstream codecs and an available fixture/reference workflow; they exclude unrelated engine parity, GPU rendering, and production release work. A second engineer can shorten elapsed time but does not remove the codec/conformance work.

## Dependencies and non-goals

### Required dependencies

- Existing Go byte parsing/error/bounds conventions in `internal/assets`.
- A fixture corpus containing at least one WMV3 I/P sequence, one B-frame or reordered sequence if advertised, malformed/truncated ASF packets, and the target WMA2 stream if audio is in scope.
- A trusted oracle for expected dimensions, timestamps, frame hashes and PCM. The oracle may be an already available reference output; this assessment does not run FFmpeg or C++.
- A licensing review before copying/adapting FFmpeg tables or code into the Go tree.

### Explicit non-goals for the first release

- DirectShow, Media Foundation, Windows Media Format SDK, DMO, `wmvcore.dll`, cgo, dynamic libraries, external FFmpeg/ffprobe processes, network streaming, ASF broadcast mode, DRM/decryption, and hardware acceleration.
- WMV3 Image/sprite (`wmv3image`) unless a concrete game asset requires it. FFmpeg registers it separately (`allcodecs.c:398-399`) and its sprite path is materially different.
- All VC-1 Advanced Profile interlace/field/slice combinations unless fixtures demonstrate they are required. Support claims must be explicit.
- General WMA Pro/lossless/voice. ASF recognition of those tags is not decoder support; current Go code only labels them in `windowsMediaStreamCodecs`.

## Risks and mitigations

1. **Codec breadth hidden behind one name.** WMV3 can exercise different profiles, frame modes, B-frame reorder and interlace paths. Mitigation: publish a profile/mode support matrix and reject unsupported modes with typed errors.
2. **ASF fragmentation and timestamps.** A parser that works on one contiguous packet can fail on multi-payload or fragmented media objects. Mitigation: packet-level fixtures, reassembly invariants, preroll/timestamp tests and strict size limits.
3. **VLC/table transcription errors.** Static tables are large and a one-entry error causes plausible but wrong pictures. Mitigation: generate/verify tables from cited local sources, test every table family, and compare frame hashes at multiple points.
4. **Reference-frame and delayed output semantics.** B pictures and seek resets require retaining/releasing references in the right order. Mitigation: test I/P/B sequences, `flush`, backward seek and close/reopen; make ownership explicit.
5. **Integer/DSP mismatch.** FFmpeg's scalar and assembly DSP have exact rounding, saturation and edge rules. Mitigation: implement scalar first, compare transform/MC micro-tests, and only optimize after conformance.
6. **WMA2 is a second codec.** Video success does not make the target movie audible. Mitigation: track WMA2 as a separate phase and acceptance gate, not an implied subtask.
7. **Memory/latency regressions.** Full-file decode would violate the bounded runtime model. Mitigation: incremental access units, frame/audio queue caps, packet-size caps and allocation instrumentation.
8. **Licensing/provenance.** Translating FFmpeg is adaptation of source, not a clean-room assertion. Mitigation: preserve required notices and source provenance, isolate imported/adapted files, and obtain project legal approval before distribution.
9. **Target asset variance.** `docs/audio-video.md` records `logo.wmv` as WMV3/WMA2, but one asset does not establish all game/movie variants. Mitigation: inventory actual shipped WMV/ASF members before broadening support claims.

## Licensing and provenance

The local FFmpeg license facts are:

- `reference/FFmpeg/LICENSE.md:3-11` states that most FFmpeg files are LGPL v2.1 or later, with some MIT/X11/BSD files, and that optional GPL parts are activated separately.
- The exact WMV3/VC-1 files inspected (`libavcodec/vc1dec.c:1-21`, `vc1.h:1-20`, `vc1_common.h:1-20`, `vc1_block.c:1-21`, `vc1_mc.c:1-21`, and `vc1dsp.h:1-20`) carry an LGPL v2.1-or-later notice.
- WMA files inspected (`libavcodec/wmadec.c:1-20` and `wma.c:1-20`) carry the same LGPL v2.1-or-later notice.
- `reference/FFmpeg/COPYING.LGPLv2.1` is the accompanying license text. `LICENSE.md:13-25` lists GPL-specific files, which are not part of the WMV3/WMA2 source set identified above, but the complete dependency closure must be rechecked before copying any generic helper.

These facts do **not** constitute a legal conclusion about the final Go distribution, patent status, or the licenses of every transitive table/helper file. A translation/adaptation should retain copyright and license notices, document changes and provenance, and ship the applicable license texts/attribution. Do not copy C++ krkrz DirectShow/Windows Media code as a substitute: it has separate project copyright and platform dependencies. This report does not alter production licensing files.

## Acceptance checkpoints

### Checkpoint A — ASF parser

- Correctly identifies the local ASF header GUID and rejects truncated/invalid object sizes.
- Enumerates stream numbers and codec tags from stream properties; reads video dimensions/rate/extradata and WMA2 WAVEFORMATEX/extradata.
- Reassembles fragmented media objects with stable timestamps, keyframe flags and preroll policy.
- Enforces packet/object/queue limits and returns typed, deterministic errors for malformed data.

### Checkpoint B — WMV3 decoder

- Decodes the agreed Simple/Main progressive fixture set with deterministic YUV output and exact dimensions.
- Produces correct frame order and timestamps for the agreed I/P/B cases; flush and reset do not leak references.
- Converts to the runtime RGBA representation with documented range/matrix behavior.
- Fails closed on unsupported profile/interlace/image modes; never labels a stub as decoded media.

### Checkpoint C — Go runtime integration

- `VideoOverlay.Open` reports non-zero metadata for a supported WMV3 stream, `CurrentFrame`/layer presentation changes on clock advance, and close/reopen/seek are deterministic.
- Frame/audio queues remain bounded under long input and malformed input; no external process, cgo, DLL or dynamic codec is used.
- Existing AMV/MPEG/WAVE/Vorbis behavior remains unchanged, and WMV3 errors preserve the source name and underlying cause.

### Checkpoint D — WMA2 and target movie (only if required)

- WMA2 fixture decodes to bounded PCM with expected rate/channels/sample count and stable superframe boundaries.
- Audio is timestamp-aligned with video, participates in the existing mixer/volume/balance path, and seek/restart resets both clocks.
- A local `logo.wmv`-style fixture produces observable video frames and audio blocks through the same Go APIs. This is the end-to-end gate; passing video-only gates is insufficient.

## Final recommendation

Do not replace the current WMV stub immediately with a partial decoder. Approve Phase 0 and Phase 1 only, with a go/no-go review after ASF parsing and a minimal WMV3 I-frame/progressive proof. If the product only needs silent logo frames, stop after the video-only acceptance gate and document the limitation. If it must reproduce the documented `logo.wmv` behavior, approve the full range including WMA2 and synchronization. In either case, keep the implementation pure Go, bounded, incremental and isolated under `internal/assets`, then connect it to the existing deterministic `VideoOverlay`/mixer contracts. 
