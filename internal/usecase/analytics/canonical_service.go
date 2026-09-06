package analytics

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"

	core "telegram-companion/internal/analytics"
	"telegram-companion/internal/domain"
)

type CanonicalStore interface {
	Rows(ctx context.Context, sourceScope string, topics []string) ([]SourceRow, error)
	SyncCanonical(ctx context.Context, values []core.CanonicalObservation) error
	ListCanonical(ctx context.Context, class core.KeywordClass) ([]core.CanonicalKeyword, error)
	ClassifyCanonical(ctx context.Context, id string, class core.KeywordClass) error
	SetCanonicalTrigger(ctx context.Context, id string, active bool) error
	RemoveCanonicalForm(ctx context.Context, id, form string) (core.CanonicalKeyword, error)
	MoveCanonicalForm(ctx context.Context, targetID, form string) error
	AddCanonicalForm(ctx context.Context, targetID, form string) error
	DeleteCanonical(ctx context.Context, id string) error
	DeleteCanonicalTrigger(ctx context.Context, id string) error
	ClearCanonicalClass(ctx context.Context, class core.KeywordClass) error
	SearchCanonical(ctx context.Context, query string, class core.KeywordClass) ([]core.CanonicalKeyword, error)
	CanonicalMetrics(ctx context.Context) (core.CanonicalMetrics, error)
	BulkImportCanonical(ctx context.Context, values []core.CanonicalImportValue, class core.KeywordClass) (core.CanonicalBulkImportResult, error)
}

type CanonicalAnalysisResult struct {
	MessageCount int64
	KeywordCount int
}

type ServiceWordProvider interface {
	ServiceWords(ctx context.Context, language domain.AnalyticsLanguage) ([]string, error)
}

type CanonicalService struct {
	store               CanonicalStore
	serviceWordProvider ServiceWordProvider
}

func NewCanonicalService(store CanonicalStore, providers ...ServiceWordProvider) *CanonicalService {
	service := &CanonicalService{store: store}
	if len(providers) > 0 {
		service.serviceWordProvider = providers[0]
	}
	return service
}

func (s *CanonicalService) Analyze(ctx context.Context) (CanonicalAnalysisResult, error) {
	if s == nil || s.store == nil {
		return CanonicalAnalysisResult{}, errors.New("canonical analytics store is required")
	}
	serviceWords, err := s.loadServiceWords(ctx)
	if err != nil {
		return CanonicalAnalysisResult{}, err
	}
	rows, err := s.store.Rows(ctx, ScopeAll, nil)
	if err != nil {
		return CanonicalAnalysisResult{}, err
	}
	documents := make([]core.Document, 0, len(rows))
	var latest time.Time
	for index, row := range rows {
		messageID := strings.TrimSpace(row.MessageID)
		if messageID == "" {
			messageID = strings.TrimSpace(row.SourceID)
		}
		if messageID == "" {
			messageID = "message:" + time.Unix(int64(index), 0).UTC().Format(time.RFC3339Nano)
		}
		documents = append(documents, core.Document{Text: row.Text, SourceID: messageID})
		if row.MessageAt.After(latest) {
			latest = row.MessageAt
		}
	}
	words := core.ExtractCanonicalWords(documents)
	observations := make([]core.CanonicalObservation, 0, len(words))
	for _, word := range words {
		language := core.Language(word.Language)
		if matchesServiceWord(word.Canonical, word.Forms, serviceWords[language]) {
			continue
		}
		forms := make([]core.FormObservation, 0, len(word.Forms))
		for _, form := range word.Forms {
			forms = append(forms, core.FormObservation{Form: form.Value, Frequency: int64(form.Frequency), LastSeen: latest})
		}
		observations = append(observations, core.CanonicalObservation{
			Canonical: word.Canonical, Language: language,
			TotalFrequency: int64(word.Frequency), MessageCount: int64(word.MessageCount),
			LastSeen: latest, Forms: forms,
		})
	}
	if err := s.deletePersistedServiceWords(ctx, serviceWords); err != nil {
		return CanonicalAnalysisResult{}, err
	}
	if err := s.store.SyncCanonical(ctx, observations); err != nil {
		return CanonicalAnalysisResult{}, err
	}
	return CanonicalAnalysisResult{MessageCount: int64(len(rows)), KeywordCount: len(observations)}, nil
}

func (s *CanonicalService) BulkImport(ctx context.Context, values []core.CanonicalImportValue, class core.KeywordClass) (core.CanonicalBulkImportResult, error) {
	if s == nil || s.store == nil {
		return core.CanonicalBulkImportResult{}, errors.New("canonical analytics store is required")
	}
	if class != core.ClassPositive && class != core.ClassNegative {
		return core.CanonicalBulkImportResult{}, errors.New("bulk keyword class must be positive or negative")
	}
	accepted, skipped := normalizeCanonicalImportValues(values)
	result, err := s.store.BulkImportCanonical(ctx, accepted, class)
	if err != nil {
		return core.CanonicalBulkImportResult{}, err
	}
	result.Skipped = skipped
	return result, nil
}

func normalizeCanonicalImportValues(values []core.CanonicalImportValue) ([]core.CanonicalImportValue, int) {
	accepted := make([]core.CanonicalImportValue, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	skipped := 0
	for _, value := range values {
		normalized, language, ok := normalizeCanonicalImportValue(value.Value)
		if !ok {
			skipped++
			continue
		}
		key := string(language) + "\x00" + normalized
		if _, exists := seen[key]; exists {
			skipped++
			continue
		}
		seen[key] = struct{}{}
		forms := make([]string, 0, len(value.Forms))
		seenForms := make(map[string]struct{}, len(value.Forms))
		for _, form := range value.Forms {
			normalizedForm, formLanguage, valid := normalizeCanonicalImportForm(form)
			if !valid || formLanguage != language {
				skipped++
				continue
			}
			if _, exists := seenForms[normalizedForm]; exists {
				continue
			}
			seenForms[normalizedForm] = struct{}{}
			forms = append(forms, normalizedForm)
		}
		accepted = append(accepted, core.CanonicalImportValue{Value: normalized, Language: language, Forms: forms})
	}
	return accepted, skipped
}

func normalizeCanonicalImportForm(value string) (string, core.Language, bool) {
	normalized, language, ok := normalizeCanonicalImportValue(value)
	if !ok || len(strings.Fields(normalized)) != 1 {
		return "", "", false
	}
	return normalized, language, true
}

func normalizeCanonicalImportValue(value string) (string, core.Language, bool) {
	value = norm.NFC.String(strings.ToLower(strings.TrimSpace(value)))
	words := strings.Fields(value)
	if len(words) == 0 || len(words) > 8 {
		return "", "", false
	}
	value = strings.Join(words, " ")
	if len([]rune(value)) > 120 {
		return "", "", false
	}
	language := core.Language("")
	for _, word := range words {
		for _, r := range word {
			current := core.Language("")
			switch {
			case r >= 'a' && r <= 'z':
				current = core.LanguageEN
			case (r >= 'а' && r <= 'я') || r == 'ё':
				current = core.LanguageRU
			default:
				return "", "", false
			}
			if language != "" && language != current {
				return "", "", false
			}
			language = current
		}
	}
	return value, language, true
}

func (s *CanonicalService) loadServiceWords(ctx context.Context) (map[core.Language]map[string]struct{}, error) {
	result := make(map[core.Language]map[string]struct{}, 2)
	if s.serviceWordProvider == nil {
		return result, nil
	}
	for _, language := range []domain.AnalyticsLanguage{domain.AnalyticsLanguageRU, domain.AnalyticsLanguageEN} {
		words, err := s.serviceWordProvider.ServiceWords(ctx, language)
		if err != nil {
			return nil, err
		}
		normalized := make(map[string]struct{}, len(words))
		for _, word := range words {
			if value, ok := normalizeServiceWord(word, language); ok {
				normalized[value] = struct{}{}
			}
		}
		result[core.Language(language)] = normalized
	}
	return result, nil
}

func normalizeServiceWord(value string, language domain.AnalyticsLanguage) (string, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || len(strings.Fields(value)) != 1 {
		return "", false
	}
	for _, r := range value {
		if language == domain.AnalyticsLanguageRU && !unicode.In(r, unicode.Cyrillic) {
			return "", false
		}
		if language == domain.AnalyticsLanguageEN && !unicode.In(r, unicode.Latin) {
			return "", false
		}
	}
	return value, true
}

func matchesServiceWord(canonical string, forms []core.CanonicalForm, words map[string]struct{}) bool {
	if _, ok := words[strings.ToLower(strings.TrimSpace(canonical))]; ok {
		return true
	}
	for _, form := range forms {
		if _, ok := words[strings.ToLower(strings.TrimSpace(form.Value))]; ok {
			return true
		}
	}
	return false
}

func (s *CanonicalService) deletePersistedServiceWords(ctx context.Context, serviceWords map[core.Language]map[string]struct{}) error {
	if s.serviceWordProvider == nil {
		return nil
	}
	keywords, err := s.store.SearchCanonical(ctx, "", "")
	if err != nil {
		return err
	}
	for _, keyword := range keywords {
		forms := make([]core.CanonicalForm, 0, len(keyword.Forms))
		for _, form := range keyword.Forms {
			forms = append(forms, core.CanonicalForm{Value: form.Value})
		}
		if !matchesServiceWord(keyword.Canonical, forms, serviceWords[keyword.Language]) {
			continue
		}
		if err := s.store.DeleteCanonical(ctx, keyword.ID); err != nil {
			return err
		}
	}
	return nil
}

func (s *CanonicalService) List(ctx context.Context, class core.KeywordClass) ([]core.CanonicalKeyword, error) {
	return s.store.ListCanonical(ctx, class)
}

func (s *CanonicalService) Classify(ctx context.Context, id string, class core.KeywordClass) error {
	return s.store.ClassifyCanonical(ctx, id, class)
}

func (s *CanonicalService) SetTriggerActive(ctx context.Context, id string, active bool) error {
	return s.store.SetCanonicalTrigger(ctx, id, active)
}

func (s *CanonicalService) RemoveForm(ctx context.Context, id, form string) (core.CanonicalKeyword, error) {
	return s.store.RemoveCanonicalForm(ctx, id, form)
}

func (s *CanonicalService) MoveForm(ctx context.Context, targetID, form string) error {
	return s.store.MoveCanonicalForm(ctx, targetID, form)
}

func (s *CanonicalService) AddForm(ctx context.Context, targetID, form string) error {
	return s.store.AddCanonicalForm(ctx, targetID, form)
}

func (s *CanonicalService) Delete(ctx context.Context, id string) error {
	return s.store.DeleteCanonical(ctx, id)
}

func (s *CanonicalService) DeleteTrigger(ctx context.Context, id string) error {
	return s.store.DeleteCanonicalTrigger(ctx, id)
}

func (s *CanonicalService) Clear(ctx context.Context, class core.KeywordClass) error {
	return s.store.ClearCanonicalClass(ctx, class)
}

func (s *CanonicalService) Search(ctx context.Context, query string, class core.KeywordClass) ([]core.CanonicalKeyword, error) {
	return s.store.SearchCanonical(ctx, query, class)
}

func (s *CanonicalService) Metrics(ctx context.Context) (core.CanonicalMetrics, error) {
	return s.store.CanonicalMetrics(ctx)
}
