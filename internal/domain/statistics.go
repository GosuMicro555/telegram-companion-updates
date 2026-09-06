package domain

import (
	"context"
	"time"
)

type ReplyStatisticsProvider interface {
	ReplyStatistics(context.Context, time.Time, time.Time) (ReplyStatistics, error)
}

type ReplyStatistics struct {
	Totals     ReplyStatisticsTotals
	TimeSeries []ReplyStatisticsBucket
	Accounts   []ReplyStatisticsAccountRow
	Channels   []ReplyStatisticsChannelRow
}

type ReplyStatisticsTotals struct {
	Replies                 int64
	PublicReplies           int64
	PrivateMessages         int64
	PrivateMessagesClosed   int64
	Channels                int
	Accounts                int
	AverageRepliesPerMinute float64
}

type ReplyStatisticsBucket struct {
	Date    string
	Replies int64
}

type ReplyStatisticsAccountRow struct {
	ID                    ID
	Title                 string
	Replies               int64
	PublicReplies         int64
	PrivateMessages       int64
	PrivateMessagesClosed int64
	LastActivityAt        time.Time
}

type ReplyStatisticsChannelRow struct {
	ID             ID
	Title          string
	Replies        int64
	LastActivityAt time.Time
}
