//go:build !public_macos_arm64 || !darwin || !arm64 || ios

package sparkle

import (
	"context"

	"telegram-companion/internal/updater"
)

type disabledDriver struct{}

// NewDriver returns a fail-closed driver outside the public macOS ARM64 build.
// The stub contains no networking code and remains disabled even if a caller
// accidentally enables the platform-neutral updater service.
func NewDriver(string) updater.Driver {
	return disabledDriver{}
}

func (disabledDriver) Check(context.Context) (updater.Update, error) {
	return updater.Update{}, updater.ErrDisabled
}

func (disabledDriver) Download(context.Context, func(int)) error {
	return updater.ErrDisabled
}

func (disabledDriver) Install(context.Context) error {
	return updater.ErrDisabled
}

func (disabledDriver) Stop() error {
	return nil
}

var _ updater.Driver = disabledDriver{}
