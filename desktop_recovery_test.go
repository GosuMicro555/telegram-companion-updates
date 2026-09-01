//go:build desktop

package main

import (
	"errors"
	"os"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"

	appbootstrap "telegram-companion/internal/app"
	secretservice "telegram-companion/internal/service/secrets"
)

func TestDesktopRecoveryCodeClassifiesErrorsWithoutStringMatchingOrRenderingCauses(t *testing.T) {
	raw := errors.New("raw-cause /Users/private/license account-secret")
	tests := []struct {
		name string
		err  error
		want desktopRecoveryCode
	}{
		{name: "license grant", err: errors.Join(errDesktopSeedLicenseRequired, raw), want: desktopRecoverySeedLicenseRequired},
		{name: "missing seed", err: errors.Join(errDesktopSeedUnavailable, raw), want: desktopRecoverySeedUnavailable},
		{name: "incompatible seed", err: errors.Join(errDesktopSeedIncompatible, raw), want: desktopRecoverySeedIncompatible},
		{name: "keychain", err: errors.Join(secretservice.ErrKeyringUnavailable, raw), want: desktopRecoveryKeychainUnavailable},
		{name: "space", err: errors.Join(&appbootstrap.InsufficientSpaceError{}, raw), want: desktopRecoveryInsufficientSpace},
		{name: "storage probe", err: errors.Join(&appbootstrap.StorageProbeError{}, raw), want: desktopRecoveryStorageUnavailable},
		{name: "storage", err: errors.Join(errDesktopStorageUnavailable, raw), want: desktopRecoveryStorageUnavailable},
		{name: "storage path error", err: &os.PathError{Op: "open", Path: "/Users/private/license", Err: syscall.EIO}, want: desktopRecoveryStorageUnavailable},
		{name: "disk full", err: &os.PathError{Op: "write", Path: "/Users/private/data", Err: syscall.ENOSPC}, want: desktopRecoveryInsufficientSpace},
		{name: "profile", err: errors.Join(errDesktopProfileBlocked, raw), want: desktopRecoveryProfileBlocked},
		{name: "unknown runtime", err: raw, want: desktopRecoveryRuntimeUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := desktopRecoveryCodeForError(test.err)
			require.Equal(t, test.want, got)
			for _, forbidden := range []string{"raw-cause", "/Users/private", "account-secret"} {
				require.NotContains(t, string(got), forbidden)
			}
		})
	}
}

func TestDesktopRecoveryCodesAreClosed(t *testing.T) {
	require.Equal(t, []desktopRecoveryCode{
		desktopRecoverySeedLicenseRequired,
		desktopRecoverySeedUnavailable,
		desktopRecoverySeedIncompatible,
		desktopRecoveryKeychainUnavailable,
		desktopRecoveryInsufficientSpace,
		desktopRecoveryStorageUnavailable,
		desktopRecoveryProfileBlocked,
		desktopRecoveryRuntimeUnavailable,
		desktopRecoveryRelaunchFailed,
	}, desktopRecoveryCodes())
}

func TestDesktopRecoveryCodeUsesClosedPrecedenceForJoinedKnownCauses(t *testing.T) {
	diskFull := &os.PathError{Op: "write", Path: "/Users/private/data", Err: syscall.ENOSPC}
	genericPath := &os.PathError{Op: "open", Path: "/Users/private/license", Err: syscall.EIO}
	tests := []struct {
		name string
		err  error
		want desktopRecoveryCode
	}{
		{
			name: "keychain outranks disk full",
			err:  errors.Join(diskFull, secretservice.ErrKeyringUnavailable),
			want: desktopRecoveryKeychainUnavailable,
		},
		{
			name: "keychain outranks typed insufficient space",
			err:  errors.Join(&appbootstrap.InsufficientSpaceError{}, secretservice.ErrKeyringUnavailable),
			want: desktopRecoveryKeychainUnavailable,
		},
		{
			name: "disk full outranks generic path failure",
			err:  errors.Join(genericPath, diskFull),
			want: desktopRecoveryInsufficientSpace,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, desktopRecoveryCodeForError(test.err))
		})
	}
}
