#!/usr/bin/env bash
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

readonly INSTALL_PATH="/Applications/Telegram Companion.app"

PUBLIC_VERSION="${PUBLIC_VERSION:-}"
HOST_DOWNLOADS="${HOST_DOWNLOADS:-}"
MANUAL_UNSEEDED_ONLY="${MANUAL_UNSEEDED_ONLY:-0}"
[[ "$MANUAL_UNSEEDED_ONLY" == "0" || "$MANUAL_UNSEEDED_ONLY" == "1" ]] ||
  die "MANUAL_UNSEEDED_ONLY must be 0 or 1"
if [[ "$MANUAL_UNSEEDED_ONLY" == "0" ]]; then
  PUBLIC_BOOTSTRAP_BUNDLE_PATH="${PUBLIC_BOOTSTRAP_BUNDLE_PATH:-}"
  PUBLIC_BOOTSTRAP_BUNDLE_SHA256="${PUBLIC_BOOTSTRAP_BUNDLE_SHA256:-}"
else
  PUBLIC_BOOTSTRAP_BUNDLE_PATH=""
  PUBLIC_BOOTSTRAP_BUNDLE_SHA256=""
fi
UTM_GUEST_SSH="${UTM_GUEST_SSH:-}"
WAILS_BIN="${WAILS_BIN:-$HOME/go/bin/wails}"
PERSISTENT_PAYLOAD_CACHE="${PERSISTENT_PAYLOAD_CACHE:-$HOME/.cache/telegram-companion-release/payloads}"
PARITY_CACHE_ROOT="${PARITY_CACHE_ROOT:-${MASTER_PUBLIC_PARITY_ROOT:-$HOME/.cache/telegram-companion-release/master-public-parity}}"

WORK_DIR=""
INSTALL_WORK_DIR=""
INSTALL_STAGE_PATH=""
INSTALL_BACKUP_PATH=""
INSTALL_BACKUP_ID=""
INSTALL_INSTALLED_ID=""
INSTALL_STAGE_ID=""
INSTALL_REPLACED=0
INSTALL_COMMITTED=0
INSTALL_WORK_RETAIN=0
SENSITIVE_LOG=""
PUBLIC_RELEASE_ROOT=""
PUBLIC_APP_PATH=""
PRIVATE_APP_PATH=""
PUBLIC_ARTIFACT_DIR=""
SOURCE_SHA=""
source_sha_before_master=""
source_sha_after_master=""
source_sha_after_public=""
PARITY_STAGING_ROOT=""
PARITY_VERSION_ROOT=""
PARITY_FINAL_ROOT=""
PARITY_CURRENT_POINTER=""
PARITY_POINTER_TMP=""
PARITY_PREVIOUS_POINTER_TARGET=""
PARITY_PREVIOUS_POINTER_PRESENT=0
PARITY_EVIDENCE_PATH=""
MASTER_APP_PATH=""
PUBLIC_PARITY_APP_PATH=""
PARITY_MOVED=0
PARITY_PROMOTED=0
PARITY_LOCK_DIR=""
PARITY_LOCK_ACQUIRED=0
PARITY_LOCK_ID=""
PARITY_STAGING_ID=""
PARITY_FINAL_ID=""
PARITY_POINTER_ID=""
PARITY_CURRENT_POINTER_ID=""

restore_installed_app() {
  local backup_present=0 install_identity=""
  [[ "$INSTALL_REPLACED" == "1" ]] || return 0

  # Validate the recovery copy before touching the currently installed app.
  # If the rename of the old app never completed, leave that unchanged path
  # alone; if either identity is ambiguous, retain the private work directory
  # for an operator rather than deleting a potentially recoverable backup.
  if [[ -n "$INSTALL_BACKUP_ID" ]]; then
    if [[ -e "$INSTALL_BACKUP_PATH" || -L "$INSTALL_BACKUP_PATH" ]]; then
      [[ -d "$INSTALL_BACKUP_PATH" && ! -L "$INSTALL_BACKUP_PATH" ]] || {
        INSTALL_WORK_RETAIN=1
        printf '%s\n' "release error: master backup is no longer a directory" >&2
        return 1
      }
      [[ "$(parity_identity "$INSTALL_BACKUP_PATH")" == "$INSTALL_BACKUP_ID" ]] || {
        INSTALL_WORK_RETAIN=1
        printf '%s\n' "release error: master backup identity changed; refusing rollback" >&2
        return 1
      }
      backup_present=1
    elif [[ -e "$INSTALL_PATH" || -L "$INSTALL_PATH" ]] &&
      [[ -d "$INSTALL_PATH" && ! -L "$INSTALL_PATH" ]] &&
      [[ "$(parity_identity "$INSTALL_PATH")" == "$INSTALL_BACKUP_ID" ]]; then
      # The original app is still in place because the rename did not commit.
      INSTALL_REPLACED=0
      INSTALL_BACKUP_ID=""
      INSTALL_INSTALLED_ID=""
      return 0
    else
      INSTALL_WORK_RETAIN=1
      printf '%s\n' "release error: master backup disappeared; refusing rollback" >&2
      return 1
    fi
  fi

  if [[ -e "$INSTALL_PATH" || -L "$INSTALL_PATH" ]]; then
    [[ -d "$INSTALL_PATH" && ! -L "$INSTALL_PATH" ]] || {
      INSTALL_WORK_RETAIN=1
      printf '%s\n' "release error: installed master path is no longer a directory" >&2
      return 1
    }
    install_identity="$(parity_identity "$INSTALL_PATH")"
    if [[ "$backup_present" == "0" && -n "$INSTALL_BACKUP_ID" &&
      "$install_identity" == "$INSTALL_BACKUP_ID" ]]; then
      # As above, the original app is still the live path.
      INSTALL_REPLACED=0
      INSTALL_BACKUP_ID=""
      INSTALL_INSTALLED_ID=""
      return 0
    fi
    [[ -n "$INSTALL_INSTALLED_ID" && "$install_identity" == "$INSTALL_INSTALLED_ID" ]] || {
      INSTALL_WORK_RETAIN=1
      printf '%s\n' "release error: installed master identity changed; refusing rollback" >&2
      return 1
    }
  elif [[ -n "$INSTALL_INSTALLED_ID" && "$backup_present" == "0" ]]; then
    INSTALL_WORK_RETAIN=1
    printf '%s\n' "release error: installed master disappeared without a verified backup" >&2
    return 1
  fi
  if [[ -n "$INSTALL_INSTALLED_ID" ]] && [[ -e "$INSTALL_PATH" || -L "$INSTALL_PATH" ]]; then
    rm -rf -- "$INSTALL_PATH"
  fi
  if [[ "$backup_present" == "1" ]]; then
    mv "$INSTALL_BACKUP_PATH" "$INSTALL_PATH"
    [[ "$(parity_identity "$INSTALL_PATH")" == "$INSTALL_BACKUP_ID" ]] || {
      INSTALL_WORK_RETAIN=1
      printf '%s\n' "release error: restored master identity changed" >&2
      return 1
    }
  fi
  INSTALL_REPLACED=0
  INSTALL_BACKUP_ID=""
  INSTALL_INSTALLED_ID=""
  INSTALL_STAGE_ID=""
}

parity_identity() {
  stat -f '%d:%i' "$1" 2>/dev/null
}

require_private_parity_path() {
  local path="$1" current="$1"
  [[ "$path" = /* ]] || die "parity path must be absolute"
  while [[ "$current" != "/" && "$current" != "." ]]; do
    [[ ! -L "$current" ]] || die "parity path ancestor is a symlink"
    current="$(dirname "$current")"
  done
  if [[ -e "$path" || -L "$path" ]]; then
    [[ -d "$path" && ! -L "$path" ]] || die "parity path is not a private directory"
  else
    mkdir "$path" || die "cannot create private parity directory"
  fi
}

cleanup() {
  local status=$? restore_pointer_tmp="" pointer_removed=0 current_pointer_id="" preserve_final=0 restore_failed=0
  trap - EXIT
  if [[ "$status" -ne 0 ]] && [[ "$INSTALL_COMMITTED" != "1" ]]; then
    restore_installed_app || status=1
  fi
  if [[ -n "$WORK_DIR" ]] && [[ -d "$WORK_DIR" ]]; then
    rm -rf -- "$WORK_DIR"
  fi
  if [[ -n "$INSTALL_WORK_DIR" ]] && [[ -d "$INSTALL_WORK_DIR" ]] &&
    [[ "$INSTALL_WORK_RETAIN" != "1" ]]; then
    rm -rf -- "$INSTALL_WORK_DIR"
  fi
  if [[ "$status" -ne 0 ]]; then
    if [[ -n "$PARITY_POINTER_TMP" ]] &&
      [[ -e "$PARITY_POINTER_TMP" || -L "$PARITY_POINTER_TMP" ]]; then
      rm -f -- "$PARITY_POINTER_TMP" || status=1
    fi
    if [[ "$PARITY_PROMOTED" == "1" ]]; then
      # Remove the new pointer only when it still points to this run's exact
      # snapshot and has the identity recorded by this run; never overwrite
      # an unrelated operator change.
      if [[ -L "$PARITY_CURRENT_POINTER" ]] &&
        [[ "$(readlink "$PARITY_CURRENT_POINTER")" == "$PARITY_FINAL_ROOT" ]]; then
        current_pointer_id="$(parity_identity "$PARITY_CURRENT_POINTER")"
        if [[ -n "$PARITY_POINTER_ID" && "$current_pointer_id" == "$PARITY_POINTER_ID" ]]; then
          rm -f -- "$PARITY_CURRENT_POINTER" || status=1
          pointer_removed=1
        else
          # The pointer was replaced, but its identity is no longer ours. Keep
          # the exact final snapshot so the live pointer cannot be left
          # dangling; an operator can reconcile the ambiguous state safely.
          status=1
          preserve_final=1
        fi
      fi
      if [[ "$pointer_removed" == "1" && "$PARITY_PREVIOUS_POINTER_PRESENT" == "1" ]]; then
        preserve_final=1
        if [[ -e "$PARITY_CURRENT_POINTER" || -L "$PARITY_CURRENT_POINTER" ]]; then
          restore_failed=1
        fi
        if [[ "$restore_failed" -eq 0 ]]; then
          restore_pointer_tmp="$(mktemp "$(dirname "$PARITY_CURRENT_POINTER")/.restore.XXXXXX")" || restore_failed=1
        fi
        if [[ "$restore_failed" -eq 0 ]]; then
          rm -f -- "$restore_pointer_tmp"
          ln -s "$PARITY_PREVIOUS_POINTER_TARGET" "$restore_pointer_tmp" || restore_failed=1
          if [[ -e "$PARITY_CURRENT_POINTER" || -L "$PARITY_CURRENT_POINTER" ]]; then
            restore_failed=1
          fi
          if [[ "$restore_failed" -eq 0 ]]; then
            mv -fh "$restore_pointer_tmp" "$PARITY_CURRENT_POINTER" || restore_failed=1
          fi
        fi
        if [[ "$restore_failed" -eq 0 ]]; then
          preserve_final=0
        else
          status=1
        fi
      fi
      if [[ "$preserve_final" == "0" ]] &&
        [[ -d "$PARITY_FINAL_ROOT" ]] && [[ ! -L "$PARITY_FINAL_ROOT" ]]; then
        if [[ -n "$PARITY_FINAL_ID" && "$(parity_identity "$PARITY_FINAL_ROOT")" == "$PARITY_FINAL_ID" ]]; then
          rm -rf -- "$PARITY_FINAL_ROOT" || status=1
        else
          status=1
        fi
      fi
      if [[ -n "$PARITY_STAGING_ROOT" ]] &&
        [[ -d "$PARITY_STAGING_ROOT" ]] && [[ ! -L "$PARITY_STAGING_ROOT" ]]; then
        if [[ -n "$PARITY_STAGING_ID" && "$(parity_identity "$PARITY_STAGING_ROOT")" == "$PARITY_STAGING_ID" ]]; then
          rm -rf -- "$PARITY_STAGING_ROOT" || status=1
        else
          status=1
        fi
      fi
    elif [[ "$PARITY_MOVED" == "1" ]]; then
      if [[ -d "$PARITY_FINAL_ROOT" ]] && [[ ! -L "$PARITY_FINAL_ROOT" ]]; then
        if [[ -n "$PARITY_FINAL_ID" && "$(parity_identity "$PARITY_FINAL_ROOT")" == "$PARITY_FINAL_ID" ]]; then
          rm -rf -- "$PARITY_FINAL_ROOT" || status=1
        else
          status=1
        fi
      fi
      if [[ -n "$PARITY_STAGING_ROOT" ]] &&
        [[ -d "$PARITY_STAGING_ROOT" ]] && [[ ! -L "$PARITY_STAGING_ROOT" ]]; then
        if [[ -n "$PARITY_STAGING_ID" && "$(parity_identity "$PARITY_STAGING_ROOT")" == "$PARITY_STAGING_ID" ]]; then
          rm -rf -- "$PARITY_STAGING_ROOT" || status=1
        else
          status=1
        fi
      fi
    elif [[ -n "$PARITY_STAGING_ROOT" ]] &&
      [[ -d "$PARITY_STAGING_ROOT" ]] && [[ ! -L "$PARITY_STAGING_ROOT" ]]; then
      # The staging directory is the only pre-promotion parity path this run
      # may remove.
      if [[ -n "$PARITY_STAGING_ID" && "$(parity_identity "$PARITY_STAGING_ROOT")" == "$PARITY_STAGING_ID" ]]; then
        rm -rf -- "$PARITY_STAGING_ROOT" || status=1
      else
        status=1
      fi
    fi
  fi
  if [[ "$PARITY_LOCK_ACQUIRED" == "1" ]] &&
    [[ -d "$PARITY_LOCK_DIR" ]] && [[ ! -L "$PARITY_LOCK_DIR" ]]; then
    if [[ -n "$PARITY_LOCK_ID" ]] &&
      [[ "$(parity_identity "$PARITY_LOCK_DIR")" == "$PARITY_LOCK_ID" ]]; then
      rmdir "$PARITY_LOCK_DIR" || status=1
    else
      status=1
    fi
  fi
  exit "$status"
}

trap cleanup EXIT
trap 'exit 1' HUP INT TERM

run_sensitive() {
  local label="$1"
  shift
  : > "$SENSITIVE_LOG"
  if ! "$@" >"$SENSITIVE_LOG" 2>&1; then
    printf '%s\n' "release error: $label failed; sensitive build output was suppressed" >&2
    return 1
  fi
}

normalize_app_bundle_permissions() {
  local bundle="$1" path
  [[ -d "$bundle" ]] || die "application bundle is missing: $bundle"

  find "$bundle" -type d -exec chmod 755 {} +
  while IFS= read -r -d '' path; do
    if [[ -x "$path" ]] || is_macho_file "$path"; then
      chmod 755 "$path"
    else
      chmod 644 "$path"
    fi
  done < <(find "$bundle" -type f -print0)
}

app_tree_digest() {
  local bundle="$1" manifest_dir manifest_raw manifest_file manifest_digest path mode entry_digest
  [[ -d "$bundle" && ! -L "$bundle" ]] || die "application bundle is missing or unsafe"
  manifest_dir="$(mktemp -d "${TMPDIR:-/tmp}/telegram-companion-preseed-digest.XXXXXX")" ||
    die "cannot create pre-seed digest workspace"
  chmod 700 "$manifest_dir"
  manifest_raw="$manifest_dir/raw"
  manifest_file="$manifest_dir/manifest"
  if ! (
    cd "$bundle" || exit 1
    find . -print | LC_ALL=C sort | while IFS= read -r path; do
      case "$path" in
        *$'\t'*|*$'\r'*|*$'\n'*) exit 1 ;;
      esac
      if [[ -L "$path" ]]; then
        printf 'L\t%s\t%s\n' "$path" "$(readlink "$path")"
      elif [[ -f "$path" ]]; then
        mode="$(stat -f '%Lp' "$path")" || exit 1
        entry_digest="$(shasum -a 256 -- "$path" | awk 'NR == 1 { print $1 }')" || exit 1
        [[ "$entry_digest" =~ ^[0-9a-f]{64}$ ]] || exit 1
        printf 'F\t%s\t%s\t%s\n' "$path" "$mode" "$entry_digest"
      elif [[ -d "$path" ]]; then
        mode="$(stat -f '%Lp' "$path")" || exit 1
        printf 'D\t%s\t%s\n' "$path" "$mode"
      else
        exit 1
      fi
    done
  ) > "$manifest_raw"; then
    rm -f -- "$manifest_raw" "$manifest_file"
    rmdir -- "$manifest_dir" 2>/dev/null || true
    die "application bundle digest failed"
  fi
  LC_ALL=C sort "$manifest_raw" -o "$manifest_file" || die "application bundle digest failed"
  manifest_digest="$(shasum -a 256 -- "$manifest_file" | awk 'NR == 1 { print $1 }')" ||
    die "application bundle digest failed"
  rm -f -- "$manifest_raw" "$manifest_file"
  rmdir -- "$manifest_dir" || die "application bundle digest cleanup failed"
  [[ "$manifest_digest" =~ ^[0-9a-f]{64}$ ]] || die "application bundle digest is malformed"
  printf '%s\n' "$manifest_digest"
}

build_and_install_internal() {
  local built_app="$WAILS_APP_PATH"

  [[ -d "$PUBLIC_APP_PATH" && ! -L "$PUBLIC_APP_PATH" ]] ||
    die "exact public runtime bundle is missing or unsafe"
  printf '%s\n' "building internal desktop app v$PUBLIC_VERSION"
  (
    cd "$REPO_ROOT"
    "$WAILS_BIN" build -clean -skipbindings -platform "darwin/arm64" -tags desktop \
      -ldflags "-X telegram-companion/internal/buildinfo.version=$PUBLIC_VERSION"
  )
  [[ -d "$built_app" ]] || die "internal Wails build did not produce the expected app bundle"
  TELEGRAM_COMPANION_APP_PATH="$built_app" \
  TELEGRAM_COMPANION_RUNTIME_BUNDLE="$PUBLIC_APP_PATH" \
    bash "$REPO_ROOT/scripts/package_macos_dev_runtime.sh"
  /usr/libexec/PlistBuddy -c "Set :CFBundleShortVersionString $PUBLIC_VERSION" \
    "$built_app/Contents/Info.plist"
  /usr/libexec/PlistBuddy -c "Set :CFBundleVersion $PUBLIC_VERSION" \
    "$built_app/Contents/Info.plist"
  normalize_app_bundle_permissions "$built_app"
  codesign --force --sign - "$built_app" >/dev/null
  codesign --verify --strict "$built_app"
  require_arm64_bundle "$built_app"

  INSTALL_WORK_DIR="$(mktemp -d "/Applications/.telegram-companion-install.XXXXXX")"
  INSTALL_STAGE_PATH="$INSTALL_WORK_DIR/$APP_NAME.app"
  INSTALL_BACKUP_PATH="$INSTALL_WORK_DIR/$APP_NAME.backup.app"
  ditto "$built_app" "$INSTALL_STAGE_PATH"
  require_arm64_bundle "$INSTALL_STAGE_PATH"

  if [[ -e "$INSTALL_PATH" || -L "$INSTALL_PATH" ]]; then
    [[ -d "$INSTALL_PATH" && ! -L "$INSTALL_PATH" ]] ||
      die "existing master application path is unsafe"
    INSTALL_BACKUP_ID="$(parity_identity "$INSTALL_PATH")"
    [[ -n "$INSTALL_BACKUP_ID" ]] || die "master backup identity is unavailable"
    INSTALL_REPLACED=1
    mv "$INSTALL_PATH" "$INSTALL_BACKUP_PATH"
    [[ -d "$INSTALL_BACKUP_PATH" && ! -L "$INSTALL_BACKUP_PATH" ]] ||
      die "master backup path is unsafe"
    [[ "$(parity_identity "$INSTALL_BACKUP_PATH")" == "$INSTALL_BACKUP_ID" ]] ||
      die "master backup identity changed"
  else
    INSTALL_REPLACED=1
  fi
  INSTALL_STAGE_ID="$(parity_identity "$INSTALL_STAGE_PATH")"
  [[ -n "$INSTALL_STAGE_ID" ]] || die "master install stage identity is unavailable"
  INSTALL_INSTALLED_ID="$INSTALL_STAGE_ID"
  mv "$INSTALL_STAGE_PATH" "$INSTALL_PATH"
  [[ -d "$INSTALL_PATH" && ! -L "$INSTALL_PATH" ]] ||
    die "installed master path is unsafe"
  [[ "$(parity_identity "$INSTALL_PATH")" == "$INSTALL_INSTALLED_ID" ]] ||
    die "installed master identity changed"
  printf '%s\n' "internal desktop app installed at $INSTALL_PATH"
}

configure_public_environment() {
  PUBLIC_RELEASE_ROOT="$WORK_DIR/public-release"
  PUBLIC_APP_PATH="$PUBLIC_RELEASE_ROOT/$APP_NAME.app"
  PRIVATE_APP_PATH="$PUBLIC_RELEASE_ROOT/private/$APP_NAME.app"
  PUBLIC_ARTIFACT_DIR="$PUBLIC_RELEASE_ROOT/artifacts"

  RELEASE_VERSION="$PUBLIC_VERSION"
  RELEASE_ROOT="$PUBLIC_RELEASE_ROOT"
  APP_PATH="$PUBLIC_APP_PATH"
  STAGED_BUNDLE_PATH="$PRIVATE_APP_PATH/Contents/Resources/bootstrap-state/state.tcs"
  ARTIFACT_DIR="$PUBLIC_ARTIFACT_DIR"
  UPDATE_ZIP="$PUBLIC_ARTIFACT_DIR/Telegram-Companion-$PUBLIC_VERSION-arm64.zip"
  DMG_PATH="$PUBLIC_ARTIFACT_DIR/Telegram-Companion-$PUBLIC_VERSION-arm64-private.dmg"
  APPCAST_INPUT_DIR="$PUBLIC_RELEASE_ROOT/appcast-input"
  PAYLOAD_CACHE="$PERSISTENT_PAYLOAD_CACHE"
  SPARKLE_PAYLOAD_ROOT="$PUBLIC_RELEASE_ROOT/extracted/sparkle"
  SPARKLE_FRAMEWORK_PATH="$SPARKLE_PAYLOAD_ROOT/Sparkle.framework"
  SPARKLE_FRAMEWORK_PARENT="$SPARKLE_PAYLOAD_ROOT"
  SPARKLE_SIGN_UPDATE="$SPARKLE_PAYLOAD_ROOT/bin/sign_update"
  SPARKLE_GENERATE_APPCAST="$SPARKLE_PAYLOAD_ROOT/bin/generate_appcast"
  RELEASE_CHANNEL="local"
  BUILD_TAGS="desktop,public_macos_arm64"

  export RELEASE_VERSION RELEASE_ROOT APP_PATH STAGED_BUNDLE_PATH ARTIFACT_DIR UPDATE_ZIP DMG_PATH
  export APPCAST_INPUT_DIR PAYLOAD_CACHE SPARKLE_PAYLOAD_ROOT SPARKLE_FRAMEWORK_PATH
  export SPARKLE_FRAMEWORK_PARENT SPARKLE_SIGN_UPDATE SPARKLE_GENERATE_APPCAST RELEASE_CHANNEL BUILD_TAGS
  export LICENSE_PUBLIC_KEY APPCAST_URL
  export SPARKLE_PUBLIC_ED_KEY
  if [[ "$MANUAL_UNSEEDED_ONLY" == "0" ]]; then
    export PUBLIC_BOOTSTRAP_BUNDLE_PATH PUBLIC_BOOTSTRAP_BUNDLE_SHA256
  fi
  export PARITY_EVIDENCE_PATH PUBLIC_APP_PATH
  export WAILS_BIN
}

build_public_private_dmg() {
  local pristine_digest private_digest

  printf '%s\n' "building clean public-macos-arm64 private ad-hoc DMG"
  run_sensitive "public preflight" bash "$SCRIPT_DIR/preflight-private.sh"
  run_sensitive "public app build" bash "$SCRIPT_DIR/build-stage.sh"
  normalize_app_bundle_permissions "$PUBLIC_APP_PATH"
  [[ ! -e "$PUBLIC_APP_PATH/Contents/Resources/bootstrap-state" ]] && \
    [[ ! -L "$PUBLIC_APP_PATH/Contents/Resources/bootstrap-state" ]] || \
    die "clean public update branch unexpectedly contains bootstrap state"
  pristine_digest="$(app_tree_digest "$PUBLIC_APP_PATH")"
  mkdir -p "$(dirname "$PRIVATE_APP_PATH")"
  ditto "$PUBLIC_APP_PATH" "$PRIVATE_APP_PATH"
  private_digest="$(app_tree_digest "$PRIVATE_APP_PATH")"
  [[ -n "$pristine_digest" ]] && [[ "$pristine_digest" == "$private_digest" ]] ||
    die "private installer branch differs from the pristine public update branch before seeding"
  APP_PATH="$PRIVATE_APP_PATH" PUBLIC_BOOTSTRAP_BUNDLE_PATH="$PUBLIC_BOOTSTRAP_BUNDLE_PATH" \
    PUBLIC_BOOTSTRAP_BUNDLE_SHA256="$PUBLIC_BOOTSTRAP_BUNDLE_SHA256" \
    run_sensitive "public bootstrap embedding" bash "$SCRIPT_DIR/embed-public-bootstrap-bundle.sh"
  normalize_app_bundle_permissions "$PRIVATE_APP_PATH"
  verify_public_bootstrap_bundle "$STAGED_BUNDLE_PATH" "$PUBLIC_BOOTSTRAP_BUNDLE_SHA256"
  APP_PATH="$PUBLIC_APP_PATH" run_sensitive "public ad-hoc signing" bash "$SCRIPT_DIR/sign-adhoc.sh"
  APP_PATH="$PRIVATE_APP_PATH" run_sensitive "private ad-hoc signing" bash "$SCRIPT_DIR/sign-adhoc.sh"
  UPDATE_APP_PATH="$PUBLIC_APP_PATH" DMG_APP_PATH="$PRIVATE_APP_PATH" \
    run_sensitive "split public/private packaging" bash "$SCRIPT_DIR/package-private.sh"
  APP_PATH="$PRIVATE_APP_PATH" \
    STAGED_BUNDLE_PATH="$PRIVATE_APP_PATH/Contents/Resources/bootstrap-state/state.tcs" \
    run_sensitive "public DMG verification" bash "$SCRIPT_DIR/verify-private.sh"
  verify_public_bootstrap_bundle "$STAGED_BUNDLE_PATH" "$PUBLIC_BOOTSTRAP_BUNDLE_SHA256"
}

build_public_unseeded_zip() {
  local checksum_path

  printf '%s\n' "building clean public-macos-arm64 unseeded ad-hoc ZIP"
  run_sensitive "public preflight" bash "$SCRIPT_DIR/preflight-public.sh"
  run_sensitive "public app build" bash "$SCRIPT_DIR/build-stage.sh"
  normalize_app_bundle_permissions "$PUBLIC_APP_PATH"
  verify_unseeded_public_bundle "$PUBLIC_APP_PATH"
  APP_PATH="$PUBLIC_APP_PATH" run_sensitive "public ad-hoc signing" bash "$SCRIPT_DIR/sign-adhoc.sh"
  codesign --verify --strict "$PUBLIC_APP_PATH"
  verify_unseeded_public_bundle "$PUBLIC_APP_PATH"
  mkdir -p "$PUBLIC_ARTIFACT_DIR"
  ditto -c -k --sequesterRsrc --keepParent "$PUBLIC_APP_PATH" "$UPDATE_ZIP"
  checksum_path="$UPDATE_ZIP.sha256"
  (
    cd "$PUBLIC_ARTIFACT_DIR"
    shasum -a 256 "$(basename "$UPDATE_ZIP")" > "$(basename "$checksum_path")"
  )
  verify_unseeded_update_archive "$UPDATE_ZIP"
  [[ "$(awk 'NR == 1 { print $1 }' "$checksum_path")" == "$(sha256_file "$UPDATE_ZIP")" ]] ||
    die "public ZIP SHA-256 checksum mismatch"
}

sha256_file() {
  shasum -a 256 -- "$1" | awk 'NR == 1 { print $1 }'
}

verify_artifact_copy() {
  local source_path="$1" destination_path="$2"
  local source_digest destination_digest

  [[ -f "$destination_path" ]] || die "artifact copy is missing: $destination_path"
  source_digest="$(sha256_file "$source_path")"
  destination_digest="$(sha256_file "$destination_path")"
  [[ -n "$source_digest" ]] && [[ -n "$destination_digest" ]] ||
    die "artifact SHA-256 calculation failed: $(basename "$source_path")"
  [[ "$source_digest" == "$destination_digest" ]] ||
    die "artifact SHA-256 mismatch after copy: $(basename "$source_path")"
}

verify_bundle_version() {
  local bundle="$1" plist="$1/Contents/Info.plist" short_version build_version
  [[ -f "$plist" ]] && [[ ! -L "$plist" ]] || die "application metadata is missing: $plist"
  short_version="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' "$plist")" ||
    die "application short version is unavailable: $bundle"
  build_version="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleVersion' "$plist")" ||
    die "application build version is unavailable: $bundle"
  [[ "$short_version" == "$PUBLIC_VERSION" && "$build_version" == "$PUBLIC_VERSION" ]] ||
    die "master application version mismatch: $bundle"
}

prepare_parity_staging() {
  local cache_parent="$HOME/.cache/telegram-companion-release"
  local previous_pointer_suffix previous_pointer_canonical
  [[ "$PARITY_CACHE_ROOT" == "$MASTER_PUBLIC_PARITY_ROOT" ]] ||
    die "parity cache root settings disagree"
  case "$PARITY_CACHE_ROOT" in
    "$cache_parent"/master-public-parity|"$cache_parent"/master-public-parity/*) ;;
    *) die "parity cache root must remain under the private release cache" ;;
  esac
  [[ "$PARITY_CACHE_ROOT" != *".."* ]] || die "parity cache root contains an unsafe path"
  require_private_parity_path "$HOME/.cache"
  require_private_parity_path "$cache_parent"
  require_private_parity_path "$PARITY_CACHE_ROOT"
  require_private_parity_path "$PARITY_CACHE_ROOT/versions"
  require_private_parity_path "$PARITY_CACHE_ROOT/current"
  chmod 700 "$PARITY_CACHE_ROOT" "$PARITY_CACHE_ROOT/versions" "$PARITY_CACHE_ROOT/current"
  PARITY_LOCK_DIR="$PARITY_CACHE_ROOT/.build.lock"
  if ! mkdir "$PARITY_LOCK_DIR" 2>/dev/null; then
    die "another master/public parity build is active"
  fi
  PARITY_LOCK_ACQUIRED=1
  PARITY_LOCK_ID="$(parity_identity "$PARITY_LOCK_DIR")"
  [[ -n "$PARITY_LOCK_ID" ]] || die "parity build lock identity is unavailable"
  [[ -d "$PARITY_LOCK_DIR" && ! -L "$PARITY_LOCK_DIR" ]] || die "parity build lock is unsafe"
  chmod 700 "$PARITY_LOCK_DIR"
  PARITY_STAGING_ROOT="$(mktemp -d "$PARITY_CACHE_ROOT/.${PUBLIC_VERSION}.staging.XXXXXX")"
  PARITY_STAGING_ID="$(parity_identity "$PARITY_STAGING_ROOT")"
  [[ -n "$PARITY_STAGING_ID" ]] || die "parity staging identity is unavailable"
  chmod 700 "$PARITY_STAGING_ROOT"
  PARITY_VERSION_ROOT="$PARITY_CACHE_ROOT/versions/$PUBLIC_VERSION"
  require_private_parity_path "$PARITY_VERSION_ROOT"
  chmod 700 "$PARITY_VERSION_ROOT"
  PARITY_FINAL_ROOT="$PARITY_VERSION_ROOT/$SOURCE_SHA"
  PARITY_CURRENT_POINTER="$PARITY_CACHE_ROOT/current/$PUBLIC_VERSION"
  [[ ! -e "$PARITY_FINAL_ROOT" && ! -L "$PARITY_FINAL_ROOT" ]] ||
    die "parity snapshot already exists for source SHA"
  if [[ -e "$PARITY_CURRENT_POINTER" || -L "$PARITY_CURRENT_POINTER" ]]; then
    [[ -L "$PARITY_CURRENT_POINTER" ]] || die "parity current pointer is not a symlink"
    PARITY_PREVIOUS_POINTER_TARGET="$(readlink "$PARITY_CURRENT_POINTER")"
    [[ "$PARITY_PREVIOUS_POINTER_TARGET" = /* ]] ||
      die "parity current pointer target is not absolute"
    [[ "$PARITY_PREVIOUS_POINTER_TARGET" == "$PARITY_VERSION_ROOT/"* ]] ||
      die "parity current pointer target is outside the private cache"
    previous_pointer_suffix="${PARITY_PREVIOUS_POINTER_TARGET#"$PARITY_VERSION_ROOT/"}"
    [[ "$previous_pointer_suffix" =~ ^[0-9a-f]{40}$ ]] ||
      die "parity current pointer target is not a source SHA"
    [[ -d "$PARITY_PREVIOUS_POINTER_TARGET" && ! -L "$PARITY_PREVIOUS_POINTER_TARGET" ]] ||
      die "parity current pointer target is missing or unsafe"
    previous_pointer_canonical="$(cd "$PARITY_PREVIOUS_POINTER_TARGET" && pwd -P)" ||
      die "parity current pointer target cannot be resolved"
    [[ "$previous_pointer_canonical" == "$PARITY_VERSION_ROOT/$previous_pointer_suffix" ]] ||
      die "parity current pointer target resolves outside the private cache"
    PARITY_PREVIOUS_POINTER_PRESENT=1
  fi
  PARITY_EVIDENCE_PATH="$PARITY_STAGING_ROOT/master-public.parity"
  MASTER_APP_PATH="$PARITY_STAGING_ROOT/master/$APP_NAME.app"
  PUBLIC_PARITY_APP_PATH="$PARITY_STAGING_ROOT/public/$APP_NAME.app"
}

promote_parity_snapshot() {
  local pointer_parent
  [[ -d "$PARITY_STAGING_ROOT" && ! -L "$PARITY_STAGING_ROOT" ]] || die "parity staging root is missing or unsafe"
  [[ ! -e "$PARITY_FINAL_ROOT" && ! -L "$PARITY_FINAL_ROOT" ]] ||
    die "parity snapshot already exists for source SHA"
  PARITY_FINAL_ID="$PARITY_STAGING_ID"
  [[ -n "$PARITY_FINAL_ID" ]] || die "parity snapshot identity is unavailable"
  PARITY_MOVED=1
  mv "$PARITY_STAGING_ROOT" "$PARITY_FINAL_ROOT"
  [[ "$(parity_identity "$PARITY_FINAL_ROOT")" == "$PARITY_FINAL_ID" ]] ||
    die "parity snapshot identity changed"
  pointer_parent="$(dirname "$PARITY_CURRENT_POINTER")"
  [[ -d "$pointer_parent" && ! -L "$pointer_parent" ]] || die "parity pointer directory is unsafe"
  PARITY_POINTER_TMP="$(mktemp "$pointer_parent/.${PUBLIC_VERSION}.pointer.XXXXXX")"
  rm -f -- "$PARITY_POINTER_TMP"
  ln -s "$PARITY_FINAL_ROOT" "$PARITY_POINTER_TMP"
  PARITY_POINTER_ID="$(parity_identity "$PARITY_POINTER_TMP")"
  [[ -n "$PARITY_POINTER_ID" ]] || die "parity pointer identity is unavailable"
  # Mark the promotion before the atomic replacement so an interrupt after
  # mv cannot make cleanup mistake the new pointer for an old one.
  PARITY_PROMOTED=1
  mv -fh "$PARITY_POINTER_TMP" "$PARITY_CURRENT_POINTER"
  PARITY_CURRENT_POINTER_ID="$(parity_identity "$PARITY_CURRENT_POINTER")"
  [[ "$PARITY_CURRENT_POINTER_ID" == "$PARITY_POINTER_ID" ]] ||
    die "parity pointer identity changed"
  PARITY_STAGING_ROOT=""
  MASTER_APP_PATH="$PARITY_FINAL_ROOT/master/$APP_NAME.app"
  PUBLIC_PARITY_APP_PATH="$PARITY_FINAL_ROOT/public/$APP_NAME.app"
  PARITY_EVIDENCE_PATH="$PARITY_FINAL_ROOT/master-public.parity"
  export PARITY_EVIDENCE_PATH SOURCE_SHA
}

deliver_artifacts() {
  local artifact host_artifact filename local_digest remote_digest remote_output
  local -a artifacts=()

  mkdir -p "$HOST_DOWNLOADS"
  while IFS= read -r -d '' artifact; do
    artifacts+=("$artifact")
  done < <(find "$PUBLIC_ARTIFACT_DIR" -maxdepth 1 -type f -print0)
  [[ "${#artifacts[@]}" -gt 0 ]] || die "public build produced no artifacts"
  for artifact in "${artifacts[@]}"; do
    host_artifact="$HOST_DOWNLOADS/$(basename "$artifact")"
    ditto "$artifact" "$host_artifact"
    verify_artifact_copy "$artifact" "$host_artifact"
  done
  printf '%s\n' "artifacts copied to HOST_DOWNLOADS and SHA-256 verified"

  if [[ -z "$UTM_GUEST_SSH" ]]; then
    printf '%s\n' "guest delivery skipped: UTM_GUEST_SSH is unset"
    return
  fi
  if ! command -v ssh >/dev/null 2>&1 || ! command -v scp >/dev/null 2>&1; then
    die "guest delivery requires ssh and scp"
  fi
  if ! ssh -o BatchMode=yes -o ConnectTimeout=5 "$UTM_GUEST_SSH" 'mkdir -p "$HOME/Downloads"' >/dev/null 2>&1; then
    die "UTM guest SSH is unavailable: $UTM_GUEST_SSH"
  fi

  for artifact in "${artifacts[@]}"; do
    filename="$(basename "$artifact")"
    local_digest="$(sha256_file "$artifact")"
    if ! scp -q -o BatchMode=yes -o ConnectTimeout=5 "$artifact" \
      "$UTM_GUEST_SSH:Downloads/" >/dev/null 2>&1; then
      die "guest delivery failed during SCP: $filename"
    fi
    if ! remote_output="$(ssh -o BatchMode=yes -o ConnectTimeout=5 "$UTM_GUEST_SSH" \
      "shasum -a 256 -- \"\$HOME/Downloads/$filename\"" 2>/dev/null)"; then
      die "guest delivery failed during SHA-256 verification: $filename"
    fi
    remote_digest="$(awk 'NR == 1 { print $1 }' <<<"$remote_output")"
    [[ "$local_digest" == "$remote_digest" ]] ||
      die "guest delivery SHA-256 mismatch: $filename"
  done
  printf '%s\n' "artifacts copied to the UTM guest Downloads directory and SHA-256 verified"
}

main() {
  require_macos_arm64_host
  cd "$REPO_ROOT"
  require_clean_source_tree
  source_sha_before_master="$(git -C "$REPO_ROOT" rev-parse HEAD)"
  [[ "$source_sha_before_master" =~ ^[0-9a-f]{40}$ ]] || die "source revision is malformed"
  SOURCE_SHA="$source_sha_before_master"
  require_env "PUBLIC_VERSION"
  require_env "LICENSE_PUBLIC_KEY"
  require_env "APPCAST_URL"
  require_env "HOST_DOWNLOADS"
  require_env "SPARKLE_PUBLIC_ED_KEY"
  if [[ "$MANUAL_UNSEEDED_ONLY" == "0" ]]; then
    require_env "PUBLIC_BOOTSTRAP_BUNDLE_PATH"
    require_env "PUBLIC_BOOTSTRAP_BUNDLE_SHA256"
  fi
  [[ "$PUBLIC_VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] ||
    die "PUBLIC_VERSION must be a numeric three-part version"
  [[ -x "$WAILS_BIN" ]] || die "Wails executable is unavailable: $WAILS_BIN"
  for command in awk codesign ditto file jq lipo mktemp rmdir shasum stat; do
    require_command "$command"
  done
  ensure_build_timestamp

  umask 077
  WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/telegram-companion-dual-local.XXXXXX")"
  WORK_DIR="$(cd "$WORK_DIR" && pwd -P)"
  SENSITIVE_LOG="$WORK_DIR/sensitive-build.log"
  : > "$SENSITIVE_LOG"
  chmod 600 "$SENSITIVE_LOG"
  umask 022

  prepare_parity_staging
  configure_public_environment
  if [[ "$MANUAL_UNSEEDED_ONLY" == "1" ]]; then
    build_public_unseeded_zip
  else
  build_public_private_dmg
  fi
  source_sha_after_public="$(git -C "$REPO_ROOT" rev-parse HEAD)"
  [[ "$source_sha_before_master" == "$source_sha_after_public" ]] ||
    die "source tree changed during public build"
  build_and_install_internal
  source_sha_after_master="$(git -C "$REPO_ROOT" rev-parse HEAD)"
  [[ "$source_sha_before_master" == "$source_sha_after_master" ]] ||
    die "source tree changed during internal build"
  mkdir -p "$(dirname "$MASTER_APP_PATH")"
  ditto "$INSTALL_PATH" "$MASTER_APP_PATH"
  require_arm64_bundle "$MASTER_APP_PATH"
  verify_bundle_version "$MASTER_APP_PATH"
  mkdir -p "$(dirname "$PUBLIC_PARITY_APP_PATH")"
  ditto "$PUBLIC_APP_PATH" "$PUBLIC_PARITY_APP_PATH"
  MASTER_APP_PATH="$MASTER_APP_PATH" \
  PUBLIC_APP_PATH="$PUBLIC_PARITY_APP_PATH" \
  RELEASE_VERSION="$PUBLIC_VERSION" \
  SOURCE_ROOT="$REPO_ROOT" \
  SOURCE_SHA="$SOURCE_SHA" \
  PARITY_EVIDENCE_PATH="$PARITY_EVIDENCE_PATH" \
    bash "$SCRIPT_DIR/master-public-parity.sh" write >/dev/null
  MASTER_APP_PATH="$MASTER_APP_PATH" \
  PUBLIC_APP_PATH="$PUBLIC_PARITY_APP_PATH" \
  RELEASE_VERSION="$PUBLIC_VERSION" \
  SOURCE_ROOT="$REPO_ROOT" \
  SOURCE_SHA="$SOURCE_SHA" \
  PARITY_EVIDENCE_PATH="$PARITY_EVIDENCE_PATH" \
    bash "$SCRIPT_DIR/master-public-parity.sh" verify >/dev/null
  promote_parity_snapshot
  printf '%s PASS\n' "$PARITY_EVIDENCE_PATH"
  deliver_artifacts

  INSTALL_COMMITTED=1
  printf '%s\n' "dual local build completed"
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  main "$@"
fi
