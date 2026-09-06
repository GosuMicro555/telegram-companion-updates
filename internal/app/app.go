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
		automation: usecase.NewAutomationController(nil),
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
