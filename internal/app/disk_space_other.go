//go:build !darwin

package app

import "errors"

var errFirstLaunchDiskSpaceUnsupported = errors.New("first launch seed: disk space probe unsupported")

func probeFirstLaunchDiskSpace(string) (firstLaunchDiskSpace, error) {
	return firstLaunchDiskSpace{}, errFirstLaunchDiskSpaceUnsupported
}
