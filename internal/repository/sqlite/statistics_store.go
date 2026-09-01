package sqlite

import (
	"context"
	"errors"
	"fmt"
	"time"

	"telegram-companion/internal/domain"
)

func (s *ProductionStore) ReplyStatistics(ctx context.Context, from, to time.Time) (domain.ReplyStatistics, error) {
	if s == nil || s.db == nil {
		return domain.ReplyStatistics{}, errors.New("reply statistics store is not configured")
	}
	if !from.IsZero() && !to.IsZero() && !to.After(from) {
		return domain.ReplyStatistics{}, errors.New("reply statistics end must be after start")
	}

	statistics := domain.ReplyStatistics{
		TimeSeries: make([]domain.ReplyStatisticsBucket, 0),
		Accounts:   make([]domain.ReplyStatisticsAccountRow, 0),
		Channels:   make([]domain.ReplyStatisticsChannelRow, 0),
	}
	filter := replyStatisticsFilter(from, to)
	if err := s.db.QueryRowContext(ctx, `SELECT
		COALESCE(SUM(CASE WHEN event.success=1 AND event.type='public_reply' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN event.success=1 AND event.type='private_message' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN event.success=0 AND event.type='private_message'
			AND event.error_code='private_message_closed' THEN 1 ELSE 0 END), 0),
		COUNT(DISTINCT CASE WHEN event.success=1 THEN channel_id END),
		COUNT(DISTINCT CASE WHEN event.success=1 THEN account_id END)
		FROM outgoing_message_events event`+filter.clause, filter.args...).Scan(
		&statistics.Totals.PublicReplies, &statistics.Totals.PrivateMessages,
		&statistics.Totals.PrivateMessagesClosed, &statistics.Totals.Channels, &statistics.Totals.Accounts,
	); err != nil {
		return domain.ReplyStatistics{}, fmt.Errorf("query reply statistic totals: %w", err)
	}
	statistics.Totals.Replies = statistics.Totals.PublicReplies + statistics.Totals.PrivateMessages
	if !from.IsZero() && !to.IsZero() {
		statistics.Totals.AverageRepliesPerMinute = float64(statistics.Totals.Replies) / to.Sub(from).Minutes()
	}
	if err := s.loadReplyStatisticsBuckets(ctx, filter, &statistics); err != nil {
		return domain.ReplyStatistics{}, err
	}
	if err := s.loadReplyStatisticsAccounts(ctx, filter, &statistics); err != nil {
		return domain.ReplyStatistics{}, err
	}
	if err := s.loadReplyStatisticsChannels(ctx, filter, &statistics); err != nil {
		return domain.ReplyStatistics{}, err
	}
	return statistics, nil
}

type replyStatisticsQueryFilter struct {
	clause string
	args   []any
}

func replyStatisticsFilter(from, to time.Time) replyStatisticsQueryFilter {
	filter := replyStatisticsQueryFilter{clause: " WHERE 1=1"}
	if !from.IsZero() {
		filter.clause += " AND event.created_at>=?"
		filter.args = append(filter.args, formatTime(from))
	}
	if !to.IsZero() {
		filter.clause += " AND event.created_at<?"
		filter.args = append(filter.args, formatTime(to))
	}
	return filter
}

func (s *ProductionStore) loadReplyStatisticsBuckets(ctx context.Context, filter replyStatisticsQueryFilter, statistics *domain.ReplyStatistics) error {
	// filter.clause is assembled only from fixed timestamp predicates in replyStatisticsFilter.
	//nolint:gosec // G202 is a false positive: timestamps remain bound query parameters.
	rows, err := s.db.QueryContext(ctx, `SELECT substr(created_at, 1, 10), COUNT(*)
		FROM outgoing_message_events event`+filter.clause+` AND event.success=1
		GROUP BY substr(event.created_at, 1, 10) ORDER BY substr(event.created_at, 1, 10)`, filter.args...)
	if err != nil {
		return fmt.Errorf("query reply statistic time series: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var bucket domain.ReplyStatisticsBucket
		if err := rows.Scan(&bucket.Date, &bucket.Replies); err != nil {
			return fmt.Errorf("scan reply statistic time series: %w", err)
		}
		statistics.TimeSeries = append(statistics.TimeSeries, bucket)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate reply statistic time series: %w", err)
	}
	return nil
}

func (s *ProductionStore) loadReplyStatisticsAccounts(ctx context.Context, filter replyStatisticsQueryFilter, statistics *domain.ReplyStatistics) error {
	// filter.clause is assembled only from fixed timestamp predicates in replyStatisticsFilter.
	//nolint:gosec // G202 is a false positive: timestamps remain bound query parameters.
	rows, err := s.db.QueryContext(ctx, `SELECT event.account_id,
		COALESCE(NULLIF(TRIM(account.display_name), ''), account.id),
		SUM(CASE WHEN event.success=1 AND event.type='public_reply' THEN 1 ELSE 0 END),
		SUM(CASE WHEN event.success=1 AND event.type='private_message' THEN 1 ELSE 0 END),
		SUM(CASE WHEN event.success=0 AND event.type='private_message'
			AND event.error_code='private_message_closed' THEN 1 ELSE 0 END),
		MAX(CASE WHEN event.success=1 OR (event.success=0 AND event.type='private_message'
			AND event.error_code='private_message_closed') THEN event.created_at END)
		FROM outgoing_message_events event
		JOIN accounts account ON account.id=event.account_id`+filter.clause+`
		GROUP BY event.account_id
		HAVING SUM(CASE WHEN event.success=1 OR (event.success=0 AND event.type='private_message'
			AND event.error_code='private_message_closed') THEN 1 ELSE 0 END)>0
		ORDER BY (SUM(CASE WHEN event.success=1 AND event.type='public_reply' THEN 1 ELSE 0 END)
			+ SUM(CASE WHEN event.success=1 AND event.type='private_message' THEN 1 ELSE 0 END)) DESC,
			MAX(CASE WHEN event.success=1 OR (event.success=0 AND event.type='private_message'
				AND event.error_code='private_message_closed') THEN event.created_at END) DESC,
			event.account_id`, filter.args...)
	if err != nil {
		return fmt.Errorf("query account reply statistics: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var row domain.ReplyStatisticsAccountRow
		var lastActivity string
		if err := rows.Scan(&row.ID, &row.Title, &row.PublicReplies, &row.PrivateMessages, &row.PrivateMessagesClosed, &lastActivity); err != nil {
			return fmt.Errorf("scan account reply statistics: %w", err)
		}
		row.Replies = row.PublicReplies + row.PrivateMessages
		var err error
		if row.LastActivityAt, err = parseTime(lastActivity); err != nil {
			return err
		}
		statistics.Accounts = append(statistics.Accounts, row)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate account reply statistics: %w", err)
	}
	return nil
}

func (s *ProductionStore) loadReplyStatisticsChannels(ctx context.Context, filter replyStatisticsQueryFilter, statistics *domain.ReplyStatistics) error {
	// filter.clause is assembled only from fixed timestamp predicates in replyStatisticsFilter.
	//nolint:gosec // G202 is a false positive: timestamps remain bound query parameters.
	rows, err := s.db.QueryContext(ctx, `SELECT event.channel_id,
		COALESCE(NULLIF(TRIM(channel.title), ''), channel.id),
		COUNT(*), MAX(event.created_at)
		FROM outgoing_message_events event
		JOIN outbound_channels channel ON channel.id=event.channel_id`+filter.clause+` AND event.success=1
		GROUP BY event.channel_id
		ORDER BY COUNT(*) DESC, MAX(event.created_at) DESC, event.channel_id`, filter.args...)
	if err != nil {
		return fmt.Errorf("query channel reply statistics: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var row domain.ReplyStatisticsChannelRow
		var lastActivity string
		if err := rows.Scan(&row.ID, &row.Title, &row.Replies, &lastActivity); err != nil {
			return fmt.Errorf("scan channel reply statistics: %w", err)
		}
		var err error
		if row.LastActivityAt, err = parseTime(lastActivity); err != nil {
			return err
		}
		statistics.Channels = append(statistics.Channels, row)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate channel reply statistics: %w", err)
	}
	return nil
}

var _ domain.ReplyStatisticsProvider = (*ProductionStore)(nil)
