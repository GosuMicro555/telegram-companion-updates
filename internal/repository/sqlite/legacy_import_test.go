package sqlite_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"telegram-companion/internal/domain"
	"telegram-companion/internal/repository/sqlite"

	"github.com/stretchr/testify/require"
	bolt "go.etcd.io/bbolt"
)

func TestLegacyImportPreservesReplyAndDMNilSemantics(t *testing.T) {
	legacy := createLegacyBoltFixture(t, domain.KeywordSettings{
		Keywords:              []string{"слил", "тест1"},
		SharedReply:           "  ответ как введён  ",
		DirectMessageKeywords: nil,
	})
	settings, summary := importLegacy(t, legacy)

	require.Equal(t, 2, summary.Keywords)
	got, err := settings.Load(context.Background())
	require.NoError(t, err)
	require.Equal(t, "  ответ как введён  ", got.SharedReply)
	require.Nil(t, got.DirectMessageKeywords)
}

func TestLegacyImportPreservesExplicitEmptyDMKeywords(t *testing.T) {
	legacy := createLegacyBoltFixture(t, domain.KeywordSettings{
		Keywords:              []string{"слил"},
		SharedReply:           "reply",
		DirectMessageKeywords: []string{},
	})
	settings, _ := importLegacy(t, legacy)

	got, err := settings.Load(context.Background())
	require.NoError(t, err)
	require.NotNil(t, got.DirectMessageKeywords)
	require.Empty(t, got.DirectMessageKeywords)
}

func TestLegacyImportMigratesChannelsAndDefaultsAccountsToSpammer(t *testing.T) {
	legacy := createLegacyBoltFixture(t, domain.KeywordSettings{Keywords: []string{"one"}})
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "app.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	_, err = db.ExecContext(ctx, `
		INSERT INTO accounts (id, phone_masked, display_name, status, session_path, created_at, updated_at)
		VALUES ('legacy-account', '+7 ***', '', 'paused', 'legacy.session', '2026-07-11T00:00:00Z', '2026-07-11T00:00:00Z')`)
	require.NoError(t, err)

	importer := sqlite.NewLegacyImporter(db)
	summary, err := importer.Import(ctx, legacy)
	require.NoError(t, err)
	require.Equal(t, 1, summary.Channels)

	channels, err := sqlite.NewCatalogStore(db).List(ctx, domain.SourceCatalogOutbound)
	require.NoError(t, err)
	require.Len(t, channels, 1)
	require.Equal(t, "Без тематики", channels[0].Topic)

	var role string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT role FROM accounts WHERE id = 'legacy-account'`).Scan(&role))
	require.Equal(t, string(domain.AccountRoleSpammer), role)
}

func TestLegacyImportIsRecordedByBoltSHA256(t *testing.T) {
	legacy := createLegacyBoltFixture(t, domain.KeywordSettings{Keywords: []string{"one"}})
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "app.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	importer := sqlite.NewLegacyImporter(db)
	first, err := importer.Import(ctx, legacy)
	require.NoError(t, err)
	require.Equal(t, 1, first.Keywords)
	second, err := importer.Import(ctx, legacy)
	require.NoError(t, err)
	require.True(t, second.AlreadyImported)
	require.Zero(t, second.Keywords)

	contents, err := os.ReadFile(legacy)
	require.NoError(t, err)
	sum := sha256.Sum256(contents)
	var markers int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM imports WHERE sha256 = ?`, hex.EncodeToString(sum[:])).Scan(&markers))
	require.Equal(t, 1, markers)
	assertRowCount(t, db, "outbound_channels", 1)
}

func TestLegacyImportConcurrentCallsAreIdempotent(t *testing.T) {
	const callers = 64
	legacy := createLegacyBoltFixtureWithChannels(t, 256)
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "app.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	db.SetMaxOpenConns(callers + 1)

	lockConn, err := db.Conn(ctx)
	require.NoError(t, err)
	locked := true
	require.NoError(t, func() error {
		_, err := lockConn.ExecContext(ctx, "BEGIN IMMEDIATE")
		return err
	}())
	defer func() {
		if locked {
			_, err := lockConn.ExecContext(ctx, "COMMIT")
			require.NoError(t, err)
		}
		require.NoError(t, lockConn.Close())
	}()

	importer := sqlite.NewLegacyImporter(db)
	start := make(chan struct{})
	type result struct {
		summary sqlite.ImportSummary
		err     error
	}
	results := make(chan result, callers)
	var ready sync.WaitGroup
	ready.Add(callers)
	for range callers {
		go func() {
			ready.Done()
			<-start
			summary, err := importer.Import(ctx, legacy)
			results <- result{summary: summary, err: err}
		}()
	}
	ready.Wait()
	close(start)
	require.Eventually(t, func() bool {
		return db.Stats().InUse == callers+1
	}, time.Second, time.Millisecond)
	require.NoError(t, func() error {
		_, err := lockConn.ExecContext(ctx, "COMMIT")
		return err
	}())
	locked = false

	var imported, alreadyImported int
	for range callers {
		result := <-results
		require.NoError(t, result.err)
		if result.summary.AlreadyImported {
			alreadyImported++
			continue
		}
		imported++
	}
	require.Equal(t, 1, imported)
	require.Equal(t, callers-1, alreadyImported)
	assertRowCount(t, db, "imports", 1)
	assertRowCount(t, db, "outbound_channels", 256)
}

func importLegacy(t *testing.T, legacy string) (*sqlite.SettingsStore, sqlite.ImportSummary) {
	t.Helper()
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "app.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	importer := sqlite.NewLegacyImporter(db)
	summary, err := importer.Import(ctx, legacy)
	require.NoError(t, err)
	return sqlite.NewSettingsStore(db), summary
}

func createLegacyBoltFixture(t *testing.T, settings domain.KeywordSettings) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := bolt.Open(path, 0o600, nil)
	require.NoError(t, err)

	settingsJSON, err := json.Marshal(settings)
	require.NoError(t, err)
	channelsJSON, err := json.Marshal([]domain.ManagedChannel{{
		ID: "legacy-channel", Title: "Legacy Channel", Link: "https://t.me/legacy", Status: domain.ChannelStatusReady, Active: true,
	}})
	require.NoError(t, err)
	require.NoError(t, db.Update(func(tx *bolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists([]byte("settings"))
		if err != nil {
			return err
		}
		if err := bucket.Put([]byte("keywords"), settingsJSON); err != nil {
			return err
		}
		return bucket.Put([]byte("channels"), channelsJSON)
	}))
	require.NoError(t, db.Close())
	return path
}

func createLegacyBoltFixtureWithChannels(t *testing.T, count int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := bolt.Open(path, 0o600, nil)
	require.NoError(t, err)

	settingsJSON, err := json.Marshal(domain.KeywordSettings{Keywords: []string{"one"}})
	require.NoError(t, err)
	channels := make([]domain.ManagedChannel, count)
	for index := range channels {
		channels[index] = domain.ManagedChannel{
			ID:     fmt.Sprintf("legacy-channel-%d", index),
			Title:  fmt.Sprintf("Legacy Channel %d", index),
			Link:   fmt.Sprintf("https://t.me/legacy%d", index),
			Status: domain.ChannelStatusReady,
			Active: true,
		}
	}
	channelsJSON, err := json.Marshal(channels)
	require.NoError(t, err)
	require.NoError(t, db.Update(func(tx *bolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists([]byte("settings"))
		if err != nil {
			return err
		}
		if err := bucket.Put([]byte("keywords"), settingsJSON); err != nil {
			return err
		}
		return bucket.Put([]byte("channels"), channelsJSON)
	}))
	require.NoError(t, db.Close())
	return path
}

func assertRowCount(t *testing.T, db *sql.DB, table string, want int) {
	t.Helper()
	var got int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM `+table).Scan(&got))
	require.Equal(t, want, got)
}
