# Telegram Companion License Revocation 0.8.3 Amendment

Date: 2026-08-25

Status: approved by explicit user sequencing decision

## Governing change

This amendment changes only the release assignment and ordering in
`docs/specs/2026-08-23-license-revocation-design.md` and
`docs/plans/2026-08-23-license-revocation-implementation.md`.

The first revocation-capable client is the final reviewed `0.8.3` build. The
revocation implementation is completed after the removal of the feature-owned
`tdata New Account` paths and before the combined 0.8.3 verification, private
friend DMG, update archive, or feed handoff. The earlier requirement to wait
for a terminal 0.8.3 release and begin in 0.8.4 is superseded.

All other security, privacy, compatibility, persistence, publication,
read-back, rollback, testing, and evidence requirements in the approved design
and plan remain binding. In particular:

- no license token schema or existing License ID changes;
- existing tdata accounts, Telegram sessions, profiles, Keychain records, and
  application data are preserved;
- revocation remains irreversible for a published License ID handle;
- the public manifest contains only domain-separated one-way handles and no
  names, Machine IDs, tokens, seeds, notes, credentials, or private material;
- the independent revocation key, signed append-only manifest, monotonic
  sequence, revoked receipt, 15-minute connected bound, and 24-hour offline
  grace remain unchanged;
- failed or ambiguous publication remains pending until compare-and-swap and
  authenticated plus anonymous exact-byte read-back succeed;
- no external user or friend license is revoked automatically. Before final
  handoff, real-transport acceptance may revoke only a dedicated disposable
  test license created for that purpose;
- publication and distribution remain blocked until all combined release
  checks and independent reviews pass.

## Compatibility wording

Clients older than the final revocation-enabled 0.8.3 artifact cannot enforce
revocation. A version label alone must not be used to claim that an already
distributed pre-capability candidate was remotely disabled. The Generator and
operator copy must refer to the final revocation-enabled 0.8.3 build or later.

No earlier 0.8.3 candidate may be placed on the update feed. A machine that
received an unpublished pre-capability 0.8.3 candidate must install the final
DMG explicitly unless the update system proves a strictly newer Sparkle build
identity. The friend handoff contains only the final verified DMG and its
checksum; update-feed publication remains a separately verified action.

## Execution boundary

Implementation starts from an exact post-`tdata New Account` baseline with its
HEAD, committed tree, working-status digest, and relevant source-file digests
recorded. The isolated `internal/revocation` protocol core may be developed in
parallel because it owns new paths. Integration seams in `main.go`,
`internal/license`, `internal/buildinfo`, activation UI/bindings, Generator UI
and persistence, and release scripts remain exclusively owned by the 0.8.3
integration controller until it hands them off.

The combined release verification and provenance evidence must be derived
again from the final source containing both the tdata removal and revocation.
Historical pre-removal counts, hashes, candidates, and capture instructions do
not authorize the changed source.
