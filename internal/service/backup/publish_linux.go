//go:build linux

package backup

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func renameNoReplaceAt(parentFD int, sourceName, destinationName string) error {
	err := unix.Renameat2(parentFD, sourceName, parentFD, destinationName, unix.RENAME_NOREPLACE)
	if errors.Is(err, unix.EEXIST) {
		return fmt.Errorf("%w: destination already exists", os.ErrExist)
	}
	return err
}

func descriptorFilePath(fd uintptr) string {
	return fmt.Sprintf("/proc/self/fd/%d", fd)
}
