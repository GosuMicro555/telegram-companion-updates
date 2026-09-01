package macosarm64_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDualLocalBuildContract(t *testing.T) {
	source := readDualLocalScript(t)

	for _, required := range []string{
		"set -euo pipefail",
		`require_env "PUBLIC_VERSION"`,
		`require_env "LICENSE_PUBLIC_KEY"`,
		`require_env "APPCAST_URL"`,
		`require_env "HOST_DOWNLOADS"`,
		`"$WAILS_BIN" build -clean -skipbindings -platform "darwin/arm64" -tags desktop`,
		`TELEGRAM_COMPANION_APP_PATH="$built_app"`,
		`bash "$REPO_ROOT/scripts/package_macos_dev_runtime.sh"`,
		`Set :CFBundleShortVersionString $PUBLIC_VERSION`,
		`Set :CFBundleVersion $PUBLIC_VERSION`,
		`INSTALL_PATH="/Applications/Telegram Companion.app"`,
		`mv "$INSTALL_PATH" "$INSTALL_BACKUP_PATH"`,
		`mv "$INSTALL_STAGE_PATH" "$INSTALL_PATH"`,
		`codesign --force --sign - "$built_app"`,
		`TELEGRAM_COMPANION_RUNTIME_BUNDLE="$PUBLIC_APP_PATH"`,
		`restore_installed_app`,
		`WORK_DIR="$(cd "$WORK_DIR" && pwd -P)"`,
		`BUILD_TAGS="desktop,public_macos_arm64"`,
		`PERSISTENT_PAYLOAD_CACHE="${PERSISTENT_PAYLOAD_CACHE:-$HOME/.cache/telegram-companion-release/payloads}"`,
		`PAYLOAD_CACHE="$PERSISTENT_PAYLOAD_CACHE"`,
		`bash "$SCRIPT_DIR/preflight-private.sh"`,
		`bash "$SCRIPT_DIR/build-stage.sh"`,
		`bash "$SCRIPT_DIR/sign-adhoc.sh"`,
		`bash "$SCRIPT_DIR/package-private.sh"`,
		`bash "$SCRIPT_DIR/verify-private.sh"`,
		`require_env "PUBLIC_BOOTSTRAP_BUNDLE_PATH"`,
		`require_env "PUBLIC_BOOTSTRAP_BUNDLE_SHA256"`,
		`PUBLIC_BOOTSTRAP_BUNDLE_SHA256="$PUBLIC_BOOTSTRAP_BUNDLE_SHA256"`,
		`bash "$SCRIPT_DIR/embed-public-bootstrap-bundle.sh"`,
		`host_artifact="$HOST_DOWNLOADS/$(basename "$artifact")"`,
		`ditto "$artifact" "$host_artifact"`,
		`sha256_file()`,
		`shasum -a 256 -- "$1"`,
		`verify_artifact_copy "$artifact" "$host_artifact"`,
		`UTM_GUEST_SSH`,
		`scp`,
		`die "guest delivery requires ssh and scp"`,
		`die "UTM guest SSH is unavailable`,
		`die "guest delivery failed`,
		`remote_digest=`,
		`[[ "$local_digest" == "$remote_digest" ]]`,
		`guest delivery skipped`,
		`trap cleanup EXIT`,
	} {
		if !strings.Contains(source, required) {
			t.Errorf("build-dual-local.sh does not contain %q", required)
		}
	}

	if strings.Contains(source, `PAYLOAD_CACHE="$PUBLIC_RELEASE_ROOT/payload-cache"`) {
		t.Fatal("dual build must not discard the verified persistent payload cache")
	}

	for _, forbidden := range []string{
		"set -x",
		`echo "$LICENSE_PUBLIC_KEY"`,
		`printf '%s' "$LICENSE_PUBLIC_KEY"`,
		"TARGET_LICENSE_FILE",
		"Machine ID",
		"package-seeded-private.sh",
		"verify-seeded-private.sh",
		"notarytool",
		"stapler",
		"publish.sh",
		"appcast.sh",
		"guest delivery incomplete",
		"guest delivery skipped: ssh or scp is unavailable",
		"guest delivery skipped: UTM guest SSH is unavailable",
	} {
		if strings.Contains(source, forbidden) {
			t.Errorf("build-dual-local.sh contains forbidden operation %q", forbidden)
		}
	}

	internalBody := shellFunctionBody(source, "build_and_install_internal")
	internalBuildAt := strings.Index(internalBody, `"$WAILS_BIN" build -clean -skipbindings -platform "darwin/arm64" -tags desktop`)
	installAt := strings.Index(internalBody, `mv "$INSTALL_STAGE_PATH" "$INSTALL_PATH"`)
	main := source[strings.Index(source, "main() {"):]
	publicAt := strings.Index(main, `if [[ "$MANUAL_UNSEEDED_ONLY" == "1" ]]; then`)
	internalAt := strings.Index(main, "\n  build_and_install_internal\n")
	deliveryAt := strings.Index(main, "\n  deliver_artifacts\n")
	if internalBuildAt < 0 || installAt <= internalBuildAt || publicAt < 0 || internalAt <= publicAt || deliveryAt <= internalAt {
		t.Fatal("dual local steps must build public, reuse its runtime for internal, install, then deliver artifacts")
	}
}

func TestDualLocalBuildUsesExactPublicTorRuntimeForMaster(t *testing.T) {
	source := readDualLocalScript(t)
	internalBody := shellFunctionBody(source, "build_and_install_internal")
	if internalBody == "" {
		t.Fatal("build_and_install_internal function is missing")
	}
	for _, required := range []string{
		`[[ -d "$PUBLIC_APP_PATH" && ! -L "$PUBLIC_APP_PATH" ]]`,
		`TELEGRAM_COMPANION_RUNTIME_BUNDLE="$PUBLIC_APP_PATH"`,
		`bash "$REPO_ROOT/scripts/package_macos_dev_runtime.sh"`,
	} {
		requireContains(t, "build_and_install_internal", internalBody, required)
	}
	requireContains(t, "build_and_install_internal", internalBody,
		"TELEGRAM_COMPANION_APP_PATH=\"$built_app\" \\\n  TELEGRAM_COMPANION_RUNTIME_BUNDLE=\"$PUBLIC_APP_PATH\" \\\n    bash \"$REPO_ROOT/scripts/package_macos_dev_runtime.sh\"")
	if strings.Contains(internalBody, "codesign --force --deep") {
		t.Fatal("internal master packaging must not use recursive deep signing")
	}

	main := source[strings.Index(source, "main() {"):]
	unseededAt := strings.Index(main, "build_public_unseeded_zip")
	privateAt := strings.Index(main, "build_public_private_dmg")
	internalAt := strings.Index(main, "\n  build_and_install_internal\n")
	masterCopyAt := strings.Index(main, `ditto "$INSTALL_PATH" "$MASTER_APP_PATH"`)
	if unseededAt < 0 || privateAt < 0 || internalAt <= unseededAt || internalAt <= privateAt || masterCopyAt <= internalAt {
		t.Fatal("public runtime must be built before the internal master is packaged and snapshotted")
	}
}

func TestDualLocalBuildKeepsSecretsPrivateAndApplicationArtifactsPortable(t *testing.T) {
	source := readDualLocalScript(t)
	for _, required := range []string{
		"normalize_app_bundle_permissions()",
		`normalize_app_bundle_permissions "$built_app"`,
		`normalize_app_bundle_permissions "$PUBLIC_APP_PATH"`,
		"is_macho_file",
		"for command in awk codesign ditto file jq lipo mktemp rmdir shasum stat",
	} {
		if !strings.Contains(source, required) {
			t.Errorf("portable bundle permission contract is missing %q", required)
		}
	}

	mainAt := strings.Index(source, "main() {")
	if mainAt < 0 {
		t.Fatal("main function is missing")
	}
	mainSource := source[mainAt:]
	privateUmaskAt := strings.Index(mainSource, "umask 077")
	workDirAt := strings.Index(mainSource, `WORK_DIR="$(mktemp -d`)
	logModeAt := strings.Index(mainSource, `chmod 600 "$SENSITIVE_LOG"`)
	portableUmaskAt := strings.Index(mainSource, "umask 022")
	internalBuildAt := strings.Index(mainSource, "\n  build_and_install_internal\n")
	if privateUmaskAt < 0 || workDirAt <= privateUmaskAt || logModeAt <= workDirAt ||
		portableUmaskAt <= logModeAt || internalBuildAt <= portableUmaskAt {
		t.Fatal("dual build must create its private workspace first, then restore portable application modes before building")
	}

	embedAt := strings.Index(source, `bash "$SCRIPT_DIR/embed-public-bootstrap-bundle.sh"`)
	verifyAt := strings.Index(source[embedAt:], `verify_public_bootstrap_bundle "$STAGED_BUNDLE_PATH" "$PUBLIC_BOOTSTRAP_BUNDLE_SHA256"`)
	signAt := strings.Index(source, `bash "$SCRIPT_DIR/sign-adhoc.sh"`)
	if embedAt < 0 || verifyAt < 0 || signAt <= embedAt+verifyAt {
		t.Fatal("public bootstrap bundle must be embedded and verified before ad-hoc signing")
	}
	if strings.Count(source, `verify_public_bootstrap_bundle "$STAGED_BUNDLE_PATH" "$PUBLIC_BOOTSTRAP_BUNDLE_SHA256"`) < 2 {
		t.Fatal("dual build must verify the embedded bootstrap both before signing and after packaging")
	}
}

func TestDualLocalBuildSeparatesUnseededUpdateFromSeededPrivateInstaller(t *testing.T) {
	dual := readDualLocalScript(t)
	packagePrivate := readFile(t, scriptPath("package-private.sh"))

	for _, required := range []string{
		`PRIVATE_APP_PATH="$PUBLIC_RELEASE_ROOT/private/$APP_NAME.app"`,
		`pristine_digest="$(app_tree_digest "$PUBLIC_APP_PATH")"`,
		`ditto "$PUBLIC_APP_PATH" "$PRIVATE_APP_PATH"`,
		`private_digest="$(app_tree_digest "$PRIVATE_APP_PATH")"`,
		`[[ -n "$pristine_digest" ]] && [[ "$pristine_digest" == "$private_digest" ]]`,
		`APP_PATH="$PRIVATE_APP_PATH" PUBLIC_BOOTSTRAP_BUNDLE_PATH="$PUBLIC_BOOTSTRAP_BUNDLE_PATH"`,
		`APP_PATH="$PRIVATE_APP_PATH" run_sensitive "private ad-hoc signing"`,
		`UPDATE_APP_PATH="$PUBLIC_APP_PATH" DMG_APP_PATH="$PRIVATE_APP_PATH"`,
	} {
		if !strings.Contains(dual, required) {
			t.Errorf("dual build split contract is missing %q", required)
		}
	}

	privateCopyAt := strings.Index(dual, `ditto "$PUBLIC_APP_PATH" "$PRIVATE_APP_PATH"`)
	privateEmbedAt := strings.Index(dual, `APP_PATH="$PRIVATE_APP_PATH" PUBLIC_BOOTSTRAP_BUNDLE_PATH="$PUBLIC_BOOTSTRAP_BUNDLE_PATH"`)
	publicSignAt := strings.Index(dual, `APP_PATH="$PUBLIC_APP_PATH" run_sensitive "public ad-hoc signing"`)
	privateSignAt := strings.Index(dual, `APP_PATH="$PRIVATE_APP_PATH" run_sensitive "private ad-hoc signing"`)
	packageAt := strings.Index(dual, `UPDATE_APP_PATH="$PUBLIC_APP_PATH" DMG_APP_PATH="$PRIVATE_APP_PATH"`)
	if privateCopyAt < 0 || privateEmbedAt <= privateCopyAt || publicSignAt <= privateEmbedAt ||
		privateSignAt <= publicSignAt || packageAt <= privateSignAt {
		t.Fatal("dual build must clone before signing, seed only the private branch, sign each branch, then package them separately")
	}

	for _, required := range []string{
		`UPDATE_APP_PATH="${UPDATE_APP_PATH:-}"`,
		`DMG_APP_PATH="${DMG_APP_PATH:-}"`,
		`[[ "$UPDATE_APP_PATH" != "$DMG_APP_PATH" ]]`,
		`ditto -c -k --sequesterRsrc --keepParent "$UPDATE_APP_PATH" "$UPDATE_ZIP"`,
		`ditto "$DMG_APP_PATH" "$DMG_STAGE/$APP_NAME.app"`,
	} {
		if !strings.Contains(packagePrivate, required) {
			t.Errorf("private package split contract is missing %q", required)
		}
	}
}

func TestDualLocalBuildUsesCanonicalPreseedDigest(t *testing.T) {
	dual := readDualLocalScript(t)
	for _, required := range []string{
		`find . -print | LC_ALL=C sort`,
		`stat -f '%Lp' "$path"`,
		`printf 'F\t%s\t%s\t%s\n' "$path" "$mode" "$entry_digest"`,
		`printf 'L\t%s\t%s\n' "$path" "$(readlink "$path")"`,
		`printf 'D\t%s\t%s\n' "$path" "$mode"`,
		`if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then`,
	} {
		if !strings.Contains(dual, required) {
			t.Errorf("canonical pre-seed digest contract is missing %q", required)
		}
	}
	if strings.Contains(dual, `COPYFILE_DISABLE=1 tar -cf - .`) {
		t.Fatal("pre-seed digest must not depend on traversal order or mutable archive metadata")
	}
}

func TestDualLocalBuildPersistsParitySnapshotsAndPromotesOnlyAfterVerification(t *testing.T) {
	source := readDualLocalScript(t)

	for _, required := range []string{
		`SOURCE_SHA=""`,
		`PARITY_CACHE_ROOT="${PARITY_CACHE_ROOT:-${MASTER_PUBLIC_PARITY_ROOT:-$HOME/.cache/telegram-companion-release/master-public-parity}}"`,
		`PARITY_STAGING_ROOT`,
		`PARITY_VERSION_ROOT`,
		`PARITY_EVIDENCE_PATH`,
		`MASTER_APP_PATH`,
		`ditto "$INSTALL_PATH" "$MASTER_APP_PATH"`,
		`ditto "$PUBLIC_APP_PATH" "$PUBLIC_PARITY_APP_PATH"`,
		`git -C "$REPO_ROOT" rev-parse HEAD`,
		`bash "$SCRIPT_DIR/master-public-parity.sh" write`,
		`bash "$SCRIPT_DIR/master-public-parity.sh" verify`,
		`mv "$PARITY_STAGING_ROOT" "$PARITY_FINAL_ROOT"`,
		`mv -fh "$PARITY_POINTER_TMP" "$PARITY_CURRENT_POINTER"`,
	} {
		requireContains(t, "build-dual-local.sh", source, required)
	}

	main := source[strings.Index(source, "main() {"):]
	publicBuildAt := strings.Index(main, "build_public_private_dmg")
	masterCopyAt := strings.Index(main, `ditto "$INSTALL_PATH" "$MASTER_APP_PATH"`)
	publicCopyAt := strings.Index(main, `ditto "$PUBLIC_APP_PATH" "$PUBLIC_PARITY_APP_PATH"`)
	writeAt := strings.Index(main, `bash "$SCRIPT_DIR/master-public-parity.sh" write`)
	verifyAt := strings.Index(main, `bash "$SCRIPT_DIR/master-public-parity.sh" verify`)
	promoteAt := strings.Index(main, "promote_parity_snapshot")
	deliverAt := strings.Index(main, "deliver_artifacts")
	if publicBuildAt < 0 || masterCopyAt <= publicBuildAt || publicCopyAt <= masterCopyAt ||
		writeAt <= publicCopyAt || verifyAt <= writeAt || promoteAt <= verifyAt || deliverAt <= promoteAt {
		t.Fatal("dual build must build public, snapshot master and public, verify parity, promote the private snapshot, then deliver artifacts")
	}

	if strings.Contains(source, `PARITY_EVIDENCE_PATH="$PUBLIC_RELEASE_ROOT`) ||
		strings.Contains(source, `PARITY_EVIDENCE_PATH="$WORK_DIR`) {
		t.Fatal("parity evidence must survive WORK_DIR cleanup in the private cache")
	}
	if strings.Contains(source, `PRIVATE_APP_PATH="/Applications/Telegram Companion.app"`) {
		t.Fatal("parity helper must use canonical MASTER_APP_PATH rather than PRIVATE_APP_PATH")
	}
}

func TestDualLocalBuildRollsBackOnlyItsParitySnapshotOnFailure(t *testing.T) {
	source := readDualLocalScript(t)
	for _, required := range []string{
		`PARITY_PREVIOUS_POINTER_TARGET`,
		`PARITY_PREVIOUS_POINTER_PRESENT`,
		`previous_pointer_suffix="${PARITY_PREVIOUS_POINTER_TARGET#"$PARITY_VERSION_ROOT/"}`,
		`[[ "$previous_pointer_suffix" =~ ^[0-9a-f]{40}$ ]]`,
		`previous_pointer_canonical="$(cd "$PARITY_PREVIOUS_POINTER_TARGET" && pwd -P)"`,
		`[[ "$previous_pointer_canonical" == "$PARITY_VERSION_ROOT/$previous_pointer_suffix" ]]`,
		`PARITY_LOCK_DIR="$PARITY_CACHE_ROOT/.build.lock"`,
		`PARITY_LOCK_ACQUIRED=1`,
		`PARITY_LOCK_ID="$(parity_identity "$PARITY_LOCK_DIR")"`,
		`[[ "$(parity_identity "$PARITY_LOCK_DIR")" == "$PARITY_LOCK_ID" ]]`,
		`PARITY_STAGING_ID="$(parity_identity "$PARITY_STAGING_ROOT")"`,
		`PARITY_FINAL_ID="$PARITY_STAGING_ID"`,
		`PARITY_POINTER_ID="$(parity_identity "$PARITY_POINTER_TMP")"`,
		`PARITY_CURRENT_POINTER_ID="$(parity_identity "$PARITY_CURRENT_POINTER")"`,
		`[[ "$(readlink "$PARITY_CURRENT_POINTER")" == "$PARITY_FINAL_ROOT" ]]`,
		`PARITY_PROMOTED=1`,
		`ln -s "$PARITY_PREVIOUS_POINTER_TARGET"`,
		`rm -rf -- "$PARITY_FINAL_ROOT"`,
	} {
		requireContains(t, "build-dual-local.sh", source, required)
	}
	for _, forbidden := range []string{
		`rm -rf -- "$PARITY_CACHE_ROOT"`,
		`rm -rf -- "$PARITY_CACHE_ROOT/versions"`,
		`rm -rf -- "$PARITY_CACHE_ROOT/current"`,
	} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("parity cleanup must not remove shared root %q", forbidden)
		}
	}
}

func TestDualLocalBuildRollsBackOnlyItsInstalledMaster(t *testing.T) {
	source := readDualLocalScript(t)
	for _, required := range []string{
		`INSTALL_BACKUP_ID=""`,
		`INSTALL_INSTALLED_ID=""`,
		`INSTALL_BACKUP_ID="$(parity_identity "$INSTALL_PATH")"`,
		`INSTALL_STAGE_ID="$(parity_identity "$INSTALL_STAGE_PATH")"`,
		`INSTALL_INSTALLED_ID="$INSTALL_STAGE_ID"`,
		`[[ "$(parity_identity "$INSTALL_PATH")" == "$INSTALL_INSTALLED_ID" ]]`,
		`[[ "$(parity_identity "$INSTALL_BACKUP_PATH")" == "$INSTALL_BACKUP_ID" ]]`,
		`[[ -d "$INSTALL_PATH" && ! -L "$INSTALL_PATH" ]]`,
		`[[ -d "$INSTALL_BACKUP_PATH" && ! -L "$INSTALL_BACKUP_PATH" ]]`,
		`[[ -n "$INSTALL_INSTALLED_ID" && "$backup_present" == "0" ]]`,
		`if [[ "$backup_present" == "1" ]]; then`,
	} {
		requireContains(t, "build-dual-local.sh", source, required)
	}
}

func TestDualLocalBuildDoesNotFollowParityRootSymlinks(t *testing.T) {
	source := readDualLocalScript(t)
	for _, required := range []string{
		`require_private_parity_path()`,
		`require_private_parity_path "$PARITY_CACHE_ROOT"`,
		`require_private_parity_path "$PARITY_CACHE_ROOT/versions"`,
		`require_private_parity_path "$PARITY_CACHE_ROOT/current"`,
		`mkdir "$path"`,
	} {
		requireContains(t, "build-dual-local.sh", source, required)
	}
	if strings.Contains(source, `mkdir -p "$PARITY_CACHE_ROOT/versions" "$PARITY_CACHE_ROOT/current"`) {
		t.Fatal("parity root creation must not follow a pre-existing symlink")
	}
}

func TestDualLocalBuildUsesTheSharedApplicationWithoutRemovedTransferMetadata(t *testing.T) {
	source := readDualLocalScript(t)

	required := `"$WAILS_BIN" build -clean -skipbindings -platform "darwin/arm64" -tags desktop`
	if !strings.Contains(source, required) {
		t.Errorf("internal build contract is missing %q", required)
	}
	for _, forbidden := range []string{"VITE_" + "EDITION", "RELAY_" + "BASE_URL", "MASTER_" + "INBOX_ID", "MASTER_" + "PUBLIC_KEY"} {
		if strings.Contains(source, forbidden) {
			t.Errorf("removed transfer metadata remains in dual build: %q", forbidden)
		}
	}
}

func TestDualLocalManualUnseededModeContract(t *testing.T) {
	source := readDualLocalScript(t)
	if !strings.Contains(source, `MANUAL_UNSEEDED_ONLY="${MANUAL_UNSEEDED_ONLY:-0}"`) {
		t.Fatal("manual mode must default to disabled")
	}
	if !strings.Contains(source, `[[ "$MANUAL_UNSEEDED_ONLY" == "0" || "$MANUAL_UNSEEDED_ONLY" == "1" ]]`) {
		t.Fatal("manual mode flag must be validated")
	}

	mode0BootstrapBlock := `if [[ "$MANUAL_UNSEEDED_ONLY" == "0" ]]; then
    require_env "PUBLIC_BOOTSTRAP_BUNDLE_PATH"
    require_env "PUBLIC_BOOTSTRAP_BUNDLE_SHA256"
  fi`
	if strings.Count(source, mode0BootstrapBlock) != 1 {
		t.Fatal("bootstrap requirements must be contained in the exact mode-0 conditional")
	}
	for _, required := range []string{
		`require_env "PUBLIC_BOOTSTRAP_BUNDLE_PATH"`,
		`require_env "PUBLIC_BOOTSTRAP_BUNDLE_SHA256"`,
	} {
		if strings.Count(source, required) != 1 {
			t.Fatalf("bootstrap requirement %q must occur exactly once", required)
		}
	}

	mainAt := strings.Index(source, "main() {")
	if mainAt < 0 {
		t.Fatal("main function is missing")
	}
	main := source[mainAt:]
	manualDispatch := `if [[ "$MANUAL_UNSEEDED_ONLY" == "1" ]]; then
    build_public_unseeded_zip
  else
  build_public_private_dmg
  fi`
	dispatchMarker := `if [[ "$MANUAL_UNSEEDED_ONLY" == "1" ]]; then`
	if strings.Count(main, dispatchMarker) != 1 {
		t.Fatal("manual mode must have exactly one mode-1 dispatch")
	}
	dispatchAt := strings.Index(main, dispatchMarker)
	if dispatchAt < 0 || !strings.HasPrefix(main[dispatchAt:], manualDispatch) {
		t.Fatal("manual mode dispatch must exactly select unseeded mode 1 and private mode 0")
	}
	dispatchSource := main[dispatchAt : dispatchAt+len(manualDispatch)]
	manualBody := dispatchSource[:strings.Index(dispatchSource, "\n  else")]
	mainOutsideAllowedMode0 := strings.Replace(main, mode0BootstrapBlock, "", 1)
	mainOutsideAllowedMode0 = strings.Replace(mainOutsideAllowedMode0, manualDispatch, "", 1)

	unseededBody := shellFunctionBody(source, "build_public_unseeded_zip")
	if unseededBody == "" {
		t.Fatal("build_public_unseeded_zip function is missing")
	}
	assertShellMarkersInOrder(t, unseededBody,
		`run_sensitive "public preflight" bash "$SCRIPT_DIR/preflight-public.sh"`,
		`run_sensitive "public app build" bash "$SCRIPT_DIR/build-stage.sh"`,
		`verify_unseeded_public_bundle "$PUBLIC_APP_PATH"`,
		`APP_PATH="$PUBLIC_APP_PATH" run_sensitive "public ad-hoc signing" bash "$SCRIPT_DIR/sign-adhoc.sh"`,
		`codesign --verify --strict "$PUBLIC_APP_PATH"`,
		`verify_unseeded_public_bundle "$PUBLIC_APP_PATH"`,
		`ditto -c -k --sequesterRsrc --keepParent "$PUBLIC_APP_PATH" "$UPDATE_ZIP"`,
		`checksum_path="$UPDATE_ZIP.sha256"`,
		`shasum -a 256 "$(basename "$UPDATE_ZIP")" > "$(basename "$checksum_path")"`,
		`verify_unseeded_update_archive "$UPDATE_ZIP"`,
		`sha256_file "$UPDATE_ZIP"`,
	)

	for _, forbidden := range []string{
		"PUBLIC_BOOTSTRAP_BUNDLE_PATH",
		"PUBLIC_BOOTSTRAP_BUNDLE_SHA256",
		"verify_public_bootstrap_bundle",
		"PRIVATE_APP_PATH",
		"DMG_PATH",
		"build_public_private_dmg",
		"package-private.sh",
		"embed-public-bootstrap-bundle.sh",
		"verify-private.sh",
		"publish.sh",
		"appcast.sh",
		"notarize.sh",
		"notarytool",
		"stapler",
		"hdiutil",
		"SPARKLE_GENERATE_APPCAST",
		"generate_appcast",
	} {
		if strings.Contains(manualBody, forbidden) ||
			strings.Contains(unseededBody, forbidden) ||
			strings.Contains(mainOutsideAllowedMode0, forbidden) {
			t.Fatalf("manual unseeded path contains forbidden operation %q", forbidden)
		}
	}
}
func shellFunctionBody(source, name string) string {
	start := strings.Index(source, name+"() {")
	if start < 0 {
		return ""
	}
	close := strings.Index(source[start:], "\n}\n")
	if close < 0 {
		return ""
	}
	return source[start : start+close]
}

func assertShellMarkersInOrder(t *testing.T, source string, markers ...string) {
	t.Helper()
	previous := -1
	for _, marker := range markers {
		at := strings.Index(source[previous+1:], marker)
		if at >= 0 {
			at += previous + 1
		}
		if at < 0 {
			t.Fatalf("shell block is missing %q", marker)
		}
		if at <= previous {
			t.Fatalf("shell block marker %q is out of order", marker)
		}
		previous = at
	}
}

func readDualLocalScript(t *testing.T) string {
	t.Helper()
	path := filepath.Join(filepath.Dir(scriptPath("lib.sh")), "build-dual-local.sh")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(contents)
}
