//go:build desktop && !darwin

package main

import (
	"context"
	"errors"
	"os"
)

const desktopRelaunchMarkerEnvironment = "_TELEGRAM_COMPANION_RELAUNCH_HANDOFF"

type desktopRelaunchCoordinator struct{}

func newDesktopRelaunchCoordinator() *desktopRelaunchCoordinator {
	return &desktopRelaunchCoordinator{}
}

func (*desktopRelaunchCoordinator) Request(context.Context) error {
	return errors.New("desktop relaunch is unavailable")
}

func (*desktopRelaunchCoordinator) Release() error { return nil }

func awaitDesktopRelaunchHandoff() error {
	if _, present := os.LookupEnv(desktopRelaunchMarkerEnvironment); !present {
		return nil
	}
	_ = os.Unsetenv(desktopRelaunchMarkerEnvironment)
	return errors.New("desktop relaunch handoff is unsupported")
}
