package gotd

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/stretchr/testify/require"

	"telegram-companion/internal/domain"
)

type fakeClientRuntime struct {
	identity Identity
	err      error
	runs     int
	api      *tg.Client
	afterRun func()
}

func (f *fakeClientRuntime) Run(ctx context.Context, callback func(context.Context) error) error {
	f.runs++
	if f.err != nil {
		return f.err
	}
	err := callback(ctx)
	if f.afterRun != nil {
		f.afterRun()
	}
	return err
}

func (f *fakeClientRuntime) FullSelf(context.Context) (Identity, error) {
	return f.identity, f.err
}

func (f *fakeClientRuntime) API() *tg.Client { return f.api }

type fakeRuntimeBuilder struct{ runtime *fakeClientRuntime }

func (b fakeRuntimeBuilder) New(string) (clientRuntime, error) { return b.runtime, nil }

type validationRecord struct {
	accountID domain.ID
	at        time.Time
}

type recordingValidationStore struct{ records []validationRecord }

func (s *recordingValidationStore) MarkValidated(_ context.Context, accountID domain.ID, at time.Time) error {
	s.records = append(s.records, validationRecord{accountID: accountID, at: at})
	return nil
}

func TestClientValidationMatchesMaskedPhoneAndPersistsAfterSuccess(t *testing.T) {
	sessionPath := ownerOnlySession(t)
	runtime := &fakeClientRuntime{identity: Identity{Self: true, Phone: "15551230040", Username: "scout"}}
	records := &recordingValidationStore{}
	now := time.Date(2026, 7, 11, 9, 30, 0, 0, time.UTC)
	factory := newClientFactory(fakeRuntimeBuilder{runtime: runtime}, records, func() time.Time { return now })
	account := domain.Account{ID: "account-1", PhoneMasked: "+1 555 *** **40", SessionPath: sessionPath}

	client, err := factory.New(account)
	require.NoError(t, err)
	at, err := client.Validate(context.Background())

	require.NoError(t, err)
	require.Equal(t, now, at)
	require.Equal(t, []validationRecord{{accountID: account.ID, at: now}}, records.records)
	require.Equal(t, 1, runtime.runs)
}

func TestClientValidationRejectsWrongIdentityWithoutPersisting(t *testing.T) {
	runtime := &fakeClientRuntime{identity: Identity{Self: true, Phone: "79990000000", Username: "other"}}
	records := &recordingValidationStore{}
	factory := newClientFactory(fakeRuntimeBuilder{runtime: runtime}, records, time.Now)
	account := domain.Account{ID: "account-1", PhoneMasked: "+1 555 *** **40", SessionPath: ownerOnlySession(t)}

	client, err := factory.New(account)
	require.NoError(t, err)
	_, err = client.Validate(context.Background())

	require.ErrorContains(t, err, "identity mismatch")
	require.Empty(t, records.records)
}

func TestClientValidationUsesAccountLabelWhenPhoneMaskMissing(t *testing.T) {
	runtime := &fakeClientRuntime{identity: Identity{Self: true, Username: "expected_user", DisplayName: "Expected User"}}
	records := &recordingValidationStore{}
	factory := newClientFactory(fakeRuntimeBuilder{runtime: runtime}, records, time.Now)
	account := domain.Account{ID: "account-1", Username: "@expected_user", DisplayName: "Expected User", SessionPath: ownerOnlySession(t)}

	client, err := factory.New(account)
	require.NoError(t, err)
	_, err = client.Validate(context.Background())

	require.NoError(t, err)
	require.Len(t, records.records, 1)
}

func TestClientValidationAcceptsOpaqueDiscoveredSelfWithoutPersistingIdentity(t *testing.T) {
	runtime := &fakeClientRuntime{identity: Identity{ID: 777, Self: true, Phone: "79990001122", Username: "private_name", DisplayName: "Private Name"}}
	records := &recordingValidationStore{}
	factory := newClientFactory(fakeRuntimeBuilder{runtime: runtime}, records, time.Now)
	account := domain.Account{ID: "account-a1b2c3d4e5f6", SessionPath: ownerOnlySession(t)}

	client, err := factory.New(account)
	require.NoError(t, err)
	_, err = client.Validate(context.Background())

	require.NoError(t, err)
	require.Len(t, records.records, 1)
	require.Equal(t, account.ID, records.records[0].accountID)
	provider, ok := client.(selfUserIDProvider)
	require.True(t, ok)
	require.Equal(t, int64(777), provider.SelfUserID())
	require.Empty(t, account.PhoneMasked)
	require.Empty(t, account.Username)
	require.Empty(t, account.DisplayName)
}

func TestClientValidationRejectsMatchingNonSelfWithoutPersisting(t *testing.T) {
	runtime := &fakeClientRuntime{identity: Identity{Phone: "15551230040", Username: "scout"}}
	records := &recordingValidationStore{}
	factory := newClientFactory(fakeRuntimeBuilder{runtime: runtime}, records, time.Now)
	account := domain.Account{ID: "account-1", PhoneMasked: "+1 555 *** **40", SessionPath: ownerOnlySession(t)}

	client, err := factory.New(account)
	require.NoError(t, err)
	_, err = client.Validate(context.Background())

	require.ErrorContains(t, err, "identity mismatch")
	require.Empty(t, records.records)
}

func TestClientValidationDoesNotPersistAfterContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	runtime := &fakeClientRuntime{
		identity: Identity{Self: true, Phone: "15551230040"},
		afterRun: cancel,
	}
	records := &recordingValidationStore{}
	factory := newClientFactory(fakeRuntimeBuilder{runtime: runtime}, records, time.Now)
	account := domain.Account{ID: "account-1", PhoneMasked: "+1 555 *** **40", SessionPath: ownerOnlySession(t)}

	client, err := factory.New(account)
	require.NoError(t, err)
	_, err = client.Validate(ctx)

	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, records.records)
}

func TestClientFactoryExposesGotdAPIForManagedRuntime(t *testing.T) {
	rpc := &tg.Client{}
	runtime := &fakeClientRuntime{api: rpc}
	factory := newClientFactory(fakeRuntimeBuilder{runtime: runtime}, &recordingValidationStore{}, time.Now)

	client, err := factory.New(domain.Account{ID: "account-1", SessionPath: ownerOnlySession(t)})

	require.NoError(t, err)
	require.Same(t, rpc, client.API())
}

func TestSwitchableUpdateHandlerDispatchesLatestHandler(t *testing.T) {
	handler := newSwitchableUpdateHandler()
	calls := 0
	handler.Set(telegram.UpdateHandlerFunc(func(context.Context, tg.UpdatesClass) error {
		calls++
		return nil
	}))

	require.NoError(t, handler.Handle(context.Background(), &tg.Updates{}))
	require.Equal(t, 1, calls)
}

func TestSessionUpdateStateRequiresFreshHandlerAndExplicitResetDropsOldRouter(t *testing.T) {
	registry := newUpdateStateRegistry()
	state := registry.forSession("session")
	oldCalls := 0
	state.downstream.Set(telegram.UpdateHandlerFunc(func(context.Context, tg.UpdatesClass) error {
		oldCalls++
		return nil
	}))
	state.initialized.Store(true)

	ready := state.beginRun()
	select {
	case <-ready:
		t.Fatal("run became ready before a fresh handler was installed")
	default:
	}
	state.setHandler(telegram.UpdateHandlerFunc(func(context.Context, tg.UpdatesClass) error { return nil }))
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("fresh handler did not release update manager")
	}

	registry.ResetExplicitLifecycle()
	require.False(t, state.initialized.Load())
	require.NoError(t, state.downstream.Handle(context.Background(), &tg.Updates{}))
	require.Zero(t, oldCalls)
}

func TestClientFactoryRejectsSessionWithGroupOrOtherPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	require.NoError(t, os.WriteFile(path, []byte("synthetic"), 0o644))
	factory := newClientFactory(fakeRuntimeBuilder{runtime: &fakeClientRuntime{}}, &recordingValidationStore{}, time.Now)

	_, err := factory.New(domain.Account{ID: "account-1", SessionPath: path})

	require.ErrorContains(t, err, "owner-only")
}

func TestClientFactoryRejectsSymlinkSessionPath(t *testing.T) {
	target := ownerOnlySession(t)
	link := filepath.Join(t.TempDir(), "linked-session.json")
	require.NoError(t, os.Symlink(target, link))
	factory := newClientFactory(fakeRuntimeBuilder{runtime: &fakeClientRuntime{}}, &recordingValidationStore{}, time.Now)

	_, err := factory.New(domain.Account{ID: "account-1", SessionPath: link})

	require.ErrorContains(t, err, "symlink")
}

func ownerOnlySession(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.json")
	require.NoError(t, os.WriteFile(path, []byte("synthetic"), 0o600))
	return path
}
