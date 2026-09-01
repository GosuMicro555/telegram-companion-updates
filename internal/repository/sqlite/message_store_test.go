package sqlite_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"telegram-companion/internal/repository/sqlite"
	messagecrypto "telegram-companion/internal/service/crypto"
	"telegram-companion/internal/usecase/scouting"

	"github.com/stretchr/testify/require"
)

type storeClock struct{ now time.Time }

func (c *storeClock) Now() time.Time { return c.now }

type diskProbeStub struct {
	available int64
	err       error
}

func (p *diskProbeStub) AvailableBytes(context.Context, string) (int64, error) {
	return p.available, p.err
}

func openMessageStore(t *testing.T) (*sql.DB, string, *sqlite.MessageStore, *storeClock, *diskProbeStub, *messagecrypto.MessageCipher) {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "scout.sqlite")
	db, err := sqlite.Open(ctx, path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	now := time.Date(2026, 7, 11, 8, 30, 0, 0, time.UTC)
	require.NoError(t, insertScoutChat(ctx, db, now))
	cipher, err := messagecrypto.NewMessageCipher(bytes.Repeat([]byte{0x42}, 32), nil)
	require.NoError(t, err)
	clock := &storeClock{now: now}
	disk := &diskProbeStub{available: 1 << 30}
	store := sqlite.NewMessageStore(db, path, disk, 100<<20)
	return db, path, store, clock, disk, cipher
}

func insertScoutChat(ctx context.Context, db *sql.DB, now time.Time) error {
	stamp := now.UTC().Format(time.RFC3339Nano)
	_, err := db.ExecContext(ctx, `INSERT INTO scout_chats
		(id, telegram_chat_id, title, link, topic, status, active, created_at, updated_at)
		VALUES ('chat-1001', '1001', 'Scout', 'https://t.me/scout1001', 'research', 'ready', 1, ?, ?)`, stamp, stamp)
	return err
}

func TestCollectorPersistsNoAuthorFieldsEncryptsAndDeduplicates(t *testing.T) {
	ctx := context.Background()
	db, path, store, clock, _, cipher := openMessageStore(t)
	collector := scouting.NewCollector(store, cipher, clock)
	update := scouting.IncomingUpdate{
		ChatID: "1001", MessageID: 77, Text: "пример текста",
		SenderID: "must-not-persist", SenderUsername: "private-author-username",
		MessageAt: clock.Now().Add(-time.Minute),
	}
	require.NoError(t, collector.Ingest(ctx, update))

	var firstCiphertext, firstNonce []byte
	require.NoError(t, db.QueryRowContext(ctx, `SELECT encrypted_text, nonce FROM scout_messages`).Scan(&firstCiphertext, &firstNonce))
	require.NoError(t, collector.Ingest(ctx, update))
	var secondCiphertext, secondNonce []byte
	require.NoError(t, db.QueryRowContext(ctx, `SELECT encrypted_text, nonce FROM scout_messages`).Scan(&secondCiphertext, &secondNonce))
	require.Equal(t, firstCiphertext, secondCiphertext)
	require.Equal(t, firstNonce, secondNonce)

	count, err := store.Count(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), count)
	var messageCount int64
	var lastActivity string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT message_count,last_activity_at FROM scout_chats WHERE id='chat-1001'`).Scan(&messageCount, &lastActivity))
	require.Equal(t, int64(1), messageCount)
	require.Equal(t, clock.Now(), parseStoredTime(t, lastActivity))
	require.NoError(t, checkpoint(ctx, db))
	raw := rawDatabaseFiles(t, path)
	require.NotContains(t, raw, "must-not-persist")
	require.NotContains(t, raw, "private-author-username")
	require.NotContains(t, raw, "пример текста")
}

func TestEditReplacesCiphertextAndNonceAndDeleteRemovesRow(t *testing.T) {
	ctx := context.Background()
	db, _, store, clock, _, cipher := openMessageStore(t)
	collector := scouting.NewCollector(store, cipher, clock)
	messageAt := clock.Now().Add(-time.Minute)
	require.NoError(t, collector.Ingest(ctx, scouting.IncomingUpdate{ChatID: "1001", MessageID: 77, Text: "original", MessageAt: messageAt}))

	var originalCiphertext, originalNonce []byte
	require.NoError(t, db.QueryRowContext(ctx, `SELECT encrypted_text, nonce FROM scout_messages`).Scan(&originalCiphertext, &originalNonce))
	editedAt := clock.Now()
	require.NoError(t, collector.Ingest(ctx, scouting.IncomingUpdate{
		ChatID: "1001", MessageID: 77, Text: "edited", MessageAt: messageAt, EditedAt: &editedAt,
	}))
	var editedCiphertext, editedNonce []byte
	require.NoError(t, db.QueryRowContext(ctx, `SELECT encrypted_text, nonce FROM scout_messages`).Scan(&editedCiphertext, &editedNonce))
	require.NotEqual(t, originalCiphertext, editedCiphertext)
	require.NotEqual(t, originalNonce, editedNonce)

	require.NoError(t, collector.Delete(ctx, "1001", 77))
	count, err := store.Count(ctx)
	require.NoError(t, err)
	require.Zero(t, count)
}

func TestConcurrentReplayedEditsNeverReplaceStrictlyNewerEdit(t *testing.T) {
	ctx := context.Background()
	db, _, store, clock, _, cipher := openMessageStore(t)
	collector := scouting.NewCollector(store, cipher, clock)
	messageAt := clock.Now().Add(-time.Hour)
	require.NoError(t, collector.Ingest(ctx, scouting.IncomingUpdate{
		ChatID: "1001", MessageID: 77, Text: "original", MessageAt: messageAt,
	}))

	newestEdit := clock.Now().Add(10 * time.Minute)
	require.NoError(t, collector.Ingest(ctx, scouting.IncomingUpdate{
		ChatID: "1001", MessageID: 77, Text: "newest", MessageAt: messageAt, EditedAt: &newestEdit,
	}))
	wantCiphertext, wantNonce, wantEditedAt := encryptedRow(t, ctx, db, 77)

	replayed := []time.Time{
		newestEdit,
		newestEdit.Add(-time.Second),
		newestEdit.Add(-time.Minute),
		newestEdit,
		newestEdit.Add(-time.Hour),
	}
	start := make(chan struct{})
	errs := make(chan error, len(replayed))
	var workers sync.WaitGroup
	for index, editedAt := range replayed {
		workers.Add(1)
		go func(index int, editedAt time.Time) {
			defer workers.Done()
			<-start
			errs <- collector.Ingest(ctx, scouting.IncomingUpdate{
				ChatID: "1001", MessageID: 77, Text: fmt.Sprintf("stale-%d", index),
				MessageAt: messageAt, EditedAt: &editedAt,
			})
		}(index, editedAt)
	}
	close(start)
	workers.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	gotCiphertext, gotNonce, gotEditedAt := encryptedRow(t, ctx, db, 77)
	require.Equal(t, wantCiphertext, gotCiphertext)
	require.Equal(t, wantNonce, gotNonce)
	require.Equal(t, wantEditedAt, gotEditedAt)
}

func TestEditOneNanosecondNewerReplacesPriorEdit(t *testing.T) {
	ctx := context.Background()
	db, _, store, clock, _, cipher := openMessageStore(t)
	collector := scouting.NewCollector(store, cipher, clock)
	messageAt := clock.Now().Add(-time.Hour)
	require.NoError(t, collector.Ingest(ctx, scouting.IncomingUpdate{
		ChatID: "1001", MessageID: 77, Text: "original", MessageAt: messageAt,
	}))
	firstEdit := clock.Now()
	require.NoError(t, collector.Ingest(ctx, scouting.IncomingUpdate{
		ChatID: "1001", MessageID: 77, Text: "first", MessageAt: messageAt, EditedAt: &firstEdit,
	}))
	newerEdit := firstEdit.Add(time.Nanosecond)
	require.NoError(t, collector.Ingest(ctx, scouting.IncomingUpdate{
		ChatID: "1001", MessageID: 77, Text: "newer", MessageAt: messageAt, EditedAt: &newerEdit,
	}))

	ciphertext, nonce, editedAt := encryptedRow(t, ctx, db, 77)
	require.Equal(t, newerEdit.Format(time.RFC3339Nano), editedAt)
	plaintext, err := cipher.Decrypt(ctx, ciphertext, nonce, "1001", 77, messageAt)
	require.NoError(t, err)
	require.Equal(t, "newer", string(plaintext))
}

func TestEditUsesCanonicalTimestampAndCannotExtendRetention(t *testing.T) {
	ctx := context.Background()
	db, _, store, clock, _, cipher := openMessageStore(t)
	collector := scouting.NewCollector(store, cipher, clock)
	originalMessageAt := clock.Now().Add(-31 * 24 * time.Hour)
	originalReceivedAt := clock.Now()
	require.NoError(t, collector.Ingest(ctx, scouting.IncomingUpdate{
		ChatID: "1001", MessageID: 77, Text: "original", MessageAt: originalMessageAt,
	}))

	clock.now = clock.Now().Add(2 * time.Hour)
	editedAt := clock.Now()
	require.NoError(t, collector.Ingest(ctx, scouting.IncomingUpdate{
		ChatID: "1001", MessageID: 77, Text: "edited",
		MessageAt: clock.Now(), EditedAt: &editedAt,
	}))

	var ciphertext, nonce []byte
	var storedMessageAt, storedReceivedAt string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT encrypted_text, nonce, message_at, received_at
		FROM scout_messages WHERE telegram_message_id = 77`).Scan(
		&ciphertext, &nonce, &storedMessageAt, &storedReceivedAt,
	))
	require.Equal(t, originalMessageAt, parseStoredTime(t, storedMessageAt))
	require.Equal(t, originalReceivedAt, parseStoredTime(t, storedReceivedAt))
	plaintext, err := cipher.Decrypt(ctx, ciphertext, nonce, "1001", 77, originalMessageAt)
	require.NoError(t, err)
	require.Equal(t, "edited", string(plaintext))

	deleted, err := store.PruneBefore(ctx, clock.Now().Add(-30*24*time.Hour))
	require.NoError(t, err)
	require.Equal(t, int64(1), deleted)
}

func TestMessageStorePausesAndResumesForLowDisk(t *testing.T) {
	ctx := context.Background()
	_, _, store, clock, disk, cipher := openMessageStore(t)
	collector := scouting.NewCollector(store, cipher, clock)
	disk.available = 99 << 20

	err := collector.Ingest(ctx, scouting.IncomingUpdate{ChatID: "1001", MessageID: 77, Text: "secret", MessageAt: clock.Now()})
	require.ErrorIs(t, err, sqlite.ErrLowDisk)
	require.True(t, store.PausedForLowDisk())

	disk.available = 101 << 20
	require.NoError(t, collector.Ingest(ctx, scouting.IncomingUpdate{ChatID: "1001", MessageID: 77, Text: "secret", MessageAt: clock.Now()}))
	require.False(t, store.PausedForLowDisk())
}

func TestMessageStorePropagatesDiskProbeFailure(t *testing.T) {
	ctx := context.Background()
	_, _, store, clock, disk, cipher := openMessageStore(t)
	collector := scouting.NewCollector(store, cipher, clock)
	disk.err = errors.New("disk probe failed")
	err := collector.Ingest(ctx, scouting.IncomingUpdate{ChatID: "1001", MessageID: 77, Text: "secret", MessageAt: clock.Now()})
	require.ErrorContains(t, err, "disk probe failed")
}

func TestDeleteTombstoneBlocksLateOriginalAndEdit(t *testing.T) {
	ctx := context.Background()
	db, _, store, clock, _, cipher := openMessageStore(t)
	collector := scouting.NewCollector(store, cipher, clock)
	messageAt := clock.Now().Add(-time.Hour)
	require.NoError(t, collector.Ingest(ctx, scouting.IncomingUpdate{
		ChatID: "1001", MessageID: 77, Text: "original", MessageAt: messageAt,
	}))
	require.NoError(t, collector.Delete(ctx, "1001", 77))

	var deletedAt string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT deleted_at FROM scout_message_tombstones
		WHERE telegram_chat_id = '1001' AND telegram_message_id = 77`).Scan(&deletedAt))
	require.Equal(t, clock.Now(), parseStoredTime(t, deletedAt))

	require.NoError(t, collector.Ingest(ctx, scouting.IncomingUpdate{
		ChatID: "1001", MessageID: 77, Text: "late original", MessageAt: messageAt,
	}))
	lateEdit := clock.Now().Add(time.Minute)
	require.NoError(t, collector.Ingest(ctx, scouting.IncomingUpdate{
		ChatID: "1001", MessageID: 77, Text: "late edit", MessageAt: messageAt, EditedAt: &lateEdit,
	}))
	count, err := store.Count(ctx)
	require.NoError(t, err)
	require.Zero(t, count)
}

func TestRetentionPrunesTombstonesOnlyAfterThirtyDays(t *testing.T) {
	ctx := context.Background()
	db, _, store, clock, _, cipher := openMessageStore(t)
	collector := scouting.NewCollector(store, cipher, clock)
	now := clock.Now()

	clock.now = now.Add(-31 * 24 * time.Hour)
	require.NoError(t, collector.Delete(ctx, "1001", 1))
	clock.now = now.Add(-30 * 24 * time.Hour)
	require.NoError(t, collector.Delete(ctx, "1001", 2))
	clock.now = now

	_, err := store.PruneBefore(ctx, now.Add(-30*24*time.Hour))
	require.NoError(t, err)
	var oldCount, boundaryCount int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM scout_message_tombstones
		WHERE telegram_message_id = 1`).Scan(&oldCount))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM scout_message_tombstones
		WHERE telegram_message_id = 2`).Scan(&boundaryCount))
	require.Zero(t, oldCount)
	require.Equal(t, 1, boundaryCount)
}

func TestMaintenanceRunsWhenOnlyTombstonesWerePruned(t *testing.T) {
	ctx := context.Background()
	db, _, store, clock, _, _ := openMessageStore(t)
	old := clock.Now().Add(-31 * 24 * time.Hour)
	for index := range 4000 {
		_, err := db.ExecContext(ctx, `INSERT INTO scout_message_tombstones
			(telegram_chat_id,telegram_message_id,deleted_at) VALUES (?,?,?)`,
			fmt.Sprintf("quiet-channel-%04d-%s", index, strings.Repeat("x", 128)), index, old.UTC().Format(time.RFC3339Nano))
		require.NoError(t, err)
	}

	deleted, err := store.PruneBefore(ctx, clock.Now().Add(-30*24*time.Hour))
	require.NoError(t, err)
	require.Zero(t, deleted, "no messages were pruned in this quiet database")
	var before int64
	require.NoError(t, db.QueryRowContext(ctx, `PRAGMA freelist_count`).Scan(&before))
	require.Positive(t, before)

	require.NoError(t, store.MaintainAfterPrune(ctx, deleted))
	var after int64
	require.NoError(t, db.QueryRowContext(ctx, `PRAGMA freelist_count`).Scan(&after))
	require.Less(t, after, before)
}

func TestRetentionTombstoneBlocksReplayOfPrunedMessage(t *testing.T) {
	ctx := context.Background()
	db, _, store, clock, _, cipher := openMessageStore(t)
	collector := scouting.NewCollector(store, cipher, clock)
	messageAt := clock.Now().Add(-31 * 24 * time.Hour)
	require.NoError(t, collector.Ingest(ctx, scouting.IncomingUpdate{
		ChatID: "1001", MessageID: 77, Text: "old original", MessageAt: messageAt,
	}))

	deleted, err := store.PruneBefore(ctx, clock.Now().Add(-30*24*time.Hour))
	require.NoError(t, err)
	require.Equal(t, int64(1), deleted)
	var tombstoneAt string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT deleted_at FROM scout_message_tombstones
		WHERE telegram_chat_id = '1001' AND telegram_message_id = 77`).Scan(&tombstoneAt))
	require.Equal(t, clock.Now().Add(-30*24*time.Hour).Add(scouting.TombstoneRetentionPeriod), parseStoredTime(t, tombstoneAt))

	require.NoError(t, collector.Ingest(ctx, scouting.IncomingUpdate{
		ChatID: "1001", MessageID: 77, Text: "replayed original", MessageAt: messageAt,
	}))
	editedAt := clock.Now().Add(time.Minute)
	require.NoError(t, collector.Ingest(ctx, scouting.IncomingUpdate{
		ChatID: "1001", MessageID: 77, Text: "replayed edit", MessageAt: messageAt, EditedAt: &editedAt,
	}))
	count, err := store.Count(ctx)
	require.NoError(t, err)
	require.Zero(t, count)
}

func TestRetentionTombstoneAndMessageDeleteAreAtomic(t *testing.T) {
	tests := []struct {
		name    string
		trigger string
	}{
		{
			name: "tombstone insert failure",
			trigger: `CREATE TRIGGER reject_retention_tombstone
				BEFORE INSERT ON scout_message_tombstones
				BEGIN SELECT RAISE(ABORT, 'blocked retention tombstone'); END`,
		},
		{
			name: "message delete failure",
			trigger: `CREATE TRIGGER reject_retention_message_delete
				BEFORE DELETE ON scout_messages
				BEGIN SELECT RAISE(ABORT, 'blocked retention message delete'); END`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			db, _, store, clock, _, cipher := openMessageStore(t)
			collector := scouting.NewCollector(store, cipher, clock)
			old := clock.Now().Add(-31 * 24 * time.Hour)
			require.NoError(t, collector.Ingest(ctx, scouting.IncomingUpdate{
				ChatID: "1001", MessageID: 77, Text: "old", MessageAt: old,
			}))
			require.NoError(t, exec(ctx, db, test.trigger))

			_, err := store.PruneBefore(ctx, clock.Now().Add(-30*24*time.Hour))
			require.Error(t, err)
			count, err := store.Count(ctx)
			require.NoError(t, err)
			require.Equal(t, int64(1), count)
			var tombstones int
			require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM scout_message_tombstones
				WHERE telegram_chat_id = '1001' AND telegram_message_id = 77`).Scan(&tombstones))
			require.Zero(t, tombstones)
		})
	}
}

func TestRetentionRollsBackMessagePruneWhenTombstonePruneFails(t *testing.T) {
	ctx := context.Background()
	db, _, store, clock, _, cipher := openMessageStore(t)
	collector := scouting.NewCollector(store, cipher, clock)
	now := clock.Now()
	old := now.Add(-31 * 24 * time.Hour)
	require.NoError(t, collector.Ingest(ctx, scouting.IncomingUpdate{
		ChatID: "1001", MessageID: 77, Text: "old", MessageAt: old,
	}))
	clock.now = old
	require.NoError(t, collector.Delete(ctx, "1001", 1))
	clock.now = now
	require.NoError(t, exec(ctx, db, `CREATE TRIGGER reject_tombstone_prune
		BEFORE DELETE ON scout_message_tombstones
		BEGIN SELECT RAISE(ABORT, 'blocked tombstone prune'); END`))

	_, err := store.PruneBefore(ctx, now.Add(-30*24*time.Hour))
	require.ErrorContains(t, err, "blocked tombstone prune")
	count, err := store.Count(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), count)
}

func TestPruneRemovesExpiredMessagesAndDerivedRowsInOneTransaction(t *testing.T) {
	ctx := context.Background()
	db, _, store, clock, _, cipher := openMessageStore(t)
	collector := scouting.NewCollector(store, cipher, clock)
	old := clock.Now().Add(-31 * 24 * time.Hour)
	recent := clock.Now().Add(-29 * 24 * time.Hour)
	require.NoError(t, collector.Ingest(ctx, scouting.IncomingUpdate{ChatID: "1001", MessageID: 1, Text: "old", MessageAt: old}))
	require.NoError(t, collector.Ingest(ctx, scouting.IncomingUpdate{ChatID: "1001", MessageID: 2, Text: "recent", MessageAt: recent}))
	require.NoError(t, exec(ctx, db, `CREATE TABLE scout_message_derivations (
		message_row_id INTEGER NOT NULL REFERENCES scout_messages(id) ON DELETE CASCADE,
		value TEXT NOT NULL)`))
	require.NoError(t, exec(ctx, db, `INSERT INTO scout_message_derivations(message_row_id, value)
		SELECT id, 'derived' FROM scout_messages WHERE telegram_message_id = 1`))

	deleted, err := store.PruneBefore(ctx, clock.Now().Add(-30*24*time.Hour))
	require.NoError(t, err)
	require.Equal(t, int64(1), deleted)
	count, err := store.Count(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), count)
	var derived int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM scout_message_derivations`).Scan(&derived))
	require.Zero(t, derived)
}

func TestMessageStoreReportsPhysicalBytesAndTimestampBounds(t *testing.T) {
	ctx := context.Background()
	_, _, store, clock, _, cipher := openMessageStore(t)
	collector := scouting.NewCollector(store, cipher, clock)
	messageAt := clock.Now().Add(-time.Hour)
	require.NoError(t, collector.Ingest(ctx, scouting.IncomingUpdate{ChatID: "1001", MessageID: 1, Text: "secret", MessageAt: messageAt}))

	bytes, err := store.DatabaseBytes(ctx)
	require.NoError(t, err)
	require.Positive(t, bytes)
	oldest, newest, err := store.TimestampBounds(ctx)
	require.NoError(t, err)
	require.Equal(t, messageAt, *oldest)
	require.Equal(t, messageAt, *newest)
}

func checkpoint(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`)
	return err
}

func rawDatabaseFiles(t *testing.T, path string) string {
	t.Helper()
	var all []byte
	for _, candidate := range []string{path, path + "-wal", path + "-shm"} {
		data, err := os.ReadFile(candidate)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		require.NoError(t, err)
		all = append(all, data...)
	}
	return string(all)
}

func exec(ctx context.Context, db *sql.DB, statement string) error {
	_, err := db.ExecContext(ctx, statement)
	return err
}

func encryptedRow(t *testing.T, ctx context.Context, db *sql.DB, messageID int64) ([]byte, []byte, string) {
	t.Helper()
	var ciphertext, nonce []byte
	var editedAt string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT encrypted_text, nonce, edited_at
		FROM scout_messages WHERE telegram_message_id = ?`, messageID).Scan(&ciphertext, &nonce, &editedAt))
	return ciphertext, nonce, editedAt
}

func parseStoredTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339Nano, value)
	require.NoError(t, err)
	return parsed
}
