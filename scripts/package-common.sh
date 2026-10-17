#!/usr/bin/env bash
# Shared helpers for the platform packaging scripts.

PKG_REPO="Eslzzyl/krkrgo"
PKG_DEFAULT_TAG="ffmpeg-sidecar-v9.0.2-r3"

die() {
    printf 'package: %s\n' "$*" >&2
    exit 1
}

require_cmd() {
    command -v "$1" >/dev/null 2>&1 || die "required command is missing: $1"
}

sidecar_arch() {
    local platform=$1
    local arch=$2
    case "$platform:$arch" in
        macos:arm64|windows:arm64)
            printf 'arm64\n'
            ;;
        macos:amd64|windows:amd64|linux:amd64)
            printf 'x86_64\n'
            ;;
        linux:arm64)
            printf 'aarch64\n'
            ;;
        *)
            die "unsupported target: $platform/$arch"
            ;;
    esac
}

sidecar_libraries() {
    local platform=$1
    case "$platform" in
        macos)
            printf '%s\n' libavutil.61.dylib libavcodec.63.dylib libavformat.63.dylib libswscale.10.dylib
            ;;
        linux)
            printf '%s\n' libavutil.so.61 libavcodec.so.63 libavformat.so.63 libswscale.so.10
            ;;
        windows)
            printf '%s\n' avutil-61.dll avcodec-63.dll avformat-63.dll swscale-10.dll
            ;;
        *)
            die "unsupported platform: $platform"
            ;;
    esac
}

check_sidecar_arch() {
    local platform=$1
    local arch=$2
    local dir=$3
    local token=
    local lib=
    local output=

    command -v file >/dev/null 2>&1 || return 0

    case "$platform:$arch" in
        macos:arm64)
            token=arm64
            ;;
        macos:amd64)
            token=x86_64
            ;;
        linux:arm64)
            token=aarch64
            ;;
        linux:amd64)
            token=x86-64
            ;;
        *)
            return 0
            ;;
    esac

    while IFS= read -r lib; do
        output=$(file -b "$dir/lib/ffmpeg/$lib" 2>/dev/null) || die "cannot inspect sidecar library: $lib"
        case "$output" in
            *"$token"*)
                ;;
            *)
                die "sidecar library $lib does not match $platform/$arch: $output"
                ;;
        esac
    done < <(sidecar_libraries "$platform")
}

validate_sidecar() {
    local platform=$1
    local arch=$2
    local dir=$3
    local lib=

    [ -d "$dir" ] || die "sidecar directory is missing: $dir"
    [ -d "$dir/lib/ffmpeg" ] || die "sidecar library directory is missing: $dir/lib/ffmpeg"

    while IFS= read -r lib; do
        [ -f "$dir/lib/ffmpeg/$lib" ] || die "sidecar library is missing: $dir/lib/ffmpeg/$lib"
    done < <(sidecar_libraries "$platform")

    [ -f "$dir/LICENSES/FFmpeg/COPYING.LGPLv2.1" ] || die "sidecar license is missing: $dir/LICENSES/FFmpeg/COPYING.LGPLv2.1"

    check_sidecar_arch "$platform" "$arch" "$dir"
}

fetch_sidecar() {
    local platform=$1
    local arch=$2
    local tag=$3
    local ffarch=
    local asset=
    local url=
    local cache=
    local archive=
    local dir=

    ffarch=$(sidecar_arch "$platform" "$arch")
    asset="${tag}-${platform}-${ffarch}.tar.gz"
    url="https://github.com/${PKG_REPO}/releases/download/${tag}/${asset}"
    cache="${ROOT_DIR}/temp/sidecars/${tag}"
    dir="${cache}/ffmpeg-${platform}-${ffarch}"

    if [ ! -d "$dir" ]; then
        require_cmd curl
        require_cmd tar
        mkdir -p "$cache"
        archive="${cache}/${asset}"
        curl -fL --retry 3 --connect-timeout 20 -o "$archive" "$url"
        tar -xzf "$archive" -C "$cache"
    fi

    [ -d "$dir" ] || die "sidecar archive layout is unexpected: $dir"
    validate_sidecar "$platform" "$arch" "$dir"
    printf '%s\n' "$dir"
}

build_app_binary() {
    local goos=$1
    local goarch=$2
    local output=$3
    local ldflags=$4

    require_cmd go
    (
        cd "$ROOT_DIR" || exit 1
        CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
            go build -trimpath -ldflags "$ldflags" -o "$output" ./cmd/krkrgo
    )
}

copy_sidecar_licenses() {
    local sidecar=$1
    local destination=$2

    mkdir -p "$destination"
    cp -R "$sidecar/LICENSES/FFmpeg" "$destination/"
}

resolve_dir() {
    local dir=$1
    [ -d "$dir" ] || die "directory does not exist: $dir"
    (CDPATH= cd -- "$dir" && pwd)
}

resolve_file() {
    local file=$1
    local dir=
    local base=

    [ -f "$file" ] || die "file does not exist: $file"
    dir=$(CDPATH= cd -- "$(dirname -- "$file")" && pwd)
    base=$(basename -- "$file")
    printf '%s/%s\n' "$dir" "$base"
}

write_sha256() {
    local file=$1
    local sum=

    if command -v sha256sum >/dev/null 2>&1; then
        sum=$(sha256sum "$file" | awk '{print $1}')
    elif command -v shasum >/dev/null 2>&1; then
        sum=$(shasum -a 256 "$file" | awk '{print $1}')
    else
        die "required command is missing: sha256sum or shasum"
    fi

    printf '%s  %s\n' "$sum" "$(basename -- "$file")" > "$file.sha256"
}
