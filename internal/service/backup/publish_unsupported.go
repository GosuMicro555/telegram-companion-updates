//go:build !linux && !darwin

package backup

import (
	"context"
	"errors"
)

type unsupportedPublishTarget struct{}

func prepareAtomicPublish(string) (publishTarget, error) {
	return nil, errors.New("descriptor-relative publication is unsupported on this platform")
}

func (*unsupportedPublishTarget) CreateTempDir(string) (string, restoreWorkspace, error) {
	return "", nil, errors.New("descriptor-relative publication is unsupported on this platform")
}

func (*unsupportedPublishTarget) Publish(context.Context, string) error {
	return errors.New("descriptor-relative publication is unsupported on this platform")
}

func (*unsupportedPublishTarget) RemoveTemp(string) error { return nil }
func (*unsupportedPublishTarget) Close() error            { return nil }

func atomicPublishNoReplace(ctx context.Context, source, destination string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return errors.New("atomic no-replace publication is unsupported on this platform")
}

func descriptorFilePath(uintptr) string { return "" }
