package macosarm64_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const (
	removedCapabilityVerifier        = "verify_removed_transfer_capability_absent"
	removedCapabilityArchiveVerifier = "verify_removed_transfer_update_archive_absent"
	removedCapabilityDMGVerifier     = "verify_removed_transfer_private_dmg_absent"
)

func TestReleaseVerifiersInvokeRemovedCapabilityArtifactScans(t *testing.T) {
	for _, name := range []string{"verify.sh", "verify-private.sh"} {
		source, err := os.ReadFile(scriptPath(name))
		if err != nil {
			t.Fatal(err)
		}
		for _, required := range []string{
			removedCapabilityVerifier + ` "$APP_PATH"`,
			removedCapabilityArchiveVerifier + ` "$UPDATE_ZIP"`,
		} {
			if !strings.Contains(string(source), required) {
				t.Errorf("%s does not invoke %q", name, required)
			}
		}
		if name == "verify-private.sh" {
			required := removedCapabilityDMGVerifier + ` "$DMG_PATH"`
			if !strings.Contains(string(source), required) {
				t.Errorf("%s does not invoke %q", name, required)
			}
		}
	}
}

func TestRemovedCapabilityPrivateDMGVerifierUsesMountedAppInventory(t *testing.T) {
	source, err := os.ReadFile(scriptPath("lib.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		removedCapabilityDMGVerifier + "()",
		`cleanup_prefix="${TMPDIR:-/tmp}/tc-private-dmg-inspect."`,
		`[[ "$scratch" == "$cleanup_prefix"* ]] || die "unsafe private DMG inspection directory"`,
		`chmod 700 "$scratch"`,
		`hdiutil attach -readonly -nobrowse`,
		`verify_removed_transfer_capability_absent "$mounted_app"`,
		`hdiutil detach "$device"`,
		`hdiutil detach "$mount_point"`,
		`trap cleanup_private_dmg_scan EXIT`,
		`trap 'exit 1' HUP INT TERM`,
		`find "$mount_point" -mindepth 1 -maxdepth 1 -name '*.app' -print`,
		`private DMG must contain exactly one application bundle`,
		`mounted_app="$mount_point/$APP_NAME.app"`,
		`[[ -d "$mounted_app" ]] && [[ ! -L "$mounted_app" ]]`,
	} {
		if !strings.Contains(string(source), required) {
			t.Errorf("private DMG verifier does not contain %q", required)
		}
	}
	for _, markerSource := range localImportMarkerSourceTokens() {
		if !strings.Contains(string(source), markerSource) {
			t.Errorf("application artifact marker array does not contain %q", markerSource)
		}
	}
	if strings.Contains(string(source), `find "$mount_point" -mindepth 1 -maxdepth 1 -print | wc`) ||
		strings.Contains(string(source), `private DMG contains unexpected top-level entries`) {
		t.Error("private DMG verifier must count only app entries and permit known non-app top-level entries")
	}
}

func TestRemovedCapabilityArtifactScanRejectsEveryLegacyMarker(t *testing.T) {
	markers := legacyTransferMarkers()
	app := newTestApp(t)
	probe := filepath.Join(app, "Contents", "Resources", "probe.bin")
	for _, marker := range markers {
		if err := os.WriteFile(probe, []byte("prefix\x00"+marker+"\x00suffix"), 0o644); err != nil {
			t.Fatal(err)
		}
		output, err := runVerifier(removedCapabilityVerifier, app)
		requireArtifactMarkerRejected(t, marker, output, err)
	}
}

func TestRemovedCapabilityArtifactScanAcceptsCleanBundle(t *testing.T) {
	app := newTestApp(t)
	probe := filepath.Join(app, "Contents", "Resources", "probe.bin")
	if err := os.WriteFile(probe, []byte("local tdata import remains available"), 0o644); err != nil {
		t.Fatal(err)
	}
	if output, err := runVerifier(removedCapabilityVerifier, app); err != nil {
		t.Fatalf("clean bundle rejected: %v (%s)", err, output)
	}
}

func TestRemovedCapabilityUpdateArchiveScanRejectsLegacyMarker(t *testing.T) {
	requireDitto(t)
	app := newTestApp(t)
	probe := filepath.Join(app, "Contents", "Resources", "probe.bin")
	for _, marker := range legacyTransferMarkers() {
		if err := os.WriteFile(probe, []byte(marker), 0o644); err != nil {
			t.Fatal(err)
		}
		archive := archiveTestApp(t, app)
		output, err := runVerifier(removedCapabilityArchiveVerifier, archive)
		requireArtifactMarkerRejected(t, marker, output, err)
	}
}

func requireArtifactMarkerRejected(t *testing.T, marker string, output []byte, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("artifact marker was accepted: %q (%s)", marker, output)
	}
	if !bytes.Contains(output, []byte("removed transfer capability remains in application bundle")) {
		t.Fatalf("artifact marker rejection lacked the fixed diagnostic: %q (%s)", marker, output)
	}
}

func TestRemovedCapabilityUpdateArchiveScanAcceptsCleanBundle(t *testing.T) {
	requireDitto(t)
	app := newTestApp(t)
	archive := archiveTestApp(t, app)
	if output, err := runVerifier(removedCapabilityArchiveVerifier, archive); err != nil {
		t.Fatalf("clean update archive rejected: %v (%s)", err, output)
	}
}

func legacyTransferMarkers() []string {
	return []string{
		"Get" + "MasterInboxStatus",
		"List" + "MasterInbox",
		"Ack" + "MasterInbox",
		"Master" + "TDataInbox",
		"MASTER_TDATA_" + "INBOX_ENABLED",
		"tdata new " + "accounts",
		"tdata" + "relay",
		"RELAY_" + "BASE_URL",
		"MASTER_" + "INBOX_ID",
		"MASTER_" + "PUBLIC_KEY",
		"Relay" + "Status",
		"Claim" + "Relay",
		"Update" + "Relay",
		"Relay" + "BaseURL",
		"Relay" + "InboxID",
		"Relay" + "PublicKey",
		"relay" + "BaseURL",
		"master" + "InboxID",
		"master" + "PublicKey",
		"master-inbox-" + "private-key-v1",
		"master-inbox-" + "admin-token-v1",
		"SubmitTData" + "DriveLinks",
		"ListTData" + "ImportItems",
		"TData" + "ImportModal",
		"ConfigureTData" + "Import",
		"NewSession" + "Importer",
		"NewTData" + "ImportStore",
		"tdata-" + "import",
		"Add tdata " + "accounts",
		"Добавить tdata " + "аккаунты",
	}
}

func localImportMarkerSourceTokens() []string {
	return []string{
		`"SubmitTData""DriveLinks"`,
		`"ListTData""ImportItems"`,
		`"TData""ImportModal"`,
		`"ConfigureTData""Import"`,
		`"NewSession""Importer"`,
		`"NewTData""ImportStore"`,
		`"tdata-""import"`,
		`"Add tdata ""accounts"`,
		`"Добавить tdata ""аккаунты"`,
	}
}

func newTestApp(t *testing.T) string {
	t.Helper()
	app := filepath.Join(t.TempDir(), "Telegram Companion.app")
	if err := os.MkdirAll(filepath.Join(app, "Contents", "Resources"), 0o755); err != nil {
		t.Fatal(err)
	}
	return app
}

func runVerifier(name, target string) ([]byte, error) {
	command := exec.Command("bash", "-c", `source "$1"; "$2" "$3"`, "removed-capability-test", scriptPath("lib.sh"), name, target)
	return command.CombinedOutput()
}

func requireDitto(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("ditto archive verification is macOS-only")
	}
	if _, err := exec.LookPath("ditto"); err != nil {
		t.Skip("ditto is unavailable")
	}
}

func archiveTestApp(t *testing.T, app string) string {
	t.Helper()
	archive := filepath.Join(t.TempDir(), "update.zip")
	command := exec.Command("ditto", "-c", "-k", "--sequesterRsrc", "--keepParent", app, archive)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("create update archive: %v (%s)", err, output)
	}
	return archive
}
