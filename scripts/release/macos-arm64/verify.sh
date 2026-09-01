#!/usr/bin/env bash
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

verify_nested_code() {
  while IFS= read -r -d '' bundle; do
    codesign --verify --strict --verbose=4 "$bundle"
  done < <(find "$APP_PATH/Contents" -depth -type d \
    \( -name '*.framework' -o -name '*.app' -o -name '*.xpc' \) -print0)

  while IFS= read -r -d '' executable; do
    codesign --verify --strict --verbose=4 "$executable"
  done < <(find "$APP_PATH/Contents" -type f \( -perm -111 -o -name '*.dylib' \) -print0)
}

main() {
  require_macos_arm64_host
  validate_revocation_build_metadata
  [[ -d "$APP_PATH" ]] || die "application bundle is missing: $APP_PATH"
  verify_public_bootstrap_bundle "$STAGED_BUNDLE_PATH" "$PUBLIC_BOOTSTRAP_BUNDLE_SHA256"
  PUBLIC_BOOTSTRAP_VERIFY_BUNDLE_PATH="$STAGED_BUNDLE_PATH" bash "$SCRIPT_DIR/verify-public-release-bootstrap.sh"
  codesign --verify --strict --verbose=4 "$APP_PATH"
  verify_nested_code
  spctl --assess --type execute --verbose=4 "$APP_PATH"
  xcrun stapler validate "$APP_PATH"
  codesign --verify --strict --verbose=4 "$DMG_PATH"
  spctl --assess --type open --context context:primary-signature --verbose=4 "$DMG_PATH"
  xcrun stapler validate "$DMG_PATH"
  require_arm64_bundle "$APP_PATH"
  verify_lyrebird_payload "$APP_PATH"
  verify_removed_transfer_capability_absent "$APP_PATH"
  verify_unseeded_update_archive "$UPDATE_ZIP"
  verify_removed_transfer_update_archive_absent "$UPDATE_ZIP"
  grep -aFq "$REVOCATION_BUILD_METADATA" "$APP_PATH/Contents/MacOS/$APP_EXECUTABLE" ||
    die "application is missing its linker-injected revocation metadata fingerprint"
  otool -L "$APP_PATH/Contents/MacOS/$APP_EXECUTABLE" |
    grep -Fq '@rpath/Sparkle.framework/Versions/B/Sparkle' || die "application is not linked to bundled Sparkle"
  [[ "$(/usr/libexec/PlistBuddy -c 'Print :LSMinimumSystemVersion' "$APP_PATH/Contents/Info.plist")" == "$MINIMUM_MACOS" ]] ||
    die "application minimum macOS version is wrong"
  [[ "$(/usr/libexec/PlistBuddy -c 'Print :SUFeedURL' "$APP_PATH/Contents/Info.plist")" == "$APPCAST_URL" ]] ||
    die "application appcast URL is wrong"
  [[ "$(/usr/libexec/PlistBuddy -c 'Print :SUPublicEDKey' "$APP_PATH/Contents/Info.plist")" == "$SPARKLE_PUBLIC_ED_KEY" ]] ||
    die "application Sparkle public key is wrong"
  grep -Fq "$(basename "$UPDATE_ZIP")" "$APPCAST_PATH" || die "appcast does not reference the update archive"
  ! grep -Fq "$(basename "$DMG_PATH")" "$APPCAST_PATH" || die "appcast references the direct-delivery DMG"
  (
    cd "$ARTIFACT_DIR"
    shasum -a 256 -c SHA256SUMS
  )
}

main "$@"
