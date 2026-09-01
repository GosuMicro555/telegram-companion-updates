//go:build darwin

package app

import "golang.org/x/sys/unix"

func probeFirstLaunchDiskSpace(path string) (firstLaunchDiskSpace, error) {
	var statistics unix.Statfs_t
	if err := unix.Statfs(path, &statistics); err != nil {
		return firstLaunchDiskSpace{}, err
	}
	blockSize := uint64(statistics.Bsize)
	available, ok := firstLaunchAvailableBytes(uint64(statistics.Bavail), blockSize)
	if !ok || blockSize == 0 {
		return firstLaunchDiskSpace{}, errInvalidDiskSpaceProbe
	}
	return firstLaunchDiskSpace{AvailableBytes: available, BlockSize: blockSize}, nil
}
