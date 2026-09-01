#!/usr/bin/env bash
set -euo pipefail

readonly TARGET_OS="darwin"
readonly TARGET_ARCH="arm64"
readonly MINIMUM_MACOS="13.0"
readonly APP_NAME="Telegram Companion"
readonly APP_EXECUTABLE="telegram-companion"
readonly SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly REPO_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"
readonly MANIFEST_PATH="$REPO_ROOT/resources/macos-arm64/payload-manifest.json"
readonly ENTITLEMENTS_PATH="$REPO_ROOT/resources/macos-arm64/entitlements.release.plist"
readonly INFO_PLIST_PATH="$REPO_ROOT/build/darwin/Info.plist"
readonly EXPECTED_REVOCATION_MANIFEST_URL="https://raw.githubusercontent.com/GosuMicro555/telegram-companion-updates/main/revocations.tcrev"
readonly EXPECTED_REVOCATION_KEY_ID="revocation-2026-01"

RELEASE_VERSION="${RELEASE_VERSION:-}"
APPCAST_URL="${APPCAST_URL:-}"
REVOCATION_MANIFEST_URL="${REVOCATION_MANIFEST_URL:-}"
REVOCATION_KEY_ID="${REVOCATION_KEY_ID:-}"
REVOCATION_PUBLIC_KEY="${REVOCATION_PUBLIC_KEY:-}"
REVOCATION_BUILD_METADATA=""
APPCAST_BRANCH="${APPCAST_BRANCH:-main}"
RELEASE_CHANNEL="${RELEASE_CHANNEL:-local}"
BUILD_TAGS="${BUILD_TAGS:-}"
VITE_BUILD_TIMESTAMP="${VITE_BUILD_TIMESTAMP:-}"
RELEASE_ROOT="${RELEASE_ROOT:-$REPO_ROOT/build/release/macos-arm64}"
APP_PATH="${APP_PATH:-$RELEASE_ROOT/$APP_NAME.app}"
WAILS_APP_PATH="${WAILS_APP_PATH:-$REPO_ROOT/build/bin/$APP_EXECUTABLE.app}"
WAILS_BIN="${WAILS_BIN:-wails}"
ARTIFACT_DIR="${ARTIFACT_DIR:-$RELEASE_ROOT/artifacts}"
APPCAST_INPUT_DIR="${APPCAST_INPUT_DIR:-$RELEASE_ROOT/appcast-input}"
PAYLOAD_CACHE="${PAYLOAD_CACHE:-$RELEASE_ROOT/payload-cache}"
SPARKLE_PAYLOAD_ROOT="${SPARKLE_PAYLOAD_ROOT:-$RELEASE_ROOT/extracted/sparkle}"
SPARKLE_FRAMEWORK_PATH="${SPARKLE_FRAMEWORK_PATH:-$SPARKLE_PAYLOAD_ROOT/Sparkle.framework}"
SPARKLE_FRAMEWORK_PARENT="${SPARKLE_FRAMEWORK_PARENT:-$SPARKLE_PAYLOAD_ROOT}"
SPARKLE_SIGN_UPDATE="${SPARKLE_SIGN_UPDATE:-$SPARKLE_PAYLOAD_ROOT/bin/sign_update}"
SPARKLE_GENERATE_APPCAST="${SPARKLE_GENERATE_APPCAST:-$SPARKLE_PAYLOAD_ROOT/bin/generate_appcast}"
UPDATE_ZIP="${UPDATE_ZIP:-$ARTIFACT_DIR/Telegram-Companion-$RELEASE_VERSION-arm64.zip}"
DMG_PATH="${DMG_PATH:-$ARTIFACT_DIR/Telegram-Companion-$RELEASE_VERSION-arm64.dmg}"
NOTARY_ZIP="${NOTARY_ZIP:-$ARTIFACT_DIR/Telegram-Companion-$RELEASE_VERSION-notary.zip}"
APPCAST_PATH="${APPCAST_PATH:-$ARTIFACT_DIR/appcast.xml}"
PUBLIC_BOOTSTRAP_BUNDLE_SHA256="${PUBLIC_BOOTSTRAP_BUNDLE_SHA256:-}"
STAGED_BUNDLE_PATH="${STAGED_BUNDLE_PATH:-$APP_PATH/Contents/Resources/bootstrap-state/state.tcs}"
MASTER_PUBLIC_PARITY_ROOT="${MASTER_PUBLIC_PARITY_ROOT:-${PARITY_CACHE_ROOT:-$HOME/.cache/telegram-companion-release/master-public-parity}}"
PARITY_EVIDENCE_PATH="${PARITY_EVIDENCE_PATH:-}"
MASTER_APP_PATH="${MASTER_APP_PATH:-}"
PUBLIC_APP_PATH="${PUBLIC_APP_PATH:-}"
SOURCE_ROOT="${SOURCE_ROOT:-$REPO_ROOT}"
SOURCE_SHA="${SOURCE_SHA:-}"
PARITY_POINTER_SOURCE_SHA=""

die() {
  printf '%s\n' "release error: $*" >&2
  exit 1
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || die "required command is unavailable: $1"
}

safe_path_under_root() {
  local root="$1" relative_path="$2" root_canonical remaining component current
  [[ -d "$root" ]] && [[ ! -L "$root" ]] || die "trusted path root is missing or unsafe"
  case "$relative_path" in
    ""|/*|*"//"*|*".."*) die "trusted relative path is unsafe" ;;
  esac
  root_canonical="$(cd "$root" && pwd -P)" || die "trusted path root cannot be resolved"
  [[ "$root_canonical" = /* ]] || die "trusted path root is unsafe"
  current="$root_canonical"
  remaining="$relative_path"
  while [[ -n "$remaining" ]]; do
    component="${remaining%%/*}"
    [[ -n "$component" && "$component" != "." && "$component" != ".." ]] ||
      die "trusted relative path is unsafe"
    current="$current/$component"
    [[ ! -L "$current" ]] || die "trusted path contains a symlink"
    if [[ "$remaining" == */* ]]; then
      remaining="${remaining#*/}"
    else
      remaining=""
    fi
  done
  printf '%s\n' "$current"
}

safe_regular_file_under_root() {
  local path
  path="$(safe_path_under_root "$1" "$2")"
  [[ -f "$path" ]] && [[ ! -L "$path" ]] || die "trusted regular file is missing or unsafe"
  printf '%s\n' "$path"
}

require_env() {
  local name="$1"
  [[ -n "${!name:-}" ]] || die "required environment variable is unset: $name"
}

require_clean_source_tree() {
  local status
  [[ -d "$SOURCE_ROOT" && ! -L "$SOURCE_ROOT" ]] || die "source root is unsafe"
  status="$(git -C "$SOURCE_ROOT" status --porcelain=v1 --untracked-files=all 2>/dev/null)" ||
    die "source status is unavailable"
  [[ -z "$status" ]] || die "source tree is dirty; commit tracked and untracked changes before release"
}

resolve_master_public_parity() {
  local cache_parent="$HOME/.cache/telegram-companion-release"
  local pointer expected_root target target_suffix expected_evidence expected_master expected_public
  local version_root current target_canonical
  PARITY_POINTER_SOURCE_SHA=""
  if [[ -n "${PARITY_CACHE_ROOT:-}" && "$PARITY_CACHE_ROOT" != "$MASTER_PUBLIC_PARITY_ROOT" ]]; then
    die "PARITY_CACHE_ROOT and MASTER_PUBLIC_PARITY_ROOT must match"
  fi
  case "$MASTER_PUBLIC_PARITY_ROOT" in
    "$cache_parent"/master-public-parity|"$cache_parent"/master-public-parity/*) ;;
    *) die "master/public parity root must remain under the private release cache" ;;
  esac
  [[ "$MASTER_PUBLIC_PARITY_ROOT" = /* ]] || die "master/public parity root must be absolute"
  [[ "$MASTER_PUBLIC_PARITY_ROOT" != *".."* ]] || die "master/public parity root contains an unsafe path"
  current="$MASTER_PUBLIC_PARITY_ROOT"
  while [[ "$current" != "/" && "$current" != "." ]]; do
    [[ ! -L "$current" ]] || die "master/public parity root contains a symlink ancestor"
    current="$(dirname "$current")"
  done
  [[ -d "$MASTER_PUBLIC_PARITY_ROOT" && ! -L "$MASTER_PUBLIC_PARITY_ROOT" ]] ||
    die "master/public parity root is missing or unsafe"
  [[ -d "$MASTER_PUBLIC_PARITY_ROOT/current" && ! -L "$MASTER_PUBLIC_PARITY_ROOT/current" ]] ||
    die "master/public parity current directory is missing or unsafe"
  [[ -d "$MASTER_PUBLIC_PARITY_ROOT/versions" && ! -L "$MASTER_PUBLIC_PARITY_ROOT/versions" ]] ||
    die "master/public parity versions directory is missing or unsafe"
  [[ "$RELEASE_VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || die "RELEASE_VERSION is malformed"
  pointer="$MASTER_PUBLIC_PARITY_ROOT/current/$RELEASE_VERSION"
  [[ -L "$pointer" ]] || die "master/public parity current pointer is missing"
  target="$(readlink "$pointer")" || die "master/public parity current pointer cannot be read"
  expected_root="$MASTER_PUBLIC_PARITY_ROOT/versions/$RELEASE_VERSION/"
  [[ "$target" == "$expected_root"* ]] || die "master/public parity pointer targets an unexpected path"
  target_suffix="${target#"$expected_root"}"
  [[ "$target_suffix" =~ ^[0-9a-f]{40}$ ]] || die "master/public parity pointer targets an unexpected path"
  PARITY_POINTER_SOURCE_SHA="$target_suffix"
  export PARITY_POINTER_SOURCE_SHA
  version_root="$MASTER_PUBLIC_PARITY_ROOT/versions/$RELEASE_VERSION"
  [[ -d "$version_root" && ! -L "$version_root" ]] || die "master/public parity version directory is missing or unsafe"
  [[ -d "$target" && ! -L "$target" ]] || die "master/public parity snapshot is missing or unsafe"
  current="$(dirname "$pointer")"
  while [[ "$current" != "/" && "$current" != "." ]]; do
    [[ ! -L "$current" ]] || die "master/public parity path contains a symlink ancestor"
    current="$(dirname "$current")"
  done
  target_canonical="$(cd "$target" && pwd -P)" ||
    die "master/public parity snapshot cannot be resolved"
  [[ "$target_canonical" == "$MASTER_PUBLIC_PARITY_ROOT/versions/$RELEASE_VERSION/$target_suffix" ]] ||
    die "master/public parity snapshot resolves outside the private cache"
  expected_evidence="$target/master-public.parity"
  expected_master="$target/master/$APP_NAME.app"
  expected_public="$target/public/$APP_NAME.app"
  if [[ -n "$PARITY_EVIDENCE_PATH" && "$PARITY_EVIDENCE_PATH" != "$expected_evidence" ]]; then
    die "PARITY_EVIDENCE_PATH does not match the current parity snapshot"
  fi
  if [[ -n "$MASTER_APP_PATH" && "$MASTER_APP_PATH" != "$expected_master" ]]; then
    die "MASTER_APP_PATH does not match the current parity snapshot"
  fi
  if [[ -n "$PUBLIC_APP_PATH" && "$PUBLIC_APP_PATH" != "$expected_public" ]]; then
    die "PUBLIC_APP_PATH does not match the current parity snapshot"
  fi
  PARITY_EVIDENCE_PATH="$expected_evidence"
  MASTER_APP_PATH="$expected_master"
  PUBLIC_APP_PATH="$expected_public"
  SOURCE_ROOT="${SOURCE_ROOT:-$REPO_ROOT}"
  [[ -d "$SOURCE_ROOT" && ! -L "$SOURCE_ROOT" ]] || die "parity source root is missing or unsafe"
  export PARITY_EVIDENCE_PATH MASTER_APP_PATH PUBLIC_APP_PATH SOURCE_ROOT
}

read_master_public_parity_source_sha() {
  local line key value candidate="" count=0
  [[ -f "$PARITY_EVIDENCE_PATH" && ! -L "$PARITY_EVIDENCE_PATH" ]] ||
    die "master/public parity evidence is missing or unsafe"
  while IFS= read -r line || [[ -n "$line" ]]; do
    key="${line%%=*}"
    if [[ "$key" == "source_sha" ]]; then
      count=$((count + 1))
      candidate="${line#*=}"
    fi
  done < "$PARITY_EVIDENCE_PATH"
  [[ "$count" -eq 1 ]] || die "master/public parity source SHA is missing or duplicated"
  [[ "$candidate" =~ ^[0-9a-f]{40}$ ]] || die "master/public parity source SHA is malformed"
  [[ "${PARITY_POINTER_SOURCE_SHA:-}" =~ ^[0-9a-f]{40}$ ]] ||
    die "master/public parity pointer source SHA is unavailable"
  [[ "$candidate" == "$PARITY_POINTER_SOURCE_SHA" ]] ||
    die "master/public parity source SHA does not match pointer"
  SOURCE_SHA="$candidate"
  export SOURCE_SHA
}

resolve_public_bootstrap_verification_path() {
  local root staged_parent staged current
  require_env "RELEASE_ROOT"
  require_env "STAGED_BUNDLE_PATH"
  [[ "$RELEASE_ROOT" = /* ]] || die "RELEASE_ROOT must be absolute"
  [[ "$STAGED_BUNDLE_PATH" = /* ]] || die "STAGED_BUNDLE_PATH must be absolute"
  [[ -d "$RELEASE_ROOT" && ! -L "$RELEASE_ROOT" ]] ||
    die "release root is missing or unsafe"
  [[ -f "$STAGED_BUNDLE_PATH" && ! -L "$STAGED_BUNDLE_PATH" ]] ||
    die "staged bootstrap bundle is missing or unsafe"

  current="$STAGED_BUNDLE_PATH"
  while [[ "$current" != "/" && "$current" != "." ]]; do
    [[ ! -L "$current" ]] || die "staged bootstrap path contains a symlink"
    current="$(dirname "$current")"
  done
  staged_parent="$(dirname "$STAGED_BUNDLE_PATH")"
  [[ -d "$staged_parent" && ! -L "$staged_parent" ]] ||
    die "staged bootstrap directory is missing or unsafe"
  root="$(cd "$RELEASE_ROOT" && pwd -P)" || die "release root cannot be resolved"
  staged="$(cd "$staged_parent" && pwd -P)/$(basename "$STAGED_BUNDLE_PATH")" ||
    die "staged bootstrap path cannot be resolved"
  [[ "$staged" == "$root/"* ]] ||
    die "STAGED_BUNDLE_PATH must be inside RELEASE_ROOT"
  [[ "$staged" != "$MASTER_PUBLIC_PARITY_ROOT" &&
    "$staged" != "$MASTER_PUBLIC_PARITY_ROOT/"* ]] ||
    die "staged bootstrap path must not come from the parity cache"
  STAGED_BUNDLE_PATH="$staged"
  export STAGED_BUNDLE_PATH
}

verify_master_public_parity() {
  resolve_master_public_parity
  read_master_public_parity_source_sha
  bash "$SCRIPT_DIR/master-public-parity.sh" verify
}

require_public_release_channel() {
  [[ "$RELEASE_CHANNEL" == "public" ]] ||
    die "public release publication requires RELEASE_CHANNEL=public"
}

ensure_build_timestamp() {
  if [[ -z "$VITE_BUILD_TIMESTAMP" ]]; then
    VITE_BUILD_TIMESTAMP="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  fi
  [[ "$VITE_BUILD_TIMESTAMP" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$ ]] ||
    die "VITE_BUILD_TIMESTAMP must use UTC ISO-8601 format"
  export VITE_BUILD_TIMESTAMP
}

require_macos_arm64_host() {
  [[ "$(uname -s)" == "Darwin" ]] || die "release host must be macOS"
  [[ "$(uname -m)" == "arm64" ]] || die "release host must be Apple Silicon arm64"
}

is_macho_file() {
  [[ -f "$1" ]] && file -b "$1" | grep -q '^Mach-O'
}

require_clean_output_path() {
  if [[ -e "$RELEASE_ROOT" ]] && [[ -n "$(find "$RELEASE_ROOT" -mindepth 1 -maxdepth 1 -print -quit)" ]]; then
    die "release output is not empty: $RELEASE_ROOT"
  fi
}

require_arm64_macho() {
  local path="$1"
  [[ -f "$path" ]] || die "missing Mach-O file: $path"
  local architectures
  architectures="$(lipo -archs "$path")"
  [[ "$architectures" == "arm64" ]] || die "expected arm64-only Mach-O, found $architectures: $path"
}

require_arm64_bundle() {
  local bundle="$1"
  [[ -d "$bundle" ]] || die "missing application bundle: $bundle"
  require_arm64_macho "$bundle/Contents/MacOS/$APP_EXECUTABLE"
  while IFS= read -r -d '' executable; do
    is_macho_file "$executable" || continue
    require_arm64_macho "$executable"
  done < <(find "$bundle/Contents" -type f -print0)
}

verify_removed_transfer_capability_absent() {
  local bundle="$1"
  local path relative marker grep_status
  local markers=(
    "Get""MasterInboxStatus"
    "List""MasterInbox"
    "Ack""MasterInbox"
    "Master""TDataInbox"
    "MASTER_TDATA_""INBOX_ENABLED"
    "tdata new ""accounts"
    "tdata""relay"
    "RELAY_""BASE_URL"
    "MASTER_""INBOX_ID"
    "MASTER_""PUBLIC_KEY"
    "Relay""Status"
    "Claim""Relay"
    "Update""Relay"
    "Relay""BaseURL"
    "Relay""InboxID"
    "Relay""PublicKey"
    "relay""BaseURL"
    "master""InboxID"
    "master""PublicKey"
    "master-inbox-""private-key-v1"
    "master-inbox-""admin-token-v1"
    "SubmitTData""DriveLinks"
    "ListTData""ImportItems"
    "TData""ImportModal"
    "ConfigureTData""Import"
    "NewSession""Importer"
    "NewTData""ImportStore"
    "tdata-""import"
    "Add tdata ""accounts"
    "Добавить tdata ""аккаунты"
  )

  [[ -d "$bundle/Contents" ]] || die "application bundle contents are missing"
  while IFS= read -r -d '' path; do
    relative="${path#"$bundle/Contents"/}"
    for marker in "${markers[@]}"; do
      [[ "$relative" != *"$marker"* ]] || die "removed transfer capability remains in application bundle"
    done
    [[ -f "$path" ]] && [[ ! -L "$path" ]] || continue
    if LC_ALL=C grep -aFq -f <(printf '%s\n' "${markers[@]}") "$path"; then
      die "removed transfer capability remains in application bundle"
    else
      grep_status=$?
      [[ "$grep_status" -eq 1 ]] || die "application bundle capability scan failed"
    fi
  done < <(find "$bundle/Contents" -print0)
}

verify_removed_transfer_update_archive_absent() {
  local archive="$1"
  local scratch cleanup_prefix archive_status=0

  [[ -f "$archive" ]] && [[ ! -L "$archive" ]] || die "update archive is missing or is not a regular file"
  scratch="$(mktemp -d "${TMPDIR:-/tmp}/tc-update-inspect.XXXXXX")" || die "cannot create update inspection directory"
  cleanup_prefix="${TMPDIR:-/tmp}/tc-update-inspect."
  [[ "$scratch" == "$cleanup_prefix"* ]] || die "unsafe update inspection directory"
  chmod 700 "$scratch"

  (
    set -e
    ditto -x -k "$archive" "$scratch"
    [[ -d "$scratch/$APP_NAME.app" ]] || die "update archive does not contain the expected application bundle"
    [[ "$(find "$scratch" -mindepth 1 -maxdepth 1 -print | wc -l | tr -d '[:space:]')" == "1" ]] ||
      die "update archive contains unexpected top-level entries"
    verify_removed_transfer_capability_absent "$scratch/$APP_NAME.app"
  ) || archive_status=$?

  rm -rf -- "$scratch"
  [[ "$archive_status" -eq 0 ]] || die "update archive capability scan failed"
}

verify_removed_transfer_private_dmg_absent() {
  local image="$1"

  [[ -f "$image" ]] && [[ ! -L "$image" ]] || die "private disk image is missing or is not a regular file"
  (
    local scratch cleanup_prefix mount_point attach_output device="" mounted_app app_count
    scratch="$(mktemp -d "${TMPDIR:-/tmp}/tc-private-dmg-inspect.XXXXXX")" || die "cannot create private DMG inspection directory"
    cleanup_prefix="${TMPDIR:-/tmp}/tc-private-dmg-inspect."
    [[ "$scratch" == "$cleanup_prefix"* ]] || die "unsafe private DMG inspection directory"
    chmod 700 "$scratch"
    mount_point="$scratch/mount"
    mkdir "$mount_point"
    cleanup_private_dmg_scan() {
      if [[ -n "${device:-}" ]]; then
        hdiutil detach "$device" >/dev/null 2>&1 || true
      fi
      hdiutil detach "$mount_point" >/dev/null 2>&1 || true
      rm -rf -- "$scratch"
    }
    trap cleanup_private_dmg_scan EXIT
    trap 'exit 1' HUP INT TERM

    attach_output="$(hdiutil attach -readonly -nobrowse -noautoopen -mountpoint "$mount_point" "$image")"
    device="$(awk '$1 ~ /^\/dev\// { device=$1 } END { print device }' <<<"$attach_output")"
    [[ -n "$device" ]] || die "could not identify private DMG device"
    app_count="$(find "$mount_point" -mindepth 1 -maxdepth 1 -name '*.app' -print | wc -l | tr -d '[:space:]')"
    [[ "$app_count" == "1" ]] || die "private DMG must contain exactly one application bundle"
    mounted_app="$mount_point/$APP_NAME.app"
    [[ -d "$mounted_app" ]] && [[ ! -L "$mounted_app" ]] || die "private DMG does not contain the expected application bundle"
    verify_removed_transfer_capability_absent "$mounted_app"
    hdiutil detach "$device"
    device=""
  )
}

validate_payload_manifest() {
  jq -e --arg os "$TARGET_OS" --arg arch "$TARGET_ARCH" --arg minimum "$MINIMUM_MACOS" '
    .schema_version == 1 and
    .target == {os: $os, arch: $arch, minimum_os: $minimum} and
    ([.payloads[] |
      (.name | type == "string" and length > 0) and
      (.version | type == "string" and length > 0) and
      (.url | test("^https://[^?#]+$")) and
      (.url | test("/(latest|main|master|head)(/|$)"; "i") | not) and
      ((.url | ascii_downcase) as $url |
       (.version | ascii_downcase | ltrimstr("v")) as $version |
       ($url | contains($version))) and
      (.sha256 | test("^[a-f0-9]{64}$")) and
      (.sha256 != ("0" * 64)) and
      (.sha256 != ("f" * 64)) and
      (.archive_type | IN("tar.xz", "tar.gz", "zip")) and
      (.source | type == "string" and length > 0) and
      (.destination | type == "string" and length > 0) and
      (.license | type == "string" and length > 0) and
      ((.name != "lyrebird") or
        ((.license_file | if type == "string" then (length > 0 and (startswith("/") | not) and (contains("..") | not)) else false end) and
         (.notice_file | if type == "string" then (length > 0 and (startswith("/") | not) and (contains("..") | not)) else false end) and
         (.pt_config_sha256 | if type == "string" then test("^[a-f0-9]{64}$") else false end)))
    ] | all) and
    ([.payloads[].name] | sort | unique) == ["lyrebird", "snowflake", "sparkle", "tor"]
  ' "$MANIFEST_PATH" >/dev/null || die "payload manifest is invalid or not fully pinned"
}

payload_field() {
  local name="$1"
  local field="$2"
  jq -er --arg name "$name" --arg field "$field" '.payloads[] | select(.name == $name) | .[$field]' "$MANIFEST_PATH"
}

verify_lyrebird_payload() {
  local bundle="$1"
  local lyrebird_binary pt_config
  local license_rel notice_rel license_file notice_file
  license_rel="$(payload_field lyrebird license_file)"
  notice_rel="$(payload_field lyrebird notice_file)"
  case "$license_rel" in /*|*..*) die "lyrebird notice/license file path is unsafe" ;; esac
  case "$notice_rel" in /*|*..*) die "lyrebird notice/license file path is unsafe" ;; esac
  lyrebird_binary="$(safe_regular_file_under_root "$bundle" "Contents/Resources/tor/pluggable_transports/lyrebird")"
  pt_config="$(safe_regular_file_under_root "$bundle" "Contents/Resources/tor/pluggable_transports/pt_config.json")"
  license_file="$(safe_regular_file_under_root "$bundle" "Contents/Resources/tor/pluggable_transports/$(basename "$license_rel")")"
  notice_file="$(safe_regular_file_under_root "$bundle" "Contents/Resources/tor/pluggable_transports/$(basename "$notice_rel")")"
  [[ -f "$lyrebird_binary" ]] && [[ ! -L "$lyrebird_binary" ]] && [[ -x "$lyrebird_binary" ]] ||
    die "bundled Lyrebird executable is missing or unsafe"
  file -b "$lyrebird_binary" | grep -Eq 'Mach-O .*arm64|Mach-O 64-bit executable arm64' ||
    die "bundled Lyrebird executable is not macOS arm64"
  require_arm64_macho "$lyrebird_binary"
  [[ -f "$pt_config" ]] && [[ ! -L "$pt_config" ]] ||
    die "frozen bridge configuration is missing or unsafe"
  verify_sha256 "$pt_config" "$(payload_field lyrebird pt_config_sha256)"
  [[ -f "$license_file" ]] && [[ ! -L "$license_file" ]] && [[ -s "$license_file" ]] ||
    die "lyrebird notice/license file is missing or unsafe"
  [[ -f "$notice_file" ]] && [[ ! -L "$notice_file" ]] && [[ -s "$notice_file" ]] ||
    die "lyrebird notice/license file is missing or unsafe"
}

verify_unseeded_public_bundle() {
  local bundle="$1"
  local forbidden_path

  [[ -d "$bundle" ]] && [[ ! -L "$bundle" ]] ||
    die "clean public bundle is missing or unsafe"
  forbidden_path="$(find "$bundle/Contents" -mindepth 1 \
    \( -iname 'state.tcs' -o -iname '*bootstrap*' -o -iname 'app.db' \
       -o -iname '*session*' -o -iname '*tdata*' -o -iname '*.tcomplicense' \
       -o -iname 'license.json' -o -iname 'license.key' -o -iname 'license.token' \
    \) -print -quit)"
  [[ -z "$forbidden_path" ]] ||
    die "clean public bundle contains seeded or private state: $forbidden_path"
  while IFS= read -r -d '' path; do
    if cmp -s <(printf 'TCSEED2\n') <(head -c 8 "$path"); then
      die "clean public bundle contains a TCSEED2 envelope"
    fi
  done < <(find "$bundle/Contents" -type f -print0)
}

verify_unseeded_update_archive() {
  local archive="$1"
  local scratch cleanup_prefix archive_status=0 extracted_app

  [[ -f "$archive" ]] && [[ ! -L "$archive" ]] ||
    die "update archive is missing or is not a regular file"
  scratch="$(mktemp -d "${TMPDIR:-/tmp}/tc-unseeded-update.XXXXXX")" ||
    die "cannot create unseeded update inspection directory"
  cleanup_prefix="${TMPDIR:-/tmp}/tc-unseeded-update."
  [[ "$scratch" == "$cleanup_prefix"* ]] || die "unsafe unseeded update inspection directory"
  chmod 700 "$scratch"
  (
    set -e
    ditto -x -k "$archive" "$scratch"
    [[ "$(find "$scratch" -mindepth 1 -maxdepth 1 -print | wc -l | tr -d '[:space:]')" == "1" ]] ||
      die "unseeded update archive contains unexpected top-level entries"
    extracted_app="$scratch/$APP_NAME.app"
    verify_unseeded_public_bundle "$extracted_app"
  ) || archive_status=$?
  rm -rf -- "$scratch"
  [[ "$archive_status" -eq 0 ]] || die "unseeded update archive verification failed"
}

require_public_distribution_signature() {
  local bundle="${1:-}" signature_details
  local authority_count team_count authority_team team_identifier

  [[ -n "$bundle" ]] || die "public release blocked: application bundle is missing"
  [[ -d "$bundle" && ! -L "$bundle" ]] ||
    die "public release blocked: application bundle is missing or unsafe"

  codesign --verify --strict --verbose=4 "$bundle" >/dev/null 2>&1 ||
    die "public release blocked: application signature verification failed"
  if ! signature_details="$(codesign -dv --verbose=4 "$bundle" 2>&1)"; then
    die "public release blocked: application signature could not be inspected"
  fi

  grep -Eiq '^Signature=adhoc$' <<<"$signature_details" &&
    die "public release blocked: ad-hoc application signature"

  authority_count="$(LC_ALL=C awk '/^Authority=Developer ID Application: / { count++ } END { print count + 0 }' <<<"$signature_details")"
  team_count="$(LC_ALL=C awk '/^TeamIdentifier=/ { count++ } END { print count + 0 }' <<<"$signature_details")"
  [[ "$authority_count" == "1" ]] ||
    die "public release blocked: stable Developer ID Application identity is missing"
  [[ "$team_count" == "1" ]] ||
    die "public release blocked: stable Apple team identifier is missing"

  authority_team="$(LC_ALL=C sed -nE 's/^Authority=Developer ID Application: .* \(([A-Z0-9]{10})\)$/\1/p' <<<"$signature_details")"
  team_identifier="$(LC_ALL=C sed -nE 's/^TeamIdentifier=([A-Z0-9]{10})$/\1/p' <<<"$signature_details")"
  [[ "$authority_team" =~ ^[A-Z0-9]{10}$ ]] ||
    die "public release blocked: stable Developer ID Application identity is malformed"
  [[ "$team_identifier" =~ ^[A-Z0-9]{10}$ ]] ||
    die "public release blocked: stable Apple team identifier is malformed"
  [[ "$authority_team" == "$team_identifier" ]] ||
    die "public release blocked: stable Apple signing identity is ambiguous"
}

verify_public_update_archive_signature() {
  local archive="$1"
  local scratch cleanup_prefix archive_status=0

  [[ -f "$archive" && ! -L "$archive" ]] ||
    die "public release blocked: update archive is missing or unsafe"
  scratch="$(mktemp -d "${TMPDIR:-/tmp}/tc-public-signature.XXXXXX")" ||
    die "public release blocked: cannot create signature inspection directory"
  cleanup_prefix="${TMPDIR:-/tmp}/tc-public-signature."
  [[ "$scratch" == "$cleanup_prefix"* ]] ||
    die "public release blocked: signature inspection directory is unsafe"
  chmod 700 "$scratch"

  (
    set -e
    ditto -x -k "$archive" "$scratch"
    [[ "$(find "$scratch" -mindepth 1 -maxdepth 1 -print | wc -l | tr -d '[:space:]')" == "1" ]] ||
      die "public release blocked: update archive contains unexpected top-level entries"
    require_public_distribution_signature "$scratch/$APP_NAME.app"
  ) || archive_status=$?

  rm -rf -- "$scratch"
  [[ "$archive_status" -eq 0 ]] ||
    die "public release blocked: update archive signature verification failed"
}

verify_sha256() {
  local file="$1"
  local expected="$2"
  local actual
  actual="$(shasum -a 256 "$file" | awk '{print $1}')"
  [[ "$actual" == "$expected" ]] || die "sha256 mismatch for $file"
}

verify_public_bootstrap_bundle() {
  local path="$1"
  local expected_sha256="${2:-}"

  [[ -f "$path" ]] && [[ ! -L "$path" ]] ||
    die "public bootstrap bundle is missing or is not a regular file: $path"
  cmp -s <(printf 'TCSEED2\n') <(LC_ALL=C head -c 8 "$path") ||
    die "public bootstrap bundle is not a TCSEED2 envelope: $path"
  [[ "$expected_sha256" =~ ^[a-f0-9]{64}$ ]] ||
    die "PUBLIC_BOOTSTRAP_BUNDLE_SHA256 must be 64 lowercase hexadecimal characters"
  verify_sha256 "$path" "$expected_sha256"
}

require_distinct_public_keys() {
  [[ "$LICENSE_PUBLIC_KEY" != "$SPARKLE_PUBLIC_ED_KEY" ]] || die "license and Sparkle public keys must be distinct"
  [[ "$LICENSE_PUBLIC_KEY" != "$REVOCATION_PUBLIC_KEY" ]] || die "license and revocation public keys must be distinct"
  [[ "$SPARKLE_PUBLIC_ED_KEY" != "$REVOCATION_PUBLIC_KEY" ]] || die "Sparkle and revocation public keys must be distinct"
}

require_raw_ed25519_public_key() {
  local name="$1"
  local value="${!name}"
  local decoded_length canonical
  decoded_length="$(printf '%s' "$value" | openssl base64 -d -A 2>/dev/null | wc -c | tr -d '[:space:]')"
  [[ "$decoded_length" == "32" ]] || die "$name must be standard base64 encoding of a raw 32-byte Ed25519 public key"
  canonical="$(printf '%s' "$value" | openssl base64 -d -A 2>/dev/null | openssl base64 -A)"
  [[ "$canonical" == "$value" ]] || die "$name must use canonical standard base64 encoding"
}

validate_revocation_build_metadata() {
  require_env "REVOCATION_MANIFEST_URL"
  require_env "REVOCATION_KEY_ID"
  require_env "REVOCATION_PUBLIC_KEY"
  require_env "LICENSE_PUBLIC_KEY"
  require_env "SPARKLE_PUBLIC_ED_KEY"
  [[ "$REVOCATION_MANIFEST_URL" == "$EXPECTED_REVOCATION_MANIFEST_URL" ]] ||
    die "REVOCATION_MANIFEST_URL must equal $EXPECTED_REVOCATION_MANIFEST_URL"
  [[ "$REVOCATION_KEY_ID" == "revocation-2026-01" ]] ||
    die "REVOCATION_KEY_ID must equal $EXPECTED_REVOCATION_KEY_ID"
  require_raw_ed25519_public_key "REVOCATION_PUBLIC_KEY"
  require_raw_ed25519_public_key "LICENSE_PUBLIC_KEY"
  require_raw_ed25519_public_key "SPARKLE_PUBLIC_ED_KEY"
  require_distinct_public_keys
  REVOCATION_BUILD_METADATA="TCREVBUILD1|$REVOCATION_MANIFEST_URL|$REVOCATION_KEY_ID|$REVOCATION_PUBLIC_KEY"
}

validate_build_metadata() {
  [[ "$RELEASE_VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] ||
    die "RELEASE_VERSION must be a numeric three-part version without a leading v"
  [[ "$APPCAST_URL" =~ ^https://[^[:space:]]+$ ]] || die "APPCAST_URL must be an HTTPS URL"
  [[ "$APPCAST_URL" != *"example.test"* ]] || die "APPCAST_URL must not use a test host"
  [[ "$BUILD_TAGS" == "desktop,public_macos_arm64" ]] ||
    die "BUILD_TAGS must be desktop,public_macos_arm64"
	validate_revocation_build_metadata
}

validate_source_version_alignment() {
  local version_source="$REPO_ROOT/frontend/src/version.ts"
  local package_source="$REPO_ROOT/frontend/package.json"
  local wails_source="$REPO_ROOT/wails.json"
  local frontend_version frontend_label package_version wails_version

  [[ -f "$version_source" ]] || die "frontend/src/version.ts is missing"
  [[ -f "$package_source" ]] || die "frontend/package.json is missing"
  [[ -f "$wails_source" ]] || die "wails.json is missing"

  frontend_version="$(sed -nE 's/^export const APP_VERSION = "([^"]+)";$/\1/p' "$version_source")"
  frontend_label="$(sed -nE 's/^export const APP_VERSION_LABEL = "([^"]+)";$/\1/p' "$version_source")"
  package_version="$(jq -er '.version' "$package_source")"
  wails_version="$(jq -er '.info.productVersion' "$wails_source")"

  if [[ "$frontend_version" != "$RELEASE_VERSION" ]] ||
     [[ "$frontend_label" != "v$RELEASE_VERSION" ]] ||
     [[ "$package_version" != "$RELEASE_VERSION" ]] ||
     [[ "$wails_version" != "$RELEASE_VERSION" ]]; then
    die "source version metadata does not match RELEASE_VERSION=$RELEASE_VERSION (frontend=$frontend_version label=$frontend_label package=$package_version wails=$wails_version)"
  fi
}

validate_publish_metadata() {
  [[ "${RELEASE_DOWNLOAD_BASE_URL:-}" =~ ^https://[^[:space:]]+$ ]] ||
    die "RELEASE_DOWNLOAD_BASE_URL must be an HTTPS URL"
  [[ "${RELEASE_REPOSITORY:-}" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]] ||
    die "RELEASE_REPOSITORY must use owner/repository format"
  [[ "${APPCAST_REPOSITORY:-}" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]] ||
    die "APPCAST_REPOSITORY must use owner/repository format"
  [[ "$APPCAST_BRANCH" =~ ^[A-Za-z0-9._/-]+$ ]] || die "APPCAST_BRANCH is invalid"
}

validate_release_metadata() {
  validate_build_metadata
  validate_publish_metadata
}
