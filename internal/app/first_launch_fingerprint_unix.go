//go:build darwin || linux

package app

import (
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"

	"golang.org/x/sys/unix"
)

var firstLaunchOpenFingerprintRoot = unix.Open

var firstLaunchOpenFingerprintAt = unix.Openat

var firstLaunchStatFingerprintAt = unix.Fstatat

func firstLaunchFingerprintTree(root string) ([]firstLaunchTreeFingerprintEntry, error) {
	observedRoot, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	if !observedRoot.IsDir() || observedRoot.Mode()&os.ModeSymlink != 0 {
		return nil, ErrFirstLaunchSeedTargetChanged
	}
	rootFD, err := firstLaunchOpenFingerprintRoot(
		root,
		unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC,
		0,
	)
	if err != nil {
		return nil, errors.Join(ErrFirstLaunchSeedTargetChanged, err)
	}
	rootFile := os.NewFile(uintptr(rootFD), "first-launch-fingerprint-root")
	if rootFile == nil {
		return nil, errors.Join(ErrFirstLaunchSeedTargetChanged, unix.Close(rootFD))
	}
	openedRoot, err := rootFile.Stat()
	if err != nil || !openedRoot.IsDir() || !os.SameFile(observedRoot, openedRoot) {
		return nil, errors.Join(ErrFirstLaunchSeedTargetChanged, err, rootFile.Close())
	}
	fingerprint := []firstLaunchTreeFingerprintEntry{{
		path: ".", mode: openedRoot.Mode(), size: openedRoot.Size(), info: openedRoot,
	}}
	walkErr := walkFirstLaunchFingerprintDirectory(rootFile, ".", &fingerprint)
	closeErr := rootFile.Close()
	if walkErr != nil || closeErr != nil {
		return nil, errors.Join(walkErr, closeErr)
	}
	return fingerprint, nil
}

func walkFirstLaunchFingerprintDirectory(
	directory *os.File,
	relativeRoot string,
	fingerprint *[]firstLaunchTreeFingerprintEntry,
) error {
	entries, err := directory.Readdir(-1)
	if err != nil {
		return errors.Join(ErrFirstLaunchSeedTargetChanged, err)
	}
	sort.Slice(entries, func(left, right int) bool {
		return entries[left].Name() < entries[right].Name()
	})
	for _, entry := range entries {
		name := entry.Name()
		if name == "" || name == "." || name == ".." || filepath.IsAbs(name) || filepath.Base(name) != name {
			return ErrFirstLaunchSeedTargetChanged
		}
		var observed unix.Stat_t
		if err := firstLaunchStatFingerprintAt(
			int(directory.Fd()), name, &observed, unix.AT_SYMLINK_NOFOLLOW,
		); err != nil {
			return errors.Join(ErrFirstLaunchSeedTargetChanged, err)
		}
		isDirectory, isRegular := firstLaunchFingerprintUnixType(uint32(observed.Mode))
		if !isDirectory && !isRegular {
			return ErrFirstLaunchSeedTargetChanged
		}
		flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_CLOEXEC
		if isDirectory {
			flags |= unix.O_DIRECTORY
		}
		childFD, err := firstLaunchOpenFingerprintAt(int(directory.Fd()), name, flags, 0)
		if err != nil {
			return errors.Join(ErrFirstLaunchSeedTargetChanged, err)
		}
		childFile := os.NewFile(uintptr(childFD), "first-launch-fingerprint-entry")
		if childFile == nil {
			return errors.Join(ErrFirstLaunchSeedTargetChanged, unix.Close(childFD))
		}
		entryErr := appendFirstLaunchFingerprintEntry(
			childFile,
			filepath.Join(relativeRoot, name),
			observed,
			isDirectory,
			fingerprint,
		)
		closeErr := childFile.Close()
		if entryErr != nil || closeErr != nil {
			return errors.Join(entryErr, closeErr)
		}
	}
	return nil
}

func appendFirstLaunchFingerprintEntry(
	file *os.File,
	relativePath string,
	observed unix.Stat_t,
	wantDirectory bool,
	fingerprint *[]firstLaunchTreeFingerprintEntry,
) error {
	var openedStat unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &openedStat); err != nil {
		return errors.Join(ErrFirstLaunchSeedTargetChanged, err)
	}
	if !sameFirstLaunchFingerprintUnixFile(observed, openedStat) {
		return ErrFirstLaunchSeedTargetChanged
	}
	openedInfo, err := file.Stat()
	if err != nil || openedInfo.IsDir() != wantDirectory ||
		(!wantDirectory && !openedInfo.Mode().IsRegular()) {
		return errors.Join(ErrFirstLaunchSeedTargetChanged, err)
	}
	entry := firstLaunchTreeFingerprintEntry{
		path: relativePath, mode: openedInfo.Mode(), size: openedInfo.Size(), info: openedInfo,
	}
	if wantDirectory {
		*fingerprint = append(*fingerprint, entry)
		return walkFirstLaunchFingerprintDirectory(file, relativePath, fingerprint)
	}
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return errors.Join(ErrFirstLaunchSeedTargetChanged, err)
	}
	copy(entry.digest[:], digest.Sum(nil))
	*fingerprint = append(*fingerprint, entry)
	return nil
}

func firstLaunchFingerprintUnixType(mode uint32) (directory bool, regular bool) {
	switch mode & uint32(unix.S_IFMT) {
	case uint32(unix.S_IFDIR):
		return true, false
	case uint32(unix.S_IFREG):
		return false, true
	default:
		return false, false
	}
}

func sameFirstLaunchFingerprintUnixFile(left, right unix.Stat_t) bool {
	return uint64(left.Dev) == uint64(right.Dev) &&
		uint64(left.Ino) == uint64(right.Ino) &&
		uint32(left.Mode)&uint32(unix.S_IFMT) == uint32(right.Mode)&uint32(unix.S_IFMT)
}
