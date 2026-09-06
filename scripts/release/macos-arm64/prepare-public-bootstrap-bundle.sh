#!/usr/bin/env bash
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

SOURCE_ROOT="${SOURCE_ROOT:-$HOME/Library/Application Support/telegram-companion}"
SOURCE_DATA="${SOURCE_DATA:-$SOURCE_ROOT/data}"
PUBLIC_BOOTSTRAP_BUNDLE_PATH="${PUBLIC_BOOTSTRAP_BUNDLE_PATH:-}"
PUBLIC_BOOTSTRAP_SEED_ID="${PUBLIC_BOOTSTRAP_SEED_ID:-}"
PUBLIC_BOOTSTRAP_SEED_KEY_FILE="${PUBLIC_BOOTSTRAP_SEED_KEY_FILE:-}"
PUBLIC_BOOTSTRAP_SEED_KEY_ENV="${PUBLIC_BOOTSTRAP_SEED_KEY_ENV:-PUBLIC_BOOTSTRAP_SEED_KEY}"
GO_BIN="${GO_BIN:-go}"
WORK_DIR=""

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
    die "public bootstrap source contains a symbolic link: $name"
  fi
  ditto "$source" "$STAGED_DATA/$name"
}

main() {
  local -a bundle_args=()
  local registered_secrets=""
  local required_secret=""
  local secret_name=""

  require_macos_arm64_host
  require_env "RELEASE_VERSION"
  require_env "PUBLIC_BOOTSTRAP_SEED_ID"
  require_env "PUBLIC_BOOTSTRAP_BUNDLE_PATH"
  [[ "$PUBLIC_BOOTSTRAP_BUNDLE_PATH" == /* ]] || die "PUBLIC_BOOTSTRAP_BUNDLE_PATH must be absolute"
  [[ "$PUBLIC_BOOTSTRAP_BUNDLE_PATH" != "$REPO_ROOT" ]] || die "PUBLIC_BOOTSTRAP_BUNDLE_PATH must be outside the repository"
  [[ "$PUBLIC_BOOTSTRAP_BUNDLE_PATH" != "$REPO_ROOT"/* ]] || die "PUBLIC_BOOTSTRAP_BUNDLE_PATH must be outside the repository"
  [[ -f "$SOURCE_DATA/app.db" ]] || die "source database is missing"
  require_command "$GO_BIN"
  for command in sqlite3 ditto find grep head mktemp; do
    require_command "$command"
  done

  umask 077
  WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/telegram-companion-public-bootstrap.XXXXXX")"
  STAGED_DATA="$WORK_DIR/data"
  mkdir -p "$STAGED_DATA" "$(dirname "$PUBLIC_BOOTSTRAP_BUNDLE_PATH")"

  sqlite3 "$SOURCE_DATA/app.db" ".timeout 5000" ".backup '$STAGED_DATA/app.db'"
  sqlite3 -bail "$STAGED_DATA/app.db" < "$SCRIPT_DIR/sanitize-seeded-state.sql"
  sqlite3 -bail "$STAGED_DATA/app.db" 'PRAGMA wal_checkpoint(TRUNCATE); PRAGMA journal_mode=DELETE;' >/dev/null
  rm -f -- "$STAGED_DATA/app.db-wal" "$STAGED_DATA/app.db-shm"
  [[ "$(sqlite3 "$STAGED_DATA/app.db" 'PRAGMA integrity_check;')" == "ok" ]] ||
    die "staged database integrity check failed"
  [[ -z "$(sqlite3 "$STAGED_DATA/app.db" 'PRAGMA foreign_key_check;')" ]] ||
    die "staged database foreign key check failed"

  for entry in application-state.bolt gotd-import-staging tdata sessions; do
    copy_if_present "$entry"
  done
  if [[ -n "$PUBLIC_BOOTSTRAP_SEED_KEY_FILE" ]] && [[ -n "${!PUBLIC_BOOTSTRAP_SEED_KEY_ENV:-}" ]]; then
    die "set either PUBLIC_BOOTSTRAP_SEED_KEY_FILE or $PUBLIC_BOOTSTRAP_SEED_KEY_ENV, not both"
  fi
  if [[ -n "$PUBLIC_BOOTSTRAP_SEED_KEY_FILE" ]]; then
    [[ -f "$PUBLIC_BOOTSTRAP_SEED_KEY_FILE" ]] && [[ ! -L "$PUBLIC_BOOTSTRAP_SEED_KEY_FILE" ]] ||
      die "public bootstrap seed key file is missing or is not a regular file"
    bundle_args+=( -seed-key-file "$PUBLIC_BOOTSTRAP_SEED_KEY_FILE" )
  else
    [[ "$PUBLIC_BOOTSTRAP_SEED_KEY_ENV" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]] ||
      die "PUBLIC_BOOTSTRAP_SEED_KEY_ENV is invalid"
    [[ -n "${!PUBLIC_BOOTSTRAP_SEED_KEY_ENV:-}" ]] ||
      die "public bootstrap seed key environment variable is unset"
    bundle_args+=( -seed-key-env "$PUBLIC_BOOTSTRAP_SEED_KEY_ENV" )
  fi
  registered_secrets="$(
    cd "$REPO_ROOT"
    "$GO_BIN" run ./scripts/release/macos-arm64/cmd/list-bootstrap-secret-names
  )"
  for required_secret in \
    scout-message-key \
    outbound-target-key \
    proxy-credentials-v1 \
    telegram-account-credentials-v1; do
    printf '%s\n' "$registered_secrets" | grep -Fxq "$required_secret" ||
      die "required operational bootstrap secret is not registered: $required_secret"
  done
  while IFS= read -r secret_name; do
    [[ -n "$secret_name" ]] || continue
    case "$secret_name" in
      scout-message-key|outbound-target-key|proxy-credentials-v1|telegram-account-credentials-v1)
        ;;
      backup-recovery-key-v1)
        bundle_args+=( -exclude-secret "$secret_name" )
        ;;
      *)
        bundle_args+=( -exclude-secret "$secret_name" )
        ;;
    esac
  done <<< "$registered_secrets"
  (
    cd "$REPO_ROOT"
    "$GO_BIN" run ./cmd/state-bundle \
      -source-data "$STAGED_DATA" \
      -output "$PUBLIC_BOOTSTRAP_BUNDLE_PATH" \
      -seed-id "$PUBLIC_BOOTSTRAP_SEED_ID" \
      -version "$RELEASE_VERSION" \
      "${bundle_args[@]}"
  )
  [[ "$(LC_ALL=C head -c 7 "$PUBLIC_BOOTSTRAP_BUNDLE_PATH")" == "TCSEED2" ]] ||
    die "public bootstrap bundle is not a TCSEED2 envelope"
}

main "$@"
