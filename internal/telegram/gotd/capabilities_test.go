package gotd

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"telegram-companion/internal/domain"
)

type capabilityRPCFake struct {
	calls      []string
	membership domain.ChannelMembership
}

func (f *capabilityRPCFake) RegisterUpdateHandler(_ context.Context, _ UpdateHandler) error {
	f.calls = append(f.calls, "updates.register")
	return nil
}

func (f *capabilityRPCFake) CheckMembership(_ context.Context, _ domain.Account, channel domain.Channel) (domain.ChannelMembership, error) {
	f.calls = append(f.calls, "channels.check:"+string(channel.ID))
	return f.membership, nil
}

func (f *capabilityRPCFake) JoinChannel(_ context.Context, account domain.Account, channel domain.Channel) (domain.ChannelMembership, error) {
	f.calls = append(f.calls, "channels.join:"+string(channel.ID))
	return domain.ChannelMembership{AccountID: account.ID, ChannelID: channel.ID, IsMember: true, Status: "member"}, nil
}

func (f *capabilityRPCFake) SendPublicReply(context.Context, domain.Account, domain.OutgoingMessageJob) error {
	f.calls = append(f.calls, "messages.sendMessage")
	return nil
}

func (f *capabilityRPCFake) SendPrivateMessage(context.Context, domain.Account, domain.OutgoingMessageJob) error {
	f.calls = append(f.calls, "messages.sendPrivate")
	return nil
}

func (f *capabilityRPCFake) GetHistory() { f.calls = append(f.calls, "messages.getHistory") }
func (f *capabilityRPCFake) Search()     { f.calls = append(f.calls, "messages.search") }
func (f *capabilityRPCFake) Backfill()   { f.calls = append(f.calls, "iterator.backfill") }

func TestScoutCapabilityHasNoSender(t *testing.T) {
	scout := NewScoutCapability(&capabilityRPCFake{})
	_, implements := any(scout).(OutboundSender)
	require.False(t, implements)
}

func TestScoutActivationDoesNotRequestHistoryOrSend(t *testing.T) {
	rpc := &capabilityRPCFake{}
	scout := NewScoutCapability(rpc)
	account := domain.Account{ID: "scout-1", Role: domain.AccountRoleScoutAnalyst, Status: domain.AccountActive}
	chat := domain.Channel{ID: "chat-1", Active: true}

	err := scout.Activate(context.Background(), account, []domain.Channel{chat}, func(context.Context, domain.IncomingMessageEvent) error { return nil })

	require.NoError(t, err)
	require.Equal(t, []string{"updates.register", "channels.check:chat-1", "channels.join:chat-1"}, rpc.calls)
	require.NotContains(t, rpc.calls, "messages.getHistory")
	require.NotContains(t, rpc.calls, "messages.search")
	require.NotContains(t, rpc.calls, "iterator.backfill")
	require.NotContains(t, rpc.calls, "messages.sendMessage")
	require.NotContains(t, rpc.calls, "messages.sendPrivate")
}

func TestRoleTransitionToScoutCannotSend(t *testing.T) {
	rpc := &capabilityRPCFake{}
	spammer := NewSpammerCapability(rpc)
	require.Implements(t, (*OutboundSender)(nil), spammer)

	scout := NewScoutCapability(rpc)
	_, implements := any(scout).(OutboundSender)
	require.False(t, implements)
	require.NoError(t, scout.Activate(context.Background(), domain.Account{ID: "account-1"}, nil, func(context.Context, domain.IncomingMessageEvent) error { return nil }))
	require.NotContains(t, rpc.calls, "messages.sendMessage")
	require.NotContains(t, rpc.calls, "messages.sendPrivate")
}
