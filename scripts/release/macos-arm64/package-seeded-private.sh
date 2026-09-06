#!/usr/bin/env bash
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

SEEDED_DMG_PATH="$ARTIFACT_DIR/Telegram-Companion-$RELEASE_VERSION-arm64-Public-Seeded-PRIVATE.dmg"
SEEDED_DMG_SHA256_PATH="$SEEDED_DMG_PATH.sha256"
TARGET_LICENSE_FILE="${TARGET_LICENSE_FILE:-}"
GO_BIN="${GO_BIN:-$HOME/.local/go/bin/go}"
WORK_DIR=""

cleanup() {
  if [[ -n "$WORK_DIR" ]] && [[ -d "$WORK_DIR" ]]; then
    rm -rf -- "$WORK_DIR"
  fi
}

trap cleanup EXIT
trap 'exit 1' HUP INT TERM

main() {
  require_macos_arm64_host
  require_env "RELEASE_VERSION"
  require_env "PUBLIC_APP_PATH"
  require_env "SEEDED_STATE_PATH"
  require_env "TARGET_LICENSE_FILE"
  [[ "$RELEASE_VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] ||
    die "RELEASE_VERSION must be a numeric three-part version without a leading v"
  [[ -d "$PUBLIC_APP_PATH" ]] || die "public application bundle is missing"
  [[ -f "$SEEDED_STATE_PATH" ]] && [[ ! -L "$SEEDED_STATE_PATH" ]] ||
    die "seeded state file is missing or is not a regular file"
  [[ -f "$TARGET_LICENSE_FILE" ]] && [[ ! -L "$TARGET_LICENSE_FILE" ]] ||
    die "target license file is missing or is not a regular file"
  [[ -x "$GO_BIN" ]] || die "Go executable is unavailable: $GO_BIN"

  for command in codesign ditto hdiutil shasum install mktemp; do
    require_command "$command"
  done

  umask 077
  (
    cd "$REPO_ROOT"
    "$GO_BIN" run ./cmd/state-bundle-validate \
      -state "$SEEDED_STATE_PATH" \
      -license-file "$TARGET_LICENSE_FILE" \
      -version "$RELEASE_VERSION"
  )

  codesign --verify --strict --verbose=4 "$PUBLIC_APP_PATH"
  require_arm64_bundle "$PUBLIC_APP_PATH"

  mkdir -p "$ARTIFACT_DIR"
  WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/telegram-companion-seeded.XXXXXX")"
  local dmg_stage="$WORK_DIR/dmg-stage"
  local staged_app_path="$dmg_stage/$APP_NAME.app"
  local staged_state_path="$staged_app_path/Contents/Resources/bootstrap-state/state.tcs"
  readonly DMG_STAGE="$dmg_stage"
  readonly STAGED_APP_PATH="$staged_app_path"
  readonly STAGED_STATE_PATH="$staged_state_path"

  mkdir -p "$DMG_STAGE"
  ditto "$PUBLIC_APP_PATH" "$STAGED_APP_PATH"
  install -d -m 0755 "$(dirname "$STAGED_STATE_PATH")"
  install -m 0644 "$SEEDED_STATE_PATH" "$STAGED_STATE_PATH"
  APP_PATH="$STAGED_APP_PATH" bash "$SCRIPT_DIR/sign-adhoc.sh" >/dev/null

  ln -s /Applications "$DMG_STAGE/Applications"
  hdiutil create -volname "$APP_NAME" -srcfolder "$DMG_STAGE" -ov -format UDZO \
    "$SEEDED_DMG_PATH" >/dev/null
  (
    cd "$ARTIFACT_DIR"
    shasum -a 256 "$(basename "$SEEDED_DMG_PATH")" > "$(basename "$SEEDED_DMG_SHA256_PATH")"
  )
}

main "$@"
