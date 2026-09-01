//go:build public_macos_arm64 && darwin && arm64 && !ios

package buildinfo

import "testing"

func TestCurrentPublicDefaultsFailClosed(t *testing.T) {
	if _, err := Current(); err == nil {
		t.Fatal("expected public linker defaults to fail closed")
	}
}
