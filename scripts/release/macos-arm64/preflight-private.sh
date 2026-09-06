#!/usr/bin/env bash
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

main() {
  require_macos_arm64_host
  require_command "$WAILS_BIN"
  for command in go jq lipo codesign ditto hdiutil shasum openssl curl tar unzip otool; do
    require_command "$command"
  done
  require_command file
  require_env "RELEASE_VERSION"
  require_env "APPCAST_URL"
  require_env "BUILD_TAGS"
  require_env "LICENSE_PUBLIC_KEY"
  require_env "SPARKLE_PUBLIC_ED_KEY"
  [[ "$SPARKLE_PUBLIC_ED_KEY" != "__SPARKLE_PUBLIC_ED_KEY__" ]] || die "Sparkle public key is a placeholder"
  validate_build_metadata
  validate_source_version_alignment
  require_clean_output_path
  [[ -f "$INFO_PLIST_PATH" ]] || die "Info.plist is missing"
  [[ -f "$ENTITLEMENTS_PATH" ]] || die "release entitlements are missing"
  [[ -f "$MANIFEST_PATH" ]] || die "payload manifest is missing"
  validate_payload_manifest
  grep -Fq '<string>13.0</string>' "$INFO_PLIST_PATH" || die "Info.plist must require macOS 13.0"
  grep -Fq '<string>__RELEASE_VERSION__</string>' "$INFO_PLIST_PATH" || die "Info.plist release version placeholder is missing"
  grep -Fq '<string>__APPCAST_URL__</string>' "$INFO_PLIST_PATH" || die "Info.plist appcast placeholder is missing"
  grep -Fq '<string>__SPARKLE_PUBLIC_ED_KEY__</string>' "$INFO_PLIST_PATH" || die "Info.plist Sparkle key placeholder is missing"
  printf '%s\n' "private DMG preflight passed for $TARGET_OS/$TARGET_ARCH"
}

main "$@"
