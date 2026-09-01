package domain

import (
	"context"
	"errors"
	"time"
)

var (
	ErrScheduledDMMaxRuns    = errors.New("scheduled DM max runs must be between 1 and 12")
	ErrScheduledDMRecurrence = errors.New("unsupported scheduled DM recurrence")
)

type ScheduledDMStatus string

const (
	ScheduledDMStatusPaused    ScheduledDMStatus = "paused"
	ScheduledDMStatusActive    ScheduledDMStatus = "active"
	ScheduledDMStatusCompleted ScheduledDMStatus = "completed"
	ScheduledDMStatusCancelled ScheduledDMStatus = "cancelled"
	ScheduledDMStatusError     ScheduledDMStatus = "error"
)

type ScheduledDMRecurrence string

const (
	ScheduledDMRecurrenceOnce          ScheduledDMRecurrence = "once"
	ScheduledDMRecurrenceFiveMinutes   ScheduledDMRecurrence = "5m"
	ScheduledDMRecurrenceTenMinutes    ScheduledDMRecurrence = "10m"
	ScheduledDMRecurrenceThirtyMinutes ScheduledDMRecurrence = "30m"
	ScheduledDMRecurrenceOneHour       ScheduledDMRecurrence = "1h"
	ScheduledDMRecurrenceTwoHours      ScheduledDMRecurrence = "2h"
	ScheduledDMRecurrenceThreeHours    ScheduledDMRecurrence = "3h"
	ScheduledDMRecurrenceFourHours     ScheduledDMRecurrence = "4h"
	ScheduledDMRecurrenceFiveHours     ScheduledDMRecurrence = "5h"
	ScheduledDMRecurrenceSixHours      ScheduledDMRecurrence = "6h"
	ScheduledDMRecurrenceSevenHours    ScheduledDMRecurrence = "7h"
	ScheduledDMRecurrenceEightHours    ScheduledDMRecurrence = "8h"
	ScheduledDMRecurrenceNineHours     ScheduledDMRecurrence = "9h"
	ScheduledDMRecurrenceTenHours      ScheduledDMRecurrence = "10h"
	ScheduledDMRecurrenceElevenHours   ScheduledDMRecurrence = "11h"
	ScheduledDMRecurrenceTwelveHours   ScheduledDMRecurrence = "12h"
	ScheduledDMRecurrenceDaily         ScheduledDMRecurrence = "daily"
	ScheduledDMRecurrenceWeekly        ScheduledDMRecurrence = "weekly"
)

type ScheduledDMRecipientStatus string

const (
	ScheduledDMRecipientStatusUnchecked ScheduledDMRecipientStatus = "unchecked"
	ScheduledDMRecipientStatusResolved  ScheduledDMRecipientStatus = "resolved"
	ScheduledDMRecipientStatusOpen      ScheduledDMRecipientStatus = "open"
	ScheduledDMRecipientStatusClosed    ScheduledDMRecipientStatus = "closed"
	ScheduledDMRecipientStatusInvalid   ScheduledDMRecipientStatus = "invalid"
	ScheduledDMRecipientStatusError     ScheduledDMRecipientStatus = "error"
)

type ScheduledDMDeliveryStatus string

const (
	ScheduledDMDeliveryStatusPending ScheduledDMDeliveryStatus = "pending"
	ScheduledDMDeliveryStatusSending ScheduledDMDeliveryStatus = "sending"
	ScheduledDMDeliveryStatusSent    ScheduledDMDeliveryStatus = "sent"
	ScheduledDMDeliveryStatusClosed  ScheduledDMDeliveryStatus = "closed"
	ScheduledDMDeliveryStatusFailed  ScheduledDMDeliveryStatus = "failed"
)

type ScheduledDMTask struct {
	ID            ID
	MessageText   string
	Status        ScheduledDMStatus
	StartAt       time.Time
	Recurrence    ScheduledDMRecurrence
	MaxRuns       int
	CompletedRuns int
	NextRunAt     *time.Time
	Recipients    []ScheduledDMRecipient
	AccountIDs    []ID
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type ScheduledDMRecipient struct {
	TaskID        ID
	Ordinal       int
	Username      string
	Status        ScheduledDMRecipientStatus
	LastError     string
	LastCheckedAt *time.Time
}

type ScheduledDMDelivery struct {
	ID               ID
	TaskID           ID
	RunID            ID
	RunNumber        int
	Recipient        string
	AccountID        ID
	TelegramRandomID int64
	LeaseToken       string
	Status           ScheduledDMDeliveryStatus
	AttemptedAt      *time.Time
	CompletedAt      *time.Time
	ErrorCode        string
}

type ScheduledDMDeliveryResult struct {
	DeliveryID  ID
	LeaseToken  string
	Status      ScheduledDMDeliveryStatus
	CompletedAt time.Time
	ErrorCode   string
}

type ScheduledDMRepository interface {
	ListTasks(context.Context) ([]ScheduledDMTask, error)
	SaveTask(context.Context, ScheduledDMTask) error
	SetTaskStatus(context.Context, ID, ScheduledDMStatus, time.Time) error
	ClaimDueDelivery(context.Context, time.Time, time.Duration) (*ScheduledDMDelivery, error)
	CompleteDelivery(context.Context, ScheduledDMDeliveryResult) error
	DelayDelivery(context.Context, ID, string, string, time.Time) error
}

func NewScheduledDMTask(task ScheduledDMTask) (ScheduledDMTask, error) {
	if task.MaxRuns < 1 || task.MaxRuns > 12 {
		return ScheduledDMTask{}, ErrScheduledDMMaxRuns
	}
	if _, ok := RecurrenceDuration(task.Recurrence); !ok {
		return ScheduledDMTask{}, ErrScheduledDMRecurrence
	}
	task.Status = ScheduledDMStatusPaused
	return task, nil
}

func RecurrenceDuration(recurrence ScheduledDMRecurrence) (time.Duration, bool) {
	switch recurrence {
	case ScheduledDMRecurrenceOnce:
		return 0, true
	case ScheduledDMRecurrenceFiveMinutes:
		return 5 * time.Minute, true
	case ScheduledDMRecurrenceTenMinutes:
		return 10 * time.Minute, true
	case ScheduledDMRecurrenceThirtyMinutes:
		return 30 * time.Minute, true
	case ScheduledDMRecurrenceOneHour:
		return time.Hour, true
	case ScheduledDMRecurrenceTwoHours:
		return 2 * time.Hour, true
	case ScheduledDMRecurrenceThreeHours:
		return 3 * time.Hour, true
	case ScheduledDMRecurrenceFourHours:
		return 4 * time.Hour, true
	case ScheduledDMRecurrenceFiveHours:
		return 5 * time.Hour, true
	case ScheduledDMRecurrenceSixHours:
		return 6 * time.Hour, true
	case ScheduledDMRecurrenceSevenHours:
		return 7 * time.Hour, true
	case ScheduledDMRecurrenceEightHours:
		return 8 * time.Hour, true
	case ScheduledDMRecurrenceNineHours:
		return 9 * time.Hour, true
	case ScheduledDMRecurrenceTenHours:
		return 10 * time.Hour, true
	case ScheduledDMRecurrenceElevenHours:
		return 11 * time.Hour, true
	case ScheduledDMRecurrenceTwelveHours:
		return 12 * time.Hour, true
	case ScheduledDMRecurrenceDaily:
		return 24 * time.Hour, true
	case ScheduledDMRecurrenceWeekly:
		return 7 * 24 * time.Hour, true
	default:
		return 0, false
	}
}
