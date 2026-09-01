package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"telegram-companion/internal/domain"
)

const keywordSettingsKey = "keyword_settings"

type CatalogStore struct {
	db *sql.DB
}

func NewCatalogStore(db *sql.DB) *CatalogStore {
	return &CatalogStore{db: db}
}

func (s *CatalogStore) List(ctx context.Context, catalog domain.SourceCatalog) ([]domain.Channel, error) {
	table, countColumn, err := catalogTable(catalog)
	if err != nil {
		return nil, err
	}
	where := ""
	if catalog == domain.SourceCatalogScout {
		where = " WHERE archived_at IS NULL"
	}
	query := fmt.Sprintf(`SELECT id, telegram_chat_id, title, link, topic, status, active, %s, last_activity_at, removal_requested_at, last_error, created_at, updated_at FROM %s%s ORDER BY created_at, id`, countColumn, table, where)
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list %s catalog: %w", catalog, err)
	}
	defer rows.Close()

	channels := make([]domain.Channel, 0)
	for rows.Next() {
		channel, err := scanCatalogChannel(rows, catalog)
		if err != nil {
			return nil, err
		}
		channels = append(channels, channel)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate %s catalog: %w", catalog, err)
	}
	return channels, nil
}

func (s *CatalogStore) Save(ctx context.Context, catalog domain.SourceCatalog, channel domain.Channel) error {
	return s.save(ctx, nil, catalog, channel)
}

func (s *CatalogStore) save(ctx context.Context, tx *sql.Tx, catalog domain.SourceCatalog, channel domain.Channel) error {
	table, countColumn, err := catalogTable(catalog)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if channel.CreatedAt.IsZero() {
		channel.CreatedAt = now
	}
	if channel.UpdatedAt.IsZero() {
		channel.UpdatedAt = now
	}
	if channel.TelegramID == "" {
		channel.TelegramID = string(channel.ID)
	}
	if channel.Status == "" {
		channel.Status = domain.ChannelPaused
	}
	if channel.Link == "" {
		return errors.New("catalog channel link is required")
	}

	count := channel.SentCount
	if catalog == domain.SourceCatalogScout {
		count = channel.MessageCount
		executor := catalogExecutor(s.db)
		if tx != nil {
			executor = tx
		}
		result, err := executor.ExecContext(ctx, `UPDATE scout_chats SET
			id=?,title=?,link=?,topic=?,status=?,active=?,removal_requested_at=NULL,
			archived_at=NULL,last_error=?,updated_at=?
			WHERE lower(link)=lower(?) AND archived_at IS NOT NULL`,
			channel.ID, channel.Title, channel.Link, channel.Topic, channel.Status, boolInt(channel.Active),
			channel.LastError, formatTime(channel.UpdatedAt), channel.Link)
		if err != nil {
			return fmt.Errorf("restore archived scout catalog channel: %w", err)
		}
		restored, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("count restored scout catalog channels: %w", err)
		}
		if restored > 1 {
			return errors.New("unexpected archived scout catalog restore count")
		}
		if restored == 1 {
			return nil
		}
	}
	query := fmt.Sprintf(`INSERT INTO %s (id, telegram_chat_id, title, link, topic, status, active, %s, last_activity_at, removal_requested_at, last_error, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET telegram_chat_id=excluded.telegram_chat_id, title=excluded.title, link=excluded.link,
		topic=excluded.topic, status=excluded.status, active=excluded.active, %s=excluded.%s,
		last_activity_at=excluded.last_activity_at, removal_requested_at=COALESCE(excluded.removal_requested_at,%s.removal_requested_at), last_error=excluded.last_error, updated_at=excluded.updated_at`, table, countColumn, countColumn, countColumn, table)
	args := []any{
		string(channel.ID), channel.TelegramID, channel.Title, channel.Link, channel.Topic, string(channel.Status), boolInt(channel.Active), count,
		formatTimePtr(channel.LastActivityAt), formatTimePtr(channel.RemovalRequestedAt), channel.LastError, formatTime(channel.CreatedAt), formatTime(channel.UpdatedAt),
	}
	if tx != nil {
		_, err = tx.ExecContext(ctx, query, args...)
	} else {
		_, err = s.db.ExecContext(ctx, query, args...)
	}
	if err != nil {
		return fmt.Errorf("save %s catalog channel: %w", catalog, err)
	}
	return nil
}

type catalogExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

type SettingsStore struct {
	db *sql.DB
}

func NewSettingsStore(db *sql.DB) *SettingsStore {
	return &SettingsStore{db: db}
}

func (s *SettingsStore) Load(ctx context.Context) (domain.KeywordSettings, error) {
	settings := domain.DefaultKeywordSettings()
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT value_json FROM app_settings WHERE key = ?`, keywordSettingsKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return settings, nil
	}
	if err != nil {
		return domain.KeywordSettings{}, fmt.Errorf("load keyword settings: %w", err)
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		return domain.KeywordSettings{}, fmt.Errorf("decode keyword settings: %w", err)
	}
	return settings, nil
}

func (s *SettingsStore) Save(ctx context.Context, settings domain.KeywordSettings) error {
	return saveKeywordSettings(ctx, s.db, nil, settings)
}

func saveKeywordSettings(ctx context.Context, db *sql.DB, tx *sql.Tx, settings domain.KeywordSettings) error {
	raw, err := json.Marshal(settings)
	if err != nil {
		return fmt.Errorf("encode keyword settings: %w", err)
	}
	query := `INSERT INTO app_settings (key, value_json, revision, updated_at) VALUES (?, ?, 1, ?)
		ON CONFLICT(key) DO UPDATE SET value_json=excluded.value_json, revision=app_settings.revision + 1, updated_at=excluded.updated_at`
	args := []any{keywordSettingsKey, string(raw), formatTime(time.Now().UTC())}
	if tx != nil {
		_, err = tx.ExecContext(ctx, query, args...)
	} else {
		_, err = db.ExecContext(ctx, query, args...)
	}
	if err != nil {
		return fmt.Errorf("save keyword settings: %w", err)
	}
	return nil
}

type catalogScanner interface {
	Scan(dest ...any) error
}

func scanCatalogChannel(scanner catalogScanner, catalog domain.SourceCatalog) (domain.Channel, error) {
	var channel domain.Channel
	var id, status, createdAt, updatedAt string
	var active int
	var count int64
	var lastActivityAt, removalRequestedAt sql.NullString
	if err := scanner.Scan(&id, &channel.TelegramID, &channel.Title, &channel.Link, &channel.Topic, &status, &active, &count, &lastActivityAt, &removalRequestedAt, &channel.LastError, &createdAt, &updatedAt); err != nil {
		return domain.Channel{}, fmt.Errorf("scan catalog channel: %w", err)
	}
	channel.ID = domain.ID(id)
	channel.Status = domain.ChannelStatus(status)
	channel.Active = active != 0
	if catalog == domain.SourceCatalogScout {
		channel.MessageCount = count
	} else {
		channel.SentCount = count
	}
	var err error
	if channel.CreatedAt, err = parseTime(createdAt); err != nil {
		return domain.Channel{}, err
	}
	if channel.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return domain.Channel{}, err
	}
	if lastActivityAt.Valid {
		value, err := parseTime(lastActivityAt.String)
		if err != nil {
			return domain.Channel{}, err
		}
		channel.LastActivityAt = &value
	}
	if removalRequestedAt.Valid {
		value, err := parseTime(removalRequestedAt.String)
		if err != nil {
			return domain.Channel{}, err
		}
		channel.RemovalRequestedAt = &value
	}
	return channel, nil
}

func catalogTable(catalog domain.SourceCatalog) (table, countColumn string, err error) {
	switch catalog {
	case domain.SourceCatalogOutbound:
		return "outbound_channels", "sent_count", nil
	case domain.SourceCatalogScout:
		return "scout_chats", "message_count", nil
	default:
		return "", "", fmt.Errorf("unsupported catalog %q", catalog)
	}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func formatTimePtr(value *time.Time) any {
	if value == nil {
		return nil
	}
	return formatTime(*value)
}

func parseTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse stored timestamp: %w", err)
	}
	return parsed, nil
}
