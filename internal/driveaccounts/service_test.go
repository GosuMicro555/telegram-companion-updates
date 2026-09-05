package driveaccounts

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestServiceContinuesAfterFailureAndReportsOnlySafeStatus(t *testing.T) {
	var calls atomic.Int32
	s := NewService(func(ctx context.Context, ref Reference, phase func(string)) (Result, error) {
		calls.Add(1)
		phase("checking")
		if ref.ID == "bad" {
			return Result{}, errors.New("secret URL and account details")
		}
		return Result{Added: 2}, nil
	})
	b, err := s.Start(context.Background(), "https://drive.google.com/uc?id=bad\nhttps://drive.google.com/uc?id=good", func() {})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.After(2 * time.Second)
	for {
		b = s.Status()
		if !b.Running {
			break
		}
		select {
		case <-deadline:
			t.Fatal("batch did not finish")
		case <-time.After(time.Millisecond):
		}
	}
	if calls.Load() != 2 || b.Items[0].Phase != "failed" || b.Items[0].Error != "import_failed" || b.Items[1].Added != 2 {
		t.Fatalf("wrong batch result: %+v", b)
	}
}
func TestServiceCancellationStopsQueuedAndReleasesOperation(t *testing.T) {
	started := make(chan struct{})
	done := make(chan struct{})
	s := NewService(func(ctx context.Context, ref Reference, p func(string)) (Result, error) {
		close(started)
		<-ctx.Done()
		return Result{}, ctx.Err()
	})
	_, err := s.Start(context.Background(), "https://drive.google.com/uc?id=one\nhttps://drive.google.com/uc?id=two", func() { close(done) })
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if _, err = s.Start(context.Background(), "https://drive.google.com/uc?id=three", func() {}); err == nil {
		t.Fatal("allowed simultaneous import")
	}
	s.Cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("operation was not released")
	}
	b := s.Status()
	if b.Running || b.Items[0].Phase != "cancelled" || b.Items[1].Phase != "cancelled" {
		t.Fatalf("bad cancellation: %+v", b)
	}
}
