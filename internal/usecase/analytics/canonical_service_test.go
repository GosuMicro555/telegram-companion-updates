package analytics

import (
	"context"
	"strings"
	"testing"
	"time"

	core "telegram-companion/internal/analytics"
	"telegram-companion/internal/domain"

	"github.com/stretchr/testify/require"
)

func TestCanonicalServiceAnalyzesAllRowsAndExposesClassOperations(t *testing.T) {
	seen := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	store := &fakeCanonicalStore{
		rows: []SourceRow{{Text: "деньги денег run running", SourceID: "chat", MessageID: "chat:1", MessageAt: seen}},
	}
	service := NewCanonicalService(store)

	result, err := service.Analyze(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, store.syncCalls)
	require.Len(t, store.synced, 2)
	require.Equal(t, int64(1), result.MessageCount)
	require.Equal(t, 2, result.KeywordCount)

	require.NoError(t, service.Classify(context.Background(), "money-id", core.ClassPositive))
	require.Equal(t, core.ClassPositive, store.classified)
	require.NoError(t, service.SetTriggerActive(context.Background(), "money-id", true))
	require.True(t, store.triggerActive)
	require.NoError(t, service.Clear(context.Background(), core.ClassNegative))
	require.Equal(t, core.ClassNegative, store.cleared)
}

func TestCanonicalServiceExposesSearchMetricsAndSeparateTriggerDeletion(t *testing.T) {
	store := &fakeCanonicalStore{}
	service := NewCanonicalService(store)

	words, err := service.Search(context.Background(), "running", core.ClassService)
	require.NoError(t, err)
	require.Empty(t, words)
	_, err = service.Metrics(context.Background())
	require.NoError(t, err)
	require.NoError(t, service.DeleteTrigger(context.Background(), "positive-word"))

	require.Equal(t, "running", store.searchQuery)
	require.Equal(t, core.ClassService, store.searchClass)
	require.Equal(t, "positive-word", store.deletedTrigger)
}

func TestCanonicalServiceExcludesCaseNormalizedServiceCanonicalAndForms(t *testing.T) {
	store := &fakeCanonicalStore{
		rows: []SourceRow{{Text: "THE running purchase \u0434\u043b\u044f \u043f\u043e\u043a\u0443\u043f\u0430\u0442\u044c", SourceID: "chat", MessageID: "chat:1"}},
	}
	provider := &fakeServiceWordProvider{words: map[domain.AnalyticsLanguage][]string{
		domain.AnalyticsLanguageRU: {" \u0414\u041b\u042f "},
		domain.AnalyticsLanguageEN: {"the", "RUNNING"},
	}}
	service := NewCanonicalService(store, provider)

	result, err := service.Analyze(context.Background())

	require.NoError(t, err)
	require.Equal(t, []domain.AnalyticsLanguage{domain.AnalyticsLanguageRU, domain.AnalyticsLanguageEN}, provider.languages)
	require.Equal(t, 2, result.KeywordCount)
	require.Len(t, store.synced, 2)
	for _, observation := range store.synced {
		require.NotEqual(t, "the", observation.Canonical)
		require.NotEqual(t, "run", observation.Canonical)
		require.NotEqual(t, "\u0434\u043b\u044f", observation.Canonical)
	}
}

func TestCanonicalServiceDeletesExistingServiceWordCanonicalBeforeSync(t *testing.T) {
	store := &fakeCanonicalStore{searchResults: []core.CanonicalKeyword{
		{ID: "positive-trigger", Canonical: "running", Language: core.LanguageEN, Class: core.ClassPositive, TriggerActive: true},
		{ID: "keep", Canonical: "purchase", Language: core.LanguageEN, Class: core.ClassPositive, TriggerActive: true},
		{ID: "form-match", Canonical: "run", Language: core.LanguageEN, Forms: []core.CanonicalKeywordForm{{Value: "running"}}},
	}}
	provider := &fakeServiceWordProvider{words: map[domain.AnalyticsLanguage][]string{
		domain.AnalyticsLanguageRU: {},
		domain.AnalyticsLanguageEN: {" RUNNING "},
	}}
	service := NewCanonicalService(store, provider)

	_, err := service.Analyze(context.Background())

	require.NoError(t, err)
	require.Equal(t, []string{"positive-trigger", "form-match"}, store.deletedCanonicals)
}

func TestCanonicalServiceBulkImportNormalizesCanonicalFormsAndSkipsInvalidValues(t *testing.T) {
	store := &fakeCanonicalStore{bulkImportResult: core.CanonicalBulkImportResult{Added: 1, Updated: 1}}
	service := NewCanonicalService(store)

	result, err := service.BulkImport(context.Background(), []core.CanonicalImportValue{
		{Value: " Бабки ", Forms: []string{" Бабосы ", "БАБОСЫ", "бабосики", "баблишко", "money", "два слова", "!"}},
		{Value: "бабки", Forms: []string{"деньги"}},
		{Value: "abcя", Forms: []string{"mixed"}},
	}, core.ClassPositive)

	require.NoError(t, err)
	require.Equal(t, core.CanonicalBulkImportResult{Added: 1, Updated: 1, Skipped: 5}, result)
	require.Equal(t, core.ClassPositive, store.bulkImportClass)
	require.Equal(t, []core.CanonicalImportValue{
		{Value: "бабки", Language: core.LanguageRU, Forms: []string{"бабосы", "бабосики", "баблишко"}},
	}, store.bulkImportValues)
}

func TestNormalizeCanonicalImportValueAcceptsPhrases(t *testing.T) {
	value, language, ok := normalizeCanonicalImportValue("\u043c\u0430\u0448\u0438\u043d\u0430 \u0435\u0434\u0435\u0442")
	require.True(t, ok)
	require.Equal(t, "\u043c\u0430\u0448\u0438\u043d\u0430 \u0435\u0434\u0435\u0442", value)
	require.Equal(t, core.LanguageRU, language)

	value, language, ok = normalizeCanonicalImportValue("  CAR   moves ")
	require.True(t, ok)
	require.Equal(t, "car moves", value)
	require.Equal(t, core.LanguageEN, language)
}

func TestNormalizeCanonicalImportValueUsesUnicodeCodePointLimitAndWhitespace(t *testing.T) {
	value, language, ok := normalizeCanonicalImportValue("car\u00a0moves")
	require.True(t, ok)
	require.Equal(t, "car moves", value)
	require.Equal(t, core.LanguageEN, language)

	value, language, ok = normalizeCanonicalImportValue(strings.Repeat("\u044f", 120))
	require.True(t, ok)
	require.Len(t, []rune(value), 120)
	require.Equal(t, core.LanguageRU, language)

	_, _, ok = normalizeCanonicalImportValue(strings.Repeat("\u044f", 121))
	require.False(t, ok)
}

func TestCanonicalServiceBulkImportPreservesCompoundCanonicalAndSingleWordForms(t *testing.T) {
	store := &fakeCanonicalStore{}
	service := NewCanonicalService(store)

	result, err := service.BulkImport(context.Background(), []core.CanonicalImportValue{{
		Value: "car\u00a0moves", Forms: []string{"AUTO", "vehicle", "two words"},
	}}, core.ClassPositive)

	require.NoError(t, err)
	require.Equal(t, core.CanonicalBulkImportResult{Skipped: 1}, result)
	require.Equal(t, []core.CanonicalImportValue{{
		Value: "car moves", Language: core.LanguageEN, Forms: []string{"auto", "vehicle"},
	}}, store.bulkImportValues)
}

func TestCanonicalServiceBulkImportRejectsInvalidPhrases(t *testing.T) {
	store := &fakeCanonicalStore{}
	service := NewCanonicalService(store)

	result, err := service.BulkImport(context.Background(), []core.CanonicalImportValue{
		{Value: "car moves"},
		{Value: "one two three four five six seven eight nine"},
		{Value: strings.Repeat("a", 121)},
	}, core.ClassPositive)

	require.NoError(t, err)
	require.Equal(t, core.CanonicalBulkImportResult{Skipped: 2}, result)
	require.Equal(t, []core.CanonicalImportValue{{Value: "car moves", Language: core.LanguageEN, Forms: []string{}}}, store.bulkImportValues)
}

type fakeCanonicalStore struct {
	rows              []SourceRow
	synced            []core.CanonicalObservation
	syncCalls         int
	classified        core.KeywordClass
	triggerActive     bool
	cleared           core.KeywordClass
	searchQuery       string
	searchClass       core.KeywordClass
	searchResults     []core.CanonicalKeyword
	deletedCanonicals []string
	deletedTrigger    string
	bulkImportValues  []core.CanonicalImportValue
	bulkImportClass   core.KeywordClass
	bulkImportResult  core.CanonicalBulkImportResult
}

func (s *fakeCanonicalStore) Rows(context.Context, string, []string) ([]SourceRow, error) {
	return append([]SourceRow(nil), s.rows...), nil
}
func (s *fakeCanonicalStore) SyncCanonical(_ context.Context, values []core.CanonicalObservation) error {
	s.syncCalls++
	s.synced = append([]core.CanonicalObservation(nil), values...)
	return nil
}
func (s *fakeCanonicalStore) ListCanonical(context.Context, core.KeywordClass) ([]core.CanonicalKeyword, error) {
	return nil, nil
}
func (s *fakeCanonicalStore) ClassifyCanonical(_ context.Context, _ string, class core.KeywordClass) error {
	s.classified = class
	return nil
}
func (s *fakeCanonicalStore) SetCanonicalTrigger(_ context.Context, _ string, active bool) error {
	s.triggerActive = active
	return nil
}
func (s *fakeCanonicalStore) RemoveCanonicalForm(context.Context, string, string) (core.CanonicalKeyword, error) {
	return core.CanonicalKeyword{}, nil
}
func (s *fakeCanonicalStore) MoveCanonicalForm(context.Context, string, string) error { return nil }
func (s *fakeCanonicalStore) AddCanonicalForm(context.Context, string, string) error  { return nil }
func (s *fakeCanonicalStore) DeleteCanonical(_ context.Context, id string) error {
	s.deletedCanonicals = append(s.deletedCanonicals, id)
	return nil
}
func (s *fakeCanonicalStore) ClearCanonicalClass(_ context.Context, class core.KeywordClass) error {
	s.cleared = class
	return nil
}
func (s *fakeCanonicalStore) SearchCanonical(_ context.Context, query string, class core.KeywordClass) ([]core.CanonicalKeyword, error) {
	s.searchQuery, s.searchClass = query, class
	return append([]core.CanonicalKeyword(nil), s.searchResults...), nil
}
func (s *fakeCanonicalStore) CanonicalMetrics(context.Context) (core.CanonicalMetrics, error) {
	return core.CanonicalMetrics{}, nil
}
func (s *fakeCanonicalStore) DeleteCanonicalTrigger(_ context.Context, id string) error {
	s.deletedTrigger = id
	return nil
}
func (s *fakeCanonicalStore) BulkImportCanonical(_ context.Context, values []core.CanonicalImportValue, class core.KeywordClass) (core.CanonicalBulkImportResult, error) {
	s.bulkImportValues = append([]core.CanonicalImportValue(nil), values...)
	s.bulkImportClass = class
	return s.bulkImportResult, nil
}

type fakeServiceWordProvider struct {
	words     map[domain.AnalyticsLanguage][]string
	languages []domain.AnalyticsLanguage
}

func (p *fakeServiceWordProvider) ServiceWords(_ context.Context, language domain.AnalyticsLanguage) ([]string, error) {
	p.languages = append(p.languages, language)
	return append([]string(nil), p.words[language]...), nil
}
