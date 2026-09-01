package usecase

import (
	"context"
	"errors"
	"time"

	"telegram-companion/internal/domain"
)

var (
	ErrAccountNotFound          = errors.New("account not found")
	ErrInvalidAccountRole       = errors.New("invalid account role")
	ErrLegacyPauseNotReviewable = errors.New("legacy paused account is not awaiting review")
)

type AccountStore interface {
	ListAccounts(context.Context) ([]domain.Account, error)
	SaveAccount(context.Context, domain.Account) error
}

type AccountService struct {
	store AccountStore
}

type accountRoleMutator interface {
	SetAccountRole(context.Context, domain.ID, domain.AccountRole, time.Time) (domain.Account, error)
}

type legacyPausedResumer interface {
	ResumeLegacyPaused(context.Context, domain.ID, time.Time) (domain.Account, error)
}

func NewAccountService(store AccountStore) *AccountService {
	return &AccountService{store: store}
}

func (s *AccountService) List(ctx context.Context) ([]domain.Account, error) {
	return s.store.ListAccounts(ctx)
}

func (s *AccountService) ListEligible(ctx context.Context, catalog domain.SourceCatalog) ([]domain.Account, error) {
	accounts, err := s.store.ListAccounts(ctx)
	if err != nil {
		return nil, err
	}
	role, err := roleForCatalog(catalog)
	if err != nil {
		return nil, err
	}
	eligible := make([]domain.Account, 0, len(accounts))
	for _, account := range accounts {
		if account.Role == role && account.Eligible() {
			eligible = append(eligible, account)
		}
	}
	return eligible, nil
}

func (s *AccountService) SetRole(ctx context.Context, id domain.ID, role domain.AccountRole) (domain.Account, error) {
	if !validAccountRole(role) {
		return domain.Account{}, ErrInvalidAccountRole
	}
	if mutator, ok := s.store.(accountRoleMutator); ok {
		account, err := mutator.SetAccountRole(ctx, id, role, time.Now().UTC())
		if errors.Is(err, ErrAccountNotFound) || (err != nil && err.Error() == "account not found") {
			return domain.Account{}, ErrAccountNotFound
		}
		return account, err
	}
	accounts, err := s.store.ListAccounts(ctx)
	if err != nil {
		return domain.Account{}, err
	}
	for _, account := range accounts {
		if account.ID != id {
			continue
		}
		account.Role = role
		account.UpdatedAt = time.Now().UTC()
		if err := s.store.SaveAccount(ctx, account); err != nil {
			return domain.Account{}, err
		}
		return account, nil
	}
	return domain.Account{}, ErrAccountNotFound
}

func (s *AccountService) ResumeLegacyPaused(ctx context.Context, id domain.ID) (domain.Account, error) {
	resumer, ok := s.store.(legacyPausedResumer)
	if !ok {
		return domain.Account{}, ErrLegacyPauseNotReviewable
	}
	return resumer.ResumeLegacyPaused(ctx, id, time.Now().UTC())
}

func validAccountRole(role domain.AccountRole) bool {
	return role == domain.AccountRoleSpammer || role == domain.AccountRoleScoutAnalyst
}

func roleForCatalog(catalog domain.SourceCatalog) (domain.AccountRole, error) {
	switch catalog {
	case domain.SourceCatalogOutbound:
		return domain.AccountRoleSpammer, nil
	case domain.SourceCatalogScout:
		return domain.AccountRoleScoutAnalyst, nil
	default:
		return "", ErrUnsupportedCatalog
	}
}
