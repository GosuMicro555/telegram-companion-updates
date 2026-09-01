package domain

import (
	"errors"
	"testing"
	"time"
)

func TestNewScheduledDMTaskDefaultsStatusToPaused(t *testing.T) {
	task, err := NewScheduledDMTask(ScheduledDMTask{
		Status:     ScheduledDMStatusActive,
		Recurrence: ScheduledDMRecurrenceOnce,
		MaxRuns:    1,
	})
	if err != nil {
		t.Fatalf("NewScheduledDMTask() error = %v", err)
	}
	if task.Status != ScheduledDMStatusPaused {
		t.Fatalf("NewScheduledDMTask() status = %q, want %q", task.Status, ScheduledDMStatusPaused)
	}
}

func TestNewScheduledDMTaskRejectsInvalidMaxRuns(t *testing.T) {
	tests := []int{-1, 0, 13}
	for _, maxRuns := range tests {
		t.Run(time.Duration(maxRuns).String(), func(t *testing.T) {
			_, err := NewScheduledDMTask(ScheduledDMTask{
				Recurrence: ScheduledDMRecurrenceOnce,
				MaxRuns:    maxRuns,
			})
			if !errors.Is(err, ErrScheduledDMMaxRuns) {
				t.Fatalf("NewScheduledDMTask() error = %v, want %v", err, ErrScheduledDMMaxRuns)
			}
		})
	}
}

func TestNewScheduledDMTaskAcceptsMaxRunsBounds(t *testing.T) {
	for _, maxRuns := range []int{1, 12} {
		t.Run(time.Duration(maxRuns).String(), func(t *testing.T) {
			task, err := NewScheduledDMTask(ScheduledDMTask{
				Recurrence: ScheduledDMRecurrenceDaily,
				MaxRuns:    maxRuns,
			})
			if err != nil {
				t.Fatalf("NewScheduledDMTask() error = %v", err)
			}
			if task.MaxRuns != maxRuns {
				t.Fatalf("NewScheduledDMTask() MaxRuns = %d, want %d", task.MaxRuns, maxRuns)
			}
		})
	}
}

func TestNewScheduledDMTaskRejectsUnsupportedRecurrence(t *testing.T) {
	_, err := NewScheduledDMTask(ScheduledDMTask{
		Recurrence: ScheduledDMRecurrence("forever"),
		MaxRuns:    1,
	})
	if !errors.Is(err, ErrScheduledDMRecurrence) {
		t.Fatalf("NewScheduledDMTask() error = %v, want %v", err, ErrScheduledDMRecurrence)
	}
}

func TestRecurrenceDuration(t *testing.T) {
	tests := []struct {
		name       string
		recurrence ScheduledDMRecurrence
		want       time.Duration
		wantOK     bool
	}{
		{name: "once", recurrence: ScheduledDMRecurrenceOnce, want: 0, wantOK: true},
		{name: "five minutes", recurrence: ScheduledDMRecurrenceFiveMinutes, want: 5 * time.Minute, wantOK: true},
		{name: "ten minutes", recurrence: ScheduledDMRecurrenceTenMinutes, want: 10 * time.Minute, wantOK: true},
		{name: "thirty minutes", recurrence: ScheduledDMRecurrenceThirtyMinutes, want: 30 * time.Minute, wantOK: true},
		{name: "one hour", recurrence: ScheduledDMRecurrenceOneHour, want: time.Hour, wantOK: true},
		{name: "two hours", recurrence: ScheduledDMRecurrenceTwoHours, want: 2 * time.Hour, wantOK: true},
		{name: "three hours", recurrence: ScheduledDMRecurrenceThreeHours, want: 3 * time.Hour, wantOK: true},
		{name: "four hours", recurrence: ScheduledDMRecurrenceFourHours, want: 4 * time.Hour, wantOK: true},
		{name: "five hours", recurrence: ScheduledDMRecurrenceFiveHours, want: 5 * time.Hour, wantOK: true},
		{name: "six hours", recurrence: ScheduledDMRecurrenceSixHours, want: 6 * time.Hour, wantOK: true},
		{name: "seven hours", recurrence: ScheduledDMRecurrenceSevenHours, want: 7 * time.Hour, wantOK: true},
		{name: "eight hours", recurrence: ScheduledDMRecurrenceEightHours, want: 8 * time.Hour, wantOK: true},
		{name: "nine hours", recurrence: ScheduledDMRecurrenceNineHours, want: 9 * time.Hour, wantOK: true},
		{name: "ten hours", recurrence: ScheduledDMRecurrenceTenHours, want: 10 * time.Hour, wantOK: true},
		{name: "eleven hours", recurrence: ScheduledDMRecurrenceElevenHours, want: 11 * time.Hour, wantOK: true},
		{name: "twelve hours", recurrence: ScheduledDMRecurrenceTwelveHours, want: 12 * time.Hour, wantOK: true},
		{name: "daily", recurrence: ScheduledDMRecurrenceDaily, want: 24 * time.Hour, wantOK: true},
		{name: "weekly", recurrence: ScheduledDMRecurrenceWeekly, want: 7 * 24 * time.Hour, wantOK: true},
		{name: "invalid", recurrence: ScheduledDMRecurrence("forever"), want: 0, wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotOK := RecurrenceDuration(tt.recurrence)
			if got != tt.want || gotOK != tt.wantOK {
				t.Fatalf("RecurrenceDuration(%q) = (%s, %t), want (%s, %t)", tt.recurrence, got, gotOK, tt.want, tt.wantOK)
			}
		})
	}
}

func TestScheduledDMDeliveryResultCarriesLeaseOwnership(t *testing.T) {
	completedAt := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	result := ScheduledDMDeliveryResult{
		DeliveryID:  "delivery-1",
		LeaseToken:  "owner-1",
		Status:      ScheduledDMDeliveryStatusSent,
		CompletedAt: completedAt,
		ErrorCode:   "",
	}

	if result.DeliveryID != "delivery-1" || result.LeaseToken != "owner-1" || result.CompletedAt != completedAt {
		t.Fatalf("ScheduledDMDeliveryResult lost lease-owned completion data: %#v", result)
	}
}
