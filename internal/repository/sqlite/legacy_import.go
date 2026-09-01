package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"telegram-companion/internal/domain"

	bolt "go.etcd.io/bbolt"
)

var (
	legacySettingsBucket = []byte("settings")
	legacyKeywordsKey    = []byte("keywords")
	legacyChannelsKey    = []byte("channels")
)

type LegacyImporter struct {
	db      *sql.DB
	catalog *CatalogStore
}

type ImportSummary struct {
	Keywords        int
	Channels        int
	AlreadyImported bool
}

type legacyData struct {
	settings *domain.KeywordSettings
	channels []domain.ManagedChannel
}

func NewLegacyImporter(db *sql.DB) *LegacyImporter {
	return &LegacyImporter{db: db, catalog: NewCatalogStore(db)}
}

func (i *LegacyImporter) Import(ctx context.Context, boltPath string) (ImportSummary, error) {
	sha, err := fileSHA256(boltPath)
	if err != nil {
		return ImportSummary{}, err
	}
	data, err := readLegacyData(boltPath)
	if err != nil {
		return ImportSummary{}, err
	}

	tx, err := i.db.BeginTx(ctx, nil)
	if err != nil {
		return ImportSummary{}, fmt.Errorf("begin legacy import: %w", err)
	}
	defer tx.Rollback()

	now := formatTime(time.Now().UTC())
	marker, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO imports (id, file_name, file_path, sha256, rights_confirmed, imported_at, status)
		VALUES (?, ?, ?, ?, 1, ?, 'complete')`, "legacy-boltdb-"+sha, filepath.Base(boltPath), boltPath, sha, now)
	if err != nil {
		return ImportSummary{}, fmt.Errorf("claim legacy import marker: %w", err)
	}
	claimed, err := marker.RowsAffected()
	if err != nil {
		return ImportSummary{}, fmt.Errorf("check legacy import marker claim: %w", err)
	}
	if claimed == 0 {
		return ImportSummary{AlreadyImported: true}, nil
	}

	summary := ImportSummary{}
	if data.settings != nil {
		if err := saveKeywordSettings(ctx, i.db, tx, *data.settings); err != nil {
			return ImportSummary{}, err
		}
		summary.Keywords = len(data.settings.Keywords)
	}
	for index, legacyChannel := range data.channels {
		channel := legacyChannelToOutbound(legacyChannel, index)
		if err := i.catalog.save(ctx, tx, domain.SourceCatalogOutbound, channel); err != nil {
			return ImportSummary{}, err
		}
		summary.Channels++
	}
	if _, err := tx.ExecContext(ctx, `UPDATE accounts SET role = ? WHERE role = ''`, domain.AccountRoleSpammer); err != nil {
		return ImportSummary{}, fmt.Errorf("default legacy account roles: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return ImportSummary{}, fmt.Errorf("commit legacy import: %w", err)
	}
	return summary, nil
}

func readLegacyData(path string) (legacyData, error) {
	db, err := bolt.Open(path, 0o600, &bolt.Options{ReadOnly: true})
	if err != nil {
		return legacyData{}, fmt.Errorf("open legacy boltdb: %w", err)
	}
	defer db.Close()

	var data legacyData
	err = db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(legacySettingsBucket)
		if bucket == nil {
			return nil
		}
		if raw := bucket.Get(legacyKeywordsKey); len(raw) != 0 {
			settings := domain.KeywordSettings{}
			if err := json.Unmarshal(raw, &settings); err != nil {
				return fmt.Errorf("decode legacy keyword settings: %w", err)
			}
			data.settings = &settings
		}
		if raw := bucket.Get(legacyChannelsKey); len(raw) != 0 {
			if err := json.Unmarshal(raw, &data.channels); err != nil {
				return fmt.Errorf("decode legacy channels: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return legacyData{}, err
	}
	return data, nil
}

func legacyChannelToOutbound(legacy domain.ManagedChannel, index int) domain.Channel {
	id := legacy.ID
	if id == "" {
		id = fmt.Sprintf("legacy-outbound-%d", index+1)
	}
	status := legacy.Status
	if status == "" {
		status = domain.ChannelPaused
	}
	now := time.Now().UTC()
	return domain.Channel{
		ID:         domain.ID(id),
		TelegramID: id,
		Title:      legacy.Title,
		Link:       legacy.Link,
		Topic:      "Без тематики",
		Status:     status,
		Active:     legacy.Active,
		SentCount:  int64(legacy.Sent),
		CreatedAt:  now,
		UpdatedAt:  now,
	}
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open legacy boltdb for checksum: %w", err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("hash legacy boltdb: %w", err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
