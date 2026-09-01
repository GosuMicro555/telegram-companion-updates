package usecase

import (
	"context"
	"errors"
	"time"

	"telegram-companion/internal/domain"
)

type ScheduledDMSender interface {
	ResolveUsername(context.Context, domain.Account, string) error
	SendScheduledPrivateMessage(context.Context, domain.Account, domain.ScheduledDMDelivery, string) error
}

type ScheduledDMRunner struct {
	repository domain.ScheduledDMRepository
	accounts   domain.AccountRepository
	sender     ScheduledDMSender
	now        func() time.Time
	interval   time.Duration
}

type scheduledDMAccountFloodWaitError struct{ until time.Time }

func (e *scheduledDMAccountFloodWaitError) Error() string {
	return "assigned scheduled DM account is in FloodWait"
}

func NewScheduledDMRunner(repository domain.ScheduledDMRepository, accounts domain.AccountRepository, sender ScheduledDMSender, now func() time.Time) *ScheduledDMRunner {
	if now == nil {
		now = time.Now
	}
	return &ScheduledDMRunner{repository: repository, accounts: accounts, sender: sender, now: now, interval: time.Second}
}

func (r *ScheduledDMRunner) RunOnce(ctx context.Context) error {
	if r == nil || r.repository == nil || r.accounts == nil || r.sender == nil {
		return errors.New("scheduled DM runner is not configured")
	}
	now := r.now().UTC()
	delivery, err := r.repository.ClaimDueDelivery(ctx, now, time.Minute)
	if err != nil || delivery == nil {
		return err
	}
	task, err := r.task(ctx, delivery.TaskID)
	if err != nil {
		return err
	}
	account, err := r.assignedAccount(ctx, delivery.AccountID, now)
	if err != nil {
		var floodWait *scheduledDMAccountFloodWaitError
		if errors.As(err, &floodWait) {
			return r.repository.DelayDelivery(ctx, delivery.ID, delivery.LeaseToken, "assigned_account_unavailable", floodWait.until)
		}
		return r.delay(ctx, *delivery, now, "assigned_account_unavailable", err)
	}

	if err := r.sender.ResolveUsername(ctx, account, delivery.Recipient); err != nil {
		return r.handleDeliveryError(ctx, *delivery, now, err, "recipient_invalid")
	}
	if err := r.sender.SendScheduledPrivateMessage(ctx, account, *delivery, task.MessageText); err != nil {
		return r.handleDeliveryError(ctx, *delivery, now, err, "send_failed")
	}
	return r.repository.CompleteDelivery(ctx, domain.ScheduledDMDeliveryResult{
		DeliveryID: delivery.ID, LeaseToken: delivery.LeaseToken, Status: domain.ScheduledDMDeliveryStatusSent, CompletedAt: now,
	})
}

func (r *ScheduledDMRunner) Run(ctx context.Context) error {
	if err := r.RunOnce(ctx); err != nil && ctx.Err() == nil {
		return err
	}
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := r.RunOnce(ctx); err != nil && ctx.Err() == nil {
				return err
			}
		}
	}
}

func (r *ScheduledDMRunner) task(ctx context.Context, taskID domain.ID) (domain.ScheduledDMTask, error) {
	tasks, err := r.repository.ListTasks(ctx)
	if err != nil {
		return domain.ScheduledDMTask{}, err
	}
	for _, task := range tasks {
		if task.ID == taskID {
			return task, nil
		}
	}
	return domain.ScheduledDMTask{}, errors.New("scheduled DM task not found")
}

func (r *ScheduledDMRunner) assignedAccount(ctx context.Context, accountID domain.ID, now time.Time) (domain.Account, error) {
	accounts, err := r.accounts.ListActive(ctx)
	if err != nil {
		return domain.Account{}, err
	}
	for _, account := range accounts {
		if account.ID == accountID && account.Role == domain.AccountRoleSpammer && account.Eligible() {
			return account, nil
		}
	}
	accounts, err = r.accounts.List(ctx)
	if err != nil {
		return domain.Account{}, err
	}
	for _, account := range accounts {
		if account.ID == accountID && account.Role == domain.AccountRoleSpammer && account.Status == domain.AccountFloodWait && account.FloodWaitUntil != nil && account.FloodWaitUntil.After(now) {
			return domain.Account{}, &scheduledDMAccountFloodWaitError{until: account.FloodWaitUntil.UTC()}
		}
	}
	return domain.Account{}, errors.New("assigned scheduled DM account is unavailable")
}

func (r *ScheduledDMRunner) handleDeliveryError(ctx context.Context, delivery domain.ScheduledDMDelivery, now time.Time, err error, permanentCode string) error {
	if domain.IsPrivateMessageClosed(err) {
		return r.repository.CompleteDelivery(ctx, domain.ScheduledDMDeliveryResult{
			DeliveryID: delivery.ID, LeaseToken: delivery.LeaseToken, Status: domain.ScheduledDMDeliveryStatusClosed, CompletedAt: now, ErrorCode: "recipient_closed",
		})
	}
	if domain.IsPermanentDeliveryFailure(err) {
		return r.repository.CompleteDelivery(ctx, domain.ScheduledDMDeliveryResult{
			DeliveryID: delivery.ID, LeaseToken: delivery.LeaseToken, Status: domain.ScheduledDMDeliveryStatusFailed, CompletedAt: now, ErrorCode: permanentCode,
		})
	}
	return r.delay(ctx, delivery, now, "send_failed", err)
}

func (r *ScheduledDMRunner) delay(ctx context.Context, delivery domain.ScheduledDMDelivery, now time.Time, reason string, err error) error {
	next := now.Add(2 * time.Second)
	var retry interface{ RetryAfter() time.Duration }
	if errors.As(err, &retry) && retry.RetryAfter() > 0 {
		next = now.Add(retry.RetryAfter())
	}
	return r.repository.DelayDelivery(ctx, delivery.ID, delivery.LeaseToken, reason, next)
}
