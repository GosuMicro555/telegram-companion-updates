//go:build !linux && !darwin

package sqlite

import (
	"context"
	"errors"
)

type SystemDiskProbe struct{}

func (SystemDiskProbe) AvailableBytes(context.Context, string) (int64, error) {
	return 0, errors.New("disk probe is unsupported on this platform")
}
