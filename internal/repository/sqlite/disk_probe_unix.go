//go:build linux || darwin

package sqlite

import (
	"context"
	"math"
	"path/filepath"

	"golang.org/x/sys/unix"
)

type SystemDiskProbe struct{}

func (SystemDiskProbe) AvailableBytes(ctx context.Context, path string) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	var stat unix.Statfs_t
	if err := unix.Statfs(filepath.Dir(path), &stat); err != nil {
		return 0, err
	}
	if stat.Bsize == 0 || stat.Bavail > uint64(math.MaxInt64)/uint64(stat.Bsize) {
		return math.MaxInt64, nil
	}
	return int64(stat.Bavail * uint64(stat.Bsize)), nil
}

var _ DiskProbe = SystemDiskProbe{}
