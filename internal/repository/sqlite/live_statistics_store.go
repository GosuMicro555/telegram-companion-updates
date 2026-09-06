package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"telegram-companion/internal/domain"
)

const defaultLiveDeliveryPageSize = 50
const liveDeliveryAccountTitleExpression = "COALESCE(NULLIF(live_delivery_history.account_title_snapshot,''),NULLIF(accounts.display_name,''),NULLIF(accounts.phone_masked,''),live_delivery_history.account_id,'')"

var liveDeliveryPageSizes = map[int]struct{}{50: {}, 100: {}, 200: {}, 500: {}, 1000: {}}

type liveDeliverySortSpec struct {
	column string
}

var liveDeliverySortColumns = map[domain.LiveDeliverySortColumn]liveDeliverySortSpec{
	domain.LiveDeliverySortTriggeredAt:     {column: "triggered_at"},
	domain.LiveDeliverySortSourceMessage:   {column: "source_message"},
	domain.LiveDeliverySortTriggerSnapshot: {column: "trigger_snapshot"},
	domain.LiveDeliverySortDeliveryType:    {column: "delivery_type"},
	domain.LiveDeliverySortAccountTitle:    {column: liveDeliveryAccountTitleExpression},
	domain.LiveDeliverySortFinalStatus:     {column: "final_status"},
	domain.LiveDeliverySortFinalizedAt:     {column: "finalized_at"},
}

var liveDeliverySortDirections = map[domain.LiveDeliverySortDirection]string{
	domain.LiveDeliverySortAscending:  "ASC",
	domain.LiveDeliverySortDescending: "DESC",
}

func (s *ProductionStore) LiveDeliveryStatistics(ctx context.Context, query domain.LiveDeliveryQuery) (domain.LiveDeliveryPage, error) {
	if s == nil || s.db == nil {
		return domain.LiveDeliveryPage{}, errors.New("live delivery statistics store is not configured")
	}
	if query.Offset < 0 {
		return domain.LiveDeliveryPage{}, errors.New("live delivery statistics offset must not be negative")
	}
	if query.Limit < 0 {
		return domain.LiveDeliveryPage{}, errors.New("live delivery statistics limit must not be negative")
	}
	if query.Limit == 0 {
		query.Limit = defaultLiveDeliveryPageSize
	}
	if _, ok := liveDeliveryPageSizes[query.Limit]; !ok {
		return domain.LiveDeliveryPage{}, errors.New("live delivery statistics page size is unsupported")
	}
	if !query.From.IsZero() && !query.To.IsZero() && !query.To.After(query.From) {
		return domain.LiveDeliveryPage{}, errors.New("live delivery statistics end must be after start")
	}
	if query.SortBy == "" {
		query.SortBy = domain.LiveDeliverySortTriggeredAt
	}
	sort, ok := liveDeliverySortColumns[query.SortBy]
	if !ok {
		return domain.LiveDeliveryPage{}, errors.New("live delivery statistics sort column is unsupported")
	}
	if query.SortDirection == "" {
		query.SortDirection = domain.LiveDeliverySortDescending
	}
	direction, ok := liveDeliverySortDirections[query.SortDirection]
	if !ok {
		return domain.LiveDeliveryPage{}, errors.New("live delivery statistics sort direction is unsupported")
	}

	page := domain.LiveDeliveryPage{Rows: make([]domain.LiveDeliveryRow, 0)}
	filter, args := liveDeliveryStatisticsFilter(query.From, query.To)
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM live_delivery_history`+filter, args...).Scan(&page.Total); err != nil {
		return domain.LiveDeliveryPage{}, fmt.Errorf("count live delivery history: %w", err)
	}
	// filter contains fixed timestamp predicates; sort and direction come from the closed allowlists above.
	//nolint:gosec // G202 is a false positive: no caller-controlled SQL fragment reaches this query.
	rows, err := s.db.QueryContext(ctx, `SELECT live_delivery_history.id,live_delivery_history.job_id,live_delivery_history.source_message,
		live_delivery_history.trigger_canonical_id,live_delivery_history.trigger_snapshot,live_delivery_history.triggered_at,
		live_delivery_history.delivery_type,live_delivery_history.account_id,`+liveDeliveryAccountTitleExpression+`,
		live_delivery_history.final_status,live_delivery_history.error_code,live_delivery_history.finalized_at
		FROM live_delivery_history LEFT JOIN accounts ON accounts.id=live_delivery_history.account_id
		`+filter+` ORDER BY `+sort.column+` `+direction+`, live_delivery_history.triggered_at DESC, live_delivery_history.id DESC LIMIT ? OFFSET ?`, append(args, query.Limit, query.Offset)...)
	if err != nil {
		return domain.LiveDeliveryPage{}, fmt.Errorf("query live delivery history: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var row domain.LiveDeliveryRow
		var canonicalID, deliveryType, accountID, finalStatus, finalizedAt sql.NullString
		var triggeredAt string
		if err := rows.Scan(&row.ID, &row.JobID, &row.SourceMessage, &canonicalID, &row.TriggerSnapshot, &triggeredAt,
			&deliveryType, &accountID, &row.AccountTitleSnapshot, &finalStatus, &row.ErrorCode, &finalizedAt); err != nil {
			return domain.LiveDeliveryPage{}, fmt.Errorf("scan live delivery history: %w", err)
		}
		var err error
		if row.TriggeredAt, err = parseTime(triggeredAt); err != nil {
			return domain.LiveDeliveryPage{}, err
		}
		if canonicalID.Valid {
			value := domain.ID(canonicalID.String)
			row.TriggerCanonicalID = &value
		}
		if deliveryType.Valid {
			value := domain.JobType(deliveryType.String)
			row.DeliveryType = &value
		}
		if accountID.Valid {
			value := domain.ID(accountID.String)
			row.AccountID = &value
		}
		if finalStatus.Valid {
			value := finalStatus.String
			row.FinalStatus = &value
		}
		if finalizedAt.Valid {
			value, err := parseTime(finalizedAt.String)
			if err != nil {
				return domain.LiveDeliveryPage{}, err
			}
			row.FinalizedAt = &value
		}
		page.Rows = append(page.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return domain.LiveDeliveryPage{}, fmt.Errorf("iterate live delivery history: %w", err)
	}
	if page.DatabaseBytes, err = liveDeliveryHistoryBytes(ctx, s.db); err != nil {
		return domain.LiveDeliveryPage{}, err
	}
	return page, nil
}

func liveDeliveryStatisticsFilter(from, to time.Time) (string, []any) {
	filter := " WHERE final_status IN ('successful','not_delivered')"
	args := make([]any, 0, 2)
	if !from.IsZero() {
		filter += " AND triggered_at>=?"
		args = append(args, formatTime(from.UTC()))
	}
	if !to.IsZero() {
		filter += " AND triggered_at<?"
		args = append(args, formatTime(to.UTC()))
	}
	return filter, args
}

func liveDeliveryHistoryBytes(ctx context.Context, db *sql.DB) (int64, error) {
	const query = `SELECT COALESCE(SUM(
		COALESCE(length(CAST(id AS BLOB)), 0) +
		COALESCE(length(CAST(job_id AS BLOB)), 0) +
		COALESCE(length(CAST(source_message AS BLOB)), 0) +
		COALESCE(length(CAST(trigger_canonical_id AS BLOB)), 0) +
		COALESCE(length(CAST(trigger_snapshot AS BLOB)), 0) +
		COALESCE(length(CAST(triggered_at AS BLOB)), 0) +
		COALESCE(length(CAST(delivery_type AS BLOB)), 0) +
		COALESCE(length(CAST(account_id AS BLOB)), 0) +
		COALESCE(length(CAST(account_title_snapshot AS BLOB)), 0) +
		COALESCE(length(CAST(final_status AS BLOB)), 0) +
		COALESCE(length(CAST(error_code AS BLOB)), 0) +
		COALESCE(length(CAST(finalized_at AS BLOB)), 0)
	), 0) FROM live_delivery_history`
	var bytes int64
	if err := db.QueryRowContext(ctx, query).Scan(&bytes); err != nil {
		return 0, fmt.Errorf("calculate live delivery history bytes: %w", err)
	}
	return bytes, nil
}

var _ domain.LiveDeliveryStatisticsProvider = (*ProductionStore)(nil)
