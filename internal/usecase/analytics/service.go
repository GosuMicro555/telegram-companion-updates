package analytics

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	core "telegram-companion/internal/analytics"
	analytictext "telegram-companion/internal/analytics/text"
)

const (
	ScopeAll   = "all"
	ScopeTopic = "topic"
)

type SourceRow struct {
	Text      string
	Topic     string
	SourceID  string
	MessageID string
	MessageAt time.Time
}

type AnalysisRequest struct {
	SourceScope  string
	ProfileID    string
	SourceTopics []string
}

type AnalysisResult struct {
	RunID             string
	Status            string
	GeneralPhrases    []core.Candidate
	GeneralWords      []core.Candidate
	ProfileCandidates []core.Candidate
}

type TopicComparisonRequest struct {
	SourceScope string
	ProfileID   string
	Topics      []string
}

type TopicSummary struct {
	Topic                  string
	Count                  int
	DistinctCandidateCount int
	TopPhrases             []core.Candidate
	RelativeShare          float64
}

type CandidateKey struct {
	NormalizedValue string
	Kind            string
}

type CandidateRef struct {
	RunID           string
	NormalizedValue string
	Kind            string
}

type StoredCandidate struct {
	RunID           string
	Candidate       core.Candidate
	ModerationState string
}

type Store interface {
	Rows(ctx context.Context, sourceScope string, topics []string) ([]SourceRow, error)
	Profile(ctx context.Context, profileID string) (*core.Profile, error)
	BeginRun(ctx context.Context, runID string, request AnalysisRequest) (RunTransaction, error)
	PreviousSuccessful(ctx context.Context, request AnalysisRequest) (AnalysisResult, error)
	Moderation(ctx context.Context) (map[CandidateKey]string, error)
	SaveModeration(ctx context.Context, key CandidateKey, state string) error
	ResetModeration(ctx context.Context, key CandidateKey) error
	VisibleCandidates(ctx context.Context, refs []CandidateRef) ([]StoredCandidate, error)
	Keywords(ctx context.Context) ([]string, error)
	AddKeyword(ctx context.Context, keyword string) error
}

type RunTransaction interface {
	SaveCandidate(ctx context.Context, candidate core.Candidate, moderationState string) error
	SaveAggregates(ctx context.Context, result AnalysisResult) error
	Commit(ctx context.Context) error
	Abort(ctx context.Context, status, message string) error
}

type KeywordExporter interface {
	Export(ctx context.Context, keywords []string) (string, error)
}

type Analyzer struct {
	store    Store
	exporter KeywordExporter
	newID    func() string
}

func NewAnalyzer(store Store, exporter KeywordExporter) *Analyzer {
	return NewAnalyzerWithIDGenerator(store, exporter, randomID)
}

func NewAnalyzerWithIDGenerator(store Store, exporter KeywordExporter, newID func() string) *Analyzer {
	return &Analyzer{store: store, exporter: exporter, newID: newID}
}

func (a *Analyzer) Analyze(ctx context.Context, request AnalysisRequest) (AnalysisResult, error) {
	if err := a.validate(request); err != nil {
		return AnalysisResult{}, err
	}
	runID := a.newID()
	tx, err := a.store.BeginRun(context.WithoutCancel(ctx), runID, request)
	if err != nil {
		return a.previous(ctx, request, err)
	}
	fail := func(cause error) (AnalysisResult, error) {
		status := "error"
		if errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
			status = "partial"
		}
		if abortErr := tx.Abort(context.WithoutCancel(ctx), status, cause.Error()); abortErr != nil {
			return AnalysisResult{}, errors.Join(cause, fmt.Errorf("persist %s run state: %w", status, abortErr))
		}
		return a.previous(context.WithoutCancel(ctx), request, cause)
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	rows, err := a.store.Rows(ctx, request.SourceScope, request.SourceTopics)
	if err != nil {
		return fail(err)
	}
	rows = filterRows(rows, request.SourceScope, request.SourceTopics)
	profile, err := a.profile(ctx, request.ProfileID)
	if err != nil {
		return fail(err)
	}
	moderation, err := a.store.Moderation(ctx)
	if err != nil {
		return fail(err)
	}
	ranked := core.Rank(documents(rows), profile)
	visible := make([]core.Candidate, 0, len(ranked))
	resetRejected := make([]CandidateKey, 0)
	for _, candidate := range ranked {
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		key := keyFor(candidate.NormalizedValue, candidate.Kind)
		state := moderation[key]
		if state == "rejected" {
			delete(moderation, key)
			resetRejected = append(resetRejected, key)
			state = ""
		}
		if state == "" {
			state = "new"
		}
		visible = append(visible, candidate)
		if err := tx.SaveCandidate(ctx, candidate, state); err != nil {
			return fail(err)
		}
	}
	selected := core.Select(visible)
	result := AnalysisResult{
		RunID: runID, Status: "complete", GeneralPhrases: selected.GeneralPhrases,
		GeneralWords: selected.GeneralWords, ProfileCandidates: selected.ProfileCandidates,
	}
	if err := tx.SaveAggregates(ctx, result); err != nil {
		return fail(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fail(err)
	}
	for _, key := range resetRejected {
		if err := a.store.ResetModeration(context.WithoutCancel(ctx), key); err != nil {
			return AnalysisResult{}, fmt.Errorf("reset moderation decision: %w", err)
		}
	}
	return result, nil
}

func (a *Analyzer) CompareTopics(ctx context.Context, request TopicComparisonRequest) ([]TopicSummary, error) {
	if a == nil || a.store == nil {
		return nil, errors.New("analytics store is required")
	}
	if len(request.Topics) == 0 {
		return nil, errors.New("at least one topic is required")
	}
	rows, err := a.store.Rows(ctx, ScopeAll, request.Topics)
	if err != nil {
		return nil, err
	}
	profile, err := a.profile(ctx, request.ProfileID)
	if err != nil {
		return nil, err
	}
	total := 0
	counts := make(map[string]int, len(request.Topics))
	for _, row := range rows {
		for _, topic := range request.Topics {
			if equalTopic(row.Topic, topic) {
				counts[topic]++
				total++
				break
			}
		}
	}
	result := make([]TopicSummary, 0, len(request.Topics))
	for _, topic := range request.Topics {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		topicRows := filterRows(rows, ScopeTopic, []string{topic})
		ranked := core.Rank(documents(topicRows), profile)
		selection := core.Select(ranked)
		top := selection.GeneralPhrases
		if len(top) > 10 {
			top = top[:10]
		}
		share := 0.0
		if total > 0 {
			share = float64(counts[topic]) / float64(total)
		}
		result = append(result, TopicSummary{Topic: topic, Count: counts[topic], DistinctCandidateCount: len(ranked), TopPhrases: top, RelativeShare: share})
	}
	return result, nil
}

func (a *Analyzer) ModerationStates(ctx context.Context) (map[CandidateKey]string, error) {
	if a == nil || a.store == nil {
		return nil, errors.New("analytics store is required")
	}
	states, err := a.store.Moderation(ctx)
	if err != nil {
		return nil, err
	}
	copyOfStates := make(map[CandidateKey]string, len(states))
	for key, state := range states {
		copyOfStates[key] = state
	}
	return copyOfStates, nil
}

func (a *Analyzer) Moderate(ctx context.Context, normalizedValue, kind, state string) error {
	if a == nil || a.store == nil {
		return errors.New("analytics store is required")
	}
	if state != "accepted" && state != "rejected" {
		return errors.New("moderation state must be accepted or rejected")
	}
	key := keyFor(normalizedValue, kind)
	if key.NormalizedValue == "" || (key.Kind != "word" && key.Kind != "phrase") {
		return errors.New("valid candidate key is required")
	}
	return a.store.SaveModeration(ctx, key, state)
}

func (a *Analyzer) ResetModeration(ctx context.Context, normalizedValue, kind string) error {
	if a == nil || a.store == nil {
		return errors.New("analytics store is required")
	}
	return a.store.ResetModeration(ctx, keyFor(normalizedValue, kind))
}

func (a *Analyzer) AddCandidatesToKeywords(ctx context.Context, refs []CandidateRef) (int, error) {
	if a == nil || a.store == nil {
		return 0, errors.New("analytics store is required")
	}
	candidates, err := a.store.VisibleCandidates(ctx, refs)
	if err != nil {
		return 0, err
	}
	keywords, err := a.store.Keywords(ctx)
	if err != nil {
		return 0, err
	}
	requested := make(map[CandidateRef]struct{}, len(refs))
	for _, ref := range refs {
		requested[normalizeRef(ref)] = struct{}{}
	}
	added := 0
	for _, stored := range candidates {
		ref := normalizeRef(CandidateRef{RunID: stored.RunID, NormalizedValue: stored.Candidate.NormalizedValue, Kind: stored.Candidate.Kind})
		if _, ok := requested[ref]; !ok {
			continue
		}
		if stored.ModerationState != "new" && stored.ModerationState != "accepted" {
			continue
		}
		value := strings.TrimSpace(stored.Candidate.DisplayValue)
		if value == "" {
			continue
		}
		if !containsFold(keywords, value) {
			if err := a.store.AddKeyword(ctx, value); err != nil {
				return added, err
			}
			keywords = append(keywords, value)
			added++
		}
		if err := a.store.SaveModeration(ctx, keyFor(stored.Candidate.NormalizedValue, stored.Candidate.Kind), "added_to_keywords"); err != nil {
			return added, err
		}
	}
	return added, nil
}

func (a *Analyzer) ExportKeywords(ctx context.Context) (string, error) {
	if a == nil || a.store == nil || a.exporter == nil {
		return "", errors.New("analytics store and keyword exporter are required")
	}
	keywords, err := a.store.Keywords(ctx)
	if err != nil {
		return "", err
	}
	keywords = uniqueKeywords(keywords)
	return a.exporter.Export(ctx, keywords)
}

func (a *Analyzer) validate(request AnalysisRequest) error {
	if a == nil || a.store == nil || a.newID == nil {
		return errors.New("analytics dependencies are required")
	}
	if request.SourceScope != ScopeAll && request.SourceScope != ScopeTopic {
		return fmt.Errorf("unsupported source scope %q", request.SourceScope)
	}
	if request.SourceScope == ScopeTopic && len(request.SourceTopics) == 0 {
		return errors.New("topic scope requires at least one topic")
	}
	return nil
}

func (a *Analyzer) profile(ctx context.Context, profileID string) (*core.Profile, error) {
	if profileID == "" {
		return core.DefaultMoneyShortageProfile(), nil
	}
	return a.store.Profile(ctx, profileID)
}

func (a *Analyzer) previous(ctx context.Context, request AnalysisRequest, cause error) (AnalysisResult, error) {
	previous, err := a.store.PreviousSuccessful(ctx, request)
	if err != nil {
		return AnalysisResult{}, errors.Join(cause, err)
	}
	return previous, cause
}

func documents(rows []SourceRow) []core.Document {
	result := make([]core.Document, len(rows))
	for i, row := range rows {
		result[i] = core.Document{Text: row.Text, Topic: row.Topic, SourceID: row.SourceID}
	}
	return result
}

func filterRows(rows []SourceRow, scope string, topics []string) []SourceRow {
	if scope != ScopeTopic {
		return append([]SourceRow(nil), rows...)
	}
	result := make([]SourceRow, 0, len(rows))
	for _, row := range rows {
		for _, topic := range topics {
			if equalTopic(row.Topic, topic) {
				result = append(result, row)
				break
			}
		}
	}
	return result
}

func equalTopic(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}

func keyFor(value, kind string) CandidateKey {
	return CandidateKey{NormalizedValue: analytictext.Normalize(value), Kind: strings.ToLower(strings.TrimSpace(kind))}
}

func normalizeRef(ref CandidateRef) CandidateRef {
	key := keyFor(ref.NormalizedValue, ref.Kind)
	return CandidateRef{RunID: strings.TrimSpace(ref.RunID), NormalizedValue: key.NormalizedValue, Kind: key.Kind}
}

func containsFold(values []string, wanted string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(wanted)) {
			return true
		}
	}
	return false
}

func uniqueKeywords(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !containsFold(result, value) {
			result = append(result, value)
		}
	}
	sort.Slice(result, func(i, j int) bool { return strings.ToLower(result[i]) < strings.ToLower(result[j]) })
	return result
}

func randomID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(value[:])
}
