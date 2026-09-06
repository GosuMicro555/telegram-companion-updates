package sqlite

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"telegram-companion/internal/domain"
)

// ScheduledDMRepository persists finite scheduled direct-message tasks. It has
// no Telegram dependency; callers explicitly claim and complete deliveries.
type ScheduledDMRepository struct {
	db         *sql.DB
	now        func() time.Time
	leaseToken func() (string, error)
}

func NewScheduledDMRepository(db *sql.DB, now func() time.Time) *ScheduledDMRepository {
	if now == nil {
		now = time.Now
	}
	return &ScheduledDMRepository{db: db, now: now, leaseToken: scheduledDMToken}
}

func (r *ScheduledDMRepository) ListTasks(ctx context.Context) ([]domain.ScheduledDMTask, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id,message_text,status,start_at,recurrence,max_runs,completed_runs,next_run_at,created_at,updated_at
		FROM scheduled_dm_tasks ORDER BY created_at,id`)
	if err != nil {
		return nil, fmt.Errorf("list scheduled DM tasks: %w", err)
	}
	defer rows.Close()

	tasks := make([]domain.ScheduledDMTask, 0)
	for rows.Next() {
		task, err := scanScheduledDMTask(rows)
		if err != nil {
			return nil, err
		}
		if task.Recipients, err = r.listRecipients(ctx, task.ID); err != nil {
			return nil, err
		}
		if task.AccountIDs, err = r.listAccountIDs(ctx, task.ID); err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate scheduled DM tasks: %w", err)
	}
	return tasks, nil
}

func (r *ScheduledDMRepository) SaveTask(ctx context.Context, task domain.ScheduledDMTask) error {
	if task.ID == "" || task.MessageText == "" || task.StartAt.IsZero() {
		return errors.New("scheduled DM task ID, message text, and start time are required")
	}
	interval, ok := domain.RecurrenceDuration(task.Recurrence)
	if !ok || task.MaxRuns < 1 || task.MaxRuns > 12 {
		return errors.New("valid scheduled DM recurrence and max runs are required")
	}
	if task.CompletedRuns < 0 || task.CompletedRuns > task.MaxRuns {
		return errors.New("scheduled DM completed runs are invalid")
	}
	now := r.now().UTC()
	if task.CreatedAt.IsZero() {
		task.CreatedAt = now
	}
	if task.UpdatedAt.IsZero() {
		task.UpdatedAt = now
	}
	if task.NextRunAt == nil {
		next := task.StartAt.UTC()
		task.NextRunAt = &next
	}
	// Saving or restoring a task must never start a sender implicitly.
	task.Status = domain.ScheduledDMStatusPaused

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin save scheduled DM task: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var intervalSeconds any
	if task.Recurrence != domain.ScheduledDMRecurrenceOnce {
		intervalSeconds = int64(interval.Seconds())
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO scheduled_dm_tasks
		(id,message_text,status,start_at,recurrence,interval_seconds,max_runs,completed_runs,next_run_at,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET message_text=excluded.message_text,status=excluded.status,start_at=excluded.start_at,
		recurrence=excluded.recurrence,interval_seconds=excluded.interval_seconds,max_runs=excluded.max_runs,
		completed_runs=excluded.completed_runs,next_run_at=excluded.next_run_at,updated_at=excluded.updated_at`,
		task.ID, task.MessageText, task.Status, formatTime(task.StartAt.UTC()), task.Recurrence, intervalSeconds,
		task.MaxRuns, task.CompletedRuns, formatTimePtr(task.NextRunAt), formatTime(task.CreatedAt.UTC()), formatTime(task.UpdatedAt.UTC())); err != nil {
		return fmt.Errorf("save scheduled DM task: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM scheduled_dm_recipients WHERE task_id=?`, task.ID); err != nil {
		return fmt.Errorf("replace scheduled DM recipients: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM scheduled_dm_accounts WHERE task_id=?`, task.ID); err != nil {
		return fmt.Errorf("replace scheduled DM accounts: %w", err)
	}
	for ordinal, recipient := range task.Recipients {
		status := recipient.Status
		if status == "" {
			status = domain.ScheduledDMRecipientStatusUnchecked
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO scheduled_dm_recipients
			(task_id,ordinal,username,peer_status,last_error,last_checked_at) VALUES (?,?,?,?,?,?)`,
			task.ID, ordinal, recipient.Username, status, recipient.LastError, formatTimePtr(recipient.LastCheckedAt)); err != nil {
			return fmt.Errorf("save scheduled DM recipient: %w", err)
		}
	}
	for ordinal, accountID := range task.AccountIDs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO scheduled_dm_accounts (task_id,ordinal,account_id) VALUES (?,?,?)`, task.ID, ordinal, accountID); err != nil {
			return fmt.Errorf("save scheduled DM account: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit scheduled DM task: %w", err)
	}
	return nil
}

func (r *ScheduledDMRepository) SetTaskStatus(ctx context.Context, taskID domain.ID, status domain.ScheduledDMStatus, at time.Time) error {
	result, err := r.db.ExecContext(ctx, `UPDATE scheduled_dm_tasks SET status=?,
		next_run_at=CASE WHEN ?='active' THEN COALESCE(next_run_at,start_at) ELSE next_run_at END,updated_at=? WHERE id=?`,
		status, status, formatTime(at.UTC()), taskID)
	if err != nil {
		return fmt.Errorf("set scheduled DM task status: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		if err != nil {
			return err
		}
		return errors.New("scheduled DM task not found")
	}
	return nil
}

func (r *ScheduledDMRepository) ClaimDueDelivery(ctx context.Context, now time.Time, leaseDuration time.Duration) (*domain.ScheduledDMDelivery, error) {
	if leaseDuration <= 0 {
		return nil, errors.New("scheduled DM lease duration must be positive")
	}
	now = now.UTC()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin scheduled DM claim: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := r.materializeDueRun(ctx, tx, now); err != nil {
		return nil, err
	}

	var id string
	err = tx.QueryRowContext(ctx, `SELECT d.id FROM scheduled_dm_deliveries d
		JOIN scheduled_dm_tasks t ON t.id=d.task_id
		JOIN scheduled_dm_runs run ON run.id=d.run_id
		WHERE t.status='active' AND d.next_attempt_at<=?
		AND (d.status='pending' OR (d.status='sending' AND d.lease_until IS NOT NULL AND d.lease_until<=?))
		ORDER BY run.scheduled_at,d.id LIMIT 1`, formatTime(now), formatTime(now)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit empty scheduled DM claim: %w", err)
		}
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("select due scheduled DM delivery: %w", err)
	}
	leaseToken, err := r.leaseToken()
	if err != nil {
		return nil, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE scheduled_dm_deliveries SET status='sending',attempted_at=?,lease_token=?,lease_until=?
		WHERE id=? AND (status='pending' OR (status='sending' AND lease_until IS NOT NULL AND lease_until<=?))`,
		formatTime(now), leaseToken, formatTime(now.Add(leaseDuration)), id, formatTime(now))
	if err != nil {
		return nil, fmt.Errorf("claim scheduled DM delivery: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		if err != nil {
			return nil, err
		}
		return nil, errors.New("scheduled DM delivery claim was lost")
	}
	delivery, err := scanScheduledDMDelivery(tx.QueryRowContext(ctx, `SELECT d.id,d.task_id,d.run_id,run.run_number,d.recipient,d.account_id,
		d.telegram_random_id,d.lease_token,d.status,d.attempted_at,d.completed_at,d.error_code
		FROM scheduled_dm_deliveries d JOIN scheduled_dm_runs run ON run.id=d.run_id WHERE d.id=?`, id))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit scheduled DM claim: %w", err)
	}
	return &delivery, nil
}

// CompleteDelivery marks one delivery terminal only for its current lease owner.
func (r *ScheduledDMRepository) CompleteDelivery(ctx context.Context, result domain.ScheduledDMDeliveryResult) error {
	if result.DeliveryID == "" || result.LeaseToken == "" || (result.Status != domain.ScheduledDMDeliveryStatusSent &&
		result.Status != domain.ScheduledDMDeliveryStatusClosed && result.Status != domain.ScheduledDMDeliveryStatusFailed) {
		return errors.New("scheduled DM terminal delivery result is required")
	}
	completedAt := r.now().UTC()
	if !result.CompletedAt.IsZero() {
		completedAt = result.CompletedAt.UTC()
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var taskID, runID string
	updateResult, err := tx.ExecContext(ctx, `UPDATE scheduled_dm_deliveries SET status=?,completed_at=?,error_code=?,lease_token='',lease_until=NULL
		WHERE id=? AND status='sending' AND lease_token=?`, result.Status, formatTime(completedAt), result.ErrorCode, result.DeliveryID, result.LeaseToken)
	if err != nil {
		return fmt.Errorf("complete scheduled DM delivery: %w", err)
	}
	if affected, err := updateResult.RowsAffected(); err != nil || affected != 1 {
		if err != nil {
			return err
		}
		return errors.New("scheduled DM delivery is not owned by the active lease")
	}
	if err := tx.QueryRowContext(ctx, `SELECT task_id,run_id FROM scheduled_dm_deliveries WHERE id=?`, result.DeliveryID).Scan(&taskID, &runID); err != nil {
		return err
	}
	recipientStatus := domain.ScheduledDMRecipientStatusError
	if result.Status == domain.ScheduledDMDeliveryStatusSent {
		recipientStatus = domain.ScheduledDMRecipientStatusOpen
	} else if result.Status == domain.ScheduledDMDeliveryStatusClosed {
		recipientStatus = domain.ScheduledDMRecipientStatusClosed
	} else if result.ErrorCode == "recipient_invalid" {
		recipientStatus = domain.ScheduledDMRecipientStatusInvalid
	}
	if _, err := tx.ExecContext(ctx, `UPDATE scheduled_dm_recipients SET peer_status=?,last_error=? WHERE task_id=? AND username=(
		SELECT recipient FROM scheduled_dm_deliveries WHERE id=?)`, recipientStatus, result.ErrorCode, taskID, result.DeliveryID); err != nil {
		return err
	}
	var remaining int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM scheduled_dm_deliveries WHERE run_id=? AND status NOT IN ('sent','closed','failed')`, runID).Scan(&remaining); err != nil {
		return err
	}
	if remaining == 0 {
		if err := r.completeRun(ctx, tx, runID, completedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *ScheduledDMRepository) DelayDelivery(ctx context.Context, deliveryID domain.ID, leaseToken, reason string, nextAttemptAt time.Time) error {
	if leaseToken == "" {
		return errors.New("scheduled DM delivery lease token is required")
	}
	result, err := r.db.ExecContext(ctx, `UPDATE scheduled_dm_deliveries SET status='pending',next_attempt_at=?,error_code=?,lease_token='',lease_until=NULL
		WHERE id=? AND status='sending' AND lease_token=?`, formatTime(nextAttemptAt.UTC()), reason, deliveryID, leaseToken)
	if err != nil {
		return fmt.Errorf("delay scheduled DM delivery: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		if err != nil {
			return err
		}
		return errors.New("scheduled DM delivery is not owned by the active lease")
	}
	return nil
}

func pauseActiveScheduledDMTasks(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `UPDATE scheduled_dm_tasks SET status='paused' WHERE status='active'`); err != nil {
		return fmt.Errorf("pause active scheduled DM tasks at startup: %w", err)
	}
	return nil
}

func (r *ScheduledDMRepository) materializeDueRun(ctx context.Context, tx *sql.Tx, now time.Time) error {
	var taskID, recurrenceRaw, scheduledAtRaw string
	var completedRuns, maxRuns int
	err := tx.QueryRowContext(ctx, `SELECT id,recurrence,completed_runs,max_runs,next_run_at FROM scheduled_dm_tasks
		WHERE status='active' AND next_run_at IS NOT NULL AND next_run_at<=? AND completed_runs<max_runs
		AND NOT EXISTS (SELECT 1 FROM scheduled_dm_runs run WHERE run.task_id=scheduled_dm_tasks.id AND run.run_number=scheduled_dm_tasks.completed_runs+1)
		ORDER BY next_run_at,id LIMIT 1`, formatTime(now)).Scan(&taskID, &recurrenceRaw, &completedRuns, &maxRuns, &scheduledAtRaw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("select due scheduled DM task: %w", err)
	}
	if completedRuns >= maxRuns {
		return nil
	}
	scheduledAt, err := parseTime(scheduledAtRaw)
	if err != nil {
		return err
	}
	if _, ok := domain.RecurrenceDuration(domain.ScheduledDMRecurrence(recurrenceRaw)); !ok {
		return errors.New("stored scheduled DM recurrence is invalid")
	}
	runNumber := completedRuns + 1
	runID := domain.ID(fmt.Sprintf("%s:run:%d", taskID, runNumber))
	if _, err := tx.ExecContext(ctx, `INSERT INTO scheduled_dm_runs (id,task_id,run_number,scheduled_at,created_at) VALUES (?,?,?,?,?)`,
		runID, taskID, runNumber, formatTime(scheduledAt), formatTime(now)); err != nil {
		return fmt.Errorf("create scheduled DM run: %w", err)
	}
	recipients, err := loadMaterializedRecipients(ctx, tx, domain.ID(taskID))
	if err != nil {
		return err
	}
	accounts, err := loadMaterializedAccounts(ctx, tx, domain.ID(taskID))
	if err != nil {
		return err
	}
	if len(recipients) == 0 || len(accounts) == 0 {
		return errors.New("scheduled DM task requires recipients and accounts")
	}
	for ordinal, recipient := range recipients {
		randomID, err := scheduledDMRandomID()
		if err != nil {
			return err
		}
		deliveryID := domain.ID(fmt.Sprintf("%s:delivery:%d", runID, ordinal))
		if _, err := tx.ExecContext(ctx, `INSERT INTO scheduled_dm_deliveries
			(id,task_id,run_id,recipient,account_id,telegram_random_id,status,next_attempt_at) VALUES (?,?,?,?,?,?, 'pending',?)`,
			deliveryID, taskID, runID, recipient, accounts[ordinal%len(accounts)], randomID, formatTime(scheduledAt)); err != nil {
			return fmt.Errorf("create scheduled DM delivery: %w", err)
		}
	}
	return nil
}

func (r *ScheduledDMRepository) completeRun(ctx context.Context, tx *sql.Tx, runID string, completedAt time.Time) error {
	var taskID, recurrenceRaw, scheduledAtRaw string
	var runNumber, completedRuns, maxRuns int
	err := tx.QueryRowContext(ctx, `SELECT run.task_id,run.run_number,run.scheduled_at,task.recurrence,task.completed_runs,task.max_runs
		FROM scheduled_dm_runs run JOIN scheduled_dm_tasks task ON task.id=run.task_id WHERE run.id=? AND run.completed_at IS NULL`, runID).
		Scan(&taskID, &runNumber, &scheduledAtRaw, &recurrenceRaw, &completedRuns, &maxRuns)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	scheduledAt, err := parseTime(scheduledAtRaw)
	if err != nil {
		return err
	}
	duration, ok := domain.RecurrenceDuration(domain.ScheduledDMRecurrence(recurrenceRaw))
	if !ok {
		return errors.New("stored scheduled DM recurrence is invalid")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE scheduled_dm_runs SET completed_at=? WHERE id=?`, formatTime(completedAt), runID); err != nil {
		return err
	}
	if runNumber >= maxRuns || duration == 0 {
		_, err = tx.ExecContext(ctx, `UPDATE scheduled_dm_tasks SET completed_runs=?,status='completed',next_run_at=NULL,updated_at=? WHERE id=?`,
			runNumber, formatTime(completedAt), taskID)
		return err
	}
	nextRunAt := scheduledAt.Add(duration)
	_, err = tx.ExecContext(ctx, `UPDATE scheduled_dm_tasks SET completed_runs=?,next_run_at=?,updated_at=? WHERE id=?`,
		runNumber, formatTime(nextRunAt), formatTime(completedAt), taskID)
	return err
}

func (r *ScheduledDMRepository) listRecipients(ctx context.Context, taskID domain.ID) ([]domain.ScheduledDMRecipient, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT ordinal,username,peer_status,last_error,last_checked_at FROM scheduled_dm_recipients WHERE task_id=? ORDER BY ordinal`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	recipients := make([]domain.ScheduledDMRecipient, 0)
	for rows.Next() {
		var recipient domain.ScheduledDMRecipient
		var checkedAt sql.NullString
		if err := rows.Scan(&recipient.Ordinal, &recipient.Username, &recipient.Status, &recipient.LastError, &checkedAt); err != nil {
			return nil, err
		}
		recipient.TaskID = taskID
		if checkedAt.Valid {
			value, err := parseTime(checkedAt.String)
			if err != nil {
				return nil, err
			}
			recipient.LastCheckedAt = &value
		}
		recipients = append(recipients, recipient)
	}
	return recipients, rows.Err()
}

func (r *ScheduledDMRepository) listAccountIDs(ctx context.Context, taskID domain.ID) ([]domain.ID, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT account_id FROM scheduled_dm_accounts WHERE task_id=? ORDER BY ordinal`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	accounts := make([]domain.ID, 0)
	for rows.Next() {
		var accountID domain.ID
		if err := rows.Scan(&accountID); err != nil {
			return nil, err
		}
		accounts = append(accounts, accountID)
	}
	return accounts, rows.Err()
}

type scheduledDMTaskScanner interface {
	Scan(...any) error
}

func scanScheduledDMTask(row scheduledDMTaskScanner) (domain.ScheduledDMTask, error) {
	var task domain.ScheduledDMTask
	var startAt, createdAt, updatedAt string
	var nextRunAt sql.NullString
	if err := row.Scan(&task.ID, &task.MessageText, &task.Status, &startAt, &task.Recurrence, &task.MaxRuns, &task.CompletedRuns, &nextRunAt, &createdAt, &updatedAt); err != nil {
		return domain.ScheduledDMTask{}, err
	}
	var err error
	if task.StartAt, err = parseTime(startAt); err != nil {
		return domain.ScheduledDMTask{}, err
	}
	if task.CreatedAt, err = parseTime(createdAt); err != nil {
		return domain.ScheduledDMTask{}, err
	}
	if task.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return domain.ScheduledDMTask{}, err
	}
	if nextRunAt.Valid {
		value, err := parseTime(nextRunAt.String)
		if err != nil {
			return domain.ScheduledDMTask{}, err
		}
		task.NextRunAt = &value
	}
	return task, nil
}

type scheduledDMDeliveryScanner interface {
	Scan(...any) error
}

func scanScheduledDMDelivery(row scheduledDMDeliveryScanner) (domain.ScheduledDMDelivery, error) {
	var delivery domain.ScheduledDMDelivery
	var attemptedAt, completedAt sql.NullString
	if err := row.Scan(&delivery.ID, &delivery.TaskID, &delivery.RunID, &delivery.RunNumber, &delivery.Recipient,
		&delivery.AccountID, &delivery.TelegramRandomID, &delivery.LeaseToken, &delivery.Status, &attemptedAt, &completedAt, &delivery.ErrorCode); err != nil {
		return domain.ScheduledDMDelivery{}, err
	}
	if attemptedAt.Valid {
		value, err := parseTime(attemptedAt.String)
		if err != nil {
			return domain.ScheduledDMDelivery{}, err
		}
		delivery.AttemptedAt = &value
	}
	if completedAt.Valid {
		value, err := parseTime(completedAt.String)
		if err != nil {
			return domain.ScheduledDMDelivery{}, err
		}
		delivery.CompletedAt = &value
	}
	return delivery, nil
}

func loadMaterializedRecipients(ctx context.Context, tx *sql.Tx, taskID domain.ID) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT username FROM scheduled_dm_recipients WHERE task_id=? ORDER BY ordinal`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var recipients []string
	for rows.Next() {
		var recipient string
		if err := rows.Scan(&recipient); err != nil {
			return nil, err
		}
		recipients = append(recipients, recipient)
	}
	return recipients, rows.Err()
}

func loadMaterializedAccounts(ctx context.Context, tx *sql.Tx, taskID domain.ID) ([]domain.ID, error) {
	rows, err := tx.QueryContext(ctx, `SELECT account_id FROM scheduled_dm_accounts WHERE task_id=? ORDER BY ordinal`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var accounts []domain.ID
	for rows.Next() {
		var accountID domain.ID
		if err := rows.Scan(&accountID); err != nil {
			return nil, err
		}
		accounts = append(accounts, accountID)
	}
	return accounts, rows.Err()
}

func scheduledDMRandomID() (int64, error) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return 0, fmt.Errorf("generate scheduled DM random ID: %w", err)
	}
	value := int64(binary.BigEndian.Uint64(raw[:]) & uint64(^uint64(0)>>1))
	if value == 0 {
		return 1, nil
	}
	return value, nil
}

func scheduledDMToken() (string, error) {
	value, err := scheduledDMRandomID()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", value), nil
}
