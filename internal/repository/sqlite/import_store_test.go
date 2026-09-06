package sqlite_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"telegram-companion/internal/domain"
	"telegram-companion/internal/repository/sqlite"

	"github.com/stretchr/testify/require"
)

func TestImportStorePersistsSHARightsAndReproducibilityMetadata(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "imports.sqlite")
	db, err := sqlite.Open(ctx, path)
	require.NoError(t, err)
	store := sqlite.NewImportStore(db)
	now := time.Date(2026, 7, 11, 10, 15, 0, 123, time.UTC)
	seedProfile(t, db, "profile-1", now)
	imported := sampleImport(now)
	candidates := []domain.ImportCandidate{{NormalizedValue: "нет денег", DisplayValue: "Нет денег", Frequency: 3, SemanticScore: 0.9, QualityScore: 0.4, Score: 0.8}}

	require.NoError(t, store.Save(ctx, imported, candidates))
	require.NoError(t, db.Close())

	db, err = sqlite.Open(ctx, path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	store = sqlite.NewImportStore(db)
	got, err := store.FindBySHA256(ctx, imported.SHA256)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, imported.SHA256, got.SHA256)
	require.True(t, got.RightsConfirmed)
	require.Equal(t, now, got.RightsConfirmedAt)
	require.Equal(t, imported.Model, got.Model)

	var raw string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT model_metadata_json FROM analysis_runs WHERE id = ?`, imported.ID).Scan(&raw))
	var metadata map[string]any
	require.NoError(t, json.Unmarshal([]byte(raw), &metadata))
	require.Equal(t, "614241f622f53c4eeff9890bdc4f31cfecc418b3", metadata["revision"])
	require.Equal(t, imported.Model.ModelSHA256, metadata["model_sha256"])
	require.Equal(t, imported.Model.TokenizerSHA256, metadata["tokenizer_sha256"])
	require.Equal(t, imported.SHA256, metadata["import_sha256"])
	require.Equal(t, true, metadata["rights_confirmed"])
	require.Equal(t, now.Format(time.RFC3339Nano), metadata["rights_confirmed_at"])
	require.Equal(t, "v-test", metadata["app_version"])

	var source string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT source FROM keyword_candidates WHERE run_id = ?`, imported.ID).Scan(&source))
	require.Equal(t, "import_ai", source)
}

func TestImportStoreDeduplicatesSHAAndRollsBackInvalidCandidate(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "imports.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	store := sqlite.NewImportStore(db)
	now := time.Date(2026, 7, 11, 10, 15, 0, 0, time.UTC)
	seedProfile(t, db, "profile-1", now)
	imported := sampleImport(now)

	err = store.Save(ctx, imported, []domain.ImportCandidate{{NormalizedValue: "", DisplayValue: "bad"}})
	require.Error(t, err)
	var count int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM imports`).Scan(&count))
	require.Zero(t, count)

	require.NoError(t, store.Save(ctx, imported, nil))
	duplicate := imported
	duplicate.ID = "other-id"
	err = store.Save(ctx, duplicate, nil)
	require.ErrorIs(t, err, sqlite.ErrDuplicateImport)
}

func TestImportStorePersistsBuiltInProfileWithoutForeignKeyRow(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "imports.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	store := sqlite.NewImportStore(db)
	imported := sampleImport(time.Date(2026, 7, 11, 10, 15, 0, 0, time.UTC))
	imported.ProfileID = "money-shortage"

	require.NoError(t, store.Save(ctx, imported, nil))
	got, err := store.FindBySHA256(ctx, imported.SHA256)
	require.NoError(t, err)
	require.Equal(t, "money-shortage", got.ProfileID)
}

func sampleImport(now time.Time) domain.Import {
	return domain.Import{
		ID: "import-1", FileName: "records.json", FilePath: "/tmp/records.json", SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		RightsConfirmed: true, RightsConfirmedAt: now, ImportedAt: now, Status: domain.ImportStatusComplete,
		SourceKind: domain.SourceKindLocalImport, ProfileID: "profile-1", RecordCount: 3,
		Model:      domain.ModelMetadata{Name: "multilingual-e5-small", Repository: "intfloat/multilingual-e5-small", Revision: "614241f622f53c4eeff9890bdc4f31cfecc418b3", ModelSHA256: "ca456c06b3a9505ddfd9131408916dd79290368331e7d76bb621f1cba6bc8665", TokenizerSHA256: "0b44a9d7b51c3c62626640cda0e2c2f70fdacdc25bbbd68038369d14ebdf4c39"},
		Thresholds: domain.ImportThresholds{SemanticWeight: 0.8, QualityWeight: 0.2, MinimumScore: 0.1}, AppVersion: "v-test",
	}
}

func seedProfile(t *testing.T, db *sql.DB, id string, now time.Time) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO analysis_profiles
		(id, name, positive_examples_json, exclusions_json, created_at, updated_at)
		VALUES (?, ?, '["example"]', '[]', ?, ?)`, id, id, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	require.NoError(t, err)
}
