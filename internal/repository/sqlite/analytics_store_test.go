package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"telegram-companion/internal/domain"
	usecaseanalytics "telegram-companion/internal/usecase/analytics"

	"github.com/stretchr/testify/require"
)

func TestAnalyticsStoreCommitsAtomicallyAndFailureKeepsPreviousCurrent(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "analytics.db")
	db, err := Open(ctx, path)
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, NewSettingsStore(db).Save(ctx, domain.KeywordSettings{}))

	source := &analyticsRows{rows: []usecaseanalytics.SourceRow{{Text: "не хватает денег", Topic: "buyers", SourceID: "chat-1"}}}
	store := NewAnalyticsStore(db, source)
	analyzer := usecaseanalytics.NewAnalyzerWithIDGenerator(store, nil, sequenceRunIDs("run-1", "run-2", "run-3"))
	first, err := analyzer.Analyze(ctx, usecaseanalytics.AnalysisRequest{SourceScope: usecaseanalytics.ScopeAll})
	require.NoError(t, err)
	require.Equal(t, "run-1", first.RunID)
	require.Equal(t, "run-1", currentSuccessfulRunID(t, db))
	require.Positive(t, candidateCount(t, db, "run-1"))
	require.Equal(t, "complete", runStatus(t, db, "run-1"))

	_, err = db.ExecContext(ctx, `CREATE TRIGGER fail_run_2_aggregates
		BEFORE INSERT ON app_settings
		WHEN NEW.key = 'analysis_result:run-2'
		BEGIN SELECT RAISE(ABORT, 'forced aggregate failure'); END`)
	require.NoError(t, err)
	source.rows = []usecaseanalytics.SourceRow{{Text: "слишком дорого", Topic: "buyers", SourceID: "chat-2"}}
	second, err := analyzer.Analyze(ctx, usecaseanalytics.AnalysisRequest{SourceScope: usecaseanalytics.ScopeAll})
	require.ErrorContains(t, err, "forced aggregate failure")
	require.Equal(t, first, second)
	require.Equal(t, "run-1", currentSuccessfulRunID(t, db))
	require.Zero(t, candidateCount(t, db, "run-2"))
	require.Equal(t, "error", runStatus(t, db, "run-2"))

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	third, err := analyzer.Analyze(cancelled, usecaseanalytics.AnalysisRequest{SourceScope: usecaseanalytics.ScopeAll})
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, first, third)
	require.Equal(t, "partial", runStatus(t, db, "run-3"))
	require.Equal(t, "run-1", currentSuccessfulRunID(t, db))
}

func TestAnalyticsStoreModerationKeywordsAndRequestedCandidatesPersistAcrossReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "analytics.db")
	db, err := Open(ctx, path)
	require.NoError(t, err)
	require.NoError(t, NewSettingsStore(db).Save(ctx, domain.KeywordSettings{}))

	source := &analyticsRows{rows: []usecaseanalytics.SourceRow{{Text: "нет денег и слишком дорого", Topic: "buyers", SourceID: "chat-1"}}}
	store := NewAnalyticsStore(db, source)
	result, err := usecaseanalytics.NewAnalyzerWithIDGenerator(store, nil, func() string { return "run-1" }).Analyze(ctx, usecaseanalytics.AnalysisRequest{SourceScope: usecaseanalytics.ScopeAll})
	require.NoError(t, err)
	require.NotEmpty(t, result.GeneralPhrases)

	requested := result.GeneralPhrases[0]
	refs := []usecaseanalytics.CandidateRef{{RunID: result.RunID, NormalizedValue: requested.NormalizedValue, Kind: requested.Kind}}
	visible, err := store.VisibleCandidates(ctx, refs)
	require.NoError(t, err)
	require.Len(t, visible, 1)
	require.Equal(t, result.RunID, visible[0].RunID)
	require.Equal(t, requested.NormalizedValue, visible[0].Candidate.NormalizedValue)

	analyzer := usecaseanalytics.NewAnalyzer(store, nil)
	require.NoError(t, analyzer.Moderate(ctx, requested.NormalizedValue, requested.Kind, "accepted"))
	added, err := analyzer.AddCandidatesToKeywords(ctx, refs)
	require.NoError(t, err)
	require.Equal(t, 1, added)
	require.NoError(t, store.SaveModeration(ctx, usecaseanalytics.CandidateKey{NormalizedValue: "нет денег", Kind: "phrase"}, "rejected"))
	require.NoError(t, db.Close())

	reopened, err := Open(ctx, path)
	require.NoError(t, err)
	defer reopened.Close()
	reopenedStore := NewAnalyticsStore(reopened, source)
	moderation, err := reopenedStore.Moderation(ctx)
	require.NoError(t, err)
	require.Equal(t, "rejected", moderation[usecaseanalytics.CandidateKey{NormalizedValue: "нет денег", Kind: "phrase"}])
	keywords, err := reopenedStore.Keywords(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{requested.DisplayValue}, keywords)
	previous, err := reopenedStore.PreviousSuccessful(ctx, usecaseanalytics.AnalysisRequest{SourceScope: usecaseanalytics.ScopeAll})
	require.NoError(t, err)
	require.Equal(t, result, previous)
}

func TestAnalyticsRunAbortFailureIsReturned(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "analytics.db"))
	require.NoError(t, err)
	defer db.Close()
	store := NewAnalyticsStore(db, &analyticsRows{})
	tx, err := store.BeginRun(ctx, "run-abort", usecaseanalytics.AnalysisRequest{SourceScope: usecaseanalytics.ScopeAll})
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `CREATE TRIGGER fail_partial_marker
		BEFORE UPDATE ON analysis_runs
		WHEN NEW.id = 'run-abort'
		BEGIN SELECT RAISE(ABORT, 'forced partial marker failure'); END`)
	require.NoError(t, err)

	err = tx.Abort(ctx, "partial", context.Canceled.Error())

	require.ErrorContains(t, err, "forced partial marker failure")
}

func TestAnalyticsRunCommitPointOfNoReturn(t *testing.T) {
	t.Run("cancellation immediately before finalization marks partial", func(t *testing.T) {
		ctx, db, store, analyzer, previous := newCommitBoundaryHarness(t)
		cancelled, cancel := context.WithCancel(ctx)
		store.runHooks = &analyticsRunHooks{beforeFinalization: cancel}

		result, err := analyzer.Analyze(cancelled, usecaseanalytics.AnalysisRequest{SourceScope: usecaseanalytics.ScopeAll})

		require.ErrorIs(t, err, context.Canceled)
		require.Equal(t, previous, result)
		require.Equal(t, "partial", runStatus(t, db, "boundary-run"))
		require.Equal(t, previous.RunID, currentSuccessfulRunID(t, db))
	})

	t.Run("cancellation after point of no return remains committed success", func(t *testing.T) {
		ctx, db, store, analyzer, _ := newCommitBoundaryHarness(t)
		cancelled, cancel := context.WithCancel(ctx)
		store.runHooks = &analyticsRunHooks{afterPointOfNoReturn: cancel}

		result, err := analyzer.Analyze(cancelled, usecaseanalytics.AnalysisRequest{SourceScope: usecaseanalytics.ScopeAll})

		require.NoError(t, err)
		require.ErrorIs(t, cancelled.Err(), context.Canceled)
		require.Equal(t, "boundary-run", result.RunID)
		require.Equal(t, "complete", runStatus(t, db, "boundary-run"))
		require.Equal(t, "boundary-run", currentSuccessfulRunID(t, db))
	})

	t.Run("commit error before durability returns error and keeps prior current", func(t *testing.T) {
		ctx, db, store, analyzer, previous := newCommitBoundaryHarness(t)
		commitErr := errors.New("commit failed before durability")
		store.runHooks = &analyticsRunHooks{commit: func(tx *sql.Tx) error {
			require.NoError(t, tx.Rollback())
			return commitErr
		}}

		result, err := analyzer.Analyze(ctx, usecaseanalytics.AnalysisRequest{SourceScope: usecaseanalytics.ScopeAll})

		require.ErrorIs(t, err, commitErr)
		require.Equal(t, previous, result)
		require.Equal(t, "error", runStatus(t, db, "boundary-run"))
		require.Equal(t, previous.RunID, currentSuccessfulRunID(t, db))
	})

	t.Run("error reported after durable commit resolves as success", func(t *testing.T) {
		ctx, db, store, analyzer, _ := newCommitBoundaryHarness(t)
		reportedErr := errors.New("driver reported commit error after durability")
		store.runHooks = &analyticsRunHooks{commit: func(tx *sql.Tx) error {
			require.NoError(t, tx.Commit())
			return reportedErr
		}}

		result, err := analyzer.Analyze(ctx, usecaseanalytics.AnalysisRequest{SourceScope: usecaseanalytics.ScopeAll})

		require.NoError(t, err)
		require.Equal(t, "boundary-run", result.RunID)
		require.Equal(t, "complete", runStatus(t, db, "boundary-run"))
		require.Equal(t, "boundary-run", currentSuccessfulRunID(t, db))
		var storedError string
		require.NoError(t, db.QueryRow(`SELECT error FROM analysis_runs WHERE id = 'boundary-run'`).Scan(&storedError))
		require.Empty(t, storedError)
	})
}

func newCommitBoundaryHarness(t *testing.T) (context.Context, *sql.DB, *AnalyticsStore, *usecaseanalytics.Analyzer, usecaseanalytics.AnalysisResult) {
	t.Helper()
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "analytics.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	source := &analyticsRows{rows: []usecaseanalytics.SourceRow{{Text: "нет денег", Topic: "buyers", SourceID: "chat-1"}}}
	store := NewAnalyticsStore(db, source)
	previous, err := usecaseanalytics.NewAnalyzerWithIDGenerator(store, nil, func() string { return "previous-run" }).Analyze(ctx, usecaseanalytics.AnalysisRequest{SourceScope: usecaseanalytics.ScopeAll})
	require.NoError(t, err)
	source.rows = []usecaseanalytics.SourceRow{{Text: "слишком дорого", Topic: "buyers", SourceID: "chat-2"}}
	analyzer := usecaseanalytics.NewAnalyzerWithIDGenerator(store, nil, func() string { return "boundary-run" })
	return ctx, db, store, analyzer, previous
}

type analyticsRows struct {
	rows []usecaseanalytics.SourceRow
	err  error
}

func (s *analyticsRows) Rows(context.Context, string, []string) ([]usecaseanalytics.SourceRow, error) {
	return append([]usecaseanalytics.SourceRow(nil), s.rows...), s.err
}

func sequenceRunIDs(values ...string) func() string {
	index := 0
	return func() string {
		value := values[index]
		index++
		return value
	}
}

func currentSuccessfulRunID(t *testing.T, db *sql.DB) string {
	t.Helper()
	var raw string
	require.NoError(t, db.QueryRow(`SELECT value_json FROM app_settings WHERE key = 'current_successful_run_id'`).Scan(&raw))
	var runID string
	require.NoError(t, json.Unmarshal([]byte(raw), &runID))
	return runID
}

func candidateCount(t *testing.T, db *sql.DB, runID string) int {
	t.Helper()
	var count int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM keyword_candidates WHERE run_id = ?`, runID).Scan(&count))
	return count
}

func runStatus(t *testing.T, db *sql.DB, runID string) string {
	t.Helper()
	var status string
	require.NoError(t, db.QueryRow(`SELECT status FROM analysis_runs WHERE id = ?`, runID).Scan(&status))
	return status
}

var _ usecaseanalytics.Store = (*AnalyticsStore)(nil)
var _ usecaseanalytics.RunTransaction = (*analyticsRunTransaction)(nil)
