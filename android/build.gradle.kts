plugins {
    id("com.android.application") version "8.7.3" apply false
    id("org.jetbrains.kotlin.android") version "2.0.21" apply false
    id("org.jetbrains.kotlin.plugin.compose") version "2.0.21" apply false
}
val goRoot = rootProject.file("..")
val mobileBinding = rootProject.file("app/libs/krkrgo.aar")

val generateGoBinding by tasks.registering {
    description = "Generates the Android AAR from the Go mobile host."
    group = "build"
    inputs.dir(goRoot.resolve("mobile/krkrgo"))
    inputs.file(goRoot.resolve("go.mod"))
    inputs.file(goRoot.resolve("go.sum"))
    inputs.file(goRoot.resolve("internal/version/version.xcconfig"))
    outputs.file(mobileBinding)

    doLast {
        mobileBinding.parentFile.mkdirs()
        exec {
            workingDir(goRoot)
            commandLine(
                "go",
                "run",
                "github.com/hajimehoshi/ebiten/v2/cmd/ebitenmobile@v2.10.5",
                "bind",
                "-target",
                "android",
                "-androidapi",
                "24",
                "-javapkg",
                "org.eu.eslzzyl.krkrgo.mobile",
                "-o",
                "android/app/libs/krkrgo.aar",
                "./mobile/krkrgo",
            )
        }
    }
}
