#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
ROOT_DIR=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)
# shellcheck source=scripts/package-common.sh
source "$SCRIPT_DIR/package-common.sh"

usage() {
    cat <<'EOF'
Usage: scripts/package.sh <all|macos|linux|windows> [options]

Options:
  --arch ARCH      arm64, amd64, or all (default: all)
  --out DIR        Output directory (default: dist)
  --tag TAG        FFmpeg sidecar release tag
  --sidecar DIR    Use an extracted sidecar directory (single target and arch)
  --icon FILE      macOS application icon (.icns)
  --version VER    macOS CFBundleShortVersionString
  -h, --help       Show this help

The Linux entry builds every target. The Windows target runs through
PowerShell (pwsh on Linux and macOS). The macOS and Windows entries exist
for hosts that build one platform locally.
EOF
}

target=${1:-}
if [ -z "$target" ]; then
    usage >&2
    exit 1
fi

case "$target" in
    all|macos|linux|windows)
        shift
        ;;
    -h|--help)
        usage
        exit 0
        ;;
    *)
        die "unknown target: $target"
        ;;
esac

arch=all
out=
tag=
sidecar=
icon=
version=

while [ $# -gt 0 ]; do
    case "$1" in
        --arch)
            [ $# -ge 2 ] || die "--arch requires a value"
            arch=$2
            shift 2
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
        --icon)
            [ $# -ge 2 ] || die "--icon requires a value"
            icon=$2
            shift 2
            ;;
        --version)
            [ $# -ge 2 ] || die "--version requires a value"
            version=$2
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

case "$arch" in
    arm64|amd64|all)
        ;;
    *)
        die "unknown architecture: $arch"
        ;;
esac

if [ -n "$sidecar" ]; then
    if [ "$target" = all ] || [ "$arch" = all ]; then
        die "--sidecar requires a single target and architecture"
    fi
fi

if [ -n "$icon" ] && [ "$target" != macos ]; then
    die "--icon applies to the macOS target"
fi
if [ -n "$version" ] && [ "$target" != macos ]; then
    die "--version applies to the macOS target"
fi

if [ -z "$out" ]; then
    out="$ROOT_DIR/dist"
fi

run_macos() {
    local args=("$arch" --out "$out")
    if [ -n "$tag" ]; then
        args+=(--tag "$tag")
    fi
    if [ -n "$sidecar" ]; then
        args+=(--sidecar "$sidecar")
    fi
    if [ -n "$icon" ]; then
        args+=(--icon "$icon")
    fi
    if [ -n "$version" ]; then
        args+=(--version "$version")
    fi
    bash "$SCRIPT_DIR/package-macos.sh" "${args[@]}"
}

run_linux() {
    local args=("$arch" --out "$out")
    if [ -n "$tag" ]; then
        args+=(--tag "$tag")
    fi
    if [ -n "$sidecar" ]; then
        args+=(--sidecar "$sidecar")
    fi
    bash "$SCRIPT_DIR/package-linux.sh" "${args[@]}"
}

run_windows() {
    local args=(-Arch "$arch" -Out "$out")
    if [ -n "$tag" ]; then
        args+=(-Tag "$tag")
    fi
    if [ -n "$sidecar" ]; then
        args+=(-Sidecar "$sidecar")
    fi
    if command -v pwsh >/dev/null 2>&1; then
        pwsh -NoProfile -File "$SCRIPT_DIR/package-windows.ps1" "${args[@]}"
    elif command -v powershell >/dev/null 2>&1; then
        powershell -NoProfile -ExecutionPolicy Bypass -File "$SCRIPT_DIR/package-windows.ps1" "${args[@]}"
    else
        die "PowerShell is required for the Windows target"
    fi
}

case "$target" in
    all)
        run_macos
        run_linux
        run_windows
        ;;
    macos)
        run_macos
        ;;
    linux)
        run_linux
        ;;
    windows)
        run_windows
        ;;
esac
