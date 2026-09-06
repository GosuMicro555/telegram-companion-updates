package buildinfo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMakefileExposesFailClosedPublicReleaseTargets(t *testing.T) {
	source := readRepositoryFile(t, "Makefile")
	for _, required := range []string{
		"build-public-macos-arm64:",
		"release-public-macos-arm64:",
		"build-license-generator-windows:",
		"GOOS=darwin GOARCH=arm64",
		"MACOSX_DEPLOYMENT_TARGET=13.0",
		"desktop,public_macos_arm64",
		"telegram-companion/internal/buildinfo.version",
		"telegram-companion/internal/buildinfo.licensePublicKey",
		"telegram-companion/internal/buildinfo.appcastURL",
		"telegram-companion/internal/buildinfo.revocationManifestURL",
		"telegram-companion/internal/buildinfo.revocationKeyID",
		"telegram-companion/internal/buildinfo.revocationPublicKey",
		"GOOS=windows GOARCH=amd64 CGO_ENABLED=0",
		"-H=windowsgui",
	} {
		if !strings.Contains(source, required) {
			t.Errorf("Makefile does not contain %q", required)
		}
	}
}

func TestCICompilesPublicMacOSARM64ContractWithoutPublishing(t *testing.T) {
	source := readRepositoryFile(t, ".github", "workflows", "ci.yml")
	for _, required := range []string{
		"public-macos-arm64-contract:",
		"runs-on: macos-14",
		"Fetch pinned Sparkle framework",
		"payload-manifest.json",
		"Sparkle.framework",
		"GOOS: darwin",
		"GOARCH: arm64",
		"CGO_ENABLED: \"1\"",
		"desktop,public_macos_arm64",
		"revocationManifestURL",
		"revocationKeyID",
		"revocationPublicKey",
	} {
		if !strings.Contains(source, required) {
			t.Errorf("CI public build contract does not contain %q", required)
		}
	}
	for _, forbidden := range []string{"gh release create", "notarytool submit", "scripts/release/macos-arm64/publish.sh"} {
		if strings.Contains(source, forbidden) {
			t.Errorf("CI must not publish or notarize public releases: found %q", forbidden)
		}
	}
}

func readRepositoryFile(t *testing.T, parts ...string) string {
	t.Helper()
	pathParts := append([]string{"..", ".."}, parts...)
	data, err := os.ReadFile(filepath.Clean(filepath.Join(pathParts...)))
	if err != nil {
		t.Fatalf("read repository file: %v", err)
	}
	return string(data)
}
