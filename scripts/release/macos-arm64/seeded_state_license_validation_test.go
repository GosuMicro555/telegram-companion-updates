package macosarm64_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"telegram-companion/internal/bootstrapstate"
)

func TestSeededStateLicenseValidatorFailsClosedForDifferentLicense(t *testing.T) {
	tempDir := t.TempDir()
	sourceData := filepath.Join(tempDir, "source-data")
	statePath := filepath.Join(tempDir, "state.tcs")
	validLicensePath := filepath.Join(tempDir, "target-license")
	wrongLicensePath := filepath.Join(tempDir, "other-license")
	validLicense := "target-license-for-test-only"
	wrongLicense := "other-license-for-test-only"

	if err := os.Mkdir(sourceData, 0o700); err != nil {
		t.Fatalf("create source data: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceData, "app.db"), []byte("test database"), 0o600); err != nil {
		t.Fatalf("write source database: %v", err)
	}
	if err := os.WriteFile(validLicensePath, []byte(validLicense), 0o600); err != nil {
		t.Fatalf("write target license: %v", err)
	}
	if err := os.WriteFile(wrongLicensePath, []byte(wrongLicense), 0o600); err != nil {
		t.Fatalf("write other license: %v", err)
	}
	if err := bootstrapstate.Pack(context.Background(), bootstrapstate.PackConfig{
		SourceData: sourceData,
		OutputPath: statePath,
		BundleID:   "seeded-state-license-validator-test",
		AppVersion: "0.8.2",
		License:    validLicense,
		Now: func() time.Time {
			return time.Unix(0, 0)
		},
	}); err != nil {
		t.Fatalf("pack seeded state: %v", err)
	}

	if output, err := runSeededStateLicenseValidator(t, statePath, validLicensePath); err != nil {
		t.Fatalf("validate compatible seeded state: %v\n%s", err, output)
	}

	output, err := runSeededStateLicenseValidator(t, statePath, wrongLicensePath)
	if err == nil {
		t.Fatal("validator accepted seeded state encrypted for a different license")
	}
	if !strings.Contains(output, "seeded state validation failed") {
		t.Fatalf("validator failure did not fail closed: %s", output)
	}
	if strings.Contains(output, validLicense) || strings.Contains(output, wrongLicense) {
		t.Fatal("validator output exposes license contents")
	}
}

func runSeededStateLicenseValidator(t *testing.T, statePath, licensePath string) (string, error) {
	t.Helper()
	releaseRoot := releaseScriptDirectory(t)
	repoRoot := filepath.Clean(filepath.Join(releaseRoot, "..", "..", ".."))
	command := exec.Command(
		"go",
		"run",
		"./cmd/state-bundle-validate",
		"-state", statePath,
		"-license-file", licensePath,
		"-version", "0.8.2",
	)
	command.Dir = repoRoot
	output, err := command.CombinedOutput()
	return string(output), err
}
