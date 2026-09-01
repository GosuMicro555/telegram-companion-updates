#!/usr/bin/env bash
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

main() {
  require_macos_arm64_host
  require_public_release_channel
  for command in awk codesign ditto head hdiutil xcrun shasum openssl base64 cmp otool spctl xmllint; do
    require_command "$command"
  done
  require_env "RELEASE_VERSION"
  require_env "APPCAST_URL"
  require_env "BUILD_TAGS"
  require_env "APPLE_CODESIGN_IDENTITY"
  require_env "APPLE_NOTARY_KEYCHAIN_PROFILE"
  require_env "LICENSE_PUBLIC_KEY"
  require_env "SPARKLE_PRIVATE_KEY_FILE"
  require_env "SPARKLE_PUBLIC_ED_KEY"
  require_env "RELEASE_DOWNLOAD_BASE_URL"
  require_env "PUBLIC_BOOTSTRAP_BUNDLE_PATH"
  require_env "PUBLIC_BOOTSTRAP_BUNDLE_SHA256"
  [[ -d "$APP_PATH" ]] || die "application bundle is missing: $APP_PATH"
  [[ -f "$SPARKLE_PRIVATE_KEY_FILE" ]] || die "Sparkle private key file is missing"
  validate_build_metadata
  verify_public_bootstrap_bundle "$PUBLIC_BOOTSTRAP_BUNDLE_PATH" "$PUBLIC_BOOTSTRAP_BUNDLE_SHA256"
  bash "$SCRIPT_DIR/verify-public-release-bootstrap.sh"
  printf '%s\n' "public release-creation preflight passed"
}

main "$@"
