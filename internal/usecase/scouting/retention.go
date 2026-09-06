package scouting

import (
	"context"
	"fmt"
	"sync"
	"time"

	"telegram-companion/internal/domain"
)

const (
	RetentionPeriod          = 100 * 365 * 24 * time.Hour
	TombstoneRetentionPeriod = RetentionPeriod
	RetentionInterval        = time.Hour
	MetricsInterval          = 60 * time.Second
)

type MessagePruner interface {
	PruneBefore(ctx context.Context, before time.Time) (int64, error)
}

type postPruneMaintainer interface {
	MaintainAfterPrune(ctx context.Context, deleted int64) error
}

type PruneResult struct {
	MessagesDeleted int64
}

type Retention struct {
	store MessagePruner
	clock domain.Clock
}

func NewRetention(store MessagePruner, clock domain.Clock) *Retention {
	return &Retention{store: store, clock: clock}
}

func (r *Retention) RunOnce(ctx context.Context, before time.Time) (PruneResult, error) {
	deleted, err := r.store.PruneBefore(ctx, before)
	if err != nil {
		return PruneResult{}, err
	}
	if maintainer, ok := r.store.(postPruneMaintainer); ok {
		if err := maintainer.MaintainAfterPrune(ctx, deleted); err != nil {
			return PruneResult{}, err
		}
	}
	return PruneResult{MessagesDeleted: deleted}, nil
}

func (r *Retention) Run(ctx context.Context, hourly <-chan time.Time) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case _, ok := <-hourly:
			if !ok {
				return nil
			}
			if _, err := r.RunOnce(ctx, r.clock.Now().Add(-RetentionPeriod)); err != nil {
				return err
			}
		}
	}
}

type StorageMetrics struct {
	RowCount         int64
	DatabaseBytes    int64
	OldestTimestamp  *time.Time
	NewestTimestamp  *time.Time
	PausedForLowDisk bool
	LastRefresh      time.Time
}

type MetricsSource interface {
	Count(ctx context.Context) (int64, error)
	DatabaseBytes(ctx context.Context) (int64, error)
	TimestampBounds(ctx context.Context) (*time.Time, *time.Time, error)
	PausedForLowDisk() bool
}

type Metrics struct {
	source      MetricsSource
	clock       domain.Clock
	mu          sync.Mutex
	cached      StorageMetrics
	refreshDone chan struct{}
}

func NewMetrics(source MetricsSource, clock domain.Clock) *Metrics {
	return &Metrics{source: source, clock: clock}
}

func (m *Metrics) Get(ctx context.Context) (StorageMetrics, error) {
	for {
		if err := ctx.Err(); err != nil {
			return StorageMetrics{}, err
		}
		now := m.clock.Now()
		m.mu.Lock()
		if !m.cached.LastRefresh.IsZero() && now.Sub(m.cached.LastRefresh) < MetricsInterval {
			cached := m.cached
			m.mu.Unlock()
			return cached, nil
		}
		if m.refreshDone != nil {
			done := m.refreshDone
			m.mu.Unlock()
			select {
			case <-ctx.Done():
				return StorageMetrics{}, ctx.Err()
			case <-done:
				continue
			}
		}
		done := make(chan struct{})
		m.refreshDone = done
		m.mu.Unlock()

		refreshed, err := m.refresh(ctx, now)
		m.mu.Lock()
		if err == nil {
			m.cached = refreshed
		}
		m.refreshDone = nil
		close(done)
		m.mu.Unlock()
		return refreshed, err
	}
}

func (m *Metrics) refresh(ctx context.Context, now time.Time) (StorageMetrics, error) {
	rowCount, err := m.source.Count(ctx)
	if err != nil {
		return StorageMetrics{}, fmt.Errorf("refresh storage row count: %w", err)
	}
	databaseBytes, err := m.source.DatabaseBytes(ctx)
	if err != nil {
		return StorageMetrics{}, fmt.Errorf("refresh database bytes: %w", err)
	}
	oldest, newest, err := m.source.TimestampBounds(ctx)
	if err != nil {
		return StorageMetrics{}, fmt.Errorf("refresh timestamp bounds: %w", err)
	}
	return StorageMetrics{
		RowCount: rowCount, DatabaseBytes: databaseBytes,
		OldestTimestamp: oldest, NewestTimestamp: newest,
		PausedForLowDisk: m.source.PausedForLowDisk(), LastRefresh: now,
	}, nil
}
