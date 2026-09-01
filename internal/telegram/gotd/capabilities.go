package gotd

import (
	"context"

	"telegram-companion/internal/domain"
)

type UpdateHandler func(context.Context, domain.IncomingMessageEvent) error

type UpdateSource interface {
	RegisterUpdateHandler(context.Context, UpdateHandler) error
}

type CatalogJoiner interface {
	CheckMembership(context.Context, domain.Account, domain.Channel) (domain.ChannelMembership, error)
	JoinChannel(context.Context, domain.Account, domain.Channel) (domain.ChannelMembership, error)
}

type OutboundSender interface {
	SendPublicReply(context.Context, domain.Account, domain.OutgoingMessageJob) error
	SendPrivateMessage(context.Context, domain.Account, domain.OutgoingMessageJob) error
}

type scoutClient interface {
	UpdateSource
	CatalogJoiner
}

type ScoutCapability struct {
	updates  UpdateSource
	catalogs CatalogJoiner
}

func NewScoutCapability(client scoutClient) *ScoutCapability {
	return &ScoutCapability{updates: client, catalogs: client}
}

func (s *ScoutCapability) RegisterUpdateHandler(ctx context.Context, handler UpdateHandler) error {
	return s.updates.RegisterUpdateHandler(ctx, handler)
}

func (s *ScoutCapability) CheckMembership(ctx context.Context, account domain.Account, channel domain.Channel) (domain.ChannelMembership, error) {
	return s.catalogs.CheckMembership(ctx, account, channel)
}

func (s *ScoutCapability) JoinChannel(ctx context.Context, account domain.Account, channel domain.Channel) (domain.ChannelMembership, error) {
	return s.catalogs.JoinChannel(ctx, account, channel)
}

func (s *ScoutCapability) Activate(ctx context.Context, account domain.Account, chats []domain.Channel, handler UpdateHandler) error {
	if err := s.RegisterUpdateHandler(ctx, handler); err != nil {
		return err
	}
	for _, chat := range chats {
		if !chat.Active {
			continue
		}
		membership, err := s.CheckMembership(ctx, account, chat)
		if err != nil {
			return err
		}
		if membership.IsMember {
			continue
		}
		if _, err := s.JoinChannel(ctx, account, chat); err != nil {
			return err
		}
	}
	return nil
}

type spammerClient interface {
	CatalogJoiner
	OutboundSender
}

type SpammerCapability struct {
	catalogs CatalogJoiner
	outbound OutboundSender
}

func NewSpammerCapability(client spammerClient) *SpammerCapability {
	return &SpammerCapability{catalogs: client, outbound: client}
}

func (s *SpammerCapability) CheckMembership(ctx context.Context, account domain.Account, channel domain.Channel) (domain.ChannelMembership, error) {
	return s.catalogs.CheckMembership(ctx, account, channel)
}

func (s *SpammerCapability) JoinChannel(ctx context.Context, account domain.Account, channel domain.Channel) (domain.ChannelMembership, error) {
	return s.catalogs.JoinChannel(ctx, account, channel)
}

func (s *SpammerCapability) SendPublicReply(ctx context.Context, account domain.Account, job domain.OutgoingMessageJob) error {
	return s.outbound.SendPublicReply(ctx, account, job)
}

func (s *SpammerCapability) SendPrivateMessage(ctx context.Context, account domain.Account, job domain.OutgoingMessageJob) error {
	return s.outbound.SendPrivateMessage(ctx, account, job)
}
