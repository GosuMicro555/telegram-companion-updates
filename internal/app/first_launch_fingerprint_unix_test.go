//go:build darwin || linux

package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestFirstLaunchFingerprintUsesDescriptorRelativeClosedUnixTraversal(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(filepath.Join(root, "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sessions", "profile.db"), []byte("profile"), 0o600); err != nil {
		t.Fatal(err)
	}

	previousOpenRoot := firstLaunchOpenFingerprintRoot
	previousOpenAt := firstLaunchOpenFingerprintAt
	previousStatAt := firstLaunchStatFingerprintAt
	t.Cleanup(func() {
		firstLaunchOpenFingerprintRoot = previousOpenRoot
		firstLaunchOpenFingerprintAt = previousOpenAt
		firstLaunchStatFingerprintAt = previousStatAt
	})
	var rootFlags int
	firstLaunchOpenFingerprintRoot = func(path string, flags int, mode uint32) (int, error) {
		rootFlags = flags
		return previousOpenRoot(path, flags, mode)
	}
	type openCall struct {
		name  string
		flags int
	}
	var openCalls []openCall
	firstLaunchOpenFingerprintAt = func(dirfd int, name string, flags int, mode uint32) (int, error) {
		if filepath.Base(name) != name || filepath.IsAbs(name) {
			t.Fatalf("descriptor-relative open name = %q, want one leaf", name)
		}
		openCalls = append(openCalls, openCall{name: name, flags: flags})
		return previousOpenAt(dirfd, name, flags, mode)
	}
	var statCalls []string
	firstLaunchStatFingerprintAt = func(dirfd int, name string, stat *unix.Stat_t, flags int) error {
		if filepath.Base(name) != name || filepath.IsAbs(name) {
			t.Fatalf("descriptor-relative stat name = %q, want one leaf", name)
		}
		if flags != unix.AT_SYMLINK_NOFOLLOW {
			t.Fatalf("fstatat flags = %#x, want exactly AT_SYMLINK_NOFOLLOW", flags)
		}
		statCalls = append(statCalls, name)
		return previousStatAt(dirfd, name, stat, flags)
	}

	if _, err := fingerprintFirstLaunchTree(root); err != nil {
		t.Fatalf("fingerprintFirstLaunchTree() error = %v", err)
	}

	wantRootFlags := unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_CLOEXEC
	if rootFlags != wantRootFlags {
		t.Fatalf("root fingerprint open flags = %#x, want exactly %#x", rootFlags, wantRootFlags)
	}
	wantOpenCalls := []openCall{
		{name: "sessions", flags: wantRootFlags},
		{name: "profile.db", flags: unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_CLOEXEC},
	}
	if !reflect.DeepEqual(openCalls, wantOpenCalls) {
		t.Fatalf("descriptor-relative open calls = %#v, want %#v", openCalls, wantOpenCalls)
	}
	if want := []string{"sessions", "profile.db"}; !reflect.DeepEqual(statCalls, want) {
		t.Fatalf("descriptor-relative stat calls = %v, want %v", statCalls, want)
	}
}

func TestFingerprintFirstLaunchTreeRejectsDescriptorRaceWithoutFollowingOrBlocking(t *testing.T) {
	tests := []struct {
		name           string
		kind           string
		triggerName    string
		mayBlock       bool
		expectNoFollow bool
	}{
		{
			name: "regular replaced by symlink to outside fifo", kind: "file-symlink",
			triggerName: "app.db", mayBlock: true, expectNoFollow: true,
		},
		{
			name: "regular replaced by fifo", kind: "file-fifo",
			triggerName: "app.db", mayBlock: true,
		},
		{
			name: "regular replaced by identical regular inode", kind: "file-regular",
			triggerName: "app.db",
		},
		{
			name: "ancestor directory replaced by symlink to outside fifo tree", kind: "directory-symlink",
			triggerName: "sessions", mayBlock: true, expectNoFollow: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dataRoot := filepath.Join(t.TempDir(), "data")
			if err := os.MkdirAll(dataRoot, 0o700); err != nil {
				t.Fatal(err)
			}
			databasePath := filepath.Join(dataRoot, "app.db")
			observedPath := databasePath + ".observed"
			if test.kind == "directory-symlink" {
				databasePath = filepath.Join(dataRoot, "sessions", "profile.db")
				observedPath = filepath.Join(dataRoot, "sessions.observed")
				if err := os.MkdirAll(filepath.Dir(databasePath), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(databasePath, []byte("observed-profile"), 0o600); err != nil {
				t.Fatal(err)
			}
			outsideRoot := t.TempDir()
			outsideFIFO := filepath.Join(outsideRoot, "profile.db")
			if err := unix.Mkfifo(outsideFIFO, 0o600); err != nil {
				t.Fatal(err)
			}
			outsideInfo, err := os.Lstat(outsideFIFO)
			if err != nil {
				t.Fatal(err)
			}

			previousOpenAt := firstLaunchOpenFingerprintAt
			t.Cleanup(func() { firstLaunchOpenFingerprintAt = previousOpenAt })
			raceInjected := make(chan struct{}, 1)
			var injectionErr error
			var rawOpenErr error
			injected := false
			firstLaunchOpenFingerprintAt = func(dirfd int, name string, flags int, mode uint32) (int, error) {
				if name == test.triggerName && !injected {
					injected = true
					injectionErr = injectFirstLaunchFingerprintRaceForTest(
						test.kind, dataRoot, databasePath, outsideRoot,
					)
					raceInjected <- struct{}{}
					if injectionErr != nil {
						return -1, fmt.Errorf("inject fingerprint race: %w", injectionErr)
					}
				}
				fd, openErr := previousOpenAt(dirfd, name, flags, mode)
				rawOpenErr = openErr
				return fd, openErr
			}

			result := make(chan error, 1)
			go func() {
				_, fingerprintErr := fingerprintFirstLaunchTree(dataRoot)
				result <- fingerprintErr
			}()
			select {
			case <-raceInjected:
			case fingerprintErr := <-result:
				t.Fatalf("fingerprint returned before injecting the descriptor race: %v", fingerprintErr)
			case <-time.After(time.Second):
				t.Fatal("fingerprint did not reach the descriptor open seam")
			}

			var fingerprintErr error
			select {
			case fingerprintErr = <-result:
			case <-time.After(500 * time.Millisecond):
				if !test.mayBlock {
					t.Fatal("fingerprint exceeded the bounded identity-race check")
				}
				unblockFirstLaunchFingerprintFIFOForTest(t, outsideFIFO, databasePath, test.kind)
				select {
				case <-result:
				case <-time.After(500 * time.Millisecond):
				}
				t.Fatal("fingerprint blocked while a file or ancestor was replaced by a symlink or FIFO")
			}
			if injectionErr != nil {
				t.Fatalf("inject descriptor race: %v", injectionErr)
			}
			if !errors.Is(fingerprintErr, ErrFirstLaunchSeedTargetChanged) {
				t.Fatalf("fingerprint error = %v, want ErrFirstLaunchSeedTargetChanged", fingerprintErr)
			}
			if test.expectNoFollow && !errors.Is(rawOpenErr, unix.ELOOP) && !errors.Is(rawOpenErr, unix.ENOTDIR) {
				t.Fatalf("nofollow descriptor open error = %v, want ELOOP or ENOTDIR", rawOpenErr)
			}
			outsideInfoAfter, err := os.Lstat(outsideFIFO)
			if err != nil {
				t.Fatal(err)
			}
			if !os.SameFile(outsideInfo, outsideInfoAfter) {
				t.Fatal("outside FIFO identity changed during fingerprint")
			}
			if _, err := os.Lstat(observedPath); err != nil {
				t.Fatalf("observed file/tree was not preserved after fingerprint race: %v", err)
			}
		})
	}
}

func injectFirstLaunchFingerprintRaceForTest(kind, dataRoot, databasePath, outsideRoot string) error {
	switch kind {
	case "file-symlink":
		if err := os.Rename(databasePath, databasePath+".observed"); err != nil {
			return err
		}
		return os.Symlink(filepath.Join(outsideRoot, "profile.db"), databasePath)
	case "file-fifo":
		if err := os.Rename(databasePath, databasePath+".observed"); err != nil {
			return err
		}
		return unix.Mkfifo(databasePath, 0o600)
	case "file-regular":
		contents, err := os.ReadFile(databasePath)
		if err != nil {
			return err
		}
		info, err := os.Lstat(databasePath)
		if err != nil {
			return err
		}
		if err := os.Rename(databasePath, databasePath+".observed"); err != nil {
			return err
		}
		return os.WriteFile(databasePath, contents, info.Mode().Perm())
	case "directory-symlink":
		sessions := filepath.Join(dataRoot, "sessions")
		if err := os.Rename(sessions, sessions+".observed"); err != nil {
			return err
		}
		return os.Symlink(outsideRoot, sessions)
	default:
		return fmt.Errorf("unknown fingerprint race %q", kind)
	}
}

func unblockFirstLaunchFingerprintFIFOForTest(
	t *testing.T,
	outsideFIFO string,
	databasePath string,
	kind string,
) {
	t.Helper()
	unblockPath := outsideFIFO
	if kind == "file-fifo" {
		unblockPath = databasePath
	}
	var fd int
	var err error
	deadline := time.Now().Add(250 * time.Millisecond)
	for {
		fd, err = unix.Open(unblockPath, unix.O_WRONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
		if err == nil || !errors.Is(err, unix.ENXIO) || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("unblock buggy fingerprint open: %v", err)
	}
	if _, err = unix.Write(fd, []byte("x")); err != nil {
		_ = unix.Close(fd)
		t.Fatalf("write unblock byte: %v", err)
	}
	if err = unix.Close(fd); err != nil {
		t.Fatalf("close unblock writer: %v", err)
	}
}
