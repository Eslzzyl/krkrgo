# krkrgo

`krkrgo` is a modern pure-Go implementation of the Kirikiri/Krkrz engine, built on [Ebitengine](https://ebitengine.org/).

The project brings the engine's scripting, storage, rendering, input, audio, and native-service model into a Go-native runtime. It targets macOS, Linux, and Windows as first-class desktop platforms.

## Highlights

- Go implementation of the TJS virtual machine and KAG execution model.
- Project startup from directory-based game installations.
- XP3 archive mounting, storage resolution, extraction, and resource caching.
- Layer trees, image compositing, transitions, hit testing, provinces, and input.
- Text and font rendering through Go image/text packages.
- Ebitengine window, presentation, input, and audio integration.
- Built-in compatibility surfaces for common Kirikiri native services and plugins.
- Deterministic headless replay and XP3 extraction tools for development and verification.
- Pure-Go desktop implementation with platform support centered on macOS, Linux, and Windows.

The implementation is actively expanding toward the behavior of the official C++ engine. Current compatibility gaps are tracked in the [verification and gaps guide](docs/verification-and-gaps.md). WMV playback remains a documented gap; the archive and ASF resources can be recognized and extracted, while full video playback is still pending.

## Quick start

### Start the GUI launcher

```sh
go run ./cmd/krkrgo
```

The equivalent single-file entry point is `go run ./cmd/krkrgo/main.go`; it also supports `run -project /path/to/project`.

### Run a project

```sh
go run ./cmd/krkrgo run -project /path/to/project
```

`-project` accepts a project root or a directory containing the project's runtime data (for example, `startup.tjs`, `data.xp3`). `-save-dir` overrides the save-data directory and `-budget` applies an optional instruction limit (zero means unlimited).

### Extract an XP3 archive

```sh
go run ./cmd/krkrgo-tool extract \
  --archive /path/to/data.xp3 \
  --output /tmp/krkrgo-extracted
```

The extractor verifies archive checksums and writes a manifest for entries that cannot be represented safely on the host filesystem.

### Run a deterministic replay

```sh
go run ./cmd/krkrgo-tool replay \
  --project /path/to/project \
  --input replay.json \
  --output result.json
```

Replay mode does not open an Ebitengine window. Its default tick step is 16 ms; `--step-ms`, `--advance-ms`, and `--budget` control deterministic verification runs.

## Development

Run the current-platform test suite:

```sh
go test ./...
```

Build the two command targets for the current platform:

```sh
go build -trimpath ./cmd/krkrgo
go build -trimpath ./cmd/krkrgo-tool
```

The repository keeps generated binaries, profiles, logs, local replay data, and local test fixtures out of version control through [.gitignore](.gitignore).

## Architecture

- `internal/tjs` — TJS lexer, compiler, VM, objects, dispatch, exceptions, and garbage collection.
- `internal/kag` — KAG tokenization and parser support.
- `internal/assets` — project storage, XP3 archives, image formats, fonts, and media resources.
- `internal/runtime` — sessions, layers, transitions, native services, timers, audio, plugins, and KAG execution.
- `internal/host` — Ebitengine presentation, input, audio output, and deterministic replay support.
- `cmd/krkrgo` — MyGo interactive game launcher and `run` game runtime.
- `cmd/krkrgo-tool` — extraction and headless replay utilities.

## Documentation

See the [documentation index](docs/README.md) for domain guides:

- [TJS and KAG](docs/tjs-kag.md)
- [Assets, storage, and media](docs/assets-storage-media.md)
- [Runtime and native services](docs/runtime-natives.md)
- [Graphics, layers, and text](docs/graphics-layer-text.md)
- [Audio and video](docs/audio-video.md)
- [Host, Ebitengine, platforms, and dependencies](docs/host-platform.md)
- [Verification and gaps](docs/verification-and-gaps.md)

## Reference implementations

- The official C++ [Kirikiri Z project](https://github.com/krkrz/krkrz) is the semantic and compatibility target.
- A Rust implementation [krkrz_rs](https://github.com/asukaminato0721/krkrz_rs)
- [krkr2](https://github.com/2468785842/krkr2)
