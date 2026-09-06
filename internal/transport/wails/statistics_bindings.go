package wails

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"telegram-companion/internal/domain"
)

type ReplyStatisticsDTO struct {
	Replies                 int64                      `json:"replies"`
	PublicReplies           int64                      `json:"publicReplies"`
	PrivateMessages         int64                      `json:"privateMessages"`
	PrivateMessagesClosed   int64                      `json:"privateMessagesClosed"`
	Channels                int                        `json:"channels"`
	Accounts                int                        `json:"accounts"`
	AverageRepliesPerMinute float64                    `json:"averageRepliesPerMinute"`
	TimeSeries              []ReplyStatisticsBucketDTO `json:"timeSeries"`
	AccountRows             []ReplyStatisticsRowDTO    `json:"accountRows"`
	ChannelRows             []ReplyStatisticsRowDTO    `json:"channelRows"`
}

type ReplyStatisticsBucketDTO struct {
	Date    string `json:"date"`
	Replies int64  `json:"replies"`
}

type ReplyStatisticsRowDTO struct {
	ID                    string `json:"id"`
	Title                 string `json:"title"`
	Replies               int64  `json:"replies"`
	PublicReplies         int64  `json:"publicReplies"`
	PrivateMessages       int64  `json:"privateMessages"`
	PrivateMessagesClosed int64  `json:"privateMessagesClosed"`
	LastActivityAt        string `json:"lastActivityAt"`
}

func (b *Bindings) GetReplyStatistics(fromISO, toISO string) (ReplyStatisticsDTO, error) {
	if err := b.runtimeError(); err != nil {
		return ReplyStatisticsDTO{}, err
	}
	provider, ok := b.settings.(domain.ReplyStatisticsProvider)
	if !ok {
		return ReplyStatisticsDTO{}, errors.New("reply statistics are unavailable")
	}
	from, err := parseReplyStatisticsISO(fromISO, false)
	if err != nil {
		return ReplyStatisticsDTO{}, err
	}
	to, err := parseReplyStatisticsISO(toISO, true)
	if err != nil {
		return ReplyStatisticsDTO{}, err
	}
	statistics, err := provider.ReplyStatistics(context.Background(), from, to)
	if err != nil {
		return ReplyStatisticsDTO{}, err
	}
	return replyStatisticsDTO(statistics), nil
}

func parseReplyStatisticsISO(value string, endOfDate bool) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	if len(value) == len("2006-01-02") {
		parsed, err := time.Parse("2006-01-02", value)
		if err != nil {
			return time.Time{}, fmt.Errorf("parse reply statistics date: %w", err)
		}
		if endOfDate {
			return parsed.AddDate(0, 0, 1), nil
		}
		return parsed, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse reply statistics date: %w", err)
	}
	return parsed.UTC(), nil
}

func replyStatisticsDTO(statistics domain.ReplyStatistics) ReplyStatisticsDTO {
	result := ReplyStatisticsDTO{
		Replies:                 statistics.Totals.Replies,
		PublicReplies:           statistics.Totals.PublicReplies,
		PrivateMessages:         statistics.Totals.PrivateMessages,
		PrivateMessagesClosed:   statistics.Totals.PrivateMessagesClosed,
		Channels:                statistics.Totals.Channels,
		Accounts:                statistics.Totals.Accounts,
		AverageRepliesPerMinute: statistics.Totals.AverageRepliesPerMinute,
		TimeSeries:              make([]ReplyStatisticsBucketDTO, 0, len(statistics.TimeSeries)),
		AccountRows:             make([]ReplyStatisticsRowDTO, 0, len(statistics.Accounts)),
		ChannelRows:             make([]ReplyStatisticsRowDTO, 0, len(statistics.Channels)),
	}
	for _, bucket := range statistics.TimeSeries {
		result.TimeSeries = append(result.TimeSeries, ReplyStatisticsBucketDTO{Date: bucket.Date, Replies: bucket.Replies})
	}
	for _, row := range statistics.Accounts {
		result.AccountRows = append(result.AccountRows, replyStatisticsRowDTO(
			row.ID, row.Title, row.Replies, row.PublicReplies, row.PrivateMessages, row.PrivateMessagesClosed, row.LastActivityAt,
		))
	}
	for _, row := range statistics.Channels {
		result.ChannelRows = append(result.ChannelRows, replyStatisticsRowDTO(row.ID, row.Title, row.Replies, 0, 0, 0, row.LastActivityAt))
	}
	return result
}

func replyStatisticsRowDTO(id domain.ID, title string, replies, publicReplies, privateMessages, privateMessagesClosed int64, lastActivityAt time.Time) ReplyStatisticsRowDTO {
	return ReplyStatisticsRowDTO{
		ID: string(id), Title: title, Replies: replies, PublicReplies: publicReplies,
		PrivateMessages: privateMessages, PrivateMessagesClosed: privateMessagesClosed,
		LastActivityAt: lastActivityAt.UTC().Format(time.RFC3339Nano),
	}
}
