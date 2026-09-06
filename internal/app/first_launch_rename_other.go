//go:build !darwin && !linux

package app

import "errors"

var errFirstLaunchAtomicRenameUnsupported = errors.New("first launch seed: atomic rename is unsupported")

func firstLaunchRenameNoReplace(string, string) error {
	return errFirstLaunchAtomicRenameUnsupported
}

func firstLaunchExchangePaths(string, string) error {
	return errFirstLaunchAtomicRenameUnsupported
}
