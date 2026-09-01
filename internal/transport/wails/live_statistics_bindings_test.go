package wails

import (
	"context"
	"encoding/csv"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	coreanalytics "telegram-companion/internal/analytics"
	"telegram-companion/internal/domain"
	"telegram-companion/internal/usecase"
)

type liveDeliveryStatisticsSettingsStub struct {
	*settingsStoreStub
	page  domain.LiveDeliveryPage
	pages map[int]domain.LiveDeliveryPage
	query domain.LiveDeliveryQuery
}

type liveStatisticsExportSelectorStub struct{ path string }

func (s liveStatisticsExportSelectorStub) SelectLiveStatisticsExportFile(context.Context, string, string) (string, error) {
	return s.path, nil
}

func TestGetLiveDeliveryStatisticsRebindsDeletedSnapshotToRecreatedActiveCanonical(t *testing.T) {
	store := &liveDeliveryStatisticsSettingsStub{
		settingsStoreStub: &settingsStoreStub{settings: domain.KeywordSettings{
			Keywords: []string{"додеп"}, CanonicalTriggerIDs: []string{"new-dodep"}, CanonicalTriggerValues: []string{"додеп"},
		}, appSettings: domain.DefaultAppSettings()},
		page: domain.LiveDeliveryPage{Rows: []domain.LiveDeliveryRow{
			{ID: "audit-canonical", TriggerSnapshot: "ДОДЕП"},
			{ID: "audit-form", TriggerSnapshot: "додепуля"},
			{ID: "audit-missing", TriggerSnapshot: "удалён навсегда"},
		}},
	}
	canonical := &canonicalServiceStub{rows: []coreanalytics.CanonicalKeyword{{
		ID: "new-dodep", Canonical: "додеп", Class: coreanalytics.ClassPositive, TriggerActive: true,
		Forms: []coreanalytics.CanonicalKeywordForm{{Value: "додепуля"}},
	}}}
	bindings := NewBindings(usecase.NewAutomationController(nil), store)
	bindings.ConfigureCanonicalAnalytics(canonical)

	page, err := bindings.GetLiveDeliveryStatistics(LiveDeliveryQueryDTO{Page: 1, PageSize: 50})

	require.NoError(t, err)
	require.NotNil(t, page.Rows[0].TriggerCanonicalID)
	require.Equal(t, "new-dodep", *page.Rows[0].TriggerCanonicalID)
	require.NotNil(t, page.Rows[1].TriggerCanonicalID)
	require.Equal(t, "new-dodep", *page.Rows[1].TriggerCanonicalID)
	require.Nil(t, page.Rows[2].TriggerCanonicalID)
}

func (s *liveDeliveryStatisticsSettingsStub) LiveDeliveryStatistics(_ context.Context, query domain.LiveDeliveryQuery) (domain.LiveDeliveryPage, error) {
	s.query = query
	if s.pages != nil {
		return s.pages[query.Offset], nil
	}
	return s.page, nil
}

func TestExportLiveDeliveryStatisticsWritesAllRowsInRequestedColumnOrder(t *testing.T) {
	triggeredAt := time.Date(2026, 7, 15, 7, 0, 0, 0, time.UTC)
	deliveryType := domain.JobType("public_reply")
	finalStatus := "successful"
	store := &liveDeliveryStatisticsSettingsStub{
		settingsStoreStub: &settingsStoreStub{settings: domain.DefaultKeywordSettings(), appSettings: domain.DefaultAppSettings()},
		pages: map[int]domain.LiveDeliveryPage{
			0:    {Rows: []domain.LiveDeliveryRow{{ID: "audit-1", SourceMessage: "Где взять денег", TriggerSnapshot: "деньги", TriggeredAt: triggeredAt, DeliveryType: &deliveryType, AccountTitleSnapshot: "Kiraa", FinalStatus: &finalStatus}}, Total: 1001},
			1000: {Rows: []domain.LiveDeliveryRow{{ID: "audit-2", SourceMessage: "тест", TriggerSnapshot: "тест", TriggeredAt: triggeredAt.Add(time.Minute), DeliveryType: &deliveryType, AccountTitleSnapshot: "Glossforge", FinalStatus: &finalStatus}}, Total: 1001},
		},
	}
	path := filepath.Join(t.TempDir(), "live-statistics.csv")
	bindings := NewBindings(usecase.NewAutomationController(nil), store)
	bindings.ConfigureLiveStatisticsExportSelector(liveStatisticsExportSelectorStub{path: path})

	exported, err := bindings.ExportLiveDeliveryStatistics(LiveDeliveryExportRequestDTO{
		From: triggeredAt.Add(-time.Hour).Format(time.RFC3339), To: triggeredAt.Add(time.Hour).Format(time.RFC3339),
		SortBy: "triggeredAt", SortDirection: "ascending", Columns: []string{"status", "sourceMessage", "date"}, Locale: "ru", TimeZone: "Europe/Moscow",
	})

	require.NoError(t, err)
	require.Equal(t, path, exported)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(string(data), "\ufeff"))
	rows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(string(data), "\ufeff"))).ReadAll()
	require.NoError(t, err)
	require.Equal(t, [][]string{
		{"Статус", "Исходное сообщение", "Дата"},
		{"Успешно", "Где взять денег", "15.07.2026"},
		{"Успешно", "тест", "15.07.2026"},
	}, rows)
}

func TestGetLiveDeliveryStatisticsConvertsLocalRangeAndDomainPage(t *testing.T) {
	triggerID := domain.ID("money")
	triggeredAt := time.Date(2026, 7, 15, 7, 0, 0, 0, time.UTC)
	finalizedAt := triggeredAt.Add(time.Second)
	deliveryType := domain.JobType("public_reply")
	finalStatus := "successful"
	store := &liveDeliveryStatisticsSettingsStub{
		settingsStoreStub: &settingsStoreStub{settings: domain.DefaultKeywordSettings(), appSettings: domain.DefaultAppSettings()},
		page: domain.LiveDeliveryPage{
			Rows: []domain.LiveDeliveryRow{{
				ID: "audit-1", SourceMessage: "where to get money", TriggerCanonicalID: &triggerID,
				TriggerSnapshot: "money", TriggeredAt: triggeredAt, DeliveryType: &deliveryType,
				AccountTitleSnapshot: "Operator", FinalStatus: &finalStatus, FinalizedAt: &finalizedAt,
			}},
			Total: 7, DatabaseBytes: 4097,
		},
	}
	bindings := NewBindings(usecase.NewAutomationController(nil), store)

	page, err := bindings.GetLiveDeliveryStatistics(LiveDeliveryQueryDTO{
		From: "2026-07-15T03:00:00-04:00", To: "2026-07-15T04:00:00-04:00",
		Page: 2, PageSize: 100, SortBy: "message", SortDirection: "ascending",
	})

	require.NoError(t, err)
	require.Equal(t, time.Date(2026, 7, 15, 7, 0, 0, 0, time.UTC), store.query.From)
	require.Equal(t, time.Date(2026, 7, 15, 8, 0, 0, 0, time.UTC), store.query.To)
	require.Equal(t, 100, store.query.Limit)
	require.Equal(t, 100, store.query.Offset)
	require.Equal(t, domain.LiveDeliverySortSourceMessage, store.query.SortBy)
	require.Equal(t, domain.LiveDeliverySortAscending, store.query.SortDirection)
	require.Equal(t, 7, page.Total)
	require.Equal(t, int64(4097), page.DatabaseBytes)
	require.Equal(t, "audit-1", page.Rows[0].ID)
	require.NotNil(t, page.Rows[0].TriggerCanonicalID)
	require.Equal(t, "money", *page.Rows[0].TriggerCanonicalID)
	require.Equal(t, "2026-07-15T07:00:00Z", page.Rows[0].TriggeredAt)
	require.NotEmpty(t, page.RefreshedAt)
}

func TestGetLiveDeliveryStatisticsRejectsUnsupportedPageSize(t *testing.T) {
	store := &liveDeliveryStatisticsSettingsStub{settingsStoreStub: &settingsStoreStub{settings: domain.DefaultKeywordSettings(), appSettings: domain.DefaultAppSettings()}}
	bindings := NewBindings(usecase.NewAutomationController(nil), store)

	_, err := bindings.GetLiveDeliveryStatistics(LiveDeliveryQueryDTO{Page: 1, PageSize: 51})

	require.EqualError(t, err, "live delivery statistics page size must be one of 50, 100, 200, 500, or 1000")
}

func TestGetLiveDeliveryStatisticsRejectsUnsupportedSort(t *testing.T) {
	store := &liveDeliveryStatisticsSettingsStub{settingsStoreStub: &settingsStoreStub{settings: domain.DefaultKeywordSettings(), appSettings: domain.DefaultAppSettings()}}
	bindings := NewBindings(usecase.NewAutomationController(nil), store)

	_, err := bindings.GetLiveDeliveryStatistics(LiveDeliveryQueryDTO{Page: 1, PageSize: 50, SortBy: "sourceMessage; DROP TABLE live_delivery_history", SortDirection: "ascending"})

	require.EqualError(t, err, "live delivery statistics sort column is unsupported")
}

func TestGetLiveDeliveryStatisticsRejectsSettingsWithoutProvider(t *testing.T) {
	bindings := NewBindings(usecase.NewAutomationController(nil), &settingsStoreStub{settings: domain.DefaultKeywordSettings(), appSettings: domain.DefaultAppSettings()})

	_, err := bindings.GetLiveDeliveryStatistics(LiveDeliveryQueryDTO{Page: 1, PageSize: 50})

	require.EqualError(t, err, "live delivery statistics are unavailable")
}
