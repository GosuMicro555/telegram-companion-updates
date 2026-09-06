package usecase

import (
	"context"
	"strings"

	"telegram-companion/internal/domain"
)

type ChannelImporter struct {
	accounts domain.AccountRepository
	channels domain.ChannelRepository
	telegram domain.TelegramGateway
}

func NewChannelImporter(accounts domain.AccountRepository, channels domain.ChannelRepository, telegram domain.TelegramGateway) *ChannelImporter {
	return &ChannelImporter{accounts: accounts, channels: channels, telegram: telegram}
}

func (i *ChannelImporter) ImportLinks(ctx context.Context, links []string) error {
	accounts, err := i.accounts.ListActive(ctx)
	if err != nil {
		return err
	}
	for _, link := range uniqueLinks(links) {
		channel, err := i.telegram.ResolveChannel(ctx, link)
		if err != nil {
			return err
		}
		if err := i.channels.Save(ctx, channel); err != nil {
			return err
		}
		for _, account := range accounts {
			membership, err := i.telegram.CheckMembership(ctx, account, channel)
			if err != nil {
				return err
			}
			if !membership.IsMember {
				membership, err = i.telegram.JoinChannel(ctx, account, channel)
				if err != nil {
					return err
				}
			}
			if err := i.channels.SaveMembership(ctx, membership); err != nil {
				return err
			}
		}
	}
	return nil
}

func uniqueLinks(links []string) []string {
	seen := make(map[string]struct{}, len(links))
	out := make([]string, 0, len(links))
	for _, link := range links {
		normalized := normalizeLink(link)
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	return out
}

func normalizeLink(link string) string {
	link = strings.TrimSpace(link)
	link = strings.TrimSuffix(link, "/")
	if strings.HasPrefix(link, "@") {
		return "https://t.me/" + strings.TrimPrefix(link, "@")
	}
	return link
}
