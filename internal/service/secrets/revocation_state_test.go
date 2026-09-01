package secrets

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	keyring "github.com/zalando/go-keyring"

	"telegram-companion/internal/revocation"
)

func TestRevocationStateStoreMissingAndRoundTrip(t *testing.T) {
	backend := &memoryKeyring{values: map[string]string{}}
	secrets := newSecretStore("telegram-companion", backend, bytes.NewReader(nil))
	store := NewRevocationStateStore(secrets)

	empty, err := store.Load(context.Background())
	require.NoError(t, err)
	require.Equal(t, revocation.SecureState{}, empty)

	at := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	handle, err := revocation.DeriveHandle("license-revocation-state-roundtrip")
	require.NoError(t, err)
	state := revocation.SecureState{
		Schema:          revocation.SecureStateSchema,
		HighestSequence: 4,
		LastSuccessUTC:  at,
		LastWallUTC:     at,
		ManifestDigest:  [32]byte{1},
		RevokedHandles:  []revocation.Handle{handle},
	}
	require.NoError(t, store.Save(context.Background(), state))
	loaded, err := store.Load(context.Background())
	require.NoError(t, err)
	require.Equal(t, state, loaded)

	encoded := backend.values["telegram-companion/"+RevocationStateSecretName]
	require.NotContains(t, encoded, handle.String())
}

func TestRevocationStateStoreFailsClosedWithoutLeakingBackendDetails(t *testing.T) {
	backend := &memoryKeyring{values: map[string]string{
		"telegram-companion/" + RevocationStateSecretName: base64.StdEncoding.EncodeToString([]byte(`{"schema":99}`)),
	}}
	store := NewRevocationStateStore(newSecretStore("telegram-companion", backend, bytes.NewReader(nil)))

	_, err := store.Load(context.Background())
	require.Error(t, err)
	require.NotContains(t, err.Error(), "schema")

	backend.getErr = errors.New("/Users/private/keychain license-id-secret")
	_, err = store.Load(context.Background())
	require.Error(t, err)
	for _, secret := range []string{"/Users/private", "license-id-secret"} {
		require.NotContains(t, err.Error(), secret)
	}
}

func TestRevocationStateStorePreservesNotFoundAndContextSemantics(t *testing.T) {
	accessor := &fakeRevocationSecrets{getErr: keyring.ErrNotFound}
	store := newRevocationStateStore(accessor)
	state, err := store.Load(context.Background())
	require.NoError(t, err)
	require.Equal(t, revocation.SecureState{}, state)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = store.Load(ctx)
	require.ErrorIs(t, err, context.Canceled)
	require.ErrorIs(t, store.Save(ctx, revocation.SecureState{}), context.Canceled)
	require.Equal(t, 0, accessor.setCalls)
}

func TestRevocationStateStoreRejectsNilAndWriteFailures(t *testing.T) {
	var nilStore *RevocationStateStore
	_, err := nilStore.Load(context.Background())
	require.Error(t, err)
	require.Error(t, nilStore.Save(context.Background(), revocation.SecureState{}))

	accessor := &fakeRevocationSecrets{setErr: errors.New("private backend detail")}
	store := newRevocationStateStore(accessor)
	err = store.Save(context.Background(), revocation.SecureState{})
	require.Error(t, err)
	require.NotContains(t, err.Error(), "private backend detail")
}

type fakeRevocationSecrets struct {
	value    []byte
	getErr   error
	setErr   error
	setCalls int
}

func (secrets *fakeRevocationSecrets) Get(ctx context.Context, _ string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return append([]byte(nil), secrets.value...), secrets.getErr
}

func (secrets *fakeRevocationSecrets) Set(ctx context.Context, _ string, value []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	secrets.setCalls++
	if secrets.setErr == nil {
		secrets.value = append([]byte(nil), value...)
	}
	return secrets.setErr
}
