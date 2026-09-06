package macosarm64_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"telegram-companion/internal/bootstrapstate"
)

func TestUniversalLocalBuildPreparesOneMachineIndependentSeed(t *testing.T) {
	path := filepath.Join(filepath.Dir(scriptPath("lib.sh")), "build-universal-local.sh")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	source := string(contents)

	for _, required := range []string{
		"set -euo pipefail",
		`require_env "PUBLIC_VERSION"`,
		`require_env "PUBLIC_BOOTSTRAP_SEED_EXPORT_FILE"`,
		`require_env "PUBLIC_BOOTSTRAP_SEED_PASSWORD_FILE"`,
		`./scripts/release/macos-arm64/cmd/decrypt-build-seed`,
		`-artifact "$PUBLIC_BOOTSTRAP_SEED_EXPORT_FILE"`,
		`-password-file "$PUBLIC_BOOTSTRAP_SEED_PASSWORD_FILE"`,
		`-seed-id-output "$PUBLIC_BOOTSTRAP_SEED_ID_FILE"`,
		`-seed-key-output "$PUBLIC_BOOTSTRAP_SEED_KEY_FILE"`,
		`PUBLIC_BOOTSTRAP_SEED_ID="$(cat "$PUBLIC_BOOTSTRAP_SEED_ID_FILE")"`,
		`umask 077`,
		`prepare-public-bootstrap-bundle.sh`,
		`PUBLIC_BOOTSTRAP_BUNDLE_SHA256="$(sha256_file "$PUBLIC_BOOTSTRAP_BUNDLE_PATH")"`,
		`export PUBLIC_BOOTSTRAP_BUNDLE_PATH PUBLIC_BOOTSTRAP_BUNDLE_SHA256`,
		`build-dual-local.sh`,
		`trap cleanup EXIT`,
	} {
		if !strings.Contains(source, required) {
			t.Errorf("universal build contract is missing %q", required)
		}
	}

	for _, forbidden := range []string{
		`require_env "PUBLIC_BOOTSTRAP_SEED_ID"`,
		`require_env "PUBLIC_BOOTSTRAP_SEED_KEY_FILE"`,
		"MACHINE_ID",
		"TARGET_LICENSE_FILE",
		"TCPLIC1.",
		"package-seeded-private.sh",
		"publish.sh",
	} {
		if strings.Contains(source, forbidden) {
			t.Errorf("universal build must not depend on recipient data: found %q", forbidden)
		}
	}
}

func TestMakefileExposesUniversalLocalBuild(t *testing.T) {
	root := filepath.Clean(filepath.Join(filepath.Dir(scriptPath("lib.sh")), "..", "..", ".."))
	contents, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	source := string(contents)

	for _, required := range []string{
		"build-universal-macos-arm64-local:",
		"PUBLIC_BOOTSTRAP_SEED_EXPORT_FILE",
		"PUBLIC_BOOTSTRAP_SEED_PASSWORD_FILE",
		"build-universal-local.sh",
	} {
		if !strings.Contains(source, required) {
			t.Errorf("Makefile universal build target is missing %q", required)
		}
	}
}

func TestUniversalSeedVerifierDecryptsWithExpectedIdentityAndRawKey(t *testing.T) {
	fixture := newUniversalSeedVerifierFixture(t)

	output, err := runUniversalSeedVerifier(t, fixture.bundlePath, fixture.seedID, fixture.version, fixture.keyPath)
	if err != nil {
		t.Fatalf("verify TCSEED2 bundle: %v\n%s", err, output)
	}
	if strings.Contains(output, string(fixture.key)) {
		t.Fatal("seed verifier printed raw key material")
	}
}

func TestUniversalSeedVerifierRejectsWrongIdentityAndRawKeyWithoutLeakingKeys(t *testing.T) {
	fixture := newUniversalSeedVerifierFixture(t)
	wrongKey := bytes.Repeat([]byte("W"), 32)
	wrongKeyPath := filepath.Join(t.TempDir(), "wrong-seed.key")
	if err := os.WriteFile(wrongKeyPath, wrongKey, 0o600); err != nil {
		t.Fatalf("write wrong seed key: %v", err)
	}

	tests := []struct {
		name    string
		seedID  string
		keyPath string
	}{
		{name: "wrong SeedID", seedID: fixture.seedID + "-wrong", keyPath: fixture.keyPath},
		{name: "wrong raw key", seedID: fixture.seedID, keyPath: wrongKeyPath},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output, err := runUniversalSeedVerifier(t, fixture.bundlePath, test.seedID, fixture.version, test.keyPath)
			if err == nil {
				t.Fatal("seed verifier accepted mismatched identity or key")
			}
			if strings.Contains(output, string(fixture.key)) || strings.Contains(output, string(wrongKey)) {
				t.Fatal("seed verifier failure printed raw key material")
			}
		})
	}
}

func TestUniversalLocalBuildVerifiesFinalDMGSeedContentAndIdentity(t *testing.T) {
	source := readUniversalLocalScript(t)

	for _, required := range []string{
		`"$GO_BIN" run ./scripts/release/macos-arm64/cmd/verify-public-bootstrap`,
		`"$GO_BIN" run ./scripts/release/macos-arm64/cmd/verify-public-bootstrap-contents`,
		`-seed-id "$PUBLIC_BOOTSTRAP_SEED_ID"`,
		`-seed-key-file "$PUBLIC_BOOTSTRAP_SEED_KEY_FILE"`,
		`hdiutil attach -readonly -nobrowse -mountpoint "$DMG_MOUNT_POINT" "$FINAL_DMG_PATH"`,
		`DMG_STAGED_BUNDLE_PATH="$DMG_MOUNT_POINT/$APP_NAME.app/Contents/Resources/bootstrap-state/state.tcs"`,
		`cmp -s "$PUBLIC_BOOTSTRAP_BUNDLE_PATH" "$DMG_STAGED_BUNDLE_PATH"`,
		`verify_public_seed_bundle "$DMG_STAGED_BUNDLE_PATH"`,
		`verify_public_seed_contents "$DMG_STAGED_BUNDLE_PATH"`,
		`if ! hdiutil detach "$DMG_MOUNT_POINT" >/dev/null; then`,
		`hdiutil detach "$DMG_MOUNT_POINT"`,
		`if [[ "$DMG_ATTACHED" == "0" ]] && [[ -n "$WORK_DIR" ]] && [[ -d "$WORK_DIR" ]]; then`,
	} {
		if !strings.Contains(source, required) {
			t.Errorf("universal seed verification is missing %q", required)
		}
	}

	mainAt := strings.Index(source, "main() {")
	finalVerifierAt := strings.Index(source, "verify_final_dmg_seed() {")
	if mainAt < 0 || finalVerifierAt < 0 || finalVerifierAt >= mainAt {
		t.Fatal("universal build is missing its main or final DMG verifier function")
	}
	mainBody := source[mainAt:]
	preparedVerifyAt := strings.Index(mainBody, `verify_public_seed_bundle "$PUBLIC_BOOTSTRAP_BUNDLE_PATH"`)
	preparedContentsAt := strings.Index(mainBody, `verify_public_seed_contents "$PUBLIC_BOOTSTRAP_BUNDLE_PATH"`)
	buildAt := strings.Index(mainBody, `bash "$SCRIPT_DIR/build-dual-local.sh"`)
	finalVerifyAt := strings.Index(mainBody, "verify_final_dmg_seed")
	if preparedVerifyAt < 0 || preparedContentsAt <= preparedVerifyAt || buildAt <= preparedContentsAt || finalVerifyAt <= buildAt {
		t.Fatal("universal build must verify prepared seed identity and contents, build, then verify the final DMG")
	}

	finalVerifierBody := source[finalVerifierAt:mainAt]
	attachAt := strings.Index(finalVerifierBody, `hdiutil attach -readonly -nobrowse -mountpoint "$DMG_MOUNT_POINT" "$FINAL_DMG_PATH"`)
	compareAt := strings.Index(finalVerifierBody, `cmp -s "$PUBLIC_BOOTSTRAP_BUNDLE_PATH" "$DMG_STAGED_BUNDLE_PATH"`)
	dmgVerifyAt := strings.Index(finalVerifierBody, `verify_public_seed_bundle "$DMG_STAGED_BUNDLE_PATH"`)
	dmgContentsAt := strings.Index(finalVerifierBody, `verify_public_seed_contents "$DMG_STAGED_BUNDLE_PATH"`)
	detachAt := strings.Index(finalVerifierBody, "detach_final_dmg")
	if attachAt < 0 || compareAt <= attachAt || dmgVerifyAt <= compareAt || dmgContentsAt <= dmgVerifyAt || detachAt <= dmgContentsAt {
		t.Fatal("final DMG verification must mount, compare, decrypt, verify contents, then detach the embedded seed")
	}
}

func TestUniversalLocalCleanupDeletesRawSeedKeyBeforeRetainingMountedWorkspace(t *testing.T) {
	source := readUniversalLocalScript(t)
	cleanupAt := strings.Index(source, "cleanup() {")
	trapAt := strings.Index(source, "trap cleanup EXIT")
	if cleanupAt < 0 || trapAt <= cleanupAt {
		t.Fatal("universal build is missing its cleanup function or EXIT trap")
	}

	cleanup := source[cleanupAt:trapAt]
	removeKeyAt := strings.Index(cleanup, `rm -f -- "$PUBLIC_BOOTSTRAP_SEED_KEY_FILE"`)
	retainMountedWorkspaceAt := strings.Index(cleanup, `if [[ "$DMG_ATTACHED" == "0" ]]`)
	if removeKeyAt < 0 || retainMountedWorkspaceAt <= removeKeyAt {
		t.Fatal("cleanup must delete the raw seed key even when a mounted DMG requires retaining the workspace")
	}
}

type universalSeedVerifierFixture struct {
	bundlePath string
	keyPath    string
	key        []byte
	seedID     string
	version    string
}

func newUniversalSeedVerifierFixture(t *testing.T) universalSeedVerifierFixture {
	t.Helper()
	root := t.TempDir()
	sourceData := filepath.Join(root, "data")
	if err := os.MkdirAll(sourceData, 0o700); err != nil {
		t.Fatalf("create source data: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceData, "app.db"), []byte("public seed content"), 0o600); err != nil {
		t.Fatalf("write source database: %v", err)
	}

	key := bytes.Repeat([]byte("K"), 32)
	keyPath := filepath.Join(root, "seed.key")
	if err := os.WriteFile(keyPath, key, 0o600); err != nil {
		t.Fatalf("write seed key: %v", err)
	}
	fixture := universalSeedVerifierFixture{
		bundlePath: filepath.Join(root, "state.tcs"),
		keyPath:    keyPath,
		key:        key,
		seedID:     "public-bootstrap-v1",
		version:    "0.8.2",
	}
	if err := bootstrapstate.Pack(context.Background(), bootstrapstate.PackConfig{
		SourceData: sourceData,
		OutputPath: fixture.bundlePath,
		BundleID:   fixture.seedID,
		AppVersion: fixture.version,
		Key:        fixture.key,
	}); err != nil {
		t.Fatalf("create TCSEED2 fixture: %v", err)
	}
	return fixture
}

func runUniversalSeedVerifier(t *testing.T, bundlePath, seedID, version, keyPath string) (string, error) {
	t.Helper()
	root := filepath.Clean(filepath.Join(filepath.Dir(scriptPath("lib.sh")), "..", "..", ".."))
	command := exec.Command(
		"go", "run", "./scripts/release/macos-arm64/cmd/verify-public-bootstrap",
		"-bundle", bundlePath,
		"-seed-id", seedID,
		"-version", version,
		"-seed-key-file", keyPath,
	)
	command.Dir = root
	output, err := command.CombinedOutput()
	return string(output), err
}

func readUniversalLocalScript(t *testing.T) string {
	t.Helper()
	path := filepath.Join(filepath.Dir(scriptPath("lib.sh")), "build-universal-local.sh")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(contents)
}
