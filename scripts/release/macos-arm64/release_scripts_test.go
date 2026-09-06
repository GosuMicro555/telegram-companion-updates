package macosarm64_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

const (
	wantTargetOS              = "darwin"
	wantTargetArch            = "arm64"
	wantMinimumOS             = "13.0"
	wantSparkle               = "2.9.2"
	wantTor                   = "15.0.20"
	wantSnowflake             = "2.14.1"
	wantLyrebird              = "15.0.20"
	wantTorExpertBundleURL    = "https://archive.torproject.org/tor-package-archive/torbrowser/15.0.20/tor-expert-bundle-macos-aarch64-15.0.20.tar.gz"
	wantTorExpertBundleSHA256 = "73fdccde8136678e41a625160993e6a9dc4f4ff8cd376318b5e41e5627d55682"
	wantPtConfigSHA256        = "2f7cf039710e96b70ea7d45473b905f9a6ce9a8b65e9f03e2507135ae8c75407"
)

var verifiedPayloads = map[string]struct {
	version string
	sha256  string
}{
	"sparkle": {
		version: wantSparkle,
		sha256:  "1cb340cbbef04c6c0d162078610c25e2221031d794a3449d89f2f56f4df77c95",
	},
	"tor": {
		version: wantTor,
		sha256:  wantTorExpertBundleSHA256,
	},
	"snowflake": {
		version: wantSnowflake,
		sha256:  "7d72d19fb2367810e06df632520de119aa5e4cc6834f28bc86e23fdf8ee9ae69",
	},
	"lyrebird": {
		version: wantLyrebird,
		sha256:  wantTorExpertBundleSHA256,
	},
}

var releaseScripts = []string{
	"lib.sh",
	"preflight.sh",
	"preflight-public-release.sh",
	"preflight-public-publish.sh",
	"preflight-private.sh",
	"preflight-private-publish.sh",
	"build-stage.sh",
	"sign.sh",
	"sign-adhoc.sh",
	"notarize.sh",
	"package-private.sh",
	"appcast.sh",
	"publish.sh",
	"verify.sh",
	"verify-private.sh",
}

func TestLiveProxySmokeContract(t *testing.T) {
	source := readFile(t, scriptPath("live-proxy-smoke.sh"))
	for _, required := range []string{
		"set -euo pipefail",
		`source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"`,
		`[[ "$APP_PATH" = /* ]]`,
		`[[ -d "$APP_PATH" && ! -L "$APP_PATH" ]]`,
		`Contents/Resources/tor/tor`,
		`Contents/Resources/tor/pluggable_transports/lyrebird`,
		`Contents/Resources/tor/pluggable_transports/pt_config.json`,
		`TELEGRAM_COMPANION_LIVE_TOR=1`,
		`TELEGRAM_COMPANION_LIVE_TOR_RESOURCES="$resources"`,
		`TestLiveManagedTorSnowflakeBootstrapAndShutdown`,
		`-count=1`,
	} {
		requireContains(t, "live-proxy-smoke.sh", source, required)
	}
	for _, forbidden := range []string{"cat ", "tor-output.log", "bootstrap.log", "Bridge "} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("live proxy smoke may expose sensitive runtime material: %q", forbidden)
		}
	}
}

type payloadManifest struct {
	SchemaVersion int `json:"schema_version"`
	Target        struct {
		OS        string `json:"os"`
		Arch      string `json:"arch"`
		MinimumOS string `json:"minimum_os"`
	} `json:"target"`
	Payloads []payload `json:"payloads"`
}

type payload struct {
	Name           string `json:"name"`
	Version        string `json:"version"`
	URL            string `json:"url"`
	SHA256         string `json:"sha256"`
	ArchiveType    string `json:"archive_type"`
	Source         string `json:"source"`
	Destination    string `json:"destination"`
	License        string `json:"license"`
	LicenseFile    string `json:"license_file"`
	NoticeFile     string `json:"notice_file"`
	PtConfigSHA256 string `json:"pt_config_sha256"`
}

func TestStaticValidatorRejectsUnpinnedSHA256(t *testing.T) {
	bad := payload{Name: "sparkle", Version: "2.7.4", URL: "https://example.test/Sparkle-2.7.4.tar.xz", SHA256: "latest"}
	if err := validatePinnedPayload(bad); err == nil {
		t.Fatal("unpinned SHA256 was accepted")
	}
}

func TestPayloadManifestPinsReleaseInputs(t *testing.T) {
	manifest := readManifest(t)
	if manifest.SchemaVersion != 1 {
		t.Fatalf("schema_version = %d, want 1", manifest.SchemaVersion)
	}
	if manifest.Target.OS != wantTargetOS || manifest.Target.Arch != wantTargetArch {
		t.Fatalf("target = %s/%s, want %s/%s", manifest.Target.OS, manifest.Target.Arch, wantTargetOS, wantTargetArch)
	}
	if manifest.Target.MinimumOS != wantMinimumOS {
		t.Fatalf("minimum_os = %q, want %q", manifest.Target.MinimumOS, wantMinimumOS)
	}

	wantNames := map[string]bool{"sparkle": false, "tor": false, "snowflake": false, "lyrebird": false}
	for _, item := range manifest.Payloads {
		if err := validatePinnedPayload(item); err != nil {
			t.Errorf("payload %q: %v", item.Name, err)
		}
		if _, ok := wantNames[item.Name]; ok {
			wantNames[item.Name] = true
		}
	}
	for name, found := range wantNames {
		if !found {
			t.Errorf("required payload %q is missing", name)
		}
	}
}

func TestLyrebirdPayloadUsesTorExpertBundleDestination(t *testing.T) {
	manifest := readManifest(t)
	for _, item := range manifest.Payloads {
		if item.Name != "lyrebird" {
			continue
		}
		if item.Version != wantLyrebird || item.SHA256 != verifiedPayloads["lyrebird"].sha256 {
			t.Fatalf("lyrebird pin = version %q sha256 %q, want %q/%q", item.Version, item.SHA256, wantLyrebird, verifiedPayloads["lyrebird"].sha256)
		}
		if item.PtConfigSHA256 != wantPtConfigSHA256 {
			t.Fatalf("lyrebird pt_config_sha256 = %q, want approved digest", item.PtConfigSHA256)
		}
		if item.Destination != "Contents/Resources/tor/pluggable_transports" {
			t.Fatalf("lyrebird destination = %q", item.Destination)
		}
		return
	}
	t.Fatal("lyrebird payload is missing")
}

func TestLyrebirdManifestPinsLicenseAndNoticeFiles(t *testing.T) {
	manifest := readManifest(t)
	for _, item := range manifest.Payloads {
		if item.Name != "lyrebird" {
			continue
		}
		if item.LicenseFile != "docs/lyrebird.txt" {
			t.Fatalf("lyrebird license_file = %q, want docs/lyrebird.txt", item.LicenseFile)
		}
		if item.NoticeFile != "docs/lyrebird.txt" {
			t.Fatalf("lyrebird notice_file = %q, want docs/lyrebird.txt", item.NoticeFile)
		}
		return
	}
	t.Fatal("lyrebird payload is missing")
}

func TestLyrebirdNoticeIsStagedAndReleaseVerifierChecksIt(t *testing.T) {
	scripts := readScripts(t)
	build := scripts["build-stage.sh"]
	lib := scripts["lib.sh"]
	verify := scripts["verify.sh"]
	for _, required := range []string{
		`payload_field lyrebird license_file`,
		`payload_field lyrebird notice_file`,
		`cp "$license_file"`,
		`verify_lyrebird_payload "$APP_PATH"`,
	} {
		if !strings.Contains(build, required) && !strings.Contains(lib, required) && !strings.Contains(verify, required) {
			t.Errorf("release scripts do not contain %q", required)
		}
	}
	if !strings.Contains(lib, "lyrebird notice/license file is missing or unsafe") {
		t.Fatal("Lyrebird verifier must reject missing or unsafe notice/license files")
	}
}

func TestPublicVerificationRejectsSeededStateInAppAndUpdateArchive(t *testing.T) {
	scripts := readScripts(t)
	lib := scripts["lib.sh"]
	verify := scripts["verify.sh"]
	embed := readFile(t, scriptPath("embed-public-bootstrap-bundle.sh"))
	for _, required := range []string{
		"verify_unseeded_public_bundle",
		"verify_unseeded_update_archive",
		"state.tcs",
		"bootstrap-state",
		"TCSEED2",
	} {
		if !strings.Contains(lib, required) && !strings.Contains(verify, required) && !strings.Contains(embed, required) {
			t.Errorf("public release verification does not contain %q", required)
		}
	}
	if !strings.Contains(embed, `verify_unseeded_public_bundle "$APP_PATH"`) {
		t.Fatal("bootstrap embedding must only run after the clean build-stage app passes the unseeded-state gate")
	}
	if !strings.Contains(verify, `verify_unseeded_update_archive "$UPDATE_ZIP"`) {
		t.Fatal("public release verification must scan the clean update archive for seeded state")
	}
}

func TestLyrebirdIsStagedAndVerifiedAsArm64Executable(t *testing.T) {
	scripts := readScripts(t)
	for name, source := range scripts {
		if name == "build-stage.sh" || name == "verify.sh" || name == "lib.sh" {
			if !strings.Contains(source, "lyrebird") {
				t.Errorf("%s does not mention lyrebird staging/verification", name)
			}
		}
	}
	for _, required := range []string{
		`stage_lyrebird_payload`,
		`Contents/Resources/tor/pluggable_transports/lyrebird`,
		`[[ -x "$lyrebird_binary" ]]`,
		`[[ ! -L "$lyrebird_binary" ]]`,
		`file -b "$lyrebird_binary"`,
		`verify_lyrebird_payload "$APP_PATH"`,
	} {
		found := false
		for _, source := range scripts {
			if strings.Contains(source, required) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("release scripts do not contain %q", required)
		}
	}
}

func TestPayloadManifestUsesVerifiedReleaseDigests(t *testing.T) {
	manifest := readManifest(t)
	for _, item := range manifest.Payloads {
		want, ok := verifiedPayloads[item.Name]
		if !ok {
			t.Errorf("unexpected payload %q", item.Name)
			continue
		}
		if item.Version != want.version || item.SHA256 != want.sha256 {
			t.Errorf("payload %q = version %q sha256 %q, want version %q sha256 %q", item.Name, item.Version, item.SHA256, want.version, want.sha256)
		}
	}
}

func TestTorAndLyrebirdUseArchivedTorExpertBundlePin(t *testing.T) {
	manifest := readManifest(t)
	found := map[string]bool{"tor": false, "lyrebird": false}
	for _, item := range manifest.Payloads {
		if _, ok := found[item.Name]; !ok {
			continue
		}
		if item.URL != wantTorExpertBundleURL {
			t.Errorf("%s URL = %q, want the archived Tor expert bundle URL", item.Name, item.URL)
		}
		if item.SHA256 != wantTorExpertBundleSHA256 {
			t.Errorf("%s SHA-256 = %q, want the verified Tor expert bundle digest", item.Name, item.SHA256)
		}
		found[item.Name] = true
	}
	for name, ok := range found {
		if !ok {
			t.Errorf("%s payload is missing", name)
		}
	}
}

func TestManifestValidatorKeepsPayloadObjectContext(t *testing.T) {
	source := readFile(t, scriptPath("lib.sh"))
	if strings.Contains(source, `(.url | ascii_downcase | contains((.version | ascii_downcase`) {
		t.Fatal("manifest validator loses the payload object after piping .url")
	}
	requireContains(t, "lib.sh", source, `((.url | ascii_downcase) as $url`)
	requireContains(t, "lib.sh", source, `(.version | ascii_downcase | ltrimstr("v")) as $version`)
	requireContains(t, "lib.sh", source, `($url | contains($version))) and`)
}

func TestStaticValidatorRejectsCodesignDeep(t *testing.T) {
	if err := rejectCodesignDeep("codesign --force --deep --sign identity App.app\n"); err == nil {
		t.Fatal("codesign --deep was accepted")
	}
}

func TestReleaseScriptsNeverUseCodesignDeep(t *testing.T) {
	for name, source := range readScripts(t) {
		if err := rejectCodesignDeep(source); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestStaticValidatorRejectsNonARM64(t *testing.T) {
	bad := payloadManifest{}
	bad.Target.OS = wantTargetOS
	bad.Target.Arch = "amd64"
	bad.Target.MinimumOS = wantMinimumOS
	if err := validateTarget(bad); err == nil {
		t.Fatal("non-ARM64 target was accepted")
	}
}

func TestReleaseContractIsARM64Only(t *testing.T) {
	manifest := readManifest(t)
	if err := validateTarget(manifest); err != nil {
		t.Fatal(err)
	}

	scripts := readScripts(t)
	requireContains(t, "lib.sh", scripts["lib.sh"], `readonly TARGET_OS="darwin"`)
	requireContains(t, "lib.sh", scripts["lib.sh"], `readonly TARGET_ARCH="arm64"`)
	requireContains(t, "lib.sh", scripts["lib.sh"], "require_arm64_macho()")
	requireContains(t, "build-stage.sh", scripts["build-stage.sh"], `-platform "$TARGET_OS/$TARGET_ARCH"`)
	requireContains(t, "build-stage.sh", scripts["build-stage.sh"], `require_arm64_bundle "$APP_PATH"`)
	requireContains(t, "verify.sh", scripts["verify.sh"], `require_arm64_bundle "$APP_PATH"`)

	for name, source := range scripts {
		if regexp.MustCompile(`(?i)\b(?:amd64|x86_64|universal2?)\b`).MatchString(source) {
			t.Errorf("%s contains a non-ARM64 release target", name)
		}
	}
}

func TestStaticValidatorRejectsAppcastBeforePayload(t *testing.T) {
	bad := "publish_appcast_last\npublish_payload_assets\nverify_published_payloads\n"
	if err := validatePublishOrder(bad); err == nil {
		t.Fatal("appcast-before-payload publication was accepted")
	}
}

func TestPublishMakesAppcastLast(t *testing.T) {
	source := readFile(t, scriptPath("publish.sh"))
	if err := validatePublishOrder(source); err != nil {
		t.Fatal(err)
	}
}

func TestStaticValidatorRejectsMissingMinimumSystemVersion(t *testing.T) {
	bad := []byte(`<?xml version="1.0"?><plist><dict><key>CFBundleName</key><string>Telegram Companion</string></dict></plist>`)
	if err := validateMinimumSystemVersion(bad); err == nil {
		t.Fatal("plist without LSMinimumSystemVersion was accepted")
	}
}

func TestInfoPlistRequiresMacOS13(t *testing.T) {
	plist := readFileBytes(t, repoPath("build", "darwin", "Info.plist"))
	if err := validateMinimumSystemVersion(plist); err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`(?s)<key>LSArchitecturePriority</key>\s*<array>\s*<string>arm64</string>\s*</array>`).Match(plist) {
		t.Fatal("Info.plist must prioritize only arm64")
	}
}

func TestScriptsFailClosedAndTakeSecretsFromEnvironment(t *testing.T) {
	scripts := readScripts(t)
	for name, source := range scripts {
		requireContains(t, name, source, "set -euo pipefail")
	}

	preflight := scripts["preflight.sh"]
	for _, name := range []string{
		"RELEASE_VERSION",
		"APPCAST_URL",
		"BUILD_TAGS",
		"LICENSE_PUBLIC_KEY",
		"SPARKLE_PUBLIC_ED_KEY",
	} {
		requireContains(t, "preflight.sh", preflight, `require_env "`+name+`"`)
	}

	notarize := scripts["notarize.sh"]
	requireContains(t, "notarize.sh", notarize, `--keychain-profile "$APPLE_NOTARY_KEYCHAIN_PROFILE"`)
	if regexp.MustCompile(`--(?:apple-id|password|team-id)(?:[=[:space:]])`).MatchString(notarize) {
		t.Fatal("notarize.sh passes notarization secrets on the command line")
	}
}

func TestRevocationBuildMetadataIsPinnedAndVerified(t *testing.T) {
	const manifestURL = "https://raw.githubusercontent.com/GosuMicro555/telegram-companion-updates/main/revocations.tcrev"
	scripts := readScripts(t)
	preflight := scripts["preflight.sh"]
	for _, name := range []string{"REVOCATION_MANIFEST_URL", "REVOCATION_KEY_ID", "REVOCATION_PUBLIC_KEY"} {
		requireContains(t, "preflight.sh", preflight, `require_env "`+name+`"`)
	}
	lib := scripts["lib.sh"]
	for _, required := range []string{
		manifestURL,
		"validate_revocation_build_metadata()",
		`require_env "REVOCATION_MANIFEST_URL"`,
		`require_env "REVOCATION_KEY_ID"`,
		`require_env "REVOCATION_PUBLIC_KEY"`,
		`[[ "$REVOCATION_KEY_ID" == "revocation-2026-01" ]]`,
		`require_raw_ed25519_public_key "REVOCATION_PUBLIC_KEY"`,
		`REVOCATION_BUILD_METADATA="TCREVBUILD1|$REVOCATION_MANIFEST_URL|$REVOCATION_KEY_ID|$REVOCATION_PUBLIC_KEY"`,
	} {
		requireContains(t, "lib.sh", lib, required)
	}
	build := scripts["build-stage.sh"]
	for _, variable := range []string{"revocationManifestURL=$REVOCATION_MANIFEST_URL", "revocationKeyID=$REVOCATION_KEY_ID", "revocationPublicKey=$REVOCATION_PUBLIC_KEY", "revocationBuildMetadata=$REVOCATION_BUILD_METADATA"} {
		requireContains(t, "build-stage.sh", build, variable)
	}
	verify := scripts["verify.sh"]
	for _, variable := range []string{"validate_revocation_build_metadata", "REVOCATION_BUILD_METADATA"} {
		requireContains(t, "verify.sh", verify, variable)
	}
	verifyPrivate := scripts["verify-private.sh"]
	for _, variable := range []string{
		"validate_revocation_build_metadata",
		`grep -aFq "$REVOCATION_BUILD_METADATA" "$APP_PATH/Contents/MacOS/$APP_EXECUTABLE" ||`,
		`die "application is missing its linker-injected revocation metadata fingerprint"`,
	} {
		requireContains(t, "verify-private.sh", verifyPrivate, variable)
	}
	for _, path := range []string{"preflight-public-publish.sh", "publish.sh"} {
		requireContains(t, path, scripts[path], "validate_revocation_build_metadata")
	}
	makefile := readFile(t, repoPath("Makefile"))
	requireContains(t, "Makefile", makefile, "revocationBuildMetadata=$(REVOCATION_BUILD_METADATA)")
	workflow := readFile(t, repoPath(".github", "workflows", "ci.yml"))
	requireContains(t, "ci.yml", workflow, "revocationBuildMetadata=$REVOCATION_BUILD_METADATA")
}

func TestScriptsExpandShellVariables(t *testing.T) {
	for name, source := range readScripts(t) {
		if strings.Contains(source, `\${`) {
			t.Errorf("%s contains an escaped shell expansion", name)
		}
	}
}

func TestReleaseMetadataIsInjectedIntoTemplate(t *testing.T) {
	plist := readFile(t, repoPath("build", "darwin", "Info.plist"))
	for _, placeholder := range []string{"__RELEASE_VERSION__", "__APPCAST_URL__", "__SPARKLE_PUBLIC_ED_KEY__"} {
		requireContains(t, "Info.plist", plist, placeholder)
	}
	if strings.Contains(plist, "example.test") {
		t.Fatal("Info.plist contains a test appcast URL")
	}

	build := readFile(t, scriptPath("build-stage.sh"))
	for _, key := range []string{"CFBundleShortVersionString", "CFBundleVersion", "SUFeedURL", "SUPublicEDKey"} {
		requireContains(t, "build-stage.sh", build, `Set :`+key)
	}
	requireContains(t, "build-stage.sh", build, `-tags "$BUILD_TAGS"`)
}

func TestPublicBootstrapModeIsValidatedAndLinkedAtBuildTime(t *testing.T) {
	source := readFile(t, scriptPath("build-stage.sh"))
	for _, required := range []string{
		`PUBLIC_BOOTSTRAP_MODE="${PUBLIC_BOOTSTRAP_MODE:-seeded}"`,
		`validate_public_bootstrap_mode`,
		`case "$PUBLIC_BOOTSTRAP_MODE" in`,
		`seeded|empty`,
		`bootstrapMode=$PUBLIC_BOOTSTRAP_MODE`,
	} {
		requireContains(t, "build-stage.sh", source, required)
	}
}

func TestReleasePreflightRejectsSourceVersionDrift(t *testing.T) {
	scripts := readScripts(t)
	lib := scripts["lib.sh"]

	for _, required := range []string{
		"validate_source_version_alignment()",
		`frontend/src/version.ts`,
		`frontend/package.json`,
		`wails.json`,
		`APP_VERSION_LABEL`,
		`source version metadata does not match RELEASE_VERSION`,
	} {
		requireContains(t, "lib.sh", lib, required)
	}

	requireContains(t, "preflight.sh", scripts["preflight.sh"], "validate_source_version_alignment")
	requireContains(t, "preflight-private.sh", scripts["preflight-private.sh"], "validate_source_version_alignment")
}

func TestReleasePublicationExcludesDirectDeliveryDMG(t *testing.T) {
	publish := readFile(t, scriptPath("publish.sh"))
	if strings.Contains(publish, "$DMG_PATH") {
		t.Fatal("publish.sh must not upload or verify the directly delivered DMG")
	}
	requireContains(t, "publish.sh", publish, "ensure_release_exists")

	appcast := readFile(t, scriptPath("appcast.sh"))
	requireContains(t, "appcast.sh", appcast, "APPCAST_INPUT_DIR")
	requireContains(t, "appcast.sh", appcast, `cp "$UPDATE_ZIP" "$APPCAST_INPUT_DIR/"`)
	requireContains(t, "appcast.sh", appcast, `"$APPCAST_INPUT_DIR"`)
}

func TestBuildLinksPinnedSparkleBeforeNativeCompilation(t *testing.T) {
	source := readFile(t, scriptPath("build-stage.sh"))
	prepare := strings.Index(source, "prepare_sparkle_framework")
	build := strings.Index(source, `"$WAILS_BIN" build`)
	if prepare < 0 || build < 0 || prepare >= build {
		t.Fatal("pinned Sparkle framework must be prepared before Wails compiles the native bridge")
	}
	for _, required := range []string{
		"CGO_CFLAGS=\"-F$SPARKLE_FRAMEWORK_PARENT",
		"CGO_LDFLAGS=\"-F$SPARKLE_FRAMEWORK_PARENT",
		"-Wl,-rpath,@executable_path/../Frameworks",
	} {
		requireContains(t, "build-stage.sh", source, required)
	}
}

func TestPinnedSparklePayloadIsThinnedToARM64(t *testing.T) {
	source := readFile(t, scriptPath("build-stage.sh"))
	for _, required := range []string{
		`thin_macho_tree_to_arm64()`,
		`lipo "$executable" -thin "$TARGET_ARCH" -output "$thinned"`,
		`thin_macho_tree_to_arm64 "$SPARKLE_PAYLOAD_ROOT"`,
	} {
		requireContains(t, "build-stage.sh", source, required)
	}
}

func TestPublicBundlePermissionsArePortableBeforeSigning(t *testing.T) {
	source := readFile(t, scriptPath("build-stage.sh"))
	for _, required := range []string{
		`find "$APP_PATH" -type d -exec chmod 0755 {} +`,
		`find "$APP_PATH" -type f -perm +111 -exec chmod 0755 {} +`,
		`find "$APP_PATH" -type f ! -perm +111 -exec chmod 0644 {} +`,
		`normalize_bundle_permissions`,
	} {
		requireContains(t, "build-stage.sh", source, required)
	}

	normalizeAt := strings.LastIndex(source, "normalize_bundle_permissions")
	verifyAt := strings.LastIndex(source, `require_arm64_bundle "$APP_PATH"`)
	if normalizeAt < 0 || verifyAt <= normalizeAt {
		t.Fatal("public bundle permissions must be normalized before release verification and signing")
	}
}

func TestPayloadDownloadFailureCannotBeMaskedByArgumentSubstitution(t *testing.T) {
	source := readFile(t, scriptPath("build-stage.sh"))
	if strings.Contains(source, `extract_payload "$(fetch_payload`) {
		t.Fatal("fetch_payload failure can be masked when used directly as an argument")
	}
	for _, required := range []string{
		`archive="$(fetch_payload sparkle)"`,
		`archive="$(fetch_payload "$name")"`,
		`extract_payload "$archive"`,
	} {
		requireContains(t, "build-stage.sh", source, required)
	}
}

func TestPayloadDownloadsAreAtomicAndReuseVerifiedCache(t *testing.T) {
	source := readFile(t, scriptPath("build-stage.sh"))
	for _, required := range []string{
		`if [[ -f "$archive" ]]`,
		`actual="$(shasum -a 256 "$archive" | awk '{print $1}')"`,
		`archive_part="$archive.part"`,
		`--output "$archive_part"`,
		`mv "$archive_part" "$archive"`,
	} {
		requireContains(t, "build-stage.sh", source, required)
	}
}

func TestSnowflakeBuildCommandHasNoPatchArtifact(t *testing.T) {
	source := readFile(t, scriptPath("build-stage.sh"))
	if strings.Contains(source, "CGO_ENABLED=0 +") {
		t.Fatal("Snowflake build command contains a stray patch token")
	}
	requireContains(t, "build-stage.sh", source,
		`go build -trimpath -buildvcs=false -o "$APP_PATH/$(payload_field "$name" destination)/snowflake-client" ./client`)
}

func TestTorPayloadSelectsExecutableFileNotExtractionDirectory(t *testing.T) {
	source := readFile(t, scriptPath("build-stage.sh"))
	requireContains(t, "build-stage.sh", source,
		`find "$extraction" -type f -name tor -print -quit`)
	if strings.Contains(source, `-perm -111`) {
		t.Fatal("Tor lookup requires all execute bits and rejects valid mode-0700 payloads")
	}
}

func TestDevelopmentMacBuildPackagesManagedRuntimeAndVersion(t *testing.T) {
	makefile := readFile(t, repoPath("Makefile"))
	for _, required := range []string{
		"TELEGRAM_COMPANION_RUNTIME_BUNDLE ?=",
		`test -n "$(TELEGRAM_COMPANION_RUNTIME_BUNDLE)"`,
		`TELEGRAM_COMPANION_RUNTIME_BUNDLE='$(TELEGRAM_COMPANION_RUNTIME_BUNDLE)' bash ./scripts/package_macos_dev_runtime.sh`,
	} {
		requireContains(t, "Makefile", makefile, required)
	}

	source := readFile(t, repoPath("scripts", "package_macos_dev_runtime.sh"))
	for _, required := range []string{
		`runtime_bundle="${TELEGRAM_COMPANION_RUNTIME_BUNDLE:-}"`,
		`[[ -n "$runtime_bundle" && "$runtime_bundle" = /* ]]`,
		`runtime_source="$runtime_bundle/Contents/Resources/tor"`,
		`local tor_binary="$root/tor"`,
		`local lyrebird_binary="$root/pluggable_transports/lyrebird"`,
		`local config="$root/pluggable_transports/pt_config.json"`,
		`require_regular_file "$tor_binary"`,
		`require_regular_file "$lyrebird_binary"`,
		`require_regular_file "$config"`,
		`require_arm64 "$tor_binary"`,
		`require_arm64 "$lyrebird_binary"`,
		`ditto "$runtime_source" "$resources/tor"`,
		"runtime_tree_inventory_digest()",
		`source_runtime_digest="$(runtime_tree_inventory_digest "$runtime_source")"`,
		`staged_runtime_digest="$(runtime_tree_inventory_digest "$resources/tor")"`,
		`[[ "$source_runtime_digest" == "$staged_runtime_digest" ]]`,
		`expected_pt_config_sha256="$(jq -er '.payloads[] | select(.name == "lyrebird") | .pt_config_sha256' "$manifest")"`,
		`actual_pt_config_sha256="$(shasum -a 256 -- "$config"`,
		`[[ "$actual_pt_config_sha256" == "$expected_pt_config_sha256" ]]`,
		`require_arm64 "$nested"`,
		"frontend/package.json",
		"CFBundleShortVersionString",
		"CFBundleVersion",
		`codesign --force --sign - --timestamp=none "$nested"`,
		`codesign --verify --strict "$nested"`,
		"codesign --verify --deep --strict",
		`if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  main "$@"
fi`,
	} {
		requireContains(t, "package_macos_dev_runtime.sh", source, required)
	}
	for _, forbidden := range []string{
		"TELEGRAM_COMPANION_RUNTIME_DIR",
		`$HOME/.local/share/telegram-companion/runtime`,
		"snowflake-client",
		"codesign --force --deep",
	} {
		if strings.Contains(source, forbidden) {
			t.Errorf("development packager contains forbidden mutable or standalone runtime path %q", forbidden)
		}
	}

	inventoryBody := shellFunctionBody(source, "runtime_tree_inventory_digest")
	for _, required := range []string{
		`find . -print`,
		`LC_ALL=C sort`,
		`stat -f '%Lp'`,
		`shasum -a 256 -- "$relative"`,
	} {
		requireContains(t, "runtime_tree_inventory_digest", inventoryBody, required)
	}

	signingBody := shellFunctionBody(source, "sign_and_verify_nested_macho")
	assertShellMarkersInOrder(t, signingBody,
		`codesign --force --sign - --timestamp=none "$nested"`,
		`codesign --verify --strict "$nested"`,
	)

	mainBody := shellFunctionBody(source, "main")
	assertShellMarkersInOrder(t, mainBody,
		`require_no_symlink_ancestors "$app_path"`,
		`require_no_symlink_ancestors "$runtime_bundle"`,
		`require_directory "$app_path/Contents"`,
		`require_directory "$resources"`,
		`require_directory "$runtime_bundle/Contents"`,
		`require_directory "$runtime_bundle/Contents/Resources"`,
		`source_runtime_digest="$(runtime_tree_inventory_digest "$runtime_source")"`,
		`require_no_symlink_ancestors "$resources"`,
		`require_no_symlink_ancestors "$runtime_source"`,
		`require_disjoint_directories "$runtime_source" "$resources"`,
		`rm -rf -- "$resources/tor" "$resources/bin"`,
		`ditto "$runtime_source" "$resources/tor"`,
		`staged_runtime_digest="$(runtime_tree_inventory_digest "$resources/tor")"`,
		`[[ "$source_runtime_digest" == "$staged_runtime_digest" ]]`,
		`sign_and_verify_nested_macho "$resources/tor"`,
		`codesign --force --sign - --timestamp=none "$app_path"`,
		`codesign --verify --deep --strict "$app_path"`,
	)
}

func TestDevelopmentMacPackagerRejectsAncestorSymlinkBeforeRemoval(t *testing.T) {
	bash := parityFixtureBash(t)
	root := t.TempDir()
	realParent := filepath.Join(root, "real-parent")
	linkParent := filepath.Join(root, "link-parent")
	resources := filepath.Join(realParent, "Telegram Companion.app", "Contents", "Resources")
	sentinel := filepath.Join(resources, "tor", "sentinel")
	if err := os.MkdirAll(filepath.Dir(sentinel), 0o700); err != nil {
		t.Fatalf("create runtime deletion fixture: %v", err)
	}
	if err := os.WriteFile(sentinel, []byte("preserve"), 0o600); err != nil {
		t.Fatalf("write runtime deletion sentinel: %v", err)
	}
	createParityFixtureSymlink(t, linkParent, realParent)
	unsafeResources := filepath.Join(linkParent, "Telegram Companion.app", "Contents", "Resources")
	script := `
source "$1"
require_no_symlink_ancestors "$2"
rm -rf -- "$2/tor"
`
	command := exec.Command(bash, "-c", script, "runtime-ancestor-fixture",
		parityFixtureBashPath(t, bash, repoPath("scripts", "package_macos_dev_runtime.sh")),
		parityFixtureBashPath(t, bash, unsafeResources))
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("packager ancestor check accepted a symlink and reached deletion\n%s", output)
	}
	if !strings.Contains(string(output), "managed runtime path contains a symlink ancestor") {
		t.Fatalf("packager ancestor check failed for an unexpected reason\n%s", output)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("ancestor-symlink rejection did not preserve the deletion sentinel: %v", err)
	}
}

func TestAppcastUsesToolsFromPinnedSparklePayload(t *testing.T) {
	preflight := readFile(t, scriptPath("preflight.sh"))
	for _, forbidden := range []string{"require_command generate_appcast", "require_command sign_update"} {
		if strings.Contains(preflight, forbidden) {
			t.Errorf("preflight depends on an unpinned external Sparkle tool: %s", forbidden)
		}
	}
	appcast := readFile(t, scriptPath("appcast.sh"))
	requireContains(t, "appcast.sh", appcast, "\"$SPARKLE_SIGN_UPDATE\"")
	requireContains(t, "appcast.sh", appcast, "\"$SPARKLE_GENERATE_APPCAST\"")
}

func TestDirectDeliveryDMGIsSignedAndAssessed(t *testing.T) {
	notarize := readFile(t, scriptPath("notarize.sh"))
	create := strings.Index(notarize, `hdiutil create`)
	sign := strings.Index(notarize, `codesign --force --timestamp --sign "$APPLE_CODESIGN_IDENTITY" "$DMG_PATH"`)
	submit := strings.LastIndex(notarize, `xcrun notarytool submit "$DMG_PATH"`)
	if create < 0 || sign <= create || submit <= sign {
		t.Fatal("DMG must be created, Developer ID signed, and only then notarized")
	}

	verify := readFile(t, scriptPath("verify.sh"))
	requireContains(t, "verify.sh", verify, `codesign --verify --strict --verbose=4 "$DMG_PATH"`)
	requireContains(t, "verify.sh", verify, `spctl --assess --type open --context context:primary-signature --verbose=4 "$DMG_PATH"`)
}

func TestPublishedAssetsAreDownloadedAndChecksumVerified(t *testing.T) {
	publish := readFile(t, scriptPath("publish.sh"))
	requireContains(t, "publish.sh", publish, `gh release download "v$RELEASE_VERSION"`)
	requireContains(t, "publish.sh", publish, `shasum -a 256 -c SHA256SUMS`)
}

func TestPublishedAssetCleanupIsScopedToVerificationSubshell(t *testing.T) {
	publish := readFile(t, scriptPath("publish.sh"))
	requireContains(t, "publish.sh", publish, "verify_published_payloads() (")
	requireContains(t, "publish.sh", publish, `trap 'rm -rf "$download_dir"' EXIT`)
	if strings.Contains(publish, `trap 'rm -rf "$download_dir"' RETURN`) {
		t.Fatal("RETURN trap outlives the local download_dir under set -u")
	}
}

func TestAppcastPublicationIsAuthenticatedAndIdempotent(t *testing.T) {
	publish := readFile(t, scriptPath("publish.sh"))
	requireContains(t, "publish.sh", publish, `gh api --method PUT`)
	requireContains(t, "publish.sh", publish, `repos/$APPCAST_REPOSITORY/contents/appcast.xml`)
	requireContains(t, "publish.sh", publish, `existing_content`)
	requireContains(t, "publish.sh", publish, `existing_content" == "$encoded_appcast`)
	if strings.Contains(publish, "git push") {
		t.Fatal("appcast publication must use GH_TOKEN through gh instead of unauthenticated git push")
	}
}

func TestPublicReleaseCreationAndPublicationUseSeparatePreflights(t *testing.T) {
	releasePreflight := readFile(t, scriptPath("preflight-public-release.sh"))
	for _, name := range []string{
		"APPLE_CODESIGN_IDENTITY",
		"APPLE_NOTARY_KEYCHAIN_PROFILE",
		"SPARKLE_PRIVATE_KEY_FILE",
		"RELEASE_DOWNLOAD_BASE_URL",
	} {
		requireContains(t, "preflight-public-release.sh", releasePreflight, `require_env "`+name+`"`)
	}
	for _, forbidden := range []string{"GH_TOKEN", "RELEASE_REPOSITORY", "APPCAST_REPOSITORY"} {
		if strings.Contains(releasePreflight, forbidden) {
			t.Errorf("public release preflight unexpectedly requires publication setting %q", forbidden)
		}
	}

	publishPreflight := readFile(t, scriptPath("preflight-public-publish.sh"))
	for _, name := range []string{"GH_TOKEN", "RELEASE_REPOSITORY", "APPCAST_REPOSITORY"} {
		requireContains(t, "preflight-public-publish.sh", publishPreflight, `require_env "`+name+`"`)
	}
	for _, forbidden := range []string{"APPLE_CODESIGN_IDENTITY", "APPLE_NOTARY_KEYCHAIN_PROFILE", "SPARKLE_PRIVATE_KEY_FILE"} {
		if strings.Contains(publishPreflight, forbidden) {
			t.Errorf("public publication preflight unexpectedly requires release-creation setting %q", forbidden)
		}
	}
}

func TestReleaseVersionIsSafeForCFBundleVersion(t *testing.T) {
	lib := readFile(t, scriptPath("lib.sh"))
	requireContains(t, "lib.sh", lib, `^[0-9]+\.[0-9]+\.[0-9]+$`)
}

func TestVerificationChecksSparkleLinkageAndNestedSignatures(t *testing.T) {
	verify := readFile(t, scriptPath("verify.sh"))
	requireContains(t, "verify.sh", verify, `otool -L "$APP_PATH/Contents/MacOS/$APP_EXECUTABLE"`)
	requireContains(t, "verify.sh", verify, `@rpath/Sparkle.framework/Versions/B/Sparkle`)
	requireContains(t, "verify.sh", verify, `verify_nested_code`)
}

func TestCIExecutesManifestValidator(t *testing.T) {
	workflow := readFile(t, repoPath(".github", "workflows", "ci.yml"))
	requireContains(t, "ci.yml", workflow, `source scripts/release/macos-arm64/lib.sh`)
	requireContains(t, "ci.yml", workflow, `validate_payload_manifest`)
}

func TestSparkleSigningFollowsInnerToOuterOrder(t *testing.T) {
	source := readFile(t, scriptPath("sign.sh"))
	steps := []string{
		"\"$version/XPCServices/Installer.xpc\"",
		"\"$version/XPCServices/Downloader.xpc\"",
		"sign_path \"$version/Autoupdate\"",
		"sign_path \"$version/Updater.app\"",
		"sign_path \"$framework\"",
	}
	last := -1
	for _, step := range steps {
		position := strings.Index(source, step)
		if position < 0 {
			t.Fatalf("sign.sh does not explicitly sign %s", step)
		}
		if position <= last {
			t.Fatalf("Sparkle signing order is not inner-to-outer at %s", step)
		}
		last = position
	}
	requireContains(t, "sign.sh", source, "--preserve-metadata=entitlements")
}

func TestMakefileSeparatesLocalPrivateBuildsFromPublicPublication(t *testing.T) {
	source := readFile(t, repoPath("Makefile"))
	for _, required := range []string{
		"build-private-macos-arm64:",
		"preflight-private.sh",
		"sign-adhoc.sh",
		"package-private.sh",
		"verify-private.sh",
		"release-public-macos-arm64: build-public-macos-arm64",
		"publish-public-macos-arm64:",
		"preflight-public-release.sh",
		"preflight-public-publish.sh",
	} {
		requireContains(t, "Makefile", source, required)
	}
	if strings.Contains(source, "publish-private-macos-arm64") || strings.Contains(source, "release-private-macos-arm64") {
		t.Fatal("private/local Makefile targets must not publish public updates")
	}

	release := regexp.MustCompile(`(?m)^release-public-macos-arm64:\s*build-public-macos-arm64\s*\n((?:\t.*\n)+)`).FindStringSubmatch(source)
	if len(release) != 2 {
		t.Fatal("public release-creation target is missing")
	}
	for _, required := range []string{"sign.sh", "notarize.sh", "appcast.sh", "verify.sh"} {
		if !strings.Contains(release[1], required) {
			t.Errorf("release-public-macos-arm64 does not invoke %s", required)
		}
	}
	if strings.Contains(release[1], "publish.sh") {
		t.Fatal("public release creation must not publish")
	}

	publish := regexp.MustCompile(`(?m)^publish-public-macos-arm64:\s*\n((?:\t.*\n)+)`).FindStringSubmatch(source)
	if len(publish) != 2 {
		t.Fatal("standalone public publication target is missing")
	}
	for _, required := range []string{"preflight-public-publish.sh", "verify.sh", "publish.sh"} {
		if !strings.Contains(publish[1], required) {
			t.Errorf("publish-public-macos-arm64 does not invoke %s", required)
		}
	}
	if strings.Contains(publish[1], "build-stage.sh") || strings.Contains(publish[1], "appcast.sh") {
		t.Fatal("public publication must use already-created release artifacts")
	}
}

func TestOperatorDocsDescribeRevocationAndDoNotAdvertisePrivatePublishTargets(t *testing.T) {
	release := readFile(t, repoPath("docs", "operations", "macos-arm64-release.md"))
	for _, required := range []string{
		"REVOCATION_MANIFEST_URL",
		"REVOCATION_KEY_ID",
		"REVOCATION_PUBLIC_KEY",
		"https://raw.githubusercontent.com/GosuMicro555/telegram-companion-updates/main/revocations.tcrev",
		"revocation-2026-01",
		"финальная сборка 0.8.3 с поддержкой отзывов или более новая",
	} {
		requireContains(t, "macos-arm64-release.md", release, required)
	}
	for _, forbidden := range []string{"publish-private-macos-arm64", "release-private-macos-arm64"} {
		if strings.Contains(release, forbidden) {
			t.Fatalf("macos-arm64-release.md advertises nonexistent unsafe target %q", forbidden)
		}
	}
	for _, required := range []string{
		"make release-public-macos-arm64",
		"не выполняет внешнюю публикацию",
		"Только после проверки",
		"RELEASE_PUBLISH_CONFIRM=publish make publish-public-macos-arm64",
		"readback",
		"`appcast.xml` публикуется последним",
		"Для локальной private сборки нужны `PUBLIC_VERSION`, `LICENSE_PUBLIC_KEY`, `APPCAST_URL`, `SPARKLE_PUBLIC_ED_KEY`, `REVOCATION_MANIFEST_URL`, `REVOCATION_KEY_ID` и `REVOCATION_PUBLIC_KEY`",
	} {
		requireContains(t, "macos-arm64-release.md", release, required)
	}
	if strings.Contains(release, "RELEASE_PUBLISH_CONFIRM=publish make release-public-macos-arm64") {
		t.Fatal("macos-arm64-release.md describes release creation as publication")
	}

	runbook := readFile(t, repoPath("docs", "operations", "license-revocation.md"))
	for _, required := range []string{
		"revocation-2026-01",
		"TCPKEYBACKUP3",
		"seq0",
		"authenticated",
		"anonymous",
		"необратим",
		"одноразов",
	} {
		requireContains(t, "license-revocation.md", runbook, required)
	}
	for _, required := range []string{
		"Неинтерактивный CLI принимает пароль только через environment variable, а не аргумент командной строки, лог или echo.",
		"Поле пароля в GUI допустимо, но его нельзя сохранять, логировать или выводить через echo.",
	} {
		requireContains(t, "license-revocation.md", runbook, required)
	}

	readme := readFile(t, repoPath("README.md"))
	requireContains(t, "README.md", readme, "docs/operations/license-revocation.md")
}

func TestPrivatePreflightNeedsNoAppleOrPublicationCredentials(t *testing.T) {
	source := readFile(t, scriptPath("preflight-private.sh"))
	for _, name := range []string{
		"RELEASE_VERSION",
		"APPCAST_URL",
		"BUILD_TAGS",
		"LICENSE_PUBLIC_KEY",
		"SPARKLE_PUBLIC_ED_KEY",
	} {
		requireContains(t, "preflight-private.sh", source, `require_env "`+name+`"`)
	}
	requireContains(t, "preflight-private.sh", source, "validate_build_metadata")

	for _, forbidden := range []string{
		"APPLE_CODESIGN_IDENTITY",
		"APPLE_NOTARY_KEYCHAIN_PROFILE",
		"GH_TOKEN",
		"SPARKLE_PRIVATE_KEY_FILE",
		"notarytool",
		"stapler",
	} {
		if strings.Contains(source, forbidden) {
			t.Errorf("private preflight contains public release requirement %q", forbidden)
		}
	}
}

func TestPrivatePublicationEntryPointFailsClosed(t *testing.T) {
	source := readFile(t, scriptPath("preflight-private-publish.sh"))
	requireContains(t, "preflight-private-publish.sh", source, "private builds cannot publish public updates")
	for _, forbidden := range []string{"GH_TOKEN", "SPARKLE_PRIVATE_KEY_FILE", "validate_publish_metadata"} {
		if strings.Contains(source, forbidden) {
			t.Errorf("private publication entry point contains publication setting %q", forbidden)
		}
	}
}

func TestOnlyConfirmedPublicChannelCanPublishUpdates(t *testing.T) {
	lib := readFile(t, scriptPath("lib.sh"))
	requireContains(t, "lib.sh", lib, `RELEASE_CHANNEL="${RELEASE_CHANNEL:-local}"`)
	requireContains(t, "lib.sh", lib, "require_public_release_channel()")
	requireContains(t, "lib.sh", lib, `[[ "$RELEASE_CHANNEL" == "public" ]]`)

	publish := readFile(t, scriptPath("publish.sh"))
	requireContains(t, "publish.sh", publish, "require_public_release_channel")
	requireContains(t, "publish.sh", publish, `RELEASE_PUBLISH_CONFIRM:-`)

	makefile := readFile(t, repoPath("Makefile"))
	requireContains(t, "Makefile", makefile, "RELEASE_CHANNEL=public")
	requireContains(t, "Makefile", makefile, "RELEASE_CHANNEL=local")

	dual := readFile(t, scriptPath("build-dual-local.sh"))
	requireContains(t, "build-dual-local.sh", dual, `RELEASE_CHANNEL="local"`)
}

func TestStandardBuildScriptsDoNotPackageExistingUserData(t *testing.T) {
	for _, name := range []string{"build-stage.sh", "package-private.sh", "preflight.sh"} {
		source := readFile(t, scriptPath(name))
		for _, forbidden := range []string{"SOURCE_ROOT", "SOURCE_DATA", "FIRST_LAUNCH_SEED_PATH"} {
			if strings.Contains(source, forbidden) {
				t.Errorf("%s packages or reads existing user-data input %q", name, forbidden)
			}
		}
	}
}

func TestPrivateAdHocSigningIsExplicitAndInnerToOuter(t *testing.T) {
	source := readFile(t, scriptPath("sign-adhoc.sh"))
	steps := []string{
		`"$version/XPCServices/Installer.xpc"`,
		`"$version/XPCServices/Downloader.xpc"`,
		`adhoc_sign_path "$version/Autoupdate"`,
		`adhoc_sign_path "$version/Updater.app"`,
		`adhoc_sign_path "$framework"`,
		`--sign - "$APP_PATH"`,
	}
	last := -1
	for _, step := range steps {
		position := strings.Index(source, step)
		if position < 0 {
			t.Fatalf("sign-adhoc.sh does not explicitly sign %s", step)
		}
		if position <= last {
			t.Fatalf("ad-hoc signing order is not inner-to-outer at %s", step)
		}
		last = position
	}
	requireContains(t, "sign-adhoc.sh", source, "codesign --force --sign -")
	for _, forbidden := range []string{"--deep", "--options runtime", "--timestamp", "APPLE_CODESIGN_IDENTITY"} {
		if strings.Contains(source, forbidden) {
			t.Errorf("ad-hoc signing contains forbidden production option %q", forbidden)
		}
	}
}

func TestPrivatePackageContainsInstallGuideAndApplicationsAlias(t *testing.T) {
	source := readFile(t, scriptPath("package-private.sh"))
	for _, required := range []string{
		`ditto -c -k --sequesterRsrc --keepParent "$UPDATE_APP_PATH" "$UPDATE_ZIP"`,
		`ln -s /Applications "$DMG_STAGE/Applications"`,
		"ПЕРВЫЙ ЗАПУСК.txt",
		`hdiutil create`,
		`shasum -a 256`,
		"Перетащите Telegram Companion.app в папку Applications",
		"обычным двойным кликом",
		"Системные настройки -> Конфиденциальность и безопасность",
		"Всё равно открыть",
		"PRIVATE-DMG-SHA256SUMS",
	} {
		requireContains(t, "package-private.sh", source, required)
	}
	guideStart := strings.Index(source, "TELEGRAM COMPANION: ПЕРВЫЙ ЗАПУСК")
	if guideStart < 0 {
		t.Fatal("private package guide heredoc is missing")
	}
	guideEnd := strings.Index(source[guideStart:], "\nEOF")
	if guideEnd < 0 {
		t.Fatal("private package guide heredoc is missing")
	}
	guide := source[guideStart : guideStart+guideEnd]
	for _, forbidden := range []string{
		"Терминал",
		"Terminal",
		"xattr",
		"com.apple.quarantine",
		"spctl",
		"Gatekeeper",
	} {
		if strings.Contains(guide, forbidden) {
			t.Errorf("private package guide contains forbidden command-line fallback %q", forbidden)
		}
	}
}

func TestPrivateVerificationChecksBundleAndDMGWithoutNotarization(t *testing.T) {
	source := readFile(t, scriptPath("verify-private.sh"))
	for _, required := range []string{
		`codesign --verify --strict --verbose=4 "$APP_PATH"`,
		"Signature=adhoc",
		`hdiutil verify "$DMG_PATH"`,
		`require_arm64_bundle "$APP_PATH"`,
		`@rpath/Sparkle.framework/Versions/B/Sparkle`,
	} {
		requireContains(t, "verify-private.sh", source, required)
	}
	for _, forbidden := range []string{"notarytool", "stapler", "spctl --master-disable", "APPLE_"} {
		if strings.Contains(source, forbidden) {
			t.Errorf("private verification contains forbidden dependency %q", forbidden)
		}
	}
}

func TestPrivateAdHocSignatureCheckDoesNotPipeCodesignIntoGrepQ(t *testing.T) {
	source := readFile(t, scriptPath("verify-private.sh"))
	if strings.Contains(source, `codesign -dv --verbose=4 "$APP_PATH" 2>&1 | grep -Fq`) {
		t.Fatal("grep -q can close the pipe early and make codesign fail under pipefail")
	}
	requireContains(t, "verify-private.sh", source,
		`signature_details="$(codesign -dv --verbose=4 "$APP_PATH" 2>&1)"`)
	requireContains(t, "verify-private.sh", source,
		`grep -Fq 'Signature=adhoc' <<<"$signature_details"`)
}

func TestPrivateScriptsNeverInvokeAppleNotarization(t *testing.T) {
	for _, name := range []string{
		"preflight-private.sh",
		"preflight-private-publish.sh",
		"sign-adhoc.sh",
		"package-private.sh",
		"verify-private.sh",
	} {
		source := readFile(t, scriptPath(name))
		for _, forbidden := range []string{"notarytool", "stapler", "APPLE_CODESIGN_IDENTITY", "APPLE_NOTARY_KEYCHAIN_PROFILE"} {
			if strings.Contains(source, forbidden) {
				t.Errorf("%s contains %q", name, forbidden)
			}
		}
	}
}

func TestAppcastVerifiesExactUpdateArchiveURL(t *testing.T) {
	source := readFile(t, scriptPath("appcast.sh"))
	for _, required := range []string{
		"expected_update_url",
		"actual_update_url",
		"xmllint --xpath",
		`${RELEASE_DOWNLOAD_BASE_URL%/}/$(basename "$UPDATE_ZIP")`,
		`[[ "$actual_update_url" == "$expected_update_url" ]]`,
	} {
		requireContains(t, "appcast.sh", source, required)
	}
}

func validatePinnedPayload(item payload) error {
	if strings.TrimSpace(item.Name) == "" || strings.TrimSpace(item.Version) == "" {
		return errors.New("name and version are required")
	}
	if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(item.SHA256) {
		return errors.New("sha256 must be 64 lowercase hexadecimal characters")
	}
	if regexp.MustCompile(`^(?:0{64}|f{64})$`).MatchString(item.SHA256) {
		return errors.New("sha256 placeholder is forbidden")
	}
	parsed, err := url.Parse(item.URL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("url must be an immutable HTTPS URL without query or fragment")
	}
	lowerURL := strings.ToLower(item.URL)
	if regexp.MustCompile(`(?:/latest/|/main/|/master/|/head(?:/|$))`).MatchString(lowerURL) {
		return errors.New("mutable release URL is forbidden")
	}
	version := strings.TrimPrefix(strings.ToLower(item.Version), "v")
	if !strings.Contains(lowerURL, version) {
		return fmt.Errorf("url does not contain pinned version %q", item.Version)
	}
	for field, value := range map[string]string{
		"archive_type": item.ArchiveType,
		"source":       item.Source,
		"destination":  item.Destination,
		"license":      item.License,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", field)
		}
	}
	return nil
}

func rejectCodesignDeep(source string) error {
	if regexp.MustCompile(`(?m)^[^#\n]*\bcodesign\b[^\n]*--deep\b`).MatchString(source) {
		return errors.New("codesign --deep is forbidden; sign nested code explicitly")
	}
	return nil
}

func validateTarget(manifest payloadManifest) error {
	if manifest.Target.OS != wantTargetOS || manifest.Target.Arch != wantTargetArch {
		return fmt.Errorf("release target must be %s/%s", wantTargetOS, wantTargetArch)
	}
	if manifest.Target.MinimumOS != wantMinimumOS {
		return fmt.Errorf("minimum macOS must be %s", wantMinimumOS)
	}
	return nil
}

func validatePublishOrder(source string) error {
	steps := []string{"publish_payload_assets", "verify_published_payloads", "publish_appcast_last"}
	last := -1
	for _, step := range steps {
		pattern := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(step) + `\s*$`)
		loc := pattern.FindStringIndex(source)
		if loc == nil {
			return fmt.Errorf("publish step %q is missing", step)
		}
		if loc[0] <= last {
			return errors.New("appcast publication must follow payload upload and verification")
		}
		last = loc[0]
	}
	return nil
}

func validateMinimumSystemVersion(plist []byte) error {
	pattern := regexp.MustCompile(`(?s)<key>LSMinimumSystemVersion</key>\s*<string>\s*([^<]+?)\s*</string>`)
	match := pattern.FindSubmatch(plist)
	if len(match) != 2 || string(match[1]) != wantMinimumOS {
		return fmt.Errorf("LSMinimumSystemVersion must be %s", wantMinimumOS)
	}
	return nil
}

func readManifest(t *testing.T) payloadManifest {
	t.Helper()
	data := readFileBytes(t, repoPath("resources", "macos-arm64", "payload-manifest.json"))
	var manifest payloadManifest
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		t.Fatalf("decode payload manifest: %v", err)
	}
	return manifest
}

func TestReleaseUsesOneConfiguredWailsExecutable(t *testing.T) {
	lib := readFile(t, scriptPath("lib.sh"))
	build := readFile(t, scriptPath("build-stage.sh"))
	for _, name := range []string{"preflight.sh", "preflight-private.sh"} {
		preflight := readFile(t, scriptPath(name))
		requireContains(t, name, preflight, `require_command "$WAILS_BIN"`)
		if strings.Contains(preflight, "require_command wails") || strings.Contains(preflight, "for command in wails ") {
			t.Errorf("%s validates a different Wails executable", name)
		}
	}
	requireContains(t, "lib.sh", lib, `WAILS_BIN="${WAILS_BIN:-wails}"`)
	requireContains(t, "build-stage.sh", build, `"$WAILS_BIN" build -clean`)
	if strings.Contains(build, "wails build -clean") {
		t.Fatal("build-stage.sh bypasses WAILS_BIN")
	}
	dual := readFile(t, scriptPath("build-dual-local.sh"))
	requireContains(t, "build-dual-local.sh", dual, "export WAILS_BIN")
	makefile := readFile(t, repoPath("Makefile"))
	requireContains(t, "Makefile", makefile, "WAILS_BIN='$(WAILS)'")
}

func TestReleaseBuildsDoNotRegenerateTrackedWailsBindings(t *testing.T) {
	for _, name := range []string{"build-dual-local.sh", "build-stage.sh"} {
		source := readFile(t, scriptPath(name))
		requireContains(t, name, source, `"$WAILS_BIN" build -clean -skipbindings`)
	}
}

func readScripts(t *testing.T) map[string]string {
	t.Helper()
	result := make(map[string]string, len(releaseScripts))
	names := append([]string(nil), releaseScripts...)
	sort.Strings(names)
	for _, name := range names {
		result[name] = readFile(t, scriptPath(name))
	}
	return result
}

func requireContains(t *testing.T, name, source, want string) {
	t.Helper()
	if !strings.Contains(source, want) {
		t.Errorf("%s does not contain %q", name, want)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	return string(readFileBytes(t, path))
}

func readFileBytes(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

func scriptPath(name string) string {
	return repoPath("scripts", "release", "macos-arm64", name)
}

func repoPath(parts ...string) string {
	all := append([]string{"..", "..", ".."}, parts...)
	return filepath.Clean(filepath.Join(all...))
}
