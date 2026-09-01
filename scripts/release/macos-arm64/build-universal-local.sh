#!/usr/bin/env bash
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

PUBLIC_VERSION="${PUBLIC_VERSION:-}"
PUBLIC_BOOTSTRAP_SEED_EXPORT_FILE="${PUBLIC_BOOTSTRAP_SEED_EXPORT_FILE:-}"
PUBLIC_BOOTSTRAP_SEED_PASSWORD_FILE="${PUBLIC_BOOTSTRAP_SEED_PASSWORD_FILE:-}"
PUBLIC_BOOTSTRAP_SEED_ID=""
PUBLIC_BOOTSTRAP_SEED_ID_FILE=""
PUBLIC_BOOTSTRAP_SEED_KEY_FILE=""
PUBLIC_BOOTSTRAP_BUNDLE_PATH="${PUBLIC_BOOTSTRAP_BUNDLE_PATH:-}"
GO_BIN="${GO_BIN:-go}"
WORK_DIR=""
DMG_MOUNT_POINT=""
DMG_ATTACHED=0
FINAL_DMG_PATH=""
DMG_STAGED_BUNDLE_PATH=""

detach_final_dmg() {
  [[ "$DMG_ATTACHED" == "1" ]] || return 0
  if ! hdiutil detach "$DMG_MOUNT_POINT" >/dev/null; then
    return 1
  fi
  DMG_ATTACHED=0
}

cleanup() {
  local status=$?
  trap - EXIT
  if [[ -n "$PUBLIC_BOOTSTRAP_SEED_KEY_FILE" ]] && ! rm -f -- "$PUBLIC_BOOTSTRAP_SEED_KEY_FILE"; then
    status=1
  fi
  if ! detach_final_dmg; then
    status=1
  fi
  if [[ "$DMG_ATTACHED" == "0" ]] && [[ -n "$WORK_DIR" ]] && [[ -d "$WORK_DIR" ]]; then
    rm -rf -- "$WORK_DIR"
  fi
  exit "$status"
}

trap cleanup EXIT
trap 'exit 1' HUP INT TERM

sha256_file() {
  shasum -a 256 -- "$1" | awk 'NR == 1 { print $1 }'
}

verify_public_seed_bundle() {
  local bundle_path="$1"
  (
    cd "$REPO_ROOT"
    "$GO_BIN" run ./scripts/release/macos-arm64/cmd/verify-public-bootstrap \
      -bundle "$bundle_path" \
      -seed-id "$PUBLIC_BOOTSTRAP_SEED_ID" \
      -version "$PUBLIC_VERSION" \
      -seed-key-file "$PUBLIC_BOOTSTRAP_SEED_KEY_FILE"
  )
}

verify_public_seed_contents() {
  local bundle_path="$1"
  (
    cd "$REPO_ROOT"
    "$GO_BIN" run ./scripts/release/macos-arm64/cmd/verify-public-bootstrap-contents \
      -bundle "$bundle_path" \
      -seed-id "$PUBLIC_BOOTSTRAP_SEED_ID" \
      -version "$PUBLIC_VERSION" \
      -seed-key-file "$PUBLIC_BOOTSTRAP_SEED_KEY_FILE"
  )
}

verify_final_dmg_seed() {
  FINAL_DMG_PATH="$HOST_DOWNLOADS/Telegram-Companion-$PUBLIC_VERSION-arm64-private.dmg"
  [[ -f "$FINAL_DMG_PATH" ]] && [[ ! -L "$FINAL_DMG_PATH" ]] ||
    die "final public disk image is missing or is not a regular file"

  DMG_MOUNT_POINT="$WORK_DIR/final-dmg"
  mkdir -p "$DMG_MOUNT_POINT"
  hdiutil attach -readonly -nobrowse -mountpoint "$DMG_MOUNT_POINT" "$FINAL_DMG_PATH" >/dev/null
  DMG_ATTACHED=1
  DMG_STAGED_BUNDLE_PATH="$DMG_MOUNT_POINT/$APP_NAME.app/Contents/Resources/bootstrap-state/state.tcs"
  cmp -s "$PUBLIC_BOOTSTRAP_BUNDLE_PATH" "$DMG_STAGED_BUNDLE_PATH" ||
    die "final DMG public bootstrap bundle differs from the staged seed"
  verify_public_bootstrap_bundle "$DMG_STAGED_BUNDLE_PATH" "$PUBLIC_BOOTSTRAP_BUNDLE_SHA256"
  verify_public_seed_bundle "$DMG_STAGED_BUNDLE_PATH"
  verify_public_seed_contents "$DMG_STAGED_BUNDLE_PATH"
  detach_final_dmg
}

main() {
  local export_directory password_directory

  require_macos_arm64_host
  require_env "PUBLIC_VERSION"
  require_env "PUBLIC_BOOTSTRAP_SEED_EXPORT_FILE"
  require_env "PUBLIC_BOOTSTRAP_SEED_PASSWORD_FILE"
  require_env "HOST_DOWNLOADS"
  require_command "$GO_BIN"
  for command in awk cat cmp hdiutil mktemp shasum; do
    require_command "$command"
  done
  [[ -f "$PUBLIC_BOOTSTRAP_SEED_EXPORT_FILE" ]] && [[ ! -L "$PUBLIC_BOOTSTRAP_SEED_EXPORT_FILE" ]] ||
    die "PUBLIC_BOOTSTRAP_SEED_EXPORT_FILE must be a regular file"
  [[ -f "$PUBLIC_BOOTSTRAP_SEED_PASSWORD_FILE" ]] && [[ ! -L "$PUBLIC_BOOTSTRAP_SEED_PASSWORD_FILE" ]] ||
    die "PUBLIC_BOOTSTRAP_SEED_PASSWORD_FILE must be a regular file"
  export_directory="$(cd "$(dirname "$PUBLIC_BOOTSTRAP_SEED_EXPORT_FILE")" && pwd -P)"
  password_directory="$(cd "$(dirname "$PUBLIC_BOOTSTRAP_SEED_PASSWORD_FILE")" && pwd -P)"
  PUBLIC_BOOTSTRAP_SEED_EXPORT_FILE="$export_directory/$(basename "$PUBLIC_BOOTSTRAP_SEED_EXPORT_FILE")"
  PUBLIC_BOOTSTRAP_SEED_PASSWORD_FILE="$password_directory/$(basename "$PUBLIC_BOOTSTRAP_SEED_PASSWORD_FILE")"

  umask 077
  WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/telegram-companion-universal-local.XXXXXX")"
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
  PUBLIC_BOOTSTRAP_SEED_ID="$(cat "$PUBLIC_BOOTSTRAP_SEED_ID_FILE")"
  [[ "$PUBLIC_BOOTSTRAP_SEED_ID" =~ ^[0-9a-f]{32}$ ]] ||
    die "decrypted build seed identity is invalid"
  if [[ -z "$PUBLIC_BOOTSTRAP_BUNDLE_PATH" ]]; then
    PUBLIC_BOOTSTRAP_BUNDLE_PATH="$WORK_DIR/bootstrap-state.tcs"
  fi

  RELEASE_VERSION="$PUBLIC_VERSION"
  export RELEASE_VERSION PUBLIC_BOOTSTRAP_SEED_ID PUBLIC_BOOTSTRAP_SEED_KEY_FILE
  export PUBLIC_BOOTSTRAP_BUNDLE_PATH
  bash "$SCRIPT_DIR/prepare-public-bootstrap-bundle.sh"

  PUBLIC_BOOTSTRAP_BUNDLE_SHA256="$(sha256_file "$PUBLIC_BOOTSTRAP_BUNDLE_PATH")"
  verify_public_bootstrap_bundle "$PUBLIC_BOOTSTRAP_BUNDLE_PATH" "$PUBLIC_BOOTSTRAP_BUNDLE_SHA256"
  verify_public_seed_bundle "$PUBLIC_BOOTSTRAP_BUNDLE_PATH"
  verify_public_seed_contents "$PUBLIC_BOOTSTRAP_BUNDLE_PATH"
  export PUBLIC_BOOTSTRAP_BUNDLE_PATH PUBLIC_BOOTSTRAP_BUNDLE_SHA256

  bash "$SCRIPT_DIR/build-dual-local.sh"
  verify_final_dmg_seed
}

main "$@"
