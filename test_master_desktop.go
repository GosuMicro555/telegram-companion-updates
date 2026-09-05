//go:build desktop && tdata_test_master && !public_macos_arm64

package main

import (
	"os"
	"path/filepath"
)

// This build-only variant opens beside the installed master with its own profile,
// keychain namespace and single-instance lock. It never migrates the main profile.
func init() {
	root, err := os.UserConfigDir()
	if err != nil {
		panic("test master config directory unavailable")
	}
	if err = os.Setenv("TELEGRAM_COMPANION_DATA_ROOT", filepath.Join(root, "Telegram Companion Test Master")); err != nil {
		panic("test master profile unavailable")
	}
	desktopSecretService = "telegram-companion-test-master"
	desktopWindowTitle = "Test Master Version"
	desktopSingleInstanceID = "f6d0e571-a25c-4f98-8bcd-884a77539154"
}
