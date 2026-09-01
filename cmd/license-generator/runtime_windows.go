//go:build windows

package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"golang.org/x/sys/windows"
)

type windowsGeneratorRuntime struct{}

func newWindowsGeneratorRuntime() *windowsGeneratorRuntime {
	return &windowsGeneratorRuntime{}
}

func (*windowsGeneratorRuntime) CopyText(ctx context.Context, text string) error {
	return wailsruntime.ClipboardSetText(ctx, text)
}

func (*windowsGeneratorRuntime) SelectSavePath(ctx context.Context, kind fileKind, defaultName string) (string, error) {
	filter, extension := fileFilter(kind)
	path, err := wailsruntime.SaveFileDialog(ctx, wailsruntime.SaveDialogOptions{
		Title:                filter.DisplayName,
		DefaultFilename:      defaultName,
		Filters:              []wailsruntime.FileFilter{filter},
		CanCreateDirectories: true,
	})
	if err != nil || path == "" {
		return path, err
	}
	if filepath.Ext(path) == "" {
		path += extension
	}
	return path, nil
}

func (*windowsGeneratorRuntime) SelectOpenPath(ctx context.Context, kind fileKind) (string, error) {
	filter, _ := fileFilter(kind)
	return wailsruntime.OpenFileDialog(ctx, wailsruntime.OpenDialogOptions{
		Title:   filter.DisplayName,
		Filters: []wailsruntime.FileFilter{filter},
	})
}

func (*windowsGeneratorRuntime) WriteFile(path string, data []byte, mode os.FileMode) error {
	if path == "" || len(data) == 0 {
		return errors.New("invalid output")
	}
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".telegram-companion-export-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	writeErr := temporary.Chmod(mode)
	if writeErr == nil {
		_, writeErr = temporary.Write(data)
	}
	if writeErr == nil {
		writeErr = temporary.Sync()
	}
	closeErr := temporary.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	from, err := windows.UTF16PtrFromString(temporaryPath)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}

func (*windowsGeneratorRuntime) ReadFile(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() < 1 || info.Size() > limit {
		return nil, errors.New("invalid input size")
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, errors.New("invalid input")
	}
	return data, nil
}

func fileFilter(kind fileKind) (wailsruntime.FileFilter, string) {
	if kind == fileKindLicense {
		return wailsruntime.FileFilter{
			DisplayName: "Лицензия Telegram Companion (*.tcomplicense)",
			Pattern:     "*.tcomplicense",
		}, ".tcomplicense"
	}
	if kind == fileKindBuildSeed {
		return wailsruntime.FileFilter{
			DisplayName: "Encrypted build seed (*.tcompbuildseed)",
			Pattern:     "*.tcompbuildseed",
		}, ".tcompbuildseed"
	}
	return wailsruntime.FileFilter{
		DisplayName: "Резервная копия ключа (*.tcompkeybackup)",
		Pattern:     "*.tcompkeybackup",
	}, ".tcompkeybackup"
}

var _ generatorRuntime = (*windowsGeneratorRuntime)(nil)
