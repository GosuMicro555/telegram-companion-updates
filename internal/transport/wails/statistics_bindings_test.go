package wails

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"telegram-companion/internal/domain"
	"telegram-companion/internal/usecase"
)

type replyStatisticsSettingsStub struct {
	*settingsStoreStub
	statistics domain.ReplyStatistics
	from       time.Time
	to         time.Time
}

func (s *replyStatisticsSettingsStub) ReplyStatistics(_ context.Context, from, to time.Time) (domain.ReplyStatistics, error) {
	s.from = from
	s.to = to
	return s.statistics, nil
}

func TestGetReplyStatisticsConvertsDateRangeAndDomainValues(t *testing.T) {
	lastActivity := time.Date(2026, 7, 2, 10, 15, 0, 0, time.UTC)
	store := &replyStatisticsSettingsStub{
		settingsStoreStub: &settingsStoreStub{settings: domain.DefaultKeywordSettings(), appSettings: domain.DefaultAppSettings()},
		statistics: domain.ReplyStatistics{
			Totals:     domain.ReplyStatisticsTotals{Replies: 3, PublicReplies: 2, PrivateMessages: 1, PrivateMessagesClosed: 4, Channels: 2, Accounts: 2, AverageRepliesPerMinute: 0.125},
			TimeSeries: []domain.ReplyStatisticsBucket{{Date: "2026-07-01", Replies: 2}},
			Accounts:   []domain.ReplyStatisticsAccountRow{{ID: "account-a", Title: "Alpha", Replies: 2, PublicReplies: 1, PrivateMessages: 1, PrivateMessagesClosed: 3, LastActivityAt: lastActivity}},
			Channels:   []domain.ReplyStatisticsChannelRow{{ID: "channel-a", Title: "First channel", Replies: 2, LastActivityAt: lastActivity}},
		},
	}
	bindings := NewBindings(usecase.NewAutomationController(nil), store)

	statistics, err := bindings.GetReplyStatistics("2026-07-01", "2026-07-02")

	require.NoError(t, err)
	require.Equal(t, time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), store.from)
	require.Equal(t, time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC), store.to)
	require.Equal(t, int64(3), statistics.Replies)
	require.Equal(t, int64(2), statistics.PublicReplies)
	require.Equal(t, int64(1), statistics.PrivateMessages)
	require.Equal(t, int64(4), statistics.PrivateMessagesClosed)
	require.Equal(t, 2, statistics.Channels)
	require.Equal(t, 2, statistics.Accounts)
	require.Equal(t, 0.125, statistics.AverageRepliesPerMinute)
	require.Equal(t, "2026-07-01", statistics.TimeSeries[0].Date)
	require.Equal(t, int64(1), statistics.AccountRows[0].PublicReplies)
	require.Equal(t, int64(1), statistics.AccountRows[0].PrivateMessages)
	require.Equal(t, int64(3), statistics.AccountRows[0].PrivateMessagesClosed)
	require.Equal(t, "2026-07-02T10:15:00Z", statistics.AccountRows[0].LastActivityAt)
	require.Equal(t, "First channel", statistics.ChannelRows[0].Title)
}

func TestGetReplyStatisticsRejectsSettingsWithoutAStatisticsProvider(t *testing.T) {
	bindings := NewBindings(usecase.NewAutomationController(nil), &settingsStoreStub{settings: domain.DefaultKeywordSettings(), appSettings: domain.DefaultAppSettings()})

	_, err := bindings.GetReplyStatistics("", "")

	require.EqualError(t, err, "reply statistics are unavailable")
}
