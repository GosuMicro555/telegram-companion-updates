package usecase

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"telegram-companion/internal/domain"
	analyticsusecase "telegram-companion/internal/usecase/analytics"
)

var ErrAnalyticsCollectionRunning = errors.New("analytics collection is already running")

type AnalyticsHistoryResult struct {
	ChatsScanned    int64
	MessagesScanned int64
	Errors          []string
}

type AnalyticsHistorySyncer interface {
	SyncAnalyticsHistory(context.Context) (AnalyticsHistoryResult, error)
}

type CanonicalAnalyticsRunner interface {
	Analyze(context.Context) (analyticsusecase.CanonicalAnalysisResult, error)
}

type AnalyticsCollectionStatus struct {
	Enabled         bool
	Running         bool
	IntervalMinutes int
	LastRunAt       *time.Time
	NextRunAt       *time.Time
	LastError       string
	AllTimeMessages int64
	AllTimeKeywords int64
	AllTimeGroups   int64
	LatestCollection domain.AnalyticsRunMetrics
	Metrics          domain.AnalyticsMetrics
}

type AnalyticsScheduler struct {
	store                 domain.AnalyticsSchedulerStore
	history               AnalyticsHistorySyncer
	canonical             CanonicalAnalyticsRunner
	syncCanonicalTriggers func() error
	now                   func() time.Time

	mu             sync.Mutex
	enabled        bool
	interval       int
	running        bool
	lastRunAt      *time.Time
	nextRunAt      *time.Time
	lastError      string
	scheduleCancel context.CancelFunc
	scheduleDone   chan struct{}
	cycleDone      chan struct{}
}

func NewAnalyticsScheduler(store domain.AnalyticsSchedulerStore, history AnalyticsHistorySyncer, canonical CanonicalAnalyticsRunner, syncCanonicalTriggers func() error, now func() time.Time) *AnalyticsScheduler {
	if now == nil {
		now = time.Now
	}
	return &AnalyticsScheduler{
		store: store, history: history, canonical: canonical,
		syncCanonicalTriggers: syncCanonicalTriggers, now: now, interval: 10,
	}
}

func (s *AnalyticsScheduler) Restore(ctx context.Context) error {
	if err := s.ready(); err != nil {
		return err
	}
	settings, err := s.store.AnalyticsSettings(ctx)
	if err != nil {
		return err
	}
	if !settings.Valid() {
		return invalidAnalyticsIntervalError()
	}
	if !settings.Enabled {
		s.mu.Lock()
		s.enabled = false
		s.interval = settings.IntervalMinutes
		s.nextRunAt = nil
		s.mu.Unlock()
		return nil
	}
	s.startSchedule(ctx, settings.IntervalMinutes)
	return nil
}

func (s *AnalyticsScheduler) Start(ctx context.Context, intervalMinutes int) error {
	if err := s.ready(); err != nil {
		return err
	}
	settings := domain.AnalyticsSettings{Enabled: true, IntervalMinutes: intervalMinutes}
	if !settings.Valid() {
		return invalidAnalyticsIntervalError()
	}
	if err := s.store.SaveAnalyticsSettings(ctx, settings); err != nil {
		return err
	}
	s.startSchedule(ctx, intervalMinutes)
	return nil
}

func (s *AnalyticsScheduler) startSchedule(root context.Context, intervalMinutes int) {
	if root == nil {
		root = context.Background()
	}
	scheduleCtx, cancel := context.WithCancel(root)
	done := make(chan struct{})
	now := s.now().UTC()
	s.mu.Lock()
	previousCancel := s.scheduleCancel
	s.enabled = true
	s.interval = intervalMinutes
	s.nextRunAt = timePointer(now)
	s.scheduleCancel = cancel
	s.scheduleDone = done
	s.mu.Unlock()
	if previousCancel != nil {
		previousCancel()
	}
	go s.scheduleLoop(scheduleCtx, root, intervalMinutes, done)
}

func (s *AnalyticsScheduler) scheduleLoop(scheduleCtx, cycleCtx context.Context, intervalMinutes int, done chan struct{}) {
	defer close(done)
	s.runScheduled(cycleCtx)
	interval := time.Duration(intervalMinutes) * time.Minute
	for {
		if scheduleCtx.Err() != nil {
			return
		}
		next := s.now().UTC().Add(interval)
		s.mu.Lock()
		if s.enabled && s.scheduleDone == done {
			s.nextRunAt = timePointer(next)
		}
		s.mu.Unlock()
		timer := time.NewTimer(interval)
		select {
		case <-scheduleCtx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-timer.C:
			s.runScheduled(cycleCtx)
		}
	}
}

func (s *AnalyticsScheduler) runScheduled(ctx context.Context) {
	_, _ = s.RunNow(ctx)
}

func (s *AnalyticsScheduler) Stop(ctx context.Context) error {
	if err := s.ready(); err != nil {
		return err
	}
	s.mu.Lock()
	interval := s.interval
	s.mu.Unlock()
	if interval == 0 {
		interval = 10
	}
	settings := domain.AnalyticsSettings{Enabled: false, IntervalMinutes: interval}
	if err := s.store.SaveAnalyticsSettings(ctx, settings); err != nil {
		return err
	}
	s.mu.Lock()
	cancel := s.scheduleCancel
	s.scheduleCancel = nil
	s.enabled = false
	s.nextRunAt = nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

func (s *AnalyticsScheduler) RunNow(ctx context.Context) (run domain.AnalyticsSchedulerRun, resultErr error) {
	if err := s.ready(); err != nil {
		return run, err
	}
	cycleDone := make(chan struct{})
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return run, ErrAnalyticsCollectionRunning
	}
	s.running = true
	s.cycleDone = cycleDone
	s.mu.Unlock()

	startedAt := s.now().UTC()
	run = domain.AnalyticsSchedulerRun{ID: randomID(), StartedAt: startedAt}
	defer func() {
		finishedAt := s.now().UTC()
		run.FinishedAt = &finishedAt
		run.Metrics.Duration = finishedAt.Sub(startedAt)
		if resultErr != nil && len(run.Metrics.Errors) == 0 {
			run.Metrics.Errors = []string{resultErr.Error()}
		}
		persistErr := s.store.AppendAnalyticsRun(context.WithoutCancel(ctx), run)
		resultErr = errors.Join(resultErr, persistErr)
		if persistErr != nil {
			run.Metrics.Errors = append(run.Metrics.Errors, persistErr.Error())
		}
		s.mu.Lock()
		s.running = false
		s.lastRunAt = timePointer(finishedAt)
		s.lastError = firstAnalyticsError(run.Metrics.Errors)
		if s.cycleDone == cycleDone {
			s.cycleDone = nil
		}
		close(cycleDone)
		s.mu.Unlock()
	}()

	history, err := s.history.SyncAnalyticsHistory(ctx)
	run.Metrics.ProcessedGroups = history.ChatsScanned
	run.Metrics.NewMessages = history.MessagesScanned
	run.Metrics.Errors = append([]string(nil), history.Errors...)
	if err != nil {
		return run, err
	}
	before, err := s.store.AnalyticsMetrics(ctx)
	if err != nil {
		return run, err
	}
	canonical, err := s.canonical.Analyze(ctx)
	if err != nil {
		return run, err
	}
	run.Metrics.ExtractedWords = int64(canonical.KeywordCount)
	if err := s.syncCanonicalTriggers(); err != nil {
		run.Metrics.Errors = append(run.Metrics.Errors, err.Error())
		return run, err
	}
	after, err := s.store.AnalyticsMetrics(ctx)
	if err != nil {
		return run, err
	}
	if after.CanonicalCount > before.CanonicalCount {
		run.Metrics.NewCanonicals = after.CanonicalCount - before.CanonicalCount
	}
	return run, nil
}

func (s *AnalyticsScheduler) Status(ctx context.Context) (AnalyticsCollectionStatus, error) {
	if err := s.ready(); err != nil {
		return AnalyticsCollectionStatus{}, err
	}
	s.mu.Lock()
	status := AnalyticsCollectionStatus{
		Enabled: s.enabled, Running: s.running, IntervalMinutes: s.interval,
		LastRunAt: cloneTimePointer(s.lastRunAt), NextRunAt: cloneTimePointer(s.nextRunAt), LastError: s.lastError,
	}
	s.mu.Unlock()
	metrics, err := s.store.AnalyticsMetrics(ctx)
	if err != nil {
		return AnalyticsCollectionStatus{}, err
	}
	status.AllTimeMessages = metrics.MessageCount
	status.AllTimeKeywords = metrics.CanonicalCount
	status.AllTimeGroups = metrics.GroupCount
	status.LatestCollection = metrics.LastRun
	status.Metrics = metrics
	runs, err := s.store.AnalyticsRunHistory(ctx, 1)
	if err != nil {
		return AnalyticsCollectionStatus{}, err
	}
	if status.LastRunAt == nil && len(runs) > 0 {
		latest := runs[0]
		if latest.FinishedAt != nil {
			status.LastRunAt = cloneTimePointer(latest.FinishedAt)
		} else {
			status.LastRunAt = timePointer(latest.StartedAt)
		}
		status.LastError = firstAnalyticsError(latest.Metrics.Errors)
	}
	return status, nil
}

func (s *AnalyticsScheduler) History(ctx context.Context, limit int) ([]domain.AnalyticsSchedulerRun, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		return nil, errors.New("analytics history limit must be positive")
	}
	return s.store.AnalyticsRunHistory(ctx, limit)
}

func (s *AnalyticsScheduler) Shutdown(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	cancel := s.scheduleCancel
	s.scheduleCancel = nil
	s.nextRunAt = nil
	scheduleDone := s.scheduleDone
	cycleDone := s.cycleDone
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return waitAnalyticsScheduler(ctx, scheduleDone, cycleDone)
}

func (s *AnalyticsScheduler) ready() error {
	if s == nil || s.store == nil || s.history == nil || s.canonical == nil || s.syncCanonicalTriggers == nil || s.now == nil {
		return errors.New("analytics scheduler dependencies are required")
	}
	return nil
}

func waitAnalyticsScheduler(ctx context.Context, channels ...<-chan struct{}) error {
	seen := make(map[<-chan struct{}]struct{})
	for _, channel := range channels {
		if channel == nil {
			continue
		}
		if _, duplicate := seen[channel]; duplicate {
			continue
		}
		seen[channel] = struct{}{}
		select {
		case <-channel:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func invalidAnalyticsIntervalError() error {
	return fmt.Errorf("analytics interval must be one of 1, 5, 10, 30, 60, or 120 minutes")
}

func timePointer(value time.Time) *time.Time {
	copy := value
	return &copy
}

func cloneTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	return timePointer(*value)
}

func firstAnalyticsError(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}
