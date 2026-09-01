#!/usr/bin/env bash
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

sign_path() {
  local path="$1"
  codesign --force --options runtime --timestamp --sign "$APPLE_CODESIGN_IDENTITY" "$path"
}

sign_sparkle_framework() {
  local framework="$APP_PATH/Contents/Frameworks/Sparkle.framework"
  local version="$framework/Versions/B"
  [[ -d "$framework" ]] || die "Sparkle.framework is missing"

  if [[ -d "$version/XPCServices/Installer.xpc" ]]; then
    sign_path "$version/XPCServices/Installer.xpc"
  fi
  if [[ -d "$version/XPCServices/Downloader.xpc" ]]; then
    codesign --force --options runtime --timestamp --preserve-metadata=entitlements \
      --sign "$APPLE_CODESIGN_IDENTITY" "$version/XPCServices/Downloader.xpc"
  fi
  sign_path "$version/Autoupdate"
  sign_path "$version/Updater.app"
  sign_path "$framework"
}

main() {
  require_macos_arm64_host
  [[ -d "$APP_PATH" ]] || die "application bundle is missing: $APP_PATH"
  [[ -f "$ENTITLEMENTS_PATH" ]] || die "release entitlements are missing"
  verify_public_bootstrap_bundle "$STAGED_BUNDLE_PATH" "$PUBLIC_BOOTSTRAP_BUNDLE_SHA256"
  PUBLIC_BOOTSTRAP_VERIFY_BUNDLE_PATH="$STAGED_BUNDLE_PATH" bash "$SCRIPT_DIR/verify-public-release-bootstrap.sh"

  while IFS= read -r -d '' path; do
    sign_path "$path"
  done < <(find "$APP_PATH/Contents" -path "$APP_PATH/Contents/Frameworks/Sparkle.framework" -prune -o \
    -type f \( -perm -111 -o -name '*.dylib' \) -print0)

  while IFS= read -r -d '' framework; do
    sign_path "$framework"
  done < <(find "$APP_PATH/Contents" -depth -type d -name '*.framework' \
    ! -path "$APP_PATH/Contents/Frameworks/Sparkle.framework" -print0)

  sign_sparkle_framework

  codesign --force --options runtime --timestamp --entitlements "$ENTITLEMENTS_PATH" \
    --sign "$APPLE_CODESIGN_IDENTITY" "$APP_PATH"
  codesign --verify --strict --verbose=4 "$APP_PATH"
  require_arm64_bundle "$APP_PATH"
}

main "$@"
