//go:build darwin

package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestImportFirstLaunchSeedUsesDarwinNoReplaceAtTheRaceBoundary(t *testing.T) {
	targetRoot := t.TempDir()
	atomicTargetRoot, err := canonicalFirstLaunchTargetRoot(targetRoot)
	if err != nil {
		t.Fatal(err)
	}
	targetData := filepath.Join(atomicTargetRoot, "data")
	bundlePath := packFirstLaunchSeedWithSecrets(t, map[string]string{
		"app.db": "seed-database",
	}, completeFirstLaunchOperationalSecretsForTest())
	previousHook := afterFirstLaunchTargetObserved
	previousRenameatx := firstLaunchRenameatxNp
	t.Cleanup(func() {
		afterFirstLaunchTargetObserved = previousHook
		firstLaunchRenameatxNp = previousRenameatx
	})
	observed := false
	afterFirstLaunchTargetObserved = func() { observed = true }
	var gotFlags uint32
	firstLaunchRenameatxNp = func(fromFD int, source string, toFD int, destination string, flags uint32) error {
		if destination == targetData {
			if !observed {
				t.Fatal("data transition reached syscall before final-observation hook")
			}
			gotFlags = flags
			writeSeedFile(t, filepath.Join(targetData, "sessions", "syscall-race.session"), "foreign-data")
		}
		return previousRenameatx(fromFD, source, toFD, destination, flags)
	}

	imported, err := ImportFirstLaunchSeed(context.Background(), FirstLaunchSeedConfig{
		BundlePath: bundlePath, TargetRoot: targetRoot, AppVersion: "0.8.2",
		License: "recipient-license", Secrets: &firstLaunchTestSecretWriter{},
	})

	if imported || !errors.Is(err, ErrFirstLaunchSeedTargetChanged) {
		t.Fatalf("ImportFirstLaunchSeed() = %v, %v, want false, ErrFirstLaunchSeedTargetChanged", imported, err)
	}
	wantFlags := uint32(unix.RENAME_EXCL | unix.RENAME_NOFOLLOW_ANY)
	if gotFlags != wantFlags {
		t.Fatalf("RenameatxNp flags = %#x, want RENAME_EXCL|RENAME_NOFOLLOW_ANY (%#x)", gotFlags, wantFlags)
	}
	assertSeedFile(t, filepath.Join(targetData, "sessions", "syscall-race.session"), "foreign-data")
	if _, statErr := os.Lstat(filepath.Join(targetRoot, "bootstrap-state")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("marker exists after no-replace conflict: %v", statErr)
	}
}

func TestFirstLaunchExchangeUsesDarwinSwapAndNoFollow(t *testing.T) {
	sentinel := errors.New("renameatx probe")
	previousRenameatx := firstLaunchRenameatxNp
	t.Cleanup(func() { firstLaunchRenameatxNp = previousRenameatx })
	var gotFlags uint32
	firstLaunchRenameatxNp = func(_ int, _ string, _ int, _ string, flags uint32) error {
		gotFlags = flags
		return sentinel
	}

	err := exchangeFirstLaunchPaths(filepath.Join(t.TempDir(), "one"), filepath.Join(t.TempDir(), "two"))

	if !errors.Is(err, sentinel) {
		t.Fatalf("exchangeFirstLaunchPaths() error = %v, want sentinel", err)
	}
	wantFlags := uint32(unix.RENAME_SWAP | unix.RENAME_NOFOLLOW_ANY)
	if gotFlags != wantFlags {
		t.Fatalf("RenameatxNp flags = %#x, want RENAME_SWAP|RENAME_NOFOLLOW_ANY (%#x)", gotFlags, wantFlags)
	}
}
