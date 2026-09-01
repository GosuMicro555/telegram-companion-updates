//go:build linux || darwin

package backup

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

type descriptorRestoreWorkspace struct {
	root *os.File
}

func openRestoreWorkspace(path string) (restoreWorkspace, error) {
	root, err := openDirectoryNoSymlinks(path)
	if err != nil {
		return nil, err
	}
	return &descriptorRestoreWorkspace{root: root}, nil
}

func newRestoreWorkspace(root *os.File) restoreWorkspace {
	return &descriptorRestoreWorkspace{root: root}
}

func (w *descriptorRestoreWorkspace) CreateFile(path string) (*os.File, error) {
	parentFD, leaf, closeParent, err := w.openParent(path, true)
	if err != nil {
		return nil, err
	}
	defer closeParent()
	fd, err := unix.Openat(parentFD, leaf, unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create descriptor-relative restore file: %w", err)
	}
	return os.NewFile(uintptr(fd), path), nil
}

func (w *descriptorRestoreWorkspace) OpenFile(path string) (*os.File, error) {
	parentFD, leaf, closeParent, err := w.openParent(path, false)
	if err != nil {
		return nil, err
	}
	defer closeParent()
	fd, err := unix.Openat(parentFD, leaf, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("open descriptor-relative restore file: %w", err)
	}
	file := os.NewFile(uintptr(fd), path)
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		file.Close()
		return nil, ErrUnsafePath
	}
	return file, nil
}

func (w *descriptorRestoreWorkspace) RemoveFile(path string) error {
	parentFD, leaf, closeParent, err := w.openParent(path, false)
	if err != nil {
		return err
	}
	defer closeParent()
	err = unix.Unlinkat(parentFD, leaf, 0)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	return err
}

func (w *descriptorRestoreWorkspace) Sync() error {
	if w == nil || w.root == nil {
		return errors.New("restore workspace is closed")
	}
	return w.root.Sync()
}

func (w *descriptorRestoreWorkspace) Close() error {
	if w == nil || w.root == nil {
		return nil
	}
	err := w.root.Close()
	w.root = nil
	return err
}

func (w *descriptorRestoreWorkspace) openParent(path string, create bool) (int, string, func(), error) {
	if w == nil || w.root == nil || !safeArchivePath(path) {
		return 0, "", func() {}, ErrUnsafePath
	}
	components := strings.Split(path, "/")
	current := int(w.root.Fd())
	owned := false
	closeCurrent := func() {
		if owned {
			_ = unix.Close(current)
		}
	}
	for _, component := range components[:len(components)-1] {
		next, err := unix.Openat(current, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if errors.Is(err, unix.ENOENT) && create {
			if err := unix.Mkdirat(current, component, 0o700); err != nil && !errors.Is(err, unix.EEXIST) {
				closeCurrent()
				return 0, "", func() {}, err
			}
			next, err = unix.Openat(current, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		}
		if err != nil {
			closeCurrent()
			return 0, "", func() {}, fmt.Errorf("open descriptor-relative restore directory: %w", err)
		}
		closeCurrent()
		current = next
		owned = true
	}
	return current, components[len(components)-1], closeCurrent, nil
}

func removeTreeAt(parentFD int, name string) error {
	fd, err := unix.Openat(parentFD, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return err
	}
	directory := os.NewFile(uintptr(fd), name)
	entries, readErr := directory.ReadDir(-1)
	for _, entry := range entries {
		if entry.IsDir() {
			if err := removeTreeAt(fd, entry.Name()); err != nil {
				directory.Close()
				return err
			}
			continue
		}
		if err := unix.Unlinkat(fd, entry.Name(), 0); err != nil && !errors.Is(err, unix.ENOENT) {
			directory.Close()
			return err
		}
	}
	closeErr := directory.Close()
	if readErr != nil || closeErr != nil {
		return errors.Join(readErr, closeErr)
	}
	err = unix.Unlinkat(parentFD, name, unix.AT_REMOVEDIR)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	return err
}
