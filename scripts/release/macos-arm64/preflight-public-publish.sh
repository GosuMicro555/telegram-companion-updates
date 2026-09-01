#!/usr/bin/env bash
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

main() {
  require_macos_arm64_host
  require_public_release_channel
  for command in codesign gh jq base64 cmp readlink shasum xmllint; do
    require_command "$command"
  done
  require_env "GH_TOKEN"
  require_env "RELEASE_REPOSITORY"
  require_env "APPCAST_REPOSITORY"
  validate_revocation_build_metadata
  validate_publish_metadata
  resolve_master_public_parity
  read_master_public_parity_source_sha
  resolve_public_bootstrap_verification_path
  require_env "PARITY_EVIDENCE_PATH"
  require_env "MASTER_APP_PATH"
  require_env "PUBLIC_APP_PATH"
  require_env "SOURCE_ROOT"
  require_env "SOURCE_SHA"
  MASTER_APP_PATH="$MASTER_APP_PATH" \
  PUBLIC_APP_PATH="$PUBLIC_APP_PATH" \
  RELEASE_VERSION="$RELEASE_VERSION" \
  SOURCE_ROOT="$SOURCE_ROOT" \
  SOURCE_SHA="$SOURCE_SHA" \
  PARITY_EVIDENCE_PATH="$PARITY_EVIDENCE_PATH" \
    bash "$SCRIPT_DIR/master-public-parity.sh" verify
  PUBLIC_BOOTSTRAP_VERIFY_BUNDLE_PATH="$STAGED_BUNDLE_PATH" bash "$SCRIPT_DIR/verify-public-release-bootstrap.sh"
  [[ -f "$UPDATE_ZIP" ]] || die "public update archive is missing: $UPDATE_ZIP"
  [[ -f "$DMG_PATH" ]] || die "public disk image is missing: $DMG_PATH"
  [[ -f "$APPCAST_PATH" ]] || die "signed appcast is missing: $APPCAST_PATH"
  [[ -f "$ARTIFACT_DIR/update-signature.txt" ]] || die "update signature is missing"
  [[ -f "$ARTIFACT_DIR/SHA256SUMS" ]] || die "release checksums are missing"
  require_public_distribution_signature "$PUBLIC_APP_PATH"
  verify_public_update_archive_signature "$UPDATE_ZIP"
  printf '%s\n' "public update publication preflight passed"
}

main "$@"
