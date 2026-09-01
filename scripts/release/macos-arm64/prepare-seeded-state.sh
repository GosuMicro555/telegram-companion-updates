#!/usr/bin/env bash
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

SOURCE_ROOT="${SOURCE_ROOT:-$HOME/Library/Application Support/telegram-companion}"
SOURCE_DATA="${SOURCE_DATA:-$SOURCE_ROOT/data}"
TARGET_LICENSE_FILE="${TARGET_LICENSE_FILE:-}"
SEEDED_STATE_PATH="${SEEDED_STATE_PATH:-$RELEASE_ROOT/seed/state.tcs}"
BUNDLE_ID="${BUNDLE_ID:-seed-$RELEASE_VERSION-$(date -u +%Y%m%dT%H%M%SZ)}"
WORK_DIR=""
GO_BIN="${GO_BIN:-$HOME/.local/go/bin/go}"
EXCLUDED_SECRETS="${EXCLUDED_SECRETS:-}"

cleanup() {
  if [[ -n "$WORK_DIR" ]] && [[ -d "$WORK_DIR" ]]; then
    rm -rf -- "$WORK_DIR"
  fi
}

trap cleanup EXIT
trap 'exit 1' HUP INT TERM

copy_if_present() {
  local name="$1"
  local source="$SOURCE_DATA/$name"
  [[ -e "$source" ]] || return 0
  if find "$source" -type l -print -quit 2>/dev/null | grep -q .; then
    die "seed source contains a symbolic link: $name"
  fi
  ditto "$source" "$STAGED_DATA/$name"
}

main() {
  require_macos_arm64_host
  require_env "RELEASE_VERSION"
  require_env "TARGET_LICENSE_FILE"
  [[ -f "$TARGET_LICENSE_FILE" ]] || die "target license file is missing"
  [[ -f "$SOURCE_DATA/app.db" ]] || die "source database is missing"
  if pgrep -x "telegram-companion" >/dev/null 2>&1 || pgrep -x "Telegram Companion" >/dev/null 2>&1; then
    die "quit Telegram Companion before preparing seeded state"
  fi
  [[ -x "$GO_BIN" ]] || die "Go executable is unavailable: $GO_BIN"
  for command in sqlite3 ditto find grep mktemp; do
    require_command "$command"
  done

  umask 077
  WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/telegram-companion-state.XXXXXX")"
  STAGED_DATA="$WORK_DIR/data"
  mkdir -p "$STAGED_DATA" "$(dirname "$SEEDED_STATE_PATH")"

  sqlite3 "$SOURCE_DATA/app.db" ".timeout 5000" ".backup '$STAGED_DATA/app.db'"
  sqlite3 -bail "$STAGED_DATA/app.db" < "$SCRIPT_DIR/sanitize-seeded-state.sql"
  [[ "$(sqlite3 "$STAGED_DATA/app.db" 'PRAGMA integrity_check;')" == "ok" ]] ||
    die "staged database integrity check failed"
  [[ -z "$(sqlite3 "$STAGED_DATA/app.db" 'PRAGMA foreign_key_check;')" ]] ||
    die "staged database foreign key check failed"

  for entry in application-state.bolt gotd-import-staging tdata sessions; do
    copy_if_present "$entry"
  done

  (
    cd "$REPO_ROOT"
    local bundle_args=(
      -source-data "$STAGED_DATA" \
      -output "$SEEDED_STATE_PATH" \
      -bundle-id "$BUNDLE_ID" \
      -version "$RELEASE_VERSION" \
      -license-file "$TARGET_LICENSE_FILE"
    )
    if [[ -n "$EXCLUDED_SECRETS" ]]; then
      local excluded_secret
      while IFS= read -r excluded_secret; do
        [[ -n "$excluded_secret" ]] || continue
        bundle_args+=( -exclude-secret "$excluded_secret" )
      done < <(printf '%s\n' "$EXCLUDED_SECRETS" | tr ',' '\n')
    fi
    "$GO_BIN" run ./cmd/state-bundle "${bundle_args[@]}"
  )
}

main "$@"
