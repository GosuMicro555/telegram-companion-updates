#!/usr/bin/env bash
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

readonly OUTPUT_PATH="${1:-}"
readonly KEY_ROOT="${KEY_ROOT:-$HOME/.config/telegram-companion-release/keys}"

[[ -n "$OUTPUT_PATH" ]] || die "output path is required"
[[ ! -d "$OUTPUT_PATH" ]] || die "output path must be a file"

read_key() {
  local filename="$1"
  local path="$KEY_ROOT/$filename"
  [[ -f "$path" ]] && [[ ! -L "$path" ]] || die "public key file is unavailable: $filename"
  tr -d '\r\n' < "$path"
}

LICENSE_PUBLIC_KEY="$(read_key license-public-key.b64)"
SPARKLE_PUBLIC_ED_KEY="$(read_key sparkle-ed25519-public.b64)"
REVOCATION_PUBLIC_KEY="$(read_key revocation-ed25519-public.b64)"
APPCAST_URL="${APPCAST_URL:-https://raw.githubusercontent.com/GosuMicro555/telegram-companion-updates/main/appcast.xml}"
REVOCATION_MANIFEST_URL="$EXPECTED_REVOCATION_MANIFEST_URL"
REVOCATION_KEY_ID="$EXPECTED_REVOCATION_KEY_ID"

validate_revocation_build_metadata
[[ "$APPCAST_URL" =~ ^https://[^[:space:]]+$ ]] || die "APPCAST_URL must be an HTTPS URL"

umask 077
mkdir -p "$(dirname "$OUTPUT_PATH")"
temporary_path="$(mktemp "${OUTPUT_PATH}.tmp.XXXXXX")"
cleanup_env() {
  rm -f -- "$temporary_path"
}
trap cleanup_env EXIT HUP INT TERM

{
  printf 'LICENSE_PUBLIC_KEY=%q\n' "$LICENSE_PUBLIC_KEY"
  printf 'SPARKLE_PUBLIC_ED_KEY=%q\n' "$SPARKLE_PUBLIC_ED_KEY"
  printf 'APPCAST_URL=%q\n' "$APPCAST_URL"
  printf 'REVOCATION_MANIFEST_URL=%q\n' "$REVOCATION_MANIFEST_URL"
  printf 'REVOCATION_KEY_ID=%q\n' "$REVOCATION_KEY_ID"
  printf 'REVOCATION_PUBLIC_KEY=%q\n' "$REVOCATION_PUBLIC_KEY"
} > "$temporary_path"
chmod 0600 "$temporary_path"
mv -f -- "$temporary_path" "$OUTPUT_PATH"
temporary_path=""
printf '%s\n' "release environment prepared"
