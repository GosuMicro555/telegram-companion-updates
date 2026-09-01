package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
)

func TestImportFirstLaunchSeedAtomicallyRejectsDataAppearingAfterFinalObservation(t *testing.T) {
	targetRoot := t.TempDir()
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
	var racedSnapshot []string
	previousHook := afterFirstLaunchTargetObserved
	t.Cleanup(func() { afterFirstLaunchTargetObserved = previousHook })
	afterFirstLaunchTargetObserved = func() {
		if _, err := os.Lstat(filepath.Join(targetRoot, "data")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("target data exists before the test race hook: %v", err)
		}
		if _, err := os.Lstat(filepath.Join(targetRoot, "bootstrap-state")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("marker was published before data: %v", err)
		}
		writeSeedFile(t, filepath.Join(targetRoot, "data", "sessions", "concurrent.session"), "concurrent-profile")
		racedSnapshot = snapshotFirstLaunchTree(t, targetRoot)
	}

	imported, err := ImportFirstLaunchSeed(context.Background(), FirstLaunchSeedConfig{
		BundlePath: bundlePath,
		TargetRoot: targetRoot,
		AppVersion: "0.8.2",
		License:    "recipient-license",
		Secrets:    writer,
	})

	if imported {
		t.Fatal("ImportFirstLaunchSeed() replaced data that appeared after final observation")
	}
	if !errors.Is(err, ErrFirstLaunchSeedTargetChanged) {
		t.Fatalf("ImportFirstLaunchSeed() error = %v, want ErrFirstLaunchSeedTargetChanged", err)
	}
	if racedSnapshot == nil {
		t.Fatal("final-observation hook was not called")
	}
	if got := snapshotFirstLaunchTree(t, targetRoot); !reflect.DeepEqual(got, racedSnapshot) {
		t.Fatalf("raced profile changed\nwant: %v\ngot:  %v", racedSnapshot, got)
	}
	if _, statErr := os.Lstat(filepath.Join(targetRoot, "bootstrap-state")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("marker exists after atomic conflict: %v", statErr)
	}
	for name, want := range oldSecrets {
		if got := writer.value(name); !reflect.DeepEqual(got, want) {
			t.Fatalf("secret %s = %q, want rolled back %q", name, got, want)
		}
	}
}

func TestImportFirstLaunchSeedAtomicallyRestoresPristineDataChangedAfterObservation(t *testing.T) {
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
	var racedSnapshot []string
	previousHook := afterFirstLaunchTargetObserved
	previousExchange := exchangeFirstLaunchPaths
	t.Cleanup(func() {
		afterFirstLaunchTargetObserved = previousHook
		exchangeFirstLaunchPaths = previousExchange
	})
	afterFirstLaunchTargetObserved = func() {
		writeSeedFile(t, filepath.Join(targetData, "sessions", "concurrent.session"), "concurrent-profile")
		racedSnapshot = snapshotFirstLaunchTree(t, targetRoot)
	}
	exchangeCalls := 0
	exchangeFirstLaunchPaths = func(left, right string) error {
		exchangeCalls++
		if exchangeCalls == 2 {
			writeSeedFile(t, filepath.Join(targetData, "sessions", "during-rollback.session"), "preserve-in-quarantine")
		}
		return previousExchange(left, right)
	}

	imported, err := ImportFirstLaunchSeed(context.Background(), FirstLaunchSeedConfig{
		BundlePath: bundlePath, TargetRoot: targetRoot, AppVersion: "0.8.2",
		License: "recipient-license", Secrets: writer,
	})

	if imported {
		t.Fatal("ImportFirstLaunchSeed() replaced pristine data changed after final observation")
	}
	if !errors.Is(err, ErrFirstLaunchSeedTargetChanged) {
		t.Fatalf("ImportFirstLaunchSeed() error = %v, want ErrFirstLaunchSeedTargetChanged", err)
	}
	if got := snapshotFirstLaunchTree(t, targetRoot); !reflect.DeepEqual(got, racedSnapshot) {
		t.Fatalf("raced pristine profile changed\nwant: %v\ngot:  %v", racedSnapshot, got)
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
		t.Fatal("atomic rollback did not restore the exact original data/database identities")
	}
	assertFirstLaunchSecretsUnchangedForTest(t, writer, oldSecrets)
	quarantine := firstLaunchQuarantineRootForTest(t, targetRoot)
	assertSeedFile(t, filepath.Join(quarantine, "data", "app.db"), "seed-database")
	assertSeedFile(t, filepath.Join(quarantine, "data", "sessions", "during-rollback.session"), "preserve-in-quarantine")
}

func TestImportFirstLaunchSeedRejectsIdenticalBytesFromReplacedDatabaseInode(t *testing.T) {
	targetRoot := t.TempDir()
	targetData := filepath.Join(targetRoot, "data")
	databasePath := filepath.Join(targetData, "app.db")
	writePristineGeneratedDatabase(t, databasePath)
	bundlePath := packFirstLaunchSeedWithSecrets(t, map[string]string{
		"app.db": "seed-database",
	}, completeFirstLaunchOperationalSecretsForTest())
	oldSecrets := completeFirstLaunchOperationalSecretsForTest()
	oldSecrets["unrelated-secret"] = []byte("unrelated-value")
	writer := &firstLaunchTestSecretWriter{values: cloneFirstLaunchSecretsForTest(oldSecrets)}
	var replacementInfo os.FileInfo
	var racedSnapshot []string
	previousHook := afterFirstLaunchTargetObserved
	t.Cleanup(func() { afterFirstLaunchTargetObserved = previousHook })
	afterFirstLaunchTargetObserved = func() {
		contents, err := os.ReadFile(databasePath)
		if err != nil {
			t.Fatal(err)
		}
		originalInfo, err := os.Lstat(databasePath)
		if err != nil {
			t.Fatal(err)
		}
		replacementPath := filepath.Join(targetData, "app.db.replacement")
		if err := os.WriteFile(replacementPath, contents, originalInfo.Mode().Perm()); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(replacementPath, databasePath); err != nil {
			t.Fatal(err)
		}
		replacementInfo, err = os.Lstat(databasePath)
		if err != nil {
			t.Fatal(err)
		}
		racedSnapshot = snapshotFirstLaunchTree(t, targetRoot)
	}

	imported, err := ImportFirstLaunchSeed(context.Background(), FirstLaunchSeedConfig{
		BundlePath: bundlePath, TargetRoot: targetRoot, AppVersion: "0.8.2",
		License: "recipient-license", Secrets: writer,
	})

	if imported || !errors.Is(err, ErrFirstLaunchSeedTargetChanged) {
		t.Fatalf("ImportFirstLaunchSeed() = %v, %v, want false, ErrFirstLaunchSeedTargetChanged", imported, err)
	}
	if got := snapshotFirstLaunchTree(t, targetRoot); !reflect.DeepEqual(got, racedSnapshot) {
		t.Fatalf("replacement-inode profile changed\nwant: %v\ngot:  %v", racedSnapshot, got)
	}
	finalInfo, err := os.Lstat(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if replacementInfo == nil || !os.SameFile(replacementInfo, finalInfo) {
		t.Fatal("atomic rollback did not restore the concurrently replaced app.db inode")
	}
	assertFirstLaunchSecretsUnchangedForTest(t, writer, oldSecrets)
	quarantine := firstLaunchQuarantineRootForTest(t, targetRoot)
	assertSeedFile(t, filepath.Join(quarantine, "data", "app.db"), "seed-database")
}

func TestImportFirstLaunchSeedAtomicallyReplacesUnchangedPristineData(t *testing.T) {
	targetRoot := t.TempDir()
	targetData := filepath.Join(targetRoot, "data")
	databasePath := filepath.Join(targetData, "app.db")
	writePristineGeneratedDatabase(t, databasePath)
	originalDataInfo, err := os.Lstat(targetData)
	if err != nil {
		t.Fatal(err)
	}
	originalDatabaseInfo, err := os.Lstat(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	originalDatabase, err := os.ReadFile(databasePath)
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

	imported, err := ImportFirstLaunchSeed(context.Background(), FirstLaunchSeedConfig{
		BundlePath: bundlePath, TargetRoot: targetRoot, AppVersion: "0.8.2",
		License: "recipient-license", Secrets: &firstLaunchTestSecretWriter{},
	})

	if err != nil || !imported {
		t.Fatalf("ImportFirstLaunchSeed() = %v, %v, want true, nil", imported, err)
	}
	assertSeedFile(t, filepath.Join(targetRoot, "data", "app.db"), "seed-database")
	if _, err := os.Lstat(filepath.Join(targetRoot, "bootstrap-state", "imported.json")); err != nil {
		t.Fatalf("marker missing after atomic pristine-data replacement: %v", err)
	}
	lateWrite := []byte("late-open-fd-write")
	if _, err := lateWriter.Write(lateWrite); err != nil {
		t.Fatalf("late write through displaced database descriptor: %v", err)
	}
	if err := lateWriter.Close(); err != nil {
		t.Fatal(err)
	}
	quarantine := firstLaunchQuarantineRootForTest(t, targetRoot)
	quarantinedData := filepath.Join(quarantine, "data")
	quarantinedDatabase := filepath.Join(quarantinedData, "app.db")
	quarantinedDataInfo, err := os.Lstat(quarantinedData)
	if err != nil {
		t.Fatal(err)
	}
	quarantinedDatabaseInfo, err := os.Lstat(quarantinedDatabase)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(originalDataInfo, quarantinedDataInfo) || !os.SameFile(originalDatabaseInfo, quarantinedDatabaseInfo) {
		t.Fatal("successful swap did not preserve the exact displaced data/database identities")
	}
	gotDatabase, err := os.ReadFile(quarantinedDatabase)
	if err != nil {
		t.Fatal(err)
	}
	if want := append(append([]byte(nil), originalDatabase...), lateWrite...); !bytes.Equal(gotDatabase, want) {
		t.Fatal("quarantined database did not retain the exact original bytes plus the late descriptor write")
	}
}

func TestImportFirstLaunchSeedMarkerConflictPreservesForeignStateAndRollsBackSecrets(t *testing.T) {
	targetRoot := t.TempDir()
	targetData := filepath.Join(targetRoot, "data")
	databasePath := filepath.Join(targetData, "app.db")
	writePristineGeneratedDatabase(t, databasePath)
	wantDataSnapshot := snapshotFirstLaunchTree(t, targetData)
	dataInfoBefore, err := os.Lstat(targetData)
	if err != nil {
		t.Fatal(err)
	}
	databaseInfoBefore, err := os.Lstat(databasePath)
	if err != nil {
		t.Fatal(err)
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
	previousRename := renameFirstLaunchNoReplace
	t.Cleanup(func() { renameFirstLaunchNoReplace = previousRename })
	var destinations []string
	renameFirstLaunchNoReplace = func(source, destination string) error {
		destinations = append(destinations, filepath.Base(destination))
		if filepath.Base(destination) == "bootstrap-state" {
			assertSeedFile(t, filepath.Join(targetData, "app.db"), "seed-database")
			writeSeedFile(t, filepath.Join(destination, "imported.json"), "foreign-marker")
			writeSeedFile(t, filepath.Join(targetData, "sessions", "foreign.session"), "foreign-data")
		}
		return previousRename(source, destination)
	}

	imported, err := ImportFirstLaunchSeed(context.Background(), FirstLaunchSeedConfig{
		BundlePath: bundlePath, TargetRoot: targetRoot, AppVersion: "0.8.2",
		License: "recipient-license", Secrets: writer,
	})

	if imported {
		t.Fatal("ImportFirstLaunchSeed() succeeded after a foreign marker collision")
	}
	if !errors.Is(err, ErrFirstLaunchSeedTargetChanged) {
		t.Fatalf("ImportFirstLaunchSeed() error = %v, want ErrFirstLaunchSeedTargetChanged", err)
	}
	if !reflect.DeepEqual(destinations, []string{"bootstrap-state"}) {
		t.Fatalf("no-replace destinations = %v, want marker after data swap", destinations)
	}
	assertSeedFile(t, filepath.Join(targetRoot, "bootstrap-state", "imported.json"), "foreign-marker")
	if got := snapshotFirstLaunchTree(t, targetData); !reflect.DeepEqual(got, wantDataSnapshot) {
		t.Fatalf("original pristine data was not restored after marker conflict\nwant: %v\ngot:  %v", wantDataSnapshot, got)
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
		t.Fatal("marker-conflict rollback did not restore exact pristine data identities")
	}
	quarantine := firstLaunchQuarantineRootForTest(t, targetRoot)
	assertSeedFile(t, filepath.Join(quarantine, "data", "app.db"), "seed-database")
	assertSeedFile(t, filepath.Join(quarantine, "data", "sessions", "foreign.session"), "foreign-data")
	assertFirstLaunchSecretsUnchangedForTest(t, writer, oldSecrets)
}

func TestImportFirstLaunchSeedPreservesBothNamespacesWhenSwapRollbackFails(t *testing.T) {
	targetRoot := t.TempDir()
	targetData := filepath.Join(targetRoot, "data")
	writePristineGeneratedDatabase(t, filepath.Join(targetData, "app.db"))
	bundlePath := packFirstLaunchSeedWithSecrets(t, map[string]string{
		"app.db": "seed-database",
	}, completeFirstLaunchOperationalSecretsForTest())
	oldSecrets := completeFirstLaunchOperationalSecretsForTest()
	oldSecrets["unrelated-secret"] = []byte("unrelated-value")
	writer := &firstLaunchTestSecretWriter{values: cloneFirstLaunchSecretsForTest(oldSecrets)}
	forcedRollback := errors.New("forced swap rollback failure")
	var wantOriginal []string
	previousHook := afterFirstLaunchTargetObserved
	previousExchange := exchangeFirstLaunchPaths
	t.Cleanup(func() {
		afterFirstLaunchTargetObserved = previousHook
		exchangeFirstLaunchPaths = previousExchange
	})
	afterFirstLaunchTargetObserved = func() {
		writeSeedFile(t, filepath.Join(targetData, "sessions", "concurrent.session"), "original-namespace")
		wantOriginal = snapshotFirstLaunchTree(t, targetData)
	}
	exchangeCalls := 0
	exchangeFirstLaunchPaths = func(left, right string) error {
		exchangeCalls++
		if exchangeCalls == 2 {
			return forcedRollback
		}
		return previousExchange(left, right)
	}

	imported, err := ImportFirstLaunchSeed(context.Background(), FirstLaunchSeedConfig{
		BundlePath: bundlePath, TargetRoot: targetRoot, AppVersion: "0.8.2",
		License: "recipient-license", Secrets: writer,
	})

	if imported || !errors.Is(err, ErrFirstLaunchSeedTargetChanged) || !errors.Is(err, forcedRollback) {
		t.Fatalf("ImportFirstLaunchSeed() = %v, %v, want target-changed plus rollback failure", imported, err)
	}
	assertSeedFile(t, filepath.Join(targetData, "app.db"), "seed-database")
	if _, statErr := os.Lstat(filepath.Join(targetRoot, "bootstrap-state")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("marker exists after failed rollback: %v", statErr)
	}
	quarantine := firstLaunchQuarantineRootForTest(t, targetRoot)
	if got := snapshotFirstLaunchTree(t, filepath.Join(quarantine, "data")); !reflect.DeepEqual(got, wantOriginal) {
		t.Fatalf("original namespace was not preserved in quarantine\nwant: %v\ngot:  %v", wantOriginal, got)
	}
	assertFirstLaunchSecretsUnchangedForTest(t, writer, oldSecrets)
}

func TestImportFirstLaunchSeedNewDataMarkerConflictQuarantinesRollback(t *testing.T) {
	targetRoot := t.TempDir()
	bundlePath := packFirstLaunchSeedWithSecrets(t, map[string]string{
		"app.db": "seed-database",
	}, completeFirstLaunchOperationalSecretsForTest())
	oldSecrets := completeFirstLaunchOperationalSecretsForTest()
	oldSecrets["unrelated-secret"] = []byte("unrelated-value")
	writer := &firstLaunchTestSecretWriter{values: cloneFirstLaunchSecretsForTest(oldSecrets)}
	previousRename := renameFirstLaunchNoReplace
	t.Cleanup(func() { renameFirstLaunchNoReplace = previousRename })
	var destinations []string
	renameFirstLaunchNoReplace = func(source, destination string) error {
		destinations = append(destinations, filepath.Base(destination))
		if filepath.Base(destination) == "bootstrap-state" {
			writeSeedFile(t, filepath.Join(destination, "imported.json"), "foreign-marker")
		}
		return previousRename(source, destination)
	}

	imported, err := ImportFirstLaunchSeed(context.Background(), FirstLaunchSeedConfig{
		BundlePath: bundlePath, TargetRoot: targetRoot, AppVersion: "0.8.2",
		License: "recipient-license", Secrets: writer,
	})

	if imported || !errors.Is(err, ErrFirstLaunchSeedTargetChanged) {
		t.Fatalf("ImportFirstLaunchSeed() = %v, %v, want false, ErrFirstLaunchSeedTargetChanged", imported, err)
	}
	if !reflect.DeepEqual(destinations, []string{"data", "bootstrap-state", "data"}) {
		t.Fatalf("no-replace destinations = %v, want data, marker, quarantined data", destinations)
	}
	if _, statErr := os.Lstat(filepath.Join(targetRoot, "data")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("target data exists after quarantined rollback: %v", statErr)
	}
	assertSeedFile(t, filepath.Join(targetRoot, "bootstrap-state", "imported.json"), "foreign-marker")
	quarantine := firstLaunchQuarantineRootForTest(t, targetRoot)
	assertSeedFile(t, filepath.Join(quarantine, "data", "app.db"), "seed-database")
	assertFirstLaunchSecretsUnchangedForTest(t, writer, oldSecrets)
}

func TestFirstLaunchDataIsReplaceableRejectsSQLiteSidecarsBeforeOpeningDatabase(t *testing.T) {
	for _, sidecar := range []struct {
		name     string
		contents string
	}{
		{name: "app.db-wal", contents: "pending-wal"},
		{name: "app.db-shm"},
	} {
		t.Run(sidecar.name, func(t *testing.T) {
			root := t.TempDir()
			writePristineGeneratedDatabase(t, filepath.Join(root, "app.db"))
			writeSeedFile(t, filepath.Join(root, sidecar.name), sidecar.contents)
			want := snapshotFirstLaunchTree(t, root)

			replaceable, err := firstLaunchDataIsReplaceable(root)

			if err != nil {
				t.Fatal(err)
			}
			if replaceable {
				t.Fatal("firstLaunchDataIsReplaceable() accepted ambiguous SQLite sidecars")
			}
			if got := snapshotFirstLaunchTree(t, root); !reflect.DeepEqual(got, want) {
				t.Fatalf("sidecar inspection mutated tree\nwant: %v\ngot:  %v", want, got)
			}
		})
	}
}

func TestFirstLaunchNoReplacePreservesBothTreesOnConflict(t *testing.T) {
	parent, err := canonicalFirstLaunchTargetRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(parent, "source")
	destination := filepath.Join(parent, "destination")
	writeSeedFile(t, filepath.Join(source, "seed"), "seed-bytes")
	writeSeedFile(t, filepath.Join(destination, "user"), "user-bytes")
	wantSource := snapshotFirstLaunchTree(t, source)
	wantDestination := snapshotFirstLaunchTree(t, destination)

	err = renameFirstLaunchNoReplace(source, destination)

	if !errors.Is(err, os.ErrExist) {
		t.Fatalf("renameFirstLaunchNoReplace() error = %v, want os.ErrExist", err)
	}
	if got := snapshotFirstLaunchTree(t, source); !reflect.DeepEqual(got, wantSource) {
		t.Fatalf("source changed on no-replace conflict\nwant: %v\ngot:  %v", wantSource, got)
	}
	if got := snapshotFirstLaunchTree(t, destination); !reflect.DeepEqual(got, wantDestination) {
		t.Fatalf("destination changed on no-replace conflict\nwant: %v\ngot:  %v", wantDestination, got)
	}
}

func TestImportFirstLaunchSeedPublishesMarkerAfterData(t *testing.T) {
	targetRoot := t.TempDir()
	bundlePath := packFirstLaunchSeedWithSecrets(t, map[string]string{
		"app.db": "seed-database",
	}, completeFirstLaunchOperationalSecretsForTest())
	previousRename := renameFirstLaunchNoReplace
	t.Cleanup(func() { renameFirstLaunchNoReplace = previousRename })
	var mu sync.Mutex
	var order []string
	renameFirstLaunchNoReplace = func(source, destination string) error {
		mu.Lock()
		order = append(order, filepath.Base(destination))
		mu.Unlock()
		return previousRename(source, destination)
	}

	imported, err := ImportFirstLaunchSeed(context.Background(), FirstLaunchSeedConfig{
		BundlePath: bundlePath,
		TargetRoot: targetRoot,
		AppVersion: "0.8.2",
		License:    "recipient-license",
		Secrets:    &firstLaunchTestSecretWriter{},
	})

	if err != nil || !imported {
		t.Fatalf("ImportFirstLaunchSeed() = %v, %v, want true, nil", imported, err)
	}
	mu.Lock()
	gotOrder := append([]string(nil), order...)
	mu.Unlock()
	wantOrder := []string{"data", "bootstrap-state"}
	if !reflect.DeepEqual(gotOrder, wantOrder) {
		t.Fatalf("publish order = %v, want %v", gotOrder, wantOrder)
	}
	assertSeedFile(t, filepath.Join(targetRoot, "data", "app.db"), "seed-database")
	if _, err := os.Lstat(filepath.Join(targetRoot, "bootstrap-state", "imported.json")); err != nil {
		t.Fatalf("marker not published last: %v", err)
	}
	if quarantines := firstLaunchQuarantineRootsForTest(t, targetRoot); len(quarantines) != 0 {
		t.Fatalf("clean absent-data import retained quarantine roots: %v", quarantines)
	}
}

func cloneFirstLaunchSecretsForTest(values map[string][]byte) map[string][]byte {
	cloned := make(map[string][]byte, len(values))
	for name, value := range values {
		cloned[name] = append([]byte(nil), value...)
	}
	return cloned
}

func assertFirstLaunchSecretsUnchangedForTest(t *testing.T, writer *firstLaunchTestSecretWriter, want map[string][]byte) {
	t.Helper()
	writer.mu.Lock()
	got := cloneFirstLaunchSecretsForTest(writer.values)
	writer.mu.Unlock()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("secrets changed after failed atomic transition\nwant: %v\ngot:  %v", want, got)
	}
}

func firstLaunchQuarantineRootForTest(t *testing.T, targetRoot string) string {
	t.Helper()
	roots := firstLaunchQuarantineRootsForTest(t, targetRoot)
	if len(roots) != 1 {
		t.Fatalf("quarantine roots = %v, want exactly one", roots)
	}
	return roots[0]
}

func firstLaunchQuarantineRootsForTest(t *testing.T, targetRoot string) []string {
	t.Helper()
	pattern := filepath.Join(filepath.Dir(targetRoot), "."+filepath.Base(targetRoot)+".first-launch-seed-*")
	roots, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatal(err)
	}
	return roots
}
