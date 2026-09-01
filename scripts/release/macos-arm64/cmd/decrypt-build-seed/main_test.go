package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"telegram-companion/internal/licenseissuer"
)

func TestRunDecryptsBuildSeedIntoPrivateFiles(t *testing.T) {
	const (
		seedID   = "0123456789abcdef0123456789abcdef"
		password = "portable build password"
	)
	seedKey := bytes.Repeat([]byte{0x42}, 32)
	artifact, err := licenseissuer.CreateBootstrapSeedExport(seedID, seedKey, password)
	if err != nil {
		t.Fatalf("CreateBootstrapSeedExport() error = %v", err)
	}

	directory := t.TempDir()
	artifactPath := filepath.Join(directory, "operator.tcompbuildseed")
	passwordPath := filepath.Join(directory, "password.txt")
	seedIDPath := filepath.Join(directory, "seed-id")
	seedKeyPath := filepath.Join(directory, "seed-key")
	if err := os.WriteFile(artifactPath, []byte(artifact+"\n"), 0o600); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
	if err := os.WriteFile(passwordPath, []byte(password+"\n"), 0o600); err != nil {
		t.Fatalf("write password: %v", err)
	}

	if err := run([]string{
		"-artifact", artifactPath,
		"-password-file", passwordPath,
		"-seed-id-output", seedIDPath,
		"-seed-key-output", seedKeyPath,
	}); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	gotID, err := os.ReadFile(seedIDPath)
	if err != nil {
		t.Fatalf("read seed ID: %v", err)
	}
	gotKey, err := os.ReadFile(seedKeyPath)
	if err != nil {
		t.Fatalf("read seed key: %v", err)
	}
	if string(gotID) != seedID || !bytes.Equal(gotKey, seedKey) {
		t.Fatal("decrypted seed grant differs from the exported grant")
	}
	if runtime.GOOS != "windows" {
		for _, path := range []string{seedIDPath, seedKeyPath} {
			info, err := os.Stat(path)
			if err != nil {
				t.Fatalf("stat %s: %v", path, err)
			}
			if info.Mode().Perm() != 0o600 {
				t.Fatalf("%s mode = %o, want 600", path, info.Mode().Perm())
			}
		}
	}
}

func TestRunRejectsWrongPasswordWithoutOutputsOrSecretLeak(t *testing.T) {
	const seedID = "fedcba9876543210fedcba9876543210"
	seedKey := bytes.Repeat([]byte("S"), 32)
	artifact, err := licenseissuer.CreateBootstrapSeedExport(seedID, seedKey, "correct build password")
	if err != nil {
		t.Fatalf("CreateBootstrapSeedExport() error = %v", err)
	}

	directory := t.TempDir()
	artifactPath := filepath.Join(directory, "operator.tcompbuildseed")
	passwordPath := filepath.Join(directory, "password.txt")
	seedIDPath := filepath.Join(directory, "seed-id")
	seedKeyPath := filepath.Join(directory, "seed-key")
	_ = os.WriteFile(artifactPath, []byte(artifact), 0o600)
	_ = os.WriteFile(passwordPath, []byte("wrong build password"), 0o600)

	err = run([]string{
		"-artifact", artifactPath,
		"-password-file", passwordPath,
		"-seed-id-output", seedIDPath,
		"-seed-key-output", seedKeyPath,
	})
	if err == nil {
		t.Fatal("run() accepted a wrong password")
	}
	if strings.Contains(err.Error(), seedID) || strings.Contains(err.Error(), string(seedKey)) {
		t.Fatal("run() error leaked seed material")
	}
	for _, path := range []string{seedIDPath, seedKeyPath} {
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatalf("failed decryption left output %s", path)
		}
	}
}

func TestRunRejectsMalformedArtifact(t *testing.T) {
	directory := t.TempDir()
	artifactPath := filepath.Join(directory, "operator.tcompbuildseed")
	passwordPath := filepath.Join(directory, "password.txt")
	_ = os.WriteFile(artifactPath, []byte("not-a-build-seed"), 0o600)
	_ = os.WriteFile(passwordPath, []byte("portable build password"), 0o600)

	if err := run([]string{
		"-artifact", artifactPath,
		"-password-file", passwordPath,
		"-seed-id-output", filepath.Join(directory, "seed-id"),
		"-seed-key-output", filepath.Join(directory, "seed-key"),
	}); err == nil {
		t.Fatal("run() accepted a malformed artifact")
	}
}
