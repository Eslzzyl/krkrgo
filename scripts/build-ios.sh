#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
FRAMEWORK_DIR="$ROOT_DIR/ios/Frameworks"
RESOURCE_LICENSE_DIR="$ROOT_DIR/ios/Resources/LICENSES/FFmpeg"
DERIVED_DATA_DIR="$ROOT_DIR/temp/ios-deriveddata"
FFMPEG_SOURCE_DIR=${KRKRGO_FFMPEG_IOS_DIR:-"$ROOT_DIR/temp/ffmpeg-ios-xcframeworks"}
MODE=${1:-all}

fail() {
    printf 'build-ios: %s\n' "$*" >&2
    exit 1
}

need_command() {
    command -v "$1" >/dev/null 2>&1 || fail "required command not found: $1"
}

need_command go
need_command xcodebuild

[[ "$MODE" == simulator || "$MODE" == device || "$MODE" == all ]] || fail "usage: scripts/build-ios.sh [simulator|device|all]"

mkdir -p "$FRAMEWORK_DIR" "$RESOURCE_LICENSE_DIR"
rm -rf "$FRAMEWORK_DIR/Krkrgo.xcframework"

go run github.com/hajimehoshi/ebiten/v2/cmd/ebitenmobile@v2.10.5 \
    bind \
    -target ios \
    -iosversion 13.0 \
    -o "$FRAMEWORK_DIR/Krkrgo.xcframework" \
    ./mobile/krkrgo

ffmpeg_framework_dir="$FFMPEG_SOURCE_DIR/Frameworks"
if [[ ! -d "$ffmpeg_framework_dir" ]]; then
    ffmpeg_framework_dir="$FFMPEG_SOURCE_DIR"
fi

ffmpeg_available=true
for name in libavutil libavcodec libavformat libswscale; do
    if [[ ! -d "$ffmpeg_framework_dir/$name.xcframework" ]]; then
        ffmpeg_available=false
        break
    fi
done

if $ffmpeg_available; then
    for name in libavutil libavcodec libavformat libswscale; do
        rm -rf "$FRAMEWORK_DIR/$name.xcframework"
        cp -R "$ffmpeg_framework_dir/$name.xcframework" "$FRAMEWORK_DIR/$name.xcframework"
    done

    license_source="$FFMPEG_SOURCE_DIR/LICENSES/FFmpeg"
    if [[ ! -d "$license_source" ]]; then
        license_source="$ROOT_DIR/lib/LICENSES/FFmpeg"
    fi
    if [[ -d "$license_source" ]]; then
        rm -rf "$RESOURCE_LICENSE_DIR"
        mkdir -p "$RESOURCE_LICENSE_DIR"
        cp -R "$license_source/." "$RESOURCE_LICENSE_DIR/"
    fi
else
    printf 'build-ios: FFmpeg XCFrameworks unavailable; building with runtime media fallback\n'
    for name in libavutil libavcodec libavformat libswscale; do
        rm -rf "$FRAMEWORK_DIR/$name.xcframework"
    done
fi

build_target() {
    local mode="$1"
    local sdk="$2"
    local destination="$3"
    local output="$DERIVED_DATA_DIR/$mode"
    rm -rf "$output"
    mkdir -p "$output"
    xcodebuild \
        -project "$ROOT_DIR/ios/krkrgo.xcodeproj" \
        -scheme krkrgo \
        -configuration Debug \
        -sdk "$sdk" \
        -destination "$destination" \
        -derivedDataPath "$output" \
        IPHONEOS_DEPLOYMENT_TARGET=15.0 \
        CODE_SIGNING_ALLOWED=NO \
        build
}

case "$MODE" in
simulator)
    build_target simulator iphonesimulator "generic/platform=iOS Simulator"
    ;;
device)
    build_target device iphoneos "generic/platform=iOS"
    ;;
all)
    build_target simulator iphonesimulator "generic/platform=iOS Simulator"
    build_target device iphoneos "generic/platform=iOS"
    ;;
esac
