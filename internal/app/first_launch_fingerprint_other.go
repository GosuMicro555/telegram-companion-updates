//go:build !darwin && !linux

package app

func firstLaunchFingerprintTree(string) ([]firstLaunchTreeFingerprintEntry, error) {
	return nil, ErrFirstLaunchSeedTargetChanged
}
