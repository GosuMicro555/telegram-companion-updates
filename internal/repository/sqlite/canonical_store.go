package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	core "telegram-companion/internal/analytics"
)

func canonicalID(language core.Language, value string) string {
	sum := sha256.Sum256([]byte(string(language) + "\x00" + strings.TrimSpace(value)))
	return hex.EncodeToString(sum[:])
}

func (s *AnalyticsStore) SyncCanonical(ctx context.Context, observations []core.CanonicalObservation) error {
	if err := s.ready(); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin canonical sync: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `DROP TABLE IF EXISTS temp.canonical_keyword_prior_frequency`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `CREATE TEMP TABLE canonical_keyword_prior_frequency AS SELECT id, frequency FROM canonical_keywords`); err != nil {
		return err
	}
	defer func() {
		_, _ = tx.ExecContext(context.Background(), `DROP TABLE IF EXISTS temp.canonical_keyword_prior_frequency`)
	}()
	if _, err = tx.ExecContext(ctx, `UPDATE canonical_keywords SET frequency_delta=-frequency,frequency=0,message_count=0,last_seen_at=NULL`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE canonical_keyword_forms SET frequency=0`); err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, observation := range observations {
		canonical := strings.TrimSpace(observation.Canonical)
		if canonical == "" || (observation.Language != core.LanguageRU && observation.Language != core.LanguageEN) {
			continue
		}
		id := canonicalID(observation.Language, canonical)
		_, err = tx.ExecContext(ctx, `INSERT INTO canonical_keywords
			(id,canonical_value,language,class,decision_source,trigger_active,frequency,frequency_delta,message_count,last_seen_at,created_at,updated_at)
			VALUES(?,?,?,'neutral','manual',0,?,0,?,?,?,?)
			ON CONFLICT(id) DO UPDATE SET frequency_delta=excluded.frequency-COALESCE((SELECT frequency FROM temp.canonical_keyword_prior_frequency WHERE id=canonical_keywords.id),0),frequency=excluded.frequency,message_count=excluded.message_count,last_seen_at=excluded.last_seen_at,updated_at=excluded.updated_at`,
			id, canonical, string(observation.Language), observation.TotalFrequency, observation.MessageCount, nullableTime(observation.LastSeen), formatTime(now), formatTime(now))
		if err != nil {
			return fmt.Errorf("upsert canonical keyword: %w", err)
		}
		for _, form := range observation.Forms {
			normalized := strings.TrimSpace(form.Form)
			if normalized == "" {
				continue
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO canonical_keyword_forms
				(normalized_value,canonical_id,display_value,frequency,manual_override,created_at,updated_at)
				VALUES(?,?,?,?,0,?,?)
				ON CONFLICT(normalized_value) DO UPDATE SET
				canonical_id=CASE WHEN canonical_keyword_forms.manual_override=1 THEN canonical_keyword_forms.canonical_id ELSE excluded.canonical_id END,
				display_value=excluded.display_value,frequency=excluded.frequency,updated_at=excluded.updated_at`,
				normalized, id, normalized, form.Frequency, formatTime(now), formatTime(now))
			if err != nil {
				return fmt.Errorf("upsert canonical form: %w", err)
			}
		}
	}
	if _, err = tx.ExecContext(ctx, `DROP TABLE IF EXISTS temp.canonical_keyword_prior_frequency`); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit canonical sync: %w", err)
	}
	return nil
}

func (s *AnalyticsStore) BulkImportCanonical(ctx context.Context, values []core.CanonicalImportValue, class core.KeywordClass) (core.CanonicalBulkImportResult, error) {
	if err := s.ready(); err != nil {
		return core.CanonicalBulkImportResult{}, err
	}
	if class != core.ClassPositive && class != core.ClassNegative {
		return core.CanonicalBulkImportResult{}, errors.New("bulk keyword class must be positive or negative")
	}
	for _, value := range values {
		if strings.TrimSpace(value.Value) == "" || (value.Language != core.LanguageRU && value.Language != core.LanguageEN) {
			return core.CanonicalBulkImportResult{}, errors.New("invalid canonical import value")
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return core.CanonicalBulkImportResult{}, fmt.Errorf("begin canonical bulk import: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	result := core.CanonicalBulkImportResult{}
	now := formatTime(time.Now().UTC())
	for _, value := range values {
		id := canonicalID(value.Language, value.Value)
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM canonical_keywords WHERE id=?)`, id).Scan(&exists); err != nil {
			return core.CanonicalBulkImportResult{}, fmt.Errorf("check canonical import value: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO canonical_keywords
			(id,canonical_value,language,class,decision_source,trigger_active,frequency,frequency_delta,message_count,created_at,updated_at)
			VALUES(?,?,?,?,'manual',0,0,0,0,?,?)
			ON CONFLICT(id) DO UPDATE SET class=excluded.class,decision_source='manual',
				trigger_active=CASE WHEN excluded.class='positive' THEN canonical_keywords.trigger_active ELSE 0 END,
				updated_at=excluded.updated_at`,
			id, value.Value, string(value.Language), string(class), now, now); err != nil {
			return core.CanonicalBulkImportResult{}, fmt.Errorf("upsert canonical import value: %w", err)
		}
		if exists {
			result.Updated++
		} else {
			result.Added++
		}
		for _, form := range value.Forms {
			if _, err := tx.ExecContext(ctx, `INSERT INTO canonical_keyword_forms
				(normalized_value,canonical_id,display_value,frequency,manual_override,created_at,updated_at)
				VALUES(?,?,?,0,1,?,?)
				ON CONFLICT(normalized_value) DO UPDATE SET canonical_id=excluded.canonical_id,
					display_value=excluded.display_value,manual_override=1,updated_at=excluded.updated_at`,
				form, id, form, now, now); err != nil {
				return core.CanonicalBulkImportResult{}, fmt.Errorf("upsert canonical import form: %w", err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return core.CanonicalBulkImportResult{}, fmt.Errorf("commit canonical bulk import: %w", err)
	}
	return result, nil
}

func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return formatTime(value)
}

func (s *AnalyticsStore) ListCanonical(ctx context.Context, class core.KeywordClass) ([]core.CanonicalKeyword, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,canonical_value,language,class,decision_source,trigger_active,frequency,frequency_delta,message_count,last_seen_at
		FROM canonical_keywords WHERE class=? ORDER BY frequency DESC,canonical_value`, string(class))
	if err != nil {
		return nil, fmt.Errorf("list canonical keywords: %w", err)
	}
	defer rows.Close()
	result := make([]core.CanonicalKeyword, 0)
	for rows.Next() {
		var word core.CanonicalKeyword
		var language, keywordClass string
		var trigger int
		var lastSeen sql.NullString
		if err := rows.Scan(&word.ID, &word.Canonical, &language, &keywordClass, &word.DecisionSource, &trigger, &word.TotalFrequency, &word.FrequencyDelta, &word.MessageCount, &lastSeen); err != nil {
			return nil, err
		}
		word.Language, word.Class, word.TriggerActive = core.Language(language), core.KeywordClass(keywordClass), trigger == 1
		if lastSeen.Valid {
			word.LastSeen, _ = parseTime(lastSeen.String)
		}
		forms, err := s.listCanonicalForms(ctx, word.ID)
		if err != nil {
			return nil, err
		}
		word.Forms = forms
		result = append(result, word)
	}
	return result, rows.Err()
}

func (s *AnalyticsStore) listCanonicalForms(ctx context.Context, id string) ([]core.CanonicalKeywordForm, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT normalized_value,display_value,frequency,manual_override FROM canonical_keyword_forms WHERE canonical_id=? ORDER BY frequency DESC,normalized_value`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []core.CanonicalKeywordForm
	for rows.Next() {
		var form core.CanonicalKeywordForm
		var manual int
		if err := rows.Scan(&form.Value, &form.DisplayValue, &form.Frequency, &manual); err != nil {
			return nil, err
		}
		form.ManualOverride = manual == 1
		result = append(result, form)
	}
	return result, rows.Err()
}

func (s *AnalyticsStore) SearchCanonical(ctx context.Context, query string, class core.KeywordClass) ([]core.CanonicalKeyword, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if class != "" && !validCanonicalClass(class) {
		return nil, errors.New("invalid keyword class")
	}
	query = strings.ToLower(strings.TrimSpace(query))
	pattern := "%" + escapeLike(query) + "%"
	rows, err := s.db.QueryContext(ctx, `SELECT id, canonical_value, language, class, decision_source, trigger_active, frequency, frequency_delta, message_count, last_seen_at
		FROM canonical_keywords keyword
		WHERE (?='' OR keyword.class=?)
			AND (?='' OR keyword.canonical_value LIKE ? ESCAPE '\' OR EXISTS (
				SELECT 1 FROM canonical_keyword_forms form
				WHERE form.canonical_id=keyword.id AND form.normalized_value LIKE ? ESCAPE '\'
			))
		ORDER BY keyword.frequency DESC, keyword.canonical_value`, string(class), string(class), query, pattern, pattern)
	if err != nil {
		return nil, fmt.Errorf("search canonical keywords: %w", err)
	}
	defer rows.Close()
	return s.readCanonicalRows(ctx, rows)
}

func (s *AnalyticsStore) CanonicalMetrics(ctx context.Context) (core.CanonicalMetrics, error) {
	if err := s.ready(); err != nil {
		return core.CanonicalMetrics{}, err
	}
	var metrics core.CanonicalMetrics
	err := s.db.QueryRowContext(ctx, `SELECT
		COUNT(*),
		COALESCE(SUM(class='positive'), 0),
		COALESCE(SUM(class='negative'), 0),
		COALESCE(SUM(class='service'), 0),
		COALESCE(SUM(class='positive' AND trigger_active=1), 0),
		(SELECT COUNT(*) FROM canonical_keyword_forms),
		COALESCE(SUM(length(CAST(canonical_value AS BLOB))), 0) +
		COALESCE((SELECT SUM(length(CAST(normalized_value AS BLOB))) FROM canonical_keyword_forms), 0)
		FROM canonical_keywords`).Scan(
		&metrics.KeywordCount, &metrics.PositiveCount, &metrics.NegativeCount, &metrics.ServiceCount,
		&metrics.TriggerCount, &metrics.FormCount, &metrics.LogicalBytes,
	)
	if err != nil {
		return core.CanonicalMetrics{}, fmt.Errorf("load canonical metrics: %w", err)
	}
	return metrics, nil
}

func (s *AnalyticsStore) ClassifyCanonical(ctx context.Context, id string, class core.KeywordClass) error {
	if !validCanonicalClass(class) {
		return errors.New("invalid keyword class")
	}
	_, err := s.db.ExecContext(ctx, `UPDATE canonical_keywords SET class=?,decision_source='manual',trigger_active=CASE WHEN ?='positive' THEN trigger_active ELSE 0 END,updated_at=? WHERE id=?`, string(class), string(class), formatTime(time.Now().UTC()), id)
	return err
}

func (s *AnalyticsStore) SetCanonicalTrigger(ctx context.Context, id string, active bool) error {
	result, err := s.db.ExecContext(ctx, `UPDATE canonical_keywords SET trigger_active=?,updated_at=? WHERE id=? AND class='positive'`, boolInt(active), formatTime(time.Now().UTC()), id)
	if err != nil {
		return err
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		return errors.New("only positive canonical keywords can be triggers")
	}
	return nil
}

func (s *AnalyticsStore) DeleteCanonicalTrigger(ctx context.Context, id string) error {
	return s.SetCanonicalTrigger(ctx, id, false)
}

func (s *AnalyticsStore) DeleteCanonical(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM canonical_keywords WHERE id=?`, id)
	return err
}

func (s *AnalyticsStore) ClearCanonicalClass(ctx context.Context, class core.KeywordClass) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM canonical_keywords WHERE class=?`, string(class))
	return err
}

func (s *AnalyticsStore) RemoveCanonicalForm(ctx context.Context, sourceID, value string) (core.CanonicalKeyword, error) {
	value = strings.TrimSpace(value)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return core.CanonicalKeyword{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var language string
	if err := tx.QueryRowContext(ctx, `SELECT language FROM canonical_keywords WHERE id=?`, sourceID).Scan(&language); err != nil {
		return core.CanonicalKeyword{}, err
	}
	id := canonicalID(core.Language(language), value)
	now := formatTime(time.Now().UTC())
	_, err = tx.ExecContext(ctx, `INSERT INTO canonical_keywords(id,canonical_value,language,class,decision_source,trigger_active,frequency,frequency_delta,message_count,created_at,updated_at)
		VALUES(?,?,?,'neutral','manual',0,0,0,0,?,?) ON CONFLICT(id) DO NOTHING`, id, value, language, now, now)
	if err != nil {
		return core.CanonicalKeyword{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE canonical_keyword_forms SET canonical_id=?,manual_override=1,updated_at=? WHERE normalized_value=? AND canonical_id=?`, id, now, value, sourceID)
	if err != nil {
		return core.CanonicalKeyword{}, err
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		return core.CanonicalKeyword{}, errors.New("canonical form not found")
	}
	if err := tx.Commit(); err != nil {
		return core.CanonicalKeyword{}, err
	}
	rows, err := s.ListCanonical(ctx, core.ClassNeutral)
	if err != nil {
		return core.CanonicalKeyword{}, err
	}
	for _, row := range rows {
		if row.ID == id {
			return row, nil
		}
	}
	return core.CanonicalKeyword{}, errors.New("detached canonical keyword not found")
}

func (s *AnalyticsStore) MoveCanonicalForm(ctx context.Context, targetID, value string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE canonical_keyword_forms SET canonical_id=?,manual_override=1,updated_at=? WHERE normalized_value=?`, targetID, formatTime(time.Now().UTC()), strings.TrimSpace(value))
	if err != nil {
		return err
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		return errors.New("canonical form not found")
	}
	_, _ = s.db.ExecContext(ctx, `DELETE FROM canonical_keywords WHERE id<>? AND NOT EXISTS(SELECT 1 FROM canonical_keyword_forms WHERE canonical_id=canonical_keywords.id)`, targetID)
	return nil
}

func (s *AnalyticsStore) AddCanonicalForm(ctx context.Context, targetID, value string) error {
	if err := s.ready(); err != nil {
		return err
	}
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || len(strings.Fields(value)) != 1 {
		return errors.New("canonical form must be one word")
	}
	now := formatTime(time.Now().UTC())
	_, err := s.db.ExecContext(ctx, `INSERT INTO canonical_keyword_forms
		(normalized_value,canonical_id,display_value,frequency,manual_override,created_at,updated_at)
		VALUES(?,?,?,0,1,?,?)
		ON CONFLICT(normalized_value) DO UPDATE SET canonical_id=excluded.canonical_id,
		display_value=excluded.display_value,manual_override=1,updated_at=excluded.updated_at`,
		value, targetID, value, now, now)
	if err != nil {
		return fmt.Errorf("add canonical form: %w", err)
	}
	return nil
}

func (s *AnalyticsStore) readCanonicalRows(ctx context.Context, rows *sql.Rows) ([]core.CanonicalKeyword, error) {
	result := make([]core.CanonicalKeyword, 0)
	for rows.Next() {
		var word core.CanonicalKeyword
		var language, keywordClass string
		var trigger int
		var lastSeen sql.NullString
		if err := rows.Scan(&word.ID, &word.Canonical, &language, &keywordClass, &word.DecisionSource, &trigger, &word.TotalFrequency, &word.FrequencyDelta, &word.MessageCount, &lastSeen); err != nil {
			return nil, err
		}
		word.Language, word.Class, word.TriggerActive = core.Language(language), core.KeywordClass(keywordClass), trigger == 1
		if lastSeen.Valid {
			word.LastSeen, _ = parseTime(lastSeen.String)
		}
		forms, err := s.listCanonicalForms(ctx, word.ID)
		if err != nil {
			return nil, err
		}
		word.Forms = forms
		result = append(result, word)
	}
	return result, rows.Err()
}

func validCanonicalClass(class core.KeywordClass) bool {
	switch class {
	case core.ClassNeutral, core.ClassPositive, core.ClassNegative, core.ClassService:
		return true
	default:
		return false
	}
}

func escapeLike(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, "%", `\%`)
	return strings.ReplaceAll(value, "_", `\_`)
}
