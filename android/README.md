# krkrgo Android

The Android launcher uses Jetpack Compose and hosts the Go/Ebitengine runtime
through the generated `krkrgo.aar` binding.

The package identity is `org.eu.eslzzyl.krkrgo`. The generated Go binding uses
`org.eu.eslzzyl.krkrgo.mobile` so its Java classes stay separate from the app
entry point.

## Build

From this directory, run:

```powershell
.\gradlew.bat assembleDebug
```

The `preBuild` task generates `app/libs/krkrgo.aar` from
`../mobile/krkrgo` before compiling the Compose app. The AAR is a generated
local build input and is excluded from Git.
