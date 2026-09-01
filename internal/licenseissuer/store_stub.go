//go:build !windows

package licenseissuer

import (
	"crypto/ed25519"
)

// StoreError is a stable, non-sensitive state-store error classification.
type StoreError string

func (e StoreError) Error() string { return "licenseissuer: " + string(e) }

func (e StoreError) Is(target error) bool {
	other, ok := target.(StoreError)
	return ok && e == other
}

const (
	ErrStoreUnsupported StoreError = "state store is unsupported on this platform"
	ErrStateNotFound    StoreError = "generator state not found"
	ErrInvalidState     StoreError = "invalid generator state"
	ErrStateStorage     StoreError = "generator state storage failed"
)

// GeneratorState is the in-memory form of the Windows generator state. The
// private key is never serialized directly by the Windows implementation.
type GeneratorState struct {
	PrivateKey      ed25519.PrivateKey
	PublicKey       ed25519.PublicKey
	BackupConfirmed bool
	History         []HistoryEntry
}

// StateStore exists on non-Windows systems only to keep shared code compile-safe.
type StateStore struct{}

func DefaultStateRoot() (string, error) { return "", ErrStoreUnsupported }

func NewStateStore(string) (*StateStore, error) { return nil, ErrStoreUnsupported }

func (*StateStore) Path() string { return "" }

func (*StateStore) Load() (GeneratorState, error) {
	return GeneratorState{}, ErrStoreUnsupported
}

func (*StateStore) Save(GeneratorState) error { return ErrStoreUnsupported }
