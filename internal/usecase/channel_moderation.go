package usecase

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"telegram-companion/internal/domain"
)

type ChannelModerationStore interface {
	ListAccounts(context.Context) ([]domain.Account, error)
	ListCatalog(context.Context, domain.SourceCatalog) ([]domain.Channel, error)
	ListMemberships(context.Context, domain.SourceCatalog, domain.ID) ([]domain.ChannelMembership, error)
}

type ChannelModerationAccount struct {
	AccountKey         domain.ID
	Title              string
	Role               domain.AccountRole
	RequestSubmittedAt time.Time
	JoinedAt           *time.Time
	Duration           time.Duration
	Status             string
}

type ChannelModerationChannel struct {
	Catalog        domain.SourceCatalog
	ChannelID      domain.ID
	Title          string
	Link           string
	Topic          string
	Applications   int
	Joined         int
	Pending        int
	Status         string
	FirstRequestAt time.Time
	Duration       time.Duration
	Accounts       []ChannelModerationAccount
}

type ChannelModerationService struct {
	store ChannelModerationStore
}

func NewChannelModerationService(store ChannelModerationStore) *ChannelModerationService {
	return &ChannelModerationService{store: store}
}

func (s *ChannelModerationService) List(ctx context.Context, now time.Time) ([]ChannelModerationChannel, error) {
	accounts, err := s.store.ListAccounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("list accounts: %w", err)
	}
	accountsByID := make(map[domain.ID]domain.Account, len(accounts))
	for _, account := range accounts {
		accountsByID[account.ID] = account
	}

	rows := make([]ChannelModerationChannel, 0)
	for _, catalog := range []domain.SourceCatalog{domain.SourceCatalogOutbound, domain.SourceCatalogScout} {
		channels, err := s.store.ListCatalog(ctx, catalog)
		if err != nil {
			return nil, fmt.Errorf("list %s catalog: %w", catalog, err)
		}
		for _, channel := range channels {
			memberships, err := s.store.ListMemberships(ctx, catalog, channel.ID)
			if err != nil {
				return nil, fmt.Errorf("list memberships for %s channel %s: %w", catalog, channel.ID, err)
			}
			row := aggregateChannelModeration(catalog, channel, memberships, accountsByID, now)
			if len(row.Accounts) > 0 {
				rows = append(rows, row)
			}
		}
	}

	sort.Slice(rows, func(i, j int) bool {
		if !rows[i].FirstRequestAt.Equal(rows[j].FirstRequestAt) {
			return rows[i].FirstRequestAt.After(rows[j].FirstRequestAt)
		}
		if rows[i].Title != rows[j].Title {
			return rows[i].Title < rows[j].Title
		}
		if rows[i].Catalog != rows[j].Catalog {
			return rows[i].Catalog < rows[j].Catalog
		}
		return rows[i].ChannelID < rows[j].ChannelID
	})
	return rows, nil
}

func aggregateChannelModeration(catalog domain.SourceCatalog, channel domain.Channel, memberships []domain.ChannelMembership, accounts map[domain.ID]domain.Account, now time.Time) ChannelModerationChannel {
	row := ChannelModerationChannel{
		Catalog:   catalog,
		ChannelID: channel.ID,
		Title:     channel.Title,
		Link:      channel.Link,
		Topic:     channel.Topic,
		Accounts:  make([]ChannelModerationAccount, 0, len(memberships)),
	}
	var latestJoinedAt time.Time

	for _, membership := range memberships {
		isMember := membership.IsMember || membership.Status == "member"
		if membership.RequestSubmittedAt == nil {
			account := accounts[membership.AccountID]
			if isMember && membership.JoinedAt != nil {
				row.Joined++
				if latestJoinedAt.IsZero() || membership.JoinedAt.After(latestJoinedAt) {
					latestJoinedAt = *membership.JoinedAt
				}
				row.Accounts = append(row.Accounts, ChannelModerationAccount{
					AccountKey: membership.AccountID,
					Title:      moderationAccountTitle(account),
					Role:       account.Role,
					JoinedAt:   membership.JoinedAt,
					Status:     membership.Status,
				})
				continue
			}
			if membership.Status != "joining" {
				continue
			}
			row.Pending++
			row.Accounts = append(row.Accounts, ChannelModerationAccount{
				AccountKey: membership.AccountID,
				Title:      moderationAccountTitle(account),
				Role:       account.Role,
				Status:     membership.Status,
			})
			continue
		}
		requestSubmittedAt := *membership.RequestSubmittedAt
		if row.FirstRequestAt.IsZero() || requestSubmittedAt.Before(row.FirstRequestAt) {
			row.FirstRequestAt = requestSubmittedAt
		}

		account := accounts[membership.AccountID]
		row.Applications++
		if isMember {
			row.Joined++
			if membership.JoinedAt != nil && (latestJoinedAt.IsZero() || membership.JoinedAt.After(latestJoinedAt)) {
				latestJoinedAt = *membership.JoinedAt
			}
		} else {
			row.Pending++
		}
		finishedAt := (*time.Time)(nil)
		if isMember {
			finishedAt = membership.JoinedAt
		}
		row.Accounts = append(row.Accounts, ChannelModerationAccount{
			AccountKey:         membership.AccountID,
			Title:              moderationAccountTitle(account),
			Role:               account.Role,
			RequestSubmittedAt: requestSubmittedAt,
			JoinedAt:           membership.JoinedAt,
			Duration:           moderationDuration(requestSubmittedAt, finishedAt, now),
			Status:             membership.Status,
		})
	}

	switch {
	case row.Joined > 0 && row.Pending == 0:
		row.Status = "member"
	case row.Joined > 0:
		row.Status = "partial"
	case row.Applications == 0 && row.Pending > 0:
		row.Status = "joining"
	default:
		row.Status = "pending_approval"
	}
	end := now
	if row.Pending == 0 && !latestJoinedAt.IsZero() {
		end = latestJoinedAt
	}
	if !row.FirstRequestAt.IsZero() {
		row.Duration = moderationDuration(row.FirstRequestAt, &end, now)
	}

	sort.Slice(row.Accounts, func(i, j int) bool {
		if rowsHaveDifferentSubmissionState(row.Accounts[i], row.Accounts[j]) {
			return !row.Accounts[i].RequestSubmittedAt.IsZero()
		}
		if !row.Accounts[i].RequestSubmittedAt.Equal(row.Accounts[j].RequestSubmittedAt) {
			return row.Accounts[i].RequestSubmittedAt.Before(row.Accounts[j].RequestSubmittedAt)
		}
		return row.Accounts[i].AccountKey < row.Accounts[j].AccountKey
	})
	return row
}

func rowsHaveDifferentSubmissionState(left, right ChannelModerationAccount) bool {
	return left.RequestSubmittedAt.IsZero() != right.RequestSubmittedAt.IsZero()
}

func moderationAccountTitle(account domain.Account) string {
	if title := strings.TrimSpace(account.DisplayName); title != "" {
		return title
	}
	if title := strings.TrimSpace(account.PhoneMasked); title != "" {
		return title
	}
	return "Telegram account"
}

func moderationDuration(start time.Time, finishedAt *time.Time, now time.Time) time.Duration {
	end := now
	if finishedAt != nil {
		end = *finishedAt
	}
	if end.Before(start) {
		return 0
	}
	return end.Sub(start)
}
