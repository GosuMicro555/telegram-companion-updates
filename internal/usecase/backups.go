package usecase

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"telegram-companion/internal/domain"
)

type BackupOperations interface {
	Create(ctx context.Context, kind domain.BackupKind) (domain.BackupRecord, error)
	Restore(ctx context.Context, archive, destination string) error
	ExportRecoveryKey(ctx context.Context, selectedPath string) error
	HasVerifiedMonthly(ctx context.Context, year int, month time.Month, location *time.Location) (bool, error)
}

type BackupScheduler struct{}

// Next returns the next 03:00 wall-clock occurrence in now's location.
func (BackupScheduler) Next(now time.Time) time.Time {
	location := now.Location()
	next := firstLocalThreeAM(now.Year(), now.Month(), now.Day(), location)
	if !now.Before(next) {
		tomorrow := time.Date(now.Year(), now.Month(), now.Day()+1, 12, 0, 0, 0, location)
		next = firstLocalThreeAM(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(), location)
	}
	return next
}

func firstLocalThreeAM(year int, month time.Month, day int, location *time.Location) time.Time {
	wall := time.Date(year, month, day, 3, 0, 0, 0, time.UTC)
	start := wall.Add(-18 * time.Hour)
	end := wall.Add(18 * time.Hour)
	var firstAfterGap time.Time
	for instant := start; !instant.After(end); instant = instant.Add(time.Second) {
		local := instant.In(location)
		if local.Year() != year || local.Month() != month || local.Day() != day {
			continue
		}
		if local.Hour() == 3 && local.Minute() == 0 && local.Second() == 0 {
			return instant.In(location)
		}
		wallSecond := local.Hour()*60*60 + local.Minute()*60 + local.Second()
		if firstAfterGap.IsZero() && wallSecond > 3*60*60 {
			firstAfterGap = instant
		}
	}
	if !firstAfterGap.IsZero() {
		return firstAfterGap.In(location)
	}
	return time.Date(year, month, day, 3, 0, 0, 0, location)
}

type BackupClock interface {
	Now() time.Time
	WaitUntil(ctx context.Context, target time.Time) error
}

type systemBackupClock struct{}

func (systemBackupClock) Now() time.Time { return time.Now() }

func (systemBackupClock) WaitUntil(ctx context.Context, target time.Time) error {
	delay := time.Until(target)
	if delay < 0 {
		delay = 0
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type BackupRunner struct {
	service   *BackupService
	scheduler BackupScheduler
	clock     BackupClock
}

func NewBackupRunner(service *BackupService) *BackupRunner {
	return newBackupRunner(service, systemBackupClock{})
}

func newBackupRunner(service *BackupService, clock BackupClock) *BackupRunner {
	return &BackupRunner{service: service, scheduler: BackupScheduler{}, clock: clock}
}

func (r *BackupRunner) Run(ctx context.Context) error {
	if r == nil || r.service == nil || r.clock == nil {
		return errors.New("backup runner is not configured")
	}
	for {
		next := r.scheduler.Next(r.clock.Now())
		if err := r.clock.WaitUntil(ctx, next); err != nil {
			return err
		}
		_, _ = r.service.RunScheduled(ctx, r.clock.Now())
		if err := ctx.Err(); err != nil {
			return err
		}
	}
}

type BackupService struct {
	manager  BackupOperations
	mu       sync.Mutex
	latestMu sync.RWMutex
	latest   domain.BackupRecord
}

func NewBackupService(manager BackupOperations) *BackupService {
	return &BackupService{manager: manager}
}

func (s *BackupService) Create(ctx context.Context, kind domain.BackupKind) (domain.BackupRecord, error) {
	if s == nil || s.manager == nil {
		return domain.BackupRecord{}, errors.New("backup service is not configured")
	}
	record, err := s.manager.Create(ctx, kind)
	s.latestMu.Lock()
	s.latest = record
	s.latestMu.Unlock()
	return record, err
}

func (s *BackupService) Latest() domain.BackupRecord {
	if s == nil {
		return domain.BackupRecord{}
	}
	s.latestMu.RLock()
	defer s.latestMu.RUnlock()
	return s.latest
}

// RunScheduled creates the daily archive first. The monthly archive is
// attempted only after that daily succeeds and only until the month has one.
func (s *BackupService) RunScheduled(ctx context.Context, now time.Time) ([]domain.BackupRecord, error) {
	if s == nil || s.manager == nil {
		return nil, errors.New("backup service is not configured")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	daily, err := s.Create(ctx, domain.BackupDaily)
	if err != nil {
		return nil, fmt.Errorf("create daily backup: %w", err)
	}
	records := []domain.BackupRecord{daily}
	local := now.In(now.Location())
	exists, err := s.manager.HasVerifiedMonthly(ctx, local.Year(), local.Month(), local.Location())
	if err != nil {
		return records, fmt.Errorf("check monthly backup: %w", err)
	}
	if exists {
		return records, nil
	}
	monthly, err := s.Create(ctx, domain.BackupMonthly)
	if err != nil {
		return records, fmt.Errorf("create monthly backup: %w", err)
	}
	return append(records, monthly), nil
}

// RunMigration makes successful verification the gate before any schema
// migration callback can run.
func (s *BackupService) RunMigration(ctx context.Context, migrate func(context.Context) error) error {
	if s == nil || s.manager == nil {
		return errors.New("backup service is not configured")
	}
	if migrate == nil {
		return errors.New("migration callback is required")
	}
	if err := s.RequirePreMigrationBackup(ctx); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return migrate(ctx)
}

func (s *BackupService) RequirePreMigrationBackup(ctx context.Context) error {
	if s == nil || s.manager == nil {
		return errors.New("backup service is not configured")
	}
	record, err := s.Create(ctx, domain.BackupPreMigration)
	if err != nil {
		return fmt.Errorf("create pre-migration backup: %w", err)
	}
	if record.Status != domain.BackupVerified || record.VerifiedAt == nil {
		return errors.New("pre-migration backup was not verified")
	}
	return nil
}

func (s *BackupService) Restore(ctx context.Context, archive, destination string) error {
	if s == nil || s.manager == nil {
		return errors.New("backup service is not configured")
	}
	return s.manager.Restore(ctx, archive, destination)
}

func (s *BackupService) ExportRecoveryKey(ctx context.Context, selectedPath string) error {
	if s == nil || s.manager == nil {
		return errors.New("backup service is not configured")
	}
	return s.manager.ExportRecoveryKey(ctx, selectedPath)
}
