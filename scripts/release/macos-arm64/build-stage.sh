#!/usr/bin/env bash
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

fetch_payload() {
  local name="$1"
  local url expected archive archive_part actual
  url="$(payload_field "$name" url)"
  expected="$(payload_field "$name" sha256)"
  archive="$PAYLOAD_CACHE/$name-$(payload_field "$name" version).$(payload_field "$name" archive_type)"
  mkdir -p "$PAYLOAD_CACHE"

  if [[ -f "$archive" ]]; then
    actual="$(shasum -a 256 "$archive" | awk '{print $1}')"
    if [[ "$actual" == "$expected" ]]; then
      printf '%s\n' "$archive"
      return
    fi
    rm -f "$archive"
  fi

  archive_part="$archive.part"
  rm -f "$archive_part"
  curl --fail --location --proto '=https' --tlsv1.2 \
    --connect-timeout 20 --max-time 300 --retry 3 --retry-delay 2 --retry-all-errors \
    --output "$archive_part" "$url"
  verify_sha256 "$archive_part" "$expected"
  mv "$archive_part" "$archive"
  printf '%s\n' "$archive"
}

extract_payload() {
  local archive="$1"
  local archive_type="$2"
  local destination="$3"
  rm -rf "$destination"
  mkdir -p "$destination"
  case "$archive_type" in
    tar.xz|tar.gz) tar -xf "$archive" -C "$destination" ;;
    zip) unzip -oq "$archive" -d "$destination" ;;
    *) die "unsupported payload archive type: $archive_type" ;;
  esac
}

copy_required_item() {
  local name="$1"
  local root="$2"
  local source_name destination found
  source_name="$(payload_field "$name" source)"
  destination="$APP_PATH/$(payload_field "$name" destination)"
  found="$(find "$root" -name "$source_name" -print -quit)"
  [[ -n "$found" ]] || die "payload $name is missing required item: $source_name"
  mkdir -p "$destination"
  cp -R "$found" "$destination/"
}

thin_macho_tree_to_arm64() {
  local root="$1"
  local executable architectures mode thinned
  while IFS= read -r -d '' executable; do
    architectures="$(lipo -archs "$executable" 2>/dev/null)" || continue
    if [[ "$architectures" == "$TARGET_ARCH" ]]; then
      continue
    fi
    [[ " $architectures " == *" $TARGET_ARCH "* ]] ||
      die "payload executable does not contain $TARGET_ARCH: $executable"
    mode="$(stat -f '%Lp' "$executable")"
    thinned="$executable.$TARGET_ARCH"
    lipo "$executable" -thin "$TARGET_ARCH" -output "$thinned"
    chmod "$mode" "$thinned"
    mv "$thinned" "$executable"
  done < <(find "$root" -type f -print0)
}

prepare_sparkle_framework() {
  local archive
  mkdir -p "$SPARKLE_PAYLOAD_ROOT"
  archive="$(fetch_payload sparkle)"
  extract_payload "$archive" "$(payload_field sparkle archive_type)" "$SPARKLE_PAYLOAD_ROOT"
  thin_macho_tree_to_arm64 "$SPARKLE_PAYLOAD_ROOT"
  [[ -d "$SPARKLE_FRAMEWORK_PATH" ]] || die "pinned Sparkle payload is missing Sparkle.framework"
  [[ -x "$SPARKLE_SIGN_UPDATE" ]] || die "pinned Sparkle payload is missing sign_update"
  [[ -x "$SPARKLE_GENERATE_APPCAST" ]] || die "pinned Sparkle payload is missing generate_appcast"
}

stage_sparkle_framework() {
  local destination="$APP_PATH/$(payload_field sparkle destination)"
  mkdir -p "$destination"
  cp -R "$SPARKLE_FRAMEWORK_PATH" "$destination/"
}

stage_payload() {
  local name="$1"
  local archive archive_type extraction
  archive_type="$(payload_field "$name" archive_type)"
  extraction="$RELEASE_ROOT/extracted/$name"
  mkdir -p "$extraction"
  archive="$(fetch_payload "$name")"
  extract_payload "$archive" "$archive_type" "$extraction"
  if [[ "$name" == "snowflake" ]]; then
    local source_root
    source_root="$(dirname "$(find "$extraction" -name go.mod -print -quit)")"
    [[ -f "$source_root/go.mod" ]] || die "Snowflake source root is missing go.mod"
    mkdir -p "$APP_PATH/$(payload_field "$name" destination)"
    (
      cd "$source_root"
      GOOS="$TARGET_OS" GOARCH="$TARGET_ARCH" CGO_ENABLED=0 \
        go build -trimpath -buildvcs=false -o "$APP_PATH/$(payload_field "$name" destination)/snowflake-client" ./client
    )
    return
  fi
  if [[ "$name" == "tor" ]]; then
    local tor_binary tor_root
    tor_binary="$(find "$extraction" -type f -name tor -print -quit)"
    [[ -x "$tor_binary" ]] || die "Tor expert bundle is missing the arm64 executable"
    tor_root="$(dirname "$tor_binary")"
    mkdir -p "$APP_PATH/$(payload_field "$name" destination)"
    cp -R "$tor_root/." "$APP_PATH/$(payload_field "$name" destination)/"
    if [[ -d "$extraction/data" ]]; then
      cp -R "$extraction/data" "$APP_PATH/$(payload_field "$name" destination)/"
    fi
    return
  fi
  copy_required_item "$name" "$extraction"
}

stage_lyrebird_payload() {
  local archive extraction lyrebird_binary pt_config license_file notice_file license_rel notice_rel destination
  # Lyrebird is shipped inside the accepted Tor Expert Bundle. Reuse the
  # verified Tor archive/cache entry instead of creating a second moving fetch.
  archive="$(fetch_payload tor)"
  verify_sha256 "$archive" "$(payload_field lyrebird sha256)"
  extraction="$RELEASE_ROOT/extracted/lyrebird"
  extract_payload "$archive" "$(payload_field lyrebird archive_type)" "$extraction"
  lyrebird_binary="$(find "$extraction" -type f -name lyrebird -print -quit)"
  pt_config="$(find "$extraction" -type f -name pt_config.json -print -quit)"
  license_rel="$(payload_field lyrebird license_file)"
  notice_rel="$(payload_field lyrebird notice_file)"
  case "$license_rel" in /*|*..*) die "pinned Lyrebird license path is unsafe" ;; esac
  case "$notice_rel" in /*|*..*) die "pinned Lyrebird notice path is unsafe" ;; esac
  license_file="$extraction/$license_rel"
  notice_file="$extraction/$notice_rel"
  [[ -n "$lyrebird_binary" ]] && [[ ! -L "$lyrebird_binary" ]] && [[ -x "$lyrebird_binary" ]] ||
    die "pinned Lyrebird payload is missing a regular executable"
  [[ -n "$pt_config" ]] && [[ ! -L "$pt_config" ]] ||
    die "pinned Lyrebird payload is missing frozen bridge configuration"
  [[ -f "$pt_config" ]] || die "pinned Lyrebird bridge configuration is not a regular file"
  verify_sha256 "$pt_config" "$(payload_field lyrebird pt_config_sha256)"
  [[ -f "$license_file" ]] && [[ ! -L "$license_file" ]] ||
    die "pinned Lyrebird license file is missing or unsafe"
  [[ -f "$notice_file" ]] && [[ ! -L "$notice_file" ]] ||
    die "pinned Lyrebird notice file is missing or unsafe"
  destination="$APP_PATH/$(payload_field lyrebird destination)"
  safe_path_under_root "$APP_PATH" "$(payload_field lyrebird destination)" >/dev/null
  mkdir -p "$destination"
  safe_path_under_root "$APP_PATH" "$(payload_field lyrebird destination)" >/dev/null
  cp "$lyrebird_binary" "$destination/lyrebird"
  cp "$pt_config" "$destination/pt_config.json"
  cp "$license_file" "$destination/$(basename "$license_rel")"
  if [[ "$notice_rel" != "$license_rel" ]]; then
    cp "$notice_file" "$destination/$(basename "$notice_rel")"
  fi
  safe_regular_file_under_root "$APP_PATH" "$(payload_field lyrebird destination)/pt_config.json" >/dev/null
  chmod 0755 "$destination/lyrebird"
  chmod 0644 "$destination/pt_config.json" \
    "$destination/$(basename "$license_rel")" "$destination/$(basename "$notice_rel")"
}

set_bundle_metadata() {
  /usr/libexec/PlistBuddy -c "Set :CFBundleShortVersionString $RELEASE_VERSION" "$APP_PATH/Contents/Info.plist"
  /usr/libexec/PlistBuddy -c "Set :CFBundleVersion $RELEASE_VERSION" "$APP_PATH/Contents/Info.plist"
  /usr/libexec/PlistBuddy -c "Set :SUFeedURL $APPCAST_URL" "$APP_PATH/Contents/Info.plist"
  /usr/libexec/PlistBuddy -c "Set :SUPublicEDKey $SPARKLE_PUBLIC_ED_KEY" "$APP_PATH/Contents/Info.plist"
}

normalize_bundle_permissions() {
  find "$APP_PATH" -type d -exec chmod 0755 {} +
  find "$APP_PATH" -type f -perm +111 -exec chmod 0755 {} +
  find "$APP_PATH" -type f ! -perm +111 -exec chmod 0644 {} +
}

main() {
  require_macos_arm64_host
  validate_build_metadata
  ensure_build_timestamp
  validate_payload_manifest
  mkdir -p "$RELEASE_ROOT" "$ARTIFACT_DIR"
  prepare_sparkle_framework
  bash "$REPO_ROOT/build/macos/generate_icon.sh"
  CGO_CFLAGS="-F$SPARKLE_FRAMEWORK_PARENT ${CGO_CFLAGS:-}" \
    CGO_LDFLAGS="-F$SPARKLE_FRAMEWORK_PARENT -Wl,-rpath,@executable_path/../Frameworks ${CGO_LDFLAGS:-}" \
    "$WAILS_BIN" build -clean -skipbindings -platform "$TARGET_OS/$TARGET_ARCH" -tags "$BUILD_TAGS" \
      -ldflags "-X telegram-companion/internal/buildinfo.version=$RELEASE_VERSION -X telegram-companion/internal/buildinfo.licensePublicKey=$LICENSE_PUBLIC_KEY -X telegram-companion/internal/buildinfo.appcastURL=$APPCAST_URL -X telegram-companion/internal/buildinfo.revocationManifestURL=$REVOCATION_MANIFEST_URL -X telegram-companion/internal/buildinfo.revocationKeyID=$REVOCATION_KEY_ID -X telegram-companion/internal/buildinfo.revocationPublicKey=$REVOCATION_PUBLIC_KEY -X telegram-companion/internal/buildinfo.revocationBuildMetadata=$REVOCATION_BUILD_METADATA"
  [[ -d "$WAILS_APP_PATH" ]] || die "Wails did not produce expected app bundle: $WAILS_APP_PATH"
  mkdir -p "$(dirname "$APP_PATH")"
  ditto "$WAILS_APP_PATH" "$APP_PATH"
  set_bundle_metadata
  stage_sparkle_framework
  stage_payload tor
  stage_lyrebird_payload
  stage_payload snowflake
  normalize_bundle_permissions
  verify_lyrebird_payload "$APP_PATH"
  require_arm64_bundle "$APP_PATH"
}

main "$@"
