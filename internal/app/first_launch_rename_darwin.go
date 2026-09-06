//go:build darwin

package app

import "golang.org/x/sys/unix"

var firstLaunchRenameatxNp = unix.RenameatxNp

func firstLaunchRenameNoReplace(source, destination string) error {
	return firstLaunchRenameatxNp(
		unix.AT_FDCWD, source, unix.AT_FDCWD, destination,
		uint32(unix.RENAME_EXCL|unix.RENAME_NOFOLLOW_ANY),
	)
}

func firstLaunchExchangePaths(left, right string) error {
	return firstLaunchRenameatxNp(
		unix.AT_FDCWD, left, unix.AT_FDCWD, right,
		uint32(unix.RENAME_SWAP|unix.RENAME_NOFOLLOW_ANY),
	)
}
