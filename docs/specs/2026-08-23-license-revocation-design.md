# Telegram Companion License Revocation Design

Date: 2026-08-23

Status: approved by delegated best-practice decision

## Objective

Allow the owner to revoke an issued Telegram Companion license from Telegram
Companion License Generator at any time. A connected client must stop licensed
operation within 15 minutes; a disconnected client may continue for no more
than 24 hours after its last successful online revocation check.

The design must preserve the existing private seed split, keep personal and
secret data out of the public update repository, remain compatible with
schema-2 licenses, and avoid modifying the active Telegram Companion v2
provenance chain.

## Fixed product decisions

- The first revocation-capable client release is `0.8.4`, after the in-progress
  `0.8.3` provenance and release work reaches its terminal reviewed boundary.
- The public artifact repository `GosuMicro555/telegram-companion-updates`
  hosts one signed snapshot at root path `revocations.tcrev`.
- The client checks on launch and starts each running check 13–14 minutes after
  the previous completed check. This leaves margin for the five-second request
  deadline while keeping enforcement below 15 minutes.
- An unreachable or invalid manifest permits operation only inside a 24-hour
  grace period measured from the last successful online validation.
- Revocation is irreversible. Restoring access requires issuing a new license;
  deleting or editing a revocation entry is prohibited.
- The revocation signing key is separate from both the license signing key and
  the Sparkle EdDSA update key.
- The public manifest contains no user name, Machine ID, license token, seed,
  reason, note, or other personally identifying or secret value.
- Revocation blocks licensed behavior but does not delete the user's profile,
  Telegram sessions, Keychain records, or application data.
- The feature introduces no telemetry and no client-to-owner identity upload.

## Alternatives considered

### Signed snapshot in the existing update repository — selected

The Generator signs and atomically publishes the complete set of revoked
license handles. Clients fetch the small public file and verify it locally.
This reuses existing hosting, needs no always-on backend, exposes no personal
data, and is sufficient for a small known cohort. Its main trade-off is bounded
rather than instantaneous enforcement and reliance on the repository for
availability.

### Authoritative online lookup API — rejected for this version

A client could submit a license handle to a dedicated backend and receive a
current decision. This permits server-side audit and near-real-time policy, but
adds infrastructure, authentication, availability, privacy, and operations
burden. It is disproportionate for friends using a privately distributed app.

### Short-lived renewable licenses — rejected

Very short license expiry would approximate revocation without a list, but it
would require frequent reissuance or a renewal backend and would turn ordinary
outages into license failures. It also does not provide the requested explicit
button-driven revocation history.

## Domain model

- **License**: a signed grant authorizing one installation under the existing
  machine and expiry rules.
- **License ID**: the existing random per-grant identifier present in both
  schema 1 and schema 2. It identifies a grant, not a person or device.
- **Revocation handle**: the public, one-way SHA-256 identity used to compare a
  local license with manifest entries. It is never a raw License ID or token.
- **Revocation event**: an irreversible local Generator record stating that a
  handle was revoked at a specific time.
- **Revocation manifest**: the complete signed snapshot of every published
  revocation event.
- **Manifest sequence**: a strictly increasing unsigned integer. A client must
  never accept a sequence lower than one it has previously accepted.
- **Online validation**: successful retrieval and cryptographic verification of
  the current manifest bytes, followed by a decision for the local license.
- **Grace period**: at most 24 hours after the last online validation during
  which an unavailable manifest does not block an otherwise valid license.
- **Revoked receipt**: persistent client state proving that the current license
  was observed in a verified manifest. It is never cleared for that license.
- **Publication**: a compare-and-swap repository update followed by authenticated
  and anonymous read-back verification of the exact signed bytes.

"Deactivation" in user-facing copy means an irreversible License revocation.
It does not mean deleting local data or remotely controlling the computer.

## Architecture and module seams

### LicenseIdentity module

The client and Generator share equivalent derivation rules behind a small
interface:

```text
DeriveRevocationHandle(license) -> Handle | IdentityError
```

For both schema 1 and schema 2, the handle is exactly:

```text
SHA-256("telegram-companion/revocation/license-id/v1\0" || UTF8(LicenseID))
```

The manifest encodes the 32-byte result as canonical unpadded base64url. The
domain separator prevents a License ID from becoming interchangeable with a
hash used by another protocol. The Generator derives and stores the handle from
its existing issuance-history `LicenseID`; the raw signed token is unnecessary.
A license missing from recovered history can be locally imported, signature-
verified with the Generator's existing public key, and reduced to its License
ID and safe issuance metadata. The raw imported token and schema-2 seed grant
are not retained.

### RevocationManifest module

This module owns strict parsing, canonical encoding, signing, verification,
sequence rules, limits, and membership checks. Its external interfaces are:

```text
VerifyManifest(envelope, pinnedPublicKey) -> VerifiedManifest | ManifestError
BuildNextManifest(current, revocationEvent, privateSigner) -> Envelope | BuildError
IsRevoked(verifiedManifest, handle) -> bool
```

Transport, UI, license parsing, and application shutdown logic do not reproduce
manifest rules. Tests exercise the same interfaces as production callers.

### RevocationEvaluator module

The client owns one authorization decision interface:

```text
EvaluateRevocation(license, secureState, fetchResult, currentTime) -> Decision
```

The decision is exactly one of `ACTIVE`, `ACTIVE_IN_GRACE`, `CHECK_REQUIRED`,
or `REVOKED`. The module hides cache age, signature results, sequence rollback,
clock rollback, legacy identity, and revoked-receipt rules. UI and Telegram
automation code consume only the decision and stable reason code.

### RevocationPublisher module

The Generator owns one publication workflow:

```text
Revoke(licenseRecord) -> PublishedRevocation | PendingPublication | PublishError
```

Its implementation loads and verifies the current remote manifest, appends one
new event locally, increments the sequence once, signs, publishes with the
remote blob identity as a compare-and-swap precondition, and verifies read-back.
Retries reconcile the remote state before creating another sequence. Callers do
not orchestrate individual GitHub, signing, or retry steps.

### Adapters

Two transport adapters satisfy the manifest transport seam:

- a production GitHub adapter using the repository Contents interface for
  compare-and-swap publication and uncached public retrieval;
- an in-memory/HTTP-test adapter for deterministic concurrency, outage, stale
  cache, and read-back tests.

The Generator uses two existing protection patterns. Its new revocation private
key is generated independently and stored in a dedicated, versioned
`generator-revocation-state.json` sidecar with its private bytes protected by
DPAPI and a revocation-specific entropy label. This preserves the current split:
the license signing state and issuance history remain in `generator-state.json`,
while the schema-2 seed grant remains in `generator-seed-state.json`. The
revocation private key is wiped from memory on close and included in the
password-encrypted portable key backup. The
fine-grained repository token is stored separately in Windows Credential
Manager through the project's keyring dependency and is never included in that
backup. Neither secret is embedded in the EXE, source, environment, command
arguments, reports, or logs.

The client uses the existing platform secure-storage seam for the accepted
sequence, last successful validation time, manifest digest, clock checkpoint,
and revoked receipt.

## License schema and compatibility

No new license schema is introduced. The live code already places a random
`license_id` in both schema 1 and schema 2, and the current Generator already
records that ID in issuance history. Schema 2 continues to carry the existing
signed seed grant; revocation neither reads nor republishes it. Every newly
issued license receives a new random License ID, including a replacement for
the same machine.

Client `0.8.4` continues accepting schema 1 and schema 2 under the existing
signature, product, channel, machine, expiry, and seed rules. A Generator import
accepts either schema solely to recover safe issuance metadata and the License
ID. Invalid, incorrectly signed, noncanonical, wrong-product, or wrong-channel
imports are rejected without persistence. An expired token may be imported for
historical revocation only when its signature and all non-time scopes are valid;
it cannot become an active license.

Clients older than `0.8.4` do not implement revocation and cannot be remotely
disabled by this feature. Distribution therefore follows this order:

1. Finish and preserve the `0.8.3` provenance/release evidence.
2. Establish a new reviewed `0.8.4` source baseline.
3. Provision the separate revocation key and publish a verified empty sequence-0
   manifest.
4. Ship and verify the `0.8.4` client before relying on the revoke button.
5. Ship the new Generator without changing the license payload schema.
6. Confirm existing issuance history is present; locally import only any known
   schema-1/schema-2 license missing from that history.

The Generator UI states that clients below `0.8.4` cannot enforce revocation.
It must not report a legacy client as remotely disabled merely because its
handle appears in the manifest.

## Manifest format

The client-facing URL is
`https://raw.githubusercontent.com/GosuMicro555/telegram-companion-updates/main/revocations.tcrev`.
Clients send `Cache-Control: no-cache` and add a minute-resolution cache-busting
query value; the query carries no client or license identifier.

The file is an ASCII envelope:

```text
TCREV1.<canonical-base64url-payload>.<canonical-base64url-ed25519-signature>
```

The decoded payload is strict canonical JSON with these fields:

```json
{
  "schema": 1,
  "key_id": "revocation-2026-01",
  "sequence": 1,
  "generated_at": "2026-08-23T00:00:00Z",
  "entries": [
    {
      "kind": "license_id_sha256",
      "value": "<canonical-base64url-32-bytes>",
      "revoked_at": "2026-08-23T00:00:00Z"
    }
  ]
}
```

`kind` is exactly `license_id_sha256`. Entries are sorted bytewise by decoded
`value`, unique, and append-only. The parser
rejects unknown or duplicate fields, duplicate handles, noncanonical encoding,
invalid UTF-8, invalid time values, out-of-order entries, trailing bytes,
unsupported schemas/keys, sequence overflow, more than 10,000 entries, an
envelope over 1,048,576 bytes, a decoded payload over 786,432 bytes, a key ID
over 64 ASCII bytes, or any handle whose decoded value is not exactly 32 bytes.
Times are canonical UTC RFC 3339 values with whole seconds and `Z`; an entry's
`revoked_at` cannot be later than `generated_at`.

The signature covers the exact payload bytes carried in the envelope. The
client pins the revocation public key independently of the Sparkle and license
verification keys.

## Client decision flow

1. Validate the local license using existing signature, machine, and expiry
   rules. Revocation never makes an otherwise invalid license valid.
2. Derive its handle and check a persistent revoked receipt. A matching receipt
   returns `REVOKED` without network access.
3. At every launch, attempt a bounded online manifest retrieval before enabling
   licensed behavior unless a verified check less than 15 minutes old is
   already cached. A fresh cache permits immediate startup and a background
   refresh.
4. While running, start the next retrieval 13–14 minutes after the previous
   completed check so the interval between completed checks remains below 15
   minutes under normal connectivity.
5. Verify the entire envelope before using any field. A malformed signature,
   unknown key, lower sequence, transport error, HTTP error, oversized response,
   or clock rollback is a failed check, never an implicit active result.
6. A verified matching handle creates the secure revoked receipt, stops new
   Telegram Companion actions, safely quiesces current licensed work, and shows
   the activation screen with `Лицензия деактивирована владельцем`.
7. A verified nonmatching manifest returns `ACTIVE`, advances secure monotonic
   state, and starts a new 24-hour grace window.
8. A failed check inside the grace window returns `ACTIVE_IN_GRACE`; the client
   retries in the background and may show a non-alarming connectivity notice.
9. A failed check after the grace window returns `CHECK_REQUIRED` and blocks
   licensed behavior until a valid online check succeeds.

Each request has a five-second total deadline and reads no more than the
1,048,576-byte envelope limit. After a failed check, an active process retries
after 1 minute, 5 minutes, and then inside the normal 13–14-minute schedule.
The client treats wall-clock movement more than five minutes behind its secure
stored checkpoint as rollback and requires online validation rather than
extending grace. Backward adjustments of five minutes or less do not lock the
client. Process-local elapsed time uses a monotonic clock.

## Generator registry and user experience

The Generator extends its existing versioned local issuance history with
revocation records containing event ID, handle, requested time, published
sequence, remote blob identity, and publication state. The UI derives its
display label, schema, shortened handle, issue time, and expiry from that state.
Machine IDs and raw tokens are not shown in public data and are not needed for
the remote manifest.

The main list supports `Активна`, `Публикация отзыва`, `Отозвана`, and `Ошибка
публикации` states. Selecting an active record enables `Отозвать лицензию`.
The confirmation identifies the local label and expiry, explains that the
action is irreversible, and states that restoring access requires a new
license.

After confirmation, the Generator:

1. records one idempotent local revocation event;
2. verifies the current remote manifest and expected repository blob;
3. signs and publishes the next sequence with compare-and-swap protection;
4. reads the file back through the authenticated repository interface;
5. retrieves it anonymously through the client-facing path with cache bypass;
6. verifies byte identity, signature, sequence, and membership;
7. changes the row to `Отозвана` only after every check passes.

If upload outcome is ambiguous, the row remains `Публикация отзыва` and retry
first reconciles remote state. A failed publication is never displayed as a
successful revocation. Repeated clicks cannot create duplicate events or skip
sequences.

## Security and trust model

- Compromise of the public repository without the revocation private key may
  cause availability failures, but cannot create a valid new revocation.
- Previously seen lower sequences are rejected and do not erase revoked
  receipts. A completely fresh installation has no remembered sequence and can
  still be exposed to a repository-level rollback to an older valid manifest;
  the first release records this residual rather than claiming transparency-log
  guarantees.
- Compromise of the revocation private key permits forged revocations. The key
  has an encrypted, restore-tested backup and a documented replacement path via
  a client update that pins a new key. Automatic key rotation is out of scope.
- The GitHub credential is fine-grained, repository-scoped, and limited to the
  minimum content-writing permission. It cannot sign a manifest.
- Logs and reports contain only manifest sequence, result codes, public file
  digests, counts, durations, and non-secret artifact identities. They never
  contain credentials, raw tokens, License IDs, Machine IDs, seed material, or
  private-key fingerprints derived from secret bytes.
- A user with sufficient privileges can patch the client binary or its runtime.
  Resistance to binary cracking is outside this feature's guarantee. The goal
  is reliable owner-controlled deactivation of ordinary distributed copies.

## Failure and recovery

- Invalid or unavailable remote data is fail-closed only after the 24-hour
  grace window. It never produces `REVOKED` without a valid signed match.
- A verified revoked receipt remains authoritative through repository outage,
  cache deletion, and manifest rollback. A new valid license has a different
  identity and starts its own state.
- The application preserves local data when blocked, allowing the owner to
  issue a replacement without destructive recovery.
- The portable key backup contains the license key, seed grant, and independent
  revocation key, but intentionally excludes issuance history, revocation
  records, personal labels, Machine IDs, and the repository token. After a
  restore, the Generator reconstructs published sequence/membership from the
  verified remote manifest; missing license rows are recovered only by importing
  the corresponding signed token.
- If the repository write succeeds but verification fails, publication pauses;
  operators verify the exact remote bytes before retrying. The existing file is
  never overwritten blindly and history is never rewritten.
- If key material is unavailable, the Generator disables publication and gives
  a credential-specific local error without logging the credential.

## Verification strategy

Implementation is test-first. Required automated coverage includes:

- unchanged schema-1/schema-2 parsing, issuance-history compatibility, and a
  proof that revocation adds no license payload field;
- domain-separated handle derivation with golden vectors shared by Generator
  and client implementations;
- envelope canonicality, valid and invalid Ed25519 signatures, duplicate and
  unknown fields, size/count limits, sorting, sequence overflow, and rollback;
- the complete evaluator matrix across active, revoked, grace, stale, malformed,
  unavailable, clock-rollback, cached-receipt, and replacement-license cases;
- Publisher idempotency, compare-and-swap races, ambiguous upload outcomes,
  read-back mismatch, invalid remote baseline, and retry reconciliation;
- absence of names, Machine IDs, License IDs, tokens, seeds, credentials, and
  private keys from manifest, logs, reports, source snapshots, and artifacts;
- deterministic time tests through an internal clock seam rather than real
  15-minute or 24-hour sleeps;
- integration tests through a local HTTP adapter for launch checks, periodic
  refresh, timeout, stale/cached responses, oversized bodies, and anonymous
  retrieval;
- GUI state tests for confirmation, pending, retry, success, minimum-client
  warning, and irreversible replacement flow;
- end-to-end disposable VM tests proving an online running client blocks within
  15 minutes, an offline client blocks after 24 hours, an active license remains
  usable, schema-1 and schema-2 License IDs are revocable on `0.8.4`, and
  profile/Keychain data is preserved.

Release verification also requires source and artifact scans, a new Generator
checksum rather than replacement of the schema-2 artifact, exact public-key
pinning, an anonymously downloaded manifest comparison, and full existing
activation/update regression suites.

## Coordination with Telegram Companion v2

The latest read-only orchestration audit on 2026-08-23 found the active v2
SourceAdmission work still in Task 6 of 12. Its previous pre-review freeze has
been superseded by fixes in production and test files; Tasks 7–12 and the native
freeze do not yet have terminal reports. That work owns the provenance staged
files, ledgers, fixed source HEAD, content tree, and license golden evidence.
This design does not authorize changes to any of them.

Until Tasks 7–12 and final SourceAdmission review complete **and** the entire
Telegram Companion v2 / Sparkle 0.8.3 release chain has either reached its
terminal reviewed status or been formally cancelled and frozen with an explicit
ownership handoff, this work may create only new local design/plan documents.
It must not modify or synchronize:

- `work/tc-sparkle-083/**`;
- `.superpowers/sdd/**`;
- existing provenance specs, plans, designs, or evidence under
  `docs/superpowers/**`;
- authoritative Mac source or `internal/license/**`;
- `Telegram-Companion-0.8.2-QA-20260818/Telegram-Companion-License-Generator-schema2.exe`;
- `Telegram-Companion-0.8.2-QA-20260818/LICENSE-GENERATOR-SHA256SUMS`.

Before implementation, the orchestrator must re-read the v2 task and workspace,
confirm the terminal state or formal frozen cancellation of the complete v2 /
0.8.3 chain, confirm no auto-continuation remains active, record an explicit
ownership handoff, capture the exact new baseline, and assign non-overlapping
files. Historical v2 source-admission and release evidence remains immutable
after handoff. If v2 is still writing or the status is unknown, implementation
remains blocked without modifying either side.

## Acceptance criteria

- A successful Generator button action produces one higher signed manifest
  sequence and is proven through authenticated and anonymous read-back.
- A connected `0.8.4` client with that license stops licensed operation within
  15 minutes and remains blocked after restart without deleting user data.
- An unreachable manifest never extends operation beyond 24 hours after the
  last successful online validation.
- Active licenses remain usable during normal checks and the bounded grace
  window; malformed or forged manifests cannot revoke them.
- Revocation cannot be reversed by publishing a lower sequence, removing an
  entry, clearing ordinary cache, or repeating the Generator action.
- A replacement schema-2 license has a new License ID and can restore access.
- Existing schema-1 and schema-2 licenses remain accepted by `0.8.4` and are
  individually revocable from preserved Generator history or a verified local
  import.
- Public bytes and diagnostics contain no personal, license, seed, or credential
  material.
- Existing activation, bootstrap, Sparkle update, profile, and Keychain behavior
  passes regression verification.
- No revocation implementation starts until the Telegram Companion v2
  provenance chain reaches the orchestrator-approved terminal checkpoint.

## Out of scope and residual limitations

- Instant enforcement against a powered-off or network-isolated machine is
  impossible; the explicit maximum is the 24-hour grace window.
- Clients older than `0.8.4` cannot enforce revocation.
- No web dashboard, telemetry backend, fleet inventory, automatic reactivation,
  or multi-operator conflict UI is introduced.
- The snapshot design does not provide transparency-log guarantees to a fresh
  installation under complete repository compromise.
- Protection against a user patching the executable is not claimed.
