package usecase

import (
	"errors"
	"sync"

	"telegram-companion/internal/domain"
)

type RoundRobinSelector struct {
	mu   sync.Mutex
	next int
}

func NewRoundRobinSelector() *RoundRobinSelector {
	return &RoundRobinSelector{}
}

func (s *RoundRobinSelector) Next(accounts []domain.Account) (*domain.Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	eligible := make([]domain.Account, 0, len(accounts))
	for _, account := range accounts {
		if account.Eligible() && account.Role == domain.AccountRoleSpammer {
			eligible = append(eligible, account)
		}
	}
	if len(eligible) == 0 {
		return nil, errors.New("no eligible accounts")
	}

	account := eligible[s.next%len(eligible)]
	s.next++
	return &account, nil
}
