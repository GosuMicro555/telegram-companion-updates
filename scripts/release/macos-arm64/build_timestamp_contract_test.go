package macosarm64_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDualBuildUsesOneTimestampForMasterAndPublic(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	lib := readBuildTimestampContractFile(t, filepath.Join(root, "scripts", "release", "macos-arm64", "lib.sh"))
	dual := readBuildTimestampContractFile(t, filepath.Join(root, "scripts", "release", "macos-arm64", "build-dual-local.sh"))
	stage := readBuildTimestampContractFile(t, filepath.Join(root, "scripts", "release", "macos-arm64", "build-stage.sh"))

	for _, fragment := range []string{
		"ensure_build_timestamp()",
		`VITE_BUILD_TIMESTAMP="$(date -u +%Y-%m-%dT%H:%M:%SZ)"`,
		"export VITE_BUILD_TIMESTAMP",
	} {
		if !strings.Contains(lib, fragment) {
			t.Fatalf("lib.sh does not contain %q", fragment)
		}
	}
	if strings.Count(dual, "ensure_build_timestamp") != 1 {
		t.Fatalf("dual build must establish the timestamp exactly once")
	}
	if !strings.Contains(stage, "ensure_build_timestamp") {
		t.Fatalf("standalone public build must establish or preserve the timestamp")
	}
}

func readBuildTimestampContractFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}
