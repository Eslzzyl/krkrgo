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
CCACHE=${CCACHE:-}
HOST_CC=${HOST_CC:-}
SYSROOT=${SYSROOT:-}
CROSS_PREFIX=${CROSS_PREFIX:-}
ENABLE_CROSS_COMPILE=${ENABLE_CROSS_COMPILE:-0}
ABI_PROBE_RUNNER=${ABI_PROBE_RUNNER:-}
AR=${AR:-}
NM=${NM:-}
OBJDUMP=${OBJDUMP:-}
WINDRES=${WINDRES:-}
READELF=${READELF:-}
ANDROID_API=${ANDROID_API:-24}
ANDROID_NDK_HOME=${ANDROID_NDK_HOME:-${ANDROID_NDK_ROOT:-}}
ANDROID_HOST_TAG=${ANDROID_HOST_TAG:-}
IOS_SDK=${IOS_SDK:-iphoneos}
IOS_MIN_VERSION=${IOS_MIN_VERSION:-13.0}

fail() {
    printf 'build-ffmpeg: %s\n' "$*" >&2
    exit 1
}

need_command() {
    if [[ "$1" == */* ]]; then
        [[ -x "$1" ]] || fail "required command not found: $1"
    else
        command -v "$1" >/dev/null 2>&1 || fail "required command not found: $1"
    fi
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
    android)
        PLATFORM=android
        TARGET_OS=android
        ;;
    ios|iphoneos|iphonesimulator)
        PLATFORM=ios
        TARGET_OS=darwin
        ;;
    windows|mingw|mingw32|msys)
        PLATFORM=windows
        TARGET_OS=mingw32
        ;;
    *)
        fail "unsupported OS: $OS_NAME"
        ;;
esac

if [[ "$PLATFORM" == ios ]]; then
    case "$(printf '%s' "$ARCH_NAME" | tr '[:upper:]' '[:lower:]')" in
        arm64|aarch64)
            TARGET_ARCH=arm64
            GO_ARCH=arm64
            ;;
        amd64|x86_64|x64)
            TARGET_ARCH=x86_64
            GO_ARCH=amd64
            ;;
        *)
            fail "unsupported iOS architecture: $ARCH_NAME"
            ;;
    esac
elif [[ "$PLATFORM" == android ]]; then
    case "$(printf '%s' "$ARCH_NAME" | tr '[:upper:]' '[:lower:]')" in
        arm64-v8a|arm64|aarch64)
            TARGET_ARCH=aarch64
            ANDROID_ABI=arm64-v8a
            ANDROID_TRIPLE=aarch64-linux-android
            ;;
        x86_64|x64|amd64)
            TARGET_ARCH=x86_64
            ANDROID_ABI=x86_64
            ANDROID_TRIPLE=x86_64-linux-android
            ;;
        armeabi-v7a|arm|armv7|armv7a)
            TARGET_ARCH=arm
            ANDROID_ABI=armeabi-v7a
            ANDROID_TRIPLE=armv7a-linux-androideabi
            ;;
        x86|i686)
            TARGET_ARCH=i686
            ANDROID_ABI=x86
            ANDROID_TRIPLE=i686-linux-android
            ;;
        *)
            fail "unsupported Android ABI: $ARCH_NAME"
            ;;
    esac
else
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
fi

if [[ "$CROSS_PREFIX" == none ]]; then
    CROSS_PREFIX=
fi
if [[ "$PLATFORM" == windows ]]; then
    if [[ -z "$CROSS_PREFIX" && "$CC" != clang* && "$CC" != gcc && "$CC" != *-w64-mingw32-gcc ]]; then
        CROSS_PREFIX="${TARGET_ARCH}-w64-mingw32-"
    fi
    CC=${CC:-"${CROSS_PREFIX}gcc"}
elif [[ "$PLATFORM" == macos ]]; then
    CC=${CC:-clang}
elif [[ "$PLATFORM" == android ]]; then
    [[ -n "$ANDROID_NDK_HOME" ]] || fail "ANDROID_NDK_HOME is required for Android builds"
    if [[ -z "$ANDROID_HOST_TAG" ]]; then
        case "$(uname -s)" in
            Linux*)
                ANDROID_HOST_TAG=linux-x86_64
                ;;
            Darwin*)
                ANDROID_HOST_TAG=darwin-x86_64
                ;;
            MINGW*|MSYS*|CYGWIN*)
                ANDROID_HOST_TAG=windows-x86_64
                ;;
            *)
                fail "cannot determine Android NDK host tag from $(uname -s)"
                ;;
        esac
    fi
    ANDROID_NDK_BIN="$ANDROID_NDK_HOME/toolchains/llvm/prebuilt/$ANDROID_HOST_TAG/bin"
    [[ -d "$ANDROID_NDK_BIN" ]] || fail "Android NDK toolchain is missing: $ANDROID_NDK_BIN"
    CC=${CC:-"$ANDROID_NDK_BIN/${ANDROID_TRIPLE}${ANDROID_API}-clang"}
    AR=${AR:-"$ANDROID_NDK_BIN/llvm-ar"}
    NM=${NM:-"$ANDROID_NDK_BIN/llvm-nm"}
    READELF=${READELF:-"$ANDROID_NDK_BIN/llvm-readelf"}
    SYSROOT=${SYSROOT:-"$ANDROID_NDK_HOME/toolchains/llvm/prebuilt/$ANDROID_HOST_TAG/sysroot"}
    ENABLE_CROSS_COMPILE=1
elif [[ "$PLATFORM" == ios ]]; then
    need_command xcrun
    case "$IOS_SDK" in
        iphoneos|iphonesimulator)
            ;;
        *)
            fail "unsupported iOS SDK: $IOS_SDK"
            ;;
    esac
    CC=${CC:-$(xcrun --sdk "$IOS_SDK" --find clang)}
    AR=${AR:-$(xcrun --sdk "$IOS_SDK" --find ar)}
    NM=${NM:-$(xcrun --sdk "$IOS_SDK" --find nm)}
    SYSROOT=${SYSROOT:-$(xcrun --sdk "$IOS_SDK" --show-sdk-path)}
    ENABLE_CROSS_COMPILE=1
else
    CC=${CC:-gcc}
    if [[ "$TARGET_ARCH" == aarch64 && -z "${CROSS_PREFIX}" ]]; then
        CROSS_PREFIX=aarch64-linux-gnu-
    fi
fi

need_command make
need_command "$CC"
compiler_command=("$CC")
if [[ -n "$CCACHE" ]]; then
    need_command "$CCACHE"
    compiler_command=("$CCACHE" "$CC")
fi
compiler_command_string=$(printf '%q ' "${compiler_command[@]}")
compiler_command_string=${compiler_command_string% }
if [[ "$PLATFORM" == windows ]]; then
    AR=${AR:-ar}
    NM=${NM:-nm}
    need_command "$AR"
    need_command "$NM"
    if [[ -z "$OBJDUMP" ]]; then
        if [[ -n "$CROSS_PREFIX" ]] && command -v "${CROSS_PREFIX}objdump" >/dev/null 2>&1; then
            OBJDUMP="${CROSS_PREFIX}objdump"
        elif command -v objdump >/dev/null 2>&1; then
            OBJDUMP=objdump
        else
            OBJDUMP=llvm-objdump
        fi
    fi
    need_command "$OBJDUMP"
elif [[ "$PLATFORM" == linux || "$PLATFORM" == android ]]; then
    NM=${NM:-nm}
    need_command "$NM"
    if [[ -z "$READELF" ]]; then
        if [[ -n "$CROSS_PREFIX" ]] && command -v "${CROSS_PREFIX}readelf" >/dev/null 2>&1; then
            READELF="${CROSS_PREFIX}readelf"
        else
            READELF=readelf
        fi
    fi
    need_command "$READELF"
elif [[ "$PLATFORM" == macos || "$PLATFORM" == ios ]]; then
    NM=${NM:-nm}
    need_command "$NM"
fi

[[ -x "$FFMPEG_SOURCE_DIR/configure" ]] || fail "FFmpeg source is missing configure: $FFMPEG_SOURCE_DIR"
[[ -f "$FFMPEG_SOURCE_DIR/RELEASE" ]] || fail "FFmpeg source is missing RELEASE: $FFMPEG_SOURCE_DIR"
source_version=$(tr -d '\r\n' < "$FFMPEG_SOURCE_DIR/RELEASE")
[[ "$source_version" == "$FFMPEG_VERSION" ]] || fail "FFmpeg source version $source_version does not match $FFMPEG_VERSION"

rm -rf "$BUILD_DIR" "$PREFIX"
lib_dir="$PREFIX/lib/ffmpeg"
if [[ "$PLATFORM" == android ]]; then
    lib_dir="$PREFIX/lib/$ANDROID_ABI"
fi
mkdir -p "$BUILD_DIR" "$PREFIX" "$lib_dir" "$PREFIX/LICENSES/FFmpeg"

extra_cflags=()
extra_ldflags=()
shared_flag=--enable-shared
static_flag=--disable-static
configure_args=(
    "--prefix=$PREFIX"
    "--libdir=$lib_dir"
    "--shlibdir=$lib_dir"
    "--arch=$TARGET_ARCH"
    "--target-os=$TARGET_OS"
    "--cc=$compiler_command_string"
    "$shared_flag"
    "$static_flag"
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
    "--enable-demuxer=ogg"
    "--enable-demuxer=mov"
    "--enable-demuxer=wav"
    "--enable-demuxer=mpegvideo"
    "--enable-demuxer=mpeg"
    "--enable-demuxer=mpegts"
    "--enable-parser=vc1"
    "--enable-parser=mpegvideo"
    "--enable-parser=aac"
    "--enable-decoder=vorbis"
    "--enable-decoder=aac"
    "--enable-decoder=wmv3"
    "--enable-decoder=wmav2"
    "--enable-decoder=pcm_u8"
    "--enable-decoder=pcm_s16le"
    "--enable-decoder=pcm_s24le"
    "--enable-decoder=pcm_s32le"
    "--enable-decoder=pcm_f32le"
    "--enable-decoder=mpeg1video"
    "--enable-decoder=mpeg2video"
)

if [[ -n "$HOST_CC" ]]; then
    configure_args+=("--host-cc=$HOST_CC")
fi
if [[ -n "$CROSS_PREFIX" || "$ENABLE_CROSS_COMPILE" == 1 ]]; then
    configure_args+=("--enable-cross-compile")
    if [[ -n "$CROSS_PREFIX" ]]; then
        configure_args+=("--cross-prefix=$CROSS_PREFIX")
    fi
fi
if [[ "$PLATFORM" == windows ]]; then
    configure_args+=("--ar=$AR" "--nm=$NM")
fi
if [[ "$PLATFORM" == windows && -n "$CROSS_PREFIX" ]]; then
    configure_args+=(
        "--ranlib=ranlib"
        "--strip=strip"
    )
    if [[ -n "$WINDRES" ]]; then
        configure_args+=("--windres=$WINDRES")
    else
        configure_args+=("--windres=windres")
    fi
fi
if [[ "$PLATFORM" == android ]]; then
    configure_args+=("--ar=$AR" "--nm=$NM" "--ranlib=$ANDROID_NDK_BIN/llvm-ranlib" "--strip=$ANDROID_NDK_BIN/llvm-strip")
fi
if [[ "$PLATFORM" == ios ]]; then
    configure_args+=("--ar=$AR" "--nm=$NM")
fi
if [[ -n "$SYSROOT" ]]; then
    extra_cflags+=("--sysroot=$SYSROOT")
    extra_ldflags+=("--sysroot=$SYSROOT")
fi

if [[ "$PLATFORM" == macos ]]; then
    mac_arch=$TARGET_ARCH
    if [[ "$mac_arch" == aarch64 ]]; then
        mac_arch=arm64
    fi
    extra_cflags+=("-arch" "$mac_arch")
    extra_ldflags+=("-arch" "$mac_arch")
    configure_args+=("--install-name-dir=@loader_path")
elif [[ "$PLATFORM" == ios ]]; then
    ios_version_flag="-miphoneos-version-min=$IOS_MIN_VERSION"
    if [[ "$IOS_SDK" == iphonesimulator ]]; then
        ios_version_flag="-mios-simulator-version-min=$IOS_MIN_VERSION"
    fi
    extra_cflags+=("-arch" "$TARGET_ARCH" "-isysroot" "$SYSROOT" "$ios_version_flag")
    extra_ldflags+=("-arch" "$TARGET_ARCH" "-isysroot" "$SYSROOT" "$ios_version_flag")
    configure_args+=("--install-name-dir=@rpath")
elif [[ "$PLATFORM" == windows && "$TARGET_ARCH" == aarch64 && "$CC" == clang* ]]; then
    extra_cflags+=("--target=aarch64-w64-windows-gnu")
    extra_ldflags+=("--target=aarch64-w64-windows-gnu")
elif [[ "$PLATFORM" == windows && "$CC" != clang* ]]; then
    extra_ldflags+=("-static-libgcc")
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

case "$PLATFORM" in
    linux)
        library_names=(libavutil.so.61 libavcodec.so.63 libavformat.so.63 libswscale.so.10)
        ;;
    android)
        library_names=(libavutil.so.61 libavcodec.so.63 libavformat.so.63 libswscale.so.10)
        ;;
    ios)
        library_names=(libavutil.61.dylib libavcodec.63.dylib libavformat.63.dylib libswscale.10.dylib)
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

if [[ "$PLATFORM" == android ]]; then
    for library in libavutil.so.61 libavcodec.so.63 libavformat.so.63 libswscale.so.10; do
        unversioned=${library%%.*}.so
        [[ -f "$lib_dir/$unversioned" ]] || cp "$lib_dir/$library" "$lib_dir/$unversioned"
    done
fi

check_symbol() {
    local library=$1
    local symbol=$2
    local output
    case "$PLATFORM" in
        linux|android)
            output=$("$NM" -D --defined-only "$library" 2>/dev/null || true)
            printf '%s\n' "$output" | tr -d '\r' | grep -Eq "(^|[[:space:]])_?${symbol}(@[^[:space:]]+)?($|[[:space:]])" || fail "missing exported symbol $symbol in $library"
            ;;
        macos)
            output=$(nm -gU "$library" 2>/dev/null || true)
            printf '%s\n' "$output" | tr -d '\r' | grep -Eq "(^|[[:space:]])_?${symbol}(@[^[:space:]]+)?($|[[:space:]])" || fail "missing exported symbol $symbol in $library"
            ;;
        ios)
            output=$("$NM" -gU "$library" 2>/dev/null || true)
            printf '%s\n' "$output" | tr -d '\r' | grep -Eq "(^|[[:space:]])_?${symbol}(@[^[:space:]]+)?($|[[:space:]])" || fail "missing exported symbol $symbol in $library"
            ;;
        windows)
            output=$("$OBJDUMP" -p "$library" 2>/dev/null || true)
            printf '%s\n' "$output" | tr -d '\r' | awk -v symbol="$symbol" '
                /^\[Ordinal\/Name Pointer\]/ { in_exports=1; next }
                /^Export Table:$/ { in_exports=1; next }
                in_exports && $NF ~ ("^_?" symbol "(@[^[:space:]]+)?$") { found=1 }
                END { exit found ? 0 : 1 }
            ' || fail "missing exported symbol $symbol in $library"
            ;;
    esac
}
windows_system_dll() {
    case "$1" in
        api-ms-win-*.dll|ext-ms-win-*.dll|advapi32.dll|bcrypt.dll|combase.dll|comdlg32.dll|crypt32.dll|gdi32.dll|imm32.dll|iphlpapi.dll|kernel32.dll|kernelbase.dll|msvcrt.dll|ntdll.dll|ole32.dll|oleaut32.dll|rpcrt4.dll|secur32.dll|shell32.dll|shlwapi.dll|ucrtbase.dll|user32.dll|version.dll|winmm.dll|ws2_32.dll)
            return 0
            ;;
        *)
            return 1
            ;;
    esac
}

validate_linux_dependencies() {
    local library dep
    for library in "${library_names[@]}"; do
        while IFS= read -r dep; do
            [[ -n "$dep" ]] || continue
            case "$dep" in
                libavutil.so.61|libavcodec.so.63|libavformat.so.63|libswscale.so.10)
                    [[ -f "$lib_dir/$dep" ]] || fail "Linux FFmpeg dependency is missing: $dep"
                    ;;
                libc.so.*|libm.so.*|libdl.so.*|libpthread.so.*|librt.so.*|libgcc_s.so.*|libatomic.so.*|libresolv.so.*|ld-linux-*.so.*|linux-vdso.so.*)
                    ;;
                *)
                    fail "unexpected Linux FFmpeg dependency: $dep"
                    ;;
            esac
        done < <("$READELF" -d "$lib_dir/$library" | awk -F'[][]' '/NEEDED/{print $2}')
    done
}

validate_android_dependencies() {
    local library dep
    for library in "${library_names[@]}"; do
        while IFS= read -r dep; do
            [[ -n "$dep" ]] || continue
            case "$dep" in
                libavutil.so.61|libavcodec.so.63|libavformat.so.63|libswscale.so.10)
                    [[ -f "$lib_dir/$dep" ]] || fail "Android FFmpeg dependency is missing: $dep"
                    ;;
                libc.so|libc.so.*|libm.so|libm.so.*|libdl.so|libdl.so.*|liblog.so|liblog.so.*|libz.so|libz.so.*|libandroid.so|libandroid.so.*|libgcc.so|libgcc.so.*|libatomic.so|libatomic.so.*|libc++_shared.so|libunwind.so|ld-android.so|linker64|linux-vdso.so.*)
                    ;;
                *)
                    fail "unexpected Android FFmpeg dependency: $dep"
                    ;;
            esac
        done < <("$READELF" -d "$lib_dir/$library" | awk -F'[][]' '/NEEDED/{print $2}')
    done
}

validate_macos_dependencies() {
    local library dep
    for library in "${library_names[@]}"; do
        while IFS= read -r dep; do
            [[ -n "$dep" ]] || continue
            case "$dep" in
                @loader_path/*|/usr/lib/*|/System/Library/*)
                    ;;
                *)
                    fail "unexpected macOS FFmpeg dependency: $dep"
                    ;;
            esac
        done < <(otool -L "$lib_dir/$library" | awk 'NR > 1 {print $1}')
    done
}

validate_ios_dependencies() {
    local library dep
    for library in "${library_names[@]}"; do
        while IFS= read -r dep; do
            [[ -n "$dep" ]] || continue
            case "$dep" in
                @rpath/*|@loader_path/*|/usr/lib/*|/System/Library/*)
                    ;;
                libavutil.61.dylib|libavcodec.63.dylib|libavformat.63.dylib|libswscale.10.dylib)
                    [[ -f "$lib_dir/$dep" ]] || fail "iOS FFmpeg dependency is missing: $dep"
                    ;;
                *)
                    fail "unexpected iOS FFmpeg dependency: $dep"
                    ;;
            esac
        done < <(otool -L "$lib_dir/$library" | awk 'NR > 1 {print $1}')
    done
}

stage_windows_runtime_dependencies() {
    local toolchain_bin dep dep_lower library library_path runtime_path runtime_dir
    local checked_library staged_path already_checked staged discovered
    local -a checked=() runtime_dirs=()

    toolchain_bin=$(CDPATH= cd -- "$(dirname -- "$(command -v "$CC")")" && pwd)
    runtime_dirs+=("$toolchain_bin")
    if [[ -n "$SYSROOT" && -d "$SYSROOT/bin" ]]; then
        runtime_dirs+=("$SYSROOT/bin")
    fi
    while :; do
        discovered=0
        for library_path in "$lib_dir"/*.dll; do
            [[ -f "$library_path" ]] || continue
            library=$(basename "$library_path")
            already_checked=0
            for checked_library in "${checked[@]}"; do
                if [[ "$checked_library" == "$library" ]]; then
                    already_checked=1
                    break
                fi
            done
            if ((already_checked)); then
                continue
            fi
            checked+=("$library")
            while IFS= read -r dep; do
                [[ -n "$dep" ]] || continue
                dep_lower=$(printf '%s' "$dep" | tr '[:upper:]' '[:lower:]')
                if windows_system_dll "$dep_lower"; then
                    continue
                fi
                staged=0
                for staged_path in "$lib_dir"/*.dll; do
                    [[ -f "$staged_path" ]] || continue
                    if [[ "$(basename "$staged_path" | tr '[:upper:]' '[:lower:]')" == "$dep_lower" ]]; then
                        staged=1
                        break
                    fi
                done
                if ((staged)); then
                    continue
                fi
                runtime_path=
                for runtime_dir in "${runtime_dirs[@]}"; do
                    runtime_path=$(find "$runtime_dir" -maxdepth 1 -type f -iname "$dep" -print -quit)
                    [[ -n "$runtime_path" ]] && break
                done
                [[ -n "$runtime_path" ]] || fail "Windows FFmpeg dependency not found: $dep"
                cp "$runtime_path" "$lib_dir/$(basename "$runtime_path")"
                discovered=1
            done < <("$OBJDUMP" -p "$library_path" | awk '/DLL Name:/{print $3}')
        done
        if ((discovered == 0)); then
            break
        fi
    done
}


check_symbol "$lib_dir/${library_names[0]}" avutil_version
check_symbol "$lib_dir/${library_names[1]}" avcodec_version
check_symbol "$lib_dir/${library_names[2]}" avformat_version
check_symbol "$lib_dir/${library_names[3]}" swscale_version
check_symbol "$lib_dir/${library_names[2]}" avformat_open_input
check_symbol "$lib_dir/${library_names[1]}" avcodec_send_packet
check_symbol "$lib_dir/${library_names[3]}" sws_scale
case "$PLATFORM" in
    linux)
        validate_linux_dependencies
        ;;
    android)
        validate_android_dependencies
        ;;
    ios)
        need_command otool
        validate_ios_dependencies
        ;;
    macos)
        need_command otool
        validate_macos_dependencies
        ;;
    windows)
        stage_windows_runtime_dependencies
        ;;
esac


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
"${compiler_command[@]}" "${probe_include[@]}" "${extra_cflags[@]}" "$probe_c" "${probe_libs[@]}" "${extra_ldflags[@]}" -o "$probe_bin"
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
