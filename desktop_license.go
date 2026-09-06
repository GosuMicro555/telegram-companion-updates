//go:build desktop

package main

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

const desktopLicenseFileLimit = 64 << 10

func selectDesktopLicenseFile(ctx context.Context) (string, error) {
	path, err := wailsruntime.OpenFileDialog(ctx, wailsruntime.OpenDialogOptions{
		Title: "Выберите файл лицензии",
		Filters: []wailsruntime.FileFilter{{
			DisplayName: "Telegram Companion License",
			Pattern:     "*.tcomplicense",
		}},
	})
	if err != nil {
		return "", errors.New("license file selection failed")
	}
	if path == "" {
		return "", nil
	}
	return readDesktopLicenseToken(path)
}

func readDesktopLicenseToken(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("license file could not be opened")
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, desktopLicenseFileLimit+1))
	if err != nil {
		return "", errors.New("license file could not be read")
	}
	if len(data) > desktopLicenseFileLimit {
		return "", errors.New("license file is too large")
	}
	token := strings.TrimSpace(string(data))
	if token == "" {
		return "", errors.New("license file is empty")
	}
	return token, nil
}
