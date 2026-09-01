package gotd

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"github.com/stretchr/testify/require"

	"telegram-companion/internal/domain"
	"telegram-companion/internal/usecase/runtimeconfig"
	"telegram-companion/internal/usecase/scouting"
)

type analyticsCursorStoreFake struct {
	mu      sync.Mutex
	cursors map[string]domain.AnalyticsScoutCursor
}

func (f *analyticsCursorStoreFake) AnalyticsSettings(context.Context) (domain.AnalyticsSettings, error) {
	return domain.DefaultAnalyticsSettings(), nil
}
func (f *analyticsCursorStoreFake) SaveAnalyticsSettings(context.Context, domain.AnalyticsSettings) error {
	return nil
}
func (f *analyticsCursorStoreFake) ScoutCursor(_ context.Context, accountID domain.ID, chatID string) (domain.AnalyticsScoutCursor, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cursors[string(accountID)+":"+chatID], nil
}
func (f *analyticsCursorStoreFake) SaveScoutCursor(_ context.Context, cursor domain.AnalyticsScoutCursor) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cursors == nil {
		f.cursors = make(map[string]domain.AnalyticsScoutCursor)
	}
	f.cursors[string(cursor.AccountID)+":"+cursor.ChatID] = cursor
	return nil
}
func (f *analyticsCursorStoreFake) AppendAnalyticsRun(context.Context, domain.AnalyticsSchedulerRun) error {
	return nil
}
func (f *analyticsCursorStoreFake) AnalyticsRunHistory(context.Context, int) ([]domain.AnalyticsSchedulerRun, error) {
	return nil, nil
}
func (f *analyticsCursorStoreFake) AnalyticsMetrics(context.Context) (domain.AnalyticsMetrics, error) {
	return domain.AnalyticsMetrics{}, nil
}
func (f *analyticsCursorStoreFake) ServiceWords(context.Context, domain.AnalyticsLanguage) ([]string, error) {
	return nil, nil
}
func (f *analyticsCursorStoreFake) UpsertServiceWord(context.Context, domain.AnalyticsServiceWord) error {
	return nil
}
func (f *analyticsCursorStoreFake) DeleteServiceWord(context.Context, domain.AnalyticsLanguage, string) error {
	return nil
}
func (f *analyticsCursorStoreFake) TablePreference(context.Context, string) (domain.AnalyticsTablePreference, error) {
	return domain.AnalyticsTablePreference{}, nil
}
func (f *analyticsCursorStoreFake) SaveTablePreference(context.Context, domain.AnalyticsTablePreference) error {
	return nil
}

type scoutHistoryPagerFake struct {
	requests []*tg.MessagesGetHistoryRequest
	pages    []tg.MessagesMessagesClass
	err      error
}

func (f *scoutHistoryPagerFake) MessagesGetHistory(_ context.Context, request *tg.MessagesGetHistoryRequest) (tg.MessagesMessagesClass, error) {
	f.requests = append(f.requests, request)
	if f.err != nil {
		return nil, f.err
	}
	if len(f.pages) == 0 {
		return &tg.MessagesMessages{}, nil
	}
	page := f.pages[0]
	f.pages = f.pages[1:]
	return page, nil
}

func TestAnalyticsHistoryCycleKeepsChatFailuresNonFatalAndCountsUniqueGroups(t *testing.T) {
	cycle := newAnalyticsHistoryCycle()
	cycle.scanChat("channel:100")
	cycle.fail(errors.New("account scout-a chat channel:100: collect failed"))
	cycle.scanChat("channel:100")
	cycle.scanChat("chat:100")

	result, err := cycle.finish(nil)

	require.NoError(t, err)
	require.Equal(t, int64(2), result.ChatsScanned)
	require.Equal(t, []string{"account scout-a chat channel:100: collect failed"}, result.Errors)
}

func TestAnalyticsHistoryCycleReservesFatalErrorForGlobalFailure(t *testing.T) {
	want := context.Canceled
	cycle := newAnalyticsHistoryCycle()
	cycle.scanChat("100")

	result, err := cycle.finish(want)

	require.ErrorIs(t, err, want)
	require.Equal(t, int64(1), result.ChatsScanned)
}

type failingHistoryCollector struct {
	updates []scouting.IncomingUpdate
	failID  int64
}

func (f *failingHistoryCollector) CollectHistory(_ context.Context, update scouting.IncomingUpdate) error {
	f.updates = append(f.updates, update)
	if update.MessageID == f.failID {
		return errors.New("durable collect failed")
	}
	return nil
}

func TestScoutHistoryFirstScanCollectsOnlyLastSevenDaysAndPersistsCursor(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	pager := &scoutHistoryPagerFake{pages: []tg.MessagesMessagesClass{&tg.MessagesMessages{Messages: []tg.MessageClass{
		&tg.Message{ID: 20, Message: "recent", Date: int(now.Add(-6 * 24 * time.Hour).Unix())},
		&tg.Message{ID: 19, Message: "too old", Date: int(now.Add(-8 * 24 * time.Hour).Unix())},
	}}}}
	collector := &failingHistoryCollector{}
	store := &analyticsCursorStoreFake{cursors: make(map[string]domain.AnalyticsScoutCursor)}

	count, err := syncScoutHistoryPeer(context.Background(), pager, collector, store, "scout-a", &tg.InputPeerChannel{ChannelID: 100}, now)

	require.NoError(t, err)
	require.Equal(t, int64(1), count)
	require.Len(t, collector.updates, 1)
	require.Equal(t, int64(20), collector.updates[0].MessageID)
	require.Len(t, pager.requests, 1)
	require.Zero(t, pager.requests[0].MinID)
	cursor, err := store.ScoutCursor(context.Background(), "scout-a", "channel:100")
	require.NoError(t, err)
	require.Equal(t, int64(20), cursor.MessageID)
}

func TestScoutHistoryScopesScansCursorsAndMessagesByPeerKind(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	store := &analyticsCursorStoreFake{cursors: make(map[string]domain.AnalyticsScoutCursor)}
	collector := &failingHistoryCollector{}
	channelPager := &scoutHistoryPagerFake{pages: []tg.MessagesMessagesClass{&tg.MessagesMessages{Messages: []tg.MessageClass{
		&tg.Message{ID: 20, Message: "channel message", Date: int(now.Unix())},
	}}}}
	chatPager := &scoutHistoryPagerFake{pages: []tg.MessagesMessagesClass{&tg.MessagesMessages{Messages: []tg.MessageClass{
		&tg.Message{ID: 7, Message: "chat message", Date: int(now.Unix())},
	}}}}
	userPager := &scoutHistoryPagerFake{pages: []tg.MessagesMessagesClass{&tg.MessagesMessages{Messages: []tg.MessageClass{
		&tg.Message{ID: 3, Message: "user message", Date: int(now.Unix())},
	}}}}

	channelCount, err := syncScoutHistoryPeer(context.Background(), channelPager, collector, store, "scout-a", &tg.InputPeerChannel{ChannelID: 100}, now)
	require.NoError(t, err)
	chatCount, err := syncScoutHistoryPeer(context.Background(), chatPager, collector, store, "scout-a", &tg.InputPeerChat{ChatID: 100}, now)
	require.NoError(t, err)
	userCount, err := syncScoutHistoryPeer(context.Background(), userPager, collector, store, "scout-a", &tg.InputPeerUser{UserID: 100}, now)
	require.NoError(t, err)

	require.Equal(t, int64(1), channelCount)
	require.Equal(t, int64(1), chatCount)
	require.Equal(t, int64(1), userCount)
	require.Zero(t, channelPager.requests[0].MinID)
	require.Zero(t, chatPager.requests[0].MinID)
	require.Zero(t, userPager.requests[0].MinID)
	require.Equal(t, []string{"channel:100", "chat:100", "user:100"}, []string{collector.updates[0].ChatID, collector.updates[1].ChatID, collector.updates[2].ChatID})
	channelCursor, err := store.ScoutCursor(context.Background(), "scout-a", "channel:100")
	require.NoError(t, err)
	require.Equal(t, int64(20), channelCursor.MessageID)
	chatCursor, err := store.ScoutCursor(context.Background(), "scout-a", "chat:100")
	require.NoError(t, err)
	require.Equal(t, int64(7), chatCursor.MessageID)
	userCursor, err := store.ScoutCursor(context.Background(), "scout-a", "user:100")
	require.NoError(t, err)
	require.Equal(t, int64(3), userCursor.MessageID)
}

func TestScoutHistoryResumesStrictlyAfterPersistedAccountChatCursor(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	store := &analyticsCursorStoreFake{cursors: map[string]domain.AnalyticsScoutCursor{
		"scout-a:channel:100": {AccountID: "scout-a", ChatID: "channel:100", MessageID: 20},
	}}
	pager := &scoutHistoryPagerFake{pages: []tg.MessagesMessagesClass{&tg.MessagesMessages{Messages: []tg.MessageClass{
		&tg.Message{ID: 22, Message: "newest", Date: int(now.Unix())},
		&tg.Message{ID: 21, Message: "new", Date: int(now.Add(-time.Hour).Unix())},
	}}}}
	collector := &failingHistoryCollector{}

	count, err := syncScoutHistoryPeer(context.Background(), pager, collector, store, "scout-a", &tg.InputPeerChannel{ChannelID: 100}, now)

	require.NoError(t, err)
	require.Equal(t, int64(2), count)
	require.Equal(t, 20, pager.requests[0].MinID)
	require.Equal(t, []int64{22, 21}, []int64{collector.updates[0].MessageID, collector.updates[1].MessageID})
	cursor, err := store.ScoutCursor(context.Background(), "scout-a", "channel:100")
	require.NoError(t, err)
	require.Equal(t, int64(22), cursor.MessageID)
}

func TestScoutHistoryDoesNotAdvanceCursorUntilWholeChatBatchIsDurable(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	store := &analyticsCursorStoreFake{cursors: map[string]domain.AnalyticsScoutCursor{
		"scout-a:channel:100": {AccountID: "scout-a", ChatID: "channel:100", MessageID: 20},
	}}
	pager := &scoutHistoryPagerFake{pages: []tg.MessagesMessagesClass{&tg.MessagesMessages{Messages: []tg.MessageClass{
		&tg.Message{ID: 22, Message: "may duplicate safely", Date: int(now.Unix())},
		&tg.Message{ID: 21, Message: "fails", Date: int(now.Add(-time.Hour).Unix())},
	}}}}
	collector := &failingHistoryCollector{failID: 21}

	_, err := syncScoutHistoryPeer(context.Background(), pager, collector, store, "scout-a", &tg.InputPeerChannel{ChannelID: 100}, now)

	require.ErrorContains(t, err, "durable collect failed")
	cursor, loadErr := store.ScoutCursor(context.Background(), "scout-a", "channel:100")
	require.NoError(t, loadErr)
	require.Equal(t, int64(20), cursor.MessageID)
}

type rawUpdateClientFake struct {
	handler telegram.UpdateHandler
	api     *tg.Client
	selfID  int64
}

func (f *rawUpdateClientFake) SetUpdateHandler(handler telegram.UpdateHandler) { f.handler = handler }
func (f *rawUpdateClientFake) Run(ctx context.Context, callback func(context.Context) error) error {
	return callback(ctx)
}
func (f *rawUpdateClientFake) API() *tg.Client                             { return f.api }
func (f *rawUpdateClientFake) Validate(context.Context) (time.Time, error) { return time.Time{}, nil }
func (f *rawUpdateClientFake) SelfUserID() int64                           { return f.selfID }

func TestUpdateActivatorJoinsPublicChannelBeforeInstallingRouter(t *testing.T) {
	invoker := &inviteInvoker{}
	catalogs := &inviteCatalog{row: domain.Channel{
		ID: "public", TelegramID: "pending", Link: "https://t.me/public_channel", Status: domain.ChannelJoining, Active: true,
	}, memberships: map[domain.ID]domain.ChannelMembership{
		"spammer": {AccountID: "spammer", ChannelID: "public", Status: "joining"},
	}}
	activator := NewUpdateActivator(catalogs, &scoutCollectorFake{}, time.Now)
	client := &rawUpdateClientFake{api: tg.NewClient(invoker)}
	snapshot := runtimeconfig.Snapshot{CatalogAssignments: map[domain.SourceCatalog][]domain.ID{
		domain.SourceCatalogOutbound: {"public"},
	}}

	require.NoError(t, activator.Apply(context.Background(), client, domain.Account{ID: "spammer", Role: domain.AccountRoleSpammer}, snapshot))
	require.Equal(t, 1, invoker.joins)
	require.Equal(t, "member", catalogs.memberships["spammer"].Status)
	require.NotNil(t, client.handler)
}

func TestUpdateActivatorSkipsFutureJoiningMembershipWithoutRPC(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	dueAt := time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
	invoker := &inviteInvoker{}
	catalogs := &inviteCatalog{row: domain.Channel{
		ID: "public", TelegramID: "pending", Link: "https://t.me/public_channel", Status: domain.ChannelJoining, Active: true,
	}, memberships: map[domain.ID]domain.ChannelMembership{
		"spammer": {AccountID: "spammer", ChannelID: "public", Status: membershipJoining, JoinNotBefore: timePointer(dueAt)},
	}}
	activator := NewUpdateActivator(catalogs, &scoutCollectorFake{}, func() time.Time { return now })
	client := &rawUpdateClientFake{api: tg.NewClient(invoker)}
	snapshot := runtimeconfig.Snapshot{CatalogAssignments: map[domain.SourceCatalog][]domain.ID{
		domain.SourceCatalogOutbound: {"public"},
	}}

	require.NoError(t, activator.Apply(context.Background(), client, domain.Account{ID: "spammer", Role: domain.AccountRoleSpammer}, snapshot))
	require.Zero(t, invoker.calls)
	require.Equal(t, timePointer(dueAt), catalogs.memberships["spammer"].JoinNotBefore)
}

func TestUpdateActivatorReconcilesRemovalWhilePausedAndUnassigned(t *testing.T) {
	now := time.Now().UTC()
	invoker := &inviteInvoker{}
	catalogs := &inviteCatalog{row: domain.Channel{
		ID: "remove", Link: "https://t.me/public_channel", RemovalRequestedAt: &now,
	}, memberships: map[domain.ID]domain.ChannelMembership{
		"spammer": {AccountID: "spammer", ChannelID: "remove", IsMember: true, Status: membershipMember},
	}}
	activator := NewUpdateActivator(catalogs, nil, time.Now)
	client := &rawUpdateClientFake{api: tg.NewClient(invoker)}

	require.NoError(t, activator.Apply(context.Background(), client, domain.Account{ID: "spammer", Role: domain.AccountRoleSpammer}, runtimeconfig.Snapshot{OutboundPaused: true}))
	require.Equal(t, 1, invoker.leaves)
	require.Empty(t, catalogs.memberships)
	require.NotZero(t, catalogs.completed)
}

func TestUpdateActivatorPropagatesAccountScopedNativeFloodWaitFromPublicJoin(t *testing.T) {
	invoker := &inviteInvoker{joinErr: tgerr.New(420, "FLOOD_WAIT_47")}
	catalogs := &inviteCatalog{row: domain.Channel{
		ID: "public", TelegramID: "pending", Link: "https://t.me/public_channel", Status: domain.ChannelJoining, Active: true,
	}, memberships: map[domain.ID]domain.ChannelMembership{
		"scout": {AccountID: "scout", ChannelID: "public", Status: "joining"},
	}}
	activator := NewUpdateActivator(catalogs, &scoutCollectorFake{}, time.Now)
	client := &rawUpdateClientFake{api: tg.NewClient(invoker)}
	snapshot := runtimeconfig.Snapshot{CatalogAssignments: map[domain.SourceCatalog][]domain.ID{
		domain.SourceCatalogScout: {"public"},
	}}

	err := activator.Apply(context.Background(), client, domain.Account{ID: "scout", Role: domain.AccountRoleScoutAnalyst}, snapshot)
	var flood *FloodWaitError
	require.ErrorAs(t, err, &flood)
	require.Equal(t, domain.ID("scout"), flood.AccountID)
	require.Equal(t, 47*time.Second, flood.RetryAfter())
	require.Equal(t, "joining", catalogs.memberships["scout"].Status)
	require.Nil(t, client.handler)
}

type scoutCatalogFake struct{ rows []domain.Channel }

func (f scoutCatalogFake) List(context.Context, domain.SourceCatalog) ([]domain.Channel, error) {
	return append([]domain.Channel(nil), f.rows...), nil
}
func (f scoutCatalogFake) Save(context.Context, domain.SourceCatalog, domain.Channel) error {
	return nil
}

type blockingScoutCatalog struct {
	entered chan struct{}
	release chan struct{}
}

func (f *blockingScoutCatalog) List(context.Context, domain.SourceCatalog) ([]domain.Channel, error) {
	close(f.entered)
	<-f.release
	return nil, nil
}

func (f *blockingScoutCatalog) Save(context.Context, domain.SourceCatalog, domain.Channel) error {
	return nil
}

func TestUpdateActivatorRegistersScoutBeforeCatalogActivationCompletes(t *testing.T) {
	catalogs := &blockingScoutCatalog{entered: make(chan struct{}), release: make(chan struct{})}
	activator := NewUpdateActivator(catalogs, &scoutCollectorFake{}, time.Now)
	client := &rawUpdateClientFake{}
	done := make(chan error, 1)

	go func() {
		done <- activator.Apply(context.Background(), client, domain.Account{ID: "scout", Role: domain.AccountRoleScoutAnalyst}, runtimeconfig.Snapshot{})
	}()

	<-catalogs.entered
	accountID, connected, err := activator.connectedScout()
	require.NoError(t, err)
	require.Equal(t, domain.ID("scout"), accountID)
	require.Same(t, client, connected)

	close(catalogs.release)
	require.NoError(t, <-done)
}

func TestUpdateActivatorDoesNotReportCancelledScoutAsConnected(t *testing.T) {
	activator := NewUpdateActivator(scoutCatalogFake{}, &scoutCollectorFake{}, time.Now)
	ctx, cancel := context.WithCancel(context.Background())
	client := &rawUpdateClientFake{}
	require.NoError(t, activator.Apply(ctx, client, domain.Account{ID: "scout", Role: domain.AccountRoleScoutAnalyst}, runtimeconfig.Snapshot{}))
	_, _, err := activator.connectedScout()
	require.NoError(t, err)

	cancel()
	require.Eventually(t, func() bool {
		_, _, connectedErr := activator.connectedScout()
		return connectedErr != nil
	}, time.Second, time.Millisecond)
}

type scoutCollectorFake struct {
	ingested  []scouting.IncomingUpdate
	triggered []scouting.IncomingUpdate
	deleted   []scouting.IncomingUpdate
	existing  map[string]map[int64]bool
}

func (f *scoutCollectorFake) Trigger(_ context.Context, update scouting.IncomingUpdate) error {
	f.triggered = append(f.triggered, update)
	return nil
}

func (f *scoutCollectorFake) Ingest(_ context.Context, update scouting.IncomingUpdate) error {
	f.ingested = append(f.ingested, update)
	if f.existing == nil {
		f.existing = make(map[string]map[int64]bool)
	}
	if f.existing[update.ChatID] == nil {
		f.existing[update.ChatID] = make(map[int64]bool)
	}
	f.existing[update.ChatID][update.MessageID] = true
	return nil
}
func (f *scoutCollectorFake) Delete(_ context.Context, chatID string, messageID int64) error {
	f.deleted = append(f.deleted, scouting.IncomingUpdate{ChatID: chatID, MessageID: messageID})
	delete(f.existing[chatID], messageID)
	return nil
}
func (f *scoutCollectorFake) Exists(_ context.Context, chatID string, messageID int64) (bool, error) {
	return f.existing[chatID][messageID], nil
}

type historyTriggerCollectorFake struct {
	scoutCollectorFake
}

func (f *historyTriggerCollectorFake) CollectHistory(ctx context.Context, update scouting.IncomingUpdate) error {
	return f.Ingest(ctx, update)
}

func TestLiveSinkStoresHistoryWithoutTriggeringOutboundReply(t *testing.T) {
	collector := &scoutCollectorFake{}
	sink := NewLiveSink(collector, nil, nil, nil, time.Now)

	err := sink.CollectHistory(context.Background(), scouting.IncomingUpdate{
		ChatID: "discussion", MessageID: 14, Text: "history keyword", MessageAt: time.Now().UTC(),
	})

	require.NoError(t, err)
	require.Len(t, collector.ingested, 1)
	require.Empty(t, collector.triggered)
}

func TestUpdateActivatorWatermarkCatalogEditDeleteAndNoIdentity(t *testing.T) {
	now := time.Date(2026, 7, 12, 10, 0, 10, 0, time.UTC)
	collector := &scoutCollectorFake{}
	activator := NewUpdateActivator(
		scoutCatalogFake{rows: []domain.Channel{
			{ID: "allowed", TelegramID: "100", Active: true},
			{ID: "filtered", TelegramID: "200", Active: true},
		}},
		collector,
		func() time.Time { return now },
	)
	client := &rawUpdateClientFake{}
	snapshot := runtimeconfig.Snapshot{CatalogAssignments: map[domain.SourceCatalog][]domain.ID{domain.SourceCatalogScout: {"allowed"}}}

	require.NoError(t, activator.Apply(context.Background(), client, domain.Account{ID: "scout", Role: domain.AccountRoleScoutAnalyst}, snapshot))
	require.NotNil(t, client.handler)

	dispatch := func(update tg.UpdateClass) {
		require.NoError(t, client.handler.Handle(context.Background(), &tg.Updates{Updates: []tg.UpdateClass{update}}))
	}
	dispatch(&tg.UpdateNewChannelMessage{Message: &tg.Message{ID: 1, PeerID: &tg.PeerChannel{ChannelID: 100}, FromID: &tg.PeerUser{UserID: 777}, Message: "old", Date: int(now.Add(-time.Second).Unix())}})
	dispatch(&tg.UpdateNewChannelMessage{Message: &tg.Message{ID: 2, PeerID: &tg.PeerChannel{ChannelID: 200}, FromID: &tg.PeerUser{UserID: 888}, Message: "filtered", Date: int(now.Unix())}})
	dispatch(&tg.UpdateNewChannelMessage{Message: &tg.Message{ID: 3, PeerID: &tg.PeerChannel{ChannelID: 100}, FromID: &tg.PeerUser{UserID: 999}, Message: "new", Date: int(now.Unix())}})
	dispatch(&tg.UpdateEditChannelMessage{Message: &tg.Message{ID: 3, PeerID: &tg.PeerChannel{ChannelID: 100}, FromID: &tg.PeerUser{UserID: 999}, Message: "edited", Date: int(now.Unix()), EditDate: int(now.Add(time.Second).Unix())}})
	dispatch(&tg.UpdateDeleteChannelMessages{ChannelID: 100, Messages: []int{3, 999}})

	require.Len(t, collector.ingested, 2)
	require.Equal(t, "new", collector.ingested[0].Text)
	require.Equal(t, "edited", collector.ingested[1].Text)
	require.NotNil(t, collector.ingested[1].EditedAt)
	require.Empty(t, collector.ingested[0].SenderID)
	require.Empty(t, collector.ingested[0].SenderUsername)
	require.Equal(t, []scouting.IncomingUpdate{{ChatID: "channel:100", MessageID: 3}}, collector.deleted)
}

func TestUpdateActivatorScopesCollectedMessageIdentityByPeerKind(t *testing.T) {
	now := time.Date(2026, 7, 12, 10, 0, 10, 0, time.UTC)
	collector := &historyTriggerCollectorFake{}
	activator := NewUpdateActivator(
		scoutCatalogFake{rows: []domain.Channel{{ID: "allowed", TelegramID: "100", Active: true}}},
		collector,
		func() time.Time { return now },
	)
	client := &rawUpdateClientFake{}
	snapshot := runtimeconfig.Snapshot{CatalogAssignments: map[domain.SourceCatalog][]domain.ID{domain.SourceCatalogScout: {"allowed"}}}
	require.NoError(t, activator.Apply(context.Background(), client, domain.Account{ID: "scout", Role: domain.AccountRoleScoutAnalyst}, snapshot))

	require.NoError(t, client.handler.Handle(context.Background(), &tg.Updates{Updates: []tg.UpdateClass{
		&tg.UpdateNewChannelMessage{Message: &tg.Message{ID: 3, PeerID: &tg.PeerChannel{ChannelID: 100}, Message: "channel", Date: int(now.Unix())}},
		&tg.UpdateNewMessage{Message: &tg.Message{ID: 3, PeerID: &tg.PeerChat{ChatID: 100}, Message: "chat", Date: int(now.Unix())}},
	}}))

	require.Len(t, collector.ingested, 2)
	require.Equal(t, []string{"channel:100", "chat:100"}, []string{collector.ingested[0].ChatID, collector.ingested[1].ChatID})
	require.Equal(t, []string{"100", "100"}, []string{collector.triggered[0].ChatID, collector.triggered[1].ChatID})
}

func TestUpdateActivatorRoutesOutboundTriggersToSpammerWithoutScoutCollection(t *testing.T) {
	now := time.Date(2026, 7, 12, 10, 0, 10, 0, time.UTC)
	client := &rawUpdateClientFake{}
	collector := &scoutCollectorFake{}
	activator := NewUpdateActivator(
		scoutCatalogFake{rows: []domain.Channel{{ID: "outbound", TelegramID: "100", Active: true}}},
		collector,
		func() time.Time { return now },
	)
	snapshot := runtimeconfig.Snapshot{CatalogAssignments: map[domain.SourceCatalog][]domain.ID{domain.SourceCatalogOutbound: {"outbound"}}}

	err := activator.Apply(context.Background(), client, domain.Account{ID: "spam", Role: domain.AccountRoleSpammer}, snapshot)

	require.NoError(t, err)
	require.NotNil(t, client.handler)
	require.NoError(t, client.handler.Handle(context.Background(), &tg.Updates{Updates: []tg.UpdateClass{
		&tg.UpdateNewChannelMessage{Message: &tg.Message{ID: 5, PeerID: &tg.PeerChannel{ChannelID: 100}, Message: "trigger", Date: int(now.Unix())}},
	}}))
	require.Empty(t, collector.ingested)
	require.Len(t, collector.triggered, 1)
	require.Equal(t, "100", collector.triggered[0].ChatID)
}

func TestUpdateActivatorNeverTriggersFromAnotherManagedAccount(t *testing.T) {
	now := time.Date(2026, 7, 29, 2, 16, 0, 0, time.UTC)
	collector := &scoutCollectorFake{}
	activator := NewUpdateActivator(
		scoutCatalogFake{rows: []domain.Channel{{ID: "outbound", TelegramID: "100", Active: true}}},
		collector,
		func() time.Time { return now },
	)
	snapshot := runtimeconfig.Snapshot{CatalogAssignments: map[domain.SourceCatalog][]domain.ID{
		domain.SourceCatalogOutbound: {"outbound"},
	}}
	first := &rawUpdateClientFake{selfID: 777}
	second := &rawUpdateClientFake{selfID: 888}

	require.NoError(t, activator.Apply(context.Background(), first, domain.Account{ID: "first", Role: domain.AccountRoleSpammer}, snapshot))
	require.NoError(t, activator.Apply(context.Background(), second, domain.Account{ID: "second", Role: domain.AccountRoleSpammer}, snapshot))

	fromManaged := &tg.Message{ID: 10, PeerID: &tg.PeerChannel{ChannelID: 100}, Message: "бабок", Date: int(now.Unix())}
	fromManaged.SetFromID(&tg.PeerUser{UserID: 777})
	require.NoError(t, second.handler.Handle(context.Background(), &tg.Updates{Updates: []tg.UpdateClass{
		&tg.UpdateNewChannelMessage{Message: fromManaged},
	}}))
	require.Empty(t, collector.triggered)

	fromFriend := &tg.Message{ID: 11, PeerID: &tg.PeerChannel{ChannelID: 100}, Message: "бабок", Date: int(now.Unix())}
	fromFriend.SetFromID(&tg.PeerUser{UserID: 999})
	require.NoError(t, second.handler.Handle(context.Background(), &tg.Updates{Updates: []tg.UpdateClass{
		&tg.UpdateNewChannelMessage{Message: fromFriend},
	}}))
	require.Len(t, collector.triggered, 1)
	require.Equal(t, int64(11), collector.triggered[0].MessageID)
}

func TestUpdateActivatorStillCollectsManagedMessagesForAnalytics(t *testing.T) {
	now := time.Date(2026, 7, 29, 2, 17, 0, 0, time.UTC)
	collector := &historyTriggerCollectorFake{}
	activator := NewUpdateActivator(
		scoutCatalogFake{rows: []domain.Channel{{ID: "channel", TelegramID: "100", Active: true}}},
		collector,
		func() time.Time { return now },
	)
	snapshot := runtimeconfig.Snapshot{CatalogAssignments: map[domain.SourceCatalog][]domain.ID{
		domain.SourceCatalogOutbound: {"channel"},
		domain.SourceCatalogScout:    {"channel"},
	}}
	spammer := &rawUpdateClientFake{selfID: 777}
	scout := &rawUpdateClientFake{selfID: 888}

	require.NoError(t, activator.Apply(context.Background(), spammer, domain.Account{ID: "spammer", Role: domain.AccountRoleSpammer}, snapshot))
	require.NoError(t, activator.Apply(context.Background(), scout, domain.Account{ID: "scout", Role: domain.AccountRoleScoutAnalyst}, snapshot))

	message := &tg.Message{ID: 12, PeerID: &tg.PeerChannel{ChannelID: 100}, Message: "бабок", Date: int(now.Unix())}
	message.SetFromID(&tg.PeerUser{UserID: 777})
	require.NoError(t, scout.handler.Handle(context.Background(), &tg.Updates{Updates: []tg.UpdateClass{
		&tg.UpdateNewChannelMessage{Message: message},
	}}))

	require.Len(t, collector.ingested, 1)
	require.Equal(t, "channel:100", collector.ingested[0].ChatID)
	require.Empty(t, collector.triggered)
}

func TestUpdateActivatorImportsPrivateInviteForEachPersistedScoutIntent(t *testing.T) {
	now := time.Date(2026, 7, 12, 10, 0, 10, 0, time.UTC)
	invoker := &inviteInvoker{}
	catalogs := &inviteCatalog{row: domain.Channel{
		ID: "private", TelegramID: "pending", Link: "https://t.me/+privateInvite", Status: domain.ChannelJoining, Active: true,
	}, memberships: map[domain.ID]domain.ChannelMembership{
		"scout-a": {AccountID: "scout-a", ChannelID: "private", Status: "joining"},
		"scout-b": {AccountID: "scout-b", ChannelID: "private", Status: "joining"},
	}}
	activator := NewUpdateActivator(catalogs, &scoutCollectorFake{}, func() time.Time { return now })
	snapshot := runtimeconfig.Snapshot{CatalogAssignments: map[domain.SourceCatalog][]domain.ID{domain.SourceCatalogScout: {"private"}}}

	require.NoError(t, activator.Apply(context.Background(), &rawUpdateClientFake{api: tg.NewClient(invoker)}, domain.Account{ID: "scout-a", Role: domain.AccountRoleScoutAnalyst}, snapshot))
	require.NoError(t, activator.Apply(context.Background(), &rawUpdateClientFake{api: tg.NewClient(invoker)}, domain.Account{ID: "scout-b", Role: domain.AccountRoleScoutAnalyst}, snapshot))
	require.Equal(t, 2, invoker.imports)
	require.Equal(t, "member", catalogs.memberships["scout-a"].Status)
	require.Equal(t, "member", catalogs.memberships["scout-b"].Status)
}

func TestUpdateActivatorReplacesScoutRouterWhenAccountBecomesSpammer(t *testing.T) {
	now := time.Date(2026, 7, 12, 10, 0, 10, 0, time.UTC)
	collector := &scoutCollectorFake{}
	activator := NewUpdateActivator(
		scoutCatalogFake{rows: []domain.Channel{{ID: "channel", TelegramID: "100", Active: true}}},
		collector, func() time.Time { return now },
	)
	client := &rawUpdateClientFake{}
	require.NoError(t, activator.Apply(context.Background(), client,
		domain.Account{ID: "account", Role: domain.AccountRoleScoutAnalyst},
		runtimeconfig.Snapshot{CatalogAssignments: map[domain.SourceCatalog][]domain.ID{domain.SourceCatalogScout: {"channel"}}}))
	require.NoError(t, activator.Apply(context.Background(), client,
		domain.Account{ID: "account", Role: domain.AccountRoleSpammer},
		runtimeconfig.Snapshot{CatalogAssignments: map[domain.SourceCatalog][]domain.ID{domain.SourceCatalogOutbound: {"channel"}}}))

	require.NoError(t, client.handler.Handle(context.Background(), &tg.Updates{Updates: []tg.UpdateClass{
		&tg.UpdateNewChannelMessage{Message: &tg.Message{ID: 6, PeerID: &tg.PeerChannel{ChannelID: 100}, Message: "trigger", Date: int(now.Unix())}},
	}}))
	require.Empty(t, collector.ingested)
	require.Len(t, collector.triggered, 1)
}

func TestUpdateActivatorExtractsOpaqueSenderReferenceFromGotdEntities(t *testing.T) {
	now := time.Date(2026, 7, 12, 10, 0, 10, 0, time.UTC)
	collector := &scoutCollectorFake{}
	references := NewSenderReferences()
	activator := NewUpdateActivator(
		scoutCatalogFake{rows: []domain.Channel{{ID: "allowed", TelegramID: "100", Active: true}}},
		collector,
		func() time.Time { return now },
		references,
	)
	client := &rawUpdateClientFake{}
	snapshot := runtimeconfig.Snapshot{CatalogAssignments: map[domain.SourceCatalog][]domain.ID{domain.SourceCatalogScout: {"allowed"}}}
	require.NoError(t, activator.Apply(context.Background(), client, domain.Account{ID: "scout", Role: domain.AccountRoleScoutAnalyst}, snapshot))

	message := &tg.Message{ID: 4, PeerID: &tg.PeerChannel{ChannelID: 100}, Message: "new", Date: int(now.Unix())}
	message.SetFromID(&tg.PeerUser{UserID: 777})
	require.NoError(t, client.handler.Handle(context.Background(), &tg.Updates{
		Users:   []tg.UserClass{&tg.User{ID: 777, AccessHash: 9988}},
		Updates: []tg.UpdateClass{&tg.UpdateNewChannelMessage{Message: message}},
	}))

	require.Len(t, collector.ingested, 1)
	reference := collector.ingested[0].SenderID
	require.NotEmpty(t, reference)
	require.NotContains(t, reference, "777")
	require.Empty(t, collector.ingested[0].SenderUsername)
}

func TestUpdateActivatorRoutesScoutUpdatesFromLinkedDiscussion(t *testing.T) {
	now := time.Date(2026, 7, 13, 20, 0, 0, 0, time.UTC)
	collector := &scoutCollectorFake{}
	catalogs := &linkedDiscussionCatalog{
		rows: []domain.Channel{{
			ID: "source", TelegramID: "100", Title: "Source", Link: "https://t.me/source", Status: domain.ChannelReady, Active: true,
		}},
		memberships: map[string]domain.ChannelMembership{
			linkedDiscussionMembershipKey("scout", "source"): {AccountID: "scout", ChannelID: "source", IsMember: true, Status: "member"},
		},
	}
	invoker := &linkedDiscussionInvoker{}
	activator := NewUpdateActivator(catalogs, collector, func() time.Time { return now })
	client := &rawUpdateClientFake{api: tg.NewClient(invoker)}
	snapshot := runtimeconfig.Snapshot{Revision: 1, CatalogAssignments: map[domain.SourceCatalog][]domain.ID{
		domain.SourceCatalogScout: {"source"},
	}}

	require.NoError(t, activator.Apply(context.Background(), client, domain.Account{ID: "scout", Role: domain.AccountRoleScoutAnalyst}, snapshot))
	require.NoError(t, client.handler.Handle(context.Background(), &tg.Updates{Updates: []tg.UpdateClass{
		&tg.UpdateNewChannelMessage{Message: &tg.Message{
			ID: 7, PeerID: &tg.PeerChannel{ChannelID: 200}, Message: "from discussion", Date: int(now.Unix()),
		}},
	}}))

	require.Equal(t, 1, invoker.fullChannelCalls)
	require.Equal(t, 1, invoker.discussionJoins)
	parent, ok := catalogs.channel("source")
	require.True(t, ok)
	require.Equal(t, "100", parent.TelegramID)
	discussion, ok := catalogs.channel("discussion:source:200")
	require.True(t, ok)
	require.Equal(t, "200", discussion.TelegramID)
	require.Equal(t, "Source Chat", discussion.Title)
	require.Equal(t, "https://t.me/source#tc-discussion=200", discussion.Link)
	membership, ok := catalogs.memberships[linkedDiscussionMembershipKey("scout", discussion.ID)]
	require.True(t, ok)
	require.True(t, membership.IsMember)
	require.Equal(t, "member", membership.Status)
	require.Len(t, collector.ingested, 1)
	require.Equal(t, "channel:200", collector.ingested[0].ChatID)
	require.Equal(t, "from discussion", collector.ingested[0].Text)

	lastActivity := now.Add(time.Minute)
	discussion.MessageCount = 9
	discussion.LastActivityAt = &lastActivity
	require.NoError(t, catalogs.Save(context.Background(), domain.SourceCatalogScout, discussion))
	snapshot.Revision = 2
	snapshot.CatalogAssignments[domain.SourceCatalogScout] = []domain.ID{"source", discussion.ID}
	require.NoError(t, activator.Apply(context.Background(), client, domain.Account{ID: "scout", Role: domain.AccountRoleScoutAnalyst}, snapshot))
	discussion, ok = catalogs.channel(discussion.ID)
	require.True(t, ok)
	require.Equal(t, int64(9), discussion.MessageCount)
	require.Equal(t, lastActivity, *discussion.LastActivityAt)
	require.Equal(t, 1, invoker.fullChannelCalls)
	require.Equal(t, 1, invoker.discussionJoins)

	parent, ok = catalogs.channel("source")
	require.True(t, ok)
	parent.Active = false
	parent.Status = domain.ChannelPaused
	require.NoError(t, catalogs.Save(context.Background(), domain.SourceCatalogScout, parent))
	snapshot.Revision = 3
	require.NoError(t, activator.Apply(context.Background(), client, domain.Account{ID: "scout", Role: domain.AccountRoleScoutAnalyst}, snapshot))
	discussion, ok = catalogs.channel(discussion.ID)
	require.True(t, ok)
	require.False(t, discussion.Active)
	require.NoError(t, client.handler.Handle(context.Background(), &tg.Updates{Updates: []tg.UpdateClass{
		&tg.UpdateNewChannelMessage{Message: &tg.Message{
			ID: 8, PeerID: &tg.PeerChannel{ChannelID: 200}, Message: "after pause", Date: int(now.Unix()),
		}},
	}}))
	require.Len(t, collector.ingested, 1)
}

func TestUpdateActivatorKeepsRouterWhenLinkedDiscussionJoinFails(t *testing.T) {
	now := time.Date(2026, 7, 13, 20, 0, 0, 0, time.UTC)
	collector := &scoutCollectorFake{}
	catalogs := &linkedDiscussionCatalog{
		rows: []domain.Channel{{
			ID: "source", TelegramID: "100", Title: "Source", Link: "https://t.me/source", Status: domain.ChannelReady, Active: true,
		}},
		memberships: map[string]domain.ChannelMembership{
			linkedDiscussionMembershipKey("scout", "source"): {AccountID: "scout", ChannelID: "source", IsMember: true, Status: "member"},
		},
	}
	invoker := &linkedDiscussionInvoker{discussionJoinErr: errors.New("discussion join failed")}
	activator := NewUpdateActivator(catalogs, collector, func() time.Time { return now })
	client := &rawUpdateClientFake{api: tg.NewClient(invoker)}
	snapshot := runtimeconfig.Snapshot{CatalogAssignments: map[domain.SourceCatalog][]domain.ID{
		domain.SourceCatalogScout: {"source"},
	}}

	require.NoError(t, activator.Apply(context.Background(), client, domain.Account{ID: "scout", Role: domain.AccountRoleScoutAnalyst}, snapshot))
	require.NotNil(t, client.handler)
	source, ok := catalogs.channel("source")
	require.True(t, ok)
	require.Equal(t, domain.ChannelError, source.Status)
	require.Contains(t, source.LastError, "discussion join failed")

	require.NoError(t, client.handler.Handle(context.Background(), &tg.Updates{Updates: []tg.UpdateClass{
		&tg.UpdateNewChannelMessage{Message: &tg.Message{
			ID: 9, PeerID: &tg.PeerChannel{ChannelID: 100}, Message: "from source", Date: int(now.Unix()),
		}},
	}}))
	require.Len(t, collector.ingested, 1)
	require.Equal(t, "channel:100", collector.ingested[0].ChatID)
}

type linkedDiscussionInvoker struct {
	fullChannelCalls  int
	discussionJoins   int
	discussionJoinErr error
}

func (i *linkedDiscussionInvoker) Invoke(_ context.Context, input bin.Encoder, output bin.Decoder) error {
	switch input.(type) {
	case *tg.ContactsResolveUsernameRequest:
		result := output.(*tg.ContactsResolvedPeer)
		*result = tg.ContactsResolvedPeer{
			Peer:  &tg.PeerChannel{ChannelID: 100},
			Chats: []tg.ChatClass{&tg.Channel{ID: 100, AccessHash: 101, Title: "Source", Broadcast: true}},
		}
	case *tg.ChannelsGetFullChannelRequest:
		i.fullChannelCalls++
		full := &tg.ChannelFull{ID: 100}
		full.SetLinkedChatID(200)
		result := output.(*tg.MessagesChatFull)
		*result = tg.MessagesChatFull{
			FullChat: full,
			Chats: []tg.ChatClass{
				&tg.Channel{ID: 200, AccessHash: 201, Title: "Source Chat", Megagroup: true},
			},
		}
	case *tg.ChannelsJoinChannelRequest:
		i.discussionJoins++
		if i.discussionJoinErr != nil {
			return i.discussionJoinErr
		}
		output.(*tg.MessagesChatInviteJoinResultBox).ChatInviteJoinResult = &tg.MessagesChatInviteJoinResultOk{Updates: &tg.Updates{}}
	}
	return nil
}

type linkedDiscussionCatalog struct {
	rows        []domain.Channel
	memberships map[string]domain.ChannelMembership
}

func (c *linkedDiscussionCatalog) List(context.Context, domain.SourceCatalog) ([]domain.Channel, error) {
	return append([]domain.Channel(nil), c.rows...), nil
}

func (c *linkedDiscussionCatalog) Save(_ context.Context, _ domain.SourceCatalog, channel domain.Channel) error {
	for index, existing := range c.rows {
		if existing.ID == channel.ID {
			c.rows[index] = channel
			return nil
		}
	}
	c.rows = append(c.rows, channel)
	return nil
}

func (c *linkedDiscussionCatalog) LoadMembership(_ context.Context, accountID domain.ID, _ domain.SourceCatalog, channelID domain.ID) (domain.ChannelMembership, bool, error) {
	membership, ok := c.memberships[linkedDiscussionMembershipKey(accountID, channelID)]
	return membership, ok, nil
}

func (c *linkedDiscussionCatalog) SaveMembership(_ context.Context, _ domain.SourceCatalog, membership domain.ChannelMembership) error {
	c.memberships[linkedDiscussionMembershipKey(membership.AccountID, membership.ChannelID)] = membership
	return nil
}

func (c *linkedDiscussionCatalog) channel(id domain.ID) (domain.Channel, bool) {
	for _, channel := range c.rows {
		if channel.ID == id {
			return channel, true
		}
	}
	return domain.Channel{}, false
}

func linkedDiscussionMembershipKey(accountID, channelID domain.ID) string {
	return string(accountID) + ":" + string(channelID)
}

func TestUpdateActivatorHotRevisionPreservesWatermarkAndSeenTracking(t *testing.T) {
	now := time.Date(2026, 7, 12, 10, 0, 10, 0, time.UTC)
	collector := &scoutCollectorFake{}
	activator := NewUpdateActivator(
		scoutCatalogFake{rows: []domain.Channel{{ID: "allowed", TelegramID: "100", Active: true}}},
		collector,
		func() time.Time { return now },
	)
	client := &rawUpdateClientFake{}
	snapshot := runtimeconfig.Snapshot{Revision: 1, CatalogAssignments: map[domain.SourceCatalog][]domain.ID{domain.SourceCatalogScout: {"allowed"}}}
	require.NoError(t, activator.Apply(context.Background(), client, domain.Account{ID: "scout", Role: domain.AccountRoleScoutAnalyst}, snapshot))
	dispatch := func(update tg.UpdateClass) {
		require.NoError(t, client.handler.Handle(context.Background(), &tg.Updates{Updates: []tg.UpdateClass{update}}))
	}
	dispatch(&tg.UpdateNewChannelMessage{Message: &tg.Message{ID: 31, PeerID: &tg.PeerChannel{ChannelID: 100}, Message: "new", Date: int(now.Unix())}})

	now = now.Add(time.Minute)
	snapshot.Revision = 2
	require.NoError(t, activator.Apply(context.Background(), client, domain.Account{ID: "scout", Role: domain.AccountRoleScoutAnalyst}, snapshot))
	dispatch(&tg.UpdateEditChannelMessage{Message: &tg.Message{ID: 31, PeerID: &tg.PeerChannel{ChannelID: 100}, Message: "edited", Date: int(now.Add(-time.Minute).Unix()), EditDate: int(now.Unix())}})
	dispatch(&tg.UpdateDeleteChannelMessages{ChannelID: 100, Messages: []int{31}})

	require.Len(t, collector.ingested, 2)
	require.Equal(t, "edited", collector.ingested[1].Text)
	require.Equal(t, []scouting.IncomingUpdate{{ChatID: "channel:100", MessageID: 31}}, collector.deleted)
}

func TestUpdateActivatorExplicitResetKeepsPersistedEditDeleteTrackingWithoutBackfill(t *testing.T) {
	now := time.Date(2026, 7, 12, 10, 0, 10, 0, time.UTC)
	collector := &scoutCollectorFake{}
	activator := NewUpdateActivator(
		scoutCatalogFake{rows: []domain.Channel{{ID: "allowed", TelegramID: "100", Active: true}}},
		collector,
		func() time.Time { return now },
	)
	client := &rawUpdateClientFake{}
	snapshot := runtimeconfig.Snapshot{Revision: 1, CatalogAssignments: map[domain.SourceCatalog][]domain.ID{domain.SourceCatalogScout: {"allowed"}}}
	require.NoError(t, activator.Apply(context.Background(), client, domain.Account{ID: "scout", Role: domain.AccountRoleScoutAnalyst}, snapshot))
	require.NoError(t, client.handler.Handle(context.Background(), &tg.Updates{Updates: []tg.UpdateClass{
		&tg.UpdateNewChannelMessage{Message: &tg.Message{ID: 41, PeerID: &tg.PeerChannel{ChannelID: 100}, Message: "before stop", Date: int(now.Unix())}},
	}}))

	activator.ResetExplicitLifecycle()
	now = now.Add(time.Minute)
	snapshot.Revision = 2
	require.NoError(t, activator.Apply(context.Background(), client, domain.Account{ID: "scout", Role: domain.AccountRoleScoutAnalyst}, snapshot))
	require.NoError(t, client.handler.Handle(context.Background(), &tg.Updates{Updates: []tg.UpdateClass{
		&tg.UpdateEditChannelMessage{Message: &tg.Message{ID: 41, PeerID: &tg.PeerChannel{ChannelID: 100}, Message: "old edit", Date: int(now.Add(-time.Minute).Unix()), EditDate: int(now.Unix())}},
		&tg.UpdateEditChannelMessage{Message: &tg.Message{ID: 40, PeerID: &tg.PeerChannel{ChannelID: 100}, Message: "unknown old edit", Date: int(now.Add(-time.Minute).Unix()), EditDate: int(now.Unix())}},
		&tg.UpdateDeleteChannelMessages{ChannelID: 100, Messages: []int{41}},
		&tg.UpdateNewChannelMessage{Message: &tg.Message{ID: 42, PeerID: &tg.PeerChannel{ChannelID: 100}, Message: "after start", Date: int(now.Unix())}},
	}}))

	require.Len(t, collector.ingested, 3)
	require.Equal(t, "old edit", collector.ingested[1].Text)
	require.Equal(t, "after start", collector.ingested[2].Text)
	require.Equal(t, []scouting.IncomingUpdate{{ChatID: "channel:100", MessageID: 41}}, collector.deleted)
}
