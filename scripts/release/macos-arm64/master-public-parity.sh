#!/usr/bin/env bash
set -euo pipefail

# Reuse the release bundle architecture checks when this helper is run from
# the release tree. Evidence is self-contained and contains identities only.
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

readonly PARITY_SCHEMA=1
readonly PT_CONFIG_RELATIVE_PATH="Contents/Resources/tor/pluggable_transports/pt_config.json"
PARITY_EVIDENCE_PARENT=""

fail() {
  printf '%s\n' "parity error: $1" >&2
  exit 1
}

usage() { fail "usage: write|verify"; }

require_parity_env() {
  require_env "PARITY_EVIDENCE_PATH"
  require_env "MASTER_APP_PATH"
  require_env "PUBLIC_APP_PATH"
  require_env "RELEASE_VERSION"
  require_env "SOURCE_SHA"
  require_env "SOURCE_ROOT"
  [[ "$RELEASE_VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || fail "release version is malformed"
  PARITY_EVIDENCE_PATH="$PARITY_EVIDENCE_PATH"
  MASTER_PUBLIC_PARITY_EVIDENCE_PATH="${MASTER_PUBLIC_PARITY_EVIDENCE_PATH:-$PARITY_EVIDENCE_PATH}"
  export MASTER_APP_PATH PARITY_EVIDENCE_PATH MASTER_PUBLIC_PARITY_EVIDENCE_PATH
}

resolve_source_sha() {
  local resolved
  [[ -d "$SOURCE_ROOT" ]] && [[ ! -L "$SOURCE_ROOT" ]] || fail "source root is unsafe"
  require_clean_source_tree
  resolved="$(git -C "$SOURCE_ROOT" rev-parse HEAD 2>/dev/null)" || fail "source revision is unavailable"
  [[ "$resolved" =~ ^[0-9a-f]{40}$ ]] || fail "source revision is malformed"
  [[ "$SOURCE_SHA" =~ ^[0-9a-f]{40}$ ]] || fail "source sha is malformed"
  [[ "$SOURCE_SHA" == "$resolved" ]] || fail "source sha mismatch"
  printf '%s\n' "$resolved"
}

read_bundle_version() {
  local plist="$1" key="$2" value
  [[ -f "$plist" ]] && [[ ! -L "$plist" ]] || fail "bundle metadata is unsafe"
  value="$(/usr/libexec/PlistBuddy -c "Print :$key" "$plist" 2>/dev/null)" || fail "bundle metadata is unavailable"
  [[ -n "$value" ]] || fail "bundle metadata is malformed"
  [[ "$value" != *[[:space:]=]* ]] || fail "bundle metadata is malformed"
  printf '%s\n' "$value"
}

require_app_bundle() {
  local bundle="$1"
  local contents="$bundle/Contents"
  local executable="$contents/MacOS/$APP_EXECUTABLE"
  local plist="$contents/Info.plist"
  local short_version build_version
  [[ -d "$bundle" ]] && [[ ! -L "$bundle" ]] || fail "application bundle is unsafe"
  [[ -d "$contents" ]] && [[ ! -L "$contents" ]] || fail "application bundle is unsafe"
  [[ -f "$executable" ]] && [[ ! -L "$executable" ]] || fail "application executable is missing"
  file -b "$executable" 2>/dev/null | grep -Eq '^Mach-O .*arm64' || fail "application executable is not arm64"
  require_arm64_bundle "$bundle"
  short_version="$(read_bundle_version "$plist" CFBundleShortVersionString)"
  build_version="$(read_bundle_version "$plist" CFBundleVersion)"
  [[ "$short_version" == "$RELEASE_VERSION" ]] || fail "bundle version mismatch"
  [[ "$build_version" == "$RELEASE_VERSION" ]] || fail "bundle build version mismatch"
}

bundle_sha256() {
  local bundle="$1" manifest_dir manifest_raw manifest_file manifest_sha path
  manifest_dir="$(mktemp -d "${TMPDIR:-/tmp}/telegram-companion-parity.XXXXXX")" || fail "cannot create hash workspace"
  chmod 700 "$manifest_dir"
  manifest_raw="$manifest_dir/raw"
  manifest_file="$manifest_dir/manifest"
  trap 'rm -f -- "${manifest_raw:-}" "${manifest_file:-}"; rmdir -- "${manifest_dir:-}" 2>/dev/null || true' EXIT HUP INT TERM
  (
    cd "$bundle" || exit 1
    find . -print | LC_ALL=C sort | while IFS= read -r path; do
      if [[ -L "$path" ]]; then
        printf 'L\t%s\t%s\n' "$path" "$(readlink "$path")"
      elif [[ -f "$path" ]]; then
        printf 'F\t%s\t%s\n' "$path" "$(shasum -a 256 -- "$path" | awk '{print $1}')"
      elif [[ -d "$path" ]]; then
        printf 'D\t%s\n' "$path"
      else
        fail "unsupported bundle entry"
      fi
    done
  ) >"$manifest_raw" || fail "bundle hash failed"
  LC_ALL=C sort "$manifest_raw" -o "$manifest_file" || fail "bundle hash failed"
  manifest_sha="$(shasum -a 256 -- "$manifest_file" | awk '{print $1}')" || fail "bundle hash failed"
  rm -f -- "$manifest_raw" "$manifest_file"
  rmdir -- "$manifest_dir" || fail "hash workspace cleanup failed"
  trap - EXIT HUP INT TERM
  [[ "$manifest_sha" =~ ^[0-9a-f]{64}$ ]] || fail "bundle hash is malformed"
  printf '%s\n' "$manifest_sha"
}

bundle_pt_config_sha256() {
  local bundle="$1" pt_config digest
  pt_config="$(safe_regular_file_under_root "$bundle" "$PT_CONFIG_RELATIVE_PATH")"
  [[ -f "$pt_config" ]] && [[ ! -L "$pt_config" ]] || fail "bridge configuration is missing or unsafe"
  digest="$(shasum -a 256 -- "$pt_config" | awk '{print $1}')" || fail "bridge configuration hash failed"
  [[ "$digest" =~ ^[0-9a-f]{64}$ ]] || fail "bridge configuration hash is malformed"
  printf '%s\n' "$digest"
}

require_canonical_evidence_path() {
  [[ "$PARITY_EVIDENCE_PATH" = /* ]] || fail "evidence path is unsafe"
  [[ "$PARITY_EVIDENCE_PATH" != *[[:cntrl:]]* ]] || fail "evidence path is unsafe"
  case "$PARITY_EVIDENCE_PATH" in
    *"//"*|*"/./"*|*"/../"*|*/.|*/..) fail "evidence path is unsafe" ;;
  esac
}

resolve_evidence_target() {
  local parent_dir evidence_name canonical_parent canonical_evidence
  require_canonical_evidence_path
  case "$PARITY_EVIDENCE_PATH" in
    */*) parent_dir="${PARITY_EVIDENCE_PATH%/*}"; evidence_name="${PARITY_EVIDENCE_PATH##*/}" ;;
    *) fail "evidence path is unsafe" ;;
  esac
  [[ -n "$parent_dir" && -n "$evidence_name" && "$evidence_name" != "." && "$evidence_name" != ".." ]] ||
    fail "evidence path is unsafe"
  [[ -L "$PARITY_EVIDENCE_PATH" ]] && fail "evidence path is a symlink"
  if [[ -e "$PARITY_EVIDENCE_PATH" && ! -f "$PARITY_EVIDENCE_PATH" ]]; then fail "evidence path is not a regular file"; fi
  [[ -d "$parent_dir" && ! -L "$parent_dir" ]] || fail "evidence directory is unsafe"
  canonical_parent="$(cd "$parent_dir" && pwd -P)" || fail "evidence directory cannot be resolved"
  [[ "$canonical_parent" = /* ]] || fail "evidence directory is unsafe"
  canonical_evidence="$canonical_parent/$evidence_name"
  [[ "$canonical_evidence" == "$canonical_parent/"* ]] || fail "evidence path escapes its directory"
  PARITY_EVIDENCE_PARENT="$canonical_parent"
  PARITY_EVIDENCE_PATH="$canonical_evidence"
  export PARITY_EVIDENCE_PARENT PARITY_EVIDENCE_PATH
}

prepare_evidence_target() {
  resolve_evidence_target
  chmod 700 "$PARITY_EVIDENCE_PARENT" || fail "evidence directory permissions failed"
}

write_parity_evidence() {
  local source_sha master_app_sha256 public_app_sha256 master_pt_config_sha256 public_pt_config_sha256 parent_dir tmp_file
  require_parity_env
  prepare_evidence_target
  source_sha="$(resolve_source_sha)"
  require_app_bundle "$MASTER_APP_PATH"
  require_app_bundle "$PUBLIC_APP_PATH"
  master_app_sha256="$(bundle_sha256 "$MASTER_APP_PATH")"
  public_app_sha256="$(bundle_sha256 "$PUBLIC_APP_PATH")"
  master_pt_config_sha256="$(bundle_pt_config_sha256 "$MASTER_APP_PATH")"
  public_pt_config_sha256="$(bundle_pt_config_sha256 "$PUBLIC_APP_PATH")"
  [[ "$master_pt_config_sha256" == "$public_pt_config_sha256" ]] || fail "bridge configuration mismatch"
  parent_dir="$PARITY_EVIDENCE_PARENT"
  tmp_file="$(mktemp "$parent_dir/.master-public-parity.XXXXXX")" || fail "cannot create evidence workspace"
  chmod 600 "$tmp_file" || fail "evidence permissions failed"
  trap 'rm -f -- "${tmp_file:-}"' EXIT HUP INT TERM
  {
    printf 'schema=%s\n' "$PARITY_SCHEMA"
    printf 'release_version=%s\n' "$RELEASE_VERSION"
    printf 'source_sha=%s\n' "$source_sha"
    printf 'master_app_sha256=%s\n' "$master_app_sha256"
    printf 'public_app_sha256=%s\n' "$public_app_sha256"
    printf 'pt_config_sha256=%s\n' "$master_pt_config_sha256"
    printf 'master_install=PASS\n'
  } >"$tmp_file" || fail "evidence write failed"
  mv -f "$tmp_file" "$PARITY_EVIDENCE_PATH" || fail "evidence commit failed"
  trap - EXIT HUP INT TERM
  chmod 600 "$PARITY_EVIDENCE_PATH" || fail "evidence permissions failed"
  printf '%s\n' PASS
}

verify_parity_evidence() {
  local source_sha master_app_sha256 public_app_sha256 master_pt_config_sha256 public_pt_config_sha256
  local schema release_version evidence_source_sha evidence_master_sha evidence_public_sha evidence_pt_config_sha256 master_install
  local seen_schema=0 seen_release=0 seen_source=0 seen_master=0 seen_public=0 seen_pt_config=0 seen_install=0 line key value
  require_parity_env
  resolve_evidence_target
  [[ -f "$PARITY_EVIDENCE_PATH" ]] && [[ ! -L "$PARITY_EVIDENCE_PATH" ]] || fail "evidence file is unsafe"
  [[ "$(stat -f '%Lp' "$PARITY_EVIDENCE_PATH" 2>/dev/null)" == "600" ]] || fail "evidence permissions are unsafe"
  source_sha="$(resolve_source_sha)"
  require_app_bundle "$MASTER_APP_PATH"
  require_app_bundle "$PUBLIC_APP_PATH"
  master_app_sha256="$(bundle_sha256 "$MASTER_APP_PATH")"
  public_app_sha256="$(bundle_sha256 "$PUBLIC_APP_PATH")"
  master_pt_config_sha256="$(bundle_pt_config_sha256 "$MASTER_APP_PATH")"
  public_pt_config_sha256="$(bundle_pt_config_sha256 "$PUBLIC_APP_PATH")"
  [[ "$master_pt_config_sha256" == "$public_pt_config_sha256" ]] || fail "bridge configuration mismatch"
  while IFS= read -r line || [[ -n "$line" ]]; do
    [[ -n "$line" ]] || fail "evidence file is malformed"
    case "$line" in *=*) ;; *) fail "evidence file is malformed" ;; esac
    key="${line%%=*}"; value="${line#*=}"
    [[ -n "$key" && -n "$value" ]] || fail "evidence file is malformed"
    case "$key" in
      schema)
        [[ "$seen_schema" -eq 0 ]] || fail "evidence file contains duplicate fields"
        [[ "$value" =~ ^1$ ]] || fail "evidence schema is malformed"
        seen_schema=1; schema="$value" ;;
      release_version)
        [[ "$seen_release" -eq 0 ]] || fail "evidence file contains duplicate fields"
        [[ "$value" != *[[:space:]=]* ]] || fail "evidence release is malformed"
        seen_release=1; release_version="$value" ;;
      source_sha)
        [[ "$seen_source" -eq 0 ]] || fail "evidence file contains duplicate fields"
        [[ "$value" =~ ^[0-9a-f]{40}$ ]] || fail "evidence source is malformed"
        seen_source=1; evidence_source_sha="$value" ;;
      master_app_sha256)
        [[ "$seen_master" -eq 0 ]] || fail "evidence file contains duplicate fields"
        [[ "$value" =~ ^[0-9a-f]{64}$ ]] || fail "evidence master hash is malformed"
        seen_master=1; evidence_master_sha="$value" ;;
      public_app_sha256)
        [[ "$seen_public" -eq 0 ]] || fail "evidence file contains duplicate fields"
        [[ "$value" =~ ^[0-9a-f]{64}$ ]] || fail "evidence public hash is malformed"
        seen_public=1; evidence_public_sha="$value" ;;
      pt_config_sha256)
        [[ "$seen_pt_config" -eq 0 ]] || fail "evidence file contains duplicate fields"
        [[ "$value" =~ ^[0-9a-f]{64}$ ]] || fail "evidence bridge configuration hash is malformed"
        seen_pt_config=1; evidence_pt_config_sha256="$value" ;;
      master_install)
        [[ "$seen_install" -eq 0 ]] || fail "evidence file contains duplicate fields"
        [[ "$value" == PASS ]] || fail "evidence install flag is malformed"
        seen_install=1; master_install="$value" ;;
      *) fail "evidence file contains unknown fields" ;;
    esac
  done <"$PARITY_EVIDENCE_PATH"
  [[ "$seen_schema" -eq 1 && "$seen_release" -eq 1 && "$seen_source" -eq 1 && "$seen_master" -eq 1 && "$seen_public" -eq 1 && "$seen_pt_config" -eq 1 && "$seen_install" -eq 1 ]] || fail "evidence file is incomplete"
  [[ "$schema" == "$PARITY_SCHEMA" ]] || fail "evidence schema mismatch"
  [[ "$release_version" == "$RELEASE_VERSION" ]] || fail "release version mismatch"
  [[ "$evidence_source_sha" == "$source_sha" ]] || fail "source sha mismatch"
  [[ "$evidence_master_sha" == "$master_app_sha256" ]] || fail "master bundle mismatch"
  [[ "$evidence_public_sha" == "$public_app_sha256" ]] || fail "public bundle mismatch"
  [[ "$evidence_pt_config_sha256" == "$master_pt_config_sha256" ]] || fail "bridge configuration evidence mismatch"
  [[ "$master_install" == PASS ]] || fail "master install flag mismatch"
  printf '%s\n' PASS
}

main() {
  case "${1:-}" in
    write) [[ "$#" -eq 1 ]] || usage; write_parity_evidence ;;
    verify) [[ "$#" -eq 1 ]] || usage; verify_parity_evidence ;;
    *) usage ;;
  esac
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  main "$@"
fi
