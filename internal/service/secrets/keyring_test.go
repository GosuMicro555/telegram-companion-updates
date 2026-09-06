package secrets

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	keyring "github.com/zalando/go-keyring"
)

type memoryKeyring struct {
	mu          sync.Mutex
	values      map[string]string
	getErr      error
	setErr      error
	setCalls    int
	setAttempts int
	failSetAt   int
}

func (m *memoryKeyring) Get(service, name string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.getErr != nil {
		return "", m.getErr
	}
	value, ok := m.values[service+"/"+name]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return value, nil
}

func (m *memoryKeyring) Set(service, name, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.setErr != nil {
		return m.setErr
	}
	m.setAttempts++
	if m.failSetAt > 0 && m.setAttempts == m.failSetAt {
		return errors.New("forced keyring write failure")
	}
	m.values[service+"/"+name] = value
	m.setCalls++
	return nil
}

func (m *memoryKeyring) Delete(service, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := service + "/" + name
	if _, ok := m.values[key]; !ok {
		return keyring.ErrNotFound
	}
	delete(m.values, key)
	return nil
}

func TestSecretStoreGetOrCreateReturnsExistingSecret(t *testing.T) {
	backend := &memoryKeyring{values: map[string]string{}}
	store := newSecretStore("telegram-companion", backend, bytes.NewReader(bytes.Repeat([]byte{0x11}, 32)))
	first, err := store.GetOrCreate(context.Background(), "message-key", 16)
	require.NoError(t, err)
	second, err := store.GetOrCreate(context.Background(), "message-key", 16)

	require.NoError(t, err)
	require.Equal(t, first, second)
	require.Len(t, first, 16)
	require.Equal(t, 1, backend.setCalls)
}

func TestSecretStoreGetOrCreateIsSingleCreationUnderConcurrency(t *testing.T) {
	backend := &memoryKeyring{values: map[string]string{}}
	store := newSecretStore("telegram-companion", backend, bytes.NewReader(bytes.Repeat([]byte{0x22}, 64)))
	const workers = 16
	results := make(chan []byte, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			secret, err := store.GetOrCreate(context.Background(), "backup-key", 32)
			results <- secret
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}
	var expected []byte
	for result := range results {
		if expected == nil {
			expected = result
		}
		require.Equal(t, expected, result)
	}
	require.Equal(t, 1, backend.setCalls)
}

func TestSecretStoreRejectsInvalidSizeAndSizeMismatch(t *testing.T) {
	backend := &memoryKeyring{values: map[string]string{}}
	store := newSecretStore("telegram-companion", backend, bytes.NewReader(bytes.Repeat([]byte{0x33}, 32)))

	_, err := store.GetOrCreate(context.Background(), "bad", 0)
	require.ErrorContains(t, err, "positive")
	_, err = store.GetOrCreate(context.Background(), "key", 16)
	require.NoError(t, err)
	_, err = store.GetOrCreate(context.Background(), "key", 32)
	require.ErrorContains(t, err, "length")
}

func TestSecretStoreHonorsCanceledContextWithoutKeyringAccess(t *testing.T) {
	backend := &memoryKeyring{values: map[string]string{}}
	store := newSecretStore("telegram-companion", backend, bytes.NewReader(bytes.Repeat([]byte{0x44}, 16)))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := store.GetOrCreate(ctx, "key", 16)

	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 0, backend.setCalls)
}

func TestSecretStoreSetAndGetRoundTrip(t *testing.T) {
	backend := &memoryKeyring{values: map[string]string{}}
	store := newSecretStore("telegram-companion", backend, bytes.NewReader(nil))
	want := []byte("master-inbox-private-value")

	require.NoError(t, store.Set(context.Background(), "master-key", want))
	got, err := store.Get(context.Background(), "master-key")

	require.NoError(t, err)
	require.Equal(t, want, got)
	require.NotEqual(t, string(want), backend.values["telegram-companion/master-key"])
}

func TestSecretStoreGetPreservesNotFound(t *testing.T) {
	backend := &memoryKeyring{values: map[string]string{}}
	store := newSecretStore("telegram-companion", backend, bytes.NewReader(nil))

	_, err := store.Get(context.Background(), "missing")

	require.ErrorIs(t, err, keyring.ErrNotFound)
}

func TestSecretStoreClassifiesKeyringFailuresWithoutRenderingSensitiveContext(t *testing.T) {
	raw := errors.New("raw-keychain-cause account-secret /Users/private/license")
	backend := &memoryKeyring{values: map[string]string{}, getErr: newKeyringUnavailableError(raw)}
	store := newSecretStore("account-service-private", backend, bytes.NewReader(nil))

	_, err := store.Get(context.Background(), "telegram-account-credentials-v1")

	require.ErrorIs(t, err, ErrKeyringUnavailable)
	var typed *KeyringUnavailableError
	require.ErrorAs(t, err, &typed)
	for _, forbidden := range []string{
		"raw-keychain-cause", "account-secret", "/Users/private", "license",
		"account-service-private", "telegram-account-credentials-v1",
	} {
		require.NotContains(t, err.Error(), forbidden)
	}
}

func TestSecretStoreSetBatchRollsBackAllWritesOnFailure(t *testing.T) {
	oldScout := base64.StdEncoding.EncodeToString([]byte("old-scout-secret"))
	backend := &memoryKeyring{
		values: map[string]string{
			"telegram-companion/scout-message-key": oldScout,
		},
		failSetAt: 2,
	}
	store := newSecretStore("telegram-companion", backend, bytes.NewReader(nil))

	err := store.SetBatch(context.Background(), map[string][]byte{
		"scout-message-key":               []byte("new-scout-secret"),
		"telegram-account-credentials-v1": []byte("new-account-secret"),
	}, func() error { return nil })

	require.ErrorContains(t, err, "forced keyring write failure")
	require.Equal(t, oldScout, backend.values["telegram-companion/scout-message-key"])
	require.NotContains(t, backend.values, "telegram-companion/telegram-account-credentials-v1")
}

func TestSecretStoreSetBatchRollsBackWhenCommitFails(t *testing.T) {
	backend := &memoryKeyring{values: map[string]string{}}
	store := newSecretStore("telegram-companion", backend, bytes.NewReader(nil))
	commitErr := errors.New("forced data commit failure")

	err := store.SetBatch(context.Background(), map[string][]byte{
		"scout-message-key": []byte("new-scout-secret"),
	}, func() error { return commitErr })

	require.ErrorIs(t, err, commitErr)
	require.NotContains(t, backend.values, "telegram-companion/scout-message-key")
}

func TestSecretStoreSetBatchSerializesStoresForTheSameService(t *testing.T) {
	backend := &memoryKeyring{values: map[string]string{}}
	first := newSecretStore("telegram-companion", backend, bytes.NewReader(nil))
	second := newSecretStore("telegram-companion", backend, bytes.NewReader(nil))
	firstCommit := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- first.SetBatch(context.Background(), map[string][]byte{
			"scout-message-key": []byte("first"),
		}, func() error {
			close(firstCommit)
			<-releaseFirst
			return nil
		})
	}()
	select {
	case <-firstCommit:
	case <-time.After(5 * time.Second):
		t.Fatal("first batch did not reach commit")
	}

	secondDone := make(chan error, 1)
	go func() {
		secondDone <- second.SetBatch(context.Background(), map[string][]byte{
			"scout-message-key": []byte("second"),
		}, func() error { return nil })
	}()
	select {
	case err := <-secondDone:
		close(releaseFirst)
		t.Fatalf("second batch completed before first commit: %v", err)
	case <-time.After(250 * time.Millisecond):
	}
	close(releaseFirst)
	require.NoError(t, <-firstDone)
	require.NoError(t, <-secondDone)
	got, err := second.Get(context.Background(), "scout-message-key")
	require.NoError(t, err)
	require.Equal(t, []byte("second"), got)
}
