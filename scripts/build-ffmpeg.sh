#!/usr/bin/env bash
set -Eeuo pipefail

# Build the software-only FFmpeg sidecar used by krkrgo. The source tree must
# match the pinned release in FFMPEG_VERSION; this script never follows a
# moving branch or downloads source.

ROOT_DIR=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
FFMPEG_VERSION=${FFMPEG_VERSION:-9.0.2}
FFMPEG_SOURCE_DIR=${FFMPEG_SOURCE_DIR:-"$ROOT_DIR/.build/ffmpeg-source"}
OS_NAME=${OS:-$(uname -s)}
ARCH_NAME=${ARCH:-$(uname -m)}
PREFIX=${PREFIX:-"$ROOT_DIR/.build/ffmpeg-prefix"}
BUILD_DIR=${BUILD_DIR:-"$ROOT_DIR/.build/ffmpeg-build"}
JOBS=${JOBS:-}
CC=${CC:-}
SYSROOT=${SYSROOT:-}
CROSS_PREFIX=${CROSS_PREFIX:-}
ENABLE_CROSS_COMPILE=${ENABLE_CROSS_COMPILE:-0}
ABI_PROBE_RUNNER=${ABI_PROBE_RUNNER:-}

fail() {
    printf 'build-ffmpeg: %s\n' "$*" >&2
    exit 1
}

need_command() {
    command -v "$1" >/dev/null 2>&1 || fail "required command not found: $1"
}

if [[ -z "$JOBS" ]]; then
    JOBS=$(getconf _NPROCESSORS_ONLN 2>/dev/null || true)
fi
if [[ -z "$JOBS" ]]; then
    JOBS=$(sysctl -n hw.ncpu 2>/dev/null || true)
fi
JOBS=${JOBS:-2}

case "$(printf '%s' "$OS_NAME" | tr '[:upper:]' '[:lower:]')" in
    darwin|macos|osx)
        PLATFORM=macos
        TARGET_OS=darwin
        ;;
    linux)
        PLATFORM=linux
        TARGET_OS=linux
        ;;
    windows|mingw|mingw32|msys)
        PLATFORM=windows
        TARGET_OS=mingw32
        ;;
    *)
        fail "unsupported OS: $OS_NAME"
        ;;
esac

case "$(printf '%s' "$ARCH_NAME" | tr '[:upper:]' '[:lower:]')" in
    amd64|x86_64|x64)
        TARGET_ARCH=x86_64
        GO_ARCH=amd64
        ;;
    arm64|aarch64)
        TARGET_ARCH=aarch64
        GO_ARCH=arm64
        ;;
    *)
        fail "unsupported architecture: $ARCH_NAME"
        ;;
esac

if [[ "$CROSS_PREFIX" == none ]]; then
    CROSS_PREFIX=
fi
if [[ "$PLATFORM" == windows ]]; then
    if [[ -z "$CROSS_PREFIX" && "$CC" != clang* ]]; then
        CROSS_PREFIX="${TARGET_ARCH}-w64-mingw32-"
    fi
    CC=${CC:-"${CROSS_PREFIX}gcc"}
elif [[ "$PLATFORM" == macos ]]; then
    CC=${CC:-clang}
else
    CC=${CC:-gcc}
    if [[ "$TARGET_ARCH" == aarch64 && -z "${CROSS_PREFIX}" ]]; then
        CROSS_PREFIX=aarch64-linux-gnu-
    fi
fi

need_command make
need_command "$CC"

[[ -x "$FFMPEG_SOURCE_DIR/configure" ]] || fail "FFmpeg source is missing configure: $FFMPEG_SOURCE_DIR"
[[ -f "$FFMPEG_SOURCE_DIR/RELEASE" ]] || fail "FFmpeg source is missing RELEASE: $FFMPEG_SOURCE_DIR"
source_version=$(tr -d '\r\n' < "$FFMPEG_SOURCE_DIR/RELEASE")
[[ "$source_version" == "$FFMPEG_VERSION" ]] || fail "FFmpeg source version $source_version does not match $FFMPEG_VERSION"

rm -rf "$BUILD_DIR" "$PREFIX"
mkdir -p "$BUILD_DIR" "$PREFIX" "$PREFIX/lib/ffmpeg" "$PREFIX/LICENSES/FFmpeg"

extra_cflags=()
extra_ldflags=()
configure_args=(
    "--prefix=$PREFIX"
    "--libdir=$PREFIX/lib/ffmpeg"
    "--arch=$TARGET_ARCH"
    "--target-os=$TARGET_OS"
    "--cc=$CC"
    "--enable-shared"
    "--disable-static"
    "--disable-programs"
    "--disable-doc"
    "--disable-network"
    "--disable-autodetect"
    "--disable-everything"
    "--disable-hwaccels"
    "--disable-amf"
    "--disable-audiotoolbox"
    "--disable-cuda-llvm"
    "--disable-cuvid"
    "--disable-d3d11va"
    "--disable-d3d12va"
    "--disable-dxva2"
    "--disable-ffnvcodec"
    "--disable-libdrm"
    "--disable-nvdec"
    "--disable-nvenc"
    "--disable-vaapi"
    "--disable-vdpau"
    "--disable-videotoolbox"
    "--disable-vulkan"
    "--disable-avdevice"
    "--disable-avfilter"
    "--disable-swresample"
    "--disable-muxers"
    "--disable-bsfs"
    "--disable-protocols"
    "--enable-demuxer=asf"
    "--enable-parser=vc1"
    "--enable-decoder=wmv3"
    "--enable-decoder=wmav2"
)

if [[ -n "$CROSS_PREFIX" || "$ENABLE_CROSS_COMPILE" == 1 ]]; then
    configure_args+=("--enable-cross-compile")
    if [[ -n "$CROSS_PREFIX" ]]; then
        configure_args+=("--cross-prefix=$CROSS_PREFIX")
    fi
fi

if [[ -n "$SYSROOT" ]]; then
    extra_cflags+=("--sysroot=$SYSROOT")
    extra_ldflags+=("--sysroot=$SYSROOT")
fi

if [[ "$PLATFORM" == macos ]]; then
    extra_cflags+=("-arch" "$TARGET_ARCH")
    extra_ldflags+=("-arch" "$TARGET_ARCH")
elif [[ "$PLATFORM" == windows && "$TARGET_ARCH" == aarch64 && "$CC" == clang* ]]; then
    extra_cflags+=("--target=aarch64-w64-windows-gnu")
    extra_ldflags+=("--target=aarch64-w64-windows-gnu")
fi

if ((${#extra_cflags[@]})); then
    configure_args+=("--extra-cflags=${extra_cflags[*]}")
fi
if ((${#extra_ldflags[@]})); then
    configure_args+=("--extra-ldflags=${extra_ldflags[*]}")
fi

printf 'FFmpeg version: %s\n' "$source_version"
printf 'Target: %s/%s\n' "$PLATFORM" "$TARGET_ARCH"
printf 'Prefix: %s\n' "$PREFIX"

(
    cd "$BUILD_DIR"
    "$FFMPEG_SOURCE_DIR/configure" "${configure_args[@]}"
    make -j"$JOBS"
    make install-libs install-headers
)

cp "$FFMPEG_SOURCE_DIR/COPYING.LGPLv2.1" "$PREFIX/LICENSES/FFmpeg/COPYING.LGPLv2.1"
printf '%s\n' "$FFMPEG_VERSION" > "$PREFIX/LICENSES/FFmpeg/source-version.txt"
printf '%s\n' "$(printf '%q ' "${configure_args[@]}")" > "$PREFIX/LICENSES/FFmpeg/configure.txt"

lib_dir="$PREFIX/lib/ffmpeg"
case "$PLATFORM" in
    linux)
        library_names=(libavutil.so.61 libavcodec.so.63 libavformat.so.63 libswscale.so.10)
        ;;
    macos)
        library_names=(libavutil.61.dylib libavcodec.63.dylib libavformat.63.dylib libswscale.10.dylib)
        ;;
    windows)
        library_names=(avutil-61.dll avcodec-63.dll avformat-63.dll swscale-10.dll)
        ;;
esac

for library in "${library_names[@]}"; do
    [[ -f "$lib_dir/$library" ]] || fail "expected shared library is missing: $lib_dir/$library"
done

check_symbol() {
    local library=$1
    local symbol=$2
    local output
    case "$PLATFORM" in
        linux)
            output=$(nm -D --defined-only "$library" 2>/dev/null || true)
            ;;
        macos)
            output=$(nm -gU "$library" 2>/dev/null || true)
            ;;
        windows)
            output=$(nm -g "$library" 2>/dev/null || true)
            ;;
    esac
    printf '%s\n' "$output" | grep -Eq "(^|[[:space:]])_?${symbol}(\$|[[:space:]])" || fail "missing exported symbol $symbol in $library"
}

check_symbol "$lib_dir/${library_names[0]}" avutil_version
check_symbol "$lib_dir/${library_names[1]}" avcodec_version
check_symbol "$lib_dir/${library_names[2]}" avformat_version
check_symbol "$lib_dir/${library_names[3]}" swscale_version
check_symbol "$lib_dir/${library_names[2]}" avformat_open_input
check_symbol "$lib_dir/${library_names[1]}" avcodec_send_packet
check_symbol "$lib_dir/${library_names[3]}" sws_scale

probe_c="$BUILD_DIR/abi_probe.c"
probe_bin="$BUILD_DIR/abi_probe"
cat > "$probe_c" <<'EOF'
#include <stddef.h>
#include <stdio.h>
#include <libavcodec/avcodec.h>
#include <libavformat/avformat.h>
#include <libavutil/channel_layout.h>
#include <libavutil/frame.h>
#include <libswscale/swscale.h>

#define FIELD(type, member) printf(#type "." #member "=%zu\n", offsetof(type, member))

int main(void) {
    FIELD(AVFormatContext, pb);
    FIELD(AVFormatContext, flags);
    FIELD(AVFormatContext, nb_streams);
    FIELD(AVFormatContext, streams);
    FIELD(AVFormatContext, duration);
    FIELD(AVStream, index);
    FIELD(AVStream, time_base);
    FIELD(AVStream, duration);
    FIELD(AVStream, nb_frames);
    FIELD(AVStream, avg_frame_rate);
    FIELD(AVStream, r_frame_rate);
    FIELD(AVStream, codecpar);
    FIELD(AVCodecParameters, codec_type);
    FIELD(AVCodecParameters, codec_id);
    FIELD(AVCodecParameters, format);
    FIELD(AVCodecParameters, sample_rate);
    FIELD(AVCodecParameters, ch_layout);
    FIELD(AVCodecParameters, width);
    FIELD(AVCodecParameters, height);
    FIELD(AVPacket, data);
    FIELD(AVPacket, size);
    FIELD(AVPacket, stream_index);
    FIELD(AVPacket, pts);
    FIELD(AVPacket, dts);
    FIELD(AVPacket, duration);
    FIELD(AVFrame, data);
    FIELD(AVFrame, extended_data);
    FIELD(AVFrame, width);
    FIELD(AVFrame, height);
    FIELD(AVFrame, format);
    FIELD(AVFrame, nb_samples);
    FIELD(AVFrame, pts);
    FIELD(AVFrame, pkt_dts);
    FIELD(AVFrame, sample_rate);
    FIELD(AVFrame, time_base);
    return 0;
}
EOF

probe_include=("-I$PREFIX/include")
probe_libs=("-L$lib_dir" -lavformat -lavcodec -lavutil -lswscale)
"$CC" "${probe_include[@]}" "$probe_c" "${probe_libs[@]}" -o "$probe_bin"
if [[ -n "$ABI_PROBE_RUNNER" ]]; then
    "$ABI_PROBE_RUNNER" "$probe_bin" > "$PREFIX/LICENSES/FFmpeg/abi-offsets.txt"
elif [[ "$PLATFORM" == "$(printf '%s' "$(uname -s)" | tr '[:upper:]' '[:lower:]')" && "$TARGET_ARCH" == "$(uname -m)" ]]; then
    "$probe_bin" > "$PREFIX/LICENSES/FFmpeg/abi-offsets.txt"
else
    printf '%s\n' "probe compiled; execution requires ABI_PROBE_RUNNER for $PLATFORM/$TARGET_ARCH" > "$PREFIX/LICENSES/FFmpeg/abi-offsets.txt"
fi

if [[ -d "$PREFIX/bin" ]]; then
    find "$PREFIX/bin" -type f -print -exec sh -c 'echo "unexpected executable: $1" >&2; exit 1' _ {} \;
fi
if [[ -d "$PREFIX/sbin" ]]; then
    find "$PREFIX/sbin" -type f -print -exec sh -c 'echo "unexpected executable: $1" >&2; exit 1' _ {} \;
fi

printf 'FFmpeg sidecar ready: %s\n' "$PREFIX"
