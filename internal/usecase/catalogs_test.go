package usecase_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"telegram-companion/internal/domain"
	"telegram-companion/internal/usecase"

	"github.com/stretchr/testify/require"
)

type catalogStoreStub struct {
	rows              map[domain.SourceCatalog][]domain.Channel
	accounts          []domain.Account
	memberships       map[catalogMembershipKey]domain.ChannelMembership
	membershipSaves   []catalogMembershipWrite
	membershipDeletes []catalogMembershipKey
	telegramLeaves    []catalogMembershipKey
	activationCalls   []catalogActivationWrite
	removalCalls      []catalogRemovalWrite
	leaveCalls        []catalogMembershipLeaveWrite
	retryCalls        []catalogMembershipRetryWrite
	activationErr     error
}

type catalogMembershipKey struct {
	catalog   domain.SourceCatalog
	channelID domain.ID
	accountID domain.ID
}

type catalogMembershipWrite struct {
	catalog    domain.SourceCatalog
	membership domain.ChannelMembership
}

type catalogActivationWrite struct {
	catalog     domain.SourceCatalog
	channel     domain.Channel
	memberships []domain.ChannelMembership
}

type catalogRemovalWrite struct {
	catalog domain.SourceCatalog
	ids     []domain.ID
}

type catalogMembershipLeaveWrite struct {
	catalog   domain.SourceCatalog
	channelID domain.ID
}

type catalogMembershipRetryWrite struct {
	catalog   domain.SourceCatalog
	channelID domain.ID
	retryAt   time.Time
}

type catalogStoreWithoutActivation struct{ usecase.CatalogStore }

type joinDelaySourceStub struct {
	delays []int
	mins   []int
	maxs   []int
	err    error
}

func (s *joinDelaySourceStub) Minutes(min, max int) (int, error) {
	s.mins = append(s.mins, min)
	s.maxs = append(s.maxs, max)
	if s.err != nil {
		return 0, s.err
	}
	if len(s.delays) == 0 {
		return 0, errors.New("unexpected join delay")
	}
	delay := s.delays[0]
	s.delays = s.delays[1:]
	return delay, nil
}

func (s *catalogStoreStub) ListCatalog(_ context.Context, catalog domain.SourceCatalog) ([]domain.Channel, error) {
	return append([]domain.Channel(nil), s.rows[catalog]...), nil
}

func (s *catalogStoreStub) SaveCatalog(_ context.Context, catalog domain.SourceCatalog, channel domain.Channel) error {
	rows := s.rows[catalog]
	for index := range rows {
		if rows[index].ID == channel.ID {
			rows[index] = channel
			s.rows[catalog] = rows
			return nil
		}
	}
	s.rows[catalog] = append(rows, channel)
	return nil
}

func (s *catalogStoreStub) SetCatalogTopics(_ context.Context, catalog domain.SourceCatalog, ids []domain.ID, topic string) error {
	rows := append([]domain.Channel(nil), s.rows[catalog]...)
	known := make(map[domain.ID]int, len(rows))
	for index := range rows {
		known[rows[index].ID] = index
	}
	for _, id := range ids {
		if _, ok := known[id]; !ok {
			return usecase.ErrChannelNotFound
		}
	}
	for _, id := range ids {
		rows[known[id]].Topic = topic
	}
	s.rows[catalog] = rows
	return nil
}

// Catalog lifecycle capability signatures used by the channel activation flow.
func (s *catalogStoreStub) ListAccounts(context.Context) ([]domain.Account, error) {
	return append([]domain.Account(nil), s.accounts...), nil
}

func (s *catalogStoreStub) ListMemberships(_ context.Context, catalog domain.SourceCatalog, channelID domain.ID) ([]domain.ChannelMembership, error) {
	memberships := make([]domain.ChannelMembership, 0)
	for key, membership := range s.memberships {
		if key.catalog == catalog && key.channelID == channelID {
			memberships = append(memberships, membership)
		}
	}
	return memberships, nil
}

func (s *catalogStoreStub) SaveMembership(_ context.Context, catalog domain.SourceCatalog, membership domain.ChannelMembership) error {
	if s.memberships == nil {
		s.memberships = make(map[catalogMembershipKey]domain.ChannelMembership)
	}
	key := catalogMembershipKey{catalog: catalog, channelID: membership.ChannelID, accountID: membership.AccountID}
	s.memberships[key] = membership
	s.membershipSaves = append(s.membershipSaves, catalogMembershipWrite{catalog: catalog, membership: membership})
	return nil
}

func (s *catalogStoreStub) ActivateCatalogWithMemberships(_ context.Context, catalog domain.SourceCatalog, channel domain.Channel, memberships []domain.ChannelMembership) error {
	if s.activationErr != nil {
		return s.activationErr
	}
	if s.memberships == nil {
		s.memberships = make(map[catalogMembershipKey]domain.ChannelMembership)
	}
	for index := range memberships {
		membership := memberships[index]
		s.memberships[catalogMembershipKey{catalog: catalog, channelID: membership.ChannelID, accountID: membership.AccountID}] = membership
	}
	if err := s.SaveCatalog(context.Background(), catalog, channel); err != nil {
		return err
	}
	s.activationCalls = append(s.activationCalls, catalogActivationWrite{catalog: catalog, channel: channel, memberships: append([]domain.ChannelMembership(nil), memberships...)})
	return nil
}

func (s *catalogStoreStub) DeleteMembership(_ context.Context, catalog domain.SourceCatalog, accountID, channelID domain.ID) error {
	key := catalogMembershipKey{catalog: catalog, channelID: channelID, accountID: accountID}
	delete(s.memberships, key)
	s.membershipDeletes = append(s.membershipDeletes, key)
	return nil
}

func (s *catalogStoreStub) LeaveChannel(_ context.Context, catalog domain.SourceCatalog, accountID, channelID domain.ID) error {
	s.telegramLeaves = append(s.telegramLeaves, catalogMembershipKey{catalog: catalog, channelID: channelID, accountID: accountID})
	return nil
}

func (s *catalogStoreStub) RequestCatalogRemoval(_ context.Context, catalog domain.SourceCatalog, ids []domain.ID) error {
	s.removalCalls = append(s.removalCalls, catalogRemovalWrite{catalog: catalog, ids: append([]domain.ID(nil), ids...)})
	return nil
}

func (s *catalogStoreStub) RequestCatalogLeave(_ context.Context, catalog domain.SourceCatalog, channelID domain.ID) error {
	s.leaveCalls = append(s.leaveCalls, catalogMembershipLeaveWrite{catalog: catalog, channelID: channelID})
	rows := s.rows[catalog]
	for index := range rows {
		if rows[index].ID == channelID {
			rows[index].Active = false
			rows[index].Status = domain.ChannelPaused
			s.rows[catalog] = rows
			return nil
		}
	}
	return usecase.ErrChannelNotFound
}

func (s *catalogStoreStub) RetryPendingCatalogMemberships(_ context.Context, catalog domain.SourceCatalog, channelID domain.ID, retryAt time.Time) (int, error) {
	s.retryCalls = append(s.retryCalls, catalogMembershipRetryWrite{catalog: catalog, channelID: channelID, retryAt: retryAt})
	updated := 0
	for key, membership := range s.memberships {
		if key.catalog != catalog || key.channelID != channelID || membership.IsMember || membership.Status != "pending_approval" {
			continue
		}
		membership.Status = "joining"
		membership.LastCheckAt = nil
		membership.RequestSubmittedAt = nil
		membership.JoinNotBefore = nil
		membership.LastError = ""
		s.memberships[key] = membership
		updated++
	}
	return updated, nil
}

func TestDeleteCatalogEntriesDelegatesUniqueIDsToRemovalStore(t *testing.T) {
	ctx := context.Background()
	store := &catalogStoreStub{rows: map[domain.SourceCatalog][]domain.Channel{
		domain.SourceCatalogOutbound: {
			{ID: "first", Link: "https://t.me/first"},
			{ID: "second", Link: "https://t.me/second"},
		},
	}}
	service := usecase.NewCatalogService(store)

	rows, err := service.Delete(ctx, domain.SourceCatalogOutbound, []domain.ID{"first", "second", "first"})

	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, []catalogRemovalWrite{{catalog: domain.SourceCatalogOutbound, ids: []domain.ID{"first", "second"}}}, store.removalCalls)
}

func TestBulkScoutImportRequiresAndAppliesOneTopic(t *testing.T) {
	ctx := context.Background()
	store := &catalogStoreStub{rows: make(map[domain.SourceCatalog][]domain.Channel)}
	service := usecase.NewCatalogService(store)

	_, err := service.AddLinks(ctx, domain.SourceCatalogScout,
		[]string{"https://t.me/one", "https://t.me/two"}, "")
	require.ErrorIs(t, err, usecase.ErrTopicRequired)
	require.Empty(t, store.rows[domain.SourceCatalogScout])

	got, err := service.AddLinks(ctx, domain.SourceCatalogScout,
		[]string{"https://t.me/one", "https://t.me/two", "https://t.me/one"}, "  Личные финансы  ")
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, "Личные финансы", got[0].Topic)
	require.Equal(t, "Личные финансы", got[1].Topic)
}

func TestAddLinksCreatesInactivePausedChannels(t *testing.T) {
	ctx := context.Background()
	store := &catalogStoreStub{rows: make(map[domain.SourceCatalog][]domain.Channel)}
	service := usecase.NewCatalogService(store)

	rows, err := service.AddLinks(ctx, domain.SourceCatalogOutbound,
		[]string{"https://t.me/+privateInvite", "https://t.me/public_channel"}, "topic")

	require.NoError(t, err)
	require.Len(t, rows, 2)
	for _, row := range rows {
		require.False(t, row.Active, "new channel %q must require explicit activation", row.Link)
		require.Equal(t, domain.ChannelPaused, row.Status)
	}
}

func TestCatalogJoinScheduleUsesCumulativeDelaysForAllRoles(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 18, 8, 0, 0, 0, time.UTC)
	delays := &joinDelaySourceStub{delays: []int{10, 60, 15}}
	store := &catalogStoreStub{
		rows: map[domain.SourceCatalog][]domain.Channel{
			domain.SourceCatalogOutbound: {{ID: "channel-1", Link: "https://t.me/room", Status: domain.ChannelPaused}},
		},
		accounts: []domain.Account{
			{ID: "spammer-1", Role: domain.AccountRoleSpammer, Status: domain.AccountActive},
			{ID: "scout-1", Role: domain.AccountRoleScoutAnalyst, Status: domain.AccountActive},
			{ID: "spammer-2", Role: domain.AccountRoleSpammer, Status: domain.AccountActive},
			{ID: "scout-2", Role: domain.AccountRoleScoutAnalyst, Status: domain.AccountActive},
			{ID: "paused", Role: domain.AccountRoleSpammer, Status: domain.AccountPaused},
		},
	}
	service := usecase.NewCatalogServiceWithJoinSchedule(store, delays, func() time.Time { return now })

	rows, err := service.Join(ctx, domain.SourceCatalogOutbound, "channel-1")

	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.False(t, rows[0].Active)
	require.Equal(t, domain.ChannelJoining, rows[0].Status)
	require.Len(t, store.activationCalls, 1)
	require.Equal(t, []domain.ChannelMembership{
		{AccountID: "spammer-1", ChannelID: "channel-1", Status: "joining", JoinNotBefore: timePtr(now), RestDurationHours: 36},
		{AccountID: "scout-1", ChannelID: "channel-1", Status: "joining", JoinNotBefore: timePtr(now.Add(10 * time.Minute)), RestDurationHours: 36},
		{AccountID: "spammer-2", ChannelID: "channel-1", Status: "joining", JoinNotBefore: timePtr(now.Add(70 * time.Minute)), RestDurationHours: 36},
		{AccountID: "scout-2", ChannelID: "channel-1", Status: "joining", JoinNotBefore: timePtr(now.Add(85 * time.Minute)), RestDurationHours: 36},
	}, store.activationCalls[0].memberships)
	require.Equal(t, []int{10, 10, 10}, delays.mins)
	require.Equal(t, []int{60, 60, 60}, delays.maxs)
}

func TestCatalogJoinRejectsRemovalRequestedChannel(t *testing.T) {
	ctx := context.Background()
	requestedAt := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	store := &catalogStoreStub{rows: map[domain.SourceCatalog][]domain.Channel{
		domain.SourceCatalogOutbound: {{
			ID: "channel-1", Link: "https://t.me/room", Status: domain.ChannelPaused, RemovalRequestedAt: &requestedAt,
		}},
	}}
	service := usecase.NewCatalogServiceWithJoinSchedule(store, &joinDelaySourceStub{}, func() time.Time { return requestedAt })

	_, err := service.Join(ctx, domain.SourceCatalogOutbound, "channel-1")

	require.ErrorContains(t, err, "removal requested")
	require.Empty(t, store.activationCalls)
	require.False(t, store.rows[domain.SourceCatalogOutbound][0].Active)
}

func TestCatalogJoinScheduleUsesConfiguredRange(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 18, 8, 0, 0, 0, time.UTC)
	delays := &joinDelaySourceStub{delays: []int{25}}
	store := &catalogStoreStub{
		rows: map[domain.SourceCatalog][]domain.Channel{
			domain.SourceCatalogOutbound: {{ID: "channel-1", Link: "https://t.me/room", Status: domain.ChannelPaused}},
		},
		accounts: []domain.Account{{ID: "one", Status: domain.AccountActive}, {ID: "two", Status: domain.AccountActive}},
	}
	service := usecase.NewCatalogServiceWithJoinSchedule(store, delays, func() time.Time { return now })
	require.NoError(t, service.SetJoinIntervalRange(20, 30))

	_, err := service.Join(ctx, domain.SourceCatalogOutbound, "channel-1")

	require.NoError(t, err)
	require.Equal(t, []int{20}, delays.mins)
	require.Equal(t, []int{30}, delays.maxs)
	require.Equal(t, now.Add(25*time.Minute), *store.activationCalls[0].memberships[1].JoinNotBefore)
}

func TestCatalogJoinSchedulesAllMembershipsImmediatelyWhenJoinIntervalDisabled(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 18, 8, 0, 0, 0, time.UTC)
	store := &catalogStoreStub{
		rows: map[domain.SourceCatalog][]domain.Channel{
			domain.SourceCatalogOutbound: {{ID: "channel-1", Link: "https://t.me/room", Status: domain.ChannelPaused}},
		},
		accounts: []domain.Account{
			{ID: "one", Status: domain.AccountActive},
			{ID: "two", Status: domain.AccountActive},
			{ID: "three", Status: domain.AccountActive},
		},
	}
	service := usecase.NewCatalogServiceWithJoinSchedule(store, nil, func() time.Time { return now })
	service.SetJoinIntervalEnabled(false)

	_, err := service.Join(ctx, domain.SourceCatalogOutbound, "channel-1")

	require.NoError(t, err)
	require.Len(t, store.activationCalls, 1)
	require.Len(t, store.activationCalls[0].memberships, 3)
	for _, membership := range store.activationCalls[0].memberships {
		require.Equal(t, now, *membership.JoinNotBefore)
	}
}

func TestCatalogJoinSnapshotsConfiguredGroupRestHours(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 18, 8, 0, 0, 0, time.UTC)
	store := &catalogStoreStub{
		rows: map[domain.SourceCatalog][]domain.Channel{
			domain.SourceCatalogOutbound: {{ID: "channel-1", Link: "https://t.me/room", Status: domain.ChannelPaused}},
		},
		accounts: []domain.Account{{ID: "one", Status: domain.AccountActive}, {ID: "two", Status: domain.AccountActive}},
	}
	service := usecase.NewCatalogServiceWithJoinSchedule(store, &joinDelaySourceStub{delays: []int{10}}, func() time.Time { return now })
	require.NoError(t, service.SetGroupRestHours(72))

	_, err := service.Join(ctx, domain.SourceCatalogOutbound, "channel-1")

	require.NoError(t, err)
	require.Len(t, store.activationCalls, 1)
	for _, membership := range store.activationCalls[0].memberships {
		require.Equal(t, 72, membership.RestDurationHours)
	}
}

func TestCatalogJoinDisablesGroupRestForNewMemberships(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 18, 8, 0, 0, 0, time.UTC)
	store := &catalogStoreStub{
		rows: map[domain.SourceCatalog][]domain.Channel{
			domain.SourceCatalogOutbound: {{ID: "channel-1", Link: "https://t.me/room", Status: domain.ChannelPaused}},
		},
		accounts: []domain.Account{{ID: "one", Status: domain.AccountActive}, {ID: "two", Status: domain.AccountActive}},
	}
	service := usecase.NewCatalogServiceWithJoinSchedule(store, &joinDelaySourceStub{delays: []int{10}}, func() time.Time { return now })
	service.SetGroupRestEnabled(false)

	_, err := service.Join(ctx, domain.SourceCatalogOutbound, "channel-1")

	require.NoError(t, err)
	require.Len(t, store.activationCalls, 1)
	for _, membership := range store.activationCalls[0].memberships {
		require.Zero(t, membership.RestDurationHours)
	}
}

func TestCatalogJoinScheduleRejectsInvalidConfiguredRange(t *testing.T) {
	ctx := context.Background()
	store := &catalogStoreStub{
		rows: map[domain.SourceCatalog][]domain.Channel{
			domain.SourceCatalogOutbound: {{ID: "channel-1", Link: "https://t.me/room", Status: domain.ChannelPaused}},
		},
		accounts: []domain.Account{{ID: "one", Status: domain.AccountActive}, {ID: "two", Status: domain.AccountActive}},
	}
	service := usecase.NewCatalogServiceWithJoinSchedule(store, &joinDelaySourceStub{delays: []int{10}}, time.Now)

	configureErr := service.SetJoinIntervalRange(30, 20)
	_, activationErr := service.Join(ctx, domain.SourceCatalogOutbound, "channel-1")

	require.ErrorIs(t, configureErr, domain.ErrInvalidJoinInterval)
	require.ErrorIs(t, activationErr, domain.ErrInvalidJoinInterval)
	require.Empty(t, store.activationCalls)
	require.False(t, store.rows[domain.SourceCatalogOutbound][0].Active)
}

func TestCatalogJoinScheduleIsStrictlyMonotonicForOneHundredAccounts(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 18, 8, 0, 0, 0, time.UTC)
	accounts := make([]domain.Account, 100)
	for index := range accounts {
		accounts[index] = domain.Account{ID: domain.ID(fmt.Sprintf("account-%03d", index)), Role: domain.AccountRoleSpammer, Status: domain.AccountActive}
	}
	delays := make([]int, 99)
	for index := range delays {
		delays[index] = 10
	}
	store := &catalogStoreStub{rows: map[domain.SourceCatalog][]domain.Channel{
		domain.SourceCatalogOutbound: {{ID: "channel-1", Link: "https://t.me/room", Status: domain.ChannelPaused}},
	}, accounts: accounts}
	service := usecase.NewCatalogServiceWithJoinSchedule(store, &joinDelaySourceStub{delays: delays}, func() time.Time { return now })

	_, err := service.Join(ctx, domain.SourceCatalogOutbound, "channel-1")

	require.NoError(t, err)
	memberships := store.activationCalls[0].memberships
	require.Len(t, memberships, 100)
	require.Equal(t, now, *memberships[0].JoinNotBefore)
	for index := 1; index < len(memberships); index++ {
		require.True(t, memberships[index].JoinNotBefore.After(*memberships[index-1].JoinNotBefore))
	}
}

func TestCatalogJoinScheduleLeavesMemberAndPendingRowsUntouched(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 18, 8, 0, 0, 0, time.UTC)
	memberKey := catalogMembershipKey{catalog: domain.SourceCatalogOutbound, channelID: "channel-1", accountID: "member"}
	pendingKey := catalogMembershipKey{catalog: domain.SourceCatalogOutbound, channelID: "channel-1", accountID: "pending"}
	member := domain.ChannelMembership{AccountID: memberKey.accountID, ChannelID: memberKey.channelID, IsMember: true, Status: "member", JoinedAt: timePtr(now.Add(-time.Hour))}
	pending := domain.ChannelMembership{AccountID: pendingKey.accountID, ChannelID: pendingKey.channelID, Status: "pending_approval", RequestSubmittedAt: timePtr(now.Add(-time.Minute)), JoinNotBefore: timePtr(now.Add(-30 * time.Minute))}
	store := &catalogStoreStub{
		rows:        map[domain.SourceCatalog][]domain.Channel{domain.SourceCatalogOutbound: {{ID: "channel-1", Link: "https://t.me/room", Status: domain.ChannelPaused}}},
		accounts:    []domain.Account{{ID: memberKey.accountID, Status: domain.AccountActive}, {ID: pendingKey.accountID, Status: domain.AccountActive}, {ID: "new", Status: domain.AccountActive}},
		memberships: map[catalogMembershipKey]domain.ChannelMembership{memberKey: member, pendingKey: pending},
	}
	service := usecase.NewCatalogServiceWithJoinSchedule(store, &joinDelaySourceStub{}, func() time.Time { return now })

	_, err := service.Join(ctx, domain.SourceCatalogOutbound, "channel-1")

	require.NoError(t, err)
	require.Equal(t, member, store.memberships[memberKey])
	require.Equal(t, pending, store.memberships[pendingKey])
	require.Equal(t, []domain.ChannelMembership{{
		AccountID: "new", ChannelID: "channel-1", Status: "joining",
		JoinNotBefore: timePtr(now), RestDurationHours: 36,
	}}, store.activationCalls[0].memberships)
}

func TestCatalogRetryJoinRequeuesOnlyPendingApprovalMembershipsImmediately(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	pendingKey := catalogMembershipKey{catalog: domain.SourceCatalogOutbound, channelID: "channel-1", accountID: "pending"}
	memberKey := catalogMembershipKey{catalog: domain.SourceCatalogOutbound, channelID: "channel-1", accountID: "member"}
	requestedAt := now.Add(-24 * time.Hour)
	lastCheckAt := now.Add(-time.Minute)
	pending := domain.ChannelMembership{
		AccountID: pendingKey.accountID, ChannelID: pendingKey.channelID, Status: "pending_approval",
		RequestSubmittedAt: &requestedAt, LastCheckAt: &lastCheckAt, JoinNotBefore: &requestedAt,
		LastError: "old error",
	}
	member := domain.ChannelMembership{AccountID: memberKey.accountID, ChannelID: memberKey.channelID, IsMember: true, Status: "member"}
	store := &catalogStoreStub{
		rows: map[domain.SourceCatalog][]domain.Channel{
			domain.SourceCatalogOutbound: {{ID: "channel-1", Link: "https://t.me/room", Active: true, Status: domain.ChannelJoining}},
		},
		memberships: map[catalogMembershipKey]domain.ChannelMembership{pendingKey: pending, memberKey: member},
	}
	service := usecase.NewCatalogServiceWithJoinSchedule(store, &joinDelaySourceStub{}, func() time.Time { return now })

	rows, err := service.RetryJoin(ctx, domain.SourceCatalogOutbound, "channel-1")

	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, []catalogMembershipRetryWrite{{catalog: domain.SourceCatalogOutbound, channelID: "channel-1", retryAt: now}}, store.retryCalls)
	require.Equal(t, member, store.memberships[memberKey], "joined accounts must not be requeued")
	retried := store.memberships[pendingKey]
	require.Equal(t, "joining", retried.Status)
	require.False(t, retried.IsMember)
	require.Nil(t, retried.RequestSubmittedAt)
	require.Nil(t, retried.LastCheckAt)
	require.Nil(t, retried.JoinNotBefore)
	require.Empty(t, retried.LastError)
}

func TestCatalogJoinScheduleRejectsInvalidDelayBeforeActivation(t *testing.T) {
	ctx := context.Background()
	store := &catalogStoreStub{
		rows:     map[domain.SourceCatalog][]domain.Channel{domain.SourceCatalogOutbound: {{ID: "channel-1", Link: "https://t.me/room", Status: domain.ChannelPaused}}},
		accounts: []domain.Account{{ID: "one", Status: domain.AccountActive}, {ID: "two", Status: domain.AccountActive}},
	}
	service := usecase.NewCatalogServiceWithJoinSchedule(store, &joinDelaySourceStub{delays: []int{9}}, time.Now)

	_, err := service.Join(ctx, domain.SourceCatalogOutbound, "channel-1")

	require.ErrorIs(t, err, usecase.ErrInvalidJoinDelay)
	require.Empty(t, store.activationCalls)
	require.False(t, store.rows[domain.SourceCatalogOutbound][0].Active)
}

func TestCatalogJoinRequiresAtomicActivationStore(t *testing.T) {
	ctx := context.Background()
	store := &catalogStoreStub{rows: map[domain.SourceCatalog][]domain.Channel{
		domain.SourceCatalogOutbound: {{ID: "channel-1", Link: "https://t.me/room", Status: domain.ChannelPaused}},
	}}
	service := usecase.NewCatalogServiceWithJoinSchedule(catalogStoreWithoutActivation{CatalogStore: store}, &joinDelaySourceStub{}, time.Now)

	_, err := service.Join(ctx, domain.SourceCatalogOutbound, "channel-1")

	require.ErrorIs(t, err, usecase.ErrCatalogActivationStoreUnsupported)
	require.False(t, store.rows[domain.SourceCatalogOutbound][0].Active)
}

func TestCatalogToggleActiveJoinSchedulePreservesExistingActiveSchedule(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 18, 8, 0, 0, 0, time.UTC)
	key := catalogMembershipKey{catalog: domain.SourceCatalogOutbound, channelID: "channel-1", accountID: "account-1"}
	existing := domain.ChannelMembership{AccountID: key.accountID, ChannelID: key.channelID, Status: "joining", JoinNotBefore: timePtr(now.Add(20 * time.Minute))}
	store := &catalogStoreStub{
		rows:        map[domain.SourceCatalog][]domain.Channel{domain.SourceCatalogOutbound: {{ID: key.channelID, Link: "https://t.me/room", Status: domain.ChannelJoining, Active: true}}},
		memberships: map[catalogMembershipKey]domain.ChannelMembership{key: existing},
	}
	service := usecase.NewCatalogServiceWithJoinSchedule(store, &joinDelaySourceStub{}, func() time.Time { return now })
	require.NoError(t, service.SetJoinIntervalRange(20, 30))

	rows, err := service.Toggle(ctx, domain.SourceCatalogOutbound, key.channelID, true)

	require.NoError(t, err)
	require.True(t, rows[0].Active)
	require.Equal(t, existing, store.memberships[key])
	require.Empty(t, store.activationCalls)
}

func TestToggleActiveDoesNotCreateMemberships(t *testing.T) {
	ctx := context.Background()
	store := &catalogStoreStub{rows: map[domain.SourceCatalog][]domain.Channel{
		domain.SourceCatalogOutbound: {{ID: "channel-1", Link: "https://t.me/room", Status: domain.ChannelPaused}},
	}}
	service := usecase.NewCatalogService(catalogStoreWithoutActivation{CatalogStore: store})

	rows, err := service.Toggle(ctx, domain.SourceCatalogOutbound, "channel-1", true)

	require.NoError(t, err)
	require.True(t, rows[0].Active)
	require.Empty(t, store.activationCalls)
	require.Empty(t, store.memberships)
}

func TestJoinCreatesMissingMembershipsWithoutChangingActive(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 31, 9, 0, 0, 0, time.UTC)
	store := &catalogStoreStub{
		rows: map[domain.SourceCatalog][]domain.Channel{
			domain.SourceCatalogOutbound: {{ID: "channel-1", Link: "https://t.me/room", Active: true, Status: domain.ChannelReady}},
		},
		accounts: []domain.Account{{ID: "spammer", Role: domain.AccountRoleSpammer, Status: domain.AccountActive}},
	}
	service := usecase.NewCatalogServiceWithJoinSchedule(store, &joinDelaySourceStub{}, func() time.Time { return now })
	service.SetJoinIntervalEnabled(false)

	rows, err := service.Join(ctx, domain.SourceCatalogOutbound, "channel-1")

	require.NoError(t, err)
	require.True(t, rows[0].Active)
	require.Equal(t, domain.ChannelJoining, rows[0].Status)
	require.Len(t, store.activationCalls, 1)
	require.Equal(t, []domain.ChannelMembership{{
		AccountID: "spammer", ChannelID: "channel-1", Status: "joining", JoinNotBefore: timePtr(now), RestDurationHours: 36,
	}}, store.activationCalls[0].memberships)
}

func TestJoinIncludesSpammerAndScoutAccounts(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 31, 9, 0, 0, 0, time.UTC)
	store := &catalogStoreStub{
		rows: map[domain.SourceCatalog][]domain.Channel{
			domain.SourceCatalogScout: {{ID: "channel-1", Link: "https://t.me/room", Status: domain.ChannelPaused}},
		},
		accounts: []domain.Account{
			{ID: "spammer", Role: domain.AccountRoleSpammer, Status: domain.AccountActive},
			{ID: "scout", Role: domain.AccountRoleScoutAnalyst, Status: domain.AccountActive},
			{ID: "paused", Role: domain.AccountRoleSpammer, Status: domain.AccountPaused},
		},
	}
	service := usecase.NewCatalogServiceWithJoinSchedule(store, &joinDelaySourceStub{}, func() time.Time { return now })
	service.SetJoinIntervalEnabled(false)

	_, err := service.Join(ctx, domain.SourceCatalogScout, "channel-1")

	require.NoError(t, err)
	require.Len(t, store.activationCalls, 1)
	require.Equal(t, []domain.ID{"spammer", "scout"}, []domain.ID{
		store.activationCalls[0].memberships[0].AccountID,
		store.activationCalls[0].memberships[1].AccountID,
	})
}

func TestLeaveDisablesActiveAndRequestsMembershipExitWithoutDeletingChannel(t *testing.T) {
	ctx := context.Background()
	store := &catalogStoreStub{rows: map[domain.SourceCatalog][]domain.Channel{
		domain.SourceCatalogOutbound: {{ID: "channel-1", Link: "https://t.me/room", Active: true, Status: domain.ChannelReady}},
	}}
	service := usecase.NewCatalogService(store)

	rows, err := service.Leave(ctx, domain.SourceCatalogOutbound, "channel-1")

	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.False(t, rows[0].Active)
	require.Equal(t, domain.ChannelPaused, rows[0].Status)
	require.Equal(t, []catalogMembershipLeaveWrite{{catalog: domain.SourceCatalogOutbound, channelID: "channel-1"}}, store.leaveCalls)
}

func TestToggleInactivePreservesMembershipAndDoesNotLeaveTelegram(t *testing.T) {
	ctx := context.Background()
	key := catalogMembershipKey{catalog: domain.SourceCatalogScout, channelID: "channel-1", accountID: "account-1"}
	wantMembership := domain.ChannelMembership{AccountID: key.accountID, ChannelID: key.channelID, IsMember: true, Status: "member"}
	store := &catalogStoreStub{
		rows: map[domain.SourceCatalog][]domain.Channel{
			domain.SourceCatalogScout: {{ID: key.channelID, Link: "https://t.me/room", Status: domain.ChannelReady, Active: true}},
		},
		memberships: map[catalogMembershipKey]domain.ChannelMembership{key: wantMembership},
	}
	service := usecase.NewCatalogService(store)

	rows, err := service.Toggle(ctx, domain.SourceCatalogScout, key.channelID, false)

	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.False(t, rows[0].Active)
	require.Equal(t, domain.ChannelReady, rows[0].Status)
	require.Equal(t, wantMembership, store.memberships[key])
	require.Empty(t, store.membershipDeletes)
	require.Empty(t, store.telegramLeaves)
}

func TestBulkScoutTopicEditIsAtomic(t *testing.T) {
	ctx := context.Background()
	store := &catalogStoreStub{rows: map[domain.SourceCatalog][]domain.Channel{
		domain.SourceCatalogScout: {
			{ID: "one", Link: "https://t.me/one", Topic: "Old"},
			{ID: "two", Link: "https://t.me/two", Topic: "Old"},
		},
	}}
	service := usecase.NewCatalogService(store)

	got, err := service.SetTopics(ctx, domain.SourceCatalogScout, []domain.ID{"one", "two"}, " New ")
	require.NoError(t, err)
	require.Equal(t, []string{"New", "New"}, channelTopics(got))

	_, err = service.SetTopics(ctx, domain.SourceCatalogScout, []domain.ID{"one", "missing"}, "Broken")
	require.ErrorIs(t, err, usecase.ErrChannelNotFound)
	got, err = store.ListCatalog(ctx, domain.SourceCatalogScout)
	require.NoError(t, err)
	require.Equal(t, []string{"New", "New"}, channelTopics(got))
}

func TestBulkScoutTopicEditRejectsBlankTopic(t *testing.T) {
	ctx := context.Background()
	store := &catalogStoreStub{rows: map[domain.SourceCatalog][]domain.Channel{
		domain.SourceCatalogScout: {{ID: "one", Topic: "Old"}},
	}}
	service := usecase.NewCatalogService(store)

	_, err := service.SetTopics(ctx, domain.SourceCatalogScout, []domain.ID{"one"}, "  ")
	require.ErrorIs(t, err, usecase.ErrTopicRequired)
	require.Equal(t, "Old", store.rows[domain.SourceCatalogScout][0].Topic)
}

func channelTopics(channels []domain.Channel) []string {
	topics := make([]string, len(channels))
	for index := range channels {
		topics[index] = channels[index].Topic
	}
	return topics
}

func timePtr(value time.Time) *time.Time {
	return &value
}
