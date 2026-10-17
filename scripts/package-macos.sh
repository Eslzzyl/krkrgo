#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
ROOT_DIR=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)
# shellcheck source=scripts/package-common.sh
source "$SCRIPT_DIR/package-common.sh"

usage() {
    cat <<'EOF'
Usage: scripts/package-macos.sh [arm64|amd64|all] [options]

Options:
  --out DIR        Output directory (default: dist)
  --tag TAG        FFmpeg sidecar release tag
  --sidecar DIR    Use an extracted sidecar directory
  --icon FILE      Application icon (.icns)
  --version VER    CFBundleShortVersionString (default: 0.1.0)
  --bundle-id ID   CFBundleIdentifier (default: io.github.eslzzyl.krkrgo)
  -h, --help       Show this help
EOF
}

arch=all
out=
tag=$PKG_DEFAULT_TAG
sidecar=
icon=
version=0.1.0
bundle_id=io.github.eslzzyl.krkrgo

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
        --bundle-id)
            [ $# -ge 2 ] || die "--bundle-id requires a value"
            bundle_id=$2
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

case "$version" in
    ''|*[!0-9.]*|.*|*.|*..*)
        die "invalid version: $version"
        ;;
esac

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

if [ -z "$icon" ] && [ -f "$ROOT_DIR/cmd/krkrgo/appicon.icns" ]; then
    icon="$ROOT_DIR/cmd/krkrgo/appicon.icns"
fi
if [ -n "$icon" ]; then
    icon=$(resolve_file "$icon")
fi

if [ "$arch" = all ]; then
    targets="arm64 amd64"
else
    targets=$arch
fi

for target_arch in $targets; do
    printf 'packaging macOS %s\n' "$target_arch"

    if [ -n "$sidecar" ]; then
        sidecar_dir=$sidecar
        validate_sidecar macos "$target_arch" "$sidecar_dir"
    else
        sidecar_dir=$(fetch_sidecar macos "$target_arch" "$tag")
    fi

    stage="$ROOT_DIR/temp/package/macos-$target_arch"
    app="$stage/krkrgo.app"
    rm -rf "$stage"
    mkdir -p "$app/Contents/MacOS/lib/ffmpeg" "$app/Contents/Resources/LICENSES"

    build_app_binary darwin "$target_arch" "$app/Contents/MacOS/krkrgo" "-s -w"
    cp "$sidecar_dir/lib/ffmpeg/"*.dylib "$app/Contents/MacOS/lib/ffmpeg/"
    copy_sidecar_licenses "$sidecar_dir" "$app/Contents/Resources/LICENSES"

    if [ -n "$icon" ]; then
        cp "$icon" "$app/Contents/Resources/AppIcon.icns"
    fi

    icon_entry=
    if [ -n "$icon" ]; then
        icon_entry="	<key>CFBundleIconFile</key>
	<string>AppIcon</string>"
    fi

    cat > "$app/Contents/Info.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleDevelopmentRegion</key>
	<string>en</string>
	<key>CFBundleExecutable</key>
	<string>krkrgo</string>
	<key>CFBundleIdentifier</key>
	<string>$bundle_id</string>
	<key>CFBundleInfoDictionaryVersion</key>
	<string>6.0</string>
	<key>CFBundleName</key>
	<string>krkrgo</string>
	<key>CFBundlePackageType</key>
	<string>APPL</string>
	<key>CFBundleShortVersionString</key>
	<string>$version</string>
	<key>CFBundleVersion</key>
	<string>1</string>
	<key>LSMinimumSystemVersion</key>
	<string>12.0</string>
	<key>NSHighResolutionCapable</key>
	<true/>
$icon_entry
</dict>
</plist>
EOF
    printf 'APPL????' > "$app/Contents/PkgInfo"

    if command -v codesign >/dev/null 2>&1; then
        for lib in "$app/Contents/MacOS/lib/ffmpeg/"*.dylib; do
            codesign --force --sign - "$lib"
        done
        codesign --force --sign - "$app/Contents/MacOS/krkrgo"
        codesign --force --sign - "$app"
    else
        printf 'codesign is unavailable; leaving the macOS bundle unsigned\n'
    fi

    artifact="$out/krkrgo-macos-$target_arch.zip"
    rm -f "$artifact" "$artifact.sha256"
    if command -v ditto >/dev/null 2>&1; then
        (cd "$stage" && ditto -c -k --keepParent krkrgo.app "$artifact")
    else
        require_cmd zip
        (cd "$stage" && zip -qry "$artifact" krkrgo.app)
    fi
    write_sha256 "$artifact"
    printf 'created %s\n' "$artifact"
done
