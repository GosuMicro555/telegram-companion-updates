package macosarm64_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSeededPrivatePackageContract(t *testing.T) {
	source := readSeededPackageScript(t)

	for _, required := range []string{
		"set -euo pipefail",
		`require_env "PUBLIC_APP_PATH"`,
		`require_env "SEEDED_STATE_PATH"`,
		`require_env "TARGET_LICENSE_FILE"`,
		`./cmd/state-bundle-validate`,
		`-state "$SEEDED_STATE_PATH"`,
		`-license-file "$TARGET_LICENSE_FILE"`,
		`-version "$RELEASE_VERSION"`,
		`ditto "$PUBLIC_APP_PATH" "$STAGED_APP_PATH"`,
		`Contents/Resources/bootstrap-state/state.tcs`,
		`install -d -m 0755 "$(dirname "$STAGED_STATE_PATH")"`,
		`install -m 0644 "$SEEDED_STATE_PATH" "$STAGED_STATE_PATH"`,
		`APP_PATH="$STAGED_APP_PATH" bash "$SCRIPT_DIR/sign-adhoc.sh"`,
		`Telegram-Companion-$RELEASE_VERSION-arm64-Public-Seeded-PRIVATE.dmg`,
		"shasum -a 256",
		"trap cleanup EXIT",
	} {
		if !strings.Contains(source, required) {
			t.Errorf("package-seeded-private.sh does not contain %q", required)
		}
	}

	for _, forbidden := range []string{
		"set -x",
		"publish.sh",
		"appcast.sh",
		"gh release",
		"notarytool",
		"stapler",
		`cat "$SEEDED_STATE_PATH"`,
	} {
		if strings.Contains(source, forbidden) {
			t.Errorf("package-seeded-private.sh contains forbidden operation %q", forbidden)
		}
	}

	copyAt := strings.Index(source, `ditto "$PUBLIC_APP_PATH" "$STAGED_APP_PATH"`)
	seedAt := strings.Index(source, `install -m 0644 "$SEEDED_STATE_PATH" "$STAGED_STATE_PATH"`)
	signAt := strings.Index(source, `APP_PATH="$STAGED_APP_PATH" bash "$SCRIPT_DIR/sign-adhoc.sh"`)
	dmgAt := strings.Index(source, "hdiutil create")
	if copyAt < 0 || seedAt <= copyAt || signAt <= seedAt || dmgAt <= signAt {
		t.Fatal("seeded package steps must copy, seed, ad-hoc sign, then create the DMG")
	}
}

func readSeededPackageScript(t *testing.T) string {
	t.Helper()
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate seeded package contract test")
	}
	path := filepath.Join(filepath.Dir(current), "package-seeded-private.sh")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(contents)
}
