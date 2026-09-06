package routes

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"telegram-companion/internal/domain"
)

type routeLookupFailureRepository struct {
	domain.ProxyProfileRepository
}

func (routeLookupFailureRepository) Route(context.Context, domain.ID) (domain.ProxyRoute, error) {
	return domain.ProxyRoute{}, errors.New("repository unavailable")
}

func (routeLookupFailureRepository) List(context.Context) ([]domain.ProxyProfile, error) {
	return []domain.ProxyProfile{{ID: "route-a", Name: "Route A", Enabled: true}}, nil
}

func (routeLookupFailureRepository) AssignmentCounts(context.Context) (map[domain.ID]int, error) {
	return map[domain.ID]int{"route-a": 1}, nil
}

func TestCheckRouteLookupFailureLeavesCustomRouteDegraded(t *testing.T) {
	now := time.Date(2026, 8, 30, 9, 30, 0, 0, time.UTC)
	registry := NewRegistry(
		routeLookupFailureRepository{},
		domain.ProxyRoute{ID: domain.SystemProxyRouteID, Enabled: true},
		WithClock(func() time.Time { return now }),
	)

	err := registry.Check(context.Background(), "route-a")
	require.ErrorIs(t, err, ErrRouteUnavailable)

	status, err := registry.Status(context.Background(), "route-a")
	require.NoError(t, err)
	require.Equal(t, StateDegraded, status.State)
	require.Equal(t, "health_check_failed", status.LastError)
	require.Equal(t, now.Add(time.Second), *status.RetryAt)

	profileID := domain.ID("route-a")
	_, err = registry.RouteFor(context.Background(), domain.Account{
		ProxyMode:      domain.ProxyModeAssigned,
		ProxyProfileID: &profileID,
	})
	require.ErrorIs(t, err, ErrRouteUnavailable)
	require.Equal(t, StateReady, registry.runtime(domain.SystemProxyRouteID).state)
}
