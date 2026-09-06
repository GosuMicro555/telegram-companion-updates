#!/usr/bin/env bash
set -euo pipefail

root=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
source_binary="${1:-$root/build/bin/telegram-companion}"
source_icon="${2:-$root/assets/icon.svg}"
release_dir=$(CDPATH= cd -- "$(dirname -- "$source_binary")" && pwd)
source_models="$release_dir/models"

if [[ ! -f "$source_binary" || ! -f "$source_icon" || ! -d "$source_models" ]]; then
  printf 'binary, icon, or models are missing\n' >&2
  exit 2
fi

app_dir="$HOME/.local/opt/telegram-companion"
icon_dir="$HOME/.local/share/icons/hicolor/scalable/apps"
menu_dir="$HOME/.local/share/applications"
desktop_dir=$(xdg-user-dir DESKTOP 2>/dev/null || true)
if [[ -z "$desktop_dir" ]]; then
  desktop_dir="$HOME/Desktop"
fi

mkdir -p "$app_dir" "$icon_dir" "$menu_dir" "$desktop_dir"
installed_binary="$app_dir/telegram-companion"
installed_models="$app_dir/models"
installed_icon="$icon_dir/telegram-companion.svg"
menu_launcher="$menu_dir/telegram-companion.desktop"
desktop_launcher="$desktop_dir/telegram-companion.desktop"

temporary_binary=$(mktemp "$app_dir/.telegram-companion.XXXXXX")
temporary_models=$(mktemp -d "$app_dir/.models.XXXXXX")
cleanup() { rm -f -- "$temporary_binary"; rm -rf -- "$temporary_models"; }
trap cleanup EXIT
install -m 0755 "$source_binary" "$temporary_binary"
cp -a "$source_models/." "$temporary_models/"

source_hash=$(sha256sum "$source_binary" | awk '{print $1}')
temporary_hash=$(sha256sum "$temporary_binary" | awk '{print $1}')
if [[ "$source_hash" != "$temporary_hash" ]]; then
  printf 'binary checksum verification failed\n' >&2
  exit 1
fi

while IFS= read -r -d '' source_model; do
  relative_model=${source_model#"$source_models"/}
  installed_model="$temporary_models/$relative_model"
  if [[ ! -f "$installed_model" ]] ||
     [[ "$(sha256sum "$source_model" | awk '{print $1}')" != "$(sha256sum "$installed_model" | awk '{print $1}')" ]]; then
    printf 'model checksum verification failed: %s\n' "$relative_model" >&2
    exit 1
  fi
done < <(find "$source_models" -type f -print0)

rm -rf -- "$installed_models"
mv -- "$temporary_models" "$installed_models"
mv -f -- "$temporary_binary" "$installed_binary"
install -m 0644 "$source_icon" "$installed_icon"

cat >"$menu_launcher" <<EOF
[Desktop Entry]
Type=Application
Name=Telegram Companion
Comment=Telegram keyword analytics and replies
Exec="$installed_binary"
Icon=$installed_icon
Terminal=false
Categories=Network;
StartupNotify=true
StartupWMClass=telegram-companion
EOF

install -m 0755 "$menu_launcher" "$desktop_launcher"
chmod 0755 "$menu_launcher"
if command -v desktop-file-validate >/dev/null 2>&1; then
  desktop-file-validate "$menu_launcher"
fi
if command -v gio >/dev/null 2>&1; then
  gio set "$desktop_launcher" metadata::trusted true >/dev/null 2>&1 || true
fi
if command -v update-desktop-database >/dev/null 2>&1; then
  update-desktop-database "$menu_dir" >/dev/null 2>&1 || true
fi

installed_hash=$(sha256sum "$installed_binary" | awk '{print $1}')
if [[ "$source_hash" != "$installed_hash" ]]; then
  printf 'installed binary checksum verification failed\n' >&2
  exit 1
fi

printf 'installed=%s\nsha256=%s\nmenu=%s\ndesktop=%s\n' \
  "$installed_binary" "$installed_hash" "$menu_launcher" "$desktop_launcher"
