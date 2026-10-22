import java.util.Properties

plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
    id("org.jetbrains.kotlin.plugin.compose")
}

val versionProperties = Properties().apply {
    rootProject.file("../internal/version/version.xcconfig").inputStream().use { load(it) }
}

val ffmpegAndroidDir = providers.gradleProperty("krkrgoFfmpegAndroidDir")
    .orElse(providers.environmentVariable("KRKRGO_FFMPEG_ANDROID_DIR"))
val generatedFfmpegJniLibs = layout.buildDirectory.dir("generated/ffmpeg-jniLibs")
val androidFfmpegAbis = listOf("arm64-v8a", "x86_64", "armeabi-v7a", "x86")
val androidFfmpegLibraries = listOf(
    "libavutil.so",
    "libavutil.so.61",
    "libavcodec.so",
    "libavcodec.so.63",
    "libavformat.so",
    "libavformat.so.63",
    "libswscale.so",
    "libswscale.so.10",
)

val prepareFfmpegAndroid by tasks.registering {
    description = "Copies Android FFmpeg shared libraries into the APK JNI source set."
    group = "build"
    if (ffmpegAndroidDir.isPresent) {
        inputs.dir(ffmpegAndroidDir)
    }
    outputs.dir(generatedFfmpegJniLibs)

    doLast {
        val sourceValue = ffmpegAndroidDir.orNull
        if (sourceValue.isNullOrBlank()) {
            logger.lifecycle("Android FFmpeg libraries are not configured; media decoding remains unavailable in this APK.")
            return@doLast
        }

        val sourceRoot = file(sourceValue)
        var copied = false
        androidFfmpegAbis.forEach { abi ->
            val candidates = listOf(
                sourceRoot.resolve(abi),
                sourceRoot.resolve("lib").resolve(abi),
                sourceRoot.resolve("jniLibs").resolve(abi),
            )
            val sourceDir = candidates.firstOrNull { candidate ->
                androidFfmpegLibraries.any { library -> candidate.resolve(library).isFile }
            } ?: return@forEach

            copy {
                from(sourceDir)
                include(androidFfmpegLibraries)
                into(generatedFfmpegJniLibs.get().asFile.resolve(abi))
            }
            copied = true
        }

        check(copied) {
            "Android FFmpeg directory contains no supported ABI libraries: $sourceRoot"
        }
    }
}

android.sourceSets.getByName("main").jniLibs.srcDir(generatedFfmpegJniLibs)

tasks.named("preBuild") {
    dependsOn(rootProject.tasks.named("generateGoBinding"))
    dependsOn(prepareFfmpegAndroid)
}

android {
    namespace = "org.eu.eslzzyl.krkrgo"
    compileSdk = 35

    defaultConfig {
        applicationId = "org.eu.eslzzyl.krkrgo"
        minSdk = 24
        targetSdk = 35
        versionCode = versionProperties.getProperty("KRKRGO_BUILD").toInt()
        versionName = versionProperties.getProperty("KRKRGO_VERSION")
    }

    buildTypes {
        release {
            isMinifyEnabled = false
            proguardFiles(
                getDefaultProguardFile("proguard-android-optimize.txt"),
                "proguard-rules.pro",
            )
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    kotlinOptions {
        jvmTarget = "17"
    }

    buildFeatures {
        compose = true
    }
}

dependencies {
    implementation(files("libs/krkrgo.aar"))

    implementation(platform("androidx.compose:compose-bom:2024.12.01"))
    implementation("androidx.activity:activity-compose:1.10.0")
    implementation("androidx.compose.foundation:foundation")
    implementation("androidx.compose.material:material-icons-extended")
    implementation("androidx.compose.material3:material3")
    implementation("androidx.documentfile:documentfile:1.1.0")
    implementation("androidx.compose.ui:ui")
    implementation("androidx.compose.ui:ui-tooling-preview")
}
