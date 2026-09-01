# Telegram Companion License Revocation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> `superpowers:subagent-driven-development` (recommended) or
> `superpowers:executing-plans` to execute this plan task by task. Use
> `superpowers:test-driven-development` for every production change and
> `superpowers:verification-before-completion` before any completion claim.

**Goal:** Let the owner irreversibly revoke a license from Telegram Companion
License Generator and make revocation take effect on connected 0.8.4+ clients
within 15 minutes, with a maximum 24-hour verified-offline grace period.

**Architecture:** Add a deep `internal/revocation` module that owns identity,
the signed manifest protocol, secure client state, decisions, transport limits,
and publication sequencing. The existing license gate consumes one revocation
decision without knowing manifest details. The Generator stores its independent
revocation key and event registry in a dedicated DPAPI sidecar, publishes via a
compare-and-swap GitHub adapter, and exposes one high-level revoke operation to
the Wails UI.

**Tech Stack:** Go 1.26, Ed25519, SHA-256, strict JSON, Wails v2, vanilla
Generator frontend, React/Vitest activation frontend, Windows DPAPI and
Credential Manager, GitHub Contents API, macOS Keychain through the existing
`internal/service/secrets` adapter.

---

## Global constraints

- Do not begin Task 1 until Task 0 is a recorded GO. A local plan is not
  authorization to change the live source tree.
- Do not change license token schema. Schema 1 and schema 2 both derive
  revocation identity from the existing `license_id`.
- Do not overwrite the schema-2 Generator EXE or checksum. Produce a new,
  versioned Generator artifact and checksum.
- Never log or publish a raw token, License ID, Machine ID, owner, comment,
  seed, private key, repository credential, or key-derived secret fingerprint.
- Do not use real GitHub writes in unit/integration tests. Use deterministic
  fake transports and a local `httptest.Server`.
- All time-dependent behavior uses injected wall and monotonic clock seams;
  tests never sleep for 15 minutes or 24 hours.
- Every task follows RED → GREEN → focused regression → review. Commit only
  after the orchestrator confirms shared Git metadata is safe to use.

## Task 0: Provenance handoff and immutable baseline

**Permanent no-touch evidence inputs:**

- `work/tc-sparkle-083/**`
- `.superpowers/sdd/**`
- all existing provenance/release specs, plans, designs, and evidence under
  `docs/superpowers/**`
- `Telegram-Companion-0.8.2-QA-20260818/Telegram-Companion-License-Generator-schema2.exe`
- `Telegram-Companion-0.8.2-QA-20260818/LICENSE-GENERATOR-SHA256SUMS`

The authoritative Mac source is read-only until GO. After GO it may change only
inside the isolated revocation worktree created from the handed-off baseline.
Every historical v2/0.8.3 evidence input above remains immutable after GO.

**Step 1: Ask the dedicated conflict orchestrator for a fresh audit**

The audit must explicitly confirm all of the following:

- SourceAdmission Tasks 7–12 and final review are complete;
- the native/R5 freeze and provenance capture have terminal reports;
- the complete Telegram Companion v2 / Sparkle 0.8.3 release task has a
  terminal reviewed report, or has been formally cancelled and frozen;
- no worker, monitor, or auto-continuation can resume that chain;
- the v2 owner has explicitly handed authoritative-source ownership to this
  feature and identified every still-reserved path;
- no v2 worker is changing the authoritative source or fixed evidence;
- the new implementation baseline HEAD/content-tree identities are recorded;
- the Generator EXE/checksum and provenance evidence are immutable inputs.

Expected result today: **NO-GO**. The 2026-08-23 audit found Task 6 still in a
review/fix cycle, Tasks 7–12 absent, native freeze absent, and
`CAPTURE_NOT_RUN`.

**Step 2: Stop on NO-GO**

On NO-GO, edit only this new plan and its paired new design document. Do not
create a worktree, branch, commit, source file, build, release artifact, or
remote repository mutation.

**Step 3: On a later GO, capture the implementation baseline**

Run in the authoritative worktree:

```bash
git status --short
git rev-parse HEAD
git write-tree
git diff --name-status
```

Record the returned identities in the implementation session notes. Re-read
all paths named below, because v2 may have changed them after this plan was
written.

**Step 4: Create isolation only after GO**

Use `superpowers:using-git-worktrees` to create a unique revocation worktree
from that exact baseline. The orchestrator must verify that its branch name,
worktree path, and file ownership do not overlap any v2 task.

## Task 1: Freeze the protocol with identity and manifest tests

**Files:**

- Create: `internal/revocation/identity.go`
- Create: `internal/revocation/identity_test.go`
- Create: `internal/revocation/manifest.go`
- Create: `internal/revocation/manifest_test.go`
- Create: `internal/revocation/testdata/manifest-sequence-0.tcrev`
- Create: `internal/revocation/testdata/manifest-sequence-1.tcrev`
- Modify only for regression assertions: `internal/license/codec_test.go`

**Step 1: Write failing identity golden tests**

The public API is:

```go
package revocation

type Handle [32]byte

func DeriveHandle(licenseID string) (Handle, error)
func (h Handle) String() string
func ParseHandle(value string) (Handle, error)
```

Test empty/invalid UTF-8 rejection, deterministic derivation, exact 32-byte
output, canonical unpadded base64url, and this formula:

```go
digest := sha256.New()
digest.Write([]byte("telegram-companion/revocation/license-id/v1\x00"))
digest.Write([]byte(licenseID))
```

Add a codec regression test proving schema-1 and schema-2 payloads expose the
same pre-existing LicenseID field and that revocation adds no token field.

Run:

```bash
go test ./internal/revocation ./internal/license -run 'Handle|LicenseID|Schema'
```

Expected RED: `internal/revocation` API does not exist.

**Step 2: Implement the minimal identity API**

Reject empty values, invalid UTF-8, surrounding whitespace, and values larger
than the existing license ID limit. Never normalize or lowercase a License ID.

**Step 3: Write failing strict-manifest tests**

Use these protocol types:

```go
const EnvelopePrefix = "TCREV1"

type Entry struct {
    Kind      string `json:"kind"`
    Value     string `json:"value"`
    RevokedAt string `json:"revoked_at"`
}

type Payload struct {
    Schema      uint64  `json:"schema"`
    KeyID       string  `json:"key_id"`
    Sequence    uint64  `json:"sequence"`
    GeneratedAt string  `json:"generated_at"`
    Entries     []Entry `json:"entries"`
}

type VerifiedManifest struct {
    Payload Payload
    Digest  [32]byte
}

func Sign(payload Payload, key ed25519.PrivateKey) (string, error)
func Verify(envelope string, keyID string, key ed25519.PublicKey) (VerifiedManifest, error)
func BuildNext(current VerifiedManifest, handle Handle, revokedAt time.Time, key ed25519.PrivateKey) (string, error)
func (m VerifiedManifest) Contains(handle Handle) bool
```

Cover exact canonical round-trip and rejection of: unknown/duplicate fields,
trailing JSON, padded or noncanonical base64url, bad prefix, bad signature,
wrong key ID, invalid UTF-8, non-UTC/fractional times, entry time after manifest
time, unsorted/duplicate entries, unsupported schema/kind, sequence overflow,
more than 10,000 entries, key IDs over 64 ASCII bytes, envelopes over 1 MiB,
decoded payloads over 768 KiB, and decoded handles not exactly 32 bytes.

Run:

```bash
go test ./internal/revocation -run 'Manifest|Envelope|BuildNext'
```

Expected RED: manifest functions are undefined.

**Step 4: Implement one strict parser/encoder**

Decode with `json.Decoder.DisallowUnknownFields`, require EOF, marshal the typed
payload again, and require byte equality with the signed payload before using
it. Sort and compare decoded handle bytes. Verify the signature over the exact
decoded payload bytes, then return a detached immutable-by-copy value.

**Step 5: Add fixed signed vectors and run focused regressions**

Generate test-only Ed25519 keys from fixed bytes. Check in the two public
fixtures and their expected digest/sequence/membership. Run:

```bash
go test ./internal/revocation ./internal/license
```

## Task 2: Implement secure state, the decision matrix, and bounded fetching

**Files:**

- Create: `internal/revocation/state.go`
- Create: `internal/revocation/state_test.go`
- Create: `internal/revocation/evaluator.go`
- Create: `internal/revocation/evaluator_test.go`
- Create: `internal/revocation/fetcher.go`
- Create: `internal/revocation/fetcher_test.go`
- Modify: `internal/service/secrets/keyring_test.go` only if an existing
  adapter contract needs a regression assertion

**Step 1: Write the evaluator table before implementation**

Use stable decisions:

```go
type Decision string

const (
    Active        Decision = "active"
    ActiveInGrace Decision = "active_in_grace"
    CheckRequired Decision = "check_required"
    Revoked       Decision = "revoked"
)

type SecureState struct {
    Schema             uint64
    HighestSequence    uint64
    LastSuccessUTC     time.Time
    LastWallUTC        time.Time
    ManifestDigest     [32]byte
    RevokedHandles     []Handle
}
```

Required cases:

- a verified nonmatch is active and advances sequence/time/digest;
- a verified match is revoked and adds an irreversible receipt;
- any matching receipt is revoked without network;
- unavailable/invalid data inside 24 hours is grace;
- unavailable/invalid data at or after 24 hours is check-required;
- no previous successful verification means check-required, not free grace;
- a lower verified sequence is a failed check;
- a replacement handle is not revoked by an old receipt;
- wall clock rollback over five minutes requires a successful online check;
- rollback of five minutes or less does not extend the stored grace deadline;
- internal-channel builds never call the revocation client.

Run:

```bash
go test ./internal/revocation -run 'Evaluate|SecureState'
```

Expected RED: evaluator and secure-state codec do not exist.

**Step 2: Implement canonical secure-state persistence**

Define a small storage interface:

```go
type StateStore interface {
    Load(context.Context) (SecureState, error)
    Save(context.Context, SecureState) error
}
```

Encode strict versioned JSON, cap it at 1 MiB, keep revoked handles sorted and
unique, and retain receipts across replacement licenses. Adapt the existing
`secretservice.SecretStore` with service `telegram-companion` and secret name
`license-revocation-state-v1`; do not add a second platform-keyring library.

**Step 3: Write bounded HTTP tests**

The fetcher contract is:

```go
type Fetcher interface {
    Fetch(context.Context) ([]byte, error)
}
```

Use `httptest.Server` to assert HTTPS-only configuration validation separately,
five-second total timeout, no redirect to a different host or non-HTTPS URL,
`Cache-Control: no-cache`, a minute-resolution `revocation_check` query with no
identifier, non-2xx rejection, a 1 MiB read limit, and close/drain behavior.

**Step 4: Implement fetcher and run race tests**

Use a dedicated `http.Client`; never use `http.DefaultClient`. The production
URL is exactly:

```text
https://raw.githubusercontent.com/GosuMicro555/telegram-companion-updates/main/revocations.tcrev
```

Run:

```bash
go test -race ./internal/revocation ./internal/service/secrets
```

## Task 3: Pin revocation metadata in the public build contract

**Files:**

- Modify: `internal/buildinfo/info.go`
- Modify: `internal/buildinfo/info_test.go`
- Modify: `internal/buildinfo/channel_internal.go`
- Modify: `internal/buildinfo/channel_public_macos_arm64.go`
- Modify: `internal/buildinfo/current_public_macos_arm64_test.go`
- Modify: `internal/buildinfo/release_build_contract_test.go`
- Modify: `Makefile`
- Modify: `.github/workflows/ci.yml`
- Modify: `scripts/release/macos-arm64/build-stage.sh`
- Modify: `scripts/release/macos-arm64/preflight.sh`
- Modify: `scripts/release/macos-arm64/verify.sh`
- Modify: `scripts/release/macos-arm64/release_scripts_test.go`

**Step 1: Add failing buildinfo validation tests**

Extend `buildinfo.Info` with:

```go
RevocationManifestURL string
RevocationKeyID       string
RevocationPublicKey   string
```

Public builds require the exact HTTPS raw-content host/path, canonical key ID
`revocation-2026-01`, and canonical standard-base64 32-byte Ed25519 public key.
Internal builds leave all three empty. Reject user info, fragments, base query
parameters, alternate hosts, noncanonical base64, and partial configuration.

Run:

```bash
go test ./internal/buildinfo -run 'Revocation|Public'
```

Expected RED: fields and compile-time variables are absent.

**Step 2: Add compile-time variables and release link flags**

Add `revocationManifestURL`, `revocationKeyID`, and `revocationPublicKey` beside
the current version/license/appcast variables. Extend release preflight to fail
before build if any public revocation value is missing or malformed. CI compiles
with deterministic test values but never publishes.

**Step 3: Run build-contract regressions**

```bash
go test ./internal/buildinfo ./scripts/release/macos-arm64
go test ./cmd/license-generator -run BuildContract
```

## Task 4: Integrate revocation into activation and startup authorization

**Files:**

- Modify: `internal/license/gate.go`
- Modify: `internal/license/gate_test.go`
- Modify: `internal/transport/wails/activation_bindings.go`
- Modify: `internal/transport/wails/activation_bindings_test.go`
- Modify: `frontend/src/activation.ts`
- Modify: `frontend/src/activation.test.ts`
- Modify: `frontend/src/ActivationScreen.tsx`
- Modify: `frontend/src/ActivationScreen.test.tsx`
- Modify: `desktop_activation.go`
- Modify: `desktop_activation_test.go`
- Modify: `main.go`
- Modify: `main_test.go`

**Step 1: Write failing gate tests**

Add stable gate states and safe codes:

```go
const (
    StateRevoked       GateState = "revoked"
    StateCheckRequired GateState = "check_required"
)

const (
    GateErrorRevoked       = "license_revoked"
    GateErrorCheckRequired = "revocation_check_required"
)
```

Inject one high-level dependency that receives the verified payload LicenseID
and returns `revocation.Decision`. Assert offline license verification runs
first, invalid licenses never touch the network, active and grace authorize,
revoked/check-required do not authorize, and activating a replacement token
performs an online decision before it becomes active.

Run:

```bash
go test ./internal/license -run 'Gate.*Revocation|Activate'
```

Expected RED: gate has no revocation dependency or states.

**Step 2: Implement the gate seam without protocol leakage**

`internal/license` may call `revocation.Checker.Check(ctx, payload.LicenseID)`;
it must not parse manifests or handle HTTP itself. Preserve the current
constructor as a compatibility wrapper for internal builds/tests and add a
dependency-aware constructor for public runtime wiring.

**Step 3: Add activation UI tests**

Map `revoked` to `Лицензия деактивирована владельцем` and explain that local
data remains intact and a newly issued license can be activated. Map
`check_required` to a connectivity-required message. Do not display raw backend
errors or identifiers.

Run:

```bash
go test ./internal/transport/wails ./... -run 'Activation|DesktopProgram'
cd frontend && npm test -- --run ActivationScreen activation
```

**Step 4: Wire startup dependencies**

In `main.go`, construct the existing runtime `SecretStore` before the public
gate, then create the revocation state adapter, bounded fetcher, verifier, and
checker from `buildinfo.Info`. A startup result of revoked/check-required builds
only the activation program; it must not construct Telegram automation,
analytics, scheduled-DM, or session services.

## Task 5: Add the periodic supervisor and safe runtime deactivation

**Files:**

- Create: `internal/revocation/supervisor.go`
- Create: `internal/revocation/supervisor_test.go`
- Modify: `desktop_activation.go`
- Modify: `desktop_activation_test.go`
- Modify: `main.go`
- Modify: `main_test.go`

**Step 1: Write deterministic scheduler tests**

Inject clock, timer, and jitter sources. Assert:

- a normal check starts 13–14 minutes after the previous completion and the
  next successful completion remains below 15 minutes under normal transport;
- failure retries are 1 minute, 5 minutes, then the normal cadence;
- cancellation stops timers and transport work;
- concurrent timer/manual checks coalesce to one request;
- revoked/check-required emits one terminal callback;
- callback is never invoked for active/grace decisions.

Run:

```bash
go test -race ./internal/revocation -run Supervisor
```

Expected RED: supervisor does not exist.

**Step 2: Implement the supervisor**

Use a single goroutine and an injected timer interface. Schedule each normal
start 13–14 minutes from the previous completion; the five-second request
deadline therefore cannot push normal successful enforcement to 15 minutes.
Persist verified state before publishing a terminal decision.

**Step 3: Write shutdown/relaunch ordering tests**

Assert this order for a terminal runtime decision:

1. gate publishes revoked/check-required;
2. new licensed operations are rejected;
3. `DesktopApp.Shutdown` quiesces running services idempotently;
4. existing relaunch coordinator requests a restart;
5. Wails quits;
6. the next launch shows activation/revoked state.

No profile, Telegram session, Keychain, license file, or app data is deleted.

**Step 4: Wire the supervisor and run lifecycle regressions**

```bash
go test -race ./... -run 'Revocation|Shutdown|Relaunch|Desktop'
```

## Task 6: Add the Generator revocation key, registry, and portable backup v3

**Files:**

- Create: `cmd/license-generator/revocation_state.go`
- Create: `cmd/license-generator/revocation_state_windows.go`
- Create: `cmd/license-generator/revocation_state_windows_test.go`
- Modify: `cmd/license-generator/state.go`
- Modify: `cmd/license-generator/service.go`
- Modify: `cmd/license-generator/service_test.go`
- Modify: `cmd/license-generator/backup.go`
- Modify: `cmd/license-generator/backup_test.go`
- Modify: `cmd/license-generator/main_windows.go`

**Step 1: Write failing state migration and protection tests**

Use a dedicated sidecar:

```text
generator-revocation-state.json
```

Its typed state contains schema, public key, DPAPI-protected private key,
independent backup-confirmed flag, and local publication records. DPAPI entropy
is exactly `Telegram Companion License Generator/Revocation/DPAPI/v1`.

Test atomic temp-file replacement, 0600 intent, strict JSON, 8 MiB cap,
canonical base64, public/private match, corrupt/truncated file rejection,
dedicated entropy, rollback on save failure, and private-byte wiping. Existing
`generator-state.json` and `generator-seed-state.json` formats remain readable
and are not rewritten merely by status display.

Run on Windows or the existing Windows test path:

```powershell
go test ./cmd/license-generator -run 'RevocationState|ProtectedState'
```

Expected RED: revocation state repository does not exist.

**Step 2: Implement state and safe initialization**

Generate the independent Ed25519 key only when revocation state is genuinely
uninitialized. If a signed remote manifest already exists but the private key
is absent, disable publication and require a v3 backup; never silently replace
the key. Overall backup readiness is the conjunction of existing signing/seed
confirmation and the revocation-state confirmation.

**Step 3: Write portable backup v3 tests**

The format is:

```text
TCPKEYBACKUP3.<encrypted-license-key>.<encrypted-seed-material>.<encrypted-revocation-key>
```

Bind all three encrypted parts by including a truncated SHA-256 binding over
the format label, license private key, seed ID, seed key, and revocation private
key in the encrypted seed material. Test round-trip, wrong password, part
splicing, truncation, noncanonical encoding, key mismatch with existing
history/manifest, and rollback after either repository save fails.

Legacy `TCPKEYBACKUP1`/`TCPKEYBACKUP2` restore keeps an already present
revocation key but cannot claim revocation backup readiness. On a fresh machine
with only a legacy backup, license issuance may be restored but revocation
publishing stays disabled until a valid v3 backup with the pinned key is
restored.

Run:

```powershell
go test ./cmd/license-generator -run 'Backup|Restore|Revocation'
```

**Step 4: Implement v3 and zeroization**

Extend `Close` to wipe both private keys and the seed key. Extend the existing
secret-material detector with `TCPKEYBACKUP3` and the revocation key encodings.
Do not put revocation records, labels, Machine IDs, or GitHub credentials in the
portable backup.

## Task 7: Implement idempotent compare-and-swap publication

**Files:**

- Create: `internal/revocation/publisher.go`
- Create: `internal/revocation/publisher_test.go`
- Create: `cmd/license-generator/github_revocation_transport.go`
- Create: `cmd/license-generator/github_revocation_transport_test.go`
- Create: `cmd/license-generator/revocation_credentials.go`
- Create: `cmd/license-generator/revocation_credentials_test.go`
- Modify: `cmd/license-generator/service.go`
- Modify: `cmd/license-generator/service_test.go`

**Step 1: Write the publisher state-machine tests**

Use a transport seam with explicit remote identity:

```go
type RemoteFile struct {
    Bytes  []byte
    BlobID string
}

type PublishTransport interface {
    LoadAuthenticated(context.Context) (RemoteFile, error)
    CompareAndSwap(context.Context, string, []byte) (RemoteFile, error)
    LoadAnonymous(context.Context, string) ([]byte, error)
}
```

Cover empty sequence-0 initialization, one append/one increment, duplicate
click idempotency, already-remote reconciliation, CAS conflict refetch/rebuild,
ambiguous upload, invalid remote signature, lower sequence, missing historical
entry, read-back byte mismatch, anonymous stale cache, retry without sequence
skips, and concurrent Generator processes.

Run:

```bash
go test -race ./internal/revocation -run Publisher
```

Expected RED: publisher does not exist.

**Step 2: Implement publication as one deep operation**

Persist an idempotent local event before network mutation. Verify the complete
remote baseline, append only, sign with the independent key, CAS against its
blob identity, then require authenticated and anonymous read-back byte equality,
valid signature, expected sequence, and membership. Return published only after
all checks pass. Ambiguous outcomes remain pending and reconcile first on retry.

**Step 3: Write GitHub adapter and credential tests**

Use the repository Contents API only for the exact owner/repository/branch/path.
Validate response sizes and blob IDs, send `If-Match`/expected SHA semantics,
redact authorization headers and response bodies, and reject redirects outside
`api.github.com` or `raw.githubusercontent.com`.

Store the fine-grained PAT through the existing `SecretStore` using service
`telegram-companion-license-generator` and name
`github-revocation-publisher-v1`. Expose only a configured boolean in status.
The token needs contents read/write on the artifact repository and no signing
capability.

**Step 4: Run integration and redaction tests**

```bash
go test -race ./internal/revocation ./cmd/license-generator -run 'Publisher|GitHub|Credential|Redact'
```

## Task 8: Add the Generator button and truthful publication states

**Files:**

- Modify: `internal/license/codec.go`
- Modify: `internal/license/codec_test.go`
- Modify: `cmd/license-generator/service.go`
- Modify: `cmd/license-generator/service_test.go`
- Modify: `cmd/license-generator/ui.go`
- Modify: `cmd/license-generator/ui_test.go`
- Modify: `cmd/license-generator/frontend/dist/index.html`
- Modify: `cmd/license-generator/frontend/dist/app.js`
- Modify: `cmd/license-generator/frontend/dist/app.css`
- Modify: `cmd/license-generator/build_contract_test.go`

**Step 1: Write backend/UI contract tests**

Extend status with safe rows only:

```go
type LicenseRow struct {
    LicenseID       string `json:"licenseID"`
    Owner           string `json:"owner"`
    IssuedAt        string `json:"issuedAt"`
    ExpiresAt       string `json:"expiresAt,omitempty"`
    RevocationState string `json:"revocationState"`
    RevokedAt       string `json:"revokedAt,omitempty"`
}
```

Expose `ConfigureRevocationCredential(token string) error`,
`RevokeLicense(licenseID string) (LicenseRow, error)`, and
`RetryRevocation(licenseID string) (LicenseRow, error)`. Resolve the LicenseID
against local issuance history before deriving a handle. Never accept a raw
handle from the UI.

Add a dedicated `license.ParseAndVerifyForRegistry` API that verifies canonical
encoding, Ed25519 signature, product, channel, and all payload structure while
intentionally allowing an expired grant and not comparing it with the
Generator machine. `GeneratorUI.ImportLicense` selects a local
`.tcomplicense`, reads it only in Go, passes it directly to the service, persists
only safe issuance metadata, clears the mutable input buffer, and never logs or
retains the transient token string. Invalid,
wrong-product, wrong-channel, or incorrectly signed imports never persist.

Test unknown LicenseID, duplicate click, credential missing, pending, retry,
published, safe error mapping, and a legacy-client warning.

**Step 2: Add DOM tests before behavior**

If no Generator frontend harness exists at the post-v2 baseline, add a small
Node DOM test beside the embedded assets and a `go test` build-contract check.
Assert:

- every active row has one `Отозвать лицензию` action;
- confirmation names the local owner/expiry and says the action is irreversible;
- states are `Активна`, `Публикация отзыва`, `Ошибка публикации`, `Отозвана`;
- pending/error offers retry, revoked offers no reverse action;
- clients below 0.8.4 are explicitly not claimed deactivated;
- the PAT input is password-masked and never echoed into status/DOM after save.

**Step 3: Implement service/UI wiring**

Use `textContent`, not HTML interpolation, for all local labels. Disable the row
action while its promise is active. Refresh backend status after every outcome.
Only a fully verified publication may render `Отозвана`.

**Step 4: Run Generator regressions**

```powershell
go test -race ./cmd/license-generator ./internal/revocation
go test ./cmd/license-generator -run 'UI|BuildContract|Secret'
```

Build Wails bindings only after tests pass, then inspect the generated diff for
secret fields and unintended v2/provenance changes.

## Task 9: Provision, release, and verify end to end

**Files:**

- Create: `docs/runbooks/license-revocation.md`
- Modify: `README.md` only for the operator-facing link and supported version
- Create: `work/tc-revocation-084/**` as a new, versioned 0.8.4 evidence and
  artifact root after the Task 0 GO
- Create: a new versioned Generator EXE and checksum inside that new root;
  never overwrite or append to the old schema-2 artifact/checksum paths

**Step 1: Full local verification before any external mutation**

```bash
go test -race ./...
go vet ./...
cd frontend && npm test
cd frontend && npm run typecheck
cd frontend && npm run build
```

Run existing release preflights and source/artifact secret scans using their
post-v2 documented commands. Any failure returns to the owning task; do not
waive a check.

**Step 2: Independent reviews**

Run `requesting-code-review` with separate spec and standards reviewers. The
spec reviewer checks every acceptance criterion in
`docs/specs/2026-08-23-license-revocation-design.md`; the standards reviewer
checks the post-v2 repository instructions. Resolve findings test-first and
repeat both reviews.

**Step 3: Provision the trust root in strict order**

1. Generate the independent revocation key in the protected Generator state.
2. Export and restore-test a `TCPKEYBACKUP3` backup offline.
3. Record only the public key and key ID for build inputs.
4. Publish and anonymously verify an empty signed sequence-0 manifest.
5. Build/sign/notarize 0.8.4 with the exact manifest URL/key ID/public key.
6. Install and verify 0.8.4 on a disposable macOS VM.
7. Build a new versioned Windows Generator and checksum.
8. Configure the fine-grained GitHub credential through the Generator UI.

External repository writes, signing, notarization, and distribution require the
normal release authority and preflight gates; this plan does not bypass them.

**Step 4: Disposable-VM acceptance matrix**

Verify with deterministic evidence:

- online active schema-1 and schema-2 licenses remain active;
- revoking each schema stops a running connected client within 15 minutes;
- restart remains revoked from the secure receipt;
- network loss stays usable only until 24 hours after last verified success;
- a first check with no prior success and no network is check-required;
- malformed/forged/lower-sequence manifests never create revocation;
- a new replacement license ID restores access after an online check;
- profile, Telegram sessions, Keychain data, and app data remain intact;
- old clients are not falsely reported as remotely deactivated;
- public manifest, logs, reports, source snapshot, app bundle, Generator EXE,
  and checksum contain none of the forbidden secret/personal values.

**Step 5: Artifact identity and final handoff**

Record the source HEAD/tree, manifest SHA-256 and sequence, pinned public key,
client artifact hashes, Generator artifact hash, checksum-file hash, test
commands/results, reviews, and VM evidence. Use
`verification-before-completion`; only then use
`finishing-a-development-branch` to choose merge/PR/cleanup.

---

## Definition of done

The work is complete only when the Generator button has published and verified
one real revocation through both authenticated and anonymous reads; a signed
0.8.4 client enforces it inside the specified bounds; active/replacement/offline
flows pass; secret scans are clean; v2 provenance remains unchanged; and both
spec and standards reviews are approved.
