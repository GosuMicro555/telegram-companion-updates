package wails

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"time"

	"telegram-companion/internal/updater"
)

const updateStatusEvent = "updater:status"

type updateService interface {
	Snapshot() updater.Snapshot
	Start(context.Context)
	CheckNow(context.Context) (updater.Snapshot, error)
	Download(context.Context) (updater.Snapshot, error)
	Install(context.Context) (updater.Snapshot, error)
	Stop()
}

// UpdateStatusDTO is the non-secret updater state exposed to the frontend.
type UpdateStatusDTO struct {
	State      string `json:"state"`
	RetryState string `json:"retryState,omitempty"`
	Version    string `json:"version,omitempty"`
	Progress   int    `json:"progress,omitempty"`
	ErrorCode  string `json:"errorCode,omitempty"`
}

// UpdateBindings exposes the updater without leaking native driver errors.
type UpdateBindings struct {
	service updateService
	emit    func(string, UpdateStatusDTO)

	rootMu sync.RWMutex
	root   context.Context
	stop   sync.Once
}

func NewUpdateBindings(service updateService, emit func(string, UpdateStatusDTO)) *UpdateBindings {
	if isNilUpdateService(service) {
		service = nil
	}
	return &UpdateBindings{service: service, emit: emit, root: context.Background()}
}

func isNilUpdateService(service updateService) bool {
	if service == nil {
		return true
	}
	value := reflect.ValueOf(service)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func (b *UpdateBindings) Startup(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	b.rootMu.Lock()
	b.root = ctx
	b.rootMu.Unlock()
	if b.service != nil {
		b.service.Start(ctx)
	}
}

func (b *UpdateBindings) Shutdown() {
	b.stop.Do(func() {
		if b.service != nil {
			b.service.Stop()
		}
	})
}

func (b *UpdateBindings) GetUpdateStatus() UpdateStatusDTO {
	if b == nil || b.service == nil {
		return UpdateStatusDTO{State: string(updater.StateDisabled)}
	}
	return updateStatusDTO(b.service.Snapshot())
}

func (b *UpdateBindings) CheckForUpdates() (UpdateStatusDTO, error) {
	return b.run(func(ctx context.Context) (updater.Snapshot, error) {
		return b.service.CheckNow(ctx)
	})
}

func (b *UpdateBindings) DownloadUpdate() (UpdateStatusDTO, error) {
	return b.run(func(ctx context.Context) (updater.Snapshot, error) {
		return b.service.Download(ctx)
	})
}

func (b *UpdateBindings) RestartAndInstallUpdate() (UpdateStatusDTO, error) {
	return b.run(func(ctx context.Context) (updater.Snapshot, error) {
		return b.service.Install(ctx)
	})
}

func (b *UpdateBindings) run(operation func(context.Context) (updater.Snapshot, error)) (UpdateStatusDTO, error) {
	if b == nil || b.service == nil || operation == nil {
		return UpdateStatusDTO{State: string(updater.StateDisabled)}, errors.New("updates are disabled")
	}
	ctx, cancel := context.WithTimeout(b.rootContext(), 10*time.Minute)
	defer cancel()
	snapshot, err := operation(ctx)
	status := updateStatusDTO(snapshot)
	b.publish(status)
	if err != nil {
		return status, safeUpdateError(err)
	}
	if snapshot.State == updater.StateError {
		return status, errors.New("update operation failed")
	}
	return status, nil
}

func (b *UpdateBindings) rootContext() context.Context {
	b.rootMu.RLock()
	defer b.rootMu.RUnlock()
	if b.root == nil {
		return context.Background()
	}
	return b.root
}

func (b *UpdateBindings) publish(status UpdateStatusDTO) {
	if b.emit != nil {
		b.emit(updateStatusEvent, status)
	}
}

func updateStatusDTO(snapshot updater.Snapshot) UpdateStatusDTO {
	status := UpdateStatusDTO{
		State:      string(snapshot.State),
		RetryState: string(snapshot.RetryState),
		Version:    snapshot.Version,
		Progress:   snapshot.Progress,
	}
	if snapshot.State == updater.StateError {
		status.ErrorCode = safeUpdateErrorCode(snapshot.ErrorCode)
		if status.ErrorCode == "" && snapshot.Error != "" {
			status.ErrorCode = "update_failed"
		}
	}
	return status
}

func safeUpdateErrorCode(code string) string {
	switch strings.TrimSpace(code) {
	case string(updater.CodeSignatureInvalid), string(updater.CodeValidationFailed),
		string(updater.CodeRunningFromDiskImage), string(updater.CodeInstallFailed):
		return strings.TrimSpace(code)
	default:
		return ""
	}
}

func safeUpdateError(err error) error {
	switch {
	case errors.Is(err, updater.ErrDisabled):
		return errors.New("updates are disabled")
	case errors.Is(err, updater.ErrStopped):
		return errors.New("updater is stopped")
	case errors.Is(err, updater.ErrBusy):
		return errors.New("update operation is already in progress")
	case errors.Is(err, updater.ErrInvalidState):
		return errors.New("update operation is not available")
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return errors.New("update operation was canceled")
	default:
		return errors.New("update operation failed")
	}
}
