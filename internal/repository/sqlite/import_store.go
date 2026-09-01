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

	core "telegram-companion/internal/analytics"
	"telegram-companion/internal/domain"
	usecaseimports "telegram-companion/internal/usecase/imports"
)

var ErrDuplicateImport = errors.New("an import with this SHA-256 already exists")

type ImportStore struct{ db *sql.DB }

func NewImportStore(db *sql.DB) *ImportStore { return &ImportStore{db: db} }

func (s *ImportStore) FindBySHA256(ctx context.Context, digest string) (*domain.Import, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	var imported domain.Import
	var rights int
	var importedAt string
	var metadataRaw string
	err := s.db.QueryRowContext(ctx, `SELECT
		i.id, i.file_name, i.file_path, i.sha256, i.rights_confirmed, i.imported_at,
		i.status, i.model_name, i.model_sha256, i.tokenizer_sha256, i.last_error,
		COALESCE(r.profile_id, ''), COALESCE(r.input_count, 0), COALESCE(r.model_metadata_json, '{}')
		FROM imports i LEFT JOIN analysis_runs r ON r.id = i.id WHERE i.sha256 = ?`, strings.ToLower(digest)).
		Scan(&imported.ID, &imported.FileName, &imported.FilePath, &imported.SHA256, &rights, &importedAt,
			&imported.Status, &imported.Model.Name, &imported.Model.ModelSHA256, &imported.Model.TokenizerSHA256,
			&imported.LastError, &imported.ProfileID, &imported.RecordCount, &metadataRaw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find import by SHA-256: %w", err)
	}
	when, err := parseTime(importedAt)
	if err != nil {
		return nil, fmt.Errorf("parse import rights timestamp: %w", err)
	}
	imported.RightsConfirmed = rights == 1
	imported.RightsConfirmedAt = when
	imported.ImportedAt = when
	imported.SourceKind = domain.SourceKindLocalImport
	var metadata importRunMetadata
	if err := json.Unmarshal([]byte(metadataRaw), &metadata); err != nil {
		return nil, fmt.Errorf("decode import model metadata: %w", err)
	}
	imported.Model.Repository = metadata.Repository
	imported.Model.Revision = metadata.Revision
	if imported.ProfileID == "" {
		imported.ProfileID = metadata.ProfileID
	}
	imported.Thresholds = metadata.Thresholds
	imported.AppVersion = metadata.AppVersion
	return &imported, nil
}

func (s *ImportStore) Profile(ctx context.Context, profileID string) (*core.Profile, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	return NewAnalyticsStore(s.db).Profile(ctx, profileID)
}

func (s *ImportStore) Save(ctx context.Context, imported domain.Import, candidates []domain.ImportCandidate) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := validateImport(imported, candidates); err != nil {
		return err
	}
	existing, err := s.FindBySHA256(ctx, imported.SHA256)
	if err != nil {
		return err
	}
	if existing != nil {
		return ErrDuplicateImport
	}
	metadata := importRunMetadata{
		Name: imported.Model.Name, Repository: imported.Model.Repository, Revision: imported.Model.Revision,
		ModelSHA256: imported.Model.ModelSHA256, TokenizerSHA256: imported.Model.TokenizerSHA256,
		Thresholds: imported.Thresholds, AppVersion: imported.AppVersion, ImportSHA256: imported.SHA256,
		RightsConfirmed: imported.RightsConfirmed, RightsConfirmedAt: formatTime(imported.RightsConfirmedAt), SourceKind: imported.SourceKind,
		ProfileID: imported.ProfileID,
	}
	metadataRaw, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("encode import model metadata: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin import save: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `INSERT INTO imports
		(id, file_name, file_path, sha256, rights_confirmed, imported_at, status,
		 model_name, model_sha256, tokenizer_sha256, last_error)
		VALUES (?, ?, ?, ?, 1, ?, 'complete', ?, ?, ?, '')`, imported.ID, imported.FileName, imported.FilePath,
		strings.ToLower(imported.SHA256), formatTime(imported.RightsConfirmedAt), imported.Model.Name,
		strings.ToLower(imported.Model.ModelSHA256), strings.ToLower(imported.Model.TokenizerSHA256))
	if err != nil {
		if strings.Contains(err.Error(), "imports.sha256") {
			return ErrDuplicateImport
		}
		return fmt.Errorf("insert import: %w", err)
	}
	var analysisProfileID any = imported.ProfileID
	if imported.ProfileID == "money-shortage" {
		analysisProfileID = nil
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO analysis_runs
		(id, source_scope, profile_id, status, progress, input_count, candidate_count,
		 model_metadata_json, started_at, completed_at, error)
		VALUES (?, 'all', ?, 'complete', 100, ?, ?, ?, ?, ?, '')`, imported.ID, analysisProfileID,
		imported.RecordCount, len(candidates), string(metadataRaw), formatTime(imported.ImportedAt), formatTime(imported.ImportedAt))
	if err != nil {
		return fmt.Errorf("insert import analysis run: %w", err)
	}
	for _, candidate := range candidates {
		_, err = tx.ExecContext(ctx, `INSERT INTO keyword_candidates
			(id, run_id, normalized_value, display_value, kind, frequency, source_diversity,
			 score, source, moderation_state, created_at)
			VALUES (?, ?, ?, ?, 'phrase', ?, 1, ?, 'import_ai', 'new', ?)`, importCandidateID(imported.ID, candidate),
			imported.ID, candidate.NormalizedValue, candidate.DisplayValue, candidate.Frequency, candidate.Score, formatTime(imported.ImportedAt))
		if err != nil {
			return fmt.Errorf("insert import candidate: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit import: %w", err)
	}
	return nil
}

type importRunMetadata struct {
	Name              string                  `json:"name"`
	Repository        string                  `json:"repository"`
	Revision          string                  `json:"revision"`
	ModelSHA256       string                  `json:"model_sha256"`
	TokenizerSHA256   string                  `json:"tokenizer_sha256"`
	Thresholds        domain.ImportThresholds `json:"thresholds"`
	AppVersion        string                  `json:"app_version"`
	ImportSHA256      string                  `json:"import_sha256"`
	RightsConfirmed   bool                    `json:"rights_confirmed"`
	RightsConfirmedAt string                  `json:"rights_confirmed_at"`
	SourceKind        domain.SourceKind       `json:"source_kind"`
	ProfileID         string                  `json:"profile_id"`
}

func validateImport(imported domain.Import, candidates []domain.ImportCandidate) error {
	if strings.TrimSpace(imported.ID) == "" {
		return errors.New("import ID is required")
	}
	if imported.SourceKind != domain.SourceKindLocalImport {
		return errors.New("only local imports may be persisted for AI analysis")
	}
	if !imported.RightsConfirmed || imported.RightsConfirmedAt.IsZero() {
		return errors.New("rights confirmation and timestamp are required")
	}
	if !isSHA256(imported.SHA256) || !isSHA256(imported.Model.ModelSHA256) || !isSHA256(imported.Model.TokenizerSHA256) {
		return errors.New("import and model SHA-256 values are required")
	}
	if imported.Model.Revision == "" || imported.Model.Repository == "" || imported.AppVersion == "" {
		return errors.New("model revision, repository, and app version are required")
	}
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate.NormalizedValue) == "" || strings.TrimSpace(candidate.DisplayValue) == "" || candidate.Frequency <= 0 {
			return errors.New("valid import candidates are required")
		}
	}
	return nil
}

func isSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func importCandidateID(runID string, candidate domain.ImportCandidate) string {
	sum := sha256.Sum256([]byte(runID + "\x00" + candidate.NormalizedValue + "\x00phrase\x00import_ai"))
	return hex.EncodeToString(sum[:])
}

func (s *ImportStore) ready() error {
	if s == nil || s.db == nil {
		return errors.New("import database is required")
	}
	return nil
}

var _ usecaseimports.Store = (*ImportStore)(nil)
