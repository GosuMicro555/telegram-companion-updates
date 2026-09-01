# Per-Account Proxy Profile Routing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the user create SOCKS5/HTTP proxy profiles in Settings, assign one stable route to each Telegram account, enforce a maximum of ten accounts per route, and keep Tor/Snowflake as the default system route.

**Architecture:** SQLite stores profile metadata and account assignments; proxy passwords are AES-GCM encrypted with a master key obtained from the OS keyring. A route registry resolves each account to either the system Tor route or an enabled custom profile. The gotd client factory asks that registry for an account-specific DC resolver. Assignment changes restart only affected account workers; unhealthy routes pause only their assigned accounts and never fall back to direct Internet access.

**Tech Stack:** Go 1.26, gotd/td, golang.org/x/net/proxy, SQLite/goose, OS keyring, Wails v2, React 19, TypeScript, Vitest.

## Global Constraints

- Preserve the current three accounts on the `Tor/Snowflake` system route.
- The system route and every custom profile each have capacity `10`; an eleventh account remains unassigned/stopped with a visible warning.
- Never rotate routes automatically, fall back to direct mode, or log credentials.
- Proxy separation is a reliability feature, not a guarantee against Telegram restrictions.
- Ship the completed delivery and proxy-routing work as application version `0.5.0`; update release notes once, immediately before the final verified build.

---

### Task 1: Persist proxy profiles and encrypted credentials

**Files:**
- Modify: `internal/domain/account.go`
- Modify: `internal/domain/ports.go`
- Create: `internal/repository/sqlite/migrations/000016_proxy_profiles.sql`
- Create: `internal/repository/sqlite/proxy_profile_store.go`
- Create: `internal/repository/sqlite/proxy_profile_store_test.go`
- Create: `internal/service/crypto/proxy_credentials.go`
- Create: `internal/service/crypto/proxy_credentials_test.go`
- Modify: `main.go`

- [ ] Write failing repository tests for create/update/list/delete, duplicate endpoint validation, and assignment counts.
- [ ] Write failing cipher tests for authenticated encryption, random nonce, wrong-key rejection, and empty password handling.
- [ ] Create `proxy_profiles` with protocol check `socks5|http`, host, port, username, encrypted password, nonce, enabled state, health fields, timestamps, and unique normalized endpoint/name constraints.
- [ ] Add persisted `accounts.proxy_mode` with `global|assigned|unassigned`: `global` plus a null profile means system Tor/Snowflake, `assigned` requires a profile, and `unassigned` never starts. Preserve the existing three accounts as `global`.
- [ ] Add `ProxyModeUnassigned` and `ProxyProfileRepository` methods for CRUD, health updates, assignment counts, and an atomic `AssignAccount(mode, profileID)` capacity check. Capacity counts every persisted assignment, including paused accounts.
- [ ] Change discovered-account defaults in `main.go` from `direct` to the route assignment service: assign `global` only while system capacity remains, otherwise persist `unassigned` and show the account as stopped.
- [ ] Derive the AES-256 key with `secrets.SecretStore.GetOrCreate(ctx, "proxy-credentials-v1", 32)`; store only ciphertext and nonce in SQLite.
- [ ] Run focused SQLite and crypto tests on Ubuntu.
- [ ] Commit: `feat: persist encrypted proxy profiles`

Use a transport-neutral runtime shape; do not expose ciphertext through Wails:

```go
type ProxyRoute struct {
	ID       ID
	Name     string
	Protocol string
	Host     string
	Port     int
	Username string
	Password string
	Enabled  bool
}
```

### Task 2: Build SOCKS5 and HTTP CONNECT resolvers

**Files:**
- Modify: `internal/telegram/gotd/proxy_resolver.go`
- Modify: `internal/telegram/gotd/proxy_resolver_test.go`
- Create: `internal/proxy/routes/dialer.go`
- Create: `internal/proxy/routes/dialer_test.go`

- [ ] Add local fake SOCKS5 and HTTP CONNECT servers in tests; assert username/password negotiation and context cancellation.
- [ ] Keep `NewTorSOCKSResolverFactory` behavior for the system route.
- [ ] Add `NewProxyResolver(route ProxyRoute) (dcs.Resolver, error)`.
- [ ] For SOCKS5, use `proxy.SOCKS5` with optional `proxy.Auth`.
- [ ] For HTTP, implement CONNECT with optional Basic auth, bounded handshake deadlines, exact `200` acceptance, and no credential text in returned errors.
- [ ] Reject unsupported protocols, empty host, and invalid ports before dialing.
- [ ] Run `go test ./internal/proxy/routes ./internal/telegram/gotd -run Proxy` on Ubuntu.
- [ ] Commit: `feat: support authenticated proxy resolvers`

### Task 3: Add route registry, health checks, and route backoff

**Files:**
- Create: `internal/proxy/routes/registry.go`
- Create: `internal/proxy/routes/registry_test.go`
- Create: `internal/proxy/routes/health.go`
- Create: `internal/proxy/routes/health_test.go`
- Modify: `internal/usecase/runtimeconfig/store.go`
- Modify: `internal/usecase/runtimeconfig/store_test.go`

- [ ] Model route states `ready`, `checking`, `degraded`, `disabled`, `full` and expose safe status snapshots.
- [ ] Resolve `NULL`/`system` assignments to Tor/Snowflake; resolve custom IDs only when enabled and healthy.
- [ ] Health-check each custom route through its own proxy handshake to a Telegram endpoint with a short timeout; update status without exposing credentials.
- [ ] Apply route-level exponential backoff for transport failures and route-level 429/retry signals; do not pause unrelated routes.
- [ ] Add gradual reconnect pacing so a recovered route does not reconnect all ten accounts simultaneously.
- [ ] Publish account-to-route IDs in `runtimeconfig.Snapshot.ProxyAssignments` instead of a single `global` URL.
- [ ] Run route and runtimeconfig tests on Ubuntu.
- [ ] Commit: `feat: manage proxy route health and capacity`

### Task 4: Select a resolver per account and restart only changed routes

**Files:**
- Modify: `internal/telegram/gotd/per_account_factory.go`
- Modify: `internal/telegram/gotd/per_account_factory_test.go`
- Modify: `internal/telegram/gotd/manager.go`
- Modify: `internal/telegram/gotd/manager_test.go`
- Modify: `main.go`

- [ ] Change the factory boundary to `ResolverFor(account domain.Account) (dcs.Resolver, error)` and construct the client factory at `New(account)` time.
- [ ] Test two accounts assigned to different fake routes and two accounts sharing one route.
- [ ] Store each worker's effective route ID; when a snapshot changes it, cancel and recreate only that worker.
- [ ] If a route is unavailable/full, report a route-specific stopped/backoff status and do not create a direct resolver.
- [ ] Wire the system Tor resolver, decrypted profile registry, health runner, and manager lifecycle in `main.go`.
- [ ] Confirm STOP/START and application restart preserve assignments.
- [ ] Confirm changing spammer/scout role never changes the assigned route.
- [ ] Run `go test ./internal/telegram/gotd` on Ubuntu.
- [ ] Commit: `feat: route Telegram clients per account`

### Task 5: Expose safe Wails APIs for profiles and assignments

**Files:**
- Modify: `internal/transport/wails/bindings.go`
- Modify: `internal/transport/wails/bindings_test.go`
- Create: `internal/transport/wails/proxy_bindings.go`
- Create: `internal/transport/wails/proxy_bindings_test.go`

- [ ] Add DTOs that return profile ID/name/protocol/masked endpoint/state/count/capacity/error but never password or ciphertext.
- [ ] Add `ListProxyProfiles`, `SaveProxyProfile`, `DeleteProxyProfile`, `CheckProxyProfile`, and `AssignAccountProxy`.
- [ ] Treat an empty profile ID as the system Tor route.
- [ ] Enforce capacity and profile state in the backend even if UI validation is bypassed.
- [ ] Extend `AccountDTO` with `proxyProfileId`, `proxyRouteName`, `proxyRouteState`, `proxyRouteUsage`, and `proxyRouteCapacity`.
- [ ] Remove the current `accountDTO` shortcut that labels every managed account as Tor/Snowflake.
- [ ] Regenerate Wails JS bindings with the project Wails command on Ubuntu.
- [ ] Run Wails binding tests.
- [ ] Commit: `feat: expose proxy profile management APIs`

### Task 6: Add Settings profile manager and account assignment controls

**Files:**
- Modify: `frontend/src/App.tsx`
- Modify: `frontend/src/accounts.ts`
- Modify: `frontend/src/i18n.ts`
- Modify: `frontend/src/styles.css`
- Create: `frontend/src/proxyProfiles.ts`
- Create: `frontend/src/proxyProfiles.test.ts`

- [ ] Add a Settings section `Прокси-профили` with compact table rows, add/edit modal, protocol selector, host, port, username, password, health check, state, and `X / 10` usage.
- [ ] Never refill the password field from backend data; an empty password on edit means keep the existing secret.
- [ ] Add an account-card route selector with `Tor/Snowflake (system)` plus enabled custom profiles.
- [ ] Show route state and usage beside each account; show `Маршрут заполнен` for capacity errors.
- [ ] Disable assignment to a full/disabled route, but keep backend errors visible.
- [ ] Keep all existing account role controls and current layout behavior.
- [ ] Run `npm test -- --run proxyProfiles accounts` and `npm run build`.
- [ ] Commit: `feat: manage account proxy routes in the desktop UI`

### Task 7: Integration and failure verification

**Files:**
- Modify only if verification exposes a defect.

- [ ] Run `go test ./...` and `go test -tags desktop ./...` on Ubuntu.
- [ ] Run `npm test` and `npm run build` from `frontend`.
- [ ] Build the Linux Wails app without changing version metadata.
- [ ] Verify existing accounts start on Tor/Snowflake after a cold restart.
- [ ] Add two local test proxy profiles, assign accounts, and verify resolver selection with route-specific connection logs that contain no credentials.
- [ ] Fill a route with ten fixture accounts and verify the eleventh is rejected/stopped.
- [ ] Stop one proxy and verify only its assigned accounts pause; restore it and verify staggered reconnect.
- [ ] Verify there is no direct fallback and no assignment loss after app restart.
- [ ] Commit any verification-only fix separately; otherwise leave the verified commits unchanged.
