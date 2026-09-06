//go:build desktop

package main

import (
	"errors"
	"os"
	"syscall"

	appbootstrap "telegram-companion/internal/app"
	secretservice "telegram-companion/internal/service/secrets"
)

type desktopRecoveryCode string

const (
	desktopRecoverySeedLicenseRequired desktopRecoveryCode = "seed_license_required"
	desktopRecoverySeedUnavailable     desktopRecoveryCode = "seed_unavailable"
	desktopRecoverySeedIncompatible    desktopRecoveryCode = "seed_incompatible"
	desktopRecoveryKeychainUnavailable desktopRecoveryCode = "keychain_unavailable"
	desktopRecoveryInsufficientSpace   desktopRecoveryCode = "insufficient_space"
	desktopRecoveryStorageUnavailable  desktopRecoveryCode = "storage_unavailable"
	desktopRecoveryProfileBlocked      desktopRecoveryCode = "profile_blocked"
	desktopRecoveryRuntimeUnavailable  desktopRecoveryCode = "runtime_unavailable"
	desktopRecoveryRelaunchFailed      desktopRecoveryCode = "relaunch_failed"
)

var (
	errDesktopSeedLicenseRequired = errors.New("desktop startup: seed license required")
	errDesktopSeedUnavailable     = errors.New("desktop startup: seed unavailable")
	errDesktopSeedIncompatible    = errors.New("desktop startup: seed incompatible")
	errDesktopStorageUnavailable  = errors.New("desktop startup: storage unavailable")
	errDesktopProfileBlocked      = errors.New("desktop startup: profile blocked")
)

func desktopRecoveryCodes() []desktopRecoveryCode {
	return []desktopRecoveryCode{
		desktopRecoverySeedLicenseRequired,
		desktopRecoverySeedUnavailable,
		desktopRecoverySeedIncompatible,
		desktopRecoveryKeychainUnavailable,
		desktopRecoveryInsufficientSpace,
		desktopRecoveryStorageUnavailable,
		desktopRecoveryProfileBlocked,
		desktopRecoveryRuntimeUnavailable,
		desktopRecoveryRelaunchFailed,
	}
}

func desktopRecoveryCodeForError(err error) desktopRecoveryCode {
	var insufficient *appbootstrap.InsufficientSpaceError
	var storageProbe *appbootstrap.StorageProbeError
	var pathError *os.PathError
	// Closed precedence favors the most integrity-sensitive actionable cause.
	// In particular, a failed Keychain rollback outranks ENOSPC, and ENOSPC
	// outranks a generic path failure, regardless of errors.Join order.
	switch {
	case errors.Is(err, errDesktopSeedLicenseRequired):
		return desktopRecoverySeedLicenseRequired
	case errors.Is(err, errDesktopSeedUnavailable):
		return desktopRecoverySeedUnavailable
	case errors.Is(err, errDesktopSeedIncompatible):
		return desktopRecoverySeedIncompatible
	case errors.Is(err, secretservice.ErrKeyringUnavailable):
		return desktopRecoveryKeychainUnavailable
	case errors.As(err, &insufficient):
		return desktopRecoveryInsufficientSpace
	case errors.Is(err, syscall.ENOSPC):
		return desktopRecoveryInsufficientSpace
	case errors.As(err, &storageProbe), errors.Is(err, errDesktopStorageUnavailable):
		return desktopRecoveryStorageUnavailable
	case errors.As(err, &pathError):
		return desktopRecoveryStorageUnavailable
	case errors.Is(err, errDesktopProfileBlocked):
		return desktopRecoveryProfileBlocked
	default:
		return desktopRecoveryRuntimeUnavailable
	}
}

type desktopStartupError struct {
	kind  error
	cause error
}

func (e *desktopStartupError) Error() string { return e.kind.Error() }

func (e *desktopStartupError) Unwrap() error { return e.cause }

func (e *desktopStartupError) Is(target error) bool {
	return target == e.kind || errors.Is(e.cause, target)
}

func newDesktopStartupError(kind, cause error) error {
	if cause == nil {
		return kind
	}
	return &desktopStartupError{kind: kind, cause: cause}
}
