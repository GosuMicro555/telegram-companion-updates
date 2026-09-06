//go:build !linux && !darwin

package backup

import (
	"errors"
	"os"
)

type unsupportedRestoreWorkspace struct{}

func openRestoreWorkspace(string) (restoreWorkspace, error) {
	return nil, errors.New("descriptor-relative restore workspace is unsupported on this platform")
}

func (*unsupportedRestoreWorkspace) CreateFile(string) (*os.File, error) { return nil, ErrUnsafePath }
func (*unsupportedRestoreWorkspace) OpenFile(string) (*os.File, error)   { return nil, ErrUnsafePath }
func (*unsupportedRestoreWorkspace) RemoveFile(string) error             { return ErrUnsafePath }
func (*unsupportedRestoreWorkspace) Sync() error                         { return nil }
func (*unsupportedRestoreWorkspace) Close() error                        { return nil }
