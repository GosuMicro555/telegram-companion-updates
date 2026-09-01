package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"telegram-companion/internal/usecase/scouting"
)

var ErrLowDisk = errors.New("scout collection paused: low disk space")
var ErrMessageTimestampMismatch = errors.New("edit message timestamp does not match stored message")

const (
	incrementalVacuumPages = 256
	fullVacuumMinPages     = 65536
	fullVacuumFreePercent  = 25
)

type DiskProbe interface {
	AvailableBytes(ctx context.Context, path string) (int64, error)
}

type MessageStore struct {
	db           *sql.DB
	path         string
	disk         DiskProbe
	minFreeBytes int64
	paused       atomic.Bool
}

func NewMessageStore(db *sql.DB, path string, disk DiskProbe, minFreeBytes int64) *MessageStore {
	return &MessageStore{db: db, path: path, disk: disk, minFreeBytes: minFreeBytes}
}

func (s *MessageStore) Exists(ctx context.Context, chatID string, messageID int64) (bool, error) {
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM scout_messages
		WHERE telegram_chat_id = ? AND telegram_message_id = ?
	)`, chatID, messageID).Scan(&exists); err != nil {
		return false, fmt.Errorf("check scout message existence: %w", err)
	}
	return exists != 0, nil
}

func (s *MessageStore) CanonicalMessageAt(ctx context.Context, chatID string, messageID int64, incoming time.Time) (time.Time, bool, error) {
	var tombstoned int
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM scout_message_tombstones
		WHERE telegram_chat_id = ? AND telegram_message_id = ?
	)`, chatID, messageID).Scan(&tombstoned); err != nil {
		return time.Time{}, false, fmt.Errorf("check scout message tombstone: %w", err)
	}
	if tombstoned != 0 {
		return time.Time{}, false, nil
	}
	var stored string
	err := s.db.QueryRowContext(ctx, `SELECT message_at FROM scout_messages
		WHERE telegram_chat_id = ? AND telegram_message_id = ?`, chatID, messageID).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		return incoming, true, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("load canonical message timestamp: %w", err)
	}
	canonical, err := parseTime(stored)
	if err != nil {
		return time.Time{}, false, err
	}
	return canonical, true, nil
}

func (s *MessageStore) SaveEncrypted(ctx context.Context, message scouting.EncryptedMessage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if message.ChatID == "" || message.MessageID <= 0 {
		return errors.New("encrypted message identity is required")
	}
	if len(message.Ciphertext) == 0 || len(message.Nonce) == 0 {
		return errors.New("ciphertext and nonce are required")
	}
	if message.MessageAt.IsZero() {
		return errors.New("message timestamp is required")
	}
	if err := s.checkDisk(ctx); err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin encrypted message save: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	result, err := tx.ExecContext(ctx, `INSERT INTO scout_messages
		(telegram_chat_id, telegram_message_id, encrypted_text, nonce, message_at, received_at, edited_at)
		SELECT ?, ?, ?, ?, ?, ?, ?
		WHERE NOT EXISTS (
			SELECT 1 FROM scout_message_tombstones
			WHERE telegram_chat_id = ? AND telegram_message_id = ?
		)
		ON CONFLICT(telegram_chat_id, telegram_message_id) DO NOTHING`,
		message.ChatID, message.MessageID, message.Ciphertext, message.Nonce,
		formatScoutTime(message.MessageAt), formatScoutTime(message.ReceivedAt), formatScoutTimePtr(message.EditedAt),
		message.ChatID, message.MessageID,
	)
	if err != nil {
		return fmt.Errorf("save encrypted scout message: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect encrypted message save: %w", err)
	}
	var updated int64
	if inserted == 0 && message.EditedAt != nil {
		result, err := tx.ExecContext(ctx, `UPDATE scout_messages SET
			encrypted_text=?,nonce=?,edited_at=?
			WHERE telegram_chat_id=? AND telegram_message_id=? AND message_at=?
			AND (edited_at IS NULL OR ? > edited_at)`,
			message.Ciphertext, message.Nonce, formatScoutTimePtr(message.EditedAt),
			message.ChatID, message.MessageID, formatScoutTime(message.MessageAt), formatScoutTimePtr(message.EditedAt))
		if err != nil {
			return fmt.Errorf("update encrypted scout message: %w", err)
		}
		updated, err = result.RowsAffected()
		if err != nil {
			return fmt.Errorf("inspect encrypted scout edit: %w", err)
		}
	}
	if inserted == 0 && updated == 0 && message.EditedAt != nil {
		var tombstoned int
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(
			SELECT 1 FROM scout_message_tombstones
			WHERE telegram_chat_id = ? AND telegram_message_id = ?
		)`, message.ChatID, message.MessageID).Scan(&tombstoned); err != nil {
			return fmt.Errorf("inspect scout message tombstone: %w", err)
		}
		if tombstoned != 0 {
			return nil
		}
		var storedMessageAt string
		if err := tx.QueryRowContext(ctx, `SELECT message_at FROM scout_messages
			WHERE telegram_chat_id = ? AND telegram_message_id = ?`, message.ChatID, message.MessageID).Scan(&storedMessageAt); err != nil {
			return fmt.Errorf("inspect ignored scout edit: %w", err)
		}
		if storedMessageAt != formatScoutTime(message.MessageAt) {
			return ErrMessageTimestampMismatch
		}
	}
	if inserted > 0 || updated > 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE scout_chats
			SET message_count=message_count+?, last_activity_at=?, updated_at=?
			WHERE telegram_chat_id=?`, inserted, formatScoutTime(message.ReceivedAt), formatScoutTime(message.ReceivedAt), message.ChatID); err != nil {
			return fmt.Errorf("update scout catalog activity: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit encrypted message save: %w", err)
	}
	return nil
}

func (s *MessageStore) Delete(ctx context.Context, chatID string, messageID int64, deletedAt time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin scout message delete: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO scout_message_tombstones
		(telegram_chat_id, telegram_message_id, deleted_at) VALUES (?, ?, ?)
		ON CONFLICT(telegram_chat_id, telegram_message_id) DO UPDATE SET
			deleted_at=excluded.deleted_at
		WHERE excluded.deleted_at > scout_message_tombstones.deleted_at`,
		chatID, messageID, formatScoutTime(deletedAt)); err != nil {
		return fmt.Errorf("save scout message tombstone: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM scout_messages WHERE telegram_chat_id = ? AND telegram_message_id = ?`, chatID, messageID); err != nil {
		return fmt.Errorf("delete scout message: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit scout message delete: %w", err)
	}
	return nil
}

func (s *MessageStore) PruneBefore(ctx context.Context, before time.Time) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin scout retention: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	retentionDeletedAt := before.Add(scouting.TombstoneRetentionPeriod)
	if _, err := tx.ExecContext(ctx, `INSERT INTO scout_message_tombstones
		(telegram_chat_id, telegram_message_id, deleted_at)
		SELECT telegram_chat_id, telegram_message_id, ?
		FROM scout_messages
		WHERE message_at < ?
		ON CONFLICT(telegram_chat_id, telegram_message_id) DO UPDATE SET
			deleted_at=excluded.deleted_at
		WHERE excluded.deleted_at > scout_message_tombstones.deleted_at`,
		formatScoutTime(retentionDeletedAt), formatScoutTime(before)); err != nil {
		return 0, fmt.Errorf("save retention tombstones: %w", err)
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM scout_messages WHERE message_at < ?`, formatScoutTime(before))
	if err != nil {
		return 0, fmt.Errorf("prune scout messages: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count pruned scout messages: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM scout_message_tombstones WHERE deleted_at < ?`, formatScoutTime(before)); err != nil {
		return 0, fmt.Errorf("prune scout message tombstones: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit scout retention: %w", err)
	}
	return deleted, nil
}

func (s *MessageStore) MaintainAfterPrune(ctx context.Context, deleted int64) error {
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`PRAGMA incremental_vacuum(%d)`, incrementalVacuumPages)); err != nil {
		return fmt.Errorf("incremental sqlite maintenance: %w", err)
	}
	var pages, freePages int64
	if err := s.db.QueryRowContext(ctx, `PRAGMA page_count`).Scan(&pages); err != nil {
		return fmt.Errorf("read sqlite page count: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, `PRAGMA freelist_count`).Scan(&freePages); err != nil {
		return fmt.Errorf("read sqlite free pages: %w", err)
	}
	if pages >= fullVacuumMinPages && freePages*100 >= pages*fullVacuumFreePercent {
		if _, err := s.db.ExecContext(ctx, `VACUUM`); err != nil {
			return fmt.Errorf("threshold sqlite compaction: %w", err)
		}
	}
	return nil
}

func (s *MessageStore) Count(ctx context.Context) (int64, error) {
	var count int64
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM scout_messages`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count scout messages: %w", err)
	}
	return count, nil
}

func (s *MessageStore) DatabaseBytes(ctx context.Context) (int64, error) {
	var total int64
	for _, path := range []string{s.path, s.path + "-wal", s.path + "-shm"} {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		info, err := os.Stat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return 0, fmt.Errorf("stat database file %s: %w", path, err)
		}
		total += info.Size()
	}
	return total, nil
}

func (s *MessageStore) TimestampBounds(ctx context.Context) (*time.Time, *time.Time, error) {
	var oldestRaw, newestRaw sql.NullString
	if err := s.db.QueryRowContext(ctx, `SELECT min(message_at), max(message_at) FROM scout_messages`).Scan(&oldestRaw, &newestRaw); err != nil {
		return nil, nil, fmt.Errorf("query scout timestamp bounds: %w", err)
	}
	if !oldestRaw.Valid {
		return nil, nil, nil
	}
	oldest, err := parseTime(oldestRaw.String)
	if err != nil {
		return nil, nil, err
	}
	newest, err := parseTime(newestRaw.String)
	if err != nil {
		return nil, nil, err
	}
	return &oldest, &newest, nil
}

func (s *MessageStore) PausedForLowDisk() bool {
	return s.paused.Load()
}

func (s *MessageStore) checkDisk(ctx context.Context) error {
	if s.disk == nil || s.minFreeBytes <= 0 {
		s.paused.Store(false)
		return nil
	}
	available, err := s.disk.AvailableBytes(ctx, s.path)
	if err != nil {
		return fmt.Errorf("probe database disk space: %w", err)
	}
	if available < s.minFreeBytes {
		s.paused.Store(true)
		return ErrLowDisk
	}
	s.paused.Store(false)
	return nil
}

func formatScoutTime(value time.Time) string {
	return value.UTC().Format("2006-01-02T15:04:05.000000000Z")
}

func formatScoutTimePtr(value *time.Time) any {
	if value == nil {
		return nil
	}
	return formatScoutTime(*value)
}

var _ scouting.MessageStorage = (*MessageStore)(nil)
