#!/usr/bin/env bash
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

main() {
  local expected_update_url actual_update_url

  [[ -f "$UPDATE_ZIP" ]] || die "signed update archive is missing: $UPDATE_ZIP"
  [[ -f "$DMG_PATH" ]] || die "direct-delivery disk image is missing: $DMG_PATH"
  [[ -f "$SPARKLE_PRIVATE_KEY_FILE" ]] || die "Sparkle private key file is missing"
  [[ -x "$SPARKLE_SIGN_UPDATE" ]] || die "pinned Sparkle sign_update tool is missing"
  [[ -x "$SPARKLE_GENERATE_APPCAST" ]] || die "pinned Sparkle generate_appcast tool is missing"
  mkdir -p "$ARTIFACT_DIR" "$APPCAST_INPUT_DIR"
  cp "$UPDATE_ZIP" "$APPCAST_INPUT_DIR/"
  "$SPARKLE_SIGN_UPDATE" "$UPDATE_ZIP" --ed-key-file "$SPARKLE_PRIVATE_KEY_FILE" > "$ARTIFACT_DIR/update-signature.txt"
  "$SPARKLE_GENERATE_APPCAST" --ed-key-file "$SPARKLE_PRIVATE_KEY_FILE" \
    --download-url-prefix "${RELEASE_DOWNLOAD_BASE_URL%/}/" \
    "$APPCAST_INPUT_DIR"
  [[ -f "$APPCAST_INPUT_DIR/appcast.xml" ]] || die "generate_appcast did not create appcast.xml"
  install -m 0644 "$APPCAST_INPUT_DIR/appcast.xml" "$APPCAST_PATH"
  grep -Fq 'sparkle:edSignature' "$APPCAST_PATH" || die "appcast is missing an EdDSA signature"
  grep -Fq "$(basename "$UPDATE_ZIP")" "$APPCAST_PATH" || die "appcast does not reference the update archive"
  expected_update_url="${RELEASE_DOWNLOAD_BASE_URL%/}/$(basename "$UPDATE_ZIP")"
  actual_update_url="$(xmllint --xpath 'string((//*[local-name()="enclosure"]/@url)[1])' "$APPCAST_PATH")"
  [[ "$actual_update_url" == "$expected_update_url" ]] || die "appcast update URL is wrong: $actual_update_url"
  ! grep -Fq "$(basename "$DMG_PATH")" "$APPCAST_PATH" || die "appcast must not reference the directly delivered DMG"
  (
    cd "$ARTIFACT_DIR"
    shasum -a 256 "$(basename "$UPDATE_ZIP")" > SHA256SUMS
  )
}

main "$@"
