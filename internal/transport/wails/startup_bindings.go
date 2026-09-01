package wails

import (
	"context"
	"strings"
	"sync"

	"telegram-companion/internal/license"
)

var desktopStartupModes = map[string]struct{}{
	"activation": {},
	"recovery":   {},
	"workspace":  {},
}

var desktopRecoveryCodes = map[string]struct{}{
	"seed_license_required": {},
	"seed_unavailable":      {},
	"seed_incompatible":     {},
	"keychain_unavailable":  {},
	"insufficient_space":    {},
	"storage_unavailable":   {},
	"profile_blocked":       {},
	"runtime_unavailable":   {},
	"relaunch_failed":       {},
}

// DesktopStartupStatusDTO is the closed, non-secret process startup state.
type DesktopStartupStatusDTO struct {
	Mode       string `json:"mode"`
	ErrorCode  string `json:"errorCode,omitempty"`
	Restarting bool   `json:"restarting,omitempty"`
}

// StartupBindings stays bound in every desktop mode. Recovery actions can
// only request a fresh process; they never construct a workspace in-place.
type StartupBindings struct {
	gate     activationGate
	importer licenseImporter
	restart  desktopRestartRequester
	quit     func(context.Context)
	schedule func(func())

	rootMu sync.RWMutex
	root   context.Context

	stateMu          sync.RWMutex
	status           DesktopStartupStatusDTO
	actionInProgress bool
}

func NewStartupBindings(
	mode string,
	errorCode string,
	gate activationGate,
	importer licenseImporter,
	restart desktopRestartRequester,
	quit func(context.Context),
	schedule func(func()),
) *StartupBindings {
	if schedule == nil {
		schedule = func(callback func()) { go callback() }
	}
	return &StartupBindings{
		gate: gate, importer: importer, restart: restart, quit: quit, schedule: schedule,
		root: context.Background(), status: safeDesktopStartupStatus(mode, errorCode),
	}
}

func safeDesktopStartupStatus(mode, errorCode string) DesktopStartupStatusDTO {
	if _, ok := desktopStartupModes[mode]; !ok {
		return DesktopStartupStatusDTO{Mode: "recovery", ErrorCode: "runtime_unavailable"}
	}
	status := DesktopStartupStatusDTO{Mode: mode}
	if mode != "recovery" {
		return status
	}
	if _, ok := desktopRecoveryCodes[errorCode]; !ok {
		errorCode = "runtime_unavailable"
	}
	status.ErrorCode = errorCode
	return status
}

func (b *StartupBindings) Startup(ctx context.Context) {
	if b == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	b.rootMu.Lock()
	b.root = ctx
	b.rootMu.Unlock()
}

func (b *StartupBindings) GetDesktopStartupStatus() DesktopStartupStatusDTO {
	if b == nil {
		return DesktopStartupStatusDTO{Mode: "recovery", ErrorCode: "runtime_unavailable"}
	}
	return b.statusSnapshot()
}

func (b *StartupBindings) RetryStartup() DesktopStartupStatusDTO {
	if b == nil {
		return DesktopStartupStatusDTO{Mode: "recovery", ErrorCode: "runtime_unavailable"}
	}
	status, started := b.beginAction()
	if !started {
		return status
	}
	finished := false
	defer func() {
		if !finished {
			b.finishAction(nil)
		}
	}()
	status = b.requestRestart()
	finished = true
	return status
}

func (b *StartupBindings) ChooseAnotherLicense() DesktopStartupStatusDTO {
	if b == nil {
		return DesktopStartupStatusDTO{Mode: "recovery", ErrorCode: "runtime_unavailable"}
	}
	status, started := b.beginAction()
	if !started {
		return status
	}
	finished := false
	defer func() {
		if !finished {
			b.finishAction(nil)
		}
	}()
	if b.gate == nil || b.importer == nil {
		status = b.finishAction(nil)
		finished = true
		return status
	}
	token, err := b.importer(b.rootContext())
	if err != nil {
		status = b.finishAction(nil)
		finished = true
		return status
	}
	token = strings.TrimSpace(token)
	if token == "" {
		status = b.finishAction(nil)
		finished = true
		return status
	}
	after := b.gate.Activate(token)
	if after.State != license.StateActivated {
		status = b.finishAction(nil)
		finished = true
		return status
	}
	status = b.requestRestart()
	finished = true
	return status
}

func (b *StartupBindings) requestRestart() DesktopStartupStatusDTO {
	if b.restart == nil || b.quit == nil {
		return b.finishAction(func(status *DesktopStartupStatusDTO) {
			status.ErrorCode = "relaunch_failed"
		})
	}
	ctx := b.rootContext()
	if err := b.restart.Request(ctx); err != nil {
		return b.finishAction(func(status *DesktopStartupStatusDTO) {
			status.ErrorCode = "relaunch_failed"
		})
	}
	status := b.finishAction(func(status *DesktopStartupStatusDTO) {
		status.Restarting = true
	})
	b.schedule(func() { b.quit(ctx) })
	return status
}

func (b *StartupBindings) statusSnapshot() DesktopStartupStatusDTO {
	b.stateMu.RLock()
	defer b.stateMu.RUnlock()
	return b.status
}

func (b *StartupBindings) beginAction() (DesktopStartupStatusDTO, bool) {
	b.stateMu.Lock()
	defer b.stateMu.Unlock()
	if b.status.Mode != "recovery" || b.status.Restarting || b.actionInProgress {
		return b.status, false
	}
	b.actionInProgress = true
	return b.status, true
}

func (b *StartupBindings) finishAction(update func(*DesktopStartupStatusDTO)) DesktopStartupStatusDTO {
	b.stateMu.Lock()
	defer b.stateMu.Unlock()
	if update != nil {
		update(&b.status)
	}
	b.actionInProgress = false
	return b.status
}

func (b *StartupBindings) rootContext() context.Context {
	b.rootMu.RLock()
	defer b.rootMu.RUnlock()
	if b.root == nil {
		return context.Background()
	}
	return b.root
}
