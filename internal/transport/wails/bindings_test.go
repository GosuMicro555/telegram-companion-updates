package wails

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	coreanalytics "telegram-companion/internal/analytics"
	"telegram-companion/internal/domain"
	"telegram-companion/internal/usecase"
	analyticsusecase "telegram-companion/internal/usecase/analytics"
	importsusecase "telegram-companion/internal/usecase/imports"
	"telegram-companion/internal/usecase/runtimeconfig"

	"github.com/stretchr/testify/require"
)

type analyticsServiceStub struct {
	started          chan struct{}
	result           analyticsusecase.AnalysisResult
	analyze          func(context.Context, analyticsusecase.AnalysisRequest) (analyticsusecase.AnalysisResult, error)
	request          analyticsusecase.AnalysisRequest
	moderate         [3]string
	refs             []analyticsusecase.CandidateRef
	onAdd            func()
	states           map[analyticsusecase.CandidateKey]string
	compare          analyticsusecase.TopicComparisonRequest
	compareStarted   chan struct{}
	compareRelease   chan struct{}
	compareCancelled chan struct{}
}

func (s *analyticsServiceStub) Analyze(ctx context.Context, request analyticsusecase.AnalysisRequest) (analyticsusecase.AnalysisResult, error) {
	s.request = request
	if s.analyze != nil {
		return s.analyze(ctx, request)
	}
	if s.started != nil {
		close(s.started)
		<-ctx.Done()
		return analyticsusecase.AnalysisResult{}, ctx.Err()
	}
	return s.result, nil
}

type historySyncerStub struct {
	called chan struct{}
	err    error
}

type canonicalServiceStub struct {
	rows             []coreanalytics.CanonicalKeyword
	classified       coreanalytics.KeywordClass
	triggered        bool
	bulkImportValues []coreanalytics.CanonicalImportValue
	bulkImportClass  coreanalytics.KeywordClass
	bulkImportResult coreanalytics.CanonicalBulkImportResult
}

type analyticsCollectionSchedulerStub struct {
	started int
	stopped int
	ran     int
	status  usecase.AnalyticsCollectionStatus
	history []domain.AnalyticsSchedulerRun
}

func (s *analyticsCollectionSchedulerStub) Start(context.Context, int) error {
	s.started++
	return nil
}
func (s *analyticsCollectionSchedulerStub) Stop(context.Context) error {
	s.stopped++
	return nil
}
func (s *analyticsCollectionSchedulerStub) RunNow(context.Context) (domain.AnalyticsSchedulerRun, error) {
	s.ran++
	return domain.AnalyticsSchedulerRun{ID: "run-now", Metrics: domain.AnalyticsRunMetrics{NewMessages: 4, ExtractedWords: 7}}, nil
}
func (s *analyticsCollectionSchedulerStub) Status(context.Context) (usecase.AnalyticsCollectionStatus, error) {
	return s.status, nil
}
func (s *analyticsCollectionSchedulerStub) History(context.Context, int) ([]domain.AnalyticsSchedulerRun, error) {
	return append([]domain.AnalyticsSchedulerRun(nil), s.history...), nil
}

type analyticsBindingStoreFake struct {
	settings    domain.AnalyticsSettings
	service     map[domain.AnalyticsLanguage][]string
	preferences map[string]domain.AnalyticsTablePreference
}

func (s *analyticsBindingStoreFake) AnalyticsSettings(context.Context) (domain.AnalyticsSettings, error) {
	return s.settings, nil
}
func (s *analyticsBindingStoreFake) SaveAnalyticsSettings(_ context.Context, settings domain.AnalyticsSettings) error {
	s.settings = settings
	return nil
}
func (*analyticsBindingStoreFake) ScoutCursor(context.Context, domain.ID, string) (domain.AnalyticsScoutCursor, error) {
	return domain.AnalyticsScoutCursor{}, nil
}
func (*analyticsBindingStoreFake) SaveScoutCursor(context.Context, domain.AnalyticsScoutCursor) error {
	return nil
}
func (*analyticsBindingStoreFake) AppendAnalyticsRun(context.Context, domain.AnalyticsSchedulerRun) error {
	return nil
}
func (*analyticsBindingStoreFake) AnalyticsRunHistory(context.Context, int) ([]domain.AnalyticsSchedulerRun, error) {
	return nil, nil
}
func (*analyticsBindingStoreFake) AnalyticsMetrics(context.Context) (domain.AnalyticsMetrics, error) {
	return domain.AnalyticsMetrics{}, nil
}
func (s *analyticsBindingStoreFake) ServiceWords(_ context.Context, language domain.AnalyticsLanguage) ([]string, error) {
	return append([]string(nil), s.service[language]...), nil
}
func (s *analyticsBindingStoreFake) UpsertServiceWord(_ context.Context, word domain.AnalyticsServiceWord) error {
	s.service[word.Language] = append(s.service[word.Language], word.Value)
	return nil
}
func (s *analyticsBindingStoreFake) DeleteServiceWord(_ context.Context, language domain.AnalyticsLanguage, value string) error {
	words := s.service[language]
	for index, word := range words {
		if word == value {
			s.service[language] = append(words[:index], words[index+1:]...)
			break
		}
	}
	return nil
}
func (s *analyticsBindingStoreFake) TablePreference(_ context.Context, tab string) (domain.AnalyticsTablePreference, error) {
	return s.preferences[tab], nil
}
func (s *analyticsBindingStoreFake) SaveTablePreference(_ context.Context, preference domain.AnalyticsTablePreference) error {
	s.preferences[preference.Tab] = preference
	return nil
}

func TestAnalyticsCollectionBindingsMatchFrontendAliasesAndValidateIntervals(t *testing.T) {
	scheduler := &analyticsCollectionSchedulerStub{status: usecase.AnalyticsCollectionStatus{
		Enabled: true, IntervalMinutes: 10, AllTimeMessages: 9, AllTimeKeywords: 3,
		LatestCollection: domain.AnalyticsRunMetrics{NewMessages: 4, ExtractedWords: 7, NewCanonicals: 2, ProcessedGroups: 3, Duration: 1200 * time.Millisecond, Errors: []string{"timeout"}},
		Metrics:          domain.AnalyticsMetrics{MessageCount: 9, CanonicalCount: 3, FormCount: 5, GroupCount: 2, LogicalKeywordBytes: 2048},
	}}
	store := &analyticsBindingStoreFake{settings: domain.DefaultAnalyticsSettings(), service: make(map[domain.AnalyticsLanguage][]string), preferences: make(map[string]domain.AnalyticsTablePreference)}
	bindings := NewBindings(usecase.NewAutomationController(nil))
	bindings.ConfigureAnalyticsCollection(scheduler, store)

	require.NoError(t, bindings.StartCanonicalAnalytics(10))
	require.NoError(t, bindings.StopCanonicalAnalytics())
	_, err := bindings.AnalyzeCanonicalKeywords()
	require.NoError(t, err)
	status, err := bindings.GetCanonicalAnalyticsStatus()
	require.NoError(t, err)
	require.True(t, status.Running)
	require.Equal(t, int64(9), status.AllTimeMessages)
	require.Equal(t, int64(4), status.LatestCollection.NewMessages)
	require.Equal(t, int64(2), status.LatestCollection.NewCanonicalWords)
	require.Equal(t, int64(5), status.Totals.Forms)
	require.Equal(t, int64(2048), status.Totals.KeywordDBBytes)
	require.Equal(t, 1, scheduler.started)
	require.Equal(t, 1, scheduler.stopped)
	require.Equal(t, 1, scheduler.ran)
	require.Error(t, bindings.StartAnalyticsCollection(2))
}

func TestAnalyticsTablePreferenceBindingsValidateFrontendPayload(t *testing.T) {
	store := &analyticsBindingStoreFake{settings: domain.DefaultAnalyticsSettings(), service: make(map[domain.AnalyticsLanguage][]string), preferences: make(map[string]domain.AnalyticsTablePreference)}
	bindings := NewBindings(usecase.NewAutomationController(nil))
	bindings.ConfigureAnalyticsCollection(&analyticsCollectionSchedulerStub{}, store)
	want := AnalyticsTablePreferenceDTO{
		Tab: "positive", SortBy: "trend", SortDirection: "ascending", PageSize: 75,
		Columns: []AnalyticsTableColumnPreferenceDTO{
			{Key: "canonical", Width: 260, Visible: true}, {Key: "class", Width: 92, Visible: false},
			{Key: "language", Width: 72, Visible: false}, {Key: "frequency", Width: 86, Visible: true},
			{Key: "trend", Width: 86, Visible: true}, {Key: "messages", Width: 94, Visible: true},
			{Key: "lastSeen", Width: 132, Visible: false}, {Key: "forms", Width: 76, Visible: true},
			{Key: "actions", Width: 144, Visible: true},
		},
	}

	require.NoError(t, bindings.SaveAnalyticsTablePreferences(want))
	got, err := bindings.GetAnalyticsTablePreferences("positive")
	require.NoError(t, err)
	require.Equal(t, want, got)

	invalid := want
	invalid.Columns = append([]AnalyticsTableColumnPreferenceDTO(nil), want.Columns...)
	invalid.Columns[0].Key = "unknown"
	require.ErrorContains(t, bindings.SaveAnalyticsTablePreferences(invalid), "column")
	invalid = want
	invalid.Tab = "not-a-tab"
	require.Error(t, bindings.SaveAnalyticsTablePreferences(invalid))
	invalid = want
	invalid.Columns = append([]AnalyticsTableColumnPreferenceDTO(nil), want.Columns...)
	invalid.Columns[0].Width = 901
	require.Error(t, bindings.SaveAnalyticsTablePreferences(invalid))
	invalid = want
	invalid.SortDirection = "sideways"
	require.Error(t, bindings.SaveAnalyticsTablePreferences(invalid))
}

func TestAnalyticsServiceWordBindingsValidateLanguageAndSingleWord(t *testing.T) {
	store := &analyticsBindingStoreFake{settings: domain.DefaultAnalyticsSettings(), service: make(map[domain.AnalyticsLanguage][]string), preferences: make(map[string]domain.AnalyticsTablePreference)}
	bindings := NewBindings(usecase.NewAutomationController(nil))
	bindings.ConfigureAnalyticsCollection(&analyticsCollectionSchedulerStub{}, store)

	require.NoError(t, bindings.AddAnalyticsServiceWord("ru", "слово"))
	require.Error(t, bindings.AddAnalyticsServiceWord("de", "wort"))
	require.Error(t, bindings.AddAnalyticsServiceWord("en", "two words"))
	words, err := bindings.GetAnalyticsServiceWords("ru")
	require.NoError(t, err)
	require.Equal(t, []string{"слово"}, words)
}

func (s *canonicalServiceStub) Analyze(context.Context) (analyticsusecase.CanonicalAnalysisResult, error) {
	return analyticsusecase.CanonicalAnalysisResult{MessageCount: 2, KeywordCount: len(s.rows)}, nil
}
func (s *canonicalServiceStub) List(_ context.Context, class coreanalytics.KeywordClass) ([]coreanalytics.CanonicalKeyword, error) {
	result := make([]coreanalytics.CanonicalKeyword, 0)
	for _, row := range s.rows {
		if row.Class == class {
			result = append(result, row)
		}
	}
	return result, nil
}
func (s *canonicalServiceStub) Classify(_ context.Context, id string, class coreanalytics.KeywordClass) error {
	s.classified = class
	for index := range s.rows {
		if s.rows[index].ID == id {
			s.rows[index].Class = class
			if class != coreanalytics.ClassPositive {
				s.rows[index].TriggerActive = false
			}
		}
	}
	return nil
}
func (s *canonicalServiceStub) SetTriggerActive(_ context.Context, id string, active bool) error {
	s.triggered = active
	for index := range s.rows {
		if s.rows[index].ID == id {
			s.rows[index].TriggerActive = active
		}
	}
	return nil
}
func (*canonicalServiceStub) RemoveForm(context.Context, string, string) (coreanalytics.CanonicalKeyword, error) {
	return coreanalytics.CanonicalKeyword{}, nil
}
func (*canonicalServiceStub) MoveForm(context.Context, string, string) error { return nil }
func (*canonicalServiceStub) AddForm(context.Context, string, string) error  { return nil }
func (s *canonicalServiceStub) BulkImport(_ context.Context, values []coreanalytics.CanonicalImportValue, class coreanalytics.KeywordClass) (coreanalytics.CanonicalBulkImportResult, error) {
	s.bulkImportValues = append([]coreanalytics.CanonicalImportValue(nil), values...)
	s.bulkImportClass = class
	return s.bulkImportResult, nil
}
func (s *canonicalServiceStub) Delete(_ context.Context, id string) error {
	kept := s.rows[:0]
	for _, row := range s.rows {
		if row.ID != id {
			kept = append(kept, row)
		}
	}
	s.rows = kept
	return nil
}
func (s *canonicalServiceStub) Clear(_ context.Context, class coreanalytics.KeywordClass) error {
	kept := s.rows[:0]
	for _, row := range s.rows {
		if row.Class != class {
			kept = append(kept, row)
		}
	}
	s.rows = kept
	return nil
}

func (s *historySyncerStub) SyncHistory(context.Context) error {
	close(s.called)
	return s.err
}

func TestCanonicalTriggersPersistCanonsAndPublishAllForms(t *testing.T) {
	store := &settingsStoreStub{settings: domain.KeywordSettings{
		Keywords: []string{"manual"}, SharedReply: "live reply", PrivateReply: "private live reply", DirectMessageKeywords: []string{"money"},
	}}
	canonical := &canonicalServiceStub{rows: []coreanalytics.CanonicalKeyword{{
		ID: "money", Canonical: "money", Language: coreanalytics.LanguageEN,
		Class: coreanalytics.ClassPositive, TriggerActive: true, TotalFrequency: 7, FrequencyDelta: 3,
		Forms: []coreanalytics.CanonicalKeywordForm{{Value: "monies"}},
	}}}
	bindings := NewBindings(usecase.NewAutomationController(nil), store)
	bindings.ConfigureCanonicalAnalytics(canonical)

	rows, err := bindings.ListCanonicalKeywords(string(coreanalytics.ClassPositive))
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, int64(3), rows[0].FrequencyDelta)

	require.NoError(t, bindings.SyncCanonicalTriggers())
	require.Equal(t, []string{"manual", "money"}, store.settings.Keywords)
	require.Equal(t, []string{"money"}, store.settings.CanonicalTriggerIDs)
	require.Equal(t, []string{"money"}, store.settings.CanonicalTriggerValues)
	require.ElementsMatch(t, []string{"manual", "money", "monies"}, bindings.RuntimeStore().Current().Keywords)
	require.Equal(t, []string{"money"}, store.settings.DirectMessageKeywords)
	require.Equal(t, []string{"money"}, store.settings.DirectMessageCanonicalTriggerIDs)
	require.Equal(t, []string{"money"}, store.settings.DirectMessageCanonicalValues)
	require.ElementsMatch(t, []string{"money", "monies"}, bindings.RuntimeStore().Current().DirectMessageKeywords)
	require.Equal(t, "live reply", bindings.RuntimeStore().Current().SharedReply)
	require.Equal(t, "private live reply", bindings.RuntimeStore().Current().PrivateReply)
	_, err = bindings.SaveKeywordSettings(KeywordSettingsDTO{
		Keywords: []string{"manual", "money"}, SharedReply: "new live reply", PrivateReply: "new private reply", DirectMessageKeywords: []string{"money"},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"manual", "money"}, store.settings.Keywords)
	require.ElementsMatch(t, []string{"manual", "money", "monies"}, bindings.RuntimeStore().Current().Keywords)
	require.Equal(t, "new live reply", bindings.RuntimeStore().Current().SharedReply)
	require.Equal(t, "new private reply", bindings.RuntimeStore().Current().PrivateReply)
	require.True(t, bindings.RuntimeStore().Current().PrivateReplyPresent)

	require.NoError(t, bindings.ClassifyCanonicalKeyword("money", string(coreanalytics.ClassNegative)))
	require.Equal(t, coreanalytics.ClassNegative, canonical.classified)
	require.Equal(t, []string{"manual"}, store.settings.Keywords)
	require.Empty(t, store.settings.CanonicalTriggerIDs)
	require.Empty(t, store.settings.CanonicalTriggerValues)
	require.Equal(t, []string{"manual"}, bindings.RuntimeStore().Current().Keywords)
	require.Empty(t, store.settings.DirectMessageKeywords)
	require.Empty(t, store.settings.DirectMessageCanonicalTriggerIDs)
	require.Empty(t, store.settings.DirectMessageCanonicalValues)
	require.Empty(t, bindings.RuntimeStore().Current().DirectMessageKeywords)
}

func TestBulkImportCanonicalKeywordsDoesNotSynchronizeResponseTriggers(t *testing.T) {
	store := &settingsStoreStub{settings: domain.KeywordSettings{Keywords: []string{"manual"}, SharedReply: "live reply"}}
	canonical := &canonicalServiceStub{bulkImportResult: coreanalytics.CanonicalBulkImportResult{Added: 2, Updated: 1}}
	bindings := NewBindings(usecase.NewAutomationController(nil), store)
	bindings.ConfigureCanonicalAnalytics(canonical)

	result, err := bindings.BulkImportCanonicalKeywords([]CanonicalImportEntryDTO{
		{Canonical: "money", Forms: []string{"monies"}},
		{Canonical: "скидка", Forms: []string{"скидочки"}},
	}, "positive")

	require.NoError(t, err)
	require.Equal(t, CanonicalBulkImportResultDTO{Added: 2, Updated: 1}, result)
	require.Equal(t, []coreanalytics.CanonicalImportValue{
		{Value: "money", Forms: []string{"monies"}},
		{Value: "скидка", Forms: []string{"скидочки"}},
	}, canonical.bulkImportValues)
	require.Equal(t, coreanalytics.ClassPositive, canonical.bulkImportClass)
	require.Equal(t, []string{"manual"}, store.settings.Keywords)

	_, err = bindings.BulkImportCanonicalKeywords([]CanonicalImportEntryDTO{{Canonical: "money"}}, "neutral")
	require.EqualError(t, err, "bulk keyword class must be positive or negative")
}

func TestCanonicalSyncPreservesManualKeywordOverlappingInactiveCanonical(t *testing.T) {
	store := &settingsStoreStub{settings: domain.KeywordSettings{Keywords: []string{"Money"}, SharedReply: "reply"}}
	canonical := &canonicalServiceStub{rows: []coreanalytics.CanonicalKeyword{{
		ID: "money", Canonical: "money", Language: coreanalytics.LanguageEN,
		Class: coreanalytics.ClassNeutral, Forms: []coreanalytics.CanonicalKeywordForm{{Value: "monies"}},
	}}}
	bindings := NewBindings(usecase.NewAutomationController(nil), store)
	bindings.ConfigureCanonicalAnalytics(canonical)

	require.NoError(t, bindings.SyncCanonicalTriggers())
	require.Equal(t, []string{"Money"}, store.settings.Keywords)
	require.Equal(t, []string{"Money"}, bindings.RuntimeStore().Current().Keywords)
}

func TestCanonicalSyncPublishesActiveCanonicalTriggerDescriptors(t *testing.T) {
	store := &settingsStoreStub{settings: domain.KeywordSettings{SharedReply: "reply"}}
	canonical := &canonicalServiceStub{rows: []coreanalytics.CanonicalKeyword{
		{ID: "money", Canonical: "Деньги", Class: coreanalytics.ClassPositive, TriggerActive: true, Forms: []coreanalytics.CanonicalKeywordForm{{Value: "денег"}}},
		{ID: "off", Canonical: "off", Class: coreanalytics.ClassPositive, TriggerActive: false},
		{ID: "negative", Canonical: "negative", Class: coreanalytics.ClassNegative, TriggerActive: true},
	}}
	bindings := NewBindings(usecase.NewAutomationController(nil), store)
	bindings.ConfigureCanonicalAnalytics(canonical)

	require.NoError(t, bindings.SyncCanonicalTriggers())
	require.Equal(t, []runtimeconfig.CanonicalTrigger{{ID: "money", Canonical: "Деньги", Forms: []string{"деньги", "денег"}}}, bindings.RuntimeStore().Current().CanonicalTriggers)
}

func TestDeletingInactiveCanonicalPreservesOverlappingManualKeyword(t *testing.T) {
	store := &settingsStoreStub{settings: domain.KeywordSettings{Keywords: []string{"Money"}, SharedReply: "reply"}}
	canonical := &canonicalServiceStub{rows: []coreanalytics.CanonicalKeyword{{
		ID: "money", Canonical: "money", Language: coreanalytics.LanguageEN, Class: coreanalytics.ClassNeutral,
	}}}
	bindings := NewBindings(usecase.NewAutomationController(nil), store)
	bindings.ConfigureCanonicalAnalytics(canonical)

	require.NoError(t, bindings.DeleteCanonicalKeyword("money"))
	require.Equal(t, []string{"Money"}, store.settings.Keywords)
	require.Equal(t, []string{"Money"}, bindings.RuntimeStore().Current().Keywords)
}

func TestDeleteLiveTriggerUsesCanonicalDeletionAndSynchronizesTriggers(t *testing.T) {
	store := &settingsStoreStub{settings: domain.KeywordSettings{
		Keywords: []string{"manual", "money"}, SharedReply: "reply",
		CanonicalTriggerIDs: []string{"money"}, CanonicalTriggerValues: []string{"money"},
	}}
	canonical := &canonicalServiceStub{rows: []coreanalytics.CanonicalKeyword{{
		ID: "money", Canonical: "money", Class: coreanalytics.ClassPositive, TriggerActive: true,
	}}}
	bindings := NewBindings(usecase.NewAutomationController(nil), store)
	bindings.ConfigureCanonicalAnalytics(canonical)

	require.NoError(t, bindings.DeleteLiveTrigger("money"))
	require.Empty(t, canonical.rows)
	require.Equal(t, []string{"manual"}, store.settings.Keywords)
	require.Empty(t, store.settings.CanonicalTriggerIDs)
	require.Empty(t, store.settings.CanonicalTriggerValues)
	require.Equal(t, []string{"manual"}, bindings.RuntimeStore().Current().Keywords)
}

func TestDeleteLiveTriggerRemovesInactiveCanonicalValueFromKeywords(t *testing.T) {
	store := &settingsStoreStub{settings: domain.KeywordSettings{
		Keywords: []string{"manual", "money"}, SharedReply: "reply",
	}}
	canonical := &canonicalServiceStub{rows: []coreanalytics.CanonicalKeyword{{
		ID: "money", Canonical: "money", Class: coreanalytics.ClassPositive, TriggerActive: false,
	}}}
	bindings := NewBindings(usecase.NewAutomationController(nil), store)
	bindings.ConfigureCanonicalAnalytics(canonical)

	require.NoError(t, bindings.DeleteLiveTrigger("money"))
	require.Empty(t, canonical.rows)
	require.Equal(t, []string{"manual"}, store.settings.Keywords)
	require.Equal(t, []string{"manual"}, bindings.RuntimeStore().Current().Keywords)
}

func TestCanonicalSyncRemovesPersistedTriggerWhoseCanonicalWasDeletedByAnalysis(t *testing.T) {
	store := &settingsStoreStub{settings: domain.KeywordSettings{
		Keywords: []string{"manual", "money"}, SharedReply: "reply",
		CanonicalTriggerIDs: []string{"deleted-money"}, CanonicalTriggerValues: []string{"money"},
	}}
	bindings := NewBindings(usecase.NewAutomationController(nil), store)
	bindings.ConfigureCanonicalAnalytics(&canonicalServiceStub{})

	require.NoError(t, bindings.SyncCanonicalTriggers())
	require.Equal(t, []string{"manual"}, store.settings.Keywords)
	require.Empty(t, store.settings.CanonicalTriggerIDs)
	require.Empty(t, store.settings.CanonicalTriggerValues)
	require.Equal(t, []string{"manual"}, bindings.RuntimeStore().Current().Keywords)
}

func TestSaveKeywordSettingsDisablesCanonicalTriggerRemovedInKeywordsUI(t *testing.T) {
	store := &settingsStoreStub{settings: domain.KeywordSettings{Keywords: []string{"manual", "money"}, SharedReply: "reply"}}
	canonical := &canonicalServiceStub{rows: []coreanalytics.CanonicalKeyword{{
		ID: "money", Canonical: "money", Language: coreanalytics.LanguageEN,
		Class: coreanalytics.ClassPositive, TriggerActive: true,
		Forms: []coreanalytics.CanonicalKeywordForm{{Value: "money"}, {Value: "monies"}},
	}}}
	bindings := NewBindings(usecase.NewAutomationController(nil), store)
	bindings.ConfigureCanonicalAnalytics(canonical)

	applied, err := bindings.SaveKeywordSettings(KeywordSettingsDTO{
		Keywords: []string{"manual"}, SharedReply: "updated reply",
	})

	require.NoError(t, err)
	require.False(t, canonical.rows[0].TriggerActive)
	require.Equal(t, coreanalytics.ClassPositive, canonical.rows[0].Class)
	require.Equal(t, []string{"manual"}, store.settings.Keywords)
	require.Equal(t, []string{"manual"}, applied.Keywords)
	require.Equal(t, []string{"manual"}, bindings.RuntimeStore().Current().Keywords)
	require.Equal(t, "updated reply", bindings.RuntimeStore().Current().SharedReply)
}

func (s *analyticsServiceStub) Moderate(_ context.Context, value, kind, state string) error {
	s.moderate = [3]string{value, kind, state}
	return nil
}

func (s *analyticsServiceStub) AddCandidatesToKeywords(_ context.Context, refs []analyticsusecase.CandidateRef) (int, error) {
	s.refs = append([]analyticsusecase.CandidateRef(nil), refs...)
	if s.onAdd != nil {
		s.onAdd()
	}
	return len(refs), nil
}

func (s *analyticsServiceStub) CompareTopics(ctx context.Context, request analyticsusecase.TopicComparisonRequest) ([]analyticsusecase.TopicSummary, error) {
	s.compare = request
	if s.compareStarted != nil {
		close(s.compareStarted)
		<-ctx.Done()
		close(s.compareCancelled)
		<-s.compareRelease
	}
	return []analyticsusecase.TopicSummary{{Topic: "A", Count: 3, DistinctCandidateCount: 2, RelativeShare: .75, TopPhrases: []coreanalytics.Candidate{{NormalizedValue: "нет денег", DisplayValue: "Нет денег", Kind: "phrase", Frequency: 2}}}}, nil
}

func (s *analyticsServiceStub) ModerationStates(context.Context) (map[analyticsusecase.CandidateKey]string, error) {
	return s.states, nil
}

type operationContextKey struct{}

type importServiceStub struct {
	request      importsusecase.ImportRequest
	contextValue any
	imports      []domain.Import
	candidates   []domain.KeywordCandidate
	exportRun    domain.ID
	exportPath   string
}

func (s *importServiceStub) List(context.Context) ([]domain.Import, error) {
	return append([]domain.Import(nil), s.imports...), nil
}

func (s *importServiceStub) ListCandidates(context.Context, domain.ID) ([]domain.KeywordCandidate, error) {
	return append([]domain.KeywordCandidate(nil), s.candidates...), nil
}

func (s *importServiceStub) ExportCandidates(_ context.Context, runID domain.ID, path string) (string, error) {
	s.exportRun = runID
	s.exportPath = path
	return path, nil
}

type importFileSelectorStub struct{ path, exportPath, locale string }

func (s *importFileSelectorStub) SelectImportFile(_ context.Context, locale string) (string, error) {
	s.locale = locale
	return s.path, nil
}
func (s *importFileSelectorStub) SelectExportFile(_ context.Context, _ string, locale string) (string, error) {
	s.locale = locale
	return s.exportPath, nil
}

func (s *importServiceStub) Import(ctx context.Context, request importsusecase.ImportRequest) (domain.Import, error) {
	s.request = request
	s.contextValue = ctx.Value(operationContextKey{})
	return domain.Import{ID: "import-1", FileName: "keywords.csv", FilePath: request.Path, Status: domain.ImportStatusComplete, RecordCount: 12}, nil
}

type blockingImportService struct{ started chan struct{} }

func (s *blockingImportService) Import(ctx context.Context, _ importsusecase.ImportRequest) (domain.Import, error) {
	close(s.started)
	<-ctx.Done()
	return domain.Import{}, ctx.Err()
}

type fileOpenerStub struct{ path string }

func (s *fileOpenerStub) Open(path string) error { s.path = path; return nil }

type backupOperationsStub struct {
	created  domain.BackupKind
	restored [2]string
	exported string
	contexts []any
}

type backupRunnerStub struct {
	started chan struct{}
	stopped chan struct{}
}

func (r *backupRunnerStub) Run(ctx context.Context) error {
	close(r.started)
	<-ctx.Done()
	close(r.stopped)
	return ctx.Err()
}

func (s *backupOperationsStub) Create(ctx context.Context, kind domain.BackupKind) (domain.BackupRecord, error) {
	s.contexts = append(s.contexts, ctx.Value(operationContextKey{}))
	s.created = kind
	verified := time.Date(2026, 7, 11, 3, 0, 0, 0, time.UTC)
	return domain.BackupRecord{
		ID: "backup-1", ArchivePath: "/backups/daily.scout-backup", Kind: kind,
		SizeBytes: 123, SHA256: "abc", Status: domain.BackupVerified,
		CreatedAt: verified, VerifiedAt: &verified,
	}, nil
}

func (s *backupOperationsStub) Restore(ctx context.Context, archive, destination string) error {
	s.contexts = append(s.contexts, ctx.Value(operationContextKey{}))
	s.restored = [2]string{archive, destination}
	return nil
}

func (s *backupOperationsStub) ExportRecoveryKey(ctx context.Context, path string) error {
	s.contexts = append(s.contexts, ctx.Value(operationContextKey{}))
	s.exported = path
	return nil
}

func (*backupOperationsStub) HasVerifiedMonthly(context.Context, int, time.Month, *time.Location) (bool, error) {
	return false, nil
}

type settingsStoreStub struct {
	mu              sync.RWMutex
	settings        domain.KeywordSettings
	channels        []domain.ManagedChannel
	accounts        []domain.Account
	catalogs        map[domain.SourceCatalog][]domain.Channel
	memberships     map[wailsMembershipKey]domain.ChannelMembership
	appSettings     domain.AppSettings
	saveErr         error
	accountSaveErr  error
	catalogSaveErr  error
	topicsSaveErr   error
	appSaveErr      error
	loadErr         error
	appLoadErr      error
	channelsErr     error
	canonicalizeApp func(domain.AppSettings) domain.AppSettings
}

type groupRestCleanerStoreStub struct {
	*settingsStoreStub
	clearCalls        int
	clearErr          error
	onClearGroupRests func() error
	joinIntervalCalls int
	joinIntervalErr   error
	onClearIntervals  func() error
}

func (s *groupRestCleanerStoreStub) ClearGroupRests(context.Context) error {
	s.clearCalls++
	if s.onClearGroupRests != nil {
		return s.onClearGroupRests()
	}
	return s.clearErr
}

func (s *groupRestCleanerStoreStub) ClearJoinIntervals(context.Context) error {
	s.joinIntervalCalls++
	if s.onClearIntervals != nil {
		return s.onClearIntervals()
	}
	return s.joinIntervalErr
}

type wailsMembershipKey struct {
	catalog   domain.SourceCatalog
	channelID domain.ID
	accountID domain.ID
}

func TestBindingsGetChannelModerationMapsSafeDTOs(t *testing.T) {
	requestedAt := time.Date(2026, 7, 18, 8, 0, 0, 0, time.UTC)
	joinedAt := requestedAt.Add(45 * time.Minute)
	scoutRequestedAt := requestedAt.Add(-time.Hour)
	store := &settingsStoreStub{
		settings:    domain.DefaultKeywordSettings(),
		appSettings: domain.DefaultAppSettings(),
		accounts: []domain.Account{
			{ID: "123456789", DisplayName: "Outbound reviewer", Role: domain.AccountRoleSpammer},
			{ID: "987654321", PhoneMasked: "+1 *** 1000", Role: domain.AccountRoleScoutAnalyst},
		},
		catalogs: map[domain.SourceCatalog][]domain.Channel{
			domain.SourceCatalogOutbound: {{ID: "outbound-channel", Title: "Outbound channel", Link: "https://t.me/outbound", Topic: "Sales"}},
			domain.SourceCatalogScout:    {{ID: "scout-channel", Title: "Scout channel", Link: "https://t.me/scout", Topic: "Research"}},
		},
		memberships: map[wailsMembershipKey]domain.ChannelMembership{
			{catalog: domain.SourceCatalogOutbound, channelID: "outbound-channel", accountID: "123456789"}: {
				ChannelID: "outbound-channel", AccountID: "123456789", RequestSubmittedAt: &requestedAt, JoinedAt: &joinedAt, IsMember: true, Status: "member",
			},
			{catalog: domain.SourceCatalogScout, channelID: "scout-channel", accountID: "987654321"}: {
				ChannelID: "scout-channel", AccountID: "987654321", RequestSubmittedAt: &scoutRequestedAt, Status: "pending_approval",
			},
		},
	}

	b := NewBindings(usecase.NewAutomationController(nil), store)
	result, err := b.GetChannelModeration()
	require.NoError(t, err)
	require.Len(t, result, 2)
	require.Equal(t, "member", result[0].Status)
	require.Equal(t, "2026-07-18T08:00:00Z", result[0].FirstRequestAt)
	require.Equal(t, int64(2700), result[0].DurationSeconds)
	require.Equal(t, "Outbound reviewer", result[0].Accounts[0].Title)
	require.Equal(t, "+1 *** 1000", result[1].Accounts[0].Title)
	require.NotContains(t, result[0].Accounts[0].Title, "123456789")

	marshaled, err := json.Marshal(result)
	require.NoError(t, err)
	require.NotContains(t, string(marshaled), "telegramId")
	require.NotContains(t, string(marshaled), "123456789")
	require.NotContains(t, string(marshaled), "987654321")

	mapped := channelModerationDTOs([]usecase.ChannelModerationChannel{{
		Status: "partial", FirstRequestAt: requestedAt, Duration: 45 * time.Minute,
	}})
	require.Equal(t, "partial", mapped[0].Status)
	require.Equal(t, "2026-07-18T08:00:00Z", mapped[0].FirstRequestAt)
	require.Equal(t, int64(2700), mapped[0].DurationSeconds)

	empty := NewBindings(usecase.NewAutomationController(nil), &settingsStoreStub{
		settings: domain.DefaultKeywordSettings(), appSettings: domain.DefaultAppSettings(),
	})
	emptyResult, err := empty.GetChannelModeration()
	require.NoError(t, err)
	require.NotNil(t, emptyResult)
	require.Empty(t, emptyResult)
}

func (s *settingsStoreStub) ListAccounts(context.Context) ([]domain.Account, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]domain.Account(nil), s.accounts...), nil
}

func (s *settingsStoreStub) SaveAccount(_ context.Context, account domain.Account) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.accountSaveErr != nil {
		return s.accountSaveErr
	}
	for index := range s.accounts {
		if s.accounts[index].ID == account.ID {
			s.accounts[index] = account
			return nil
		}
	}
	return usecase.ErrAccountNotFound
}

func (s *settingsStoreStub) ResumeLegacyPaused(_ context.Context, id domain.ID, _ time.Time) (domain.Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for index := range s.accounts {
		if s.accounts[index].ID == id && s.accounts[index].LegacyPauseReviewRequired {
			s.accounts[index].Status = domain.AccountStopped
			s.accounts[index].LegacyPauseReviewRequired = false
			return s.accounts[index], nil
		}
	}
	return domain.Account{}, usecase.ErrLegacyPauseNotReviewable
}

func (s *settingsStoreStub) ListCatalog(_ context.Context, catalog domain.SourceCatalog) ([]domain.Channel, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]domain.Channel(nil), s.catalogs[catalog]...), nil
}

func (s *settingsStoreStub) SaveCatalog(_ context.Context, catalog domain.SourceCatalog, channel domain.Channel) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.catalogSaveErr != nil {
		return s.catalogSaveErr
	}
	rows := s.catalogs[catalog]
	for index := range rows {
		if rows[index].ID == channel.ID {
			rows[index] = channel
			s.catalogs[catalog] = rows
			return nil
		}
	}
	s.catalogs[catalog] = append(rows, channel)
	return nil
}

func (s *settingsStoreStub) ActivateCatalogWithMemberships(_ context.Context, catalog domain.SourceCatalog, channel domain.Channel, memberships []domain.ChannelMembership) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows := s.catalogs[catalog]
	for index := range rows {
		if rows[index].ID == channel.ID {
			rows[index] = channel
			s.catalogs[catalog] = rows
			break
		}
	}
	if s.memberships == nil {
		s.memberships = make(map[wailsMembershipKey]domain.ChannelMembership)
	}
	for _, membership := range memberships {
		s.memberships[wailsMembershipKey{catalog: catalog, channelID: membership.ChannelID, accountID: membership.AccountID}] = membership
	}
	return nil
}

func (s *settingsStoreStub) SetCatalogTopics(_ context.Context, catalog domain.SourceCatalog, ids []domain.ID, topic string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.topicsSaveErr != nil {
		return s.topicsSaveErr
	}
	rows := append([]domain.Channel(nil), s.catalogs[catalog]...)
	positions := make(map[domain.ID]int, len(rows))
	for index := range rows {
		positions[rows[index].ID] = index
	}
	for _, id := range ids {
		if _, exists := positions[id]; !exists {
			return usecase.ErrChannelNotFound
		}
	}
	for _, id := range ids {
		rows[positions[id]].Topic = topic
	}
	s.catalogs[catalog] = rows
	return nil
}

func (s *settingsStoreStub) RequestCatalogRemoval(_ context.Context, catalog domain.SourceCatalog, ids []domain.ID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	requested := make(map[domain.ID]struct{}, len(ids))
	for _, id := range ids {
		requested[id] = struct{}{}
	}
	for index := range s.catalogs[catalog] {
		if _, ok := requested[s.catalogs[catalog][index].ID]; !ok {
			continue
		}
		s.catalogs[catalog][index].Active = false
		s.catalogs[catalog][index].RemovalRequestedAt = &now
	}
	return nil
}

func (s *settingsStoreStub) RequestCatalogLeave(_ context.Context, catalog domain.SourceCatalog, channelID domain.ID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	found := false
	for index := range s.catalogs[catalog] {
		if s.catalogs[catalog][index].ID != channelID {
			continue
		}
		s.catalogs[catalog][index].Active = false
		s.catalogs[catalog][index].Status = domain.ChannelPaused
		found = true
		break
	}
	if !found {
		return usecase.ErrChannelNotFound
	}
	for key, membership := range s.memberships {
		if key.catalog != catalog || key.channelID != channelID {
			continue
		}
		membership.Status = "leaving"
		s.memberships[key] = membership
	}
	return nil
}

func (s *settingsStoreStub) RetryPendingCatalogMemberships(_ context.Context, catalog domain.SourceCatalog, channelID domain.ID, _ time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	updated := 0
	for key, membership := range s.memberships {
		if key.catalog != catalog || key.channelID != channelID || membership.IsMember || membership.Status != "pending_approval" {
			continue
		}
		membership.Status = "joining"
		membership.LastCheckAt = nil
		membership.RequestSubmittedAt = nil
		membership.JoinNotBefore = nil
		membership.LastError = ""
		s.memberships[key] = membership
		updated++
	}
	return updated, nil
}

func (s *settingsStoreStub) ListMemberships(_ context.Context, catalog domain.SourceCatalog, channelID domain.ID) ([]domain.ChannelMembership, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	memberships := make([]domain.ChannelMembership, 0)
	for key, membership := range s.memberships {
		if key.catalog == catalog && key.channelID == channelID {
			memberships = append(memberships, membership)
		}
	}
	return memberships, nil
}

func (s *settingsStoreStub) SaveMembership(_ context.Context, catalog domain.SourceCatalog, membership domain.ChannelMembership) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.memberships == nil {
		s.memberships = make(map[wailsMembershipKey]domain.ChannelMembership)
	}
	s.memberships[wailsMembershipKey{catalog: catalog, channelID: membership.ChannelID, accountID: membership.AccountID}] = membership
	return nil
}

func (s *settingsStoreStub) Load(context.Context) (domain.KeywordSettings, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.loadErr != nil {
		return domain.KeywordSettings{}, s.loadErr
	}
	return s.settings, nil
}

func (s *settingsStoreStub) Save(_ context.Context, settings domain.KeywordSettings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.saveErr != nil {
		return s.saveErr
	}
	s.settings = settings
	return nil
}

func (s *settingsStoreStub) LoadChannels(context.Context) ([]domain.ManagedChannel, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.channelsErr != nil {
		return nil, s.channelsErr
	}
	return append([]domain.ManagedChannel(nil), s.channels...), nil
}

func (s *settingsStoreStub) SaveChannels(_ context.Context, channels []domain.ManagedChannel) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.channels = append([]domain.ManagedChannel(nil), channels...)
	return nil
}

func (s *settingsStoreStub) LoadAppSettings(context.Context) (domain.AppSettings, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.appLoadErr != nil {
		return domain.AppSettings{}, s.appLoadErr
	}
	return s.appSettings, nil
}

func (s *settingsStoreStub) SaveAppSettings(_ context.Context, settings domain.AppSettings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.appSaveErr != nil {
		return s.appSaveErr
	}
	if s.canonicalizeApp != nil {
		settings = s.canonicalizeApp(settings)
	}
	s.appSettings = settings
	return nil
}

func TestBindingsStartStop(t *testing.T) {
	store := &settingsStoreStub{settings: domain.DefaultKeywordSettings()}
	b := NewBindings(usecase.NewAutomationController(usecase.AutomationRunnerFunc(func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})), store)

	if err := b.StartAutomation(); err != nil {
		t.Fatalf("StartAutomation returned error: %v", err)
	}
	dashboard, err := b.GetDashboard()
	if err != nil {
		t.Fatalf("GetDashboard returned error: %v", err)
	}
	if !dashboard.Running {
		t.Fatal("dashboard running=false, want true")
	}
	if err := b.StopAutomation(); err != nil {
		t.Fatalf("StopAutomation returned error: %v", err)
	}
}

func TestBindingsExposeBackupStatusRestoreAndExplicitRecoveryExport(t *testing.T) {
	store := &settingsStoreStub{settings: domain.DefaultKeywordSettings()}
	operations := &backupOperationsStub{}
	backups := usecase.NewBackupService(operations)
	b := NewBindingsWithBackups(usecase.NewAutomationController(nil), store, backups)

	created, err := b.CreateBackup("daily")
	if err != nil {
		t.Fatalf("CreateBackup returned error: %v", err)
	}
	if created.Status != domain.BackupVerified || created.ArchivePath == "" {
		t.Fatalf("created backup = %+v", created)
	}
	if operations.exported != "" {
		t.Fatal("recovery key was exported without an explicit command")
	}
	if got := b.GetBackupStatus(); got.ID != created.ID {
		t.Fatalf("backup status = %+v, want %+v", got, created)
	}
	if err := b.RestoreBackup("/archive", "/safe-restore"); err != nil {
		t.Fatalf("RestoreBackup returned error: %v", err)
	}
	if operations.restored != [2]string{"/archive", "/safe-restore"} {
		t.Fatalf("restore arguments = %v", operations.restored)
	}
	if err := b.ExportBackupRecoveryKey("/selected/recovery.key"); err != nil {
		t.Fatalf("ExportBackupRecoveryKey returned error: %v", err)
	}
	if operations.exported != "/selected/recovery.key" {
		t.Fatalf("export path = %q", operations.exported)
	}
}

func TestManualBackupAndImportCommandsUseRootContext(t *testing.T) {
	store := &settingsStoreStub{settings: domain.DefaultKeywordSettings()}
	operations := &backupOperationsStub{}
	importer := &importServiceStub{}
	b := NewBindingsWithBackups(usecase.NewAutomationController(nil), store, usecase.NewBackupService(operations))
	b.ConfigureProduction(nil, nil, importer, nil)
	b.SetRootContext(context.WithValue(context.Background(), operationContextKey{}, "root"))

	_, err := b.CreateBackup("daily")
	require.NoError(t, err)
	require.NoError(t, b.RestoreBackup("/archive", "/restore"))
	require.NoError(t, b.ExportBackupRecoveryKey("/recovery"))
	_, err = b.ImportAIFile(AIImportRequestDTO{Path: "/tmp/import.csv", SHA256: "abc", RightsConfirmed: true})
	require.NoError(t, err)
	require.Equal(t, []any{"root", "root", "root"}, operations.contexts)
	require.Equal(t, "root", importer.contextValue)
}

func TestStopOperationsCancelsAndWaitsForActiveImport(t *testing.T) {
	importer := &blockingImportService{started: make(chan struct{})}
	b := NewBindingsWithAnalytics(usecase.NewAutomationController(nil), &settingsStoreStub{}, nil, importer, nil)
	b.SetRootContext(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := b.ImportAIFile(AIImportRequestDTO{Path: "/tmp/import.csv", SHA256: "abc", RightsConfirmed: true})
		done <- err
	}()
	<-importer.started
	shutdown, shutdownCancel := context.WithTimeout(context.Background(), time.Second)
	defer shutdownCancel()
	require.NoError(t, b.StopOperations(shutdown))
	require.ErrorIs(t, <-done, context.Canceled)
}

func TestStopOperationsRejectsEveryNewLicensedOperationClass(t *testing.T) {
	b := &Bindings{}
	require.NoError(t, b.StopOperations(context.Background()))

	operations := map[string]func() error{
		"automation":             b.StartAutomation,
		"analysis":               func() error { _, err := b.StartAnalysis(AnalysisRequestDTO{}); return err },
		"topic comparison":       func() error { _, err := b.StartTopicComparison(TopicComparisonRequestDTO{}); return err },
		"candidate moderation":   func() error { return b.ModerateAnalysisCandidate("word", "keyword", "accepted") },
		"candidate add":          func() error { _, err := b.AddAnalysisCandidates(nil); return err },
		"analytics schedule":     func() error { return b.StartAnalyticsCollection(5) },
		"analytics run":          func() error { _, err := b.RunAnalyticsCollectionNow(); return err },
		"analytics interval":     func() error { _, err := b.SetAnalyticsCollectionInterval(5); return err },
		"service word add":       func() error { return b.AddAnalyticsServiceWord("ru", "word") },
		"service word delete":    func() error { return b.DeleteAnalyticsServiceWord("ru", "word") },
		"service words clear":    b.ClearServiceCanonicalKeywords,
		"table preferences":      func() error { return b.SaveAnalyticsTablePreferences(AnalyticsTablePreferenceDTO{}) },
		"keyword classify":       func() error { return b.ClassifyCanonicalKeyword("id", "class") },
		"keyword trigger":        func() error { return b.SetCanonicalKeywordTrigger("id", true) },
		"keyword delete":         func() error { return b.DeleteCanonicalKeyword("id") },
		"live trigger delete":    func() error { return b.DeleteLiveTrigger("id") },
		"keywords clear":         func() error { return b.ClearCanonicalKeywords("class") },
		"canonical form remove":  func() error { _, err := b.RemoveCanonicalForm("id", "form"); return err },
		"canonical form move":    func() error { return b.MoveCanonicalForm("id", "form") },
		"canonical form add":     func() error { return b.AddCanonicalForm("id", "form") },
		"canonical bulk import":  func() error { _, err := b.BulkImportCanonicalKeywords(nil, "class"); return err },
		"canonical trigger sync": b.SyncCanonicalTriggers,
		"file open":              func() error { return b.OpenFile("/private/result") },
	}
	for name, operation := range operations {
		t.Run(name, func(t *testing.T) {
			require.ErrorIs(t, operation(), errDesktopOperationsClosed)
		})
	}
}

func TestNewBindingsUsesExplicitProductionBackupRuntime(t *testing.T) {
	operations := &backupOperationsStub{}
	runner := &backupRunnerStub{started: make(chan struct{}), stopped: make(chan struct{})}
	store := &settingsStoreStub{settings: domain.DefaultKeywordSettings()}
	automation := usecase.NewAutomationController(usecase.AutomationRunnerFunc(func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}))
	b := NewBindings(automation, store)
	b.ConfigureBackups(BackupRuntime{Service: usecase.NewBackupService(operations), Runner: runner})

	if err := b.StartAutomation(); err != nil {
		t.Fatalf("StartAutomation returned error: %v", err)
	}
	select {
	case <-runner.started:
	case <-time.After(time.Second):
		t.Fatal("production backup runner did not start")
	}
	if _, err := b.CreateBackup("daily"); err != nil {
		t.Fatalf("production backup service is not wired: %v", err)
	}
	if err := b.StopAutomation(); err != nil {
		t.Fatalf("StopAutomation returned error: %v", err)
	}
	select {
	case <-runner.stopped:
		t.Fatal("STOP automation must not stop backup background services")
	case <-time.After(100 * time.Millisecond):
	}
	if err := b.CloseBackgroundServices(context.Background()); err != nil {
		t.Fatalf("CloseBackgroundServices returned error: %v", err)
	}
	select {
	case <-runner.stopped:
	case <-time.After(time.Second):
		t.Fatal("explicit background-services close did not stop backup runner")
	}
}

func TestNewBindingsWithoutExplicitBackupHasNoBackgroundRuntime(t *testing.T) {
	b := NewBindings(usecase.NewAutomationController(nil))
	require.NoError(t, b.StartBackgroundServices())
	require.NoError(t, b.CloseBackgroundServices(context.Background()))
}

func TestBindingsPersistKeywordSettings(t *testing.T) {
	store := &settingsStoreStub{settings: domain.DefaultKeywordSettings()}
	b := NewBindings(usecase.NewAutomationController(nil), store)

	want := KeywordSettingsDTO{
		Keywords:    []string{"слил", "тест1"},
		SharedReply: "ответ",
	}
	if _, err := b.SaveKeywordSettings(want); err != nil {
		t.Fatalf("SaveKeywordSettings returned error: %v", err)
	}
	got, err := b.GetKeywordSettings()
	if err != nil {
		t.Fatalf("GetKeywordSettings returned error: %v", err)
	}
	if got.SharedReply != want.SharedReply || len(got.Keywords) != 2 {
		t.Fatalf("settings = %+v, want %+v", got, want)
	}
}

func TestBindingsPersistMinusKeywords(t *testing.T) {
	store := &settingsStoreStub{settings: domain.DefaultKeywordSettings()}
	b := NewBindings(usecase.NewAutomationController(nil), store)

	want := []string{"ремонт", "\"не звонить\""}
	applied, err := b.SaveKeywordSettings(KeywordSettingsDTO{
		Keywords:      []string{"деньги"},
		MinusKeywords: want,
		SharedReply:   "reply",
	})
	require.NoError(t, err)
	require.Equal(t, want, applied.MinusKeywords)
	require.Equal(t, want, store.settings.MinusKeywords)
	require.Equal(t, want, b.RuntimeStore().Current().MinusKeywords)

	loaded, err := b.GetKeywordSettings()
	require.NoError(t, err)
	require.Equal(t, want, loaded.MinusKeywords)
}

func TestBindingsPersistDirectMessageKeywords(t *testing.T) {
	store := &settingsStoreStub{settings: domain.DefaultKeywordSettings()}
	b := NewBindings(usecase.NewAutomationController(nil), store)

	var want KeywordSettingsDTO
	if err := json.Unmarshal([]byte(`{"keywords":["слил","тест1"],"sharedReply":"ответ","directMessageKeywords":["тест1"]}`), &want); err != nil {
		t.Fatalf("Unmarshal DTO returned error: %v", err)
	}
	if _, err := b.SaveKeywordSettings(want); err != nil {
		t.Fatalf("SaveKeywordSettings returned error: %v", err)
	}
	got, err := b.GetKeywordSettings()
	if err != nil {
		t.Fatalf("GetKeywordSettings returned error: %v", err)
	}
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("Marshal DTO returned error: %v", err)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("Unmarshal DTO JSON returned error: %v", err)
	}
	if got := string(payload["directMessageKeywords"]); got != `["тест1"]` {
		t.Fatalf("directMessageKeywords = %s, want [\"тест1\"]", got)
	}
}

func TestBindingsRoundTripPrivateReplyThroughDTOAndRuntime(t *testing.T) {
	store := &settingsStoreStub{settings: domain.DefaultKeywordSettings(), appSettings: domain.DefaultAppSettings()}
	b := NewBindings(usecase.NewAutomationController(nil), store)

	applied, err := b.SaveKeywordSettings(KeywordSettingsDTO{
		SharedReply:  "comment reply",
		PrivateReply: "private reply",
	})
	require.NoError(t, err)
	require.Equal(t, "private reply", applied.PrivateReply)
	require.Equal(t, "private reply", store.settings.PrivateReply)
	require.Equal(t, "private reply", b.RuntimeStore().Current().PrivateReply)
	require.True(t, b.RuntimeStore().Current().PrivateReplyPresent)

	loaded, err := b.GetKeywordSettings()
	require.NoError(t, err)
	require.Equal(t, "comment reply", loaded.SharedReply)
	require.Equal(t, "private reply", loaded.PrivateReply)
}

func TestBindingsExpandLegacyDirectMessageKeywordsForTransport(t *testing.T) {
	store := &settingsStoreStub{settings: domain.KeywordSettings{Keywords: []string{"legacy"}}}
	b := NewBindings(usecase.NewAutomationController(nil), store)

	got, err := b.GetKeywordSettings()
	require.NoError(t, err)
	require.Equal(t, []string{"legacy"}, got.DirectMessageKeywords)
}

func TestBindingsAddChannelLinks(t *testing.T) {
	store := &settingsStoreStub{settings: domain.DefaultKeywordSettings(), appSettings: domain.DefaultAppSettings()}
	b := NewBindings(usecase.NewAutomationController(nil), store)

	got, err := b.AddChannelLinks("https://t.me/design_friends\nhttps://t.me/+unitTestInviteHash https://t.me/design_friends")
	if err != nil {
		t.Fatalf("AddChannelLinks returned error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("channels = %+v, want two unique links", got)
	}
	if got[0].Title != "@design_friends" || !got[0].Active {
		t.Fatalf("first channel = %+v", got[0])
	}
	if got[1].Status != string(domain.ChannelStatusJoining) {
		t.Fatalf("private invite status = %q", got[1].Status)
	}
}

func TestBindingsToggleChannelActivePersistsPausedStatus(t *testing.T) {
	store := &settingsStoreStub{
		settings: domain.DefaultKeywordSettings(),
		channels: []domain.ManagedChannel{{
			ID:     "one",
			Title:  "@one",
			Link:   "https://t.me/one",
			Status: domain.ChannelStatusReady,
			Active: true,
		}},
		appSettings: domain.DefaultAppSettings(),
	}
	b := NewBindings(usecase.NewAutomationController(nil), store)

	got, err := b.ToggleChannelActive("one", false)
	if err != nil {
		t.Fatalf("ToggleChannelActive returned error: %v", err)
	}
	if len(got) != 1 || got[0].Active || got[0].Status != string(domain.ChannelStatusPaused) {
		t.Fatalf("channels = %+v, want paused inactive channel", got)
	}
}

func TestBindingsGetCatalogAggregatesMembershipStatusAndSummary(t *testing.T) {
	catalog := domain.SourceCatalogScout
	store := &settingsStoreStub{
		settings: domain.DefaultKeywordSettings(),
		catalogs: map[domain.SourceCatalog][]domain.Channel{
			catalog: {
				{ID: "mixed", Link: "https://t.me/mixed", Status: domain.ChannelReady, Active: true},
				{ID: "pending", Link: "https://t.me/pending", Status: domain.ChannelReady, Active: true},
				{ID: "joining", Link: "https://t.me/joining", Status: domain.ChannelReady, Active: true},
				{ID: "leaving", Link: "https://t.me/leaving", Status: domain.ChannelReady, Active: true},
				{ID: "failed", Link: "https://t.me/failed", Status: domain.ChannelReady, Active: true},
				{ID: "paused", Link: "https://t.me/paused", Status: domain.ChannelReady, Active: false},
			},
		},
		memberships: map[wailsMembershipKey]domain.ChannelMembership{
			{catalog: catalog, channelID: "mixed", accountID: "member"}:  {AccountID: "member", ChannelID: "mixed", IsMember: true, Status: "member"},
			{catalog: catalog, channelID: "mixed", accountID: "pending"}: {AccountID: "pending", ChannelID: "mixed", Status: "pending_approval"},
			{catalog: catalog, channelID: "mixed", accountID: "joining"}: {AccountID: "joining", ChannelID: "mixed", Status: "joining"},
			{catalog: catalog, channelID: "mixed", accountID: "error"}:   {AccountID: "error", ChannelID: "mixed", Status: "error", LastError: "CHANNEL_PRIVATE"},
			{catalog: catalog, channelID: "pending", accountID: "one"}:   {AccountID: "one", ChannelID: "pending", Status: "pending_approval"},
			{catalog: catalog, channelID: "pending", accountID: "two"}:   {AccountID: "two", ChannelID: "pending", Status: "pending_approval"},
			{catalog: catalog, channelID: "joining", accountID: "one"}:   {AccountID: "one", ChannelID: "joining", Status: "joining"},
			{catalog: catalog, channelID: "leaving", accountID: "one"}:   {AccountID: "one", ChannelID: "leaving", Status: "leaving"},
			{catalog: catalog, channelID: "failed", accountID: "one"}:    {AccountID: "one", ChannelID: "failed", Status: "error", LastError: "CHANNEL_PRIVATE"},
			{catalog: catalog, channelID: "paused", accountID: "one"}:    {AccountID: "one", ChannelID: "paused", IsMember: true, Status: "member"},
		},
		appSettings: domain.DefaultAppSettings(),
	}
	b := NewBindings(usecase.NewAutomationController(nil), store)

	got, err := b.GetCatalog(string(catalog))
	require.NoError(t, err)

	payload, err := json.Marshal(got)
	require.NoError(t, err)
	var rows []map[string]any
	require.NoError(t, json.Unmarshal(payload, &rows))
	require.Len(t, rows, 6)
	require.Equal(t, []any{"partial", "moderation", "joining", "leaving", "error", "paused"}, []any{
		rows[0]["status"], rows[1]["status"], rows[2]["status"], rows[3]["status"], rows[4]["status"], rows[5]["status"],
	})
	require.Equal(t, float64(1), rows[0]["member"])
	require.Equal(t, float64(1), rows[0]["pendingApproval"])
	require.Equal(t, float64(1), rows[0]["joining"])
	require.Equal(t, float64(1), rows[0]["failed"])
	require.Equal(t, float64(2), rows[1]["pendingApproval"])
	require.Equal(t, float64(1), rows[3]["leaving"])
}

func TestBindingsDeleteCatalogEntriesPublishesRemovingRowsWithoutAssignments(t *testing.T) {
	catalog := domain.SourceCatalogOutbound
	store := &settingsStoreStub{
		settings:    domain.DefaultKeywordSettings(),
		appSettings: domain.DefaultAppSettings(),
		catalogs: map[domain.SourceCatalog][]domain.Channel{
			catalog: {{ID: "remove", Link: "https://t.me/remove", Active: true}},
		},
	}
	bindings := NewBindings(usecase.NewAutomationController(nil), store)

	rows, err := bindings.DeleteCatalogEntries(string(catalog), []string{"remove"})

	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "removing", rows[0].Status)
	require.False(t, rows[0].Active)
	require.Empty(t, bindings.RuntimeStore().Current().CatalogAssignments[catalog])
}

func TestBindingsCatalogToggleJoinAndLeaveAreIndependent(t *testing.T) {
	catalog := domain.SourceCatalogOutbound
	store := &settingsStoreStub{
		settings:    domain.DefaultKeywordSettings(),
		appSettings: domain.DefaultAppSettings(),
		accounts:    []domain.Account{{ID: "account", Status: domain.AccountActive}},
		catalogs: map[domain.SourceCatalog][]domain.Channel{
			catalog: {{ID: "channel", Link: "https://t.me/channel", Status: domain.ChannelPaused}},
		},
	}
	bindings := NewBindings(usecase.NewAutomationController(nil), store)

	rows, err := bindings.ToggleCatalogEntry(string(catalog), "channel", true)
	require.NoError(t, err)
	require.True(t, rows[0].Active)
	memberships, err := store.ListMemberships(context.Background(), catalog, "channel")
	require.NoError(t, err)
	require.Empty(t, memberships, "processing toggle must not create membership intents")

	rows, err = bindings.JoinCatalogEntry(string(catalog), "channel")
	require.NoError(t, err)
	require.True(t, rows[0].Active, "joining must preserve the processing toggle")
	memberships, err = store.ListMemberships(context.Background(), catalog, "channel")
	require.NoError(t, err)
	require.Len(t, memberships, 1)
	require.Equal(t, "joining", memberships[0].Status)

	rows, err = bindings.LeaveCatalogEntry(string(catalog), "channel")
	require.NoError(t, err)
	require.False(t, rows[0].Active, "leaving disables processing for safety")
	require.Equal(t, 1, rows[0].Leaving)
	memberships, err = store.ListMemberships(context.Background(), catalog, "channel")
	require.NoError(t, err)
	require.Equal(t, "leaving", memberships[0].Status)
}

func TestBindingsRetryCatalogJoinRequeuesPendingApprovalMemberships(t *testing.T) {
	catalog := domain.SourceCatalogOutbound
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	key := wailsMembershipKey{catalog: catalog, channelID: "channel", accountID: "account"}
	store := &settingsStoreStub{
		settings:    domain.DefaultKeywordSettings(),
		appSettings: domain.DefaultAppSettings(),
		accounts:    []domain.Account{{ID: "account", Status: domain.AccountActive}},
		catalogs: map[domain.SourceCatalog][]domain.Channel{
			catalog: {{ID: "channel", Link: "https://t.me/channel", Status: domain.ChannelJoining}},
		},
		memberships: map[wailsMembershipKey]domain.ChannelMembership{
			key: {AccountID: "account", ChannelID: "channel", Status: "pending_approval", RequestSubmittedAt: &now},
		},
	}
	bindings := NewBindings(usecase.NewAutomationController(nil), store)

	rows, err := bindings.RetryCatalogJoin(string(catalog), "channel")

	require.NoError(t, err)
	require.Len(t, rows, 1)
	memberships, err := store.ListMemberships(context.Background(), catalog, "channel")
	require.NoError(t, err)
	require.Len(t, memberships, 1)
	require.Equal(t, "joining", memberships[0].Status)
	require.Nil(t, memberships[0].RequestSubmittedAt)
}

func TestBindingsPersistAppSettings(t *testing.T) {
	store := &settingsStoreStub{settings: domain.DefaultKeywordSettings(), appSettings: domain.DefaultAppSettings()}
	b := NewBindings(usecase.NewAutomationController(nil), store)

	want := AppSettingsDTO{RepliesPerMinute: 5, MinIntervalSeconds: 3, DirectMessages: false, Proxy: "socks5://127.0.0.1:19050"}
	if _, err := b.SaveAppSettings(want); err != nil {
		t.Fatalf("SaveAppSettings returned error: %v", err)
	}
	got, err := b.GetAppSettings()
	if err != nil {
		t.Fatalf("GetAppSettings returned error: %v", err)
	}
	if got.RepliesPerMinute != want.RepliesPerMinute || got.MinIntervalSeconds != want.MinIntervalSeconds || got.DirectMessages != want.DirectMessages || got.Proxy != want.Proxy {
		t.Fatalf("settings = %+v, want %+v", got, want)
	}
}

func TestBindingsExposeJoinIntervalDefaultsAndReloadSavedRange(t *testing.T) {
	catalog := domain.SourceCatalogOutbound
	store := &settingsStoreStub{
		settings:    domain.DefaultKeywordSettings(),
		appSettings: domain.AppSettings{},
		accounts: []domain.Account{
			{ID: "first", Status: domain.AccountActive},
			{ID: "second", Status: domain.AccountActive},
		},
		catalogs: map[domain.SourceCatalog][]domain.Channel{
			catalog: {{ID: "planned", Link: "https://t.me/planned", Status: domain.ChannelPaused}},
		},
	}

	bindings := NewBindings(usecase.NewAutomationController(nil), store)
	defaults, err := bindings.GetAppSettings()
	require.NoError(t, err)
	require.Equal(t, 10, defaults.JoinIntervalMinMinutes)
	require.Equal(t, 60, defaults.JoinIntervalMaxMinutes)
	require.Equal(t, 36, defaults.GroupRestHours)

	_, err = bindings.SaveAppSettings(AppSettingsDTO{
		RepliesPerMinute: 7, MinIntervalSeconds: 3, JoinIntervalMinMinutes: 20, JoinIntervalMaxMinutes: 20, GroupRestHours: 72,
		JoinIntervalEnabled: true, GroupRestEnabled: true,
	})
	require.NoError(t, err)
	require.Equal(t, 20, store.appSettings.JoinIntervalMinMinutes)
	require.Equal(t, 20, store.appSettings.JoinIntervalMaxMinutes)
	require.Equal(t, 72, store.appSettings.GroupRestHours)

	reloaded := NewBindings(usecase.NewAutomationController(nil), store)
	_, err = reloaded.JoinCatalogEntry(string(catalog), "planned")
	require.NoError(t, err)
	memberships, err := store.ListMemberships(context.Background(), catalog, "planned")
	require.NoError(t, err)
	require.Len(t, memberships, 2)
	byAccountID := make(map[domain.ID]domain.ChannelMembership, len(memberships))
	for _, membership := range memberships {
		byAccountID[membership.AccountID] = membership
	}
	require.Equal(t, 20*time.Minute, byAccountID["second"].JoinNotBefore.Sub(*byAccountID["first"].JoinNotBefore))
	require.Equal(t, 72, byAccountID["first"].RestDurationHours)
	require.Equal(t, 72, byAccountID["second"].RestDurationHours)
}

func TestBindingsClearPersistedGroupRestsWhenDisabledAtStartup(t *testing.T) {
	store := &groupRestCleanerStoreStub{settingsStoreStub: &settingsStoreStub{
		settings: domain.DefaultKeywordSettings(),
		appSettings: domain.AppSettings{
			RepliesPerMinute: 19, MinIntervalSeconds: 2,
			JoinIntervalEnabled: true, JoinIntervalMinMinutes: 10, JoinIntervalMaxMinutes: 60,
			GroupRestHours: 36, GroupRestEnabled: false,
		},
	}}

	_, err := NewBindingsWithError(usecase.NewAutomationController(nil), store)

	require.NoError(t, err)
	require.Equal(t, 1, store.clearCalls)
}

func TestBindingsClearPersistedGroupRestsWhenToggleIsDisabled(t *testing.T) {
	store := &groupRestCleanerStoreStub{settingsStoreStub: &settingsStoreStub{
		settings:    domain.DefaultKeywordSettings(),
		appSettings: domain.DefaultAppSettings(),
	}}
	bindings := NewBindings(usecase.NewAutomationController(nil), store)
	require.Zero(t, store.clearCalls)

	applied, err := bindings.SaveAppSettings(AppSettingsDTO{
		RepliesPerMinute: 19, MinIntervalSeconds: 2,
		JoinIntervalEnabled: true, JoinIntervalMinMinutes: 0, JoinIntervalMaxMinutes: 3000,
		GroupRestHours: 36, GroupRestEnabled: false,
	})

	require.NoError(t, err)
	require.False(t, applied.GroupRestEnabled)
	require.True(t, applied.JoinIntervalEnabled)
	require.Equal(t, 0, applied.JoinIntervalMinMinutes)
	require.Equal(t, 3000, applied.JoinIntervalMaxMinutes)
	require.Equal(t, 1, store.clearCalls)
}

func TestBindingsClearExistingJoinIntervalsWhenToggleIsDisabled(t *testing.T) {
	store := &groupRestCleanerStoreStub{settingsStoreStub: &settingsStoreStub{
		settings:    domain.DefaultKeywordSettings(),
		appSettings: domain.DefaultAppSettings(),
	}}
	bindings := NewBindings(usecase.NewAutomationController(nil), store)
	require.Zero(t, store.joinIntervalCalls)

	applied, err := bindings.SaveAppSettings(AppSettingsDTO{
		RepliesPerMinute: 19, MinIntervalSeconds: 2,
		JoinIntervalEnabled: false, JoinIntervalMinMinutes: 10, JoinIntervalMaxMinutes: 60,
		GroupRestHours: 36, GroupRestEnabled: true,
	})

	require.NoError(t, err)
	require.False(t, applied.JoinIntervalEnabled)
	require.Equal(t, 1, store.joinIntervalCalls)
	require.Zero(t, store.clearCalls)
}

func TestBindingsDisableJoinIntervalRuntimeBeforeCleanup(t *testing.T) {
	catalog := domain.SourceCatalogOutbound
	store := &groupRestCleanerStoreStub{settingsStoreStub: &settingsStoreStub{
		settings:    domain.DefaultKeywordSettings(),
		appSettings: domain.DefaultAppSettings(),
		accounts: []domain.Account{
			{ID: "first", Status: domain.AccountActive},
			{ID: "second", Status: domain.AccountActive},
		},
		catalogs: map[domain.SourceCatalog][]domain.Channel{
			catalog: {{ID: "planned", Link: "https://t.me/planned", Status: domain.ChannelPaused}},
		},
	}}
	bindings := NewBindings(usecase.NewAutomationController(nil), store)
	store.onClearIntervals = func() error {
		_, err := bindings.JoinCatalogEntry(string(catalog), "planned")
		return err
	}

	_, err := bindings.SaveAppSettings(AppSettingsDTO{
		RepliesPerMinute: 19, MinIntervalSeconds: 2,
		JoinIntervalEnabled: false, JoinIntervalMinMinutes: 10, JoinIntervalMaxMinutes: 60,
		GroupRestHours: 36, GroupRestEnabled: true,
	})

	require.NoError(t, err)
	memberships, err := store.ListMemberships(context.Background(), catalog, "planned")
	require.NoError(t, err)
	require.Len(t, memberships, 2)
	require.NotNil(t, memberships[0].JoinNotBefore)
	require.NotNil(t, memberships[1].JoinNotBefore)
	require.Equal(t, *memberships[0].JoinNotBefore, *memberships[1].JoinNotBefore)
}

func TestBindingsDisableGroupRestRuntimeBeforeCleanup(t *testing.T) {
	catalog := domain.SourceCatalogOutbound
	store := &groupRestCleanerStoreStub{settingsStoreStub: &settingsStoreStub{
		settings:    domain.DefaultKeywordSettings(),
		appSettings: domain.DefaultAppSettings(),
		accounts:    []domain.Account{{ID: "first", Status: domain.AccountActive}},
		catalogs: map[domain.SourceCatalog][]domain.Channel{
			catalog: {{ID: "planned", Link: "https://t.me/planned", Status: domain.ChannelPaused}},
		},
	}}
	bindings := NewBindings(usecase.NewAutomationController(nil), store)
	store.onClearGroupRests = func() error {
		_, err := bindings.JoinCatalogEntry(string(catalog), "planned")
		return err
	}

	_, err := bindings.SaveAppSettings(AppSettingsDTO{
		RepliesPerMinute: 19, MinIntervalSeconds: 2,
		JoinIntervalEnabled: true, JoinIntervalMinMinutes: 0, JoinIntervalMaxMinutes: 0,
		GroupRestHours: 36, GroupRestEnabled: false,
	})

	require.NoError(t, err)
	memberships, err := store.ListMemberships(context.Background(), catalog, "planned")
	require.NoError(t, err)
	require.Len(t, memberships, 1)
	require.Zero(t, memberships[0].RestDurationHours)
}

func TestBindingsRejectInvalidGroupRestHoursBeforeSaving(t *testing.T) {
	store := &settingsStoreStub{settings: domain.DefaultKeywordSettings(), appSettings: domain.DefaultAppSettings()}
	bindings := NewBindings(usecase.NewAutomationController(nil), store)
	before := store.appSettings

	_, err := bindings.SaveAppSettings(AppSettingsDTO{
		RepliesPerMinute: 7, MinIntervalSeconds: 3, JoinIntervalMinMinutes: 20, JoinIntervalMaxMinutes: 30, GroupRestHours: 721,
	})

	require.ErrorIs(t, err, domain.ErrInvalidGroupRestHours)
	require.Equal(t, before, store.appSettings)
}

func TestBindingsRejectInvalidJoinIntervalBeforeSaving(t *testing.T) {
	store := &settingsStoreStub{settings: domain.DefaultKeywordSettings(), appSettings: domain.DefaultAppSettings()}
	bindings := NewBindings(usecase.NewAutomationController(nil), store)
	before := store.appSettings

	_, err := bindings.SaveAppSettings(AppSettingsDTO{
		RepliesPerMinute: 7, MinIntervalSeconds: 3, JoinIntervalMinMinutes: 30, JoinIntervalMaxMinutes: 20,
	})
	require.ErrorIs(t, err, domain.ErrInvalidJoinInterval)
	require.Equal(t, before, store.appSettings)
}

func TestBindingsExposeFutureJoiningMembershipAsPlanned(t *testing.T) {
	now := time.Now().UTC()
	future := now.Add(time.Hour)
	catalog := domain.SourceCatalogScout
	store := &settingsStoreStub{
		settings:    domain.DefaultKeywordSettings(),
		appSettings: domain.DefaultAppSettings(),
		catalogs: map[domain.SourceCatalog][]domain.Channel{
			catalog: {{ID: "planned", Link: "https://t.me/planned", Status: domain.ChannelJoining, Active: true}},
		},
		memberships: map[wailsMembershipKey]domain.ChannelMembership{
			{catalog: catalog, channelID: "planned", accountID: "private-account"}: {
				AccountID: "private-account", ChannelID: "planned", Status: "joining", JoinNotBefore: &future,
			},
		},
	}

	rows, err := NewBindings(usecase.NewAutomationController(nil), store).GetCatalog(string(catalog))
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.True(t, rows[0].Planned)
	require.Equal(t, future.Format(time.RFC3339Nano), rows[0].JoinNotBefore)

	payload, err := json.Marshal(rows[0])
	require.NoError(t, err)
	require.NotContains(t, string(payload), "private-account")
}

func TestBindingsPublishSettingsOnlyAfterSuccessfulSave(t *testing.T) {
	store := &settingsStoreStub{settings: domain.DefaultKeywordSettings(), appSettings: domain.DefaultAppSettings()}
	b := NewBindings(usecase.NewAutomationController(nil), store)

	before := b.runtime.Current()
	want := KeywordSettingsDTO{SharedReply: "  reply\nwith whitespace  "}
	applied, err := b.SaveKeywordSettings(want)
	if err != nil {
		t.Fatalf("SaveKeywordSettings returned error: %v", err)
	}
	if applied.Revision != before.Revision+1 {
		t.Fatalf("revision = %d, want %d", applied.Revision, before.Revision+1)
	}
	if got := b.runtime.Current(); got.SharedReply != want.SharedReply {
		t.Fatalf("published reply = %q, want %q", got.SharedReply, want.SharedReply)
	}

	store.saveErr = errors.New("sqlite transaction failed")
	previous := b.runtime.Current()
	if _, err := b.SaveKeywordSettings(KeywordSettingsDTO{SharedReply: "not published"}); err == nil {
		t.Fatal("SaveKeywordSettings returned nil error after failed save")
	}
	if got := b.runtime.Current(); got.Revision != previous.Revision || got.SharedReply != previous.SharedReply {
		t.Fatalf("failed save published %+v, want %+v", got, previous)
	}
}

func TestBindingsAllowEmptySharedReplyToPauseRepliesWithoutStoppingListeners(t *testing.T) {
	store := &settingsStoreStub{settings: domain.DefaultKeywordSettings(), appSettings: domain.DefaultAppSettings()}
	b := NewBindings(usecase.NewAutomationController(nil), store)

	applied, err := b.SaveKeywordSettings(KeywordSettingsDTO{Keywords: []string{"trigger"}, SharedReply: "  "})
	require.NoError(t, err)
	require.Equal(t, []string{"trigger"}, store.settings.Keywords)
	require.Equal(t, "  ", applied.SharedReply)
	require.Equal(t, "  ", b.runtime.Current().SharedReply)

	legacyStore := &settingsStoreStub{
		settings:    domain.KeywordSettings{Keywords: []string{"trigger"}},
		appSettings: domain.DefaultAppSettings(),
	}
	legacy := NewBindings(usecase.NewAutomationController(usecase.AutomationRunnerFunc(func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})), legacyStore)
	require.NoError(t, legacy.StartAutomation())
	require.NoError(t, legacy.StopAutomation())
}

func TestBindingsPublishAppSettingsOnlyAfterSuccessfulSave(t *testing.T) {
	store := &settingsStoreStub{settings: domain.DefaultKeywordSettings(), appSettings: domain.DefaultAppSettings()}
	b := NewBindings(usecase.NewAutomationController(nil), store)

	before := b.runtime.Current()
	want := AppSettingsDTO{RepliesPerMinute: 7, MinIntervalSeconds: 4, DirectMessages: false, Proxy: "socks5://127.0.0.1:19050"}
	applied, err := b.SaveAppSettings(want)
	if err != nil {
		t.Fatalf("SaveAppSettings returned error: %v", err)
	}
	if applied.Revision != before.Revision+1 {
		t.Fatalf("revision = %d, want %d", applied.Revision, before.Revision+1)
	}
	got := b.runtime.Current()
	if got.RateLimits.RepliesPerMinute != want.RepliesPerMinute || got.RateLimits.MinIntervalSeconds != want.MinIntervalSeconds || got.DirectMessages != want.DirectMessages || len(got.ProxyAssignments) != 0 {
		t.Fatalf("published app settings = %+v, want %+v", got, want)
	}

	store.appSaveErr = errors.New("sqlite transaction failed")
	previous := b.runtime.Current()
	if _, err := b.SaveAppSettings(AppSettingsDTO{RepliesPerMinute: 1}); err == nil {
		t.Fatal("SaveAppSettings returned nil error after failed save")
	}
	if got := b.runtime.Current(); !reflect.DeepEqual(got, previous) {
		t.Fatalf("failed save published %+v, want %+v", got, previous)
	}
}

func TestBindingsConcurrentIndependentSavesPreserveBothRuntimeFields(t *testing.T) {
	store := &settingsStoreStub{settings: domain.DefaultKeywordSettings(), appSettings: domain.DefaultAppSettings()}
	b := NewBindings(usecase.NewAutomationController(nil), store)
	start := make(chan struct{})
	errs := make(chan error, 3)
	var saves sync.WaitGroup

	saves.Add(3)
	go func() {
		defer saves.Done()
		<-start
		_, err := b.SaveKeywordSettings(KeywordSettingsDTO{Keywords: []string{"keyword"}, SharedReply: "reply", DirectMessageKeywords: []string{"dm"}})
		errs <- err
	}()
	go func() {
		defer saves.Done()
		<-start
		_, err := b.SaveAppSettings(AppSettingsDTO{RepliesPerMinute: 7, MinIntervalSeconds: 3, DirectMessages: false, Proxy: "socks5://127.0.0.1:19050"})
		errs <- err
	}()
	go func() {
		defer saves.Done()
		<-start
		_, err := b.AddChannelLinks("https://t.me/runtime_updates")
		errs <- err
	}()

	close(start)
	saves.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent save returned error: %v", err)
		}
	}

	got := b.runtime.Current()
	if len(got.Keywords) != 1 || got.Keywords[0] != "keyword" || got.SharedReply != "reply" || len(got.DirectMessageKeywords) != 1 || got.DirectMessageKeywords[0] != "dm" {
		t.Fatalf("keyword fields = %+v, want saved keyword settings", got)
	}
	if got.RateLimits != (runtimeconfig.RateLimits{RepliesPerMinute: 7, MinIntervalSeconds: 3}) || got.DirectMessages || len(got.ProxyAssignments) != 0 {
		t.Fatalf("app fields = %+v, want saved app settings", got)
	}
	if got := got.CatalogAssignments[domain.SourceCatalogOutbound]; len(got) != 1 {
		t.Fatalf("channel assignments = %+v, want one saved channel", got)
	}
}

func TestBindingsPublishesCanonicalCommittedAppSettings(t *testing.T) {
	store := &settingsStoreStub{
		settings:    domain.DefaultKeywordSettings(),
		appSettings: domain.DefaultAppSettings(),
		canonicalizeApp: func(settings domain.AppSettings) domain.AppSettings {
			settings.RepliesPerMinute = 19
			settings.MinIntervalSeconds = 2
			settings.Proxy = "socks5://canonical:9050"
			return settings
		},
	}
	b := NewBindings(usecase.NewAutomationController(nil), store)

	applied, err := b.SaveAppSettings(AppSettingsDTO{RepliesPerMinute: 7, MinIntervalSeconds: 3, DirectMessages: false, Proxy: "socks5://raw:19050"})
	if err != nil {
		t.Fatalf("SaveAppSettings returned error: %v", err)
	}
	if applied.RepliesPerMinute != 19 || applied.MinIntervalSeconds != 2 || applied.Proxy != "socks5://canonical:9050" {
		t.Fatalf("applied settings = %+v, want canonical committed settings", applied)
	}
	got := b.runtime.Current()
	if got.RateLimits != (runtimeconfig.RateLimits{RepliesPerMinute: 19, MinIntervalSeconds: 2}) || len(got.ProxyAssignments) != 0 {
		t.Fatalf("runtime settings = %+v, want canonical committed settings", got)
	}
}

func TestBindingsPublishesPerAccountProxyRouteIDs(t *testing.T) {
	profileID := domain.ID("route-custom")
	store := &settingsStoreStub{
		settings: domain.DefaultKeywordSettings(), appSettings: domain.DefaultAppSettings(),
		accounts: []domain.Account{
			{ID: "system", ProxyMode: domain.ProxyModeGlobal},
			{ID: "custom", ProxyMode: domain.ProxyModeAssigned, ProxyProfileID: &profileID},
			{ID: "overflow", ProxyMode: domain.ProxyModeUnassigned},
		},
	}
	b := NewBindings(usecase.NewAutomationController(nil), store)
	assignments := b.runtime.Current().ProxyAssignments
	require.Equal(t, "system", assignments["system"])
	require.Equal(t, "route-custom", assignments["custom"])
	require.Equal(t, "unassigned", assignments["overflow"])
	require.NotContains(t, assignments, domain.ID("global"))
}

func TestNewBindingsWithErrorRejectsPartialRuntimeSnapshot(t *testing.T) {
	tests := []struct {
		name  string
		store *settingsStoreStub
	}{
		{name: "keywords", store: &settingsStoreStub{loadErr: errors.New("keywords unavailable")}},
		{name: "app", store: &settingsStoreStub{appLoadErr: errors.New("app settings unavailable")}},
		{name: "channels", store: &settingsStoreStub{channelsErr: errors.New("channels unavailable")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, err := NewBindingsWithError(usecase.NewAutomationController(nil), tt.store)
			if err == nil {
				t.Fatal("NewBindingsWithError error = nil, want load error")
			}
			if b != nil {
				t.Fatalf("NewBindingsWithError bindings = %+v, want nil", b)
			}

			legacy := NewBindings(usecase.NewAutomationController(nil), tt.store)
			if legacy.initializationErr == nil {
				t.Fatal("legacy constructor initialization error = nil, want load error")
			}
			if _, err := legacy.SaveKeywordSettings(KeywordSettingsDTO{}); err == nil {
				t.Fatal("legacy SaveKeywordSettings error = nil after failed initialization")
			}
		})
	}
}

func TestCompatibilityBindingsSurfaceInitializationFailure(t *testing.T) {
	want := errors.New("app settings unavailable")
	store := &settingsStoreStub{
		settings:   domain.DefaultKeywordSettings(),
		appLoadErr: want,
	}
	runnerStarted := false
	b := NewBindings(
		usecase.NewAutomationController(usecase.AutomationRunnerFunc(func(context.Context) error {
			runnerStarted = true
			return nil
		})),
		store,
	)

	if err := b.StartAutomation(); !errors.Is(err, want) {
		t.Fatalf("StartAutomation error = %v, want %v", err, want)
	}
	if runnerStarted {
		t.Fatal("automation runner started after runtime initialization failure")
	}

	dashboard, err := b.GetDashboard()
	if err != nil {
		t.Fatalf("GetDashboard returned error: %v", err)
	}
	if dashboard.Running {
		t.Fatal("dashboard running=true after runtime initialization failure")
	}
	if dashboard.LastStatus != "error: load app settings: app settings unavailable" {
		t.Fatalf("dashboard last status = %q, want deterministic initialization error", dashboard.LastStatus)
	}
}

func TestBindingsSetAccountRolePersistsThenPublishes(t *testing.T) {
	store := &settingsStoreStub{
		settings: domain.DefaultKeywordSettings(), appSettings: domain.DefaultAppSettings(),
		accounts: []domain.Account{{ID: "account", PhoneMasked: "+7 ***", Role: domain.AccountRoleSpammer, Status: domain.AccountActive}},
		catalogs: make(map[domain.SourceCatalog][]domain.Channel),
	}
	b := NewBindings(usecase.NewAutomationController(nil), store)
	before := b.runtime.Current()

	updated, err := b.SetAccountRole("account", string(domain.AccountRoleScoutAnalyst))
	if err != nil {
		t.Fatalf("SetAccountRole returned error: %v", err)
	}
	if updated.Role != string(domain.AccountRoleScoutAnalyst) || store.accounts[0].Role != domain.AccountRoleScoutAnalyst {
		t.Fatalf("account role = %q / %q, want scout", updated.Role, store.accounts[0].Role)
	}
	if got := b.runtime.Current(); got.Revision != before.Revision+1 || got.Roles["account"] != domain.AccountRoleScoutAnalyst {
		t.Fatalf("runtime = %+v, want published scout role", got)
	}

	store.accountSaveErr = errors.New("persist role")
	previous := b.runtime.Current()
	if _, err := b.SetAccountRole("account", string(domain.AccountRoleSpammer)); err == nil {
		t.Fatal("SetAccountRole error = nil after persistence failure")
	}
	if got := b.runtime.Current(); !reflect.DeepEqual(got, previous) {
		t.Fatalf("failed role save published %+v, want %+v", got, previous)
	}
}

func TestAccountDTOExposesDeliveryCursorAndClosedPrivateCount(t *testing.T) {
	public := accountDTO(domain.Account{
		ID: "account-a", NextDelivery: domain.DeliveryTargetPublic, PrivateMessagesClosed: 5,
	}, 7, false, "")
	require.Equal(t, "public", public.NextDelivery)
	require.Equal(t, int64(5), public.PrivateMessagesClosed)

	legacy := accountDTO(domain.Account{ID: "account-legacy"}, 8, false, "")
	require.Equal(t, "private", legacy.NextDelivery)
	require.Zero(t, legacy.PrivateMessagesClosed)
}

func TestAccountDTOExposesOnlySafeSessionErrorCode(t *testing.T) {
	invalid := accountDTO(domain.Account{
		ID: "invalid", Status: domain.AccountError, LastError: "rpc_auth_key_duplicated",
	}, 1, true, "ready")
	require.Equal(t, "rpc_auth_key_duplicated", invalid.ErrorCode)

	unsafe := accountDTO(domain.Account{
		ID: "unsafe", Status: domain.AccountError, LastError: "/private/session path",
	}, 1, true, "ready")
	require.Empty(t, unsafe.ErrorCode)

	transient := accountDTO(domain.Account{
		ID: "transient", Status: domain.AccountStatus("partial"), LastError: "rpc_auth_key_duplicated",
	}, 1, true, "ready")
	require.Empty(t, transient.ErrorCode)
}

func TestAccountDTOExposesUnassignedCapacityWarning(t *testing.T) {
	unassigned := accountDTO(domain.Account{
		ID: "overflow", Status: domain.AccountStopped, ProxyMode: domain.ProxyModeUnassigned,
	}, 7, true, "starting")
	require.Equal(t, "unassigned", unassigned.Proxy)
	require.Equal(t, "route_capacity", unassigned.ProxyWarning)
	require.Equal(t, "stopped", unassigned.Status)

	global := accountDTO(domain.Account{
		ID: "global", Status: domain.AccountStopped, ProxyMode: domain.ProxyModeGlobal,
	}, 8, true, "starting")
	require.Equal(t, "Tor/Snowflake", global.Proxy)
	require.Empty(t, global.ProxyWarning)
	require.Equal(t, "connecting", global.Status)
}

func TestBindingsResumesOnlyExplicitlySelectedLegacyPauseAndPublishes(t *testing.T) {
	store := &settingsStoreStub{
		settings: domain.DefaultKeywordSettings(), appSettings: domain.DefaultAppSettings(),
		accounts: []domain.Account{
			{ID: "selected", Role: domain.AccountRoleSpammer, Status: domain.AccountPaused, LegacyPauseReviewRequired: true},
			{ID: "untouched", Role: domain.AccountRoleSpammer, Status: domain.AccountPaused, LegacyPauseReviewRequired: true},
		},
		catalogs: make(map[domain.SourceCatalog][]domain.Channel),
	}
	b := NewBindings(usecase.NewAutomationController(nil), store)
	before := b.runtime.Current().Revision

	updated, err := b.ResumeLegacyPausedAccount("selected")
	require.NoError(t, err)
	require.Equal(t, "stopped", updated.Status)
	require.False(t, updated.LegacyPauseReviewRequired)
	require.Equal(t, before+1, updated.Revision)
	require.Equal(t, domain.AccountPaused, store.accounts[1].Status)
	require.True(t, store.accounts[1].LegacyPauseReviewRequired)
}

func TestBindingsSetChannelTopicsPersistsAtomicallyThenPublishes(t *testing.T) {
	store := &settingsStoreStub{
		settings: domain.DefaultKeywordSettings(), appSettings: domain.DefaultAppSettings(),
		catalogs: map[domain.SourceCatalog][]domain.Channel{
			domain.SourceCatalogScout: {
				{ID: "one", Link: "https://t.me/one", Topic: "Old", Active: true},
				{ID: "two", Link: "https://t.me/two", Topic: "Old", Active: true},
			},
		},
	}
	b := NewBindings(usecase.NewAutomationController(nil), store)
	before := b.runtime.Current()

	rows, err := b.SetChannelTopics(string(domain.SourceCatalogScout), []string{"one", "two"}, " New ")
	if err != nil {
		t.Fatalf("SetChannelTopics returned error: %v", err)
	}
	if len(rows) != 2 || rows[0].Topic != "New" || rows[1].Topic != "New" {
		t.Fatalf("rows = %+v, want two New topics", rows)
	}
	if got := b.runtime.Current(); got.Revision != before.Revision+1 || len(got.CatalogAssignments[domain.SourceCatalogScout]) != 2 {
		t.Fatalf("runtime = %+v, want published scout assignments", got)
	}

	previous := b.runtime.Current()
	if _, err := b.SetChannelTopics(string(domain.SourceCatalogScout), []string{"one", "missing"}, "Broken"); !errors.Is(err, usecase.ErrChannelNotFound) {
		t.Fatalf("SetChannelTopics error = %v, want channel not found", err)
	}
	if store.catalogs[domain.SourceCatalogScout][0].Topic != "New" || store.catalogs[domain.SourceCatalogScout][1].Topic != "New" {
		t.Fatalf("failed transaction changed rows: %+v", store.catalogs[domain.SourceCatalogScout])
	}
	if got := b.runtime.Current(); !reflect.DeepEqual(got, previous) {
		t.Fatalf("failed topic save published %+v, want %+v", got, previous)
	}
}

func TestAnalysisRunPublishesResultsAndStatus(t *testing.T) {
	analyzer := &analyticsServiceStub{result: analyticsusecase.AnalysisResult{
		RunID: "run-1", Status: "complete",
		GeneralWords: []coreanalytics.Candidate{{NormalizedValue: "деньг", DisplayValue: "Деньги", Kind: "word", Frequency: 8, SourceDiversity: 3, Score: 19}},
	}}
	b := NewBindingsWithAnalytics(usecase.NewAutomationController(nil), &settingsStoreStub{}, analyzer, nil, nil)

	started, err := b.StartAnalysis(AnalysisRequestDTO{SourceScope: "all", SourceTopics: []string{"Финансы"}})
	require.NoError(t, err)
	require.Equal(t, "running", started.Status)
	require.Eventually(t, func() bool { return b.GetAnalysisStatus().Status == "complete" }, time.Second, time.Millisecond)

	result := b.GetAnalysisResults()
	require.Equal(t, "run-1", result.RunID)
	require.Len(t, result.GeneralWords, 1)
	require.Equal(t, "Деньги", result.GeneralWords[0].DisplayValue)
	require.Equal(t, []string{"Финансы"}, analyzer.request.SourceTopics)
}

func TestAnalysisSynchronizesScoutHistoryBeforeAnalyzing(t *testing.T) {
	history := &historySyncerStub{called: make(chan struct{})}
	analyzer := &analyticsServiceStub{result: analyticsusecase.AnalysisResult{RunID: "run-history", Status: "complete"}}
	analyzer.analyze = func(ctx context.Context, request analyticsusecase.AnalysisRequest) (analyticsusecase.AnalysisResult, error) {
		select {
		case <-history.called:
			return analyzer.result, nil
		default:
			return analyticsusecase.AnalysisResult{}, errors.New("analysis started before history sync")
		}
	}
	b := NewBindingsWithAnalytics(usecase.NewAutomationController(nil), &settingsStoreStub{}, analyzer, nil, nil)
	b.historySyncer = history

	_, err := b.StartAnalysis(AnalysisRequestDTO{SourceScope: "all"})
	require.NoError(t, err)
	require.Eventually(t, func() bool { return b.GetAnalysisStatus().Status == "complete" }, time.Second, time.Millisecond)
	require.Equal(t, "run-history", b.GetAnalysisResults().RunID)
}

func TestAnalysisResultsPreservePersistedModerationState(t *testing.T) {
	analyzer := &analyticsServiceStub{
		result: analyticsusecase.AnalysisResult{RunID: "run-1", Status: "complete", GeneralWords: []coreanalytics.Candidate{{NormalizedValue: "деньг", DisplayValue: "Деньги", Kind: "word"}}},
		states: map[analyticsusecase.CandidateKey]string{{NormalizedValue: "деньг", Kind: "word"}: "added_to_keywords"},
	}
	b := NewBindingsWithAnalytics(usecase.NewAutomationController(nil), &settingsStoreStub{}, analyzer, nil, nil)

	_, err := b.StartAnalysis(AnalysisRequestDTO{SourceScope: "all"})
	require.NoError(t, err)
	require.Eventually(t, func() bool { return b.GetAnalysisStatus().Status == "complete" }, time.Second, time.Millisecond)

	require.Equal(t, "added_to_keywords", b.GetAnalysisResults().GeneralWords[0].ModerationState)
}

func TestCompareAnalysisTopicsReturnsPerTopicSummaries(t *testing.T) {
	analyzer := &analyticsServiceStub{}
	b := NewBindingsWithAnalytics(usecase.NewAutomationController(nil), &settingsStoreStub{}, analyzer, nil, nil)

	status, err := b.StartTopicComparison(TopicComparisonRequestDTO{SourceScope: "all", ProfileID: "money", Topics: []string{"A", "B"}})
	require.NoError(t, err)
	require.Equal(t, "comparison", status.Operation)
	require.Eventually(t, func() bool { return b.GetAnalysisStatus().Status == "complete" }, time.Second, time.Millisecond)
	rows := b.GetTopicComparisonResults()
	require.Len(t, rows, 1)
	require.Equal(t, "A", rows[0].Topic)
	require.Equal(t, 3, rows[0].Count)
	require.Equal(t, .75, rows[0].RelativeShare)
	require.Equal(t, []string{"A", "B"}, analyzer.compare.Topics)
}

func TestTopicComparisonCancellationDoesNotPublishLateResults(t *testing.T) {
	analyzer := &analyticsServiceStub{
		compareStarted: make(chan struct{}), compareRelease: make(chan struct{}), compareCancelled: make(chan struct{}),
	}
	b := NewBindingsWithAnalytics(usecase.NewAutomationController(nil), &settingsStoreStub{}, analyzer, nil, nil)

	status, err := b.StartTopicComparison(TopicComparisonRequestDTO{SourceScope: "all", Topics: []string{"A", "B"}})
	require.NoError(t, err)
	require.Equal(t, "running", status.Status)
	<-analyzer.compareStarted
	require.NoError(t, b.CancelAnalysis())
	<-analyzer.compareCancelled
	close(analyzer.compareRelease)
	require.Eventually(t, func() bool { return b.GetAnalysisStatus().Status == "cancelled" }, time.Second, time.Millisecond)
	require.Empty(t, b.GetTopicComparisonResults())
}

func TestAnalysisStatusDefaultsToIdle(t *testing.T) {
	b := NewBindings(usecase.NewAutomationController(nil))
	require.Equal(t, "idle", b.GetAnalysisStatus().Status)
}

func TestAnalysisRunCanBeCancelled(t *testing.T) {
	analyzer := &analyticsServiceStub{started: make(chan struct{})}
	b := NewBindingsWithAnalytics(usecase.NewAutomationController(nil), &settingsStoreStub{}, analyzer, nil, nil)

	_, err := b.StartAnalysis(AnalysisRequestDTO{SourceScope: "all"})
	require.NoError(t, err)
	<-analyzer.started
	require.NoError(t, b.CancelAnalysis())
	require.Eventually(t, func() bool { return b.GetAnalysisStatus().Status == "cancelled" }, time.Second, time.Millisecond)
}

func TestBindingsRootContextOwnsAnalysisAndBackupRunner(t *testing.T) {
	root, cancelRoot := context.WithCancel(context.Background())
	analyzer := &analyticsServiceStub{started: make(chan struct{})}
	backup := &backupRunnerStub{started: make(chan struct{}), stopped: make(chan struct{})}
	b := NewBindingsWithAnalytics(usecase.NewAutomationController(nil), &settingsStoreStub{}, analyzer, nil, nil)
	b.backupRunner = backup
	b.SetRootContext(root)
	require.NoError(t, b.StartBackgroundServices())
	<-backup.started
	_, err := b.StartAnalysis(AnalysisRequestDTO{SourceScope: "all"})
	require.NoError(t, err)
	<-analyzer.started

	cancelRoot()
	require.Eventually(t, func() bool { return b.GetAnalysisStatus().Status == "cancelled" }, time.Second, time.Millisecond)
	select {
	case <-backup.stopped:
	case <-time.After(time.Second):
		t.Fatal("backup runner did not inherit root cancellation")
	}
}

func TestStopAnalysisWaitsForActiveOperationToExit(t *testing.T) {
	analyzer := &analyticsServiceStub{started: make(chan struct{})}
	b := NewBindingsWithAnalytics(usecase.NewAutomationController(nil), &settingsStoreStub{}, analyzer, nil, nil)
	_, err := b.StartAnalysis(AnalysisRequestDTO{SourceScope: "all"})
	require.NoError(t, err)
	<-analyzer.started

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, b.StopAnalysis(ctx))
	require.Equal(t, "cancelled", b.GetAnalysisStatus().Status)
}

func TestAnalyticsModerationAndAddAllDelegate(t *testing.T) {
	analyzer := &analyticsServiceStub{}
	b := NewBindingsWithAnalytics(usecase.NewAutomationController(nil), &settingsStoreStub{}, analyzer, nil, nil)

	require.NoError(t, b.ModerateAnalysisCandidate("деньг", "word", "accepted"))
	added, err := b.AddAnalysisCandidates([]AnalysisCandidateRefDTO{{RunID: "run-1", NormalizedValue: "деньг", Kind: "word"}})
	require.NoError(t, err)
	require.Equal(t, 1, added)
	require.Equal(t, [3]string{"деньг", "word", "accepted"}, analyzer.moderate)
	require.Equal(t, "run-1", analyzer.refs[0].RunID)
}

func TestAddAnalysisCandidatesPublishesCommittedKeywordSettingsToRuntime(t *testing.T) {
	store := &settingsStoreStub{settings: domain.KeywordSettings{SharedReply: "reply"}}
	analyzer := &analyticsServiceStub{onAdd: func() {
		require.NoError(t, store.Save(context.Background(), domain.KeywordSettings{
			Keywords: []string{"accepted keyword"}, SharedReply: "reply",
		}))
	}}
	b := NewBindingsWithAnalytics(usecase.NewAutomationController(nil), store, analyzer, nil, nil)

	_, err := b.AddAnalysisCandidates([]AnalysisCandidateRefDTO{{RunID: "run-1", NormalizedValue: "accepted keyword", Kind: "phrase"}})
	require.NoError(t, err)

	got := b.runtime.Current()
	require.Equal(t, []string{"accepted keyword"}, got.Keywords)
	require.Equal(t, "reply", got.SharedReply)
}

func TestAIImportAndFileOpenUseInjectedDependencies(t *testing.T) {
	importer := &importServiceStub{}
	opener := &fileOpenerStub{}
	b := NewBindingsWithAnalytics(usecase.NewAutomationController(nil), &settingsStoreStub{}, nil, importer, opener)

	imported, err := b.ImportAIFile(AIImportRequestDTO{Path: "/tmp/keywords.csv", SHA256: "abc", RightsConfirmed: true, ProfileID: "money"})
	require.NoError(t, err)
	require.Equal(t, "import-1", imported.ID)
	require.True(t, importer.request.UseAI)
	require.Equal(t, domain.SourceKindLocalImport, importer.request.SourceKind)
	require.Empty(t, imported.FilePath)
	require.NoError(t, b.OpenFile("/tmp/keywords.csv"))
	require.Equal(t, "/tmp/keywords.csv", opener.path)
}

func TestNativeAIImportComputesHashBackendAndReloadsPersistedCandidates(t *testing.T) {
	importer := &importServiceStub{
		imports: []domain.Import{{ID: "import-1", FileName: "keywords.csv", Status: domain.ImportStatusComplete}},
		candidates: []domain.KeywordCandidate{{
			ID: "candidate-1", RunID: "import-1", NormalizedValue: "need money", DisplayValue: "Need money",
			Kind: "phrase", Frequency: 3, SourceDiversity: 1, Score: 0.9, Source: "import_ai", ModerationState: "new",
		}},
	}
	b := NewBindingsWithAnalytics(usecase.NewAutomationController(nil), &settingsStoreStub{}, nil, importer, nil)
	selector := &importFileSelectorStub{path: "/private/user/keywords.csv"}
	b.ConfigureImportFileSelector(selector)

	imported, err := b.SelectAndImportAIFile(true, "money-shortage", "ru")
	require.NoError(t, err)
	require.Equal(t, "ru", selector.locale)
	require.Equal(t, "/private/user/keywords.csv", importer.request.Path)
	require.Empty(t, importer.request.SHA256)
	require.Empty(t, imported.FilePath)
	imports, err := b.GetAIImports()
	require.NoError(t, err)
	require.Len(t, imports, 1)
	candidates, err := b.GetAIImportCandidates("import-1")
	require.NoError(t, err)
	require.Len(t, candidates, 1)
	require.Equal(t, "import_ai", candidates[0].Source)
}

func TestNativeAIImportCandidatesExportUsesRootOperationAndSaveSelection(t *testing.T) {
	importer := &importServiceStub{}
	b := NewBindingsWithAnalytics(usecase.NewAutomationController(nil), &settingsStoreStub{}, nil, importer, nil)
	selector := &importFileSelectorStub{exportPath: "/selected/import-candidates.txt"}
	b.ConfigureImportCandidateExportSelector(selector)

	path, err := b.ExportAIImportCandidates("import-1", "en")
	require.NoError(t, err)
	require.Equal(t, "en", selector.locale)
	require.Equal(t, "/selected/import-candidates.txt", path)
	require.Equal(t, domain.ID("import-1"), importer.exportRun)
	require.Equal(t, path, importer.exportPath)
}

type proxyStatusProviderStub struct{ status ProxyStatusDTO }

func (s proxyStatusProviderStub) Status() ProxyStatusDTO { return s.status }

func TestBindingsExposeManagedProxyStatusAndEffectiveAccountRoute(t *testing.T) {
	updated := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	want := ProxyStatusDTO{
		Mode: "tor_snowflake", State: "ready", Address: "127.0.0.1:19050",
		Transport: "snowflake", RestartCount: 2, UpdatedAt: updated.Format(time.RFC3339Nano),
		AutoRestart: true,
	}
	store := &settingsStoreStub{
		settings: domain.DefaultKeywordSettings(),
		accounts: []domain.Account{{ID: "account-a", ProxyMode: domain.ProxyModeGlobal}},
	}
	bindings := NewBindings(usecase.NewAutomationController(nil), store)
	bindings.ConfigureProxyStatus(proxyStatusProviderStub{status: want})

	require.Equal(t, want, bindings.GetProxyStatus())
	accounts, err := bindings.GetAccounts()
	require.NoError(t, err)
	require.Len(t, accounts, 1)
	require.Equal(t, "Tor/Snowflake", accounts[0].Proxy)
}

func TestBindingsNeverExposeDirectModeAsManagedSystemProxy(t *testing.T) {
	store := &settingsStoreStub{
		settings: domain.DefaultKeywordSettings(),
		accounts: []domain.Account{{ID: "account-a", ProxyMode: domain.ProxyModeDirect}},
	}
	bindings := NewBindings(usecase.NewAutomationController(nil), store)
	bindings.ConfigureProxyStatus(proxyStatusProviderStub{status: ProxyStatusDTO{State: "ready"}})

	accounts, err := bindings.GetAccounts()
	require.NoError(t, err)
	require.Equal(t, "unassigned", accounts[0].Proxy)
	require.Equal(t, "route_capacity", accounts[0].ProxyWarning)
	require.Equal(t, string(domain.ProxyModeUnassigned), accountProxyRouteID(store.accounts[0]))
}

func TestBindingsShowConnectingWhileManagedProxyStarts(t *testing.T) {
	store := &settingsStoreStub{
		settings: domain.DefaultKeywordSettings(),
		accounts: []domain.Account{{ID: "account-a", Status: domain.AccountStopped}},
	}
	bindings := NewBindings(usecase.NewAutomationController(nil), store)
	bindings.ConfigureProxyStatus(proxyStatusProviderStub{status: ProxyStatusDTO{State: "starting"}})

	accounts, err := bindings.GetAccounts()
	require.NoError(t, err)
	require.Len(t, accounts, 1)
	require.Equal(t, "connecting", accounts[0].Status)
}

func TestNormalizeChannelLinkPreservesTelegramDiscussionTarget(t *testing.T) {
	require.Equal(t,
		"https://t.me/+mgppYi2XAIMxYjFi#tc-discussion=4291488698",
		normalizeChannelLink("https://t.me/+mgppYi2XAIMxYjFi#tc-discussion=4291488698"),
	)
	require.Equal(t,
		"https://t.me/+mgppYi2XAIMxYjFi",
		normalizeChannelLink("https://t.me/+mgppYi2XAIMxYjFi#untrusted-fragment"),
	)
}

func TestScheduledDMBindingsExposeSafePausedTaskDTOs(t *testing.T) {
	now := time.Now().UTC()
	repository := &scheduledDMBindingRepository{}
	accounts := &scheduledDMBindingAccounts{accounts: []domain.Account{{
		ID: "spammer-1", Role: domain.AccountRoleSpammer, Status: domain.AccountActive,
	}}}
	bindings := scheduledDMBindingsForTest(repository, accounts, &scheduledDMBindingResolver{}, nil)

	saved, err := bindings.SaveScheduledDMTask(ScheduledDMDraftDTO{
		MessageText: "Hello from this task", Recipients: []string{"contact_one"}, AccountIDs: []string{"spammer-1"},
		StartAt: now.Add(time.Hour).Format(time.RFC3339Nano), Recurrence: "once", MaxRuns: 1,
	})
	require.NoError(t, err)
	require.Equal(t, "paused", saved.Status)
	require.Equal(t, "contact_one", saved.Recipients[0].Username)
	require.Equal(t, "spammer-1", saved.Recipients[0].AccountID)
	require.Equal(t, "unchecked", saved.Recipients[0].Status)

	rows, err := bindings.ListScheduledDMTasks()
	require.NoError(t, err)
	require.Equal(t, []ScheduledDMTaskDTO{saved}, rows)

	for _, dto := range []any{ScheduledDMDraftDTO{}, ScheduledDMTaskDTO{}, ScheduledDMRecipientDTO{}} {
		typ := reflect.TypeOf(dto)
		for index := 0; index < typ.NumField(); index++ {
			name := strings.ToLower(typ.Field(index).Name)
			require.NotContains(t, name, "password")
			require.NotContains(t, name, "session")
			require.NotContains(t, name, "hash")
			require.NotContains(t, name, "phone")
		}
	}
}

func TestScheduledDMBindingsRejectNewWorkButKeepQuiesceMethodsAfterOperationsClose(t *testing.T) {
	now := time.Now().UTC()
	repository := &scheduledDMBindingRepository{tasks: []domain.ScheduledDMTask{{
		ID: "task-1", Status: domain.ScheduledDMStatusActive, StartAt: now.Add(time.Hour),
		Recipients: []domain.ScheduledDMRecipient{{TaskID: "task-1", Ordinal: 0, Username: "contact_one"}},
		AccountIDs: []domain.ID{"spammer-1"},
	}}}
	accounts := &scheduledDMBindingAccounts{accounts: []domain.Account{{
		ID: "spammer-1", Role: domain.AccountRoleSpammer, Status: domain.AccountActive,
	}}}
	bindings := scheduledDMBindingsForTest(repository, accounts, &scheduledDMBindingResolver{}, nil)
	require.NoError(t, bindings.StopOperations(context.Background()))

	_, err := bindings.SaveScheduledDMTask(ScheduledDMDraftDTO{StartAt: now.Format(time.RFC3339Nano)})
	require.ErrorIs(t, err, errDesktopOperationsClosed)
	require.ErrorIs(t, bindings.StartScheduledDMTask("task-1"), errDesktopOperationsClosed)
	_, err = bindings.ResolveScheduledDMRecipients("task-1")
	require.ErrorIs(t, err, errDesktopOperationsClosed)

	require.NoError(t, bindings.StopScheduledDMTask("task-1"))
	require.NoError(t, bindings.CancelScheduledDMTask("task-1"))
}

func TestScheduledDMBindingsStartOnlyTheDedicatedRunner(t *testing.T) {
	now := time.Now().UTC()
	repository := &scheduledDMBindingRepository{tasks: []domain.ScheduledDMTask{{
		ID: "task-1", Status: domain.ScheduledDMStatusPaused, StartAt: now.Add(time.Hour),
		Recipients: []domain.ScheduledDMRecipient{{TaskID: "task-1", Ordinal: 0, Username: "contact_one"}}, AccountIDs: []domain.ID{"spammer-1"},
	}}}
	accounts := &scheduledDMBindingAccounts{accounts: []domain.Account{{
		ID: "spammer-1", Role: domain.AccountRoleSpammer, Status: domain.AccountActive,
	}}}
	dedicatedStarted := make(chan struct{})
	dedicatedRunner := usecase.NewAutomationController(usecase.AutomationRunnerFunc(func(ctx context.Context) error {
		close(dedicatedStarted)
		<-ctx.Done()
		return ctx.Err()
	}))
	globalStarted := make(chan struct{})
	globalRunner := usecase.NewAutomationController(usecase.AutomationRunnerFunc(func(ctx context.Context) error {
		close(globalStarted)
		<-ctx.Done()
		return ctx.Err()
	}))
	bindings := scheduledDMBindingsForTest(repository, accounts, &scheduledDMBindingResolver{}, dedicatedRunner)
	bindings.SetAutomationController(globalRunner)

	require.NoError(t, bindings.StartAutomation())
	require.Eventually(t, func() bool {
		select {
		case <-globalStarted:
			return true
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)
	select {
	case <-dedicatedStarted:
		t.Fatal("global automation started scheduled DM runner")
	default:
	}

	require.NoError(t, bindings.StartScheduledDMTask("task-1"))
	require.Eventually(t, func() bool {
		select {
		case <-dedicatedStarted:
			return true
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)
	require.Equal(t, domain.ScheduledDMStatusActive, repository.statusChanges[0].status)
	require.NoError(t, bindings.StopScheduledDMTask("task-1"))
	require.NoError(t, bindings.CancelScheduledDMTask("task-1"))
	require.Equal(t, []domain.ScheduledDMStatus{
		domain.ScheduledDMStatusActive, domain.ScheduledDMStatusPaused, domain.ScheduledDMStatusCancelled,
	}, scheduledDMBindingStatuses(repository.statusChanges))
	require.NoError(t, bindings.StopAutomation())
	require.NoError(t, dedicatedRunner.Stop(context.Background()))
}

func TestScheduledDMBindingsFailedActivationStopsNewlyStartedDedicatedRunner(t *testing.T) {
	activationErr := errors.New("activate scheduled DM task")
	repository := &scheduledDMBindingRepository{setTaskStatusErr: activationErr}
	accounts := &scheduledDMBindingAccounts{}
	runnerStarted := make(chan struct{})
	dedicatedRunner := usecase.NewAutomationController(usecase.AutomationRunnerFunc(func(ctx context.Context) error {
		close(runnerStarted)
		<-ctx.Done()
		return ctx.Err()
	}))
	t.Cleanup(func() {
		require.NoError(t, dedicatedRunner.Stop(context.Background()))
	})
	bindings := scheduledDMBindingsForTest(repository, accounts, &scheduledDMBindingResolver{}, dedicatedRunner)

	require.ErrorIs(t, bindings.StartScheduledDMTask("task-1"), activationErr)
	require.False(t, dedicatedRunner.Running())
	select {
	case <-runnerStarted:
	default:
		t.Fatal("scheduled DM runner did not start before task activation")
	}
}

func TestScheduledDMBindingsFailedActivationKeepsAlreadyRunningDedicatedRunner(t *testing.T) {
	activationErr := errors.New("activate scheduled DM task")
	repository := &scheduledDMBindingRepository{setTaskStatusErr: activationErr}
	runnerStarted := make(chan struct{})
	dedicatedRunner := usecase.NewAutomationController(usecase.AutomationRunnerFunc(func(ctx context.Context) error {
		close(runnerStarted)
		<-ctx.Done()
		return ctx.Err()
	}))
	t.Cleanup(func() {
		require.NoError(t, dedicatedRunner.Stop(context.Background()))
	})
	require.NoError(t, dedicatedRunner.Start(context.Background()))
	require.Eventually(t, func() bool {
		select {
		case <-runnerStarted:
			return true
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)
	bindings := scheduledDMBindingsForTest(
		repository,
		&scheduledDMBindingAccounts{},
		&scheduledDMBindingResolver{},
		dedicatedRunner,
	)

	require.ErrorIs(t, bindings.StartScheduledDMTask("task-1"), activationErr)
	require.True(t, dedicatedRunner.Running())
}

func TestScheduledDMBindingsRunnerStartFailureLeavesTaskPaused(t *testing.T) {
	repository := &scheduledDMBindingRepository{}
	bindings := scheduledDMBindingsForTest(
		repository,
		&scheduledDMBindingAccounts{},
		&scheduledDMBindingResolver{},
		usecase.NewAutomationController(nil),
	)

	require.ErrorIs(t, bindings.StartScheduledDMTask("task-1"), usecase.ErrAutomationRunnerNotConfigured)
	require.Empty(t, repository.statusChanges)
}

func TestScheduledDMBindingsResolveRecipientsWithoutClaimingMessageAvailability(t *testing.T) {
	now := time.Now().UTC()
	repository := &scheduledDMBindingRepository{tasks: []domain.ScheduledDMTask{{
		ID: "task-1", StartAt: now.Add(time.Hour), AccountIDs: []domain.ID{"spammer-1"},
		Recipients: []domain.ScheduledDMRecipient{{TaskID: "task-1", Ordinal: 0, Username: "found"}, {TaskID: "task-1", Ordinal: 1, Username: "missing"}},
	}}}
	accounts := &scheduledDMBindingAccounts{accounts: []domain.Account{{
		ID: "spammer-1", Role: domain.AccountRoleSpammer, Status: domain.AccountActive,
	}}}
	resolver := &scheduledDMBindingResolver{errors: map[string]error{"missing": domain.PermanentDeliveryFailure(errors.New("USERNAME_NOT_OCCUPIED"))}}
	bindings := scheduledDMBindingsForTest(repository, accounts, resolver, nil)

	resolved, err := bindings.ResolveScheduledDMRecipients("task-1")
	require.NoError(t, err)
	require.Equal(t, []ScheduledDMRecipientDTO{
		{Username: "found", AccountID: "spammer-1", Status: "resolved"},
		{Username: "missing", AccountID: "spammer-1", Status: "invalid", LastError: "recipient_invalid"},
	}, []ScheduledDMRecipientDTO{
		{Username: resolved[0].Username, AccountID: resolved[0].AccountID, Status: resolved[0].Status, LastError: resolved[0].LastError},
		{Username: resolved[1].Username, AccountID: resolved[1].AccountID, Status: resolved[1].Status, LastError: resolved[1].LastError},
	})
	require.NotEmpty(t, resolved[0].LastCheckedAt)
	require.Equal(t, resolved[0].LastCheckedAt, resolved[1].LastCheckedAt)
	require.Equal(t, []domain.Account{accounts.accounts[0], accounts.accounts[0]}, resolver.accounts)
	for _, recipient := range resolved {
		require.NotContains(t, []string{"open", "closed"}, recipient.Status)
	}
}

func scheduledDMBindingsForTest(repository *scheduledDMBindingRepository, accounts *scheduledDMBindingAccounts, resolver *scheduledDMBindingResolver, runner *usecase.AutomationController) *Bindings {
	bindings := NewBindings(usecase.NewAutomationController(nil))
	ConfigureScheduledDM(
		bindings,
		usecase.NewScheduledDMService(repository, accounts, time.Now),
		repository,
		accounts,
		resolver,
		runner,
	)
	return bindings
}

type scheduledDMBindingRepository struct {
	tasks            []domain.ScheduledDMTask
	statusChanges    []scheduledDMBindingStatusChange
	setTaskStatusErr error
}

type scheduledDMBindingStatusChange struct{ status domain.ScheduledDMStatus }

func (r *scheduledDMBindingRepository) ListTasks(context.Context) ([]domain.ScheduledDMTask, error) {
	return append([]domain.ScheduledDMTask(nil), r.tasks...), nil
}
func (r *scheduledDMBindingRepository) SaveTask(_ context.Context, task domain.ScheduledDMTask) error {
	r.tasks = append(r.tasks, task)
	return nil
}
func (r *scheduledDMBindingRepository) SetTaskStatus(_ context.Context, id domain.ID, status domain.ScheduledDMStatus, _ time.Time) error {
	if r.setTaskStatusErr != nil {
		return r.setTaskStatusErr
	}
	r.statusChanges = append(r.statusChanges, scheduledDMBindingStatusChange{status: status})
	for index := range r.tasks {
		if r.tasks[index].ID == id {
			r.tasks[index].Status = status
		}
	}
	return nil
}
func (*scheduledDMBindingRepository) ClaimDueDelivery(context.Context, time.Time, time.Duration) (*domain.ScheduledDMDelivery, error) {
	return nil, nil
}
func (*scheduledDMBindingRepository) CompleteDelivery(context.Context, domain.ScheduledDMDeliveryResult) error {
	return nil
}
func (*scheduledDMBindingRepository) DelayDelivery(context.Context, domain.ID, string, string, time.Time) error {
	return nil
}

func scheduledDMBindingStatuses(changes []scheduledDMBindingStatusChange) []domain.ScheduledDMStatus {
	statuses := make([]domain.ScheduledDMStatus, len(changes))
	for index, change := range changes {
		statuses[index] = change.status
	}
	return statuses
}

type scheduledDMBindingAccounts struct{ accounts []domain.Account }

func (a *scheduledDMBindingAccounts) ListAccounts(context.Context) ([]domain.Account, error) {
	return a.accounts, nil
}
func (*scheduledDMBindingAccounts) SaveAccount(context.Context, domain.Account) error { return nil }
func (a *scheduledDMBindingAccounts) List(context.Context) ([]domain.Account, error) {
	return a.accounts, nil
}
func (a *scheduledDMBindingAccounts) ListActive(context.Context) ([]domain.Account, error) {
	return a.accounts, nil
}
func (*scheduledDMBindingAccounts) Save(context.Context, domain.Account) error { return nil }

type scheduledDMBindingResolver struct {
	errors   map[string]error
	accounts []domain.Account
}

func (r *scheduledDMBindingResolver) ResolveUsername(_ context.Context, account domain.Account, username string) error {
	r.accounts = append(r.accounts, account)
	return r.errors[username]
}

func (*scheduledDMBindingResolver) SendScheduledPrivateMessage(context.Context, domain.Account, domain.ScheduledDMDelivery, string) error {
	return nil
}
