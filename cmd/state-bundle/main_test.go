package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	keyring "github.com/zalando/go-keyring"

	"telegram-companion/internal/bootstrapstate"
)

type secretReaderStub struct {
	value []byte
	err   error
}

func TestRunCreatesTCSEED2BundleFromSeedKeyFile(t *testing.T) {
	root := t.TempDir()
	sourceData := filepath.Join(root, "data")
	if err := os.MkdirAll(sourceData, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceData, "app.db"), []byte("database"), 0o600); err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{0x42}, 32)
	keyPath := filepath.Join(root, "seed.key")
	if err := os.WriteFile(keyPath, key, 0o600); err != nil {
		t.Fatal(err)
	}
	outputPath := filepath.Join(root, "public-bootstrap.tcs")

	args := []string{
		"-source-data", sourceData,
		"-output", outputPath,
		"-seed-id", "public-bootstrap-v1",
		"-version", "1.2.3",
		"-seed-key-file", keyPath,
	}
	for _, name := range bootstrapstate.SecretNames() {
		args = append(args, "-exclude-secret", name)
	}
	var stdout, stderr bytes.Buffer
	if err := run(context.Background(), args, &stdout, &stderr); err != nil {
		t.Fatalf("run() error = %v; stderr = %s", err, stderr.String())
	}
	if strings.Contains(stdout.String(), keyPath) || strings.Contains(stdout.String(), string(key)) {
		t.Fatalf("run() exposed seed material in stdout: %q", stdout.String())
	}
	bundle, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(bundle, []byte("TCSEED2\n")) {
		t.Fatalf("bundle magic = %q, want TCSEED2", bundle[:min(len(bundle), 7)])
	}
}

func TestRunCreatesTCSEED2BundleFromNamedSeedKeyEnvironment(t *testing.T) {
	root := t.TempDir()
	sourceData := filepath.Join(root, "data")
	if err := os.MkdirAll(sourceData, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceData, "app.db"), []byte("database"), 0o600); err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{0x24}, 32)
	t.Setenv("PUBLIC_BOOTSTRAP_SEED_KEY", base64.RawStdEncoding.EncodeToString(key))
	outputPath := filepath.Join(root, "public-bootstrap.tcs")

	args := []string{
		"-source-data", sourceData,
		"-output", outputPath,
		"-seed-id", "public-bootstrap-v1",
		"-version", "1.2.3",
		"-seed-key-env", "PUBLIC_BOOTSTRAP_SEED_KEY",
	}
	for _, name := range bootstrapstate.SecretNames() {
		args = append(args, "-exclude-secret", name)
	}
	if err := run(context.Background(), args, io.Discard, io.Discard); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	bundle, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(bundle, []byte("TCSEED2\n")) {
		t.Fatalf("bundle magic = %q, want TCSEED2", bundle[:min(len(bundle), 7)])
	}
}

func TestRunRejectsSeedKeyFileWithWrongLength(t *testing.T) {
	root := t.TempDir()
	sourceData := filepath.Join(root, "data")
	if err := os.MkdirAll(sourceData, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceData, "app.db"), []byte("database"), 0o600); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(root, "seed.key")
	if err := os.WriteFile(keyPath, bytes.Repeat([]byte{0x01}, 31), 0o600); err != nil {
		t.Fatal(err)
	}

	err := run(context.Background(), []string{
		"-source-data", sourceData,
		"-output", filepath.Join(root, "public-bootstrap.tcs"),
		"-seed-id", "public-bootstrap-v1",
		"-version", "1.2.3",
		"-seed-key-file", keyPath,
	}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "32-byte") {
		t.Fatalf("run() error = %v, want 32-byte seed key rejection", err)
	}
}

func (s secretReaderStub) Get(context.Context, string) ([]byte, error) {
	return s.value, s.err
}

func TestOptionalSecretReaderMapsMissingKeyringSecret(t *testing.T) {
	reader := optionalSecretReader{reader: secretReaderStub{err: keyring.ErrNotFound}}

	_, err := reader.Get(context.Background(), "missing")
	if !errors.Is(err, bootstrapstate.ErrSecretNotFound) {
		t.Fatalf("Get() error = %v, want ErrSecretNotFound", err)
	}
}

func TestOptionalSecretReaderExcludesSecretWithoutReadingKeyring(t *testing.T) {
	reader := optionalSecretReader{
		reader:   secretReaderStub{err: errors.New("must not read keyring")},
		excluded: map[string]struct{}{"backup-recovery-key-v1": {}},
	}

	_, err := reader.Get(context.Background(), "backup-recovery-key-v1")
	if !errors.Is(err, bootstrapstate.ErrSecretNotFound) {
		t.Fatalf("Get() error = %v, want ErrSecretNotFound", err)
	}
}

func TestValidateExcludedSecretsRejectsUnknownName(t *testing.T) {
	if _, err := validateExcludedSecrets([]string{"unknown-operational-secret-v2"}); err == nil {
		t.Fatal("validateExcludedSecrets() error = nil, want error")
	}
}

func TestValidateExcludedSecretsAcceptsKnownNames(t *testing.T) {
	got, err := validateExcludedSecrets([]string{"backup-recovery-key-v1"})
	if err != nil {
		t.Fatalf("validateExcludedSecrets() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(excluded) = %d, want 1", len(got))
	}
}
