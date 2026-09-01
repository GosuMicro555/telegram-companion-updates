# Private macOS ARM64 DMG Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Produce a directly transferable Apple Silicon DMG without Developer ID or notarization while preserving licensing and signed Sparkle updates.

**Architecture:** Add an isolated private release path beside the existing Developer ID path. Reuse pinned payload staging and public build metadata, then perform explicit inner-to-outer ad-hoc signing, private packaging, verification, and optional appcast-last publication.

**Tech Stack:** Bash, Make, Go static contract tests, Wails 2, macOS `codesign`, `hdiutil`, Sparkle EdDSA.

## Global Constraints

- Target only macOS 13+ on Apple Silicon arm64.
- Do not weaken or replace the existing Developer ID/notarization pipeline.
- Never use `codesign --deep` or globally disable Gatekeeper.
- Keep Apple credentials optional and unused by the private path.

---

### Task 1: Private release contract

**Files:**
- Modify: `scripts/release/macos-arm64/release_scripts_test.go`
- Modify: `Makefile`

- [ ] Add failing static tests for targets, required scripts, no Apple credentials, explicit ad-hoc signing order, DMG contents, and private verification.
- [ ] Run `go test ./scripts/release/macos-arm64` and confirm the private contract fails because scripts and targets are absent.

### Task 2: Private build and packaging scripts

**Files:**
- Modify: `scripts/release/macos-arm64/lib.sh`
- Create: `scripts/release/macos-arm64/preflight-private.sh`
- Create: `scripts/release/macos-arm64/sign-adhoc.sh`
- Create: `scripts/release/macos-arm64/package-private.sh`
- Create: `scripts/release/macos-arm64/preflight-private-publish.sh`
- Create: `scripts/release/macos-arm64/verify-private.sh`
- Modify: `Makefile`

- [ ] Split build and publication metadata validation without changing the public validation contract.
- [ ] Implement private preflight with no Apple credentials.
- [ ] Implement explicit inner-to-outer ad-hoc signing without Hardened Runtime or timestamp.
- [ ] Package app, `/Applications` alias, first-launch guide, DMG, update ZIP, and checksums.
- [ ] Verify signatures, ARM64 architecture, metadata, Sparkle linkage, DMG integrity, and optional appcast.
- [ ] Add build and publish targets with separate output paths.
- [ ] Run focused Go tests and shell syntax checks.

### Task 3: Operator handoff

**Files:**
- Modify: `docs/operations/macos-arm64-release.md`
- Modify: `README.md`

- [ ] Document exact private build, installation, fallback quarantine removal, manual update fallback, and real-Mac acceptance checks.
- [ ] Run all release-script tests and repository tests that are available on Windows.
