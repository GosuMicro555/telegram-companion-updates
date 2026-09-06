package analytics

import (
	"context"
	"errors"
	"testing"

	core "telegram-companion/internal/analytics"

	"github.com/stretchr/testify/require"
)

func TestAnalyzeFiltersTopicBeforeAggregationAndCommits(t *testing.T) {
	store := newFakeStore([]SourceRow{
		{Text: "не хватает денег", Topic: "buyers", SourceID: "1"},
		{Text: "совсем другая фраза", Topic: "sellers", SourceID: "2"},
	})
	analyzer := NewAnalyzerWithIDGenerator(store, nil, func() string { return "run-1" })
	result, err := analyzer.Analyze(context.Background(), AnalysisRequest{SourceScope: ScopeTopic, SourceTopics: []string{"buyers"}})
	require.NoError(t, err)
	require.Equal(t, "run-1", result.RunID)
	require.Equal(t, "complete", result.Status)
	require.True(t, store.committed)
	for _, candidate := range append(result.GeneralPhrases, result.GeneralWords...) {
		require.NotContains(t, candidate.DisplayValue, "другая")
	}
}

func TestAnalyzeFailureAbortsAndReturnsPreviousSuccessfulResult(t *testing.T) {
	previous := AnalysisResult{RunID: "previous", Status: "complete"}
	store := newFakeStore([]SourceRow{{Text: "нет денег", Topic: "buyers", SourceID: "1"}})
	store.previous = previous
	store.saveErr = errors.New("candidate write failed")
	analyzer := NewAnalyzerWithIDGenerator(store, nil, func() string { return "failed" })
	result, err := analyzer.Analyze(context.Background(), AnalysisRequest{SourceScope: ScopeAll})
	require.ErrorContains(t, err, "candidate write failed")
	require.Equal(t, previous, result)
	require.Equal(t, "error", store.abortedStatus)
	require.False(t, store.committed)
}

func TestAnalyzeCancellationMarksPartialAndKeepsPrevious(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	store := newFakeStore([]SourceRow{{Text: "нет денег", SourceID: "1"}})
	store.previous = AnalysisResult{RunID: "previous", Status: "complete"}
	analyzer := NewAnalyzerWithIDGenerator(store, nil, func() string { return "partial" })
	result, err := analyzer.Analyze(ctx, AnalysisRequest{SourceScope: ScopeAll})
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, "previous", result.RunID)
	require.Equal(t, "partial", store.abortedStatus)
}

func TestAnalyzeCancellationAbortFailureReturnsJoinedErrorWithoutFallback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	store := newFakeStore([]SourceRow{{Text: "нет денег", SourceID: "1"}})
	store.previous = AnalysisResult{RunID: "previous", Status: "complete"}
	store.abortErr = errors.New("persist partial marker failed")
	analyzer := NewAnalyzerWithIDGenerator(store, nil, func() string { return "partial" })

	result, err := analyzer.Analyze(ctx, AnalysisRequest{SourceScope: ScopeAll})

	require.ErrorIs(t, err, context.Canceled)
	require.ErrorContains(t, err, "persist partial marker failed")
	require.Empty(t, result)
	require.Zero(t, store.previousCalls)
}

func TestAnalyzeRestoresRejectedCandidatesOnNextRun(t *testing.T) {
	store := newFakeStore([]SourceRow{{Text: "нет денег", SourceID: "1"}})
	store.requireCommittedBeforeReset = true
	analyzer := NewAnalyzerWithIDGenerator(store, nil, sequentialIDs())
	require.NoError(t, analyzer.Moderate(context.Background(), "нет денег", "phrase", "rejected"))
	result, err := analyzer.Analyze(context.Background(), AnalysisRequest{SourceScope: ScopeAll})
	require.NoError(t, err)
	require.Contains(t, candidateValues(result), "нет денег")
	require.Empty(t, store.moderation)
	require.False(t, store.resetBeforeCommit)
}

func TestAddCandidatesToKeywordsUsesVisibleRowsAndCaseInsensitiveDedupe(t *testing.T) {
	store := newFakeStore(nil)
	store.keywords = []string{"НЕТ ДЕНЕГ"}
	store.visible = []StoredCandidate{
		{RunID: "run", Candidate: core.Candidate{DisplayValue: "нет денег", NormalizedValue: "нет денег", Kind: "phrase"}, ModerationState: "new"},
		{RunID: "other-run", Candidate: core.Candidate{DisplayValue: "слишком дорого", NormalizedValue: "слишком дорого", Kind: "phrase"}, ModerationState: "accepted"},
		{RunID: "run", Candidate: core.Candidate{DisplayValue: "скрыто", NormalizedValue: "скрыто", Kind: "word"}, ModerationState: "rejected"},
	}
	analyzer := NewAnalyzer(store, nil)
	count, err := analyzer.AddCandidatesToKeywords(context.Background(), []CandidateRef{{RunID: "run", NormalizedValue: "нет денег", Kind: "phrase"}})
	require.NoError(t, err)
	require.Equal(t, 0, count)
	require.Equal(t, []string{"НЕТ ДЕНЕГ"}, store.keywords)
}

func TestCompareTopicsUsesAggregateCountsOnly(t *testing.T) {
	store := newFakeStore([]SourceRow{
		{Text: "нет денег", Topic: "a", SourceID: "1"},
		{Text: "не хватает денег", Topic: "a", SourceID: "2"},
		{Text: "слишком дорого", Topic: "b", SourceID: "3"},
	})
	analyzer := NewAnalyzer(store, nil)
	got, err := analyzer.CompareTopics(context.Background(), TopicComparisonRequest{Topics: []string{"a", "b"}})
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, 2, got[0].Count)
	require.InDelta(t, 2.0/3.0, got[0].RelativeShare, 0.0001)
	require.NotZero(t, got[0].DistinctCandidateCount)
	require.LessOrEqual(t, len(got[0].TopPhrases), 10)
}

func TestModerationStatesReturnsPersistedStatesWithoutSharingStoreMap(t *testing.T) {
	store := newFakeStore(nil)
	key := CandidateKey{NormalizedValue: "нет денег", Kind: "phrase"}
	store.moderation[key] = "accepted"

	got, err := NewAnalyzer(store, nil).ModerationStates(context.Background())
	require.NoError(t, err)
	require.Equal(t, "accepted", got[key])
	got[key] = "rejected"
	require.Equal(t, "accepted", store.moderation[key])
}

func TestExportKeywordsExportsRulesWithoutProvenance(t *testing.T) {
	store := newFakeStore(nil)
	store.keywords = []string{"нет денег", "слишком дорого"}
	exporter := &fakeExporter{path: "/data/exports/keywords.txt"}
	path, err := NewAnalyzer(store, exporter).ExportKeywords(context.Background())
	require.NoError(t, err)
	require.Equal(t, exporter.path, path)
	require.Equal(t, store.keywords, exporter.values)
}

type fakeStore struct {
	rows                        []SourceRow
	previous                    AnalysisResult
	moderation                  map[CandidateKey]string
	visible                     []StoredCandidate
	keywords                    []string
	saved                       []StoredCandidate
	saveErr                     error
	abortErr                    error
	committed                   bool
	abortedStatus               string
	previousCalls               int
	requireCommittedBeforeReset bool
	resetBeforeCommit           bool
}

func newFakeStore(rows []SourceRow) *fakeStore {
	return &fakeStore{rows: rows, moderation: map[CandidateKey]string{}}
}

func (s *fakeStore) Rows(context.Context, string, []string) ([]SourceRow, error) { return s.rows, nil }
func (s *fakeStore) Profile(context.Context, string) (*core.Profile, error) {
	return core.DefaultMoneyShortageProfile(), nil
}
func (s *fakeStore) BeginRun(context.Context, string, AnalysisRequest) (RunTransaction, error) {
	return s, nil
}
func (s *fakeStore) PreviousSuccessful(context.Context, AnalysisRequest) (AnalysisResult, error) {
	s.previousCalls++
	return s.previous, nil
}
func (s *fakeStore) Moderation(context.Context) (map[CandidateKey]string, error) {
	return s.moderation, nil
}
func (s *fakeStore) SaveModeration(_ context.Context, key CandidateKey, state string) error {
	s.moderation[key] = state
	return nil
}
func (s *fakeStore) ResetModeration(_ context.Context, key CandidateKey) error {
	if s.requireCommittedBeforeReset && !s.committed {
		s.resetBeforeCommit = true
		return errors.New("moderation reset must follow analysis commit")
	}
	delete(s.moderation, key)
	return nil
}
func (s *fakeStore) VisibleCandidates(context.Context, []CandidateRef) ([]StoredCandidate, error) {
	return s.visible, nil
}
func (s *fakeStore) Keywords(context.Context) ([]string, error) {
	return append([]string(nil), s.keywords...), nil
}
func (s *fakeStore) AddKeyword(_ context.Context, keyword string) error {
	s.keywords = append(s.keywords, keyword)
	return nil
}
func (s *fakeStore) SaveCandidate(_ context.Context, candidate core.Candidate, state string) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	s.saved = append(s.saved, StoredCandidate{Candidate: candidate, ModerationState: state})
	return nil
}
func (s *fakeStore) SaveAggregates(context.Context, AnalysisResult) error { return nil }
func (s *fakeStore) Commit(context.Context) error                         { s.committed = true; return nil }
func (s *fakeStore) Abort(_ context.Context, status, _ string) error {
	s.abortedStatus = status
	return s.abortErr
}

type fakeExporter struct {
	path   string
	values []string
}

func (e *fakeExporter) Export(_ context.Context, values []string) (string, error) {
	e.values = append([]string(nil), values...)
	return e.path, nil
}

func candidateValues(result AnalysisResult) []string {
	all := append(append([]core.Candidate(nil), result.GeneralPhrases...), result.GeneralWords...)
	values := make([]string, 0, len(all))
	for _, candidate := range all {
		values = append(values, candidate.NormalizedValue)
	}
	return values
}

func sequentialIDs() func() string {
	n := 0
	return func() string { n++; return string(rune('0' + n)) }
}
