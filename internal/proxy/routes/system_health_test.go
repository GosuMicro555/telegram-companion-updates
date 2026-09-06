package routes

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"telegram-companion/internal/domain"
)

func TestSystemRouteFollowsSupervisorHealth(t *testing.T) {
	ready := false
	registry := NewRegistry(nil, domain.ProxyRoute{
		ID:      domain.SystemProxyRouteID,
		Name:    "Managed proxy",
		Enabled: true,
	}, WithSystemHealth(func() bool { return ready }))
	account := domain.Account{ID: "system-account", ProxyMode: domain.ProxyModeGlobal}

	status, err := registry.Status(context.Background(), domain.SystemProxyRouteID)
	require.NoError(t, err)
	require.Equal(t, StateDegraded, status.State)
	require.Equal(t, "managed_proxy_unavailable", status.LastError)
	_, err = registry.RouteFor(context.Background(), account)
	require.ErrorIs(t, err, ErrRouteUnavailable)

	ready = true
	require.NoError(t, registry.CheckAll(context.Background()))
	status, err = registry.Status(context.Background(), domain.SystemProxyRouteID)
	require.NoError(t, err)
	require.Equal(t, StateReady, status.State)
	_, err = registry.RouteFor(context.Background(), account)
	require.NoError(t, err)

	ready = false
	require.NoError(t, registry.CheckAll(context.Background()))
	status, err = registry.Status(context.Background(), domain.SystemProxyRouteID)
	require.NoError(t, err)
	require.Equal(t, StateDegraded, status.State)
	require.Equal(t, "managed_proxy_unavailable", status.LastError)
	_, err = registry.RouteFor(context.Background(), account)
	require.ErrorIs(t, err, ErrRouteUnavailable)
}

func TestSystemRouteRefreshesSupervisorHealthWithoutCheckAll(t *testing.T) {
	ready := true
	registry := NewRegistry(nil, domain.ProxyRoute{
		ID: domain.SystemProxyRouteID, Name: "Managed proxy", Enabled: true,
	}, WithSystemHealth(func() bool { return ready }))
	account := domain.Account{ID: "system-account", ProxyMode: domain.ProxyModeGlobal}

	status, err := registry.Status(context.Background(), domain.SystemProxyRouteID)
	require.NoError(t, err)
	require.Equal(t, StateReady, status.State)

	ready = false
	_, err = registry.RouteFor(context.Background(), account)
	require.ErrorIs(t, err, ErrRouteUnavailable)
	status, err = registry.Status(context.Background(), domain.SystemProxyRouteID)
	require.NoError(t, err)
	require.Equal(t, StateDegraded, status.State)
	require.Equal(t, "managed_proxy_unavailable", status.LastError)

	ready = true
	route, err := registry.RouteFor(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, domain.SystemProxyRouteID, route.ID)
	status, err = registry.Status(context.Background(), domain.SystemProxyRouteID)
	require.NoError(t, err)
	require.Equal(t, StateReady, status.State)
	require.Empty(t, status.LastError)
}

func TestSystemRouteDropsStaleOverlappingHealthObservation(t *testing.T) {
	var calls atomic.Int32
	calls.Store(-1) // NewRegistry performs one initial observation.
	firstStarted := make(chan struct{})
	secondStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	health := func() bool {
		switch calls.Add(1) {
		case 0:
			return false
		case 1:
			close(firstStarted)
			<-releaseFirst
			return true
		case 2:
			close(secondStarted)
			return false
		default:
			return false
		}
	}
	registry := NewRegistry(nil, domain.ProxyRoute{
		ID: domain.SystemProxyRouteID, Name: "Managed proxy", Enabled: true,
	}, WithSystemHealth(health))
	account := domain.Account{ID: "system-account", ProxyMode: domain.ProxyModeGlobal}

	firstResult := make(chan error, 1)
	go func() {
		_, err := registry.RouteFor(context.Background(), account)
		firstResult <- err
	}()
	<-firstStarted

	secondResult := make(chan Status, 1)
	go func() {
		status, _ := registry.Status(context.Background(), domain.SystemProxyRouteID)
		secondResult <- status
	}()
	<-secondStarted
	close(releaseFirst)

	status := <-secondResult
	<-firstResult
	require.Equal(t, StateDegraded, status.State)
	require.Equal(t, "managed_proxy_unavailable", status.LastError)
	status, err := registry.Status(context.Background(), domain.SystemProxyRouteID)
	require.NoError(t, err)
	require.Equal(t, StateDegraded, status.State)
	require.Equal(t, "managed_proxy_unavailable", status.LastError)
}

func TestSystemHealthDoesNotLimitSystemRouteOrChangeCustomRoutes(t *testing.T) {
	registry := NewRegistry(testRegistryRepo(), domain.ProxyRoute{
		ID:      domain.SystemProxyRouteID,
		Enabled: true,
	}, WithSystemHealth(func() bool { return false }), WithProbe(func(context.Context, domain.ProxyRoute, string) error {
		return nil
	}))

	status, err := registry.Status(context.Background(), domain.SystemProxyRouteID)
	require.NoError(t, err)
	require.Zero(t, status.Capacity)

	profileID := domain.ID("route-a")
	_, err = registry.RouteFor(context.Background(), domain.Account{
		ProxyMode:      domain.ProxyModeAssigned,
		ProxyProfileID: &profileID,
	})
	require.ErrorIs(t, err, ErrRouteUnavailable)
	require.NoError(t, registry.Check(context.Background(), profileID))
	custom, err := registry.RouteFor(context.Background(), domain.Account{
		ProxyMode:      domain.ProxyModeAssigned,
		ProxyProfileID: &profileID,
	})
	require.NoError(t, err)
	require.Equal(t, profileID, custom.ID)
}
