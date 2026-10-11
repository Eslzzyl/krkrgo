# Runtime and Native Services Porting Status

## Session and dispatch model

[`runtime.Session`](../internal/runtime/session.go) owns a TJS VM, runtime `Services`, and a shared execution budget. [`Services`](../internal/runtime/services.go) owns host-independent state and implements the TJS host-call boundary. [`NativeRegistry`](../internal/runtime/native.go) installs operation names and routes calls to services. This permits deterministic/headless use and interactive hosting; it does not create OS UI services by itself.

Native methods are bounded surfaces aligned with selected C++ behavior. Registration is in `session.go`/`native.go`; current behavior is summarized by domain below.

## Date

`Session` registers the TJS `Date` class using the existing `tjs.Date` calendar and parser implementation. The bounded bridge provides constructor initialization, date getters and setters, `getTime`/`setTime`, instance `parse`, and static `getTimezoneOffset`; stored timestamps retain the Go implementation's millisecond precision.

The TJS `Math` namespace exposes constants and the implemented numeric operations through the runtime native. `Math.random` uses the TJS xorshift generator (initialized with a fixed seed and explicitly seedable); its generator is package-level rather than session-local.

## Time and storage compatibility

Headless sessions advance a virtual clock as replay time moves forward; script waits are fulfilled by advancing that clock rather than sleeping for wall-clock time. The replay step defaults to 16 ms, with action timestamps and checkpoints evaluated against the same session timeline.

`Storages.fstat(path)` is implemented for the active project storage and returns a dictionary containing the resource `size`. It works for loose files and entries resolved from mounted XP3 archives; unavailable storage and missing-resource errors are not converted into fabricated metadata. This is a bounded file-size query, not the full `fstat.dll` mutation or directory API.

`TextStream.write(path, text, mode?)` honors the optional `oN` offset parsed from the mode string. A write at an offset replaces bytes from that position while preserving the preceding prefix; an offset at the current end appends, and gaps are zero-filled. File-path writes remain confined to the save overlay. Focused behavior tests cover `"o4"` replacement, `"o15"` append, and `Dictionary.saveStruct` with `"zo10"` followed by `Scripts.evalStorage(..., "o10")` reading the serialized value at that offset.


## System, storage, clipboard, and scripts

- [`system_native.go`](../internal/runtime/system_native.go) implements selected `System` values and operations: version/platform/width values, event/window flags, UUID generation, callbacks, cache/compaction/message operations, key state, and explicit termination requests. `System.getKeyState(key, current=true)` reads held keys/buttons from headless input events; `current=false` reads and consumes the recent-down state. A query in either mode clears that key's recent-down latch, and without input events both modes return `0`. `System.getArgument` uses exact key lookup; `System.setArgument` stringifies and stores values, with `void` stored as the empty string. Window/display-related operations also use [`window_native.go`](../internal/runtime/window_native.go).
- [`storages_native.go`](../internal/runtime/storages_native.go) exposes path helpers, project-storage queries, auto paths, and archive-cache clearing. `Storages.isExistentStorage` checks the save overlay before project storage; `TextStream` reads/writes resolve local `file://.` URIs under the configured save directory or project, with writes confined to the save overlay. File-media limits are in [Assets, storage, and media](assets-storage-media.md).
- [`clipboard_native.go`](../internal/runtime/clipboard_native.go) supplies a static, text-only `Clipboard.asText`/`hasFormat(1)` path through `atotto/clipboard`. Linux helper requirements are in [Host and platform](host-platform.md).
- [`scripts_native.go`](../internal/runtime/scripts_native.go) implements the supported source execution/evaluation, text-encoding, VM trace, and class-name surface. See [TJS and KAG](tjs-kag.md) for specific language/runtime gaps.

## Window focus

`Window.focusedLayer` returns the original `Layer` object when a layer has focus, or `null` when none does. Assigning a `Layer` requests focus through the same layer focus transition used by `Layer.focus()`; assigning `null` (or `void`) clears focus. Invalid non-Layer objects are rejected. Focus changes continue to update the `Layer.focused` state and deliver the layer focus/blur callbacks.

## Timer and event natives

[`timer_native.go`](../internal/runtime/timer_native.go) wraps runtime timers with `ActionOwner`/optional `ActionName`, fixed-point millisecond intervals, enabled/capacity/mode properties, and deferred callback delivery. The bridge covers bounded `normal`, `exclusive`, and `idle` modes; other C++ action-dispatch extensions are not implemented.

[`rect_native.go`](../internal/runtime/rect_native.go) implements four-edge `Rect` operations/properties. `Rect.nativeArray` returns a fresh safe TJS array rather than a C++ raw native pointer. [`async_trigger_native.go`](../internal/runtime/async_trigger_native.go) uses the runtime event queue for cached/coalesced delivery and bounded event priorities.
An empty `ActionName` calls `ActionOwner` itself; a non-empty name calls that owner member. Both forms receive one event dictionary with `type` set to `"onFire"` and `target` set to the trigger.

## Built-in plugin links

`Plugins.link` records plugin paths using slash-normalized, case-insensitive keys; `.tpm` and `.dll` names share a key, and `KAGParserEx` resolves to the built-in `KAGParser` module. Linking `menu` installs the headless `MenuItem` surface. `KAGParser` and WindowEx-compatible `Window` methods are already registered by the session, so their built-in plugin links preserve the compatibility surface without loading external libraries. The portable Window surface includes Win32 notification name/number lookup, non-client hit-test constants, extended event/hook state, geometry, and a retained `exSystemMenu` description; icon, overlay, z-order, menu-focus, and non-client hit-test host operations return explicit unsupported-device errors. Other native plugin names may be linked and recorded, but are not thereby installed or implemented.

The small compatibility links `getabout.dll`, `win32dialog.dll`, and `fftgraph.dll` use the same case-insensitive `.dll`/`.tpm` path normalization:

`getabout.dll` exposes `System.getAboutString()` (`cpp/plugins/getabout.cpp:4-5`). This method is registered when each Session is initialized and is callable before or after the plugin link; linking records plugin compatibility rather than dynamically adding the method. It returns the existing Go version-information value because the runtime has no executable about-resource/important-log input; it does not synthesize resource text. The reference `TVPGetAboutString` formats version-resource content and appends important logs (`msg/MsgIntf.cpp:162-175`; resource reader `win32/MsgImpl.cpp:45+`).

`win32dialog.dll` evaluates the `WIN32Dialog` class with `messageBox(message, caption, type)` (`cpp/plugins/win32dialog.cpp:6-10`). The compatibility method routes through the host dialog handler and returns true, matching `!System.inform(...)` when that void-returning call completes; absent a handler it returns `ErrDialogUnavailable`.

`fftgraph.dll` installs the global `drawFFTGraph()` (`cpp/plugins/fftgraph.cpp:5-7`). The C++ function is an empty no-op, but the Go runtime has no FFT renderer contract, so invoking it returns an explicit unsupported-device error rather than implying that a graph was drawn.

## Graphics and media plugin compatibility

The krkr2 C++ plugin sources expose substantially larger APIs than the Go host currently implements. The Go runtime has no PSB/PSD decoder, no GDI+/Anti-Grain `layerExDraw` renderer, and no native video presentation backend for the `layerExMovie` Layer extension. It does already provide general-purpose PNG/JPEG/BMP image loading/saving, Layer image operations, XP3 archive reading, and a headless `VideoOverlay` path; these facilities are not aliases for the plugin-specific formats or presentation behavior.

- `psbfile.dll` registers `PSBFile` with `load` and `root` (`cpp/plugins/psbfile/main.cpp`). `psdfile.dll` and `psbfile.dll` are not implemented in Go; linking their names does not load a DLL or install a fake parser.
- `layerExDraw.dll` is a large GDI+-style geometry, image, brush, pen, path, and graphics surface (`cpp/plugins/layerex_draw/`). `layerExPerspective.cpp` installs the perspective-copy Layer method. Neither plugin surface is represented by the current Go Layer renderer.
- `layerExMovie.dll` attaches `openMovie`, `startMovie`, `stopMovie`, and `isPlayingMovie` to Layer (`cpp/plugins/layerExMovie.cpp`). Those four methods return `ErrUnsupportedDevice` with a video-backend explanation; they never silently claim to open or play a movie. This remains distinct from the Go `VideoOverlay` native.
- `motionplayer.dll` / `emoteplayer.dll` register PSB/MTN resource managers, animation players, adaptors, and related classes (`cpp/plugins/motionplayer/main.cpp`, `cpp/plugins/EmotePlayer.cpp`). The Go runtime does not emulate these APIs. `addFont.dll` only adds `System.addFont` (`cpp/plugins/addFont.cpp`); it is not conflated with Go's explicit font-book configuration.
- `fstat.dll` adds `Storages` file/directory metadata and mutation helpers (`cpp/plugins/fstat/main.cpp`); Go currently provides only the bounded `Storages.fstat(path)` size query described above. `xp3filter.dll` is an archive-filter plugin (`cpp/plugins/xp3filter.cpp`) with repacking/filter-script behavior and is not installed by linking the name. The Go XP3 archive reader remains available through normal storage access.
- `wutcwf.dll` is not part of the local krkr2 plugin registration inventory. No compatible Go API is inferred from the filename.

Unimplemented plugin calls continue through the normal unsupported-native path unless a method is explicitly routed to a device limitation, as with the `layerExMovie` methods. Plugin-name tracking is not a promise that those plugin APIs are available.

## Script and data plugin compatibility

The built-in `Scripts` class includes the pure-Go `ScriptsEx` helpers `getObjectKeys`, `getObjectCount`, `getMD5HashString`, `equalStruct`, and `equalStructNumericLoose`. Object keys are returned in deterministic UTF-16 order; structural equality recursively compares arrays and dictionaries, and the numeric-loose variant also equates integer and real numeric values. `getMD5HashString` accepts an Octet and returns lowercase hexadecimal MD5. The core VM serialization registration provides `saveStruct`/`saveStruct2` and `toStructString` for arrays and dictionaries, with the existing `Array.save`/`load` binary serialization surface kept separate. `TextRenderBase` is a built-in TJS native backed by Go text-render state, not an external DLL.

Linking `csvParser.dll` activates `CSVParser` (`cpp/plugins/csvParser.cpp`) with `init`, `initStorage`, `getNextLine`, `parse`, `parseStorage`, and read-only `currentLineNumber`. It returns TJS arrays, combines quoted physical lines using the configured newline string, supports doubled quotes and custom single-rune separators, and invokes `doLine(fields, recordNumber)` for parse calls. Storage input follows the runtime text decoder unless the UTF-8 flag is set, in which case bytes are interpreted as UTF-8. The Go CSV reader accepts malformed quote syntax with `LazyQuotes`; the C++ parser has its own permissive recovery rules, so malformed-input edge cases are not promised byte-for-byte identical.

Linking `varfile.dll` activates the `var://./...` storage subset (`cpp/plugins/varfile.cpp`). Paths traverse global TJS objects/dictionaries (including arrays by numeric member), leaf files must be Octet values, writes replace the leaf with an Octet, `Storages.isExistentStorage` checks Octet leaves, and `Storages.getListAt` returns Octet member names sorted deterministically. This maps the C++ in-memory stream's read/write and file-list behavior without host filesystem access. Domain names other than `.` are rejected; TJS object properties are the only directory representation, and `varfile` append/update seek modes, local OS paths, and non-Octet stream values are not exposed by this text/storage API. Linking either name does not load an external DLL.
## C++ comparison and semantic limits

- Unsupported native/device calls return errors rather than silently succeeding. Dialogs, file dialogs, and shell execution require handlers supplied by a host; absent handlers report unavailable errors.
- `System.versionString`/`versionInformation` are stable `krkrgo` project values. `platformName` is the deterministic `GOOS/GOARCH` value.
- Without a platform system-color table, `System.toActualColor` preserves the low 24-bit RGB payload instead of applying a C++ system-color mapping.
- `System.exit` records the requested code and returns `ErrSystemExit`; a Go library does not terminate its embedding process.

Graphics natives are detailed in [Graphics, layers, and text](graphics-layer-text.md); sound/video natives are in [Audio and video](audio-video.md).

**C++ reference:** [Kirikiri Z source](https://github.com/krkrz/krkrz). The runtime APIs here model a bounded Go subset and do not imply full native API parity.
