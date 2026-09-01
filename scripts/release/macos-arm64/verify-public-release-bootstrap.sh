#!/usr/bin/env bash
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

PUBLIC_BOOTSTRAP_SEED_EXPORT_FILE="${PUBLIC_BOOTSTRAP_SEED_EXPORT_FILE:-}"
PUBLIC_BOOTSTRAP_SEED_PASSWORD_FILE="${PUBLIC_BOOTSTRAP_SEED_PASSWORD_FILE:-}"
PUBLIC_BOOTSTRAP_VERIFY_BUNDLE_PATH="${PUBLIC_BOOTSTRAP_VERIFY_BUNDLE_PATH:-${PUBLIC_BOOTSTRAP_BUNDLE_PATH:-}}"
GO_BIN="${GO_BIN:-go}"
WORK_DIR=""
PUBLIC_BOOTSTRAP_SEED_ID_FILE=""
PUBLIC_BOOTSTRAP_SEED_KEY_FILE=""

cleanup() {
  local status=$?
  trap - EXIT
  if [[ -n "$PUBLIC_BOOTSTRAP_SEED_KEY_FILE" ]] && ! rm -f -- "$PUBLIC_BOOTSTRAP_SEED_KEY_FILE"; then
    status=1
  fi
  if [[ -n "$WORK_DIR" ]] && [[ -d "$WORK_DIR" ]]; then
    rm -rf -- "$WORK_DIR"
  fi
  exit "$status"
}

trap cleanup EXIT
trap 'exit 1' HUP INT TERM

main() {
  local seed_id

  require_macos_arm64_host
  require_env "RELEASE_VERSION"
  require_env "PUBLIC_BOOTSTRAP_BUNDLE_SHA256"
  require_env "PUBLIC_BOOTSTRAP_SEED_EXPORT_FILE"
  require_env "PUBLIC_BOOTSTRAP_SEED_PASSWORD_FILE"
  [[ -n "$PUBLIC_BOOTSTRAP_VERIFY_BUNDLE_PATH" ]] ||
    die "public bootstrap verification bundle path is unavailable"
  require_command "$GO_BIN"
  for command in cat mktemp rm; do
    require_command "$command"
  done
  [[ -f "$PUBLIC_BOOTSTRAP_SEED_EXPORT_FILE" ]] && [[ ! -L "$PUBLIC_BOOTSTRAP_SEED_EXPORT_FILE" ]] ||
    die "PUBLIC_BOOTSTRAP_SEED_EXPORT_FILE must be a regular file"
  [[ -f "$PUBLIC_BOOTSTRAP_SEED_PASSWORD_FILE" ]] && [[ ! -L "$PUBLIC_BOOTSTRAP_SEED_PASSWORD_FILE" ]] ||
    die "PUBLIC_BOOTSTRAP_SEED_PASSWORD_FILE must be a regular file"
  verify_public_bootstrap_bundle "$PUBLIC_BOOTSTRAP_VERIFY_BUNDLE_PATH" "$PUBLIC_BOOTSTRAP_BUNDLE_SHA256"

  umask 077
  WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/telegram-companion-public-release-verify.XXXXXX")"
  PUBLIC_BOOTSTRAP_SEED_ID_FILE="$WORK_DIR/bootstrap-seed.id"
  PUBLIC_BOOTSTRAP_SEED_KEY_FILE="$WORK_DIR/bootstrap-seed.key"
  (
    cd "$REPO_ROOT"
    "$GO_BIN" run ./scripts/release/macos-arm64/cmd/decrypt-build-seed \
      -artifact "$PUBLIC_BOOTSTRAP_SEED_EXPORT_FILE" \
      -password-file "$PUBLIC_BOOTSTRAP_SEED_PASSWORD_FILE" \
      -seed-id-output "$PUBLIC_BOOTSTRAP_SEED_ID_FILE" \
      -seed-key-output "$PUBLIC_BOOTSTRAP_SEED_KEY_FILE"
  )
  seed_id="$(cat "$PUBLIC_BOOTSTRAP_SEED_ID_FILE")"
  [[ "$seed_id" =~ ^[0-9a-f]{32}$ ]] || die "decrypted build seed identity is invalid"
  (
    cd "$REPO_ROOT"
    "$GO_BIN" run ./scripts/release/macos-arm64/cmd/verify-public-bootstrap \
      -bundle "$PUBLIC_BOOTSTRAP_VERIFY_BUNDLE_PATH" \
      -seed-id "$seed_id" \
      -version "$RELEASE_VERSION" \
      -seed-key-file "$PUBLIC_BOOTSTRAP_SEED_KEY_FILE"
    "$GO_BIN" run ./scripts/release/macos-arm64/cmd/verify-public-bootstrap-contents \
      -bundle "$PUBLIC_BOOTSTRAP_VERIFY_BUNDLE_PATH" \
      -seed-id "$seed_id" \
      -version "$RELEASE_VERSION" \
      -seed-key-file "$PUBLIC_BOOTSTRAP_SEED_KEY_FILE"
  )
  printf '%s\n' "public release bootstrap semantically verified"
}

main "$@"
