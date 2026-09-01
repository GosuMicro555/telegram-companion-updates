#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
app_path="${TELEGRAM_COMPANION_APP_PATH:-$repo_root/build/bin/telegram-companion.app}"
runtime_bundle="${TELEGRAM_COMPANION_RUNTIME_BUNDLE:-}"
resources="$app_path/Contents/Resources"
plist="$app_path/Contents/Info.plist"
manifest="$repo_root/resources/macos-arm64/payload-manifest.json"
runtime_source="$runtime_bundle/Contents/Resources/tor"

fail() {
  printf 'macOS runtime packaging failed: %s\n' "$1" >&2
  exit 1
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || fail "required command is unavailable"
}

require_regular_file() {
  [[ -f "$1" && ! -L "$1" ]] || fail "required regular file is missing or unsafe"
}

require_directory() {
  [[ -d "$1" && ! -L "$1" ]] || fail "required directory is missing or unsafe"
}

require_no_symlink_ancestors() {
  local path="$1" current
  [[ "$path" = /* ]] || fail "managed runtime path must be absolute"
  current="$path"
  while [[ "$current" != "/" ]]; do
    [[ ! -L "$current" ]] || fail "managed runtime path contains a symlink ancestor"
    current="$(dirname "$current")" || fail "managed runtime ancestor is unavailable"
  done
}

require_disjoint_directories() {
  local left="$1" right="$2" left_real right_real
  left_real="$(cd "$left" && pwd -P)" || fail "runtime source path is unavailable"
  right_real="$(cd "$right" && pwd -P)" || fail "runtime destination path is unavailable"
  case "$left_real/" in
    "$right_real/"*) fail "runtime source overlaps the application destination" ;;
  esac
  case "$right_real/" in
    "$left_real/"*) fail "application destination overlaps the runtime source" ;;
  esac
}

require_arm64() {
  local architectures
  architectures="$(lipo -archs "$1" 2>/dev/null)" || fail "Mach-O architecture is unavailable"
  [[ "$architectures" == "arm64" ]] || fail "runtime Mach-O is not arm64-only"
}

runtime_tree_inventory_digest() {
  local root="$1"
  (
    cd "$root"
    find . -print | LC_ALL=C sort | while IFS= read -r relative; do
      if [[ -d "$relative" && ! -L "$relative" ]]; then
        printf 'D\t%s\t%s\n' "$relative" "$(stat -f '%Lp' "$relative")"
      elif [[ -f "$relative" && ! -L "$relative" ]]; then
        printf 'F\t%s\t%s\t%s\n' "$relative" "$(stat -f '%Lp' "$relative")" \
          "$(shasum -a 256 -- "$relative" | awk 'NR == 1 { print $1 }')"
      else
        exit 1
      fi
    done
  ) | shasum -a 256 | awk 'NR == 1 { print $1 }'
}

validate_runtime_tree() {
  local root="$1"
  local tor_binary="$root/tor"
  local lyrebird_binary="$root/pluggable_transports/lyrebird"
  local config="$root/pluggable_transports/pt_config.json"
  local expected_pt_config_sha256 actual_pt_config_sha256 nested unexpected_entry

  require_directory "$root"
  unexpected_entry="$(find "$root" -mindepth 1 ! \( -type f -o -type d \) -print -quit)"
  [[ -z "$unexpected_entry" ]] || fail "runtime tree contains a non-regular entry"
  while IFS= read -r -d '' nested; do
    [[ "$nested" != *$'\n'* && "$nested" != *$'\r'* && "$nested" != *$'\t'* ]] ||
      fail "runtime tree contains an unsafe path"
  done < <(find "$root" -print0)

  require_regular_file "$tor_binary"
  require_regular_file "$lyrebird_binary"
  require_regular_file "$config"
  [[ -x "$tor_binary" && -x "$lyrebird_binary" ]] || fail "runtime executable mode is invalid"
  require_arm64 "$tor_binary"
  require_arm64 "$lyrebird_binary"

  expected_pt_config_sha256="$(jq -er '.payloads[] | select(.name == "lyrebird") | .pt_config_sha256' "$manifest")" ||
    fail "pinned bridge configuration digest is unavailable"
  [[ "$expected_pt_config_sha256" =~ ^[0-9a-f]{64}$ ]] ||
    fail "pinned bridge configuration digest is malformed"
  actual_pt_config_sha256="$(shasum -a 256 -- "$config" | awk 'NR == 1 { print $1 }')"
  [[ "$actual_pt_config_sha256" == "$expected_pt_config_sha256" ]] ||
    fail "bridge configuration digest does not match the pinned manifest"

  while IFS= read -r -d '' nested; do
    if file -b "$nested" | grep -q '^Mach-O'; then
      require_arm64 "$nested"
    fi
  done < <(find "$root" -type f -print0)

  "$tor_binary" --version >/dev/null 2>&1 || fail "bundled Tor dependency closure is invalid"
  "$lyrebird_binary" --version >/dev/null 2>&1 || fail "bundled Lyrebird executable is invalid"
}

sign_and_verify_nested_macho() {
  local root="$1" nested
  while IFS= read -r -d '' nested; do
    if file -b "$nested" | grep -q '^Mach-O'; then
      codesign --force --sign - --timestamp=none "$nested"
    fi
  done < <(find "$root" -type f -print0)
  while IFS= read -r -d '' nested; do
    if file -b "$nested" | grep -q '^Mach-O'; then
      codesign --verify --strict "$nested"
    fi
  done < <(find "$root" -type f -print0)
}

main() {
  local command_name version source_runtime_digest staged_runtime_digest

  for command_name in awk codesign dirname ditto file find grep jq lipo node plutil shasum sort stat; do
    require_command "$command_name"
  done
  [[ -n "$runtime_bundle" && "$runtime_bundle" = /* ]] ||
    fail "TELEGRAM_COMPANION_RUNTIME_BUNDLE is required and must be absolute"
  require_no_symlink_ancestors "$app_path"
  require_no_symlink_ancestors "$runtime_bundle"
  require_directory "$app_path"
  require_directory "$app_path/Contents"
  require_directory "$resources"
  require_directory "$runtime_bundle"
  require_directory "$runtime_bundle/Contents"
  require_directory "$runtime_bundle/Contents/Resources"
  require_regular_file "$plist"
  require_regular_file "$repo_root/frontend/package.json"
  require_regular_file "$manifest"
  validate_runtime_tree "$runtime_source"

  version="$(node -p "JSON.parse(require('fs').readFileSync('$repo_root/frontend/package.json', 'utf8')).version")"
  [[ -n "$version" ]] || fail "application version is empty"

  source_runtime_digest="$(runtime_tree_inventory_digest "$runtime_source")"
  [[ "$source_runtime_digest" =~ ^[0-9a-f]{64}$ ]] || fail "source runtime inventory digest is invalid"
  require_no_symlink_ancestors "$resources"
  require_no_symlink_ancestors "$runtime_source"
  require_disjoint_directories "$runtime_source" "$resources"
  rm -rf -- "$resources/tor" "$resources/bin"
  mkdir -p "$resources"
  ditto "$runtime_source" "$resources/tor"
  validate_runtime_tree "$resources/tor"
  staged_runtime_digest="$(runtime_tree_inventory_digest "$resources/tor")"
  [[ "$source_runtime_digest" == "$staged_runtime_digest" ]] ||
    fail "staged runtime differs from the exact public runtime before signing"

  plutil -replace CFBundleShortVersionString -string "$version" "$plist"
  plutil -replace CFBundleVersion -string "$version" "$plist"
  sign_and_verify_nested_macho "$resources/tor"
  codesign --force --sign - --timestamp=none "$app_path"
  codesign --verify --deep --strict "$app_path"

  printf 'Packaged Telegram Companion %s for macOS arm64: %s\n' "$version" "$app_path"
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  main "$@"
fi
