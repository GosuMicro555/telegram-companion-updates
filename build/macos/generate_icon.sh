#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SOURCE_SVG="$ROOT_DIR/frontend/src/assets/telegram-turquoise.svg"
ICONSET_DIR="$ROOT_DIR/build/darwin/Telegram Companion.iconset"
ICONSET_ICON="$ROOT_DIR/build/darwin/icon.icns"
WAILS_ICON="$ROOT_DIR/build/appicon.png"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

command -v qlmanage >/dev/null 2>&1 || { echo "icon build requires qlmanage" >&2; exit 1; }
command -v sips >/dev/null 2>&1 || { echo "icon build requires sips" >&2; exit 1; }
command -v iconutil >/dev/null 2>&1 || { echo "icon build requires iconutil" >&2; exit 1; }
[[ -f "$SOURCE_SVG" ]] || { echo "logo asset is missing: $SOURCE_SVG" >&2; exit 1; }

mkdir -p "$ROOT_DIR/build/darwin" "$ROOT_DIR/build"
qlmanage -t -s 2048 -o "$TMP_DIR" "$SOURCE_SVG" >/dev/null
SOURCE_PNG="$TMP_DIR/telegram-turquoise.svg.png"
[[ -f "$SOURCE_PNG" ]] || { echo "qlmanage did not render the logo SVG" >&2; exit 1; }

rm -rf "$ICONSET_DIR"
mkdir -p "$ICONSET_DIR"
for size in 16 32 128 256 512; do
  sips -z "$size" "$size" "$SOURCE_PNG" --out "$ICONSET_DIR/icon_${size}x${size}.png" >/dev/null
  if [[ "$size" -le 512 ]]; then
    double=$((size * 2))
    sips -z "$double" "$double" "$SOURCE_PNG" --out "$ICONSET_DIR/icon_${size}x${size}@2x.png" >/dev/null
  fi
done

cp "$ICONSET_DIR/icon_512x512@2x.png" "$WAILS_ICON"
iconutil -c icns "$ICONSET_DIR" -o "$ICONSET_ICON"
