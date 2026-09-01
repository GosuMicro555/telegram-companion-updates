package usecase

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"telegram-companion/internal/domain"
)

type AccountRestStatus string

const (
	AccountRestStatusResting AccountRestStatus = "resting"
	AccountRestStatusReady   AccountRestStatus = "ready"
)

type AccountRestStore interface {
	ListAccountGroupRests(context.Context) ([]domain.AccountGroupRest, error)
}

type AccountRest struct {
	AccountID     domain.ID
	AccountTitle  string
	ChannelID     domain.ID
	ChannelTitle  string
	Catalog       domain.SourceCatalog
	Status        AccountRestStatus
	StartedAt     time.Time
	Until         time.Time
	DurationHours int
}

type AccountRestService struct {
	store AccountRestStore
	now   func() time.Time
}

func NewAccountRestService(store AccountRestStore, now func() time.Time) *AccountRestService {
	if now == nil {
		now = time.Now
	}
	return &AccountRestService{store: store, now: now}
}

func (s *AccountRestService) List(ctx context.Context) ([]AccountRest, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("account rest store is not configured")
	}
	rows, err := s.store.ListAccountGroupRests(ctx)
	if err != nil {
		return nil, fmt.Errorf("list account rests: %w", err)
	}
	now := s.now().UTC()
	result := make([]AccountRest, 0, len(rows))
	for _, row := range rows {
		status := AccountRestStatusReady
		if row.Until.After(now) {
			status = AccountRestStatusResting
		}
		result = append(result, AccountRest{
			AccountID: row.AccountID, AccountTitle: row.AccountTitle,
			ChannelID: row.ChannelID, ChannelTitle: row.ChannelTitle,
			Catalog: row.Catalog, Status: status,
			StartedAt: row.StartedAt.UTC(), Until: row.Until.UTC(), DurationHours: row.DurationHours,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if !result[i].Until.Equal(result[j].Until) {
			return result[i].Until.After(result[j].Until)
		}
		if result[i].AccountID != result[j].AccountID {
			return result[i].AccountID < result[j].AccountID
		}
		if result[i].Catalog != result[j].Catalog {
			return result[i].Catalog < result[j].Catalog
		}
		return result[i].ChannelID < result[j].ChannelID
	})
	return result, nil
}
