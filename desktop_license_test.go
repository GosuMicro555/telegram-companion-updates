//go:build desktop

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReadDesktopLicenseTokenReadsSmallFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "license.tcomplicense")
	require.NoError(t, os.WriteFile(path, []byte("TCPLIC1.payload.signature\n"), 0o600))

	token, err := readDesktopLicenseToken(path)

	require.NoError(t, err)
	require.Equal(t, "TCPLIC1.payload.signature", token)
}

func TestReadDesktopLicenseTokenRejectsOversizedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "license.tcomplicense")
	require.NoError(t, os.WriteFile(path, []byte(strings.Repeat("x", desktopLicenseFileLimit+1)), 0o600))

	_, err := readDesktopLicenseToken(path)

	require.Error(t, err)
	require.NotContains(t, err.Error(), path)
}
