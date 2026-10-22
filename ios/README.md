# krkrgo iOS

The iOS launcher uses SwiftUI and hosts the Go/Ebitengine runtime through the
generated `Krkrgo.xcframework` binding. Project folders are selected with the
iOS document picker and accessed through a security-scoped bookmark.

## Build inputs

The build needs:

- Go 1.27 or newer on `PATH`.
- Xcode with the iOS and iOS Simulator SDKs.

FFmpeg is an optional runtime package. Set `KRKRGO_FFMPEG_IOS_DIR` to the
extracted iOS FFmpeg package directory when media decoding is desired. The
directory can contain `Frameworks/` and `LICENSES/FFmpeg/`.

## Compile

From the repository root:

```sh
scripts/build-ios.sh simulator
scripts/build-ios.sh device
```

With the optional runtime media package:

```sh
KRKRGO_FFMPEG_IOS_DIR=/path/to/ffmpeg-ios-xcframeworks scripts/build-ios.sh all
```

`scripts/build-ios.sh all` compiles both SDK targets. It generates the Go
binding under `ios/Frameworks/`, optionally embeds the FFmpeg dynamic
frameworks, and stores Xcode derived data under `temp/ios-deriveddata/`.
The Xcode target links only `Krkrgo.xcframework`; the Go runtime loads each
FFmpeg framework through the runtime loader when the files are present.
