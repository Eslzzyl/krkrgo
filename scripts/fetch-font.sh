#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
ROOT_DIR=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)

FONT_COMMIT=${KRKRGO_FONT_COMMIT:-f8d157532fbfaeda587e826d4cd5b21a49186f7c}
FONT_NAME=NotoSansCJK-Regular.ttc
FONT_URL="https://raw.githubusercontent.com/notofonts/noto-cjk/$FONT_COMMIT/Sans/OTC/$FONT_NAME"
FONT_DIR="$ROOT_DIR/lib/fonts"
FONT_PATH="$FONT_DIR/$FONT_NAME"
FONT_SHA256=b76b0433203017ca80401b2ee0dd69350349871c4b19d504c34dbdd80541690a

require_cmd() {
    command -v "$1" >/dev/null 2>&1 || {
        printf 'fetch-font: required command is missing: %s\n' "$1" >&2
        exit 1
    }
}

sha256_file() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | awk '{print $1}'
    else
        shasum -a 256 "$1" | awk '{print $1}'
    fi
}

require_cmd curl
if ! command -v sha256sum >/dev/null 2>&1 && ! command -v shasum >/dev/null 2>&1; then
    printf 'fetch-font: required command is missing: sha256sum or shasum\n' >&2
    exit 1
fi

mkdir -p "$FONT_DIR"
if [ -f "$FONT_PATH" ] && [ "$(sha256_file "$FONT_PATH")" = "$FONT_SHA256" ]; then
    printf '%s\n' "$FONT_PATH"
    exit 0
fi

tmp_path="$FONT_PATH.tmp.$$"
trap 'rm -f "$tmp_path"' EXIT
curl -fL --retry 3 --connect-timeout 20 -o "$tmp_path" "$FONT_URL"
actual_sha256=$(sha256_file "$tmp_path")
if [ "$actual_sha256" != "$FONT_SHA256" ]; then
    printf 'fetch-font: checksum mismatch for %s: %s\n' "$FONT_NAME" "$actual_sha256" >&2
    exit 1
fi
mv "$tmp_path" "$FONT_PATH"
printf '%s\n' "$FONT_PATH"
