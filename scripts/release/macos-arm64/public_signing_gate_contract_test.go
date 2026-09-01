package macosarm64_test

import (
	"strings"
	"testing"
)

func TestPublicPublicationUsesDistributionSignatureGate(t *testing.T) {
	lib := readFile(t, scriptPath("lib.sh"))
	preflight := readFile(t, scriptPath("preflight-public-publish.sh"))
	publish := readFile(t, scriptPath("publish.sh"))

	for _, required := range []string{
		"require_public_distribution_signature()",
		"verify_public_update_archive_signature()",
		"Signature=adhoc",
		"Authority=Developer ID Application:",
		"TeamIdentifier=",
		"public release blocked",
	} {
		requireContains(t, "lib.sh", lib, required)
	}

	for name, source := range map[string]string{
		"preflight-public-publish.sh": preflight,
		"publish.sh":                 publish,
	} {
		for _, required := range []string{
			"require_public_distribution_signature \"$PUBLIC_APP_PATH\"",
			"verify_public_update_archive_signature \"$UPDATE_ZIP\"",
		} {
			requireContains(t, name, source, required)
		}
		if strings.Contains(source, "sign-adhoc.sh") {
			t.Fatalf("%s must never use the private ad-hoc signer", name)
		}
	}
}

func TestPublicSignatureGateBindsCertificateTeamToCodeDirectoryTeam(t *testing.T) {
	lib := readFile(t, scriptPath("lib.sh"))
	for _, required := range []string{
		"authority_team=",
		"team_identifier=",
		`[[ "$authority_team" == "$team_identifier" ]]`,
	} {
		requireContains(t, "lib.sh", lib, required)
	}
}

func TestPublicSigningGateRunsBeforeAnyPublicationMutation(t *testing.T) {
	for _, name := range []string{"preflight-public-publish.sh", "publish.sh"} {
		source := readFile(t, scriptPath(name))
		mainAt := strings.Index(source, "main() {")
		if mainAt < 0 {
			t.Fatalf("%s must define main", name)
		}
		main := source[mainAt:]
		parityAt := strings.Index(main, `bash "$SCRIPT_DIR/master-public-parity.sh" verify`)
		bundleGateAt := strings.Index(main, `require_public_distribution_signature "$PUBLIC_APP_PATH"`)
		archiveGateAt := strings.Index(main, `verify_public_update_archive_signature "$UPDATE_ZIP"`)
		mutationAt := strings.Index(main, "publish_payload_assets")
		if parityAt < 0 || bundleGateAt <= parityAt || archiveGateAt <= bundleGateAt {
			t.Fatalf("%s must verify parity before checking signed public artifacts", name)
		}
		if name == "publish.sh" && (mutationAt < 0 || mutationAt <= archiveGateAt) {
			t.Fatalf("%s must complete signature gates before uploading assets", name)
		}
	}
}

func TestPublicSigningGateDoesNotAlterPrivateAdHocWorkflow(t *testing.T) {
	for _, name := range []string{"sign-adhoc.sh", "verify-private.sh", "package-private.sh", "preflight-private.sh"} {
		source := readFile(t, scriptPath(name))
		if strings.Contains(source, "require_public_distribution_signature") ||
			strings.Contains(source, "verify_public_update_archive_signature") {
			t.Fatalf("%s must remain independent of the public distribution gate", name)
		}
	}
}

func TestPublicSigningGateKeepsCodesignDiagnosticsSanitized(t *testing.T) {
	lib := readFile(t, scriptPath("lib.sh"))
	if strings.Contains(lib, `die "$signature_details`) || strings.Contains(lib, `printf '%s`+"\\n"+`"$signature_details`) {
		t.Fatal("public signing gate must not print raw codesign diagnostics")
	}
	for _, required := range []string{
		`codesign --verify --strict --verbose=4 "$bundle" >/dev/null 2>&1`,
		`signature_details="$(codesign -dv --verbose=4 "$bundle" 2>&1)"`,
	} {
		requireContains(t, "lib.sh", lib, required)
	}
}
