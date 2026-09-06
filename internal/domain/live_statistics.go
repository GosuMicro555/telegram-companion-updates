package domain

import (
	"context"
	"time"
)

type LiveDeliveryDraft struct {
	SourceMessage      string
	TriggerCanonicalID ID
	TriggerSnapshot    string
	TriggeredAt        time.Time
}

type LiveDeliveryQuery struct {
	From          time.Time
	To            time.Time
	Limit         int
	Offset        int
	SortBy        LiveDeliverySortColumn
	SortDirection LiveDeliverySortDirection
}

type LiveDeliverySortColumn string

const (
	LiveDeliverySortTriggeredAt     LiveDeliverySortColumn = "triggered_at"
	LiveDeliverySortSourceMessage   LiveDeliverySortColumn = "source_message"
	LiveDeliverySortTriggerSnapshot LiveDeliverySortColumn = "trigger_snapshot"
	LiveDeliverySortDeliveryType    LiveDeliverySortColumn = "delivery_type"
	LiveDeliverySortAccountTitle    LiveDeliverySortColumn = "account_title_snapshot"
	LiveDeliverySortFinalStatus     LiveDeliverySortColumn = "final_status"
	LiveDeliverySortFinalizedAt     LiveDeliverySortColumn = "finalized_at"
)

type LiveDeliverySortDirection string

const (
	LiveDeliverySortAscending  LiveDeliverySortDirection = "ascending"
	LiveDeliverySortDescending LiveDeliverySortDirection = "descending"
)

type LiveDeliveryPage struct {
	Rows          []LiveDeliveryRow
	Total         int
	DatabaseBytes int64
}

type LiveDeliveryRow struct {
	ID                   ID
	JobID                ID
	SourceMessage        string
	TriggerCanonicalID   *ID
	TriggerSnapshot      string
	TriggeredAt          time.Time
	DeliveryType         *JobType
	AccountID            *ID
	AccountTitleSnapshot string
	FinalStatus          *string
	ErrorCode            string
	FinalizedAt          *time.Time
}

type LiveDeliveryStatisticsProvider interface {
	LiveDeliveryStatistics(context.Context, LiveDeliveryQuery) (LiveDeliveryPage, error)
}
