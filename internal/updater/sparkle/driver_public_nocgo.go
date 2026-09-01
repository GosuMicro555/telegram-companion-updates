//go:build public_macos_arm64 && darwin && arm64 && !ios && !cgo

package sparkle

// A public updater must never silently fall back to a non-native implementation.
// Sparkle's Objective-C bridge requires CGO, so fail the release build closed.
var publicMacOSARM64UpdaterRequiresCGO [0 - 1]int
