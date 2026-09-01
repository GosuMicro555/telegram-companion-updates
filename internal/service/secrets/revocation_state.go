package secrets

import (
	"context"
	"errors"

	keyring "github.com/zalando/go-keyring"

	"telegram-companion/internal/revocation"
)

const RevocationStateSecretName = "license-revocation-state-v1"

var errRevocationStateStore = errors.New("revocation state store unavailable")

type revocationSecrets interface {
	Get(context.Context, string) ([]byte, error)
	Set(context.Context, string, []byte) error
}

// RevocationStateStore keeps rollback and terminal-receipt state in the
// platform keyring. It never deletes or rewrites any license or user data.
type RevocationStateStore struct {
	secrets revocationSecrets
}

func NewRevocationStateStore(store *SecretStore) *RevocationStateStore {
	return newRevocationStateStore(store)
}

func newRevocationStateStore(secrets revocationSecrets) *RevocationStateStore {
	return &RevocationStateStore{secrets: secrets}
}

func (store *RevocationStateStore) Load(ctx context.Context) (revocation.SecureState, error) {
	if store == nil || store.secrets == nil {
		return revocation.SecureState{}, errRevocationStateStore
	}
	if err := ctx.Err(); err != nil {
		return revocation.SecureState{}, err
	}
	encoded, err := store.secrets.Get(ctx, RevocationStateSecretName)
	if errors.Is(err, keyring.ErrNotFound) {
		return revocation.SecureState{}, nil
	}
	if err != nil {
		return revocation.SecureState{}, contextOrRevocationStateError(ctx)
	}
	state, err := revocation.UnmarshalSecureState(encoded)
	if err != nil {
		return revocation.SecureState{}, errRevocationStateStore
	}
	return state, nil
}

func (store *RevocationStateStore) Save(ctx context.Context, state revocation.SecureState) error {
	if store == nil || store.secrets == nil {
		return errRevocationStateStore
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	encoded, err := revocation.MarshalSecureState(state)
	if err != nil {
		return errRevocationStateStore
	}
	if err := store.secrets.Set(ctx, RevocationStateSecretName, encoded); err != nil {
		return contextOrRevocationStateError(ctx)
	}
	return nil
}

func contextOrRevocationStateError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return errRevocationStateStore
}
