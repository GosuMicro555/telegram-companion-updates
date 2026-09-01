#!/usr/bin/env bash
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

create_update_zip() {
  ditto -c -k --sequesterRsrc --keepParent "$APP_PATH" "$UPDATE_ZIP"
}

main() {
  require_macos_arm64_host
  [[ -d "$APP_PATH" ]] || die "application bundle is missing: $APP_PATH"
  verify_public_bootstrap_bundle "$STAGED_BUNDLE_PATH" "$PUBLIC_BOOTSTRAP_BUNDLE_SHA256"
  PUBLIC_BOOTSTRAP_VERIFY_BUNDLE_PATH="$STAGED_BUNDLE_PATH" bash "$SCRIPT_DIR/verify-public-release-bootstrap.sh"
  mkdir -p "$ARTIFACT_DIR"
  ditto -c -k --sequesterRsrc --keepParent "$APP_PATH" "$NOTARY_ZIP"
  xcrun notarytool submit "$NOTARY_ZIP" --keychain-profile "$APPLE_NOTARY_KEYCHAIN_PROFILE" --wait
  xcrun stapler staple "$APP_PATH"
  xcrun stapler validate "$APP_PATH"
  create_update_zip
  hdiutil create -volname "$APP_NAME" -srcfolder "$APP_PATH" -ov -format UDZO "$DMG_PATH"
  codesign --force --timestamp --sign "$APPLE_CODESIGN_IDENTITY" "$DMG_PATH"
  codesign --verify --strict --verbose=4 "$DMG_PATH"
  xcrun notarytool submit "$DMG_PATH" --keychain-profile "$APPLE_NOTARY_KEYCHAIN_PROFILE" --wait
  xcrun stapler staple "$DMG_PATH"
  xcrun stapler validate "$DMG_PATH"
}

main "$@"
