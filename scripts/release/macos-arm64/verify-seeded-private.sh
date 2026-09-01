#!/usr/bin/env bash
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

readonly EXPECTED_VERSION="0.8.2"
SEEDED_DMG_PATH="${1:-${SEEDED_DMG_PATH:-$ARTIFACT_DIR/Telegram-Companion-$EXPECTED_VERSION-arm64-Public-Seeded-PRIVATE.dmg}}"
SEEDED_DMG_SHA256_PATH="${SEEDED_DMG_SHA256_PATH:-$SEEDED_DMG_PATH.sha256}"
readonly SEEDED_DMG_PATH
readonly SEEDED_DMG_SHA256_PATH

WORK_DIR=""
MOUNT_POINT=""
ATTACHED_DEVICE=""

cleanup() {
  local exit_code=$? can_remove_work_dir
  trap - EXIT
  set +e
  can_remove_work_dir=1
  if [[ -n "$ATTACHED_DEVICE" ]]; then
    if ! hdiutil detach "$ATTACHED_DEVICE" >/dev/null 2>&1; then
      can_remove_work_dir=0
      exit_code=1
    fi
  elif [[ -n "$MOUNT_POINT" ]] && mount | grep -Fq " on $MOUNT_POINT "; then
    if ! hdiutil detach "$MOUNT_POINT" >/dev/null 2>&1; then
      can_remove_work_dir=0
      exit_code=1
    fi
  fi
  if [[ "$can_remove_work_dir" -eq 1 ]] && [[ -n "$WORK_DIR" ]] && [[ -d "$WORK_DIR" ]]; then
    rm -rf -- "$WORK_DIR"
  fi
  exit "$exit_code"
}

trap cleanup EXIT
trap 'exit 1' HUP INT TERM

verify_checksum_sidecar() {
  local checksum_line_count checksum_line sidecar_name

  [[ -f "$SEEDED_DMG_SHA256_PATH" ]] && [[ ! -L "$SEEDED_DMG_SHA256_PATH" ]] ||
    die "seeded DMG SHA-256 sidecar is missing or is not a regular file: $SEEDED_DMG_SHA256_PATH"
  checksum_line_count="$(awk 'NF { count++ } END { print count + 0 }' "$SEEDED_DMG_SHA256_PATH")"
  [[ "$checksum_line_count" -eq 1 ]] || die "seeded DMG SHA-256 sidecar must contain exactly one checksum"
  checksum_line="$(awk 'NF { print; exit }' "$SEEDED_DMG_SHA256_PATH")"
  [[ "$checksum_line" =~ ^[[:xdigit:]]{64}[[:space:]]+\*?(.+)$ ]] ||
    die "seeded DMG SHA-256 sidecar has an invalid format"
  sidecar_name="${BASH_REMATCH[1]}"
  [[ "$sidecar_name" == "$(basename "$SEEDED_DMG_PATH")" ]] ||
    die "seeded DMG SHA-256 sidecar references an unexpected file: $sidecar_name"

  (
    cd "$(dirname "$SEEDED_DMG_PATH")"
    shasum -a 256 -c "$SEEDED_DMG_SHA256_PATH"
  )
}

verify_nested_code() {
  local bundle executable

  while IFS= read -r -d '' bundle; do
    codesign --verify --strict --verbose=4 "$bundle"
  done < <(find "$MOUNTED_APP_PATH/Contents" -depth -type d \
    \( -name '*.framework' -o -name '*.app' -o -name '*.xpc' \) -print0)

  while IFS= read -r -d '' executable; do
    codesign --verify --strict --verbose=4 "$executable"
  done < <(find "$MOUNTED_APP_PATH/Contents" -type f \( -perm -111 -o -name '*.dylib' \) -print0)
}

verify_bundle_identity() {
  local plist display_name executable identifier bundle_name package_type high_resolution

  plist="$MOUNTED_APP_PATH/Contents/Info.plist"
  [[ -f "$plist" ]] && [[ ! -L "$plist" ]] || die "application Info.plist is missing or is not a regular file"
  plutil -lint "$plist" >/dev/null || die "application Info.plist is invalid"

  display_name="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleDisplayName' "$plist")"
  executable="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleExecutable' "$plist")"
  identifier="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleIdentifier' "$plist")"
  bundle_name="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleName' "$plist")"
  package_type="$(/usr/libexec/PlistBuddy -c 'Print :CFBundlePackageType' "$plist")"
  high_resolution="$(/usr/libexec/PlistBuddy -c 'Print :NSHighResolutionCapable' "$plist")"

  [[ "$display_name" == "Telegram Companion" ]] || die "CFBundleDisplayName is unexpected: $display_name"
  [[ "$executable" == "telegram-companion" ]] || die "CFBundleExecutable is unexpected: $executable"
  [[ "$identifier" == "com.telegramcompanion.desktop" ]] || die "CFBundleIdentifier is unexpected: $identifier"
  [[ "$bundle_name" == "Telegram Companion" ]] || die "CFBundleName is unexpected: $bundle_name"
  [[ "$package_type" == "APPL" ]] || die "CFBundlePackageType is unexpected: $package_type"
  [[ "$high_resolution" == "true" ]] || die "NSHighResolutionCapable must be true"
  [[ -x "$MOUNTED_APP_PATH/Contents/MacOS/$executable" ]] || die "declared application executable is missing or is not executable"
}

verify_bootstrap_state() {
  local expected_state_path state_count state_path magic

  expected_state_path="$MOUNTED_APP_PATH/Contents/Resources/bootstrap-state/state.tcs"
  state_count=0
  state_path=""
  while IFS= read -r -d '' candidate; do
    state_count=$((state_count + 1))
    state_path="$candidate"
  done < <(find "$MOUNTED_APP_PATH" -type f -path '*/bootstrap-state/state.tcs' -print0)

  [[ "$state_count" -eq 1 ]] || die "application must contain exactly one bootstrap-state/state.tcs, found $state_count"
  [[ "$state_path" == "$expected_state_path" ]] || die "bootstrap state is stored at an unexpected path: $state_path"
  [[ -f "$state_path" ]] && [[ ! -L "$state_path" ]] || die "bootstrap state is not a regular file"
  [[ "$(stat -f '%z' "$state_path")" -gt 7 ]] || die "bootstrap state envelope is empty"
  magic="$(LC_ALL=C head -c 7 "$state_path")"
  [[ "$magic" == "TCSEED1" ]] || die "bootstrap state is not an encrypted TCSEED1 envelope"
}

verify_no_plaintext_state() {
  local plaintext_path

  plaintext_path=""
  while IFS= read -r -d '' candidate; do
    plaintext_path="$candidate"
    break
  done < <(find "$MOUNTED_APP_PATH" \( \
    \( -type d \( -iname 'tdata' -o -iname 'session' -o -iname 'sessions' \) \) -o \
    \( -type f \( \
      -iname 'app.db' -o -iname 'app.db-*' -o \
      -iname '*.session' -o -iname '*.session-journal' -o \
      -iname '*.tcomplicense' -o -iname 'license.json' -o \
      -iname 'license.key' -o -iname 'license.token' \
    \) \) \) -print0)
  [[ -z "$plaintext_path" ]] || die "plaintext application state is present in the DMG: $plaintext_path"

  if grep -R -I -l -F 'TCPLIC1.' "$MOUNTED_APP_PATH" >/dev/null 2>&1; then
    die "plaintext license token is present in the DMG"
  fi
}

verify_portable_permissions() {
  local path mode expected_mode

  while IFS= read -r -d '' path; do
    mode="$(stat -f '%Lp' "$path")"
    [[ "$mode" == "755" ]] || die "directory does not have portable mode 755: $path ($mode)"
  done < <(find "$MOUNTED_APP_PATH" -type d -print0)

  while IFS= read -r -d '' path; do
    mode="$(stat -f '%Lp' "$path")"
    expected_mode="644"
    if [[ -x "$path" ]]; then
      expected_mode="755"
    fi
    [[ "$mode" == "$expected_mode" ]] ||
      die "file does not have portable mode $expected_mode: $path ($mode)"
  done < <(find "$MOUNTED_APP_PATH" -type f -print0)
}

main() {
  local attach_output mount_details app_count app_path short_version bundle_version

  require_macos_arm64_host
  for command in awk codesign find grep head hdiutil lipo mount plutil shasum stat mktemp; do
    require_command "$command"
  done
  [[ -x /usr/libexec/PlistBuddy ]] || die "PlistBuddy is unavailable"
  [[ -f "$SEEDED_DMG_PATH" ]] && [[ ! -L "$SEEDED_DMG_PATH" ]] ||
    die "seeded private DMG is missing or is not a regular file: $SEEDED_DMG_PATH"

  verify_checksum_sidecar
  hdiutil verify "$SEEDED_DMG_PATH"

  WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/telegram-companion-seeded-verify.XXXXXX")"
  MOUNT_POINT="$WORK_DIR/mount"
  mkdir "$MOUNT_POINT"
  MOUNT_POINT="$(cd "$MOUNT_POINT" && pwd -P)"
  attach_output="$(hdiutil attach -readonly -nobrowse -noautoopen -mountpoint "$MOUNT_POINT" "$SEEDED_DMG_PATH")"
  ATTACHED_DEVICE="$(awk '$1 ~ /^\/dev\// { device=$1 } END { print device }' <<<"$attach_output")"
  [[ -n "$ATTACHED_DEVICE" ]] || die "could not identify the read-only DMG device"
  mount_details="$(mount | grep -F " on $MOUNT_POINT " || true)"
  [[ -n "$mount_details" ]] || die "seeded DMG is not mounted at the expected path"
  grep -Eq '\([^)]*(read-only|rdonly)[^)]*\)' <<<"$mount_details" || die "seeded DMG mount is not read-only"

  app_count=0
  app_path=""
  shopt -s nullglob
  for candidate in "$MOUNT_POINT"/*.app; do
    [[ -d "$candidate" ]] || continue
    app_count=$((app_count + 1))
    app_path="$candidate"
  done
  shopt -u nullglob
  [[ "$app_count" -eq 1 ]] || die "seeded DMG must contain exactly one application bundle, found $app_count"
  [[ "$(basename "$app_path")" == "$APP_NAME.app" ]] || die "seeded DMG contains an unexpected application bundle"
  readonly MOUNTED_APP_PATH="$app_path"

  short_version="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' "$MOUNTED_APP_PATH/Contents/Info.plist")"
  bundle_version="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleVersion' "$MOUNTED_APP_PATH/Contents/Info.plist")"
  [[ "$short_version" == "$EXPECTED_VERSION" ]] || die "CFBundleShortVersionString is $short_version, expected $EXPECTED_VERSION"
  [[ "$bundle_version" == "$EXPECTED_VERSION" ]] || die "CFBundleVersion is $bundle_version, expected $EXPECTED_VERSION"

  verify_bundle_identity
  codesign --verify --strict --verbose=4 "$MOUNTED_APP_PATH"
  verify_nested_code
  require_arm64_bundle "$MOUNTED_APP_PATH"
  verify_bootstrap_state
  verify_no_plaintext_state
  verify_portable_permissions

  printf '%s\n' "seeded private DMG verified: version $EXPECTED_VERSION, arm64, signed, encrypted, and portable"
}

main "$@"
