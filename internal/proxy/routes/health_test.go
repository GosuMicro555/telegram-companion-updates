package routes

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"telegram-companion/internal/domain"
)

type retrySignal time.Duration

func (r retrySignal) Error() string             { return "retry" }
func (r retrySignal) RetryAfter() time.Duration { return time.Duration(r) }

func TestRouteBackoffIsScopedAndHonorsRetrySignal(t *testing.T) {
	now := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	registry := NewRegistry(testRegistryRepo(), domain.ProxyRoute{ID: domain.SystemProxyRouteID, Enabled: true}, WithClock(func() time.Time { return now }))

	require.Equal(t, time.Second, registry.RecordFailure("route-a", errors.New("transport")))
	require.Equal(t, 2*time.Second, registry.RecordFailure("route-a", errors.New("transport")))
	require.Equal(t, 45*time.Second, registry.RecordFailure("route-b", retrySignal(45*time.Second)))

	statusA, err := registry.Status(context.Background(), "route-a")
	require.NoError(t, err)
	statusB, err := registry.Status(context.Background(), "route-b")
	require.NoError(t, err)
	require.Equal(t, now.Add(2*time.Second), *statusA.RetryAt)
	require.Equal(t, now.Add(45*time.Second), *statusB.RetryAt)
	require.Equal(t, "transport_failure", statusA.LastError)
	require.Equal(t, "retry_required", statusB.LastError)
}

func TestReconnectPacingStaggersAccountsOnRecoveredRoute(t *testing.T) {
	registry := NewRegistry(testRegistryRepo(), domain.ProxyRoute{ID: domain.SystemProxyRouteID, Enabled: true})
	require.Zero(t, registry.ReconnectDelay(0))
	require.Equal(t, 300*time.Millisecond, registry.ReconnectDelay(1))
	require.Equal(t, 2700*time.Millisecond, registry.ReconnectDelay(9))
}

func TestCheckAllSkipsDisabledProfilesAndUpdatesIndependentHealth(t *testing.T) {
	repo := testRegistryRepo()
	checked := make(map[domain.ID]int)
	registry := NewRegistry(repo, domain.ProxyRoute{ID: domain.SystemProxyRouteID, Enabled: true}, WithProbe(func(_ context.Context, route domain.ProxyRoute, _ string) error {
		checked[route.ID]++
		return nil
	}))
	require.NoError(t, registry.CheckAll(context.Background()))
	require.Equal(t, 1, checked[domain.ID("route-a")])
	require.Equal(t, 1, checked[domain.ID("route-b")])
	require.Zero(t, checked[domain.ID("disabled")])
	require.Equal(t, StateDisabled, mustRouteStatus(t, registry, "disabled").State)
}

func TestCheckAllRecoversSystemRouteAfterTemporaryFailure(t *testing.T) {
	now := time.Date(2026, 7, 28, 18, 30, 0, 0, time.UTC)
	var calls atomic.Int32
	registry := NewRegistry(testRegistryRepo(), domain.ProxyRoute{ID: domain.SystemProxyRouteID, Enabled: true},
		WithClock(func() time.Time { return now }),
		WithProbe(func(_ context.Context, route domain.ProxyRoute, _ string) error {
			if route.ID == domain.SystemProxyRouteID {
				calls.Add(1)
			}
			return nil
		}),
	)

	registry.RecordFailure(domain.SystemProxyRouteID, errors.New("temporary transport failure"))
	now = now.Add(time.Second)

	require.NoError(t, registry.CheckAll(context.Background()))
	require.Zero(t, calls.Load(), "the managed Tor route is already verified by its supervisor")
	require.Equal(t, StateReady, mustRouteStatus(t, registry, domain.SystemProxyRouteID).State)
}

func TestCheckHonorsActiveRetryAt(t *testing.T) {
	now := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	var calls atomic.Int32
	registry := NewRegistry(testRegistryRepo(), domain.ProxyRoute{ID: domain.SystemProxyRouteID, Enabled: true},
		WithClock(func() time.Time { return now }),
		WithProbe(func(context.Context, domain.ProxyRoute, string) error {
			calls.Add(1)
			return nil
		}),
	)

	registry.RecordFailure("route-a", errors.New("transport"))
	require.NoError(t, registry.Check(context.Background(), "route-a"))
	require.Zero(t, calls.Load())
	status := mustRouteStatus(t, registry, "route-a")
	require.Equal(t, StateDegraded, status.State)
	require.Equal(t, now.Add(time.Second), *status.RetryAt)

	now = now.Add(time.Second)
	require.NoError(t, registry.Check(context.Background(), "route-a"))
	require.EqualValues(t, 1, calls.Load())
	require.Equal(t, StateReady, registry.runtime("route-a").state)
	require.Equal(t, StateFull, mustRouteStatus(t, registry, "route-a").State)
}

func TestCheckCoalescesConcurrentChecksForRoute(t *testing.T) {
	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	registry := NewRegistry(testRegistryRepo(), domain.ProxyRoute{ID: domain.SystemProxyRouteID, Enabled: true}, WithProbe(func(context.Context, domain.ProxyRoute, string) error {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		return nil
	}))

	results := make(chan error, 2)
	go func() { results <- registry.Check(context.Background(), "route-a") }()
	<-started
	go func() { results <- registry.Check(context.Background(), "route-a") }()
	require.Never(t, func() bool { return calls.Load() > 1 }, 100*time.Millisecond, 5*time.Millisecond)
	close(release)
	require.NoError(t, <-results)
	require.NoError(t, <-results)
	require.EqualValues(t, 1, calls.Load())
}

func TestCheckDoesNotOverwriteNewerFailure(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	registry := NewRegistry(testRegistryRepo(), domain.ProxyRoute{ID: domain.SystemProxyRouteID, Enabled: true}, WithProbe(func(context.Context, domain.ProxyRoute, string) error {
		close(started)
		<-release
		return nil
	}))

	done := make(chan error, 1)
	go func() { done <- registry.Check(context.Background(), "route-a") }()
	<-started
	registry.RecordFailure("route-a", errors.New("newer transport failure"))
	close(release)
	require.NoError(t, <-done)

	status := mustRouteStatus(t, registry, "route-a")
	require.Equal(t, StateDegraded, status.State)
	require.Equal(t, "transport_failure", status.LastError)
	require.NotNil(t, status.RetryAt)
}

func mustRouteStatus(t *testing.T, registry *Registry, id domain.ID) Status {
	t.Helper()
	status, err := registry.Status(context.Background(), id)
	require.NoError(t, err)
	return status
}
