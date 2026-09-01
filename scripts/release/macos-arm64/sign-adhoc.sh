#!/usr/bin/env bash
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

adhoc_sign_path() {
  local path="$1"
  codesign --force --sign - "$path"
}

adhoc_sign_sparkle_framework() {
  local framework="$APP_PATH/Contents/Frameworks/Sparkle.framework"
  local version="$framework/Versions/B"
  [[ -d "$framework" ]] || die "Sparkle.framework is missing"

  if [[ -d "$version/XPCServices/Installer.xpc" ]]; then
    adhoc_sign_path "$version/XPCServices/Installer.xpc"
  fi
  if [[ -d "$version/XPCServices/Downloader.xpc" ]]; then
    codesign --force --preserve-metadata=entitlements --sign - "$version/XPCServices/Downloader.xpc"
  fi
  adhoc_sign_path "$version/Autoupdate"
  adhoc_sign_path "$version/Updater.app"
  adhoc_sign_path "$framework"
}

main() {
  require_macos_arm64_host
  [[ -d "$APP_PATH" ]] || die "application bundle is missing: $APP_PATH"
  [[ -f "$ENTITLEMENTS_PATH" ]] || die "release entitlements are missing"

  while IFS= read -r -d '' path; do
    adhoc_sign_path "$path"
  done < <(find "$APP_PATH/Contents" -path "$APP_PATH/Contents/Frameworks/Sparkle.framework" -prune -o \
    -type f \( -perm -111 -o -name '*.dylib' \) -print0)

  while IFS= read -r -d '' framework; do
    adhoc_sign_path "$framework"
  done < <(find "$APP_PATH/Contents" -depth -type d -name '*.framework' \
    ! -path "$APP_PATH/Contents/Frameworks/Sparkle.framework" -print0)

  adhoc_sign_sparkle_framework
  codesign --force --entitlements "$ENTITLEMENTS_PATH" --sign - "$APP_PATH"
  codesign --verify --strict --verbose=4 "$APP_PATH"
  require_arm64_bundle "$APP_PATH"
}

main "$@"
