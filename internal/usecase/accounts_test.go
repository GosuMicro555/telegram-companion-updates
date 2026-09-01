package usecase_test

import (
	"context"
	"testing"

	"telegram-companion/internal/domain"
	"telegram-companion/internal/usecase"

	"github.com/stretchr/testify/require"
)

type accountStoreStub struct {
	accounts []domain.Account
}

func (s *accountStoreStub) ListAccounts(context.Context) ([]domain.Account, error) {
	return append([]domain.Account(nil), s.accounts...), nil
}

func (s *accountStoreStub) SaveAccount(_ context.Context, account domain.Account) error {
	for index := range s.accounts {
		if s.accounts[index].ID == account.ID {
			s.accounts[index] = account
			return nil
		}
	}
	return usecase.ErrAccountNotFound
}

func TestRoleCatalogEligibilitySeparatesAccountWork(t *testing.T) {
	ctx := context.Background()
	store := &accountStoreStub{accounts: []domain.Account{
		{ID: "spammer", Role: domain.AccountRoleSpammer, Status: domain.AccountActive},
		{ID: "scout", Role: domain.AccountRoleScoutAnalyst, Status: domain.AccountActive},
		{ID: "paused-scout", Role: domain.AccountRoleScoutAnalyst, Status: domain.AccountPaused},
	}}
	service := usecase.NewAccountService(store)

	outbound, err := service.ListEligible(ctx, domain.SourceCatalogOutbound)
	require.NoError(t, err)
	require.Equal(t, []domain.ID{"spammer"}, accountIDs(outbound))

	scout, err := service.ListEligible(ctx, domain.SourceCatalogScout)
	require.NoError(t, err)
	require.Equal(t, []domain.ID{"scout"}, accountIDs(scout))
}

func TestRoleCatalogChangePersists(t *testing.T) {
	ctx := context.Background()
	store := &accountStoreStub{accounts: []domain.Account{{
		ID: "account", Role: domain.AccountRoleSpammer, Status: domain.AccountActive,
	}}}
	service := usecase.NewAccountService(store)

	updated, err := service.SetRole(ctx, "account", domain.AccountRoleScoutAnalyst)
	require.NoError(t, err)
	require.Equal(t, domain.AccountRoleScoutAnalyst, updated.Role)
	require.Equal(t, domain.AccountRoleScoutAnalyst, store.accounts[0].Role)

	_, err = service.SetRole(ctx, "account", domain.AccountRole("owner"))
	require.ErrorIs(t, err, usecase.ErrInvalidAccountRole)
	require.Equal(t, domain.AccountRoleScoutAnalyst, store.accounts[0].Role)
}

func accountIDs(accounts []domain.Account) []domain.ID {
	ids := make([]domain.ID, len(accounts))
	for index := range accounts {
		ids[index] = accounts[index].ID
	}
	return ids
}
