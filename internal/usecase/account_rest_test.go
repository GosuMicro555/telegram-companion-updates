package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"telegram-companion/internal/domain"
)

type accountRestStoreStub struct {
	rows []domain.AccountGroupRest
	err  error
	ctx  context.Context
}

func (s *accountRestStoreStub) ListAccountGroupRests(ctx context.Context) ([]domain.AccountGroupRest, error) {
	s.ctx = ctx
	return append([]domain.AccountGroupRest(nil), s.rows...), s.err
}

func TestAccountRestServiceListsRestingAndReadyRowsInDeterministicOrder(t *testing.T) {
	type contextKey string
	const key contextKey = "request"
	ctx := context.WithValue(context.Background(), key, "account-rest")
	now := time.Date(2026, 7, 24, 10, 0, 0, 500_000_000, time.UTC)
	store := &accountRestStoreStub{rows: []domain.AccountGroupRest{
		{
			AccountID: "expired", AccountTitle: "Expired", ChannelID: "channel-c", ChannelTitle: "Gamma",
			Catalog: domain.SourceCatalogScout, StartedAt: now.Add(-36 * time.Hour), Until: now, DurationHours: 36,
		},
		{
			AccountID: "beta", AccountTitle: "Beta", ChannelID: "channel-b", ChannelTitle: "Beta channel",
			Catalog: domain.SourceCatalogOutbound, StartedAt: now.Add(-34 * time.Hour), Until: now.Add(2 * time.Hour), DurationHours: 36,
		},
		{
			AccountID: "alpha", AccountTitle: "Alpha", ChannelID: "channel-a", ChannelTitle: "Alpha channel",
			Catalog: domain.SourceCatalogOutbound, StartedAt: now.Add(-34 * time.Hour), Until: now.Add(2 * time.Hour), DurationHours: 36,
		},
		{
			AccountID: "old", AccountTitle: "Old", ChannelID: "channel-d", ChannelTitle: "Delta",
			Catalog: domain.SourceCatalogScout, StartedAt: now.Add(-72 * time.Hour), Until: now.Add(-36 * time.Hour), DurationHours: 36,
		},
	}}
	service := NewAccountRestService(store, func() time.Time { return now })

	rows, err := service.List(ctx)

	require.NoError(t, err)
	require.Same(t, ctx, store.ctx)
	require.Len(t, rows, 4)
	require.Equal(t, []domain.ID{"alpha", "beta", "expired", "old"}, []domain.ID{
		rows[0].AccountID, rows[1].AccountID, rows[2].AccountID, rows[3].AccountID,
	})
	require.Equal(t, AccountRestStatusResting, rows[0].Status)
	require.Equal(t, AccountRestStatusResting, rows[1].Status)
	require.Equal(t, AccountRestStatusReady, rows[2].Status)
	require.Equal(t, AccountRestStatusReady, rows[3].Status)
	require.Equal(t, "Alpha channel", rows[0].ChannelTitle)
	require.Equal(t, domain.SourceCatalogOutbound, rows[0].Catalog)
	require.Equal(t, 36, rows[0].DurationHours)
}

func TestAccountRestServiceWrapsRepositoryFailure(t *testing.T) {
	want := errors.New("sqlite: /private/account-rest.db")
	service := NewAccountRestService(&accountRestStoreStub{err: want}, time.Now)

	rows, err := service.List(context.Background())

	require.Nil(t, rows)
	require.ErrorIs(t, err, want)
	require.ErrorContains(t, err, "list account rests")
}
