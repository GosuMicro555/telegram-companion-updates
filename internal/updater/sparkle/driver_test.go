//go:build !public_macos_arm64 || !darwin || !arm64 || ios

package sparkle

import (
	"context"
	"errors"
	"testing"

	"telegram-companion/internal/updater"
)

func TestStubDriverIsFailClosed(t *testing.T) {
	driver := NewDriver("https://updates.example.test/appcast.xml")

	update, err := driver.Check(context.Background())
	if !errors.Is(err, updater.ErrDisabled) {
		t.Fatalf("Check() error = %v, want updater.ErrDisabled", err)
	}
	if update != (updater.Update{}) {
		t.Fatalf("Check() update = %#v, want zero value", update)
	}

	progressCalled := false
	if err := driver.Download(context.Background(), func(int) {
		progressCalled = true
	}); !errors.Is(err, updater.ErrDisabled) {
		t.Fatalf("Download() error = %v, want updater.ErrDisabled", err)
	}
	if progressCalled {
		t.Fatal("Download() called progress callback in disabled stub")
	}

	if err := driver.Install(context.Background()); !errors.Is(err, updater.ErrDisabled) {
		t.Fatalf("Install() error = %v, want updater.ErrDisabled", err)
	}
}

func TestStubDriverStopIsIdempotent(t *testing.T) {
	driver := NewDriver("https://updates.example.test/appcast.xml")
	if err := driver.Stop(); err != nil {
		t.Fatalf("first Stop() error = %v", err)
	}
	if err := driver.Stop(); err != nil {
		t.Fatalf("second Stop() error = %v", err)
	}

	if _, err := driver.Check(context.Background()); !errors.Is(err, updater.ErrDisabled) {
		t.Fatalf("Check() after Stop error = %v, want updater.ErrDisabled", err)
	}
}
