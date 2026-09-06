package domain

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"time"
)

type BackupKind string

const (
	BackupKindDaily        BackupKind = "daily"
	BackupKindMonthly      BackupKind = "monthly"
	BackupKindPreMigration BackupKind = "pre_migration"

	BackupDaily        = BackupKindDaily
	BackupMonthly      = BackupKindMonthly
	BackupPreMigration = BackupKindPreMigration

	BackupPending  = "pending"
	BackupRunning  = "running"
	BackupVerified = "verified"
	BackupError    = "error"

	BackupFailureCancelled         = "backup_cancelled"
	BackupFailureTimeout           = "backup_timeout"
	BackupFailureUnsafePath        = "backup_unsafe_path"
	BackupFailureSourceUnavailable = "backup_source_unavailable"
	BackupFailureSourceUnreadable  = "backup_source_unreadable"
	BackupFailureHistory           = "backup_history_failed"
	BackupFailureGeneric           = "backup_failed"
)

func BackupFailureCode(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return BackupFailureTimeout
	case errors.Is(err, context.Canceled):
		return BackupFailureCancelled
	case errors.Is(err, fs.ErrNotExist):
		return BackupFailureSourceUnavailable
	case errors.Is(err, fs.ErrPermission):
		return BackupFailureSourceUnreadable
	default:
		return BackupFailureGeneric
	}
}

func SafeBackupError(value string) string {
	value = strings.TrimSpace(value)
	switch value {
	case "", BackupFailureCancelled, BackupFailureTimeout, BackupFailureUnsafePath,
		BackupFailureSourceUnavailable, BackupFailureSourceUnreadable,
		BackupFailureHistory, BackupFailureGeneric:
		return value
	default:
		return BackupFailureGeneric
	}
}

type BackupRecord struct {
	ID          ID
	ArchivePath string
	Kind        BackupKind
	SizeBytes   int64
	SHA256      string
	Status      string
	CreatedAt   time.Time
	VerifiedAt  *time.Time
	Error       string
}
