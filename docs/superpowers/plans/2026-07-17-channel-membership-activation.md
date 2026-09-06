# Массовое подключение аккаунтов к каналам Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Подключать все Telegram-аккаунты к явно активированному каналу, корректно учитывать заявки на модерацию и показывать агрегированный статус.

**Architecture:** Существующая таблица `account_channel_memberships` остаётся источником истины. `CatalogService` создаёт выключенные каналы и при активации формирует намерения членства для всех аккаунтов; Telegram-реестр выполняет и периодически сверяет эти намерения, а Wails DTO агрегирует состояния для UI.

**Tech Stack:** Go 1.25+, gotd/td MTProto, SQLite, Wails, React/TypeScript, Vitest.

## Global Constraints

- Все роли Telegram-аккаунтов вступают в активированный канал.
- Новый канал выключен по умолчанию.
- Выключение не покидает Telegram-группу.
- `INVITE_REQUEST_SENT` означает ожидание модерации, а не ошибку.
- Закрытая личка заменяется ответом в комментарии с актуальным публичным текстом.

---

### Task 1: Модель и хранение членства

**Files:**
- Modify: `internal/domain/channel.go`
- Modify: `internal/repository/sqlite/migrations.go`
- Modify: `internal/repository/sqlite/production_store.go`
- Test: `internal/repository/sqlite/catalog_store_test.go`

**Interfaces:**
- Produces: статусы `joining`, `pending_approval`, `member`, `flood_wait`, `error`, `paused`; выборки членства по каналу и аккаунту.

- [ ] Добавить падающие тесты расширенного CHECK и сохранения всех состояний.
- [ ] Добавить миграцию пересоздания `account_channel_memberships` с новым CHECK.
- [ ] Добавить методы списка членств по каналу и активных намерений аккаунта.
- [ ] Запустить `go test ./internal/repository/sqlite` и получить PASS.

### Task 2: Активация канала для всех аккаунтов

**Files:**
- Modify: `internal/usecase/catalogs.go`
- Test: `internal/usecase/catalogs_test.go`

**Interfaces:**
- Consumes: репозиторий аккаунтов и членства из Task 1.
- Produces: `CatalogService.Toggle(..., true)`, создающий `joining` для каждого аккаунта независимо от роли.

- [ ] Проверить тестом, что `AddLinks` создаёт `active=false`, `status=paused`.
- [ ] Проверить тестом создание намерений для spammer и scout.
- [ ] Реализовать активацию и сохранение намерений; при выключении не удалять членство.
- [ ] Запустить `go test ./internal/usecase` и получить PASS.

### Task 3: MTProto join и ожидание модерации

**Files:**
- Modify: `internal/telegram/gotd/sender.go`
- Modify: `internal/telegram/gotd/updates.go`
- Create: `internal/telegram/gotd/membership_coordinator.go`
- Test: `internal/telegram/gotd/sender_test.go`
- Test: `internal/telegram/gotd/updates_test.go`

**Interfaces:**
- Consumes: активные намерения членства.
- Produces: подключение любой роли, `pending_approval` для `INVITE_REQUEST_SENT`, повторная проверка без повторной заявки.

- [ ] Добавить падающие тесты прямого вступления, заявки и повторного подключения сессии.
- [ ] Отделить регистрацию sender от выполнения членства, чтобы join работал для обеих ролей.
- [ ] Реализовать координатор и безопасное расписание проверок 15м/1ч/6ч/сутки.
- [ ] Запустить `go test ./internal/telegram/gotd` и получить PASS.

### Task 4: Агрегированный DTO и подтверждение UI

**Files:**
- Modify: `internal/transport/wails/bindings.go`
- Modify: `frontend/src/catalogs.ts`
- Modify: `frontend/src/i18n.ts`
- Modify: `frontend/src/App.tsx`
- Modify: `frontend/src/styles.css`
- Test: `frontend/src/catalogs.test.ts`
- Test: `frontend/src/App.test.tsx`

**Interfaces:**
- Produces: `CatalogEntryDTO` с количествами `member`, `pendingApproval`, `joining`, `failed`, русским агрегированным статусом и подтверждением активации.

- [ ] Добавить падающие тесты локализации, агрегата и отмены/подтверждения.
- [ ] Расширить DTO и чистые helper-функции отображения статуса.
- [ ] Добавить подтверждение только при переходе `off -> on`.
- [ ] Запустить frontend tests и получить PASS.

### Task 5: Регрессия fallback и полная проверка

**Files:**
- Modify: `internal/telegram/gotd/outbound_test.go`

**Interfaces:**
- Verifies: закрытая личка использует последнюю версию `SharedReply`.

- [ ] Добавить регрессионный тест изменения публичного ответа между ошибкой ЛС и fallback.
- [ ] Запустить `go test ./...`.
- [ ] Запустить frontend tests и production build.
- [ ] Установить сборку в Ubuntu, проверить запуск Tor и подключение аккаунтов без отправки сообщений в Telegram.
