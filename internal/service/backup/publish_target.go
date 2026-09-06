package backup

import (
	"context"
	"os"
)

type restoreWorkspace interface {
	CreateFile(path string) (*os.File, error)
	OpenFile(path string) (*os.File, error)
	RemoveFile(path string) error
	Sync() error
	Close() error
}

type publishTarget interface {
	CreateTempDir(prefix string) (name string, workspace restoreWorkspace, err error)
	Publish(ctx context.Context, sourceName string) error
	RemoveTemp(name string) error
	Close() error
}
