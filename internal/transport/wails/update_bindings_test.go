package wails

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"telegram-companion/internal/updater"
)

type updateServiceStub struct {
	snapshot updater.Snapshot
	check    func(context.Context) (updater.Snapshot, error)
	download func(context.Context) (updater.Snapshot, error)
	install  func(context.Context) (updater.Snapshot, error)
	started  context.Context
	stopped  int
}

func (s *updateServiceStub) Snapshot() updater.Snapshot { return s.snapshot }
func (s *updateServiceStub) Start(ctx context.Context)  { s.started = ctx }
func (s *updateServiceStub) Stop()                      { s.stopped++ }
func (s *updateServiceStub) CheckNow(ctx context.Context) (updater.Snapshot, error) {
	return s.check(ctx)
}
func (s *updateServiceStub) Download(ctx context.Context) (updater.Snapshot, error) {
	return s.download(ctx)
}
func (s *updateServiceStub) Install(ctx context.Context) (updater.Snapshot, error) {
	return s.install(ctx)
}

func TestUpdateBindingsExposeImmutableSafeStatus(t *testing.T) {
	service := &updateServiceStub{snapshot: updater.Snapshot{
		State: updater.StateError, RetryState: updater.StateAvailable, Version: "0.7.0", Progress: 42,
		Error: "/Users/private/GitHub-token",
	}}
	bindings := NewUpdateBindings(service, nil)

	got := bindings.GetUpdateStatus()

	require.Equal(t, "error", got.State)
	require.Equal(t, "available", got.RetryState)
	require.Equal(t, "0.7.0", got.Version)
	require.Equal(t, 42, got.Progress)
	require.Equal(t, "update_failed", got.ErrorCode)
	require.NotContains(t, got.ErrorCode, "Users")
}

func TestCheckForUpdatesUsesRootContextAndPublishesStatus(t *testing.T) {
	type contextKey string
	const key contextKey = "root"
	service := &updateServiceStub{}
	service.check = func(ctx context.Context) (updater.Snapshot, error) {
		require.Equal(t, "activation", ctx.Value(key))
		return updater.Snapshot{State: updater.StateAvailable, Version: "0.7.0"}, nil
	}
	var events []UpdateStatusDTO
	bindings := NewUpdateBindings(service, func(event string, payload UpdateStatusDTO) {
		require.Equal(t, updateStatusEvent, event)
		events = append(events, payload)
	})
	root := context.WithValue(context.Background(), key, "activation")
	bindings.Startup(root)

	got, err := bindings.CheckForUpdates()

	require.NoError(t, err)
	require.Equal(t, "available", got.State)
	require.Equal(t, "0.7.0", got.Version)
	require.Equal(t, []UpdateStatusDTO{got}, events)
	require.Same(t, root, service.started)
}

func TestUpdateActionsReturnOnlySafeErrors(t *testing.T) {
	service := &updateServiceStub{}
	service.check = func(context.Context) (updater.Snapshot, error) {
		return updater.Snapshot{State: updater.StateError, Error: "secret proxy password"}, errors.New("/private/path")
	}
	bindings := NewUpdateBindings(service, nil)

	got, err := bindings.CheckForUpdates()

	require.EqualError(t, err, "update operation failed")
	require.Equal(t, "error", got.State)
	require.Equal(t, "update_failed", got.ErrorCode)
	require.NotContains(t, err.Error(), "private")
}

func TestUpdateBindingsSurfaceDriverFailureStoredInSnapshot(t *testing.T) {
	service := &updateServiceStub{}
	service.check = func(context.Context) (updater.Snapshot, error) {
		return updater.Snapshot{State: updater.StateError, Error: "native details"}, nil
	}
	bindings := NewUpdateBindings(service, nil)

	got, err := bindings.CheckForUpdates()

	require.EqualError(t, err, "update operation failed")
	require.Equal(t, "error", got.State)
	require.Equal(t, "update_failed", got.ErrorCode)
}

func TestUpdateBindingsExposeOnlyAllowlistedDiagnosticCode(t *testing.T) {
	service := &updateServiceStub{snapshot: updater.Snapshot{
		State: updater.StateError, RetryState: updater.StateReady,
		Error: "/private/native/path", ErrorCode: string(updater.CodeInstallFailed),
	}}
	bindings := NewUpdateBindings(service, nil)

	got := bindings.GetUpdateStatus()
	require.Equal(t, "update_install_failed", got.ErrorCode)
	require.NotContains(t, got.ErrorCode, "private")

	service.snapshot.ErrorCode = "native_secret_path"
	got = bindings.GetUpdateStatus()
	require.Equal(t, "update_failed", got.ErrorCode)
}

func TestUpdateBindingsStopServiceOnce(t *testing.T) {
	service := &updateServiceStub{}
	bindings := NewUpdateBindings(service, nil)

	bindings.Shutdown()
	bindings.Shutdown()

	require.Equal(t, 1, service.stopped)
}

func TestUpdateBindingsTreatTypedNilServiceAsDisabled(t *testing.T) {
	var service *updater.Service
	bindings := NewUpdateBindings(service, nil)

	require.NotPanics(t, func() { bindings.Startup(context.Background()) })
	require.Equal(t, string(updater.StateDisabled), bindings.GetUpdateStatus().State)
	require.NotPanics(t, bindings.Shutdown)
}
