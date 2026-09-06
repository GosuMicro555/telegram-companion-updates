//go:build !public_macos_arm64

package buildinfo

import "testing"

func TestCurrentInternalDefaults(t *testing.T) {
	got, err := Current()
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if got.Channel != ChannelInternal || got.ProductID != ProductID {
		t.Fatalf("unexpected current metadata: %#v", got)
	}
}
