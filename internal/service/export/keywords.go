package export

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

type KeywordExporter struct {
	path string
}

func NewKeywordExporter(exportPath string) *KeywordExporter {
	return &KeywordExporter{path: exportPath}
}

func DefaultKeywordExportPath(root string) string {
	return filepath.Join(root, "data", "exports", "keywords.txt")
}

func (e *KeywordExporter) Path() string {
	if e == nil {
		return ""
	}
	return e.path
}

func (e *KeywordExporter) Export(ctx context.Context, keywords []string) (string, error) {
	if e == nil || strings.TrimSpace(e.path) == "" {
		return "", errors.New("keyword export path is required")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	for _, keyword := range keywords {
		if !utf8.ValidString(keyword) {
			return "", errors.New("keyword is not valid UTF-8")
		}
		if strings.ContainsAny(keyword, "\r\n") {
			return "", errors.New("keyword must be a single line")
		}
	}
	directory := filepath.Dir(e.path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", fmt.Errorf("create export directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".keywords-*.tmp")
	if err != nil {
		return "", fmt.Errorf("create temporary export: %w", err)
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return "", fmt.Errorf("secure temporary export: %w", err)
	}
	for _, keyword := range keywords {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		keyword = strings.TrimSpace(keyword)
		if keyword == "" {
			continue
		}
		if _, err := temporary.WriteString(keyword + "\n"); err != nil {
			return "", fmt.Errorf("write keyword export: %w", err)
		}
	}
	if err := temporary.Sync(); err != nil {
		return "", fmt.Errorf("sync keyword export: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return "", fmt.Errorf("close keyword export: %w", err)
	}
	if err := os.Rename(temporaryPath, e.path); err != nil {
		return "", fmt.Errorf("commit keyword export: %w", err)
	}
	committed = true
	if err := syncDirectory(directory); err != nil {
		return "", err
	}
	return e.path, nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open export directory for sync: %w", err)
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("sync export directory: %w", err)
	}
	return nil
}
