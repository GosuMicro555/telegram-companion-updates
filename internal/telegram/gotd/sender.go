package gotd

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"telegram-companion/internal/domain"
	"telegram-companion/internal/usecase/runtimeconfig"
)

type accountMessageSender interface {
	SendPublicReply(context.Context, domain.Account, domain.OutgoingMessageJob) error
	SendPrivateMessage(context.Context, domain.Account, domain.OutgoingMessageJob) error
	ResolveUsername(context.Context, domain.Account, string) error
	SendScheduledPrivateMessage(context.Context, domain.Account, domain.ScheduledDMDelivery, string) error
}

type accountMembershipCatalog interface {
	LoadMembership(context.Context, domain.ID, domain.SourceCatalog, domain.ID) (domain.ChannelMembership, bool, error)
	SaveMembership(context.Context, domain.SourceCatalog, domain.ChannelMembership) error
}

type ClientSenderRegistry struct {
	mu          sync.RWMutex
	senders     map[domain.ID]accountMessageSender
	catalogs    domain.CatalogRepository
	references  *SenderReferences
	memberships accountMembershipCatalog
}

func NewClientSenderRegistry(catalogs domain.CatalogRepository, references ...*SenderReferences) *ClientSenderRegistry {
	var senderReferences *SenderReferences
	if len(references) > 0 {
		senderReferences = references[0]
	}
	memberships, _ := catalogs.(accountMembershipCatalog)
	return &ClientSenderRegistry{senders: make(map[domain.ID]accountMessageSender), catalogs: catalogs, references: senderReferences, memberships: memberships}
}

func (r *ClientSenderRegistry) Register(ctx context.Context, account domain.Account, client TelegramClient) error {
	if account.Role != domain.AccountRoleSpammer {
		return nil
	}
	if client == nil || client.API() == nil {
		return errors.New("connected gotd API is required")
	}
	sender := &gotdAccountSender{api: client.API(), catalogs: r.catalogs, references: r.references}
	rows, err := r.catalogs.List(ctx, domain.SourceCatalogOutbound)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if !row.Active {
			continue
		}
		var peer tg.InputPeerClass
		var title string
		membership, found, membershipErr := loadAccountMembershipIntent(ctx, r.memberships, account.ID, domain.SourceCatalogOutbound, row)
		if membershipErr != nil {
			return membershipErr
		}
		if !found {
			continue
		}
		transition, transitionErr := reconcileMembershipIntent(ctx, client.API(), row, membership, time.Now().UTC())
		if transitionErr != nil {
			return accountRPCError(account.ID, transitionErr)
		}
		membership = transition.membership
		if transition.changed {
			if err := r.memberships.SaveMembership(ctx, domain.SourceCatalogOutbound, membership); err != nil {
				return err
			}
		}
		if !membership.IsMember || membership.Status != membershipMember {
			continue
		}
		peer, title = transition.peer, transition.title
		if peer == nil {
			peer, title, err = resolveTelegramPeer(ctx, client.API(), row.Link, false)
		}
		if err != nil {
			return accountRPCError(account.ID, err)
		}
		peer, title, err = resolveExplicitDiscussionTarget(ctx, client.API(), row.Link, peer, title)
		if err != nil {
			return accountRPCError(account.ID, err)
		}
		row.TelegramID = inputPeerID(peer)
		if title != "" {
			row.Title = title
		}
		row.Status = domain.ChannelReady
		if err := r.catalogs.Save(ctx, domain.SourceCatalogOutbound, row); err != nil {
			return err
		}
	}
	r.mu.Lock()
	r.senders[account.ID] = sender
	r.mu.Unlock()
	return nil
}

func isPrivateInviteLink(link string) bool {
	_, ok := privateInviteHash(link)
	return ok
}

func telegramJoinLink(link string) string {
	parsed, err := url.Parse(strings.TrimSpace(link))
	if err != nil {
		return strings.TrimSpace(link)
	}
	parsed.Fragment = ""
	return parsed.String()
}

func explicitDiscussionTargetID(link string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(link))
	if err != nil {
		return "", false
	}
	const prefix = "tc-discussion="
	if !strings.HasPrefix(parsed.Fragment, prefix) {
		return "", false
	}
	targetID := strings.TrimPrefix(parsed.Fragment, prefix)
	if _, err := strconv.ParseInt(targetID, 10, 64); err != nil || targetID == "" {
		return "", false
	}
	return targetID, true
}

func (r *ClientSenderRegistry) Unregister(accountID domain.ID) {
	r.mu.Lock()
	delete(r.senders, accountID)
	r.mu.Unlock()
}

func (r *ClientSenderRegistry) Available(accountID domain.ID) bool {
	r.mu.RLock()
	_, ok := r.senders[accountID]
	r.mu.RUnlock()
	return ok
}

func (r *ClientSenderRegistry) Reset() {
	r.mu.Lock()
	r.senders = make(map[domain.ID]accountMessageSender)
	r.mu.Unlock()
}

func (r *ClientSenderRegistry) SendPublicReply(ctx context.Context, account domain.Account, job domain.OutgoingMessageJob) error {
	sender, err := r.sender(account.ID)
	if err != nil {
		return domain.TransientDeliveryFailure(err)
	}
	return sender.SendPublicReply(ctx, account, job)
}

func (r *ClientSenderRegistry) SendPrivateMessage(ctx context.Context, account domain.Account, job domain.OutgoingMessageJob) error {
	sender, err := r.sender(account.ID)
	if err != nil {
		return domain.TransientDeliveryFailure(err)
	}
	return sender.SendPrivateMessage(ctx, account, job)
}

func (r *ClientSenderRegistry) ResolveUsername(ctx context.Context, account domain.Account, username string) error {
	sender, err := r.sender(account.ID)
	if err != nil {
		return domain.TransientDeliveryFailure(err)
	}
	return sender.ResolveUsername(ctx, account, username)
}

func (r *ClientSenderRegistry) SendScheduledPrivateMessage(ctx context.Context, account domain.Account, delivery domain.ScheduledDMDelivery, messageText string) error {
	sender, err := r.sender(account.ID)
	if err != nil {
		return domain.TransientDeliveryFailure(err)
	}
	return sender.SendScheduledPrivateMessage(ctx, account, delivery, messageText)
}

func (r *ClientSenderRegistry) sender(accountID domain.ID) (accountMessageSender, error) {
	r.mu.RLock()
	sender := r.senders[accountID]
	r.mu.RUnlock()
	if sender == nil {
		return nil, errors.New("spammer account is not connected")
	}
	return sender, nil
}

type ProductionActivator struct {
	updates *UpdateActivator
	senders *ClientSenderRegistry
}

func NewProductionActivator(updates *UpdateActivator, senders *ClientSenderRegistry) *ProductionActivator {
	return &ProductionActivator{updates: updates, senders: senders}
}

func (a *ProductionActivator) Apply(ctx context.Context, client TelegramClient, account domain.Account, snapshot runtimeconfig.Snapshot) error {
	if account.Role == domain.AccountRoleSpammer {
		if err := a.updates.Apply(ctx, client, account, snapshot); err != nil {
			return err
		}
		if snapshot.OutboundPaused {
			a.senders.Unregister(account.ID)
			return nil
		}
		if err := a.senders.Register(ctx, account, client); err != nil {
			a.senders.Unregister(account.ID)
			return err
		}
		return nil
	}
	a.senders.Unregister(account.ID)
	return a.updates.Apply(ctx, client, account, snapshot)
}

func (a *ProductionActivator) NextMembershipCheck(ctx context.Context, accountID domain.ID, snapshot runtimeconfig.Snapshot, after time.Time) (*time.Time, error) {
	if a == nil || a.updates == nil {
		return nil, nil
	}
	return a.updates.NextMembershipCheck(ctx, accountID, snapshot, after)
}

func (a *ProductionActivator) Deactivate(accountID domain.ID) {
	a.senders.Unregister(accountID)
}

func (a *ProductionActivator) ResetExplicitLifecycle() {
	a.updates.ResetExplicitLifecycle()
	a.senders.Reset()
}

type gotdAccountSender struct {
	api        *tg.Client
	catalogs   domain.CatalogRepository
	references *SenderReferences

	mu                  sync.RWMutex
	scheduledRecipients map[string]*tg.InputPeerUser
}

func (s *gotdAccountSender) SendPublicReply(ctx context.Context, _ domain.Account, job domain.OutgoingMessageJob) error {
	channels, err := s.catalogs.List(ctx, domain.SourceCatalogOutbound)
	if err != nil {
		return domain.TransientDeliveryFailure(err)
	}
	var channel domain.Channel
	for _, candidate := range channels {
		if candidate.ID == job.ChannelID {
			channel = candidate
			break
		}
	}
	if channel.ID == "" {
		return domain.PermanentDeliveryFailure(errors.New("outbound channel not found"))
	}
	if strings.TrimSpace(channel.Link) == "" {
		return domain.PermanentDeliveryFailure(errors.New("outbound channel link is required"))
	}
	peer, title, err := resolveTelegramPeer(ctx, s.api, channel.Link, false)
	if err != nil {
		return domain.TransientDeliveryFailure(accountRPCError("", err))
	}
	peer, _, err = resolveExplicitDiscussionTarget(ctx, s.api, channel.Link, peer, title)
	if err != nil {
		return domain.TransientDeliveryFailure(accountRPCError("", err))
	}
	replyID, err := strconv.Atoi(job.ReplyToMessageID)
	if err != nil || replyID <= 0 {
		return domain.PermanentDeliveryFailure(errors.New("valid reply message ID is required"))
	}
	_, err = s.api.MessagesSendMessage(ctx, &tg.MessagesSendMessageRequest{
		Peer: peer, ReplyTo: &tg.InputReplyToMessage{ReplyToMsgID: replyID}, Message: job.Text, RandomID: telegramRandomID(job.ID),
	})
	return domain.TransientDeliveryFailure(accountRPCError("", err))
}

func (s *gotdAccountSender) SendPrivateMessage(ctx context.Context, _ domain.Account, job domain.OutgoingMessageJob) error {
	if s.references == nil {
		return domain.PermanentDeliveryFailure(errors.New("private message sender references are not configured"))
	}
	descriptor, ok := s.references.Resolve(job.TargetTelegramID)
	if !ok {
		return domain.PermanentDeliveryFailure(errors.New("private message sender reference is unavailable"))
	}
	peer := &tg.InputPeerUser{UserID: descriptor.UserID, AccessHash: descriptor.AccessHash}
	_, err := s.api.MessagesSendMessage(ctx, &tg.MessagesSendMessageRequest{Peer: peer, Message: job.Text, RandomID: telegramRandomID(job.ID)})
	if err == nil {
		return nil
	}
	err = accountRPCError("", err)
	if isClosedPrivateMessageRPC(err) {
		return domain.PrivateMessageClosed(err)
	}
	return domain.TransientDeliveryFailure(err)
}

func (s *gotdAccountSender) ResolveUsername(ctx context.Context, account domain.Account, username string) error {
	username = normalizedScheduledDMUsername(username)
	if s.api == nil || username == "" {
		return domain.PermanentDeliveryFailure(errors.New("scheduled DM username is required"))
	}
	resolved, err := s.api.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{Username: username})
	if err != nil {
		return classifyScheduledDMResolveError(account.ID, err)
	}
	for _, candidate := range resolved.Users {
		user, ok := candidate.(*tg.User)
		if !ok {
			continue
		}
		peer := user.AsInputPeer()
		s.mu.Lock()
		if s.scheduledRecipients == nil {
			s.scheduledRecipients = make(map[string]*tg.InputPeerUser)
		}
		s.scheduledRecipients[username] = peer
		s.mu.Unlock()
		return nil
	}
	return domain.PermanentDeliveryFailure(errors.New("resolved scheduled DM target is not a user"))
}

func (s *gotdAccountSender) SendScheduledPrivateMessage(ctx context.Context, account domain.Account, delivery domain.ScheduledDMDelivery, messageText string) error {
	if strings.TrimSpace(messageText) == "" {
		return domain.PermanentDeliveryFailure(errors.New("scheduled DM message text is required"))
	}
	if s.api == nil {
		return domain.TransientDeliveryFailure(errors.New("telegram API is not connected"))
	}
	s.mu.RLock()
	peer := s.scheduledRecipients[normalizedScheduledDMUsername(delivery.Recipient)]
	s.mu.RUnlock()
	if peer == nil {
		return domain.PermanentDeliveryFailure(errors.New("scheduled DM recipient was not resolved"))
	}
	_, err := s.api.MessagesSendMessage(ctx, &tg.MessagesSendMessageRequest{Peer: peer, Message: messageText, RandomID: delivery.TelegramRandomID})
	if err == nil {
		return nil
	}
	err = accountRPCError(account.ID, err)
	if isClosedPrivateMessageRPC(err) {
		return domain.PrivateMessageClosed(err)
	}
	return domain.TransientDeliveryFailure(err)
}

func normalizedScheduledDMUsername(username string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(username), "@"))
}

func classifyScheduledDMResolveError(accountID domain.ID, err error) error {
	err = accountRPCError(accountID, err)
	if tgerr.Is(err, "USERNAME_INVALID", "USERNAME_NOT_OCCUPIED") {
		return domain.PermanentDeliveryFailure(err)
	}
	if isClosedPrivateMessageRPC(err) {
		return domain.PrivateMessageClosed(err)
	}
	return domain.TransientDeliveryFailure(err)
}

func isClosedPrivateMessageRPC(err error) bool {
	return tgerr.Is(err,
		tg.ErrUserPrivacyRestricted,
		tg.ErrUserIsBlocked,
		tg.ErrYouBlockedUser,
		"PEER_ID_INVALID",
	)
}

func resolveTelegramPeer(ctx context.Context, api *tg.Client, target string, userOnly bool) (tg.InputPeerClass, string, error) {
	target = telegramJoinLink(target)
	if api == nil || target == "" {
		return nil, "", errors.New("telegram peer target is required")
	}
	if _, private := privateInviteHash(target); private {
		peer, title, already, err := checkPrivateInviteMembership(ctx, api, target)
		if err != nil {
			return nil, "", err
		}
		if already {
			return peer, title, nil
		}
		return nil, "", errors.New("private Telegram invite requires an explicit persisted join operation")
	}
	username := strings.TrimPrefix(target, "@")
	if strings.Contains(username, "/") {
		username = username[strings.LastIndex(username, "/")+1:]
	}
	resolved, err := api.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{Username: username})
	if err != nil {
		return nil, "", err
	}
	if userOnly {
		for _, candidate := range resolved.Users {
			if user, ok := candidate.(*tg.User); ok {
				return user.AsInputPeer(), strings.TrimSpace(user.FirstName + " " + user.LastName), nil
			}
		}
		return nil, "", errors.New("resolved Telegram target is not a user")
	}
	for _, candidate := range resolved.Chats {
		return inputPeerFromChat(candidate)
	}
	return nil, "", errors.New("resolved Telegram target has no chat")
}

func joinTelegramPeer(ctx context.Context, api *tg.Client, target string) (tg.InputPeerClass, string, membershipJoinOutcome, error) {
	target = telegramJoinLink(target)
	hash, ok := privateInviteHash(target)
	if api == nil || !ok {
		return nil, "", membershipAlreadyJoined, errors.New("private Telegram invite is required")
	}
	peer, title, already, err := checkPrivateInviteMembership(ctx, api, target)
	if err != nil {
		return nil, "", membershipAlreadyJoined, err
	}
	if already {
		return peer, title, membershipAlreadyJoined, nil
	}
	joined, err := api.MessagesImportChatInvite(ctx, hash)
	if err != nil {
		return nil, "", membershipAlreadyJoined, err
	}
	joinedOK, ok := joined.(*tg.MessagesChatInviteJoinResultOk)
	if !ok {
		return nil, "", membershipAlreadyJoined, errors.New("telegram invite join requires web confirmation")
	}
	for _, chat := range updateChats(joinedOK.Updates) {
		peer, title, err := inputPeerFromChat(chat)
		return peer, title, membershipNewlyJoined, err
	}
	return nil, "", membershipAlreadyJoined, errors.New("joined Telegram invite returned no chat")
}

func checkPrivateInviteMembership(ctx context.Context, api *tg.Client, target string) (tg.InputPeerClass, string, bool, error) {
	target = telegramJoinLink(target)
	hash, ok := privateInviteHash(target)
	if api == nil || !ok {
		return nil, "", false, errors.New("private Telegram invite is required")
	}
	invite, err := api.MessagesCheckChatInvite(ctx, hash)
	if err != nil {
		return nil, "", false, err
	}
	already, ok := invite.(*tg.ChatInviteAlready)
	if !ok {
		return nil, "", false, nil
	}
	peer, title, err := inputPeerFromChat(already.Chat)
	return peer, title, err == nil, err
}

func privateInviteHash(target string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(target))
	if err != nil {
		return "", false
	}
	if strings.EqualFold(parsed.Scheme, "tg") && strings.EqualFold(parsed.Host, "join") {
		hash := strings.TrimSpace(parsed.Query().Get("invite"))
		return hash, hash != ""
	}
	path := strings.Trim(parsed.Path, "/")
	lowerPath := strings.ToLower(path)
	if strings.HasPrefix(path, "+") {
		hash := strings.TrimPrefix(path, "+")
		return hash, hash != ""
	}
	if strings.HasPrefix(lowerPath, "joinchat/") {
		hash := path[len("joinchat/"):]
		return hash, hash != ""
	}
	return "", false
}

func joinPublicTelegramPeer(ctx context.Context, api *tg.Client, target string) (tg.InputPeerClass, string, membershipJoinOutcome, error) {
	peer, title, err := resolveTelegramPeer(ctx, api, target, false)
	if err != nil {
		return nil, "", membershipAlreadyJoined, err
	}
	channel, ok := peer.(*tg.InputPeerChannel)
	if !ok {
		return nil, "", membershipAlreadyJoined, errors.New("resolved public Telegram target is not a channel")
	}
	_, err = api.ChannelsJoinChannel(ctx, &tg.InputChannel{ChannelID: channel.ChannelID, AccessHash: channel.AccessHash})
	if tgerr.Is(err, "USER_ALREADY_PARTICIPANT") {
		return peer, title, membershipAlreadyJoined, nil
	}
	if err != nil {
		return nil, "", membershipAlreadyJoined, err
	}
	return peer, title, membershipNewlyJoined, nil
}

func updateChats(updates tg.UpdatesClass) []tg.ChatClass {
	switch value := updates.(type) {
	case *tg.Updates:
		return value.Chats
	case *tg.UpdatesCombined:
		return value.Chats
	default:
		return nil
	}
}

func inputPeerFromChat(chat tg.ChatClass) (tg.InputPeerClass, string, error) {
	switch value := chat.(type) {
	case *tg.Channel:
		return value.AsInputPeer(), value.Title, nil
	case *tg.Chat:
		return value.AsInputPeer(), value.Title, nil
	default:
		return nil, "", fmt.Errorf("unsupported Telegram chat %T", chat)
	}
}

func telegramRandomID(jobID domain.ID) int64 {
	sum := sha256.Sum256([]byte("telegram-message\x00" + string(jobID)))
	value := int64(binary.LittleEndian.Uint64(sum[:8]) & ^(uint64(1) << 63))
	if value == 0 {
		return 1
	}
	return value
}

var _ ClientActivator = (*ProductionActivator)(nil)
var _ OutboundSender = (*ClientSenderRegistry)(nil)
