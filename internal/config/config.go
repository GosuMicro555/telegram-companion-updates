package config

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/joho/godotenv"
)

type Config struct {
	Env      string `env:"APP_ENV" envDefault:"local"`
	Locale   string `env:"APP_LOCALE" envDefault:"ru"`
	LogLevel string `env:"APP_LOG_LEVEL" envDefault:"info"`
	DataDir  string `env:"APP_DATA_DIR" envDefault:"./data"`

	Postgres PostgresConfig
	Redis    RedisConfig
	Telegram TelegramConfig
	Rate     RateConfig
	Proxy    ProxyConfig
}

type PostgresConfig struct {
	DSN string `env:"POSTGRES_DSN,required"`
}

type RedisConfig struct {
	Addr     string `env:"REDIS_ADDR" envDefault:"localhost:6379"`
	Password string `env:"REDIS_PASSWORD"`
	DB       int    `env:"REDIS_DB" envDefault:"0"`
}

type TelegramConfig struct {
	APIID   int    `env:"TELEGRAM_API_ID,required"`
	APIHash string `env:"TELEGRAM_API_HASH,required"`
}

type RateConfig struct {
	GlobalMinInterval   time.Duration `env:"RATE_GLOBAL_MIN_INTERVAL" envDefault:"2s"`
	AccountMaxPerMinute int           `env:"RATE_ACCOUNT_MAX_PER_MINUTE" envDefault:"19"`
	DMMaxPerMinute      int           `env:"RATE_DM_MAX_PER_MINUTE" envDefault:"5"`
}

type ProxyConfig struct {
	GlobalMode string `env:"PROXY_GLOBAL_MODE" envDefault:"direct"`
}

func Load(ctx context.Context, path string) (Config, error) {
	select {
	case <-ctx.Done():
		return Config{}, ctx.Err()
	default:
	}

	if path != "" {
		if err := godotenv.Load(path); err != nil {
			return Config{}, fmt.Errorf("load env file: %w", err)
		}
	}

	var cfg Config
	if err := env.Parse(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse env: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func (c Config) Validate() error {
	if c.Locale != "ru" && c.Locale != "en" {
		return fmt.Errorf("unsupported locale %q", c.Locale)
	}
	if c.Rate.AccountMaxPerMinute <= 0 {
		return errors.New("RATE_ACCOUNT_MAX_PER_MINUTE must be > 0")
	}
	if c.Rate.AccountMaxPerMinute > 19 {
		return errors.New("RATE_ACCOUNT_MAX_PER_MINUTE must be <= 19")
	}
	if c.Rate.GlobalMinInterval < 2*time.Second {
		return errors.New("RATE_GLOBAL_MIN_INTERVAL must be >= 2s")
	}
	if c.Rate.DMMaxPerMinute <= 0 {
		return errors.New("RATE_DM_MAX_PER_MINUTE must be > 0")
	}
	if c.Rate.DMMaxPerMinute > c.Rate.AccountMaxPerMinute {
		return errors.New("RATE_DM_MAX_PER_MINUTE must be <= RATE_ACCOUNT_MAX_PER_MINUTE")
	}

	switch c.Proxy.GlobalMode {
	case "direct", "global", "assigned":
		return nil
	default:
		return fmt.Errorf("unsupported proxy mode %q", c.Proxy.GlobalMode)
	}
}
