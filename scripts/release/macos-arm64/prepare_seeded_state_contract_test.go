package macosarm64_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPrepareSeededStateContract(t *testing.T) {
	root := releaseScriptDirectory(t)
	script := readContractFile(t, filepath.Join(root, "prepare-seeded-state.sh"))
	sql := readContractFile(t, filepath.Join(root, "sanitize-seeded-state.sql"))

	for _, required := range []string{
		`pgrep -x "Telegram Companion"`,
		`sqlite3 "$SOURCE_DATA/app.db"`,
		`.backup '$STAGED_DATA/app.db'`,
		`sanitize-seeded-state.sql`,
		`"$GO_BIN" run ./cmd/state-bundle`,
		`-license-file "$TARGET_LICENSE_FILE"`,
		`printf '%s\n' "$EXCLUDED_SECRETS"`,
		`application-state.bolt`,
		`gotd-import-staging`,
		`tdata`,
		`sessions`,
	} {
		if !strings.Contains(script, required) {
			t.Errorf("prepare-seeded-state.sh does not contain %q", required)
		}
	}

	for _, required := range []string{
		"CHECK (version = 32)",
		"UPDATE accounts",
		"status = 'stopped'",
		"status_source = 'runtime'",
		"UPDATE account_channel_memberships",
		"UPDATE outgoing_message_jobs",
		"UPDATE scheduled_dm_tasks",
		"UPDATE scheduled_dm_deliveries",
		"UPDATE analytics_scheduler_settings",
		"PRAGMA foreign_key_check",
		"PRAGMA integrity_check",
	} {
		if !strings.Contains(sql, required) {
			t.Errorf("sanitize-seeded-state.sql does not contain %q", required)
		}
	}

	for _, forbidden := range []string{
		"tdata-" + "import",
		"tdata_" + "import_items",
		"UPDATE tdata_import_items",
		"FROM tdata_import_items",
	} {
		if strings.Contains(script+"\n"+sql, forbidden) {
			t.Errorf("seed preparation retains local-import dependency %q", forbidden)
		}
	}
}

func releaseScriptDirectory(t *testing.T) string {
	t.Helper()
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate release script directory")
	}
	return filepath.Dir(current)
}

func readContractFile(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(contents)
}
