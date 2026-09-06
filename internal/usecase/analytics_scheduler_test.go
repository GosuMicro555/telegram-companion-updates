package usecase

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"telegram-companion/internal/domain"
	analyticsusecase "telegram-companion/internal/usecase/analytics"
)

type analyticsSchedulerStoreFake struct {
	mu       sync.Mutex
	settings domain.AnalyticsSettings
	runs     []domain.AnalyticsSchedulerRun
	metrics  domain.AnalyticsMetrics
}

func (f *analyticsSchedulerStoreFake) AnalyticsSettings(context.Context) (domain.AnalyticsSettings, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.settings, nil
}

func (f *analyticsSchedulerStoreFake) SaveAnalyticsSettings(_ context.Context, settings domain.AnalyticsSettings) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.settings = settings
	return nil
}

func (f *analyticsSchedulerStoreFake) ScoutCursor(context.Context, domain.ID, string) (domain.AnalyticsScoutCursor, error) {
	return domain.AnalyticsScoutCursor{}, nil
}

func (f *analyticsSchedulerStoreFake) SaveScoutCursor(context.Context, domain.AnalyticsScoutCursor) error {
	return nil
}

func (f *analyticsSchedulerStoreFake) AppendAnalyticsRun(_ context.Context, run domain.AnalyticsSchedulerRun) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runs = append(f.runs, run)
	return nil
}

func (f *analyticsSchedulerStoreFake) AnalyticsRunHistory(_ context.Context, limit int) ([]domain.AnalyticsSchedulerRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if limit > len(f.runs) {
		limit = len(f.runs)
	}
	result := append([]domain.AnalyticsSchedulerRun(nil), f.runs[len(f.runs)-limit:]...)
	for left, right := 0, len(result)-1; left < right; left, right = left+1, right-1 {
		result[left], result[right] = result[right], result[left]
	}
	return result, nil
}

func (f *analyticsSchedulerStoreFake) AnalyticsMetrics(context.Context) (domain.AnalyticsMetrics, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.metrics, nil
}
func (f *analyticsSchedulerStoreFake) ServiceWords(context.Context, domain.AnalyticsLanguage) ([]string, error) {
	return nil, nil
}
func (f *analyticsSchedulerStoreFake) UpsertServiceWord(context.Context, domain.AnalyticsServiceWord) error {
	return nil
}
func (f *analyticsSchedulerStoreFake) DeleteServiceWord(context.Context, domain.AnalyticsLanguage, string) error {
	return nil
}
func (f *analyticsSchedulerStoreFake) TablePreference(context.Context, string) (domain.AnalyticsTablePreference, error) {
	return domain.AnalyticsTablePreference{}, nil
}
func (f *analyticsSchedulerStoreFake) SaveTablePreference(context.Context, domain.AnalyticsTablePreference) error {
	return nil
}

type analyticsHistorySyncerFake struct {
	started chan struct{}
	release chan struct{}
	active  atomic.Int32
	maximum atomic.Int32
	calls   atomic.Int32
	result  AnalyticsHistoryResult
	err     error
}

func (f *analyticsHistorySyncerFake) SyncAnalyticsHistory(ctx context.Context) (AnalyticsHistoryResult, error) {
	f.calls.Add(1)
	active := f.active.Add(1)
	defer f.active.Add(-1)
	for {
		maximum := f.maximum.Load()
		if active <= maximum || f.maximum.CompareAndSwap(maximum, active) {
			break
		}
	}
	if f.started != nil {
		select {
		case f.started <- struct{}{}:
		default:
		}
	}
	if f.release != nil {
		select {
		case <-ctx.Done():
			return AnalyticsHistoryResult{}, ctx.Err()
		case <-f.release:
		}
	}
	return f.result, f.err
}

type canonicalSchedulerFake struct {
	result analyticsusecase.CanonicalAnalysisResult
	err    error
	calls  atomic.Int32
}

func (f *canonicalSchedulerFake) Analyze(context.Context) (analyticsusecase.CanonicalAnalysisResult, error) {
	f.calls.Add(1)
	return f.result, f.err
}

func noopCanonicalTriggerSync() error { return nil }

func TestAnalyticsSchedulerStartRunsPromptlyAndRecordsLiveMetrics(t *testing.T) {
	store := &analyticsSchedulerStoreFake{settings: domain.DefaultAnalyticsSettings(), metrics: domain.AnalyticsMetrics{MessageCount: 17, CanonicalCount: 7, GroupCount: 3}}
	history := &analyticsHistorySyncerFake{result: AnalyticsHistoryResult{ChatsScanned: 3, MessagesScanned: 17}}
	canonical := &canonicalSchedulerFake{result: analyticsusecase.CanonicalAnalysisResult{MessageCount: 19, KeywordCount: 7}}
	var syncCalls atomic.Int32
	scheduler := NewAnalyticsScheduler(store, history, canonical, func() error {
		syncCalls.Add(1)
		return nil
	}, time.Now)

	require.NoError(t, scheduler.Start(context.Background(), 5))
	require.Eventually(t, func() bool {
		status, err := scheduler.Status(context.Background())
		store.mu.Lock()
		runCount := len(store.runs)
		store.mu.Unlock()
		return err == nil && history.calls.Load() == 1 && syncCalls.Load() == 1 && runCount == 1 && status.AllTimeMessages == 17 && status.AllTimeKeywords == 7
	}, time.Second, time.Millisecond)

	status, err := scheduler.Status(context.Background())
	require.NoError(t, err)
	require.True(t, status.Enabled)
	require.Equal(t, 5, status.IntervalMinutes)
	require.Equal(t, int64(3), status.AllTimeGroups)
	require.NotNil(t, status.NextRunAt)
	require.Equal(t, domain.AnalyticsSettings{Enabled: true, IntervalMinutes: 5}, store.settings)
	require.NoError(t, scheduler.Shutdown(context.Background()))
}

func TestAnalyticsSchedulerRejectsInvalidIntervals(t *testing.T) {
	scheduler := NewAnalyticsScheduler(&analyticsSchedulerStoreFake{}, &analyticsHistorySyncerFake{}, &canonicalSchedulerFake{}, noopCanonicalTriggerSync, time.Now)
	for _, interval := range []int{-1, 0, 2, 15, 121} {
		require.ErrorContains(t, scheduler.Start(context.Background(), interval), "1, 5, 10, 30, 60, or 120")
	}
}

func TestAnalyticsSchedulerPreventsManualAndScheduledOverlap(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	history := &analyticsHistorySyncerFake{started: started, release: release}
	scheduler := NewAnalyticsScheduler(&analyticsSchedulerStoreFake{}, history, &canonicalSchedulerFake{}, noopCanonicalTriggerSync, time.Now)

	require.NoError(t, scheduler.Start(context.Background(), 10))
	<-started
	_, err := scheduler.RunNow(context.Background())
	require.ErrorIs(t, err, ErrAnalyticsCollectionRunning)
	close(release)
	require.Eventually(t, func() bool {
		status, statusErr := scheduler.Status(context.Background())
		return statusErr == nil && !status.Running
	}, time.Second, time.Millisecond)
	require.Equal(t, int32(1), history.maximum.Load())
	require.NoError(t, scheduler.Shutdown(context.Background()))
}

func TestAnalyticsSchedulerStopCancelsFutureTimersButLetsActiveCycleFinish(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	history := &analyticsHistorySyncerFake{started: started, release: release}
	store := &analyticsSchedulerStoreFake{}
	scheduler := NewAnalyticsScheduler(store, history, &canonicalSchedulerFake{}, noopCanonicalTriggerSync, time.Now)

	require.NoError(t, scheduler.Start(context.Background(), 1))
	<-started
	require.NoError(t, scheduler.Stop(context.Background()))
	status, err := scheduler.Status(context.Background())
	require.NoError(t, err)
	require.False(t, status.Enabled)
	require.True(t, status.Running)
	require.Nil(t, status.NextRunAt)
	require.Equal(t, domain.AnalyticsSettings{Enabled: false, IntervalMinutes: 1}, store.settings)

	close(release)
	require.Eventually(t, func() bool {
		status, statusErr := scheduler.Status(context.Background())
		return statusErr == nil && !status.Running
	}, time.Second, time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	require.Equal(t, int32(1), history.calls.Load())
}

func TestAnalyticsSchedulerRecordsCycleErrorsAndSkipsCanonicalSynchronization(t *testing.T) {
	want := errors.New("chat failed")
	store := &analyticsSchedulerStoreFake{}
	history := &analyticsHistorySyncerFake{result: AnalyticsHistoryResult{ChatsScanned: 2, MessagesScanned: 4}, err: want}
	canonical := &canonicalSchedulerFake{}
	scheduler := NewAnalyticsScheduler(store, history, canonical, noopCanonicalTriggerSync, time.Now)

	run, err := scheduler.RunNow(context.Background())
	require.ErrorIs(t, err, want)
	require.Equal(t, []string{"chat failed"}, run.Metrics.Errors)
	require.Equal(t, int64(2), run.Metrics.ProcessedGroups)
	require.Equal(t, int64(4), run.Metrics.NewMessages)
	require.Zero(t, canonical.calls.Load())
	historyRows, historyErr := scheduler.History(context.Background(), 10)
	require.NoError(t, historyErr)
	require.Len(t, historyRows, 1)
}

func TestAnalyticsSchedulerAnalyzesCanonicalAfterPartialChatFailure(t *testing.T) {
	partialFailure := "account scout-a chat 100: durable collect failed"
	store := &analyticsSchedulerStoreFake{}
	history := &analyticsHistorySyncerFake{result: AnalyticsHistoryResult{
		ChatsScanned:    2,
		MessagesScanned: 4,
		Errors:          []string{partialFailure},
	}}
	canonical := &canonicalSchedulerFake{result: analyticsusecase.CanonicalAnalysisResult{
		MessageCount: 4,
		KeywordCount: 2,
	}}
	scheduler := NewAnalyticsScheduler(store, history, canonical, noopCanonicalTriggerSync, time.Now)

	run, err := scheduler.RunNow(context.Background())

	require.NoError(t, err)
	require.Equal(t, int32(1), canonical.calls.Load())
	require.Equal(t, int64(2), run.Metrics.ProcessedGroups)
	require.Equal(t, []string{partialFailure}, run.Metrics.Errors)
	historyRows, historyErr := scheduler.History(context.Background(), 10)
	require.NoError(t, historyErr)
	require.Len(t, historyRows, 1)
	require.Equal(t, []string{partialFailure}, historyRows[0].Metrics.Errors)
}

func TestAnalyticsSchedulerRecordsCanonicalTriggerSynchronizationFailure(t *testing.T) {
	want := errors.New("canonical trigger synchronization failed")
	store := &analyticsSchedulerStoreFake{}
	canonical := &canonicalSchedulerFake{}
	var syncCalls atomic.Int32
	scheduler := NewAnalyticsScheduler(store, &analyticsHistorySyncerFake{}, canonical, func() error {
		require.Equal(t, int32(1), canonical.calls.Load(), "trigger synchronization must follow canonical analysis")
		syncCalls.Add(1)
		return want
	}, time.Now)

	run, err := scheduler.RunNow(context.Background())

	require.ErrorIs(t, err, want)
	require.Equal(t, int32(1), syncCalls.Load())
	require.Equal(t, []string{want.Error()}, run.Metrics.Errors)
	historyRows, historyErr := scheduler.History(context.Background(), 10)
	require.NoError(t, historyErr)
	require.Len(t, historyRows, 1)
	require.Equal(t, []string{want.Error()}, historyRows[0].Metrics.Errors)
	status, statusErr := scheduler.Status(context.Background())
	require.NoError(t, statusErr)
	require.Equal(t, want.Error(), status.LastError)
}
