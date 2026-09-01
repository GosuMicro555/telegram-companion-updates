#!/usr/bin/env bash
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

PUBLIC_BOOTSTRAP_BUNDLE_PATH="${PUBLIC_BOOTSTRAP_BUNDLE_PATH:-}"

main() {
  require_env "APP_PATH"
  require_env "PUBLIC_BOOTSTRAP_BUNDLE_PATH"
  [[ -d "$APP_PATH" ]] || die "application bundle is missing"
  for command in awk cmp head install shasum; do
    require_command "$command"
  done
  # The public app is still clean at this boundary; after this point the
  # bootstrap seed belongs only to the separately verified seeded installer.
  verify_unseeded_public_bundle "$APP_PATH"
  verify_public_bootstrap_bundle "$PUBLIC_BOOTSTRAP_BUNDLE_PATH" "$PUBLIC_BOOTSTRAP_BUNDLE_SHA256"

  STAGED_BUNDLE_PATH="$APP_PATH/Contents/Resources/bootstrap-state/state.tcs"
  install -d -m 0755 "$(dirname "$STAGED_BUNDLE_PATH")"
  install -m 0644 "$PUBLIC_BOOTSTRAP_BUNDLE_PATH" "$STAGED_BUNDLE_PATH"
  cmp -s "$PUBLIC_BOOTSTRAP_BUNDLE_PATH" "$STAGED_BUNDLE_PATH" ||
    die "embedded public bootstrap bundle differs from the prebuilt source"
  verify_public_bootstrap_bundle "$STAGED_BUNDLE_PATH" "$PUBLIC_BOOTSTRAP_BUNDLE_SHA256"
}

main "$@"
