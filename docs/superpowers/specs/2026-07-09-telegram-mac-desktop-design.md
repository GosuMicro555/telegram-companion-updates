# Telegram Mac Desktop Companion Design

## Goal

Build a macOS-first desktop application, tested initially on Ubuntu, that connects multiple Telegram user accounts from Telegram Desktop `tdata`, joins and manages multiple channels/groups, watches discussions for global keywords, and sends playful automated responses in comments and/or private messages.

The application must be a standalone desktop app, not a browser dashboard.

## Primary Language and Localization

Russian is the primary UI language.

The app must be designed with localization from the start:

- `ru` is the default locale.
- `en` is supported by the same translation mechanism.
- User-facing statuses, menu labels, validation errors, and settings labels must not be hardcoded in business logic.

Initial Russian navigation labels:

- `Каналы`
- `Аккаунты`
- `Ключевые слова`
- `Статистика`
- `Настройки`

## Product Scope

### In Scope

- macOS-first glass-style desktop UI.
- Ubuntu desktop build and test environment.
- Multiple Telegram user accounts.
- Main account import path through passwordless Telegram Desktop `tdata`.
- Import from `.rar` archives and extracted `tdata` folders.
- Channel/group bulk import from pasted links.
- Automatic membership check for every connected account.
- Automatic join for every connected account when it is not already a member.
- Main screen centered on channel/group management.
- Global keyword rules shared across all channels.
- Public replies in discussion/comment chats.
- Automatic private messages triggered by keywords in approved discussions.
- Round-robin account selection for outgoing messages.
- Per-account proxy configuration so different Telegram accounts can use different outbound IP addresses.
- Global `START / STOP` control for listeners and message sending.
- Channel-level active/paused toggle.
- PostgreSQL persistence.
- Redis-backed queue/rate coordination.
- Docker Compose for local infrastructure.
- Structured logging.
- Graceful shutdown.
- Unit tests and integration-test-friendly boundaries.

### Out of Scope for MVP

- Browser dashboard as the primary UI.
- Per-channel keyword sets.
- Mass direct messaging from arbitrary user lists.
- Message generation by LLM.
- Cloud multi-tenant hosting.
- Admin web panel.
- Automatic bypass of Telegram limits or restrictions.

## Recommended Technical Approach

Use Wails with Go for the desktop shell and application backend.

Reasoning:

- The app remains a real desktop application on macOS and Linux.
- Go can host the application core, Telegram workers, queues, and persistence logic.
- The UI can be built with modern frontend tooling while still shipping as a desktop app.
- The backend core can be extracted into a daemon in a future version if needed.

Telegram integration must use MTProto user-account APIs. Bot API is not suitable for acting as user accounts in discussions or private messages.

The preferred Go MTProto client family is `gotd/td`. The final implementation plan must verify the exact current `tdata` import path and compatibility before coding the importer.

## Architecture

```text
Wails Desktop App
+-- Glass UI
|   +-- Channels
|   +-- Accounts
|   +-- Keywords
|   +-- Stats
|   +-- Settings
+-- Go Application Core
|   +-- app lifecycle
|   +-- account manager
|   +-- tdata importer
|   +-- channel manager
|   +-- membership/join manager
|   +-- keyword rules engine
|   +-- Telegram listeners
|   +-- reply/DM scheduler
|   +-- rate limiter
|   +-- stats collector
+-- Local Infrastructure
    +-- PostgreSQL
    +-- Redis
```

The Go code must follow Clean Architecture:

```text
cmd/
internal/
    app/
    config/
    domain/
    logger/
    repository/
    service/
    telegram/
    transport/
    usecase/
pkg/
configs/
migrations/
scripts/
deploy/
```

Dependency direction:

- `domain` contains entities, value objects, and interfaces.
- `usecase` orchestrates business actions.
- `repository` implements persistence interfaces.
- `telegram` implements Telegram-specific gateways.
- `transport` exposes Wails bindings and local APIs to UI.
- `app` wires dependencies, lifecycle, workers, and graceful shutdown.

Telegram client construction must be account-aware. Every account client is created with its own session, rate state, and proxy settings.

## UI Design

The visual style should be macOS/iOS inspired:

- translucent glass panels;
- restrained gradients and blur;
- compact sidebar;
- dense but readable tables;
- clear status chips;
- native-feeling toggles and segmented controls;
- no browser-dashboard feel.

### Main Screen: Channels

The default screen is `Каналы`.

Channel table columns:

- title/avatar when available;
- link or username;
- type: `channel`, `group`, `supergroup`, `discussion`;
- status;
- account membership count, for example `3/3`;
- number of sent replies/messages;
- last activity;
- active/paused toggle.

Allowed channel statuses:

- `ready`
- `joining`
- `partial`
- `error`
- `flood wait`
- `paused`

The UI may display localized labels, but internal status codes must stay stable.

### Sidebar

Sidebar sections:

- `Каналы`
- `Аккаунты`
- `Ключевые слова`
- `Статистика`
- `Настройки`

### Global Controls

The app must have a prominent `START / STOP` control.

`START`:

- starts Telegram listeners;
- enables keyword processing;
- enables outgoing public replies and private messages;
- starts queue workers.

`STOP`:

- stops Telegram listeners;
- prevents new outgoing replies and DMs;
- lets in-flight operations finish or cancel by context according to graceful shutdown rules;
- does not delete channel/account/rule configuration.

Channel import and membership checks are separate manual operations and do not require the automation system to be running.

## Account Management

Accounts are imported from passwordless Telegram Desktop `tdata`.

Input formats:

- `.rar` archive containing `tdata`;
- extracted `tdata` folder.

The real `tdata` integration phase must account for the external Telegram Desktop workflow described in the provided guide:

- use the latest Telegram Desktop Portable build for compatibility checks;
- expect version-related compatibility issues and surface them clearly;
- support a manual verification path where `tdata` is placed into a portable Telegram Desktop folder;
- require other Telegram clients for the same account to be closed during verification/import;
- verify that the expected Telegram Desktop data files are present before attempting import;
- apply the selected account proxy before opening or validating the account when a manual portable check is required.

Importer behavior:

- copy/extract into a temporary workspace;
- validate Telegram Desktop data structure;
- import session into the app session store;
- persist account metadata;
- remove temporary extracted files;
- never log raw session data, auth keys, or sensitive local paths.

The app must display connected accounts with:

- display name when available;
- phone masked;
- username when available;
- assigned proxy profile;
- detected proxy health/status;
- status: active, paused, error, limited, flood wait;
- number of public replies sent;
- number of DMs sent;
- last activity;
- last error summary.

Session files must be stored outside the repository and excluded from backups/artifacts by default. The implementation must support an application-specific data directory on macOS and Linux.

## Channel Management

Users can paste a bulk list of channel/group links, for example 100 lines.

Accepted input:

- `@username`
- `https://t.me/name`
- public channel/group links
- invite links where technically supported by the selected MTProto library

Import behavior:

1. Normalize links.
2. Deduplicate links.
3. Resolve Telegram entity.
4. Fetch title and type.
5. Save channel/group record.
6. For every connected account, check membership.
7. If an account is not a member, enqueue a join operation.
8. Update membership status per account.

Every connected account should attempt to join every imported channel/group.

The application must maintain a list of which accounts are members of which channels/groups.

Join operations must be rate-limited and retried with backoff. FloodWait must pause affected account/channel work without stopping the whole app.

## Keywords and Actions

Keywords are global across all channels.

Each rule contains:

- keyword or match expression;
- enabled flag;
- public reply text;
- private message text;
- action mode:
  - public reply only;
  - private message only;
  - both public reply and private message.

MVP matching can be case-insensitive substring matching. The domain model should allow future expansion to regex or advanced matchers without changing storage of historical events.

Responses are editable in the UI.

The app must prevent empty enabled rules that have no action text for their selected action mode.

## Message Processing

The app listens to comments/discussions for explicitly added and active channels/groups.

When a new message arrives:

1. Verify the automation is running.
2. Verify the channel/group is active.
3. Ignore messages from connected own accounts.
4. Match global keyword rules.
5. Create one or more outgoing jobs according to rule action mode.
6. Enqueue jobs for the scheduler.
7. Persist event and rule match metadata for stats/debugging.

Outgoing jobs:

- public reply in the discussion/comment chat;
- private message to the triggering user.

Private messages are only triggered by messages observed in explicitly added channels/groups. The app must not support arbitrary mass DM lists in MVP.

## Account Selection

Outgoing message jobs use round-robin selection across eligible active accounts.

Eligibility checks:

- account is active;
- account is a member of the relevant channel/group when needed;
- account is not currently under FloodWait/cooldown;
- account has not exceeded per-account limits;
- account can resolve/send to target peer.

If no account is eligible, the job is delayed with a bounded retry policy and the UI shows a degraded status.

## Rate Limits

Hard product limits:

- maximum 19 outgoing messages per minute;
- never send more often than one message every 2 seconds.

The implementation should use layered rate limiting:

- global limiter: one outgoing message every 2 seconds;
- per-account limiter: no more than 19 messages per minute;
- separate cautious limiter for private messages;
- Telegram FloodWait/cooldown limiter per account;
- join-operation limiter per account.

The UI may allow lowering limits, but must not allow values above the hard maximums.

## Statistics

The app must show statistics for:

- public replies sent per account;
- private messages sent per account;
- total outgoing messages per channel/group;
- rule trigger counts;
- failed sends by category;
- join successes/failures;
- last activity per channel/account.

Stats should be derived from persisted events so that restarting the app does not reset historical counters.

## Persistence Model

Core entities:

- `Account`
- `AccountSession`
- `Channel`
- `ChannelMembership`
- `KeywordRule`
- `IncomingMessageEvent`
- `OutgoingMessageJob`
- `OutgoingMessageEvent`
- `JoinJob`
- `RateLimitState`
- `AppSetting`

PostgreSQL stores durable state.

Redis stores queue coordination, short-lived locks, and distributed limiter state. Even in single-instance desktop mode, Redis keeps the architecture ready for a future daemon split.

## Configuration

Configuration must use `.env` and typed config loading.

Expected configuration groups:

- application environment;
- database DSN;
- Redis DSN;
- Telegram API ID/API hash;
- session storage path;
- log level;
- default locale;
- rate limit defaults;
- proxy defaults.

Secrets must not be committed.

## Proxy Support

The design must support SOCKS5 and HTTP proxies.

Proxy can be configured:

- globally;
- per account.

Per-account proxy support is part of the MVP data model and Telegram client factory.

Each proxy profile contains:

- display name;
- protocol: SOCKS5 or HTTP;
- host;
- port;
- optional username;
- optional password;
- enabled flag;
- last health check status;
- last health check time;
- last error summary.

Account settings contain:

- optional assigned proxy profile;
- proxy mode:
  - use assigned proxy;
  - use global default proxy;
  - direct connection.

The app must provide a proxy test action before assigning a proxy to an account. The test should verify TCP connectivity and, where practical, external IP visibility through the proxy. Proxy passwords must be encrypted or stored in the OS/application secret store when available and must never be logged.

Operational guidance:

- one proxy can be assigned to one or more accounts, but the UI should make shared usage visible;
- the app should warn when multiple accounts share the same proxy because Telegram will see the same outbound IP;
- failed proxy health should pause connection attempts for affected accounts instead of falling back to direct connection silently;
- reconnect/backoff must be applied per account and per proxy.

## Error Handling

Telegram-specific errors must be categorized:

- FloodWait;
- PeerFlood;
- privacy restricted;
- account limited;
- invite invalid/expired;
- join requires approval;
- channel private/inaccessible;
- user unavailable;
- send failed;
- reconnect needed.

The app must show user-safe error summaries in the UI and write structured diagnostic logs without leaking session secrets.

## Graceful Shutdown

The app must use `context.Context` throughout backend flows.

On shutdown:

- stop listeners;
- stop accepting new outgoing jobs;
- cancel or finish active jobs according to context deadline;
- flush stats/events;
- close Telegram clients;
- close database/Redis connections.

## Testing Strategy

Unit tests:

- keyword matching;
- rule validation;
- round-robin account selection;
- rate limiter behavior;
- status derivation;
- channel link normalization;
- stats aggregation.

Integration tests:

- repository migrations;
- queue scheduling behavior with fake Telegram gateway;
- join workflow with fake Telegram gateway;
- outgoing public reply/DM workflow with fake Telegram gateway.

Manual verification:

- import `tdata` account;
- import 100 channel links;
- verify channel titles;
- verify all accounts attempt membership;
- start automation;
- trigger public reply keyword;
- trigger DM keyword;
- stop automation and verify no new outgoing messages.

## Security Rules

- Do not log auth keys, session payloads, raw `tdata`, full phone numbers, or private message contents by default.
- Delete temporary extracted `tdata` files after import.
- Store sessions in app data directory, not inside repository.
- Keep `.env` out of git.
- Require explicit user-added channels/groups before automation can observe or message users.
- Private messages only fire from keyword events in approved discussions.
- Surface Telegram restrictions instead of trying to bypass them.

## Implementation Phases

1. Create project skeleton, config, logger, lifecycle, Makefile, Dockerfile, Docker Compose, migrations, and README.
2. Implement domain model, repositories, and migrations.
3. Implement account import abstraction and verify `tdata` import path with the selected MTProto library.
4. Implement channel import, entity resolution, membership tracking, and join queue.
5. Implement keyword rules and validation.
6. Implement listener pipeline using fake Telegram gateway first, then real gateway.
7. Implement outgoing scheduler with round-robin and rate limits.
8. Implement stats/event aggregation.
9. Implement Wails glass UI with Russian default localization and English translations.
10. Implement end-to-end manual verification on Ubuntu.
11. Prepare macOS build notes.

## Open Implementation Decisions

These are design decisions already resolved for MVP:

- Desktop app, not browser UI.
- Mac-first UI, Ubuntu test environment.
- `tdata` is the primary login method.
- `tdata` archives are passwordless.
- All imported accounts join all imported channels/groups.
- Keywords are global.
- Outgoing account selection is round-robin.
- Public replies and automatic private messages are both supported.
- Russian is the primary language with English support planned from the start.

The implementation plan must still verify the exact `tdata` import library/API before coding because Telegram Desktop session internals are not a stable public API.
