package usecase

import (
	"context"
	"testing"

	"telegram-companion/internal/domain"
	"telegram-companion/internal/telegram/fake"
)

type task7AccountRepoStub struct{ accounts []domain.Account }

func (r task7AccountRepoStub) ListActive(context.Context) ([]domain.Account, error) { return r.accounts, nil }
func (r task7AccountRepoStub) List(context.Context) ([]domain.Account, error)     { return r.accounts, nil }
func (r task7AccountRepoStub) Save(context.Context, domain.Account) error          { return nil }

type task7ChannelRepoStub struct {
	channels    []domain.Channel
	memberships []domain.ChannelMembership
}

func (r *task7ChannelRepoStub) List(context.Context) ([]domain.Channel, error) { return r.channels, nil }
func (r *task7ChannelRepoStub) ListActive(context.Context) ([]domain.Channel, error) {
	return r.channels, nil
}
func (r *task7ChannelRepoStub) Save(_ context.Context, channel domain.Channel) error {
	r.channels = append(r.channels, channel)
	return nil
}
func (r *task7ChannelRepoStub) SaveMembership(_ context.Context, membership domain.ChannelMembership) error {
	r.memberships = append(r.memberships, membership)
	return nil
}

func TestChannelImporterJoinsAllAccounts(t *testing.T) {
	channels := &task7ChannelRepoStub{}
	importer := NewChannelImporter(
		task7AccountRepoStub{accounts: []domain.Account{{ID: "a1", Status: domain.AccountActive}, {ID: "a2", Status: domain.AccountActive}}},
		channels,
		fake.NewGateway(),
	)

	err := importer.ImportLinks(context.Background(), []string{"https://t.me/test", "@test"})
	if err != nil {
		t.Fatalf("ImportLinks returned error: %v", err)
	}
	if len(channels.channels) != 1 {
		t.Fatalf("channels = %d, want 1", len(channels.channels))
	}
	if len(channels.memberships) != 2 {
		t.Fatalf("memberships = %d, want 2", len(channels.memberships))
	}
}
