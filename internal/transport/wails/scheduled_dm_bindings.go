package wails

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"telegram-companion/internal/domain"
	"telegram-companion/internal/usecase"
)

// ScheduledDMDraftDTO contains only the user-editable schedule fields.
type ScheduledDMDraftDTO struct {
	ID          string   `json:"id"`
	MessageText string   `json:"messageText"`
	Recipients  []string `json:"recipients"`
	AccountIDs  []string `json:"accountIds"`
	StartAt     string   `json:"startAt"`
	Recurrence  string   `json:"recurrence"`
	MaxRuns     int      `json:"maxRuns"`
}

// ScheduledDMRecipientDTO intentionally omits Telegram peer identifiers and access hashes.
type ScheduledDMRecipientDTO struct {
	Username      string `json:"username"`
	AccountID     string `json:"accountId"`
	Status        string `json:"status"`
	LastError     string `json:"lastError"`
	LastCheckedAt string `json:"lastCheckedAt"`
}

// ScheduledDMTaskDTO is the safe, local task view returned to the frontend.
type ScheduledDMTaskDTO struct {
	ID            string                    `json:"id"`
	MessageText   string                    `json:"messageText"`
	Status        string                    `json:"status"`
	StartAt       string                    `json:"startAt"`
	Recurrence    string                    `json:"recurrence"`
	MaxRuns       int                       `json:"maxRuns"`
	CompletedRuns int                       `json:"completedRuns"`
	NextRunAt     string                    `json:"nextRunAt"`
	Recipients    []ScheduledDMRecipientDTO `json:"recipients"`
	AccountIDs    []string                  `json:"accountIds"`
	CreatedAt     string                    `json:"createdAt"`
	UpdatedAt     string                    `json:"updatedAt"`
}

// scheduledDMRuntime keeps this independently-started workflow out of global automation.
type scheduledDMRuntime struct {
	Service  *usecase.ScheduledDMService
	Tasks    domain.ScheduledDMRepository
	Accounts domain.AccountRepository
	Resolver usecase.ScheduledDMSender
	Runner   *usecase.AutomationController
}

// ConfigureScheduledDM injects the independently-started scheduled DM workflow
// from desktop composition without adding a Wails-bound Bindings method.
func ConfigureScheduledDM(
	bindings *Bindings,
	service *usecase.ScheduledDMService,
	tasks domain.ScheduledDMRepository,
	accounts domain.AccountRepository,
	resolver usecase.ScheduledDMSender,
	runner *usecase.AutomationController,
) {
	bindings.scheduledDM = scheduledDMRuntime{
		Service: service, Tasks: tasks, Accounts: accounts, Resolver: resolver, Runner: runner,
	}
}

func (b *Bindings) ListScheduledDMTasks() ([]ScheduledDMTaskDTO, error) {
	if err := b.scheduledDMReady(); err != nil {
		return nil, err
	}
	tasks, err := b.scheduledDM.Tasks.ListTasks(b.rootContext())
	if err != nil {
		return nil, err
	}
	result := make([]ScheduledDMTaskDTO, len(tasks))
	for index, task := range tasks {
		result[index] = scheduledDMTaskDTO(task)
	}
	return result, nil
}

func (b *Bindings) SaveScheduledDMTask(draft ScheduledDMDraftDTO) (ScheduledDMTaskDTO, error) {
	if err := b.runtimeError(); err != nil {
		return ScheduledDMTaskDTO{}, err
	}
	if err := b.scheduledDMReady(); err != nil {
		return ScheduledDMTaskDTO{}, err
	}
	startAt, err := time.Parse(time.RFC3339Nano, draft.StartAt)
	if err != nil {
		return ScheduledDMTaskDTO{}, fmt.Errorf("parse scheduled DM start time: %w", err)
	}
	recipients, err := usecase.ParseScheduledDMRecipients(strings.Join(draft.Recipients, "\n"))
	if err != nil {
		return ScheduledDMTaskDTO{}, err
	}
	accountIDs := make([]domain.ID, len(draft.AccountIDs))
	for index, id := range draft.AccountIDs {
		accountIDs[index] = domain.ID(id)
	}
	task, err := b.scheduledDM.Service.Save(b.rootContext(), usecase.ScheduledDMDraft{
		ID: domain.ID(draft.ID), MessageText: draft.MessageText, Recipients: recipients, AccountIDs: accountIDs,
		StartAt: startAt, Recurrence: domain.ScheduledDMRecurrence(draft.Recurrence), MaxRuns: draft.MaxRuns,
	})
	if err != nil {
		return ScheduledDMTaskDTO{}, err
	}
	return scheduledDMTaskDTO(task), nil
}

func (b *Bindings) StartScheduledDMTask(id string) error {
	if err := b.runtimeError(); err != nil {
		return err
	}
	if err := b.scheduledDMReady(); err != nil {
		return err
	}
	if b.scheduledDM.Runner == nil {
		return errors.New("scheduled DM runner is not configured")
	}
	ctx := b.rootContext()
	taskID := domain.ID(id)
	runnerWasRunning := b.scheduledDM.Runner.Running()
	if err := b.scheduledDM.Runner.Start(ctx); err != nil {
		return err
	}
	if err := b.scheduledDM.Service.Start(ctx, taskID); err != nil {
		if runnerWasRunning {
			return err
		}
		return errors.Join(err, b.scheduledDM.Runner.Stop(context.WithoutCancel(ctx)))
	}
	return nil
}

func (b *Bindings) StopScheduledDMTask(id string) error {
	if err := b.scheduledDMReady(); err != nil {
		return err
	}
	return b.scheduledDM.Service.Stop(b.rootContext(), domain.ID(id))
}

func (b *Bindings) CancelScheduledDMTask(id string) error {
	if err := b.scheduledDMReady(); err != nil {
		return err
	}
	return b.scheduledDM.Service.Cancel(b.rootContext(), domain.ID(id))
}

// ResolveScheduledDMRecipients checks username existence only. It never infers open or closed DMs.
func (b *Bindings) ResolveScheduledDMRecipients(id string) ([]ScheduledDMRecipientDTO, error) {
	if err := b.runtimeError(); err != nil {
		return nil, err
	}
	if err := b.scheduledDMReady(); err != nil {
		return nil, err
	}
	if b.scheduledDM.Resolver == nil {
		return nil, errors.New("scheduled DM resolver is not configured")
	}
	task, err := b.scheduledDMTask(b.rootContext(), domain.ID(id))
	if err != nil {
		return nil, err
	}
	accounts, err := b.scheduledDM.Accounts.List(b.rootContext())
	if err != nil {
		return nil, err
	}
	byID := make(map[domain.ID]domain.Account, len(accounts))
	for _, account := range accounts {
		byID[account.ID] = account
	}
	if len(task.AccountIDs) == 0 {
		return nil, errors.New("scheduled DM task has no assigned accounts")
	}

	checkedAt := time.Now().UTC()
	result := make([]ScheduledDMRecipientDTO, len(task.Recipients))
	for index, recipient := range task.Recipients {
		accountID := task.AccountIDs[index%len(task.AccountIDs)]
		result[index] = ScheduledDMRecipientDTO{Username: recipient.Username, AccountID: string(accountID), Status: string(domain.ScheduledDMRecipientStatusError), LastCheckedAt: checkedAt.Format(time.RFC3339Nano)}
		account, ok := byID[accountID]
		if !ok {
			result[index].LastError = "assigned_account_unavailable"
			continue
		}
		if err := b.scheduledDM.Resolver.ResolveUsername(b.rootContext(), account, recipient.Username); err != nil {
			if domain.IsPermanentDeliveryFailure(err) {
				result[index].Status = string(domain.ScheduledDMRecipientStatusInvalid)
				result[index].LastError = "recipient_invalid"
			} else {
				result[index].LastError = "recipient_resolution_failed"
			}
			continue
		}
		result[index].Status = string(domain.ScheduledDMRecipientStatusResolved)
		result[index].LastError = ""
	}
	return result, nil
}

func (b *Bindings) scheduledDMReady() error {
	if b.scheduledDM.Service == nil || b.scheduledDM.Tasks == nil || b.scheduledDM.Accounts == nil {
		return errors.New("scheduled DM service is not configured")
	}
	return nil
}

func (b *Bindings) scheduledDMTask(ctx context.Context, id domain.ID) (domain.ScheduledDMTask, error) {
	tasks, err := b.scheduledDM.Tasks.ListTasks(ctx)
	if err != nil {
		return domain.ScheduledDMTask{}, err
	}
	for _, task := range tasks {
		if task.ID == id {
			return task, nil
		}
	}
	return domain.ScheduledDMTask{}, errors.New("scheduled DM task not found")
}

func scheduledDMTaskDTO(task domain.ScheduledDMTask) ScheduledDMTaskDTO {
	result := ScheduledDMTaskDTO{
		ID: string(task.ID), MessageText: task.MessageText, Status: string(task.Status), StartAt: task.StartAt.UTC().Format(time.RFC3339Nano),
		Recurrence: string(task.Recurrence), MaxRuns: task.MaxRuns, CompletedRuns: task.CompletedRuns,
		AccountIDs: make([]string, len(task.AccountIDs)), Recipients: make([]ScheduledDMRecipientDTO, len(task.Recipients)),
		CreatedAt: task.CreatedAt.UTC().Format(time.RFC3339Nano), UpdatedAt: task.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
	if task.NextRunAt != nil {
		result.NextRunAt = task.NextRunAt.UTC().Format(time.RFC3339Nano)
	}
	for index, accountID := range task.AccountIDs {
		result.AccountIDs[index] = string(accountID)
	}
	for index, recipient := range task.Recipients {
		result.Recipients[index] = ScheduledDMRecipientDTO{
			Username: recipient.Username, Status: string(recipient.Status), LastError: recipient.LastError,
		}
		if len(task.AccountIDs) != 0 {
			result.Recipients[index].AccountID = string(task.AccountIDs[index%len(task.AccountIDs)])
		}
		if recipient.LastCheckedAt != nil {
			result.Recipients[index].LastCheckedAt = recipient.LastCheckedAt.UTC().Format(time.RFC3339Nano)
		}
	}
	return result
}
