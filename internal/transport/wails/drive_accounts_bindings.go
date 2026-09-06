package wails

import (
	"errors"
	"telegram-companion/internal/driveaccounts"
	"time"
)

type driveAccountsRuntime struct{ service *driveaccounts.Service }

func ConfigureDriveAccounts(b *Bindings, s *driveaccounts.Service) { b.driveAccounts.service = s }
func (b *Bindings) StartDriveAccountImport(raw string) (driveaccounts.Batch, error) {
	if err := b.runtimeError(); err != nil {
		return driveaccounts.Batch{}, err
	}
	if b.driveAccounts.service == nil {
		return driveaccounts.Batch{}, errors.New("import_unavailable")
	}
	ctx, done, err := b.beginOperation(6 * time.Hour)
	if err != nil {
		return driveaccounts.Batch{}, err
	}
	batch, err := b.driveAccounts.service.Start(ctx, raw, done)
	if err != nil {
		done()
	}
	return batch, err
}
func (b *Bindings) GetDriveAccountImportStatus() (driveaccounts.Batch, error) {
	if err := b.runtimeError(); err != nil {
		return driveaccounts.Batch{}, err
	}
	if b.driveAccounts.service == nil {
		return driveaccounts.Batch{}, errors.New("import_unavailable")
	}
	return b.driveAccounts.service.Status(), nil
}
func (b *Bindings) CancelDriveAccountImport() error {
	if err := b.runtimeError(); err != nil {
		return err
	}
	if b.driveAccounts.service == nil {
		return errors.New("import_unavailable")
	}
	b.driveAccounts.service.Cancel()
	return nil
}
