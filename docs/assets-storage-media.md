# Assets, Storage, and Media Porting Status

## Scope and C++ reference

[`internal/assets`](../internal/assets) owns project resource access and bounded format decoders. It follows useful Rust migration interfaces while the official C++ [Kirikiri Z source](https://github.com/krkrz/krkrz) remains the semantic target. The Go storage implementation currently supplies a local project backend, not the full C++ media/storage backend ecosystem.

## Storage and XP3

[`storage.go`](../internal/assets/storage.go) resolves loose project files and explicitly mounted XP3 archives. Names are normalized against the project; search paths are project-relative. Later search paths take precedence, while loose files take precedence over mounted archive entries.

For a project opened from its root with `data/startup.tjs`, runtime storage keeps the project root as its base (and therefore as `System.exePath`) while adding `data/` as a lower-priority search path. XP3 archives found in `data/` are mounted alongside root archives; resources can be addressed by archive basename or project-relative archive path, including executable-relative names such as `System.exePath + "data/image00.xp3>"`. The default save directory remains `<project root>/savedata`; opening `data/` itself still treats that directory as the project root.

[`xp3.go`](../internal/assets/xp3.go) supports ordinary XP3 archives and an XP3 archive embedded in an MZ executable. `findArchiveOffset` scans 16-byte boundaries within a configured bound; archive-relative index/segment offsets are translated through `Archive.baseOffset` when read. Segment data is bounded/cached. Cache clearing drops mounted XP3 segment data; there is no separate process-global archive-object cache.

These project-relative and executable-relative XP3 names are the resource paths used by the headless project runtime as well as ordinary storage reads; `Layer.loadImages` and media consumers resolve through the same mounted storage rather than requiring extracted loose-file copies.

`Storages.addArchive(path)` dynamically mounts a local XP3 archive addressed relative to the project (or by a normalized local `file://.` path). It adds the archive after current mounts, so its entries take precedence over earlier archive entries; malformed, missing, or non-local archive paths fail rather than being ignored.

- Runtime storage reads accept `file://.` paths under the active project or configured save directory. Absolute local paths are resolved through existing ancestors before component-aware containment checks, accepting symlink aliases of approved roots while rejecting outside targets. Writes through absolute project paths are redirected into the save overlay; relative storage names remain rooted in the save overlay before project lookup.

### Storage gaps

- Only local `file` media with domain `.` is available. Other media schemes return `assets.ErrUnsupportedStorageMedia`; non-current file domains and C++ media backends are not implemented.
- Dynamic current-domain/path state and platform-specific drive/UNC mapping are unavailable.
- `getFullPath` and successful `getPlacedPath` use normalized `file://.` names, not OS filesystem paths. Absolute project paths round-trip through storage resolution, including the case-normalized spelling returned by `getFullPath`; they are converted back to project-relative virtual names only when contained by the active project, with symlink containment enforced during lookup. Internal resource reads continue through secured filesystem/archive resolution.

## Resource and text formats

The README and sources record PSB v2 structured values/resources, SLI legacy/v2 loop metadata (labels, conditions, and link selection), and AMV container/alpha-packet handling. See [`psb.go`](../internal/assets/psb.go), [`sli.go`](../internal/assets/sli.go), and [`amv.go`](../internal/assets/amv.go).

[`text.go`](../internal/assets/text.go) decodes Kirikiri text streams, including UTF-16 representations and BOM-less UTF-8, Shift_JIS, and GBK sources. For non-UTF-8 input, the compatibility decoder compares strict Shift_JIS and GBK candidates using script-text character plausibility; malformed input is rejected rather than silently replaced. Until `Scripts.textEncoding` is explicitly assigned, `Scripts.execStorage` and `evalStorage` use this decoder; explicit UTF-8, Shift_JIS, and GBK settings select strict source decoders. TJS storage text decoding has a related encoding-aware path in [`scripts_native.go`](../internal/runtime/scripts_native.go).

## Image and audio data

- [`media.go`](../internal/assets/media.go) uses standard GIF/JPEG/PNG decoders and provides bounded BMP/TLG image paths through [`media_indexed.go`](../internal/assets/media_indexed.go) and [`tlg.go`](../internal/assets/tlg.go). TLG5 is supported; TLG6 is limited to 1, 3, or 4 channels, zero flags, Golomb entropy coding, and generic 8×8 filters. Other TLG6 modes are unsupported.
- WAVE decoding produces bounded interleaved signed-16 PCM. IEEE float32 and WAVE_FORMAT_EXTENSIBLE PCM/float sources are converted to that representation. Ogg/Vorbis is decoded in process to bounded signed-16 PCM.
- `assets.Audio` omits C++ `WaveIntf` metadata such as speaker mask, total sample/time fields, and seekability. Runtime stream and mixer behavior is described in [Audio and video](audio-video.md).

## C++ comparison and remaining scope

File storage, PSB/SLI/AMV handling, text conversion, and decoders are bounded migration components, not substitutes for all C++ plugins, codecs, media schemes, or platform path behavior. Unsupported formats/modes return typed errors.

**C++ reference:** [Kirikiri Z source](https://github.com/krkrz/krkrz), especially the project storage and resource-format implementation. No claim is made here that every C++ media backend is present.
