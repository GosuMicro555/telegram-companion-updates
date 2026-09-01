#!/usr/bin/env bash
set -euo pipefail

platform="${1:-}"
script_dir="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
root="$(dirname -- "$script_dir")"
go_command="${GO:-go}"
manifest="$root/models/manifest.json"

"$go_command" run "$script_dir/fetch_model.go" -manifest "$manifest"
"$go_command" run "$script_dir/fetch_model.go" -manifest "$manifest" -verify-payload

case "$platform" in
  linux)
    package_root="$root/build/bin"
    ;;
  darwin)
    package_root="$root/build/bin/telegram-companion.app/Contents/Resources"
    ;;
  *)
    printf 'unsupported Wails model package platform: %s\n' "$platform" >&2
    exit 2
    ;;
esac

"$go_command" run "$script_dir/fetch_model.go" -manifest "$manifest" -package-root "$package_root"

if [[ "$platform" == "darwin" ]]; then
  app_bundle="$root/build/bin/telegram-companion.app"
  codesign --force --deep --sign - "$app_bundle"
  codesign --verify --deep --strict "$app_bundle"
fi
