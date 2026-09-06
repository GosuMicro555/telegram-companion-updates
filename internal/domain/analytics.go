package domain

import (
	"context"
	"time"
)

type AnalyticsLanguage string

const (
	AnalyticsLanguageRU AnalyticsLanguage = "ru"
	AnalyticsLanguageEN AnalyticsLanguage = "en"
)

type AnalyticsSettings struct {
	Enabled         bool
	IntervalMinutes int
}

func DefaultAnalyticsSettings() AnalyticsSettings {
	return AnalyticsSettings{Enabled: true, IntervalMinutes: 10}
}

func (s AnalyticsSettings) Valid() bool {
	switch s.IntervalMinutes {
	case 1, 5, 10, 30, 60, 120:
		return true
	default:
		return false
	}
}

type AnalyticsScoutCursor struct {
	AccountID ID
	ChatID    string
	MessageID int64
	UpdatedAt time.Time
}

type AnalyticsRunMetrics struct {
	NewMessages     int64
	ExtractedWords  int64
	NewCanonicals   int64
	ProcessedGroups int64
	Duration        time.Duration
	Errors          []string
}

type AnalyticsSchedulerRun struct {
	ID         string
	StartedAt  time.Time
	FinishedAt *time.Time
	Metrics    AnalyticsRunMetrics
}

type AnalyticsServiceWord struct {
	Language AnalyticsLanguage
	Value    string
}

type AnalyticsSortDirection string

const (
	AnalyticsSortNone       AnalyticsSortDirection = "none"
	AnalyticsSortAscending  AnalyticsSortDirection = "ascending"
	AnalyticsSortDescending AnalyticsSortDirection = "descending"
)

type AnalyticsTableColumnPreference struct {
	Key     string
	Width   int
	Visible bool
}

type AnalyticsTablePreference struct {
	Tab           string
	Columns       []AnalyticsTableColumnPreference
	SortBy        string
	SortDirection AnalyticsSortDirection
	PageSize      int
}

type AnalyticsMetrics struct {
	LastRun             AnalyticsRunMetrics
	CanonicalCount      int64
	FormCount           int64
	MessageCount        int64
	GroupCount          int64
	LogicalKeywordBytes int64
}

// AnalyticsSchedulerStore is the small persistence surface used by the
// periodic analytics worker. Its records intentionally avoid transport types.
type AnalyticsSchedulerStore interface {
	AnalyticsSettings(context.Context) (AnalyticsSettings, error)
	SaveAnalyticsSettings(context.Context, AnalyticsSettings) error
	ScoutCursor(ctx context.Context, accountID ID, chatID string) (AnalyticsScoutCursor, error)
	SaveScoutCursor(context.Context, AnalyticsScoutCursor) error
	AppendAnalyticsRun(context.Context, AnalyticsSchedulerRun) error
	AnalyticsRunHistory(ctx context.Context, limit int) ([]AnalyticsSchedulerRun, error)
	AnalyticsMetrics(context.Context) (AnalyticsMetrics, error)
	ServiceWords(context.Context, AnalyticsLanguage) ([]string, error)
	UpsertServiceWord(context.Context, AnalyticsServiceWord) error
	DeleteServiceWord(ctx context.Context, language AnalyticsLanguage, value string) error
	TablePreference(context.Context, string) (AnalyticsTablePreference, error)
	SaveTablePreference(ctx context.Context, preference AnalyticsTablePreference) error
}

type SourceCatalog string

const (
	SourceCatalogOutbound SourceCatalog = "outbound"
	SourceCatalogScout    SourceCatalog = "scout"
)

type ScoutMessage struct {
	ChatTelegramID string
	MessageID      int64
	Text           string
	MessageAt      time.Time
	ReceivedAt     time.Time
	EditedAt       *time.Time
}

type AnalysisProfile struct {
	ID               ID
	Name             string
	Description      string
	PositiveExamples []string
	Exclusions       []string
	Enabled          bool
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type AnalysisRun struct {
	ID             ID
	ProfileID      *ID
	Topic          string
	Status         string
	Progress       int
	InputCount     int
	CandidateCount int
	StartedAt      time.Time
	CompletedAt    *time.Time
	Error          string
}

type KeywordCandidate struct {
	ID              ID
	RunID           ID
	NormalizedValue string
	DisplayValue    string
	Kind            string
	Frequency       int
	SourceDiversity int
	Score           float64
	Source          string
	ModerationState string
	CreatedAt       time.Time
}

type ModerationDecision struct {
	NormalizedValue string
	Kind            string
	State           string
	DecidedAt       time.Time
}
