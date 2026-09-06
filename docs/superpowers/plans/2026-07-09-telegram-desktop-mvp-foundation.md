# Telegram Desktop MVP Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the first runnable MVP slice of the macOS-first Telegram desktop app: project skeleton, configuration, persistence, domain/use cases, fake Telegram gateway, queue/rate scheduling, localized Wails UI shell, and Dockerized local infrastructure.

**Architecture:** This plan implements the stable core boundaries before real Telegram network integration. The app uses Clean Architecture: domain and use cases are independent of Wails, PostgreSQL, Redis, and Telegram; adapters implement repository, queue, and gateway interfaces. A fake Telegram gateway proves channel import, membership, keyword matching, round-robin selection, public replies, private-message jobs, START/STOP, and stats without risking Telegram accounts.

**Tech Stack:** Go 1.25+, Wails v2 stable, TypeScript + React + Vite, PostgreSQL 16+, Redis 7+, goose migrations, pgx, go-redis, slog, godotenv, golangci-lint, Docker Compose.

## Global Constraints

- Desktop application only; do not build a browser dashboard as the primary UI.
- macOS-first UX; Ubuntu is the first build/test environment.
- Use Go 1.25+ and Go modules.
- Use `context.Context` in backend flows.
- Use structured logging with `slog`.
- Use graceful shutdown.
- Use dependency injection through explicit constructors.
- Use configuration through `.env`.
- Include Makefile, Dockerfile, docker-compose.yml, README.md, migrations, linter config, and unit tests.
- Follow Clean Architecture under `cmd/`, `internal/`, `pkg/`, `configs/`, `migrations/`, `scripts/`, and `deploy/`.
- Telegram integration must be behind interfaces; this plan uses a fake gateway and creates the extension point for real MTProto/tdata.
- Primary UI language is Russian; English strings must be supported through the same i18n mechanism.
- Hard outgoing limits: max 19 messages per minute and no more often than one outgoing message every 2 seconds.
- Keywords are global across all channels.
- Account selection for outgoing jobs is round-robin.
- Public replies and automatic private messages are supported as separate job types.
- Per-account proxy configuration is part of the data model and client-factory contract.
- Do not commit secrets, `.env`, session files, tdata extracts, auth keys, or proxy passwords.

---

## Scope Check

The approved product spec covers several large subsystems: real Telegram `tdata` import, MTProto listeners, channel joining, desktop UI, queueing, persistence, proxies, and stats. This plan intentionally implements the first independently testable slice. It does not connect to real Telegram yet. It prepares the exact interfaces and tables needed so the next plan can add real `gotd/td` + `tdata` without changing UI or use case boundaries.

## File Map

- Create `go.mod`: Go module and backend dependencies.
- Create `.gitignore`: excludes secrets, sessions, local build output, and frontend artifacts.
- Create `.env.example`: documented local configuration.
- Create `Makefile`: common build/test/lint/dev commands.
- Create `Dockerfile`: Linux build/runtime image for backend sanity checks.
- Create `docker-compose.yml`: PostgreSQL and Redis services.
- Create `.golangci.yml`: linter configuration.
- Create `README.md`: setup, local run, architecture, safety constraints.
- Create `configs/app.example.env`: app-level example config.
- Create `migrations/000001_init.sql`: PostgreSQL schema for accounts, proxies, channels, memberships, rules, jobs, events, and settings.
- Create `cmd/telegram-companion/main.go`: application entrypoint for backend smoke runs outside Wails.
- Create `internal/config/config.go`: typed `.env` loading.
- Create `internal/logger/logger.go`: slog setup.
- Create `internal/domain/*.go`: entities, statuses, validation, and ports.
- Create `internal/usecase/*.go`: account/channel/rule/automation/scheduler/stats use cases.
- Create `internal/repository/postgres/*.go`: PostgreSQL repositories.
- Create `internal/repository/redisqueue/*.go`: Redis queue and limiter adapter.
- Create `internal/telegram/fake/*.go`: fake Telegram gateway for tests/dev.
- Create `internal/app/app.go`: dependency wiring and lifecycle.
- Create `internal/transport/wails/bindings.go`: Wails-facing backend methods.
- Create `frontend/package.json`, `frontend/src/*`: React UI shell with Russian default translations.
- Create `tests` through Go `_test.go` files next to packages.

---

### Task 1: Project Skeleton and Tooling

**Files:**
- Create: `go.mod`
- Create: `.gitignore`
- Create: `.env.example`
- Create: `configs/app.example.env`
- Create: `Makefile`
- Create: `Dockerfile`
- Create: `docker-compose.yml`
- Create: `.golangci.yml`
- Create: `README.md`

**Interfaces:**
- Consumes: none.
- Produces: module `telegram-companion`, make targets `test`, `lint`, `compose-up`, `compose-down`, `migrate-up`, `run-backend`.

- [ ] **Step 1: Create Go module file**

Create `go.mod`:

```go
module telegram-companion

go 1.25
```

Run: `go mod tidy`

Expected: `go mod tidy` exits successfully. `go.sum` may not exist until later tasks add imports.

- [ ] **Step 2: Create ignore rules**

Create `.gitignore`:

```gitignore
.env
.env.*
!.env.example
!configs/*.example.env

bin/
dist/
build/
tmp/
coverage.out

frontend/node_modules/
frontend/dist/

data/
sessions/
tdata/
*.session
*.rar
*.7z
*.zip

.DS_Store
```

- [ ] **Step 3: Create environment examples**

Create `.env.example`:

```dotenv
APP_ENV=local
APP_LOCALE=ru
APP_LOG_LEVEL=debug
APP_DATA_DIR=./data

POSTGRES_DSN=postgres://telegram:telegram@localhost:5432/telegram_companion?sslmode=disable
REDIS_ADDR=localhost:6379
REDIS_PASSWORD=
REDIS_DB=0

TELEGRAM_API_ID=0
TELEGRAM_API_HASH=

RATE_GLOBAL_MIN_INTERVAL=2s
RATE_ACCOUNT_MAX_PER_MINUTE=19
RATE_DM_MAX_PER_MINUTE=5

PROXY_GLOBAL_MODE=direct
```

Create `configs/app.example.env` with the same content.

- [ ] **Step 4: Create Makefile**

Create `Makefile`:

```makefile
.PHONY: test lint compose-up compose-down migrate-up run-backend tidy

test:
	go test ./...

lint:
	golangci-lint run ./...

compose-up:
	docker compose up -d postgres redis

compose-down:
	docker compose down

migrate-up:
	go run ./cmd/telegram-companion migrate-up

run-backend:
	go run ./cmd/telegram-companion serve

tidy:
	go mod tidy
```

- [ ] **Step 5: Create Docker Compose**

Create `docker-compose.yml`:

```yaml
services:
  postgres:
    image: postgres:16-alpine
    environment:
      POSTGRES_USER: telegram
      POSTGRES_PASSWORD: telegram
      POSTGRES_DB: telegram_companion
    ports:
      - "5432:5432"
    volumes:
      - postgres_data:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U telegram -d telegram_companion"]
      interval: 5s
      timeout: 3s
      retries: 20

  redis:
    image: redis:7-alpine
    ports:
      - "6379:6379"
    healthcheck:
      test: ["CMD", "redis-cli", "ping"]
      interval: 5s
      timeout: 3s
      retries: 20

volumes:
  postgres_data:
```

- [ ] **Step 6: Create Dockerfile**

Create `Dockerfile`:

```dockerfile
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o /out/telegram-companion ./cmd/telegram-companion

FROM alpine:3.22
RUN adduser -D -H appuser
USER appuser
WORKDIR /app
COPY --from=build /out/telegram-companion /app/telegram-companion
ENTRYPOINT ["/app/telegram-companion"]
```

- [ ] **Step 7: Create golangci-lint config**

Create `.golangci.yml`:

```yaml
version: "2"
run:
  timeout: 5m
linters:
  enable:
    - errcheck
    - govet
    - ineffassign
    - staticcheck
    - unused
    - revive
    - gosec
issues:
  exclude-rules:
    - linters:
        - gosec
      text: "G404"
```

- [ ] **Step 8: Create README**

Create `README.md`:

```markdown
# Telegram Companion

macOS-first desktop app for managing multiple Telegram user accounts, channels, keyword rules, public replies, and keyword-triggered private messages.

## Safety

- Only explicitly added channels/groups are observed.
- Hard outgoing limits are enforced: max 19 messages/min and at least 2 seconds between outgoing messages.
- Proxy fallback to direct connection is never silent.
- Session data, tdata, auth keys, proxy passwords, and `.env` files must not be committed.

## Local Infrastructure

```bash
cp .env.example .env
make compose-up
make migrate-up
make test
```

## Architecture

The backend follows Clean Architecture:

- `internal/domain`: entities and ports
- `internal/usecase`: business workflows
- `internal/repository`: PostgreSQL and Redis adapters
- `internal/telegram`: Telegram gateways
- `internal/transport`: Wails bindings
- `internal/app`: dependency wiring and lifecycle
```

- [ ] **Step 9: Verify skeleton**

Run: `go mod tidy && make test`

Expected: `go mod tidy` succeeds; `make test` prints no packages yet or passes once packages are added.

- [ ] **Step 10: Commit**

```bash
git add go.mod .gitignore .env.example configs/app.example.env Makefile Dockerfile docker-compose.yml .golangci.yml README.md
git commit -m "chore: add project skeleton and tooling"
```

---

### Task 2: Configuration and Logging

**Files:**
- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`
- Create: `internal/logger/logger.go`
- Create: `internal/logger/logger_test.go`
- Create: `cmd/telegram-companion/main.go`

**Interfaces:**
- Consumes: `.env` variables from Task 1.
- Produces:
  - `type Config struct`
  - `func Load(ctx context.Context, path string) (Config, error)`
  - `func New(level string) (*slog.Logger, error)`

- [ ] **Step 1: Write config tests**

Create `internal/config/config_test.go`:

```go
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
```

- [ ] **Step 2: Run config tests and see failure**

Run: `go test ./internal/config -run TestLoad -v`

Expected: FAIL because package does not exist or `Load` is undefined.

- [ ] **Step 3: Implement config loader**

Create `internal/config/config.go`:

```go
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
	GlobalMinInterval  time.Duration `env:"RATE_GLOBAL_MIN_INTERVAL" envDefault:"2s"`
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
	if c.Rate.AccountMaxPerMinute > 19 {
		return errors.New("RATE_ACCOUNT_MAX_PER_MINUTE must be <= 19")
	}
	if c.Rate.GlobalMinInterval < 2*time.Second {
		return errors.New("RATE_GLOBAL_MIN_INTERVAL must be >= 2s")
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
```

- [ ] **Step 4: Write logger tests**

Create `internal/logger/logger_test.go`:

```go
package logger

import "testing"

func TestNewAcceptsKnownLevels(t *testing.T) {
	for _, level := range []string{"debug", "info", "warn", "error"} {
		if _, err := New(level); err != nil {
			t.Fatalf("New(%q) returned error: %v", level, err)
		}
	}
}

func TestNewRejectsUnknownLevel(t *testing.T) {
	if _, err := New("verbose"); err == nil {
		t.Fatal("New succeeded, want error")
	}
}
```

- [ ] **Step 5: Implement logger**

Create `internal/logger/logger.go`:

```go
package logger

import (
	"fmt"
	"log/slog"
	"os"
)

func New(level string) (*slog.Logger, error) {
	var slogLevel slog.Level
	switch level {
	case "debug":
		slogLevel = slog.LevelDebug
	case "info":
		slogLevel = slog.LevelInfo
	case "warn":
		slogLevel = slog.LevelWarn
	case "error":
		slogLevel = slog.LevelError
	default:
		return nil, fmt.Errorf("unsupported log level %q", level)
	}

	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slogLevel})
	return slog.New(handler), nil
}
```

- [ ] **Step 6: Create CLI entrypoint**

Create `cmd/telegram-companion/main.go`:

```go
package main

import (
	"context"
	"fmt"
	"os"

	"telegram-companion/internal/config"
	"telegram-companion/internal/logger"
)

func main() {
	ctx := context.Background()
	cfg, err := config.Load(ctx, ".env")
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}

	log, err := logger.New(cfg.LogLevel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create logger: %v\n", err)
		os.Exit(1)
	}

	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}

	log.Info("telegram companion command", "cmd", cmd, "env", cfg.Env, "locale", cfg.Locale)
}
```

- [ ] **Step 7: Run tests**

Run: `go test ./internal/config ./internal/logger -v`

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/config internal/logger cmd/telegram-companion go.mod go.sum
git commit -m "feat: add config and structured logging"
```

---

### Task 3: Domain Model and Validation

**Files:**
- Create: `internal/domain/types.go`
- Create: `internal/domain/account.go`
- Create: `internal/domain/channel.go`
- Create: `internal/domain/rule.go`
- Create: `internal/domain/job.go`
- Create: `internal/domain/ports.go`
- Create: `internal/domain/rule_test.go`
- Create: `internal/domain/round_robin_test.go`

**Interfaces:**
- Consumes: none.
- Produces:
  - domain status constants
  - `KeywordRule.Validate() error`
  - repository and gateway ports used by use cases

- [ ] **Step 1: Write rule validation tests**

Create `internal/domain/rule_test.go`:

```go
package domain

import "testing"

func TestKeywordRuleValidateRequiresKeyword(t *testing.T) {
	rule := KeywordRule{Enabled: true, ActionMode: ActionPublicReply, PublicReplyText: "ok"}
	if err := rule.Validate(); err == nil {
		t.Fatal("Validate succeeded, want error")
	}
}

func TestKeywordRuleValidateRequiresPublicText(t *testing.T) {
	rule := KeywordRule{Keyword: "hello", Enabled: true, ActionMode: ActionPublicReply}
	if err := rule.Validate(); err == nil {
		t.Fatal("Validate succeeded, want error")
	}
}

func TestKeywordRuleValidateRequiresPrivateText(t *testing.T) {
	rule := KeywordRule{Keyword: "hello", Enabled: true, ActionMode: ActionPrivateMessage}
	if err := rule.Validate(); err == nil {
		t.Fatal("Validate succeeded, want error")
	}
}

func TestKeywordRuleValidateBothActions(t *testing.T) {
	rule := KeywordRule{
		Keyword:            "hello",
		Enabled:            true,
		ActionMode:         ActionBoth,
		PublicReplyText:    "chat",
		PrivateMessageText: "dm",
	}
	if err := rule.Validate(); err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
}
```

- [ ] **Step 2: Create domain types**

Create `internal/domain/types.go`:

```go
package domain

import "time"

type ID string

type ChannelStatus string

const (
	ChannelReady     ChannelStatus = "ready"
	ChannelJoining   ChannelStatus = "joining"
	ChannelPartial   ChannelStatus = "partial"
	ChannelError     ChannelStatus = "error"
	ChannelFloodWait ChannelStatus = "flood_wait"
	ChannelPaused    ChannelStatus = "paused"
)

type AccountStatus string

const (
	AccountActive    AccountStatus = "active"
	AccountPaused    AccountStatus = "paused"
	AccountError     AccountStatus = "error"
	AccountLimited   AccountStatus = "limited"
	AccountFloodWait AccountStatus = "flood_wait"
)

type JobType string

const (
	JobPublicReply    JobType = "public_reply"
	JobPrivateMessage JobType = "private_message"
)

type ActionMode string

const (
	ActionPublicReply    ActionMode = "public_reply"
	ActionPrivateMessage ActionMode = "private_message"
	ActionBoth           ActionMode = "both"
)

type ProxyMode string

const (
	ProxyModeAssigned ProxyMode = "assigned"
	ProxyModeGlobal   ProxyMode = "global"
	ProxyModeDirect   ProxyMode = "direct"
)

type Clock interface {
	Now() time.Time
}
```

- [ ] **Step 3: Create account entity**

Create `internal/domain/account.go`:

```go
package domain

import "time"

type Account struct {
	ID                 ID
	DisplayName        string
	Username           string
	PhoneMasked        string
	Status             AccountStatus
	ProxyProfileID     *ID
	ProxyMode          ProxyMode
	PublicRepliesSent  int64
	PrivateMessagesSent int64
	LastActivityAt     *time.Time
	LastError          string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

func (a Account) Eligible() bool {
	return a.Status == AccountActive
}

type ProxyProfile struct {
	ID              ID
	Name            string
	Protocol        string
	Host            string
	Port            int
	Username        string
	PasswordSecret  string
	Enabled         bool
	LastHealthStatus string
	LastHealthAt     *time.Time
	LastError        string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}
```

- [ ] **Step 4: Create channel entity**

Create `internal/domain/channel.go`:

```go
package domain

import "time"

type ChannelType string

const (
	ChannelTypeChannel    ChannelType = "channel"
	ChannelTypeGroup      ChannelType = "group"
	ChannelTypeSupergroup ChannelType = "supergroup"
	ChannelTypeDiscussion ChannelType = "discussion"
)

type Channel struct {
	ID              ID
	TelegramID      string
	Title           string
	Link            string
	Username        string
	Type            ChannelType
	Status          ChannelStatus
	Active          bool
	SentCount       int64
	LastActivityAt  *time.Time
	LastError       string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type ChannelMembership struct {
	AccountID  ID
	ChannelID  ID
	IsMember   bool
	Status     string
	LastCheckAt *time.Time
	LastError  string
}
```

- [ ] **Step 5: Create rule entity**

Create `internal/domain/rule.go`:

```go
package domain

import (
	"errors"
	"strings"
)

type KeywordRule struct {
	ID                 ID
	Keyword            string
	Enabled            bool
	ActionMode         ActionMode
	PublicReplyText    string
	PrivateMessageText string
}

func (r KeywordRule) Validate() error {
	if strings.TrimSpace(r.Keyword) == "" {
		return errors.New("keyword is required")
	}
	if !r.Enabled {
		return nil
	}
	switch r.ActionMode {
	case ActionPublicReply:
		if strings.TrimSpace(r.PublicReplyText) == "" {
			return errors.New("public reply text is required")
		}
	case ActionPrivateMessage:
		if strings.TrimSpace(r.PrivateMessageText) == "" {
			return errors.New("private message text is required")
		}
	case ActionBoth:
		if strings.TrimSpace(r.PublicReplyText) == "" {
			return errors.New("public reply text is required")
		}
		if strings.TrimSpace(r.PrivateMessageText) == "" {
			return errors.New("private message text is required")
		}
	default:
		return errors.New("unsupported action mode")
	}
	return nil
}

func (r KeywordRule) Matches(text string) bool {
	if !r.Enabled {
		return false
	}
	return strings.Contains(strings.ToLower(text), strings.ToLower(strings.TrimSpace(r.Keyword)))
}
```

- [ ] **Step 6: Create job entity**

Create `internal/domain/job.go`:

```go
package domain

import "time"

type IncomingMessageEvent struct {
	ID                ID
	ChannelID         ID
	TelegramMessageID string
	SenderTelegramID  string
	Text              string
	ReceivedAt        time.Time
}

type OutgoingMessageJob struct {
	ID                ID
	Type              JobType
	AccountID         *ID
	ChannelID         ID
	RuleID            ID
	TargetTelegramID  string
	ReplyToMessageID  string
	Text              string
	Status            string
	Attempts          int
	NextAttemptAt     time.Time
	CreatedAt         time.Time
}

type OutgoingMessageEvent struct {
	ID        ID
	JobID     ID
	AccountID ID
	ChannelID ID
	Type      JobType
	Success   bool
	ErrorCode string
	CreatedAt time.Time
}
```

- [ ] **Step 7: Create ports**

Create `internal/domain/ports.go`:

```go
package domain

import "context"

type AccountRepository interface {
	ListActive(ctx context.Context) ([]Account, error)
	List(ctx context.Context) ([]Account, error)
	Save(ctx context.Context, account Account) error
}

type ChannelRepository interface {
	List(ctx context.Context) ([]Channel, error)
	ListActive(ctx context.Context) ([]Channel, error)
	Save(ctx context.Context, channel Channel) error
	SaveMembership(ctx context.Context, membership ChannelMembership) error
}

type RuleRepository interface {
	ListEnabled(ctx context.Context) ([]KeywordRule, error)
	Save(ctx context.Context, rule KeywordRule) error
}

type JobRepository interface {
	Enqueue(ctx context.Context, job OutgoingMessageJob) error
	NextDue(ctx context.Context) (*OutgoingMessageJob, error)
	MarkDone(ctx context.Context, jobID ID, event OutgoingMessageEvent) error
	Delay(ctx context.Context, jobID ID, reason string) error
}

type StatsRepository interface {
	AccountStats(ctx context.Context) ([]Account, error)
}

type TelegramGateway interface {
	ResolveChannel(ctx context.Context, link string) (Channel, error)
	CheckMembership(ctx context.Context, account Account, channel Channel) (ChannelMembership, error)
	JoinChannel(ctx context.Context, account Account, channel Channel) (ChannelMembership, error)
	SendPublicReply(ctx context.Context, account Account, job OutgoingMessageJob) error
	SendPrivateMessage(ctx context.Context, account Account, job OutgoingMessageJob) error
}
```

- [ ] **Step 8: Run domain tests**

Run: `go test ./internal/domain -v`

Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/domain
git commit -m "feat: add core domain model"
```

---

### Task 4: Round-Robin Selector and Rate Limiter

**Files:**
- Create: `internal/usecase/selector.go`
- Create: `internal/usecase/selector_test.go`
- Create: `internal/usecase/ratelimit.go`
- Create: `internal/usecase/ratelimit_test.go`

**Interfaces:**
- Consumes: `domain.Account`.
- Produces:
  - `func NewRoundRobinSelector() *RoundRobinSelector`
  - `func (s *RoundRobinSelector) Next(accounts []domain.Account) (*domain.Account, error)`
  - `func NewMemoryLimiter(interval time.Duration, maxPerMinute int) *MemoryLimiter`
  - `func (l *MemoryLimiter) Allow(accountID domain.ID, now time.Time) bool`

- [ ] **Step 1: Write selector tests**

Create `internal/usecase/selector_test.go`:

```go
package usecase

import (
	"testing"

	"telegram-companion/internal/domain"
)

func TestRoundRobinSelectorSkipsIneligible(t *testing.T) {
	selector := NewRoundRobinSelector()
	accounts := []domain.Account{
		{ID: "a1", Status: domain.AccountPaused},
		{ID: "a2", Status: domain.AccountActive},
		{ID: "a3", Status: domain.AccountActive},
	}

	first, err := selector.Next(accounts)
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	second, err := selector.Next(accounts)
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}

	if first.ID != "a2" || second.ID != "a3" {
		t.Fatalf("round-robin = %s,%s; want a2,a3", first.ID, second.ID)
	}
}
```

- [ ] **Step 2: Implement selector**

Create `internal/usecase/selector.go`:

```go
package usecase

import (
	"errors"
	"sync"

	"telegram-companion/internal/domain"
)

type RoundRobinSelector struct {
	mu   sync.Mutex
	next int
}

func NewRoundRobinSelector() *RoundRobinSelector {
	return &RoundRobinSelector{}
}

func (s *RoundRobinSelector) Next(accounts []domain.Account) (*domain.Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	eligible := make([]domain.Account, 0, len(accounts))
	for _, account := range accounts {
		if account.Eligible() {
			eligible = append(eligible, account)
		}
	}
	if len(eligible) == 0 {
		return nil, errors.New("no eligible accounts")
	}

	account := eligible[s.next%len(eligible)]
	s.next++
	return &account, nil
}
```

- [ ] **Step 3: Write limiter tests**

Create `internal/usecase/ratelimit_test.go`:

```go
package usecase

import (
	"testing"
	"time"

	"telegram-companion/internal/domain"
)

func TestMemoryLimiterEnforcesGlobalInterval(t *testing.T) {
	limiter := NewMemoryLimiter(2*time.Second, 19)
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)

	if !limiter.Allow(domain.ID("a1"), now) {
		t.Fatal("first send denied")
	}
	if limiter.Allow(domain.ID("a2"), now.Add(time.Second)) {
		t.Fatal("second send within 2s allowed")
	}
	if !limiter.Allow(domain.ID("a2"), now.Add(2*time.Second)) {
		t.Fatal("send after 2s denied")
	}
}

func TestMemoryLimiterEnforcesPerAccountMinute(t *testing.T) {
	limiter := NewMemoryLimiter(0, 2)
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)

	if !limiter.Allow("a1", now) || !limiter.Allow("a1", now.Add(time.Second)) {
		t.Fatal("first two sends should be allowed")
	}
	if limiter.Allow("a1", now.Add(2*time.Second)) {
		t.Fatal("third send in one minute allowed")
	}
	if !limiter.Allow("a1", now.Add(61*time.Second)) {
		t.Fatal("send after window denied")
	}
}
```

- [ ] **Step 4: Implement limiter**

Create `internal/usecase/ratelimit.go`:

```go
package usecase

import (
	"sync"
	"time"

	"telegram-companion/internal/domain"
)

type MemoryLimiter struct {
	mu           sync.Mutex
	interval     time.Duration
	maxPerMinute int
	lastGlobal   time.Time
	perAccount   map[domain.ID][]time.Time
}

func NewMemoryLimiter(interval time.Duration, maxPerMinute int) *MemoryLimiter {
	return &MemoryLimiter{
		interval:     interval,
		maxPerMinute: maxPerMinute,
		perAccount:   make(map[domain.ID][]time.Time),
	}
}

func (l *MemoryLimiter) Allow(accountID domain.ID, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	if !l.lastGlobal.IsZero() && now.Sub(l.lastGlobal) < l.interval {
		return false
	}

	windowStart := now.Add(-time.Minute)
	history := l.perAccount[accountID]
	kept := history[:0]
	for _, ts := range history {
		if ts.After(windowStart) {
			kept = append(kept, ts)
		}
	}
	if len(kept) >= l.maxPerMinute {
		l.perAccount[accountID] = kept
		return false
	}

	kept = append(kept, now)
	l.perAccount[accountID] = kept
	l.lastGlobal = now
	return true
}
```

- [ ] **Step 5: Run tests**

Run: `go test ./internal/usecase -run 'TestRoundRobin|TestMemoryLimiter' -v`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/usecase
git commit -m "feat: add account selector and rate limiter"
```

---

### Task 5: Database Schema

**Files:**
- Create: `migrations/000001_init.sql`
- Create: `internal/repository/postgres/schema_test.go`

**Interfaces:**
- Consumes: domain entities.
- Produces: durable tables matching domain model.

- [ ] **Step 1: Create migration**

Create `migrations/000001_init.sql`:

```sql
-- +goose Up
CREATE TABLE proxy_profiles (
    id UUID PRIMARY KEY,
    name TEXT NOT NULL,
    protocol TEXT NOT NULL CHECK (protocol IN ('socks5', 'http')),
    host TEXT NOT NULL,
    port INTEGER NOT NULL CHECK (port > 0),
    username TEXT NOT NULL DEFAULT '',
    password_secret TEXT NOT NULL DEFAULT '',
    enabled BOOLEAN NOT NULL DEFAULT true,
    last_health_status TEXT NOT NULL DEFAULT 'unknown',
    last_health_at TIMESTAMPTZ,
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE accounts (
    id UUID PRIMARY KEY,
    display_name TEXT NOT NULL DEFAULT '',
    username TEXT NOT NULL DEFAULT '',
    phone_masked TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL,
    proxy_profile_id UUID REFERENCES proxy_profiles(id),
    proxy_mode TEXT NOT NULL CHECK (proxy_mode IN ('assigned', 'global', 'direct')),
    public_replies_sent BIGINT NOT NULL DEFAULT 0,
    private_messages_sent BIGINT NOT NULL DEFAULT 0,
    last_activity_at TIMESTAMPTZ,
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE account_sessions (
    account_id UUID PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
    session_path TEXT NOT NULL,
    imported_from TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE channels (
    id UUID PRIMARY KEY,
    telegram_id TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL,
    link TEXT NOT NULL UNIQUE,
    username TEXT NOT NULL DEFAULT '',
    type TEXT NOT NULL,
    status TEXT NOT NULL,
    active BOOLEAN NOT NULL DEFAULT true,
    sent_count BIGINT NOT NULL DEFAULT 0,
    last_activity_at TIMESTAMPTZ,
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE channel_memberships (
    account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    channel_id UUID NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    is_member BOOLEAN NOT NULL DEFAULT false,
    status TEXT NOT NULL DEFAULT 'unknown',
    last_check_at TIMESTAMPTZ,
    last_error TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (account_id, channel_id)
);

CREATE TABLE keyword_rules (
    id UUID PRIMARY KEY,
    keyword TEXT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT true,
    action_mode TEXT NOT NULL CHECK (action_mode IN ('public_reply', 'private_message', 'both')),
    public_reply_text TEXT NOT NULL DEFAULT '',
    private_message_text TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE incoming_message_events (
    id UUID PRIMARY KEY,
    channel_id UUID NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    telegram_message_id TEXT NOT NULL,
    sender_telegram_id TEXT NOT NULL,
    text_hash TEXT NOT NULL DEFAULT '',
    received_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE outgoing_message_jobs (
    id UUID PRIMARY KEY,
    type TEXT NOT NULL CHECK (type IN ('public_reply', 'private_message')),
    account_id UUID REFERENCES accounts(id),
    channel_id UUID NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    rule_id UUID NOT NULL REFERENCES keyword_rules(id) ON DELETE CASCADE,
    target_telegram_id TEXT NOT NULL,
    reply_to_message_id TEXT NOT NULL DEFAULT '',
    text TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'queued',
    attempts INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE outgoing_message_events (
    id UUID PRIMARY KEY,
    job_id UUID NOT NULL REFERENCES outgoing_message_jobs(id) ON DELETE CASCADE,
    account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    channel_id UUID NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    type TEXT NOT NULL,
    success BOOLEAN NOT NULL,
    error_code TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE app_settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_channels_active ON channels(active);
CREATE INDEX idx_jobs_due ON outgoing_message_jobs(status, next_attempt_at);
CREATE INDEX idx_events_account ON outgoing_message_events(account_id, created_at);
CREATE INDEX idx_events_channel ON outgoing_message_events(channel_id, created_at);

-- +goose Down
DROP TABLE IF EXISTS app_settings;
DROP TABLE IF EXISTS outgoing_message_events;
DROP TABLE IF EXISTS outgoing_message_jobs;
DROP TABLE IF EXISTS incoming_message_events;
DROP TABLE IF EXISTS keyword_rules;
DROP TABLE IF EXISTS channel_memberships;
DROP TABLE IF EXISTS channels;
DROP TABLE IF EXISTS account_sessions;
DROP TABLE IF EXISTS accounts;
DROP TABLE IF EXISTS proxy_profiles;
```

- [ ] **Step 2: Write migration smoke test**

Create `internal/repository/postgres/schema_test.go`:

```go
package postgres

import (
	"os"
	"strings"
	"testing"
)

func TestInitialMigrationContainsRequiredTables(t *testing.T) {
	content, err := os.ReadFile("../../../migrations/000001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(content)
	for _, table := range []string{
		"accounts",
		"proxy_profiles",
		"channels",
		"channel_memberships",
		"keyword_rules",
		"outgoing_message_jobs",
		"outgoing_message_events",
	} {
		if !strings.Contains(sql, "CREATE TABLE "+table) {
			t.Fatalf("migration missing table %s", table)
		}
	}
}
```

- [ ] **Step 3: Run migration smoke test**

Run: `go test ./internal/repository/postgres -run TestInitialMigrationContainsRequiredTables -v`

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add migrations internal/repository/postgres
git commit -m "feat: add initial database schema"
```

---

### Task 6: Keyword Processing Use Case

**Files:**
- Create: `internal/usecase/keywords.go`
- Create: `internal/usecase/keywords_test.go`

**Interfaces:**
- Consumes: `domain.KeywordRule`, `domain.IncomingMessageEvent`.
- Produces:
  - `func NewKeywordProcessor(ruleRepo domain.RuleRepository, jobRepo domain.JobRepository) *KeywordProcessor`
  - `func (p *KeywordProcessor) Process(ctx context.Context, event domain.IncomingMessageEvent) error`

- [ ] **Step 1: Write keyword processor test**

Create `internal/usecase/keywords_test.go`:

```go
package usecase

import (
	"context"
	"testing"
	"time"

	"telegram-companion/internal/domain"
)

type ruleRepoStub struct{ rules []domain.KeywordRule }

func (r ruleRepoStub) ListEnabled(context.Context) ([]domain.KeywordRule, error) { return r.rules, nil }
func (r ruleRepoStub) Save(context.Context, domain.KeywordRule) error { return nil }

type jobRepoStub struct{ jobs []domain.OutgoingMessageJob }

func (j *jobRepoStub) Enqueue(_ context.Context, job domain.OutgoingMessageJob) error {
	j.jobs = append(j.jobs, job)
	return nil
}
func (j *jobRepoStub) NextDue(context.Context) (*domain.OutgoingMessageJob, error) { return nil, nil }
func (j *jobRepoStub) MarkDone(context.Context, domain.ID, domain.OutgoingMessageEvent) error { return nil }
func (j *jobRepoStub) Delay(context.Context, domain.ID, string) error { return nil }

func TestKeywordProcessorCreatesPublicAndPrivateJobs(t *testing.T) {
	jobs := &jobRepoStub{}
	processor := NewKeywordProcessor(ruleRepoStub{rules: []domain.KeywordRule{{
		ID:                 "rule-1",
		Keyword:            "привет",
		Enabled:            true,
		ActionMode:         domain.ActionBoth,
		PublicReplyText:    "Привет в чат",
		PrivateMessageText: "Привет в личку",
	}}}, jobs)

	err := processor.Process(context.Background(), domain.IncomingMessageEvent{
		ID: "event-1", ChannelID: "channel-1", SenderTelegramID: "user-1", TelegramMessageID: "msg-1", Text: "ну привет", ReceivedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("Process returned error: %v", err)
	}
	if len(jobs.jobs) != 2 {
		t.Fatalf("jobs = %d, want 2", len(jobs.jobs))
	}
	if jobs.jobs[0].Type != domain.JobPublicReply || jobs.jobs[1].Type != domain.JobPrivateMessage {
		t.Fatalf("job types = %s,%s", jobs.jobs[0].Type, jobs.jobs[1].Type)
	}
}
```

- [ ] **Step 2: Implement keyword processor**

Create `internal/usecase/keywords.go`:

```go
package usecase

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"

	"telegram-companion/internal/domain"
)

type KeywordProcessor struct {
	rules domain.RuleRepository
	jobs  domain.JobRepository
}

func NewKeywordProcessor(ruleRepo domain.RuleRepository, jobRepo domain.JobRepository) *KeywordProcessor {
	return &KeywordProcessor{rules: ruleRepo, jobs: jobRepo}
}

func (p *KeywordProcessor) Process(ctx context.Context, event domain.IncomingMessageEvent) error {
	rules, err := p.rules.ListEnabled(ctx)
	if err != nil {
		return err
	}
	for _, rule := range rules {
		if !rule.Matches(event.Text) {
			continue
		}
		if rule.ActionMode == domain.ActionPublicReply || rule.ActionMode == domain.ActionBoth {
			if err := p.jobs.Enqueue(ctx, newJob(domain.JobPublicReply, event, rule, rule.PublicReplyText)); err != nil {
				return err
			}
		}
		if rule.ActionMode == domain.ActionPrivateMessage || rule.ActionMode == domain.ActionBoth {
			if err := p.jobs.Enqueue(ctx, newJob(domain.JobPrivateMessage, event, rule, rule.PrivateMessageText)); err != nil {
				return err
			}
		}
	}
	return nil
}

func newJob(jobType domain.JobType, event domain.IncomingMessageEvent, rule domain.KeywordRule, text string) domain.OutgoingMessageJob {
	return domain.OutgoingMessageJob{
		ID:               domain.ID(randomID()),
		Type:             jobType,
		ChannelID:        event.ChannelID,
		RuleID:           rule.ID,
		TargetTelegramID: event.SenderTelegramID,
		ReplyToMessageID: event.TelegramMessageID,
		Text:             text,
		Status:           "queued",
		NextAttemptAt:    time.Now().UTC(),
		CreatedAt:        time.Now().UTC(),
	}
}

func randomID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return hex.EncodeToString([]byte(time.Now().Format(time.RFC3339Nano)))
	}
	return hex.EncodeToString(b[:])
}
```

- [ ] **Step 3: Run tests**

Run: `go test ./internal/usecase -run TestKeywordProcessor -v`

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/usecase/keywords.go internal/usecase/keywords_test.go
git commit -m "feat: enqueue keyword-triggered jobs"
```

---

### Task 7: Fake Telegram Gateway and Channel Import Use Case

**Files:**
- Create: `internal/telegram/fake/gateway.go`
- Create: `internal/usecase/channels.go`
- Create: `internal/usecase/channels_test.go`

**Interfaces:**
- Consumes: `domain.TelegramGateway`, `domain.ChannelRepository`, `domain.AccountRepository`.
- Produces:
  - `func NewChannelImporter(accounts domain.AccountRepository, channels domain.ChannelRepository, telegram domain.TelegramGateway) *ChannelImporter`
  - `func (i *ChannelImporter) ImportLinks(ctx context.Context, links []string) error`

- [ ] **Step 1: Create fake Telegram gateway**

Create `internal/telegram/fake/gateway.go`:

```go
package fake

import (
	"context"
	"strings"
	"time"

	"telegram-companion/internal/domain"
)

type Gateway struct{}

func NewGateway() *Gateway { return &Gateway{} }

func (g *Gateway) ResolveChannel(_ context.Context, link string) (domain.Channel, error) {
	normalized := strings.TrimSpace(link)
	title := strings.TrimPrefix(normalized, "https://t.me/")
	title = strings.TrimPrefix(title, "@")
	now := time.Now().UTC()
	return domain.Channel{
		ID:        domain.ID("channel-" + title),
		Title:     title,
		Link:      normalized,
		Username:  title,
		Type:      domain.ChannelTypeChannel,
		Status:    domain.ChannelReady,
		Active:    true,
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

func (g *Gateway) CheckMembership(_ context.Context, account domain.Account, channel domain.Channel) (domain.ChannelMembership, error) {
	now := time.Now().UTC()
	return domain.ChannelMembership{AccountID: account.ID, ChannelID: channel.ID, IsMember: false, Status: "not_member", LastCheckAt: &now}, nil
}

func (g *Gateway) JoinChannel(_ context.Context, account domain.Account, channel domain.Channel) (domain.ChannelMembership, error) {
	now := time.Now().UTC()
	return domain.ChannelMembership{AccountID: account.ID, ChannelID: channel.ID, IsMember: true, Status: "member", LastCheckAt: &now}, nil
}

func (g *Gateway) SendPublicReply(context.Context, domain.Account, domain.OutgoingMessageJob) error {
	return nil
}

func (g *Gateway) SendPrivateMessage(context.Context, domain.Account, domain.OutgoingMessageJob) error {
	return nil
}
```

- [ ] **Step 2: Write channel importer test**

Create `internal/usecase/channels_test.go`:

```go
package usecase

import (
	"context"
	"testing"

	"telegram-companion/internal/domain"
	"telegram-companion/internal/telegram/fake"
)

type accountRepoStub struct{ accounts []domain.Account }

func (r accountRepoStub) ListActive(context.Context) ([]domain.Account, error) { return r.accounts, nil }
func (r accountRepoStub) List(context.Context) ([]domain.Account, error) { return r.accounts, nil }
func (r accountRepoStub) Save(context.Context, domain.Account) error { return nil }

type channelRepoStub struct {
	channels    []domain.Channel
	memberships []domain.ChannelMembership
}

func (r *channelRepoStub) List(context.Context) ([]domain.Channel, error) { return r.channels, nil }
func (r *channelRepoStub) ListActive(context.Context) ([]domain.Channel, error) { return r.channels, nil }
func (r *channelRepoStub) Save(_ context.Context, channel domain.Channel) error {
	r.channels = append(r.channels, channel)
	return nil
}
func (r *channelRepoStub) SaveMembership(_ context.Context, membership domain.ChannelMembership) error {
	r.memberships = append(r.memberships, membership)
	return nil
}

func TestChannelImporterJoinsAllAccounts(t *testing.T) {
	channels := &channelRepoStub{}
	importer := NewChannelImporter(
		accountRepoStub{accounts: []domain.Account{{ID: "a1", Status: domain.AccountActive}, {ID: "a2", Status: domain.AccountActive}}},
		channels,
		fake.NewGateway(),
	)

	err := importer.ImportLinks(context.Background(), []string{"https://t.me/test", "@test"})
	if err != nil {
		t.Fatalf("ImportLinks returned error: %v", err)
	}
	if len(channels.channels) != 1 {
		t.Fatalf("channels = %d, want 1", len(channels.channels))
	}
	if len(channels.memberships) != 2 {
		t.Fatalf("memberships = %d, want 2", len(channels.memberships))
	}
}
```

- [ ] **Step 3: Implement channel importer**

Create `internal/usecase/channels.go`:

```go
package usecase

import (
	"context"
	"strings"

	"telegram-companion/internal/domain"
)

type ChannelImporter struct {
	accounts domain.AccountRepository
	channels domain.ChannelRepository
	telegram domain.TelegramGateway
}

func NewChannelImporter(accounts domain.AccountRepository, channels domain.ChannelRepository, telegram domain.TelegramGateway) *ChannelImporter {
	return &ChannelImporter{accounts: accounts, channels: channels, telegram: telegram}
}

func (i *ChannelImporter) ImportLinks(ctx context.Context, links []string) error {
	accounts, err := i.accounts.ListActive(ctx)
	if err != nil {
		return err
	}
	for _, link := range uniqueLinks(links) {
		channel, err := i.telegram.ResolveChannel(ctx, link)
		if err != nil {
			return err
		}
		if err := i.channels.Save(ctx, channel); err != nil {
			return err
		}
		for _, account := range accounts {
			membership, err := i.telegram.CheckMembership(ctx, account, channel)
			if err != nil {
				return err
			}
			if !membership.IsMember {
				membership, err = i.telegram.JoinChannel(ctx, account, channel)
				if err != nil {
					return err
				}
			}
			if err := i.channels.SaveMembership(ctx, membership); err != nil {
				return err
			}
		}
	}
	return nil
}

func uniqueLinks(links []string) []string {
	seen := make(map[string]struct{}, len(links))
	out := make([]string, 0, len(links))
	for _, link := range links {
		normalized := normalizeLink(link)
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	return out
}

func normalizeLink(link string) string {
	link = strings.TrimSpace(link)
	link = strings.TrimSuffix(link, "/")
	if strings.HasPrefix(link, "@") {
		return "https://t.me/" + strings.TrimPrefix(link, "@")
	}
	return link
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/usecase ./internal/telegram/fake -run 'TestChannelImporter|Test' -v`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/usecase/channels.go internal/usecase/channels_test.go internal/telegram/fake
git commit -m "feat: add fake telegram channel import flow"
```

---

### Task 8: Scheduler Use Case

**Files:**
- Create: `internal/usecase/scheduler.go`
- Create: `internal/usecase/scheduler_test.go`

**Interfaces:**
- Consumes: repositories, selector, limiter, TelegramGateway.
- Produces:
  - `func NewScheduler(accounts domain.AccountRepository, jobs domain.JobRepository, telegram domain.TelegramGateway, selector *RoundRobinSelector, limiter *MemoryLimiter) *Scheduler`
  - `func (s *Scheduler) RunOnce(ctx context.Context, now time.Time) error`

- [ ] **Step 1: Write scheduler test**

Create `internal/usecase/scheduler_test.go`:

```go
package usecase

import (
	"context"
	"testing"
	"time"

	"telegram-companion/internal/domain"
	"telegram-companion/internal/telegram/fake"
)

type oneJobRepo struct {
	job    *domain.OutgoingMessageJob
	done   bool
	delays int
}

func (r *oneJobRepo) Enqueue(context.Context, domain.OutgoingMessageJob) error { return nil }
func (r *oneJobRepo) NextDue(context.Context) (*domain.OutgoingMessageJob, error) { return r.job, nil }
func (r *oneJobRepo) MarkDone(_ context.Context, _ domain.ID, _ domain.OutgoingMessageEvent) error {
	r.done = true
	return nil
}
func (r *oneJobRepo) Delay(context.Context, domain.ID, string) error {
	r.delays++
	return nil
}

func TestSchedulerSendsDueJob(t *testing.T) {
	jobRepo := &oneJobRepo{job: &domain.OutgoingMessageJob{ID: "job-1", Type: domain.JobPublicReply, ChannelID: "c1", Text: "hello"}}
	scheduler := NewScheduler(
		accountRepoStub{accounts: []domain.Account{{ID: "a1", Status: domain.AccountActive}}},
		jobRepo,
		fake.NewGateway(),
		NewRoundRobinSelector(),
		NewMemoryLimiter(2*time.Second, 19),
	)

	if err := scheduler.RunOnce(context.Background(), time.Now()); err != nil {
		t.Fatalf("RunOnce returned error: %v", err)
	}
	if !jobRepo.done {
		t.Fatal("job was not marked done")
	}
}
```

- [ ] **Step 2: Implement scheduler**

Create `internal/usecase/scheduler.go`:

```go
package usecase

import (
	"context"
	"time"

	"telegram-companion/internal/domain"
)

type Scheduler struct {
	accounts domain.AccountRepository
	jobs     domain.JobRepository
	telegram domain.TelegramGateway
	selector *RoundRobinSelector
	limiter  *MemoryLimiter
}

func NewScheduler(accounts domain.AccountRepository, jobs domain.JobRepository, telegram domain.TelegramGateway, selector *RoundRobinSelector, limiter *MemoryLimiter) *Scheduler {
	return &Scheduler{accounts: accounts, jobs: jobs, telegram: telegram, selector: selector, limiter: limiter}
}

func (s *Scheduler) RunOnce(ctx context.Context, now time.Time) error {
	job, err := s.jobs.NextDue(ctx)
	if err != nil || job == nil {
		return err
	}

	accounts, err := s.accounts.ListActive(ctx)
	if err != nil {
		return err
	}
	account, err := s.selector.Next(accounts)
	if err != nil {
		return s.jobs.Delay(ctx, job.ID, "no_eligible_account")
	}
	if !s.limiter.Allow(account.ID, now) {
		return s.jobs.Delay(ctx, job.ID, "rate_limited")
	}

	switch job.Type {
	case domain.JobPublicReply:
		err = s.telegram.SendPublicReply(ctx, *account, *job)
	case domain.JobPrivateMessage:
		err = s.telegram.SendPrivateMessage(ctx, *account, *job)
	default:
		return s.jobs.Delay(ctx, job.ID, "unsupported_job_type")
	}
	if err != nil {
		return s.jobs.Delay(ctx, job.ID, "send_failed")
	}

	event := domain.OutgoingMessageEvent{
		ID:        domain.ID(randomID()),
		JobID:     job.ID,
		AccountID: account.ID,
		ChannelID: job.ChannelID,
		Type:      job.Type,
		Success:   true,
		CreatedAt: now.UTC(),
	}
	return s.jobs.MarkDone(ctx, job.ID, event)
}
```

- [ ] **Step 3: Run scheduler tests**

Run: `go test ./internal/usecase -run TestScheduler -v`

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/usecase/scheduler.go internal/usecase/scheduler_test.go
git commit -m "feat: add outgoing job scheduler"
```

---

### Task 9: Application Lifecycle and START/STOP State

**Files:**
- Create: `internal/usecase/automation.go`
- Create: `internal/usecase/automation_test.go`
- Create: `internal/app/app.go`

**Interfaces:**
- Consumes: scheduler and processor use cases.
- Produces:
  - `type AutomationController struct`
  - `func (c *AutomationController) Start(ctx context.Context) error`
  - `func (c *AutomationController) Stop(ctx context.Context) error`
  - `func (c *AutomationController) Running() bool`
  - `type App struct`

- [ ] **Step 1: Write automation controller test**

Create `internal/usecase/automation_test.go`:

```go
package usecase

import (
	"context"
	"testing"
)

func TestAutomationControllerStartStop(t *testing.T) {
	controller := NewAutomationController()
	if controller.Running() {
		t.Fatal("new controller is running")
	}
	if err := controller.Start(context.Background()); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if !controller.Running() {
		t.Fatal("controller did not start")
	}
	if err := controller.Stop(context.Background()); err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}
	if controller.Running() {
		t.Fatal("controller did not stop")
	}
}
```

- [ ] **Step 2: Implement automation controller**

Create `internal/usecase/automation.go`:

```go
package usecase

import (
	"context"
	"sync"
)

type AutomationController struct {
	mu      sync.RWMutex
	running bool
}

func NewAutomationController() *AutomationController {
	return &AutomationController{}
}

func (c *AutomationController) Start(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.running = true
	return nil
}

func (c *AutomationController) Stop(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.running = false
	return nil
}

func (c *AutomationController) Running() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.running
}
```

- [ ] **Step 3: Create app wiring**

Create `internal/app/app.go`:

```go
package app

import (
	"context"
	"log/slog"

	"telegram-companion/internal/config"
	"telegram-companion/internal/usecase"
)

type App struct {
	cfg        config.Config
	log        *slog.Logger
	automation *usecase.AutomationController
}

func New(cfg config.Config, log *slog.Logger) *App {
	return &App{
		cfg:        cfg,
		log:        log,
		automation: usecase.NewAutomationController(),
	}
}

func (a *App) Start(ctx context.Context) error {
	a.log.Info("app start", "locale", a.cfg.Locale)
	return nil
}

func (a *App) Shutdown(ctx context.Context) error {
	a.log.Info("app shutdown")
	return a.automation.Stop(ctx)
}

func (a *App) Automation() *usecase.AutomationController {
	return a.automation
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/usecase ./internal/app -run TestAutomation -v`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/usecase/automation.go internal/usecase/automation_test.go internal/app
git commit -m "feat: add automation lifecycle control"
```

---

### Task 10: Wails Bindings and Localized UI Shell

**Files:**
- Create: `internal/transport/wails/bindings.go`
- Create: `internal/transport/wails/bindings_test.go`
- Create: `frontend/package.json`
- Create: `frontend/index.html`
- Create: `frontend/src/main.tsx`
- Create: `frontend/src/App.tsx`
- Create: `frontend/src/i18n.ts`
- Create: `frontend/src/styles.css`

**Interfaces:**
- Consumes: `usecase.AutomationController`.
- Produces Wails-callable methods:
  - `StartAutomation(ctx context.Context) error`
  - `StopAutomation(ctx context.Context) error`
  - `GetDashboard(ctx context.Context) (DashboardDTO, error)`

- [ ] **Step 1: Write bindings test**

Create `internal/transport/wails/bindings_test.go`:

```go
package wails

import (
	"context"
	"testing"

	"telegram-companion/internal/usecase"
)

func TestBindingsStartStop(t *testing.T) {
	b := NewBindings(usecase.NewAutomationController())
	if err := b.StartAutomation(context.Background()); err != nil {
		t.Fatalf("StartAutomation returned error: %v", err)
	}
	dashboard, err := b.GetDashboard(context.Background())
	if err != nil {
		t.Fatalf("GetDashboard returned error: %v", err)
	}
	if !dashboard.Running {
		t.Fatal("dashboard running=false, want true")
	}
	if err := b.StopAutomation(context.Background()); err != nil {
		t.Fatalf("StopAutomation returned error: %v", err)
	}
}
```

- [ ] **Step 2: Implement bindings**

Create `internal/transport/wails/bindings.go`:

```go
package wails

import (
	"context"

	"telegram-companion/internal/usecase"
)

type Bindings struct {
	automation *usecase.AutomationController
}

type DashboardDTO struct {
	Running       bool   `json:"running"`
	Locale        string `json:"locale"`
	ChannelCount  int    `json:"channelCount"`
	AccountCount  int    `json:"accountCount"`
	LastStatus    string `json:"lastStatus"`
}

func NewBindings(automation *usecase.AutomationController) *Bindings {
	return &Bindings{automation: automation}
}

func (b *Bindings) StartAutomation(ctx context.Context) error {
	return b.automation.Start(ctx)
}

func (b *Bindings) StopAutomation(ctx context.Context) error {
	return b.automation.Stop(ctx)
}

func (b *Bindings) GetDashboard(ctx context.Context) (DashboardDTO, error) {
	select {
	case <-ctx.Done():
		return DashboardDTO{}, ctx.Err()
	default:
	}
	return DashboardDTO{
		Running:    b.automation.Running(),
		Locale:     "ru",
		LastStatus: "ready",
	}, nil
}
```

- [ ] **Step 3: Create frontend package**

Create `frontend/package.json`:

```json
{
  "name": "telegram-companion-ui",
  "private": true,
  "version": "0.1.0",
  "type": "module",
  "scripts": {
    "dev": "vite",
    "build": "tsc && vite build",
    "preview": "vite preview"
  },
  "dependencies": {
    "@vitejs/plugin-react": "latest",
    "typescript": "latest",
    "vite": "latest",
    "react": "latest",
    "react-dom": "latest",
    "lucide-react": "latest"
  },
  "devDependencies": {}
}
```

- [ ] **Step 4: Create frontend HTML**

Create `frontend/index.html`:

```html
<!doctype html>
<html lang="ru">
  <head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <title>Telegram Companion</title>
  </head>
  <body>
    <div id="root"></div>
    <script type="module" src="/src/main.tsx"></script>
  </body>
</html>
```

- [ ] **Step 5: Create i18n module**

Create `frontend/src/i18n.ts`:

```ts
export const messages = {
  ru: {
    channels: "Каналы",
    accounts: "Аккаунты",
    keywords: "Ключевые слова",
    stats: "Статистика",
    settings: "Настройки",
    start: "START",
    stop: "STOP",
    ready: "Готов",
    channelTitle: "Заголовок",
    status: "Статус",
    members: "Аккаунты",
    sent: "Отправлено",
    lastActivity: "Активность",
    active: "Активен"
  },
  en: {
    channels: "Channels",
    accounts: "Accounts",
    keywords: "Keywords",
    stats: "Stats",
    settings: "Settings",
    start: "START",
    stop: "STOP",
    ready: "Ready",
    channelTitle: "Title",
    status: "Status",
    members: "Accounts",
    sent: "Sent",
    lastActivity: "Last activity",
    active: "Active"
  }
} as const;

export type Locale = keyof typeof messages;

export function t(locale: Locale, key: keyof typeof messages.ru): string {
  return messages[locale][key];
}
```

- [ ] **Step 6: Create React app**

Create `frontend/src/App.tsx`:

```tsx
import { BarChart3, Bot, Hash, RadioTower, Settings } from "lucide-react";
import { t, type Locale } from "./i18n";
import "./styles.css";

const locale: Locale = "ru";

const channels = [
  { title: "Design Friends", link: "https://t.me/design_friends", status: "ready", members: "3/3", sent: 42, last: "12:40", active: true },
  { title: "Dev Chat", link: "https://t.me/dev_chat", status: "joining", members: "2/3", sent: 13, last: "12:21", active: true },
  { title: "Memes Lab", link: "https://t.me/memes_lab", status: "paused", members: "3/3", sent: 87, last: "11:58", active: false }
];

export default function App() {
  return (
    <main className="shell">
      <aside className="sidebar">
        <div className="brand">Telegram Companion</div>
        <nav>
          <button className="nav active"><RadioTower size={18} />{t(locale, "channels")}</button>
          <button className="nav"><Bot size={18} />{t(locale, "accounts")}</button>
          <button className="nav"><Hash size={18} />{t(locale, "keywords")}</button>
          <button className="nav"><BarChart3 size={18} />{t(locale, "stats")}</button>
          <button className="nav"><Settings size={18} />{t(locale, "settings")}</button>
        </nav>
      </aside>

      <section className="content">
        <header className="topbar">
          <div>
            <h1>{t(locale, "channels")}</h1>
            <p>Управление каналами, вступлением аккаунтов и статусом автоматизации.</p>
          </div>
          <button className="startButton">{t(locale, "start")} / {t(locale, "stop")}</button>
        </header>

        <section className="glassPanel">
          <div className="tableHeader">
            <span>{t(locale, "channelTitle")}</span>
            <span>{t(locale, "status")}</span>
            <span>{t(locale, "members")}</span>
            <span>{t(locale, "sent")}</span>
            <span>{t(locale, "lastActivity")}</span>
            <span>{t(locale, "active")}</span>
          </div>
          {channels.map((channel) => (
            <div className="tableRow" key={channel.link}>
              <div>
                <strong>{channel.title}</strong>
                <small>{channel.link}</small>
              </div>
              <span className={`status ${channel.status}`}>{channel.status}</span>
              <span>{channel.members}</span>
              <span>{channel.sent}</span>
              <span>{channel.last}</span>
              <span className={channel.active ? "toggle on" : "toggle"} />
            </div>
          ))}
        </section>
      </section>
    </main>
  );
}
```

Create `frontend/src/main.tsx`:

```tsx
import React from "react";
import ReactDOM from "react-dom/client";
import App from "./App";

ReactDOM.createRoot(document.getElementById("root") as HTMLElement).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>
);
```

- [ ] **Step 7: Create glass styles**

Create `frontend/src/styles.css`:

```css
:root {
  font-family: Inter, -apple-system, BlinkMacSystemFont, "SF Pro Display", "Segoe UI", sans-serif;
  color: #172033;
  background: #dfe8f3;
}

* { box-sizing: border-box; }
body { margin: 0; min-width: 980px; min-height: 720px; }
button { font: inherit; }

.shell {
  display: grid;
  grid-template-columns: 236px 1fr;
  min-height: 100vh;
  background:
    linear-gradient(135deg, rgba(255,255,255,.78), rgba(197,213,229,.72)),
    radial-gradient(circle at 20% 10%, rgba(108,154,196,.32), transparent 34%),
    radial-gradient(circle at 80% 0%, rgba(129,184,162,.25), transparent 28%);
}

.sidebar {
  padding: 22px 14px;
  border-right: 1px solid rgba(255,255,255,.52);
  background: rgba(255,255,255,.42);
  backdrop-filter: blur(24px);
}

.brand {
  height: 42px;
  padding: 10px 12px;
  font-weight: 700;
}

nav { display: grid; gap: 6px; margin-top: 18px; }

.nav {
  display: flex;
  align-items: center;
  gap: 10px;
  height: 38px;
  padding: 0 10px;
  border: 0;
  border-radius: 8px;
  color: #31445f;
  background: transparent;
  cursor: pointer;
}

.nav.active, .nav:hover {
  background: rgba(255,255,255,.7);
}

.content {
  padding: 28px;
}

.topbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 18px;
  margin-bottom: 22px;
}

h1 { margin: 0; font-size: 30px; letter-spacing: 0; }
p { margin: 6px 0 0; color: #5b6c82; }

.startButton {
  min-width: 150px;
  height: 44px;
  border: 1px solid rgba(255,255,255,.72);
  border-radius: 8px;
  color: white;
  background: linear-gradient(135deg, #177ddc, #26a269);
  box-shadow: 0 16px 28px rgba(23,125,220,.22);
  cursor: pointer;
}

.glassPanel {
  border: 1px solid rgba(255,255,255,.7);
  border-radius: 8px;
  background: rgba(255,255,255,.52);
  backdrop-filter: blur(26px);
  box-shadow: 0 20px 42px rgba(55,76,102,.12);
  overflow: hidden;
}

.tableHeader, .tableRow {
  display: grid;
  grid-template-columns: minmax(260px, 1.5fr) 120px 100px 110px 120px 88px;
  align-items: center;
  gap: 14px;
  padding: 12px 16px;
}

.tableHeader {
  color: #607189;
  font-size: 13px;
  border-bottom: 1px solid rgba(82,102,128,.12);
}

.tableRow {
  min-height: 62px;
  border-bottom: 1px solid rgba(82,102,128,.08);
}

.tableRow strong, .tableRow small { display: block; }
.tableRow small { margin-top: 4px; color: #6a7a91; }

.status {
  width: fit-content;
  padding: 5px 9px;
  border-radius: 999px;
  font-size: 12px;
  background: rgba(96,113,137,.14);
}

.status.ready { color: #146c43; background: rgba(38,162,105,.16); }
.status.joining { color: #8a5a00; background: rgba(245,158,11,.18); }
.status.paused { color: #586174; background: rgba(88,97,116,.14); }

.toggle {
  width: 42px;
  height: 24px;
  border-radius: 999px;
  background: rgba(88,97,116,.22);
  position: relative;
}

.toggle::after {
  content: "";
  position: absolute;
  width: 18px;
  height: 18px;
  top: 3px;
  left: 3px;
  border-radius: 50%;
  background: white;
  box-shadow: 0 2px 8px rgba(0,0,0,.18);
}

.toggle.on { background: #26a269; }
.toggle.on::after { left: 21px; }
```

- [ ] **Step 8: Run backend and frontend checks**

Run: `go test ./internal/transport/wails -v`

Expected: PASS.

Run:

```bash
cd frontend
npm install
npm run build
```

Expected: TypeScript and Vite build succeed.

- [ ] **Step 9: Commit**

```bash
git add internal/transport/wails frontend
git commit -m "feat: add localized desktop UI shell"
```

---

### Task 11: Documentation for Real Telegram Integration Handoff

**Files:**
- Create: `docs/telegram-integration-notes.md`
- Modify: `README.md`

**Interfaces:**
- Consumes: all domain ports and fake gateway.
- Produces: next-plan checklist for `gotd/td`, `tdata`, proxies, and real listeners.

- [ ] **Step 1: Create integration notes**

Create `docs/telegram-integration-notes.md`:

```markdown
# Telegram Integration Notes

## Current State

The first MVP slice uses `internal/telegram/fake` to prove application flows without real Telegram traffic.

## Real Gateway Requirements

The real gateway must implement `domain.TelegramGateway`:

- `ResolveChannel`
- `CheckMembership`
- `JoinChannel`
- `SendPublicReply`
- `SendPrivateMessage`

It must also provide listener support in the next plan through a new event source interface.

## tdata Import

The next plan must verify `gotd/td` session import support for Telegram Desktop `tdata`, especially the `session/tdesktop` package and current API shape.

The next plan must also incorporate the supplied operational guide:

- Source: `https://dark.shopping/help-center/post/vhod-v-telegram-s-pomosu-tdata`
- Use the latest Telegram Desktop Portable build for compatibility/manual checks.
- Place the provided `tdata` folder into the portable Telegram Desktop directory when manual verification is required.
- Ensure other Telegram clients for the same account are closed before verification/import.
- Check that the expected Telegram Desktop data files are present before attempting import.
- Configure the intended proxy before opening/validating the account in Telegram Desktop Portable.
- Report version mismatch or damaged `tdata` as user-facing import errors.

Importer rules:

- accept `.rar` archives and extracted folders;
- extract to temporary directory;
- import into app session store;
- delete temporary files;
- do not log session data.

## Proxy Requirements

Every real Telegram client must be constructed per account with:

- account session path;
- assigned proxy profile or direct mode;
- per-account reconnect/backoff;
- no silent fallback to direct connection when proxy mode is assigned.

## Safety Requirements

- Respect FloodWait and PeerFlood.
- Keep max 19 messages/minute and 2 seconds between outgoing messages.
- Private messages only fire from keyword events in explicit channels/groups.
```

- [ ] **Step 2: Update README with fake gateway status**

Append to `README.md`:

```markdown

## Telegram Gateway Status

The current MVP foundation uses a fake Telegram gateway. Real MTProto and `tdata` import are intentionally isolated behind `domain.TelegramGateway` and will be implemented in the next plan.
```

- [ ] **Step 3: Run full verification**

Run:

```bash
go test ./...
cd frontend && npm run build
```

Expected: all Go tests pass and frontend build succeeds.

- [ ] **Step 4: Commit**

```bash
git add docs/telegram-integration-notes.md README.md
git commit -m "docs: document telegram integration handoff"
```

---

## Self-Review Checklist

- Spec coverage:
  - Desktop app shell: Task 10.
  - Russian-first i18n: Task 10.
  - Clean Architecture skeleton: Tasks 1-3.
  - Config, logger, graceful lifecycle: Tasks 2 and 9.
  - Accounts/channels/rules/jobs/stats schema: Task 5.
  - Global keywords and public/DM jobs: Task 6.
  - Bulk channel import and all-account join behavior with fake gateway: Task 7.
  - Round-robin and rate limits: Tasks 4 and 8.
  - START/STOP: Tasks 9 and 10.
  - Proxies in model/client-factory contract: Tasks 3, 5, and 11.
  - Real MTProto/tdata: explicitly deferred to next plan after interfaces and handoff notes.
- Red-flag scan: no unresolved markers or vague generic error-handling steps.
- Type consistency:
  - Domain ports in Task 3 are consumed by Tasks 6-8.
  - `RoundRobinSelector` and `MemoryLimiter` signatures match Task 8.
  - Wails bindings use `AutomationController` from Task 9.

## Execution Choice

Plan complete and saved to `docs/superpowers/plans/2026-07-09-telegram-desktop-mvp-foundation.md`.

Two execution options:

1. **Subagent-Driven (recommended)** - dispatch a fresh subagent per task, review between tasks, fast iteration.
2. **Inline Execution** - execute tasks in this session using executing-plans, batch execution with checkpoints.

Which approach?
