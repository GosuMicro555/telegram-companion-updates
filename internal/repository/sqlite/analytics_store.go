package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	core "telegram-companion/internal/analytics"
	analytictext "telegram-companion/internal/analytics/text"
	"telegram-companion/internal/domain"
	usecaseanalytics "telegram-companion/internal/usecase/analytics"
)

const (
	currentSuccessfulRunKey = "current_successful_run_id"
	analysisResultKeyPrefix = "analysis_result:"
)

type AnalyticsRowSource interface {
	Rows(ctx context.Context, sourceScope string, topics []string) ([]usecaseanalytics.SourceRow, error)
}

type AnalyticsStore struct {
	db       *sql.DB
	source   AnalyticsRowSource
	runHooks *analyticsRunHooks
}

func NewAnalyticsStore(db *sql.DB, sources ...AnalyticsRowSource) *AnalyticsStore {
	var source AnalyticsRowSource
	if len(sources) > 0 {
		source = sources[0]
	}
	return &AnalyticsStore{db: db, source: source}
}

func (s *AnalyticsStore) Rows(ctx context.Context, sourceScope string, topics []string) ([]usecaseanalytics.SourceRow, error) {
	if s == nil || s.source == nil {
		return nil, errors.New("analytics row source is required")
	}
	return s.source.Rows(ctx, sourceScope, topics)
}

func (s *AnalyticsStore) Profile(ctx context.Context, profileID string) (*core.Profile, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	var profile core.Profile
	var positives, exclusions string
	err := s.db.QueryRowContext(ctx, `SELECT id, name, positive_examples_json, exclusions_json
		FROM analysis_profiles WHERE id = ? AND enabled = 1`, profileID).
		Scan(&profile.ID, &profile.Name, &positives, &exclusions)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("analysis profile %q not found", profileID)
	}
	if err != nil {
		return nil, fmt.Errorf("load analysis profile: %w", err)
	}
	if err := json.Unmarshal([]byte(positives), &profile.PositiveExamples); err != nil {
		return nil, fmt.Errorf("decode analysis profile positive examples: %w", err)
	}
	if err := json.Unmarshal([]byte(exclusions), &profile.Exclusions); err != nil {
		return nil, fmt.Errorf("decode analysis profile exclusions: %w", err)
	}
	return &profile, nil
}

func (s *AnalyticsStore) BeginRun(ctx context.Context, runID string, request usecaseanalytics.AnalysisRequest) (usecaseanalytics.RunTransaction, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(runID) == "" {
		return nil, errors.New("analysis run ID is required")
	}
	var profileID any
	if request.ProfileID != "" {
		profileID = request.ProfileID
	}
	var topic any
	if len(request.SourceTopics) > 0 {
		raw, err := json.Marshal(request.SourceTopics)
		if err != nil {
			return nil, fmt.Errorf("encode analysis topics: %w", err)
		}
		topic = string(raw)
	}
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `INSERT INTO analysis_runs
		(id, source_scope, profile_id, topic, status, progress, started_at)
		VALUES (?, ?, ?, ?, 'running', 0, ?)`, runID, request.SourceScope, profileID, topic, formatTime(now))
	if err != nil {
		return nil, fmt.Errorf("create analysis run: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		_, markErr := s.db.ExecContext(context.WithoutCancel(ctx), `UPDATE analysis_runs
			SET status = 'error', completed_at = ?, error = ? WHERE id = ?`, formatTime(time.Now().UTC()), err.Error(), runID)
		return nil, errors.Join(fmt.Errorf("begin analysis run transaction: %w", err), markErr)
	}
	return &analyticsRunTransaction{db: s.db, tx: tx, runID: runID, startedAt: now, hooks: s.runHooks}, nil
}

func (s *AnalyticsStore) PreviousSuccessful(ctx context.Context, _ usecaseanalytics.AnalysisRequest) (usecaseanalytics.AnalysisResult, error) {
	if err := s.ready(); err != nil {
		return usecaseanalytics.AnalysisResult{}, err
	}
	var currentRaw string
	err := s.db.QueryRowContext(ctx, `SELECT value_json FROM app_settings WHERE key = ?`, currentSuccessfulRunKey).Scan(&currentRaw)
	if errors.Is(err, sql.ErrNoRows) {
		return usecaseanalytics.AnalysisResult{}, nil
	}
	if err != nil {
		return usecaseanalytics.AnalysisResult{}, fmt.Errorf("load current successful analysis run: %w", err)
	}
	var runID string
	if err := json.Unmarshal([]byte(currentRaw), &runID); err != nil {
		return usecaseanalytics.AnalysisResult{}, fmt.Errorf("decode current successful analysis run: %w", err)
	}
	var resultRaw string
	err = s.db.QueryRowContext(ctx, `SELECT settings.value_json
		FROM app_settings settings
		JOIN analysis_runs run ON run.id = ? AND run.status = 'complete'
		WHERE settings.key = ?`, runID, analysisResultKeyPrefix+runID).Scan(&resultRaw)
	if errors.Is(err, sql.ErrNoRows) {
		return usecaseanalytics.AnalysisResult{}, errors.New("current successful analysis result is unavailable")
	}
	if err != nil {
		return usecaseanalytics.AnalysisResult{}, fmt.Errorf("load successful analysis result: %w", err)
	}
	var result usecaseanalytics.AnalysisResult
	if err := json.Unmarshal([]byte(resultRaw), &result); err != nil {
		return usecaseanalytics.AnalysisResult{}, fmt.Errorf("decode successful analysis result: %w", err)
	}
	return result, nil
}

func (s *AnalyticsStore) Moderation(ctx context.Context) (map[usecaseanalytics.CandidateKey]string, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT normalized_value, kind, state FROM moderation_decisions`)
	if err != nil {
		return nil, fmt.Errorf("list moderation decisions: %w", err)
	}
	defer rows.Close()
	result := make(map[usecaseanalytics.CandidateKey]string)
	for rows.Next() {
		var key usecaseanalytics.CandidateKey
		var state string
		if err := rows.Scan(&key.NormalizedValue, &key.Kind, &state); err != nil {
			return nil, fmt.Errorf("scan moderation decision: %w", err)
		}
		result[key] = state
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate moderation decisions: %w", err)
	}
	return result, nil
}

func (s *AnalyticsStore) SaveModeration(ctx context.Context, key usecaseanalytics.CandidateKey, state string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if !validModerationState(state) {
		return fmt.Errorf("unsupported moderation state %q", state)
	}
	key = normalizeCandidateKey(key)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin moderation save: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO moderation_decisions
		(normalized_value, kind, state, decided_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(normalized_value, kind) DO UPDATE SET state = excluded.state, decided_at = excluded.decided_at`,
		key.NormalizedValue, key.Kind, state, formatTime(time.Now().UTC())); err != nil {
		return fmt.Errorf("save moderation decision: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE keyword_candidates SET moderation_state = ?
		WHERE normalized_value = ? AND kind = ?`, state, key.NormalizedValue, key.Kind); err != nil {
		return fmt.Errorf("apply moderation decision: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit moderation decision: %w", err)
	}
	return nil
}

func (s *AnalyticsStore) ResetModeration(ctx context.Context, key usecaseanalytics.CandidateKey) error {
	if err := s.ready(); err != nil {
		return err
	}
	key = normalizeCandidateKey(key)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin moderation reset: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM moderation_decisions WHERE normalized_value = ? AND kind = ?`, key.NormalizedValue, key.Kind); err != nil {
		return fmt.Errorf("reset moderation decision: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE keyword_candidates SET moderation_state = 'new'
		WHERE normalized_value = ? AND kind = ?`, key.NormalizedValue, key.Kind); err != nil {
		return fmt.Errorf("reset candidate moderation state: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit moderation reset: %w", err)
	}
	return nil
}

func (s *AnalyticsStore) VisibleCandidates(ctx context.Context, refs []usecaseanalytics.CandidateRef) ([]usecaseanalytics.StoredCandidate, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	result := make([]usecaseanalytics.StoredCandidate, 0, len(refs))
	seen := make(map[usecaseanalytics.CandidateRef]struct{}, len(refs))
	for _, ref := range refs {
		key := normalizeCandidateKey(usecaseanalytics.CandidateKey{NormalizedValue: ref.NormalizedValue, Kind: ref.Kind})
		ref = usecaseanalytics.CandidateRef{RunID: strings.TrimSpace(ref.RunID), NormalizedValue: key.NormalizedValue, Kind: key.Kind}
		if _, ok := seen[ref]; ok {
			continue
		}
		seen[ref] = struct{}{}
		var stored usecaseanalytics.StoredCandidate
		stored.RunID = ref.RunID
		err := s.db.QueryRowContext(ctx, `SELECT normalized_value, display_value, kind, frequency, source_diversity, score, moderation_state
			FROM keyword_candidates
			WHERE run_id = ? AND normalized_value = ? AND kind = ?
				AND moderation_state IN ('new', 'accepted')`, ref.RunID, ref.NormalizedValue, ref.Kind).
			Scan(&stored.Candidate.NormalizedValue, &stored.Candidate.DisplayValue, &stored.Candidate.Kind,
				&stored.Candidate.Frequency, &stored.Candidate.SourceDiversity, &stored.Candidate.Score, &stored.ModerationState)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("load requested keyword candidate: %w", err)
		}
		result = append(result, stored)
	}
	return result, nil
}

func (s *AnalyticsStore) Keywords(ctx context.Context) ([]string, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	settings, err := NewSettingsStore(s.db).Load(ctx)
	if err != nil {
		return nil, err
	}
	return append([]string(nil), settings.Keywords...), nil
}

func (s *AnalyticsStore) AddKeyword(ctx context.Context, keyword string) error {
	if err := s.ready(); err != nil {
		return err
	}
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return errors.New("keyword is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin keyword add: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	settings, err := loadKeywordSettingsTx(ctx, tx)
	if err != nil {
		return err
	}
	for _, existing := range settings.Keywords {
		if strings.EqualFold(strings.TrimSpace(existing), keyword) {
			return tx.Commit()
		}
	}
	settings.Keywords = append(settings.Keywords, keyword)
	if err := saveKeywordSettings(ctx, s.db, tx, settings); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit keyword add: %w", err)
	}
	return nil
}

func (s *AnalyticsStore) ready() error {
	if s == nil || s.db == nil {
		return errors.New("analytics database is required")
	}
	return nil
}

type analyticsRunTransaction struct {
	db         *sql.DB
	tx         *sql.Tx
	runID      string
	startedAt  time.Time
	mu         sync.Mutex
	finished   bool
	aggregates bool
	hooks      *analyticsRunHooks
}

type analyticsRunHooks struct {
	beforeFinalization   func()
	afterPointOfNoReturn func()
	commit               func(*sql.Tx) error
}

func (t *analyticsRunTransaction) SaveCandidate(ctx context.Context, candidate core.Candidate, moderationState string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.usable(); err != nil {
		return err
	}
	if !validModerationState(moderationState) {
		return fmt.Errorf("unsupported candidate moderation state %q", moderationState)
	}
	_, err := t.tx.ExecContext(ctx, `INSERT INTO keyword_candidates
		(id, run_id, normalized_value, display_value, kind, frequency, source_diversity, score, source, moderation_state, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'telegram', ?, ?)`, candidateID(t.runID, candidate), t.runID,
		candidate.NormalizedValue, candidate.DisplayValue, candidate.Kind, candidate.Frequency,
		candidate.SourceDiversity, candidate.Score, moderationState, formatTime(time.Now().UTC()))
	if err != nil {
		return fmt.Errorf("save analysis candidate: %w", err)
	}
	return nil
}

func (t *analyticsRunTransaction) SaveAggregates(ctx context.Context, result usecaseanalytics.AnalysisResult) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.usable(); err != nil {
		return err
	}
	if result.RunID != t.runID {
		return errors.New("analysis result run ID does not match transaction")
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("encode analysis aggregates: %w", err)
	}
	if err := saveAppSettingTx(ctx, t.tx, analysisResultKeyPrefix+t.runID, raw); err != nil {
		return fmt.Errorf("save analysis aggregates: %w", err)
	}
	t.aggregates = true
	return nil
}

func (t *analyticsRunTransaction) Commit(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.usable(); err != nil {
		return err
	}
	if !t.aggregates {
		return errors.New("analysis aggregates must be saved before commit")
	}
	var candidateCount int
	if err := t.tx.QueryRowContext(ctx, `SELECT count(*) FROM keyword_candidates WHERE run_id = ?`, t.runID).Scan(&candidateCount); err != nil {
		return fmt.Errorf("count analysis candidates: %w", err)
	}
	currentRaw, err := json.Marshal(t.runID)
	if err != nil {
		return fmt.Errorf("encode current analysis run: %w", err)
	}
	if t.hooks != nil && t.hooks.beforeFinalization != nil {
		t.hooks.beforeFinalization()
	}
	// This is the final cancellation boundary. Past it, finalization must reach
	// an authoritative durable outcome even if the caller is cancelled.
	if err := ctx.Err(); err != nil {
		return err
	}
	finalizeCtx := context.WithoutCancel(ctx)
	if t.hooks != nil && t.hooks.afterPointOfNoReturn != nil {
		t.hooks.afterPointOfNoReturn()
	}
	completedAt := time.Now().UTC()
	if _, err := t.tx.ExecContext(finalizeCtx, `UPDATE analysis_runs SET status = 'complete', progress = 100,
		candidate_count = ?, completed_at = ?, error = '' WHERE id = ?`, candidateCount, formatTime(completedAt), t.runID); err != nil {
		return fmt.Errorf("complete analysis run: %w", err)
	}
	// This is deliberately the final statement: the pointer and all run data
	// become visible together only when the SQL transaction commits.
	if err := saveAppSettingTx(finalizeCtx, t.tx, currentSuccessfulRunKey, currentRaw); err != nil {
		return fmt.Errorf("switch current successful analysis run: %w", err)
	}
	commit := t.tx.Commit
	if t.hooks != nil && t.hooks.commit != nil {
		commit = func() error { return t.hooks.commit(t.tx) }
	}
	if err := commit(); err != nil {
		durable, resolveErr := t.isDurablyCommitted(finalizeCtx)
		if resolveErr != nil {
			return errors.Join(fmt.Errorf("commit analysis run: %w", err), fmt.Errorf("resolve analysis commit outcome: %w", resolveErr))
		}
		if !durable {
			return fmt.Errorf("commit analysis run: %w", err)
		}
	}
	t.finished = true
	return nil
}

func (t *analyticsRunTransaction) isDurablyCommitted(ctx context.Context) (bool, error) {
	var status string
	if err := t.db.QueryRowContext(ctx, `SELECT status FROM analysis_runs WHERE id = ?`, t.runID).Scan(&status); err != nil {
		return false, fmt.Errorf("load analysis run status: %w", err)
	}
	var currentRaw string
	if err := t.db.QueryRowContext(ctx, `SELECT value_json FROM app_settings WHERE key = ?`, currentSuccessfulRunKey).Scan(&currentRaw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("load current successful run: %w", err)
	}
	var currentRunID string
	if err := json.Unmarshal([]byte(currentRaw), &currentRunID); err != nil {
		return false, fmt.Errorf("decode current successful run: %w", err)
	}
	return status == "complete" && currentRunID == t.runID, nil
}

func (t *analyticsRunTransaction) Abort(ctx context.Context, status, message string) error {
	if t == nil || t.db == nil || t.tx == nil {
		return errors.New("analysis run transaction is not initialized")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.finished {
		return errors.New("analysis run transaction is already finished")
	}
	if status != "partial" && status != "error" {
		return fmt.Errorf("unsupported analysis abort status %q", status)
	}
	rollbackErr := t.tx.Rollback()
	if errors.Is(rollbackErr, sql.ErrTxDone) {
		rollbackErr = nil
	}
	result, markerErr := t.db.ExecContext(ctx, `UPDATE analysis_runs
		SET status = ?, completed_at = ?, error = ? WHERE id = ?`, status, formatTime(time.Now().UTC()), message, t.runID)
	if markerErr == nil {
		var affected int64
		affected, markerErr = result.RowsAffected()
		if markerErr == nil && affected != 1 {
			markerErr = fmt.Errorf("analysis run marker updated %d rows", affected)
		}
	}
	if markerErr != nil {
		return errors.Join(rollbackErr, fmt.Errorf("persist analysis run %s marker: %w", status, markerErr))
	}
	t.finished = true
	if rollbackErr != nil {
		return fmt.Errorf("rollback analysis run: %w", rollbackErr)
	}
	return nil
}

func (t *analyticsRunTransaction) usable() error {
	if t == nil || t.tx == nil || t.db == nil {
		return errors.New("analysis run transaction is not initialized")
	}
	if t.finished {
		return errors.New("analysis run transaction is already finished")
	}
	return nil
}

func saveAppSettingTx(ctx context.Context, tx *sql.Tx, key string, raw []byte) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO app_settings (key, value_json, revision, updated_at)
		VALUES (?, ?, 1, ?)
		ON CONFLICT(key) DO UPDATE SET value_json = excluded.value_json,
			revision = app_settings.revision + 1, updated_at = excluded.updated_at`, key, string(raw), formatTime(time.Now().UTC()))
	return err
}

func loadKeywordSettingsTx(ctx context.Context, tx *sql.Tx) (domain.KeywordSettings, error) {
	settings := domain.DefaultKeywordSettings()
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT value_json FROM app_settings WHERE key = ?`, keywordSettingsKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return settings, nil
	}
	if err != nil {
		return domain.KeywordSettings{}, fmt.Errorf("load keyword settings: %w", err)
	}
	if err := json.Unmarshal([]byte(raw), &settings); err != nil {
		return domain.KeywordSettings{}, fmt.Errorf("decode keyword settings: %w", err)
	}
	return settings, nil
}

func normalizeCandidateKey(key usecaseanalytics.CandidateKey) usecaseanalytics.CandidateKey {
	return usecaseanalytics.CandidateKey{
		NormalizedValue: analytictext.Normalize(key.NormalizedValue),
		Kind:            strings.ToLower(strings.TrimSpace(key.Kind)),
	}
}

func validModerationState(state string) bool {
	switch state {
	case "new", "accepted", "rejected", "added_to_keywords":
		return true
	default:
		return false
	}
}

func candidateID(runID string, candidate core.Candidate) string {
	sum := sha256.Sum256([]byte(runID + "\x00" + candidate.NormalizedValue + "\x00" + candidate.Kind + "\x00telegram"))
	return hex.EncodeToString(sum[:])
}

var _ usecaseanalytics.Store = (*AnalyticsStore)(nil)
var _ usecaseanalytics.RunTransaction = (*analyticsRunTransaction)(nil)
