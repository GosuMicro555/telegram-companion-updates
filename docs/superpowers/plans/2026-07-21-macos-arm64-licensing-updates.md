# macOS ARM64 Licensing And Updates Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Produce an Apple-Silicon-only public Telegram Companion build that cannot initialize application data or Telegram services before offline activation, can update through Sparkle/GitHub Releases, and has a Windows license generator.

**Architecture:** Compile-time build metadata selects either the existing internal runtime or a public macOS ARM64 activation shell. The shell verifies a product-scoped Ed25519 license against a domain-separated Machine ID before lazily constructing SQLite, Tor/Snowflake, Telegram, backups, or schedulers. A platform-neutral updater state machine drives a small Wails UI while a Darwin bridge delegates downloads and installation to Sparkle.

**Tech Stack:** Go 1.26, Wails 2.13, React 19, TypeScript 7, Vitest 4, Ed25519, Windows DPAPI, scrypt + AES-256-GCM, Sparkle 2, GitHub Releases, Apple codesign/notarytool.

## Global Constraints

- Public target is `darwin/arm64` on macOS 13 or newer; Intel and Universal Binary artifacts are excluded.
- Build channels are exactly `internal` and `public-macos-arm64`, selected at compile time.
- The public app stores data under `~/Library/Application Support/Telegram Companion/`; development data remains isolated.
- The license private key exists only in the Windows generator; the public app embeds only its Ed25519 public key.
- License payloads are product- and channel-scoped and must not authorize another product or build channel.
- Raw `IOPlatformUUID`, license text, private keys, proxy secrets, and Telegram session material must never be logged.
- Before successful activation, the public build must not open SQLite or start Tor, Snowflake, Telegram, backups, analytics, or message schedulers.
- Automatic update checks begin two minutes after successful activation and repeat no more often than hourly.
- The initial DMG is delivered directly; the public GitHub repository contains only release artifacts, appcast, release notes, and a minimal README.
- Telegram automation remains stopped throughout development and verification in this environment.

---

### Task 1: Compile-Time Product And Channel Metadata

**Files:**
- Create: `internal/buildinfo/info.go`
- Create: `internal/buildinfo/info_test.go`
- Create: `internal/buildinfo/channel_internal.go`
- Create: `internal/buildinfo/channel_public_macos_arm64.go`
- Modify: `Makefile`

**Interfaces:**
- Produces: `buildinfo.Info`, `buildinfo.Current() (Info, error)`, `buildinfo.ChannelInternal`, and `buildinfo.ChannelPublicMacOSARM64`.
- Linker variables: `version`, `licensePublicKey`, and `appcastURL`; public builds fail closed when any required value is invalid.

- [ ] **Step 1: Write failing metadata tests**

```go
func TestValidatePublicRequiresReleaseMetadata(t *testing.T) {
    _, err := validate(Info{Channel: ChannelPublicMacOSARM64, ProductID: ProductID})
    if err == nil { t.Fatal("expected public metadata validation error") }
}

func TestValidateInternalDoesNotEnablePublicCapabilities(t *testing.T) {
    got, err := validate(Info{Channel: ChannelInternal, ProductID: ProductID, Version: "0.7.0"})
    if err != nil || got.RequiresActivation() || got.UpdatesEnabled() { t.Fatalf("unexpected metadata: %#v %v", got, err) }
}
```

- [ ] **Step 2: Run `go test ./internal/buildinfo` and confirm the package is missing**
- [ ] **Step 3: Implement immutable validated metadata and mutually exclusive build-tag files**

```go
type Info struct {
    Channel          Channel
    ProductID        string
    Version          string
    LicensePublicKey string
    AppcastURL       string
}

func (i Info) RequiresActivation() bool { return i.Channel == ChannelPublicMacOSARM64 }
func (i Info) UpdatesEnabled() bool     { return i.Channel == ChannelPublicMacOSARM64 }
```

- [ ] **Step 4: Run `go test ./internal/buildinfo` and both internal/public compile checks**
- [ ] **Step 5: Commit only Task 1 paths with `feat: add compile-time public build metadata`**

### Task 2: Product-Scoped License Codec, Machine ID, And Store

**Files:**
- Create: `internal/license/codec.go`
- Create: `internal/license/codec_test.go`
- Create: `internal/license/machineid.go`
- Create: `internal/license/machineid_test.go`
- Create: `internal/license/machineid_darwin.go`
- Create: `internal/license/machineid_stub.go`
- Create: `internal/license/store.go`
- Create: `internal/license/store_test.go`

**Interfaces:**
- Consumes: `buildinfo.Info` from Task 1.
- Produces: `license.Payload`, `license.ParseAndVerify`, `license.DeriveMachineID`, `license.MachineID`, and `license.FileStore`.

- [ ] **Step 1: Write failing codec tests for valid, malformed, wrong-product, wrong-channel, wrong-machine, expired, and tampered tokens**

```go
type Payload struct {
    Schema    int    `json:"schema"`
    LicenseID string `json:"license_id"`
    Product   string `json:"product"`
    Channel   string `json:"channel"`
    MachineID string `json:"machine_id"`
    Owner     string `json:"owner"`
    Comment   string `json:"comment,omitempty"`
    IssuedAt  string `json:"issued_at"`
    ExpiresAt string `json:"expires_at,omitempty"`
}
```

- [ ] **Step 2: Run `go test ./internal/license -run 'TestParse|TestVerify'` and confirm failure**
- [ ] **Step 3: Implement canonical `TCPLIC1.<payload>.<signature>` parsing and Ed25519 verification with constant product/channel/machine checks**
- [ ] **Step 4: Write failing Machine ID tests proving normalization, uppercase SHA-256 output, domain separation, and no raw identifier in errors**
- [ ] **Step 5: Implement `DeriveMachineID(raw string) string` as SHA-256 of `telegram-companion:machine:v1:` plus normalized input and add Darwin `IOPlatformUUID` acquisition behind build tags**
- [ ] **Step 6: Write failing atomic-store tests for save, reload, permissions, replacement, and corrupt file behavior**
- [ ] **Step 7: Implement `FileStore` at `<dataRoot>/license/license.tcomplicense` using temp-file, fsync, rename, and `0600` permissions**
- [ ] **Step 8: Run `go test ./internal/license` and `go test ./internal/buildinfo ./internal/license`**
- [ ] **Step 9: Commit only Task 2 paths with `feat: add offline license verification`**

### Task 3: Activation Preflight And Process-Level Runtime Gate

**Files:**
- Create: `internal/license/gate.go`
- Create: `internal/license/gate_test.go`
- Create: `internal/transport/wails/activation_bindings.go`
- Create: `internal/transport/wails/activation_bindings_test.go`
- Create: `desktop_activation.go`
- Create: `desktop_activation_test.go`
- Modify: `main.go`
- Modify: `main_bindings.go`

**Interfaces:**
- Consumes: `buildinfo.Info`, `license.MachineID`, `license.FileStore`, and `license.ParseAndVerify`.
- Produces: `ActivationBindings.GetActivationStatus()`, `ActivationBindings.ActivateLicenseKey(string)`, `ActivationBindings.ImportLicenseFile()`, and a process-level preflight that runs either the activation-only Wails shell or the existing complete desktop application.

- [ ] **Step 1: Write a failing gate test whose fake runtime factory counts constructions**

```go
func TestPublicPreflightDoesNotConstructRuntimeBeforeActivation(t *testing.T) {
    calls := 0
    program := newDesktopProgram(publicInfo, fakeLicenseGate(false), func() (*DesktopApp, error) {
        calls++
        return &DesktopApp{}, nil
    })
    if program.Mode() != modeActivation || calls != 0 {
        t.Fatalf("mode=%q runtime constructions=%d", program.Mode(), calls)
    }
}
```

- [ ] **Step 2: Run focused gate tests and confirm failure**
- [ ] **Step 3: Implement `license.Gate` snapshots with states `checking`, `needs_activation`, `activated`, and `error`**
- [ ] **Step 4: Implement process preflight: internal or already-licensed builds create the existing `DesktopApp`; an unlicensed public build creates only activation bindings and no workspace dependencies**
- [ ] **Step 5: On successful activation, atomically save the license, return the activated snapshot, then relaunch the executable and close the activation shell**
- [ ] **Step 6: Bind activation status in both modes; bind the existing workspace `Bindings` only in the authorized full-app mode**
- [ ] **Step 7: Add regression tests proving DB/Tor/Telegram/startup fakes are untouched before activation and initialized exactly once after a licensed restart**
- [ ] **Step 8: Run `go test ./internal/license ./internal/transport/wails` and `go test ./...`**
- [ ] **Step 9: Commit exact Task 3 paths with `feat: gate public runtime behind activation`**

### Task 4: Activation Screen And Workspace Mount Gate

**Files:**
- Create: `frontend/src/activation.ts`
- Create: `frontend/src/activation.test.ts`
- Create: `frontend/src/ActivationScreen.tsx`
- Create: `frontend/src/ActivationScreen.test.tsx`
- Modify: `frontend/src/App.tsx`
- Modify: `frontend/src/i18n.ts`
- Modify: `frontend/src/styles.css`

**Interfaces:**
- Consumes Wails methods from Task 3.
- Produces `activationView(snapshot)` and a bootstrap `App` that mounts the existing workspace only for `internal` or `activated`.

- [ ] **Step 1: Write failing pure tests for loading, activation, workspace, import, and Russian error mapping**
- [ ] **Step 2: Run `npm test -- activation.test.ts ActivationScreen.test.tsx` and confirm failure**
- [ ] **Step 3: Implement the typed activation model**

```ts
export type ActivationSnapshot = {
  mode: "internal" | "public-macos-arm64";
  state: "checking" | "needs_activation" | "activated" | "error";
  machineID?: string;
  errorCode?: string;
};
```

- [ ] **Step 4: Implement a full-window activation view with Machine ID copy, token paste, `.tcomplicense` import, validation progress, and non-secret errors**
- [ ] **Step 5: Rename the current `App` body to `WorkspaceApp` and mount it only after the bootstrap gate authorizes it**
- [ ] **Step 6: Add complete RU and EN strings and scoped light/dark styles without changing release-note typography rules**
- [ ] **Step 7: Run focused tests, then `npm test` and `npm run build`**
- [ ] **Step 8: Commit exact Task 4 paths with `feat: add mac activation experience`**

### Task 5: Windows License Generator

**Files:**
- Create: `cmd/license-generator/main_windows.go`
- Create: `cmd/license-generator/main_stub.go`
- Create: `internal/licenseissuer/issuer.go`
- Create: `internal/licenseissuer/issuer_test.go`
- Create: `internal/licenseissuer/backup.go`
- Create: `internal/licenseissuer/backup_test.go`
- Create: `internal/licenseissuer/store_windows.go`
- Create: `internal/licenseissuer/store_stub.go`
- Create: `internal/licenseissuer/history.go`
- Create: `internal/licenseissuer/history_test.go`
- Modify: `Makefile`

**Interfaces:**
- Consumes the canonical payload and token format from Task 2.
- Produces `Telegram Companion License Generator.exe`, text keys, `.tcomplicense` files, DPAPI-protected active key state, password-encrypted portable backup, and token-free issuance history.

- [ ] **Step 1: Write failing issuer tests proving generated licenses verify in `internal/license` and cross-product/channel IDs are rejected**
- [ ] **Step 2: Implement Ed25519 generation/signing and deterministic canonical serialization**
- [ ] **Step 3: Write failing backup tests for wrong password, tamper, round-trip, random salt/nonce, and no plaintext private key**
- [ ] **Step 4: Implement `TCPKEYBACKUP1` using scrypt `N=32768,r=8,p=1`, AES-256-GCM, 16-byte salt, 12-byte nonce, and the format string as AAD**
- [ ] **Step 5: Write failing history tests proving tokens and private keys are never persisted in issuance metadata**
- [ ] **Step 6: Implement atomic Windows generator state: DPAPI private-key blob, SPKI public key, backup-confirmed flag, and JSON issuance history**
- [ ] **Step 7: Implement a compact native Windows GUI for Machine ID, owner, comment, optional expiry, issue/copy/save, backup, and restore**
- [ ] **Step 8: Run `go test ./internal/licenseissuer ./internal/license` and cross-build `GOOS=windows GOARCH=amd64 go build ./cmd/license-generator`**
- [ ] **Step 9: Commit exact Task 5 paths with `feat: add Windows license generator`**

### Task 6: Updater State Machine And Wails UI

**Files:**
- Create: `internal/updater/service.go`
- Create: `internal/updater/service_test.go`
- Create: `internal/updater/driver.go`
- Create: `internal/transport/wails/update_bindings.go`
- Create: `internal/transport/wails/update_bindings_test.go`
- Create: `frontend/src/updater.ts`
- Create: `frontend/src/updater.test.ts`
- Create: `frontend/src/UpdateButton.tsx`
- Create: `frontend/src/UpdateButton.test.tsx`
- Modify: `frontend/src/App.tsx`
- Modify: `frontend/src/i18n.ts`
- Modify: `frontend/src/styles.css`

**Interfaces:**
- Produces updater states `disabled`, `idle`, `checking`, `available`, `downloading`, `ready`, and `error` plus `GetUpdateStatus`, `CheckForUpdates`, `DownloadUpdate`, and `RestartAndInstallUpdate`.

- [ ] **Step 1: Write fake-driver tests for the two-minute startup delay, hourly checks, manual download, progress clamping, install, and shutdown cleanup**
- [ ] **Step 2: Run `go test ./internal/updater` and confirm failure**
- [ ] **Step 3: Implement the synchronized state machine with injected clock/timer and driver**
- [ ] **Step 4: Expose immutable Wails snapshots, `CheckForUpdates`, and emit `updater:status` only after activation**
- [ ] **Step 5: Write failing frontend tests for hidden, available, downloading, and ready button models**
- [ ] **Step 6: Implement the compact topbar button, progress behavior, and a `Проверить обновления` action in the Updates section; preserve release-note first-run handling**
- [ ] **Step 7: Run `go test ./internal/updater ./internal/transport/wails`, focused Vitest, full Vitest, and frontend build**
- [ ] **Step 8: Commit exact Task 6 paths with `feat: add public update workflow`**

### Task 7: Sparkle Darwin Bridge

**Files:**
- Create: `internal/updater/sparkle/driver_stub.go`
- Create: `internal/updater/sparkle/driver_darwin_arm64.go`
- Create: `internal/updater/sparkle/bridge_darwin_arm64.m`
- Create: `internal/updater/sparkle/driver_test.go`
- Modify: `main.go`

**Interfaces:**
- Consumes `updater.Driver` from Task 6.
- Produces a no-op driver for non-public builds and a Sparkle-backed driver only for `darwin && arm64 && public_macos_arm64`.

- [ ] **Step 1: Write and run stub-driver tests proving internal builds are disabled and perform no network action**
- [ ] **Step 2: Implement the Darwin ARM64 bridge around `SPUStandardUpdaterController` with callbacks for available version, progress, ready, error, and install**
- [ ] **Step 3: Wire the driver only after activation and stop it during graceful shutdown**
- [ ] **Step 4: Run Linux/Windows compile tests and a Darwin ARM64 compile-only check; record native linkage as a real-Mac verification item**
- [ ] **Step 5: Commit exact Task 7 paths with `feat: bridge public updates to Sparkle`**

### Task 8: Apple-Silicon Release Pipeline

**Files:**
- Create: `build/darwin/Info.plist`
- Create: `resources/macos-arm64/entitlements.release.plist`
- Create: `resources/macos-arm64/payload-manifest.json`
- Create: `scripts/release/macos-arm64/lib.sh`
- Create: `scripts/release/macos-arm64/preflight.sh`
- Create: `scripts/release/macos-arm64/build-stage.sh`
- Create: `scripts/release/macos-arm64/sign.sh`
- Create: `scripts/release/macos-arm64/notarize.sh`
- Create: `scripts/release/macos-arm64/appcast.sh`
- Create: `scripts/release/macos-arm64/publish.sh`
- Create: `scripts/release/macos-arm64/verify.sh`
- Create: `scripts/release/macos-arm64/release_scripts_test.go`
- Modify: `Makefile`
- Modify: `wails.json`
- Modify: `.github/workflows/ci.yml`
- Modify: `.gitignore`

**Interfaces:**
- Consumes Tasks 1, 5, and 7 plus operator-supplied Apple/Sparkle/GitHub secrets.
- Produces a signed/notarized ARM64 `.app`, direct-delivery `.dmg`, Sparkle update ZIP/deltas, checksums, release notes, and appcast-last publication.

- [ ] **Step 1: Write static tests that reject missing pinned hashes, `codesign --deep`, publication before appcast dependencies, non-ARM64 artifacts, or missing macOS 13 metadata**
- [ ] **Step 2: Implement shared fail-closed shell helpers and a preflight that validates tools, clean output paths, build metadata, keys, and pinned payload manifests**
- [ ] **Step 3: Implement build/stage for models, complete Tor/Snowflake runtime, Sparkle framework, licenses, and notices**
- [ ] **Step 4: Implement inner-to-outer signing, notarization/stapling, DMG creation, `generate_appcast`, checksums, and appcast-last publication**
- [ ] **Step 5: Update CI to compile/test Darwin ARM64 without release secrets or publication**
- [ ] **Step 6: Run script tests, shell syntax checks, frontend tests/build, Go tests, and internal Linux build**
- [ ] **Step 7: Commit exact Task 8 paths with `build: add macOS ARM64 release pipeline`**

### Task 9: Integrated Verification And Mac Handoff

**Files:**
- Create: `docs/operations/macos-arm64-release.md`
- Create: `docs/operations/windows-license-generator.md`
- Modify: `README.md`

**Interfaces:**
- Produces a concise operator runbook; it contains no secrets or private repository contents.

- [ ] **Step 1: Run all Go tests with race detection for the new pure packages and normal tests for the full repository**
- [ ] **Step 2: Run all frontend tests, TypeScript compilation, Vite production build, and generated-binding consistency checks**
- [ ] **Step 3: Build the Windows generator and inspect it for embedded private-key or license-token strings**
- [ ] **Step 4: Build the internal Ubuntu application without starting it and verify no Telegram process, Tor process, or automation was launched by tests**
- [ ] **Step 5: Document the exact real-Mac sequence for key insertion, Wails ARM64 build, nested signing, notarization, Gatekeeper smoke test, activation persistence, update from N to N+1, and appcast-last publication**
- [ ] **Step 6: Commit only runbook/README paths with `docs: add macOS release and license operations`**
