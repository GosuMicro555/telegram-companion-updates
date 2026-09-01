//go:build public_macos_arm64 && (!darwin || !arm64 || ios)

package buildinfo

// Public artifacts are supported only on Apple Silicon macOS.
var _ [0 - 1]struct{}
