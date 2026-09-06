package usecase

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"telegram-companion/internal/domain"
)

var (
	ErrScheduledDMRecipientRequired       = errors.New("scheduled DM recipient is required")
	ErrScheduledDMMessageRequired         = errors.New("scheduled DM message text is required")
	ErrScheduledDMStartMustBeFuture       = errors.New("scheduled DM start time must be in the future")
	ErrScheduledDMAccountsRequired        = errors.New("at least one scheduled DM account is required")
	ErrScheduledDMAccountsLimit           = errors.New("scheduled DM supports at most three accounts")
	ErrScheduledDMDuplicateAccountID      = errors.New("scheduled DM account IDs must be unique")
	ErrScheduledDMAccountNotActiveSpammer = errors.New("scheduled DM account must be an active spammer")
	ErrScheduledDMOnceMaxRuns             = errors.New("one-time scheduled DM must run exactly once")
	ErrScheduledDMTaskNotRestartable      = errors.New("completed or cancelled scheduled DM task cannot be restarted")
)

type ScheduledDMDraft struct {
	ID          domain.ID
	MessageText string
	Recipients  []string
	AccountIDs  []domain.ID
	StartAt     time.Time
	Recurrence  domain.ScheduledDMRecurrence
	MaxRuns     int
}

// ScheduledDMService validates and persists schedules; it has no Telegram dependency.
type ScheduledDMService struct {
	repository domain.ScheduledDMRepository
	accounts   AccountStore
	now        func() time.Time
}

func NewScheduledDMService(repository domain.ScheduledDMRepository, accounts AccountStore, now func() time.Time) *ScheduledDMService {
	if now == nil {
		now = time.Now
	}
	return &ScheduledDMService{repository: repository, accounts: accounts, now: now}
}

func (s *ScheduledDMService) Save(ctx context.Context, draft ScheduledDMDraft) (domain.ScheduledDMTask, error) {
	if err := validateScheduledDMDraft(draft, s.clockNow()); err != nil {
		return domain.ScheduledDMTask{}, err
	}
	if err := s.validateAccounts(ctx, draft.AccountIDs); err != nil {
		return domain.ScheduledDMTask{}, err
	}

	now := s.clockNow()
	taskID := draft.ID
	if taskID == "" {
		taskID = domain.ID(uuid.NewString())
	}
	recipients := make([]domain.ScheduledDMRecipient, len(draft.Recipients))
	for ordinal, username := range draft.Recipients {
		recipients[ordinal] = domain.ScheduledDMRecipient{
			TaskID:   taskID,
			Ordinal:  ordinal,
			Username: strings.TrimSpace(username),
			Status:   domain.ScheduledDMRecipientStatusUnchecked,
		}
	}
	startAt := draft.StartAt.UTC()
	task, err := domain.NewScheduledDMTask(domain.ScheduledDMTask{
		ID:          taskID,
		MessageText: strings.TrimSpace(draft.MessageText),
		StartAt:     startAt,
		Recurrence:  draft.Recurrence,
		MaxRuns:     draft.MaxRuns,
		NextRunAt:   &startAt,
		Recipients:  recipients,
		AccountIDs:  append([]domain.ID(nil), draft.AccountIDs...),
		CreatedAt:   now,
		UpdatedAt:   now,
	})
	if err != nil {
		return domain.ScheduledDMTask{}, err
	}
	if err := s.repository.SaveTask(ctx, task); err != nil {
		return domain.ScheduledDMTask{}, fmt.Errorf("save scheduled DM task: %w", err)
	}
	return task, nil
}

func (s *ScheduledDMService) Start(ctx context.Context, id domain.ID) error {
	tasks, err := s.repository.ListTasks(ctx)
	if err != nil {
		return fmt.Errorf("list scheduled DM tasks: %w", err)
	}
	for _, task := range tasks {
		if task.ID == id && (task.Status == domain.ScheduledDMStatusCancelled || task.Status == domain.ScheduledDMStatusCompleted) {
			return fmt.Errorf("%w: %s", ErrScheduledDMTaskNotRestartable, id)
		}
	}
	if err := s.repository.SetTaskStatus(ctx, id, domain.ScheduledDMStatusActive, s.clockNow()); err != nil {
		return fmt.Errorf("start scheduled DM task: %w", err)
	}
	return nil
}

func (s *ScheduledDMService) Stop(ctx context.Context, id domain.ID) error {
	tasks, err := s.repository.ListTasks(ctx)
	if err != nil {
		return fmt.Errorf("list scheduled DM tasks: %w", err)
	}
	for _, task := range tasks {
		if task.ID == id && (task.Status == domain.ScheduledDMStatusCancelled || task.Status == domain.ScheduledDMStatusCompleted) {
			return fmt.Errorf("%w: %s", ErrScheduledDMTaskNotRestartable, id)
		}
	}
	if err := s.repository.SetTaskStatus(ctx, id, domain.ScheduledDMStatusPaused, s.clockNow()); err != nil {
		return fmt.Errorf("stop scheduled DM task: %w", err)
	}
	return nil
}

func (s *ScheduledDMService) Cancel(ctx context.Context, id domain.ID) error {
	if err := s.repository.SetTaskStatus(ctx, id, domain.ScheduledDMStatusCancelled, s.clockNow()); err != nil {
		return fmt.Errorf("cancel scheduled DM task: %w", err)
	}
	return nil
}

func validateScheduledDMDraft(draft ScheduledDMDraft, now time.Time) error {
	if draft.Recurrence == domain.ScheduledDMRecurrenceOnce && draft.MaxRuns > 1 && draft.MaxRuns <= 12 {
		return ErrScheduledDMOnceMaxRuns
	}
	if len(draft.Recipients) == 0 {
		return ErrScheduledDMRecipientsRequired
	}
	if len(draft.Recipients) > 3 {
		return ErrScheduledDMRecipientsLimit
	}
	for _, recipient := range draft.Recipients {
		if strings.TrimSpace(recipient) == "" {
			return ErrScheduledDMRecipientRequired
		}
	}
	if strings.TrimSpace(draft.MessageText) == "" {
		return ErrScheduledDMMessageRequired
	}
	if !draft.StartAt.After(now) {
		return ErrScheduledDMStartMustBeFuture
	}
	if len(draft.AccountIDs) == 0 {
		return ErrScheduledDMAccountsRequired
	}
	uniqueAccountIDs := make(map[domain.ID]struct{}, len(draft.AccountIDs))
	for _, accountID := range draft.AccountIDs {
		if _, exists := uniqueAccountIDs[accountID]; exists {
			return ErrScheduledDMDuplicateAccountID
		}
		uniqueAccountIDs[accountID] = struct{}{}
	}
	if len(uniqueAccountIDs) > 3 {
		return ErrScheduledDMAccountsLimit
	}
	return nil
}

func (s *ScheduledDMService) validateAccounts(ctx context.Context, selected []domain.ID) error {
	accounts, err := s.accounts.ListAccounts(ctx)
	if err != nil {
		return fmt.Errorf("list scheduled DM accounts: %w", err)
	}
	usableSpammers := make(map[domain.ID]struct{}, len(accounts))
	for _, account := range accounts {
		if account.Role == domain.AccountRoleSpammer && isScheduledDMAccountUsable(account.Status) {
			usableSpammers[account.ID] = struct{}{}
		}
	}
	for _, id := range selected {
		if _, ok := usableSpammers[id]; !ok {
			return ErrScheduledDMAccountNotActiveSpammer
		}
	}
	return nil
}

func isScheduledDMAccountUsable(status domain.AccountStatus) bool {
	return status == "ready" || status == "connected" || status == domain.AccountActive
}

func (s *ScheduledDMService) clockNow() time.Time {
	return s.now().UTC()
}
