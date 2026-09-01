# Telegram Integration Notes

## Current State

The first MVP slice uses `internal/telegram/fake` to prove application flows without real Telegram traffic.

## Real Gateway Requirements

The real gateway must implement `domain.TelegramGateway`:

- `ResolveChannel`
- `CheckMembership`
- `JoinChannel`
- `SendPublicReply`
- `SendPrivateMessage`

It should also provide listener support in the next plan through a new event source interface.

## tdata Import

The next plan must verify `gotd/td` session import support for Telegram Desktop `tdata`, especially the `session/tdesktop` package and current API shape.

### Import path and conversion flow

- Import `tdata` using `session/tdesktop`:
  - `tdesktop.Read(root string, passcode []byte)` for a filesystem path.
  - `tdesktop.ReadFS(...)` for custom filesystem abstraction.
- For passwordless `tdata`, pass `nil` to `Read`/`ReadFS`.
- Convert the imported account via:
  - `session.TDesktopSession(account tdesktop.Account)`.
- Persist the converted session through:
  - `session.Loader{Storage: ...}.Save(...)`.

### Import flow requirements

- Accept `.rar` archives and extracted folders.
- Extract archives to a temporary directory before import.
- Import into the app session store.
- Delete temporary files after import.
- Do not log session data.

### Error handling

Surface and map the following errors to actionable user messages:

- `ErrKeyInfoDecrypt`
- `ErrNoAccounts`
- Telegram `tdata` version / layout mismatch.
## Manual Telegram Desktop Portable Check

For user-facing validation:

- Use the latest Telegram Desktop Portable build for compatibility checks.
- Put the provided `tdata` folder in Telegram Desktop Portable directory before verifying.
- Close all other Telegram clients using the same account.
- Configure the intended proxy before opening and validating the account in Telegram Desktop Portable.
- Confirm expected Telegram Desktop data files are present before import.

## Proxy Requirements

- Every real Telegram client must be built per account with explicit account session path, assigned proxy profile/direct mode, per-account reconnect/backoff, and per-account proxy injection via per-account gotd client options/resolver/dialer (no global env fallback).
- Never silently fall back to direct mode when proxy mode is explicitly assigned.

## Safety Requirements

- Respect FloodWait and PeerFlood conditions.
- Keep max 19 messages/minute and 2 seconds between outgoing messages.
- Private messages only fire from keyword events in explicit channels/groups.
