package usecase

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"telegram-companion/internal/domain"
)

func TestScheduledDMServiceSavePersistsPausedTaskWithDurableRoundRobinAssignments(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.FixedZone("UTC+3", 3*60*60))
	repository := &scheduledDMRepositoryFake{}
	service := NewScheduledDMService(repository, scheduledDMAccountStoreFake{accounts: []domain.Account{
		{ID: "spammer-1", Role: domain.AccountRoleSpammer, Status: domain.AccountActive},
		{ID: "spammer-2", Role: domain.AccountRoleSpammer, Status: domain.AccountActive},
	}}, func() time.Time { return now })

	task, err := service.Save(context.Background(), ScheduledDMDraft{
		MessageText: "  hello  ",
		Recipients:  []string{"alice", "bob", "carol"},
		AccountIDs:  []domain.ID{"spammer-2", "spammer-1"},
		StartAt:     now.Add(time.Hour),
		Recurrence:  domain.ScheduledDMRecurrenceFiveMinutes,
		MaxRuns:     12,
	})
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if task.ID == "" {
		t.Fatal("Save() task ID is empty")
	}
	if task.Status != domain.ScheduledDMStatusPaused {
		t.Fatalf("Save() status = %q, want %q", task.Status, domain.ScheduledDMStatusPaused)
	}
	if task.MessageText != "hello" {
		t.Fatalf("Save() message text = %q, want %q", task.MessageText, "hello")
	}
	if !reflect.DeepEqual(task.AccountIDs, []domain.ID{"spammer-2", "spammer-1"}) {
		t.Fatalf("Save() account assignment = %#v, want ordered durable assignment", task.AccountIDs)
	}
	if task.CreatedAt != now.UTC() || task.UpdatedAt != now.UTC() {
		t.Fatalf("Save() timestamps = (%s, %s), want %s", task.CreatedAt, task.UpdatedAt, now.UTC())
	}
	for ordinal, recipient := range task.Recipients {
		if recipient.TaskID != task.ID || recipient.Ordinal != ordinal || recipient.Status != domain.ScheduledDMRecipientStatusUnchecked {
			t.Fatalf("Save() recipient %d = %#v, want unchecked persisted recipient", ordinal, recipient)
		}
	}
	if len(repository.saved) != 1 || !reflect.DeepEqual(repository.saved[0], task) {
		t.Fatalf("Save() repository task = %#v, want %#v", repository.saved, task)
	}
}

func TestScheduledDMServiceSaveAcceptsUsableSpammerAccountStatuses(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	for _, status := range []domain.AccountStatus{"ready", "connected", domain.AccountActive} {
		t.Run(string(status), func(t *testing.T) {
			repository := &scheduledDMRepositoryFake{}
			service := NewScheduledDMService(repository, scheduledDMAccountStoreFake{accounts: []domain.Account{
				{ID: "spammer", Role: domain.AccountRoleSpammer, Status: status},
			}}, func() time.Time { return now })

			_, err := service.Save(context.Background(), validScheduledDMDraft(now))
			if err != nil {
				t.Fatalf("Save() error = %v", err)
			}
		})
	}
}

func TestScheduledDMServiceSaveRejectsInvalidDraft(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	accounts := []domain.Account{
		{ID: "spammer", Role: domain.AccountRoleSpammer, Status: domain.AccountActive},
		{ID: "paused", Role: domain.AccountRoleSpammer, Status: domain.AccountPaused},
		{ID: "scout", Role: domain.AccountRoleScoutAnalyst, Status: domain.AccountActive},
	}
	tests := []struct {
		name string
		edit func(*ScheduledDMDraft)
		want error
	}{
		{name: "no recipients", edit: func(draft *ScheduledDMDraft) { draft.Recipients = nil }, want: ErrScheduledDMRecipientsRequired},
		{name: "too many recipients", edit: func(draft *ScheduledDMDraft) { draft.Recipients = []string{"a", "b", "c", "d"} }, want: ErrScheduledDMRecipientsLimit},
		{name: "blank recipient", edit: func(draft *ScheduledDMDraft) { draft.Recipients = []string{" "} }, want: ErrScheduledDMRecipientRequired},
		{name: "blank message", edit: func(draft *ScheduledDMDraft) { draft.MessageText = " \n " }, want: ErrScheduledDMMessageRequired},
		{name: "start now", edit: func(draft *ScheduledDMDraft) { draft.StartAt = now }, want: ErrScheduledDMStartMustBeFuture},
		{name: "start in past", edit: func(draft *ScheduledDMDraft) { draft.StartAt = now.Add(-time.Second) }, want: ErrScheduledDMStartMustBeFuture},
		{name: "no accounts", edit: func(draft *ScheduledDMDraft) { draft.AccountIDs = nil }, want: ErrScheduledDMAccountsRequired},
		{name: "too many accounts", edit: func(draft *ScheduledDMDraft) { draft.AccountIDs = []domain.ID{"spammer", "paused", "scout", "missing"} }, want: ErrScheduledDMAccountsLimit},
		{name: "unknown account", edit: func(draft *ScheduledDMDraft) { draft.AccountIDs = []domain.ID{"missing"} }, want: ErrScheduledDMAccountNotActiveSpammer},
		{name: "paused account", edit: func(draft *ScheduledDMDraft) { draft.AccountIDs = []domain.ID{"paused"} }, want: ErrScheduledDMAccountNotActiveSpammer},
		{name: "non spammer", edit: func(draft *ScheduledDMDraft) { draft.AccountIDs = []domain.ID{"scout"} }, want: ErrScheduledDMAccountNotActiveSpammer},
		{name: "unsupported recurrence", edit: func(draft *ScheduledDMDraft) { draft.Recurrence = "forever" }, want: domain.ErrScheduledDMRecurrence},
		{name: "too few runs", edit: func(draft *ScheduledDMDraft) { draft.MaxRuns = 0 }, want: domain.ErrScheduledDMMaxRuns},
		{name: "too many runs", edit: func(draft *ScheduledDMDraft) { draft.MaxRuns = 13 }, want: domain.ErrScheduledDMMaxRuns},
		{name: "one-time task with multiple runs", edit: func(draft *ScheduledDMDraft) { draft.MaxRuns = 2 }, want: ErrScheduledDMOnceMaxRuns},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repository := &scheduledDMRepositoryFake{}
			service := NewScheduledDMService(repository, scheduledDMAccountStoreFake{accounts: accounts}, func() time.Time { return now })
			draft := validScheduledDMDraft(now)
			tt.edit(&draft)

			_, err := service.Save(context.Background(), draft)
			if !errors.Is(err, tt.want) {
				t.Fatalf("Save() error = %v, want %v", err, tt.want)
			}
			if len(repository.saved) != 0 {
				t.Fatalf("Save() persisted invalid draft: %#v", repository.saved)
			}
		})
	}
}

func TestScheduledDMServiceSaveRejectsDuplicateAccountsBeforeStoreCalls(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	repository := &scheduledDMRepositoryFake{}
	accountListCalls := 0
	service := NewScheduledDMService(repository, scheduledDMAccountStoreFake{listCalls: &accountListCalls}, func() time.Time { return now })
	draft := validScheduledDMDraft(now)
	draft.AccountIDs = []domain.ID{"spammer", "spammer"}

	_, err := service.Save(context.Background(), draft)
	if !errors.Is(err, ErrScheduledDMDuplicateAccountID) {
		t.Fatalf("Save() error = %v, want %v", err, ErrScheduledDMDuplicateAccountID)
	}
	if accountListCalls != 0 {
		t.Fatalf("Save() account list calls = %d, want 0", accountListCalls)
	}
	if len(repository.saved) != 0 {
		t.Fatalf("Save() persisted duplicate account draft: %#v", repository.saved)
	}
}

func TestScheduledDMServiceLifecycleChangesOnlyTaskStatus(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	repository := &scheduledDMRepositoryFake{}
	service := NewScheduledDMService(repository, scheduledDMAccountStoreFake{}, func() time.Time { return now })

	for _, operation := range []struct {
		name   string
		call   func(context.Context, domain.ID) error
		status domain.ScheduledDMStatus
	}{
		{name: "start", call: service.Start, status: domain.ScheduledDMStatusActive},
		{name: "stop", call: service.Stop, status: domain.ScheduledDMStatusPaused},
		{name: "cancel", call: service.Cancel, status: domain.ScheduledDMStatusCancelled},
	} {
		t.Run(operation.name, func(t *testing.T) {
			if err := operation.call(context.Background(), "task-1"); err != nil {
				t.Fatalf("%s() error = %v", operation.name, err)
			}
			got := repository.statusChanges[len(repository.statusChanges)-1]
			if got.id != "task-1" || got.status != operation.status || got.at != now {
				t.Fatalf("%s() status change = %#v, want task-1 %q at %s", operation.name, got, operation.status, now)
			}
		})
	}
	if len(repository.saved) != 0 {
		t.Fatalf("lifecycle operations saved tasks: %#v", repository.saved)
	}
}

func TestScheduledDMServiceStartRejectsCancelledAndCompletedTasks(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	repository := &scheduledDMRepositoryFake{tasks: []domain.ScheduledDMTask{
		{ID: "cancelled", Status: domain.ScheduledDMStatusCancelled},
		{ID: "completed", Status: domain.ScheduledDMStatusCompleted},
	}}
	service := NewScheduledDMService(repository, scheduledDMAccountStoreFake{}, func() time.Time { return now })

	for _, id := range []domain.ID{"cancelled", "completed"} {
		t.Run(string(id), func(t *testing.T) {
			err := service.Start(context.Background(), id)
			if !errors.Is(err, ErrScheduledDMTaskNotRestartable) {
				t.Fatalf("Start() error = %v, want %v", err, ErrScheduledDMTaskNotRestartable)
			}
		})
	}
	if len(repository.statusChanges) != 0 {
		t.Fatalf("Start() changed terminal task status: %#v", repository.statusChanges)
	}
}

func TestScheduledDMServiceStopRejectsCancelledAndCompletedTasks(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	repository := &scheduledDMRepositoryFake{tasks: []domain.ScheduledDMTask{
		{ID: "cancelled", Status: domain.ScheduledDMStatusCancelled},
		{ID: "completed", Status: domain.ScheduledDMStatusCompleted},
	}}
	originalTasks := append([]domain.ScheduledDMTask(nil), repository.tasks...)
	service := NewScheduledDMService(repository, scheduledDMAccountStoreFake{}, func() time.Time { return now })

	for _, id := range []domain.ID{"cancelled", "completed"} {
		t.Run(string(id), func(t *testing.T) {
			err := service.Stop(context.Background(), id)
			if !errors.Is(err, ErrScheduledDMTaskNotRestartable) {
				t.Fatalf("Stop() error = %v, want %v", err, ErrScheduledDMTaskNotRestartable)
			}
		})
	}
	if len(repository.statusChanges) != 0 {
		t.Fatalf("Stop() changed terminal task status: %#v", repository.statusChanges)
	}
	if !reflect.DeepEqual(repository.tasks, originalTasks) {
		t.Fatalf("Stop() changed repository tasks: %#v, want %#v", repository.tasks, originalTasks)
	}
}

func TestScheduledDMServiceSaveWrapsAccountListError(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	cause := errors.New("account store unavailable")
	storeErr := &scheduledDMStoreTestError{cause: cause}
	service := NewScheduledDMService(&scheduledDMRepositoryFake{}, scheduledDMAccountStoreFake{err: storeErr}, func() time.Time { return now })

	_, err := service.Save(context.Background(), validScheduledDMDraft(now))
	assertScheduledDMWrappedError(t, err, cause, "list scheduled DM accounts")
}

func TestScheduledDMServiceSaveWrapsRepositoryError(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	cause := errors.New("task repository unavailable")
	storeErr := &scheduledDMStoreTestError{cause: cause}
	repository := &scheduledDMRepositoryFake{saveErr: storeErr}
	service := NewScheduledDMService(repository, scheduledDMAccountStoreFake{accounts: []domain.Account{
		{ID: "spammer", Role: domain.AccountRoleSpammer, Status: domain.AccountActive},
	}}, func() time.Time { return now })

	_, err := service.Save(context.Background(), validScheduledDMDraft(now))
	assertScheduledDMWrappedError(t, err, cause, "save scheduled DM task")
}

func TestScheduledDMServiceLifecycleWrapsRepositoryErrors(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		operation func(*ScheduledDMService, context.Context, domain.ID) error
		context   string
	}{
		{name: "start", operation: (*ScheduledDMService).Start, context: "start scheduled DM task"},
		{name: "stop", operation: (*ScheduledDMService).Stop, context: "stop scheduled DM task"},
		{name: "cancel", operation: (*ScheduledDMService).Cancel, context: "cancel scheduled DM task"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cause := errors.New("status repository unavailable")
			storeErr := &scheduledDMStoreTestError{cause: cause}
			repository := &scheduledDMRepositoryFake{statusErr: storeErr}
			service := NewScheduledDMService(repository, scheduledDMAccountStoreFake{}, func() time.Time { return now })

			err := tt.operation(service, context.Background(), "task-1")
			assertScheduledDMWrappedError(t, err, cause, tt.context)
		})
	}
}

func assertScheduledDMWrappedError(t *testing.T, got error, cause error, operation string) {
	t.Helper()
	if !errors.Is(got, cause) {
		t.Fatalf("error = %v, want errors.Is(..., %v)", got, cause)
	}
	var typed *scheduledDMStoreTestError
	if !errors.As(got, &typed) {
		t.Fatalf("error = %v, want errors.As(..., *scheduledDMStoreTestError)", got)
	}
	if !strings.Contains(got.Error(), operation) {
		t.Fatalf("error = %q, want operation context %q", got, operation)
	}
}

func validScheduledDMDraft(now time.Time) ScheduledDMDraft {
	return ScheduledDMDraft{
		MessageText: "hello",
		Recipients:  []string{"alice"},
		AccountIDs:  []domain.ID{"spammer"},
		StartAt:     now.Add(time.Minute),
		Recurrence:  domain.ScheduledDMRecurrenceOnce,
		MaxRuns:     1,
	}
}

type scheduledDMRepositoryFake struct {
	tasks         []domain.ScheduledDMTask
	saved         []domain.ScheduledDMTask
	statusChanges []scheduledDMStatusChange
	saveErr       error
	statusErr     error
}

type scheduledDMStatusChange struct {
	id     domain.ID
	status domain.ScheduledDMStatus
	at     time.Time
}

func (f *scheduledDMRepositoryFake) ListTasks(context.Context) ([]domain.ScheduledDMTask, error) {
	return append([]domain.ScheduledDMTask(nil), f.tasks...), nil
}

func (f *scheduledDMRepositoryFake) SaveTask(_ context.Context, task domain.ScheduledDMTask) error {
	f.saved = append(f.saved, task)
	return f.saveErr
}

func (f *scheduledDMRepositoryFake) SetTaskStatus(_ context.Context, id domain.ID, status domain.ScheduledDMStatus, at time.Time) error {
	f.statusChanges = append(f.statusChanges, scheduledDMStatusChange{id: id, status: status, at: at})
	return f.statusErr
}

func (f *scheduledDMRepositoryFake) ClaimDueDelivery(context.Context, time.Time, time.Duration) (*domain.ScheduledDMDelivery, error) {
	return nil, nil
}

func (f *scheduledDMRepositoryFake) CompleteDelivery(context.Context, domain.ScheduledDMDeliveryResult) error {
	return nil
}

func (f *scheduledDMRepositoryFake) DelayDelivery(context.Context, domain.ID, string, string, time.Time) error {
	return nil
}

type scheduledDMAccountStoreFake struct {
	accounts  []domain.Account
	err       error
	listCalls *int
}

func (f scheduledDMAccountStoreFake) ListAccounts(context.Context) ([]domain.Account, error) {
	if f.listCalls != nil {
		(*f.listCalls)++
	}
	return f.accounts, f.err
}

func (f scheduledDMAccountStoreFake) SaveAccount(context.Context, domain.Account) error {
	return nil
}

type scheduledDMStoreTestError struct {
	cause error
}

func (e *scheduledDMStoreTestError) Error() string {
	return "scheduled DM store test error: " + e.cause.Error()
}

func (e *scheduledDMStoreTestError) Unwrap() error {
	return e.cause
}
