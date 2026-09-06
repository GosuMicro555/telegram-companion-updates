//go:build linux || darwin

package backup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

type descriptorPublishTarget struct {
	parent     *os.File
	parentPath string
	leaf       string
}

func prepareAtomicPublish(destination string) (publishTarget, error) {
	if destination == "" || !filepath.IsAbs(destination) || filepath.Clean(destination) != destination {
		return nil, ErrUnsafePath
	}
	leaf := filepath.Base(destination)
	if leaf == "." || leaf == ".." || strings.ContainsRune(leaf, os.PathSeparator) {
		return nil, ErrUnsafePath
	}
	parentPath := filepath.Dir(destination)
	parent, err := openDirectoryNoSymlinks(parentPath)
	if err != nil {
		return nil, err
	}
	return &descriptorPublishTarget{parent: parent, parentPath: parent.Name(), leaf: leaf}, nil
}

func openDirectoryNoSymlinks(path string) (*os.File, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, ErrUnsafePath
	}
	path, err := normalizeTrustedSystemPath(path)
	if err != nil {
		return nil, err
	}
	fd, err := unix.Open(string(os.PathSeparator), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	current := fd
	for _, component := range strings.Split(strings.TrimPrefix(path, string(os.PathSeparator)), string(os.PathSeparator)) {
		if component == "" {
			continue
		}
		next, openErr := unix.Openat(current, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if openErr != nil {
			_ = unix.Close(current)
			return nil, fmt.Errorf("%w: open restore parent: %v", ErrUnsafePath, openErr)
		}
		_ = unix.Close(current)
		current = next
	}
	return os.NewFile(uintptr(current), path), nil
}

func (t *descriptorPublishTarget) CreateTempDir(prefix string) (string, restoreWorkspace, error) {
	if t == nil || t.parent == nil {
		return "", nil, errors.New("publish target is closed")
	}
	id, err := randomID()
	if err != nil {
		return "", nil, err
	}
	name := prefix + id
	if err := unix.Mkdirat(int(t.parent.Fd()), name, 0o700); err != nil {
		return "", nil, fmt.Errorf("create descriptor-relative restore temp: %w", err)
	}
	fd, err := unix.Openat(int(t.parent.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		_ = unix.Unlinkat(int(t.parent.Fd()), name, unix.AT_REMOVEDIR)
		return "", nil, err
	}
	return name, newRestoreWorkspace(os.NewFile(uintptr(fd), name)), nil
}

func (t *descriptorPublishTarget) Publish(ctx context.Context, sourceName string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if t == nil || t.parent == nil || filepath.Base(sourceName) != sourceName || sourceName == "." || sourceName == ".." {
		return ErrUnsafePath
	}
	if err := t.parentStillSelected(); err != nil {
		return err
	}
	if err := renameNoReplaceAt(int(t.parent.Fd()), sourceName, t.leaf); err != nil {
		return err
	}
	return t.parent.Sync()
}

func (t *descriptorPublishTarget) RemoveTemp(name string) error {
	if t == nil || t.parent == nil || filepath.Base(name) != name || name == "." || name == ".." {
		return ErrUnsafePath
	}
	return removeTreeAt(int(t.parent.Fd()), name)
}

func (t *descriptorPublishTarget) parentStillSelected() error {
	selected, err := os.Lstat(t.parentPath)
	if err != nil || selected.Mode()&os.ModeSymlink != 0 || !selected.IsDir() {
		return ErrUnsafePath
	}
	opened, err := t.parent.Stat()
	if err != nil || !os.SameFile(selected, opened) {
		return ErrUnsafePath
	}
	return nil
}

func (t *descriptorPublishTarget) Close() error {
	if t == nil || t.parent == nil {
		return nil
	}
	err := t.parent.Close()
	t.parent = nil
	return err
}

func atomicPublishNoReplace(ctx context.Context, source, destination string) error {
	target, err := prepareAtomicPublish(destination)
	if err != nil {
		return err
	}
	defer target.Close()
	parentInfo, err := os.Lstat(filepath.Dir(source))
	if err != nil || parentInfo.Mode()&os.ModeSymlink != 0 || !parentInfo.IsDir() {
		return ErrUnsafePath
	}
	descriptorTarget := target.(*descriptorPublishTarget)
	openedInfo, err := descriptorTarget.parent.Stat()
	if err != nil || !os.SameFile(parentInfo, openedInfo) {
		return ErrUnsafePath
	}
	return target.Publish(ctx, filepath.Base(source))
}
