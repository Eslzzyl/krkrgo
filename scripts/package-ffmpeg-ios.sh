#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
INPUT_DIR=${1:-"$ROOT_DIR/.build/ffmpeg-ios-slices"}
OUTPUT_DIR=${2:-"$ROOT_DIR/.build/ffmpeg-ios-xcframeworks"}

fail() {
    printf 'package-ffmpeg-ios: %s\n' "$*" >&2
    exit 1
}

need_command() {
    command -v "$1" >/dev/null 2>&1 || fail "required command not found: $1"
}

need_command xcodebuild
need_command lipo

slices=(
    ios-device-arm64
    ios-simulator-arm64
    ios-simulator-x86_64
)
libraries=(
    libavutil.61.dylib
    libavcodec.63.dylib
    libavformat.63.dylib
    libswscale.10.dylib
)

for slice in "${slices[@]}"; do
    slice_dir="$INPUT_DIR/ffmpeg-$slice"
    [[ -d "$slice_dir" ]] || fail "iOS FFmpeg slice is missing: $slice_dir"
    [[ -d "$slice_dir/include" ]] || fail "FFmpeg headers are missing: $slice_dir/include"
    for library in "${libraries[@]}"; do
        [[ -f "$slice_dir/lib/ffmpeg/$library" ]] || fail "FFmpeg dynamic library is missing: $slice_dir/lib/ffmpeg/$library"
    done
done

rm -rf "$OUTPUT_DIR"
mkdir -p "$OUTPUT_DIR/Frameworks" "$OUTPUT_DIR/LICENSES/FFmpeg"

for library in "${libraries[@]}"; do
    name=${library%%.*}
    for slice in "${slices[@]}"; do
        slice_dir="$INPUT_DIR/ffmpeg-$slice"
        framework_dir="$slice_dir/frameworks/$name.framework"
        rm -rf "$framework_dir"
        mkdir -p "$framework_dir/Headers"
        cp "$slice_dir/lib/ffmpeg/$library" "$framework_dir/$name"
        cp -R "$slice_dir/include/." "$framework_dir/Headers/"
        cat > "$framework_dir/Info.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>CFBundleDevelopmentRegion</key>
    <string>en</string>
    <key>CFBundleExecutable</key>
    <string>$name</string>
    <key>CFBundleIdentifier</key>
    <string>org.eu.eslzzyl.krkrgo.ffmpeg.$name</string>
    <key>CFBundleInfoDictionaryVersion</key>
    <string>6.0</string>
    <key>CFBundleName</key>
    <string>$name</string>
    <key>CFBundlePackageType</key>
    <string>FMWK</string>
    <key>CFBundleShortVersionString</key>
    <string>9.0.2</string>
    <key>CFBundleVersion</key>
    <string>9.0.2</string>
</dict>
</plist>
EOF
        install_name_tool -id "@rpath/$name.framework/$name" "$framework_dir/$name"
        while IFS= read -r dependency_path; do
            dependency=$(basename "$dependency_path")
            case "$dependency" in
                libavutil.61.dylib|libavcodec.63.dylib|libavformat.63.dylib|libswscale.10.dylib)
                    dependency_name=${dependency%%.*}
                    [[ "$dependency_name" == "$name" ]] && continue
                    install_name_tool -change "$dependency_path" "@rpath/$dependency_name.framework/$dependency_name" "$framework_dir/$name"
                    ;;
            esac
        done < <(otool -L "$framework_dir/$name" | awk 'NR > 1 {print $1}')
    done

    simulator_arm_framework="$INPUT_DIR/ffmpeg-ios-simulator-arm64/frameworks/$name.framework"
    simulator_x86_framework="$INPUT_DIR/ffmpeg-ios-simulator-x86_64/frameworks/$name.framework"
    simulator_framework_dir="$OUTPUT_DIR/.simulator/$name.framework"
    rm -rf "$simulator_framework_dir"
    mkdir -p "$simulator_framework_dir/Headers"
    cp -R "$simulator_arm_framework/Headers/." "$simulator_framework_dir/Headers/"
    cp "$simulator_arm_framework/Info.plist" "$simulator_framework_dir/Info.plist"
    lipo -create \
        "$simulator_arm_framework/$name" \
        "$simulator_x86_framework/$name" \
        -output "$simulator_framework_dir/$name"

    framework_args=(
        -framework "$INPUT_DIR/ffmpeg-ios-device-arm64/frameworks/$name.framework"
        -framework "$simulator_framework_dir"
    )
    xcodebuild -create-xcframework \
        "${framework_args[@]}" \
        -output "$OUTPUT_DIR/Frameworks/$name.xcframework"
done

rm -rf "$OUTPUT_DIR/.simulator"
cp "$INPUT_DIR/ffmpeg-ios-device-arm64/LICENSES/FFmpeg/"* "$OUTPUT_DIR/LICENSES/FFmpeg/"
printf '%s\n' "FFmpeg iOS dynamic XCFramework package" > "$OUTPUT_DIR/LICENSES/FFmpeg/package.txt"
printf 'FFmpeg iOS XCFrameworks ready: %s\n' "$OUTPUT_DIR"
