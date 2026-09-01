package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"telegram-companion/internal/domain"
)

type SQLiteAnalyticsSchedulerStore struct {
	db *sql.DB
}

func NewAnalyticsSchedulerStore(db *sql.DB) *SQLiteAnalyticsSchedulerStore {
	return &SQLiteAnalyticsSchedulerStore{db: db}
}

func (s *SQLiteAnalyticsSchedulerStore) AnalyticsSettings(ctx context.Context) (domain.AnalyticsSettings, error) {
	if err := s.ready(); err != nil {
		return domain.AnalyticsSettings{}, err
	}
	var enabled int
	settings := domain.DefaultAnalyticsSettings()
	err := s.db.QueryRowContext(ctx, `SELECT enabled, interval_minutes
		FROM analytics_scheduler_settings WHERE singleton=1`).Scan(&enabled, &settings.IntervalMinutes)
	if err != nil {
		return domain.AnalyticsSettings{}, fmt.Errorf("load analytics scheduler settings: %w", err)
	}
	settings.Enabled = enabled == 1
	return settings, nil
}

func (s *SQLiteAnalyticsSchedulerStore) SaveAnalyticsSettings(ctx context.Context, settings domain.AnalyticsSettings) error {
	if err := s.ready(); err != nil {
		return err
	}
	if !settings.Valid() {
		return errors.New("analytics interval must be one of 1, 5, 10, 30, 60, or 120 minutes")
	}
	_, err := s.db.ExecContext(ctx, `UPDATE analytics_scheduler_settings
		SET enabled=?, interval_minutes=?, updated_at=? WHERE singleton=1`,
		boolInt(settings.Enabled), settings.IntervalMinutes, formatTime(time.Now().UTC()))
	if err != nil {
		return fmt.Errorf("save analytics scheduler settings: %w", err)
	}
	return nil
}

func (s *SQLiteAnalyticsSchedulerStore) ScoutCursor(ctx context.Context, accountID domain.ID, chatID string) (domain.AnalyticsScoutCursor, error) {
	if err := s.ready(); err != nil {
		return domain.AnalyticsScoutCursor{}, err
	}
	result := domain.AnalyticsScoutCursor{AccountID: accountID, ChatID: strings.TrimSpace(chatID)}
	var updatedAt string
	err := s.db.QueryRowContext(ctx, `SELECT message_id, updated_at FROM analytics_scout_cursors
		WHERE account_id=? AND chat_id=?`, string(result.AccountID), result.ChatID).Scan(&result.MessageID, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return result, nil
	}
	if err != nil {
		return domain.AnalyticsScoutCursor{}, fmt.Errorf("load analytics scout cursor: %w", err)
	}
	result.UpdatedAt, err = parseTime(updatedAt)
	if err != nil {
		return domain.AnalyticsScoutCursor{}, fmt.Errorf("parse analytics scout cursor timestamp: %w", err)
	}
	return result, nil
}

func (s *SQLiteAnalyticsSchedulerStore) SaveScoutCursor(ctx context.Context, cursor domain.AnalyticsScoutCursor) error {
	if err := s.ready(); err != nil {
		return err
	}
	cursor.AccountID = domain.ID(strings.TrimSpace(string(cursor.AccountID)))
	cursor.ChatID = strings.TrimSpace(cursor.ChatID)
	if cursor.AccountID == "" || cursor.ChatID == "" || cursor.MessageID < 0 {
		return errors.New("analytics scout cursor requires account, chat, and a non-negative message ID")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO analytics_scout_cursors(account_id, chat_id, message_id, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(account_id, chat_id) DO UPDATE SET
		message_id=MAX(analytics_scout_cursors.message_id, excluded.message_id), updated_at=excluded.updated_at`,
		string(cursor.AccountID), cursor.ChatID, cursor.MessageID, formatTime(time.Now().UTC()))
	if err != nil {
		return fmt.Errorf("save analytics scout cursor: %w", err)
	}
	return nil
}

func (s *SQLiteAnalyticsSchedulerStore) AppendAnalyticsRun(ctx context.Context, run domain.AnalyticsSchedulerRun) error {
	if err := s.ready(); err != nil {
		return err
	}
	run.ID = strings.TrimSpace(run.ID)
	if run.ID == "" || run.StartedAt.IsZero() || !validRunMetrics(run.Metrics) {
		return errors.New("analytics scheduler run has invalid fields")
	}
	errorsJSON, err := json.Marshal(normalizeRunErrors(run.Metrics.Errors))
	if err != nil {
		return fmt.Errorf("encode analytics scheduler run errors: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO analytics_scheduler_runs
		(id, started_at, finished_at, new_messages, extracted_words, new_canonicals, processed_groups, duration_ms, errors_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, run.ID, formatTime(run.StartedAt), nullableTimePtr(run.FinishedAt),
		run.Metrics.NewMessages, run.Metrics.ExtractedWords, run.Metrics.NewCanonicals, run.Metrics.ProcessedGroups,
		run.Metrics.Duration.Milliseconds(), string(errorsJSON))
	if err != nil {
		return fmt.Errorf("append analytics scheduler run: %w", err)
	}
	return nil
}

func (s *SQLiteAnalyticsSchedulerStore) AnalyticsRunHistory(ctx context.Context, limit int) ([]domain.AnalyticsSchedulerRun, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		return []domain.AnalyticsSchedulerRun{}, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, started_at, finished_at, new_messages, extracted_words, new_canonicals,
		processed_groups, duration_ms, errors_json
		FROM analytics_scheduler_runs ORDER BY started_at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list analytics scheduler runs: %w", err)
	}
	defer rows.Close()
	result := make([]domain.AnalyticsSchedulerRun, 0)
	for rows.Next() {
		var run domain.AnalyticsSchedulerRun
		var startedAt string
		var finishedAt sql.NullString
		var durationMS int64
		var errorsJSON string
		if err := rows.Scan(&run.ID, &startedAt, &finishedAt, &run.Metrics.NewMessages, &run.Metrics.ExtractedWords,
			&run.Metrics.NewCanonicals, &run.Metrics.ProcessedGroups, &durationMS, &errorsJSON); err != nil {
			return nil, fmt.Errorf("scan analytics scheduler run: %w", err)
		}
		run.Metrics.Duration = time.Duration(durationMS) * time.Millisecond
		if err := json.Unmarshal([]byte(errorsJSON), &run.Metrics.Errors); err != nil {
			return nil, fmt.Errorf("decode analytics scheduler run errors: %w", err)
		}
		var parseErr error
		run.StartedAt, parseErr = parseTime(startedAt)
		if parseErr != nil {
			return nil, parseErr
		}
		if finishedAt.Valid {
			finished, parseErr := parseTime(finishedAt.String)
			if parseErr != nil {
				return nil, parseErr
			}
			run.FinishedAt = &finished
		}
		result = append(result, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate analytics scheduler runs: %w", err)
	}
	return result, nil
}

func (s *SQLiteAnalyticsSchedulerStore) AnalyticsMetrics(ctx context.Context) (domain.AnalyticsMetrics, error) {
	if err := s.ready(); err != nil {
		return domain.AnalyticsMetrics{}, err
	}
	var metrics domain.AnalyticsMetrics
	err := s.db.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM canonical_keywords),
		(SELECT COUNT(*) FROM canonical_keyword_forms),
		(SELECT COUNT(*) FROM scout_messages),
		(SELECT COUNT(*) FROM scout_chats),
		COALESCE((SELECT SUM(length(CAST(canonical_value AS BLOB))) FROM canonical_keywords), 0) +
		COALESCE((SELECT SUM(length(CAST(normalized_value AS BLOB))) FROM canonical_keyword_forms), 0)`).Scan(
		&metrics.CanonicalCount, &metrics.FormCount, &metrics.MessageCount, &metrics.GroupCount, &metrics.LogicalKeywordBytes,
	)
	if err != nil {
		return domain.AnalyticsMetrics{}, fmt.Errorf("load analytics metrics: %w", err)
	}
	history, err := s.AnalyticsRunHistory(ctx, 1)
	if err != nil {
		return domain.AnalyticsMetrics{}, err
	}
	if len(history) == 1 {
		metrics.LastRun = history[0].Metrics
	}
	return metrics, nil
}

func (s *SQLiteAnalyticsSchedulerStore) ServiceWords(ctx context.Context, language domain.AnalyticsLanguage) ([]string, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if !validAnalyticsLanguage(language) {
		return nil, errors.New("analytics service-word language must be ru or en")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT value FROM analytics_service_words WHERE language=? ORDER BY value`, string(language))
	if err != nil {
		return nil, fmt.Errorf("list analytics service words: %w", err)
	}
	defer rows.Close()
	words := make([]string, 0)
	for rows.Next() {
		var word string
		if err := rows.Scan(&word); err != nil {
			return nil, fmt.Errorf("scan analytics service word: %w", err)
		}
		words = append(words, word)
	}
	return words, rows.Err()
}

func (s *SQLiteAnalyticsSchedulerStore) UpsertServiceWord(ctx context.Context, word domain.AnalyticsServiceWord) error {
	if err := s.ready(); err != nil {
		return err
	}
	value, err := normalizeServiceWord(word)
	if err != nil {
		return err
	}
	now := formatTime(time.Now().UTC())
	_, err = s.db.ExecContext(ctx, `INSERT INTO analytics_service_words(language, value, created_at, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(language, value) DO UPDATE SET updated_at=excluded.updated_at`, string(word.Language), value, now, now)
	if err != nil {
		return fmt.Errorf("upsert analytics service word: %w", err)
	}
	return nil
}

func (s *SQLiteAnalyticsSchedulerStore) DeleteServiceWord(ctx context.Context, language domain.AnalyticsLanguage, value string) error {
	if err := s.ready(); err != nil {
		return err
	}
	value, err := normalizeServiceWord(domain.AnalyticsServiceWord{Language: language, Value: value})
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM analytics_service_words WHERE language=? AND value=?`, string(language), value)
	if err != nil {
		return fmt.Errorf("delete analytics service word: %w", err)
	}
	return nil
}

func (s *SQLiteAnalyticsSchedulerStore) TablePreference(ctx context.Context, tab string) (domain.AnalyticsTablePreference, error) {
	if err := s.ready(); err != nil {
		return domain.AnalyticsTablePreference{}, err
	}
	pref := domain.AnalyticsTablePreference{Tab: strings.TrimSpace(tab)}
	var columnsJSON, direction string
	err := s.db.QueryRowContext(ctx, `SELECT columns_json, sort_by, sort_direction, page_size FROM analytics_table_preferences WHERE tab=?`, pref.Tab).
		Scan(&columnsJSON, &pref.SortBy, &direction, &pref.PageSize)
	if errors.Is(err, sql.ErrNoRows) {
		return pref, nil
	}
	if err != nil {
		return domain.AnalyticsTablePreference{}, fmt.Errorf("load analytics table preference: %w", err)
	}
	pref.SortDirection = domain.AnalyticsSortDirection(direction)
	if err := json.Unmarshal([]byte(columnsJSON), &pref.Columns); err != nil {
		return domain.AnalyticsTablePreference{}, fmt.Errorf("decode analytics table columns: %w", err)
	}
	return pref, nil
}

func (s *SQLiteAnalyticsSchedulerStore) SaveTablePreference(ctx context.Context, pref domain.AnalyticsTablePreference) error {
	if err := s.ready(); err != nil {
		return err
	}
	pref.Tab = strings.TrimSpace(pref.Tab)
	pref.SortBy = strings.TrimSpace(pref.SortBy)
	if err := validTablePreference(pref); err != nil {
		return err
	}
	columnsJSON, err := json.Marshal(pref.Columns)
	if err != nil {
		return fmt.Errorf("encode analytics table columns: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO analytics_table_preferences(tab, columns_json, sort_by, sort_direction, page_size, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(tab) DO UPDATE SET columns_json=excluded.columns_json, sort_by=excluded.sort_by,
		sort_direction=excluded.sort_direction, page_size=excluded.page_size, updated_at=excluded.updated_at`,
		pref.Tab, string(columnsJSON), pref.SortBy, string(pref.SortDirection), pref.PageSize, formatTime(time.Now().UTC()))
	if err != nil {
		return fmt.Errorf("save analytics table preference: %w", err)
	}
	return nil
}

func (s *SQLiteAnalyticsSchedulerStore) ready() error {
	if s == nil || s.db == nil {
		return errors.New("analytics scheduler store is not initialized")
	}
	return nil
}

func validAnalyticsLanguage(language domain.AnalyticsLanguage) bool {
	return language == domain.AnalyticsLanguageRU || language == domain.AnalyticsLanguageEN
}

func normalizeServiceWord(word domain.AnalyticsServiceWord) (string, error) {
	if !validAnalyticsLanguage(word.Language) {
		return "", errors.New("analytics service-word language must be ru or en")
	}
	value := strings.ToLower(strings.TrimSpace(word.Value))
	if value == "" || len(strings.Fields(value)) != 1 {
		return "", errors.New("analytics service word must be one word")
	}
	return value, nil
}

func nullableTimePtr(value *time.Time) any {
	if value == nil || value.IsZero() {
		return nil
	}
	return formatTime(*value)
}

func validRunMetrics(metrics domain.AnalyticsRunMetrics) bool {
	return metrics.NewMessages >= 0 && metrics.ExtractedWords >= 0 && metrics.NewCanonicals >= 0 &&
		metrics.ProcessedGroups >= 0 && metrics.Duration >= 0
}

func normalizeRunErrors(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func validTablePreference(pref domain.AnalyticsTablePreference) error {
	if pref.Tab == "" || pref.PageSize <= 0 {
		return errors.New("analytics table preference requires tab and positive page size")
	}
	if pref.SortDirection != domain.AnalyticsSortNone && pref.SortDirection != domain.AnalyticsSortAscending && pref.SortDirection != domain.AnalyticsSortDescending {
		return errors.New("analytics table preference has invalid sort direction")
	}
	if pref.SortDirection != domain.AnalyticsSortNone && pref.SortBy == "" {
		return errors.New("analytics table preference requires a sort field when sorted")
	}
	seen := make(map[string]struct{}, len(pref.Columns))
	for _, column := range pref.Columns {
		key := strings.TrimSpace(column.Key)
		if key == "" || column.Width <= 0 {
			return errors.New("analytics table columns require names and positive widths")
		}
		if _, exists := seen[key]; exists {
			return errors.New("analytics table columns must be unique")
		}
		seen[key] = struct{}{}
	}
	return nil
}

var _ domain.AnalyticsSchedulerStore = (*SQLiteAnalyticsSchedulerStore)(nil)
