package wails

import (
	"context"
	"errors"
	"time"

	"telegram-companion/internal/usecase"
)

var errAccountRestsUnavailable = errors.New("account rests are unavailable")

type AccountRestLister interface {
	List(context.Context) ([]usecase.AccountRest, error)
}

type AccountRestDTO struct {
	AccountID     string `json:"accountID"`
	AccountTitle  string `json:"accountTitle"`
	ChannelID     string `json:"channelID"`
	ChannelTitle  string `json:"channelTitle"`
	Catalog       string `json:"catalog"`
	Status        string `json:"status"`
	StartedAt     string `json:"startedAt"`
	Until         string `json:"until"`
	DurationHours int    `json:"durationHours"`
}

func ConfigureAccountRests(bindings *Bindings, lister AccountRestLister) {
	if bindings != nil {
		bindings.accountRests = lister
	}
}

func (b *Bindings) ListAccountRests() ([]AccountRestDTO, error) {
	if b == nil || b.runtimeError() != nil || b.accountRests == nil {
		return nil, errAccountRestsUnavailable
	}
	rows, err := b.accountRests.List(b.rootContext())
	if err != nil {
		return nil, errAccountRestsUnavailable
	}
	result := make([]AccountRestDTO, len(rows))
	for index, row := range rows {
		result[index] = AccountRestDTO{
			AccountID: string(row.AccountID), AccountTitle: row.AccountTitle,
			ChannelID: string(row.ChannelID), ChannelTitle: row.ChannelTitle,
			Catalog: string(row.Catalog), Status: string(row.Status),
			StartedAt: row.StartedAt.UTC().Format(time.RFC3339Nano),
			Until:     row.Until.UTC().Format(time.RFC3339Nano), DurationHours: row.DurationHours,
		}
	}
	return result, nil
}
