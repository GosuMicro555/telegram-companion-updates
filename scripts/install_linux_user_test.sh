#!/usr/bin/env bash
set -euo pipefail

root=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
work=$(mktemp -d)
cleanup() { rm -rf -- "$work"; }
trap cleanup EXIT

export HOME="$work/home"
mkdir -p "$HOME/Desktop" "$work/input/models/multilingual-e5-small"
printf 'v0.4.1-binary' >"$work/input/telegram-companion"
printf 'png' >"$work/input/icon.png"
printf 'model-v1' >"$work/input/models/multilingual-e5-small/model.onnx"
printf 'tokenizer-v1' >"$work/input/models/multilingual-e5-small/tokenizer.json"

bash "$root/scripts/install_linux_user.sh" "$work/input/telegram-companion" "$work/input/icon.png"

installed="$HOME/.local/opt/telegram-companion/telegram-companion"
installed_model="$HOME/.local/opt/telegram-companion/models/multilingual-e5-small/model.onnx"
menu="$HOME/.local/share/applications/telegram-companion.desktop"
desktop="$HOME/Desktop/telegram-companion.desktop"
test "$(sha256sum "$installed" | awk '{print $1}')" = "$(sha256sum "$work/input/telegram-companion" | awk '{print $1}')"
test -x "$installed"
grep -F 'model-v1' "$installed_model" >/dev/null
test -x "$menu"
test -x "$desktop"
grep -F "Exec=\"$installed\"" "$menu" >/dev/null
cmp "$menu" "$desktop"

printf 'v0.4.1-replacement' >"$work/input/telegram-companion"
printf 'model-v2' >"$work/input/models/multilingual-e5-small/model.onnx"
bash "$root/scripts/install_linux_user.sh" "$work/input/telegram-companion" "$work/input/icon.png" >/dev/null
grep -F 'v0.4.1-replacement' "$installed" >/dev/null
grep -F 'model-v2' "$installed_model" >/dev/null

project="$work/project"
mkdir -p "$project/scripts" "$project/assets" "$project/build/bin/models/multilingual-e5-small"
cp "$root/scripts/install_linux_user.sh" "$project/scripts/"
cp "$root/assets/icon.svg" "$project/assets/"
printf 'default-v0.4.1' >"$project/build/bin/telegram-companion"
printf 'default-model' >"$project/build/bin/models/multilingual-e5-small/model.onnx"
bash "$project/scripts/install_linux_user.sh" >/dev/null
test -f "$HOME/.local/share/icons/hicolor/scalable/apps/telegram-companion.svg"
grep -F 'default-v0.4.1' "$installed" >/dev/null
