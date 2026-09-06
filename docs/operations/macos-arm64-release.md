# Выпуск macOS ARM64

Краткая инструкция для публичного канала `public-macos-arm64`.

## Ограничения и ключи

- Поддерживаются только Mac с процессором Apple M-серии и macOS 13+.
- Первичный нотарифицированный `.dmg` передаётся пользователю напрямую. В release-only GitHub repo исходного кода нет.
- Последующие версии публикуются в GitHub Releases и устанавливаются через Sparkle. Appcast хранится в том же release-only репозитории.
- Лицензионный Ed25519-ключ и Sparkle signing key независимы. Лицензионный private key используется только Windows-генератором; Sparkle private key хранится только на машине выпуска или в защищённом хранилище. Ни один private key не помещается в GitHub, репозиторий или артефакты.
- Developer ID Application и credentials нотарификации хранятся в macOS Keychain. Не вставляйте значения ключей и токенов в команды, логи или документацию.

## Подготовка Mac

1. Работайте на реальном Mac M-серии с macOS 13+ и актуальными Xcode Command Line Tools, Go, Node.js и Wails 2.
2. Проверьте доступ к сертификату Developer ID Application и `notarytool` через Keychain. Убедитесь, что Sparkle signing key доступен локально, а в build metadata указан только лицензионный public key и Sparkle public key.
3. Выполните preflight из `scripts/release/macos-arm64/`. Проверьте чистый каталог вывода, версию, канал `public-macos-arm64`, архитектуру `arm64`, минимальную macOS 13 и pinned resource manifest.

Sparkle, Tor и Snowflake не устанавливаются вручную и не берутся из `latest`: release-скрипты скачивают версии из `resources/macos-arm64/payload-manifest.json`, сверяют SHA-256 и прекращают сборку при любом несовпадении.

## Переменные выпуска

Перед сборкой задайте локально, не сохраняя значения в Git или shell history:

- `PUBLIC_VERSION` - числовая версия из трёх частей без префикса `v`, например `0.7.0`;
- `LICENSE_PUBLIC_KEY` - стандартный Base64 от 32-байтового Ed25519 public key лицензий;
- `APPCAST_URL` - публичный HTTPS URL файла `appcast.xml`;
- `SPARKLE_PUBLIC_ED_KEY` и `SPARKLE_PRIVATE_KEY_FILE` - отдельная пара Sparkle;
- `REVOCATION_MANIFEST_URL` - строго `https://raw.githubusercontent.com/GosuMicro555/telegram-companion-updates/main/revocations.tcrev`;
- `REVOCATION_KEY_ID` - строго `revocation-2026-01`;
- `REVOCATION_PUBLIC_KEY` - стандартный Base64 от отдельного 32-байтового Ed25519 public key отзывов;
- `APPLE_CODESIGN_IDENTITY` и `APPLE_NOTARY_KEYCHAIN_PROFILE` - подпись и профиль нотарификации из Keychain;
- `RELEASE_DOWNLOAD_BASE_URL` - HTTPS-адрес каталога assets конкретного GitHub Release без завершающего `/`;
- `GH_TOKEN` - токен с минимально необходимым доступом только к release-only репозиторию;
- `RELEASE_REPOSITORY` и `APPCAST_REPOSITORY` - репозитории в формате `owner/repository`; они могут указывать на один release-only репозиторий;
- `APPCAST_BRANCH` - ветка публикации `appcast.xml`, по умолчанию `main`.

Проверочная сборка выполняется командой `make build-public-macos-arm64`. Создание полного выпуска выполняется отдельной командой и не выполняет внешнюю публикацию:

```bash
make release-public-macos-arm64
```

Только после проверки созданных артефактов выполните публикацию:

```bash
RELEASE_PUBLISH_CONFIRM=publish make publish-public-macos-arm64
```

Публикация закрыта по умолчанию без `RELEASE_PUBLISH_CONFIRM=publish`. В GitHub Release загружаются только Sparkle ZIP, `SHA256SUMS` и подпись обновления; скрипт выполняет readback assets и сверяет SHA-256, а `appcast.xml` публикуется последним. DMG остаётся локальным артефактом для прямой передачи.

## Выпуск версии N

1. Соберите Wails-приложение локально на этом Mac как `darwin/arm64`; включите только ARM64-ресурсы, модель и нужные runtime-файлы.
2. Проверьте, что публичная сборка содержит правильные product ID, канал, appcast URL и публичные ключи, а не private keys или license tokens.
3. Подпишите вложенные исполняемые файлы и framework изнутри наружу. Затем подпишите `.app` сертификатом Developer ID Application с Hardened Runtime и timestamp.
4. Создайте Sparkle update archive и первоначальный `.dmg`. Update archive подпишите отдельным Sparkle EdDSA-ключом.
5. Подпишите DMG сертификатом Developer ID Application, отправьте `.app`/`.dmg` на нотарификацию через `notarytool`, дождитесь успешного результата и прикрепите ticket (`stapler`).
6. На чистом пользовательском профиле проверьте Gatekeeper, подпись, нотарификацию, запуск, архитектуру вложенных файлов и активацию корректной лицензией.
7. Первичный `.dmg` передайте пользователю напрямую. Не требуется и не следует помещать его в release-only репозиторий.
8. Для обновлений загрузите в GitHub Release все assets версии N: полный Sparkle archive, подписи, checksums, release notes и при необходимости delta. Полный archive обязателен как fallback.
9. Проверьте каждый asset и только после этого обновите appcast последним. Незавершённый набор assets не должен попасть в appcast.

## Проверка обновления N -> N+1

1. На реальном Mac установите лично переданный нотарифицированный `.dmg`, пройдите Gatekeeper без обхода защиты и активируйте лицензию этого Mac.
2. Убедитесь, что лицензия переживает перезапуск и пользовательский каталог остаётся вне `.app`.
3. Опубликуйте тестовый выпуск N+1 в release-only GitHub repo: сначала assets, затем appcast. Не добавляйте исходники или секреты.
4. В приложении нажмите ручную кнопку **Проверить обновления**. Проверка должна читать HTTPS appcast, увидеть только более новую версию канала `public-macos-arm64`, а загрузка должна начаться только после действия пользователя.
5. Дождитесь проверки архива, нажмите **Перезапустить и обновить**, подтвердите замену `.app` через Sparkle и запуск версии N+1.
6. Проверьте сохранность базы, настроек, `tdata`, Telegram-сессий и лицензии, а также однократный показ русских release notes.
7. Подтвердите, что обновление не запускает Telegram-автоматизацию. Финальные native-проверки подписи, нотарификации, Gatekeeper, Sparkle и реального обновления выполняются на Mac, а не только в CI.

## Приватный DMG без Developer ID

Этот вариант предназначен для прямой передачи приложения нескольким известным пользователям. Он не требует сертификата Apple или нотарификации. Приложение сохраняет канал `public-macos-arm64`, активацию и проверку Sparkle-обновлений.

Для локальной private сборки нужны `PUBLIC_VERSION`, `LICENSE_PUBLIC_KEY`, `APPCAST_URL`, `SPARKLE_PUBLIC_ED_KEY`, `REVOCATION_MANIFEST_URL`, `REVOCATION_KEY_ID` и `REVOCATION_PUBLIC_KEY`. Apple credentials, GitHub token и Sparkle private key не нужны:

```bash
make build-private-macos-arm64
```

Результат находится в `build/release/macos-arm64-private/artifacts/`:

- `Telegram-Companion-<версия>-arm64-private.dmg` для прямой передачи;
- `PRIVATE-DMG-SHA256SUMS` для проверки полученного DMG;
- ARM64 ZIP приложения, который используется только при подготовке подписанного Sparkle-обновления.

В DMG находятся приложение, ссылка `Applications` и файл `ПЕРВЫЙ ЗАПУСК.txt`. Получатель сверяет SHA-256, переносит `.app` в `/Applications`, пробует запустить двойным кликом, затем при блокировке открывает **Системные настройки -> Конфиденциальность и безопасность** и нажимает **Всё равно открыть**. Других действий от получателя не требуется. Если штатное подтверждение запуска недоступно, передачу артефакта останавливают до исправления сборки.

Локальный private target ничего не публикует. Установленная сборка использует тот же HTTPS appcast и тот же Sparkle public key, поэтому получает только последующий, отдельно подготовленный и подписанный выпуск. Публикация выполняется исключительно проверенными public release targets после создания release-артефактов; несуществующие private publish targets использовать нельзя.

Отзыв может исполнять только финальная сборка 0.8.3 с поддержкой отзывов или более новая. Версия сама по себе не доказывает наличие этой возможности: друг получает только финальный проверенный DMG и его checksum. Поскольку Apple не подтверждает ad-hoc сборку, обновление N -> N+1 обязательно проверяется на реальном Mac. Если Sparkle не сможет заменить приложение, пользователь устанавливает новый private DMG поверх старой `.app`; база, лицензия и Telegram-сессии остаются в `~/Library/Application Support/Telegram Companion/`.

## Если выпуск прерван

- Ошибка подписи, нотарификации, проверки архива или asset блокирует публикацию appcast. Исправьте выпуск и повторите проверки.
- Перед повторной полной сборкой той же версии очистите только созданный каталог `build/release/macos-arm64`; preflight намеренно не смешивает старые и новые артефакты. Уже загруженные assets можно безопасно заменить повторным запуском: после загрузки скрипт скачивает их обратно и сверяет SHA-256 до публикации appcast.
- Повторная публикация идентичного `appcast.xml` завершается успешно без лишнего коммита; обновление файла выполняется через GitHub API с `GH_TOKEN`.
- Ошибка сети или Sparkle не должна удалять текущую `.app` или пользовательские данные; повторите проверку позже.
- Не заменяйте лицензионный ключ ключом Sparkle и не публикуйте private key, расшифрованные резервные копии или license tokens.
