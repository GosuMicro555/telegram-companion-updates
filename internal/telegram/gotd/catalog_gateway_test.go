package gotd

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"github.com/stretchr/testify/require"

	"telegram-companion/internal/domain"
	"telegram-companion/internal/usecase/runtimeconfig"
)

type catalogRepoFake struct {
	saved []domain.Channel
	into  []domain.SourceCatalog
}

func (r *catalogRepoFake) List(context.Context, domain.SourceCatalog) ([]domain.Channel, error) {
	return nil, nil
}
func (r *catalogRepoFake) Save(_ context.Context, catalog domain.SourceCatalog, channel domain.Channel) error {
	r.into = append(r.into, catalog)
	r.saved = append(r.saved, channel)
	return nil
}

type channelRepoFake struct{ memberships []domain.ChannelMembership }

func (r *channelRepoFake) List(context.Context) ([]domain.Channel, error)       { return nil, nil }
func (r *channelRepoFake) ListActive(context.Context) ([]domain.Channel, error) { return nil, nil }
func (r *channelRepoFake) Save(context.Context, domain.Channel) error           { return nil }
func (r *channelRepoFake) SaveMembership(_ context.Context, membership domain.ChannelMembership) error {
	r.memberships = append(r.memberships, membership)
	return nil
}

type catalogRPCFake struct {
	resolvedLinks []string
	checked       []domain.ID
	joined        []domain.ID
	floodAccount  domain.ID
	joinErr       error
	channel       domain.Channel
}

func (r *catalogRPCFake) ResolveChannel(_ context.Context, link string) (domain.Channel, error) {
	r.resolvedLinks = append(r.resolvedLinks, link)
	if r.channel.ID != "" {
		channel := r.channel
		channel.Link = link
		return channel, nil
	}
	return domain.Channel{ID: "channel-1", TelegramID: "-100123", Title: "Resolved", Link: link, Active: true}, nil
}

type assignmentKey struct {
	account domain.ID
	catalog domain.SourceCatalog
	channel domain.ID
}

type catalogAssignmentResolverFake struct{ assigned map[assignmentKey]bool }

func (r catalogAssignmentResolverFake) Assigned(_ runtimeconfig.Snapshot, accountID domain.ID, catalog domain.SourceCatalog, channelID domain.ID) bool {
	return r.assigned[assignmentKey{account: accountID, catalog: catalog, channel: channelID}]
}

func assignedCatalogGateway(accounts domain.AccountRepository, catalogs domain.CatalogRepository, channels domain.ChannelRepository, rpc CatalogRPC, snapshot runtimeconfig.Snapshot, keys ...assignmentKey) *CatalogGateway {
	assigned := make(map[assignmentKey]bool, len(keys))
	for _, key := range keys {
		assigned[key] = true
	}
	gateway := NewCatalogGateway(accounts, catalogs, channels, rpc, catalogAssignmentResolverFake{assigned: assigned})
	gateway.ApplySnapshot(snapshot)
	return gateway
}

func (r *catalogRPCFake) CheckMembership(_ context.Context, account domain.Account, channel domain.Channel) (domain.ChannelMembership, error) {
	r.checked = append(r.checked, account.ID)
	return domain.ChannelMembership{AccountID: account.ID, ChannelID: channel.ID, Status: "not_member"}, nil
}

func (r *catalogRPCFake) JoinChannel(_ context.Context, account domain.Account, channel domain.Channel) (domain.ChannelMembership, error) {
	r.joined = append(r.joined, account.ID)
	if r.joinErr != nil {
		return domain.ChannelMembership{}, r.joinErr
	}
	if account.ID == r.floodAccount {
		return domain.ChannelMembership{}, floodDurationError{duration: 42 * time.Second}
	}
	return domain.ChannelMembership{AccountID: account.ID, ChannelID: channel.ID, IsMember: true, Status: "member"}, nil
}

func TestCatalogGatewaySurfacesNativeGotdFloodWait(t *testing.T) {
	native := tgerr.New(420, tgerr.ErrFloodWait)
	native.Argument = 19
	rpc := &catalogRPCFake{joinErr: native}
	gateway := assignedCatalogGateway(
		managerAccountRepo{accounts: []domain.Account{{ID: "scout", Role: domain.AccountRoleScoutAnalyst, Status: domain.AccountActive}}},
		&catalogRepoFake{}, &channelRepoFake{}, rpc,
		runtimeconfig.Snapshot{Revision: 1, CatalogAssignments: map[domain.SourceCatalog][]domain.ID{domain.SourceCatalogScout: {"channel-1"}}},
		assignmentKey{account: "scout", catalog: domain.SourceCatalogScout, channel: "channel-1"},
	)

	_, err := gateway.ResolveAndJoin(context.Background(), domain.SourceCatalogScout, "@room")

	var flood *FloodWaitError
	require.ErrorAs(t, err, &flood)
	require.Equal(t, domain.ID("scout"), flood.AccountID)
	require.Equal(t, 19*time.Second, flood.Duration)
}

func TestCatalogGatewayTreatsInviteRequestSentAsPendingApproval(t *testing.T) {
	beforeRequest := time.Now().UTC()
	rpc := &catalogRPCFake{joinErr: tgerr.New(400, tg.ErrInviteRequestSent)}
	channels := &channelRepoFake{}
	gateway := assignedCatalogGateway(
		managerAccountRepo{accounts: []domain.Account{{ID: "scout", Role: domain.AccountRoleScoutAnalyst, Status: domain.AccountActive}}},
		&catalogRepoFake{}, channels, rpc,
		runtimeconfig.Snapshot{Revision: 1, CatalogAssignments: map[domain.SourceCatalog][]domain.ID{domain.SourceCatalogScout: {"channel-1"}}},
		assignmentKey{account: "scout", catalog: domain.SourceCatalogScout, channel: "channel-1"},
	)

	_, err := gateway.ResolveAndJoin(context.Background(), domain.SourceCatalogScout, "https://t.me/+invite")
	afterRequest := time.Now().UTC()

	require.NoError(t, err)
	require.Len(t, channels.memberships, 1)
	membership := channels.memberships[0]
	require.Equal(t, domain.ID("scout"), membership.AccountID)
	require.Equal(t, domain.ID("channel-1"), membership.ChannelID)
	require.Equal(t, membershipPendingApproval, membership.Status)
	require.NotNil(t, membership.RequestSubmittedAt)
	require.NotNil(t, membership.LastCheckAt)
	require.True(t, membership.RequestSubmittedAt.Equal(*membership.LastCheckAt))
	require.WithinDuration(t, beforeRequest, *membership.RequestSubmittedAt, time.Second)
	require.WithinDuration(t, afterRequest, *membership.RequestSubmittedAt, time.Second)
}

type floodDurationError struct{ duration time.Duration }

func (e floodDurationError) Error() string            { return "FLOOD_WAIT" }
func (e floodDurationError) FloodWait() time.Duration { return e.duration }

func TestCatalogGatewayNormalizesAndJoinsOnlyEligibleAccounts(t *testing.T) {
	accounts := managerAccountRepo{accounts: []domain.Account{
		{ID: "scout", Role: domain.AccountRoleScoutAnalyst, Status: domain.AccountActive},
		{ID: "spammer", Role: domain.AccountRoleSpammer, Status: domain.AccountActive},
		{ID: "paused-scout", Role: domain.AccountRoleScoutAnalyst, Status: domain.AccountPaused},
	}}
	catalogs := &catalogRepoFake{}
	channels := &channelRepoFake{}
	rpc := &catalogRPCFake{}
	gateway := assignedCatalogGateway(
		accounts, catalogs, channels, rpc,
		runtimeconfig.Snapshot{Revision: 1, CatalogAssignments: map[domain.SourceCatalog][]domain.ID{domain.SourceCatalogScout: {"channel-1"}}},
		assignmentKey{account: "scout", catalog: domain.SourceCatalogScout, channel: "channel-1"},
	)

	channel, err := gateway.ResolveAndJoin(context.Background(), domain.SourceCatalogScout, "  @ScoutRoom/  ")

	require.NoError(t, err)
	require.Equal(t, domain.ID("channel-1"), channel.ID)
	require.Equal(t, []string{"https://t.me/ScoutRoom"}, rpc.resolvedLinks)
	require.Equal(t, []domain.ID{"scout"}, rpc.checked)
	require.Equal(t, []domain.ID{"scout"}, rpc.joined)
	require.Equal(t, []domain.SourceCatalog{domain.SourceCatalogScout}, catalogs.into)
	require.Equal(t, []domain.ChannelMembership{{AccountID: "scout", ChannelID: "channel-1", IsMember: true, Status: "member"}}, channels.memberships)
}

func TestCatalogGatewayJoinsOnlyAccountAssignedToCatalogEntry(t *testing.T) {
	tests := []struct {
		name    string
		catalog domain.SourceCatalog
		role    domain.AccountRole
	}{
		{name: "outbound", catalog: domain.SourceCatalogOutbound, role: domain.AccountRoleSpammer},
		{name: "scout", catalog: domain.SourceCatalogScout, role: domain.AccountRoleScoutAnalyst},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			accounts := managerAccountRepo{accounts: []domain.Account{
				{ID: "first", Role: tt.role, Status: domain.AccountActive},
				{ID: "second", Role: tt.role, Status: domain.AccountActive},
			}}
			rpc := &catalogRPCFake{channel: domain.Channel{ID: "entry-one", TelegramID: "-1001", Title: "One", Active: true}}
			gateway := assignedCatalogGateway(
				accounts, &catalogRepoFake{}, &channelRepoFake{}, rpc,
				runtimeconfig.Snapshot{Revision: 7, CatalogAssignments: map[domain.SourceCatalog][]domain.ID{tt.catalog: {"entry-one", "entry-two"}}},
				assignmentKey{account: "first", catalog: tt.catalog, channel: "entry-one"},
				assignmentKey{account: "second", catalog: tt.catalog, channel: "entry-two"},
			)

			_, err := gateway.ResolveAndJoin(context.Background(), tt.catalog, "@entry_one")

			require.NoError(t, err)
			require.Equal(t, []domain.ID{"first"}, rpc.checked)
			require.Equal(t, []domain.ID{"first"}, rpc.joined)
		})
	}
}

func TestCatalogGatewayDoesNotJoinWithoutExplicitAssignment(t *testing.T) {
	accounts := managerAccountRepo{accounts: []domain.Account{{
		ID: "scout", Role: domain.AccountRoleScoutAnalyst, Status: domain.AccountActive,
	}}}
	rpc := &catalogRPCFake{}
	gateway := NewCatalogGateway(accounts, &catalogRepoFake{}, &channelRepoFake{}, rpc)

	_, err := gateway.ResolveAndJoin(context.Background(), domain.SourceCatalogScout, "@unassigned")

	require.NoError(t, err)
	require.Empty(t, rpc.checked)
	require.Empty(t, rpc.joined)
}

func TestCatalogGatewayNormalizesInviteLinks(t *testing.T) {
	rpc := &catalogRPCFake{}
	gateway := NewCatalogGateway(managerAccountRepo{}, &catalogRepoFake{}, &channelRepoFake{}, rpc)

	_, err := gateway.ResolveAndJoin(context.Background(), domain.SourceCatalogScout, "https://telegram.me/joinchat/AbCd/")

	require.NoError(t, err)
	require.Equal(t, []string{"https://t.me/+AbCd"}, rpc.resolvedLinks)
}

func TestCatalogGatewaySurfacesFloodWaitWithoutRotatingAccounts(t *testing.T) {
	accounts := managerAccountRepo{accounts: []domain.Account{
		{ID: "first", Role: domain.AccountRoleSpammer, Status: domain.AccountActive},
		{ID: "second", Role: domain.AccountRoleSpammer, Status: domain.AccountActive},
	}}
	channels := &channelRepoFake{}
	rpc := &catalogRPCFake{floodAccount: "first"}
	gateway := assignedCatalogGateway(
		accounts, &catalogRepoFake{}, channels, rpc,
		runtimeconfig.Snapshot{Revision: 1, CatalogAssignments: map[domain.SourceCatalog][]domain.ID{domain.SourceCatalogOutbound: {"channel-1"}}},
		assignmentKey{account: "first", catalog: domain.SourceCatalogOutbound, channel: "channel-1"},
		assignmentKey{account: "second", catalog: domain.SourceCatalogOutbound, channel: "channel-1"},
	)

	_, err := gateway.ResolveAndJoin(context.Background(), domain.SourceCatalogOutbound, "https://t.me/public")

	var flood *FloodWaitError
	require.True(t, errors.As(err, &flood))
	require.Equal(t, domain.ID("first"), flood.AccountID)
	require.Equal(t, 42*time.Second, flood.Duration)
	require.Equal(t, []domain.ID{"first"}, rpc.joined)
	require.Equal(t, []domain.ChannelMembership{{
		AccountID: "first", ChannelID: "channel-1", Status: "flood_wait", LastError: "FLOOD_WAIT",
	}}, channels.memberships)
}

func TestCatalogGatewayRejectsUnsupportedCatalogBeforeRPC(t *testing.T) {
	rpc := &catalogRPCFake{}
	gateway := NewCatalogGateway(managerAccountRepo{}, &catalogRepoFake{}, &channelRepoFake{}, rpc)

	_, err := gateway.ResolveAndJoin(context.Background(), domain.SourceCatalog("unknown"), "@room")

	require.ErrorIs(t, err, ErrUnsupportedCatalog)
	require.Empty(t, rpc.resolvedLinks)
}
