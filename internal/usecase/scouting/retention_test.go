package scouting_test

import (
	"context"
	"testing"
	"time"

	"telegram-companion/internal/usecase/scouting"

	"github.com/stretchr/testify/require"
)

type prunerSpy struct {
	before     time.Time
	rows       int64
	called     chan struct{}
	maintained int64
}

func (p *prunerSpy) MaintainAfterPrune(_ context.Context, deleted int64) error {
	p.maintained = deleted
	return nil
}

func (p *prunerSpy) PruneBefore(_ context.Context, before time.Time) (int64, error) {
	p.before = before
	if p.called != nil {
		p.called <- struct{}{}
	}
	return p.rows, nil
}

func TestRetentionRunOnceReturnsDeletedCount(t *testing.T) {
	before := time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC)
	pruner := &prunerSpy{rows: 3}
	retention := scouting.NewRetention(pruner, &fixedClock{})

	result, err := retention.RunOnce(context.Background(), before)
	require.NoError(t, err)
	require.Equal(t, before, pruner.before)
	require.Equal(t, int64(3), result.MessagesDeleted)
	require.Equal(t, int64(3), pruner.maintained)
}

func TestRetentionHourlyRunUsesHundredYearCutoffFromInjectedClock(t *testing.T) {
	now := time.Date(2026, 7, 11, 8, 0, 0, 0, time.UTC)
	clock := &fixedClock{now: now}
	pruner := &prunerSpy{called: make(chan struct{}, 1)}
	retention := scouting.NewRetention(pruner, clock)
	ticks := make(chan time.Time)
	ctx, cancel := context.WithCancel(context.Background())
	errs := make(chan error, 1)
	go func() { errs <- retention.Run(ctx, ticks) }()
	ticks <- now
	<-pruner.called
	cancel()

	err := <-errs
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, now.Add(-100*365*24*time.Hour), pruner.before)
	require.Equal(t, time.Hour, scouting.RetentionInterval)
}

type metricsSourceStub struct {
	countCalls int
	rowCount   int64
	bytes      int64
	oldest     *time.Time
	newest     *time.Time
	paused     bool
}

func (s *metricsSourceStub) Count(context.Context) (int64, error) {
	s.countCalls++
	return s.rowCount, nil
}
func (s *metricsSourceStub) DatabaseBytes(context.Context) (int64, error) { return s.bytes, nil }
func (s *metricsSourceStub) TimestampBounds(context.Context) (*time.Time, *time.Time, error) {
	return s.oldest, s.newest, nil
}
func (s *metricsSourceStub) PausedForLowDisk() bool { return s.paused }

func TestStorageMetricsRefreshAtMostEverySixtySeconds(t *testing.T) {
	now := time.Date(2026, 7, 11, 8, 0, 0, 0, time.UTC)
	oldest, newest := now.Add(-time.Hour), now
	clock := &fixedClock{now: now}
	source := &metricsSourceStub{rowCount: 4, bytes: 8192, oldest: &oldest, newest: &newest, paused: true}
	metrics := scouting.NewMetrics(source, clock)

	first, err := metrics.Get(context.Background())
	require.NoError(t, err)
	require.Equal(t, scouting.StorageMetrics{
		RowCount: 4, DatabaseBytes: 8192, OldestTimestamp: &oldest,
		NewestTimestamp: &newest, PausedForLowDisk: true, LastRefresh: now,
	}, first)

	clock.now = now.Add(59 * time.Second)
	_, err = metrics.Get(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, source.countCalls)

	clock.now = now.Add(60 * time.Second)
	source.rowCount = 5
	refreshed, err := metrics.Get(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(5), refreshed.RowCount)
	require.Equal(t, 2, source.countCalls)
}

type blockedMetricsSource struct {
	started chan struct{}
	release chan struct{}
}

func (s *blockedMetricsSource) Count(ctx context.Context) (int64, error) {
	close(s.started)
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-s.release:
		return 1, nil
	}
}

func (s *blockedMetricsSource) DatabaseBytes(context.Context) (int64, error) { return 1, nil }
func (s *blockedMetricsSource) TimestampBounds(context.Context) (*time.Time, *time.Time, error) {
	return nil, nil, nil
}
func (s *blockedMetricsSource) PausedForLowDisk() bool { return false }

func TestStorageMetricsWaitingForBlockedRefreshHonorsCancellation(t *testing.T) {
	source := &blockedMetricsSource{started: make(chan struct{}), release: make(chan struct{})}
	defer close(source.release)
	metrics := scouting.NewMetrics(source, &fixedClock{now: time.Now()})
	firstDone := make(chan error, 1)
	go func() {
		_, err := metrics.Get(context.Background())
		firstDone <- err
	}()
	<-source.started

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	waiterDone := make(chan error, 1)
	go func() {
		_, err := metrics.Get(ctx)
		waiterDone <- err
	}()

	select {
	case err := <-waiterDone:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(250 * time.Millisecond):
		t.Fatal("cancelled metrics caller remained blocked behind refresh mutex")
	}
}
