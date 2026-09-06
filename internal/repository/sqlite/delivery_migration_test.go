package sqlite

import (
	"context"
	"database/sql"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"telegram-companion/internal/repository/sqlite/migrations"

	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func TestMigrationV15PreservesDeliveryRowsAndAddsAlternationDefaults(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "v14.db"))
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, migrateDeliveryThrough(ctx, db, 14))
	require.NoError(t, Configure(ctx, db))

	const now = "2026-07-15T09:00:00.000000000Z"
	_, err = db.ExecContext(ctx, `INSERT INTO accounts
		(id,phone_masked,display_name,role,status,session_path,public_replies_sent,private_messages_sent,last_error,created_at,updated_at)
		VALUES ('account-1','***01','Delivery','spammer','ready','/sessions/1',7,5,'kept',?,?)`, now, now)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO outbound_channels
		(id,telegram_chat_id,title,link,topic,sent_count,created_at,updated_at)
		VALUES ('channel-1','101','Channel','https://t.me/channel','topic',3,?,?)`, now, now)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO outgoing_message_jobs
		(id,type,account_id,channel_id,rule_id,target_telegram_id,reply_to_message_id,text,status,attempts,next_attempt_at,created_at,last_error,lease_token,lease_until,dead_lettered_at)
		VALUES ('job-1','public_reply','account-1','channel-1','rule-1','target-1','reply-1','text','delayed',2,?,?,'retry','lease-1',?,?)`, now, now, now, now)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO outgoing_message_events
		(id,job_id,account_id,channel_id,type,success,error_code,created_at)
		VALUES ('event-1','job-1','account-1','channel-1','public_reply',1,'',?)`, now)
	require.NoError(t, err)

	require.NoError(t, Migrate(ctx, db))

	var nextDelivery, accountError string
	var publicSent, privateSent, privateClosed int64
	require.NoError(t, db.QueryRowContext(ctx, `SELECT public_replies_sent,private_messages_sent,private_messages_closed,next_delivery,last_error
		FROM accounts WHERE id='account-1'`).Scan(&publicSent, &privateSent, &privateClosed, &nextDelivery, &accountError))
	require.Equal(t, int64(7), publicSent)
	require.Equal(t, int64(5), privateSent)
	require.Zero(t, privateClosed)
	require.Equal(t, "private", nextDelivery)
	require.Equal(t, "kept", accountError)

	var jobType, accountID, channelID, ruleID, targetID, replyID, jobText, status string
	var nextAttempt, createdAt, lastError, leaseToken, leaseUntil, deadLettered string
	var attempts, allowPrivate int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT type,account_id,channel_id,rule_id,target_telegram_id,reply_to_message_id,text,
		status,attempts,next_attempt_at,created_at,last_error,lease_token,lease_until,dead_lettered_at,allow_private
		FROM outgoing_message_jobs WHERE id='job-1'`).Scan(&jobType, &accountID, &channelID, &ruleID, &targetID, &replyID, &jobText,
		&status, &attempts, &nextAttempt, &createdAt, &lastError, &leaseToken, &leaseUntil, &deadLettered, &allowPrivate))
	require.Equal(t, []any{"public_reply", "account-1", "channel-1", "rule-1", "target-1", "reply-1", "text", "delayed", 2, now, now, "retry", "lease-1", now, now, 0},
		[]any{jobType, accountID, channelID, ruleID, targetID, replyID, jobText, status, attempts, nextAttempt, createdAt, lastError, leaseToken, leaseUntil, deadLettered, allowPrivate})

	var eventJobID, eventAccountID, eventChannelID, eventType, eventCreatedAt string
	var eventSuccess int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT job_id,account_id,channel_id,type,success,created_at FROM outgoing_message_events WHERE id='event-1'`).
		Scan(&eventJobID, &eventAccountID, &eventChannelID, &eventType, &eventSuccess, &eventCreatedAt))
	require.Equal(t, []any{"job-1", "account-1", "channel-1", "public_reply", 1, now},
		[]any{eventJobID, eventAccountID, eventChannelID, eventType, eventSuccess, eventCreatedAt})

	_, err = db.ExecContext(ctx, `INSERT INTO outgoing_message_jobs
		(id,type,channel_id,status,next_attempt_at,created_at,allow_private)
		VALUES ('job-keyword','keyword_response','channel-1','queued',?,?,1)`, now, now)
	require.NoError(t, err)
	var foreignKeyErrors int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_foreign_key_check`).Scan(&foreignKeyErrors))
	require.Zero(t, foreignKeyErrors)
	for _, index := range []string{"idx_outgoing_jobs_due", "idx_outgoing_jobs_lease"} {
		var count int
		require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`, index).Scan(&count))
		require.Equal(t, 1, count, index)
	}
}

func TestMigrationV15DownRejectsKeywordJobsWithoutMutation(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "v15-down.db"))
	require.NoError(t, err)
	defer db.Close()

	const now = "2026-07-16T09:00:00.000000000Z"
	_, err = db.ExecContext(ctx, `INSERT INTO accounts
		(id,phone_masked,display_name,role,status,session_path,next_delivery,private_messages_closed,created_at,updated_at)
		VALUES ('account-1','***01','Delivery','spammer','ready','/sessions/1','public',1,?,?)`, now, now)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO outgoing_message_jobs
		(id,type,account_id,channel_id,status,next_attempt_at,created_at,allow_private)
		VALUES ('job-keyword','keyword_response','account-1','channel-1','done',?,?,0)`, now, now)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO outgoing_message_events
		(id,job_id,account_id,channel_id,type,success,error_code,created_at) VALUES
		('job-keyword:private_closed','job-keyword','account-1','channel-1','private_message',0,'private_message_closed',?),
		('job-keyword:public_reply','job-keyword','account-1','channel-1','public_reply',1,'',?)`, now, now)
	require.NoError(t, err)

	provider, err := migrationProvider(db)
	require.NoError(t, err)
	for {
		version, versionErr := provider.GetDBVersion(ctx)
		require.NoError(t, versionErr)
		if version == 15 {
			break
		}
		_, err = provider.Down(ctx)
		require.NoError(t, err)
	}
	_, err = provider.Down(ctx)
	require.Error(t, err)

	version, err := provider.GetDBVersion(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(15), version)
	var deliveryColumns int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('accounts')
		WHERE name IN ('next_delivery','private_messages_closed')`).Scan(&deliveryColumns))
	require.Equal(t, 2, deliveryColumns)
	var jobType, status string
	var allowPrivate int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT type,status,allow_private FROM outgoing_message_jobs WHERE id='job-keyword'`).
		Scan(&jobType, &status, &allowPrivate))
	require.Equal(t, "keyword_response", jobType)
	require.Equal(t, "done", status)
	require.Zero(t, allowPrivate)
	var events int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM outgoing_message_events WHERE job_id='job-keyword'`).Scan(&events))
	require.Equal(t, 2, events)
	var foreignKeys, foreignKeyErrors int
	require.NoError(t, db.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&foreignKeys))
	require.Equal(t, 1, foreignKeys)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_foreign_key_check`).Scan(&foreignKeyErrors))
	require.Zero(t, foreignKeyErrors)
	var guardTables int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_temp_master WHERE name='delivery_v15_down_guard'`).Scan(&guardTables))
	require.Zero(t, guardTables)
}

func migrateDeliveryThrough(ctx context.Context, db *sql.DB, maxVersion int) error {
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		return err
	}
	legacy := fstest.MapFS{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		version, err := strconv.Atoi(strings.SplitN(entry.Name(), "_", 2)[0])
		if err != nil || version > maxVersion {
			continue
		}
		raw, err := fs.ReadFile(migrations.FS, entry.Name())
		if err != nil {
			return err
		}
		legacy[entry.Name()] = &fstest.MapFile{Data: raw}
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, legacy)
	if err != nil {
		return err
	}
	_, err = provider.Up(ctx)
	return err
}
