# Отзыв лицензий

Этот runbook относится только к финальной сборке 0.8.3 с поддержкой отзывов
или более новой. Более ранний кандидат с тем же номером версии нельзя считать
удалённо отключаемым.

## Границы доверия

- Manifest публикуется только по фиксированному URL
  `https://raw.githubusercontent.com/GosuMicro555/telegram-companion-updates/main/revocations.tcrev`.
- Идентификатор ключа — `revocation-2026-01`; лицензионный, Sparkle и
  revocation Ed25519-ключи должны быть разными.
- Публичный manifest содержит только domain-separated необратимые handles,
  последовательность и подпись. В нём не должно быть имён, Machine ID,
  license token, seed, заметок, credentials или private key.
- Отзыв опубликованного License ID необратим. Возврат доступа выполняется
  выпуском новой лицензии с новым License ID.
- Лицензии пользователя или друга автоматически не отзываются. Сквозная
  проверка может использовать только специально созданную одноразовую тестовую
  лицензию.

## Первичное создание trust root

1. До внешней записи завершить тесты, secret scans и независимые reviews.
2. В Windows Generator открыть существующее защищённое signing state. Новый
   revocation key допустимо создать только после точного anonymous HTTP 404 по
   фиксированному URL; другая ошибка, redirect или ответ закрывают операцию.
3. Экспортировать `TCPKEYBACKUP3` в новый файл, перечитать его и выполнить
   offline restore validation. Неинтерактивный CLI принимает пароль только через environment variable, а не аргумент командной строки, лог или echo. Поле пароля в GUI допустимо, но его нельзя сохранять, логировать или выводить через echo.
4. Сохранить отдельно только `key_id` и public key для build inputs. Private
   material остаётся в DPAPI state и зашифрованном backup.
5. Опубликовать подписанный пустой manifest `seq0` через compare-and-swap.
6. Признать инициализацию успешной только после authenticated и anonymous
   exact-byte readback. При неоднозначном результате оставить состояние
   pending и повторить reconciliation, не создавая другой ключ.
7. Зафиксировать SHA-256 backup/public export/manifest и sequence без вывода
   секретов.

Неинтерактивный provisioning использует только имена secret variables:

```powershell
LicenseGenerator.exe --provision-revocation `
  --backup-output <new-or-matching.tcompkeybackup> `
  --public-output <new-or-identical.json> `
  --password-env TC_LICENSE_GENERATOR_REVOCATION_BACKUP_PASSWORD `
  --credential-env TC_LICENSE_GENERATOR_GITHUB_TOKEN
```

## Отзыв одной лицензии

1. Обновить registry и выбрать точную запись по владельцу, сроку и License ID.
2. Прочитать подтверждение: операция необратима, а восстановление требует новой
   лицензии. Сверить владельца и срок ещё раз.
3. Нажать действие отзыва один раз. Статус `Отозвана` допустим только после
   compare-and-swap и двух readback-проверок.
4. `Публикация отзыва` означает незавершённую операцию. `Ошибка публикации`
   требует retry/reconciliation; нельзя вручную редактировать sequence или
   удалять pending evidence.
5. Проверить, что финальный клиент блокирует новые операции, сохраняет профиль,
   локальную базу, Keychain и Telegram-сессии, а новая лицензия после свежей
   online-проверки возвращает доступ.

## Восстановление

- При утрате локального sidecar восстановить тот же `TCPKEYBACKUP3`; Generator
  обязан сверить ключ с уже опубликованным manifest до изменения state.
- При сетевой неопределённости сначала выполнить authenticated и anonymous
  readback. Не понижать sequence и не заменять ключ.
- Backup и его пароль хранить раздельно. Не публиковать расшифрованный backup,
  GitHub credential, license token или private key в issue, Git, release asset
  либо журнале проверки.
