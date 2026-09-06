package main

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"telegram-companion/internal/bootstrapstate"

	bolt "go.etcd.io/bbolt"
	_ "modernc.org/sqlite"
)

const (
	testSeedID  = "public-seed-test"
	testVersion = "0.8.2"
)

var testSeedKey = []byte("0123456789abcdef0123456789abcdef")

func TestRunAcceptsOperationalBundleWithoutDisclosingDetails(t *testing.T) {
	bundlePath, keyPath := packTestBundle(t, bundleFixture{
		accountRows:         1,
		outboundChannelRows: 1,
		activeKeywordRows:   1,
		liveHistoryRows:     1,
		files: map[string][]byte{
			"application-state.bolt":        []byte("non-empty-state"),
			"sessions/account/session.json": []byte(`{"session":"present"}`),
		},
		secrets: completeOperationalSecrets(),
	})
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := run(context.Background(), verifierArgs(bundlePath, keyPath), &stdout, &stderr)
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if got, want := stdout.String(), "public bootstrap contents verified\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	assertNoSensitiveOutput(t, stdout.String(), stderr.String())
}

func TestRunRequiresExactLengthForEveryOperationalSecretWithoutLeakingMaterial(t *testing.T) {
	const genericError = "verify public bootstrap contents: operational credentials are incomplete"
	secretNames := []string{
		"scout-message-key",
		"outbound-target-key",
		"proxy-credentials-v1",
		"telegram-account-credentials-v1",
	}
	lengths := []int{31, 32, 33}

	for secretIndex, secretName := range secretNames {
		for _, length := range lengths {
			t.Run(fmt.Sprintf("%s/%d_bytes", secretName, length), func(t *testing.T) {
				fixture := completeOperationalFixture()
				material := bytes.Repeat([]byte{byte('A' + secretIndex)}, length)
				fixture.secrets[secretName] = material
				bundlePath, keyPath := packTestBundle(t, fixture)
				var stdout bytes.Buffer
				var stderr bytes.Buffer

				err := run(context.Background(), verifierArgs(bundlePath, keyPath), &stdout, &stderr)
				if length == 32 {
					if err != nil {
						t.Fatalf("run() error = %v, want success", err)
					}
					if got, want := stdout.String(), "public bootstrap contents verified\n"; got != want {
						t.Fatalf("stdout = %q, want %q", got, want)
					}
				} else if err == nil || err.Error() != genericError {
					t.Fatalf("run() error = %v, want %q", err, genericError)
				}
				if stderr.Len() != 0 {
					t.Fatalf("stderr = %q, want empty", stderr.String())
				}
				errorText := ""
				if err != nil {
					errorText = err.Error()
				}
				assertMaterialNotOutput(t, material, stdout.String(), stderr.String(), errorText)
			})
		}
	}
}

func TestRunRejectsAccountRowWithoutTelegramArtifact(t *testing.T) {
	bundlePath, keyPath := packTestBundle(t, bundleFixture{
		accountRows:         1,
		outboundChannelRows: 1,
		activeKeywordRows:   1,
		liveHistoryRows:     1,
		files: map[string][]byte{
			"application-state.bolt": []byte("non-empty-state"),
		},
		secrets: completeOperationalSecrets(),
	})

	err := run(context.Background(), verifierArgs(bundlePath, keyPath), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "Telegram operational artifact") {
		t.Fatalf("run() error = %v, want Telegram artifact rejection", err)
	}
}

func TestRunRejectsTelegramArtifactWithoutAccountRow(t *testing.T) {
	bundlePath, keyPath := packTestBundle(t, bundleFixture{
		outboundChannelRows: 1,
		activeKeywordRows:   1,
		liveHistoryRows:     1,
		files: map[string][]byte{
			"application-state.bolt":        []byte("non-empty-state"),
			"sessions/account/session.json": []byte("session"),
		},
		secrets: completeOperationalSecrets(),
	})

	err := run(context.Background(), verifierArgs(bundlePath, keyPath), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "account data") {
		t.Fatalf("run() error = %v, want account data rejection", err)
	}
}

func TestRunRejectsBundleWithoutRequiredOperationalRows(t *testing.T) {
	tests := []struct {
		name            string
		mutate          func(*bundleFixture)
		expectedMessage string
	}{
		{name: "channels", mutate: func(fixture *bundleFixture) { fixture.outboundChannelRows = 0 }, expectedMessage: "channel data"},
		{name: "active keywords", mutate: func(fixture *bundleFixture) { fixture.activeKeywordRows = 0 }, expectedMessage: "active keyword data"},
		{name: "live statistics", mutate: func(fixture *bundleFixture) { fixture.liveHistoryRows = 0 }, expectedMessage: "Live-statistics data"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := completeOperationalFixture()
			test.mutate(&fixture)
			bundlePath, keyPath := packTestBundle(t, fixture)

			err := run(context.Background(), verifierArgs(bundlePath, keyPath), &bytes.Buffer{}, &bytes.Buffer{})
			if err == nil || !strings.Contains(err.Error(), test.expectedMessage) {
				t.Fatalf("run() error = %v, want %q rejection", err, test.expectedMessage)
			}
		})
	}
}

func TestRunRejectsEmptyApplicationDatabase(t *testing.T) {
	bundlePath, keyPath := packTestBundle(t, bundleFixture{
		emptyDatabase: true,
		files: map[string][]byte{
			"application-state.bolt": []byte("non-empty-state"),
			"tdata/account/map0":     []byte("telegram-session"),
		},
		secrets: completeOperationalSecrets(),
	})

	err := run(context.Background(), verifierArgs(bundlePath, keyPath), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "application database") {
		t.Fatalf("run() error = %v, want application database rejection", err)
	}
}

func TestRunRejectsInvalidApplicationDatabase(t *testing.T) {
	bundlePath, keyPath := packTestBundle(t, bundleFixture{
		invalidDatabase: true,
		files: map[string][]byte{
			"application-state.bolt": []byte("non-empty-state"),
			"tdata/account/map0":     []byte("telegram-session"),
		},
		secrets: completeOperationalSecrets(),
	})

	err := run(context.Background(), verifierArgs(bundlePath, keyPath), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "application database") {
		t.Fatalf("run() error = %v, want invalid database rejection", err)
	}
}

func TestRunRejectsApplicationDatabaseWithoutApplicationTables(t *testing.T) {
	bundlePath, keyPath := packTestBundle(t, bundleFixture{
		emptyDatabaseSchema: true,
		files: map[string][]byte{
			"application-state.bolt":        []byte("non-empty-state"),
			"sessions/account/session.json": []byte("telegram-session"),
		},
		secrets: completeOperationalSecrets(),
	})

	err := run(context.Background(), verifierArgs(bundlePath, keyPath), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "application database") {
		t.Fatalf("run() error = %v, want empty application schema rejection", err)
	}
}

func TestRunRejectsMissingOrEmptyApplicationState(t *testing.T) {
	for _, test := range []struct {
		name  string
		files map[string][]byte
	}{
		{name: "missing", files: map[string][]byte{"sessions/account/session.json": []byte("session")}},
		{name: "empty", files: map[string][]byte{
			"application-state.bolt":        nil,
			"sessions/account/session.json": []byte("session"),
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			bundlePath, keyPath := packTestBundle(t, bundleFixture{
				files:   test.files,
				secrets: completeOperationalSecrets(),
			})

			err := run(context.Background(), verifierArgs(bundlePath, keyPath), &bytes.Buffer{}, &bytes.Buffer{})
			if err == nil || !strings.Contains(err.Error(), "application state") {
				t.Fatalf("run() error = %v, want application state rejection", err)
			}
		})
	}
}

func TestRunRejectsInvalidApplicationState(t *testing.T) {
	bundlePath, keyPath := packTestBundle(t, bundleFixture{
		invalidStateDatabase: true,
		files: map[string][]byte{
			"application-state.bolt":        []byte("not-a-bolt-database"),
			"sessions/account/session.json": []byte("session"),
		},
		secrets: completeOperationalSecrets(),
	})

	err := run(context.Background(), verifierArgs(bundlePath, keyPath), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "application state") {
		t.Fatalf("run() error = %v, want invalid application state rejection", err)
	}
}

func TestRunRejectsBundleWithoutTelegramArtifact(t *testing.T) {
	bundlePath, keyPath := packTestBundle(t, bundleFixture{
		accountRows:         1,
		outboundChannelRows: 1,
		activeKeywordRows:   1,
		liveHistoryRows:     1,
		files: map[string][]byte{
			"application-state.bolt": []byte("non-empty-state"),
		},
		secrets: completeOperationalSecrets(),
	})

	err := run(context.Background(), verifierArgs(bundlePath, keyPath), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "Telegram operational artifact") {
		t.Fatalf("run() error = %v, want Telegram artifact rejection", err)
	}
}

func TestRunRejectsMissingOperationalSecretWithoutLeakingSecretValues(t *testing.T) {
	secrets := completeOperationalSecrets()
	delete(secrets, "outbound-target-key")
	fixture := completeOperationalFixture()
	fixture.secrets = secrets
	bundlePath, keyPath := packTestBundle(t, fixture)
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := run(context.Background(), verifierArgs(bundlePath, keyPath), &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "operational credentials") {
		t.Fatalf("run() error = %v, want operational credential rejection", err)
	}
	assertNoSensitiveOutput(t, stdout.String(), stderr.String(), err.Error())
}

func TestRunRejectsForbiddenSecretWithoutLeakingSecretValues(t *testing.T) {
	for _, name := range []string{
		"backup-recovery-key-v1",
	} {
		t.Run(name, func(t *testing.T) {
			fixture := completeOperationalFixture()
			fixture.secrets[name] = []byte("sensitive-forbidden-material-001")
			bundlePath, keyPath := packTestBundle(t, fixture)
			var stdout bytes.Buffer
			var stderr bytes.Buffer

			err := run(context.Background(), verifierArgs(bundlePath, keyPath), &stdout, &stderr)
			if err == nil || !strings.Contains(err.Error(), "forbidden credentials") {
				t.Fatalf("run() error = %v, want forbidden credential rejection", err)
			}
			assertNoSensitiveOutput(t, stdout.String(), stderr.String(), err.Error())
		})
	}
}

func TestRunRejectsWrongKeyWithoutLeavingPlaintextOrLeakingSecrets(t *testing.T) {
	bundlePath, _ := packTestBundle(t, bundleFixture{
		files: map[string][]byte{
			"application-state.bolt":        []byte("state-secret-marker"),
			"sessions/account/session.json": []byte("session-secret-marker"),
		},
		secrets: completeOperationalSecrets(),
	})
	wrongKeyPath := filepath.Join(t.TempDir(), "wrong.key")
	if err := os.WriteFile(wrongKeyPath, []byte("abcdef0123456789abcdef0123456789"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := run(context.Background(), verifierArgs(bundlePath, wrongKeyPath), &stdout, &stderr)
	if err == nil {
		t.Fatal("run() error = nil, want authentication failure")
	}
	assertNoSensitiveOutput(t, stdout.String(), stderr.String(), err.Error())
}

type bundleFixture struct {
	accountRows          int
	outboundChannelRows  int
	activeKeywordRows    int
	liveHistoryRows      int
	emptyDatabase        bool
	invalidDatabase      bool
	emptyDatabaseSchema  bool
	invalidStateDatabase bool
	files                map[string][]byte
	secrets              map[string][]byte
}

func packTestBundle(t *testing.T, fixture bundleFixture) (string, string) {
	t.Helper()
	sourceData := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(sourceData, 0o700); err != nil {
		t.Fatal(err)
	}
	databasePath := filepath.Join(sourceData, "app.db")
	switch {
	case fixture.emptyDatabase:
		if err := os.WriteFile(databasePath, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	case fixture.invalidDatabase:
		if err := os.WriteFile(databasePath, []byte("not-a-sqlite-database"), 0o600); err != nil {
			t.Fatal(err)
		}
	case fixture.emptyDatabaseSchema:
		writeEmptyTestDatabase(t, databasePath)
	default:
		writeTestDatabase(t, databasePath, fixture)
	}
	for name, data := range fixture.files {
		path := filepath.Join(sourceData, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if name == "application-state.bolt" && len(data) > 0 && !fixture.invalidStateDatabase {
			writeTestStateDatabase(t, path, data)
			continue
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	bundlePath := filepath.Join(t.TempDir(), "state.tcs")
	if err := bootstrapstate.Pack(context.Background(), bootstrapstate.PackConfig{
		SourceData: sourceData,
		OutputPath: bundlePath,
		BundleID:   testSeedID,
		AppVersion: testVersion,
		Key:        append([]byte(nil), testSeedKey...),
		Secrets:    testSecretReader{values: fixture.secrets},
	}); err != nil {
		t.Fatalf("Pack() error = %v", err)
	}
	keyPath := filepath.Join(t.TempDir(), "seed.key")
	if err := os.WriteFile(keyPath, testSeedKey, 0o600); err != nil {
		t.Fatal(err)
	}
	return bundlePath, keyPath
}

func writeTestStateDatabase(t *testing.T, path string, value []byte) {
	t.Helper()
	db, err := bolt.Open(path, 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists([]byte("settings"))
		if err != nil {
			return err
		}
		return bucket.Put([]byte("fixture"), value)
	}); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func writeEmptyTestDatabase(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE placeholder (id INTEGER); DROP TABLE placeholder`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func writeTestDatabase(t *testing.T, path string, fixture bundleFixture) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		CREATE TABLE accounts (id TEXT PRIMARY KEY);
		CREATE TABLE outbound_channels (id TEXT PRIMARY KEY);
		CREATE TABLE canonical_keywords (id TEXT PRIMARY KEY, trigger_active INTEGER NOT NULL);
		CREATE TABLE live_delivery_history (id TEXT PRIMARY KEY);
	`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	for index := 0; index < fixture.accountRows; index++ {
		if _, err := db.Exec(`INSERT INTO accounts(id) VALUES (?)`, "account-"+string(rune('a'+index))); err != nil {
			_ = db.Close()
			t.Fatal(err)
		}
	}
	for index := 0; index < fixture.outboundChannelRows; index++ {
		if _, err := db.Exec(`INSERT INTO outbound_channels(id) VALUES (?)`, "channel-"+string(rune('a'+index))); err != nil {
			_ = db.Close()
			t.Fatal(err)
		}
	}
	for index := 0; index < fixture.activeKeywordRows; index++ {
		if _, err := db.Exec(`INSERT INTO canonical_keywords(id,trigger_active) VALUES (?,1)`, "keyword-"+string(rune('a'+index))); err != nil {
			_ = db.Close()
			t.Fatal(err)
		}
	}
	for index := 0; index < fixture.liveHistoryRows; index++ {
		if _, err := db.Exec(`INSERT INTO live_delivery_history(id) VALUES (?)`, "history-"+string(rune('a'+index))); err != nil {
			_ = db.Close()
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func completeOperationalFixture() bundleFixture {
	return bundleFixture{
		accountRows:         1,
		outboundChannelRows: 1,
		activeKeywordRows:   1,
		liveHistoryRows:     1,
		files: map[string][]byte{
			"application-state.bolt":        []byte("non-empty-state"),
			"sessions/account/session.json": []byte("session"),
		},
		secrets: completeOperationalSecrets(),
	}
}

type testSecretReader struct {
	values map[string][]byte
}

func (reader testSecretReader) Get(_ context.Context, name string) ([]byte, error) {
	value, ok := reader.values[name]
	if !ok {
		return nil, bootstrapstate.ErrSecretNotFound
	}
	return append([]byte(nil), value...), nil
}

func completeOperationalSecrets() map[string][]byte {
	return map[string][]byte{
		"scout-message-key":               []byte("sensitive-scout-material-000001x"),
		"outbound-target-key":             []byte("sensitive-outbound-material-001x"),
		"proxy-credentials-v1":            []byte("sensitive-proxy-material-000001x"),
		"telegram-account-credentials-v1": []byte("sensitive-account-material-0001x"),
	}
}

func verifierArgs(bundlePath, keyPath string) []string {
	return []string{
		"-bundle", bundlePath,
		"-seed-id", testSeedID,
		"-version", testVersion,
		"-seed-key-file", keyPath,
	}
}

func assertNoSensitiveOutput(t *testing.T, values ...string) {
	t.Helper()
	sensitive := []string{
		"sensitive-scout-material-000001x",
		"sensitive-outbound-material-001x",
		"sensitive-proxy-material-000001x",
		"sensitive-account-material-0001x",
		"sensitive-master-material-000001",
		"state-secret-marker",
		"session-secret-marker",
	}
	for _, value := range values {
		for _, secret := range sensitive {
			if strings.Contains(value, secret) {
				t.Fatalf("sensitive material was written to output")
			}
		}
	}
}

func assertMaterialNotOutput(t *testing.T, material []byte, values ...string) {
	t.Helper()
	for _, value := range values {
		if strings.Contains(value, string(material)) {
			t.Fatalf("sensitive material was written to output")
		}
	}
}
