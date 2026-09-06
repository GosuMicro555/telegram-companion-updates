package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	core "telegram-companion/internal/analytics"

	"github.com/stretchr/testify/require"
)

func TestCanonicalStorePersistsClassesFormsAndTriggerInvariant(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "canonical.db")
	db, err := Open(ctx, path)
	require.NoError(t, err)
	store := NewAnalyticsStore(db)
	seen := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	require.NoError(t, store.SyncCanonical(ctx, []core.CanonicalObservation{{
		Canonical: "деньги", Language: core.LanguageRU, TotalFrequency: 3, MessageCount: 2, LastSeen: seen,
		Forms: []core.FormObservation{
			{Form: "деньги", Frequency: 1, MessageKeys: []string{"1"}, LastSeen: seen},
			{Form: "денег", Frequency: 2, MessageKeys: []string{"1", "2"}, LastSeen: seen},
		},
	}}))
	neutral, err := store.ListCanonical(ctx, core.ClassNeutral)
	require.NoError(t, err)
	require.Len(t, neutral, 1)
	money := neutral[0]
	require.Equal(t, int64(3), money.TotalFrequency)
	require.Equal(t, int64(2), money.MessageCount)
	require.Len(t, money.Forms, 2)

	require.NoError(t, store.ClassifyCanonical(ctx, money.ID, core.ClassPositive))
	require.NoError(t, store.SetCanonicalTrigger(ctx, money.ID, true))
	require.NoError(t, store.ClassifyCanonical(ctx, money.ID, core.ClassNegative))
	negative, err := store.ListCanonical(ctx, core.ClassNegative)
	require.NoError(t, err)
	require.Len(t, negative, 1)
	require.False(t, negative[0].TriggerActive)

	detached, err := store.RemoveCanonicalForm(ctx, money.ID, "денег")
	require.NoError(t, err)
	require.Equal(t, core.ClassNeutral, detached.Class)
	require.Equal(t, "денег", detached.Canonical)
	require.NoError(t, store.MoveCanonicalForm(ctx, money.ID, "денег"))
	neutral, err = store.ListCanonical(ctx, core.ClassNeutral)
	require.NoError(t, err)
	require.Empty(t, neutral)

	require.NoError(t, db.Close())
	reopened, err := Open(ctx, path)
	require.NoError(t, err)
	defer reopened.Close()
	negative, err = NewAnalyticsStore(reopened).ListCanonical(ctx, core.ClassNegative)
	require.NoError(t, err)
	require.Len(t, negative, 1)
	require.Len(t, negative[0].Forms, 2)
}

func TestCanonicalStoreClearClassCascadesGloballyUniqueForms(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "canonical.db"))
	require.NoError(t, err)
	defer db.Close()
	store := NewAnalyticsStore(db)
	require.NoError(t, store.SyncCanonical(ctx, []core.CanonicalObservation{{
		Canonical: "run", Language: core.LanguageEN,
		Forms: []core.FormObservation{{Form: "run", Frequency: 1, MessageKeys: []string{"1"}}},
	}}))
	all, err := store.ListCanonical(ctx, core.ClassNeutral)
	require.NoError(t, err)
	require.Len(t, all, 1)
	require.NoError(t, store.ClearCanonicalClass(ctx, core.ClassNeutral))
	all, err = store.ListCanonical(ctx, core.ClassNeutral)
	require.NoError(t, err)
	require.Empty(t, all)
	var forms int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM canonical_keyword_forms`).Scan(&forms))
	require.Zero(t, forms)
}

func TestCanonicalStoreSearchesFormsAndKeepsPositiveKeywordWhenDeletingTrigger(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "canonical-search.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	store := NewAnalyticsStore(db)
	require.NoError(t, store.SyncCanonical(ctx, []core.CanonicalObservation{
		{Canonical: "money", Language: core.LanguageEN, TotalFrequency: 4, MessageCount: 2,
			Forms: []core.FormObservation{{Form: "money", Frequency: 4}}},
		{Canonical: "run", Language: core.LanguageEN, TotalFrequency: 3, MessageCount: 2,
			Forms: []core.FormObservation{{Form: "run", Frequency: 1}, {Form: "running", Frequency: 2}}},
	}))

	neutral, err := store.ListCanonical(ctx, core.ClassNeutral)
	require.NoError(t, err)
	require.Len(t, neutral, 2)
	var moneyID, runID string
	for _, word := range neutral {
		switch word.Canonical {
		case "money":
			moneyID = word.ID
		case "run":
			runID = word.ID
		}
	}
	require.NoError(t, store.ClassifyCanonical(ctx, moneyID, core.ClassPositive))
	require.NoError(t, store.SetCanonicalTrigger(ctx, moneyID, true))
	require.NoError(t, store.DeleteCanonicalTrigger(ctx, moneyID))
	positive, err := store.ListCanonical(ctx, core.ClassPositive)
	require.NoError(t, err)
	require.Len(t, positive, 1)
	require.Equal(t, moneyID, positive[0].ID)
	require.False(t, positive[0].TriggerActive)

	require.NoError(t, store.ClassifyCanonical(ctx, runID, core.ClassPositive))
	require.NoError(t, store.SetCanonicalTrigger(ctx, runID, true))
	require.NoError(t, store.ClassifyCanonical(ctx, runID, core.ClassService))
	service, err := store.SearchCanonical(ctx, "running", core.ClassService)
	require.NoError(t, err)
	require.Len(t, service, 1)
	require.Equal(t, "run", service[0].Canonical)
	require.False(t, service[0].TriggerActive)

	metrics, err := store.CanonicalMetrics(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(2), metrics.KeywordCount)
	require.Equal(t, int64(1), metrics.PositiveCount)
	require.Equal(t, int64(1), metrics.ServiceCount)
	require.Equal(t, int64(0), metrics.TriggerCount)
	require.Equal(t, int64(3), metrics.FormCount)
	require.Equal(t, int64(len("money")+len("run")+len("money")+len("run")+len("running")), metrics.LogicalBytes)
}

func TestCanonicalStoreSyncTracksFrequencyDeltaAcrossCollections(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "canonical-trend.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	store := NewAnalyticsStore(db)

	require.NoError(t, store.SyncCanonical(ctx, []core.CanonicalObservation{{
		Canonical: "money", Language: core.LanguageEN, TotalFrequency: 2, MessageCount: 1,
		Forms: []core.FormObservation{{Form: "money", Frequency: 2}},
	}}))
	money := requireCanonical(t, store, ctx, core.ClassNeutral, "money")
	require.Equal(t, int64(0), money.FrequencyDelta)

	require.NoError(t, store.SyncCanonical(ctx, []core.CanonicalObservation{{
		Canonical: "money", Language: core.LanguageEN, TotalFrequency: 5, MessageCount: 3,
		Forms: []core.FormObservation{{Form: "money", Frequency: 5}},
	}}))
	money = requireCanonical(t, store, ctx, core.ClassNeutral, "money")
	require.Equal(t, int64(3), money.FrequencyDelta)
	searchRows, err := store.SearchCanonical(ctx, "money", core.ClassNeutral)
	require.NoError(t, err)
	require.Len(t, searchRows, 1)
	require.Equal(t, int64(3), searchRows[0].FrequencyDelta)

	require.NoError(t, store.SyncCanonical(ctx, nil))
	money = requireCanonical(t, store, ctx, core.ClassNeutral, "money")
	require.Equal(t, int64(0), money.TotalFrequency)
	require.Equal(t, int64(0), money.MessageCount)
	require.Equal(t, int64(-5), money.FrequencyDelta)
}

func TestCanonicalStoreBulkImportUpsertsClassesWithoutEnablingTriggers(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "canonical-bulk-import.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	store := NewAnalyticsStore(db)
	require.NoError(t, store.SyncCanonical(ctx, []core.CanonicalObservation{{
		Canonical: "money", Language: core.LanguageEN,
		Forms: []core.FormObservation{{Form: "money", Frequency: 1}},
	}}))
	money := requireCanonical(t, store, ctx, core.ClassNeutral, "money")
	require.NoError(t, store.ClassifyCanonical(ctx, money.ID, core.ClassPositive))
	require.NoError(t, store.SetCanonicalTrigger(ctx, money.ID, true))

	result, err := store.BulkImportCanonical(ctx, []core.CanonicalImportValue{
		{Value: "money", Language: core.LanguageEN, Forms: []string{"monies"}},
		{Value: "скидка", Language: core.LanguageRU, Forms: []string{"скидочки", "скидочки"}},
	}, core.ClassPositive)

	require.NoError(t, err)
	require.Equal(t, core.CanonicalBulkImportResult{Added: 1, Updated: 1}, result)
	positive, err := store.ListCanonical(ctx, core.ClassPositive)
	require.NoError(t, err)
	require.Len(t, positive, 2)
	for _, keyword := range positive {
		switch keyword.Canonical {
		case "money":
			require.True(t, keyword.TriggerActive)
			require.Equal(t, []core.CanonicalKeywordForm{{Value: "money", DisplayValue: "money", Frequency: 1}, {Value: "monies", DisplayValue: "monies", ManualOverride: true}}, keyword.Forms)
		case "скидка":
			require.False(t, keyword.TriggerActive)
			require.Equal(t, []core.CanonicalKeywordForm{{Value: "скидочки", DisplayValue: "скидочки", ManualOverride: true}}, keyword.Forms)
		}
	}

	reimported, err := store.BulkImportCanonical(ctx, []core.CanonicalImportValue{{
		Value: "скидка", Language: core.LanguageRU, Forms: []string{"скидочки"},
	}}, core.ClassPositive)
	require.NoError(t, err)
	require.Equal(t, core.CanonicalBulkImportResult{Updated: 1}, reimported)
	positive, err = store.ListCanonical(ctx, core.ClassPositive)
	require.NoError(t, err)
	for _, keyword := range positive {
		if keyword.Canonical == "скидка" {
			require.Equal(t, []core.CanonicalKeywordForm{{Value: "скидочки", DisplayValue: "скидочки", ManualOverride: true}}, keyword.Forms)
		}
	}
}

func TestCanonicalStoreKeepsDetachedFormIndependentOnLaterSync(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "canonical-detach.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	store := NewAnalyticsStore(db)
	observation := core.CanonicalObservation{
		Canonical: "деньги", Language: core.LanguageRU,
		Forms: []core.FormObservation{{Form: "деньги", Frequency: 1}, {Form: "денег", Frequency: 1}},
	}
	require.NoError(t, store.SyncCanonical(ctx, []core.CanonicalObservation{observation}))
	money := requireCanonical(t, store, ctx, core.ClassNeutral, "деньги")
	_, err = store.RemoveCanonicalForm(ctx, money.ID, "денег")
	require.NoError(t, err)

	require.NoError(t, store.SyncCanonical(ctx, []core.CanonicalObservation{observation}))
	money = requireCanonical(t, store, ctx, core.ClassNeutral, "деньги")
	detached := requireCanonical(t, store, ctx, core.ClassNeutral, "денег")
	require.ElementsMatch(t, []string{"деньги"}, canonicalKeywordFormValues(money.Forms))
	require.ElementsMatch(t, []string{"денег"}, canonicalKeywordFormValues(detached.Forms))
	require.True(t, detached.Forms[0].ManualOverride)
}

func requireCanonical(t *testing.T, store *AnalyticsStore, ctx context.Context, class core.KeywordClass, canonical string) core.CanonicalKeyword {
	t.Helper()
	rows, err := store.ListCanonical(ctx, class)
	require.NoError(t, err)
	for _, row := range rows {
		if row.Canonical == canonical {
			return row
		}
	}
	require.Failf(t, "canonical keyword not found", "canonical %q class %q", canonical, class)
	return core.CanonicalKeyword{}
}

func canonicalKeywordFormValues(forms []core.CanonicalKeywordForm) []string {
	values := make([]string, len(forms))
	for index, form := range forms {
		values[index] = form.Value
	}
	return values
}
