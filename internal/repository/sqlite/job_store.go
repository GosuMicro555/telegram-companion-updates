package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"telegram-companion/internal/domain"
)

type JobRepository struct {
	db  *sql.DB
	now func() time.Time
}

var _ domain.KeywordResponseEnqueuer = (*JobRepository)(nil)

const maxDeliveryAttempts = 8

func NewJobRepository(db *sql.DB, now func() time.Time) *JobRepository {
	if now == nil {
		now = time.Now
	}
	return &JobRepository{db: db, now: now}
}

func (s *ProductionStore) Jobs() *JobRepository { return NewJobRepository(s.db, time.Now) }

// ReleaseGroupRestDelays makes only rest-paused jobs eligible when the global rest switch is turned off.
func (s *ProductionStore) ReleaseGroupRestDelays(ctx context.Context) error {
	return s.Jobs().ReleaseGroupRestDelays(ctx)
}

func (r *JobRepository) Enqueue(ctx context.Context, job domain.OutgoingMessageJob) error {
	job, err := r.prepareEnqueue(job)
	if err != nil {
		return err
	}
	if _, err := r.insertJob(ctx, r.db, job); err != nil {
		return fmt.Errorf("enqueue outgoing job: %w", err)
	}
	return nil
}

func (r *JobRepository) EnqueueKeywordResponse(ctx context.Context, job domain.OutgoingMessageJob, draft domain.LiveDeliveryDraft) error {
	if job.Type != domain.JobKeywordResponse {
		return errors.New("keyword response job type is required")
	}
	if draft.SourceMessage == "" || draft.TriggerSnapshot == "" || draft.TriggeredAt.IsZero() {
		return errors.New("live delivery source message, trigger snapshot, and timestamp are required")
	}
	job, err := r.prepareEnqueue(job)
	if err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin keyword response enqueue: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	inserted, err := r.insertJob(ctx, tx, job)
	if err != nil {
		return fmt.Errorf("enqueue outgoing keyword response: %w", err)
	}
	if inserted == 0 {
		matches, err := matchingKeywordJobIdentity(ctx, tx, job)
		if err != nil {
			return fmt.Errorf("load existing outgoing keyword response: %w", err)
		}
		if !matches {
			return errors.New("existing outgoing job does not match keyword response identity")
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO live_delivery_history
		(id,job_id,source_message,trigger_canonical_id,trigger_snapshot,triggered_at)
		VALUES (?,?,?,(SELECT id FROM canonical_keywords WHERE id=?),?,?) ON CONFLICT(job_id) DO NOTHING`,
		domain.ID(string(job.ID)+":live"), job.ID, draft.SourceMessage, draft.TriggerCanonicalID,
		draft.TriggerSnapshot, formatTime(draft.TriggeredAt.UTC())); err != nil {
		return fmt.Errorf("enqueue live delivery audit: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit keyword response enqueue: %w", err)
	}
	return nil
}

func (r *JobRepository) prepareEnqueue(job domain.OutgoingMessageJob) (domain.OutgoingMessageJob, error) {
	if job.ID == "" || (job.Type != domain.JobPublicReply && job.Type != domain.JobPrivateMessage && job.Type != domain.JobKeywordResponse) {
		return domain.OutgoingMessageJob{}, errors.New("valid outgoing job ID and type are required")
	}
	now := r.now().UTC()
	if job.Status == "" {
		job.Status = "queued"
	}
	if job.NextAttemptAt.IsZero() {
		job.NextAttemptAt = now
	}
	if job.CreatedAt.IsZero() {
		job.CreatedAt = now
	}
	return job, nil
}

type jobExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func (r *JobRepository) insertJob(ctx context.Context, executor jobExecutor, job domain.OutgoingMessageJob) (int64, error) {
	result, err := executor.ExecContext(ctx, `INSERT INTO outgoing_message_jobs
		(id,type,account_id,channel_id,rule_id,target_telegram_id,reply_to_message_id,text,allow_private,keyword_delivery_mode,fallback_to_public,status,attempts,next_attempt_at,created_at,last_error)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO NOTHING`,
		job.ID, job.Type, job.AccountID, job.ChannelID, job.RuleID, job.TargetTelegramID, job.ReplyToMessageID,
		job.Text, boolInt(job.AllowPrivate), job.KeywordDeliveryMode, boolInt(job.FallbackToPublic), job.Status, job.Attempts, formatTime(job.NextAttemptAt), formatTime(job.CreatedAt), "")
	if err != nil {
		return 0, err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	return inserted, nil
}

func matchingKeywordJobIdentity(ctx context.Context, tx *sql.Tx, job domain.OutgoingMessageJob) (bool, error) {
	var storedType string
	var channelID, ruleID, targetTelegramID, replyToMessageID string
	var deliveryMode domain.KeywordDeliveryMode
	var fallbackToPublic bool
	if err := tx.QueryRowContext(ctx, `SELECT type,channel_id,rule_id,target_telegram_id,reply_to_message_id,keyword_delivery_mode,fallback_to_public
		FROM outgoing_message_jobs WHERE id=?`, job.ID).Scan(
		&storedType, &channelID, &ruleID, &targetTelegramID, &replyToMessageID, &deliveryMode, &fallbackToPublic,
	); err != nil {
		return false, err
	}
	return storedType == string(job.Type) &&
		channelID == string(job.ChannelID) &&
		ruleID == string(job.RuleID) &&
		targetTelegramID == job.TargetTelegramID &&
		replyToMessageID == job.ReplyToMessageID &&
		deliveryMode == job.KeywordDeliveryMode &&
		fallbackToPublic == job.FallbackToPublic, nil
}

func (r *JobRepository) NextDue(ctx context.Context) (*domain.OutgoingMessageJob, error) {
	row := r.db.QueryRowContext(ctx, `SELECT id,type,account_id,channel_id,rule_id,target_telegram_id,
		reply_to_message_id,text,allow_private,keyword_delivery_mode,fallback_to_public,status,attempts,next_attempt_at,created_at
		FROM outgoing_message_jobs AS jobs
		WHERE jobs.status IN ('queued','delayed') AND jobs.next_attempt_at<=?
		AND (jobs.lease_token='' OR jobs.lease_until IS NULL OR jobs.lease_until<=?)
		AND (
			jobs.type='private_message'
			OR EXISTS(SELECT 1 FROM outbound_channels
				WHERE id=jobs.channel_id AND active=1 AND removal_requested_at IS NULL)
			OR EXISTS(SELECT 1 FROM scout_chats
				WHERE id=jobs.channel_id AND active=1 AND removal_requested_at IS NULL)
		)
		ORDER BY next_attempt_at,created_at,id LIMIT 1`, formatTime(r.now().UTC()), formatTime(r.now().UTC()))
	var job domain.OutgoingMessageJob
	var id, jobType, channelID, ruleID, nextAttemptAt, createdAt string
	var accountID sql.NullString
	if err := row.Scan(&id, &jobType, &accountID, &channelID, &ruleID, &job.TargetTelegramID,
		&job.ReplyToMessageID, &job.Text, &job.AllowPrivate, &job.KeywordDeliveryMode, &job.FallbackToPublic, &job.Status, &job.Attempts, &nextAttemptAt, &createdAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("load next outgoing job: %w", err)
	}
	job.ID = domain.ID(id)
	job.Type = domain.JobType(jobType)
	job.ChannelID = domain.ID(channelID)
	job.RuleID = domain.ID(ruleID)
	if accountID.Valid {
		value := domain.ID(accountID.String)
		job.AccountID = &value
	}
	var err error
	if job.NextAttemptAt, err = parseTime(nextAttemptAt); err != nil {
		return nil, err
	}
	if job.CreatedAt, err = parseTime(createdAt); err != nil {
		return nil, err
	}
	return &job, nil
}

func (r *JobRepository) CanDispatch(ctx context.Context, jobID, accountID domain.ID) (bool, error) {
	if jobID == "" || accountID == "" {
		return false, errors.New("outgoing job and account IDs are required")
	}
	var allowed int
	err := r.db.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM outgoing_message_jobs AS jobs
		WHERE jobs.id=? AND jobs.account_id=?
		AND jobs.status IN ('queued','delayed')
		AND (
			jobs.type='private_message'
			OR EXISTS(SELECT 1 FROM outbound_channels WHERE id=jobs.channel_id AND active=1 AND removal_requested_at IS NULL)
			OR EXISTS(SELECT 1 FROM scout_chats WHERE id=jobs.channel_id AND active=1 AND removal_requested_at IS NULL)
		)
	)`, jobID, accountID).Scan(&allowed)
	if err != nil {
		return false, fmt.Errorf("check outgoing job dispatch: %w", err)
	}
	return allowed == 1, nil
}

func (r *JobRepository) Acquire(ctx context.Context, jobID, accountID domain.ID, token string, now, leaseUntil time.Time) (domain.ID, bool, error) {
	if jobID == "" || accountID == "" || token == "" || !leaseUntil.After(now) {
		return "", false, errors.New("valid outgoing job lease is required")
	}
	result, err := r.db.ExecContext(ctx, `UPDATE outgoing_message_jobs
		SET account_id=COALESCE(account_id,?),lease_token=?,lease_until=?
		WHERE id=? AND status IN ('queued','delayed')
		AND (account_id IS NULL OR account_id=?)
		AND (lease_token='' OR lease_until IS NULL OR lease_until<=?)`,
		accountID, token, formatTime(leaseUntil), jobID, accountID, formatTime(now))
	if err != nil {
		return "", false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return "", false, err
	}
	var claimed, status string
	if err := r.db.QueryRowContext(ctx, `SELECT COALESCE(account_id,''),status FROM outgoing_message_jobs WHERE id=?`, jobID).Scan(&claimed, &status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, errors.New("outgoing job not found")
		}
		return "", false, err
	}
	if status != "queued" && status != "delayed" {
		return domain.ID(claimed), false, nil
	}
	return domain.ID(claimed), affected == 1, nil
}

func (r *JobRepository) Claim(ctx context.Context, jobID, accountID domain.ID) (domain.ID, error) {
	if jobID == "" || accountID == "" {
		return "", errors.New("outgoing job and account IDs are required")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `UPDATE outgoing_message_jobs SET account_id=?
		WHERE id=? AND status IN ('queued','delayed') AND account_id IS NULL`, accountID, jobID); err != nil {
		return "", err
	}
	var claimed, status string
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(account_id,''),status FROM outgoing_message_jobs WHERE id=?`, jobID).Scan(&claimed, &status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", errors.New("outgoing job not found")
		}
		return "", err
	}
	if status != "queued" && status != "delayed" {
		return "", errors.New("outgoing job is not pending")
	}
	if claimed == "" {
		return "", errors.New("outgoing job was not claimed")
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return domain.ID(claimed), nil
}

func (r *JobRepository) Complete(ctx context.Context, jobID domain.ID, event domain.OutgoingMessageEvent) (bool, error) {
	return r.complete(ctx, jobID, "", event)
}

func (r *JobRepository) CompleteLease(ctx context.Context, jobID domain.ID, token string, event domain.OutgoingMessageEvent) (bool, error) {
	if token == "" {
		return false, errors.New("outgoing job lease token is required")
	}
	return r.complete(ctx, jobID, token, event)
}

func (r *JobRepository) complete(ctx context.Context, jobID domain.ID, token string, event domain.OutgoingMessageEvent) (bool, error) {
	if jobID == "" || event.AccountID == "" {
		return false, errors.New("outgoing job and account IDs are required")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	affected, err := markPendingJobDone(ctx, tx, jobID, event.AccountID, token)
	if err != nil {
		return false, err
	}
	if affected == 0 {
		var status, accountID string
		if err := tx.QueryRowContext(ctx, `SELECT status,COALESCE(account_id,'') FROM outgoing_message_jobs WHERE id=?`, jobID).Scan(&status, &accountID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return false, errors.New("outgoing job not found")
			}
			return false, err
		}
		if status == "done" && domain.ID(accountID) == event.AccountID {
			return false, tx.Commit()
		}
		return false, errors.New("outgoing job is not pending for claimed lease")
	}
	if affected != 1 {
		return false, errors.New("unexpected outgoing job completion count")
	}
	if err := recordSuccessfulDelivery(ctx, tx, jobID, event, false); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (r *JobRepository) RecordPrivateClosed(ctx context.Context, jobID, accountID domain.ID, closedAt time.Time) error {
	if jobID == "" || accountID == "" {
		return errors.New("outgoing job and account IDs are required")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var deliveryMode domain.KeywordDeliveryMode
	var fallbackToPublic bool
	if err := tx.QueryRowContext(ctx, `SELECT keyword_delivery_mode,fallback_to_public
		FROM outgoing_message_jobs WHERE id=? AND account_id=?`, jobID, accountID).Scan(&deliveryMode, &fallbackToPublic); err != nil {
		return err
	}
	terminalPrivateFailure := deliveryMode == domain.KeywordDeliveryModePrivate && !fallbackToPublic

	eventID := domain.ID(string(jobID) + ":private_closed")
	result, err := tx.ExecContext(ctx, `INSERT INTO outgoing_message_events
		(id,job_id,account_id,channel_id,type,success,error_code,created_at)
		SELECT ?,id,account_id,channel_id,'private_message',0,'private_message_closed',?
		FROM outgoing_message_jobs
		WHERE id=? AND account_id=? AND type='keyword_response'
		AND status IN ('queued','delayed') AND (allow_private=1 OR keyword_delivery_mode='private')
		ON CONFLICT(id) DO NOTHING`, eventID, formatTime(closedAt), jobID, accountID)
	if err != nil {
		return err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if inserted == 0 {
		identical, err := isPrivateClosedEvent(ctx, tx, eventID, jobID, accountID)
		if err != nil {
			return err
		}
		if identical {
			return tx.Commit()
		}
		return errors.New("outgoing keyword response is not pending for private delivery")
	}
	if inserted != 1 {
		return errors.New("unexpected private-closed event count")
	}

	accountResult, err := tx.ExecContext(ctx, `UPDATE accounts
		SET private_messages_closed=private_messages_closed+1,next_delivery='public'
		WHERE id=?`, accountID)
	if err != nil {
		return err
	}
	if err := requireSingleRow(accountResult, "outbound account not found"); err != nil {
		return err
	}
	jobResult, err := tx.ExecContext(ctx, `UPDATE outgoing_message_jobs
		SET allow_private=0,
			keyword_delivery_mode=CASE WHEN keyword_delivery_mode='private' AND fallback_to_public=1 THEN 'comments' ELSE keyword_delivery_mode END,
			fallback_to_public=0,
			status=CASE WHEN ? THEN 'done' ELSE status END,
			lease_token=CASE WHEN ? THEN '' ELSE lease_token END,
			lease_until=CASE WHEN ? THEN NULL ELSE lease_until END,
			last_error='private_message_closed'
		WHERE id=? AND account_id=? AND type='keyword_response'
		AND status IN ('queued','delayed') AND (allow_private=1 OR keyword_delivery_mode='private')`, terminalPrivateFailure, terminalPrivateFailure, terminalPrivateFailure, jobID, accountID)
	if err != nil {
		return err
	}
	if err := requireSingleRow(jobResult, "outgoing keyword response is not pending for private delivery"); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE live_delivery_history
		SET delivery_type=CASE WHEN ? THEN 'private_message' ELSE delivery_type END,
			account_id=CASE WHEN ? THEN ? ELSE account_id END,
			final_status=CASE WHEN ? THEN 'not_delivered' ELSE final_status END,
			error_code='private_message_closed',
			finalized_at=CASE WHEN ? THEN ? ELSE finalized_at END
		WHERE job_id=? AND final_status IS NULL`, terminalPrivateFailure, terminalPrivateFailure, accountID, terminalPrivateFailure, terminalPrivateFailure, formatTime(closedAt.UTC()), jobID); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *JobRepository) CompleteKeywordResponse(ctx context.Context, outcome domain.KeywordDeliveryOutcome) (bool, error) {
	if outcome.JobID == "" || outcome.AccountID == "" {
		return false, errors.New("outgoing job and account IDs are required")
	}
	if outcome.Type != domain.JobPublicReply && outcome.Type != domain.JobPrivateMessage {
		return false, errors.New("keyword delivery type must be public_reply or private_message")
	}
	if outcome.AdvanceTo != nil && *outcome.AdvanceTo != domain.DeliveryTargetPrivate && *outcome.AdvanceTo != domain.DeliveryTargetPublic {
		return false, errors.New("invalid next delivery target")
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	storedChannelID, affected, err := markKeywordResponseDone(ctx, tx, outcome)
	if err != nil {
		return false, err
	}
	eventID := domain.ID(string(outcome.JobID) + ":" + string(outcome.Type))
	if affected == 0 {
		identical, err := isCompletedKeywordDelivery(ctx, tx, outcome, eventID)
		if err != nil {
			return false, err
		}
		if identical {
			return false, tx.Commit()
		}
		return false, errors.New("outgoing keyword response is not pending for claimed account")
	}
	if affected != 1 {
		return false, errors.New("unexpected outgoing job completion count")
	}
	event := domain.OutgoingMessageEvent{
		ID: eventID, JobID: outcome.JobID, AccountID: outcome.AccountID,
		ChannelID: storedChannelID, Type: outcome.Type, Success: true,
		CreatedAt: outcome.CompletedAt,
	}
	if err := recordSuccessfulDelivery(ctx, tx, outcome.JobID, event, true); err != nil {
		return false, err
	}
	if err := finalizeKeywordSuccess(ctx, tx, outcome); err != nil {
		return false, err
	}
	if outcome.AdvanceTo != nil {
		result, err := tx.ExecContext(ctx, `UPDATE accounts SET next_delivery=? WHERE id=?`, *outcome.AdvanceTo, outcome.AccountID)
		if err != nil {
			return false, err
		}
		if err := requireSingleRow(result, "outbound account not found"); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func finalizeKeywordSuccess(ctx context.Context, tx *sql.Tx, outcome domain.KeywordDeliveryOutcome) error {
	accountTitle := strings.TrimSpace(outcome.AccountTitleSnapshot)
	if accountTitle == "" {
		accountTitle = string(outcome.AccountID)
	}
	result, err := tx.ExecContext(ctx, `UPDATE live_delivery_history
		SET delivery_type=?,account_id=?,account_title_snapshot=?,final_status='successful',
			error_code=CASE WHEN error_code='private_message_closed' THEN error_code ELSE '' END,finalized_at=?
		WHERE job_id=? AND final_status IS NULL`,
		outcome.Type, outcome.AccountID, accountTitle, formatTime(outcome.CompletedAt.UTC()), outcome.JobID)
	if err != nil {
		return err
	}
	_, err = result.RowsAffected()
	return err
}

func (r *JobRepository) DelayKeywordFailure(ctx context.Context, failure domain.KeywordDeliveryFailure) error {
	if failure.JobID == "" || failure.ErrorCode == "" {
		return errors.New("outgoing keyword job and error code are required")
	}
	if failure.Type != nil && *failure.Type != domain.JobPublicReply && *failure.Type != domain.JobPrivateMessage {
		return errors.New("keyword delivery type must be public_reply or private_message")
	}
	failedAt := failure.FailedAt.UTC()
	if failedAt.IsZero() {
		failedAt = r.now().UTC()
	}
	nextAttemptAt := failure.NextAttemptAt.UTC()
	if nextAttemptAt.IsZero() {
		nextAttemptAt = failedAt.Add(2 * time.Second)
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var attempts int
	err = tx.QueryRowContext(ctx, `UPDATE outgoing_message_jobs
		SET status=CASE WHEN attempts+1>=? THEN 'done' ELSE 'delayed' END,
		attempts=attempts+1,next_attempt_at=?,last_error=?,lease_token='',lease_until=NULL,
		dead_lettered_at=CASE WHEN attempts+1>=? THEN ? ELSE dead_lettered_at END
		WHERE id=? AND type='keyword_response' AND status IN ('queued','delayed')
		RETURNING attempts`, maxDeliveryAttempts, formatTime(nextAttemptAt), failure.ErrorCode, maxDeliveryAttempts, formatTime(failedAt), failure.JobID).Scan(&attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return r.completedKeywordFailure(ctx, tx, failure)
	}
	if err != nil {
		return fmt.Errorf("delay keyword response: %w", err)
	}
	if err := persistKeywordFailureMetadata(ctx, tx, failure); err != nil {
		return err
	}
	if attempts >= maxDeliveryAttempts {
		if err := finalizeKeywordFailure(ctx, tx, failure, failedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *JobRepository) completedKeywordFailure(ctx context.Context, tx *sql.Tx, failure domain.KeywordDeliveryFailure) error {
	var status, finalStatus string
	err := tx.QueryRowContext(ctx, `SELECT j.status,COALESCE(h.final_status,'')
		FROM outgoing_message_jobs j LEFT JOIN live_delivery_history h ON h.job_id=j.id WHERE j.id=?`, failure.JobID).
		Scan(&status, &finalStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return errors.New("outgoing keyword job is not pending")
	}
	if err != nil {
		return err
	}
	if status == "done" && finalStatus == "not_delivered" {
		return tx.Commit()
	}
	return errors.New("outgoing keyword job is not pending")
}

func finalizeKeywordFailure(ctx context.Context, tx *sql.Tx, failure domain.KeywordDeliveryFailure, failedAt time.Time) error {
	_, err := tx.ExecContext(ctx, `UPDATE live_delivery_history
		SET final_status='not_delivered',error_code=?,finalized_at=?
		WHERE job_id=? AND final_status IS NULL`,
		failure.ErrorCode, formatTime(failedAt), failure.JobID)
	return err
}

func persistKeywordFailureMetadata(ctx context.Context, tx *sql.Tx, failure domain.KeywordDeliveryFailure) error {
	var accountID any
	if failure.AccountID != nil {
		accountID = *failure.AccountID
	}
	var deliveryType any
	if failure.Type != nil {
		deliveryType = *failure.Type
	}
	_, err := tx.ExecContext(ctx, `UPDATE live_delivery_history
		SET delivery_type=COALESCE(?,delivery_type),account_id=COALESCE(?,account_id),
		account_title_snapshot=CASE WHEN ? IS NULL THEN account_title_snapshot ELSE ? END
		WHERE job_id=? AND final_status IS NULL`,
		deliveryType, accountID, accountID, failure.AccountTitleSnapshot, failure.JobID)
	if err != nil {
		return fmt.Errorf("persist keyword response attempt metadata: %w", err)
	}
	return nil
}

func markPendingJobDone(ctx context.Context, tx *sql.Tx, jobID, accountID domain.ID, leaseToken string) (int64, error) {
	var result sql.Result
	var err error
	if leaseToken == "" {
		result, err = tx.ExecContext(ctx, `UPDATE outgoing_message_jobs
			SET status='done',last_error='',lease_token='',lease_until=NULL
			WHERE id=? AND account_id=? AND status IN ('queued','delayed') AND lease_token=''`,
			jobID, accountID)
	} else {
		result, err = tx.ExecContext(ctx, `UPDATE outgoing_message_jobs
			SET status='done',last_error='',lease_token='',lease_until=NULL
			WHERE id=? AND account_id=? AND status IN ('queued','delayed') AND lease_token=?`,
			jobID, accountID, leaseToken)
	}
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func markKeywordResponseDone(ctx context.Context, tx *sql.Tx, outcome domain.KeywordDeliveryOutcome) (domain.ID, int64, error) {
	var channelID string
	err := tx.QueryRowContext(ctx, `UPDATE outgoing_message_jobs
		SET status='done',last_error='',lease_token='',lease_until=NULL
		WHERE id=? AND account_id=? AND channel_id=? AND type='keyword_response'
		AND status IN ('queued','delayed') RETURNING channel_id`,
		outcome.JobID, outcome.AccountID, outcome.ChannelID).Scan(&channelID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, nil
	}
	if err != nil {
		return "", 0, err
	}
	return domain.ID(channelID), 1, nil
}

func recordSuccessfulDelivery(ctx context.Context, tx *sql.Tx, jobID domain.ID, event domain.OutgoingMessageEvent, requireChannel bool) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO outgoing_message_events
		(id,job_id,account_id,channel_id,type,success,error_code,created_at) VALUES (?,?,?,?,?,?,?,?)`,
		event.ID, jobID, event.AccountID, event.ChannelID, event.Type, boolInt(event.Success), event.ErrorCode, formatTime(event.CreatedAt)); err != nil {
		return err
	}
	query := `UPDATE accounts SET public_replies_sent=public_replies_sent+1,last_activity_at=?,
		flood_wait_until=NULL,last_error='',updated_at=? WHERE id=?`
	if event.Type == domain.JobPrivateMessage {
		query = `UPDATE accounts SET private_messages_sent=private_messages_sent+1,last_activity_at=?,
			flood_wait_until=NULL,last_error='',updated_at=? WHERE id=?`
	} else if event.Type != domain.JobPublicReply {
		return errors.New("unsupported completed job type")
	}
	result, err := tx.ExecContext(ctx, query, formatTime(event.CreatedAt), formatTime(event.CreatedAt), event.AccountID)
	if err != nil {
		return err
	}
	if err := requireSingleRow(result, "outbound account not found"); err != nil {
		return err
	}
	if event.ChannelID != "" {
		channelResult, err := tx.ExecContext(ctx, `UPDATE outbound_channels
			SET sent_count=sent_count+1,last_activity_at=?,updated_at=? WHERE id=?`,
			formatTime(event.CreatedAt), formatTime(event.CreatedAt), event.ChannelID)
		if err != nil {
			return err
		}
		if requireChannel {
			return requireSingleRow(channelResult, "outbound channel not found")
		}
	}
	return nil
}

func isPrivateClosedEvent(ctx context.Context, tx *sql.Tx, eventID, jobID, accountID domain.ID) (bool, error) {
	var storedJobID, storedAccountID, eventType, errorCode string
	var success int
	err := tx.QueryRowContext(ctx, `SELECT job_id,account_id,type,success,error_code
		FROM outgoing_message_events WHERE id=?`, eventID).
		Scan(&storedJobID, &storedAccountID, &eventType, &success, &errorCode)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return domain.ID(storedJobID) == jobID && domain.ID(storedAccountID) == accountID &&
		domain.JobType(eventType) == domain.JobPrivateMessage && success == 0 && errorCode == "private_message_closed", nil
}

func isCompletedKeywordDelivery(ctx context.Context, tx *sql.Tx, outcome domain.KeywordDeliveryOutcome, eventID domain.ID) (bool, error) {
	var status, jobType, claimedAccount string
	if err := tx.QueryRowContext(ctx, `SELECT status,type,COALESCE(account_id,'') FROM outgoing_message_jobs WHERE id=?`, outcome.JobID).
		Scan(&status, &jobType, &claimedAccount); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, errors.New("outgoing job not found")
		}
		return false, err
	}
	if status != "done" || domain.JobType(jobType) != domain.JobKeywordResponse || domain.ID(claimedAccount) != outcome.AccountID {
		return false, nil
	}
	var storedAccount, storedChannel, eventType string
	var success int
	err := tx.QueryRowContext(ctx, `SELECT account_id,channel_id,type,success FROM outgoing_message_events WHERE id=? AND job_id=?`, eventID, outcome.JobID).
		Scan(&storedAccount, &storedChannel, &eventType, &success)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return domain.ID(storedAccount) == outcome.AccountID && domain.ID(storedChannel) == outcome.ChannelID &&
		domain.JobType(eventType) == outcome.Type && success == 1, nil
}

func requireSingleRow(result sql.Result, missing string) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return errors.New(missing)
	}
	return nil
}

func (r *JobRepository) MarkDone(ctx context.Context, jobID domain.ID, event domain.OutgoingMessageEvent) error {
	if _, err := r.Claim(ctx, jobID, event.AccountID); err != nil {
		return err
	}
	_, err := r.Complete(ctx, jobID, event)
	return err
}

func (r *JobRepository) Delay(ctx context.Context, jobID domain.ID, reason string) error {
	return r.DelayUntil(ctx, jobID, reason, r.now().UTC().Add(2*time.Second))
}

func (r *JobRepository) DelayUntil(ctx context.Context, jobID domain.ID, reason string, nextAttemptAt time.Time) error {
	result, err := r.db.ExecContext(ctx, `UPDATE outgoing_message_jobs
		SET status=CASE WHEN attempts+1>=? THEN 'done' ELSE 'delayed' END,
		attempts=attempts+1,next_attempt_at=?,last_error=?,
		dead_lettered_at=CASE WHEN attempts+1>=? THEN ? ELSE dead_lettered_at END
		WHERE id=? AND status IN ('queued','delayed')`,
		maxDeliveryAttempts, formatTime(nextAttemptAt), reason, maxDeliveryAttempts, formatTime(r.now().UTC()), jobID)
	if err != nil {
		return fmt.Errorf("delay outgoing job: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		if err != nil {
			return err
		}
		return errors.New("outgoing job is not pending")
	}
	return nil
}

func (r *JobRepository) DelayLease(ctx context.Context, jobID domain.ID, token, reason string) error {
	return r.DelayLeaseUntil(ctx, jobID, token, reason, r.now().UTC().Add(2*time.Second))
}

func (r *JobRepository) DelayLeaseUntil(ctx context.Context, jobID domain.ID, token, reason string, nextAttemptAt time.Time) error {
	if token == "" {
		return errors.New("outgoing job lease token is required")
	}
	result, err := r.db.ExecContext(ctx, `UPDATE outgoing_message_jobs
		SET status=CASE WHEN attempts+1>=? THEN 'done' ELSE 'delayed' END,
		attempts=attempts+1,next_attempt_at=?,last_error=?,lease_token='',lease_until=NULL,
		dead_lettered_at=CASE WHEN attempts+1>=? THEN ? ELSE dead_lettered_at END
		WHERE id=? AND lease_token=? AND status IN ('queued','delayed')`,
		maxDeliveryAttempts, formatTime(nextAttemptAt), reason, maxDeliveryAttempts, formatTime(r.now().UTC()), jobID, token)
	if err != nil {
		return fmt.Errorf("delay leased outgoing job: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		if err != nil {
			return err
		}
		return errors.New("outgoing job lease is not active")
	}
	return nil
}

func (r *JobRepository) DelayTransient(ctx context.Context, jobID domain.ID, reason string) error {
	return r.DelayTransientUntil(ctx, jobID, reason, r.now().UTC().Add(2*time.Second))
}

func (r *JobRepository) ReleaseGroupRestDelays(ctx context.Context) error {
	return releaseGroupRestDelays(ctx, r.db, r.now().UTC())
}

func releaseGroupRestDelays(ctx context.Context, executor jobExecutor, now time.Time) error {
	_, err := executor.ExecContext(ctx, `UPDATE outgoing_message_jobs
		SET status='queued',next_attempt_at=?,last_error=''
		WHERE status='delayed' AND last_error='account_group_rest'`, formatTime(now))
	if err != nil {
		return fmt.Errorf("release group rest delays: %w", err)
	}
	return nil
}

func (r *JobRepository) DelayTransientUntil(ctx context.Context, jobID domain.ID, reason string, nextAttemptAt time.Time) error {
	result, err := r.db.ExecContext(ctx, `UPDATE outgoing_message_jobs
		SET status='delayed',next_attempt_at=?,last_error=?
		WHERE id=? AND status IN ('queued','delayed')
		AND (
			? <> 'account_group_rest'
			OR COALESCE((
				SELECT json_extract(value_json,'$.groupRestEnabled')
				FROM app_settings
				WHERE key='app_settings'
			),1)=1
		)`, formatTime(nextAttemptAt), reason, jobID, reason)
	if err != nil {
		return fmt.Errorf("transiently delay outgoing job: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		if err != nil {
			return err
		}
		if reason == "account_group_rest" {
			var enabled int
			if err := r.db.QueryRowContext(ctx, `SELECT COALESCE((
				SELECT json_extract(value_json,'$.groupRestEnabled')
				FROM app_settings
				WHERE key='app_settings'
			),1)`).Scan(&enabled); err != nil {
				return fmt.Errorf("read account group rest setting: %w", err)
			}
			if enabled == 0 {
				return nil
			}
		}
		return errors.New("outgoing job is not pending")
	}
	return nil
}

func (r *JobRepository) DelayTransientLease(ctx context.Context, jobID domain.ID, token, reason string) error {
	return r.DelayTransientLeaseUntil(ctx, jobID, token, reason, r.now().UTC().Add(2*time.Second))
}

func (r *JobRepository) DelayTransientLeaseUntil(ctx context.Context, jobID domain.ID, token, reason string, nextAttemptAt time.Time) error {
	if token == "" {
		return errors.New("outgoing job lease token is required")
	}
	result, err := r.db.ExecContext(ctx, `UPDATE outgoing_message_jobs
		SET status=CASE
				WHEN ?='account_group_rest' AND COALESCE((
					SELECT json_extract(value_json,'$.groupRestEnabled')
					FROM app_settings
					WHERE key='app_settings'
				),1)=0 THEN 'queued'
				ELSE 'delayed'
			END,
			next_attempt_at=CASE
				WHEN ?='account_group_rest' AND COALESCE((
					SELECT json_extract(value_json,'$.groupRestEnabled')
					FROM app_settings
					WHERE key='app_settings'
				),1)=0 THEN ?
				ELSE ?
			END,
			last_error=CASE
				WHEN ?='account_group_rest' AND COALESCE((
					SELECT json_extract(value_json,'$.groupRestEnabled')
					FROM app_settings
					WHERE key='app_settings'
				),1)=0 THEN ''
				ELSE ?
			END,
			lease_token='',lease_until=NULL
		WHERE id=? AND lease_token=? AND status IN ('queued','delayed')`,
		reason,
		reason, formatTime(r.now().UTC()), formatTime(nextAttemptAt),
		reason, reason,
		jobID, token,
	)
	if err != nil {
		return fmt.Errorf("transiently delay leased outgoing job: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		if err != nil {
			return err
		}
		return errors.New("outgoing job lease is not active")
	}
	return nil
}

var _ domain.JobRepository = (*JobRepository)(nil)
var _ domain.DelayedJobRepository = (*JobRepository)(nil)
