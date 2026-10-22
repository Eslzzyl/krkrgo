# Android Development Notes

This directory contains the experimental Android launcher for `krkrgo`.
The launcher uses Jetpack Compose and embeds the Go/Ebitengine runtime through
the generated `app/libs/krkrgo.aar` binding.

## Identity and architecture

- Application package: `org.eu.eslzzyl.krkrgo`.
- Android namespace: `org.eu.eslzzyl.krkrgo`.
- Generated Go binding package: `org.eu.eslzzyl.krkrgo.mobile`.
- `MainActivity` owns the Compose launcher and the embedded game screen.
- `mobile/krkrgo/bridge.go` owns the native runtime session and forwards
  `OpenGame`, `PauseGame`, `ResumeGame`, and `CloseGame` to the Ebitengine view.
- The game screen and launcher share one Android activity. The game is a view
  inside that activity rather than a second Android window.
- The launcher follows the system orientation. Successful game launch locks
  the activity to landscape, and the in-app Back action restores the system
  orientation.

## Toolchain

- Android Studio is not required for the command-line build.
- Use JDK 17 for the Android Gradle Plugin and Kotlin target.
- `android/gradlew.bat` is the repository build entry point and must remain
  available in the repository.
- The `preBuild` task runs `ebitenmobile bind` and generates
  `app/libs/krkrgo.aar` from `../mobile/krkrgo`.
- The generated AAR is a local build input and is ignored by Git.

Build from this directory:

```powershell
.\gradlew.bat assembleDebug
```

The build requires `go` on `PATH`, an Android SDK with platform 35 and build
tools installed, and a JDK 17 installation selected through `JAVA_HOME`.

## Android FFmpeg libraries

The launcher packages `libavutil.so`, `libavcodec.so`, `libavformat.so`, and
`libswscale.so` for each available ABI. The FFmpeg workflow produces these
libraries for `arm64-v8a` and `x86_64`; the Android build accepts an aggregate
directory through `KRKRGO_FFMPEG_ANDROID_DIR` or
`-PkrkrgoFfmpegAndroidDir=...`. The directory may contain ABI folders directly,
under `lib/`, or under `jniLibs/`.

Without that setting the launcher still builds, and the APK contains no
FFmpeg native libraries. Generated native libraries remain build inputs and
stay outside Git.

## Emulator setup

The validated AVD is `krkrgo-api35` with Android 35, x86_64, four virtual CPU
cores, and a 1080x2400 display.

Start it with host GPU acceleration:

```powershell
& "$env:ANDROID_HOME\emulator\emulator.exe" `
  -avd krkrgo-api35 `
  -gpu host `
  -no-boot-anim
```

`-gpu auto` is also supported. The `swiftshader_indirect` mode uses a software
renderer and can push `qemu-system-x86_64` to a high host CPU load while the
game is drawing. The validated host renderer reported the NVIDIA host driver
and reduced QEMU usage from roughly 68-71% to roughly 6% of the 16-logical-CPU
machine during the `amairo` title screen.

Verify the active renderer:

```powershell
adb -s emulator-5554 shell dumpsys SurfaceFlinger | Select-String "GLES:"
```

The output should identify the host GPU vendor and driver. `Google SwiftShader`
indicates software rendering.

Verify the Android process and host process separately:

```powershell
adb -s emulator-5554 shell pidof org.eu.eslzzyl.krkrgo
adb -s emulator-5554 shell top -b -n 1 -p <pid> -o PID,CPU,ARGS
```

Windows Task Manager attributes much of the emulator graphics work to the QEMU
process. A low Android package CPU value can therefore coexist with a high
QEMU value when SwiftShader is active.

## Project import and real-game validation

- The launcher uses Android's Storage Access Framework directory picker.
- The user selects a project directory; manual path entry is not part of the
  mobile flow.
- `ProjectImporter` validates the selected tree through `DocumentFile` and
  stores its persisted `content://` URI. Project files remain in their source
  folder and are read through `ContentUriStorage` during startup and gameplay.
- `PersistentProjectCatalog` stores the project name and URI in app preferences.
  The Android framework's persisted read permission keeps the source available
  across process restarts; save files are written under the selected project's
  `savedata/` directory through the same persisted tree URI.
- `testdata/amairo` is the validated real fixture. It contains roughly 1.3 GiB
  and 14 XP3 archives, and it reaches the title menu on the emulator.
- A successful build alone does not validate the runtime. Launch the real
  fixture, confirm the title screen, and collect CPU and frame data there.

Useful checks:

```powershell
adb -s emulator-5554 shell dumpsys activity activities |
  Select-String "mResumedActivity|mFocusedApp"
adb -s emulator-5554 shell dumpsys gfxinfo org.eu.eslzzyl.krkrgo
```

The game screen has an in-app `Back` button. The Android system Back key can
finish `MainActivity` and return to the emulator home screen, so use the
in-app button when checking the launcher-to-game handoff.

## Runtime timing

`internal/host/ebiten.go` advances the runtime with a fixed 16 ms logical tick
per Ebitengine update. The Android bridge selects the GPU renderer and uses a
scale of 1. A display running through SwiftShader makes this fixed-rate draw
path expensive on the host; host GPU mode is required for meaningful emulator
performance measurements.

## Android verification checklist

1. Run `.\gradlew.bat assembleDebug` from `android/`.
2. Install the debug APK with `adb install -r`.
3. Start the AVD with `-gpu host` or `-gpu auto`.
4. Verify the `GLES:` renderer before measuring performance.
5. Import a real project through the directory picker.
6. Launch the project and confirm a real title screen.
7. Record Android package CPU, QEMU CPU, renderer, and `gfxinfo` frame data.

Keep generated AAR files, APKs, logs, screenshots, and imported game data out
of Git. Store temporary diagnostics under the repository `temp/` directory.
