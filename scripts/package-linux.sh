#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
ROOT_DIR=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)
# shellcheck source=scripts/package-common.sh
source "$SCRIPT_DIR/package-common.sh"

usage() {
    cat <<'EOF'
Usage: scripts/package-linux.sh [arm64|amd64|all] [options]

Options:
  --out DIR        Output directory (default: dist)
  --tag TAG        FFmpeg sidecar release tag
  --sidecar DIR    Use an extracted sidecar directory
  -h, --help       Show this help
EOF
}

arch=all
out=
tag=$PKG_DEFAULT_TAG
sidecar=

while [ $# -gt 0 ]; do
    case "$1" in
        arm64|amd64|all)
            arch=$1
            shift
            ;;
        --out)
            [ $# -ge 2 ] || die "--out requires a value"
            out=$2
            shift 2
            ;;
        --tag)
            [ $# -ge 2 ] || die "--tag requires a value"
            tag=$2
            shift 2
            ;;
        --sidecar)
            [ $# -ge 2 ] || die "--sidecar requires a value"
            sidecar=$2
            shift 2
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            die "unknown argument: $1"
            ;;
    esac
done

if [ -z "$out" ]; then
    out="$ROOT_DIR/dist"
fi
mkdir -p "$out"
out=$(resolve_dir "$out")

if [ -n "$sidecar" ]; then
    if [ "$arch" = all ]; then
        die "--sidecar requires a single architecture"
    fi
    sidecar=$(resolve_dir "$sidecar")
fi

if [ "$arch" = all ]; then
    targets="arm64 amd64"
else
    targets=$arch
fi

require_cmd tar

for target_arch in $targets; do
    printf 'packaging Linux %s\n' "$target_arch"

    if [ -n "$sidecar" ]; then
        sidecar_dir=$sidecar
        validate_sidecar linux "$target_arch" "$sidecar_dir"
    else
        sidecar_dir=$(fetch_sidecar linux "$target_arch" "$tag")
    fi

    stage="$ROOT_DIR/temp/package/linux-$target_arch"
    package_name="krkrgo-linux-$target_arch"
    package_dir="$stage/$package_name"
    rm -rf "$stage"
    mkdir -p "$package_dir/lib/ffmpeg" "$package_dir/lib/fonts" "$package_dir/LICENSES"

    build_app_binary linux "$target_arch" "$package_dir/krkrgo" "-s -w"
    cp "$ROOT_DIR/lib/fonts/NotoSansCJK-Regular.ttc" "$package_dir/lib/fonts/"
    cp "$sidecar_dir/lib/ffmpeg/"*.so.* "$package_dir/lib/ffmpeg/"
    copy_sidecar_licenses "$sidecar_dir" "$package_dir/LICENSES"

    artifact="$out/$package_name.tar.gz"
    rm -f "$artifact" "$artifact.sha256"
    (cd "$stage" && tar -czf "$artifact" "$package_name")
    write_sha256 "$artifact"
    printf 'created %s\n' "$artifact"
done
