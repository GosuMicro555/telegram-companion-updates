package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"

	"telegram-companion/internal/config"
	"telegram-companion/internal/logger"
	"telegram-companion/migrations"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/joho/godotenv"
	"github.com/pressly/goose/v3"
)

func main() {
	os.Exit(run(context.Background(), os.Args, os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer) int {
	cmd := "serve"
	if len(args) > 1 {
		cmd = args[1]
	}

	switch cmd {
	case "serve":
		return runServe(ctx, stderr)
	case "migrate-up":
		if err := runMigrateUp(ctx); err != nil {
			_, _ = fmt.Fprintf(stderr, "migrate-up: %v\n", err)
			return 1
		}
		_, _ = fmt.Fprintln(stdout, "migrations applied")
		return 0
	default:
		_, _ = fmt.Fprintf(stderr, "unknown command %q\n", cmd)
		return 2
	}
}

func runServe(ctx context.Context, stderr io.Writer) int {
	cfg, err := config.Load(ctx, ".env")
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "load config: %v\n", err)
		return 1
	}

	log, err := logger.New(cfg.LogLevel)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "create logger: %v\n", err)
		return 1
	}

	log.Info("telegram companion command", "cmd", "serve", "env", cfg.Env, "locale", cfg.Locale)
	return 0
}

func runMigrateUp(ctx context.Context) error {
	if err := loadDefaultEnv(); err != nil {
		return err
	}
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		return errors.New("POSTGRES_DSN is required")
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}
	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("set migration dialect: %w", err)
	}
	if err := goose.UpContext(ctx, db, "."); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}

func loadDefaultEnv() error {
	if err := godotenv.Load(".env"); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("load .env: %w", err)
	}
	return nil
}
