# Launcher Architecture

This document records the launcher design for desktop and mobile targets.
The process model is platform-specific because desktop and mobile expose
different application-hosting APIs.

## Role

The launcher provides visual control for project selection, save-directory
configuration, budget configuration, and game start.

The launcher and game runtime share one executable with explicit dispatch:
no arguments (or launcher options) open the GUI, while `run` starts a game.
Direct runtime and tool commands are:

```text
krkrgo run -project <directory> [-save-dir <directory>] [-budget <instructions>] [-renderer gpu|cpu] [-render-scale 1-4] [-upscaler off|bilinear|bicubic|anime4k]
krkrgo-tool replay|extract ...
```

`-upscaler` selects the frame post-processor: `bilinear`, `bicubic`, or `anime4k`. It requires the GPU renderer and a resolved render scale of 2; the runtime composes at 1x and presents the 2x post-processed frame. `-anime4k` is an alias for `-upscaler anime4k`. See [Frame upscaling](upscaling.md).

The game runtime, TJS VM, compositor, and presentation layer remain shared.
The launcher supplies host-specific UI and lifecycle control.

## Desktop: separate process

The desktop launcher runs as an independent process.

```text
launcher process (cmd/krkrgo, MyGo native UI)
  - selects a project directory
  - configures save-dir and budget
  - starts the same krkrgo executable with `run` and constructed arguments
  - monitors the child process and its log

game process (cmd/krkrgo run)
  - parses -project, -save-dir, -budget, -renderer, -render-scale, -upscaler, -anime4k, and repeated -game-arg values
  - creates the runtime Session
  - creates the Ebitengine host
  - runs ebiten.RunGame
```

### Why separate process

The launcher starts its own executable with a `run` subcommand and passes
arguments as an array. Separate processes keep MyGo's native event loop and
Ebitengine's game window independent. A TJS panic or native game failure leaves
the launcher available for configuration and log inspection.

The model also fits a future streaming client in which the launcher selects a
remote or cached project and starts a local game client. Streaming remains a
future extension; it is not required by the current launcher implementation.


### Window model

The launcher and game windows may remain open simultaneously. The launcher
shows static configuration UI and the game owns the Ebitengine presentation
window. The user can switch between them through normal OS window management.

The launcher can also hide its window while the game is active and restore it
when the child process exits. This is a UI policy choice and does not change
the process model.

### GUI framework

The launcher is a native-UI application built with
`github.com/egoist/mygo`:

- Go-native widgets, layouts, dialogs, text input, virtualized lists, and
  icons.
- Desktop APIs for menus, notifications, file associations, and window state.
- Native application packaging through the MyGo build tool.
- A CGO-disabled Go build path through MyGo's purego backend.

On Linux, MyGo Native UI uses system GTK3, GLib, Cairo, and related graphics
components at runtime. These are platform prerequisites; the launcher does
not ship an additional game library or webview.

The `internal/launcher` package does not import Ebitengine or the game runtime.
The combined executable starts itself in `run` mode with `os/exec` and passes
arguments without shell interpolation.

### Localization

The launcher uses MyGo catalogs, the same mechanism as MyGo's controls and
menus. The configuration lives in `mygo.json` at the repository root:

```json
{
  "name": "krkrgo",
  "identifier": "com.eslzzyl.krkrgo",
  "main": "./cmd/krkrgo",
  "i18n": {
    "dir": "internal/launcher/locales",
    "source": "en"
  }
}
```

`main` names the launcher command package, so `mygo generate` can resolve the
packages the app is built from and verify that the catalogs are imported.

The catalog directory holds one ARB-format JSON file per language:

```text
internal/launcher/locales/
├── en.json          source language, every message
├── zh-Hans.json     Simplified Chinese
├── catalog_gen.go   written by mygo generate
└── catalog_test.go  key parity and formatting checks
```

Messages use ICU MessageFormat. Keys are flat and named after generated
functions: `launchGame` becomes `locales.LaunchGame()`. Placeholders carry
types in the source catalog:

```json
"catalogLoadFailed": "Could not load game catalog: {error}",
"gamesReady": "{count, plural, one {1 game ready.} other {# games ready.}}",
"@gamesAddedWithFailures": {"placeholders": {"failures": {"type": "String"}}},
"gamesAddedWithFailures": "{count, plural, one {Added 1 game. {failures}} other {Added # games. {failures}}}"
```

The generator derives Go signatures from those declarations:

```go
locales.CatalogLoadFailed(err.Error())
locales.GamesReady(len(a.catalog.Games))
locales.GamesAddedWithFailures(failures, added)
locales.Instructions(strconv.FormatUint(game.Budget, 10))
```

`catalog_gen.go` registers the catalog in `init`, which makes the catalog's
languages the application's languages. `cmd/krkrgo/main.go` imports the package
with a blank import to make that registration explicit at the application
entry point. The launcher reads the active language through MyGo's locale and
does not call `SetLanguages`.

Language changes rebuild the application menu and reset the status line in
`OnLocaleChanged`; message calls during rendering pick up the new catalog
immediately.

After changing any JSON catalog, regenerate the accessors and commit both the
catalog and the generated file:

```sh
go run github.com/egoist/mygo/cmd/mygo@v0.5.5 generate
go test ./internal/launcher/...
```

`catalog_test.go` checks that English and Chinese carry the same message keys,
that no message is empty, and that plural and placeholder formatting matches
the generated API.

## Mobile: native library in the application process

Mobile uses one application package and one main application process. The
native launcher UI and the Go/Ebitengine game host belong to the same mobile
application target.

```text
application process
├── native launcher UI
│   ├── Jetpack Compose on Android
│   └── SwiftUI on iOS
└── Go/Ebitengine native library
    ├── runtime.Session
    └── Ebitengine mobile view
```

### Ebitengine mobile binding

The mobile host uses the official `ebitenmobile bind` workflow. The Go
package registers the game with `github.com/hajimehoshi/ebiten/v2/mobile`
through `mobile.SetGame`.

The mobile host does not call `ebiten.RunGame`. The generated bindings provide:

- Android: an AAR containing the `EbitenView` class.
- iOS: an XCFramework containing the generated Ebitengine view controller.

The official Ebitengine mobile backend currently uses OpenGL ES on Android
and Metal on iOS. The native binding hides the backend choice from the host.

Reference: <https://ebitengine.org/en/documents/mobile.html>

### Mobile lifecycle

The shared Go host exposes lifecycle operations for the native UI:

```text
OpenGame(project)
PauseGame()
ResumeGame()
CloseGame()
```

The native host forwards Activity or scene lifecycle events to the Go host.
The launcher observes game completion through an in-process callback and
returns to its launcher screen.

The runtime core and game logic are shared with desktop. The mobile entry
point, native view binding, lifecycle forwarding, and file access adapter are
mobile-specific.

### Mobile project storage

The launcher imports or opens projects through the platform file picker:

- Android receives a Storage Access Framework URI.
- iOS receives a security-scoped document URL.

The first implementation copies the selected project or XP3 archive into the
application sandbox and records the imported path in the catalog. A
provider-backed storage adapter can support large or remote projects later.

### Mobile process choice

An Android package can technically declare additional private processes.
The primary design uses one process because `ebitenmobile` supplies an
in-process View designed for a native Activity, and the launcher/game
lifecycle can remain in one application target.

iOS uses the main application process. App extensions and XPC services have
different lifecycles and APIs, so they are not used as the game host.

## Resource contention

The mobile launcher screen is static while the game view is active. The
launcher does not require a continuous 60 FPS redraw. CPU, GPU, audio, and
memory usage still require profiling on low-end Android devices and older
iOS hardware.

The former `~9 ms/frame` value is a measurement for a particular runtime
scenario. It is not a platform-wide constant and does not belong in the
architecture contract without device and workload details.

## Platform comparison

| | Desktop | Mobile |
| --- | --- | --- |
| Launcher UI | MyGo native UI process | Compose/SwiftUI in the app process |
| Game runtime | `krkrgo run` child process | Go/Ebitengine native library |
| Launcher → game | `os/exec` argument array | Host API and lifecycle callback |
| Process isolation | Yes | No |
| Runtime core | Shared | Shared |
| Host entry | `krkrgo` executable (`run` mode) | Android AAR / iOS XCFramework |

The launcher responsibilities remain consistent across platforms: select a
project, configure runtime options, start the game, and present errors.
Process monitoring means child-process monitoring on desktop and lifecycle
callback monitoring on mobile.

## Future asset streaming

Future asset streaming can fit both host models. The launcher can obtain an
asset manifest, configure a storage implementation with HTTP and local
caching, then start the game.

The current launcher does not require a network service. Any streaming
implementation must preserve the runtime storage contract and receive its
own platform-specific verification.
