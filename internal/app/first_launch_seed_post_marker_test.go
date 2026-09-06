package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestImportFirstLaunchSeedPostMarkerFailureRestoresExactPristineTree(t *testing.T) {
	targetRoot := t.TempDir()
	targetData := filepath.Join(targetRoot, "data")
	databasePath := filepath.Join(targetData, "app.db")
	writePristineGeneratedDatabase(t, databasePath)
	dataInfoBefore, err := os.Lstat(targetData)
	if err != nil {
		t.Fatal(err)
	}
	databaseInfoBefore, err := os.Lstat(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	databaseBefore, err := os.ReadFile(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	lateWriter, err := os.OpenFile(databasePath, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lateWriter.Close() })
	bundlePath := packFirstLaunchSeedWithSecrets(t, map[string]string{
		"app.db": "seed-database",
	}, completeFirstLaunchOperationalSecretsForTest())
	oldSecrets := completeFirstLaunchOperationalSecretsForTest()
	oldSecrets["unrelated-secret"] = []byte("unrelated-value")
	writer := &firstLaunchTestSecretWriter{values: cloneFirstLaunchSecretsForTest(oldSecrets)}

	lateBytes := []byte("late-before-postcheck")
	foreignPath := filepath.Join(targetRoot, "unknown-after-marker")
	previousHook := afterFirstLaunchMarkerPublished
	t.Cleanup(func() { afterFirstLaunchMarkerPublished = previousHook })
	var foreignInfo os.FileInfo
	afterFirstLaunchMarkerPublished = func() {
		if _, err := lateWriter.Write(lateBytes); err != nil {
			t.Fatal(err)
		}
		writeSeedFile(t, foreignPath, "foreign-root-state")
		var err error
		foreignInfo, err = os.Lstat(foreignPath)
		if err != nil {
			t.Fatal(err)
		}
	}

	imported, err := ImportFirstLaunchSeed(context.Background(), FirstLaunchSeedConfig{
		BundlePath: bundlePath, TargetRoot: targetRoot, AppVersion: "0.8.2",
		License: "recipient-license", Secrets: writer,
	})

	if imported || !errors.Is(err, ErrFirstLaunchSeedTargetChanged) {
		t.Fatalf("ImportFirstLaunchSeed() = %v, %v, want false, ErrFirstLaunchSeedTargetChanged", imported, err)
	}
	dataInfoAfter, err := os.Lstat(targetData)
	if err != nil {
		t.Fatal(err)
	}
	databaseInfoAfter, err := os.Lstat(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(dataInfoBefore, dataInfoAfter) || !os.SameFile(databaseInfoBefore, databaseInfoAfter) {
		t.Fatal("post-marker rollback did not restore exact pristine tree identities")
	}
	databaseAfter, err := os.ReadFile(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if want := append(append([]byte(nil), databaseBefore...), lateBytes...); !bytes.Equal(databaseAfter, want) {
		t.Fatal("post-marker rollback lost a late write through the displaced pristine descriptor")
	}
	foreignInfoAfter, err := os.Lstat(foreignPath)
	if err != nil {
		t.Fatal(err)
	}
	if foreignInfo == nil || !os.SameFile(foreignInfo, foreignInfoAfter) {
		t.Fatal("post-marker rollback changed the foreign root entry")
	}
	if _, statErr := os.Lstat(filepath.Join(targetRoot, "bootstrap-state")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("marker remains after pristine rollback: %v", statErr)
	}
	quarantine := firstLaunchQuarantineRootForTest(t, targetRoot)
	assertSeedFile(t, filepath.Join(quarantine, "data", "app.db"), "seed-database")
	if _, statErr := os.Lstat(filepath.Join(quarantine, "import", "bootstrap-state", "imported.json")); statErr != nil {
		t.Fatalf("marker was not quarantined after pristine rollback: %v", statErr)
	}
	assertFirstLaunchSecretsUnchangedForTest(t, writer, oldSecrets)
}

func TestImportFirstLaunchSeedRollsBackUnsafeRootStateInsertedAfterMarker(t *testing.T) {
	tests := []struct {
		name            string
		prepare         func(*testing.T, string)
		foreignRelative string
	}{
		{
			name:            "unknown root entry",
			foreignRelative: "unknown-after-marker",
		},
		{
			name: "unknown runtime service child",
			prepare: func(t *testing.T, root string) {
				writeSeedFile(t, filepath.Join(root, "logs", "desktop.log"), "runtime-log")
			},
			foreignRelative: filepath.Join("logs", "unexpected.log"),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			targetRoot := t.TempDir()
			if test.prepare != nil {
				test.prepare(t, targetRoot)
			}
			bundlePath := packFirstLaunchSeedWithSecrets(t, map[string]string{
				"app.db": "seed-database",
			}, completeFirstLaunchOperationalSecretsForTest())
			oldSecrets := completeFirstLaunchOperationalSecretsWithOverridesForTest(map[string][]byte{
				"outbound-target-key":             []byte("old-outbound"),
				"proxy-credentials-v1":            []byte("old-proxy"),
				"scout-message-key":               []byte("old-scout"),
				"telegram-account-credentials-v1": []byte("old-account"),
			})
			oldSecrets["unrelated-secret"] = []byte("unrelated-value")
			writer := &firstLaunchTestSecretWriter{values: cloneFirstLaunchSecretsForTest(oldSecrets)}

			previousHook := afterFirstLaunchMarkerPublished
			t.Cleanup(func() { afterFirstLaunchMarkerPublished = previousHook })
			foreignPath := filepath.Join(targetRoot, test.foreignRelative)
			var foreignInfo os.FileInfo
			afterFirstLaunchMarkerPublished = func() {
				assertSeedFile(t, filepath.Join(targetRoot, "data", "app.db"), "seed-database")
				if _, err := os.Lstat(filepath.Join(targetRoot, "bootstrap-state", "imported.json")); err != nil {
					t.Fatalf("marker was not published before post-marker hook: %v", err)
				}
				writeSeedFile(t, foreignPath, "foreign-runtime-state")
				var err error
				foreignInfo, err = os.Lstat(foreignPath)
				if err != nil {
					t.Fatal(err)
				}
			}

			imported, err := ImportFirstLaunchSeed(context.Background(), FirstLaunchSeedConfig{
				BundlePath: bundlePath, TargetRoot: targetRoot, AppVersion: "0.8.2",
				License: "recipient-license", Secrets: writer,
			})

			if imported || !errors.Is(err, ErrFirstLaunchSeedTargetChanged) {
				t.Fatalf("ImportFirstLaunchSeed() = %v, %v, want false, ErrFirstLaunchSeedTargetChanged", imported, err)
			}
			if foreignInfo == nil {
				t.Fatal("post-marker hook was not called")
			}
			foreignInfoAfter, err := os.Lstat(foreignPath)
			if err != nil {
				t.Fatal(err)
			}
			if !os.SameFile(foreignInfo, foreignInfoAfter) {
				t.Fatal("foreign root/service entry identity changed during rollback")
			}
			assertSeedFile(t, foreignPath, "foreign-runtime-state")
			if _, statErr := os.Lstat(filepath.Join(targetRoot, "data")); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("published data remains in unsafe target: %v", statErr)
			}
			if _, statErr := os.Lstat(filepath.Join(targetRoot, "bootstrap-state")); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("published marker remains in unsafe target: %v", statErr)
			}
			if test.foreignRelative == filepath.Join("logs", "unexpected.log") {
				assertSeedFile(t, filepath.Join(targetRoot, "logs", "desktop.log"), "runtime-log")
			}
			quarantine := firstLaunchQuarantineRootForTest(t, targetRoot)
			assertSeedFile(t, filepath.Join(quarantine, "data", "app.db"), "seed-database")
			if _, statErr := os.Lstat(filepath.Join(quarantine, "import", "bootstrap-state", "imported.json")); statErr != nil {
				t.Fatalf("published marker was not quarantined: %v", statErr)
			}
			assertFirstLaunchSecretsUnchangedForTest(t, writer, oldSecrets)
		})
	}
}

func TestImportFirstLaunchSeedPostMarkerValidationAllowsRuntimeLogAppend(t *testing.T) {
	targetRoot := t.TempDir()
	logPath := filepath.Join(targetRoot, "logs", "desktop.log")
	writeSeedFile(t, logPath, "before")
	bundlePath := packFirstLaunchSeedWithSecrets(t, map[string]string{
		"app.db": "seed-database",
	}, completeFirstLaunchOperationalSecretsForTest())

	previousHook := afterFirstLaunchMarkerPublished
	t.Cleanup(func() { afterFirstLaunchMarkerPublished = previousHook })
	afterFirstLaunchMarkerPublished = func() {
		file, err := os.OpenFile(logPath, os.O_WRONLY|os.O_APPEND, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.WriteString("-during-import"); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	}

	imported, err := ImportFirstLaunchSeed(context.Background(), FirstLaunchSeedConfig{
		BundlePath: bundlePath, TargetRoot: targetRoot, AppVersion: "0.8.2",
		License: "recipient-license", Secrets: &firstLaunchTestSecretWriter{},
	})

	if err != nil || !imported {
		t.Fatalf("ImportFirstLaunchSeed() = %v, %v, want true, nil", imported, err)
	}
	assertSeedFile(t, logPath, "before-during-import")
	assertSeedFile(t, filepath.Join(targetRoot, "data", "app.db"), "seed-database")
	if _, err := os.Lstat(filepath.Join(targetRoot, "bootstrap-state", "imported.json")); err != nil {
		t.Fatalf("marker missing after allowed runtime append: %v", err)
	}
	if quarantines := firstLaunchQuarantineRootsForTest(t, targetRoot); len(quarantines) != 0 {
		t.Fatalf("allowed absent-data import retained quarantine roots: %v", quarantines)
	}
}

func TestImportFirstLaunchSeedRollsBackOwnedTreeMutationAfterMarker(t *testing.T) {
	tests := []struct {
		name               string
		mutate             func(*testing.T, string) (os.FileInfo, os.FileInfo)
		quarantineRelative string
	}{
		{
			name: "published data changed",
			mutate: func(t *testing.T, root string) (os.FileInfo, os.FileInfo) {
				path := filepath.Join(root, "data", "sessions", "foreign.session")
				writeSeedFile(t, path, "foreign-data")
				info, err := os.Lstat(path)
				if err != nil {
					t.Fatal(err)
				}
				return info, nil
			},
			quarantineRelative: filepath.Join("data", "sessions", "foreign.session"),
		},
		{
			name: "published marker file replaced by identical inode",
			mutate: func(t *testing.T, root string) (os.FileInfo, os.FileInfo) {
				path := filepath.Join(root, "bootstrap-state", "imported.json")
				contents, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				originalInfo, err := os.Lstat(path)
				if err != nil {
					t.Fatal(err)
				}
				preservedOriginal := filepath.Join(t.TempDir(), "original-imported.json")
				if err := os.Rename(path, preservedOriginal); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, contents, originalInfo.Mode().Perm()); err != nil {
					t.Fatal(err)
				}
				foreignInfo, err := os.Lstat(path)
				if err != nil {
					t.Fatal(err)
				}
				preservedInfo, err := os.Lstat(preservedOriginal)
				if err != nil {
					t.Fatal(err)
				}
				if !os.SameFile(originalInfo, preservedInfo) {
					t.Fatal("test did not preserve the original marker inode")
				}
				return foreignInfo, preservedInfo
			},
			quarantineRelative: filepath.Join("import", "bootstrap-state", "imported.json"),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			targetRoot := t.TempDir()
			bundlePath := packFirstLaunchSeedWithSecrets(t, map[string]string{
				"app.db": "seed-database",
			}, completeFirstLaunchOperationalSecretsForTest())
			oldSecrets := completeFirstLaunchOperationalSecretsForTest()
			oldSecrets["unrelated-secret"] = []byte("unrelated-value")
			writer := &firstLaunchTestSecretWriter{values: cloneFirstLaunchSecretsForTest(oldSecrets)}

			previousHook := afterFirstLaunchMarkerPublished
			t.Cleanup(func() { afterFirstLaunchMarkerPublished = previousHook })
			var foreignInfo os.FileInfo
			var preservedOriginalInfo os.FileInfo
			afterFirstLaunchMarkerPublished = func() {
				foreignInfo, preservedOriginalInfo = test.mutate(t, targetRoot)
			}

			imported, err := ImportFirstLaunchSeed(context.Background(), FirstLaunchSeedConfig{
				BundlePath: bundlePath, TargetRoot: targetRoot, AppVersion: "0.8.2",
				License: "recipient-license", Secrets: writer,
			})

			if imported || !errors.Is(err, ErrFirstLaunchSeedTargetChanged) {
				t.Fatalf("ImportFirstLaunchSeed() = %v, %v, want false, ErrFirstLaunchSeedTargetChanged", imported, err)
			}
			if foreignInfo == nil {
				t.Fatal("post-marker mutation hook was not called")
			}
			if _, statErr := os.Lstat(filepath.Join(targetRoot, "data")); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("mutated published data remains in target: %v", statErr)
			}
			if _, statErr := os.Lstat(filepath.Join(targetRoot, "bootstrap-state")); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("mutated published marker remains in target: %v", statErr)
			}
			quarantine := firstLaunchQuarantineRootForTest(t, targetRoot)
			quarantinedPath := filepath.Join(quarantine, test.quarantineRelative)
			quarantinedInfo, statErr := os.Lstat(quarantinedPath)
			if statErr != nil {
				t.Fatalf("mutated published inode was not quarantined: %v", statErr)
			}
			if !os.SameFile(foreignInfo, quarantinedInfo) {
				t.Fatal("post-marker rollback did not preserve the exact foreign inode")
			}
			if preservedOriginalInfo != nil && os.SameFile(preservedOriginalInfo, quarantinedInfo) {
				t.Fatal("post-marker rollback confused the preserved original marker with its replacement")
			}
			assertSeedFile(t, filepath.Join(quarantine, "data", "app.db"), "seed-database")
			if _, statErr := os.Lstat(filepath.Join(quarantine, "import", "bootstrap-state", "imported.json")); statErr != nil {
				t.Fatalf("marker missing from quarantine: %v", statErr)
			}
			assertFirstLaunchSecretsUnchangedForTest(t, writer, oldSecrets)
		})
	}
}
