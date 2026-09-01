package usecase

import (
	"testing"

	"telegram-companion/internal/domain"
)

func TestRoundRobinSelectorSkipsIneligible(t *testing.T) {
	selector := NewRoundRobinSelector()
	accounts := []domain.Account{
		{ID: "a1", Role: domain.AccountRoleSpammer, Status: domain.AccountPaused},
		{ID: "a2", Role: domain.AccountRoleSpammer, Status: domain.AccountActive},
		{ID: "a3", Role: domain.AccountRoleSpammer, Status: domain.AccountActive},
	}

	first, err := selector.Next(accounts)
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	second, err := selector.Next(accounts)
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}

	if first.ID != "a2" || second.ID != "a3" {
		t.Fatalf("round-robin = %s,%s; want a2,a3", first.ID, second.ID)
	}
}

func TestRoundRobinSelectorChoosesOnlyActiveSpammers(t *testing.T) {
	selector := NewRoundRobinSelector()
	accounts := []domain.Account{
		{ID: "scout", Role: domain.AccountRoleScoutAnalyst, Status: domain.AccountActive},
		{ID: "paused", Role: domain.AccountRoleSpammer, Status: domain.AccountPaused},
		{ID: "spam-1", Role: domain.AccountRoleSpammer, Status: domain.AccountActive},
		{ID: "spam-2", Role: domain.AccountRoleSpammer, Status: domain.AccountActive},
	}

	first, err := selector.Next(accounts)
	if err != nil {
		t.Fatal(err)
	}
	second, err := selector.Next(accounts)
	if err != nil {
		t.Fatal(err)
	}

	if first.ID != "spam-1" || second.ID != "spam-2" {
		t.Fatalf("round-robin = %s,%s; want spam-1,spam-2", first.ID, second.ID)
	}
}
