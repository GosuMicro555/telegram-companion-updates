//go:build !windows

package licenseissuer

import (
	"errors"
	"testing"
)

func TestStateStoreStubIsExplicitlyUnsupported(t *testing.T) {
	if _, err := DefaultStateRoot(); !errors.Is(err, ErrStoreUnsupported) {
		t.Fatalf("DefaultStateRoot() error = %v, want ErrStoreUnsupported", err)
	}
	if _, err := NewStateStore(t.TempDir()); !errors.Is(err, ErrStoreUnsupported) {
		t.Fatalf("NewStateStore() error = %v, want ErrStoreUnsupported", err)
	}

	var store StateStore
	if _, err := store.Load(); !errors.Is(err, ErrStoreUnsupported) {
		t.Fatalf("Load() error = %v, want ErrStoreUnsupported", err)
	}
	if err := store.Save(GeneratorState{}); !errors.Is(err, ErrStoreUnsupported) {
		t.Fatalf("Save() error = %v, want ErrStoreUnsupported", err)
	}
}
