package fake

import (
	"context"
	"crypto/sha1"
	"fmt"
	"strings"
	"time"

	"telegram-companion/internal/domain"
)

type Gateway struct{}

func NewGateway() *Gateway { return &Gateway{} }

func (g *Gateway) ResolveChannel(_ context.Context, link string) (domain.Channel, error) {
	normalized := strings.TrimSpace(link)
	title := strings.TrimPrefix(normalized, "https://t.me/")
	title = strings.TrimPrefix(title, "@")
	now := time.Now().UTC()
	return domain.Channel{
		ID:        domain.ID(stableUUID(normalized)),
		Title:     title,
		Link:      normalized,
		Username:  title,
		Type:      domain.ChannelTypeChannel,
		Status:    domain.ChannelReady,
		Active:    true,
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

func (g *Gateway) CheckMembership(_ context.Context, account domain.Account, channel domain.Channel) (domain.ChannelMembership, error) {
	now := time.Now().UTC()
	return domain.ChannelMembership{AccountID: account.ID, ChannelID: channel.ID, IsMember: false, Status: "not_member", LastCheckAt: &now}, nil
}

func (g *Gateway) JoinChannel(_ context.Context, account domain.Account, channel domain.Channel) (domain.ChannelMembership, error) {
	now := time.Now().UTC()
	return domain.ChannelMembership{AccountID: account.ID, ChannelID: channel.ID, IsMember: true, Status: "member", LastCheckAt: &now}, nil
}

func (g *Gateway) SendPublicReply(context.Context, domain.Account, domain.OutgoingMessageJob) error {
	return nil
}

func (g *Gateway) SendPrivateMessage(context.Context, domain.Account, domain.OutgoingMessageJob) error {
	return nil
}

func stableUUID(value string) string {
	sum := sha1.Sum([]byte(value))
	return fmt.Sprintf("%x-%x-%x-%x-%x", sum[0:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
}
