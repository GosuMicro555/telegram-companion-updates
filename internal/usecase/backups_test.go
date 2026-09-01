package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"telegram-companion/internal/domain"

	"github.com/stretchr/testify/require"
)

type backupManagerStub struct {
	created       []domain.BackupKind
	monthlyExists bool
	createErr     map[domain.BackupKind]error
	restored      [2]string
	exported      string
	createHook    func(domain.BackupKind)
}

func (m *backupManagerStub) Create(_ context.Context, kind domain.BackupKind) (domain.BackupRecord, error) {
	m.created = append(m.created, kind)
	if m.createHook != nil {
		m.createHook(kind)
	}
	if err := m.createErr[kind]; err != nil {
		return domain.BackupRecord{}, err
	}
	verified := time.Now()
	return domain.BackupRecord{Kind: kind, Status: domain.BackupVerified, VerifiedAt: &verified}, nil
}

type backupClockStub struct {
	now time.Time
}

func (c *backupClockStub) Now() time.Time { return c.now }

func (c *backupClockStub) WaitUntil(ctx context.Context, target time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.now = target
	return nil
}

func (m *backupManagerStub) HasVerifiedMonthly(context.Context, int, time.Month, *time.Location) (bool, error) {
	return m.monthlyExists, nil
}

func (m *backupManagerStub) Restore(_ context.Context, archive, destination string) error {
	m.restored = [2]string{archive, destination}
	return nil
}

func (m *backupManagerStub) ExportRecoveryKey(_ context.Context, path string) error {
	m.exported = path
	return nil
}

func TestBackupSchedulerNextUsesThreeAMInNowLocation(t *testing.T) {
	location := time.FixedZone("local", 3*60*60)
	scheduler := BackupScheduler{}
	require.Equal(t,
		time.Date(2026, 7, 11, 3, 0, 0, 0, location),
		scheduler.Next(time.Date(2026, 7, 11, 2, 59, 59, 0, location)),
	)
	require.Equal(t,
		time.Date(2026, 7, 12, 3, 0, 0, 0, location),
		scheduler.Next(time.Date(2026, 7, 11, 3, 0, 0, 0, location)),
	)
}

func TestBackupSchedulerDSTPolicy(t *testing.T) {
	location, err := time.LoadLocation("Europe/Helsinki")
	require.NoError(t, err)
	scheduler := BackupScheduler{}

	spring := scheduler.Next(time.Date(2026, 3, 29, 0, 0, 0, 0, location))
	require.Equal(t, time.Date(2026, 3, 29, 1, 0, 0, 0, time.UTC), spring.UTC(),
		"nonexistent 03:00 must use the first valid instant after the gap")
	require.Equal(t, 4, spring.Hour())

	fall := scheduler.Next(time.Date(2026, 10, 25, 0, 0, 0, 0, location))
	require.Equal(t, time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC), fall.UTC(),
		"repeated 03:00 must use the first occurrence")
	betweenOccurrences := time.Date(2026, 10, 25, 0, 30, 0, 0, time.UTC).In(location)
	require.Equal(t, time.Date(2026, 10, 26, 3, 0, 0, 0, location), scheduler.Next(betweenOccurrences),
		"a local calendar date must run at most once")
}

func TestBackupServiceCreatesDailyThenFirstSuccessfulMonthly(t *testing.T) {
	now := time.Date(2026, 7, 11, 3, 0, 0, 0, time.Local)
	manager := &backupManagerStub{createErr: make(map[domain.BackupKind]error)}
	service := NewBackupService(manager)

	records, err := service.RunScheduled(context.Background(), now)
	require.NoError(t, err)
	require.Equal(t, []domain.BackupKind{domain.BackupDaily, domain.BackupMonthly}, manager.created)
	require.Len(t, records, 2)

	manager.created = nil
	manager.monthlyExists = true
	records, err = service.RunScheduled(context.Background(), now.AddDate(0, 0, 1))
	require.NoError(t, err)
	require.Equal(t, []domain.BackupKind{domain.BackupDaily}, manager.created)
	require.Len(t, records, 1)
}

func TestBackupRunnerCallsScheduledServiceAtNextLocalThreeAM(t *testing.T) {
	location := time.FixedZone("local", 3*60*60)
	clock := &backupClockStub{now: time.Date(2026, 7, 11, 2, 59, 0, 0, location)}
	ctx, cancel := context.WithCancel(context.Background())
	manager := &backupManagerStub{createErr: make(map[domain.BackupKind]error)}
	manager.createHook = func(kind domain.BackupKind) {
		if kind == domain.BackupMonthly {
			cancel()
		}
	}
	runner := newBackupRunner(NewBackupService(manager), clock)

	err := runner.Run(ctx)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, time.Date(2026, 7, 11, 3, 0, 0, 0, location), clock.now)
	require.Equal(t, []domain.BackupKind{domain.BackupDaily, domain.BackupMonthly}, manager.created)
}

func TestBackupServiceRetriesMonthlyAndNeverRunsItAfterDailyFailure(t *testing.T) {
	now := time.Date(2026, 7, 1, 3, 0, 0, 0, time.Local)
	manager := &backupManagerStub{createErr: map[domain.BackupKind]error{domain.BackupDaily: errors.New("daily failed")}}
	service := NewBackupService(manager)
	_, err := service.RunScheduled(context.Background(), now)
	require.Error(t, err)
	require.Equal(t, []domain.BackupKind{domain.BackupDaily}, manager.created)

	manager.created = nil
	delete(manager.createErr, domain.BackupDaily)
	manager.createErr[domain.BackupMonthly] = errors.New("monthly failed")
	_, err = service.RunScheduled(context.Background(), now.AddDate(0, 0, 1))
	require.Error(t, err)
	require.Equal(t, []domain.BackupKind{domain.BackupDaily, domain.BackupMonthly}, manager.created)

	manager.created = nil
	delete(manager.createErr, domain.BackupMonthly)
	_, err = service.RunScheduled(context.Background(), now.AddDate(0, 0, 2))
	require.NoError(t, err)
	require.Equal(t, []domain.BackupKind{domain.BackupDaily, domain.BackupMonthly}, manager.created)
}

func TestBackupServiceRequiresVerifiedPreMigrationBackup(t *testing.T) {
	manager := &backupManagerStub{createErr: make(map[domain.BackupKind]error)}
	service := NewBackupService(manager)
	migrated := false
	require.NoError(t, service.RunMigration(context.Background(), func(context.Context) error {
		migrated = true
		return nil
	}))
	require.True(t, migrated)
	require.Equal(t, []domain.BackupKind{domain.BackupPreMigration}, manager.created)

	manager.created = nil
	manager.createErr[domain.BackupPreMigration] = errors.New("backup failed")
	migrated = false
	require.Error(t, service.RunMigration(context.Background(), func(context.Context) error {
		migrated = true
		return nil
	}))
	require.False(t, migrated)
}

func TestBackupServiceExposesRestoreAndOnlyExplicitKeyExport(t *testing.T) {
	manager := &backupManagerStub{createErr: make(map[domain.BackupKind]error)}
	service := NewBackupService(manager)
	require.NoError(t, service.Restore(context.Background(), "/archive", "/restore"))
	require.Equal(t, [2]string{"/archive", "/restore"}, manager.restored)
	require.Empty(t, manager.exported)
	require.NoError(t, service.ExportRecoveryKey(context.Background(), "/selected/key"))
	require.Equal(t, "/selected/key", manager.exported)
}
