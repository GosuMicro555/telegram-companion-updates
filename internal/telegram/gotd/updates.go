package gotd

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"telegram-companion/internal/domain"
	"telegram-companion/internal/usecase"
	"telegram-companion/internal/usecase/runtimeconfig"
	"telegram-companion/internal/usecase/scouting"
)

type UpdateCollector interface {
	Ingest(context.Context, scouting.IncomingUpdate) error
	Delete(context.Context, string, int64) error
	Exists(context.Context, string, int64) (bool, error)
}

type UpdateTrigger interface {
	Trigger(context.Context, scouting.IncomingUpdate) error
}

type HistoryCollector interface {
	CollectHistory(context.Context, scouting.IncomingUpdate) error
}

type historyTriggerCollector interface {
	HistoryCollector
	UpdateTrigger
}

type selfUserIDProvider interface {
	SelfUserID() int64
}

type managedSenderRegistry struct {
	mu      sync.RWMutex
	userIDs map[int64]struct{}
}

func newManagedSenderRegistry() *managedSenderRegistry {
	return &managedSenderRegistry{userIDs: make(map[int64]struct{})}
}

func (r *managedSenderRegistry) Add(userID int64) {
	if r == nil || userID == 0 {
		return
	}
	r.mu.Lock()
	r.userIDs[userID] = struct{}{}
	r.mu.Unlock()
}

func (r *managedSenderRegistry) Contains(userID int64) bool {
	if r == nil || userID == 0 {
		return false
	}
	r.mu.RLock()
	_, ok := r.userIDs[userID]
	r.mu.RUnlock()
	return ok
}

type rawUpdateHandlerSetter interface {
	SetUpdateHandler(telegram.UpdateHandler)
}

type UpdateActivator struct {
	catalogs     domain.CatalogRepository
	memberships  accountMembershipCatalog
	collector    UpdateCollector
	now          func() time.Time
	references   *SenderReferences
	mu           sync.Mutex
	routers      map[domain.ID]*scoutUpdateRouter
	setters      map[domain.ID]rawUpdateHandlerSetter
	scouts       map[domain.ID]TelegramClient
	scoutEpoch   map[domain.ID]uint64
	historyStore domain.AnalyticsSchedulerStore
	managed      *managedSenderRegistry
}

func (a *UpdateActivator) ConfigureHistoryStore(store domain.AnalyticsSchedulerStore) {
	if a == nil {
		return
	}
	a.mu.Lock()
	a.historyStore = store
	a.mu.Unlock()
}

func NewUpdateActivator(catalogs domain.CatalogRepository, collector UpdateCollector, now func() time.Time, references ...*SenderReferences) *UpdateActivator {
	var senderReferences *SenderReferences
	if len(references) > 0 {
		senderReferences = references[0]
	}
	memberships, _ := catalogs.(accountMembershipCatalog)
	return &UpdateActivator{
		catalogs: catalogs, memberships: memberships, collector: collector, now: now, references: senderReferences,
		routers: make(map[domain.ID]*scoutUpdateRouter), setters: make(map[domain.ID]rawUpdateHandlerSetter),
		scouts: make(map[domain.ID]TelegramClient), scoutEpoch: make(map[domain.ID]uint64),
		managed: newManagedSenderRegistry(),
	}
}

func (a *UpdateActivator) reconcileMemberships(
	ctx context.Context,
	client TelegramClient,
	account domain.Account,
	snapshot runtimeconfig.Snapshot,
	primaryCatalog domain.SourceCatalog,
) (map[domain.SourceCatalog][]domain.Channel, map[domain.SourceCatalog]map[domain.ID]tg.InputPeerClass, error) {
	catalogRows := make(map[domain.SourceCatalog][]domain.Channel, 2)
	catalogPeers := make(map[domain.SourceCatalog]map[domain.ID]tg.InputPeerClass, 2)
	for _, currentCatalog := range []domain.SourceCatalog{primaryCatalog, otherCatalog(primaryCatalog)} {
		if currentCatalog != primaryCatalog && a.memberships == nil {
			continue
		}
		rows, err := a.catalogs.List(ctx, currentCatalog)
		if err != nil {
			return nil, nil, err
		}
		assigned := assignedCatalogChannels(snapshot, currentCatalog)
		rows, peers, err := reconcileCatalogMembershipIntents(
			ctx, client.API(), a.catalogs, a.memberships, account.ID, currentCatalog, rows, assigned, a.now().UTC(),
		)
		if err != nil {
			return nil, nil, err
		}
		catalogRows[currentCatalog] = rows
		catalogPeers[currentCatalog] = peers
	}
	return catalogRows, catalogPeers, nil
}

func (a *UpdateActivator) Apply(ctx context.Context, client TelegramClient, account domain.Account, snapshot runtimeconfig.Snapshot) error {
	if account.Role != domain.AccountRoleScoutAnalyst && account.Role != domain.AccountRoleSpammer {
		return nil
	}
	if a == nil || a.catalogs == nil || a.now == nil {
		return errors.New("update activator is not configured")
	}
	if provider, ok := client.(selfUserIDProvider); ok {
		a.managed.Add(provider.SelfUserID())
	}
	catalog := domain.SourceCatalogScout
	collect := true
	if account.Role == domain.AccountRoleSpammer {
		catalog = domain.SourceCatalogOutbound
		collect = false
	}
	if snapshot.OutboundPaused {
		if _, _, err := a.reconcileMemberships(ctx, client, account, snapshot, catalog); err != nil {
			return err
		}
		a.Disable(client, account.ID)
		return nil
	}
	if a.collector == nil {
		return errors.New("update activator is not configured")
	}
	setter, ok := client.(rawUpdateHandlerSetter)
	if !ok {
		return errors.New("telegram client does not support update dispatch")
	}
	if !collect {
		if _, ok := a.collector.(UpdateTrigger); !ok {
			return errors.New("outbound update trigger is not configured")
		}
	}
	if collect {
		a.mu.Lock()
		a.scouts[account.ID] = client
		a.scoutEpoch[account.ID]++
		epoch := a.scoutEpoch[account.ID]
		a.mu.Unlock()
		if ctx.Done() != nil {
			go a.unregisterScoutOnDisconnect(ctx, account.ID, epoch)
		}
	}
	catalogRows, catalogPeers, err := a.reconcileMemberships(ctx, client, account, snapshot, catalog)
	if err != nil {
		return err
	}
	rows := catalogRows[catalog]
	assigned := assignedCatalogChannels(snapshot, catalog)
	allowed := make(map[string]struct{})
	for _, row := range rows {
		if _, ok := assigned[row.ID]; !ok || row.TelegramID == "" {
			continue
		}
		if catalog == domain.SourceCatalogScout && isDerivedScoutDiscussion(row) {
			continue
		}

		var derivedDiscussion *domain.Channel
		if catalog == domain.SourceCatalogScout {
			if existing, ok := derivedScoutDiscussionFor(rows, row.ID); ok {
				synchronized, changed := synchronizeDerivedScoutDiscussion(existing, row, a.now().UTC())
				if changed {
					if err := a.catalogs.Save(ctx, catalog, synchronized); err != nil {
						return err
					}
				}
				derivedDiscussion = &synchronized
			}
		}
		if !row.Active {
			continue
		}

		resolvedPeer := catalogPeers[catalog][row.ID]
		_, membershipAllowed, membershipErr := loadOrCreateAccountMembership(ctx, a.memberships, account.ID, catalog, row)
		if membershipErr != nil {
			return membershipErr
		}
		if !membershipAllowed {
			continue
		}
		if _, err := strconv.ParseInt(row.TelegramID, 10, 64); err != nil && client.API() != nil {
			var peer tg.InputPeerClass
			var title string
			var resolveErr error
			peer, title, resolveErr = resolveTelegramPeer(ctx, client.API(), row.Link, false)
			if resolveErr != nil {
				return accountRPCError(account.ID, resolveErr)
			}
			resolvedPeer, title, resolveErr = resolveExplicitDiscussionTarget(ctx, client.API(), row.Link, peer, title)
			if resolveErr != nil {
				return accountRPCError(account.ID, resolveErr)
			}
			row.TelegramID = inputPeerID(resolvedPeer)
			if title != "" {
				row.Title = title
			}
			row.Status = domain.ChannelReady
			row.UpdatedAt = a.now().UTC()
			if err := a.catalogs.Save(ctx, catalog, row); err != nil {
				return err
			}
			if catalog == domain.SourceCatalogScout {
				if err := a.copyResolvedPeerToOutbound(ctx, row); err != nil {
					return err
				}
			}
		}
		if catalog == domain.SourceCatalogScout && client.API() != nil {
			discussionReady, readyErr := a.derivedScoutDiscussionMember(ctx, account.ID, derivedDiscussion)
			if readyErr != nil {
				return readyErr
			}
			if discussionReady {
				allowed[derivedDiscussion.TelegramID] = struct{}{}
			} else {
				if resolvedPeer == nil {
					var resolveErr error
					resolvedPeer, _, resolveErr = resolveTelegramPeer(ctx, client.API(), row.Link, false)
					if resolveErr != nil {
						return accountRPCError(account.ID, resolveErr)
					}
				}
				discussion, title, found, discussionErr := resolveLinkedScoutDiscussion(ctx, client.API(), resolvedPeer)
				if discussionErr != nil {
					target := row
					if derivedDiscussion != nil {
						target = *derivedDiscussion
					}
					target = markScoutDiscussionError(target, discussionErr, a.now().UTC())
					if err := a.catalogs.Save(ctx, catalog, target); err != nil {
						return err
					}
				} else if found {
					now := a.now().UTC()
					discussionRow := derivedScoutDiscussion(row, discussion, title, now)
					if derivedDiscussion != nil {
						discussionRow = refreshDerivedScoutDiscussion(*derivedDiscussion, row, discussion, title, now)
					}
					if err := a.catalogs.Save(ctx, catalog, discussionRow); err != nil {
						return err
					}
					if a.memberships != nil {
						checkedAt := discussionRow.UpdatedAt
						if err := a.memberships.SaveMembership(ctx, catalog, domain.ChannelMembership{
							AccountID: account.ID, ChannelID: discussionRow.ID, IsMember: true, Status: "member", LastCheckAt: &checkedAt,
						}); err != nil {
							return err
						}
					}
					allowed[discussionRow.TelegramID] = struct{}{}
				}
			}
		}
		allowed[row.TelegramID] = struct{}{}
	}
	a.mu.Lock()
	router := a.routers[account.ID]
	if router == nil || router.collect != collect {
		router = newScoutUpdateRouter(a.collector, allowed, a.now().UTC().Unix(), collect, a.references)
		router.managed = a.managed
		a.routers[account.ID] = router
	} else {
		router.setAllowed(allowed)
	}
	a.setters[account.ID] = setter
	if collect {
		a.scouts[account.ID] = client
	} else {
		delete(a.scouts, account.ID)
		a.scoutEpoch[account.ID]++
	}
	a.mu.Unlock()
	setter.SetUpdateHandler(router.dispatcher())
	return nil
}

func otherCatalog(catalog domain.SourceCatalog) domain.SourceCatalog {
	if catalog == domain.SourceCatalogOutbound {
		return domain.SourceCatalogScout
	}
	return domain.SourceCatalogOutbound
}

func assignedCatalogChannels(snapshot runtimeconfig.Snapshot, catalog domain.SourceCatalog) map[domain.ID]struct{} {
	assigned := make(map[domain.ID]struct{}, len(snapshot.CatalogAssignments[catalog]))
	for _, id := range snapshot.CatalogAssignments[catalog] {
		assigned[id] = struct{}{}
	}
	return assigned
}

func resolveExplicitDiscussionTarget(ctx context.Context, api *tg.Client, link string, parent tg.InputPeerClass, parentTitle string) (tg.InputPeerClass, string, error) {
	wantedID, explicit := explicitDiscussionTargetID(link)
	if !explicit {
		return parent, parentTitle, nil
	}
	if api == nil {
		return nil, "", errors.New("telegram API is unavailable for discussion target")
	}
	discussion, title, found, err := resolveLinkedScoutDiscussion(ctx, api, parent)
	if err != nil {
		return nil, "", err
	}
	if !found || inputPeerID(discussion) != wantedID {
		return nil, "", fmt.Errorf("requested discussion %s is not available for this channel", wantedID)
	}
	return discussion, title, nil
}

const (
	scoutHistoryPageSize  = 100
	scoutHistoryPageDelay = 250 * time.Millisecond
	scoutHistoryRetention = 7 * 24 * time.Hour
)

type analyticsHistoryCycle struct {
	result       usecase.AnalyticsHistoryResult
	failures     []error
	scannedChats map[string]struct{}
}

func newAnalyticsHistoryCycle() *analyticsHistoryCycle {
	return &analyticsHistoryCycle{scannedChats: make(map[string]struct{})}
}

func (c *analyticsHistoryCycle) scanChat(chatID string) {
	if _, scanned := c.scannedChats[chatID]; scanned {
		return
	}
	c.scannedChats[chatID] = struct{}{}
	c.result.ChatsScanned++
}

func (c *analyticsHistoryCycle) fail(err error) {
	if err != nil {
		c.failures = append(c.failures, err)
	}
}

func (c *analyticsHistoryCycle) finish(fatal error) (usecase.AnalyticsHistoryResult, error) {
	for _, failure := range c.failures {
		c.result.Errors = append(c.result.Errors, failure.Error())
	}
	return c.result, fatal
}

// SyncHistory remains the compatibility surface for one-shot analysis.
func (a *UpdateActivator) SyncHistory(ctx context.Context) error {
	_, err := a.SyncAnalyticsHistory(ctx)
	return err
}

// SyncAnalyticsHistory imports each connected scout account's incremental
// chat history. Collection deliberately bypasses outbound triggers.
func (a *UpdateActivator) SyncAnalyticsHistory(ctx context.Context) (usecase.AnalyticsHistoryResult, error) {
	cycle := newAnalyticsHistoryCycle()
	if a == nil || a.catalogs == nil || a.collector == nil || a.now == nil {
		return cycle.finish(errors.New("history synchronization is not configured"))
	}
	collector, ok := a.collector.(HistoryCollector)
	if !ok {
		return cycle.finish(errors.New("history collector is not configured"))
	}
	a.mu.Lock()
	historyStore := a.historyStore
	a.mu.Unlock()
	if historyStore == nil {
		return cycle.finish(errors.New("analytics history cursor store is not configured"))
	}
	scouts := a.connectedScouts()
	if len(scouts) == 0 {
		return cycle.finish(errors.New("scout account is not connected"))
	}
	rows, err := a.catalogs.List(ctx, domain.SourceCatalogScout)
	if err != nil {
		return cycle.finish(err)
	}
	for _, scout := range scouts {
		if err := ctx.Err(); err != nil {
			return cycle.finish(err)
		}
		api := scout.client.API()
		if api == nil {
			cycle.fail(fmt.Errorf("account %s: scout client API is not ready", scout.accountID))
			continue
		}
		seen := make(map[string]struct{})
		for _, row := range rows {
			if err := ctx.Err(); err != nil {
				return cycle.finish(err)
			}
			if !row.Active || isDerivedScoutDiscussion(row) {
				continue
			}
			peer, _, resolveErr := resolveTelegramPeer(ctx, api, row.Link, false)
			if resolveErr != nil {
				if err := ctx.Err(); err != nil {
					return cycle.finish(err)
				}
				cycle.fail(fmt.Errorf("account %s source %s: %w", scout.accountID, row.ID, accountRPCError(scout.accountID, resolveErr)))
				continue
			}
			if _, hasDiscussion := derivedScoutDiscussionFor(rows, row.ID); hasDiscussion {
				discussion, _, found, discussionErr := resolveLinkedScoutDiscussion(ctx, api, peer)
				if discussionErr != nil {
					if err := ctx.Err(); err != nil {
						return cycle.finish(err)
					}
					cycle.fail(fmt.Errorf("account %s source %s: %w", scout.accountID, row.ID, accountRPCError(scout.accountID, discussionErr)))
					continue
				}
				if !found {
					cycle.fail(fmt.Errorf("account %s source %s: linked discussion is not available", scout.accountID, row.ID))
					continue
				}
				peer = discussion
			}
			chatID := inputPeerIdentity(peer)
			if chatID == "" {
				cycle.fail(fmt.Errorf("account %s source %s: scout source peer is not supported", scout.accountID, row.ID))
				continue
			}
			if _, duplicate := seen[chatID]; duplicate {
				continue
			}
			seen[chatID] = struct{}{}
			cycle.scanChat(chatID)
			messages, syncErr := syncScoutHistoryPeer(ctx, api, collector, historyStore, scout.accountID, peer, a.now().UTC())
			cycle.result.MessagesScanned += messages
			if syncErr != nil {
				if err := ctx.Err(); err != nil {
					return cycle.finish(err)
				}
				cycle.fail(fmt.Errorf("account %s chat %s: %w", scout.accountID, chatID, accountRPCError(scout.accountID, syncErr)))
			}
		}
	}
	return cycle.finish(nil)
}

func (a *UpdateActivator) connectedScout() (domain.ID, TelegramClient, error) {
	scouts := a.connectedScouts()
	if len(scouts) > 0 {
		return scouts[0].accountID, scouts[0].client, nil
	}
	return "", nil, errors.New("scout account is not connected")
}

type connectedScoutClient struct {
	accountID domain.ID
	client    TelegramClient
}

func (a *UpdateActivator) connectedScouts() []connectedScoutClient {
	a.mu.Lock()
	defer a.mu.Unlock()
	result := make([]connectedScoutClient, 0, len(a.scouts))
	for accountID, client := range a.scouts {
		if client != nil {
			result = append(result, connectedScoutClient{accountID: accountID, client: client})
		}
	}
	sort.Slice(result, func(left, right int) bool { return result[left].accountID < result[right].accountID })
	return result
}

func (a *UpdateActivator) unregisterScoutOnDisconnect(ctx context.Context, accountID domain.ID, epoch uint64) {
	<-ctx.Done()
	a.mu.Lock()
	if a.scoutEpoch[accountID] == epoch {
		delete(a.scouts, accountID)
	}
	a.mu.Unlock()
}

type scoutHistoryPager interface {
	MessagesGetHistory(context.Context, *tg.MessagesGetHistoryRequest) (tg.MessagesMessagesClass, error)
}

func syncScoutHistoryPeer(ctx context.Context, api scoutHistoryPager, collector HistoryCollector, cursors domain.AnalyticsSchedulerStore, accountID domain.ID, peer tg.InputPeerClass, now time.Time) (int64, error) {
	chatID := inputPeerIdentity(peer)
	if chatID == "" {
		return 0, errors.New("telegram history peer is not supported")
	}
	cursor, err := cursors.ScoutCursor(ctx, accountID, chatID)
	if err != nil {
		return 0, err
	}
	firstScan := cursor.MessageID == 0
	cutoff := now.UTC().Add(-scoutHistoryRetention)
	offsetID := 0
	maxSeenID := cursor.MessageID
	var collected int64
	for {
		if err := ctx.Err(); err != nil {
			return collected, err
		}
		page, err := api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
			Peer: peer, OffsetID: offsetID, MinID: int(cursor.MessageID), Limit: scoutHistoryPageSize,
		})
		if err != nil {
			return collected, err
		}
		messages := scoutHistoryMessages(page)
		if len(messages) == 0 {
			return collected, saveScoutHistoryCursor(ctx, cursors, accountID, chatID, cursor.MessageID, maxSeenID)
		}
		nextOffsetID := offsetID
		reachedCutoff := false
		for _, raw := range messages {
			message, ok := raw.(*tg.Message)
			if !ok || message.ID <= 0 {
				continue
			}
			if int64(message.ID) > maxSeenID {
				maxSeenID = int64(message.ID)
			}
			if nextOffsetID == 0 || message.ID < nextOffsetID {
				nextOffsetID = message.ID
			}
			messageAt := time.Unix(int64(message.Date), 0).UTC()
			if firstScan && messageAt.Before(cutoff) {
				reachedCutoff = true
				continue
			}
			if int64(message.ID) <= cursor.MessageID {
				continue
			}
			if strings.TrimSpace(message.Message) == "" {
				continue
			}
			if err := collector.CollectHistory(ctx, scouting.IncomingUpdate{
				ChatID: chatID, MessageID: int64(message.ID), Text: message.Message, MessageAt: messageAt,
			}); err != nil {
				return collected, err
			}
			collected++
		}
		if reachedCutoff || len(messages) < scoutHistoryPageSize {
			return collected, saveScoutHistoryCursor(ctx, cursors, accountID, chatID, cursor.MessageID, maxSeenID)
		}
		if nextOffsetID == 0 || (offsetID != 0 && nextOffsetID >= offsetID) {
			return collected, errors.New("telegram history pagination did not advance")
		}
		offsetID = nextOffsetID
		if err := waitForScoutHistoryPage(ctx); err != nil {
			return collected, err
		}
	}
}

func saveScoutHistoryCursor(ctx context.Context, store domain.AnalyticsSchedulerStore, accountID domain.ID, chatID string, previousID, nextID int64) error {
	if nextID <= previousID {
		return nil
	}
	return store.SaveScoutCursor(ctx, domain.AnalyticsScoutCursor{AccountID: accountID, ChatID: chatID, MessageID: nextID})
}

func scoutHistoryMessages(page tg.MessagesMessagesClass) []tg.MessageClass {
	switch value := page.(type) {
	case *tg.MessagesMessages:
		return value.Messages
	case *tg.MessagesMessagesSlice:
		return value.Messages
	case *tg.MessagesChannelMessages:
		return value.Messages
	default:
		return nil
	}
}

func waitForScoutHistoryPage(ctx context.Context) error {
	timer := time.NewTimer(scoutHistoryPageDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func isDerivedScoutDiscussion(row domain.Channel) bool {
	return strings.HasPrefix(string(row.ID), "discussion:")
}

func derivedScoutDiscussionFor(rows []domain.Channel, sourceID domain.ID) (domain.Channel, bool) {
	prefix := "discussion:" + string(sourceID) + ":"
	for _, row := range rows {
		if strings.HasPrefix(string(row.ID), prefix) {
			return row, true
		}
	}
	return domain.Channel{}, false
}

// synchronizeDerivedScoutDiscussion keeps the derived discussion's persisted
// collection metrics intact while mirroring the source's activation state.
func synchronizeDerivedScoutDiscussion(discussion, source domain.Channel, now time.Time) (domain.Channel, bool) {
	changed := false
	if discussion.Topic != source.Topic {
		discussion.Topic = source.Topic
		changed = true
	}
	if discussion.Active != source.Active {
		discussion.Active = source.Active
		changed = true
	}
	if !source.Active && discussion.Status != domain.ChannelPaused {
		discussion.Status = domain.ChannelPaused
		changed = true
	}
	if source.Active && discussion.Status == domain.ChannelPaused {
		discussion.Status = domain.ChannelReady
		changed = true
	}
	if changed {
		discussion.UpdatedAt = now
	}
	return discussion, changed
}

func (a *UpdateActivator) derivedScoutDiscussionMember(ctx context.Context, accountID domain.ID, discussion *domain.Channel) (bool, error) {
	if discussion == nil || !discussion.Active || discussion.TelegramID == "" {
		return false, nil
	}
	if a.memberships == nil {
		return true, nil
	}
	membership, found, err := a.memberships.LoadMembership(ctx, accountID, domain.SourceCatalogScout, discussion.ID)
	if err != nil {
		return false, err
	}
	return found && membership.IsMember && membership.Status == "member", nil
}

func derivedScoutDiscussion(source domain.Channel, peer tg.InputPeerClass, title string, now time.Time) domain.Channel {
	telegramID := inputPeerID(peer)
	if title == "" {
		title = source.Title
	}
	return domain.Channel{
		ID:         domain.ID("discussion:" + string(source.ID) + ":" + telegramID),
		TelegramID: telegramID,
		Title:      title,
		Link:       source.Link + "#tc-discussion=" + telegramID,
		Topic:      source.Topic,
		Status:     domain.ChannelReady,
		Active:     source.Active,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
}

func refreshDerivedScoutDiscussion(existing, source domain.Channel, peer tg.InputPeerClass, title string, now time.Time) domain.Channel {
	if title == "" {
		title = source.Title
	}
	existing.TelegramID = inputPeerID(peer)
	existing.Title = title
	existing.Link = source.Link + "#tc-discussion=" + existing.TelegramID
	existing.Topic = source.Topic
	existing.Active = source.Active
	if source.Active {
		existing.Status = domain.ChannelReady
		existing.LastError = ""
	} else {
		existing.Status = domain.ChannelPaused
	}
	existing.UpdatedAt = now
	return existing
}

func markScoutDiscussionError(channel domain.Channel, cause error, now time.Time) domain.Channel {
	channel.Status = domain.ChannelError
	var flood *FloodWaitError
	if errors.As(accountRPCError("", cause), &flood) {
		channel.Status = domain.ChannelFloodWait
	}
	channel.LastError = cause.Error()
	channel.UpdatedAt = now
	return channel
}

// resolveLinkedScoutDiscussion converts a broadcast-channel source into its
// associated discussion group when one is configured in Telegram. The group
// is where channel comments are delivered as normal messages.
func resolveLinkedScoutDiscussion(ctx context.Context, api *tg.Client, peer tg.InputPeerClass) (tg.InputPeerClass, string, bool, error) {
	channel, ok := peer.(*tg.InputPeerChannel)
	if !ok {
		return nil, "", false, nil
	}
	full, err := api.ChannelsGetFullChannel(ctx, &tg.InputChannel{ChannelID: channel.ChannelID, AccessHash: channel.AccessHash})
	if err != nil {
		return nil, "", false, err
	}
	channelFull, ok := full.FullChat.(*tg.ChannelFull)
	if !ok {
		return nil, "", false, nil
	}
	linkedID, ok := channelFull.GetLinkedChatID()
	if !ok || linkedID == 0 {
		return nil, "", false, nil
	}
	for _, chat := range full.Chats {
		linked, ok := chat.(*tg.Channel)
		if !ok || linked.ID != linkedID {
			continue
		}
		if _, err := api.ChannelsJoinChannel(ctx, &tg.InputChannel{ChannelID: linked.ID, AccessHash: linked.AccessHash}); err != nil && !tgerr.Is(err, "USER_ALREADY_PARTICIPANT") {
			return nil, "", false, err
		}
		return linked.AsInputPeer(), linked.Title, true, nil
	}
	return nil, "", false, errors.New("linked Telegram discussion was not returned")
}

func (a *UpdateActivator) copyResolvedPeerToOutbound(ctx context.Context, row domain.Channel) error {
	outboundRows, err := a.catalogs.List(ctx, domain.SourceCatalogOutbound)
	if err != nil {
		return err
	}
	for _, outbound := range outboundRows {
		if outbound.Link != row.Link {
			continue
		}
		outbound.TelegramID = row.TelegramID
		outbound.Title = row.Title
		outbound.UpdatedAt = row.UpdatedAt
		if err := a.catalogs.Save(ctx, domain.SourceCatalogOutbound, outbound); err != nil {
			return err
		}
	}
	return nil
}

func (a *UpdateActivator) Disable(client TelegramClient, accountID domain.ID) {
	if setter, ok := client.(rawUpdateHandlerSetter); ok {
		setter.SetUpdateHandler(nil)
	}
	a.mu.Lock()
	delete(a.routers, accountID)
	delete(a.setters, accountID)
	delete(a.scouts, accountID)
	a.mu.Unlock()
}

func (a *UpdateActivator) ResetExplicitLifecycle() {
	a.mu.Lock()
	setters := make([]rawUpdateHandlerSetter, 0, len(a.setters))
	for _, setter := range a.setters {
		setters = append(setters, setter)
	}
	a.routers = make(map[domain.ID]*scoutUpdateRouter)
	a.setters = make(map[domain.ID]rawUpdateHandlerSetter)
	a.scouts = make(map[domain.ID]TelegramClient)
	a.mu.Unlock()
	for _, setter := range setters {
		setter.SetUpdateHandler(nil)
	}
}

func inputPeerID(peer tg.InputPeerClass) string {
	switch value := peer.(type) {
	case *tg.InputPeerChannel:
		return strconv.FormatInt(value.ChannelID, 10)
	case *tg.InputPeerChat:
		return strconv.FormatInt(value.ChatID, 10)
	case *tg.InputPeerUser:
		return strconv.FormatInt(value.UserID, 10)
	default:
		return ""
	}
}

func inputPeerIdentity(peer tg.InputPeerClass) string {
	switch value := peer.(type) {
	case *tg.InputPeerChannel:
		return telegramPeerIdentity("channel", value.ChannelID)
	case *tg.InputPeerChat:
		return telegramPeerIdentity("chat", value.ChatID)
	case *tg.InputPeerUser:
		return telegramPeerIdentity("user", value.UserID)
	default:
		return ""
	}
}

func telegramPeerIdentity(namespace string, id int64) string {
	return namespace + ":" + strconv.FormatInt(id, 10)
}

type scoutUpdateRouter struct {
	collector      UpdateCollector
	allowed        map[string]struct{}
	activationUnix int64
	mu             sync.Mutex
	seen           map[string]map[int64]struct{}
	references     *SenderReferences
	managed        *managedSenderRegistry
	collect        bool
	allowedMu      sync.RWMutex
}

func (r *scoutUpdateRouter) setAllowed(allowed map[string]struct{}) {
	r.allowedMu.Lock()
	r.allowed = allowed
	r.allowedMu.Unlock()
}

func (r *scoutUpdateRouter) allows(chatID string) bool {
	r.allowedMu.RLock()
	_, ok := r.allowed[chatID]
	r.allowedMu.RUnlock()
	return ok
}

func newScoutUpdateRouter(collector UpdateCollector, allowed map[string]struct{}, activationUnix int64, collect bool, references ...*SenderReferences) *scoutUpdateRouter {
	var senderReferences *SenderReferences
	if len(references) > 0 {
		senderReferences = references[0]
	}
	return &scoutUpdateRouter{collector: collector, allowed: allowed, activationUnix: activationUnix, collect: collect, seen: make(map[string]map[int64]struct{}), references: senderReferences}
}

func (r *scoutUpdateRouter) dispatcher() telegram.UpdateHandler {
	d := tg.NewUpdateDispatcher()
	d.OnNewMessage(func(ctx context.Context, entities tg.Entities, update *tg.UpdateNewMessage) error {
		return r.message(ctx, entities, update.Message, false)
	})
	d.OnNewChannelMessage(func(ctx context.Context, entities tg.Entities, update *tg.UpdateNewChannelMessage) error {
		return r.message(ctx, entities, update.Message, false)
	})
	d.OnEditMessage(func(ctx context.Context, entities tg.Entities, update *tg.UpdateEditMessage) error {
		return r.message(ctx, entities, update.Message, true)
	})
	d.OnEditChannelMessage(func(ctx context.Context, entities tg.Entities, update *tg.UpdateEditChannelMessage) error {
		return r.message(ctx, entities, update.Message, true)
	})
	d.OnDeleteChannelMessages(func(ctx context.Context, _ tg.Entities, update *tg.UpdateDeleteChannelMessages) error {
		chatID := strconv.FormatInt(update.ChannelID, 10)
		return r.delete(ctx, chatID, telegramPeerIdentity("channel", update.ChannelID), update.Messages)
	})
	d.OnDeleteMessages(func(ctx context.Context, _ tg.Entities, update *tg.UpdateDeleteMessages) error {
		return r.deleteSeen(ctx, update.Messages)
	})
	return d
}

func (r *scoutUpdateRouter) message(ctx context.Context, entities tg.Entities, class tg.MessageClass, edited bool) error {
	message, ok := class.(*tg.Message)
	if !ok {
		return nil
	}
	managedSender := message.Out || r.managed.Contains(messageSenderUserID(message))
	telegramID := peerID(message.PeerID)
	messageChatID := peerIdentity(message.PeerID)
	if telegramID == "" || messageChatID == "" || !r.allows(telegramID) {
		return nil
	}
	if !r.collect {
		if edited || int64(message.Date) < r.activationUnix {
			return nil
		}
		if managedSender {
			return nil
		}
		return r.collector.(UpdateTrigger).Trigger(ctx, scouting.IncomingUpdate{
			ChatID: telegramID, MessageID: int64(message.ID), Text: message.Message,
			MessageAt: time.Unix(int64(message.Date), 0).UTC(), SenderID: r.senderReference(entities, message),
		})
	}
	if edited {
		exists, err := r.collector.Exists(ctx, messageChatID, int64(message.ID))
		if err != nil || !exists {
			return err
		}
	} else if int64(message.Date) < r.activationUnix {
		return nil
	}
	messageAt := time.Unix(int64(message.Date), 0).UTC()
	var editedAt *time.Time
	if edited {
		value := time.Unix(int64(message.EditDate), 0).UTC()
		editedAt = &value
	}
	update := scouting.IncomingUpdate{
		ChatID: messageChatID, MessageID: int64(message.ID), Text: message.Message,
		MessageAt: messageAt, EditedAt: editedAt, SenderID: r.senderReference(entities, message),
	}
	if err := r.ingest(ctx, telegramID, update, !managedSender); err != nil {
		return err
	}
	r.mu.Lock()
	if r.seen[messageChatID] == nil {
		r.seen[messageChatID] = make(map[int64]struct{})
	}
	r.seen[messageChatID][int64(message.ID)] = struct{}{}
	r.mu.Unlock()
	return nil
}

func (r *scoutUpdateRouter) ingest(ctx context.Context, telegramID string, update scouting.IncomingUpdate, trigger bool) error {
	collector, ok := r.collector.(historyTriggerCollector)
	if !ok {
		if !trigger {
			if history, ok := r.collector.(HistoryCollector); ok {
				return history.CollectHistory(ctx, update)
			}
			return nil
		}
		return r.collector.Ingest(ctx, update)
	}
	if err := collector.CollectHistory(ctx, update); err != nil {
		return err
	}
	if !trigger {
		return nil
	}
	update.ChatID = telegramID
	return collector.Trigger(ctx, update)
}

func messageSenderUserID(message *tg.Message) int64 {
	if message == nil {
		return 0
	}
	from, ok := message.GetFromID()
	if !ok {
		return 0
	}
	user, ok := from.(*tg.PeerUser)
	if !ok {
		return 0
	}
	return user.UserID
}

func (r *scoutUpdateRouter) senderReference(entities tg.Entities, message *tg.Message) string {
	if r.references == nil {
		return ""
	}
	from, ok := message.GetFromID()
	if !ok {
		return ""
	}
	peer, ok := from.(*tg.PeerUser)
	if !ok {
		return ""
	}
	user := entities.Users[peer.UserID]
	if user == nil {
		return ""
	}
	return r.references.Store(TelegramPeerDescriptor{UserID: user.ID, AccessHash: user.AccessHash, Username: user.Username})
}

func (r *scoutUpdateRouter) delete(ctx context.Context, telegramID, messageChatID string, ids []int) error {
	if !r.collect || !r.allows(telegramID) {
		return nil
	}
	for _, id := range ids {
		messageID := int64(id)
		seen := r.consumeSeen(messageChatID, messageID)
		if !seen {
			exists, err := r.collector.Exists(ctx, messageChatID, messageID)
			if err != nil {
				return err
			}
			if !exists {
				continue
			}
		}
		if err := r.collector.Delete(ctx, messageChatID, messageID); err != nil {
			return err
		}
	}
	return nil
}

func (r *scoutUpdateRouter) deleteSeen(ctx context.Context, ids []int) error {
	r.allowedMu.RLock()
	chatIDs := make([]string, 0, len(r.allowed))
	for chatID := range r.allowed {
		chatIDs = append(chatIDs, chatID)
	}
	r.allowedMu.RUnlock()
	for _, telegramID := range chatIDs {
		for _, namespace := range []string{"chat", "user"} {
			messageChatID := namespace + ":" + telegramID
			if err := r.delete(ctx, telegramID, messageChatID, ids); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *scoutUpdateRouter) consumeSeen(chatID string, messageID int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.seen[chatID][messageID]; !ok {
		return false
	}
	delete(r.seen[chatID], messageID)
	return true
}

func peerID(peer tg.PeerClass) string {
	switch value := peer.(type) {
	case *tg.PeerChannel:
		return strconv.FormatInt(value.ChannelID, 10)
	case *tg.PeerChat:
		return strconv.FormatInt(value.ChatID, 10)
	case *tg.PeerUser:
		return strconv.FormatInt(value.UserID, 10)
	default:
		return ""
	}
}

func peerIdentity(peer tg.PeerClass) string {
	switch value := peer.(type) {
	case *tg.PeerChannel:
		return telegramPeerIdentity("channel", value.ChannelID)
	case *tg.PeerChat:
		return telegramPeerIdentity("chat", value.ChatID)
	case *tg.PeerUser:
		return telegramPeerIdentity("user", value.UserID)
	default:
		return ""
	}
}

var _ ClientActivator = (*UpdateActivator)(nil)
