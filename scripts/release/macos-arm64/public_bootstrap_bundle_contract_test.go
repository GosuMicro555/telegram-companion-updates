package macosarm64_test

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestProductionPublicReleaseEmbedsPrebuiltBootstrapBeforeSigning(t *testing.T) {
	root := publicBootstrapScriptDirectory(t)
	makefile := readPublicBootstrapContractFile(t, filepath.Join(root, "..", "..", "..", "Makefile"))
	release := regexp.MustCompile(`(?m)^release-public-macos-arm64:\s*build-public-macos-arm64\s*\n((?:\t.*\n)+)`).FindStringSubmatch(makefile)
	if len(release) != 2 {
		t.Fatal("public release-creation target is missing")
	}

	last := -1
	for _, step := range []string{
		"preflight-public-release.sh",
		"embed-public-bootstrap-bundle.sh",
		"sign.sh",
		"notarize.sh",
	} {
		position := strings.Index(release[1], step)
		if position < 0 {
			t.Fatalf("release-public-macos-arm64 does not invoke %s", step)
		}
		if position <= last {
			t.Fatalf("release-public-macos-arm64 invokes %s out of order", step)
		}
		last = position
	}

	for _, required := range []string{
		"PUBLIC_BOOTSTRAP_BUNDLE_PATH ?=",
		"PUBLIC_BOOTSTRAP_BUNDLE_SHA256 ?=",
		"PUBLIC_BOOTSTRAP_BUNDLE_PATH='$(PUBLIC_BOOTSTRAP_BUNDLE_PATH)'",
		"PUBLIC_BOOTSTRAP_BUNDLE_SHA256='$(PUBLIC_BOOTSTRAP_BUNDLE_SHA256)'",
	} {
		if !strings.Contains(makefile, required) {
			t.Errorf("Makefile does not contain %q", required)
		}
	}
	for _, forbidden := range []string{
		"prepare-public-bootstrap-bundle.sh",
		"cmd/state-bundle",
		"TARGET_LICENSE_FILE",
		"publish.sh",
	} {
		if strings.Contains(release[1], forbidden) {
			t.Errorf("production public release creates, personalizes, or publishes the bootstrap artifact via %q", forbidden)
		}
	}
}

func TestProductionPublicReleaseRequiresSemanticBootstrapVerification(t *testing.T) {
	root := publicBootstrapScriptDirectory(t)
	makefile := readPublicBootstrapContractFile(t, filepath.Join(root, "..", "..", "..", "Makefile"))
	for _, required := range []string{
		"PUBLIC_BOOTSTRAP_SEED_EXPORT_FILE='$(PUBLIC_BOOTSTRAP_SEED_EXPORT_FILE)'",
		"PUBLIC_BOOTSTRAP_SEED_PASSWORD_FILE='$(PUBLIC_BOOTSTRAP_SEED_PASSWORD_FILE)'",
	} {
		if !strings.Contains(makefile, required) {
			t.Errorf("Makefile does not pass semantic verification input %q", required)
		}
	}

	guard := readPublicBootstrapContractFile(t, filepath.Join(root, "verify-public-release-bootstrap.sh"))
	for _, required := range []string{
		`require_env "PUBLIC_BOOTSTRAP_SEED_EXPORT_FILE"`,
		`require_env "PUBLIC_BOOTSTRAP_SEED_PASSWORD_FILE"`,
		`./scripts/release/macos-arm64/cmd/decrypt-build-seed`,
		`./scripts/release/macos-arm64/cmd/verify-public-bootstrap`,
		`./scripts/release/macos-arm64/cmd/verify-public-bootstrap-contents`,
		`rm -f -- "$PUBLIC_BOOTSTRAP_SEED_KEY_FILE"`,
		`trap cleanup EXIT`,
	} {
		if !strings.Contains(guard, required) {
			t.Errorf("semantic bootstrap guard does not contain %q", required)
		}
	}
	if strings.Contains(guard, "set -x") {
		t.Fatal("semantic bootstrap guard must not enable shell tracing")
	}

	for _, name := range []string{
		"preflight-public-release.sh",
		"sign.sh",
		"notarize.sh",
		"verify.sh",
		"preflight-public-publish.sh",
		"publish.sh",
	} {
		source := readPublicBootstrapContractFile(t, filepath.Join(root, name))
		if !strings.Contains(source, "verify-public-release-bootstrap.sh") {
			t.Errorf("%s can proceed without semantic bootstrap verification", name)
		}
	}
}

func TestPublicBootstrapEmbeddingFailsClosedAndPinsIdentity(t *testing.T) {
	root := publicBootstrapScriptDirectory(t)
	lib := readPublicBootstrapContractFile(t, filepath.Join(root, "lib.sh"))
	preflight := readPublicBootstrapContractFile(t, filepath.Join(root, "preflight-public-release.sh"))
	embed := readPublicBootstrapContractFile(t, filepath.Join(root, "embed-public-bootstrap-bundle.sh"))

	for _, required := range []string{
		`verify_public_bootstrap_bundle()`,
		`[[ -f "$path" ]] && [[ ! -L "$path" ]]`,
		`cmp -s <(printf 'TCSEED2\n') <(LC_ALL=C head -c 8 "$path")`,
		`[[ "$expected_sha256" =~ ^[a-f0-9]{64}$ ]]`,
		`verify_sha256 "$path" "$expected_sha256"`,
	} {
		if !strings.Contains(lib, required) {
			t.Errorf("lib.sh does not contain %q", required)
		}
	}
	for _, required := range []string{
		`require_env "PUBLIC_BOOTSTRAP_BUNDLE_PATH"`,
		`require_env "PUBLIC_BOOTSTRAP_BUNDLE_SHA256"`,
		`verify_public_bootstrap_bundle "$PUBLIC_BOOTSTRAP_BUNDLE_PATH" "$PUBLIC_BOOTSTRAP_BUNDLE_SHA256"`,
	} {
		if !strings.Contains(preflight, required) {
			t.Errorf("preflight-public-release.sh does not contain %q", required)
		}
	}
	if strings.Contains(lib, `if [[ -n "$expected_sha256" ]]`) {
		t.Fatal("public bootstrap verification must not allow an omitted SHA-256 identity")
	}

	sourceVerify := strings.Index(embed, `verify_public_bootstrap_bundle "$PUBLIC_BOOTSTRAP_BUNDLE_PATH" "$PUBLIC_BOOTSTRAP_BUNDLE_SHA256"`)
	installAt := strings.Index(embed, `install -m 0644 "$PUBLIC_BOOTSTRAP_BUNDLE_PATH" "$STAGED_BUNDLE_PATH"`)
	compareAt := strings.Index(embed, `cmp -s "$PUBLIC_BOOTSTRAP_BUNDLE_PATH" "$STAGED_BUNDLE_PATH"`)
	stagedVerify := strings.LastIndex(embed, `verify_public_bootstrap_bundle "$STAGED_BUNDLE_PATH" "$PUBLIC_BOOTSTRAP_BUNDLE_SHA256"`)
	if sourceVerify < 0 || installAt <= sourceVerify || compareAt <= installAt || stagedVerify <= compareAt {
		t.Fatal("embed script must verify the prebuilt source, copy it exactly, and verify the staged bundle")
	}
}

func TestSigningNotarizationAndFinalVerificationRequireEmbeddedBootstrap(t *testing.T) {
	root := publicBootstrapScriptDirectory(t)
	firstProtectedAction := map[string]string{
		"sign.sh":     `while IFS= read -r -d '' path`,
		"notarize.sh": `ditto -c -k --sequesterRsrc --keepParent "$APP_PATH" "$NOTARY_ZIP"`,
		"verify.sh":   `codesign --verify --strict --verbose=4 "$APP_PATH"`,
	}
	for _, name := range []string{"sign.sh", "notarize.sh", "verify.sh"} {
		source := readPublicBootstrapContractFile(t, filepath.Join(root, name))
		mainAt := strings.Index(source, "main() {")
		if mainAt < 0 {
			t.Errorf("%s has no main function", name)
			continue
		}
		mainBody := source[mainAt:]
		verifyAt := strings.Index(mainBody, `verify_public_bootstrap_bundle "$STAGED_BUNDLE_PATH" "$PUBLIC_BOOTSTRAP_BUNDLE_SHA256"`)
		actionAt := strings.Index(mainBody, firstProtectedAction[name])
		if verifyAt < 0 || actionAt <= verifyAt {
			t.Errorf("%s does not fail closed before its signing, notarization, or verification work", name)
		}
	}
}

func TestPublicBootstrapBundleContract(t *testing.T) {
	root := publicBootstrapScriptDirectory(t)
	prepare := readPublicBootstrapContractFile(t, filepath.Join(root, "prepare-public-bootstrap-bundle.sh"))
	embed := readPublicBootstrapContractFile(t, filepath.Join(root, "embed-public-bootstrap-bundle.sh"))

	for _, required := range []string{
		"set -euo pipefail",
		`require_env "RELEASE_VERSION"`,
		`require_env "PUBLIC_BOOTSTRAP_SEED_ID"`,
		`require_env "PUBLIC_BOOTSTRAP_BUNDLE_PATH"`,
		`-seed-id "$PUBLIC_BOOTSTRAP_SEED_ID"`,
		`-seed-key-file "$PUBLIC_BOOTSTRAP_SEED_KEY_FILE"`,
		`-seed-key-env "$PUBLIC_BOOTSTRAP_SEED_KEY_ENV"`,
		`"$GO_BIN" run ./cmd/state-bundle`,
		"sanitize-seeded-state.sql",
		"TCSEED2",
	} {
		if !strings.Contains(prepare, required) {
			t.Errorf("prepare-public-bootstrap-bundle.sh does not contain %q", required)
		}
	}
	for _, required := range []string{
		"set -euo pipefail",
		`require_env "PUBLIC_BOOTSTRAP_BUNDLE_PATH"`,
		`Contents/Resources/bootstrap-state/state.tcs`,
		`install -m 0644 "$PUBLIC_BOOTSTRAP_BUNDLE_PATH" "$STAGED_BUNDLE_PATH"`,
	} {
		if !strings.Contains(embed, required) {
			t.Errorf("embed-public-bootstrap-bundle.sh does not contain %q", required)
		}
	}

	for name, source := range map[string]string{
		"prepare-public-bootstrap-bundle.sh": prepare,
		"embed-public-bootstrap-bundle.sh":   embed,
		"build-dual-local.sh":                readPublicBootstrapContractFile(t, filepath.Join(root, "build-dual-local.sh")),
	} {
		for _, forbidden := range []string{
			"TARGET_LICENSE_FILE",
			"target license",
			"machine id",
			"machine-id",
			"package-seeded-private.sh",
			"verify-seeded-private.sh",
			"publish.sh",
			"gh release",
			"set -x",
		} {
			if strings.Contains(strings.ToLower(source), strings.ToLower(forbidden)) {
				t.Errorf("%s contains forbidden %q", name, forbidden)
			}
		}
	}
}

func TestPublicBootstrapPreparationUsesStrictOperationalSecretAllowlist(t *testing.T) {
	root := publicBootstrapScriptDirectory(t)
	prepare := readPublicBootstrapContractFile(t, filepath.Join(root, "prepare-public-bootstrap-bundle.sh"))

	for _, runtimeEntry := range []string{
		"application-state.bolt",
		"gotd-import-staging",
		"tdata",
		"sessions",
	} {
		if !strings.Contains(prepare, runtimeEntry) {
			t.Errorf("prepare-public-bootstrap-bundle.sh does not stage Telegram runtime entry %q", runtimeEntry)
		}
	}

	if !strings.Contains(prepare, "list-bootstrap-secret-names") {
		t.Fatal("prepare-public-bootstrap-bundle.sh does not enumerate the supported keyring secrets")
	}
	for _, operationalSecret := range []string{
		"scout-message-key",
		"outbound-target-key",
		"proxy-credentials-v1",
		"telegram-account-credentials-v1",
	} {
		if !strings.Contains(prepare, operationalSecret) {
			t.Errorf("prepare-public-bootstrap-bundle.sh does not explicitly allow operational secret %q", operationalSecret)
		}
		if strings.Contains(prepare, `-exclude-secret "`+operationalSecret+`"`) {
			t.Errorf("prepare-public-bootstrap-bundle.sh excludes required operational secret %q", operationalSecret)
		}
	}
	if !strings.Contains(prepare, `bundle_args+=( -exclude-secret "$secret_name" )`) {
		t.Error("prepare-public-bootstrap-bundle.sh does not exclude secrets outside the operational allowlist")
	}
	if !strings.Contains(prepare, "required operational bootstrap secret is not registered") {
		t.Error("prepare-public-bootstrap-bundle.sh does not fail closed when an operational secret disappears from the registry")
	}

	for _, excludedSecret := range []string{
		"backup-recovery-key-v1",
	} {
		if !strings.Contains(prepare, excludedSecret) {
			t.Errorf("prepare-public-bootstrap-bundle.sh does not explicitly identify excluded secret %q", excludedSecret)
		}
	}
	if strings.Contains(prepare, "tdata-import") {
		t.Error("prepare-public-bootstrap-bundle.sh includes the transient tdata import workspace")
	}
}

func publicBootstrapScriptDirectory(t *testing.T) string {
	t.Helper()
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate public bootstrap contract test")
	}
	return filepath.Dir(current)
}

func readPublicBootstrapContractFile(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(contents)
}
