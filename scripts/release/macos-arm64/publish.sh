#!/usr/bin/env bash
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

require_publish_configuration() {
  require_public_release_channel
  require_env "GH_TOKEN"
  require_env "RELEASE_REPOSITORY"
  require_env "APPCAST_REPOSITORY"
  [[ "${RELEASE_PUBLISH_CONFIRM:-}" == "publish" ]] || die "set RELEASE_PUBLISH_CONFIRM=publish to publish"
}

ensure_release_exists() {
  if ! gh release view "v$RELEASE_VERSION" --repo "$RELEASE_REPOSITORY" >/dev/null 2>&1; then
    gh release create "v$RELEASE_VERSION" --repo "$RELEASE_REPOSITORY" \
      --title "Telegram Companion $RELEASE_VERSION" --generate-notes
  fi
}

publish_payload_assets() {
  ensure_release_exists
  ensure_immutable_release_assets
}

# Release assets are immutable inputs to the signed appcast.  A retry may
# observe assets that were already uploaded, but a different byte stream under
# the same version must never be silently replaced.  Existing assets are
# downloaded into a private temporary directory and compared exactly; only
# missing names are uploaded, without --clobber.
ensure_immutable_release_assets() (
  local release_json verify_dir path name asset_count
  local -a expected_paths missing_assets=()

  expected_paths=(
    "$UPDATE_ZIP"
    "$ARTIFACT_DIR/SHA256SUMS"
    "$ARTIFACT_DIR/update-signature.txt"
  )
  for path in "${expected_paths[@]}"; do
    [[ -f "$path" ]] || die "release asset is missing: $path"
  done

  release_json="$(gh release view "v$RELEASE_VERSION" --repo "$RELEASE_REPOSITORY" --json assets)" ||
    die "release assets are unavailable"
  jq -e '.assets | type == "array"' <<<"$release_json" >/dev/null ||
    die "release assets response is malformed"

  verify_dir="$(mktemp -d "${TMPDIR:-/tmp}/telegram-companion-publish-assets.XXXXXX")" ||
    die "release asset verification workspace is unavailable"
  trap 'rm -rf -- "$verify_dir"' EXIT HUP INT TERM
  chmod 700 "$verify_dir" || die "release asset verification permissions failed"

  for path in "${expected_paths[@]}"; do
    name="$(basename "$path")"
    asset_count="$(jq -er --arg asset "$name" '[.assets[] | select(.name == $asset)] | length' <<<"$release_json")" ||
      die "release assets response is malformed"
    case "$asset_count" in
      0)
        missing_assets+=("$path")
        ;;
      1)
        gh release download "v$RELEASE_VERSION" --repo "$RELEASE_REPOSITORY" \
          --dir "$verify_dir" --pattern "$name" --clobber >/dev/null 2>&1 ||
          die "existing release asset cannot be read: $name"
        if ! cmp -s "$path" "$verify_dir/$name"; then
          die "release asset differs from local artifact: $name"
        fi
        ;;
      *)
        die "release contains duplicate asset names: $name"
        ;;
    esac
  done

  if [[ "${#missing_assets[@]}" -gt 0 ]]; then
    gh release upload "v$RELEASE_VERSION" --repo "$RELEASE_REPOSITORY" \
      "${missing_assets[@]}" >/dev/null || die "release asset upload failed"
  fi
)

verify_published_payloads() (
  local release_json download_dir
  release_json="$(gh release view "v$RELEASE_VERSION" --repo "$RELEASE_REPOSITORY" --json assets)"
  for asset in "$(basename "$UPDATE_ZIP")" SHA256SUMS update-signature.txt; do
    jq -e --arg asset "$asset" '.assets[] | select(.name == $asset) | .url' <<<"$release_json" >/dev/null ||
      die "published asset is unavailable: $asset"
  done

  download_dir="$(mktemp -d)"
  trap 'rm -rf "$download_dir"' EXIT
  gh release download "v$RELEASE_VERSION" --repo "$RELEASE_REPOSITORY" --clobber \
    --dir "$download_dir" \
    --pattern "$(basename "$UPDATE_ZIP")" \
    --pattern SHA256SUMS \
    --pattern update-signature.txt
  (
    cd "$download_dir"
    shasum -a 256 -c SHA256SUMS
  )
  cmp -s "$UPDATE_ZIP" "$download_dir/$(basename "$UPDATE_ZIP")" || die "published update archive differs from local artifact"
  cmp -s "$ARTIFACT_DIR/SHA256SUMS" "$download_dir/SHA256SUMS" || die "published checksums differ from local artifact"
  cmp -s "$ARTIFACT_DIR/update-signature.txt" "$download_dir/update-signature.txt" || die "published signature differs from local artifact"
)

publish_appcast_last() {
  local endpoint encoded_appcast existing_json existing_content existing_sha
  local lookup_error_file readback_json readback_content readback_type
  endpoint="repos/$APPCAST_REPOSITORY/contents/appcast.xml"
  encoded_appcast="$(base64 < "$APPCAST_PATH" | tr -d '\r\n')"
  existing_json=""
  existing_content=""
  existing_sha=""

  lookup_error_file="$(mktemp "${TMPDIR:-/tmp}/telegram-companion-appcast-lookup.XXXXXX")" ||
    die "remote appcast lookup workspace is unavailable"
  chmod 600 "$lookup_error_file" || {
    rm -f -- "$lookup_error_file"
    die "remote appcast lookup permissions failed"
  }
  if existing_json="$(gh api --method GET "$endpoint" -f "ref=$APPCAST_BRANCH" 2>"$lookup_error_file")"; then
    rm -f -- "$lookup_error_file"
    readback_type="$(jq -er '.type // empty' <<<"$existing_json")" ||
      die "remote appcast response is malformed"
    [[ "$readback_type" == "file" ]] || die "remote appcast response is malformed"
    existing_content="$(jq -er '.content // empty' <<<"$existing_json" | tr -d '\r\n')" ||
      die "remote appcast response is malformed"
    existing_sha="$(jq -er '.sha // empty' <<<"$existing_json")" ||
      die "remote appcast response is malformed"
    [[ -n "$existing_content" && "$existing_sha" =~ ^[0-9a-f]{40}$ ]] ||
      die "remote appcast response is malformed"
    if [[ "$existing_content" == "$encoded_appcast" ]]; then
      printf '%s\n' "appcast already contains release $RELEASE_VERSION"
      return
    fi
  else
    if ! grep -Eiq 'HTTP[[:space:]]+404|HTTP/[^[:space:]]+[[:space:]]+404' "$lookup_error_file" 2>/dev/null; then
      rm -f -- "$lookup_error_file"
      die "remote appcast lookup failed"
    fi
    rm -f -- "$lookup_error_file"
  fi

  if [[ -n "$existing_sha" ]]; then
    gh api --method PUT "$endpoint" \
      -f "message=release: publish Telegram Companion $RELEASE_VERSION appcast" \
      -f "content=$encoded_appcast" -f "branch=$APPCAST_BRANCH" -f "sha=$existing_sha" >/dev/null ||
      die "remote appcast update failed"
  else
    gh api --method PUT "$endpoint" \
      -f "message=release: publish Telegram Companion $RELEASE_VERSION appcast" \
      -f "content=$encoded_appcast" -f "branch=$APPCAST_BRANCH" >/dev/null ||
      die "remote appcast creation failed"
  fi

  readback_json="$(gh api --method GET "$endpoint" -f "ref=$APPCAST_BRANCH" 2>/dev/null)" ||
    die "remote appcast readback failed"
  readback_type="$(jq -er '.type // empty' <<<"$readback_json")" ||
    die "remote appcast readback is malformed"
  [[ "$readback_type" == "file" ]] || die "remote appcast readback is malformed"
  readback_content="$(jq -er '.content // empty' <<<"$readback_json" | tr -d '\r\n')" ||
    die "remote appcast readback is malformed"
  [[ "$readback_content" == "$encoded_appcast" ]] ||
    die "remote appcast differs from local appcast"
}

main() {
  require_publish_configuration
  for command in base64 codesign ditto find gh grep jq mktemp readlink rm cmp wc; do
    require_command "$command"
  done
  validate_revocation_build_metadata
  resolve_master_public_parity
  read_master_public_parity_source_sha
  resolve_public_bootstrap_verification_path
  require_env "PARITY_EVIDENCE_PATH"
  require_env "MASTER_APP_PATH"
  require_env "PUBLIC_APP_PATH"
  require_env "SOURCE_ROOT"
  require_env "SOURCE_SHA"
  MASTER_APP_PATH="$MASTER_APP_PATH" \
  PUBLIC_APP_PATH="$PUBLIC_APP_PATH" \
  RELEASE_VERSION="$RELEASE_VERSION" \
  SOURCE_ROOT="$SOURCE_ROOT" \
  SOURCE_SHA="$SOURCE_SHA" \
  PARITY_EVIDENCE_PATH="$PARITY_EVIDENCE_PATH" \
    bash "$SCRIPT_DIR/master-public-parity.sh" verify
  PUBLIC_BOOTSTRAP_VERIFY_BUNDLE_PATH="$STAGED_BUNDLE_PATH" bash "$SCRIPT_DIR/verify-public-release-bootstrap.sh"
  [[ -f "$APPCAST_PATH" ]] || die "signed appcast is missing: $APPCAST_PATH"
  require_public_distribution_signature "$PUBLIC_APP_PATH"
  verify_public_update_archive_signature "$UPDATE_ZIP"
  publish_payload_assets
  verify_published_payloads
  publish_appcast_last
}

main "$@"
