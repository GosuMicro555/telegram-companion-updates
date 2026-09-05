package wails

import (
	"context"
	"telegram-companion/internal/driveaccounts"
	"testing"
	"time"
)

func TestDriveImportIsCancelledOnDesktopShutdown(t *testing.T) {
	b := NewBindings(nil)
	started := make(chan struct{})
	s := driveaccounts.NewService(func(ctx context.Context, r driveaccounts.Reference, p func(string)) (driveaccounts.Result, error) {
		close(started)
		<-ctx.Done()
		return driveaccounts.Result{}, ctx.Err()
	})
	ConfigureDriveAccounts(b, s)
	if _, err := b.StartDriveAccountImport("https://drive.google.com/uc?id=synthetic"); err != nil {
		t.Fatal(err)
	}
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := b.StopOperations(ctx); err != nil {
		t.Fatal(err)
	}
	if s.Status().Running {
		t.Fatal("import survived desktop shutdown")
	}
	if _, err := b.StartDriveAccountImport("https://drive.google.com/uc?id=synthetic"); err == nil {
		t.Fatal("allowed import on closed desktop")
	}
}
