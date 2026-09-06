package domain

import "time"

type SourceKind string

const (
	SourceKindLocalImport SourceKind = "local_import"
	SourceKindTelegram    SourceKind = "telegram"
)

const (
	ImportStatusComplete = "complete"
	ImportStatusError    = "error"
)

type ModelMetadata struct {
	Name            string `json:"name"`
	Repository      string `json:"repository"`
	Revision        string `json:"revision"`
	ModelSHA256     string `json:"model_sha256"`
	TokenizerSHA256 string `json:"tokenizer_sha256"`
}

type ImportThresholds struct {
	SemanticWeight float64 `json:"semantic_weight"`
	QualityWeight  float64 `json:"quality_weight"`
	MinimumScore   float64 `json:"minimum_score"`
}

type Import struct {
	ID                string
	FileName          string
	FilePath          string
	SHA256            string
	RightsConfirmed   bool
	RightsConfirmedAt time.Time
	ImportedAt        time.Time
	Status            string
	SourceKind        SourceKind
	ProfileID         string
	RecordCount       int
	Model             ModelMetadata
	Thresholds        ImportThresholds
	AppVersion        string
	LastError         string
}

type ImportCandidate struct {
	NormalizedValue string
	DisplayValue    string
	Frequency       int
	SemanticScore   float64
	QualityScore    float64
	Score           float64
}
