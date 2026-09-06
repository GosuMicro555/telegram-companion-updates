# Telegram Companion

Ubuntu and macOS Wails desktop application for managed Telegram user accounts,
scout collection, deterministic keyword analytics, moderated keyword lists,
and rate-limited replies.

This repository contains the application source, public update feed (`appcast.xml`), and signed license revocation manifest. Development continues on `main`.

## Production Runtime

- gotd runs all accounts in the desktop process. Python/Telethon is not a
  production dependency.
- `data/app.db` is the single SQLite database for settings, account roles,
  catalogs, encrypted scout messages, analytics, imports, and backup history.
- Scout accounts are receive-only. Collection starts at activation and does
  not request message history. Sender identity is removed before encrypted
  persistence.
- Only active `spammer` accounts can send. Selection is round robin and the
  final adapter checks the role again, reads the latest shared reply, enforces
  at most 19 messages per minute and at least two seconds between sends, and
  isolates FloodWait to the affected account.
- Telegram connections are direct. Proxy configuration remains optional and
  disabled; the application does not start Tor or Snowflake.
- Session files, tdata, API credentials, databases, model payloads, imports,
  logs, backups, and acceptance message text must never be committed.

On first Task 12 startup, a legacy Bolt `data/app.db` is atomically renamed to
`data/application-state.bolt`, imported once, and replaced by SQLite.

## Toolchain

Build and verify with Go 1.26.5 or newer. Earlier 1.26 patch releases do not
pass the descriptor-relative filesystem checks used by startup validation.
CI and local verification run
heavy Go commands serially with `GOMAXPROCS=2` and `-p=1`. Node 22, Wails
2.13.0, and golangci-lint 2.11.4 are used in CI.

```bash
make test
make test-race
make verify-model
make lint
make build-linux   # Ubuntu
make build-macos   # macOS
make build-private-macos-arm64  # direct ARM64 DMG without Developer ID
```

The pinned model payload is fetched separately and verified against
`models/manifest.json`; it is never committed or cached by CI.

## Architecture

- `internal/domain`: entities and ports
- `internal/usecase`: automation, selection, analytics, imports, retention
- `internal/repository/sqlite`: the production persistence adapters
- `internal/telegram/gotd`: session clients, gap-aware updates, catalog gates,
  receive-only scout adapter, and outbound adapter
- `internal/transport/wails`: fully injected desktop bindings
- `internal/service`: encryption, keyring, export, and verified backups

See [restore operations](docs/operations/restore-backup.md) for the verified
backup promotion procedure.

## Release Operations

- [macOS ARM64 release](docs/operations/macos-arm64-release.md)
- [Windows license generator](docs/operations/windows-license-generator.md)
- [license revocation operations](docs/operations/license-revocation.md)
