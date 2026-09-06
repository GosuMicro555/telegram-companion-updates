package routes

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"telegram-companion/internal/domain"
)

type registryRepoFake struct {
	mu       sync.Mutex
	profiles map[domain.ID]domain.ProxyProfile
	routes   map[domain.ID]domain.ProxyRoute
	counts   map[domain.ID]int
	health   map[domain.ID]string
}

func (r *registryRepoFake) Save(context.Context, domain.ProxyProfile, *string) error { return nil }
func (r *registryRepoFake) Delete(context.Context, domain.ID) error                  { return nil }
func (r *registryRepoFake) AssignAccount(context.Context, domain.ID, domain.ProxyMode, *domain.ID) error {
	return nil
}
func (r *registryRepoFake) List(context.Context) ([]domain.ProxyProfile, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]domain.ProxyProfile, 0, len(r.profiles))
	for _, profile := range r.profiles {
		result = append(result, profile)
	}
	return result, nil
}
func (r *registryRepoFake) Route(_ context.Context, id domain.ID) (domain.ProxyRoute, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	route, ok := r.routes[id]
	if !ok {
		return domain.ProxyRoute{}, errors.New("route not found")
	}
	return route, nil
}
func (r *registryRepoFake) AssignmentCounts(context.Context) (map[domain.ID]int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make(map[domain.ID]int, len(r.counts))
	for id, count := range r.counts {
		result[id] = count
	}
	return result, nil
}
func (r *registryRepoFake) UpdateHealth(_ context.Context, id domain.ID, status, _ string, _ time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.health == nil {
		r.health = make(map[domain.ID]string)
	}
	r.health[id] = status
	return nil
}

func testRegistryRepo() *registryRepoFake {
	return &registryRepoFake{
		profiles: map[domain.ID]domain.ProxyProfile{
			"route-a":  {ID: "route-a", Name: "Route A", Enabled: true},
			"route-b":  {ID: "route-b", Name: "Route B", Enabled: true},
			"disabled": {ID: "disabled", Name: "Disabled", Enabled: false},
		},
		routes: map[domain.ID]domain.ProxyRoute{
			"route-a":  {ID: "route-a", Name: "Route A", Protocol: "socks5", Host: "127.0.0.1", Port: 1080, Password: "secret-a", Enabled: true},
			"route-b":  {ID: "route-b", Name: "Route B", Protocol: "http", Host: "127.0.0.1", Port: 8080, Password: "secret-b", Enabled: true},
			"disabled": {ID: "disabled", Name: "Disabled", Protocol: "http", Host: "127.0.0.1", Port: 8081, Enabled: false},
		},
		counts: map[domain.ID]int{domain.SystemProxyRouteID: 3, "route-a": 10, "route-b": 2},
	}
}

func TestRegistryResolvesSystemAndHealthyAssignedRoutes(t *testing.T) {
	repo := testRegistryRepo()
	probe := func(_ context.Context, route domain.ProxyRoute, _ string) error {
		if route.ID == "route-a" {
			return nil
		}
		return errors.New("offline")
	}
	registry := NewRegistry(repo, domain.ProxyRoute{ID: domain.SystemProxyRouteID, Name: "Tor/Snowflake", Protocol: "socks5", Host: "127.0.0.1", Port: 19050, Enabled: true}, WithProbe(probe))

	system, err := registry.RouteFor(context.Background(), domain.Account{ID: "system-account", ProxyMode: domain.ProxyModeGlobal})
	require.NoError(t, err)
	require.Equal(t, domain.SystemProxyRouteID, system.ID)

	profileID := domain.ID("route-a")
	_, err = registry.RouteFor(context.Background(), domain.Account{ID: "custom", ProxyMode: domain.ProxyModeAssigned, ProxyProfileID: &profileID})
	require.ErrorIs(t, err, ErrRouteUnavailable)
	require.NoError(t, registry.Check(context.Background(), profileID))
	custom, err := registry.RouteFor(context.Background(), domain.Account{ID: "custom", ProxyMode: domain.ProxyModeAssigned, ProxyProfileID: &profileID})
	require.NoError(t, err)
	require.Equal(t, profileID, custom.ID)

	status, err := registry.Status(context.Background(), profileID)
	require.NoError(t, err)
	require.Equal(t, StateFull, status.State)
	require.Equal(t, 10, status.Usage)
	require.Equal(t, 10, status.Capacity)
	require.NotContains(t, status.LastError, "secret-a")
}

func TestRegistrySystemRouteStatusIsUnlimitedAndReconnectDelayIsCapped(t *testing.T) {
	repo := testRegistryRepo()
	repo.counts[domain.SystemProxyRouteID] = 500
	registry := NewRegistry(repo, domain.ProxyRoute{ID: domain.SystemProxyRouteID, Enabled: true})

	status, err := registry.Status(context.Background(), domain.SystemProxyRouteID)
	require.NoError(t, err)
	require.Equal(t, 500, status.Usage)
	require.Zero(t, status.Capacity)
	require.Equal(t, StateReady, status.State)
	require.Equal(t, 3*time.Second, registry.ReconnectDelay(11))
	require.Equal(t, 3*time.Second, registry.ReconnectDelay(500))
}

func TestRegistryIsolatesFailedAndDisabledRoutes(t *testing.T) {
	repo := testRegistryRepo()
	registry := NewRegistry(repo, domain.ProxyRoute{ID: domain.SystemProxyRouteID, Enabled: true}, WithProbe(func(_ context.Context, route domain.ProxyRoute, _ string) error {
		if route.ID == "route-a" {
			return errors.New("dial failed with secret-a")
		}
		return nil
	}))

	require.Error(t, registry.Check(context.Background(), "route-a"))
	require.NoError(t, registry.Check(context.Background(), "route-b"))
	for id, wantAvailable := range map[domain.ID]bool{"route-a": false, "route-b": true, "disabled": false} {
		profileID := id
		_, err := registry.RouteFor(context.Background(), domain.Account{ProxyMode: domain.ProxyModeAssigned, ProxyProfileID: &profileID})
		if wantAvailable {
			require.NoError(t, err)
		} else {
			require.ErrorIs(t, err, ErrRouteUnavailable)
		}
	}
	status, err := registry.Status(context.Background(), "route-a")
	require.NoError(t, err)
	require.Equal(t, StateDegraded, status.State)
	require.Equal(t, "health_check_failed", status.LastError)
	require.NotContains(t, status.LastError, "secret-a")
}

func TestRegistryRejectsUnassignedAndMalformedAssignments(t *testing.T) {
	registry := NewRegistry(testRegistryRepo(), domain.ProxyRoute{ID: domain.SystemProxyRouteID, Enabled: true})
	_, err := registry.RouteFor(context.Background(), domain.Account{ProxyMode: domain.ProxyModeDirect})
	require.ErrorIs(t, err, ErrRouteUnavailable)
	_, err = registry.RouteFor(context.Background(), domain.Account{ProxyMode: domain.ProxyModeUnassigned})
	require.ErrorIs(t, err, ErrRouteUnavailable)
	_, err = registry.RouteFor(context.Background(), domain.Account{ProxyMode: domain.ProxyModeAssigned})
	require.ErrorIs(t, err, ErrRouteUnavailable)
}
