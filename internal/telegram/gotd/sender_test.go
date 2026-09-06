package gotd

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"github.com/stretchr/testify/require"

	"telegram-companion/internal/domain"
	"telegram-companion/internal/repository/sqlite"
	"telegram-companion/internal/usecase"
	"telegram-companion/internal/usecase/runtimeconfig"
)

type inviteInvoker struct {
	imports  int
	joins    int
	calls    int
	joinErr  error
	leaveErr error
	leaves   int
	sends    []*tg.MessagesSendMessageRequest
}

type explicitDiscussionSendInvoker struct {
	sends []*tg.MessagesSendMessageRequest
}

func (i *explicitDiscussionSendInvoker) Invoke(_ context.Context, input bin.Encoder, output bin.Decoder) error {
	switch input.(type) {
	case *tg.MessagesCheckChatInviteRequest:
		output.(*tg.ChatInviteBox).ChatInvite = &tg.ChatInviteAlready{Chat: &tg.Channel{
			ID: 100, AccessHash: 101, Title: "Source", Broadcast: true,
		}}
	case *tg.ChannelsGetFullChannelRequest:
		full := &tg.ChannelFull{ID: 100}
		full.SetLinkedChatID(200)
		*output.(*tg.MessagesChatFull) = tg.MessagesChatFull{
			FullChat: full,
			Chats: []tg.ChatClass{
				&tg.Channel{ID: 200, AccessHash: 201, Title: "Source Chat", Megagroup: true},
			},
		}
	case *tg.ChannelsJoinChannelRequest:
		output.(*tg.MessagesChatInviteJoinResultBox).ChatInviteJoinResult = &tg.MessagesChatInviteJoinResultOk{Updates: &tg.Updates{}}
	case *tg.MessagesSendMessageRequest:
		i.sends = append(i.sends, input.(*tg.MessagesSendMessageRequest))
		output.(*tg.UpdatesBox).Updates = &tg.Updates{}
	}
	return nil
}

func TestSendPublicReplyUsesExplicitDiscussionTarget(t *testing.T) {
	invoker := &explicitDiscussionSendInvoker{}
	sender := &gotdAccountSender{
		api: tg.NewClient(invoker),
		catalogs: &inviteCatalog{row: domain.Channel{
			ID: "discussion", Link: "https://t.me/+invite#tc-discussion=200", Active: true,
		}},
	}

	err := sender.SendPublicReply(context.Background(), domain.Account{}, domain.OutgoingMessageJob{
		ID: "job", ChannelID: "discussion", ReplyToMessageID: "63", Text: "reply",
	})

	require.NoError(t, err)
	require.Len(t, invoker.sends, 1)
	peer, ok := invoker.sends[0].Peer.(*tg.InputPeerChannel)
	require.True(t, ok)
	require.Equal(t, int64(200), peer.ChannelID)
	require.Equal(t, 63, invoker.sends[0].ReplyTo.(*tg.InputReplyToMessage).ReplyToMsgID)
}

func (i *inviteInvoker) Invoke(_ context.Context, input bin.Encoder, output bin.Decoder) error {
	i.calls++
	switch input.(type) {
	case *tg.MessagesCheckChatInviteRequest:
		output.(*tg.ChatInviteBox).ChatInvite = &tg.ChatInvite{Title: "private"}
	case *tg.MessagesImportChatInviteRequest:
		i.imports++
		output.(*tg.MessagesChatInviteJoinResultBox).ChatInviteJoinResult = &tg.MessagesChatInviteJoinResultOk{
			Updates: &tg.Updates{Chats: []tg.ChatClass{&tg.Channel{ID: 77, AccessHash: 88, Title: "private"}}},
		}
	case *tg.ContactsResolveUsernameRequest:
		resolved := output.(*tg.ContactsResolvedPeer)
		*resolved = tg.ContactsResolvedPeer{
			Peer:  &tg.PeerChannel{ChannelID: 77},
			Chats: []tg.ChatClass{&tg.Channel{ID: 77, AccessHash: 88, Title: "public"}},
		}
	case *tg.ChannelsJoinChannelRequest:
		i.joins++
		if i.joinErr != nil {
			return i.joinErr
		}
		output.(*tg.MessagesChatInviteJoinResultBox).ChatInviteJoinResult = &tg.MessagesChatInviteJoinResultOk{Updates: &tg.Updates{}}
	case *tg.ChannelsLeaveChannelRequest:
		i.leaves++
		if i.leaveErr != nil {
			return i.leaveErr
		}
		output.(*tg.UpdatesBox).Updates = &tg.Updates{}
	case *tg.MessagesSendMessageRequest:
		request := input.(*tg.MessagesSendMessageRequest)
		i.sends = append(i.sends, request)
		output.(*tg.UpdatesBox).Updates = &tg.Updates{}
	}
	return nil
}

func TestRegistryJoinsPublicChannelForPersistedAccountIntent(t *testing.T) {
	invoker := &inviteInvoker{}
	catalogs := &inviteCatalog{row: domain.Channel{
		ID: "public", TelegramID: "pending", Link: "https://t.me/public_channel", Status: domain.ChannelJoining, Active: true,
	}, memberships: map[domain.ID]domain.ChannelMembership{
		"account-a": {AccountID: "account-a", ChannelID: "public", Status: "joining"},
	}}
	registry := NewClientSenderRegistry(catalogs)

	require.NoError(t, registry.Register(context.Background(), domain.Account{ID: "account-a", Role: domain.AccountRoleSpammer}, inviteClient{api: tg.NewClient(invoker)}))
	require.Equal(t, 1, invoker.joins)
	require.Equal(t, "member", catalogs.memberships["account-a"].Status)
	require.True(t, catalogs.memberships["account-a"].IsMember)
}

func TestRegistryDoesNotJoinPublicChannelWithoutPersistedAccountIntent(t *testing.T) {
	invoker := &inviteInvoker{}
	catalogs := &inviteCatalog{row: domain.Channel{
		ID: "public", TelegramID: "pending", Link: "https://t.me/public_channel", Status: domain.ChannelJoining, Active: true,
	}, memberships: map[domain.ID]domain.ChannelMembership{}}
	registry := NewClientSenderRegistry(catalogs)

	require.NoError(t, registry.Register(context.Background(), domain.Account{ID: "account-a", Role: domain.AccountRoleSpammer}, inviteClient{api: tg.NewClient(invoker)}))
	require.Zero(t, invoker.calls)
	require.Empty(t, catalogs.memberships)
}

func TestRegistryPersistsExplicitDiscussionTargetForOutboundChannel(t *testing.T) {
	invoker := &linkedDiscussionInvoker{}
	catalogs := &inviteCatalog{row: domain.Channel{
		ID: "discussion", TelegramID: "pending", Link: "https://t.me/source#tc-discussion=200", Status: domain.ChannelJoining, Active: true,
	}, memberships: map[domain.ID]domain.ChannelMembership{
		"account-a": {AccountID: "account-a", ChannelID: "discussion", Status: "joining"},
	}}
	registry := NewClientSenderRegistry(catalogs)

	require.NoError(t, registry.Register(context.Background(), domain.Account{ID: "account-a", Role: domain.AccountRoleSpammer}, inviteClient{api: tg.NewClient(invoker)}))
	require.Equal(t, "200", catalogs.row.TelegramID)
	require.Equal(t, "Source Chat", catalogs.row.Title)
	require.Equal(t, "member", catalogs.memberships["account-a"].Status)
	require.Equal(t, 2, invoker.discussionJoins)
}

func TestProductionSendersClassifyDisconnectedAndInvalidFailures(t *testing.T) {
	registry := NewClientSenderRegistry(&inviteCatalog{})
	err := registry.SendPublicReply(context.Background(), domain.Account{ID: "offline"}, domain.OutgoingMessageJob{})
	require.True(t, domain.IsTransientDeliveryFailure(err))

	sender := &gotdAccountSender{catalogs: &inviteCatalog{row: domain.Channel{ID: "known"}}}
	err = sender.SendPublicReply(context.Background(), domain.Account{}, domain.OutgoingMessageJob{ChannelID: "missing"})
	require.True(t, domain.IsPermanentDeliveryFailure(err))
	err = sender.SendPrivateMessage(context.Background(), domain.Account{}, domain.OutgoingMessageJob{})
	require.True(t, domain.IsPermanentDeliveryFailure(err))
}

type privateMessageInvoker struct {
	err   error
	sends int
}

func (i *privateMessageInvoker) Invoke(_ context.Context, input bin.Encoder, output bin.Decoder) error {
	if _, ok := input.(*tg.MessagesSendMessageRequest); !ok {
		return nil
	}
	i.sends++
	if i.err != nil {
		return i.err
	}
	output.(*tg.UpdatesBox).Updates = &tg.Updates{}
	return nil
}

func TestSendPrivateMessageClassifiesClosedDMRPCErrorsExactly(t *testing.T) {
	key := make([]byte, 32)
	references := NewSenderReferences(key)
	target := references.Store(TelegramPeerDescriptor{UserID: 777, AccessHash: 9988})

	tests := []struct {
		name          string
		rpcErr        error
		missingRef    bool
		wantClosed    bool
		wantTransient bool
		wantPermanent bool
		wantFloodWait bool
		wantSend      int
	}{
		{name: "privacy restricted", rpcErr: tgerr.New(400, tg.ErrUserPrivacyRestricted), wantClosed: true, wantSend: 1},
		{name: "user is blocked", rpcErr: tgerr.New(400, tg.ErrUserIsBlocked), wantClosed: true, wantSend: 1},
		{name: "you blocked user", rpcErr: tgerr.New(400, tg.ErrYouBlockedUser), wantClosed: true, wantSend: 1},
		{name: "peer id invalid", rpcErr: tgerr.New(400, "PEER_ID_INVALID"), wantClosed: true, wantSend: 1},
		{name: "flood wait", rpcErr: tgerr.New(420, "FLOOD_WAIT_30"), wantTransient: true, wantFloodWait: true, wantSend: 1},
		{name: "network error", rpcErr: errors.New("connection reset by peer"), wantTransient: true, wantSend: 1},
		{name: "missing sender reference", missingRef: true, wantPermanent: true},
		{name: "success", wantSend: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			invoker := &privateMessageInvoker{err: tt.rpcErr}
			sender := &gotdAccountSender{api: tg.NewClient(invoker), references: references}
			job := domain.OutgoingMessageJob{TargetTelegramID: target, Text: "hello"}
			if tt.missingRef {
				sender.references = nil
			}

			err := sender.SendPrivateMessage(context.Background(), domain.Account{}, job)

			require.Equal(t, tt.wantClosed, domain.IsPrivateMessageClosed(err))
			require.Equal(t, tt.wantTransient, domain.IsTransientDeliveryFailure(err))
			require.Equal(t, tt.wantPermanent, domain.IsPermanentDeliveryFailure(err))
			require.Equal(t, tt.wantSend, invoker.sends)
			var flood *FloodWaitError
			require.Equal(t, tt.wantFloodWait, errors.As(err, &flood))
			if tt.wantFloodWait {
				require.Equal(t, 30*time.Second, flood.Duration)
			}
		})
	}
}

func TestSendPrivateMessageUnresolvableSenderReferenceIsPermanent(t *testing.T) {
	invoker := &privateMessageInvoker{}
	sender := &gotdAccountSender{
		api:        tg.NewClient(invoker),
		references: NewSenderReferences(make([]byte, 32)),
	}

	err := sender.SendPrivateMessage(context.Background(), domain.Account{}, domain.OutgoingMessageJob{
		TargetTelegramID: "not-a-valid-sender-reference",
		Text:             "hello",
	})

	require.True(t, domain.IsPermanentDeliveryFailure(err))
	require.False(t, domain.IsPrivateMessageClosed(err))
	require.False(t, domain.IsTransientDeliveryFailure(err))
	require.Zero(t, invoker.sends)
}

type scheduledDMInvoker struct {
	resolveErr error
	sendErr    error
	requests   []*tg.MessagesSendMessageRequest
}

func (i *scheduledDMInvoker) Invoke(_ context.Context, input bin.Encoder, output bin.Decoder) error {
	switch input.(type) {
	case *tg.ContactsResolveUsernameRequest:
		if i.resolveErr != nil {
			return i.resolveErr
		}
		*output.(*tg.ContactsResolvedPeer) = tg.ContactsResolvedPeer{Users: []tg.UserClass{
			&tg.User{ID: 104, AccessHash: 105, FirstName: "Consenting"},
		}}
	case *tg.MessagesSendMessageRequest:
		if i.sendErr != nil {
			return i.sendErr
		}
		i.requests = append(i.requests, input.(*tg.MessagesSendMessageRequest))
		output.(*tg.UpdatesBox).Updates = &tg.Updates{}
	}
	return nil
}

func TestScheduledPrivateMessageResolvesAndSendsWithDurableRandomID(t *testing.T) {
	invoker := &scheduledDMInvoker{}
	sender := &gotdAccountSender{api: tg.NewClient(invoker)}
	delivery := domain.ScheduledDMDelivery{ID: "delivery-1", Recipient: "consenting_contact", TelegramRandomID: 991}

	require.NoError(t, sender.ResolveUsername(context.Background(), domain.Account{ID: "spam"}, delivery.Recipient))
	require.NoError(t, sender.SendScheduledPrivateMessage(context.Background(), domain.Account{ID: "spam"}, delivery, "task-specific text"))

	require.Len(t, invoker.requests, 1)
	require.Equal(t, "task-specific text", invoker.requests[0].Message)
	require.Equal(t, int64(991), invoker.requests[0].RandomID)
	peer, ok := invoker.requests[0].Peer.(*tg.InputPeerUser)
	require.True(t, ok)
	require.Equal(t, int64(104), peer.UserID)
}

func TestScheduledPrivateMessageUsesNormalizedRecipientForAtUsername(t *testing.T) {
	invoker := &scheduledDMInvoker{}
	sender := &gotdAccountSender{api: tg.NewClient(invoker)}
	delivery := domain.ScheduledDMDelivery{ID: "delivery-1", Recipient: "@Consenting_Contact", TelegramRandomID: 991}

	require.NoError(t, sender.ResolveUsername(context.Background(), domain.Account{ID: "spam"}, delivery.Recipient))
	require.NoError(t, sender.SendScheduledPrivateMessage(context.Background(), domain.Account{ID: "spam"}, delivery, "task-specific text"))
	require.Len(t, invoker.requests, 1)
}

func TestScheduledPrivateMessageClassifiesInvalidUsernameAsPermanent(t *testing.T) {
	invoker := &scheduledDMInvoker{resolveErr: tgerr.New(400, "USERNAME_NOT_OCCUPIED")}
	sender := &gotdAccountSender{api: tg.NewClient(invoker)}

	err := sender.ResolveUsername(context.Background(), domain.Account{ID: "spam"}, "missing_user")

	require.True(t, domain.IsPermanentDeliveryFailure(err))
	require.False(t, domain.IsTransientDeliveryFailure(err))
}

func TestResolveTelegramPeerNeverImportsPrivateInviteDuringActivation(t *testing.T) {
	invoker := &inviteInvoker{}

	_, _, err := resolveTelegramPeer(context.Background(), tg.NewClient(invoker), "https://t.me/+privateInvite", false)

	require.ErrorContains(t, err, "explicit")
	require.Zero(t, invoker.imports)
}

type inviteCatalog struct {
	row         domain.Channel
	saved       []domain.Channel
	memberships map[domain.ID]domain.ChannelMembership
	completed   int
}

func (c *inviteCatalog) List(context.Context, domain.SourceCatalog) ([]domain.Channel, error) {
	return []domain.Channel{c.row}, nil
}

func (c *inviteCatalog) Save(_ context.Context, _ domain.SourceCatalog, row domain.Channel) error {
	c.saved = append(c.saved, row)
	c.row = row
	return nil
}

func (c *inviteCatalog) LoadMembership(_ context.Context, accountID domain.ID, _ domain.SourceCatalog, channelID domain.ID) (domain.ChannelMembership, bool, error) {
	membership, ok := c.memberships[accountID]
	return membership, ok && membership.ChannelID == channelID, nil
}

func (c *inviteCatalog) SaveMembership(_ context.Context, _ domain.SourceCatalog, membership domain.ChannelMembership) error {
	c.memberships[membership.AccountID] = membership
	return nil
}
func (c *inviteCatalog) DeleteMembership(_ context.Context, _ domain.SourceCatalog, accountID, _ domain.ID) error {
	delete(c.memberships, accountID)
	return nil
}
func (c *inviteCatalog) CompleteCatalogRemoval(context.Context, domain.SourceCatalog, domain.ID) error {
	c.completed++
	return nil
}

type inviteClient struct{ api *tg.Client }

func (c inviteClient) Run(ctx context.Context, callback func(context.Context) error) error {
	return callback(ctx)
}
func (c inviteClient) API() *tg.Client                           { return c.api }
func (inviteClient) Validate(context.Context) (time.Time, error) { return time.Time{}, nil }

func TestRegistryImportsPrivateInviteForEachPersistedAccountIntent(t *testing.T) {
	invoker := &inviteInvoker{}
	catalogs := &inviteCatalog{row: domain.Channel{
		ID: "private", Link: "https://t.me/+privateInvite", Status: domain.ChannelJoining, Active: true,
	}, memberships: map[domain.ID]domain.ChannelMembership{
		"account-a": {AccountID: "account-a", ChannelID: "private", Status: "joining"},
		"account-b": {AccountID: "account-b", ChannelID: "private", Status: "joining"},
	}}
	registry := NewClientSenderRegistry(catalogs)

	require.NoError(t, registry.Register(context.Background(), domain.Account{ID: "account-a", Role: domain.AccountRoleSpammer}, inviteClient{api: tg.NewClient(invoker)}))
	require.NoError(t, registry.Register(context.Background(), domain.Account{ID: "account-b", Role: domain.AccountRoleSpammer}, inviteClient{api: tg.NewClient(invoker)}))
	require.Equal(t, 2, invoker.imports)
	require.Len(t, catalogs.saved, 2)
	require.Equal(t, domain.ChannelReady, catalogs.saved[0].Status)
	require.Equal(t, "member", catalogs.memberships["account-a"].Status)
	require.Equal(t, "member", catalogs.memberships["account-b"].Status)
}

type commitFailJobRepository struct {
	*sqlite.JobRepository
	completeCalls int
}

func (r *commitFailJobRepository) Complete(ctx context.Context, jobID domain.ID, event domain.OutgoingMessageEvent) (bool, error) {
	r.completeCalls++
	if r.completeCalls == 1 {
		return false, errors.New("local commit failed")
	}
	return r.JobRepository.Complete(ctx, jobID, event)
}

func (r *commitFailJobRepository) CompleteLease(ctx context.Context, jobID domain.ID, token string, event domain.OutgoingMessageEvent) (bool, error) {
	r.completeCalls++
	if r.completeCalls == 1 {
		return false, errors.New("local commit failed")
	}
	return r.JobRepository.CompleteLease(ctx, jobID, token, event)
}

type accountRecordingSender struct {
	delegate usecase.MessageSender
	accounts []domain.ID
}

func (s *accountRecordingSender) SendPublicReply(ctx context.Context, account domain.Account, job domain.OutgoingMessageJob) error {
	s.accounts = append(s.accounts, account.ID)
	return s.delegate.SendPublicReply(ctx, account, job)
}

func (s *accountRecordingSender) SendPrivateMessage(ctx context.Context, account domain.Account, job domain.OutgoingMessageJob) error {
	s.accounts = append(s.accounts, account.ID)
	return s.delegate.SendPrivateMessage(ctx, account, job)
}

func TestTelegramSuccessThenLocalCommitRetryKeepsAccountRandomIDAndAccountingIdempotent(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	require.NoError(t, err)
	defer db.Close()

	now := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	store := sqlite.NewProductionStore(db)
	require.NoError(t, store.EnsureAccounts(ctx, []domain.Account{
		{ID: "account-a", Role: domain.AccountRoleSpammer, Status: domain.AccountActive, SessionPath: "/session/a", CreatedAt: now, UpdatedAt: now},
		{ID: "account-b", Role: domain.AccountRoleSpammer, Status: domain.AccountActive, SessionPath: "/session/b", CreatedAt: now, UpdatedAt: now},
	}))

	key := make([]byte, 32)
	references := NewSenderReferences(key)
	target := references.Store(TelegramPeerDescriptor{UserID: 777, AccessHash: 9988})
	invoker := &inviteInvoker{}
	telegram := &gotdAccountSender{api: tg.NewClient(invoker), references: references}
	outbound := NewOutbound(
		&outboundConfigFake{snapshot: runtimeconfig.Snapshot{SharedReply: "reply", RateLimits: runtimeconfig.RateLimits{RepliesPerMinute: 19, MinIntervalSeconds: 2}}},
		store.Accounts(), telegram, func() time.Time { return now },
	)
	baseJobs := sqlite.NewJobRepository(db, func() time.Time { return now })
	require.NoError(t, baseJobs.Enqueue(ctx, domain.OutgoingMessageJob{
		ID: "durable-job", Type: domain.JobPrivateMessage, TargetTelegramID: target,
		Status: "queued", NextAttemptAt: now, CreatedAt: now,
	}))
	jobs := &commitFailJobRepository{JobRepository: baseJobs}
	recorder := &accountRecordingSender{delegate: outbound}
	scheduler := usecase.NewScheduler(store.Accounts(), jobs, store, recorder, usecase.NewRoundRobinSelector(), nil)

	require.ErrorContains(t, scheduler.RunOnce(ctx, now), "local commit failed")
	now = now.Add(6 * time.Minute)
	require.NoError(t, scheduler.RunOnce(ctx, now))

	require.Equal(t, []domain.ID{"account-a", "account-a"}, recorder.accounts)
	require.Len(t, invoker.sends, 2)
	require.NotZero(t, invoker.sends[0].RandomID)
	require.Equal(t, invoker.sends[0].RandomID, invoker.sends[1].RandomID)

	accounts, err := store.Accounts().List(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), accounts[0].PrivateMessagesSent)
	require.Zero(t, accounts[1].PrivateMessagesSent)
	var status, accountID string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT status, account_id FROM outgoing_message_jobs WHERE id='durable-job'`).Scan(&status, &accountID))
	require.Equal(t, "done", status)
	require.Equal(t, "account-a", accountID)
	var events int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM outgoing_message_events WHERE job_id='durable-job'`).Scan(&events))
	require.Equal(t, 1, events)
}

func TestDiscussionTargetLinkHelpers(t *testing.T) {
	link := "https://t.me/+mgppYi2XAIMxYjFi#tc-discussion=4291488698"

	targetID, ok := explicitDiscussionTargetID(link)
	require.True(t, ok)
	require.Equal(t, "4291488698", targetID)
	require.Equal(t, "https://t.me/+mgppYi2XAIMxYjFi", telegramJoinLink(link))
	_, ok = explicitDiscussionTargetID("https://t.me/+invite#other=1")
	require.False(t, ok)
}
