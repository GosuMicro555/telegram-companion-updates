package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadConfigFromEnvFile(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	content := []byte("APP_ENV=test\nAPP_LOCALE=ru\nAPP_LOG_LEVEL=debug\nAPP_DATA_DIR=./data\nPOSTGRES_DSN=postgres://x\nREDIS_ADDR=localhost:6379\nREDIS_DB=2\nTELEGRAM_API_ID=123\nTELEGRAM_API_HASH=hash\nRATE_GLOBAL_MIN_INTERVAL=2s\nRATE_ACCOUNT_MAX_PER_MINUTE=19\nRATE_DM_MAX_PER_MINUTE=5\nPROXY_GLOBAL_MODE=direct\n")
	if err := os.WriteFile(envPath, content, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(context.Background(), envPath)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.Locale != "ru" {
		t.Fatalf("Locale = %q, want ru", cfg.Locale)
	}
	if cfg.Telegram.APIID != 123 {
		t.Fatalf("APIID = %d, want 123", cfg.Telegram.APIID)
	}
	if cfg.Rate.AccountMaxPerMinute != 19 {
		t.Fatalf("AccountMaxPerMinute = %d, want 19", cfg.Rate.AccountMaxPerMinute)
	}
	if cfg.Rate.GlobalMinInterval != 2*time.Second {
		t.Fatalf("GlobalMinInterval = %s, want 2s", cfg.Rate.GlobalMinInterval)
	}
}

func TestLoadRejectsUnsafeRateLimit(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	t.Setenv("APP_LOCALE", "ru")
	t.Setenv("APP_LOG_LEVEL", "info")
	t.Setenv("APP_DATA_DIR", "./data")
	t.Setenv("POSTGRES_DSN", "postgres://x")
	t.Setenv("REDIS_ADDR", "localhost:6379")
	t.Setenv("REDIS_DB", "0")
	t.Setenv("TELEGRAM_API_ID", "123")
	t.Setenv("TELEGRAM_API_HASH", "hash")
	t.Setenv("RATE_GLOBAL_MIN_INTERVAL", "1s")
	t.Setenv("RATE_ACCOUNT_MAX_PER_MINUTE", "20")
	t.Setenv("RATE_DM_MAX_PER_MINUTE", "5")
	t.Setenv("PROXY_GLOBAL_MODE", "direct")

	_, err := Load(context.Background(), "")
	if err == nil {
		t.Fatal("Load succeeded, want error")
	}
}

func TestLoadRejectsNonPositiveRateLimits(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	t.Setenv("APP_LOCALE", "ru")
	t.Setenv("APP_LOG_LEVEL", "info")
	t.Setenv("APP_DATA_DIR", "./data")
	t.Setenv("POSTGRES_DSN", "postgres://x")
	t.Setenv("REDIS_ADDR", "localhost:6379")
	t.Setenv("REDIS_DB", "0")
	t.Setenv("TELEGRAM_API_ID", "123")
	t.Setenv("TELEGRAM_API_HASH", "hash")
	t.Setenv("RATE_GLOBAL_MIN_INTERVAL", "2s")
	t.Setenv("RATE_ACCOUNT_MAX_PER_MINUTE", "0")
	t.Setenv("RATE_DM_MAX_PER_MINUTE", "0")
	t.Setenv("PROXY_GLOBAL_MODE", "direct")

	_, err := Load(context.Background(), "")
	if err == nil {
		t.Fatal("Load succeeded, want error")
	}
}
