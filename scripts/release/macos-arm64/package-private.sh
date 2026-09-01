#!/usr/bin/env bash
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

DMG_STAGE="${DMG_STAGE:-$RELEASE_ROOT/dmg-stage}"
PRIVATE_DMG_CHECKSUMS="${PRIVATE_DMG_CHECKSUMS:-$ARTIFACT_DIR/PRIVATE-DMG-SHA256SUMS}"
UPDATE_APP_PATH="${UPDATE_APP_PATH:-}"
DMG_APP_PATH="${DMG_APP_PATH:-}"

write_first_launch_guide() {
  cat > "$DMG_STAGE/ПЕРВЫЙ ЗАПУСК.txt" <<'EOF'
TELEGRAM COMPANION: ПЕРВЫЙ ЗАПУСК

1. Перетащите Telegram Companion.app в папку Applications.
2. Попробуйте открыть приложение обычным двойным кликом.
3. Если macOS заблокировала запуск, откройте:
   Системные настройки -> Конфиденциальность и безопасность.
4. В разделе «Безопасность» нажмите «Всё равно открыть», введите пароль Mac
   и ещё раз подтвердите открытие приложения.

Сверьте SHA-256 образа с файлом PRIVATE-DMG-SHA256SUMS, который передал
разработчик вместе с DMG.
EOF
}

main() {
  require_macos_arm64_host
  require_env "UPDATE_APP_PATH"
  require_env "DMG_APP_PATH"
  [[ -d "$UPDATE_APP_PATH" ]] && [[ ! -L "$UPDATE_APP_PATH" ]] ||
    die "clean update application bundle is missing or unsafe"
  [[ -d "$DMG_APP_PATH" ]] && [[ ! -L "$DMG_APP_PATH" ]] ||
    die "private installer application bundle is missing or unsafe"
  [[ "$UPDATE_APP_PATH" != "$DMG_APP_PATH" ]] ||
    die "update and private installer application paths must be distinct"
  update_canonical="$(cd "$UPDATE_APP_PATH" && pwd -P)"
  dmg_canonical="$(cd "$DMG_APP_PATH" && pwd -P)"
  [[ "$update_canonical" != "$dmg_canonical" ]] ||
    die "update and private installer application paths resolve to the same bundle"
  codesign --verify --strict --verbose=4 "$UPDATE_APP_PATH"
  codesign --verify --strict --verbose=4 "$DMG_APP_PATH"
  mkdir -p "$ARTIFACT_DIR" "$DMG_STAGE"
  ditto -c -k --sequesterRsrc --keepParent "$UPDATE_APP_PATH" "$UPDATE_ZIP"
  ditto "$DMG_APP_PATH" "$DMG_STAGE/$APP_NAME.app"
  ln -s /Applications "$DMG_STAGE/Applications"
  write_first_launch_guide
  hdiutil create -volname "$APP_NAME" -srcfolder "$DMG_STAGE" -ov -format UDZO "$DMG_PATH"
  (
    cd "$ARTIFACT_DIR"
    shasum -a 256 "$(basename "$DMG_PATH")" > "$(basename "$PRIVATE_DMG_CHECKSUMS")"
  )
}

main "$@"
