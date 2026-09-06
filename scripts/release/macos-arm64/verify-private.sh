#!/usr/bin/env bash
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

PRIVATE_DMG_CHECKSUMS="${PRIVATE_DMG_CHECKSUMS:-$ARTIFACT_DIR/PRIVATE-DMG-SHA256SUMS}"

verify_private_nested_code() {
  while IFS= read -r -d '' bundle; do
    codesign --verify --strict --verbose=4 "$bundle"
  done < <(find "$APP_PATH/Contents" -depth -type d \
    \( -name '*.framework' -o -name '*.app' -o -name '*.xpc' \) -print0)

  while IFS= read -r -d '' executable; do
    is_macho_file "$executable" || continue
    codesign --verify --strict --verbose=4 "$executable"
  done < <(find "$APP_PATH/Contents" -type f -print0)
}

verify_clean_public_bundle() {
  local app="${1:-$APP_PATH}"
  local forbidden_path

  [[ -d "$app" ]] && [[ ! -L "$app" ]] ||
    die "clean public bundle is missing or unsafe"
  forbidden_path="$(find "$app/Contents" -mindepth 1 \
    \( -iname 'state.tcs' -o -iname '*bootstrap*' -o -iname '*license*' \
       -o -iname 'app.db' -o -iname '*session*' -o -iname '*tdata*' \) \
    -print -quit)"
  [[ -z "$forbidden_path" ]] ||
    die "clean public bundle contains forbidden state: $forbidden_path"
}

verify_clean_update_archive() {
  local archive="$1"
  local scratch cleanup_prefix archive_status=0

  [[ -f "$archive" ]] && [[ ! -L "$archive" ]] ||
    die "update archive is missing or is not a regular file"
  scratch="$(mktemp -d "${TMPDIR:-/tmp}/tc-clean-update.XXXXXX")" ||
    die "cannot create clean update inspection directory"
  cleanup_prefix="${TMPDIR:-/tmp}/tc-clean-update."
  [[ "$scratch" == "$cleanup_prefix"* ]] || die "unsafe clean update inspection directory"
  chmod 700 "$scratch"

  (
    set -e
    local extracted_app path
    ditto -x -k "$archive" "$scratch"
    [[ "$(find "$scratch" -mindepth 1 -maxdepth 1 -print | wc -l | tr -d '[:space:]')" == "1" ]] ||
      die "clean update archive contains unexpected top-level entries"
    extracted_app="$scratch/$APP_NAME.app"
    [[ -d "$extracted_app" ]] && [[ ! -L "$extracted_app" ]] ||
      die "clean update archive does not contain the expected application bundle"
    verify_clean_public_bundle "$extracted_app"
    while IFS= read -r -d '' path; do
      if cmp -s <(printf 'TCSEED2\n') <(head -c 8 "$path"); then
        die "clean update archive contains a TCSEED2 envelope"
      fi
    done < <(find "$extracted_app" -type f -print0)
  ) || archive_status=$?

  rm -rf -- "$scratch"
  [[ "$archive_status" -eq 0 ]] || die "clean update archive verification failed"
}

verify_seeded_public_bundle() {
  local canonical_directory="$APP_PATH/Contents/Resources/bootstrap-state"
  local canonical_seed="$canonical_directory/state.tcs"
  local forbidden_path child_count

  [[ "$STAGED_BUNDLE_PATH" == "$canonical_seed" ]] ||
    die "seeded public bootstrap path is not canonical"
  [[ -d "$canonical_directory" ]] && [[ ! -L "$canonical_directory" ]] ||
    die "seeded public bootstrap directory is missing or unsafe"
  child_count="$(find "$canonical_directory" -mindepth 1 -maxdepth 1 -print | wc -l | tr -d '[:space:]')"
  [[ "$child_count" == "1" ]] ||
    die "seeded public bundle contains forbidden state"
  verify_public_bootstrap_bundle "$canonical_seed" "$PUBLIC_BOOTSTRAP_BUNDLE_SHA256"

  forbidden_path="$(find "$APP_PATH/Contents" -mindepth 1 \
    \( -iname 'state.tcs' -o -iname '*bootstrap*' -o -iname '*license*' \
       -o -iname 'app.db' -o -iname '*session*' -o -iname '*tdata*' \) \
    ! -path "$canonical_directory" ! -path "$canonical_seed" -print -quit)"
  [[ -z "$forbidden_path" ]] ||
    die "seeded public bundle contains forbidden state: $forbidden_path"
}

verify_portable_bundle_modes() {
  local path mode expected_mode

  while IFS= read -r -d '' path; do
    if [[ -d "$path" ]]; then
      expected_mode="755"
    else
      expected_mode="644"
      if [[ -x "$path" ]] || is_macho_file "$path"; then
        expected_mode="755"
      fi
    fi
    mode="$(stat -f '%Lp' "$path")"
    [[ "$mode" == "$expected_mode" ]] ||
      die "non-portable bundle mode $mode (expected $expected_mode): $path"
  done < <(find "$APP_PATH" \( -type d -o -type f \) -print0)
}

verify_embedded_transport_versions() {
  local tor_binary="$APP_PATH/Contents/Resources/tor/tor"
  local snowflake_binary="$APP_PATH/Contents/Resources/bin/snowflake-client"
  local tor_version snowflake_version

  [[ -f "$tor_binary" ]] && [[ ! -L "$tor_binary" ]] && [[ -x "$tor_binary" ]] ||
    die "bundled Tor executable is missing or unsafe"
  [[ -f "$snowflake_binary" ]] && [[ ! -L "$snowflake_binary" ]] && [[ -x "$snowflake_binary" ]] ||
    die "bundled Snowflake executable is missing or unsafe"
  if ! tor_version="$("$tor_binary" --version 2>&1)"; then
    die "bundled Tor version command failed"
  fi
  grep -Eq '^Tor version [0-9]+\.' <<<"$tor_version" ||
    die "bundled Tor version command returned unexpected output"
  if ! snowflake_version="$("$snowflake_binary" -version 2>&1)"; then
    die "bundled Snowflake version command failed"
  fi
  grep -Eq '^snowflake-client [0-9]+\.[0-9]+\.[0-9]+' <<<"$snowflake_version" ||
    die "bundled Snowflake version command returned unexpected output"
}

main() {
  local signature_details

  require_macos_arm64_host
  validate_revocation_build_metadata
  [[ -d "$APP_PATH" ]] || die "application bundle is missing: $APP_PATH"
  [[ -f "$DMG_PATH" ]] || die "private disk image is missing: $DMG_PATH"
  [[ -f "$UPDATE_ZIP" ]] || die "private update archive is missing: $UPDATE_ZIP"
  [[ -f "$PRIVATE_DMG_CHECKSUMS" ]] || die "private DMG checksum is missing"
  if [[ -n "$PUBLIC_BOOTSTRAP_BUNDLE_SHA256" ]]; then
    verify_seeded_public_bundle
  else
    verify_clean_public_bundle
  fi
  codesign --verify --strict --verbose=4 "$APP_PATH"
  signature_details="$(codesign -dv --verbose=4 "$APP_PATH" 2>&1)"
  grep -Fq 'Signature=adhoc' <<<"$signature_details" ||
    die "application does not have the expected Signature=adhoc"
  verify_private_nested_code
  hdiutil verify "$DMG_PATH"
  require_arm64_bundle "$APP_PATH"
  verify_removed_transfer_capability_absent "$APP_PATH"
  verify_clean_update_archive "$UPDATE_ZIP"
  verify_removed_transfer_update_archive_absent "$UPDATE_ZIP"
  verify_removed_transfer_private_dmg_absent "$DMG_PATH"
  grep -aFq "$REVOCATION_BUILD_METADATA" "$APP_PATH/Contents/MacOS/$APP_EXECUTABLE" ||
    die "application is missing its linker-injected revocation metadata fingerprint"
  verify_portable_bundle_modes
  verify_embedded_transport_versions
  otool -L "$APP_PATH/Contents/MacOS/$APP_EXECUTABLE" |
    grep -Fq '@rpath/Sparkle.framework/Versions/B/Sparkle' || die "application is not linked to bundled Sparkle"
  [[ "$(/usr/libexec/PlistBuddy -c 'Print :LSMinimumSystemVersion' "$APP_PATH/Contents/Info.plist")" == "$MINIMUM_MACOS" ]] ||
    die "application minimum macOS version is wrong"
  [[ "$(/usr/libexec/PlistBuddy -c 'Print :SUFeedURL' "$APP_PATH/Contents/Info.plist")" == "$APPCAST_URL" ]] ||
    die "application appcast URL is wrong"
  [[ "$(/usr/libexec/PlistBuddy -c 'Print :SUPublicEDKey' "$APP_PATH/Contents/Info.plist")" == "$SPARKLE_PUBLIC_ED_KEY" ]] ||
    die "application Sparkle public key is wrong"
  (
    cd "$ARTIFACT_DIR"
    shasum -a 256 -c "$(basename "$PRIVATE_DMG_CHECKSUMS")"
  )

  if [[ -f "$APPCAST_PATH" ]]; then
    grep -Fq 'sparkle:edSignature' "$APPCAST_PATH" || die "appcast is missing an EdDSA signature"
    grep -Fq "$(basename "$UPDATE_ZIP")" "$APPCAST_PATH" || die "appcast does not reference the update archive"
    ! grep -Fq "$(basename "$DMG_PATH")" "$APPCAST_PATH" || die "appcast references the direct-delivery DMG"
    (
      cd "$ARTIFACT_DIR"
      shasum -a 256 -c SHA256SUMS
    )
  fi

  printf '%s\n' "private DMG verified; Gatekeeper approval remains a user action on first launch"
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  main "$@"
fi
