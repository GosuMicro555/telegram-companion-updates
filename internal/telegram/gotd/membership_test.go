package gotd

import (
	"context"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"github.com/stretchr/testify/require"

	"telegram-companion/internal/domain"
)

type publicMembershipInvoker struct {
	participantErr error
	joinErr        error
	calls          int
	joins          int
	leaves         int
}

type privateMembershipInvoker struct {
	already bool
	imports int
}

type removalDiscussionInvoker struct {
	leaveChannelID int64
	inviteErr      error
}

func (i *removalDiscussionInvoker) Invoke(_ context.Context, input bin.Encoder, output bin.Decoder) error {
	switch request := input.(type) {
	case *tg.MessagesCheckChatInviteRequest:
		if i.inviteErr != nil {
			return i.inviteErr
		}
		output.(*tg.ChatInviteBox).ChatInvite = &tg.ChatInviteAlready{Chat: &tg.Channel{
			ID: 100, AccessHash: 101, Title: "Source", Broadcast: true,
		}}
	case *tg.ContactsResolveUsernameRequest:
		*output.(*tg.ContactsResolvedPeer) = tg.ContactsResolvedPeer{
			Peer:  &tg.PeerChannel{ChannelID: 100},
			Chats: []tg.ChatClass{&tg.Channel{ID: 100, AccessHash: 101, Title: "Source", Broadcast: true}},
		}
	case *tg.ChannelsGetParticipantRequest:
		*output.(*tg.ChannelsChannelParticipant) = tg.ChannelsChannelParticipant{}
	case *tg.ChannelsGetFullChannelRequest:
		full := &tg.ChannelFull{ID: 100}
		full.SetLinkedChatID(200)
		*output.(*tg.MessagesChatFull) = tg.MessagesChatFull{
			FullChat: full,
			Chats:    []tg.ChatClass{&tg.Channel{ID: 200, AccessHash: 201, Title: "Source Chat", Megagroup: true}},
		}
	case *tg.ChannelsLeaveChannelRequest:
		channel, ok := request.Channel.(*tg.InputChannel)
		if ok {
			i.leaveChannelID = channel.ChannelID
		}
	}
	return nil
}

type removalMembershipCatalog struct {
	membership       domain.ChannelMembership
	memberships      map[domain.ID]domain.ChannelMembership
	savedRows        []domain.Channel
	deletedChannelID []domain.ID
	events           []string
	saves            int
	deletes          int
	completed        int
}

func (*removalMembershipCatalog) List(context.Context, domain.SourceCatalog) ([]domain.Channel, error) {
	return nil, nil
}

func (c *removalMembershipCatalog) Save(_ context.Context, _ domain.SourceCatalog, row domain.Channel) error {
	c.savedRows = append(c.savedRows, row)
	c.events = append(c.events, "catalog")
	return nil
}

func (c *removalMembershipCatalog) LoadMembership(_ context.Context, _ domain.ID, _ domain.SourceCatalog, channelID domain.ID) (domain.ChannelMembership, bool, error) {
	if c.memberships != nil {
		membership, found := c.memberships[channelID]
		return membership, found, nil
	}
	return c.membership, true, nil
}

func (c *removalMembershipCatalog) SaveMembership(_ context.Context, _ domain.SourceCatalog, membership domain.ChannelMembership) error {
	c.membership = membership
	if c.memberships != nil {
		c.memberships[membership.ChannelID] = membership
	}
	c.events = append(c.events, "membership")
	c.saves++
	return nil
}

func (c *removalMembershipCatalog) DeleteMembership(_ context.Context, _ domain.SourceCatalog, _ domain.ID, channelID domain.ID) error {
	if c.memberships != nil {
		delete(c.memberships, channelID)
	}
	c.deletedChannelID = append(c.deletedChannelID, channelID)
	c.events = append(c.events, "delete")
	c.deletes++
	return nil
}

func (c *removalMembershipCatalog) CompleteCatalogRemoval(context.Context, domain.SourceCatalog, domain.ID) error {
	c.events = append(c.events, "complete")
	c.completed++
	return nil
}

func (i *publicMembershipInvoker) Invoke(_ context.Context, input bin.Encoder, output bin.Decoder) error {
	i.calls++
	switch input.(type) {
	case *tg.ContactsResolveUsernameRequest:
		resolved := output.(*tg.ContactsResolvedPeer)
		*resolved = tg.ContactsResolvedPeer{
			Peer:  &tg.PeerChannel{ChannelID: 77},
			Chats: []tg.ChatClass{&tg.Channel{ID: 77, AccessHash: 88, Title: "public"}},
		}
	case *tg.ChannelsGetParticipantRequest:
		if i.participantErr != nil {
			return i.participantErr
		}
		*output.(*tg.ChannelsChannelParticipant) = tg.ChannelsChannelParticipant{}
	case *tg.ChannelsJoinChannelRequest:
		i.joins++
		return i.joinErr
	case *tg.ChannelsLeaveChannelRequest:
		i.leaves++
	}
	return nil
}

func (i *privateMembershipInvoker) Invoke(_ context.Context, input bin.Encoder, output bin.Decoder) error {
	switch input.(type) {
	case *tg.MessagesCheckChatInviteRequest:
		if i.already {
			output.(*tg.ChatInviteBox).ChatInvite = &tg.ChatInviteAlready{Chat: &tg.Channel{
				ID: 77, AccessHash: 88, Title: "private",
			}}
			return nil
		}
		output.(*tg.ChatInviteBox).ChatInvite = &tg.ChatInvite{Title: "private"}
	case *tg.MessagesImportChatInviteRequest:
		i.imports++
		output.(*tg.MessagesChatInviteJoinResultBox).ChatInviteJoinResult = &tg.MessagesChatInviteJoinResultOk{
			Updates: &tg.Updates{Chats: []tg.ChatClass{
				&tg.Channel{ID: 77, AccessHash: 88, Title: "private"},
			}},
		}
	}
	return nil
}

func TestReconcileMembershipIntentSkipsFutureJoiningWithoutRPC(t *testing.T) {
	checkedAt := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	dueAt := checkedAt.Add(time.Minute)
	row := domain.Channel{ID: "public", Link: "https://t.me/public_group", Active: true}
	membership := domain.ChannelMembership{
		AccountID: "account", ChannelID: row.ID, Status: membershipJoining, JoinNotBefore: timePointer(dueAt),
	}
	invoker := &publicMembershipInvoker{}

	transition, err := reconcileMembershipIntent(
		context.Background(), tg.NewClient(invoker), row, membership, checkedAt,
	)

	require.NoError(t, err)
	require.Zero(t, invoker.calls)
	require.False(t, transition.changed)
	require.Equal(t, membership, transition.membership)
}

func TestImmediateJoinSetsJoinedAt(t *testing.T) {
	checkedAt := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	dueAt := checkedAt.Add(-time.Minute)
	row := domain.Channel{ID: "public", Link: "https://t.me/public_group", Active: true}
	membership := domain.ChannelMembership{
		AccountID: "account", ChannelID: row.ID, Status: membershipJoining,
		JoinNotBefore: timePointer(dueAt), RestDurationHours: 36,
	}
	invoker := &publicMembershipInvoker{}

	transition, err := reconcileMembershipIntent(
		context.Background(), tg.NewClient(invoker), row, membership, checkedAt,
	)

	require.NoError(t, err)
	require.Equal(t, 1, invoker.joins)
	require.True(t, transition.membership.IsMember)
	require.Equal(t, membershipMember, transition.membership.Status)
	require.Equal(t, timePointer(checkedAt), transition.membership.JoinedAt)
	require.Equal(t, timePointer(checkedAt), transition.membership.RestStartedAt)
	require.Equal(t, timePointer(checkedAt.Add(36*time.Hour)), transition.membership.RestUntil)
	require.Equal(t, 36, transition.membership.RestDurationHours)
}

func TestJoiningPublicAlreadyParticipantDoesNotStartRest(t *testing.T) {
	checkedAt := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	row := domain.Channel{ID: "public", Link: "https://t.me/public_group", Active: true}
	membership := domain.ChannelMembership{
		AccountID: "account", ChannelID: row.ID, Status: membershipJoining, RestDurationHours: 36,
	}
	invoker := &publicMembershipInvoker{joinErr: tgerr.New(400, "USER_ALREADY_PARTICIPANT")}

	transition, err := reconcileMembershipIntent(
		context.Background(), tg.NewClient(invoker), row, membership, checkedAt,
	)

	require.NoError(t, err)
	require.Equal(t, 1, invoker.joins)
	require.True(t, transition.membership.IsMember)
	require.Equal(t, membershipMember, transition.membership.Status)
	require.Nil(t, transition.membership.JoinedAt)
	require.Nil(t, transition.membership.RestStartedAt)
	require.Nil(t, transition.membership.RestUntil)
	require.Equal(t, 36, transition.membership.RestDurationHours)
}

func TestJoiningPrivateAlreadyParticipantDoesNotStartRest(t *testing.T) {
	checkedAt := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	row := domain.Channel{ID: "private", Link: "https://t.me/+invite", Active: true}
	membership := domain.ChannelMembership{
		AccountID: "account", ChannelID: row.ID, Status: membershipJoining, RestDurationHours: 36,
	}
	invoker := &privateMembershipInvoker{already: true}

	transition, err := reconcileMembershipIntent(
		context.Background(), tg.NewClient(invoker), row, membership, checkedAt,
	)

	require.NoError(t, err)
	require.Zero(t, invoker.imports)
	require.True(t, transition.membership.IsMember)
	require.Equal(t, membershipMember, transition.membership.Status)
	require.Nil(t, transition.membership.JoinedAt)
	require.Nil(t, transition.membership.RestStartedAt)
	require.Nil(t, transition.membership.RestUntil)
	require.Equal(t, 36, transition.membership.RestDurationHours)
}

func TestImmediatePrivateJoinStartsRest(t *testing.T) {
	checkedAt := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	row := domain.Channel{ID: "private", Link: "https://t.me/+invite", Active: true}
	membership := domain.ChannelMembership{
		AccountID: "account", ChannelID: row.ID, Status: membershipJoining, RestDurationHours: 36,
	}
	invoker := &privateMembershipInvoker{}

	transition, err := reconcileMembershipIntent(
		context.Background(), tg.NewClient(invoker), row, membership, checkedAt,
	)

	require.NoError(t, err)
	require.Equal(t, 1, invoker.imports)
	require.Equal(t, timePointer(checkedAt), transition.membership.JoinedAt)
	require.Equal(t, timePointer(checkedAt), transition.membership.RestStartedAt)
	require.Equal(t, timePointer(checkedAt.Add(36*time.Hour)), transition.membership.RestUntil)
}

func TestReconcileMembershipIntentPendingApprovalIgnoresJoinNotBefore(t *testing.T) {
	checkedAt := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	dueAt := checkedAt.Add(time.Hour)
	row := domain.Channel{ID: "public", Link: "https://t.me/public_group", Active: true}
	membership := domain.ChannelMembership{
		AccountID: "account", ChannelID: row.ID, Status: membershipPendingApproval, JoinNotBefore: timePointer(dueAt),
	}
	invoker := &publicMembershipInvoker{participantErr: tgerr.New(400, "USER_NOT_PARTICIPANT")}

	transition, err := reconcileMembershipIntent(
		context.Background(), tg.NewClient(invoker), row, membership, checkedAt,
	)

	require.NoError(t, err)
	require.NotZero(t, invoker.calls)
	require.Equal(t, membershipPendingApproval, transition.membership.Status)
	require.Equal(t, timePointer(dueAt), transition.membership.JoinNotBefore)
	require.Equal(t, timePointer(checkedAt), transition.membership.LastCheckAt)
}

func TestReconcileMembershipIntentRepeatPreservesJoinNotBefore(t *testing.T) {
	checkedAt := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	dueAt := checkedAt.Add(-time.Minute)
	row := domain.Channel{ID: "public", Link: "https://t.me/public_group", Active: true}
	membership := domain.ChannelMembership{
		AccountID: "account", ChannelID: row.ID, Status: membershipJoining, JoinNotBefore: timePointer(dueAt),
	}
	invoker := &publicMembershipInvoker{}

	first, err := reconcileMembershipIntent(context.Background(), tg.NewClient(invoker), row, membership, checkedAt)
	require.NoError(t, err)
	second, err := reconcileMembershipIntent(context.Background(), tg.NewClient(invoker), row, first.membership, checkedAt.Add(time.Minute))

	require.NoError(t, err)
	require.Equal(t, 1, invoker.joins)
	require.Equal(t, timePointer(dueAt), second.membership.JoinNotBefore)
	require.False(t, second.changed)
}

func TestMembershipModerationInviteRequestSetsRequestTimeOnce(t *testing.T) {
	requestedAt := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	row := domain.Channel{ID: "public", Link: "https://t.me/public_group", Active: true}
	membership := domain.ChannelMembership{AccountID: "account", ChannelID: row.ID, Status: membershipJoining}

	transition, err := reconcileMembershipIntent(
		context.Background(),
		tg.NewClient(&publicMembershipInvoker{joinErr: tgerr.New(400, tg.ErrInviteRequestSent)}),
		row,
		membership,
		requestedAt,
	)

	require.NoError(t, err)
	require.False(t, transition.membership.IsMember)
	require.Equal(t, membershipPendingApproval, transition.membership.Status)
	require.Equal(t, timePointer(requestedAt), transition.membership.RequestSubmittedAt)
	require.Nil(t, transition.membership.JoinedAt)
}

func TestMembershipModerationPendingRecheckPreservesRequestTime(t *testing.T) {
	requestedAt := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	checkedAt := time.Date(2026, 7, 17, 13, 0, 0, 0, time.UTC)
	row := domain.Channel{ID: "public", Link: "https://t.me/public_group", Active: true}
	membership := domain.ChannelMembership{
		AccountID:          "account",
		ChannelID:          row.ID,
		Status:             membershipPendingApproval,
		RequestSubmittedAt: timePointer(requestedAt),
	}

	transition, err := reconcileMembershipIntent(
		context.Background(),
		tg.NewClient(&publicMembershipInvoker{participantErr: tgerr.New(400, "USER_NOT_PARTICIPANT")}),
		row,
		membership,
		checkedAt,
	)

	require.NoError(t, err)
	require.False(t, transition.membership.IsMember)
	require.Equal(t, membershipPendingApproval, transition.membership.Status)
	require.Equal(t, timePointer(requestedAt), transition.membership.RequestSubmittedAt)
	require.Nil(t, transition.membership.JoinedAt)
	require.Equal(t, timePointer(checkedAt), transition.membership.LastCheckAt)
}

func TestPendingApprovalBecomingMemberSetsJoinedAt(t *testing.T) {
	requestedAt := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	joinedAt := time.Date(2026, 7, 17, 13, 0, 0, 0, time.UTC)
	row := domain.Channel{ID: "public", Link: "https://t.me/public_group", Active: true}
	membership := domain.ChannelMembership{
		AccountID:          "account",
		ChannelID:          row.ID,
		Status:             membershipPendingApproval,
		RequestSubmittedAt: timePointer(requestedAt),
		RestDurationHours:  72,
	}

	transition, err := reconcileMembershipIntent(
		context.Background(), tg.NewClient(&publicMembershipInvoker{}), row, membership, joinedAt,
	)

	require.NoError(t, err)
	require.True(t, transition.membership.IsMember)
	require.Equal(t, membershipMember, transition.membership.Status)
	require.Equal(t, timePointer(requestedAt), transition.membership.RequestSubmittedAt)
	require.Equal(t, timePointer(joinedAt), transition.membership.JoinedAt)
	require.Equal(t, timePointer(joinedAt), transition.membership.RestStartedAt)
	require.Equal(t, timePointer(joinedAt.Add(72*time.Hour)), transition.membership.RestUntil)
}

func TestExistingMembershipCheckDoesNotSetJoinedAt(t *testing.T) {
	checkedAt := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	row := domain.Channel{ID: "public", Link: "https://t.me/public_group", Active: true}
	membership := domain.ChannelMembership{
		AccountID: "account", ChannelID: row.ID, IsMember: true, Status: membershipMember, RestDurationHours: 36,
	}
	invoker := &publicMembershipInvoker{}

	transition, err := reconcileMembershipIntent(
		context.Background(), tg.NewClient(invoker), row, membership, checkedAt,
	)

	require.NoError(t, err)
	require.Zero(t, invoker.calls)
	require.False(t, transition.changed)
	require.True(t, transition.membership.IsMember)
	require.Equal(t, membershipMember, transition.membership.Status)
	require.Nil(t, transition.membership.RequestSubmittedAt)
	require.Nil(t, transition.membership.JoinedAt)
	require.Nil(t, transition.membership.RestStartedAt)
	require.Nil(t, transition.membership.RestUntil)
}

func TestReconcilePublicPendingApprovalStaysPendingUntilAccountIsMember(t *testing.T) {
	checkedAt := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	row := domain.Channel{ID: "public", Link: "https://t.me/public_group", Active: true}
	membership := domain.ChannelMembership{AccountID: "account", ChannelID: row.ID, Status: membershipPendingApproval}

	transition, err := reconcileMembershipIntent(
		context.Background(),
		tg.NewClient(&publicMembershipInvoker{participantErr: tgerr.New(400, "USER_NOT_PARTICIPANT")}),
		row,
		membership,
		checkedAt,
	)

	require.NoError(t, err)
	require.False(t, transition.membership.IsMember)
	require.Equal(t, membershipPendingApproval, transition.membership.Status)
	require.NotNil(t, transition.membership.LastCheckAt)
	require.Equal(t, checkedAt, *transition.membership.LastCheckAt)
}

func TestReconcilePublicPendingApprovalBecomesMemberAfterApproval(t *testing.T) {
	checkedAt := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	row := domain.Channel{ID: "public", Link: "https://t.me/public_group", Active: true}
	membership := domain.ChannelMembership{AccountID: "account", ChannelID: row.ID, Status: membershipPendingApproval}

	transition, err := reconcileMembershipIntent(
		context.Background(), tg.NewClient(&publicMembershipInvoker{}), row, membership, checkedAt,
	)

	require.NoError(t, err)
	require.True(t, transition.membership.IsMember)
	require.Equal(t, membershipMember, transition.membership.Status)
	require.Equal(t, "77", inputPeerID(transition.peer))
}

func TestReconcileRemovalMembershipLeavesChannelAndTreatsNotParticipantAsTerminal(t *testing.T) {
	invoker := &inviteInvoker{leaveErr: tgerr.New(400, "USER_NOT_PARTICIPANT")}
	row := domain.Channel{ID: "channel", TelegramID: "77", Link: "https://t.me/public_channel"}
	membership := domain.ChannelMembership{AccountID: "account", ChannelID: row.ID, IsMember: true, Status: membershipMember}

	terminal, err := reconcileRemovalMembership(context.Background(), tg.NewClient(invoker), row, membership, time.Now())

	require.NoError(t, err)
	require.True(t, terminal)
	require.Equal(t, 1, invoker.leaves)
}

func TestReconcileRemovalMembershipLeavesExplicitDiscussionOnlyWhenItMatchesStoredPeer(t *testing.T) {
	row := domain.Channel{
		ID:         "discussion",
		TelegramID: "200",
		Link:       "https://t.me/source#tc-discussion=200",
	}
	membership := domain.ChannelMembership{AccountID: "account", ChannelID: row.ID, IsMember: true, Status: membershipMember}

	t.Run("leaves discussion peer", func(t *testing.T) {
		invoker := &removalDiscussionInvoker{}

		terminal, err := reconcileRemovalMembership(context.Background(), tg.NewClient(invoker), row, membership, time.Now())

		require.NoError(t, err)
		require.True(t, terminal)
		require.Equal(t, int64(200), invoker.leaveChannelID)
	})

	t.Run("does not leave a peer that differs from the stored id", func(t *testing.T) {
		invoker := &removalDiscussionInvoker{}
		mismatched := row
		mismatched.TelegramID = "999"

		terminal, err := reconcileRemovalMembership(context.Background(), tg.NewClient(invoker), mismatched, membership, time.Now())

		require.Error(t, err)
		require.False(t, terminal)
		require.Zero(t, invoker.leaveChannelID)
	})
}

func TestInactiveChannelStillProcessesExplicitJoinIntent(t *testing.T) {
	checkedAt := time.Date(2026, 7, 31, 9, 0, 0, 0, time.UTC)
	row := domain.Channel{ID: "channel", Link: "https://t.me/public_channel", Active: false}
	catalogs := &removalMembershipCatalog{membership: domain.ChannelMembership{
		AccountID: "account", ChannelID: row.ID, Status: membershipJoining,
	}}
	invoker := &publicMembershipInvoker{}

	_, _, err := reconcileCatalogMembershipIntents(
		context.Background(), tg.NewClient(invoker), catalogs, catalogs, "account",
		domain.SourceCatalogOutbound, []domain.Channel{row}, nil, checkedAt,
	)

	require.NoError(t, err)
	require.Equal(t, 1, invoker.joins)
	require.Equal(t, membershipMember, catalogs.membership.Status)
}

func TestLeavingMemberLeavesTelegramAndDeletesOnlyMembership(t *testing.T) {
	checkedAt := time.Date(2026, 7, 31, 9, 0, 0, 0, time.UTC)
	row := domain.Channel{ID: "channel", TelegramID: "77", Link: "https://t.me/public_channel", Active: false}
	catalogs := &removalMembershipCatalog{membership: domain.ChannelMembership{
		AccountID: "account", ChannelID: row.ID, IsMember: true, Status: "leaving",
	}}
	invoker := &publicMembershipInvoker{}

	rows, _, err := reconcileCatalogMembershipIntents(
		context.Background(), tg.NewClient(invoker), catalogs, catalogs, "account",
		domain.SourceCatalogOutbound, []domain.Channel{row}, nil, checkedAt,
	)

	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, 1, invoker.leaves)
	require.Equal(t, 1, catalogs.deletes)
	require.Zero(t, catalogs.completed)
}

func TestLeavingUnsubmittedMembershipCompletesWithoutTelegramCall(t *testing.T) {
	checkedAt := time.Date(2026, 7, 31, 9, 0, 0, 0, time.UTC)
	row := domain.Channel{ID: "channel", Link: "https://t.me/public_channel", Active: false}
	catalogs := &removalMembershipCatalog{membership: domain.ChannelMembership{
		AccountID: "account", ChannelID: row.ID, Status: "leaving",
	}}

	_, _, err := reconcileCatalogMembershipIntents(
		context.Background(), nil, catalogs, catalogs, "account",
		domain.SourceCatalogOutbound, []domain.Channel{row}, nil, checkedAt,
	)

	require.NoError(t, err)
	require.Equal(t, 1, catalogs.deletes)
	require.Zero(t, catalogs.completed)
}

func TestLeavingPendingApprovalWaitsThenLeavesAfterApproval(t *testing.T) {
	checkedAt := time.Date(2026, 7, 31, 9, 0, 0, 0, time.UTC)
	requestedAt := checkedAt.Add(-time.Hour)
	row := domain.Channel{ID: "channel", TelegramID: "77", Link: "https://t.me/public_channel", Active: false}
	catalogs := &removalMembershipCatalog{membership: domain.ChannelMembership{
		AccountID: "account", ChannelID: row.ID, Status: "leaving", RequestSubmittedAt: &requestedAt,
	}}
	invoker := &publicMembershipInvoker{}

	_, _, err := reconcileCatalogMembershipIntents(
		context.Background(), tg.NewClient(invoker), catalogs, catalogs, "account",
		domain.SourceCatalogOutbound, []domain.Channel{row}, nil, checkedAt,
	)

	require.NoError(t, err)
	require.Equal(t, 1, invoker.leaves)
	require.Equal(t, 1, catalogs.deletes)
	require.Zero(t, catalogs.completed)
}

func TestReconcileCatalogRemovalRetainsPendingApprovalMembership(t *testing.T) {
	checkedAt := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	row := domain.Channel{
		ID:                 "pending",
		Link:               "https://t.me/public_group",
		RemovalRequestedAt: timePointer(checkedAt),
	}
	catalogs := &removalMembershipCatalog{membership: domain.ChannelMembership{
		AccountID: "account", ChannelID: row.ID, Status: membershipPendingApproval,
	}}

	_, _, err := reconcileCatalogMembershipIntents(
		context.Background(),
		tg.NewClient(&publicMembershipInvoker{participantErr: tgerr.New(400, "USER_NOT_PARTICIPANT")}),
		catalogs,
		catalogs,
		"account",
		domain.SourceCatalogOutbound,
		[]domain.Channel{row},
		nil,
		checkedAt,
	)

	require.NoError(t, err)
	require.Equal(t, membershipPendingApproval, catalogs.membership.Status)
	require.Equal(t, 1, catalogs.saves)
	require.Zero(t, catalogs.deletes)
	require.Zero(t, catalogs.completed)
}

func TestReconcileCatalogRemovalPersistsResolvedDiscussionPeerBeforeLeaveAfterApproval(t *testing.T) {
	checkedAt := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	row := domain.Channel{
		ID:                 "discussion",
		TelegramID:         "temporary-catalog-id",
		Link:               "https://t.me/source#tc-discussion=200",
		RemovalRequestedAt: timePointer(checkedAt),
	}
	catalogs := &removalMembershipCatalog{membership: domain.ChannelMembership{
		AccountID: "account", ChannelID: row.ID, Status: membershipPendingApproval,
	}}
	invoker := &removalDiscussionInvoker{}

	_, _, err := reconcileCatalogMembershipIntents(
		context.Background(),
		tg.NewClient(invoker),
		catalogs,
		catalogs,
		"account",
		domain.SourceCatalogOutbound,
		[]domain.Channel{row},
		nil,
		checkedAt,
	)

	require.NoError(t, err)
	require.Equal(t, int64(200), invoker.leaveChannelID)
	require.Len(t, catalogs.savedRows, 1)
	require.Equal(t, "200", catalogs.savedRows[0].TelegramID)
	require.Equal(t, "Source Chat", catalogs.savedRows[0].Title)
	require.Equal(t, 1, catalogs.deletes)
	require.Equal(t, 1, catalogs.completed)
	require.Equal(t, []string{"catalog", "membership", "delete", "complete"}, catalogs.events)
}

func TestReconcileCatalogRemovalContinuesAfterTerminalPendingInviteError(t *testing.T) {
	for _, code := range []string{"INVITE_HASH_INVALID", "INVITE_HASH_EXPIRED"} {
		t.Run(code, func(t *testing.T) {
			checkedAt := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
			expired := domain.Channel{
				ID:                 "expired",
				TelegramID:         "temporary-expired-id",
				Link:               "https://t.me/+expired",
				RemovalRequestedAt: timePointer(checkedAt),
			}
			next := domain.Channel{
				ID:                 "next",
				TelegramID:         "100",
				Link:               "https://t.me/source",
				RemovalRequestedAt: timePointer(checkedAt),
			}
			catalogs := &removalMembershipCatalog{memberships: map[domain.ID]domain.ChannelMembership{
				expired.ID: {
					AccountID: "account", ChannelID: expired.ID, Status: membershipPendingApproval,
				},
				next.ID: {
					AccountID: "account", ChannelID: next.ID, IsMember: true, Status: membershipMember,
				},
			}}
			invoker := &removalDiscussionInvoker{inviteErr: tgerr.New(400, code)}

			_, _, err := reconcileCatalogMembershipIntents(
				context.Background(),
				tg.NewClient(invoker),
				catalogs,
				catalogs,
				"account",
				domain.SourceCatalogOutbound,
				[]domain.Channel{expired, next},
				nil,
				checkedAt,
			)

			require.NoError(t, err)
			retained, found := catalogs.memberships[expired.ID]
			require.True(t, found)
			require.Equal(t, membershipPendingApproval, retained.Status)
			require.Contains(t, retained.LastError, code)
			require.Equal(t, timePointer(checkedAt), retained.LastCheckAt)
			require.NotContains(t, catalogs.deletedChannelID, expired.ID)
			require.Contains(t, catalogs.deletedChannelID, next.ID)
			require.Equal(t, int64(100), invoker.leaveChannelID)
			require.Equal(t, 1, catalogs.completed)
		})
	}
}

func TestReconcileRemovalMembershipDropsNonMemberWithoutRPC(t *testing.T) {
	row := domain.Channel{ID: "channel", Link: "https://t.me/public_channel"}
	membership := domain.ChannelMembership{AccountID: "account", ChannelID: row.ID, Status: membershipJoining}

	terminal, err := reconcileRemovalMembership(context.Background(), nil, row, membership, time.Now())

	require.NoError(t, err)
	require.True(t, terminal)
}
