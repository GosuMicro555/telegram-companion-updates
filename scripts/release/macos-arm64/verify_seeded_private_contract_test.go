package macosarm64_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSeededPrivateVerifierContract(t *testing.T) {
	source := readSeededPrivateVerifier(t)

	for _, required := range []string{
		"set -euo pipefail",
		`readonly EXPECTED_VERSION="0.8.2"`,
		`shasum -a 256 -c`,
		`hdiutil verify "$SEEDED_DMG_PATH"`,
		`hdiutil attach -readonly -nobrowse`,
		`$1 ~ /^\/dev\// { device=$1 } END { print device }`,
		`mount | grep -F " on $MOUNT_POINT "`,
		`read-only`,
		`CFBundleShortVersionString`,
		`CFBundleVersion`,
		`verify_bundle_identity`,
		`CFBundleDisplayName`,
		`CFBundleExecutable`,
		`CFBundleIdentifier`,
		`CFBundleName`,
		`CFBundlePackageType`,
		`NSHighResolutionCapable`,
		`com.telegramcompanion.desktop`,
		`telegram-companion`,
		`[[ -x "$MOUNTED_APP_PATH/Contents/MacOS/$executable" ]]`,
		`require_arm64_bundle "$MOUNTED_APP_PATH"`,
		`codesign --verify --strict --verbose=4 "$MOUNTED_APP_PATH"`,
		`bootstrap-state/state.tcs`,
		`[[ "$state_count" -eq 1 ]]`,
		`head -c 7 "$state_path"`,
		`TCSEED1`,
		`app.db`,
		`tdata`,
		`session`,
		`.tcomplicense`,
		`TCPLIC1.`,
		`stat -f '%Lp'`,
		`[[ "$mode" == "755" ]]`,
		`[[ "$mode" == "$expected_mode" ]]`,
		`hdiutil detach "$ATTACHED_DEVICE"`,
		`trap cleanup EXIT`,
	} {
		if !strings.Contains(source, required) {
			t.Errorf("verify-seeded-private.sh does not contain %q", required)
		}
	}

	for _, forbidden := range []string{
		"set -x",
		"codesign --force",
		"hdiutil convert",
		"hdiutil resize",
		"mount -uw",
		"chmod ",
		"chown ",
		"xattr ",
	} {
		if strings.Contains(source, forbidden) {
			t.Errorf("verify-seeded-private.sh contains mutating operation %q", forbidden)
		}
	}

	attachAt := strings.Index(source, `hdiutil attach -readonly -nobrowse`)
	inspectAt := strings.Index(source, `codesign --verify --strict --verbose=4 "$MOUNTED_APP_PATH"`)
	detachAt := strings.Index(source, `hdiutil detach "$ATTACHED_DEVICE"`)
	if attachAt < 0 || inspectAt <= attachAt || detachAt < 0 {
		t.Fatal("seeded verifier must mount read-only, inspect the mounted app, and detach during cleanup")
	}
}

func readSeededPrivateVerifier(t *testing.T) string {
	t.Helper()
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate seeded private verifier contract test")
	}
	path := filepath.Join(filepath.Dir(current), "verify-seeded-private.sh")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(contents)
}
