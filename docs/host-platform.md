# Host, Ebitengine, Platform, and Dependency Status

## Interactive and headless paths

[`cmd/krkrgo`](../cmd/krkrgo/main.go) is the combined desktop launcher and interactive game runtime. Running `krkrgo` opens the MyGo launcher; `krkrgo run -project DIR` opens a project-backed `runtime.Session` and runs its `startup.tjs` before entering Ebitengine's game loop. `-save-dir DIR` overrides the save-data location. `-renderer gpu|cpu` selects the interactive compositor, with GPU as the default. `-render-scale 1-4` overrides the rendering pixel density; the default 0 follows the monitor device scale factor, so the runtime buffers, composition, and text rasterization run at physical display resolution while layer coordinates stay logical. Script execution has no global instruction-count limit by default, matching the C++ interpreter; `-budget N` applies an optional instruction limit for diagnostics and tests. [`internal/host.Game`](../internal/host/ebiten.go) is the presentation boundary for one session: it samples input, sends deterministic runtime events, advances the session, and presents the latest bounded RGBA frame. VM and runtime state remain owned by `internal/runtime`.

`Session.CaptureFrame` and `Services.CaptureFrame` return independently owned snapshots. The host instead uses `Session.CaptureFrameForPresentation`, whose RGBA and province slices are borrowed and may be reused by the next capture; the host consumes them synchronously and must not retain or mutate them.


`DIR` may be either the directory containing `startup.tjs` or a project root whose `data/` subdirectory contains it. `runtime.OpenSession` checks `DIR/startup.tjs` first and then `DIR/data/startup.tjs`.

[`cmd/krkrgo-tool replay`](../cmd/krkrgo-tool/main.go) is the headless route and does not start Ebitengine. For a project replay, the command shape is:

```sh
go run ./cmd/krkrgo-tool replay --project DIR --input replay.json --output result.json
```

The tool opens project storage, runs `startup.tjs`, advances on a deterministic 16 ms tick by default, and writes frame/audio summaries and checkpoints. Replay `--budget N` sets the session's instruction budget (default `1048576`); it is not a duration or wall-clock timeout. `--step-ms` selects the tick step, and `--advance-ms` optionally advances the clock after the last input event.
[`cmd/krkrgo-tool extract --archive FILE --output DIR`](../cmd/krkrgo-tool/main.go) extracts readable XP3 entries into a new output directory, verifies each Adler-32 checksum, and writes names using the storage layer's normalized lowercase paths; an existing output directory is rejected. Entries with a path component over the portable 255-byte limit are skipped rather than truncated or overwritten, with their original name, Adler-32, size, and reason recorded in `__xp3_extract_manifest.json` (or a collision-free suffixed variant) and a warning written to stderr. XP3 protected-flag/zlib entries are decompressed by `Archive.Read`; for lovelyday, startup script failure is separate from archive bytes and stems from Shift-JIS text decoding in nested `Init.tjs`. The long-name manifest filename can coincide with an existing archive entry, so extraction reserves a collision-free manifest name.


The runtime `Window.mainWindow` object is registered before `startup.tjs` executes, so startup scripts can access it. This describes runtime window state; Ebitengine's `RunGame` begins after startup returns.

The Ebitengine host resizes its presentation window to each newly observed runtime frame size. Keeping the window and logical canvas dimensions equal avoids resampling game pixels (including antialiased text) through a non-integer fit scale.

[`internal/host/input.go`](../internal/host/input.go) converts Ebitengine keyboard, pointer, touch, wheel, text, and focus state into portable events. On backends that expose touch IDs, the primary contact maps to left-button down/move/up; release within a 10-logical-pixel slop also emits a click. Ebitengine's `AppendTouchIDs` returns no contacts on desktop, so direct touch polling is available only on backends that expose touch IDs. Audio output is separate and opt-in; see [Audio and video](audio-video.md). Dialogs, file dialogs, and shell execution are host-injected services, not automatic OS integrations.

## Dependencies

Versions below are declared by [`go.mod`](../go.mod), whose module directive is `go 1.25.0`. A dependency declaration does not guarantee every backend or feature on every platform.

| Module | Version/status | Current role |
| --- | --- | --- |
| `github.com/hajimehoshi/ebiten/v2` | `v2.10.4` | Selected engine for host input, windowing, rendering, and optional audio. |
| `github.com/atotto/clipboard` | `v0.1.4` | Clipboard text reads/writes. |
| `golang.org/x/text` | `v0.41.0` | Shift_JIS/text decoding and transformation. |
| `golang.org/x/image` | `v0.45.0` | Font/OpenType interfaces and fixed-point text layout. |
| `github.com/ebitengine/oto/v3` | `v3.5.0` indirect | Ebitengine device-audio path, used only through opt-in `host.AudioOutput`. |
| `github.com/jfreymuth/pulse` | `v0.1.3` indirect | Present in the Ebitengine audio dependency graph. |

## Platform policy and limits

The workspace execution contract identifies macOS, Linux, and Windows as first-class desktop targets and prioritizes a desktop path without cgo. It records a prior build baseline for all three desktop OSes at amd64, before audio-adapter integration. This is historical evidence, not a current all-platform verification; see [Verification and gaps](verification-and-gaps.md).

The contract requires Android and iOS support and allows cgo/C/C++ toolchains on mobile. This is target policy, not evidence of a completed or verified mobile build. Ebitengine and device-backend behavior must be assessed per platform/version. The contract records Oto v3.5.0 desktop Darwin/Linux/Windows driver paths as cgo-free and Android/console paths as using C/C++ toolchains; that dependency assessment does not prove all application audio features on all targets.

Linux `Clipboard` runtime use depends on external utilities. The pinned [`atotto/clipboard` v0.1.4 Unix backend](https://github.com/atotto/clipboard/blob/v0.1.4/clipboard_unix.go) uses `wl-copy`/`wl-paste` when available for Wayland, otherwise checks `xclip` and `xsel`; without a usable helper the calls fail. A successful build does not provide those utilities.

## C++ host comparison

The Go host uses Ebitengine for presentation and input rather than reimplementing the C++ host. Runtime window state and deterministic input do not imply all platform window-manager, dialog, or device behavior is present.

**C++ reference:** [Kirikiri Z source](https://github.com/krkrz/krkrz). The host/platform policy source is the workspace execution contract; Go dependency versions are in [`go.mod`](../go.mod).
