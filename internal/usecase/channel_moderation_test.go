package usecase

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"telegram-companion/internal/domain"
)

func TestChannelModerationListAggregatesModeratedChannels(t *testing.T) {
	requestedAt := time.Date(2026, 7, 18, 8, 0, 0, 0, time.UTC)
	secondRequestAt := requestedAt.Add(15 * time.Minute)
	joinedAt := requestedAt.Add(30 * time.Minute)
	scoutRequestedAt := requestedAt.Add(-2 * time.Hour)
	scoutSecondRequestAt := scoutRequestedAt.Add(10 * time.Minute)
	scoutJoinedAt := scoutRequestedAt.Add(40 * time.Minute)
	scoutLatestJoinedAt := scoutRequestedAt.Add(55 * time.Minute)
	now := requestedAt.Add(45 * time.Minute)

	store := &channelModerationStoreStub{
		accounts: []domain.Account{
			{ID: "outbound-member", DisplayName: "Member name", Role: domain.AccountRoleSpammer},
			{ID: "outbound-pending", PhoneMasked: "+1 *** 1000", Role: domain.AccountRoleSpammer},
			{ID: "scout-member", Role: domain.AccountRoleScoutAnalyst},
			{ID: "scout-member-late", PhoneMasked: "+1 *** 2000", Role: domain.AccountRoleScoutAnalyst},
		},
		catalogs: map[domain.SourceCatalog][]domain.Channel{
			domain.SourceCatalogOutbound: {
				{ID: "outbound", Title: "Outbound channel", Link: "https://t.me/outbound", Topic: "Sales"},
				{ID: "direct", Title: "Direct member"},
			},
			domain.SourceCatalogScout: {
				{ID: "scout", Title: "Scout channel", Link: "https://t.me/scout", Topic: "Research"},
			},
		},
		memberships: map[channelModerationMembershipKey][]domain.ChannelMembership{
			{catalog: domain.SourceCatalogOutbound, channelID: "outbound"}: {
				{AccountID: "outbound-pending", ChannelID: "outbound", Status: "pending_approval", RequestSubmittedAt: &secondRequestAt},
				{AccountID: "outbound-member", ChannelID: "outbound", IsMember: true, Status: "member", RequestSubmittedAt: &requestedAt, JoinedAt: &joinedAt},
			},
			{catalog: domain.SourceCatalogOutbound, channelID: "direct"}: {
				{AccountID: "outbound-member", ChannelID: "direct", IsMember: true, Status: "member"},
			},
			{catalog: domain.SourceCatalogScout, channelID: "scout"}: {
				{AccountID: "scout-member", ChannelID: "scout", IsMember: true, Status: "member", RequestSubmittedAt: &scoutRequestedAt, JoinedAt: &scoutJoinedAt},
				{AccountID: "scout-member-late", ChannelID: "scout", IsMember: true, Status: "member", RequestSubmittedAt: &scoutSecondRequestAt, JoinedAt: &scoutLatestJoinedAt},
			},
		},
	}

	rows, err := NewChannelModerationService(store).List(context.Background(), now)
	require.NoError(t, err)
	require.Equal(t, []string{"accounts", "catalog:outbound", "membership:outbound:outbound", "membership:outbound:direct", "catalog:scout", "membership:scout:scout"}, store.calls)
	require.Len(t, rows, 2)

	require.Equal(t, domain.SourceCatalogOutbound, rows[0].Catalog)
	require.Equal(t, domain.ID("outbound"), rows[0].ChannelID)
	require.Equal(t, "Outbound channel", rows[0].Title)
	require.Equal(t, "https://t.me/outbound", rows[0].Link)
	require.Equal(t, "Sales", rows[0].Topic)
	require.Equal(t, "partial", rows[0].Status)
	require.Equal(t, 2, rows[0].Applications)
	require.Equal(t, 1, rows[0].Joined)
	require.Equal(t, 1, rows[0].Pending)
	require.Equal(t, requestedAt, rows[0].FirstRequestAt)
	require.Equal(t, 45*time.Minute, rows[0].Duration)
	require.Equal(t, []ChannelModerationAccount{
		{AccountKey: "outbound-member", Title: "Member name", Role: domain.AccountRoleSpammer, RequestSubmittedAt: requestedAt, JoinedAt: &joinedAt, Duration: 30 * time.Minute, Status: "member"},
		{AccountKey: "outbound-pending", Title: "+1 *** 1000", Role: domain.AccountRoleSpammer, RequestSubmittedAt: secondRequestAt, Duration: 30 * time.Minute, Status: "pending_approval"},
	}, rows[0].Accounts)

	require.Equal(t, domain.SourceCatalogScout, rows[1].Catalog)
	require.Equal(t, "member", rows[1].Status)
	require.Equal(t, 2, rows[1].Applications)
	require.Equal(t, 2, rows[1].Joined)
	require.Zero(t, rows[1].Pending)
	require.Equal(t, scoutLatestJoinedAt.Sub(scoutRequestedAt), rows[1].Duration)
	require.Equal(t, "Telegram account", rows[1].Accounts[0].Title)
}

func TestChannelModerationListIncludesScheduledJoiningAccounts(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	firstDue := now.Add(-2 * time.Hour)
	secondDue := now.Add(time.Hour)
	store := &channelModerationStoreStub{
		accounts: []domain.Account{
			{ID: "first", DisplayName: "First account", Role: domain.AccountRoleSpammer},
			{ID: "second", DisplayName: "Second account", Role: domain.AccountRoleScoutAnalyst},
		},
		catalogs: map[domain.SourceCatalog][]domain.Channel{
			domain.SourceCatalogOutbound: {{ID: "scheduled", Title: "Scheduled channel", Active: true}},
		},
		memberships: map[channelModerationMembershipKey][]domain.ChannelMembership{
			{catalog: domain.SourceCatalogOutbound, channelID: "scheduled"}: {
				{AccountID: "first", ChannelID: "scheduled", Status: "joining", JoinNotBefore: &firstDue},
				{AccountID: "second", ChannelID: "scheduled", Status: "joining", JoinNotBefore: &secondDue},
			},
		},
	}

	rows, err := NewChannelModerationService(store).List(context.Background(), now)

	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "joining", rows[0].Status)
	require.Zero(t, rows[0].Applications)
	require.Zero(t, rows[0].Joined)
	require.Equal(t, 2, rows[0].Pending)
	require.Len(t, rows[0].Accounts, 2)
	require.Equal(t, []string{"joining", "joining"}, []string{rows[0].Accounts[0].Status, rows[0].Accounts[1].Status})
}

func TestChannelModerationListIncludesCompletedDirectJoinWithRemainingQueue(t *testing.T) {
	now := time.Date(2026, 7, 27, 9, 0, 0, 0, time.UTC)
	joinedAt := now.Add(-30 * time.Minute)
	store := &channelModerationStoreStub{
		accounts: []domain.Account{
			{ID: "joined", DisplayName: "Joined account"},
			{ID: "joining-first", DisplayName: "First queued account"},
			{ID: "joining-second", DisplayName: "Second queued account"},
		},
		catalogs: map[domain.SourceCatalog][]domain.Channel{
			domain.SourceCatalogOutbound: {{ID: "channel", Title: "Mixed join progress", Active: true}},
		},
		memberships: map[channelModerationMembershipKey][]domain.ChannelMembership{
			{catalog: domain.SourceCatalogOutbound, channelID: "channel"}: {
				{AccountID: "joined", ChannelID: "channel", Status: "member", IsMember: true, JoinedAt: &joinedAt},
				{AccountID: "joining-first", ChannelID: "channel", Status: "joining"},
				{AccountID: "joining-second", ChannelID: "channel", Status: "joining"},
			},
		},
	}

	rows, err := NewChannelModerationService(store).List(context.Background(), now)

	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Zero(t, rows[0].Applications)
	require.Equal(t, 1, rows[0].Joined)
	require.Equal(t, 2, rows[0].Pending)
	require.Equal(t, "partial", rows[0].Status)
	require.Len(t, rows[0].Accounts, 3)
	require.Equal(t, []string{"member", "joining", "joining"}, []string{
		rows[0].Accounts[0].Status,
		rows[0].Accounts[1].Status,
		rows[0].Accounts[2].Status,
	})
	require.Equal(t, &joinedAt, rows[0].Accounts[0].JoinedAt)
}

func TestChannelModerationListCountsRequestedErrorAndFloodWaitAsPending(t *testing.T) {
	requestedAt := time.Date(2026, 7, 18, 8, 0, 0, 0, time.UTC)
	store := &channelModerationStoreStub{
		accounts: []domain.Account{
			{ID: "error", DisplayName: "Error account"},
			{ID: "flood", DisplayName: "Flood account"},
		},
		catalogs: map[domain.SourceCatalog][]domain.Channel{
			domain.SourceCatalogOutbound: {{ID: "channel", Title: "Moderated"}},
		},
		memberships: map[channelModerationMembershipKey][]domain.ChannelMembership{
			{catalog: domain.SourceCatalogOutbound, channelID: "channel"}: {
				{AccountID: "flood", ChannelID: "channel", Status: "flood_wait", RequestSubmittedAt: &requestedAt},
				{AccountID: "error", ChannelID: "channel", Status: "error", RequestSubmittedAt: &requestedAt},
			},
		},
	}

	rows, err := NewChannelModerationService(store).List(context.Background(), requestedAt.Add(time.Hour))
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "pending_approval", rows[0].Status)
	require.Equal(t, 2, rows[0].Applications)
	require.Zero(t, rows[0].Joined)
	require.Equal(t, 2, rows[0].Pending)
	require.Equal(t, []string{"error", "flood"}, []string{string(rows[0].Accounts[0].AccountKey), string(rows[0].Accounts[1].AccountKey)})
	require.Equal(t, []string{"error", "flood_wait"}, []string{rows[0].Accounts[0].Status, rows[0].Accounts[1].Status})
}

func TestChannelModerationListUsesNowForPendingRowsWithHistoricalJoinedAt(t *testing.T) {
	requestedAt := time.Date(2026, 7, 18, 8, 0, 0, 0, time.UTC)
	historicalJoinedAt := requestedAt.Add(10 * time.Minute)
	now := requestedAt.Add(time.Hour)
	store := &channelModerationStoreStub{
		accounts: []domain.Account{
			{ID: "error", DisplayName: "Error account"},
			{ID: "flood", DisplayName: "Flood account"},
		},
		catalogs: map[domain.SourceCatalog][]domain.Channel{
			domain.SourceCatalogOutbound: {{ID: "channel", Title: "Moderated"}},
		},
		memberships: map[channelModerationMembershipKey][]domain.ChannelMembership{
			{catalog: domain.SourceCatalogOutbound, channelID: "channel"}: {
				{AccountID: "flood", ChannelID: "channel", Status: "flood_wait", RequestSubmittedAt: &requestedAt, JoinedAt: &historicalJoinedAt},
				{AccountID: "error", ChannelID: "channel", Status: "error", RequestSubmittedAt: &requestedAt, JoinedAt: &historicalJoinedAt},
			},
		},
	}

	rows, err := NewChannelModerationService(store).List(context.Background(), now)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, 2, rows[0].Pending)
	require.Equal(t, []time.Duration{time.Hour, time.Hour}, []time.Duration{rows[0].Accounts[0].Duration, rows[0].Accounts[1].Duration})
}

func TestChannelModerationListClampsNegativeDuration(t *testing.T) {
	now := time.Date(2026, 7, 18, 8, 0, 0, 0, time.UTC)
	requestedAt := now.Add(time.Minute)
	store := &channelModerationStoreStub{
		catalogs: map[domain.SourceCatalog][]domain.Channel{
			domain.SourceCatalogScout: {{ID: "future", Title: "Future"}},
		},
		memberships: map[channelModerationMembershipKey][]domain.ChannelMembership{
			{catalog: domain.SourceCatalogScout, channelID: "future"}: {
				{AccountID: "unknown", ChannelID: "future", Status: "pending_approval", RequestSubmittedAt: &requestedAt},
			},
		},
	}

	rows, err := NewChannelModerationService(store).List(context.Background(), now)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Zero(t, rows[0].Duration)
	require.Zero(t, rows[0].Accounts[0].Duration)
	require.Equal(t, "Telegram account", rows[0].Accounts[0].Title)
}

func TestChannelModerationListWrapsStoreErrorsWithContext(t *testing.T) {
	t.Run("catalog", func(t *testing.T) {
		store := &channelModerationStoreStub{catalogErrs: map[domain.SourceCatalog]error{domain.SourceCatalogOutbound: errors.New("catalog unavailable")}}

		_, err := NewChannelModerationService(store).List(context.Background(), time.Now())
		require.Error(t, err)
		require.ErrorContains(t, err, "list outbound catalog")
		require.ErrorIs(t, err, store.catalogErrs[domain.SourceCatalogOutbound])
	})

	t.Run("channel", func(t *testing.T) {
		store := &channelModerationStoreStub{
			catalogs: map[domain.SourceCatalog][]domain.Channel{
				domain.SourceCatalogOutbound: {{ID: "channel", Title: "Context channel"}},
			},
			membershipErrs: map[channelModerationMembershipKey]error{
				{catalog: domain.SourceCatalogOutbound, channelID: "channel"}: errors.New("memberships unavailable"),
			},
		}

		_, err := NewChannelModerationService(store).List(context.Background(), time.Now())
		require.Error(t, err)
		require.ErrorContains(t, err, "list memberships for outbound channel channel")
		require.ErrorIs(t, err, store.membershipErrs[channelModerationMembershipKey{catalog: domain.SourceCatalogOutbound, channelID: "channel"}])
	})
}

type channelModerationMembershipKey struct {
	catalog   domain.SourceCatalog
	channelID domain.ID
}

type channelModerationStoreStub struct {
	accounts       []domain.Account
	accountsErr    error
	catalogs       map[domain.SourceCatalog][]domain.Channel
	catalogErrs    map[domain.SourceCatalog]error
	memberships    map[channelModerationMembershipKey][]domain.ChannelMembership
	membershipErrs map[channelModerationMembershipKey]error
	calls          []string
}

func (s *channelModerationStoreStub) ListAccounts(context.Context) ([]domain.Account, error) {
	s.calls = append(s.calls, "accounts")
	return s.accounts, s.accountsErr
}

func (s *channelModerationStoreStub) ListCatalog(_ context.Context, catalog domain.SourceCatalog) ([]domain.Channel, error) {
	s.calls = append(s.calls, fmt.Sprintf("catalog:%s", catalog))
	return s.catalogs[catalog], s.catalogErrs[catalog]
}

func (s *channelModerationStoreStub) ListMemberships(_ context.Context, catalog domain.SourceCatalog, channelID domain.ID) ([]domain.ChannelMembership, error) {
	s.calls = append(s.calls, fmt.Sprintf("membership:%s:%s", catalog, channelID))
	key := channelModerationMembershipKey{catalog: catalog, channelID: channelID}
	return s.memberships[key], s.membershipErrs[key]
}
