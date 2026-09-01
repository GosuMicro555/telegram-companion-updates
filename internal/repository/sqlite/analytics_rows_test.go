package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/chacha20poly1305"

	messagecrypto "telegram-companion/internal/service/crypto"
	"telegram-companion/internal/usecase/scouting"
)

func TestEncryptedAnalyticsRowSourceDecryptsExactAADWithoutIdentity(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	require.NoError(t, err)
	defer db.Close()
	now := time.Date(2026, 7, 12, 9, 10, 11, 123, time.UTC)
	_, err = db.ExecContext(ctx, `INSERT INTO scout_chats
		(id, telegram_chat_id, title, link, topic, status, active, created_at, updated_at)
		VALUES ('source-a', '100', 'chat', 'https://t.me/example', 'boats', 'ready', 1, ?, ?)`, formatTime(now), formatTime(now))
	require.NoError(t, err)
	cipher, err := messagecrypto.NewMessageCipher(make([]byte, chacha20poly1305.KeySize), nil)
	require.NoError(t, err)
	store := NewMessageStore(db, "", nil, 0)
	collector := scouting.NewCollector(store, cipher, fixedSQLiteClock{now: now})
	require.NoError(t, collector.Ingest(ctx, scouting.IncomingUpdate{
		ChatID: "100", MessageID: 7, Text: "neutral analytics phrase", SenderID: "private-user", SenderUsername: "private-name", MessageAt: now,
	}))

	source := NewEncryptedAnalyticsRowSource(db, cipher)
	rows, err := source.Rows(ctx, "topic", []string{"boats"})

	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "neutral analytics phrase", rows[0].Text)
	require.Equal(t, "boats", rows[0].Topic)
	require.Equal(t, "source-a", rows[0].SourceID)
	require.Equal(t, "7", rows[0].MessageID)
	require.Equal(t, now, rows[0].MessageAt)
	require.NotContains(t, rows[0].SourceID, "private")
}

type fixedSQLiteClock struct{ now time.Time }

func (c fixedSQLiteClock) Now() time.Time { return c.now }
