package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"telegram-companion/internal/bootstrapstate"

	_ "modernc.org/sqlite"
)

func TestInspectFirstLaunchSeedTargetClassifiesStrictlyWithoutMutation(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*testing.T, string)
		want  FirstLaunchTargetState
	}{
		{name: "clean", want: FirstLaunchTargetRequiresSeed},
		{
			name: "valid import marker with meaningful data and runtime-owned proxy state",
			setup: func(t *testing.T, root string) {
				writeValidFirstLaunchMarker(t, root)
				writeSeedFile(t, filepath.Join(root, "data", "sessions", "session.bin"), "existing-session")
				writeSeedFile(t, filepath.Join(root, "license", "license.tcomplicense"), "recipient-license")
				writeSeedFile(t, filepath.Join(root, "logs", "desktop.log"), "runtime-log")
				writeSeedFile(t, filepath.Join(root, "proxy", "tor-snowflake", "torrc"), "runtime-config")
				writeSeedFile(t, filepath.Join(root, "proxy", "tor-snowflake", "tor-output.log"), "runtime-output")
				writeSeedFile(t, filepath.Join(root, "proxy", "tor-snowflake", "bootstrap.log"), "runtime-bootstrap")
				writeSeedFile(t, filepath.Join(root, "proxy", "tor-snowflake", "data", "state"), "runtime-state")
			},
			want: FirstLaunchTargetExistingProfile,
		},
		{
			name: "valid marker without meaningful data",
			setup: func(t *testing.T, root string) {
				writeValidFirstLaunchMarker(t, root)
			},
			want: FirstLaunchTargetBlocked,
		},
		{
			name: "malformed marker with meaningful data",
			setup: func(t *testing.T, root string) {
				writeSeedFile(t, filepath.Join(root, "bootstrap-state", "imported.json"), `{"schema_version":`)
				writeSeedFile(t, filepath.Join(root, "data", "sessions", "session.bin"), "existing-session")
			},
			want: FirstLaunchTargetBlocked,
		},
		{
			name: "wrong-shape marker with meaningful data",
			setup: func(t *testing.T, root string) {
				writeSeedFile(t, filepath.Join(root, "bootstrap-state", "imported.json"), `{"schema_version":1,"bundle_id":"seed","app_version":"0.8.2","created_at":"2026-08-13T12:00:00Z","secrets":{"scout-message-key":"forbidden"}}`)
				writeSeedFile(t, filepath.Join(root, "data", "sessions", "session.bin"), "existing-session")
			},
			want: FirstLaunchTargetBlocked,
		},
		{
			name: "nonempty runtime-owned proxy without profile",
			setup: func(t *testing.T, root string) {
				writeSeedFile(t, filepath.Join(root, "proxy", "tor-snowflake", "torrc"), "runtime-config")
			},
			want: FirstLaunchTargetBlocked,
		},
		{
			name: "empty runtime-owned proxy without profile",
			setup: func(t *testing.T, root string) {
				if err := os.MkdirAll(filepath.Join(root, "proxy", "tor-snowflake"), 0o700); err != nil {
					t.Fatal(err)
				}
			},
			want: FirstLaunchTargetBlocked,
		},
		{
			name: "unrecognized proxy child alongside meaningful profile",
			setup: func(t *testing.T, root string) {
				writeValidFirstLaunchMarker(t, root)
				writeSeedFile(t, filepath.Join(root, "data", "sessions", "session.bin"), "existing-session")
				writeSeedFile(t, filepath.Join(root, "proxy", "tor-snowflake", "state.json"), "unknown-runtime-state")
			},
			want: FirstLaunchTargetBlocked,
		},
		{
			name: "unrecognized license file alongside meaningful profile",
			setup: func(t *testing.T, root string) {
				writeSeedFile(t, filepath.Join(root, "data", "sessions", "session.bin"), "existing-session")
				writeSeedFile(t, filepath.Join(root, "license", "license.backup"), "unknown-license-state")
			},
			want: FirstLaunchTargetBlocked,
		},
		{
			name: "unrecognized log file alongside meaningful profile",
			setup: func(t *testing.T, root string) {
				writeSeedFile(t, filepath.Join(root, "data", "sessions", "session.bin"), "existing-session")
				writeSeedFile(t, filepath.Join(root, "logs", "debug.log"), "unknown-log-state")
			},
			want: FirstLaunchTargetBlocked,
		},
		{
			name: "meaningful recognized profile",
			setup: func(t *testing.T, root string) {
				writeSeedFile(t, filepath.Join(root, "data", "sessions", "session.bin"), "existing-session")
			},
			want: FirstLaunchTargetExistingProfile,
		},
		{
			name: "unknown entry",
			setup: func(t *testing.T, root string) {
				writeSeedFile(t, filepath.Join(root, "unknown-profile-entry"), "preserve-me")
			},
			want: FirstLaunchTargetBlocked,
		},
		{
			name: "pristine database with wal sidecar",
			setup: func(t *testing.T, root string) {
				writePristineGeneratedDatabase(t, filepath.Join(root, "data", "app.db"))
				writeSeedFile(t, filepath.Join(root, "data", "app.db-wal"), "pending-wal")
			},
			want: FirstLaunchTargetBlocked,
		},
		{
			name: "pristine database with empty shm sidecar",
			setup: func(t *testing.T, root string) {
				writePristineGeneratedDatabase(t, filepath.Join(root, "data", "app.db"))
				writeSeedFile(t, filepath.Join(root, "data", "app.db-shm"), "")
			},
			want: FirstLaunchTargetBlocked,
		},
		{
			name: "symbolic link",
			setup: func(t *testing.T, root string) {
				target := filepath.Join(t.TempDir(), "outside")
				writeSeedFile(t, target, "outside")
				if err := os.Symlink(target, filepath.Join(root, "logs")); err != nil {
					t.Fatal(err)
				}
			},
			want: FirstLaunchTargetBlocked,
		},
		{
			name: "nested symbolic link",
			setup: func(t *testing.T, root string) {
				target := filepath.Join(t.TempDir(), "outside")
				writeSeedFile(t, target, "outside")
				if err := os.MkdirAll(filepath.Join(root, "logs"), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, filepath.Join(root, "logs", "desktop.log")); err != nil {
					t.Fatal(err)
				}
			},
			want: FirstLaunchTargetBlocked,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if test.setup != nil {
				test.setup(t, root)
			}
			before := snapshotFirstLaunchTree(t, root)

			got, err := InspectFirstLaunchSeedTarget(root)

			if err != nil {
				t.Fatalf("InspectFirstLaunchSeedTarget() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("InspectFirstLaunchSeedTarget() = %v, want %v", got, test.want)
			}
			required, err := FirstLaunchSeedRequired(root)
			if err != nil {
				t.Fatalf("FirstLaunchSeedRequired() error = %v", err)
			}
			if required != (test.want == FirstLaunchTargetRequiresSeed) {
				t.Fatalf("FirstLaunchSeedRequired() = %v for state %v", required, test.want)
			}
			if after := snapshotFirstLaunchTree(t, root); fmt.Sprint(after) != fmt.Sprint(before) {
				t.Fatalf("inspection mutated tree\nbefore: %v\nafter:  %v", before, after)
			}
		})
	}
}

func writeValidFirstLaunchMarker(t *testing.T, root string) {
	t.Helper()
	writeSeedFile(t, filepath.Join(root, "bootstrap-state", "imported.json"),
		`{"schema_version":1,"bundle_id":"first-launch-test","app_version":"0.8.2","created_at":"2026-08-13T12:00:00Z"}`)
}

func TestInspectFirstLaunchSeedTargetBlocksSymlinkRootWithoutMutation(t *testing.T) {
	target := t.TempDir()
	writeSeedFile(t, filepath.Join(target, "logs", "desktop.log"), "preserve")
	link := filepath.Join(t.TempDir(), "profile")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	before := snapshotFirstLaunchTree(t, target)

	state, err := InspectFirstLaunchSeedTarget(link)

	if err != nil {
		t.Fatal(err)
	}
	if state != FirstLaunchTargetBlocked {
		t.Fatalf("InspectFirstLaunchSeedTarget() = %v, want blocked", state)
	}
	if after := snapshotFirstLaunchTree(t, target); fmt.Sprint(after) != fmt.Sprint(before) {
		t.Fatalf("inspection mutated symlink target\nbefore: %v\nafter: %v", before, after)
	}
}

func snapshotFirstLaunchTree(t *testing.T, root string) []string {
	t.Helper()
	var snapshot []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		value := ""
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			value, err = os.Readlink(path)
		case info.Mode().IsRegular():
			var data []byte
			data, err = os.ReadFile(path)
			value = string(data)
		}
		if err != nil {
			return err
		}
		snapshot = append(snapshot, fmt.Sprintf("%s|%s|%q", relative, info.Mode(), value))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestImportFirstLaunchSeedInstallsAllowedDataWithoutReplacingExistingRootState(t *testing.T) {
	targetRoot := t.TempDir()
	writeSeedFile(t, filepath.Join(targetRoot, "license", "license.tcomplicense"), "recipient-license")
	writeSeedFile(t, filepath.Join(targetRoot, "logs", "desktop.log"), "existing-log")
	bundlePath := packFirstLaunchSeed(t, map[string]string{
		"app.db":                    "database-snapshot",
		"tdata/account/session.bin": "telegram-tdata",
		"sessions/account.json":     "telegram-session",
		"gotd-import-staging/source/account-1/session.json": "gotd-session",
		"application-state.bolt":                            "master-settings",
	})

	imported, err := ImportFirstLaunchSeed(context.Background(), FirstLaunchSeedConfig{
		BundlePath: bundlePath,
		TargetRoot: targetRoot,
		AppVersion: "0.8.2",
		License:    "recipient-license",
		Secrets:    &firstLaunchTestSecretWriter{},
	})
	if err != nil {
		t.Fatalf("ImportFirstLaunchSeed() error = %v", err)
	}
	if !imported {
		t.Fatal("ImportFirstLaunchSeed() = false, want true")
	}

	assertSeedFile(t, filepath.Join(targetRoot, "data", "app.db"), "database-snapshot")
	assertSeedFile(t, filepath.Join(targetRoot, "data", "tdata", "account", "session.bin"), "telegram-tdata")
	assertSeedFile(t, filepath.Join(targetRoot, "data", "sessions", "account.json"), "telegram-session")
	assertSeedFile(t, filepath.Join(targetRoot, "data", "gotd-import-staging", "source", "account-1", "session.json"), "gotd-session")
	assertSeedFile(t, filepath.Join(targetRoot, "data", "application-state.bolt"), "master-settings")
	assertSeedFile(t, filepath.Join(targetRoot, "license", "license.tcomplicense"), "recipient-license")
	assertSeedFile(t, filepath.Join(targetRoot, "logs", "desktop.log"), "existing-log")
	if _, err := os.Stat(filepath.Join(targetRoot, "bootstrap-state", "imported.json")); err != nil {
		t.Fatalf("first launch seed marker is not installed in the profile: %v", err)
	}
	assertNoFirstLaunchStaging(t, targetRoot)
}

func TestImportFirstLaunchSeedDiskCapacityBoundary(t *testing.T) {
	files := map[string]string{
		"app.db":                   "database-snapshot",
		"sessions/account/session": "telegram-session",
	}
	bundlePath := packFirstLaunchSeed(t, files)
	const blockSize = uint64(4096)
	footprint := bootstrapstate.BundleFootprint{
		DataBytes:     uint64(len(files["app.db"]) + len(files["sessions/account/session"])),
		RegularFiles:  2,
		Directories:   3,
		DatabaseBytes: uint64(len(files["app.db"])),
	}
	required := footprint.DataBytes + footprint.RegularFiles*(blockSize-1) + footprint.Directories*blockSize + uint64(512<<20)

	for _, test := range []struct {
		name      string
		available uint64
		want      bool
	}{
		{name: "one byte short", available: required - 1, want: false},
		{name: "exact capacity", available: required, want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			previousProbe := firstLaunchDiskSpaceProbe
			firstLaunchDiskSpaceProbe = func(string) (firstLaunchDiskSpace, error) {
				return firstLaunchDiskSpace{AvailableBytes: test.available, BlockSize: blockSize}, nil
			}
			t.Cleanup(func() { firstLaunchDiskSpaceProbe = previousProbe })

			targetRoot := t.TempDir()
			writer := &firstLaunchTestSecretWriter{}
			imported, err := ImportFirstLaunchSeed(context.Background(), FirstLaunchSeedConfig{
				BundlePath: bundlePath,
				TargetRoot: targetRoot,
				AppVersion: "0.8.2",
				License:    "recipient-license",
				Secrets:    writer,
			})
			if test.want {
				if err != nil {
					t.Fatalf("ImportFirstLaunchSeed() error = %v", err)
				}
				if !imported {
					t.Fatal("ImportFirstLaunchSeed() = false at exact capacity")
				}
				return
			}

			if imported {
				t.Fatal("ImportFirstLaunchSeed() imported with insufficient capacity")
			}
			var insufficient *InsufficientSpaceError
			if !errors.As(err, &insufficient) {
				t.Fatalf("ImportFirstLaunchSeed() error = %T, want *InsufficientSpaceError", err)
			}
			if insufficient.RequiredBytes != required || insufficient.AvailableBytes != required-1 {
				t.Fatalf("insufficient space = %+v, want required %d available %d", insufficient, required, required-1)
			}
			if writer.batchCalls != 0 {
				t.Fatalf("SetBatch calls = %d, want 0 before failed preflight", writer.batchCalls)
			}
			if _, statErr := os.Stat(filepath.Join(targetRoot, "data")); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("target data exists after failed preflight: %v", statErr)
			}
		})
	}
}

func TestImportFirstLaunchSeedDiskProbeUsesTargetRootAndRedactsFailure(t *testing.T) {
	bundlePath := packFirstLaunchSeed(t, map[string]string{"app.db": "database-snapshot"})
	targetRoot := filepath.Join(t.TempDir(), "profile", "..", "profile")
	rawProbeErr := errors.New("private probe detail")
	previousProbe := firstLaunchDiskSpaceProbe
	var probedPath string
	firstLaunchDiskSpaceProbe = func(path string) (firstLaunchDiskSpace, error) {
		probedPath = path
		return firstLaunchDiskSpace{}, rawProbeErr
	}
	t.Cleanup(func() { firstLaunchDiskSpaceProbe = previousProbe })

	imported, err := ImportFirstLaunchSeed(context.Background(), FirstLaunchSeedConfig{
		BundlePath: bundlePath,
		TargetRoot: targetRoot,
		AppVersion: "0.8.2",
		License:    "recipient-license",
		Secrets:    &firstLaunchTestSecretWriter{},
	})
	if imported {
		t.Fatal("ImportFirstLaunchSeed() imported after disk probe failure")
	}
	var probeError *StorageProbeError
	if !errors.As(err, &probeError) {
		t.Fatalf("ImportFirstLaunchSeed() error = %T, want *StorageProbeError", err)
	}
	if !errors.Is(err, rawProbeErr) {
		t.Fatal("StorageProbeError does not retain its cause for typed classification")
	}
	if probedPath != filepath.Clean(targetRoot) {
		t.Fatal("disk probe did not use the cleaned TargetRoot")
	}
	if strings.Contains(err.Error(), rawProbeErr.Error()) || strings.Contains(err.Error(), filepath.Clean(targetRoot)) {
		t.Fatal("disk probe error exposes private detail")
	}
}

func TestFirstLaunchDiskSpaceArithmeticBoundariesFailClosed(t *testing.T) {
	maximum := ^uint64(0)
	for _, test := range []struct {
		name      string
		footprint bootstrapstate.BundleFootprint
		blockSize uint64
	}{
		{name: "zero block size", blockSize: 0},
		{
			name:      "regular file rounding multiplication overflow",
			footprint: bootstrapstate.BundleFootprint{RegularFiles: 2},
			blockSize: maximum,
		},
		{
			name:      "directory allocation multiplication overflow",
			footprint: bootstrapstate.BundleFootprint{Directories: 2},
			blockSize: maximum,
		},
		{
			name:      "data and rounding addition overflow",
			footprint: bootstrapstate.BundleFootprint{DataBytes: maximum, RegularFiles: 1},
			blockSize: 2,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, ok := firstLaunchRequiredDiskBytes(test.footprint, test.blockSize); ok {
				t.Fatal("firstLaunchRequiredDiskBytes() accepted overflowing input")
			}
		})
	}
}

func TestFirstLaunchDiskArithmeticFailureUsesStableTypedError(t *testing.T) {
	previousProbe := firstLaunchDiskSpaceProbe
	firstLaunchDiskSpaceProbe = func(string) (firstLaunchDiskSpace, error) {
		return firstLaunchDiskSpace{AvailableBytes: ^uint64(0), BlockSize: 0}, nil
	}
	t.Cleanup(func() { firstLaunchDiskSpaceProbe = previousProbe })
	err := preflightFirstLaunchDiskSpace(context.Background(), t.TempDir(), bootstrapstate.BundleFootprint{})
	var probeError *StorageProbeError
	if !errors.As(err, &probeError) {
		t.Fatalf("preflightFirstLaunchDiskSpace() error = %T, want *StorageProbeError", err)
	}
	if err.Error() != "first launch seed: storage availability check failed" {
		t.Fatal("storage arithmetic error message is not stable and redacted")
	}
}

func TestFirstLaunchAvailableBytesRejectsDarwinStatfsOverflow(t *testing.T) {
	if _, ok := firstLaunchAvailableBytes(2, ^uint64(0)); ok {
		t.Fatal("firstLaunchAvailableBytes() accepted multiplication overflow")
	}
	available, ok := firstLaunchAvailableBytes(3, 4096)
	if !ok || available != 12_288 {
		t.Fatalf("firstLaunchAvailableBytes() = %d, %t, want 12288, true", available, ok)
	}
}

func TestImportFirstLaunchSeedRollsBackAfterPostPreflightENOSPC(t *testing.T) {
	targetRoot := t.TempDir()
	atomicTargetRoot, err := canonicalFirstLaunchTargetRoot(targetRoot)
	if err != nil {
		t.Fatal(err)
	}
	bundlePath := packFirstLaunchSeedWithSecrets(t, map[string]string{
		"app.db": "database-snapshot",
	}, completeFirstLaunchOperationalSecretsWithOverridesForTest(map[string][]byte{
		"scout-message-key": firstLaunchSecretBytesForTest(0x51),
	}))
	writer := &firstLaunchTestSecretWriter{values: map[string][]byte{
		"scout-message-key": []byte("existing-operational-secret"),
	}}
	previousProbe := firstLaunchDiskSpaceProbe
	probeCalls := 0
	firstLaunchDiskSpaceProbe = func(string) (firstLaunchDiskSpace, error) {
		probeCalls++
		return firstLaunchDiskSpace{AvailableBytes: ^uint64(0), BlockSize: 4096}, nil
	}
	t.Cleanup(func() { firstLaunchDiskSpaceProbe = previousProbe })
	previousRename := renameFirstLaunchNoReplace
	renameFirstLaunchNoReplace = func(source, destination string) error {
		if destination == filepath.Join(atomicTargetRoot, "data") {
			return syscall.ENOSPC
		}
		return previousRename(source, destination)
	}
	t.Cleanup(func() { renameFirstLaunchNoReplace = previousRename })

	imported, err := ImportFirstLaunchSeed(context.Background(), FirstLaunchSeedConfig{
		BundlePath: bundlePath,
		TargetRoot: targetRoot,
		AppVersion: "0.8.2",
		License:    "recipient-license",
		Secrets:    writer,
	})
	if imported {
		t.Fatal("ImportFirstLaunchSeed() imported after ENOSPC")
	}
	if !errors.Is(err, syscall.ENOSPC) {
		t.Fatalf("ImportFirstLaunchSeed() error = %v, want ENOSPC", err)
	}
	if probeCalls != 1 {
		t.Fatalf("disk probe calls = %d, want 1", probeCalls)
	}
	if writer.batchCalls != 1 {
		t.Fatalf("SetBatch calls = %d, want 1 after successful preflight", writer.batchCalls)
	}
	if got := string(writer.value("scout-message-key")); got != "existing-operational-secret" {
		t.Fatal("operational secret was not rolled back")
	}
	for _, name := range []string{"data", "bootstrap-state"} {
		if _, statErr := os.Stat(filepath.Join(targetRoot, name)); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("target %s exists after ENOSPC rollback: %v", name, statErr)
		}
	}
}

func TestImportFirstLaunchSeedSkipsExistingUserDataBeforeReadingBundle(t *testing.T) {
	targetRoot := t.TempDir()
	writeSeedFile(t, filepath.Join(targetRoot, "data", "app.db"), "user-database")
	writeSeedFile(t, filepath.Join(targetRoot, "data", "tdata", "account", "session.bin"), "user-session")

	imported, err := ImportFirstLaunchSeed(context.Background(), FirstLaunchSeedConfig{
		BundlePath: filepath.Join(t.TempDir(), "missing-state.tcs"),
		TargetRoot: targetRoot,
		AppVersion: "0.8.2",
		License:    "recipient-license",
		Secrets:    &firstLaunchTestSecretWriter{},
	})
	if err != nil {
		t.Fatalf("ImportFirstLaunchSeed() error = %v", err)
	}
	if imported {
		t.Fatal("ImportFirstLaunchSeed() imported over existing user data")
	}
	assertSeedFile(t, filepath.Join(targetRoot, "data", "app.db"), "user-database")
}

func TestImportFirstLaunchSeedSkipsLegacyDatabaseWithExistingBackups(t *testing.T) {
	targetRoot := t.TempDir()
	writeFirstLaunchRuntimeDatabase(t, filepath.Join(targetRoot, "data", "app.db"), false)
	writeSeedFile(t, filepath.Join(targetRoot, "data", "application-state.bolt"), "empty-legacy-state")
	writeSeedFile(t, filepath.Join(targetRoot, "data", "backups", "daily.scout-backup"), "generated-backup")
	bundlePath := packFirstLaunchSeed(t, map[string]string{
		"app.db":                    "master-database",
		"tdata/account/session.bin": "telegram-tdata",
		"gotd-import-staging/source/account-1/session.json": "gotd-session",
	})

	imported, err := ImportFirstLaunchSeed(context.Background(), FirstLaunchSeedConfig{
		BundlePath: bundlePath,
		TargetRoot: targetRoot,
		AppVersion: "0.8.2",
		License:    "recipient-license",
		Secrets:    &firstLaunchTestSecretWriter{},
	})
	if err != nil {
		t.Fatalf("ImportFirstLaunchSeed() error = %v", err)
	}
	if imported {
		t.Fatal("ImportFirstLaunchSeed() replaced data with existing backups")
	}
	assertSeedFile(t, filepath.Join(targetRoot, "data", "backups", "daily.scout-backup"), "generated-backup")
}

func TestImportFirstLaunchSeedSkipsDatabaseWithUserStateWithoutTelegramSessions(t *testing.T) {
	targetRoot := t.TempDir()
	databasePath := filepath.Join(targetRoot, "data", "app.db")
	writeFirstLaunchRuntimeDatabase(t, databasePath, true)

	imported, err := ImportFirstLaunchSeed(context.Background(), FirstLaunchSeedConfig{
		BundlePath: filepath.Join(t.TempDir(), "missing-state.tcs"),
		TargetRoot: targetRoot,
		AppVersion: "0.8.2",
		License:    "recipient-license",
		Secrets:    &firstLaunchTestSecretWriter{},
	})
	if err != nil {
		t.Fatalf("ImportFirstLaunchSeed() error = %v", err)
	}
	if imported {
		t.Fatal("ImportFirstLaunchSeed() imported over database user state")
	}
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var accounts int
	if err := db.QueryRow("SELECT COUNT(*) FROM accounts").Scan(&accounts); err != nil {
		t.Fatal(err)
	}
	if accounts != 1 {
		t.Fatalf("accounts = %d, want 1", accounts)
	}
}

func TestImportFirstLaunchSeedSkipsNonEmptyApplicationState(t *testing.T) {
	targetRoot := t.TempDir()
	writeFirstLaunchRuntimeDatabase(t, filepath.Join(targetRoot, "data", "app.db"), false)
	writeSeedFile(t, filepath.Join(targetRoot, "data", "application-state.bolt"), "user-settings")
	bundlePath := packFirstLaunchSeed(t, map[string]string{
		"app.db":                 "master-database",
		"application-state.bolt": "master-settings",
	})

	imported, err := ImportFirstLaunchSeed(context.Background(), FirstLaunchSeedConfig{
		BundlePath: bundlePath,
		TargetRoot: targetRoot,
		AppVersion: "0.8.2",
		License:    "recipient-license",
		Secrets:    &firstLaunchTestSecretWriter{},
	})
	if err != nil {
		t.Fatalf("ImportFirstLaunchSeed() error = %v", err)
	}
	if imported {
		t.Fatal("ImportFirstLaunchSeed() replaced non-empty application state")
	}
	assertSeedFile(t, filepath.Join(targetRoot, "data", "application-state.bolt"), "user-settings")
}

func TestImportFirstLaunchSeedSkipsMeaningfulAppSettingsRows(t *testing.T) {
	targetRoot := t.TempDir()
	databasePath := filepath.Join(targetRoot, "data", "app.db")
	writeFirstLaunchRuntimeDatabase(t, databasePath, false)
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE app_settings (key TEXT PRIMARY KEY, value_json TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO app_settings(key, value_json) VALUES ('keyword_settings', '{"keywords":["user"]}')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	bundlePath := packFirstLaunchSeed(t, map[string]string{"app.db": "master-database"})

	imported, err := ImportFirstLaunchSeed(context.Background(), FirstLaunchSeedConfig{
		BundlePath: bundlePath,
		TargetRoot: targetRoot,
		AppVersion: "0.8.2",
		License:    "recipient-license",
		Secrets:    &firstLaunchTestSecretWriter{},
	})
	if err != nil {
		t.Fatalf("ImportFirstLaunchSeed() error = %v", err)
	}
	if imported {
		t.Fatal("ImportFirstLaunchSeed() replaced persisted app settings")
	}

	db, err = sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var value string
	if err := db.QueryRow(`SELECT value_json FROM app_settings WHERE key = 'keyword_settings'`).Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != `{"keywords":["user"]}` {
		t.Fatalf("keyword settings = %s, want persisted user settings", value)
	}
}

func TestFirstLaunchSeedRequiredRejectsMeaningfulGeneratedTableState(t *testing.T) {
	tests := []struct {
		name   string
		mutate string
	}{
		{
			name:   "changed analytics scheduler settings",
			mutate: `UPDATE analytics_scheduler_settings SET enabled = 0, updated_at = '2026-08-13T12:01:00Z' WHERE singleton = 1`,
		},
		{
			name:   "custom analytics service word",
			mutate: `INSERT INTO analytics_service_words(id, created_at, updated_at) VALUES (89, '2026-08-13T12:01:00Z', '2026-08-13T12:01:00Z')`,
		},
		{
			name:   "analytics scheduler run",
			mutate: `INSERT INTO analytics_scheduler_runs(id) VALUES ('run-1')`,
		},
		{
			name:   "backup history",
			mutate: `INSERT INTO backup_history(id) VALUES ('backup-1')`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			targetRoot := t.TempDir()
			databasePath := filepath.Join(targetRoot, "data", "app.db")
			writePristineGeneratedDatabase(t, databasePath)
			db, err := sql.Open("sqlite", databasePath)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(test.mutate); err != nil {
				_ = db.Close()
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}

			required, err := FirstLaunchSeedRequired(targetRoot)
			if err != nil {
				t.Fatalf("FirstLaunchSeedRequired() error = %v", err)
			}
			if required {
				t.Fatal("FirstLaunchSeedRequired() = true for meaningful persisted state")
			}
		})
	}
}

func TestFirstLaunchSeedRequiredAllowsPristineGeneratedTableState(t *testing.T) {
	targetRoot := t.TempDir()
	writePristineGeneratedDatabase(t, filepath.Join(targetRoot, "data", "app.db"))

	required, err := FirstLaunchSeedRequired(targetRoot)
	if err != nil {
		t.Fatalf("FirstLaunchSeedRequired() error = %v", err)
	}
	if !required {
		t.Fatal("FirstLaunchSeedRequired() = false for pristine migration state")
	}
}

func TestImportFirstLaunchSeedSkipsNonEmptyApplicationSupportOutsideActivationArtifacts(t *testing.T) {
	targetRoot := t.TempDir()
	writeSeedFile(t, filepath.Join(targetRoot, "custom-settings.json"), "user-settings")
	bundlePath := packFirstLaunchSeed(t, map[string]string{"app.db": "database-snapshot"})

	imported, err := ImportFirstLaunchSeed(context.Background(), FirstLaunchSeedConfig{
		BundlePath: bundlePath,
		TargetRoot: targetRoot,
		AppVersion: "0.8.2",
		License:    "recipient-license",
		Secrets:    &firstLaunchTestSecretWriter{},
	})
	if err != nil {
		t.Fatalf("ImportFirstLaunchSeed() error = %v", err)
	}
	if imported {
		t.Fatal("ImportFirstLaunchSeed() imported into non-empty Application Support")
	}
	assertSeedFile(t, filepath.Join(targetRoot, "custom-settings.json"), "user-settings")
}

func TestImportFirstLaunchSeedInstallsOperationalSecrets(t *testing.T) {
	targetRoot := t.TempDir()
	want := completeFirstLaunchOperationalSecretsWithOverridesForTest(map[string][]byte{
		"scout-message-key":               firstLaunchSecretBytesForTest(0x61),
		"telegram-account-credentials-v1": firstLaunchSecretBytesForTest(0x62),
	})
	bundlePath := packFirstLaunchSeedWithSecrets(t, map[string]string{
		"app.db":                   "database-snapshot",
		"sessions/account/session": "telegram-session",
	}, want)
	writer := &firstLaunchTestSecretWriter{}

	imported, err := ImportFirstLaunchSeed(context.Background(), FirstLaunchSeedConfig{
		BundlePath: bundlePath,
		TargetRoot: targetRoot,
		AppVersion: "0.8.2",
		License:    "recipient-license",
		Secrets:    writer,
	})
	if err != nil {
		t.Fatalf("ImportFirstLaunchSeed() error = %v", err)
	}
	if !imported {
		t.Fatal("ImportFirstLaunchSeed() = false, want true")
	}
	for name, value := range want {
		if got := string(writer.values[name]); got != string(value) {
			t.Fatalf("imported secret %s = %q, want %q", name, got, value)
		}
	}
}

func TestImportFirstLaunchSeedRollsBackOperationalSecretsWhenAWriteFails(t *testing.T) {
	targetRoot := t.TempDir()
	bundlePath := packFirstLaunchSeedWithSecrets(t, map[string]string{
		"app.db": "database-snapshot",
	}, completeFirstLaunchOperationalSecretsWithOverridesForTest(map[string][]byte{
		"scout-message-key":               firstLaunchSecretBytesForTest(0x71),
		"telegram-account-credentials-v1": firstLaunchSecretBytesForTest(0x72),
	}))
	writer := &firstLaunchTestSecretWriter{
		values: map[string][]byte{
			"scout-message-key":               []byte("old-scout-secret"),
			"telegram-account-credentials-v1": []byte("old-account-secret"),
		},
		failSetAt: 2,
	}

	imported, err := ImportFirstLaunchSeed(context.Background(), FirstLaunchSeedConfig{
		BundlePath: bundlePath,
		TargetRoot: targetRoot,
		AppVersion: "0.8.2",
		License:    "recipient-license",
		Secrets:    writer,
	})
	if err == nil {
		t.Fatal("ImportFirstLaunchSeed() error = nil, want secret write failure")
	}
	if imported {
		t.Fatal("ImportFirstLaunchSeed() imported data after a secret write failure")
	}
	if got := string(writer.value("scout-message-key")); got != "old-scout-secret" {
		t.Fatalf("scout secret = %q, want original value", got)
	}
	if got := string(writer.value("telegram-account-credentials-v1")); got != "old-account-secret" {
		t.Fatalf("account secret = %q, want original value", got)
	}
	if _, statErr := os.Stat(filepath.Join(targetRoot, "data")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("target data exists after secret failure: %v", statErr)
	}
}

func TestImportFirstLaunchSeedRollsBackSecretsWhenUserStateAppearsDuringCommit(t *testing.T) {
	targetRoot := t.TempDir()
	bundlePath := packFirstLaunchSeedWithSecrets(t, map[string]string{
		"app.db": "database-snapshot",
	}, completeFirstLaunchOperationalSecretsWithOverridesForTest(map[string][]byte{
		"scout-message-key": firstLaunchSecretBytesForTest(0x81),
	}))
	userState := filepath.Join(targetRoot, "data", "sessions", "user-session")
	writer := &firstLaunchTestSecretWriter{
		afterFirstWrite: func() {
			writeSeedFile(t, userState, "user-state")
		},
	}

	imported, err := ImportFirstLaunchSeed(context.Background(), FirstLaunchSeedConfig{
		BundlePath: bundlePath,
		TargetRoot: targetRoot,
		AppVersion: "0.8.2",
		License:    "recipient-license",
		Secrets:    writer,
	})
	if imported {
		t.Fatal("ImportFirstLaunchSeed() replaced concurrently-created user state")
	}
	if !errors.Is(err, ErrFirstLaunchSeedTargetChanged) {
		t.Fatalf("ImportFirstLaunchSeed() error = %v, want ErrFirstLaunchSeedTargetChanged", err)
	}
	if got := writer.value("scout-message-key"); got != nil {
		t.Fatalf("scout secret remains after concurrent state won: %q", got)
	}
	assertSeedFile(t, userState, "user-state")
}

func TestImportFirstLaunchSeedSerializesConcurrentImports(t *testing.T) {
	targetRoot := t.TempDir()
	bundlePath := packFirstLaunchSeedWithSecrets(t, map[string]string{
		"app.db": "database-snapshot",
	}, completeFirstLaunchOperationalSecretsWithOverridesForTest(map[string][]byte{
		"scout-message-key": firstLaunchSecretBytesForTest(0x91),
	}))
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	writer := &firstLaunchTestSecretWriter{batchEntered: entered, batchRelease: release}
	config := FirstLaunchSeedConfig{
		BundlePath: bundlePath,
		TargetRoot: targetRoot,
		AppVersion: "0.8.2",
		License:    "recipient-license",
		Secrets:    writer,
	}
	type result struct {
		imported bool
		err      error
	}
	results := make(chan result, 2)
	run := func() {
		imported, err := ImportFirstLaunchSeed(context.Background(), config)
		results <- result{imported: imported, err: err}
	}

	go run()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first import did not reach secret commit")
	}
	go run()
	secondEntered := false
	select {
	case <-entered:
		secondEntered = true
	case <-time.After(250 * time.Millisecond):
	}
	close(release)

	importCount := 0
	for range 2 {
		select {
		case outcome := <-results:
			if outcome.err != nil {
				t.Fatalf("ImportFirstLaunchSeed() error = %v", outcome.err)
			}
			if outcome.imported {
				importCount++
			}
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent import did not finish")
		}
	}
	if secondEntered {
		t.Fatal("concurrent import reached keyring commit before the first import finished")
	}
	if importCount != 1 {
		t.Fatalf("successful imports = %d, want 1", importCount)
	}
	writer.mu.Lock()
	batchCalls := writer.batchCalls
	writer.mu.Unlock()
	if batchCalls != 1 {
		t.Fatalf("atomic secret commits = %d, want 1", batchCalls)
	}
}

func TestImportFirstLaunchSeedRejectsForbiddenRuntimeData(t *testing.T) {
	for _, test := range []struct {
		name  string
		files map[string]string
	}{
		{
			name: "master license file",
			files: map[string]string{
				"app.db":                       "database-snapshot",
				"license/license.tcomplicense": "master-license",
			},
		},
		{
			name: "master machine identifier",
			files: map[string]string{
				"app.db":     "database-snapshot",
				"machine-id": "master-machine-id",
			},
		},
		{
			name: "log directory",
			files: map[string]string{
				"app.db":           "database-snapshot",
				"logs/desktop.log": "master-log",
			},
		},
		{
			name: "tor runtime",
			files: map[string]string{
				"app.db":         "database-snapshot",
				"tor/state.json": "runtime-state",
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			targetRoot := t.TempDir()
			bundlePath := packFirstLaunchSeedWithSecrets(t, test.files, nil)

			imported, err := ImportFirstLaunchSeed(context.Background(), FirstLaunchSeedConfig{
				BundlePath: bundlePath,
				TargetRoot: targetRoot,
				AppVersion: "0.8.2",
				License:    "recipient-license",
				Secrets:    &firstLaunchTestSecretWriter{},
			})
			if err == nil {
				t.Fatal("ImportFirstLaunchSeed() error = nil, want rejection")
			}
			if imported {
				t.Fatal("ImportFirstLaunchSeed() imported a forbidden seed")
			}
			if !errors.Is(err, ErrInvalidFirstLaunchSeed) {
				t.Fatalf("ImportFirstLaunchSeed() error = %v, want ErrInvalidFirstLaunchSeed", err)
			}
			_, statErr := os.Stat(filepath.Join(targetRoot, "data"))
			if !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("target data exists after rejected seed: %v", statErr)
			}
		})
	}
}

func packFirstLaunchSeed(t *testing.T, files map[string]string) string {
	t.Helper()
	return packFirstLaunchSeedWithSecrets(t, files, completeFirstLaunchOperationalSecretsForTest())
}

func packFirstLaunchSeedWithSecrets(t *testing.T, files map[string]string, secrets map[string][]byte) string {
	t.Helper()
	sourceData := filepath.Join(t.TempDir(), "source-data")
	for path, contents := range files {
		writeSeedFile(t, filepath.Join(sourceData, path), contents)
	}
	bundlePath := filepath.Join(t.TempDir(), "state.tcs")
	if err := bootstrapstate.Pack(context.Background(), bootstrapstate.PackConfig{
		SourceData: sourceData,
		OutputPath: bundlePath,
		BundleID:   "first-launch-test",
		AppVersion: "0.8.2",
		License:    "recipient-license",
		Secrets:    firstLaunchTestSecrets{values: secrets},
	}); err != nil {
		t.Fatalf("pack seed: %v", err)
	}
	return bundlePath
}

type firstLaunchTestSecrets struct {
	values map[string][]byte
}

type firstLaunchTestSecretWriter struct {
	mu               sync.Mutex
	values           map[string][]byte
	setAttempts      int
	failSetAt        int
	afterFirstWrite  func()
	batchEntered     chan<- struct{}
	batchRelease     <-chan struct{}
	afterWriteCalled bool
	batchCalls       int
}

func (s *firstLaunchTestSecretWriter) Set(ctx context.Context, name string, value []byte) error {
	s.enterBatch()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.setLocked(ctx, name, value)
}

func (s *firstLaunchTestSecretWriter) SetBatch(ctx context.Context, values map[string][]byte, commit func() error) error {
	s.enterBatch()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.batchCalls++

	previous := make(map[string][]byte, len(values))
	missing := make(map[string]bool, len(values))
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
		value, ok := s.values[name]
		if !ok {
			missing[name] = true
			continue
		}
		previous[name] = append([]byte(nil), value...)
	}
	sort.Strings(names)
	rollback := func() {
		for _, name := range names {
			if missing[name] {
				delete(s.values, name)
				continue
			}
			s.values[name] = append([]byte(nil), previous[name]...)
		}
	}
	for _, name := range names {
		if err := s.setLocked(ctx, name, values[name]); err != nil {
			rollback()
			return err
		}
	}
	if err := commit(); err != nil {
		rollback()
		return err
	}
	return nil
}

func (s *firstLaunchTestSecretWriter) setLocked(ctx context.Context, name string, value []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.setAttempts++
	if s.failSetAt > 0 && s.setAttempts == s.failSetAt {
		return errors.New("forced secret write failure")
	}
	if s.values == nil {
		s.values = make(map[string][]byte)
	}
	s.values[name] = append([]byte(nil), value...)
	if !s.afterWriteCalled && s.afterFirstWrite != nil {
		s.afterWriteCalled = true
		s.afterFirstWrite()
	}
	return nil
}

func (s *firstLaunchTestSecretWriter) value(name string) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.values[name]...)
}

func (s *firstLaunchTestSecretWriter) enterBatch() {
	if s.batchEntered != nil {
		s.batchEntered <- struct{}{}
	}
	if s.batchRelease != nil {
		<-s.batchRelease
	}
}

func (s firstLaunchTestSecrets) Get(_ context.Context, name string) ([]byte, error) {
	value, ok := s.values[name]
	if !ok {
		return nil, bootstrapstate.ErrSecretNotFound
	}
	return append([]byte(nil), value...), nil
}

func writeSeedFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeFirstLaunchRuntimeDatabase(t *testing.T, path string, withUserState bool) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE accounts (id TEXT PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	if withUserState {
		if _, err := db.Exec("INSERT INTO accounts(id) VALUES ('account-1')"); err != nil {
			t.Fatal(err)
		}
	}
}

func writePristineGeneratedDatabase(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range []string{
		`CREATE TABLE goose_db_version (version_id INTEGER PRIMARY KEY)`,
		`INSERT INTO goose_db_version(version_id) VALUES (30)`,
		`CREATE TABLE analytics_scheduler_settings (singleton INTEGER PRIMARY KEY, enabled INTEGER NOT NULL, interval_minutes INTEGER NOT NULL, updated_at TEXT NOT NULL)`,
		`INSERT INTO analytics_scheduler_settings(singleton, enabled, interval_minutes, updated_at) VALUES (1, 1, 10, '2026-08-13T12:00:00Z')`,
		`CREATE TABLE analytics_service_words (id INTEGER PRIMARY KEY, created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`CREATE TABLE analytics_scheduler_runs (id TEXT PRIMARY KEY)`,
		`CREATE TABLE backup_history (id TEXT PRIMARY KEY)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	for id := range 88 {
		if _, err := db.Exec(
			`INSERT INTO analytics_service_words(id, created_at, updated_at) VALUES (?, '2026-08-13T12:00:00Z', '2026-08-13T12:00:00Z')`,
			id,
		); err != nil {
			t.Fatal(err)
		}
	}
}

func assertSeedFile(t *testing.T, path, want string) {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != want {
		t.Fatalf("%s = %q, want %q", path, contents, want)
	}
}

func assertNoFirstLaunchStaging(t *testing.T, targetRoot string) {
	t.Helper()
	for _, name := range []string{".first-launch-seed"} {
		_, err := os.Stat(filepath.Join(targetRoot, name))
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("bootstrap artifact %s remains: %v", name, err)
		}
	}
}
