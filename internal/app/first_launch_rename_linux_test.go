//go:build linux

package app

import (
	"errors"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func init() {
	renameFirstLaunchNoReplace = firstLaunchTestRenameNoReplace
	exchangeFirstLaunchPaths = firstLaunchTestExchangePaths
}

func firstLaunchTestRenameNoReplace(source, destination string) error {
	return unix.Renameat2(
		unix.AT_FDCWD, source, unix.AT_FDCWD, destination, unix.RENAME_NOREPLACE,
	)
}

func firstLaunchTestExchangePaths(left, right string) error {
	return unix.Renameat2(
		unix.AT_FDCWD, left, unix.AT_FDCWD, right, unix.RENAME_EXCHANGE,
	)
}

func TestFirstLaunchLinuxProductionRenameReturnsUnsupported(t *testing.T) {
	left := filepath.Join(t.TempDir(), "left")
	right := filepath.Join(t.TempDir(), "right")
	for name, operation := range map[string]func(string, string) error{
		"no replace": firstLaunchRenameNoReplace,
		"exchange":   firstLaunchExchangePaths,
	} {
		t.Run(name, func(t *testing.T) {
			if err := operation(left, right); !errors.Is(err, errFirstLaunchAtomicRenameUnsupported) {
				t.Fatalf("production Linux rename error = %v, want errFirstLaunchAtomicRenameUnsupported", err)
			}
		})
	}
}
