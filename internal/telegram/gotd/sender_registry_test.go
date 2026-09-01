package gotd

import (
	"context"
	"testing"
	"time"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"github.com/stretchr/testify/require"

	"telegram-companion/internal/domain"
	"telegram-companion/internal/usecase/runtimeconfig"
)

type registrySenderFake struct{}

func (registrySenderFake) SendPublicReply(context.Context, domain.Account, domain.OutgoingMessageJob) error {
	return nil
}

func TestProductionActivatorKeepsSpammerSenderAndOutboundUpdatesActive(t *testing.T) {
	registry := NewClientSenderRegistry(&catalogRepoFake{})
	updates := NewUpdateActivator(scoutCatalogFake{}, &scoutCollectorFake{}, timeNow)
	activator := NewProductionActivator(updates, registry)
	client := &rawUpdateClientFake{api: &tg.Client{}}
	account := domain.Account{ID: "account-1", Role: domain.AccountRoleSpammer}

	require.NoError(t, activator.Apply(context.Background(), client, account, runtimeconfig.Snapshot{}))
	require.True(t, registry.Available(account.ID))
	require.NotNil(t, client.handler)
}
func (registrySenderFake) SendPrivateMessage(context.Context, domain.Account, domain.OutgoingMessageJob) error {
	return nil
}

func (registrySenderFake) ResolveUsername(context.Context, domain.Account, string) error {
	return nil
}

func (registrySenderFake) SendScheduledPrivateMessage(context.Context, domain.Account, domain.ScheduledDMDelivery, string) error {
	return nil
}

func TestClientSenderRegistryUnregisterMakesDisconnectedAccountUnavailable(t *testing.T) {
	registry := NewClientSenderRegistry(&catalogRepoFake{})
	registry.senders["account-1"] = registrySenderFake{}
	require.True(t, registry.Available("account-1"))

	registry.Unregister("account-1")

	require.False(t, registry.Available("account-1"))
	err := registry.SendPublicReply(context.Background(), domain.Account{ID: "account-1"}, domain.OutgoingMessageJob{})
	require.ErrorContains(t, err, "not connected")
}

func TestProductionActivatorUnregistersSenderWhenRoleChangesToScout(t *testing.T) {
	registry := NewClientSenderRegistry(&catalogRepoFake{})
	registry.senders["account-1"] = registrySenderFake{}
	updates := NewUpdateActivator(scoutCatalogFake{}, &scoutCollectorFake{}, timeNow)
	activator := NewProductionActivator(updates, registry)
	client := &rawUpdateClientFake{}

	require.NoError(t, activator.Apply(context.Background(), client, domain.Account{ID: "account-1", Role: domain.AccountRoleScoutAnalyst}, runtimeconfig.Snapshot{}))
	require.False(t, registry.Available("account-1"))
}

func TestProductionActivatorProcessesMembershipWithoutEnablingMessagesWhilePaused(t *testing.T) {
	invoker := &inviteInvoker{joinErr: tgerr.New(400, tg.ErrInviteRequestSent)}
	catalogs := &inviteCatalog{row: domain.Channel{
		ID: "public", TelegramID: "pending", Link: "https://t.me/public_channel", Status: domain.ChannelJoining, Active: true,
	}, memberships: map[domain.ID]domain.ChannelMembership{
		"spammer": {AccountID: "spammer", ChannelID: "public", Status: "joining"},
	}}
	registry := NewClientSenderRegistry(catalogs)
	updates := NewUpdateActivator(catalogs, &scoutCollectorFake{}, timeNow)
	activator := NewProductionActivator(updates, registry)
	client := &rawUpdateClientFake{api: tg.NewClient(invoker)}
	snapshot := runtimeconfig.Snapshot{
		OutboundPaused:     true,
		CatalogAssignments: map[domain.SourceCatalog][]domain.ID{domain.SourceCatalogOutbound: {"public"}},
	}

	require.NoError(t, activator.Apply(context.Background(), client, domain.Account{ID: "spammer", Role: domain.AccountRoleSpammer}, snapshot))
	require.Equal(t, 1, invoker.joins)
	require.Equal(t, membershipPendingApproval, catalogs.memberships["spammer"].Status)
	require.NotNil(t, catalogs.memberships["spammer"].RequestSubmittedAt)
	require.False(t, registry.Available("spammer"))
	require.Nil(t, client.handler)
}

func timeNow() time.Time { return time.Now() }
