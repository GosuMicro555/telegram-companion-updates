package wails

import (
	"context"
	"strings"
	"sync"

	"telegram-companion/internal/license"
)

type activationGate interface {
	Snapshot() license.Snapshot
	Activate(string) license.Snapshot
}

type licenseImporter func(context.Context) (string, error)

type desktopRestartRequester interface {
	Request(context.Context) error
}

type successfulDesktopRestartRequester struct{}

func (successfulDesktopRestartRequester) Request(context.Context) error { return nil }

// ActivationStatusDTO is the non-secret activation state exposed to the UI.
type ActivationStatusDTO struct {
	Mode      string `json:"mode"`
	State     string `json:"state"`
	MachineID string `json:"machineID,omitempty"`
	ErrorCode string `json:"errorCode,omitempty"`
}

// ActivationBindings exposes the process-level license gate to Wails.
type ActivationBindings struct {
	gate     activationGate
	importer licenseImporter
	restart  desktopRestartRequester
	quit     func(context.Context)
	schedule func(func())

	rootMu sync.RWMutex
	root   context.Context

	restartMu        sync.Mutex
	restartCommitted bool
}

func NewActivationBindings(
	gate activationGate,
	importer licenseImporter,
	restart func(),
	schedule func(func()),
) *ActivationBindings {
	var quit func(context.Context)
	if restart != nil {
		quit = func(context.Context) { restart() }
	}
	return NewActivationBindingsWithRestartRequester(gate, importer, restartRequesterForQuit(quit), quit, schedule)
}

func NewActivationBindingsWithContextRestart(
	gate activationGate,
	importer licenseImporter,
	restart func(context.Context),
	schedule func(func()),
) *ActivationBindings {
	return NewActivationBindingsWithRestartRequester(
		gate,
		importer,
		restartRequesterForQuit(restart),
		restart,
		schedule,
	)
}

func NewActivationBindingsWithRestartRequester(
	gate activationGate,
	importer licenseImporter,
	restart desktopRestartRequester,
	quit func(context.Context),
	schedule func(func()),
) *ActivationBindings {
	if schedule == nil {
		schedule = func(callback func()) { go callback() }
	}
	return &ActivationBindings{
		gate: gate, importer: importer, restart: restart, quit: quit, schedule: schedule,
		root: context.Background(),
	}
}

func restartRequesterForQuit(quit func(context.Context)) desktopRestartRequester {
	if quit == nil {
		return nil
	}
	return successfulDesktopRestartRequester{}
}

func (b *ActivationBindings) Startup(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	b.rootMu.Lock()
	b.root = ctx
	b.rootMu.Unlock()
}

func (b *ActivationBindings) GetActivationStatus() ActivationStatusDTO {
	if b == nil || b.gate == nil {
		return ActivationStatusDTO{State: string(license.StateError), ErrorCode: "activation_unavailable"}
	}
	return activationStatusDTO(b.gate.Snapshot())
}

func (b *ActivationBindings) ActivateLicenseKey(token string) ActivationStatusDTO {
	if b == nil || b.gate == nil {
		return ActivationStatusDTO{State: string(license.StateError), ErrorCode: "activation_unavailable"}
	}
	after := b.gate.Activate(strings.TrimSpace(token))
	if failure := b.scheduleRestart(after); failure != nil {
		return *failure
	}
	return activationStatusDTO(after)
}

func (b *ActivationBindings) ImportLicenseFile() ActivationStatusDTO {
	if b == nil || b.gate == nil {
		return ActivationStatusDTO{State: string(license.StateError), ErrorCode: "activation_unavailable"}
	}
	if b.importer == nil {
		return b.importFailure()
	}
	token, err := b.importer(b.rootContext())
	if err != nil {
		return b.importFailure()
	}
	if token == "" {
		return activationStatusDTO(b.gate.Snapshot())
	}
	return b.ActivateLicenseKey(token)
}

func (b *ActivationBindings) scheduleRestart(after license.Snapshot) *ActivationStatusDTO {
	if after.State != license.StateActivated || b.restart == nil || b.quit == nil {
		return nil
	}
	b.restartMu.Lock()
	defer b.restartMu.Unlock()
	if b.restartCommitted {
		return nil
	}
	ctx := b.rootContext()
	if err := b.restart.Request(ctx); err != nil {
		failure := activationStatusDTO(after)
		failure.State = string(license.StateError)
		failure.ErrorCode = "relaunch_failed"
		return &failure
	}
	b.restartCommitted = true
	b.schedule(func() { b.quit(ctx) })
	return nil
}

func (b *ActivationBindings) importFailure() ActivationStatusDTO {
	snapshot := b.gate.Snapshot()
	snapshot.State = license.StateError
	snapshot.ErrorCode = "import_failed"
	return activationStatusDTO(snapshot)
}

func (b *ActivationBindings) rootContext() context.Context {
	b.rootMu.RLock()
	defer b.rootMu.RUnlock()
	if b.root == nil {
		return context.Background()
	}
	return b.root
}

func activationStatusDTO(snapshot license.Snapshot) ActivationStatusDTO {
	return ActivationStatusDTO{
		Mode:      string(snapshot.Mode),
		State:     string(snapshot.State),
		MachineID: snapshot.MachineID,
		ErrorCode: snapshot.ErrorCode,
	}
}
