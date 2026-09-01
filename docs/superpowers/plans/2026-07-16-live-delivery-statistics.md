# План реализации Live-статистики доставки

> **Для агентных исполнителей:** ОБЯЗАТЕЛЬНЫЙ ПОДСКИЛЛ: выполнять план по задачам через `superpowers:subagent-driven-development` (рекомендуется) либо `superpowers:executing-plans`. Шаги отслеживаются чекбоксами `- [ ]`.

**Цель:** добавить отдельный экран Live-статистики с бессрочной обезличенной историей триггерных отправок, финальными статусами, фильтрацией, пагинацией и общей настраиваемой таблицей статистики.

**Архитектура:** отдельная таблица `live_delivery_history` создаётся транзакционно вместе с keyword-задачей и финализируется существующим контуром доставки. Постраничный SQLite-репозиторий публикуется через Wails API; frontend опрашивает только открытую страницу раз в пять секунд. Существующая и Live-таблицы используют один generic-компонент столбцов, но отдельные сохранённые настройки.

**Стек:** Go 1.25+, SQLite/goose, Wails v2, React 19, TypeScript 7, Vitest, `slog`, существующие Clean Architecture границы.

## Глобальные ограничения

- Исходный текст хранится локально и бессрочно; имя автора, Telegram ID автора и другие идентификаторы автора не сохраняются.
- Время строки — момент срабатывания триггера, хранится в UTC и отображается по Москве.
- Видимы только финальные статусы `successful` и `not_delivered`; промежуточного статуса нет.
- Окончательная ошибка наступает после существующего лимита `8` попыток.
- Закрытая ЛС с успешной заменой на комментарий отображается как `Ответ / Успешно`.
- Опрос выполняется каждые `5` секунд только при открытом Live-разделе и видимом документе.
- Размеры страниц строго: `50`, `100`, `200`, `500`, `1000`; по умолчанию `50`.
- Крестик удаляет канон без подтверждения одновременно из `Позитивных` и активных `Ключевых слов`, но не удаляет историю.
- Размер базы включает основной SQLite-файл и WAL-файл и отображается в МБ.
- Существующий пункт меню называется `Статистика аккаунтов`; отдельный `Live-статистика` находится сразу под ним.
- Не менять работающие триггеры, горячий единый ответ, round-robin, чередование ЛС/ответа, лимиты, FloodWait и прокси-маршруты.

---

### Task 1: Домен Live-аудита и схема SQLite

**Файлы:**
- Создать: `internal/domain/live_statistics.go`
- Создать: `internal/repository/sqlite/migrations/000018_live_delivery_history.sql`
- Создать: `internal/repository/sqlite/live_statistics_store.go`
- Создать: `internal/repository/sqlite/live_statistics_store_test.go`
- Изменить: `internal/domain/ports.go`
- Изменить: `internal/repository/sqlite/job_store.go`
- Изменить: `internal/repository/sqlite/job_store_test.go`

**Интерфейсы:**
- Создаёт `domain.LiveDeliveryDraft`, `domain.LiveDeliveryQuery`, `domain.LiveDeliveryPage`, `domain.LiveDeliveryRow`.
- Создаёт `domain.LiveDeliveryStatisticsProvider.LiveDeliveryStatistics(context.Context, LiveDeliveryQuery) (LiveDeliveryPage, error)`.
- Создаёт узкий `domain.KeywordResponseEnqueuer.EnqueueKeywordResponse(context.Context, OutgoingMessageJob, LiveDeliveryDraft) error`, не расширяя общий `JobRepository`.
- `JobRepository.EnqueueKeywordResponse` атомарно вставляет задачу и скрытую audit-строку.

- [ ] **Шаг 1: написать падающий migration/repository test**

Проверить, что таблица не допускает третьего финального статуса, author ID отсутствует в схеме, а `EnqueueKeywordResponse` либо сохраняет задачу и audit вместе, либо откатывает обе записи.

```go
func TestKeywordEnqueueCreatesAnonymizedLiveAuditAtomically(t *testing.T) {
	job := domain.OutgoingMessageJob{
		ID: "job-1", Type: domain.JobKeywordResponse, ChannelID: "channel-1",
	}
	draft := domain.LiveDeliveryDraft{SourceMessage: "где взять денег", TriggerCanonicalID: "money", TriggerSnapshot: "деньги", TriggeredAt: now}
	require.NoError(t, repo.EnqueueKeywordResponse(ctx, job, draft))
	var message, trigger string
	var status sql.NullString
	require.NoError(t, db.QueryRowContext(ctx, `SELECT source_message,trigger_snapshot,final_status FROM live_delivery_history WHERE job_id=?`, job.ID).
		Scan(&message, &trigger, &status))
	require.Equal(t, "где взять денег", message)
	require.Equal(t, "деньги", trigger)
	require.False(t, status.Valid)
}
```

- [ ] **Шаг 2: запустить тест и подтвердить RED**

Выполнить: `go test ./internal/repository/sqlite -run 'TestKeywordEnqueueCreatesAnonymizedLiveAuditAtomically' -count=1`

Ожидается: FAIL, потому что таблицы и полей ещё нет.

- [ ] **Шаг 3: добавить доменные типы и миграцию**

Основной SQL:

```sql
CREATE TABLE live_delivery_history (
  id TEXT PRIMARY KEY,
  job_id TEXT NOT NULL UNIQUE,
  source_message TEXT NOT NULL,
  trigger_canonical_id TEXT REFERENCES canonical_keywords(id) ON DELETE SET NULL,
  trigger_snapshot TEXT NOT NULL,
  triggered_at TEXT NOT NULL,
  delivery_type TEXT CHECK(delivery_type IS NULL OR delivery_type IN ('private_message','public_reply')),
  account_id TEXT,
  account_title_snapshot TEXT NOT NULL DEFAULT '',
  final_status TEXT CHECK(final_status IS NULL OR final_status IN ('successful','not_delivered')),
  error_code TEXT NOT NULL DEFAULT '',
  finalized_at TEXT
);
CREATE INDEX idx_live_history_triggered ON live_delivery_history(triggered_at DESC,id DESC);
CREATE INDEX idx_live_history_status_time ON live_delivery_history(final_status,triggered_at DESC);
CREATE INDEX idx_live_history_canonical ON live_delivery_history(trigger_canonical_id);
```

В Down удалить индексы и таблицу.

- [ ] **Шаг 4: сделать транзакционный enqueue**

В `JobRepository.EnqueueKeywordResponse` начать транзакцию, вставить job, затем audit с детерминированным ID `job.ID + ":live"`. `ON CONFLICT(job_id) DO NOTHING` сохраняет идемпотентность. При пустом `SourceMessage`, `TriggerSnapshot` или `TriggeredAt` вернуть валидационную ошибку до транзакции. У `job_id` намеренно нет каскадного FK: очистка технической очереди не удаляет бессрочную историю.

- [ ] **Шаг 5: проверить GREEN и регрессию job store**

Выполнить:

```text
go test ./internal/repository/sqlite -run 'TestKeywordEnqueue|TestJobRepository' -count=1
```

Ожидается: PASS.

- [ ] **Шаг 6: закоммитить задачу**

```text
git add internal/domain internal/repository/sqlite
git commit -m "feat: persist anonymized live delivery audits"
```

---

### Task 2: Точное определение канона и финализация доставки

**Файлы:**
- Изменить: `internal/usecase/runtimeconfig/store.go`
- Изменить: `internal/usecase/runtimeconfig/store_test.go`
- Изменить: `internal/transport/wails/bindings.go`
- Изменить: `internal/transport/wails/bindings_test.go`
- Изменить: `internal/telegram/gotd/live_sink.go`
- Изменить: `internal/telegram/gotd/live_sink_test.go`
- Изменить: `internal/usecase/scheduler.go`
- Изменить: `internal/usecase/scheduler_test.go`
- Изменить: `internal/repository/sqlite/job_store.go`
- Изменить: `internal/repository/sqlite/job_store_test.go`

**Интерфейсы:**
- Создаёт `runtimeconfig.CanonicalTrigger{ID, Canonical, Forms}` и `Snapshot.CanonicalTriggers`.
- Создаёт matcher `matchCanonicalTrigger(text string, triggers []CanonicalTrigger) (CanonicalTrigger, bool)`.
- Репозиторий финализирует success в существующем `CompleteKeywordResponse` и terminal failure в новом `DelayKeywordFailure(context.Context, KeywordDeliveryFailure) error`.

- [ ] **Шаг 1: написать RED-тесты matcher и runtime snapshot**

```go
func TestMatchCanonicalTriggerReturnsOwnerForInflectedForm(t *testing.T) {
	trigger, ok := matchCanonicalTrigger("сегодня нет денег", []runtimeconfig.CanonicalTrigger{{
		ID: "money", Canonical: "деньги", Forms: []string{"деньги", "денег"},
	}})
	require.True(t, ok)
	require.Equal(t, "money", trigger.ID)
}
```

Проверить глубокое копирование `CanonicalTriggers` в `runtimeconfig.Store`.

- [ ] **Шаг 2: запустить matcher tests и подтвердить RED**

Выполнить: `go test ./internal/telegram/gotd ./internal/usecase/runtimeconfig -run 'TestMatchCanonicalTrigger|TestStore' -count=1`

Ожидается: FAIL из-за отсутствующих типов и matcher.

- [ ] **Шаг 3: опубликовать canonical descriptors в hot snapshot**

В `syncCanonicalTriggers` строить descriptors только из строк `class=positive && trigger_active=true`; каждый descriptor содержит ID, канон и нормализованные формы. Старые `Keywords` сохранить для совместимости, но `LiveSink` использует descriptors как источник ID и снимка.

- [ ] **Шаг 4: заменить bool matcher на canonical matcher**

В `LiveSink.Trigger` получить совпавший descriptor и заполнить:

```go
job.RuleID = domain.ID(trigger.ID)
draft := domain.LiveDeliveryDraft{
    SourceMessage: strings.TrimSpace(update.Text),
    TriggerCanonicalID: domain.ID(trigger.ID),
    TriggerSnapshot: trigger.Canonical,
    TriggeredAt: now,
}
```

Передать `job` и `draft` в узкий `KeywordResponseEnqueuer`. Логика определения Direct Message остаётся по существующим `DirectMessageKeywords`.

- [ ] **Шаг 5: написать RED-тесты финализации**

Проверить четыре случая: private success, public success, private closed + public success, восьмая send failure. После первых семи ошибок audit скрыт; после восьмой имеет `not_delivered`, фактический тип и код ошибки. Повторная финализация не создаёт вторую строку.

- [ ] **Шаг 6: реализовать success и terminal failure атомарно**

Расширить `KeywordDeliveryOutcome` снимком отображаемого имени аккаунта. `CompleteKeywordResponse` в той же транзакции обновляет audit:

```sql
UPDATE live_delivery_history
SET delivery_type=?,account_id=?,account_title_snapshot=?,final_status='successful',error_code='',finalized_at=?
WHERE job_id=? AND final_status IS NULL;
```

Новый `DelayKeywordFailure` увеличивает попытку и, только достигнув `maxDeliveryAttempts`, помечает job done/dead-letter и audit `not_delivered` в одной транзакции. `Scheduler` направляет через него все учитываемые задержки keyword-задачи, включая send failure, отсутствие доступного аккаунта и потерю назначенного аккаунта. Transient fallback `private_message_closed` не расходует attempts и не финализирует audit. Если задача ни разу не получила аккаунт или фактический тип доставки, соответствующие поля остаются `NULL`, а UI показывает `—`.

- [ ] **Шаг 7: запустить backend lifecycle tests**

Выполнить:

```text
go test ./internal/usecase/runtimeconfig ./internal/telegram/gotd ./internal/usecase ./internal/repository/sqlite -run 'CanonicalTrigger|LiveSink|KeywordResponse|PrivateClosed|Terminal' -count=1
```

Ожидается: PASS.

- [ ] **Шаг 8: закоммитить задачу**

```text
git add internal
git commit -m "feat: finalize live delivery outcomes"
```

---

### Task 3: Постраничная выборка, размер базы и Wails API

**Файлы:**
- Изменить: `internal/repository/sqlite/live_statistics_store.go`
- Изменить: `internal/repository/sqlite/live_statistics_store_test.go`
- Изменить: `internal/domain/live_statistics.go`
- Создать: `internal/transport/wails/live_statistics_bindings.go`
- Создать: `internal/transport/wails/live_statistics_bindings_test.go`
- Изменить: `internal/transport/wails/bindings.go`
- Изменить: `internal/transport/wails/bindings_test.go`

**Интерфейсы:**
- Wails: `GetLiveDeliveryStatistics(LiveDeliveryQueryDTO) (LiveDeliveryPageDTO, error)`.
- Wails: `DeleteLiveTrigger(canonicalID string) error`, использующий тот же доменный путь, что `DeleteCanonicalKeyword`.
- Page DTO содержит `rows`, `total`, `databaseBytes`, `refreshedAt`.

- [ ] **Шаг 1: написать RED-тесты SQL query**

Табличными тестами проверить границы `from/to`, порядок newest-first, сортировку разрешённых столбцов, страницы 50/100/200/500/1000, отклонение других размеров и исключение строк с пустым final status.

- [ ] **Шаг 2: подтвердить RED**

Выполнить: `go test ./internal/repository/sqlite -run 'TestLiveDeliveryStatistics' -count=1`

Ожидается: FAIL из-за отсутствующей выборки.

- [ ] **Шаг 3: реализовать whitelist query builder и размер базы**

Не подставлять frontend-строки напрямую в SQL. Сопоставлять допустимые поля с константами SQL. Путь к базе получить через `PRAGMA database_list`; размер вычислять как сумму `os.Stat(dbPath).Size()` и существующего `dbPath + "-wal"`. Отсутствие WAL не является ошибкой.

- [ ] **Шаг 4: написать RED-тест Wails DTO и удаления**

Проверить преобразование локального RFC3339 диапазона в UTC, точную передачу total/bytes, и что `DeleteLiveTrigger("money")` вызывает существующее удаление канона с последующим `syncCanonicalTriggers`, не удаляя audit row.

- [ ] **Шаг 5: реализовать bindings**

Валидация:

```go
var livePageSizes = map[int]struct{}{50: {}, 100: {}, 200: {}, 500: {}, 1000: {}}
```

Использовать `b.rootContext()` с таймаутом 30 секунд. Для удаления извлечь общую приватную функцию из `DeleteCanonicalKeyword`, чтобы оба публичных метода использовали один сценарий и не дублировали синхронизацию.

- [ ] **Шаг 6: запустить repository и Wails tests**

Выполнить: `go test ./internal/repository/sqlite ./internal/transport/wails -run 'LiveDelivery|DeleteLiveTrigger' -count=1`

Ожидается: PASS.

- [ ] **Шаг 7: закоммитить задачу**

```text
git add internal
git commit -m "feat: expose paged live delivery statistics"
```

---

### Task 4: Единый настраиваемый компонент таблиц статистики

**Файлы:**
- Создать: `frontend/src/ConfigurableStatsTable.tsx`
- Создать: `frontend/src/configurableStatsTable.ts`
- Создать: `frontend/src/configurableStatsTable.test.ts`
- Создать: `frontend/src/ConfigurableStatsTable.test.tsx`
- Изменить: `frontend/src/StatsView.tsx`
- Изменить: `frontend/src/StatsView.test.tsx`
- Изменить: `frontend/src/styles.css`

**Интерфейсы:**
- `ConfigurableStatsTable<Row, ColumnID>` получает definitions, rows, rowKey, settingsKey, sort и callbacks пагинации.
- Настройки `{order, visible, widths}` хранятся в `localStorage` отдельно по `stats.accounts.table.v1` и `stats.live.table.v1`.

- [ ] **Шаг 1: написать RED unit tests preferences**

```ts
test("keeps independent order, visibility and width per settings key", () => {
  const storage = new MemoryStorage();
  saveTablePreferences(storage, "stats.accounts.table.v1", { order: ["name"], visible: ["name"], widths: { name: 320 } });
  expect(loadTablePreferences(storage, "stats.live.table.v1", liveDefaults)).toEqual(liveDefaults);
});
```

Также проверить reorder, clamp минимальной/максимальной ширины и восстановление повреждённого JSON.

- [ ] **Шаг 2: подтвердить RED**

Выполнить: `npm test -- configurableStatsTable.test.ts`

Ожидается: FAIL, модуль отсутствует.

- [ ] **Шаг 3: реализовать чистые preferences helpers**

Использовать generic string IDs, whitelist текущих definitions и диапазон ширины `48..900`, совпадающий с аналитической таблицей. Не переносить analytics-specific tabs или Wails DTO.

- [ ] **Шаг 4: написать RED component test**

Проверить наличие drag handles, resize handles, visibility menu, `aria-sort`, единый grid template header/rows и вызов сортировки кликом.

- [ ] **Шаг 5: реализовать компонент и перевести StatsView**

Компонент содержит только механику таблицы; тексты, cells и бизнес-сортировка передаются definitions. Существующий экран статистики аккаунтов использует компонент без изменения своих метрик и графика.

- [ ] **Шаг 6: обеспечить внутренний скролл**

Контейнер stats screen — `min-height:0; overflow:hidden`; body таблицы — `min-height:0; overflow:auto`; header sticky. Не добавлять page-level scroll.

- [ ] **Шаг 7: запустить frontend tests**

Выполнить: `npm test -- configurableStatsTable.test.ts ConfigurableStatsTable.test.tsx StatsView.test.tsx stats.test.ts`

Ожидается: PASS.

- [ ] **Шаг 8: закоммитить задачу**

```text
git add frontend/src
git commit -m "refactor: share configurable statistics table"
```

---

### Task 5: Экран Live-статистики, навигация и polling

**Файлы:**
- Создать: `frontend/src/liveStatistics.ts`
- Создать: `frontend/src/liveStatistics.test.ts`
- Создать: `frontend/src/LiveStatisticsView.tsx`
- Создать: `frontend/src/LiveStatisticsView.test.tsx`
- Изменить: `frontend/src/App.tsx`
- Изменить: `frontend/src/i18n.ts`
- Изменить: `frontend/src/styles.css`
- Обновить генерацией: `frontend/wailsjs/go/wails/Bindings.js`
- Обновить генерацией: `frontend/wailsjs/go/wails/Bindings.d.ts`
- Обновить генерацией: `frontend/wailsjs/go/models.ts`

**Интерфейсы:**
- Новый `Section` ID: `liveStats`.
- `LiveStatisticsView` использует `ConfigurableStatsTable` и typed Wails binding.
- Polling interval: 5000 ms, активен только пока component mounted и `document.visibilityState === "visible"`.

- [ ] **Шаг 1: написать RED-тесты нормализации и форматирования**

Проверить безопасные defaults для null DTO, московские `ДД.ММ.ГГГГ` и `ЧЧ:ММ:СС`, типы `ЛС/Ответ`, статусы и преобразование `databaseBytes` в MB.

- [ ] **Шаг 2: написать RED-тест polling lifecycle**

С fake timers проверить: немедленный первый запрос, следующий через 5000 ms, отсутствие запросов при hidden document, возобновление после visibilitychange и cleanup при unmount.

- [ ] **Шаг 3: подтвердить RED**

Выполнить: `npm test -- liveStatistics.test.ts LiveStatisticsView.test.tsx`

Ожидается: FAIL, экран отсутствует.

- [ ] **Шаг 4: реализовать LiveStatisticsView**

Toolbar: два `datetime-local`, total, MB, last refresh. Таблица использует столбцы из спецификации, серверные sort/page/pageSize и newest-first default. Ошибка polling выводится inline, не очищая последнюю страницу.

Крестик вызывает `DeleteLiveTrigger`, затем обновляет текущую страницу. Удалённый канон определяется по nullable `triggerCanonicalID`; кнопка исчезает, snapshot и пометка `Удалён` остаются.

- [ ] **Шаг 5: изменить навигацию и локализацию**

Переименовать `stats` в отображении на `Статистика аккаунтов`, добавить `liveStats` сразу после него. Английские значения: `Account statistics` и `Live statistics`.

- [ ] **Шаг 6: регенерировать Wails bindings**

В Ubuntu выполнить `/home/codex/go/bin/wails build -clean -tags desktop,webkit2_41`, затем `python3 scripts/clean_wailsjs.py`. Не редактировать generated-файлы вручную.

- [ ] **Шаг 7: запустить frontend suite**

Выполнить:

```text
npm test
npm run build
```

Ожидается: все тесты и TypeScript build проходят.

- [ ] **Шаг 8: закоммитить задачу**

```text
git add frontend
git commit -m "feat: add live delivery statistics screen"
```

---

### Task 6: Интеграционная проверка и регрессионные гарантии

**Файлы:**
- Проверить без плановых изменений production-кода.

**Интерфейсы:**
- Проверяет сквозной путь `Telegram update -> trigger match -> audit -> send/final fail -> Wails page`.

- [ ] **Шаг 1: выполнить полную проверку**

```text
go test -p=1 ./...
go test -p=1 -tags desktop ./...
go test -p=1 -race ./internal/repository/sqlite ./internal/usecase ./internal/telegram/gotd ./internal/transport/wails
npm --prefix frontend test
npm --prefix frontend run build
```

Ожидается: все команды PASS без data races и TypeScript errors.

- [ ] **Шаг 2: проверить diff и статус**

```text
git diff --check
git status --short
```

Ожидается: только ожидаемые изменения либо чистое дерево после коммита.

- [ ] **Шаг 3: при обнаружении регрессии вернуться к соответствующему TDD-этапу**

Сначала добавить минимальный тест, подтвердить ожидаемый RED, затем исправить production-код и повторить весь набор команд. Не вносить исправления без воспроизводящего теста и не рефакторить несвязанные пакеты.
