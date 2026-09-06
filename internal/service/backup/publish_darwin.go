//go:build darwin

package backup

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func renameNoReplaceAt(parentFD int, sourceName, destinationName string) error {
	err := unix.RenameatxNp(parentFD, sourceName, parentFD, destinationName, unix.RENAME_EXCL)
	if errors.Is(err, unix.EEXIST) {
		return fmt.Errorf("%w: destination already exists", os.ErrExist)
	}
	return err
}

func descriptorFilePath(fd uintptr) string {
	return fmt.Sprintf("/dev/fd/%d", fd)
}
