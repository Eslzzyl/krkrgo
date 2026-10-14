# AGENTS.md

## Project Overview

`krkrgo` is a Go and Ebitengine implementation of the Kirikiri/Krkrz engine. It provides a TJS virtual machine, KAG execution, XP3 resource access, graphics composition, input, audio, media, and desktop runtime support for macOS, Linux, and Windows.

## Directory Structure

- `cmd/`: Executable entry points.
  - `cmd/krkrgo`: Graphical launcher and game runtime command.
  - `cmd/krkrgo-tool`: XP3 extraction and headless replay utilities.
  - `cmd/diag`, `cmd/gpu_probe`, `cmd/tmp*`: Diagnostic and development utilities.
- `internal/tjs/`: TJS lexing, compilation, bytecode, virtual machine, object system, dispatch, exceptions, and garbage collection.
- `internal/kag/`: KAG tokenization, parsing, and related support.
- `internal/assets/`: Project storage, XP3 archives, images, fonts, media, PSB, and related resources.
- `internal/runtime/`: Sessions, KAG execution, layers, transitions, timers, audio, plugins, native services, and window runtime.
- `internal/host/`: Ebitengine window, input, audio output, CPU/GPU composition, and deterministic replay.
- `internal/core/`: Project configuration, common limits, path handling, and shared data structures.
- `internal/media/`: Media decoding; `internal/media/ffmpeg/` loads FFmpeg shared libraries and performs FFmpeg-backed decoding.
- `internal/launcher/`: Game directory inspection, launcher catalog, and game entry management.
- `internal/krkrgocli/`: `krkrgo run` command-line arguments and runtime flow.
- `lib/`: FFmpeg sidecar libraries, headers, licenses, and related local library content. Generated content is generally managed by `.gitignore`.
- `scripts/`: Build helper scripts, primarily `build-ffmpeg.sh`.
- `docs/`: Architecture, porting status, runtime, and verification documentation. Start at `docs/README.md`.
- `reference/`: Semantic and compatibility reference projects, including the official C++ Kirikiri Z project, `krkr2-main`, Rust `krkrz_rs`, FreeMote, LunaTranslator, and FFmpeg. When behavior differs or an implementation issue appears, inspect the corresponding source, scripts, and documentation in these projects.
- `testdata/`: Test game projects. Current projects include `amairo`, `islanddiary`, and `lovelyday`; they support startup, resource, and compatibility checks.
- `replays/`: Local replay inputs and result data.
- `temp/`: Project-local temporary data. Use this directory for temporary files, diagnostic output, build artifacts, and experimental data.
- `agents/`: Project collaboration rules and execution notes.

## Common Commands

Run commands from the repository root:

```sh
# Start the graphical launcher
go run ./cmd/krkrgo

# Run a game project directly
go run ./cmd/krkrgo run -project /path/to/project

# Run all Go tests
go test ./...

# Build the two main commands
go build -trimpath ./cmd/krkrgo
go build -trimpath ./cmd/krkrgo-tool

# Build all Go packages
go build ./...

# Extract an XP3 archive
go run ./cmd/krkrgo-tool extract \
  --archive /path/to/data.xp3 \
  --output temp/krkrgo-extracted

# Run a deterministic replay
go run ./cmd/krkrgo-tool replay \
  --project /path/to/project \
  --input replays/input.json \
  --output temp/replay-result.json
```

Output paths may use dedicated subdirectories under `temp/`. Build the FFmpeg sidecar with `scripts/build-ffmpeg.sh`; the script requires a verified FFmpeg source directory, target platform, and target architecture supplied through environment variables. Refer to `README.md` and the script for the complete parameter set.

## Working Conventions

- Read `README.md`, relevant `docs/` pages, and the target package's existing code before editing. Follow existing interfaces, error handling, and test patterns.
- For TJS, KAG, storage, layer, media, or plugin behavior differences, first inspect the corresponding implementations in `reference/krkrz`, `reference/krkr2-main`, and `reference/krkrz_rs`. For PSB and FreeMote-related issues, inspect `reference/FreeMote`.
- Use shell commands such as `find`, `grep`, `git grep`, and `cat` when searching files. The repository contains ignored reference source and game data, which generic file-search tools may skip.
- Test game projects are under `testdata/`. Before running one, confirm the project path, required shared libraries, and the local graphics and audio environment.
- Write all temporary data under `temp/`, using clear subdirectories or filenames for separate tasks.
- After code changes, run the Go tests related to the change. For changes spanning multiple packages, run `go test ./...` and, when appropriate, `go build ./...`.
- Update `README.md` and relevant `docs/` pages when changing command-line arguments, directory layout, or runtime flow.
- Keep generated files, logs, replay results, and local test artifacts in `temp/`, `replays/`, or another established location. Do not add local game data or build artifacts to commits.
