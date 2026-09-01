package macosarm64_test

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

func TestMasterPublicParityHelperAndEvidenceContract(t *testing.T) {
	source := readFile(t, scriptPath("master-public-parity.sh"))

	for _, required := range []string{
		"set -euo pipefail",
		`case "${1:-}" in`,
		`write)`,
		`verify)`,
		`require_env "MASTER_APP_PATH"`,
		`require_env "RELEASE_VERSION"`,
		`require_env "SOURCE_SHA"`,
		`require_env "PUBLIC_APP_PATH"`,
		`chmod 600 "$PARITY_EVIDENCE_PATH"`,
		`stat -f '%Lp' "$PARITY_EVIDENCE_PATH"`,
		"resolve_evidence_target()",
		"require_canonical_evidence_path()",
		`PARITY_EVIDENCE_PATH="$PARITY_EVIDENCE_PATH"`,
		`MASTER_PUBLIC_PARITY_EVIDENCE_PATH`,
		`MASTER_APP_PATH`,
		`PUBLIC_APP_PATH`,
		`RELEASE_VERSION`,
		`SOURCE_SHA`,
		`write_parity_evidence`,
		`verify_parity_evidence`,
	} {
		requireContains(t, "master-public-parity.sh", source, required)
	}

	for _, forbidden := range []string{
		"SECRET",
		"TOKEN",
		"PASSWORD",
		"API_KEY",
		"PRIVATE_KEY",
		"LICENSE_PUBLIC_KEY",
		"SPARKLE_PRIVATE_KEY_FILE",
	} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("master-public-parity.sh contains secret-looking field %q", forbidden)
		}
	}

	writeAt := strings.Index(source, "write_parity_evidence")
	verifyAt := strings.Index(source, "verify_parity_evidence")
	if writeAt < 0 || verifyAt <= writeAt {
		t.Fatal("master-public-parity.sh must write evidence before verifying it")
	}
}

func TestMasterPublicParityHelperInitializesBundlePathsUnderNounset(t *testing.T) {
	source := readFile(t, scriptPath("master-public-parity.sh"))
	if strings.Contains(source, `local bundle="$1" contents="$bundle/Contents"`) {
		t.Fatal("require_app_bundle must not expand bundle-dependent locals in the same declaration under set -u")
	}
	for _, required := range []string{
		`local bundle="$1"`,
		`local contents="$bundle/Contents"`,
		`local executable="$contents/MacOS/$APP_EXECUTABLE"`,
		`local plist="$contents/Info.plist"`,
	} {
		requireContains(t, "master-public-parity.sh", source, required)
	}
}

func TestMasterPublicParityEqualPtConfigFixtureWritesOnlyItsDigest(t *testing.T) {
	source := readFile(t, scriptPath("master-public-parity.sh"))
	const fixtureSentinel = "fixture-pt-config-secret-must-not-escape"
	for _, required := range []string{
		`readonly PT_CONFIG_RELATIVE_PATH="Contents/Resources/tor/pluggable_transports/pt_config.json"`,
		"bundle_pt_config_sha256()",
		`master_pt_config_sha256="$(bundle_pt_config_sha256 "$MASTER_APP_PATH")"`,
		`public_pt_config_sha256="$(bundle_pt_config_sha256 "$PUBLIC_APP_PATH")"`,
		`[[ "$master_pt_config_sha256" == "$public_pt_config_sha256" ]] || fail "bridge configuration mismatch"`,
		`printf 'pt_config_sha256=%s\n' "$master_pt_config_sha256"`,
	} {
		requireContains(t, "master-public-parity.sh", source, required)
	}
	if strings.Contains(source, fixtureSentinel) || strings.Contains(source, `cat "$pt_config"`) {
		t.Fatal("parity evidence must never expose bridge configuration fixture content")
	}
}

func TestMasterPublicParityPtConfigFixturesRejectMismatchAndSymlink(t *testing.T) {
	source := readFile(t, scriptPath("master-public-parity.sh"))
	for _, required := range []string{
		`[[ -f "$pt_config" ]] && [[ ! -L "$pt_config" ]] || fail "bridge configuration is missing or unsafe"`,
		`shasum -a 256 -- "$pt_config" | awk '{print $1}'`,
		`[[ "$value" =~ ^[0-9a-f]{64}$ ]] || fail "evidence bridge configuration hash is malformed"`,
		`[[ "$evidence_pt_config_sha256" == "$master_pt_config_sha256" ]] || fail "bridge configuration evidence mismatch"`,
	} {
		requireContains(t, "master-public-parity.sh", source, required)
	}
}

func TestMasterPublicParityPtConfigBehavioralFixtures(t *testing.T) {
	const fixtureSentinel = "fixture-pt-config-content-must-not-escape"

	t.Run("equal configurations write and verify digest-only evidence", func(t *testing.T) {
		fixture := newParityPtConfigFixture(t, fixtureSentinel, fixtureSentinel)
		output, err := fixture.run("write", "verify")
		if err != nil {
			t.Fatalf("equal configuration parity failed: %v\n%s", err, output)
		}
		evidence := readFile(t, fixture.evidencePath)
		wantDigest := fmt.Sprintf("%x", sha256.Sum256([]byte(fixtureSentinel)))
		if !strings.Contains(evidence, "pt_config_sha256="+wantDigest+"\n") {
			t.Fatal("parity evidence does not record the shared pt_config digest")
		}
		assertPtConfigSentinelIsNotExposed(t, fixtureSentinel, output, evidence)
	})

	t.Run("legitimate external ancestor symlink is accepted", func(t *testing.T) {
		fixture := newParityPtConfigFixture(t, fixtureSentinel, fixtureSentinel)
		fixture.moveMasterUnderExternalAncestorSymlink(t)
		output, err := fixture.run("write", "verify")
		if err != nil {
			t.Fatalf("parity rejected a safe bundle beneath an external ancestor symlink: %v\n%s", err, output)
		}
		assertPtConfigSentinelIsNotExposed(t, fixtureSentinel, output, readFile(t, fixture.evidencePath))
	})

	t.Run("evidence beneath a legitimate external ancestor symlink is accepted", func(t *testing.T) {
		fixture := newParityPtConfigFixture(t, fixtureSentinel, fixtureSentinel)
		fixture.moveEvidenceUnderExternalAncestorSymlink(t)
		output, err := fixture.run("write", "verify")
		if err != nil {
			t.Fatalf("parity rejected evidence beneath a safe external ancestor symlink: %v\n%s", err, output)
		}
		assertPtConfigSentinelIsNotExposed(t, fixtureSentinel, output, readFile(t, fixture.evidencePath))
	})

	t.Run("evidence final symlink fails closed", func(t *testing.T) {
		fixture := newParityPtConfigFixture(t, fixtureSentinel, fixtureSentinel)
		fixture.replaceEvidenceWithSymlink(t)
		output, err := fixture.run("write")
		if err == nil {
			t.Fatal("evidence final symlink was accepted")
		}
		assertPtConfigSentinelIsNotExposed(t, fixtureSentinel, output, readOptionalFile(t, fixture.evidencePath))
	})

	t.Run("evidence direct parent symlink fails closed", func(t *testing.T) {
		fixture := newParityPtConfigFixture(t, fixtureSentinel, fixtureSentinel)
		fixture.moveEvidenceUnderDirectParentSymlink(t)
		output, err := fixture.run("write")
		if err == nil {
			t.Fatal("evidence direct parent symlink was accepted")
		}
		assertPtConfigSentinelIsNotExposed(t, fixtureSentinel, output, readOptionalFile(t, fixture.evidencePath))
	})

	t.Run("one byte mismatch fails closed", func(t *testing.T) {
		fixture := newParityPtConfigFixture(t, fixtureSentinel, fixtureSentinel+"x")
		output, err := fixture.run("write")
		if err == nil {
			t.Fatal("one-byte pt_config mismatch was accepted")
		}
		assertPtConfigSentinelIsNotExposed(t, fixtureSentinel, output, readOptionalFile(t, fixture.evidencePath))
	})

	t.Run("final configuration symlink fails closed", func(t *testing.T) {
		fixture := newParityPtConfigFixture(t, fixtureSentinel, fixtureSentinel)
		fixture.replaceFinalConfigWithSymlink(t)
		output, err := fixture.run("write")
		if err == nil {
			t.Fatal("final pt_config symlink was accepted")
		}
		assertPtConfigSentinelIsNotExposed(t, fixtureSentinel, output, readOptionalFile(t, fixture.evidencePath))
	})

	t.Run("ancestor symlink fails closed", func(t *testing.T) {
		fixture := newParityPtConfigFixture(t, fixtureSentinel, fixtureSentinel)
		fixture.replaceResourcesWithSymlink(t)
		output, err := fixture.run("write")
		if err == nil {
			t.Fatal("pt_config ancestor symlink was accepted")
		}
		assertPtConfigSentinelIsNotExposed(t, fixtureSentinel, output, readOptionalFile(t, fixture.evidencePath))
	})

	for _, testCase := range []struct {
		name   string
		suffix string
	}{
		{name: "current directory segment", suffix: "/./master-public.parity"},
		{name: "doubled separator", suffix: "//master-public.parity"},
		{name: "parent traversal", suffix: "/evidence-parent/../master-public.parity"},
	} {
		t.Run("noncanonical evidence path "+testCase.name+" fails closed", func(t *testing.T) {
			fixture := newParityPtConfigFixture(t, fixtureSentinel, fixtureSentinel)
			fixture.evidencePath = parityFixtureBashPath(t, parityFixtureBash(t), fixture.root) + testCase.suffix
			output, err := fixture.run("write")
			if err == nil {
				t.Fatal("noncanonical evidence path was accepted")
			}
			assertPtConfigSentinelIsNotExposed(t, fixtureSentinel, output, readOptionalFile(t, fixture.evidencePath))
			assertParityFixturePathIsNotExposed(t, fixture.evidencePath, output)
		})
	}

	t.Run("disguised evidence direct parent symlink fails closed", func(t *testing.T) {
		fixture := newParityPtConfigFixture(t, fixtureSentinel, fixtureSentinel)
		fixture.moveEvidenceUnderDirectParentSymlink(t)
		fixture.evidencePath = parityFixtureBashPath(t, parityFixtureBash(t), fixture.root) + "/evidence-parent-link/./master-public.parity"
		output, err := fixture.run("write")
		if err == nil {
			t.Fatal("disguised evidence direct parent symlink was accepted")
		}
		assertPtConfigSentinelIsNotExposed(t, fixtureSentinel, output, readOptionalFile(t, fixture.evidencePath))
		assertParityFixturePathIsNotExposed(t, fixture.evidencePath, output)
	})
}

type parityPtConfigFixture struct {
	root         string
	masterApp    string
	publicApp    string
	evidencePath string
}

func newParityPtConfigFixture(t *testing.T, masterConfig, publicConfig string) parityPtConfigFixture {
	t.Helper()
	root := t.TempDir()
	fixture := parityPtConfigFixture{
		root:         root,
		masterApp:    filepath.Join(root, "master", "Telegram Companion.app"),
		publicApp:    filepath.Join(root, "public", "Telegram Companion.app"),
		evidencePath: filepath.Join(root, "master-public.parity"),
	}
	writePtConfigFixture(t, fixture.masterApp, masterConfig)
	writePtConfigFixture(t, fixture.publicApp, publicConfig)
	return fixture
}

func writePtConfigFixture(t *testing.T, appPath, content string) {
	t.Helper()
	path := filepath.Join(appPath, "Contents", "Resources", "tor", "pluggable_transports", "pt_config.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create pt_config fixture directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write pt_config fixture: %v", err)
	}
}

func (fixture parityPtConfigFixture) replaceFinalConfigWithSymlink(t *testing.T) {
	t.Helper()
	configPath := filepath.Join(fixture.publicApp, "Contents", "Resources", "tor", "pluggable_transports", "pt_config.json")
	targetPath := filepath.Join(fixture.root, "outside-config")
	if err := os.WriteFile(targetPath, []byte("synthetic-target"), 0o600); err != nil {
		t.Fatalf("write symlink target: %v", err)
	}
	createParityFixtureSymlink(t, configPath, targetPath)
}

func (fixture parityPtConfigFixture) replaceResourcesWithSymlink(t *testing.T) {
	t.Helper()
	resourcesPath := filepath.Join(fixture.publicApp, "Contents", "Resources")
	targetPath := filepath.Join(fixture.root, "outside-resources")
	if err := os.Rename(resourcesPath, targetPath); err != nil {
		t.Fatalf("move resources outside fixture bundle: %v", err)
	}
	createParityFixtureSymlink(t, resourcesPath, targetPath)
}

func (fixture *parityPtConfigFixture) moveMasterUnderExternalAncestorSymlink(t *testing.T) {
	t.Helper()
	realAncestor := filepath.Join(fixture.root, "external-ancestor-real")
	linkAncestor := filepath.Join(fixture.root, "external-ancestor-link")
	movedApp := filepath.Join(realAncestor, "master", "Telegram Companion.app")
	if err := os.MkdirAll(filepath.Dir(movedApp), 0o700); err != nil {
		t.Fatalf("create external ancestor fixture: %v", err)
	}
	if err := os.Rename(fixture.masterApp, movedApp); err != nil {
		t.Fatalf("move bundle beneath external ancestor fixture: %v", err)
	}
	createParityFixtureSymlink(t, linkAncestor, realAncestor)
	fixture.masterApp = parityFixtureBashPath(t, parityFixtureBash(t), fixture.root) + "/external-ancestor-link/master/Telegram Companion.app"
}

func (fixture *parityPtConfigFixture) moveEvidenceUnderExternalAncestorSymlink(t *testing.T) {
	t.Helper()
	realAncestor := filepath.Join(fixture.root, "evidence-ancestor-real")
	linkAncestor := filepath.Join(fixture.root, "evidence-ancestor-link")
	if err := os.MkdirAll(filepath.Join(realAncestor, "evidence-parent"), 0o700); err != nil {
		t.Fatalf("create external evidence ancestor fixture: %v", err)
	}
	createParityFixtureSymlink(t, linkAncestor, realAncestor)
	fixture.evidencePath = parityFixtureBashPath(t, parityFixtureBash(t), fixture.root) + "/evidence-ancestor-link/evidence-parent/master-public.parity"
}

func (fixture *parityPtConfigFixture) replaceEvidenceWithSymlink(t *testing.T) {
	t.Helper()
	targetPath := filepath.Join(fixture.root, "outside-evidence")
	if err := os.WriteFile(targetPath, []byte("synthetic-evidence-target"), 0o600); err != nil {
		t.Fatalf("write evidence symlink target: %v", err)
	}
	createParityFixtureSymlink(t, fixture.evidencePath, targetPath)
}

func (fixture *parityPtConfigFixture) moveEvidenceUnderDirectParentSymlink(t *testing.T) {
	t.Helper()
	realParent := filepath.Join(fixture.root, "evidence-parent-real")
	linkParent := filepath.Join(fixture.root, "evidence-parent-link")
	if err := os.MkdirAll(realParent, 0o700); err != nil {
		t.Fatalf("create evidence direct parent fixture: %v", err)
	}
	createParityFixtureSymlink(t, linkParent, realParent)
	fixture.evidencePath = filepath.Join(linkParent, "master-public.parity")
}

func createParityFixtureSymlink(t *testing.T, linkPath, targetPath string) {
	t.Helper()
	if err := os.Remove(linkPath); err != nil && !os.IsNotExist(err) {
		t.Fatalf("remove synthetic symlink fixture target: %v", err)
	}
	if err := os.Symlink(targetPath, linkPath); err != nil {
		if runtime.GOOS == "windows" && errors.Is(err, syscall.Errno(1314)) {
			t.Skip("native symlink privilege is unavailable on this Windows test host")
		}
		t.Fatalf("create synthetic symlink fixture: %v", err)
	}
}

func (fixture parityPtConfigFixture) run(actions ...string) (string, error) {
	bash := parityFixtureBash(nil)
	script := `
if [[ "${PARITY_FIXTURE_WINDOWS_HASHER:-}" == "1" ]]; then
  shasum() {
    local file
    for file in "$@"; do :; done
    /c/Windows/System32/certutil.exe -hashfile "$file" SHA256 |
      awk '/^[0-9A-Fa-f ]+$/ { gsub(/[[:space:]]/, ""); print tolower($0); exit }'
  }
fi
source "$1"
resolve_source_sha() { printf '%s\n' "$SOURCE_SHA"; }
require_app_bundle() { [[ -d "$1" ]] && [[ ! -L "$1" ]] || fail "application bundle is unsafe"; }
bundle_sha256() { printf '%064d\n' 0; }
stat() { printf '%s\n' 600; }
shift
for action in "$@"; do
  case "$action" in
    write) write_parity_evidence ;;
    verify) verify_parity_evidence ;;
    *) exit 64 ;;
  esac
done
`
	args := append([]string{"-c", script, "parity-fixture", parityFixtureBashPath(nil, bash, scriptPath("master-public-parity.sh"))}, actions...)
	command := exec.Command(bash, args...)
	command.Env = append(os.Environ(),
		"PARITY_EVIDENCE_PATH="+parityFixtureBashPath(nil, bash, fixture.evidencePath),
		"MASTER_APP_PATH="+parityFixtureBashPath(nil, bash, fixture.masterApp),
		"PUBLIC_APP_PATH="+parityFixtureBashPath(nil, bash, fixture.publicApp),
		"RELEASE_VERSION=0.8.6",
		"SOURCE_SHA=0000000000000000000000000000000000000000",
		"SOURCE_ROOT="+parityFixtureBashPath(nil, bash, fixture.root),
	)
	if runtime.GOOS == "windows" {
		command.Env = append(command.Env, "PARITY_FIXTURE_WINDOWS_HASHER=1")
	}
	output, err := command.CombinedOutput()
	return string(output), err
}

func parityFixtureBash(t *testing.T) string {
	if bash, err := exec.LookPath("bash"); err == nil {
		return bash
	}
	for _, candidate := range []string{
		`C:\Program Files\Git\bin\bash.exe`,
		`C:\Program Files\Git\usr\bin\bash.exe`,
	} {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	if t != nil {
		t.Fatal("bash is required for parity fixture tests")
	}
	panic("bash is required for parity fixture tests")
}

func parityFixtureBashPath(t *testing.T, bash, path string) string {
	if t != nil {
		t.Helper()
	}
	if runtime.GOOS != "windows" {
		return path
	}
	if strings.HasPrefix(path, "/") {
		return path
	}
	command := exec.Command(bash, "-c", `cygpath -u -- "$1"`, "parity-fixture-cygpath", path)
	output, err := command.CombinedOutput()
	if err != nil {
		if t != nil {
			t.Fatalf("convert synthetic fixture path for Git Bash: %v\n%s", err, output)
		}
		panic(fmt.Sprintf("convert synthetic fixture path for Git Bash: %v\n%s", err, output))
	}
	return strings.TrimSpace(string(output))
}

func assertPtConfigSentinelIsNotExposed(t *testing.T, sentinel string, values ...string) {
	t.Helper()
	for _, value := range values {
		if strings.Contains(value, sentinel) {
			t.Fatal("pt_config fixture content escaped into output or evidence")
		}
	}
}

func assertParityFixturePathIsNotExposed(t *testing.T, path string, values ...string) {
	t.Helper()
	for _, value := range values {
		if strings.Contains(value, path) {
			t.Fatal("parity fixture path escaped into output")
		}
	}
}

func readOptionalFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatalf("read optional fixture evidence: %v", err)
	}
	return string(data)
}

func TestParityRejectsDirtySourceBeforeUsingCommitSHA(t *testing.T) {
	lib := readFile(t, scriptPath("lib.sh"))
	parity := readFile(t, scriptPath("master-public-parity.sh"))
	dual := readDualLocalScript(t)

	// HEAD alone is unchanged by staged, unstaged, and untracked files.  The
	// release gate must therefore reject any non-empty porcelain status before
	// it records or verifies source_sha.  Untracked files are rejected too:
	// otherwise a file copied into the source tree could still enter a build
	// without being represented by the commit identity.
	for _, required := range []string{
		"require_clean_source_tree()",
		`git -C "$SOURCE_ROOT" status --porcelain=v1 --untracked-files=all`,
		"source tree is dirty",
	} {
		requireContains(t, "lib.sh", lib, required)
	}

	resolveAt := strings.Index(parity, "resolve_source_sha()")
	guardAt := strings.Index(parity[resolveAt:], "require_clean_source_tree")
	revParseAt := strings.Index(parity[resolveAt:], `git -C "$SOURCE_ROOT" rev-parse HEAD`)
	if resolveAt < 0 || guardAt < 0 || revParseAt < 0 || guardAt > revParseAt {
		t.Fatal("parity source SHA must reject dirty source before resolving HEAD")
	}

	mainAt := strings.Index(dual, "main() {")
	if mainAt < 0 {
		t.Fatal("build-dual-local.sh must define main")
	}
	main := dual[mainAt:]
	guardAt = strings.Index(main, "require_clean_source_tree")
	revParseAt = strings.Index(main, `git -C "$REPO_ROOT" rev-parse HEAD`)
	if guardAt < 0 || revParseAt < 0 || guardAt > revParseAt {
		t.Fatal("dual build must reject dirty source before recording source_sha")
	}
}

func TestDualBuildCapturesAndRechecksSourceSHA(t *testing.T) {
	source := readDualLocalScript(t)

	for _, required := range []string{
		`source_sha_before_master="$(git -C "$REPO_ROOT" rev-parse HEAD)"`,
		`source_sha_after_master="$(git -C "$REPO_ROOT" rev-parse HEAD)"`,
		`source_sha_after_public="$(git -C "$REPO_ROOT" rev-parse HEAD)"`,
		`bash "$SCRIPT_DIR/master-public-parity.sh" write`,
		`bash "$SCRIPT_DIR/master-public-parity.sh" verify`,
	} {
		requireContains(t, "build-dual-local.sh", source, required)
	}

	mainAt := strings.Index(source, "main() {")
	if mainAt < 0 {
		t.Fatal("build-dual-local.sh must define main")
	}
	main := source[mainAt:]
	beforeMaster := strings.Index(main, `source_sha_before_master="$(git -C "$REPO_ROOT" rev-parse HEAD)"`)
	publicBuild := strings.Index(main, "\n  build_public_private_dmg\n")
	afterPublic := strings.Index(main, `source_sha_after_public="$(git -C "$REPO_ROOT" rev-parse HEAD)"`)
	masterBuild := strings.Index(main, "\n  build_and_install_internal\n")
	afterMaster := strings.Index(main, `source_sha_after_master="$(git -C "$REPO_ROOT" rev-parse HEAD)"`)
	writeAt := strings.Index(main, `bash "$SCRIPT_DIR/master-public-parity.sh" write`)
	verifyAt := strings.Index(main, `bash "$SCRIPT_DIR/master-public-parity.sh" verify`)
	deliverAt := strings.Index(main, "\n  deliver_artifacts\n")
	if beforeMaster < 0 || publicBuild <= beforeMaster || afterPublic <= publicBuild ||
		masterBuild <= afterPublic || afterMaster <= masterBuild || writeAt <= afterMaster ||
		verifyAt <= writeAt || deliverAt <= verifyAt {
		t.Fatal("build-dual-local.sh must capture HEAD, build/recheck public then master, run parity helper, then deliver artifacts")
	}
}

func TestDualBuildUsesPrivateDurableParityStaging(t *testing.T) {
	source := readDualLocalScript(t)

	for _, required := range []string{
		`PARITY_CACHE_ROOT="${PARITY_CACHE_ROOT:-${MASTER_PUBLIC_PARITY_ROOT:-$HOME/.cache/telegram-companion-release/master-public-parity}}"`,
		`PARITY_STAGING_ROOT`,
		`PARITY_VERSION_ROOT="$PARITY_CACHE_ROOT/versions/$PUBLIC_VERSION"`,
		`PARITY_FINAL_ROOT="$PARITY_VERSION_ROOT/$SOURCE_SHA"`,
		`PARITY_CURRENT_POINTER="$PARITY_CACHE_ROOT/current/$PUBLIC_VERSION"`,
		`chmod 700 "$PARITY_STAGING_ROOT"`,
		`PARITY_EVIDENCE_PATH="$PARITY_STAGING_ROOT/master-public.parity"`,
		`MASTER_APP_PATH="$PARITY_STAGING_ROOT/master/$APP_NAME.app"`,
		`PUBLIC_PARITY_APP_PATH="$PARITY_STAGING_ROOT/public/$APP_NAME.app"`,
		`ditto "$INSTALL_PATH" "$MASTER_APP_PATH"`,
		`ditto "$PUBLIC_APP_PATH" "$PUBLIC_PARITY_APP_PATH"`,
		`SOURCE_ROOT="$REPO_ROOT"`,
		`mv "$PARITY_STAGING_ROOT" "$PARITY_FINAL_ROOT"`,
		`mv -fh "$PARITY_POINTER_TMP" "$PARITY_CURRENT_POINTER"`,
	} {
		requireContains(t, "build-dual-local.sh", source, required)
	}
	if strings.Contains(source, `mv -f "$PARITY_POINTER_TMP" "$PARITY_CURRENT_POINTER"`) ||
		strings.Contains(source, `mv -f "$restore_pointer_tmp" "$PARITY_CURRENT_POINTER"`) {
		t.Fatal("parity pointer replacement must not follow an existing directory symlink on macOS")
	}

	mainAt := strings.Index(source, "main() {")
	if mainAt < 0 {
		t.Fatal("build-dual-local.sh must define main")
	}
	main := source[mainAt:]
	publicCopyAt := strings.Index(main, `ditto "$PUBLIC_APP_PATH" "$PUBLIC_PARITY_APP_PATH"`)
	writeAt := strings.Index(main, `bash "$SCRIPT_DIR/master-public-parity.sh" write`)
	verifyAt := strings.Index(main, `bash "$SCRIPT_DIR/master-public-parity.sh" verify`)
	promoteAt := strings.Index(main, `promote_parity_snapshot`)
	deliverAt := strings.Index(main, "\n  deliver_artifacts\n")
	if publicCopyAt < 0 || writeAt <= publicCopyAt || verifyAt <= writeAt || promoteAt <= verifyAt || deliverAt <= promoteAt {
		t.Fatal("parity must snapshot the unseeded public app, write and verify evidence, atomically promote, then deliver")
	}
	if strings.Contains(source, `PARITY_EVIDENCE_PATH="$WORK_DIR`) ||
		strings.Contains(source, `MASTER_APP_PATH="$WORK_DIR`) ||
		strings.Contains(source, `PUBLIC_APP_PATH="$PUBLIC_RELEASE_ROOT"`+"/private") {
		t.Fatal("parity snapshots/evidence must not be rooted in WORK_DIR or the seeded private app")
	}
	if strings.Contains(main, `PRIVATE_APP_PATH="$PRIVATE_APP_PATH"`) {
		t.Fatal("PRIVATE_APP_PATH must not be passed to the parity helper")
	}
}

func TestPublicationRequiresMasterParity(t *testing.T) {
	preflight := readFile(t, scriptPath("preflight-public-publish.sh"))
	publish := readFile(t, scriptPath("publish.sh"))

	for _, required := range []string{
		`require_env "PARITY_EVIDENCE_PATH"`,
		`bash "$SCRIPT_DIR/master-public-parity.sh" verify`,
	} {
		requireContains(t, "preflight-public-publish.sh", preflight, required)
		requireContains(t, "publish.sh", publish, required)
	}

	preflightVerifyAt := strings.Index(preflight, `bash "$SCRIPT_DIR/master-public-parity.sh" verify`)
	preflightMutationAt := strings.Index(preflight, `gh release download`)
	if preflightMutationAt > -1 && preflightVerifyAt > preflightMutationAt {
		t.Fatal("preflight-public-publish.sh must verify parity evidence before release download or mutation")
	}

	mainAt := strings.Index(publish, "main() {")
	if mainAt < 0 {
		t.Fatal("publish.sh must define main")
	}
	main := publish[mainAt:]
	publishVerifyAt := strings.Index(main, `bash "$SCRIPT_DIR/master-public-parity.sh" verify`)
	publishUploadAt := strings.Index(main, "publish_payload_assets")
	publishAppcastAt := strings.Index(main, "publish_appcast_last")
	if publishVerifyAt < 0 || publishUploadAt <= publishVerifyAt || publishAppcastAt <= publishVerifyAt {
		t.Fatal("publish.sh must verify parity evidence before upload or appcast mutation")
	}
}

func TestPublicationResolvesOnlyTheDurablePrivateParitySnapshot(t *testing.T) {
	lib := readFile(t, scriptPath("lib.sh"))
	for _, required := range []string{
		`MASTER_PUBLIC_PARITY_ROOT="${MASTER_PUBLIC_PARITY_ROOT:-${PARITY_CACHE_ROOT:-$HOME/.cache/telegram-companion-release/master-public-parity}}"`,
		"resolve_master_public_parity()",
		`current/$RELEASE_VERSION`,
		`read_master_public_parity_source_sha()`,
		`SOURCE_SHA="$candidate"`,
		`master/public parity pointer targets an unexpected path`,
		`PARITY_CACHE_ROOT and MASTER_PUBLIC_PARITY_ROOT must match`,
		`target_suffix=`,
		`target_suffix" =~ ^[0-9a-f]{40}$`,
		`current="$(dirname "$pointer")"`,
	} {
		requireContains(t, "lib.sh", lib, required)
	}
	for _, name := range []string{"preflight-public-publish.sh", "publish.sh"} {
		source := readFile(t, scriptPath(name))
		for _, required := range []string{
			"resolve_master_public_parity",
			"read_master_public_parity_source_sha",
			`require_env "SOURCE_SHA"`,
			`bash "$SCRIPT_DIR/master-public-parity.sh" verify`,
		} {
			requireContains(t, name, source, required)
		}
		mainAt := strings.Index(source, "main() {")
		if mainAt < 0 {
			t.Fatalf("%s must define main", name)
		}
		main := source[mainAt:]
		resolveAt := strings.Index(main, "resolve_master_public_parity")
		readAt := strings.Index(main, "read_master_public_parity_source_sha")
		verifyAt := strings.Index(main, `bash "$SCRIPT_DIR/master-public-parity.sh" verify`)
		if resolveAt < 0 || readAt <= resolveAt || verifyAt <= readAt {
			t.Fatalf("%s must resolve the private pointer, read the evidence SHA, then verify parity", name)
		}
	}
}

func TestPublicationBindsBootstrapVerificationToReleaseWorkspace(t *testing.T) {
	lib := readFile(t, scriptPath("lib.sh"))
	for _, required := range []string{
		"resolve_public_bootstrap_verification_path()",
		`require_env "STAGED_BUNDLE_PATH"`,
		`STAGED_BUNDLE_PATH must be inside RELEASE_ROOT`,
	} {
		requireContains(t, "lib.sh", lib, required)
	}
	for _, name := range []string{"preflight-public-publish.sh", "publish.sh"} {
		source := readFile(t, scriptPath(name))
		requireContains(t, name, source, `PUBLIC_BOOTSTRAP_VERIFY_BUNDLE_PATH="$STAGED_BUNDLE_PATH"`)
		mainAt := strings.Index(source, "main() {")
		if mainAt < 0 {
			t.Fatalf("%s must define main", name)
		}
		main := source[mainAt:]
		resolveAt := strings.Index(main, "resolve_master_public_parity")
		bootstrapPathAt := strings.Index(main, "resolve_public_bootstrap_verification_path")
		verifyAt := strings.Index(main, `PUBLIC_BOOTSTRAP_VERIFY_BUNDLE_PATH="$STAGED_BUNDLE_PATH"`)
		if resolveAt < 0 || bootstrapPathAt <= resolveAt || verifyAt <= bootstrapPathAt {
			t.Fatalf("%s must bind the staged bootstrap path before verification", name)
		}
	}
}

func TestPublicationDoesNotClobberImmutableReleaseAssets(t *testing.T) {
	source := readFile(t, scriptPath("publish.sh"))

	// A release asset is part of the signed update identity.  Replacing an
	// existing asset under the same version would let a later build silently
	// invalidate an appcast that clients have already cached.  The publisher
	// must compare an existing remote asset byte-for-byte, upload only missing
	// assets, and fail closed on a mismatch.
	for _, required := range []string{
		"ensure_immutable_release_assets()",
		`gh release view "v$RELEASE_VERSION" --repo "$RELEASE_REPOSITORY" --json assets`,
		`gh release download "v$RELEASE_VERSION" --repo "$RELEASE_REPOSITORY"`,
		"cmp -s",
		"release asset differs from local artifact",
		"missing_assets",
	} {
		requireContains(t, "publish.sh", source, required)
	}

	uploadAt := strings.Index(source, "publish_payload_assets()")
	if uploadAt < 0 {
		t.Fatal("publish.sh must define publish_payload_assets")
	}
	uploadEnd := strings.Index(source[uploadAt:], "\n}\n")
	if uploadEnd < 0 {
		t.Fatal("publish_payload_assets body is unavailable")
	}
	uploadBody := source[uploadAt : uploadAt+uploadEnd]
	if strings.Contains(uploadBody, "--clobber") {
		t.Fatal("publish_payload_assets must never clobber an existing immutable asset")
	}

	ensureAt := strings.Index(source, "ensure_immutable_release_assets()")
	ensureCallAt := strings.Index(uploadBody, "ensure_immutable_release_assets")
	if ensureAt < 0 || ensureCallAt < 0 {
		t.Fatal("publish_payload_assets must use the immutable asset guard")
	}
	if ensureCallAt == 0 {
		t.Fatal("publish_payload_assets must call the immutable asset guard")
	}

	mainAt := strings.Index(source, "main() {")
	if mainAt < 0 {
		t.Fatal("publish.sh must define main")
	}
	main := source[mainAt:]
	publishCallAt := strings.Index(main, "publish_payload_assets")
	appcastAt := strings.Index(main, "publish_appcast_last")
	if publishCallAt < 0 || appcastAt <= publishCallAt {
		t.Fatal("immutable asset guard must complete before appcast publication")
	}
}

func TestParityMismatchAndVersionMismatchFailClosedBeforeUpload(t *testing.T) {
	lib := readFile(t, scriptPath("lib.sh"))
	parity := readFile(t, scriptPath("master-public-parity.sh"))
	publish := readFile(t, scriptPath("publish.sh"))

	// These predicates are the rejection cases for a synthetic pair whose
	// pointer/evidence source SHAs differ or whose bundle version is stale.
	for _, testCase := range []struct {
		name   string
		source string
		marker string
	}{
		{
			name:   "pointer and evidence source SHA differ",
			source: lib,
			marker: `[[ "$candidate" == "$PARITY_POINTER_SOURCE_SHA" ]] ||`,
		},
		{
			name:   "short version differs from release",
			source: parity,
			marker: `[[ "$short_version" == "$RELEASE_VERSION" ]] || fail "bundle version mismatch"`,
		},
		{
			name:   "build version differs from release",
			source: parity,
			marker: `[[ "$build_version" == "$RELEASE_VERSION" ]] || fail "bundle build version mismatch"`,
		},
	} {
		if !strings.Contains(testCase.source, testCase.marker) {
			t.Errorf("%s rejection predicate is missing", testCase.name)
		}
	}

	assignAt := strings.Index(lib, `PARITY_POINTER_SOURCE_SHA="$target_suffix"`)
	compareAt := strings.Index(lib, `[[ "$candidate" == "$PARITY_POINTER_SOURCE_SHA" ]]`)
	if assignAt < 0 || compareAt <= assignAt {
		t.Fatal("parity must bind the pointer SHA before comparing evidence source_sha")
	}

	mainAt := strings.Index(publish, "main() {")
	if mainAt < 0 {
		t.Fatal("publish.sh must define main")
	}
	main := publish[mainAt:]
	verifyAt := strings.Index(main, `bash "$SCRIPT_DIR/master-public-parity.sh" verify`)
	uploadAt := strings.Index(main, "publish_payload_assets")
	if verifyAt < 0 || uploadAt <= verifyAt {
		t.Fatal("mismatched parity/version fixtures must be rejected before upload")
	}
}

func TestAppcastLookupFailsClosedAndReadsBackAfterPut(t *testing.T) {
	source := readFile(t, scriptPath("publish.sh"))
	functionAt := strings.Index(source, "publish_appcast_last()")
	if functionAt < 0 {
		t.Fatal("publish.sh must define publish_appcast_last")
	}
	body := source[functionAt:]
	for _, required := range []string{
		"telegram-companion-appcast-lookup",
		"remote appcast lookup failed",
		"HTTP[[:space:]]+404",
		"remote appcast readback failed",
		"remote appcast differs from local appcast",
		`readback_json="$(gh api --method GET "$endpoint" -f "ref=$APPCAST_BRANCH"`,
	} {
		requireContains(t, "publish.sh", body, required)
	}

	lookupAt := strings.Index(body, `gh api --method GET "$endpoint" -f "ref=$APPCAST_BRANCH"`)
	putAt := strings.Index(body, `gh api --method PUT "$endpoint"`)
	readbackAt := strings.LastIndex(body, `readback_json="$(gh api --method GET "$endpoint" -f "ref=$APPCAST_BRANCH"`)
	if lookupAt < 0 || putAt <= lookupAt || readbackAt <= putAt {
		t.Fatal("appcast lookup must fail closed, PUT, then perform a remote readback")
	}
	if strings.Index(body, "remote appcast lookup failed") > putAt {
		t.Fatal("appcast lookup failures must be handled before any PUT")
	}
}
