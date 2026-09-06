//go:build desktop

package main

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"telegram-companion/internal/buildinfo"
)

func TestResolveBuildDesktopPathsUsesConfiguredRootForPublicMacOS(t *testing.T) {
	info := buildinfo.Info{Channel: buildinfo.ChannelPublicMacOSARM64}
	paths, err := resolveBuildDesktopPaths(info, filepath.Join("/ignored", "override"), "/Users/operator/Library/Application Support", "/Applications/Telegram Companion.app/Contents/MacOS/telegram-companion")
	require.NoError(t, err)
	require.Equal(t, filepath.Join("/Users/operator/Library/Application Support", "Telegram Companion"), paths.root)
}

func TestResolveBuildDesktopPathsPreservesInternalOverride(t *testing.T) {
	info := buildinfo.Info{Channel: buildinfo.ChannelInternal}
	paths, err := resolveBuildDesktopPaths(info, "/srv/telegram-companion", "/Users/operator/Library/Application Support", "/opt/telegram-companion")
	require.NoError(t, err)
	require.Equal(t, "/srv/telegram-companion", paths.root)
}
